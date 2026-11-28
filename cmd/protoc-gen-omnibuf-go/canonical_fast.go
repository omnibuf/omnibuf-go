package main

import (
	"fmt"
	"strings"

	"github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func isCanonicalFastMessage(orderedFields []*protogen.Field) bool {
	if len(orderedFields) == 0 || len(orderedFields) > 32 {
		return false
	}
	for _, f := range orderedFields {
		if f.Desc.IsMap() || f.Oneof != nil {
			return false
		}
		if f.Desc.Number() < 1 || f.Desc.Number() > 1023 {
			return false
		}
		if f.Desc.IsList() {
			switch f.Desc.Kind() {
			case protoreflect.StringKind,
				protoreflect.Uint32Kind,
				protoreflect.Uint64Kind,
				protoreflect.MessageKind:
			default:
				return false
			}
			continue
		}
		switch f.Desc.Kind() {
		case protoreflect.BoolKind,
			protoreflect.Uint32Kind, protoreflect.Uint64Kind,
			protoreflect.Int32Kind, protoreflect.Sint32Kind,
			protoreflect.Int64Kind, protoreflect.Sint64Kind,
			protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind,
			protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind,
			protoreflect.FloatKind, protoreflect.DoubleKind,
			protoreflect.StringKind, protoreflect.BytesKind,
			protoreflect.MessageKind:
		default:
			return false
		}
	}
	return true
}

func canonicalMinFieldSize(f *protogen.Field) int {
	if !f.Desc.IsList() && f.Desc.Kind() != protoreflect.MessageKind {
		return flatMinFieldSize(f)
	}
	fnum := uint16(f.Desc.Number())
	if f.Desc.IsList() {
		// tag + container byteLen varint (>=1) + elem wireType (1) + count varint (>=1)
		return len(flatTagBytes(fnum, wire.WireArray)) + 3
	}
	// Singular submessage: tag + byteLen varint (>=1)
	return len(flatTagBytes(fnum, wire.WireSubMsg)) + 1
}

