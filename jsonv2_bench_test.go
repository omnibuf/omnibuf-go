//go:build go1.27 && goexperiment.jsonv2

package omnibuf_test

import (
	jsonv2 "encoding/json/v2"
	"strings"
	"testing"
)

func BenchmarkMarshal_JSONv2(b *testing.B) {
	orig := SamplePeerUpdate()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt, err := jsonv2.Marshal(&orig)
		if err != nil || len(pkt) == 0 {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshal_JSONv2_SafeAlloc(b *testing.B) {
	orig := SamplePeerUpdate()
	pkt, err := jsonv2.Marshal(&orig)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out PeerUpdate
		if err := jsonv2.Unmarshal(pkt, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshal_JSONv2_Reuse(b *testing.B) {
	orig := SamplePeerUpdate()
	pkt, err := jsonv2.Marshal(&orig)
	if err != nil {
		b.Fatal(err)
	}
	var out PeerUpdate
	out.Tags = make([]string, 0, 8)
	out.Prefixes = make([]uint32, 0, 8)
	out.Neighbors = make([]Neighbor, 0, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out.Tags = out.Tags[:0]
		out.Prefixes = out.Prefixes[:0]
		out.Neighbors = out.Neighbors[:0]
		if err := jsonv2.Unmarshal(pkt, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLargeNested_JSONv2(b *testing.B) {
	orig := SampleLargeNestedPeerUpdate()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt, err := jsonv2.Marshal(&orig)
		if err != nil || len(pkt) <= 38912 {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONv2_ReadModifyWrite(b *testing.B) {
	orig := SamplePeerUpdate()
	pkt, err := jsonv2.Marshal(&orig)
	if err != nil {
		b.Fatal(err)
	}
	msg := SamplePeerUpdate()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.Tags = msg.Tags[:0]
		msg.Prefixes = msg.Prefixes[:0]
		msg.Neighbors = msg.Neighbors[:0]
		if err := jsonv2.Unmarshal(pkt, &msg); err != nil {
			b.Fatal(err)
		}
		if msg.Location.City == "Frankfurt" {
			msg.Sequence = msg.Sequence + 1
		}
		out, err := jsonv2.Marshal(&msg)
		if err != nil || len(out) == 0 {
			b.Fatal(err)
		}
	}
}

func BenchmarkEmbeddedSubMsgGrow_JSONv2(b *testing.B) {
	orig := SampleManyNeighborsPeerUpdate()
	pkt, err := jsonv2.Marshal(&orig)
	if err != nil {
		b.Fatal(err)
	}
	grownDC := strings.Repeat("d", 20480)
	msg := SampleManyNeighborsPeerUpdate()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.Tags = msg.Tags[:0]
		msg.Prefixes = msg.Prefixes[:0]
		msg.Neighbors = msg.Neighbors[:0]
		if err := jsonv2.Unmarshal(pkt, &msg); err != nil {
			b.Fatal(err)
		}
		if msg.Location.City == "Frankfurt" {
			msg.Location.Datacenter = grownDC
		}
		out, err := jsonv2.Marshal(&msg)
		if err != nil || len(out) <= 20480 {
			b.Fatal(err)
		}
	}
}
