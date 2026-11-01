// Package command runs the commands the platform delivers to an Edge Node. A command runs at most
// once per command execution id: the id is stored durably before the command starts, and a
// delivery of a known id is acknowledged and ignored. A command that arrives after its
// expires_at is refused (REJECTED, command.expired). Its timeout counts from ACCEPTED. Every state
// goes back to the platform as a CommandEvent in the COMMAND_REPLY lane.
//
// A Runner is the session's downlink handler (session.Config.Handler = runner.HandleFrame) and
// publishes through the session (Config.Publisher = the *session.Session); call Recover once at
// start, before the session runs.
package command

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

// Error codes the runner reports itself.
const (
	CodeExpired     = "command.expired"
	CodeTimeout     = "command.timeout"
	CodeFailed      = "command.failed"
	CodeInterrupted = "command.interrupted"
	CodeCancelled   = "command.cancelled"
)

// Progress reports that the command is running, with its progress 0..1 when known (negative =
// unknown) and a message.
type Progress func(fraction float32, message string)

// Handler runs one command on the asset it names. ctx ends when the command's timeout passes or
// the platform cancels it. A nil error succeeds with result; an *Error fails with its code.
type Handler interface {
	Execute(ctx context.Context, cmd *capabilityv3.Command, progress Progress) (*structpb.Struct, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, cmd *capabilityv3.Command, progress Progress) (*structpb.Struct, error)

// Execute calls f.
func (f HandlerFunc) Execute(ctx context.Context, cmd *capabilityv3.Command, progress Progress) (*structpb.Struct, error) {
	return f(ctx, cmd, progress)
}

// Error is a failure with a code the capability declares, e.g. "flight.not_airborne".
type Error struct {
	Code     string
	Message  string
	Category commonv3.ErrorCategory
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Publisher queues an envelope for the platform; the session's Publish.
type Publisher interface {
	Publish(ctx context.Context, env *edgelinkv3.Envelope) (*edgelinkv3.MessageId, error)
}

// Config configures a Runner.
type Config struct {
	Store     *store.Store
	Publisher Publisher
	Handler   Handler
	// Now defaults to time.Now.
	Now func() time.Time
}

// Runner is safe for concurrent use.
type Runner struct {
	cfg Config

	mu      sync.Mutex
	running map[string]context.CancelCauseFunc
	wg      sync.WaitGroup
}

var errCancelled = errors.New("cancelled by the platform")

// New checks cfg.
func New(cfg Config) (*Runner, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("command: store is required")
	case cfg.Publisher == nil:
		return nil, errors.New("command: publisher is required")
	case cfg.Handler == nil:
		return nil, errors.New("command: handler is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Runner{cfg: cfg, running: map[string]context.CancelCauseFunc{}}, nil
}

// Recover reports the commands a previous run of the node claimed and never finished as FAILED
// (command.interrupted). They do not run again: a command runs at most once.
func (r *Runner) Recover(ctx context.Context) error {
	records, err := r.cfg.Store.UnfinishedCommands()
	if err != nil {
		return err
	}
	for _, rec := range records {
		var d edgelinkv3.CommandDelivery
		if err := proto.Unmarshal(rec.Payload, &d); err != nil {
			d = edgelinkv3.CommandDelivery{CommandExecutionId: rec.ID}
		}
		ev := r.event(&d, capabilityv3.CommandState_COMMAND_STATE_FAILED)
		ev.Error = r.failure(CodeInterrupted, "The Edge Node restarted before the command finished",
			commonv3.ErrorCategory_ERROR_CATEGORY_ASSET)
		ev.Message = ev.Error.GetMessage()
		if err := r.publish(ctx, ev); err != nil {
			return err
		}
		if err := r.cfg.Store.FinishCommand(rec.ID); err != nil {
			return err
		}
	}
	return nil
}

// HandleFrame is the session's downlink handler for commands; other frames pass. A nil return
// lets the session acknowledge the frame; an error leaves it for the platform to send again.
func (r *Runner) HandleFrame(ctx context.Context, f *edgelinkv3.DownlinkFrame) error {
	d := f.GetCommand()
	if d == nil {
		return nil
	}
	if d.GetCommandExecutionId() == "" {
		return nil
	}
	switch a := d.GetAction().(type) {
	case *edgelinkv3.CommandDelivery_Execute:
		return r.execute(ctx, d, a.Execute)
	case *edgelinkv3.CommandDelivery_Cancel:
		r.cancel(d.GetCommandExecutionId())
	}
	return nil
}

// Wait waits for the commands running now.
func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) execute(ctx context.Context, d *edgelinkv3.CommandDelivery, cmd *capabilityv3.Command) error {
	payload, err := proto.Marshal(d)
	if err != nil {
		return nil
	}
	claimed, err := r.cfg.Store.ClaimCommand(d.GetCommandExecutionId(), payload)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	if exp := cmd.GetExpiresAt(); exp != nil && !r.cfg.Now().Before(exp.AsTime()) {
		ev := r.event(d, capabilityv3.CommandState_COMMAND_STATE_REJECTED)
		ev.Error = r.failure(CodeExpired, fmt.Sprintf("The command expired at %s", exp.AsTime().Format(time.RFC3339)),
			commonv3.ErrorCategory_ERROR_CATEGORY_TIMEOUT)
		ev.Message = ev.Error.GetMessage()
		return r.finish(ctx, d, ev)
	}
	if err := r.publish(ctx, r.event(d, capabilityv3.CommandState_COMMAND_STATE_ACCEPTED)); err != nil {
		return errors.Join(err, r.cfg.Store.ForgetCommand(d.GetCommandExecutionId()))
	}

	runCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	if t := cmd.GetTimeout(); t != nil && t.AsDuration() > 0 {
		var stop context.CancelFunc
		runCtx, stop = context.WithTimeoutCause(runCtx, t.AsDuration(), context.DeadlineExceeded)
		inner := cancel
		cancel = func(cause error) { inner(cause); stop() }
	}
	r.mu.Lock()
	r.running[d.GetCommandExecutionId()] = cancel
	r.mu.Unlock()
	r.wg.Add(1)
	go r.run(runCtx, cancel, d, cmd)
	return nil
}

func (r *Runner) run(ctx context.Context, cancel context.CancelCauseFunc, d *edgelinkv3.CommandDelivery, cmd *capabilityv3.Command) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		delete(r.running, d.GetCommandExecutionId())
		r.mu.Unlock()
		cancel(nil)
	}()
	progress := func(fraction float32, message string) {
		ev := r.event(d, capabilityv3.CommandState_COMMAND_STATE_RUNNING)
		if fraction >= 0 {
			ev.Progress = proto.Float32(fraction)
		}
		ev.Message = message
		_ = r.publish(context.WithoutCancel(ctx), ev)
	}
	result, err := r.cfg.Handler.Execute(ctx, proto.Clone(cmd).(*capabilityv3.Command), progress)
	_ = r.finish(context.WithoutCancel(ctx), d, r.outcome(ctx, result, err, d))
}

