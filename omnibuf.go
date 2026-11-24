// Package omnibuf implements the OmniBuf high-throughput binary wire format.
//
// It supports three usage modes in Go:
//   - Plain Go structs annotated with `omnibuf:"<fieldNum>[,name=<name>][,wire=<wireType>][,omitempty]"`
//   - Existing protoc-gen-go generated structs (*pb.Message) and dynamicpb.Message via Marshal and Unmarshal
//   - Generated single-pass codecs and Opaque zero-decode lazy readers via protoc-gen-omnibuf (using github.com/omnibuf/omnibuf-go/wire)
package omnibuf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unsafe"

	"github.com/omnibuf/omnibuf-go/internal/protohook"
	"github.com/omnibuf/omnibuf-go/wire"
)

// Sentinel errors returned by OmniBuf encoders, decoders, and validators.
var (
	// ErrMaxDepth is returned when message or container nesting exceeds the configured depth limit.
	ErrMaxDepth = errors.New("omnibuf: nesting depth limit exceeded")

	// ErrInvalidUTF8 is returned when a WireString payload contains invalid UTF-8 bytes.
	ErrInvalidUTF8 = errors.New("omnibuf: invalid UTF-8 in WireString")

	// ErrCorruptWire is returned when an OmniBuf packet is truncated or structurally malformed.
	ErrCorruptWire = errors.New("omnibuf: corrupt wire packet")

	// ErrMessageTooLarge is returned when an input packet exceeds UnmarshalOptions.MaxBytes.
	ErrMessageTooLarge = errors.New("omnibuf: message exceeds maximum allowed byte size")
)

// UnmarshalOptions configures OmniBuf wire decoding.
type UnmarshalOptions struct {
	// MaxBytes rejects packets larger than MaxBytes bytes with ErrMessageTooLarge.
	// Zero disables the size cap.
	MaxBytes int

	// MaxDepth overrides the maximum allowed sub-message / container nesting depth.
	// Zero defaults to MaxNestingDepth (32).
	MaxDepth int

	// Merge preserves existing fields in the destination struct when they are absent
	// from the packet, merging sub-messages, appending slices, and merging maps (Spec §2).
	Merge bool

	// ZeroCopy returns unsafe string and byte-slice views backed by the packet buffer.
	// Do not modify or reuse the packet buffer while decoded values are still in use.
	ZeroCopy bool
}

func (o UnmarshalOptions) effectiveMaxDepth() int {
	if o.MaxDepth > 0 {
		return o.MaxDepth
	}
	return MaxNestingDepth
}

// Unmarshal decodes packet into v using the configured options.
func (o UnmarshalOptions) Unmarshal(packet []byte, v any) error {
	return unmarshalTableWithOptions(packet, v, o)
}

// UnmarshalMerge decodes packet into v while preserving existing fields not present in packet.
func UnmarshalMerge(packet []byte, v any) error {
	return UnmarshalOptions{Merge: true}.Unmarshal(packet, v)
}

// MarshalOptions configures OmniBuf wire encoding.
type MarshalOptions struct {
	// Deterministic sorts wireMap keys in ascending order so identical messages
	// always produce byte-for-byte identical wire output.
	Deterministic bool

	// SelfDescribing emits Field #0 (Tag = 0x19, wireSubMsg = 12, omnibuf.v1.SchemaMeta)
	// at byte 0 of the root message with field names and nested children schemas.
	SelfDescribing bool
}

// Marshal encodes v using the configured options.
func (o MarshalOptions) Marshal(v any) ([]byte, error) {
	return o.MarshalAppend(nil, v)
}

// MarshalAppend appends the OmniBuf encoding of v to dst using the configured options.
func (o MarshalOptions) MarshalAppend(dst []byte, v any) ([]byte, error) {
	if protohook.MarshalHook != nil {
		if out, handled, err := protohook.MarshalHook(dst, v, o.Deterministic, o.SelfDescribing); handled {
			return out, err
		}
	}
	return marshalTableAppend(dst, v, o)
}

// MarshalDeterministic encodes v with deterministic wireMap key ordering.
func MarshalDeterministic(v any) ([]byte, error) {
	return MarshalOptions{Deterministic: true}.Marshal(v)
}

// MaxNestingDepth is the maximum allowed sub-message / array nesting depth (prevents stack-overflow bombs).
const MaxNestingDepth = 32

const maxProtobufFieldNumber = 536870911

