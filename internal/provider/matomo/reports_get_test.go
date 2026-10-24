package matomo

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The fixtures follow the answers of API.getProcessedReport: columns as an ordered map, reportData as a list
// of rows (or one object for a report without dimension, or an object per period), and reportMetadata aligned
// with the rows.
const processedMetadata = `{"category":"Behaviour","name":"Page URLs","module":"Actions","action":"getPageUrls",` +
	`"dimension":"Page URL","metrics":{"nb_hits":"Pageviews"},"parameters":[]}`

func pageRows(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = `{"label":"/page-` + strconv.Itoa(i) + `","nb_hits":` + strconv.Itoa(1000-i) + `,"bounce_rate":0.25}`
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func pageMeta(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = `{"url":"https://shop.example.test/page-` + strconv.Itoa(i) + `","idsubdatatable":` + strconv.Itoa(i+7) + `}`
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func processed(columns, data, meta, totals string) string {
	return `{"website":"shop","prettyDate":"October 2026","metadata":` + processedMetadata + `,"columns":` + columns +
		`,"reportData":` + data + `,"reportMetadata":` + meta + `,"reportTotal":` + totals + `}`
}

const pageColumns = `{"label":"Page URL","nb_hits":"Pageviews","bounce_rate":"Bounce Rate"}`

// reportServer answers the catalog and every processed report with the given function.
func reportServer(answer func(call) string, seen *[]call) answers {
	return with(catalogAnswers(nil), answers{"API.getProcessedReport": func(c call) *http.Response {
		if seen != nil {
			*seen = append(*seen, c)
		}
		body := answer(c)
		if strings.HasPrefix(body, "error:") {
			return failure(strings.TrimPrefix(body, "error:"))
		}
		return ok(body)
	}})
}

// windowOf serves rows of a table of total rows the way Matomo's Limit filter does.
func windowOf(c call, total int) (offset, count int) {
	offset, _ = strconv.Atoi(c.form.Get("filter_offset"))
	limit, _ := strconv.Atoi(c.form.Get("filter_limit"))
	count = max(0, min(limit, total-offset))
	return offset, count
}

func pagedPages(total int) func(call) string {
	return func(c call) string {
		offset, count := windowOf(c, total)
		rows := make([]string, count)
		meta := make([]string, count)
		for i := range rows {
			n := offset + i
			rows[i] = `{"label":"/page-` + strconv.Itoa(n) + `","nb_hits":` + strconv.Itoa(1000-n) + `}`
			meta[i] = `{"idsubdatatable":` + strconv.Itoa(n+7) + `}`
		}
		return processed(pageColumns, "["+strings.Join(rows, ",")+"]", "["+strings.Join(meta, ",")+"]", `{"nb_hits":4}`)
	}
}

type reportOut struct {
	IDSite      int              `json:"idSite"`
	Report      ReportInfo       `json:"report"`
	Period      string           `json:"period"`
	Date        string           `json:"date"`
	PrettyDate  string           `json:"pretty_date"`
	Columns     []ReportColumn   `json:"columns"`
	Rows        []map[string]any `json:"rows"`
	Totals      map[string]any   `json:"totals"`
	Metadata    map[string]any   `json:"metadata"`
	RowMetadata []map[string]any `json:"row_metadata"`
	HasMore     bool             `json:"has_more"`
	NextCursor  string           `json:"next_cursor"`
}

func getReport(t *testing.T, env *environment, arguments string) reportOut {
	t.Helper()
	result, err := env.invoke("matomo.reports.get", "open", arguments)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var out reportOut
	if json.Unmarshal([]byte(result), &out) != nil {
		t.Fatalf("result = %s", result)
	}
	return out
}

const pagesArgs = `"idSite":1,"report":"Actions_getPageUrls","period":"month","date":"2026-09-01"`

func lastReport(calls []call) call {
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].api() == "API.getProcessedReport" {
			return calls[i]
		}
	}
	return call{}
}

