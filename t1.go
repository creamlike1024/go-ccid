package ccid

import (
	"context"
	"errors"
	"fmt"
)

// T=1 at TPDU level: the host runs the block protocol of ISO/IEC 7816-3
// (also ETSI TS 102 221, 7.2.3), and the reader carries each block, its
// epilogue included.

// T=1 block types and S-block functions.
const (
	sResynch = 0x00
	sIFS     = 0x01
	sAbort   = 0x02
	sWTX     = 0x03

	sResponse = 0x20
)

// R-block error bits.
const (
	rOK       = 0x00
	rEDCError = 0x01
	rOther    = 0x02
)

// t1Retries is how many times an invalid or missing block is asked for
// again, after the first, before the protocol is resynchronized.
const t1Retries = 2

// maxT1Blocks bounds the blocks of one APDU's exchange: an extended
// APDU and its response, of 65 KB each, in blocks of 254 bytes, with
// WTX and retransmissions to spare.
const maxT1Blocks = 1 << 11

// maxIFSD is the largest information field T=1 allows.
const maxIFSD = 254

// ErrResynchronized is returned when the T=1 protocol had to be
// resynchronized: the APDU may or may not have reached the card.
var ErrResynchronized = errors.New("ccid: T=1 resynchronized after errors")

type t1State struct {
	ns, nr byte // N(S) of the host's next I-block, N(S) expected of the card
	ifsc   int
	bwi    byte // the WTX multiplier of the next block, 0 for none
}

// t1Block is a received block.
type t1Block struct {
	pcb byte
	inf []byte
}

func (b t1Block) isI() bool       { return b.pcb&0x80 == 0 }
func (b t1Block) isR() bool       { return b.pcb&0xC0 == 0x80 }
func (b t1Block) isS() bool       { return b.pcb&0xC0 == 0xC0 }
func (b t1Block) ns() byte        { return b.pcb >> 6 & 1 }
func (b t1Block) more() bool      { return b.pcb&0x20 != 0 }
func (b t1Block) nr() byte        { return b.pcb >> 4 & 1 }
func (b t1Block) sType() byte     { return b.pcb & 0x1F }
func (b t1Block) sResponse() bool { return b.pcb&sResponse != 0 }

func lrc(b []byte) byte {
	var x byte
	for _, c := range b {
		x ^= c
	}
	return x
}

// encodeT1 makes a block of NAD 0, its PCB and INF, and its LRC.
func encodeT1(pcb byte, inf []byte) []byte {
	b := make([]byte, 0, 4+len(inf))
	b = append(b, 0x00, pcb, byte(len(inf)))
	b = append(b, inf...)
	return append(b, lrc(b))
}

// decodeT1 checks and parses a received block; the error is the R-block
// error bits to answer an invalid block with.
func decodeT1(b []byte) (t1Block, byte, bool) {
	if len(b) < 4 || int(b[2]) != len(b)-4 || b[2] == 0xFF {
		return t1Block{}, rOther, false
	}
	if lrc(b) != 0 {
		return t1Block{}, rEDCError, false
	}
	blk := t1Block{pcb: b[1], inf: b[3 : len(b)-1]}
	switch {
	case blk.isI(), blk.isR():
	case blk.isS() && blk.sType() <= sWTX:
	default:
		return t1Block{}, rOther, false
	}
	return blk, 0, true
}

func iBlockPCB(ns byte, more bool) byte {
	pcb := ns << 6
	if more {
		pcb |= 0x20
	}
	return pcb
}

func rBlockPCB(nr, errBits byte) byte { return 0x80 | nr<<4 | errBits }

// startT1 begins the protocol after SetParameters: the IFSC of the ATR,
// and an IFSD as large as the reader takes, unless the reader sets it.
func (c *Card) startT1(ctx context.Context) error {
	c.t1 = t1State{ifsc: int(c.atr.IFSC())}
	if c.r.desc.Features&FeatureAutoIFSD != 0 {
		return nil
	}
	ifsd := min(maxIFSD, int(c.r.desc.MaxIFSD), c.r.maxLen-HeaderLength-4)
	if ifsd <= 32 { // the default
		return nil
	}
	req := encodeT1(0xC0|sIFS, []byte{byte(ifsd)})
	// A card that does not take it keeps sending blocks of 32 bytes or
	// less, which works as well: only failures of the reader count.
	_, err := c.t1Exchange(ctx, req, true)
	if err != nil && !errors.Is(err, ErrProtocol) && !errors.Is(err, ErrResynchronized) {
		return err
	}
	return nil
}

// linkError reports a failed XfrBlock that means no valid block came:
// the card did not answer within the block waiting time, or the
// characters were garbled.
func linkError(err error) bool {
	var ce *CommandError
	if !errors.As(err, &ce) || ce.ICC == ICCAbsent {
		return false
	}
	switch ce.Code {
	case errICCMute, errXfrParity, errXfrOverrun:
		return true
	}
	return false
}

