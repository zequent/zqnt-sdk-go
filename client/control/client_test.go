package control_test

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zequent/zqnt-client-sdk-go/v3/auth"
	"github.com/zequent/zqnt-client-sdk-go/v3/control"
	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	controlv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/control/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeRemoteControl struct {
	controlv3.UnimplementedRemoteControlServiceServer

	mu          sync.Mutex
	capRequest  *controlv3.GetCapabilitiesRequest
	execRequest *controlv3.ExecuteCommandRequest
	cancelReq   *controlv3.CancelCommandRequest
	watchReq    *controlv3.WatchCommandEventsRequest
	authHeader  string
	events      chan *capabilityv3.CommandEvent
}

func newFake() *fakeRemoteControl {
	return &fakeRemoteControl{events: make(chan *capabilityv3.CommandEvent, 16)}
}

func (f *fakeRemoteControl) GetCapabilities(ctx context.Context, req *controlv3.GetCapabilitiesRequest) (*controlv3.GetCapabilitiesResponse, error) {
	f.mu.Lock()
	f.capRequest = req
	f.mu.Unlock()
	if req.GetAsset().GetSn() == "" {
		return nil, status.Error(codes.InvalidArgument, "asset.sn is required")
	}
	return &controlv3.GetCapabilitiesResponse{Capabilities: &capabilityv3.CapabilitySet{
		AssetSn: req.GetAsset().GetSn(),
		Capabilities: []*capabilityv3.Capability{
			{CommandId: "flight.takeoff", Completion: capabilityv3.CompletionMode_COMPLETION_MODE_ASYNCHRONOUS},
			{CommandId: "dock.open_cover", Completion: capabilityv3.CompletionMode_COMPLETION_MODE_ON_REPLY},
		},
		SnapshotState: capabilityv3.SnapshotState_SNAPSHOT_STATE_CURRENT,
	}}, nil
}

func (f *fakeRemoteControl) ExecuteCommand(ctx context.Context, req *controlv3.ExecuteCommandRequest) (*controlv3.ExecuteCommandResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	f.mu.Lock()
	f.execRequest = req
	f.authHeader = strings.Join(md.Get("authorization"), ",")
	f.mu.Unlock()
	command := req.GetCommand()
	result := &capabilityv3.CommandResult{CommandExecutionId: "exec-1", CommandId: command.GetCommandId()}
	switch command.GetCommandId() {
	case "dock.open_cover":
		result.State = capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED
		result.Result, _ = structpb.NewStruct(map[string]any{"cover": "OPEN"})
	case "flight.takeoff":
		result.State = capabilityv3.CommandState_COMMAND_STATE_ACCEPTED
		f.events <- &capabilityv3.CommandEvent{CommandExecutionId: "other", CommandId: "camera.photo",
			State: capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED}
		f.events <- &capabilityv3.CommandEvent{CommandExecutionId: "exec-1", CommandId: "flight.takeoff",
			State: capabilityv3.CommandState_COMMAND_STATE_RUNNING}
		final := &capabilityv3.CommandEvent{CommandExecutionId: "exec-1", CommandId: "flight.takeoff",
			State: capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED}
		final.Result, _ = structpb.NewStruct(map[string]any{"altitude": 40})
		f.events <- final
	case "flight.land":
		result.State = capabilityv3.CommandState_COMMAND_STATE_ACCEPTED
		f.events <- &capabilityv3.CommandEvent{CommandExecutionId: "exec-1", CommandId: "flight.land",
			State: capabilityv3.CommandState_COMMAND_STATE_FAILED,
			Error: &commonv3.Error{Category: commonv3.ErrorCategory_ERROR_CATEGORY_ASSET, Code: "flight.motor_fault", Message: "motor 2"}}
	case "flight.unknown":
		result.CommandExecutionId = ""
		result.State = capabilityv3.CommandState_COMMAND_STATE_REJECTED
		result.Error = &commonv3.Error{Category: commonv3.ErrorCategory_ERROR_CATEGORY_PRECONDITION_FAILED,
			Code: "flight.not_airborne", Message: "aircraft is on the ground", Retryable: true}
	case "flight.forbidden":
		return nil, status.Error(codes.PermissionDenied, "Asset DRONE-1 is not one of this credential's organization's assets")
	}
	return &controlv3.ExecuteCommandResponse{Result: result}, nil
}

func (f *fakeRemoteControl) CancelCommand(ctx context.Context, req *controlv3.CancelCommandRequest) (*controlv3.CancelCommandResponse, error) {
	f.mu.Lock()
	f.cancelReq = req
	f.mu.Unlock()
	state := capabilityv3.CommandState_COMMAND_STATE_CANCELLED
	if req.GetCommandExecutionId() == "finished" {
		state = capabilityv3.CommandState_COMMAND_STATE_REJECTED
	}
	return &controlv3.CancelCommandResponse{Result: &capabilityv3.CommandResult{
		CommandExecutionId: req.GetCommandExecutionId(), State: state}}, nil
}