// emitCanonicalFastUnmarshal emits a two-phase canonical-order UnmarshalOmniBuf for
// structured messages (scalars + supported arrays + submessages):
//   - Phase 1 validates all top-level tags, container headers, counts, bounds, exact EOF,
//     and 7-bit ASCII on singular strings and string arrays without mutating x. Any
//     mismatch, omission, extra/out-of-order field, Field #0, or non-ASCII byte falls
//     back cleanly to decodeOmniBuf with x untouched.
//   - Phase 2 resets proto state once and commits scalars, tight array loops, and
//     submessages (routing flat-scalar children directly through unmarshalFlatFast).
func emitCanonicalFastUnmarshal(g *protogen.GeneratedFile, msg *protogen.Message, orderedFields []*protogen.Field) {
	typeName := msg.GoIdent.GoName

	g.P("// UnmarshalOmniBuf decodes data into ", typeName, " using generated canonical fast path with switch fallback.")
	g.P("func (x *", typeName, ") UnmarshalOmniBuf(data []byte, zeroCopy bool) error {")
	g.P("	if x == nil {")
	g.P(`		return fmt.Errorf("omnibuf: Unmarshal requires non-nil pointer")`)
	g.P("	}")

	remMin := make([]int, len(orderedFields))
	running := 0
	for i := len(orderedFields) - 1; i >= 0; i-- {
		remMin[i] = running
		running += canonicalMinFieldSize(orderedFields[i])
	}
	totalMin := running

	g.P("	b := data")
	g.P("	if len(b) < ", totalMin, " {")
	g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
	g.P("	}")

	type fieldRef struct {
		idx   int
		field *protogen.Field
	}
	var asciiSlices []string
	var strFields []fieldRef
	var bytesFields []fieldRef
	var scalarAssigns []string
	var complexFields []fieldRef

	// Phase 1: Pure read-only validation and container slicing.
	for i, f := range orderedFields {
		fnum := uint16(f.Desc.Number())
		rem := remMin[i]

		if !f.Desc.IsList() && f.Desc.Kind() == protoreflect.BoolKind {
			tb := flatTagBytes(fnum, wire.WireBoolFalse)
			if len(tb) == 1 {
				g.P(fmt.Sprintf("	if (b[0]&^2) != 0x%02x {", tb[0]))
				g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
				g.P("	}")
				g.P("	f_", i, " := (b[0] & 2) != 0")
				g.P("	b = b[1:]")
			} else {
				g.P(fmt.Sprintf("	if (b[0]&^4) != 0x%02x || b[1] != 0x%02x {", tb[0], tb[1]))
				g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
				g.P("	}")
				g.P("	f_", i, " := (b[0] & 4) != 0")
				g.P("	b = b[2:]")
			}
			scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
			continue
		}

		tb := flatTagBytes(fnum, wireTypeForField(f))
		var tagConds []string
		for ti, bByte := range tb {
			tagConds = append(tagConds, fmt.Sprintf("b[%d] != 0x%02x", ti, bByte))
		}
		g.P("	if ", strings.Join(tagConds, " || "), " {")
		g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
		g.P("	}")
		g.P("	b = b[", len(tb), ":]")

		if f.Desc.IsList() {
			g.P("	aLen_", i, ", an_", i, " := wire.DecodeVarint(b)")
			if rem > 0 {
				g.P("	if an_", i, " == 0 || len(b)-an_", i, " < ", rem, " || aLen_", i, " > uint64(len(b)-an_", i, "-", rem, ") {")
			} else {
				g.P("	if an_", i, " == 0 || aLen_", i, " > uint64(len(b)-an_", i, ") {")
			}
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
			g.P("	end_", i, " := an_", i, " + int(aLen_", i, ")")
			g.P("	body_", i, " := b[an_", i, ":end_", i, "]")
			g.P("	b = b[end_", i, ":]")

			var elemWT string
			switch f.Desc.Kind() {
			case protoreflect.StringKind:
				elemWT = "wire.WireString"
			case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
				elemWT = "wire.WireUint"
			case protoreflect.MessageKind:
				elemWT = "wire.WireSubMsg"
			}
			g.P("	if len(body_", i, ") < 2 || body_", i, "[0] != ", elemWT, " {")
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
			g.P("	cu_", i, ", cn_", i, " := wire.DecodeVarint(body_", i, "[1:])")
			g.P("	if cn_", i, " == 0 || cu_", i, " > uint64(len(body_", i, ")-(1+cn_", i, ")) {")
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
			if f.Desc.Kind() == protoreflect.StringKind {
				asciiSlices = append(asciiSlices, fmt.Sprintf("body_%d", i))
			}
			complexFields = append(complexFields, fieldRef{idx: i, field: f})
			continue
		}

		switch f.Desc.Kind() {
		case protoreflect.Uint32Kind, protoreflect.Uint64Kind,
			protoreflect.Int32Kind, protoreflect.Sint32Kind,
			protoreflect.Int64Kind, protoreflect.Sint64Kind:
			g.P("	v_", i, ", vn_", i, " := wire.DecodeVarint(b)")
			if rem > 0 {
				g.P("	if vn_", i, " == 0 || len(b)-vn_", i, " < ", rem, " {")
			} else {
				g.P("	if vn_", i, " == 0 {")
			}
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
			g.P("	b = b[vn_", i, ":]")
			switch f.Desc.Kind() {
			case protoreflect.Uint32Kind:
				scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = uint32(v_%d)", f.GoName, i))
			case protoreflect.Uint64Kind:
				scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = v_%d", f.GoName, i))
			case protoreflect.Int32Kind, protoreflect.Sint32Kind:
				scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = int32(wire.DecodeZigZag(v_%d))", f.GoName, i))
			case protoreflect.Int64Kind, protoreflect.Sint64Kind:
				scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = wire.DecodeZigZag(v_%d)", f.GoName, i))
			}
		case protoreflect.Fixed32Kind:
			g.P("	f_", i, " := binary.LittleEndian.Uint32(b)")
			g.P("	b = b[4:]")
			scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.Sfixed32Kind:
			g.P("	f_", i, " := int32(binary.LittleEndian.Uint32(b))")
			g.P("	b = b[4:]")
			scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.FloatKind:
			g.P("	f_", i, " := math.Float32frombits(binary.LittleEndian.Uint32(b))")
			g.P("	b = b[4:]")
			scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.Fixed64Kind:
			g.P("	f_", i, " := binary.LittleEndian.Uint64(b)")
			g.P("	b = b[8:]")
			scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.Sfixed64Kind:
			g.P("	f_", i, " := int64(binary.LittleEndian.Uint64(b))")
			g.P("	b = b[8:]")
			scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.DoubleKind:
			g.P("	f_", i, " := math.Float64frombits(binary.LittleEndian.Uint64(b))")
			g.P("	b = b[8:]")
			scalarAssigns = append(scalarAssigns, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.StringKind:
			g.P("	sLen_", i, ", sn_", i, " := wire.DecodeVarint(b)")
			if rem > 0 {
				g.P("	if sn_", i, " == 0 || len(b)-sn_", i, " < ", rem, " || sLen_", i, " > uint64(len(b)-sn_", i, "-", rem, ") {")
			} else {
				g.P("	if sn_", i, " == 0 || sLen_", i, " > uint64(len(b)-sn_", i, ") {")
			}
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
			g.P("	end_", i, " := sn_", i, " + int(sLen_", i, ")")
			g.P("	s_", i, " := b[sn_", i, ":end_", i, "]")
			g.P("	b = b[end_", i, ":]")
			asciiSlices = append(asciiSlices, fmt.Sprintf("s_%d", i))
			strFields = append(strFields, fieldRef{idx: i, field: f})
		case protoreflect.BytesKind:
			g.P("	bLen_", i, ", bn_", i, " := wire.DecodeVarint(b)")
			if rem > 0 {
				g.P("	if bn_", i, " == 0 || len(b)-bn_", i, " < ", rem, " || bLen_", i, " > uint64(len(b)-bn_", i, "-", rem, ") {")
			} else {
				g.P("	if bn_", i, " == 0 || bLen_", i, " > uint64(len(b)-bn_", i, ") {")
			}
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
			g.P("	end_", i, " := bn_", i, " + int(bLen_", i, ")")
			g.P("	b_", i, " := b[bn_", i, ":end_", i, ":end_", i, "]")
			g.P("	b = b[end_", i, ":]")
			bytesFields = append(bytesFields, fieldRef{idx: i, field: f})
		case protoreflect.MessageKind:
			g.P("	subLen_", i, ", sn_", i, " := wire.DecodeVarint(b)")
			if rem > 0 {
				g.P("	if sn_", i, " == 0 || len(b)-sn_", i, " < ", rem, " || subLen_", i, " > uint64(len(b)-sn_", i, "-", rem, ") {")
			} else {
				g.P("	if sn_", i, " == 0 || subLen_", i, " > uint64(len(b)-sn_", i, ") {")
			}
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
			g.P("	end_", i, " := sn_", i, " + int(subLen_", i, ")")
			g.P("	sub_", i, " := b[sn_", i, ":end_", i, "]")
			g.P("	b = b[end_", i, ":]")
			complexFields = append(complexFields, fieldRef{idx: i, field: f})
		}
	}

	g.P("	if len(b) != 0 {")
	g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
	g.P("	}")

	if len(asciiSlices) > 0 {
		var checks []string
		for _, sl := range asciiSlices {
			checks = append(checks, fmt.Sprintf("!wire.IsASCII(%s)", sl))
		}
		g.P("	if ", strings.Join(checks, " || "), " {")
		g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
		g.P("	}")
	}

	// Phase 2: Commit into x.
	g.P("	_omnibufProtoState_", typeName, ".Reset(unsafe.Pointer(x), false)")
	for _, stmt := range scalarAssigns {
		g.P("	", stmt)
	}

	if len(strFields) >= 2 {
		var lenTerms []string
		for _, sf := range strFields {
			lenTerms = append(lenTerms, fmt.Sprintf("len(s_%d)", sf.idx))
		}
		g.P("	if zeroCopy {")
		for _, sf := range strFields {
			g.P("		x.", sf.field.GoName, " = wire.DecodeStr(s_", sf.idx, ", true)")
		}
		g.P("	} else if totalStr := ", strings.Join(lenTerms, " + "), "; totalStr > 0 && totalStr <= 256 {")
		g.P("		sb := make([]byte, totalStr)")
		g.P("		strOff := 0")
		for _, sf := range strFields {
			g.P("		strOff = wire.CopyBytesAt(sb, strOff, s_", sf.idx, ")")
		}
		g.P("		all := unsafe.String(&sb[0], totalStr)")
		g.P("		strOff = 0")
		for _, sf := range strFields {
			g.P("		if l := len(s_", sf.idx, "); l > 0 {")
			g.P("			x.", sf.field.GoName, " = all[strOff : strOff+l]")
			g.P("			strOff += l")
			g.P("		} else {")
			g.P(`			x.`, sf.field.GoName, ` = ""`)
			g.P("		}")
		}
		g.P("	} else {")
		for _, sf := range strFields {
			g.P("		x.", sf.field.GoName, " = string(s_", sf.idx, ")")
		}
		g.P("	}")
	} else if len(strFields) == 1 {
		sf := strFields[0]
		g.P("	x.", sf.field.GoName, " = wire.DecodeStr(s_", sf.idx, ", zeroCopy)")
	}

	if len(bytesFields) > 0 {
		g.P("	if zeroCopy {")
		for _, bf := range bytesFields {
			g.P("		x.", bf.field.GoName, " = b_", bf.idx)
		}
		g.P("	} else {")
		for _, bf := range bytesFields {
			g.P("		x.", bf.field.GoName, " = append([]byte(nil), b_", bf.idx, "...)")
		}
		g.P("	}")
	}

	for _, cf := range complexFields {
		i := cf.idx
		f := cf.field
		if f.Desc.IsList() {
			switch f.Desc.Kind() {
			case protoreflect.StringKind:
				g.P("	{")
				g.P("		body := body_", i)
				g.P("		count := int(cu_", i, ")")
				g.P("		p := 1 + cn_", i)
				g.P("		bLen := len(body)")
				g.P("		oldSlice := x.", f.GoName)
				g.P("		oldLen := len(oldSlice)")
				g.P("		reused := false")
				g.P("		if cap(oldSlice) < count {")
				g.P("			x.", f.GoName, " = make([]string, count)")
				g.P("		} else {")
				g.P("			x.", f.GoName, " = oldSlice[:count]")
				g.P("			reused = true")
				g.P("		}")
				g.P("		dst := x.", f.GoName)
				g.P("		if zeroCopy {")
				g.P("			for i := 0; i < count; i++ {")
				g.P("				if p >= bLen {")
				g.P("					if reused {")
				g.P("						clear(oldSlice[:max(oldLen, count)])")
				g.P("						x.", f.GoName, " = oldSlice[:0]")
				g.P("					}")
				g.P(`					return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("				}")
				g.P("				var l int")
				g.P("				if lb := body[p]; (lb & 1) != 0 {")
				g.P("					l = int(lb >> 1)")
				g.P("					p++")
				g.P("				} else {")
				g.P("					lu, ln := wire.DecodeVarint(body[p:])")
				g.P("					if ln == 0 || lu > uint64(bLen-(p+ln)) {")
				g.P("						if reused {")
				g.P("							clear(oldSlice[:max(oldLen, count)])")
				g.P("							x.", f.GoName, " = oldSlice[:0]")
				g.P("						}")
				g.P(`						return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("					}")
				g.P("					l = int(lu)")
				g.P("					p += ln")
				g.P("				}")
				g.P("				if l > bLen-p {")
				g.P("					if reused {")
				g.P("						clear(oldSlice[:max(oldLen, count)])")
				g.P("						x.", f.GoName, " = oldSlice[:0]")
				g.P("					}")
				g.P(`					return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("				}")
				g.P("				if l > 0 {")
				g.P("					dst[i] = unsafe.String(&body[p], l)")
				g.P("					p += l")
				g.P("				} else {")
				g.P(`					dst[i] = ""`)
				g.P("				}")
				g.P("			}")
				g.P("		} else if remBytes := bLen - p; count >= 2 && remBytes > 0 && remBytes <= 256 {")
				g.P("			sb := make([]byte, remBytes)")
				g.P("			strOff := 0")
				g.P("			all := unsafe.String(&sb[0], remBytes)")
				g.P("			for i := 0; i < count; i++ {")
				g.P("				if p >= bLen {")
				g.P("					if reused {")
				g.P("						clear(oldSlice[:max(oldLen, count)])")
				g.P("						x.", f.GoName, " = oldSlice[:0]")
				g.P("					}")
				g.P(`					return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("				}")
				g.P("				var l int")
				g.P("				if lb := body[p]; (lb & 1) != 0 {")
				g.P("					l = int(lb >> 1)")
				g.P("					p++")
				g.P("				} else {")
				g.P("					lu, ln := wire.DecodeVarint(body[p:])")
				g.P("					if ln == 0 || lu > uint64(bLen-(p+ln)) {")
				g.P("						if reused {")
				g.P("							clear(oldSlice[:max(oldLen, count)])")
				g.P("							x.", f.GoName, " = oldSlice[:0]")
				g.P("						}")
				g.P(`						return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("					}")
				g.P("					l = int(lu)")
				g.P("					p += ln")
				g.P("				}")
				g.P("				if l > bLen-p {")
				g.P("					if reused {")
				g.P("						clear(oldSlice[:max(oldLen, count)])")
				g.P("						x.", f.GoName, " = oldSlice[:0]")
				g.P("					}")
				g.P(`					return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("				}")
				g.P("				if l > 0 {")
				g.P("					nextOff := wire.CopyBytesAt(sb, strOff, body[p:p+l])")
				g.P("					dst[i] = all[strOff:nextOff]")
				g.P("					strOff = nextOff")
				g.P("					p += l")
				g.P("				} else {")
				g.P(`					dst[i] = ""`)
				g.P("				}")
				g.P("			}")
				g.P("		} else {")
				g.P("			for i := 0; i < count; i++ {")
				g.P("				if p >= bLen {")
				g.P("					if reused {")
				g.P("						clear(oldSlice[:max(oldLen, count)])")
				g.P("						x.", f.GoName, " = oldSlice[:0]")
				g.P("					}")
				g.P(`					return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("				}")
				g.P("				var l int")
				g.P("				if lb := body[p]; (lb & 1) != 0 {")
				g.P("					l = int(lb >> 1)")
				g.P("					p++")
				g.P("				} else {")
				g.P("					lu, ln := wire.DecodeVarint(body[p:])")
				g.P("					if ln == 0 || lu > uint64(bLen-(p+ln)) {")
				g.P("						if reused {")
				g.P("							clear(oldSlice[:max(oldLen, count)])")
				g.P("							x.", f.GoName, " = oldSlice[:0]")
				g.P("						}")
				g.P(`						return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("					}")
				g.P("					l = int(lu)")
				g.P("					p += ln")
				g.P("				}")
				g.P("				if l > bLen-p {")
				g.P("					if reused {")
				g.P("						clear(oldSlice[:max(oldLen, count)])")
				g.P("						x.", f.GoName, " = oldSlice[:0]")
				g.P("					}")
				g.P(`					return fmt.Errorf("omnibuf: invalid string element")`)
				g.P("				}")
				g.P("				dst[i] = string(body[p : p+l])")
				g.P("				p += l")
				g.P("			}")
				g.P("		}")
				g.P("		if p != bLen {")
				g.P("			if reused {")
				g.P("				clear(oldSlice[:max(oldLen, count)])")
				g.P("				x.", f.GoName, " = oldSlice[:0]")
				g.P("			}")
				g.P(`			return fmt.Errorf("omnibuf: string array trailing bytes")`)
				g.P("		}")
				g.P("		if reused && count < oldLen {")
				g.P("			clear(oldSlice[count:oldLen])")
				g.P("		}")
				g.P("	}")

			case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
				goElem := "uint32"
				errName := "uint32"
				if f.Desc.Kind() == protoreflect.Uint64Kind {
					goElem = "uint64"
					errName = "uint64"
				}
				g.P("	{")
				g.P("		body := body_", i)
				g.P("		count := int(cu_", i, ")")
				g.P("		p := 1 + cn_", i)
				g.P("		bLen := len(body)")
				g.P("		if cap(x.", f.GoName, ") < count {")
				g.P("			x.", f.GoName, " = make([]", goElem, ", count)")
				g.P("		} else {")
				g.P("			x.", f.GoName, " = x.", f.GoName, "[:count]")
				g.P("		}")
				g.P("		dst := x.", f.GoName)
				g.P("		for i := 0; i < count; i++ {")
				g.P("			if p >= bLen {")
				g.P(fmt.Sprintf(`				return fmt.Errorf("omnibuf: invalid %s element")`, errName))
				g.P("			}")
				g.P("			b0 := body[p]")
				g.P("			if (b0 & 1) != 0 {")
				g.P("				dst[i] = ", goElem, "(b0 >> 1)")
				g.P("				p++")
				g.P("			} else if (b0 & 2) != 0 {")
				g.P("				if p+2 > bLen {")
				g.P(fmt.Sprintf(`					return fmt.Errorf("omnibuf: invalid %s element")`, errName))
				g.P("				}")
				g.P("				dst[i] = ", goElem, "(binary.LittleEndian.Uint16(body[p:]) >> 2)")
				g.P("				p += 2")
				g.P("			} else if (b0 & 4) != 0 {")
				g.P("				if p+4 > bLen {")
				g.P(fmt.Sprintf(`					return fmt.Errorf("omnibuf: invalid %s element")`, errName))
				g.P("				}")
				g.P("				dst[i] = ", goElem, "(binary.LittleEndian.Uint32(body[p:]) >> 3)")
				g.P("				p += 4")
				g.P("			} else if b0 == 0 && p+9 <= bLen {")
				g.P("				dst[i] = ", goElem, "(binary.LittleEndian.Uint64(body[p+1:]))")
				g.P("				p += 9")
				g.P("			} else {")
				g.P(fmt.Sprintf(`				return fmt.Errorf("omnibuf: invalid %s element")`, errName))
				g.P("			}")
				g.P("		}")
				g.P("		if p != bLen {")
				g.P(fmt.Sprintf(`			return fmt.Errorf("omnibuf: %s array trailing bytes")`, errName))
				g.P("		}")
				g.P("	}")

			case protoreflect.MessageKind:
				elemType := f.Message.GoIdent.GoName
				childFlat := isSamePkgFlatScalar(msg, f.Message)
				g.P("	{")
				g.P("		body := body_", i)
				g.P("		count := int(cu_", i, ")")
				g.P("		p := 1 + cn_", i)
				g.P("		bLen := len(body)")
				g.P("		oldSlice := x.", f.GoName)
				g.P("		oldLen := len(oldSlice)")
				g.P("		reused := false")
				g.P("		if cap(oldSlice) < count {")
				g.P("			if _, ok := wire.CountSubMsgArrElems(body); !ok {")
				g.P(`				return fmt.Errorf("omnibuf: invalid submsg array")`)
				g.P("			}")
				g.P("			slab := make([]", elemType, ", count)")
				g.P("			next := make([]*", elemType, ", count)")
				g.P("			for i := 0; i < count; i++ {")
				g.P("				next[i] = &slab[i]")
				g.P("			}")
				g.P("			x.", f.GoName, " = next")
				g.P("		} else {")
				g.P("			x.", f.GoName, " = oldSlice[:count]")
				g.P("			reused = true")
				g.P("		}")
				g.P("		dst := x.", f.GoName)
				g.P("		for i := 0; i < count; i++ {")
				g.P("			var el int")
				g.P("			if p < bLen && (body[p]&1) != 0 {")
				g.P("				el = int(body[p] >> 1)")
				g.P("				p++")
				g.P("			} else {")
				g.P("				elu, en := wire.DecodeVarint(body[p:])")
				g.P("				if en == 0 || elu > uint64(bLen-(p+en)) {")
				g.P("					if reused {")
				g.P("						clear(oldSlice[:max(oldLen, count)])")
				g.P("						x.", f.GoName, " = oldSlice[:0]")
				g.P("					}")
				g.P(`					return fmt.Errorf("omnibuf: invalid submsg elem length")`)
				g.P("				}")
				g.P("				el = int(elu)")
				g.P("				p += en")
				g.P("			}")
				g.P("			if el > bLen-p {")
				g.P("				if reused {")
				g.P("					clear(oldSlice[:max(oldLen, count)])")
				g.P("					x.", f.GoName, " = oldSlice[:0]")
				g.P("				}")
				g.P(`				return fmt.Errorf("omnibuf: invalid submsg elem length")`)
				g.P("			}")
				g.P("			elem := dst[i]")
				g.P("			if elem == nil {")
				g.P("				elem = new(", elemType, ")")
				g.P("				dst[i] = elem")
				g.P("			}")
				if childFlat {
					g.P("			if !elem.unmarshalFlatFast(body[p:p+el], zeroCopy) {")
					g.P("				if err := elem.decodeOmniBuf(body[p:p+el], zeroCopy, 2, false); err != nil {")
					g.P("					if reused {")
					g.P("						clear(oldSlice[:max(oldLen, count)])")
					g.P("						x.", f.GoName, " = oldSlice[:0]")
					g.P("					}")
					g.P("					return err")
					g.P("				}")
					g.P("			}")
				} else {
					g.P("			if err := elem.decodeOmniBuf(body[p:p+el], zeroCopy, 2, false); err != nil {")
					g.P("				if reused {")
					g.P("					clear(oldSlice[:max(oldLen, count)])")
					g.P("					x.", f.GoName, " = oldSlice[:0]")
					g.P("				}")
					g.P("				return err")
					g.P("			}")
				}
				g.P("			p += el")
				g.P("		}")
				g.P("		if p != bLen {")
				g.P("			if reused {")
				g.P("				clear(oldSlice[:max(oldLen, count)])")
				g.P("				x.", f.GoName, " = oldSlice[:0]")
				g.P("			}")
				g.P(`			return fmt.Errorf("omnibuf: submsg array trailing bytes")`)
				g.P("		}")
				g.P("		if reused && count < oldLen {")
				g.P("			clear(oldSlice[count:oldLen])")
				g.P("		}")
				g.P("	}")
			}
			continue
		}

		// Singular submessage
		subType := f.Message.GoIdent.GoName
		g.P("	if x.", f.GoName, " == nil {")
		g.P("		x.", f.GoName, " = new(", subType, ")")
		g.P("	}")
		if isSamePkgFlatScalar(msg, f.Message) {
			g.P("	if !x.", f.GoName, ".unmarshalFlatFast(sub_", i, ", zeroCopy) {")
			g.P("		if err := x.", f.GoName, ".decodeOmniBuf(sub_", i, ", zeroCopy, 1, false); err != nil {")
			g.P("			return err")
			g.P("		}")
			g.P("	}")
		} else {
			g.P("	if err := x.", f.GoName, ".decodeOmniBuf(sub_", i, ", zeroCopy, 1, false); err != nil {")
			g.P("		return err")
			g.P("	}")
		}
	}

	g.P("	return nil")
	g.P("}")
	g.P()
}
