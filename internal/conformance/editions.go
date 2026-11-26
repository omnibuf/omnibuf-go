package conformance

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"

	"github.com/omnibuf/omnibuf-go/wire"
)

type editionsChildState struct {
	hasID   bool
	id      int32
	hasNote bool
	note    string
}

type editionsMsgState struct {
	hasExplicitInt    bool
	explicitInt       int32
	hasExplicitBool   bool
	explicitBool      bool
	hasExplicitString bool
	explicitString    string
	hasExplicitBytes  bool
	explicitBytes     []byte
	hasExplicitDouble bool
	explicitDouble    float64
	hasStatus         bool
	status            int32

	implicitCount int32
	implicitFlag  bool

	hasChild          bool
	child             editionsChildState
	hasDelimitedChild bool
	delimitedChild    editionsChildState

	packedVals   []int32
	expandedVals []int32
	scores       map[string]int32

	activeChoice uint32 // 0 = none, 14 = int, 15 = text, 16 = flag, 17 = child
	choiceInt    int32
	choiceText   string
	choiceFlag   bool
	choiceChild  editionsChildState
}

func zigzagDecodeInt32(u uint64) int32 {
	return int32((u >> 1) ^ -(u & 1))
}

func decodeWireSignedInt32(data []byte, pos int, wt uint8) (int32, int, error) {
	v, n := wire.DecodeVarint(data[pos:])
	if n <= 0 {
		return 0, 0, fmt.Errorf("invalid varint at offset %d", pos)
	}
	switch wt {
	case wire.WireSint:
		return zigzagDecodeInt32(v), pos + n, nil
	case wire.WireUint:
		return int32(v), pos + n, nil
	default:
		return 0, 0, fmt.Errorf("unexpected integer wire type %d at offset %d", wt, pos)
	}
}

func decodeWireLenBytes(data []byte, pos int) ([]byte, int, error) {
	bLen, bn := wire.DecodeVarint(data[pos:])
	if bn <= 0 || bLen > uint64(len(data)-pos-bn) {
		return nil, 0, fmt.Errorf("invalid length-prefixed field at offset %d", pos)
	}
	start := pos + bn
	end := start + int(bLen)
	return data[start:end], end, nil
}

func decodeEditionsChild(data []byte, dst *editionsChildState) error {
	pos := 0
	for pos < len(data) {
		tag, tn := wire.DecodeVarint(data[pos:])
		if tn <= 0 {
			return fmt.Errorf("invalid child tag at offset %d", pos)
		}
		pos += tn
		fnum := uint32(tag >> 4)
		wt := uint8(tag & 0x0F)
		switch fnum {
		case 0:
			next, err := wire.SkipValue(data, pos, wt)
			if err != nil {
				return err
			}
			pos = next
		case 1:
			v, next, err := decodeWireSignedInt32(data, pos, wt)
			if err != nil {
				return err
			}
			dst.hasID = true
			dst.id = v
			pos = next
		case 2:
			if wt != wire.WireString {
				return fmt.Errorf("child field 2 expected WireString, got %d", wt)
			}
			raw, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return err
			}
			dst.hasNote = true
			dst.note = string(raw)
			pos = next
		default:
			next, err := wire.SkipValue(data, pos, wt)
			if err != nil {
				return err
			}
			pos = next
		}
	}
	return nil
}

