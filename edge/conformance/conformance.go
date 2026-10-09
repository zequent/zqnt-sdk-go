// Package conformance is a test kit an adapter runs against itself (with fake hardware or a
// simulator, never a real device -- it executes commands):
//
//	func TestConformance(t *testing.T) {
//	    events := &conformance.Recorder{}
//	    conformance.Run(t, newAdapter(fakeDrone, events), conformance.Options{Events: events})
//	}
//
// It checks that every advertised command is executable, every executable command is advertised,
// every schema parses, and that an ACCEPTED command is completed by an event under its
// command_execution_id that carries occurred_at.
package conformance

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	adaptergrpc "github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/grpc"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/schema"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/livedata"
	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"

	"google.golang.org/protobuf/types/known/structpb"
)

// Options tunes a conformance run.
type Options struct {
	// SN is the asset the commands are sent to. Default "CONFORMANCE-1".
	SN string
	// Params are the params per command id. A command without an entry gets params generated from
	// its input schema (schema.Example).
	Params map[string]map[string]any
	// ProbeIDs are extra command ids to try in "executable ids are advertised", next to the
	// built-in catalog ids and the adapter's registered ones.
	ProbeIDs []string
	// Events receives the adapter's command events. Required when a command answers ACCEPTED.
	Events *Recorder
	// CompletionTimeout is how long an ACCEPTED command may take to report completion. Default 5s.
	CompletionTimeout time.Duration
}

// Recorder is a CommandEventPublisher that keeps what the adapter publishes.
type Recorder struct {
	mu     sync.Mutex
	events []*domains.CommandExecutionEvent
}

// PublishCommandExecutionEvent records the event.
func (r *Recorder) PublishCommandExecutionEvent(_ context.Context, e *domains.CommandExecutionEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := *e
	r.events = append(r.events, &copied)
	return nil
}

// Events returns what was published so far.
func (r *Recorder) Events() []*domains.CommandExecutionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

var _ adapter.CommandEventPublisher = (*Recorder)(nil)

// Report lists the problems Check found, per rule. An empty Report is a conforming adapter.
type Report struct {
	Schemas    []string
	Advertised []string
	Executable []string
	Completion []string
}

// Run checks the adapter and reports every problem as a failure of the matching subtest.
func Run(t *testing.T, a adapter.EdgeAdapter, opts Options) {
	t.Helper()
	r := Check(a, opts)
	for name, problems := range map[string][]string{
		"schemas parse":                       r.Schemas,
		"advertised ids are executable":       r.Advertised,
		"executable ids are advertised":       r.Executable,
		"completion events carry occurred_at": r.Completion,
	} {
		t.Run(name, func(t *testing.T) {
			for _, p := range problems {
				t.Error(p)
			}
		})
	}
}

