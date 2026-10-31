// Package link is the edge runtime's link layer: drivers that carry Zequent frames over one kind of
// link (an IP session, a mesh, a radio). It holds the part of the driver contract the first drivers
// need; zqnt-sdk-go#18 extends it (metrics, link state, mesh info, neighbours) and adds sim-mesh.
package link

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Caps describes what a link can carry.
type Caps struct {
	// MaxPayload is the largest frame Send accepts, in bytes.
	MaxPayload int
	// Bidirectional is false for links that only send or only receive.
	Bidirectional bool
	// LinkSecurity is true when the link itself encrypts and authenticates frames.
	LinkSecurity bool
}

// SendStatus is what happened to a frame handed to Send.
type SendStatus int

const (
	Sent SendStatus = iota
	Queued
	TooLarge
	Down
	UnknownDestination
)

func (s SendStatus) String() string {
	switch s {
	case Sent:
		return "SENT"
	case Queued:
		return "QUEUED"
	case TooLarge:
		return "TOO_LARGE"
	case Down:
		return "DOWN"
	case UnknownDestination:
		return "UNKNOWN_DESTINATION"
	}
	return fmt.Sprintf("SendStatus(%d)", int(s))
}

// SendResult is the outcome of Send.
type SendResult struct {
	Status SendStatus
	Reason string
}

// Frame is one frame received on a link.
type Frame struct {
	Payload []byte
	// From names the sender in the driver's own address form.
	From string
	// Hops is how many relays the frame passed, when the link knows it.
	Hops       int
	ReceivedAt time.Time
}

// Driver carries frames over one link.
type Driver interface {
	ID() string
	Caps() Caps
	Open(ctx context.Context) error
	Close() error
	// Send hands one frame to the link for the destination, in the driver's own address form.
	Send(ctx context.Context, destination string, payload []byte) SendResult
	// Receive delivers frames addressed to this node until Close.
	Receive() <-chan Frame
}

// Factory builds a driver from its section of the node configuration.
type Factory func(config map[string]any) (Driver, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register makes a driver available under its id. Drivers register themselves in init.
func Register(id string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, taken := registry[id]; taken {
		panic("link: driver registered twice: " + id)
	}
	registry[id] = factory
}

// New builds the driver registered under id; an unknown id is an error.
func New(id string, config map[string]any) (Driver, error) {
	registryMu.RLock()
	factory, ok := registry[id]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("link: no driver %q (known: %v)", id, Drivers())
	}
	return factory(config)
}

// Drivers lists the registered driver ids.
func Drivers() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
