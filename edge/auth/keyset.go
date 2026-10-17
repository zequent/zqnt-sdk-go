package auth

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	keySetRefreshInterval  = 5 * time.Minute
	keySetOnDemandSpacing  = 30 * time.Second
	keySetFetchTimeout     = 5 * time.Second
	keySetInitialBackoff   = time.Second
	keySetMaxBackoff       = 30 * time.Second
	keySetMaxResponseBytes = 1 << 20
)

// Keys resolves the public key a token is verified with from the token's kid header ("" when the
// token has none).
type Keys interface {
	Key(kid string) (ed25519.PublicKey, error)
}

// StaticKey verifies tokens without kid with one configured key (ZQNT_PLATFORM_PUBLIC_KEY).
type StaticKey struct {
	key ed25519.PublicKey
}

// Key implements [Keys].
func (s StaticKey) Key(kid string) (ed25519.PublicKey, error) {
	if kid != "" {
		return nil, fmt.Errorf("token is signed with key %s, but ZQNT_PLATFORM_KEYS_URL is not configured", kid)
	}
	return s.key, nil
}

// KeySet verifies tokens with the platform's published signing keys (core's JWK set at
// /api/admin-console/auth/service-keys), so the platform can rotate them. A token without kid is
// verified with the static key when one is configured, else with the set's key without kid.
type KeySet struct {
	url    string
	static ed25519.PublicKey
	client *http.Client
	log    *slog.Logger

	now              func() time.Time
	refreshInterval  time.Duration
	onDemandSpacing  time.Duration
	initialBackoff   time.Duration
	maxBackoff       time.Duration
	fetchMu          sync.Mutex
	lastFetchAttempt time.Time

	mu         sync.RWMutex
	byKid      map[string]ed25519.PublicKey
	withoutKid ed25519.PublicKey

	stop     chan struct{}
	stopOnce sync.Once
}

// NewKeySet returns a key set fetched from url. static may be nil. Call [KeySet.Start] to load it
// in the background and keep it fresh, and [KeySet.Close] to stop that.
func NewKeySet(url string, static ed25519.PublicKey, log *slog.Logger) *KeySet {
	if log == nil {
		log = slog.Default()
	}
	return &KeySet{
		url:             url,
		static:          static,
		client:          &http.Client{Timeout: keySetFetchTimeout},
		log:             log,
		now:             time.Now,
		refreshInterval: keySetRefreshInterval,
		onDemandSpacing: keySetOnDemandSpacing,
		initialBackoff:  keySetInitialBackoff,
		maxBackoff:      keySetMaxBackoff,
		byKid:           map[string]ed25519.PublicKey{},
		stop:            make(chan struct{}),
	}
}

// Start loads the set in the background, retrying until the platform answers, then refreshes it
// every five minutes.
func (k *KeySet) Start() {
	go k.run()
}

// Close stops the background refresh.
func (k *KeySet) Close() {
	k.stopOnce.Do(func() { close(k.stop) })
}

// Key implements [Keys]. A kid the set does not know triggers a refresh, at most every 30 seconds.
func (k *KeySet) Key(kid string) (ed25519.PublicKey, error) {
	if kid == "" && k.static != nil {
		return k.static, nil
	}
	if key := k.lookup(kid); key != nil {
		return key, nil
	}
	k.refreshIfDue()
	if key := k.lookup(kid); key != nil {
		return key, nil
	}
	if kid == "" {
		return nil, errors.New("token names no signing key and the platform publishes none without kid")
	}
	return nil, fmt.Errorf("token is signed with an unknown key %s", kid)
}

// Refresh fetches the set now. A failed fetch keeps the keys already loaded.
func (k *KeySet) Refresh() error {
	k.fetchMu.Lock()
	defer k.fetchMu.Unlock()
	return k.fetch()
}

func (k *KeySet) run() {
	backoff := k.initialBackoff
	for {
		err := k.Refresh()
		if err == nil {
			break
		}
		k.log.Warn("platform signing keys are not readable yet", "url", k.url, "error", err.Error())
		if !k.sleep(backoff) {
			return
		}
		backoff = min(2*backoff, k.maxBackoff)
	}
	for k.sleep(k.refreshInterval) {
		if err := k.Refresh(); err != nil {
			k.log.Warn("could not refresh the platform signing keys, keeping the cached ones", "url", k.url,
				"error", err.Error())
		}
	}
}

func (k *KeySet) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-k.stop:
		return false
	case <-timer.C:
		return true
	}
}

func (k *KeySet) lookup(kid string) ed25519.PublicKey {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if kid == "" {
		return k.withoutKid
	}
	return k.byKid[kid]
}

func (k *KeySet) refreshIfDue() {
	k.fetchMu.Lock()
	defer k.fetchMu.Unlock()
	if !k.lastFetchAttempt.IsZero() && k.now().Sub(k.lastFetchAttempt) < k.onDemandSpacing {
		return
	}
	if err := k.fetch(); err != nil {
		k.log.Warn("could not refresh the platform signing keys", "url", k.url, "error", err.Error())
	}
}

func (k *KeySet) fetch() error {
	k.lastFetchAttempt = k.now()
	req, err := http.NewRequest(http.MethodGet, k.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("key set answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, keySetMaxResponseBytes))
	if err != nil {
		return err
	}
	byKid, withoutKid, err := parseKeySet(body)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.byKid, k.withoutKid = byKid, withoutKid
	k.mu.Unlock()
	return nil
}

type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	X   string `json:"x"`
}

func parseKeySet(body []byte) (map[string]ed25519.PublicKey, ed25519.PublicKey, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, nil, fmt.Errorf("key set is not a JWK set: %w", err)
	}
	if set.Keys == nil {
		return nil, nil, errors.New("key set has no keys member")
	}
	byKid := map[string]ed25519.PublicKey{}
	var withoutKid ed25519.PublicKey
	for _, entry := range set.Keys {
		key, ok := entry.ed25519()
		if !ok {
			continue
		}
		if entry.Kid == "" {
			withoutKid = key
		} else {
			byKid[entry.Kid] = key
		}
	}
	return byKid, withoutKid, nil
}

func (j jwk) ed25519() (ed25519.PublicKey, bool) {
	if j.Kty != "OKP" || j.Crv != "Ed25519" || (j.Alg != "" && j.Alg != "EdDSA") || (j.Use != "" && j.Use != "sig") {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(j.X, "="))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, false
	}
	return ed25519.PublicKey(raw), true
}

// ParsePublicKey reads an Ed25519 public key given as base64 DER (SubjectPublicKeyInfo) or PEM.
func ParsePublicKey(publicKey string) (ed25519.PublicKey, error) {
	value := strings.TrimSpace(publicKey)
	var der []byte
	if block, _ := pem.Decode([]byte(value)); block != nil {
		der = block.Bytes
	} else {
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("platform public key is not base64: %w", err)
		}
		der = decoded
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("platform public key: %w", err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("platform public key is not an Ed25519 key")
	}
	return key, nil
}