// Check runs the rules against the adapter and returns what does not conform.
func Check(a adapter.EdgeAdapter, opts Options) Report {
	if opts.SN == "" {
		opts.SN = "CONFORMANCE-1"
	}
	if opts.CompletionTimeout == 0 {
		opts.CompletionTimeout = 5 * time.Second
	}
	var r Report
	ctx := context.Background()
	caps, err := a.GetCapabilities(ctx, opts.SN)
	if err != nil || caps == nil {
		r.Advertised = append(r.Advertised, fmt.Sprintf("GetCapabilities: %v", err))
		return r
	}
	server := adaptergrpc.NewServerV3(a, slog.New(slog.NewTextHandler(io.Discard, nil)))
	advertised := map[string]bool{}
	for _, c := range caps.Capabilities {
		advertised[c.Command] = true
		r.Schemas = append(r.Schemas, schemaProblems(c.Command, "input schema", c.InputSchema)...)
		r.Schemas = append(r.Schemas, schemaProblems(c.Command, "output schema", c.OutputSchema)...)
		for _, e := range c.Events {
			r.Schemas = append(r.Schemas, schemaProblems(c.Command, "event "+e.Name+" payload schema", e.PayloadSchema)...)
		}
	}

	accepted := map[string][]string{}
	for _, c := range caps.Capabilities {
		if !c.Available {
			continue
		}
		params, ok := opts.Params[c.Command]
		if !ok {
			params = schema.Example(c.InputSchema)
		}
		res, err := execute(server, opts.SN, c.Command, params)
		switch {
		case err != nil:
			r.Advertised = append(r.Advertised, err.Error())
		case isNotSupported(res):
			r.Advertised = append(r.Advertised, fmt.Sprintf("%s is advertised but not executable: %s", c.Command, res.GetError().GetMessage()))
		case res.GetError().GetCode() == schema.InvalidParamsCode:
			r.Advertised = append(r.Advertised, fmt.Sprintf("%s refused its params (set Options.Params for it): %s", c.Command, res.GetError().GetMessage()))
		case res.GetState() == capabilityv3.CommandState_COMMAND_STATE_ACCEPTED:
			ids := []string{res.GetCommandExecutionId()}
			if own := res.GetResult().GetFields()["external_execution_id"].GetStringValue(); own != "" {
				ids = append(ids, own)
			}
			accepted[c.Command] = ids
		}
	}

	probe := append(adaptergrpc.BuiltInCommandIDs(), opts.ProbeIDs...)
	if ra, ok := a.(adapter.RegistryAdapter); ok {
		for _, c := range ra.CommandRegistry().Capabilities(opts.SN).Capabilities {
			probe = append(probe, c.Command)
		}
	}
	for _, id := range uniq(probe) {
		if advertised[id] {
			continue
		}
		res, err := execute(server, opts.SN, id, opts.Params[id])
		if err != nil {
			r.Executable = append(r.Executable, err.Error())
		} else if !isNotSupported(res) {
			r.Executable = append(r.Executable, fmt.Sprintf("%s is executable (answered %s) but not advertised", id, res.GetState()))
		}
	}

	if len(accepted) > 0 && opts.Events == nil {
		r.Completion = append(r.Completion, fmt.Sprintf("commands %v answer ACCEPTED: pass Options.Events and publish their completion to it", sortedKeys(accepted)))
		return r
	}
	for _, command := range sortedKeys(accepted) {
		event := awaitCompletion(opts.Events, accepted[command], opts.CompletionTimeout)
		if event == nil {
			r.Completion = append(r.Completion, fmt.Sprintf("%s was ACCEPTED as %v but no SUCCEEDED/FAILED/CANCELLED event under that id followed", command, accepted[command]))
			continue
		}
		v3, err := livedata.CommandEventV3(event)
		switch {
		case err != nil:
			r.Completion = append(r.Completion, fmt.Sprintf("%s completion event: %v", command, err))
		case v3.GetOccurredAt() == nil || v3.GetOccurredAt().AsTime().After(time.Now().Add(time.Minute)):
			r.Completion = append(r.Completion, fmt.Sprintf("%s completion event has no plausible occurred_at: %v", command, v3.GetOccurredAt()))
		case v3.GetAsset().GetSn() == "":
			r.Completion = append(r.Completion, fmt.Sprintf("%s completion event names no asset", command))
		}
	}
	return r
}

func schemaProblems(command, what string, doc map[string]any) []string {
	if len(doc) == 0 {
		return nil
	}
	var out []string
	if _, err := schema.Compile(doc); err != nil {
		out = append(out, fmt.Sprintf("%s: %s does not parse: %v", command, what, err))
	}
	if _, err := structpb.NewStruct(doc); err != nil {
		out = append(out, fmt.Sprintf("%s: %s cannot be sent: %v", command, what, err))
	}
	return out
}

func execute(server *adaptergrpc.ServerV3, sn, id string, params map[string]any) (*capabilityv3.CommandResult, error) {
	p, err := structpb.NewStruct(params)
	if err != nil {
		return nil, fmt.Errorf("%s: params are not JSON: %w", id, err)
	}
	resp, err := server.ExecuteCommand(context.Background(), &edgev3.ExecuteCommandRequest{
		CommandExecutionId: "conformance:" + id,
		Command:            &capabilityv3.Command{Asset: &commonv3.AssetRef{Sn: sn}, CommandId: id, Params: p},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: ExecuteCommand failed with a status instead of a result: %w", id, err)
	}
	return resp.GetResult(), nil
}

func isNotSupported(r *capabilityv3.CommandResult) bool {
	return r.GetState() == capabilityv3.CommandState_COMMAND_STATE_REJECTED && r.GetError().GetCode() == adaptergrpc.NotSupportedCode
}

func awaitCompletion(r *Recorder, ids []string, timeout time.Duration) *domains.CommandExecutionEvent {
	deadline := time.Now().Add(timeout)
	for {
		for _, e := range r.Events() {
			if !slices.Contains(ids, e.ExecutionID()) {
				continue
			}
			switch e.Status {
			case domains.CommandExecutionSucceeded, domains.CommandExecutionFailed, domains.CommandExecutionCancelled:
				return e
			}
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func uniq(ids []string) []string {
	slices.Sort(ids)
	return slices.Compact(ids)
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
