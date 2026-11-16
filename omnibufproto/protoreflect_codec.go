package omnibufproto

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"maps"
	"math"
	"slices"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func readLenSlice(data []byte, pos int, minLen uint64) ([]byte, int, error) {
	bLen, bn := wire.DecodeVarint(data[pos:])
	if bn == 0 {
		return nil, 0, fmt.Errorf("omnibuf: truncated length varint at %d", pos)
	}
	pos += bn
	if bLen < minLen || bLen > uint64(len(data)-pos) {
		return nil, 0, fmt.Errorf("omnibuf: invalid byteLen %d at %d", bLen, pos)
	}
	end := pos + int(bLen)
	return data[pos:end:end], end, nil
}

func readStrSlice(data []byte, pos int) ([]byte, int, error) {
	body, nextPos, err := readLenSlice(data, pos, 0)
	if err != nil {
		return nil, 0, err
	}
	if !wire.ValidUTF8(body) {
		return nil, 0, fmt.Errorf("omnibuf: invalid UTF-8 in WireString at %d", pos)
	}
	return body, nextPos, nil
}

func validateField0(data []byte, pos int, wt uint8, depth int) (int, error) {
	if wt != wire.WireSubMsg {
		return 0, fmt.Errorf("omnibuf: invalid Field #0 wire type %d", wt)
	}
	return omnibuf.ValidateField0(data, pos, depth)
}

func mergeSchemaMeta(base, next *omnibuf.SchemaMeta) *omnibuf.SchemaMeta {
	if next == nil {
		return base
	}
	if base == nil {
		cp := &omnibuf.SchemaMeta{TypeName: next.TypeName}
		if len(next.Fields) > 0 {
			cp.Fields = make(map[uint32]string, len(next.Fields))
			maps.Copy(cp.Fields, next.Fields)
		}
		if len(next.Children) > 0 {
			cp.Children = make(map[uint32]*omnibuf.SchemaMeta, len(next.Children))
			for k, c := range next.Children {
				cp.Children[k] = mergeSchemaMeta(nil, c)
			}
		}
		return cp
	}
	if next.TypeName != "" {
		base.TypeName = next.TypeName
	}
	if len(next.Fields) > 0 {
		if base.Fields == nil {
			base.Fields = make(map[uint32]string, len(next.Fields))
		}
		maps.Copy(base.Fields, next.Fields)
	}
	if len(next.Children) > 0 {
		if base.Children == nil {
			base.Children = make(map[uint32]*omnibuf.SchemaMeta, len(next.Children))
		}
		for k, c := range next.Children {
			base.Children[k] = mergeSchemaMeta(base.Children[k], c)
		}
	}
	return base
}

func (o MarshalOptions) marshalProtoReflectAppend(dst []byte, m protoreflect.Message) ([]byte, error) {
	if m == nil || !m.IsValid() {
		return nil, fmt.Errorf("omnibuf: Marshal requires non-nil protoreflect.Message")
	}
	if err := proto.CheckInitialized(m.Interface()); err != nil {
		return nil, err
	}
	if cap(dst) < 256 {
		dst = make([]byte, 0, 256)
	} else {
		dst = dst[:0]
	}
	if o.SelfDescribing {
		meta, cyclic := buildProtoReflectSchemaMeta(m.Descriptor(), 1, nil)
		if cyclic {
			meta = buildProtoReflectValueSchemaMeta(m, 1)
		}
		dst = meta.AppendField0(dst, o.Deterministic)
	}
	bodyOpts := o
	bodyOpts.SelfDescribing = false
	return encodeProtoReflectMsg(dst, m, bodyOpts)
}