// t1Exchange sends a block and returns the card's answer, a valid block
// other than S(WTX request) and S(IFS request), which it answers itself.
// An invalid or missing answer is asked for again, as ISO/IEC 7816-3
// says (the S-request resent, or an R-block); after t1Retries the
// protocol is resynchronized and ErrResynchronized returned. sRequest
// tells that blk is an S(request), which is resent rather than answered
// with an R-block.
func (c *Card) t1Exchange(ctx context.Context, blk []byte, sRequest bool) (t1Block, error) {
	send := blk
	for bad := 0; ; {
		bwi := c.t1.bwi
		c.t1.bwi = 0
		res, err := c.xfrBWI(ctx, bwi, send, 0)
		var b t1Block
		ok := false
		errBits := byte(rOK)
		switch {
		case err == nil:
			b, errBits, ok = decodeT1(res.Data)
		case linkError(err):
		default:
			return t1Block{}, err
		}
		if !ok {
			if bad++; bad > t1Retries {
				return t1Block{}, c.resynchronize(ctx)
			}
			switch {
			case sRequest:
				send = blk
			case send[1]&0xC0 == 0x80:
				// An R-block went, and is resent.
			default:
				send = encodeT1(rBlockPCB(c.t1.nr, errBits), nil)
			}
			continue
		}
		if !b.isS() || b.sResponse() {
			return b, nil
		}
		switch b.sType() {
		case sWTX:
			if len(b.inf) != 1 {
				return t1Block{}, fmt.Errorf("%w: S(WTX request) with %d bytes", ErrProtocol, len(b.inf))
			}
			c.t1.bwi = b.inf[0]
			send = encodeT1(0xE0|sWTX, b.inf)
		case sIFS:
			if len(b.inf) != 1 || b.inf[0] == 0 || b.inf[0] == 0xFF {
				return t1Block{}, fmt.Errorf("%w: S(IFS request) of % x", ErrProtocol, b.inf)
			}
			c.t1.ifsc = int(b.inf[0])
			send = encodeT1(0xE0|sIFS, b.inf)
		case sAbort:
			c.xfrBWI(ctx, 0, encodeT1(0xE0|sAbort, nil), 0)
			return t1Block{}, fmt.Errorf("%w: the card aborted the T=1 chain", ErrAborted)
		default: // S(RESYNCH request) is the host's to send
			return t1Block{}, fmt.Errorf("%w: S-block %#02x from the card", ErrProtocol, b.pcb)
		}
	}
}

// resynchronize sends S(RESYNCH request), which resets the sequence
// numbers, and returns ErrResynchronized, or an error when the card does
// not answer it.
func (c *Card) resynchronize(ctx context.Context) error {
	req := encodeT1(0xC0|sResynch, nil)
	for range t1Retries + 1 {
		res, err := c.xfrBWI(ctx, 0, req, 0)
		if err != nil && !linkError(err) {
			return err
		}
		if err != nil {
			continue
		}
		if b, _, ok := decodeT1(res.Data); ok && b.isS() && b.sResponse() && b.sType() == sResynch {
			c.t1 = t1State{ifsc: int(c.atr.IFSC())}
			return ErrResynchronized
		}
	}
	return fmt.Errorf("%w: the card does not answer S(RESYNCH request); reconnect", ErrProtocol)
}

// transmitT1 sends an APDU in I-blocks of at most IFSC bytes, chained,
// and gathers the response from the card's I-blocks.
func (c *Card) transmitT1(ctx context.Context, apdu []byte) ([]byte, error) {
	blocks := 0
	exchange := func(blk []byte) (t1Block, error) {
		if blocks++; blocks > maxT1Blocks {
			return t1Block{}, fmt.Errorf("%w: T=1 exchange does not end", ErrProtocol)
		}
		return c.t1Exchange(ctx, blk, false)
	}
	var resp t1Block
	for off := 0; ; {
		n := min(c.t1.ifsc, len(apdu)-off)
		more := off+n < len(apdu)
		iblk := encodeT1(iBlockPCB(c.t1.ns, more), apdu[off:off+n])
		b, err := exchange(iblk)
		// R(N(R) = our N(S)) asks for the I-block again.
		for tries := 0; err == nil && b.isR() && b.nr() == c.t1.ns; tries++ {
			if tries == t1Retries {
				return nil, c.resynchronize(ctx)
			}
			b, err = exchange(iblk)
		}
		if err != nil {
			return nil, err
		}
		c.t1.ns ^= 1
		off += n
		if more {
			if !b.isR() {
				return nil, fmt.Errorf("%w: chained I-block answered with PCB %#02x", ErrProtocol, b.pcb)
			}
			continue
		}
		resp = b
		break
	}
	var out []byte
	for tries := 0; ; {
		switch {
		case !resp.isI() || resp.ns() != c.t1.nr:
			// Not the I-block expected: ask for it.
			if tries++; tries > t1Retries {
				return nil, c.resynchronize(ctx)
			}
		default:
			tries = 0
			out = append(out, resp.inf...)
			c.t1.nr ^= 1
			if !resp.more() {
				if len(out) < 2 {
					return nil, fmt.Errorf("%w: response of %d bytes without status words", ErrProtocol, len(out))
				}
				return out, nil
			}
		}
		var err error
		if resp, err = exchange(encodeT1(rBlockPCB(c.t1.nr, rOK), nil)); err != nil {
			return nil, err
		}
	}
}
