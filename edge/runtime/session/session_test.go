package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	_ "github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link/ipsession"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/session"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

const nodeID = "node-1"

type node struct {
	sess  *session.Session
	store *store.Store
	stop  func()
}

type nodeOpts struct {
	handler session.Handler
	rto     time.Duration
	epoch   uint32
}

func startNode(t *testing.T, path string, p *platform, o nodeOpts) *node {
	t.Helper()
	if o.epoch == 0 {
		o.epoch = 7
	}
	st, err := store.Open(path, store.Options{FlushInterval: 10 * time.Millisecond, NewEpoch: func() uint32 { return o.epoch }})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := link.ParseConfig([]byte(`
links:
  - id: lte
    driver: ip-session
    endpoint: ` + p.addr + `
    insecure: true
    min_backoff: 20ms
    max_backoff: 100ms
`))
	if err != nil {
		t.Fatal(err)
	}
	drivers, err := link.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := session.New(session.Config{
		NodeID:            nodeID,
		AssetSNs:          []string{"SIM-1"},
		LinkKind:          "ip-session",
		RuntimeVersion:    "test",
		Store:             st,
		Driver:            drivers[0],
		Handler:           o.handler,
		RetransmitTimeout: o.rto,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sess.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("Run = %v", err)
			}
			if err := st.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return &node{sess: sess, store: st, stop: stop}
}

func sample(i int) *edgelinkv3.Envelope {
	return &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_Sample{Sample: &telemetryv3.TelemetrySample{
		Asset:          &commonv3.AssetRef{Sn: "SIM-1"},
		BatteryPercent: ptr(float64(i)),
	}}}
}

func alert(code string) *edgelinkv3.Envelope {
	return &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_Alert{Alert: &telemetryv3.Alert{
		Asset: &commonv3.AssetRef{Sn: "SIM-1"}, Severity: telemetryv3.AlertSeverity_ALERT_SEVERITY_CRITICAL, Code: code,
	}}}
}

func commandEvent(id string) *edgelinkv3.Envelope {
	return &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_CommandEvent{CommandEvent: &capabilityv3.CommandEvent{
		CommandExecutionId: id, State: capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED,
	}}}
}

func capabilities() *edgelinkv3.Envelope {
	return &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_Capabilities{Capabilities: &capabilityv3.CapabilitySet{AssetSn: "SIM-1"}}}
}

func detections() *edgelinkv3.Envelope {
	return &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_Detections{Detections: &telemetryv3.DetectionBatch{}}}
}

func ptr[T any](v T) *T { return &v }