func (f *fakeRemoteControl) WatchCommandEvents(req *controlv3.WatchCommandEventsRequest, stream grpc.ServerStreamingServer[controlv3.WatchCommandEventsResponse]) error {
	f.mu.Lock()
	f.watchReq = req
	f.mu.Unlock()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case event := <-f.events:
			if err := stream.Send(&controlv3.WatchCommandEventsResponse{Event: event}); err != nil {
				return err
			}
			if req.GetCommandExecutionId() != "" && control.Terminal(event.GetState()) {
				return nil
			}
		}
	}
}

func dial(t *testing.T, fake *fakeRemoteControl, token string) *control.Client {
	t.Helper()
	t.Setenv(auth.EnvVar, "")
	lis := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	controlv3.RegisterRemoteControlServiceServer(server, fake)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	opts := append([]grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, auth.DialOptions(token)...)
	conn, err := grpc.NewClient("passthrough:///bufnet", opts...)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return control.New(conn)
}

func timeout(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestListCapabilitiesReturnsTheAssetsCommands(t *testing.T) {
	fake := newFake()
	set, err := dial(t, fake, "tok").ListCapabilities(timeout(t), "DOCK-1",
		control.WithTarget(capabilityv3.TargetType_TARGET_TYPE_PAYLOAD, "speaker-0"))
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if set.GetAssetSn() != "DOCK-1" || len(set.GetCapabilities()) != 2 || set.GetCapabilities()[0].GetCommandId() != "flight.takeoff" {
		t.Fatalf("capabilities = %v", set)
	}
	req := fake.capRequest
	if req.GetTarget().GetType() != capabilityv3.TargetType_TARGET_TYPE_PAYLOAD || req.GetTarget().GetRef() != "speaker-0" {
		t.Fatalf("target = %v", req.GetTarget())
	}
	if !strings.HasPrefix(req.GetContext().GetRequestId(), "client-go-sdk-") {
		t.Fatalf("request id = %q", req.GetContext().GetRequestId())
	}
}

func TestExecuteCommandSendsTheIdAndParamsAsAStruct(t *testing.T) {
	fake := newFake()
	result, err := dial(t, fake, "tok-1").ExecuteCommand(timeout(t), "DOCK-1", "dock.open_cover",
		control.Params{"latitude": 52.52, "longitude": 13.405, "altitude": 60, "tags": []any{"a"}},
		control.WithTimeout(90*time.Second), control.WithReason("inspection"), control.WithNoFlyZoneOverride(),
		control.WithIdempotencyKey("key-1"), control.WithRequestID("req-1"))
	if err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if result.GetState() != capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED || result.GetResult().AsMap()["cover"] != "OPEN" {
		t.Fatalf("result = %v", result)
	}
	req := fake.execRequest
	command := req.GetCommand()
	if command.GetAsset().GetSn() != "DOCK-1" || command.GetCommandId() != "dock.open_cover" {
		t.Fatalf("command = %v", command)
	}
	params := command.GetParams().GetFields()
	if params["latitude"].GetNumberValue() != 52.52 || params["altitude"].GetNumberValue() != 60 ||
		params["tags"].GetListValue().GetValues()[0].GetStringValue() != "a" {
		t.Fatalf("params = %v", command.GetParams())
	}
	if command.GetTimeout().AsDuration() != 90*time.Second || req.GetReason() != "inspection" || !req.GetNoFlyZoneOverride() {
		t.Fatalf("options not sent: %v", req)
	}
	if req.GetContext().GetRequestId() != "req-1" || req.GetContext().GetIdempotencyKey() != "key-1" {
		t.Fatalf("context = %v", req.GetContext())
	}
	if fake.authHeader != "Bearer tok-1" {
		t.Fatalf("authorization = %q", fake.authHeader)
	}
}

func TestExecuteCommandWithoutParamsSendsNone(t *testing.T) {
	fake := newFake()
	if _, err := dial(t, fake, "tok").ExecuteCommand(timeout(t), "DOCK-1", "dock.open_cover", nil); err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if fake.execRequest.GetCommand().GetParams() != nil || fake.execRequest.GetNoFlyZoneOverride() {
		t.Fatalf("request = %v", fake.execRequest)
	}
}

func TestParamsThatAreNotJSONAreRefusedBeforeSending(t *testing.T) {
	fake := newFake()
	_, err := dial(t, fake, "tok").ExecuteCommand(timeout(t), "DOCK-1", "dock.open_cover", control.Params{"when": time.Now()})
	if err == nil || !strings.Contains(err.Error(), "params") {
		t.Fatalf("err = %v", err)
	}
	if fake.execRequest != nil {
		t.Fatal("nothing must be sent")
	}
}

func TestARejectedCommandIsACommandError(t *testing.T) {
	_, err := dial(t, newFake(), "tok").ExecuteCommand(timeout(t), "DRONE-1", "flight.unknown", nil)
	var commandErr *control.CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("err = %T %v", err, err)
	}
	if commandErr.State() != capabilityv3.CommandState_COMMAND_STATE_REJECTED ||
		commandErr.Category() != commonv3.ErrorCategory_ERROR_CATEGORY_PRECONDITION_FAILED || !commandErr.Retryable() {
		t.Fatalf("command error = %v", commandErr.Result)
	}
	if err.Error() != "control: flight.unknown rejected: flight.not_airborne: aircraft is on the ground" {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestARefusedCallKeepsItsStatusCodeAndSaysWhatToDo(t *testing.T) {
	_, err := dial(t, newFake(), "tok").ExecuteCommand(timeout(t), "DRONE-1", "flight.forbidden", nil)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v (%v)", status.Code(err), err)
	}
	if !strings.Contains(err.Error(), "own organization") || !strings.Contains(err.Error(), "ExecuteCommand(DRONE-1, flight.forbidden)") {
		t.Fatalf("message = %v", err)
	}
	var commandErr *control.CommandError
	if errors.As(err, &commandErr) {
		t.Fatal("a refused call is not a command error")
	}
}

func TestListCapabilitiesKeepsInvalidArgument(t *testing.T) {
	_, err := dial(t, newFake(), "tok").ListCapabilities(timeout(t), "")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v (%v)", status.Code(err), err)
	}
}

