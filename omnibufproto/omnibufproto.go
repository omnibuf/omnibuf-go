// Package omnibufproto provides Protocol Buffers (google.golang.org/protobuf)
// integration for OmniBuf, keeping the core github.com/omnibuf/omnibuf-go
// module completely dependency-free.
//
// Importing omnibufproto (either directly or via a blank import) also registers
// proto.Message and protoreflect.Message handling into omnibuf.Marshal and
// omnibuf.Unmarshal for seamless dispatch on dynamicpb, oneofs, enums, proto2
// extensions/required fields, and Go Opaque API messages.
package omnibufproto

import (
	"reflect"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/internal/protohook"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func init() {
	protohook.MarshalHook = tryMarshalHook
	protohook.UnmarshalHook = tryUnmarshalHook
}

func tryMarshalHook(dst []byte, v any, deterministic, selfDescribing bool) ([]byte, bool, error) {
	opts := MarshalOptions{
		Deterministic:  deterministic,
		SelfDescribing: selfDescribing,
	}
	if pm, ok := v.(proto.Message); ok && (reflect.ValueOf(pm).IsNil() || needsProtoReflection(pm)) {
		out, err := opts.marshalProtoReflectAppend(dst, pm.ProtoReflect())
		return out, true, err
	}
	if pr, ok := v.(protoreflect.Message); ok {
		out, err := opts.marshalProtoReflectAppend(dst, pr)
		return out, true, err
	}
	return dst, false, nil
}

func tryUnmarshalHook(packet []byte, v any) (bool, error) {
	if pm, ok := v.(proto.Message); ok && (reflect.ValueOf(pm).IsNil() || needsProtoReflection(pm)) {
		return true, unmarshalProtoReflect(packet, pm.ProtoReflect())
	}
	if pr, ok := v.(protoreflect.Message); ok {
		return true, unmarshalProtoReflect(packet, pr)
	}
	return false, nil
}

type generatedMarshaler interface {
	MarshalOmniBufAppend(dst []byte) []byte
}

type generatedUnmarshaler interface {
	UnmarshalOmniBuf(data []byte, zeroCopy bool) error
}

// MarshalOptions configures OmniBuf wire encoding for Protocol Buffers messages.
type MarshalOptions struct {
	Deterministic  bool
	SelfDescribing bool
}

// Marshal encodes a proto.Message, protoreflect.Message, or struct pointer into OmniBuf wire format.
func (o MarshalOptions) Marshal(v any) ([]byte, error) {
	return o.MarshalAppend(nil, v)
}

// MarshalAppend appends the OmniBuf wire encoding of v to dst.
func (o MarshalOptions) MarshalAppend(dst []byte, v any) ([]byte, error) {
	if !o.SelfDescribing && !o.Deterministic {
		if fast, ok := v.(generatedMarshaler); ok && v != nil && !reflect.ValueOf(v).IsNil() {
			return fast.MarshalOmniBufAppend(dst), nil
		}
	}
	if pr, ok := v.(protoreflect.Message); ok {
		return o.marshalProtoReflectAppend(dst, pr)
	}
	if pm, ok := v.(proto.Message); ok && (reflect.ValueOf(pm).IsNil() || needsProtoReflection(pm)) {
		return o.marshalProtoReflectAppend(dst, pm.ProtoReflect())
	}
	return omnibuf.MarshalOptions{
		Deterministic:  o.Deterministic,
		SelfDescribing: o.SelfDescribing,
	}.MarshalAppend(dst, v)
}

// Marshal encodes v into OmniBuf wire format using the fastest available path
// (protoc-gen-omnibuf generated methods, table-driven open struct codec, or protoreflect).
func Marshal(v any) ([]byte, error) {
	return MarshalOptions{}.MarshalAppend(nil, v)
}

// MarshalAppend appends the OmniBuf wire encoding of v to dst.
func MarshalAppend(dst []byte, v any) ([]byte, error) {
	return MarshalOptions{}.MarshalAppend(dst, v)
}

// MarshalDeterministic encodes v with deterministic wireMap key ordering.
func MarshalDeterministic(v any) ([]byte, error) {
	return MarshalOptions{Deterministic: true}.Marshal(v)
}

// Unmarshal decodes an OmniBuf packet into v (any proto.Message, protoreflect.Message, or struct pointer).
func Unmarshal(packet []byte, v any) error {
	if fast, ok := v.(generatedUnmarshaler); ok && v != nil && !reflect.ValueOf(v).IsNil() {
		return fast.UnmarshalOmniBuf(packet, false)
	}
	if pr, ok := v.(protoreflect.Message); ok {
		return unmarshalProtoReflect(packet, pr)
	}
	if pm, ok := v.(proto.Message); ok && (reflect.ValueOf(pm).IsNil() || needsProtoReflection(pm)) {
		return unmarshalProtoReflect(packet, pm.ProtoReflect())
	}
	return omnibuf.Unmarshal(packet, v)
}

// UnmarshalZeroCopy decodes an OmniBuf packet into v using zero-copy string views when supported.
func UnmarshalZeroCopy(packet []byte, v any) error {
	if fast, ok := v.(generatedUnmarshaler); ok && v != nil && !reflect.ValueOf(v).IsNil() {
		return fast.UnmarshalOmniBuf(packet, true)
	}
	if pr, ok := v.(protoreflect.Message); ok {
		return unmarshalProtoReflect(packet, pr)
	}
	if pm, ok := v.(proto.Message); ok && (reflect.ValueOf(pm).IsNil() || needsProtoReflection(pm)) {
		return unmarshalProtoReflect(packet, pm.ProtoReflect())
	}
	return omnibuf.UnmarshalZeroCopy(packet, v)
}
