package matomo

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const segmentsFixture = `[
{"type":"dimension","category":"Visit Location","name":"Country","segment":"countryCode","acceptedValues":"de, us, fr","permission":1},
{"type":"dimension","category":"Behaviour","name":"Page URL","segment":"pageUrl","acceptedValues":"All URLs","permission":1},
{"type":"metric","category":"Visitors","name":"Visits","segment":"visitCount","acceptedValues":"0, 1, 2","permission":1},
{"type":"dimension","category":"Behaviour","name":"Page title","segment":"pageTitle","permission":1}
]`

func segmentAnswers() answers {
	return with(versionOnly("5.14.2"), answers{
		"API.getSegmentsMetadata": func(call) *http.Response { return ok(segmentsFixture) },
		"API.getSuggestedValuesForSegment": func(c call) *http.Response {
			switch c.form.Get("segmentName") {
			case "browserName":
				return ok(`["Chrome","Firefox",7,"",{"x":1}]`)
			case "pageUrl":
				return ok(`[]`)
			}
			return failure("The segment does not exist")
		},
	})
}

func TestSegmentsFieldsListsFieldsWithOperatorsAndFilters(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, segmentAnswers())
	result, err := env.invoke("matomo.segments.fields", "open", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out SegmentFieldsResult
	_ = json.Unmarshal([]byte(result), &out)
	if len(out.Fields) != 4 || out.Fields[0].Segment != "countryCode" || out.Fields[0].Type != "dimension" ||
		!slices.Contains(out.Fields[0].Operators, "=@") || !slices.Contains(out.Fields[2].Operators, "<=") ||
		out.Fields[0].AcceptedValues != "de, us, fr" || len(out.Categories) != 3 || out.IDSite != 1 {
		t.Fatalf("out = %s", result)
	}
	if last := calls[len(calls)-1]; last.api() != "API.getSegmentsMetadata" || last.form.Get("idSites") != "1" {
		t.Errorf("request = %v", last.form)
	}
	filtered, _ := env.invoke("matomo.segments.fields", "open", `{"idSite":1,"category":"behaviour","limit":1}`)
	var page SegmentFieldsResult
	_ = json.Unmarshal([]byte(filtered), &page)
	if len(page.Fields) != 1 || page.Fields[0].Segment != "pageUrl" || !page.HasMore || page.Categories != nil {
		t.Fatalf("filtered = %s", filtered)
	}
	next, err := env.invoke("matomo.segments.fields", "open", `{"idSite":1,"category":"behaviour","cursor":"`+page.NextCursor+`"}`)
	if err != nil || !strings.Contains(next, "pageTitle") || strings.Contains(next, "next_cursor") {
		t.Fatalf("next = %s, err = %v", next, err)
	}
	if _, err := env.invoke("matomo.segments.fields", "open", `{"idSite":1,"category":"Visitors","cursor":"`+page.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another filter: err = %v", err)
	}
}

func TestSegmentsFieldsMapsErrors(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.getSegmentsMetadata": func(call) *http.Response { return failure(permissionMsg) },
	}))
	if _, err := env.invoke("matomo.segments.fields", "open", `{"idSite":1}`); classOf(err) != provider.ClassPermission {
		t.Errorf("err = %v, want permission", err)
	}
}

func TestSuggestValuesReturnsBoundedValuesAndEmptyAnswers(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, segmentAnswers())
	result, err := env.invoke("matomo.segments.suggest-values", "open", `{"idSite":1,"segment":"browserName"}`)
	if err != nil || result != `{"idSite":1,"segment":"browserName","values":["Chrome","Firefox","7"]}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if last := calls[len(calls)-1]; last.form.Get("idSite") != "1" || last.form.Get("segmentName") != "browserName" {
		t.Errorf("request = %v", last.form)
	}
	empty, err := env.invoke("matomo.segments.suggest-values", "open", `{"idSite":1,"segment":"pageUrl"}`)
	if err != nil || empty != `{"idSite":1,"segment":"pageUrl"}` {
		t.Fatalf("empty = %s, err = %v", empty, err)
	}
	if _, err := env.invoke("matomo.segments.suggest-values", "open", `{"idSite":1,"segment":"nope"}`); classOf(err) != provider.ClassNotFound {
		t.Errorf("unknown segment: err = %v, want not-found", err)
	}
	for _, bad := range []string{`{"idSite":1}`, `{"idSite":1,"segment":"a b"}`, `{"idSite":1,"segment":"x;y"}`} {
		if _, err := env.invoke("matomo.segments.suggest-values", "open", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	many := make([]string, 300)
	for i := range many {
		many[i] = `"` + strings.Repeat("v", 600) + `"`
	}
	var calls2 []call
	env2 := newCatalogEnvironment(t, &calls2, with(versionOnly("5.14.2"), answers{
		"API.getSuggestedValuesForSegment": func(call) *http.Response { return ok("[" + strings.Join(many, ",") + "]") },
	}))
	big, _ := env2.invoke("matomo.segments.suggest-values", "open", `{"idSite":1,"segment":"pageUrl"}`)
	var out SuggestedValuesResult
	_ = json.Unmarshal([]byte(big), &out)
	if len(out.Values) != maxSuggestedValues || len(out.Values[0]) > maxStringLength {
		t.Errorf("values = %d of length %d", len(out.Values), len(out.Values[0]))
	}
}
