package wire

import (
	"bytes"
	"math"
	"math/rand/v2"
	"testing"
)

func TestVarintSizeAndPutVarint(t *testing.T) {
	values := []uint64{
		// 1-byte boundaries (0..127)
		0, 1, 63, 126, 127,
		// 2-byte boundaries (128..16383)
		128, 129, 255, 256, 8191, 16382, 16383,
		// 4-byte boundaries (16384..536870911)
		16384, 16385, 65535, 65536, 1 << 20, 536870910, 536870911,
		// 9-byte boundaries (536870912..MaxUint64)
		536870912, 536870913, 1 << 32, 1 << 48, 1 << 63, math.MaxUint64 - 1, math.MaxUint64,
	}

	rng := rand.New(rand.NewPCG(7, 13))
	for range 200 {
		values = append(values,
			rng.Uint64N(128),
			128+rng.Uint64N(16384-128),
			16384+rng.Uint64N(536870912-16384),
			536870912+rng.Uint64(),
		)
	}

	for _, v := range values {
		want := AppendVarint(nil, v)
		if gotSize := VarintSize(v); gotSize != len(want) {
			t.Fatalf("VarintSize(%d) = %d, want %d", v, gotSize, len(want))
		}

		// Test PutVarint at offset 0 with trailing sentinels.
		buf := bytes.Repeat([]byte{0xaa}, 16)
		n := PutVarint(buf, v)
		if n != len(want) {
			t.Fatalf("PutVarint(%d) wrote %d bytes, want %d", v, n, len(want))
		}
		if !bytes.Equal(buf[:n], want) {
			t.Fatalf("PutVarint(%d) = %x, want %x", v, buf[:n], want)
		}
		for i := n; i < len(buf); i++ {
			if buf[i] != 0xaa {
				t.Fatalf("PutVarint(%d) clobbered sentinel at index %d: got 0x%02x", v, i, buf[i])
			}
		}
		dec, dn := DecodeVarint(buf[:n])
		if dn != n || dec != v {
			t.Fatalf("DecodeVarint(PutVarint(%d)) = (%d, %d), want (%d, %d)", v, dec, dn, v, n)
		}

		// Test PutVarintAt across aligned and unaligned offsets with surrounding sentinels.
		for _, pos := range []int{0, 1, 3, 7, 16} {
			dst := bytes.Repeat([]byte{0x55}, pos+len(want)+7)
			next := PutVarintAt(dst, pos, v)
			if next != pos+len(want) {
				t.Fatalf("PutVarintAt(pos=%d, v=%d) = %d, want %d", pos, v, next, pos+len(want))
			}
			if !bytes.Equal(dst[pos:next], want) {
				t.Fatalf("PutVarintAt(pos=%d, v=%d) = %x, want %x", pos, v, dst[pos:next], want)
			}
			for i := 0; i < pos; i++ {
				if dst[i] != 0x55 {
					t.Fatalf("PutVarintAt(pos=%d, v=%d) clobbered prefix byte %d", pos, v, i)
				}
			}
			for i := next; i < len(dst); i++ {
				if dst[i] != 0x55 {
					t.Fatalf("PutVarintAt(pos=%d, v=%d) clobbered suffix byte %d", pos, v, i)
				}
			}
		}
	}
}

func TestCopyStringAtAndCopyBytesAt(t *testing.T) {
	offsets := []int{0, 1, 3, 7, 8, 15, 16}
	for n := 0; n <= 128; n++ {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte((i*31 + 17) & 0xff)
		}
		str := string(src)

		for _, pos := range offsets {
			dstBytes := bytes.Repeat([]byte{0xcc}, pos+n+11)
			nextB := CopyBytesAt(dstBytes, pos, src)
			if nextB != pos+n {
				t.Fatalf("CopyBytesAt(n=%d, pos=%d) = %d, want %d", n, pos, nextB, pos+n)
			}
			if !bytes.Equal(dstBytes[pos:nextB], src) {
				t.Fatalf("CopyBytesAt(n=%d, pos=%d) content mismatch", n, pos)
			}
			for i := 0; i < pos; i++ {
				if dstBytes[i] != 0xcc {
					t.Fatalf("CopyBytesAt(n=%d, pos=%d) clobbered prefix %d", n, pos, i)
				}
			}
			for i := nextB; i < len(dstBytes); i++ {
				if dstBytes[i] != 0xcc {
					t.Fatalf("CopyBytesAt(n=%d, pos=%d) clobbered suffix %d", n, pos, i)
				}
			}

			dstStr := bytes.Repeat([]byte{0xdd}, pos+n+11)
			nextS := CopyStringAt(dstStr, pos, str)
			if nextS != pos+n {
				t.Fatalf("CopyStringAt(n=%d, pos=%d) = %d, want %d", n, pos, nextS, pos+n)
			}
			if !bytes.Equal(dstStr[pos:nextS], src) {
				t.Fatalf("CopyStringAt(n=%d, pos=%d) content mismatch", n, pos)
			}
			for i := 0; i < pos; i++ {
				if dstStr[i] != 0xdd {
					t.Fatalf("CopyStringAt(n=%d, pos=%d) clobbered prefix %d", n, pos, i)
				}
			}
			for i := nextS; i < len(dstStr); i++ {
				if dstStr[i] != 0xdd {
					t.Fatalf("CopyStringAt(n=%d, pos=%d) clobbered suffix %d", n, pos, i)
				}
			}
		}
	}
}
