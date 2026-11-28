package omnibuf

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/omnibuf/omnibuf-go/internal/protohook"
	"github.com/omnibuf/omnibuf-go/wire"
)

// ============================================================================
// Generic Table-Driven Marshal / Unmarshal for Go Structs & .pb.go Structs
// ============================================================================
//
// Works directly on:
//   1. Plain Go structs annotated with `omnibuf:"<fieldNum>[,name=<name>][,wire=<wireType>][,omitempty]"`
//   2. Existing `protoc-gen-go` generated structs (e.g. *pb.PeerUpdate) with `protobuf:"..."` tags
//
// Just like google.golang.org/protobuf's internal `protoimpl` table-driven engine,
// OmniBuf inspects the struct's reflection metadata ONCE per reflect.Type, compiles a
// flat array of `fieldCodec` descriptors with precomputed `unsafe.Pointer` byte offsets,
// and executes Marshal / Unmarshal using direct pointer arithmetic.

const maxDirectFieldSlot = 16

type fieldKind uint8

const (
	kindUint8 fieldKind = iota
	kindUint16
	kindUint32
	kindUint64
	kindUint8Sint
	kindUint16Sint
	kindUint32Sint
	kindUint64Sint
	kindInt8
	kindInt16
	kindInt32
	kindInt64
	kindSint32
	kindSint64
	kindInt8Uint
	kindInt16Uint
	kindInt32Uint
	kindInt64Uint
	kindBool
	kindFloat32
	kindFloat64
	kindFixed32
	kindFixed64
	kindFixed32_8
	kindFixed32_16
	kindFixed32_64
	kindSfixed32_8
	kindSfixed32_16
	kindSfixed32_64
	kindFixed64_8
	kindFixed64_16
	kindFixed64_32
	kindSfixed64_8
	kindSfixed64_16
	kindSfixed64_32
	kindString
	kindBytes
	kindStringSlice
	kindBytesSlice
	kindBoolSlice
	kindUint8Slice
	kindUint16Slice
	kindUint32Slice
	kindUint64Slice
	kindUint8SintSlice
	kindUint16SintSlice
	kindUint32SintSlice
	kindUint64SintSlice
	kindInt8Slice
	kindInt16Slice
	kindInt32Slice
	kindInt64Slice
	kindFloat32Slice
	kindFloat64Slice
	kindUint8Fixed32Slice
	kindUint16Fixed32Slice
	kindUint64Fixed32Slice
	kindInt8Fixed32Slice
	kindInt16Fixed32Slice
	kindInt64Fixed32Slice
	kindUint8Fixed64Slice
	kindUint16Fixed64Slice
	kindUint32Fixed64Slice
	kindInt8Fixed64Slice
	kindInt16Fixed64Slice
	kindInt32Fixed64Slice
	kindStructVal
	kindStructPtr
	kindScalarPtr
	kindStructValSlice
	kindStructPtrSlice
	kindMapStringUint
	kindMap
)

type fieldCodec struct {
	elemWireType uint8
	wireType     uint8
	tagLen       uint8 // 1, 2, 4, or 9
	omitEmpty    bool
	kind         fieldKind
	fieldNum     uint32
	tag1B        byte   // Precomputed 1-byte Prefix-Varint tag if fieldNum <= 7
	tag2B        uint16 // Precomputed 2-byte Prefix-Varint tag if 8 <= fieldNum <= 1023
	tag4B        uint32 // Precomputed 4-byte Prefix-Varint tag if 1024 <= fieldNum <= 33554431
	tag8B        uint64 // Raw tag for 9-byte Prefix-Varint if fieldNum >= 33554432
	name         string
	offset       uintptr
	subCodec     *structCodec
	elemCodec    *fieldCodec
	elemSize     uintptr
	elemHasPtrs  bool
	sliceType    reflect.Type
	elemType     reflect.Type
	mapKeyWT     uint8
	mapValWT     uint8
	index        int          // position in structCodec.fields (for seen tracking)
	goKind       reflect.Kind // Go kind of the struct field (for clearing on reuse)
	goSize       uintptr      // Go size of the struct field (for clearing on reuse)
}

type structCodec struct {
	typ          reflect.Type
	size         uintptr
	fields       []fieldCodec
	byNum        [maxDirectFieldSlot + 1]*fieldCodec
	overflow     map[uint32]*fieldCodec
	expectedMask uint64 // all fields seen; overflow fields are checked separately
	schemaNames  []string
	metaOnce     sync.Once
	schemaMeta   *SchemaMeta
	field0Wire   []byte
	cyclicSchema bool
	protoState   ProtoStateOffsets
}

const noFieldOffset = ^uintptr(0)

// ProtoStateOffsets stores precomputed byte offsets for protoc-gen-go internal
// sizeCache and unknownFields fields so generated and table codecs can reset them
// without allocations or reflection on the hot path.
type ProtoStateOffsets struct {
	sizeCacheOff     uintptr
	unknownFieldsOff uintptr
}

// InspectProtoState inspects T once for protoc-gen-go sizeCache and unknownFields offsets.
func InspectProtoState[T any]() ProtoStateOffsets {
	return inspectProtoStateType(reflect.TypeFor[T]())
}

func inspectProtoStateType(t reflect.Type) ProtoStateOffsets {
	res := ProtoStateOffsets{
		sizeCacheOff:     noFieldOffset,
		unknownFieldsOff: noFieldOffset,
	}
	if t == nil || t.Kind() != reflect.Struct {
		return res
	}
	isProtoStruct := false
	var sizeOff = noFieldOffset
	var unkOff = noFieldOffset
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.IsExported() {
			if pbTag := sf.Tag.Get("protobuf"); pbTag != "" && pbTag != "-" {
				isProtoStruct = true
			}
			continue
		}
		switch sf.Name {
		case "state":
			if strings.Contains(sf.Type.PkgPath(), "protobuf") {
				isProtoStruct = true
			}
		case "sizeCache":
			if sf.Type.Kind() == reflect.Int32 && sf.Type.Size() == 4 {
				sizeOff = sf.Offset
			}
		case "unknownFields":
			if sf.Type.Kind() == reflect.Slice && sf.Type.Elem().Kind() == reflect.Uint8 {
				unkOff = sf.Offset
			}
		}
	}
	if isProtoStruct {
		res.sizeCacheOff = sizeOff
		res.unknownFieldsOff = unkOff
	}
	return res
}

// Reset clears sizeCache unconditionally and clears unknownFields when merge is false.
func (p ProtoStateOffsets) Reset(ptr unsafe.Pointer, merge bool) {
	if (p.sizeCacheOff & p.unknownFieldsOff) == noFieldOffset {
		return
	}
	if p.sizeCacheOff != noFieldOffset {
		sp := (*int32)(unsafe.Add(ptr, p.sizeCacheOff))
		if *sp != 0 {
			*sp = 0
		}
	}
	if !merge && p.unknownFieldsOff != noFieldOffset {
		up := (*[]byte)(unsafe.Add(ptr, p.unknownFieldsOff))
		if *up != nil {
			*up = nil
		}
	}
}

var codecCache sync.Map // map[reflect.Type]*structCodec

func getStructCodec(t reflect.Type) (*structCodec, error) {
	if c, ok := codecCache.Load(t); ok {
		return c.(*structCodec), nil
	}
	building := make(map[reflect.Type]*structCodec)
	c, err := compileStructCodec(t, building)
	if err != nil {
		return nil, err
	}
	for bt, bc := range building {
		codecCache.LoadOrStore(bt, bc)
	}
	actual, _ := codecCache.LoadOrStore(t, c)
	return actual.(*structCodec), nil
}

func (sc *structCodec) getField0Wire() []byte {
	sc.metaOnce.Do(func() {
		sc.schemaMeta, sc.cyclicSchema = buildStructSchemaMeta(sc, 1, nil)
		sc.field0Wire = appendField0SchemaMetaDeterministic(nil, sc.schemaMeta, true)
	})
	return sc.field0Wire
}

func buildStructSchemaMeta(sc *structCodec, schemaDepth int, active map[reflect.Type]bool) (*SchemaMeta, bool) {
	if sc == nil || schemaDepth+1 > MaxNestingDepth {
		return nil, false
	}
	if active[sc.typ] {
		return nil, true
	}
	if active == nil {
		active = make(map[reflect.Type]bool)
	}
	active[sc.typ] = true
	defer delete(active, sc.typ)

	cyclic := false
	meta := &SchemaMeta{
		TypeName: sc.typ.Name(),
		Fields:   make(map[uint32]string, len(sc.fields)),
	}
	for i := range sc.fields {
		fc := &sc.fields[i]
		fnum := fc.fieldNum
		meta.Fields[fnum] = fc.name
		if fc.subCodec != nil && schemaDepth+3 <= MaxNestingDepth {
			child, childCyclic := buildStructSchemaMeta(fc.subCodec, schemaDepth+2, active)
			if childCyclic {
				cyclic = true
			}
			if child != nil {
				if meta.Children == nil {
					meta.Children = make(map[uint32]*SchemaMeta)
				}
				meta.Children[fnum] = child
			}
		}
	}
	return meta, cyclic
}

func buildStructValueSchemaMeta(sc *structCodec, ptr unsafe.Pointer, schemaDepth int) *SchemaMeta {
	if sc == nil || ptr == nil || schemaDepth+1 > MaxNestingDepth {
		return nil
	}
	meta := &SchemaMeta{
		TypeName: sc.typ.Name(),
		Fields:   make(map[uint32]string, len(sc.fields)),
	}
	for i := range sc.fields {
		fc := &sc.fields[i]
		fnum := fc.fieldNum
		meta.Fields[fnum] = fc.name
		if fc.subCodec == nil || schemaDepth+3 > MaxNestingDepth {
			continue
		}
		fPtr := unsafe.Add(ptr, fc.offset)
		var child *SchemaMeta
		switch fc.kind {
		case kindStructVal:
			if !fc.omitEmpty || !isFieldEmpty(fc, fPtr) {
				child = buildStructValueSchemaMeta(fc.subCodec, fPtr, schemaDepth+2)
			}
		case kindStructPtr:
			if subPtr := *(*unsafe.Pointer)(fPtr); subPtr != nil {
				child = buildStructValueSchemaMeta(fc.subCodec, subPtr, schemaDepth+2)
			}
		case kindStructValSlice:
			sh := (*unsafeSliceHeader)(fPtr)
			for j := 0; j < sh.Len; j++ {
				elemPtr := unsafe.Add(sh.Data, uintptr(j)*fc.elemSize)
				child = mergeSchemaMeta(child, buildStructValueSchemaMeta(fc.subCodec, elemPtr, schemaDepth+2))
			}
		case kindStructPtrSlice:
			sh := (*unsafeSliceHeader)(fPtr)
			for j := 0; j < sh.Len; j++ {
				elemPtr := *(*unsafe.Pointer)(unsafe.Add(sh.Data, uintptr(j)*unsafe.Sizeof(uintptr(0))))
				if elemPtr != nil {
					child = mergeSchemaMeta(child, buildStructValueSchemaMeta(fc.subCodec, elemPtr, schemaDepth+2))
				}
			}
			if child == nil && sh.Len > 0 {
				child, _ = buildStructSchemaMeta(fc.subCodec, schemaDepth+2, map[reflect.Type]bool{sc.typ: true})
			}
		case kindMap:
			mv := reflect.NewAt(fc.sliceType, fPtr).Elem()
			if !mv.IsNil() && mv.Len() > 0 {
				iter := mv.MapRange()
				for iter.Next() {
					vv := iter.Value()
					if vv.Kind() == reflect.Pointer {
						if !vv.IsNil() {
							child = mergeSchemaMeta(child, buildStructValueSchemaMeta(fc.subCodec, vv.UnsafePointer(), schemaDepth+2))
						}
					} else {
						cp := reflect.New(vv.Type())
						cp.Elem().Set(vv)
						child = mergeSchemaMeta(child, buildStructValueSchemaMeta(fc.subCodec, cp.UnsafePointer(), schemaDepth+2))
					}
				}
			}
		}
		if child != nil {
			if meta.Children == nil {
				meta.Children = make(map[uint32]*SchemaMeta)
			}
			meta.Children[fnum] = child
		}
	}
	return meta
}

