package config

import (
	"fmt"
	"slices"
	"strings"
)

// Effect is one class of operation a connection may expose and execute locally. Provider credentials
// remain an independent upper bound: a permission can restrict a stronger credential but cannot expand a
// weaker one.
type Effect string

const (
	EffectRead    Effect = "read"
	EffectCreate  Effect = "create"
	EffectUpdate  Effect = "update"
	EffectDelete  Effect = "delete"
	EffectExecute Effect = "execute"
)

// Effects returns every supported value in stable display order.
func Effects() []Effect {
	return []Effect{EffectRead, EffectCreate, EffectUpdate, EffectDelete, EffectExecute}
}

// ParseEffects reads the stable comma-separated form used internally by configuration editors. An
// empty value keeps the provider's compatibility default; "none" expresses an explicit deny-all list.
func ParseEffects(raw string) ([]Effect, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if trimmed == "none" {
		return []Effect{}, nil
	}
	parts := strings.Split(raw, ",")
	permissions := make([]Effect, 0, len(parts))
	for _, part := range parts {
		permission := Effect(strings.TrimSpace(part))
		if !validEffect(permission) {
			return nil, fmt.Errorf("unknown permission %q, supported permissions are %s", permission, permissionNames())
		}
		permissions = append(permissions, permission)
	}
	return permissions, nil
}

// FormatEffects returns the editable comma-separated form without expanding compatibility defaults.
func FormatEffects(permissions []Effect) string {
	if permissions != nil && len(permissions) == 0 {
		return "none"
	}
	values := make([]string, len(permissions))
	for i, permission := range permissions {
		values[i] = string(permission)
	}
	return strings.Join(values, ", ")
}

// SecretRole describes one provider-defined credential value without ever carrying that value.
type SecretRole struct {
	Name        string
	Description string
}

// TargetMetadata describes the non-secret target bound to a connection. Validate, when set, checks the
// form of one configured target so a malformed value fails configuration validation instead of the first
// call. ValidateSet, when set, checks the targets of one connection together once each of them passed
// Validate, so a provider can refuse a combination its connections cannot form. Neither error may quote a
// value. Kinds, when set, names every kind of target the provider accepts with the forms an entry of that
// kind takes, so an editor can offer the kinds one by one.
type TargetMetadata struct {
	Label           string
	Description     string
	Required        bool
	Multiple        bool
	Wildcard        string
	WildcardWarning string
	Kinds           []TargetKind
	Validate        func(string) error
	ValidateSet     func([]string) error
	// ValidateSingle, when set, checks the value of the single target field instead of Validate, so a
	// provider with a targets list can keep the old meaning of that field and refuse values the list
	// would read differently. It may not quote the value either.
	ValidateSingle func(string) error
}

// TargetKind is one kind of target, such as a repository or a project. Forms lists the accepted spellings
// with upper-case placeholders, for example repos/OWNER/REPO, and Description says what an entry allows.
type TargetKind struct {
	Name        string
	Description string
	Forms       []string
}

// Placeholder reports whether a segment of a form is a placeholder such as OWNER.
func Placeholder(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return segment[0] >= 'A' && segment[0] <= 'Z'
}

// FitsForm reports whether the segments of a target are spelled in the form.
func FitsForm(segments, form []string) bool {
	if len(segments) != len(form) {
		return false
	}
	for i, part := range form {
		switch {
		case Placeholder(part):
			if segments[i] == "" || segments[i] == "*" {
				return false
			}
		case segments[i] != part:
			return false
		}
	}
	return true
}

// FixesTarget reports whether the targets of a connection leave an argument that selects a target of kind
// no choice: exactly one target is of the kind, and it names one value rather than a wildcard. A provider
// without kinds takes kind "*" and has no choice left with exactly one target that is not its wildcard.
func (t TargetMetadata) FixesTarget(kind string, targets []string) bool {
	if len(t.Kinds) == 0 {
		return len(targets) == 1 && targets[0] != t.Wildcard
	}
	var own []string
	for _, target := range targets {
		if t.kindOf(target) == kind {
			own = append(own, target)
		}
	}
	return len(own) == 1 && !strings.Contains(own[0], "*")
}

// kindOf names the kind whose form a target is spelled in. A target that is a fixed form of its own, such
// as bot, belongs to that kind even where a placeholder form of another kind fits it as well.
func (t TargetMetadata) kindOf(target string) string {
	segments := strings.Split(target, "/")
	found := ""
	for _, kind := range t.Kinds {
		for _, form := range kind.Forms {
			parts := strings.Split(form, "/")
			if !FitsForm(segments, parts) {
				continue
			}
			if !slices.ContainsFunc(parts, Placeholder) {
				return kind.Name
			}
			if found == "" {
				found = kind.Name
			}
		}
	}
	return found
}

// ToolMetadata is the configuration view of one registered operation: the ID a connection's tools list
// names, a title for editors, and the effect the connection's permissions must allow as well.
//
// RequiresToolAllowList marks a tool whose effect alone must never expose it: a connection offers it only
// when its tools list names it, and a connection without a tools list never does. It keeps a high-risk tool
// out of every connection that was written for other work, whatever permissions that connection holds.
type ToolMetadata struct {
	ID                    string
	Title                 string
	Effect                Effect
	RequiresToolAllowList bool
	// Group is the ID of the provider's ToolGroup the tool belongs to; empty for a provider without Groups.
	Group string
	// LocalFiles is empty for a tool without local file access, otherwise the direction it reads or writes
	// local files in; the connection's files list of that direction bounds it.
	LocalFiles LocalFiles
}

