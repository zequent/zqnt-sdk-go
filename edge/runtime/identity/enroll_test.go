package identity_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity/identitytest"
)

type fixture struct {
	auth   *identitytest.Authority
	tls    identitytest.TLS
	server *identitytest.EnrollmentServer
	dir    identity.Dir
	store  *identity.FileSealedKeyStore
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	auth := identitytest.NewAuthority(t)
	creds := identitytest.NewTLS(t)
	dir := identity.Dir(filepath.Join(t.TempDir(), "identity"))
	store, err := identity.NewFileSealedKeyStore(string(dir), bytes.Repeat([]byte{7}, identity.SealingKeySize))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{auth: auth, tls: creds, server: identitytest.StartEnrollment(t, auth, &creds), dir: dir, store: store}
}

func (f *fixture) options() identity.EnrollOptions {
	return identity.EnrollOptions{
		Dir:            f.dir,
		Store:          f.store,
		ClaimCode:      "zq-abcde-fghjk",
		Platform:       identity.Platform{Endpoint: f.server.Addr, CAFile: f.tls.CAFile, ServerName: "localhost"},
		Assets:         []*edgelinkv3.EnrollingAsset{{Sn: "DOCK-1"}, {Sn: "DRONE-1"}},
		RuntimeVersion: "test",
		Suite:          uint32(edgelinkv3.CryptoSuite_CRYPTO_SUITE_S1_CHACHA20_POLY1305),
		NodeClass:      edgelinkv3.NodeClass_NODE_CLASS_INFRASTRUCTURE,
	}
}

