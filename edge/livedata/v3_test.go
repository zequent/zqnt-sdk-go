package livedata

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	detectionpb "github.com/zequent/zqnt-utils-golang/v2/gen/common/detection/proto"
	eventspb "github.com/zequent/zqnt-utils-golang/v2/gen/events/proto"
	livedatapb "github.com/zequent/zqnt-utils-golang/v2/gen/livedata/proto"
	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"
	telemetryv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/telemetry/v3"
	"github.com/zequent/zqnt-utils-golang/v2/telemetry"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// platform is an in-process platform: v2 live-data always, the v3 services only when asked for,
// so a v2-only core answers UNIMPLEMENTED exactly like the real one.
type platform struct {
	livedatapb.UnimplementedLiveDataServiceServer
	edgev3.UnimplementedEdgeGatewayServiceServer
	telemetryv3.UnimplementedTelemetryIngestServiceServer

	mu            sync.Mutex
	v3Events      []*capabilityv3.CommandEvent
	v2Events      []*eventspb.CommandExecutionEvent
	v3Samples     []*telemetryv3.TelemetrySample
	v2Telemetry   []*livedatapb.Telemetry
	v3Detections  []*telemetryv3.DetectionBatch
	v2Detections  []*detectionpb.DetectionBatch
	v3Alerts      []*telemetryv3.Alert
	gatewayErr    error
	gatewayCalled int
}

func (p *platform) PublishCommandEvent(_ context.Context, req *edgev3.PublishCommandEventRequest) (*edgev3.PublishCommandEventResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gatewayCalled++
	if p.gatewayErr != nil {
		return nil, p.gatewayErr
	}
	if req.GetEvent().GetOccurredAt() == nil {
		return nil, status.Error(codes.InvalidArgument, "occurred_at is required")
	}
	p.v3Events = append(p.v3Events, req.GetEvent())
	return &edgev3.PublishCommandEventResponse{}, nil
}

func (p *platform) ProduceNotification(stream grpc.ClientStreamingServer[eventspb.ProduceNotificationRequest, livedatapb.LiveDataResponse]) error {
	return drain(stream, func(r *eventspb.ProduceNotificationRequest) {
		p.mu.Lock()
		p.v2Events = append(p.v2Events, r.GetEvent().GetCommandExecution())
		p.mu.Unlock()
	}, &livedatapb.LiveDataResponse{})
}

func (p *platform) ProduceTelemetry(stream grpc.ClientStreamingServer[livedatapb.ProduceTelemetryRequest, livedatapb.LiveDataResponse]) error {
	return drain(stream, func(r *livedatapb.ProduceTelemetryRequest) {
		p.mu.Lock()
		p.v2Telemetry = append(p.v2Telemetry, r.GetData())
		p.mu.Unlock()
	}, &livedatapb.LiveDataResponse{})
}

func (p *platform) ProduceDetection(stream grpc.ClientStreamingServer[detectionpb.DetectionBatch, livedatapb.LiveDataResponse]) error {
	return drain(stream, func(r *detectionpb.DetectionBatch) {
		p.mu.Lock()
		p.v2Detections = append(p.v2Detections, r)
		p.mu.Unlock()
	}, &livedatapb.LiveDataResponse{})
}

func (p *platform) PublishTelemetry(stream grpc.ClientStreamingServer[telemetryv3.PublishTelemetryRequest, telemetryv3.PublishTelemetryResponse]) error {
	return drain(stream, func(r *telemetryv3.PublishTelemetryRequest) {
		p.mu.Lock()
		p.v3Samples = append(p.v3Samples, r.GetSample())
		p.mu.Unlock()
	}, &telemetryv3.PublishTelemetryResponse{})
}

func (p *platform) PublishDetections(stream grpc.ClientStreamingServer[telemetryv3.PublishDetectionsRequest, telemetryv3.PublishDetectionsResponse]) error {
	return drain(stream, func(r *telemetryv3.PublishDetectionsRequest) {
		p.mu.Lock()
		p.v3Detections = append(p.v3Detections, r.GetBatch())
		p.mu.Unlock()
	}, &telemetryv3.PublishDetectionsResponse{})
}

func (p *platform) PublishAlerts(stream grpc.ClientStreamingServer[telemetryv3.PublishAlertsRequest, telemetryv3.PublishAlertsResponse]) error {
	return drain(stream, func(r *telemetryv3.PublishAlertsRequest) {
		p.mu.Lock()
		p.v3Alerts = append(p.v3Alerts, r.GetAlert())
		p.mu.Unlock()
	}, &telemetryv3.PublishAlertsResponse{})
}

func drain[Req, Resp any](stream grpc.ClientStreamingServer[Req, Resp], record func(*Req), resp *Resp) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(resp)
		}
		if err != nil {
			return err
		}
		record(req)
	}
}

