package identity

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
)

// Provider signs the node's Hellos with its identity key. After a key rotation whose outcome the
// node did not learn, it holds both keys: it offers the new key first and the old one after a
// refused handshake, and keeps whichever key the platform accepts.
type Provider struct {
	dir   Dir
	store KeyStore

	mu      sync.Mutex
	state   *State
	active  Key
	next    Key
	useNext bool
}

type keyWithID struct {
	Key
	id []byte
}

// NewProvider loads the active key and a pending rotated key, if any, from store.
func NewProvider(dir Dir, store KeyStore, state *State) (*Provider, error) {
	p := &Provider{dir: dir, store: store, state: state}
	active, err := loadKey(store, KeyActive)
	if errors.Is(err, ErrNoKey) {
		return nil, fmt.Errorf("identity: the configured key store holds no identity key (enrolled with another key_store?): %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("identity: active key: %w", err)
	}
	next, err := loadKey(store, KeyNext)
	switch {
	case errors.Is(err, ErrNoKey):
	case err != nil:
		return nil, fmt.Errorf("identity: rotated key: %w", err)
	default:
		p.next, p.useNext = next, true
	}
	p.active = active
	if !bytes.Equal(keyID(active), state.IdentityKeyID) && (next == nil || !bytes.Equal(keyID(next), state.IdentityKeyID)) {
		return nil, fmt.Errorf("identity: the key in the key store (%x) is not the enrolled key %x", keyID(active), []byte(state.IdentityKeyID))
	}
	return p, nil
}

func loadKey(store KeyStore, name string) (Key, error) {
	k, err := store.Load(name)
	if err != nil {
		return nil, err
	}
	pub, err := PublicKeyBytes(k)
	if err != nil {
		return nil, err
	}
	return keyWithID{k, handshake.KeyID(handshake.AlgECDSAP256, pub)}, nil
}

func keyID(k Key) []byte { return k.(keyWithID).id }

// State is the node's enrollment.
func (p *Provider) State() *State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Suite is the node's crypto suite.
func (p *Provider) Suite() uint32 { return p.State().Suite }

// KeyID is the id of the key the next Hello is signed with.
func (p *Provider) KeyID() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return keyID(p.current())
}

func (p *Provider) current() Key {
	if p.useNext && p.next != nil {
		return p.next
	}
	return p.active
}

// Sign signs a transcript with the key named keyID, r || s.
func (p *Provider) Sign(id, transcript []byte) ([]byte, error) {
	p.mu.Lock()
	var k Key
	for _, c := range []Key{p.active, p.next} {
		if c != nil && bytes.Equal(keyID(c), id) {
			k = c
		}
	}
	p.mu.Unlock()
	if k == nil {
		return nil, fmt.Errorf("identity: no key %x", id)
	}
	return handshake.Sign(k, transcript)
}

// Protection is the protection class of the active key.
func (p *Provider) Protection() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current().Protection().String()
}

// Confirmed is called when the platform accepted a Hello signed with key id. A pending rotated
// key becomes the active key when it was that key, and is erased otherwise.
func (p *Provider) Confirmed(id []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next == nil {
		return nil
	}
	if bytes.Equal(keyID(p.next), id) {
		if err := p.store.Rename(KeyNext, KeyActive); err != nil {
			return err
		}
		st := *p.state
		st.IdentityKeyID = keyID(p.next)
		st.KeyProtection = p.next.Protection().String()
		if err := p.dir.SaveState(&st); err != nil {
			return err
		}
		p.state, p.active = &st, p.next
	} else if err := p.store.Delete(KeyNext); err != nil {
		return err
	}
	p.next, p.useNext = nil, false
	return nil
}

// Refused is called when a handshake failed. With a pending rotated key the next Hello uses the
// other key; when the key was rotated by another process (zqnt-edge-node rotate-key), the keys
// are loaded again.
func (p *Provider) Refused() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next != nil {
		p.useNext = !p.useNext
		return
	}
	st, err := p.dir.LoadState()
	if err != nil || bytes.Equal(st.IdentityKeyID, keyID(p.active)) {
		return
	}
	if fresh, err := NewProvider(p.dir, p.store, st); err == nil {
		p.state, p.active, p.next, p.useNext = fresh.state, fresh.active, fresh.next, fresh.useNext
	}
}
