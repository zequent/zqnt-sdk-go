package command_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/command"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type recorder struct {
	mu     sync.Mutex
	events []*capabilityv3.CommandEvent
	lanes  []edgelinkv3.Lane
	fail   atomic.Bool
}

func (r *recorder) Publish(_ context.Context, env *edgelinkv3.Envelope) (*edgelinkv3.MessageId, error) {
	if r.fail.Load() {
		return nil, errors.New("store full")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, env.GetCommandEvent())
	r.lanes = append(r.lanes, env.GetLane())
	return &edgelinkv3.MessageId{Epoch: 1, Seq: uint32(len(r.events))}, nil
}

func (r *recorder) states() []capabilityv3.CommandState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]capabilityv3.CommandState, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, ev.GetState())
	}
	return out
}

func (r *recorder) last() *capabilityv3.CommandEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[len(r.events)-1]
}

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func runner(t *testing.T, s *store.Store, pub command.Publisher, h command.Handler) *command.Runner {
	t.Helper()
	r, err := command.New(command.Config{Store: s, Publisher: pub, Handler: h, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func delivery(id string, cmd *capabilityv3.Command) *edgelinkv3.DownlinkFrame {
	return &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Command{Command: &edgelinkv3.CommandDelivery{
		Id:                 &edgelinkv3.MessageId{Epoch: 1, Seq: 1},
		CommandExecutionId: id,
		Action:             &edgelinkv3.CommandDelivery_Execute{Execute: cmd},
	}}}
}

func cancelFrame(id string) *edgelinkv3.DownlinkFrame {
	return &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Command{Command: &edgelinkv3.CommandDelivery{
		Id:                 &edgelinkv3.MessageId{Epoch: 1, Seq: 2},
		CommandExecutionId: id,
		Action:             &edgelinkv3.CommandDelivery_Cancel{Cancel: &edgelinkv3.CommandCancel{Reason: "operator"}},
	}}}
}

func takeoff() *capabilityv3.Command {
	return &capabilityv3.Command{
		Asset:     &commonv3.AssetRef{Sn: "SIM-DRONE-1"},
		CommandId: "flight.takeoff",
		ExpiresAt: timestamppb.New(now.Add(time.Minute)),
		Execution: &capabilityv3.ExecutionRef{ExecutionId: "exec-1", NodeId: "n1"},
	}
}

func succeed(calls *atomic.Int32) command.HandlerFunc {
	return func(_ context.Context, cmd *capabilityv3.Command, progress command.Progress) (*structpb.Struct, error) {
		calls.Add(1)
		progress(0.5, "climbing")
		return structpb.NewStruct(map[string]any{"altitude": 40})
	}
}

func equalStates(t *testing.T, got []capabilityv3.CommandState, want ...capabilityv3.CommandState) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("states = %v, want %v", got, want)
		}
	}
}

func TestACommandRunsAndReportsEveryStateInTheCommandReplyLane(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "node.db"))
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	var calls atomic.Int32
	r := runner(t, s, pub, succeed(&calls))

	if err := r.HandleFrame(context.Background(), delivery("ce-1", takeoff())); err != nil {
		t.Fatal(err)
	}
	r.Wait()

	equalStates(t, pub.states(), capabilityv3.CommandState_COMMAND_STATE_ACCEPTED,
		capabilityv3.CommandState_COMMAND_STATE_RUNNING, capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED)
	for _, lane := range pub.lanes {
		if lane != edgelinkv3.Lane_LANE_COMMAND_REPLY {
			t.Fatalf("lane = %v", lane)
		}
	}
	done := pub.last()
	if done.GetCommandExecutionId() != "ce-1" || done.GetAsset().GetSn() != "SIM-DRONE-1" ||
		done.GetExecution().GetExecutionId() != "exec-1" || done.GetOccurredAt() == nil {
		t.Fatalf("event = %v", done)
	}
	if done.GetResult().GetFields()["altitude"].GetNumberValue() != 40 {
		t.Fatalf("result = %v", done.GetResult())
	}
	if pub.events[1].GetProgress() != 0.5 {
		t.Fatalf("progress = %v", pub.events[1].GetProgress())
	}
}

func TestACommandDeliveredTwiceRunsOnce(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "node.db"))
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	var calls atomic.Int32
	r := runner(t, s, pub, succeed(&calls))

	for range 3 {
		if err := r.HandleFrame(context.Background(), delivery("ce-1", takeoff())); err != nil {
			t.Fatal(err)
		}
	}
	r.Wait()

	if calls.Load() != 1 {
		t.Fatalf("ran %d times", calls.Load())
	}
	equalStates(t, pub.states(), capabilityv3.CommandState_COMMAND_STATE_ACCEPTED,
		capabilityv3.CommandState_COMMAND_STATE_RUNNING, capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED)
}

func TestAnExpiredCommandIsRefused(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "node.db"))
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	var calls atomic.Int32
	r := runner(t, s, pub, succeed(&calls))
	cmd := takeoff()
	cmd.ExpiresAt = timestamppb.New(now.Add(-time.Second))

	if err := r.HandleFrame(context.Background(), delivery("ce-1", cmd)); err != nil {
		t.Fatal(err)
	}
	_ = r.HandleFrame(context.Background(), delivery("ce-1", cmd))
	r.Wait()

	if calls.Load() != 0 {
		t.Fatal("an expired command ran")
	}
	equalStates(t, pub.states(), capabilityv3.CommandState_COMMAND_STATE_REJECTED)
	if code := pub.last().GetError().GetCode(); code != command.CodeExpired {
		t.Fatalf("code = %q", code)
	}
}

