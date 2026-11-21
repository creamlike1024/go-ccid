package ccid

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// Direction of a traced message.
type Direction int

const (
	ToReader   Direction = iota // a command
	FromReader                  // a response
)

// Options configure a Reader.
type Options struct {
	// Trace, when set, receives every message sent and received, whole,
	// as it goes over the transport. It is called in the exchange's
	// goroutine and must not keep the slice.
	Trace func(d Direction, msg []byte)
}

// maxStale is how many responses to earlier commands an exchange drops
// before it gives up.
const maxStale = 16

// minMessageLength is the smallest dwMaxCCIDMessageLength the
// specification allows: a header and a short APDU.
const minMessageLength = HeaderLength + 261

// Reader is a CCID reader on a Transport.
type Reader struct {
	t      Transport
	desc   Descriptor
	maxLen int
	trace  func(Direction, []byte)

	mu  sync.Mutex
	seq byte
	buf []byte
}

// Open returns a Reader for the reader that t reaches and whose class
// descriptor is d.
func Open(t Transport, d Descriptor, o *Options) (*Reader, error) {
	maxLen := int(d.MaxMessageLength)
	if maxLen < minMessageLength {
		// Some readers report less than the specification allows; a
		// short APDU still fits the messages they actually take.
		maxLen = minMessageLength
	}
	r := &Reader{t: t, desc: d, maxLen: maxLen, buf: make([]byte, maxLen)}
	if o != nil {
		r.trace = o.Trace
	}
	return r, nil
}

// Descriptor returns the reader's class descriptor.
func (r *Reader) Descriptor() Descriptor { return r.desc }

// Close closes the transport.
func (r *Reader) Close() error { return r.t.Close() }

// Exchange sends a command message and returns its response, after the
// time extensions the reader asks for. The message's Seq is set by the
// Reader. A response that reports the command as failed is returned with
// a *CommandError.
func (r *Reader) Exchange(ctx context.Context, m Message) (Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exchange(ctx, m)
}

func (r *Reader) exchange(ctx context.Context, m Message) (Message, error) {
	if HeaderLength+len(m.Data) > r.maxLen {
		return Message{}, fmt.Errorf("ccid: %v of %d bytes exceeds the reader's %d", m.Type, HeaderLength+len(m.Data), r.maxLen)
	}
	m.Seq = r.seq
	r.seq++
	b, _ := m.MarshalBinary()
	if r.trace != nil {
		r.trace(ToReader, b)
	}
	if err := r.t.Write(ctx, b); err != nil {
		return Message{}, fmt.Errorf("ccid: sending %v: %w", m.Type, err)
	}
	for stale := 0; ; {
		res, err := r.read(ctx)
		if err != nil {
			return Message{}, fmt.Errorf("ccid: waiting for the response to %v: %w", m.Type, err)
		}
		if res.Seq != m.Seq || res.Slot != m.Slot {
			// The late response of an earlier command, as one whose
			// context was cancelled.
			if stale++; stale > maxStale {
				return Message{}, fmt.Errorf("%w: no response to %v with sequence %d", ErrProtocol, m.Type, m.Seq)
			}
			continue
		}
		if res.CommandStatus() == CommandTimeExtension {
			continue
		}
		if res.CommandStatus() == CommandFailed {
			return res, &CommandError{Command: m.Type, ICC: res.ICCStatus(), Code: res.ErrorCode()}
		}
		return res, nil
	}
}

// maxEmptyReads is how many zero-length transfers in a row a response may
// hold (a zero-length packet ends a transfer of a multiple of the packet
// size).
const maxEmptyReads = 4

// read reads one response message, in as many transfers as it takes.
func (r *Reader) read(ctx context.Context) (Message, error) {
	have, empty := 0, 0
	for {
		if have == len(r.buf) {
			return Message{}, fmt.Errorf("%w: response longer than %d bytes", ErrProtocol, len(r.buf))
		}
		n, err := r.t.Read(ctx, r.buf[have:])
		if err != nil {
			return Message{}, err
		}
		if n == 0 {
			if empty++; empty > maxEmptyReads {
				return Message{}, fmt.Errorf("%w: empty transfers instead of a response", ErrProtocol)
			}
			continue
		}
		empty = 0
		have += n
		if have < HeaderLength {
			continue
		}
		want := HeaderLength + int(binary.LittleEndian.Uint32(r.buf[1:]))
		if want > len(r.buf) {
			return Message{}, fmt.Errorf("%w: response of %d bytes exceeds %d", ErrProtocol, want, len(r.buf))
		}
		if have < want {
			continue
		}
		// Bytes past the message, which a reader should not send, are
		// dropped with it.
		if r.trace != nil {
			r.trace(FromReader, r.buf[:want])
		}
		var m Message
		if err := m.UnmarshalBinary(r.buf[:want]); err != nil {
			return Message{}, err
		}
		return m, nil
	}
}

// SlotStatus returns the state of the card in a slot.
func (r *Reader) SlotStatus(ctx context.Context, slot byte) (ICCStatus, error) {
	res, err := r.Exchange(ctx, Message{Type: PCtoRDRGetSlotStatus, Slot: slot})
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) {
			return ce.ICC, nil
		}
		return 0, err
	}
	if res.Type != RDRtoPCSlotStatus {
		return 0, fmt.Errorf("%w: %v answered with %v", ErrProtocol, PCtoRDRGetSlotStatus, res.Type)
	}
	return res.ICCStatus(), nil
}

// Escape sends a vendor command to the reader and returns its answer.
func (r *Reader) Escape(ctx context.Context, slot byte, data []byte) ([]byte, error) {
	res, err := r.Exchange(ctx, Message{Type: PCtoRDREscape, Slot: slot, Data: data})
	if err != nil {
		return nil, err
	}
	if res.Type != RDRtoPCEscape {
		return nil, fmt.Errorf("%w: %v answered with %v", ErrProtocol, PCtoRDREscape, res.Type)
	}
	return res.Data, nil
}