func TestReportsGetListReportSendsCatalogParametersAndBoundaries(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(500), &seen))
	got := getReport(t, env, `{`+pagesArgs+`}`)
	sent := seen[0].form
	for name, want := range map[string]string{"apiModule": "Actions", "apiAction": "getPageUrls", "idSite": "1",
		"period": "month", "date": "2026-09-01", "hideMetricsDoc": "1", "showTimer": "0", "format": "JSON",
		"filter_limit": "101", "filter_offset": "0", "format_metrics": "0"} {
		if sent.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.Get(name), want)
		}
	}
	if len(got.Rows) != 100 || !got.HasMore || got.NextCursor == "" || got.Rows[0]["label"] != "/page-0" ||
		got.IDSite != 1 || got.Report.ID != "Actions_getPageUrls" || got.Report.Name != "Page URLs" ||
		got.Period != "month" || got.Date != "2026-09-01" || got.PrettyDate != "October 2026" ||
		len(got.Columns) != 3 || got.Columns[0] != (ReportColumn{Name: "label", Label: "Page URL"}) ||
		got.Totals["nb_hits"] != float64(4) {
		t.Fatalf("got = %+v", got)
	}
	if got.Metadata != nil || got.RowMetadata != nil {
		t.Errorf("metadata without include_metadata: %v %v", got.Metadata, got.RowMetadata)
	}
	for _, name := range []string{"disable_generic_filters", "disable_queued_filters", "disable_root_datatable_post_processor",
		"callback", "jsoncallback", "token_auth", "showRawMetrics"} {
		if _, found := sent[name]; found {
			t.Errorf("%s was sent", name)
		}
	}
	raw, _ := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`}`)
	if strings.Contains(raw, "reportMetadata") || strings.Contains(raw, "idsubdatatable") {
		t.Errorf("metadata leaked: %s", raw)
	}
}

func TestReportsGetFilterLimitIsAlwaysLimitPlusOneAndNeverMinusOne(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(5), &seen))
	for _, limit := range []int{1, 10, 100, 999, 1000} {
		getReport(t, env, `{`+pagesArgs+`,"limit":`+strconv.Itoa(limit)+`}`)
		if got := seen[len(seen)-1].form.Get("filter_limit"); got != strconv.Itoa(limit+1) {
			t.Errorf("limit %d: filter_limit = %s", limit, got)
		}
	}
	for _, c := range seen {
		if c.form.Get("filter_limit") == "-1" || c.form.Get("filter_limit") == "" {
			t.Errorf("filter_limit = %q", c.form.Get("filter_limit"))
		}
	}
}

func TestReportsGetPagesWithCursorToTheLastRow(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(25), &seen))
	var labels []string
	cursor, pages := "", 0
	for {
		args := `{` + pagesArgs + `,"limit":10`
		if cursor != "" {
			args += `,"cursor":"` + cursor + `"`
		}
		got := getReport(t, env, args+`}`)
		pages++
		for _, row := range got.Rows {
			labels = append(labels, row["label"].(string))
		}
		if !got.HasMore {
			if got.NextCursor != "" {
				t.Errorf("cursor on the last page")
			}
			break
		}
		cursor = got.NextCursor
	}
	if pages != 3 || len(labels) != 25 || labels[24] != "/page-24" {
		t.Fatalf("pages = %d, labels = %v", pages, labels)
	}
	if got := seen[1].form; got.Get("filter_offset") != "10" || got.Get("filter_limit") != "11" {
		t.Errorf("second page sent %v", got)
	}
}

func TestReportsGetHasMoreOnFullAndLastPage(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(10), nil))
	if got := getReport(t, env, `{`+pagesArgs+`,"limit":10}`); got.HasMore || got.NextCursor != "" || len(got.Rows) != 10 {
		t.Errorf("exactly full page: has_more = %v, rows = %d", got.HasMore, len(got.Rows))
	}
	if got := getReport(t, env, `{`+pagesArgs+`,"limit":9}`); !got.HasMore || len(got.Rows) != 9 {
		t.Errorf("one row beyond: has_more = %v, rows = %d", got.HasMore, len(got.Rows))
	}
}

