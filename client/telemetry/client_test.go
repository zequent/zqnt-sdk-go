package telemetry_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/zequent/zqnt-client-sdk-go/v3/telemetry"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	telemetryv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/telemetry/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeTelemetry struct {
	telemetryv3.UnimplementedTelemetryServiceServer

	mu        sync.Mutex
	request   *telemetryv3.WatchTelemetryRequest
	refuse    error
	responses []*telemetryv3.WatchTelemetryResponse
	end       bool
	cancelled chan struct{}
}

func (f *fakeTelemetry) WatchTelemetry(req *telemetryv3.WatchTelemetryRequest, stream grpc.ServerStreamingServer[telemetryv3.WatchTelemetryResponse]) error {
	f.mu.Lock()
	f.request = req
	f.mu.Unlock()
	if f.refuse != nil {
		return f.refuse
	}
	for _, resp := range f.responses {
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
	if f.end {
		return nil
	}
	<-stream.Context().Done()
	close(f.cancelled)
	return nil
}

func dial(t *testing.T, fake *fakeTelemetry) *telemetry.Client {
	t.Helper()
	fake.cancelled = make(chan struct{})
	lis := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	telemetryv3.RegisterTelemetryServiceServer(server, fake)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return telemetry.New(conn)
}

func timeout(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func waitCancelled(t *testing.T, fake *fakeTelemetry) {
	t.Helper()
	select {
	case <-fake.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never saw the watch end")
	}
}

func TestWatchTelemetryYieldsStatusAndSamplesAndSkipsHeartbeats(t *testing.T) {
	fake := &fakeTelemetry{responses: []*telemetryv3.WatchTelemetryResponse{
		{Item: &telemetryv3.WatchTelemetryResponse_SourceStatus{SourceStatus: &telemetryv3.SourceStatus{
			Asset: &commonv3.AssetRef{Sn: "DRONE-1"}, State: telemetryv3.SourceState_SOURCE_STATE_ONLINE}}},
		{Item: &telemetryv3.WatchTelemetryResponse_Heartbeat{Heartbeat: timestamppb.Now()}},
		{Item: &telemetryv3.WatchTelemetryResponse_Sample{Sample: &telemetryv3.TelemetrySample{
			Asset: &commonv3.AssetRef{Sn: "DRONE-1"}, BatteryPercent: ptr(87.0)}}},
	}}
	watch, err := dial(t, fake).WatchTelemetry(timeout(t), []string{"DOCK-1", "DRONE-1"},
		telemetry.WithMinInterval(500*time.Millisecond))
	if err != nil {
		t.Fatalf("WatchTelemetry: %v", err)
	}
	first, err := watch.Recv()
	if err != nil || first.GetSourceStatus().GetState() != telemetryv3.SourceState_SOURCE_STATE_ONLINE {
		t.Fatalf("first = %v, %v", first, err)
	}
	second, err := watch.Recv()
	if err != nil || second.GetSample().GetBatteryPercent() != 87 {
		t.Fatalf("second = %v, %v", second, err)
	}
	watch.Close()
	waitCancelled(t, fake)
	req := fake.request
	if len(req.GetAssets()) != 2 || req.GetAssets()[0].GetSn() != "DOCK-1" || req.GetMinInterval().AsDuration() != 500*time.Millisecond {
		t.Fatalf("request = %v", req)
	}
	if req.GetContext().GetRequestId() == "" {
		t.Fatal("no request id")
	}
}

func TestNoAssetWatchesTheOrganizationAndTheEndIsEOF(t *testing.T) {
	fake := &fakeTelemetry{end: true}
	watch, err := dial(t, fake).WatchTelemetry(timeout(t), nil)
	if err != nil {
		t.Fatalf("WatchTelemetry: %v", err)
	}
	if _, err := watch.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv = %v", err)
	}
	if len(fake.request.GetAssets()) != 0 || fake.request.GetMinInterval() != nil {
		t.Fatalf("request = %v", fake.request)
	}
}

func TestARefusedWatchKeepsItsStatusCode(t *testing.T) {
	fake := &fakeTelemetry{refuse: status.Error(codes.PermissionDenied, "live data is not licensed")}
	watch, err := dial(t, fake).WatchTelemetry(timeout(t), []string{"DRONE-1"})
	if err != nil {
		t.Fatalf("WatchTelemetry: %v", err)
	}
	if _, err := watch.Recv(); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Recv = %v", err)
	}
}

func TestASilentStreamEndsWithErrSilent(t *testing.T) {
	fake := &fakeTelemetry{}
	watch, err := dial(t, fake).WatchTelemetry(timeout(t), []string{"DRONE-1"},
		telemetry.WithHeartbeatTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatalf("WatchTelemetry: %v", err)
	}
	if _, err := watch.Recv(); !errors.Is(err, telemetry.ErrSilent) {
		t.Fatalf("Recv = %v", err)
	}
	waitCancelled(t, fake)
}

func TestAnEmptySerialIsRefused(t *testing.T) {
	if _, err := dial(t, &fakeTelemetry{}).WatchTelemetry(timeout(t), []string{"DRONE-1", ""}); err == nil {
		t.Fatal("an empty serial must be refused")
	}
}

func ptr[T any](v T) *T { return &v }
