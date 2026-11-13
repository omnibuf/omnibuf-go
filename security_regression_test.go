package omnibuf_test

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	ob "github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/internal/pb"
	"github.com/omnibuf/omnibuf-go/wire"
)

// A small, valid packet must not cause quadratic cumulative allocation when
// repeated fields are fragmented into individual elements.
func TestFragmentedArrayAllocation(t *testing.T) {
	const count = 2048
	sample := &pb.TestAllTypes{F9StringArr: []string{"x"}, F10Uint32Arr: []uint32{7}, F11SubMsgArr: []*pb.AllTypesSubMsg{{Id: 42}}}
	packet := bytes.Repeat(sample.MarshalOmniBufAppend(nil), count)
	type child struct {
		ID uint32 `omnibuf:"1"`
	}
	type values struct {
		Strings  []string `omnibuf:"9"`
		Numbers  []uint32 `omnibuf:"10"`
		Children []child  `omnibuf:"11"`
	}
	for _, tier := range []string{"table-values", "table-pointers", "generated", "wire", "table-wire-overrides"} {
		t.Run(tier, func(t *testing.T) {
			decode := func() {
				t.Helper()
				var ss []string
				var ns []uint32
				switch tier {
				case "table-values":
					var out values
					if err := ob.UnmarshalZeroCopy(packet, &out); err != nil {
						t.Fatal(err)
					}
					ss, ns = out.Strings, out.Numbers
					if len(out.Children) != count {
						t.Fatal("wrong child count")
					}
					for _, v := range out.Children {
						if v.ID != 42 {
							t.Fatal("corrupt child")
						}
					}
				case "table-pointers", "generated":
					var out pb.TestAllTypes
					var err error
					if tier == "generated" {
						err = out.UnmarshalOmniBuf(packet, true)
					} else {
						err = ob.UnmarshalZeroCopy(packet, &out)
					}
					if err != nil {
						t.Fatal(err)
					}
					ss, ns = out.F9StringArr, out.F10Uint32Arr
					if len(out.F11SubMsgArr) != count {
						t.Fatal("wrong child count")
					}
					for _, v := range out.F11SubMsgArr {
						if v == nil || v.Id != 42 {
							t.Fatal("corrupt child")
						}
					}
				case "table-wire-overrides":
					type overrides struct {
						Strings []string `omnibuf:"9,wire=bytes"`
						Numbers []uint32 `omnibuf:"10"`
					}
					segment, err := ob.Marshal(&overrides{Strings: []string{"x"}, Numbers: []uint32{7}})
					if err != nil {
						t.Fatal(err)
					}
					input := bytes.Repeat(segment, count)
					var out overrides
					if err := ob.UnmarshalZeroCopy(input, &out); err != nil {
						t.Fatal(err)
					}
					ss, ns = out.Strings, out.Numbers
				case "wire":
					var ok bool
					ss, ok = wire.ReadStringArr(packet, 9)
					if !ok {
						t.Fatal("strings rejected")
					}
					ns, ok = wire.ReadUint32Arr(packet, 10)
					if !ok {
						t.Fatal("integers rejected")
					}
				}
				if len(ss) != count || len(ns) != count {
					t.Fatal("wrong array length")
				}
				for i := range ss {
					if ss[i] != "x" || ns[i] != 7 {
						t.Fatal("corrupt element")
					}
				}
			}
			decode() // Warm codec caches before measuring.
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			decode()
			runtime.ReadMemStats(&after)
			// Generous allowance for linear output and race instrumentation. The old
			// exact-sized growth allocates tens of MiB for this input.
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
				t.Fatalf("fragmented decode allocated %d bytes for %d wire bytes", allocated, len(packet))
			}
		})
	}
}