func TestReportsGetCursorIsBoundAndManipulationRefused(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(50), nil))
	first := getReport(t, env, `{`+pagesArgs+`,"limit":10}`)
	cursor := first.NextCursor
	for name, args := range map[string]string{
		"other report":  `{"idSite":1,"report":"Actions_getPageTitles","period":"month","date":"2026-09-01","limit":10,"cursor":"` + cursor + `"}`,
		"other date":    `{"idSite":1,"report":"Actions_getPageUrls","period":"month","date":"2026-08-01","limit":10,"cursor":"` + cursor + `"}`,
		"other limit":   `{` + pagesArgs + `,"limit":20,"cursor":"` + cursor + `"}`,
		"other columns": `{` + pagesArgs + `,"columns":["nb_hits"],"cursor":"` + cursor + `"}`,
		"other sort":    `{` + pagesArgs + `,"sort_column":"nb_hits","cursor":"` + cursor + `"}`,
		"altered":       `{` + pagesArgs + `,"cursor":"` + cursor[:len(cursor)-2] + `AA"}`,
		"garbage":       `{` + pagesArgs + `,"cursor":"AAAA"}`,
	} {
		if _, err := env.invoke("matomo.reports.get", "open", args); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
	if _, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`,"cursor":"`+cursor+`"}`); err != nil {
		t.Errorf("continuation without limit: %v", err)
	}
}

func TestReportsGetColumnsChangeTheRequestAndTheOutput(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(func(c call) string {
		if c.form.Get("showColumns") == "nb_hits" {
			return processed(`{"label":"Page URL","nb_hits":"Pageviews"}`, `[{"label":"/a","nb_hits":3}]`, `[]`, `[]`)
		}
		return processed(pageColumns, `[{"label":"/a","nb_hits":3,"bounce_rate":0.5}]`, `[]`, `[]`)
	}, &seen))
	all := getReport(t, env, `{`+pagesArgs+`}`)
	some := getReport(t, env, `{`+pagesArgs+`,"columns":["nb_hits"]}`)
	if seen[0].form.Get("showColumns") != "" || seen[1].form.Get("showColumns") != "nb_hits" {
		t.Errorf("showColumns = %q, %q", seen[0].form.Get("showColumns"), seen[1].form.Get("showColumns"))
	}
	if _, found := all.Rows[0]["bounce_rate"]; !found {
		t.Errorf("all columns = %v", all.Rows[0])
	}
	if _, found := some.Rows[0]["bounce_rate"]; found || len(some.Columns) != 2 || some.Totals != nil {
		t.Errorf("selected columns = %v, %v", some.Rows[0], some.Columns)
	}
}

func TestReportsGetIncludeMetadataAddsReportAndRowMetadata(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, reportServer(func(call) string {
		return processed(pageColumns, pageRows(2), pageMeta(2), `[]`)
	}, nil))
	got := getReport(t, env, `{`+pagesArgs+`,"include_metadata":true}`)
	if got.Metadata["module"] != "Actions" || len(got.RowMetadata) != 2 || got.RowMetadata[1]["idsubdatatable"] != float64(8) ||
		got.RowMetadata[0]["url"] != "https://shop.example.test/page-0" {
		t.Fatalf("got = %+v", got)
	}
}

func TestReportsGetWithoutDimensionReturnsOneRow(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(func(call) string {
		return processed(`{"nb_visits":"Visits","nb_uniq_visitors":"Unique visitors"}`, `{"nb_visits":42,"nb_uniq_visitors":30}`, `[]`, `[]`)
	}, &seen))
	got := getReport(t, env, `{"idSite":1,"report":"Goals_get_idGoal--3","period":"day","date":"today"}`)
	if len(got.Rows) != 1 || got.Rows[0]["nb_visits"] != float64(42) || got.HasMore || len(got.Columns) != 2 {
		t.Fatalf("got = %+v", got)
	}
	if seen[0].form.Get("apiParameters[idGoal]") != "3" || seen[0].form.Get("apiModule") != "Goals" {
		t.Errorf("sent %v", seen[0].form)
	}
}

func TestReportsGetSendsCatalogParametersOfCustomDimension(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(1), &seen))
	getReport(t, env, `{"idSite":1,"report":"CustomDimensions_getCustomDimension_idDimension--2","period":"day","date":"today"}`)
	if seen[0].form.Get("apiParameters[idDimension]") != "2" {
		t.Errorf("sent %v", seen[0].form)
	}
}

