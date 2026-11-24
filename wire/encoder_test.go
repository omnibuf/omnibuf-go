package wire

import "testing"

func TestEncoderMisuseNoPanic(t *testing.T) {
	enc := NewEncoder(-10)
	enc.EndSubMsg()
	enc.BeginSubMsgElem()
	enc.EndSubMsgElem()
	enc.EndSubMsgArr()
	enc.WriteUint(1, 42)
	if v, ok := ReadUint(enc.Bytes(), 1); !ok || v != 42 {
		t.Fatalf("ReadUint = (%d, %v), want (42, true)", v, ok)
	}
}

func TestEncoderWriteSfixed(t *testing.T) {
	enc := NewEncoder(32)
	enc.WriteSfixed32(1, -123456)
	enc.WriteSfixed64(2, -9876543210123)
	pkt := enc.Bytes()

	if v, ok := ReadSfixed32(pkt, 1); !ok || v != -123456 {
		t.Fatalf("ReadSfixed32 = (%d, %v), want (-123456, true)", v, ok)
	}
	if v, ok := ReadSfixed64(pkt, 2); !ok || v != -9876543210123 {
		t.Fatalf("ReadSfixed64 = (%d, %v), want (-9876543210123, true)", v, ok)
	}
	if _, ok := ReadFixed32(pkt, 1); ok {
		t.Fatal("ReadFixed32 unexpectedly matched WireSfixed32")
	}
	if _, ok := ReadFixed64(pkt, 2); ok {
		t.Fatal("ReadFixed64 unexpectedly matched WireSfixed64")
	}
}
