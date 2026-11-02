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

const (
	// optionNoInstance is the reason of an option that needs the instance target the connection lacks; it is
	// named without a request.
	optionNoInstance = "instance-target"
	subOptionProps   = `{"type":"object","properties":{"uniqueId":{"type":"string"},"name":{"type":"string"}},` +
		`"additionalProperties":false}`
)

var customReportOptionsList = capability.Descriptor{
	ID: Provider + ".custom-report-options.list", Version: 1, Title: "List Matomo custom report options",
	Description: "List what a custom report of a website can be built from: the categories and subcategories it can " +
		"be listed under, the report types, the dimensions and the metrics, and for connections that bind the " +
		"instance also the metrics a custom metric can be built from. The rights differ by option and are not " +
		"checked in advance: an option the token may not read is listed in unavailable with reason permission, " +
		"one that needs the instance target the connection lacks with reason instance-target (without a request), " +
		"and the call fails with a permission error when every requested option is refused. Any other failure ends " +
		"the call. Answers without a verified example are returned as Matomo gives them, shortened. Needs the " +
		"premium plugin CustomReports and a view right on the website",
	Tags: []string{"matomo", "custom-reports", "reference", "list"}, Risk: readRisk(customReportsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"categories":{"type":"array","items":{"type":"object","properties":{"uniqueId":{"type":"string"},` +
		`"name":{"type":"string"},"subcategories":{"type":"array","items":` + subOptionProps + `}},` +
		`"additionalProperties":false}},"report_types":{},"dimensions":{},"metrics":{},"custom_metric_metrics":{},` +
		`"unavailable":{"type":"array","items":{"type":"object","properties":{"option":{"type":"string"},` +
		`"reason":{"type":"string"}},"required":["option","reason"],"additionalProperties":false}}},` +
		`"required":["idSite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the options are resolved for"},
		{Name: "categories", Description: "Categories a report can be listed under, at most 100, each with uniqueId, " +
			"name (untrusted), and subcategories (at most 100: uniqueId and name); read with view right"},
		{Name: "report_types", Description: "Report types by identifier and translated name as Matomo gives them, " +
			"such as table or evolution; shortened; needs write right on at least one website"},
		{Name: "dimensions", Description: "Dimensions a report can use, grouped as Matomo gives them; shortened; " +
			"needs a logged-in user"},
		{Name: "metrics", Description: "Metrics a report can use, grouped as Matomo gives them; shortened; needs a " +
			"logged-in user"},
		{Name: "custom_metric_metrics", Description: "Metrics a custom metric can be built from as Matomo gives " +
			"them; shortened; only with a connection that binds the instance, and needs Super User rights"},
		{Name: "unavailable", Description: "Options left out, each with option (categories, report_types, " +
			"dimensions, metrics, custom_metric_metrics) and reason permission or instance-target; absent when every " +
			"option was read"}},
	Examples: []capability.Example{{Description: "Options for website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// CustomReportSubcategory is a subcategory a report can be listed under. The name is untrusted provider data.
type CustomReportSubcategory struct {
	UniqueID string `json:"uniqueId"`
	Name     string `json:"name,omitempty"`
}

// CustomReportCategoryOption is a category a report can be listed under. The name is untrusted provider data.
type CustomReportCategoryOption struct {
	UniqueID      string                    `json:"uniqueId"`
	Name          string                    `json:"name,omitempty"`
	Subcategories []CustomReportSubcategory `json:"subcategories,omitempty"`
}

// CustomReportOptionGap names an option that is not in the result.
type CustomReportOptionGap struct {
	Option string `json:"option"`
	Reason string `json:"reason"`
}

// CustomReportOptionsResult is the answer of custom-report-options.list.
type CustomReportOptionsResult struct {
	IDSite              int                          `json:"idSite"`
	Categories          []CustomReportCategoryOption `json:"categories,omitempty"`
	ReportTypes         any                          `json:"report_types,omitempty"`
	Dimensions          any                          `json:"dimensions,omitempty"`
	Metrics             any                          `json:"metrics,omitempty"`
	CustomMetricMetrics any                          `json:"custom_metric_metrics,omitempty"`
	Unavailable         []CustomReportOptionGap      `json:"unavailable,omitempty"`
}

type categoryOptionJSON struct {
	UniqueID      *loose          `json:"uniqueId"`
	Name          *loose          `json:"name"`
	Subcategories json.RawMessage `json:"subcategories"`
}

func categoryOptions(raw json.RawMessage) ([]CustomReportCategoryOption, bool) {
	rows, valid := readRows[categoryOptionJSON](raw)
	if !valid {
		return nil, false
	}
	var out []CustomReportCategoryOption
	for _, row := range rows {
		if len(out) == maxResultEntries {
			break
		}
		subs, valid := readRows[categoryOptionJSON](row.Subcategories)
		if !valid {
			return nil, false
		}
		category := CustomReportCategoryOption{UniqueID: row.UniqueID.str(maxStringLength), Name: row.Name.str(maxStringLength)}
		for _, sub := range subs {
			if len(category.Subcategories) == maxResultEntries {
				break
			}
			category.Subcategories = append(category.Subcategories, CustomReportSubcategory{
				UniqueID: sub.UniqueID.str(maxStringLength), Name: sub.Name.str(maxStringLength)})
		}
		out = append(out, category)
	}
	return out, true
}

func invokeCustomReportOptionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list custom report options"
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
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := customReportsRequirement(customReportOptionsList.ID)
	out := &CustomReportOptionsResult{IDSite: site}
	asked, denied := 0, 0
	var firstDenied error
	// ask runs one method. A refusal for the right of the token leaves the option out and is reported; any other
	// failure ends the call, so nothing is silently missing.
	ask := func(option, method string, params url.Values) (json.RawMessage, bool, error) {
		asked++
		var answer json.RawMessage
		err := client.call(ctx, op, req, "CustomReports."+method, params, &answer)
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			denied++
			if firstDenied == nil {
				firstDenied = err
			}
			out.Unavailable = append(out.Unavailable, CustomReportOptionGap{Option: option, Reason: optionDenied})
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
		{"categories", "getAvailableCategories", siteParams(), func(a json.RawMessage) (ok bool) {
			out.Categories, ok = categoryOptions(a)
			return
		}},
		{"report_types", "getAvailableReportTypes", nil, func(a json.RawMessage) bool { out.ReportTypes = boundedAnswer(a); return true }},
		{"dimensions", "getAvailableDimensions", siteParams(), func(a json.RawMessage) bool { out.Dimensions = boundedAnswer(a); return true }},
		{"metrics", "getAvailableMetrics", siteParams(), func(a json.RawMessage) bool { out.Metrics = boundedAnswer(a); return true }},
		{"custom_metric_metrics", "getAvailableMetricsForCustomMetric", siteParams(), func(a json.RawMessage) bool {
			out.CustomMetricMetrics = boundedAnswer(a)
			return true
		}},
	}
	for _, step := range steps {
		if step.option == "custom_metric_metrics" && !bound.instance {
			out.Unavailable = append(out.Unavailable, CustomReportOptionGap{Option: step.option, Reason: optionNoInstance})
			continue
		}
		answer, got, err := ask(step.option, step.method, step.params)
		if err != nil {
			return nil, err
		}
		if got && !step.store(answer) {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
		}
	}
	if denied == asked {
		return nil, firstDenied
	}
	return out, nil
}

func customReportOptionsOperations() []capability.Operation {
	return []capability.Operation{{Descriptor: customReportOptionsList, Handler: capability.Handler(invokeCustomReportOptionsList)}}
}