func publish(t *testing.T, n *node, envs ...*edgelinkv3.Envelope) []*edgelinkv3.MessageId {
	t.Helper()
	var ids []*edgelinkv3.MessageId
	for _, env := range envs {
		id, err := n.sess.Publish(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func received(p *platform) int {
	var n int
	p.read(func(p *platform) { n = len(p.got) })
	return n
}

func backlogEmpty(t *testing.T, st *store.Store) bool {
	backlog, err := st.Backlog()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range backlog {
		if b.Items > 0 {
			return false
		}
	}
	return true
}

func TestDeliversAndDropsAcknowledged(t *testing.T) {
	p := startPlatform(t)
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{})
	for i := range 20 {
		publish(t, n, sample(i))
	}
	publish(t, n, alert("dock.rain"), alert("motor.fault"))

	waitFor(t, "22 envelopes", func() bool { return received(p) == 22 })
	waitFor(t, "an empty backlog", func() bool { return backlogEmpty(t, n.store) })

	p.read(func(p *platform) {
		h := p.hellos[0]
		if h.GetNodeId() != nodeID || h.GetLinkKind() != "ip-session" || h.GetWallclock() == nil || len(h.GetAssetSns()) != 1 {
			t.Errorf("hello = %v", h)
		}
		for _, env := range p.order {
			if env.GetNodeId() != nodeID || env.GetId().GetEpoch() != 7 {
				t.Errorf("envelope %v", env)
			}
		}
		if p.dupes != 0 {
			t.Errorf("%d duplicates", p.dupes)
		}
	})
	if n.sess.SessionID() != "s1" || !n.sess.Connected() {
		t.Errorf("session %q connected=%v", n.sess.SessionID(), n.sess.Connected())
	}
}

func TestResumesAfterStreamLoss(t *testing.T) {
	p := startPlatform(t)
	p.set(func(p *platform) { p.holdAcks = true })
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{})
	for i := range 10 {
		publish(t, n, sample(i))
	}
	waitFor(t, "10 envelopes", func() bool { return received(p) == 10 })
	if backlogEmpty(t, n.store) {
		t.Fatal("unacknowledged envelopes were dropped")
	}

	lost := time.Now()
	p.killStreams()
	waitFor(t, "the second session", func() bool {
		var s int
		p.read(func(p *platform) { s = p.sessions })
		return s == 2
	})
	waitFor(t, "resume_from to clear the backlog", func() bool { return backlogEmpty(t, n.store) })
	if took := time.Since(lost); took > 10*time.Second {
		t.Errorf("resumed after %v", took)
	}

	p.set(func(p *platform) { p.holdAcks = false })
	waitFor(t, "a connected session", n.sess.Connected)
	for i := range 5 {
		publish(t, n, sample(100+i))
	}
	waitFor(t, "15 envelopes", func() bool { return received(p) == 15 })
	p.read(func(p *platform) {
		if p.dupes != 0 {
			t.Errorf("%d duplicates after resume", p.dupes)
		}
	})
}

func TestNoLossAcrossRestart(t *testing.T) {
	p := startPlatform(t)
	p.set(func(p *platform) { p.blackhole = true })
	path := filepath.Join(t.TempDir(), "node.db")
	first := startNode(t, path, p, nodeOpts{})
	var ids []*edgelinkv3.MessageId
	for i := range 30 {
		env := sample(i)
		if i%10 == 0 {
			env = alert("a")
		}
		ids = append(ids, publish(t, first, env)...)
	}
	waitFor(t, "the first transmission", func() bool {
		var f int
		p.read(func(p *platform) { f = p.frames })
		return f >= 30
	})
	first.stop()

	p.set(func(p *platform) { p.blackhole = false })
	second := startNode(t, path, p, nodeOpts{epoch: 999})
	waitFor(t, "30 envelopes after restart", func() bool { return received(p) == 30 })
	waitFor(t, "an empty backlog", func() bool { return backlogEmpty(t, second.store) })
	p.read(func(p *platform) {
		for _, id := range ids {
			if _, ok := p.got[msgKey{id.GetEpoch(), id.GetSeq()}]; !ok {
				t.Errorf("lost %v", id)
			}
		}
	})
}

func TestDrainsLaneByLane(t *testing.T) {
	p := startPlatform(t)
	p.set(func(p *platform) { p.reject = true })
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{})
	publish(t, n,
		sample(1), detections(), capabilities(), alert("a1"), commandEvent("c1"),
		sample(2), alert("a2"), detections(), commandEvent("c2"), sample(3),
	)
	if err := n.store.Flush(); err != nil {
		t.Fatal(err)
	}
	p.set(func(p *platform) { p.reject = false })
	waitFor(t, "10 envelopes", func() bool { return received(p) == 10 })
	waitFor(t, "selective acks to clear the backlog", func() bool { return backlogEmpty(t, n.store) })

	p.read(func(p *platform) {
		var lastLane edgelinkv3.Lane
		lastSeq := map[edgelinkv3.Lane]uint32{}
		for _, env := range p.order {
			if env.GetLane() < lastLane {
				t.Errorf("%v sent after %v", env.GetLane(), lastLane)
			}
			if env.GetId().GetSeq() <= lastSeq[env.GetLane()] {
				t.Errorf("%v out of order within its lane", env.GetId())
			}
			lastLane, lastSeq[env.GetLane()] = env.GetLane(), env.GetId().GetSeq()
		}
		if p.order[0].GetLane() != edgelinkv3.Lane_LANE_ALARM || p.order[9].GetLane() != edgelinkv3.Lane_LANE_BULK {
			t.Errorf("first %v, last %v", p.order[0].GetLane(), p.order[9].GetLane())
		}
	})
}

func TestClockOffsetAndUnsynced(t *testing.T) {
	p := startPlatform(t)
	p.set(func(p *platform) {
		p.reject = true
		p.offsetMs = time.Hour.Milliseconds()
	})
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{})
	early := publish(t, n, sample(1))[0]
	p.set(func(p *platform) { p.reject = false })
	waitFor(t, "a connected session", n.sess.Connected)
	late := publish(t, n, sample(2))[0]
	waitFor(t, "2 envelopes", func() bool { return received(p) == 2 })

	now := time.Now()
	p.read(func(p *platform) {
		e := p.got[msgKey{early.GetEpoch(), early.GetSeq()}]
		if !e.GetClockUnsynced() || e.GetDeviceTime().AsTime().Sub(now).Abs() > time.Minute {
			t.Errorf("envelope before sync: unsynced=%v time=%v", e.GetClockUnsynced(), e.GetDeviceTime().AsTime())
		}
		l := p.got[msgKey{late.GetEpoch(), late.GetSeq()}]
		if l.GetClockUnsynced() || l.GetDeviceTime().AsTime().Sub(now.Add(time.Hour)).Abs() > time.Minute {
			t.Errorf("envelope after sync: unsynced=%v time=%v", l.GetClockUnsynced(), l.GetDeviceTime().AsTime())
		}
	})
}

