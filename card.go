package ccid

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ConnectOptions configure Reader.Connect.
type ConnectOptions struct {
	// Prefer lists the protocols to use, in order of preference, among
	// those the card offers and the reader supports; T=0, then T=1, when
	// empty. A card in specific mode uses its own.
	Prefer []Protocol
	// Voltage powers the card at Voltage5V, Voltage3V or Voltage18V. When
	// 0, the reader chooses if it selects voltages itself, or else the
	// voltages it supports are tried from the lowest, as ISO/IEC 7816-3
	// advises.
	Voltage byte
	// AutoGetResponse, at TPDU level with T=0, has Transmit fetch the
	// response of 61xx with GET RESPONSE and resend a command of case 2
	// with the Le of 6Cxx, returning the whole response, as the transport
	// layer of ISO/IEC 7816-3 does. Off, the status words come back as the
	// card sends them, as with PC/SC.
	AutoGetResponse bool
}

// Card is a card connected in a reader's slot.
type Card struct {
	r      *Reader
	slot   byte
	atr    ATR
	proto  Protocol
	level  ExchangeLevel
	autoGR bool
	closed bool
}

// ATR returns the card's ATR.
func (c *Card) ATR() ATR { return c.atr }

// Protocol returns the protocol in use.
func (c *Card) Protocol() Protocol { return c.proto }

// Slot returns the card's slot.
func (c *Card) Slot() byte { return c.slot }

