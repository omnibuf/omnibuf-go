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

// TestAuditGO1_OutOfOrderFieldNumbersInOpaqueSplice isolates GO-1:
// When a .proto message declares fields out of ascending field-number order
// (e.g. `string title = 10; uint32 severity = 2;`), generateOpaqueMessage sets
// `maxFnum` from the last declared field (`2`) instead of the maximum field
// number (`10`). As a result, SpliceFields and TrySpliceFields stop scanning
// newly dirtied bits at `2` and silently drop field `#10`.
func TestAuditGO1_OutOfOrderFieldNumbersInOpaqueSplice(t *testing.T) {
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
