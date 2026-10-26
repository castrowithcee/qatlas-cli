package matomo

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxSEOTarget bounds the URL or host name of seo.rank; maxHostName is the DNS limit.
	maxSEOTarget = 2048
	maxHostName  = 253
	maxSEORows   = 50
	// locationProviderPattern is the shape of a geolocation provider ID such as default or geoip2php.
	locationProviderPattern = `^[A-Za-z][A-Za-z0-9_]{0,63}$`
	nonceSchemaPattern      = `^[a-f0-9]{32}$`
)

var (
	locationProviderForm = regexp.MustCompile(locationProviderPattern)
	installNonceForm     = regexp.MustCompile(nonceSchemaPattern)

	unblockSentOnce = "; sent once and never repeated by Qatlas: after an unclear result, check the blocked IP " +
		"addresses in Matomo before repeating it"
)

var seoRank = capability.Descriptor{
	ID: Provider + ".seo.rank", Version: 1, Title: "Get the SEO metrics of a domain",
	Description: "Read SEO metrics of the host of a URL (SEO.getRank), such as the Bing index size and the domain age. It needs a " +
		"view right on any website. Matomo itself asks external SEO services about the host, so the host leaves your " +
		"Matomo instance; only the host is sent to Matomo, and Qatlas never fetches the address. Metrics and labels " +
		"are untrusted text",
	Tags: []string{"matomo", "seo", "rank", "diagnostics"}, Risk: readRisk(instanceSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1,"maxLength":` +
		strconv.Itoa(maxSEOTarget) + `}},"required":["url"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"host":{"type":"string"},"metrics":{"type":"array",` +
		`"items":{"type":"object","properties":{"label":{"type":"string"},"id":{"type":"string"},"rank":{"type":"string"},` +
		`"logo":{"type":"string"},"logo_link":{"type":"string"},"logo_tooltip":{"type":"string"},` +
		`"rank_suffix":{"type":"string"}},"additionalProperties":false}}},"required":["host","metrics"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "url", Required: true, Description: "An http or https URL or a host name; " +
		"credentials in a URL are refused and only the host is used"}},
	Fields: []capability.Field{
		{Name: "host", Description: "The host that was asked for"},
		{Name: "metrics", Description: "One entry per SEO metric Matomo knows"},
		{Name: "metrics[].label", Description: "Name of the metric, untrusted"},
		{Name: "metrics[].id", Description: "ID of the metric, untrusted"},
		{Name: "metrics[].rank", Description: "Value of the metric as text, untrusted; empty when unknown"},
		{Name: "metrics[].logo", Description: "Path of the logo of the metric, untrusted, never fetched"},
		{Name: "metrics[].logo_link", Description: "Address of the source of the metric, untrusted, never fetched"},
		{Name: "metrics[].logo_tooltip", Description: "Tooltip of the metric, untrusted"},
		{Name: "metrics[].rank_suffix", Description: "Unit of the value, untrusted"},
	},
	Examples: []capability.Example{{Description: "Ask for a host", Arguments: json.RawMessage(`{"url":"https://www.example.test/"}`)}},
}

var ipLocationsGet = capability.Descriptor{
	ID: Provider + ".ip-locations.get", Version: 1, Title: "Locate an IP address through Matomo",
	Description: "Ask the geolocation provider of Matomo where an IP address is (UserCountry.getLocationFromIP). It needs " +
		"a view right on any website. The address is required; Matomo's fallback to the address of the request is " +
		"not offered. The address and the location can be personal data. The location is untrusted text",
	Tags: []string{"matomo", "geolocation", "ip", "diagnostics"}, Risk: readRisk(liveSensitivity), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"ip":{"type":"string","minLength":2,"maxLength":45},` +
		`"provider":{"type":"string","pattern":"` + locationProviderPattern + `","maxLength":64}},"required":["ip"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"ip":{"type":"string"},"continent_code":{"type":"string"},` +
		`"continent_name":{"type":"string"},"country_code":{"type":"string"},"country_name":{"type":"string"},` +
		`"region_code":{"type":"string"},"region_name":{"type":"string"},"city_name":{"type":"string"},` +
		`"area_code":{"type":"string"},"lat":{"type":"string"},"long":{"type":"string"},"postal_code":{"type":"string"},` +
		`"isp":{"type":"string"},"org":{"type":"string"}},"required":["ip"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "ip", Required: true, Description: "An IPv4 or IPv6 address without zone"},
		{Name: "provider", Description: "ID of a geolocation provider, such as default or geoip2php; the provider " +
			"configured in Matomo when omitted"},
	},
	Fields: []capability.Field{
		{Name: "ip", Description: "The address that was located, as sent"},
		{Name: "continent_code", Description: "Continent code, untrusted; only what the provider knows"},
		{Name: "continent_name", Description: "Continent name, untrusted"},
		{Name: "country_code", Description: "Country code, untrusted"},
		{Name: "country_name", Description: "Country name, untrusted"},
		{Name: "region_code", Description: "Region code, untrusted"},
		{Name: "region_name", Description: "Region name, untrusted"},
		{Name: "city_name", Description: "City name, untrusted"},
		{Name: "area_code", Description: "Area code, untrusted"},
		{Name: "lat", Description: "Latitude as text, untrusted"},
		{Name: "long", Description: "Longitude as text, untrusted"},
		{Name: "postal_code", Description: "Postal code, untrusted"},
		{Name: "isp", Description: "Internet provider, untrusted"},
		{Name: "org", Description: "Organisation, untrusted"},
	},
	Examples: []capability.Example{{Description: "Locate a documentation address", Arguments: json.RawMessage(`{"ip":"192.0.2.1"}`)}},
}

