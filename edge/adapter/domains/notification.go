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
// ExternalExecutionID is the key the platform correlates by, and for a command that arrived from
// capability execution it is the id the platform itself sent: EdgeExecutionNodeDispatcher builds
// "capexec:<executionId>:<nodeId>" and puts it in RequestBase.tid, which reaches the adapter as
// the request's TID. Echoing that value back is what binds this event to the waiting node. An
// adapter reporting a command it minted itself (a vendor flight id, say) may use that instead,
// provided the same id was returned to the platform when the command was accepted.
//
// SN, ExternalExecutionID and OccurredAt are all required on the wire -- live-data's
// CommandExecutionEventPublisher rejects an event missing any of them. The rejection is invisible
// from here, because notifications are published fire-and-forget over a stream: the publish call
// succeeds and the event is simply dropped, leaving the node waiting. PublishCommandExecutionEvent
// therefore defaults OccurredAt rather than letting a zero value through.
type CommandExecutionEvent struct {
	SN                  string
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
	// OccurredAt is when the adapter observed this. Defaults to publish time when zero.
	OccurredAt time.Time
}
