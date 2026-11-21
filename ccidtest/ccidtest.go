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
		MaxDataRate:      344086,
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
		r.powered, r.protocol, r.pending = true, -1, nil
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
		if !r.powered {
			return []ccid.Message{r.failed(ccid.RDRtoPCDataBlock, errICCMute)}
		}
		var res []ccid.Message
		for range r.o.TimeExtensions {
			res = append(res, ccid.Message{Type: ccid.RDRtoPCDataBlock, Param: [3]byte{r.status(ccid.CommandTimeExtension), 1}})
		}
		level := uint16(m.Param[1]) | uint16(m.Param[2])<<8
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