// Connect powers on the card in a slot, reads its ATR, chooses the
// protocol and sets the reader's parameters for it.
func (r *Reader) Connect(ctx context.Context, slot byte, o *ConnectOptions) (*Card, error) {
	if o == nil {
		o = &ConnectOptions{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	level := r.desc.ExchangeLevel()
	switch level {
	case LevelTPDU, LevelShortAPDU, LevelExtendedAPDU:
	default:
		return nil, fmt.Errorf("%w: reader at %v level", ErrNotSupported, level)
	}
	raw, err := r.powerOn(ctx, slot, o.Voltage)
	if err != nil {
		return nil, err
	}
	c := &Card{r: r, slot: slot, level: level, autoGR: o.AutoGetResponse}
	fail := func(err error) (*Card, error) {
		r.exchange(ctx, Message{Type: PCtoRDRIccPowerOff, Slot: slot})
		return nil, err
	}
	if c.atr, err = ParseATR(raw); err != nil {
		return fail(err)
	}
	if c.proto, err = r.chooseProtocol(c.atr, o.Prefer); err != nil {
		return fail(err)
	}
	if level == LevelTPDU && c.proto == T1 {
		return fail(fmt.Errorf("%w: T=1 at TPDU level", ErrNotSupported))
	}
	if level == LevelTPDU || r.desc.Features&FeatureAutoParameters == 0 {
		if err := r.setParameters(ctx, slot, c.atr, c.proto); err != nil {
			return fail(err)
		}
	} else if p, err := r.getProtocol(ctx, slot); err == nil {
		// The reader set the parameters from the ATR itself.
		c.proto = p
	}
	return c, nil
}

// powerSelect maps the voltages of bVoltageSupport to bPowerSelect.
var powerSelect = map[byte]byte{0: 0, Voltage5V: 1, Voltage3V: 2, Voltage18V: 3}

func (r *Reader) powerOn(ctx context.Context, slot, voltage byte) ([]byte, error) {
	var tries []byte
	switch {
	case voltage != 0:
		if _, ok := powerSelect[voltage]; !ok {
			return nil, fmt.Errorf("ccid: voltage %#02x is not one of Voltage5V, Voltage3V, Voltage18V", voltage)
		}
		tries = []byte{voltage}
	case r.desc.Features&FeatureAutoVoltage != 0:
		tries = []byte{0}
	default:
		for _, v := range []byte{Voltage18V, Voltage3V, Voltage5V} {
			if r.desc.VoltageSupport&v != 0 {
				tries = append(tries, v)
			}
		}
		if len(tries) == 0 {
			tries = []byte{0}
		}
	}
	var last error
	for i, v := range tries {
		if i > 0 {
			r.exchange(ctx, Message{Type: PCtoRDRIccPowerOff, Slot: slot})
		}
		res, err := r.exchange(ctx, Message{Type: PCtoRDRIccPowerOn, Slot: slot, Param: [3]byte{powerSelect[v]}})
		if err == nil {
			if res.Type != RDRtoPCDataBlock {
				return nil, fmt.Errorf("%w: %v answered with %v", ErrProtocol, PCtoRDRIccPowerOn, res.Type)
			}
			return res.Data, nil
		}
		last = err
		var ce *CommandError
		if !errors.As(err, &ce) || ce.ICC == ICCAbsent {
			return nil, err
		}
		switch ce.Code {
		case errICCMute, errBadATRTS, errBadATRTCK, errClassNotSupported:
			// The card may want another voltage.
		default:
			return nil, err
		}
	}
	return nil, last
}

func (r *Reader) chooseProtocol(atr ATR, prefer []Protocol) (Protocol, error) {
	offered := atr.Protocols()
	if p, ok := atr.SpecificMode(); ok {
		offered = []Protocol{p}
	}
	if len(prefer) == 0 {
		prefer = []Protocol{T0, T1}
	}
	for _, p := range prefer {
		if slices.Contains(offered, p) && (r.desc.Protocols == 0 || r.desc.SupportsProtocol(p)) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("%w: the card offers %v, the reader supports %#x, %v preferred", ErrNotSupported, offered, r.desc.Protocols, prefer)
}

// fiDiOffset is the offset of bmFindexDindex in PC_to_RDR_SetParameters,
// which bError names when the reader takes not the card's rate.
const fiDiOffset = HeaderLength

func (r *Reader) setParameters(ctx context.Context, slot byte, atr ATR, p Protocol) error {
	fidi := atr.FiDi()
	_, specific := atr.SpecificMode()
	if !specific && r.desc.Features&(FeatureAutoPPSProposal|FeatureAutoPPSCurrent) == 0 {
		// Without a PPS, a card in negotiable mode works at the default
		// rate.
		fidi = 0x11
	}
	var conv byte
	if atr.Inverse {
		conv = 0x02
	}
	data := func(fidi byte) []byte {
		if p == T1 {
			tcck := 0x10 | conv
			if atr.CRC() {
				tcck |= 0x01
			}
			return []byte{fidi, tcck, atr.ExtraGuardTime(), atr.WaitingIntegersT1(), 0x00, atr.IFSC(), 0x00}
		}
		return []byte{fidi, conv, atr.ExtraGuardTime(), atr.WaitingIntegerT0(), 0x00}
	}
	m := Message{Type: PCtoRDRSetParameters, Slot: slot, Param: [3]byte{byte(p)}, Data: data(fidi)}
	res, err := r.exchange(ctx, m)
	var ce *CommandError
	if errors.As(err, &ce) && ce.Code == fiDiOffset && fidi != 0x11 && !specific {
		m.Data = data(0x11)
		res, err = r.exchange(ctx, m)
	}
	if err != nil {
		return err
	}
	if res.Type != RDRtoPCParameters {
		return fmt.Errorf("%w: %v answered with %v", ErrProtocol, PCtoRDRSetParameters, res.Type)
	}
	return nil
}

func (r *Reader) getProtocol(ctx context.Context, slot byte) (Protocol, error) {
	res, err := r.exchange(ctx, Message{Type: PCtoRDRGetParameters, Slot: slot})
	if err != nil {
		return 0, err
	}
	if res.Type != RDRtoPCParameters || res.Param[2] > byte(T1) {
		return 0, ErrProtocol
	}
	return Protocol(res.Param[2]), nil
}

// Disconnect powers the card off. The Card is not usable after it.
func (c *Card) Disconnect(ctx context.Context) error {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_, err := c.r.exchange(ctx, Message{Type: PCtoRDRIccPowerOff, Slot: c.slot})
	return err
}

// Transmit sends a command APDU and returns the response APDU, its data
// and status words.
func (c *Card) Transmit(ctx context.Context, apdu []byte) ([]byte, error) {
	cmd, err := parseCommand(apdu)
	if err != nil {
		return nil, err
	}
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	if c.closed {
		return nil, ErrDisconnected
	}
	switch c.level {
	case LevelShortAPDU:
		if cmd.extended {
			return nil, fmt.Errorf("%w: extended APDU on a short APDU level reader", ErrNotSupported)
		}
		return c.transmitAPDU(ctx, apdu)
	case LevelExtendedAPDU:
		return c.transmitAPDU(ctx, apdu)
	}
	return c.transmitT0(ctx, cmd)
}

// xfr sends one XfrBlock and returns its RDR_to_PC_DataBlock.
func (c *Card) xfr(ctx context.Context, data []byte, level uint16) (Message, error) {
	res, err := c.r.exchange(ctx, Message{
		Type:  PCtoRDRXfrBlock,
		Slot:  c.slot,
		Param: [3]byte{0, byte(level), byte(level >> 8)},
		Data:  data,
	})
	if err != nil {
		return Message{}, err
	}
	if res.Type != RDRtoPCDataBlock {
		return Message{}, fmt.Errorf("%w: %v answered with %v", ErrProtocol, PCtoRDRXfrBlock, res.Type)
	}
	return res, nil
}

// transmitAPDU sends an APDU to a reader at APDU level, in chained blocks
// when it is longer than a message holds, and gathers the response's.
func (c *Card) transmitAPDU(ctx context.Context, apdu []byte) ([]byte, error) {
	room := c.r.maxLen - HeaderLength
	var res Message
	for off := 0; ; {
		end := min(off+room, len(apdu))
		first, last := off == 0, end == len(apdu)
		var level uint16
		switch {
		case first && last:
			level = chainBeginEnd
		case first:
			level = chainBeginMore
		case last:
			level = chainContinueEnd
		default:
			level = chainContinueMore
		}
		var err error
		if res, err = c.xfr(ctx, apdu[off:end], level); err != nil {
			return nil, err
		}
		off = end
		if last {
			break
		}
		if res.ChainParameter() != chainEmptyContinue {
			return nil, fmt.Errorf("%w: chained command block answered with bChainParameter %#02x", ErrProtocol, res.ChainParameter())
		}
	}
	out := slices.Clone(res.Data)
	for cp := res.ChainParameter(); cp != chainBeginEnd && cp != chainContinueEnd; cp = res.ChainParameter() {
		if cp != chainBeginMore && cp != chainContinueMore {
			return nil, fmt.Errorf("%w: response block with bChainParameter %#02x", ErrProtocol, cp)
		}
		var err error
		if res, err = c.xfr(ctx, nil, chainEmptyContinue); err != nil {
			return nil, err
		}
		out = append(out, res.Data...)
	}
	return out, nil
}

// maxGetResponse bounds the GET RESPONSE commands of one response: 256
// of 256 bytes is the longest response there is.
const maxGetResponse = 256

// transmitT0 sends an APDU as a T=0 TPDU (ISO/IEC 7816-3, 12.2).
func (c *Card) transmitT0(ctx context.Context, cmd command) ([]byte, error) {
	if cmd.extended {
		return nil, fmt.Errorf("%w: extended APDU over T=0 (ENVELOPE)", ErrNotSupported)
	}
	tpdu := append(cmd.header[:], 0)
	switch cmd.cas {
	case 2:
		tpdu[4] = byte(cmd.le) // 256 is 0
	case 3, 4:
		// A case 4 command goes without its Le; the card answers 61xx.
		tpdu[4] = byte(len(cmd.data))
		tpdu = append(tpdu, cmd.data...)
	}
	send := func(tpdu []byte) ([]byte, byte, byte, error) {
		res, err := c.xfr(ctx, tpdu, 0)
		if err != nil {
			return nil, 0, 0, err
		}
		if len(res.Data) < 2 {
			return nil, 0, 0, fmt.Errorf("%w: response of %d bytes without status words", ErrProtocol, len(res.Data))
		}
		n := len(res.Data)
		return res.Data, res.Data[n-2], res.Data[n-1], nil
	}
	resp, sw1, sw2, err := send(tpdu)
	if err != nil || !c.autoGR {
		return resp, err
	}
	if cmd.cas == 2 && sw1 == 0x6C {
		tpdu[4] = sw2
		if resp, sw1, sw2, err = send(tpdu); err != nil {
			return nil, err
		}
	}
	var out []byte
	for i := 0; sw1 == 0x61; i++ {
		if i == maxGetResponse {
			return nil, fmt.Errorf("%w: GET RESPONSE does not end", ErrProtocol)
		}
		out = append(out, resp[:len(resp)-2]...)
		gr := []byte{getResponseClass(cmd.header[0]), 0xC0, 0x00, 0x00, sw2}
		if resp, sw1, sw2, err = send(gr); err != nil {
			return nil, err
		}
		if sw1 == 0x6C {
			gr[4] = sw2
			if resp, sw1, sw2, err = send(gr); err != nil {
				return nil, err
			}
		}
	}
	return append(out, resp...), nil
}
