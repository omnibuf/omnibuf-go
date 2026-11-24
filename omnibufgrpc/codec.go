// Package omnibufgrpc provides a gRPC encoding.CodecV2 backed by omnibufproto.
package omnibufgrpc

import (
	"github.com/omnibuf/omnibuf-go/omnibufproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/mem"
)

const (
	// NameCompact is the gRPC codec subtype for compact OmniBuf payloads.
	NameCompact = "omnibuf"

	// NameSelfDescribing is the gRPC codec subtype for self-describing OmniBuf
	// payloads carrying Field #0 (SchemaMeta) inline.
	NameSelfDescribing = "x-omnibuf"

	// ContentType is the gRPC Content-Type header for compact OmniBuf payloads.
	ContentType = "application/grpc+omnibuf"
)

// Codec implements google.golang.org/grpc/encoding.CodecV2 using omnibufproto.
type Codec struct {
	// MarshalOptions configures OmniBuf wire encoding.
	MarshalOptions omnibufproto.MarshalOptions

	// UnmarshalOptions configures OmniBuf wire decoding limits and behavior.
	UnmarshalOptions omnibufproto.UnmarshalOptions
}

var _ encoding.CodecV2 = (*Codec)(nil)

// New returns a compact OmniBuf gRPC codec named "omnibuf".
func New() *Codec {
	return &Codec{}
}

// NewSelfDescribing returns a self-describing OmniBuf gRPC codec named "x-omnibuf".
func NewSelfDescribing() *Codec {
	return &Codec{
		MarshalOptions: omnibufproto.MarshalOptions{
			SelfDescribing: true,
		},
	}
}

// Register registers both the compact ("omnibuf") and self-describing ("x-omnibuf")
// OmniBuf codecs with google.golang.org/grpc/encoding.
func Register() {
	encoding.RegisterCodecV2(New())
	encoding.RegisterCodecV2(NewSelfDescribing())
}

// CallOption returns a gRPC CallOption that forces the compact "omnibuf" codec.
func CallOption() grpc.CallOption {
	return grpc.ForceCodecV2(New())
}

// SelfDescribingCallOption returns a gRPC CallOption that forces the "x-omnibuf" codec.
func SelfDescribingCallOption() grpc.CallOption {
	return grpc.ForceCodecV2(NewSelfDescribing())
}

// Name returns the gRPC codec subtype ("omnibuf" or "x-omnibuf").
func (c *Codec) Name() string {
	if c != nil && c.MarshalOptions.SelfDescribing {
		return NameSelfDescribing
	}
	return NameCompact
}

// Marshal encodes v into a gRPC mem.BufferSlice in OmniBuf wire format.
func (c *Codec) Marshal(v any) (mem.BufferSlice, error) {
	opts := omnibufproto.MarshalOptions{}
	if c != nil {
		opts = c.MarshalOptions
	}
	wire, err := opts.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(wire) == 0 {
		return nil, nil
	}
	return mem.BufferSlice{mem.SliceBuffer(wire)}, nil
}

// Unmarshal decodes an OmniBuf gRPC mem.BufferSlice into v.
func (c *Codec) Unmarshal(data mem.BufferSlice, v any) error {
	opts := omnibufproto.UnmarshalOptions{}
	if c != nil {
		opts = c.UnmarshalOptions
	}
	raw := data.Materialize()
	return opts.Unmarshal(raw, v)
}
