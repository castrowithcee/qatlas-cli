package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// experimentState is one of the three lifecycle changes of an experiment. Each has a fixed Matomo method.
type experimentState struct {
	action  string
	done    string
	method  string
	op      string
	title   string
	summary string
	// listedOnly keeps a change that acts on the visitors of the website out of a connection's effects.
	listedOnly bool
}

var experimentStates = []experimentState{
	{"start", "started", "AbTesting.startExperiment", "start experiment", "Start a Matomo A/B test experiment",
		"Start an experiment immediately; it then takes effect for the visitors of the website", true},
	{"finish", "finished", "AbTesting.finishExperiment", "finish experiment", "Finish a Matomo A/B test experiment",
		"Finish an experiment so that it no longer accepts new tracking requests", false},
	{"archive", "archived", "AbTesting.archiveExperiment", "archive experiment", "Archive a Matomo A/B test experiment",
		"Archive an experiment so that it is no longer available in reports, segments, or updates", false},
}

func (s experimentState) descriptor() capability.Descriptor {
	listed := ""
	if s.listedOnly {
		listed = "; offered only where a connection's tools list names it"
	}
	return capability.Descriptor{
		ID: Provider + ".experiments." + s.action, Version: 1, Title: s.title,
		Description: s.summary + ". The experiment is read first and must belong to the website, otherwise it is not " +
			"found. Needs the premium plugin AbTesting and, expected, a write right on the website; idempotent in " +
			"effect" + experimentSentOnceGet + listed,
		Tags: []string{"matomo", "experiments", "ab-testing", "update"}, Provider: Provider, RequiresToolAllowList: s.listedOnly,
		Risk: experimentChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idExperimentSchema + `},` +
			`"required":["idExperiment"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
			`"idExperiment":{"type":"integer"},"` + s.done + `":{"type":"boolean"}},` +
			`"required":["idSite","idExperiment","` + s.done + `"],"additionalProperties":false}`),
		Arguments: []capability.Argument{idSiteArgument(),
			{Name: "idExperiment", Required: true, Description: "ID of the experiment, from matomo.experiments.list"}},
		Fields: []capability.Field{
			{Name: "idSite", Description: "The website the experiment belongs to"},
			{Name: "idExperiment", Description: "The experiment that was changed"},
			{Name: s.done, Description: "True once Matomo accepted the change"}},
		Examples: []capability.Example{{Description: s.title + ", experiment 4 of website 1",
			Arguments: json.RawMessage(`{"idSite":1,"idExperiment":4}`)}},
	}
}

// ExperimentStateChanged is the answer of experiments.start, experiments.finish, and experiments.archive;
// exactly one of the flags is present, named after the action.
type ExperimentStateChanged struct {
	IDSite       int  `json:"idSite"`
	IDExperiment int  `json:"idExperiment"`
	Started      bool `json:"started,omitempty"`
	Finished     bool `json:"finished,omitempty"`
	Archived     bool `json:"archived,omitempty"`
}

func (s experimentState) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		IDSite       *int `json:"idSite"`
		IDExperiment *int `json:"idExperiment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkExperimentID(args.IDExperiment); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idExperiment": {strconv.Itoa(*args.IDExperiment)}}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := experimentsRequirement(Provider + ".experiments." + s.action)
	// The experiment ID counts across websites: one read must show it on this website before its state changes.
	if _, err := client.fetchExperiment(ctx, s.op, req, site, *args.IDExperiment); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, s.op, req, s.method, params, experimentSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(s.op, "Matomo returned no confirmation of the experiment", experimentSentOnceGet)
	}
	out := &ExperimentStateChanged{IDSite: site, IDExperiment: *args.IDExperiment}
	switch s.action {
	case "start":
		out.Started = true
	case "finish":
		out.Finished = true
	default:
		out.Archived = true
	}
	return out, nil
}

func experimentStateOperations() []capability.Operation {
	ops := make([]capability.Operation, 0, len(experimentStates))
	for _, s := range experimentStates {
		ops = append(ops, capability.Operation{Descriptor: s.descriptor(), Handler: capability.Handler(s.invoke)})
	}
	return ops
}
