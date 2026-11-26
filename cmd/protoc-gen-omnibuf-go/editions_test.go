package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestSetSupportedFeatures(t *testing.T) {
	req := peerProtoRequest()
	gen, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.New: %v", err)
	}
	setSupportedFeatures(gen)
	resp := gen.Response()
	wantFeatures := uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL | pluginpb.CodeGeneratorResponse_FEATURE_SUPPORTS_EDITIONS)
	if resp.GetSupportedFeatures() != wantFeatures {
		t.Fatalf("SupportedFeatures = %#x, want %#x", resp.GetSupportedFeatures(), wantFeatures)
	}
	if resp.GetMinimumEdition() != int32(descriptorpb.Edition_EDITION_2023) {
		t.Fatalf("MinimumEdition = %d, want %d", resp.GetMinimumEdition(), descriptorpb.Edition_EDITION_2023)
	}
	if resp.GetMaximumEdition() != int32(descriptorpb.Edition_EDITION_2026) {
		t.Fatalf("MaximumEdition = %d, want %d", resp.GetMaximumEdition(), descriptorpb.Edition_EDITION_2026)
	}
}

func buildEditionTestPlugins(t *testing.T) (protocPath, protocGenGo, protocGenOmnibuf string) {
	t.Helper()
	var err error
	protocPath, err = exec.LookPath("protoc")
	if err != nil {
		t.Skipf("protoc not found in PATH: %v", err)
	}
	binDir := t.TempDir()
	protocGenGo = filepath.Join(binDir, "protoc-gen-go")
	protocGenOmnibuf = filepath.Join(binDir, "protoc-gen-omnibuf-go")

	if out, err := exec.Command("go", "build", "-o", protocGenGo, "google.golang.org/protobuf/cmd/protoc-gen-go").CombinedOutput(); err != nil {
		t.Fatalf("build protoc-gen-go: %v\n%s", err, out)
	}
	if out, err := exec.Command("go", "build", "-o", protocGenOmnibuf, ".").CombinedOutput(); err != nil {
		t.Fatalf("build protoc-gen-omnibuf-go: %v\n%s", err, out)
	}
	return protocPath, protocGenGo, protocGenOmnibuf
}

func runProtocEdition(t *testing.T, protocPath, protocGenGo, protocGenOmnibuf, protoDir, outDir string, protoFiles ...string) ([]byte, error) {
	t.Helper()
	args := []string{
		"-I" + protoDir,
		"--plugin=protoc-gen-go=" + protocGenGo,
		"--plugin=protoc-gen-omnibuf-go=" + protocGenOmnibuf,
		"--go_out=" + outDir,
		"--go_opt=paths=source_relative",
		"--omnibuf-go_out=" + outDir,
		"--omnibuf-go_opt=paths=source_relative",
	}
	args = append(args, protoFiles...)
	return exec.Command(protocPath, args...).CombinedOutput()
}

