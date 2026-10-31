package rns

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Interface carries packets to and from one Reticulum interface.
type Interface interface {
	Send(packet []byte) error
	Incoming() <-chan []byte
}

// Received is a packet delivered to a local destination, decrypted.
type Received struct {
	Destination *Destination
	Data        []byte
	Hops        int
	At          time.Time
}

// Path is what an announce taught about reaching a destination.
type Path struct {
	Announce *Announce
	// Hops counts this node's own hop, as Reticulum does when it receives.
	Hops int
	// NextHop is the transport node to address when the destination is more than one hop away.
	NextHop   []byte
	LearnedAt time.Time
}

// Transport is a minimal Reticulum transport for one interface: it announces local destinations,
// learns paths from announces, encrypts packets to known destinations and decrypts packets for local
// ones. It does not forward for others, answer path requests or handle links and resources.
type Transport struct {
	iface Interface

	mu        sync.RWMutex
	local     map[string]*Destination
	paths     map[string]*Path
	seen      map[string]struct{}
	received  chan Received
	announces chan *Announce
}

const seenLimit = 4096

// NewTransport attaches a transport to an interface; Run starts it.
func NewTransport(iface Interface) *Transport {
	return &Transport{
		iface:     iface,
		local:     map[string]*Destination{},
		paths:     map[string]*Path{},
		seen:      map[string]struct{}{},
		received:  make(chan Received, 256),
		announces: make(chan *Announce, 64),
	}
}

// Received delivers decrypted packets for local destinations.
func (t *Transport) Received() <-chan Received { return t.received }

// Announces delivers every valid announce heard.
func (t *Transport) Announces() <-chan *Announce { return t.announces }

// Register makes a local destination receive packets.
func (t *Transport) Register(d *Destination) {
	t.mu.Lock()
	t.local[hex.EncodeToString(d.Hash)] = d
	t.mu.Unlock()
}

// Announce sends an announce for a local destination.
func (t *Transport) Announce(d *Destination, appData []byte) error {
	p, err := d.Announce(appData, false)
	if err != nil {
		return err
	}
	return t.send(p)
}

// PathTo returns what is known about reaching a destination.
func (t *Transport) PathTo(destinationHash []byte) (*Path, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.paths[hex.EncodeToString(destinationHash)]
	return p, ok
}

// ErrUnknownDestination means no announce for the destination has been heard yet.
var ErrUnknownDestination = errors.New("rns: destination has not been announced")

// Send encrypts data for a destination learned from an announce and sends it, through the next hop
// when the destination is further than one hop.
func (t *Transport) Send(destinationHash, data []byte) error {
	if len(data) > EncryptedMDU {
		return fmt.Errorf("rns: %d bytes exceed the %d a packet carries", len(data), EncryptedMDU)
	}
	path, ok := t.PathTo(destinationHash)
	if !ok {
		return ErrUnknownDestination
	}
	ciphertext, err := path.Announce.Identity.Encrypt(data, path.Announce.Ratchet)
	if err != nil {
		return err
	}
	p := &Packet{
		HeaderType:      Header1,
		TransportType:   TransportBroadcast,
		DestType:        DestSingle,
		Type:            PacketData,
		DestinationHash: destinationHash,
		Context:         ContextNone,
		Data:            ciphertext,
	}
	if path.Hops > 1 && len(path.NextHop) == TruncatedHashLength {
		p.HeaderType = Header2
		p.TransportType = TransportTransport
		p.TransportID = path.NextHop
	}
	return t.send(p)
}

func (t *Transport) send(p *Packet) error {
	raw, err := p.Pack()
	if err != nil {
		return err
	}
	if h, err := p.Hash(); err == nil {
		t.remember(h)
	}
	return t.iface.Send(raw)
}

// Run processes incoming packets until ctx ends or the interface closes.
func (t *Transport) Run(ctx context.Context) {
	defer close(t.received)
	defer close(t.announces)
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-t.iface.Incoming():
			if !ok {
				return
			}
			t.inbound(raw)
		}
	}
}

func (t *Transport) inbound(raw []byte) {
	p, err := Unpack(raw)
	if err != nil {
		return
	}
	p.Hops++
	h, err := p.Hash()
	if err != nil || !t.remember(h) {
		return
	}
	switch p.Type {
	case PacketAnnounce:
		a, err := ParseAnnounce(p)
		if err != nil {
			return
		}
		key := hex.EncodeToString(a.DestinationHash)
		t.mu.Lock()
		if _, own := t.local[key]; own {
			t.mu.Unlock()
			return
		}
		if prev, ok := t.paths[key]; ok && a.EmittedAt.Before(prev.Announce.EmittedAt) {
			t.mu.Unlock()
			return
		}
		path := &Path{Announce: a, Hops: int(p.Hops), LearnedAt: time.Now()}
		if p.HeaderType == Header2 {
			path.NextHop = p.TransportID
		}
		t.paths[key] = path
		t.mu.Unlock()
		select {
		case t.announces <- a:
		default:
		}
	case PacketData:
		t.mu.RLock()
		d, ok := t.local[hex.EncodeToString(p.DestinationHash)]
		t.mu.RUnlock()
		if !ok || p.DestType != DestSingle || p.Context != ContextNone {
			return
		}
		plaintext, err := d.Identity.Decrypt(p.Data)
		if err != nil {
			return
		}
		select {
		case t.received <- Received{Destination: d, Data: plaintext, Hops: int(p.Hops), At: time.Now()}:
		default:
		}
	}
}

// remember records a packet hash; false when it was seen before.
func (t *Transport) remember(hash []byte) bool {
	key := string(hash)
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, dup := t.seen[key]; dup {
		return false
	}
	if len(t.seen) >= seenLimit {
		t.seen = map[string]struct{}{}
	}
	t.seen[key] = struct{}{}
	return true
}
