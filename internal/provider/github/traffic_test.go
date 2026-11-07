package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

const (
	documentedViews = `{"count":14850,"uniques":3782,"views":[{"timestamp":"2016-10-10T00:00:00Z","count":440,` +
		`"uniques":143},{"timestamp":"2016-10-11T00:00:00Z","count":1308,"uniques":414}]}`
	documentedClones = `{"count":173,"uniques":128,"clones":[{"timestamp":"2016-10-10T00:00:00Z","count":2,` +
		`"uniques":1},{"timestamp":"2016-10-11T00:00:00Z","count":17,"uniques":16}]}`
	documentedPaths = `[{"path":"/github/hubot","title":"github/hubot: A customizable life embetterment robot.",` +
		`"count":3542,"uniques":2225},{"path":"/github/hubot/blob/master/docs/scripting.md","title":"Scripting",` +
		`"count":1707,"uniques":804}]`
	documentedReferrers = `[{"referrer":"Google","count":4,"uniques":3},{"referrer":"docs.example.org","count":3,"uniques":1}]`
)

type trafficCase struct {
	id, route, body string
}

var trafficCases = []trafficCase{
	{trafficViewsGet.ID, "/api/v3/repos/octo-org/example/traffic/views", documentedViews},
	{trafficClonesGet.ID, "/api/v3/repos/octo-org/example/traffic/clones", documentedClones},
	{trafficPathsList.ID, "/api/v3/repos/octo-org/example/traffic/popular/paths", documentedPaths},
	{trafficReferrersList.ID, "/api/v3/repos/octo-org/example/traffic/popular/referrers", documentedReferrers},
}

func TestTrafficReportsEveryDocumentedMember(t *testing.T) {
	for _, tc := range trafficCases {
		f, core := cannedCore(t, tc.body, false, relationConfig)
		got, err := invoke(t, core, tc.id, "repo", `{}`, false)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		coversSchema(t, tc.id, got)
		request := lastRequest(t, f)
		if request.method != http.MethodGet || request.path != tc.route || request.query != "" {
			t.Errorf("%s request = %+v, want the fixed route", tc.id, request)
		}
		want := map[string]string{
			trafficViewsGet.ID: `{"count":14850,"uniques":3782,"views":[{"timestamp":"2016-10-10T00:00:00Z",` +
				`"count":440,"uniques":143},{"timestamp":"2016-10-11T00:00:00Z","count":1308,"uniques":414}],` +
				`"repository":"octo-org/example"}`,
			trafficClonesGet.ID: `{"count":173,"uniques":128,"clones":[{"timestamp":"2016-10-10T00:00:00Z",` +
				`"count":2,"uniques":1},{"timestamp":"2016-10-11T00:00:00Z","count":17,"uniques":16}],` +
				`"repository":"octo-org/example"}`,
			trafficPathsList.ID:     `{"paths":` + documentedPaths + `,"repository":"octo-org/example"}`,
			trafficReferrersList.ID: `{"referrers":` + documentedReferrers + `,"repository":"octo-org/example"}`,
		}[tc.id]
		sameJSON(t, tc.id, got, want)
	}
}

func TestTrafficPassesOnlyAValidSpan(t *testing.T) {
	for _, id := range []string{trafficViewsGet.ID, trafficClonesGet.ID} {
		for _, per := range []string{"day", "week"} {
			body := documentedViews
			if id == trafficClonesGet.ID {
				body = documentedClones
			}
			f, core := cannedCore(t, body, false, relationConfig)
			if _, err := invoke(t, core, id, "repo", `{"per":"`+per+`"}`, false); err != nil {
				t.Fatalf("%s per %s: %v", id, per, err)
			}
			if query, _ := url.ParseQuery(lastRequest(t, f).query); query.Get("per") != per {
				t.Errorf("%s per = %q, want %q", id, query.Get("per"), per)
			}
		}
	}
}

