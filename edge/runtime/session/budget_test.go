package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

type meteredDriver struct {
	link.Driver
	bps int64
}

func (d meteredDriver) Metrics() link.Metrics { return link.Metrics{BandwidthEstimate: d.bps} }

func TestTheBacklogBudgetFollowsTheBandwidthEstimate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "node.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s, err := New(Config{NodeID: "n", Store: st, Driver: meteredDriver{bps: 8000}, Window: 10})
	if err != nil {
		t.Fatal(err)
	}
	// 30 % of 8000 bit/s is 300 B/s; the budget starts with one second of it.
	o := s.takeOptions(64)
	if o.BacklogItems != 3 || o.BacklogBytes != 300 || o.FreshAfter.After(time.Now().Add(-2*time.Minute)) {
		t.Fatalf("options = %+v", o)
	}
	s.spendBacklog([]store.Item{{Backlog: true, Payload: make([]byte, 1000)}, {Payload: make([]byte, 5000)}})
	if o := s.takeOptions(64); o.BacklogItems != 0 {
		t.Fatalf("backlog allowed while the budget is in debt: %+v", o)
	}
	s.mu.Lock()
	s.tokensAt = s.tokensAt.Add(-3 * time.Second)
	s.mu.Unlock()
	if o := s.takeOptions(64); o.BacklogBytes < 190 || o.BacklogBytes > 210 {
		t.Fatalf("budget after 3 s = %d, want about 200", o.BacklogBytes)
	}
}