func TestExecuteAndWaitReturnsTheOutcomeOfThisRun(t *testing.T) {
	fake := newFake()
	result, err := dial(t, fake, "tok").ExecuteAndWait(timeout(t), "DRONE-1", "flight.takeoff", control.Params{"altitude": 40})
	if err != nil {
		t.Fatalf("ExecuteAndWait: %v", err)
	}
	if result.GetCommandExecutionId() != "exec-1" || result.GetState() != capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED ||
		result.GetResult().AsMap()["altitude"] != float64(40) {
		t.Fatalf("result = %v", result)
	}
	if fake.watchReq.GetAsset().GetSn() != "DRONE-1" {
		t.Fatalf("watch = %v", fake.watchReq)
	}
}

func TestExecuteAndWaitReturnsAFailedOutcomeAsACommandError(t *testing.T) {
	_, err := dial(t, newFake(), "tok").ExecuteAndWait(timeout(t), "DRONE-1", "flight.land", nil)
	var commandErr *control.CommandError
	if !errors.As(err, &commandErr) || commandErr.State() != capabilityv3.CommandState_COMMAND_STATE_FAILED ||
		commandErr.Category() != commonv3.ErrorCategory_ERROR_CATEGORY_ASSET {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "flight.land failed (exec-1): flight.motor_fault: motor 2") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestWatchCommandStreamsUntilTheFinalEvent(t *testing.T) {
	fake := newFake()
	fake.events <- &capabilityv3.CommandEvent{CommandExecutionId: "exec-9", State: capabilityv3.CommandState_COMMAND_STATE_RUNNING}
	fake.events <- &capabilityv3.CommandEvent{CommandExecutionId: "exec-9", State: capabilityv3.CommandState_COMMAND_STATE_CANCELLED}
	events, err := dial(t, fake, "tok").WatchCommand(timeout(t), "exec-9")
	if err != nil {
		t.Fatalf("WatchCommand: %v", err)
	}
	var states []capabilityv3.CommandState
	for {
		event, err := events.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		states = append(states, event.GetState())
	}
	if len(states) != 2 || states[1] != capabilityv3.CommandState_COMMAND_STATE_CANCELLED {
		t.Fatalf("states = %v", states)
	}
	if fake.watchReq.GetCommandExecutionId() != "exec-9" || fake.watchReq.GetAsset() != nil {
		t.Fatalf("watch = %v", fake.watchReq)
	}
}

func TestCancelCommand(t *testing.T) {
	fake := newFake()
	client := dial(t, fake, "tok")
	result, err := client.CancelCommand(timeout(t), "exec-1", "operator abort")
	if err != nil || result.GetState() != capabilityv3.CommandState_COMMAND_STATE_CANCELLED {
		t.Fatalf("CancelCommand: %v %v", result, err)
	}
	if fake.cancelReq.GetReason() != "operator abort" || fake.cancelReq.GetCommandExecutionId() != "exec-1" {
		t.Fatalf("request = %v", fake.cancelReq)
	}
	if _, err := client.CancelCommand(timeout(t), "finished", ""); err == nil {
		t.Fatal("a refused cancel is an error")
	}
}
