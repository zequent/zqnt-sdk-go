package rns

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
)

// DriverID is the id the driver registers under.
const DriverID = "rns"

// maxHops is Reticulum's path length limit (PATHFINDER_M).
const maxHops = 128

const rxBuffer = 256

func init() {
	link.Register(DriverID, func(cfg link.LinkConfig) (link.Driver, error) { return NewDriver(cfg) })
}

// Driver carries Zequent frames as encrypted Reticulum packets between SINGLE destinations, over a
// TCP connection to a Reticulum instance. Addresses are destination hashes in hex.
//
// Configuration:
//
//	address:  host:port of a Reticulum TCP server interface (required)
//	identity: path of a 64-byte Reticulum identity file; created when missing (required)
//	app:      app name of the node's destination (default "zequent")
//	aspects:  aspects of the node's destination (default ["edge", "link"])
//	announce_interval: Go duration between announces (default "5m")
type Driver struct {
	linkID   string
	address  string
	identity *Identity
	dest     *Destination
	interval time.Duration
	rx       chan link.RxFrame
	state    *link.StateFeed
	done     chan struct{}

	mu        sync.Mutex
	opened    bool
	closed    bool
	cancel    context.CancelFunc
	transport *Transport
	metrics   link.Metrics
}

// NewDriver builds the driver from its link configuration.
func NewDriver(cfg link.LinkConfig) (*Driver, error) {
	address, err := cfg.RequiredString("address")
	if err != nil {
		return nil, err
	}
	identityPath, err := cfg.RequiredString("identity")
	if err != nil {
		return nil, err
	}
	id, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		return nil, err
	}
	app, err := cfg.String("app", "zequent")
	if err != nil {
		return nil, err
	}
	aspects, err := aspectsOption(cfg)
	if err != nil {
		return nil, err
	}
	interval, err := cfg.Duration("announce_interval", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	dest, err := NewSingle(id, app, aspects...)
	if err != nil {
		return nil, err
	}
	linkID := cfg.ID
	if linkID == "" {
		linkID = DriverID
	}
	return &Driver{
		linkID:   linkID,
		address:  address,
		identity: id,
		dest:     dest,
		interval: interval,
		rx:       make(chan link.RxFrame, rxBuffer),
		state:    link.NewStateFeed(link.StateDown, "not open"),
		done:     make(chan struct{}),
	}, nil
}

func aspectsOption(cfg link.LinkConfig) ([]string, error) {
	switch v := cfg.Options["aspects"].(type) {
	case nil:
		return []string{"edge", "link"}, nil
	case []string:
		if len(v) == 0 {
			return nil, errors.New("aspects must not be empty")
		}
		return v, nil
	case []any:
		if len(v) == 0 {
			return nil, errors.New("aspects must not be empty")
		}
		aspects := make([]string, 0, len(v))
		for _, a := range v {
			s, ok := a.(string)
			if !ok {
				return nil, fmt.Errorf("aspects: want strings, got %T", a)
			}
			aspects = append(aspects, s)
		}
		return aspects, nil
	default:
		return nil, fmt.Errorf("aspects: want a list of strings, got %T", v)
	}
}

func loadOrCreateIdentity(path string) (*Identity, error) {
	if b, err := os.ReadFile(path); err == nil {
		return IdentityFromPrivateKey(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	id, err := NewIdentity()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, id.PrivateKey(), 0o600); err != nil {
		return nil, err
	}
	return id, nil
}

func (d *Driver) ID() string { return d.linkID }

func (d *Driver) Caps() link.Caps {
	return link.Caps{
		MaxPayload:    EncryptedMDU,
		Bidirectional: true,
		Encodings:     []link.Encoding{link.EncodingTiny, link.EncodingLean},
		Security:      link.SecurityLinkAuth,
		Cost:          link.CostFree,
	}
}

// Address is this node's own destination hash, which peers send to.
func (d *Driver) Address() string { return hex.EncodeToString(d.dest.Hash) }

// Open connects, announces the node's destination on every connect and at the interval, and starts
// delivering received frames.
func (d *Driver) Open(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.closed:
		return errors.New("rns: driver closed")
	case d.opened:
		return errors.New("rns: driver already open")
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := NewTCPClient(d.address)
	t := NewTransport(client)
	t.Register(d.dest)
	d.transport, d.cancel, d.opened = t, cancel, true
	d.state.Publish(link.StateDown, "connecting")
	go client.Run(ctx)
	go t.Run(ctx)
	go d.announceLoop(ctx, client)
	go d.deliver(t)
	return nil
}

func (d *Driver) announceLoop(ctx context.Context, client *TCPClient) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case up := <-client.Online():
			if up {
				d.state.Publish(link.StateUp, "connected")
				_ = d.transport.Announce(d.dest, nil)
			} else {
				d.state.Publish(link.StateDown, "disconnected")
			}
		case <-ticker.C:
			_ = d.transport.Announce(d.dest, nil)
		}
	}
}

