// Package domains contains the domain models used by the EdgeAdapter interface.
package domains

// ResultType indicates the outcome category of an edge command.
type ResultType int

const (
	ResultTypeSuccess        ResultType = iota // Command executed successfully.
	ResultTypeError                            // Command failed with an asset or system error.
	ResultTypeNotImplemented                   // Command is not supported by this adapter.
	ResultTypeRejected                         // Command was refused before it started; see ErrorCode.
)

func (r ResultType) String() string {
	switch r {
	case ResultTypeSuccess:
		return "SUCCESS"
	case ResultTypeError:
		return "ERROR"
	case ResultTypeNotImplemented:
		return "NOT_IMPLEMENTED"
	case ResultTypeRejected:
		return "REJECTED"
	default:
		return "UNKNOWN"
	}
}

// CommandResult is the standard return value for all EdgeAdapter operations.
type CommandResult struct {
	Success    bool
	Message    string
	TID        string
	SN         string
	ResultType ResultType
	// ExternalExecutionID is the vendor's own id for a command that is accepted now and finishes
	// later (a flight id). Set it, and the command is reported ACCEPTED: the platform waits for
	// the CommandExecutionEvent carrying this id. Use Accepted to build such a result.
	ExternalExecutionID string
	// Output is the command's result, shaped by its output schema. Values must be JSON-shaped.
	Output map[string]any
	// ErrorCode is a stable, machine-readable code for a failed or rejected command, e.g.
	// "command.invalid_params" or one of the codes the capability declares in Errors.
	ErrorCode string
}

// Rejected is a command refused before it started, e.g. params that do not match its schema.
func Rejected(code, message, sn string) *CommandResult {
	return &CommandResult{Success: false, Message: message, SN: sn, ResultType: ResultTypeRejected, ErrorCode: code}
}

// IsRejected reports whether the command was refused before it started.
func (r *CommandResult) IsRejected() bool {
	return r != nil && r.ResultType == ResultTypeRejected
}

// SuccessWithOutput is a finished command that returns a result.
func SuccessWithOutput(message, sn string, output map[string]any) *CommandResult {
	return &CommandResult{Success: true, Message: message, SN: sn, ResultType: ResultTypeSuccess, Output: output}
}

// Accepted is a success that only started the command: its outcome follows as a
// CommandExecutionEvent under externalExecutionID.
func Accepted(message, externalExecutionID, sn string) *CommandResult {
	return &CommandResult{Success: true, Message: message, SN: sn, ResultType: ResultTypeSuccess,
		ExternalExecutionID: externalExecutionID}
}

// IsSuccess reports whether the command succeeded.
func (r *CommandResult) IsSuccess() bool {
	return r != nil && r.ResultType == ResultTypeSuccess
}

// IsNotImplemented reports whether the command is not implemented.
func (r *CommandResult) IsNotImplemented() bool {
	return r != nil && r.ResultType == ResultTypeNotImplemented
}

// Success returns a successful CommandResult.
func Success(message, sn string) *CommandResult {
	return &CommandResult{Success: true, Message: message, SN: sn, ResultType: ResultTypeSuccess}
}

// SuccessWithTID returns a successful CommandResult with a transaction ID.
func SuccessWithTID(message, tid, sn string) *CommandResult {
	return &CommandResult{Success: true, Message: message, TID: tid, SN: sn, ResultType: ResultTypeSuccess}
}

// Error returns a failed CommandResult.
func Error(message, sn string) *CommandResult {
	return &CommandResult{Success: false, Message: message, SN: sn, ResultType: ResultTypeError}
}

// ErrorWithTID returns a failed CommandResult with a transaction ID.
func ErrorWithTID(message, tid, sn string) *CommandResult {
	return &CommandResult{Success: false, Message: message, TID: tid, SN: sn, ResultType: ResultTypeError}
}

// NotImplemented returns a CommandResult indicating the operation is not supported.
func NotImplemented(message, sn string) *CommandResult {
	return &CommandResult{Success: false, Message: message, SN: sn, ResultType: ResultTypeNotImplemented}
}
