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
	strCount := 0
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
			protoreflect.BytesKind:
		case protoreflect.StringKind:
			strCount++
		default:
			return false
		}
	}
	// Keep combined safe string slab bounded (<= 256 bytes when every string is <= 16 bytes).
	return strCount <= 16
}

func flatTagBytes(fnum uint16, wt uint8) []byte {
	rawTag := (uint64(fnum) << 4) | uint64(wt)
	if rawTag <= 127 {
		return []byte{byte(rawTag<<1) | 1}
	}
	u := uint16(rawTag<<2) | 2
	return []byte{byte(u), byte(u >> 8)}
}

func isVarLenScalar(f *protogen.Field) bool {
	k := f.Desc.Kind()
	return k == protoreflect.StringKind || k == protoreflect.BytesKind
}

type flatFieldMeta struct {
	idx       int
	field     *protogen.Field
	fnum      uint16
	tagBytes  []byte
	fixedSize int // tag + fixed payload (or tag + 1-byte varint/length)
	valOff    int // offset within run to value/len byte
}

type flatRun struct {
	fields     []flatFieldMeta
	fixedLen   int
	endsVarLen bool
	varLenMeta flatFieldMeta
}

func buildFlatRuns(orderedFields []*protogen.Field) ([]flatRun, int) {
	var runs []flatRun
	var cur flatRun
	totalMin := 0

	for i, f := range orderedFields {
		fnum := uint16(f.Desc.Number())
		var tb []byte
		if f.Desc.Kind() == protoreflect.BoolKind {
			tb = flatTagBytes(fnum, wire.WireBoolFalse)
		} else {
			tb = flatTagBytes(fnum, wireTypeForField(f))
		}
		meta := flatFieldMeta{
			idx:      i,
			field:    f,
			fnum:     fnum,
			tagBytes: tb,
			valOff:   cur.fixedLen + len(tb),
		}
		switch f.Desc.Kind() {
		case protoreflect.BoolKind:
			meta.fixedSize = len(tb)
			meta.valOff = cur.fixedLen
		case protoreflect.Uint32Kind,
			protoreflect.Int32Kind, protoreflect.Sint32Kind,
			protoreflect.StringKind, protoreflect.BytesKind:
			meta.fixedSize = len(tb) + 1
		case protoreflect.Uint64Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind:
			// 64-bit varints (> 536870911, e.g. UnixNano timestamps / IDs) encode as 0x00 + 8-byte LE uint64.
			meta.fixedSize = len(tb) + 9
		case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
			meta.fixedSize = len(tb) + 4
		case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
			meta.fixedSize = len(tb) + 8
		}
		cur.fixedLen += meta.fixedSize
		totalMin += meta.fixedSize
		cur.fields = append(cur.fields, meta)

		if isVarLenScalar(f) {
			cur.endsVarLen = true
			cur.varLenMeta = meta
			runs = append(runs, cur)
			cur = flatRun{}
		}
	}
	if len(cur.fields) > 0 {
		runs = append(runs, cur)
	}
	return runs, totalMin
}

func constSliceExpr(sliceVar string, off int) string {
	if off == 0 {
		return sliceVar
	}
	return fmt.Sprintf("%s[%d:]", sliceVar, off)
}

func slideBack8Expr(payOff, idx int) string {
	delta := payOff - 8
	if delta == 0 {
		return fmt.Sprintf("n_%d", idx)
	}
	if delta > 0 {
		return fmt.Sprintf("%d+n_%d", delta, idx)
	}
	return fmt.Sprintf("n_%d-%d", idx, -delta)
}

