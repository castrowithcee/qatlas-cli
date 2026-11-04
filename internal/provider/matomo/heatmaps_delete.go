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

// heatmapDeletion is one of the two deletions of a heatmap. Each has a fixed Matomo method.
type heatmapDeletion struct {
	action        string
	method        string
	op            string
	title         string
	summary       string
	captureOption bool
}

var heatmapDeletions = []heatmapDeletion{
	{action: "delete", method: "HeatmapSessionRecording.deleteHeatmap", op: "delete heatmap",
		title: "Delete a Matomo heatmap",
		summary: "Delete a heatmap with its configuration; Qatlas Connect offers no way to restore it. Matomo does not " +
			"document what happens to the recorded data"},
	{action: "delete-screenshot", method: "HeatmapSessionRecording.deleteHeatmapScreenshot", op: "delete heatmap screenshot",
		title:   "Delete the screenshot of a Matomo heatmap",
		summary: "Delete the stored page screenshot of a heatmap; Qatlas Connect offers no way to restore it, and the heatmap itself stays", captureOption: true},
}

func (d heatmapDeletion) descriptor() capability.Descriptor {
	arguments := []capability.Argument{idSiteArgument(),
		{Name: "idSiteHsr", Required: true, Description: "ID of the heatmap, from matomo.heatmaps.list"}}
	properties := idSiteSchema + `,` + idHeatmapSchema
	examples := `{"idSite":1,"idSiteHsr":11}`
	extra := ""
	if d.captureOption {
		arguments = append(arguments, capability.Argument{Name: "captureManually", Description: "Matomo's captureManually " +
			"option of the screenshot; sent only when given, so that Matomo applies its default (false). Needs " +
			"HeatmapSessionRecording 5.6.0 or later, older builds may refuse it"})
		properties += `,"captureManually":{"type":"boolean"}`
		extra = " captureManually is sent only when given and needs plugin 5.6.0 or later."
	}
	return capability.Descriptor{
		ID: Provider + ".heatmaps." + d.action, Version: 1, Title: d.title,
		Description: d.summary + ". The heatmap is read first and must belong to the website, otherwise it is not " +
			"found." + extra + " Needs the premium plugin HeatmapSessionRecording and, expected, a write right on the website; " +
			"idempotent in effect" + heatmapSentOnceGet + "; offered only where a connection's tools list names it",
		Tags: []string{"matomo", "heatmaps", "behavior", "delete"}, Provider: Provider, RequiresToolAllowList: true,
		Risk: heatmapChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + properties + `},` +
			`"required":["idSiteHsr"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
			`"idSiteHsr":{"type":"integer"},"deleted":{"type":"boolean"}},` +
			`"required":["idSite","idSiteHsr","deleted"],"additionalProperties":false}`),
		Arguments: arguments,
		Fields: []capability.Field{
			{Name: "idSite", Description: "The website the heatmap belongs to"},
			{Name: "idSiteHsr", Description: "The heatmap that was changed"},
			{Name: "deleted", Description: "True once Matomo accepted the deletion"}},
		Examples: []capability.Example{{Description: d.title + ", heatmap 11 of website 1",
			Arguments: json.RawMessage(examples)}},
	}
}

// HeatmapDeleted is the answer of heatmaps.delete and heatmaps.delete-screenshot.
type HeatmapDeleted struct {
	IDSite    int  `json:"idSite"`
	IDSiteHsr int  `json:"idSiteHsr"`
	Deleted   bool `json:"deleted"`
}

func (d heatmapDeletion) invoke(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		IDSite          *int  `json:"idSite"`
		IDSiteHsr       *int  `json:"idSiteHsr"`
		CaptureManually *bool `json:"captureManually"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkHeatmapID(args.IDSiteHsr); err != nil {
		return nil, err
	}
	if args.CaptureManually != nil && !d.captureOption {
		return nil, invalidArgument("captureManually is not an argument of this tool")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idSiteHsr": {strconv.Itoa(*args.IDSiteHsr)}}
	if args.CaptureManually != nil {
		// Older plugin builds do not know the parameter, so it goes out only when the caller asks for it.
		params.Set("captureManually", flagValue(args.CaptureManually))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := heatmapsRequirement(Provider + ".heatmaps." + d.action)
	// The heatmap ID counts across websites: one read must show it on this website before anything is deleted.
	if _, err := client.fetchHeatmap(ctx, d.op, req, site, *args.IDSiteHsr); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, d.op, req, d.method, params, heatmapSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !d.deleted(answer) {
		if d.captureOption && isBooleanFalse(answer) {
			return nil, provider.Fail(d.op, "Matomo did not delete the screenshot")
		}
		return nil, unclearChange(d.op, "Matomo returned no confirmation of the deletion", heatmapSentOnceGet)
	}
	return &HeatmapDeleted{IDSite: site, IDSiteHsr: *args.IDSiteHsr, Deleted: true}, nil
}

// deleted reads the answer. The reference describes the screenshot method only as a boolean, so the generic
// success form and true both count for it.
func (d heatmapDeletion) deleted(answer []byte) bool {
	if isSuccessAnswer(answer) {
		return true
	}
	if !d.captureOption {
		return false
	}
	value, ok := booleanAnswer(answer)
	return ok && value
}

func isBooleanFalse(answer []byte) bool {
	value, ok := booleanAnswer(answer)
	return ok && !value
}

// booleanAnswer reads a bare boolean or Matomo's {"value":bool} form.
func booleanAnswer(answer []byte) (value, ok bool) {
	var bare bool
	if json.Unmarshal(answer, &bare) == nil {
		return bare, true
	}
	var wrapped struct {
		Value *bool `json:"value"`
	}
	if json.Unmarshal(answer, &wrapped) == nil && wrapped.Value != nil {
		return *wrapped.Value, true
	}
	return false, false
}

func heatmapDeleteOperations() []capability.Operation {
	ops := make([]capability.Operation, 0, len(heatmapDeletions))
	for _, d := range heatmapDeletions {
		ops = append(ops, capability.Operation{Descriptor: d.descriptor(), Handler: capability.Handler(d.invoke)})
	}
	return ops
}
