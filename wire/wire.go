// Package wire provides low-level OmniBuf wire format primitives, O(1) zero-decode
// field hoppers, the streaming Encoder, and runtime helpers for protoc-gen-omnibuf.
package wire

import (
	"encoding/binary"
	"fmt"
	"slices"
)

// Explicit 4-bit Wire Types (0..15) encoded in the low 4 bits of the field tag:
// Tag = (FieldNumber << 4) | WireType
const (
	WireUint      uint8 = 0
	WireSint      uint8 = 1
	WireBoolFalse uint8 = 2
	WireBoolTrue  uint8 = 3
	WireFixed32   uint8 = 4
	WireFixed64   uint8 = 5
	WireSfixed32  uint8 = 6
	WireSfixed64  uint8 = 7
	WireFloat32   uint8 = 8
	WireFloat64   uint8 = 9
	WireString    uint8 = 10
	WireBytes     uint8 = 11
	WireSubMsg    uint8 = 12
	WireArray     uint8 = 13
	WireMap       uint8 = 14
	WireExtended  uint8 = 15
)

const maxNestingDepth = 32

// EncodeZigZag maps signed integers to unsigned integers so small negative values stay compact.
func EncodeZigZag(n int64) uint64 {
	return uint64((n << 1) ^ (n >> 63))
}

// DecodeZigZag maps a ZigZag-encoded unsigned integer back to a signed int64.
func DecodeZigZag(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// VarintSize returns the encoded byte length (1, 2, 4, or 9) of a Prefix-Varint.
func VarintSize(v uint64) int {
	if v <= 127 {
		return 1
	}
	if v <= 16383 {
		return 2
	}
	if v <= 536870911 {
		return 4
	}
	return 9
}

// PutVarintAt writes a Prefix-Varint into dst starting at pos and returns the next offset.
// Caller must ensure len(dst)-pos >= VarintSize(v).
func PutVarintAt(dst []byte, pos int, v uint64) int {
	if v <= 127 {
		dst[pos] = byte(v<<1) | 1
		return pos + 1
	}
	if v <= 16383 {
		binary.LittleEndian.PutUint16(dst[pos:], uint16(v<<2)|2)
		return pos + 2
	}
	if v <= 536870911 {
		binary.LittleEndian.PutUint32(dst[pos:], uint32(v<<3)|4)
		return pos + 4
	}
	dst[pos] = 0
	binary.LittleEndian.PutUint64(dst[pos+1:], v)
	return pos + 9
}

// PutVarint writes a Prefix-Varint into dst and returns the number of bytes written (1, 2, 4, or 9).
// Caller must ensure len(dst) >= VarintSize(v).
func PutVarint(dst []byte, v uint64) int {
	return PutVarintAt(dst, 0, v)
}

// AppendVarint appends a Prefix-Varint to dst and returns the extended slice.
// Fast-pathed for 1-byte values (0..127) so the Go compiler inlines it at every call site.
func AppendVarint(dst []byte, v uint64) []byte {
	if v <= 127 {
		return append(dst, byte(v<<1)|1)
	}
	return appendVarintMulti(dst, v)
}

//go:noinline
func appendVarintMulti(dst []byte, v uint64) []byte {
	if v <= 16383 {
		u := uint16(v<<2) | 2
		return append(dst, byte(u), byte(u>>8))
	}
	if v <= 536870911 {
		u := uint32(v<<3) | 4
		return append(dst, byte(u), byte(u>>8), byte(u>>16), byte(u>>24))
	}
	return append(dst,
		0x00,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56),
	)
}

// DecodeVarint decodes a Prefix-Varint from b using first-byte bit tests
// and returns (value, bytesRead).
// Keep this hot path inlineable; just go-inline-check guards the compiler decision.
func DecodeVarint(b []byte) (v uint64, n int) {
	if len(b) == 0 {
		return
	}
	b0 := b[0]
	if b0&1 != 0 {
		return uint64(b0 >> 1), 1
	}
	if b0&2 != 0 && len(b) >= 2 {
		return uint64(binary.LittleEndian.Uint16(b) >> 2), 2
	}
	if b0&4 != 0 && len(b) >= 4 {
		return uint64(binary.LittleEndian.Uint32(b) >> 3), 4
	}
	// A nine-byte varint must start with zero; other low-three-bit-zero prefixes are reserved.
	if b0 == 0 && len(b) >= 9 {
		return binary.LittleEndian.Uint64(b[1:]), 9
	}
	return
}

