package ipsession_test

import (
	"bytes"
	"context"
	"net"
	"sync"
	"testing"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity/identitytest"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link/ipsession"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/tlsbinding"
)

// bindingServer records the channel binding of each stream's TLS session and the binding the
// node put into its first frame, then answers every frame with a Pong.
type bindingServer struct {
	edgelinkv3.UnimplementedEdgeLinkServiceServer

	mu     sync.Mutex
	server [][]byte
	node   [][]byte
}

func (s *bindingServer) Connect(stream edgelinkv3.EdgeLinkService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	p, _ := peer.FromContext(stream.Context())
	cb, err := tlsbinding.FromPeer(p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.server = append(s.server, cb)
	s.node = append(s.node, first.GetHello().GetEphemeralPub())
	s.mu.Unlock()
	for {
		if _, err := stream.Recv(); err != nil {
			return err
		}
		if err := stream.Send(&edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Pong{Pong: &edgelinkv3.Pong{}}}); err != nil {
			return err
		}
	}
}

func TestHandshakeCarriesTheChannelBindingOfItsConnection(t *testing.T) {
	creds := identitytest.NewTLS(t)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(creds.Server))
	s := &bindingServer{}
	edgelinkv3.RegisterEdgeLinkServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	d, err := ipsession.New("lte", ipsession.Options{
		Endpoint: lis.Addr().String(), CAFile: creds.CAFile, ServerName: "localhost",
		MinBackoff: 10 * time.Millisecond, RefusalMinBackoff: 300 * time.Millisecond, RefusalMaxBackoff: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	var mu sync.Mutex
	var handshakes []link.Handshake
	d.SetHandshake(func(hs link.Handshake) ([]byte, error) {
		mu.Lock()
		handshakes = append(handshakes, hs)
		mu.Unlock()
		// The binding travels in the ephemeral key field so the server can compare.
		return proto.Marshal(&edgelinkv3.UplinkFrame{Frame: &edgelinkv3.UplinkFrame_Hello{Hello: &edgelinkv3.Hello{EphemeralPub: hs.ChannelBinding}}})
	})
	if err := d.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitState(t, d, link.StateUp)
	d.Send(context.Background(), ping(t, 1), link.SendMeta{})
	select {
	case f := <-d.Receive():
		mu.Lock()
		conn := handshakes[0].Conn
		mu.Unlock()
		if f.Conn != conn {
			t.Fatalf("frame on connection %d, handshake %d", f.Conn, conn)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
	}

	mu.Lock()
	first := handshakes[0].Conn
	mu.Unlock()
	resetAt := time.Now()
	d.Reset(first, "bad welcome")
	waitState(t, d, link.StateDown)
	waitState(t, d, link.StateUp)
	if wait := time.Since(resetAt); wait < 150*time.Millisecond {
		t.Errorf("reconnected %v after a reset, want the refusal backoff", wait)
	}
	d.Reset(first, "stale")
	d.Send(context.Background(), ping(t, 2), link.SendMeta{})
	select {
	case f := <-d.Receive():
		if f.Conn != first+1 {
			t.Fatalf("frame on connection %d after a stale reset, want %d", f.Conn, first+1)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a reset of an ended connection ended the current one")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if len(s.server) != 2 || len(handshakes) != 2 || handshakes[1].Conn != handshakes[0].Conn+1 {
		t.Fatalf("server saw %d streams, node %d handshakes", len(s.server), len(handshakes))
	}
	for i := range s.server {
		if len(s.server[i]) != tlsbinding.Size || !bytes.Equal(s.server[i], s.node[i]) {
			t.Errorf("stream %d: server binding %x, node binding %x", i, s.server[i], s.node[i])
		}
	}
}