func fastSkipValue(packet []byte, pos int, wt uint8) (int, bool) {
	n := len(packet)
	switch wt {
	case wire.WireUint, wire.WireSint:
		if uint(pos) >= uint(n) {
			return 0, false
		}
		vb := packet[pos]
		if (vb & 1) != 0 {
			return pos + 1, true
		} else if (vb & 2) != 0 {
			return pos + 2, n-pos >= 2
		} else if (vb & 4) != 0 {
			return pos + 4, n-pos >= 4
		} else if vb == 0 {
			return pos + 9, n-pos >= 9
		}
		return 0, false
	case wire.WireBoolFalse, wire.WireBoolTrue:
		return pos, uint(pos) <= uint(n)
	case wire.WireString, wire.WireBytes, wire.WireSubMsg, wire.WireArray, wire.WireMap, wire.WireExtended:
		if uint(pos) >= uint(n) {
			return 0, false
		}
		lb := packet[pos]
		if (lb & 1) != 0 {
			l := int(lb >> 1)
			return pos + 1 + l, n-pos-1 >= l
		}
		if (lb & 2) != 0 {
			if n-pos < 2 {
				return 0, false
			}
			l := int(binary.LittleEndian.Uint16(packet[pos:pos+2]) >> 2)
			return pos + 2 + l, n-pos-2 >= l
		}
		return skipLargeContainer(packet, pos)
	case wire.WireFixed32, wire.WireFloat32, wire.WireSfixed32:
		if uint(pos) > uint(n) || n-pos < 4 {
			return 0, false
		}
		return pos + 4, true
	case wire.WireFixed64, wire.WireFloat64, wire.WireSfixed64:
		if uint(pos) > uint(n) || n-pos < 8 {
			return 0, false
		}
		return pos + 8, true
	default:
		return 0, false
	}
}

//go:noinline
func skipLargeContainer(packet []byte, pos int) (int, bool) {
	bLen, bn := wire.DecodeVarint(packet[pos:])
	if bn == 0 || bLen > uint64(len(packet)-(pos+bn)) {
		return 0, false
	}
	pos += bn + int(bLen)
	return pos, pos <= len(packet)
}

// ValidateValue validates a single field payload starting at data[pos] with wire type wt
// at the given container nesting depth, enforcing MaxNestingDepth and full inner validation.
func ValidateValue(data []byte, pos int, wt uint8, depth int) (int, error) {
	return skipValueWithMaxDepthChecked(data, pos, wt, depth, MaxNestingDepth, false)
}

// ValidateValueWithMaxDepth validates a single field payload starting at data[pos] with wire type wt
// at the given container nesting depth, enforcing maxDepth and full inner validation.
func ValidateValueWithMaxDepth(data []byte, pos int, wt uint8, depth, maxDepth int) (int, error) {
	if maxDepth <= 0 {
		maxDepth = MaxNestingDepth
	}
	return skipValueWithMaxDepthChecked(data, pos, wt, depth, maxDepth, false)
}

func skipValue(b []byte, pos int, wireType uint8) (int, error) {
	return skipValueWithMaxDepthChecked(b, pos, wireType, 0, MaxNestingDepth, false)
}

