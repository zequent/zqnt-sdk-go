package local_test

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	edgev3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edge/v3"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/command"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/local"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/session"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

const asset = "DOCK-1"

// recorder stands in for the node session: it keeps what would go to the platform.
type recorder struct {
	mu   sync.Mutex
	envs []*edgelinkv3.Envelope
}

func (r *recorder) Publish(_ context.Context, env *edgelinkv3.Envelope) (*edgelinkv3.MessageId, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	env = proto.Clone(env).(*edgelinkv3.Envelope)
	env.Lane = session.LaneFor(env)
	r.envs = append(r.envs, env)
	return &edgelinkv3.MessageId{Epoch: 1, Seq: uint32(len(r.envs))}, nil
}

func (r *recorder) all() []*edgelinkv3.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.envs)
}

func (r *recorder) states(id string) []capabilityv3.CommandState {
	var out []capabilityv3.CommandState
	for _, env := range r.all() {
		if ev := env.GetCommandEvent(); ev.GetCommandExecutionId() == id {
			out = append(out, ev.GetState())
		}
	}
	return out
}

func (r *recorder) last(id string) *capabilityv3.CommandEvent {
	var last *capabilityv3.CommandEvent
	for _, env := range r.all() {
		if ev := env.GetCommandEvent(); ev.GetCommandExecutionId() == id {
			last = ev
		}
	}
	return last
}

// adapter is a Device Adapter on its own socket. Its ExecuteCommand answers what reply returns.
type adapter struct {
	edgev3.UnimplementedEdgeAdapterServiceServer
	endpoint string

	mu        sync.Mutex
	reply     func(req *edgev3.ExecuteCommandRequest) *capabilityv3.CommandResult
	executed  []*edgev3.ExecuteCommandRequest
	cancelled []string
}

func (a *adapter) ExecuteCommand(_ context.Context, req *edgev3.ExecuteCommandRequest) (*edgev3.ExecuteCommandResponse, error) {
	a.mu.Lock()
	a.executed = append(a.executed, req)
	reply := a.reply
	a.mu.Unlock()
	return &edgev3.ExecuteCommandResponse{Result: reply(req)}, nil
}

func (a *adapter) CancelCommand(_ context.Context, req *edgev3.CancelCommandRequest) (*edgev3.CancelCommandResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelled = append(a.cancelled, req.GetCommandExecutionId())
	return &edgev3.CancelCommandResponse{}, nil
}

func (a *adapter) set(reply func(*edgev3.ExecuteCommandRequest) *capabilityv3.CommandResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reply = reply
}

func (a *adapter) read(f func(a *adapter)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f(a)
}

