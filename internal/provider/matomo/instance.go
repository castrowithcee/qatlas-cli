package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxGlobalList bounds one comma separated global setting; Matomo keeps such lists short.
	maxGlobalList = 8192
	// maxRawJSON bounds a nested provider structure that is passed on as it is.
	maxRawJSON = 16384
	// maxIPRange bounds the text of an IP range argument.
	maxIPRange = 64
)

// globalSection is one global setting: its name, the fixed method, and how the answer is read.
type globalSection struct {
	name, method string
	// flag marks a boolean answer; siteBound marks a method that takes the optional idSite.
	flag, siteBound bool
}

// globalSections lists the settings in the order a call without section reads them.
var globalSections = []globalSection{
	{name: "excluded-ips", method: "SitesManager.getExcludedIpsGlobal"},
	{name: "excluded-user-agents", method: "SitesManager.getExcludedUserAgentsGlobal"},
	{name: "excluded-referrers", method: "SitesManager.getExcludedReferrersGlobal"},
	{name: "excluded-query-parameters", method: "SitesManager.getExcludedQueryParametersGlobal", siteBound: true},
	{name: "query-parameter-exclusion-type", method: "SitesManager.getExclusionTypeForQueryParams", siteBound: true},
	{name: "search-keyword-parameters", method: "SitesManager.getSearchKeywordParametersGlobal"},
	{name: "search-category-parameters", method: "SitesManager.getSearchCategoryParametersGlobal"},
	{name: "keep-url-fragments", method: "SitesManager.getKeepURLFragmentsGlobal", flag: true},
	{name: "default-currency", method: "SitesManager.getDefaultCurrency"},
	{name: "default-timezone", method: "SitesManager.getDefaultTimezone"},
}

func sectionNames() []string {
	names := make([]string, len(globalSections))
	for i, s := range globalSections {
		names[i] = s.name
	}
	return names
}

func quotedList(values []string) string { return `"` + strings.Join(values, `","`) + `"` }