func buildProtoReflectSchemaMeta(md protoreflect.MessageDescriptor, schemaDepth int, active map[protoreflect.FullName]bool) (*omnibuf.SchemaMeta, bool) {
	if md == nil || schemaDepth+1 > omnibuf.MaxNestingDepth {
		return nil, false
	}
	if active[md.FullName()] {
		return nil, true
	}
	if active == nil {
		active = make(map[protoreflect.FullName]bool)
	}
	active[md.FullName()] = true
	defer delete(active, md.FullName())

	cyclic := false
	fields := md.Fields()
	nFields := fields.Len()
	meta := &omnibuf.SchemaMeta{
		TypeName: string(md.Name()),
		Fields:   make(map[uint32]string, nFields),
	}
	for i := range nFields {
		fd := fields.Get(i)
		fnum := uint32(fd.Number())
		meta.Fields[fnum] = string(fd.Name())
		var childMD protoreflect.MessageDescriptor
		if fd.IsMap() && fd.MapValue().Kind() == protoreflect.MessageKind {
			childMD = fd.MapValue().Message()
		} else if !fd.IsMap() && fd.Kind() == protoreflect.MessageKind {
			childMD = fd.Message()
		}
		if childMD != nil && schemaDepth+3 <= omnibuf.MaxNestingDepth {
			child, childCyclic := buildProtoReflectSchemaMeta(childMD, schemaDepth+2, active)
			if childCyclic {
				cyclic = true
			}
			if child != nil {
				if meta.Children == nil {
					meta.Children = make(map[uint32]*omnibuf.SchemaMeta)
				}
				meta.Children[fnum] = child
			}
		}
	}
	return meta, cyclic
}

func buildProtoReflectValueSchemaMeta(m protoreflect.Message, schemaDepth int) *omnibuf.SchemaMeta {
	if m == nil || schemaDepth+1 > omnibuf.MaxNestingDepth {
		return nil
	}
	md := m.Descriptor()
	fields := md.Fields()
	nFields := fields.Len()
	meta := &omnibuf.SchemaMeta{
		TypeName: string(md.Name()),
		Fields:   make(map[uint32]string, nFields),
	}
	for i := range nFields {
		fd := fields.Get(i)
		fnum := uint32(fd.Number())
		meta.Fields[fnum] = string(fd.Name())
		if schemaDepth+3 > omnibuf.MaxNestingDepth {
			continue
		}
		var child *omnibuf.SchemaMeta
		if fd.IsMap() {
			if fd.MapValue().Kind() == protoreflect.MessageKind && m.Has(fd) {
				mp := m.Get(fd).Map()
				mp.Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
					child = mergeSchemaMeta(child, buildProtoReflectValueSchemaMeta(v.Message(), schemaDepth+2))
					return true
				})
			}
		} else if fd.IsList() {
			if fd.Kind() == protoreflect.MessageKind && m.Has(fd) {
				list := m.Get(fd).List()
				for j := 0; j < list.Len(); j++ {
					child = mergeSchemaMeta(child, buildProtoReflectValueSchemaMeta(list.Get(j).Message(), schemaDepth+2))
				}
			}
		} else if fd.Kind() == protoreflect.MessageKind && m.Has(fd) {
			child = buildProtoReflectValueSchemaMeta(m.Get(fd).Message(), schemaDepth+2)
		}
		if child != nil {
			if meta.Children == nil {
				meta.Children = make(map[uint32]*omnibuf.SchemaMeta)
			}
			meta.Children[fnum] = child
		}
	}
	return meta
}

func appendRawTag(dst []byte, fieldNum uint32, wt uint8) []byte {
	tag := (uint64(fieldNum) << 4) | uint64(wt&0x0F)
	return wire.AppendVarint(dst, tag)
}

func sortProtoMapKeys(kd protoreflect.FieldDescriptor, keys []protoreflect.MapKey) {
	kind := kd.Kind()
	slices.SortFunc(keys, func(a, b protoreflect.MapKey) int {
		switch kind {
		case protoreflect.BoolKind:
			ab, bb := a.Bool(), b.Bool()
			if ab == bb {
				return 0
			}
			if !ab && bb {
				return -1
			}
			return 1
		case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
			protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
			return cmp.Compare(a.Int(), b.Int())
		case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
			protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
			return cmp.Compare(a.Uint(), b.Uint())
		default:
			return cmp.Compare(a.String(), b.String())
		}
	})
}

