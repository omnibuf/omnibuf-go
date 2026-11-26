package omnibufproto

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func compileProtoToFiles(t *testing.T, protoSources map[string]string) *protoregistry.Files {
	t.Helper()
	protocPath, err := exec.LookPath("protoc")
	if err != nil {
		t.Skipf("protoc not found in PATH: %v", err)
	}
	dir := t.TempDir()
	fdsPath := filepath.Join(dir, "out.fds")
	args := []string{"-I" + dir, "--include_imports", "--descriptor_set_out=" + fdsPath}
	for name, src := range protoSources {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, p)
	}
	if out, err := exec.Command(protocPath, args...).CombinedOutput(); err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(fdsPath)
	if err != nil {
		t.Fatal(err)
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &fds); err != nil {
		t.Fatalf("unmarshal FileDescriptorSet: %v", err)
	}
	files, err := protodesc.NewFiles(&fds)
	if err != nil {
		t.Fatalf("protodesc.NewFiles: %v", err)
	}
	return files
}

func mustFindMessage(t *testing.T, files *protoregistry.Files, fullName protoreflect.FullName) protoreflect.MessageDescriptor {
	t.Helper()
	d, err := files.FindDescriptorByName(fullName)
	if err != nil {
		t.Fatalf("FindDescriptorByName(%s): %v", fullName, err)
	}
	md, ok := d.(protoreflect.MessageDescriptor)
	if !ok {
		t.Fatalf("%s is not a MessageDescriptor", fullName)
	}
	return md
}