var globalSettingsGet = capability.Descriptor{
	ID: Provider + ".global-settings.get", Version: 1, Title: "Get the global Matomo website settings",
	Description: "Read the instance-wide defaults for websites: excluded IPs, user agents, referrers, and query " +
		"parameters, site search parameters, URL fragment handling, default currency and time zone. One section " +
		"is one Matomo request; without section all ten are read one after another. Most sections need an admin " +
		"right on some website, the query parameter sections a view right; a connection bound to the instance " +
		"target is required; values are untrusted",
	Tags: []string{"matomo", "settings", "global", "instance", "exclusions"}, Risk: readRisk(instanceSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string","enum":[` +
		quotedList(sectionNames()) + `]},"idSite":{"type":"integer","minimum":1,"maximum":999999999}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"excluded_ips":{"type":"string"},` +
		`"excluded_user_agents":{"type":"string"},"excluded_referrers":{"type":"string"},` +
		`"excluded_query_parameters":{"type":"string"},"query_parameter_exclusion_type":{"type":"string"},` +
		`"search_keyword_parameters":{"type":"string"},"search_category_parameters":{"type":"string"},` +
		`"keep_url_fragments":{"type":"boolean"},"default_currency":{"type":"string"},` +
		`"default_timezone":{"type":"string"}},"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "section", Description: "One setting to read: " + strings.Join(sectionNames(), ", ") +
			"; all of them when omitted"},
		{Name: "idSite", Description: "Website ID for the site-specific filtering of excluded-query-parameters and " +
			"query-parameter-exclusion-type; must lie inside the connection's targets; the global value when omitted",
			Target: "site"},
	},
	Fields: []capability.Field{
		{Name: "excluded_ips", Description: "IPs and ranges excluded on all websites, comma separated, untrusted; empty when none"},
		{Name: "excluded_user_agents", Description: "User agent fragments excluded on all websites, comma separated, untrusted"},
		{Name: "excluded_referrers", Description: "Referrer hosts excluded on all websites, comma separated, untrusted"},
		{Name: "excluded_query_parameters", Description: "URL query parameters removed from tracked addresses, comma " +
			"separated; follows the exclusion type (empty for common session parameters), untrusted"},
		{Name: "query_parameter_exclusion_type", Description: "Exclusion type name Matomo reports for query parameters"},
		{Name: "search_keyword_parameters", Description: "Default site search keyword parameter names, comma separated, untrusted"},
		{Name: "search_category_parameters", Description: "Default site search category parameter names, comma " +
			"separated, untrusted; empty when not configured"},
		{Name: "keep_url_fragments", Description: "True when URL fragments are kept by default"},
		{Name: "default_currency", Description: "Currency code used when websites are created"},
		{Name: "default_timezone", Description: "Time zone identifier used when websites are created"},
	},
	Examples: []capability.Example{
		{Description: "Read all global settings", Arguments: json.RawMessage(`{}`)},
		{Description: "Read the excluded IPs", Arguments: json.RawMessage(`{"section":"excluded-ips"}`)},
	},
}

// GlobalSettingsResult is the answer of global-settings.get; a member is present when its section was read.
type GlobalSettingsResult struct {
	ExcludedIPs                 *string `json:"excluded_ips,omitempty"`
	ExcludedUserAgents          *string `json:"excluded_user_agents,omitempty"`
	ExcludedReferrers           *string `json:"excluded_referrers,omitempty"`
	ExcludedQueryParameters     *string `json:"excluded_query_parameters,omitempty"`
	QueryParameterExclusionType *string `json:"query_parameter_exclusion_type,omitempty"`
	SearchKeywordParameters     *string `json:"search_keyword_parameters,omitempty"`
	SearchCategoryParameters    *string `json:"search_category_parameters,omitempty"`
	KeepURLFragments            *bool   `json:"keep_url_fragments,omitempty"`
	DefaultCurrency             *string `json:"default_currency,omitempty"`
	DefaultTimezone             *string `json:"default_timezone,omitempty"`
}

func (r *GlobalSettingsResult) set(name string, text string, flag *bool) {
	switch name {
	case "excluded-ips":
		r.ExcludedIPs = &text
	case "excluded-user-agents":
		r.ExcludedUserAgents = &text
	case "excluded-referrers":
		r.ExcludedReferrers = &text
	case "excluded-query-parameters":
		r.ExcludedQueryParameters = &text
	case "query-parameter-exclusion-type":
		r.QueryParameterExclusionType = &text
	case "search-keyword-parameters":
		r.SearchKeywordParameters = &text
	case "search-category-parameters":
		r.SearchCategoryParameters = &text
	case "keep-url-fragments":
		r.KeepURLFragments = flag
	case "default-currency":
		r.DefaultCurrency = &text
	case "default-timezone":
		r.DefaultTimezone = &text
	}
}

func invokeGlobalSettingsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get global settings"
	var args struct {
		Section *string `json:"section"`
		IDSite  *int    `json:"idSite"`
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
	chosen := globalSections
	if args.Section != nil {
		index := slices.IndexFunc(globalSections, func(s globalSection) bool { return s.name == *args.Section })
		if index < 0 {
			return nil, invalidArgument("section must be one of " + strings.Join(sectionNames(), ", "))
		}
		chosen = globalSections[index : index+1]
	}
	if args.IDSite != nil {
		if *args.IDSite < 1 || len(strconv.Itoa(*args.IDSite)) > maxIDDigits {
			return nil, invalidArgument("idSite must be a positive integer")
		}
		if !bound.allows(*args.IDSite) {
			return nil, invalidArgument(outsideTargets)
		}
		if !slices.ContainsFunc(chosen, func(s globalSection) bool { return s.siteBound }) {
			return nil, invalidArgument("idSite applies only to excluded-query-parameters and query-parameter-exclusion-type")
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(globalSettingsGet.ID)
	result := &GlobalSettingsResult{}
	for _, section := range chosen {
		params := url.Values{}
		if section.siteBound && args.IDSite != nil {
			params.Set("idSite", strconv.Itoa(*args.IDSite))
		}
		var answer json.RawMessage
		if err := client.call(ctx, op, req, section.method, params, &answer); err != nil {
			return nil, err
		}
		if section.flag {
			var value loose
			if value.UnmarshalJSON(answer) != nil || value.flag() == nil {
				return nil, provider.InvalidResponse(op, "Matomo returned an invalid setting")
			}
			result.set(section.name, "", value.flag())
			continue
		}
		// Matomo answers false for a list that was never set.
		trimmed := bytes.TrimSpace(answer)
		text := ""
		if !bytes.Equal(trimmed, []byte("false")) {
			var value loose
			if value.UnmarshalJSON(trimmed) != nil {
				return nil, provider.InvalidResponse(op, "Matomo returned an invalid setting")
			}
			text = value.str(maxGlobalList)
		}
		result.set(section.name, text, nil)
	}
	return result, nil
}

var referenceKinds = []string{"currencies", "currency-symbols", "timezones", "timezone-name", "timezone-support",
	"measurable-types", "country-codes"}

var referenceDataList = capability.Descriptor{
	ID: Provider + ".reference-data.list", Version: 1, Title: "List Matomo reference data",
	Description: "Read one reference list of the instance: currencies, currency symbols, time zones, the label of one " +
		"time zone, whether time zone support is enabled, the measurable types, or the country codes. A view right " +
		"on some website is enough; lists are paged and sorted, names are translated by Matomo and untrusted",
	Tags: []string{"matomo", "reference", "currency", "timezone", "country", "list"}, Risk: readRisk(instanceSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":[` +
		quotedList(referenceKinds) + `]},"timezone":{"type":"string","minLength":1,"maxLength":64},` +
		`"countryCode":{"type":"string","pattern":"^[A-Z]{2}$"},"multipleTimezonesInCountry":{"type":"boolean"},` +
		pagingKeys + `},"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},` +
		`"entries":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"name":{"type":"string"},"group":{"type":"string"}},"required":["id","name"],"additionalProperties":false}},` +
		`"measurable_types":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"name":{"type":"string"},"description":{"type":"string"},"long_description":{"type":"string"},` +
		`"how_to_setup_url":{"type":"string"},"settings":{}},"required":["id"],"additionalProperties":false}},` +
		`"timezone":{"type":"string"},"timezone_name":{"type":"string"},"timezone_support":{"type":"boolean"},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["kind"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "kind", Required: true, Description: "List to read: " + strings.Join(referenceKinds, ", ")},
		{Name: "timezone", Description: "Time zone identifier such as Asia/Tokyo, up to 64 characters; required for " +
			"timezone-name and refused for other kinds"},
		{Name: "countryCode", Description: "Upper-case two-letter country code for timezone-name; looked up by Matomo when omitted"},
		{Name: "multipleTimezonesInCountry", Description: "For timezone-name: whether the country has several time " +
			"zones; looked up by Matomo when omitted"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "kind", Description: "The list read"},
		{Name: "entries", Description: "Entries of currencies (id is the code, name the name with symbol), " +
			"currency-symbols (name is the symbol), timezones (id is the identifier, name the label, group the " +
			"continent or UTC), and country-codes (id is the lowercase ISO code, name the country); sorted by group and id"},
		{Name: "measurable_types", Description: "Measurable types with their settings as Matomo describes them, untrusted"},
		{Name: "timezone", Description: "The time zone asked for with timezone-name"},
		{Name: "timezone_name", Description: "Label Matomo gives that time zone, untrusted"},
		{Name: "timezone_support", Description: "True when the server supports time zones; present for timezone-support"},
	}, pagingFields...),
	Examples: []capability.Example{
		{Description: "List currencies", Arguments: json.RawMessage(`{"kind":"currencies","limit":50}`)},
		{Description: "Label a time zone", Arguments: json.RawMessage(`{"kind":"timezone-name","timezone":"Asia/Tokyo"}`)},
	},
}

// ReferenceEntry is one row of a reference list.
type ReferenceEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group,omitempty"`
}

// MeasurableType is one measurable type as Matomo describes it.
type MeasurableType struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	Description     string `json:"description,omitempty"`
	LongDescription string `json:"long_description,omitempty"`
	HowToSetupURL   string `json:"how_to_setup_url,omitempty"`
	Settings        any    `json:"settings,omitempty"`
}

