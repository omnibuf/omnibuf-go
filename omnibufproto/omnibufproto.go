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
	"fmt"
	"reflect"
	"sync"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/internal/protohook"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	protoMessageType     = reflect.TypeFor[proto.Message]()
	generatedMarshalType = reflect.TypeFor[generatedMarshaler]()
	generatedUnmarshType = reflect.TypeFor[generatedUnmarshaler]()
	directGenTypes       sync.Map // map[reflect.Type]bool
	embeddedProtoTypes   sync.Map // map[reflect.Type]bool
)

func init() {
	protohook.MarshalHook = tryMarshalHook
	protohook.UnmarshalHook = tryUnmarshalHook
}

func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

func isDirectNonNilGeneratedPtr(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return false
	}
	t := rv.Type()
	if cached, ok := directGenTypes.Load(t); ok {
		return cached.(bool)
	}
	elem := t.Elem()
	direct := elem.Kind() == reflect.Struct
	if direct {
		for i := 0; i < elem.NumField(); i++ {
			sf := elem.Field(i)
			if sf.Anonymous && (sf.Type.Implements(protoMessageType) || sf.Type.Implements(generatedMarshalType) || sf.Type.Implements(generatedUnmarshType)) {
				direct = false
				break
			}
		}
	}
	directGenTypes.Store(t, direct)
	return direct
}

func typeHasEmbeddedProto(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	if cached, ok := embeddedProtoTypes.Load(t); ok {
		return cached.(bool)
	}
	has := false
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.Anonymous && sf.Type.Implements(protoMessageType) {
			has = true
			break
		}
	}
	embeddedProtoTypes.Store(t, has)
	return has
}

func hasNilEmbeddedProto(rv reflect.Value) bool {
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return true
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return false
	}
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.Anonymous || !sf.Type.Implements(protoMessageType) {
			continue
		}
		fv := rv.Field(i)
		switch fv.Kind() {
		case reflect.Pointer, reflect.Interface:
			if fv.IsNil() {
				return true
			}
		case reflect.Struct:
			if hasNilEmbeddedProto(fv) {
				return true
			}
		}
	}
	return false
}

func isNilOrInvalidProto(pm proto.Message) bool {
	if isNilValue(pm) {
		return true
	}
	if !typeHasEmbeddedProto(reflect.TypeOf(pm)) {
		return false
	}
	return hasNilEmbeddedProto(reflect.ValueOf(pm))
}

func tryMarshalHook(dst []byte, v any, deterministic, selfDescribing bool) ([]byte, bool, error) {
	opts := MarshalOptions{
		Deterministic:  deterministic,
		SelfDescribing: selfDescribing,
	}
	if pm, ok := v.(proto.Message); ok {
		if isNilOrInvalidProto(pm) {
			return nil, true, fmt.Errorf("omnibuf: Marshal requires non-nil proto.Message")
		}
		if needsProtoReflection(pm) {
			out, err := opts.marshalProtoReflectAppend(dst, pm.ProtoReflect())
			return out, true, err
		}
	}
	if pr, ok := v.(protoreflect.Message); ok {
		if isNilValue(pr) {
			return nil, true, fmt.Errorf("omnibuf: Marshal requires non-nil protoreflect.Message")
		}
		out, err := opts.marshalProtoReflectAppend(dst, pr)
		return out, true, err
	}
	return dst, false, nil
}

func tryUnmarshalHook(packet []byte, v any, maxBytes, maxDepth int, merge, _ bool) (bool, error) {
	if pm, ok := v.(proto.Message); ok {
		if isNilOrInvalidProto(pm) {
			return true, fmt.Errorf("omnibuf: Unmarshal requires non-nil proto.Message")
		}
		if needsProtoReflection(pm) {
			return true, unmarshalProtoReflectWithOptions(packet, pm.ProtoReflect(), maxBytes, maxDepth, merge)
		}
	}
	if pr, ok := v.(protoreflect.Message); ok {
		if isNilValue(pr) {
			return true, fmt.Errorf("omnibuf: Unmarshal requires non-nil protoreflect.Message")
		}
		return true, unmarshalProtoReflectWithOptions(packet, pr, maxBytes, maxDepth, merge)
	}
	return false, nil
}

type generatedMarshaler interface {
	MarshalOmniBufAppend(dst []byte) []byte
}

type tryGeneratedMarshaler interface {
	TryMarshalOmniBufAppend(dst []byte) ([]byte, error)
}