func TestEditions_RealProtoc_2023_2024_2026_RoundTrip(t *testing.T) {
	editions := []struct {
		year        string
		extraHeader string
		visPrefix   string
	}{
		{
			year:        "2023",
			extraHeader: "option features.json_format = LEGACY_BEST_EFFORT;",
		},
		{
			year:        "2024",
			extraHeader: "option features.enforce_naming_style = STYLE2024;\noption features.default_symbol_visibility = EXPORT_TOP_LEVEL;",
		},
		{
			year:        "2026",
			extraHeader: "option features.enforce_naming_style = STYLE2026;\noption features.default_symbol_visibility = STRICT;",
			visPrefix:   "export ",
		},
	}

	for _, ed := range editions {
		t.Run("edition_"+ed.year, func(t *testing.T) {
			src := `edition = "` + ed.year + `";
package test.ed` + ed.year + `;

import "google/protobuf/descriptor.proto";
` + ed.extraHeader + `

extend google.protobuf.FieldOptions {
  string custom_note = 50002;
}

` + ed.visPrefix + `enum OpenState {
  OPEN_STATE_UNSPECIFIED = 0;
  OPEN_STATE_READY = 1;
}

` + ed.visPrefix + `message Child {
  int32 id = 1 [default = 0];
  string label = 2 [default = "", (custom_note) = "ok"];
}

` + ed.visPrefix + `message Record {
  int32 exp_i32 = 1 [default = 0];
  double exp_f64 = 2 [default = 0.0];
  bool exp_bool = 3 [default = false];
  string exp_str = 4 [default = ""];
  bytes exp_bytes = 5 [default = ""];
  OpenState exp_state = 6 [default = OPEN_STATE_UNSPECIFIED];

  int32 imp_i32 = 7 [features.field_presence = IMPLICIT];
  bool imp_bool = 8 [features.field_presence = IMPLICIT];

  repeated int32 packed_nums = 9 [features.repeated_field_encoding = PACKED];
  repeated int32 expanded_nums = 10 [features.repeated_field_encoding = EXPANDED];
  repeated Child children_lp = 11;
  repeated Child children_delim = 12 [features.message_encoding = DELIMITED];

  map<string, int32> scores = 13;
  map<string, Child> child_map = 14;

  Child child_lp = 15;
  Child child_delim = 16 [features.message_encoding = DELIMITED];

  oneof choice {
    int32 choice_num = 17;
    bool choice_flag = 18;
    string choice_text = 19;
  }
}

` + ed.visPrefix + `message DelimitedPair {
  Child single = 1 [features.message_encoding = DELIMITED];
  repeated Child items = 2 [features.message_encoding = DELIMITED];
}

` + ed.visPrefix + `message LengthPrefixedPair {
  Child single = 1;
  repeated Child items = 2;
}
`
			files := compileProtoToFiles(t, map[string]string{"schema.proto": src})
			pkg := protoreflect.FullName("test.ed" + ed.year)
			recMD := mustFindMessage(t, files, pkg+".Record")
			childMD := mustFindMessage(t, files, pkg+".Child")
			delimMD := mustFindMessage(t, files, pkg+".DelimitedPair")
			lpMD := mustFindMessage(t, files, pkg+".LengthPrefixedPair")

			// Verify DELIMITED field actually uses protoreflect.GroupKind under Editions.
			if recMD.Fields().ByNumber(16).Kind() != protoreflect.GroupKind {
				t.Fatalf("expected child_delim to have GroupKind, got %v", recMD.Fields().ByNumber(16).Kind())
			}

			// 1. Absent vs explicit zero values.
			absent := dynamicpb.NewMessage(recMD)
			zero := dynamicpb.NewMessage(recMD)
			zero.Set(recMD.Fields().ByNumber(1), protoreflect.ValueOfInt32(0))
			zero.Set(recMD.Fields().ByNumber(2), protoreflect.ValueOfFloat64(0.0))
			zero.Set(recMD.Fields().ByNumber(3), protoreflect.ValueOfBool(false))
			zero.Set(recMD.Fields().ByNumber(4), protoreflect.ValueOfString(""))
			zero.Set(recMD.Fields().ByNumber(5), protoreflect.ValueOfBytes([]byte{}))
			zero.Set(recMD.Fields().ByNumber(6), protoreflect.ValueOfEnum(0))

			absentWire, err := Marshal(absent)
			if err != nil {
				t.Fatalf("Marshal absent: %v", err)
			}
			zeroWire, err := Marshal(zero)
			if err != nil {
				t.Fatalf("Marshal explicit zero: %v", err)
			}
			if bytes.Equal(absentWire, zeroWire) {
				t.Fatalf("absent and explicit-zero produced identical wire bytes: %x", absentWire)
			}
			if b, ok := wire.ReadBool(zeroWire, 3); !ok || b {
				t.Fatalf("explicit false must emit WireBoolFalse, got (%v, %v)", b, ok)
			}
			if _, _, ok := wire.LocateField(absentWire, 3); ok {
				t.Fatalf("absent explicit bool must not be emitted")
			}

			gotZero := dynamicpb.NewMessage(recMD)
			if err := Unmarshal(zeroWire, gotZero); err != nil {
				t.Fatalf("Unmarshal explicit zero: %v", err)
			}
			for _, fnum := range []protoreflect.FieldNumber{1, 2, 3, 4, 5, 6} {
				fd := recMD.Fields().ByNumber(fnum)
				if !gotZero.Has(fd) {
					t.Fatalf("field %s lost explicit zero presence", fd.Name())
				}
			}

			gotAbsent := dynamicpb.NewMessage(recMD)
			if err := Unmarshal(absentWire, gotAbsent); err != nil {
				t.Fatalf("Unmarshal absent: %v", err)
			}
			for _, fnum := range []protoreflect.FieldNumber{1, 2, 3, 4, 5, 6} {
				fd := recMD.Fields().ByNumber(fnum)
				if gotAbsent.Has(fd) {
					t.Fatalf("absent field %s unexpectedly present", fd.Name())
				}
			}

			// 2. Full message with unknown open enum, packed vs expanded, maps, delimited, oneof.
			full := dynamicpb.NewMessage(recMD)
			full.Set(recMD.Fields().ByNumber(1), protoreflect.ValueOfInt32(-42))
			full.Set(recMD.Fields().ByNumber(2), protoreflect.ValueOfFloat64(2.5))
			full.Set(recMD.Fields().ByNumber(3), protoreflect.ValueOfBool(true))
			full.Set(recMD.Fields().ByNumber(4), protoreflect.ValueOfString("hello-"+ed.year))
			full.Set(recMD.Fields().ByNumber(5), protoreflect.ValueOfBytes([]byte{0xaa, 0xbb}))
			full.Set(recMD.Fields().ByNumber(6), protoreflect.ValueOfEnum(777))
			full.Set(recMD.Fields().ByNumber(7), protoreflect.ValueOfInt32(19))
			full.Set(recMD.Fields().ByNumber(8), protoreflect.ValueOfBool(true))

			for _, fnum := range []protoreflect.FieldNumber{9, 10} {
				l := full.Mutable(recMD.Fields().ByNumber(fnum)).List()
				l.Append(protoreflect.ValueOfInt32(-3))
				l.Append(protoreflect.ValueOfInt32(0))
				l.Append(protoreflect.ValueOfInt32(9))
			}

			makeChild := func(id int32, label string) *dynamicpb.Message {
				c := dynamicpb.NewMessage(childMD)
				c.Set(childMD.Fields().ByNumber(1), protoreflect.ValueOfInt32(id))
				c.Set(childMD.Fields().ByNumber(2), protoreflect.ValueOfString(label))
				return c
			}
			full.Mutable(recMD.Fields().ByNumber(11)).List().Append(protoreflect.ValueOfMessage(makeChild(1, "lp0")))
			full.Mutable(recMD.Fields().ByNumber(12)).List().Append(protoreflect.ValueOfMessage(makeChild(2, "dl0")))

			scores := full.Mutable(recMD.Fields().ByNumber(13)).Map()
			scores.Set(protoreflect.ValueOfString("b").MapKey(), protoreflect.ValueOfInt32(2))
			scores.Set(protoreflect.ValueOfString("a").MapKey(), protoreflect.ValueOfInt32(1))

			cmap := full.Mutable(recMD.Fields().ByNumber(14)).Map()
			cmap.Set(protoreflect.ValueOfString("k1").MapKey(), protoreflect.ValueOfMessage(makeChild(3, "m1")))

			full.Set(recMD.Fields().ByNumber(15), protoreflect.ValueOfMessage(makeChild(4, "slp")))
			full.Set(recMD.Fields().ByNumber(16), protoreflect.ValueOfMessage(makeChild(5, "sdl")))
			full.Set(recMD.Fields().ByNumber(18), protoreflect.ValueOfBool(false))

			raw, err := MarshalDeterministic(full)
			if err != nil {
				t.Fatalf("MarshalDeterministic full: %v", err)
			}
			// Verify packed and expanded repeated fields emit identical WireArray payloads.
			bPacked, wtP, okP := wire.LocateField(raw, 9)
			bExpanded, wtE, okE := wire.LocateField(raw, 10)
			lP, lnP := wire.DecodeVarint(bPacked)
			lE, lnE := wire.DecodeVarint(bExpanded)
			if !okP || !okE || wtP != wire.WireArray || wtE != wire.WireArray || !bytes.Equal(bPacked[:lnP+int(lP)], bExpanded[:lnE+int(lE)]) {
				t.Fatalf("packed vs expanded WireArray mismatch")
			}

			// Verify omnibuf.Marshal (root package hook) produces identical bytes.
			rootRaw, err := omnibuf.MarshalDeterministic(full)
			if err != nil {
				t.Fatalf("omnibuf.MarshalDeterministic: %v", err)
			}
			if !bytes.Equal(raw, rootRaw) {
				t.Fatalf("root omnibuf.MarshalDeterministic differed from omnibufproto")
			}

			// Round-trip including SelfDescribing + DecodeBlind.
			sdRaw, err := (MarshalOptions{Deterministic: true, SelfDescribing: true}).Marshal(full)
			if err != nil {
				t.Fatalf("Marshal SelfDescribing: %v", err)
			}
			blind, err := omnibuf.DecodeBlind(sdRaw)
			if err != nil {
				t.Fatalf("DecodeBlind: %v", err)
			}
			if dl, ok := blind["child_delim"].(map[string]any); !ok || dl["label"] != "sdl" {
				t.Fatalf("DecodeBlind child_delim = %#v", blind["child_delim"])
			}

			out := dynamicpb.NewMessage(recMD)
			if err := Unmarshal(sdRaw, out); err != nil {
				t.Fatalf("Unmarshal sdRaw: %v", err)
			}
			if !proto.Equal(full, out) {
				t.Fatalf("round-trip mismatch:\ngot:  %v\nwant: %v", out, full)
			}
			if out.Get(recMD.Fields().ByNumber(6)).Enum() != 777 {
				t.Fatalf("unknown open enum not preserved")
			}
			if !out.Has(recMD.Fields().ByNumber(18)) || out.Get(recMD.Fields().ByNumber(18)).Bool() {
				t.Fatalf("oneof false active branch lost")
			}

			// Switch oneof branch on reused message.
			sw := dynamicpb.NewMessage(recMD)
			sw.Set(recMD.Fields().ByNumber(19), protoreflect.ValueOfString(""))
			swWire, err := Marshal(sw)
			if err != nil {
				t.Fatalf("Marshal switched oneof: %v", err)
			}
			if err := Unmarshal(swWire, out); err != nil {
				t.Fatalf("Unmarshal switched oneof: %v", err)
			}
			if out.Has(recMD.Fields().ByNumber(18)) || !out.Has(recMD.Fields().ByNumber(19)) || out.Get(recMD.Fields().ByNumber(19)).String() != "" {
				t.Fatalf("oneof branch switch failed")
			}

			// Clear oneof on empty packet.
			if err := Unmarshal(nil, out); err != nil {
				t.Fatalf("Unmarshal empty: %v", err)
			}
			if out.WhichOneof(recMD.Oneofs().ByName("choice")) != nil {
				t.Fatalf("expected oneof cleared after empty packet")
			}

			// 3. DELIMITED vs LENGTH_PREFIXED exact wire byte identity (singular and repeated).
			dp := dynamicpb.NewMessage(delimMD)
			dp.Set(delimMD.Fields().ByNumber(1), protoreflect.ValueOfMessage(makeChild(10, "same")))
			dp.Mutable(delimMD.Fields().ByNumber(2)).List().Append(protoreflect.ValueOfMessage(makeChild(11, "e0")))
			dp.Mutable(delimMD.Fields().ByNumber(2)).List().Append(protoreflect.ValueOfMessage(makeChild(12, "e1")))

			lp := dynamicpb.NewMessage(lpMD)
			lp.Set(lpMD.Fields().ByNumber(1), protoreflect.ValueOfMessage(makeChild(10, "same")))
			lp.Mutable(lpMD.Fields().ByNumber(2)).List().Append(protoreflect.ValueOfMessage(makeChild(11, "e0")))
			lp.Mutable(lpMD.Fields().ByNumber(2)).List().Append(protoreflect.ValueOfMessage(makeChild(12, "e1")))

			dpWire, err := Marshal(dp)
			if err != nil {
				t.Fatalf("Marshal DelimitedPair: %v", err)
			}
			lpWire, err := Marshal(lp)
			if err != nil {
				t.Fatalf("Marshal LengthPrefixedPair: %v", err)
			}
			if !bytes.Equal(dpWire, lpWire) {
				t.Fatalf("DELIMITED vs LENGTH_PREFIXED wire bytes differ:\ndelim: %x\nlp:    %x", dpWire, lpWire)
			}
			dpOut := dynamicpb.NewMessage(delimMD)
			if err := Unmarshal(dpWire, dpOut); err != nil {
				t.Fatalf("Unmarshal DelimitedPair: %v", err)
			}
			if !proto.Equal(dp, dpOut) {
				t.Fatalf("DelimitedPair round-trip mismatch")
			}
		})
	}
}

