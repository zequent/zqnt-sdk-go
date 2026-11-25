package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

func appendAt(t *testing.T, s *store.Store, lane store.Lane, at time.Time, payload string) uint32 {
	t.Helper()
	seq, err := s.Append(context.Background(), store.Item{Lane: lane, DeviceTime: at, Payload: []byte(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func payloadsOf(items []store.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = string(it.Payload)
	}
	return out
}

func TestFreshTelemetryGoesBeforeTheBacklogWhichGoesNewestFirst(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := open(t, tempDB(t), store.Options{Now: func() time.Time { return now }})
	for i, p := range []string{"old-1", "old-2", "old-3", "old-4"} {
		appendAt(t, s, telemetry, now.Add(-time.Hour+time.Duration(i)*time.Minute), p)
	}
	appendAt(t, s, bulk, now, "bulk")
	appendAt(t, s, telemetry, now.Add(-time.Second), "fresh-1")
	appendAt(t, s, alarm, now, "alarm")
	appendAt(t, s, telemetry, now, "fresh-2")
	flush(t, s)

	items, err := s.TakeWith(store.TakeOptions{Max: 100, FreshAfter: now.Add(-2 * time.Minute), BacklogItems: 2})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alarm", "fresh-1", "fresh-2", "old-4", "old-3", "bulk"}
	if got := payloadsOf(items); !slices.Equal(got, want) {
		t.Fatalf("take = %v, want %v", got, want)
	}
	for _, it := range items {
		if it.Backlog != (it.Payload[0] == 'o') {
			t.Errorf("%s: backlog = %v", it.Payload, it.Backlog)
		}
		if it.Epoch != 7 {
			t.Errorf("%s: epoch = %d", it.Payload, it.Epoch)
		}
	}
	if n := s.InFlightBacklog(); n != 2 {
		t.Fatalf("backlog in flight = %d, want 2", n)
	}
	more, err := s.TakeWith(store.TakeOptions{Max: 100, FreshAfter: now.Add(-2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(more) != 0 {
		t.Fatalf("took %v without a backlog budget", payloadsOf(more))
	}
}

func TestTheBacklogByteBudgetAlwaysLetsOneSampleThrough(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := open(t, tempDB(t), store.Options{Now: func() time.Time { return now }})
	for _, p := range []string{"aaaa", "bbbb", "cccc"} {
		appendAt(t, s, telemetry, now.Add(-time.Hour), p)
	}
	flush(t, s)
	opts := store.TakeOptions{Max: 10, FreshAfter: now, BacklogItems: 10, BacklogBytes: 2}
	items, err := s.TakeWith(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := payloadsOf(items); !slices.Equal(got, []string{"cccc"}) {
		t.Fatalf("take = %v, want the newest sample only", got)
	}
	opts.BacklogBytes = 8
	items, err = s.TakeWith(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := payloadsOf(items); !slices.Equal(got, []string{"bbbb", "aaaa"}) {
		t.Fatalf("take = %v", got)
	}
}

func TestDroppingSignalsAndCountsAgainstTheQuota(t *testing.T) {
	lanes := store.DefaultLanes()
	lanes[alarm] = store.LanePolicy{MaxItems: 2, Sync: true}
	s := open(t, tempDB(t), store.Options{Lanes: lanes})
	appendN(t, s, alarm, 2)
	select {
	case <-s.Drops():
		t.Fatal("drops signalled before anything was dropped")
	default:
	}
	appendN(t, s, alarm, 1)
	select {
	case <-s.Drops():
	case <-time.After(time.Second):
		t.Fatal("no drops signal")
	}
	b := lane(t, s, alarm)
	if b.Dropped != 1 || b.Items != 2 || b.MaxItems != 2 || b.Fill() != 1 {
		t.Fatalf("alarm backlog = %+v fill %v", b, b.Fill())
	}
	if f := lane(t, s, telemetry); f.MaxBytes != 50<<20 || f.Fill() != 0 {
		t.Fatalf("telemetry backlog = %+v", f)
	}
}

func TestUnmarkIgnoresAnotherEpoch(t *testing.T) {
	s := open(t, tempDB(t), store.Options{})
	seqs := appendN(t, s, alarm, 1)
	takeAll(t, s)
	s.Unmark(s.Epoch()+1, seqs...)
	if s.InFlight() != 1 {
		t.Fatal("unmark of another epoch released the item")
	}
}

func TestRollsOverToANewEpochBeforeSeqWraps(t *testing.T) {
	path := tempDB(t)
	s := open(t, path, store.Options{MaxSeq: 10, FlushInterval: time.Hour})
	appendN(t, s, alarm, 5)
	takeAll(t, s)
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: 3})
	seqs := appendN(t, s, alarm, 5)
	if seqs[len(seqs)-1] != 10 {
		t.Fatalf("seqs = %v", seqs)
	}
	next := appendN(t, s, alarm, 1)
	if s.Epoch() != 8 {
		t.Fatalf("epoch after seq 10 = %d, want 8", s.Epoch())
	}
	// Seqs 4..10 are still held: they become 1..7 in their order, the new item 8.
	if next[0] != 8 {
		t.Fatalf("first seq of the new epoch = %d, want 8", next[0])
	}
	items := takeAll(t, s)
	if got := seqsOf(items); !slices.Equal(got, []uint32{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("seqs after rollover = %v", got)
	}
	want := []string{"LANE_ALARM-3", "LANE_ALARM-4", "LANE_ALARM-0", "LANE_ALARM-1", "LANE_ALARM-2", "LANE_ALARM-3", "LANE_ALARM-4", "LANE_ALARM-0"}
	if got := payloadsOf(items); !slices.Equal(got, want) {
		t.Fatalf("order after rollover = %v, want %v", got, want)
	}
	for _, it := range items {
		if it.Epoch != 8 {
			t.Fatalf("item epoch = %d", it.Epoch)
		}
	}
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: 10})
	if s.InFlight() != 8 {
		t.Fatal("an ack of the old epoch settled renumbered items")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	r := open(t, path, store.Options{MaxSeq: 10})
	if r.Epoch() != 8 {
		t.Fatalf("epoch after reopen = %d", r.Epoch())
	}
	if got := seqsOf(takeAll(t, r)); !slices.Equal(got, []uint32{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("seqs after reopen = %v", got)
	}
	if seq := appendN(t, r, alarm, 1)[0]; seq != 9 {
		t.Fatalf("next seq after reopen = %d", seq)
	}
}

func TestRollsOverEarlyOnlyWhileNothingIsInFlight(t *testing.T) {
	s := open(t, tempDB(t), store.Options{MaxSeq: 32, FlushInterval: 5 * time.Millisecond})
	appendN(t, s, telemetry, 29)
	flush(t, s)
	items := takeAll(t, s)
	appendN(t, s, telemetry, 1)
	time.Sleep(50 * time.Millisecond)
	if s.Epoch() != 7 {
		t.Fatal("rolled over with items in flight")
	}
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: items[len(items)-1].Seq})
	waitFor(t, func() bool { return s.Epoch() == 8 })
	if got := seqsOf(takeAll(t, s)); !slices.Equal(got, []uint32{1}) {
		t.Fatalf("held after an early rollover = %v", got)
	}
	if seq := appendN(t, s, telemetry, 1)[0]; seq != 2 {
		t.Fatalf("next seq after an early rollover = %d", seq)
	}
}

func TestAppendFailsWhenEvenANewEpochHasNoSeqLeft(t *testing.T) {
	s := open(t, tempDB(t), store.Options{MaxSeq: 10, FlushInterval: time.Hour})
	appendN(t, s, telemetry, 10)
	if _, err := s.Append(context.Background(), store.Item{Lane: telemetry}); !errors.Is(err, store.ErrSeqExhausted) {
		t.Fatalf("append beyond MaxSeq held items = %v", err)
	}
	if s.Epoch() != 7 {
		t.Fatalf("rolled over to epoch %d without freeing a seq", s.Epoch())
	}
	flush(t, s)
	takeAll(t, s)
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: 1})
	if seq := appendN(t, s, telemetry, 1)[0]; seq != 10 || s.Epoch() != 8 {
		t.Fatalf("seq %d in epoch %d, want 10 in 8", seq, s.Epoch())
	}
	flush(t, s)
	if got := seqsOf(takeAll(t, s)); !slices.Equal(got, []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
		t.Fatalf("seqs = %v", got)
	}
}

func TestAppendRollsOverItemsWaitingForTheirCommit(t *testing.T) {
	s := open(t, tempDB(t), store.Options{MaxSeq: 10, FlushInterval: time.Hour})
	appendN(t, s, telemetry, 8)
	flush(t, s)
	takeAll(t, s)
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: 8})
	appendN(t, s, telemetry, 2)
	if seq := appendN(t, s, telemetry, 1)[0]; seq != 3 || s.Epoch() != 8 {
		t.Fatalf("seq %d in epoch %d, want 3 in 8", seq, s.Epoch())
	}
	flush(t, s)
	items := takeAll(t, s)
	want := []string{"LANE_TELEMETRY-0", "LANE_TELEMETRY-1", "LANE_TELEMETRY-0"}
	if got := payloadsOf(items); !slices.Equal(got, want) || !slices.Equal(seqsOf(items), []uint32{1, 2, 3}) {
		t.Fatalf("after rollover = %v %v", seqsOf(items), got)
	}
}
