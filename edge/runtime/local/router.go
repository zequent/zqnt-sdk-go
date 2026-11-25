package local

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	edgev3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edge/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/command"
)

// Error codes the router reports for commands it could not hand to an adapter.
const (
	CodeNoAdapter          = "edge.no_adapter"
	CodeAdapterUnreachable = "edge.adapter_unreachable"
)

// finishedMemory is how many finished command execution ids the router remembers, so a late event
// from the adapter for a command the runner already reported is dropped.
const finishedMemory = 256

type adapterConn struct {
	endpoint string
	cc       *grpc.ClientConn
	client   edgev3.EdgeAdapterServiceClient
}

type waiter struct {
	events chan *capabilityv3.CommandEvent
	final  chan *capabilityv3.CommandEvent
}

// router knows which adapter serves which asset and runs commands on it.
type router struct {
	dial         []grpc.DialOption
	cancelPeriod time.Duration

	mu       sync.Mutex
	conns    map[string]*adapterConn
	assets   map[string]*adapterConn
	waiting  map[string]*waiter
	finished map[string]*list.Element
	order    *list.List
	closed   bool
}

func newRouter(creds grpc.DialOption, cancelPeriod time.Duration) *router {
	dial := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if creds != nil {
		dial = append(dial, creds)
	}
	return &router{
		dial:         dial,
		cancelPeriod: cancelPeriod,
		conns:        map[string]*adapterConn{},
		assets:       map[string]*adapterConn{},
		waiting:      map[string]*waiter{},
		finished:     map[string]*list.Element{},
		order:        list.New(),
	}
}

// adapterTarget checks that an adapter endpoint is a Unix socket and returns its gRPC target:
// the runtime dials adapters on the node only.
func adapterTarget(endpoint string) (string, error) {
	path, ok := strings.CutPrefix(endpoint, "unix://")
	if !ok {
		path, ok = strings.CutPrefix(endpoint, "unix:")
	}
	if !ok || !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("adapter endpoint %q is not an absolute unix socket (unix:///path)", endpoint)
	}
	return "unix://" + path, nil
}

// register binds asset to the adapter at endpoint; the latest registration wins.
func (r *router) register(asset, endpoint string) error {
	target, err := adapterTarget(endpoint)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("local: server stopped")
	}
	c := r.conns[target]
	if c == nil {
		cc, err := grpc.NewClient(target, r.dial...)
		if err != nil {
			return fmt.Errorf("adapter endpoint %q: %w", endpoint, err)
		}
		c = &adapterConn{endpoint: target, cc: cc, client: edgev3.NewEdgeAdapterServiceClient(cc)}
		r.conns[target] = c
	}
	r.assets[asset] = c
	return nil
}

// Assets maps every registered asset to its adapter's endpoint.
func (r *router) Assets() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.assets))
	for sn, c := range r.assets {
		out[sn] = c.endpoint
	}
	return out
}

func (r *router) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	var errs []error
	for _, c := range r.conns {
		errs = append(errs, c.cc.Close())
	}
	clear(r.conns)
	clear(r.assets)
	return errors.Join(errs...)
}