func TestEditions_FileLevelDelimitedWithMapAndSchemaMeta(t *testing.T) {
	src := `edition = "2024";
package test.filedelim;
option features.message_encoding = DELIMITED;

message Sub {
  int32 id = 1;
  string name = 2;
}

message Holder {
  Sub one = 1;
  repeated Sub many = 2;
  map<string, Sub> by_name = 3;
}
`
	files := compileProtoToFiles(t, map[string]string{"filedelim.proto": src})
	subMD := mustFindMessage(t, files, "test.filedelim.Sub")
	holderMD := mustFindMessage(t, files, "test.filedelim.Holder")

	// Verify singular and repeated fields inherited GroupKind from file-level DELIMITED.
	if holderMD.Fields().ByNumber(1).Kind() != protoreflect.GroupKind || holderMD.Fields().ByNumber(2).Kind() != protoreflect.GroupKind {
		t.Fatalf("expected singular and repeated fields to inherit GroupKind, got (%v, %v)",
			holderMD.Fields().ByNumber(1).Kind(), holderMD.Fields().ByNumber(2).Kind())
	}

	makeSub := func(id int32, name string) *dynamicpb.Message {
		s := dynamicpb.NewMessage(subMD)
		s.Set(subMD.Fields().ByNumber(1), protoreflect.ValueOfInt32(id))
		s.Set(subMD.Fields().ByNumber(2), protoreflect.ValueOfString(name))
		return s
	}

	in := dynamicpb.NewMessage(holderMD)
	in.Set(holderMD.Fields().ByNumber(1), protoreflect.ValueOfMessage(makeSub(1, "one")))
	in.Mutable(holderMD.Fields().ByNumber(2)).List().Append(protoreflect.ValueOfMessage(makeSub(2, "two")))
	in.Mutable(holderMD.Fields().ByNumber(3)).Map().Set(
		protoreflect.ValueOfString("k").MapKey(),
		protoreflect.ValueOfMessage(makeSub(3, "three")),
	)

	sdWire, err := (MarshalOptions{Deterministic: true, SelfDescribing: true}).Marshal(in)
	if err != nil {
		t.Fatalf("Marshal file-level DELIMITED with map: %v", err)
	}
	blind, err := omnibuf.DecodeBlind(sdWire)
	if err != nil {
		t.Fatalf("DecodeBlind file-level DELIMITED: %v", err)
	}
	byName, ok := blind["by_name"].(map[string]any)
	if !ok {
		t.Fatalf("DecodeBlind by_name = %#v", blind["by_name"])
	}
	entry, ok := byName["k"].(map[string]any)
	if !ok || entry["name"] != "three" {
		t.Fatalf("DecodeBlind by_name[k] = %#v", byName["k"])
	}

	out := dynamicpb.NewMessage(holderMD)
	if err := Unmarshal(sdWire, out); err != nil {
		t.Fatalf("Unmarshal file-level DELIMITED: %v", err)
	}
	if !proto.Equal(in, out) {
		t.Fatalf("file-level DELIMITED round-trip mismatch")
	}

	// Also verify cyclic DELIMITED messages exercise buildProtoReflectValueSchemaMeta cleanly.
	cycSrc := `edition = "2026";
package test.cycdelim;
export message Node {
  int32 id = 1;
  Node next = 2 [features.message_encoding = DELIMITED];
}
`
	cycFiles := compileProtoToFiles(t, map[string]string{"cyc.proto": cycSrc})
	nodeMD := mustFindMessage(t, cycFiles, "test.cycdelim.Node")
	if nodeMD.Fields().ByNumber(2).Kind() != protoreflect.GroupKind {
		t.Fatalf("expected cyclic next field to be GroupKind, got %v", nodeMD.Fields().ByNumber(2).Kind())
	}
	leaf := dynamicpb.NewMessage(nodeMD)
	leaf.Set(nodeMD.Fields().ByNumber(1), protoreflect.ValueOfInt32(2))
	root := dynamicpb.NewMessage(nodeMD)
	root.Set(nodeMD.Fields().ByNumber(1), protoreflect.ValueOfInt32(1))
	root.Set(nodeMD.Fields().ByNumber(2), protoreflect.ValueOfMessage(leaf))
	cycWire, err := (MarshalOptions{Deterministic: true, SelfDescribing: true}).Marshal(root)
	if err != nil {
		t.Fatalf("Marshal cyclic DELIMITED SelfDescribing: %v", err)
	}
	cycBlind, err := omnibuf.DecodeBlind(cycWire)
	if err != nil {
		t.Fatalf("DecodeBlind cyclic DELIMITED: %v", err)
	}
	nextBlind, ok := cycBlind["next"].(map[string]any)
	if !ok || nextBlind["id"] != int64(2) {
		t.Fatalf("DecodeBlind cyclic DELIMITED next = %#v", cycBlind["next"])
	}
	cycOut := dynamicpb.NewMessage(nodeMD)
	if err := Unmarshal(cycWire, cycOut); err != nil {
		t.Fatalf("Unmarshal cyclic DELIMITED: %v", err)
	}
	if !proto.Equal(root, cycOut) {
		t.Fatalf("cyclic DELIMITED round-trip mismatch")
	}
}