func decodeEditionsWire(data []byte, strictOutput bool) (*editionsMsgState, error) {
	st := &editionsMsgState{}
	oneofSeen := 0
	pos := 0
	for pos < len(data) {
		tag, tn := wire.DecodeVarint(data[pos:])
		if tn <= 0 {
			return nil, fmt.Errorf("invalid tag varint at offset %d", pos)
		}
		pos += tn
		fnum := uint32(tag >> 4)
		wt := uint8(tag & 0x0F)

		switch fnum {
		case 0:
			next, err := wire.SkipValue(data, pos, wt)
			if err != nil {
				return nil, err
			}
			pos = next
		case 1:
			v, next, err := decodeWireSignedInt32(data, pos, wt)
			if err != nil {
				return nil, err
			}
			st.hasExplicitInt = true
			st.explicitInt = v
			pos = next
		case 2:
			if wt != wire.WireBoolFalse && wt != wire.WireBoolTrue {
				return nil, fmt.Errorf("field 2 expected bool wire type, got %d", wt)
			}
			st.hasExplicitBool = true
			st.explicitBool = wt == wire.WireBoolTrue
		case 3:
			if wt != wire.WireString {
				return nil, fmt.Errorf("field 3 expected WireString, got %d", wt)
			}
			raw, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return nil, err
			}
			st.hasExplicitString = true
			st.explicitString = string(raw)
			pos = next
		case 4:
			if wt != wire.WireBytes {
				return nil, fmt.Errorf("field 4 expected WireBytes, got %d", wt)
			}
			raw, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return nil, err
			}
			st.hasExplicitBytes = true
			st.explicitBytes = append([]byte(nil), raw...)
			pos = next
		case 5:
			if wt != wire.WireFloat64 {
				return nil, fmt.Errorf("field 5 expected WireFloat64, got %d", wt)
			}
			if pos+8 > len(data) {
				return nil, fmt.Errorf("truncated float64 at offset %d", pos)
			}
			st.hasExplicitDouble = true
			st.explicitDouble = math.Float64frombits(binary.LittleEndian.Uint64(data[pos : pos+8]))
			pos += 8
		case 6:
			v, next, err := decodeWireSignedInt32(data, pos, wt)
			if err != nil {
				return nil, err
			}
			st.hasStatus = true
			st.status = v
			pos = next
		case 7:
			v, next, err := decodeWireSignedInt32(data, pos, wt)
			if err != nil {
				return nil, err
			}
			st.implicitCount = v
			pos = next
		case 8:
			if wt != wire.WireBoolFalse && wt != wire.WireBoolTrue {
				return nil, fmt.Errorf("field 8 expected bool wire type, got %d", wt)
			}
			st.implicitFlag = wt == wire.WireBoolTrue
		case 9:
			if wt != wire.WireSubMsg {
				return nil, fmt.Errorf("field 9 expected WireSubMsg, got %d", wt)
			}
			raw, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return nil, err
			}
			st.hasChild = true
			if err := decodeEditionsChild(raw, &st.child); err != nil {
				return nil, err
			}
			pos = next
		case 10:
			if wt != wire.WireSubMsg {
				return nil, fmt.Errorf("field 10 (delimited_child) expected WireSubMsg, got %d", wt)
			}
			raw, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return nil, err
			}
			st.hasDelimitedChild = true
			if err := decodeEditionsChild(raw, &st.delimitedChild); err != nil {
				return nil, err
			}
			pos = next
		case 11, 12:
			if wt != wire.WireArray {
				return nil, fmt.Errorf("field %d expected WireArray, got %d", fnum, wt)
			}
			body, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return nil, err
			}
			if len(body) < 2 {
				return nil, fmt.Errorf("truncated WireArray header on field %d", fnum)
			}
			elemWT := body[0]
			count, cn := wire.DecodeVarint(body[1:])
			if cn <= 0 {
				return nil, fmt.Errorf("invalid WireArray count on field %d", fnum)
			}
			p := 1 + cn
			for range count {
				v, np, err := decodeWireSignedInt32(body, p, elemWT)
				if err != nil {
					return nil, err
				}
				if fnum == 11 {
					st.packedVals = append(st.packedVals, v)
				} else {
					st.expandedVals = append(st.expandedVals, v)
				}
				p = np
			}
			if p != len(body) {
				return nil, fmt.Errorf("trailing bytes in WireArray on field %d", fnum)
			}
			pos = next
		case 13:
			if wt != wire.WireMap {
				return nil, fmt.Errorf("field 13 expected WireMap, got %d", wt)
			}
			body, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return nil, err
			}
			keyWT, valWT, count, p, ok := wire.ParseMapHeader(body)
			if !ok || keyWT != wire.WireString {
				return nil, fmt.Errorf("invalid scores WireMap header")
			}
			if st.scores == nil {
				st.scores = make(map[string]int32, count)
			}
			for range count {
				kRaw, kp, err := decodeWireLenBytes(body, p)
				if err != nil {
					return nil, err
				}
				val, vp, err := decodeWireSignedInt32(body, kp, valWT)
				if err != nil {
					return nil, err
				}
				st.scores[string(kRaw)] = val
				p = vp
			}
			if p != len(body) {
				return nil, fmt.Errorf("trailing bytes in WireMap on field 13")
			}
			pos = next
		case 14:
			oneofSeen++
			v, next, err := decodeWireSignedInt32(data, pos, wt)
			if err != nil {
				return nil, err
			}
			st.activeChoice = 14
			st.choiceInt = v
			st.choiceText = ""
			st.choiceFlag = false
			st.choiceChild = editionsChildState{}
			pos = next
		case 15:
			oneofSeen++
			if wt != wire.WireString {
				return nil, fmt.Errorf("field 15 expected WireString, got %d", wt)
			}
			raw, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return nil, err
			}
			st.activeChoice = 15
			st.choiceInt = 0
			st.choiceText = string(raw)
			st.choiceFlag = false
			st.choiceChild = editionsChildState{}
			pos = next
		case 16:
			oneofSeen++
			if wt != wire.WireBoolFalse && wt != wire.WireBoolTrue {
				return nil, fmt.Errorf("field 16 expected bool wire type, got %d", wt)
			}
			st.activeChoice = 16
			st.choiceInt = 0
			st.choiceText = ""
			st.choiceFlag = wt == wire.WireBoolTrue
			st.choiceChild = editionsChildState{}
		case 17:
			oneofSeen++
			if wt != wire.WireSubMsg {
				return nil, fmt.Errorf("field 17 expected WireSubMsg, got %d", wt)
			}
			raw, next, err := decodeWireLenBytes(data, pos)
			if err != nil {
				return nil, err
			}
			if st.activeChoice != 17 {
				st.choiceChild = editionsChildState{}
			}
			st.activeChoice = 17
			st.choiceInt = 0
			st.choiceText = ""
			st.choiceFlag = false
			if err := decodeEditionsChild(raw, &st.choiceChild); err != nil {
				return nil, err
			}
			pos = next
		default:
			next, err := wire.SkipValue(data, pos, wt)
			if err != nil {
				return nil, err
			}
			pos = next
		}
	}
	if strictOutput && oneofSeen > 1 {
		return nil, fmt.Errorf("output wire emitted %d active choice oneof branches (want at most 1)", oneofSeen)
	}
	return st, nil
}

