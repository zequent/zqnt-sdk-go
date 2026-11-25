package edgesdk

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	edgev3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edge/v3"
	"github.com/zequent/zqnt-sdk-go/v3/edge/adapter"
	"github.com/zequent/zqnt-sdk-go/v3/internaltls"
	"github.com/zequent/zqnt-sdk-go/v3/internaltls/internaltlstest"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func serveGatewayTLS(t *testing.T, pki internaltlstest.TestCertificate) (*gatewayServer, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := &gatewayServer{}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(pki.Server)))
	edgev3.RegisterEdgeGatewayServiceServer(srv, gw)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return gw, lis.Addr().String()
}

func startClient(t *testing.T, addr string, opts ...Option) *EdgeClient {
	t.Helper()
	drone := &struct{ adapter.Base }{}
	drone.MustRegisterCommand("flight.takeoff", nil, nil, noop)
	client, err := NewEdgeClient(addr, "SN-TLS", drone, append(opts, WithoutPlatformAuth(),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))...)
	if err != nil {
		t.Fatal(err)
	}
	adapterLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = client.StartServing(ctx, adapterLis) }()
	t.Cleanup(func() { cancel(); _ = client.Shutdown(context.Background()) })
	return client
}

func TestThePlatformIsReachedOverInternalTLS(t *testing.T) {
	pki := internaltlstest.NewTestCertificate(t, t.TempDir())
	gw, addr := serveGatewayTLS(t, pki)
	startClient(t, addr, WithInternalTLS(internaltls.Config{Enabled: true, CAFile: pki.CAFile}))

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, set := gw.last(); set != nil && set.GetAssetSn() == "SN-TLS" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the capability report never arrived over TLS")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAnotherCAOrPlaintextDoesNotReachTheTLSPlatform(t *testing.T) {
	pki := internaltlstest.NewTestCertificate(t, t.TempDir())
	gw, addr := serveGatewayTLS(t, pki)
	other := internaltlstest.NewTestCertificate(t, t.TempDir())
	startClient(t, addr, WithInternalTLS(internaltls.Config{Enabled: true, CAFile: other.CAFile}))
	startClient(t, addr, WithInternalTLS(internaltls.Config{}))

	time.Sleep(500 * time.Millisecond)
	if n, _ := gw.last(); n != 0 {
		t.Fatalf("%d report(s) arrived without the installation CA", n)
	}
}

func TestAnUnreadableCAFailsTheClientUpFront(t *testing.T) {
	drone := &struct{ adapter.Base }{}
	_, err := NewEdgeClient("127.0.0.1:1", "SN-1", drone, WithoutPlatformAuth(),
		WithInternalTLS(internaltls.Config{Enabled: true, CAFile: t.TempDir() + "/missing.crt"}))
	if err == nil || !strings.Contains(err.Error(), "ZQNT_INTERNAL_TLS_CA") {
		t.Fatalf("want the CA error, got %v", err)
	}
}
