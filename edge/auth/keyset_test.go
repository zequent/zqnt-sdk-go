package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type publishedKey struct {
	kid string
	key ed25519.PublicKey
}

type fakeKeyServer struct {
	*httptest.Server
	mu      sync.Mutex
	keys    []publishedKey
	failing bool
	fetches atomic.Int32
}

func newFakeKeyServer(t *testing.T, keys ...publishedKey) *fakeKeyServer {
	t.Helper()
	f := &fakeKeyServer{keys: keys}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.fetches.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failing {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		entries := []map[string]string{}
		for _, k := range f.keys {
			entry := map[string]string{"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "use": "sig",
				"x": base64.RawURLEncoding.EncodeToString(k.key)}
			if k.kid != "" {
				entry["kid"] = k.kid
			}
			entries = append(entries, entry)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": entries})
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeKeyServer) publish(keys ...publishedKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = keys
}

func (f *fakeKeyServer) setFailing(failing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = failing
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestKeySet(url string, static ed25519.PublicKey) (*KeySet, *fakeClock) {
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	ks := NewKeySet(url, static, nil)
	ks.now = clock.Now
	return ks, clock
}

func generate(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func signWithKid(t *testing.T, key ed25519.PrivateKey, kid string) string {
	t.Helper()
	now := time.Now()
	header := map[string]string{"alg": "EdDSA", "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	headerJSON, _ := json.Marshal(header)
	payload, _ := json.Marshal(map[string]any{
		"iss": Issuer, "sub": "svc:remote-control-service", "aud": AudienceEdge, "scope": []string{ScopePlatform},
		"org_id": "org-1", "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": "j",
	})
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(headerJSON) + "." + enc.EncodeToString(payload)
	return input + "." + enc.EncodeToString(ed25519.Sign(key, []byte(input)))
}

func TestKeySetFollowsARotation(t *testing.T) {
	v1, v1Priv := generate(t)
	v2, v2Priv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"service-token:v1", v1})
	ks, clock := newTestKeySet(server.URL, nil)
	verifier := NewKeysVerifier(ks)

	claims, err := verifier.Verify(signWithKid(t, v1Priv, "service-token:v1"))
	if err != nil || claims.OrganizationID != "org-1" {
		t.Fatalf("v1 token: %+v, %v", claims, err)
	}

	server.publish(publishedKey{"service-token:v1", v1}, publishedKey{"service-token:v2", v2})
	clock.advance(keySetOnDemandSpacing)
	if _, err := verifier.Verify(signWithKid(t, v2Priv, "service-token:v2")); err != nil {
		t.Fatalf("v2 token after the rotation: %v", err)
	}
	if _, err := verifier.Verify(signWithKid(t, v1Priv, "service-token:v1")); err != nil {
		t.Fatalf("v1 token after the rotation: %v", err)
	}
	if got := server.fetches.Load(); got != 2 {
		t.Fatalf("fetches = %d, want 2", got)
	}
}

func TestKeySetRefusesAnUnknownKid(t *testing.T) {
	v1, _ := generate(t)
	_, otherPriv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"service-token:v1", v1})
	ks, _ := newTestKeySet(server.URL, nil)

	_, err := NewKeysVerifier(ks).Verify(signWithKid(t, otherPriv, "service-token:v9"))
	if err == nil || !strings.Contains(err.Error(), "unknown key service-token:v9") {
		t.Fatalf("got %v", err)
	}
}

func TestKeySetRejectsAKnownKidWithAForeignSignature(t *testing.T) {
	v1, _ := generate(t)
	_, otherPriv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"service-token:v1", v1})
	ks, _ := newTestKeySet(server.URL, nil)

	if _, err := NewKeysVerifier(ks).Verify(signWithKid(t, otherPriv, "service-token:v1")); err == nil {
		t.Fatal("a token signed with another key was accepted")
	}
}

func TestKeySetRefreshesOnDemandAtMostEvery30Seconds(t *testing.T) {
	v1, _ := generate(t)
	_, otherPriv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"service-token:v1", v1})
	ks, clock := newTestKeySet(server.URL, nil)
	verifier := NewKeysVerifier(ks)
	unknown := signWithKid(t, otherPriv, "service-token:v7")

	for range 5 {
		_, _ = verifier.Verify(unknown)
	}
	if got := server.fetches.Load(); got != 1 {
		t.Fatalf("fetches within 30 s = %d, want 1", got)
	}
	clock.advance(keySetOnDemandSpacing - time.Second)
	_, _ = verifier.Verify(unknown)
	if got := server.fetches.Load(); got != 1 {
		t.Fatalf("fetches after 29 s = %d, want 1", got)
	}
	clock.advance(time.Second)
	_, _ = verifier.Verify(unknown)
	if got := server.fetches.Load(); got != 2 {
		t.Fatalf("fetches after 30 s = %d, want 2", got)
	}
}

func TestKeySetVerifiesTokensWithoutKidWithTheStaticKey(t *testing.T) {
	static, staticPriv := generate(t)
	server := newFakeKeyServer(t)
	ks, _ := newTestKeySet(server.URL, static)

	if _, err := NewKeysVerifier(ks).Verify(signWithKid(t, staticPriv, "")); err != nil {
		t.Fatalf("token without kid: %v", err)
	}
	if got := server.fetches.Load(); got != 0 {
		t.Fatalf("the static key must not need the set: %d fetches", got)
	}
}

