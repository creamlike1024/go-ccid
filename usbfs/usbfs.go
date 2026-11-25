//go:build linux

package usbfs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	ccid "github.com/creamlike1024/go-ccid"
)

var (
	// ErrBusy: the interface is claimed by another program or driver.
	ErrBusy = errors.New("usbfs: interface busy (claimed by another program, such as pcscd, or a driver)")
	// ErrTimeout: a transfer did not complete in time.
	ErrTimeout = errors.New("usbfs: transfer timed out")
	// ErrStall: the endpoint stalled; it has been cleared.
	ErrStall = errors.New("usbfs: endpoint stalled")
	// ErrDisconnected: the device is gone.
	ErrDisconnected = errors.New("usbfs: device disconnected")
	// ErrClosed: the Device or Endpoints were closed.
	ErrClosed = errors.New("usbfs: closed")
)

// Options configure a Device.
type Options struct {
	// Timeout bounds each transfer when the context has no earlier
	// deadline; 10 seconds when 0. A reader that needs more time sends
	// time extensions, each a transfer of its own.
	Timeout time.Duration
	// DetachKernelDriver has Claim take the interface from a kernel
	// driver bound to it. A program on usbfs, as pcscd through libusb,
	// keeps it regardless.
	DetachKernelDriver bool
}

// Device is a USB device opened through usbfs.
type Device struct {
	fd      int
	owned   bool
	timeout time.Duration
	detach  bool
	raw     []byte
	ifs     []ccid.Interface

	mu      sync.Mutex
	closed  bool
	claimed map[byte]*Endpoints

	reaper reaper
}

// Open opens a usbfs device file, as /dev/bus/usb/001/005. Close closes
// it.
func Open(path string, o *Options) (*Device, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("usbfs: opening %s: %w", path, err)
	}
	d, err := newDevice(fd, true, o)
	if err != nil {
		syscall.Close(fd)
		return nil, err
	}
	return d, nil
}

// FromFD uses a usbfs file descriptor that is already open, as the one
// of Android's UsbDeviceConnection.getFileDescriptor. The descriptor
// stays the caller's: Close does not close it.
func FromFD(fd int, o *Options) (*Device, error) {
	return newDevice(fd, false, o)
}

func newDevice(fd int, owned bool, o *Options) (*Device, error) {
	if o == nil {
		o = &Options{}
	}
	d := &Device{fd: fd, owned: owned, timeout: o.Timeout, detach: o.DetachKernelDriver, claimed: map[byte]*Endpoints{}}
	d.reaper.init()
	if d.timeout <= 0 {
		d.timeout = 10 * time.Second
	}
	raw, err := readDescriptors(fd)
	if err != nil {
		return nil, err
	}
	d.raw = raw
	ifs, err := ccid.FindInterfaces(raw)
	if err != nil && !errors.Is(err, ccid.ErrNoInterface) {
		return nil, err
	}
	d.ifs = ifs
	return d, nil
}

// readDescriptors reads what a usbfs file holds: the device descriptor
// and the descriptors of every configuration.
func readDescriptors(fd int) ([]byte, error) {
	var raw []byte
	buf := make([]byte, 4096)
	for off := int64(0); ; {
		n, err := syscall.Pread(fd, buf, off)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("usbfs: reading the descriptors: %w", err)
		}
		if n == 0 {
			break
		}
		raw = append(raw, buf[:n]...)
		off += int64(n)
	}
	if len(raw) < 18 || raw[0] != 18 || raw[1] != 0x01 {
		return nil, errors.New("usbfs: not a USB device file (no device descriptor)")
	}
	return raw, nil
}

// Descriptors returns the device's raw descriptors.
func (d *Device) Descriptors() []byte { return d.raw }

// VendorID returns idVendor.
func (d *Device) VendorID() uint16 { return uint16(d.raw[8]) | uint16(d.raw[9])<<8 }

// ProductID returns idProduct.
func (d *Device) ProductID() uint16 { return uint16(d.raw[10]) | uint16(d.raw[11])<<8 }

// Interfaces returns the device's CCID interfaces, none for a device
// that is not a CCID reader.
func (d *Device) Interfaces() []ccid.Interface { return d.ifs }

// Claim claims a CCID interface and returns its endpoints.
func (d *Device) Claim(i ccid.Interface) (*Endpoints, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	if _, ok := d.claimed[i.Number]; ok {
		return nil, fmt.Errorf("usbfs: interface %d already claimed", i.Number)
	}
	if err := d.claim(uint32(i.Number)); err != nil {
		return nil, err
	}
	if i.AltSetting != 0 {
		si := setInterface{uint32(i.Number), uint32(i.AltSetting)}
		if err := d.ioctl(reqSetInterface, unsafe.Pointer(&si)); err != nil {
			d.release(uint32(i.Number))
			return nil, fmt.Errorf("usbfs: alternate setting %d of interface %d: %w", i.AltSetting, i.Number, mapErr(err))
		}
	}
	e := &Endpoints{d: d, iface: i, quit: make(chan struct{})}
	d.claimed[i.Number] = e
	return e, nil
}

func (d *Device) claim(n uint32) error {
	var err error
	if d.detach {
		dc := disconnectClaim{iface: n, flags: disconnectClaimExceptDriver}
		copy(dc.driver[:], "usbfs")
		err = d.ioctl(reqDisconnectClaim, unsafe.Pointer(&dc))
	} else {
		err = d.ioctl(reqClaimInterface, unsafe.Pointer(&n))
	}
	if err != nil {
		return fmt.Errorf("usbfs: claiming interface %d: %w", n, mapErr(err))
	}
	return nil
}

