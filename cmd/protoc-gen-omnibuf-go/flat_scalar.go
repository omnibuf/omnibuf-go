package main

import (
	"fmt"
	"strings"

	"github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func isFlatScalarMessage(orderedFields []*protogen.Field) bool {
	if len(orderedFields) == 0 || len(orderedFields) > 32 {
		return false
	}
	for _, f := range orderedFields {
		if f.Desc.IsMap() || f.Desc.IsList() {
			return false
		}
		if f.Desc.Number() < 1 || f.Desc.Number() > 1023 {
			return false
		}
		switch f.Desc.Kind() {
		case protoreflect.BoolKind,
			protoreflect.Uint32Kind, protoreflect.Uint64Kind,
			protoreflect.Int32Kind, protoreflect.Sint32Kind,
			protoreflect.Int64Kind, protoreflect.Sint64Kind,
			protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind,
			protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind,
			protoreflect.FloatKind, protoreflect.DoubleKind,
			protoreflect.StringKind, protoreflect.BytesKind:
		default:
			return false
		}
	}
	return true
}

func flatTagBytes(fnum uint16, wt uint8) []byte {
	rawTag := (uint64(fnum) << 4) | uint64(wt)
	if rawTag <= 127 {
		return []byte{byte(rawTag<<1) | 1}
	}
	u := uint16(rawTag<<2) | 2
	return []byte{byte(u), byte(u >> 8)}
}

func flatFixedFieldSize(f *protogen.Field) (int, bool) {
	fnum := uint16(f.Desc.Number())
	switch f.Desc.Kind() {
	case protoreflect.BoolKind:
		return len(flatTagBytes(fnum, wire.WireBoolFalse)), true
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return len(flatTagBytes(fnum, wireTypeForField(f))) + 4, true
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return len(flatTagBytes(fnum, wireTypeForField(f))) + 8, true
	default:
		return 0, false
	}
}

func flatMinFieldSize(f *protogen.Field) int {
	if sz, ok := flatFixedFieldSize(f); ok {
		return sz
	}
	fnum := uint16(f.Desc.Number())
	return len(flatTagBytes(fnum, wireTypeForField(f))) + 1
}

