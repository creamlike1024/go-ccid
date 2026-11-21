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

var sw9000 = []byte{0x90, 0x00}

// testCard answers INS 0xCA with n bytes (0, 1, 2, ...), INS 0xEE with
// the command's data reversed, and anything else with 9000 alone. It
// records the commands it receives.
type testCard struct {
	atr  []byte
	n    int
	seen [][]byte
}

func (c *testCard) ATR() []byte { return c.atr }

func (c *testCard) Transmit(apdu []byte) []byte {
	c.seen = append(c.seen, slices.Clone(apdu))
	switch apdu[1] {
	case 0xCA:
		return append(pattern(c.n), sw9000...)
	case 0xEE:
		data := slices.Clone(dataOf(apdu))
		slices.Reverse(data)
		return append(data, sw9000...)
	}
	return slices.Clone(sw9000)
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// dataOf returns the data of a short or extended case 3 or 4 APDU.
func dataOf(apdu []byte) []byte {
	if len(apdu) <= 5 {
		return nil
	}
	if apdu[4] != 0 {
		return apdu[5 : 5+int(apdu[4])]
	}
	n := int(apdu[5])<<8 | int(apdu[6])
	return apdu[7 : 7+n]
}

func open(t *testing.T, card ccidtest.Card, o ccidtest.Options) (*ccid.Reader, *ccidtest.Reader) {
	t.Helper()
	sim := ccidtest.NewReader(card, o)
	r, err := ccid.Open(sim, sim.Descriptor(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, sim
}

func connect(t *testing.T, r *ccid.Reader, o *ccid.ConnectOptions) *ccid.Card {
	t.Helper()
	c, err := r.Connect(context.Background(), 0, o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func transmit(t *testing.T, c *ccid.Card, apdu ...byte) []byte {
	t.Helper()
	resp, err := c.Transmit(context.Background(), apdu)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestConnectSetsT0Parameters(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0}, ccidtest.Options{})
	c := connect(t, r, nil)
	if c.Protocol() != ccid.T0 {
		t.Errorf("protocol %v", c.Protocol())
	}
	p, params := sim.Parameters()
	// Fd/Dd, direct convention, no extra guard time, WI 10, no clock stop.
	if p != 0 || !bytes.Equal(params, []byte{0x11, 0x00, 0x00, 10, 0x00}) {
		t.Errorf("SetParameters T=%d % x", p, params)
	}
}

func TestConnectRate(t *testing.T) {
	// Without PPS by the reader, a negotiable card stays at the default
	// rate.
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0T1}, ccidtest.Options{})
	connect(t, r, nil)
	if _, params := sim.Parameters(); params[0] != 0x11 {
		t.Errorf("without PPS: bmFindexDindex %#x, want 0x11", params[0])
	}
	// A reader that negotiates PPS gets the card's TA1.
	r, sim = open(t, &testCard{atr: ccidtest.ATRT0T1}, ccidtest.Options{Features: ccid.FeatureAutoPPSProposal})
	connect(t, r, nil)
	if _, params := sim.Parameters(); params[0] != 0x96 {
		t.Errorf("with PPS: bmFindexDindex %#x, want 0x96", params[0])
	}
	// A reader that turns the rate down is asked for the default.
	r, sim = open(t, &testCard{atr: ccidtest.ATRT0T1}, ccidtest.Options{Features: ccid.FeatureAutoPPSProposal, RejectFiDi: true})
	connect(t, r, nil)
	if _, params := sim.Parameters(); params[0] != 0x11 {
		t.Errorf("rate refused: bmFindexDindex %#x, want 0x11", params[0])
	}
}

func TestConnectTriesVoltages(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0}, ccidtest.Options{CardVoltages: ccid.Voltage5V})
	connect(t, r, nil)
	var selects []byte
	for _, m := range sim.Commands() {
		if m.Type == ccid.PCtoRDRIccPowerOn {
			selects = append(selects, m.Param[0])
		}
	}
	// 1.8 V, then 3 V, then 5 V.
	if !bytes.Equal(selects, []byte{3, 2, 1}) {
		t.Errorf("bPowerSelect %v, want [3 2 1]", selects)
	}
	r, sim = open(t, &testCard{atr: ccidtest.ATRT0}, ccidtest.Options{Features: ccid.FeatureAutoVoltage})
	connect(t, r, nil)
	if m := sim.Commands()[0]; m.Type != ccid.PCtoRDRIccPowerOn || m.Param[0] != 0 {
		t.Errorf("automatic voltage: first command %v", m)
	}
}

