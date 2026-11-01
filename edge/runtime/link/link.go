// Package link is the edge runtime's link layer: drivers that carry opaque frames over one kind of
// link (an IP session, a radio, a mesh), a registry that builds them from the node configuration,
// and the types the link manager and topology reports read from them.
//
// The enum values of LinkState and MeshKind match zqnt.edgelink.v3 (LinkState, MeshKind), so a
// conversion to the contract is a plain cast.
package link

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Driver carries frames over one link. Every method is safe for concurrent use.
//
// Lifecycle: a driver is opened once. Send before Open or after Close returns Down. Close is
// idempotent; once it returns, the Receive and State channels are closed.
type Driver interface {
	// ID is the link id from the node configuration (e.g. "lte", "lora").
	ID() string
	Caps() Caps
	Open(ctx context.Context) error
	Close() error
	// Send hands one frame to the link. It never blocks on the medium and never keeps frame.
	Send(ctx context.Context, frame []byte, meta SendMeta) SendResult
	// Receive delivers frames for this node.
	Receive() <-chan RxFrame
	Metrics() Metrics
	// State delivers link state changes. A slow reader misses intermediate changes but always
	// gets the latest one.
	State() <-chan StateChange
	MeshInfo() MeshInfo
	// Neighbors lists the peers the node hears on this link; empty for links without a mesh.
	Neighbors() []Neighbor
}

// SendMeta says where and how a frame goes.
type SendMeta struct {
	// To is the destination in the driver's address form; empty broadcasts where the link can.
	To string
	// HopLimit caps the links a frame crosses on a mesh; 0 is the driver's default.
	HopLimit int
}

// SendStatus is what happened to a frame handed to Send.
type SendStatus int

const (
	Sent SendStatus = iota + 1
	Queued
	TooLarge
	Down
	EmconBlocked
	DutyCycleExceeded
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
	case EmconBlocked:
		return "EMCON_BLOCKED"
	case DutyCycleExceeded:
		return "DUTY_CYCLE_EXCEEDED"
	case UnknownDestination:
		return "UNKNOWN_DESTINATION"
	}
	return fmt.Sprintf("SendStatus(%d)", int(s))
}

// SendResult is the outcome of Send.
type SendResult struct {
	Status SendStatus
	Reason string
	// RetryAfter is when sending may succeed again, for DutyCycleExceeded.
	RetryAfter time.Duration
}

// OK is true when the link took the frame.
func (r SendResult) OK() bool { return r.Status == Sent || r.Status == Queued }

// RxFrame is one frame received on a link.
type RxFrame struct {
	Payload []byte
	// From is the sender in the driver's address form; empty when the link does not carry it.
	From string
	// Via is the neighbour the frame was heard from; empty when the link does not know it.
	Via string
	// Hops is the number of links the frame crossed (1 = from a neighbour); 0 when unknown.
	Hops int
	// Signal is nil when the link reports no radio measurements.
	Signal     *Signal
	ReceivedAt time.Time
}

// Signal is the radio measurement of a received frame.
type Signal struct {
	RSSI int
	SNR  int
}

// Encoding is a frame encoding a link can carry: full, lean or tiny.
type Encoding string

const (
	EncodingFull Encoding = "full"
	EncodingLean Encoding = "lean"
	EncodingTiny Encoding = "tiny"
)

// LinkSecurity is the protection the link itself gives a frame.
type LinkSecurity int

const (
	SecurityUnspecified LinkSecurity = iota
	SecurityNone
	SecurityLinkAuth
	SecurityApprovedCrypto
)

// CostClass is what traffic on a link costs.
type CostClass int

const (
	CostUnspecified CostClass = iota
	CostFree
	CostMetered
	CostExpensive
)

// EmissionProfile is how visible the link's emissions are.
type EmissionProfile int

const (
	EmissionUnspecified EmissionProfile = iota
	EmissionLow
	EmissionMedium
	EmissionHigh
)

// Caps describes what a link can carry.
type Caps struct {
	// MaxPayload is the largest frame Send accepts, in bytes.
	MaxPayload    int
	Streaming     bool
	Bidirectional bool
	UpBps         int64
	DownBps       int64
	Encodings     []Encoding
	Security      LinkSecurity
	Cost          CostClass
	// DutyCycle is the allowed fraction of airtime, 0 when the link has no duty cycle.
	DutyCycle  float64
	Emission   EmissionProfile
	TypicalRTT time.Duration
}

// Metrics is a driver's view of its link; zero values mean unknown.
type Metrics struct {
	RTT               time.Duration
	Loss              float64
	BandwidthEstimate int64
	BytesSent         uint64
	BytesReceived     uint64
	FramesSent        uint64
	FramesReceived    uint64
	// FramesDropped counts frames the driver discarded locally (full receive buffer, relay limits).
	FramesDropped uint64
	LastRx        time.Time
	ProbeFailures int
}

// LinkState is the health of a link, as zqnt.edgelink.v3.LinkState.
type LinkState int32

const (
	StateUnspecified LinkState = 0
	StateUp          LinkState = 1
	StateDegraded    LinkState = 2
	StateDown        LinkState = 3
)

func (s LinkState) String() string {
	switch s {
	case StateUp:
		return "UP"
	case StateDegraded:
		return "DEGRADED"
	case StateDown:
		return "DOWN"
	}
	return "UNSPECIFIED"
}

// StateChange is one transition of a link's state.
type StateChange struct {
	State  LinkState
	Reason string
	At     time.Time
}

// MeshKind is how a link reaches peers beyond its neighbours, as zqnt.edgelink.v3.MeshKind.
type MeshKind int32

const (
	MeshUnspecified MeshKind = 0
	MeshNone        MeshKind = 1
	MeshIP          MeshKind = 2
	MeshFlood       MeshKind = 3
	MeshRouted      MeshKind = 4
	MeshSingleHop   MeshKind = 5
)

func (k MeshKind) String() string {
	switch k {
	case MeshNone:
		return "NONE"
	case MeshIP:
		return "IP_MESH"
	case MeshFlood:
		return "FLOOD"
	case MeshRouted:
		return "ROUTED"
	case MeshSingleHop:
		return "SINGLE_HOP"
	}
	return "UNSPECIFIED"
}

// ParseMeshKind reads a mesh kind as written in configuration ("FLOOD", "single_hop", ...).
func ParseMeshKind(s string) (MeshKind, error) {
	for k := MeshNone; k <= MeshSingleHop; k++ {
		if strings.EqualFold(k.String(), strings.ReplaceAll(s, "-", "_")) {
			return k, nil
		}
	}
	return MeshUnspecified, fmt.Errorf("link: unknown mesh kind %q", s)
}

// MeshInfo describes the mesh a link belongs to.
type MeshInfo struct {
	Kind MeshKind
	// MaxHops is 0 when unknown.
	MaxHops  int
	MeshName string
}

// Neighbor is a peer the node hears on one link. Pointer fields are nil when unknown.
type Neighbor struct {
	PeerID      string
	NodeTag     *uint32
	RSSI        *int
	SNR         *int
	LinkQuality *float64
	LastHeard   time.Time
	Hops        *int
}
