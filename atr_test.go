package ccid_test

import (
	"errors"
	"slices"
	"testing"

	ccid "github.com/creamlike1024/go-ccid"
	"github.com/creamlike1024/go-ccid/ccidtest"
)

func TestParseATR(t *testing.T) {
	a, err := ccid.ParseATR(ccidtest.ATRT0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Protocols(), []ccid.Protocol{ccid.T0}) || a.FiDi() != 0x11 || a.WaitingIntegerT0() != 10 || a.HasTCK {
		t.Errorf("T=0 ATR: %+v", a)
	}

	a, err = ccid.ParseATR(ccidtest.ATRT0T1)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Protocols(), []ccid.Protocol{ccid.T0, ccid.T1}) {
		t.Errorf("protocols %v", a.Protocols())
	}
	if a.FiDi() != 0x96 || a.IFSC() != 254 || a.WaitingIntegersT1() != 0x45 || a.CRC() || !a.HasTCK {
		t.Errorf("T=0/T=1 ATR: FiDi %#x IFSC %d BWI/CWI %#x CRC %v", a.FiDi(), a.IFSC(), a.WaitingIntegersT1(), a.CRC())
	}
	if _, specific := a.SpecificMode(); specific {
		t.Error("negotiable ATR in specific mode")
	}

	// Specific mode, T=1: TA1 0x18, TD1 (TA2 follows, T=1), TA2 0x81
	// (specific, T=1); historical "AB".
	specific := []byte{0x3B, 0x92, 0x18, 0x11, 0x81, 'A', 'B'}
	specific = append(specific, tck(specific))
	a, err = ccid.ParseATR(specific)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := a.SpecificMode(); !ok || p != ccid.T1 || string(a.Historical) != "AB" {
		t.Errorf("specific mode %v %v, historical %q", p, ok, a.Historical)
	}

	inverse := []byte{0x3F, 0x00}
	if a, err := ccid.ParseATR(inverse); err != nil || !a.Inverse {
		t.Errorf("inverse convention: %v %+v", err, a)
	}

	badTCK := slices.Clone(ccidtest.ATRT0T1)
	badTCK[len(badTCK)-1] ^= 1
	for name, b := range map[string][]byte{
		"bad TS":     {0x3A, 0x00},
		"too short":  {0x3B},
		"cut":        {0x3B, 0x90, 0x96},
		"historical": {0x3B, 0x02, 'A'},
		"extra":      {0x3B, 0x00, 0x00},
		"bad TCK":    badTCK,
		"no TCK":     ccidtest.ATRT0T1[:len(ccidtest.ATRT0T1)-1],
	} {
		if _, err := ccid.ParseATR(b); !errors.Is(err, ccid.ErrBadATR) {
			t.Errorf("%s: %v, want ErrBadATR", name, err)
		}
	}
}

func tck(atr []byte) byte {
	var x byte
	for _, b := range atr[1:] {
		x ^= b
	}
	return x
}
