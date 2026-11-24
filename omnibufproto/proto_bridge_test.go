package omnibufproto_test

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/omnibufproto"
	"github.com/omnibuf/omnibuf-go/omnibufproto/internal/pb"
	"github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/runtime/protoiface"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// TestProtoDispatchValueStructEmbeddingProtoPointer verifies that passing a
// value struct embedding *pb.PeerUpdate does not panic in Marshal or Unmarshal.
func TestProtoDispatchValueStructEmbeddingProtoPointer(t *testing.T) {
	type Envelope struct {
		*pb.PeerUpdate
	}

	validPkt, err := omnibufproto.Marshal(&pb.PeerUpdate{Asn: 64512, RouterId: "rtr-01"})
	if err != nil {
		t.Fatalf("precondition Marshal failed: %v", err)
	}

	t.Run("omnibufproto_Marshal_non_nil", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("omnibufproto.Marshal panicked on value Envelope: %v", r)
			}
		}()
		env := Envelope{PeerUpdate: &pb.PeerUpdate{Asn: 64512, RouterId: "rtr-01"}}
		_, _ = omnibufproto.Marshal(env)
	})

	t.Run("omnibufproto_Marshal_nil_inner", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("omnibufproto.Marshal panicked on zero Envelope{}: %v", r)
			}
		}()
		if _, err := omnibufproto.Marshal(Envelope{}); err == nil {
			t.Fatal("expected error from omnibufproto.Marshal(Envelope{}), got nil")
		}
	})

	t.Run("omnibufproto_Unmarshal", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("omnibufproto.Unmarshal panicked on zero Envelope{}: %v", r)
			}
		}()
		if err := omnibufproto.Unmarshal(validPkt, Envelope{}); err == nil {
			t.Fatal("expected error from omnibufproto.Unmarshal(pkt, Envelope{}), got nil")
		}
	})

	t.Run("omnibuf_Marshal", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("omnibuf.Marshal panicked on value Envelope: %v", r)
			}
		}()
		if _, err := omnibuf.Marshal(Envelope{}); err == nil {
			t.Fatal("expected error from omnibuf.Marshal(Envelope{}), got nil")
		}
	})
}

