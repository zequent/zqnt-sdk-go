package simmesh

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
)

// DriverID is the id the driver registers under.
const DriverID = "sim-mesh"

const rxBuffer = 1024

func init() {
	link.Register(DriverID, func(cfg link.LinkConfig) (link.Driver, error) {
		name, err := cfg.RequiredString("medium")
		if err != nil {
			return nil, err
		}
		nodeID, err := cfg.RequiredString("node")
		if err != nil {
			return nil, err
		}
		m, ok := lookupMedium(name)
		if !ok {
			return nil, fmt.Errorf("no medium %q registered", name)
		}
		return m.Driver(cfg.ID, nodeID)
	})
}

var (
	mediaMu sync.Mutex
	media   = map[string]*Medium{}
)

// RegisterMedium makes a medium available to the registry's sim-mesh links under name (their
// "medium" option). The returned function removes it again.
func RegisterMedium(name string, m *Medium) (unregister func(), err error) {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	if _, taken := media[name]; taken {
		return nil, fmt.Errorf("simmesh: medium %q already registered", name)
	}
	media[name] = m
	return func() {
		mediaMu.Lock()
		defer mediaMu.Unlock()
		if media[name] == m {
			delete(media, name)
		}
	}, nil
}

func lookupMedium(name string) (*Medium, bool) {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	m, ok := media[name]
	return m, ok
}

// Driver is one node's radio on a medium. Addresses are node ids; an empty destination
// broadcasts (flooded on FLOOD meshes, to neighbours only otherwise).
type Driver struct {
	m      *Medium
	linkID string
	node   string
	rx     chan link.RxFrame
	state  *link.StateFeed

	mu      sync.Mutex
	open    bool
	closed  bool
	metrics link.Metrics
}

// Driver builds the (unopened) driver of one node; linkID defaults to the node id.
func (m *Medium) Driver(linkID, nodeID string) (*Driver, error) {
	m.mu.Lock()
	_, ok := m.nodes[nodeID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("simmesh: unknown node %q", nodeID)
	}
	if linkID == "" {
		linkID = nodeID
	}
	return &Driver{
		m:      m,
		linkID: linkID,
		node:   nodeID,
		rx:     make(chan link.RxFrame, rxBuffer),
		state:  link.NewStateFeed(link.StateDown, "not open"),
	}, nil
}

func (d *Driver) ID() string { return d.linkID }

// Node is the driver's node id, its address on the medium.
func (d *Driver) Node() string { return d.node }

func (d *Driver) Caps() link.Caps {
	m := d.m
	m.mu.Lock()
	defer m.mu.Unlock()
	var rtt time.Duration
	for _, nb := range m.neighbours(d.node) {
		rtt = max(rtt, 2*m.edges[pairOf(d.node, nb)].Latency)
	}
	return link.Caps{
		MaxPayload:    m.cfg.mtu,
		Bidirectional: true,
		UpBps:         m.cfg.bitrate,
		DownBps:       m.cfg.bitrate,
		Encodings:     []link.Encoding{link.EncodingTiny, link.EncodingLean},
		Security:      link.SecurityNone,
		Cost:          link.CostFree,
		DutyCycle:     m.nodes[d.node].duty,
		Emission:      link.EmissionLow,
		TypicalRTT:    rtt,
	}
}

// Open attaches the driver to its node; one open driver per node.
func (d *Driver) Open(context.Context) error {
	m := d.m
	m.mu.Lock()
	defer m.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.closed:
		return errors.New("simmesh: driver closed")
	case d.open:
		return errors.New("simmesh: driver already open")
	}
	n := m.nodes[d.node]
	if n.drv != nil {
		return fmt.Errorf("simmesh: node %q already has an open driver", d.node)
	}
	n.drv = d
	d.open = true
	d.state.Publish(m.linkState(d.node))
	return nil
}

func (d *Driver) Close() error {
	m := d.m
	m.mu.Lock()
	if n := m.nodes[d.node]; n.drv == d {
		n.drv = nil
	}
	m.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		d.open = false
		close(d.rx)
		d.state.Close()
	}
	return nil
}

func (d *Driver) Send(_ context.Context, frame []byte, meta link.SendMeta) link.SendResult {
	m := d.m
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.nodes[d.node]
	if n.drv != d {
		return link.SendResult{Status: link.Down, Reason: "driver not open"}
	}
	if len(frame) > m.cfg.mtu {
		return link.SendResult{Status: link.TooLarge, Reason: fmt.Sprintf("%d bytes, the mesh carries %d", len(frame), m.cfg.mtu)}
	}
	if n.emcon {
		return link.SendResult{Status: link.EmconBlocked, Reason: "emission control is on"}
	}
	if meta.To == d.node {
		return link.SendResult{Status: link.UnknownDestination, Reason: "destination is this node"}
	}
	if _, ok := m.nodes[meta.To]; meta.To != "" && !ok {
		return link.SendResult{Status: link.UnknownDestination, Reason: fmt.Sprintf("no node %q on the medium", meta.To)}
	}
	if state, reason := m.linkState(d.node); state == link.StateDown {
		return link.SendResult{Status: link.Down, Reason: reason}
	}
	tx := transmission{from: d.node, to: meta.To, payload: slices.Clone(frame), limit: m.cfg.hopLimit}
	if meta.HopLimit > 0 {
		tx.limit = meta.HopLimit
	}
	routed := m.cfg.kind == link.MeshRouted && meta.To != ""
	var path []string
	if routed {
		if path = m.shortestPath(tx.from, tx.to); path == nil || len(path)-1 > tx.limit {
			return link.SendResult{Status: link.UnknownDestination, Reason: fmt.Sprintf("no route to %q within %d hops", tx.to, tx.limit)}
		}
	}
	if ok, wait := m.spend(n, len(frame)); !ok {
		return link.SendResult{Status: link.DutyCycleExceeded, Reason: "duty-cycle budget spent", RetryAfter: wait}
	}
	m.frame++
	tx.frame = m.frame
	if routed {
		m.route(tx, path)
	} else {
		m.propagate(tx)
	}
	d.mu.Lock()
	d.metrics.FramesSent++
	d.metrics.BytesSent += uint64(len(frame))
	d.mu.Unlock()
	return link.SendResult{Status: link.Sent}
}

func (d *Driver) receive(rx link.RxFrame) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	rx.ReceivedAt = time.Now()
	select {
	case d.rx <- rx:
		d.metrics.FramesReceived++
		d.metrics.BytesReceived += uint64(len(rx.Payload))
		d.metrics.LastRx = rx.ReceivedAt
	default:
		d.metrics.FramesDropped++
	}
}

func (d *Driver) Receive() <-chan link.RxFrame { return d.rx }

func (d *Driver) Metrics() link.Metrics {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.metrics
}

func (d *Driver) State() <-chan link.StateChange { return d.state.C() }

func (d *Driver) MeshInfo() link.MeshInfo {
	return link.MeshInfo{Kind: d.m.cfg.kind, MaxHops: d.m.cfg.hopLimit, MeshName: DriverID}
}

// Neighbors lists the nodes with an edge to this one, in id order.
func (d *Driver) Neighbors() []link.Neighbor {
	m := d.m
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.nodes[d.node]
	var out []link.Neighbor
	for _, nb := range m.neighbours(d.node) {
		quality := 1 - m.edges[pairOf(d.node, nb)].Loss
		hops := 1
		out = append(out, link.Neighbor{PeerID: nb, LinkQuality: &quality, LastHeard: n.heard[nb], Hops: &hops})
	}
	return out
}
