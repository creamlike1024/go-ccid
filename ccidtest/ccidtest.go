// Package ccidtest simulates a CCID reader and the card in it, for tests
// of package ccid and of the programs that use it: a Reader is a
// ccid.Transport that answers the commands written to it as a reader
// would.
package ccidtest

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"sync"

	ccid "github.com/creamlike1024/go-ccid"
)

// Card is a simulated card.
type Card interface {
	// ATR returns the card's ATR.
	ATR() []byte
	// Transmit answers a command APDU with the response APDU, its data and
	// status words. At TPDU level a command of case 3 or 4 comes with an
	// Le of 00, as T=0 does not tell them apart.
	Transmit(apdu []byte) []byte
}

// FuncCard is a Card of an ATR and a function.
type FuncCard struct {
	Answer []byte
	Func   func(apdu []byte) []byte
}

func (c FuncCard) ATR() []byte                 { return c.Answer }
func (c FuncCard) Transmit(apdu []byte) []byte { return c.Func(apdu) }

// ATRT0 is the ATR of a T=0 card at the default rate.
var ATRT0 = []byte{0x3B, 0x00}

// ATRT0T1 is the ATR of a card offering T=0 and T=1 in negotiable mode:
// TA1 0x96 (Fi 512, Di 32), and for T=1 an IFSC of 254 and BWI 4, CWI 5.
var ATRT0T1 = []byte{0x3B, 0x90, 0x96, 0x80, 0x31, 0xFE, 0x45, 0x0C}

// Options configure a Reader.
type Options struct {
	// Level is the exchange level; LevelTPDU when 0 (the character level
	// is not simulated).
	Level ccid.ExchangeLevel
	// MaxMessageLength is dwMaxCCIDMessageLength; 271 when 0.
	MaxMessageLength int
	// Features are dwFeatures besides the exchange level.
	Features uint32
	// Protocols is dwProtocols; T=0 and T=1 when 0.
	Protocols uint32
	// Voltages is bVoltageSupport; all three when 0.
	Voltages byte
	// CardVoltages are the voltages the card answers at; all when 0.
	CardVoltages byte
	// TimeExtensions is how many time extensions precede the answer to
	// each XfrBlock.
	TimeExtensions int
	// ChunkSize splits responses into transfers of this many bytes; whole
	// when 0.
	ChunkSize int
	// RejectFiDi makes SetParameters fail at bmFindexDindex for any value
	// but 0x11.
	RejectFiDi bool
	// MaxDataRate is dwMaxDataRate; 344086 when 0.
	MaxDataRate uint32
	// RejectPPS has the card not answer a PPS, and then not answer
	// anything until it is powered on again.
	RejectPPS bool
	// PPSDefault has the card answer a PPS without PPS1: the default rate.
	PPSDefault bool

	// T=1 at TPDU level: the card's faults.

	// T1WTX is how many S(WTX request) precede each answer to a complete
	// APDU.
	T1WTX int
	// T1LoseResponses loses the next answers of the card: the reader
	// reports ICC_MUTE, as at the end of the block waiting time.
	T1LoseResponses int
	// T1CorruptResponses spoils the LRC of the next answers.
	T1CorruptResponses int
	// T1IFSRequest, when not 0, has the card send S(IFS request) with it
	// before its first answer to an APDU.
	T1IFSRequest byte
	// T1IgnoreResynch has the card not answer S(RESYNCH request).
	T1IgnoreResynch bool
}

// ErrTimeout is returned by Read when no response is pending, as a USB
// read would time out.
var ErrTimeout = errors.New("ccidtest: read timed out")

// Reader is a simulated CCID reader with one slot.
type Reader struct {
	mu       sync.Mutex
	o        Options
	card     Card
	powered  bool
	protocol int // -1 until SetParameters
	params   []byte
	out      []byte // pending response bytes
	left     int    // bytes of out left in the message being read
	commands []ccid.Message

	// T=0 TPDU state: the data that GET RESPONSE returns.
	pending []byte
	// APDU level chaining state.
	cmdChain  []byte
	respChain []byte
	closed    bool

	t1 t1Card

	ppsReq   []byte // the last PPS request
	confused bool   // after a failed PPS, until powered on again
	powerOns int
}

// PPS returns the last PPS request received, nil before one.
func (r *Reader) PPS() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ppsReq)
}

// PowerOns counts the IccPowerOn commands that powered the card.
func (r *Reader) PowerOns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.powerOns
}

