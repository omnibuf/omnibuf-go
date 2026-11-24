// Package omnibufconnect provides a connectrpc.com/connect.Codec backed by omnibufproto.
package omnibufconnect

import (
	"connectrpc.com/connect"
	"github.com/omnibuf/omnibuf-go/omnibufproto"
)

const (
	// NameCompact is the Connect codec name for compact OmniBuf payloads.
	NameCompact = "omnibuf"

	// NameSelfDescribing is the Connect codec name for self-describing OmniBuf
	// payloads carrying Field #0 (SchemaMeta) inline.
	NameSelfDescribing = "x-omnibuf"

	// ContentType is the Connect unary Content-Type header for compact OmniBuf payloads.
	ContentType = "application/omnibuf"
)

// Codec implements connectrpc.com/connect.Codec using omnibufproto.
type Codec struct {
	// MarshalOptions configures OmniBuf wire encoding.
	MarshalOptions omnibufproto.MarshalOptions

	// UnmarshalOptions configures OmniBuf wire decoding limits and behavior.
	UnmarshalOptions omnibufproto.UnmarshalOptions
}

var _ connect.Codec = (*Codec)(nil)

// New returns a compact OmniBuf Connect codec named "omnibuf".
func New() *Codec {
	return &Codec{}
}

// NewSelfDescribing returns a self-describing OmniBuf Connect codec named "x-omnibuf".
func NewSelfDescribing() *Codec {
	return &Codec{
		MarshalOptions: omnibufproto.MarshalOptions{
			SelfDescribing: true,
		},
	}
}

// HandlerOptions returns Connect handler options registering both compact ("omnibuf")
// and self-describing ("x-omnibuf") OmniBuf codecs.
func HandlerOptions() []connect.HandlerOption {
	return []connect.HandlerOption{
		connect.WithCodec(New()),
		connect.WithCodec(NewSelfDescribing()),
	}
}

// ClientOption returns a Connect client option selecting the compact "omnibuf" codec.
func ClientOption() connect.ClientOption {
	return connect.WithCodec(New())
}

// SelfDescribingClientOption returns a Connect client option selecting the "x-omnibuf" codec.
func SelfDescribingClientOption() connect.ClientOption {
	return connect.WithCodec(NewSelfDescribing())
}

// Name returns the Connect codec name ("omnibuf" or "x-omnibuf").
func (c *Codec) Name() string {
	if c != nil && c.MarshalOptions.SelfDescribing {
		return NameSelfDescribing
	}
	return NameCompact
}

// IsBinary reports that OmniBuf uses a binary wire format.
func (c *Codec) IsBinary() bool {
	return true
}

// Marshal encodes v into OmniBuf wire bytes.
func (c *Codec) Marshal(v any) ([]byte, error) {
	return c.marshalOptions().Marshal(v)
}

// MarshalAppend appends the OmniBuf wire encoding of v to dst.
func (c *Codec) MarshalAppend(dst []byte, v any) ([]byte, error) {
	return c.marshalOptions().MarshalAppend(dst, v)
}

// MarshalStable encodes v deterministically with sorted map keys.
func (c *Codec) MarshalStable(v any) ([]byte, error) {
	opts := c.marshalOptions()
	opts.Deterministic = true
	return opts.Marshal(v)
}

// Unmarshal decodes an OmniBuf payload into v.
func (c *Codec) Unmarshal(data []byte, v any) error {
	return c.unmarshalOptions().Unmarshal(data, v)
}

func (c *Codec) marshalOptions() omnibufproto.MarshalOptions {
	if c == nil {
		return omnibufproto.MarshalOptions{}
	}
	return c.MarshalOptions
}

func (c *Codec) unmarshalOptions() omnibufproto.UnmarshalOptions {
	if c == nil {
		return omnibufproto.UnmarshalOptions{}
	}
	return c.UnmarshalOptions
}
