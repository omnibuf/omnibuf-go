package omnibufproto_test

import (
	"bytes"
	"reflect"
	"runtime"
	"strings"
	"testing"

	ob "github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/omnibufproto"
	"github.com/omnibuf/omnibuf-go/omnibufproto/internal/pb"
	"github.com/omnibuf/omnibuf-go/wire"
)

func TestMarshalAppendAndCopyPacketPreserveDstPrefix(t *testing.T) {
	prefix := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	makeDst := func(capacity int) []byte {
		b := make([]byte, len(prefix), capacity)
		copy(b, prefix)
		return b
	}

	type simpleRecord struct {
		ID uint32 `omnibuf:"1"`
	}

	samplePB := &pb.PeerUpdate{Asn: 64512, RouterId: "rtr-fra-01"}
	sampleRaw, err := omnibufproto.Marshal(samplePB)
	if err != nil {
		t.Fatalf("precondition Marshal samplePB: %v", err)
	}

	for _, capSize := range []int{len(prefix), 1024} {
		capName := "small_cap"
		if capSize > len(prefix) {
			capName = "large_cap"
		}

		t.Run("omnibuf_MarshalAppend_"+capName, func(t *testing.T) {
			out, err := ob.MarshalAppend(makeDst(capSize), &simpleRecord{ID: 7})
			if err != nil {
				t.Fatalf("omnibuf.MarshalAppend: %v", err)
			}
			if !bytes.HasPrefix(out, prefix) {
				t.Fatalf("omnibuf.MarshalAppend wiped prefix %x: got %x", prefix, out)
			}
		})

		t.Run("omnibufproto_MarshalAppend_SelfDescribing_"+capName, func(t *testing.T) {
			// Test both open-struct proto and protoreflect-dispatched proto (TestAllTypes has enums/oneofs).
			outOpen, err := (omnibufproto.MarshalOptions{SelfDescribing: true}).MarshalAppend(makeDst(capSize), samplePB)
			if err != nil {
				t.Fatalf("MarshalOptions{SelfDescribing:true}.MarshalAppend(PeerUpdate): %v", err)
			}
			if !bytes.HasPrefix(outOpen, prefix) {
				t.Errorf("MarshalOptions{SelfDescribing:true}.MarshalAppend(PeerUpdate) wiped prefix %x: got %x", prefix, outOpen)
			}

			outReflect, err := (omnibufproto.MarshalOptions{SelfDescribing: true}).MarshalAppend(makeDst(capSize), &pb.TestAllTypes{F1Uint32: 42})
			if err != nil {
				t.Fatalf("MarshalOptions{SelfDescribing:true}.MarshalAppend(TestAllTypes): %v", err)
			}
			if !bytes.HasPrefix(outReflect, prefix) {
				t.Errorf("MarshalOptions{SelfDescribing:true}.MarshalAppend(TestAllTypes) wiped prefix %x: got %x", prefix, outReflect)
			}
		})

		t.Run("wire_CopyPacket_"+capName, func(t *testing.T) {
			out := wire.CopyPacket(makeDst(capSize), sampleRaw)
			if !bytes.HasPrefix(out, prefix) || !bytes.Equal(out[len(prefix):], sampleRaw) {
				t.Fatalf("wire.CopyPacket wiped prefix %x: got %x", prefix, out)
			}
		})

		t.Run("OpaquePeerUpdate_MarshalAppend_clean_"+capName, func(t *testing.T) {
			var op pb.OpaquePeerUpdate
			if err := op.Unmarshal(sampleRaw); err != nil {
				t.Fatalf("OpaquePeerUpdate.Unmarshal: %v", err)
			}
			out := op.MarshalAppend(makeDst(capSize))
			if !bytes.HasPrefix(out, prefix) {
				t.Fatalf("OpaquePeerUpdate.MarshalAppend (clean) wiped prefix %x: got %x", prefix, out)
			}
		})

		t.Run("OpaquePeerUpdate_MarshalAppend_dirty_"+capName, func(t *testing.T) {
			var op pb.OpaquePeerUpdate
			if err := op.Unmarshal(sampleRaw); err != nil {
				t.Fatalf("OpaquePeerUpdate.Unmarshal: %v", err)
			}
			op.SetAsn(65001)
			out := op.MarshalAppend(makeDst(capSize))
			if !bytes.HasPrefix(out, prefix) {
				t.Fatalf("OpaquePeerUpdate.MarshalAppend (dirty) wiped prefix %x: got %x", prefix, out)
			}
		})
	}
}