func skipValueWithMaxDepthChecked(b []byte, pos int, wireType uint8, depth, maxDepth int, checkUTF8 bool) (int, error) {
	if depth > maxDepth {
		return 0, fmt.Errorf("%w: nesting depth %d exceeds %d", ErrMaxDepth, depth, maxDepth)
	}
	if pos < 0 || pos > len(b) {
		return 0, fmt.Errorf("%w: position %d out of bounds", ErrCorruptWire, pos)
	}
	if wireType == wire.WireSubMsg {
		body, end, err := readLenSlice(b, pos, 0)
		if err != nil {
			return 0, err
		}
		if err := validateMessageBytesWithMaxDepthChecked(body, depth+1, maxDepth, checkUTF8); err != nil {
			return 0, err
		}
		return end, nil
	}
	if wireType == wire.WireArray || wireType == wire.WireMap || wireType == wire.WireExtended {
		length, ln := wire.DecodeVarint(b[pos:])
		if ln == 0 || length < 1 || length > uint64(len(b)-pos-ln) {
			return 0, fmt.Errorf("%w: invalid container length", ErrCorruptWire)
		}
		end := pos + ln + int(length)
		body := b[pos+ln : end]
		switch wireType {
		case wire.WireArray:
			if depth+1 > maxDepth {
				return 0, fmt.Errorf("%w: nesting depth %d exceeds %d", ErrMaxDepth, depth+1, maxDepth)
			}
			elemWT, count, p, ok := parseArrayHeader(body)
			if !ok {
				return 0, fmt.Errorf("%w: invalid array header", ErrCorruptWire)
			}
			if err := validateArrayBodyWithMaxDepthChecked(body, p, elemWT, count, depth+1, maxDepth, checkUTF8); err != nil {
				return 0, err
			}
		case wire.WireMap:
			if depth+1 > maxDepth {
				return 0, fmt.Errorf("%w: nesting depth %d exceeds %d", ErrMaxDepth, depth+1, maxDepth)
			}
			keyWT, valWT, count, p, ok := parseMapHeader(body)
			if !ok {
				return 0, fmt.Errorf("%w: invalid map header", ErrCorruptWire)
			}
			for range count {
				var err error
				p, err = validateContainerEntryWithMaxDepthChecked(body, p, keyWT, depth+1, maxDepth, checkUTF8)
				if err != nil {
					return 0, err
				}
				p, err = validateContainerEntryWithMaxDepthChecked(body, p, valWT, depth+1, maxDepth, checkUTF8)
				if err != nil {
					return 0, err
				}
			}
			if p != len(body) {
				return 0, fmt.Errorf("%w: trailing map bytes", ErrCorruptWire)
			}
		case wire.WireExtended:
			// ByteLen >= 1 (checked above) already guarantees the 1-byte ExtType header is present.
		}
		return end, nil
	}

	if checkUTF8 && wireType == wire.WireString {
		_, end, err := readStrSlice(b, pos)
		return end, err
	}
	nextPos, ok := fastSkipValue(b, pos, wireType)
	if !ok {
		return 0, fmt.Errorf("%w: truncated or invalid wireType %d at %d", ErrCorruptWire, wireType, pos)
	}
	return nextPos, nil
}

func validateArrayBodyWithMaxDepthChecked(body []byte, p int, elemWT uint8, count, depth, maxDepth int, checkUTF8 bool) error {
	switch elemWT {
	case wire.WireBoolFalse:
		if len(body)-p != count {
			return fmt.Errorf("%w: invalid bool array length", ErrCorruptWire)
		}
		for i := range count {
			if body[p+i] > 1 {
				return fmt.Errorf("%w: invalid bool byte in array", ErrCorruptWire)
			}
		}
		return nil
	case wire.WireFixed32, wire.WireSfixed32, wire.WireFloat32:
		if len(body)-p != count*4 {
			return fmt.Errorf("%w: invalid 32-bit array length", ErrCorruptWire)
		}
		return nil
	case wire.WireFixed64, wire.WireSfixed64, wire.WireFloat64:
		if len(body)-p != count*8 {
			return fmt.Errorf("%w: invalid 64-bit array length", ErrCorruptWire)
		}
		return nil
	}
	for range count {
		var err error
		p, err = validateContainerEntryWithMaxDepthChecked(body, p, elemWT, depth, maxDepth, checkUTF8)
		if err != nil {
			return err
		}
	}
	if p != len(body) {
		return fmt.Errorf("%w: trailing array bytes", ErrCorruptWire)
	}
	return nil
}

func validateContainerEntryChecked(body []byte, p int, wt uint8, depth int, checkUTF8 bool) (int, error) {
	return validateContainerEntryWithMaxDepthChecked(body, p, wt, depth, MaxNestingDepth, checkUTF8)
}

func validateContainerEntryWithMaxDepthChecked(body []byte, p int, wt uint8, depth, maxDepth int, checkUTF8 bool) (int, error) {
	if wt == wire.WireBoolFalse {
		if p >= len(body) || body[p] > 1 {
			return 0, fmt.Errorf("%w: invalid bool byte in container", ErrCorruptWire)
		}
		return p + 1, nil
	}
	return skipValueWithMaxDepthChecked(body, p, wt, depth, maxDepth, checkUTF8)
}

func validateMessageBytes(data []byte, depth int) error {
	return validateMessageBytesWithMaxDepthChecked(data, depth, MaxNestingDepth, false)
}

func validateMessageBytesWithMaxDepthChecked(data []byte, depth, maxDepth int, checkUTF8 bool) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: nesting depth %d exceeds %d", ErrMaxDepth, depth, maxDepth)
	}
	pos := 0
	n := len(data)
	for pos < n {
		tag, tn := wire.DecodeVarint(data[pos:])
		if tn == 0 || tag>>4 > maxProtobufFieldNumber {
			return fmt.Errorf("%w: invalid or truncated tag", ErrCorruptWire)
		}
		pos += tn
		fnum := tag >> 4
		wt := uint8(tag & 0x0F)
		if fnum == 0 {
			nextPos, err := validateField0WithMaxDepth(data, pos, wt, depth, maxDepth)
			if err != nil {
				return err
			}
			pos = nextPos
			continue
		}
		nextPos, err := skipValueWithMaxDepthChecked(data, pos, wt, depth, maxDepth, checkUTF8)
		if err != nil {
			return err
		}
		pos = nextPos
	}
	return nil
}

