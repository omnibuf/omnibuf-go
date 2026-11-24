package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// TestOpaqueSpliceOutOfOrderFieldNumbers verifies that when a .proto message
// declares fields out of ascending field-number order, the generated Opaque
// type tracks the true maximum field number so SpliceFields and TrySpliceFields
// do not drop mutated high-numbered fields.
func TestOpaqueSpliceOutOfOrderFieldNumbers(t *testing.T) {
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("omnibuf/audit/alert.proto"),
		Package: proto.String("omnibuf.audit"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/omnibuf/omnibuf-go/omnibufproto/internal/pb;pb"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Alert"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("title"),
						Number: proto.Int32(10),
						Label:  opt.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
					{
						Name:   proto.String("severity"),
						Number: proto.Int32(2),
						Label:  opt.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum(),
					},
				},
			},
		},
	}
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"omnibuf/audit/alert.proto"},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fdp},
	}

	gen, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.New: %v", err)
	}
	generateFile(gen, gen.Files[0])
	resp := gen.Response()
	if resp.GetError() != "" {
		t.Fatalf("generateFile error: %s", resp.GetError())
	}

	dir := t.TempDir()
	generated := filepath.Join(dir, "alert_omnibuf.pb.go")
	testFile := filepath.Join(dir, "alert_test.go")
	if err := os.WriteFile(generated, []byte(resp.File[0].GetContent()), 0o600); err != nil {
		t.Fatalf("write generated file: %v", err)
	}

	testSource := `package pb

import (
	"testing"

	"github.com/omnibuf/omnibuf-go/wire"
)

type Alert struct {
	Title    string
	Severity uint32
}

func TestOpaqueAlertOutOfOrderSplice(t *testing.T) {
	enc := wire.NewEncoder(16)
	enc.WriteUint(2, 5)
	raw := append([]byte(nil), enc.Finish()...)

	var op OpaqueAlert
	if err := op.Unmarshal(raw); err != nil {
		t.Fatalf("OpaqueAlert.Unmarshal: %v", err)
	}
	op.SetTitle("disk-full")

	spliced := op.MarshalAppend(nil)
	if got, ok := wire.ReadString(spliced, 10); !ok || got != "disk-full" {
		t.Errorf("OpaqueAlert.MarshalAppend dropped mutated field #10: got (%q, %v), want (\"disk-full\", true)", got, ok)
	}

	trySpliced, err := op.Marshal()
	if err != nil {
		t.Fatalf("OpaqueAlert.Marshal: %v", err)
	}
	if got, ok := wire.ReadString(trySpliced, 10); !ok || got != "disk-full" {
		t.Errorf("OpaqueAlert.Marshal (TrySpliceFields) dropped mutated field #10: got (%q, %v), want (\"disk-full\", true)", got, ok)
	}
}
`
	if err := os.WriteFile(testFile, []byte(testSource), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	if out, err := exec.Command("go", "test", "-v", generated, testFile).CombinedOutput(); err != nil {
		t.Fatalf("OpaqueAlert splice test failed:\n%s", out)
	}
}

