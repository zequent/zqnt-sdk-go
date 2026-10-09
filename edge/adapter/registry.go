package adapter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/schema"
)

// CommandHandler runs one registered command. Params arrive validated against the command's input
// schema, with whole numbers under "integer" properties as int64 and every other number float64.
type CommandHandler func(ctx context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error)

// CommandOption describes a registered command beyond its id and schemas.
type CommandOption func(*domains.Capability)

// WithDescription sets the text the catalog shows for the command.
func WithDescription(d string) CommandOption {
	return func(c *domains.Capability) { c.Description = d }
}

// WithCompletion declares whether the reply finishes the command or a later event does.
func WithCompletion(mode domains.CompletionMode, completionEvent string) CommandOption {
	return func(c *domains.Capability) { c.Completion, c.CompletionEvent = mode, completionEvent }
}

// WithTarget names the part of the asset that runs the command. Commands default to the asset.
func WithTarget(t domains.CapabilityTargetType, ref string) CommandOption {
	return func(c *domains.Capability) {
		c.TargetType = t
		if ref != "" {
			c.TargetRef = &ref
		}
	}
}

// Unavailable advertises the command as currently not usable, with the reason.
func Unavailable(reason string) CommandOption {
	return func(c *domains.Capability) { c.Available, c.UnavailableReason = false, &reason }
}

// WithCapability sets any other field of the advertised capability.
func WithCapability(f func(*domains.Capability)) CommandOption {
	return func(c *domains.Capability) { f(c) }
}

type registeredCommand struct {
	capability domains.Capability
	input      *schema.Schema
	handler    CommandHandler
}

// Registry is the single list of commands an adapter can run: what it advertises
// (Capabilities) and what it executes (Execute) come from the same registrations, so the two
// cannot drift apart.
type Registry struct {
	mu              sync.RWMutex
	assetType       string
	commands        map[string]*registeredCommand
	order           []string
	telemetryFields []domains.TelemetryField
	listeners       []func()
}

// SetAssetType sets the asset type reported with the capabilities, e.g. "ASSET_TYPE_DRONE".
func (r *Registry) SetAssetType(t string) {
	r.mu.Lock()
	r.assetType = t
	r.mu.Unlock()
}

// RegisterCommand declares one command: its dotted id, the JSON Schemas of its params and result,
// and the handler that runs it. Registering an id again replaces it. Capability listeners are
// notified, so a running SDK reports the new set to the platform.
func (r *Registry) RegisterCommand(id string, inputSchema, outputSchema map[string]any, handler CommandHandler, opts ...CommandOption) error {
	if id == "" {
		return errors.New("register command: id is required")
	}
	if handler == nil {
		return fmt.Errorf("register command %s: handler is required", id)
	}
	input, err := schema.Compile(inputSchema)
	if err != nil {
		return fmt.Errorf("register command %s: input schema: %w", id, err)
	}
	if _, err := schema.Compile(outputSchema); err != nil {
		return fmt.Errorf("register command %s: output schema: %w", id, err)
	}
	c := domains.Capability{
		Command:      id,
		Available:    true,
		TargetType:   domains.CapabilityTargetAsset,
		Source:       domains.CapabilitySourceEdgeAdapter,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
	}
	for _, o := range opts {
		o(&c)
	}
	r.mu.Lock()
	if r.commands == nil {
		r.commands = map[string]*registeredCommand{}
	}
	if _, exists := r.commands[id]; !exists {
		r.order = append(r.order, id)
	}
	r.commands[id] = &registeredCommand{capability: c, input: input, handler: handler}
	r.mu.Unlock()
	r.changed()
	return nil
}

// MustRegisterCommand is RegisterCommand for registrations fixed at compile time; it panics on a
// broken schema.
func (r *Registry) MustRegisterCommand(id string, inputSchema, outputSchema map[string]any, handler CommandHandler, opts ...CommandOption) {
	if err := r.RegisterCommand(id, inputSchema, outputSchema, handler, opts...); err != nil {
		panic(err)
	}
}

// UnregisterCommand removes a command, e.g. when a payload is detached.
func (r *Registry) UnregisterCommand(id string) {
	r.mu.Lock()
	_, ok := r.commands[id]
	if ok {
		delete(r.commands, id)
		for i, o := range r.order {
			if o == id {
				r.order = append(r.order[:i], r.order[i+1:]...)
				break
			}
		}
	}
	r.mu.Unlock()
	if ok {
		r.changed()
	}
}

// DeclareTelemetryField declares a key the asset sends in TelemetrySample.Details. Declaring a
// key again replaces it.
func (r *Registry) DeclareTelemetryField(f domains.TelemetryField) {
	r.mu.Lock()
	replaced := false
	for i, existing := range r.telemetryFields {
		if existing.Key == f.Key {
			r.telemetryFields[i], replaced = f, true
		}
	}
	if !replaced {
		r.telemetryFields = append(r.telemetryFields, f)
	}
	r.mu.Unlock()
	r.changed()
}

// OnChange calls f after every change of the registered commands or telemetry fields.
func (r *Registry) OnChange(f func()) {
	r.mu.Lock()
	r.listeners = append(r.listeners, f)
	r.mu.Unlock()
}

func (r *Registry) changed() {
	r.mu.RLock()
	listeners := append([]func(){}, r.listeners...)
	r.mu.RUnlock()
	for _, f := range listeners {
		f()
	}
}

// Has reports whether id is registered.
func (r *Registry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.commands[id]
	return ok
}

// Capabilities is the advertised capability set, derived from the registrations only.
func (r *Registry) Capabilities(sn string) *domains.CurrentCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := &domains.CurrentCapabilities{
		SN:              sn,
		AssetType:       r.assetType,
		Capabilities:    make([]domains.Capability, 0, len(r.order)),
		Timestamp:       time.Now(),
		TelemetryFields: append([]domains.TelemetryField(nil), r.telemetryFields...),
	}
	for _, id := range r.order {
		out.Capabilities = append(out.Capabilities, r.commands[id].capability)
	}
	return out
}

// Execute validates the params against the command's input schema and runs its handler. An
// unregistered id is not implemented; params that do not match are rejected with
// schema.InvalidParamsCode before the handler runs.
func (r *Registry) Execute(ctx context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	r.mu.RLock()
	cmd, ok := r.commands[req.CommandID]
	r.mu.RUnlock()
	if !ok {
		return domains.NotImplemented(req.CommandID+" is not supported by this adapter", req.SN), nil
	}
	params, err := cmd.input.Prepare(req.Params)
	if err != nil {
		return domains.Rejected(schema.InvalidParamsCode, req.CommandID+": "+err.Error(), req.SN), nil
	}
	prepared := *req
	prepared.Params = params
	return cmd.handler(ctx, &prepared)
}