// t1Card is the state of the simulated card's T=1 protocol.
type t1Card struct {
	ns, nr     byte   // N(S) of its next I-block, N(S) expected of the host
	ifsd       int    // the host's information field size
	ifsc       int    // its own, 0 for the ATR's
	cmd        []byte // the command APDU being received
	resp       []byte // the rest of the response to send
	last       []byte // the last block sent, for retransmission
	wtxLeft    int
	ifsDone    bool
	resyncs    int
	gotIFSD    byte
	pendingRsp bool // the response waits behind WTX or IFS requests
}

// T1Stats are what the simulated T=1 card saw.
type T1Stats struct {
	// IFSD is the INF of the host's last S(IFS request), 0 without one.
	IFSD byte
	// Resynchs counts the S(RESYNCH request) answered.
	Resynchs int
}

// SetFaults sets the T=1 faults of the options (T1WTX, T1LoseResponses,
// T1CorruptResponses, T1IFSRequest, T1IgnoreResynch) from now on.
func (r *Reader) SetFaults(o Options) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.o.T1WTX, r.o.T1LoseResponses, r.o.T1CorruptResponses = o.T1WTX, o.T1LoseResponses, o.T1CorruptResponses
	r.o.T1IFSRequest, r.o.T1IgnoreResynch = o.T1IFSRequest, o.T1IgnoreResynch
}

// T1 returns what the simulated T=1 card saw.
func (r *Reader) T1() T1Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return T1Stats{IFSD: r.t1.gotIFSD, Resynchs: r.t1.resyncs}
}

// NewReader returns a simulated reader holding card, which may be nil.
func NewReader(card Card, o Options) *Reader {
	if o.Level == 0 {
		o.Level = ccid.LevelTPDU
	}
	if o.MaxMessageLength == 0 {
		o.MaxMessageLength = 271
	}
	if o.Protocols == 0 {
		o.Protocols = 3
	}
	if o.MaxDataRate == 0 {
		o.MaxDataRate = 344086
	}
	if o.Voltages == 0 {
		o.Voltages = ccid.Voltage5V | ccid.Voltage3V | ccid.Voltage18V
	}
	return &Reader{o: o, card: card, protocol: -1}
}

// Descriptor returns the reader's class descriptor.
func (r *Reader) Descriptor() ccid.Descriptor {
	return ccid.Descriptor{
		CCIDVersion:      0x0110,
		VoltageSupport:   r.o.Voltages,
		Protocols:        r.o.Protocols,
		DefaultClock:     4000,
		MaximumClock:     4000,
		DataRate:         10752,
		MaxDataRate:      r.o.MaxDataRate,
		MaxIFSD:          254,
		Features:         uint32(r.o.Level) | r.o.Features,
		MaxMessageLength: uint32(r.o.MaxMessageLength),
		ClassGetResponse: 0xFF,
		ClassEnvelope:    0xFF,
		MaxCCIDBusySlots: 1,
	}
}

// Insert puts a card in the slot, unpowered.
func (r *Reader) Insert(c Card) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.card, r.powered, r.protocol = c, false, -1
}

// Remove takes the card out.
func (r *Reader) Remove() { r.Insert(nil) }

// Inject queues raw bytes to be read before the next response, as the
// late response of an earlier command.
func (r *Reader) Inject(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.out = append(r.out, b...)
}

// Commands returns the command messages received so far.
func (r *Reader) Commands() []ccid.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.commands)
}

// Parameters returns the protocol and the abProtocolDataStructure of the
// last SetParameters, -1 and nil before one.
func (r *Reader) Parameters() (int, []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.protocol, slices.Clone(r.params)
}

// Write takes a command message and queues its response.
func (r *Reader) Write(ctx context.Context, p []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("ccidtest: closed")
	}
	if len(p) > r.o.MaxMessageLength {
		return errors.New("ccidtest: message longer than dwMaxCCIDMessageLength")
	}
	var m ccid.Message
	if err := m.UnmarshalBinary(p); err != nil {
		return err
	}
	r.commands = append(r.commands, m)
	for _, res := range r.handle(m) {
		res.Slot, res.Seq = m.Slot, m.Seq
		b, _ := res.MarshalBinary()
		r.out = append(r.out, b...)
	}
	return nil
}

