# OmniBuf for Go (`omnibuf-go`)

[![CI](https://github.com/omnibuf/omnibuf-go/actions/workflows/ci.yml/badge.svg)](https://github.com/omnibuf/omnibuf-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/omnibuf/omnibuf-go.svg)](https://pkg.go.dev/github.com/omnibuf/omnibuf-go)
[![Website](https://img.shields.io/badge/website-omnibuf.com-111111?style=flat&labelColor=111111&color=D4FF33)](https://omnibuf.com/go)

**[Website (omnibuf.com)](https://omnibuf.com)** · **[How It Works](https://omnibuf.com/how-it-works)** · **[Go Documentation](https://omnibuf.com/go)** · **[TypeScript / JS (`omnibuf-es`)](https://github.com/omnibuf/omnibuf-es)** · **[CLI (`omnibuf-cli`)](https://github.com/omnibuf/omnibuf-cli)** · **[Wire Format Specification](https://omnibuf.com/spec)**

Go runtime library (`package omnibuf`) and `protoc-gen-omnibuf` compiler plugin for the **OmniBuf** binary wire format:

1. **Keep `.proto` schemas and Go structs:** Existing `.proto` files, `protoc-gen-go` structs, and `dynamicpb` messages work without changes.
2. **Single-pass encode & zero-loop varints:** Prefix-Varints (`TZCNT`) decode integers in a single load with no byte-by-byte continuation loop, and 2-byte length backpatching encodes messages in a single forward pass (~6x faster encode and ~6x faster zero-copy decode than `google.golang.org/protobuf`).
3. **Inspect packets without a schema (`DecodeBlind`):** Explicit 4-bit wire types (`0..15`) replace Protobuf's overloaded `Wire Type 2`, and optional **Field `#0`** metadata attaches field names on the wire when needed.
4. **Flat `[Tag][Value]` stream:** Containers carry outer byte lengths for single-step skipping (`LocateField`) and in-place field splicing (`SpliceFields`), and concatenating two packets (`append(msgA, msgB...)`) merges them just like Protobuf.

---

## Install

```bash
go get github.com/omnibuf/omnibuf-go
go install github.com/omnibuf/omnibuf-go/cmd/protoc-gen-omnibuf@latest
```

---

## Go Library Usage

OmniBuf works directly with plain Go structs using `omnibuf:"..."` struct tags as well as existing `protoc-gen-go` generated `.pb.go` structs (`proto.Message` / `protoreflect.Message`):

### 1. Plain Go Structs (`omnibuf` Struct Tags)

Tag exported struct fields with a field number (`1..65535`) and optional key-value settings (`name=<name>`, `wire=<type>`, `omitempty`, or `-` to ignore):

```go
package main

import (
	"fmt"

	"github.com/omnibuf/omnibuf-go"
)

type PeerEvent struct {
	ASN       uint32            `omnibuf:"1,name=asn"`
	RouterID  string            `omnibuf:"2,name=router_id"`
	Active    bool              `omnibuf:"3,name=active"`
	LatencyUS uint32            `omnibuf:"4,name=latency_us,wire=fixed32,omitempty"`
	Tags      []string          `omnibuf:"5,name=tags,omitempty"`
	Counters  map[string]uint64 `omnibuf:"6,name=counters,omitempty"`
	Internal  string            `omnibuf:"-"`
}

func main() {
	in := &PeerEvent{
		ASN:       64512,
		RouterID:  "edge-rtr-fra-01",
		Active:    true,
		LatencyUS: 850,
		Tags:      []string{"tier1", "ebgp"},
	}

	wire, err := omnibuf.MarshalOptions{SelfDescribing: true}.Marshal(in)
	if err != nil {
		panic(err)
	}

	var out PeerEvent
	if err := omnibuf.Unmarshal(wire, &out); err != nil {
		panic(err)
	}

	blind, err := omnibuf.DecodeBlind(wire)
	if err != nil {
		panic(err)
	}
	fmt.Println(blind)
}
```

### 2. Existing `.pb.go` Structs (`protoc-gen-go`)

```go
package main

import (
	"fmt"

	"example.com/yourproject/pb"
	"github.com/omnibuf/omnibuf-go"
)

func main() {
	msg := &pb.PeerUpdate{
		Asn:      64512,
		RouterId: "edge-rtr-fra-01",
		Active:   true,
	}

	wire, err := omnibuf.Marshal(msg)
	if err != nil {
		panic(err)
	}

	var out pb.PeerUpdate
	if err := omnibuf.Unmarshal(wire, &out); err != nil {
		panic(err)
	}

	blind, err := omnibuf.DecodeBlind(wire)
	if err != nil {
		panic(err)
	}
	fmt.Println(blind)
}
```

---

## Development

```bash
just fmt-check
just lint
just test
just conformance
just fuzz 10s
```

## License

Released under the [MIT License](./LICENSE).
