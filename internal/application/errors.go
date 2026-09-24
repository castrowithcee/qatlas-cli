package application

import (
	"fmt"
	"strings"
)

// InvalidRequestError reports malformed JSON or arguments that do not satisfy the input schema. The message
// carries no prefix of its own: every diagnostic already leads with the invalid-request code.
type InvalidRequestError struct{ Message string }

func (e *InvalidRequestError) Error() string { return e.Message }

// UnknownOperationError reports an operation ID or requested version absent from the registry.
type UnknownOperationError struct {
	Operation string
	Version   int
}

func (e *UnknownOperationError) Error() string {
	if e.Version > 0 {
		return fmt.Sprintf("unknown tool %q at version %d", e.Operation, e.Version)
	}
	return fmt.Sprintf("unknown tool %q", e.Operation)
}

// ConnectionAmbiguousError reports that local configuration has several valid routes and no unique default.
// Connections holds every route the operation may take, each with the description its owner maintains, so
// the caller can choose one explicitly. The core never chooses among them by description or by order.
type ConnectionAmbiguousError struct {
	Operation   string
	Connections []ConnectionRef
}

func (e *ConnectionAmbiguousError) Error() string {
	names := make([]string, len(e.Connections))
	for i, connection := range e.Connections {
		names[i] = connection.Name
	}
	return fmt.Sprintf("tool %q has multiple matching connections: %s",
		e.Operation, strings.Join(names, ", "))
}

// ConnectionSelectionError reports a registered operation for which no connection can be selected.
// ExplicitRequired distinguishes an operation contract that deliberately refuses defaults and the
// single-connection fallback. Agent requests carry that connection in JSON rather than a CLI flag.
type ConnectionSelectionError struct {
	Operation        string
	ExplicitRequired bool
}

func (e *ConnectionSelectionError) Error() string {
	if e.ExplicitRequired {
		return fmt.Sprintf("tool %q requires an explicit connection in this invoke request", e.Operation)
	}
	return fmt.Sprintf("no configured connection can invoke tool %q", e.Operation)
}

// ConfirmationRequiredError reports a mutating request without its request-bound confirmation.
type ConfirmationRequiredError struct{ Operation string }

func (e *ConfirmationRequiredError) Error() string {
	return fmt.Sprintf("tool %q requires confirmation in this invoke request", e.Operation)
}

// PolicyDeniedError deliberately omits policy internals from the public diagnostic.
type PolicyDeniedError struct{ Operation string }

func (e *PolicyDeniedError) Error() string {
	return fmt.Sprintf("policy denied tool %q", e.Operation)
}

// InvalidProviderResponseError reports output that does not satisfy the registered contract.
type InvalidProviderResponseError struct{ Operation string }

func (e *InvalidProviderResponseError) Error() string {
	return fmt.Sprintf("tool %q returned an invalid provider response", e.Operation)
}
