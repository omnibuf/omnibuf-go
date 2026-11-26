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

// runGoInRoot executes a go command in the repository root module so tests of
// generated code always compile against the in-tree github.com/omnibuf/omnibuf-go
// and wire packages without requiring a replace directive in this submodule.
func runGoInRoot(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = filepath.Join("..", "..")
	return cmd.CombinedOutput()
}

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

	if out, err := runGoInRoot(t, "test", "-v", generated, testFile); err != nil {
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
	if out, err := runGoInRoot(t, "test", "-v", generated, testFile); err != nil {
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
	if out, err := runGoInRoot(t, "build", generated); err != nil {
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
			{
				// Covers all scalar kinds, 2-byte field numbers, single string field,
				// and a trailing variable-length field (tailBytes == 0).
				Name: proto.String("AllFlatTypes"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("u32", 1, descriptorpb.FieldDescriptorProto_TYPE_UINT32),
					f("u64", 2, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
					f("i32", 3, descriptorpb.FieldDescriptorProto_TYPE_INT32),
					f("s32", 4, descriptorpb.FieldDescriptorProto_TYPE_SINT32),
					f("i64", 5, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					f("s64", 6, descriptorpb.FieldDescriptorProto_TYPE_SINT64),
					f("f32", 7, descriptorpb.FieldDescriptorProto_TYPE_FIXED32),
					f("sf32", 8, descriptorpb.FieldDescriptorProto_TYPE_SFIXED32),
					f("f64", 9, descriptorpb.FieldDescriptorProto_TYPE_FIXED64),
					f("sf64", 10, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64),
					f("fl", 11, descriptorpb.FieldDescriptorProto_TYPE_FLOAT),
					f("db", 12, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
					f("flag", 100, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
					f("payload", 200, descriptorpb.FieldDescriptorProto_TYPE_BYTES),
					f("label", 500, descriptorpb.FieldDescriptorProto_TYPE_STRING),
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
	"math"
	"strings"
	"testing"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/wire"
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

type AllFlatTypes struct {
	U32     uint32  ` + "`" + `omnibuf:"1,name=u32"` + "`" + `
	U64     uint64  ` + "`" + `omnibuf:"2,name=u64"` + "`" + `
	I32     int32   ` + "`" + `omnibuf:"3,name=i32"` + "`" + `
	S32     int32   ` + "`" + `omnibuf:"4,name=s32"` + "`" + `
	I64     int64   ` + "`" + `omnibuf:"5,name=i64"` + "`" + `
	S64     int64   ` + "`" + `omnibuf:"6,name=s64"` + "`" + `
	F32     uint32  ` + "`" + `omnibuf:"7,name=f32,wire=fixed32"` + "`" + `
	Sf32    int32   ` + "`" + `omnibuf:"8,name=sf32,wire=sfixed32"` + "`" + `
	F64     uint64  ` + "`" + `omnibuf:"9,name=f64,wire=fixed64"` + "`" + `
	Sf64    int64   ` + "`" + `omnibuf:"10,name=sf64,wire=sfixed64"` + "`" + `
	Fl      float32 ` + "`" + `omnibuf:"11,name=fl"` + "`" + `
	Db      float64 ` + "`" + `omnibuf:"12,name=db"` + "`" + `
	Flag    bool    ` + "`" + `omnibuf:"100,name=flag"` + "`" + `
	Payload []byte  ` + "`" + `omnibuf:"200,name=payload"` + "`" + `
	Label   string  ` + "`" + `omnibuf:"500,name=label"` + "`" + `
}

func TestFlatScalarRoundTripAndEdgeCases(t *testing.T) {
	cases := []FlatBench{
		// 8..64 byte ASCII strings (inline word copy path), 9-byte int64, 1-byte int32.
		{Name: "0123456789abcdef", BirthDay: 1795000000123456789, Phone: "5551234567", Siblings: 3, Spouse: true, Money: 42.5},
		{Name: "abcdefgh", BirthDay: -123456789, Phone: "12345678", Siblings: -5, Spouse: false, Money: -0.25},
		// Empty and <8 byte strings, zero values.
		{Name: "", BirthDay: 0, Phone: "123", Siblings: 0, Spouse: false, Money: 0},
		{Name: "a", BirthDay: 63, Phone: "", Siblings: -64, Spouse: true, Money: 1.0},
		// 2-byte and 4-byte varint widths, exact 64-byte string boundary, and 65-byte (>64) fallback.
		{Name: strings.Repeat("a", 64), BirthDay: 8000, Phone: strings.Repeat("b", 65), Siblings: 10000, Spouse: true, Money: 1.5},
		// Total string length > 256 (bypasses safe string slab).
		{Name: strings.Repeat("x", 200), BirthDay: math.MinInt64, Phone: strings.Repeat("y", 100), Siblings: math.MaxInt32, Spouse: false, Money: -99.75},
		// Non-ASCII valid UTF-8 (hits IsASCII -> decodeOmniBuf fallback).
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

		// Buffer reuse paths:
		// 1) cap >= maxNeed (fast reuse skipping VarintSize)
		// 2) exact need <= cap < maxNeed (tight reuse branch)
		// 3) non-empty prefix with grow
		// 4) non-empty prefix with spare capacity
		for _, capHint := range []int{len(wantWire), len(wantWire) + 1, len(wantWire) + 64} {
			reused := tc.MarshalOmniBufAppend(make([]byte, 0, capHint))
			if !bytes.Equal(reused, wantWire) {
				t.Fatalf("case %d reuse(cap=%d) mismatch", idx, capHint)
			}
		}
		prefix := []byte{0xde, 0xad}
		appended := tc.MarshalOmniBufAppend(append([]byte(nil), prefix...))
		if !bytes.Equal(appended, append(append([]byte(nil), prefix...), wantWire...)) {
			t.Fatalf("case %d append-grow mismatch", idx)
		}
		buf := make([]byte, 2, len(wantWire)+128)
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

	// Nil receiver error.
	var nilFB *FlatBench
	if err := nilFB.UnmarshalOmniBuf(nil, false); err == nil {
		t.Fatal("expected error for nil receiver")
	}

	// Every byte prefix must match table-driven omnibuf.Unmarshal (succeeding at
	// complete field boundaries and returning an error mid-field, never panicking).
	valid := cases[0].MarshalOmniBufAppend(nil)
	for cut := 0; cut <= len(valid); cut++ {
		prefix := valid[:cut]
		ref := FlatBench{Name: "stale", BirthDay: 99, Phone: "stale", Siblings: 9, Spouse: true, Money: 9}
		refErr := omnibuf.Unmarshal(prefix, &ref)
		got := FlatBench{Name: "stale", BirthDay: 99, Phone: "stale", Siblings: 9, Spouse: true, Money: 9}
		gotErr := got.UnmarshalOmniBuf(prefix, false)
		if (gotErr != nil) != (refErr != nil) {
			t.Fatalf("prefix %d/%d error mismatch: gotErr=%v, refErr=%v", cut, len(valid), gotErr, refErr)
		}
		if gotErr == nil && got != ref {
			t.Fatalf("prefix %d/%d value mismatch: got %+v, want %+v", cut, len(valid), got, ref)
		}
	}

	// Trailing garbage must be rejected.
	withGarbage := append(append([]byte(nil), valid...), 0x00)
	var garbageOut FlatBench
	if err := garbageOut.UnmarshalOmniBuf(withGarbage, false); err == nil {
		t.Fatal("expected error for trailing garbage byte")
	}

	// Partial / out-of-order message must clear unseen fields via fallback.
	enc := wire.NewEncoder(32)
	enc.WriteFloat64(6, 7.25)
	enc.WriteString(1, "partial")
	partialWire := append([]byte(nil), enc.Finish()...)
	partialOut := FlatBench{Name: "old", BirthDay: 123, Phone: "old", Siblings: 9, Spouse: true, Money: 1}
	if err := partialOut.UnmarshalOmniBuf(partialWire, false); err != nil {
		t.Fatalf("partial unmarshal: %v", err)
	}
	wantPartial := FlatBench{Name: "partial", Money: 7.25}
	if partialOut != wantPartial {
		t.Fatalf("partial mismatch: got %+v, want %+v", partialOut, wantPartial)
	}

	// Invalid UTF-8 in short, medium (8..64), and long (>64) strings must be rejected.
	for _, s := range []string{"abc", "0123456789abcdef", strings.Repeat("z", 80)} {
		w := (&FlatBench{Name: s, Phone: "5551234567"}).MarshalOmniBufAppend(nil)
		// Corrupt the last byte of Name.
		nameLenBytes := wire.VarintSize(uint64(len(s)))
		w[1+nameLenBytes+len(s)-1] = 0xff
		var badOut FlatBench
		if err := badOut.UnmarshalOmniBuf(w, false); err == nil {
			t.Fatalf("expected invalid UTF-8 error (safe, len=%d)", len(s))
		}
		if err := badOut.UnmarshalOmniBuf(w, true); err == nil {
			t.Fatalf("expected invalid UTF-8 error (zeroCopy, len=%d)", len(s))
		}
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

	// Fixed-only message (no varint or varlen fields).
	fo := FixedOnly{Id: 0xdeadbeefcafebabe, Score: 3.5, Ok: true}
	var foOut FixedOnly
	if err := foOut.UnmarshalOmniBuf(fo.MarshalOmniBufAppend(nil), false); err != nil {
		t.Fatalf("FixedOnly unmarshal: %v", err)
	}
	if foOut != fo {
		t.Fatalf("FixedOnly mismatch: got %+v, want %+v", foOut, fo)
	}

	// AllFlatTypes: every scalar kind, 2-byte tags, single string field, tailBytes == 0.
	allCases := []AllFlatTypes{
		{
			U32: 127, U64: 16383, I32: -100, S32: 500000, I64: -900000000000, S64: math.MaxInt64,
			F32: 0x12345678, Sf32: -123456, F64: 0xfeedfacecafebeef, Sf64: -9876543210,
			Fl: -12.5, Db: 98765.4321, Flag: true,
			Payload: []byte("0123456789abcdef"), Label: "single-string-field",
		},
		{
			U32: math.MaxUint32, U64: math.MaxUint64, I32: math.MinInt32, S32: math.MaxInt32,
			I64: math.MinInt64, S64: 0, Flag: false,
			Payload: bytes.Repeat([]byte{0xab}, 70), Label: "café-🚀",
		},
	}
	for idx, tc := range allCases {
		wantWire, err := omnibuf.Marshal(&tc)
		if err != nil {
			t.Fatalf("AllFlatTypes[%d] omnibuf.Marshal: %v", idx, err)
		}
		gotWire := tc.MarshalOmniBufAppend(nil)
		if !bytes.Equal(gotWire, wantWire) {
			t.Fatalf("AllFlatTypes[%d] wire mismatch:\ngot  %x\nwant %x", idx, gotWire, wantWire)
		}
		for _, zc := range []bool{false, true} {
			wireCopy := append([]byte(nil), gotWire...)
			var out AllFlatTypes
			if err := out.UnmarshalOmniBuf(wireCopy, zc); err != nil {
				t.Fatalf("AllFlatTypes[%d] unmarshal(zc=%v): %v", idx, zc, err)
			}
			if !zc {
				for i := range wireCopy {
					wireCopy[i] ^= 0xff
				}
			}
			if out.U32 != tc.U32 || out.U64 != tc.U64 || out.I32 != tc.I32 || out.S32 != tc.S32 ||
				out.I64 != tc.I64 || out.S64 != tc.S64 || out.F32 != tc.F32 || out.Sf32 != tc.Sf32 ||
				out.F64 != tc.F64 || out.Sf64 != tc.Sf64 || out.Fl != tc.Fl || out.Db != tc.Db ||
				out.Flag != tc.Flag || !bytes.Equal(out.Payload, tc.Payload) || out.Label != tc.Label {
				t.Fatalf("AllFlatTypes[%d] mismatch(zc=%v):\ngot  %+v\nwant %+v", idx, zc, out, tc)
			}
		}
	}
}
`
	if err := os.WriteFile(testFile, []byte(testSource), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if out, err := runGoInRoot(t, "test", "-race", "-v", generated, testFile); err != nil {
		t.Fatalf("flat scalar fast path test failed:\n%s", out)
	}
}
