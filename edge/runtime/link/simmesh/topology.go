// Package simmesh is the sim-mesh link driver: an in-memory radio medium shared by the drivers of
// one process, so relay, dedup and store-and-forward can be tested without radios.
//
// The medium models what those tests need and nothing more: per-edge loss, latency and jitter,
// one MTU, a duty-cycle budget per node, the mesh kinds SINGLE_HOP, FLOOD (hop limit, duplicate
// suppression) and ROUTED (shortest path), and edges that change at runtime. Every random draw
// comes from one seeded source, in send order, so a test with a fixed seed and a fixed sequence of
// sends sees the same deliveries every run.
package simmesh

import (
	"errors"
	"fmt"
	"os"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
)

// Topology is a medium's configuration, usually loaded from YAML:
//
//	kind: FLOOD
//	hop_limit: 3
//	mtu: 48
//	seed: 7
//	region: EU868
//	nodes: [{id: a}, {id: b}, {id: c, duty_cycle: 0.1}]
//	edges:
//	  - {a: a, b: b, loss: 0.1, latency: 20ms, jitter: 5ms}
//	  - {a: b, b: c}
type Topology struct {
	// Kind is SINGLE_HOP (default), FLOOD or ROUTED.
	Kind string `yaml:"kind"`
	// HopLimit is the default number of links a frame may cross (default 3).
	HopLimit int `yaml:"hop_limit"`
	// MTU is the largest frame in bytes (default 255).
	MTU int `yaml:"mtu"`
	// Bitrate turns frame size into airtime for the duty cycle, in bit/s (default 5470).
	Bitrate int64 `yaml:"bitrate"`
	Seed    int64 `yaml:"seed"`
	// Region picks a duty cycle: EU868 is 1 % per hour. DutyCycle overrides it.
	Region     string        `yaml:"region"`
	DutyCycle  float64       `yaml:"duty_cycle"`
	DutyWindow time.Duration `yaml:"duty_window"`
	Nodes      []NodeSpec    `yaml:"nodes"`
	Edges      []Edge        `yaml:"edges"`
}

// NodeSpec is one radio on the medium.
type NodeSpec struct {
	ID string `yaml:"id"`
	// DutyCycle overrides the medium's duty cycle for this node.
	DutyCycle *float64 `yaml:"duty_cycle"`
}

// Edge is an undirected radio link between two nodes.
type Edge struct {
	A       string        `yaml:"a"`
	B       string        `yaml:"b"`
	Loss    float64       `yaml:"loss"`
	Latency time.Duration `yaml:"latency"`
	Jitter  time.Duration `yaml:"jitter"`
}

var regionDutyCycle = map[string]float64{
	"EU868": 0.01,
}

// ParseTopology reads a topology from YAML and validates it.
func ParseTopology(data []byte) (Topology, error) {
	var t Topology
	if err := yaml.Unmarshal(data, &t); err != nil {
		return Topology{}, fmt.Errorf("simmesh: topology: %w", err)
	}
	if _, err := t.normalized(); err != nil {
		return Topology{}, err
	}
	return t, nil
}

// LoadTopology reads a topology file.
func LoadTopology(path string) (Topology, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Topology{}, fmt.Errorf("simmesh: topology: %w", err)
	}
	return ParseTopology(data)
}

type settings struct {
	kind     link.MeshKind
	hopLimit int
	mtu      int
	bitrate  int64
	duty     float64
	window   time.Duration
}

func (t Topology) normalized() (settings, error) {
	s := settings{kind: link.MeshSingleHop, hopLimit: 3, mtu: 255, bitrate: 5470, window: time.Hour}
	var errs []error
	if t.Kind != "" {
		k, err := link.ParseMeshKind(t.Kind)
		switch {
		case err != nil:
			errs = append(errs, err)
		case k != link.MeshSingleHop && k != link.MeshFlood && k != link.MeshRouted:
			errs = append(errs, fmt.Errorf("kind %s is not simulated", k))
		default:
			s.kind = k
		}
	}
	if t.HopLimit < 0 || t.MTU < 0 || t.Bitrate < 0 || t.DutyWindow < 0 {
		errs = append(errs, errors.New("hop_limit, mtu, bitrate and duty_window must not be negative"))
	}
	if t.HopLimit > 0 {
		s.hopLimit = t.HopLimit
	}
	if t.MTU > 0 {
		s.mtu = t.MTU
	}
	if t.Bitrate > 0 {
		s.bitrate = t.Bitrate
	}
	if t.DutyWindow > 0 {
		s.window = t.DutyWindow
	}
	if t.Region != "" {
		d, ok := regionDutyCycle[t.Region]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown region %q", t.Region))
		}
		s.duty = d
	}
	if t.DutyCycle != 0 {
		s.duty = t.DutyCycle
	}
	if !validFraction(s.duty) {
		errs = append(errs, fmt.Errorf("duty_cycle %v is not in [0, 1]", s.duty))
	}
	ids := map[string]bool{}
	for i, n := range t.Nodes {
		switch {
		case n.ID == "":
			errs = append(errs, fmt.Errorf("nodes[%d]: id is required", i))
		case ids[n.ID]:
			errs = append(errs, fmt.Errorf("nodes[%d]: duplicate id %q", i, n.ID))
		}
		ids[n.ID] = true
		if n.DutyCycle != nil && !validFraction(*n.DutyCycle) {
			errs = append(errs, fmt.Errorf("node %q: duty_cycle %v is not in [0, 1]", n.ID, *n.DutyCycle))
		}
	}
	for i, e := range t.Edges {
		if err := e.validate(ids); err != nil {
			errs = append(errs, fmt.Errorf("edges[%d]: %w", i, err))
		}
	}
	if len(errs) > 0 {
		return settings{}, fmt.Errorf("simmesh: topology: %w", errors.Join(errs...))
	}
	return s, nil
}

func (e Edge) validate(nodes map[string]bool) error {
	switch {
	case !nodes[e.A] || !nodes[e.B]:
		return fmt.Errorf("unknown node in %q-%q", e.A, e.B)
	case e.A == e.B:
		return fmt.Errorf("edge %q-%q joins a node to itself", e.A, e.B)
	case !validFraction(e.Loss):
		return fmt.Errorf("loss %v is not in [0, 1]", e.Loss)
	case e.Latency < 0 || e.Jitter < 0:
		return errors.New("latency and jitter must not be negative")
	}
	return nil
}

func validFraction(f float64) bool { return f >= 0 && f <= 1 }