func TestConnectNoCard(t *testing.T) {
	r, _ := open(t, nil, ccidtest.Options{})
	if _, err := r.Connect(context.Background(), 0, nil); !errors.Is(err, ccid.ErrNoCard) {
		t.Fatalf("Connect without a card: %v", err)
	}
	if _, err := r.Connect(context.Background(), 0, nil); errors.Is(err, ccid.ErrCardMute) {
		t.Error("no card reported as a mute card")
	}
}

func TestConnectCharacterLevelUnsupported(t *testing.T) {
	sim := ccidtest.NewReader(&testCard{atr: ccidtest.ATRT0}, ccidtest.Options{})
	d := sim.Descriptor()
	d.Features &^= uint32(ccid.LevelTPDU)
	r, _ := ccid.Open(sim, d, nil)
	if _, err := r.Connect(context.Background(), 0, nil); !errors.Is(err, ccid.ErrNotSupported) {
		t.Fatalf("character level: %v", err)
	}
}

func TestT0Cases(t *testing.T) {
	card := &testCard{atr: ccidtest.ATRT0, n: 16}
	r, _ := open(t, card, ccidtest.Options{})
	c := connect(t, r, nil)

	// Case 1 and case 3.
	if resp := transmit(t, c, 0x00, 0x70, 0x00, 0x00); !bytes.Equal(resp, sw9000) {
		t.Errorf("case 1: % x", resp)
	}
	if resp := transmit(t, c, 0x00, 0xD6, 0x00, 0x00, 0x02, 0x01, 0x02); !bytes.Equal(resp, sw9000) {
		t.Errorf("case 3: % x", resp)
	}
	// Case 2 with the right Le.
	if resp := transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 0x10); !bytes.Equal(resp, append(pattern(16), sw9000...)) {
		t.Errorf("case 2: % x", resp)
	}
	// Case 4: the TPDU leaves Le out, and the card's 61xx comes back.
	resp := transmit(t, c, 0x80, 0xEE, 0x00, 0x00, 0x03, 0x01, 0x02, 0x03, 0x00)
	if !bytes.Equal(resp, []byte{0x61, 0x03}) {
		t.Errorf("case 4 without GET RESPONSE: % x", resp)
	}
	if got := card.seen[len(card.seen)-1]; !bytes.Equal(got, []byte{0x80, 0xEE, 0x00, 0x00, 0x03, 0x01, 0x02, 0x03, 0x00}) {
		t.Errorf("TPDU reached the card as % x", got)
	}
	// Case 2 with the wrong Le comes back as 6Cxx.
	if resp := transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 0x00); !bytes.Equal(resp, []byte{0x6C, 0x10}) {
		t.Errorf("case 2 with Le 00: % x", resp)
	}
}