func (d *Driver) deliver(t *Transport) {
	defer close(d.done)
	defer close(d.rx)
	for r := range t.Received() {
		d.receive(link.RxFrame{Payload: r.Data, Hops: r.Hops, ReceivedAt: r.At})
	}
}

func (d *Driver) receive(frame link.RxFrame) {
	d.mu.Lock()
	defer d.mu.Unlock()
	select {
	case d.rx <- frame:
		d.metrics.FramesReceived++
		d.metrics.BytesReceived += uint64(len(frame.Payload))
		d.metrics.LastRx = frame.ReceivedAt
	default:
		d.metrics.FramesDropped++
	}
}

func (d *Driver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	opened, cancel := d.opened, d.cancel
	d.mu.Unlock()
	if opened {
		cancel()
		<-d.done
	} else {
		close(d.rx)
	}
	d.state.Close()
	return nil
}

// Send encrypts the frame for the destination hash (meta.To, hex) and sends it. A destination this
// node has not heard announce yet is UnknownDestination; the driver does not broadcast.
func (d *Driver) Send(_ context.Context, frame []byte, meta link.SendMeta) link.SendResult {
	hash, err := hex.DecodeString(strings.TrimSpace(meta.To))
	if err != nil || len(hash) != TruncatedHashLength {
		return link.SendResult{Status: link.UnknownDestination, Reason: "destination must be a 16-byte hash in hex"}
	}
	if len(frame) > EncryptedMDU {
		return link.SendResult{Status: link.TooLarge, Reason: fmt.Sprintf("%d bytes, a packet carries %d", len(frame), EncryptedMDU)}
	}
	d.mu.Lock()
	t := d.transport
	if d.closed {
		t = nil
	}
	d.mu.Unlock()
	if t == nil {
		return link.SendResult{Status: link.Down, Reason: "driver not open"}
	}
	switch err := t.Send(hash, frame); {
	case err == nil:
		d.mu.Lock()
		d.metrics.FramesSent++
		d.metrics.BytesSent += uint64(len(frame))
		d.mu.Unlock()
		return link.SendResult{Status: link.Sent}
	case errors.Is(err, ErrUnknownDestination):
		return link.SendResult{Status: link.UnknownDestination, Reason: err.Error()}
	default:
		return link.SendResult{Status: link.Down, Reason: err.Error()}
	}
}

func (d *Driver) Receive() <-chan link.RxFrame { return d.rx }

func (d *Driver) State() <-chan link.StateChange { return d.state.C() }

func (d *Driver) Metrics() link.Metrics {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.metrics
}

func (d *Driver) MeshInfo() link.MeshInfo {
	return link.MeshInfo{Kind: link.MeshRouted, MaxHops: maxHops, MeshName: "reticulum"}
}

// Neighbors lists the destinations this node has heard announce, with the hop count of their path.
func (d *Driver) Neighbors() []link.Neighbor {
	t := d.Transport()
	if t == nil {
		return nil
	}
	var out []link.Neighbor
	for hash, p := range t.Paths() {
		hops := p.Hops
		out = append(out, link.Neighbor{PeerID: hash, Hops: &hops, LastHeard: p.LearnedAt})
	}
	return out
}

// Transport exposes the driver's transport, for announces and paths.
func (d *Driver) Transport() *Transport {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.transport
}