func protoScalarWireType(k protoreflect.Kind) (uint8, error) {
	switch k {
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		return wire.WireUint, nil
	case protoreflect.EnumKind, protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind:
		return wire.WireSint, nil
	case protoreflect.StringKind:
		return wire.WireString, nil
	case protoreflect.BytesKind:
		return wire.WireBytes, nil
	case protoreflect.MessageKind:
		return wire.WireSubMsg, nil
	case protoreflect.FloatKind:
		return wire.WireFloat32, nil
	case protoreflect.DoubleKind:
		return wire.WireFloat64, nil
	case protoreflect.Fixed32Kind:
		return wire.WireFixed32, nil
	case protoreflect.Fixed64Kind:
		return wire.WireFixed64, nil
	case protoreflect.Sfixed32Kind:
		return wire.WireSfixed32, nil
	case protoreflect.Sfixed64Kind:
		return wire.WireSfixed64, nil
	case protoreflect.BoolKind:
		return wire.WireBoolFalse, nil
	default:
		return 0, fmt.Errorf("omnibuf: unsupported kind %v", k)
	}
}

func protoFixed32Bits(k protoreflect.Kind, v protoreflect.Value) uint32 {
	switch k {
	case protoreflect.FloatKind:
		return math.Float32bits(float32(v.Float()))
	case protoreflect.Fixed32Kind:
		return uint32(v.Uint())
	default:
		return uint32(int32(v.Int()))
	}
}

func protoFixed64Bits(k protoreflect.Kind, v protoreflect.Value) uint64 {
	switch k {
	case protoreflect.DoubleKind:
		return math.Float64bits(v.Float())
	case protoreflect.Fixed64Kind:
		return v.Uint()
	default:
		return uint64(v.Int())
	}
}

func protoValueFromFixed32(k protoreflect.Kind, bits uint32) protoreflect.Value {
	switch k {
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(math.Float32frombits(bits))
	case protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(bits)
	default:
		return protoreflect.ValueOfInt32(int32(bits))
	}
}

func protoValueFromFixed64(k protoreflect.Kind, bits uint64) protoreflect.Value {
	switch k {
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(math.Float64frombits(bits))
	case protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(bits)
	default:
		return protoreflect.ValueOfInt64(int64(bits))
	}
}

func appendProtoScalarPayload(dst []byte, k protoreflect.Kind, v protoreflect.Value) ([]byte, error) {
	switch k {
	case protoreflect.BoolKind:
		if v.Bool() {
			return append(dst, 1), nil
		}
		return append(dst, 0), nil

	case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		return wire.AppendVarint(dst, v.Uint()), nil
	case protoreflect.EnumKind:
		return wire.AppendVarint(dst, wire.EncodeZigZag(int64(v.Enum()))), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind:
		return wire.AppendVarint(dst, wire.EncodeZigZag(v.Int())), nil
	case protoreflect.StringKind:
		s := v.String()
		if !wire.ValidUTF8([]byte(s)) {
			return nil, fmt.Errorf("omnibuf: string contains invalid UTF-8 (use bytes for binary data)")
		}
		dst = wire.AppendVarint(dst, uint64(len(s)))
		return append(dst, s...), nil
	case protoreflect.BytesKind:
		b := v.Bytes()
		dst = wire.AppendVarint(dst, uint64(len(b)))
		return append(dst, b...), nil
	case protoreflect.FloatKind, protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		bits := protoFixed32Bits(k, v)
		return append(dst, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24)), nil
	case protoreflect.DoubleKind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		bits := protoFixed64Bits(k, v)
		return append(dst,
			byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
			byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56),
		), nil
	default:
		return nil, fmt.Errorf("omnibuf: unsupported scalar kind %v", k)
	}
}

