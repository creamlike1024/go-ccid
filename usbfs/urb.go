//go:build linux

package usbfs

import (
	"context"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Transfers are URBs submitted to the kernel, which keeps them until they
// complete, are discarded or the device goes: unlike a synchronous bulk
// transfer, which the kernel cancels, with what it had received, as its
// timeout ends. A transfer waits once for as long as it may, and is
// discarded as its context is done.

// reaper hands the URBs the kernel completes to the transfers waiting for
// them: one waiter at a time reaps for all.
type reaper struct {
	mu      sync.Mutex
	cond    sync.Cond
	reaping bool
	done    map[uintptr]struct{} // URBs reaped for another waiter, by address
}

func (r *reaper) init() {
	r.cond.L = &r.mu
	r.done = map[uintptr]struct{}{}
}

// transfer runs one bulk transfer on endpoint ep, its direction the
// endpoint's, and returns how many bytes went. It ends with timeout, or
// with ctx's error as ctx is done.
func (d *Device) transfer(ctx context.Context, ep byte, p []byte, timeout time.Duration) (int, error) {
	u := &urb{typ: urbTypeBulk, endpoint: ep, bufferLength: int32(len(p))}
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(u) // the kernel writes the outcome back into it
	if len(p) > 0 {
		pin.Pin(&p[0])
		u.buffer = unsafe.Pointer(&p[0])
	}
	if err := d.ioctl(reqSubmitURB, unsafe.Pointer(u)); err != nil {
		return 0, mapErr(err)
	}

	// The watcher discards the URB as the time or the context ends,
	// unless the URB came back first: then the kernel no longer knows
	// it, and its address may be another's.
	var (
		mu       sync.Mutex
		back     bool
		timedOut bool
	)
	stop := make(chan struct{})
	go func() {
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case <-stop:
			return
		case <-ctx.Done():
		case <-t.C:
			mu.Lock()
			timedOut = true
			mu.Unlock()
		}
		mu.Lock()
		defer mu.Unlock()
		if !back {
			d.ioctl(reqDiscardURB, unsafe.Pointer(u))
		}
	}()

	err := d.wait(uintptr(unsafe.Pointer(u)))
	mu.Lock()
	back = true
	expired := timedOut
	mu.Unlock()
	close(stop)
	if err != nil {
		return 0, err
	}

	n := int(u.actualLength)
	switch errno := syscall.Errno(-u.status); {
	case u.status == 0:
		return n, nil
	case errno == syscall.ENOENT || errno == syscall.ECONNRESET:
		// Discarded: the context's end, or the time's.
		if expired {
			return n, ErrTimeout
		}
		if err := ctx.Err(); err != nil {
			return n, err
		}
		return n, ErrTimeout
	case errno == syscall.EPIPE:
		e := uint32(ep)
		d.ioctl(reqClearHalt, unsafe.Pointer(&e))
		return n, ErrStall
	case errno == syscall.EPROTO || errno == syscall.EILSEQ || errno == syscall.ETIME:
		// Errors of the bus, which a reader unplugged mid-transfer gives.
		return n, ErrDisconnected
	default:
		return n, mapErr(errno)
	}
}

// wait waits until the kernel gives back the URB at u.
func (d *Device) wait(u uintptr) error {
	r := &d.reaper
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		if _, ok := r.done[u]; ok {
			delete(r.done, u)
			return nil
		}
		if r.reaping {
			r.cond.Wait()
			continue
		}
		r.reaping = true
		r.mu.Unlock()
		got, err := d.reap()
		r.mu.Lock()
		r.reaping = false
		r.cond.Broadcast()
		if err != nil {
			// The device is gone with the URB unreaped, or the file closed.
			return err
		}
		r.done[got] = struct{}{}
	}
}

// reap waits for a URB to complete and returns its address.
func (d *Device) reap() (uintptr, error) {
	var got uintptr
	if err := d.ioctl(reqReapURB, unsafe.Pointer(&got)); err != nil {
		return 0, mapErr(err)
	}
	return got, nil
}