// emitFlatScalarMarshal emits a single-allocation, direct-indexed MarshalOmniBufAppend
// for flat scalar messages. When dst already has spare capacity for the upper-bound
// wire size it skips varint sizing completely; otherwise it computes the exact size
// so fresh allocations never over-allocate.
func emitFlatScalarMarshal(g *protogen.GeneratedFile, typeName string, orderedFields []*protogen.Field) {
	g.P("// MarshalOmniBufAppend encodes ", typeName, " in a single forward pass using generated field tags.")
	g.P("func (x *", typeName, ") MarshalOmniBufAppend(dst []byte) []byte {")

	constBytes := 0
	numVarints := 0
	var sizeTerms []string
	var maxTerms []string
	for _, f := range orderedFields {
		fnum := uint16(f.Desc.Number())
		var tb []byte
		if f.Desc.Kind() == protoreflect.BoolKind {
			tb = flatTagBytes(fnum, wire.WireBoolFalse)
		} else {
			tb = flatTagBytes(fnum, wireTypeForField(f))
		}
		constBytes += len(tb)

		switch f.Desc.Kind() {
		case protoreflect.BoolKind:
			// Tag byte alone encodes the boolean value.
		case protoreflect.Uint32Kind:
			numVarints++
			sizeTerms = append(sizeTerms, fmt.Sprintf("wire.VarintSize(uint64(x.%s))", f.GoName))
		case protoreflect.Uint64Kind:
			numVarints++
			sizeTerms = append(sizeTerms, fmt.Sprintf("wire.VarintSize(x.%s)", f.GoName))
		case protoreflect.Int32Kind, protoreflect.Sint32Kind:
			numVarints++
			sizeTerms = append(sizeTerms, fmt.Sprintf("wire.VarintSize(wire.EncodeZigZag(int64(x.%s)))", f.GoName))
		case protoreflect.Int64Kind, protoreflect.Sint64Kind:
			numVarints++
			sizeTerms = append(sizeTerms, fmt.Sprintf("wire.VarintSize(wire.EncodeZigZag(x.%s))", f.GoName))
		case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
			constBytes += 4
		case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
			constBytes += 8
		case protoreflect.StringKind, protoreflect.BytesKind:
			numVarints++
			sizeTerms = append(sizeTerms, fmt.Sprintf("wire.VarintSize(uint64(len(x.%s)))", f.GoName), fmt.Sprintf("len(x.%s)", f.GoName))
			maxTerms = append(maxTerms, fmt.Sprintf("len(x.%s)", f.GoName))
		}
	}

	maxNeedExpr := fmt.Sprintf("%d", constBytes+numVarints*9)
	if len(maxTerms) > 0 {
		maxNeedExpr += " + " + strings.Join(maxTerms, " + ")
	}
	needExpr := fmt.Sprintf("%d", constBytes)
	if len(sizeTerms) > 0 {
		needExpr += " + " + strings.Join(sizeTerms, " + ")
	}

	g.P("	pos := len(dst)")
	if numVarints > 0 {
		g.P("	if maxNeed := ", maxNeedExpr, "; cap(dst)-pos >= maxNeed {")
		g.P("		dst = dst[:pos+maxNeed]")
		g.P("	} else {")
		g.P("		need := ", needExpr)
		g.P("		if cap(dst)-pos >= need {")
		g.P("			dst = dst[:pos+need]")
		g.P("		} else if pos == 0 {")
		g.P("			dst = make([]byte, need)")
		g.P("		} else {")
		g.P("			next := make([]byte, pos+need, max(pos+need, cap(dst)*2))")
		g.P("			copy(next, dst)")
		g.P("			dst = next")
		g.P("		}")
		g.P("	}")
	} else {
		g.P("	const need = ", constBytes)
		g.P("	if cap(dst)-pos >= need {")
		g.P("		dst = dst[:pos+need]")
		g.P("	} else if pos == 0 {")
		g.P("		dst = make([]byte, need)")
		g.P("	} else {")
		g.P("		next := make([]byte, pos+need, max(pos+need, cap(dst)*2))")
		g.P("		copy(next, dst)")
		g.P("		dst = next")
		g.P("	}")
	}

	// Combine any trailing suffix of fixed-size fields into one sub-slice so Go emits
	// a single bounds check for the entire fixed suffix.
	tailStart := len(orderedFields)
	tailBytes := 0
	for i := len(orderedFields) - 1; i >= 0; i-- {
		sz, ok := flatFixedFieldSize(orderedFields[i])
		if !ok {
			break
		}
		tailStart = i
		tailBytes += sz
	}

	for i, f := range orderedFields {
		if i == tailStart && tailBytes > 0 {
			g.P("	tail := dst[pos : pos+", tailBytes, "]")
		}
		fnum := uint16(f.Desc.Number())
		if i >= tailStart {
			off := 0
			for j := tailStart; j < i; j++ {
				sz, _ := flatFixedFieldSize(orderedFields[j])
				off += sz
			}
			if f.Desc.Kind() == protoreflect.BoolKind {
				tb := flatTagBytes(fnum, wire.WireBoolFalse)
				if len(tb) == 1 {
					g.P(fmt.Sprintf("	sp_%d := byte(0x%02x)", i, tb[0]))
					g.P("	if x.", f.GoName, " {")
					g.P(fmt.Sprintf("		sp_%d = 0x%02x", i, tb[0]|2))
					g.P("	}")
					g.P(fmt.Sprintf("	tail[%d] = sp_%d", off, i))
				} else {
					g.P(fmt.Sprintf("	sp_%d := byte(0x%02x)", i, tb[0]))
					g.P("	if x.", f.GoName, " {")
					g.P(fmt.Sprintf("		sp_%d = 0x%02x", i, tb[0]|4))
					g.P("	}")
					g.P(fmt.Sprintf("	tail[%d] = sp_%d", off, i))
					g.P(fmt.Sprintf("	tail[%d] = 0x%02x", off+1, tb[1]))
				}
				continue
			}

			tb := flatTagBytes(fnum, wireTypeForField(f))
			for ti, bByte := range tb {
				g.P(fmt.Sprintf("	tail[%d] = 0x%02x", off+ti, bByte))
			}
			payOff := off + len(tb)
			switch f.Desc.Kind() {
			case protoreflect.Fixed32Kind:
				g.P(fmt.Sprintf("	binary.LittleEndian.PutUint32(tail[%d:], x.%s)", payOff, f.GoName))
			case protoreflect.Sfixed32Kind:
				g.P(fmt.Sprintf("	binary.LittleEndian.PutUint32(tail[%d:], uint32(x.%s))", payOff, f.GoName))
			case protoreflect.FloatKind:
				g.P(fmt.Sprintf("	binary.LittleEndian.PutUint32(tail[%d:], math.Float32bits(x.%s))", payOff, f.GoName))
			case protoreflect.Fixed64Kind:
				g.P(fmt.Sprintf("	binary.LittleEndian.PutUint64(tail[%d:], x.%s)", payOff, f.GoName))
			case protoreflect.Sfixed64Kind:
				g.P(fmt.Sprintf("	binary.LittleEndian.PutUint64(tail[%d:], uint64(x.%s))", payOff, f.GoName))
			case protoreflect.DoubleKind:
				g.P(fmt.Sprintf("	binary.LittleEndian.PutUint64(tail[%d:], math.Float64bits(x.%s))", payOff, f.GoName))
			}
			continue
		}

		if f.Desc.Kind() == protoreflect.BoolKind {
			tb := flatTagBytes(fnum, wire.WireBoolFalse)
			if len(tb) == 1 {
				g.P(fmt.Sprintf("	sp_%d := byte(0x%02x)", i, tb[0]))
				g.P("	if x.", f.GoName, " {")
				g.P(fmt.Sprintf("		sp_%d = 0x%02x", i, tb[0]|2))
				g.P("	}")
				g.P("	dst[pos] = sp_", i)
				g.P("	pos++")
			} else {
				g.P(fmt.Sprintf("	sp_%d := byte(0x%02x)", i, tb[0]))
				g.P("	if x.", f.GoName, " {")
				g.P(fmt.Sprintf("		sp_%d = 0x%02x", i, tb[0]|4))
				g.P("	}")
				g.P("	dst[pos] = sp_", i)
				g.P(fmt.Sprintf("	dst[pos+1] = 0x%02x", tb[1]))
				g.P("	pos += 2")
			}
			continue
		}

		tb := flatTagBytes(fnum, wireTypeForField(f))
		for ti, bByte := range tb {
			if ti == 0 {
				g.P(fmt.Sprintf("	dst[pos] = 0x%02x", bByte))
			} else {
				g.P(fmt.Sprintf("	dst[pos+%d] = 0x%02x", ti, bByte))
			}
		}

		switch f.Desc.Kind() {
		case protoreflect.Uint32Kind:
			g.P("	pos = wire.PutVarintAt(dst, pos+", len(tb), ", uint64(x.", f.GoName, "))")
		case protoreflect.Uint64Kind:
			g.P("	pos = wire.PutVarintAt(dst, pos+", len(tb), ", x.", f.GoName, ")")
		case protoreflect.Int32Kind, protoreflect.Sint32Kind:
			g.P("	pos = wire.PutVarintAt(dst, pos+", len(tb), ", wire.EncodeZigZag(int64(x.", f.GoName, ")))")
		case protoreflect.Int64Kind, protoreflect.Sint64Kind:
			g.P("	pos = wire.PutVarintAt(dst, pos+", len(tb), ", wire.EncodeZigZag(x.", f.GoName, "))")
		case protoreflect.Fixed32Kind:
			g.P("	binary.LittleEndian.PutUint32(dst[pos+", len(tb), ":], x.", f.GoName, ")")
			g.P("	pos += ", len(tb)+4)
		case protoreflect.Sfixed32Kind:
			g.P("	binary.LittleEndian.PutUint32(dst[pos+", len(tb), ":], uint32(x.", f.GoName, "))")
			g.P("	pos += ", len(tb)+4)
		case protoreflect.FloatKind:
			g.P("	binary.LittleEndian.PutUint32(dst[pos+", len(tb), ":], math.Float32bits(x.", f.GoName, "))")
			g.P("	pos += ", len(tb)+4)
		case protoreflect.Fixed64Kind:
			g.P("	binary.LittleEndian.PutUint64(dst[pos+", len(tb), ":], x.", f.GoName, ")")
			g.P("	pos += ", len(tb)+8)
		case protoreflect.Sfixed64Kind:
			g.P("	binary.LittleEndian.PutUint64(dst[pos+", len(tb), ":], uint64(x.", f.GoName, "))")
			g.P("	pos += ", len(tb)+8)
		case protoreflect.DoubleKind:
			g.P("	binary.LittleEndian.PutUint64(dst[pos+", len(tb), ":], math.Float64bits(x.", f.GoName, "))")
			g.P("	pos += ", len(tb)+8)
		case protoreflect.StringKind:
			g.P("	pos = wire.PutVarintAt(dst, pos+", len(tb), ", uint64(len(x.", f.GoName, ")))")
			g.P("	pos = wire.CopyStringAt(dst, pos, x.", f.GoName, ")")
		case protoreflect.BytesKind:
			g.P("	pos = wire.PutVarintAt(dst, pos+", len(tb), ", uint64(len(x.", f.GoName, ")))")
			g.P("	pos = wire.CopyBytesAt(dst, pos, x.", f.GoName, ")")
		}
	}

	if tailBytes > 0 {
		g.P("	pos += ", tailBytes)
	}
	g.P("	return dst[:pos]")
	g.P("}")
	g.P()
}

