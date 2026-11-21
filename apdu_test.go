package ccid

import (
	"bytes"
	"errors"
	"testing"
)

func TestParseCommand(t *testing.T) {
	long := bytes.Repeat([]byte{0xAB}, 300)
	ext := func(lc int, data []byte, le ...byte) []byte {
		b := []byte{0x80, 0xE2, 0x91, 0x00, 0x00, byte(lc >> 8), byte(lc)}
		b = append(b, data...)
		return append(b, le...)
	}
	for _, tc := range []struct {
		name     string
		apdu     []byte
		cas, le  int
		dataLen  int
		extended bool
	}{
		{"case 1", []byte{0x00, 0x70, 0x00, 0x00}, 1, -1, 0, false},
		{"case 2 short", []byte{0x00, 0xB0, 0x00, 0x00, 0x10}, 2, 16, 0, false},
		{"case 2 short Le 00", []byte{0x00, 0xB0, 0x00, 0x00, 0x00}, 2, 256, 0, false},
		{"case 3 short", []byte{0x00, 0xA4, 0x04, 0x00, 0x02, 0x3F, 0x00}, 3, -1, 2, false},
		{"case 4 short", []byte{0x00, 0xA4, 0x04, 0x00, 0x02, 0x3F, 0x00, 0x00}, 4, 256, 2, false},
		{"case 2 extended", []byte{0x00, 0xB0, 0x00, 0x00, 0x00, 0x01, 0x00}, 2, 256, 0, true},
		{"case 2 extended Le 0000", []byte{0x00, 0xB0, 0x00, 0x00, 0x00, 0x00, 0x00}, 2, 65536, 0, true},
		{"case 3 extended", ext(300, long), 3, -1, 300, true},
		{"case 4 extended", ext(300, long, 0x00, 0x00), 4, 65536, 300, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := parseCommand(tc.apdu)
			if err != nil {
				t.Fatal(err)
			}
			if c.cas != tc.cas || c.le != tc.le || len(c.data) != tc.dataLen || c.extended != tc.extended {
				t.Errorf("got case %d le %d data %d extended %v, want %d %d %d %v",
					c.cas, c.le, len(c.data), c.extended, tc.cas, tc.le, tc.dataLen, tc.extended)
			}
		})
	}
	for _, bad := range [][]byte{
		{0x00, 0xA4, 0x04},                               // short header
		{0x00, 0xA4, 0x04, 0x00, 0x02, 0x3F},             // Lc 2, 1 byte
		{0x00, 0xA4, 0x04, 0x00, 0x02, 0x3F, 0, 0, 0},    // Lc 2, 3 bytes after
		{0x00, 0xA4, 0x04, 0x00, 0x00, 0x01},             // extended length cut
		{0x00, 0xA4, 0x04, 0x00, 0x00, 0x00, 0x00, 0x01}, // extended Lc 0
		ext(300, long[:299]),
	} {
		if _, err := parseCommand(bad); !errors.Is(err, ErrInvalidAPDU) {
			t.Errorf("parseCommand(% x): %v, want ErrInvalidAPDU", bad, err)
		}
	}
}

func TestGetResponseClass(t *testing.T) {
	for cla, want := range map[byte]byte{
		0x00: 0x00, 0x01: 0x01, 0x03: 0x03,
		0x80: 0x00, 0x81: 0x01, 0x83: 0x03, 0x0C: 0x00,
		0x40: 0x40, 0x4F: 0x4F, 0x60: 0x40,
	} {
		if got := getResponseClass(cla); got != want {
			t.Errorf("getResponseClass(%#02x) = %#02x, want %#02x", cla, got, want)
		}
	}
}
