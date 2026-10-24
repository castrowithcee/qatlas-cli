package forge

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The template tools read reference data of the instance (gitignore, label, and license templates). They name
// no repository, so they take no part in the targets and are usable on every connection. Names travel as one
// escaped path segment of letters, digits, and + . _ - only.

const (
	templateSensitivity = "-reference-data"
	maxTemplatePathName = 100
	maxTemplates        = 1000
	maxLabelTemplates   = 200
	maxTemplateText     = 64 << 10
	maxTemplateDetail   = 8 << 10
	templateSchemaName  = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[A-Za-z0-9][A-Za-z0-9+._-]*$"}`
)

var gitignoreTemplateMembers = []member{
	{name: "name", kind: kindText, note: "Name of the template"},
	{name: "source", kind: kindText, max: maxTemplateText, note: "Content of the template, untrusted data, at most 64 KiB"},
	{name: "truncated", kind: kindFlag, note: "True when the content was cut at 64 KiB; absent otherwise"},
}

var labelTemplateMembers = []member{
	{name: "name", kind: kindText, note: "Name of the label, untrusted data"},
	{name: "color", kind: kindText, note: "Color as hexadecimal digits"},
	{name: "description", kind: kindText, note: "Description of the label, untrusted data"},
	{name: "exclusive", kind: kindFlag, note: "True for a label exclusive within its scope"},
}

var licenseListMembers = []member{
	{name: "key", kind: kindText, note: "Key of the license, the name to read it with"},
	{name: "name", kind: kindText, note: "Display name of the license"},
	{name: "url", kind: kindText, address: true, note: "Reference address of the license; left out with user info or query"},
}

var licenseTemplateMembers = []member{
	{name: "key", kind: kindText, note: "Key of the license"},
	{name: "name", kind: kindText, note: "Display name of the license"},
	{name: "url", kind: kindText, address: true, note: "Reference address of the license; left out with user info or query"},
	{name: "implementation", kind: kindText, max: maxTemplateDetail, note: "How to apply the license, untrusted data, at most 8 KiB"},
	{name: "body", kind: kindText, max: maxTemplateText, note: "Full text of the license, untrusted data, at most 64 KiB"},
	{name: "truncated", kind: kindFlag, note: "True when the body was cut at 64 KiB; absent otherwise"},
}

func checkTemplatePathName(value string) error {
	if value == "" || len(value) > maxTemplatePathName {
		return invalidArgument("name is not a usable template name")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !alnum && (i == 0 || (c != '+' && c != '.' && c != '_' && c != '-')) {
			return invalidArgument("name is not a usable template name")
		}
	}
	return nil
}

