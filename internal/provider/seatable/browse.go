package seatable

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	tableKind = "table"
	viewKind  = "view"
	// maxBrowseEntries bounds one list; the base metadata has no paging.
	maxBrowseEntries = 500
)

func targetBrowser() capability.TargetBrowser {
	return capability.TargetBrowser{List: listTargets, ResolveURL: resolveTargetURL}
}

// listTargets lists the tables of the base or the views of one table from the base metadata, never filtered
// by the connection's targets. Entries use the id: forms, because a table or view name can be renamed.
func listTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	const op = "choose targets"
	var parent target
	switch req.Kind {
	case tableKind:
	case viewKind:
		var err error
		if parent, err = parseTarget(strings.TrimSpace(req.Parent)); err != nil || parent.view != "" {
			return config.TargetPage{}, &provider.InvalidRequestError{Message: "the parent of a view must be a table"}
		}
	default:
		return config.TargetPage{}, &provider.InvalidRequestError{Message: "the kind is not one this provider offers"}
	}
	if req.Cursor != "" {
		return config.TargetPage{}, &provider.InvalidRequestError{Message: "the target cursor is not a page of this provider"}
	}
	if resolved == nil {
		return config.TargetPage{}, providerError(op, "no connection was selected")
	}
	client, err := build(ctx, resolved, secrets, red, nil, false)
	if err != nil {
		return config.TargetPage{}, err
	}
	document, err := client.metadata(ctx, op)
	if err != nil {
		return config.TargetPage{}, err
	}
	var page config.TargetPage
	for _, table := range document.Metadata.Tables {
		if len(page.Entries) >= maxBrowseEntries {
			break
		}
		if req.Kind == tableKind {
			if validID(table.ID) {
				page.Entries = append(page.Entries, config.TargetEntry{Target: idPrefix + table.ID,
					Name: browseName(table.Name, table.ID), Kind: tableKind, HasChildren: len(table.Views) > 0})
			}
			continue
		}
		if !matchesTable(parent, table) || !validID(table.ID) {
			continue
		}
		for _, view := range table.Views {
			if validID(view.ID) && len(page.Entries) < maxBrowseEntries {
				page.Entries = append(page.Entries, config.TargetEntry{Target: idPrefix + table.ID + "/" + idPrefix + view.ID,
					Name: browseName(view.Name, view.ID), Kind: viewKind})
			}
		}
	}
	return page, nil
}

// browseName makes a name plain text for the picker, which drops an entry whose name is empty or carries
// control characters: those become spaces, an empty name falls back to the ID.
func browseName(name, id string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, name))
	if r := []rune(name); len(r) > maxNameLength {
		name = string(r[:maxNameLength])
	}
	if name == "" {
		return id
	}
	return name
}

// resolveTargetURL reads table and view from the address of a base in the web interface. The address is
// <origin>/workspace/<workspace id>/dtable/<base name>/ (SeaTable help, "Find the workspace ID of a group")
// with the query tid and vid holding table and view ID (SeaTable API reference, "List View External Links").
// The address counts only on the host of the connection and only for the base of the connection: the token
// exchange tells its workspace and name, and the metadata proves table and view exist in it.
func resolveTargetURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	origin, rawURL string) ([]config.TargetEntry, error) {
	const op = "resolve target"
	if resolved == nil || secrets == nil || origin == "" {
		return nil, nil
	}
	service, err := url.Parse(origin)
	parsed, perr := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || perr != nil || service.Host == "" || parsed.Scheme != "https" ||
		!strings.EqualFold(parsed.Host, service.Host) {
		return nil, nil
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) != 4 || segments[0] != "workspace" || segments[2] != "dtable" || segments[3] == "" {
		return nil, nil
	}
	query := parsed.Query()
	tableID, viewID := query.Get("tid"), query.Get("vid")
	if !validID(tableID) || viewID != "" && !validID(viewID) {
		return nil, nil
	}
	client, err := build(ctx, resolved, secrets, red, nil, false)
	if err != nil {
		return nil, nil
	}
	access, err := client.access(ctx, op)
	if err != nil || segments[1] != strconv.FormatInt(access.workspaceID, 10) || segments[3] != access.name {
		return nil, nil
	}
	document, err := client.metadata(ctx, op)
	if err != nil {
		return nil, nil
	}
	for _, table := range document.Metadata.Tables {
		if table.ID != tableID {
			continue
		}
		entries := []config.TargetEntry{{Target: idPrefix + table.ID, Name: browseName(table.Name, table.ID),
			Kind: tableKind, HasChildren: len(table.Views) > 0}}
		for _, view := range table.Views {
			if viewID != "" && view.ID == viewID {
				entries = append(entries, config.TargetEntry{Target: idPrefix + table.ID + "/" + idPrefix + view.ID,
					Name: browseName(view.Name, view.ID), Kind: viewKind})
			}
		}
		return entries, nil
	}
	return nil, nil
}

// reaches names the tools a binding cannot serve, as the call refuses them today: changing the base itself
// needs the whole base, and a change of a table's structure needs a table that is not narrowed to a view.
func reaches(tool string, targets []string) (bool, []string) {
	if len(targets) == 0 {
		return true, nil
	}
	switch tool {
	case baseOperations.ID, tablesCreate.ID, tablesDuplicate.ID:
		if len(targets) == 1 && strings.TrimSpace(targets[0]) == "*" {
			return true, nil
		}
		return false, []string{"*"}
	case tablesRename.ID, tablesDelete.ID, columnCreate.ID, columnUpdate.ID, columnDelete.ID,
		optionsAdd.ID, optionsUpdate.ID, optionsDelete.ID, viewsCreate.ID:
		for _, value := range targets {
			if value = strings.TrimSpace(value); value == "*" {
				return true, nil
			}
			if parsed, err := parseTarget(value); err != nil || parsed.view == "" {
				// A malformed value is refused at the call; it does not narrow the binding here.
				return true, nil
			}
		}
		return false, []string{"TABLE", "*"}
	}
	return true, nil
}
