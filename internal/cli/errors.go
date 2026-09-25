package cli

import (
	"errors"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// codeFor maps an error to its provider-independent code. Agents branch on the code instead of parsing
// the message.
func codeFor(err error) output.Code {
	var deadline *deadlineError
	if errors.As(err, &deadline) {
		// An invoke that reached its limit is a timeout, whatever surfaced when it did.
		return output.CodeTimeout
	}
	code := application.ErrorCode(err)
	var (
		projection *output.ProjectionError
		usage      *UsageError
	)
	if code == output.CodeRuntime && (errors.As(err, &projection) || errors.As(err, &usage)) {
		return output.CodeUsage
	}
	return code
}

// route is the way a diagnostic reaches its caller. A next step that is an action of a person reads the
// same on both routes; one that points to discovery names the commands of the CLI or the tools of MCP.
type route int

const (
	routeCLI route = iota
	routeMCP
)

// nextStep is what to do about a refusal err, or "" where the message of the error already says it. It never
// names a secret, a secret source, or a target, so it is safe on every route: at most the name of a provider
// or a tool the request asked about.
func nextStep(err error, r route) string {
	switch codeFor(err) {
	case output.CodeAuth:
		// Some providers answer a credential that lacks access with auth as well, so the step does not
		// claim which of the two happened.
		return "check or renew the credential of this connection with " +
			"'qatlas credential set <credential> <role>' or in 'qatlas tui'"
	case output.CodeUnknownConnection:
		var (
			unknown             *capability.UnknownConnectionError
			provider, operation string
		)
		if errors.As(err, &unknown) {
			provider, operation = unknown.Provider, unknown.Operation
		}
		if r == routeMCP {
			if operation != "" {
				return "call qatlas.describe with operation " + operation +
					" and without connection for the connections that can run it"
			}
			return "call qatlas.describe of the tool without connection for the connections that can run it"
		}
		if provider != "" {
			return "list the configured connections with 'qatlas connections " + provider + "'"
		}
		return "list the configured connections with 'qatlas connections'"
	}
	return ""
}

// nextStepError adds the next step of its code to a diagnostic. Unwrap keeps the classification of the error
// it carries, so code, detail, exit code, and audit stay what they were.
type nextStepError struct {
	err  error
	step string
}

func (e *nextStepError) Error() string { return e.err.Error() + "; " + e.step }
func (e *nextStepError) Unwrap() error { return e.err }

// withNextStep returns err with the next step its code names on route. The CLI and the MCP broker both call
// it before anything of the error is shown, so a message and its detail carry the same text.
func withNextStep(err error, r route) error {
	step := nextStep(err, r)
	if step == "" {
		return err
	}
	return &nextStepError{err: err, step: step}
}

// errorDetail is the machine-readable form of a diagnostic whose code alone does not say how to go on. For
// connection-ambiguous and connection-selection it names every route an explicit connection may choose,
// each with the description its owner maintains and an empty one where there is none, so a caller picks a
// route without parsing the message. connection-selection names none when no connection offers the tool.
// Names and descriptions are all it publishes of a route: never a service, credential, target, or
// secret source.
type errorDetail struct {
	Code        output.Code                 `json:"code"`
	Message     string                      `json:"message"`
	Operation   string                      `json:"operation"`
	Connections []application.ConnectionRef `json:"connections"`
}

// unsupportedDetail is the machine-readable form of unsupported-capability: the refused connection and the
// stable reason the configuration rule gave, the same value 'qatlas tools --all' publishes. The reason is
// empty when a provider refused the request for a reason of its own.
type unsupportedDetail struct {
	Code       output.Code    `json:"code"`
	Message    string         `json:"message"`
	Operation  string         `json:"operation"`
	Connection string         `json:"connection"`
	Reason     config.Refusal `json:"reason"`
}

// errorDetailFor returns the detail of err, or nil when its code and message already say everything. The
// CLI and the MCP broker both publish exactly this value, after the same redaction as the message.
func errorDetailFor(err error, redactor *redact.Redactor) any {
	var deadline *deadlineError
	if errors.As(err, &deadline) {
		return nil
	}
	var unsupported *capability.UnsupportedError
	if errors.As(err, &unsupported) && unsupported.Connection != "" {
		return &unsupportedDetail{
			Code: output.CodeUnsupportedCapability, Message: redactor.Error(err),
			Operation: redactor.Apply(unsupported.Capability), Connection: redactor.Apply(unsupported.Connection),
			Reason: unsupported.Reason,
		}
	}
	var (
		ambiguous *application.ConnectionAmbiguousError
		selection *application.ConnectionSelectionError
		code      output.Code
		operation string
		refs      []application.ConnectionRef
	)
	switch {
	case errors.As(err, &ambiguous):
		code, operation, refs = output.CodeConnectionAmbiguous, ambiguous.Operation, ambiguous.Connections
	case errors.As(err, &selection):
		code, operation, refs = output.CodeConnectionSelection, selection.Operation, selection.Connections
	default:
		return nil
	}
	connections := make([]application.ConnectionRef, len(refs))
	for i, connection := range refs {
		connections[i] = application.ConnectionRef{
			Name: redactor.Apply(connection.Name), Description: redactor.Apply(connection.Description),
		}
	}
	return &errorDetail{
		Code: code, Message: redactor.Error(err), Operation: redactor.Apply(operation), Connections: connections,
	}
}

// classifyUserError marks everything the user can fix in their configuration or invocation as a usage
// problem: a missing, malformed, or inconsistent configuration, an unselectable connection, a capability
// that is not offered, and an unknown projection field. Any other failure, for example an unreadable file,
// stays a runtime error.
func classifyUserError(err error) error {
	if err == nil {
		return nil
	}
	// A timeout is a runtime failure even where the secret it waited for is what surfaced.
	var deadline *deadlineError
	if errors.As(err, &deadline) {
		return err
	}
	var (
		notFound     *config.NotFoundError
		invalid      *config.InvalidError
		selection    *config.SelectionError
		unknownConn  *capability.UnknownConnectionError
		unsupported  *capability.UnsupportedError
		projection   *output.ProjectionError
		invalidReq   *application.InvalidRequestError
		unknownOp    *application.UnknownOperationError
		ambiguous    *application.ConnectionAmbiguousError
		appSelection *application.ConnectionSelectionError
		confirmation *application.ConfirmationRequiredError
		denied       *application.PolicyDeniedError

		missingSecret *secret.MissingSecretError
		permission    *secret.PermissionError
	)
	switch {
	case errors.As(err, &notFound), errors.As(err, &invalid), errors.As(err, &selection),
		errors.As(err, &unknownConn), errors.As(err, &unsupported), errors.As(err, &projection),
		errors.As(err, &missingSecret), errors.As(err, &permission), errors.As(err, &invalidReq),
		errors.As(err, &unknownOp), errors.As(err, &ambiguous), errors.As(err, &confirmation),
		errors.As(err, &appSelection), errors.As(err, &denied):
		return &UsageError{err}
	}
	return err
}