// start serves p and returns a ServiceImpl talking to it; withV3 decides whether the platform
// serves the v3 services at all.
func start(t *testing.T, p *platform, withV3 bool) *ServiceImpl {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	livedatapb.RegisterLiveDataServiceServer(srv, p)
	if withV3 {
		edgev3.RegisterEdgeGatewayServiceServer(srv, p)
		telemetryv3.RegisterTelemetryIngestServiceServer(srv, p)
	}
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServiceImpl(livedatapb.NewLiveDataServiceClient(conn), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithGateway(edgev3.NewEdgeGatewayServiceClient(conn)),
		WithTelemetryIngest(telemetryv3.NewTelemetryIngestServiceClient(conn)))
	t.Cleanup(func() {
		_ = s.Shutdown(context.Background())
		_ = conn.Close()
		srv.Stop()
	})
	return s
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *platform) locked(f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f()
}

func TestCommandEventGoesOverV3WithOccurredAtAndTheExecutionID(t *testing.T) {
	p := &platform{}
	s := start(t, p, true)
	progress := float32(0.5)
	err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
		SN: "SN-1", CommandExecutionID: "capexec:e1:n1", ExternalExecutionID: "dji-9", CommandID: "flight.takeoff",
		Status: domains.CommandExecutionSucceeded, Progress: &progress, Output: map[string]any{"altitude": 40.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.locked(func() {
		if len(p.v3Events) != 1 || len(p.v2Events) != 0 {
			t.Fatalf("v3 %d, v2 %d events", len(p.v3Events), len(p.v2Events))
		}
		e := p.v3Events[0]
		if e.GetCommandExecutionId() != "capexec:e1:n1" || e.GetState() != capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED ||
			e.GetAsset().GetSn() != "SN-1" || e.GetResult().GetFields()["altitude"].GetNumberValue() != 40 {
			t.Fatalf("event = %v", e)
		}
		if time.Since(e.GetOccurredAt().AsTime()) > time.Minute {
			t.Fatalf("occurred_at = %v, want now", e.GetOccurredAt().AsTime())
		}
	})
}

func TestCommandEventFallsBackToV2AndStaysThere(t *testing.T) {
	p := &platform{}
	s := start(t, p, false)
	for _, status := range []domains.CommandExecutionStatus{domains.CommandExecutionRunning, domains.CommandExecutionFailed} {
		if err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
			SN: "SN-1", CommandExecutionID: "capexec:e1:n1", Status: status, Message: "motor fault",
		}); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "two v2 events", func() bool {
		var n int
		p.locked(func() { n = len(p.v2Events) })
		return n == 2
	})
	p.locked(func() {
		if got := p.v2Events[0].GetExternalExecutionId(); got != "capexec:e1:n1" {
			t.Errorf("v2 external execution id = %q", got)
		}
		if p.v2Events[1].GetOccurredAt() == nil {
			t.Error("v2 event without occurred_at")
		}
	})
	if s.gatewayV3.Available() {
		t.Error("v3 must be skipped for a while after UNIMPLEMENTED")
	}
}

func TestCommandEventRefusedByTheGatewayIsReturned(t *testing.T) {
	p := &platform{gatewayErr: status.Error(codes.InvalidArgument, "unknown execution")}
	s := start(t, p, true)
	err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
		SN: "SN-1", CommandExecutionID: "x", Status: domains.CommandExecutionSucceeded})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
	p.locked(func() {
		if len(p.v2Events) != 0 {
			t.Error("a refused event must not be re-sent over v2")
		}
	})
}

func TestCommandEventNeedsAnID(t *testing.T) {
	s := start(t, &platform{}, true)
	if err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{SN: "SN-1"}); err == nil {
		t.Fatal("an event without an id must be refused locally")
	}
}

func ptr[T any](v T) *T { return &v }

func sample() *domains.TelemetrySample {
	return &domains.TelemetrySample{
		SN:              "SN-1",
		Position:        &domains.GeoPoint{Lat: 52.5, Lon: 13.4, Alt: ptr(120.0)},
		HeadingDegrees:  ptr(90.0),
		HorizontalSpeed: ptr(4.5),
		BatteryPercent:  ptr(81.0),
		Details:         map[string]any{"drone.gear": 1.0},
	}
}

func TestTelemetrySampleGoesOverTheV3Stream(t *testing.T) {
	p := &platform{}
	s := start(t, p, true)
	for range 3 {
		if err := s.PublishTelemetrySample(context.Background(), sample()); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "three v3 samples", func() bool {
		var n int
		p.locked(func() { n = len(p.v3Samples) })
		return n == 3
	})
	p.locked(func() {
		got := p.v3Samples[0]
		if got.GetAsset().GetSn() != "SN-1" || got.GetPosition().GetAltitude() != 120 || got.GetObservedAt() == nil ||
			got.GetDetails().GetFields()["drone.gear"].GetNumberValue() != 1 {
			t.Fatalf("sample = %v", got)
		}
		if len(p.v2Telemetry) != 0 {
			t.Error("v2 used although v3 is served")
		}
	})
}