// TestProtoUnmarshalClearsUnknownFieldsAndSizeCacheOnReuse verifies that
// decoding into a reused *pb.PeerUpdate clears stale unknownFields and sizeCache.
func TestProtoUnmarshalClearsUnknownFieldsAndSizeCacheOnReuse(t *testing.T) {
	basePB, err := proto.Marshal(&pb.PeerUpdate{Asn: 100, RouterId: "old"})
	if err != nil {
		t.Fatalf("proto.Marshal base: %v", err)
	}
	pbWithUnknown := append([]byte(nil), basePB...)
	pbWithUnknown = protowire.AppendTag(pbWithUnknown, 99, protowire.VarintType)
	pbWithUnknown = protowire.AppendVarint(pbWithUnknown, 777)

	cleanTarget := &pb.PeerUpdate{Asn: 200, RouterId: "clean"}
	cleanOmni, err := omnibufproto.Marshal(cleanTarget)
	if err != nil {
		t.Fatalf("omnibufproto.Marshal cleanTarget: %v", err)
	}
	wantCleanPB, err := proto.Marshal(cleanTarget)
	if err != nil {
		t.Fatalf("proto.Marshal cleanTarget: %v", err)
	}

	t.Run("unknownFields_UnmarshalOmniBuf", func(t *testing.T) {
		reused := &pb.PeerUpdate{}
		if err := proto.Unmarshal(pbWithUnknown, reused); err != nil {
			t.Fatalf("proto.Unmarshal pbWithUnknown: %v", err)
		}
		if len(reused.ProtoReflect().GetUnknown()) == 0 {
			t.Fatal("precondition failed: expected non-empty unknownFields")
		}
		if err := omnibufproto.Unmarshal(cleanOmni, reused); err != nil {
			t.Fatalf("omnibufproto.Unmarshal: %v", err)
		}
		if gotUnknown := reused.ProtoReflect().GetUnknown(); len(gotUnknown) != 0 {
			t.Errorf("stale unknownFields leaked after omnibufproto.Unmarshal: %x", gotUnknown)
		}
		gotPB, err := proto.Marshal(reused)
		if err != nil {
			t.Fatalf("proto.Marshal reused: %v", err)
		}
		if !bytes.Equal(gotPB, wantCleanPB) {
			t.Fatalf("proto.Marshal leaked stale unknown field #99: got %x, want %x", gotPB, wantCleanPB)
		}
	})

	t.Run("unknownFields_omnibuf_Unmarshal", func(t *testing.T) {
		reused := &pb.PeerUpdate{}
		if err := proto.Unmarshal(pbWithUnknown, reused); err != nil {
			t.Fatalf("proto.Unmarshal pbWithUnknown: %v", err)
		}
		if err := omnibuf.Unmarshal(cleanOmni, reused); err != nil {
			t.Fatalf("omnibuf.Unmarshal: %v", err)
		}
		if gotUnknown := reused.ProtoReflect().GetUnknown(); len(gotUnknown) != 0 {
			t.Errorf("stale unknownFields leaked after omnibuf.Unmarshal: %x", gotUnknown)
		}
		gotPB, err := proto.Marshal(reused)
		if err != nil {
			t.Fatalf("proto.Marshal reused: %v", err)
		}
		if !bytes.Equal(gotPB, wantCleanPB) {
			t.Fatalf("proto.Marshal leaked stale unknown field #99: got %x, want %x", gotPB, wantCleanPB)
		}
	})

	smallTarget := &pb.PeerUpdate{
		Asn:      1,
		RouterId: "a",
		Location: &pb.Location{City: "NY"},
	}
	smallOmni, err := omnibufproto.Marshal(smallTarget)
	if err != nil {
		t.Fatalf("omnibufproto.Marshal smallTarget: %v", err)
	}
	wantSmallPB, err := proto.Marshal(smallTarget)
	if err != nil {
		t.Fatalf("proto.Marshal smallTarget: %v", err)
	}

	t.Run("sizeCache_UnmarshalOmniBuf", func(t *testing.T) {
		reused := &pb.PeerUpdate{
			Asn:      999999,
			RouterId: strings.Repeat("r", 96),
			Location: &pb.Location{City: strings.Repeat("c", 96)},
		}
		if _, err := proto.Marshal(reused); err != nil {
			t.Fatalf("proto.Marshal populate sizeCache: %v", err)
		}
		if err := omnibufproto.Unmarshal(smallOmni, reused); err != nil {
			t.Fatalf("omnibufproto.Unmarshal smallOmni: %v", err)
		}
		cachedPB, err := (proto.MarshalOptions{UseCachedSize: true}).Marshal(reused)
		if err != nil {
			t.Fatalf("proto.Marshal(UseCachedSize): %v", err)
		}
		if !bytes.Equal(cachedPB, wantSmallPB) {
			t.Fatalf("stale sizeCache corrupted UseCachedSize output after omnibufproto.Unmarshal:\n got (%dB): %x\nwant (%dB): %x", len(cachedPB), cachedPB, len(wantSmallPB), wantSmallPB)
		}
	})

	t.Run("sizeCache_omnibuf_Unmarshal", func(t *testing.T) {
		reused := &pb.PeerUpdate{
			Asn:      999999,
			RouterId: strings.Repeat("r", 96),
			Location: &pb.Location{City: strings.Repeat("c", 96)},
		}
		if _, err := proto.Marshal(reused); err != nil {
			t.Fatalf("proto.Marshal populate sizeCache: %v", err)
		}
		if err := omnibuf.Unmarshal(smallOmni, reused); err != nil {
			t.Fatalf("omnibuf.Unmarshal smallOmni: %v", err)
		}
		cachedPB, err := (proto.MarshalOptions{UseCachedSize: true}).Marshal(reused)
		if err != nil {
			t.Fatalf("proto.Marshal(UseCachedSize): %v", err)
		}
		if !bytes.Equal(cachedPB, wantSmallPB) {
			t.Fatalf("stale sizeCache corrupted UseCachedSize output after omnibuf.Unmarshal:\n got (%dB): %x\nwant (%dB): %x", len(cachedPB), cachedPB, len(wantSmallPB), wantSmallPB)
		}
	})
}

