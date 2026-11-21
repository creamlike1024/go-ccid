package ccid

import (
	"errors"
	"fmt"
	"slices"
)

// Protocol is a transmission protocol of ISO/IEC 7816-3.
type Protocol byte

const (
	T0 Protocol = 0
	T1 Protocol = 1
)

func (p Protocol) String() string { return fmt.Sprintf("T=%d", byte(p)) }

// InterfaceBytes are the interface bytes TAi, TBi, TCi and TDi of one
// group of an ATR; Has tells which are present.
type InterfaceBytes struct {
	TA, TB, TC, TD byte
	Has            struct{ TA, TB, TC, TD bool }
}

// ATR is a parsed Answer-To-Reset.
type ATR struct {
	Raw []byte
	// Inverse reports the inverse convention (TS = 0x3F).
	Inverse bool
	// Groups are the interface byte groups, Groups[0] being TA1..TD1.
	Groups     []InterfaceBytes
	Historical []byte
	// TCK is the check byte, present when a protocol other than T=0 is
	// indicated.
	TCK    byte
	HasTCK bool
}

// ErrBadATR is returned by ParseATR for a malformed ATR.
var ErrBadATR = errors.New("ccid: malformed ATR")

// ParseATR parses an ATR as the reader returns it, TS first.
func ParseATR(b []byte) (ATR, error) {
	a := ATR{Raw: slices.Clone(b)}
	if len(b) < 2 {
		return a, fmt.Errorf("%w: %d bytes", ErrBadATR, len(b))
	}
	switch b[0] {
	case 0x3B:
	case 0x3F:
		a.Inverse = true
	default:
		return a, fmt.Errorf("%w: TS %#02x", ErrBadATR, b[0])
	}
	i := 1
	y := b[1] >> 4
	k := int(b[1] & 0x0F)
	i++
	needTCK := false
	for {
		var g InterfaceBytes
		read := func(present bool, dst *byte, has *bool) error {
			if !present {
				return nil
			}
			if i >= len(b) {
				return fmt.Errorf("%w: interface bytes past the end", ErrBadATR)
			}
			*dst, *has = b[i], true
			i++
			return nil
		}
		if err := read(y&1 != 0, &g.TA, &g.Has.TA); err != nil {
			return a, err
		}
		if err := read(y&2 != 0, &g.TB, &g.Has.TB); err != nil {
			return a, err
		}
		if err := read(y&4 != 0, &g.TC, &g.Has.TC); err != nil {
			return a, err
		}
		if err := read(y&8 != 0, &g.TD, &g.Has.TD); err != nil {
			return a, err
		}
		a.Groups = append(a.Groups, g)
		if !g.Has.TD {
			break
		}
		if g.TD&0x0F != 0 {
			needTCK = true
		}
		y = g.TD >> 4
	}
	if i+k > len(b) {
		return a, fmt.Errorf("%w: %d historical bytes past the end", ErrBadATR, k)
	}
	a.Historical = slices.Clone(b[i : i+k])
	i += k
	if needTCK {
		if i >= len(b) {
			return a, fmt.Errorf("%w: TCK missing", ErrBadATR)
		}
		a.TCK, a.HasTCK = b[i], true
		var x byte
		for _, c := range b[1 : i+1] {
			x ^= c
		}
		if x != 0 {
			return a, fmt.Errorf("%w: TCK does not check", ErrBadATR)
		}
		i++
	}
	if i != len(b) {
		return a, fmt.Errorf("%w: %d bytes after its end", ErrBadATR, len(b)-i)
	}
	return a, nil
}

func (a ATR) group(i int) (InterfaceBytes, bool) {
	if i < 1 || i > len(a.Groups) {
		return InterfaceBytes{}, false
	}
	return a.Groups[i-1], true
}

// Protocols returns the protocols the card offers, in the order of the
// ATR: T=0 alone when it indicates none.
func (a ATR) Protocols() []Protocol {
	var ps []Protocol
	for _, g := range a.Groups {
		if g.Has.TD {
			p := Protocol(g.TD & 0x0F)
			if p != 15 && !slices.Contains(ps, p) {
				ps = append(ps, p)
			}
		}
	}
	if len(ps) == 0 {
		ps = []Protocol{T0}
	}
	return ps
}

// FiDi returns TA1, the clock rate conversion and baud rate adjustment
// factors, or 0x11 (Fd, Dd) when absent.
func (a ATR) FiDi() byte {
	if g, ok := a.group(1); ok && g.Has.TA {
		return g.TA
	}
	return 0x11
}

// SpecificMode reports whether the card is in specific mode (TA2
// present): it uses the parameters of the ATR, and takes no PPS.
func (a ATR) SpecificMode() (Protocol, bool) {
	if g, ok := a.group(2); ok && g.Has.TA {
		return Protocol(g.TA & 0x0F), true
	}
	return 0, false
}

// ExtraGuardTime returns TC1 (N), 0 when absent.
func (a ATR) ExtraGuardTime() byte {
	if g, ok := a.group(1); ok && g.Has.TC {
		return g.TC
	}
	return 0
}

// WaitingIntegerT0 returns TC2, the T=0 waiting time integer, 10 when
// absent.
func (a ATR) WaitingIntegerT0() byte {
	if g, ok := a.group(2); ok && g.Has.TC && g.TC != 0 {
		return g.TC
	}
	return 10
}

// t1Group returns the first group of T=1 specific bytes: group i (i >= 3)
// whose TD(i-1) indicates T=1.
func (a ATR) t1Group() (InterfaceBytes, bool) {
	for i := 3; i <= len(a.Groups); i++ {
		if prev := a.Groups[i-2]; prev.Has.TD && prev.TD&0x0F == byte(T1) {
			return a.Groups[i-1], true
		}
	}
	return InterfaceBytes{}, false
}

// IFSC returns the card's T=1 information field size, 32 when absent.
func (a ATR) IFSC() byte {
	if g, ok := a.t1Group(); ok && g.Has.TA && g.TA != 0 && g.TA != 0xFF {
		return g.TA
	}
	return 32
}

// WaitingIntegersT1 returns the T=1 BWI (high nibble) and CWI (low
// nibble), 0x4D when absent.
func (a ATR) WaitingIntegersT1() byte {
	if g, ok := a.t1Group(); ok && g.Has.TB {
		return g.TB
	}
	return 0x4D
}

// CRC reports whether T=1 blocks end with a CRC rather than an LRC.
func (a ATR) CRC() bool {
	g, ok := a.t1Group()
	return ok && g.Has.TC && g.TC&1 != 0
}
