package application

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// InvalidRequestError reports malformed JSON or arguments that do not satisfy the input schema. The message
// carries no prefix of its own: every diagnostic already leads with the invalid-request code.
type InvalidRequestError struct{ Message string }

func (e *InvalidRequestError) Error() string { return e.Message }

// UnknownOperationError reports an operation ID or requested version absent from the registry. Suggestion
// is the registered ID the unknown one most likely misspells, or empty.
type UnknownOperationError struct {
	Operation  string
	Version    int
	Suggestion string
}

func (e *UnknownOperationError) Error() string {
	if e.Version > 0 {
		return fmt.Sprintf("unknown tool %q at version %d", e.Operation, e.Version)
	}
	return fmt.Sprintf("unknown tool %q", e.Operation) + DidYouMean(e.Suggestion)
}

// Suggest returns the candidate that name most likely misspells: the closest one by edit distance, ignoring
// case, when at most a third of name's characters, and at least one, differ. It returns "" when none is that
// close or name is itself a candidate. Ties go to the candidate that comes first in sorted order, so the
// answer is deterministic. The candidates are names of the registry or the local configuration, never a
// value.
func Suggest(name string, candidates []string) string {
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)
	best, bestDistance := "", max(1, utf8.RuneCountInString(name)/3)+1
	for _, candidate := range sorted {
		if candidate == name {
			return ""
		}
		if distance := editDistance(strings.ToLower(name), strings.ToLower(candidate)); distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	return best
}

// DidYouMean phrases a suggestion as the suffix of a diagnostic, or returns "" without one.
func DidYouMean(suggestion string) string {
	if suggestion == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", suggestion)
}