func TestEditions_PresenceInheritanceOverrides(t *testing.T) {
	// Test File (IMPLICIT) -> Message (EXPLICIT) -> Field (IMPLICIT) and Oneof (EXPLICIT)
	// via protodesc.NewFile to exercise all 4 inheritance scopes.
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("editions"),
		Edition: descriptorpb.Edition_EDITION_2024.Enum(),
		Name:    proto.String("presence_inherit.proto"),
		Package: proto.String("test.pres"),
		Options: &descriptorpb.FileOptions{
			Features: &descriptorpb.FeatureSet{
				FieldPresence: descriptorpb.FeatureSet_IMPLICIT.Enum(),
			},
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("FileImplicit"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{
					{Name: proto.String("pick")},
				},
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("f_imp"),
						Number: proto.Int32(1),
						Label:  opt.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
					},
					{
						Name:   proto.String("f_exp"),
						Number: proto.Int32(2),
						Label:  opt.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
						Options: &descriptorpb.FieldOptions{
							Features: &descriptorpb.FeatureSet{
								FieldPresence: descriptorpb.FeatureSet_EXPLICIT.Enum(),
							},
						},
					},
					{
						Name:       proto.String("o_zero"),
						Number:     proto.Int32(3),
						Label:      opt.Enum(),
						Type:       descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
						OneofIndex: proto.Int32(0),
					},
				},
			},
			{
				Name: proto.String("MsgExplicitOverride"),
				Options: &descriptorpb.MessageOptions{
					Features: &descriptorpb.FeatureSet{
						FieldPresence: descriptorpb.FeatureSet_EXPLICIT.Enum(),
					},
				},
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("m_exp"),
						Number: proto.Int32(1),
						Label:  opt.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
					},
					{
						Name:   proto.String("m_field_imp"),
						Number: proto.Int32(2),
						Label:  opt.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
						Options: &descriptorpb.FieldOptions{
							Features: &descriptorpb.FeatureSet{
								FieldPresence: descriptorpb.FeatureSet_IMPLICIT.Enum(),
							},
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

	fileImpMD := fd.Messages().ByName("FileImplicit")
	if fileImpMD.Fields().ByNumber(1).HasPresence() {
		t.Fatal("f_imp should inherit IMPLICIT presence")
	}
	if !fileImpMD.Fields().ByNumber(2).HasPresence() {
		t.Fatal("f_exp should override to EXPLICIT presence")
	}
	if !fileImpMD.Fields().ByNumber(3).HasPresence() {
		t.Fatal("o_zero oneof field must have EXPLICIT presence")
	}

	msgExpMD := fd.Messages().ByName("MsgExplicitOverride")
	if !msgExpMD.Fields().ByNumber(1).HasPresence() {
		t.Fatal("m_exp should inherit message EXPLICIT presence")
	}
	if msgExpMD.Fields().ByNumber(2).HasPresence() {
		t.Fatal("m_field_imp should override to IMPLICIT presence")
	}

	// Verify absent vs explicit zero on MsgExplicitOverride.
	m1 := dynamicpb.NewMessage(msgExpMD)
	w1, err := Marshal(m1)
	if err != nil {
		t.Fatalf("Marshal m1: %v", err)
	}
	if _, _, ok := wire.LocateField(w1, 1); ok {
		t.Fatal("absent m_exp should not be encoded")
	}
	if _, _, ok := wire.LocateField(w1, 2); !ok {
		t.Fatal("implicit m_field_imp should be encoded even when zero")
	}

	m2 := dynamicpb.NewMessage(msgExpMD)
	m2.Set(msgExpMD.Fields().ByNumber(1), protoreflect.ValueOfInt32(0))
	w2, err := Marshal(m2)
	if err != nil {
		t.Fatalf("Marshal m2: %v", err)
	}
	if _, _, ok := wire.LocateField(w2, 1); !ok {
		t.Fatal("explicit zero m_exp must be encoded")
	}
	out := dynamicpb.NewMessage(msgExpMD)
	if err := Unmarshal(w2, out); err != nil {
		t.Fatalf("Unmarshal w2: %v", err)
	}
	if !out.Has(msgExpMD.Fields().ByNumber(1)) || out.Get(msgExpMD.Fields().ByNumber(1)).Int() != 0 {
		t.Fatal("explicit zero m_exp presence lost on round-trip")
	}
}

type editionOpenImplicitStruct struct {
	ID uint32 `protobuf:"varint,1,opt,name=id"`
	md protoreflect.MessageDescriptor
}

func (m *editionOpenImplicitStruct) ProtoReflect() protoreflect.Message {
	return dynamicpb.NewMessage(m.md)
}

func TestNeedsProtoReflection_EditionsOpenImplicitAlwaysReflects(t *testing.T) {
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("editions"),
		Edition: descriptorpb.Edition_EDITION_2026.Enum(),
		Name:    proto.String("open_imp.proto"),
		Package: proto.String("test.openimp"),
		Options: &descriptorpb.FileOptions{
			Features: &descriptorpb.FeatureSet{
				FieldPresence: descriptorpb.FeatureSet_IMPLICIT.Enum(),
			},
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("OpenImp"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("id"),
						Number: proto.Int32(1),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum(),
					},
				},
			},
		},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("protodesc.NewFile: %v", err)
	}
	md := fd.Messages().ByName("OpenImp")
	msg := &editionOpenImplicitStruct{md: md}

	if !hasAllOpenStructFields(reflect.TypeOf(msg), md, make(map[reflect.Type]bool)) {
		t.Fatal("precondition failed: expected struct to satisfy hasAllOpenStructFields")
	}
	if !needsProtoReflection(msg) {
		t.Fatal("expected needsProtoReflection to return true for Editions message even with open struct and implicit presence")
	}
}

