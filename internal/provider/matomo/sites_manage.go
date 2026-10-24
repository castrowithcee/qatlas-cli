package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxSiteNameLength     = 250
	maxSiteDescription    = 255
	maxWrittenPlugins     = 20
	maxSettingsPerPlugin  = 50
	maxSettingValueItems  = 100
	maxSettingNameLength  = 100
	maxSettingTextLength  = 2048
	maxSiteShortText      = 64
	siteChangeSensitivity = "matomo-websites"
)

var startDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

var (
	websiteSentOnce = sentOnceFor("website", "matomo.sites.get")
	websiteListOnce = sentOnceFor("websites", "matomo.sites.list")
)

func siteChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: siteChangeSensitivity}
}

// siteDefinitionSchema holds the members that SitesManager.addSite and SitesManager.updateSite share.
const siteDefinitionSchema = `"urls":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string","minLength":1,` +
	`"maxLength":2048}},"ecommerce":{"type":"boolean"},"siteSearch":{"type":"boolean"},` +
	`"searchKeywordParameters":{"type":"string","maxLength":2048},` +
	`"searchCategoryParameters":{"type":"string","maxLength":2048},"excludedIps":{"type":"string","maxLength":2048},` +
	`"excludedQueryParameters":{"type":"string","maxLength":2048},"timezone":{"type":"string","maxLength":64},` +
	`"currency":{"type":"string","maxLength":64},"group":{"type":"string","maxLength":250},` +
	`"startDate":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},` +
	`"excludedUserAgents":{"type":"string","maxLength":2048},"keepURLFragments":{"type":"integer","minimum":0,"maximum":2},` +
	`"type":{"type":"string","maxLength":64},"settingValues":{"type":"object","maxProperties":20,` +
	`"additionalProperties":{"type":"array","maxItems":50,"items":{"type":"object","properties":{` +
	`"name":{"type":"string","minLength":1,"maxLength":100},"value":{}},"required":["name","value"],` +
	`"additionalProperties":false}}},"excludeUnknownUrls":{"type":"boolean"},` +
	`"excludedReferrers":{"type":"string","maxLength":2048},"description":{"type":"string","maxLength":255}`

func siteDefinitionArguments(update bool) []capability.Argument {
	list := "empty when omitted"
	if update {
		list = "left unchanged when omitted; an empty text clears it"
	}
	same := "the default of the instance when omitted"
	if update {
		same = "left unchanged when omitted"
	}
	return []capability.Argument{
		{Name: "urls", Description: "Addresses of the website, 1 to 100 of up to 2048 characters; the first is the main " +
			"address, the others are aliases" + map[bool]string{true: "; given, they replace all current addresses; left unchanged when omitted", false: ""}[update]},
		{Name: "ecommerce", Description: "True to enable ecommerce reporting; " + same},
		{Name: "siteSearch", Description: "True to enable site search tracking; " + same},
		{Name: "searchKeywordParameters", Description: "Comma-separated query parameter names that carry the site " +
			"search keyword, up to 2048 characters; " + list},
		{Name: "searchCategoryParameters", Description: "Comma-separated query parameter names that carry the site " +
			"search category, up to 2048 characters; " + list},
		{Name: "excludedIps", Description: "Comma-separated IP addresses or ranges excluded from tracking (wildcards " +
			"allowed), up to 2048 characters; " + list},
		{Name: "excludedQueryParameters", Description: "Comma-separated URL query parameters stripped from tracked " +
			"addresses, up to 2048 characters; " + list},
		{Name: "timezone", Description: "Time zone such as Europe/Berlin; " + same},
		{Name: "currency", Description: "Currency code such as EUR; " + same},
		{Name: "group", Description: "Group label, up to 250 characters; Super User only (Matomo ignores it for others); " +
			"empty and no group when omitted on add, left unchanged when omitted on update"},
		{Name: "startDate", Description: "Date YYYY-MM-DD from which statistics start (the creation date of the " +
			"website); today when omitted on add, left unchanged when omitted on update"},
		{Name: "excludedUserAgents", Description: "Comma-separated user agent fragments excluded from tracking, up to " +
			"2048 characters; " + list},
		{Name: "keepURLFragments", Description: "0 follows the global setting, 1 keeps URL fragments, 2 removes them; " + same},
		{Name: "type", Description: "Website type such as website; website when omitted on add, left unchanged when " +
			"omitted on update"},
		{Name: "settingValues", Description: "Further measurable settings keyed by plugin name, each a list of name and " +
			"value pairs (value: text, number, boolean, or a list of texts or numbers); at most 20 plugins and 50 " +
			"settings each; they apply to the website type as Matomo defines them"},
		{Name: "excludeUnknownUrls", Description: "True to track only addresses that belong to the website; " + same},
		{Name: "excludedReferrers", Description: "Comma-separated referrer hosts or addresses excluded from referrer " +
			"detection, up to 2048 characters; " + list},
		{Name: "description", Description: "Description of the website, up to 255 characters; empty when omitted on " +
			"add, left unchanged when omitted on update"},
	}
}