// ToolGroup is one subject area a provider sorts its tools into for display. A group is presentation only:
// it never changes a tool ID, a permission, or a stored tools list.
type ToolGroup struct {
	ID          string
	Title       string
	Description string
}

// ToolProfile is a named starting selection of a provider's tools for setting up a new connection. It is
// no role and is never stored: an editor expands it into the permissions its tools need and the concrete
// tool IDs it lists, and only those two lists are written. A tool registered later never joins a profile or
// a saved connection on its own, because a profile names every tool it selects.
//
// Exactly one profile of a provider is Recommended. It selects reads only, unless MutationReason explains
// why its changes are safe to preselect.
type ToolProfile struct {
	ID             string
	Title          string
	Description    string
	Recommended    bool
	MutationReason string
	Tools          []string
}

// ProviderMetadata is the configuration contract of one compiled provider. SupportedEffects and Tools
// are derived from the registered operations, sorted by effect order and by ID, and never declared by hand.
// Profiles are declared by the provider and checked against its registered tools.
//
// Description is one English line on what kind of system the provider is, the same for every
// installation. Discovery publishes it beside the note a user maintains in ProviderNotes, and searches both.
//
// ValidateBaseURL, when set, checks the effective base URL of one configured service against the transport
// boundary this provider actually opens with, so a base URL its own client would refuse fails configuration
// validation instead of the first call. It runs the same logic the provider uses to open a connection, and
// its error may not quote the value.
type ProviderMetadata struct {
	ID               string
	Name             string
	Description      string
	DefaultBaseURL   string
	ValidateBaseURL  func(string) error
	DefaultEffects   []Effect
	SupportedEffects []Effect
	Tools            []ToolMetadata
	// RemovedTools lists tool IDs qatlas no longer offers. A tools list may still name one: the entry is
	// ignored with a warning instead of failing the configuration.
	RemovedTools []string
	// RenamedTools maps a former tool ID to the registered tool that replaced it. A configuration naming
	// the former ID is read as the current one; discovery, describe and invoke refuse the former ID and
	// name the current one. A former ID is never registered and never a target itself.
	RenamedTools map[string]string
	// FormerIDs lists earlier IDs of this provider, read the same way as a renamed tool.
	FormerIDs []string
	Profiles  []ToolProfile
	// Groups, when set, sorts every tool of the provider into exactly one of these groups.
	Groups []ToolGroup
	// ServiceLevel makes the group of a tool its second ID segment (<provider>.<group>.<object>.<action>).
	// It needs Groups and is invalid for a <vendor>.<product> provider.
	ServiceLevel bool
	SecretRoles  []SecretRole
	Target       TargetMetadata
	// LocalFiles states which directions of local file access at least one tool of the provider has. It is
	// derived from the registered operations and never declared by hand.
	LocalFiles LocalFilesSupport
}

// RecommendedProfile returns the profile a new connection of this provider starts with.
func (m ProviderMetadata) RecommendedProfile() (ToolProfile, bool) {
	for _, profile := range m.Profiles {
		if profile.Recommended {
			return profile, true
		}
	}
	return ToolProfile{}, false
}

// ProfileEffects returns the effects the tools of one profile need, in stable display order. A tool
// this provider did not register contributes nothing.
func (m ProviderMetadata) ProfileEffects(profile ToolProfile) []Effect {
	needed := map[Effect]bool{}
	for _, id := range profile.Tools {
		for _, tool := range m.Tools {
			if tool.ID == id {
				needed[tool.Effect] = true
			}
		}
	}
	permissions := []Effect{}
	for _, permission := range Effects() {
		if needed[permission] {
			permissions = append(permissions, permission)
		}
	}
	return permissions
}

// ProviderCatalog is the provider metadata view used by configuration and user interfaces.
type ProviderCatalog interface {
	ProviderMetadata(string) (ProviderMetadata, bool)
	ProviderMetadataAll() []ProviderMetadata
}

type emptyProviderCatalog struct{}

func (emptyProviderCatalog) ProviderMetadata(string) (ProviderMetadata, bool) {
	return ProviderMetadata{}, false
}
func (emptyProviderCatalog) ProviderMetadataAll() []ProviderMetadata { return nil }

// CurrentProviderID returns the provider that carries former as an earlier ID. A provider that is
// registered under that ID itself is not a rename.
func CurrentProviderID(catalog ProviderCatalog, former string) (string, bool) {
	for _, metadata := range catalog.ProviderMetadataAll() {
		if metadata.ID != former && contains(metadata.FormerIDs, former) {
			return metadata.ID, true
		}
	}
	return "", false
}

// CurrentToolID returns the tool that replaced old in some provider's rename table.
func CurrentToolID(catalog ProviderCatalog, old string) (string, bool) {
	for _, metadata := range catalog.ProviderMetadataAll() {
		if current, ok := metadata.RenamedTools[old]; ok {
			return current, true
		}
	}
	return "", false
}