// Read returns the next transfer of the pending responses.
func (r *Reader) Read(ctx context.Context, p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(r.out) == 0 {
		return 0, ErrTimeout
	}
	if r.left == 0 {
		// A transfer ends with its message, as USB ends one with a short
		// packet.
		r.left = len(r.out)
		if len(r.out) >= ccid.HeaderLength {
			r.left = min(r.left, ccid.HeaderLength+int(binary.LittleEndian.Uint32(r.out[1:])))
		}
	}
	n := r.left
	if r.o.ChunkSize > 0 {
		n = min(n, r.o.ChunkSize)
	}
	n = copy(p, r.out[:n])
	r.out = r.out[n:]
	r.left -= n
	return n, nil
}

// Close closes the simulated transport.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *Reader) status(cmd ccid.CommandStatus) byte {
	icc := ccid.ICCAbsent
	switch {
	case r.card != nil && r.powered:
		icc = ccid.ICCActive
	case r.card != nil:
		icc = ccid.ICCInactive
	}
	return byte(cmd)<<6 | byte(icc)
}

func (r *Reader) failed(t ccid.MessageType, code byte) ccid.Message {
	return ccid.Message{Type: t, Param: [3]byte{r.status(ccid.CommandFailed), code}}
}

func (r *Reader) ok(t ccid.MessageType, third byte, data []byte) ccid.Message {
	return ccid.Message{Type: t, Param: [3]byte{r.status(ccid.CommandOK), 0, third}, Data: data}
}

const (
	errICCMute       = 0xFE
	errCmdNotSupport = 0x00
)

var voltageOf = map[byte]byte{1: ccid.Voltage5V, 2: ccid.Voltage3V, 3: ccid.Voltage18V}

func (r *Reader) handle(m ccid.Message) []ccid.Message {
	switch m.Type {
	case ccid.PCtoRDRIccPowerOn:
		if r.card == nil {
			return []ccid.Message{r.failed(ccid.RDRtoPCDataBlock, errICCMute)}
		}
		if v := voltageOf[m.Param[0]]; v != 0 && r.o.CardVoltages != 0 && r.o.CardVoltages&v == 0 {
			return []ccid.Message{r.failed(ccid.RDRtoPCDataBlock, errICCMute)}
		}
		r.powered, r.protocol, r.pending, r.confused = true, -1, nil, false
		r.powerOns++
		r.t1 = t1Card{ifsd: 32}
		return []ccid.Message{r.ok(ccid.RDRtoPCDataBlock, 0, r.card.ATR())}
	case ccid.PCtoRDRIccPowerOff:
		r.powered = false
		return []ccid.Message{r.ok(ccid.RDRtoPCSlotStatus, 0, nil)}
	case ccid.PCtoRDRGetSlotStatus:
		return []ccid.Message{r.ok(ccid.RDRtoPCSlotStatus, 0, nil)}
	case ccid.PCtoRDRSetParameters:
		if !r.powered {
			return []ccid.Message{r.failed(ccid.RDRtoPCParameters, errICCMute)}
		}
		if r.o.RejectFiDi && len(m.Data) > 0 && m.Data[0] != 0x11 {
			return []ccid.Message{r.failed(ccid.RDRtoPCParameters, ccid.HeaderLength)}
		}
		r.protocol, r.params = int(m.Param[0]), slices.Clone(m.Data)
		return []ccid.Message{r.ok(ccid.RDRtoPCParameters, m.Param[0], m.Data)}
	case ccid.PCtoRDRGetParameters:
		p := max(r.protocol, 0)
		return []ccid.Message{r.ok(ccid.RDRtoPCParameters, byte(p), r.params)}
	case ccid.PCtoRDREscape:
		return []ccid.Message{r.ok(ccid.RDRtoPCEscape, 0, m.Data)}
	case ccid.PCtoRDRXfrBlock:
		if !r.powered || r.confused {
			return []ccid.Message{r.failed(ccid.RDRtoPCDataBlock, errICCMute)}
		}
		if r.o.Level == ccid.LevelTPDU && r.protocol == -1 && len(m.Data) > 0 && m.Data[0] == 0xFF {
			r.ppsReq = slices.Clone(m.Data)
			switch {
			case r.o.RejectPPS:
				r.confused = true
				return []ccid.Message{r.failed(ccid.RDRtoPCDataBlock, errICCMute)}
			case r.o.PPSDefault:
				pps0 := m.Data[1] & 0x0F
				return []ccid.Message{r.ok(ccid.RDRtoPCDataBlock, 0, []byte{0xFF, pps0, 0xFF ^ pps0})}
			}
			return []ccid.Message{r.ok(ccid.RDRtoPCDataBlock, 0, m.Data)}
		}
		var res []ccid.Message
		for range r.o.TimeExtensions {
			res = append(res, ccid.Message{Type: ccid.RDRtoPCDataBlock, Param: [3]byte{r.status(ccid.CommandTimeExtension), 1}})
		}
		level := uint16(m.Param[1]) | uint16(m.Param[2])<<8
		if r.o.Level == ccid.LevelTPDU && r.protocol == 1 {
			blk := r.tpduT1(m.Data)
			switch {
			case r.o.T1LoseResponses > 0:
				r.o.T1LoseResponses--
				return append(res, r.failed(ccid.RDRtoPCDataBlock, errICCMute))
			case r.o.T1CorruptResponses > 0:
				r.o.T1CorruptResponses--
				blk = slices.Clone(blk)
				blk[len(blk)-1] ^= 0xFF
			}
			return append(res, r.ok(ccid.RDRtoPCDataBlock, 0, blk))
		}
		if r.o.Level == ccid.LevelTPDU {
			return append(res, r.ok(ccid.RDRtoPCDataBlock, 0, r.tpduT0(m.Data)))
		}
		return append(res, r.xfrAPDU(m.Data, level))
	}
	return []ccid.Message{r.failed(ccid.RDRtoPCSlotStatus, errCmdNotSupport)}
}