// emitFlatScalarMarshal emits a single-allocation, sliding-window, zero-memmove fast path
// for flat scalar messages when all varint values fit in 1 byte and variable-length
// string/bytes fields are 8..16 bytes (falling back to marshalOmniBufSlow otherwise).
// Each run after a varlen field slices 8 bytes back so the previous field's tail 8 bytes
// land at constant offset 0, eliminating per-field bounds checks and sub-slices.
func emitFlatScalarMarshal(g *protogen.GeneratedFile, typeName string, orderedFields []*protogen.Field) bool {
	runs, constBase := buildFlatRuns(orderedFields)

	g.P("// MarshalOmniBufAppend encodes ", typeName, " in a single forward pass using generated field tags.")
	g.P("func (x *", typeName, ") MarshalOmniBufAppend(dst []byte) []byte {")

	var intVarintChecks []string
	var wide64Checks []string
	var shortVarLenChecks []string
	var lenTerms []string
	for i, f := range orderedFields {
		switch f.Desc.Kind() {
		case protoreflect.StringKind, protoreflect.BytesKind:
			g.P("	n_", i, " := len(x.", f.GoName, ")")
			shortVarLenChecks = append(shortVarLenChecks, fmt.Sprintf("n_%d < 8 || n_%d > 16", i, i))
			lenTerms = append(lenTerms, fmt.Sprintf("n_%d", i))
		case protoreflect.Uint32Kind:
			intVarintChecks = append(intVarintChecks, fmt.Sprintf("uint64(x.%s)", f.GoName))
		case protoreflect.Uint64Kind:
			wide64Checks = append(wide64Checks, fmt.Sprintf("x.%s <= 536870911", f.GoName))
		case protoreflect.Int32Kind, protoreflect.Sint32Kind:
			g.P("	v_", i, " := wire.EncodeZigZag(int64(x.", f.GoName, "))")
			intVarintChecks = append(intVarintChecks, fmt.Sprintf("v_%d", i))
		case protoreflect.Int64Kind, protoreflect.Sint64Kind:
			g.P("	v_", i, " := wire.EncodeZigZag(x.", f.GoName, ")")
			wide64Checks = append(wide64Checks, fmt.Sprintf("v_%d <= 536870911", i))
		}
	}

	var slowConds []string
	slowConds = append(slowConds, shortVarLenChecks...)
	slowConds = append(slowConds, wide64Checks...)
	if len(intVarintChecks) > 0 {
		slowConds = append(slowConds, fmt.Sprintf("(%s) > 127", strings.Join(intVarintChecks, "|")))
	}
	if len(slowConds) > 0 {
		g.P("	if ", strings.Join(slowConds, " || "), " {")
		g.P("		return x.marshalOmniBufSlow(dst)")
		g.P("	}")
	}

	needExpr := fmt.Sprintf("%d", constBase)
	if len(lenTerms) > 0 {
		needExpr += " + " + strings.Join(lenTerms, " + ")
	}
	g.P("	need := ", needExpr)
	g.P("	off := len(dst)")
	g.P("	var b0 []byte")
	g.P("	if cap(dst)-off >= need {")
	g.P("		dst = dst[:off+need]")
	g.P("		b0 = dst[off:]")
	g.P("	} else if off == 0 {")
	g.P("		dst = make([]byte, need)")
	g.P("		b0 = dst")
	g.P("	} else {")
	g.P("		return x.marshalOmniBufSlow(dst)")
	g.P("	}")

	for rIdx, run := range runs {
		bVar := fmt.Sprintf("b%d", rIdx)
		shift := 0
		if rIdx > 0 {
			shift = 8
		}
		// Compute max constant offset touched in this run.
		maxTouch := shift + run.fixedLen
		if run.endsVarLen {
			maxTouch += 8 // head 8 bytes of varlen payload
		}
		if maxTouch > 0 {
			g.P("	_ = ", bVar, "[", maxTouch-1, "]")
		}
		if rIdx > 0 {
			prev := runs[rIdx-1].varLenMeta
			if prev.field.Desc.Kind() == protoreflect.StringKind {
				g.P("	binary.LittleEndian.PutUint64(", bVar, ", binary.LittleEndian.Uint64(s_", prev.idx, "[n_", prev.idx, "-8:]))")
			} else {
				g.P("	binary.LittleEndian.PutUint64(", bVar, ", binary.LittleEndian.Uint64(x.", prev.field.GoName, "[n_", prev.idx, "-8:]))")
			}
		}

		for _, m := range run.fields {
			f := m.field
			if f.Desc.Kind() == protoreflect.BoolKind {
				tagOff := shift + m.valOff
				if len(m.tagBytes) == 1 {
					tFalse := m.tagBytes[0]
					tTrue := tFalse | 2
					g.P(fmt.Sprintf("	sp_%d := byte(0x%02x)", m.idx, tFalse))
					g.P("	if x.", f.GoName, " {")
					g.P(fmt.Sprintf("		sp_%d = 0x%02x", m.idx, tTrue))
					g.P("	}")
					g.P("	", bVar, "[", tagOff, "] = sp_", m.idx)
				} else {
					lowFalse := m.tagBytes[0]
					lowTrue := lowFalse | 4
					g.P(fmt.Sprintf("	sp_%d := byte(0x%02x)", m.idx, lowFalse))
					g.P("	if x.", f.GoName, " {")
					g.P(fmt.Sprintf("		sp_%d = 0x%02x", m.idx, lowTrue))
					g.P("	}")
					g.P("	", bVar, "[", tagOff, "] = sp_", m.idx)
					g.P(fmt.Sprintf("	%s[%d] = 0x%02x", bVar, tagOff+1, m.tagBytes[1]))
				}
				continue
			}

			tagOff := shift + m.valOff - len(m.tagBytes)
			for ti, tb := range m.tagBytes {
				g.P(fmt.Sprintf("	%s[%d] = 0x%02x", bVar, tagOff+ti, tb))
			}

			valOff := shift + m.valOff
			switch f.Desc.Kind() {
			case protoreflect.Uint32Kind:
				g.P("	", bVar, "[", valOff, "] = byte(x.", f.GoName, "<<1) | 1")
			case protoreflect.Uint64Kind:
				g.P("	", bVar, "[", valOff, "] = 0x00")
				g.P("	binary.LittleEndian.PutUint64(", constSliceExpr(bVar, valOff+1), ", x.", f.GoName, ")")
			case protoreflect.Int32Kind, protoreflect.Sint32Kind:
				g.P("	", bVar, "[", valOff, "] = byte(v_", m.idx, "<<1) | 1")
			case protoreflect.Int64Kind, protoreflect.Sint64Kind:
				g.P("	", bVar, "[", valOff, "] = 0x00")
				g.P("	binary.LittleEndian.PutUint64(", constSliceExpr(bVar, valOff+1), ", v_", m.idx, ")")
			case protoreflect.Fixed32Kind:
				g.P("	binary.LittleEndian.PutUint32(", constSliceExpr(bVar, valOff), ", x.", f.GoName, ")")
			case protoreflect.Sfixed32Kind:
				g.P("	binary.LittleEndian.PutUint32(", constSliceExpr(bVar, valOff), ", uint32(x.", f.GoName, "))")
			case protoreflect.FloatKind:
				g.P("	binary.LittleEndian.PutUint32(", constSliceExpr(bVar, valOff), ", math.Float32bits(x.", f.GoName, "))")
			case protoreflect.Fixed64Kind:
				g.P("	binary.LittleEndian.PutUint64(", constSliceExpr(bVar, valOff), ", x.", f.GoName, ")")
			case protoreflect.Sfixed64Kind:
				g.P("	binary.LittleEndian.PutUint64(", constSliceExpr(bVar, valOff), ", uint64(x.", f.GoName, "))")
			case protoreflect.DoubleKind:
				g.P("	binary.LittleEndian.PutUint64(", constSliceExpr(bVar, valOff), ", math.Float64bits(x.", f.GoName, "))")
			case protoreflect.StringKind:
				g.P("	", bVar, "[", valOff, "] = byte(n_", m.idx, "<<1) | 1")
				payOff := shift + run.fixedLen
				g.P("	s_", m.idx, " := unsafe.Slice(unsafe.StringData(x.", f.GoName, "), n_", m.idx, ")")
				g.P("	binary.LittleEndian.PutUint64(", constSliceExpr(bVar, payOff), ", binary.LittleEndian.Uint64(s_", m.idx, "))")
				if rIdx < len(runs)-1 {
					g.P("	b", rIdx+1, " := ", bVar, "[", slideBack8Expr(payOff, m.idx), ":]")
				} else {
					g.P("	binary.LittleEndian.PutUint64(", bVar, "[", slideBack8Expr(payOff, m.idx), ":], binary.LittleEndian.Uint64(s_", m.idx, "[n_", m.idx, "-8:]))")
				}
			case protoreflect.BytesKind:
				g.P("	", bVar, "[", valOff, "] = byte(n_", m.idx, "<<1) | 1")
				payOff := shift + run.fixedLen
				g.P("	binary.LittleEndian.PutUint64(", constSliceExpr(bVar, payOff), ", binary.LittleEndian.Uint64(x.", f.GoName, "))")
				if rIdx < len(runs)-1 {
					g.P("	b", rIdx+1, " := ", bVar, "[", slideBack8Expr(payOff, m.idx), ":]")
				} else {
					g.P("	binary.LittleEndian.PutUint64(", bVar, "[", slideBack8Expr(payOff, m.idx), ":], binary.LittleEndian.Uint64(x.", f.GoName, "[n_", m.idx, "-8:]))")
				}
			}
		}
	}

	g.P("	return dst")
	g.P("}")
	g.P()
	return true
}

