package session_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity/identitytest"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/session"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/tlsbinding"
)

// edgeLink is an in-process edge-link that terminates TLS and runs the platform side of the
// handshake with the zqnt-api helper: it checks the Hello against the TLS session's channel
// binding, answers a stale clock with a HelloRetry and signs the Welcome. Its misbehave hook
// lets a test corrupt what it sends.
type edgeLink struct {
	edgelinkv3.UnimplementedEdgeLinkServiceServer
	auth    *identitytest.Authority
	org     string
	nodeKey []byte
	served  []string

	mu        sync.Mutex
	misbehave func(w *edgelinkv3.Welcome) *edgelinkv3.DownlinkFrame
	hellos    []*edgelinkv3.Hello
	refused   int
	retries   int
	envelopes int
}

func (e *edgeLink) Connect(stream edgelinkv3.EdgeLinkService_ConnectServer) error {
	p, _ := peer.FromContext(stream.Context())
	cb, err := tlsbinding.FromPeer(p)
	if err != nil {
		return status.Error(codes.Unauthenticated, "handshake refused")
	}
	hello, transcript, err := e.recvHello(stream, cb)
	if err != nil {
		return err
	}
	now := time.Now()
	if wc := hello.GetWallclock().AsTime(); hello.GetWallclock() == nil || now.Sub(wc).Abs() > 120*time.Second {
		retry := e.auth.HelloRetry(transcript, now)
		e.count(func() { e.retries++ })
		if err := stream.Send(&edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_HelloRetry{HelloRetry: retry}}); err != nil {
			return err
		}
		if hello, transcript, err = e.recvHello(stream, cb); err != nil {
			return err
		}
		if !bytes.Equal(hello.GetServerNonce(), retry.GetServerNonce()) {
			e.count(func() { e.refused++ })
			return status.Error(codes.Unauthenticated, "handshake refused")
		}
	}
	offset := time.Since(hello.GetWallclock().AsTime()).Milliseconds()
	w := &edgelinkv3.Welcome{SessionId: "s1", ClockOffsetMs: offset, ServedAssetSns: e.served}
	e.auth.Welcome(transcript, hello, w).Zero()
	frame := &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Welcome{Welcome: w}}
	e.mu.Lock()
	if e.misbehave != nil {
		frame = e.misbehave(w)
	}
	e.mu.Unlock()
	if frame != nil {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	for {
		f, err := stream.Recv()
		if err != nil {
			return err
		}
		if f.GetEnvelope() != nil {
			e.count(func() { e.envelopes++ })
		}
	}
}

func (e *edgeLink) recvHello(stream edgelinkv3.EdgeLinkService_ConnectServer, cb []byte) (*edgelinkv3.Hello, []byte, error) {
	f, err := stream.Recv()
	if err != nil {
		return nil, nil, err
	}
	hello := f.GetHello()
	e.count(func() { e.hellos = append(e.hellos, hello) })
	transcript, err := identitytest.VerifyHello(hello, e.org, cb, e.nodeKey)
	if err != nil {
		e.count(func() { e.refused++ })
		return nil, nil, status.Error(codes.Unauthenticated, "handshake refused")
	}
	return hello, transcript, nil
}

func (e *edgeLink) count(f func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f()
}

type handshakeNode struct {
	sess   *session.Session
	trust  *identity.Trust
	errs   chan error
	edge   *edgeLink
	cancel func()
}

type handshakeOpts struct {
	clock     func() time.Time
	misbehave func(w *edgelinkv3.Welcome) *edgelinkv3.DownlinkFrame
	auth      *identitytest.Authority
	pinned    *identitytest.Authority
	highWater time.Time
}