// ReadVarintAt decodes a Prefix-Varint from b[pos:] without constructing a sub-slice.
// It performs no validation: callers MUST already know that b[pos:] holds a
// complete, well-formed varint (use DecodeVarint for untrusted input).
func ReadVarintAt(b []byte, pos int) (uint64, int) {
	b0 := b[pos]
	if (b0 & 1) != 0 {
		return uint64(b0 >> 1), pos + 1
	}
	if (b0 & 2) != 0 {
		return uint64(binary.LittleEndian.Uint16(b[pos:]) >> 2), pos + 2
	}
	if (b0 & 4) != 0 {
		return uint64(binary.LittleEndian.Uint32(b[pos:]) >> 3), pos + 4
	}
	return binary.LittleEndian.Uint64(b[pos+1:]), pos + 9
}

// PutOverlong2BVarint encodes a length (0..16383) as an explicit 2-byte Prefix-Varint
// (TZCNT=1: uint16(v<<2)|2) for single-pass sub-message and container length backpatching.
func PutOverlong2BVarint(dst []byte, v uint16) {
	binary.LittleEndian.PutUint16(dst[:2], (v<<2)|2)
}

// Max2BContainerLen is the largest payload length (16,383 bytes) that fits in a 2-byte Prefix-Varint.
const Max2BContainerLen = 16383

// BackpatchContainerLen fills in the container byte length at dst[lenPos:lenPos+2]
// for the common fast path (subLen <= 16383). If subLen > 16383, it expands the
// reserved 2-byte slot to a 4-byte Prefix-Varint (up to 512 MB) or 9-byte Prefix-Varint
// by shifting the container body right in place.
func BackpatchContainerLen(dst []byte, lenPos int) []byte {
	if b := dst[lenPos:]; len(b) <= 16385 {
		binary.LittleEndian.PutUint16(b, uint16(len(b)<<2-6))
		return dst
	}
	return backpatchLargeContainerLen(dst, lenPos)
}

// BackpatchContainerLen4 fills a pre-reserved 4-byte Prefix-Varint container length slot
// at dst[lenPos:lenPos+4] when a lower-bound size check proved subLen > 16383 up front.
func BackpatchContainerLen4(dst []byte, lenPos int) []byte {
	subLen := uint64(len(dst) - (lenPos + 4))
	if subLen <= 536870911 {
		binary.LittleEndian.PutUint32(dst[lenPos:lenPos+4], uint32(subLen<<3)|4)
		return dst
	}
	return backpatchHugeContainerLen4(dst, lenPos, subLen)
}

//go:noinline
func backpatchHugeContainerLen4(dst []byte, lenPos int, subLen uint64) []byte {
	dst = append(dst, 0, 0, 0, 0, 0)
	copy(dst[lenPos+9:], dst[lenPos+4:len(dst)-5])
	dst[lenPos] = 0
	binary.LittleEndian.PutUint64(dst[lenPos+1:lenPos+9], subLen)
	return dst
}

//go:noinline
func backpatchLargeContainerLen(dst []byte, lenPos int) []byte {
	subLen := uint64(len(dst) - (lenPos + 2))
	if subLen <= 536870911 {
		dst = append(dst, 0, 0)
		copy(dst[lenPos+4:], dst[lenPos+2:len(dst)-2])
		binary.LittleEndian.PutUint32(dst[lenPos:lenPos+4], uint32(subLen<<3)|4)
		return dst
	}
	dst = append(dst, 0, 0, 0, 0, 0, 0, 0)
	copy(dst[lenPos+9:], dst[lenPos+2:len(dst)-7])
	dst[lenPos] = 0
	binary.LittleEndian.PutUint64(dst[lenPos+1:lenPos+9], subLen)
	return dst
}