// Execute runs cmd on the adapter that registered its asset. A command the adapter ACCEPTED ends
// with the terminal event the adapter publishes for it; its RUNNING events report progress. When
// ctx ends first (timeout or cancel by the platform), the adapter is asked to cancel.
func (r *router) Execute(ctx context.Context, cmd *capabilityv3.Command, progress command.Progress) (*structpb.Struct, error) {
	d := command.DeliveryFrom(ctx)
	id := d.GetCommandExecutionId()
	if id == "" {
		return nil, &command.Error{Code: command.CodeFailed, Message: "The command has no execution id", Rejected: true,
			Category: commonv3.ErrorCategory_ERROR_CATEGORY_INVALID_ARGUMENT}
	}
	sn := cmd.GetAsset().GetSn()
	r.mu.Lock()
	c := r.assets[sn]
	w := &waiter{events: make(chan *capabilityv3.CommandEvent, 16), final: make(chan *capabilityv3.CommandEvent, 1)}
	if c != nil {
		r.waiting[id] = w
	}
	r.mu.Unlock()
	if c == nil {
		return nil, &command.Error{Code: CodeNoAdapter, Message: fmt.Sprintf("No adapter on this Edge Node serves asset %q", sn),
			Rejected: true, Category: commonv3.ErrorCategory_ERROR_CATEGORY_NOT_FOUND}
	}
	defer r.finish(id)

	resp, err := c.client.ExecuteCommand(ctx, &edgev3.ExecuteCommandRequest{
		Context:            d.GetContext(),
		Command:            cmd,
		CommandExecutionId: id,
	})
	if err != nil {
		if ctx.Err() != nil {
			r.cancel(c, id, context.Cause(ctx))
			return nil, ctx.Err()
		}
		return nil, &command.Error{Code: CodeAdapterUnreachable, Message: fmt.Sprintf("The adapter for %q did not take the command: %v", sn, err),
			Category: commonv3.ErrorCategory_ERROR_CATEGORY_ASSET}
	}
	if res := resp.GetResult(); terminal(res.GetState()) {
		return outcome(res.GetState(), res.GetResult(), res.GetError())
	}
	for {
		select {
		case ev := <-w.events:
			fraction := float32(-1)
			if ev.Progress != nil {
				fraction = ev.GetProgress()
			}
			progress(fraction, ev.GetMessage())
		case ev := <-w.final:
			return outcome(ev.GetState(), ev.GetResult(), ev.GetError())
		case <-ctx.Done():
			r.cancel(c, id, context.Cause(ctx))
			return nil, ctx.Err()
		}
	}
}

func (r *router) cancel(c *adapterConn, id string, cause error) {
	ctx, stop := context.WithTimeout(context.Background(), r.cancelPeriod)
	defer stop()
	reason := "cancelled by the platform"
	if errors.Is(cause, context.DeadlineExceeded) {
		reason = "timed out"
	}
	_, _ = c.client.CancelCommand(ctx, &edgev3.CancelCommandRequest{CommandExecutionId: id, Reason: reason})
}

// deliver hands an adapter's command event to the Execute waiting for it. It reports false for
// an event no Execute waits for and the router did not finish, which the caller publishes itself.
func (r *router) deliver(ev *capabilityv3.CommandEvent) bool {
	id := ev.GetCommandExecutionId()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, done := r.finished[id]; done {
		return true
	}
	w := r.waiting[id]
	if w == nil {
		return false
	}
	ev = proto.Clone(ev).(*capabilityv3.CommandEvent)
	if terminal(ev.GetState()) {
		select {
		case w.final <- ev:
		default:
		}
		return true
	}
	select {
	case w.events <- ev:
	default:
	}
	return true
}

func (r *router) finish(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.waiting, id)
	if _, ok := r.finished[id]; ok {
		return
	}
	r.finished[id] = r.order.PushBack(id)
	for r.order.Len() > finishedMemory {
		oldest := r.order.Front()
		r.order.Remove(oldest)
		delete(r.finished, oldest.Value.(string))
	}
}

func terminal(s capabilityv3.CommandState) bool {
	switch s {
	case capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED,
		capabilityv3.CommandState_COMMAND_STATE_FAILED,
		capabilityv3.CommandState_COMMAND_STATE_REJECTED,
		capabilityv3.CommandState_COMMAND_STATE_CANCELLED,
		capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT:
		return true
	}
	return false
}

// outcome turns an adapter's terminal state into what a command.Handler returns.
func outcome(state capabilityv3.CommandState, result *structpb.Struct, e *commonv3.Error) (*structpb.Struct, error) {
	if state == capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED {
		return result, nil
	}
	ce := &command.Error{Code: e.GetCode(), Message: e.GetMessage(), Category: e.GetCategory(),
		Rejected: state == capabilityv3.CommandState_COMMAND_STATE_REJECTED}
	if ce.Code == "" {
		switch state {
		case capabilityv3.CommandState_COMMAND_STATE_CANCELLED:
			ce.Code = command.CodeCancelled
		case capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT:
			ce.Code = command.CodeTimeout
		default:
			ce.Code = command.CodeFailed
		}
	}
	if ce.Message == "" {
		ce.Message = "The adapter reported " + strings.TrimPrefix(state.String(), "COMMAND_STATE_")
	}
	return nil, ce
}