// tpduT0 plays a T=0 card behind a TPDU level reader.
func (r *Reader) tpduT0(tpdu []byte) []byte {
	if len(tpdu) < 5 {
		return []byte{0x67, 0x00}
	}
	hdr, p3, data := tpdu[:4], tpdu[4], tpdu[5:]
	if hdr[1] == 0xC0 && len(data) == 0 { // GET RESPONSE
		if len(r.pending) == 0 {
			return []byte{0x6F, 0x00}
		}
		want := int(p3)
		if want == 0 {
			want = 256
		}
		if want > len(r.pending) {
			return []byte{0x6C, byte(len(r.pending))}
		}
		out := append(slices.Clone(r.pending[:want]), 0x90, 0x00)
		r.pending = r.pending[want:]
		if len(r.pending) > 0 {
			out[len(out)-2], out[len(out)-1] = 0x61, byte(min(len(r.pending), 256))
		}
		return out
	}
	r.pending = nil
	if len(data) == 0 { // case 1 or 2: P3 is Le
		resp := r.card.Transmit(slices.Clone(tpdu))
		body, sw := resp[:len(resp)-2], resp[len(resp)-2:]
		le := int(p3)
		if le == 0 {
			le = 256
		}
		if len(body) > 0 && le != len(body) {
			return []byte{0x6C, byte(len(body))}
		}
		return append(slices.Clone(body), sw...)
	}
	if int(p3) != len(data) {
		return []byte{0x67, 0x00}
	}
	// Case 3 or 4: the response data waits for GET RESPONSE.
	resp := r.card.Transmit(append(slices.Clone(tpdu), 0x00))
	body, sw := resp[:len(resp)-2], resp[len(resp)-2:]
	if len(body) == 0 {
		return slices.Clone(sw)
	}
	if sw[0] != 0x90 || sw[1] != 0x00 {
		return slices.Clone(sw)
	}
	r.pending = slices.Clone(body)
	return []byte{0x61, byte(min(len(body), 256))}
}

// xfrAPDU plays an APDU level reader, with the chaining of the extended
// APDU level.
func (r *Reader) xfrAPDU(data []byte, level uint16) ccid.Message {
	const (
		beginEnd, beginMore, continueEnd, continueMore, emptyContinue = 0x00, 0x01, 0x02, 0x03, 0x10
	)
	room := r.o.MaxMessageLength - ccid.HeaderLength
	switch level {
	case emptyContinue: // the next block of the response
		if len(r.respChain) == 0 {
			return r.failed(ccid.RDRtoPCDataBlock, ccid.HeaderLength+1)
		}
		n := min(room, len(r.respChain))
		chunk := r.respChain[:n]
		r.respChain = r.respChain[n:]
		cp := byte(continueEnd)
		if len(r.respChain) > 0 {
			cp = continueMore
		}
		return r.ok(ccid.RDRtoPCDataBlock, cp, chunk)
	case beginMore:
		r.cmdChain = slices.Clone(data)
		return r.ok(ccid.RDRtoPCDataBlock, emptyContinue, nil)
	case continueMore:
		r.cmdChain = append(r.cmdChain, data...)
		return r.ok(ccid.RDRtoPCDataBlock, emptyContinue, nil)
	case continueEnd:
		data = append(r.cmdChain, data...)
		r.cmdChain = nil
	case beginEnd:
	default:
		return r.failed(ccid.RDRtoPCDataBlock, ccid.HeaderLength+1)
	}
	resp := r.card.Transmit(slices.Clone(data))
	if len(resp) <= room {
		return r.ok(ccid.RDRtoPCDataBlock, beginEnd, resp)
	}
	r.respChain = slices.Clone(resp[room:])
	return r.ok(ccid.RDRtoPCDataBlock, beginMore, resp[:room])
}