func TestT0AutoGetResponse(t *testing.T) {
	card := &testCard{atr: ccidtest.ATRT0, n: 300}
	r, sim := open(t, card, ccidtest.Options{})
	c := connect(t, r, &ccid.ConnectOptions{AutoGetResponse: true})

	// Case 4 on logical channel 1: one GET RESPONSE, of class 01.
	resp := transmit(t, c, 0x81, 0xEE, 0x00, 0x00, 0x03, 0x01, 0x02, 0x03, 0x00)
	if !bytes.Equal(resp, []byte{0x03, 0x02, 0x01, 0x90, 0x00}) {
		t.Errorf("case 4: % x", resp)
	}
	var gr []ccid.Message
	for _, m := range sim.Commands() {
		if m.Type == ccid.PCtoRDRXfrBlock && len(m.Data) == 5 && m.Data[1] == 0xC0 {
			gr = append(gr, m)
		}
	}
	if len(gr) != 1 || gr[0].Data[0] != 0x01 {
		t.Errorf("GET RESPONSE commands %v, want one of class 01", gr)
	}

	// Case 4 with 300 bytes of response: 6100, then 256 bytes and 612C,
	// then the last 44.
	resp = transmit(t, c, 0x80, 0xCA, 0x00, 0x00, 0x05, 1, 2, 3, 4, 5, 0x00)
	if !bytes.Equal(resp, append(pattern(300), sw9000...)) {
		t.Errorf("long case 4: %d bytes, ends % x", len(resp), resp[len(resp)-2:])
	}

	// Case 2 with the wrong Le is resent with the card's.
	card.n = 16
	if resp := transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 0x00); !bytes.Equal(resp, append(pattern(16), sw9000...)) {
		t.Errorf("case 2 with Le corrected: % x", resp)
	}
}

func TestExtendedAPDUOverT0Unsupported(t *testing.T) {
	r, _ := open(t, &testCard{atr: ccidtest.ATRT0}, ccidtest.Options{})
	c := connect(t, r, nil)
	if _, err := c.Transmit(context.Background(), []byte{0x00, 0xB0, 0x00, 0x00, 0x00, 0x01, 0x00}); !errors.Is(err, ccid.ErrNotSupported) {
		t.Errorf("extended APDU over T=0: %v", err)
	}
	if _, err := c.Transmit(context.Background(), []byte{0x00, 0xA4}); !errors.Is(err, ccid.ErrInvalidAPDU) {
		t.Errorf("short APDU: %v", err)
	}
}

func TestShortAPDULevel(t *testing.T) {
	card := &testCard{atr: ccidtest.ATRT0T1, n: 200}
	r, sim := open(t, card, ccidtest.Options{Level: ccid.LevelShortAPDU, Features: ccid.FeatureAutoParameters})
	c := connect(t, r, nil)
	apdu := []byte{0x80, 0xEE, 0x00, 0x00, 0x03, 0x01, 0x02, 0x03, 0x00}
	if resp := transmit(t, c, apdu...); !bytes.Equal(resp, []byte{0x03, 0x02, 0x01, 0x90, 0x00}) {
		t.Errorf("case 4: % x", resp)
	}
	// The APDU goes as it is, Le included.
	if got := card.seen[len(card.seen)-1]; !bytes.Equal(got, apdu) {
		t.Errorf("APDU reached the card as % x", got)
	}
	// The reader set the parameters itself: no SetParameters.
	for _, m := range sim.Commands() {
		if m.Type == ccid.PCtoRDRSetParameters {
			t.Error("SetParameters sent to a reader that configures itself")
		}
	}
	if _, err := c.Transmit(context.Background(), []byte{0x00, 0xB0, 0x00, 0x00, 0x00, 0x01, 0x00}); !errors.Is(err, ccid.ErrNotSupported) {
		t.Errorf("extended APDU at short APDU level: %v", err)
	}
}

func TestExtendedAPDUChaining(t *testing.T) {
	card := &testCard{atr: ccidtest.ATRT0T1, n: 1000}
	r, sim := open(t, card, ccidtest.Options{Level: ccid.LevelExtendedAPDU, Features: ccid.FeatureAutoParameters})
	c := connect(t, r, nil)

	data := pattern(700)
	apdu := append([]byte{0x80, 0xEE, 0x00, 0x00, 0x00, byte(len(data) >> 8), byte(len(data))}, data...)
	apdu = append(apdu, 0x00, 0x00)
	resp := transmit(t, c, apdu...)
	want := slices.Clone(data)
	slices.Reverse(want)
	if !bytes.Equal(resp, append(want, sw9000...)) {
		t.Fatalf("700 bytes echoed reversed: got %d bytes", len(resp))
	}
	if got := card.seen[len(card.seen)-1]; !bytes.Equal(got, apdu) {
		t.Errorf("chained APDU reassembled as %d bytes, want %d", len(got), len(apdu))
	}
	var levels []uint16
	for _, m := range sim.Commands() {
		if m.Type == ccid.PCtoRDRXfrBlock {
			levels = append(levels, uint16(m.Param[1])|uint16(m.Param[2])<<8)
		}
	}
	// 709 bytes in blocks of 261: begin, continue, end; then 702 bytes of
	// response in blocks of 261: two continuation requests.
	if !slices.Equal(levels, []uint16{0x01, 0x03, 0x02, 0x10, 0x10}) {
		t.Errorf("wLevelParameter of the XfrBlocks %#x", levels)
	}

	resp = transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 0x00, 0x03, 0xE8)
	if !bytes.Equal(resp, append(pattern(1000), sw9000...)) {
		t.Errorf("1000 bytes of response: got %d", len(resp))
	}
}