// TestProtoMarshalRejectsInvalidUTF8Consistently verifies that omnibufproto.Marshal
// rejects invalid UTF-8 strings consistently with MarshalDeterministic.
func TestProtoMarshalRejectsInvalidUTF8Consistently(t *testing.T) {
	bad := &pb.PeerUpdate{RouterId: "\xff"}
	if _, err := omnibufproto.MarshalDeterministic(bad); err == nil {
		t.Fatal("precondition failed: expected MarshalDeterministic to reject invalid UTF-8")
	}

	pkt, err := omnibufproto.Marshal(bad)
	if err == nil {
		t.Fatalf("omnibufproto.Marshal accepted invalid UTF-8 RouterId and emitted %x", pkt)
	}
}

var (
	hybridDescOnce sync.Once
	hybridMsgDesc  protoreflect.MessageDescriptor
)

func getHybridMsgDescriptor() protoreflect.MessageDescriptor {
	hybridDescOnce.Do(func() {
		opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
		fdp := &descriptorpb.FileDescriptorProto{
			Syntax:  proto.String("proto3"),
			Name:    proto.String("omnibuf/audit/hybrid.proto"),
			Package: proto.String("omnibuf.audit"),
			MessageType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String("HybridMsg"),
					Field: []*descriptorpb.FieldDescriptorProto{
						{
							Name:   proto.String("visible"),
							Number: proto.Int32(1),
							Label:  opt.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
						{
							Name:   proto.String("hidden"),
							Number: proto.Int32(2),
							Label:  opt.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum(),
						},
					},
				},
			},
		}
		fd, err := protodesc.NewFile(fdp, nil)
		if err != nil {
			panic(err)
		}
		hybridMsgDesc = fd.Messages().ByName("HybridMsg")
	})
	return hybridMsgDesc
}

type hybridProtoMsg struct {
	Visible string `protobuf:"bytes,1,opt,name=visible,proto3"`
	hidden  uint32
	unknown protoreflect.RawFields
}

func (m *hybridProtoMsg) ProtoReflect() protoreflect.Message {
	return &hybridMessageReflect{msg: m}
}

type hybridMessageType struct{}

func (hybridMessageType) New() protoreflect.Message {
	return &hybridMessageReflect{msg: &hybridProtoMsg{}}
}
func (hybridMessageType) Zero() protoreflect.Message                 { return &hybridMessageReflect{} }
func (hybridMessageType) Descriptor() protoreflect.MessageDescriptor { return getHybridMsgDescriptor() }

type hybridMessageReflect struct {
	msg *hybridProtoMsg
}

