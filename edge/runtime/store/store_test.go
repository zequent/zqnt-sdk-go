package store_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

const (
	alarm     = edgelinkv3.Lane_LANE_ALARM
	reply     = edgelinkv3.Lane_LANE_COMMAND_REPLY
	status    = edgelinkv3.Lane_LANE_STATUS
	telemetry = edgelinkv3.Lane_LANE_TELEMETRY
	bulk      = edgelinkv3.Lane_LANE_BULK
)

func open(t *testing.T, path string, opts store.Options) *store.Store {
	t.Helper()
	if opts.NewEpoch == nil {
		opts.NewEpoch = func() uint32 { return 7 }
	}
	s, err := store.Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func tempDB(t *testing.T) string { return filepath.Join(t.TempDir(), "node.db") }

func appendN(t *testing.T, s *store.Store, lane store.Lane, n int) []uint32 {
	t.Helper()
	var seqs []uint32
	for i := range n {
		seq, err := s.Append(context.Background(), store.Item{Lane: lane, Payload: fmt.Appendf(nil, "%v-%d", lane, i)})
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

func takeAll(t *testing.T, s *store.Store) []store.Item {
	t.Helper()
	items, err := s.Take(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func seqsOf(items []store.Item) []uint32 {
	out := make([]uint32, len(items))
	for i, it := range items {
		out[i] = it.Seq
	}
	return out
}

func flush(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestTakeDrainsByLaneThenSeq(t *testing.T) {
	s := open(t, tempDB(t), store.Options{FlushInterval: time.Hour})
	tel := appendN(t, s, telemetry, 2)
	blk := appendN(t, s, bulk, 1)
	st := appendN(t, s, status, 1)
	al := appendN(t, s, alarm, 2)
	rep := appendN(t, s, reply, 1)
	flush(t, s)

	got := seqsOf(takeAll(t, s))
	want := slices.Concat(al, rep, st, tel, blk)
	if !slices.Equal(got, want) {
		t.Fatalf("drain order = %v, want %v", got, want)
	}
	if again := takeAll(t, s); len(again) != 0 {
		t.Fatalf("in-flight items handed out again: %v", seqsOf(again))
	}
}

func TestBatchedCommits(t *testing.T) {
	s := open(t, tempDB(t), store.Options{FlushInterval: time.Hour, FlushItems: 5})
	appendN(t, s, telemetry, 4)
	if got := takeAll(t, s); len(got) != 0 {
		t.Fatalf("uncommitted items visible: %v", seqsOf(got))
	}
	appendN(t, s, telemetry, 1)
	waitFor(t, func() bool { return len(takeAll(t, s)) == 5 })

	appendN(t, s, alarm, 1)
	if got := takeAll(t, s); len(got) != 1 || got[0].Lane != alarm {
		t.Fatalf("sync lane not committed on append: %v", got)
	}

	timed := open(t, tempDB(t), store.Options{FlushInterval: 20 * time.Millisecond})
	appendN(t, timed, telemetry, 1)
	waitFor(t, func() bool { return len(takeAll(t, timed)) == 1 })
}

func TestAckCumulativeAndSelective(t *testing.T) {
	path := tempDB(t)
	s := open(t, path, store.Options{FlushInterval: time.Hour})
	seqs := appendN(t, s, telemetry, 10)
	flush(t, s)
	takeAll(t, s)

	s.Ack(&edgelinkv3.Ack{Epoch: 99, CumulativeSeq: 10})
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: seqs[2], SelectiveBitmap: []byte{0b0000_0101}})
	s.Release()
	got := seqsOf(takeAll(t, s))
	want := []uint32{seqs[4], seqs[6], seqs[7], seqs[8], seqs[9]}
	if !slices.Equal(got, want) {
		t.Fatalf("after ack = %v, want %v", got, want)
	}
	flush(t, s)
	backlog := lane(t, s, telemetry)
	if backlog.Items != 5 {
		t.Fatalf("backlog after delete = %d items, want 5", backlog.Items)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := open(t, path, store.Options{})
	if got := seqsOf(takeAll(t, reopened)); !slices.Equal(got, want) {
		t.Fatalf("after reopen = %v, want %v", got, want)
	}
}

func TestAckFromSelectiveBase(t *testing.T) {
	s := open(t, tempDB(t), store.Options{FlushInterval: time.Hour})
	seqs := appendN(t, s, telemetry, 10)
	flush(t, s)
	takeAll(t, s)

	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: seqs[0], SelectiveBase: seqs[6], SelectiveBitmap: []byte{0b0000_0101}})
	s.Release()
	got := seqsOf(takeAll(t, s))
	want := []uint32{seqs[1], seqs[2], seqs[3], seqs[4], seqs[5], seqs[6], seqs[8]}
	if !slices.Equal(got, want) {
		t.Fatalf("after ack = %v, want %v", got, want)
	}
}

func TestHeldFrom(t *testing.T) {
	lanes := store.DefaultLanes()
	lanes[alarm] = store.LanePolicy{MaxItems: 2, Sync: true}
	s := open(t, tempDB(t), store.Options{Lanes: lanes, FlushInterval: time.Hour})
	if held, err := s.HeldFrom(); err != nil || held != 0 {
		t.Fatalf("empty store held from %d, %v", held, err)
	}
	seqs := appendN(t, s, telemetry, 5)
	flush(t, s)
	if held, _ := s.HeldFrom(); held != seqs[0] {
		t.Fatalf("held from %d, want %d", held, seqs[0])
	}

	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: seqs[0], SelectiveBitmap: []byte{0b0000_0001}})
	if held, _ := s.HeldFrom(); held != seqs[2] {
		t.Fatalf("held from %d after acks, want %d", held, seqs[2])
	}
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: seqs[4]})
	flush(t, s)

	alarms := appendN(t, s, alarm, 4)
	if held, _ := s.HeldFrom(); held != alarms[2] {
		t.Fatalf("held from %d with the oldest alarms dropped, want %d", held, alarms[2])
	}
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: alarms[3]})
	if held, _ := s.HeldFrom(); held != 0 {
		t.Fatalf("held from %d with everything acknowledged", held)
	}
}

