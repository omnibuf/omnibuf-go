package omnibufproto_test

import (
	"bytes"
	"reflect"
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
)

// TestAuditGO2_ValueStructEmbeddingProtoPointer isolates GO-2:
// Passing a value struct that embeds `*pb.PeerUpdate` (`type Envelope struct { *pb.PeerUpdate }`)
// satisfies `proto.Message` / `generatedMarshaler` / `generatedUnmarshaler`, causing
// unguarded `reflect.ValueOf(v).IsNil()` calls in omnibufproto.Marshal,
// omnibufproto.Unmarshal, and omnibuf.Marshal (via tryMarshalHook) to panic.
func TestAuditGO2_ValueStructEmbeddingProtoPointer(t *testing.T) {
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

// TestAuditGO3_ReusedProtoMessageStaleUnknownFieldsAndSizeCache isolates GO-3:
// Decoding into a reused `*pb.PeerUpdate` via `UnmarshalOmniBuf` (omnibufproto.Unmarshal)
// or `omnibuf.Unmarshal` leaves stale `unknownFields` and `sizeCache` intact, causing
// subsequent `proto.Marshal` to leak old unknown fields and `UseCachedSize: true` to
// emit corrupt protobuf wire output.
func TestAuditGO3_ReusedProtoMessageStaleUnknownFieldsAndSizeCache(t *testing.T) {
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

// TestAuditGO4_MultiLevelScalarPointerStruct isolates GO-4:
// A struct with a multi-level scalar pointer (`Val **uint32`) unwraps only one
// pointer indirection in `encodeStruct`, emitting a bare field tag with 0 payload
// bytes that fails `wire.ValidatePacket` and `omnibuf.Unmarshal`.
func TestAuditGO4_MultiLevelScalarPointerStruct(t *testing.T) {
	type DoublePtr struct {
		Val **uint32 `omnibuf:"1"`
	}

	v := uint32(424242)
	p := &v
	src := DoublePtr{Val: &p}

	pkt, err := omnibuf.Marshal(&src)
	if err != nil {
		t.Fatalf("omnibuf.Marshal(&DoublePtr): %v", err)
	}
	if err := wire.ValidatePacket(pkt); err != nil {
		t.Fatalf("omnibuf.Marshal(&DoublePtr) emitted invalid packet %x: %v", pkt, err)
	}

	var dst DoublePtr
	if err := omnibuf.Unmarshal(pkt, &dst); err != nil {
		t.Fatalf("omnibuf.Unmarshal(DoublePtr) failed on packet %x: %v", pkt, err)
	}
	if dst.Val == nil || *dst.Val == nil || **dst.Val != v {
		t.Fatalf("DoublePtr round-trip mismatch: got %+v, want %d", dst.Val, v)
	}
}

// TestAuditGO6a_ValidatePacketAndSchemaMetaRejectInvalidUTF8 isolates GO-6 (validator part):
// `wire.ValidatePacket` accepts `WireString` containing invalid UTF-8 `[0xFF, 0xFE]`,
// and `wire.ValidateSchemaMeta` accepts an unknown `WireString` field `#9` inside
// `Field #0` containing invalid UTF-8 `[0xFF, 0xFE]`.
func TestAuditGO6a_ValidatePacketAndSchemaMetaRejectInvalidUTF8(t *testing.T) {
	t.Run("ValidatePacket_WireString", func(t *testing.T) {
		var pkt []byte
		pkt = wire.AppendVarint(pkt, (1<<4)|uint64(wire.WireString))
		pkt = wire.AppendVarint(pkt, 2)
		pkt = append(pkt, 0xFF, 0xFE)

		if err := wire.ValidatePacket(pkt); err == nil {
			t.Fatalf("wire.ValidatePacket accepted invalid UTF-8 WireString %x", pkt)
		}
	})

	t.Run("ValidateSchemaMeta_UnknownWireStringField", func(t *testing.T) {
		var metaBody []byte
		metaBody = wire.AppendVarint(metaBody, (9<<4)|uint64(wire.WireString))
		metaBody = wire.AppendVarint(metaBody, 2)
		metaBody = append(metaBody, 0xFF, 0xFE)

		if wire.ValidateSchemaMeta(metaBody, 1) {
			t.Errorf("wire.ValidateSchemaMeta accepted invalid UTF-8 in unknown WireString field #9: %x", metaBody)
		}

		var pkt []byte
		pkt = wire.AppendVarint(pkt, (0<<4)|uint64(wire.WireSubMsg))
		pkt = wire.AppendVarint(pkt, uint64(len(metaBody)))
		pkt = append(pkt, metaBody...)

		if err := wire.ValidatePacket(pkt); err == nil {
			t.Errorf("wire.ValidatePacket accepted Field #0 with invalid UTF-8 in unknown WireString field #9: %x", pkt)
		}
		if _, ok := omnibuf.ReadField0SchemaMeta(pkt); ok {
			t.Errorf("omnibuf.ReadField0SchemaMeta accepted Field #0 with invalid UTF-8 in unknown WireString field #9: %x", pkt)
		}
	})
}

// TestAuditGO6b_MarshalRejectsInvalidUTF8Consistently isolates GO-6 (marshaler part):
// `omnibufproto.Marshal(&pb.PeerUpdate{RouterId: "\xff"})` dispatches to the generated
// fast path without UTF-8 validation and succeeds, whereas `MarshalDeterministic`
// rejects the same message.
func TestAuditGO6b_MarshalRejectsInvalidUTF8Consistently(t *testing.T) {
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

// TestAuditGO7_HybridProtoMessagePartialExportedFields isolates GO-7:
// `hasAllOpenStructFields` returns `true` whenever `pbFieldCount > 0` instead of
// verifying that all `md.Fields().Len()` descriptor fields are backed by exported
// `protobuf:"..."` struct tags, silently dropping unexported/opaque fields.
func TestAuditGO7_HybridProtoMessagePartialExportedFields(t *testing.T) {
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

// TestAuditGO10_MarshalAppendAndCopyPacketPreserveDstPrefix isolates GO-10:
// Existing prefix bytes in `dst` (`0xAA, 0xBB, 0xCC, 0xDD`) are wiped out instead
// of preserved by `omnibuf.MarshalAppend`, `omnibufproto.MarshalOptions{SelfDescribing: true}.MarshalAppend`,
// `wire.CopyPacket`, and `pb.OpaquePeerUpdate.MarshalAppend`.
func TestAuditGO10_MarshalAppendAndCopyPacketPreserveDstPrefix(t *testing.T) {
	prefix := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	makeDst := func(capacity int) []byte {
		b := make([]byte, len(prefix), capacity)
		copy(b, prefix)
		return b
	}

	type simpleRecord struct {
		ID uint32 `omnibuf:"1"`
	}

	samplePB := &pb.PeerUpdate{Asn: 64512, RouterId: "rtr-fra-01"}
	sampleRaw, err := omnibufproto.Marshal(samplePB)
	if err != nil {
		t.Fatalf("precondition Marshal samplePB: %v", err)
	}

	for _, capSize := range []int{len(prefix), 1024} {
		capName := "small_cap"
		if capSize > len(prefix) {
			capName = "large_cap"
		}

		t.Run("omnibuf_MarshalAppend_"+capName, func(t *testing.T) {
			out, err := omnibuf.MarshalAppend(makeDst(capSize), &simpleRecord{ID: 7})
			if err != nil {
				t.Fatalf("omnibuf.MarshalAppend: %v", err)
			}
			if !bytes.HasPrefix(out, prefix) {
				t.Fatalf("omnibuf.MarshalAppend wiped prefix %x: got %x", prefix, out)
			}
		})

		t.Run("omnibufproto_MarshalAppend_SelfDescribing_"+capName, func(t *testing.T) {
			// Test both open-struct proto and protoreflect-dispatched proto (TestAllTypes has enums/oneofs).
			outOpen, err := (omnibufproto.MarshalOptions{SelfDescribing: true}).MarshalAppend(makeDst(capSize), samplePB)
			if err != nil {
				t.Fatalf("MarshalOptions{SelfDescribing:true}.MarshalAppend(PeerUpdate): %v", err)
			}
			if !bytes.HasPrefix(outOpen, prefix) {
				t.Errorf("MarshalOptions{SelfDescribing:true}.MarshalAppend(PeerUpdate) wiped prefix %x: got %x", prefix, outOpen)
			}

			outReflect, err := (omnibufproto.MarshalOptions{SelfDescribing: true}).MarshalAppend(makeDst(capSize), &pb.TestAllTypes{F1Uint32: 42})
			if err != nil {
				t.Fatalf("MarshalOptions{SelfDescribing:true}.MarshalAppend(TestAllTypes): %v", err)
			}
			if !bytes.HasPrefix(outReflect, prefix) {
				t.Errorf("MarshalOptions{SelfDescribing:true}.MarshalAppend(TestAllTypes) wiped prefix %x: got %x", prefix, outReflect)
			}
		})

		t.Run("wire_CopyPacket_"+capName, func(t *testing.T) {
			out := wire.CopyPacket(makeDst(capSize), sampleRaw)
			if !bytes.HasPrefix(out, prefix) || !bytes.Equal(out[len(prefix):], sampleRaw) {
				t.Fatalf("wire.CopyPacket wiped prefix %x: got %x", prefix, out)
			}
		})

		t.Run("OpaquePeerUpdate_MarshalAppend_clean_"+capName, func(t *testing.T) {
			var op pb.OpaquePeerUpdate
			if err := op.Unmarshal(sampleRaw); err != nil {
				t.Fatalf("OpaquePeerUpdate.Unmarshal: %v", err)
			}
			out := op.MarshalAppend(makeDst(capSize))
			if !bytes.HasPrefix(out, prefix) {
				t.Fatalf("OpaquePeerUpdate.MarshalAppend (clean) wiped prefix %x: got %x", prefix, out)
			}
		})

		t.Run("OpaquePeerUpdate_MarshalAppend_dirty_"+capName, func(t *testing.T) {
			var op pb.OpaquePeerUpdate
			if err := op.Unmarshal(sampleRaw); err != nil {
				t.Fatalf("OpaquePeerUpdate.Unmarshal: %v", err)
			}
			op.SetAsn(65001)
			out := op.MarshalAppend(makeDst(capSize))
			if !bytes.HasPrefix(out, prefix) {
				t.Fatalf("OpaquePeerUpdate.MarshalAppend (dirty) wiped prefix %x: got %x", prefix, out)
			}
		})
	}
}

// TestAuditGO11_OpaqueResetAndStringSliceTailZeroing isolates GO-11:
//  1. `OpaquePeerUpdate.Unmarshal(emptyBytes)` clears `dirty` without zeroing
//     cached heap fields (`f_router_id`, `f_tags`, `f_location`).
//  2. Shrinking a `[]string` slice from 4 elements to 1 during `Unmarshal` leaves
//     `s[:4]` tail string headers non-zeroed in the backing array.
func TestAuditGO11_OpaqueResetAndStringSliceTailZeroing(t *testing.T) {
	t.Run("OpaquePeerUpdate_Unmarshal_clears_dirty_heap_fields", func(t *testing.T) {
		var op pb.OpaquePeerUpdate
		op.SetRouterId(strings.Repeat("dirty-router-", 4))
		op.SetTags([]string{"dirty-tag-1", "dirty-tag-2"})
		op.SetLocation(&pb.Location{City: "Frankfurt"})

		if err := op.Unmarshal([]byte{}); err != nil {
			t.Fatalf("OpaquePeerUpdate.Unmarshal(empty): %v", err)
		}

		rv := reflect.ValueOf(&op).Elem()
		if gotRouter := rv.FieldByName("f_router_id").String(); gotRouter != "" {
			t.Errorf("OpaquePeerUpdate retained dirty f_router_id = %q after Unmarshal(empty)", gotRouter)
		}
		if gotTagsLen := rv.FieldByName("f_tags").Len(); gotTagsLen != 0 {
			t.Errorf("OpaquePeerUpdate retained dirty f_tags (len=%d) after Unmarshal(empty)", gotTagsLen)
		}
		if !rv.FieldByName("f_location").IsNil() {
			t.Errorf("OpaquePeerUpdate retained dirty f_location pointer after Unmarshal(empty)")
		}
	})

	t.Run("Unmarshal_shrinking_string_slice_zeros_tail", func(t *testing.T) {
		type stringListMsg struct {
			Items []string `omnibuf:"1"`
		}
		pkt4, err := omnibuf.Marshal(&stringListMsg{Items: []string{"s0", "s1", "s2", "s3"}})
		if err != nil {
			t.Fatalf("Marshal 4 strings: %v", err)
		}
		pkt1, err := omnibuf.Marshal(&stringListMsg{Items: []string{"only"}})
		if err != nil {
			t.Fatalf("Marshal 1 string: %v", err)
		}

		var dst stringListMsg
		if err := omnibuf.Unmarshal(pkt4, &dst); err != nil {
			t.Fatalf("Unmarshal 4 strings: %v", err)
		}
		if err := omnibuf.Unmarshal(pkt1, &dst); err != nil {
			t.Fatalf("Unmarshal 1 string: %v", err)
		}
		if cap(dst.Items) < 4 {
			t.Fatalf("precondition failed: expected reused slice cap >= 4, got %d", cap(dst.Items))
		}
		backing := dst.Items[:4]
		for i := 1; i < 4; i++ {
			if backing[i] != "" {
				t.Errorf("omnibuf.Unmarshal left stale string header at backing[%d] = %q", i, backing[i])
			}
		}

		pb4, err := omnibufproto.Marshal(&pb.PeerUpdate{Tags: []string{"t0", "t1", "t2", "t3"}})
		if err != nil {
			t.Fatalf("Marshal PeerUpdate 4 tags: %v", err)
		}
		pb1, err := omnibufproto.Marshal(&pb.PeerUpdate{Tags: []string{"only"}})
		if err != nil {
			t.Fatalf("Marshal PeerUpdate 1 tag: %v", err)
		}
		var pbDst pb.PeerUpdate
		if err := omnibufproto.Unmarshal(pb4, &pbDst); err != nil {
			t.Fatalf("Unmarshal PeerUpdate 4 tags: %v", err)
		}
		if err := omnibufproto.Unmarshal(pb1, &pbDst); err != nil {
			t.Fatalf("Unmarshal PeerUpdate 1 tag: %v", err)
		}
		pbBacking := pbDst.Tags[:4]
		for i := 1; i < 4; i++ {
			if pbBacking[i] != "" {
				t.Errorf("omnibufproto.Unmarshal left stale string header at pbDst.Tags[:4][%d] = %q", i, pbBacking[i])
			}
		}
	})
}

// TestAuditGO12a_WriteField0FieldsDeterminismAndValidation isolates GO-12 (part 1):
// `Encoder.WriteField0Fields` iterates an unsorted Go map (producing non-deterministic
// wire bytes across runs) and accepts key `0` and invalid UTF-8 names (emitting
// structurally invalid Field #0 packets).
func TestAuditGO12a_WriteField0FieldsDeterminismAndValidation(t *testing.T) {
	t.Run("deterministic_key_order", func(t *testing.T) {
		fields := map[uint32]string{
			1: "f1", 2: "f2", 3: "f3", 4: "f4",
			5: "f5", 6: "f6", 7: "f7", 8: "f8",
			9: "f9", 10: "f10", 11: "f11", 12: "f12",
		}
		want := (&omnibuf.SchemaMeta{Fields: fields}).AppendField0(nil, true)

		enc := wire.NewEncoder(128)
		for runIdx := 0; runIdx < 32; runIdx++ {
			enc.Reset()
			enc.WriteField0Fields(fields)
			got := enc.Finish()
			if !bytes.Equal(got, want) {
				t.Fatalf("WriteField0Fields emitted non-deterministic key order on run %d:\n got: %x\nwant: %x", runIdx, got, want)
			}
		}
	})

	t.Run("rejects_key_0_and_invalid_utf8", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			fields map[uint32]string
		}{
			{name: "key_0", fields: map[uint32]string{0: "zero", 1: "valid"}},
			{name: "invalid_utf8", fields: map[uint32]string{1: "\xff\xfe", 2: "valid"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var pkt []byte
				panicked := false
				func() {
					defer func() {
						if recover() != nil {
							panicked = true
						}
					}()
					enc := wire.NewEncoder(64)
					enc.WriteField0Fields(tc.fields)
					pkt = append([]byte(nil), enc.Finish()...)
				}()
				if !panicked {
					if err := wire.ValidatePacket(pkt); err != nil {
						t.Fatalf("WriteField0Fields(%s) silently emitted corrupt Field #0 packet %x: %v", tc.name, pkt, err)
					}
				}
			})
		}
	})
}

// TestAuditGO12b_Uint32SintSliceSignExtension isolates GO-12 (part 2):
// A struct field `Vals []uint32 \`omnibuf:"1,wire=sint"\“ reinterprets `uint32`
// elements as `int32`, sign-extending `3_000_000_000` to `-1294967296` in
// `DecodeBlind` (contradicting scalar `Val uint32 \`omnibuf:"1,wire=sint"\“).
func TestAuditGO12b_Uint32SintSliceSignExtension(t *testing.T) {
	type scalarSint struct {
		Val uint32 `omnibuf:"1,wire=sint"`
	}
	type sliceSint struct {
		Vals []uint32 `omnibuf:"1,wire=sint"`
	}

	const large uint32 = 3_000_000_000

	scalarPkt, err := omnibuf.Marshal(&scalarSint{Val: large})
	if err != nil {
		t.Fatalf("Marshal scalarSint: %v", err)
	}
	scalarBlind, err := omnibuf.DecodeBlind(scalarPkt)
	if err != nil {
		t.Fatalf("DecodeBlind scalarSint: %v", err)
	}
	if scalarBlind["field_1"] != int64(large) {
		t.Fatalf("precondition failed: scalarSint DecodeBlind = %v, want %d", scalarBlind["field_1"], large)
	}

	slicePkt, err := omnibuf.Marshal(&sliceSint{Vals: []uint32{large}})
	if err != nil {
		t.Fatalf("Marshal sliceSint: %v", err)
	}
	sliceBlind, err := omnibuf.DecodeBlind(slicePkt)
	if err != nil {
		t.Fatalf("DecodeBlind sliceSint: %v", err)
	}
	want := []int64{int64(large)}
	if !reflect.DeepEqual(sliceBlind["field_1"], want) {
		t.Fatalf("sliceSint DecodeBlind sign-extended uint32: got %v (%T), want %v", sliceBlind["field_1"], sliceBlind["field_1"], want)
	}
}

// TestAuditGO12c_ReadNestedStringBytesCorruptSecondSegment isolates GO-12 (part 3):
// `wire.ReadNestedStringBytes` returns `ok == true` from the first sub-message
// segment even when a second concatenated occurrence of the sub-message is
// truncated/corrupt.
func TestAuditGO12c_ReadNestedStringBytesCorruptSecondSegment(t *testing.T) {
	enc := wire.NewEncoder(32)
	enc.BeginSubMsg(1)
	enc.WriteString(1, "Frankfurt")
	enc.EndSubMsg()
	validFirst := append([]byte(nil), enc.Finish()...)

	// Second occurrence of sub-message field #1 whose outer length (2 bytes) is
	// in-bounds, while its inner string field #1 claims length 50 (truncated).
	var corruptInner []byte
	corruptInner = wire.AppendVarint(corruptInner, (1<<4)|uint64(wire.WireString))
	corruptInner = wire.AppendVarint(corruptInner, 50)

	var concat []byte
	concat = append(concat, validFirst...)
	concat = wire.AppendVarint(concat, (1<<4)|uint64(wire.WireSubMsg))
	concat = wire.AppendVarint(concat, uint64(len(corruptInner)))
	concat = append(concat, corruptInner...)

	if err := wire.ValidatePacket(concat); err == nil {
		t.Fatal("precondition failed: expected ValidatePacket(concat) to fail")
	}
	if _, ok := wire.ReadSubMsg(concat, 1); ok {
		t.Fatal("precondition failed: expected ReadSubMsg(concat, 1) to reject corrupt second segment")
	}

	if got, ok := wire.ReadNestedStringBytes(concat, 1, 1); ok {
		t.Fatalf("wire.ReadNestedStringBytes succeeded on packet with corrupt second sub-message segment: got (%q, true), want (_, false)", got)
	}
}