func nextContainerField(packet []byte, pos int, targetField uint16, expWT uint8, minLen uint64) (body []byte, nextPos int, matched bool, valid bool) {
	n := len(packet)
	for pos < n {
		var fnum uint32
		var wt uint8
		b0 := packet[pos]
		if (b0 & 1) != 0 {
			fnum = uint32(b0 >> 5)
			wt = (b0 >> 1) & 0x0F
			pos++
		} else if (b0 & 2) != 0 {
			if pos+2 > n {
				return nil, 0, false, false
			}
			u := binary.LittleEndian.Uint16(packet[pos:pos+2]) >> 2
			fnum = uint32(u >> 4)
			wt = uint8(u & 0x0F)
			pos += 2
		} else {
			tag, tn := wire.DecodeVarint(packet[pos:])
			if tn == 0 || tag>>4 > maxProtobufFieldNumber {
				return nil, 0, false, false
			}
			fnum = uint32(tag >> 4)
			wt = uint8(tag & 0x0F)
			pos += tn
		}

		if fnum == 0 {
			if wt != wire.WireSubMsg {
				return nil, 0, false, false
			}
			if targetField != 0 {
				var err error
				pos, err = validateField0(packet, pos, wt, 0)
				if err != nil {
					return nil, 0, false, false
				}
				continue
			}
		}
		if fnum == uint32(targetField) {
			if wt != expWT {
				return nil, 0, false, false
			}
			bLen, bn := wire.DecodeVarint(packet[pos:])
			if bn == 0 || bLen < minLen || bLen > uint64(n-(pos+bn)) {
				return nil, 0, false, false
			}
			pos += bn
			return packet[pos : pos+int(bLen)], pos + int(bLen), true, true
		}

		var err error
		pos, err = skipValue(packet, pos, wt)
		if err != nil {
			return nil, 0, false, false
		}
	}
	return nil, n, false, true
}

func sortUint32Slice(s []uint32) {
	if len(s) <= 32 {
		for i := 1; i < len(s); i++ {
			v := s[i]
			j := i - 1
			for j >= 0 && s[j] > v {
				s[j+1] = s[j]
				j--
			}
			s[j+1] = v
		}
		return
	}
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

func decodeStr(b []byte, zeroCopy bool) string {
	if len(b) == 0 {
		return ""
	}
	if zeroCopy {
		return unsafe.String(&b[0], len(b))
	}
	return string(b)
}

func readLenSlice(data []byte, pos int, minLen uint64) (body []byte, nextPos int, err error) {
	bLen, bn := wire.DecodeVarint(data[pos:])
	if bn == 0 {
		return nil, 0, fmt.Errorf("%w: truncated length varint at %d", ErrCorruptWire, pos)
	}
	pos += bn
	if bLen < minLen || bLen > uint64(len(data)-pos) {
		return nil, 0, fmt.Errorf("%w: invalid byteLen %d at %d", ErrCorruptWire, bLen, pos)
	}
	end := pos + int(bLen)
	return data[pos:end:end], end, nil
}

// readStrSlice is readLenSlice for WireString payloads: it additionally
// rejects invalid UTF-8 as required by the spec (binary data uses WireBytes).
func readStrSlice(data []byte, pos int) (body []byte, nextPos int, err error) {
	body, nextPos, err = readLenSlice(data, pos, 0)
	if err != nil {
		return nil, 0, err
	}
	if !wire.ValidUTF8(body) {
		return nil, 0, fmt.Errorf("%w at %d", ErrInvalidUTF8, pos)
	}
	return body, nextPos, nil
}

func countSubMsgArrElems(body []byte) (int, bool) {
	e, count, p, ok := parseArrayHeader(body)
	if !ok || e != wire.WireSubMsg {
		return 0, false
	}
	for i := 0; i < count; i++ {
		l, n := wire.DecodeVarint(body[p:])
		if n == 0 || l > uint64(len(body)-p-n) {
			return 0, false
		}
		p += n + int(l)
	}
	return count, p == len(body)
}
