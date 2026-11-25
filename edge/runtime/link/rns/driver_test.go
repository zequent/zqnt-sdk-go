package rns

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
)

func newTestDriver(t *testing.T, options map[string]any) *Driver {
	t.Helper()
	opts := map[string]any{"address": "127.0.0.1:1", "identity": filepath.Join(t.TempDir(), "node.identity")}
	for k, v := range options {
		opts[k] = v
	}
	d, err := NewDriver(link.LinkConfig{ID: "mesh", Driver: DriverID, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDriverIsRegisteredAndReadsItsOptions(t *testing.T) {
	if !link.Registered(DriverID) {
		t.Fatal("driver not registered")
	}
	d := newTestDriver(t, map[string]any{"app": "demo", "aspects": []any{"a", "b"}, "announce_interval": "1s"})
	if d.ID() != "mesh" {
		t.Errorf("ID %q, want the link id", d.ID())
	}
	if want, err := NewSingle(d.identity, "demo", "a", "b"); err != nil || !bytes.Equal(d.dest.Hash, want.Hash) {
		t.Errorf("destination %x, want demo.a.b %x (%v)", d.dest.Hash, want.Hash, err)
	}
	if len(d.Address()) != 2*TruncatedHashLength {
		t.Errorf("address %q", d.Address())
	}
	c := d.Caps()
	if c.MaxPayload != EncryptedMDU || !c.Bidirectional || c.Security == link.SecurityUnspecified || c.Cost == link.CostUnspecified {
		t.Errorf("caps %+v", c)
	}
	if d.MeshInfo().Kind != link.MeshRouted {
		t.Errorf("mesh kind %s", d.MeshInfo().Kind)
	}
	for _, bad := range []map[string]any{
		{"address": ""},
		{"aspects": []any{1}},
		{"aspects": []any{}},
		{"announce_interval": "soon"},
	} {
		opts := map[string]any{"address": "127.0.0.1:1", "identity": filepath.Join(t.TempDir(), "id")}
		for k, v := range bad {
			opts[k] = v
		}
		if _, err := NewDriver(link.LinkConfig{ID: "mesh", Driver: DriverID, Options: opts}); err == nil {
			t.Errorf("options %v accepted", bad)
		}
	}
}

func TestDriverReusesItsIdentityFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.identity")
	first := newTestDriver(t, map[string]any{"identity": path})
	second := newTestDriver(t, map[string]any{"identity": path})
	if first.Address() != second.Address() {
		t.Fatalf("addresses differ: %s vs %s", first.Address(), second.Address())
	}
}

func TestDriverLifecycle(t *testing.T) {
	d := newTestDriver(t, nil)
	if r := d.Send(t.Context(), []byte("x"), link.SendMeta{To: d.Address()}); r.Status != link.Down {
		t.Fatalf("send before open: %s", r.Status)
	}
	if err := d.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d.Open(t.Context()); err == nil {
		t.Fatal("second Open succeeded")
	}
	if s := <-d.State(); s.State != link.StateDown || s.Reason != "connecting" {
		t.Fatalf("state after open: %+v", s)
	}
	if r := d.Send(t.Context(), make([]byte, EncryptedMDU+1), link.SendMeta{To: d.Address()}); r.Status != link.TooLarge {
		t.Errorf("oversized send: %s", r.Status)
	}
	if r := d.Send(t.Context(), []byte("x"), link.SendMeta{To: "nope"}); r.Status != link.UnknownDestination {
		t.Errorf("send to a malformed address: %s", r.Status)
	}
	if r := d.Send(t.Context(), []byte("x"), link.SendMeta{To: d.Address()}); r.Status != link.UnknownDestination {
		t.Errorf("send to an unannounced destination: %s", r.Status)
	}
	if n := d.Neighbors(); len(n) != 0 {
		t.Errorf("neighbours before any announce: %+v", n)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if r := d.Send(t.Context(), []byte("x"), link.SendMeta{To: d.Address()}); r.Status != link.Down {
		t.Errorf("send after close: %s", r.Status)
	}
	if _, ok := <-d.Receive(); ok {
		t.Error("receive channel open after Close")
	}
	for range d.State() {
	}
	if err := d.Open(t.Context()); err == nil {
		t.Error("Open after Close succeeded")
	}
}

func TestDriverCloseBeforeOpen(t *testing.T) {
	d := newTestDriver(t, nil)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-d.Receive(); ok {
		t.Error("receive channel open after Close")
	}
	if _, ok := <-d.State(); ok {
		t.Error("state channel open after Close")
	}
}