func TestLargeDeterministicMap(t *testing.T) {
	for _, count := range []int{16, 17, 4096} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			values := make(map[string]uint64, count)
			prefix := strings.Repeat("prefix-", 32)
			for i := range count {
				values[fmt.Sprintf("%s%08d", prefix, i)] = uint64(i)
			}
			type message struct {
				Values map[string]uint64 `omnibuf:"12"`
			}
			want := wire.NewEncoder(0)
			want.WriteMapStringUintDeterministic(12, values)
			packet := want.Finish()
			got, ok := wire.ReadMapStringUint(packet, 12, false)
			if !ok || !reflect.DeepEqual(got, values) {
				t.Fatal("map round trip failed")
			}
			// Both independent encoders must preserve identical sorted wire output.
			for range 3 {
				encoded, err := ob.MarshalOptions{Deterministic: true}.Marshal(&message{values})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(encoded, packet) {
					t.Fatal("table deterministic map differs")
				}
			}
		})
	}
}

// A shared schema must not be copied for each array element, including when
// elements supply small local overrides of a large inherited schema.
func TestBlindSharedSchemaAllocation(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprintf("local=%v", local), func(t *testing.T) {
			var small uint64
			for _, count := range []int{256, 1024} {
				fields := make(map[uint32]string, count)
				for i := 1; i <= count; i++ {
					fields[uint32(i)] = fmt.Sprintf("name_%d", i)
				}
				meta := &ob.SchemaMeta{Children: map[uint32]*ob.SchemaMeta{1: {Fields: fields}}}
				packet := meta.AppendField0(nil, true)
				body := wire.AppendVarint([]byte{wire.WireSubMsg}, uint64(count))
				for i := range count {
					elem := []byte{33, 1} // field 1, uint 0
					if local && i%2 == 0 {
						// Metadata after the value must still rename it, without
						// changing the inherited schema for the next sibling.
						elem = (&ob.SchemaMeta{Fields: map[uint32]string{1: "local"}}).AppendField0(elem, true)
					}
					body = wire.AppendVarint(body, uint64(len(elem)))
					body = append(body, elem...)
				}
				packet = wire.AppendVarint(packet, 1<<4|uint64(wire.WireArray))
				packet = wire.AppendVarint(packet, uint64(len(body)))
				packet = append(packet, body...)
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				out, err := ob.DecodeBlind(packet)
				runtime.ReadMemStats(&after)
				if err != nil {
					t.Fatal(err)
				}
				children := out["field_1"].([]map[string]any)
				if len(children) != count {
					t.Fatal("wrong child count")
				}
				for i, child := range children {
					name := "name_1"
					if local && i%2 == 0 {
						name = "local"
					}
					if value, ok := child[name]; !ok || value != uint64(0) || len(child) != 1 {
						t.Fatalf("child %d: %v", i, child)
					}
				}
				allocated := after.TotalAlloc - before.TotalAlloc
				if count == 256 {
					small = allocated
				} else if allocated > 8<<20 || allocated > small*6 {
					t.Fatalf("schema allocation is not linear: small=%d large=%d", small, allocated)
				}
			}
		})
	}
}

