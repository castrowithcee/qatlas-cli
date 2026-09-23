package config

import (
	"fmt"
	"strings"
)

// Permission is one class of operation a connection may expose and execute locally. Provider credentials
// remain an independent upper bound: a permission can restrict a stronger credential but cannot expand a
// weaker one.
type Permission string

const (
	PermissionRead    Permission = "read"
	PermissionCreate  Permission = "create"
	PermissionUpdate  Permission = "update"
	PermissionDelete  Permission = "delete"
	PermissionExecute Permission = "execute"
)

// Permissions returns every supported value in stable display order.
func Permissions() []Permission {
	return []Permission{PermissionRead, PermissionCreate, PermissionUpdate, PermissionDelete, PermissionExecute}
}

// ParsePermissions reads the stable comma-separated form used internally by configuration editors. An
// empty value keeps the provider's compatibility default; "none" expresses an explicit deny-all list.
func ParsePermissions(raw string) ([]Permission, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if trimmed == "none" {
		return []Permission{}, nil
	}
	parts := strings.Split(raw, ",")
	permissions := make([]Permission, 0, len(parts))
	for _, part := range parts {
		permission := Permission(strings.TrimSpace(part))
		if !validPermission(permission) {
			return nil, fmt.Errorf("unknown permission %q, supported permissions are %s", permission, permissionNames())
		}
		permissions = append(permissions, permission)
	}
	return permissions, nil
}

// FormatPermissions returns the editable comma-separated form without expanding compatibility defaults.
func FormatPermissions(permissions []Permission) string {
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
// value.
type TargetMetadata struct {
	Label           string
	Description     string
	Required        bool
	Multiple        bool
	Wildcard        string
	WildcardWarning string
	Validate        func(string) error
	ValidateSet     func([]string) error
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
	Effect                Permission
	RequiresToolAllowList bool
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

// ProviderMetadata is the configuration contract of one compiled provider. SupportedPermissions and Tools
// are derived from the registered operations, sorted by effect order and by ID, and never declared by hand.
// Profiles are declared by the provider and checked against its registered tools.
type ProviderMetadata struct {
	ID                   string
	Name                 string
	DefaultBaseURL       string
	DefaultPermissions   []Permission
	SupportedPermissions []Permission
	Tools                []ToolMetadata
	Profiles             []ToolProfile
	SecretRoles          []SecretRole
	Target               TargetMetadata
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

// ProfilePermissions returns the effects the tools of one profile need, in stable display order. A tool
// this provider did not register contributes nothing.
func (m ProviderMetadata) ProfilePermissions(profile ToolProfile) []Permission {
	needed := map[Permission]bool{}
	for _, id := range profile.Tools {
		for _, tool := range m.Tools {
			if tool.ID == id {
				needed[tool.Effect] = true
			}
		}
	}
	permissions := []Permission{}
	for _, permission := range Permissions() {
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