func (d *Device) release(n uint32) error {
	return mapErr(d.ioctl(reqReleaseInterface, unsafe.Pointer(&n)))
}

// Reset resets the device. Claimed interfaces stay claimed if the device
// comes back as it was; otherwise reopen it.
func (d *Device) Reset() error {
	return mapErr(d.ioctl(reqReset, nil))
}

// Close releases the claimed interfaces and, for a Device of Open,
// closes the file. Transfers under way end, with ErrClosed, before it
// returns.
func (d *Device) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	claimed := d.claimed
	for _, e := range claimed {
		e.stop()
	}
	d.claimed = nil
	d.mu.Unlock()
	for n, e := range claimed {
		e.busy.Wait()
		d.release(uint32(n))
	}
	if d.owned {
		return syscall.Close(d.fd)
	}
	return nil
}

func (d *Device) ioctl(req uintptr, arg unsafe.Pointer) error {
	for {
		_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(d.fd), req, uintptr(arg))
		switch e {
		case 0:
			return nil
		case syscall.EINTR:
			continue
		}
		return e
	}
}

func mapErr(err error) error {
	switch err {
	case nil:
		return nil
	case syscall.EBUSY:
		return ErrBusy
	case syscall.ETIMEDOUT:
		return ErrTimeout
	case syscall.EPIPE:
		return ErrStall
	case syscall.ENODEV, syscall.ESHUTDOWN:
		return ErrDisconnected
	}
	return err
}

// Endpoints are the bulk endpoints of a claimed CCID interface: a
// ccid.Transport.
type Endpoints struct {
	d      *Device
	iface  ccid.Interface
	closed bool // under d.mu
	// quit is closed as the Endpoints close: transfers under way discard
	// their URBs.
	quit chan struct{}
	// busy counts the transfers under way, which Close waits for: the
	// kernel frees the URBs of a released interface without giving them
	// back, which would leave their waiters waiting.
	busy sync.WaitGroup
}

var _ ccid.Controller = (*Endpoints)(nil)

// Interface returns the claimed interface.
func (e *Endpoints) Interface() ccid.Interface { return e.iface }

// begin counts a transfer under way, or returns ErrClosed. done ends it.
func (e *Endpoints) begin() error {
	e.d.mu.Lock()
	defer e.d.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	e.busy.Add(1)
	return nil
}

func (e *Endpoints) done() { e.busy.Done() }

// stop marks the Endpoints closed and has their transfers end: under d.mu.
func (e *Endpoints) stop() {
	e.closed = true
	close(e.quit)
}

// limit returns how long a transfer may take: the device's timeout, or
// less as the context's deadline is nearer.
func (e *Endpoints) limit(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	t := e.d.timeout
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left < t {
			if left <= 0 {
				return 0, context.DeadlineExceeded
			}
			t = left
		}
	}
	return t, nil
}

// Write sends a command message to the bulk-OUT endpoint.
func (e *Endpoints) Write(ctx context.Context, p []byte) error {
	if err := e.begin(); err != nil {
		return err
	}
	defer e.done()
	t, err := e.limit(ctx)
	if err != nil {
		return err
	}
	n, err := e.d.transfer(ctx, e.iface.BulkOut, p, t, e.quit)
	if err != nil {
		return err
	}
	if n != len(p) {
		return fmt.Errorf("usbfs: wrote %d of %d bytes", n, len(p))
	}
	return nil
}

// Read receives one transfer from the bulk-IN endpoint. It returns the
// context's error as soon as the context is done.
func (e *Endpoints) Read(ctx context.Context, p []byte) (int, error) {
	if err := e.begin(); err != nil {
		return 0, err
	}
	defer e.done()
	t, err := e.limit(ctx)
	if err != nil {
		return 0, err
	}
	return e.d.transfer(ctx, e.iface.BulkIn, p, t, e.quit)
}

// ClassRequest sends a class-specific request with no data stage to the
// interface, on the default control pipe: as CCID's ABORT.
func (e *Endpoints) ClassRequest(ctx context.Context, request byte, value uint16) error {
	if err := e.begin(); err != nil {
		return err
	}
	defer e.done()
	t, err := e.limit(ctx)
	if err != nil {
		return err
	}
	ct := ctrlTransfer{
		requestType: 0x21, // host to device, class, interface
		request:     request,
		value:       value,
		index:       uint16(e.iface.Number),
		timeout:     uint32(max(t.Milliseconds(), 1)), // 0 would wait forever
	}
	if err := e.d.ioctl(reqControl, unsafe.Pointer(&ct)); err != nil {
		return fmt.Errorf("usbfs: class request %#02x: %w", request, mapErr(err))
	}
	return nil
}

// Close releases the interface. The Device stays open. Transfers under
// way end, with ErrClosed, before it returns.
func (e *Endpoints) Close() error {
	e.d.mu.Lock()
	if e.closed {
		e.d.mu.Unlock()
		return nil
	}
	e.stop()
	delete(e.d.claimed, e.iface.Number)
	e.d.mu.Unlock()
	e.busy.Wait()
	return e.d.release(uint32(e.iface.Number))
}