func TestEditions_RuntimeRejections(t *testing.T) {
	rejectProtos := []struct {
		name        string
		protoSrc    string
		msgName     protoreflect.FullName
		wantContain string
	}{
		{
			name: "closed_enum_field",
			protoSrc: `edition = "2023";
package rej.c1;
enum ClosedE {
  option features.enum_type = CLOSED;
  CLOSED_E_UNSPECIFIED = 0;
}
message M { ClosedE e = 1; }
`,
			msgName:     "rej.c1.M",
			wantContain: "CLOSED enums",
		},
		{
			name: "closed_enum_nested_unused",
			protoSrc: `edition = "2024";
package rej.c2;
message M {
  enum ClosedInner {
    option features.enum_type = CLOSED;
    CLOSED_INNER_UNSPECIFIED = 0;
  }
  int32 x = 1;
}
`,
			msgName:     "rej.c2.M",
			wantContain: "CLOSED enums",
		},
		{
			name: "closed_enum_map_value",
			protoSrc: `edition = "2026";
package rej.c3;
option features.enum_type = CLOSED;
export enum ClosedVal {
  CLOSED_VAL_UNSPECIFIED = 0;
}
export message M {
  map<string, ClosedVal> m = 1;
}
`,
			msgName:     "rej.c3.M",
			wantContain: "CLOSED enums",
		},
		{
			name: "legacy_required",
			protoSrc: `edition = "2023";
package rej.req;
message M {
  int32 x = 1 [features.field_presence = LEGACY_REQUIRED];
}
`,
			msgName:     "rej.req.M",
			wantContain: "LEGACY_REQUIRED",
		},
		{
			name: "utf8_none_singular",
			protoSrc: `edition = "2023";
package rej.u1;
message M {
  string s = 1 [features.utf8_validation = NONE];
}
`,
			msgName:     "rej.u1.M",
			wantContain: "utf8_validation = NONE",
		},
		{
			name: "utf8_none_repeated",
			protoSrc: `edition = "2024";
package rej.u2;
message M {
  repeated string rs = 1 [features.utf8_validation = NONE];
}
`,
			msgName:     "rej.u2.M",
			wantContain: "utf8_validation = NONE",
		},
		{
			name: "utf8_none_map_key",
			protoSrc: `edition = "2026";
package rej.u3;
export message M {
  map<string, int32> m = 1 [features.utf8_validation = NONE];
}
`,
			msgName:     "rej.u3.M",
			wantContain: "utf8_validation = NONE",
		},
		{
			name: "utf8_none_map_val_file_inherited",
			protoSrc: `edition = "2026";
package rej.u4;
option features.utf8_validation = NONE;
export message M {
  map<int32, string> m = 1;
}
`,
			msgName:     "rej.u4.M",
			wantContain: "utf8_validation = NONE",
		},
		{
			name: "nonzero_default_int",
			protoSrc: `edition = "2023";
package rej.d1;
message M { int32 x = 1 [default = 5]; }
`,
			msgName:     "rej.d1.M",
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_bool",
			protoSrc: `edition = "2024";
package rej.d2;
message M { bool b = 1 [default = true]; }
`,
			msgName:     "rej.d2.M",
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_string",
			protoSrc: `edition = "2026";
package rej.d3;
export message M { string s = 1 [default = "abc"]; }
`,
			msgName:     "rej.d3.M",
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_bytes",
			protoSrc: `edition = "2023";
package rej.d4;
message M { bytes b = 1 [default = "xyz"]; }
`,
			msgName:     "rej.d4.M",
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_float",
			protoSrc: `edition = "2024";
package rej.d5;
message M { float f = 1 [default = 1.25]; }
`,
			msgName:     "rej.d5.M",
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_neg_zero",
			protoSrc: `edition = "2024";
package rej.d6;
message M { double d = 1 [default = -0.0]; }
`,
			msgName:     "rej.d6.M",
			wantContain: "non-zero default",
		},
		{
			name: "nonzero_default_enum",
			protoSrc: `edition = "2026";
package rej.d7;
export enum E {
  E_UNSPECIFIED = 0;
  E_TWO = 2;
}
export message M { E e = 1 [default = E_TWO]; }
`,
			msgName:     "rej.d7.M",
			wantContain: "non-zero default",
		},
		{
			name: "message_extension_range",
			protoSrc: `edition = "2023";
package rej.e1;
message M {
  int32 x = 1;
  extensions 100 to 199;
}
`,
			msgName:     "rej.e1.M",
			wantContain: "extensions are not supported",
		},
		{
			name: "file_non_option_extension",
			protoSrc: `edition = "2024";
package rej.e2;
message Target { extensions 100 to 199; }
extend Target { int32 ext_field = 100; }
message M { int32 x = 1; }
`,
			msgName:     "rej.e2.M",
			wantContain: "extensions are not supported",
		},
	}

	for _, tc := range rejectProtos {
		t.Run(tc.name, func(t *testing.T) {
			files := compileProtoToFiles(t, map[string]string{tc.name + ".proto": tc.protoSrc})
			md := mustFindMessage(t, files, tc.msgName)
			msg := dynamicpb.NewMessage(md)

			if _, err := Marshal(msg); err == nil || !strings.Contains(err.Error(), tc.wantContain) {
				t.Fatalf("Marshal(%s): got err=%v, want substring %q", tc.name, err, tc.wantContain)
			}
			if err := Unmarshal(nil, msg); err == nil || !strings.Contains(err.Error(), tc.wantContain) {
				t.Fatalf("Unmarshal(%s): got err=%v, want substring %q", tc.name, err, tc.wantContain)
			}
		})
	}

	// File-level utf8_validation = NONE allowed when string fields override to VERIFY.
	t.Run("file_utf8_none_overridden_to_verify_ok", func(t *testing.T) {
		src := `edition = "2026";
package ok.u1;
option features.utf8_validation = NONE;
export message M {
  int32 x = 1;
  string s = 2 [features.utf8_validation = VERIFY];
}
`
		files := compileProtoToFiles(t, map[string]string{"ok.proto": src})
		md := mustFindMessage(t, files, "ok.u1.M")
		in := dynamicpb.NewMessage(md)
		in.Set(md.Fields().ByNumber(2), protoreflect.ValueOfString("valid"))
		raw, err := Marshal(in)
		if err != nil {
			t.Fatalf("expected valid marshal: %v", err)
		}
		out := dynamicpb.NewMessage(md)
		if err := Unmarshal(raw, out); err != nil {
			t.Fatalf("expected valid unmarshal: %v", err)
		}
		if !proto.Equal(in, out) {
			t.Fatalf("round-trip mismatch")
		}
	})
}

