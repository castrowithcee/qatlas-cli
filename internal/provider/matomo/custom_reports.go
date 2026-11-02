package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// customReportsSensitive covers the names, segments, and structure of reports people configured, and the
	// metric formulas of the instance.
	customReportsSensitive = "matomo-custom-reports"
	customReportsPlugin    = "CustomReports"
	maxCustomReportID      = 2147483647
)

func customReportsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: customReportsPlugin}
}

const (
	idCustomReportSchema = `"idCustomReport":{"type":"integer","minimum":1,"maximum":2147483647}`
	customStringList     = `{"type":"array","items":{"type":"string"}}`
	customReportProps    = `"idcustomreport":{"type":"integer"},"idsite":{"type":"integer"},"revision":{"type":"integer"},` +
		`"report_type":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},` +
		`"category":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"order":{"type":"integer"},"icon":{"type":"string"}},"additionalProperties":false},` +
		`"subcategory":{"type":"string"},"subcategory_order":{"type":"integer"},"dimensions":` + customStringList + `,` +
		`"metrics":` + customStringList + `,"segment_filter":{"type":"string"},"created_date":{"type":"string"},` +
		`"updated_date":{"type":"string"},"status":{"type":"string"},"multipleIdSites":{"type":"array","items":{"type":"integer"}},` +
		`"site":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}},"additionalProperties":false},` +
		`"child_reports":{},"allowedToEdit":{"type":"boolean"}`
)

func customReportFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idcustomreport", Description: "Report ID, used as idCustomReport by matomo.custom-reports.get"},
		{Name: prefix + "idsite", Description: "The website the report belongs to"},
		{Name: prefix + "revision", Description: "Revision counter of the report"},
		{Name: prefix + "report_type", Description: "Type of the report, such as table or evolution; the values come " +
			"from matomo.custom-report-options.list"},
		{Name: prefix + "name", Description: "Report name, untrusted"},
		{Name: prefix + "description", Description: "Report description, untrusted"},
		{Name: prefix + "category", Description: "Category the report is listed under: id, and unless " +
			"skipCategoryMetadata is set also name, order, and icon (untrusted)"},
		{Name: prefix + "subcategory", Description: "Subcategory ID the report is listed under, untrusted; absent without one"},
		{Name: prefix + "subcategory_order", Description: "Sort position within the subcategory"},
		{Name: prefix + "dimensions", Description: "Dimension IDs of the report in order, at most 100"},
		{Name: prefix + "metrics", Description: "Metric IDs of the report in order, at most 100"},
		{Name: prefix + "segment_filter", Description: "Segment the report is limited to, untrusted"},
		{Name: prefix + "created_date", Description: "Time the report was created as Matomo stores it"},
		{Name: prefix + "updated_date", Description: "Time the report was last changed as Matomo stores it"},
		{Name: prefix + "status", Description: "Status of the report, such as active or paused"},
		{Name: prefix + "multipleIdSites", Description: "The websites of a report that spans several; it is shown " +
			"only when all of them are inside the connection's targets"},
		{Name: prefix + "site", Description: "The website of the report: id and name (untrusted)"},
		{Name: prefix + "child_reports", Description: "Child reports as Matomo gives them, shortened; only " +
			"matomo.custom-reports.get"},
		{Name: prefix + "allowedToEdit", Description: "Whether the token's user may edit the report"},
	}
}