var jsTrackerInstallGetStatus = capability.Descriptor{
	ID: Provider + ".js-tracker-install.get-status", Version: 1, Title: "Get the JavaScript tracker install test result",
	Description: "Read whether Matomo recorded a successful test request of the JavaScript tracking code for a website " +
		"(JsTrackerInstallCheck.wasJsTrackerInstallTestSuccessful) and the main URL of the website. It needs a view " +
		"right on the website. Qatlas does not start the test; it only reads the result of one started elsewhere. " +
		"The main URL is untrusted text and never fetched",
	Tags: []string{"matomo", "tracking", "install", "diagnostics"}, Risk: readRisk(sitesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,"nonce":{"type":"string",` +
		`"pattern":"` + nonceSchemaPattern + `","maxLength":32}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"isSuccess":{"type":"boolean"},` +
		`"mainUrl":{"type":"string"}},"required":["idSite","isSuccess"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), {Name: "nonce", Description: "The 32 lower-case hexadecimal " +
		"characters of a started test; the latest result of the website when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the result belongs to"},
		{Name: "isSuccess", Description: "True when a test request was recorded, for the nonce when one was given"},
		{Name: "mainUrl", Description: "Main URL of the website, untrusted, never fetched"},
	},
	Examples: []capability.Example{{Description: "Read the latest result of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var bruteForceIPsUnblock = capability.Descriptor{
	ID: Provider + ".brute-force-ips.unblock", Version: 1, Title: "Unblock all brute-force blocked IPs of Matomo",
	Description: "Lift every block that Matomo's brute-force protection currently holds (Login.unblockBruteForceIPs), " +
		"for all addresses at once, attackers included; it takes no address and cannot lift single blocks. Blocked " +
		"addresses can be blocked again by new failed logins. A call with nothing blocked changes nothing." +
		operationsNote + "; offered only where a connection's tools list names it" + unblockSentOnce,
	Tags: []string{"matomo", "login", "brute-force", "security", "update", "instance"},
	Risk: operationsRisk(capability.EffectUpdate), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"unblocked":{"type":"boolean"}},"required":["unblocked"],` +
		`"additionalProperties":false}`),
	Fields:   []capability.Field{{Name: "unblocked", Description: "True once Matomo accepted the call"}},
	Examples: []capability.Example{{Description: "Lift all blocks", Arguments: json.RawMessage(`{}`)}},
}

// SEOMetric is one metric of seo.rank. All text is untrusted provider data.
type SEOMetric struct {
	Label       string `json:"label,omitempty"`
	ID          string `json:"id,omitempty"`
	Rank        string `json:"rank,omitempty"`
	Logo        string `json:"logo,omitempty"`
	LogoLink    string `json:"logo_link,omitempty"`
	LogoTooltip string `json:"logo_tooltip,omitempty"`
	RankSuffix  string `json:"rank_suffix,omitempty"`
}

// SEORankResult is the answer of seo.rank.
type SEORankResult struct {
	Host    string      `json:"host"`
	Metrics []SEOMetric `json:"metrics"`
}

// IPLocation is the answer of ip-locations.get. All text is untrusted provider data.
type IPLocation struct {
	IP            string `json:"ip"`
	ContinentCode string `json:"continent_code,omitempty"`
	ContinentName string `json:"continent_name,omitempty"`
	CountryCode   string `json:"country_code,omitempty"`
	CountryName   string `json:"country_name,omitempty"`
	RegionCode    string `json:"region_code,omitempty"`
	RegionName    string `json:"region_name,omitempty"`
	CityName      string `json:"city_name,omitempty"`
	AreaCode      string `json:"area_code,omitempty"`
	Lat           string `json:"lat,omitempty"`
	Long          string `json:"long,omitempty"`
	PostalCode    string `json:"postal_code,omitempty"`
	ISP           string `json:"isp,omitempty"`
	Org           string `json:"org,omitempty"`
}

// JSTrackerInstallStatus is the answer of js-tracker-install.get-status.
type JSTrackerInstallStatus struct {
	IDSite    int    `json:"idSite"`
	IsSuccess bool   `json:"isSuccess"`
	MainURL   string `json:"mainUrl,omitempty"`
}

// BruteForceIPsUnblocked is the answer of brute-force-ips.unblock.
type BruteForceIPsUnblocked struct {
	Unblocked bool `json:"unblocked"`
}

// seoHost reduces an http(s) URL or a host name to a lower-case ASCII host name. Credentials, other schemes,
// and anything that is not a plain host name are refused.
func seoHost(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > maxSEOTarget || strings.ContainsAny(text, " \t\r\n\\") {
		return "", false
	}
	host := text
	if strings.Contains(text, "://") {
		parsed, err := url.Parse(text)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return "", false
		}
		host = parsed.Hostname()
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || len(host) > maxHostName || !hostnameForm.MatchString(host) {
		return "", false
	}
	return host, true
}

func invokeSEORank(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get SEO rank"
	var args struct {
		URL string `json:"url"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	host, valid := seoHost(args.URL)
	if !valid {
		return nil, invalidArgument("url must be an http or https URL without credentials, or a host name")
	}
	if err := needsWebsites(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var rows []row
	if err := client.call(ctx, op, baseRequirement(seoRank.ID), "SEO.getRank", url.Values{"url": {host}}, &rows); err != nil {
		return nil, err
	}
	if len(rows) > maxSEORows {
		return nil, provider.InvalidResponse(op, "Matomo returned too many SEO metrics")
	}
	metrics := make([]SEOMetric, 0, len(rows))
	for _, one := range rows {
		metrics = append(metrics, SEOMetric{Label: one.text("label", maxStringLength), ID: one.text("id", maxStringLength),
			Rank: one.text("rank", maxStringLength), Logo: one.text("logo", maxUrlLength),
			LogoLink: one.text("logo_link", maxUrlLength), LogoTooltip: one.text("logo_tooltip", maxStringLength),
			RankSuffix: one.text("rank_suffix", maxStringLength)})
	}
	return &SEORankResult{Host: host, Metrics: metrics}, nil
}

