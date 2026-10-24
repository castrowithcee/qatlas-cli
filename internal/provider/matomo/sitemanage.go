package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxAliasURLs      = 20
	maxAliasURLLength = 2000
	maxCreatedSites   = 100
	dateLayout        = "2006-01-02"
)

var (
	siteSentOnce      = sentOnceFor("website", "matomo.sites.get")
	siteGroupSentOnce = sentOnceFor("group", "matomo.site-groups.list")
)

func siteChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: sitesSensitive}
}

const aliasURLsSchema = `"urls":{"type":"array","maxItems":20,"items":{"type":"string","minLength":1,"maxLength":2000}}`

const siteURLsOutput = `{"type":"object","properties":{"idSite":{"type":"integer"},"inserted":{"type":"integer"},` +
	`"updated":{"type":"boolean"}},"required":["idSite","inserted","updated"],"additionalProperties":false}`

const urlsNote = "URLs are only an assignment for tracking: Matomo stores them as accepted addresses of the website, " +
	"never fetches them, and sends no data to them. Each is an http or https address without user information, up to " +
	"2000 characters, at most 20; the main URL of the website is not touched; it needs an admin right on the website"

var siteURLsAdd = capability.Descriptor{
	ID: Provider + ".site-urls.add", Version: 1, Title: "Add alias URLs to a Matomo website",
	Description: "Add alias URLs to one website, keeping the ones it has; URLs already stored are not duplicated. " +
		urlsNote + siteSentOnce,
	Tags: []string{"matomo", "sites", "urls", "create"}, Risk: siteChangeRisk(capability.EffectCreate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + aliasURLsSchema + `},` +
		`"required":["urls"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(siteURLsOutput),
	Arguments: []capability.Argument{idSiteArgument(), {Name: "urls", Required: true,
		Description: "The alias URLs to add, 1 to 20 http or https addresses"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the URLs were added to"},
		{Name: "inserted", Description: "Number of URLs Matomo newly stored"},
		{Name: "updated", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Add a second host to website 1",
		Arguments: json.RawMessage(`{"idSite":1,"urls":["https://www.shop.example.test"]}`)}},
}

var siteURLsSet = capability.Descriptor{
	ID: Provider + ".site-urls.set", Version: 1, Title: "Set the alias URLs of a Matomo website",
	Description: "Replace the whole list of alias URLs of one website with the given list; URLs not in it are " +
		"removed, and an empty list removes all alias URLs, so read the website first. " + urlsNote + siteSentOnce,
	Tags: []string{"matomo", "sites", "urls", "update"}, Risk: siteChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + aliasURLsSchema + `},` +
		`"required":["urls"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(siteURLsOutput),
	Arguments: []capability.Argument{idSiteArgument(), {Name: "urls", Required: true,
		Description: "The complete list of alias URLs, 0 to 20 http or https addresses; an empty list removes " +
			"every alias URL"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website whose alias URLs were replaced"},
		{Name: "inserted", Description: "The count Matomo reports for this call"},
		{Name: "updated", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Keep exactly one alias URL",
		Arguments: json.RawMessage(`{"idSite":1,"urls":["https://www.shop.example.test"]}`)}},
}

var sitesUpdateCreatedTime = capability.Descriptor{
	ID: Provider + ".sites.update-created-time", Version: 1, Title: "Update the creation date of Matomo websites",
	Description: "Set the creation date of up to 100 websites to one day before minDate (YYYY-MM-DD). It needs an " +
		"admin right on every website, and the whole call is refused when one is outside the connection's targets. " +
		"It changes only the stored creation date, which bounds the dates Matomo reports for a website; idempotent" +
		siteSentOnce,
	Tags: []string{"matomo", "sites", "created", "update"}, Risk: siteChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSites":{"type":"array","minItems":1,` +
		`"maxItems":100,"uniqueItems":true,"items":{"type":"integer","minimum":1,"maximum":999999999}},` +
		`"minDate":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}},` +
		`"required":["idSites","minDate"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSites":{"type":"array","items":{"type":"integer"}},` +
		`"minDate":{"type":"string"},"updated":{"type":"boolean"}},"required":["idSites","minDate","updated"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSites", Required: true, Description: "1 to 100 website IDs, each inside the " +
			"connection's targets; no all"},
		{Name: "minDate", Required: true, Description: "Earliest date as YYYY-MM-DD; the creation date becomes " +
			"the day before"},
	},
	Fields: []capability.Field{
		{Name: "idSites", Description: "The websites that were updated"},
		{Name: "minDate", Description: "The date that was sent"},
		{Name: "updated", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Move the creation date of website 1 back",
		Arguments: json.RawMessage(`{"idSites":[1],"minDate":"2024-01-15"}`)}},
}

var siteGroupsRename = capability.Descriptor{
	ID: Provider + ".site-groups.rename", Version: 1, Title: "Rename a Matomo website group",
	Description: "Rename a website group across all websites of the instance that use it, including websites " +
		"outside the connection's website targets. It needs Super User rights and a connection bound to the " +
		"instance target. An empty new name removes the group from those websites; idempotent" + siteGroupSentOnce,
	Tags: []string{"matomo", "sites", "groups", "update", "instance"}, Risk: siteChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"oldGroupName":{"type":"string","minLength":1,` +
		`"maxLength":250},"newGroupName":{"type":"string","maxLength":250}},"required":["oldGroupName","newGroupName"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"oldGroupName":{"type":"string"},` +
		`"newGroupName":{"type":"string"},"renamed":{"type":"boolean"}},"required":["oldGroupName","newGroupName",` +
		`"renamed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "oldGroupName", Required: true, Description: "Existing group name, up to 250 characters, not empty"},
		{Name: "newGroupName", Required: true, Description: "New group name, up to 250 characters; empty removes " +
			"the group"},
	},
	Fields: []capability.Field{
		{Name: "oldGroupName", Description: "The group that was renamed"},
		{Name: "newGroupName", Description: "The new name"},
		{Name: "renamed", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Rename a group",
		Arguments: json.RawMessage(`{"oldGroupName":"Shops","newGroupName":"Stores"}`)}},
}

// SiteURLsChanged is the answer of site-urls.add and site-urls.set.
type SiteURLsChanged struct {
	IDSite   int  `json:"idSite"`
	Inserted int  `json:"inserted"`
	Updated  bool `json:"updated"`
}

// SitesCreatedTimeUpdated is the answer of sites.update-created-time.
type SitesCreatedTimeUpdated struct {
	IDSites []int  `json:"idSites"`
	MinDate string `json:"minDate"`
	Updated bool   `json:"updated"`
}

// SiteGroupRenamed is the answer of site-groups.rename.
type SiteGroupRenamed struct {
	OldGroupName string `json:"oldGroupName"`
	NewGroupName string `json:"newGroupName"`
	Renamed      bool   `json:"renamed"`
}

// aliasURLs checks the list before anything is sent: plain http or https addresses without user information.
func aliasURLs(urls []string, allowEmpty bool) ([]string, error) {
	if len(urls) > maxAliasURLs || (len(urls) == 0 && !allowEmpty) {
		return nil, invalidArgument("urls must hold 1 to 20 addresses")
	}
	for _, raw := range urls {
		if raw == "" || len(raw) > maxAliasURLLength || strings.IndexFunc(raw, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		}) >= 0 {
			return nil, invalidArgument("each url must be a non-empty address without spaces, up to 2000 characters")
		}
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
			parsed.User != nil {
			return nil, invalidArgument("each url must be an http or https address without user information")
		}
	}
	return slices.Clone(urls), nil
}

func invokeSiteURLs(descriptor capability.Descriptor, method, op string, allowEmpty bool) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		var args struct {
			IDSite *int     `json:"idSite"`
			URLs   []string `json:"urls"`
		}
		if err := decodeArguments(raw, &args); err != nil {
			return nil, err
		}
		id, err := selectSite(resolved, args.IDSite)
		if err != nil {
			return nil, err
		}
		urls, err := aliasURLs(args.URLs, allowEmpty)
		if err != nil {
			return nil, err
		}
		params := url.Values{"idSite": {strconv.Itoa(id)}}
		if len(urls) > 0 {
			params["urls[]"] = urls
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		var answer json.RawMessage
		if err := client.change(ctx, op, siteRequirement(descriptor.ID), method, params, siteSentOnce, &answer); err != nil {
			return nil, err
		}
		var inserted int
		if scalar(answer, &inserted) != nil || inserted < 0 {
			return nil, unclearChange(op, "Matomo returned no usable count", siteSentOnce)
		}
		return &SiteURLsChanged{IDSite: id, Inserted: inserted, Updated: true}, nil
	}
}

func invokeSitesUpdateCreatedTime(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update website creation date"
	var args struct {
		IDSites []int  `json:"idSites"`
		MinDate string `json:"minDate"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if len(args.IDSites) < 1 || len(args.IDSites) > maxCreatedSites {
		return nil, invalidArgument("idSites must hold 1 to 100 website IDs")
	}
	seen := map[int]bool{}
	ids := make([]string, 0, len(args.IDSites))
	for _, id := range args.IDSites {
		if id < 1 || len(strconv.Itoa(id)) > maxIDDigits {
			return nil, invalidArgument("idSites must be positive integers")
		}
		if !bound.allows(id) {
			return nil, invalidArgument("an ID in idSites is outside the targets of this connection")
		}
		if seen[id] {
			return nil, invalidArgument("idSites must not repeat an ID")
		}
		seen[id] = true
		ids = append(ids, strconv.Itoa(id))
	}
	date, err := time.Parse(dateLayout, args.MinDate)
	if err != nil || date.Format(dateLayout) != args.MinDate {
		return nil, invalidArgument("minDate must be a date as YYYY-MM-DD")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSites": {strings.Join(ids, ",")}, "minDate": {args.MinDate}}
	var answer json.RawMessage
	if err := client.change(ctx, op, siteRequirement(sitesUpdateCreatedTime.ID), "SitesManager.updateSiteCreatedTime",
		params, siteSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", siteSentOnce)
	}
	return &SitesCreatedTimeUpdated{IDSites: args.IDSites, MinDate: args.MinDate, Updated: true}, nil
}

func invokeSiteGroupsRename(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "rename website group"
	var args struct {
		Old *string `json:"oldGroupName"`
		New *string `json:"newGroupName"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.instance {
		return nil, invalidArgument("this connection is not bound to the instance")
	}
	if args.Old == nil || args.New == nil {
		return nil, invalidArgument("oldGroupName and newGroupName are required")
	}
	// An empty old name would address every website without a group.
	oldName, newName := strings.TrimSpace(*args.Old), strings.TrimSpace(*args.New)
	if oldName == "" || len(oldName) > maxGroupName || len(newName) > maxGroupName {
		return nil, invalidArgument("oldGroupName must be 1 to 250 characters and newGroupName at most 250")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"oldGroupName": {oldName}, "newGroupName": {newName}}
	var answer json.RawMessage
	if err := client.change(ctx, op, siteRequirement(siteGroupsRename.ID), "SitesManager.renameGroup", params,
		siteGroupSentOnce, &answer); err != nil {
		return nil, err
	}
	var done bool
	if scalar(answer, &done) != nil || !done {
		return nil, unclearChange(op, "Matomo returned no confirmation of the rename", siteGroupSentOnce)
	}
	return &SiteGroupRenamed{OldGroupName: oldName, NewGroupName: newName, Renamed: true}, nil
}
