package simulation_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	simulationv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/application_simulation/v3"
	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	"github.com/zequent/zqnt-sdk-go/v3/client/simulation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeSimulations struct {
	simulationv3.UnimplementedSimulationServiceServer
	mu           sync.Mutex
	start        *simulationv3.StartSimulationRequest
	traceRequest *simulationv3.GetSimulationTraceRequest
	cancel       *simulationv3.CancelSimulationRequest
	refuse       error
	statuses     []simulationv3.SimulationStatus
	traceCalls   int
}

func (f *fakeSimulations) StartSimulation(_ context.Context, req *simulationv3.StartSimulationRequest) (*simulationv3.StartSimulationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.start = req
	return &simulationv3.StartSimulationResponse{ExecutionId: "exec-1", Status: simulationv3.SimulationStatus_SIMULATION_STATUS_RUNNING}, nil
}

func (f *fakeSimulations) GetSimulationTrace(_ context.Context, req *simulationv3.GetSimulationTraceRequest) (*simulationv3.GetSimulationTraceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.traceRequest = req
	f.traceCalls++
	if f.refuse != nil {
		return nil, f.refuse
	}
	st := simulationv3.SimulationStatus_SIMULATION_STATUS_RUNNING
	if len(f.statuses) > 0 {
		st = f.statuses[0]
		if len(f.statuses) > 1 {
			f.statuses = f.statuses[1:]
		}
	}
	return &simulationv3.GetSimulationTraceResponse{Trace: traceOf(req.GetExecutionId(), st)}, nil
}

func (f *fakeSimulations) CancelSimulation(_ context.Context, req *simulationv3.CancelSimulationRequest) (*simulationv3.CancelSimulationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancel = req
	return &simulationv3.CancelSimulationResponse{Status: simulationv3.SimulationStatus_SIMULATION_STATUS_CANCELLING}, nil
}

func traceOf(executionID string, st simulationv3.SimulationStatus) *simulationv3.SimulationTrace {
	output, _ := structpb.NewStruct(map[string]any{"battery_percent": 18})
	return &simulationv3.SimulationTrace{
		ExecutionId: executionID,
		Status:      st,
		AssetSn:     "VIRTUAL-exec-1-DRONE-1",
		FastForward: true,
		Steps: []*simulationv3.TraceStep{{
			NodeId:    "takeoff",
			CommandId: "flight.takeoff",
			Status:    simulationv3.NodeStatus_NODE_STATUS_FAILED,
			Output:    output,
			Next: []*simulationv3.TraceTransition{{
				TargetNodeId: "return",
				EdgeType:     simulationv3.EdgeType_EDGE_TYPE_FAILURE,
				Reason:       "failed: flight.not_airborne",
			}},
			SimulatedBy: "scenario: failure",
		}},
		SkippedNodeIds: []string{"land"},
	}
}

func dial(t *testing.T, fake *fakeSimulations) *simulation.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	simulationv3.RegisterSimulationServiceServer(server, fake)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return simulation.New(conn)
}

