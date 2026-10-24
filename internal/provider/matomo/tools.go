package matomo

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	instanceSensitive = "matomo-instance-metadata"
	sitesSensitive    = "matomo-websites"
)

// readRisk is the risk of every read: safe, no confirmation, and open to content that visitors and users of
// the instance wrote.
func readRisk(sensitivity string) capability.Risk {
	return capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: sensitivity}
}

const pluginPattern = `^[A-Za-z][A-Za-z0-9_]{0,63}$`

var versionGet = capability.Descriptor{
	ID: Provider + ".version.get", Version: 1, Title: "Get the Matomo version",
	Description: "Read the version of the Matomo instance and, when plugin is given, whether that plugin is " +
		"activated; both need a view right on any website",
	Tags: []string{"matomo", "version", "plugin", "instance"}, Risk: readRisk(instanceSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"plugin":{"type":"string","pattern":"` +
		pluginPattern + `","maxLength":64}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"version":{"type":"string"},` +
		`"plugin":{"type":"object","properties":{"name":{"type":"string"},"activated":{"type":"boolean"}},` +
		`"required":["name","activated"],"additionalProperties":false}},` +
		`"required":["version"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "plugin", Description: "Name of a plugin, such as Goals or Ecommerce, " +
		"whose activation is also read"}},
	Fields: []capability.Field{
		{Name: "version", Description: "Version as the instance reports it, untrusted"},
		{Name: "plugin", Description: "Activation of the plugin asked for, absent when plugin was not given"},
		{Name: "plugin.name", Description: "The plugin asked for"},
		{Name: "plugin.activated", Description: "True when the plugin is activated; a premium plugin that is not " +
			"installed is not activated"},
	},
	Examples: []capability.Example{
		{Description: "Read the version", Arguments: json.RawMessage(`{}`)},
		{Description: "Check whether Goals is activated", Arguments: json.RawMessage(`{"plugin":"Goals"}`)},
	},
}

// VersionResult is the answer of version.get.
type VersionResult struct {
	Version string        `json:"version"`
	Plugin  *PluginResult `json:"plugin,omitempty"`
}

// PluginResult is the activation of one plugin.
type PluginResult struct {
	Name      string `json:"name"`
	Activated bool   `json:"activated"`
}

func invokeVersionGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get version"
	var args struct {
		Plugin string `json:"plugin"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.Plugin != "" && !validPlugin(args.Plugin) {
		return nil, invalidArgument("plugin must be a plugin name of letters, digits, and underscores")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// The tool asks the instance itself instead of the held value, so a changed release shows at once.
	req := baseRequirement(versionGet.ID)
	info, err := client.instance(ctx, op, true)
	if err != nil {
		return nil, err
	}
	if err := client.supports(info, req); err != nil {
		return nil, err
	}
	result := &VersionResult{Version: info.version.String()}
	if args.Plugin != "" {
		active, err := client.pluginActive(ctx, op, requirement{tool: versionGet.ID, plugin: args.Plugin}, true)
		if err != nil {
			return nil, err
		}
		result.Plugin = &PluginResult{Name: args.Plugin, Activated: active}
	}
	return result, nil
}

func validPlugin(name string) bool {
	if name == "" || len(name) > 64 || !(name[0] >= 'A' && name[0] <= 'Z' || name[0] >= 'a' && name[0] <= 'z') {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

const siteProperties = `"id":{"type":"integer"},"name":{"type":"string"},"main_url":{"type":"string"},` +
	`"urls":{"type":"array","items":{"type":"string"}},"ts_created":{"type":"string"},` +
	`"ecommerce":{"type":"boolean"},"sitesearch":{"type":"boolean"},` +
	`"sitesearch_keyword_parameters":{"type":"string"},"sitesearch_category_parameters":{"type":"string"},` +
	`"timezone":{"type":"string"},"timezone_name":{"type":"string"},"currency":{"type":"string"},` +
	`"currency_name":{"type":"string"},"exclude_unknown_urls":{"type":"boolean"},"excluded_ips":{"type":"string"},` +
	`"excluded_parameters":{"type":"string"},"excluded_user_agents":{"type":"string"},` +
	`"excluded_referrers":{"type":"string"},"group":{"type":"string"},"type":{"type":"string"},` +
	`"keep_url_fragment":{"type":"integer"},"creator_login":{"type":"string"}`

func siteFields(prefix string, withURLs bool) []capability.Field {
	fields := []capability.Field{
		{Name: prefix + "id", Description: "Website ID, used as idSite by the other tools"},
		{Name: prefix + "name", Description: "Website name, untrusted"},
		{Name: prefix + "main_url", Description: "Main address of the website, untrusted text that is never fetched"},
		{Name: prefix + "ts_created", Description: "Creation time as Matomo reports it (UTC)"},
		{Name: prefix + "ecommerce", Description: "True when ecommerce tracking is enabled"},
		{Name: prefix + "sitesearch", Description: "True when site search tracking is enabled"},
		{Name: prefix + "sitesearch_keyword_parameters", Description: "Query parameters that carry the site search " +
			"keyword, comma separated, untrusted"},
		{Name: prefix + "sitesearch_category_parameters", Description: "Query parameters that carry the site search " +
			"category, comma separated, untrusted"},
		{Name: prefix + "timezone", Description: "Time zone of the website, such as Europe/Berlin"},
		{Name: prefix + "timezone_name", Description: "Display name of the time zone, untrusted"},
		{Name: prefix + "currency", Description: "Currency code of the website"},
		{Name: prefix + "currency_name", Description: "Display name of the currency, untrusted"},
		{Name: prefix + "exclude_unknown_urls", Description: "True when hits to addresses outside the website's " +
			"addresses are ignored"},
		{Name: prefix + "excluded_ips", Description: "IP addresses and ranges excluded from tracking, comma " +
			"separated, untrusted"},
		{Name: prefix + "excluded_parameters", Description: "URL parameters excluded from tracked addresses, " +
			"comma separated, untrusted"},
		{Name: prefix + "excluded_user_agents", Description: "User agent fragments excluded from tracking, comma " +
			"separated, untrusted"},
		{Name: prefix + "excluded_referrers", Description: "Referrer addresses excluded from tracking, comma " +
			"separated, untrusted"},
		{Name: prefix + "group", Description: "Group label of the website, untrusted"},
		{Name: prefix + "type", Description: "Website type, such as website or intranet"},
		{Name: prefix + "keep_url_fragment", Description: "How URL fragments are tracked: 0 follows the global " +
			"setting, 1 keeps them, 2 removes them"},
		{Name: prefix + "creator_login", Description: "Login of the user who created the website, untrusted"},
	}
	if withURLs {
		fields = append(fields, capability.Field{Name: prefix + "urls", Description: "All addresses of the website, " +
			"main address first, at most 100, untrusted text that is never fetched"})
	}
	return fields
}

const maxPatternSchema = `"pattern":{"type":"string","minLength":1,"maxLength":100}`

var sitesList = capability.Descriptor{
	ID: Provider + ".sites.list", Version: 1, Title: "List Matomo websites",
	Description: "List the websites the token can view, inside the connection's targets, in batches ordered by " +
		"ID; pattern keeps the websites whose name, address, or group matches; names and addresses are untrusted",
	Tags: []string{"matomo", "sites", "websites", "list"}, Risk: readRisk(sitesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + maxPatternSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"sites":{"type":"array","items":` +
		`{"type":"object","properties":{` + siteProperties + `},"required":["id"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["sites","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "pattern", Description: "Text a website's name, address, or " +
		"group must contain, up to 100 characters; all websites when omitted"}}, pagingArguments...),
	Fields: append(append([]capability.Field{{Name: "sites", Description: "Websites ordered by id"}},
		siteFields("sites[].", false)...), pagingFields...),
	Examples: []capability.Example{{Description: "List the first websites", Arguments: json.RawMessage(`{}`)}},
}

var sitesGet = capability.Descriptor{
	ID: Provider + ".sites.get", Version: 1, Title: "Get a Matomo website",
	Description: "Read one website with its name, addresses, time zone, currency, and tracking settings; a " +
		"view right on it is enough; names and addresses are untrusted",
	Tags: []string{"matomo", "sites", "websites", "get"}, Risk: readRisk(sitesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer","minimum":1,` +
		`"maximum":999999999}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + siteProperties +
		`},"required":["id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "idSite", Description: "Website ID from matomo.sites.list; optional " +
		"when the connection binds exactly one website; must lie inside the connection's targets",
		Target: "site"}},
	Fields:   siteFields("", true),
	Examples: []capability.Example{{Description: "Read website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}
