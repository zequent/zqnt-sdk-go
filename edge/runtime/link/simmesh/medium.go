package simmesh

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
)

const (
	seenCacheSize  = 4096
	airtimeEpsilon = 1e-9
)

// Stats counts what the medium did on behalf of one node.
type Stats struct {
	// Relayed counts frames the node retransmitted for others.
	Relayed uint64
	// RelayDropped counts frames the node did not retransmit because its duty cycle was spent.
	RelayDropped uint64
	// Duplicates counts copies of a frame the node had already seen.
	Duplicates uint64
	// Lost counts transmissions from the node that an edge's loss dropped.
	Lost uint64
}

// Option changes how a medium is built.
type Option func(*Medium)

// WithSeed overrides the topology's seed.
func WithSeed(seed int64) Option {
	return func(m *Medium) { m.seed = seed }
}

// WithClock sets the clock the duty-cycle budget refills by.
func WithClock(now func() time.Time) Option {
	return func(m *Medium) { m.now = now }
}

// Medium is the shared radio medium. Drivers attach to its nodes.
type Medium struct {
	mu    sync.Mutex
	cfg   settings
	seed  int64
	rng   *rand.Rand
	now   func() time.Time
	nodes map[string]*node
	edges map[pair]Edge
	frame uint64
}

type pair struct{ a, b string }

func pairOf(a, b string) pair {
	if a > b {
		a, b = b, a
	}
	return pair{a, b}
}

type node struct {
	id       string
	duty     float64
	tokens   float64
	refilled time.Time
	emcon    bool
	drv      *Driver
	seen     map[uint64]struct{}
	seenRing []uint64
	heard    map[string]time.Time
	stats    Stats
}

// NewMedium builds a medium from a topology.
func NewMedium(t Topology, opts ...Option) (*Medium, error) {
	cfg, err := t.normalized()
	if err != nil {
		return nil, err
	}
	m := &Medium{cfg: cfg, seed: t.Seed, now: time.Now, nodes: map[string]*node{}, edges: map[pair]Edge{}}
	for _, o := range opts {
		o(m)
	}
	m.rng = rand.New(rand.NewPCG(uint64(m.seed), uint64(m.seed)))
	now := m.now()
	for _, spec := range t.Nodes {
		duty := cfg.duty
		if spec.DutyCycle != nil {
			duty = *spec.DutyCycle
		}
		m.nodes[spec.ID] = &node{
			id:       spec.ID,
			duty:     duty,
			tokens:   m.budget(duty),
			refilled: now,
			seen:     map[uint64]struct{}{},
			heard:    map[string]time.Time{},
		}
	}
	for _, e := range t.Edges {
		m.edges[pairOf(e.A, e.B)] = e
	}
	return m, nil
}

// AddEdge adds or replaces the edge between e.A and e.B.
func (m *Medium) AddEdge(e Edge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make(map[string]bool, len(m.nodes))
	for id := range m.nodes {
		ids[id] = true
	}
	if err := e.validate(ids); err != nil {
		return fmt.Errorf("simmesh: %w", err)
	}
	m.edges[pairOf(e.A, e.B)] = e
	m.publishStates()
	return nil
}

// RemoveEdge cuts the link between a and b; frames already in flight still arrive.
func (m *Medium) RemoveEdge(a, b string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.edges[pairOf(a, b)]; !ok {
		return fmt.Errorf("simmesh: no edge %q-%q", a, b)
	}
	delete(m.edges, pairOf(a, b))
	m.publishStates()
	return nil
}

// SetEmcon switches emission control for a node: while on, its driver refuses to send and the
// node relays nothing.
func (m *Medium) SetEmcon(nodeID string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[nodeID]
	if !ok {
		return fmt.Errorf("simmesh: unknown node %q", nodeID)
	}
	n.emcon = on
	return nil
}

// Stats returns the counters of one node.
func (m *Medium) Stats(nodeID string) Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.nodes[nodeID]; ok {
		return n.stats
	}
	return Stats{}
}

func (m *Medium) budget(duty float64) float64 {
	return duty * m.cfg.window.Seconds()
}

func (m *Medium) airtime(size int) float64 {
	return float64(size*8) / float64(m.cfg.bitrate)
}

// spend takes airtime from a node's duty-cycle budget; when it is short it returns how long the
// node has to wait.
func (m *Medium) spend(n *node, size int) (bool, time.Duration) {
	if n.duty == 0 {
		return true, 0
	}
	now := m.now()
	n.tokens = min(m.budget(n.duty), n.tokens+now.Sub(n.refilled).Seconds()*n.duty)
	n.refilled = now
	need := m.airtime(size)
	if n.tokens+airtimeEpsilon < need {
		return false, time.Duration(math.Ceil((need - n.tokens) / n.duty * float64(time.Second)))
	}
	n.tokens = max(n.tokens-need, 0)
	return true, 0
}

func (m *Medium) neighbours(id string) []string {
	var out []string
	for p := range m.edges {
		switch id {
		case p.a:
			out = append(out, p.b)
		case p.b:
			out = append(out, p.a)
		}
	}
	slices.Sort(out)
	return out
}