// fastSkipValue advances pos past a field value of wire type wt in O(1) time without error string allocation.
func fastSkipValue(packet []byte, pos int, wt uint8) (int, bool) {
	n := len(packet)
	switch wt {
	case WireUint, WireSint:
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
	case WireBoolFalse, WireBoolTrue:
		return pos, uint(pos) <= uint(n)
	case WireString, WireBytes, WireSubMsg, WireArray, WireMap, WireExtended:
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
	case WireFixed32, WireFloat32, WireSfixed32:
		if uint(pos) > uint(n) || n-pos < 4 {
			return 0, false
		}
		return pos + 4, true
	case WireFixed64, WireFloat64, WireSfixed64:
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
	bLen, bn := DecodeVarint(packet[pos:])
	if bn == 0 || bLen > uint64(len(packet)-(pos+bn)) {
		return 0, false
	}
	pos += bn + int(bLen)
	return pos, pos <= len(packet)
}

// SkipValue advances pos past a single field payload of the given 4-bit wireType (0..15),
// performing full bounds validation in O(1) time without looping inside containers.
func SkipValue(b []byte, pos int, wireType uint8) (int, error) {
	if pos < 0 || pos > len(b) {
		return 0, fmt.Errorf("omnibuf: position %d out of bounds", pos)
	}
	if wireType == WireArray || wireType == WireMap || wireType == WireExtended {
		length, ln := DecodeVarint(b[pos:])
		if ln == 0 || length < 1 || length > uint64(len(b)-pos-ln) {
			return 0, fmt.Errorf("omnibuf: invalid container length")
		}
		end := pos + ln + int(length)
		switch wireType {
		case WireArray:
			if _, _, _, ok := ParseArrayHeader(b[pos+ln : end]); !ok {
				return 0, fmt.Errorf("omnibuf: invalid array header")
			}
		case WireMap:
			if _, _, _, _, ok := ParseMapHeader(b[pos+ln : end]); !ok {
				return 0, fmt.Errorf("omnibuf: invalid map header")
			}
		}
		return end, nil
	}

	nextPos, ok := fastSkipValue(b, pos, wireType)
	if !ok {
		return 0, fmt.Errorf("omnibuf: truncated or invalid wireType %d at %d", wireType, pos)
	}
	return nextPos, nil
}

// LocateField locates targetField inside packet by hopping across field headers in O(1) time per field
// using TZCNT length prefixes (without parsing inside preceding strings, sub-messages, or arrays).
// To support raw message concatenation (append(msgA, msgB...)) with Protobuf "last write wins"
// semantics, it scans all field headers in the message and returns the last matching occurrence.
func LocateField(packet []byte, targetField uint16) (valSlice []byte, wireType uint8, ok bool) {
	return locateFieldMask(packet, targetField, 0xFFFF)
}

func locateFieldMask(packet []byte, targetField uint16, expMask uint16) (valSlice []byte, wireType uint8, ok bool) {
	pos := 0
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
				return nil, 0, false
			}
			u := binary.LittleEndian.Uint16(packet[pos:pos+2]) >> 2
			fnum = uint32(u >> 4)
			wt = uint8(u & 0x0F)
			pos += 2
		} else {
			tag, tn := DecodeVarint(packet[pos:])
			if tn == 0 || tag>>4 > 536870911 {
				return nil, 0, false
			}
			fnum = uint32(tag >> 4)
			wt = uint8(tag & 0x0F)
			pos += tn
		}

		if fnum == 0 {
			nextPos, validF0 := ValidateField0At(packet, pos, wt, 0)
			if !validF0 {
				return nil, 0, false
			}
			pos = nextPos
			continue
		}
		if fnum == uint32(targetField) && (expMask&(uint16(1)<<wt)) != 0 {
			valSlice = packet[pos:]
			wireType = wt
			ok = true
		}

		switch wt {
		case WireUint, WireSint:
			if pos >= n {
				return nil, 0, false
			}
			vb := packet[pos]
			if (vb & 1) != 0 {
				pos++
			} else if (vb & 2) != 0 {
				pos += 2
			} else if (vb & 4) != 0 {
				pos += 4
			} else if vb == 0 {
				pos += 9
			} else {
				return nil, 0, false
			}
			if pos > n {
				return nil, 0, false
			}
		case WireBoolFalse, WireBoolTrue:
		case WireString, WireBytes, WireSubMsg:
			if pos >= n {
				return nil, 0, false
			}
			lb := packet[pos]
			if (lb & 1) != 0 {
				pos += 1 + int(lb>>1)
			} else if (lb & 2) != 0 {
				if pos+2 > n {
					return nil, 0, false
				}
				pos += 2 + int(binary.LittleEndian.Uint16(packet[pos:pos+2])>>2)
			} else {
				bLen, bn := DecodeVarint(packet[pos:])
				if bn == 0 || bLen > uint64(n-(pos+bn)) {
					return nil, 0, false
				}
				pos += bn + int(bLen)
			}
			if pos > n {
				return nil, 0, false
			}
		case WireArray, WireMap, WireExtended:
			next, err := SkipValue(packet, pos, wt)
			if err != nil {
				return nil, 0, false
			}
			pos = next
		case WireFixed32, WireFloat32, WireSfixed32:
			pos += 4
			if pos > n {
				return nil, 0, false
			}
		case WireFixed64, WireFloat64, WireSfixed64:
			pos += 8
			if pos > n {
				return nil, 0, false
			}
		default:
			return nil, 0, false
		}
	}
	return valSlice, wireType, ok
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
			tag, tn := DecodeVarint(packet[pos:])
			if tn == 0 || tag>>4 > 536870911 {
				return nil, 0, false, false
			}
			fnum = uint32(tag >> 4)
			wt = uint8(tag & 0x0F)
			pos += tn
		}

		if fnum == 0 {
			if wt != WireSubMsg {
				return nil, 0, false, false
			}
			if targetField != 0 {
				nextPos, validF0 := ValidateField0At(packet, pos, wt, 0)
				if !validF0 {
					return nil, 0, false, false
				}
				pos = nextPos
				continue
			}
		}
		if fnum == uint32(targetField) && wt == expWT {
			bLen, bn := DecodeVarint(packet[pos:])
			if bn == 0 || bLen < minLen || bLen > uint64(n-(pos+bn)) {
				return nil, 0, false, false
			}
			pos += bn
			return packet[pos : pos+int(bLen)], pos + int(bLen), true, true
		}

		if wt == WireArray || wt == WireMap || wt == WireExtended {
			var err error
			pos, err = SkipValue(packet, pos, wt)
			if err != nil {
				return nil, 0, false, false
			}
		} else {
			var ok bool
			pos, ok = fastSkipValue(packet, pos, wt)
			if !ok {
				return nil, 0, false, false
			}
		}
	}
	return nil, n, false, true
}

