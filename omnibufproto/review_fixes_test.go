package omnibufproto_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/omnibufproto"
	"github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func buildOutOfOrderDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("out_of_order.proto"),
		Package: proto.String("test.order"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("OutOfOrder"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:     proto.String("tenth"),
						Number:   proto.Int32(10),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						JsonName: proto.String("tenth"),
					},
					{
						Name:     proto.String("second"),
						Number:   proto.Int32(2),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum(),
						JsonName: proto.String("second"),
					},
					{
						Name:     proto.String("fifth"),
						Number:   proto.Int32(5),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						JsonName: proto.String("fifth"),
					},
					{
						Name:     proto.String("nested"),
						Number:   proto.Int32(1),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".test.order.OutOfOrder"),
						JsonName: proto.String("nested"),
					},
				},
			},
		},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("protodesc.NewFile: %v", err)
	}
	return fd.Messages().ByName("OutOfOrder")
}

func TestProtoReflectAscendingFieldOrderAndCachedSchemaMeta(t *testing.T) {
	md := buildOutOfOrderDescriptor(t)
	msg := dynamicpb.NewMessage(md)
	msg.Set(md.Fields().ByNumber(10), protoreflect.ValueOfString("ten"))
	msg.Set(md.Fields().ByNumber(2), protoreflect.ValueOfUint32(22))
	list := msg.Mutable(md.Fields().ByNumber(5)).List()
	list.Append(protoreflect.ValueOfString("five"))

	raw, err := omnibufproto.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify emitted wire field numbers are strictly ascending: 2, 5, 10
	var gotOrder []uint32
	pos := 0
	for pos < len(raw) {
		tag, n := wire.DecodeVarint(raw[pos:])
		if n == 0 {
			t.Fatalf("invalid tag at %d", pos)
		}
		pos += n
		gotOrder = append(gotOrder, uint32(tag>>4))
		nextPos, err := wire.SkipValue(raw, pos, uint8(tag&0x0F))
		if err != nil {
			t.Fatalf("SkipValue: %v", err)
		}
		pos = nextPos
	}
	wantOrder := []uint32{2, 5, 10}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("field order = %v, want %v", gotOrder, wantOrder)
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("field order = %v, want %v", gotOrder, wantOrder)
		}
	}

	// SelfDescribing repeated calls must produce identical bytes (cached SchemaMeta path)
	sd1, err := omnibufproto.MarshalOptions{SelfDescribing: true, Deterministic: true}.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal SelfDescribing 1: %v", err)
	}
	sd2, err := omnibufproto.MarshalOptions{SelfDescribing: true, Deterministic: true}.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal SelfDescribing 2: %v", err)
	}
	if !bytes.Equal(sd1, sd2) {
		t.Fatalf("SelfDescribing outputs differ across calls")
	}
}

func TestProtoReflectUnmarshalOptions(t *testing.T) {
	md := buildOutOfOrderDescriptor(t)
	m1 := dynamicpb.NewMessage(md)
	m1.Set(md.Fields().ByNumber(2), protoreflect.ValueOfUint32(7))
	m1.Mutable(md.Fields().ByNumber(5)).List().Append(protoreflect.ValueOfString("a"))

	p1, err := omnibufproto.Marshal(m1)
	if err != nil {
		t.Fatalf("Marshal m1: %v", err)
	}
	// Encode p2 with only field 5 and field 10 present on the wire (field 2 absent)
	enc2 := wire.NewEncoder(64)
	enc2.WriteStringArr(5, []string{"b"})
	enc2.WriteString(10, "merged")
	p2 := enc2.Bytes()

	// MaxBytes
	dst := dynamicpb.NewMessage(md)
	if err := (omnibufproto.UnmarshalOptions{MaxBytes: len(p1) - 1}).Unmarshal(p1, dst); !errors.Is(err, omnibuf.ErrMessageTooLarge) {
		t.Fatalf("expected ErrMessageTooLarge, got %v", err)
	}

	// Merge
	if err := omnibufproto.Unmarshal(p1, dst); err != nil {
		t.Fatalf("Unmarshal p1: %v", err)
	}
	if err := omnibufproto.UnmarshalMerge(p2, dst); err != nil {
		t.Fatalf("UnmarshalMerge p2: %v", err)
	}
	if got := dst.Get(md.Fields().ByNumber(2)).Uint(); got != 7 {
		t.Fatalf("merged field 2 = %d, want 7", got)
	}
	if got := dst.Get(md.Fields().ByNumber(10)).String(); got != "merged" {
		t.Fatalf("merged field 10 = %q, want %q", got, "merged")
	}
	list := dst.Get(md.Fields().ByNumber(5)).List()
	if list.Len() != 2 || list.Get(0).String() != "a" || list.Get(1).String() != "b" {
		t.Fatalf("merged field 5 list len=%d", list.Len())
	}

	// MaxDepth
	root := dynamicpb.NewMessage(md)
	child := dynamicpb.NewMessage(md)
	grand := dynamicpb.NewMessage(md)
	grand.Set(md.Fields().ByNumber(2), protoreflect.ValueOfUint32(99))
	child.Set(md.Fields().ByNumber(1), protoreflect.ValueOfMessage(grand))
	root.Set(md.Fields().ByNumber(1), protoreflect.ValueOfMessage(child))
	deepPkt, err := omnibufproto.Marshal(root)
	if err != nil {
		t.Fatalf("Marshal deep: %v", err)
	}
	deepDst := dynamicpb.NewMessage(md)
	if err := (omnibufproto.UnmarshalOptions{MaxDepth: 2}).Unmarshal(deepPkt, deepDst); !errors.Is(err, omnibuf.ErrMaxDepth) {
		t.Fatalf("expected ErrMaxDepth for MaxDepth=2, got %v", err)
	}
	if err := (omnibufproto.UnmarshalOptions{MaxDepth: 3}).Unmarshal(deepPkt, deepDst); err != nil {
		t.Fatalf("MaxDepth=3 failed: %v", err)
	}
}
