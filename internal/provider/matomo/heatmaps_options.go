package matomo

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxMatchRules = 20
	// optionDenied is the reason of an option the token may not read.
	optionDenied = "permission"
)

const matchRuleSchema = `{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{` +
	`"attribute":{"type":"string","pattern":"` + patternTypePattern + `"},` +
	`"type":{"type":"string","pattern":"` + patternTypePattern + `"},` +
	`"value":{"type":"string","minLength":1,"maxLength":2048},"inverted":{"type":"boolean"}},` +
	`"required":["attribute","type","value"],"additionalProperties":false}}`

var heatmapOptionsList = capability.Descriptor{
	ID: Provider + ".heatmap-options.list", Version: 1, Title: "List Matomo heatmap options",
	Description: "List the values heatmaps are configured and read with: device types and heatmap types (a view " +
		"right on any website), and statuses, page rule building blocks, and session recording sample limits " +
		"(Matomo asks a write right on at least one website for these three). With url, also ask Matomo whether the " +
		"URL matches the given page rules (matchPageRules), which also needs that write right; nothing is stored " +
		"and the URL is never fetched. When the token lacks the right for some options they are listed in " +
		"unavailable with reason permission and left out; when it lacks it for all, the call fails with a " +
		"permission error. Any other failure ends the call. The answers of the options without a documented " +
		"example are returned as Matomo gives them, shortened. Reads no website data. Needs the premium plugin " +
		"HeatmapSessionRecording and a connection that binds a website",
	Tags: []string{"matomo", "heatmaps", "reference", "list"}, Risk: readRisk(heatmapsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1,"maxLength":2048},` +
		`"matchPageRules":` + matchRuleSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"device_types":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"key":{"type":"integer"},"logo":{"type":"string"}},"additionalProperties":false}},` +
		`"heatmap_types":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"key":{"type":"integer"}},"additionalProperties":false}},` +
		`"statuses":{},"target_page_rules":{},"session_recording_sample_limits":{},"url_match":{},` +
		`"unavailable":{"type":"array","items":{"type":"object","properties":{"option":{"type":"string"},` +
		`"reason":{"type":"string"}},"required":["option","reason"],"additionalProperties":false}}},` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "url", Description: "A URL or comparable value to test against matchPageRules, up to 2048 " +
			"characters; without it the test is not asked"},
		{Name: "matchPageRules", Description: "1 to 20 page rules to test url against; each holds attribute and " +
			"type (lowercase letters and underscores, values from target_page_rules) and value, and optionally " +
			"inverted; only with url"}},
	Fields: []capability.Field{
		{Name: "device_types", Description: "Device types a heatmap can be read for: name, key (the deviceType " +
			"argument of matomo.recorded-heatmaps.get), and logo, a path inside Matomo, untrusted"},
		{Name: "heatmap_types", Description: "Heatmap types: name and key (the heatmapType argument of " +
			"matomo.recorded-heatmaps.get)"},
		{Name: "statuses", Description: "Heatmap and recording statuses as Matomo gives them; strings, lists, and " +
			"nesting are shortened"},
		{Name: "target_page_rules", Description: "Attributes and comparison types for page rules as Matomo gives " +
			"them; shortened"},
		{Name: "session_recording_sample_limits", Description: "Sample limits a session recording can use as " +
			"Matomo gives them; shortened"},
		{Name: "url_match", Description: "Matomo's answer to the URL test, untrusted; shortened; only with url"},
		{Name: "unavailable", Description: "Options left out because the token may not read them, each with option " +
			"(device_types, heatmap_types, statuses, target_page_rules, session_recording_sample_limits, url_match) " +
			"and reason permission; absent when every asked option was read"}},
	Examples: []capability.Example{
		{Description: "List the options", Arguments: json.RawMessage(`{}`)},
		{Description: "Test a URL against a page rule", Arguments: json.RawMessage(
			`{"url":"https://example.org/pricing","matchPageRules":[{"attribute":"url","type":"equals_exactly","value":"https://example.org/pricing"}]}`)}},
}

// HeatmapOptionType is a device type or heatmap type. All text is untrusted provider data.
type HeatmapOptionType struct {
	Name string `json:"name,omitempty"`
	Key  *int   `json:"key,omitempty"`
	Logo string `json:"logo,omitempty"`
}

// HeatmapOptionGap names an option the token may not read.
type HeatmapOptionGap struct {
	Option string `json:"option"`
	Reason string `json:"reason"`
}

// HeatmapOptionsResult is the answer of heatmap-options.list.
type HeatmapOptionsResult struct {
	DeviceTypes                  []HeatmapOptionType `json:"device_types,omitempty"`
	HeatmapTypes                 []HeatmapOptionType `json:"heatmap_types,omitempty"`
	Statuses                     any                 `json:"statuses,omitempty"`
	TargetPageRules              any                 `json:"target_page_rules,omitempty"`
	SessionRecordingSampleLimits any                 `json:"session_recording_sample_limits,omitempty"`
	URLMatch                     any                 `json:"url_match,omitempty"`
	Unavailable                  []HeatmapOptionGap  `json:"unavailable,omitempty"`
}