func (r *Runner) outcome(ctx context.Context, result *structpb.Struct, err error, d *edgelinkv3.CommandDelivery) *capabilityv3.CommandEvent {
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, errCancelled):
		ev := r.event(d, capabilityv3.CommandState_COMMAND_STATE_CANCELLED)
		ev.Message = "Cancelled by the platform"
		return ev
	case errors.Is(cause, context.DeadlineExceeded):
		ev := r.event(d, capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT)
		ev.Error = r.failure(CodeTimeout, "The command did not finish within its timeout", commonv3.ErrorCategory_ERROR_CATEGORY_TIMEOUT)
		ev.Message = ev.Error.GetMessage()
		return ev
	case err != nil:
		ev := r.event(d, capabilityv3.CommandState_COMMAND_STATE_FAILED)
		var coded *Error
		if errors.As(err, &coded) {
			category := coded.Category
			if category == commonv3.ErrorCategory_ERROR_CATEGORY_UNSPECIFIED {
				category = commonv3.ErrorCategory_ERROR_CATEGORY_ASSET
			}
			ev.Error = r.failure(coded.Code, coded.Message, category)
		} else {
			ev.Error = r.failure(CodeFailed, err.Error(), commonv3.ErrorCategory_ERROR_CATEGORY_ASSET)
		}
		ev.Message = ev.Error.GetMessage()
		return ev
	}
	ev := r.event(d, capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED)
	ev.Result = result
	return ev
}

func (r *Runner) cancel(id string) {
	r.mu.Lock()
	cancel, ok := r.running[id]
	r.mu.Unlock()
	if ok {
		cancel(errCancelled)
	}
}

func (r *Runner) finish(ctx context.Context, d *edgelinkv3.CommandDelivery, ev *capabilityv3.CommandEvent) error {
	if err := r.publish(ctx, ev); err != nil {
		return err
	}
	return r.cfg.Store.FinishCommand(d.GetCommandExecutionId())
}

func (r *Runner) publish(ctx context.Context, ev *capabilityv3.CommandEvent) error {
	_, err := r.cfg.Publisher.Publish(ctx, &edgelinkv3.Envelope{
		Lane: edgelinkv3.Lane_LANE_COMMAND_REPLY,
		Body: &edgelinkv3.Envelope_CommandEvent{CommandEvent: ev},
	})
	return err
}

func (r *Runner) event(d *edgelinkv3.CommandDelivery, state capabilityv3.CommandState) *capabilityv3.CommandEvent {
	cmd := d.GetExecute()
	return &capabilityv3.CommandEvent{
		CommandExecutionId: d.GetCommandExecutionId(),
		CommandId:          cmd.GetCommandId(),
		Asset:              cmd.GetAsset(),
		State:              state,
		OccurredAt:         timestamppb.New(r.cfg.Now()),
		Execution:          cmd.GetExecution(),
	}
}

func (r *Runner) failure(code, message string, category commonv3.ErrorCategory) *commonv3.Error {
	return &commonv3.Error{Category: category, Code: code, Message: message, OccurredAt: timestamppb.New(r.cfg.Now())}
}