type byteCheck struct {
	mask byte
	want byte
}

func buildRunCheckExprShifted(run flatRun, sliceVar string, shift int) string {
	checks := make([]byteCheck, run.fixedLen)
	for _, m := range run.fields {
		k := m.field.Desc.Kind()
		if k == protoreflect.BoolKind {
			if len(m.tagBytes) == 1 {
				checks[m.valOff] = byteCheck{mask: 0xfd, want: m.tagBytes[0]}
			} else {
				checks[m.valOff] = byteCheck{mask: 0xfb, want: m.tagBytes[0]}
				checks[m.valOff+1] = byteCheck{mask: 0xff, want: m.tagBytes[1]}
			}
			continue
		}
		tagOff := m.valOff - len(m.tagBytes)
		for ti, tb := range m.tagBytes {
			checks[tagOff+ti] = byteCheck{mask: 0xff, want: tb}
		}
		switch k {
		case protoreflect.Uint32Kind,
			protoreflect.Int32Kind, protoreflect.Sint32Kind,
			protoreflect.StringKind, protoreflect.BytesKind:
			checks[m.valOff] = byteCheck{mask: 0x01, want: 0x01}
		case protoreflect.Uint64Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind:
			checks[m.valOff] = byteCheck{mask: 0xff, want: 0x00}
		}
	}

	var conds []string
	for i := 0; i < len(checks); {
		if checks[i].mask == 0 {
			i++
			continue
		}
		off := shift + i
		if i+4 <= len(checks) && (checks[i+2].mask != 0 || checks[i+3].mask != 0) {
			mask := uint32(checks[i].mask) | uint32(checks[i+1].mask)<<8 | uint32(checks[i+2].mask)<<16 | uint32(checks[i+3].mask)<<24
			want := uint32(checks[i].want) | uint32(checks[i+1].want)<<8 | uint32(checks[i+2].want)<<16 | uint32(checks[i+3].want)<<24
			se := constSliceExpr(sliceVar, off)
			if mask == 0xffffffff {
				conds = append(conds, fmt.Sprintf("binary.LittleEndian.Uint32(%s) == 0x%08x", se, want))
			} else {
				conds = append(conds, fmt.Sprintf("binary.LittleEndian.Uint32(%s)&0x%08x == 0x%08x", se, mask, want))
			}
			i += 4
			continue
		}
		if i+2 <= len(checks) && checks[i+1].mask != 0 {
			mask := uint16(checks[i].mask) | uint16(checks[i+1].mask)<<8
			want := uint16(checks[i].want) | uint16(checks[i+1].want)<<8
			se := constSliceExpr(sliceVar, off)
			if mask == 0xffff {
				conds = append(conds, fmt.Sprintf("binary.LittleEndian.Uint16(%s) == 0x%04x", se, want))
			} else {
				conds = append(conds, fmt.Sprintf("binary.LittleEndian.Uint16(%s)&0x%04x == 0x%04x", se, mask, want))
			}
			i += 2
			continue
		}
		if checks[i].mask == 0xff {
			conds = append(conds, fmt.Sprintf("%s[%d] == 0x%02x", sliceVar, off, checks[i].want))
		} else {
			conds = append(conds, fmt.Sprintf("%s[%d]&0x%02x == 0x%02x", sliceVar, off, checks[i].mask, checks[i].want))
		}
		i++
	}
	return strings.Join(conds, " && ")
}

