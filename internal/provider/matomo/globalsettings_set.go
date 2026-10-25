package matomo

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxExcludedEntries = 500
	maxExcludedIP      = 64
	maxExcludedText    = 256
)

const (
	exclusionCommon = "common_session_parameters"
	exclusionPII    = "matomo_recommended_pii"
	exclusionCustom = "custom"
)

var globalSentOnce = sentOnceFor("setting", "matomo.global-settings.get")

// wildcardIP is an IPv4 address whose trailing octets are replaced by *, the only wildcard form checked here.
var wildcardIP = regexp.MustCompile(`^(\d{1,3}\.){1,3}\*(\.\*)*$`)

const globalSetNote = "It replaces the whole global list that every website inherits, so read it first with " +
	"matomo.global-settings.get; an empty list clears it. It needs Super User rights and a connection whose targets " +
	"name the instance, and it changes the tracking of every website of the instance, including those outside the " +
	"connection's website targets; idempotent"

func globalListSchema(name string, maxLength int) string {
	return `{"type":"object","properties":{"` + name + `":{"type":"array","maxItems":500,"items":{"type":"string",` +
		`"minLength":1,"maxLength":` + strconv.Itoa(maxLength) + `}}},"required":["` + name + `"],"additionalProperties":false}`
}

func globalSetOutput(name string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + name + `":{"type":"array","items":{"type":"string"}},` +
		`"updated":{"type":"boolean"}},"required":["` + name + `","updated"],"additionalProperties":false}`)
}

func globalListDescriptor(id, title, description, name, argument, example string, maxLength int) capability.Descriptor {
	return capability.Descriptor{
		ID: id, Version: 1, Title: title, Description: description + " " + globalSetNote + globalSentOnce,
		Tags: []string{"matomo", "global-settings", "exclusions", "update", "instance"},
		Risk: siteChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
		InputSchema:  json.RawMessage(globalListSchema(name, maxLength)),
		OutputSchema: globalSetOutput(name),
		Arguments:    []capability.Argument{{Name: name, Required: true, Description: argument}},
		Fields: []capability.Field{
			{Name: name, Description: "The complete list that was sent"},
			{Name: "updated", Description: "True once Matomo accepted the change"},
		},
		Examples: []capability.Example{{Description: "Replace the list",
			Arguments: json.RawMessage(example)}},
	}
}

var globalSettingsSetExcludedIPs = globalListDescriptor(Provider+".global-settings.set-excluded-ips",
	"Set the globally excluded IPs of Matomo",
	"Replace the IP addresses whose visits no website tracks (SitesManager.setGlobalExcludedIps).",
	"excludedIps", "The complete list, 0 to 500 entries of up to 64 characters: an IP address, an IPv4 address "+
		"with trailing * octets such as 192.0.2.*, or a CIDR range. Matomo checks every entry again",
	`{"excludedIps":["192.0.2.1","198.51.100.0/24"]}`, maxExcludedIP)

var globalSettingsSetExcludedUserAgents = globalListDescriptor(Provider+".global-settings.set-excluded-user-agents",
	"Set the globally excluded user agents of Matomo",
	"Replace the user agent substrings whose visits no website tracks (SitesManager.setGlobalExcludedUserAgents).",
	"excludedUserAgents", "The complete list, 0 to 500 substrings of up to 256 characters without comma or "+
		"control characters; Matomo trims them and drops duplicates",
	`{"excludedUserAgents":["ExampleBot"]}`, maxExcludedText)

var globalSettingsSetExcludedReferrers = globalListDescriptor(Provider+".global-settings.set-excluded-referrers",
	"Set the globally excluded referrers of Matomo",
	"Replace the URLs or hosts that no website counts as referrers (SitesManager.setGlobalExcludedReferrers).",
	"excludedReferrers", "The complete list, 0 to 500 entries of up to 256 characters without spaces, comma or "+
		"control characters: a URL, a host such as example.test/path, or .example.test for subdomains; Matomo "+
		"checks the form again",
	`{"excludedReferrers":["spam.example.test",".tracker.example.test"]}`, maxExcludedText)

