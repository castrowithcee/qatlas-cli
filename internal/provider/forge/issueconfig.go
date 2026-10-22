package forge

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The issue template and configuration tools read what a repository's files define. Titles, bodies, form
// attributes, and links come from files of the repository and are untrusted data.

var issueFormFieldMembers = []member{
	{name: "id", kind: kindText, note: "Field ID"},
	{name: "type", kind: kindText, note: "markdown, textarea, input, dropdown, or checkboxes"},
	{name: "attributes", kind: kindFree, note: "Free-form field attributes, bounded, untrusted data"},
	{name: "validations", kind: kindFree, note: "Free-form field validations, bounded, untrusted data"},
	{name: "visible", kind: kindTexts, note: "Where the field shows: form, content"},
}

var issueTemplateMembers = []member{
	{name: "name", kind: kindText, note: "Template name, untrusted data"},
	{name: "title", kind: kindText, note: "Default issue title, untrusted data"},
	{name: "about", kind: kindText, note: "Description, untrusted data"},
	{name: "file_name", kind: kindText, note: "File of the template in the repository"},
	{name: "content", kind: kindText, note: "Body of a Markdown template, untrusted data"},
	{name: "ref", kind: kindText, note: "Reference the template is read from"},
	{name: "labels", kind: kindTexts, note: "Labels the template sets"},
	{name: "assignees", kind: kindTexts, only: gitea, note: "Assignees the template sets"},
	{name: "body", kind: kindObjects, sub: issueFormFieldMembers, note: "Fields of a form template"},
}

var issueConfigMembers = []member{
	{name: "blank_issues_enabled", kind: kindFlag, note: "True while an issue may be opened without a template"},
	{name: "contact_links", kind: kindObjects, note: "Contact links, untrusted data", sub: []member{
		{name: "name", kind: kindText, note: "Link name"},
		{name: "about", kind: kindText, note: "Link description"},
		{name: "url", kind: kindText, address: true, note: "Link address; left out when it carries user info or a query"},
	}},
}

var issueConfigValidationMembers = []member{
	{name: "valid", kind: kindFlag, note: "True when the configuration file is valid"},
	{name: "message", kind: kindText, note: "Validation message of the instance, untrusted data"},
}

const issueConfigSensitivity = "-repository-data"

const repositoryArgument = "Repository as OWNER/REPO; optional when the connection's targets name exactly one " +
	"repository; must lie inside the targets when the connection lists any"

// issueConfigTool builds a read of one fixed path below a repository whose answer is one object or a list.
func issueConfigTool(action, title, description, endpoint string, members []member, key string, required ...string) *tool {
	return &tool{
		group: "issue-" + map[bool]string{true: "templates", false: "config"}[key != ""], action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			output := schemaOf(p, members, required...)
			fields := fieldsOf(p, members)
			if key != "" {
				output = `{"type":"object","properties":{"` + key + `":{"type":"array","items":` + output +
					`},"truncated":{"type":"boolean"}},"required":["` + key + `"],"additionalProperties":false}`
				fields = []capability.Field{{Name: key, Description: "Entries with every documented member"},
					{Name: "truncated", Description: "True when the instance sent more entries than the tool returns"}}
			}
			return capability.Descriptor{
				Title: title + " of a " + p.name + " repository", Description: description,
				Tags: []string{p.id, "issues", action},
				Risk: readRisk(p.id + issueConfigSensitivity), InputSchema: inputSchema(`"repository":` + repoSchema),
				OutputSchema: json.RawMessage(output),
				Arguments: []capability.Argument{{Name: "repository", Description: repositoryArgument,
					Target: kindRepository}},
				Fields: fields,
				Examples: []capability.Example{{Description: title,
					Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments struct {
				Repository string `json:"repository"`
			}
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			chosen, err := allowed.chooseRepository(arguments.Repository)
			if err != nil {
				return nil, err
			}
			path := "/repos/" + escape(chosen.owner) + "/" + escape(chosen.repo) + endpoint
			return func(ctx context.Context, c *Client) (any, error) {
				op := "read " + title
				if key == "" {
					var answer object
					if _, err := c.get(ctx, op, path, nil, &answer); err != nil {
						return nil, err
					}
					return project(p, members, answer), nil
				}
				var rows []object
				if _, err := c.get(ctx, op, path, nil, &rows); err != nil {
					return nil, err
				}
				result := map[string]any{}
				if len(rows) > maxListItems {
					rows, result["truncated"] = rows[:maxListItems], true
				}
				entries := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					if row != nil {
						entries = append(entries, project(p, members, row))
					}
				}
				result[key] = entries
				return result, nil
			}, nil
		},
	}
}

var (
	issueTemplatesList = issueConfigTool("list", "List issue templates",
		"List the issue templates and issue forms a repository defines, with every member of each template",
		"/issue_templates", issueTemplateMembers, "templates")
	issueConfigGet = issueConfigTool("get", "Read issue configuration",
		"Read the issue configuration of a repository: whether blank issues are enabled and its contact links",
		"/issue_config", issueConfigMembers, "")
	issueConfigValidate = issueConfigTool("validate", "Validate issue configuration",
		"Ask the instance whether the issue configuration file of a repository is valid; changes nothing",
		"/issue_config/validate", issueConfigValidationMembers, "", "valid")
)
