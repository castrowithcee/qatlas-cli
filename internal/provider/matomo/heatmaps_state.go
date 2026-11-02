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

// heatmapState is one of the three state changes of a heatmap. Each has a fixed Matomo method.
type heatmapState struct {
	action      string
	method      string
	op          string
	title       string
	summary     string
	idempotency capability.Idempotency
}

var heatmapStates = []heatmapState{
	{"pause", "HeatmapSessionRecording.pauseHeatmap", "pause heatmap", "Pause a Matomo heatmap",
		"Pause the recording of a heatmap so that it collects no further page views; the configuration and the " +
			"data stay. Resume it with matomo.heatmaps.resume", capability.IdempotencyIdempotent},
	{"resume", "HeatmapSessionRecording.resumeHeatmap", "resume heatmap", "Resume a Matomo heatmap",
		"Resume the recording of a paused heatmap so that it collects page views again", capability.IdempotencyIdempotent},
	{"end", "HeatmapSessionRecording.endHeatmap", "end heatmap", "End a Matomo heatmap",
		"End (finish) the recording of a heatmap; the configuration and the collected data stay. Matomo does not " +
			"document whether an ended heatmap can be resumed", capability.IdempotencyIdempotent},
}

func (s heatmapState) descriptor() capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".heatmaps." + s.action, Version: 1, Title: s.title,
		Description: s.summary + ". The heatmap is read first and must belong to the website, otherwise it is not " +
			"found. Needs the premium plugin HeatmapSessionRecording and, expected, a write right on the website; " +
			"idempotent in effect" + heatmapSentOnceGet,
		Tags: []string{"matomo", "heatmaps", "behavior", "update"},
		Risk: heatmapChangeRisk(capability.EffectUpdate, s.idempotency), Provider: Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idHeatmapSchema + `},` +
			`"required":["idSiteHsr"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
			`"idSiteHsr":{"type":"integer"},"` + s.done() + `":{"type":"boolean"}},` +
			`"required":["idSite","idSiteHsr","` + s.done() + `"],"additionalProperties":false}`),
		Arguments: []capability.Argument{idSiteArgument(),
			{Name: "idSiteHsr", Required: true, Description: "ID of the heatmap, from matomo.heatmaps.list"}},
		Fields: []capability.Field{
			{Name: "idSite", Description: "The website the heatmap belongs to"},
			{Name: "idSiteHsr", Description: "The heatmap that was changed"},
			{Name: s.done(), Description: "True once Matomo accepted the change"}},
		Examples: []capability.Example{{Description: s.title + ", heatmap 11 of website 1",
			Arguments: json.RawMessage(`{"idSite":1,"idSiteHsr":11}`)}},
	}
}

func (s heatmapState) done() string {
	switch s.action {
	case "pause":
		return "paused"
	case "resume":
		return "resumed"
	}
	return "ended"
}

// HeatmapStateChanged is the answer of heatmaps.pause, heatmaps.resume, and heatmaps.end; exactly one of the
// flags is present, named after the action.
type HeatmapStateChanged struct {
	IDSite    int  `json:"idSite"`
	IDSiteHsr int  `json:"idSiteHsr"`
	Paused    bool `json:"paused,omitempty"`
	Resumed   bool `json:"resumed,omitempty"`
	Ended     bool `json:"ended,omitempty"`
}

func (s heatmapState) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		IDSite    *int `json:"idSite"`
		IDSiteHsr *int `json:"idSiteHsr"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkHeatmapID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)}}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(Provider + ".heatmaps." + s.action)
	// The heatmap ID counts across websites: one read must show it on this website before its state changes.
	if _, err := client.fetchHeatmap(ctx, s.op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, s.op, req, s.method, params, heatmapSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(s.op, "Matomo returned no confirmation of the heatmap", heatmapSentOnceGet)
	}
	out := &HeatmapStateChanged{IDSite: site, IDSiteHsr: *args.IDSiteHsr}
	switch s.action {
	case "pause":
		out.Paused = true
	case "resume":
		out.Resumed = true
	default:
		out.Ended = true
	}
	return out, nil
}

func heatmapStateOperations() []capability.Operation {
	ops := make([]capability.Operation, 0, len(heatmapStates))
	for _, s := range heatmapStates {
		ops = append(ops, capability.Operation{Descriptor: s.descriptor(), Handler: capability.Handler(s.invoke)})
	}
	return ops
}
