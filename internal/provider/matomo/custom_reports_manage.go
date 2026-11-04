package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	maxCustomReportName  = 255
	maxCustomReportItems = 100
	maxCustomReportSites = 20
	// customReportIdentifier is the form of report types, metric, dimension, category, and subcategory IDs.
	customReportIdentifier = `^[A-Za-z0-9_.-]{1,128}$`
)

var (
	customReportIdentifierPattern = regexp.MustCompile(customReportIdentifier)
	customReportSentOnceList      = sentOnceFor("custom report", "matomo.custom-reports.list")
	customReportSentOnceGet       = sentOnceFor("custom report", "matomo.custom-reports.get")
)

// customReportChangeRisk is the risk of every custom report change: confirmed, open to the instance, and about
// the report configuration of a website.
func customReportChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: customReportsSensitive}
}

const (
	customIdentifierSchema = `{"type":"string","pattern":"` + customReportIdentifier + `"}`
	customIdentifierList   = `{"type":"array","minItems":1,"maxItems":100,"uniqueItems":true,"items":` + customIdentifierSchema + `}`
	customSiteListSchema   = `{"type":"array","minItems":1,"maxItems":20,"uniqueItems":true,` +
		`"items":{"type":"integer","minimum":1,"maximum":999999999}}`
	customDefinitionSchema = `"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"reportType":{"type":"string","pattern":"` + customReportIdentifier + `"},"metricIds":` + customIdentifierList + `,` +
		`"categoryId":` + customIdentifierSchema + `,"dimensionIds":` + customIdentifierList + `,` +
		`"subcategoryId":` + customIdentifierSchema + `,"description":{"type":"string","maxLength":2048},` +
		`"segmentFilter":{"type":"string","maxLength":2048},"multipleIdSites":` + customSiteListSchema
	customDefinitionHint = "Optional settings that are not given are not sent, so Matomo applies its own defaults " +
		"(by its signature: the default category of custom reports, no dimensions, a new reporting page, no " +
		"description, no segment, no further websites)"
)

func customDefinitionArguments(replaces bool) []capability.Argument {
	given := "; Matomo's default when omitted"
	if replaces {
		given = "; reset to Matomo's default when omitted"
	}
	return []capability.Argument{
		{Name: "name", Required: true, Description: "Name of the report, 1 to 255 characters"},
		{Name: "reportType", Required: true, Description: "Type of the report, such as table or evolution (letters, " +
			"digits, underscore, dot, hyphen; values from matomo.custom-report-options.list)"},
		{Name: "metricIds", Required: true, Description: "1 to 100 different metric IDs of the report in order, from " +
			"matomo.custom-report-options.list"},
		{Name: "categoryId", Description: "ID of the category the report is listed under" + given},
		{Name: "dimensionIds", Description: "1 to 100 different dimension IDs of the report in order, from " +
			"matomo.custom-report-options.list" + given},
		{Name: "subcategoryId", Description: "ID of the subcategory the report is listed under; Matomo creates a new " +
			"reporting page when omitted"},
		{Name: "description", Description: "Description of the report, up to 2048 characters" + given},
		{Name: "segmentFilter", Description: "Segment the report is limited to, as terms of field, operator, and " +
			"URL-encoded value joined by ; or , (fields: matomo.segments.fields), up to 2048 characters" + given},
		{Name: "multipleIdSites", Description: "1 to 20 different website IDs the report spans, each inside the " +
			"connection's targets" + given}}
}