// ValidatePacket validates all field tags, Field #0 SchemaMeta blocks, and container framing in packet.
func ValidatePacket(packet []byte) error {
	if !ValidateMessage(packet, 0) {
		return fmt.Errorf("omnibuf: corrupt wire packet")
	}
	return nil
}

// ValidateMessage reports whether data is a structurally valid OmniBuf message at nesting depth.
func ValidateMessage(data []byte, depth int) bool {
	if depth > maxNestingDepth {
		return false
	}
	pos := 0
	n := len(data)
	for pos < n {
		tag, tn := DecodeVarint(data[pos:])
		if tn == 0 || tag>>4 > 536870911 {
			return false
		}
		pos += tn
		fnum := tag >> 4
		wt := uint8(tag & 0x0F)
		if fnum == 0 {
			nextPos, ok := ValidateField0At(data, pos, wt, depth)
			if !ok {
				return false
			}
			pos = nextPos
			continue
		}
		nextPos, ok := validateValueWithDepth(data, pos, wt, depth)
		if !ok {
			return false
		}
		pos = nextPos
	}
	return true
}

// ValidateField0At validates a Field #0 SchemaMeta block starting at data[pos:] with wire type wt.
func ValidateField0At(data []byte, pos int, wt uint8, depth int) (int, bool) {
	if wt != WireSubMsg || pos < 0 || pos >= len(data) {
		return 0, false
	}
	bLen, bn := DecodeVarint(data[pos:])
	if bn == 0 || bLen > uint64(len(data)-(pos+bn)) {
		return 0, false
	}
	start := pos + bn
	end := start + int(bLen)
	if !ValidateSchemaMeta(data[start:end], depth+1) {
		return 0, false
	}
	return end, true
}

