package provider

import (
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// InvalidRequestError reports malformed JSON or arguments that do not satisfy the input schema. The message
// carries no prefix of its own: every diagnostic already leads with the invalid-request code.
type InvalidRequestError struct{ Message string }

func (e *InvalidRequestError) Error() string { return e.Message }

// MayHaveArrived reports whether a request that failed this way may still have reached the provider: it ran
// into a deadline, the connection was reset, or the cause is unknown. A mutation must then say its outcome is
// uncertain instead of presenting the failure as a clean refusal.
func (e *Error) MayHaveArrived() bool {
	return e.Class == ClassTimeout || e.Cause == CauseConnectionReset || e.Cause == CauseUnknown
}

// Fail returns a provider error of the generic provider-error class for the operation.
func Fail(op, message string) *Error {
	return &Error{Class: ClassProviderError, Op: op, Message: message}
}

// InvalidResponse returns a provider error of the invalid-response class for the operation.
func InvalidResponse(op, message string) *Error {
	return &Error{Class: ClassInvalidResponse, Op: op, Message: message}
}

// ValidHeaderToken reports whether value is a printable-ASCII secret of 8 to 4096 characters. It keeps an
// obviously unusable value out of a request header; the provider's own check is the real one.
func ValidHeaderToken(value string) bool {
	if len(value) < 8 || len(value) > 4096 {
		return false
	}
	for i := 0; i < len(value); i++ {
		// A header value may not carry control characters.
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// TargetsOf returns the targets a connection is bound to: its target list or, when that is empty, its single
// non-blank target. It returns the resolved list itself, so callers must not modify it.
func TargetsOf(resolved *config.Resolved) []string {
	if len(resolved.Targets) == 0 && strings.TrimSpace(resolved.Target) != "" {
		return []string{resolved.Target}
	}
	return resolved.Targets
}