func timeSeries(days int) func(call) string {
	return func(c call) string {
		keys := make([]string, days)
		meta := make([]string, days)
		start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		for i := range keys {
			day := start.AddDate(0, 0, i).Format("Monday, January 2, 2006")
			keys[i] = `"` + day + `":{"nb_visits":` + strconv.Itoa(i+1) + `}`
			meta[i] = `"` + day + `":[]`
		}
		return processed(`{"nb_visits":"Visits"}`, "{"+strings.Join(keys, ",")+"}", "{"+strings.Join(meta, ",")+"}", `{"nb_visits":465}`)
	}
}

func TestReportsGetDateRangeReadsOneRowPerPeriodInOrder(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(timeSeries(30), &seen))
	got := getReport(t, env, `{"idSite":1,"report":"Goals_get_idGoal--3","period":"day","date":"last30"}`)
	if len(got.Rows) != 30 || got.HasMore || got.Rows[0][periodLabelKey] != "Tuesday, September 1, 2026" ||
		got.Rows[29][periodLabelKey] != "Wednesday, September 30, 2026" || got.Rows[29]["nb_visits"] != float64(30) {
		t.Fatalf("got = %+v", got.Rows)
	}
	if seen[0].form.Get("filter_limit") != "34" {
		t.Errorf("default limit for 30 periods: filter_limit = %s, want 34 (limit 33)", seen[0].form.Get("filter_limit"))
	}
}

func TestReportsGetLimitTimesPeriodsIsBounded(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(timeSeries(30), &seen))
	cases := map[string]string{
		"limit 100 over 30 days": `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last30","limit":100}`,
		"explicit range":         `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"2026-09-01,2026-09-30","limit":34}`,
		"week range":             `{"idSite":1,"report":"Actions_getPageUrls","period":"week","date":"last52","limit":20}`,
		"too many periods":       `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last1001"}`,
		"limit over 1000":        `{` + pagesArgs + `,"limit":1001}`,
		"limit zero negative":    `{` + pagesArgs + `,"limit":-1}`,
	}
	for name, args := range cases {
		_, err := env.invoke("matomo.reports.get", "open", args)
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
	if _, err := env.invoke("matomo.reports.get", "open", cases["limit 100 over 30 days"]); err == nil ||
		!strings.Contains(err.Error(), "use limit 33 or less") {
		t.Errorf("message = %v, want the allowed combination", err)
	}
	if len(seen) != 0 {
		t.Errorf("a refused combination read the report")
	}
	if _, err := env.invoke("matomo.reports.get", "open",
		`{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last30","limit":33}`); err != nil {
		t.Errorf("limit 33 over 30 days: %v", err)
	}
}

func TestReportsGetRangePeriodIsOneTable(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(3), &seen))
	got := getReport(t, env, `{"idSite":1,"report":"Actions_getPageUrls","period":"range","date":"2026-09-01,2026-09-30","limit":1000}`)
	if len(got.Rows) != 3 || got.Rows[0][periodLabelKey] != nil {
		t.Fatalf("got = %+v", got.Rows)
	}
}

func TestReportsGetPeriodAndDateValidation(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(1), nil))
	for _, args := range []string{
		`{"idSite":1,"report":"Actions_getPageUrls","period":"day"}`,
		`{"idSite":1,"report":"Actions_getPageUrls","date":"today"}`,
		`{"idSite":1,"report":"Actions_getPageUrls","period":"hour","date":"today"}`,
		`{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"2026-13-01"}`,
		`{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"2026-09-30,2026-09-01"}`,
		`{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last0"}`,
		`{"idSite":1,"report":"Actions_getPageUrls","period":"range","date":"today"}`,
		`{"idSite":1,"period":"day","date":"today"}`,
		`{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"today","language":"DE"}`,
	} {
		if _, err := env.invoke("matomo.reports.get", "open", args); err == nil {
			t.Errorf("accepted %s", args)
		}
	}
}

