package matomo

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The fixtures follow API.getRowEvolution: one label answers every metric of the report per period, several
// labels answer one column per label, and a period without the row is an empty list.
const singleEvolution = `{"label":"shop.example.test/pricing","reportData":{"2026-09-01":[{"nb_visits":5,"nb_hits":9}],` +
	`"2026-09-02":[],"2026-09-03":[{"nb_visits":7,"nb_hits":10,"label_html":"<b>x</b>"}]},` +
	`"metadata":{"metrics":{"nb_visits":{"name":"Unique Pageviews","min":5,"max":7,"change":"+40%"},` +
	`"nb_hits":{"name":"Pageviews","min":9,"max":10,"change":"+11%","logo":"plugins/Morpheus/icons/a.png"}},"dimension":"Page URL"},` +
	`"logo":"plugins/Morpheus/icons/b.png"}`

const multiEvolution = `{"column":"nb_visits","reportData":{"2026-08":[{"nb_visits_0":5,"nb_visits_1":3}],` +
	`"2026-09":[{"nb_visits_0":7,"nb_visits_1":0}]},"metadata":{"metrics":{` +
	`"nb_visits_0":{"name":"Direct Entry (Visits)","min":5,"max":7,"change":"+40%"},` +
	`"nb_visits_1":{"name":"Websites (Visits)","min":3,"max":3}},` +
	`"columns":{"nb_visits":"Visits","nb_actions":"Actions"},"dimension":"Channel Type"}}`

type evolutionOut struct {
	IDSite    int        `json:"idSite"`
	Report    ReportInfo `json:"report"`
	Period    string     `json:"period"`
	Date      string     `json:"date"`
	Label     string     `json:"label"`
	Column    string     `json:"column"`
	Dimension string     `json:"dimension"`
	Logo      string     `json:"logo"`
	Metrics   []struct {
		Name, Label, Change, Logo string
		Min, Max                  json.Number
	} `json:"metrics"`
	Columns []ReportColumn `json:"columns"`
	Points  []struct {
		Period string         `json:"period"`
		Values map[string]any `json:"values"`
	} `json:"points"`
}

func readEvolution(t *testing.T, env *environment, connection, arguments string) evolutionOut {
	t.Helper()
	result, err := env.invoke("matomo.reports.evolution", connection, arguments)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var out evolutionOut
	if json.Unmarshal([]byte(result), &out) != nil {
		t.Fatalf("result = %s", result)
	}
	return out
}

func TestReportsEvolutionOneLabelAnswersEveryMetric(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("API.getRowEvolution", fixed(singleEvolution)))
	out := readEvolution(t, env, "lr-open", `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last3",`+
		`"label":["/pricing"],"language":"de","segment":"countryCode==de","labelUseAbsoluteUrl":false}`)
	if out.IDSite != 1 || out.Report.ID != "Actions_getPageUrls" || out.Label != "shop.example.test/pricing" ||
		out.Dimension != "Page URL" || out.Logo != "plugins/Morpheus/icons/b.png" || out.Column != "" {
		t.Errorf("out = %+v", out)
	}
	if len(out.Metrics) != 2 || out.Metrics[0].Name != "nb_visits" || out.Metrics[0].Label != "Unique Pageviews" ||
		out.Metrics[0].Min != "5" || out.Metrics[0].Max != "7" || out.Metrics[0].Change != "+40%" ||
		out.Metrics[1].Logo != "plugins/Morpheus/icons/a.png" {
		t.Errorf("metrics = %+v", out.Metrics)
	}
	if len(out.Points) != 3 || out.Points[0].Period != "2026-09-01" || out.Points[0].Values["nb_hits"] != json.Number("9") &&
		out.Points[0].Values["nb_hits"] != float64(9) || len(out.Points[1].Values) != 0 || out.Points[2].Period != "2026-09-03" {
		t.Errorf("points = %+v", out.Points)
	}
	sent := lastCall(calls, "API.getRowEvolution").form
	if sent.Get("apiModule") != "Actions" || sent.Get("apiAction") != "getPageUrls" || sent.Get("idSite") != "1" ||
		sent.Get("period") != "day" || sent.Get("date") != "last3" || sent["label[]"][0] != "/pricing" ||
		sent.Get("language") != "de" || sent.Get("segment") != "countryCode==de" || sent.Get("labelUseAbsoluteUrl") != "0" ||
		sent.Has("legendAppendMetric") || sent.Has("column") || sent.Has("label") {
		t.Errorf("form = %v", sent)
	}
}

