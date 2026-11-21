package omnibufproto

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type proto2ReqInner struct {
	md protoreflect.MessageDescriptor
}

func (m *proto2ReqInner) ProtoReflect() protoreflect.Message {
	return dynamicpb.NewMessage(m.md)
}

type proto2ReqOuter struct {
	Inner *proto2ReqInner `protobuf:"bytes,1,req,name=inner"`
	md    protoreflect.MessageDescriptor
}

func (m *proto2ReqOuter) ProtoReflect() protoreflect.Message {
	return dynamicpb.NewMessage(m.md)
}

func TestNeedsProtoReflection_Proto2RequiredMessageField(t *testing.T) {
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("req_msg.proto"),
		Package: proto.String("test.req"),
		Syntax:  proto.String("proto2"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Inner"),
			},
			{
				Name: proto.String("Outer"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:     proto.String("inner"),
						Number:   proto.Int32(1),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_REQUIRED.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".test.req.Inner"),
					},
				},
			},
		},
	}

	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("protodesc.NewFile: %v", err)
	}
	outerDesc := fd.Messages().ByName("Outer")
	msg := &proto2ReqOuter{md: outerDesc}

	// Precondition: struct layout satisfies hasAllOpenStructFields, so only the
	// descriptor check for protoreflect.Required forces reflective dispatch.
	if !hasAllOpenStructFields(reflect.TypeOf(msg), outerDesc, make(map[reflect.Type]bool)) {
		t.Fatal("precondition failed: expected proto2ReqOuter to satisfy hasAllOpenStructFields")
	}

	if !needsProtoReflection(msg) {
		t.Fatal("expected needsProtoReflection to return true for proto2 message with required message field")
	}

	// Marshal and Unmarshal must reject missing required message field via proto.CheckInitialized.
	dynMsg := dynamicpb.NewMessage(outerDesc)
	if _, err := Marshal(dynMsg); err == nil {
		t.Fatal("expected Marshal to fail on uninitialized proto2 required message field")
	}
	if err := Unmarshal(nil, dynMsg); err == nil {
		t.Fatal("expected Unmarshal to fail when proto2 required message field is absent")
	}
}
