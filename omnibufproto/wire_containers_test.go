package omnibufproto_test

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	_ "github.com/omnibuf/omnibuf-go/omnibufproto"

	ob "github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/omnibufproto/internal/pb"
	"github.com/omnibuf/omnibuf-go/wire"
)

func TestStructTableMultiLevelScalarPointer(t *testing.T) {
	type DoublePtr struct {
		Val **uint32 `omnibuf:"1"`
	}

	v := uint32(424242)
	p := &v
	src := DoublePtr{Val: &p}

	pkt, err := ob.Marshal(&src)
	if err != nil {
		t.Fatalf("omnibuf.Marshal(&DoublePtr): %v", err)
	}
	if err := wire.ValidatePacket(pkt); err != nil {
		t.Fatalf("omnibuf.Marshal(&DoublePtr) emitted invalid packet %x: %v", pkt, err)
	}

	var dst DoublePtr
	if err := ob.Unmarshal(pkt, &dst); err != nil {
		t.Fatalf("omnibuf.Unmarshal(DoublePtr) failed on packet %x: %v", pkt, err)
	}
	if dst.Val == nil || *dst.Val == nil || **dst.Val != v {
		t.Fatalf("DoublePtr round-trip mismatch: got %+v, want %d", dst.Val, v)
	}
}

func TestUint32SintSliceNoSignExtension(t *testing.T) {
	type scalarSint struct {
		Val uint32 `omnibuf:"1,wire=sint"`
	}
	type sliceSint struct {
		Vals []uint32 `omnibuf:"1,wire=sint"`
	}

	const large uint32 = 3_000_000_000

	scalarPkt, err := ob.Marshal(&scalarSint{Val: large})
	if err != nil {
		t.Fatalf("Marshal scalarSint: %v", err)
	}
	scalarBlind, err := ob.DecodeBlind(scalarPkt)
	if err != nil {
		t.Fatalf("DecodeBlind scalarSint: %v", err)
	}
	if scalarBlind["field_1"] != int64(large) {
		t.Fatalf("precondition failed: scalarSint DecodeBlind = %v, want %d", scalarBlind["field_1"], large)
	}

	slicePkt, err := ob.Marshal(&sliceSint{Vals: []uint32{large}})
	if err != nil {
		t.Fatalf("Marshal sliceSint: %v", err)
	}
	sliceBlind, err := ob.DecodeBlind(slicePkt)
	if err != nil {
		t.Fatalf("DecodeBlind sliceSint: %v", err)
	}
	want := []int64{int64(large)}
	if !reflect.DeepEqual(sliceBlind["field_1"], want) {
		t.Fatalf("sliceSint DecodeBlind sign-extended uint32: got %v (%T), want %v", sliceBlind["field_1"], sliceBlind["field_1"], want)
	}
}

func TestReadNestedStringBytesRejectsCorruptSecondSegment(t *testing.T) {
	enc := wire.NewEncoder(32)
	enc.BeginSubMsg(1)
	enc.WriteString(1, "Frankfurt")
	enc.EndSubMsg()
	validFirst := append([]byte(nil), enc.Finish()...)

	// Second occurrence of sub-message field #1 whose outer length (2 bytes) is
	// in-bounds, while its inner string field #1 claims length 50 (truncated).
	var corruptInner []byte
	corruptInner = wire.AppendVarint(corruptInner, (1<<4)|uint64(wire.WireString))
	corruptInner = wire.AppendVarint(corruptInner, 50)

	var concat []byte
	concat = append(concat, validFirst...)
	concat = wire.AppendVarint(concat, (1<<4)|uint64(wire.WireSubMsg))
	concat = wire.AppendVarint(concat, uint64(len(corruptInner)))
	concat = append(concat, corruptInner...)

	if err := wire.ValidatePacket(concat); err == nil {
		t.Fatal("precondition failed: expected ValidatePacket(concat) to fail")
	}
	if _, ok := wire.ReadSubMsg(concat, 1); ok {
		t.Fatal("precondition failed: expected ReadSubMsg(concat, 1) to reject corrupt second segment")
	}

	if got, ok := wire.ReadNestedStringBytes(concat, 1, 1); ok {
		t.Fatalf("wire.ReadNestedStringBytes succeeded on packet with corrupt second sub-message segment: got (%q, true), want (_, false)", got)
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

func TestStressBenchmarkWorkloadsRoundTrip(t *testing.T) {
	large := SampleLargeNestedPeerUpdate()
	pbLarge := SamplePBPeerUpdate(&large)

	obGen := pbLarge.MarshalOmniBufAppend(nil)
	obTag, err := ob.Marshal(&large)
	if err != nil {
		t.Fatalf("Marshal(large): %v", err)
	}
	if len(obGen) != len(obTag) {
		t.Fatalf("len(obGen)=%d != len(obTag)=%d", len(obGen), len(obTag))
	}
	var decLarge PeerUpdate
	if err := ob.Unmarshal(obGen, &decLarge); err != nil || !reflect.DeepEqual(decLarge, large) {
		t.Fatalf("Unmarshal(obGen) mismatch: err=%v", err)
	}

	protoBytes, err := MarshalProtoOfficial(pbLarge)
	if err != nil {
		t.Fatalf("MarshalProtoOfficial: %v", err)
	}
	jsonBytes, err := MarshalJSON(&large)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	t.Logf("LargeNested wire sizes: OmniBuf=%d Protobuf=%d JSON=%d",
		len(obGen), len(protoBytes), len(jsonBytes))

	many := SampleManyNeighborsPeerUpdate()
	if len(many.Neighbors) != 32 {
		t.Fatalf("len(many.Neighbors) = %d, want 32", len(many.Neighbors))
	}
	pktMany := SamplePBPeerUpdate(&many).MarshalOmniBufAppend(nil)
	grownDC := strings.Repeat("d", 20480)
	var op pb.OpaquePeerUpdate
	_ = op.Unmarshal(pktMany)
	loc := op.GetLocation()
	if loc.GetCity() != "Frankfurt" {
		t.Fatalf("loc.GetCity() = %q, want Frankfurt", loc.GetCity())
	}
	op.SetLocation(&pb.Location{
		Region:     loc.GetRegion(),
		Datacenter: grownDC,
		City:       loc.GetCity(),
		Rack:       loc.GetRack(),
		Latitude:   loc.GetLatitude(),
		Longitude:  loc.GetLongitude(),
	})
	spliced, err := op.Marshal()
	if err != nil {
		t.Fatalf("OpaquePeerUpdate.Marshal after growth failed: %v", err)
	}
	var decGrown PeerUpdate
	if err := ob.Unmarshal(spliced, &decGrown); err != nil {
		t.Fatalf("Unmarshal(spliced): %v", err)
	}
	wantGrown := many
	wantGrown.Location.Datacenter = grownDC
	if !reflect.DeepEqual(decGrown, wantGrown) {
		t.Fatalf("spliced PeerUpdate mismatch after growth")
	}
}