func TestOpaqueResetAndStringSliceTailZeroing(t *testing.T) {
	t.Run("OpaquePeerUpdate_Unmarshal_clears_dirty_heap_fields", func(t *testing.T) {
		var op pb.OpaquePeerUpdate
		op.SetRouterId(strings.Repeat("dirty-router-", 4))
		op.SetTags([]string{"dirty-tag-1", "dirty-tag-2"})
		op.SetLocation(&pb.Location{City: "Frankfurt"})

		if err := op.Unmarshal([]byte{}); err != nil {
			t.Fatalf("OpaquePeerUpdate.Unmarshal(empty): %v", err)
		}

		rv := reflect.ValueOf(&op).Elem()
		if gotRouter := rv.FieldByName("f_router_id").String(); gotRouter != "" {
			t.Errorf("OpaquePeerUpdate retained dirty f_router_id = %q after Unmarshal(empty)", gotRouter)
		}
		if gotTagsLen := rv.FieldByName("f_tags").Len(); gotTagsLen != 0 {
			t.Errorf("OpaquePeerUpdate retained dirty f_tags (len=%d) after Unmarshal(empty)", gotTagsLen)
		}
		if !rv.FieldByName("f_location").IsNil() {
			t.Errorf("OpaquePeerUpdate retained dirty f_location pointer after Unmarshal(empty)")
		}
	})

	t.Run("Unmarshal_shrinking_string_slice_zeros_tail", func(t *testing.T) {
		type stringListMsg struct {
			Items []string `omnibuf:"1"`
		}
		pkt4, err := ob.Marshal(&stringListMsg{Items: []string{"s0", "s1", "s2", "s3"}})
		if err != nil {
			t.Fatalf("Marshal 4 strings: %v", err)
		}
		pkt1, err := ob.Marshal(&stringListMsg{Items: []string{"only"}})
		if err != nil {
			t.Fatalf("Marshal 1 string: %v", err)
		}

		var dst stringListMsg
		if err := ob.Unmarshal(pkt4, &dst); err != nil {
			t.Fatalf("Unmarshal 4 strings: %v", err)
		}
		if err := ob.Unmarshal(pkt1, &dst); err != nil {
			t.Fatalf("Unmarshal 1 string: %v", err)
		}
		if cap(dst.Items) < 4 {
			t.Fatalf("precondition failed: expected reused slice cap >= 4, got %d", cap(dst.Items))
		}
		backing := dst.Items[:4]
		for i := 1; i < 4; i++ {
			if backing[i] != "" {
				t.Errorf("omnibuf.Unmarshal left stale string header at backing[%d] = %q", i, backing[i])
			}
		}

		pb4, err := omnibufproto.Marshal(&pb.PeerUpdate{Tags: []string{"t0", "t1", "t2", "t3"}})
		if err != nil {
			t.Fatalf("Marshal PeerUpdate 4 tags: %v", err)
		}
		pb1, err := omnibufproto.Marshal(&pb.PeerUpdate{Tags: []string{"only"}})
		if err != nil {
			t.Fatalf("Marshal PeerUpdate 1 tag: %v", err)
		}
		var pbDst pb.PeerUpdate
		if err := omnibufproto.Unmarshal(pb4, &pbDst); err != nil {
			t.Fatalf("Unmarshal PeerUpdate 4 tags: %v", err)
		}
		if err := omnibufproto.Unmarshal(pb1, &pbDst); err != nil {
			t.Fatalf("Unmarshal PeerUpdate 1 tag: %v", err)
		}
		pbBacking := pbDst.Tags[:4]
		for i := 1; i < 4; i++ {
			if pbBacking[i] != "" {
				t.Errorf("omnibufproto.Unmarshal left stale string header at pbDst.Tags[:4][%d] = %q", i, pbBacking[i])
			}
		}
	})
}

func TestOpaqueMarshalRejectsCorruptPacket(t *testing.T) {
	// Valid field #1 followed by truncated 2B varint at end of packet
	var corrupt []byte
	corrupt = appendTag(corrupt, 1, wire.WireUint)
	corrupt = wire.AppendVarint(corrupt, 10)
	corrupt = append(corrupt, 0x41, 0x02) // field #2 WireUint with truncated 2B varint

	var op pb.OpaqueTestAllTypes
	_ = op.Unmarshal(corrupt)
	op.SetF1Uint32(99)
	if _, err := op.Marshal(); err == nil {
		t.Fatal("expected OpaqueTestAllTypes.Marshal to fail on trailing truncated field")
	}
	if _, err := op.TryMarshalAppend(nil); err == nil {
		t.Fatal("expected OpaqueTestAllTypes.TryMarshalAppend to fail on trailing truncated field")
	}
}

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

func TestAbsentStringClearedOnReuse(t *testing.T) {
	type message struct {
		Text string `omnibuf:"1"`
	}
	value := message{Text: strings.Repeat("live", 1024)}
	if err := ob.Unmarshal(nil, &value); err != nil {
		t.Fatal(err)
	}
	if value.Text != "" {
		t.Fatal("absent string was not cleared")
	}
	runtime.GC()
}