var customReportsList = capability.Descriptor{
	ID: Provider + ".custom-reports.list", Version: 1, Title: "List Matomo custom reports",
	Description: "List the custom reports configured for a website, in batches. Reports of other websites, reports " +
		"without a website, and reports that span a website outside the connection's targets are not shown. Names, " +
		"descriptions, and segments are untrusted. The data of a report is read with matomo.reports.get. Needs the " +
		"premium plugin CustomReports and a view right on the website",
	Tags: []string{"matomo", "custom-reports", "list"}, Risk: readRisk(customReportsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"skipCategoryMetadata":{"type":"boolean"},` + pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"reports":{"type":"array","items":{"type":"object","properties":{` + customReportProps + `},` +
		`"required":["idcustomreport","idsite"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","reports","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "skipCategoryMetadata", Description: "True to leave the name, order, and icon of the category out and " +
			"keep only its ID; false when omitted"}}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the reports belong to"},
		{Name: "reports", Description: "Reports in the order Matomo lists them; empty without one"}},
		customReportFields("reports[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Custom reports of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var customReportsGet = capability.Descriptor{
	ID: Provider + ".custom-reports.get", Version: 1, Title: "Get a Matomo custom report",
	Description: "Read the configuration of one custom report of a website by its ID. The report ID counts across " +
		"websites, so the answer is shown only when Matomo reports it for this website; a report of another website, " +
		"one without a website, or one that spans a website outside the connection's targets is not found. Names, " +
		"descriptions, and segments are untrusted. Needs the premium plugin CustomReports and a view right on the website",
	Tags: []string{"matomo", "custom-reports", "get"}, Risk: readRisk(customReportsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idCustomReportSchema + `},` +
		`"required":["idCustomReport"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + customReportProps + `},` +
		`"required":["idcustomreport","idsite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idCustomReport", Required: true, Description: "ID of the report, from matomo.custom-reports.list"}},
	Fields:   customReportFields(""),
	Examples: []capability.Example{{Description: "Report 1 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idCustomReport":1}`)}},
}

// CustomReportCategory is the category of a report. All text is untrusted provider data.
type CustomReportCategory struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Order *int   `json:"order,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// CustomReportSite is the website member of a report. The name is untrusted provider data.
type CustomReportSite struct {
	ID   int    `json:"id"`
	Name string `json:"name,omitempty"`
}

// CustomReport is a custom report of a website. All text is untrusted provider data.
type CustomReport struct {
	ID               int                   `json:"idcustomreport"`
	IDSite           int                   `json:"idsite"`
	Revision         *int                  `json:"revision,omitempty"`
	ReportType       string                `json:"report_type,omitempty"`
	Name             string                `json:"name,omitempty"`
	Description      string                `json:"description,omitempty"`
	Category         *CustomReportCategory `json:"category,omitempty"`
	Subcategory      string                `json:"subcategory,omitempty"`
	SubcategoryOrder *int                  `json:"subcategory_order,omitempty"`
	Dimensions       []string              `json:"dimensions,omitempty"`
	Metrics          []string              `json:"metrics,omitempty"`
	SegmentFilter    string                `json:"segment_filter,omitempty"`
	CreatedDate      string                `json:"created_date,omitempty"`
	UpdatedDate      string                `json:"updated_date,omitempty"`
	Status           string                `json:"status,omitempty"`
	MultipleIDSites  []int                 `json:"multipleIdSites,omitempty"`
	Site             *CustomReportSite     `json:"site,omitempty"`
	ChildReports     any                   `json:"child_reports,omitempty"`
	AllowedToEdit    *bool                 `json:"allowedToEdit,omitempty"`
}

// CustomReportsResult is the answer of custom-reports.list.
type CustomReportsResult struct {
	IDSite     int            `json:"idSite"`
	Reports    []CustomReport `json:"reports"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// customReportJSON is a report as CustomReports gives it. Members are read loosely and none is trusted. The
// members created_by, updated_by, and the *_pretty dates are not read: the first two are logins of users.
type customReportJSON struct {
	ID               *loose          `json:"idcustomreport"`
	IDSite           *loose          `json:"idsite"`
	Revision         *loose          `json:"revision"`
	ReportType       *loose          `json:"report_type"`
	Name             *loose          `json:"name"`
	Description      *loose          `json:"description"`
	Category         json.RawMessage `json:"category"`
	Subcategory      json.RawMessage `json:"subcategory"`
	SubcategoryOrder *loose          `json:"subcategory_order"`
	Dimensions       json.RawMessage `json:"dimensions"`
	Metrics          json.RawMessage `json:"metrics"`
	SegmentFilter    *loose          `json:"segment_filter"`
	CreatedDate      *loose          `json:"created_date"`
	UpdatedDate      *loose          `json:"updated_date"`
	Status           *loose          `json:"status"`
	MultipleLegacy   json.RawMessage `json:"multiple_idsites"`
	MultipleIDSites  json.RawMessage `json:"multipleIdSites"`
	Site             json.RawMessage `json:"site"`
	ChildReports     json.RawMessage `json:"child_reports"`
	AllowedToEdit    *loose          `json:"allowedToEdit"`
}