func TestEditions2023_2024_2026_RealProtocE2E(t *testing.T) {
	protocPath, protocGenGo, protocGenOmnibuf := buildEditionTestPlugins(t)

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	omnibufProtoDir := filepath.Join(repoRoot, "omnibufproto")

	workDir := t.TempDir()
	protoDir := filepath.Join(workDir, "protos")
	pkgDir := filepath.Join(workDir, "edtest")
	if err := os.MkdirAll(protoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	protoSources := map[string]string{
		"ed2023.proto": `edition = "2023";
package omnibuf.ed2023;

import "google/protobuf/descriptor.proto";
import "google/protobuf/go_features.proto";

option go_package = "example.com/edtest;edtest";
option features.(pb.go).api_level = API_OPEN;
option features.json_format = LEGACY_BEST_EFFORT;

extend google.protobuf.FieldOptions {
  string omni_note = 50001;
}

enum Status2023 {
  STATUS2023_UNSPECIFIED = 0;
  STATUS2023_ACTIVE = 1;
  STATUS2023_PAUSED = 2;
}

message Child2023 {
  int32 id = 1 [default = 0];
  string label = 2 [default = "", (omni_note) = "custom-opt-ok"];
}

message Record2023 {
  int32 exp_i32 = 1 [default = 0];
  double exp_f64 = 2 [default = 0.0];
  bool exp_bool = 3 [default = false];
  string exp_str = 4 [default = ""];
  bytes exp_bytes = 5 [default = ""];
  Status2023 exp_status = 6 [default = STATUS2023_UNSPECIFIED];

  int32 imp_i32 = 7 [features.field_presence = IMPLICIT];
  bool imp_bool = 8 [features.field_presence = IMPLICIT];

  repeated int32 packed_nums = 9 [features.repeated_field_encoding = PACKED];
  repeated int32 expanded_nums = 10 [features.repeated_field_encoding = EXPANDED];
  repeated Child2023 children_lp = 11;
  repeated Child2023 children_delim = 12 [features.message_encoding = DELIMITED];

  map<string, int32> scores = 13;
  map<string, Child2023> child_map = 14;

  Child2023 child_lp = 15;
  Child2023 child_delim = 16 [features.message_encoding = DELIMITED];

  message Nested2023 {
    string detail = 1;
    int32 count = 2;
  }
  Nested2023 nested = 17;

  oneof choice {
    int32 choice_num = 18;
    bool choice_flag = 19;
    string choice_text = 20;
    Child2023 choice_child = 21;
  }
}

message DelimitedPair2023 {
  Child2023 single = 1 [features.message_encoding = DELIMITED];
  repeated Child2023 items = 2 [features.message_encoding = DELIMITED];
}

message LengthPrefixedPair2023 {
  Child2023 single = 1;
  repeated Child2023 items = 2;
}
`,
		"ed2023_implicit.proto": `edition = "2023";
package omnibuf.ed2023imp;

import "google/protobuf/go_features.proto";

option go_package = "example.com/edtest;edtest";
option features.(pb.go).api_level = API_OPEN;
option features.field_presence = IMPLICIT;

message ImplicitFile2023 {
  int32 file_imp_i32 = 1;
  bool file_imp_bool = 2;
  int32 field_exp_i32 = 3 [features.field_presence = EXPLICIT];
  oneof pick {
    int32 pick_i32 = 4;
    bool pick_bool = 5;
    string pick_str = 6;
  }
}
`,
		"ed2024.proto": `edition = "2024";
package omnibuf.ed2024;

import "google/protobuf/go_features.proto";

option go_package = "example.com/edtest;edtest";
option features.enforce_naming_style = STYLE2024;
option features.default_symbol_visibility = EXPORT_TOP_LEVEL;

enum Mode2024 {
  MODE2024_UNSPECIFIED = 0;
  MODE2024_FAST = 1;
}

message Child2024 {
  int32 id = 1;
  string name = 2;
}

message OpaqueRecord2024 {
  int32 exp_i32 = 1;
  double exp_f64 = 2;
  bool exp_bool = 3;
  string exp_str = 4;
  bytes exp_bytes = 5;
  Mode2024 mode = 6;
  int32 imp_i32 = 7 [features.field_presence = IMPLICIT];

  repeated int32 packed_nums = 8 [features.repeated_field_encoding = PACKED];
  repeated int32 expanded_nums = 9 [features.repeated_field_encoding = EXPANDED];
  map<string, int32> scores = 10;
  map<string, Child2024> child_map = 11;

  Child2024 child_lp = 12;
  Child2024 child_delim = 13 [features.message_encoding = DELIMITED];
  repeated Child2024 children_delim = 14 [features.message_encoding = DELIMITED];

  message Inner2024 {
    string tag = 1;
  }
  Inner2024 inner = 15;

  oneof branch {
    int32 branch_i32 = 16;
    bool branch_bool = 17;
    string branch_str = 18;
  }
}

message OpenRecord2024 {
  option features.(pb.go).api_level = API_OPEN;
  int32 a = 1;
  string b = 2;
}

message HybridRecord2024 {
  option features.(pb.go).api_level = API_HYBRID;
  int32 id = 1;
  string note = 2;
}
`,
		"ed2026.proto": `edition = "2026";
package omnibuf.ed2026;

import "google/protobuf/go_features.proto";

option go_package = "example.com/edtest;edtest";
option features.enforce_naming_style = STYLE2026;
option features.default_symbol_visibility = STRICT;

export enum Kind2026 {
  KIND2026_UNSPECIFIED = 0;
  KIND2026_PRIMARY = 1;
}

local message LocalLeaf2026 {
  int32 id = 1;
  string label = 2;
}

export message OpaqueRecord2026 {
  option features.enforce_proto_limits = PROTO_LIMITS2026;

  int32 exp_i32 = 1;
  double exp_f64 = 2;
  bool exp_bool = 3;
  string exp_str = 4;
  bytes exp_bytes = 5;
  Kind2026 kind = 6;
  int32 imp_i32 = 7 [features.field_presence = IMPLICIT];

  repeated int32 packed_nums = 8 [features.repeated_field_encoding = PACKED];
  repeated int32 expanded_nums = 9 [features.repeated_field_encoding = EXPANDED];
  map<string, int32> scores = 10;
  map<string, LocalLeaf2026> leaf_map = 11;

  LocalLeaf2026 leaf_lp = 12;
  LocalLeaf2026 leaf_delim = 13 [features.message_encoding = DELIMITED];
  repeated LocalLeaf2026 leaves_delim = 14 [features.message_encoding = DELIMITED];

  message Detail2026 {
    string info = 1;
  }
  Detail2026 detail = 15;

  oneof payload {
    int32 payload_i32 = 16;
    bool payload_bool = 17;
    string payload_str = 18;
  }
}

export message OpenRecord2026 {
  option features.(pb.go).api_level = API_OPEN;
  int32 value = 1;
  string name = 2;
  LocalLeaf2026 leaf_delim = 3 [features.message_encoding = DELIMITED];
}

export message DelimitedPair2026 {
  LocalLeaf2026 single = 1 [features.message_encoding = DELIMITED];
  repeated LocalLeaf2026 items = 2 [features.message_encoding = DELIMITED];
}

export message LengthPrefixedPair2026 {
  LocalLeaf2026 single = 1;
  repeated LocalLeaf2026 items = 2;
}
`,
		"ed2026_enum_only.proto": `edition = "2026";
package omnibuf.ed2026enum;

option go_package = "example.com/edtest;edtest";

export enum Standalone2026 {
  STANDALONE2026_UNSPECIFIED = 0;
  STANDALONE2026_OK = 1;
}
`,
	}

	var protoFiles []string
	for name, src := range protoSources {
		p := filepath.Join(protoDir, name)
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		protoFiles = append(protoFiles, p)
	}

	if out, err := runProtocEdition(t, protocPath, protocGenGo, protocGenOmnibuf, protoDir, pkgDir, protoFiles...); err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, out)
	}

	omnibufGoVer := "v0.2.7"
	if protoModBytes, err := os.ReadFile(filepath.Join(omnibufProtoDir, "go.mod")); err == nil {
		for line := range strings.SplitSeq(string(protoModBytes), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "github.com/omnibuf/omnibuf-go" && !strings.HasPrefix(fields[1], "=>") {
				omnibufGoVer = fields[1]
				break
			}
		}
	}

	goMod := fmt.Sprintf(`module example.com/edtest

go 1.26

replace (
	github.com/omnibuf/omnibuf-go => %s
	github.com/omnibuf/omnibuf-go/omnibufproto => %s
)

require (
	github.com/omnibuf/omnibuf-go %s
	github.com/omnibuf/omnibuf-go/omnibufproto v0.0.0-00010101000000-000000000000
	google.golang.org/protobuf v1.37.0-rc.1
)
`, repoRoot, omnibufProtoDir, omnibufGoVer)
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	goSum, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatalf("read go.sum: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "go.sum"), goSum, 0o600); err != nil {
		t.Fatal(err)
	}

	e2eTestSrc := `package edtest

import (
	"bytes"
	"testing"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/omnibufproto"
	"github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/proto"
)

func TestEdition2023OpenAPIFeatures(t *testing.T) {
	// Absent vs explicit zero values must differ on wire and preserve presence.
	absent := &Record2023{}
	explicitZero := &Record2023{
		ExpI32:    proto.Int32(0),
		ExpF64:    proto.Float64(0.0),
		ExpBool:   proto.Bool(false),
		ExpStr:    proto.String(""),
		ExpBytes:  []byte{},
		ExpStatus: Status2023_STATUS2023_UNSPECIFIED.Enum(),
	}
	absentWire, err := absent.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal absent: %v", err)
	}
	zeroWire, err := explicitZero.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal explicitZero: %v", err)
	}
	if bytes.Equal(absentWire, zeroWire) {
		t.Fatalf("absent and explicit-zero wire bytes matched unexpectedly: %x", absentWire)
	}
	if v, ok := wire.ReadBool(zeroWire, 3); !ok || v {
		t.Fatalf("expected WireBoolFalse for explicit false, got (%v, %v)", v, ok)
	}
	if _, _, ok := wire.LocateField(absentWire, 3); ok {
		t.Fatalf("absent explicit bool should not appear on wire")
	}

	var gotAbsent Record2023
	if err := gotAbsent.UnmarshalOmniBuf(absentWire, false); err != nil {
		t.Fatalf("unmarshal absent: %v", err)
	}
	if gotAbsent.ExpI32 != nil || gotAbsent.ExpF64 != nil || gotAbsent.ExpBool != nil || gotAbsent.ExpStr != nil || gotAbsent.ExpBytes != nil || gotAbsent.ExpStatus != nil {
		t.Fatalf("absent fields unexpectedly set: %+v", &gotAbsent)
	}

	var gotZero Record2023
	if err := gotZero.UnmarshalOmniBuf(zeroWire, false); err != nil {
		t.Fatalf("unmarshal explicitZero: %v", err)
	}
	if gotZero.ExpI32 == nil || *gotZero.ExpI32 != 0 ||
		gotZero.ExpF64 == nil || *gotZero.ExpF64 != 0.0 ||
		gotZero.ExpBool == nil || *gotZero.ExpBool ||
		gotZero.ExpStr == nil || *gotZero.ExpStr != "" ||
		gotZero.ExpBytes == nil || len(gotZero.ExpBytes) != 0 ||
		gotZero.ExpStatus == nil || *gotZero.ExpStatus != Status2023_STATUS2023_UNSPECIFIED {
		t.Fatalf("explicit zero presence lost: %+v", &gotZero)
	}

	// Full payload with unknown open enum, packed/expanded repeated, maps, nested, delimited, oneof.
	unknownStatus := Status2023(99)
	full := &Record2023{
		ExpI32:        proto.Int32(-7),
		ExpF64:        proto.Float64(3.25),
		ExpBool:       proto.Bool(true),
		ExpStr:        proto.String("ed2023"),
		ExpBytes:      []byte{0xde, 0xad},
		ExpStatus:     &unknownStatus,
		ImpI32:        42,
		ImpBool:       true,
		PackedNums:    []int32{1, -2, 3},
		ExpandedNums:  []int32{1, -2, 3},
		ChildrenLp:    []*Child2023{{Id: proto.Int32(1), Label: proto.String("a")}},
		ChildrenDelim: []*Child2023{{Id: proto.Int32(2), Label: proto.String("b")}},
		Scores:        map[string]int32{"b": 2, "a": 1},
		ChildMap:      map[string]*Child2023{"k": {Id: proto.Int32(9), Label: proto.String("m")}},
		ChildLp:       &Child2023{Id: proto.Int32(3), Label: proto.String("lp")},
		ChildDelim:    &Child2023{Id: proto.Int32(4), Label: proto.String("delim")},
		Nested:        &Record2023_Nested2023{Detail: proto.String("inner"), Count: proto.Int32(5)},
		Choice:        &Record2023_ChoiceFlag{ChoiceFlag: false},
	}

	raw1 := full.MarshalOmniBufAppend(nil)
	raw2, err := omnibufproto.MarshalDeterministic(full)
	if err != nil {
		t.Fatalf("omnibufproto.MarshalDeterministic: %v", err)
	}
	raw3, err := omnibuf.MarshalDeterministic(full)
	if err != nil {
		t.Fatalf("omnibuf.MarshalDeterministic: %v", err)
	}
	if !bytes.Equal(raw1, raw2) || !bytes.Equal(raw1, raw3) {
		t.Fatalf("wire bytes differ across MarshalOmniBufAppend / omnibufproto / omnibuf")
	}

	// Packed and expanded repeated fields both encode as WireArray with identical body.
	bPacked, wtP, okP := wire.LocateField(raw1, 9)
	bExpanded, wtE, okE := wire.LocateField(raw1, 10)
	lP, lnP := wire.DecodeVarint(bPacked)
	lE, lnE := wire.DecodeVarint(bExpanded)
	if !okP || !okE || wtP != wire.WireArray || wtE != wire.WireArray || !bytes.Equal(bPacked[:lnP+int(lP)], bExpanded[:lnE+int(lE)]) {
		t.Fatalf("packed vs expanded WireArray mismatch: wt=(%d,%d)", wtP, wtE)
	}

	var out Record2023
	if err := out.UnmarshalOmniBuf(raw1, false); err != nil {
		t.Fatalf("UnmarshalOmniBuf full: %v", err)
	}
	if !proto.Equal(full, &out) {
		t.Fatalf("full round-trip mismatch:\ngot:  %v\nwant: %v", &out, full)
	}
	if out.GetExpStatus() != Status2023(99) {
		t.Fatalf("unknown open enum not preserved: got %d", out.GetExpStatus())
	}

	// Switch oneof branch on reused struct.
	switched := &Record2023{Choice: &Record2023_ChoiceText{ChoiceText: ""}}
	swWire, err := switched.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal switched: %v", err)
	}
	if err := out.UnmarshalOmniBuf(swWire, false); err != nil {
		t.Fatalf("unmarshal switched: %v", err)
	}
	if _, ok := out.Choice.(*Record2023_ChoiceText); !ok || out.GetChoiceText() != "" {
		t.Fatalf("oneof switch to empty string failed: %#v", out.Choice)
	}

	// Nested message generated methods compile and work.
	nIn := &Record2023_Nested2023{Detail: proto.String("direct"), Count: proto.Int32(0)}
	nWire, err := nIn.TryMarshalOmniBufSelfDescribingAppend(nil)
	if err != nil {
		t.Fatalf("nested self-describing: %v", err)
	}
	var nOut Record2023_Nested2023
	if err := nOut.UnmarshalOmniBuf(nWire, true); err != nil {
		t.Fatalf("nested unmarshal: %v", err)
	}
	if !proto.Equal(nIn, &nOut) {
		t.Fatalf("nested round-trip mismatch")
	}

	// DELIMITED vs LENGTH_PREFIXED must emit identical OmniBuf WireSubMsg bytes.
	dp := &DelimitedPair2023{
		Single: &Child2023{Id: proto.Int32(11), Label: proto.String("same")},
		Items:  []*Child2023{{Id: proto.Int32(12), Label: proto.String("x")}, {Id: proto.Int32(13), Label: proto.String("y")}},
	}
	lp := &LengthPrefixedPair2023{
		Single: &Child2023{Id: proto.Int32(11), Label: proto.String("same")},
		Items:  []*Child2023{{Id: proto.Int32(12), Label: proto.String("x")}, {Id: proto.Int32(13), Label: proto.String("y")}},
	}
	dpWire, err := dp.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal delimited: %v", err)
	}
	lpWire, err := lp.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal length-prefixed: %v", err)
	}
	if !bytes.Equal(dpWire, lpWire) {
		t.Fatalf("DELIMITED vs LENGTH_PREFIXED bytes differ:\ndelim: %x\nlp:    %x", dpWire, lpWire)
	}
	var dpOut DelimitedPair2023
	if err := dpOut.UnmarshalOmniBuf(dpWire, false); err != nil {
		t.Fatalf("unmarshal delimited: %v", err)
	}
	if !proto.Equal(dp, &dpOut) {
		t.Fatalf("delimited round-trip mismatch")
	}

	// File-level IMPLICIT with field-level EXPLICIT override and oneof.
	imp := &ImplicitFile2023{
		FileImpI32:  0,
		FileImpBool: false,
		FieldExpI32: proto.Int32(0),
		Pick:        &ImplicitFile2023_PickI32{PickI32: 0},
	}
	impWire, err := imp.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal implicit file: %v", err)
	}
	var impOut ImplicitFile2023
	if err := impOut.UnmarshalOmniBuf(impWire, false); err != nil {
		t.Fatalf("unmarshal implicit file: %v", err)
	}
	if impOut.FieldExpI32 == nil || *impOut.FieldExpI32 != 0 {
		t.Fatalf("field-level EXPLICIT override lost presence")
	}
	if _, ok := impOut.Pick.(*ImplicitFile2023_PickI32); !ok || impOut.GetPickI32() != 0 {
		t.Fatalf("oneof zero branch lost in implicit file")
	}

	// Nil receiver returns error instead of panicking.
	var nilRec *Record2023
	if _, err := nilRec.TryMarshalOmniBufAppend(nil); err == nil {
		t.Fatal("expected error for nil TryMarshalOmniBufAppend")
	}
	if _, err := nilRec.TryMarshalOmniBufSelfDescribingAppend(nil); err == nil {
		t.Fatal("expected error for nil TryMarshalOmniBufSelfDescribingAppend")
	}
	if err := nilRec.UnmarshalOmniBuf(nil, false); err == nil {
		t.Fatal("expected error for nil UnmarshalOmniBuf")
	}
}

func TestEdition2024OpaqueAndOpenAndHybrid(t *testing.T) {
	absent := &OpaqueRecord2024{}
	zero := OpaqueRecord2024_builder{
		ExpI32:   proto.Int32(0),
		ExpF64:   proto.Float64(0.0),
		ExpBool:  proto.Bool(false),
		ExpStr:   proto.String(""),
		ExpBytes: []byte{},
		Mode:     Mode2024_MODE2024_UNSPECIFIED.Enum(),
	}.Build()

	absentWire, err := absent.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal absent 2024: %v", err)
	}
	zeroWire, err := zero.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal zero 2024: %v", err)
	}
	if bytes.Equal(absentWire, zeroWire) {
		t.Fatalf("2024 absent and explicit zero matched")
	}

	var gotZero OpaqueRecord2024
	if err := gotZero.UnmarshalOmniBuf(zeroWire, false); err != nil {
		t.Fatalf("unmarshal zero 2024: %v", err)
	}
	if !gotZero.HasExpI32() || !gotZero.HasExpF64() || !gotZero.HasExpBool() || !gotZero.HasExpStr() || !gotZero.HasExpBytes() || !gotZero.HasMode() {
		t.Fatalf("2024 opaque explicit zero presence lost")
	}

	unknownMode := Mode2024(-5)
	full := OpaqueRecord2024_builder{
		ExpI32:       proto.Int32(77),
		ExpF64:       proto.Float64(-1.5),
		ExpBool:      proto.Bool(true),
		ExpStr:       proto.String("opaque-2024"),
		ExpBytes:     []byte{1, 2, 3},
		Mode:         &unknownMode,
		ImpI32:       12,
		PackedNums:   []int32{5, 6},
		ExpandedNums: []int32{5, 6},
		Scores:       map[string]int32{"z": 9, "a": 1},
		ChildMap: map[string]*Child2024{
			"c1": Child2024_builder{Id: proto.Int32(1), Name: proto.String("one")}.Build(),
		},
		ChildLp:       Child2024_builder{Id: proto.Int32(2), Name: proto.String("lp")}.Build(),
		ChildDelim:    Child2024_builder{Id: proto.Int32(3), Name: proto.String("delim")}.Build(),
		ChildrenDelim: []*Child2024{Child2024_builder{Id: proto.Int32(4), Name: proto.String("d0")}.Build()},
		Inner:         OpaqueRecord2024_Inner2024_builder{Tag: proto.String("nested-opaque")}.Build(),
		BranchBool:    proto.Bool(false),
	}.Build()

	raw, err := full.TryMarshalOmniBufSelfDescribingAppend(nil)
	if err != nil {
		t.Fatalf("self-describing 2024: %v", err)
	}
	blind, err := omnibuf.DecodeBlind(raw)
	if err != nil {
		t.Fatalf("DecodeBlind 2024: %v", err)
	}
	delimMap, ok := blind["child_delim"].(map[string]any)
	if !ok || delimMap["name"] != "delim" {
		t.Fatalf("DecodeBlind child_delim mismatch: %#v", blind["child_delim"])
	}

	var out OpaqueRecord2024
	if err := out.UnmarshalOmniBuf(raw, false); err != nil {
		t.Fatalf("unmarshal 2024: %v", err)
	}
	if !proto.Equal(full, &out) || out.GetMode() != Mode2024(-5) || !out.HasBranchBool() || out.GetBranchBool() {
		t.Fatalf("2024 opaque round-trip mismatch")
	}

	// OpenRecord2024 and HybridRecord2024 compile and round-trip.
	openRec := &OpenRecord2024{A: proto.Int32(0), B: proto.String("open24")}
	openWire, err := omnibufproto.Marshal(openRec)
	if err != nil {
		t.Fatalf("marshal OpenRecord2024: %v", err)
	}
	var openOut OpenRecord2024
	if err := omnibufproto.Unmarshal(openWire, &openOut); err != nil {
		t.Fatalf("unmarshal OpenRecord2024: %v", err)
	}
	if !proto.Equal(openRec, &openOut) {
		t.Fatalf("OpenRecord2024 mismatch")
	}

	hyb := HybridRecord2024_builder{Id: proto.Int32(42), Note: proto.String("hybrid")}.Build()
	hybWire, err := hyb.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal HybridRecord2024: %v", err)
	}
	var hybOut HybridRecord2024
	if err := hybOut.UnmarshalOmniBuf(hybWire, false); err != nil {
		t.Fatalf("unmarshal HybridRecord2024: %v", err)
	}
	if !proto.Equal(hyb, &hybOut) {
		t.Fatalf("HybridRecord2024 mismatch")
	}

	var nilOpaque *OpaqueRecord2024
	if _, err := nilOpaque.TryMarshalOmniBufAppend(nil); err == nil {
		t.Fatal("expected error for nil opaque TryMarshalOmniBufAppend")
	}
}

func TestEdition2026StrictVisibilityOpaqueAndOpen(t *testing.T) {
	unknownKind := Kind2026(1234)
	full := OpaqueRecord2026_builder{
		ExpI32:       proto.Int32(0),
		ExpF64:       proto.Float64(0.0),
		ExpBool:      proto.Bool(false),
		ExpStr:       proto.String(""),
		ExpBytes:     []byte{},
		Kind:         &unknownKind,
		ImpI32:       9,
		PackedNums:   []int32{-1, 0, 1},
		ExpandedNums: []int32{-1, 0, 1},
		Scores:       map[string]int32{"k1": 10, "k2": 20},
		LeafMap: map[string]*LocalLeaf2026{
			"l1": LocalLeaf2026_builder{Id: proto.Int32(1), Label: proto.String("m1")}.Build(),
		},
		LeafLp:      LocalLeaf2026_builder{Id: proto.Int32(2), Label: proto.String("lp")}.Build(),
		LeafDelim:   LocalLeaf2026_builder{Id: proto.Int32(3), Label: proto.String("delim")}.Build(),
		LeavesDelim: []*LocalLeaf2026{LocalLeaf2026_builder{Id: proto.Int32(4), Label: proto.String("d1")}.Build()},
		Detail:      OpaqueRecord2026_Detail2026_builder{Info: proto.String("nested26")}.Build(),
		PayloadI32:  proto.Int32(0),
	}.Build()

	raw, err := full.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal 2026: %v", err)
	}
	var out OpaqueRecord2026
	if err := out.UnmarshalOmniBuf(raw, false); err != nil {
		t.Fatalf("unmarshal 2026: %v", err)
	}
	if !proto.Equal(full, &out) || out.GetKind() != Kind2026(1234) || !out.HasPayloadI32() || out.GetPayloadI32() != 0 {
		t.Fatalf("2026 round-trip mismatch")
	}

	// OpenRecord2026 (API_OPEN in edition 2026).
	open26 := &OpenRecord2026{
		Value:     proto.Int32(0),
		Name:      proto.String("open26"),
		LeafDelim: LocalLeaf2026_builder{Id: proto.Int32(5), Label: proto.String("d5")}.Build(),
	}
	openWire, err := omnibuf.Marshal(open26)
	if err != nil {
		t.Fatalf("omnibuf.Marshal OpenRecord2026: %v", err)
	}
	var openOut OpenRecord2026
	if err := omnibuf.Unmarshal(openWire, &openOut); err != nil {
		t.Fatalf("omnibuf.Unmarshal OpenRecord2026: %v", err)
	}
	if !proto.Equal(open26, &openOut) {
		t.Fatalf("OpenRecord2026 mismatch")
	}

	// DELIMITED vs LENGTH_PREFIXED wire identity in edition 2026.
	dp := DelimitedPair2026_builder{
		Single: LocalLeaf2026_builder{Id: proto.Int32(1), Label: proto.String("a")}.Build(),
		Items:  []*LocalLeaf2026{LocalLeaf2026_builder{Id: proto.Int32(2), Label: proto.String("b")}.Build()},
	}.Build()
	lp := LengthPrefixedPair2026_builder{
		Single: LocalLeaf2026_builder{Id: proto.Int32(1), Label: proto.String("a")}.Build(),
		Items:  []*LocalLeaf2026{LocalLeaf2026_builder{Id: proto.Int32(2), Label: proto.String("b")}.Build()},
	}.Build()
	dpWire, err := dp.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal DelimitedPair2026: %v", err)
	}
	lpWire, err := lp.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("marshal LengthPrefixedPair2026: %v", err)
	}
	if !bytes.Equal(dpWire, lpWire) {
		t.Fatalf("2026 DELIMITED vs LENGTH_PREFIXED bytes differ:\ndelim: %x\nlp:    %x", dpWire, lpWire)
	}
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "editions_e2e_test.go"), []byte(e2eTestSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	// Run go test in default mode and with -tags=protoopaque (validating API_HYBRID).
	for _, extraArgs := range [][]string{
		{"test", "-race", "-v", "./..."},
		{"test", "-tags=protoopaque", "-v", "./..."},
	} {
		cmd := exec.Command("go", extraArgs...)
		cmd.Dir = workDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v failed: %v\n%s", extraArgs, err, out)
		}
	}
}

func TestEditions_PluginRejections(t *testing.T) {
	protocPath, protocGenGo, protocGenOmnibuf := buildEditionTestPlugins(t)

	rejectCases := []struct {
		name        string
		protoSrc    string
		wantContain string
	}{
		{
			name: "closed_enum_top_level",
			protoSrc: `edition = "2023";