func (f *fixture) enroll(t *testing.T) *identity.State {
	t.Helper()
	state, err := identity.Enroll(context.Background(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestEnrollStoresTheIdentity(t *testing.T) {
	f := newFixture(t)
	state := f.enroll(t)

	nodes := f.server.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("%d nodes enrolled", len(nodes))
	}
	n := nodes[0]
	if len(n.Binding) != 32 {
		t.Fatalf("platform saw channel binding %x", n.Binding)
	}
	if n.Protection != edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_DEV || len(n.Assets) != 2 {
		t.Fatalf("enrolled %+v", n)
	}
	loaded, err := f.dir.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NodeID != n.NodeID || state.NodeID != n.NodeID || !bytes.Equal(loaded.IdentityKeyID, n.KeyID) ||
		loaded.InstallationID != f.auth.InstallationID || loaded.OrganizationID != f.server.OrganizationID ||
		loaded.KeyProtection != "KEY_PROTECTION_SOFTWARE_DEV" || !bytes.Equal(loaded.Anchors[0].KeyID, f.auth.Anchor.ID) {
		t.Fatalf("state %+v", loaded)
	}
	token, meta, err := f.dir.Credential()
	if err != nil || token != n.Credential || time.Until(meta.ExpiresAt) < 300*24*time.Hour {
		t.Fatalf("credential %q %+v %v", token, meta, err)
	}
	p, err := identity.NewProvider(f.dir, f.store, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.KeyID(), n.KeyID) {
		t.Fatalf("provider key %x, enrolled %x", p.KeyID(), n.KeyID)
	}
	if _, err := f.store.Load(identity.KeyNext); !errors.Is(err, identity.ErrNoKey) {
		t.Fatalf("a pending key is left after enrollment: %v", err)
	}

	if _, err := identity.Enroll(context.Background(), f.options()); err == nil {
		t.Fatal("enrolled an enrolled node again without --reenroll")
	}
}

func TestEnrollWithoutTLSHasNoChannelBinding(t *testing.T) {
	auth := identitytest.NewAuthority(t)
	srv := identitytest.StartEnrollment(t, auth, nil)
	dir := identity.Dir(t.TempDir())
	store, err := identity.NewDevKeyStore(string(dir))
	if err != nil {
		t.Fatal(err)
	}
	_, err = identity.Enroll(context.Background(), identity.EnrollOptions{
		Dir: dir, Store: store, ClaimCode: srv.ClaimCode, Platform: identity.Platform{Endpoint: srv.Addr, Insecure: true},
		Suite: 0x0101, NodeClass: edgelinkv3.NodeClass_NODE_CLASS_INFRASTRUCTURE,
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := srv.Nodes()[0]; len(n.Binding) != 0 || n.Protection != edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_DEV {
		t.Fatalf("enrolled %+v", n)
	}
}

func TestEnrollmentRefusals(t *testing.T) {
	cases := map[string]func(t *testing.T, f *fixture, o *identity.EnrollOptions){
		"wrong claim code": func(_ *testing.T, _ *fixture, o *identity.EnrollOptions) { o.ClaimCode = "ZQ-XXXXX-XXXXX" },
		"other node class": func(_ *testing.T, _ *fixture, o *identity.EnrollOptions) {
			o.NodeClass = edgelinkv3.NodeClass_NODE_CLASS_PERSONAL
		},
		"provisioned anchor differs": func(t *testing.T, f *fixture, o *identity.EnrollOptions) {
			other := identitytest.NewAuthority(t)
			o.Provisioning = &identity.Provisioning{Version: 1, InstallationID: f.auth.InstallationID, Anchors: other.Anchors()}
		},
		"provisioned installation differs": func(_ *testing.T, f *fixture, o *identity.EnrollOptions) {
			o.Provisioning = &identity.Provisioning{Version: 1, InstallationID: "inst-other", Anchors: f.auth.Anchors()}
		},
		"platform certificate not trusted": func(_ *testing.T, _ *fixture, o *identity.EnrollOptions) { o.Platform.CAFile = "" },
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			o := f.options()
			modify(t, f, &o)
			if _, err := identity.Enroll(context.Background(), o); err == nil {
				t.Fatal("enrolled")
			}
			if _, err := f.dir.LoadState(); !errors.Is(err, identity.ErrNotEnrolled) {
				t.Fatalf("state after a refused enrollment: %v", err)
			}
			for _, k := range []string{identity.KeyActive, identity.KeyNext} {
				if _, err := f.store.Load(k); !errors.Is(err, identity.ErrNoKey) {
					t.Fatalf("key %s left after a refused enrollment: %v", k, err)
				}
			}
		})
	}
}

func TestAClaimOfAnotherClassCreatesNoNode(t *testing.T) {
	f := newFixture(t)
	o := f.options()
	o.NodeClass = edgelinkv3.NodeClass_NODE_CLASS_PERSONAL
	_, err := identity.Enroll(context.Background(), o)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
	if nodes := f.server.Nodes(); len(nodes) != 0 {
		t.Fatalf("the platform created %d nodes", len(nodes))
	}
}

func TestEnrollWithProvisioning(t *testing.T) {
	f := newFixture(t)
	o := f.options()
	anchors := f.auth.Anchors()
	anchors[0].PublicKey = nil
	o.Provisioning = &identity.Provisioning{Version: 1, InstallationID: f.auth.InstallationID, Anchors: anchors}
	qr, err := o.Provisioning.EncodeQR()
	if err != nil {
		t.Fatal(err)
	}
	if o.Provisioning, err = identity.ParseProvisioningQR(qr); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Enroll(context.Background(), o); err != nil {
		t.Fatal(err)
	}
}

func TestRotateKey(t *testing.T) {
	f := newFixture(t)
	before := f.enroll(t)
	n := identity.Node{Dir: f.dir, Store: f.store}
	after, err := n.RotateKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(after.IdentityKeyID, before.IdentityKeyID) || !bytes.Equal(f.server.Nodes()[0].KeyID, after.IdentityKeyID) {
		t.Fatalf("rotated to %x, platform has %x", []byte(after.IdentityKeyID), f.server.Nodes()[0].KeyID)
	}
	p, err := identity.NewProvider(f.dir, f.store, after)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.KeyID(), after.IdentityKeyID) {
		t.Fatal("provider does not use the rotated key")
	}
	if _, err := f.store.Load(identity.KeyNext); !errors.Is(err, identity.ErrNoKey) {
		t.Fatal("pending key left after a confirmed rotation")
	}
}

func TestRotationWithALostAnswerTriesBothKeys(t *testing.T) {
	f := newFixture(t)
	before := f.enroll(t)
	f.server.FailNext(identitytest.ErrUnavailable)
	n := identity.Node{Dir: f.dir, Store: f.store}
	if _, err := n.RotateKey(context.Background()); !errors.Is(err, identity.ErrRotationUnknown) {
		t.Fatalf("RotateKey = %v", err)
	}
	newID := f.server.Nodes()[0].KeyID
	state, err := f.dir.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	p, err := identity.NewProvider(f.dir, f.store, state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.KeyID(), newID) {
		t.Fatalf("first Hello key %x, want the new key %x", p.KeyID(), newID)
	}
	p.Refused()
	if !bytes.Equal(p.KeyID(), before.IdentityKeyID) {
		t.Fatalf("after a refusal key %x, want the old key", p.KeyID())
	}
	p.Refused()
	if err := p.Confirmed(newID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.dir.LoadState(); !bytes.Equal(got.IdentityKeyID, newID) {
		t.Fatalf("state keeps %x after the platform confirmed the new key", []byte(got.IdentityKeyID))
	}
	if _, err := f.store.Load(identity.KeyNext); !errors.Is(err, identity.ErrNoKey) {
		t.Fatal("pending key left after confirmation")
	}
}

func TestRefusedRotationKeepsTheOldKey(t *testing.T) {
	f := newFixture(t)
	before := f.enroll(t)
	if err := os.WriteFile(f.dir.CredentialPath(), []byte("stolen\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (identity.Node{Dir: f.dir, Store: f.store}).RotateKey(context.Background()); err == nil || errors.Is(err, identity.ErrRotationUnknown) {
		t.Fatalf("RotateKey with a bad credential = %v", err)
	}
	state, _ := f.dir.LoadState()
	if !bytes.Equal(state.IdentityKeyID, before.IdentityKeyID) {
		t.Fatal("state changed after a refused rotation")
	}
	if _, err := f.store.Load(identity.KeyNext); !errors.Is(err, identity.ErrNoKey) {
		t.Fatal("pending key left after a refused rotation")
	}
}

func TestRenewCredential(t *testing.T) {
	f := newFixture(t)
	state := f.enroll(t)
	old, _, _ := f.dir.Credential()
	p, err := identity.NewProvider(f.dir, f.store, state)
	if err != nil {
		t.Fatal(err)
	}
	n := identity.Node{Dir: f.dir, Store: f.store}
	if _, err := n.RenewCredential(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	token, _, _ := f.dir.Credential()
	if token == old || token != f.server.Nodes()[0].Credential {
		t.Fatalf("credential %q after renewal, platform %q", token, f.server.Nodes()[0].Credential)
	}
}

func TestRenewalRunsWhenAThirdOfTheLifetimeRemains(t *testing.T) {
	f := newFixture(t)
	state := f.enroll(t)
	old, meta, _ := f.dir.Credential()
	if want := meta.ExpiresAt.Add(-meta.ExpiresAt.Sub(meta.IssuedAt) / 3); !meta.RenewAt().Equal(want) {
		t.Fatalf("RenewAt %v", meta.RenewAt())
	}
	p, err := identity.NewProvider(f.dir, f.store, state)
	if err != nil {
		t.Fatal(err)
	}
	clock := meta.RenewAt().Add(time.Second)
	n := identity.Node{Dir: f.dir, Store: f.store, Now: func() time.Time { return clock }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); n.RunRenewal(ctx, p, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if token, _, _ := f.dir.Credential(); token != old {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("credential not renewed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestProvisioningParsing(t *testing.T) {
	auth := identitytest.NewAuthority(t)
	good := &identity.Provisioning{Version: 1, InstallationID: auth.InstallationID, Anchors: auth.Anchors(), ClaimCode: "ZQ-1"}
	qr, err := good.EncodeQR()
	if err != nil {
		t.Fatal(err)
	}
	got, err := identity.ParseProvisioningQR(qr)
	if err != nil || got.ClaimCode != "ZQ-1" || !bytes.Equal(got.Anchors[0].PublicKey, auth.Anchor.Public) {
		t.Fatalf("QR round trip: %+v %v", got, err)
	}
	for name, data := range map[string]string{
		"unknown field": `{"version":1,"installation_id":"i","anchors":[{"key_id":"a9c6451c6ffc4d2ccf7043e60e4d3cee","alg":2}],"extra":1}`,
		"no anchors":    `{"version":1,"installation_id":"i","anchors":[]}`,
		"short key id":  `{"version":1,"installation_id":"i","anchors":[{"key_id":"a9c6","alg":2}]}`,
		"wrong version": `{"version":2,"installation_id":"i","anchors":[{"key_id":"a9c6451c6ffc4d2ccf7043e60e4d3cee","alg":2}]}`,
		"key mismatch":  `{"version":1,"installation_id":"i","anchors":[{"key_id":"a9c6451c6ffc4d2ccf7043e60e4d3cee","alg":2,"public_key":"` + b64(auth.Handshake.Public) + `"}]}`,
	} {
		if _, err := identity.ParseProvisioning([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := identity.ParseProvisioningQR("https://example.com"); err == nil {
		t.Error("accepted a QR payload without the prefix")
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
