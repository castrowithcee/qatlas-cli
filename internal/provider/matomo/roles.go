package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxRoleEntries = 200

var roleSections = []string{"roles", "capabilities"}

var rolesList = capability.Descriptor{
	ID: Provider + ".roles.list", Version: 1, Title: "List Matomo roles and capabilities",
	Description: "List the roles (view, write, admin) with UsersManager.getAvailableRoles and the capabilities " +
		"with getAvailableCapabilities, which plugins extend. Both need an admin right on some website, so the " +
		"tool is in no setup profile. Names and descriptions are translated by Matomo and untrusted",
	Tags: []string{"matomo", "users", "roles", "capabilities", "list"}, Risk: readRisk(instanceSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string","enum":[` +
		quotedList(roleSections) + `]}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"roles":{"type":"array","items":{"type":"object",` +
		`"properties":{"id":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},` +
		`"help_url":{"type":"string"}},"required":["id"],"additionalProperties":false}},` +
		`"capabilities":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"name":{"type":"string"},"description":{"type":"string"},"help_url":{"type":"string"},` +
		`"included_in_roles":{"type":"array","items":{"type":"string"}},"category":{"type":"string"}},` +
		`"required":["id"],"additionalProperties":false}}},"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "section", Description: "Which list to read: " +
		strings.Join(roleSections, ", ") + "; both when omitted"}},
	Fields: []capability.Field{
		{Name: "roles", Description: "Roles a user can be given on a website, at most 200; superuser and noaccess are not among them"},
		{Name: "roles[].id", Description: "Role ID used by the other tools, such as view"},
		{Name: "roles[].name", Description: "Display name, untrusted"},
		{Name: "roles[].description", Description: "Description, untrusted"},
		{Name: "roles[].help_url", Description: "Help address, untrusted text that is never fetched"},
		{Name: "capabilities", Description: "Capabilities a user can be given beyond the role, at most 200"},
		{Name: "capabilities[].id", Description: "Capability ID used by the other tools"},
		{Name: "capabilities[].name", Description: "Display name, untrusted"},
		{Name: "capabilities[].description", Description: "Description, untrusted"},
		{Name: "capabilities[].help_url", Description: "Help address, untrusted text that is never fetched"},
		{Name: "capabilities[].included_in_roles", Description: "Roles that include the capability without it being assigned"},
		{Name: "capabilities[].category", Description: "Category label, untrusted"},
	},
	Examples: []capability.Example{
		{Description: "List the roles and capabilities", Arguments: json.RawMessage(`{}`)},
		{Description: "List only the roles", Arguments: json.RawMessage(`{"section":"roles"}`)},
	},
}

// Role is a role a user can hold on a website.
type Role struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	HelpURL     string `json:"help_url,omitempty"`
}

// CapabilityInfo is a capability a user can be given.
type CapabilityInfo struct {
	ID              string   `json:"id"`
	Name            string   `json:"name,omitempty"`
	Description     string   `json:"description,omitempty"`
	HelpURL         string   `json:"help_url,omitempty"`
	IncludedInRoles []string `json:"included_in_roles,omitempty"`
	Category        string   `json:"category,omitempty"`
}

// RolesListResult is the answer of roles.list.
type RolesListResult struct {
	Roles        []Role           `json:"roles,omitempty"`
	Capabilities []CapabilityInfo `json:"capabilities,omitempty"`
}

func invokeRolesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list roles"
	var args struct {
		Section *string `json:"section"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	section := ""
	if args.Section != nil {
		if section = *args.Section; section != "roles" && section != "capabilities" {
			return nil, invalidArgument("section must be one of " + strings.Join(roleSections, ", "))
		}
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(rolesList.ID)
	result := &RolesListResult{}
	if section != "capabilities" {
		var rows []struct {
			ID          *loose `json:"id"`
			Name        *loose `json:"name"`
			Description *loose `json:"description"`
			HelpURL     *loose `json:"helpUrl"`
		}
		if err := client.call(ctx, op, req, "UsersManager.getAvailableRoles", url.Values{}, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			if id := row.ID.str(maxStringLength); id != "" && len(result.Roles) < maxRoleEntries {
				result.Roles = append(result.Roles, Role{ID: id, Name: row.Name.str(maxStringLength),
					Description: row.Description.str(maxSettingsText), HelpURL: row.HelpURL.str(maxUrlLength)})
			}
		}
	}
	if section != "roles" {
		var rows []struct {
			ID              *loose  `json:"id"`
			Name            *loose  `json:"name"`
			Description     *loose  `json:"description"`
			HelpURL         *loose  `json:"helpUrl"`
			IncludedInRoles []loose `json:"includedInRoles"`
			Category        *loose  `json:"category"`
		}
		if err := client.call(ctx, op, req, "UsersManager.getAvailableCapabilities", url.Values{}, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			id := row.ID.str(maxStringLength)
			if id == "" || len(result.Capabilities) >= maxRoleEntries {
				continue
			}
			entry := CapabilityInfo{ID: id, Name: row.Name.str(maxStringLength), Description: row.Description.str(maxSettingsText),
				HelpURL: row.HelpURL.str(maxUrlLength), Category: row.Category.str(maxStringLength)}
			for i := range row.IncludedInRoles {
				if text := row.IncludedInRoles[i].str(maxStringLength); text != "" && len(entry.IncludedInRoles) < maxRoleEntries {
					entry.IncludedInRoles = append(entry.IncludedInRoles, text)
				}
			}
			result.Capabilities = append(result.Capabilities, entry)
		}
	}
	return result, nil
}
