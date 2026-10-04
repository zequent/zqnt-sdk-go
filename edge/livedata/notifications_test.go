package livedata

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	eventspb "github.com/zequent/zqnt-utils-golang/v2/gen/events/proto"
	livedatapb "github.com/zequent/zqnt-utils-golang/v2/gen/livedata/proto"

	"google.golang.org/grpc"
)

// fakeNotificationStream records what the SDK sent instead of talking to a backend. The embedded
// grpc.ClientStream satisfies the parts of the streaming interface these tests never touch; it is
// nil, so calling one of them panics rather than passing silently.
type fakeNotificationStream struct {
	grpc.ClientStream
	mu   sync.Mutex
	sent []*eventspb.ProduceNotificationRequest
	// broken makes every Send fail the way a stream does after the server went away.
	broken bool
}

func (f *fakeNotificationStream) Send(req *eventspb.ProduceNotificationRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.broken {
		return io.EOF
	}
	f.sent = append(f.sent, req)
	return nil
}

func (f *fakeNotificationStream) CloseAndRecv() (*livedatapb.LiveDataResponse, error) {
	return &livedatapb.LiveDataResponse{}, nil
}

func (f *fakeNotificationStream) requests() []*eventspb.ProduceNotificationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*eventspb.ProduceNotificationRequest(nil), f.sent...)
}

// fakeStub implements only ProduceNotification; the embedded interface is nil, so any other RPC
// these tests accidentally reach panics instead of silently returning a zero value.
type fakeStub struct {
	livedatapb.LiveDataServiceClient
	mu      sync.Mutex
	stream  *fakeNotificationStream
	opened  int
	openErr error
	// alwaysBroken hands out a broken stream on every open.
	alwaysBroken bool
}

func (f *fakeStub) ProduceNotification(_ context.Context, _ ...grpc.CallOption) (grpc.ClientStreamingClient[eventspb.ProduceNotificationRequest, livedatapb.LiveDataResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened++
	if f.openErr != nil {
		return nil, f.openErr
	}
	if f.alwaysBroken {
		f.stream = &fakeNotificationStream{broken: true}
	}
	if f.stream == nil {
		f.stream = &fakeNotificationStream{}
	}
	return f.stream, nil
}

func (f *fakeStub) opens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opened
}

func newTestService() (*ServiceImpl, *fakeStub) {
	stub := &fakeStub{}
	return NewServiceImpl(stub, slog.New(slog.NewTextHandler(io.Discard, nil))), stub
}

func commandEvent(t *testing.T, s *ServiceImpl, stub *fakeStub, event *domains.CommandExecutionEvent) *eventspb.CommandExecutionEvent {
	t.Helper()
	if err := s.PublishCommandExecutionEvent(context.Background(), event); err != nil {
		t.Fatalf("PublishCommandExecutionEvent: %v", err)
	}
	sent := stub.stream.requests()
	if len(sent) != 1 {
		t.Fatalf("sent %d notifications, want 1", len(sent))
	}
	return sent[0].GetEvent().GetCommandExecution()
}

// TestPublishCommandExecutionEvent_RequiresCorrelationFields guards the fields live-data's
// CommandExecutionEventPublisher rejects an event for. That rejection happens after this stream
// has accepted the message and is invisible to the adapter, so the only place it can be caught is
// before sending -- and an event that vanishes there leaves a skill node running until its
// execution times out.
func TestPublishCommandExecutionEvent_RequiresCorrelationFields(t *testing.T) {
	cases := map[string]*domains.CommandExecutionEvent{
		"no SN":                  {ExternalExecutionID: "capexec:e1:n1", Status: domains.CommandExecutionSucceeded},
		"no ExternalExecutionID": {SN: "SIM-1", Status: domains.CommandExecutionSucceeded},
	}
	for name, event := range cases {
		s, stub := newTestService()
		if err := s.PublishCommandExecutionEvent(context.Background(), event); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if stub.opens() != 0 {
			t.Errorf("%s: opened a stream for an event that cannot be delivered", name)
		}
	}
}

// TestPublishCommandExecutionEvent_DefaultsOccurredAt pins the same silent-drop rule for the third
// required field, which an adapter has no reason to set by hand.
func TestPublishCommandExecutionEvent_DefaultsOccurredAt(t *testing.T) {
	s, stub := newTestService()
	event := commandEvent(t, s, stub, &domains.CommandExecutionEvent{
		SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1", Status: domains.CommandExecutionSucceeded,
	})
	if event.GetOccurredAt() == nil {
		t.Fatal("occurred_at is unset; live-data drops the event and the node waits forever")
	}
	if delta := time.Since(event.GetOccurredAt().AsTime()); delta > time.Minute || delta < -time.Minute {
		t.Errorf("occurred_at = %v, want roughly now", event.GetOccurredAt().AsTime())
	}
}