func decodeProtoScalarPayload(data []byte, pos int, k protoreflect.Kind) (protoreflect.Value, int, error) {
	switch k {
	case protoreflect.BoolKind:
		if pos >= len(data) || data[pos] > 1 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: invalid boolean")
		}
		return protoreflect.ValueOfBool(data[pos] == 1), pos + 1, nil

	case protoreflect.Uint32Kind:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: truncated uint32 at %d", pos)
		}
		return protoreflect.ValueOfUint32(uint32(v)), pos + vn, nil
	case protoreflect.Uint64Kind:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: truncated uint64 at %d", pos)
		}
		return protoreflect.ValueOfUint64(v), pos + vn, nil
	case protoreflect.EnumKind:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: truncated enum at %d", pos)
		}
		return protoreflect.ValueOfEnum(protoreflect.EnumNumber(wire.DecodeZigZag(v))), pos + vn, nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: truncated int32 at %d", pos)
		}
		return protoreflect.ValueOfInt32(int32(wire.DecodeZigZag(v))), pos + vn, nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: truncated int64 at %d", pos)
		}
		return protoreflect.ValueOfInt64(wire.DecodeZigZag(v)), pos + vn, nil
	case protoreflect.StringKind:
		s, nextPos, err := readStrSlice(data, pos)
		if err != nil {
			return protoreflect.Value{}, 0, err
		}
		return protoreflect.ValueOfString(string(s)), nextPos, nil
	case protoreflect.BytesKind:
		b, nextPos, err := readLenSlice(data, pos, 0)
		if err != nil {
			return protoreflect.Value{}, 0, err
		}
		cp := make([]byte, len(b))
		copy(cp, b)
		return protoreflect.ValueOfBytes(cp), nextPos, nil
	case protoreflect.FloatKind, protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		if len(data)-pos < 4 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: truncated fixed32 at %d", pos)
		}
		return protoValueFromFixed32(k, binary.LittleEndian.Uint32(data[pos:pos+4])), pos + 4, nil
	case protoreflect.DoubleKind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		if len(data)-pos < 8 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: truncated fixed64 at %d", pos)
		}
		return protoValueFromFixed64(k, binary.LittleEndian.Uint64(data[pos:pos+8])), pos + 8, nil
	default:
		return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: unsupported scalar kind %v", k)
	}
}

