package omnibufproto_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/proto"

	. "github.com/omnibuf/omnibuf-go"
	"github.com/omnibuf/omnibuf-go/omnibufproto/internal/pb"
	. "github.com/omnibuf/omnibuf-go/wire"
)

type editionsConformanceMsg interface {
	proto.Message
	TryMarshalOmniBufAppend(dst []byte) ([]byte, error)
	TryMarshalOmniBufSelfDescribingAppend(dst []byte) ([]byte, error)
	UnmarshalOmniBuf(data []byte, zeroCopy bool) error
}

func buildEditionsVectors(t *testing.T) []ConformanceVector {
	t.Helper()
	var out []ConformanceVector

	out = append(out, buildEditionSuiteVectors(
		t,
		"2023",
		"TestEditions2023",
		func() editionsConformanceMsg { return &pb.TestEditions2023{} },
		func() editionsConformanceMsg {
			zeroInt := int32(0)
			zeroBool := false
			zeroStr := ""
			zeroDouble := 0.0
			zeroStatus := pb.Editions2023Status_EDITIONS2023_STATUS_UNSPECIFIED
			return &pb.TestEditions2023{
				ExplicitInt:    &zeroInt,
				ExplicitBool:   &zeroBool,
				ExplicitString: &zeroStr,
				ExplicitBytes:  []byte{},
				ExplicitDouble: &zeroDouble,
				Status:         &zeroStatus,
				Child:          &pb.Editions2023Child{Id: &zeroInt, Note: &zeroStr},
				DelimitedChild: &pb.Editions2023Child{},
				Choice:         &pb.TestEditions2023_ChoiceInt{ChoiceInt: 0},
			}
		},
		func() editionsConformanceMsg {
			vInt := int32(-42)
			vBool := true
			vStr := "edition-2023-🚀"
			vDouble := -12.5
			vStatus := pb.Editions2023Status(99)
			c1ID := int32(101)
			c1Note := "length-prefixed"
			c2ID := int32(202)
			c2Note := "delimited-wire-submsg"
			return &pb.TestEditions2023{
				ExplicitInt:    &vInt,
				ExplicitBool:   &vBool,
				ExplicitString: &vStr,
				ExplicitBytes:  []byte{0x00, 0xde, 0xad, 0xbe, 0xef},
				ExplicitDouble: &vDouble,
				Status:         &vStatus,
				ImplicitCount:  7,
				ImplicitFlag:   true,
				Child:          &pb.Editions2023Child{Id: &c1ID, Note: &c1Note},
				DelimitedChild: &pb.Editions2023Child{Id: &c2ID, Note: &c2Note},
				PackedVals:     []int32{-10, 0, 10, 100000},
				ExpandedVals:   []int32{1, -2, 3},
				Scores:         map[string]int32{"beta": -5, "alpha": 0, "🚀": 42},
				Choice:         &pb.TestEditions2023_ChoiceText{ChoiceText: ""},
			}
		},
	)...)

	out = append(out, buildEditionSuiteVectors(
		t,
		"2024",
		"TestEditions2024",
		func() editionsConformanceMsg { return &pb.TestEditions2024{} },
		func() editionsConformanceMsg {
			msg := &pb.TestEditions2024{}
			msg.SetExplicitInt(0)
			msg.SetExplicitBool(false)
			msg.SetExplicitString("")
			msg.SetExplicitBytes([]byte{})
			msg.SetExplicitDouble(0.0)
			msg.SetStatus(pb.Editions2024Status_EDITIONS2024_STATUS_UNSPECIFIED)
			child := &pb.Editions2024Child{}
			child.SetId(0)
			child.SetNote("")
			msg.SetChild(child)
			msg.SetDelimitedChild(&pb.Editions2024Child{})
			msg.SetChoiceInt(0)
			return msg
		},
		func() editionsConformanceMsg {
			msg := &pb.TestEditions2024{}
			msg.SetExplicitInt(-42)
			msg.SetExplicitBool(true)
			msg.SetExplicitString("edition-2024-🚀")
			msg.SetExplicitBytes([]byte{0x00, 0xde, 0xad, 0xbe, 0xef})
			msg.SetExplicitDouble(-12.5)
			msg.SetStatus(pb.Editions2024Status(99))
			msg.SetImplicitCount(7)
			msg.SetImplicitFlag(true)
			c1 := &pb.Editions2024Child{}
			c1.SetId(101)
			c1.SetNote("length-prefixed")
			msg.SetChild(c1)
			c2 := &pb.Editions2024Child{}
			c2.SetId(202)
			c2.SetNote("delimited-wire-submsg")
			msg.SetDelimitedChild(c2)
			msg.SetPackedVals([]int32{-10, 0, 10, 100000})
			msg.SetExpandedVals([]int32{1, -2, 3})
			msg.SetScores(map[string]int32{"beta": -5, "alpha": 0, "🚀": 42})
			msg.SetChoiceText("")
			return msg
		},
	)...)

	out = append(out, buildEditionSuiteVectors(
		t,
		"2026",
		"TestEditions2026",
		func() editionsConformanceMsg { return &pb.TestEditions2026{} },
		func() editionsConformanceMsg {
			msg := &pb.TestEditions2026{}
			msg.SetExplicitInt(0)
			msg.SetExplicitBool(false)
			msg.SetExplicitString("")
			msg.SetExplicitBytes([]byte{})
			msg.SetExplicitDouble(0.0)
			msg.SetStatus(pb.Editions2026Status_EDITIONS2026_STATUS_UNSPECIFIED)
			child := &pb.Editions2026Child{}
			child.SetId(0)
			child.SetNote("")
			msg.SetChild(child)
			msg.SetDelimitedChild(&pb.Editions2026Child{})
			msg.SetChoiceInt(0)
			return msg
		},
		func() editionsConformanceMsg {
			msg := &pb.TestEditions2026{}
			msg.SetExplicitInt(-42)
			msg.SetExplicitBool(true)
			msg.SetExplicitString("edition-2026-🚀")
			msg.SetExplicitBytes([]byte{0x00, 0xde, 0xad, 0xbe, 0xef})
			msg.SetExplicitDouble(-12.5)
			msg.SetStatus(pb.Editions2026Status(99))
			msg.SetImplicitCount(7)
			msg.SetImplicitFlag(true)
			c1 := &pb.Editions2026Child{}
			c1.SetId(101)
			c1.SetNote("length-prefixed")
			msg.SetChild(c1)
			c2 := &pb.Editions2026Child{}
			c2.SetId(202)
			c2.SetNote("delimited-wire-submsg")
			msg.SetDelimitedChild(c2)
			msg.SetPackedVals([]int32{-10, 0, 10, 100000})
			msg.SetExpandedVals([]int32{1, -2, 3})
			msg.SetScores(map[string]int32{"beta": -5, "alpha": 0, "🚀": 42})
			msg.SetChoiceText("")
			return msg
		},
	)...)

	return out
}

