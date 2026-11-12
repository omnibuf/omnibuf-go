package omnibuf

import (
	"math"
	"reflect"
	"testing"
	"unsafe"

	"github.com/omnibuf/omnibuf-go/wire"
)

func TestFastSkipValueOverflowAnd32BitKinds(t *testing.T) {
	// 1. fastSkipValue and wire.SkipValue must never succeed via signed int overflow when pos is near math.MaxInt.
	for _, wt := range []uint8{wireUint, wireSint, wireString, wireFixed32, wireFixed64} {
		if next, ok := fastSkipValue(nil, math.MaxInt-1, wt); ok {
			t.Fatalf("fastSkipValue overflowed to next=%d, ok=true for wt=%d", next, wt)
		}
		if next, err := wire.SkipValue(nil, math.MaxInt-1, wt); err == nil {
			t.Fatalf("wire.SkipValue overflowed to next=%d, err=nil for wt=%d", next, wt)
		}
	}

	// 2. When int/uint are 4 bytes (32-bit architectures), compileStructCodec must emit
	// 32-bit field/slice kinds that never read or write past 4 bytes per value.
	origIntBytes := ptrIntBytes
	ptrIntBytes = 4
	defer func() { ptrIntBytes = origIntBytes }()

	sc32, err := compileStructCodec(reflect.TypeFor[struct {
		I    int    `omnibuf:"1"`
		U    uint   `omnibuf:"2"`
		IF64 int    `omnibuf:"3,wire=fixed64"`
		UF64 uint   `omnibuf:"4,wire=fixed64"`
		IS   []int  `omnibuf:"5"`
		US   []uint `omnibuf:"6"`
	}](), make(map[reflect.Type]*structCodec))
	if err != nil {
		t.Fatalf("compileStructCodec (32-bit mode) failed: %v", err)
	}
	wantKinds := []fieldKind{
		kindInt32,
		kindUint32,
		kindSfixed64_32,
		kindFixed64_32,
		kindInt32Slice,
		kindUint32Slice,
	}
	for i, want := range wantKinds {
		if got := sc32.fields[i].kind; got != want {
			t.Fatalf("field %d: got kind %v, want %v", i, got, want)
		}
	}

	// Verify 32-bit decode into a raw memory slot never clobbers the adjacent 4 bytes at offset +4.
	var raw [8]byte
	*(*uint32)(unsafe.Add(unsafe.Pointer(&raw[0]), 4)) = 0xdeadbeef
	// Encode field #1 (sint = -5 -> zigzag 9 -> prefix varint 0x13)
	pkt := []byte{0x23, 0x13}
	if err := sc32.decodeStruct(pkt, unsafe.Pointer(&raw[0]), false, 0, true); err != nil {
		t.Fatalf("sc32.decodeStruct failed: %v", err)
	}
	if got := *(*int32)(unsafe.Pointer(&raw[0])); got != -5 {
		t.Fatalf("decoded int32 slot = %d, want -5", got)
	}
	if sentinel := *(*uint32)(unsafe.Add(unsafe.Pointer(&raw[0]), 4)); sentinel != 0xdeadbeef {
		t.Fatalf("adjacent 4-byte slot clobbered: got 0x%x, want 0xdeadbeef", sentinel)
	}
}
