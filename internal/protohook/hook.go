package protohook

// MarshalHook allows the optional omnibufproto submodule to register
// proto.Message and protoreflect.Message serialization without adding
// google.golang.org/protobuf to the core omnibuf-go module.
var MarshalHook func(dst []byte, v any, deterministic, selfDescribing bool) ([]byte, bool, error)

// UnmarshalHook allows the optional omnibufproto submodule to register
// proto.Message and protoreflect.Message deserialization.
var UnmarshalHook func(packet []byte, v any) (bool, error)
