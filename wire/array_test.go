package wire

import (
	"reflect"
	"testing"
)

func TestAppendFixedArrBodyStrictWireType(t *testing.T) {
	f32Body := EncodeFixed32Arr(nil, []uint32{1, 2}, WireFixed32)
	// Strip outer container length prefix (2-byte backpatched varint)
	_, n := DecodeVarint(f32Body)
	f32Body = f32Body[n:]

	s32Body := EncodeFixed32Arr(nil, []int32{-1, -2}, WireSfixed32)
	_, n = DecodeVarint(s32Body)
	s32Body = s32Body[n:]

	fl32Body := EncodeFloat32Arr(nil, []float32{1.5})
	_, n = DecodeVarint(fl32Body)
	fl32Body = fl32Body[n:]

	if got, ok := AppendFixed32ArrBody[uint32](nil, f32Body); !ok || !reflect.DeepEqual(got, []uint32{1, 2}) {
		t.Fatalf("AppendFixed32ArrBody(WireFixed32) = (%v, %v)", got, ok)
	}
	if _, ok := AppendFixed32ArrBody[uint32](nil, s32Body); ok {
		t.Fatal("AppendFixed32ArrBody accepted WireSfixed32")
	}
	if _, ok := AppendFixed32ArrBody[uint32](nil, fl32Body); ok {
		t.Fatal("AppendFixed32ArrBody accepted WireFloat32")
	}
	if got, ok := AppendSfixed32ArrBody[int32](nil, s32Body); !ok || !reflect.DeepEqual(got, []int32{-1, -2}) {
		t.Fatalf("AppendSfixed32ArrBody(WireSfixed32) = (%v, %v)", got, ok)
	}
	if _, ok := AppendSfixed32ArrBody[int32](nil, f32Body); ok {
		t.Fatal("AppendSfixed32ArrBody accepted WireFixed32")
	}

	f64Body := EncodeFixed64Arr(nil, []uint64{10, 20}, WireFixed64)
	_, n = DecodeVarint(f64Body)
	f64Body = f64Body[n:]

	s64Body := EncodeFixed64Arr(nil, []int64{-10, -20}, WireSfixed64)
	_, n = DecodeVarint(s64Body)
	s64Body = s64Body[n:]

	if got, ok := AppendFixed64ArrBody[uint64](nil, f64Body); !ok || !reflect.DeepEqual(got, []uint64{10, 20}) {
		t.Fatalf("AppendFixed64ArrBody(WireFixed64) = (%v, %v)", got, ok)
	}
	if _, ok := AppendFixed64ArrBody[uint64](nil, s64Body); ok {
		t.Fatal("AppendFixed64ArrBody accepted WireSfixed64")
	}
	if got, ok := AppendSfixed64ArrBody[int64](nil, s64Body); !ok || !reflect.DeepEqual(got, []int64{-10, -20}) {
		t.Fatalf("AppendSfixed64ArrBody(WireSfixed64) = (%v, %v)", got, ok)
	}
	if _, ok := AppendSfixed64ArrBody[int64](nil, f64Body); ok {
		t.Fatal("AppendSfixed64ArrBody accepted WireFixed64")
	}
}