func TestPublishCommandExecutionEvent_KeepsExplicitOccurredAt(t *testing.T) {
	s, stub := newTestService()
	observed := time.Date(2026, 9, 16, 10, 30, 0, 0, time.UTC)
	event := commandEvent(t, s, stub, &domains.CommandExecutionEvent{
		SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1",
		Status: domains.CommandExecutionSucceeded, OccurredAt: observed,
	})
	if got := event.GetOccurredAt().AsTime(); !got.Equal(observed) {
		t.Errorf("occurred_at = %v, want %v", got, observed)
	}
}

// TestPublishCommandExecutionEvent_MapsTheWholeEvent covers what mission-autonomy reads:
// external_execution_id is the correlation key, asset_sn is re-checked against the execution's own
// asset, progress drives the node's progress, and output becomes the completed node's result.
func TestPublishCommandExecutionEvent_MapsTheWholeEvent(t *testing.T) {
	s, stub := newTestService()
	progress := float32(0.5)
	event := commandEvent(t, s, stub, &domains.CommandExecutionEvent{
		SN:                  "SIM-1",
		ExternalExecutionID: "capexec:exec-1:node-1",
		CommandID:           "navigation.go_to",
		Status:              domains.CommandExecutionRunning,
		Progress:            &progress,
		Message:             "halfway",
		Output:              map[string]any{"legs": float64(3)},
	})
	if event.GetExternalExecutionId() != "capexec:exec-1:node-1" {
		t.Errorf("external_execution_id = %q", event.GetExternalExecutionId())
	}
	if event.GetAssetSn() != "SIM-1" {
		t.Errorf("asset_sn = %q, want SIM-1", event.GetAssetSn())
	}
	if event.GetCommandId() != "navigation.go_to" {
		t.Errorf("command_id = %q", event.GetCommandId())
	}
	if event.GetStatus() != eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_RUNNING {
		t.Errorf("status = %v", event.GetStatus())
	}
	if event.GetProgress() != 0.5 {
		t.Errorf("progress = %v, want 0.5", event.GetProgress())
	}
	if event.GetMessage() != "halfway" {
		t.Errorf("message = %q", event.GetMessage())
	}
	if got := event.GetOutput().GetFields()["legs"].GetNumberValue(); got != 3 {
		t.Errorf("output.legs = %v, want 3", got)
	}
}

// An absent axis of the event stays absent: mission-autonomy leaves a node's progress untouched
// for an event without one (`if (!event.hasProgress())`), which is not the same as reporting 0.
func TestPublishCommandExecutionEvent_OmitsAbsentOptionals(t *testing.T) {
	s, stub := newTestService()
	event := commandEvent(t, s, stub, &domains.CommandExecutionEvent{
		SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1", Status: domains.CommandExecutionAccepted,
	})
	if event.Progress != nil {
		t.Errorf("progress = %v, want absent", *event.Progress)
	}
	if event.CommandId != nil {
		t.Errorf("command_id = %v, want absent", *event.CommandId)
	}
	if event.Message != nil {
		t.Errorf("message = %v, want absent", *event.Message)
	}
	if event.GetOutput() != nil {
		t.Errorf("output = %v, want absent", event.GetOutput())
	}
}

func TestPublishCommandExecutionEvent_SeverityAndStatusMapping(t *testing.T) {
	cases := map[domains.CommandExecutionStatus]struct {
		status   eventspb.CommandExecutionStatus
		severity eventspb.NotificationSeverity
	}{
		domains.CommandExecutionAccepted:  {eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_ACCEPTED, eventspb.NotificationSeverity_NOTIFICATION_SEVERITY_INFO},
		domains.CommandExecutionRunning:   {eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_RUNNING, eventspb.NotificationSeverity_NOTIFICATION_SEVERITY_INFO},
		domains.CommandExecutionSucceeded: {eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_SUCCEEDED, eventspb.NotificationSeverity_NOTIFICATION_SEVERITY_INFO},
		domains.CommandExecutionFailed:    {eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_FAILED, eventspb.NotificationSeverity_NOTIFICATION_SEVERITY_CRITICAL},
		domains.CommandExecutionCancelled: {eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_CANCELLED, eventspb.NotificationSeverity_NOTIFICATION_SEVERITY_INFO},
	}
	for in, want := range cases {
		s, stub := newTestService()
		if err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
			SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1", Status: in,
		}); err != nil {
			t.Fatalf("%v: %v", in, err)
		}
		req := stub.stream.requests()[0]
		if got := req.GetEvent().GetCommandExecution().GetStatus(); got != want.status {
			t.Errorf("%v: status = %v, want %v", in, got, want.status)
		}
		if req.GetSeverity() != want.severity {
			t.Errorf("%v: severity = %v, want %v", in, req.GetSeverity(), want.severity)
		}
		if req.GetEventType() != eventspb.NotificationEventType_NOTIFICATION_EVENT_COMMAND_EXECUTION {
			t.Errorf("%v: event_type = %v", in, req.GetEventType())
		}
		if req.GetBase().GetSn() != "SIM-1" {
			t.Errorf("%v: base.sn = %q, want SIM-1", in, req.GetBase().GetSn())
		}
	}
}