func encodeProtoReflectMsg(dst []byte, m protoreflect.Message, opts MarshalOptions) ([]byte, error) {
	md := m.Descriptor()
	if md.ExtensionRanges().Len() > 0 {
		var extFD protoreflect.FieldDescriptor
		m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
			if fd.IsExtension() {
				extFD = fd
				return false
			}
			return true
		})
		if extFD != nil {
			return nil, fmt.Errorf("omnibuf: protobuf extensions are not supported (%s)", extFD.FullName())
		}
	}
	fields := md.Fields()
	nFields := fields.Len()

	for i := range nFields {
		fd := fields.Get(i)
		fnum := uint32(fd.Number())
		if fd.HasPresence() && !m.Has(fd) {
			continue
		}
		val := m.Get(fd)

		if fd.IsMap() {
			mp := val.Map()
			keyWT, err := protoScalarWireType(fd.MapKey().Kind())
			if err != nil {
				return nil, fmt.Errorf("omnibuf: field %s: %w", fd.Name(), err)
			}
			valWT, err := protoScalarWireType(fd.MapValue().Kind())
			if err != nil {
				return nil, fmt.Errorf("omnibuf: field %s: %w", fd.Name(), err)
			}
			dst = appendRawTag(dst, fnum, wire.WireMap)
			lenPos := len(dst)
			dst = append(dst, 0, 0, keyWT, valWT)
			dst = wire.AppendVarint(dst, uint64(mp.Len()))
			if opts.Deterministic && mp.Len() > 1 {
				keys := make([]protoreflect.MapKey, 0, mp.Len())
				mp.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
					keys = append(keys, k)
					return true
				})
				sortProtoMapKeys(fd.MapKey(), keys)
				for _, k := range keys {
					dst, err = appendProtoScalarPayload(dst, fd.MapKey().Kind(), k.Value())
					if err != nil {
						return nil, err
					}
					dst, err = appendMapEntryVal(dst, fd.MapValue(), mp.Get(k), opts)
					if err != nil {
						return nil, err
					}
				}
			} else {
				var mapErr error
				mp.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
					dst, mapErr = appendProtoScalarPayload(dst, fd.MapKey().Kind(), k.Value())
					if mapErr != nil {
						return false
					}
					dst, mapErr = appendMapEntryVal(dst, fd.MapValue(), v, opts)
					return mapErr == nil
				})
				if mapErr != nil {
					return nil, mapErr
				}
			}
			dst = wire.BackpatchContainerLen(dst, lenPos)
			continue
		}

		if fd.IsList() {
			list := val.List()
			elem, err := protoScalarWireType(fd.Kind())
			if err != nil {
				return nil, err
			}
			dst = appendRawTag(dst, fnum, wire.WireArray)
			lenPos := len(dst)
			dst = append(dst, 0, 0, elem)
			dst = wire.AppendVarint(dst, uint64(list.Len()))
			for j := 0; j < list.Len(); j++ {
				if elem == wire.WireSubMsg {
					p := len(dst)
					dst = append(dst, 0, 0)
					dst, err = encodeProtoReflectMsg(dst, list.Get(j).Message(), opts)
					if err != nil {
						return nil, err
					}
					dst = wire.BackpatchContainerLen(dst, p)
				} else {
					dst, err = appendProtoScalarPayload(dst, fd.Kind(), list.Get(j))
					if err != nil {
						return nil, err
					}
				}
			}
			dst = wire.BackpatchContainerLen(dst, lenPos)
			continue
		}

		// Singular field:
		switch fd.Kind() {
		case protoreflect.MessageKind:
			if !m.Has(fd) {
				continue
			}
			dst = appendRawTag(dst, fnum, wire.WireSubMsg)
			lenPos := len(dst)
			dst = append(dst, 0, 0)
			var err error
			dst, err = encodeProtoReflectMsg(dst, val.Message(), opts)
			if err != nil {
				return nil, err
			}
			dst = wire.BackpatchContainerLen(dst, lenPos)
		case protoreflect.BoolKind:
			wt := wire.WireBoolFalse
			if val.Bool() {
				wt = wire.WireBoolTrue
			}
			dst = appendRawTag(dst, fnum, wt)
		default:
			wt, err := protoScalarWireType(fd.Kind())
			if err != nil {
				return nil, err
			}
			dst = appendRawTag(dst, fnum, wt)
			dst, err = appendProtoScalarPayload(dst, fd.Kind(), val)
			if err != nil {
				return nil, err
			}
		}
	}
	return dst, nil
}

func unmarshalProtoReflect(data []byte, m protoreflect.Message) error {
	if m == nil || !m.IsValid() {
		return fmt.Errorf("omnibuf: Unmarshal requires non-nil protoreflect.Message")
	}
	// Like proto.Unmarshal, decoding replaces the destination message:
	// decodeProtoReflectMsg (merge=false) clears every field absent from data.
	if err := decodeProtoReflectMsg(data, m, 0, false); err != nil {
		return err
	}
	return proto.CheckInitialized(m.Interface())
}

