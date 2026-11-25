// Package linktest is the conformance suite every link driver passes:
//
//	func TestConformance(t *testing.T) {
//	    linktest.Run(t, func(t *testing.T) linktest.Pair { ... two drivers that reach each other ... })
//	}
//
// It checks the lifecycle (Send before Open and after Close, Open twice, Close twice, channels
// closed after Close), a round trip in both directions, MTU rejection, that Send does not keep the
// caller's buffer, state, metrics, mesh info and concurrent use.
package linktest

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
)

// Pair is two unopened drivers on one link that reach each other without loss.
type Pair struct {
	A, B link.Driver
	// AddrA and AddrB are the drivers' addresses in SendMeta.To form.
	AddrA, AddrB string
	// Unknown is an address no peer has; empty skips the unknown-destination check.
	Unknown string
}

// NewPair builds a fresh pair for each subtest. Cleanup of anything besides the drivers is the
// harness's job (t.Cleanup).
type NewPair func(t *testing.T) Pair

// Timeout bounds every wait for a frame or a state.
var Timeout = 5 * time.Second

// Run runs the suite.
func Run(t *testing.T, newPair NewPair) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(*testing.T, Pair)
	}{
		{"Identity", testIdentity},
		{"SendBeforeOpen", testSendBeforeOpen},
		{"OpenTwice", testOpenTwice},
		{"StateUpAfterOpen", testStateUp},
		{"RoundTrip", testRoundTrip},
		{"MaxPayload", testMaxPayload},
		{"TooLarge", testTooLarge},
		{"SendCopiesFrame", testSendCopies},
		{"UnknownDestination", testUnknownDestination},
		{"Metrics", testMetrics},
		{"ConcurrentSend", testConcurrentSend},
		{"Close", testClose},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newPair(t)
			t.Cleanup(func() {
				_ = p.A.Close()
				_ = p.B.Close()
			})
			tc.fn(t, p)
		})
	}
}

func open(t *testing.T, p Pair) {
	t.Helper()
	ctx := t.Context()
	if err := p.A.Open(ctx); err != nil {
		t.Fatalf("open A: %v", err)
	}
	if err := p.B.Open(ctx); err != nil {
		t.Fatalf("open B: %v", err)
	}
	waitUp(t, p.A)
	waitUp(t, p.B)
}

func waitUp(t *testing.T, d link.Driver) {
	t.Helper()
	deadline := time.After(Timeout)
	for {
		select {
		case s, ok := <-d.State():
			if !ok {
				t.Fatalf("%s: state channel closed before UP", d.ID())
			}
			if s.State == link.StateUp {
				return
			}
		case <-deadline:
			t.Fatalf("%s: not UP after %v", d.ID(), Timeout)
		}
	}
}

// Receive waits for the next frame on d.
func Receive(t *testing.T, d link.Driver) link.RxFrame {
	t.Helper()
	select {
	case f, ok := <-d.Receive():
		if !ok {
			t.Fatalf("%s: receive channel closed", d.ID())
		}
		return f
	case <-time.After(Timeout):
		t.Fatalf("%s: no frame after %v", d.ID(), Timeout)
	}
	return link.RxFrame{}
}

func send(t *testing.T, d link.Driver, to string, frame []byte) {
	t.Helper()
	if r := d.Send(t.Context(), frame, link.SendMeta{To: to}); !r.OK() {
		t.Fatalf("%s: send: %s %s", d.ID(), r.Status, r.Reason)
	}
}

func testIdentity(t *testing.T, p Pair) {
	for _, d := range []link.Driver{p.A, p.B} {
		if d.ID() == "" {
			t.Error("ID is empty")
		}
		c := d.Caps()
		if c.MaxPayload <= 0 {
			t.Errorf("%s: MaxPayload %d", d.ID(), c.MaxPayload)
		}
		if c.Security == link.SecurityUnspecified || c.Cost == link.CostUnspecified {
			t.Errorf("%s: Caps leave Security or Cost unspecified: %+v", d.ID(), c)
		}
		if c.DutyCycle < 0 || c.DutyCycle > 1 {
			t.Errorf("%s: DutyCycle %v", d.ID(), c.DutyCycle)
		}
		if d.MeshInfo().Kind == link.MeshUnspecified {
			t.Errorf("%s: MeshInfo.Kind unspecified (links without a mesh report NONE)", d.ID())
		}
	}
}

func testSendBeforeOpen(t *testing.T, p Pair) {
	if r := p.A.Send(t.Context(), []byte("x"), link.SendMeta{To: p.AddrB}); r.Status != link.Down {
		t.Fatalf("send before open: %s, want DOWN", r.Status)
	}
}

func testOpenTwice(t *testing.T, p Pair) {
	open(t, p)
	if err := p.A.Open(t.Context()); err == nil {
		t.Fatal("second Open succeeded")
	}
}

