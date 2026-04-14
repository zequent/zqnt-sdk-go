// Package domains contains the domain models used by the EdgeAdapter interface.
package domains

// ResultType indicates the outcome category of an edge command.
type ResultType int

const (
	ResultTypeSuccess        ResultType = iota // Command executed successfully.
	ResultTypeError                            // Command failed with an asset or system error.
	ResultTypeNotImplemented                   // Command is not supported by this adapter.
)

func (r ResultType) String() string {
	switch r {
	case ResultTypeSuccess:
		return "SUCCESS"
	case ResultTypeError:
		return "ERROR"
	case ResultTypeNotImplemented:
		return "NOT_IMPLEMENTED"
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