func TestResendsUnacknowledgedAfterTimeout(t *testing.T) {
	p := startPlatform(t)
	p.set(func(p *platform) { p.holdAcks = true })
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{rto: 100 * time.Millisecond})
	publish(t, n, alert("a"), alert("b"), alert("c"))
	waitFor(t, "3 envelopes", func() bool { return received(p) == 3 })
	p.set(func(p *platform) { p.holdAcks = false })
	waitFor(t, "an empty backlog", func() bool { return backlogEmpty(t, n.store) })
	p.read(func(p *platform) {
		if p.dupes == 0 {
			t.Error("nothing was resent")
		}
	})
}

func TestStartsAboveThePlatformsEpoch(t *testing.T) {
	p := startPlatform(t)
	p.set(func(p *platform) {
		p.epoch = 1_000_000
		p.got[msgKey{1_000_000, 1}] = &edgelinkv3.Envelope{}
	})
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{})
	publish(t, n, sample(1), sample(2))
	waitFor(t, "3 envelopes", func() bool { return received(p) == 3 })
	if got := n.store.Epoch(); got != 1_000_001 {
		t.Fatalf("store epoch = %d", got)
	}
	p.read(func(p *platform) {
		for _, env := range p.order {
			if env.GetId().GetEpoch() != 1_000_001 {
				t.Errorf("envelope sent under epoch %d", env.GetId().GetEpoch())
			}
		}
	})
}

func TestDownlinkAtMostOnce(t *testing.T) {
	p := startPlatform(t)
	path := filepath.Join(t.TempDir(), "node.db")
	var mu sync.Mutex
	calls := map[string]int{}
	failNext := map[string]bool{"cmd-3": true}
	handler := func(_ context.Context, f *edgelinkv3.DownlinkFrame) error {
		mu.Lock()
		defer mu.Unlock()
		id := f.GetCommand().GetCommandExecutionId()
		if failNext[id] {
			delete(failNext, id)
			return errors.New("not now")
		}
		calls[id]++
		return nil
	}
	count := func(id string) int {
		mu.Lock()
		defer mu.Unlock()
		return calls[id]
	}
	command := func(seq uint32, id string) *edgelinkv3.DownlinkFrame {
		return &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Command{Command: &edgelinkv3.CommandDelivery{
			Id: &edgelinkv3.MessageId{Epoch: 9, Seq: seq}, CommandExecutionId: id,
		}}}
	}
	lastAck := func() *edgelinkv3.Ack {
		var a *edgelinkv3.Ack
		p.read(func(p *platform) {
			if len(p.dlAcks) > 0 {
				a = p.dlAcks[len(p.dlAcks)-1]
			}
		})
		return a
	}

	n := startNode(t, path, p, nodeOpts{handler: handler})
	waitFor(t, "a connected session", n.sess.Connected)
	p.sendQ <- command(1, "cmd-1")
	p.sendQ <- command(1, "cmd-1")
	p.sendQ <- command(2, "cmd-2")
	p.sendQ <- command(3, "cmd-3")
	waitFor(t, "downlink ack 2", func() bool {
		a := lastAck()
		return a != nil && a.GetEpoch() == 9 && a.GetCumulativeSeq() == 2
	})
	p.sendQ <- command(3, "cmd-3")
	waitFor(t, "downlink ack 3", func() bool { a := lastAck(); return a != nil && a.GetCumulativeSeq() == 3 })
	if count("cmd-1") != 1 || count("cmd-2") != 1 || count("cmd-3") != 1 {
		t.Fatalf("handler calls = %v", calls)
	}
	n.stop()

	restarted := startNode(t, path, p, nodeOpts{handler: handler})
	waitFor(t, "a connected session", restarted.sess.Connected)
	p.read(func(p *platform) {
		if a := p.hellos[len(p.hellos)-1].GetDownlinkAcked(); a.GetEpoch() != 9 || a.GetCumulativeSeq() != 3 {
			t.Errorf("hello after restart reports downlink %v", a)
		}
	})
	acks := func() int {
		var n int
		p.read(func(p *platform) { n = len(p.dlAcks) })
		return n
	}
	before := acks()
	p.sendQ <- command(2, "cmd-2")
	waitFor(t, "the duplicate to be acknowledged", func() bool { return acks() > before })
	if count("cmd-2") != 1 {
		t.Fatalf("cmd-2 ran %d times", count("cmd-2"))
	}
}