func compileStructCodec(t reflect.Type, building map[reflect.Type]*structCodec) (*structCodec, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("omnibuf: expected struct type, got %v", t)
	}
	if c, ok := codecCache.Load(t); ok {
		return c.(*structCodec), nil
	}
	if c, ok := building[t]; ok {
		return c, nil
	}
	sc := &structCodec{
		typ:        t,
		size:       t.Size(),
		protoState: inspectProtoStateType(t),
	}
	building[t] = sc

	seenNums := make(map[uint32]string, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		var (
			fc  fieldCodec
			err error
		)
		if obTag, hasOB := sf.Tag.Lookup("omnibuf"); hasOB {
			if obTag == "" || obTag == "-" {
				continue
			}
			fc, err = parseOmniBufField(sf, obTag, building)
		} else {
			pbTag := sf.Tag.Get("protobuf")
			if pbTag == "" || pbTag == "-" {
				continue
			}
			fc, err = parseProtobufField(sf, pbTag, building)
		}
		if err != nil {
			return nil, err
		}
		if prevName, exists := seenNums[fc.fieldNum]; exists {
			return nil, fmt.Errorf("omnibuf: duplicate field number %d on %s (already used by %s)", fc.fieldNum, sf.Name, prevName)
		}

		seenNums[fc.fieldNum] = sf.Name
		fc.goKind = sf.Type.Kind()
		fc.goSize = sf.Type.Size()
		sc.fields = append(sc.fields, fc)
	}

	sort.Slice(sc.fields, func(i, j int) bool {
		return sc.fields[i].fieldNum < sc.fields[j].fieldNum
	})

	sc.expectedMask = uint64(1)<<min(len(sc.fields), 64) - 1
	sc.schemaNames = make([]string, len(sc.fields))
	for i := range sc.fields {
		fc := &sc.fields[i]
		fc.index = i
		sc.schemaNames[i] = fc.name
		if fc.fieldNum <= maxDirectFieldSlot {
			sc.byNum[fc.fieldNum] = fc
		} else {
			if sc.overflow == nil {
				sc.overflow = make(map[uint32]*fieldCodec)
			}
			sc.overflow[fc.fieldNum] = fc
		}
	}
	return sc, nil
}

// parseOmniBufField parses an `omnibuf:"<fieldNum>[,name=<name>][,wire=<wireType>][,omitempty]"` tag.
func parseOmniBufField(sf reflect.StructField, obTag string, building map[reflect.Type]*structCodec) (fieldCodec, error) {
	parts := strings.Split(obTag, ",")
	fnumStr := strings.TrimSpace(parts[0])
	fnum64, err := strconv.ParseUint(fnumStr, 10, 32)
	if err != nil || fnum64 == 0 || fnum64 > maxProtobufFieldNumber || (fnum64 >= 19000 && fnum64 <= 19999) {
		return fieldCodec{}, fmt.Errorf("omnibuf: invalid field number in tag %q on %s", obTag, sf.Name)
	}
	fnum := uint32(fnum64)
	name := sf.Name
	wireOpt := ""
	omitEmpty := false

	for _, rawOpt := range parts[1:] {
		opt := strings.TrimSpace(rawOpt)
		switch {
		case opt == "omitempty":
			omitEmpty = true
		case strings.HasPrefix(opt, "name="):
			val := strings.TrimPrefix(opt, "name=")
			if val == "" {
				return fieldCodec{}, fmt.Errorf("omnibuf: empty name option in tag %q on %s", obTag, sf.Name)
			}
			name = val
		case strings.HasPrefix(opt, "wire="):
			val := strings.TrimPrefix(opt, "wire=")
			if val == "" {
				return fieldCodec{}, fmt.Errorf("omnibuf: empty wire option in tag %q on %s", obTag, sf.Name)
			}
			wireOpt = val
		default:
			return fieldCodec{}, fmt.Errorf("omnibuf: invalid option %q in tag %q on %s", rawOpt, obTag, sf.Name)
		}
	}

	fc := fieldCodec{
		fieldNum:  fnum,
		name:      name,
		offset:    sf.Offset,
		omitEmpty: omitEmpty,
	}
	if err := configureFieldCodec(&fc, sf, sf.Type, wireOpt, building); err != nil {
		return fieldCodec{}, err
	}
	precomputeFieldTag(&fc)
	return fc, nil
}

// parseProtobufField parses a `protoc-gen-go` `protobuf:"..."` tag.
func parseProtobufField(sf reflect.StructField, pbTag string, building map[reflect.Type]*structCodec) (fieldCodec, error) {
	parts := strings.Split(pbTag, ",")
	if len(parts) < 2 {
		return fieldCodec{}, fmt.Errorf("omnibuf: invalid protobuf tag %q on %s", pbTag, sf.Name)
	}
	encType := parts[0]
	fnum64, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || fnum64 == 0 || fnum64 > maxProtobufFieldNumber || (fnum64 >= 19000 && fnum64 <= 19999) {
		return fieldCodec{}, fmt.Errorf("omnibuf: invalid field number in tag %q on %s", pbTag, sf.Name)
	}
	fnum := uint32(fnum64)
	name := strings.ToLower(sf.Name)
	for _, p := range parts[2:] {
		if after, ok := strings.CutPrefix(p, "name="); ok {
			name = after
		}
	}

	wireOpt := ""
	switch encType {
	case "fixed32":
		wireOpt = "fixed32"
	case "fixed64":
		wireOpt = "fixed64"
	case "zigzag32", "zigzag64":
		wireOpt = "sint"
	}

	fc := fieldCodec{
		fieldNum: fnum,
		name:     name,
		offset:   sf.Offset,
	}
	if err := configureFieldCodec(&fc, sf, sf.Type, wireOpt, building); err != nil {
		return fieldCodec{}, err
	}
	if encType == "zigzag32" && fc.kind == kindInt32 {
		fc.kind = kindSint32
	} else if encType == "zigzag64" && fc.kind == kindInt64 {
		fc.kind = kindSint64
	}
	precomputeFieldTag(&fc)
	return fc, nil
}

func precomputeFieldTag(fc *fieldCodec) {
	rawTag := (uint64(fc.fieldNum) << 4) | uint64(fc.wireType)
	if rawTag <= 127 {
		fc.tagLen = 1
		fc.tag1B = byte(rawTag<<1) | 1
	} else if rawTag <= 16383 {
		fc.tagLen = 2
		fc.tag2B = uint16(rawTag<<2) | 2
	} else if rawTag <= 536870911 {
		fc.tagLen = 4
		fc.tag4B = uint32(rawTag<<3) | 4
	} else {
		fc.tagLen = 9
		fc.tag8B = rawTag
	}
}

var ptrIntBytes = unsafe.Sizeof(int(0))

func sizedIntKind(k reflect.Kind) reflect.Kind {
	switch k {
	case reflect.Int:
		if ptrIntBytes == 4 {
			return reflect.Int32
		}
		return reflect.Int64
	case reflect.Uint:
		if ptrIntBytes == 4 {
			return reflect.Uint32
		}
		return reflect.Uint64
	default:
		return k
	}
}