func flatScalarDecodeExprShifted(m flatFieldMeta, sliceVar string, shift int) string {
	off := shift + m.valOff
	se := constSliceExpr(sliceVar, off)
	switch m.field.Desc.Kind() {
	case protoreflect.BoolKind:
		bit := 2
		if len(m.tagBytes) == 2 {
			bit = 4
		}
		return fmt.Sprintf("(%s[%d] & %d) != 0", sliceVar, off, bit)
	case protoreflect.Uint32Kind:
		return fmt.Sprintf("uint32(%s[%d] >> 1)", sliceVar, off)
	case protoreflect.Uint64Kind:
		return fmt.Sprintf("binary.LittleEndian.Uint64(%s)", constSliceExpr(sliceVar, off+1))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind:
		return fmt.Sprintf("int32(wire.DecodeZigZag(uint64(%s[%d] >> 1)))", sliceVar, off)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind:
		return fmt.Sprintf("wire.DecodeZigZag(binary.LittleEndian.Uint64(%s))", constSliceExpr(sliceVar, off+1))
	case protoreflect.Fixed32Kind:
		return fmt.Sprintf("binary.LittleEndian.Uint32(%s)", se)
	case protoreflect.Sfixed32Kind:
		return fmt.Sprintf("int32(binary.LittleEndian.Uint32(%s))", se)
	case protoreflect.FloatKind:
		return fmt.Sprintf("math.Float32frombits(binary.LittleEndian.Uint32(%s))", se)
	case protoreflect.Fixed64Kind:
		return fmt.Sprintf("binary.LittleEndian.Uint64(%s)", se)
	case protoreflect.Sfixed64Kind:
		return fmt.Sprintf("int64(binary.LittleEndian.Uint64(%s))", se)
	case protoreflect.DoubleKind:
		return fmt.Sprintf("math.Float64frombits(binary.LittleEndian.Uint64(%s))", se)
	}
	return ""
}

