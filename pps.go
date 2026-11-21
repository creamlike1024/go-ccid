package ccid

import (
	"context"
	"errors"
	"slices"
	"sort"
)

// Clock rate conversion factors Fi and baud rate adjustment factors Di of
// TA1 (ISO/IEC 7816-3, tables 7 and 8); 0 is RFU.
var (
	fiTable = [16]int{372, 372, 558, 744, 1116, 1488, 1860, 0, 0, 512, 768, 1024, 1536, 2048, 0, 0}
	diTable = [16]int{0, 1, 2, 4, 8, 16, 32, 64, 12, 20, 0, 0, 0, 0, 0, 0}
)

// choosePPS returns the bmFindexDindex to propose in a PPS: the card's
// TA1, or the same Fi with a smaller Di (a power of two) when the reader
// is not as fast, the highest rate both take (ETSI TS 102 221, 6.4). It reports false
// when there is nothing faster than the default to propose.
func (r *Reader) choosePPS(atr ATR) (byte, bool) {
	ta1 := atr.FiDi()
	fi, di := int(ta1>>4), int(ta1&0x0F)
	F, D := fiTable[fi], diTable[di]
	clock := int(r.desc.DefaultClock) * 1000
	if ta1 == 0x11 || F == 0 || D == 0 || clock == 0 {
		return 0, false
	}
	// The card's own Di, then smaller powers of two: cards take those
	// (UICCs at least 8 and 16), not always 12 or 20.
	cands := []int{di}
	for i := 1; i <= 7; i++ {
		if diTable[i] < D {
			cands = append(cands, i)
		}
	}
	sort.Slice(cands, func(a, b int) bool { return diTable[cands[a]] > diTable[cands[b]] })
	for _, i := range cands {
		rate := clock * diTable[i] / F
		if r.desc.MaxDataRate != 0 && rate > int(r.desc.MaxDataRate) {
			continue
		}
		if F == 372 && diTable[i] == 1 {
			return 0, false
		}
		return byte(fi<<4 | i), true
	}
	return 0, false
}

// errPPSFailed is a PPS the card did not complete: it is to be reset.
var errPPSFailed = errors.New("ccid: PPS failed")

// pps runs a PPS exchange through a TPDU level reader (CCID rev 1.1,
// 3.2.1) and returns the bmFindexDindex agreed: fidi, or 0x11 when the
// card answers without PPS1.
func (r *Reader) pps(ctx context.Context, slot byte, p Protocol, fidi byte) (byte, error) {
	req := []byte{0xFF, 0x10 | byte(p), fidi}
	req = append(req, lrc(req))
	res, err := r.exchange(ctx, Message{Type: PCtoRDRXfrBlock, Slot: slot, Data: req})
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) && ce.ICC != ICCAbsent {
			return 0, errPPSFailed
		}
		return 0, err
	}
	d := res.Data
	switch {
	case res.Type != RDRtoPCDataBlock:
		return 0, errPPSFailed
	case slices.Equal(d, req):
		return fidi, nil
	case len(d) == 3 && d[0] == 0xFF && d[1] == byte(p) && lrc(d) == 0:
		// PPS1 left out: the default rate.
		return 0x11, nil
	}
	return 0, errPPSFailed
}
