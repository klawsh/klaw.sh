package agent

import "fmt"

// ErrorCode identifies categories of agent errors.
type ErrorCode string

const (
	ErrMaxIterations ErrorCode = "max_iterations"
	ErrProvider      ErrorCode = "provider_error"
	ErrToolExec      ErrorCode = "tool_execution"
	ErrContextLimit  ErrorCode = "context_limit"
	ErrBudgetExceed  ErrorCode = "budget_exceeded"
)

// AgentError is a structured error with a machine-readable code.
type AgentError struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *AgentError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *AgentError) Unwrap() error {
	return e.Cause
}
