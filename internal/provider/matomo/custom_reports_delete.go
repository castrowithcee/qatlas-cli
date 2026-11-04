package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

var customReportsDelete = capability.Descriptor{
	ID: Provider + ".custom-reports.delete", Version: 1, Title: "Delete a Matomo custom report",
	Description: "Delete the definition of a custom report of a website; Qatlas Connect offers no way to restore it. Reports " +
		"shared with all websites (idSite all in Matomo) are not reachable. The report is read first and must belong " +
		"to the website, otherwise it is not found. Needs the premium plugin CustomReports and, expected, a write " +
		"right on the website; idempotent in effect" + customReportSentOnceGet + "; offered only where a " +
		"connection's tools list names it",
	Tags: []string{"matomo", "custom-reports", "delete"}, Provider: Provider, RequiresToolAllowList: true,
	Risk: customReportChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idCustomReportSchema + `},` +
		`"required":["idCustomReport"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idCustomReport":{"type":"integer"},"deleted":{"type":"boolean"}},` +
		`"required":["idSite","idCustomReport","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idCustomReport", Required: true, Description: "ID of the report to delete, from matomo.custom-reports.list"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the report belonged to"},
		{Name: "idCustomReport", Description: "The report that was deleted"},
		{Name: "deleted", Description: "True once Matomo accepted the deletion"}},
	Examples: []capability.Example{{Description: "Delete report 3 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idCustomReport":3}`)}},
}

// CustomReportDeleted is the answer of custom-reports.delete.
type CustomReportDeleted struct {
	IDSite         int  `json:"idSite"`
	IDCustomReport int  `json:"idCustomReport"`
	Deleted        bool `json:"deleted"`
}

func invokeCustomReportsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete custom report"
	var args struct {
		IDSite         *int `json:"idSite"`
		IDCustomReport *int `json:"idCustomReport"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkCustomReportID(args.IDCustomReport); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idCustomReport": {strconv.Itoa(*args.IDCustomReport)}}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := customReportsRequirement(customReportsDelete.ID)
	if err := provenReport(ctx, client, resolved, op, req, site, *args.IDCustomReport); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "CustomReports.deleteCustomReport", params, customReportSentOnceGet,
		&answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", customReportSentOnceGet)
	}
	return &CustomReportDeleted{IDSite: site, IDCustomReport: *args.IDCustomReport, Deleted: true}, nil
}