func TestTrafficRefusesBeforeIO(t *testing.T) {
	f, core, reads := guardedCore(t)
	for _, tc := range trafficCases {
		for _, c := range []struct{ name, connection, arguments string }{
			{"foreign repository", "repo", `{"repository":"other/example"}`},
			{"excluded repository", "repoexcluded", `{"repository":"octo-org/example"}`},
			{"project target", "planning", `{}`},
			{"invalid span", "repo", `{"per":"month"}`},
			{"upper case span", "repo", `{"per":"Day"}`},
		} {
			if strings.Contains(c.name, "span") && tc.id != trafficViewsGet.ID && tc.id != trafficClonesGet.ID {
				continue
			}
			*reads = 0
			before := len(f.recorded())
			if _, err := invoke(t, core, tc.id, c.connection, c.arguments, false); !isInvalidRequest(err) {
				t.Errorf("%s, %s = %v, want an invalid request", tc.id, c.name, err)
			}
			if *reads != 0 || len(f.recorded()) != before {
				t.Errorf("%s, %s reached the credential or GitHub", tc.id, c.name)
			}
		}
	}
}

func failingCore(t *testing.T, status int, message string) *application.Core {
	t.Helper()
	f := &fakeGitHub{failure: func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"message":%q}`, message)
		return true
	}}
	red := &redact.Redactor{}
	return application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
}

func TestTrafficExplainsAForbiddenToken(t *testing.T) {
	core := failingCore(t, http.StatusForbidden, "Resource not accessible by personal access token SECRET-PROVIDER-TEXT")
	for _, tc := range trafficCases {
		_, err := invoke(t, core, tc.id, "repo", `{}`, false)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "push access") ||
			strings.Contains(err.Error(), "SECRET-PROVIDER-TEXT") {
			t.Errorf("%s forbidden = %v, want a permission error naming push access and no provider text", tc.id, err)
		}
	}
}

func TestTrafficReportsAMissingRepository(t *testing.T) {
	core := failingCore(t, http.StatusNotFound, "Not Found")
	subjects := map[string]string{
		trafficViewsGet.ID:      "the traffic views of this repository",
		trafficClonesGet.ID:     "the traffic clones of this repository",
		trafficPathsList.ID:     "the popular paths of this repository",
		trafficReferrersList.ID: "the popular referrers of this repository",
	}
	for _, tc := range trafficCases {
		_, err := invoke(t, core, tc.id, "repo", `{}`, false)
		if classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), subjects[tc.id]) {
			t.Errorf("%s missing = %v, want not-found naming %q", tc.id, err, subjects[tc.id])
		}
	}
}

func TestTrafficCutsUntrustedText(t *testing.T) {
	longPath := "/" + strings.Repeat("p", projectionPathLimit+50)
	longName := strings.Repeat("t", projectionNameLimit+50)
	_, core := cannedCore(t, `[{"path":"`+longPath+`","title":"`+longName+`\u0007x","count":1,"uniques":1}]`, false, relationConfig)
	got, err := invoke(t, core, trafficPathsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := members(t, got, "paths", "#0")
	for key, limit := range map[string]int{"path": projectionPathLimit, "title": projectionNameLimit} {
		var text string
		if err := json.Unmarshal(entry[key], &text); err != nil || len([]rune(text)) != limit {
			t.Errorf("%s = %d runes (%v), want %d", key, len([]rune(text)), err, limit)
		}
	}
	_, core = cannedCore(t, `[{"referrer":"`+longName+`","count":1,"uniques":1}]`, false, relationConfig)
	got, err = invoke(t, core, trafficReferrersList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal(members(t, got, "referrers", "#0")["referrer"], &text); err != nil ||
		len([]rune(text)) != projectionNameLimit {
		t.Errorf("referrer = %d runes (%v), want %d", len([]rune(text)), err, projectionNameLimit)
	}
}

func TestTrafficRefusesAnIncompleteAnswer(t *testing.T) {
	for _, tc := range trafficCases {
		_, core := cannedCore(t, map[bool]string{true: `{"views":[],"clones":[]}`, false: `[{"count":1}]`}[tc.body[0] == '{'],
			false, relationConfig)
		if _, err := invoke(t, core, tc.id, "repo", `{}`, false); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s incomplete = %v, want an invalid response", tc.id, err)
		}
	}
}
