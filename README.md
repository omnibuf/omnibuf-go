# OmniBuf for Go (`omnibuf-go`)

[![CI](https://github.com/omnibuf/omnibuf-go/actions/workflows/ci.yml/badge.svg)](https://github.com/omnibuf/omnibuf-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/omnibuf/omnibuf-go.svg)](https://pkg.go.dev/github.com/omnibuf/omnibuf-go)
[![Wire Spec: v0.1](https://img.shields.io/badge/spec-v0.1-111111?style=flat&labelColor=111111&color=FF4F18)](https://omnibuf.com/spec)
[![Website](https://img.shields.io/badge/website-omnibuf.com-111111?style=flat&labelColor=111111&color=D4FF33)](https://omnibuf.com/go)

**[Website (omnibuf.com)](https://omnibuf.com)** · **[How It Works](https://omnibuf.com/how-it-works)** · **[Go Documentation](https://omnibuf.com/go)** · **[TypeScript / JS (`omnibuf-es`)](https://github.com/omnibuf/omnibuf-es)** · **[CLI (`omnibuf-cli`)](https://github.com/omnibuf/omnibuf-cli)** · **[Wire Format Specification](https://omnibuf.com/spec)**

Go runtime library (`package omnibuf`), optional Protobuf bridge (`package omnibufproto`), and `protoc-gen-omnibuf` compiler plugin implementing **[OmniBuf Wire Specification v0.1](https://omnibuf.com/spec)**:

1. **Zero-dependency core module (`github.com/omnibuf/omnibuf-go`):** Pure Go standard library with zero external dependencies for `omnibuf:"..."` struct tags, `DecodeBlind`, `wire`, and open `.pb.go` struct tags.
2. **Optional Protobuf module (`github.com/omnibuf/omnibuf-go/omnibufproto`):** Separate submodule adding `protoreflect.Message`, `dynamicpb.Message`, `oneof`, `enum`, and Go Opaque API support only in projects that use `google.golang.org/protobuf`.
3. **Single-pass encode & zero-loop varints:** Prefix-Varints (`TZCNT`) decode integers in a single load with no byte-by-byte continuation loop, and 2-byte length backpatching encodes messages in a single forward pass (~6x faster encode and ~6x faster zero-copy decode than `google.golang.org/protobuf`).
4. **Inspect packets without a schema (`DecodeBlind`):** Explicit 4-bit wire types (`0..15`) replace Protobuf's overloaded `Wire Type 2`, and optional **Field `#0`** metadata attaches field names on the wire when needed.
5. **Flat `[Tag][Value]` stream:** Containers carry outer byte lengths for single-step skipping (`LocateField`) and in-place field splicing (`SpliceFields`), and concatenating two packets (`append(msgA, msgB...)`) merges them just like Protobuf.

---

## Install

```bash
# Zero-dependency core runtime (plain Go structs + DecodeBlind + wire)
go get github.com/omnibuf/omnibuf-go

# Optional Protocol Buffers bridge (proto.Message, dynamicpb, oneofs, enums)
go get github.com/omnibuf/omnibuf-go/omnibufproto

# Install the protoc-gen-omnibuf compiler plugin via Go or Homebrew:
go install github.com/omnibuf/omnibuf-go/cmd/protoc-gen-omnibuf@latest
# or: brew install omnibuf/tap/protoc-gen-omnibuf
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

### 2. Existing `.pb.go` Structs & `dynamicpb` (`omnibufproto`)

```go
package main

import (
	"fmt"

	"example.com/yourproject/pb"
	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/omnibufproto"
)

func main() {
	msg := &pb.PeerUpdate{
		Asn:      64512,
		RouterId: "edge-rtr-fra-01",
		Active:   true,
	}

	wire, err := omnibufproto.Marshal(msg)
	if err != nil {
		panic(err)
	}

	var out pb.PeerUpdate
	if err := omnibufproto.Unmarshal(wire, &out); err != nil {
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

## Cross-Language Conformance Runner

`omnibuf-go` hosts the canonical 758-case cross-language conformance runner (`cmd/conformance-runner`) with the embedded test corpus (`internal/conformance/conformance_corpus.json`, containing 58 valid vectors and 90 invalid wire cases). Adapter framing, tier requirements, and splice mutation rules are documented in [`CONFORMANCE.md`](CONFORMANCE.md). Prebuilt `conformance-runner` binaries are attached to [GitHub Releases](https://github.com/omnibuf/omnibuf-go/releases), or install via Go:

```bash
go install github.com/omnibuf/omnibuf-go/cmd/conformance-runner@latest

# Run all 4 tiers against a language adapter binary:
conformance-runner ./bin/conformance-go
conformance-runner node ../omnibuf-es/packages/omnibuf-node/dist/bin/conformance-es.js

# Filter to specific tiers during incremental bring-up (blind, struct, proto, plugin):
conformance-runner --tier=blind,struct ./bin/conformance-rs
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

Released under the [Apache 2.0 License](./LICENSE).

## Buffer ownership

`Unmarshal` copies decoded strings and bytes. `UnmarshalZeroCopy` and the
`Read*ZeroCopy` helpers borrow packet storage: do not modify the packet or return
it to a buffer pool until every decoded string, map key, and byte slice is no
longer used. Copying a Go string variable does not copy its backing storage.

The table codec uses reflected field offsets and unsafe typed accesses. Its CI
runs race detection; changes to pointer handling should also be checked with
`go test -gcflags=all=-d=checkptr=2 ./...`.