func configureFieldCodec(fc *fieldCodec, sf reflect.StructField, ft reflect.Type, wireOpt string, building map[reflect.Type]*structCodec) (err error) {
	defer func() {
		if err == nil {
			finalizeWireType(fc, ft, wireOpt)
			precomputeFieldTag(fc)
		}
	}()

	switch sizedIntKind(ft.Kind()) {
	case reflect.Uint8:
		switch wireOpt {
		case "", "uint":
			fc.kind = kindUint8
			fc.wireType = wire.WireUint
		case "sint":
			fc.kind = kindUint8Sint
			fc.wireType = wire.WireSint
		case "fixed32":
			fc.kind = kindFixed32_8
			fc.wireType = wire.WireFixed32
		case "fixed64":
			fc.kind = kindFixed64_8
			fc.wireType = wire.WireFixed64
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Uint16:
		switch wireOpt {
		case "", "uint":
			fc.kind = kindUint16
			fc.wireType = wire.WireUint
		case "sint":
			fc.kind = kindUint16Sint
			fc.wireType = wire.WireSint
		case "fixed32":
			fc.kind = kindFixed32_16
			fc.wireType = wire.WireFixed32
		case "fixed64":
			fc.kind = kindFixed64_16
			fc.wireType = wire.WireFixed64
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Uint32:
		switch wireOpt {
		case "", "uint":
			fc.kind = kindUint32
			fc.wireType = wire.WireUint
		case "sint":
			fc.kind = kindUint32Sint
			fc.wireType = wire.WireSint
		case "fixed32":
			fc.kind = kindFixed32
			fc.wireType = wire.WireFixed32
		case "fixed64":
			fc.kind = kindFixed64_32
			fc.wireType = wire.WireFixed64
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Uint64:
		switch wireOpt {
		case "", "uint":
			fc.kind = kindUint64
			fc.wireType = wire.WireUint
		case "sint":
			fc.kind = kindUint64Sint
			fc.wireType = wire.WireSint
		case "fixed32":
			fc.kind = kindFixed32_64
			fc.wireType = wire.WireFixed32
		case "fixed64":
			fc.kind = kindFixed64
			fc.wireType = wire.WireFixed64
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Int8:
		switch wireOpt {
		case "", "sint":
			fc.kind = kindInt8
			fc.wireType = wire.WireSint
		case "uint":
			fc.kind = kindInt8Uint
			fc.wireType = wire.WireUint
		case "fixed32", "sfixed32":
			fc.kind = kindSfixed32_8
			fc.wireType = wire.WireFixed32
		case "fixed64", "sfixed64":
			fc.kind = kindSfixed64_8
			fc.wireType = wire.WireFixed64
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Int16:
		switch wireOpt {
		case "", "sint":
			fc.kind = kindInt16
			fc.wireType = wire.WireSint
		case "uint":
			fc.kind = kindInt16Uint
			fc.wireType = wire.WireUint
		case "fixed32", "sfixed32":
			fc.kind = kindSfixed32_16
			fc.wireType = wire.WireFixed32
		case "fixed64", "sfixed64":
			fc.kind = kindSfixed64_16
			fc.wireType = wire.WireFixed64
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Int32:
		switch wireOpt {
		case "", "sint":
			fc.kind = kindInt32
			fc.wireType = wire.WireSint
		case "uint":
			fc.kind = kindInt32Uint
			fc.wireType = wire.WireUint
		case "fixed32", "sfixed32":
			fc.kind = kindFixed32
			fc.wireType = wire.WireFixed32
		case "fixed64", "sfixed64":
			fc.kind = kindSfixed64_32
			fc.wireType = wire.WireFixed64
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Int64:
		switch wireOpt {
		case "", "sint":
			fc.kind = kindInt64
			fc.wireType = wire.WireSint
		case "uint":
			fc.kind = kindInt64Uint
			fc.wireType = wire.WireUint
		case "fixed32", "sfixed32":
			fc.kind = kindSfixed32_64
			fc.wireType = wire.WireFixed32
		case "fixed64", "sfixed64":
			fc.kind = kindFixed64
			fc.wireType = wire.WireFixed64
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Bool:
		if wireOpt != "" && wireOpt != "bool" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for bool on %s", wireOpt, sf.Name)
		}
		fc.kind = kindBool
		fc.wireType = wire.WireBoolTrue
	case reflect.Float32:
		if wireOpt != "" && wireOpt != "fixed32" && wireOpt != "float32" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for float32 on %s", wireOpt, sf.Name)
		}
		fc.kind = kindFloat32
		fc.wireType = wire.WireFixed32
	case reflect.Float64:
		if wireOpt != "" && wireOpt != "fixed64" && wireOpt != "float64" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for float64 on %s", wireOpt, sf.Name)
		}
		fc.kind = kindFloat64
		fc.wireType = wire.WireFixed64
	case reflect.String:
		switch wireOpt {
		case "", "string":
			fc.kind = kindString
			fc.wireType = wire.WireString
		case "bytes":
			fc.kind = kindString
			fc.wireType = wire.WireBytes
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for string on %s", wireOpt, sf.Name)
		}
	case reflect.Struct:
		if wireOpt != "" && wireOpt != "submsg" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for struct on %s", wireOpt, sf.Name)
		}
		sub, err := compileStructCodec(ft, building)
		if err != nil {
			return err
		}
		fc.kind = kindStructVal
		fc.wireType = wire.WireSubMsg
		fc.subCodec = sub
	case reflect.Pointer:
		if ft.Elem().Kind() == reflect.Struct {
			if wireOpt != "" && wireOpt != "submsg" {
				return fmt.Errorf("omnibuf: unsupported wire type %q for struct pointer on %s", wireOpt, sf.Name)
			}
			sub, err := compileStructCodec(ft.Elem(), building)
			if err != nil {
				return err
			}
			fc.kind = kindStructPtr
			fc.wireType = wire.WireSubMsg
			fc.subCodec = sub
			fc.elemType = ft.Elem()
			return nil
		}
		elemCodec := &fieldCodec{
			fieldNum: fc.fieldNum,
			name:     fc.name,
			offset:   0,
		}
		if err := configureFieldCodec(elemCodec, sf, ft.Elem(), wireOpt, building); err != nil {
			return fmt.Errorf("omnibuf: unsupported pointer field %s: %w", sf.Name, err)
		}
		precomputeFieldTag(elemCodec)
		fc.kind = kindScalarPtr
		fc.wireType = elemCodec.wireType
		fc.elemType = ft.Elem()
		fc.elemCodec = elemCodec
		fc.subCodec = elemCodec.subCodec
	case reflect.Slice:
		return configureSliceFieldCodec(fc, sf, ft, wireOpt, building)
	case reflect.Map:
		return configureMapFieldCodec(fc, sf, ft, wireOpt, building)
	default:
		return fmt.Errorf("omnibuf: unsupported field type %v on %s", ft, sf.Name)
	}
	return nil
}

func configureSliceFieldCodec(fc *fieldCodec, sf reflect.StructField, ft reflect.Type, wireOpt string, building map[reflect.Type]*structCodec) error {
	elem := ft.Elem()
	switch sizedIntKind(elem.Kind()) {
	case reflect.Uint8:
		switch wireOpt {
		case "", "bytes":
			fc.kind = kindBytes
			fc.wireType = wire.WireBytes
		case "string":
			fc.kind = kindBytes
			fc.wireType = wire.WireString
		case "uint":
			fc.kind = kindUint8Slice
			fc.wireType = wire.WireArray
		case "sint":
			fc.kind = kindUint8SintSlice
			fc.wireType = wire.WireArray
		case "fixed32":
			fc.kind = kindUint8Fixed32Slice
			fc.wireType = wire.WireArray
		case "fixed64":
			fc.kind = kindUint8Fixed64Slice
			fc.wireType = wire.WireArray
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.String:
		if wireOpt != "" && wireOpt != "string" && wireOpt != "bytes" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
		fc.kind = kindStringSlice
		fc.wireType = wire.WireArray
		fc.elemWireType = wire.WireString
		if wireOpt == "bytes" {
			fc.elemWireType = wire.WireBytes
		}
	case reflect.Slice:
		if elem.Elem().Kind() == reflect.Uint8 {
			if wireOpt != "" && wireOpt != "bytes" && wireOpt != "string" {
				return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
			}
			fc.kind = kindBytesSlice
			fc.wireType = wire.WireArray
			fc.elemWireType = wire.WireBytes
			if wireOpt == "string" {
				fc.elemWireType = wire.WireString
			}
			return nil
		}
		return fmt.Errorf("omnibuf: unsupported slice type %v on %s", ft, sf.Name)
	case reflect.Bool:
		if wireOpt != "" && wireOpt != "bool" && wireOpt != "uint" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
		fc.kind = kindBoolSlice
		fc.wireType = wire.WireArray
	case reflect.Uint16:
		switch wireOpt {
		case "", "uint":
			fc.kind = kindUint16Slice
			fc.wireType = wire.WireArray
		case "sint":
			fc.kind = kindUint16SintSlice
			fc.wireType = wire.WireArray
		case "fixed32":
			fc.kind = kindUint16Fixed32Slice
			fc.wireType = wire.WireArray
		case "fixed64":
			fc.kind = kindUint16Fixed64Slice
			fc.wireType = wire.WireArray
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Uint32:
		switch wireOpt {
		case "", "uint":
			fc.kind = kindUint32Slice
			fc.wireType = wire.WireArray
		case "sint":
			fc.kind = kindUint32SintSlice
			fc.wireType = wire.WireArray
		case "fixed32":
			fc.kind = kindFloat32Slice
			fc.wireType = wire.WireArray
		case "fixed64":
			fc.kind = kindUint32Fixed64Slice
			fc.wireType = wire.WireArray
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Uint64:
		switch wireOpt {
		case "", "uint":
			fc.kind = kindUint64Slice
			fc.wireType = wire.WireArray
		case "sint":
			fc.kind = kindUint64SintSlice
			fc.wireType = wire.WireArray
		case "fixed32":
			fc.kind = kindUint64Fixed32Slice
			fc.wireType = wire.WireArray
		case "fixed64":
			fc.kind = kindFloat64Slice
			fc.wireType = wire.WireArray
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Int8:
		switch wireOpt {
		case "", "sint":
			fc.kind = kindInt8Slice
			fc.wireType = wire.WireArray
		case "uint":
			fc.kind = kindUint8Slice
			fc.wireType = wire.WireArray
		case "fixed32", "sfixed32":
			fc.kind = kindInt8Fixed32Slice
			fc.wireType = wire.WireArray
		case "fixed64", "sfixed64":
			fc.kind = kindInt8Fixed64Slice
			fc.wireType = wire.WireArray
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Int16:
		switch wireOpt {
		case "", "sint":
			fc.kind = kindInt16Slice
			fc.wireType = wire.WireArray
		case "uint":
			fc.kind = kindUint16Slice
			fc.wireType = wire.WireArray
		case "fixed32", "sfixed32":
			fc.kind = kindInt16Fixed32Slice
			fc.wireType = wire.WireArray
		case "fixed64", "sfixed64":
			fc.kind = kindInt16Fixed64Slice
			fc.wireType = wire.WireArray
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Int32:
		switch wireOpt {
		case "", "sint":
			fc.kind = kindInt32Slice
			fc.wireType = wire.WireArray
		case "uint":
			fc.kind = kindUint32Slice
			fc.wireType = wire.WireArray
		case "fixed32", "sfixed32":
			fc.kind = kindFloat32Slice
			fc.wireType = wire.WireArray
		case "fixed64", "sfixed64":
			fc.kind = kindInt32Fixed64Slice
			fc.wireType = wire.WireArray
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Int64:
		switch wireOpt {
		case "", "sint":
			fc.kind = kindInt64Slice
			fc.wireType = wire.WireArray
		case "uint":
			fc.kind = kindUint64Slice
			fc.wireType = wire.WireArray
		case "fixed32", "sfixed32":
			fc.kind = kindInt64Fixed32Slice
			fc.wireType = wire.WireArray
		case "fixed64", "sfixed64":
			fc.kind = kindFloat64Slice
			fc.wireType = wire.WireArray
		default:
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
	case reflect.Float32:
		if wireOpt != "" && wireOpt != "fixed32" && wireOpt != "float32" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
		fc.kind = kindFloat32Slice
		fc.wireType = wire.WireArray
	case reflect.Float64:
		if wireOpt != "" && wireOpt != "fixed64" && wireOpt != "float64" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
		fc.kind = kindFloat64Slice
		fc.wireType = wire.WireArray
	case reflect.Struct:
		if wireOpt != "" && wireOpt != "submsg" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
		sub, err := compileStructCodec(elem, building)
		if err != nil {
			return err
		}
		fc.kind = kindStructValSlice
		fc.wireType = wire.WireArray
		fc.subCodec = sub
		fc.elemSize = elem.Size()
		fc.elemHasPtrs = typeHasPointers(elem)
		fc.sliceType = ft
		fc.elemType = elem
	case reflect.Pointer:
		if elem.Elem().Kind() != reflect.Struct {
			return fmt.Errorf("omnibuf: unsupported slice element %v on %s", elem, sf.Name)
		}
		if wireOpt != "" && wireOpt != "submsg" {
			return fmt.Errorf("omnibuf: unsupported wire type %q for %v on %s", wireOpt, ft, sf.Name)
		}
		sub, err := compileStructCodec(elem.Elem(), building)
		if err != nil {
			return err
		}
		fc.kind = kindStructPtrSlice
		fc.wireType = wire.WireArray
		fc.subCodec = sub
		fc.elemSize = elem.Elem().Size()
		fc.sliceType = ft
		fc.elemType = elem.Elem()
	default:
		return fmt.Errorf("omnibuf: unsupported slice type %v on %s", ft, sf.Name)
	}
	return nil
}

func mapKeyWireType(kt reflect.Type, wireOpt string) (uint8, error) {
	switch kt.Kind() {
	case reflect.String:
		if wireOpt != "" && wireOpt != "string" {
			return 0, fmt.Errorf("omnibuf: unsupported map key wire type %q for %v", wireOpt, kt)
		}
		return wire.WireString, nil
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
		switch wireOpt {
		case "", "uint":
			return wire.WireUint, nil
		case "sint":
			return wire.WireSint, nil
		case "fixed32":
			return wire.WireFixed32, nil
		case "fixed64":
			return wire.WireFixed64, nil
		default:
			return 0, fmt.Errorf("omnibuf: unsupported map key wire type %q for %v", wireOpt, kt)
		}
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
		switch wireOpt {
		case "", "sint":
			return wire.WireSint, nil
		case "uint":
			return wire.WireUint, nil
		case "fixed32", "sfixed32":
			return wire.WireFixed32, nil
		case "fixed64", "sfixed64":
			return wire.WireFixed64, nil
		default:
			return 0, fmt.Errorf("omnibuf: unsupported map key wire type %q for %v", wireOpt, kt)
		}
	case reflect.Bool:
		if wireOpt != "" && wireOpt != "bool" {
			return 0, fmt.Errorf("omnibuf: unsupported map key wire type %q for %v", wireOpt, kt)
		}
		return wire.WireBoolTrue, nil
	default:
		return 0, fmt.Errorf("omnibuf: unsupported map key type %v", kt)
	}
}

func mapValWireType(vt reflect.Type, wireOpt string, building map[reflect.Type]*structCodec) (uint8, *structCodec, error) {
	switch vt.Kind() {
	case reflect.String:
		switch wireOpt {
		case "", "string":
			return wire.WireString, nil, nil
		case "bytes":
			return wire.WireBytes, nil, nil
		default:
			return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
		}
	case reflect.Slice:
		if vt.Elem().Kind() == reflect.Uint8 {
			switch wireOpt {
			case "", "bytes":
				return wire.WireBytes, nil, nil
			case "string":
				return wire.WireString, nil, nil
			default:
				return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
			}
		}
		return 0, nil, fmt.Errorf("omnibuf: unsupported map value type %v", vt)
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
		switch wireOpt {
		case "", "uint":
			return wire.WireUint, nil, nil
		case "sint":
			return wire.WireSint, nil, nil
		case "fixed32":
			return wire.WireFixed32, nil, nil
		case "fixed64":
			return wire.WireFixed64, nil, nil
		default:
			return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
		}
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
		switch wireOpt {
		case "", "sint":
			return wire.WireSint, nil, nil
		case "uint":
			return wire.WireUint, nil, nil
		case "fixed32", "sfixed32":
			return wire.WireFixed32, nil, nil
		case "fixed64", "sfixed64":
			return wire.WireFixed64, nil, nil
		default:
			return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
		}
	case reflect.Float32:
		if wireOpt != "" && wireOpt != "fixed32" && wireOpt != "float32" {
			return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
		}
		return wire.WireFixed32, nil, nil
	case reflect.Float64:
		if wireOpt != "" && wireOpt != "fixed64" && wireOpt != "float64" {
			return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
		}
		return wire.WireFixed64, nil, nil
	case reflect.Bool:
		if wireOpt != "" && wireOpt != "bool" {
			return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
		}
		return wire.WireBoolTrue, nil, nil
	case reflect.Struct:
		if wireOpt != "" && wireOpt != "submsg" {
			return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
		}
		sub, err := compileStructCodec(vt, building)
		if err != nil {
			return 0, nil, err
		}
		return wire.WireSubMsg, sub, nil
	case reflect.Pointer:
		if vt.Elem().Kind() == reflect.Struct {
			if wireOpt != "" && wireOpt != "submsg" {
				return 0, nil, fmt.Errorf("omnibuf: unsupported map value wire type %q for %v", wireOpt, vt)
			}
			sub, err := compileStructCodec(vt.Elem(), building)
			if err != nil {
				return 0, nil, err
			}
			return wire.WireSubMsg, sub, nil
		}
		return 0, nil, fmt.Errorf("omnibuf: unsupported map value type %v", vt)
	default:
		return 0, nil, fmt.Errorf("omnibuf: unsupported map value type %v", vt)
	}
}

func pbTagEncToWireOpt(pbSubTag string) string {
	if pbSubTag == "" {
		return ""
	}
	enc := strings.Split(pbSubTag, ",")[0]
	switch enc {
	case "fixed32":
		return "fixed32"
	case "fixed64":
		return "fixed64"
	case "zigzag32", "zigzag64":
		return "sint"
	default:
		return ""
	}
}

func configureMapFieldCodec(fc *fieldCodec, sf reflect.StructField, ft reflect.Type, wireOpt string, building map[reflect.Type]*structCodec) error {
	if wireOpt == "map" {
		wireOpt = ""
	}
	keyWireOpt := pbTagEncToWireOpt(sf.Tag.Get("protobuf_key"))
	valWireOpt := pbTagEncToWireOpt(sf.Tag.Get("protobuf_val"))
	if wireOpt != "" {
		valWireOpt = wireOpt
	}
	keyWT, err := mapKeyWireType(ft.Key(), keyWireOpt)
	if err != nil {
		return fmt.Errorf("omnibuf: field %s: %w", sf.Name, err)
	}
	valWT, subCodec, err := mapValWireType(ft.Elem(), valWireOpt, building)
	if err != nil {
		return fmt.Errorf("omnibuf: field %s: %w", sf.Name, err)
	}

	fc.wireType = wire.WireMap
	fc.mapKeyWT = keyWT
	fc.mapValWT = valWT
	fc.subCodec = subCodec
	fc.sliceType = ft
	fc.elemType = ft.Elem()

	if ft.Key() == reflect.TypeFor[string]() && ft.Elem() == reflect.TypeFor[uint64]() && keyWT == wire.WireString && valWT == wire.WireUint {
		fc.kind = kindMapStringUint
		return nil
	}
	fc.kind = kindMap
	return nil
}

// Marshal encodes any struct pointer (including plain Go structs with `omnibuf:"..."` tags
// and existing *pb.PeerUpdate protoc-gen-go structs) into a newly allocated OmniBuf packet.
func Marshal(v any) ([]byte, error) {
	return MarshalAppend(nil, v)
}

// MarshalAppend appends the OmniBuf encoding of struct pointer v to dst using the
// cached unsafe-offset table codec (0 reflection allocations on the hot path).
func MarshalAppend(dst []byte, v any) ([]byte, error) {
	return marshalTableAppend(dst, v, MarshalOptions{})
}

func marshalTableAppend(dst []byte, v any, opts MarshalOptions) ([]byte, error) {
	if protohook.MarshalHook != nil {
		if out, handled, err := protohook.MarshalHook(dst, v, opts.Deterministic, opts.SelfDescribing); handled {
			return out, err
		}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, fmt.Errorf("omnibuf: Marshal requires non-nil pointer to struct")
	}
	sc, err := getStructCodec(rv.Type().Elem())
	if err != nil {
		return nil, err
	}
	ptr := rv.UnsafePointer()
	if dst == nil {
		minSize := sc.minWireSize(ptr, 0)
		dst = make([]byte, 0, max(64, minSize+minSize/4))
	} else if cap(dst)-len(dst) < 64 {
		minSize := sc.minWireSize(ptr, 0)
		minCap := max(64, minSize+minSize/4)
		grown := make([]byte, len(dst), max(cap(dst)*2, len(dst)+minCap))
		copy(grown, dst)
		dst = grown
	}
	if opts.SelfDescribing {
		f0 := sc.getField0Wire()
		if sc.cyclicSchema {
			meta := buildStructValueSchemaMeta(sc, ptr, 1)
			dst = appendField0SchemaMetaDeterministic(dst, meta, opts.Deterministic)
		} else {
			dst = append(dst, f0...)
		}
	}
	st := encState{deterministic: opts.Deterministic}
	dst = sc.encodeStruct(dst, ptr, &st, 0)
	if st.depthErr {
		return nil, fmt.Errorf("%w: nesting depth exceeds MaxNestingDepth (%d)", ErrMaxDepth, MaxNestingDepth)
	}
	if st.badUTF8 != "" {
		return nil, fmt.Errorf("%w: field %s (use WireBytes / wire=bytes for binary data)", ErrInvalidUTF8, st.badUTF8)
	}
	return dst, nil
}

// encState carries per-call encoding options and the first validation error
// observed while encoding (encodeStruct itself stays allocation- and error-free
// on the hot path).
type encState struct {
	deterministic bool
	badUTF8       string // name of the first field holding invalid UTF-8
	depthErr      bool   // true if nesting depth exceeded MaxNestingDepth
}

func (st *encState) checkString(fc *fieldCodec, s string) {
	if st.badUTF8 == "" && !wire.ValidUTF8String(s) {
		st.badUTF8 = fc.name
	}
}

func (st *encState) checkBytes(fc *fieldCodec, b []byte) {
	if st.badUTF8 == "" && !wire.ValidUTF8(b) {
		st.badUTF8 = fc.name
	}
}

func isFieldEmpty(fc *fieldCodec, fPtr unsafe.Pointer) bool {
	switch fc.kind {
	case kindUint8, kindUint8Sint, kindInt8, kindInt8Uint, kindFixed32_8, kindSfixed32_8, kindFixed64_8, kindSfixed64_8:
		return *(*uint8)(fPtr) == 0
	case kindUint16, kindUint16Sint, kindInt16, kindInt16Uint, kindFixed32_16, kindSfixed32_16, kindFixed64_16, kindSfixed64_16:
		return *(*uint16)(fPtr) == 0
	case kindUint32, kindUint32Sint, kindInt32, kindSint32, kindInt32Uint, kindFixed32, kindFixed64_32, kindSfixed64_32:
		return *(*uint32)(fPtr) == 0
	case kindUint64, kindUint64Sint, kindInt64, kindSint64, kindInt64Uint, kindFixed64, kindFixed32_64, kindSfixed32_64:
		return *(*uint64)(fPtr) == 0
	case kindFloat32:
		return *(*uint32)(fPtr) == 0
	case kindFloat64:
		return *(*uint64)(fPtr) == 0
	case kindBool:
		return !*(*bool)(fPtr)
	case kindString:
		return len(*(*string)(fPtr)) == 0
	case kindBytes, kindStringSlice, kindBytesSlice, kindBoolSlice,
		kindUint8Slice, kindUint16Slice, kindUint32Slice, kindUint64Slice,
		kindUint8SintSlice, kindUint16SintSlice, kindUint32SintSlice, kindUint64SintSlice,
		kindInt8Slice, kindInt16Slice, kindInt32Slice, kindInt64Slice,
		kindFloat32Slice, kindFloat64Slice,
		kindUint8Fixed32Slice, kindUint16Fixed32Slice, kindUint64Fixed32Slice,
		kindInt8Fixed32Slice, kindInt16Fixed32Slice, kindInt64Fixed32Slice,
		kindUint8Fixed64Slice, kindUint16Fixed64Slice, kindUint32Fixed64Slice,
		kindInt8Fixed64Slice, kindInt16Fixed64Slice, kindInt32Fixed64Slice,
		kindStructValSlice, kindStructPtrSlice:
		return (*unsafeSliceHeader)(fPtr).Len == 0
	case kindStructPtr:
		return *(*unsafe.Pointer)(fPtr) == nil
	case kindScalarPtr:
		for fc.kind == kindScalarPtr {
			fPtr = *(*unsafe.Pointer)(fPtr)
			if fPtr == nil {
				return true
			}
			fc = fc.elemCodec
		}
		if fc.kind == kindStructPtr {
			return *(*unsafe.Pointer)(fPtr) == nil
		}
		return false
	case kindStructVal:
		return isStructEmpty(fc.subCodec, fPtr)
	case kindMapStringUint:
		return len(*(*map[string]uint64)(fPtr)) == 0
	case kindMap:
		return reflect.NewAt(fc.sliceType, fPtr).Elem().Len() == 0
	default:
		return false
	}
}

func (sc *structCodec) minWireSize(base unsafe.Pointer, depth int) int {
	if base == nil || depth > 8 {
		return 0
	}
	total := 0
	fields := sc.fields
	for i := range fields {
		fc := &fields[i]
		fPtr := unsafe.Add(base, fc.offset)
		switch fc.kind {
		case kindString:
			n := len(*(*string)(fPtr))
			if n > 0 || !fc.omitEmpty {
				total += 2 + n
			}
		case kindBytes:
			n := (*unsafeSliceHeader)(fPtr).Len
			if n > 0 || !fc.omitEmpty {
				total += 2 + n
			}
		case kindStructVal:
			sub := fc.subCodec.minWireSize(fPtr, depth+1)
			if sub > 0 {
				total += 3 + sub
			}
		case kindStructPtr:
			p := *(*unsafe.Pointer)(fPtr)
			if p != nil {
				total += 3 + fc.subCodec.minWireSize(p, depth+1)
			}
		case kindStructValSlice:
			sh := (*unsafeSliceHeader)(fPtr)
			if sh.Len > 0 {
				total += 4
				for j := range sh.Len {
					elemPtr := unsafe.Add(sh.Data, uintptr(j)*fc.elemSize)
					total += 2 + fc.subCodec.minWireSize(elemPtr, depth+1)
				}
			}
		case kindStructPtrSlice:
			ptrs := *(*[]unsafe.Pointer)(fPtr)
			if len(ptrs) > 0 {
				total += 4
				for j := range ptrs {
					total += 2
					if ptrs[j] != nil {
						total += fc.subCodec.minWireSize(ptrs[j], depth+1)
					}
				}
			}
		case kindStringSlice:
			ss := *(*[]string)(fPtr)
			if len(ss) > 0 {
				total += 4
				for j := range ss {
					total += 1 + len(ss[j])
				}
			}
		case kindBytesSlice:
			bs := *(*[][]byte)(fPtr)
			if len(bs) > 0 {
				total += 4
				for j := range bs {
					total += 1 + len(bs[j])
				}
			}
		}
	}
	return total
}

func isStructEmpty(sc *structCodec, base unsafe.Pointer) bool {
	for i := range sc.fields {
		fc := &sc.fields[i]
		if !isFieldEmpty(fc, unsafe.Add(base, fc.offset)) {
			return false
		}
	}
	return true
}

func (sc *structCodec) encodeStruct(dst []byte, base unsafe.Pointer, st *encState, depth int) []byte {
	if depth > MaxNestingDepth {
		st.depthErr = true
		return dst
	}
	fields := sc.fields
	for i := range fields {
		fc := &fields[i]
		fPtr := unsafe.Add(base, fc.offset)
		omitEmpty := fc.omitEmpty

		if fc.kind == kindScalarPtr {
			nilPtr := false
			for fc.kind == kindScalarPtr {
				fPtr = *(*unsafe.Pointer)(fPtr)
				if fPtr == nil {
					nilPtr = true
					break
				}
				fc = fc.elemCodec
			}
			if nilPtr {
				continue
			}
		}
		if fc.kind == kindStructPtr && *(*unsafe.Pointer)(fPtr) == nil {
			continue
		}
		if omitEmpty && isFieldEmpty(&fields[i], unsafe.Add(base, fields[i].offset)) {
			continue
		}
		if (fc.wireType == wire.WireArray || fc.wireType == wire.WireMap) && depth+1 > MaxNestingDepth {
			st.depthErr = true
			return dst
		}
		if fc.kind == kindBool {
			wt := wire.WireBoolFalse
			if *(*bool)(fPtr) {
				wt = wire.WireBoolTrue
			}
			rawTag := (uint64(fc.fieldNum) << 4) | uint64(wt)
			if rawTag <= 127 {
				dst = append(dst, byte(rawTag<<1)|1)
			} else if rawTag <= 16383 {
				u := uint16(rawTag<<2) | 2
				dst = append(dst, byte(u), byte(u>>8))
			} else if rawTag <= 536870911 {
				u := uint32(rawTag<<3) | 4
				dst = append(dst, byte(u), byte(u>>8), byte(u>>16), byte(u>>24))
			} else {
				dst = append(dst,
					0x00,
					byte(rawTag), byte(rawTag>>8), byte(rawTag>>16), byte(rawTag>>24),
					byte(rawTag>>32), byte(rawTag>>40), byte(rawTag>>48), byte(rawTag>>56),
				)
			}
			continue
		}

		switch fc.tagLen {
		case 1:
			dst = append(dst, fc.tag1B)
		case 2:
			dst = append(dst, byte(fc.tag2B), byte(fc.tag2B>>8))
		case 4:
			dst = append(dst, byte(fc.tag4B), byte(fc.tag4B>>8), byte(fc.tag4B>>16), byte(fc.tag4B>>24))
		default:
			dst = append(dst,
				0x00,
				byte(fc.tag8B), byte(fc.tag8B>>8), byte(fc.tag8B>>16), byte(fc.tag8B>>24),
				byte(fc.tag8B>>32), byte(fc.tag8B>>40), byte(fc.tag8B>>48), byte(fc.tag8B>>56),
			)
		}

		switch fc.kind {
		case kindUint8:
			dst = wire.AppendVarint(dst, uint64(*(*uint8)(fPtr)))
		case kindUint16:
			dst = wire.AppendVarint(dst, uint64(*(*uint16)(fPtr)))
		case kindUint32:
			dst = wire.AppendVarint(dst, uint64(*(*uint32)(fPtr)))
		case kindUint64:
			dst = wire.AppendVarint(dst, *(*uint64)(fPtr))
		case kindUint8Sint:
			dst = wire.AppendVarint(dst, wire.EncodeZigZag(int64(*(*uint8)(fPtr))))
		case kindUint16Sint:
			dst = wire.AppendVarint(dst, wire.EncodeZigZag(int64(*(*uint16)(fPtr))))
		case kindUint32Sint:
			dst = wire.AppendVarint(dst, wire.EncodeZigZag(int64(*(*uint32)(fPtr))))
		case kindUint64Sint:
			dst = wire.AppendVarint(dst, wire.EncodeZigZag(int64(*(*uint64)(fPtr))))
		case kindInt8:
			dst = wire.AppendVarint(dst, wire.EncodeZigZag(int64(*(*int8)(fPtr))))
		case kindInt16:
			dst = wire.AppendVarint(dst, wire.EncodeZigZag(int64(*(*int16)(fPtr))))
		case kindInt32, kindSint32:
			dst = wire.AppendVarint(dst, wire.EncodeZigZag(int64(*(*int32)(fPtr))))
		case kindInt64, kindSint64:
			dst = wire.AppendVarint(dst, wire.EncodeZigZag(*(*int64)(fPtr)))
		case kindInt8Uint:
			dst = wire.AppendVarint(dst, uint64(uint8(*(*int8)(fPtr))))
		case kindInt16Uint:
			dst = wire.AppendVarint(dst, uint64(uint16(*(*int16)(fPtr))))
		case kindInt32Uint:
			dst = wire.AppendVarint(dst, uint64(uint32(*(*int32)(fPtr))))
		case kindInt64Uint:
			dst = wire.AppendVarint(dst, uint64(*(*int64)(fPtr)))
		case kindFloat32, kindFixed32:
			v := *(*uint32)(fPtr)
			dst = append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		case kindFixed32_8:
			v := uint32(*(*uint8)(fPtr))
			dst = append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		case kindSfixed32_8:
			v := uint32(int32(*(*int8)(fPtr)))
			dst = append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		case kindFixed32_16:
			v := uint32(*(*uint16)(fPtr))
			dst = append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		case kindSfixed32_16:
			v := uint32(int32(*(*int16)(fPtr)))
			dst = append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		case kindFixed32_64, kindSfixed32_64:
			v := uint32(*(*uint64)(fPtr))
			dst = append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		case kindFloat64, kindFixed64:
			v := *(*uint64)(fPtr)
			dst = append(dst,
				byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
				byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56),
			)
		case kindFixed64_8:
			v := uint64(*(*uint8)(fPtr))
			dst = append(dst,
				byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
				byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56),
			)
		case kindSfixed64_8:
			v := uint64(int64(*(*int8)(fPtr)))
			dst = append(dst,
				byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
				byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56),
			)
		case kindFixed64_16:
			v := uint64(*(*uint16)(fPtr))
			dst = append(dst,
				byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
				byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56),
			)
		case kindSfixed64_16:
			v := uint64(int64(*(*int16)(fPtr)))
			dst = append(dst,
				byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
				byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56),
			)
		case kindFixed64_32:
			v := uint64(*(*uint32)(fPtr))
			dst = append(dst,
				byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
				byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56),
			)
		case kindSfixed64_32:
			v := uint64(int64(*(*int32)(fPtr)))
			dst = append(dst,
				byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
				byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56),
			)
		case kindString:
			s := *(*string)(fPtr)
			if fc.wireType == wire.WireString {
				st.checkString(fc, s)
			}
			dst = wire.AppendVarint(dst, uint64(len(s)))
			dst = append(dst, s...)
		case kindBytes:
			b := *(*[]byte)(fPtr)
			if fc.wireType == wire.WireString {
				st.checkBytes(fc, b)
			}
			dst = wire.AppendVarint(dst, uint64(len(b)))
			dst = append(dst, b...)
		case kindStringSlice:
			items := *(*[]string)(fPtr)
			if fc.elemWireType == wire.WireBytes {
				dst = encodeStringArrAs(dst, items, wire.WireBytes)
			} else {
				for _, v := range items {
					st.checkString(fc, v)
				}
				dst = encodeStringArr(dst, items)
			}
		case kindBytesSlice:
			items := *(*[][]byte)(fPtr)
			if fc.elemWireType == wire.WireString {
				for _, v := range items {
					st.checkBytes(fc, v)
				}
				dst = encodeBytesArrAs(dst, items, wire.WireString)
			} else {
				dst = encodeBytesArr(dst, items)
			}
		case kindBoolSlice:
			dst = encodeBoolArr(dst, *(*[]bool)(fPtr))
		case kindUint8Slice:
			dst = encodeUintArr(dst, *(*[]uint8)(fPtr))
		case kindUint16Slice:
			dst = encodeUintArr(dst, *(*[]uint16)(fPtr))
		case kindUint32Slice:
			dst = encodeUintArr(dst, *(*[]uint32)(fPtr))
		case kindUint64Slice:
			dst = encodeUintArr(dst, *(*[]uint64)(fPtr))
		case kindUint8SintSlice:
			dst = encodeSintArr(dst, *(*[]uint8)(fPtr))
		case kindUint16SintSlice:
			dst = encodeSintArr(dst, *(*[]uint16)(fPtr))
		case kindUint32SintSlice:
			dst = encodeSintArr(dst, *(*[]uint32)(fPtr))
		case kindUint64SintSlice:
			dst = encodeSintArr(dst, *(*[]uint64)(fPtr))
		case kindInt8Slice:
			dst = encodeSintArr(dst, *(*[]int8)(fPtr))
		case kindInt16Slice:
			dst = encodeSintArr(dst, *(*[]int16)(fPtr))
		case kindInt32Slice:
			dst = encodeSintArr(dst, *(*[]int32)(fPtr))
		case kindInt64Slice:
			dst = encodeSintArr(dst, *(*[]int64)(fPtr))
		case kindFloat32Slice:
			dst = encodeFixed32Arr(dst, *(*[]uint32)(fPtr), fc.elemWireType)
		case kindFloat64Slice:
			dst = encodeFixed64Arr(dst, *(*[]uint64)(fPtr), fc.elemWireType)
		case kindUint8Fixed32Slice:
			dst = encodeFixed32CastArr(dst, *(*[]uint8)(fPtr), fc.elemWireType)
		case kindUint16Fixed32Slice:
			dst = encodeFixed32CastArr(dst, *(*[]uint16)(fPtr), fc.elemWireType)
		case kindUint64Fixed32Slice, kindInt64Fixed32Slice:
			dst = encodeFixed32CastArr(dst, *(*[]uint64)(fPtr), fc.elemWireType)
		case kindInt8Fixed32Slice:
			dst = encodeFixed32CastArr(dst, *(*[]int8)(fPtr), fc.elemWireType)
		case kindInt16Fixed32Slice:
			dst = encodeFixed32CastArr(dst, *(*[]int16)(fPtr), fc.elemWireType)
		case kindUint8Fixed64Slice:
			dst = encodeFixed64CastArr(dst, *(*[]uint8)(fPtr), fc.elemWireType)
		case kindUint16Fixed64Slice:
			dst = encodeFixed64CastArr(dst, *(*[]uint16)(fPtr), fc.elemWireType)
		case kindUint32Fixed64Slice:
			dst = encodeFixed64CastArr(dst, *(*[]uint32)(fPtr), fc.elemWireType)
		case kindInt8Fixed64Slice:
			dst = encodeFixed64CastArr(dst, *(*[]int8)(fPtr), fc.elemWireType)
		case kindInt16Fixed64Slice:
			dst = encodeFixed64CastArr(dst, *(*[]int16)(fPtr), fc.elemWireType)
		case kindInt32Fixed64Slice:
			dst = encodeFixed64CastArr(dst, *(*[]int32)(fPtr), fc.elemWireType)
		case kindStructVal:
			lenPos := len(dst)
			if fc.subCodec.minWireSize(fPtr, 0) > wire.Max2BContainerLen {
				dst = append(dst, 0, 0, 0, 0)
				dst = fc.subCodec.encodeStruct(dst, fPtr, st, depth+1)
				if st.depthErr {
					return dst
				}
				dst = wire.BackpatchContainerLen4(dst, lenPos)
			} else {
				dst = append(dst, 0, 0)
				dst = fc.subCodec.encodeStruct(dst, fPtr, st, depth+1)
				if st.depthErr {
					return dst
				}
				dst = wire.BackpatchContainerLen(dst, lenPos)
			}
		case kindStructPtr:
			subPtr := *(*unsafe.Pointer)(fPtr)
			lenPos := len(dst)
			if fc.subCodec.minWireSize(subPtr, 0) > wire.Max2BContainerLen {
				dst = append(dst, 0, 0, 0, 0)
				dst = fc.subCodec.encodeStruct(dst, subPtr, st, depth+1)
				if st.depthErr {
					return dst
				}
				dst = wire.BackpatchContainerLen4(dst, lenPos)
			} else {
				dst = append(dst, 0, 0)
				dst = fc.subCodec.encodeStruct(dst, subPtr, st, depth+1)
				if st.depthErr {
					return dst
				}
				dst = wire.BackpatchContainerLen(dst, lenPos)
			}
		case kindStructValSlice:
			sh := (*unsafeSliceHeader)(fPtr)
			arrMin := 2
			for j := range sh.Len {
				elemPtr := unsafe.Add(sh.Data, uintptr(j)*fc.elemSize)
				arrMin += 2 + fc.subCodec.minWireSize(elemPtr, 0)
			}
			lenPos := len(dst)
			if arrMin > wire.Max2BContainerLen {
				dst = append(dst, 0, 0, 0, 0, wire.WireSubMsg)
			} else {
				dst = append(dst, 0, 0, wire.WireSubMsg)
			}
			dst = wire.AppendVarint(dst, uint64(sh.Len))
			for j := range sh.Len {
				elemPtr := unsafe.Add(sh.Data, uintptr(j)*fc.elemSize)
				eLenPos := len(dst)
				if fc.subCodec.minWireSize(elemPtr, 0) > wire.Max2BContainerLen {
					dst = append(dst, 0, 0, 0, 0)
					dst = fc.subCodec.encodeStruct(dst, elemPtr, st, depth+2)
					if st.depthErr {
						return dst
					}
					dst = wire.BackpatchContainerLen4(dst, eLenPos)
				} else {
					dst = append(dst, 0, 0)
					dst = fc.subCodec.encodeStruct(dst, elemPtr, st, depth+2)
					if st.depthErr {
						return dst
					}
					dst = wire.BackpatchContainerLen(dst, eLenPos)
				}
			}
			if arrMin > wire.Max2BContainerLen {
				dst = wire.BackpatchContainerLen4(dst, lenPos)
			} else {
				dst = wire.BackpatchContainerLen(dst, lenPos)
			}
		case kindStructPtrSlice:
			ptrs := *(*[]unsafe.Pointer)(fPtr)
			arrMin := 2
			for j := range ptrs {
				arrMin += 2
				if ptrs[j] != nil {
					arrMin += fc.subCodec.minWireSize(ptrs[j], 0)
				}
			}
			lenPos := len(dst)
			if arrMin > wire.Max2BContainerLen {
				dst = append(dst, 0, 0, 0, 0, wire.WireSubMsg)
			} else {
				dst = append(dst, 0, 0, wire.WireSubMsg)
			}
			dst = wire.AppendVarint(dst, uint64(len(ptrs)))
			for j := range ptrs {
				eLenPos := len(dst)
				if ptrs[j] != nil && fc.subCodec.minWireSize(ptrs[j], 0) > wire.Max2BContainerLen {
					dst = append(dst, 0, 0, 0, 0)
					dst = fc.subCodec.encodeStruct(dst, ptrs[j], st, depth+2)
					if st.depthErr {
						return dst
					}
					dst = wire.BackpatchContainerLen4(dst, eLenPos)
				} else {
					dst = append(dst, 0, 0)
					if ptrs[j] != nil {
						dst = fc.subCodec.encodeStruct(dst, ptrs[j], st, depth+2)
						if st.depthErr {
							return dst
						}
					}
					dst = wire.BackpatchContainerLen(dst, eLenPos)
				}
			}
			if arrMin > wire.Max2BContainerLen {
				dst = wire.BackpatchContainerLen4(dst, lenPos)
			} else {
				dst = wire.BackpatchContainerLen(dst, lenPos)
			}
		case kindMapStringUint:
			m := *(*map[string]uint64)(fPtr)
			for k := range m {
				st.checkString(fc, k)
			}
			dst = encodeMapStringUint(dst, m, st.deterministic)
		case kindMap:
			dst = encodeGenericMap(dst, fc, fPtr, st, depth+1)
			if st.depthErr {
				return dst
			}
		}
	}
	return dst
}

func sortReflectMapKeys(keys []reflect.Value) {
	if len(keys) <= 1 {
		return
	}
	kind := keys[0].Kind()
	slices.SortFunc(keys, func(a, b reflect.Value) int {
		switch kind {
		case reflect.String:
			return cmp.Compare(a.String(), b.String())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return cmp.Compare(a.Int(), b.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return cmp.Compare(a.Uint(), b.Uint())
		case reflect.Bool:
			ab, bb := a.Bool(), b.Bool()
			if ab == bb {
				return 0
			}
			if !ab && bb {
				return -1
			}
			return 1
		default:
			return 0
		}
	})
}

func appendReflectMapEntryPayload(dst []byte, fc *fieldCodec, rv reflect.Value, wt uint8, subCodec *structCodec, st *encState, depth int) []byte {
	switch wt {
	case wire.WireString, wire.WireBytes:
		if rv.Kind() == reflect.String {
			s := rv.String()
			if wt == wire.WireString {
				st.checkString(fc, s)
			}
			dst = wire.AppendVarint(dst, uint64(len(s)))
			return append(dst, s...)
		}
		b := rv.Bytes()
		if wt == wire.WireString {
			st.checkBytes(fc, b)
		}
		dst = wire.AppendVarint(dst, uint64(len(b)))
		return append(dst, b...)
	case wire.WireUint:
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return wire.AppendVarint(dst, uint64(rv.Int()))
		default:
			return wire.AppendVarint(dst, rv.Uint())
		}
	case wire.WireSint:
		switch rv.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return wire.AppendVarint(dst, wire.EncodeZigZag(int64(rv.Uint())))
		default:
			return wire.AppendVarint(dst, wire.EncodeZigZag(rv.Int()))
		}
	case wire.WireFixed32, wire.WireFloat32, wire.WireSfixed32:
		var bits uint32
		switch rv.Kind() {
		case reflect.Float32:
			bits = math.Float32bits(float32(rv.Float()))
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			bits = uint32(rv.Int())
		default:
			bits = uint32(rv.Uint())
		}
		return append(dst, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	case wire.WireFixed64, wire.WireFloat64, wire.WireSfixed64:
		var bits uint64
		switch rv.Kind() {
		case reflect.Float64:
			bits = math.Float64bits(rv.Float())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			bits = uint64(rv.Int())
		default:
			bits = rv.Uint()
		}
		return append(dst,
			byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
			byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56),
		)
	case wire.WireBoolTrue, wire.WireBoolFalse:
		if rv.Bool() {
			return append(dst, 1)
		}
		return append(dst, 0)
	case wire.WireSubMsg:
		lenPos := len(dst)
		dst = append(dst, 0, 0)
		if rv.Kind() == reflect.Pointer {
			if !rv.IsNil() {
				dst = subCodec.encodeStruct(dst, rv.UnsafePointer(), st, depth+1)
				if st.depthErr {
					return dst
				}
			}
		} else {
			cp := reflect.New(rv.Type())
			cp.Elem().Set(rv)
			dst = subCodec.encodeStruct(dst, cp.UnsafePointer(), st, depth+1)
			if st.depthErr {
				return dst
			}
		}
		return wire.BackpatchContainerLen(dst, lenPos)
	default:
		return dst
	}
}

func encodeGenericMap(dst []byte, fc *fieldCodec, fPtr unsafe.Pointer, st *encState, depth int) []byte {
	rv := reflect.NewAt(fc.sliceType, fPtr).Elem()
	lenPos := len(dst)
	dst = append(dst, 0, 0, fc.mapKeyWT, fc.mapValWT)
	dst = wire.AppendVarint(dst, uint64(rv.Len()))
	if st.deterministic && rv.Len() > 1 {
		keys := rv.MapKeys()
		sortReflectMapKeys(keys)
		for _, k := range keys {
			dst = appendReflectMapEntryPayload(dst, fc, k, fc.mapKeyWT, nil, st, depth)
			dst = appendReflectMapEntryPayload(dst, fc, rv.MapIndex(k), fc.mapValWT, fc.subCodec, st, depth)
			if st.depthErr {
				return dst
			}
		}
	} else {
		iter := rv.MapRange()
		for iter.Next() {
			dst = appendReflectMapEntryPayload(dst, fc, iter.Key(), fc.mapKeyWT, nil, st, depth)
			dst = appendReflectMapEntryPayload(dst, fc, iter.Value(), fc.mapValWT, fc.subCodec, st, depth)
			if st.depthErr {
				return dst
			}
		}
	}
	return wire.BackpatchContainerLen(dst, lenPos)
}

type unsafeSliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

// Unmarshal decodes an OmniBuf packet into any struct pointer v (including *pb.PeerUpdate)
// using safe heap-allocated strings.
func Unmarshal(packet []byte, v any) error {
	return unmarshalTableWithOptions(packet, v, UnmarshalOptions{})
}

// UnmarshalZeroCopy decodes an OmniBuf packet into any struct pointer v using zero-copy
// unsafe.String references into packet. The packet must not be modified or returned
// to a buffer pool while any decoded string, map key, or byte slice remains in use.
func UnmarshalZeroCopy(packet []byte, v any) error {
	return unmarshalTableWithOptions(packet, v, UnmarshalOptions{ZeroCopy: true})
}

func unmarshalTableWithOptions(packet []byte, v any, opts UnmarshalOptions) error {
	if opts.MaxBytes > 0 && len(packet) > opts.MaxBytes {
		return fmt.Errorf("%w: %d > %d", ErrMessageTooLarge, len(packet), opts.MaxBytes)
	}
	if protohook.UnmarshalHook != nil {
		if handled, err := protohook.UnmarshalHook(packet, v, opts.MaxBytes, opts.MaxDepth, opts.Merge, opts.ZeroCopy); handled {
			return err
		}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("omnibuf: Unmarshal requires non-nil pointer to struct")
	}
	sc, err := getStructCodec(rv.Type().Elem())
	if err != nil {
		return err
	}
	ptr := rv.UnsafePointer()
	// Like proto.Unmarshal, decoding replaces the destination when opts.Merge is false:
	// fields absent from packet are reset (slice capacity is retained for reuse).
	return sc.decodeStruct(packet, ptr, opts.ZeroCopy, 0, opts.effectiveMaxDepth(), opts.Merge)
}

func sliceForDecode[T any](p *[]T, alreadySeen bool) []T {
	sl := *p
	if !alreadySeen && sl != nil {
		return sl[:0]
	}
	return sl
}

func isBoolField(fc *fieldCodec) bool {
	for fc.kind == kindScalarPtr {
		fc = fc.elemCodec
	}
	return fc.kind == kindBool
}

// decodeStruct decodes data into the struct at base. When merge is false the
// struct is replaced: every field not present in data is reset to its zero
// value (slices keep their capacity). When merge is true (a repeated
// occurrence of the same sub-message, e.g. from concatenation) fields present
// in data are merged onto the existing value and absent fields are kept.
func (sc *structCodec) decodeStruct(data []byte, base unsafe.Pointer, zeroCopy bool, depth, maxDepth int, merge bool) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: nesting depth %d exceeds %d", ErrMaxDepth, depth, maxDepth)
	}
	sc.protoState.Reset(base, merge)
	n := len(data)
	pos := 0
	var seenMask uint64
	var seenOverflow map[int]bool

	for pos < n {
		b0 := data[pos]
		var tag uint64
		if (b0 & 1) != 0 {
			tag = uint64(b0 >> 1)
			pos++
		} else if (b0 & 2) != 0 {
			if pos+2 > n {
				return fmt.Errorf("%w: truncated tag at %d", ErrCorruptWire, pos)
			}
			tag = uint64(binary.LittleEndian.Uint16(data[pos:]) >> 2)
			pos += 2
		} else {
			t64, tn := wire.DecodeVarint(data[pos:])
			if tn == 0 || t64>>4 > maxProtobufFieldNumber {
				return fmt.Errorf("%w: truncated or invalid multi-byte tag at %d", ErrCorruptWire, pos)
			}
			tag = t64
			pos += tn
		}
		fieldNum32 := uint32(tag >> 4)
		wt := uint8(tag & 0x0F)

		if fieldNum32 == 0 {
			nextPos, err := validateField0WithMaxDepth(data, pos, wt, depth, maxDepth)
			if err != nil {
				return err
			}
			pos = nextPos
			continue
		}

		var fc *fieldCodec
		if fieldNum32 <= maxDirectFieldSlot {
			fc = sc.byNum[fieldNum32]
		} else if sc.overflow != nil {
			fc = sc.overflow[fieldNum32]
		}
		// A known field arriving with a different wire type is treated like
		// an unknown field (as in Protobuf): its payload is validated and
		// skipped. All decoder tiers share this rule.
		if fc == nil || (!isBoolField(fc) && fc.wireType != wt) || (isBoolField(fc) && wt != wire.WireBoolFalse && wt != wire.WireBoolTrue) {
			nextPos, err := skipValueWithMaxDepthChecked(data, pos, wt, depth, maxDepth, false)
			if err != nil {
				return err
			}
			pos = nextPos
			continue
		}

		var alreadySeen bool
		if idx := fc.index; idx < 64 {
			alreadySeen = (seenMask & (1 << idx)) != 0
			seenMask |= (1 << idx)
		} else {
			alreadySeen = seenOverflow[idx]
			if seenOverflow == nil {
				seenOverflow = make(map[int]bool)
			}
			seenOverflow[idx] = true
		}
		fPtr := unsafe.Add(base, fc.offset)

		alreadySeen = alreadySeen || merge
		nextPos, err := decodeFieldValue(data, pos, wt, fc, fPtr, alreadySeen, zeroCopy, depth, maxDepth)
		if err != nil {
			return err
		}
		pos = nextPos
	}
	if !merge && (len(sc.fields) > 64 || seenMask != sc.expectedMask) {
		for i := range sc.fields {
			if i < 64 {
				if seenMask&(1<<i) != 0 {
					continue
				}
			} else if seenOverflow[i] {
				continue
			}
			fc := &sc.fields[i]
			clearField(fc, unsafe.Add(base, fc.offset))
		}
	}
	return nil
}

// clearField resets a struct field that was absent from the decoded packet.
// Slices are truncated (keeping capacity for reuse), maps are emptied,
// pointers are set to nil, and nested struct values are cleared recursively.
func typeHasPointers(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.String, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func, reflect.Interface, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return t.Len() > 0 && typeHasPointers(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if typeHasPointers(t.Field(i).Type) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func clearStructValSliceRange(fc *fieldCodec, fPtr unsafe.Pointer, from, to int) {
	if !fc.elemHasPtrs || from >= to {
		return
	}
	sh := (*unsafeSliceHeader)(fPtr)
	if sh.Data == nil || to > sh.Cap {
		return
	}
	reflect.NewAt(fc.sliceType, fPtr).Elem().Slice(from, to).Clear()
}

func clearField(fc *fieldCodec, fPtr unsafe.Pointer) {
	switch fc.goKind {
	case reflect.Slice:
		switch fc.kind {
		case kindBytes:
			*(*[]byte)(fPtr) = nil
			return
		case kindStringSlice:
			s := *(*[]string)(fPtr)
			clear(s)
			*(*[]string)(fPtr) = s[:0]
			return
		case kindBytesSlice:
			s := *(*[][]byte)(fPtr)
			clear(s)
			*(*[][]byte)(fPtr) = s[:0]
			return
		case kindStructPtrSlice:
			s := *(*[]unsafe.Pointer)(fPtr)
			clear(s)
			*(*[]unsafe.Pointer)(fPtr) = s[:0]
			return
		case kindStructValSlice:
			sh := (*unsafeSliceHeader)(fPtr)
			clearStructValSliceRange(fc, fPtr, 0, sh.Len)
			sh.Len = 0
			return
		default:
			(*unsafeSliceHeader)(fPtr).Len = 0
			return
		}
	case reflect.Map:
		if fc.kind == kindMapStringUint {
			clear(*(*map[string]uint64)(fPtr))
			return
		}
		mv := reflect.NewAt(fc.sliceType, fPtr).Elem()
		if !mv.IsNil() {
			mv.Clear()
		}
	case reflect.String:
		*(*string)(fPtr) = ""
	case reflect.Pointer:
		*(*unsafe.Pointer)(fPtr) = nil
	case reflect.Struct:
		if fc.subCodec != nil {
			fc.subCodec.protoState.Reset(fPtr, false)
			for i := range fc.subCodec.fields {
				sub := &fc.subCodec.fields[i]
				clearField(sub, unsafe.Add(fPtr, sub.offset))
			}
			return
		}
		clear(unsafe.Slice((*byte)(fPtr), fc.goSize))
	default:
		clear(unsafe.Slice((*byte)(fPtr), fc.goSize))
	}
}

func decodeFieldValue(data []byte, pos int, wt uint8, fc *fieldCodec, fPtr unsafe.Pointer, alreadySeen, zeroCopy bool, depth, maxDepth int) (int, error) {
	n := len(data)
	if fc.kind == kindScalarPtr {
		subPtr := *(*unsafe.Pointer)(fPtr)
		if subPtr == nil {
			subPtr = reflect.New(fc.elemType).UnsafePointer()
			*(*unsafe.Pointer)(fPtr) = subPtr
		}
		return decodeFieldValue(data, pos, wt, fc.elemCodec, subPtr, alreadySeen, zeroCopy, depth, maxDepth)
	}
	// Arrays and maps count toward the nesting limit just like messages
	// (spec §10): a container nested more than maxDepth levels below
	// the root is rejected, and message elements/values sit one level deeper.
	if (fc.wireType == wire.WireArray || fc.wireType == wire.WireMap) && depth+1 > maxDepth {
		return 0, fmt.Errorf("%w: nesting depth %d exceeds %d", ErrMaxDepth, depth+1, maxDepth)
	}

	switch fc.kind {
	case kindUint8, kindInt8Uint:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return 0, fmt.Errorf("omnibuf: truncated uint8 at %d", pos)
		}
		*(*uint8)(fPtr) = uint8(v)
		return pos + vn, nil
	case kindUint16, kindInt16Uint:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return 0, fmt.Errorf("omnibuf: truncated uint16 at %d", pos)
		}
		*(*uint16)(fPtr) = uint16(v)
		return pos + vn, nil
	case kindUint32, kindInt32Uint:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return 0, fmt.Errorf("omnibuf: truncated uint32 at %d", pos)
		}
		*(*uint32)(fPtr) = uint32(v)
		return pos + vn, nil
	case kindUint64, kindInt64Uint:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return 0, fmt.Errorf("omnibuf: truncated uint64 at %d", pos)
		}
		*(*uint64)(fPtr) = v
		return pos + vn, nil
	case kindInt8, kindUint8Sint:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return 0, fmt.Errorf("omnibuf: truncated int8 at %d", pos)
		}
		*(*int8)(fPtr) = int8(wire.DecodeZigZag(v))
		return pos + vn, nil
	case kindInt16, kindUint16Sint:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return 0, fmt.Errorf("omnibuf: truncated int16 at %d", pos)
		}
		*(*int16)(fPtr) = int16(wire.DecodeZigZag(v))
		return pos + vn, nil
	case kindInt32, kindSint32, kindUint32Sint:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return 0, fmt.Errorf("omnibuf: truncated int32 at %d", pos)
		}
		*(*int32)(fPtr) = int32(wire.DecodeZigZag(v))
		return pos + vn, nil
	case kindInt64, kindSint64, kindUint64Sint:
		v, vn := wire.DecodeVarint(data[pos:])
		if vn == 0 {
			return 0, fmt.Errorf("omnibuf: truncated int64 at %d", pos)
		}
		*(*int64)(fPtr) = wire.DecodeZigZag(v)
		return pos + vn, nil
	case kindBool:
		*(*bool)(fPtr) = (wt == wire.WireBoolTrue)
		return pos, nil
	case kindFloat32, kindFixed32:
		if n-pos < 4 {
			return 0, fmt.Errorf("omnibuf: truncated fixed32 at %d", pos)
		}
		*(*uint32)(fPtr) = binary.LittleEndian.Uint32(data[pos:])
		return pos + 4, nil
	case kindFixed32_8, kindSfixed32_8:
		if n-pos < 4 {
			return 0, fmt.Errorf("omnibuf: truncated fixed32 at %d", pos)
		}
		*(*uint8)(fPtr) = uint8(binary.LittleEndian.Uint32(data[pos:]))
		return pos + 4, nil
	case kindFixed32_16, kindSfixed32_16:
		if n-pos < 4 {
			return 0, fmt.Errorf("omnibuf: truncated fixed32 at %d", pos)
		}
		*(*uint16)(fPtr) = uint16(binary.LittleEndian.Uint32(data[pos:]))
		return pos + 4, nil
	case kindFixed32_64:
		if n-pos < 4 {
			return 0, fmt.Errorf("omnibuf: truncated fixed32 at %d", pos)
		}
		*(*uint64)(fPtr) = uint64(binary.LittleEndian.Uint32(data[pos:]))
		return pos + 4, nil
	case kindSfixed32_64:
		if n-pos < 4 {
			return 0, fmt.Errorf("omnibuf: truncated fixed32 at %d", pos)
		}
		*(*int64)(fPtr) = int64(int32(binary.LittleEndian.Uint32(data[pos:])))
		return pos + 4, nil
	case kindFloat64, kindFixed64:
		if n-pos < 8 {
			return 0, fmt.Errorf("omnibuf: truncated fixed64 at %d", pos)
		}
		*(*uint64)(fPtr) = binary.LittleEndian.Uint64(data[pos:])
		return pos + 8, nil
	case kindFixed64_8, kindSfixed64_8:
		if n-pos < 8 {
			return 0, fmt.Errorf("omnibuf: truncated fixed64 at %d", pos)
		}
		*(*uint8)(fPtr) = uint8(binary.LittleEndian.Uint64(data[pos:]))
		return pos + 8, nil
	case kindFixed64_16, kindSfixed64_16:
		if n-pos < 8 {
			return 0, fmt.Errorf("omnibuf: truncated fixed64 at %d", pos)
		}
		*(*uint16)(fPtr) = uint16(binary.LittleEndian.Uint64(data[pos:]))
		return pos + 8, nil
	case kindFixed64_32, kindSfixed64_32:
		if n-pos < 8 {
			return 0, fmt.Errorf("omnibuf: truncated fixed64 at %d", pos)
		}
		*(*uint32)(fPtr) = uint32(binary.LittleEndian.Uint64(data[pos:]))
		return pos + 8, nil
	case kindString:
		s, nextPos, err := readLenSlice(data, pos, 0)
		if err != nil {
			return 0, err
		}
		if wt == wire.WireString && !wire.ValidUTF8(s) {
			return 0, fmt.Errorf("%w: string field %s at %d", ErrInvalidUTF8, fc.name, pos)
		}
		*(*string)(fPtr) = decodeStr(s, zeroCopy)
		return nextPos, nil
	case kindBytes:
		b, nextPos, err := readLenSlice(data, pos, 0)
		if err != nil {
			return 0, err
		}
		if wt == wire.WireString && !wire.ValidUTF8(b) {
			return 0, fmt.Errorf("%w: string field %s at %d", ErrInvalidUTF8, fc.name, pos)
		}
		if zeroCopy {
			*(*[]byte)(fPtr) = b
		} else {
			cp := make([]byte, len(b))
			copy(cp, b)
			*(*[]byte)(fPtr) = cp
		}
		return nextPos, nil
	case kindStringSlice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]string)(fPtr)
		old := *p
		oldLen := len(old)
		base := sliceForDecode(p, alreadySeen)
		sl, ok := appendStringArrBodyAs(base, body, zeroCopy, fc.elemWireType)
		if !ok {
			if unsafe.SliceData(base) == unsafe.SliceData(old) && cap(old) > len(base) {
				if alreadySeen {
					clear(old[oldLen:cap(old)])
				} else {
					clear(old[:oldLen])
					*p = old[:0]
				}
			}
			return 0, fmt.Errorf("omnibuf: invalid string array at %d", pos)
		}
		if !alreadySeen && len(sl) < oldLen && unsafe.SliceData(sl) == unsafe.SliceData(old) {
			clear(old[len(sl):oldLen])
		}
		*p = sl
		return nextPos, nil
	case kindBytesSlice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[][]byte)(fPtr)
		old := *p
		oldLen := len(old)
		base := sliceForDecode(p, alreadySeen)
		sl, ok := appendBytesArrBodyAs(base, body, zeroCopy, fc.elemWireType)
		if !ok {
			if unsafe.SliceData(base) == unsafe.SliceData(old) && cap(old) > len(base) {
				if alreadySeen {
					clear(old[oldLen:cap(old)])
				} else {
					clear(old[:oldLen])
					*p = old[:0]
				}
			}
			return 0, fmt.Errorf("omnibuf: invalid bytes array at %d", pos)
		}
		if !alreadySeen && len(sl) < oldLen && unsafe.SliceData(sl) == unsafe.SliceData(old) {
			clear(old[len(sl):oldLen])
		}
		*p = sl
		return nextPos, nil
	case kindBoolSlice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]bool)(fPtr)
		sl, ok := appendBoolArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid bool array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint8Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint8)(fPtr)
		sl, ok := appendUintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid uint8 array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint16Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint16)(fPtr)
		sl, ok := appendUintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid uint16 array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint32Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint32)(fPtr)
		sl, ok := appendUintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid uint32 array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint64Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint64)(fPtr)
		sl, ok := appendUintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid uint64 array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint8SintSlice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint8)(fPtr)
		sl, ok := appendSintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid uint8 sint array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint16SintSlice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint16)(fPtr)
		sl, ok := appendSintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid uint16 sint array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint32SintSlice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint32)(fPtr)
		sl, ok := appendSintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid uint32 sint array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint64SintSlice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint64)(fPtr)
		sl, ok := appendSintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid uint64 sint array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindInt8Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]int8)(fPtr)
		sl, ok := appendSintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid int8 array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindInt16Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]int16)(fPtr)
		sl, ok := appendSintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid int16 array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindInt32Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]int32)(fPtr)
		sl, ok := appendSintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid int32 array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindInt64Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]int64)(fPtr)
		sl, ok := appendSintArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid int64 array at %d", pos)
		}
		*p = sl
		return nextPos, nil
	case kindFloat32Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint32)(fPtr)
		sl, ok := appendFixed32ArrBody(sliceForDecode(p, alreadySeen), body, fc.elemWireType)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed32 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindFloat64Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint64)(fPtr)
		sl, ok := appendFixed64ArrBody(sliceForDecode(p, alreadySeen), body, fc.elemWireType)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed64 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint8Fixed32Slice, kindInt8Fixed32Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint8)(fPtr)
		sl, ok := appendFixed32CastArrBody(sliceForDecode(p, alreadySeen), body, fc.elemWireType)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed32 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint16Fixed32Slice, kindInt16Fixed32Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint16)(fPtr)
		sl, ok := appendFixed32CastArrBody(sliceForDecode(p, alreadySeen), body, fc.elemWireType)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed32 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint64Fixed32Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint64)(fPtr)
		sl, ok := appendFixed32CastArrBody(sliceForDecode(p, alreadySeen), body, fc.elemWireType)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed32 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindInt64Fixed32Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]int64)(fPtr)
		sl, ok := appendSfixed32CastArrBody(sliceForDecode(p, alreadySeen), body)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed32 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint8Fixed64Slice, kindInt8Fixed64Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint8)(fPtr)
		sl, ok := appendFixed64CastArrBody(sliceForDecode(p, alreadySeen), body, fc.elemWireType)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed64 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint16Fixed64Slice, kindInt16Fixed64Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint16)(fPtr)
		sl, ok := appendFixed64CastArrBody(sliceForDecode(p, alreadySeen), body, fc.elemWireType)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed64 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindUint32Fixed64Slice, kindInt32Fixed64Slice:
		body, nextPos, err := readLenSlice(data, pos, 1)
		if err != nil {
			return 0, err
		}
		p := (*[]uint32)(fPtr)
		sl, ok := appendFixed64CastArrBody(sliceForDecode(p, alreadySeen), body, fc.elemWireType)
		if !ok {
			return 0, fmt.Errorf("%w: invalid fixed64 array at %d", ErrCorruptWire, pos)
		}
		*p = sl
		return nextPos, nil
	case kindStructVal:
		subBytes, nextPos, err := readLenSlice(data, pos, 0)
		if err != nil {
			return 0, err
		}
		if err := fc.subCodec.decodeStruct(subBytes, fPtr, zeroCopy, depth+1, maxDepth, alreadySeen); err != nil {
			return 0, err
		}
		return nextPos, nil
	case kindStructPtr:
		subBytes, nextPos, err := readLenSlice(data, pos, 0)
		if err != nil {
			return 0, err
		}
		subPtr := *(*unsafe.Pointer)(fPtr)
		if subPtr == nil {
			subPtr = reflect.New(fc.elemType).UnsafePointer()
			*(*unsafe.Pointer)(fPtr) = subPtr
		}
		if err := fc.subCodec.decodeStruct(subBytes, subPtr, zeroCopy, depth+1, maxDepth, alreadySeen); err != nil {
			return 0, err
		}
		return nextPos, nil
	case kindStructValSlice:
		arrPayload, nextPos, err := readLenSlice(data, pos, 0)
		if err != nil {
			return 0, err
		}
		elem, elemCount, p, ok := parseArrayHeader(arrPayload)
		if !ok || elem != wire.WireSubMsg {
			return 0, fmt.Errorf("omnibuf: corrupt submsg array at %d", pos)
		}
		sh := (*unsafeSliceHeader)(fPtr)
		oldLen := sh.Len
		start := 0
		if alreadySeen {
			start = oldLen
		}
		total := start + elemCount
		dataPtr := sh.Data
		newCap := sh.Cap
		reused := true
		if sh.Cap < total {
			if _, ok := countSubMsgArrElems(arrPayload); !ok {
				return 0, fmt.Errorf("omnibuf: corrupt submsg array at %d", pos)
			}
			newSlice := reflect.MakeSlice(fc.sliceType, total, wire.GrowCapacity(sh.Cap, total))
			dataPtr = newSlice.UnsafePointer()
			if start > 0 && sh.Data != nil {
				reflect.Copy(newSlice, reflect.NewAt(fc.sliceType, unsafe.Pointer(sh)).Elem())
			}
			newCap = newSlice.Cap()
			reused = false
		}
		for j := range elemCount {
			el, en := wire.DecodeVarint(arrPayload[p:])
			if en == 0 || el > uint64(len(arrPayload)-(p+en)) {
				if reused && !alreadySeen {
					clearStructValSliceRange(fc, fPtr, 0, oldLen)
					sh.Len = 0
				}
				return 0, fmt.Errorf("omnibuf: corrupt submsg array at %d", pos)
			}
			p += en
			elemPtr := unsafe.Add(dataPtr, uintptr(start+j)*fc.elemSize)
			if err := fc.subCodec.decodeStruct(arrPayload[p:p+int(el)], elemPtr, zeroCopy, depth+2, maxDepth, false); err != nil {
				if reused && !alreadySeen {
					clearStructValSliceRange(fc, fPtr, 0, max(oldLen, start+j+1))
					sh.Len = 0
				}
				return 0, err
			}
			p += int(el)
		}
		if p != len(arrPayload) {
			if reused && !alreadySeen {
				clearStructValSliceRange(fc, fPtr, 0, max(oldLen, total))
				sh.Len = 0
			}
			return 0, fmt.Errorf("omnibuf: corrupt submsg array at %d", pos)
		}
		if reused && !alreadySeen && total < oldLen {
			clearStructValSliceRange(fc, fPtr, total, oldLen)
		}
		sh.Data = dataPtr
		sh.Len = total
		sh.Cap = newCap
		return nextPos, nil
	case kindStructPtrSlice:
		arrPayload, nextPos, err := readLenSlice(data, pos, 0)
		if err != nil {
			return 0, err
		}
		elem, elemCount, p, ok := parseArrayHeader(arrPayload)
		if !ok || elem != wire.WireSubMsg {
			return 0, fmt.Errorf("omnibuf: corrupt submsg array at %d", pos)
		}
		oldPtrs := *(*[]unsafe.Pointer)(fPtr)
		oldLen := len(oldPtrs)
		start := 0
		if alreadySeen {
			start = oldLen
		}
		total := start + elemCount
		var ptrs []unsafe.Pointer
		reused := false
		if cap(oldPtrs) < total {
			if _, ok := countSubMsgArrElems(arrPayload); !ok {
				return 0, fmt.Errorf("omnibuf: corrupt submsg array at %d", pos)
			}
			slab := reflect.MakeSlice(reflect.SliceOf(fc.elemType), elemCount, elemCount)
			slabPtr := slab.UnsafePointer()
			ptrs = make([]unsafe.Pointer, total, wire.GrowCapacity(cap(oldPtrs), total))
			copy(ptrs, oldPtrs[:start])
			for j := range elemCount {
				ptrs[start+j] = unsafe.Add(slabPtr, uintptr(j)*fc.elemSize)
			}
		} else {
			ptrs = oldPtrs[:total]
			reused = true
		}
		for j := range elemCount {
			el, en := wire.DecodeVarint(arrPayload[p:])
			if en == 0 || el > uint64(len(arrPayload)-(p+en)) {
				if reused && !alreadySeen {
					clear(oldPtrs[:max(oldLen, total)])
					*(*[]unsafe.Pointer)(fPtr) = oldPtrs[:0]
				}
				return 0, fmt.Errorf("omnibuf: corrupt submsg array at %d", pos)
			}
			p += en
			if ptrs[start+j] == nil {
				ptrs[start+j] = reflect.New(fc.elemType).UnsafePointer()
			}
			if err := fc.subCodec.decodeStruct(arrPayload[p:p+int(el)], ptrs[start+j], zeroCopy, depth+2, maxDepth, false); err != nil {
				if reused && !alreadySeen {
					clear(oldPtrs[:max(oldLen, total)])
					*(*[]unsafe.Pointer)(fPtr) = oldPtrs[:0]
				}
				return 0, err
			}
			p += int(el)
		}
		if p != len(arrPayload) {
			if reused && !alreadySeen {
				clear(oldPtrs[:max(oldLen, total)])
				*(*[]unsafe.Pointer)(fPtr) = oldPtrs[:0]
			}
			return 0, fmt.Errorf("omnibuf: corrupt submsg array at %d", pos)
		}
		if reused && !alreadySeen && total < oldLen {
			clear(oldPtrs[total:oldLen])
		}
		*(*[]unsafe.Pointer)(fPtr) = ptrs
		return nextPos, nil
	case kindMapStringUint:
		body, nextPos, err := readLenSlice(data, pos, 3)
		if err != nil {
			return 0, err
		}
		var prev map[string]uint64
		if alreadySeen {
			prev = *(*map[string]uint64)(fPtr)
		}
		m, ok := decodeMapStringUintBody(prev, body, zeroCopy)
		if !ok {
			return 0, fmt.Errorf("omnibuf: invalid wireMap at %d", pos)
		}
		*(*map[string]uint64)(fPtr) = m
		return nextPos, nil
	case kindMap:
		body, nextPos, err := readLenSlice(data, pos, 3)
		if err != nil {
			return 0, err
		}
		if err := decodeGenericMapBody(fc, fPtr, body, alreadySeen, zeroCopy, depth+1, maxDepth); err != nil {
			return 0, err
		}
		return nextPos, nil
	default:
		return 0, fmt.Errorf("omnibuf: unsupported field kind %d", fc.kind)
	}
}

