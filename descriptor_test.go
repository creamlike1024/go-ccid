package ccid_test

import (
	"errors"
	"reflect"
	"testing"

	ccid "github.com/creamlike1024/go-ccid"
	"github.com/creamlike1024/go-ccid/ccidtest"
)

func TestDescriptorRoundTrip(t *testing.T) {
	d := ccidtest.NewReader(nil, ccidtest.Options{Level: ccid.LevelShortAPDU, Features: ccid.FeatureAutoVoltage}).Descriptor()
	b, err := d.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != ccid.DescriptorLength || b[0] != 54 || b[1] != 0x21 {
		t.Fatalf("descriptor header % x", b[:2])
	}
	got, err := ccid.ParseDescriptor(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, d) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, d)
	}
	if got.ExchangeLevel() != ccid.LevelShortAPDU {
		t.Errorf("exchange level %v", got.ExchangeLevel())
	}
	if !got.SupportsProtocol(ccid.T0) || !got.SupportsProtocol(ccid.T1) {
		t.Errorf("protocols %#x", got.Protocols)
	}
	if _, err := ccid.ParseDescriptor(b[:53]); err == nil {
		t.Error("short descriptor accepted")
	}
	b[1] = 0x24
	if _, err := ccid.ParseDescriptor(b); err == nil {
		t.Error("wrong descriptor type accepted")
	}
}

// rawDescriptors builds a device's descriptors: a device descriptor, a
// configuration, and the interface given.
func rawDescriptors(t *testing.T, class byte, cdAfterEndpoints bool) []byte {
	t.Helper()
	cd, _ := ccidtest.NewReader(nil, ccidtest.Options{}).Descriptor().MarshalBinary()
	device := []byte{18, 0x01, 0x00, 0x02, 0, 0, 0, 64, 0x2F, 0x07, 0xCC, 0x90, 0x00, 0x01, 1, 2, 0, 1}
	config := []byte{9, 0x02, 0, 0, 1, 1, 0, 0x80, 50}
	iface := []byte{9, 0x04, 0, 0, 3, class, 0, 0, 0}
	bulkOut := []byte{7, 0x05, 0x02, 0x02, 64, 0, 0}
	bulkIn := []byte{7, 0x05, 0x82, 0x02, 64, 0, 0}
	intr := []byte{7, 0x05, 0x83, 0x03, 8, 0, 16}
	raw := append(device, config...)
	raw = append(raw, iface...)
	if !cdAfterEndpoints {
		raw = append(raw, cd...)
	}
	raw = append(raw, bulkOut...)
	raw = append(raw, bulkIn...)
	raw = append(raw, intr...)
	if cdAfterEndpoints {
		raw = append(raw, cd...)
	}
	return raw
}

func TestFindInterfaces(t *testing.T) {
	for _, tc := range []struct {
		name  string
		class byte
		after bool
	}{
		{"CCID", ccid.InterfaceClass, false},
		{"descriptor after the endpoints", ccid.InterfaceClass, true},
		{"vendor class", 0xFF, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ifs, err := ccid.FindInterfaces(rawDescriptors(t, tc.class, tc.after))
			if err != nil {
				t.Fatal(err)
			}
			if len(ifs) != 1 {
				t.Fatalf("%d interfaces", len(ifs))
			}
			i := ifs[0]
			if i.BulkIn != 0x82 || i.BulkOut != 0x02 || i.InterruptIn != 0x83 || i.BulkInMaxPacket != 64 {
				t.Errorf("endpoints %+v", i)
			}
			if i.Descriptor.MaxMessageLength != 271 {
				t.Errorf("class descriptor %+v", i.Descriptor)
			}
		})
	}
	if _, err := ccid.FindInterfaces(rawDescriptors(t, 0x08, false)); !errors.Is(err, ccid.ErrNoInterface) {
		t.Errorf("mass storage interface: %v", err)
	}
	if _, err := ccid.FindInterfaces([]byte{9, 0x02, 0}); err == nil {
		t.Error("truncated descriptors accepted")
	}
}