// ValidateSchemaMeta validates an omnibuf.v1.SchemaMeta body at nesting depth.
func ValidateSchemaMeta(data []byte, depth int) bool {
	if depth > maxNestingDepth {
		return false
	}
	pos := 0
	n := len(data)
	for pos < n {
		tag, tn := DecodeVarint(data[pos:])
		if tn == 0 || tag>>4 > 536870911 {
			return false
		}
		pos += tn
		fnum := tag >> 4
		wt := uint8(tag & 0x0F)
		switch fnum {
		case 1:
			if wt != WireString {
				return false
			}
			sLen, sn := DecodeVarint(data[pos:])
			if sn == 0 || sLen > uint64(n-(pos+sn)) {
				return false
			}
			pos += sn
			if !ValidUTF8(data[pos : pos+int(sLen)]) {
				return false
			}
			pos += int(sLen)
		case 2:
			if wt != WireMap || depth+1 > maxNestingDepth {
				return false
			}
			bLen, bn := DecodeVarint(data[pos:])
			if bn == 0 || bLen < 3 || bLen > uint64(n-(pos+bn)) {
				return false
			}
			body := data[pos+bn : pos+bn+int(bLen)]
			pos += bn + int(bLen)
			keyWT, valWT, count, p, ok := ParseMapHeader(body)
			if !ok || keyWT != WireUint || valWT != WireString {
				return false
			}
			for range count {
				k64, kn := DecodeVarint(body[p:])
				if kn == 0 || k64 == 0 || k64 > 536870911 {
					return false
				}
				p += kn
				sLen, sn := DecodeVarint(body[p:])
				if sn == 0 || sLen > uint64(len(body)-(p+sn)) {
					return false
				}
				p += sn
				if !ValidUTF8(body[p : p+int(sLen)]) {
					return false
				}
				p += int(sLen)
			}
			if p != len(body) {
				return false
			}
		case 3:
			if wt != WireMap || depth+1 > maxNestingDepth {
				return false
			}
			bLen, bn := DecodeVarint(data[pos:])
			if bn == 0 || bLen < 3 || bLen > uint64(n-(pos+bn)) {
				return false
			}
			body := data[pos+bn : pos+bn+int(bLen)]
			pos += bn + int(bLen)
			keyWT, valWT, count, p, ok := ParseMapHeader(body)
			if !ok || keyWT != WireUint || valWT != WireSubMsg {
				return false
			}
			for range count {
				k64, kn := DecodeVarint(body[p:])
				if kn == 0 || k64 == 0 || k64 > 536870911 {
					return false
				}
				p += kn
				cLen, cn := DecodeVarint(body[p:])
				if cn == 0 || cLen > uint64(len(body)-(p+cn)) {
					return false
				}
				p += cn
				if !ValidateSchemaMeta(body[p:p+int(cLen)], depth+2) {
					return false
				}
				p += int(cLen)
			}
			if p != len(body) {
				return false
			}
		default:
			if fnum == 0 {
				return false
			}
			nextPos, ok := validateValueWithDepth(data, pos, wt, depth)
			if !ok {
				return false
			}
			pos = nextPos
		}
	}
	return true
}

