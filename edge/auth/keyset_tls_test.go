package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/internaltls"
	"github.com/zequent/zqnt-sdk-go/v3/internaltls/internaltlstest"
)

func TestTheKeySetIsFetchedOverInternalTLS(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pki := internaltlstest.NewTestCertificate(t, t.TempDir())
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "use": "sig", "kid": "service-token:v1",
			"x": base64.RawURLEncoding.EncodeToString(pub)}}})
	}))
	server.TLS = pki.Server
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := internaltls.Config{Enabled: true, CAFile: pki.CAFile}.HTTPClient(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ks := NewKeySetWithClient(server.URL, nil, nil, client)
	if err := ks.Refresh(); err != nil {
		t.Fatalf("refresh over the installation CA: %v", err)
	}
	if key, err := ks.Key("service-token:v1"); err != nil || !key.Equal(pub) {
		t.Fatalf("key = %v, %v", key, err)
	}

	plain := NewKeySet(server.URL, nil, nil)
	if err := plain.Refresh(); err == nil {
		t.Fatal("a plain client must not trust the installation's certificate")
	}
}
