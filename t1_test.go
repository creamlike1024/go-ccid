package ccid_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	ccid "github.com/creamlike1024/go-ccid"
	"github.com/creamlike1024/go-ccid/ccidtest"
)

// atrT1 is the ATR of a T=1 card with an IFSC of ifsc and LRC.
func atrT1(ifsc byte) []byte {
	// TD1: TD2 follows, T=1; TD2: TA3 and TB3 follow, T=1; TA3 IFSC,
	// TB3 BWI 4 CWI 5.
	b := []byte{0x3B, 0x80, 0x81, 0x31, ifsc, 0x45}
	return append(b, tck(b))
}

func openT1(t *testing.T, card *testCard, o ccidtest.Options) (*ccid.Card, *ccidtest.Reader) {
	t.Helper()
	r, sim := open(t, card, o)
	c := connect(t, r, &ccid.ConnectOptions{Prefer: []ccid.Protocol{ccid.T1}})
	if c.Protocol() != ccid.T1 {
		t.Fatalf("protocol %v", c.Protocol())
	}
	return c, sim
}

func echoAPDU(data []byte) (apdu, want []byte) {
	apdu = []byte{0x80, 0xEE, 0x00, 0x00}
	if len(data) <= 255 {
		apdu = append(apdu, byte(len(data)))
	} else {
		apdu = append(apdu, 0x00, byte(len(data)>>8), byte(len(data)))
	}
	apdu = append(apdu, data...)
	want = slices.Clone(data)
	slices.Reverse(want)
	return apdu, append(want, sw9000...)
}

func TestT1Connect(t *testing.T) {
	_, sim := openT1(t, &testCard{atr: atrT1(0x20)}, ccidtest.Options{})
	p, params, ok := sim.Parameters()
	// Fd/Dd, LRC, no extra guard time, BWI 4 CWI 5, IFSC 32, NAD 0.
	if !ok || p != ccid.T1 || !bytes.Equal(params, []byte{0x11, 0x10, 0x00, 0x45, 0x00, 0x20, 0x00}) {
		t.Errorf("SetParameters T=%d % x", p, params)
	}
	if got := sim.T1().IFSD; got != 254 {
		t.Errorf("IFSD asked for %d, want 254", got)
	}
}

func TestT1Chaining(t *testing.T) {
	// An IFSC of 32: the command goes in blocks of 32; an IFSD of 254:
	// the response in blocks of 254.
	card := &testCard{atr: atrT1(0x20), n: 600}
	c, _ := openT1(t, card, ccidtest.Options{})
	for _, n := range []int{0, 1, 31, 32, 33, 100, 255, 1000} {
		apdu, want := echoAPDU(pattern(n))
		if resp := transmit(t, c, apdu...); !bytes.Equal(resp, want) {
			t.Fatalf("%d bytes echoed: got %d bytes", n, len(resp))
		}
		if got := card.seen[len(card.seen)-1]; !bytes.Equal(got, apdu) {
			t.Fatalf("%d bytes: the card got %d bytes, want %d", n, len(got), len(apdu))
		}
	}
	if resp := transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 0x00, 0x02, 0x58); !bytes.Equal(resp, append(pattern(600), sw9000...)) {
		t.Errorf("600 bytes of response: got %d", len(resp))
	}
}

func TestT1WTXAndIFSRequest(t *testing.T) {
	card := &testCard{atr: atrT1(0x20)}
	c, sim := openT1(t, card, ccidtest.Options{T1WTX: 2, T1IFSRequest: 0x40})
	apdu, want := echoAPDU(pattern(100))
	if resp := transmit(t, c, apdu...); !bytes.Equal(resp, want) {
		t.Fatalf("after WTX: % x", resp)
	}
	var bwis []byte
	for _, m := range sim.Commands() {
		if m.Type == ccid.PCtoRDRXfrBlock && m.Param[0] != 0 {
			bwis = append(bwis, m.Param[0])
		}
	}
	// Each S(WTX response) gets the answer after it a bBWI of 1.
	if !bytes.Equal(bwis, []byte{1, 1}) {
		t.Errorf("bBWI of the XfrBlocks after WTX: %v", bwis)
	}
	// The card's IFSC is now 64: 100 bytes go in two blocks.
	before := len(sim.Commands())
	transmit(t, c, apdu...)
	var iblocks int
	for _, m := range sim.Commands()[before:] {
		if m.Type == ccid.PCtoRDRXfrBlock && m.Data[1]&0x80 == 0 {
			iblocks++
		}
	}
	if iblocks != 2 {
		t.Errorf("%d I-blocks for 105 bytes at IFSC 64, want 2", iblocks)
	}
}

func TestT1Recovery(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    ccidtest.Options
	}{
		{"lost answer", ccidtest.Options{T1LoseResponses: 1}},
		{"two lost answers", ccidtest.Options{T1LoseResponses: 2}},
		{"corrupt answer", ccidtest.Options{T1CorruptResponses: 1}},
		{"corrupt and lost", ccidtest.Options{T1CorruptResponses: 1, T1LoseResponses: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := &testCard{atr: atrT1(0x20)}
			c, sim := openT1(t, card, ccidtest.Options{})
			apdu, want := echoAPDU(pattern(100))
			transmit(t, c, apdu...)
			// The faults hit the next exchange, in the middle of a chain.
			sim.SetFaults(tc.o)
			if resp := transmit(t, c, apdu...); !bytes.Equal(resp, want) {
				t.Fatalf("after %s: % x", tc.name, resp)
			}
			if got := len(card.seen); got != 2 {
				t.Errorf("the card got %d APDUs, want 2", got)
			}
			if resp := transmit(t, c, apdu...); !bytes.Equal(resp, want) {
				t.Fatalf("next APDU: % x", resp)
			}
		})
	}
}

func TestT1Resynchronize(t *testing.T) {
	card := &testCard{atr: atrT1(0x20)}
	c, sim := openT1(t, card, ccidtest.Options{})
	sim.SetFaults(ccidtest.Options{T1LoseResponses: 3})
	apdu, want := echoAPDU(pattern(10))
	if _, err := c.Transmit(context.Background(), apdu); !errors.Is(err, ccid.ErrResynchronized) {
		t.Fatalf("after 3 lost answers: %v, want ErrResynchronized", err)
	}
	if sim.T1().Resynchs != 1 {
		t.Errorf("resynchs %d", sim.T1().Resynchs)
	}
	// The protocol works again from sequence numbers 0.
	if resp := transmit(t, c, apdu...); !bytes.Equal(resp, want) {
		t.Fatalf("after resynchronization: % x", resp)
	}

	c, sim = openT1(t, card, ccidtest.Options{T1IgnoreResynch: true})
	sim.SetFaults(ccidtest.Options{T1LoseResponses: 3, T1IgnoreResynch: true})
	if _, err := c.Transmit(context.Background(), apdu); !errors.Is(err, ccid.ErrProtocol) {
		t.Fatalf("resynchronization refused: %v, want ErrProtocol", err)
	}
}

func TestT1WithCRCUnsupported(t *testing.T) {
	// TC3 bit 1: CRC.
	b := []byte{0x3B, 0x80, 0x81, 0x51, 0x20, 0x01}
	r, _ := open(t, &testCard{atr: append(b, tck(b))}, ccidtest.Options{})
	if _, err := r.Connect(context.Background(), 0, &ccid.ConnectOptions{Prefer: []ccid.Protocol{ccid.T1}}); !errors.Is(err, ccid.ErrNotSupported) {
		t.Fatalf("T=1 with CRC: %v", err)
	}
}