func decodeReflectMapEntryPayload(body []byte, p int, targetType reflect.Type, wt uint8, subCodec *structCodec, zeroCopy bool, depth, maxDepth int) (reflect.Value, int, error) {
	rv := reflect.New(targetType).Elem()
	switch wt {
	case wire.WireString, wire.WireBytes:
		sBytes, nextP, err := readLenSlice(body, p, 0)
		if err != nil {
			return reflect.Value{}, 0, err
		}
		if wt == wire.WireString && !wire.ValidUTF8(sBytes) {
			return reflect.Value{}, 0, fmt.Errorf("%w: wireMap string at %d", ErrInvalidUTF8, p)
		}
		if targetType.Kind() == reflect.String {
			rv.SetString(decodeStr(sBytes, zeroCopy))
		} else {
			if zeroCopy {
				rv.SetBytes(sBytes)
			} else {
				cp := make([]byte, len(sBytes))
				copy(cp, sBytes)
				rv.SetBytes(cp)
			}
		}
		return rv, nextP, nil
	case wire.WireUint:
		v, vn := wire.DecodeVarint(body[p:])
		if vn == 0 {
			return reflect.Value{}, 0, fmt.Errorf("omnibuf: truncated wireMap uint at %d", p)
		}
		switch targetType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			rv.SetInt(int64(v))
		default:
			rv.SetUint(v)
		}
		return rv, p + vn, nil
	case wire.WireSint:
		v, vn := wire.DecodeVarint(body[p:])
		if vn == 0 {
			return reflect.Value{}, 0, fmt.Errorf("omnibuf: truncated wireMap sint at %d", p)
		}
		sv := wire.DecodeZigZag(v)
		switch targetType.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			rv.SetUint(uint64(sv))
		default:
			rv.SetInt(sv)
		}
		return rv, p + vn, nil
	case wire.WireFixed32, wire.WireFloat32, wire.WireSfixed32:
		if len(body)-p < 4 {
			return reflect.Value{}, 0, fmt.Errorf("omnibuf: truncated wireMap fixed32 at %d", p)
		}
		bits := binary.LittleEndian.Uint32(body[p : p+4])
		switch targetType.Kind() {
		case reflect.Float32:
			rv.SetFloat(float64(math.Float32frombits(bits)))
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			rv.SetInt(int64(int32(bits)))
		default:
			rv.SetUint(uint64(bits))
		}
		return rv, p + 4, nil
	case wire.WireFixed64, wire.WireFloat64, wire.WireSfixed64:
		if len(body)-p < 8 {
			return reflect.Value{}, 0, fmt.Errorf("omnibuf: truncated wireMap fixed64 at %d", p)
		}
		bits := binary.LittleEndian.Uint64(body[p : p+8])
		switch targetType.Kind() {
		case reflect.Float64:
			rv.SetFloat(math.Float64frombits(bits))
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			rv.SetInt(int64(bits))
		default:
			rv.SetUint(bits)
		}
		return rv, p + 8, nil
	case wire.WireBoolTrue, wire.WireBoolFalse:
		if len(body)-p < 1 {
			return reflect.Value{}, 0, fmt.Errorf("omnibuf: truncated wireMap bool at %d", p)
		}
		b := body[p]
		if b != 0 && b != 1 {
			return reflect.Value{}, 0, fmt.Errorf("omnibuf: invalid wireMap bool %d at %d", b, p)
		}
		rv.SetBool(b == 1)
		return rv, p + 1, nil
	case wire.WireSubMsg:
		subBytes, nextP, err := readLenSlice(body, p, 0)
		if err != nil {
			return reflect.Value{}, 0, err
		}
		if targetType.Kind() == reflect.Pointer {
			ptrVal := reflect.New(targetType.Elem())
			if err := subCodec.decodeStruct(subBytes, ptrVal.UnsafePointer(), zeroCopy, depth+1, maxDepth, false); err != nil {
				return reflect.Value{}, 0, err
			}
			return ptrVal, nextP, nil
		}
		ptrVal := reflect.New(targetType)
		if err := subCodec.decodeStruct(subBytes, ptrVal.UnsafePointer(), zeroCopy, depth+1, maxDepth, false); err != nil {
			return reflect.Value{}, 0, err
		}
		return ptrVal.Elem(), nextP, nil
	default:
		return reflect.Value{}, 0, fmt.Errorf("%w: invalid wireMap entry wire type %d", ErrCorruptWire, wt)
	}
}