func (r *hybridMessageReflect) Descriptor() protoreflect.MessageDescriptor {
	return getHybridMsgDescriptor()
}
func (r *hybridMessageReflect) Type() protoreflect.MessageType { return hybridMessageType{} }
func (r *hybridMessageReflect) New() protoreflect.Message {
	return &hybridMessageReflect{msg: &hybridProtoMsg{}}
}
func (r *hybridMessageReflect) Interface() protoreflect.ProtoMessage { return r.msg }
func (r *hybridMessageReflect) IsValid() bool                        { return r.msg != nil }
func (r *hybridMessageReflect) ProtoMethods() *protoiface.Methods    { return nil }
func (r *hybridMessageReflect) GetUnknown() protoreflect.RawFields {
	if r.msg == nil {
		return nil
	}
	return r.msg.unknown
}
func (r *hybridMessageReflect) SetUnknown(b protoreflect.RawFields) {
	r.msg.unknown = b
}
func (r *hybridMessageReflect) WhichOneof(protoreflect.OneofDescriptor) protoreflect.FieldDescriptor {
	return nil
}
func (r *hybridMessageReflect) NewField(fd protoreflect.FieldDescriptor) protoreflect.Value {
	return fd.Default()
}
func (r *hybridMessageReflect) Mutable(protoreflect.FieldDescriptor) protoreflect.Value {
	panic("no composite fields on HybridMsg")
}
func (r *hybridMessageReflect) Range(f func(protoreflect.FieldDescriptor, protoreflect.Value) bool) {
	if r.msg == nil {
		return
	}
	fds := getHybridMsgDescriptor().Fields()
	if r.msg.Visible != "" {
		if !f(fds.ByNumber(1), protoreflect.ValueOfString(r.msg.Visible)) {
			return
		}
	}
	if r.msg.hidden != 0 {
		f(fds.ByNumber(2), protoreflect.ValueOfUint32(r.msg.hidden))
	}
}
func (r *hybridMessageReflect) Has(fd protoreflect.FieldDescriptor) bool {
	if r.msg == nil {
		return false
	}
	switch fd.Number() {
	case 1:
		return r.msg.Visible != ""
	case 2:
		return r.msg.hidden != 0
	default:
		return false
	}
}
func (r *hybridMessageReflect) Clear(fd protoreflect.FieldDescriptor) {
	switch fd.Number() {
	case 1:
		r.msg.Visible = ""
	case 2:
		r.msg.hidden = 0
	}
}
func (r *hybridMessageReflect) Get(fd protoreflect.FieldDescriptor) protoreflect.Value {
	if r.msg == nil {
		return fd.Default()
	}
	switch fd.Number() {
	case 1:
		return protoreflect.ValueOfString(r.msg.Visible)
	case 2:
		return protoreflect.ValueOfUint32(r.msg.hidden)
	default:
		return fd.Default()
	}
}
func (r *hybridMessageReflect) Set(fd protoreflect.FieldDescriptor, v protoreflect.Value) {
	switch fd.Number() {
	case 1:
		r.msg.Visible = v.String()
	case 2:
		r.msg.hidden = uint32(v.Uint())
	}
}

// TestProtoDispatchHybridMessagePartialExportedFields verifies that a hybrid
// proto.Message with unexported/opaque fields falls back to protoreflect
// instead of silently dropping fields that lack exported protobuf tags.
func TestProtoDispatchHybridMessagePartialExportedFields(t *testing.T) {
	src := &hybridProtoMsg{Visible: "alpha", hidden: 99}
	if gotLen := src.ProtoReflect().Descriptor().Fields().Len(); gotLen != 2 {
		t.Fatalf("precondition failed: descriptor fields len = %d, want 2", gotLen)
	}

	pbBytes, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("precondition proto.Marshal failed: %v", err)
	}
	var checkPB hybridProtoMsg
	if err := proto.Unmarshal(pbBytes, &checkPB); err != nil || checkPB.Visible != "alpha" || checkPB.hidden != 99 {
		t.Fatalf("precondition proto round-trip failed: err=%v, got=%+v", err, checkPB)
	}

	obBytes, err := omnibufproto.Marshal(src)
	if err != nil {
		t.Fatalf("omnibufproto.Marshal(hybridProtoMsg): %v", err)
	}
	var dst hybridProtoMsg
	if err := omnibufproto.Unmarshal(obBytes, &dst); err != nil {
		t.Fatalf("omnibufproto.Unmarshal(hybridProtoMsg): %v", err)
	}
	if dst.Visible != "alpha" || dst.hidden != 99 {
		t.Fatalf("hybrid proto.Message lost field #2: got Visible=%q hidden=%d, want Visible=\"alpha\" hidden=99", dst.Visible, dst.hidden)
	}
}

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
