// Package tiny implements the tiny frame format of zqnt-api spec/tiny-frame-v1.md: sealed frames
// of at most 48 bytes for LoRa, ExpressLRS and satellite links, keyed by a pre-provisioned 256-bit
// root key per node.
package tiny

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// RootKeySize is the size of K_root.
const RootKeySize = 32

const (
	labelSalt = "zqnt-edge-tiny-v1"
	labelKey  = "zqnt-edge-tiny-v1 key"
	labelTag  = "zqnt-edge-tiny-v1 tag"
)

// Suite is the one-byte AEAD id of the tiny header (spec §3).
type Suite uint8

const (
	SuiteChaCha20Poly1305 Suite = 0x01
	SuiteAES256GCM        Suite = 0x02
)

func (s Suite) valid() bool {
	return s == SuiteChaCha20Poly1305 || s == SuiteAES256GCM
}

// TimeBase is second 0 of frame times and day 0 of epochs: 2025-01-01T00:00:00Z.
var TimeBase = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// EpochAt is the recommended epoch for t: whole days since TimeBase.
func EpochAt(t time.Time) uint16 {
	if t.Before(TimeBase) {
		return 0
	}
	return uint16(t.Sub(TimeBase) / (24 * time.Hour))
}

// EpochKey holds what a sender or receiver needs for one node, suite and epoch.
type EpochKey struct {
	Suite   Suite
	Epoch   uint16
	NodeTag uint32
	aead    cipher.AEAD
}

// DeriveEpoch computes K_epoch and the node tag from the node's root key (spec §4).
func DeriveEpoch(root []byte, suite Suite, epoch uint16) (*EpochKey, error) {
	if len(root) != RootKeySize {
		return nil, fmt.Errorf("tiny: root key must be %d bytes, got %d", RootKeySize, len(root))
	}
	if !suite.valid() {
		return nil, fmt.Errorf("tiny: unsupported suite 0x%02x", uint8(suite))
	}
	prk, err := hkdf.Extract(sha256.New, root, []byte(labelSalt))
	if err != nil {
		return nil, err
	}
	key, err := hkdf.Expand(sha256.New, prk, labelKey+string([]byte{byte(suite)})+string(u16(epoch)), 32)
	if err != nil {
		return nil, err
	}
	tag, err := nodeTag(prk, epoch)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(suite, key)
	clear(key)
	clear(prk)
	if err != nil {
		return nil, err
	}
	return &EpochKey{Suite: suite, Epoch: epoch, NodeTag: tag, aead: aead}, nil
}

// NodeTag is the pseudonym a node with this root key carries in every frame header of an epoch.
func NodeTag(root []byte, epoch uint16) (uint32, error) {
	if len(root) != RootKeySize {
		return 0, fmt.Errorf("tiny: root key must be %d bytes, got %d", RootKeySize, len(root))
	}
	prk, err := hkdf.Extract(sha256.New, root, []byte(labelSalt))
	if err != nil {
		return 0, err
	}
	defer clear(prk)
	return nodeTag(prk, epoch)
}

func nodeTag(prk []byte, epoch uint16) (uint32, error) {
	tag, err := hkdf.Expand(sha256.New, prk, labelTag+string(u16(epoch)), 4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(tag), nil
}

func newAEAD(suite Suite, key []byte) (cipher.AEAD, error) {
	switch suite {
	case SuiteChaCha20Poly1305:
		return chacha20poly1305.New(key)
	case SuiteAES256GCM:
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	}
	return nil, errors.New("tiny: unsupported suite")
}

func u16(v uint16) []byte {
	return binary.BigEndian.AppendUint16(nil, v)
}
