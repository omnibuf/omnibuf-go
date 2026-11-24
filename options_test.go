package omnibuf_test

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/wire"
)

type mergeSub struct {
	A uint32 `omnibuf:"1,name=a,omitempty"`
	B string `omnibuf:"2,name=b,omitempty"`
}

type mergeMsg struct {
	ID   uint32            `omnibuf:"1,name=id,omitempty"`
	Name string            `omnibuf:"2,name=name,omitempty"`
	Tags []string          `omnibuf:"3,name=tags,omitempty"`
	Meta map[string]uint64 `omnibuf:"4,name=meta,omitempty"`
	Sub  *mergeSub         `omnibuf:"5,name=sub,omitempty"`
}

func TestUnmarshalOptions_MaxBytesMaxDepthAndMerge(t *testing.T) {
	p1, err := omnibuf.Marshal(&mergeMsg{
		ID:   10,
		Name: "alpha",
		Tags: []string{"t1"},
		Meta: map[string]uint64{"k1": 1},
		Sub:  &mergeSub{A: 7},
	})
	if err != nil {
		t.Fatalf("Marshal p1: %v", err)
	}
	p2, err := omnibuf.Marshal(&mergeMsg{
		Name: "beta",
		Tags: []string{"t2"},
		Meta: map[string]uint64{"k2": 2},
		Sub:  &mergeSub{B: "nested"},
	})
	if err != nil {
		t.Fatalf("Marshal p2: %v", err)
	}

	// MaxBytes cap
	var dst mergeMsg
	err = omnibuf.UnmarshalOptions{MaxBytes: len(p1) - 1}.Unmarshal(p1, &dst)
	if !errors.Is(err, omnibuf.ErrMessageTooLarge) {
		t.Fatalf("expected ErrMessageTooLarge, got %v", err)
	}
	if err := (omnibuf.UnmarshalOptions{MaxBytes: len(p1)}).Unmarshal(p1, &dst); err != nil {
		t.Fatalf("exact MaxBytes failed: %v", err)
	}

	// Merge preserves existing fields, appends slices, merges maps and sub-messages
	if err := omnibuf.UnmarshalMerge(p2, &dst); err != nil {
		t.Fatalf("UnmarshalMerge failed: %v", err)
	}
	want := mergeMsg{
		ID:   10,
		Name: "beta",
		Tags: []string{"t1", "t2"},
		Meta: map[string]uint64{"k1": 1, "k2": 2},
		Sub:  &mergeSub{A: 7, B: "nested"},
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatalf("UnmarshalMerge got %+v (sub=%+v), want %+v (sub=%+v)", dst, dst.Sub, want, want.Sub)
	}

	// MaxDepth cap
	type deepNode struct {
		Child *deepNode `omnibuf:"1,omitempty"`
	}
	deepPkt, err := omnibuf.Marshal(&deepNode{Child: &deepNode{Child: &deepNode{}}})
	if err != nil {
		t.Fatalf("Marshal deep: %v", err)
	}
	var deepDst deepNode
	err = omnibuf.UnmarshalOptions{MaxDepth: 1}.Unmarshal(deepPkt, &deepDst)
	if !errors.Is(err, omnibuf.ErrMaxDepth) {
		t.Fatalf("expected ErrMaxDepth for MaxDepth=1, got %v", err)
	}
	if err := (omnibuf.UnmarshalOptions{MaxDepth: 2}).Unmarshal(deepPkt, &deepDst); err != nil {
		t.Fatalf("expected MaxDepth=2 to succeed, got %v", err)
	}
}

func TestSentinelErrors(t *testing.T) {
	// ErrInvalidUTF8 on Marshal and Unmarshal
	type strMsg struct {
		S string `omnibuf:"1"`
	}
	if _, err := omnibuf.Marshal(&strMsg{S: string([]byte{0xff, 0xfe})}); !errors.Is(err, omnibuf.ErrInvalidUTF8) {
		t.Fatalf("Marshal invalid UTF-8: got %v, want ErrInvalidUTF8", err)
	}

	enc := wire.NewEncoder(32)
	enc.WriteString(1, string([]byte{0xff, 0xfe}))
	var dst strMsg
	if err := omnibuf.Unmarshal(enc.Bytes(), &dst); !errors.Is(err, omnibuf.ErrInvalidUTF8) {
		t.Fatalf("Unmarshal invalid UTF-8: got %v, want ErrInvalidUTF8", err)
	}

	// ErrCorruptWire on truncated packet
	if err := omnibuf.Unmarshal([]byte{0x02}, &dst); !errors.Is(err, omnibuf.ErrCorruptWire) {
		t.Fatalf("Unmarshal truncated tag: got %v, want ErrCorruptWire", err)
	}
}

func TestIntuitiveWireStructTags(t *testing.T) {
	type tagged struct {
		A int32              `omnibuf:"1,wire=sfixed32"`
		B int64              `omnibuf:"2,wire=sfixed64"`
		C float32            `omnibuf:"3,wire=float32"`
		D float64            `omnibuf:"4,wire=float64"`
		E []int32            `omnibuf:"5,wire=sfixed32"`
		F []int64            `omnibuf:"6,wire=sfixed64"`
		G []float32          `omnibuf:"7,wire=float32"`
		H []float64          `omnibuf:"8,wire=float64"`
		M map[string]int32   `omnibuf:"9,wire=sfixed32"`
		N map[string]float64 `omnibuf:"10,wire=float64"`
	}
	in := tagged{
		A: -42,
		B: -1234567890123,
		C: 3.25,
		D: math.Pi,
		E: []int32{-1, 0, 1},
		F: []int64{-100, 200},
		G: []float32{1.5, -2.5},
		H: []float64{0.25, -0.75},
		M: map[string]int32{"k": -9},
		N: map[string]float64{"p": 2.5},
	}
	raw, err := omnibuf.Marshal(&in)
	if err != nil {
		t.Fatalf("Marshal tagged: %v", err)
	}
	var out tagged
	if err := omnibuf.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal tagged: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}

	// Verify exact wire types on the wire
	if v, ok := wire.ReadSfixed32(raw, 1); !ok || v != -42 {
		t.Fatalf("field 1 ReadSfixed32 = (%d, %v), want (-42, true)", v, ok)
	}
	if v, ok := wire.ReadSfixed64(raw, 2); !ok || v != -1234567890123 {
		t.Fatalf("field 2 ReadSfixed64 = (%d, %v), want (-1234567890123, true)", v, ok)
	}
	if v, ok := wire.ReadFloat32(raw, 3); !ok || v != 3.25 {
		t.Fatalf("field 3 ReadFloat32 = (%v, %v), want (3.25, true)", v, ok)
	}
	if v, ok := wire.ReadFloat64(raw, 4); !ok || v != math.Pi {
		t.Fatalf("field 4 ReadFloat64 = (%v, %v), want (%v, true)", v, ok, math.Pi)
	}
}