// needsWebsites refuses before any secret is read when the connection is bound to no website.
func needsWebsites(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !bound.reachesSites() {
		return invalidArgument("this connection is bound to no website")
	}
	return nil
}

func invokeIPLocationsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "locate IP address"
	var args struct {
		IP       string `json:"ip"`
		Provider string `json:"provider"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(args.IP))
	if err != nil || addr.Zone() != "" {
		return nil, invalidArgument("ip must be an IPv4 or IPv6 address")
	}
	if args.Provider != "" && !locationProviderForm.MatchString(args.Provider) {
		return nil, invalidArgument("provider must be a geolocation provider ID of letters, digits, and underscores")
	}
	if err := needsWebsites(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"ip": {addr.String()}}
	if args.Provider != "" {
		params.Set("provider", args.Provider)
	}
	var found row
	if err := client.call(ctx, op, baseRequirement(ipLocationsGet.ID), "UserCountry.getLocationFromIP", params, &found); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid location")
	}
	return &IPLocation{IP: addr.String(), ContinentCode: found.text("continent_code", maxStringLength),
		ContinentName: found.text("continent_name", maxStringLength), CountryCode: found.text("country_code", maxStringLength),
		CountryName: found.text("country_name", maxStringLength), RegionCode: found.text("region_code", maxStringLength),
		RegionName: found.text("region_name", maxStringLength), CityName: found.text("city_name", maxStringLength),
		AreaCode: found.text("area_code", maxStringLength), Lat: found.text("lat", maxStringLength),
		Long: found.text("long", maxStringLength), PostalCode: found.text("postal_code", maxStringLength),
		ISP: found.text("isp", maxStringLength), Org: found.text("org", maxStringLength)}, nil
}

func invokeJSTrackerInstallGetStatus(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get JavaScript tracker install status"
	var args struct {
		IDSite *int   `json:"idSite"`
		Nonce  string `json:"nonce"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.Nonce != "" && !installNonceForm.MatchString(args.Nonce) {
		return nil, invalidArgument("nonce must be 32 lower-case hexadecimal characters")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if args.Nonce != "" {
		params.Set("nonce", args.Nonce)
	}
	var found row
	if err := client.call(ctx, op, baseRequirement(jsTrackerInstallGetStatus.ID),
		"JsTrackerInstallCheck.wasJsTrackerInstallTestSuccessful", params, &found); err != nil {
		return nil, err
	}
	success := found.get("isSuccess").flag()
	if success == nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid install status")
	}
	return &JSTrackerInstallStatus{IDSite: id, IsSuccess: *success, MainURL: found.text("mainUrl", maxUrlLength)}, nil
}

func invokeBruteForceIPsUnblock(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "unblock brute-force IPs"
	var args struct{}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := onlyKeys(raw); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, baseRequirement(bruteForceIPsUnblock.ID), "Login.unblockBruteForceIPs", url.Values{},
		unblockSentOnce, &answer); err != nil {
		return nil, err
	}
	if !confirmedByMatomo(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the change", unblockSentOnce)
	}
	return &BruteForceIPsUnblocked{Unblocked: true}, nil
}

func diagnosticsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: seoRank, Handler: capability.Handler(invokeSEORank)},
		{Descriptor: ipLocationsGet, Handler: capability.Handler(invokeIPLocationsGet)},
		{Descriptor: jsTrackerInstallGetStatus, Handler: capability.Handler(invokeJSTrackerInstallGetStatus)},
		{Descriptor: bruteForceIPsUnblock, Handler: capability.Handler(invokeBruteForceIPsUnblock)},
	}
}
