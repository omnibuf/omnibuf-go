package wire

import (
	"bytes"
	"math/rand/v2"
	"testing"
	"unicode/utf8"
)

func TestValidUTF8MatchesStandardLibrary(t *testing.T) {
	check := func(b []byte) {
		t.Helper()
		want := utf8.Valid(b)
		if got := ValidUTF8(b); got != want {
			t.Fatalf("ValidUTF8(%x) = %v, want %v", b, got, want)
		}
		if got := ValidUTF8String(string(b)); got != want {
			t.Fatalf("ValidUTF8String(%x) = %v, want %v", b, got, want)
		}
	}
	check(nil)
	// Exercise word-load boundaries, overlap, 64-byte chunks, and every possible
	// non-ASCII byte at every position, including the final byte.
	for n := 0; n <= 144; n++ {
		b := bytes.Repeat([]byte{'a'}, n)
		check(b)
		for i := range b {
			for c := 128; c <= 255; c++ {
				b[i] = byte(c)
				check(b)
			}
			b[i] = 'a'
		}
		for _, suffix := range []string{"é", "世界", "🚀", "\xc0\x80", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xe2\x82"} {
			check(append(bytes.Clone(b), suffix...))
			check(append([]byte(suffix), b...))
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		b := make([]byte, rng.IntN(4096))
		for i := range b {
			b[i] = byte(rng.Uint32())
		}
		check(b)
	}
}

func FuzzValidUTF8(f *testing.F) {
	for _, seed := range []string{"", "ASCII", "eu-central-1", "世界🚀", "\xff", "\xc0\x80", "aaaaaaaaaaaaaaa\xe2\x82"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if got, want := ValidUTF8(b), utf8.Valid(b); got != want {
			t.Fatalf("ValidUTF8(%x) = %v, want %v", b, got, want)
		}
	})
}

func TestIsASCII(t *testing.T) {
	refASCII := func(b []byte) bool {
		for _, c := range b {
			if c >= utf8.RuneSelf {
				return false
			}
		}
		return true
	}
	check := func(b []byte) {
		t.Helper()
		want := refASCII(b)
		if got := IsASCII(b); got != want {
			t.Fatalf("IsASCII(%x) = %v, want %v", b, got, want)
		}
	}

	check(nil)
	check([]byte{})
	check([]byte{0x00, 0x7f})
	for _, nonASCII := range []string{"é", "世界", "🚀", "\x80", "\xff"} {
		if IsASCII([]byte(nonASCII)) {
			t.Fatalf("IsASCII(%q) = true, want false", nonASCII)
		}
	}

	// Exhaustively test every length across 8-byte word and overlap boundaries,
	// placing a high-bit byte at every possible index.
	for n := 0; n <= 144; n++ {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i & 0x7f)
		}
		check(b)
		for i := range b {
			orig := b[i]
			for _, hi := range []byte{0x80, 0x81, 0xc2, 0xfe, 0xff} {
				b[i] = hi
				check(b)
			}
			b[i] = orig
		}
	}

	rng := rand.New(rand.NewPCG(3, 5))
	for range 1000 {
		b := make([]byte, rng.IntN(512))
		for i := range b {
			b[i] = byte(rng.Uint32() & 0x7f)
		}
		check(b)
		if len(b) > 0 {
			b[rng.IntN(len(b))] |= 0x80
			check(b)
		}
	}
}
