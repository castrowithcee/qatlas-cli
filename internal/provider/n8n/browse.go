package n8n

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxBrowseText bounds a project or workflow name n8n sent, shown to a person choosing a target.
const maxBrowseText = 200

func browseText(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
	if len(value) > maxBrowseText {
		cut := maxBrowseText
		for !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = strings.TrimSpace(value[:cut])
	}
	return value
}

// listTargets reads one page of the instance's projects or of one project's workflows with the API key
// alone, never through the connection's targets, so a person finds an entry the targets do not name yet. The
// cursor is n8n's own. Only workflows are searchable on the server (name); the project list has no filter.
// Workflows are reachable only below a project: a workflow of an instance without the Projects feature is
// chosen by ID or URL instead.
func listTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	if len(req.Cursor) > maxCursorLength {
		return config.TargetPage{}, invalidRequest("the target cursor is not a page of this provider")
	}
	var parentID string
	switch req.Kind {
	case "project":
	case "workflow":
		kind, id, err := parseTarget(strings.TrimSpace(req.Parent))
		if err != nil || kind != "project" {
			return config.TargetPage{}, invalidRequest("the parent of a workflow must be project/PROJECT_ID")
		}
		parentID = id
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one this provider offers")
	}
	if resolved == nil {
		return config.TargetPage{}, providerError("open", "no connection was selected")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	client.scope = scope{}
	query := url.Values{"limit": {strconv.Itoa(defaultListLimit)}}
	if req.Cursor != "" {
		query.Set("cursor", req.Cursor)
	}
	if req.Kind == "project" {
		const op = "list project targets"
		var page projectsPageJSON
		if err := client.get(ctx, op, "/projects", query, &page, maxResponseBytes); err != nil {
			return config.TargetPage{}, err
		}
		entries := make([]config.TargetEntry, 0, len(page.Data))
		for _, p := range page.Data {
			if validTargetID(p.ID) {
				entries = append(entries, config.TargetEntry{Target: projectPrefix + p.ID,
					Name: browseText(p.Name), Kind: "project", HasChildren: true})
			}
		}
		return config.TargetPage{Entries: entries, Next: page.NextCursor}, nil
	}
	const op = "list workflow targets"
	query.Set("projectId", parentID)
	if req.Query != "" {
		query.Set("name", req.Query)
	}
	var page workflowsPageJSON
	if err := client.get(ctx, op, "/workflows", query, &page, maxResponseBytes); err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(page.Data))
	for _, w := range page.Data {
		if !validTargetID(w.ID) || w.IsArchived {
			continue
		}
		// The server filter is a courtesy: a row that reports other projects only is not listed here.
		if ids := w.projectIDs(); len(ids) > 0 && !contains(ids, parentID) {
			continue
		}
		entries = append(entries, config.TargetEntry{Target: workflowPrefix + w.ID, Name: browseText(w.Name),
			Kind: "workflow"})
	}
	return config.TargetPage{Entries: entries, Next: page.NextCursor, ServerSearch: req.Query != ""}, nil
}

// resolveTargetURL reads project and workflow from a URL of the editor without a request. The n8n editor
// routes (n8n-io/n8n, packages/frontend/editor-ui src/app/router.ts and
// src/features/collaboration/projects/projects.routes.ts) are /workflow/:workflowId/:nodeId? and
// /projects/:projectId/(workflows|credentials|executions|folders/...|variables|settings); no route carries
// both. Only the instance's own origin and path prefix count.
func resolveTargetURL(_ context.Context, resolved *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	origin, rawURL string) ([]config.TargetEntry, error) {
	if origin == "" && resolved != nil {
		origin = resolved.BaseURL
	}
	base, err := parseInstance(origin)
	if err != nil {
		return nil, nil
	}
	service, err := url.Parse(base)
	if err != nil {
		return nil, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, service.Host) {
		return nil, nil
	}
	rest, ok := strings.CutPrefix(parsed.EscapedPath(), service.EscapedPath())
	if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return nil, nil
	}
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	if len(segments) < 2 {
		return nil, nil
	}
	id, err := url.PathUnescape(segments[1])
	if err != nil || !validTargetID(id) {
		return nil, nil
	}
	switch segments[0] {
	case "projects":
		return []config.TargetEntry{{Target: projectPrefix + id, Name: id, Kind: "project", HasChildren: true}}, nil
	case "workflow":
		if id == "new" {
			return nil, nil
		}
		return []config.TargetEntry{{Target: workflowPrefix + id, Name: id, Kind: "workflow"}}, nil
	}
	return nil, nil
}
