package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	devicecontrolpb "github.com/zequent/zqnt-utils-golang/v2/gen/devicecontrol/contracts/proto"
	remotecontrolpb "github.com/zequent/zqnt-utils-golang/v2/gen/remotecontrol/proto"
	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type remoteControl struct {
	remotecontrolpb.UnimplementedRemoteControlServiceServer
	edgev3.UnimplementedEdgeGatewayServiceServer
	mu     sync.Mutex
	v3Sets []*capabilityv3.CapabilitySet
	v2     []*devicecontrolpb.ReportAssetRuntimeRequest
}

func (r *remoteControl) ReportCapabilities(_ context.Context, req *edgev3.ReportCapabilitiesRequest) (*edgev3.ReportCapabilitiesResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.v3Sets = append(r.v3Sets, req.GetCapabilities())
	return &edgev3.ReportCapabilitiesResponse{AcceptedRevision: req.GetCapabilities().GetRevision()}, nil
}

func (r *remoteControl) ReportAssetRuntime(_ context.Context, req *devicecontrolpb.ReportAssetRuntimeRequest) (*devicecontrolpb.ReportAssetRuntimeResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.v2 = append(r.v2, req)
	rev := req.GetRevision()
	return &devicecontrolpb.ReportAssetRuntimeResponse{AcceptedRevision: &rev}, nil
}

func reporter(t *testing.T, rc *remoteControl, withV3 bool) *CapabilityReporter {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	remotecontrolpb.RegisterRemoteControlServiceServer(srv, rc)
	if withV3 {
		edgev3.RegisterEdgeGatewayServiceServer(srv, rc)
	}
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); srv.Stop() })
	return NewCapabilityReporter(edgev3.NewEdgeGatewayServiceClient(conn), remotecontrolpb.NewRemoteControlServiceClient(conn),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func caps() *domains.CurrentCapabilities {
	return &domains.CurrentCapabilities{SN: "SN-1", AssetType: "ASSET_TYPE_DRONE", Timestamp: time.Now(),
		Capabilities:    []domains.Capability{{Command: "flight.takeoff", Available: true, InputSchema: map[string]any{"type": "object"}}},
		TelemetryFields: []domains.TelemetryField{{Key: "drone.gear", Type: domains.TelemetryValueNumber}}}
}

func TestCapabilitiesAreReportedOverV3WithARevision(t *testing.T) {
	rc := &remoteControl{}
	rev, err := reporter(t, rc, true).Report(context.Background(), caps())
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.v3Sets) != 1 || len(rc.v2) != 0 {
		t.Fatalf("v3 %d, v2 %d", len(rc.v3Sets), len(rc.v2))
	}
	set := rc.v3Sets[0]
	if rev == "" || set.GetRevision() != rev || set.GetTelemetryFields()[0].GetKey() != "drone.gear" ||
		set.GetCapabilities()[0].GetCommandId() != "flight.takeoff" {
		t.Fatalf("revision %q, set %v", rev, set)
	}
}

func TestRevisionChangesWithTheContentOnly(t *testing.T) {
	rc := &remoteControl{}
	r := reporter(t, rc, true)
	first, _ := r.Report(context.Background(), caps())
	later := caps()
	later.Timestamp = later.Timestamp.Add(time.Hour)
	again, _ := r.Report(context.Background(), later)
	changed := caps()
	changed.Capabilities[0].Available = false
	other, _ := r.Report(context.Background(), changed)
	if first != again || first == other {
		t.Fatalf("revisions %q %q %q", first, again, other)
	}
}

func TestCapabilitiesFallBackToReportAssetRuntime(t *testing.T) {
	rc := &remoteControl{}
	r := reporter(t, rc, false)
	for range 2 {
		if _, err := r.Report(context.Background(), caps()); err != nil {
			t.Fatal(err)
		}
	}
	if len(rc.v2) != 2 {
		t.Fatalf("v2 reports = %d", len(rc.v2))
	}
	if got := rc.v2[0]; got.GetAssetSn() != "SN-1" || got.GetCapabilities()[0].GetCommandId() != "flight.takeoff" || got.GetRevision() == "" {
		t.Fatalf("v2 request = %v", got)
	}
	if r.sw.Available() {
		t.Fatal("v3 must be skipped after UNIMPLEMENTED")
	}
}