func testStateUp(t *testing.T, p Pair) {
	open(t, p)
}

func testRoundTrip(t *testing.T, p Pair) {
	open(t, p)
	before := time.Now()
	send(t, p.A, p.AddrB, []byte("ping"))
	f := Receive(t, p.B)
	if !bytes.Equal(f.Payload, []byte("ping")) {
		t.Fatalf("B got %q", f.Payload)
	}
	if f.From != "" && f.From != p.AddrA {
		t.Errorf("From %q, want %q or empty", f.From, p.AddrA)
	}
	if f.ReceivedAt.Before(before) {
		t.Errorf("ReceivedAt %v is before the send", f.ReceivedAt)
	}
	if f.Hops < 0 {
		t.Errorf("Hops %d", f.Hops)
	}
	if !p.B.Caps().Bidirectional {
		return
	}
	send(t, p.B, p.AddrA, []byte("pong"))
	if f := Receive(t, p.A); !bytes.Equal(f.Payload, []byte("pong")) {
		t.Fatalf("A got %q", f.Payload)
	}
}

func testMaxPayload(t *testing.T, p Pair) {
	open(t, p)
	frame := bytes.Repeat([]byte{0xA5}, p.A.Caps().MaxPayload)
	send(t, p.A, p.AddrB, frame)
	if f := Receive(t, p.B); !bytes.Equal(f.Payload, frame) {
		t.Fatalf("a frame of MaxPayload bytes arrived as %d bytes", len(f.Payload))
	}
}

func testTooLarge(t *testing.T, p Pair) {
	open(t, p)
	frame := make([]byte, p.A.Caps().MaxPayload+1)
	if r := p.A.Send(t.Context(), frame, link.SendMeta{To: p.AddrB}); r.Status != link.TooLarge {
		t.Fatalf("send of MaxPayload+1 bytes: %s, want TOO_LARGE", r.Status)
	}
	send(t, p.A, p.AddrB, []byte("after"))
	if f := Receive(t, p.B); string(f.Payload) != "after" {
		t.Fatalf("B got %q, the oversized frame went out", f.Payload)
	}
}

func testSendCopies(t *testing.T, p Pair) {
	open(t, p)
	frame := []byte("original")
	send(t, p.A, p.AddrB, frame)
	copy(frame, "XXXXXXXX")
	if f := Receive(t, p.B); string(f.Payload) != "original" {
		t.Fatalf("B got %q: the driver kept the caller's buffer", f.Payload)
	}
}

func testUnknownDestination(t *testing.T, p Pair) {
	if p.Unknown == "" {
		t.Skip("harness gives no unknown address")
	}
	open(t, p)
	if r := p.A.Send(t.Context(), []byte("x"), link.SendMeta{To: p.Unknown}); r.Status != link.UnknownDestination {
		t.Fatalf("send to %q: %s, want UNKNOWN_DESTINATION", p.Unknown, r.Status)
	}
}

func testMetrics(t *testing.T, p Pair) {
	open(t, p)
	send(t, p.A, p.AddrB, []byte("count me"))
	Receive(t, p.B)
	if m := p.A.Metrics(); m.FramesSent < 1 || m.BytesSent < 8 {
		t.Errorf("A metrics after one send: %+v", m)
	}
	if m := p.B.Metrics(); m.FramesReceived < 1 || m.BytesReceived < 8 || m.LastRx.IsZero() {
		t.Errorf("B metrics after one receive: %+v", m)
	}
}

func testConcurrentSend(t *testing.T, p Pair) {
	open(t, p)
	const senders, each = 4, 25
	var wg sync.WaitGroup
	for s := range senders {
		wg.Go(func() {
			for i := range each {
				p.A.Send(context.Background(), fmt.Appendf(nil, "%d-%d", s, i), link.SendMeta{To: p.AddrB})
				_ = p.A.Metrics()
				_ = p.A.Neighbors()
			}
		})
	}
	seen := map[string]bool{}
	for range senders * each {
		seen[string(Receive(t, p.B).Payload)] = true
	}
	wg.Wait()
	if len(seen) != senders*each {
		t.Fatalf("B got %d distinct frames, want %d", len(seen), senders*each)
	}
}

func testClose(t *testing.T, p Pair) {
	open(t, p)
	if err := p.A.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := p.A.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if r := p.A.Send(t.Context(), []byte("x"), link.SendMeta{To: p.AddrB}); r.Status != link.Down {
		t.Errorf("send after close: %s, want DOWN", r.Status)
	}
	waitClosed(t, p.A.Receive(), "receive")
	waitClosed(t, p.A.State(), "state")
}

func waitClosed[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	deadline := time.After(Timeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatalf("%s channel not closed after Close", what)
		}
	}
}
