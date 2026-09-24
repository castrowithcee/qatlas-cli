package application

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
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
