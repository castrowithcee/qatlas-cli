package forge

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The version tool reads the product version the instance reports below /api/v1. It is instance metadata, not
// data of an owner or repository, so every connection offers it whatever its targets.

var versionGet = &tool{
	group: "version", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title:       "Get the " + p.name + " version",
			Description: "Read the version string the " + p.name + " instance reports",
			Tags:        []string{p.id, "instance", "version", "get"},
			Risk:        readRisk(p.id + "-instance-metadata"),
			InputSchema: inputSchema(""),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"version":{"type":"string"}},` +
				`"required":["version"],"additionalProperties":false}`),
			Fields: []capability.Field{{Name: "version", Description: "Version string as the instance reports it, " +
				"untrusted data"}},
			Examples: []capability.Example{{Description: "Read the version", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, _ allowlist, _ json.RawMessage) (run, error) {
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get version"
			var answer struct {
				Version string `json:"version"`
			}
			if _, err := c.get(ctx, op, "/version", nil, &answer); err != nil {
				return nil, err
			}
			if answer.Version == "" {
				return nil, provider.InvalidResponse(op, p.name+" returned an invalid version")
			}
			return map[string]any{"version": bounded(answer.Version)}, nil
		}, nil
	},
}

// The tools below read further instance-wide data: settings, node information, and the public signing key.
// None of it belongs to an owner or repository, so every connection offers them whatever its targets, like the
// version.

const instanceSensitivity = "-instance-metadata"

// settingsSections maps the section argument to its fixed path and the members that section documents.
var settingsSections = []struct {
	name    string
	path    string
	members []member
}{
	{"api", "/settings/api", []member{
		{name: "default_git_trees_per_page", kind: kindInteger, note: "Default number of entries of a Git tree page"},
		{name: "default_max_blob_size", kind: kindInteger, note: "Default maximum blob size in bytes"},
		{name: "default_max_response_size", kind: kindInteger, only: gitea, note: "Default maximum response size"},
		{name: "default_paging_num", kind: kindInteger, note: "Default entries per page of a list"},
		{name: "max_response_items", kind: kindInteger, note: "Largest page of a list the instance serves"},
	}},
	{"repository", "/settings/repository", []member{
		{name: "forks_disabled", kind: kindFlag, only: forgejo, note: "True when forking is disabled"},
		{name: "http_git_disabled", kind: kindFlag, note: "True when Git over HTTP is disabled"},
		{name: "lfs_disabled", kind: kindFlag, note: "True when Git LFS is disabled"},
		{name: "migrations_disabled", kind: kindFlag, note: "True when migrations are disabled"},
		{name: "mirrors_disabled", kind: kindFlag, note: "True when mirrors are disabled"},
		{name: "stars_disabled", kind: kindFlag, note: "True when starring is disabled"},
		{name: "time_tracking_disabled", kind: kindFlag, note: "True when time tracking is disabled"},
	}},
	{"attachment", "/settings/attachment", []member{
		{name: "allowed_types", kind: kindText, note: "Allowed attachment types, untrusted data"},
		{name: "enabled", kind: kindFlag, note: "True when attachments are enabled"},
		{name: "max_files", kind: kindInteger, note: "Largest number of files per attachment upload"},
		{name: "max_size", kind: kindInteger, note: "Largest size of one attachment"},
	}},
	{"ui", "/settings/ui", []member{
		{name: "allowed_reactions", kind: kindTexts, note: "Allowed reactions, untrusted data"},
		{name: "custom_emojis", kind: kindTexts, note: "Custom emojis, untrusted data"},
		{name: "default_theme", kind: kindText, note: "Default theme, untrusted data"},
	}},
}

// settingsMembers are the members of all sections, for the schema.
func settingsMembers() []member {
	var all []member
	for _, section := range settingsSections {
		all = append(all, section.members...)
	}
	return all
}

var settingsGet = &tool{
	group: "settings", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		names := make([]string, len(settingsSections))
		for i, section := range settingsSections {
			names[i] = `"` + section.name + `"`
		}
		return capability.Descriptor{
			Title: "Get " + p.name + " instance settings",
			Description: "Read one section of the settings the " + p.name + " instance exposes: api (including the " +
				"largest page of a list), repository, attachment, or ui",
			Tags:        []string{p.id, "instance", "settings", "get"},
			Risk:        readRisk(p.id + instanceSensitivity),
			InputSchema: inputSchema(`"section":{"type":"string","enum":[`+strings.Join(names, ",")+`]}`, "section"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string"},` +
				`"settings":` + schemaOf(p, settingsMembers()) + `},"required":["section","settings"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{{Name: "section", Description: "api, repository, attachment, or ui", Required: true}},
			Fields: append([]capability.Field{{Name: "section", Description: "The section that was read"},
				{Name: "settings", Description: "Every member the section documents; absent members are ones " +
					"this instance does not send"}}, fieldsOf(p, settingsMembers())...),
			Examples: []capability.Example{{Description: "Read the API settings",
				Arguments: json.RawMessage(`{"section":"api"}`)}},
		}
	},
	prepare: func(p *product, _ allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Section string `json:"section"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		for _, section := range settingsSections {
			if section.name != arguments.Section {
				continue
			}
			return func(ctx context.Context, c *Client) (any, error) {
				var answer object
				if _, err := c.get(ctx, "get settings", section.path, nil, &answer); err != nil {
					return nil, err
				}
				return map[string]any{"section": section.name, "settings": project(p, section.members, answer)}, nil
			}, nil
		}
		return nil, invalidArgument("section must be api, repository, attachment, or ui")
	},
}