func t1Encode(pcb byte, inf []byte) []byte {
	b := append([]byte{0x00, pcb, byte(len(inf))}, inf...)
	var x byte
	for _, c := range b {
		x ^= c
	}
	return append(b, x)
}

// tpduT1 plays a T=1 card behind a TPDU level reader: it answers one
// block from the host with one block.
func (r *Reader) tpduT1(b []byte) []byte {
	t := &r.t1
	send := func(blk []byte) []byte {
		t.last = blk
		return blk
	}
	rblock := func(errBits byte) []byte { return send(t1Encode(0x80|t.nr<<4|errBits, nil)) }
	var x byte
	for _, c := range b {
		x ^= c
	}
	if len(b) < 4 || int(b[2]) != len(b)-4 || x != 0 {
		return rblock(0x01)
	}
	pcb, inf := b[1], b[3:len(b)-1]
	ifsc := t.ifsc
	if ifsc == 0 {
		ifsc = 32
		if a, err := ccid.ParseATR(r.card.ATR()); err == nil {
			ifsc = int(a.IFSC())
		}
	}
	switch {
	case pcb&0xC0 == 0xC0: // S-block
		switch pcb {
		case 0xC1: // IFS request
			if len(inf) != 1 {
				return rblock(0x02)
			}
			t.ifsd, t.gotIFSD = int(inf[0]), inf[0]
			return send(t1Encode(0xE1, inf))
		case 0xC0: // RESYNCH request
			if r.o.T1IgnoreResynch {
				return rblock(0x02)
			}
			*t = t1Card{ifsd: 32, resyncs: t.resyncs + 1, gotIFSD: t.gotIFSD}
			return send(t1Encode(0xE0, nil))
		case 0xE3, 0xE1: // WTX or IFS response: on with the response
			if t.pendingRsp {
				return r.t1Answer()
			}
		}
		return rblock(0x02)
	case pcb&0xC0 == 0x80: // R-block
		nr := pcb >> 4 & 1
		if len(t.resp) > 0 && nr == t.ns && !t.pendingRsp {
			return r.t1Next()
		}
		if t.last == nil {
			return rblock(0x02)
		}
		return send(t.last)
	}
	// I-block
	ns, more := pcb>>6&1, pcb&0x20 != 0
	if ns != t.nr {
		// A block already received, sent again: what answered it is lost.
		if t.last != nil {
			return send(t.last)
		}
		return rblock(0x02)
	}
	if len(inf) > ifsc {
		return rblock(0x02)
	}
	t.nr ^= 1
	t.cmd = append(t.cmd, inf...)
	if more {
		return rblock(0)
	}
	t.resp = r.card.Transmit(t.cmd)
	t.cmd = nil
	t.wtxLeft = r.o.T1WTX
	t.pendingRsp = true
	return r.t1Answer()
}

// t1Answer sends what comes before the response, then its first block.
func (r *Reader) t1Answer() []byte {
	t := &r.t1
	if r.o.T1IFSRequest != 0 && !t.ifsDone {
		t.ifsDone, t.ifsc = true, int(r.o.T1IFSRequest)
		t.last = t1Encode(0xC1, []byte{r.o.T1IFSRequest})
		return t.last
	}
	if t.wtxLeft > 0 {
		t.wtxLeft--
		t.last = t1Encode(0xC3, []byte{1})
		return t.last
	}
	t.pendingRsp = false
	return r.t1Next()
}

// t1Next sends the next I-block of the response.
func (r *Reader) t1Next() []byte {
	t := &r.t1
	n := min(t.ifsd, len(t.resp))
	pcb := t.ns << 6
	if n < len(t.resp) {
		pcb |= 0x20
	}
	t.last = t1Encode(pcb, t.resp[:n])
	t.resp = t.resp[n:]
	t.ns ^= 1
	return t.last
}