func TestReportsGetUnknownReportIsNotFoundBeforeTheReport(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(1), &seen))
	for _, id := range []string{"Actions_getNothing", "Actions.getPageUrls", "actions_getpageurls"} {
		_, err := env.invoke("matomo.reports.get", "open", `{"idSite":1,"report":"`+id+`","period":"day","date":"today"}`)
		if classOf(err) != provider.ClassNotFound {
			t.Errorf("%s: err = %v, want not-found", id, err)
		}
	}
	if len(seen) != 0 {
		t.Errorf("an unknown report reached getProcessedReport")
	}
}

func TestReportsGetFilterArgumentsReachMatomo(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(1), &seen))
	getReport(t, env, `{`+pagesArgs+`,"segment":"browserName==Firefox;countryCode==de","sort_column":"nb_hits",`+
		`"sort_order":"asc","filter_column":"label","filter_pattern":"^/shop","filter_excludelowpop":"nb_hits",`+
		`"filter_excludelowpop_value":5,"flat":true,"subtable":9,"language":"de"}`)
	sent := seen[0].form
	for name, want := range map[string]string{"segment": "browserName==Firefox;countryCode==de",
		"filter_sort_column": "nb_hits", "filter_sort_order": "asc", "filter_column": "label", "filter_pattern": "^/shop",
		"filter_excludelowpop": "nb_hits", "filter_excludelowpop_value": "5", "flat": "1", "idSubtable": "9",
		"language": "de"} {
		if sent.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.Get(name), want)
		}
	}
	if sent.Get("keep_flattened_dimension_columns") != "" {
		t.Errorf("keep_flattened_dimension_columns without columns")
	}
}

func TestReportsGetRejectsInvalidArguments(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(1), &seen))
	for name, extra := range map[string]string{
		"segment without operator": `"segment":"browserName"`,
		"segment with empty term":  `"segment":"browserName==a;;countryCode==de"`,
		"segment control":          `"segment":"browserName==a\nb"`,
		"segment too long":         `"segment":"a==` + strings.Repeat("x", 1000) + `"`,
		"column injection":         `"columns":["nb_hits,label"]`,
		"duplicate column":         `"columns":["nb_hits","nb_hits"]`,
		"empty columns":            `"columns":[]`,
		"sort injection":           `"sort_column":"label&filter_limit=-1"`,
		"sort order":               `"sort_order":"up"`,
		"column without pattern":   `"filter_column":"label"`,
		"long pattern":             `"filter_pattern":"` + strings.Repeat("a", 101) + `"`,
		"lowpop value alone":       `"filter_excludelowpop_value":3`,
		"negative lowpop":          `"filter_excludelowpop":"nb_hits","filter_excludelowpop_value":-1`,
		"subtable zero":            `"subtable":0`,
		"too many labels":          `"label":["a","b","c","d","e","f","g","h","i","j","k"]`,
		"empty label":              `"label":[""]`,
	} {
		_, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`,`+extra+`}`)
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if len(seen) != 0 {
		t.Errorf("a rejected argument reached Matomo")
	}
}

func TestReportsGetForbiddenParametersAreNotOffered(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(1), &seen))
	for _, name := range []string{"disable_generic_filters", "disable_queued_filters", "disable_root_datatable_post_processor",
		"callback", "jsoncallback", "filter_limit", "filter_offset", "idSites", "apiModule", "apiAction", "apiParameters",
		"showRawMetrics", "hideMetricsDoc", "showTimer", "format_metrics", "format", "token_auth", "compareDates"} {
		if _, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`,"`+name+`":"1"}`); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := env.invoke("matomo.reports.get", "open", `{"idSite":"all","report":"Actions_getPageUrls","period":"day","date":"today"}`); err == nil {
		t.Error("idSite all was accepted")
	}
	if len(seen) != 0 {
		t.Errorf("a forbidden parameter reached Matomo")
	}
	descriptor, _, _ := registry(t).Lookup("matomo.reports.get")
	for _, argument := range descriptor.Arguments {
		if strings.HasPrefix(argument.Name, "disable_") || strings.Contains(argument.Name, "callback") {
			t.Errorf("argument %s offered", argument.Name)
		}
	}
}

