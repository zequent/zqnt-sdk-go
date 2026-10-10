// Package control commands assets on a Zequent 3.x platform: list what an asset can do, run any of
// it by command id with parameters, cancel a run and watch its events. It is the client of
// zqnt.control.v3.RemoteControlService, served by remote-control next to the 2.x service.
//
// Every action is a capability with a dotted command id (flight.takeoff, navigation.go_to,
// dock.open_cover, vendor.<vendor>.<name>); ListCapabilities returns the ids an asset offers with
// the JSON Schema of their parameters and result.
//
//	cfg, err := config.FromEnv()
//	conn, err := cfg.Dial(cfg.RemoteControl)
//	assets := control.New(conn)
//	result, err := assets.ExecuteCommand(ctx, "DRONE-1", "navigation.go_to",
//		control.Params{"latitude": 52.52, "longitude": 13.405, "altitude": 60})
package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	controlv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/control/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
)

// Params are a command's parameters, as described by its capability's input schema. Values are
// what encoding/json would produce: nil, bool, numbers, string, []any and map[string]any.
type Params = map[string]any

// Client wraps the generated RemoteControlServiceClient.
type Client struct {
	grpc controlv3.RemoteControlServiceClient
}

// New wraps a connection to remote-control, e.g. cfg.Dial(cfg.RemoteControl).
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{grpc: controlv3.NewRemoteControlServiceClient(conn)}
}

// Option adjusts one call. Options a call has no use for are ignored.
type Option func(*call)

type call struct {
	requestID         string
	idempotencyKey    string
	target            *capabilityv3.Target
	timeout           time.Duration
	reason            string
	noFlyZoneOverride bool
}

// WithTarget addresses a part of the asset (a sub-asset, payload or component) instead of the
// asset itself.
func WithTarget(kind capabilityv3.TargetType, ref string) Option {
	return func(c *call) { c.target = &capabilityv3.Target{Type: kind, Ref: ref} }
}

// WithTimeout replaces the capability's own default timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *call) { c.timeout = d }
}

// WithReason is the human reason the platform records; CRITICAL commands require one.
func WithReason(reason string) Option {
	return func(c *call) { c.reason = reason }
}

// WithNoFlyZoneOverride flies through no-fly zones instead of around them. The platform honours
// it for an organization admin or a system admin only and refuses the command for anybody else.
func WithNoFlyZoneOverride() Option {
	return func(c *call) { c.noFlyZoneOverride = true }
}

// WithIdempotencyKey makes a retried call with the same key return the first call's result.
func WithIdempotencyKey(key string) Option {
	return func(c *call) { c.idempotencyKey = key }
}

// WithRequestID correlates the call with the platform's logs and events; one is generated
// otherwise.
func WithRequestID(id string) Option {
	return func(c *call) { c.requestID = id }
}

func options(opts []Option) call {
	var c call
	for _, opt := range opts {
		opt(&c)
	}
	if c.requestID == "" {
		c.requestID = newRequestID()
	}
	return c
}

func (c call) context() *commonv3.RequestContext {
	return &commonv3.RequestContext{RequestId: c.requestID, IdempotencyKey: c.idempotencyKey}
}

// ListCapabilities returns everything the asset can do right now. When its adapter cannot be
// reached the platform answers with the last snapshot it kept, marked SNAPSHOT_STATE_STALE.
func (c *Client) ListCapabilities(ctx context.Context, sn string, opts ...Option) (*capabilityv3.CapabilitySet, error) {
	o := options(opts)
	resp, err := c.grpc.GetCapabilities(ctx, &controlv3.GetCapabilitiesRequest{
		Context: o.context(),
		Asset:   &commonv3.AssetRef{Sn: sn},
		Target:  o.target,
	})
	if err != nil {
		return nil, fmt.Errorf("control: ListCapabilities(%s): %w", sn, err)
	}
	return resp.GetCapabilities(), nil
}