package rej.closed_top;
option go_package = "example.com/rej;rej";
enum ClosedTop {
  option features.enum_type = CLOSED;
  CLOSED_TOP_UNSPECIFIED = 0;
}
message M { int32 x = 1; }
`,
			wantContain: "CLOSED enums",
		},
		{
			name: "closed_enum_nested",
			protoSrc: `edition = "2024";
package rej.closed_nested;
option go_package = "example.com/rej;rej";
message M {
  enum ClosedInner {
    option features.enum_type = CLOSED;
    CLOSED_INNER_UNSPECIFIED = 0;
  }
  int32 x = 1;
}
`,
			wantContain: "CLOSED enums",
		},
		{
			name: "closed_enum_field_ref",
			protoSrc: `edition = "2026";
package rej.closed_ref;
option go_package = "example.com/rej;rej";
option features.enum_type = CLOSED;
export enum C {
  C_UNSPECIFIED = 0;
}
export message M { C c = 1; }
`,
			wantContain: "CLOSED enums",
		},
		{
			name: "legacy_required",
			protoSrc: `edition = "2023";
package rej.req;
option go_package = "example.com/rej;rej";
message M {
  int32 x = 1 [features.field_presence = LEGACY_REQUIRED];
}
`,
			wantContain: "LEGACY_REQUIRED",
		},
		{
			name: "utf8_none_singular",
			protoSrc: `edition = "2023";
