package matomo

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
	browsePageSize = 50
	maxBrowseText  = 200
)

func targetBrowser() capability.TargetBrowser {
	return capability.TargetBrowser{List: listTargets, ResolveURL: resolveTargetURL}
}

// browseText makes a website name plain text for the picker: control characters become spaces and the text
// is cut on a rune boundary.
func browseText(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
	return strings.TrimSpace(boundString(value, maxBrowseText))
}

// listTargets lists the websites the token can view as site/ID, never filtered by the connection's targets,
// so a person finds a website the targets do not name yet. The list method is the one of sites.list; a search
// text uses the pattern method. The instance is no listed kind.
func listTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	const op = "list website targets"
	if req.Kind != "site" || req.Parent != "" {
		return config.TargetPage{}, invalidArgument("the kind is not one this provider lists")
	}
	if resolved == nil {
		return config.TargetPage{}, provider.Fail("open", "no connection was selected")
	}
	query := strings.TrimSpace(req.Query)
	if len(query) > maxPatternLength {
		return config.TargetPage{}, invalidArgument("the search text is too long")
	}
	paging, err := startPage(provider.CursorBinding("targets", query), browsePageSize, req.Cursor)
	if err != nil {
		return config.TargetPage{}, err
	}
	client, err := build(ctx, resolved, secrets, red, wholeToken())
	if err != nil {
		return config.TargetPage{}, err
	}
	method := "SitesManager.getSitesWithAtLeastViewAccess"
	params := url.Values{"limit": {strconv.Itoa(paging.want())}}
	if query != "" {
		method = "SitesManager.getPatternMatchSites"
		params.Set("pattern", query)
	}
	var listed []siteJSON
	if err := client.call(ctx, op, siteRequirement(""), method, params, &listed); err != nil {
		return config.TargetPage{}, err
	}
	var rows []config.TargetEntry
	for _, row := range listed {
		if site, ok := row.site(); ok {
			name := browseText(site.Name)
			if name == "" {
				name = strconv.Itoa(site.ID)
			}
			rows = append(rows, config.TargetEntry{Target: sitePrefix + strconv.Itoa(site.ID), Name: name, Kind: "site"})
		}
	}
	batch, next := slice(paging, rows)
	return config.TargetPage{Entries: batch, Next: next, ServerSearch: query != ""}, nil
}

// resolveTargetURL resolves nothing: no address form of the Matomo interface is documented as a stable
// reference to a website, so a person chooses from the list or enters the ID.
func resolveTargetURL(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, string,
	string) ([]config.TargetEntry, error) {
	return nil, nil
}