func decodeProtoReflectMsg(data []byte, m protoreflect.Message, depth int, merge bool) error {
	if depth > omnibuf.MaxNestingDepth {
		return fmt.Errorf("omnibuf: nesting depth %d exceeds omnibuf.MaxNestingDepth (%d)", depth, omnibuf.MaxNestingDepth)
	}
	fields := m.Descriptor().Fields()
	n := len(data)
	pos := 0
	seen := make(map[uint32]bool)

	for pos < n {
		tag, tn := wire.DecodeVarint(data[pos:])
		if tn == 0 || tag>>4 > (1<<29)-1 {
			return fmt.Errorf("omnibuf: truncated or invalid tag at %d", pos)
		}
		pos += tn
		fnum := uint32(tag >> 4)
		wt := uint8(tag & 0x0F)

		if fnum == 0 {
			nextPos, err := validateField0(data, pos, wt, depth)
			if err != nil {
				return err
			}
			pos = nextPos
			continue
		}

		fd := fields.ByNumber(protoreflect.FieldNumber(fnum))
		if fd == nil {
			nextPos, err := omnibuf.ValidateValue(data, pos, wt, depth)
			if err != nil {
				return err
			}
			pos = nextPos
			continue
		}

		if fd.IsMap() {
			if wt != wire.WireMap {
				nextPos, err := omnibuf.ValidateValue(data, pos, wt, depth)
				if err != nil {
					return err
				}
				pos = nextPos
				continue
			}
			if depth+1 > omnibuf.MaxNestingDepth {
				return fmt.Errorf("omnibuf: nesting depth %d exceeds omnibuf.MaxNestingDepth (%d)", depth+1, omnibuf.MaxNestingDepth)
			}
			alreadySeen := seen[fnum] || merge
			seen[fnum] = true
			body, nextPos, err := readLenSlice(data, pos, 3)
			if err != nil {
				return err
			}
			keyWT, valWT, count, p, ok := wire.ParseMapHeader(body)
			if !ok {
				return fmt.Errorf("omnibuf: invalid wire.WireMap header at %d", pos)
			}
			expKeyWT, err := protoScalarWireType(fd.MapKey().Kind())
			if err != nil || keyWT != expKeyWT {
				return fmt.Errorf("omnibuf: wire.WireMap key wire type mismatch at %d", pos)
			}
			expValWT, err := protoScalarWireType(fd.MapValue().Kind())
			if err != nil || valWT != expValWT {
				return fmt.Errorf("omnibuf: wire.WireMap val wire type mismatch at %d", pos)
			}
			mp := m.Mutable(fd).Map()
			if !alreadySeen {
				mp.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
					mp.Clear(k)
					return true
				})
			}
			for range count {
				kv, nextP, err := decodeProtoScalarPayload(body, p, fd.MapKey().Kind())
				if err != nil {
					return err
				}
				mv, nextP2, err := decodeMapEntryVal(body, nextP, fd.MapValue(), mp, depth+1)
				if err != nil {
					return err
				}
				p = nextP2
				mp.Set(kv.MapKey(), mv)
			}
			if p != len(body) {
				return fmt.Errorf("omnibuf: wire.WireMap trailing bytes at %d", p)
			}
			pos = nextPos
			continue
		}

		if fd.IsList() {
			if wt != wire.WireArray {
				nextPos, err := omnibuf.ValidateValue(data, pos, wt, depth)
				if err != nil {
					return err
				}
				pos = nextPos
				continue
			}
			if depth+1 > omnibuf.MaxNestingDepth {
				return fmt.Errorf("omnibuf: nesting depth %d exceeds omnibuf.MaxNestingDepth (%d)", depth+1, omnibuf.MaxNestingDepth)
			}
			alreadySeen := seen[fnum] || merge
			seen[fnum] = true
			body, next, err := readLenSlice(data, pos, 2)
			if err != nil {
				return err
			}
			elem, count, p, ok := wire.ParseArrayHeader(body)
			expected, err := protoScalarWireType(fd.Kind())
			if !ok || err != nil || elem != expected {
				return fmt.Errorf("omnibuf: invalid array element type on %s", fd.FullName())
			}
			list := m.Mutable(fd).List()
			if !alreadySeen {
				list.Truncate(0)
			}
			for j := 0; j < count; j++ {
				if elem == wire.WireSubMsg {
					sub, nextP, err := readLenSlice(body, p, 0)
					if err != nil {
						return err
					}
					v := list.NewElement().Message()
					if err := decodeProtoReflectMsg(sub, v, depth+2, false); err != nil {
						return err
					}
					list.Append(protoreflect.ValueOfMessage(v))
					p = nextP
				} else {
					v, nextP, err := decodeProtoScalarPayload(body, p, fd.Kind())
					if err != nil {
						return err
					}
					list.Append(v)
					p = nextP
				}
			}
			if p != len(body) {
				return fmt.Errorf("omnibuf: array count/length mismatch")
			}
			pos = next
			continue
		}

		// Singular field:
		if fd.Kind() == protoreflect.BoolKind {
			if wt != wire.WireBoolFalse && wt != wire.WireBoolTrue {
				nextPos, err := omnibuf.ValidateValue(data, pos, wt, depth)
				if err != nil {
					return err
				}
				pos = nextPos
				continue
			}
			seen[fnum] = true
			m.Set(fd, protoreflect.ValueOfBool(wt == wire.WireBoolTrue))
			continue
		}

		expWT, err := protoScalarWireType(fd.Kind())
		if err != nil {
			return fmt.Errorf("omnibuf: field %s: %w", fd.FullName(), err)
		}
		if wt != expWT {
			// Known field with a different wire type: validated and skipped
			// like an unknown field (Protobuf semantics, shared by all tiers).
			nextPos, err := omnibuf.ValidateValue(data, pos, wt, depth)
			if err != nil {
				return err
			}
			pos = nextPos
			continue
		}

		alreadySeen := seen[fnum] || merge
		seen[fnum] = true

		if fd.Kind() == protoreflect.MessageKind {
			subBytes, nextPos, err := readLenSlice(data, pos, 0)
			if err != nil {
				return err
			}
			subMsg := m.Mutable(fd).Message()
			if err := decodeProtoReflectMsg(subBytes, subMsg, depth+1, alreadySeen); err != nil {
				return err
			}
			pos = nextPos
			continue
		}

		sv, nextPos, err := decodeProtoScalarPayload(data, pos, fd.Kind())
		if err != nil {
			return err
		}
		m.Set(fd, sv)
		pos = nextPos
	}
	if !merge {
		for i := range fields.Len() {
			fd := fields.Get(i)
			if !seen[uint32(fd.Number())] && m.Has(fd) {
				m.Clear(fd)
			}
		}
		if m.Descriptor().ExtensionRanges().Len() > 0 {
			var staleExts []protoreflect.FieldDescriptor
			m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
				if fd.IsExtension() {
					staleExts = append(staleExts, fd)
				}
				return true
			})
			for _, fd := range staleExts {
				m.Clear(fd)
			}
		}
		if len(m.GetUnknown()) > 0 {
			m.SetUnknown(nil)
		}
	}
	return nil
}

