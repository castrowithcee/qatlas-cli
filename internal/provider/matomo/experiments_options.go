package matomo

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var experimentOptionsList = capability.Descriptor{
	ID: Provider + ".experiment-options.list", Version: 1, Title: "List Matomo A/B test options",
	Description: "List the values experiments are configured with: the statuses and the success metrics available for " +
		"the chosen website, and the target attributes and target rule types. When the token lacks the right for some " +
		"options they are listed in unavailable with reason permission and left out; when it lacks it for all, the call " +
		"fails with a permission error. Any other failure ends the call. Answers without a documented example are " +
		"returned as Matomo gives them, shortened. Needs the premium plugin AbTesting and a right on the website",
	Tags: []string{"matomo", "experiments", "ab-testing", "reference", "list"}, Risk: readRisk(experimentsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"statuses":{},"success_metrics":{},"target_attributes":{},` +
		`"unavailable":{"type":"array","items":{"type":"object","properties":{"option":{"type":"string"},` +
		`"reason":{"type":"string"}},"required":["option","reason"],"additionalProperties":false}}},` +
		`"required":["idSite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the statuses and success metrics belong to"},
		{Name: "statuses", Description: "Experiment statuses, the values of statuses in matomo.experiments.list, as " +
			"Matomo gives them; strings, lists, and nesting are shortened"},
		{Name: "success_metrics", Description: "Success metrics available for the website, with their display names, as " +
			"Matomo gives them; shortened"},
		{Name: "target_attributes", Description: "Target attributes with their supported rule types and example values, " +
			"as Matomo gives them; shortened"},
		{Name: "unavailable", Description: "Options left out because the token may not read them, each with option " +
			"(statuses, success_metrics, target_attributes) and reason permission; absent when every option was read"}},
	Examples: []capability.Example{{Description: "Options for website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// ExperimentOptionGap names an option the token may not read.
type ExperimentOptionGap struct {
	Option string `json:"option"`
	Reason string `json:"reason"`
}

// ExperimentOptionsResult is the answer of experiment-options.list.
type ExperimentOptionsResult struct {
	IDSite           int                   `json:"idSite"`
	Statuses         any                   `json:"statuses,omitempty"`
	SuccessMetrics   any                   `json:"success_metrics,omitempty"`
	TargetAttributes any                   `json:"target_attributes,omitempty"`
	Unavailable      []ExperimentOptionGap `json:"unavailable,omitempty"`
}

func invokeExperimentOptionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list experiment options"
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
	req := experimentsRequirement(experimentOptionsList.ID)
	out := &ExperimentOptionsResult{IDSite: site}
	siteParams := func() url.Values { return url.Values{"idSite": {strconv.Itoa(site)}} }
	steps := []struct {
		option, method string
		params         url.Values
		store          func(any)
	}{
		{"statuses", "getAvailableStatuses", siteParams(), func(v any) { out.Statuses = v }},
		{"success_metrics", "getAvailableSuccessMetrics", siteParams(), func(v any) { out.SuccessMetrics = v }},
		// The reference lists no website for the attributes: they are the same for every website.
		{"target_attributes", "getAvailableTargetAttributes", nil, func(v any) { out.TargetAttributes = v }},
	}
	denied := 0
	var firstDenied error
	for _, step := range steps {
		var answer json.RawMessage
		err := client.call(ctx, op, req, "AbTesting."+step.method, step.params, &answer)
		var failure *provider.Error
		// A refusal for the right of the token leaves the option out and is reported; any other failure ends the
		// call, so nothing is silently missing.
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			denied++
			if firstDenied == nil {
				firstDenied = err
			}
			out.Unavailable = append(out.Unavailable, ExperimentOptionGap{Option: step.option, Reason: optionDenied})
			continue
		}
		if err != nil {
			return nil, err
		}
		step.store(boundedAnswer(answer))
	}
	if denied == len(steps) {
		return nil, firstDenied
	}
	return out, nil
}

func experimentOptionsOperations() []capability.Operation {
	return []capability.Operation{{Descriptor: experimentOptionsList, Handler: capability.Handler(invokeExperimentOptionsList)}}
}