// One stream serves every event: a command reports several stages in quick succession, and
// reopening the stream per event would both cost a round trip and risk reordering them.
func TestPublishCommandExecutionEvent_ReusesOneStream(t *testing.T) {
	s, stub := newTestService()
	for _, status := range []domains.CommandExecutionStatus{
		domains.CommandExecutionAccepted, domains.CommandExecutionRunning, domains.CommandExecutionSucceeded,
	} {
		if err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
			SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1", Status: status,
		}); err != nil {
			t.Fatalf("%v: %v", status, err)
		}
	}
	if stub.opens() != 1 {
		t.Errorf("opened %d streams for 3 events, want 1", stub.opens())
	}
	if got := len(stub.stream.requests()); got != 3 {
		t.Errorf("sent %d events, want 3", got)
	}
}

// After Shutdown the publisher stops rather than reopening a stream into a backend the process is
// disconnecting from -- and reports no error, so shutdown ordering can't fail a caller.
func TestPublishCommandExecutionEvent_SilentAfterShutdown(t *testing.T) {
	s, stub := newTestService()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
		SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1", Status: domains.CommandExecutionSucceeded,
	}); err != nil {
		t.Errorf("PublishCommandExecutionEvent after shutdown: %v", err)
	}
	if stub.opens() != 0 {
		t.Error("opened a notification stream after shutdown")
	}
}

// The interface is what adapters program against, so the implementation has to satisfy it.
var _ LiveDataService = (*ServiceImpl)(nil)

// A live-data restart breaks the long-lived stream; the first Send after it gets io.EOF. That
// message used to be dropped -- in practice a command's SUCCEEDED, leaving its skill node RUNNING
// forever. It now goes out again on a fresh stream.
func TestPublishCommandExecutionEvent_ResendsOnAFreshStreamAfterTheOldOneBroke(t *testing.T) {
	s, stub := newTestService()
	if err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
		SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1", Status: domains.CommandExecutionAccepted,
	}); err != nil {
		t.Fatalf("first event: %v", err)
	}

	// live-data restarts: the open stream is dead, the next open gets a working one.
	stub.mu.Lock()
	stub.stream.mu.Lock()
	stub.stream.broken = true
	stub.stream.mu.Unlock()
	dead := stub.stream
	stub.stream = nil
	stub.mu.Unlock()

	if err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
		SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1", Status: domains.CommandExecutionSucceeded,
	}); err != nil {
		t.Fatalf("event after the restart: %v", err)
	}

	if stub.opens() != 2 {
		t.Errorf("opened %d streams, want 2 (the original and one after the break)", stub.opens())
	}
	if len(dead.requests()) != 1 {
		t.Errorf("dead stream holds %d events, want only the one sent before the break", len(dead.requests()))
	}
	got := stub.stream.requests()
	if len(got) != 1 || !strings.HasSuffix(got[0].GetEvent().GetCommandExecution().GetStatus().String(), "SUCCEEDED") {
		t.Fatalf("fresh stream holds %d events, want the resent SUCCEEDED", len(got))
	}
}

// When the fresh stream fails too, the error reaches the caller instead of being swallowed.
func TestPublishCommandExecutionEvent_ReportsAFailureOnBothStreams(t *testing.T) {
	s, stub := newTestService()
	stub.stream = &fakeNotificationStream{broken: true}
	stub.alwaysBroken = true

	err := s.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
		SN: "SIM-1", ExternalExecutionID: "capexec:e1:n1", Status: domains.CommandExecutionSucceeded,
	})
	if err == nil {
		t.Fatal("want an error when neither stream accepts the event")
	}
	if stub.opens() != 2 {
		t.Errorf("opened %d streams, want 2", stub.opens())
	}
}
