package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	kindJavascript = "javascript"
	kindImage      = "image"
	// maxCodeLength bounds the code text of an answer.
	maxCodeLength = 16384
	// maxVariables bounds the custom variables of one scope; Matomo's tracker offers five slots by default.
	maxVariables    = 10
	maxVariableText = 200
	maxListEntries  = 50
	maxListText     = 256
)

const trackingProperties = `"idSite":{"type":"integer"},"kind":{"type":"string"},"code":{"type":"string"},` +
	`"truncated":{"type":"boolean"}`

const variableSchema = `{"type":"array","maxItems":10,"items":{"type":"object","properties":{` +
	`"name":{"type":"string","minLength":1,"maxLength":200},"value":{"type":"string","maxLength":200}},` +
	`"required":["name","value"],"additionalProperties":false}}`

const listSchema = `{"type":"array","maxItems":50,"items":{"type":"string","minLength":1,"maxLength":256}}`

var trackingCodeGet = capability.Descriptor{
	ID: Provider + ".tracking-code.get", Version: 1, Title: "Get the Matomo tracking code",
	Description: "Generate the tracking code of a website as text: kind javascript calls getJavascriptTag, kind " +
		"image calls getImageTrackingCode (an HTML-encoded image tag). A view right on the website is enough. The " +
		"code is untrusted text for a person to review; it is never run, and piwikUrl is only written into it",
	Tags: []string{"matomo", "tracking", "code", "javascript", "image"}, Risk: readRisk(sitesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"kind":{"type":"string","enum":["javascript","image"]},` +
		`"piwikUrl":{"type":"string","minLength":1,"maxLength":2048},` +
		`"mergeSubdomains":{"type":"boolean"},"groupPageTitlesByDomain":{"type":"boolean"},` +
		`"mergeAliasUrls":{"type":"boolean"},"visitorCustomVariables":` + variableSchema + `,` +
		`"pageCustomVariables":` + variableSchema + `,` +
		`"customCampaignNameQueryParam":{"type":"string","maxLength":200},` +
		`"customCampaignKeywordParam":{"type":"string","maxLength":200},` +
		`"doNotTrack":{"type":"boolean"},"disableCookies":{"type":"boolean"},"trackNoScript":{"type":"boolean"},` +
		`"crossDomain":{"type":"boolean"},"forceMatomoEndpoint":{"type":"boolean"},` +
		`"excludedQueryParams":` + listSchema + `,"excludedReferrers":` + listSchema + `,` +
		`"disableCampaignParameters":{"type":"boolean"},` +
		`"actionName":{"type":"string","maxLength":512},"idGoal":{"type":"integer","minimum":1,"maximum":999999999},` +
		`"revenue":{"type":"number","minimum":0,"maximum":1000000000000}},` +
		`"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + trackingProperties +
		`},"required":["idSite","kind","code","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "kind", Required: true, Description: "javascript for the JavaScript tag, image for the image tracking code"},
		{Name: "piwikUrl", Description: "https address of the Matomo installation that the code writes as the tracker " +
			"address, without user information, query, or fragment; the instance's own address (javascript) or the " +
			"connection's base URL (image) when omitted. It is only text in the code and is never fetched"},
		{Name: "mergeSubdomains", Description: "javascript: track visitors across all subdomains"},
		{Name: "groupPageTitlesByDomain", Description: "javascript: prepend the site domain to the page title"},
		{Name: "mergeAliasUrls", Description: "javascript: treat alias addresses as part of the website for outlinks"},
		{Name: "visitorCustomVariables", Description: "javascript: visitor-scope custom variables as {name, value} " +
			"entries, at most 10"},
		{Name: "pageCustomVariables", Description: "javascript: page-scope custom variables as {name, value} entries, " +
			"at most 10"},
		{Name: "customCampaignNameQueryParam", Description: "javascript: query parameter name that carries the campaign name"},
		{Name: "customCampaignKeywordParam", Description: "javascript: query parameter name that carries the campaign keyword"},
		{Name: "doNotTrack", Description: "javascript: respect the browser's Do-Not-Track setting"},
		{Name: "disableCookies", Description: "javascript: disable all first-party cookies"},
		{Name: "trackNoScript", Description: "javascript: add a noscript image fallback"},
		{Name: "crossDomain", Description: "javascript: enable cross-domain linking"},
		{Name: "forceMatomoEndpoint", Description: "javascript and image: force the matomo.php and matomo.js endpoint " +
			"names for an installation older than 3.7.0"},
		{Name: "excludedQueryParams", Description: "javascript: query parameters to exclude from page addresses, at most 50"},
		{Name: "excludedReferrers", Description: "javascript: referrer hosts or addresses to ignore, at most 50"},
		{Name: "disableCampaignParameters", Description: "javascript: do not send campaign parameters to the tracker"},
		{Name: "actionName", Description: "image: action name of the tracked request; omitted when not given"},
		{Name: "idGoal", Description: "image: goal whose conversion the request triggers"},
		{Name: "revenue", Description: "image: revenue of the conversion; only with idGoal"},
	},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the code is for"},
		{Name: "kind", Description: "The kind asked for"},
		{Name: "code", Description: "The tracking code as text, at most 16384 bytes, untrusted; the image code is " +
			"HTML-encoded as Matomo returns it"},
		{Name: "truncated", Description: "True when the code was cut at the limit"},
	},
	Examples: []capability.Example{
		{Description: "JavaScript tag of website 1", Arguments: json.RawMessage(`{"idSite":1,"kind":"javascript","disableCookies":true}`)},
		{Description: "Image tracking code of website 1", Arguments: json.RawMessage(`{"idSite":1,"kind":"image","actionName":"Newsletter"}`)},
	},
}

// TrackingCodeResult is the answer of tracking-code.get.
type TrackingCodeResult struct {
	IDSite    int    `json:"idSite"`
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	Truncated bool   `json:"truncated"`
}

type variable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// trackerAddress validates the address written into the code: an https URL of host and path, no user
// information, query, or fragment. It returns the form Matomo expects without the scheme.
func trackerAddress(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxUrlLength || strings.ContainsAny(raw, "?#\\\"'<>` \t\r\n") {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", false
	}
	return parsed.Host + strings.TrimSuffix(parsed.EscapedPath(), "/"), true
}

