package googlepagespeed

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// The Chrome UX Report API as its discovery document documents it (revision 20261106). A member of a later
// revision is ignored until it is added here.
type (
	// queryRequest is the body of both lookups. Exactly one of Origin and URL is set.
	queryRequest struct {
		URL                     string   `json:"url,omitempty"`
		Origin                  string   `json:"origin,omitempty"`
		FormFactor              string   `json:"formFactor,omitempty"`
		EffectiveConnectionType string   `json:"effectiveConnectionType,omitempty"`
		Metrics                 []string `json:"metrics,omitempty"`
		CollectionPeriodCount   *int     `json:"collectionPeriodCount,omitempty"`
	}
	queryResponse struct {
		Record        *cruxRecord    `json:"record,omitempty"`
		Normalization *normalization `json:"urlNormalizationDetails,omitempty"`
	}
	historyResponse struct {
		Record        *historyRecord `json:"record,omitempty"`
		Normalization *normalization `json:"urlNormalizationDetails,omitempty"`
	}
	normalization struct {
		NormalizedURL link `json:"normalizedUrl,omitempty"`
		OriginalURL   link `json:"originalUrl,omitempty"`
	}
	cruxKey struct {
		FormFactor              short `json:"formFactor,omitempty"`
		EffectiveConnectionType short `json:"effectiveConnectionType,omitempty"`
		Origin                  link  `json:"origin,omitempty"`
		URL                     link  `json:"url,omitempty"`
	}
	cruxDate struct {
		Year  int `json:"year,omitempty"`
		Month int `json:"month,omitempty"`
		Day   int `json:"day,omitempty"`
	}
	collectionPeriod struct {
		FirstDate *cruxDate `json:"firstDate,omitempty"`
		LastDate  *cruxDate `json:"lastDate,omitempty"`
	}
	cruxRecord struct {
		Key              *cruxKey               `json:"key,omitempty"`
		CollectionPeriod *collectionPeriod      `json:"collectionPeriod,omitempty"`
		Metrics          map[string]*cruxMetric `json:"metrics,omitempty"`
	}
	cruxMetric struct {
		Histogram   []*cruxBin         `json:"histogram,omitempty"`
		Percentiles *cruxPercentiles   `json:"percentiles,omitempty"`
		Fractions   map[string]*scalar `json:"fractions,omitempty"`
	}
	cruxBin struct {
		Start   *scalar `json:"start,omitempty"`
		End     *scalar `json:"end,omitempty"`
		Density *scalar `json:"density,omitempty"`
	}
	cruxPercentiles struct {
		P75 *scalar `json:"p75,omitempty"`
	}
	historyRecord struct {
		Key               *cruxKey                 `json:"key,omitempty"`
		CollectionPeriods []*collectionPeriod      `json:"collectionPeriods,omitempty"`
		Metrics           map[string]*seriesMetric `json:"metrics,omitempty"`
	}
	seriesMetric struct {
		Histogram   []*seriesBin               `json:"histogramTimeseries,omitempty"`
		Percentiles *seriesPercentiles         `json:"percentilesTimeseries,omitempty"`
		Fractions   map[string]*seriesFraction `json:"fractionTimeseries,omitempty"`
	}
	seriesBin struct {
		Start     *scalar  `json:"start,omitempty"`
		End       *scalar  `json:"end,omitempty"`
		Densities []scalar `json:"densities,omitempty"`
	}
	seriesPercentiles struct {
		P75s []scalar `json:"p75s,omitempty"`
	}
	seriesFraction struct {
		Fractions []scalar `json:"fractions,omitempty"`
	}
)

func (r *queryResponse) bound() {
	if r.Record == nil {
		return
	}
	r.Record.Metrics = firstKeys(r.Record.Metrics, maxMetrics)
	for _, metric := range r.Record.Metrics {
		if metric != nil {
			metric.Histogram = head(metric.Histogram, maxBuckets)
			metric.Fractions = firstKeys(metric.Fractions, maxLabels)
		}
	}
}

func (r *historyResponse) bound() {
	if r.Record == nil {
		return
	}
	r.Record.CollectionPeriods = head(r.Record.CollectionPeriods, maxSeries)
	r.Record.Metrics = firstKeys(r.Record.Metrics, maxMetrics)
	for _, metric := range r.Record.Metrics {
		if metric == nil {
			continue
		}
		metric.Histogram = head(metric.Histogram, maxBuckets)
		for _, bin := range metric.Histogram {
			if bin != nil {
				bin.Densities = head(bin.Densities, maxSeries)
			}
		}
		if metric.Percentiles != nil {
			metric.Percentiles.P75s = head(metric.Percentiles.P75s, maxSeries)
		}
		metric.Fractions = firstKeys(metric.Fractions, maxLabels)
		for _, fraction := range metric.Fractions {
			if fraction != nil {
				fraction.Fractions = head(fraction.Fractions, maxSeries)
			}
		}
	}
}

