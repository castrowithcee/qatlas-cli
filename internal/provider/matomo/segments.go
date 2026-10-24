package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	segmentsSensitive  = "matomo-segment-metadata"
	suggestedSensitive = "matomo-visitor-data"
	maxSuggestedValues = 100
	segmentPattern     = `^[A-Za-z0-9_.]{1,100}$`
)

// Operators of the Matomo segment syntax per field type: a dimension compares text, a metric compares numbers.
var (
	dimensionOperators = []string{"==", "!=", "=@", "!@", "=^", "=$"}
	metricOperators    = []string{"==", "!=", "<", ">", "<=", ">="}
)

// SegmentField is a segment field of a website. Names are the identifiers of a segment expression.
type SegmentField struct {
	Segment        string   `json:"segment"`
	Name           string   `json:"name"`
	Type           string   `json:"type,omitempty"`
	Category       string   `json:"category,omitempty"`
	Operators      []string `json:"operators,omitempty"`
	AcceptedValues string   `json:"accepted_values,omitempty"`
}

type segmentJSON struct {
	Segment        *loose `json:"segment"`
	Name           *loose `json:"name"`
	Type           *loose `json:"type"`
	Category       *loose `json:"category"`
	AcceptedValues *loose `json:"acceptedValues"`
}

func (s segmentJSON) field() (SegmentField, bool) {
	field := SegmentField{
		Segment: s.Segment.str(maxStringLength), Name: s.Name.str(maxStringLength), Type: s.Type.str(maxStringLength),
		Category: s.Category.str(maxStringLength), AcceptedValues: s.AcceptedValues.str(maxReportText),
	}
	switch field.Type {
	case "dimension":
		field.Operators = dimensionOperators
	case "metric":
		field.Operators = metricOperators
	}
	return field, field.Segment != ""
}