func emptyAnswer(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "null", "false", "[]", "{}", `""`, "":
		return true
	}
	return false
}

// reportStrings reads a list of IDs; an unreadable list is empty.
func reportStrings(raw json.RawMessage) []string {
	rows, valid := readRows[loose](raw)
	if !valid {
		return nil
	}
	var out []string
	for i := range rows {
		if len(out) == maxResultEntries {
			break
		}
		out = append(out, rows[i].str(maxStringLength))
	}
	return out
}

func reportCategory(raw json.RawMessage) *CustomReportCategory {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case bytes.HasPrefix(trimmed, []byte(`"`)):
		// With skipCategoryMetadata Matomo gives the category ID alone.
		if id := funnelText(trimmed, maxStringLength); id != "" {
			return &CustomReportCategory{ID: id}
		}
	case bytes.HasPrefix(trimmed, []byte("{")):
		var row struct {
			ID    *loose `json:"id"`
			Name  *loose `json:"name"`
			Order *loose `json:"order"`
			Icon  *loose `json:"icon"`
		}
		if json.Unmarshal(trimmed, &row) == nil {
			return &CustomReportCategory{ID: row.ID.str(maxStringLength), Name: row.Name.str(maxStringLength),
				Order: row.Order.number(), Icon: row.Icon.str(maxStringLength)}
		}
	}
	return nil
}

func reportSite(raw json.RawMessage) *CustomReportSite {
	var row struct {
		ID   *loose `json:"id"`
		Name *loose `json:"name"`
	}
	trimmed := bytes.TrimSpace(raw)
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &row) != nil {
		return nil
	}
	id := row.ID.number()
	if id == nil {
		return nil
	}
	return &CustomReportSite{ID: *id, Name: row.Name.str(maxStringLength)}
}

// spannedSites reads the websites a report spans from the legacy member and the array member. ok is false for
// anything but websites named by positive integers, such as a word for all websites: the report then counts as
// reaching beyond the targets.
func spannedSites(raws ...json.RawMessage) (ids []int, ok bool) {
	for _, raw := range raws {
		if emptyAnswer(raw) {
			continue
		}
		var items []json.RawMessage
		trimmed := bytes.TrimSpace(raw)
		if bytes.HasPrefix(trimmed, []byte("[")) {
			if json.Unmarshal(trimmed, &items) != nil {
				return nil, false
			}
		} else {
			var text loose
			if json.Unmarshal(trimmed, &text) != nil {
				return nil, false
			}
			for _, part := range strings.Split(text.text, ",") {
				items = append(items, json.RawMessage(strconv.Quote(strings.TrimSpace(part))))
			}
		}
		for _, item := range items {
			var one loose
			if json.Unmarshal(item, &one) != nil {
				return nil, false
			}
			id := one.number()
			if id == nil || *id < 1 {
				return nil, false
			}
			ids = append(ids, *id)
		}
	}
	return ids, true
}