func startHandshake(t *testing.T, o handshakeOpts) *handshakeNode {
	t.Helper()
	if o.auth == nil {
		o.auth = identitytest.NewAuthority(t)
	}
	if o.pinned == nil {
		o.pinned = o.auth
	}
	creds := identitytest.NewTLS(t)
	org := "6f1c2a0e-3b5d-4c8e-9a7f-1d2e3f405162"
	enrolled := identitytest.Enroll(t, o.pinned, org)
	e := &edgeLink{auth: o.auth, org: org, nodeKey: enrolled.Key.Public, served: []string{"SIM-1"}, misbehave: o.misbehave}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(creds.Server))
	edgelinkv3.RegisterEdgeLinkServiceServer(srv, e)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	st, err := store.Open(filepath.Join(t.TempDir(), "node.db"), store.Options{FlushInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg, err := link.ParseConfig([]byte(`
links:
  - id: lte
    driver: ip-session
    endpoint: ` + lis.Addr().String() + `
    ca_file: ` + creds.CAFile + `
    server_name: localhost
    min_backoff: 10ms
    max_backoff: 50ms
    refusal_min_backoff: 20ms
    refusal_max_backoff: 100ms
`))
	if err != nil {
		t.Fatal(err)
	}
	drivers, err := link.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id, err := identity.NewProvider(enrolled.Dir, enrolled.Store, enrolled.State)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := enrolled.Dir.Trust(enrolled.State)
	if err != nil {
		t.Fatal(err)
	}
	if !o.highWater.IsZero() {
		if err := trust.RaiseHighWater(o.highWater); err != nil {
			t.Fatal(err)
		}
	}
	errs := make(chan error, 64)
	sess, err := session.New(session.Config{
		NodeID:         enrolled.State.NodeID,
		AssetSNs:       []string{"SIM-1", "SIM-2"},
		LinkKind:       "ip-session",
		RuntimeVersion: "test",
		Store:          st,
		Driver:         drivers[0],
		Identity:       id,
		Trust:          trust,
		Clock:          session.NewClock(o.clock),
		ReportInterval: -1,
		WelcomeTimeout: 500 * time.Millisecond,
		OnHandshakeError: func(err error) {
			select {
			case errs <- err:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sess.Run(ctx) }()
	stop := func() { cancel(); <-done }
	t.Cleanup(stop)
	return &handshakeNode{sess: sess, trust: trust, errs: errs, edge: e, cancel: stop}
}

func (n *handshakeNode) expectRefusal(t *testing.T, want error) {
	t.Helper()
	select {
	case err := <-n.errs:
		if want != nil && !errors.Is(err, want) {
			t.Fatalf("handshake error %v, want %v", err, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no handshake error")
	}
	waitFor(t, "a second Hello after the refusal", func() bool {
		var n2 int
		n.edge.count(func() { n2 = len(n.edge.hellos) })
		return n2 >= 2
	})
	if n.sess.Connected() {
		t.Fatal("session connected after a refused Welcome")
	}
}

func TestSignedHandshakeOverTLS(t *testing.T) {
	n := startHandshake(t, handshakeOpts{})
	waitFor(t, "a session", n.sess.Connected)
	if got := n.sess.ServedAssets(); len(got) != 1 || got[0] != "SIM-1" {
		t.Fatalf("served assets %v", got)
	}
	if n.sess.SessionID() != "s1" || !n.sess.Clock().Synced() {
		t.Fatalf("session %q, clock synced %v", n.sess.SessionID(), n.sess.Clock().Synced())
	}
	if _, err := n.sess.Publish(context.Background(), sample(1)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the envelope", func() bool {
		var got int
		n.edge.count(func() { got = n.edge.envelopes })
		return got == 1
	})
	n.edge.count(func() {
		h := n.edge.hellos[0]
		if h.GetCryptoSuite() != 0x0101 || len(h.GetIdentityKeyId()) != 16 || len(h.GetSignature()) != 64 ||
			len(h.GetEphemeralPub()) != 32 || len(h.GetServerNonce()) != 0 {
			t.Errorf("hello %v", h)
		}
	})
	if hw := n.trust.HighWater(); time.Since(hw) > time.Minute {
		t.Errorf("time high-water mark %v not raised", hw)
	}
}

func TestStaleClockTakesTheHelloRetryPath(t *testing.T) {
	skew := -3 * time.Hour
	n := startHandshake(t, handshakeOpts{clock: func() time.Time { return time.Now().Add(skew) }})
	waitFor(t, "a session", n.sess.Connected)
	n.edge.count(func() {
		if n.edge.retries != 1 || len(n.edge.hellos) != 2 {
			t.Fatalf("%d retries, %d hellos", n.edge.retries, len(n.edge.hellos))
		}
		first, second := n.edge.hellos[0], n.edge.hellos[1]
		if len(second.GetServerNonce()) != 32 || bytes.Equal(first.GetEphemeralPub(), second.GetEphemeralPub()) {
			t.Errorf("second hello: nonce %x, same ephemeral key %v", second.GetServerNonce(), bytes.Equal(first.GetEphemeralPub(), second.GetEphemeralPub()))
		}
	})
	if d := time.Until(n.sess.Clock().Now()).Abs(); d > 5*time.Second {
		t.Fatalf("clock off by %v after the HelloRetry", d)
	}
}

func TestRefusesABadWelcomeSignature(t *testing.T) {
	n := startHandshake(t, handshakeOpts{misbehave: func(w *edgelinkv3.Welcome) *edgelinkv3.DownlinkFrame {
		w.Signature[5] ^= 1
		return &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Welcome{Welcome: w}}
	}})
	n.expectRefusal(t, handshake.ErrSignature)
}

func TestRefusesAnUnsignedWelcome(t *testing.T) {
	n := startHandshake(t, handshakeOpts{misbehave: func(*edgelinkv3.Welcome) *edgelinkv3.DownlinkFrame {
		return &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Welcome{Welcome: &edgelinkv3.Welcome{SessionId: "forged"}}}
	}})
	n.expectRefusal(t, identity.ErrPlatformKey)
}

func TestRefusesAWelcomeThatChangedAfterSigning(t *testing.T) {
	n := startHandshake(t, handshakeOpts{misbehave: func(w *edgelinkv3.Welcome) *edgelinkv3.DownlinkFrame {
		w.ServedAssetSns = append(w.ServedAssetSns, "SIM-2")
		return &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Welcome{Welcome: w}}
	}})
	n.expectRefusal(t, handshake.ErrSignature)
}

func TestRefusesAPlatformOfAnotherAnchor(t *testing.T) {
	n := startHandshake(t, handshakeOpts{pinned: identitytest.NewAuthority(t), auth: identitytest.NewAuthority(t)})
	n.expectRefusal(t, nil)
}

func TestRefusesAWelcomeSignedByAnotherKeyThanCertified(t *testing.T) {
	auth := identitytest.NewAuthority(t)
	n := startHandshake(t, handshakeOpts{auth: auth, misbehave: func(w *edgelinkv3.Welcome) *edgelinkv3.DownlinkFrame {
		other := identitytest.NewKey(t)
		w.PlatformKey = auth.PlatformKey()
		w.Signature = other.Sign(t, []byte("anything"))
		return &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Welcome{Welcome: w}}
	}})
	n.expectRefusal(t, handshake.ErrSignature)
}

func TestRefusesAnExpiredPlatformKey(t *testing.T) {
	auth := identitytest.NewAuthority(t)
	auth.NotBefore, auth.NotAfter = time.Now().Add(-72*time.Hour), time.Now().Add(-time.Hour)
	n := startHandshake(t, handshakeOpts{auth: auth, highWater: time.Now()})
	n.expectRefusal(t, identity.ErrPlatformKeyTime)
}

func TestRefusesAnExpiredPlatformKeyOnceTheClockIsTrusted(t *testing.T) {
	auth := identitytest.NewAuthority(t)
	auth.NotBefore, auth.NotAfter = time.Now().Add(-72*time.Hour), time.Now().Add(-time.Hour)
	// The stale clock makes the platform send a HelloRetry, whose time the node then trusts.
	n := startHandshake(t, handshakeOpts{auth: auth, clock: func() time.Time { return time.Now().Add(-5 * time.Hour) }})
	n.expectRefusal(t, identity.ErrPlatformKeyTime)
}

func TestRefusesFramesBeforeTheWelcome(t *testing.T) {
	n := startHandshake(t, handshakeOpts{misbehave: func(*edgelinkv3.Welcome) *edgelinkv3.DownlinkFrame {
		return &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Ack{Ack: &edgelinkv3.Ack{Epoch: 1}}}
	}})
	n.expectRefusal(t, nil)
}

func TestGivesUpWithoutAWelcome(t *testing.T) {
	n := startHandshake(t, handshakeOpts{misbehave: func(*edgelinkv3.Welcome) *edgelinkv3.DownlinkFrame { return nil }})
	n.expectRefusal(t, nil)
}
