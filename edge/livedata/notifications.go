package livedata

import (
	"context"
	"fmt"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/internal/protohelpers"
	commonpb "github.com/zequent/zqnt-utils-golang/v2/gen/common/proto"
	eventspb "github.com/zequent/zqnt-utils-golang/v2/gen/events/proto"
	livedatapb "github.com/zequent/zqnt-utils-golang/v2/gen/livedata/proto"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Notifications travel over their own LiveDataService stream, separate from telemetry.
//
// Telemetry keys a stream per device SN because each device produces its own continuous sample
// flow. Notifications are occasional and already carry the asset they concern in the event body,
// so one stream per adapter process is enough -- and keeping it single also keeps event ordering
// intact across devices, which matters when a command's RUNNING and SUCCEEDED land close together.

// PublishCommandExecutionEvent reports one stage of a physical command's lifecycle to the
// platform. mission-autonomy correlates the event to the waiting skill node by
// ExternalExecutionID; see domains.CommandExecutionEvent for where that id comes from.
//
// Delivery is fire-and-forget by design (a client-streaming RPC answers once, at the end), so a
// nil error means "handed to the stream", not "the platform accepted it". What this method *can*
// catch is the event being unusable before it leaves: the three fields live-data requires are
// validated here rather than dropped silently downstream.
func (s *ServiceImpl) PublishCommandExecutionEvent(ctx context.Context, event *domains.CommandExecutionEvent) error {
	if event == nil {
		return nil
	}
	if event.SN == "" {
		return fmt.Errorf("livedata: command execution event requires SN")
	}
	if event.ExternalExecutionID == "" {
		return fmt.Errorf("livedata: command execution event requires ExternalExecutionID")
	}
	req, err := s.toNotificationRequest(event)
	if err != nil {
		return err
	}
	return s.ProduceNotification(ctx, req)
}

// ProduceNotification sends a pre-built notification over the shared stream, opening or reopening
// it as needed. Use this for event kinds the SDK has no typed publisher for yet.
func (s *ServiceImpl) ProduceNotification(ctx context.Context, req *eventspb.ProduceNotificationRequest) error {
	if s.shuttingDown.Load() {
		s.log.Warn("cannot produce notification: service is shutting down")
		return nil
	}

	// Two attempts: the stream is long-lived, so when live-data restarts the first Send after it
	// is the one that finds the connection gone (io.EOF). Dropping that message lost exactly the
	// events that matter -- a command's SUCCEEDED -- and left its skill node RUNNING for good. A
	// second attempt goes out on a freshly opened stream; a message that fails there too is
	// reported to the caller.
	var sendErr error
	for attempt := 0; attempt < 2; attempt++ {
		stream, err := s.getOrCreateNotificationStream(ctx)
		if err != nil || stream == nil {
			return err
		}
		if sendErr = stream.Send(req); sendErr == nil {
			return nil
		}
		s.log.Warn("error sending notification", "attempt", attempt+1, "error", sendErr)
		s.removeNotificationStream()
		go s.handleNotificationStreamFailure(stream)
	}
	s.log.Error("notification not delivered", "error", sendErr)
	return sendErr
}

// CloseNotificationStream closes the notification stream if one is open. Safe to call when none is.
func (s *ServiceImpl) CloseNotificationStream(_ context.Context) error {
	s.notifyMu.Lock()
	entry := s.notifyStream
	s.notifyStream = nil
	s.notifyAttempts = 0
	s.notifyMu.Unlock()

	if entry != nil {
		entry.cancel()
		if _, err := entry.stream.CloseAndRecv(); err != nil {
			s.log.Warn("error closing notification stream", "error", err)
		}
		s.log.Info("closed notification stream")
	}
	return nil
}

// ---- internal helpers -------------------------------------------------------

func (s *ServiceImpl) getOrCreateNotificationStream(ctx context.Context) (livedatapb.LiveDataService_ProduceNotificationClient, error) {
	s.notifyMu.RLock()
	entry := s.notifyStream
	s.notifyMu.RUnlock()
	if entry != nil {
		return entry.stream, nil
	}

	if s.shuttingDown.Load() {
		return nil, nil
	}
	return s.createNotificationStream(ctx)
}

func (s *ServiceImpl) createNotificationStream(_ context.Context) (livedatapb.LiveDataService_ProduceNotificationClient, error) {
	s.log.Info("creating gRPC notification stream")

	// Deliberately not derived from the caller's ctx: the stream outlives the one publish that
	// happened to open it, exactly as the telemetry streams do.
	streamCtx, cancel := context.WithCancel(context.Background())
	stream, err := s.stub.ProduceNotification(streamCtx)
	if err != nil {
		cancel()
		s.log.Error("failed to create notification stream", "error", err)
		s.scheduleNotificationReconnect(1)
		return nil, err
	}

	s.notifyMu.Lock()
	s.notifyStream = &notificationStream{stream: stream, cancel: cancel}
	s.notifyAttempts = 0
	s.notifyMu.Unlock()

	return stream, nil
}

// handleNotificationStreamFailure mirrors handleStreamFailure: it runs only after a Send has
// already failed, because CloseAndRecv is how a client-streaming RPC signals "done sending" and
// calling it early would half-close a stream that is still in use.
func (s *ServiceImpl) handleNotificationStreamFailure(stream livedatapb.LiveDataService_ProduceNotificationClient) {
	_, err := stream.CloseAndRecv()
	if err == nil {
		s.log.Info("notification stream completed normally")
		return
	}

	s.log.Error("notification stream error", "error", err)

	if s.shuttingDown.Load() || !s.shouldReconnect(err) {
		return
	}

	s.notifyMu.Lock()
	s.notifyAttempts++
	attempt := s.notifyAttempts
	s.notifyMu.Unlock()

	if attempt <= maxReconnectAttempts {
		s.scheduleNotificationReconnect(attempt)
	} else {
		s.log.Warn("max reconnect attempts reached for notifications; manual recovery required", "attempts", attempt)
	}
}

func (s *ServiceImpl) removeNotificationStream() {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	s.notifyStream = nil
}

func (s *ServiceImpl) scheduleNotificationReconnect(attempt int) {
	if s.shuttingDown.Load() {
		return
	}
	delay := s.computeDelay(attempt)
	s.log.Info("scheduling notification stream reconnect", "delay", delay, "attempt", attempt)

	time.AfterFunc(delay, func() {
		if s.shuttingDown.Load() {
			return
		}
		s.notifyMu.RLock()
		exists := s.notifyStream != nil
		s.notifyMu.RUnlock()
		if exists {
			return
		}
		if _, err := s.createNotificationStream(context.Background()); err != nil {
			s.log.Error("notification stream reconnect failed", "error", err)
		}
	})
}

// toNotificationRequest wraps a domain event in the ProduceNotificationRequest live-data expects.
//
// occurred_at is filled in here when the caller left it zero. It is not optional on the wire --
// CommandExecutionEventPublisher drops an event without it -- and since the drop happens after
// this stream has already accepted the message, an adapter would have no way to notice.
func (s *ServiceImpl) toNotificationRequest(event *domains.CommandExecutionEvent) (*eventspb.ProduceNotificationRequest, error) {
	occurredAt := protohelpers.Now()
	if !event.OccurredAt.IsZero() {
		occurredAt = timestamppb.New(event.OccurredAt)
	}

	payload := &eventspb.CommandExecutionEvent{
		ExternalExecutionId: event.ExternalExecutionID,
		Status:              toCommandExecutionStatus(event.Status),
		AssetSn:             event.SN,
		OccurredAt:          occurredAt,
	}
	if event.CommandID != "" {
		payload.CommandId = &event.CommandID
	}
	if event.Progress != nil {
		payload.Progress = event.Progress
	}
	if event.Message != "" {
		payload.Message = &event.Message
	}
	if len(event.Output) > 0 {
		output, err := structpb.NewStruct(event.Output)
		if err != nil {
			return nil, fmt.Errorf("livedata: command execution event output is not JSON-shaped: %w", err)
		}
		payload.Output = output
	}

	return &eventspb.ProduceNotificationRequest{
		Base: &commonpb.RequestBase{
			Tid:       protohelpers.GenerateTID(),
			Sn:        event.SN,
			Timestamp: protohelpers.Now(),
		},
		Event:     &eventspb.NotificationEvent{Event: &eventspb.NotificationEvent_CommandExecution{CommandExecution: payload}},
		Severity:  toNotificationSeverity(event.Status),
		EventType: eventspb.NotificationEventType_NOTIFICATION_EVENT_COMMAND_EXECUTION,
	}, nil
}

func toCommandExecutionStatus(status domains.CommandExecutionStatus) eventspb.CommandExecutionStatus {
	switch status {
	case domains.CommandExecutionAccepted:
		return eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_ACCEPTED
	case domains.CommandExecutionRunning:
		return eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_RUNNING
	case domains.CommandExecutionSucceeded:
		return eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_SUCCEEDED
	case domains.CommandExecutionFailed:
		return eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_FAILED
	case domains.CommandExecutionCancelled:
		return eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_CANCELLED
	default:
		return eventspb.CommandExecutionStatus_COMMAND_EXECUTION_STATUS_UNSPECIFIED
	}
}

// A failed command is the one stage an operator needs to see without going looking for it.
func toNotificationSeverity(status domains.CommandExecutionStatus) eventspb.NotificationSeverity {
	if status == domains.CommandExecutionFailed {
		return eventspb.NotificationSeverity_NOTIFICATION_SEVERITY_CRITICAL
	}
	return eventspb.NotificationSeverity_NOTIFICATION_SEVERITY_INFO
}