var customReportsAdd = capability.Descriptor{
	ID: Provider + ".custom-reports.add", Version: 1, Title: "Create a Matomo custom report",
	Description: "Create a custom report on a website from a name, report type, and metrics, and optional settings. " +
		customDefinitionHint + ". Every website in multipleIdSites must lie inside the connection's targets, " +
		"otherwise the call is refused before anything is sent. A repeated call creates a second report. Needs the " +
		"premium plugin CustomReports and, expected, a write right on the website" + customReportSentOnceList,
	Tags: []string{"matomo", "custom-reports", "create"}, Risk: customReportChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + customDefinitionSchema + `},` +
		`"required":["name","reportType","metricIds"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idCustomReport":{"type":"integer"},"created":{"type":"boolean"}},"required":["idSite","idCustomReport","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, customDefinitionArguments(false)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the report belongs to"},
		{Name: "idCustomReport", Description: "ID of the new report, used as idCustomReport by the other custom report tools"},
		{Name: "created", Description: "True once Matomo created the report"}},
	Examples: []capability.Example{{Description: "Visits by page title on website 1",
		Arguments: json.RawMessage(`{"idSite":1,"name":"Pages","reportType":"table","metricIds":["nb_visits"],` +
			`"dimensionIds":["Actions.PageTitle"]}`)}},
}

var customReportsUpdate = capability.Descriptor{
	ID: Provider + ".custom-reports.update", Version: 1, Title: "Replace the configuration of a Matomo custom report",
	Description: "Replace the configuration of a custom report of a website as a whole: name, reportType, and " +
		"metricIds are required, and " + customDefinitionHint + ", so a setting left out may reset a value the " +
		"report had; read the report with matomo.custom-reports.get and pass every setting to keep, also the child " +
		"reports in subCategoryReportIds. Every website in multipleIdSites must lie inside the connection's " +
		"targets, otherwise the call is refused before anything is sent. The report is read first and must belong " +
		"to the website, otherwise it is not found. Needs the premium plugin CustomReports and, expected, a write " +
		"right on the website; idempotent in effect" + customReportSentOnceGet,
	Tags: []string{"matomo", "custom-reports", "update"}, Risk: customReportChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idCustomReportSchema + `,` +
		customDefinitionSchema + `,"subCategoryReportIds":{"type":"array","minItems":1,"maxItems":100,"uniqueItems":true,` +
		`"items":{"type":"integer","minimum":1,"maximum":2147483647}}},` +
		`"required":["idCustomReport","name","reportType","metricIds"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idCustomReport":{"type":"integer"},"saved":{"type":"boolean"}},"required":["idSite","idCustomReport","saved"],` +
		`"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{idSiteArgument(),
		{Name: "idCustomReport", Required: true, Description: "ID of the report to replace, from matomo.custom-reports.list"}},
		customDefinitionArguments(true)...),
		capability.Argument{Name: "subCategoryReportIds", Description: "1 to 100 different IDs of the child reports mapped " +
			"to this report; reset to Matomo's default when omitted"}),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the report belongs to"},
		{Name: "idCustomReport", Description: "The report that was replaced"},
		{Name: "saved", Description: "True once Matomo accepted the configuration"}},
	Examples: []capability.Example{{Description: "Rename report 3 of website 1 and keep its metric",
		Arguments: json.RawMessage(`{"idSite":1,"idCustomReport":3,"name":"Pages","reportType":"table",` +
			`"metricIds":["nb_visits"],"dimensionIds":["Actions.PageTitle"]}`)}},
}

var customReportsDuplicate = capability.Descriptor{
	ID: Provider + ".custom-reports.duplicate", Version: 1, Title: "Copy a Matomo custom report to websites",
	Description: "Copy a custom report of a website to one or more websites, each a new report. Every website in " +
		"idDestinationSites must lie inside the connection's targets, otherwise the whole call is refused before " +
		"anything is sent; the list holds 1 to 20 different websites and may name the source website, which is the " +
		"only destination when it is omitted. The report is read first and must belong to the source website, " +
		"otherwise it is not found. A repeated call copies it again. Needs the premium plugin CustomReports and, " +
		"expected, a write right on the websites" + customReportSentOnceList,
	Tags: []string{"matomo", "custom-reports", "create"}, Risk: customReportChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idCustomReportSchema + `,` +
		`"idDestinationSites":` + customSiteListSchema + `},"required":["idCustomReport"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idCustomReport":{"type":"integer"},"idDestinationSites":{"type":"array","items":{"type":"integer"}},` +
		`"duplicated":{"type":"boolean"},"answer":{}},"required":["idSite","idCustomReport","idDestinationSites",` +
		`"duplicated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idCustomReport", Required: true, Description: "ID of the report to copy, from matomo.custom-reports.list"},
		{Name: "idDestinationSites", Description: "1 to 20 different website IDs that receive a copy, each inside the " +
			"connection's targets; the source website is allowed and the only destination when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the report was copied from"},
		{Name: "idCustomReport", Description: "The report that was copied"},
		{Name: "idDestinationSites", Description: "The websites that were asked to receive a copy"},
		{Name: "duplicated", Description: "True once Matomo accepted the request"},
		{Name: "answer", Description: "What Matomo answered, such as the ID of the new report, as Matomo gives it, " +
			"untrusted, shortened; the form is not documented"}},
	Examples: []capability.Example{{Description: "Copy report 3 of website 1 to website 2",
		Arguments: json.RawMessage(`{"idSite":1,"idCustomReport":3,"idDestinationSites":[2]}`)}},
}

var customReportsPause = capability.Descriptor{
	ID: Provider + ".custom-reports.pause", Version: 1, Title: "Pause or resume a Matomo custom report",
	Description: "Pause a custom report of a website, or with resume true resume a paused one; exactly one of the two " +
		"is requested per call. The report is read first and must belong to the website, otherwise it is not found. " +
		"Needs the premium plugin CustomReports and, expected, a write right on the website; idempotent in " +
		"effect" + customReportSentOnceGet,
	Tags: []string{"matomo", "custom-reports", "update"}, Risk: customReportChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idCustomReportSchema + `,` +
		`"resume":{"type":"boolean"}},"required":["idCustomReport"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idCustomReport":{"type":"integer"},"paused":{"type":"boolean"}},"required":["idSite","idCustomReport","paused"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idCustomReport", Required: true, Description: "ID of the report, from matomo.custom-reports.list"},
		{Name: "resume", Description: "True to resume a paused report; false to pause it, as when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the report belongs to"},
		{Name: "idCustomReport", Description: "The report that was changed"},
		{Name: "paused", Description: "The state Matomo accepted: true after pausing, false after resuming"}},
	Examples: []capability.Example{{Description: "Pause report 3 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idCustomReport":3}`)}},
}

// CustomReportCreated is the answer of custom-reports.add.
type CustomReportCreated struct {
	IDSite         int  `json:"idSite"`
	IDCustomReport int  `json:"idCustomReport"`
	Created        bool `json:"created"`
}

// CustomReportSaved is the answer of custom-reports.update.
type CustomReportSaved struct {
	IDSite         int  `json:"idSite"`
	IDCustomReport int  `json:"idCustomReport"`
	Saved          bool `json:"saved"`
}

// CustomReportDuplicated is the answer of custom-reports.duplicate.
type CustomReportDuplicated struct {
	IDSite             int   `json:"idSite"`
	IDCustomReport     int   `json:"idCustomReport"`
	IDDestinationSites []int `json:"idDestinationSites"`
	Duplicated         bool  `json:"duplicated"`
	Answer             any   `json:"answer,omitempty"`
}

// CustomReportPaused is the answer of custom-reports.pause; Paused is the state after the call.
type CustomReportPaused struct {
	IDSite         int  `json:"idSite"`
	IDCustomReport int  `json:"idCustomReport"`
	Paused         bool `json:"paused"`
}

// customReportDefinition holds the settings of add and update. Pointers and nil lists tell a setting that is not
// given, which is not sent, from one that is.
type customReportDefinition struct {
	Name            string   `json:"name"`
	ReportType      string   `json:"reportType"`
	MetricIDs       []string `json:"metricIds"`
	CategoryID      *string  `json:"categoryId"`
	DimensionIDs    []string `json:"dimensionIds"`
	SubcategoryID   *string  `json:"subcategoryId"`
	Description     *string  `json:"description"`
	SegmentFilter   *string  `json:"segmentFilter"`
	MultipleIDSites []int    `json:"multipleIdSites"`
}

// customSites checks a list of websites against the targets as a whole: one website outside them refuses the
// call, and no message names a value.
func customSites(resolved *config.Resolved, name string, given []int, max int) error {
	if len(given) < 1 || len(given) > max {
		return invalidArgument(name + " must hold 1 to " + strconv.Itoa(max) + " website IDs")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	outside := false
	for i, id := range given {
		if id < 1 || len(strconv.Itoa(id)) > maxIDDigits {
			return invalidArgument(name + " must hold positive integer website IDs")
		}
		if slices.Index(given, id) != i {
			return invalidArgument(name + " must name each website once")
		}
		outside = outside || !bound.allows(id)
	}
	if outside {
		return invalidArgument("every website in " + name + " must be available to this connection")
	}
	return nil
}

func addIndexed(params url.Values, key string, values []string) {
	for i, value := range values {
		params.Set(key+"["+strconv.Itoa(i)+"]", value)
	}
}

func customIdentifier(name, value string) error {
	if !customReportIdentifierPattern.MatchString(value) {
		return invalidArgument(name + " must hold 1 to 128 letters, digits, underscores, dots, or hyphens")
	}
	return nil
}

func customIdentifiers(name string, values []string, required bool) error {
	if values == nil && !required {
		return nil
	}
	if len(values) < 1 || len(values) > maxCustomReportItems {
		return invalidArgument(name + " must hold 1 to 100 IDs")
	}
	for i, value := range values {
		if err := customIdentifier(name, value); err != nil {
			return err
		}
		if slices.Index(values, value) != i {
			return invalidArgument(name + " must name each ID once")
		}
	}
	return nil
}

// params checks the definition the way the descriptor promises and adds only what is given. Matomo stays the
// authority: it checks again, and its answer decides.
func (d customReportDefinition) params(resolved *config.Resolved, params url.Values) error {
	if strings.TrimSpace(d.Name) == "" || utf8.RuneCountInString(d.Name) > maxCustomReportName {
		return invalidArgument("name must hold 1 to 255 characters")
	}
	if err := customIdentifier("reportType", d.ReportType); err != nil {
		return err
	}
	if err := customIdentifiers("metricIds", d.MetricIDs, true); err != nil {
		return err
	}
	if err := customIdentifiers("dimensionIds", d.DimensionIDs, false); err != nil {
		return err
	}
	for _, id := range []struct {
		key   string
		value *string
	}{{"categoryId", d.CategoryID}, {"subcategoryId", d.SubcategoryID}} {
		if id.value == nil {
			continue
		}
		if err := customIdentifier(id.key, *id.value); err != nil {
			return err
		}
		params.Set(id.key, *id.value)
	}
	for _, text := range []struct {
		key   string
		value *string
	}{{"description", d.Description}, {"segmentFilter", d.SegmentFilter}} {
		if text.value == nil {
			continue
		}
		if utf8.RuneCountInString(*text.value) > maxUrlLength {
			return invalidArgument(text.key + " is longer than allowed")
		}
		params.Set(text.key, *text.value)
	}
	if d.SegmentFilter != nil {
		if err := checkSegmentExpression(*d.SegmentFilter); err != nil {
			return err
		}
	}
	if d.MultipleIDSites != nil {
		if err := customSites(resolved, "multipleIdSites", d.MultipleIDSites, maxCustomReportSites); err != nil {
			return err
		}
		for i, id := range d.MultipleIDSites {
			params.Set("multipleIdSites["+strconv.Itoa(i)+"]", strconv.Itoa(id))
		}
	}
	params.Set("name", d.Name)
	params.Set("reportType", d.ReportType)
	addIndexed(params, "metricIds", d.MetricIDs)
	addIndexed(params, "dimensionIds", d.DimensionIDs)
	return nil
}

func checkCustomReportID(id *int) error {
	if id == nil || *id < 1 || *id > maxCustomReportID {
		return invalidArgument("idCustomReport must be a positive integer report ID from matomo.custom-reports.list")
	}
	return nil
}

func invokeCustomReportsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add custom report"
	var args struct {
		IDSite *int `json:"idSite"`
		customReportDefinition
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if err := args.params(resolved, params); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, customReportsRequirement(customReportsAdd.ID), "CustomReports.addCustomReport",
		params, customReportSentOnceList, &answer); err != nil {
		return nil, err
	}
	var id int
	if scalar(answer, &id) != nil || id < 1 {
		return nil, unclearChange(op, "Matomo returned no ID of the custom report", customReportSentOnceList)
	}
	return &CustomReportCreated{IDSite: site, IDCustomReport: id, Created: true}, nil
}

// provenReport reads the report once so that it is proven to belong to the website, and to lie inside the
// targets, before a change follows.
func provenReport(ctx context.Context, client *Client, resolved *config.Resolved, op string, req requirement, site, id int) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	_, err = client.fetchCustomReport(ctx, op, req, bound, site, id)
	return err
}

func invokeCustomReportsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update custom report"
	var args struct {
		IDSite               *int  `json:"idSite"`
		IDCustomReport       *int  `json:"idCustomReport"`
		SubCategoryReportIDs []int `json:"subCategoryReportIds"`
		customReportDefinition
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
	if err := args.params(resolved, params); err != nil {
		return nil, err
	}
	if args.SubCategoryReportIDs != nil {
		if len(args.SubCategoryReportIDs) < 1 || len(args.SubCategoryReportIDs) > maxCustomReportItems {
			return nil, invalidArgument("subCategoryReportIds must hold 1 to 100 report IDs")
		}
		for i, id := range args.SubCategoryReportIDs {
			if id < 1 || id > maxCustomReportID || slices.Index(args.SubCategoryReportIDs, id) != i {
				return nil, invalidArgument("subCategoryReportIds must hold different positive integer report IDs")
			}
			params.Set("subCategoryReportIds["+strconv.Itoa(i)+"]", strconv.Itoa(id))
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := customReportsRequirement(customReportsUpdate.ID)
	if err := provenReport(ctx, client, resolved, op, req, site, *args.IDCustomReport); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "CustomReports.updateCustomReport", params, customReportSentOnceGet,
		&answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the custom report", customReportSentOnceGet)
	}
	return &CustomReportSaved{IDSite: site, IDCustomReport: *args.IDCustomReport, Saved: true}, nil
}

func invokeCustomReportsDuplicate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "duplicate custom report"
	var args struct {
		IDSite             *int  `json:"idSite"`
		IDCustomReport     *int  `json:"idCustomReport"`
		IDDestinationSites []int `json:"idDestinationSites"`
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
	destinations := args.IDDestinationSites
	if destinations == nil {
		destinations = []int{site}
	}
	if err := customSites(resolved, "idDestinationSites", destinations, maxCustomReportSites); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idCustomReport": {strconv.Itoa(*args.IDCustomReport)}}
	for i, id := range destinations {
		params.Set("idDestinationSites["+strconv.Itoa(i)+"]", strconv.Itoa(id))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := customReportsRequirement(customReportsDuplicate.ID)
	if err := provenReport(ctx, client, resolved, op, req, site, *args.IDCustomReport); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "CustomReports.duplicateCustomReport", params, customReportSentOnceList,
		&answer); err != nil {
		return nil, err
	}
	shown, ok := duplicateAnswer(answer)
	if !ok {
		// The reference promises the new report ID without showing the form: a bare ID counts too.
		var id int
		if scalar(answer, &id) != nil || id < 1 {
			return nil, unclearChange(op, "Matomo returned no confirmation of the copy", customReportSentOnceList)
		}
		shown = id
	}
	return &CustomReportDuplicated{IDSite: site, IDCustomReport: *args.IDCustomReport, IDDestinationSites: destinations,
		Duplicated: true, Answer: shown}, nil
}

func invokeCustomReportsPause(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		IDSite         *int  `json:"idSite"`
		IDCustomReport *int  `json:"idCustomReport"`
		Resume         *bool `json:"resume"`
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
	// Exactly one fixed method per call.
	op, method, paused := "pause custom report", "CustomReports.pauseCustomReport", true
	if args.Resume != nil && *args.Resume {
		op, method, paused = "resume custom report", "CustomReports.resumeCustomReport", false
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idCustomReport": {strconv.Itoa(*args.IDCustomReport)}}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := customReportsRequirement(customReportsPause.ID)
	if err := provenReport(ctx, client, resolved, op, req, site, *args.IDCustomReport); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, method, params, customReportSentOnceGet, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the custom report", customReportSentOnceGet)
	}
	return &CustomReportPaused{IDSite: site, IDCustomReport: *args.IDCustomReport, Paused: paused}, nil
}

func customReportChangeOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: customReportsAdd, Handler: capability.Handler(invokeCustomReportsAdd)},
		{Descriptor: customReportsUpdate, Handler: capability.Handler(invokeCustomReportsUpdate)},
		{Descriptor: customReportsDuplicate, Handler: capability.Handler(invokeCustomReportsDuplicate)},
		{Descriptor: customReportsPause, Handler: capability.Handler(invokeCustomReportsPause)},
		{Descriptor: customReportsDelete, Handler: capability.Handler(invokeCustomReportsDelete)},
	}
}