func TestKeySetUsesThePublishedKeyWithoutKid(t *testing.T) {
	envKey, envPriv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"", envKey})
	ks, _ := newTestKeySet(server.URL, nil)

	if _, err := NewKeysVerifier(ks).Verify(signWithKid(t, envPriv, "")); err != nil {
		t.Fatalf("token without kid: %v", err)
	}
}

func TestKeySetRefusesATokenWithoutKidWhenNoKeyWithoutKidExists(t *testing.T) {
	v1, v1Priv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"service-token:v1", v1})
	ks, _ := newTestKeySet(server.URL, nil)

	if _, err := NewKeysVerifier(ks).Verify(signWithKid(t, v1Priv, "")); err == nil {
		t.Fatal("a token without kid was accepted without a key for it")
	}
}

func TestKeySetKeepsItsKeysWhenARefreshFails(t *testing.T) {
	v1, v1Priv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"service-token:v1", v1})
	ks, _ := newTestKeySet(server.URL, nil)
	if err := ks.Refresh(); err != nil {
		t.Fatal(err)
	}
	server.setFailing(true)
	if err := ks.Refresh(); err == nil {
		t.Fatal("expected the failing refresh to report an error")
	}
	if _, err := NewKeysVerifier(ks).Verify(signWithKid(t, v1Priv, "service-token:v1")); err != nil {
		t.Fatalf("cached key lost: %v", err)
	}
}

func TestKeySetRetriesTheFirstLoadUntilThePlatformAnswers(t *testing.T) {
	v1, v1Priv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"service-token:v1", v1})
	server.setFailing(true)
	ks := NewKeySet(server.URL, nil, nil)
	ks.initialBackoff = 5 * time.Millisecond
	ks.maxBackoff = 10 * time.Millisecond
	ks.Start()
	defer ks.Close()

	deadline := time.Now().Add(2 * time.Second)
	for server.fetches.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("the first load is not retried")
		}
		time.Sleep(5 * time.Millisecond)
	}
	server.setFailing(false)
	for ks.lookup("service-token:v1") == nil {
		if time.Now().After(deadline) {
			t.Fatal("the key set never loaded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := NewKeysVerifier(ks).Verify(signWithKid(t, v1Priv, "service-token:v1")); err != nil {
		t.Fatal(err)
	}
}

func TestParseKeySetSkipsWhatIsNotAnEd25519SigningKey(t *testing.T) {
	good, _ := generate(t)
	x := base64.RawURLEncoding.EncodeToString(good)
	body := `{"keys":[
		{"kty":"RSA","kid":"rsa:v1","n":"abc","e":"AQAB"},
		{"kty":"OKP","crv":"X25519","kid":"x:v1","x":"` + x + `"},
		{"kty":"OKP","crv":"Ed25519","use":"enc","kid":"enc:v1","x":"` + x + `"},
		{"kty":"OKP","crv":"Ed25519","kid":"short:v1","x":"AAAA"},
		{"kty":"OKP","crv":"Ed25519","kid":"padded:v1","x":"` + base64.URLEncoding.EncodeToString(good) + `"},
		{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","use":"sig","kid":"service-token:v1","x":"` + x + `"}
	]}`
	byKid, withoutKid, err := parseKeySet([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(byKid) != 2 || byKid["service-token:v1"] == nil || byKid["padded:v1"] == nil || withoutKid != nil {
		t.Fatalf("got %v / %v", byKid, withoutKid)
	}
	if _, _, err := parseKeySet([]byte(`{"nope":1}`)); err == nil {
		t.Fatal("a body without keys must be an error")
	}
}

func TestStaticKeyRefusesATokenWithKid(t *testing.T) {
	key, pub := newKey(t)
	v, _ := NewVerifier(pub)
	if _, err := v.Verify(signWithKid(t, key, "service-token:v1")); err == nil ||
		!strings.Contains(err.Error(), "ZQNT_PLATFORM_KEYS_URL") {
		t.Fatalf("got %v", err)
	}
}

func TestGuardWithAKeySet(t *testing.T) {
	v1, v1Priv := generate(t)
	server := newFakeKeyServer(t, publishedKey{"service-token:v1", v1})
	guard, err := NewGuard(Config{PlatformKeysURL: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	const method = "/zqnt.EdgeAdapterService/TakeOff"
	if err := guard.Check(withBearer(signWithKid(t, v1Priv, "service-token:v1")), method); err != nil {
		t.Fatalf("a token from the set was refused: %v", err)
	}
	_, otherPriv := generate(t)
	if status.Code(guard.Check(withBearer(signWithKid(t, otherPriv, "service-token:v2")), method)) != codes.Unauthenticated {
		t.Fatal("a token with an unknown kid passed")
	}
}

func TestFromEnvReadsTheKeySetURL(t *testing.T) {
	t.Setenv("ZQNT_PLATFORM_KEYS_URL", " http://admin-console:8005/api/admin-console/auth/service-keys ")
	if got := FromEnv().PlatformKeysURL; got != "http://admin-console:8005/api/admin-console/auth/service-keys" {
		t.Fatalf("got %q", got)
	}
}