func verifyEditionsRoundTrip(schema string, inputWire, outputWire []byte) error {
	want, err := decodeEditionsWire(inputWire, false)
	if err != nil {
		return fmt.Errorf("runner decode input %s: %w", schema, err)
	}
	got, err := decodeEditionsWire(outputWire, true)
	if err != nil {
		return fmt.Errorf("runner decode output %s: %w", schema, err)
	}
	if want.hasExplicitInt != got.hasExplicitInt || want.explicitInt != got.explicitInt ||
		want.hasExplicitBool != got.hasExplicitBool || want.explicitBool != got.explicitBool ||
		want.hasExplicitString != got.hasExplicitString || want.explicitString != got.explicitString ||
		want.hasExplicitBytes != got.hasExplicitBytes || !bytes.Equal(want.explicitBytes, got.explicitBytes) ||
		want.hasExplicitDouble != got.hasExplicitDouble || mathFloat64Bits(want.explicitDouble) != mathFloat64Bits(got.explicitDouble) ||
		want.hasStatus != got.hasStatus || want.status != got.status ||
		want.implicitCount != got.implicitCount || want.implicitFlag != got.implicitFlag ||
		want.hasChild != got.hasChild || want.child != got.child ||
		want.hasDelimitedChild != got.hasDelimitedChild || want.delimitedChild != got.delimitedChild ||
		want.activeChoice != got.activeChoice ||
		want.choiceInt != got.choiceInt || want.choiceText != got.choiceText ||
		want.choiceFlag != got.choiceFlag || want.choiceChild != got.choiceChild {
		return fmt.Errorf("%s round-trip mismatch:\ngot:  %+v\nwant: %+v", schema, got, want)
	}
	if len(want.packedVals) != len(got.packedVals) || (len(want.packedVals) > 0 && !reflect.DeepEqual(want.packedVals, got.packedVals)) {
		return fmt.Errorf("%s packed_vals mismatch: got %v, want %v", schema, got.packedVals, want.packedVals)
	}
	if len(want.expandedVals) != len(got.expandedVals) || (len(want.expandedVals) > 0 && !reflect.DeepEqual(want.expandedVals, got.expandedVals)) {
		return fmt.Errorf("%s expanded_vals mismatch: got %v, want %v", schema, got.expandedVals, want.expandedVals)
	}
	if len(want.scores) != len(got.scores) || (len(want.scores) > 0 && !reflect.DeepEqual(want.scores, got.scores)) {
		return fmt.Errorf("%s scores mismatch: got %v, want %v", schema, got.scores, want.scores)
	}
	return nil
}