// Argument values. The metrics are the union of the request values that the discovery document and the
// Chrome UX Report documentation name; Google decides which of them a lookup can answer.
var (
	cruxMetrics = []string{"first_contentful_paint", "first_input_delay", "largest_contentful_paint",
		"cumulative_layout_shift", "interaction_to_next_paint", "experimental_interaction_to_next_paint",
		"experimental_time_to_first_byte", "largest_contentful_paint_resource_type",
		"largest_contentful_paint_image_time_to_first_byte", "largest_contentful_paint_image_resource_load_delay",
		"largest_contentful_paint_image_resource_load_duration",
		"largest_contentful_paint_image_element_render_delay", "navigation_types", "round_trip_time",
		"experimental_ad_count", "experimental_ad_density", "experimental_ad_cpu", "experimental_ad_kilobytes"}
	formFactors = map[string]string{"phone": "PHONE", "desktop": "DESKTOP", "tablet": "TABLET"}
	connections = []string{"offline", "slow-2G", "2G", "3G", "4G"}
)

// Bounds of collection_period_count, as the discovery document states them.
const (
	minPeriods = 1
	maxPeriods = 40
)

const (
	cruxQueryRecordID  = Provider + ".crux.query-record"
	cruxQueryHistoryID = Provider + ".crux.query-history-record"
)