func TestSurvivesRestart(t *testing.T) {
	path := tempDB(t)
	s := open(t, path, store.Options{FlushInterval: time.Hour})
	appendN(t, s, telemetry, 3)
	appendN(t, s, alarm, 1)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	r := open(t, path, store.Options{NewEpoch: func() uint32 { return 1000 }})
	if r.Epoch() != 7 {
		t.Fatalf("epoch after reopen = %d, want 7", r.Epoch())
	}
	items := takeAll(t, r)
	if len(items) != 4 || !bytes.Equal(items[1].Payload, []byte("LANE_TELEMETRY-0")) {
		t.Fatalf("items after reopen = %v", items)
	}
	r.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: 4})
	flush(t, r)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	again := open(t, path, store.Options{})
	next := appendN(t, again, telemetry, 1)
	if next[0] != 5 {
		t.Fatalf("seq after everything was acknowledged = %d, want 5", next[0])
	}
}

func TestStatusKeepsLatestPerKey(t *testing.T) {
	s := open(t, tempDB(t), store.Options{FlushInterval: time.Hour})
	ctx := context.Background()
	for i, key := range []string{"caps/A", "caps/B", "caps/A", "caps/A"} {
		if _, err := s.Append(ctx, store.Item{Lane: status, Key: key, Payload: fmt.Appendf(nil, "%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	flush(t, s)
	items := takeAll(t, s)
	if len(items) != 2 || string(items[0].Payload) != "1" || string(items[1].Payload) != "3" {
		t.Fatalf("status items = %v", items)
	}
	if b := lane(t, s, status); b.Items != 2 || b.Dropped != 0 {
		t.Fatalf("status backlog = %+v", b)
	}
}

func TestCountQuotaEvictsLowestRankFirst(t *testing.T) {
	lanes := store.DefaultLanes()
	lanes[alarm] = store.LanePolicy{MaxItems: 3, Sync: true}
	s := open(t, tempDB(t), store.Options{Lanes: lanes})
	ctx := context.Background()
	for i, rank := range []int{1, 0, 1, 1, 0} {
		if _, err := s.Append(ctx, store.Item{Lane: alarm, Rank: rank, Payload: fmt.Appendf(nil, "%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	var kept []string
	for _, it := range takeAll(t, s) {
		kept = append(kept, string(it.Payload))
	}
	if !slices.Equal(kept, []string{"0", "2", "3"}) {
		t.Fatalf("kept = %v, want the three rank-1 alarms", kept)
	}
	if b := lane(t, s, alarm); b.Items != 3 || b.Dropped != 2 {
		t.Fatalf("alarm backlog = %+v", b)
	}
}

func TestTelemetryThinsOldestHalf(t *testing.T) {
	lanes := store.DefaultLanes()
	lanes[telemetry] = store.LanePolicy{MaxBytes: 80 * 10, Overflow: store.Thin}
	s := open(t, tempDB(t), store.Options{Lanes: lanes, FlushInterval: time.Hour, FlushItems: 1000})
	ctx := context.Background()
	for i := range 100 {
		if _, err := s.Append(ctx, store.Item{Lane: telemetry, Payload: bytes.Repeat([]byte{byte(i)}, 10)}); err != nil {
			t.Fatal(err)
		}
	}
	flush(t, s)
	b := lane(t, s, telemetry)
	if b.Bytes > 800 || b.Items != 75 || b.Dropped != 25 {
		t.Fatalf("telemetry backlog = %+v, want every other of the oldest 50 thinned", b)
	}
	items := takeAll(t, s)
	last := items[len(items)-1].Payload[0]
	if last != 99 {
		t.Fatalf("newest sample was thinned (last = %d)", last)
	}
	newest := 0
	for _, it := range items {
		if it.Payload[0] >= 50 {
			newest++
		}
	}
	if newest != 50 {
		t.Fatalf("thinning touched the newest half: %d of 50 left", newest)
	}
}

func TestBulkDropsOldest(t *testing.T) {
	lanes := store.DefaultLanes()
	lanes[bulk] = store.LanePolicy{MaxBytes: 30}
	s := open(t, tempDB(t), store.Options{Lanes: lanes, FlushInterval: time.Hour})
	ctx := context.Background()
	for i := range 5 {
		if _, err := s.Append(ctx, store.Item{Lane: bulk, Payload: bytes.Repeat([]byte{byte(i)}, 10)}); err != nil {
			t.Fatal(err)
		}
	}
	flush(t, s)
	var firsts []byte
	for _, it := range takeAll(t, s) {
		firsts = append(firsts, it.Payload[0])
	}
	if !bytes.Equal(firsts, []byte{2, 3, 4}) {
		t.Fatalf("bulk kept %v, want the newest three", firsts)
	}
}

func TestRetryAndUnmark(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := open(t, tempDB(t), store.Options{Now: func() time.Time { return now }})
	seqs := appendN(t, s, alarm, 2)
	takeAll(t, s)
	if s.InFlight() != 2 {
		t.Fatalf("in flight = %d", s.InFlight())
	}
	s.Unmark(s.Epoch(), seqs[1])
	if got := seqsOf(takeAll(t, s)); !slices.Equal(got, seqs[1:]) {
		t.Fatalf("after unmark = %v", got)
	}
	if n := s.Retry(time.Second); n != 0 {
		t.Fatalf("retried %d fresh items", n)
	}
	now = now.Add(2 * time.Second)
	if n := s.Retry(time.Second); n != 2 {
		t.Fatalf("retried %d, want 2", n)
	}
	if got := seqsOf(takeAll(t, s)); !slices.Equal(got, seqs) {
		t.Fatalf("after retry = %v", got)
	}
}

func TestAdvanceEpoch(t *testing.T) {
	path := tempDB(t)
	s := open(t, path, store.Options{})
	seqs := appendN(t, s, alarm, 2)
	takeAll(t, s)
	if err := s.AdvanceEpoch(5); err != nil {
		t.Fatal(err)
	}
	if s.Epoch() != 7 {
		t.Fatalf("epoch moved backwards to %d", s.Epoch())
	}
	if err := s.AdvanceEpoch(42); err != nil {
		t.Fatal(err)
	}
	if got := seqsOf(takeAll(t, s)); !slices.Equal(got, seqs) {
		t.Fatalf("items after epoch change = %v", got)
	}
	s.Ack(&edgelinkv3.Ack{Epoch: 7, CumulativeSeq: 10})
	s.Release()
	if got := takeAll(t, s); len(got) != 2 {
		t.Fatalf("ack of the old epoch dropped items")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if r := open(t, path, store.Options{}); r.Epoch() != 42 {
		t.Fatalf("epoch after reopen = %d", r.Epoch())
	}
}

func TestDownlinkTracking(t *testing.T) {
	path := tempDB(t)
	s := open(t, path, store.Options{})
	id := func(epoch, seq uint32) *edgelinkv3.MessageId {
		return &edgelinkv3.MessageId{Epoch: epoch, Seq: seq}
	}
	for _, seq := range []uint32{1, 2, 4, 6} {
		if err := s.MarkDownlink(id(3, seq)); err != nil {
			t.Fatal(err)
		}
	}
	a := s.DownlinkAck()
	if a.Epoch != 3 || a.CumulativeSeq != 2 || !bytes.Equal(a.SelectiveBitmap, []byte{0b1010}) {
		t.Fatalf("downlink ack = %v", a)
	}
	if !s.SeenDownlink(id(3, 4)) || s.SeenDownlink(id(3, 3)) || !s.SeenDownlink(id(2, 9)) || s.SeenDownlink(id(4, 1)) {
		t.Fatal("SeenDownlink is wrong")
	}
	if err := s.MarkDownlink(id(3, 3)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	r := open(t, path, store.Options{})
	a = r.DownlinkAck()
	if a.CumulativeSeq != 4 || !bytes.Equal(a.SelectiveBitmap, []byte{0b10}) {
		t.Fatalf("downlink ack after reopen = %v", a)
	}
	if err := r.MarkDownlink(id(4, 1)); err != nil {
		t.Fatal(err)
	}
	if a := r.DownlinkAck(); a.Epoch != 4 || a.CumulativeSeq != 1 || len(a.SelectiveBitmap) != 0 {
		t.Fatalf("downlink ack after platform epoch change = %v", a)
	}
}

func TestRefusesUnknownLane(t *testing.T) {
	s := open(t, tempDB(t), store.Options{})
	if _, err := s.Append(context.Background(), store.Item{Lane: edgelinkv3.Lane_LANE_UNSPECIFIED}); err == nil {
		t.Fatal("append to an unconfigured lane succeeded")
	}
}

func lane(t *testing.T, s *store.Store, l store.Lane) store.LaneBacklog {
	t.Helper()
	backlog, err := s.Backlog()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range backlog {
		if b.Lane == l {
			return b
		}
	}
	t.Fatalf("no backlog for %v", l)
	return store.LaneBacklog{}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
