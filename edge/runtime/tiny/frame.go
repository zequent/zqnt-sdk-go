package tiny

import (
	"encoding/binary"
	"errors"
	"fmt"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

const (
	Version      = 1
	HeaderSize   = 12
	TagSize      = 16
	MaxFrameSize = 48
	MinFrameSize = HeaderSize + 2 + TagSize
	MaxBodySize  = MaxFrameSize - MinFrameSize
)

const (
	flagAckRequested = 1 << 5
	flagReserved     = 1 << 4
	flagMore         = 1 << 3
	laneMask         = 0x07
)

// Direction separates the nonces of both directions under one epoch key.
type Direction uint8

const (
	Uplink   Direction = 0x01
	Downlink Direction = 0x02
)

var (
	ErrMalformed      = errors.New("tiny: malformed frame")
	ErrAuthentication = errors.New("tiny: frame does not authenticate")
	ErrWrongKey       = errors.New("tiny: frame is for another node, suite or epoch")
)

// Header is the clear part of a frame, authenticated as associated data.
type Header struct {
	Lane         edgelinkv3.Lane
	AckRequested bool
	More         bool
	Suite        Suite
	Epoch        uint16
	NodeTag      uint32
	Counter      uint32
}

// Message is a frame's content: header flags, counter, schema id and body.
type Message struct {
	Lane         edgelinkv3.Lane
	AckRequested bool
	More         bool
	Counter      uint32
	Schema       uint16
	Body         []byte
}

func (h Header) append(b []byte) []byte {
	first := byte(Version<<6) | byte(h.Lane)&laneMask
	if h.AckRequested {
		first |= flagAckRequested
	}
	if h.More {
		first |= flagMore
	}
	b = append(b, first, byte(h.Suite))
	b = binary.BigEndian.AppendUint16(b, h.Epoch)
	b = binary.BigEndian.AppendUint32(b, h.NodeTag)
	return binary.BigEndian.AppendUint32(b, h.Counter)
}

// ParseHeader reads and checks the clear header; what it returns is not authenticated yet.
func ParseHeader(frame []byte) (Header, error) {
	if len(frame) < MinFrameSize || len(frame) > MaxFrameSize {
		return Header{}, fmt.Errorf("%w: %d bytes, want %d..%d", ErrMalformed, len(frame), MinFrameSize, MaxFrameSize)
	}
	first := frame[0]
	if first>>6 != Version {
		return Header{}, fmt.Errorf("%w: version %d", ErrMalformed, first>>6)
	}
	if first&flagReserved != 0 {
		return Header{}, fmt.Errorf("%w: reserved flag set", ErrMalformed)
	}
	h := Header{
		Lane:         edgelinkv3.Lane(first & laneMask),
		AckRequested: first&flagAckRequested != 0,
		More:         first&flagMore != 0,
		Suite:        Suite(frame[1]),
		Epoch:        binary.BigEndian.Uint16(frame[2:4]),
		NodeTag:      binary.BigEndian.Uint32(frame[4:8]),
		Counter:      binary.BigEndian.Uint32(frame[8:12]),
	}
	if h.Lane < edgelinkv3.Lane_LANE_ALARM || h.Lane > edgelinkv3.Lane_LANE_BULK {
		return Header{}, fmt.Errorf("%w: lane %d", ErrMalformed, h.Lane)
	}
	if !h.Suite.valid() {
		return Header{}, fmt.Errorf("%w: suite 0x%02x", ErrMalformed, uint8(h.Suite))
	}
	if h.Counter == 0 {
		return Header{}, fmt.Errorf("%w: counter 0", ErrMalformed)
	}
	return h, nil
}

func nonce(dir Direction, tag, counter uint32) []byte {
	n := make([]byte, 4, 12)
	n[0] = byte(dir)
	n = binary.BigEndian.AppendUint32(n, tag)
	return binary.BigEndian.AppendUint32(n, counter)
}

// Seal builds a frame for direction dir. The counter must be fresh for this key and direction.
func (k *EpochKey) Seal(dir Direction, m Message) ([]byte, error) {
	if m.Counter == 0 {
		return nil, fmt.Errorf("%w: counter 0", ErrMalformed)
	}
	if m.Lane < edgelinkv3.Lane_LANE_ALARM || m.Lane > edgelinkv3.Lane_LANE_BULK {
		return nil, fmt.Errorf("%w: lane %d", ErrMalformed, m.Lane)
	}
	if len(m.Body) > MaxBodySize {
		return nil, fmt.Errorf("%w: body of %d bytes, at most %d", ErrMalformed, len(m.Body), MaxBodySize)
	}
	if m.Schema == 0 {
		return nil, fmt.Errorf("%w: schema 0", ErrMalformed)
	}
	h := Header{Lane: m.Lane, AckRequested: m.AckRequested, More: m.More, Suite: k.Suite, Epoch: k.Epoch,
		NodeTag: k.NodeTag, Counter: m.Counter}
	frame := h.append(make([]byte, 0, MaxFrameSize))
	plain := binary.BigEndian.AppendUint16(make([]byte, 0, 2+len(m.Body)), m.Schema)
	plain = append(plain, m.Body...)
	return k.aead.Seal(frame, nonce(dir, k.NodeTag, m.Counter), plain, frame[:HeaderSize]), nil
}

// Open authenticates and decrypts a frame received in direction dir. Replay is checked separately
// (ReplayWindow), after Open succeeded.
func (k *EpochKey) Open(dir Direction, frame []byte) (Message, error) {
	h, err := ParseHeader(frame)
	if err != nil {
		return Message{}, err
	}
	if h.Suite != k.Suite || h.Epoch != k.Epoch || h.NodeTag != k.NodeTag {
		return Message{}, ErrWrongKey
	}
	plain, err := k.aead.Open(nil, nonce(dir, h.NodeTag, h.Counter), frame[HeaderSize:], frame[:HeaderSize])
	if err != nil {
		return Message{}, ErrAuthentication
	}
	schema := binary.BigEndian.Uint16(plain[:2])
	if schema == 0 {
		return Message{}, fmt.Errorf("%w: schema 0", ErrMalformed)
	}
	return Message{Lane: h.Lane, AckRequested: h.AckRequested, More: h.More, Counter: h.Counter, Schema: schema,
		Body: plain[2:]}, nil
}
