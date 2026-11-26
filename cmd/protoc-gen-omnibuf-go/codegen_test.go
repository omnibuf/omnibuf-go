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

func TestFlatScalarFastPath(t *testing.T) {
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	f := func(name string, num int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{
			Name:   proto.String(name),
			Number: proto.Int32(num),
			Label:  opt.Enum(),
			Type:   typ.Enum(),
		}
	}
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("omnibuf/audit/flat.proto"),
		Package: proto.String("omnibuf.audit"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/omnibuf/omnibuf-go/omnibufproto/internal/pb;pb"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("FlatBench"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					f("birth_day", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					f("phone", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					f("siblings", 4, descriptorpb.FieldDescriptorProto_TYPE_INT32),
					f("spouse", 5, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
					f("money", 6, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
				},
			},
			{
				Name: proto.String("ConsecStrings"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("a", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					f("b", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					f("raw", 3, descriptorpb.FieldDescriptorProto_TYPE_BYTES),
					f("wide_flag", 20, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				},
			},
			{
				Name: proto.String("FixedOnly"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("id", 1, descriptorpb.FieldDescriptorProto_TYPE_FIXED64),
					f("score", 2, descriptorpb.FieldDescriptorProto_TYPE_FLOAT),
					f("ok", 3, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				},
			},
		},
	}
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"omnibuf/audit/flat.proto"},
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
	generated := filepath.Join(dir, "flat_wire.pb.go")
	testFile := filepath.Join(dir, "flat_test.go")
	if err := os.WriteFile(generated, []byte(resp.File[0].GetContent()), 0o600); err != nil {
		t.Fatalf("write generated file: %v", err)
	}

	testSource := `package pb

import (
	"bytes"
	"testing"

	"github.com/omnibuf/omnibuf-go"
)

type FlatBench struct {
	Name     string  ` + "`" + `omnibuf:"1,name=name"` + "`" + `
	BirthDay int64   ` + "`" + `omnibuf:"2,name=birth_day"` + "`" + `
	Phone    string  ` + "`" + `omnibuf:"3,name=phone"` + "`" + `
	Siblings int32   ` + "`" + `omnibuf:"4,name=siblings"` + "`" + `
	Spouse   bool    ` + "`" + `omnibuf:"5,name=spouse"` + "`" + `
	Money    float64 ` + "`" + `omnibuf:"6,name=money"` + "`" + `
}

type ConsecStrings struct {
	A        string ` + "`" + `omnibuf:"1,name=a"` + "`" + `
	B        string ` + "`" + `omnibuf:"2,name=b"` + "`" + `
	Raw      []byte ` + "`" + `omnibuf:"3,name=raw"` + "`" + `
	WideFlag bool   ` + "`" + `omnibuf:"20,name=wide_flag"` + "`" + `
}

type FixedOnly struct {
	Id    uint64  ` + "`" + `omnibuf:"1,name=id,wire=fixed64"` + "`" + `
	Score float32 ` + "`" + `omnibuf:"2,name=score"` + "`" + `
	Ok    bool    ` + "`" + `omnibuf:"3,name=ok"` + "`" + `
}

func TestFlatScalarRoundTripAndEdgeCases(t *testing.T) {
	cases := []FlatBench{
		// Fast path: 8..16 byte ASCII strings, small int32, both bool values.
		{Name: "0123456789abcdef", BirthDay: 1795000000123456789, Phone: "5551234567", Siblings: 3, Spouse: true, Money: 42.5},
		{Name: "abcdefgh", BirthDay: -123456789, Phone: "12345678", Siblings: -5, Spouse: false, Money: -0.25},
		// Slow path: short strings (<8), empty strings, long strings (>16), large int32, non-ASCII UTF-8.
		{Name: "", BirthDay: 0, Phone: "123", Siblings: 0, Spouse: false, Money: 0},
		{Name: "very-long-name-exceeding-sixteen-bytes", BirthDay: 99, Phone: "01234567890123456789", Siblings: 10000, Spouse: true, Money: 1.5},
		{Name: "héllo wörld!", BirthDay: 42, Phone: "日本語テスト", Siblings: -500, Spouse: true, Money: 3.14},
	}

	for idx, tc := range cases {
		// Compare against table-driven omnibuf.Marshal for bit-for-bit wire parity.
		wantWire, err := omnibuf.Marshal(&tc)
		if err != nil {
			t.Fatalf("case %d omnibuf.Marshal: %v", idx, err)
		}
		gotWire := tc.MarshalOmniBufAppend(nil)
		if !bytes.Equal(gotWire, wantWire) {
			t.Fatalf("case %d wire mismatch:\ngot  %x\nwant %x", idx, gotWire, wantWire)
		}

		// Also verify appending onto non-empty dst (both with and without spare capacity).
		prefix := []byte{0xde, 0xad}
		appended := tc.MarshalOmniBufAppend(append([]byte(nil), prefix...))
		if !bytes.Equal(appended, append(append([]byte(nil), prefix...), wantWire...)) {
			t.Fatalf("case %d append-grow mismatch", idx)
		}
		buf := make([]byte, 2, 256)
		copy(buf, prefix)
		appendedCap := tc.MarshalOmniBufAppend(buf)
		if !bytes.Equal(appendedCap, append(append([]byte(nil), prefix...), wantWire...)) {
			t.Fatalf("case %d append-reuse mismatch", idx)
		}

		// Safe unmarshal: mutating input buffer after decode must NOT mutate strings.
		wireCopy := append([]byte(nil), gotWire...)
		out := FlatBench{Name: "stale", BirthDay: 999, Phone: "stale", Siblings: 99, Spouse: !tc.Spouse, Money: 99}
		if err := out.UnmarshalOmniBuf(wireCopy, false); err != nil {
			t.Fatalf("case %d safe unmarshal: %v", idx, err)
		}
		for i := range wireCopy {
			wireCopy[i] = 'X'
		}
		if out != tc {
			t.Fatalf("case %d safe round-trip mismatch (or aliased buffer): got %+v, want %+v", idx, out, tc)
		}

		// Zero-copy unmarshal.
		outZC := FlatBench{Name: "stale", BirthDay: 999, Phone: "stale", Siblings: 99, Spouse: !tc.Spouse, Money: 99}
		if err := outZC.UnmarshalOmniBuf(gotWire, true); err != nil {
			t.Fatalf("case %d zeroCopy unmarshal: %v", idx, err)
		}
		if outZC != tc {
			t.Fatalf("case %d zeroCopy round-trip mismatch: got %+v, want %+v", idx, outZC, tc)
		}

		// Self-describing round-trip (Field #0 hits fallback cleanly).
		sd := tc.MarshalOmniBufSelfDescribingAppend(nil)
		var outSD FlatBench
		if err := outSD.UnmarshalOmniBuf(sd, false); err != nil {
			t.Fatalf("case %d self-describing unmarshal: %v", idx, err)
		}
		if outSD != tc {
			t.Fatalf("case %d self-describing mismatch: got %+v, want %+v", idx, outSD, tc)
		}
	}

	// Invalid UTF-8 in 8..16 byte string must be rejected even on canonical layout.
	valid := cases[0].MarshalOmniBufAppend(nil)
	badUTF8 := append([]byte(nil), valid...)
	badUTF8[2] = 0xff // first byte of Name
	var badOut FlatBench
	if err := badOut.UnmarshalOmniBuf(badUTF8, false); err == nil {
		t.Fatal("expected invalid UTF-8 error for fast-path packet")
	}
	if err := badOut.UnmarshalOmniBuf(badUTF8, true); err == nil {
		t.Fatal("expected invalid UTF-8 error for zeroCopy fast-path packet")
	}

	// Consecutive strings + bytes + 2-byte bool tag.
	for _, wf := range []bool{false, true} {
		cs := ConsecStrings{A: "abcdefgh", B: "0123456789", Raw: []byte{0xff, 0x00, 1, 2, 3, 4, 5, 6, 7}, WideFlag: wf}
		w := cs.MarshalOmniBufAppend(nil)
		wCopy := append([]byte(nil), w...)
		var csOut ConsecStrings
		if err := csOut.UnmarshalOmniBuf(wCopy, false); err != nil {
			t.Fatalf("ConsecStrings safe unmarshal: %v", err)
		}
		wCopy[len(wCopy)-3] ^= 0xff
		if csOut.A != cs.A || csOut.B != cs.B || !bytes.Equal(csOut.Raw, cs.Raw) || csOut.WideFlag != cs.WideFlag {
			t.Fatalf("ConsecStrings mismatch: got %+v, want %+v", csOut, cs)
		}
	}

	// Fixed-only message (no varlen fields).
	fo := FixedOnly{Id: 0xdeadbeefcafebabe, Score: 3.5, Ok: true}
	var foOut FixedOnly
	if err := foOut.UnmarshalOmniBuf(fo.MarshalOmniBufAppend(nil), false); err != nil {
		t.Fatalf("FixedOnly unmarshal: %v", err)
	}
	if foOut != fo {
		t.Fatalf("FixedOnly mismatch: got %+v, want %+v", foOut, fo)
	}
}
`
	if err := os.WriteFile(testFile, []byte(testSource), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if out, err := exec.Command("go", "test", "-race", "-v", generated, testFile).CombinedOutput(); err != nil {
		t.Fatalf("flat scalar fast path test failed:\n%s", out)
	}
}