func TestBlindSchemaOverlayNestedMerges(t *testing.T) {
	inherited := &ob.SchemaMeta{Children: map[uint32]*ob.SchemaMeta{1: {
		Fields:   map[uint32]string{1: "nested"},
		Children: map[uint32]*ob.SchemaMeta{1: {Fields: map[uint32]string{1: "original", 2: "retained"}}},
	}}}
	// Two local metadata blocks merge before overlaying the inherited child.
	first := &ob.SchemaMeta{Children: map[uint32]*ob.SchemaMeta{1: {Fields: map[uint32]string{1: "replaced"}}}}
	last := &ob.SchemaMeta{Children: map[uint32]*ob.SchemaMeta{1: {Fields: map[uint32]string{1: ""}}}}
	grandchild := []byte{33, 3, 65, 5} // uint fields 1=1 and 2=2
	child := wire.AppendVarint(nil, 1<<4|uint64(wire.WireSubMsg))
	child = wire.AppendVarint(child, uint64(len(grandchild)))
	child = append(child, grandchild...)
	localChild := first.AppendField0(append([]byte(nil), child...), true)
	localChild = last.AppendField0(localChild, true)
	body := wire.AppendVarint([]byte{wire.WireSubMsg}, 2)
	for _, elem := range [][]byte{localChild, child} {
		body = wire.AppendVarint(body, uint64(len(elem)))
		body = append(body, elem...)
	}
	packet := inherited.AppendField0(nil, true)
	packet = wire.AppendVarint(packet, 1<<4|uint64(wire.WireArray))
	packet = wire.AppendVarint(packet, uint64(len(body)))
	packet = append(packet, body...)
	got, err := ob.DecodeBlind(packet)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"field_1": []map[string]any{
		{"nested": map[string]any{"field_1": uint64(1), "retained": uint64(2)}},
		{"nested": map[string]any{"original": uint64(1), "retained": uint64(2)}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnknownFieldDepthAndReservedTagsAndSpliceValidation(t *testing.T) {
	// 1. Unknown nested WireArray exceeding MaxNestingDepth must be rejected across all schema-aware tiers.
	deepUnknown := []byte{0x21, 0x05} // field #1 uint = 2 inside deepest array
	for range ob.MaxNestingDepth + 2 {
		body := wire.AppendVarint([]byte{wire.WireSubMsg}, 1)
		body = wire.AppendVarint(body, uint64(len(deepUnknown)))
		body = append(body, deepUnknown...)
		pkt := wire.AppendVarint(nil, 999<<4|uint64(wire.WireArray))
		pkt = wire.AppendVarint(pkt, uint64(len(body)))
		deepUnknown = append(pkt, body...)
	}
	var genMsg pb.TestAllTypes
	if err := genMsg.UnmarshalOmniBuf(deepUnknown, false); err == nil {
		t.Fatal("generated decoder accepted deep unknown field > MaxNestingDepth")
	}
	if err := ob.Unmarshal(deepUnknown, &genMsg); err == nil {
		t.Fatal("protoreflect decoder accepted deep unknown field > MaxNestingDepth")
	}
	type taggedMsg struct {
		ID uint32 `omnibuf:"1"`
	}
	var tm taggedMsg
	if err := ob.Unmarshal(deepUnknown, &tm); err == nil {
		t.Fatal("table decoder accepted deep unknown field > MaxNestingDepth")
	}

	// 2. Protobuf reserved field numbers 19000..19999 must be rejected in struct tags.
	type reservedTagMsg struct {
		Bad uint32 `omnibuf:"19000"`
	}
	if _, err := ob.Marshal(&reservedTagMsg{Bad: 1}); err == nil {
		t.Fatal("Marshal accepted reserved field number 19000")
	}

	// 3. SpliceFields must not append corrupt tail bytes on malformed input.
	validField1 := []byte{0x21, 0x09} // field #1 uint = 4
	corruptField2 := []byte{0x55}     // field #2 string, truncated length
	raw := append(append([]byte(nil), validField1...), corruptField2...)
	spliced := wire.SpliceFields(nil, raw, 1<<3, 3, func(dst []byte, fnum uint16) []byte {
		dst = wire.AppendVarint(dst, uint64(fnum)<<4|uint64(wire.WireUint))
		return wire.AppendVarint(dst, 77)
	})
	if _, ok := wire.ReadUint(spliced, 1); !ok {
		t.Fatal("expected valid field #1 in spliced prefix")
	}
	if bytes.Contains(spliced, corruptField2) {
		t.Fatal("SpliceFields copied corrupt tail bytes into output")
	}

	// 4. AppendUint32Arr must preserve caller's orig slice when packet is malformed.
	orig := []uint32{11, 22}
	gotSlice := wire.AppendUint32Arr(orig, corruptField2, 1)
	if len(gotSlice) != 2 || gotSlice[0] != 11 || gotSlice[1] != 22 {
		t.Fatalf("AppendUint32Arr failed to preserve orig slice: got %v", gotSlice)
	}
}

func TestWireEncoderStackGrowthBeyond64(t *testing.T) {
	enc := wire.NewEncoder(256)
	const levels = 40 // 40 SubMsgArr + 40 SubMsgElem = 80 stack slots (> 64)
	for range levels {
		enc.BeginSubMsgArr(1)
		enc.BeginSubMsgElem()
	}
	enc.BeginSubMsg(2)
	enc.WriteUint(1, 777)
	enc.EndSubMsg()
	for range levels {
		enc.EndSubMsgElem()
		enc.EndSubMsgArr()
	}
	pkt := enc.Finish()
	cur := pkt
	for i := range levels {
		elem, ok := wire.ReadSubMsgArrElem(cur, 1, 0)
		if !ok {
			t.Fatalf("failed reading submsg array elem at level %d", i)
		}
		cur = elem
	}
	inner, ok := wire.ReadSubMsg(cur, 2)
	if !ok {
		t.Fatal("failed reading inner submsg at leaf")
	}
	v, ok := wire.ReadUint(inner, 1)
	if !ok || v != 777 {
		t.Fatalf("expected inner uint 777, got %d (ok=%v)", v, ok)
	}
}

func TestZeroCopyBytesCapacityClippingAndOpaqueCleanAlias(t *testing.T) {
	// 1. Struct-tagged UnmarshalZeroCopy must clip []byte slice capacity so append() cannot corrupt packet.
	type bytesMsg struct {
		Raw     []byte            `omnibuf:"1"`
		RawArr  [][]byte          `omnibuf:"2"`
		StrArr  [][]byte          `omnibuf:"3,wire=string"`
		RawMap  map[string][]byte `omnibuf:"4"`
		Trailer string            `omnibuf:"5"`
	}
	src := bytesMsg{
		Raw:     []byte("abcd"),
		RawArr:  [][]byte{[]byte("ef")},
		StrArr:  [][]byte{[]byte("gh")},
		RawMap:  map[string][]byte{"k": []byte("ij")},
		Trailer: "UNTOUCHED",
	}
	pkt, err := ob.Marshal(&src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	origCopy := append([]byte(nil), pkt...)

	var dst bytesMsg
	if err := ob.UnmarshalZeroCopy(pkt, &dst); err != nil {
		t.Fatalf("UnmarshalZeroCopy failed: %v", err)
	}
	_ = append(dst.Raw, 'X', 'Y', 'Z')
	_ = append(dst.RawArr[0], 'X', 'Y', 'Z')
	_ = append(dst.StrArr[0], 'X', 'Y', 'Z')
	_ = append(dst.RawMap["k"], 'X', 'Y', 'Z')
	if !bytes.Equal(pkt, origCopy) {
		t.Fatalf("append on zero-copy struct slices mutated source packet:\ngot  %x\nwant %x", pkt, origCopy)
	}

	// 2. Generated UnmarshalOmniBuf(pkt, true) and Opaque.RawBytes() must clip []byte capacities.
	pbSrc := &pb.TestAllTypes{
		F7Bytes:     []byte("hello"),
		F6String:    "world",
		F9StringArr: []string{"short", string(bytes.Repeat([]byte("a"), 200))},
		F8SubMsg:    &pb.AllTypesSubMsg{Id: 42, Label: "sub"},
		F11SubMsgArr: []*pb.AllTypesSubMsg{
			{Id: 1, Label: "first"},
			{Id: 2, Label: "second"},
		},
	}
	pbPkt := pbSrc.MarshalOmniBufAppend(nil)
	pbOrig := append([]byte(nil), pbPkt...)

	var pbDec pb.TestAllTypes
	if err := pbDec.UnmarshalOmniBuf(pbPkt, true); err != nil {
		t.Fatalf("UnmarshalOmniBuf(zeroCopy) failed: %v", err)
	}
	_ = append(pbDec.F7Bytes, '!', '!', '!')
	if !bytes.Equal(pbPkt, pbOrig) {
		t.Fatal("append on generated zero-copy F7Bytes mutated source packet")
	}

	// 3. Low-level wire slice readers must clip capacity.
	if b, ok := wire.ReadBytes(pbPkt, 7); !ok {
		t.Fatal("ReadBytes failed")
	} else {
		_ = append(b, '!')
	}
	if sb, ok := wire.ReadStringBytes(pbPkt, 6); !ok {
		t.Fatal("ReadStringBytes short failed")
	} else {
		_ = append(sb, '!')
	}
	// Long string (> 127B, 2-byte varint length) + bytes array
	enc := wire.NewEncoder(300)
	enc.WriteString(1, string(bytes.Repeat([]byte("z"), 150)))
	wirePkt := append([]byte(nil), enc.Finish()...)
	wirePkt = wire.AppendVarint(wirePkt, (2<<4)|uint64(wire.WireArray))
	wirePkt = wire.EncodeBytesArr(wirePkt, [][]byte{[]byte("elem0"), []byte("elem1")})
	wirePkt = wire.AppendVarint(wirePkt, (3<<4)|uint64(wire.WireUint))
	wirePkt = wire.AppendVarint(wirePkt, 999)
	wireOrig := append([]byte(nil), wirePkt...)
	if sbLong, ok := wire.ReadStringBytes(wirePkt, 1); !ok {
		t.Fatal("ReadStringBytes long failed")
	} else {
		_ = append(sbLong, '!')
	}
	arrSlices := wire.AppendBytesArr(nil, wirePkt, 2, true)
	if len(arrSlices) != 2 {
		t.Fatalf("AppendBytesArr got %d elems", len(arrSlices))
	}
	_ = append(arrSlices[0], '!')
	if !bytes.Equal(wirePkt, wireOrig) {
		t.Fatal("append on wire.ReadStringBytes / AppendBytesArr mutated source packet")
	}

	if sub, ok := wire.ReadSubMsg(pbPkt, 8); !ok {
		t.Fatal("ReadSubMsg failed")
	} else {
		_ = append(sub, '!')
	}
	if elem0, ok := wire.ReadSubMsgArrElem(pbPkt, 11, 0); !ok {
		t.Fatal("ReadSubMsgArrElem failed")
	} else {
		_ = append(elem0, '!')
	}
	if !bytes.Equal(pbPkt, pbOrig) {
		t.Fatal("append on wire.ReadSubMsg / ReadSubMsgArrElem mutated source packet")
	}

	// 4. Opaque.Unmarshal clips RawBytes() capacity AND clean Opaque.MarshalAppend detaches overlapping dst.
	backing := make([]byte, 16+len(pbOrig)+32)
	copy(backing[16:], pbOrig)
	copy(backing[16+len(pbOrig):], "SENTINEL")

	var op pb.OpaqueTestAllTypes
	if err := op.Unmarshal(backing[16 : 16+len(pbOrig)]); err != nil {
		t.Fatalf("Opaque.Unmarshal failed: %v", err)
	}
	_ = append(op.RawBytes(), 'X', 'Y')
	if string(backing[16+len(pbOrig):16+len(pbOrig)+8]) != "SENTINEL" {
		t.Fatal("append on Opaque.RawBytes() overwrote trailing bytes in backing buffer")
	}

	aliasedOut := op.MarshalAppend(backing[:0])
	if !bytes.Equal(aliasedOut, pbOrig) {
		t.Fatal("Opaque.MarshalAppend(aliased) returned wrong bytes")
	}
	if !bytes.Equal(op.RawBytes(), pbOrig) || op.GetF6String() != "world" {
		t.Fatalf("clean Opaque.MarshalAppend(aliased) overwrote op.raw in place: F6String=%q", op.GetF6String())
	}
	aliasedOut[0] ^= 0xff
	if !bytes.Equal(op.RawBytes(), pbOrig) {
		t.Fatal("mutating Opaque.MarshalAppend output mutated op.raw")
	}
}
