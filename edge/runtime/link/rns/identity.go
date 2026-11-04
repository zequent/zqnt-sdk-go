package rns

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	// KeySize is the size of a public (or private) identity key: X25519 then Ed25519, 32 bytes each.
	KeySize = 64
	// TruncatedHashLength is the length of identity and destination hashes in bytes.
	TruncatedHashLength = 16
	// NameHashLength is the length of a destination's name hash in bytes.
	NameHashLength = 10
	// SignatureLength is the length of an Ed25519 signature.
	SignatureLength = 64
	// RatchetSize is the length of an announced ratchet public key.
	RatchetSize = 32
)

// Identity is a Reticulum identity: an X25519 key for encryption and an Ed25519 key for signatures.
// A remote identity holds only the public halves.
type Identity struct {
	encPrivate *ecdh.PrivateKey
	encPublic  *ecdh.PublicKey
	sigPrivate ed25519.PrivateKey
	sigPublic  ed25519.PublicKey
	hash       []byte
}

// NewIdentity creates an identity with fresh keys.
func NewIdentity() (*Identity, error) {
	private := make([]byte, KeySize)
	if _, err := rand.Read(private); err != nil {
		return nil, err
	}
	return IdentityFromPrivateKey(private)
}

// IdentityFromPrivateKey loads an identity from its 64-byte private key (X25519 then Ed25519 seed),
// the format Reticulum stores identities in.
func IdentityFromPrivateKey(private []byte) (*Identity, error) {
	if len(private) != KeySize {
		return nil, fmt.Errorf("rns: private key must be %d bytes, not %d", KeySize, len(private))
	}
	encPrivate, err := ecdh.X25519().NewPrivateKey(private[:32])
	if err != nil {
		return nil, err
	}
	sigPrivate := ed25519.NewKeyFromSeed(private[32:])
	id := &Identity{
		encPrivate: encPrivate,
		encPublic:  encPrivate.PublicKey(),
		sigPrivate: sigPrivate,
		sigPublic:  sigPrivate.Public().(ed25519.PublicKey),
	}
	id.hash = truncatedHash(id.PublicKey())
	return id, nil
}

// IdentityFromPublicKey loads a remote identity from its 64-byte public key.
func IdentityFromPublicKey(public []byte) (*Identity, error) {
	if len(public) != KeySize {
		return nil, fmt.Errorf("rns: public key must be %d bytes, not %d", KeySize, len(public))
	}
	encPublic, err := ecdh.X25519().NewPublicKey(public[:32])
	if err != nil {
		return nil, err
	}
	id := &Identity{encPublic: encPublic, sigPublic: ed25519.PublicKey(append([]byte{}, public[32:]...))}
	id.hash = truncatedHash(id.PublicKey())
	return id, nil
}

// PrivateKey returns the 64-byte private key, or nil for a remote identity.
func (id *Identity) PrivateKey() []byte {
	if id.encPrivate == nil {
		return nil
	}
	return append(append([]byte{}, id.encPrivate.Bytes()...), id.sigPrivate.Seed()...)
}

// PublicKey returns the 64-byte public key.
func (id *Identity) PublicKey() []byte {
	return append(append([]byte{}, id.encPublic.Bytes()...), id.sigPublic...)
}

// Hash returns the 16-byte identity hash.
func (id *Identity) Hash() []byte { return append([]byte{}, id.hash...) }

// HexHash returns the identity hash in hex.
func (id *Identity) HexHash() string { return hex.EncodeToString(id.hash) }

// Sign signs a message with the identity's Ed25519 key.
func (id *Identity) Sign(message []byte) ([]byte, error) {
	if id.sigPrivate == nil {
		return nil, errors.New("rns: identity holds no private key")
	}
	return ed25519.Sign(id.sigPrivate, message), nil
}

// Verify checks an Ed25519 signature made by this identity.
func (id *Identity) Verify(signature, message []byte) bool {
	return len(signature) == SignatureLength && ed25519.Verify(id.sigPublic, message, signature)
}

// Encrypt encrypts for this identity: an ephemeral X25519 key, a key derived from the shared secret
// with the identity hash as salt, and a token. A ratchet public key, when the destination announced
// one, replaces the identity's own X25519 key as the target.
func (id *Identity) Encrypt(plaintext, ratchet []byte) ([]byte, error) {
	target := id.encPublic
	if len(ratchet) > 0 {
		r, err := ecdh.X25519().NewPublicKey(ratchet)
		if err != nil {
			return nil, err
		}
		target = r
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(target)
	if err != nil {
		return nil, err
	}
	key, err := deriveKey(shared, id.hash)
	if err != nil {
		return nil, err
	}
	token, err := tokenEncrypt(key, plaintext)
	if err != nil {
		return nil, err
	}
	return append(ephemeral.PublicKey().Bytes(), token...), nil
}

// Decrypt reverses Encrypt with the identity's own X25519 key.
func (id *Identity) Decrypt(ciphertext []byte) ([]byte, error) {
	if id.encPrivate == nil {
		return nil, errors.New("rns: identity holds no private key")
	}
	if len(ciphertext) <= 32 {
		return nil, errors.New("rns: ciphertext too short")
	}
	peer, err := ecdh.X25519().NewPublicKey(ciphertext[:32])
	if err != nil {
		return nil, err
	}
	shared, err := id.encPrivate.ECDH(peer)
	if err != nil {
		return nil, err
	}
	key, err := deriveKey(shared, id.hash)
	if err != nil {
		return nil, err
	}
	return tokenDecrypt(key, ciphertext[32:])
}
