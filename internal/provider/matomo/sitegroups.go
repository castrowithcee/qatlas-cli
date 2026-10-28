package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxGroupName = 250

var siteGroupsList = capability.Descriptor{
	ID: Provider + ".site-groups.list", Version: 1, Title: "List Matomo website groups",
	Description: "List the website groups of the instance, or with group the websites of one group (an empty group " +
		"names the websites without a group). It needs Super User rights and a connection bound to the instance " +
		"target; the websites of a group are kept to those inside the connection's website targets; names are untrusted",
	Tags: []string{"matomo", "sites", "groups", "list", "instance"}, Risk: readRisk(sitesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"group":{"type":"string","maxLength":250},` +
		pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"groups":{"type":"array","items":{"type":"string"}},` +
		`"sites":{"type":"array","items":{"type":"object","properties":{` + siteProperties + `},` +
		`"required":["id"],"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "group", Description: "Group name whose websites are listed, up to " +
		"250 characters; an empty text names the websites without a group; the group names are listed when omitted"}},
		pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "groups", Description: "Distinct group names, untrusted; an empty name stands for websites without a " +
			"group; present without group. The names come from the whole instance, not only from the targets, and also name groups of excluded websites"},
		{Name: "sites", Description: "With group: the websites of the group inside the website targets, ordered by id"},
	}, siteFields("sites[].", false)...), pagingFields...),
	Examples: []capability.Example{
		{Description: "List the group names", Arguments: json.RawMessage(`{}`)},
		{Description: "List the websites of a group", Arguments: json.RawMessage(`{"group":"Shops"}`)},
	},
}

// SiteGroupsResult is the answer of site-groups.list.
type SiteGroupsResult struct {
	Groups     []string `json:"groups,omitempty"`
	Sites      []Site   `json:"sites,omitempty"`
	HasMore    bool     `json:"has_more"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func invokeSiteGroupsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list website groups"
	var args struct {
		Group  *string `json:"group"`
		Limit  int     `json:"limit"`
		Cursor string  `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	// Checked here as well as in Reaches: the tool needs the instance target, whatever the caller sends.
	if !bound.instance {
		return nil, invalidArgument("this connection is not bound to the instance")
	}
	group := ""
	binding := "groups"
	if args.Group != nil {
		group = strings.TrimSpace(*args.Group)
		if len(group) > maxGroupName {
			return nil, invalidArgument("group must hold at most 250 characters")
		}
		binding = "group:" + group
	}
	paging, err := startPage(provider.CursorBinding(siteGroupsList.ID, bound.binding(), binding), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := siteRequirement(siteGroupsList.ID)
	if args.Group == nil {
		var names []json.RawMessage
		if err := client.call(ctx, op, req, "SitesManager.getSitesGroups", url.Values{}, &names); err != nil {
			return nil, err
		}
		groups := make([]string, 0, len(names))
		for _, name := range names {
			var text loose
			if text.UnmarshalJSON(name) != nil {
				return nil, provider.InvalidResponse(op, "Matomo returned an invalid group")
			}
			groups = append(groups, text.str(maxStringLength))
		}
		batch, next := slice(paging, groups)
		if batch == nil {
			batch = []string{}
		}
		return &SiteGroupsResult{Groups: batch, HasMore: next != "", NextCursor: next}, nil
	}
	var listed []siteJSON
	if err := client.call(ctx, op, req, "SitesManager.getSitesFromGroup", url.Values{"group": {group}}, &listed); err != nil {
		return nil, err
	}
	var kept []Site
	for _, row := range listed {
		if site, ok := row.site(); ok && bound.allows(site.ID) {
			kept = append(kept, site)
		}
	}
	slices.SortStableFunc(kept, func(a, b Site) int { return a.ID - b.ID })
	batch, next := slice(paging, kept)
	if batch == nil {
		batch = []Site{}
	}
	return &SiteGroupsResult{Sites: batch, HasMore: next != "", NextCursor: next}, nil
}