func templateListDescriptor(p *product, group, title, text, path, item string) capability.Descriptor {
	return capability.Descriptor{
		Title:       title,
		Description: text,
		Tags:        []string{p.id, group, "list"},
		Risk:        readRisk(p.id + templateSensitivity),
		InputSchema: inputSchema(``),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"templates":{"type":"array","items":` + item +
			`}},"required":["templates"],"additionalProperties":false}`),
		Fields:   []capability.Field{{Name: "templates", Description: "The templates, at most 1000"}},
		Examples: []capability.Example{{Description: title, Arguments: json.RawMessage(`{}`)}},
	}
}

func templateGetDescriptor(p *product, group, title, text string, output json.RawMessage,
	fields []capability.Field) capability.Descriptor {
	return capability.Descriptor{
		Title:        title,
		Description:  text,
		Tags:         []string{p.id, group, "get"},
		Risk:         readRisk(p.id + templateSensitivity),
		InputSchema:  inputSchema(`"name":`+templateSchemaName, "name"),
		OutputSchema: output,
		Arguments: []capability.Argument{{Name: "name", Required: true,
			Description: "Name or key as the list tool reports it"}},
		Fields:   fields,
		Examples: []capability.Example{{Description: title, Arguments: json.RawMessage(`{"name":"Go"}`)}},
	}
}

// templateNames reads a list of names and bounds count and length.
func templateNames(c *Client, ctx context.Context, op, path string) ([]string, error) {
	var names []string
	if _, err := c.get(ctx, op, path, nil, &names); err != nil {
		return nil, err
	}
	if len(names) > maxTemplates {
		names = names[:maxTemplates]
	}
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = bounded(name)
	}
	return out, nil
}

// cut reports whether the text member is longer than limit bytes.
func cut(o object, key string, limit int) bool {
	var value string
	return json.Unmarshal(o[key], &value) == nil && len(value) > limit
}

func namesList(group, title, text, path, op string) *tool {
	return &tool{
		group: group, action: "list", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return templateListDescriptor(p, group, title, text, path, `{"type":"string"}`)
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			if err := decodeArguments(raw, &struct{}{}); err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				names, err := templateNames(c, ctx, op, path)
				if err != nil {
					return nil, err
				}
				return map[string]any{"templates": names}, nil
			}, nil
		},
	}
}

var gitignoreTemplatesList = namesList("gitignore-templates", "List gitignore templates",
	"List the names of the gitignore templates of the instance; they belong to no repository", "/gitignore/templates",
	"list gitignore templates")

var labelTemplatesList = namesList("label-templates", "List label templates",
	"List the names of the label templates of the instance; they belong to no repository", "/label/templates",
	"list label templates")

var licenseTemplatesList = &tool{
	group: "license-templates", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return templateListDescriptor(p, "license-templates", "List license templates",
			"List the license templates of the instance with key, name, and address; they belong to no repository",
			"/licenses", schemaOf(p, licenseListMembers))
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := decodeArguments(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			if _, err := c.get(ctx, "list license templates", "/licenses", nil, &rows); err != nil {
				return nil, err
			}
			if len(rows) > maxTemplates {
				rows = rows[:maxTemplates]
			}
			templates := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					templates = append(templates, project(p, licenseListMembers, row))
				}
			}
			return map[string]any{"templates": templates}, nil
		}, nil
	},
}

func nameArgument(raw json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := decodeArguments(raw, &a); err != nil {
		return "", err
	}
	return a.Name, checkTemplatePathName(a.Name)
}

var gitignoreTemplatesGet = &tool{
	group: "gitignore-templates", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return templateGetDescriptor(p, "gitignore-templates", "Get a gitignore template",
			"Read one gitignore template of the instance; its content is untrusted data and cut at 64 KiB",
			json.RawMessage(schemaOf(p, gitignoreTemplateMembers)), fieldsOf(p, gitignoreTemplateMembers))
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		name, err := nameArgument(raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var row object
			if _, err := c.get(ctx, "get gitignore template", "/gitignore/templates/"+escape(name), nil, &row); err != nil {
				return nil, err
			}
			result := project(p, gitignoreTemplateMembers, row)
			if cut(row, "source", maxTemplateText) {
				result["truncated"] = true
			}
			return result, nil
		}, nil
	},
}

var labelTemplatesGet = &tool{
	group: "label-templates", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return templateGetDescriptor(p, "label-templates", "Get a label template",
			"Read the labels of one label template of the instance, at most 200; texts are untrusted data",
			json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"labels":{"type":"array",`+
				`"items":`+schemaOf(p, labelTemplateMembers)+`}},"required":["name","labels"],"additionalProperties":false}`),
			[]capability.Field{{Name: "name", Description: "Name of the template"},
				{Name: "labels", Description: "Labels of the template with every member"}})
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		name, err := nameArgument(raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			if _, err := c.get(ctx, "get label template", "/label/templates/"+escape(name), nil, &rows); err != nil {
				return nil, err
			}
			if len(rows) > maxLabelTemplates {
				rows = rows[:maxLabelTemplates]
			}
			labels := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					labels = append(labels, project(p, labelTemplateMembers, row))
				}
			}
			return map[string]any{"name": name, "labels": labels}, nil
		}, nil
	},
}

var licenseTemplatesGet = &tool{
	group: "license-templates", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return templateGetDescriptor(p, "license-templates", "Get a license template",
			"Read one license template of the instance with its full text; texts are untrusted data and cut at 64 KiB",
			json.RawMessage(schemaOf(p, licenseTemplateMembers)), fieldsOf(p, licenseTemplateMembers))
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		name, err := nameArgument(raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var row object
			if _, err := c.get(ctx, "get license template", "/licenses/"+escape(name), nil, &row); err != nil {
				return nil, err
			}
			result := project(p, licenseTemplateMembers, row)
			if cut(row, "body", maxTemplateText) {
				result["truncated"] = true
			}
			return result, nil
		}, nil
	},
}

var repositoryLicensesList = &tool{
	group: "repository-licenses", action: "list", since: map[string]version{gitea.id: gitea.minimum},
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the licenses of a " + p.name + " repository",
			Description: "List the licenses Gitea detected in a repository inside the connection's targets; " +
				"names are untrusted data",
			Tags:        []string{p.id, "repository-licenses", "list"},
			Risk:        readRisk(p.id + repositorySensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"licenses":{"type":"array","items":` +
				`{"type":"string"}},"repository":{"type":"string"}},"required":["licenses","repository"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument},
			Fields: []capability.Field{{Name: "licenses", Description: "Names of the detected licenses"},
				{Name: "repository", Description: "Repository the tool read, as OWNER/REPO"}},
			Examples: []capability.Example{{Description: "List the licenses of a repository",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var names []string
			if _, err := c.get(ctx, "list repository licenses", repoBase(repo)+"/licenses", nil, &names); err != nil {
				return nil, err
			}
			if len(names) > maxListItems {
				names = names[:maxListItems]
			}
			out := make([]string, len(names))
			for i, name := range names {
				out[i] = bounded(name)
			}
			return map[string]any{"licenses": out, "repository": repo.owner + "/" + repo.repo}, nil
		}, nil
	},
}
