package omnibuf

import (
	"strings"
	"testing"
)

type cyclicStruct struct {
	ID       uint32                   `omnibuf:"1"`
	Next     *cyclicStruct            `omnibuf:"2"`
	Other    *cyclicStruct            `omnibuf:"3"`
	Children []*cyclicStruct          `omnibuf:"4"`
	ByKey    map[string]*cyclicStruct `omnibuf:"5"`
}

func TestMarshalCyclicStructsReturnsError(t *testing.T) {
	t.Run("singular_pointer_cycle", func(t *testing.T) {
		n := &cyclicStruct{ID: 1}
		n.Next = n
		if _, err := Marshal(n); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("Marshal(singular cycle) = %v, want MaxNestingDepth error", err)
		}
		if _, err := (MarshalOptions{SelfDescribing: true}).Marshal(n); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("MarshalOptions{SelfDescribing: true}.Marshal(singular cycle) = %v, want MaxNestingDepth error", err)
		}
	})

	t.Run("branching_cycle", func(t *testing.T) {
		n := &cyclicStruct{ID: 2}
		n.Next = n
		n.Other = n
		if _, err := Marshal(n); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("Marshal(branching cycle) = %v, want MaxNestingDepth error", err)
		}
	})

	t.Run("slice_pointer_cycle", func(t *testing.T) {
		n := &cyclicStruct{ID: 3}
		n.Children = []*cyclicStruct{n}
		if _, err := Marshal(n); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("Marshal(slice cycle) = %v, want MaxNestingDepth error", err)
		}
	})

	t.Run("map_value_cycle", func(t *testing.T) {
		n := &cyclicStruct{ID: 4}
		n.ByKey = map[string]*cyclicStruct{"self": n}
		if _, err := Marshal(n); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("Marshal(map cycle) = %v, want MaxNestingDepth error", err)
		}
	})
}
