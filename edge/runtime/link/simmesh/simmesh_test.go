package simmesh

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link/linktest"
)

func newMedium(t *testing.T, topology string, opts ...Option) *Medium {
	t.Helper()
	topo, err := ParseTopology([]byte(topology))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMedium(topo, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func openAll(t *testing.T, m *Medium, nodes ...string) map[string]*Driver {
	t.Helper()
	out := map[string]*Driver{}
	for _, id := range nodes {
		d, err := m.Driver("", id)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Open(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		out[id] = d
	}
	return out
}

func send(t *testing.T, d *Driver, to string, frame []byte, hopLimit int) link.SendResult {
	t.Helper()
	return d.Send(t.Context(), frame, link.SendMeta{To: to, HopLimit: hopLimit})
}

func mustSend(t *testing.T, d *Driver, to string, frame []byte, hopLimit int) {
	t.Helper()
	if r := send(t, d, to, frame, hopLimit); r.Status != link.Sent {
		t.Fatalf("%s: send: %s %s", d.Node(), r.Status, r.Reason)
	}
}

// drain takes every frame delivered so far; with zero latency delivery happens inside Send.
func drain(d *Driver) []link.RxFrame {
	var out []link.RxFrame
	for {
		select {
		case f := <-d.Receive():
			out = append(out, f)
		default:
			return out
		}
	}
}

func payloads(frames []link.RxFrame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, string(f.Payload))
	}
	return out
}

func TestConformance(t *testing.T) {
	for _, kind := range []string{"SINGLE_HOP", "FLOOD", "ROUTED"} {
		t.Run(kind, func(t *testing.T) {
			linktest.Run(t, func(t *testing.T) linktest.Pair {
				m := newMedium(t, fmt.Sprintf(`
kind: %s
nodes: [{id: a}, {id: b}, {id: c}]
edges: [{a: a, b: b, latency: 2ms, jitter: 1ms}]
`, kind))
				a, err := m.Driver("mesh-a", "a")
				if err != nil {
					t.Fatal(err)
				}
				b, err := m.Driver("mesh-b", "b")
				if err != nil {
					t.Fatal(err)
				}
				return linktest.Pair{A: a, B: b, AddrA: "a", AddrB: "b", Unknown: "nobody"}
			})
		})
	}
}

func TestLossIsReproducibleWithASeed(t *testing.T) {
	run := func(seed int64) []string {
		m := newMedium(t, `
nodes: [{id: a}, {id: b}]
edges: [{a: a, b: b, loss: 0.3}]
`, WithSeed(seed))
		d := openAll(t, m, "a", "b")
		for i := range 1000 {
			mustSend(t, d["a"], "b", fmt.Appendf(nil, "%d", i), 0)
		}
		if lost := m.Stats("a").Lost; lost == 0 {
			t.Fatal("no loss counted")
		}
		return payloads(drain(d["b"]))
	}
	first := run(42)
	if n := len(first); n < 650 || n > 750 {
		t.Fatalf("%d of 1000 frames arrived over a 30 %% loss edge", n)
	}
	if again := run(42); !slices.Equal(first, again) {
		t.Fatal("same seed, different deliveries")
	}
	if other := run(43); slices.Equal(first, other) {
		t.Fatal("different seed, same deliveries")
	}
}

func TestMTURejection(t *testing.T) {
	m := newMedium(t, `
mtu: 48
nodes: [{id: a}, {id: b}]
edges: [{a: a, b: b}]
`)
	d := openAll(t, m, "a", "b")
	if r := send(t, d["a"], "b", make([]byte, 49), 0); r.Status != link.TooLarge {
		t.Fatalf("49 bytes on a 48-byte mesh: %s", r.Status)
	}
	mustSend(t, d["a"], "b", make([]byte, 48), 0)
	if got := drain(d["b"]); len(got) != 1 || len(got[0].Payload) != 48 {
		t.Fatalf("b got %d frames", len(got))
	}
	if d["a"].Caps().MaxPayload != 48 {
		t.Fatalf("MaxPayload %d", d["a"].Caps().MaxPayload)
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestDutyCycleRefusal(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	// 1 % of 100 s is 1 s of airtime; 48 bytes at 800 bit/s take 0.48 s.
	m := newMedium(t, `
duty_cycle: 0.01
duty_window: 100s
bitrate: 800
nodes: [{id: a}, {id: b}]
edges: [{a: a, b: b}]
`, WithClock(clock.Now))
	d := openAll(t, m, "a", "b")
	frame := make([]byte, 48)
	mustSend(t, d["a"], "b", frame, 0)
	mustSend(t, d["a"], "b", frame, 0)
	r := send(t, d["a"], "b", frame, 0)
	if r.Status != link.DutyCycleExceeded {
		t.Fatalf("third frame: %s, want DUTY_CYCLE_EXCEEDED", r.Status)
	}
	if want := 44 * time.Second; r.RetryAfter < want-time.Millisecond || r.RetryAfter > want+time.Millisecond {
		t.Fatalf("RetryAfter %v, want %v", r.RetryAfter, want)
	}
	clock.Advance(r.RetryAfter)
	mustSend(t, d["a"], "b", frame, 0)
	if got := len(drain(d["b"])); got != 3 {
		t.Fatalf("b got %d frames, want 3", got)
	}
	if c := d["a"].Caps().DutyCycle; c != 0.01 {
		t.Fatalf("Caps.DutyCycle %v", c)
	}
}

func TestRelayRespectsItsOwnDutyCycle(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	m := newMedium(t, `
kind: FLOOD
bitrate: 800
duty_window: 100s
nodes: [{id: a}, {id: b, duty_cycle: 0.005}, {id: c}]
edges: [{a: a, b: b}, {a: b, b: c}]
`, WithClock(clock.Now))
	d := openAll(t, m, "a", "b", "c")
	frame := make([]byte, 48)
	mustSend(t, d["a"], "", frame, 0)
	mustSend(t, d["a"], "", frame, 0)
	if got := len(drain(d["c"])); got != 1 {
		t.Fatalf("c got %d frames, want 1 (b can relay one 0.48 s frame from 0.5 s)", got)
	}
	if s := m.Stats("b"); s.Relayed != 1 || s.RelayDropped != 1 {
		t.Fatalf("b stats %+v", s)
	}
}

const chain = `
kind: FLOOD
hop_limit: 2
nodes: [{id: a}, {id: b}, {id: c}, {id: d}, {id: e}]
edges: [{a: a, b: b}, {a: b, b: c}, {a: c, b: d}, {a: d, b: e}]
`

func TestFloodHopLimit(t *testing.T) {
	m := newMedium(t, chain)
	d := openAll(t, m, "a", "b", "c", "d", "e")
	mustSend(t, d["a"], "", []byte("x"), 0)
	for node, wantHops := range map[string]int{"b": 1, "c": 2} {
		got := drain(d[node])
		if len(got) != 1 || got[0].Hops != wantHops || got[0].From != "a" {
			t.Fatalf("%s got %+v, want one frame from a after %d hops", node, got, wantHops)
		}
	}
	for _, node := range []string{"a", "d", "e"} {
		if got := drain(d[node]); len(got) != 0 {
			t.Fatalf("%s got %d frames beyond the hop limit", node, len(got))
		}
	}
	mustSend(t, d["a"], "", []byte("y"), 4)
	got := drain(d["e"])
	if len(got) != 1 || got[0].Hops != 4 || got[0].Via != "d" {
		t.Fatalf("e got %+v with hop limit 4", got)
	}
}

func TestFloodSuppressesDuplicates(t *testing.T) {
	m := newMedium(t, `
kind: FLOOD
nodes: [{id: a}, {id: b}, {id: c}, {id: d}]
edges: [{a: a, b: b}, {a: a, b: c}, {a: b, b: d}, {a: c, b: d}]
`)
	d := openAll(t, m, "a", "b", "c", "d")
	mustSend(t, d["a"], "", []byte("x"), 0)
	if got := drain(d["d"]); len(got) != 1 || got[0].Via != "b" || got[0].Hops != 2 {
		t.Fatalf("d got %+v, want one copy via b", got)
	}
	if got := drain(d["a"]); len(got) != 0 {
		t.Fatalf("a got its own frame back %d times", len(got))
	}
	if s := m.Stats("d"); s.Duplicates != 1 || s.Relayed != 1 {
		t.Fatalf("d stats %+v, want 1 duplicate and 1 relay", s)
	}
	if s := m.Stats("a"); s.Duplicates != 2 {
		t.Fatalf("a stats %+v, want its own frame suppressed twice", s)
	}
}

func TestFloodUnicastIsDeliveredOnlyToTheAddressee(t *testing.T) {
	m := newMedium(t, chain)
	d := openAll(t, m, "a", "b", "c", "d", "e")
	mustSend(t, d["a"], "c", []byte("for c"), 0)
	if got := drain(d["c"]); len(got) != 1 {
		t.Fatalf("c got %d frames", len(got))
	}
	for _, node := range []string{"b", "d", "e"} {
		if got := drain(d[node]); len(got) != 0 {
			t.Fatalf("%s got a frame for c", node)
		}
	}
	if s := m.Stats("c"); s.Relayed != 0 {
		t.Fatal("the addressee relayed its own frame")
	}
}

func TestFloodReproducibleWithLossAndJitter(t *testing.T) {
	run := func() ([]string, Stats) {
		m := newMedium(t, `
kind: FLOOD
seed: 9
hop_limit: 4
nodes: [{id: a}, {id: b}, {id: c}, {id: d}, {id: e}]
edges:
  - {a: a, b: b, loss: 0.2, jitter: 1ms}
  - {a: a, b: c, loss: 0.2, jitter: 1ms}
  - {a: b, b: d, loss: 0.2, jitter: 1ms}
  - {a: c, b: d, loss: 0.2, jitter: 1ms}
  - {a: d, b: e, loss: 0.2, jitter: 1ms}
`)
		d := openAll(t, m, "a", "b", "c", "d", "e")
		for i := range 200 {
			mustSend(t, d["a"], "", fmt.Appendf(nil, "%03d", i), 0)
		}
		deadline := time.Now().Add(time.Second)
		var got []link.RxFrame
		for time.Now().Before(deadline) {
			got = append(got, drain(d["e"])...)
			time.Sleep(5 * time.Millisecond)
		}
		p := payloads(got)
		slices.Sort(p)
		return p, m.Stats("d")
	}
	p1, s1 := run()
	p2, s2 := run()
	if len(p1) == 0 || len(p1) == 200 {
		t.Fatalf("e got %d of 200 frames, want some lost", len(p1))
	}
	if !slices.Equal(p1, p2) || s1 != s2 {
		t.Fatalf("same seed, different runs: %d vs %d frames, %+v vs %+v", len(p1), len(p2), s1, s2)
	}
}

const diamond = `
kind: ROUTED
nodes: [{id: a}, {id: b}, {id: c}, {id: d}, {id: x}]
edges: [{a: a, b: b}, {a: b, b: c}, {a: c, b: d}, {a: a, b: x}, {a: x, b: d}]
`

func TestRoutedShortestPathAndEdgeRemoval(t *testing.T) {
	m := newMedium(t, diamond)
	d := openAll(t, m, "a", "b", "c", "d", "x")
	mustSend(t, d["a"], "d", []byte("1"), 0)
	if got := drain(d["d"]); len(got) != 1 || got[0].Via != "x" || got[0].Hops != 2 {
		t.Fatalf("d got %+v, want one frame via x after 2 hops", got)
	}
	if m.Stats("x").Relayed != 1 || m.Stats("b").Relayed != 0 {
		t.Fatal("the frame did not take the short path")
	}
	for _, node := range []string{"b", "c", "x"} {
		if got := drain(d[node]); len(got) != 0 {
			t.Fatalf("relay %s delivered a frame for d", node)
		}
	}
	if err := m.RemoveEdge("x", "d"); err != nil {
		t.Fatal(err)
	}
	mustSend(t, d["a"], "d", []byte("2"), 0)
	if got := drain(d["d"]); len(got) != 1 || got[0].Via != "c" || got[0].Hops != 3 {
		t.Fatalf("d got %+v, want one frame via c after 3 hops", got)
	}
	if r := send(t, d["a"], "d", []byte("3"), 2); r.Status != link.UnknownDestination {
		t.Fatalf("3-hop route with hop limit 2: %s", r.Status)
	}
	if err := m.RemoveEdge("b", "c"); err != nil {
		t.Fatal(err)
	}
	if r := send(t, d["a"], "d", []byte("4"), 0); r.Status != link.UnknownDestination || !strings.Contains(r.Reason, "no route") {
		t.Fatalf("partitioned: %s %s", r.Status, r.Reason)
	}
	if err := m.AddEdge(Edge{A: "x", B: "d"}); err != nil {
		t.Fatal(err)
	}
	mustSend(t, d["a"], "d", []byte("5"), 0)
	if got := drain(d["d"]); len(got) != 1 {
		t.Fatal("no delivery after the edge came back")
	}
}

func TestRoutedSkipsClosedNodes(t *testing.T) {
	m := newMedium(t, diamond)
	d := openAll(t, m, "a", "b", "c", "d", "x")
	if err := d["x"].Close(); err != nil {
		t.Fatal(err)
	}
	mustSend(t, d["a"], "d", []byte("1"), 0)
	if got := drain(d["d"]); len(got) != 1 || got[0].Via != "c" {
		t.Fatalf("d got %+v, want the long path while x is off", got)
	}
}

func TestSingleHopReachesOnlyNeighbours(t *testing.T) {
	m := newMedium(t, `
nodes: [{id: a}, {id: b}, {id: c}]
edges: [{a: a, b: b}, {a: b, b: c}]
`)
	d := openAll(t, m, "a", "b", "c")
	mustSend(t, d["a"], "c", []byte("too far"), 0)
	mustSend(t, d["a"], "", []byte("hello"), 0)
	if got := payloads(drain(d["b"])); !slices.Equal(got, []string{"hello"}) {
		t.Fatalf("b got %v", got)
	}
	if got := drain(d["c"]); len(got) != 0 {
		t.Fatalf("c got %d frames on a single-hop mesh", len(got))
	}
	if info := d["a"].MeshInfo(); info.Kind != link.MeshSingleHop || info.MeshName != DriverID {
		t.Fatalf("MeshInfo %+v", info)
	}
}

func TestLatency(t *testing.T) {
	m := newMedium(t, `
nodes: [{id: a}, {id: b}]
edges: [{a: a, b: b, latency: 30ms}]
`)
	d := openAll(t, m, "a", "b")
	start := time.Now()
	mustSend(t, d["a"], "b", []byte("x"), 0)
	if got := drain(d["b"]); len(got) != 0 {
		t.Fatal("frame arrived before the edge latency")
	}
	f := linktest.Receive(t, d["b"])
	if elapsed := f.ReceivedAt.Sub(start); elapsed < 30*time.Millisecond {
		t.Fatalf("arrived after %v", elapsed)
	}
}

func TestEdgeRemovalChangesLinkState(t *testing.T) {
	m := newMedium(t, `
nodes: [{id: a}, {id: b}]
edges: [{a: a, b: b}]
`)
	d := openAll(t, m, "a", "b")
	expectState(t, d["a"], link.StateUp)
	if err := m.RemoveEdge("a", "b"); err != nil {
		t.Fatal(err)
	}
	expectState(t, d["a"], link.StateDown)
	if r := send(t, d["a"], "b", []byte("x"), 0); r.Status != link.Down {
		t.Fatalf("isolated send: %s", r.Status)
	}
	if err := m.AddEdge(Edge{A: "b", B: "a"}); err != nil {
		t.Fatal(err)
	}
	expectState(t, d["a"], link.StateUp)
}

func expectState(t *testing.T, d *Driver, want link.LinkState) {
	t.Helper()
	select {
	case s := <-d.State():
		if s.State != want {
			t.Fatalf("state %s (%s), want %s", s.State, s.Reason, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("no state change, want %s", want)
	}
}

func TestEmcon(t *testing.T) {
	m := newMedium(t, `
kind: FLOOD
nodes: [{id: a}, {id: b}, {id: c}]
edges: [{a: a, b: b}, {a: b, b: c}]
`)
	d := openAll(t, m, "a", "b", "c")
	if err := m.SetEmcon("a", true); err != nil {
		t.Fatal(err)
	}
	if r := send(t, d["a"], "", []byte("x"), 0); r.Status != link.EmconBlocked {
		t.Fatalf("send under EMCON: %s", r.Status)
	}
	if err := m.SetEmcon("a", false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEmcon("b", true); err != nil {
		t.Fatal(err)
	}
	mustSend(t, d["a"], "", []byte("y"), 0)
	if len(drain(d["b"])) != 1 || len(drain(d["c"])) != 0 {
		t.Fatal("a node under EMCON relayed")
	}
}

func TestNeighbors(t *testing.T) {
	m := newMedium(t, `
nodes: [{id: a}, {id: b}, {id: c}]
edges: [{a: a, b: c, loss: 0.25}, {a: a, b: b}]
`)
	d := openAll(t, m, "a", "b", "c")
	mustSend(t, d["b"], "a", []byte("x"), 0)
	nbs := d["a"].Neighbors()
	if len(nbs) != 2 || nbs[0].PeerID != "b" || nbs[1].PeerID != "c" {
		t.Fatalf("neighbours %+v", nbs)
	}
	if nbs[0].LastHeard.IsZero() || !nbs[1].LastHeard.IsZero() {
		t.Fatal("LastHeard should be set for b only")
	}
	if *nbs[1].LinkQuality != 0.75 || *nbs[0].Hops != 1 {
		t.Fatalf("quality %v hops %v", *nbs[1].LinkQuality, *nbs[0].Hops)
	}
}

func TestOneOpenDriverPerNode(t *testing.T) {
	m := newMedium(t, `
nodes: [{id: a}]
`)
	openAll(t, m, "a")
	other, err := m.Driver("again", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Open(t.Context()); err == nil {
		t.Fatal("two open drivers on one node")
	}
	if _, err := m.Driver("", "ghost"); err == nil {
		t.Fatal("driver for an unknown node")
	}
}

func TestTopologyValidation(t *testing.T) {
	for name, topo := range map[string]string{
		"unknown kind":        "kind: STAR\nnodes: [{id: a}]",
		"ip mesh":             "kind: IP_MESH\nnodes: [{id: a}]",
		"unknown region":      "region: MARS\nnodes: [{id: a}]",
		"duplicate node":      "nodes: [{id: a}, {id: a}]",
		"edge to nowhere":     "nodes: [{id: a}]\nedges: [{a: a, b: z}]",
		"self edge":           "nodes: [{id: a}]\nedges: [{a: a, b: a}]",
		"loss above one":      "nodes: [{id: a}, {id: b}]\nedges: [{a: a, b: b, loss: 1.5}]",
		"negative mtu":        "mtu: -1\nnodes: [{id: a}]",
		"node duty above one": "nodes: [{id: a, duty_cycle: 2}]",
		"not yaml":            "nodes: [",
	} {
		if _, err := ParseTopology([]byte(topo)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	topo, err := ParseTopology([]byte("region: EU868\nkind: single-hop\nnodes: [{id: a}, {id: b}]\nedges: [{a: a, b: b, latency: 20ms}]"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMedium(topo)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := m.Driver("", "a")
	if c := a.Caps(); c.DutyCycle != 0.01 || c.TypicalRTT != 40*time.Millisecond {
		t.Fatalf("caps %+v", c)
	}
	if err := m.AddEdge(Edge{A: "a", B: "nobody"}); err == nil {
		t.Fatal("AddEdge accepted an unknown node")
	}
	if err := m.RemoveEdge("a", "nobody"); err == nil {
		t.Fatal("RemoveEdge accepted a missing edge")
	}
}

func TestNodeConfigBuildsSimMeshLinks(t *testing.T) {
	m := newMedium(t, `
kind: FLOOD
nodes: [{id: n1}, {id: n2}]
edges: [{a: n1, b: n2}]
`)
	unregister, err := RegisterMedium("field-a", m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregister)
	if _, err := RegisterMedium("field-a", m); err == nil {
		t.Fatal("medium registered twice")
	}
	build := func(node string) link.Driver {
		cfg, err := link.ParseConfig(fmt.Appendf(nil, `
node_id: edge-1
links:
  - id: lora
    driver: sim-mesh
    medium: field-a
    node: %s
`, node))
		if err != nil {
			t.Fatal(err)
		}
		drivers, err := link.Build(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := drivers[0].Open(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = drivers[0].Close() })
		return drivers[0]
	}
	a, b := build("n1"), build("n2")
	if a.ID() != "lora" {
		t.Fatalf("ID %q", a.ID())
	}
	if r := a.Send(t.Context(), []byte("hi"), link.SendMeta{To: "n2"}); r.Status != link.Sent {
		t.Fatalf("send: %s", r.Status)
	}
	if f := linktest.Receive(t, b); !bytes.Equal(f.Payload, []byte("hi")) {
		t.Fatalf("got %q", f.Payload)
	}

	for name, opts := range map[string]map[string]any{
		"unknown medium": {"medium": "nowhere", "node": "n1"},
		"missing node":   {"medium": "field-a"},
		"unknown node":   {"medium": "field-a", "node": "ghost"},
		"node not text":  {"medium": "field-a", "node": 7},
	} {
		if _, err := link.New(link.LinkConfig{ID: "x", Driver: DriverID, Options: opts}); err == nil {
			t.Errorf("%s: built", name)
		}
	}
}
