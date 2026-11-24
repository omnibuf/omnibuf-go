package omnibufproto_test

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"

	_ "github.com/omnibuf/omnibuf-go/omnibufproto"

	ob "github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/wire"
)

func TestValidatePacketAndSchemaMetaRejectInvalidUTF8(t *testing.T) {
	t.Run("ValidatePacket_WireString", func(t *testing.T) {
		var pkt []byte
		pkt = wire.AppendVarint(pkt, (1<<4)|uint64(wire.WireString))
		pkt = wire.AppendVarint(pkt, 2)
		pkt = append(pkt, 0xFF, 0xFE)

		if err := wire.ValidatePacket(pkt); err == nil {
			t.Fatalf("wire.ValidatePacket accepted invalid UTF-8 WireString %x", pkt)
		}
	})

	t.Run("ValidateSchemaMeta_UnknownWireStringField", func(t *testing.T) {
		var metaBody []byte
		metaBody = wire.AppendVarint(metaBody, (9<<4)|uint64(wire.WireString))
		metaBody = wire.AppendVarint(metaBody, 2)
		metaBody = append(metaBody, 0xFF, 0xFE)

		if wire.ValidateSchemaMeta(metaBody, 1) {
			t.Errorf("wire.ValidateSchemaMeta accepted invalid UTF-8 in unknown WireString field #9: %x", metaBody)
		}

		var pkt []byte
		pkt = wire.AppendVarint(pkt, (0<<4)|uint64(wire.WireSubMsg))
		pkt = wire.AppendVarint(pkt, uint64(len(metaBody)))
		pkt = append(pkt, metaBody...)

		if err := wire.ValidatePacket(pkt); err == nil {
			t.Errorf("wire.ValidatePacket accepted Field #0 with invalid UTF-8 in unknown WireString field #9: %x", pkt)
		}
		if _, ok := ob.ReadField0SchemaMeta(pkt); ok {
			t.Errorf("omnibuf.ReadField0SchemaMeta accepted Field #0 with invalid UTF-8 in unknown WireString field #9: %x", pkt)
		}
	})
}

func TestWriteField0FieldsDeterminismAndValidation(t *testing.T) {
	t.Run("deterministic_key_order", func(t *testing.T) {
		fields := map[uint32]string{
			1: "f1", 2: "f2", 3: "f3", 4: "f4",
			5: "f5", 6: "f6", 7: "f7", 8: "f8",
			9: "f9", 10: "f10", 11: "f11", 12: "f12",
		}
		want := (&ob.SchemaMeta{Fields: fields}).AppendField0(nil, true)

		enc := wire.NewEncoder(128)
		for runIdx := 0; runIdx < 32; runIdx++ {
			enc.Reset()
			enc.WriteField0Fields(fields)
			got := enc.Finish()
			if !bytes.Equal(got, want) {
				t.Fatalf("WriteField0Fields emitted non-deterministic key order on run %d:\n got: %x\nwant: %x", runIdx, got, want)
			}
		}
	})

	t.Run("rejects_key_0_and_invalid_utf8", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			fields map[uint32]string
		}{
			{name: "key_0", fields: map[uint32]string{0: "zero", 1: "valid"}},
			{name: "invalid_utf8", fields: map[uint32]string{1: "\xff\xfe", 2: "valid"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var pkt []byte
				panicked := false
				func() {
					defer func() {
						if recover() != nil {
							panicked = true
						}
					}()
					enc := wire.NewEncoder(64)
					enc.WriteField0Fields(tc.fields)
					pkt = append([]byte(nil), enc.Finish()...)
				}()
				if !panicked {
					if err := wire.ValidatePacket(pkt); err != nil {
						t.Fatalf("WriteField0Fields(%s) silently emitted corrupt Field #0 packet %x: %v", tc.name, pkt, err)
					}
				}
			})
		}
	})
}

func TestDecodeBlindJSONNonFiniteAndNegZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  float64
	}{
		{"nan", math.NaN()},
		{"pos_inf", math.Inf(1)},
		{"neg_inf", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := wire.NewEncoder(32)
			enc.WriteFloat64(1, tc.val)
			pkt := append([]byte(nil), enc.Finish()...)
			if _, err := ob.DecodeBlindJSON(pkt); !errors.Is(err, ob.ErrNonFiniteFloat) {
				t.Fatalf("DecodeBlindJSON(%s) err = %v, want ErrNonFiniteFloat", tc.name, err)
			}
		})
	}

	enc := wire.NewEncoder(32)
	enc.WriteFloat64(1, math.Float64frombits(0x8000000000000000))
	negZeroJSON, err := ob.DecodeBlindJSON(enc.Finish())
	if err != nil {
		t.Fatalf("DecodeBlindJSON(-0.0) failed: %v", err)
	}
	if !strings.Contains(negZeroJSON, "-0") {
		t.Fatalf("DecodeBlindJSON(-0.0) = %q, expected -0 sign preserved", negZeroJSON)
	}
}

