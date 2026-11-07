package forge

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

// The repository flag tools read and change the instance-defined flags of one repository inside the connection's
// targets. They exist on Forgejo only; Gitea has no flag routes. Flags are set and removed by instance
// administrators only; the instance answers 403 to every other token. A flag name is a path segment and, in
// replace, a list member, so it is checked before any request: 1 to maxFlagLength ASCII letters, digits, "_", "-",
// and ".", and not "." or "..". This is a deliberately narrow reading; the swagger names no format.

const maxFlagLength = 100

const flagAdminNote = "Only an instance administrator can set or remove flags; the instance answers 403 to any " +
	"other token. "

var flagArgument = capability.Argument{Name: "flag", Required: true,
	Description: "Flag name: 1 to 100 ASCII letters, digits, '_', '-', or '.', not '.' or '..'"}

var flagsArgument = capability.Argument{Name: "flags", Required: true,
	Description: "Complete new list of flag names; an empty list removes all flags"}

const flagSchema = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[A-Za-z0-9_.-]+$"}`

func validFlag(value string) bool {
	if value == "" || len(value) > maxFlagLength || value == "." || value == ".." {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' ||
			c == '.') {
			return false
		}
	}
	return true
}

func flagsPath(repo target) string { return repositoryPath(repo) + "/flags" }

func flagPath(repo target, flag string) string { return flagsPath(repo) + "/" + escape(flag) }

func flagMessage() string {
	return "flag must be 1 to 100 letters, digits, '_', '-', or '.' and not '.' or '..'"
}

var flagRepositoryProperty = `"repository":` + repoSchema

// flagOutput is the output schema with the repository and the given extra members, all required.
func flagOutput(extra string, required ...string) json.RawMessage {
	names := `"repository"`
	for _, name := range required {
		names += `,"` + name + `"`
	}
	return json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"}` + extra +
		`},"required":[` + names + `],"additionalProperties":false}`)
}

type flagArgs struct {
	Repository string          `json:"repository"`
	Flag       string          `json:"flag"`
	Flags      json.RawMessage `json:"flags"`
}

// flagTarget reads the arguments and binds the repository before anything else is looked at.
func flagTarget(allowed allowlist, raw json.RawMessage, withFlag bool) (flagArgs, target, error) {
	var a flagArgs
	if err := decodeArguments(raw, &a); err != nil {
		return a, target{}, err
	}
	repo, err := allowed.chooseRepository(a.Repository)
	if err != nil {
		return a, target{}, err
	}
	if withFlag && !validFlag(a.Flag) {
		return a, target{}, invalidArgument(flagMessage())
	}
	if !withFlag && a.Flag != "" {
		return a, target{}, invalidArgument("an argument is not offered by this tool")
	}
	return a, repo, nil
}

