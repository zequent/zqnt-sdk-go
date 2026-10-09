package edgesdk

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"

	"google.golang.org/grpc"
)

type gatewayServer struct {
	edgev3.UnimplementedEdgeGatewayServiceServer
	mu      sync.Mutex
	reports []*capabilityv3.CapabilitySet
}

func (g *gatewayServer) ReportCapabilities(_ context.Context, req *edgev3.ReportCapabilitiesRequest) (*edgev3.ReportCapabilitiesResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reports = append(g.reports, req.GetCapabilities())
	return &edgev3.ReportCapabilitiesResponse{AcceptedRevision: req.GetCapabilities().GetRevision()}, nil
}

func (g *gatewayServer) last() (int, *capabilityv3.CapabilitySet) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.reports) == 0 {
		return 0, nil
	}
	return len(g.reports), g.reports[len(g.reports)-1]
}

func noop(context.Context, *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	return domains.Success("", ""), nil
}

func TestCapabilitiesAreReportedOnStartAndOnEveryRegistryChange(t *testing.T) {
	platformLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gw := &gatewayServer{}
	srv := grpc.NewServer()
	edgev3.RegisterEdgeGatewayServiceServer(srv, gw)
	go func() { _ = srv.Serve(platformLis) }()
	defer srv.Stop()

	drone := &struct{ adapter.Base }{}
	drone.MustRegisterCommand("flight.takeoff", nil, nil, noop)

	client, err := NewEdgeClient(platformLis.Addr().String(), "SN-1", drone, WithoutPlatformAuth(),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	adapterLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.StartServing(ctx, adapterLis) }()
	defer func() { _ = client.Shutdown(context.Background()) }()

	waitFor := func(what string, cond func(*capabilityv3.CapabilitySet) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, set := gw.last(); set != nil && cond(set) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor("the start report", func(set *capabilityv3.CapabilitySet) bool {
		return set.GetAssetSn() == "SN-1" && len(set.GetCapabilities()) == 1
	})
	drone.MustRegisterCommand("dock.open_cover", nil, nil, noop)
	waitFor("the report after a registration", func(set *capabilityv3.CapabilitySet) bool {
		return len(set.GetCapabilities()) == 2
	})
}
