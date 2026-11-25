//go:build linux

// Command ccid-probe talks to a CCID reader through usbfs: it lists the
// readers, connects to the card in one, and sends the APDUs given, in
// hex, printing the responses.
//
//	ccid-probe                       # list the readers, connect to the first
//	ccid-probe 00A4040000            # and send an APDU
//	ccid-probe -trace -t1 00A4040000 # with every CCID message, over T=1
//
// The reader must not be in use by pcscd. Each APDU goes as given: only
// send commands whose effect on the card you know.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	ccid "github.com/creamlike1024/go-ccid"
	"github.com/creamlike1024/go-ccid/usbfs"
)

func main() {
	path := flag.String("reader", "", "the reader's usbfs device file, as /dev/bus/usb/001/005 (the first reader found when empty)")
	trace := flag.Bool("trace", false, "print every CCID message")
	t1 := flag.Bool("t1", false, "prefer T=1 to T=0")
	noPPS := flag.Bool("nopps", false, "keep the card at the default rate")
	getResponse := flag.Bool("getresponse", false, "fetch the responses of 61xx and correct the Le of 6Cxx at TPDU level with T=0")
	slot := flag.Uint("slot", 0, "the slot")
	timeout := flag.Duration("timeout", time.Minute, "the time for all of it")
	flag.Parse()
	if err := run(*path, *trace, *t1, *noPPS, *getResponse, byte(*slot), *timeout, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "ccid-probe:", err)
		os.Exit(1)
	}
}

func run(path string, trace, t1, noPPS, getResponse bool, slot byte, timeout time.Duration, apdus []string) error {
	var cmds [][]byte
	for _, a := range apdus {
		b, err := hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(a))
		if err != nil {
			return fmt.Errorf("APDU %q: %w", a, err)
		}
		cmds = append(cmds, b)
	}
	// A reader given is opened as it is: where sysfs can't be read, as for
	// Android's shell, Enumerate finds nothing.
	if path == "" {
		readers, err := usbfs.Enumerate()
		if err != nil {
			return err
		}
		for _, r := range readers {
			fmt.Printf("%s  %04x:%04x  %s %s\n", r.Path, r.VendorID, r.ProductID, r.Manufacturer, r.Product)
		}
		if len(readers) == 0 {
			return errors.New("no CCID reader found")
		}
		path = readers[0].Path
	}
	dev, err := usbfs.Open(path, nil)
	if err != nil {
		return err
	}
	defer dev.Close()
	ifs := dev.Interfaces()
	if len(ifs) == 0 {
		return fmt.Errorf("%s has no CCID interface", path)
	}
	iface := ifs[0]
	d := iface.Descriptor
	fmt.Printf("interface %d: bulk in %#02x out %#02x, interrupt %#02x\n", iface.Number, iface.BulkIn, iface.BulkOut, iface.InterruptIn)
	fmt.Printf("CCID %x.%02x, %v level, dwFeatures %#08x, protocols %#x, max message %d, max IFSD %d, clock %d kHz, rate %d to %d bit/s\n",
		d.CCIDVersion>>8, d.CCIDVersion&0xFF, d.ExchangeLevel(), d.Features, d.Protocols, d.MaxMessageLength, d.MaxIFSD, d.DefaultClock, d.DataRate, d.MaxDataRate)
	ep, err := dev.Claim(iface)
	if err != nil {
		return err
	}
	opts := &ccid.Options{}
	if trace {
		opts.Trace = func(dir ccid.Direction, m []byte) {
			arrow := "->"
			if dir == ccid.FromReader {
				arrow = "<-"
			}
			fmt.Printf("    %s %x\n", arrow, m)
		}
	}
	r, err := ccid.Open(ep, d, opts)
	if err != nil {
		return err
	}
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	st, err := r.SlotStatus(ctx, slot)
	if err != nil {
		return err
	}
	fmt.Println("slot", slot, "card", st)
	co := &ccid.ConnectOptions{NoPPS: noPPS, AutoGetResponse: getResponse}
	if t1 {
		co.Prefer = []ccid.Protocol{ccid.T1, ccid.T0}
	}
	start := time.Now()
	card, err := r.Connect(ctx, slot, co)
	if err != nil {
		return err
	}
	defer card.Disconnect(ctx)
	fmt.Printf("connected in %v: %v, ATR %x\n", time.Since(start).Round(time.Millisecond), card.Protocol(), card.ATR().Raw)
	for _, cmd := range cmds {
		t0 := time.Now()
		resp, err := card.Transmit(ctx, cmd)
		if err != nil {
			return fmt.Errorf("%x: %w", cmd, err)
		}
		fmt.Printf("> %x\n< %x  (%v)\n", cmd, resp, time.Since(t0).Round(time.Millisecond))
	}
	return nil
}