var globalSettingsSetExcludedQueryParameters = capability.Descriptor{
	ID: Provider + ".global-settings.set-excluded-query-parameters", Version: 1,
	Title: "Set the globally excluded query parameters of Matomo",
	Description: "Set which URL query parameters Matomo strips from tracked URLs of every website " +
		"(SitesManager.setGlobalQueryParamExclusion). The type common_session_parameters or matomo_recommended_pii " +
		"selects a list Matomo maintains and takes no parameters; custom takes the complete list of 1 to 500 " +
		"parameter names. " + globalSetNote + "; a privacy policy of the instance can make Matomo keep the old " +
		"setting while it still answers success, so read it afterwards" + globalSentOnce,
	Tags: []string{"matomo", "global-settings", "exclusions", "update", "instance"},
	Risk: siteChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"exclusionType":{"type":"string","enum":["` +
		exclusionCommon + `","` + exclusionPII + `","` + exclusionCustom + `"]},"queryParameters":{"type":"array",` +
		`"maxItems":500,"items":{"type":"string","minLength":1,"maxLength":256}}},"required":["exclusionType"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"exclusionType":{"type":"string"},` +
		`"queryParameters":{"type":"array","items":{"type":"string"}},"updated":{"type":"boolean"}},` +
		`"required":["exclusionType","queryParameters","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "exclusionType", Required: true, Description: "common_session_parameters, matomo_recommended_pii, or custom"},
		{Name: "queryParameters", Description: "Parameter names of up to 256 characters without comma or control " +
			"characters; required and not empty for custom, refused for the other types"},
	},
	Fields: []capability.Field{
		{Name: "exclusionType", Description: "The type that was sent"},
		{Name: "queryParameters", Description: "The parameter names that were sent, empty unless the type is custom"},
		{Name: "updated", Description: "True once Matomo accepted the call"},
	},
	Examples: []capability.Example{{Description: "Strip two parameters",
		Arguments: json.RawMessage(`{"exclusionType":"custom","queryParameters":["sessionid","token"]}`)}},
}

// GlobalListSet is the answer of the list setters; the list sits under the argument name.
type GlobalListSet map[string]any

// GlobalQueryParametersSet is the answer of global-settings.set-excluded-query-parameters.
type GlobalQueryParametersSet struct {
	ExclusionType   string   `json:"exclusionType"`
	QueryParameters []string `json:"queryParameters"`
	Updated         bool     `json:"updated"`
}

type globalListKind struct {
	descriptor capability.Descriptor
	name       string
	method     string
	op         string
	check      func(string) bool
}

func validExcludedIP(entry string) bool {
	if len(entry) > maxExcludedIP {
		return false
	}
	if _, err := netip.ParsePrefix(entry); err == nil {
		return true
	}
	if addr, err := netip.ParseAddr(entry); err == nil {
		return addr.Zone() == ""
	}
	if !wildcardIP.MatchString(entry) {
		return false
	}
	parts := strings.Split(entry, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "*" {
			continue
		}
		if len(part) > 3 || (len(part) > 1 && part[0] == '0') || part > "255" && len(part) == 3 {
			return false
		}
	}
	return true
}

func plainText(allowSpace bool) func(string) bool {
	return func(entry string) bool {
		return len(entry) <= maxExcludedText && strings.IndexFunc(entry, func(r rune) bool {
			return r == ',' || unicode.IsControl(r) || (!allowSpace && unicode.IsSpace(r))
		}) < 0
	}
}

// excludedEntries checks and trims a list before anything is sent. Rejections never repeat a value.
func excludedEntries(name string, entries []string, check func(string) bool, allowEmpty bool) ([]string, error) {
	if len(entries) > maxExcludedEntries || (len(entries) == 0 && !allowEmpty) {
		return nil, invalidArgument(name + " must hold " + map[bool]string{true: "0", false: "1"}[allowEmpty] +
			" to 500 entries")
	}
	out := make([]string, 0, len(entries))
	total := 0
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" || !check(entry) {
			return nil, invalidArgument("an entry of " + name + " is empty, too long, or not in the allowed form")
		}
		total += len(entry) + 1
		out = append(out, entry)
	}
	if total > maxGlobalList {
		return nil, invalidArgument(name + " is longer than 8192 characters in total")
	}
	return out, nil
}

func confirmedByMatomo(answer json.RawMessage) bool {
	var done bool
	if scalar(answer, &done) == nil && done {
		return true
	}
	return isSuccessAnswer(answer)
}

func (k globalListKind) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args map[string]json.RawMessage
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	for key := range args {
		if key != k.name {
			return nil, invalidArgument("only " + k.name + " is accepted")
		}
	}
	var entries []string
	if args[k.name] == nil || json.Unmarshal(args[k.name], &entries) != nil || entries == nil {
		return nil, invalidArgument(k.name + " must be a list of text")
	}
	list, err := excludedEntries(k.name, entries, k.check, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, k.op, siteRequirement(k.descriptor.ID), k.method,
		url.Values{k.name: {strings.Join(list, ",")}}, globalSentOnce, &answer); err != nil {
		return nil, err
	}
	if !confirmedByMatomo(answer) {
		return nil, unclearChange(k.op, "Matomo returned no confirmation of the change", globalSentOnce)
	}
	return GlobalListSet{k.name: list, "updated": true}, nil
}

func invokeSetExcludedQueryParameters(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set excluded query parameters"
	var args struct {
		ExclusionType   *string  `json:"exclusionType"`
		QueryParameters []string `json:"queryParameters"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.ExclusionType == nil || !slices.Contains([]string{exclusionCommon, exclusionPII, exclusionCustom},
		*args.ExclusionType) {
		return nil, invalidArgument("exclusionType must be common_session_parameters, matomo_recommended_pii, or custom")
	}
	kind := *args.ExclusionType
	list := []string{}
	if kind == exclusionCustom {
		var err error
		if list, err = excludedEntries("queryParameters", args.QueryParameters, plainText(false), false); err != nil {
			return nil, err
		}
	} else if len(args.QueryParameters) > 0 {
		return nil, invalidArgument("queryParameters is only accepted for the type custom")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"exclusionType": {kind}}
	if kind == exclusionCustom {
		params.Set("queryParamsToExclude", strings.Join(list, ","))
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, siteRequirement(globalSettingsSetExcludedQueryParameters.ID),
		"SitesManager.setGlobalQueryParamExclusion", params, globalSentOnce, &answer); err != nil {
		return nil, err
	}
	if !confirmedByMatomo(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the change", globalSentOnce)
	}
	return &GlobalQueryParametersSet{ExclusionType: kind, QueryParameters: list, Updated: true}, nil
}

var globalListKinds = []globalListKind{
	{globalSettingsSetExcludedIPs, "excludedIps", "SitesManager.setGlobalExcludedIps", "set excluded IPs",
		validExcludedIP},
	{globalSettingsSetExcludedUserAgents, "excludedUserAgents", "SitesManager.setGlobalExcludedUserAgents",
		"set excluded user agents", plainText(true)},
	{globalSettingsSetExcludedReferrers, "excludedReferrers", "SitesManager.setGlobalExcludedReferrers",
		"set excluded referrers", plainText(false)},
}

func globalSetOperations() []capability.Operation {
	ops := make([]capability.Operation, 0, len(globalListKinds)+1+len(globalDefaultKinds))
	for _, kind := range globalListKinds {
		ops = append(ops, capability.Operation{Descriptor: kind.descriptor, Handler: capability.Handler(kind.invoke)})
	}
	ops = append(ops, capability.Operation{Descriptor: globalSettingsSetExcludedQueryParameters,
		Handler: capability.Handler(invokeSetExcludedQueryParameters)})
	return append(ops, globalDefaultOperations()...)
}
