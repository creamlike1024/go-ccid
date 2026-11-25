//go:build linux

package usbfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	ccid "github.com/creamlike1024/go-ccid"
	"github.com/creamlike1024/go-ccid/ccidtest"
)

// The request numbers of the kernel, for 64-bit and 32-bit pointers on
// the generic encoding.
func TestRequestNumbers(t *testing.T) {
	if iocSizeBits != 14 {
		t.Skip("MIPS or PowerPC encoding")
	}
	control, submit, reap := uintptr(0xC0185500), uintptr(0x8038550A), uintptr(0x4008550C)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		control, submit, reap = 0xC0105500, 0x802C550A, 0x4004550C
	}
	for name, c := range map[string][2]uintptr{
		"CONTROL":          {reqControl, control},
		"SUBMITURB":        {reqSubmitURB, submit},
		"DISCARDURB":       {reqDiscardURB, 0x0000550B},
		"REAPURB":          {reqReapURB, reap},
		"SETINTERFACE":     {reqSetInterface, 0x80085504},
		"CLAIMINTERFACE":   {reqClaimInterface, 0x8004550F},
		"RELEASEINTERFACE": {reqReleaseInterface, 0x80045510},
		"RESET":            {reqReset, 0x00005514},
		"CLEAR_HALT":       {reqClearHalt, 0x80045515},
		"DISCONNECT_CLAIM": {reqDisconnectClaim, 0x8108551B},
	} {
		if c[0] != c[1] {
			t.Errorf("USBDEVFS_%s = %#x, want %#x", name, c[0], c[1])
		}
	}
}

// urb is laid out as struct usbdevfs_urb.
func TestURBLayout(t *testing.T) {
	var u urb
	want := map[string][2]uintptr{
		"status":       {unsafe.Offsetof(u.status), 4},
		"flags":        {unsafe.Offsetof(u.flags), 8},
		"actualLength": {unsafe.Offsetof(u.actualLength), 0},
		"userContext":  {unsafe.Offsetof(u.userContext), 0},
		"size":         {unsafe.Sizeof(u), 0},
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		want["actualLength"] = [2]uintptr{want["actualLength"][0], 28}
		want["userContext"] = [2]uintptr{want["userContext"][0], 48}
		want["size"] = [2]uintptr{want["size"][0], 56}
	} else {
		want["actualLength"] = [2]uintptr{want["actualLength"][0], 20}
		want["userContext"] = [2]uintptr{want["userContext"][0], 40}
		want["size"] = [2]uintptr{want["size"][0], 44}
	}
	for name, c := range want {
		if c[0] != c[1] {
			t.Errorf("%s at %d, want %d", name, c[0], c[1])
		}
	}
}

// readerDescriptors are the descriptors of a reader with one CCID
// interface, as a usbfs file or sysfs gives them.
func readerDescriptors(t *testing.T) []byte {
	t.Helper()
	cd, _ := ccidtest.NewReader(nil, &ccidtest.Options{}).Descriptor().MarshalBinary()
	raw := []byte{18, 0x01, 0x00, 0x02, 0, 0, 0, 64, 0x2F, 0x07, 0xCC, 0x90, 0x00, 0x01, 1, 2, 0, 1}
	raw = append(raw, 9, 0x02, byte(9+9+54+21), 0, 1, 1, 0, 0x80, 50)
	raw = append(raw, 9, 0x04, 0, 0, 3, ccid.InterfaceClass, 0, 0, 0)
	raw = append(raw, cd...)
	raw = append(raw, 7, 0x05, 0x02, 0x02, 64, 0, 0)
	raw = append(raw, 7, 0x05, 0x82, 0x02, 64, 0, 0)
	return append(raw, 7, 0x05, 0x83, 0x03, 8, 0, 16)
}

func TestFromFDReadsDescriptors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "001")
	if err := os.WriteFile(path, readerDescriptors(t), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := FromFD(int(f.Fd()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.VendorID() != 0x072F || d.ProductID() != 0x90CC {
		t.Errorf("IDs %04x:%04x", d.VendorID(), d.ProductID())
	}
	ifs := d.Interfaces()
	if len(ifs) != 1 || ifs[0].BulkIn != 0x82 || ifs[0].BulkOut != 0x02 {
		t.Fatalf("interfaces %+v", ifs)
	}
	// A plain file takes no ioctl (Android's SELinux denies it first).
	if _, err := d.Claim(ifs[0]); !errors.Is(err, syscall.ENOTTY) && !errors.Is(err, syscall.EACCES) {
		t.Errorf("Claim on a plain file: %v, want ENOTTY", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	// FromFD leaves the descriptor open.
	if _, err := f.Stat(); err != nil {
		t.Errorf("descriptor closed by Close: %v", err)
	}
	if _, err := d.Claim(ifs[0]); !errors.Is(err, ErrClosed) {
		t.Errorf("Claim after Close: %v", err)
	}
}

func TestOpenRejectsNonDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	os.WriteFile(path, []byte("not descriptors"), 0o600)
	if _, err := Open(path, nil); err == nil {
		t.Error("a file without a device descriptor opened")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing"), nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
}

func TestEnumerate(t *testing.T) {
	sys := t.TempDir()
	write := func(dev, name, content string) {
		dir := filepath.Join(sys, dev)
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A reader, a device that is not one, and an interface directory.
	write("3-2", "descriptors", string(readerDescriptors(t)))
	write("3-2", "busnum", "3\n")
	write("3-2", "devnum", "7\n")
	write("3-2", "product", "Test Reader\n")
	hub := readerDescriptors(t)[:18+9]
	write("usb1", "descriptors", string(hub))
	write("usb1", "busnum", "1\n")
	write("usb1", "devnum", "1\n")
	write("3-2:1.0", "descriptors", "x")
	got, err := enumerate(sys, "/dev/bus/usb")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %+v", got)
	}
	r := got[0]
	if r.Path != "/dev/bus/usb/003/007" || r.VendorID != 0x072F || r.Product != "Test Reader" || len(r.Interfaces) != 1 {
		t.Errorf("reader %+v", r)
	}
}
