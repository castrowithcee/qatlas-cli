package capability

import (
	"fmt"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// UnknownConnectionError reports a connection that is not configured.
type UnknownConnectionError struct{ Name string }

func (e *UnknownConnectionError) Error() string {
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
		return fmt.Sprintf("no configured connection offers capability %q", e.Capability)
	}
	message := fmt.Sprintf("connection %q does not offer capability %q", e.Connection, e.Capability)
	if e.Reason != "" {
		message += fmt.Sprintf(" (%s)", e.Reason)
	}
	// The next step is the same whatever the reason: pick a route that offers the tool, or change this one.
	return message + fmt.Sprintf("; 'qatlas describe %s' names the connections that offer it, or change "+
		"the connection in 'qatlas tui'", e.Capability)
}