var sitesAdd = capability.Descriptor{
	ID: Provider + ".sites.add", Version: 1, Title: "Add a Matomo website",
	Description: "Create a website (SitesManager.addSite). It needs Super User rights and a connection whose targets " +
		"name the instance. The new website lies outside the site/ID targets the connection binds: it is reachable " +
		"only after the targets change, or with the target site without an ID. Not idempotent, since a repeated call " +
		"creates a second website; the result names the new idSite; sent once and never repeated",
	Tags: []string{"matomo", "sites", "websites", "create", "instance"}, Risk: siteChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"siteName":{"type":"string","minLength":1,` +
		`"maxLength":250},` + siteDefinitionSchema + `},"required":["siteName"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"created":{"type":"boolean"}},` +
		`"required":["idSite","created"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "siteName", Required: true,
		Description: "Website name, 1 to 250 characters"}}, siteDefinitionArguments(false)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "ID of the new website; it lies outside the site/ID targets of the connection"},
		{Name: "created", Description: "True once Matomo created the website"},
	},
	Examples: []capability.Example{{Description: "Create a website",
		Arguments: json.RawMessage(`{"siteName":"Shop","urls":["https://shop.example.test"],"timezone":"Europe/Berlin",` +
			`"currency":"EUR","ecommerce":true}`)}},
}

var sitesUpdate = capability.Descriptor{
	ID: Provider + ".sites.update", Version: 1, Title: "Update a Matomo website",
	Description: "Change the settings of one website (SitesManager.updateSite); it needs an admin right on it. A value " +
		"not given stays unchanged. Matomo would clear the six comma-separated lists (searchKeywordParameters, " +
		"searchCategoryParameters, excludedIps, excludedQueryParameters, excludedUserAgents, excludedReferrers) when " +
		"they are left out, so Qatlas reads the website first and sends the current value of each list that is not " +
		"given. group needs Super User rights; urls, when given, replace all addresses. Idempotent, since the same " +
		"values leave the same state; sent once and never repeated",
	Tags: []string{"matomo", "sites", "websites", "update"}, Risk: siteChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,"siteName":{"type":"string",` +
		`"minLength":1,"maxLength":250},` + siteDefinitionSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"updated":{"type":"boolean"}},` +
		`"required":["idSite","updated"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(), {Name: "siteName",
		Description: "New website name, 1 to 250 characters; left unchanged when omitted"}}, siteDefinitionArguments(true)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website that was changed"},
		{Name: "updated", Description: "True once Matomo applied the change"},
	},
	Examples: []capability.Example{{Description: "Exclude an address range of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"excludedIps":"192.0.2.0/24"}`)}},
}

