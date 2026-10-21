package baserow

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// webHostCloud is the web interface of Baserow Cloud; its API sits on api.baserow.io.
const webHostCloud = "baserow.io"

func targetBrowser() capability.TargetBrowser {
	return capability.TargetBrowser{List: listTargets, ResolveURL: resolveTargetURL}
}

// listTargets lists the databases of the token or the tables of one database, never filtered by the
// connection's targets. A database token reads the table list of the whole token
// (GET /api/database/tables/all-tables/, "only works in combination with the token authentication"; the
// per-database table list declares no token authentication), and that list carries no database name. A
// database is therefore derived from the table list and is shown by its ID. The table list has no paging, so
// every list is one page.
func listTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	const op = "choose targets"
	var parent int64
	switch req.Kind {
	case "database":
	case "table":
		database, id, err := parseTarget(strings.TrimSpace(req.Parent))
		if err != nil || !database {
			return config.TargetPage{}, invalidRequest("the parent of a table must be database/DATABASE_ID")
		}
		parent = id
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one this provider offers")
	}
	if req.Cursor != "" {
		return config.TargetPage{}, invalidRequest("the target cursor is not a page of this provider")
	}
	if resolved == nil {
		return config.TargetPage{}, providerError("open", "no connection was selected")
	}
	// Choosing targets reads beyond the connection's targets, which may not exist yet: an empty scope.
	client, err := build(ctx, resolved, secrets, red, nil, scope{})
	if err != nil {
		return config.TargetPage{}, err
	}
	var raw []tableJSON
	if err := client.get(ctx, op, allTablesPath, nil, &raw); err != nil {
		return config.TargetPage{}, err
	}
	var page config.TargetPage
	if req.Kind == "database" {
		seen := map[int64]bool{}
		var ids []int64
		for _, entry := range raw {
			if entry.ID > 0 && entry.DatabaseID > 0 && !seen[entry.DatabaseID] {
				seen[entry.DatabaseID] = true
				ids = append(ids, entry.DatabaseID)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			text := strconv.FormatInt(id, 10)
			page.Entries = append(page.Entries, config.TargetEntry{Target: databasePrefix + text, Name: text,
				Kind: "database", HasChildren: true})
		}
		return page, nil
	}
	for _, entry := range raw {
		if entry.ID > 0 && entry.DatabaseID == parent && len(page.Entries) < maxTables {
			page.Entries = append(page.Entries, config.TargetEntry{Target: tablePrefix + strconv.FormatInt(entry.ID, 10),
				Name: browseName(entry.Name, entry.ID), Kind: "table"})
		}
	}
	return page, nil
}

// browseName makes a table name plain text for the picker, which drops an entry whose name is empty or carries
// control characters: those become spaces, and an empty name falls back to the ID.
func browseName(name string, id int64) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, bounded(name)))
	if name == "" {
		return strconv.FormatInt(id, 10)
	}
	return name
}

// resolveTargetURL reads database and table from a URL of the web interface without a request. The route
// /database/:databaseId/table/:tableId/:viewId? is the "database-table" route of the Baserow frontend
// (web-frontend/modules/database/routes.js, tag 2.4.0). Baserow Cloud serves it on baserow.io while its API
// sits on api.baserow.io. For a self-hosted instance the web origin is configured apart from the API
// origin (PUBLIC_WEB_FRONTEND_URL, PUBLIC_BACKEND_URL), so only the host of the connection's own base URL
// counts. Names are unknown without a request, so the IDs stand in.
func resolveTargetURL(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	origin, rawURL string) ([]config.TargetEntry, error) {
	if origin == "" {
		origin = cloudOrigin
	}
	service, err := url.Parse(origin)
	if err != nil || service.Host == "" {
		return nil, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" {
		return nil, nil
	}
	host := strings.ToLower(parsed.Host)
	apiHost := strings.ToLower(service.Host)
	if host != apiHost && !(apiHost == "api."+webHostCloud && host == webHostCloud) {
		return nil, nil
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 2 || segments[0] != "database" {
		return nil, nil
	}
	database, ok := parseID(segments[1])
	if !ok {
		return nil, nil
	}
	databaseText := strconv.FormatInt(database, 10)
	entries := []config.TargetEntry{{Target: databasePrefix + databaseText, Name: databaseText, Kind: "database", HasChildren: true}}
	if len(segments) >= 4 && segments[2] == "table" {
		if table, ok := parseID(segments[3]); ok {
			text := strconv.FormatInt(table, 10)
			entries = append(entries, config.TargetEntry{Target: tablePrefix + text, Name: text, Kind: "table"})
		}
	}
	return entries, nil
}