var nodeInfoMembers = []member{
	{name: "version", kind: kindText, note: "NodeInfo schema version"},
	{name: "openRegistrations", kind: kindFlag, note: "True when anyone may register"},
	{name: "protocols", kind: kindTexts, note: "Federation protocols the instance names"},
	{name: "services", kind: kindObject, note: "Services the instance names", sub: []member{
		{name: "inbound", kind: kindTexts, note: "Inbound services"},
		{name: "outbound", kind: kindTexts, note: "Outbound services"},
	}},
	{name: "software", kind: kindObject, note: "The software of the instance", sub: []member{
		{name: "name", kind: kindText, note: "Software name"},
		{name: "version", kind: kindText, note: "Software version"},
		{name: "homepage", kind: kindText, address: true, note: "Address of the software's homepage"},
		{name: "repository", kind: kindText, address: true, note: "Address of the software's source"},
	}},
	{name: "usage", kind: kindObject, note: "Usage counters", sub: []member{
		{name: "localComments", kind: kindInteger, note: "Number of local comments"},
		{name: "localPosts", kind: kindInteger, note: "Number of local posts"},
		{name: "users", kind: kindObject, note: "User counters", sub: []member{
			{name: "activeHalfyear", kind: kindInteger, note: "Users active in the last half year"},
			{name: "activeMonth", kind: kindInteger, note: "Users active in the last month"},
			{name: "total", kind: kindInteger, note: "Number of users"},
		}},
	}},
	{name: "metadata", kind: kindFree, note: "Free-form instance metadata, bounded, untrusted data"},
}

var nodeInfoGet = &tool{
	group: "nodeinfo", action: "get", since: map[string]version{forgejo.id: forgejo.minimum},
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title:        "Get the " + p.name + " node information",
			Description:  "Read the NodeInfo document the " + p.name + " instance publishes about itself",
			Tags:         []string{p.id, "instance", "nodeinfo", "get"},
			Risk:         readRisk(p.id + instanceSensitivity),
			InputSchema:  inputSchema(""),
			OutputSchema: json.RawMessage(schemaOf(p, nodeInfoMembers)),
			Fields:       fieldsOf(p, nodeInfoMembers),
			Examples:     []capability.Example{{Description: "Read the node information", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, _ allowlist, _ json.RawMessage) (run, error) {
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			if _, err := c.get(ctx, "get node information", "/nodeinfo", nil, &answer); err != nil {
				return nil, err
			}
			return project(p, nodeInfoMembers, answer), nil
		}, nil
	},
}

// signingKeyFormats are the key formats per product with their fixed paths. GPG is served by both; the SSH
// public key lives at /signing-key.pub on Gitea and at /signing-key.ssh on Forgejo.
var signingKeyFormats = map[string]map[string]string{
	forgejo.id: {"gpg": "/signing-key.gpg", "ssh": "/signing-key.ssh"},
	gitea.id:   {"gpg": "/signing-key.gpg", "pub": "/signing-key.pub"},
}

// maxSigningKeyBytes bounds a public key; a longer answer is no key.
const maxSigningKeyBytes = 64 << 10

var signingKeysGet = &tool{
	group: "signing-keys", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		other := "ssh"
		if p == gitea {
			other = "pub"
		}
		return capability.Descriptor{
			Title: "Get the " + p.name + " signing key",
			Description: "Read the public key the " + p.name + " instance signs its commits with, as GPG or as " +
				"an SSH public key; this is no access credential",
			Tags:        []string{p.id, "instance", "signing-keys", "get"},
			Risk:        readRisk(p.id + instanceSensitivity),
			InputSchema: inputSchema(`"format":{"type":"string","enum":["gpg","` + other + `"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"format":{"type":"string"},` +
				`"key":{"type":"string"}},"required":["format","key"],"additionalProperties":false}`),
			Arguments: []capability.Argument{{Name: "format", Description: "gpg (default) or " + other +
				" for the SSH public key"}},
			Fields: []capability.Field{{Name: "format", Description: "The format that was read"},
				{Name: "key", Description: "The public key as the instance serves it, untrusted data"}},
			Examples: []capability.Example{{Description: "Read the GPG key", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, _ allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Format string `json:"format"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if arguments.Format == "" {
			arguments.Format = "gpg"
		}
		path, ok := signingKeyFormats[p.id][arguments.Format]
		if !ok {
			return nil, invalidArgument("format is not one " + p.name + " serves")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get signing key"
			answer := &textAnswer{limit: maxSigningKeyBytes}
			if err := c.getText(ctx, op, path, answer); err != nil {
				return nil, err
			}
			key := strings.TrimSpace(string(answer.data))
			if answer.truncated || key == "" || !utf8.ValidString(key) {
				return nil, provider.InvalidResponse(op, p.name+" returned an invalid signing key")
			}
			return map[string]any{"format": arguments.Format, "key": key}, nil
		}, nil
	},
}