// ExecuteCommand runs commandID on the asset. A command that finishes with the reply comes back
// SUCCEEDED with its result; one that takes time (takeoff, go-to) comes back ACCEPTED or RUNNING
// and its outcome follows as events (WatchCommand, or ExecuteAndWait). A command the platform
// refused or that failed is a *CommandError; a call that never reached the platform keeps its
// gRPC status (status.Code(err)).
func (c *Client) ExecuteCommand(ctx context.Context, sn, commandID string, params Params, opts ...Option) (*capabilityv3.CommandResult, error) {
	req, err := executeRequest(sn, commandID, params, options(opts))
	if err != nil {
		return nil, err
	}
	resp, err := c.grpc.ExecuteCommand(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("control: ExecuteCommand(%s, %s): %w", sn, commandID, err)
	}
	return outcome(resp.GetResult())
}

// ExecuteAndWait is ExecuteCommand followed by waiting for the command's outcome, for a command
// that does not finish with the reply. It watches the asset before sending the command, so an
// outcome that arrives quickly is not missed. ctx bounds the wait.
func (c *Client) ExecuteAndWait(ctx context.Context, sn, commandID string, params Params, opts ...Option) (*capabilityv3.CommandResult, error) {
	req, err := executeRequest(sn, commandID, params, options(opts))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := c.WatchAsset(ctx, sn)
	if err != nil {
		return nil, err
	}
	resp, err := c.grpc.ExecuteCommand(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("control: ExecuteCommand(%s, %s): %w", sn, commandID, err)
	}
	result, err := outcome(resp.GetResult())
	if err != nil || Terminal(result.GetState()) {
		return result, err
	}
	for {
		event, err := events.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("control: waiting for %s (%s): %w", commandID, result.GetCommandExecutionId(), err)
		}
		if event.GetCommandExecutionId() == result.GetCommandExecutionId() && Terminal(event.GetState()) {
			return outcome(&capabilityv3.CommandResult{
				CommandExecutionId: event.GetCommandExecutionId(),
				CommandId:          firstNonEmpty(event.GetCommandId(), commandID),
				State:              event.GetState(),
				Result:             event.GetResult(),
				Error:              event.GetError(),
			})
		}
	}
}

// CancelCommand stops a command run by the command_execution_id ExecuteCommand returned.
func (c *Client) CancelCommand(ctx context.Context, commandExecutionID, reason string, opts ...Option) (*capabilityv3.CommandResult, error) {
	o := options(opts)
	resp, err := c.grpc.CancelCommand(ctx, &controlv3.CancelCommandRequest{
		Context:            o.context(),
		CommandExecutionId: commandExecutionID,
		Reason:             reason,
	})
	if err != nil {
		return nil, fmt.Errorf("control: CancelCommand(%s): %w", commandExecutionID, err)
	}
	result := resp.GetResult()
	switch result.GetState() {
	case capabilityv3.CommandState_COMMAND_STATE_REJECTED, capabilityv3.CommandState_COMMAND_STATE_FAILED:
		return nil, &CommandError{Result: result}
	}
	return result, nil
}

// WatchCommand streams the events of one command run from now on; the stream ends (io.EOF) after
// its final event.
func (c *Client) WatchCommand(ctx context.Context, commandExecutionID string) (*Events, error) {
	return c.watch(ctx, &controlv3.WatchCommandEventsRequest{CommandExecutionId: commandExecutionID}, commandExecutionID)
}

// WatchAsset streams the events of every command on the asset from now on, until ctx ends.
func (c *Client) WatchAsset(ctx context.Context, sn string) (*Events, error) {
	return c.watch(ctx, &controlv3.WatchCommandEventsRequest{Asset: &commonv3.AssetRef{Sn: sn}}, sn)
}

func (c *Client) watch(ctx context.Context, req *controlv3.WatchCommandEventsRequest, subject string) (*Events, error) {
	stream, err := c.grpc.WatchCommandEvents(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("control: WatchCommandEvents(%s): %w", subject, err)
	}
	return &Events{stream: stream}, nil
}