var segmentsFields = capability.Descriptor{
	ID: Provider + ".segments.fields", Version: 1, Title: "List Matomo segment fields",
	Description: "List the fields a segment expression of a website can use, with type, operators, and category, " +
		"in batches; the first batch without category also names the categories; names are untrusted",
	Tags: []string{"matomo", "segments", "fields", "list"}, Risk: readRisk(segmentsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + languageSchema + `,` +
		`"category":{"type":"string","minLength":1,"maxLength":100},` + pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"fields":{"type":"array",` +
		`"items":{"type":"object","properties":{"segment":{"type":"string"},"name":{"type":"string"},` +
		`"type":{"type":"string"},"category":{"type":"string"},"operators":{"type":"array","items":{"type":"string"}},` +
		`"accepted_values":{"type":"string"}},"required":["segment","name"],"additionalProperties":false}},` +
		`"categories":{"type":"array","items":{"type":"object","properties":{"category":{"type":"string"},` +
		`"count":{"type":"integer"}},"required":["category","count"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","fields","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(), languageArgument(),
		{Name: "category", Description: "Only fields of this category, matched without regard to case"}},
		pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website the fields belong to"},
		{Name: "fields", Description: "Segment fields in the order Matomo lists them"},
		{Name: "fields[].segment", Description: "Identifier used in a segment expression, such as pageUrl"},
		{Name: "fields[].name", Description: "Display name, untrusted"},
		{Name: "fields[].type", Description: "dimension or metric"},
		{Name: "fields[].category", Description: "Field category, untrusted"},
		{Name: "fields[].operators", Description: "Operators of the segment syntax that fit the type"},
		{Name: "fields[].accepted_values", Description: "Hint Matomo gives about the values, untrusted"},
		{Name: "categories", Description: "Categories with their field counts, only in the first batch of a list " +
			"without category"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "Fields of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var segmentsSuggest = capability.Descriptor{
	ID: Provider + ".segments.suggest-values", Version: 1, Title: "Suggest values of a Matomo segment field",
	Description: "Read frequent values of a segment field of a website, such as common page URLs or browsers; the " +
		"values are visitor data and untrusted",
	Tags: []string{"matomo", "segments", "values", "suggest"}, Risk: readRisk(suggestedSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"segment":{"type":"string","pattern":"` + segmentPattern + `","maxLength":100}},` +
		`"required":["segment"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"segment":{"type":"string"},` +
		`"values":{"type":"array","items":{"type":"string"}}},"required":["idSite","segment","values"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "segment", Required: true, Description: "Segment field identifier from matomo.segments.fields, such as pageUrl"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the values belong to"},
		{Name: "segment", Description: "The field asked for"},
		{Name: "values", Description: "Frequent values, at most 100, untrusted visitor data; empty when Matomo has none"},
	},
	Examples: []capability.Example{{Description: "Frequent browsers", Arguments: json.RawMessage(`{"idSite":1,"segment":"browserName"}`)}},
}

// SegmentFieldsResult is the answer of segments.fields.
type SegmentFieldsResult struct {
	IDSite     int             `json:"idSite"`
	Fields     []SegmentField  `json:"fields"`
	Categories []CategoryCount `json:"categories,omitempty"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// SuggestedValuesResult is the answer of segments.suggest-values.
type SuggestedValuesResult struct {
	IDSite  int      `json:"idSite"`
	Segment string   `json:"segment"`
	Values  []string `json:"values"`
}

func invokeSegmentsFields(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list segment fields"
	var args struct {
		IDSite   *int   `json:"idSite"`
		Language string `json:"language"`
		Category string `json:"category"`
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
	if !validLanguage(args.Language) {
		return nil, invalidArgument("language must be a language code such as en or de")
	}
	category := strings.TrimSpace(args.Category)
	if args.Category != "" && category == "" || len(category) > maxQueryLength {
		return nil, invalidArgument("category must hold 1 to 100 characters")
	}
	binding := provider.CursorBinding(segmentsFields.ID, scopeBinding(resolved), id, args.Language, category)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSites": {strconv.Itoa(id)}}
	if args.Language != "" {
		params.Set("language", args.Language)
	}
	var rows []segmentJSON
	if err := client.call(ctx, op, baseRequirement(segmentsFields.ID), "API.getSegmentsMetadata", params, &rows); err != nil {
		return nil, err
	}
	var matched []SegmentField
	var categories []CategoryCount
	seen := map[string]bool{}
	for _, row := range rows {
		field, ok := row.field()
		if !ok || seen[field.Segment] {
			continue
		}
		seen[field.Segment] = true
		if category == "" {
			if i := slices.IndexFunc(categories, func(c CategoryCount) bool { return c.Category == field.Category }); i >= 0 {
				categories[i].Count++
			} else {
				categories = append(categories, CategoryCount{Category: field.Category, Count: 1})
			}
		} else if !strings.EqualFold(field.Category, category) {
			continue
		}
		matched = append(matched, field)
	}
	batch, next := slice(paging, matched)
	if batch == nil {
		batch = []SegmentField{}
	}
	result := &SegmentFieldsResult{IDSite: id, Fields: batch, HasMore: next != "", NextCursor: next}
	if args.Cursor == "" {
		result.Categories = categories
	}
	return result, nil
}

func invokeSegmentsSuggest(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "suggest segment values"
	var args struct {
		IDSite  *int   `json:"idSite"`
		Segment string `json:"segment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if !validSegmentName(args.Segment) {
		return nil, invalidArgument("segment must be a field identifier from matomo.segments.fields")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"segmentName": {args.Segment}, "idSite": {strconv.Itoa(id)}}
	var rows []json.RawMessage
	if err := client.call(ctx, op, baseRequirement(segmentsSuggest.ID), "API.getSuggestedValuesForSegment", params, &rows); err != nil {
		return nil, err
	}
	values := []string{}
	for _, row := range rows {
		var value loose
		if len(values) >= maxSuggestedValues || value.UnmarshalJSON(row) != nil {
			continue
		}
		if text := value.str(maxStringLength); text != "" {
			values = append(values, text)
		}
	}
	return &SuggestedValuesResult{IDSite: id, Segment: args.Segment, Values: values}, nil
}

func validSegmentName(name string) bool {
	if name == "" || len(name) > 100 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
