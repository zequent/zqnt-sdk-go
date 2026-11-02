package ipsession_test

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link/ipsession"
)

// echo answers every Ping with a Pong and remembers each stream's first frame and token.
type echo struct {
	edgelinkv3.UnimplementedEdgeLinkServiceServer

	mu     sync.Mutex
	firsts []*edgelinkv3.UplinkFrame
	tokens []string
	kill   []context.CancelFunc
}

func (e *echo) Connect(stream edgelinkv3.EdgeLinkService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	md, _ := metadata.FromIncomingContext(stream.Context())
	e.mu.Lock()
	e.firsts = append(e.firsts, first)
	e.tokens = append(e.tokens, md.Get("authorization")...)
	e.kill = append(e.kill, cancel)
	e.mu.Unlock()
	errs := make(chan error, 1)
	go func() {
		for {
			f, err := stream.Recv()
			if err != nil {
				errs <- err
				return
			}
			pong := &edgelinkv3.Pong{Nonce: f.GetPing().GetNonce()}
			if err := stream.Send(&edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Pong{Pong: pong}}); err != nil {
				errs <- err
				return
			}
		}
	}()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		return status.Error(codes.Unavailable, "killed")
	}
}

func (e *echo) killAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, k := range e.kill {
		k()
	}
	e.kill = nil
}

func (e *echo) streams() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.firsts)
}

func serve(t *testing.T) (*echo, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e := &echo{}
	srv := grpc.NewServer()
	edgelinkv3.RegisterEdgeLinkServiceServer(srv, e)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return e, lis.Addr().String()
}

func frame(t *testing.T, f *edgelinkv3.UplinkFrame) []byte {
	t.Helper()
	b, err := proto.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ping(t *testing.T, nonce uint32) []byte {
	return frame(t, &edgelinkv3.UplinkFrame{Frame: &edgelinkv3.UplinkFrame_Ping{Ping: &edgelinkv3.Ping{Nonce: nonce}}})
}

func waitState(t *testing.T, d link.Driver, want link.LinkState) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case st := <-d.State():
			if st.State == want {
				return
			}
		case <-timeout:
			t.Fatalf("link never became %v", want)
		}
	}
}

func receivePong(t *testing.T, d link.Driver, nonce uint32) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f := <-d.Receive():
			var df edgelinkv3.DownlinkFrame
			if err := proto.Unmarshal(f.Payload, &df); err != nil {
				t.Fatal(err)
			}
			if df.GetPong().GetNonce() == nonce {
				return
			}
		case <-timeout:
			t.Fatalf("no pong %d", nonce)
		}
	}
}

func TestHandshakeFirstOnEveryConnection(t *testing.T) {
	e, addr := serve(t)
	tokenFile := t.TempDir() + "/token"
	if err := os.WriteFile(tokenFile, []byte("node-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := ipsession.New("lte", ipsession.Options{
		Endpoint: addr, Insecure: true, TokenFile: tokenFile, MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	d.SetHandshake(func(link.Handshake) ([]byte, error) {
		return frame(t, &edgelinkv3.UplinkFrame{Frame: &edgelinkv3.UplinkFrame_Hello{Hello: &edgelinkv3.Hello{NodeId: "n1"}}}), nil
	})
	if res := d.Send(context.Background(), ping(t, 1), link.SendMeta{}); res.Status != link.Down {
		t.Fatalf("Send before Open = %v", res.Status)
	}
	if err := d.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitState(t, d, link.StateUp)
	if res := d.Send(context.Background(), ping(t, 1), link.SendMeta{}); !res.OK() {
		t.Fatalf("Send = %v", res)
	}
	receivePong(t, d, 1)

	e.killAll()
	waitState(t, d, link.StateDown)
	waitState(t, d, link.StateUp)
	if res := d.Send(context.Background(), ping(t, 2), link.SendMeta{}); !res.OK() {
		t.Fatalf("Send after reconnect = %v", res)
	}
	receivePong(t, d, 2)

	e.mu.Lock()
	for i, f := range e.firsts {
		if f.GetHello().GetNodeId() != "n1" {
			t.Errorf("stream %d opened with %v", i, f)
		}
	}
	if len(e.tokens) < 2 || e.tokens[0] != "Bearer node-secret" {
		t.Errorf("tokens = %v", e.tokens)
	}
	e.mu.Unlock()
	if e.streams() < 2 {
		t.Fatalf("%d streams", e.streams())
	}
	m := d.Metrics()
	if m.FramesSent < 4 || m.FramesReceived < 2 || m.LastRx.IsZero() {
		t.Errorf("metrics = %+v", m)
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-d.Receive(); ok {
		t.Fatal("receive channel open after Close")
	}
	for range d.State() {
	}
	if res := d.Send(context.Background(), ping(t, 3), link.SendMeta{}); res.Status != link.Down {
		t.Fatalf("Send after Close = %v", res.Status)
	}
}

func TestRefusesOversizedFrames(t *testing.T) {
	d, err := ipsession.New("lte", ipsession.Options{Endpoint: "127.0.0.1:1", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if res := d.Send(context.Background(), make([]byte, d.Caps().MaxPayload+1), link.SendMeta{}); res.Status != link.TooLarge {
		t.Fatalf("Send = %v", res.Status)
	}
}

func TestRegistryOptions(t *testing.T) {
	cfg, err := link.ParseConfig([]byte(`
links:
  - id: lte
    driver: ip-session
    endpoint: edge.example:443
    min_backoff: 1s
    max_backoff: 30s
`))
	if err != nil {
		t.Fatal(err)
	}
	drivers, err := link.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if drivers[0].ID() != "lte" || drivers[0].Caps().Security != link.SecurityLinkAuth {
		t.Fatalf("driver %s caps %+v", drivers[0].ID(), drivers[0].Caps())
	}
	for _, bad := range []string{
		"links: [{id: lte, driver: ip-session}]",
		"links: [{id: lte, driver: ip-session, endpoint: x:1, max_backoff: soon}]",
		"links: [{id: lte, driver: ip-session, endpoint: x:1, insecure: maybe}]",
	} {
		cfg, err := link.ParseConfig([]byte(bad))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := link.Build(cfg); err == nil {
			t.Errorf("built %q", bad)
		}
	}
}