func validateValueWithDepth(b []byte, pos int, wt uint8, depth int) (int, bool) {
	if depth > maxNestingDepth || pos < 0 || pos > len(b) {
		return 0, false
	}
	if wt == WireSubMsg {
		if pos >= len(b) {
			return 0, false
		}
		bLen, bn := DecodeVarint(b[pos:])
		if bn == 0 || bLen > uint64(len(b)-(pos+bn)) {
			return 0, false
		}
		start := pos + bn
		end := start + int(bLen)
		if !ValidateMessage(b[start:end], depth+1) {
			return 0, false
		}
		return end, true
	}
	if wt == WireArray || wt == WireMap || wt == WireExtended {
		if pos >= len(b) {
			return 0, false
		}
		length, ln := DecodeVarint(b[pos:])
		if ln == 0 || length < 1 || length > uint64(len(b)-pos-ln) {
			return 0, false
		}
		end := pos + ln + int(length)
		body := b[pos+ln : end]
		switch wt {
		case WireArray:
			if depth+1 > maxNestingDepth {
				return 0, false
			}
			elemWT, count, p, ok := ParseArrayHeader(body)
			if !ok || !validateArrayBodyWithDepth(body, p, elemWT, count, depth+1) {
				return 0, false
			}
		case WireMap:
			if depth+1 > maxNestingDepth {
				return 0, false
			}
			keyWT, valWT, count, p, ok := ParseMapHeader(body)
			if !ok {
				return 0, false
			}
			for range count {
				p, ok = validateEntryWithDepth(body, p, keyWT, depth+1)
				if !ok {
					return 0, false
				}
				p, ok = validateEntryWithDepth(body, p, valWT, depth+1)
				if !ok {
					return 0, false
				}
			}
			if p != len(body) {
				return 0, false
			}
		}
		return end, true
	}
	if wt == WireString {
		if pos >= len(b) {
			return 0, false
		}
		lb := b[pos]
		if (lb & 1) != 0 {
			l := int(lb >> 1)
			end := pos + 1 + l
			if len(b)-pos-1 < l || !ValidUTF8(b[pos+1:end]) {
				return 0, false
			}
			return end, true
		}
		sLen, sn := DecodeVarint(b[pos:])
		if sn == 0 || sLen > uint64(len(b)-(pos+sn)) {
			return 0, false
		}
		start := pos + sn
		end := start + int(sLen)
		if !ValidUTF8(b[start:end]) {
			return 0, false
		}
		return end, true
	}
	return fastSkipValue(b, pos, wt)
}

func validateArrayBodyWithDepth(body []byte, p int, elemWT uint8, count, depth int) bool {
	switch elemWT {
	case WireBoolFalse:
		if len(body)-p != count {
			return false
		}
		for i := range count {
			if body[p+i] > 1 {
				return false
			}
		}
		return true
	case WireFixed32, WireSfixed32, WireFloat32:
		return len(body)-p == count*4
	case WireFixed64, WireSfixed64, WireFloat64:
		return len(body)-p == count*8
	}
	for range count {
		var ok bool
		p, ok = validateEntryWithDepth(body, p, elemWT, depth)
		if !ok {
			return false
		}
	}
	return p == len(body)
}

func validateEntryWithDepth(body []byte, p int, wt uint8, depth int) (int, bool) {
	if wt == WireBoolFalse {
		if p >= len(body) || body[p] > 1 {
			return 0, false
		}
		return p + 1, true
	}
	return validateValueWithDepth(body, p, wt, depth)
}

// Encoder encodes OmniBuf messages into a reusable contiguous byte buffer in a single forward pass.
type Encoder struct {
	buf           []byte
	lenStack      [maxNestingDepth * 2]int
	depth         int
	countStack    [maxNestingDepth * 2]int
	lenOverflow   []int
	countOverflow []int
}

// NewEncoder creates an Encoder with the specified initial buffer capacity.
func NewEncoder(capacity int) *Encoder {
	if capacity < 0 {
		capacity = 0
	}
	return &Encoder{
		buf: make([]byte, 0, capacity),
	}
}

// Reset clears the encoder buffer for zero-allocation reuse.
func (e *Encoder) Reset() {
	e.buf = e.buf[:0]
	e.depth = 0
}

// Bytes returns the encoded packet bytes.
func (e *Encoder) Bytes() []byte {
	return e.buf
}

// Finish returns the encoded message bytes.
func (e *Encoder) Finish() []byte {
	return e.buf
}

// WriteRaw appends pre-encoded raw bytes (such as a pre-encoded Field #0 SchemaMeta block) to e.
func (e *Encoder) WriteRaw(raw []byte) {
	e.buf = append(e.buf, raw...)
}

func (e *Encoder) writeTag(fieldNum uint16, wireType uint8) {
	tag := (uint32(fieldNum) << 4) | uint32(wireType)
	if tag <= 127 {
		e.buf = append(e.buf, byte(tag<<1)|1)
		return
	}
	if tag <= 16383 {
		u := uint16(tag<<2) | 2
		e.buf = append(e.buf, byte(u), byte(u>>8))
		return
	}
	e.buf = appendVarintMulti(e.buf, uint64(tag))
}

