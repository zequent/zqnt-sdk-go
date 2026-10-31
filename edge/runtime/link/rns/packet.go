package rns

import (
	"errors"
	"fmt"
)

const (
	// MTU is the largest packet Reticulum sends on an interface.
	MTU = 500
	// HeaderMinSize is a HEADER_1 header: flags, hops, destination hash, context.
	HeaderMinSize = 2 + 1 + TruncatedHashLength
	// HeaderMaxSize is a HEADER_2 header, which adds the transport id.
	HeaderMaxSize = 2 + 1 + 2*TruncatedHashLength
	// MDU is the largest packet payload.
	MDU = MTU - HeaderMaxSize - 1
	// EncryptedMDU is the largest plaintext a single packet to a SINGLE destination carries.
	EncryptedMDU = (MDU-TokenOverhead-KeySize/2)/16*16 - 1
)

// Packet types.
const (
	PacketData        byte = 0x00
	PacketAnnounce    byte = 0x01
	PacketLinkRequest byte = 0x02
	PacketProof       byte = 0x03
)

// Header types.
const (
	Header1 byte = 0x00
	Header2 byte = 0x01
)

// Transport types.
const (
	TransportBroadcast byte = 0x00
	TransportTransport byte = 0x01
)

// Destination types.
const (
	DestSingle byte = 0x00
	DestGroup  byte = 0x01
	DestPlain  byte = 0x02
	DestLink   byte = 0x03
)

// Packet contexts used here; the rest of the context space is left as received.
const (
	ContextNone         byte = 0x00
	ContextPathResponse byte = 0x0B
)

// Packet is one Reticulum packet in its wire form, split into header fields and data.
type Packet struct {
	HeaderType    byte
	ContextFlag   bool
	TransportType byte
	DestType      byte
	Type          byte
	Hops          byte
	// TransportID is the next hop of a HEADER_2 packet.
	TransportID     []byte
	DestinationHash []byte
	Context         byte
	Data            []byte
}

func (p *Packet) flags() byte {
	f := p.HeaderType<<6 | p.TransportType<<4 | p.DestType<<2 | p.Type
	if p.ContextFlag {
		f |= 1 << 5
	}
	return f
}

// Pack encodes the packet; it fails above the MTU.
func (p *Packet) Pack() ([]byte, error) {
	if len(p.DestinationHash) != TruncatedHashLength {
		return nil, errors.New("rns: destination hash must be 16 bytes")
	}
	raw := []byte{p.flags(), p.Hops}
	if p.HeaderType == Header2 {
		if len(p.TransportID) != TruncatedHashLength {
			return nil, errors.New("rns: a HEADER_2 packet needs a 16-byte transport id")
		}
		raw = append(raw, p.TransportID...)
	}
	raw = append(raw, p.DestinationHash...)
	raw = append(raw, p.Context)
	raw = append(raw, p.Data...)
	if len(raw) > MTU {
		return nil, fmt.Errorf("rns: packet of %d bytes exceeds the MTU of %d", len(raw), MTU)
	}
	return raw, nil
}

// Unpack decodes a packet from its wire form.
func Unpack(raw []byte) (*Packet, error) {
	if len(raw) < HeaderMinSize {
		return nil, errors.New("rns: packet shorter than a header")
	}
	f := raw[0]
	p := &Packet{
		HeaderType:    (f & 0b01000000) >> 6,
		ContextFlag:   f&0b00100000 != 0,
		TransportType: (f & 0b00010000) >> 4,
		DestType:      (f & 0b00001100) >> 2,
		Type:          f & 0b00000011,
		Hops:          raw[1],
	}
	rest := raw[2:]
	if p.HeaderType == Header2 {
		if len(rest) < 2*TruncatedHashLength+1 {
			return nil, errors.New("rns: HEADER_2 packet too short")
		}
		p.TransportID = append([]byte{}, rest[:TruncatedHashLength]...)
		rest = rest[TruncatedHashLength:]
	}
	p.DestinationHash = append([]byte{}, rest[:TruncatedHashLength]...)
	p.Context = rest[TruncatedHashLength]
	p.Data = append([]byte{}, rest[TruncatedHashLength+1:]...)
	return p, nil
}

// Hash is the packet hash Reticulum uses for duplicate detection and proofs: SHA-256 over the low
// nibble of the flags and everything after the hops (and transport id), so it stays the same while
// the packet travels.
func (p *Packet) Hash() ([]byte, error) {
	raw, err := p.Pack()
	if err != nil {
		return nil, err
	}
	part := []byte{raw[0] & 0b00001111}
	if p.HeaderType == Header2 {
		part = append(part, raw[2+TruncatedHashLength:]...)
	} else {
		part = append(part, raw[2:]...)
	}
	return fullHash(part), nil
}
