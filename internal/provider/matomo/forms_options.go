package matomo

import (
	"context"
	"encoding/json"
	"errors"
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

var formOptionsList = capability.Descriptor{
	ID: Provider + ".form-options.list", Version: 1, Title: "List Matomo form options",
	Description: "List the values forms are configured with: the statuses, the building blocks of form rules and page " +
		"rules, and the conversion rule options, and for the chosen website the auto-creation settings of forms and " +
		"its goals. When the token lacks the right for some options they are listed in unavailable with reason " +
		"permission and left out; when it lacks it for all, the call fails with a permission error. Any other " +
		"failure ends the call. Answers without a documented example are returned as Matomo gives them, shortened. " +
		"Needs the premium plugin FormAnalytics and a view right on the website",
	Tags: []string{"matomo", "forms", "reference", "list"}, Risk: readRisk(formsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"statuses":{},"form_rules":{},"page_rules":{},"conversion_rule_options":{},"auto_creation_settings":{},` +
		`"goals":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
		`"name":{"type":"string"}},"required":["id"],"additionalProperties":false}},` +
		`"unavailable":{"type":"array","items":{"type":"object","properties":{"option":{"type":"string"},` +
		`"reason":{"type":"string"}},"required":["option","reason"],"additionalProperties":false}}},` +
		`"required":["idSite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the auto-creation settings and goals belong to"},
		{Name: "statuses", Description: "Form statuses, the values of statuses in matomo.forms.list, as Matomo gives them; " +
			"strings, lists, and nesting are shortened"},
		{Name: "form_rules", Description: "Attributes, patterns, and examples of form rules as Matomo gives them; shortened"},
		{Name: "page_rules", Description: "Attributes, patterns, and examples of page rules as Matomo gives them; shortened"},
		{Name: "conversion_rule_options", Description: "Conversion rule options with their labels as Matomo gives them; shortened"},
		{Name: "auto_creation_settings", Description: "Message Matomo shows for the auto-creation of forms of the " +
			"website, untrusted; shortened"},
		{Name: "goals", Description: "Goals of the website, at most 100, by ID: id and name (untrusted); Matomo's " +
			"empty default entry is left out"},
		{Name: "unavailable", Description: "Options left out because the token may not read them, each with option " +
			"(statuses, form_rules, page_rules, conversion_rule_options, auto_creation_settings, goals) and reason " +
			"permission; absent when every option was read"}},
	Examples: []capability.Example{{Description: "Options for website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// FormGoal is a goal a form can count conversions for. The name is untrusted provider data.
type FormGoal struct {
	ID   int    `json:"id"`
	Name string `json:"name,omitempty"`
}

// FormOptionGap names an option the token may not read.
type FormOptionGap struct {
	Option string `json:"option"`
	Reason string `json:"reason"`
}

// FormOptionsResult is the answer of form-options.list.
type FormOptionsResult struct {
	IDSite                int             `json:"idSite"`
	Statuses              any             `json:"statuses,omitempty"`
	FormRules             any             `json:"form_rules,omitempty"`
	PageRules             any             `json:"page_rules,omitempty"`
	ConversionRuleOptions any             `json:"conversion_rule_options,omitempty"`
	AutoCreationSettings  any             `json:"auto_creation_settings,omitempty"`
	Goals                 []FormGoal      `json:"goals,omitempty"`
	Unavailable           []FormOptionGap `json:"unavailable,omitempty"`
}

// formGoals reads the map of goal IDs to goal names. Entries whose key is no goal ID, such as the empty default
// option, are left out.
func formGoals(raw json.RawMessage) ([]FormGoal, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "[]" || trimmed == "null" {
		return nil, true
	}
	var entries map[string]json.RawMessage
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal(raw, &entries) != nil {
		return nil, false
	}
	var goals []FormGoal
	for key, value := range entries {
		id, err := strconv.Atoi(key)
		if err != nil || id < 1 {
			continue
		}
		goals = append(goals, FormGoal{ID: id, Name: funnelText(value, maxStringLength)})
	}
	slices.SortFunc(goals, func(a, b FormGoal) int { return a.ID - b.ID })
	if len(goals) > maxResultEntries {
		goals = goals[:maxResultEntries]
	}
	return goals, true
}

func invokeFormOptionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list form options"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := formsRequirement(formOptionsList.ID)
	out := &FormOptionsResult{IDSite: site}
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
			out.Unavailable = append(out.Unavailable, FormOptionGap{Option: option, Reason: optionDenied})
			return nil, false, nil
		}
		return answer, err == nil, err
	}
	siteParams := func() url.Values { return url.Values{"idSite": {strconv.Itoa(site)}} }
	steps := []struct {
		option, method string
		params         url.Values
		store          func(json.RawMessage) bool
	}{
		{"statuses", "getAvailableStatuses", nil, func(a json.RawMessage) bool { out.Statuses = boundedAnswer(a); return true }},
		{"form_rules", "getAvailableFormRules", nil, func(a json.RawMessage) bool { out.FormRules = boundedAnswer(a); return true }},
		{"page_rules", "getAvailablePageRules", nil, func(a json.RawMessage) bool { out.PageRules = boundedAnswer(a); return true }},
		{"conversion_rule_options", "getAvailableConversionRuleOptions", nil, func(a json.RawMessage) bool {
			out.ConversionRuleOptions = boundedAnswer(a)
			return true
		}},
		{"auto_creation_settings", "getAutoCreationSettings", siteParams(), func(a json.RawMessage) bool {
			out.AutoCreationSettings = boundedAnswer(a)
			return true
		}},
		{"goals", "getAllGoals", siteParams(), func(a json.RawMessage) (ok bool) {
			out.Goals, ok = formGoals(a)
			return
		}},
	}
	for _, step := range steps {
		answer, got, err := ask(step.option, "FormAnalytics."+step.method, step.params)
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

func formOptionsOperations() []capability.Operation {
	return []capability.Operation{{Descriptor: formOptionsList, Handler: capability.Handler(invokeFormOptionsList)}}
}