var repositoryFlagsList = &tool{
	group: "repository-flags", action: "list", since: forgejoOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the flags of a " + p.name + " repository",
			Description: "List all flags of a repository inside the connection's targets as the instance reports " +
				"them, at most 100; the instance does not page this list. Flag names are untrusted data.",
			Tags:        []string{p.id, "repository-flags", "list"},
			Risk:        readRisk(p.id + accessSensitivity),
			InputSchema: inputSchema(flagRepositoryProperty),
			OutputSchema: flagOutput(`,"flags":{"type":"array","items":{"type":"string"}},`+
				`"truncated":{"type":"boolean"}`, "flags"),
			Arguments: []capability.Argument{repositoryArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"},
				{Name: "flags", Description: "Flag names"},
				{Name: "truncated", Description: "True when the instance sent more flags than the tool returns"}},
			Examples: []capability.Example{{Description: "List the flags",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		_, repo, err := flagTarget(allowed, raw, false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []string
			if _, err := c.get(ctx, "list repository flags", flagsPath(repo), nil, &rows); err != nil {
				return nil, err
			}
			result := map[string]any{"repository": fullName(repo)}
			if len(rows) > maxListItems {
				rows, result["truncated"] = rows[:maxListItems], true
			}
			if rows == nil {
				rows = []string{}
			}
			result["flags"] = rows
			return result, nil
		}, nil
	},
}

var repositoryFlagsCheck = &tool{
	group: "repository-flags", action: "check", since: forgejoOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Check whether a " + p.name + " repository has a flag",
			Description: "Ask whether a repository inside the connection's targets has one flag. The answer is a " +
				"boolean; a 404 is reported as not set, although the instance also answers it for a repository the " +
				"token does not reach.",
			Tags:         []string{p.id, "repository-flags", "check"},
			Risk:         readRisk(p.id + accessSensitivity),
			InputSchema:  inputSchema(flagRepositoryProperty+`,"flag":`+flagSchema, "flag"),
			OutputSchema: flagOutput(`,"flag":{"type":"string"},"has_flag":{"type":"boolean"}`, "flag", "has_flag"),
			Arguments:    []capability.Argument{repositoryArgument, flagArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository that was checked as OWNER/REPO"},
				{Name: "flag", Description: "Flag name that was checked"},
				{Name: "has_flag", Description: "True when the instance answered 204; false when it answered 404"}},
			Examples: []capability.Example{{Description: "Check a flag",
				Arguments: json.RawMessage(`{"repository":"example/project","flag":"archived"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, repo, err := flagTarget(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			ok, err := c.probe(ctx, "check repository flag", flagPath(repo, a.Flag))
			if err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "flag": a.Flag, "has_flag": ok}, nil
		}, nil
	},
}

// flagChange builds the three changes that name no list: add and remove of one flag, and clear of all.
func flagChange(action, key, method string, one, listedOnly bool, title, text string) *tool {
	effect := capability.EffectUpdate
	if action == "clear" {
		effect = capability.EffectDelete
	}
	return &tool{
		group: "repository-flags", action: action, since: forgejoOnly,
		describe: func(p *product) capability.Descriptor {
			properties, required := flagRepositoryProperty, []string{}
			arguments := []capability.Argument{repositoryArgument}
			example := `{"repository":"example/project"}`
			fields := []capability.Field{{Name: "repository", Description: "Repository the tool acted on as OWNER/REPO"}}
			extra, names := "", []string{key}
			if one {
				properties += `,"flag":` + flagSchema
				required = append(required, "flag")
				arguments = append(arguments, flagArgument)
				example = `{"repository":"example/project","flag":"archived"}`
				fields = append(fields, capability.Field{Name: "flag", Description: "Flag name the tool acted on"})
				extra, names = `,"flag":{"type":"string"}`, []string{"flag", key}
			}
			fields = append(fields, capability.Field{Name: key, Description: "True once the instance accepted the request"})
			return capability.Descriptor{
				Title: titleFor(title, p),
				Description: flagAdminNote + text + " The request is sent once and never repeated after an " +
					"unclear outcome.",
				Tags:                  []string{p.id, "repository-flags", action},
				Risk:                  changeRisk(effect, capability.IdempotencyIdempotent, p.id+accessSensitivity),
				RequiresToolAllowList: listedOnly,
				InputSchema:           inputSchema(properties, required...),
				OutputSchema:          flagOutput(extra+`,"`+key+`":{"type":"boolean"}`, names...),
				Arguments:             arguments,
				Fields:                fields,
				Examples: []capability.Example{{Description: titleFor(title, p),
					Arguments: json.RawMessage(example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			a, repo, err := flagTarget(allowed, raw, one)
			if err != nil {
				return nil, err
			}
			if a.Flags != nil {
				return nil, invalidArgument("an argument is not offered by this tool")
			}
			path := flagsPath(repo)
			if one {
				path = flagPath(repo, a.Flag)
			}
			return func(ctx context.Context, c *Client) (any, error) {
				if err := c.change(ctx, action+" repository flag", method, path, nil, nil); err != nil {
					return nil, err
				}
				result := map[string]any{"repository": fullName(repo), key: true}
				if one {
					result["flag"] = a.Flag
				}
				return result, nil
			}, nil
		},
	}
}

var repositoryFlagsAdd = flagChange("add", "added", http.MethodPut, true, false,
	"Add a flag to a %s repository",
	"Set one flag on a repository inside the connection's targets; a flag that is set stays set.")

var repositoryFlagsRemove = flagChange("remove", "removed", http.MethodDelete, true, false,
	"Remove a flag from a %s repository",
	"Remove one flag from a repository inside the connection's targets; the other flags stay.")

var repositoryFlagsClear = flagChange("clear", "cleared", http.MethodDelete, false, true,
	"Remove all flags from a %s repository",
	"Remove every flag from a repository inside the connection's targets. Only a connection whose tools list "+
		"names this tool offers it.")

var repositoryFlagsReplace = &tool{
	group: "repository-flags", action: "replace", since: forgejoOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Replace all flags of a " + p.name + " repository",
			Description: flagAdminNote + "Replace the complete flag list of a repository inside the connection's " +
				"targets with the given list of at most 100 flags; flags that are left out are removed, and an " +
				"empty list removes all. The request is sent once and never repeated after an unclear outcome. " +
				"Only a connection whose tools list names this tool offers it.",
			Tags:                  []string{p.id, "repository-flags", "replace"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+accessSensitivity),
			RequiresToolAllowList: true,
			InputSchema: inputSchema(flagRepositoryProperty+`,"flags":{"type":"array","maxItems":100,"items":`+
				flagSchema+`}`, "flags"),
			OutputSchema: flagOutput(`,"flags":{"type":"array","items":{"type":"string"}},"replaced":{"type":"boolean"}`,
				"flags", "replaced"),
			Arguments: []capability.Argument{repositoryArgument, flagsArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository the tool acted on as OWNER/REPO"},
				{Name: "flags", Description: "Flag list that was sent"},
				{Name: "replaced", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Replace the flags",
				Arguments: json.RawMessage(`{"repository":"example/project","flags":["archived"]}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, repo, err := flagTarget(allowed, raw, false)
		if err != nil {
			return nil, err
		}
		var flags []string
		if len(a.Flags) == 0 || json.Unmarshal(a.Flags, &flags) != nil || flags == nil || len(flags) > maxListItems {
			return nil, invalidArgument("flags must be a list of at most 100 flag names")
		}
		for _, flag := range flags {
			if !validFlag(flag) {
				return nil, invalidArgument(flagMessage())
			}
		}
		body := map[string][]string{"flags": flags}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "replace repository flags", http.MethodPut, flagsPath(repo), body, nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "flags": flags, "replaced": true}, nil
		}, nil
	},
}