func TestMixedOpaqueAndNonOpaqueChildCodegen(t *testing.T) {
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	rep := descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("omnibuf/audit/mixed.proto"),
		Package: proto.String("omnibuf.audit"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/omnibuf/omnibuf-go/omnibufproto/internal/pb;pb"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("EmptyChild"),
			},
			{
				Name: proto.String("WideChild"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("big"), Number: proto.Int32(70), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum()},
				},
			},
			{
				Name: proto.String("ParentOfEmpty"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("id"), Number: proto.Int32(1), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum()},
					{Name: proto.String("child"), Number: proto.Int32(2), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".omnibuf.audit.EmptyChild")},
				},
			},
			{
				Name: proto.String("ParentOfWideList"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("id"), Number: proto.Int32(1), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum()},
					{Name: proto.String("items"), Number: proto.Int32(2), Label: rep.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".omnibuf.audit.WideChild")},
				},
			},
			{
				Name: proto.String("RecursiveNode"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("id"), Number: proto.Int32(1), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum()},
					{Name: proto.String("next"), Number: proto.Int32(2), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".omnibuf.audit.RecursiveNode")},
				},
			},
			{
				Name: proto.String("CycleA"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("b"), Number: proto.Int32(1), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".omnibuf.audit.CycleB")},
				},
			},
			{
				Name: proto.String("CycleB"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("a"), Number: proto.Int32(1), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".omnibuf.audit.CycleA")},
					{Name: proto.String("overflow"), Number: proto.Int32(70), Label: opt.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum()},
				},
			},
		},
	}
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"omnibuf/audit/mixed.proto"},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fdp},
	}

	gen, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.New: %v", err)
	}
	generateFile(gen, gen.Files[0])
	resp := gen.Response()
	if resp.GetError() != "" {
		t.Fatalf("generateFile error: %s", resp.GetError())
	}

	dir := t.TempDir()
	generated := filepath.Join(dir, "mixed_wire.pb.go")
	testFile := filepath.Join(dir, "mixed_test.go")
	if err := os.WriteFile(generated, []byte(resp.File[0].GetContent()), 0o600); err != nil {
		t.Fatalf("write generated file: %v", err)
	}

	testSource := `package pb

import "testing"

type EmptyChild struct{}
type WideChild struct {
	Big uint32
}
type ParentOfEmpty struct {
	Id    uint32
	Child *EmptyChild
}
type ParentOfWideList struct {
	Id    uint32
	Items []*WideChild
}
type RecursiveNode struct {
	Id   uint32
	Next *RecursiveNode
}
type CycleA struct {
	B *CycleB
}
type CycleB struct {
	A        *CycleA
	Overflow uint32
}

func TestMixedCompileAndOpaqueRecursive(t *testing.T) {
	in := &RecursiveNode{Id: 1, Next: &RecursiveNode{Id: 2}}
	raw := in.MarshalOmniBufAppend(nil)
	var op OpaqueRecursiveNode
	if err := op.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}
	if op.GetId() != 1 || op.GetNext().GetId() != 2 {
		t.Fatalf("unexpected opaque recursive values: %d, %d", op.GetId(), op.GetNext().GetId())
	}
	p := &ParentOfWideList{Id: 7, Items: []*WideChild{{Big: 99}}}
	var pOut ParentOfWideList
	if err := pOut.UnmarshalOmniBuf(p.MarshalOmniBufAppend(nil), false); err != nil {
		t.Fatal(err)
	}
	if pOut.Id != 7 || len(pOut.Items) != 1 || pOut.Items[0].Big != 99 {
		t.Fatalf("unexpected ParentOfWideList round-trip: %+v", pOut)
	}
}
`
	if err := os.WriteFile(testFile, []byte(testSource), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if out, err := exec.Command("go", "test", "-v", generated, testFile).CombinedOutput(); err != nil {
		t.Fatalf("mixed opaque/non-opaque generated code failed:\n%s", out)
	}
}

func TestEmptyMessageFreeProtoNoUnusedImports(t *testing.T) {
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("omnibuf/audit/enum_only.proto"),
		Package: proto.String("omnibuf.audit"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/omnibuf/omnibuf-go/omnibufproto/internal/pb;pb"),
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{
			{
				Name: proto.String("Status"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					{Name: proto.String("STATUS_UNSPECIFIED"), Number: proto.Int32(0)},
				},
			},
		},
	}
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"omnibuf/audit/enum_only.proto"},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fdp},
	}

	gen, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.New: %v", err)
	}
	generateFile(gen, gen.Files[0])
	resp := gen.Response()
	if resp.GetError() != "" {
		t.Fatalf("generateFile error: %s", resp.GetError())
	}

	dir := t.TempDir()
	generated := filepath.Join(dir, "enum_only_wire.pb.go")
	if err := os.WriteFile(generated, []byte(resp.File[0].GetContent()), 0o600); err != nil {
		t.Fatalf("write generated file: %v", err)
	}
	if out, err := exec.Command("go", "build", generated).CombinedOutput(); err != nil {
		t.Fatalf("empty/message-free generated file failed to compile:\n%s\ncontent:\n%s", out, resp.File[0].GetContent())
	}
}