package rej.utf8_s;
option go_package = "example.com/rej;rej";
message M {
  string s = 1 [features.utf8_validation = NONE];
}
`,
			wantContain: "utf8_validation = NONE",
		},
		{
			name: "utf8_none_repeated",
			protoSrc: `edition = "2024";
package rej.utf8_rep;
option go_package = "example.com/rej;rej";
message M {
  repeated string rs = 1 [features.utf8_validation = NONE];
}
`,
			wantContain: "utf8_validation = NONE",
		},
		{
			name: "utf8_none_map_key",
			protoSrc: `edition = "2026";
package rej.utf8_mk;
option go_package = "example.com/rej;rej";
export message M {
  map<string, int32> m = 1 [features.utf8_validation = NONE];
}
`,
			wantContain: "utf8_validation = NONE",
		},
		{
			name: "utf8_none_map_val_file_inherited",
			protoSrc: `edition = "2026";
package rej.utf8_mv;
option go_package = "example.com/rej;rej";
option features.utf8_validation = NONE;
export message M {
  map<int32, string> m = 1;
}
`,
			wantContain: "utf8_validation = NONE",
		},
		{
			name: "nonzero_default_int",
			protoSrc: `edition = "2023";
package rej.def_int;
option go_package = "example.com/rej;rej";
message M {
  int32 x = 1 [default = 7];
}
`,
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_bool",
			protoSrc: `edition = "2024";