func TestTelemetrySampleFallsBackToV2WithItsCatalogDetails(t *testing.T) {
	p := &platform{}
	s := start(t, p, false)
	eventually(t, "a v2 telemetry sample", func() bool {
		_ = s.PublishTelemetrySample(context.Background(), sample())
		var n int
		p.locked(func() { n = len(p.v2Telemetry) })
		return n > 0
	})
	p.locked(func() {
		got := p.v2Telemetry[0]
		if got.GetSn() != "SN-1" || got.GetLatitude() != 52.5 || got.GetAbsoluteAltitude() != 120 || got.GetHeading() != 90 ||
			got.GetSubAsset().GetHorizontalSpeed() != 4.5 || got.GetSubAsset().GetBatteryInformation().GetPercentage() != "81" ||
			got.GetSubAsset().GetGear() != 1 {
			t.Fatalf("v2 telemetry = %v", got)
		}
	})
}

func TestAStandingAssetFallsBackToV2AssetTelemetry(t *testing.T) {
	p := &platform{}
	s := start(t, p, false)
	standing := &domains.TelemetrySample{
		SN:             "SN-2",
		BatteryPercent: ptr(64.0),
		Details: map[string]any{
			telemetry.DockMode:                       "WORKING",
			telemetry.DockManualControlActiveSession: true,
			telemetry.WindSpeed:                      2.5,
		},
	}
	eventually(t, "a v2 telemetry sample", func() bool {
		_ = s.PublishTelemetrySample(context.Background(), standing)
		var n int
		p.locked(func() { n = len(p.v2Telemetry) })
		return n > 0
	})
	p.locked(func() {
		got := p.v2Telemetry[0]
		asset := got.GetAsset()
		if asset == nil || asset.GetMode().String() != "ASSET_MODE_WORKING" || !asset.GetHasActiveManualControlSession() ||
			asset.GetSubAssetPercentage() != 64 || got.GetWindSpeed() != 2.5 {
			t.Fatalf("v2 telemetry = %v", got)
		}
	})
}

func TestDetectionsGoOverV3OrFallBackToV2(t *testing.T) {
	batch := &domains.DetectionBatch{SN: "SN-1", Detections: []domains.DetectionResult{{ObjectID: "o1", ObjectType: "person", Confidence: 0.9}}}

	p3 := &platform{}
	s3 := start(t, p3, true)
	if err := s3.PublishDetections(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a v3 detection batch", func() bool {
		var n int
		p3.locked(func() { n = len(p3.v3Detections) })
		return n == 1
	})

	p2 := &platform{}
	s2 := start(t, p2, false)
	eventually(t, "a v2 detection batch", func() bool {
		_ = s2.PublishDetections(context.Background(), batch)
		var n int
		p2.locked(func() { n = len(p2.v2Detections) })
		return n > 0
	})
	p2.locked(func() {
		if d := p2.v2Detections[0]; d.GetBase().GetSn() != "SN-1" || d.GetDetections()[0].GetObjectType() != "person" {
			t.Fatalf("v2 batch = %v", d)
		}
	})
}

func TestAlertsGoOverV3(t *testing.T) {
	p := &platform{}
	s := start(t, p, true)
	if err := s.PublishAlert(context.Background(), &domains.Alert{SN: "SN-1", Code: "dock.rain", Severity: domains.AlertSeverityWarning}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a v3 alert", func() bool {
		var n int
		p.locked(func() { n = len(p.v3Alerts) })
		return n == 1
	})
	p.locked(func() {
		if a := p.v3Alerts[0]; a.GetSeverity() != telemetryv3.AlertSeverity_ALERT_SEVERITY_WARNING || a.GetOccurredAt() == nil {
			t.Fatalf("alert = %v", a)
		}
	})
}

func TestBrokenStreamWaitsForItsBackoffBeforeReopening(t *testing.T) {
	opens := 0
	now := time.Unix(0, 0)
	st := newIngestStream("test", func(context.Context) (grpc.ClientStreamingClient[telemetryv3.PublishTelemetryRequest, telemetryv3.PublishTelemetryResponse], error) {
		opens++
		return nil, status.Error(codes.Unavailable, "down")
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	st.now = func() time.Time { return now }

	if err := st.send(&telemetryv3.PublishTelemetryRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v", err)
	}
	if err := st.send(&telemetryv3.PublishTelemetryRequest{}); !errors.Is(err, ErrReconnecting) {
		t.Fatalf("err = %v, want ErrReconnecting during backoff", err)
	}
	now = now.Add(initialReconnectDelay)
	_ = st.send(&telemetryv3.PublishTelemetryRequest{})
	if opens != 2 {
		t.Fatalf("opened %d times, want 2", opens)
	}
	now = now.Add(initialReconnectDelay)
	if err := st.send(&telemetryv3.PublishTelemetryRequest{}); !errors.Is(err, ErrReconnecting) {
		t.Fatalf("second backoff must be longer: err = %v", err)
	}
}
