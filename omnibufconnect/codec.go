// Package omnibufconnect provides a connectrpc.com/connect/v2 Codec and StableCodec backed by omnibufproto.
package omnibufconnect

import (
	"context"
	"io"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
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

// Codec implements connectrpc.com/connect/v2.Codec and StableCodec using omnibufproto.
type Codec struct {
	// MarshalOptions configures OmniBuf wire encoding.
	MarshalOptions omnibufproto.MarshalOptions

	// UnmarshalOptions configures OmniBuf wire decoding limits and behavior.
	UnmarshalOptions omnibufproto.UnmarshalOptions
}

var (
	_ connect.Codec       = (*Codec)(nil)
	_ connect.StableCodec = (*Codec)(nil)
)

// New returns a compact OmniBuf Connect v2 codec named "omnibuf".
func New() *Codec {
	return &Codec{}
}

// NewSelfDescribing returns a self-describing OmniBuf Connect v2 codec named "x-omnibuf".
func NewSelfDescribing() *Codec {
	return &Codec{
		MarshalOptions: omnibufproto.MarshalOptions{
			SelfDescribing: true,
		},
	}
}

// ServerOptions returns connecthttp options registering both compact ("omnibuf")
// and self-describing ("x-omnibuf") OmniBuf codecs on a Connect v2 server.
func ServerOptions() []connecthttp.Option {
	return []connecthttp.Option{
		connecthttp.WithCodecs(New(), NewSelfDescribing()),
	}
}

// ClientOptions returns connecthttp transport options registering both OmniBuf
// codecs and selecting compact ("omnibuf") for outbound requests.
func ClientOptions() []connecthttp.Option {
	return []connecthttp.Option{
		connecthttp.WithCodecs(New(), NewSelfDescribing()),
		connecthttp.WithSendCodec(NameCompact),
	}
}

// SelfDescribingClientOptions returns connecthttp transport options registering
// both OmniBuf codecs and selecting self-describing ("x-omnibuf") for outbound requests.
func SelfDescribingClientOptions() []connecthttp.Option {
	return []connecthttp.Option{
		connecthttp.WithCodecs(New(), NewSelfDescribing()),
		connecthttp.WithSendCodec(NameSelfDescribing),
	}
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

// MarshalWrite encodes msg in OmniBuf wire format and writes it to dst.
func (c *Codec) MarshalWrite(_ context.Context, dst io.Writer, msg any) error {
	opts := omnibufproto.MarshalOptions{}
	if c != nil {
		opts = c.MarshalOptions
	}
	return marshalWrite(dst, opts, msg)
}

// MarshalWriteStable encodes msg deterministically in OmniBuf wire format and writes it to dst.
func (c *Codec) MarshalWriteStable(_ context.Context, dst io.Writer, msg any) error {
	opts := omnibufproto.MarshalOptions{}
	if c != nil {
		opts = c.MarshalOptions
	}
	opts.Deterministic = true
	return marshalWrite(dst, opts, msg)
}

type availableBufferWriter interface {
	AvailableBuffer() []byte
}

func marshalWrite(dst io.Writer, opts omnibufproto.MarshalOptions, msg any) error {
	var scratch []byte
	if abw, ok := dst.(availableBufferWriter); ok {
		scratch = abw.AvailableBuffer()
	}
	raw, err := opts.MarshalAppend(scratch, msg)
	if err != nil {
		return err
	}
	_, err = dst.Write(raw)
	return err
}

// UnmarshalRead reads a bounded OmniBuf message from src and decodes it into msg.
func (c *Codec) UnmarshalRead(_ context.Context, src io.Reader, msg any) error {
	raw, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	opts := omnibufproto.UnmarshalOptions{}
	if c != nil {
		opts = c.UnmarshalOptions
	}
	return opts.Unmarshal(raw, msg)
}