package rej.def_bool;
option go_package = "example.com/rej;rej";
message M {
  bool b = 1 [default = true];
}
`,
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_string",
			protoSrc: `edition = "2026";
package rej.def_str;
option go_package = "example.com/rej;rej";
export message M {
  string s = 1 [default = "hi"];
}
`,
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_enum",
			protoSrc: `edition = "2023";
package rej.def_enum;
option go_package = "example.com/rej;rej";
enum E {
  E_UNSPECIFIED = 0;
  E_ONE = 1;
}
message M {
  E e = 1 [default = E_ONE];
}
`,
			wantContain: "non-zero default",
		},
		{
			name: "message_extension_range",
			protoSrc: `edition = "2023";
package rej.ext_range;
option go_package = "example.com/rej;rej";
message M {
  int32 x = 1;
  extensions 100 to 199;
}
`,
			wantContain: "extensions are not supported",
		},
		{
			name: "file_level_non_option_extension",
			protoSrc: `edition = "2024";
package rej.ext_file;
option go_package = "example.com/rej;rej";
message Target {
  extensions 100 to 199;
}
extend Target {
  string extra = 100;
}
`,
			wantContain: "extensions are not supported",
		},
	}

	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			p := filepath.Join(workDir, tc.name+".proto")
			if err := os.WriteFile(p, []byte(tc.protoSrc), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := runProtocEdition(t, protocPath, protocGenGo, protocGenOmnibuf, workDir, workDir, p)
			if err == nil {
				t.Fatalf("expected protoc-gen-omnibuf-go to reject %s, but it succeeded:\n%s", tc.name, out)
			}
			if !strings.Contains(string(out), tc.wantContain) {
				t.Fatalf("rejection output for %s missing %q:\n%s", tc.name, tc.wantContain, out)
			}
		})
	}

	// File-level utf8_validation = NONE is allowed when no string field resolves to NONE.
	t.Run("file_utf8_none_overridden_to_verify_ok", func(t *testing.T) {
		workDir := t.TempDir()
		p := filepath.Join(workDir, "ok_utf8.proto")
		src := `edition = "2026";