var sitesDelete = capability.Descriptor{
	ID: Provider + ".sites.delete", Version: 1, Title: "Delete a Matomo website",
	Description: "Delete one website for good (SitesManager.deleteSite). The website record and its configuration " +
		"(goals, segments, and the like) are removed and cannot be restored; Matomo does not delete its tracked logs " +
		"and archives, which stay in the database unreachable by the website. It needs Super User rights, a " +
		"connection whose targets name the instance, and a website inside its site targets; Matomo refuses to delete " +
		"the last website. A personal token needs no password confirmation. The website is read first; sent once and " +
		"never repeated; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "sites", "websites", "delete", "instance"}, Risk: siteChangeRisk(capability.EffectDelete,
		capability.IdempotencyIdempotent), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer","minimum":1,` +
		`"maximum":999999999}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"name":{"type":"string"},` +
		`"deleted":{"type":"boolean"}},"required":["idSite","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "idSite", Target: "site",
		Description: "ID of the website to delete, from matomo.sites.list; always required here; it must lie inside the connection's site targets"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website that was deleted"},
		{Name: "name", Description: "Name the website had, untrusted"},
		{Name: "deleted", Description: "True once Matomo deleted the website"},
	},
	Examples: []capability.Example{{Description: "Delete website 4", Arguments: json.RawMessage(`{"idSite":4}`)}},
}

// SiteAdded is the answer of sites.add.
type SiteAdded struct {
	IDSite  int  `json:"idSite"`
	Created bool `json:"created"`
}

// SiteUpdated is the answer of sites.update.
type SiteUpdated struct {
	IDSite  int  `json:"idSite"`
	Updated bool `json:"updated"`
}

// SiteDeleted is the answer of sites.delete.
type SiteDeleted struct {
	IDSite  int    `json:"idSite"`
	Name    string `json:"name,omitempty"`
	Deleted bool   `json:"deleted"`
}

type siteSettingValue struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// siteDefinition is the set of values both SitesManager.addSite and SitesManager.updateSite take.
type siteDefinition struct {
	SiteName                 *string                       `json:"siteName"`
	URLs                     []string                      `json:"urls"`
	Ecommerce                *bool                         `json:"ecommerce"`
	SiteSearch               *bool                         `json:"siteSearch"`
	SearchKeywordParameters  *string                       `json:"searchKeywordParameters"`
	SearchCategoryParameters *string                       `json:"searchCategoryParameters"`
	ExcludedIPs              *string                       `json:"excludedIps"`
	ExcludedQueryParameters  *string                       `json:"excludedQueryParameters"`
	Timezone                 *string                       `json:"timezone"`
	Currency                 *string                       `json:"currency"`
	Group                    *string                       `json:"group"`
	StartDate                *string                       `json:"startDate"`
	ExcludedUserAgents       *string                       `json:"excludedUserAgents"`
	KeepURLFragments         *int                          `json:"keepURLFragments"`
	Type                     *string                       `json:"type"`
	SettingValues            map[string][]siteSettingValue `json:"settingValues"`
	ExcludeUnknownURLs       *bool                         `json:"excludeUnknownUrls"`
	ExcludedReferrers        *string                       `json:"excludedReferrers"`
	Description              *string                       `json:"description"`
}

// listParams are the comma-separated lists that Matomo resets when they are not sent.
func (d *siteDefinition) listParams() map[string]**string {
	return map[string]**string{
		"searchKeywordParameters": &d.SearchKeywordParameters, "searchCategoryParameters": &d.SearchCategoryParameters,
		"excludedIps": &d.ExcludedIPs, "excludedQueryParameters": &d.ExcludedQueryParameters,
		"excludedUserAgents": &d.ExcludedUserAgents, "excludedReferrers": &d.ExcludedReferrers,
	}
}

func checkText(name string, value *string, limit int) error {
	if value != nil && utf8.RuneCountInString(*value) > limit {
		return invalidArgument(name + " must hold at most " + strconv.Itoa(limit) + " characters")
	}
	return nil
}

// params checks the values the way the schema states and builds the form values. Matomo stays the authority.
func (d *siteDefinition) params() (url.Values, error) {
	values := url.Values{}
	if d.SiteName != nil {
		if strings.TrimSpace(*d.SiteName) == "" || utf8.RuneCountInString(*d.SiteName) > maxSiteNameLength {
			return nil, invalidArgument("siteName must hold 1 to 250 characters")
		}
		values.Set("siteName", *d.SiteName)
	}
	if d.URLs != nil {
		if len(d.URLs) < 1 || len(d.URLs) > maxSiteURLs {
			return nil, invalidArgument("urls must hold 1 to 100 addresses")
		}
		for _, address := range d.URLs {
			if strings.TrimSpace(address) == "" || len(address) > maxUrlLength {
				return nil, invalidArgument("every address in urls must hold 1 to 2048 characters")
			}
			values.Add("urls[]", address)
		}
	}
	for name, flag := range map[string]*bool{"ecommerce": d.Ecommerce, "siteSearch": d.SiteSearch,
		"excludeUnknownUrls": d.ExcludeUnknownURLs} {
		if flag != nil {
			values.Set(name, flagValue(flag))
		}
	}
	for name, field := range d.listParams() {
		if err := checkText(name, *field, maxSettingTextLength); err != nil {
			return nil, err
		}
		if *field != nil {
			values.Set(name, **field)
		}
	}
	for name, field := range map[string]struct {
		value *string
		limit int
	}{"timezone": {d.Timezone, maxSiteShortText}, "currency": {d.Currency, maxSiteShortText},
		"group": {d.Group, 250}, "type": {d.Type, maxSiteShortText}, "description": {d.Description, maxSiteDescription}} {
		if err := checkText(name, field.value, field.limit); err != nil {
			return nil, err
		}
		if field.value != nil {
			values.Set(name, *field.value)
		}
	}
	if d.StartDate != nil {
		if !startDatePattern.MatchString(*d.StartDate) {
			return nil, invalidArgument("startDate must be a date as YYYY-MM-DD")
		}
		values.Set("startDate", *d.StartDate)
	}
	if d.KeepURLFragments != nil {
		if *d.KeepURLFragments < 0 || *d.KeepURLFragments > 2 {
			return nil, invalidArgument("keepURLFragments must be 0, 1, or 2")
		}
		values.Set("keepURLFragments", strconv.Itoa(*d.KeepURLFragments))
	}
	return values, d.settingParams(values)
}

// settingParams encodes settingValues the way Matomo reads nested form arrays.
func (d *siteDefinition) settingParams(values url.Values) error {
	if len(d.SettingValues) > maxWrittenPlugins {
		return invalidArgument("settingValues may name at most 20 plugins")
	}
	for plugin, settings := range d.SettingValues {
		if !validPlugin(plugin) || len(settings) > maxSettingsPerPlugin {
			return invalidArgument("settingValues needs plugin names and at most 50 settings per plugin")
		}
		for i, setting := range settings {
			if setting.Name == "" || len(setting.Name) > maxSettingNameLength {
				return invalidArgument("every setting needs a name of 1 to 100 characters")
			}
			key := "settingValues[" + plugin + "][" + strconv.Itoa(i) + "]"
			values.Set(key+"[name]", setting.Name)
			var scalarValue any
			if json.Unmarshal(setting.Value, &scalarValue) != nil {
				return invalidArgument("a setting value could not be read")
			}
			switch v := scalarValue.(type) {
			case string:
				if len(v) > maxSettingTextLength {
					return invalidArgument("a setting value is too long")
				}
				values.Set(key+"[value]", v)
			case float64:
				values.Set(key+"[value]", strconv.FormatFloat(v, 'f', -1, 64))
			case bool:
				values.Set(key+"[value]", flagValue(&v))
			case []any:
				if len(v) > maxSettingValueItems {
					return invalidArgument("a setting value holds too many items")
				}
				for _, item := range v {
					switch entry := item.(type) {
					case string:
						if len(entry) > maxSettingTextLength {
							return invalidArgument("a setting value is too long")
						}
						values.Add(key+"[value][]", entry)
					case float64:
						values.Add(key+"[value][]", strconv.FormatFloat(entry, 'f', -1, 64))
					default:
						return invalidArgument("a setting value list holds texts or numbers only")
					}
				}
			default:
				return invalidArgument("a setting value must be text, a number, a boolean, or a list")
			}
		}
	}
	return nil
}

// needsInstance refuses before any secret is read when the connection does not name the instance.
func needsInstance(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !bound.instance {
		return invalidArgument("this tool needs a connection bound to the instance")
	}
	return nil
}

func invokeSitesAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add website"
	var args siteDefinition
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.SiteName == nil {
		return nil, invalidArgument("siteName is required")
	}
	params, err := args.params()
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, siteRequirement(sitesAdd.ID), "SitesManager.addSite", params, websiteListOnce,
		&answer); err != nil {
		return nil, err
	}
	var created int
	if scalar(answer, &created) != nil || created < 1 {
		return nil, unclearChange(op, "Matomo returned no usable ID for the new website", websiteListOnce)
	}
	return &SiteAdded{IDSite: created, Created: true}, nil
}

func invokeSitesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update website"
	var args struct {
		IDSite *int `json:"idSite"`
		siteDefinition
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params, err := args.params()
	if err != nil {
		return nil, err
	}
	if len(params) == 0 && args.SettingValues == nil {
		return nil, invalidArgument("give at least one value to change")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := siteRequirement(sitesUpdate.ID)
	current := map[string]string{}
	missing := false
	for name, field := range args.listParams() {
		if *field == nil {
			missing = true
			current[name] = ""
		}
	}
	// Matomo resets a comma-separated list that is not sent, so the current value is sent back.
	if missing {
		site, err := client.fetchSite(ctx, op, req, id)
		if err != nil {
			return nil, err
		}
		stored := map[string]string{
			"searchKeywordParameters": site.SiteSearchKeywordParams, "searchCategoryParameters": site.SiteSearchCategoryParams,
			"excludedIps": site.ExcludedIPs, "excludedQueryParameters": site.ExcludedParameters,
			"excludedUserAgents": site.ExcludedUserAgents, "excludedReferrers": site.ExcludedReferrers,
		}
		for name := range current {
			if len(stored[name]) >= maxSettingsText {
				return nil, invalidArgument(name + " is too long to be kept unchanged; give it explicitly")
			}
			params.Set(name, stored[name])
		}
	}
	params.Set("idSite", strconv.Itoa(id))
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "SitesManager.updateSite", params, websiteSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", websiteSentOnce)
	}
	return &SiteUpdated{IDSite: id, Updated: true}, nil
}

func invokeSitesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete website"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.IDSite == nil {
		return nil, invalidArgument("idSite is required")
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := siteRequirement(sitesDelete.ID)
	site, err := client.fetchSite(ctx, op, req, id)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "SitesManager.deleteSite", params, websiteSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", websiteSentOnce)
	}
	return &SiteDeleted{IDSite: id, Name: site.Name, Deleted: true}, nil
}