// WriteField0Fields writes Field #0 (Tag = 0x19, WireSubMsg = 12) mapping FieldNumber -> FieldName.
func (e *Encoder) WriteField0Fields(fields map[uint32]string) {
	if len(fields) == 0 {
		return
	}
	var stackKeys [32]uint32
	keys := stackKeys[:0]
	if len(fields) > len(stackKeys) {
		keys = make([]uint32, 0, len(fields))
	}
	for k, name := range fields {
		if k == 0 || k > 536870911 {
			panic(fmt.Sprintf("omnibuf: invalid Field #0 field number %d", k))
		}
		if !ValidUTF8String(name) {
			panic("omnibuf: invalid UTF-8 in Field #0 field name")
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)

	e.buf = append(e.buf, (((0<<4)|WireSubMsg)<<1)|1)
	lenPos := len(e.buf)
	e.buf = append(e.buf, 0, 0)
	e.buf = append(e.buf, (((2<<4)|WireMap)<<1)|1)
	mLenPos := len(e.buf)
	e.buf = append(e.buf, 0, 0, WireUint, WireString)
	e.buf = AppendVarint(e.buf, uint64(len(keys)))
	for _, k := range keys {
		name := fields[k]
		e.buf = AppendVarint(e.buf, uint64(k))
		e.buf = AppendVarint(e.buf, uint64(len(name)))
		e.buf = append(e.buf, name...)
	}
	e.buf = BackpatchContainerLen(e.buf, mLenPos)
	e.buf = BackpatchContainerLen(e.buf, lenPos)
}

// WriteField0Schema writes Field #0 (Tag = 0x19, WireSubMsg = 12) mapping field numbers 1..len(names)
// to names[0..len(names)-1].
func (e *Encoder) WriteField0Schema(names []string) {
	count := 0
	for i, name := range names {
		if name == "" {
			continue
		}
		if uint64(i+1) > 536870911 {
			panic(fmt.Sprintf("omnibuf: invalid Field #0 field number %d", i+1))
		}
		if !ValidUTF8String(name) {
			panic("omnibuf: invalid UTF-8 in Field #0 field name")
		}
		count++
	}
	if count == 0 {
		return
	}
	e.buf = append(e.buf, (((0<<4)|WireSubMsg)<<1)|1)
	lenPos := len(e.buf)
	e.buf = append(e.buf, 0, 0)
	e.buf = append(e.buf, (((2<<4)|WireMap)<<1)|1)
	mLenPos := len(e.buf)
	e.buf = append(e.buf, 0, 0, WireUint, WireString)
	e.buf = AppendVarint(e.buf, uint64(count))
	for i, name := range names {
		if name == "" {
			continue
		}
		e.buf = AppendVarint(e.buf, uint64(i+1))
		e.buf = AppendVarint(e.buf, uint64(len(name)))
		e.buf = append(e.buf, name...)
	}
	e.buf = BackpatchContainerLen(e.buf, mLenPos)
	e.buf = BackpatchContainerLen(e.buf, lenPos)
}

// PatchReservedVarint fills a 1-byte reserved varint slot and expands it if value > 127.
func PatchReservedVarint(dst []byte, pos int, value uint64) []byte {
	if value <= 127 {
		dst[pos] = byte(value<<1) | 1
		return dst
	}
	width := 2
	if value > 536870911 {
		width = 9
	} else if value > 16383 {
		width = 4
	}
	oldLen := len(dst)
	dst = append(dst, make([]byte, width-1)...)
	copy(dst[pos+width:], dst[pos+1:oldLen])
	switch width {
	case 2:
		binary.LittleEndian.PutUint16(dst[pos:pos+2], uint16(value<<2)|2)
	case 4:
		binary.LittleEndian.PutUint32(dst[pos:pos+4], uint32(value<<3)|4)
	default:
		dst[pos] = 0
		binary.LittleEndian.PutUint64(dst[pos+1:pos+9], value)
	}
	return dst
}