package ok.utf8;
option go_package = "example.com/ok;ok";
option features.utf8_validation = NONE;
export message M {
  int32 x = 1;
  string s = 2 [features.utf8_validation = VERIFY];
}
`
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := runProtocEdition(t, protocPath, protocGenGo, protocGenOmnibuf, workDir, workDir, p); err != nil {
			t.Fatalf("expected success when string field overrides utf8_validation = VERIFY: %v\n%s", err, out)
		}
	})
}

func TestEditions_UnsupportedEditionRejected(t *testing.T) {
	// EDITION_UNSTABLE (9999) is accepted by protodesc/protogen and must be rejected by generateEditionFile.
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("editions"),
		Edition: descriptorpb.Edition_EDITION_UNSTABLE.Enum(),
		Name:    proto.String("unstable.proto"),
		Package: proto.String("test.unstable"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/unstable;unstable"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Msg"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("id"),
						Number: proto.Int32(1),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
					},
				},
			},
		},
	}
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"unstable.proto"},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fdp},
	}
	gen, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.New(EDITION_UNSTABLE): %v", err)
	}
	generateFile(gen, gen.Files[0])
	resp := gen.Response()
	if !strings.Contains(resp.GetError(), "unsupported protobuf edition") {
		t.Fatalf("expected unsupported protobuf edition error for 9999, got %q", resp.GetError())
	}

	// Future edition 1003 must also be rejected by validateEditionFile.
	futureFile := &protogen.File{
		Proto: &descriptorpb.FileDescriptorProto{
			Syntax:  proto.String("editions"),
			Edition: descriptorpb.Edition(1003).Enum(),
		},
	}
	if err := validateEditionFile(futureFile); err == nil || !strings.Contains(err.Error(), "unsupported protobuf edition") {
		t.Fatalf("expected unsupported protobuf edition error for 1003, got %v", err)
	}
}
