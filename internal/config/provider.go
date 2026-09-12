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

// ParsePermissions reads the comma-separated form used by the TUI. An empty value keeps the provider's
// compatibility default; "none" expresses an explicit deny-all list in the TUI.
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

// TargetMetadata describes the non-secret target bound to a connection.
type TargetMetadata struct {
	Label       string
	Description string
	Required    bool
}

// ProviderMetadata is the configuration contract of one compiled provider.
type ProviderMetadata struct {
	ID                 string
	Name               string
	DefaultBaseURL     string
	DefaultPermissions []Permission
	SecretRoles        []SecretRole
	Target             TargetMetadata
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