type trySelfDescribingGeneratedMarshaler interface {
	TryMarshalOmniBufSelfDescribingAppend(dst []byte) ([]byte, error)
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
	if !o.SelfDescribing {
		if fast, ok := v.(tryGeneratedMarshaler); ok && isDirectNonNilGeneratedPtr(v) {
			return fast.TryMarshalOmniBufAppend(dst)
		}
	} else {
		if fast, ok := v.(trySelfDescribingGeneratedMarshaler); ok && isDirectNonNilGeneratedPtr(v) {
			return fast.TryMarshalOmniBufSelfDescribingAppend(dst)
		}
	}
	if pr, ok := v.(protoreflect.Message); ok {
		if isNilValue(pr) {
			return nil, fmt.Errorf("omnibuf: Marshal requires non-nil protoreflect.Message")
		}
		return o.marshalProtoReflectAppend(dst, pr)
	}
	if pm, ok := v.(proto.Message); ok {
		if isNilOrInvalidProto(pm) {
			return nil, fmt.Errorf("omnibuf: Marshal requires non-nil proto.Message")
		}
		if needsProtoReflection(pm) {
			return o.marshalProtoReflectAppend(dst, pm.ProtoReflect())
		}
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

// UnmarshalOptions configures OmniBuf wire decoding for Protocol Buffers messages.
type UnmarshalOptions struct {
	// MaxBytes rejects packets larger than MaxBytes bytes with omnibuf.ErrMessageTooLarge.
	// Zero disables the size cap.
	MaxBytes int

	// MaxDepth overrides the maximum allowed sub-message / container nesting depth.
	// Zero defaults to omnibuf.MaxNestingDepth (32).
	MaxDepth int

	// Merge preserves existing fields in the destination message when absent from
	// the packet, merging sub-messages, appending lists, and merging maps (Spec §2).
	Merge bool

	// ZeroCopy uses zero-copy string views when supported.
	ZeroCopy bool
}

// Unmarshal decodes an OmniBuf packet into v using the configured options.
func (o UnmarshalOptions) Unmarshal(packet []byte, v any) error {
	if o.MaxBytes > 0 && len(packet) > o.MaxBytes {
		return fmt.Errorf("%w: %d > %d", omnibuf.ErrMessageTooLarge, len(packet), o.MaxBytes)
	}
	useDefaultDepth := o.MaxDepth <= 0 || o.MaxDepth == omnibuf.MaxNestingDepth
	if !o.Merge && useDefaultDepth {
		if fast, ok := v.(generatedUnmarshaler); ok && isDirectNonNilGeneratedPtr(v) {
			return fast.UnmarshalOmniBuf(packet, o.ZeroCopy)
		}
	}
	if pr, ok := v.(protoreflect.Message); ok {
		if isNilValue(pr) {
			return fmt.Errorf("omnibuf: Unmarshal requires non-nil protoreflect.Message")
		}
		return unmarshalProtoReflectWithOptions(packet, pr, o.MaxBytes, o.MaxDepth, o.Merge)
	}
	if pm, ok := v.(proto.Message); ok {
		if isNilOrInvalidProto(pm) {
			return fmt.Errorf("omnibuf: Unmarshal requires non-nil proto.Message")
		}
		if needsProtoReflection(pm) {
			return unmarshalProtoReflectWithOptions(packet, pm.ProtoReflect(), o.MaxBytes, o.MaxDepth, o.Merge)
		}
	}
	return omnibuf.UnmarshalOptions{
		MaxBytes: o.MaxBytes,
		MaxDepth: o.MaxDepth,
		Merge:    o.Merge,
		ZeroCopy: o.ZeroCopy,
	}.Unmarshal(packet, v)
}

// Unmarshal decodes an OmniBuf packet into v (any proto.Message, protoreflect.Message, or struct pointer).
func Unmarshal(packet []byte, v any) error {
	return UnmarshalOptions{}.Unmarshal(packet, v)
}

// UnmarshalZeroCopy decodes an OmniBuf packet into v using zero-copy string views when supported.
func UnmarshalZeroCopy(packet []byte, v any) error {
	return UnmarshalOptions{ZeroCopy: true}.Unmarshal(packet, v)
}

// UnmarshalMerge decodes an OmniBuf packet into v while preserving existing fields not present in packet.
func UnmarshalMerge(packet []byte, v any) error {
	return UnmarshalOptions{Merge: true}.Unmarshal(packet, v)
}