func TestTheTimeoutCountsFromAccepted(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "node.db"))
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	r := runner(t, s, pub, command.HandlerFunc(func(ctx context.Context, _ *capabilityv3.Command, _ command.Progress) (*structpb.Struct, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	cmd := takeoff()
	cmd.Timeout = durationpb.New(50 * time.Millisecond)

	started := time.Now()
	if err := r.HandleFrame(context.Background(), delivery("ce-1", cmd)); err != nil {
		t.Fatal(err)
	}
	r.Wait()

	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Fatalf("timed out after %v", elapsed)
	}
	equalStates(t, pub.states(), capabilityv3.CommandState_COMMAND_STATE_ACCEPTED, capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT)
	if code := pub.last().GetError().GetCode(); code != command.CodeTimeout {
		t.Fatalf("code = %q", code)
	}
}

func TestAFailureCarriesTheHandlersCode(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "node.db"))
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	r := runner(t, s, pub, command.HandlerFunc(func(context.Context, *capabilityv3.Command, command.Progress) (*structpb.Struct, error) {
		return nil, &command.Error{Code: "flight.not_airborne", Message: "on the ground"}
	}))

	_ = r.HandleFrame(context.Background(), delivery("ce-1", takeoff()))
	r.Wait()

	equalStates(t, pub.states(), capabilityv3.CommandState_COMMAND_STATE_ACCEPTED, capabilityv3.CommandState_COMMAND_STATE_FAILED)
	if e := pub.last().GetError(); e.GetCode() != "flight.not_airborne" || e.GetCategory() != commonv3.ErrorCategory_ERROR_CATEGORY_ASSET {
		t.Fatalf("error = %v", e)
	}
}

func TestACancelStopsARunningCommand(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "node.db"))
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	started := make(chan struct{})
	r := runner(t, s, pub, command.HandlerFunc(func(ctx context.Context, _ *capabilityv3.Command, _ command.Progress) (*structpb.Struct, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}))

	_ = r.HandleFrame(context.Background(), delivery("ce-1", takeoff()))
	<-started
	_ = r.HandleFrame(context.Background(), cancelFrame("ce-1"))
	r.Wait()

	equalStates(t, pub.states(), capabilityv3.CommandState_COMMAND_STATE_ACCEPTED, capabilityv3.CommandState_COMMAND_STATE_CANCELLED)
}

func TestACommandWhoseAcceptanceCouldNotBeQueuedIsLeftForTheNextDelivery(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "node.db"))
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	var calls atomic.Int32
	r := runner(t, s, pub, succeed(&calls))

	pub.fail.Store(true)
	if err := r.HandleFrame(context.Background(), delivery("ce-1", takeoff())); err == nil {
		t.Fatal("the frame was acknowledged although nothing was queued")
	}
	pub.fail.Store(false)
	if err := r.HandleFrame(context.Background(), delivery("ce-1", takeoff())); err != nil {
		t.Fatal(err)
	}
	r.Wait()

	if calls.Load() != 1 {
		t.Fatalf("ran %d times", calls.Load())
	}
}

func TestARestartBetweenReceiptAndExecutionNeverRunsTheCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.db")
	s := openStore(t, path)
	claimed, err := s.ClaimCommand("ce-1", mustDelivery(t, "ce-1"))
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openStore(t, path)
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	var calls atomic.Int32
	r := runner(t, s, pub, succeed(&calls))
	if err := r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.HandleFrame(context.Background(), delivery("ce-1", takeoff())); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.Wait()

	if calls.Load() != 0 {
		t.Fatal("the command ran after the restart")
	}
	equalStates(t, pub.states(), capabilityv3.CommandState_COMMAND_STATE_FAILED)
	if ev := pub.last(); ev.GetError().GetCode() != command.CodeInterrupted || ev.GetAsset().GetSn() != "SIM-DRONE-1" ||
		ev.GetCommandId() != "flight.takeoff" {
		t.Fatalf("event = %v", ev)
	}
}

func TestARestartBeforeTheOutcomeWasQueuedReportsTheCommandInterrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.db")
	s := openStore(t, path)
	pub := &recorder{}
	started, release := make(chan struct{}), make(chan struct{})
	r := runner(t, s, pub, command.HandlerFunc(func(context.Context, *capabilityv3.Command, command.Progress) (*structpb.Struct, error) {
		close(started)
		<-release
		return nil, nil
	}))
	_ = r.HandleFrame(context.Background(), delivery("ce-1", takeoff()))
	<-started
	pub.fail.Store(true)
	close(release)
	r.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openStore(t, path)
	defer func() { _ = s.Close() }()
	after := &recorder{}
	var calls atomic.Int32
	r = runner(t, s, after, succeed(&calls))
	if err := r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = r.HandleFrame(context.Background(), delivery("ce-1", takeoff()))
	r.Wait()

	if calls.Load() != 0 {
		t.Fatal("the command ran twice")
	}
	equalStates(t, after.states(), capabilityv3.CommandState_COMMAND_STATE_FAILED)
}

func TestOtherFramesPass(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "node.db"))
	defer func() { _ = s.Close() }()
	pub := &recorder{}
	var calls atomic.Int32
	r := runner(t, s, pub, succeed(&calls))

	if err := r.HandleFrame(context.Background(), &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Policy{
		Policy: &edgelinkv3.PolicyUpdate{}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.HandleFrame(context.Background(), cancelFrame("unknown")); err != nil {
		t.Fatal(err)
	}
	if len(pub.states()) != 0 {
		t.Fatalf("events = %v", pub.states())
	}
}

func mustDelivery(t *testing.T, id string) []byte {
	t.Helper()
	b, err := proto.Marshal(delivery(id, takeoff()).GetCommand())
	if err != nil {
		t.Fatal(err)
	}
	return b
}
