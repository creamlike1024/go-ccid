package ccid_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ccid "github.com/creamlike1024/go-ccid"
	"github.com/creamlike1024/go-ccid/ccidtest"
)

// A command whose context ends while the card takes its time is aborted,
// and the slot takes the next command.
func TestAbortAfterCancel(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0, n: 2}, ccidtest.Options{})
	c := connect(t, r, nil)
	sim.Hold()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Transmit(ctx, []byte{0x00, 0xA4, 0x04, 0x00, 0x00}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline's", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("gave up after %v", d)
	}
	if sim.Aborts() != 1 {
		t.Fatalf("%d ABORT requests, want 1", sim.Aborts())
	}
	// The PC_to_RDR_Abort followed the ABORT request.
	cmds := sim.Commands()
	if last := cmds[len(cmds)-1]; last.Type != ccid.PCtoRDRAbort {
		t.Fatalf("last command %v", last.Type)
	}
	if got := transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 0x02); string(got) != "\x00\x01\x90\x00" {
		t.Fatalf("after the abort: % x", got)
	}
}

// The response a reader had already sent for the command aborted is
// dropped: only the slot status of the PC_to_RDR_Abort ends the abort.
func TestAbortDropsLateResponse(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0, n: 2}, ccidtest.Options{})
	c := connect(t, r, nil)
	// The late response of the command given up on, with its sequence.
	seq := byte(len(sim.Commands()))
	late, _ := ccid.Message{Type: ccid.RDRtoPCDataBlock, Seq: seq, Data: []byte{0x6F, 0x00}}.MarshalBinary()
	sim.Hold()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	go func() {
		<-ctx.Done()
		sim.Inject(late)
	}()
	if _, err := c.Transmit(ctx, []byte{0x00, 0xA4, 0x04, 0x00, 0x00}); err == nil {
		t.Fatal("no error")
	}
	if got := transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 0x02); string(got) != "\x00\x01\x90\x00" {
		t.Fatalf("after the abort: % x", got)
	}
}

// Reader.Abort needs control transfers.
func TestAbortNeedsController(t *testing.T) {
	sim := ccidtest.NewReader(&testCard{atr: ccidtest.ATRT0, n: 2}, &ccidtest.Options{})
	r, err := ccid.Open(bulkOnly{sim}, sim.Descriptor(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Abort(context.Background(), 0); !errors.Is(err, ccid.ErrNotSupported) {
		t.Fatalf("err = %v", err)
	}
	// Without them a cancelled command is left to the stale responses'
	// handling, as before.
	c := connect(t, r, nil)
	sim.Hold()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Transmit(ctx, []byte{0x00, 0xA4, 0x04, 0x00, 0x00}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	sim.Release()
	if got := transmit(t, c, 0x00, 0xCA, 0x00, 0x00, 0x02); string(got) != "\x00\x01\x90\x00" {
		t.Fatalf("after the stale response: % x", got)
	}
}

// bulkOnly is a Transport without control transfers.
type bulkOnly struct{ ccid.Transport }

// A reader unplugged fails what follows with its transport's error, at
// once.
func TestUnplugged(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0, n: 2}, ccidtest.Options{})
	c := connect(t, r, nil)
	sim.Unplug()
	if _, err := c.Transmit(context.Background(), []byte{0x00, 0xCA, 0x00, 0x00, 0x01}); !errors.Is(err, ccidtest.ErrUnplugged) {
		t.Fatalf("transmit: %v", err)
	}
	if _, err := r.SlotStatus(context.Background(), 0); !errors.Is(err, ccidtest.ErrUnplugged) {
		t.Fatalf("slot status: %v", err)
	}
}

// A reader unplugged while the card takes its time ends the wait.
func TestUnpluggedWhileWaiting(t *testing.T) {
	r, sim := open(t, &testCard{atr: ccidtest.ATRT0, n: 2}, ccidtest.Options{})
	c := connect(t, r, nil)
	sim.Hold()
	go func() {
		time.Sleep(20 * time.Millisecond)
		sim.Unplug()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Transmit(ctx, []byte{0x00, 0xCA, 0x00, 0x00, 0x01}); !errors.Is(err, ccidtest.ErrUnplugged) {
		t.Fatalf("err = %v", err)
	}
}
