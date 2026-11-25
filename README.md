# omnibuf-go

[![OmniBuf Spec: v1 DRAFT-1](https://img.shields.io/badge/OmniBuf%20Spec-v1%20DRAFT--1-FF4F18)](https://omnibuf.com/spec)

Reference Go implementation of [OmniBuf Wire Specification v1 DRAFT-1](https://omnibuf.com/spec), including the `omnibuf` runtime, `omnibufproto` bridge, `protoc-gen-omnibuf-go` plugin, and the cross-language conformance runner. Full docs live at [omnibuf.com/go](https://omnibuf.com/go).

## Install

```bash
go get github.com/omnibuf/omnibuf-go
go get github.com/omnibuf/omnibuf-go/omnibufproto
go install github.com/omnibuf/omnibuf-go/cmd/protoc-gen-omnibuf-go@latest
```

## Quick usage

```go
package main

import (
	"fmt"

	"github.com/omnibuf/omnibuf-go"
)

type PeerEvent struct {
	ASN      uint32 `omnibuf:"1,name=asn"`
	RouterID string `omnibuf:"2,name=router_id"`
	Active   bool   `omnibuf:"3,name=active"`
}

func main() {
	in := &PeerEvent{ASN: 64512, RouterID: "edge-rtr-fra-01", Active: true}
	wire, err := omnibuf.MarshalOptions{SelfDescribing: true}.Marshal(in)
	if err != nil {
		panic(err)
	}

	var out PeerEvent
	if err := omnibuf.Unmarshal(wire, &out); err != nil {
		panic(err)
	}

	blind, _ := omnibuf.DecodeBlind(wire)
	fmt.Println(blind)
}
```

## gRPC & Connect RPC

Two separate Go submodules provide framework codecs without adding RPC dependencies to core `omnibuf-go`:

- **gRPC (`github.com/omnibuf/omnibuf-go/omnibufgrpc`):** Implements `google.golang.org/grpc/encoding.CodecV2` (`"omnibuf"` and `"x-omnibuf"`).
- **Connect (`github.com/omnibuf/omnibuf-go/omnibufconnect`):** Implements `connectrpc.com/connect/v2.Codec` and `StableCodec` (`"omnibuf"` and `"x-omnibuf"`).

```go
// gRPC
import (
	"github.com/omnibuf/omnibuf-go/omnibufgrpc"
	"google.golang.org/grpc"
)

omnibufgrpc.Register()
conn, err := grpc.NewClient(addr, grpc.WithDefaultCallOptions(omnibufgrpc.CallOption()))

// Connect v2
import (
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/omnibuf/omnibuf-go/omnibufconnect"
)

connecthttp.Mount(mux, server, omnibufconnect.ServerOptions()...)
transport := connecthttp.NewTransport(httpClient, baseURL, omnibufconnect.ClientOptions()...)
client := connect.NewClient(transport)
```

## Implementation

- **Core module (`github.com/omnibuf/omnibuf-go`):** Pure Go standard library with zero external dependencies. Supports `omnibuf:"..."` struct tags (`name`, `wire`, `omitempty`, `-`), `DecodeBlind`, `LocateField`, and `SpliceFields`.
- **Protobuf bridge (`github.com/omnibuf/omnibuf-go/omnibufproto`):** Separate submodule for `proto.Message`, `protoreflect.Message`, `dynamicpb.Message`, `oneof`, `enum`, and Go Opaque API types.
- **Compiler plugin (`protoc-gen-omnibuf-go`):** Generates static OmniBuf codecs from `.proto` files. Available via `go install` or `brew install omnibuf/tap/protoc-gen-omnibuf-go`.
- **Conformance suite host (`cmd/conformance-runner`):** Runs the canonical 758-case cross-language conformance corpus (`internal/conformance/conformance_corpus.json`). See [CONFORMANCE.md](./CONFORMANCE.md).
- **Buffer ownership:** `Unmarshal` copies strings and bytes. `UnmarshalZeroCopy` and `Read*ZeroCopy` borrow the packet buffer, so keep the backing slice live and unmodified while decoded values are in use.

## License

Apache-2.0
