package wire

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"unsafe"
)

// ============================================================================
// Opaque API Runtime Support: Zero-Decode Wire Splicing
// ============================================================================

// CopyPacket appends raw to dst, allocating a detached slice when dst's
// backing array overlaps raw so neither raw nor the returned slice aliases the other.
func CopyPacket(dst, raw []byte) []byte {
	n := len(raw)
	if n > 0 && cap(dst) > 0 {
		dstStart := uintptr(unsafe.Pointer(unsafe.SliceData(dst)))
		dstEnd := dstStart + uintptr(cap(dst))
		rawStart := uintptr(unsafe.Pointer(unsafe.SliceData(raw)))
		rawEnd := rawStart + uintptr(n)
		if dstStart < rawEnd && rawStart < dstEnd {
			out := make([]byte, len(dst)+n)
			copy(out, dst)
			copy(out[len(dst):], raw)
			return out
		}
	}
	return append(dst, raw...)
}

// SpliceFields splices unmodified contiguous byte ranges from raw and invokes appendDirty
// only for fields whose bit is set in dirtyMask, avoiding any decoding of untouched fields.
func SpliceFields(dst, raw []byte, dirtyMask uint64, maxField uint16, appendDirty func(dst []byte, fnum uint16) []byte) []byte {
	n := len(raw)
	if n > 0 && cap(dst) > 0 {
		dstStart := uintptr(unsafe.Pointer(unsafe.SliceData(dst)))
		dstEnd := dstStart + uintptr(cap(dst))
		rawStart := uintptr(unsafe.Pointer(unsafe.SliceData(raw)))
		rawEnd := rawStart + uintptr(n)
		if dstStart < rawEnd && rawStart < dstEnd {
			// Preserve the caller's prefix while detaching writes from raw.
			dst = append([]byte(nil), dst...)
		}
	}
	pos := 0
	cleanStart := 0
	validEnd := 0
	var emitted uint64

	for pos < n {
		fieldStart := pos
		var fnum uint32
		var wt uint8
		b0 := raw[pos]
		if (b0 & 1) != 0 {
			fnum = uint32(b0 >> 5)
			wt = (b0 >> 1) & 0x0F
			pos++
		} else if (b0&2) != 0 && pos+2 <= n {
			u := binary.LittleEndian.Uint16(raw[pos:pos+2]) >> 2
			fnum = uint32(u >> 4)
			wt = uint8(u & 0x0F)
			pos += 2
		} else {
			tag, tn := decodeVarintMulti(raw[pos:])
			if tn == 0 || tag>>4 > 536870911 {
				break
			}
			fnum = uint32(tag >> 4)
			wt = uint8(tag & 0x0F)
			pos += tn
		}

		var nextPos int
		if fnum == 0 {
			var validF0 bool
			nextPos, validF0 = ValidateField0At(raw, pos, wt, 0)
			if !validF0 {
				break
			}
		} else if wt == WireArray || wt == WireMap || wt == WireExtended {
			var err error
			nextPos, err = SkipValue(raw, pos, wt)
			if err != nil {
				break
			}
		} else {
			var ok bool
			nextPos, ok = fastSkipValue(raw, pos, wt)
			if !ok {
				break
			}
		}
		pos = nextPos
		fieldEnd := pos
		validEnd = fieldEnd

		var bit uint64
		if fnum < 64 {
			bit = uint64(1) << fnum
		}
		if (dirtyMask & bit) != 0 {
			if cleanStart < fieldStart {
				dst = append(dst, raw[cleanStart:fieldStart]...)
			}
			if (emitted & bit) == 0 {
				dst = appendDirty(dst, uint16(fnum))
				emitted |= bit
			}
			cleanStart = fieldEnd
		} else {
			emitted |= bit
		}
	}

	if cleanStart < validEnd {
		dst = append(dst, raw[cleanStart:validEnd]...)
	}

	// Append any newly added dirty fields that were not present in raw
	rem := dirtyMask &^ emitted &^ 1
	if maxField < 63 {
		rem &= (uint64(1) << (maxField + 1)) - 1
	}
	for rem != 0 {
		fnum := uint16(bits.TrailingZeros64(rem))
		dst = appendDirty(dst, fnum)
		rem &= rem - 1
	}
	return dst
}

// TrySpliceFields splices unmodified byte ranges from raw and invokes appendDirty
// for dirty fields, returning an error if raw contains any structurally corrupt wire data.
func TrySpliceFields(dst, raw []byte, dirtyMask uint64, maxField uint16, appendDirty func(dst []byte, fnum uint16) []byte) ([]byte, error) {
	n := len(raw)
	if n > 0 && cap(dst) > 0 {
		dstStart := uintptr(unsafe.Pointer(unsafe.SliceData(dst)))
		dstEnd := dstStart + uintptr(cap(dst))
		rawStart := uintptr(unsafe.Pointer(unsafe.SliceData(raw)))
		rawEnd := rawStart + uintptr(n)
		if dstStart < rawEnd && rawStart < dstEnd {
			dst = append([]byte(nil), dst...)
		}
	}
	pos := 0
	cleanStart := 0
	var emitted uint64

	for pos < n {
		fieldStart := pos
		var fnum uint32
		var wt uint8
		b0 := raw[pos]
		if (b0 & 1) != 0 {
			fnum = uint32(b0 >> 5)
			wt = (b0 >> 1) & 0x0F
			pos++
		} else if (b0&2) != 0 && pos+2 <= n {
			u := binary.LittleEndian.Uint16(raw[pos:pos+2]) >> 2
			fnum = uint32(u >> 4)
			wt = uint8(u & 0x0F)
			pos += 2
		} else {
			tag, tn := decodeVarintMulti(raw[pos:])
			if tn == 0 || tag>>4 > 536870911 {
				return nil, fmt.Errorf("omnibuf: invalid or truncated tag during splice at %d", fieldStart)
			}
			fnum = uint32(tag >> 4)
			wt = uint8(tag & 0x0F)
			pos += tn
		}

		var nextPos int
		var ok bool
		if fnum == 0 {
			nextPos, ok = ValidateField0At(raw, pos, wt, 0)
			if !ok {
				return nil, fmt.Errorf("omnibuf: invalid Field #0 during splice at %d", fieldStart)
			}
		} else {
			nextPos, ok = validateValueWithDepth(raw, pos, wt, 0)
			if !ok {
				return nil, fmt.Errorf("omnibuf: corrupt field #%d (wireType %d) during splice at %d", fnum, wt, fieldStart)
			}
		}
		pos = nextPos
		fieldEnd := pos

		var bit uint64
		if fnum < 64 {
			bit = uint64(1) << fnum
		}
		if (dirtyMask & bit) != 0 {
			if cleanStart < fieldStart {
				dst = append(dst, raw[cleanStart:fieldStart]...)
			}
			if (emitted & bit) == 0 {
				dst = appendDirty(dst, uint16(fnum))
				emitted |= bit
			}
			cleanStart = fieldEnd
		} else {
			emitted |= bit
		}
	}

	if cleanStart < n {
		dst = append(dst, raw[cleanStart:n]...)
	}

	rem := dirtyMask &^ emitted &^ 1
	if maxField < 63 {
		rem &= (uint64(1) << (maxField + 1)) - 1
	}
	for rem != 0 {
		fnum := uint16(bits.TrailingZeros64(rem))
		dst = appendDirty(dst, fnum)
		rem &= rem - 1
	}
	return dst, nil
}
