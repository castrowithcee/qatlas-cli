package todoist

import (
	"context"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Choosing targets reads the projects and sections the token reaches, or resolves a URL of the web
// interface. It only reads, never through the connection's targets, so a person finds an entry the targets
// do not name yet.

const (
	browsePageSize = 50
	maxBrowseText  = 200
	// maxTagText bounds an identifier, a unit, or a language tag; maxOrderKey bounds an order key.
	maxTagText  = 64
	maxOrderKey = 128
	// maxChainDepth bounds the parent chain shown as the path of a project.
	maxChainDepth = 20
	webHost       = "app.todoist.com"
)

// browseText bounds a name Todoist sent.
func browseText(value string) string { return boundedText(value, maxBrowseText) }

// boundedText bounds a text Todoist sent: control characters become spaces and the text is cut to at most
// limit bytes on a rune boundary.
func boundedText(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
	if len(value) > limit {
		cut := limit
		for !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = strings.TrimSpace(value[:cut])
	}
	return value
}

// browseClient opens the token of the connection without its targets, which may not exist yet.
func browseClient(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	return build(ctx, resolved, secrets, red, nil, scope{})
}

// browseParent reads the project a listing sits under: PROJECT_ID or project/PROJECT_ID, never a section.
func browseParent(value string) (string, error) {
	id, prefix, err := parseTarget(strings.TrimSpace(value))
	if err != nil || prefix == sectionPrefix {
		return "", invalidRequest("the parent must be a project: PROJECT_ID or project/PROJECT_ID")
	}
	return id, nil
}

func browseList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	if err := checkSearch(req.Query); err != nil {
		return config.TargetPage{}, err
	}
	var parent string
	switch req.Kind {
	case "project":
		if req.Parent != "" {
			var err error
			if parent, err = browseParent(req.Parent); err != nil {
				return config.TargetPage{}, err
			}
		}
	case "section":
		var err error
		if parent, err = browseParent(req.Parent); err != nil {
			return config.TargetPage{}, err
		}
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one of this provider")
	}
	binding := fingerprint("browse", req.Kind, parent, req.Query)
	_, after, err := decodeCursor(binding, req.Cursor, browsePageSize)
	if err != nil {
		return config.TargetPage{}, err
	}
	client, err := browseClient(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	if req.Kind == "section" {
		return client.browseSections(ctx, parent, req.Query, binding, after)
	}
	return client.browseProjects(ctx, parent, req.Query, binding, after)
}

func browseNext(binding []byte, next string) string {
	if next == "" {
		return ""
	}
	return encodeCursor(binding, browsePageSize, next)
}

// browseProjects reads one page of the active projects. Without a search it keeps the projects directly
// below the parent, or the top-level ones; a search runs at Todoist over every depth, narrowed to the
// subprojects of the parent when there is one. Todoist has no parent filter, so a page may hold few or no
// matches and still announce a next page.
func (c *Client) browseProjects(ctx context.Context, parent, query string, binding []byte,
	after string) (config.TargetPage, error) {
	const op = "list project targets"
	path, values := searchable("/projects", query)
	raws, next, err := c.browsePage(ctx, op, path, values, after, browsePageSize)
	if err != nil {
		return config.TargetPage{}, err
	}
	var kept []rawProject
	for _, raw := range raws {
		if !validID(raw.ID) || raw.IsDeleted || raw.archived() {
			continue
		}
		if (parent != "" || query == "") && text(raw.ParentID) != parent {
			continue
		}
		kept = append(kept, raw)
	}
	page := config.TargetPage{Entries: []config.TargetEntry{}, Next: browseNext(binding, next),
		ServerSearch: query != ""}
	var names map[string]rawProject
	for _, raw := range kept {
		path := ""
		if text(raw.ParentID) != "" {
			if names == nil {
				if names, err = c.browseNames(ctx, op); err != nil {
					return config.TargetPage{}, err
				}
			}
			path = projectChain(names, text(raw.ParentID))
		}
		name := browseText(raw.Name)
		if name == "" {
			name = raw.ID
		}
		page.Entries = append(page.Entries, config.TargetEntry{Target: treePrefix + raw.ID, Name: name,
			Kind: "project", Path: path, HasChildren: true})
	}
	return page, nil
}

// browseNames reads every active project once, up to maxScanPages, to name the parent chains of a page. A
// longer list only shortens the chains.
func (c *Client) browseNames(ctx context.Context, op string) (map[string]rawProject, error) {
	names := map[string]rawProject{}
	after := ""
	for pages := 0; pages < maxScanPages; pages++ {
		raws, next, err := c.browsePage(ctx, op, "/projects", nil, after, maxLimit)
		if err != nil {
			return nil, err
		}
		for _, raw := range raws {
			names[raw.ID] = raw
		}
		if next == "" {
			break
		}
		after = next
	}
	return names, nil
}

// projectChain names a project and its ancestors from the top, joined by " / ".
func projectChain(names map[string]rawProject, id string) string {
	var chain []string
	for steps := 0; id != "" && steps < maxChainDepth; steps++ {
		raw, ok := names[id]
		if !ok {
			break
		}
		name := browseText(raw.Name)
		if name == "" {
			name = raw.ID
		}
		chain = append([]string{name}, chain...)
		id = text(raw.ParentID)
	}
	return strings.Join(chain, " / ")
}

// browsePage reads one page of projects.
func (c *Client) browsePage(ctx context.Context, op, path string, values url.Values, after string,
	size int) ([]rawProject, string, error) {
	entries, next, err := c.page(ctx, op, path, values, size, after)
	if err != nil {
		return nil, "", err
	}
	raws, err := decodeEach(op, entries, func(p rawProject) string { return p.ID })
	return raws, next, err
}

// browseSections reads one page of the sections of one project. The path is the name of that project.
func (c *Client) browseSections(ctx context.Context, parent, query string, binding []byte,
	after string) (config.TargetPage, error) {
	const op = "list section targets"
	var project rawProject
	if err := c.get(ctx, op, "/projects/"+url.PathEscape(parent), nil, &project); err != nil {
		return config.TargetPage{}, err
	}
	path, values := searchable("/sections", query)
	values.Set("project_id", parent)
	entries, next, err := c.page(ctx, op, path, values, browsePageSize, after)
	if err != nil {
		return config.TargetPage{}, err
	}
	raws, err := decodeEach(op, entries, func(s rawSection) string { return s.ID })
	if err != nil {
		return config.TargetPage{}, err
	}
	place := browseText(project.Name)
	page := config.TargetPage{Entries: []config.TargetEntry{}, Next: browseNext(binding, next),
		ServerSearch: query != ""}
	for _, raw := range raws {
		// The server filter is a courtesy: a section of another project is not listed.
		if !validID(raw.ID) || raw.IsDeleted || raw.archived() || raw.ProjectID != parent {
			continue
		}
		name := browseText(raw.Name)
		if name == "" {
			name = raw.ID
		}
		page.Entries = append(page.Entries, config.TargetEntry{Target: sectionPrefix + raw.ID, Name: name,
			Kind: "section", Path: place})
	}
	return page, nil
}

// browseResolveURL reads project and section from a URL of the web interface: /app/project/{slug}-{id}
// names a project and /app/task/{slug}-{id} names the project and, if set, the section of a task, which
// is read once. Only the host of the web interface counts, whatever the connection's base URL is.
func browseResolveURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _, rawURL string) ([]config.TargetEntry, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || !strings.EqualFold(parsed.Host, webHost) {
		return nil, nil
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 3 || segments[0] != "app" || (segments[1] != "project" && segments[1] != "task") {
		return nil, nil
	}
	slug, err := url.PathUnescape(segments[2])
	if err != nil {
		return nil, nil
	}
	id := slug[strings.LastIndex(slug, "-")+1:]
	if !validID(id) {
		return nil, nil
	}
	if segments[1] == "project" {
		return []config.TargetEntry{projectEntry(id)}, nil
	}
	client, err := browseClient(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var task rawTask
	if err := client.get(ctx, "read task url", "/tasks/"+url.PathEscape(id), nil, &task); err != nil {
		return nil, err
	}
	if task.ID == "" || task.IsDeleted {
		return nil, nil
	}
	var entries []config.TargetEntry
	if validID(task.ProjectID) {
		entries = append(entries, projectEntry(task.ProjectID))
	}
	if task.SectionID != nil && validID(*task.SectionID) {
		entries = append(entries, config.TargetEntry{Target: sectionPrefix + *task.SectionID, Name: *task.SectionID,
			Kind: "section"})
	}
	return entries, nil
}

func projectEntry(id string) config.TargetEntry {
	return config.TargetEntry{Target: treePrefix + id, Name: id, Kind: "project", HasChildren: true}
}
