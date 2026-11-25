package ccid_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	ccid "github.com/creamlike1024/go-ccid"
	"github.com/creamlike1024/go-ccid/ccidtest"
)

func TestDescriptorRoundTrip(t *testing.T) {
	d := ccidtest.NewReader(nil, &ccidtest.Options{Level: ccid.LevelShortAPDU, Features: ccid.FeatureAutoVoltage}).Descriptor()
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
	return rawDescriptorsProtocol(t, class, 0, cdAfterEndpoints)
}

func rawDescriptorsProtocol(t *testing.T, class, protocol byte, cdAfterEndpoints bool) []byte {
	t.Helper()
	cd, _ := ccidtest.NewReader(nil, &ccidtest.Options{}).Descriptor().MarshalBinary()
	device := []byte{18, 0x01, 0x00, 0x02, 0, 0, 0, 64, 0x2F, 0x07, 0xCC, 0x90, 0x00, 0x01, 1, 2, 0, 1}
	config := []byte{9, 0x02, 0, 0, 1, 1, 0, 0x80, 50}
	iface := []byte{9, 0x04, 0, 0, 3, class, 0, protocol, 0}
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
	for _, p := range []byte{ccid.InterfaceProtocolICCDA, ccid.InterfaceProtocolICCDB} {
		_, err := ccid.FindInterfaces(rawDescriptorsProtocol(t, ccid.InterfaceClass, p, false))
		if !errors.Is(err, ccid.ErrNotSupported) {
			t.Errorf("ICCD protocol %d: %v, want ErrNotSupported", p, err)
		}
	}
	if _, err := ccid.FindInterfaces(rawDescriptorsProtocol(t, ccid.InterfaceClass, 3, false)); !errors.Is(err, ccid.ErrNoInterface) {
		t.Errorf("reserved protocol 3: %v, want ErrNoInterface", err)
	}
	if ifs, err := ccid.FindInterfaces(rawDescriptorsProtocol(t, 0xFF, 5, false)); err != nil || ifs[0].Protocol != 5 {
		t.Errorf("vendor class with its own protocol: %v %v", ifs, err)
	}
	if _, err := ccid.FindInterfaces([]byte{9, 0x02, 0}); err == nil {
		t.Error("truncated descriptors accepted")
	}
}

// A composite device, as a security key: a HID interface, whose HID class
// descriptor is also of type 0x21, before or after the CCID interface.
func TestFindInterfacesComposite(t *testing.T) {
	hid := []byte{
		9, 0x04, 1, 0, 2, 0x03, 0, 0, 0, // interface 1: HID
		9, 0x21, 0x11, 0x01, 0, 1, 0x22, 34, 0, // HID class descriptor
		7, 0x05, 0x84, 0x03, 64, 0, 5, // interrupt IN
		7, 0x05, 0x04, 0x03, 64, 0, 5, // interrupt OUT
	}
	ccidRaw := rawDescriptors(t, ccid.InterfaceClass, false)
	head, ifaces := ccidRaw[:27], ccidRaw[27:] // device and configuration, then the CCID interface
	for name, raw := range map[string][]byte{
		"HID first": append(append(slices.Clone(head), hid...), ifaces...),
		"HID last":  append(slices.Clone(ccidRaw), hid...),
	} {
		ifs, err := ccid.FindInterfaces(raw)
		if err != nil || len(ifs) != 1 || ifs[0].Descriptor.MaxMessageLength != 271 {
			t.Errorf("%s: %+v, %v; want the CCID interface", name, ifs, err)
		}
	}

	// A CCID interface with a malformed class descriptor is left out, not
	// the device's others.
	bad := slices.Clone(ccidRaw)
	bad[27+9] = 9 // the class descriptor, cut short
	bad = append(bad[:27+9+9], bad[27+9+ccid.DescriptorLength:]...)
	bad = append(bad, rawDescriptors(t, 0xFF, false)[27:]...)
	if ifs, err := ccid.FindInterfaces(bad); err != nil || len(ifs) != 1 || ifs[0].Class != 0xFF {
		t.Errorf("one malformed CCID interface: %+v, %v; want the other", ifs, err)
	}
}
