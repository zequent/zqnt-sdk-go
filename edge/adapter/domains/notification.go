package domains

import "time"

// CommandExecutionStatus is the lifecycle stage of one physical command dispatched to an adapter.
//
// This is the vendor-neutral feedback channel mission-autonomy correlates a skill node against.
// The typed flight commands (flight.takeoff, navigation.go_to, gimbal.look_at,
// flight.return_to_home) and every custom command are dispatched *asynchronously*:
// EdgeExecutionNodeDispatcher puts the node in ACCEPTED and then waits for an event carrying the
// node's external execution id before the graph advances. An adapter that never publishes one
// leaves the node running until the execution times out, however promptly its RPC answered.
type CommandExecutionStatus int

const (
	CommandExecutionAccepted  CommandExecutionStatus = iota + 1 // The device took the command.
	CommandExecutionRunning                                     // In progress; carries Progress.
	CommandExecutionSucceeded                                   // Finished; the node completes.
	CommandExecutionFailed                                      // Finished badly; the node fails.
	CommandExecutionCancelled                                   // Stopped before finishing.
)

func (s CommandExecutionStatus) String() string {
	switch s {
	case CommandExecutionAccepted:
		return "ACCEPTED"
	case CommandExecutionRunning:
		return "RUNNING"
	case CommandExecutionSucceeded:
		return "SUCCEEDED"
	case CommandExecutionFailed:
		return "FAILED"
	case CommandExecutionCancelled:
		return "CANCELLED"
	default:
		return "UNSPECIFIED"
	}
}

// CommandExecutionEvent is one lifecycle report for a command the platform is waiting on.
//
// The platform matches it to the waiting node by its id: on v3 the command_execution_id it sent
// with ExecuteCommand (also the request's TID), on v2 the "capexec:<execution>:<node>" TID. An
// adapter reporting a command under its own vendor id may use that instead, provided it returned
// the same id when the command was accepted.
//
// SN, the id and OccurredAt are required on the wire; the SDK sets OccurredAt to the publish time
// when it is zero, because an event without it is refused (v3) or silently dropped (v2).
type CommandExecutionEvent struct {
	SN string
	// CommandExecutionID is the platform's id of the run (v3 ExecuteCommand's
	// command_execution_id, domains.CustomCommandRequest.CommandExecutionID). Takes precedence over
	// ExternalExecutionID when both are set.
	CommandExecutionID  string
	ExternalExecutionID string
	CommandID           string
	Status              CommandExecutionStatus
	// Progress is 0..1 and belongs on a RUNNING event; nil leaves the node's progress untouched.
	// mission-autonomy clamps it and recomputes the whole execution's progress from it.
	Progress *float32
	Message  string
	// Output is handed to the completing node as its result, for a SUCCEEDED event. Values must be
	// JSON-shaped (the wire type is a google.protobuf.Struct).
	Output map[string]any
	// ErrorCode is a machine-readable code for a FAILED event, ideally one the capability declares.
	ErrorCode string
	// OccurredAt is when the adapter observed this. Defaults to publish time when zero.
	OccurredAt time.Time
}

// ExecutionID is the id the platform matches this event by.
func (e *CommandExecutionEvent) ExecutionID() string {
	if e.CommandExecutionID != "" {
		return e.CommandExecutionID
	}
	return e.ExternalExecutionID
}