// ReferenceDataResult is the answer of reference-data.list.
type ReferenceDataResult struct {
	Kind            string           `json:"kind"`
	Entries         []ReferenceEntry `json:"entries,omitempty"`
	MeasurableTypes []MeasurableType `json:"measurable_types,omitempty"`
	Timezone        string           `json:"timezone,omitempty"`
	TimezoneName    *string          `json:"timezone_name,omitempty"`
	TimezoneSupport *bool            `json:"timezone_support,omitempty"`
	HasMore         bool             `json:"has_more"`
	NextCursor      string           `json:"next_cursor,omitempty"`
}

func isCountryCode(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

func invokeReferenceDataList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list reference data"
	var args struct {
		Kind                       string  `json:"kind"`
		Timezone                   *string `json:"timezone"`
		CountryCode                *string `json:"countryCode"`
		MultipleTimezonesInCountry *bool   `json:"multipleTimezonesInCountry"`
		Limit                      int     `json:"limit"`
		Cursor                     string  `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if !slices.Contains(referenceKinds, args.Kind) {
		return nil, invalidArgument("kind must be one of " + strings.Join(referenceKinds, ", "))
	}
	named := args.Kind == "timezone-name"
	if (args.Timezone != nil || args.CountryCode != nil || args.MultipleTimezonesInCountry != nil) && !named {
		return nil, invalidArgument("timezone, countryCode, and multipleTimezonesInCountry apply only to timezone-name")
	}
	params := url.Values{}
	timezone := ""
	if named {
		if args.Timezone == nil {
			return nil, invalidArgument("timezone is required for timezone-name")
		}
		timezone = strings.TrimSpace(*args.Timezone)
		if timezone == "" || len(timezone) > 64 || strings.ContainsAny(timezone, " \t\r\n\x00") {
			return nil, invalidArgument("timezone must be an identifier of at most 64 characters")
		}
		params.Set("timezone", timezone)
		if args.CountryCode != nil {
			if !isCountryCode(*args.CountryCode) {
				return nil, invalidArgument("countryCode must be two upper-case letters")
			}
			params.Set("countryCode", *args.CountryCode)
		}
		if args.MultipleTimezonesInCountry != nil {
			params.Set("multipleTimezonesInCountry", strconv.FormatBool(*args.MultipleTimezonesInCountry))
		}
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	sites := slices.Clone(bound.sites)
	slices.Sort(sites)
	paging, err := startPage(provider.CursorBinding(referenceDataList.ID, bound.allSites, sites, args.Kind), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(referenceDataList.ID)
	result := &ReferenceDataResult{Kind: args.Kind}
	switch args.Kind {
	case "currencies", "currency-symbols", "country-codes":
		method := map[string]string{"currencies": "SitesManager.getCurrencyList",
			"currency-symbols": "SitesManager.getCurrencySymbols", "country-codes": "UserCountry.getCountryCodeMapping"}[args.Kind]
		if args.Kind == "country-codes" {
			req.plugin = "UserCountry"
		}
		var table map[string]loose
		if err := client.call(ctx, op, req, method, url.Values{}, &table); err != nil {
			return nil, err
		}
		entries := make([]ReferenceEntry, 0, len(table))
		for id, name := range table {
			entries = append(entries, ReferenceEntry{ID: bounded(id), Name: name.str(maxStringLength)})
		}
		result.Entries, result.NextCursor = pageEntries(paging, entries)
	case "timezones":
		var table map[string]map[string]loose
		if err := client.call(ctx, op, req, "SitesManager.getTimezonesList", url.Values{}, &table); err != nil {
			return nil, err
		}
		var entries []ReferenceEntry
		for group, zones := range table {
			for id, name := range zones {
				entries = append(entries, ReferenceEntry{ID: bounded(id), Name: name.str(maxStringLength), Group: bounded(group)})
			}
		}
		result.Entries, result.NextCursor = pageEntries(paging, entries)
	case "timezone-name":
		var label loose
		if err := client.call(ctx, op, req, "SitesManager.getTimezoneName", params, &label); err != nil {
			return nil, err
		}
		name := label.str(maxStringLength)
		result.Timezone, result.TimezoneName = timezone, &name
	case "timezone-support":
		var value loose
		if err := client.call(ctx, op, req, "SitesManager.isTimezoneSupportEnabled", url.Values{}, &value); err != nil || value.flag() == nil {
			if err == nil {
				err = provider.InvalidResponse(op, "Matomo returned an invalid answer")
			}
			return nil, err
		}
		result.TimezoneSupport = value.flag()
	case "measurable-types":
		var types []struct {
			ID              *loose          `json:"id"`
			Name            *loose          `json:"name"`
			Description     *loose          `json:"description"`
			LongDescription *loose          `json:"longDescription"`
			HowToSetupURL   *loose          `json:"howToSetupUrl"`
			Settings        json.RawMessage `json:"settings"`
		}
		if err := client.call(ctx, op, req, "API.getAvailableMeasurableTypes", url.Values{}, &types); err != nil {
			return nil, err
		}
		rows := make([]MeasurableType, 0, len(types))
		for _, t := range types {
			rows = append(rows, MeasurableType{ID: t.ID.str(maxStringLength), Name: t.Name.str(maxStringLength),
				Description: t.Description.str(maxGlobalList), LongDescription: t.LongDescription.str(maxGlobalList),
				HowToSetupURL: t.HowToSetupURL.str(maxUrlLength), Settings: boundedJSON(t.Settings)})
		}
		batch, next := slice(paging, rows)
		result.MeasurableTypes, result.NextCursor = batch, next
	}
	result.HasMore = result.NextCursor != ""
	return result, nil
}

// pageEntries sorts the rows by group and ID, since a JSON object has no stable order, and cuts the batch.
func pageEntries(p page, entries []ReferenceEntry) ([]ReferenceEntry, string) {
	slices.SortFunc(entries, func(a, b ReferenceEntry) int {
		if c := strings.Compare(a.Group, b.Group); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	batch, next := slice(p, entries)
	if batch == nil {
		batch = []ReferenceEntry{}
	}
	return batch, next
}

// boundedJSON passes a nested provider structure on as it is, or as cut text when it is too large.
func boundedJSON(raw json.RawMessage) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || !json.Valid(trimmed) {
		return nil
	}
	if len(trimmed) > maxRawJSON {
		return boundString(string(trimmed), maxRawJSON)
	}
	return json.RawMessage(trimmed)
}

var ipRangesExpand = capability.Descriptor{
	ID: Provider + ".ip-ranges.expand", Version: 1, Title: "Expand an IP range",
	Description: "Compute the lowest and highest address of an IP range given as an address or in CIDR notation, " +
		"IPv4 or IPv6, through Matomo. The method checks no right and changes nothing; the range is not stored",
	Tags: []string{"matomo", "ip", "range", "cidr", "helper"}, Risk: readRisk(instanceSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"ipRange":{"type":"string","minLength":2,` +
		`"maxLength":64}},"required":["ipRange"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"ip_range":{"type":"string"},` +
		`"low":{"type":"string"},"high":{"type":"string"}},"required":["ip_range","low","high"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "ipRange", Required: true, Description: "An IP address or a range in CIDR notation such " +
		"as 192.168.1.0/24, up to 64 characters; wildcards and dashes are refused"}},
	Fields: []capability.Field{
		{Name: "ip_range", Description: "The range as sent"},
		{Name: "low", Description: "Lowest address of the range"},
		{Name: "high", Description: "Highest address of the range"},
	},
	Examples: []capability.Example{{Description: "Expand a range", Arguments: json.RawMessage(`{"ipRange":"192.168.1.0/24"}`)}},
}

// IPRangeResult is the answer of ip-ranges.expand.
type IPRangeResult struct {
	IPRange string `json:"ip_range"`
	Low     string `json:"low"`
	High    string `json:"high"`
}

func invokeIPRangesExpand(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "expand IP range"
	var args struct {
		IPRange string `json:"ipRange"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(args.IPRange)
	if len(text) > maxIPRange {
		return nil, invalidArgument("ipRange must hold at most 64 characters")
	}
	if prefix, err := netip.ParsePrefix(text); err == nil {
		text = prefix.String()
	} else if addr, err := netip.ParseAddr(text); err == nil && addr.Zone() == "" {
		text = addr.String()
	} else {
		return nil, invalidArgument("ipRange must be an IP address or a range in CIDR notation")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var bounds json.RawMessage
	if err := client.call(ctx, op, baseRequirement(ipRangesExpand.ID), "SitesManager.getIpsForRange",
		url.Values{"ipRange": {text}}, &bounds); err != nil {
		return nil, err
	}
	// Matomo answers false for a range it cannot read.
	var pair []string
	if bytes.Equal(bytes.TrimSpace(bounds), []byte("false")) {
		return nil, invalidArgument("Matomo did not accept the IP range")
	}
	if json.Unmarshal(bounds, &pair) != nil || len(pair) != 2 {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid range")
	}
	low, lowErr := netip.ParseAddr(pair[0])
	high, highErr := netip.ParseAddr(pair[1])
	if lowErr != nil || highErr != nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid range")
	}
	return &IPRangeResult{IPRange: text, Low: low.String(), High: high.String()}, nil
}
