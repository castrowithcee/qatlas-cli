package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	templateExperiment = "experiment"
	templateInclude    = "include"
)

var experimentTemplatesGet = capability.Descriptor{
	ID: Provider + ".experiment-templates.get", Version: 1, Title: "Get a Matomo A/B test code template",
	Description: "Read a JavaScript snippet that Matomo generates for A/B tests, as text: kind experiment gives the " +
		"snippet that runs one experiment of a website (getJsExperimentTemplate), kind include gives the script tag " +
		"that loads the A/B testing tracker (getJsIncludeTemplate). The snippet is untrusted text: it is shortened, " +
		"never run, and never interpreted. It is empty when Matomo has nothing to embed, such as for a finished " +
		"experiment. For kind experiment the experiment is read first and must belong to the website, otherwise it " +
		"is not found. Needs the premium plugin AbTesting and a right on the website",
	Tags: []string{"matomo", "experiments", "ab-testing", "template", "get"}, Risk: readRisk(experimentsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idExperimentSchema + `,` +
		`"kind":{"type":"string","enum":["experiment","include"]}},"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"idSite":{"type":"integer"},` +
		`"idExperiment":{"type":"integer"},"template":{"type":"string"},"truncated":{"type":"boolean"}},` +
		`"required":["kind","template"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "kind", Required: true, Description: "experiment for the snippet of one experiment, include for the script " +
			"tag of the tracker"},
		idSiteArgument(),
		{Name: "idExperiment", Description: "ID of the experiment, from matomo.experiments.list; required with kind " +
			"experiment, not allowed with include"}},
	Fields: []capability.Field{
		{Name: "kind", Description: "The kind read"},
		{Name: "idSite", Description: "The website of the experiment; only for kind experiment"},
		{Name: "idExperiment", Description: "The experiment; only for kind experiment"},
		{Name: "template", Description: "JavaScript or HTML text as Matomo generates it, untrusted and never run; empty " +
			"when Matomo has nothing to embed, shortened to " + strconv.Itoa(maxSnippetText) + " bytes"},
		{Name: "truncated", Description: "True when the text was shortened; absent otherwise"}},
	Examples: []capability.Example{
		{Description: "Snippet of experiment 1 of website 1", Arguments: json.RawMessage(
			`{"kind":"experiment","idSite":1,"idExperiment":1}`)},
		{Description: "Script tag of the tracker", Arguments: json.RawMessage(`{"kind":"include"}`)}},
}

// ExperimentTemplateResult is the answer of experiment-templates.get.
type ExperimentTemplateResult struct {
	Kind         string `json:"kind"`
	IDSite       int    `json:"idSite,omitempty"`
	IDExperiment int    `json:"idExperiment,omitempty"`
	Template     string `json:"template"`
	Truncated    bool   `json:"truncated,omitempty"`
}

func invokeExperimentTemplatesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get experiment template"
	var args struct {
		Kind         string `json:"kind"`
		IDSite       *int   `json:"idSite"`
		IDExperiment *int   `json:"idExperiment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	out := &ExperimentTemplateResult{Kind: args.Kind}
	var site int
	switch args.Kind {
	case templateExperiment:
		if args.IDExperiment == nil || *args.IDExperiment < 1 || *args.IDExperiment > maxExperimentID {
			return nil, invalidArgument("idExperiment must be a positive integer experiment ID from matomo.experiments.list")
		}
		var err error
		if site, err = selectSite(resolved, args.IDSite); err != nil {
			return nil, err
		}
		out.IDSite, out.IDExperiment = site, *args.IDExperiment
	case templateInclude:
		// The method names no website; the call is offered only where the connection binds one.
		if args.IDSite != nil || args.IDExperiment != nil {
			return nil, invalidArgument("idSite and idExperiment are used only with kind " + templateExperiment)
		}
		if err := websiteBound(resolved); err != nil {
			return nil, err
		}
	default:
		return nil, invalidArgument("kind must be " + templateExperiment + " or " + templateInclude)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := experimentsRequirement(experimentTemplatesGet.ID)
	method, params := "AbTesting.getJsIncludeTemplate", url.Values{}
	if args.Kind == templateExperiment {
		// The template method is not known to check the experiment against the website, so the experiment is
		// proven for the website first.
		if _, err := client.fetchExperiment(ctx, op, req, site, out.IDExperiment); err != nil {
			return nil, err
		}
		method = "AbTesting.getJsExperimentTemplate"
		params = url.Values{"idSite": {strconv.Itoa(site)}, "idExperiment": {strconv.Itoa(out.IDExperiment)}}
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, method, params, &answer); err != nil {
		return nil, err
	}
	var text string
	switch value := unwrapValue(answer); string(value) {
	case "null", "false", "":
	default:
		if json.Unmarshal(value, &text) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid template")
		}
	}
	out.Template = boundString(text, maxSnippetText)
	out.Truncated = len(out.Template) < len(text)
	return out, nil
}

func experimentTemplatesOperations() []capability.Operation {
	return []capability.Operation{{Descriptor: experimentTemplatesGet, Handler: capability.Handler(invokeExperimentTemplatesGet)}}
}