func appendMapEntryVal(dst []byte, vd protoreflect.FieldDescriptor, v protoreflect.Value, opts MarshalOptions) ([]byte, error) {
	switch vd.Kind() {
	case protoreflect.BoolKind:
		if v.Bool() {
			return append(dst, 1), nil
		}
		return append(dst, 0), nil
	case protoreflect.MessageKind:
		lenPos := len(dst)
		dst = append(dst, 0, 0)
		var err error
		dst, err = encodeProtoReflectMsg(dst, v.Message(), opts)
		if err != nil {
			return nil, err
		}
		return wire.BackpatchContainerLen(dst, lenPos), nil
	default:
		return appendProtoScalarPayload(dst, vd.Kind(), v)
	}
}

func decodeMapEntryVal(body []byte, p int, vd protoreflect.FieldDescriptor, mp protoreflect.Map, depth int) (protoreflect.Value, int, error) {
	switch vd.Kind() {
	case protoreflect.BoolKind:
		if len(body)-p < 1 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: truncated wire.WireMap bool val at %d", p)
		}
		b := body[p]
		if b != 0 && b != 1 {
			return protoreflect.Value{}, 0, fmt.Errorf("omnibuf: invalid wire.WireMap bool val %d at %d", b, p)
		}
		return protoreflect.ValueOfBool(b == 1), p + 1, nil
	case protoreflect.MessageKind:
		subBytes, nextP, err := readLenSlice(body, p, 0)
		if err != nil {
			return protoreflect.Value{}, 0, err
		}
		subMsg := mp.NewValue().Message()
		if err := decodeProtoReflectMsg(subBytes, subMsg, depth+1, false); err != nil {
			return protoreflect.Value{}, 0, err
		}
		return protoreflect.ValueOfMessage(subMsg), nextP, nil
	default:
		return decodeProtoScalarPayload(body, p, vd.Kind())
	}
}