func TestReportsGetRefusesCatalogParametersThatCouldSteerFilters(t *testing.T) {
	hostile := strings.Replace(catalogFixture, `"parameters":{"idGoal":"3"}`, `"parameters":{"filter_limit":"-1"}`, 1)
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, with(reportServer(pagedPages(1), &seen), answers{
		"API.getReportMetadata": func(call) *http.Response { return ok(hostile) }}))
	_, err := env.invoke("matomo.reports.get", "open", `{"idSite":1,"report":"Goals_get_idGoal--3","period":"day","date":"today"}`)
	if classOf(err) != provider.ClassInvalidResponse || len(seen) != 0 {
		t.Fatalf("err = %v, seen = %d", err, len(seen))
	}
}

func TestReportsGetLabelFilterIsPagedOnTheClientSide(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(func(call) string {
		return processed(pageColumns, pageRows(5), `[]`, `[]`)
	}, &seen))
	first := getReport(t, env, `{`+pagesArgs+`,"label":["a","b"],"limit":2}`)
	if len(first.Rows) != 2 || !first.HasMore {
		t.Fatalf("first = %+v", first)
	}
	if seen[0].form["label[]"][1] != "b" {
		t.Errorf("labels = %v", seen[0].form["label[]"])
	}
	second := getReport(t, env, `{`+pagesArgs+`,"label":["a","b"],"cursor":"`+first.NextCursor+`"}`)
	if len(second.Rows) != 2 || second.Rows[0]["label"] != "/page-2" || !second.HasMore {
		t.Fatalf("second = %+v", second.Rows)
	}
}

func TestReportsGetCutsRowsThatMatomoDidNotLimit(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, reportServer(func(call) string {
		return processed(pageColumns, pageRows(300), `[]`, `[]`)
	}, nil))
	got := getReport(t, env, `{`+pagesArgs+`,"limit":10}`)
	if len(got.Rows) != 10 || !got.HasMore {
		t.Fatalf("rows = %d, has_more = %v", len(got.Rows), got.HasMore)
	}
	next := getReport(t, env, `{`+pagesArgs+`,"limit":10,"cursor":"`+got.NextCursor+`"}`)
	if next.Rows[0]["label"] != "/page-10" {
		t.Errorf("next = %v", next.Rows[0])
	}
}

func TestReportsGetBoundsLabelsAndDropsNestedValues(t *testing.T) {
	long := strings.Repeat("ä", 1000)
	var calls []call
	env := newCatalogEnvironment(t, &calls, reportServer(func(call) string {
		return processed(`{"label":"`+long+`","nb_hits":"Pageviews"}`,
			`[{"label":"`+long+`","nb_hits":3,"comparisons":[{"label":"x"}],"goals":{"1":2}}]`, `[{"url":"`+long+`"}]`, `{"x":{"y":{"z":{"w":{"v":1}}}}}`)
	}, nil))
	got := getReport(t, env, `{`+pagesArgs+`,"include_metadata":true}`)
	label := got.Rows[0]["label"].(string)
	if len(label) > maxStringLength || len(label) < 400 || got.Rows[0]["comparisons"] != nil || got.Rows[0]["goals"] != nil {
		t.Errorf("row = %v", got.Rows[0])
	}
	if len(got.Columns[0].Label) > maxStringLength || len(got.RowMetadata[0]["url"].(string)) > maxReportText {
		t.Errorf("unbounded text")
	}
}

func TestReportsGetMapsErrorsAndRespectsTargets(t *testing.T) {
	for name, tc := range map[string]struct {
		message string
		class   provider.Class
	}{
		"permission":  {permissionMsg, provider.ClassPermission},
		"report gone": {"Requested report Actions.getPageUrls for Website id=1 not found in the list of available reports", provider.ClassNotFound},
		"not found":   {"An unexpected website was found in the request: website id = 1 not found", provider.ClassNotFound},
	} {
		var calls []call
		env := newCatalogEnvironment(t, &calls, reportServer(func(call) string { return "error:" + tc.message }, nil))
		_, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`}`)
		if classOf(err) != tc.class {
			t.Errorf("%s: err = %v, want %s", name, err, tc.class)
		}
		if err != nil && strings.Contains(err.Error(), "website id") {
			t.Errorf("%s: provider text in %q", name, err.Error())
		}
	}
	var calls []call
	reads := 0
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(1), nil))
	env.reads = &reads
	if _, err := env.invoke("matomo.reports.get", "one", `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"today"}`); err == nil {
		t.Fatal("foreign website accepted")
	}
	if len(calls) != 0 || reads != 0 {
		t.Errorf("foreign target caused %d requests and %d secret reads", len(calls), reads)
	}
}

