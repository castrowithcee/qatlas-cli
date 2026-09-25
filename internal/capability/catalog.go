package capability

import (
	"fmt"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// UnknownConnectionError reports a connection that is not configured. Suggestion is the configured name the
// unknown one most likely misspells, or empty.
type UnknownConnectionError struct {
	Name       string
	Suggestion string
	// Provider and Operation name what the request asked the connection for, where it named that; the
	// next step of the diagnostic points to the connections configured for them.
	Provider  string
	Operation string
}

func (e *UnknownConnectionError) Error() string {
	if e.Suggestion != "" {
		return fmt.Sprintf("unknown connection %q (did you mean %q?)", e.Name, e.Suggestion)
	}
	return fmt.Sprintf("unknown connection %q", e.Name)
}

// UnsupportedError reports that an operation is not offered. Connection is empty when no configured
// connection offers it at all. Reason is the refusal of the configuration rule when that rule decided, and
// empty when a provider refused the request for a reason of its own.
type UnsupportedError struct {
	Connection string
	Capability string
	Reason     config.Refusal
}

func (e *UnsupportedError) Error() string {
	if e.Connection == "" {
		return fmt.Sprintf("no configured connection offers tool %q", e.Capability)
	}
	message := fmt.Sprintf("connection %q does not offer tool %q", e.Connection, e.Capability)
	if e.Reason != "" {
		message += fmt.Sprintf(" (%s)", e.Reason)
	}
	// The next step names discovery, which reads differently on the command line and over MCP, so the
	// surface that shows the message appends it.
	return message
}