// Events is a stream of command events.
type Events struct {
	stream grpc.ServerStreamingClient[controlv3.WatchCommandEventsResponse]
}

// Recv returns the next event, io.EOF when the stream has ended, or the gRPC error that ended it.
func (e *Events) Recv() (*capabilityv3.CommandEvent, error) {
	resp, err := e.stream.Recv()
	if err != nil {
		return nil, err
	}
	return resp.GetEvent(), nil
}

// Terminal reports whether state is a command's final state.
func Terminal(state capabilityv3.CommandState) bool {
	switch state {
	case capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED,
		capabilityv3.CommandState_COMMAND_STATE_FAILED,
		capabilityv3.CommandState_COMMAND_STATE_REJECTED,
		capabilityv3.CommandState_COMMAND_STATE_CANCELLED,
		capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT:
		return true
	}
	return false
}

// CommandError is a command the platform or the asset did not carry out: REJECTED before it
// started (unknown id, parameters, precondition, permission), or FAILED, CANCELLED or TIMED_OUT.
type CommandError struct {
	Result *capabilityv3.CommandResult
}

func (e *CommandError) Error() string {
	r := e.Result
	msg := fmt.Sprintf("control: %s %s", firstNonEmpty(r.GetCommandId(), "command"), stateName(r.GetState()))
	if r.GetCommandExecutionId() != "" {
		msg += " (" + r.GetCommandExecutionId() + ")"
	}
	if code := r.GetError().GetCode(); code != "" {
		msg += ": " + code
	}
	if text := r.GetError().GetMessage(); text != "" {
		msg += ": " + text
	}
	return msg
}

// State is the command's final state.
func (e *CommandError) State() capabilityv3.CommandState {
	return e.Result.GetState()
}

// Category says whose problem it is: the request, a permission, the asset's state, the asset, the
// platform.
func (e *CommandError) Category() commonv3.ErrorCategory {
	return e.Result.GetError().GetCategory()
}

// Retryable reports whether the same command may succeed if sent again.
func (e *CommandError) Retryable() bool {
	return e.Result.GetError().GetRetryable()
}

func executeRequest(sn, commandID string, params Params, o call) (*controlv3.ExecuteCommandRequest, error) {
	command := &capabilityv3.Command{
		Asset:     &commonv3.AssetRef{Sn: sn},
		CommandId: commandID,
		Target:    o.target,
	}
	if params != nil {
		s, err := structpb.NewStruct(params)
		if err != nil {
			return nil, fmt.Errorf("control: ExecuteCommand(%s, %s): params: %w", sn, commandID, err)
		}
		command.Params = s
	}
	if o.timeout > 0 {
		command.Timeout = durationpb.New(o.timeout)
	}
	return &controlv3.ExecuteCommandRequest{
		Context:           o.context(),
		Command:           command,
		Reason:            o.reason,
		NoFlyZoneOverride: o.noFlyZoneOverride,
	}, nil
}

func outcome(result *capabilityv3.CommandResult) (*capabilityv3.CommandResult, error) {
	switch result.GetState() {
	case capabilityv3.CommandState_COMMAND_STATE_REJECTED,
		capabilityv3.CommandState_COMMAND_STATE_FAILED,
		capabilityv3.CommandState_COMMAND_STATE_CANCELLED,
		capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT:
		return nil, &CommandError{Result: result}
	}
	return result, nil
}

func stateName(state capabilityv3.CommandState) string {
	switch state {
	case capabilityv3.CommandState_COMMAND_STATE_REJECTED:
		return "rejected"
	case capabilityv3.CommandState_COMMAND_STATE_FAILED:
		return "failed"
	case capabilityv3.CommandState_COMMAND_STATE_CANCELLED:
		return "cancelled"
	case capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT:
		return "timed out"
	}
	return state.String()
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "client-go-sdk-" + hex.EncodeToString(b)
}