func TestReportsEvolutionSeveralLabelsAnswerOneColumn(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("API.getRowEvolution", fixed(multiEvolution)))
	out := readEvolution(t, env, "lr-open", `{"idSite":1,"report":"Referrers_getReferrerType","period":"month","date":"2026-08-01,2026-09-30",`+
		`"label":["Direct Entry","Websites"],"column":"nb_visits","legendAppendMetric":false}`)
	if out.Column != "nb_visits" || out.Label != "" || len(out.Metrics) != 2 || out.Metrics[1].Name != "nb_visits_1" ||
		out.Metrics[1].Label != "Websites (Visits)" || out.Metrics[1].Change != "" || len(out.Columns) != 2 ||
		out.Columns[1].Name != "nb_actions" || len(out.Points) != 2 || out.Points[1].Values["nb_visits_0"] == nil {
		t.Errorf("out = %+v", out)
	}
	sent := lastCall(calls, "API.getRowEvolution").form
	if strings.Join(sent["label[]"], "|") != "Direct Entry|Websites" || sent.Get("column") != "nb_visits" ||
		sent.Get("legendAppendMetric") != "0" || sent.Get("date") != "2026-08-01,2026-09-30" {
		t.Errorf("form = %v", sent)
	}
}

func TestReportsEvolutionSendsCatalogEntityIDsAndGoalMetrics(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("API.getRowEvolution", fixed(`[]`)))
	out := readEvolution(t, env, "lr-open", `{"idSite":1,"report":"CustomDimensions_getCustomDimension_idDimension--2",`+
		`"period":"week","date":"last4","label":["Pro"],"showGoalMetricsForGoal":"-1"}`)
	if len(out.Points) != 0 || len(out.Metrics) != 0 {
		t.Errorf("out = %+v, want an empty series", out)
	}
	sent := lastCall(calls, "API.getRowEvolution").form
	if sent.Get("idDimension") != "2" || sent.Get("showGoalMetricsForGoal") != "-1" || sent.Has("apiParameters[idDimension]") {
		t.Errorf("form = %v", sent)
	}
	if lastCall(calls, "API.isPluginActivated").form.Get("pluginName") == "" {
		t.Error("the Goals plugin was not checked for the goal metrics")
	}
}