// editDistance is the Levenshtein distance of a and b in runes.
func editDistance(a, b string) int {
	source, target := []rune(a), []rune(b)
	previous := make([]int, len(target)+1)
	current := make([]int, len(target)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(source); i++ {
		current[0] = i
		for j := 1; j <= len(target); j++ {
			cost := 1
			if source[i-1] == target[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(target)]
}

// ConnectionAmbiguousError reports that local configuration has several valid routes and no unique default.
// Connections holds every route the operation may take, each with the description its owner maintains, so
// the caller can choose one explicitly. The core never chooses among them by description or by order.
type ConnectionAmbiguousError struct {
	Operation   string
	Connections []ConnectionRef
}

func (e *ConnectionAmbiguousError) Error() string {
	return fmt.Sprintf("tool %q has multiple matching connections: %s", e.Operation, candidates(e.Connections))
}

// ConnectionSelectionError reports a registered operation for which no connection can be selected.
// ExplicitRequired distinguishes an operation contract that deliberately refuses defaults and the
// single-connection fallback. Agent requests carry that connection in JSON rather than a CLI flag.
// Connections holds every route that offers the operation, each with the description its owner maintains,
// so the caller can name one; it is empty when no configured connection offers it.
type ConnectionSelectionError struct {
	Operation        string
	ExplicitRequired bool
	Connections      []ConnectionRef
}

func (e *ConnectionSelectionError) Error() string {
	if e.ExplicitRequired {
		message := fmt.Sprintf("tool %q requires an explicit connection in this invoke request", e.Operation)
		if len(e.Connections) > 0 {
			message += "; the connections that offer it: " + candidates(e.Connections)
		}
		return message
	}
	return fmt.Sprintf("no configured connection can invoke tool %q", e.Operation)
}

// maxCandidateDescription bounds the description a diagnostic shows of each candidate route, so a message
// that names many routes stays readable where only its text is shown.
const maxCandidateDescription = 80

// candidates names every route a caller may choose, each with the description its owner maintains in
// parentheses, shortened to maxCandidateDescription characters, and alone where there is none. The detail of
// the diagnostic keeps every description in full.
func candidates(connections []ConnectionRef) string {
	named := make([]string, len(connections))
	for i, connection := range connections {
		named[i] = connection.Name
		if description := []rune(connection.Description); len(description) > maxCandidateDescription {
			named[i] += " (" + string(description[:maxCandidateDescription-1]) + "…)"
		} else if len(description) > 0 {
			named[i] += " (" + connection.Description + ")"
		}
	}
	return strings.Join(named, ", ")
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

// AdminRequiredError reports that a CLI command managing a credential or the vault was refused before it
// did anything: storing or removing a credential's secret of any type, and switching the vault's encryption
// on, off, or to a new passphrase, all run only when a person is at an interactive terminal, checked before
// any file, credential store, or vault access the command would otherwise make. An agent never manages a
// credential or the vault, whatever route it reaches qatlas by; it asks the user to run the command
// themselves instead.
type AdminRequiredError struct{}

func (e *AdminRequiredError) Error() string {
	return "managing a credential or the vault needs a person at an interactive terminal"
}

// ErrorCode maps an error of the core, the configuration, the secrets, or a provider to its
// provider-independent code, and anything else to runtime. Agents branch on the code instead of parsing the
// message, and the audit event of a failed change records the same code.
func ErrorCode(err error) output.Code {
	var (
		notFound      *config.NotFoundError
		invalid       *config.InvalidError
		selection     *config.SelectionError
		unknownConn   *capability.UnknownConnectionError
		unsupported   *capability.UnsupportedError
		invalidReq    *InvalidRequestError
		unknownOp     *UnknownOperationError
		ambiguous     *ConnectionAmbiguousError
		appSelection  *ConnectionSelectionError
		confirmation  *ConfirmationRequiredError
		denied        *PolicyDeniedError
		invalidResult *InvalidProviderResponseError
		adminReq      *AdminRequiredError

		missingSecret   *secret.MissingSecretError
		permission      *secret.PermissionError
		vaultPermission *vault.PermissionError
		vaultLocked     *secret.VaultLockedError
		providerErr     *provider.Error
	)
	switch {
	case errors.As(err, &notFound):
		return output.CodeConfigMissing
	case errors.As(err, &invalid):
		return output.CodeConfigInvalid
	case errors.As(err, &selection):
		// A name that does not exist is the same problem however the command reached it.
		if selection.Name != "" {
			return output.CodeUnknownConnection
		}
		return output.CodeConnectionSelection
	case errors.As(err, &unknownConn):
		return output.CodeUnknownConnection
	case errors.As(err, &ambiguous):
		return output.CodeConnectionAmbiguous
	case errors.As(err, &appSelection):
		return output.CodeConnectionSelection
	case errors.As(err, &unknownOp):
		return output.CodeUnknownOperation
	case errors.As(err, &unsupported):
		return output.CodeUnsupportedCapability
	case errors.As(err, &invalidReq):
		return output.CodeInvalidRequest
	case errors.As(err, &confirmation):
		return output.CodeConfirmationRequired
	case errors.As(err, &denied):
		return output.CodePolicyDenied
	case errors.As(err, &invalidResult):
		return output.CodeInvalidProviderResult
	case errors.As(err, &adminReq):
		return output.CodeAdminRequired
	case errors.As(err, &permission), errors.As(err, &vaultPermission):
		// A credential file others can read is one state with one fix, whichever operation ran into it.
		// It is named before the missing secret it causes, so reading, writing, and deleting all report
		// the file rather than three different things. A vault file that is too open follows the same
		// rule.
		return output.CodeConfigInvalid
	case errors.As(err, &missingSecret):
		return output.CodeMissingSecret
	case errors.As(err, &vaultLocked):
		// A locked vault is a runtime state, not a configuration mistake: unlocking it and retrying needs
		// no change to the file, unlike every code above.
		return output.CodeVaultLocked
	case errors.As(err, &providerErr):
		return providerCode(providerErr.Class)
	}
	return output.CodeRuntime
}

func providerCode(class provider.Class) output.Code {
	switch class {
	case provider.ClassUnreachable:
		return output.CodeUnreachable
	case provider.ClassTLS:
		return output.CodeTLS
	case provider.ClassAuth:
		return output.CodeAuth
	case provider.ClassPermission:
		return output.CodePermission
	case provider.ClassNotFound:
		return output.CodeNotFound
	case provider.ClassTimeout:
		return output.CodeTimeout
	case provider.ClassRateLimited:
		return output.CodeRateLimited
	case provider.ClassInvalidResponse:
		return output.CodeInvalidProviderResult
	default:
		return output.CodeProviderError
	}
}
