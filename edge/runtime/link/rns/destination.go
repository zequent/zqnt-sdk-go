package rns

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Destination is a named endpoint: app name and aspects, plus the identity for SINGLE destinations.
type Destination struct {
	Identity *Identity
	Type     byte
	App      string
	Aspects  []string
	Hash     []byte
	NameHash []byte
}

// ExpandName joins app name and aspects with dots, as Reticulum names destinations.
func ExpandName(app string, aspects ...string) (string, error) {
	if strings.Contains(app, ".") {
		return "", errors.New("rns: dots can't be used in app names")
	}
	name := app
	for _, a := range aspects {
		if strings.Contains(a, ".") {
			return "", errors.New("rns: dots can't be used in aspects")
		}
		name += "." + a
	}
	return name, nil
}

// NameHash is the first 10 bytes of SHA-256 over the expanded name.
func NameHash(app string, aspects ...string) ([]byte, error) {
	name, err := ExpandName(app, aspects...)
	if err != nil {
		return nil, err
	}
	return fullHash([]byte(name))[:NameHashLength], nil
}

// DestinationHash is the truncated SHA-256 over the name hash and, when given, the identity hash.
func DestinationHash(identityHash []byte, app string, aspects ...string) ([]byte, error) {
	nameHash, err := NameHash(app, aspects...)
	if err != nil {
		return nil, err
	}
	return truncatedHash(nameHash, identityHash), nil
}

// NewSingle creates a SINGLE destination for an identity.
func NewSingle(id *Identity, app string, aspects ...string) (*Destination, error) {
	if id == nil {
		return nil, errors.New("rns: a SINGLE destination needs an identity")
	}
	nameHash, err := NameHash(app, aspects...)
	if err != nil {
		return nil, err
	}
	return &Destination{
		Identity: id,
		Type:     DestSingle,
		App:      app,
		Aspects:  append([]string{}, aspects...),
		Hash:     truncatedHash(nameHash, id.Hash()),
		NameHash: nameHash,
	}, nil
}

// Announce builds an announce packet: public key, name hash, a random hash (5 random bytes and the
// emission time in seconds as 5 big-endian bytes), the signature and optional app data. Ratchets
// are not announced yet.
func (d *Destination) Announce(appData []byte, pathResponse bool) (*Packet, error) {
	if d.Type != DestSingle || d.Identity == nil || d.Identity.encPrivate == nil {
		return nil, errors.New("rns: only own SINGLE destinations can be announced")
	}
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	var now [8]byte
	binary.BigEndian.PutUint64(now[:], uint64(time.Now().Unix()))
	randomHash := append(random, now[3:]...)
	publicKey := d.Identity.PublicKey()

	signed := bytes.Join([][]byte{d.Hash, publicKey, d.NameHash, randomHash, appData}, nil)
	signature, err := d.Identity.Sign(signed)
	if err != nil {
		return nil, err
	}
	data := bytes.Join([][]byte{publicKey, d.NameHash, randomHash, signature, appData}, nil)
	ctx := ContextNone
	if pathResponse {
		ctx = ContextPathResponse
	}
	return &Packet{
		HeaderType:      Header1,
		TransportType:   TransportBroadcast,
		DestType:        DestSingle,
		Type:            PacketAnnounce,
		DestinationHash: d.Hash,
		Context:         ctx,
		Data:            data,
	}, nil
}

// Announce is a validated announce.
type Announce struct {
	DestinationHash []byte
	Identity        *Identity
	NameHash        []byte
	RandomHash      []byte
	// Ratchet is the destination's current ratchet public key, when it announced one; packets to it
	// are encrypted for the ratchet.
	Ratchet []byte
	AppData []byte
	// EmittedAt is the emission time carried in the random hash.
	EmittedAt time.Time
}

// ParseAnnounce validates an announce packet: the signature over destination hash, public key, name
// hash, random hash, ratchet and app data, and that the destination hash matches name hash and
// identity.
func ParseAnnounce(p *Packet) (*Announce, error) {
	if p.Type != PacketAnnounce {
		return nil, errors.New("rns: not an announce")
	}
	fixed := KeySize + NameHashLength + 10 + SignatureLength
	if p.ContextFlag {
		fixed += RatchetSize
	}
	if len(p.Data) < fixed {
		return nil, fmt.Errorf("rns: announce of %d bytes is shorter than %d", len(p.Data), fixed)
	}
	d := p.Data
	a := &Announce{DestinationHash: append([]byte{}, p.DestinationHash...)}
	publicKey := d[:KeySize]
	a.NameHash = append([]byte{}, d[KeySize:KeySize+NameHashLength]...)
	a.RandomHash = append([]byte{}, d[KeySize+NameHashLength:KeySize+NameHashLength+10]...)
	at := KeySize + NameHashLength + 10
	if p.ContextFlag {
		a.Ratchet = append([]byte{}, d[at:at+RatchetSize]...)
		at += RatchetSize
	}
	signature := d[at : at+SignatureLength]
	if len(d) > at+SignatureLength {
		a.AppData = append([]byte{}, d[at+SignatureLength:]...)
	}
	id, err := IdentityFromPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	signed := bytes.Join([][]byte{p.DestinationHash, publicKey, a.NameHash, a.RandomHash, a.Ratchet, a.AppData}, nil)
	if !id.Verify(signature, signed) {
		return nil, errors.New("rns: announce signature is invalid")
	}
	if !bytes.Equal(truncatedHash(a.NameHash, id.Hash()), p.DestinationHash) {
		return nil, errors.New("rns: announce destination hash does not match its identity")
	}
	a.Identity = id
	var emitted [8]byte
	copy(emitted[3:], a.RandomHash[5:10])
	a.EmittedAt = time.Unix(int64(binary.BigEndian.Uint64(emitted[:])), 0)
	return a, nil
}
