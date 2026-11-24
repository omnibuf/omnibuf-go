package omnibufproto

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func buildCyclicDynamicDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	rep := descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("cyclic.proto"),
		Package: proto.String("test.cyclic"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Node"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("id"), Number: proto.Int32(1), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum()},
					{Name: proto.String("next"), Number: proto.Int32(2), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".test.cyclic.Node")},
					{Name: proto.String("children"), Number: proto.Int32(3), Label: rep.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".test.cyclic.Node")},
					{Name: proto.String("by_key"), Number: proto.Int32(4), Label: rep.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".test.cyclic.Node.ByKeyEntry")},
				},
				NestedType: []*descriptorpb.DescriptorProto{
					{
						Name:    proto.String("ByKeyEntry"),
						Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
						Field: []*descriptorpb.FieldDescriptorProto{
							{Name: proto.String("key"), Number: proto.Int32(1), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
							{Name: proto.String("value"), Number: proto.Int32(2), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".test.cyclic.Node")},
						},
					},
				},
			},
		},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("protodesc.NewFile: %v", err)
	}
	return fd.Messages().ByName("Node")
}

func TestMarshalProtoReflectCyclicReturnsError(t *testing.T) {
	md := buildCyclicDynamicDescriptor(t)

	t.Run("singular_cycle", func(t *testing.T) {
		msg := dynamicpb.NewMessage(md)
		msg.Set(md.Fields().ByName("id"), protoreflect.ValueOfUint32(1))
		msg.Set(md.Fields().ByName("next"), protoreflect.ValueOfMessage(msg))

		if _, err := Marshal(msg); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("Marshal(singular cycle) = %v, want MaxNestingDepth error", err)
		}
		if _, err := (MarshalOptions{SelfDescribing: true}).Marshal(msg); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("Marshal SelfDescribing(singular cycle) = %v, want MaxNestingDepth error", err)
		}
	})

	t.Run("list_cycle", func(t *testing.T) {
		msg := dynamicpb.NewMessage(md)
		msg.Mutable(md.Fields().ByName("children")).List().Append(protoreflect.ValueOfMessage(msg))

		if _, err := Marshal(msg); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("Marshal(list cycle) = %v, want MaxNestingDepth error", err)
		}
	})

	t.Run("map_cycle", func(t *testing.T) {
		msg := dynamicpb.NewMessage(md)
		msg.Mutable(md.Fields().ByName("by_key")).Map().Set(
			protoreflect.ValueOfString("self").MapKey(),
			protoreflect.ValueOfMessage(msg),
		)

		if _, err := Marshal(msg); err == nil || !strings.Contains(err.Error(), "MaxNestingDepth") {
			t.Fatalf("Marshal(map cycle) = %v, want MaxNestingDepth error", err)
		}
	})
}