func startAdapter(t *testing.T, dir, name string) *adapter {
	t.Helper()
	path := filepath.Join(dir, name+".sock")
	lis, err := local.Listen(path, local.ListenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := &adapter{endpoint: "unix://" + path, reply: func(*edgev3.ExecuteCommandRequest) *capabilityv3.CommandResult {
		return &capabilityv3.CommandResult{State: capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED}
	}}
	srv := grpc.NewServer()
	edgev3.RegisterEdgeAdapterServiceServer(srv, a)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return a
}

type node struct {
	dir    string
	store  *store.Store
	rec    *recorder
	server *local.Server
	runner *command.Runner
	conn   *grpc.ClientConn
}

func startNode(t *testing.T, cfg local.Config) *node {
	t.Helper()
	dir := socketDir(t)
	if cfg.Store == nil {
		st, err := store.Open(filepath.Join(t.TempDir(), "node.db"), store.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		cfg.Store = st
	}
	rec := &recorder{}
	cfg.Publisher = rec
	srv, err := local.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "node.sock")
	lis, err := local.Listen(sock, local.ListenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { _ = srv.Stop() })
	runner, err := command.New(command.Config{Store: cfg.Store, Publisher: rec, Handler: srv.Handler()})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &node{dir: dir, store: cfg.Store, rec: rec, server: srv, runner: runner, conn: conn}
}

func (n *node) gateway() edgev3.EdgeGatewayServiceClient {
	return edgev3.NewEdgeGatewayServiceClient(n.conn)
}

func (n *node) register(t *testing.T, a *adapter, sn string) {
	t.Helper()
	ctx := metadata.AppendToOutgoingContext(context.Background(), local.AdapterEndpointHeader, a.endpoint)
	resp, err := n.gateway().ReportCapabilities(ctx, &edgev3.ReportCapabilitiesRequest{
		Capabilities: &capabilityv3.CapabilitySet{AssetSn: sn, Revision: "r1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetAcceptedRevision() != "r1" {
		t.Fatalf("accepted revision %q", resp.GetAcceptedRevision())
	}
}

func (n *node) deliver(t *testing.T, id string, cmd *capabilityv3.Command) {
	t.Helper()
	f := &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Command{Command: &edgelinkv3.CommandDelivery{
		Id:                 &edgelinkv3.MessageId{Epoch: 1, Seq: 1},
		Context:            &commonv3.RequestContext{RequestId: "req-" + id},
		CommandExecutionId: id,
		Action:             &edgelinkv3.CommandDelivery_Execute{Execute: cmd},
	}}}
	if err := n.runner.HandleFrame(context.Background(), f); err != nil {
		t.Fatal(err)
	}
}

func (n *node) cancel(t *testing.T, id string) {
	t.Helper()
	f := &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Command{Command: &edgelinkv3.CommandDelivery{
		CommandExecutionId: id,
		Action:             &edgelinkv3.CommandDelivery_Cancel{Cancel: &edgelinkv3.CommandCancel{}},
	}}}
	if err := n.runner.HandleFrame(context.Background(), f); err != nil {
		t.Fatal(err)
	}
}

func (n *node) publishEvent(t *testing.T, id string, state capabilityv3.CommandState, mutate ...func(*capabilityv3.CommandEvent)) {
	t.Helper()
	ev := &capabilityv3.CommandEvent{CommandExecutionId: id, Asset: &commonv3.AssetRef{Sn: asset}, State: state, OccurredAt: timestamppb.Now()}
	for _, m := range mutate {
		m(ev)
	}
	if _, err := n.gateway().PublishCommandEvent(context.Background(), &edgev3.PublishCommandEventRequest{Event: ev}); err != nil {
		t.Fatal(err)
	}
}

func takeoff(sn string) *capabilityv3.Command {
	return &capabilityv3.Command{Asset: &commonv3.AssetRef{Sn: sn}, CommandId: "flight.takeoff"}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func equalStates(t *testing.T, got []capabilityv3.CommandState, want ...capabilityv3.CommandState) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
}

const (
	accepted  = capabilityv3.CommandState_COMMAND_STATE_ACCEPTED
	running   = capabilityv3.CommandState_COMMAND_STATE_RUNNING
	succeeded = capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED
	failed    = capabilityv3.CommandState_COMMAND_STATE_FAILED
	rejected  = capabilityv3.CommandState_COMMAND_STATE_REJECTED
	cancelled = capabilityv3.CommandState_COMMAND_STATE_CANCELLED
	timedOut  = capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT
)

func TestAdapterReportsGoIntoTheirLanes(t *testing.T) {
	n := startNode(t, local.Config{DefaultAdapterEndpoint: "unix:///run/zqnt/adapter.sock"})
	ctx := context.Background()
	observed := time.Now().Add(-time.Minute).Truncate(time.Millisecond)

	ingest := telemetryv3.NewTelemetryIngestServiceClient(n.conn)
	ts, err := ingest.PublishTelemetry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range []string{asset, "", asset} {
		if err := ts.Send(&telemetryv3.PublishTelemetryRequest{Sample: &telemetryv3.TelemetrySample{
			Asset: &commonv3.AssetRef{Sn: sn}, ObservedAt: timestamppb.New(observed),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := ts.CloseAndRecv()
	if err != nil {
		t.Fatal(err)
	}
	if tr.GetAccepted() != 2 || tr.GetRejected() != 1 {
		t.Fatalf("telemetry response = %v", tr)
	}

	as, err := ingest.PublishAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sev := range []telemetryv3.AlertSeverity{telemetryv3.AlertSeverity_ALERT_SEVERITY_CRITICAL, telemetryv3.AlertSeverity_ALERT_SEVERITY_INFO} {
		if err := as.Send(&telemetryv3.PublishAlertsRequest{Alert: &telemetryv3.Alert{
			Asset: &commonv3.AssetRef{Sn: asset}, Severity: sev, Code: "dock.rain",
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := as.CloseAndRecv(); err != nil {
		t.Fatal(err)
	}

	ds, err := ingest.PublishDetections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.Send(&telemetryv3.PublishDetectionsRequest{Batch: &telemetryv3.DetectionBatch{Asset: &commonv3.AssetRef{Sn: asset}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.CloseAndRecv(); err != nil {
		t.Fatal(err)
	}

	if _, err := n.gateway().ReportCapabilities(ctx, &edgev3.ReportCapabilitiesRequest{
		Capabilities: &capabilityv3.CapabilitySet{AssetSn: asset, Capabilities: []*capabilityv3.Capability{
			{CommandId: "flight.land"}, {CommandId: "camera.take_photo"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	var lanes []edgelinkv3.Lane
	for _, env := range n.rec.all() {
		lanes = append(lanes, env.GetLane())
	}
	want := []edgelinkv3.Lane{
		edgelinkv3.Lane_LANE_TELEMETRY, edgelinkv3.Lane_LANE_TELEMETRY,
		edgelinkv3.Lane_LANE_ALARM, edgelinkv3.Lane_LANE_BULK,
		edgelinkv3.Lane_LANE_BULK,
		edgelinkv3.Lane_LANE_STATUS,
	}
	if !slices.Equal(lanes, want) {
		t.Fatalf("lanes = %v, want %v", lanes, want)
	}
	reported := n.rec.all()[len(n.rec.all())-1].GetCapabilities().GetCapabilities()
	if reported[0].GetTinySchemaId() != 258 || reported[1].GetTinySchemaId() != 0 {
		t.Fatalf("tiny schema ids %d, %d; want 258 for flight.land and none for camera.take_photo",
			reported[0].GetTinySchemaId(), reported[1].GetTinySchemaId())
	}
	if got := n.rec.all()[0].GetDeviceTime().AsTime(); !got.Equal(observed) {
		t.Fatalf("device time = %v, want the sample's observed_at %v", got, observed)
	}
	if got := n.server.Assets()[asset]; got != "unix:///run/zqnt/adapter.sock" {
		t.Fatalf("asset bound to %q", got)
	}
}

func TestAnAdapterMustNameAUnixSocket(t *testing.T) {
	n := startNode(t, local.Config{})
	caps := &edgev3.ReportCapabilitiesRequest{Capabilities: &capabilityv3.CapabilitySet{AssetSn: asset}}
	if _, err := n.gateway().ReportCapabilities(context.Background(), caps); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("without an endpoint: %v", err)
	}
	for _, endpoint := range []string{"10.0.0.5:9090", "dns:///adapter:9090", "unix:relative.sock"} {
		ctx := metadata.AppendToOutgoingContext(context.Background(), local.AdapterEndpointHeader, endpoint)
		if _, err := n.gateway().ReportCapabilities(ctx, caps); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("endpoint %q: %v", endpoint, err)
		}
	}
	if len(n.rec.all()) != 0 || len(n.server.Assets()) != 0 {
		t.Fatal("a refused report was published or registered")
	}
	if _, err := local.New(local.Config{Store: n.store, Publisher: n.rec, DefaultAdapterEndpoint: "localhost:1"}); err == nil {
		t.Fatal("a network default endpoint was accepted")
	}
}

func TestACommandRunsOnTheAdapterThatRegisteredTheAsset(t *testing.T) {
	n := startNode(t, local.Config{})
	a := startAdapter(t, n.dir, "a")
	b := startAdapter(t, n.dir, "b")
	n.register(t, a, asset)
	n.register(t, b, "DRONE-2")
	b.set(func(*edgev3.ExecuteCommandRequest) *capabilityv3.CommandResult {
		r, _ := structpb.NewStruct(map[string]any{"ok": true})
		return &capabilityv3.CommandResult{State: succeeded, Result: r}
	})

	n.deliver(t, "c-1", takeoff(asset))
	n.deliver(t, "c-2", takeoff("DRONE-2"))
	waitFor(t, "both outcomes", func() bool { return len(n.rec.states("c-1")) == 2 && len(n.rec.states("c-2")) == 2 })
	equalStates(t, n.rec.states("c-1"), accepted, succeeded)
	if !n.rec.last("c-2").GetResult().GetFields()["ok"].GetBoolValue() {
		t.Fatalf("result = %v", n.rec.last("c-2"))
	}
	a.read(func(a *adapter) {
		if len(a.executed) != 1 || a.executed[0].GetCommandExecutionId() != "c-1" || a.executed[0].GetContext().GetRequestId() != "req-c-1" {
			t.Errorf("adapter a executed %v", a.executed)
		}
	})
	b.read(func(b *adapter) {
		if len(b.executed) != 1 || b.executed[0].GetCommand().GetAsset().GetSn() != "DRONE-2" {
			t.Errorf("adapter b executed %v", b.executed)
		}
	})
}

func TestALongCommandEndsWithTheAdaptersEvent(t *testing.T) {
	n := startNode(t, local.Config{})
	a := startAdapter(t, n.dir, "a")
	n.register(t, a, asset)
	a.set(func(*edgev3.ExecuteCommandRequest) *capabilityv3.CommandResult {
		return &capabilityv3.CommandResult{State: accepted}
	})
	n.deliver(t, "c-1", takeoff(asset))
	waitFor(t, "the adapter call", func() bool {
		var called bool
		a.read(func(a *adapter) { called = len(a.executed) == 1 })
		return called
	})
	n.publishEvent(t, "c-1", running, func(ev *capabilityv3.CommandEvent) {
		ev.Progress = proto.Float32(0.5)
		ev.Message = "climbing"
	})
	waitFor(t, "progress", func() bool { return len(n.rec.states("c-1")) == 2 })
	if ev := n.rec.last("c-1"); ev.GetProgress() != 0.5 || ev.GetMessage() != "climbing" {
		t.Fatalf("progress event = %v", ev)
	}
	n.publishEvent(t, "c-1", failed, func(ev *capabilityv3.CommandEvent) {
		ev.Error = &commonv3.Error{Code: "flight.motor_fault", Message: "motor 3"}
	})
	waitFor(t, "the outcome", func() bool { return len(n.rec.states("c-1")) == 3 })
	equalStates(t, n.rec.states("c-1"), accepted, running, failed)
	if e := n.rec.last("c-1").GetError(); e.GetCode() != "flight.motor_fault" || e.GetMessage() != "motor 3" {
		t.Fatalf("error = %v", e)
	}

	n.publishEvent(t, "c-1", succeeded)
	time.Sleep(20 * time.Millisecond)
	if got := len(n.rec.states("c-1")); got != 3 {
		t.Fatalf("a late event of a finished command reached the platform: %v", n.rec.states("c-1"))
	}
	n.publishEvent(t, "other", succeeded)
	if got := n.rec.states("other"); !slices.Equal(got, []capabilityv3.CommandState{succeeded}) {
		t.Fatalf("an event of a command the node does not run = %v", got)
	}
	if lane := n.rec.all()[len(n.rec.all())-1].GetLane(); lane != edgelinkv3.Lane_LANE_COMMAND_REPLY {
		t.Fatalf("lane = %v", lane)
	}
}

func TestAnAdapterRefusalIsRejected(t *testing.T) {
	n := startNode(t, local.Config{})
	a := startAdapter(t, n.dir, "a")
	n.register(t, a, asset)
	a.set(func(*edgev3.ExecuteCommandRequest) *capabilityv3.CommandResult {
		return &capabilityv3.CommandResult{State: rejected, Error: &commonv3.Error{Code: "flight.not_landed", Message: "airborne"}}
	})
	n.deliver(t, "c-1", takeoff(asset))
	waitFor(t, "the outcome", func() bool { return len(n.rec.states("c-1")) == 2 })
	equalStates(t, n.rec.states("c-1"), accepted, rejected)
	if n.rec.last("c-1").GetError().GetCode() != "flight.not_landed" {
		t.Fatalf("event = %v", n.rec.last("c-1"))
	}
}

func TestACommandForAnAssetNoAdapterServesIsRejected(t *testing.T) {
	n := startNode(t, local.Config{})
	n.deliver(t, "c-1", takeoff("NOBODY"))
	waitFor(t, "the outcome", func() bool { return len(n.rec.states("c-1")) == 2 })
	equalStates(t, n.rec.states("c-1"), accepted, rejected)
	if n.rec.last("c-1").GetError().GetCode() != local.CodeNoAdapter {
		t.Fatalf("event = %v", n.rec.last("c-1"))
	}
}

func TestAnUnreachableAdapterFailsTheCommand(t *testing.T) {
	n := startNode(t, local.Config{DefaultAdapterEndpoint: "unix://" + filepath.Join(socketDir(t), "gone.sock")})
	if _, err := n.gateway().ReportCapabilities(context.Background(), &edgev3.ReportCapabilitiesRequest{
		Capabilities: &capabilityv3.CapabilitySet{AssetSn: asset},
	}); err != nil {
		t.Fatal(err)
	}
	cmd := takeoff(asset)
	cmd.Timeout = durationpb.New(5 * time.Second)
	n.deliver(t, "c-1", cmd)
	waitFor(t, "the outcome", func() bool { return len(n.rec.states("c-1")) == 2 })
	equalStates(t, n.rec.states("c-1"), accepted, failed)
	if n.rec.last("c-1").GetError().GetCode() != local.CodeAdapterUnreachable {
		t.Fatalf("event = %v", n.rec.last("c-1"))
	}
}

func TestCancelAndTimeoutReachTheAdapter(t *testing.T) {
	n := startNode(t, local.Config{})
	a := startAdapter(t, n.dir, "a")
	n.register(t, a, asset)
	a.set(func(*edgev3.ExecuteCommandRequest) *capabilityv3.CommandResult {
		return &capabilityv3.CommandResult{State: accepted}
	})
	n.deliver(t, "c-1", takeoff(asset))
	slow := takeoff(asset)
	slow.Timeout = durationpb.New(50 * time.Millisecond)
	n.deliver(t, "c-2", slow)
	waitFor(t, "both running", func() bool {
		var calls int
		a.read(func(a *adapter) { calls = len(a.executed) })
		return calls == 2
	})
	n.cancel(t, "c-1")
	waitFor(t, "both outcomes", func() bool { return len(n.rec.states("c-1")) == 2 && len(n.rec.states("c-2")) == 2 })
	equalStates(t, n.rec.states("c-1"), accepted, cancelled)
	equalStates(t, n.rec.states("c-2"), accepted, timedOut)
	a.read(func(a *adapter) {
		if !slices.Contains(a.cancelled, "c-1") || !slices.Contains(a.cancelled, "c-2") {
			t.Errorf("adapter cancels = %v", a.cancelled)
		}
	})
	n.publishEvent(t, "c-1", cancelled)
	if got := len(n.rec.states("c-1")); got != 2 {
		t.Fatalf("the adapter's own cancel event was published as well: %v", n.rec.states("c-1"))
	}
}

func TestQueueStatsAndThrottleReachAdapters(t *testing.T) {
	lanes := store.DefaultLanes()
	lanes[edgelinkv3.Lane_LANE_TELEMETRY] = store.LanePolicy{MaxItems: 10, Overflow: store.Thin}
	st, err := store.Open(filepath.Join(t.TempDir(), "node.db"), store.Options{Lanes: lanes, FlushInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connected := false
	var mu sync.Mutex
	n := startNode(t, local.Config{Store: st, WatchInterval: 10 * time.Millisecond, Connected: func() bool {
		mu.Lock()
		defer mu.Unlock()
		return connected
	}})
	client := local.NewQueueClient(n.conn)
	q, err := client.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tel, ok := q.Lane(edgelinkv3.Lane_LANE_TELEMETRY)
	if q.Connected || len(q.Lanes) != 5 || !ok || tel.Throttle != 1 || tel.MaxItems != 10 {
		t.Fatalf("stats = %+v", q)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	updates := make(chan local.QueueStats, 16)
	go func() {
		_ = client.Watch(ctx, func(q local.QueueStats) error {
			updates <- q
			return nil
		})
	}()
	next := func(what string, cond func(local.QueueStats) bool) local.QueueStats {
		t.Helper()
		timeout := time.After(5 * time.Second)
		for {
			select {
			case q := <-updates:
				if cond(q) {
					return q
				}
			case <-timeout:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	next("the first stats", func(local.QueueStats) bool { return true })

	for range 8 {
		if _, err := st.Append(context.Background(), store.Item{Lane: edgelinkv3.Lane_LANE_TELEMETRY, Payload: []byte("s")}); err != nil {
			t.Fatal(err)
		}
	}
	q = next("a throttle", func(q local.QueueStats) bool {
		l, _ := q.Lane(edgelinkv3.Lane_LANE_TELEMETRY)
		return l.Throttle < 1
	})
	if l, _ := q.Lane(edgelinkv3.Lane_LANE_TELEMETRY); l.Items != 8 || l.Fill != 0.8 || l.Throttle != 0.5 || l.Oldest.IsZero() {
		t.Fatalf("telemetry = %+v", l)
	}
	mu.Lock()
	connected = true
	mu.Unlock()
	next("connected", func(q local.QueueStats) bool { return q.Connected })
}

func TestThrottle(t *testing.T) {
	for fill, want := range map[float64]float64{0: 1, 0.5: 1, 0.7: 0.6, 0.8: 0.5, 1: 0.1, 2: 0.1} {
		if got := local.Throttle(fill); got != want {
			t.Errorf("Throttle(%v) = %v, want %v", fill, got, want)
		}
	}
}
