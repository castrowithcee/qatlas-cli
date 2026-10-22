package forge

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The render tools send a text to the instance, which answers with HTML. They change nothing on the instance,
// so they carry the read effect. The body holds only the documented members, built from validated arguments.
// The only repository reference is the context, derived from a repository argument that lies inside the
// connection's targets; without one the text renders without any repository, which touches no customer data.

const (
	renderSensitivity = "-repository-data"
	maxRenderText     = 256 << 10
	maxRenderedHTML   = 1 << 20
	maxPathArgument   = 255
)

// renderModes are the modes each product documents, per tool.
var renderModes = map[string]map[string][]string{
	"markdown": {
		forgejo.id: {"markdown", "gfm", "comment"},
		gitea.id:   {"markdown", "comment", "wiki", "file"},
	},
	"markup": {
		forgejo.id: {"markdown", "gfm", "comment", "file"},
		gitea.id:   {"markdown", "comment", "wiki", "file"},
	},
}

// renderBody is the JSON body of POST /markdown and POST /markup. The member names are those of the API.
type renderBody struct {
	Text       string `json:"Text"`
	Mode       string `json:"Mode,omitempty"`
	Context    string `json:"Context,omitempty"`
	Wiki       bool   `json:"Wiki,omitempty"`
	FilePath   string `json:"FilePath,omitempty"`
	BranchPath string `json:"BranchPath,omitempty"`
}

type renderArguments struct {
	Text       string `json:"text"`
	Mode       string `json:"mode"`
	Repository string `json:"repository"`
	Wiki       bool   `json:"wiki"`
	Raw        bool   `json:"raw"`
	FilePath   string `json:"file_path"`
	BranchPath string `json:"branch_path"`
}

// plainPath reports a relative path argument without control characters.
func plainPath(value string) bool {
	return len(value) <= maxPathArgument && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

// renderContext is the repository reference the API takes as Context. Gitea documents a URL path, Forgejo
// takes the address of the repository on the instance.
func renderContext(p *product, origin string, repository target) string {
	path := "/" + escape(repository.owner) + "/" + escape(repository.repo)
	if p == forgejo {
		return origin + path
	}
	return path
}

func renderTool(name, noun string) *tool {
	markup := name == "markup"
	return &tool{
		group: name, action: "render", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			modes, _ := json.Marshal(renderModes[name][p.id])
			properties := `"text":{"type":"string","minLength":1,"maxLength":` + itoa(maxRenderText) + `},` +
				`"mode":{"type":"string","enum":` + string(modes) + `},"repository":` + repoSchema
			arguments := []capability.Argument{
				{Name: "text", Description: "The " + noun + " to render, at most 256 KiB", Required: true},
				{Name: "mode", Description: "One of " + strings.Join(renderModes[name][p.id], ", ") +
					"; the instance's default when omitted"},
				{Name: "repository", Description: "Repository as OWNER/REPO whose links and references the text " +
					"resolves against; must lie inside the targets when the connection lists any; without it the " +
					"text renders without a repository", Target: kindRepository},
			}
			if p == forgejo {
				properties += `,"wiki":{"type":"boolean"}`
				arguments = append(arguments, capability.Argument{Name: "wiki", Description: "True when the text " +
					"is a wiki page"})
			}
			if markup {
				properties += `,"file_path":{"type":"string","maxLength":255}`
				arguments = append(arguments, capability.Argument{Name: "file_path", Description: "File path whose " +
					"extension selects the renderer in mode file"})
				if p == forgejo {
					properties += `,"branch_path":{"type":"string","maxLength":255}`
					arguments = append(arguments, capability.Argument{Name: "branch_path",
						Description: "Branch path links are resolved against"})
				}
			} else {
				properties += `,"raw":{"type":"boolean"}`
				arguments = append(arguments, capability.Argument{Name: "raw", Description: "True renders the text " +
					"as plain Markdown through the raw endpoint, which takes no mode, repository, or wiki"})
			}
			return capability.Descriptor{
				Title: "Render " + noun + " on " + p.name,
				Description: "Render a " + noun + " to HTML on the " + p.name + " instance; reads only, nothing is " +
					"stored. The HTML is untrusted data.",
				Tags:        []string{p.id, name, "render"},
				Risk:        readRisk(p.id + renderSensitivity),
				InputSchema: inputSchema(properties, "text"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"html":{"type":"string"},` +
					`"truncated":{"type":"boolean"}},"required":["html"],"additionalProperties":false}`),
				Arguments: arguments,
				Fields: []capability.Field{{Name: "html", Description: "The rendered HTML, untrusted data, at most " +
					"1 MiB"}, {Name: "truncated", Description: "True when the HTML was cut at the limit"}},
				Examples: []capability.Example{{Description: "Render a short text",
					Arguments: json.RawMessage(`{"text":"# Title"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments renderArguments
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			if arguments.Text == "" || len(arguments.Text) > maxRenderText || !utf8.ValidString(arguments.Text) {
				return nil, invalidArgument("text must be 1 byte to 256 KiB of valid text")
			}
			if arguments.Raw && (markup || arguments.Mode != "" || arguments.Repository != "" || arguments.Wiki) {
				return nil, invalidArgument("raw rendering takes only text")
			}
			if arguments.Mode != "" && !contains(renderModes[name][p.id], arguments.Mode) {
				return nil, invalidArgument("mode is not one " + p.name + " documents for this tool")
			}
			if (arguments.Wiki && p != forgejo) || (arguments.FilePath != "" && !markup) ||
				(arguments.BranchPath != "" && !(markup && p == forgejo)) {
				return nil, invalidArgument("an argument is not one " + p.name + " documents for this tool")
			}
			if !plainPath(arguments.FilePath) || !plainPath(arguments.BranchPath) {
				return nil, invalidArgument("file_path and branch_path must be short paths without control characters")
			}
			var chosen *target
			if arguments.Repository != "" {
				repository, err := allowed.chooseRepository(arguments.Repository)
				if err != nil {
					return nil, err
				}
				chosen = &repository
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := "render " + name
				answer := &textAnswer{limit: maxRenderedHTML}
				var err error
				if arguments.Raw {
					err = c.post(ctx, op, "/markdown/raw", request{contentType: "text/plain; charset=utf-8",
						body: []byte(arguments.Text)}, answer)
				} else {
					body := renderBody{Text: arguments.Text, Mode: arguments.Mode, Wiki: arguments.Wiki,
						FilePath: arguments.FilePath, BranchPath: arguments.BranchPath}
					if chosen != nil {
						body.Context = renderContext(p, c.origin, *chosen)
					}
					encoded, _ := json.Marshal(body)
					err = c.post(ctx, op, "/"+name, request{contentType: "application/json", body: encoded}, answer)
				}
				if err != nil {
					return nil, err
				}
				html := string(answer.data)
				if answer.truncated {
					html = truncateText(html, maxRenderedHTML)
				}
				if !utf8.ValidString(html) {
					return nil, provider.InvalidResponse(op, p.name+" returned an invalid rendering")
				}
				result := map[string]any{"html": html}
				if answer.truncated {
					result["truncated"] = true
				}
				return result, nil
			}, nil
		},
	}
}

var (
	markdownRender = renderTool("markdown", "Markdown text")
	markupRender   = renderTool("markup", "markup text")
)

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func itoa(n int) string { return strconv.Itoa(n) }