func timeout(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestStartSimulationGeneratesAnIdempotencyKeyWhenTheCallerGivesNone(t *testing.T) {
	fake := &fakeSimulations{}
	battery := 18.0
	req := &simulationv3.StartSimulationRequest{
		ApplicationId: "app-1",
		Nodes: []*simulationv3.NodeScenario{{
			CommandId: "flight.takeoff",
			Outcome:   simulationv3.NodeOutcome_NODE_OUTCOME_FAILURE,
			ErrorCode: "flight.not_airborne",
		}},
		Fleet: []*simulationv3.VirtualAsset{{Sn: "DRONE-1", BatteryPercent: &battery}},
	}

	started, err := dial(t, fake).StartSimulation(timeout(t), req)
	if err != nil {
		t.Fatalf("StartSimulation: %v", err)
	}
	if started.GetExecutionId() != "exec-1" || started.GetStatus() != simulationv3.SimulationStatus_SIMULATION_STATUS_RUNNING {
		t.Fatalf("started = %v", started)
	}
	if !strings.HasPrefix(fake.start.GetContext().GetIdempotencyKey(), "client-go-sdk-") || fake.start.GetContext().GetRequestId() == "" {
		t.Fatalf("context = %v", fake.start.GetContext())
	}
	sent := proto.Clone(fake.start).(*simulationv3.StartSimulationRequest)
	sent.Context = nil
	if !proto.Equal(sent, req) {
		t.Fatalf("sent = %v, want %v", sent, req)
	}
	if req.GetContext() != nil {
		t.Fatalf("the caller's request was changed: %v", req.GetContext())
	}
}

func TestStartSimulationKeepsTheCallersIdempotencyKey(t *testing.T) {
	fake := &fakeSimulations{}
	_, err := dial(t, fake).StartSimulation(timeout(t), &simulationv3.StartSimulationRequest{
		Context:       &commonv3.RequestContext{IdempotencyKey: "retry-me"},
		ApplicationId: "app-1",
	})
	if err != nil || fake.start.GetContext().GetIdempotencyKey() != "retry-me" {
		t.Fatalf("key = %q, %v", fake.start.GetContext().GetIdempotencyKey(), err)
	}
}

func TestStartSimulationWithoutAnApplicationIsRefusedBeforeItIsSent(t *testing.T) {
	fake := &fakeSimulations{}
	if _, err := dial(t, fake).StartSimulation(timeout(t), &simulationv3.StartSimulationRequest{}); err == nil || fake.start != nil {
		t.Fatalf("err = %v, sent = %v", err, fake.start)
	}
}

func TestSimulationTraceIsTheOneThePlatformAnswers(t *testing.T) {
	fake := &fakeSimulations{statuses: []simulationv3.SimulationStatus{simulationv3.SimulationStatus_SIMULATION_STATUS_FAILED}}

	trace, err := dial(t, fake).SimulationTrace(timeout(t), "exec-1")
	if err != nil {
		t.Fatalf("SimulationTrace: %v", err)
	}
	if fake.traceRequest.GetExecutionId() != "exec-1" {
		t.Fatalf("execution id = %q", fake.traceRequest.GetExecutionId())
	}
	if want := traceOf("exec-1", simulationv3.SimulationStatus_SIMULATION_STATUS_FAILED); !proto.Equal(trace, want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}
}

func TestCancelSimulationReturnsTheStatus(t *testing.T) {
	fake := &fakeSimulations{}
	st, err := dial(t, fake).CancelSimulation(timeout(t), "exec-1")
	if err != nil || st != simulationv3.SimulationStatus_SIMULATION_STATUS_CANCELLING || fake.cancel.GetExecutionId() != "exec-1" {
		t.Fatalf("status = %v, %v", st, err)
	}
}

func TestSimulationTraceKeepsTheGrpcStatusOfARefusal(t *testing.T) {
	fake := &fakeSimulations{refuse: status.Error(codes.NotFound, "no simulation exec-9")}
	if _, err := dial(t, fake).SimulationTrace(timeout(t), "exec-9"); status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v (%v)", status.Code(err), err)
	}
}

func TestWaitForSimulationPollsUntilTheRunHasEnded(t *testing.T) {
	fake := &fakeSimulations{statuses: []simulationv3.SimulationStatus{
		simulationv3.SimulationStatus_SIMULATION_STATUS_PENDING,
		simulationv3.SimulationStatus_SIMULATION_STATUS_RUNNING,
		simulationv3.SimulationStatus_SIMULATION_STATUS_CANCELLING,
		simulationv3.SimulationStatus_SIMULATION_STATUS_CANCELLED,
	}}

	trace, err := dial(t, fake).WaitForSimulation(timeout(t), "exec-1", 10*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitForSimulation: %v", err)
	}
	if trace.GetStatus() != simulationv3.SimulationStatus_SIMULATION_STATUS_CANCELLED || fake.traceCalls != 4 {
		t.Fatalf("status = %v after %d calls", trace.GetStatus(), fake.traceCalls)
	}
}

func TestWaitForSimulationTimesOutWhileTheRunKeepsGoing(t *testing.T) {
	fake := &fakeSimulations{}
	_, err := dial(t, fake).WaitForSimulation(timeout(t), "exec-1", 10*time.Millisecond, 100*time.Millisecond)
	if !errors.Is(err, simulation.ErrWaitTimedOut) {
		t.Fatalf("err = %v", err)
	}
	if fake.cancel != nil {
		t.Fatalf("the run was cancelled")
	}
}

func TestSimulateStartsAndWaitsForTheTrace(t *testing.T) {
	fake := &fakeSimulations{statuses: []simulationv3.SimulationStatus{
		simulationv3.SimulationStatus_SIMULATION_STATUS_RUNNING,
		simulationv3.SimulationStatus_SIMULATION_STATUS_SUCCEEDED,
	}}
	trace, err := dial(t, fake).Simulate(timeout(t), &simulationv3.StartSimulationRequest{ApplicationId: "app-1"}, 10*time.Millisecond, 5*time.Second)
	if err != nil || trace.GetStatus() != simulationv3.SimulationStatus_SIMULATION_STATUS_SUCCEEDED || fake.traceRequest.GetExecutionId() != "exec-1" {
		t.Fatalf("trace = %v, %v", trace, err)
	}
}