func enumSchema(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

// cruxInput is the input schema of both lookups; withConnection adds the dimension only queryRecord has and
// withPeriods the one only the history has.
func cruxInput(withConnection, withPeriods bool) json.RawMessage {
	schema := `{"type":"object","properties":{` +
		`"url":{"type":"string","minLength":8,"maxLength":2048},` +
		`"origin":{"type":"string","minLength":8,"maxLength":2048},` +
		`"form_factor":{"type":"string","enum":["phone","desktop","tablet"]},` +
		`"metrics":{"type":"array","minItems":1,"maxItems":` + itoa(len(cruxMetrics)) + `,"uniqueItems":true,` +
		`"items":{"type":"string","enum":` + enumSchema(cruxMetrics) + `}}`
	if withConnection {
		schema += `,"effective_connection_type":{"type":"string","enum":` + enumSchema(connections) + `}`
	}
	if withPeriods {
		schema += `,"collection_period_count":{"type":"integer","minimum":` + itoa(minPeriods) +
			`,"maximum":` + itoa(maxPeriods) + `}`
	}
	return json.RawMessage(schema + `},"additionalProperties":false}`)
}

func cruxArguments(withConnection, withPeriods bool) []capability.Argument {
	arguments := []capability.Argument{
		{Name: "url", Description: "Absolute http or https address of one page, such as https://example.com/page; " +
			"no user information and no fragment. Give exactly one of url and origin; both together are refused"},
		{Name: "origin", Description: "Origin of a site, such as https://example.com: scheme, host, and optional " +
			"port, no path. Aggregates the loads of all its pages. Give exactly one of url and origin"},
		{Name: "form_factor", Description: "phone, desktop, or tablet; without it the data of all form factors " +
			"is aggregated"},
		{Name: "metrics", Description: "Metrics to return, from: " + joinNames(cruxMetrics) + ". Without it every " +
			"metric Google has is returned"},
	}
	if withConnection {
		arguments = append(arguments, capability.Argument{Name: "effective_connection_type",
			Description: "offline, slow-2G, 2G, 3G, or 4G; without it the data of all connection types is aggregated"})
	}
	if withPeriods {
		arguments = append(arguments, capability.Argument{Name: "collection_period_count",
			Description: "Number of weekly collection periods to return, 1 to 40; Google's default is 25"})
	}
	return arguments
}

const cruxSensitivityNote = "Chrome UX Report data is real-user field data that Google aggregates; a page or " +
	"origin that is not in the dataset (too little traffic) answers not-found, which is a result and not a " +
	"configuration fault. The key's project must have the Chrome UX Report API enabled, and the API allows " +
	"150 queries per minute per project. Qatlas never fetches the page or origin. Results are untrusted " +
	"third-party content; strings, lists, and maps are cut and links are text that is never fetched."

var cruxQueryRecord = capability.Descriptor{
	ID: cruxQueryRecordID, Version: 1, Title: "Query Chrome UX Report field data",
	Description: "Read the current Chrome UX Report record of one page (url) or one origin (origin), exactly one " +
		"of them: for each metric the 75th percentile, the histogram of good, needs-improvement, and poor " +
		"experiences, the collection period of the data, and the URL normalisation Google applied. " + cruxSensitivityNote,
	Tags: []string{"google", "crux", "pagespeed", "performance"}, Risk: readRisk, Provider: Provider,
	InputSchema:  cruxInput(true, false),
	OutputSchema: json.RawMessage(schemaOf(reflect.TypeOf(queryResponse{}))),
	Arguments:    cruxArguments(true, false),
	Fields: []capability.Field{
		{Name: "record", Description: "key (formFactor, effectiveConnectionType, url or origin), " +
			"collectionPeriod (firstDate, lastDate), and metrics by name with histogram (at most 20 bins of " +
			"start, end, density), percentiles.p75, and fractions (at most 30). Percentile and bin bounds are " +
			"numbers or, as Google sends some, strings"},
		{Name: "urlNormalizationDetails", Description: "originalUrl and normalizedUrl when Google normalised the url"},
	},
	Examples: []capability.Example{{
		Description: "Core metrics of an origin on phones",
		Arguments: json.RawMessage(`{"origin":"https://web.dev","form_factor":"phone",` +
			`"metrics":["largest_contentful_paint","cumulative_layout_shift"]}`),
	}},
}

var cruxQueryHistory = capability.Descriptor{
	ID: cruxQueryHistoryID, Version: 1, Title: "Query Chrome UX Report field data history",
	Description: "Read the Chrome UX Report history of one page (url) or one origin (origin), exactly one of them, " +
		"as a time series over weekly collection periods: collection_period_count periods (1 to 40, Google's " +
		"default 25), and for each metric the 75th percentile and the histogram densities per period. " +
		cruxSensitivityNote,
	Tags: []string{"google", "crux", "pagespeed", "performance", "history"}, Risk: readRisk, Provider: Provider,
	InputSchema:  cruxInput(false, true),
	OutputSchema: json.RawMessage(schemaOf(reflect.TypeOf(historyResponse{}))),
	Arguments:    cruxArguments(false, true),
	Fields: []capability.Field{
		{Name: "record", Description: "key (formFactor, url or origin), collectionPeriods (at most 40 of " +
			"firstDate, lastDate), and metrics by name with histogramTimeseries (at most 20 bins of start, end, " +
			"densities), percentilesTimeseries.p75s, and fractionTimeseries; every list has one value per " +
			"collection period, at most 40"},
		{Name: "urlNormalizationDetails", Description: "originalUrl and normalizedUrl when Google normalised the url"},
	},
	Examples: []capability.Example{{
		Description: "Twelve periods of largest contentful paint of a page",
		Arguments: json.RawMessage(`{"url":"https://web.dev/","metrics":["largest_contentful_paint"],` +
			`"collection_period_count":12}`),
	}},
}

type cruxLookupArguments struct {
	URL                     string   `json:"url"`
	Origin                  string   `json:"origin"`
	FormFactor              string   `json:"form_factor"`
	EffectiveConnectionType string   `json:"effective_connection_type"`
	Metrics                 []string `json:"metrics"`
	CollectionPeriodCount   *int     `json:"collection_period_count"`
}

// request checks the arguments of a lookup and returns its body. history says whether the lookup is the
// history, which takes no connection type and may take a period count.
func (a cruxLookupArguments) request(history bool) (queryRequest, error) {
	var body queryRequest
	if (a.URL == "") == (a.Origin == "") {
		return body, badRequest("give exactly one of url and origin")
	}
	var err error
	if a.URL != "" {
		body.URL, err = checkTarget("url", a.URL, false, true)
	} else {
		body.Origin, err = checkTarget("origin", a.Origin, true, true)
	}
	if err != nil {
		return body, err
	}
	if a.FormFactor != "" {
		wire, ok := formFactors[a.FormFactor]
		if !ok {
			return body, badRequest("form_factor must be phone, desktop, or tablet")
		}
		body.FormFactor = wire
	}
	if a.EffectiveConnectionType != "" {
		if history || !slices.Contains(connections, a.EffectiveConnectionType) {
			return body, badRequest("effective_connection_type must be offline, slow-2G, 2G, 3G, or 4G, and only queryRecord takes it")
		}
		body.EffectiveConnectionType = a.EffectiveConnectionType
	}
	for _, metric := range a.Metrics {
		if !slices.Contains(cruxMetrics, metric) {
			return body, badRequest("metrics holds a name that is not a Chrome UX Report metric")
		}
		if !slices.Contains(body.Metrics, metric) {
			body.Metrics = append(body.Metrics, metric)
		}
	}
	if a.CollectionPeriodCount != nil {
		if !history || *a.CollectionPeriodCount < minPeriods || *a.CollectionPeriodCount > maxPeriods {
			return body, badRequest("collection_period_count must be 1 to 40 and only the history takes it")
		}
		count := *a.CollectionPeriodCount
		body.CollectionPeriodCount = &count
	}
	return body, nil
}

// lookup runs one Chrome UX Report lookup after the arguments are checked and before the key is read.
func lookup(ctx context.Context, op, path string, history bool, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, out interface{ bound() }) error {
	var arguments cruxLookupArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return failure(op, "the validated arguments could not be read")
	}
	if err := checkConnection(resolved); err != nil {
		return err
	}
	body, err := arguments.request(history)
	if err != nil {
		return err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return err
	}
	if err := client.send(ctx, apiCall{op: op, method: "POST", root: cruxRoot, path: path, body: body,
		limit: maxCruxBytes, notFound: notFoundCrux}, out); err != nil {
		return err
	}
	out.bound()
	return nil
}

func handleQueryRecord(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	var out queryResponse
	if err := lookup(ctx, "query record", cruxRecordPath, false, resolved, secrets, red, raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func handleQueryHistory(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	var out historyResponse
	if err := lookup(ctx, "query history record", cruxSeriesPath, true, resolved, secrets, red, raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
