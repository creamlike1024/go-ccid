package ccid

import (
	"bytes"
	"testing"
)

func FuzzParseATR(f *testing.F) {
	for _, s := range [][]byte{
		{0x3B, 0x00},
		{0x3B, 0x90, 0x96, 0x80, 0x31, 0xFE, 0x45, 0x0C},
		{0x3B, 0x92, 0x18, 0x11, 0x81, 'A', 'B', 0x00},
		{0x3F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		a, err := ParseATR(b)
		if err != nil {
			return
		}
		// Everything a parsed ATR offers works on it.
		a.Protocols()
		a.FiDi()
		a.SpecificMode()
		a.ExtraGuardTime()
		a.WaitingIntegerT0()
		a.IFSC()
		a.WaitingIntegersT1()
		a.CRC()
		if !bytes.Equal(a.Raw, b) {
			t.Fatalf("Raw % x, parsed from % x", a.Raw, b)
		}
	})
}

func FuzzFindInterfaces(f *testing.F) {
	d := Descriptor{Features: uint32(LevelTPDU), MaxMessageLength: 271, Protocols: 3}
	cd, _ := d.MarshalBinary()
	seed := []byte{18, 0x01, 0x00, 0x02, 0, 0, 0, 64, 0x2F, 0x07, 0xCC, 0x90, 0x00, 0x01, 1, 2, 0, 1,
		9, 0x02, 0, 0, 1, 1, 0, 0x80, 50, 9, 0x04, 0, 0, 3, 0x0B, 0, 0, 0}
	seed = append(seed, cd...)
	seed = append(seed, 7, 0x05, 0x02, 0x02, 64, 0, 0, 7, 0x05, 0x82, 0x02, 64, 0, 0, 7, 0x05, 0x83, 0x03, 8, 0, 16)
	f.Add(seed)
	f.Add([]byte{9, 0x02, 0})
	f.Fuzz(func(t *testing.T, raw []byte) {
		ifs, err := FindInterfaces(raw)
		if err != nil {
			return
		}
		for _, i := range ifs {
			if i.BulkIn == 0 || i.BulkOut == 0 || i.BulkIn&0x80 == 0 || i.BulkOut&0x80 != 0 {
				t.Fatalf("interface without its bulk endpoints: %+v", i)
			}
			b, _ := i.Descriptor.MarshalBinary()
			if got, err := ParseDescriptor(b); err != nil || got != i.Descriptor {
				t.Fatalf("descriptor does not round trip: %v", err)
			}
		}
	})
}

func FuzzMessage(f *testing.F) {
	m, _ := Message{Type: RDRtoPCDataBlock, Slot: 1, Seq: 2, Param: [3]byte{3, 4, 5}, Data: []byte{0x90, 0x00}}.MarshalBinary()
	f.Add(m)
	f.Add([]byte{0x80, 0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		var m Message
		if m.UnmarshalBinary(b) != nil {
			return
		}
		back, _ := m.MarshalBinary()
		if !bytes.Equal(back, b) {
			t.Fatalf("message does not round trip: % x, % x", b, back)
		}
	})
}

func FuzzParseCommand(f *testing.F) {
	f.Add([]byte{0x00, 0xA4, 0x04, 0x00, 0x02, 0x3F, 0x00, 0x00})
	f.Add([]byte{0x00, 0xB0, 0x00, 0x00, 0x00, 0x01, 0x00})
	f.Add([]byte{0x80, 0xE2, 0x91, 0x00, 0x00, 0x00, 0x02, 0xAA, 0xBB, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := parseCommand(b)
		if err != nil {
			return
		}
		if c.cas < 1 || c.cas > 4 || (c.cas == 1 || c.cas == 3) != (c.le == -1) {
			t.Fatalf("case %d with le %d", c.cas, c.le)
		}
		if (c.cas >= 3) != (len(c.data) > 0) {
			t.Fatalf("case %d with %d bytes of data", c.cas, len(c.data))
		}
	})
}

func FuzzDecodeT1(f *testing.F) {
	f.Add(encodeT1(0x00, []byte{0x90, 0x00}))
	f.Add(encodeT1(0xC3, []byte{1}))
	f.Add(encodeT1(0x90, nil))
	f.Fuzz(func(t *testing.T, b []byte) {
		blk, _, ok := decodeT1(b)
		if !ok {
			return
		}
		if again := encodeT1(blk.pcb, blk.inf); b[0] == 0 && !bytes.Equal(again, b) {
			t.Fatalf("block does not round trip: % x, % x", b, again)
		}
	})
}