type fakeEditionFileDesc struct {
	protoreflect.FileDescriptor
	edition int32
}

func (f fakeEditionFileDesc) Edition() int32 { return f.edition }

type fakeEditionMsgDesc struct {
	protoreflect.MessageDescriptor
	parentFile protoreflect.FileDescriptor
}

func (m fakeEditionMsgDesc) ParentFile() protoreflect.FileDescriptor { return m.parentFile }

func TestEditions_UnsupportedEditionRejected(t *testing.T) {
	// 1. EDITION_UNSTABLE (9999) via real protodesc.NewFile.
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("editions"),
		Edition: descriptorpb.Edition_EDITION_UNSTABLE.Enum(),
		Name:    proto.String("unstable.proto"),
		Package: proto.String("test.unstable"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Msg"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("x"),
						Number: proto.Int32(1),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
					},
				},
			},
		},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("protodesc.NewFile(EDITION_UNSTABLE): %v", err)
	}
	md := fd.Messages().ByName("Msg")
	msg := dynamicpb.NewMessage(md)
	if _, err := Marshal(msg); err == nil || !strings.Contains(err.Error(), "unsupported protobuf edition") {
		t.Fatalf("Marshal(EDITION_UNSTABLE): got err=%v", err)
	}
	if err := Unmarshal(nil, msg); err == nil || !strings.Contains(err.Error(), "unsupported protobuf edition") {
		t.Fatalf("Unmarshal(EDITION_UNSTABLE): got err=%v", err)
	}

	// 2. Future edition 1003 via wrapper FileDescriptor.
	wrappedMD := fakeEditionMsgDesc{
		MessageDescriptor: md,
		parentFile:        fakeEditionFileDesc{FileDescriptor: fd, edition: 1003},
	}
	if err := validateEditionDescriptorTree(wrappedMD); err == nil || !strings.Contains(err.Error(), "unsupported protobuf edition") {
		t.Fatalf("validateEditionDescriptorTree(1003): got err=%v", err)
	}
}