// report converts a row. belongs is false for a row of another website, a row without a website, and a report
// that spans a website outside the targets; ok is false for a row without a usable report ID.
func (r customReportJSON) report(bound scope, site int) (report CustomReport, belongs, ok bool) {
	id := r.ID.number()
	if id == nil || *id < 1 {
		return CustomReport{}, false, false
	}
	if row := r.IDSite.number(); row == nil || *row != site {
		return CustomReport{}, false, true
	}
	spanned, valid := spannedSites(r.MultipleLegacy, r.MultipleIDSites)
	if !valid {
		return CustomReport{}, false, true
	}
	for _, other := range spanned {
		if !bound.allows(other) {
			return CustomReport{}, false, true
		}
	}
	var child any
	if !emptyAnswer(r.ChildReports) {
		child = boundedAnswer(r.ChildReports)
	}
	return CustomReport{ID: *id, IDSite: site, Revision: r.Revision.number(), ReportType: r.ReportType.str(maxStringLength),
		Name: r.Name.str(maxStringLength), Description: r.Description.str(maxUrlLength), Category: reportCategory(r.Category),
		Subcategory: funnelText(r.Subcategory, maxStringLength), SubcategoryOrder: r.SubcategoryOrder.number(),
		Dimensions: reportStrings(r.Dimensions), Metrics: reportStrings(r.Metrics),
		SegmentFilter: r.SegmentFilter.str(maxUrlLength), CreatedDate: r.CreatedDate.str(maxStringLength),
		UpdatedDate: r.UpdatedDate.str(maxStringLength), Status: r.Status.str(maxStringLength),
		MultipleIDSites: spanned, Site: reportSite(r.Site), ChildReports: child, AllowedToEdit: r.AllowedToEdit.flag()}, true, true
}

func customReportNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no such custom report for this website inside the targets of this connection"}
}

func invokeCustomReportsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list custom reports"
	var args struct {
		IDSite               *int   `json:"idSite"`
		SkipCategoryMetadata bool   `json:"skipCategoryMetadata"`
		Limit                int    `json:"limit"`
		Cursor               string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(customReportsList.ID, scopeBinding(resolved), site,
		args.SkipCategoryMetadata), args.Limit, args.Cursor)
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
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if args.SkipCategoryMetadata {
		params.Set("skipCategoryMetadata", "1")
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, customReportsRequirement(customReportsList.ID),
		"CustomReports.getConfiguredReports", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[customReportJSON](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid custom report list")
	}
	reports := []CustomReport{}
	for i := range rows {
		if report, belongs, usable := rows[i].report(bound, site); usable && belongs {
			reports = append(reports, report)
		}
	}
	batch, next := slice(paging, reports)
	return &CustomReportsResult{IDSite: site, Reports: batch, HasMore: next != "", NextCursor: next}, nil
}

func invokeCustomReportsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get custom report"
	var args struct {
		IDSite         *int `json:"idSite"`
		IDCustomReport *int `json:"idCustomReport"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.IDCustomReport == nil || *args.IDCustomReport < 1 || *args.IDCustomReport > maxCustomReportID {
		return nil, invalidArgument("idCustomReport must be a positive integer report ID from matomo.custom-reports.list")
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
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idCustomReport": {strconv.Itoa(*args.IDCustomReport)}}
	var answer json.RawMessage
	if err := client.call(ctx, op, customReportsRequirement(customReportsGet.ID),
		"CustomReports.getConfiguredReport", params, &answer); err != nil {
		return nil, err
	}
	if emptyAnswer(answer) {
		return nil, customReportNotFound(op)
	}
	trimmed := bytes.TrimSpace(answer)
	var row customReportJSON
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &row) != nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid custom report")
	}
	report, belongs, ok := row.report(bound, site)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid custom report")
	}
	if !belongs {
		return nil, customReportNotFound(op)
	}
	if report.ID != *args.IDCustomReport {
		return nil, provider.InvalidResponse(op, "Matomo returned another custom report than asked for")
	}
	return &report, nil
}

func customReportsOperations() []capability.Operation {
	return append([]capability.Operation{
		{Descriptor: customReportsList, Handler: capability.Handler(invokeCustomReportsList)},
		{Descriptor: customReportsGet, Handler: capability.Handler(invokeCustomReportsGet)},
	}, append(customReportOptionsOperations(), customMetricsOperations()...)...)
}