func TestReportsGetFlatWithColumnsNeedsMatomo5140(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, with(reportServer(pagedPages(1), &seen), versionOnly("5.13.0")))
	_, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`,"flat":true,"columns":["nb_hits"]}`)
	if !isUnsupported(err) || !strings.Contains(err.Error(), "5.14.0") || len(seen) != 0 {
		t.Fatalf("err = %v", err)
	}
	if _, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`,"flat":true}`); err != nil {
		t.Errorf("flat without columns: %v", err)
	}
	env = newCatalogEnvironment(t, &calls, reportServer(pagedPages(1), &seen))
	getReport(t, env, `{`+pagesArgs+`,"flat":true,"columns":["nb_hits"]}`)
	if lastReport(seen).form.Get("keep_flattened_dimension_columns") != "1" {
		t.Errorf("keep_flattened_dimension_columns not sent: %v", lastReport(seen).form)
	}
}

func TestReportsGetErrorsOfTheTransport(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, with(catalogAnswers(nil), answers{
		"API.getProcessedReport": func(call) *http.Response { return respond(http.StatusTooManyRequests, "") }}))
	if _, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`}`); classOf(err) != provider.ClassRateLimited {
		t.Errorf("429: err = %v", err)
	}
	env = newCatalogEnvironment(t, &calls, with(catalogAnswers(nil), answers{
		"API.getProcessedReport": func(call) *http.Response { return ok("<html>login</html>") }}))
	if _, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("html: err = %v", err)
	}
}

func TestReportsGetOutputAndRequestCarryNoToken(t *testing.T) {
	var calls, seen []call
	env := newCatalogEnvironment(t, &calls, reportServer(pagedPages(2), &seen))
	raw, err := env.invoke("matomo.reports.get", "open", `{`+pagesArgs+`}`)
	if err != nil {
		t.Fatal(err)
	}
	c := seen[0]
	if c.auth != "Bearer "+tokenValue || c.rawQuery != "" || c.form.Get("token_auth") != "" || strings.Contains(raw, tokenValue) {
		t.Errorf("auth = %q, query = %q", c.auth, c.rawQuery)
	}
}

func TestReportsGetIsInTheReadProfileAndReadRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	profile, _ := metadata.RecommendedProfile()
	if !slices.Contains(profile.Tools, "matomo.reports.get") {
		t.Errorf("profile = %v", profile.Tools)
	}
	descriptor, _, _ := reg.Lookup("matomo.reports.get")
	risk := descriptor.Risk
	if risk.Effect != "read" || !risk.OpenWorld || risk.DataSensitivity == "" ||
		!strings.Contains(descriptor.Description, "Others") || !strings.Contains(descriptor.Description, "no total") {
		t.Errorf("descriptor = %+v", descriptor)
	}
}

func TestPlanPeriodCountsPeriods(t *testing.T) {
	for _, tc := range []struct {
		period, date string
		points       int
		multiple     bool
	}{
		{"day", "today", 1, false}, {"day", "2026-09-01", 1, false}, {"day", "last30", 30, true},
		{"day", "2026-09-01,2026-09-30", 30, true}, {"day", "2026-09-01,2026-09-01", 1, true},
		{"week", "2026-09-01,2026-09-30", 5, true}, {"week", "2026-08-31,2026-09-06", 1, true},
		{"month", "2026-01-15,2026-12-01", 12, true}, {"year", "2020-01-01,2026-12-31", 7, true},
		{"month", "previous3", 3, true}, {"range", "2026-09-01,2026-09-30", 1, false}, {"range", "last30", 1, false},
	} {
		plan, err := planPeriod(tc.period, tc.date)
		if err != nil || plan.points != tc.points || plan.multiple != tc.multiple {
			t.Errorf("%s %s = %+v, %v, want %d %v", tc.period, tc.date, plan, err, tc.points, tc.multiple)
		}
	}
}