func TestSelfDescribingRecursiveTreeNode(t *testing.T) {
	root := &TreeNode{
		ID:   1,
		Name: "root-node",
		Primary: &TreeNode{
			ID:   2,
			Name: "primary-child",
			Children: []TreeNode{
				{ID: 3, Name: "grandchild-elem"},
			},
			Branches: []*TreeNode{
				{ID: 4, Name: "branch-leaf"},
			},
		},
	}

	raw, err := (ob.MarshalOptions{Deterministic: true, SelfDescribing: true}).Marshal(root)
	if err != nil {
		t.Fatalf("Marshal(SelfDescribing: true) recursive TreeNode failed: %v", err)
	}
	blind, err := ob.DecodeBlind(raw)
	if err != nil {
		t.Fatalf("DecodeBlind(SelfDescribing TreeNode) failed: %v", err)
	}
	if blind["name"] != "root-node" {
		t.Fatalf("root name = %v, want root-node", blind["name"])
	}
	prim, ok := blind["primary"].(map[string]any)
	if !ok || prim["name"] != "primary-child" {
		t.Fatalf("primary child = %v, want name=primary-child", blind["primary"])
	}
	children, ok := prim["children"].([]map[string]any)
	if !ok || len(children) != 1 {
		t.Fatalf("children = %T(%v), want 1 element", prim["children"], prim["children"])
	}
	if children[0]["name"] != "grandchild-elem" {
		t.Fatalf("grandchild name = %v, want grandchild-elem", children[0]["name"])
	}
	branches, ok := prim["branches"].([]map[string]any)
	if !ok || len(branches) != 1 {
		t.Fatalf("branches = %T(%v), want 1 element", prim["branches"], prim["branches"])
	}
	if branches[0]["name"] != "branch-leaf" {
		t.Fatalf("branch name = %v, want branch-leaf", branches[0]["name"])
	}
}

func TestSchemaMetaChildrenDepthBoundary(t *testing.T) {
	buildNestedChildrenField0 := func(levels int, withLeafFields bool) []byte {
		var body []byte
		if withLeafFields {
			var fmap []byte
			fmap = append(fmap, wire.WireUint, wire.WireString)
			fmap = wire.AppendVarint(fmap, 1)
			fmap = wire.AppendVarint(fmap, 1)
			fmap = wire.AppendVarint(fmap, 2)
			fmap = append(fmap, "id"...)
			body = appendTag(body, 2, wire.WireMap)
			body = wire.AppendVarint(body, uint64(len(fmap)))
			body = append(body, fmap...)
		}
		for i := 0; i < levels; i++ {
			var cmap []byte
			cmap = append(cmap, wire.WireUint, wire.WireSubMsg)
			cmap = wire.AppendVarint(cmap, 1)
			cmap = wire.AppendVarint(cmap, 1)
			cmap = wire.AppendVarint(cmap, uint64(len(body)))
			cmap = append(cmap, body...)
			var next []byte
			next = appendTag(next, 3, wire.WireMap)
			next = wire.AppendVarint(next, uint64(len(cmap)))
			next = append(next, cmap...)
			body = next
		}
		var pkt []byte
		pkt = appendTag(pkt, 0, wire.WireSubMsg)
		pkt = wire.AppendVarint(pkt, uint64(len(body)))
		pkt = append(pkt, body...)
		return pkt
	}

	// 15 children levels + leaf fields map => container depth = 1 + 15*2 + 1 = 32 (exact MaxNestingDepth)
	valid32 := buildNestedChildrenField0(15, true)
	if _, err := ob.DecodeBlind(valid32); err != nil {
		t.Fatalf("expected depth-32 SchemaMeta to succeed in DecodeBlind, got: %v", err)
	}
	var dst TestAllTypes
	if err := ob.Unmarshal(valid32, &dst); err != nil {
		t.Fatalf("expected depth-32 SchemaMeta to succeed in Unmarshal, got: %v", err)
	}

	// 16 children levels => container depth = 1 + 16*2 = 33 > MaxNestingDepth
	invalid33 := buildNestedChildrenField0(16, false)
	if _, err := ob.DecodeBlind(invalid33); err == nil {
		t.Fatal("expected depth-33 SchemaMeta to fail in DecodeBlind")
	}
	if err := ob.Unmarshal(invalid33, &dst); err == nil {
		t.Fatal("expected depth-33 SchemaMeta to fail in Unmarshal")
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
