//go:build linux

package usbfs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	ccid "github.com/creamlike1024/go-ccid"
)

// Info describes a CCID reader that Enumerate found.
type Info struct {
	// Path is the usbfs device file, for Open.
	Path      string
	Bus, Addr int
	VendorID  uint16
	ProductID uint16
	// Manufacturer, Product and Serial are the device's strings, as the
	// kernel read them; empty when it has none.
	Manufacturer, Product, Serial string
	Interfaces                    []ccid.Interface
}

// Enumerate lists the CCID readers attached, from sysfs, which needs no
// permission on the device files. It is for Linux: Android apps find
// devices through UsbManager, and may not read sysfs.
func Enumerate() ([]Info, error) {
	return enumerate("/sys/bus/usb/devices", "/dev/bus/usb")
}

func enumerate(sysRoot, devRoot string) ([]Info, error) {
	entries, err := os.ReadDir(sysRoot)
	if err != nil {
		return nil, fmt.Errorf("usbfs: listing USB devices: %w", err)
	}
	var found []Info
	for _, ent := range entries {
		name := ent.Name()
		if strings.Contains(name, ":") { // an interface, as 1-1:1.0
			continue
		}
		dir := filepath.Join(sysRoot, name)
		raw, err := os.ReadFile(filepath.Join(dir, "descriptors"))
		if err != nil {
			continue
		}
		// Devices with no CCID interface, ICCD devices of control
		// transfers and malformed descriptors are no readers to list.
		ifs, err := ccid.FindInterfaces(raw)
		if err != nil {
			continue
		}
		bus, err1 := readInt(dir, "busnum")
		addr, err2 := readInt(dir, "devnum")
		if err1 != nil || err2 != nil || len(raw) < 12 {
			continue
		}
		found = append(found, Info{
			Path:         filepath.Join(devRoot, fmt.Sprintf("%03d", bus), fmt.Sprintf("%03d", addr)),
			Bus:          bus,
			Addr:         addr,
			VendorID:     uint16(raw[8]) | uint16(raw[9])<<8,
			ProductID:    uint16(raw[10]) | uint16(raw[11])<<8,
			Manufacturer: readString(dir, "manufacturer"),
			Product:      readString(dir, "product"),
			Serial:       readString(dir, "serial"),
			Interfaces:   ifs,
		})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Bus != found[j].Bus {
			return found[i].Bus < found[j].Bus
		}
		return found[i].Addr < found[j].Addr
	})
	return found, nil
}

func readString(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(dir, name string) (int, error) {
	return strconv.Atoi(readString(dir, name))
}
