package omnibuf_test

import (
	"testing"

	"github.com/omnibuf/omnibuf-go"
)

type coreFuzzNested struct {
	City string `omnibuf:"1,name=city"`
	Rack uint32 `omnibuf:"2,name=rack"`
}

type coreFuzzRecord struct {
	ID       uint64            `omnibuf:"1,name=id"`
	Router   string            `omnibuf:"2,name=router"`
	Active   bool              `omnibuf:"3,name=active"`
	Delta    int32             `omnibuf:"4,name=delta,wire=sint"`
	Load     float64           `omnibuf:"5,name=load"`
	Tags     []string          `omnibuf:"6,name=tags"`
	Counters map[string]uint64 `omnibuf:"7,name=counters"`
	Location *coreFuzzNested   `omnibuf:"8,name=location"`
}

func FuzzCoreDecodeNoPanic(f *testing.F) {
	seed, err := omnibuf.MarshalOptions{SelfDescribing: true, Deterministic: true}.Marshal(&coreFuzzRecord{
		ID:       42,
		Router:   "edge-fra-01",
		Active:   true,
		Delta:    -17,
		Load:     0.42,
		Tags:     []string{"ebgp", "core"},
		Counters: map[string]uint64{"et0": 100, "et1": 200},
		Location: &coreFuzzNested{City: "Frankfurt", Rack: 9},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{})
	f.Add([]byte{0x19, 0x02, 0x00})

	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = omnibuf.DecodeBlind(data)
		_, _ = omnibuf.DecodeBlindJSON(data)
		var out coreFuzzRecord
		_ = omnibuf.Unmarshal(data, &out)
		var outZC coreFuzzRecord
		_ = omnibuf.UnmarshalZeroCopy(data, &outZC)
	})
}