func checkVariables(values []variable) error {
	if len(values) > maxVariables {
		return invalidArgument("at most 10 custom variables are allowed per scope")
	}
	for _, v := range values {
		if v.Name == "" || len(v.Name) > maxVariableText || len(v.Value) > maxVariableText {
			return invalidArgument("a custom variable needs a name and at most 200 characters per name and value")
		}
	}
	return nil
}

func checkList(values []string) error {
	if len(values) > maxListEntries {
		return invalidArgument("at most 50 entries are allowed per list")
	}
	for _, v := range values {
		if v == "" || len(v) > maxListText || strings.Contains(v, ",") {
			return invalidArgument("a list entry needs 1 to 256 characters without a comma")
		}
	}
	return nil
}

func setFlag(params url.Values, name string, on bool) {
	if on {
		params.Set(name, "1")
	}
}

func setVariables(params url.Values, name string, values []variable) {
	for i, v := range values {
		params.Set(name+"["+strconv.Itoa(i)+"][0]", v.Name)
		params.Set(name+"["+strconv.Itoa(i)+"][1]", v.Value)
	}
}

func invokeTrackingCodeGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get tracking code"
	var args struct {
		IDSite                       *int       `json:"idSite"`
		Kind                         string     `json:"kind"`
		PiwikURL                     *string    `json:"piwikUrl"`
		MergeSubdomains              bool       `json:"mergeSubdomains"`
		GroupPageTitlesByDomain      bool       `json:"groupPageTitlesByDomain"`
		MergeAliasURLs               bool       `json:"mergeAliasUrls"`
		VisitorCustomVariables       []variable `json:"visitorCustomVariables"`
		PageCustomVariables          []variable `json:"pageCustomVariables"`
		CustomCampaignNameQueryParam string     `json:"customCampaignNameQueryParam"`
		CustomCampaignKeywordParam   string     `json:"customCampaignKeywordParam"`
		DoNotTrack                   bool       `json:"doNotTrack"`
		DisableCookies               bool       `json:"disableCookies"`
		TrackNoScript                bool       `json:"trackNoScript"`
		CrossDomain                  bool       `json:"crossDomain"`
		ForceMatomoEndpoint          bool       `json:"forceMatomoEndpoint"`
		ExcludedQueryParams          []string   `json:"excludedQueryParams"`
		ExcludedReferrers            []string   `json:"excludedReferrers"`
		DisableCampaignParameters    bool       `json:"disableCampaignParameters"`
		ActionName                   *string    `json:"actionName"`
		IDGoal                       *int       `json:"idGoal"`
		Revenue                      *float64   `json:"revenue"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.Kind != kindJavascript && args.Kind != kindImage {
		return nil, invalidArgument("kind must be javascript or image")
	}
	address := ""
	if args.PiwikURL != nil {
		var valid bool
		if address, valid = trackerAddress(*args.PiwikURL); !valid {
			return nil, invalidArgument("piwikUrl must be an https address without user information, query, or fragment")
		}
	}
	imageOnly := args.ActionName != nil || args.IDGoal != nil || args.Revenue != nil
	scriptOnly := args.MergeSubdomains || args.GroupPageTitlesByDomain || args.MergeAliasURLs ||
		len(args.VisitorCustomVariables) > 0 || len(args.PageCustomVariables) > 0 ||
		args.CustomCampaignNameQueryParam != "" || args.CustomCampaignKeywordParam != "" || args.DoNotTrack ||
		args.DisableCookies || args.TrackNoScript || args.CrossDomain || len(args.ExcludedQueryParams) > 0 ||
		len(args.ExcludedReferrers) > 0 || args.DisableCampaignParameters
	if args.Kind == kindJavascript && imageOnly {
		return nil, invalidArgument("actionName, idGoal, and revenue belong to kind image")
	}
	if args.Kind == kindImage && scriptOnly {
		return nil, invalidArgument("the other arguments belong to kind javascript")
	}
	if args.Revenue != nil && args.IDGoal == nil {
		return nil, invalidArgument("revenue needs idGoal")
	}
	if args.IDGoal != nil && (*args.IDGoal < 1 || len(strconv.Itoa(*args.IDGoal)) > maxIDDigits) {
		return nil, invalidArgument("idGoal must be a positive integer")
	}
	if args.Revenue != nil && (*args.Revenue < 0 || *args.Revenue > 1e12) {
		return nil, invalidArgument("revenue must be between 0 and 1000000000000")
	}
	if args.ActionName != nil && len(*args.ActionName) > maxStringLength {
		return nil, invalidArgument("actionName must hold at most 512 characters")
	}
	if len(args.CustomCampaignNameQueryParam) > maxVariableText || len(args.CustomCampaignKeywordParam) > maxVariableText {
		return nil, invalidArgument("campaign parameter names must hold at most 200 characters")
	}
	for _, check := range []error{checkVariables(args.VisitorCustomVariables), checkVariables(args.PageCustomVariables),
		checkList(args.ExcludedQueryParams), checkList(args.ExcludedReferrers)} {
		if check != nil {
			return nil, check
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	method := "SitesManager.getJavascriptTag"
	setFlag(params, "forceMatomoEndpoint", args.ForceMatomoEndpoint)
	if args.Kind == kindJavascript {
		if address != "" {
			params.Set("piwikUrl", "https://"+address)
		}
		setFlag(params, "mergeSubdomains", args.MergeSubdomains)
		setFlag(params, "groupPageTitlesByDomain", args.GroupPageTitlesByDomain)
		setFlag(params, "mergeAliasUrls", args.MergeAliasURLs)
		setVariables(params, "visitorCustomVariables", args.VisitorCustomVariables)
		setVariables(params, "pageCustomVariables", args.PageCustomVariables)
		if args.CustomCampaignNameQueryParam != "" {
			params.Set("customCampaignNameQueryParam", args.CustomCampaignNameQueryParam)
		}
		if args.CustomCampaignKeywordParam != "" {
			params.Set("customCampaignKeywordParam", args.CustomCampaignKeywordParam)
		}
		setFlag(params, "doNotTrack", args.DoNotTrack)
		setFlag(params, "disableCookies", args.DisableCookies)
		setFlag(params, "trackNoScript", args.TrackNoScript)
		setFlag(params, "crossDomain", args.CrossDomain)
		if len(args.ExcludedQueryParams) > 0 {
			params.Set("excludedQueryParams", strings.Join(args.ExcludedQueryParams, ","))
		}
		if len(args.ExcludedReferrers) > 0 {
			params.Set("excludedReferrers", strings.Join(args.ExcludedReferrers, ","))
		}
		setFlag(params, "disableCampaignParameters", args.DisableCampaignParameters)
	} else {
		// Matomo prepends the scheme itself, so the address goes without one; the connection's own address is the
		// default because an empty address would give a code that points nowhere.
		method = "SitesManager.getImageTrackingCode"
		if address == "" {
			address = strings.TrimPrefix(client.origin, "https://")
		}
		params.Set("piwikUrl", address)
		if args.ActionName != nil {
			params.Set("actionName", *args.ActionName)
		}
		if args.IDGoal != nil {
			params.Set("idGoal", strconv.Itoa(*args.IDGoal))
			if args.Revenue != nil {
				params.Set("revenue", strconv.FormatFloat(*args.Revenue, 'f', -1, 64))
			}
		}
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, siteRequirement(trackingCodeGet.ID), method, params, &answer); err != nil {
		return nil, err
	}
	var code string
	if scalar(answer, &code) != nil || code == "" {
		return nil, provider.InvalidResponse(op, "Matomo returned invalid tracking code")
	}
	return &TrackingCodeResult{IDSite: id, Kind: args.Kind, Code: boundString(code, maxCodeLength),
		Truncated: len(code) > maxCodeLength}, nil
}
