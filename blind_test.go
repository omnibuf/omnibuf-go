package omnibuf_test

import (
	"reflect"
	"testing"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/wire"
)

func TestDecodeBlind_SingleAndMergedSubMessages(t *testing.T) {
	// Single-occurrence deeply nested submessage
	enc := wire.NewEncoder(64)
	enc.BeginSubMsg(1)
	enc.BeginSubMsg(2)
	enc.WriteString(3, "leaf")
	enc.EndSubMsg()
	enc.EndSubMsg()
	got, err := omnibuf.DecodeBlind(enc.Bytes())
	if err != nil {
		t.Fatalf("DecodeBlind nested: %v", err)
	}
	want := map[string]any{
		"field_1": map[string]any{
			"field_2": map[string]any{
				"field_3": "leaf",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeBlind nested = %#v, want %#v", got, want)
	}

	// Multi-occurrence merged submessages
	enc2 := wire.NewEncoder(64)
	enc2.BeginSubMsg(1)
	enc2.WriteUint(1, 10)
	enc2.EndSubMsg()
	enc2.BeginSubMsg(1)
	enc2.WriteString(2, "merged")
	enc2.EndSubMsg()
	got2, err := omnibuf.DecodeBlind(enc2.Bytes())
	if err != nil {
		t.Fatalf("DecodeBlind merged: %v", err)
	}
	want2 := map[string]any{
		"field_1": map[string]any{
			"field_1": uint64(10),
			"field_2": "merged",
		},
	}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("DecodeBlind merged = %#v, want %#v", got2, want2)
	}

	// Truncated first segment must NOT be rescued by concatenating a second segment
	var bad []byte
	bad = wire.AppendVarint(bad, (1<<4)|uint64(wire.WireSubMsg))
	bad = wire.AppendVarint(bad, 1)
	bad = append(bad, (((1<<4)|wire.WireUint)<<1)|1) // tag with missing varint payload
	bad = wire.AppendVarint(bad, (1<<4)|uint64(wire.WireSubMsg))
	bad = wire.AppendVarint(bad, 1)
	bad = append(bad, 0x03) // would look like varint 1 if concatenated without per-segment validation
	if _, err := omnibuf.DecodeBlind(bad); err == nil {
		t.Fatal("DecodeBlind accepted truncated first submessage segment merged with second segment")
	}
}