type heatmapOptionTypeJSON struct {
	Name *loose `json:"name"`
	Key  *loose `json:"key"`
	Logo *loose `json:"logo"`
}

func optionTypes(raw json.RawMessage) ([]HeatmapOptionType, bool) {
	rows, ok := readRows[heatmapOptionTypeJSON](raw)
	if !ok {
		return nil, false
	}
	out := make([]HeatmapOptionType, 0, min(len(rows), maxResultEntries))
	for _, row := range rows {
		if len(out) == maxResultEntries {
			break
		}
		out = append(out, HeatmapOptionType{Name: row.Name.str(maxStringLength), Key: row.Key.number(),
			Logo: row.Logo.str(maxUrlLength)})
	}
	return out, true
}

type matchRuleInput struct {
	Attribute string `json:"attribute"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	Inverted  *bool  `json:"inverted"`
}

// matchRuleParams checks the page rules and adds them as matchPageRules[N][member].
func matchRuleParams(params url.Values, rules []matchRuleInput) error {
	if len(rules) < 1 || len(rules) > maxMatchRules {
		return invalidArgument("matchPageRules must hold 1 to " + strconv.Itoa(maxMatchRules) + " rules")
	}
	for i, rule := range rules {
		if !patternTypeOK(rule.Attribute) || !patternTypeOK(rule.Type) || rule.Value == "" ||
			utf8.RuneCountInString(rule.Value) > maxUrlLength {
			return invalidArgument("each page rule needs an attribute and a type of lowercase letters and " +
				"underscores and a value of 1 to 2048 characters")
		}
		key := "matchPageRules[" + strconv.Itoa(i) + "]"
		params.Set(key+"[attribute]", rule.Attribute)
		params.Set(key+"[type]", rule.Type)
		params.Set(key+"[value]", rule.Value)
		if rule.Inverted != nil {
			params.Set(key+"[inverted]", flagValue(rule.Inverted))
		}
	}
	return nil
}

func invokeHeatmapOptionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list heatmap options"
	var args struct {
		URL            string           `json:"url"`
		MatchPageRules []matchRuleInput `json:"matchPageRules"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	var testParams url.Values
	switch {
	case args.URL == "" && args.MatchPageRules != nil:
		return nil, invalidArgument("matchPageRules is tested only together with url")
	case args.URL != "":
		if utf8.RuneCountInString(args.URL) > maxUrlLength {
			return nil, invalidArgument("url must hold 1 to 2048 characters")
		}
		testParams = url.Values{"url": {args.URL}}
		if args.MatchPageRules != nil {
			if err := matchRuleParams(testParams, args.MatchPageRules); err != nil {
				return nil, err
			}
		}
	}
	if err := websiteBound(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(heatmapOptionsList.ID)
	out := &HeatmapOptionsResult{}
	denied := 0
	var firstDenied error
	// ask runs one method. A refusal for the right of the token leaves the option out and is reported; any other
	// failure ends the call, so nothing is silently missing.
	ask := func(option, method string, params url.Values) (json.RawMessage, bool, error) {
		var answer json.RawMessage
		err := client.call(ctx, op, req, method, params, &answer)
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			denied++
			if firstDenied == nil {
				firstDenied = err
			}
			out.Unavailable = append(out.Unavailable, HeatmapOptionGap{Option: option, Reason: optionDenied})
			return nil, false, nil
		}
		return answer, err == nil, err
	}
	steps := []struct {
		option, method string
		params         url.Values
		store          func(json.RawMessage) bool
	}{
		{"device_types", "getAvailableDeviceTypes", nil, func(a json.RawMessage) (ok bool) {
			out.DeviceTypes, ok = optionTypes(a)
			return
		}},
		{"heatmap_types", "getAvailableHeatmapTypes", nil, func(a json.RawMessage) (ok bool) {
			out.HeatmapTypes, ok = optionTypes(a)
			return
		}},
		{"statuses", "getAvailableStatuses", nil, func(a json.RawMessage) bool { out.Statuses = boundedAnswer(a); return true }},
		{"target_page_rules", "getAvailableTargetPageRules", nil, func(a json.RawMessage) bool {
			out.TargetPageRules = boundedAnswer(a)
			return true
		}},
		{"session_recording_sample_limits", "getAvailableSessionRecordingSampleLimits", nil, func(a json.RawMessage) bool {
			out.SessionRecordingSampleLimits = boundedAnswer(a)
			return true
		}},
	}
	if testParams != nil {
		steps = append(steps, struct {
			option, method string
			params         url.Values
			store          func(json.RawMessage) bool
		}{"url_match", "testUrlMatchPages", testParams, func(a json.RawMessage) bool {
			out.URLMatch = boundedAnswer(a)
			return true
		}})
	}
	for _, step := range steps {
		answer, got, err := ask(step.option, "HeatmapSessionRecording."+step.method, step.params)
		if err != nil {
			return nil, err
		}
		if got && !step.store(answer) {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
		}
	}
	if denied == len(steps) {
		return nil, firstDenied
	}
	return out, nil
}

func heatmapOptionsOperations() []capability.Operation {
	return []capability.Operation{{Descriptor: heatmapOptionsList, Handler: capability.Handler(invokeHeatmapOptionsList)}}
}