func decodeGenericMapBody(fc *fieldCodec, fPtr unsafe.Pointer, body []byte, alreadySeen, zeroCopy bool, depth, maxDepth int) error {
	keyWT, valWT, count, p, ok := parseMapHeader(body)
	if !ok || keyWT != fc.mapKeyWT || valWT != fc.mapValWT {
		return fmt.Errorf("%w: invalid wireMap header", ErrCorruptWire)
	}
	mapVal := reflect.NewAt(fc.sliceType, fPtr).Elem()
	if !alreadySeen || mapVal.IsNil() {
		mapVal.Set(reflect.MakeMapWithSize(fc.sliceType, min(count, 4096)))
	}
	keyType := fc.sliceType.Key()
	valType := fc.sliceType.Elem()
	for range count {
		kv, nextP, err := decodeReflectMapEntryPayload(body, p, keyType, fc.mapKeyWT, nil, zeroCopy, depth, maxDepth)
		if err != nil {
			return err
		}
		vv, nextP2, err := decodeReflectMapEntryPayload(body, nextP, valType, fc.mapValWT, fc.subCodec, zeroCopy, depth, maxDepth)
		if err != nil {
			return err
		}
		p = nextP2
		mapVal.SetMapIndex(kv, vv)
	}
	if p != len(body) {
		return fmt.Errorf("%w: wireMap trailing bytes (%d != %d)", ErrCorruptWire, p, len(body))
	}
	return nil
}