type slidingVarLen struct {
	meta     flatFieldMeta
	headExpr string // constant-offset slice expr for head 8 bytes and zero-copy &ptr
	headPtr  string // e.g. &r0[2] or &r1[19]
	tailExpr string // constant-offset slice expr for tail 8 bytes
	rawSlice string // e.g. r0[2 : 2+n_0 : 2+n_0] (for BytesKind zeroCopy)
}

// emitFlatScalarUnmarshal emits UnmarshalOmniBuf with a canonical-order sliding-window
// fast path. Each run after a varlen field slices 8 bytes back so every tag check, scalar
// load, and 8-byte ASCII/UTF-8 word load uses constant offsets with minimal bounds checks,
// zero memmove, and zero function calls on the zero-copy path.
func emitFlatScalarUnmarshal(g *protogen.GeneratedFile, typeName string, orderedFields []*protogen.Field) {
	runs, minCanonical := buildFlatRuns(orderedFields)
	remMinAfter := make([]int, len(runs))
	for r := len(runs) - 2; r >= 0; r-- {
		remMinAfter[r] = remMinAfter[r+1] + runs[r+1].fixedLen
	}

	lastVarLenRun := -1
	for rIdx, run := range runs {
		if run.endsVarLen {
			lastVarLenRun = rIdx
		}
	}

	g.P("// UnmarshalOmniBuf decodes data into ", typeName, " using generated switch dispatch.")
	g.P("func (x *", typeName, ") UnmarshalOmniBuf(data []byte, zeroCopy bool) error {")
	g.P("	if x == nil {")
	g.P(`		return fmt.Errorf("omnibuf: Unmarshal requires non-nil pointer")`)
	g.P("	}")

	indent := "\t"
	openDepth := 0
	open := func(line string) {
		g.P(indent, line)
		indent += "\t"
		openDepth++
	}

	var strLocs []slidingVarLen
	var bytesLocs []slidingVarLen

	if lastVarLenRun < 0 {
		open(fmt.Sprintf("if len(data) == %d {", minCanonical))
	} else {
		open(fmt.Sprintf("if len(data) >= %d {", minCanonical))
	}
	g.P(indent, "r0 := data")

	for rIdx, run := range runs {
		rVar := fmt.Sprintf("r%d", rIdx)
		shift := 0
		if rIdx > 0 {
			shift = 8
		}
		if shift+run.fixedLen > 0 {
			g.P(indent, "_ = ", rVar, "[", shift+run.fixedLen-1, "]")
		}
		checkExpr := buildRunCheckExprShifted(run, rVar, shift)
		open("if " + checkExpr + " {")

		if run.endsVarLen {
			vm := run.varLenMeta
			valOff := shift + vm.valOff
			payOff := shift + run.fixedLen
			g.P(indent, "n_", vm.idx, " := int(", rVar, "[", valOff, "] >> 1)")

			op := "<="
			if rIdx == lastVarLenRun {
				op = "=="
			}
			lenCheck := fmt.Sprintf("%d+n_%d", payOff, vm.idx)
			if remMinAfter[rIdx] > 0 {
				lenCheck += fmt.Sprintf("+%d", remMinAfter[rIdx])
			}
			open(fmt.Sprintf("if n_%d >= 8 && n_%d <= 16 && %s %s len(%s) {", vm.idx, vm.idx, lenCheck, op, rVar))
			// Hint that head 8 bytes of this varlen payload are in bounds at constant offset payOff.
			g.P(indent, "_ = ", rVar, "[", payOff+7, "]")

			var tailExpr string
			if rIdx < len(runs)-1 {
				nextVar := fmt.Sprintf("r%d", rIdx+1)
				g.P(indent, nextVar, " := ", rVar, "[", slideBack8Expr(payOff, vm.idx), ":]")
				tailExpr = nextVar
			} else {
				// Last run ends with a varlen field: len(rVar) == payOff+n_idx.
				tailExpr = fmt.Sprintf("%s[len(%s)-8:]", rVar, rVar)
			}

			sv := slidingVarLen{
				meta:     vm,
				headExpr: constSliceExpr(rVar, payOff),
				headPtr:  fmt.Sprintf("&%s[%d]", rVar, payOff),
				tailExpr: tailExpr,
				rawSlice: fmt.Sprintf("%s[%d : %d+n_%d : %d+n_%d]", rVar, payOff, payOff, vm.idx, payOff, vm.idx),
			}
			if vm.field.Desc.Kind() == protoreflect.StringKind {
				strLocs = append(strLocs, sv)
			} else {
				bytesLocs = append(bytesLocs, sv)
			}
		}
	}

	if len(strLocs) > 0 {
		var asciiTerms []string
		for _, loc := range strLocs {
			idx := loc.meta.idx
			g.P(indent, "w_", idx, "a := binary.LittleEndian.Uint64(", loc.headExpr, ")")
			g.P(indent, "w_", idx, "b := binary.LittleEndian.Uint64(", loc.tailExpr, ")")
			asciiTerms = append(asciiTerms, fmt.Sprintf("w_%da", idx), fmt.Sprintf("w_%db", idx))
		}
		open(fmt.Sprintf("if (%s)&0x8080808080808080 == 0 {", strings.Join(asciiTerms, "|")))
	}

	g.P(indent, "_omnibufProtoState_", typeName, ".Reset(unsafe.Pointer(x), false)")

	// Assign fixed scalar fields BEFORE any safe-mode heap allocation so r0..rN die before makeslice.
	for rIdx, run := range runs {
		rVar := fmt.Sprintf("r%d", rIdx)
		shift := 0
		if rIdx > 0 {
			shift = 8
		}
		for _, m := range run.fields {
			if isVarLenScalar(m.field) {
				continue
			}
			g.P(indent, "x.", m.field.GoName, " = ", flatScalarDecodeExprShifted(m, rVar, shift))
		}
	}

	if len(strLocs) > 0 {
		g.P(indent, "if zeroCopy {")
		for _, loc := range strLocs {
			idx := loc.meta.idx
			g.P(indent, "	x.", loc.meta.field.GoName, " = unsafe.String(", loc.headPtr, ", n_", idx, ")")
		}
		g.P(indent, "} else {")
		var nTerms []string
		for _, loc := range strLocs {
			nTerms = append(nTerms, fmt.Sprintf("n_%d", loc.meta.idx))
		}
		g.P(indent, "	totalStr := ", strings.Join(nTerms, "+"))
		g.P(indent, "	sb := make([]byte, totalStr)")
		g.P(indent, "	_ = sb[totalStr-1]")
		if len(strLocs) == 1 {
			idx := strLocs[0].meta.idx
			g.P(indent, "	binary.LittleEndian.PutUint64(sb, w_", idx, "a)")
			g.P(indent, "	binary.LittleEndian.PutUint64(sb[n_", idx, "-8:], w_", idx, "b)")
			g.P(indent, "	x.", strLocs[0].meta.field.GoName, " = unsafe.String(&sb[0], totalStr)")
		} else {
			offExpr := ""
			for i, loc := range strLocs {
				idx := loc.meta.idx
				if i == 0 {
					g.P(indent, "	binary.LittleEndian.PutUint64(sb, w_", idx, "a)")
					g.P(indent, "	binary.LittleEndian.PutUint64(sb[n_", idx, "-8:], w_", idx, "b)")
					offExpr = fmt.Sprintf("n_%d", idx)
				} else {
					g.P(indent, "	binary.LittleEndian.PutUint64(sb[", offExpr, ":], w_", idx, "a)")
					endExpr := fmt.Sprintf("%s+n_%d", offExpr, idx)
					g.P(indent, "	binary.LittleEndian.PutUint64(sb[", endExpr, "-8:], w_", idx, "b)")
					offExpr = endExpr
				}
			}
			g.P(indent, "	all := unsafe.String(&sb[0], totalStr)")
			offExpr = ""
			for i, loc := range strLocs {
				idx := loc.meta.idx
				if i == 0 {
					g.P(indent, "	x.", loc.meta.field.GoName, " = all[:n_", idx, "]")
					offExpr = fmt.Sprintf("n_%d", idx)
				} else if i == len(strLocs)-1 {
					g.P(indent, "	x.", loc.meta.field.GoName, " = all[", offExpr, ":]")
				} else {
					endExpr := fmt.Sprintf("%s+n_%d", offExpr, idx)
					g.P(indent, "	x.", loc.meta.field.GoName, " = all[", offExpr, ":", endExpr, "]")
					offExpr = endExpr
				}
			}
		}
		g.P(indent, "}")
	}

	if len(bytesLocs) > 0 {
		g.P(indent, "if zeroCopy {")
		for _, loc := range bytesLocs {
			g.P(indent, "	x.", loc.meta.field.GoName, " = ", loc.rawSlice)
		}
		g.P(indent, "} else {")
		for _, loc := range bytesLocs {
			idx := loc.meta.idx
			g.P(indent, "	bb_", idx, " := make([]byte, n_", idx, ")")
			g.P(indent, "	binary.LittleEndian.PutUint64(bb_", idx, ", binary.LittleEndian.Uint64(", loc.headExpr, "))")
			g.P(indent, "	binary.LittleEndian.PutUint64(bb_", idx, "[n_", idx, "-8:], binary.LittleEndian.Uint64(", loc.tailExpr, "))")
			g.P(indent, "	x.", loc.meta.field.GoName, " = bb_", idx)
		}
		g.P(indent, "}")
	}

	g.P(indent, "return nil")

	for openDepth > 0 {
		openDepth--
		indent = indent[:len(indent)-1]
		g.P(indent, "}")
	}

	g.P("	return x.decodeOmniBuf(data, zeroCopy, 0, false)")
	g.P("}")
	g.P()
}