func TestProto2LegacyGroupBehaviorUnchanged(t *testing.T) {
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto2"),
		Name:    proto.String("legacy_group.proto"),
		Package: proto.String("test.p2group"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Holder"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:     proto.String("mygroup"),
						Number:   proto.Int32(1),
						Label:    opt.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_GROUP.Enum(),
						TypeName: proto.String(".test.p2group.Holder.MyGroup"),
					},
				},
				NestedType: []*descriptorpb.DescriptorProto{
					{
						Name: proto.String("MyGroup"),
						Field: []*descriptorpb.FieldDescriptorProto{
							{
								Name:   proto.String("a"),
								Number: proto.Int32(1),
								Label:  opt.Enum(),
								Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
							},
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
	holderMD := fd.Messages().ByName("Holder")
	groupMD := holderMD.Messages().ByName("MyGroup")

	m := dynamicpb.NewMessage(holderMD)
	g := dynamicpb.NewMessage(groupMD)
	g.Set(groupMD.Fields().ByNumber(1), protoreflect.ValueOfInt32(5))
	m.Set(holderMD.Fields().ByNumber(1), protoreflect.ValueOfMessage(g))

	if _, err := Marshal(m); err == nil || !strings.Contains(err.Error(), "unsupported kind group") {
		t.Fatalf("expected legacy proto2 GroupKind error, got %v", err)
	}
}