func (m *Medium) delay(e Edge) time.Duration {
	d := e.Latency
	if e.Jitter > 0 {
		d += time.Duration(m.rng.Int64N(int64(2*e.Jitter)+1)) - e.Jitter
	}
	return max(d, 0)
}

func (m *Medium) linkState(id string) (link.LinkState, string) {
	if len(m.neighbours(id)) == 0 {
		return link.StateDown, "isolated: no edges"
	}
	return link.StateUp, ""
}

func (m *Medium) publishStates() {
	for id, n := range m.nodes {
		if n.drv != nil {
			n.drv.state.Publish(m.linkState(id))
		}
	}
}

func (n *node) markSeen(frame uint64) bool {
	if _, dup := n.seen[frame]; dup {
		return false
	}
	if len(n.seenRing) == seenCacheSize {
		delete(n.seen, n.seenRing[0])
		n.seenRing = n.seenRing[1:]
	}
	n.seen[frame] = struct{}{}
	n.seenRing = append(n.seenRing, frame)
	return true
}

type transmission struct {
	frame   uint64
	from    string
	to      string
	payload []byte
	limit   int
}

type arrival struct {
	at   time.Duration
	node string
	via  string
	hops int
}

type arrivals []arrival

func (q arrivals) Len() int { return len(q) }
func (q arrivals) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	if q[i].node != q[j].node {
		return q[i].node < q[j].node
	}
	return q[i].via < q[j].via
}
func (q arrivals) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *arrivals) Push(x any)   { *q = append(*q, x.(arrival)) }
func (q *arrivals) Pop() any {
	old := *q
	a := old[len(old)-1]
	*q = old[:len(old)-1]
	return a
}

// broadcastFrom puts one transmission of node id on the air: every neighbour with an open driver
// hears it after the edge's delay unless the edge loses it.
func (m *Medium) broadcastFrom(id string, at time.Duration, hops int, q *arrivals) {
	for _, nb := range m.neighbours(id) {
		if m.nodes[nb].drv == nil {
			continue
		}
		e := m.edges[pairOf(id, nb)]
		if m.rng.Float64() < e.Loss {
			m.nodes[id].stats.Lost++
			continue
		}
		heap.Push(q, arrival{at: at + m.delay(e), node: nb, via: id, hops: hops + 1})
	}
}

func (m *Medium) propagate(tx transmission) {
	m.nodes[tx.from].markSeen(tx.frame)
	q := &arrivals{}
	m.broadcastFrom(tx.from, 0, 0, q)
	now := m.now()
	for q.Len() > 0 {
		a := heap.Pop(q).(arrival)
		n := m.nodes[a.node]
		n.heard[a.via] = now.Add(a.at)
		if !n.markSeen(tx.frame) {
			n.stats.Duplicates++
			continue
		}
		if tx.to == "" || tx.to == a.node {
			m.deliver(n, tx, a)
		}
		if m.cfg.kind != link.MeshFlood || tx.to == a.node || a.hops >= tx.limit || n.emcon {
			continue
		}
		if ok, _ := m.spend(n, len(tx.payload)); !ok {
			n.stats.RelayDropped++
			continue
		}
		n.stats.Relayed++
		m.broadcastFrom(a.node, a.at, a.hops, q)
	}
}

// route carries a unicast frame along path, hop by hop.
func (m *Medium) route(tx transmission, path []string) {
	m.nodes[tx.from].markSeen(tx.frame)
	now := m.now()
	var at time.Duration
	for i := 1; i < len(path); i++ {
		prev, cur := m.nodes[path[i-1]], m.nodes[path[i]]
		if i > 1 {
			if prev.emcon {
				return
			}
			if ok, _ := m.spend(prev, len(tx.payload)); !ok {
				prev.stats.RelayDropped++
				return
			}
			prev.stats.Relayed++
		}
		e := m.edges[pairOf(prev.id, cur.id)]
		if m.rng.Float64() < e.Loss {
			prev.stats.Lost++
			return
		}
		at += m.delay(e)
		cur.heard[prev.id] = now.Add(at)
		cur.markSeen(tx.frame)
	}
	m.deliver(m.nodes[tx.to], tx, arrival{at: at, node: tx.to, via: path[len(path)-2], hops: len(path) - 1})
}

func (m *Medium) shortestPath(from, to string) []string {
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			path := []string{to}
			for p := prev[to]; p != ""; p = prev[p] {
				path = append(path, p)
			}
			slices.Reverse(path)
			return path
		}
		for _, nb := range m.neighbours(cur) {
			if _, visited := prev[nb]; visited || m.nodes[nb].drv == nil {
				continue
			}
			prev[nb] = cur
			queue = append(queue, nb)
		}
	}
	return nil
}

func (m *Medium) deliver(n *node, tx transmission, a arrival) {
	d := n.drv
	rx := link.RxFrame{Payload: slices.Clone(tx.payload), From: tx.from, Via: a.via, Hops: a.hops}
	if a.at == 0 {
		d.receive(rx)
		return
	}
	time.AfterFunc(a.at, func() { d.receive(rx) })
}
