package config

import (
	"errors"
	"fmt"
	"strconv"
)

// Sections of the configuration file a Problem is reported under.
const (
	SectionVersion       = "version"
	SectionServices      = "services"
	SectionCredentials   = "credentials"
	SectionConnections   = "connections"
	SectionDefaults      = "defaults"
	SectionVault         = "vault"
	SectionLogs          = "logs"
	SectionProviderNotes = "provider_notes"
)

// Problem codes. Each names one rule; Hint says how to resolve it.
const (
	CodeUnsupportedVersion           = "unsupported-version"
	CodeInvalidName                  = "invalid-name"
	CodeProviderMissing              = "provider-missing"
	CodeUnknownProvider              = "unknown-provider"
	CodeInvalidBaseURL               = "invalid-base-url"
	CodeUnknownSecretRole            = "unknown-secret-role"
	CodeSecretRolesMissing           = "secret-roles-missing"
	CodeEmptySecretRole              = "empty-secret-role"
	CodeInvalidEnvName               = "invalid-env-name"
	CodeSecretValuesNotAllowed       = "secret-values-not-allowed"
	CodeInvalidCredentialType        = "invalid-credential-type"
	CodeForwardFieldsNotAllowed      = "forward-fields-not-allowed"
	CodeForwardDescriptionNotAllowed = "forward-description-not-allowed"
	CodeInvalidDescription           = "invalid-description"
	CodeForwardProviderNotAllowed    = "forward-provider-not-allowed"
	CodeInvalidForwardType           = "invalid-forward-type"
	CodeForwardFieldsMissing         = "forward-fields-missing"
	CodeInvalidFieldName             = "invalid-field-name"
	CodeDuplicateField               = "duplicate-field"
	CodeUnknownService               = "unknown-service"
	CodeUnknownCredential            = "unknown-credential"
	CodeForwardCredentialAsLogin     = "forward-credential-as-login"
	CodeUnknownForwardSecret         = "unknown-forward-secret"
	CodeNotForwardCredential         = "not-forward-credential"
	CodeDuplicateForwardSecret       = "duplicate-forward-secret"
	CodeProviderMismatch             = "provider-mismatch"
	CodeSecretRoleMissing            = "secret-role-missing"
	CodePathsEmpty                   = "paths-empty"
	CodeInvalidPath                  = "invalid-path"
	CodeDuplicatePath                = "duplicate-path"
	CodeUnknownPermission            = "unknown-permission"
	CodeDuplicatePermission          = "duplicate-permission"
	CodeTargetRequired               = "target-required"
	CodeTargetAndTargets             = "target-and-targets"
	CodeTargetsSingleOnly            = "targets-single-only"
	CodeEmptyTarget                  = "empty-target"
	CodeDuplicateTarget              = "duplicate-target"
	CodeInvalidTarget                = "invalid-target"
	CodeInvalidTargetSet             = "invalid-target-set"
	CodeWildcardNotAlone             = "wildcard-not-alone"
	CodeDuplicateTool                = "duplicate-tool"
	CodeToolEffectNotPermitted       = "tool-effect-not-permitted"
	CodeToolOfOtherProvider          = "tool-of-other-provider"
	CodeUnknownToolEntry             = "unknown-tool-entry"
	CodeFilesEmpty                   = "files-empty"
	CodeFilesDirectionUnsupported    = "files-direction-unsupported"
	CodeInvalidFilesEntry            = "invalid-files-entry"
	CodeDuplicateFilesEntry          = "duplicate-files-entry"
	CodeInvalidNote                  = "invalid-note"
	CodeUnknownConnection            = "unknown-connection"
	CodeInvalidSecretStore           = "invalid-secret-store"
	CodeInvalidTimeout               = "invalid-timeout"
	CodeInvalidRetention             = "invalid-retention"
	CodeInvalidID                    = "invalid-id"
	CodeDuplicateID                  = "duplicate-id"
)

