package omnibuf_test

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	. "github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/internal/pb"
	. "github.com/omnibuf/omnibuf-go/wire"
)

func TestReviewRegression_DecodeBlindJSONNonFiniteAndNegZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  float64
	}{
		{"nan", math.NaN()},
		{"pos_inf", math.Inf(1)},
		{"neg_inf", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(32)
			enc.WriteFloat64(1, tc.val)
			pkt := append([]byte(nil), enc.Finish()...)
			if _, err := DecodeBlindJSON(pkt); !errors.Is(err, ErrNonFiniteFloat) {
				t.Fatalf("DecodeBlindJSON(%s) err = %v, want ErrNonFiniteFloat", tc.name, err)
			}
		})
	}

	enc := NewEncoder(32)
	enc.WriteFloat64(1, math.Float64frombits(0x8000000000000000))
	negZeroJSON, err := DecodeBlindJSON(enc.Finish())
	if err != nil {
		t.Fatalf("DecodeBlindJSON(-0.0) failed: %v", err)
	}
	if !strings.Contains(negZeroJSON, "-0") {
		t.Fatalf("DecodeBlindJSON(-0.0) = %q, expected -0 sign preserved", negZeroJSON)
	}
}

func TestReviewRegression_SelfDescribingRecursiveTreeNode(t *testing.T) {
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

	raw, err := (MarshalOptions{Deterministic: true, SelfDescribing: true}).Marshal(root)
	if err != nil {
		t.Fatalf("Marshal(SelfDescribing: true) recursive TreeNode failed: %v", err)
	}
	blind, err := DecodeBlind(raw)
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

func TestReviewRegression_SchemaMetaChildrenDepthBoundary(t *testing.T) {
	buildNestedChildrenField0 := func(levels int, withLeafFields bool) []byte {
		var body []byte
		if withLeafFields {
			var fmap []byte
			fmap = append(fmap, WireUint, WireString)
			fmap = AppendVarint(fmap, 1)
			fmap = AppendVarint(fmap, 1)
			fmap = AppendVarint(fmap, 2)
			fmap = append(fmap, "id"...)
			body = appendTag(body, 2, WireMap)
			body = AppendVarint(body, uint64(len(fmap)))
			body = append(body, fmap...)
		}
		for i := 0; i < levels; i++ {
			var cmap []byte
			cmap = append(cmap, WireUint, WireSubMsg)
			cmap = AppendVarint(cmap, 1)
			cmap = AppendVarint(cmap, 1)
			cmap = AppendVarint(cmap, uint64(len(body)))
			cmap = append(cmap, body...)
			var next []byte
			next = appendTag(next, 3, WireMap)
			next = AppendVarint(next, uint64(len(cmap)))
			next = append(next, cmap...)
			body = next
		}
		var pkt []byte
		pkt = appendTag(pkt, 0, WireSubMsg)
		pkt = AppendVarint(pkt, uint64(len(body)))
		pkt = append(pkt, body...)
		return pkt
	}

	// 15 children levels + leaf fields map => container depth = 1 + 15*2 + 1 = 32 (exact MaxNestingDepth)
	valid32 := buildNestedChildrenField0(15, true)
	if _, err := DecodeBlind(valid32); err != nil {
		t.Fatalf("expected depth-32 SchemaMeta to succeed in DecodeBlind, got: %v", err)
	}
	var dst TestAllTypes
	if err := Unmarshal(valid32, &dst); err != nil {
		t.Fatalf("expected depth-32 SchemaMeta to succeed in Unmarshal, got: %v", err)
	}

	// 16 children levels => container depth = 1 + 16*2 = 33 > MaxNestingDepth
	invalid33 := buildNestedChildrenField0(16, false)
	if _, err := DecodeBlind(invalid33); err == nil {
		t.Fatal("expected depth-33 SchemaMeta to fail in DecodeBlind")
	}
	if err := Unmarshal(invalid33, &dst); err == nil {
		t.Fatal("expected depth-33 SchemaMeta to fail in Unmarshal")
	}
}

func TestReviewRegression_OpaqueMarshalRejectsCorruptPacket(t *testing.T) {
	// Valid field #1 followed by truncated 2B varint at end of packet
	var corrupt []byte
	corrupt = appendTag(corrupt, 1, WireUint)
	corrupt = AppendVarint(corrupt, 10)
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

func TestStressBenchmarkWorkloadsRoundTrip(t *testing.T) {
	large := SampleLargeNestedPeerUpdate()
	pbLarge := SamplePBPeerUpdate(&large)

	obGen := pbLarge.MarshalOmniBufAppend(nil)
	obTag, err := Marshal(&large)
	if err != nil {
		t.Fatalf("Marshal(large): %v", err)
	}
	if len(obGen) != len(obTag) {
		t.Fatalf("len(obGen)=%d != len(obTag)=%d", len(obGen), len(obTag))
	}
	var decLarge PeerUpdate
	if err := Unmarshal(obGen, &decLarge); err != nil || !reflect.DeepEqual(decLarge, large) {
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
	if err := Unmarshal(spliced, &decGrown); err != nil {
		t.Fatalf("Unmarshal(spliced): %v", err)
	}
	wantGrown := many
	wantGrown.Location.Datacenter = grownDC
	if !reflect.DeepEqual(decGrown, wantGrown) {
		t.Fatalf("spliced PeerUpdate mismatch after growth")
	}
}
