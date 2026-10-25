package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	sectionReports = "reports"
	sectionMetrics = "metrics"
	// maxGlossaryEntries bounds what one call reads from the glossary of a website.
	maxGlossaryEntries = 2000
)

var reportGlossaryList = capability.Descriptor{
	ID: Provider + ".report-glossary.list", Version: 1, Title: "List the Matomo report glossary",
	Description: "Read what the reports and metrics of a website mean, from the glossary of Matomo " +
		"(API.getGlossaryReports and API.getGlossaryMetrics): section reports explains each report, section " +
		"metrics each metric such as bounce rate, both ordered by name, in batches. The texts depend on the plugins " +
		"of the website and on language and are untrusted",
	Tags: []string{"matomo", "reports", "metrics", "glossary", "documentation"}, Risk: readRisk(reportsSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"section":{"type":"string","enum":["reports","metrics"]},` + languageSchema + `,` + pagingKeys + `},` +
		`"required":["section"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"section":{"type":"string"},"reports":{"type":"array","items":{"type":"object","properties":{` +
		`"name":{"type":"string"},"documentation":{"type":"string"},"online_guide_url":{"type":"string"}},` +
		`"required":["name","documentation"],"additionalProperties":false}},` +
		`"metrics":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"name":{"type":"string"},"documentation":{"type":"string"}},` +
		`"required":["id","name","documentation"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","section","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "section", Required: true, Description: "reports for the explanation of the reports, metrics for the " +
			"explanation of the metrics"},
		languageArgument()}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website whose glossary was read"},
		{Name: "section", Description: "The section that was read"},
		{Name: "reports", Description: "With section reports: the reports that have an explanation, ordered by name"},
		{Name: "reports[].name", Description: "Report name with its category in brackets, untrusted"},
		{Name: "reports[].documentation", Description: "Explanation of the report, untrusted"},
		{Name: "reports[].online_guide_url", Description: "Address of the online guide of the report, untrusted text " +
			"that is never fetched, absent when there is none"},
		{Name: "metrics", Description: "With section metrics: the metrics that have an explanation, ordered by name"},
		{Name: "metrics[].id", Description: "Metric name as the columns of reports show it, such as nb_visits"},
		{Name: "metrics[].name", Description: "Translated metric name, untrusted"},
		{Name: "metrics[].documentation", Description: "Explanation of the metric, untrusted"},
	}, pagingFields...),
	Examples: []capability.Example{
		{Description: "Explain the metrics", Arguments: json.RawMessage(`{"idSite":1,"section":"metrics"}`)},
		{Description: "Explain the reports in German", Arguments: json.RawMessage(
			`{"idSite":1,"section":"reports","language":"de","limit":50}`)},
	},
}

// GlossaryReport explains one report.
type GlossaryReport struct {
	Name           string `json:"name"`
	Documentation  string `json:"documentation"`
	OnlineGuideURL string `json:"online_guide_url,omitempty"`
}

// GlossaryMetric explains one metric.
type GlossaryMetric struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Documentation string `json:"documentation"`
}

// GlossaryResult is the answer of report-glossary.list.
type GlossaryResult struct {
	IDSite     int              `json:"idSite"`
	Section    string           `json:"section"`
	Reports    []GlossaryReport `json:"reports,omitempty"`
	Metrics    []GlossaryMetric `json:"metrics,omitempty"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type glossaryReportJSON struct {
	Name           *loose `json:"name"`
	Documentation  *loose `json:"documentation"`
	OnlineGuideURL *loose `json:"onlineGuideUrl"`
}

type glossaryMetricJSON struct {
	ID            *loose `json:"id"`
	Name          *loose `json:"name"`
	Documentation *loose `json:"documentation"`
}

func invokeReportGlossaryList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list report glossary"
	var args struct {
		IDSite   *int   `json:"idSite"`
		Section  string `json:"section"`
		Language string `json:"language"`
		Limit    int    `json:"limit"`
		Cursor   string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	method := map[string]string{sectionReports: "API.getGlossaryReports", sectionMetrics: "API.getGlossaryMetrics"}[args.Section]
	if method == "" {
		return nil, invalidArgument("section must be reports or metrics")
	}
	if !validLanguage(args.Language) {
		return nil, invalidArgument("language must be a language code such as en or de")
	}
	binding := provider.CursorBinding(reportGlossaryList.ID, scopeBinding(resolved), id, args.Section, args.Language)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if args.Language != "" {
		params.Set("language", args.Language)
	}
	var rows []json.RawMessage
	if err := client.call(ctx, op, baseRequirement(reportGlossaryList.ID), method, params, &rows); err != nil {
		return nil, err
	}
	if len(rows) > maxGlossaryEntries {
		rows = rows[:maxGlossaryEntries]
	}
	batch, next := slice(paging, rows)
	result := &GlossaryResult{IDSite: id, Section: args.Section, HasMore: next != "", NextCursor: next}
	for _, one := range batch {
		if args.Section == sectionReports {
			var entry glossaryReportJSON
			if json.Unmarshal(one, &entry) != nil || entry.Name.str(maxStringLength) == "" {
				continue
			}
			result.Reports = append(result.Reports, GlossaryReport{Name: entry.Name.str(maxStringLength),
				Documentation: entry.Documentation.str(maxReportText), OnlineGuideURL: entry.OnlineGuideURL.str(maxUrlLength)})
			continue
		}
		var entry glossaryMetricJSON
		if json.Unmarshal(one, &entry) != nil || entry.ID.str(maxStringLength) == "" {
			continue
		}
		result.Metrics = append(result.Metrics, GlossaryMetric{ID: entry.ID.str(maxStringLength),
			Name: entry.Name.str(maxStringLength), Documentation: entry.Documentation.str(maxReportText)})
	}
	return result, nil
}