// hints maps every code to the English advice that resolves it. The rules live here, in the core, so every
// surface says the same thing. Hints name no provider and no value.
var hints = map[string]string{
	CodeUnsupportedVersion:           "Set the configuration version to a supported one.",
	CodeInvalidName:                  "Rename the entry to a name that follows the naming rule.",
	CodeProviderMissing:              "Choose a registered provider for the service.",
	CodeUnknownProvider:              "Choose a registered provider, or remove the entry.",
	CodeInvalidBaseURL:               "Correct the base URL to an endpoint the provider accepts, or leave it out to use the default.",
	CodeUnknownSecretRole:            "Rename the role to one the provider defines, or remove it.",
	CodeSecretRolesMissing:           "Add at least one secret role with the name of its environment variable.",
	CodeEmptySecretRole:              "Give the secret role a name, or remove the entry.",
	CodeInvalidEnvName:               "Name an environment variable that follows the naming rule.",
	CodeSecretValuesNotAllowed:       "Remove the values; this credential type keeps its secrets in its own store.",
	CodeInvalidCredentialType:        "Choose one of the supported credential types.",
	CodeForwardFieldsNotAllowed:      "Remove the fields, or mark the credential as a forward credential.",
	CodeForwardDescriptionNotAllowed: "Remove the description, or mark the credential as a forward credential.",
	CodeInvalidDescription:           "Shorten the text to a single line within the length limit.",
	CodeForwardProviderNotAllowed:    "Remove the provider; a forward credential belongs to no provider.",
	CodeInvalidForwardType:           "Choose a credential type that can hold a forward credential.",
	CodeForwardFieldsMissing:         "Add at least one field to the forward credential.",
	CodeInvalidFieldName:             "Rename the field to a name that follows the naming rule.",
	CodeDuplicateField:               "Remove the repeated field.",
	CodeUnknownService:               "Choose a service that exists, or add it.",
	CodeUnknownCredential:            "Choose a credential that exists, or add it.",
	CodeForwardCredentialAsLogin:     "Choose a credential that is not a forward credential.",
	CodeUnknownForwardSecret:         "Choose a forward credential that exists, or remove the entry.",
	CodeNotForwardCredential:         "List only forward credentials, or mark the credential as one.",
	CodeDuplicateForwardSecret:       "Remove the repeated entry.",
	CodeProviderMismatch:             "Choose a credential of the same provider as the service, or one without a provider.",
	CodeSecretRoleMissing:            "Add the secret role to the credential.",
	CodePathsEmpty:                   "List at least one directory, or leave the list out to use the connection everywhere.",
	CodeInvalidPath:                  "Use an absolute directory or one starting with '~/', without glob patterns.",
	CodeDuplicatePath:                "Remove the repeated directory.",
	CodeUnknownPermission:            "Use one of the supported permissions.",
	CodeDuplicatePermission:          "Remove the repeated permission.",
	CodeTargetRequired:               "Set the target the provider requires.",
	CodeTargetAndTargets:             "Use either a single target or a list of targets, not both.",
	CodeTargetsSingleOnly:            "Keep a single target, as the provider accepts only one.",
	CodeEmptyTarget:                  "Fill in the target, or remove the entry.",
	CodeDuplicateTarget:              "Remove the repeated target.",
	CodeInvalidTarget:                "Correct the target to the form the provider accepts.",
	CodeInvalidTargetSet:             "Correct the list of targets to the combination the provider accepts.",
	CodeWildcardNotAlone:             "Keep only the wildcard as target, or list explicit targets without it.",
	CodeDuplicateTool:                "Remove the repeated tool.",
	CodeToolEffectNotPermitted:       "Tick the effect again or untick these tools.",
	CodeToolOfOtherProvider:          "Remove the tool; it belongs to another provider.",
	CodeUnknownToolEntry:             "Remove the entry, or choose a registered tool of the provider.",
	CodeFilesEmpty:                   "List at least one directory, or leave the direction out to release none.",
	CodeFilesDirectionUnsupported:    "Remove the direction; the provider has no tool that uses it.",
	CodeInvalidFilesEntry:            "Use a directory that is neither the file system root nor the whole home directory, without '..'.",
	CodeDuplicateFilesEntry:          "Remove the repeated directory.",
	CodeInvalidNote:                  "Shorten the note to a single line within the length limit.",
	CodeUnknownConnection:            "Choose a connection that exists, or remove the default.",
	CodeInvalidSecretStore:           "Choose one of the supported secret stores.",
	CodeInvalidTimeout:               "Use a duration within the supported range, such as 15m.",
	CodeInvalidRetention:             "Use a positive number of days.",
	CodeInvalidID:                    "Remove the identifier; a new one is assigned when the file is saved.",
	CodeDuplicateID:                  "Remove the identifier from one of the entries; a new one is assigned when the file is saved.",
}