func buildEditionSuiteVectors(
	t *testing.T,
	year string,
	schema string,
	newEmpty func() editionsConformanceMsg,
	buildExplicitZeros func() editionsConformanceMsg,
	buildFull func() editionsConformanceMsg,
) []ConformanceVector {
	t.Helper()
	var out []ConformanceVector

	makeVec := func(name, desc string, raw []byte) ConformanceVector {
		t.Helper()
		blind, err := DecodeBlind(raw)
		if err != nil {
			t.Fatalf("[%s] DecodeBlind failed: %v", name, err)
		}
		return ConformanceVector{
			Name:          name,
			Category:      "P_ProtobufEditions",
			Description:   desc,
			Schema:        schema,
			WireHex:       hex.EncodeToString(raw),
			WireByteLen:   len(raw),
			PresentFields: inspectPresentFields(raw),
			ExpectedBlind: blind,
		}
	}

	verifyPluginAndReflect := func(name string, src editionsConformanceMsg) []byte {
		t.Helper()
		wirePlugin, err := src.TryMarshalOmniBufAppend(nil)
		if err != nil {
			t.Fatalf("[%s] TryMarshalOmniBufAppend failed: %v", name, err)
		}
		wireReflect, err := MarshalDeterministic(src)
		if err != nil {
			t.Fatalf("[%s] MarshalDeterministic failed: %v", name, err)
		}
		if !bytes.Equal(wirePlugin, wireReflect) {
			t.Fatalf("[%s] plugin vs reflect bytes mismatch:\nplugin:  %x\nreflect: %x", name, wirePlugin, wireReflect)
		}
		got := newEmpty()
		if err := got.UnmarshalOmniBuf(wirePlugin, false); err != nil {
			t.Fatalf("[%s] UnmarshalOmniBuf failed: %v", name, err)
		}
		reencoded, err := got.TryMarshalOmniBufAppend(nil)
		if err != nil {
			t.Fatalf("[%s] re-encode failed: %v", name, err)
		}
		if !bytes.Equal(wirePlugin, reencoded) {
			t.Fatalf("[%s] round-trip bytes mismatch:\nwant: %x\ngot:  %x", name, wirePlugin, reencoded)
		}
		return wirePlugin
	}

	// 1. Empty Editions message (0-byte input: all explicit fields absent)
	wireEmpty := []byte{}
	gotEmpty := newEmpty()
	if err := gotEmpty.UnmarshalOmniBuf(wireEmpty, false); err != nil {
		t.Fatalf("editions_%s_empty UnmarshalOmniBuf failed: %v", year, err)
	}
	reEmptyPlugin, err := gotEmpty.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("editions_%s_empty TryMarshalOmniBufAppend failed: %v", year, err)
	}
	reEmptyReflect, err := MarshalDeterministic(gotEmpty)
	if err != nil {
		t.Fatalf("editions_%s_empty MarshalDeterministic failed: %v", year, err)
	}
	if !bytes.Equal(reEmptyPlugin, reEmptyReflect) {
		t.Fatalf("editions_%s_empty plugin vs reflect mismatch", year)
	}
	for _, fnum := range inspectPresentFields(reEmptyPlugin) {
		if (fnum >= 1 && fnum <= 6) || fnum == 9 || fnum == 10 || (fnum >= 14 && fnum <= 17) {
			t.Fatalf("editions_%s_empty unexpectedly emitted explicit field #%d", year, fnum)
		}
	}
	out = append(out, makeVec(
		"editions_"+year+"_empty",
		"Edition "+year+" empty message with all explicit fields absent (0 bytes input; explicit fields must remain absent)",
		wireEmpty,
	))

	// 2. Explicit zeros (0, false, "", b"", 0.0, enum 0, explicit zero child, empty DELIMITED child, active zero oneof)
	wireZeros := verifyPluginAndReflect("editions_"+year+"_explicit_zeros", buildExplicitZeros())
	out = append(out, makeVec(
		"editions_"+year+"_explicit_zeros",
		"Edition "+year+" message preserving explicit 0, false, empty string/bytes, 0.0, enum 0, explicit-zero child, empty DELIMITED child, and zero oneof",
		wireZeros,
	))

	// 3. Mixed explicit zeros (#1,#2,#16) and implicit zeros (#7,#8) with other explicit fields absent
	encImp := NewEncoder(64)
	encImp.WriteInt(1, 0)       // explicit_int = 0 (MUST BE KEPT)
	encImp.WriteBool(2, false)  // explicit_bool = false (MUST BE KEPT)
	encImp.WriteInt(7, 0)       // implicit_count = 0 (IMPLICIT)
	encImp.WriteBool(8, false)  // implicit_flag = false (IMPLICIT)
	encImp.WriteBool(16, false) // choice_flag = false (active oneof, MUST BE KEPT)
	wireImp := append([]byte(nil), encImp.Finish()...)
	gotImp := newEmpty()
	if err := gotImp.UnmarshalOmniBuf(wireImp, false); err != nil {
		t.Fatalf("editions_%s_implicit_vs_explicit_presence UnmarshalOmniBuf failed: %v", year, err)
	}
	reImp, err := gotImp.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("editions_%s_implicit_vs_explicit_presence re-encode failed: %v", year, err)
	}
	hasField := make(map[uint32]bool)
	for _, fnum := range inspectPresentFields(reImp) {
		hasField[fnum] = true
	}
	if !hasField[1] || !hasField[2] || !hasField[16] {
		t.Fatalf("editions_%s_implicit_vs_explicit_presence lost explicit zero presence: %v", year, inspectPresentFields(reImp))
	}
	for _, absentF := range []uint32{3, 4, 5, 6, 9, 10, 14, 15, 17} {
		if hasField[absentF] {
			t.Fatalf("editions_%s_implicit_vs_explicit_presence emitted absent explicit field #%d", year, absentF)
		}
	}
	out = append(out, makeVec(
		"editions_"+year+"_implicit_vs_explicit_presence",
		"Edition "+year+" input carrying explicit zeros (#1,#2,#16) and implicit zeros (#7,#8) while other explicit fields remain absent",
		wireImp,
	))

	// 4. Full populated message with unknown open enum 99, DELIMITED child, PACKED & EXPANDED arrays, sorted map, empty string oneof
	wireFull := verifyPluginAndReflect("editions_"+year+"_full_and_open_enum", buildFull())
	out = append(out, makeVec(
		"editions_"+year+"_full_and_open_enum",
		"Edition "+year+" full message with unknown open enum 99, LENGTH_PREFIXED & DELIMITED children, PACKED & EXPANDED arrays, sorted map, and empty string oneof",
		wireFull,
	))

	// 5. Oneof last-wins across multiple branches ending in choice_child + negative unknown open enum (-7)
	encChild := NewEncoder(64)
	encChild.WriteInt(1, 303)
	encChild.WriteString(2, "winning-branch")
	childWire := append([]byte(nil), encChild.Finish()...)

	zigzag := func(v int64) uint64 {
		return uint64((v << 1) ^ (v >> 63))
	}
	var wireOneof []byte
	wireOneof = appendTag(wireOneof, 6, WireSint)
	wireOneof = AppendVarint(wireOneof, zigzag(-7))
	wireOneof = appendTag(wireOneof, 14, WireSint)
	wireOneof = AppendVarint(wireOneof, zigzag(77))
	wireOneof = appendTag(wireOneof, 15, WireString)
	wireOneof = AppendVarint(wireOneof, uint64(len("overwritten")))
	wireOneof = append(wireOneof, "overwritten"...)
	wireOneof = appendTag(wireOneof, 17, WireSubMsg)
	wireOneof = AppendVarint(wireOneof, uint64(len(childWire)))
	wireOneof = append(wireOneof, childWire...)

	gotOneof := newEmpty()
	if err := gotOneof.UnmarshalOmniBuf(wireOneof, false); err != nil {
		t.Fatalf("editions_%s_oneof_last_wins_and_child UnmarshalOmniBuf failed: %v", year, err)
	}
	reOneof, err := gotOneof.TryMarshalOmniBufAppend(nil)
	if err != nil {
		t.Fatalf("editions_%s_oneof_last_wins_and_child re-encode failed: %v", year, err)
	}
	hasOneof := make(map[uint32]bool)
	for _, fnum := range inspectPresentFields(reOneof) {
		hasOneof[fnum] = true
	}
	if !hasOneof[6] || !hasOneof[17] || hasOneof[14] || hasOneof[15] || hasOneof[16] {
		t.Fatalf("editions_%s_oneof_last_wins_and_child re-encoded fields = %v, want #6 and #17 only among explicit/oneof", year, inspectPresentFields(reOneof))
	}
	out = append(out, makeVec(
		"editions_"+year+"_oneof_last_wins_and_child",
		"Edition "+year+" message with negative open enum (-7) and multiple choice oneof branches verifying last-wins choice_child",
		wireOneof,
	))

	return out
}

