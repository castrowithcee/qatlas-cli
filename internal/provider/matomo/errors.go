package matomo

import (
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// missingError is Matomo's answer for a method, module, or plugin the instance lacks. It never leaves the
// package: call turns it into the refusal of the tool.
type missingError struct {
	// granular marks the answer of a switched-off feature flag, which has its own hint.
	granular bool
}

func (*missingError) Error() string { return "the Matomo instance lacks the method" }

// answeredError is a failure Matomo itself reported inside an HTTP 200 answer, as opposed to one of the
// transport or an HTTP status. It unwraps to the provider error that carries the class.
type answeredError struct{ failure *provider.Error }

func (e *answeredError) Error() string { return e.failure.Error() }
func (e *answeredError) Unwrap() error { return e.failure }

// unavailableError is the refusal of a tool the instance cannot serve. It is an unsupported capability of
// the connection; the message names the version or plugin the tool needs, never a Matomo text.
type unavailableError struct {
	*capability.UnsupportedError
	message string
}

func (e *unavailableError) Error() string { return e.message }
func (e *unavailableError) Unwrap() error { return e.UnsupportedError }

// messagePatterns map the English texts Matomo puts in the message of an application error to a class.
// Matomo translates these texts into the language of the token's user, so another language falls through to
// invalid-provider-response. The first matching group wins; a pattern is matched on the lower-cased text with
// every quoted name masked, so a name like 'tokenadmin' cannot pick the class.
var messagePatterns = []struct {
	class    string
	patterns []string
}{
	// These come first: their texts also hold words of the groups below, and one quotes a setting ID.
	{"password-refused", []string{"the current password you entered is not correct", "re-authenticate to confirm"}},
	{"granular-off", []string{"granular compliance configuration is not enabled"}},
	{"config-controlled", []string{"controlled by the config file"}},
	{"unknown-setting", []string{"is unknown or cannot be toggled"}},
	{"invalid", []string{"the specified segment is invalid", "invalid name for this custom segment"}},
	// Refusals of the Tag Manager version tools; Matomo changed nothing.
	{"version-name-in-use", []string{"this name is already in use"}},
	{"version-not-deletable", []string{"version of a container cannot be deleted",
		"cannot be deleted as it is published to at least one environment"}},
	{"missing", []string{"not available in the module", "not activated", "not enabled", "deactivated"}},
	// A container, environment, or context ID is quoted in double quotes and is not masked; this comes before the
	// groups below so that an ID like token1 cannot pick the class.
	{"not-found", []string{`" does not exist`, `the context "`}},
	{"permission", []string{"you can't access this resource", "super user", "not allowed to", "required access level",
		"requires permission", "adding segments for all websites has been disabled", "real time segments are disabled",
		"pre-processed segments can only", "does not have access to any sites"}},
	{"auth", []string{"token", "authenticat", "password", "logged in", "login"}},
	{"not-found", []string{"unexpected website", "website not found", "was not found", "does not exist",
		"not found in the list of available reports", "there is no goal", "requested segment not found",
		"marked as deleted", "there is no note", "couldn't be found", "doesn't exist", "invalid compliance", "nodataforaction"}},
}

// applicationError reads a JSON answer for Matomo's {"result":"error"} form and maps it. It returns nil for
// any other answer. The message text is only matched, never copied.
func applicationError(op string, data []byte) error {
	var answer struct {
		Result  string          `json:"result"`
		Message json.RawMessage `json:"message"`
	}
	if len(data) == 0 || data[0] != '{' || json.Unmarshal(data, &answer) != nil || answer.Result != "error" {
		return nil
	}
	var message string
	_ = json.Unmarshal(answer.Message, &message)
	switch classifyMessage(message) {
	case "invalid":
		return &provider.InvalidRequestError{Message: "Matomo refused the segment name or definition"}
	case "version-name-in-use":
		return &provider.InvalidRequestError{Message: "Matomo refused the name: another version of the container " +
			"already uses it; nothing was changed"}
	case "version-not-deletable":
		return &provider.InvalidRequestError{Message: "Matomo refused the deletion: the draft and a version that is " +
			"released to an environment cannot be deleted; nothing was changed"}
	case "missing":
		return &missingError{}
	case "password-refused":
		return &answeredError{&provider.Error{Class: provider.ClassAuth, Op: op,
			Message: "Matomo refused the password confirmation; check the secret role " + rolePasswordConfirmation +
				" of this connection's credential"}}
	case "granular-off":
		return &missingError{granular: true}
	case "config-controlled":
		return &answeredError{&provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "the compliance policy is controlled by the Matomo configuration file and cannot be changed " +
				"through the API; change it there"}}
	case "unknown-setting":
		return &provider.InvalidRequestError{Message: "Matomo refused a setting ID: it is unknown for this policy or cannot be toggled"}
	case "auth":
		return &answeredError{&provider.Error{Class: provider.ClassAuth, Op: op, Message: "Matomo rejected the token"}}
	case "permission":
		return &answeredError{&provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this token may not use the requested website or function; check its rights for the website"}}
	case "not-found":
		return &answeredError{&provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Matomo does not hold this resource or does not show it to this token"}}
	}
	return provider.InvalidResponse(op, "Matomo reported an error this provider does not recognise")
}

func classifyMessage(message string) string {
	lower := maskQuoted(strings.ToLower(message))
	for _, group := range messagePatterns {
		for _, pattern := range group.patterns {
			if strings.Contains(lower, pattern) {
				return group.class
			}
		}
	}
	return ""
}

// maskQuoted empties each quoted section and keeps its two quotes. A quote opens only at the start or after a
// space or one of "(=:#" and closes only before a space, punctuation, or the end, so the apostrophe in
// "doesn't" or "o'brien" neither opens nor closes a section.
func maskQuoted(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\'' && (i == 0 || strings.IndexByte(" \t\r\n(=:#", text[i-1]) >= 0) {
			if end := closingQuote(text, i+1); end >= 0 {
				out.WriteString("''")
				i = end
				continue
			}
		}
		out.WriteByte(text[i])
	}
	return out.String()
}

func closingQuote(text string, from int) int {
	for j := from; j < len(text); j++ {
		if text[j] == '\'' && (j+1 == len(text) || strings.IndexByte(" \t\r\n.,;:!?)", text[j+1]) >= 0) {
			return j
		}
	}
	return -1
}
