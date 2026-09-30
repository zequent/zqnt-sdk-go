package config_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/Zequent/zqnt-client-sdk-go/auth"
	"github.com/Zequent/zqnt-client-sdk-go/config"
	"github.com/Zequent/zqnt-client-sdk-go/connector"
	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	connectorpb "github.com/Zequent/zqnt-client-sdk-go/gen/connector/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func lookup(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestNothingSetIsTheLocalStack(t *testing.T) {
	c, err := config.FromLookup(lookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]config.Endpoint{
		"connector":        {Host: "localhost", Port: 8010, Plaintext: true},
		"remote-control":   {Host: "localhost", Port: 8002, Plaintext: true},
		"live-data":        {Host: "localhost", Port: 8003, Plaintext: true},
		"mission-autonomy": {Host: "localhost", Port: 8004, Plaintext: true},
	}
	got := map[string]config.Endpoint{
		"connector": c.Connector, "remote-control": c.RemoteControl,
		"live-data": c.LiveData, "mission-autonomy": c.MissionAutonomy,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %+v, want %+v", name, got[name], w)
		}
	}
	if c.HasToken() {
		t.Error("no built-in development credential")
	}
}

func TestADeploymentSetsHostPortTLSAndToken(t *testing.T) {
	c, err := config.FromLookup(lookup(map[string]string{
		"CONNECTOR_SERVICE_HOST":          "connector.zequent.internal",
		"CONNECTOR_SERVICE_PORT":          " 443 ",
		"CONNECTOR_SERVICE_USE_PLAINTEXT": "false",
		"LIVE_DATA_SERVICE_USE_PLAINTEXT": "",
		"ZQNT_CLIENT_TOKEN":               "deployment-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Connector != (config.Endpoint{Host: "connector.zequent.internal", Port: 443, Plaintext: false}) {
		t.Errorf("connector = %+v", c.Connector)
	}
	if c.Connector.Target() != "connector.zequent.internal:443" {
		t.Errorf("target = %s", c.Connector.Target())
	}
	if !c.LiveData.Plaintext {
		t.Error("a blank value is the local default")
	}
	if !c.HasToken() {
		t.Error("ZQNT_CLIENT_TOKEN is the credential")
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", c, c, c), "deployment-token") {
		t.Error("printing a Config must not print the credential")
	}
}

func TestAMalformedPortNamesItsVariable(t *testing.T) {
	_, err := config.FromLookup(lookup(map[string]string{"LIVE_DATA_SERVICE_PORT": "80a3"}))
	if err == nil || !strings.Contains(err.Error(), "LIVE_DATA_SERVICE_PORT") {
		t.Fatalf("err = %v", err)
	}
}

type recorder struct {
	connectorpb.UnimplementedConnectorServiceServer
	mu      sync.Mutex
	headers []string
}

func (r *recorder) GetAssetBySn(ctx context.Context, _ *base.RequestBase) (*connectorpb.ConnectorResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.headers = append(r.headers, strings.Join(md.Get("authorization"), ","))
	return &connectorpb.ConnectorResponse{}, nil
}

func TestDialSendsTheCredentialAndHostOptionsRunFirst(t *testing.T) {
	r := &recorder{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	connectorpb.RegisterConnectorServiceServer(server, r)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	port := lis.Addr().(*net.TCPAddr).Port

	c, err := config.FromLookup(lookup(map[string]string{
		"CONNECTOR_SERVICE_HOST": "127.0.0.1",
		"CONNECTOR_SERVICE_PORT": fmt.Sprint(port),
	}))
	if err != nil {
		t.Fatal(err)
	}
	c = c.WithToken("fixed-token")

	plain, err := c.Dial(c.Connector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	if _, err := connector.New(plain).GetAssetBySn(context.Background(), "DOCK-1"); err != nil {
		t.Fatalf("GetAssetBySn: %v", err)
	}

	forwarding := grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(auth.WithToken(ctx, "caller-token"), method, req, reply, cc, opts...)
	})
	host, err := c.Dial(c.Connector, forwarding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	if _, err := connector.New(host).GetAssetBySn(context.Background(), "DOCK-1"); err != nil {
		t.Fatalf("GetAssetBySn: %v", err)
	}

	if got := strings.Join(r.headers, "|"); got != "Bearer fixed-token|Bearer caller-token" {
		t.Fatalf("headers = %q", got)
	}
}