func buildEditionsInvalidWireCases() []InvalidWireCase {
	cat := func(parts ...[]byte) []byte {
		var b []byte
		for _, p := range parts {
			b = append(b, p...)
		}
		return b
	}
	badUTF8 := cat(
		appendTag(nil, 3, WireString),
		AppendVarint(nil, 2),
		[]byte{0xFF, 0xFE},
	)
	truncDelimited := cat(
		appendTag(nil, 10, WireSubMsg),
		AppendVarint(nil, 10),
		[]byte{0x11, 0x02},
	)
	return []InvalidWireCase{
		{
			Name:        "editions_2023_invalid_utf8",
			Category:    "InvalidUTF8",
			Description: "Edition 2023 explicit_string field #3 containing invalid UTF-8 [0xFF, 0xFE] under utf8_validation = VERIFY",
			Schema:      "TestEditions2023",
			WireHex:     hex.EncodeToString(badUTF8),
		},
		{
			Name:        "editions_2024_invalid_utf8",
			Category:    "InvalidUTF8",
			Description: "Edition 2024 explicit_string field #3 containing invalid UTF-8 [0xFF, 0xFE] under utf8_validation = VERIFY",
			Schema:      "TestEditions2024",
			WireHex:     hex.EncodeToString(badUTF8),
		},
		{
			Name:        "editions_2026_invalid_utf8",
			Category:    "InvalidUTF8",
			Description: "Edition 2026 explicit_string field #3 containing invalid UTF-8 [0xFF, 0xFE] under utf8_validation = VERIFY",
			Schema:      "TestEditions2026",
			WireHex:     hex.EncodeToString(badUTF8),
		},
		{
			Name:        "editions_2026_truncated_delimited_child",
			Category:    "CorruptSubMsg",
			Description: "Edition 2026 delimited_child field #10 WireSubMsg claiming 10 bytes with only 2 payload bytes",
			Schema:      "TestEditions2026",
			WireHex:     hex.EncodeToString(truncDelimited),
		},
	}
}