// emitFlatScalarUnmarshal emits UnmarshalOmniBuf with a general canonical-order fast path
// that decodes all fields sequentially without loop or switch dispatch, supporting any
// varint width and any string/bytes length. Non-canonical, partial, non-ASCII, or corrupt
// packets fall back to decodeOmniBuf.
func emitFlatScalarUnmarshal(g *protogen.GeneratedFile, typeName string, orderedFields []*protogen.Field) {
	g.P("// UnmarshalOmniBuf decodes data into ", typeName, " using generated switch dispatch.")
	g.P("func (x *", typeName, ") UnmarshalOmniBuf(data []byte, zeroCopy bool) error {")
	g.P("	if x == nil {")
	g.P(`		return fmt.Errorf("omnibuf: Unmarshal requires non-nil pointer")`)
	g.P("	}")

	// Precompute the minimum remaining wire bytes after each field so the compiler
	// proves slice bounds across consecutive fields and eliminates redundant length checks.
	remMin := make([]int, len(orderedFields))
	running := 0
	for i := len(orderedFields) - 1; i >= 0; i-- {
		remMin[i] = running
		running += flatMinFieldSize(orderedFields[i])
	}
	totalMin := running

	g.P("	b := data")
	g.P("	if len(b) < ", totalMin, " {")
	g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
	g.P("	}")

	// Find any trailing suffix of fixed-size fields so we can validate their combined
	// exact length in a single check (also replacing the final len(b) != 0 check).
	tailStart := len(orderedFields)
	tailBytes := 0
	for i := len(orderedFields) - 1; i >= 0; i-- {
		sz, ok := flatFixedFieldSize(orderedFields[i])
		if !ok {
			break
		}
		tailStart = i
		tailBytes += sz
	}

	type strFieldInfo struct {
		idx   int
		field *protogen.Field
	}
	var strFields []strFieldInfo
	var bytesFields []strFieldInfo
	var assignStmts []string

	for i, f := range orderedFields {
		inTail := i >= tailStart
		if i == tailStart && tailBytes > 0 {
			g.P("	if len(b) != ", tailBytes, " {")
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
		}

		fnum := uint16(f.Desc.Number())
		if inTail {
			off := 0
			for j := tailStart; j < i; j++ {
				sz, _ := flatFixedFieldSize(orderedFields[j])
				off += sz
			}
			if f.Desc.Kind() == protoreflect.BoolKind {
				tb := flatTagBytes(fnum, wire.WireBoolFalse)
				if len(tb) == 1 {
					g.P(fmt.Sprintf("	if (b[%d]&^2) != 0x%02x {", off, tb[0]))
					g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
					g.P("	}")
					g.P(fmt.Sprintf("	f_%d := (b[%d] & 2) != 0", i, off))
				} else {
					g.P(fmt.Sprintf("	if (b[%d]&^4) != 0x%02x || b[%d] != 0x%02x {", off, tb[0], off+1, tb[1]))
					g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
					g.P("	}")
					g.P(fmt.Sprintf("	f_%d := (b[%d] & 4) != 0", i, off))
				}
				assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
				continue
			}

			tb := flatTagBytes(fnum, wireTypeForField(f))
			var tagConds []string
			for ti, bByte := range tb {
				tagConds = append(tagConds, fmt.Sprintf("b[%d] != 0x%02x", off+ti, bByte))
			}
			g.P("	if ", strings.Join(tagConds, " || "), " {")
			g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
			g.P("	}")
			payOff := off + len(tb)
			switch f.Desc.Kind() {
			case protoreflect.Fixed32Kind:
				g.P(fmt.Sprintf("	f_%d := binary.LittleEndian.Uint32(b[%d:])", i, payOff))
			case protoreflect.Sfixed32Kind:
				g.P(fmt.Sprintf("	f_%d := int32(binary.LittleEndian.Uint32(b[%d:]))", i, payOff))
			case protoreflect.FloatKind:
				g.P(fmt.Sprintf("	f_%d := math.Float32frombits(binary.LittleEndian.Uint32(b[%d:]))", i, payOff))
			case protoreflect.Fixed64Kind:
				g.P(fmt.Sprintf("	f_%d := binary.LittleEndian.Uint64(b[%d:])", i, payOff))
			case protoreflect.Sfixed64Kind:
				g.P(fmt.Sprintf("	f_%d := int64(binary.LittleEndian.Uint64(b[%d:]))", i, payOff))
			case protoreflect.DoubleKind:
				g.P(fmt.Sprintf("	f_%d := math.Float64frombits(binary.LittleEndian.Uint64(b[%d:]))", i, payOff))
			}
			assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
			continue
		}

		// Invariant at start of field i: len(b) >= flatMinFieldSize(f) + remMin[i].
		if f.Desc.Kind() == protoreflect.BoolKind {
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
			assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
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

		rem := remMin[i]
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
				assignStmts = append(assignStmts, fmt.Sprintf("x.%s = uint32(v_%d)", f.GoName, i))
			case protoreflect.Uint64Kind:
				assignStmts = append(assignStmts, fmt.Sprintf("x.%s = v_%d", f.GoName, i))
			case protoreflect.Int32Kind, protoreflect.Sint32Kind:
				assignStmts = append(assignStmts, fmt.Sprintf("x.%s = int32(wire.DecodeZigZag(v_%d))", f.GoName, i))
			case protoreflect.Int64Kind, protoreflect.Sint64Kind:
				assignStmts = append(assignStmts, fmt.Sprintf("x.%s = wire.DecodeZigZag(v_%d)", f.GoName, i))
			}
		case protoreflect.Fixed32Kind:
			g.P("	f_", i, " := binary.LittleEndian.Uint32(b)")
			g.P("	b = b[4:]")
			assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.Sfixed32Kind:
			g.P("	f_", i, " := int32(binary.LittleEndian.Uint32(b))")
			g.P("	b = b[4:]")
			assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.FloatKind:
			g.P("	f_", i, " := math.Float32frombits(binary.LittleEndian.Uint32(b))")
			g.P("	b = b[4:]")
			assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.Fixed64Kind:
			g.P("	f_", i, " := binary.LittleEndian.Uint64(b)")
			g.P("	b = b[8:]")
			assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.Sfixed64Kind:
			g.P("	f_", i, " := int64(binary.LittleEndian.Uint64(b))")
			g.P("	b = b[8:]")
			assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
		case protoreflect.DoubleKind:
			g.P("	f_", i, " := math.Float64frombits(binary.LittleEndian.Uint64(b))")
			g.P("	b = b[8:]")
			assignStmts = append(assignStmts, fmt.Sprintf("x.%s = f_%d", f.GoName, i))
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
			strFields = append(strFields, strFieldInfo{idx: i, field: f})
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
			bytesFields = append(bytesFields, strFieldInfo{idx: i, field: f})
		}
	}

	if tailBytes == 0 {
		g.P("	if len(b) != 0 {")
		g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
		g.P("	}")
	}

	if len(strFields) > 0 {
		var asciiChecks []string
		for _, sf := range strFields {
			asciiChecks = append(asciiChecks, fmt.Sprintf("!wire.IsASCII(s_%d)", sf.idx))
		}
		g.P("	if ", strings.Join(asciiChecks, " || "), " {")
		g.P("		return x.decodeOmniBuf(data, zeroCopy, 0, false)")
		g.P("	}")
	}

	g.P("	_omnibufProtoState_", typeName, ".Reset(unsafe.Pointer(x), false)")
	for _, stmt := range assignStmts {
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

	g.P("	return nil")
	g.P("}")
	g.P()
}