func TestTimeExtensionsAndTransfers(t *testing.T) {
	card := &testCard{atr: ccidtest.ATRT0, n: 100}
	r, _ := open(t, card, ccidtest.Options{TimeExtensions: 3, ChunkSize: 7})
	c := connect(t, r, nil)
	if resp := transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 100); !bytes.Equal(resp, append(pattern(100), sw9000...)) {
		t.Errorf("response after time extensions, in 7 byte transfers: % x", resp)
	}
}

func TestStaleResponseDropped(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0}, ccidtest.Options{})
	c := connect(t, r, nil)
	stale, _ := ccid.Message{Type: ccid.RDRtoPCDataBlock, Seq: 0xEE, Data: []byte{0x6F, 0x00}}.MarshalBinary()
	sim.Inject(stale)
	if resp := transmit(t, c, 0x00, 0x70, 0x00, 0x00); !bytes.Equal(resp, sw9000) {
		t.Errorf("response after a stale one: % x", resp)
	}
}

func TestCancelledContext(t *testing.T) {
	r, _ := open(t, &testCard{atr: ccidtest.ATRT0}, ccidtest.Options{})
	c := connect(t, r, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Transmit(ctx, []byte{0x00, 0x70, 0x00, 0x00}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: %v", err)
	}
	// The late response is dropped by the next exchange.
	if resp := transmit(t, c, 0x00, 0x70, 0x00, 0x00); !bytes.Equal(resp, sw9000) {
		t.Errorf("after a cancelled exchange: % x", resp)
	}
}

func TestDisconnect(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0}, ccidtest.Options{})
	c := connect(t, r, nil)
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Transmit(context.Background(), []byte{0x00, 0x70, 0x00, 0x00}); !errors.Is(err, ccid.ErrDisconnected) {
		t.Errorf("Transmit after Disconnect: %v", err)
	}
	if s, err := r.SlotStatus(context.Background(), 0); err != nil || s != ccid.ICCInactive {
		t.Errorf("slot after Disconnect: %v %v", s, err)
	}
	sim.Remove()
	if s, err := r.SlotStatus(context.Background(), 0); err != nil || s != ccid.ICCAbsent {
		t.Errorf("slot after removal: %v %v", s, err)
	}
}

func TestCardRemoved(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0}, ccidtest.Options{})
	c := connect(t, r, nil)
	sim.Remove()
	if _, err := c.Transmit(context.Background(), []byte{0x00, 0x70, 0x00, 0x00}); !errors.Is(err, ccid.ErrNoCard) {
		t.Errorf("Transmit with the card removed: %v", err)
	}
}

func TestEscapeAndTrace(t *testing.T) {
	sim := ccidtest.NewReader(nil, ccidtest.Options{})
	var traced []ccid.Direction
	r, _ := ccid.Open(sim, sim.Descriptor(), &ccid.Options{Trace: func(d ccid.Direction, msg []byte) {
		traced = append(traced, d)
	}})
	got, err := r.Escape(context.Background(), 0, []byte{1, 2, 3})
	if err != nil || !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("Escape: % x %v", got, err)
	}
	if !slices.Equal(traced, []ccid.Direction{ccid.ToReader, ccid.FromReader}) {
		t.Errorf("trace %v", traced)
	}
}
