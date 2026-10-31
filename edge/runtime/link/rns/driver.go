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

func init() {
	link.Register(DriverID, func(config map[string]any) (link.Driver, error) { return NewDriver(config) })
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
	address  string
	identity *Identity
	dest     *Destination
	interval time.Duration

	mu        sync.Mutex
	cancel    context.CancelFunc
	transport *Transport
	frames    chan link.Frame
}

// NewDriver builds the driver from its configuration.
func NewDriver(config map[string]any) (*Driver, error) {
	address, _ := config["address"].(string)
	if address == "" {
		return nil, errors.New("rns: address is required")
	}
	identityPath, _ := config["identity"].(string)
	if identityPath == "" {
		return nil, errors.New("rns: identity is required")
	}
	id, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		return nil, err
	}
	app := "zequent"
	if v, ok := config["app"].(string); ok && v != "" {
		app = v
	}
	aspects := []string{"edge", "link"}
	if v, ok := config["aspects"].([]string); ok && len(v) > 0 {
		aspects = v
	} else if v, ok := config["aspects"].([]any); ok && len(v) > 0 {
		aspects = aspects[:0]
		for _, a := range v {
			aspects = append(aspects, fmt.Sprint(a))
		}
	}
	interval := 5 * time.Minute
	if v, ok := config["announce_interval"].(string); ok && v != "" {
		if interval, err = time.ParseDuration(v); err != nil {
			return nil, fmt.Errorf("rns: announce_interval: %w", err)
		}
	}
	dest, err := NewSingle(id, app, aspects...)
	if err != nil {
		return nil, err
	}
	return &Driver{address: address, identity: id, dest: dest, interval: interval, frames: make(chan link.Frame, 256)}, nil
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

func (d *Driver) ID() string { return DriverID }

func (d *Driver) Caps() link.Caps {
	return link.Caps{MaxPayload: EncryptedMDU, Bidirectional: true, LinkSecurity: true}
}

// Address is this node's own destination hash, which peers send to.
func (d *Driver) Address() string { return hex.EncodeToString(d.dest.Hash) }

// Open connects, announces the node's destination on every connect and at the interval, and starts
// delivering received frames.
func (d *Driver) Open(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		return errors.New("rns: driver already open")
	}
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	client := NewTCPClient(d.address)
	t := NewTransport(client)
	t.Register(d.dest)
	d.transport = t
	go client.Run(ctx)
	go t.Run(ctx)
	go d.announceLoop(ctx, client)
	go d.deliver(ctx, t)
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
				_ = d.transport.Announce(d.dest, nil)
			}
		case <-ticker.C:
			_ = d.transport.Announce(d.dest, nil)
		}
	}
}

func (d *Driver) deliver(ctx context.Context, t *Transport) {
	for r := range t.Received() {
		select {
		case d.frames <- link.Frame{Payload: r.Data, Hops: r.Hops, ReceivedAt: r.At}:
		case <-ctx.Done():
			return
		}
	}
}

func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	return nil
}

// Send encrypts the frame for the destination hash (hex) and sends it. A destination this node has
// not heard announce yet is UnknownDestination.
func (d *Driver) Send(_ context.Context, destination string, payload []byte) link.SendResult {
	hash, err := hex.DecodeString(strings.TrimSpace(destination))
	if err != nil || len(hash) != TruncatedHashLength {
		return link.SendResult{Status: link.UnknownDestination, Reason: "destination must be a 16-byte hash in hex"}
	}
	if len(payload) > EncryptedMDU {
		return link.SendResult{Status: link.TooLarge, Reason: fmt.Sprintf("%d bytes, a packet carries %d", len(payload), EncryptedMDU)}
	}
	d.mu.Lock()
	t := d.transport
	d.mu.Unlock()
	if t == nil {
		return link.SendResult{Status: link.Down, Reason: "driver not open"}
	}
	switch err := t.Send(hash, payload); {
	case err == nil:
		return link.SendResult{Status: link.Sent}
	case errors.Is(err, ErrUnknownDestination):
		return link.SendResult{Status: link.UnknownDestination, Reason: err.Error()}
	default:
		return link.SendResult{Status: link.Down, Reason: err.Error()}
	}
}

func (d *Driver) Receive() <-chan link.Frame { return d.frames }

// Transport exposes the driver's transport, for announces and paths.
func (d *Driver) Transport() *Transport {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.transport
}