// Problem is one validation finding, structured so that a surface can attach it to a form row and offer a
// resolution. Error() renders the text Validate has always reported. Parameters carry only configuration
// keys, entry names, registered IDs and positions, never a free value such as a target, URL, path or
// description: those appear only in the rendered text, which Error and Detail return.
type Problem struct {
	// Section is the top-level configuration key.
	Section string
	// Name is the entry name within the section, "" when the problem concerns the section itself.
	Name string
	// Field is the key path below the entry, such as "base_url", "values.<role>" or "files.read".
	Field string
	// Index is the position within the field as the rendered text shows it: 0-based, except for the
	// unknown-tool-entry code, which counts from 1. Valid only when HasIndex is set.
	Index    int
	HasIndex bool
	// Code names the violated rule; see the Code constants.
	Code string
	// Params name registered IDs and keys the problem refers to, keyed by parameter name.
	Params map[string]string

	detail string
	cause  error
	// shown is the location the rendered text uses when it is less specific than the structure.
	shown string
}

// problem builds a Problem whose rendered text is the location followed by the formatted detail.
func problem(section, name, field, code, format string, args ...any) Problem {
	return Problem{Section: section, Name: name, Field: field, Code: code, detail: fmt.Sprintf(format, args...)}
}

// at sets the position and returns the Problem.
func (p Problem) at(index int) Problem {
	p.Index, p.HasIndex = index, true
	return p
}

// refined sets the field the problem belongs to while the rendered text keeps its location, so the text
// stays what it has always been.
func (p Problem) refined(field string) Problem {
	p.shown = p.Location()
	p.Field = field
	return p
}

// with adds a parameter and returns the Problem.
func (p Problem) with(key, value string) Problem {
	params := make(map[string]string, len(p.Params)+1)
	for k, v := range p.Params {
		params[k] = v
	}
	params[key] = value
	p.Params = params
	return p
}

// because attaches the error of a provider-owned validator, which stays opaque to the core.
func (p Problem) because(err error) Problem {
	p.cause = err
	return p
}

// Location returns the dotted key path of the problem, such as "connections.work.paths[0]".
func (p Problem) Location() string {
	where := p.Section
	if p.Name != "" {
		where += "." + p.Name
	}
	if p.Field != "" {
		where += "." + p.Field
	}
	if p.HasIndex {
		where += "[" + strconv.Itoa(p.Index) + "]"
	}
	return where
}

// Detail returns the rendered rule text without its location.
func (p Problem) Detail() string { return p.detail }

// Error renders the location and the rule text.
func (p Problem) Error() string {
	if p.shown != "" {
		return p.shown + ": " + p.detail
	}
	return p.Location() + ": " + p.detail
}

// Unwrap returns the error of a provider-owned validator, or nil.
func (p Problem) Unwrap() error { return p.cause }

// Hint returns the English advice that resolves the problem.
func (p Problem) Hint() string { return hints[p.Code] }

// Problems returns the structured problems inside err, in report order. It sees through InvalidError and
// joined errors and returns nil when err holds none.
func Problems(err error) []Problem {
	var out []Problem
	var walk func(error)
	walk = func(err error) {
		switch e := err.(type) {
		case nil:
		case Problem:
			out = append(out, e)
		case interface{ Unwrap() []error }:
			for _, inner := range e.Unwrap() {
				walk(inner)
			}
		default:
			walk(errors.Unwrap(err))
		}
	}
	walk(err)
	return out
}
