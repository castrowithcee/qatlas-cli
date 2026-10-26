package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	optOutSensitive = "matomo-opt-out-code"
	kindOptOutJS    = "js"
	kindOptOutSelf  = "self-contained"
	// maxOptOutCodeLength bounds the code text of an answer; the self-contained code carries its script.
	maxOptOutCodeLength = 65536
)

// The patterns are narrower than Matomo's: the values are written into an HTML attribute or a style, so a
// quote, bracket, or semicolon never passes.
var (
	optOutColor      = regexp.MustCompile(`^([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6})$`)
	optOutFontSize   = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3})?(px|pt|em|rem|%)$`)
	optOutFamily     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ,-]{0,99}$`)
	optOutLanguage   = regexp.MustCompile(`^(auto|[a-z]{2,3}(-[A-Za-z]{2,4})?)$`)
	optOutCookiePath = regexp.MustCompile(`^/[A-Za-z0-9/_.-]{0,254}$`)
	optOutCookieHost = regexp.MustCompile(`^\.?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

var optOutSameSite = []string{"Lax", "Strict", "None"}

var optOutCodeGet = capability.Descriptor{
	ID: Provider + ".opt-out-code.get", Version: 1, Title: "Get the Matomo opt-out code",
	Description: "Generate the embed code of the tracking opt-out form as text: kind js calls " +
		"getOptOutJSEmbedCode (a script tag that loads the form from the instance), kind self-contained calls " +
		"getOptOutSelfContainedEmbedCode (the form and its script in the page). Matomo checks no right and no " +
		"website for it. The code is untrusted text for a person to review; it is never run, and matomoUrl is only " +
		"written into it",
	Tags: []string{"matomo", "opt-out", "privacy", "code", "consent"}, Risk: readRisk(optOutSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":["js","self-contained"]},` +
		`"applyStyling":{"type":"boolean"},"showIntro":{"type":"boolean"},` +
		`"backgroundColor":{"type":"string","pattern":"^([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6})$"},` +
		`"fontColor":{"type":"string","pattern":"^([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6})$"},` +
		`"fontSize":{"type":"string","maxLength":10,"pattern":"^[0-9]{1,3}(\\.[0-9]{1,3})?(px|pt|em|rem|%)$"},` +
		`"fontFamily":{"type":"string","maxLength":100,"pattern":"^[A-Za-z0-9][A-Za-z0-9 ,-]{0,99}$"},` +
		`"matomoUrl":{"type":"string","minLength":1,"maxLength":2048},` +
		`"language":{"type":"string","maxLength":16,"pattern":"^(auto|[a-z]{2,3}(-[A-Za-z]{2,4})?)$"},` +
		`"cookiePath":{"type":"string","maxLength":255,"pattern":"^/[A-Za-z0-9/_.-]{0,254}$"},` +
		`"cookieDomain":{"type":"string","maxLength":253},` +
		`"cookieSameSite":{"type":"string","enum":["Lax","Strict","None"]}},` +
		`"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"code":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},"required":["kind","code","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "kind", Required: true, Description: "js for the script that loads the form from the instance, " +
			"self-contained for the form with its script in the page"},
		{Name: "applyStyling", Description: "True to apply the colors, size, and font below; false when omitted"},
		{Name: "showIntro", Description: "False to leave out the introductory text; true when omitted"},
		{Name: "backgroundColor", Description: "Background color as 3 or 6 hex digits without #, such as ffffff; only " +
			"with applyStyling"},
		{Name: "fontColor", Description: "Font color as 3 or 6 hex digits without #; only with applyStyling"},
		{Name: "fontSize", Description: "Font size such as 13px, 15pt, 1.2em, or 100%; only with applyStyling"},
		{Name: "fontFamily", Description: "Font family names of letters, digits, spaces, commas, and hyphens, such as " +
			"Monaco, monospace; only with applyStyling"},
		{Name: "matomoUrl", Description: "kind js only: https address of the Matomo installation that the form is loaded " +
			"from, without user information, query, or fragment; the connection's base URL when omitted. It is only " +
			"text in the code and is never fetched"},
		{Name: "language", Description: "kind js only: language code of the form such as en or de-AT, or auto for the " +
			"browser language; auto when omitted"},
		{Name: "cookiePath", Description: "kind self-contained only: path of the consent cookie, starting with /; / when omitted"},
		{Name: "cookieDomain", Description: "kind self-contained only: domain of the consent cookie; the page's own " +
			"domain when omitted"},
		{Name: "cookieSameSite", Description: "kind self-contained only: Lax, Strict, or None; Lax when omitted"},
	},
	Fields: []capability.Field{
		{Name: "kind", Description: "The kind asked for"},
		{Name: "code", Description: "The embed code as text, at most 65536 bytes, untrusted"},
		{Name: "truncated", Description: "True when the code was cut at the limit"},
	},
	Examples: []capability.Example{
		{Description: "Script tag of the opt-out form in German", Arguments: json.RawMessage(`{"kind":"js","language":"de"}`)},
		{Description: "Self-contained form with styling", Arguments: json.RawMessage(
			`{"kind":"self-contained","applyStyling":true,"fontColor":"333333","fontSize":"14px"}`)},
	},
}

// OptOutCodeResult is the answer of opt-out-code.get.
type OptOutCodeResult struct {
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	Truncated bool   `json:"truncated"`
}

func invokeOptOutCodeGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get opt-out code"
	var args struct {
		Kind            string  `json:"kind"`
		ApplyStyling    bool    `json:"applyStyling"`
		ShowIntro       *bool   `json:"showIntro"`
		BackgroundColor string  `json:"backgroundColor"`
		FontColor       string  `json:"fontColor"`
		FontSize        string  `json:"fontSize"`
		FontFamily      string  `json:"fontFamily"`
		MatomoURL       *string `json:"matomoUrl"`
		Language        string  `json:"language"`
		CookiePath      string  `json:"cookiePath"`
		CookieDomain    string  `json:"cookieDomain"`
		CookieSameSite  string  `json:"cookieSameSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.Kind != kindOptOutJS && args.Kind != kindOptOutSelf {
		return nil, invalidArgument("kind must be js or self-contained")
	}
	styling := args.BackgroundColor != "" || args.FontColor != "" || args.FontSize != "" || args.FontFamily != ""
	if styling && !args.ApplyStyling {
		return nil, invalidArgument("backgroundColor, fontColor, fontSize, and fontFamily need applyStyling")
	}
	for _, check := range []struct {
		value   string
		pattern *regexp.Regexp
		message string
	}{
		{args.BackgroundColor, optOutColor, "backgroundColor must be 3 or 6 hex digits without #"},
		{args.FontColor, optOutColor, "fontColor must be 3 or 6 hex digits without #"},
		{args.FontSize, optOutFontSize, "fontSize must be a size such as 13px, 15pt, 1.2em, or 100%"},
		{args.FontFamily, optOutFamily, "fontFamily must hold 1 to 100 letters, digits, spaces, commas, and hyphens"},
		{args.Language, optOutLanguage, "language must be auto or a language code such as en or de-AT"},
		{args.CookiePath, optOutCookiePath, "cookiePath must be a path that starts with /"},
		{args.CookieDomain, optOutCookieHost, "cookieDomain must be a domain name"},
	} {
		if check.value != "" && !check.pattern.MatchString(check.value) {
			return nil, invalidArgument(check.message)
		}
	}
	if args.CookieSameSite != "" && !slices.Contains(optOutSameSite, args.CookieSameSite) {
		return nil, invalidArgument("cookieSameSite must be Lax, Strict, or None")
	}
	selfOnly := args.CookiePath != "" || args.CookieDomain != "" || args.CookieSameSite != ""
	jsOnly := args.MatomoURL != nil || args.Language != ""
	if args.Kind == kindOptOutJS && selfOnly {
		return nil, invalidArgument("cookiePath, cookieDomain, and cookieSameSite belong to kind self-contained")
	}
	if args.Kind == kindOptOutSelf && jsOnly {
		return nil, invalidArgument("matomoUrl and language belong to kind js")
	}
	address := ""
	if args.MatomoURL != nil {
		var valid bool
		if address, valid = trackerAddress(*args.MatomoURL); !valid {
			return nil, invalidArgument("matomoUrl must be an https address without user information, query, or fragment")
		}
	}
	// Matomo checks no right for this call, but the connection must still reach websites like every other
	// read; this is settled before the secret is read.
	if bound, err := boundScope(resolved); err != nil {
		return nil, err
	} else if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Matomo declares the four style values as required, so they are always sent, empty when not asked for.
	params := url.Values{"backgroundColor": {args.BackgroundColor}, "fontColor": {args.FontColor},
		"fontSize": {args.FontSize}, "fontFamily": {args.FontFamily}, "applyStyling": {flag01(args.ApplyStyling)},
		"showIntro": {flag01(args.ShowIntro == nil || *args.ShowIntro)}}
	method := "CoreAdminHome.getOptOutJSEmbedCode"
	if args.Kind == kindOptOutJS {
		if address == "" {
			address = strings.TrimPrefix(client.origin, "https://")
		}
		language := args.Language
		if language == "" {
			language = "auto"
		}
		params.Set("matomoUrl", "https://"+address)
		params.Set("language", language)
	} else {
		method = "CoreAdminHome.getOptOutSelfContainedEmbedCode"
		if args.CookiePath != "" {
			params.Set("cookiePath", args.CookiePath)
		}
		if args.CookieDomain != "" {
			params.Set("cookieDomain", args.CookieDomain)
		}
		if args.CookieSameSite != "" {
			params.Set("cookieSameSite", args.CookieSameSite)
		}
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, baseRequirement(optOutCodeGet.ID), method, params, &answer); err != nil {
		return nil, err
	}
	var code string
	if scalar(answer, &code) != nil || code == "" {
		return nil, provider.InvalidResponse(op, "Matomo returned invalid opt-out code")
	}
	return &OptOutCodeResult{Kind: args.Kind, Code: boundString(code, maxOptOutCodeLength),
		Truncated: len(code) > maxOptOutCodeLength}, nil
}

func flag01(on bool) string {
	if on {
		return "1"
	}
	return "0"
}
