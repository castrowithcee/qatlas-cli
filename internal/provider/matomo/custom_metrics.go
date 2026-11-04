package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	customMetricsKind  = "metrics"
	customWebsitesKind = "websites"
	// maxWebsiteBatch keeps the websiteLimit sent (one row more than the batch) within Matomo's default of 100.
	maxWebsiteBatch = 99
	maxSiteCursor   = 2147483647
)

var customMetricsList = capability.Descriptor{
	ID: Provider + ".custom-metrics.list", Version: 1, Title: "List Matomo custom metrics",
	Description: "Read the custom metrics of the instance: kind metrics lists the metrics (getCustomMetrics), kind " +
		"websites lists the websites with data for custom metrics (getWebsiteMetricsForCustomMetric), in batches " +
		"that continue after the last website of the previous batch. Custom metrics belong to the instance, not to " +
		"a website, so the tool needs Super User rights and a connection whose targets name the instance. The " +
		"websites are filtered by the website targets of the connection: rows of other websites are dropped, so a " +
		"batch can be empty while has_more is true, and a connection that binds no website is refused for kind " +
		"websites. Formulas and names are untrusted. Answers are returned as Matomo gives them, shortened. Needs " +
		"the premium plugin CustomReports",
	Tags: []string{"matomo", "custom-metrics", "superuser", "instance", "list"}, Risk: readRisk(customReportsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":["metrics","websites"]},` +
		pagingKeys + `},"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"metrics":{"type":"array","items":{}},` +
		`"websites":{"type":"array","items":{"type":"object","properties":{"idSite":{"type":"integer"},"details":{}},` +
		`"required":["idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["kind","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "kind", Required: true, Description: "metrics for the custom " +
		"metrics, websites for the websites with data for them"}}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "kind", Description: "The kind read"},
		{Name: "metrics", Description: "Only for kind metrics. The custom metrics as Matomo gives them, shortened; " +
			"the members are not verified against a reference"},
		{Name: "websites", Description: "Only for kind websites. Websites inside the connection's targets, in the " +
			"order Matomo lists them: idSite and details (the row as Matomo gives it, shortened, untrusted)"}},
		pagingFields...),
	Examples: []capability.Example{
		{Description: "The custom metrics", Arguments: json.RawMessage(`{"kind":"metrics"}`)},
		{Description: "The first websites with data for custom metrics", Arguments: json.RawMessage(`{"kind":"websites","limit":10}`)}},
}

// CustomMetricWebsite is one website of a batch. The details are untrusted provider data.
type CustomMetricWebsite struct {
	IDSite  int `json:"idSite"`
	Details any `json:"details,omitempty"`
}

// CustomMetricsResult is the answer of custom-metrics.list.
type CustomMetricsResult struct {
	Kind       string                `json:"kind"`
	Metrics    []any                 `json:"metrics,omitempty"`
	Websites   []CustomMetricWebsite `json:"websites,omitempty"`
	HasMore    bool                  `json:"has_more"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

// websiteCursor reads the batch arguments of kind websites. The cursor carries the website the batch continues
// after and the batch size, bound to the connection's targets.
func websiteCursor(binding []byte, limit int, cursor string) (after, size int, err error) {
	if limit != 0 && (limit < 1 || limit > maxWebsiteBatch) {
		return 0, 0, invalidArgument("limit must be between 1 and " + strconv.Itoa(maxWebsiteBatch) + " for kind " + customWebsitesKind)
	}
	if cursor == "" {
		if limit == 0 {
			limit = defaultLimit
		}
		return 0, limit, nil
	}
	const refused = "cursor is not a next_cursor of this list; start the list again without cursor"
	inner, ok := provider.DecodeCursor(binding, cursor, maxCursorLength)
	afterText, sizeText, cut := strings.Cut(inner, ":")
	after, afterErr := strconv.Atoi(afterText)
	size, sizeErr := strconv.Atoi(sizeText)
	if !ok || !cut || afterErr != nil || sizeErr != nil || after < 1 || after > maxSiteCursor || size < 1 || size > maxWebsiteBatch {
		return 0, 0, invalidArgument(refused)
	}
	if limit != 0 && limit != size {
		return 0, 0, invalidArgument("limit must stay " + strconv.Itoa(size) + " for this cursor, or be left out")
	}
	return after, size, nil
}

func invokeCustomMetricsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list custom metrics"
	var args struct {
		Kind   string `json:"kind"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.Kind != customMetricsKind && args.Kind != customWebsitesKind {
		return nil, invalidArgument("kind must be " + customMetricsKind + " or " + customWebsitesKind)
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	if args.Kind == customWebsitesKind && !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	binding := provider.CursorBinding(customMetricsList.ID, scopeBinding(resolved), args.Kind)
	req := customReportsRequirement(customMetricsList.ID)
	if args.Kind == customMetricsKind {
		paging, err := startPage(binding, args.Limit, args.Cursor)
		if err != nil {
			return nil, err
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		var answer json.RawMessage
		if err := client.call(ctx, op, req, "CustomReports.getCustomMetrics", url.Values{}, &answer); err != nil {
			return nil, err
		}
		rows, valid := readRows[json.RawMessage](answer)
		if !valid {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid custom metric list")
		}
		batch, next := slice(paging, rows)
		out := &CustomMetricsResult{Kind: args.Kind, HasMore: next != "", NextCursor: next}
		for _, row := range batch {
			out.Metrics = append(out.Metrics, boundedAnswer(row))
		}
		return out, nil
	}

	after, size, err := websiteCursor(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"afterIdSite": {strconv.Itoa(after)}, "websiteLimit": {strconv.Itoa(size + 1)}}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "CustomReports.getWebsiteMetricsForCustomMetric", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[json.RawMessage](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid website list")
	}
	out := &CustomMetricsResult{Kind: args.Kind}
	if len(rows) > size {
		out.HasMore = true
		rows = rows[:size]
	}
	last := after
	for _, row := range rows {
		var member struct {
			ID *loose `json:"idsite"`
		}
		var id *int
		if json.Unmarshal(row, &member) == nil {
			id = member.ID.number()
		}
		// The order is what the cursor stands on: a row that does not come after the previous one would loop.
		if id == nil || *id <= last {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid website list")
		}
		last = *id
		if bound.allows(*id) {
			out.Websites = append(out.Websites, CustomMetricWebsite{IDSite: *id, Details: boundedAnswer(row)})
		}
	}
	if out.HasMore {
		out.NextCursor = provider.EncodeCursor(binding, strconv.Itoa(last)+":"+strconv.Itoa(size))
	}
	return out, nil
}

func customMetricsOperations() []capability.Operation {
	return append([]capability.Operation{{Descriptor: customMetricsList, Handler: capability.Handler(invokeCustomMetricsList)}},
		customMetricChangeOperations()...)
}