func TestReportsEvolutionRefusesReportsItCannotFollow(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("API.getRowEvolution", fixed(singleEvolution)))
	for name, tc := range map[string]struct {
		arguments string
		class     provider.Class
		invalid   bool
	}{
		"unknown report":      {`{"idSite":1,"report":"Nope_get","period":"day","date":"last3","label":["a"]}`, provider.ClassNotFound, false},
		"report without rows": {`{"idSite":1,"report":"Goals_get_idGoal--3","period":"day","date":"last3","label":["a"]}`, "", true},
	} {
		_, err := env.invoke("matomo.reports.evolution", "lr-open", tc.arguments)
		if tc.invalid && !isInvalidRequest(err) || !tc.invalid && classOf(err) != tc.class {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if countMethod(calls, "API.getRowEvolution") != 0 {
		t.Error("a report outside the catalog or without rows reached Matomo")
	}
	// A catalog that names other parameters than the entity IDs is not sent.
	odd := `[{"category":"X","name":"Odd","module":"Actions","action":"getOdd","dimension":"D","parameters":{"filter_limit":"-1"},"uniqueId":"Odd"}]`
	env = newListedEnvironment(t, &calls, with(listedAnswers("API.getRowEvolution", fixed(singleEvolution)), answers{
		"API.getReportMetadata": fixed(odd)}))
	_, err := env.invoke("matomo.reports.evolution", "lr-open", `{"idSite":1,"report":"Odd","period":"day","date":"last3","label":["a"]}`)
	if classOf(err) != provider.ClassInvalidResponse || countMethod(calls, "API.getRowEvolution") != 0 {
		t.Errorf("odd catalog parameter: err = %v", err)
	}
}

func TestReportsEvolutionBoundsLabelsTimesPeriods(t *testing.T) {
	const base = `"idSite":1,"report":"Actions_getPageUrls","period":"day",`
	label := func(n int) string {
		labels := make([]string, n)
		for i := range labels {
			labels[i] = `"l` + strconv.Itoa(i) + `"`
		}
		return `"label":[` + strings.Join(labels, ",") + `]`
	}
	for name, tc := range map[string]struct {
		arguments string
		accepted  bool
	}{
		"one label, 1000 periods":     {`{` + base + `"date":"last1000",` + label(1) + `}`, true},
		"one label, 1001 periods":     {`{` + base + `"date":"last1001",` + label(1) + `}`, false},
		"two labels, 500 periods":     {`{` + base + `"date":"last500",` + label(2) + `}`, true},
		"two labels, 501 periods":     {`{` + base + `"date":"last501",` + label(2) + `}`, false},
		"ten labels, 100 periods":     {`{` + base + `"date":"previous100",` + label(10) + `}`, true},
		"ten labels, 101 periods":     {`{` + base + `"date":"previous101",` + label(10) + `}`, false},
		"one period":                  {`{` + base + `"date":"last1",` + label(1) + `}`, true},
		"last0":                       {`{` + base + `"date":"last0",` + label(1) + `}`, false},
		"range of 3 days":             {`{` + base + `"date":"2026-09-01,2026-09-03",` + label(1) + `}`, true},
		"reversed range":              {`{` + base + `"date":"2026-09-03,2026-09-01",` + label(1) + `}`, false},
		"one date is no series":       {`{` + base + `"date":"2026-09-01",` + label(1) + `}`, false},
		"today is no series":          {`{` + base + `"date":"today",` + label(1) + `}`, false},
		"11 labels":                   {`{` + base + `"date":"last3",` + label(11) + `}`, false},
		"no label":                    {`{` + base + `"date":"last3"}`, false},
		"empty label list":            {`{` + base + `"date":"last3","label":[]}`, false},
		"empty label":                 {`{` + base + `"date":"last3","label":[""]}`, false},
		"repeated label":              {`{` + base + `"date":"last3","label":["a","a"]}`, false},
		"label with control":          {`{` + base + `"date":"last3","label":["a\nb"]}`, false},
		"long label":                  {`{` + base + `"date":"last3","label":["` + strings.Repeat("a", 257) + `"]}`, false},
		"column with one label":       {`{` + base + `"date":"last3",` + label(1) + `,"column":"nb_visits"}`, false},
		"legend with one label":       {`{` + base + `"date":"last3",` + label(1) + `,"legendAppendMetric":true}`, false},
		"bad column":                  {`{` + base + `"date":"last3",` + label(2) + `,"column":"a b"}`, false},
		"bad goal metrics":            {`{` + base + `"date":"last3",` + label(1) + `,"showGoalMetricsForGoal":"1;2"}`, false},
		"bad segment":                 {`{` + base + `"date":"last3",` + label(1) + `,"segment":"x"}`, false},
		"bad language":                {`{` + base + `"date":"last3",` + label(1) + `,"language":"de; drop"}`, false},
		"period range":                {`{"idSite":1,"report":"Actions_getPageUrls","period":"range","date":"last3",` + label(1) + `}`, false},
		"free method":                 {`{` + base + `"date":"last3",` + label(1) + `,"method":"API.getRowEvolution"}`, false},
		"free module":                 {`{` + base + `"date":"last3",` + label(1) + `,"apiModule":"Actions"}`, false},
		"callback":                    {`{` + base + `"date":"last3",` + label(1) + `,"callback":"x"}`, false},
		"all labels by omission only": {`{` + base + `"date":"last3","label":["a"],"filter_limit":-1}`, false},
	} {
		var calls []call
		env := newListedEnvironment(t, &calls, listedAnswers("API.getRowEvolution", fixed(`[]`)))
		_, err := env.invoke("matomo.reports.evolution", "lr-open", tc.arguments)
		if (err == nil) != tc.accepted {
			t.Errorf("%s: err = %v, want accepted = %t", name, err, tc.accepted)
		}
		if !tc.accepted && (len(calls) != 0 || *env.reads != 0) {
			t.Errorf("%s: refused arguments caused %d requests and %d secret reads", name, len(calls), *env.reads)
		}
	}
}

func TestReportsEvolutionReadsOnlyWhatItUnderstandsAndBoundsText(t *testing.T) {
	long := strings.Repeat("x", 5000)
	var calls []call
	body := `{"label":"` + long + `","reportData":{"` + long + `":[{"nb_visits":1,"deep":{"a":1},"label_html":"` + long + `"}]},` +
		`"metadata":{"metrics":{"nb_visits":{"name":"` + long + `","min":"low","max":2,"change":"` + long + `"}},"dimension":"` + long + `"}}`
	env := newListedEnvironment(t, &calls, listedAnswers("API.getRowEvolution", fixed(body)))
	result, err := env.invoke("matomo.reports.evolution", "lr-open", `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last3","label":["a"]}`)
	if err != nil || len(result) > 6*maxStringLength+600 || strings.Contains(result, "deep") || strings.Contains(result, `"min"`) {
		t.Errorf("len = %d, err = %v, result = %.300s", len(result), err, result)
	}
	for name, answer := range map[string]string{"text": `"x"`, "list of tables": `[{"a":1}]`, "report data text": `{"reportData":"x"}`,
		"table text": `{"reportData":{"2026-09-01":"x"}}`} {
		env = newListedEnvironment(t, &calls, listedAnswers("API.getRowEvolution", fixed(answer)))
		_, err := env.invoke("matomo.reports.evolution", "lr-open", `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last3","label":["a"]}`)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v, want invalid response", name, err)
		}
	}
}

func TestReportsEvolutionMapsMatomoRefusalOfRowlessReports(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("API.getRowEvolution", func(call) *http.Response {
		return failure("Requested report Actions.getPageUrls for Website id=1 not found in the list of available reports")
	}))
	_, err := env.invoke("matomo.reports.evolution", "lr-open", `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last3","label":["a"]}`)
	if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), "Website id") {
		t.Errorf("err = %v", err)
	}
}
