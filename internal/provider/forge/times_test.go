package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const (
	widgetTimes   = "/api/v1/repos/acme/widget/issues/7/times"
	widgetStopper = "/api/v1/repos/acme/widget/issues/7/stopwatch/"
)

// timeEntry is one tracked time of the issue of a repository.
func timeEntry(id int, repository string) string {
	return `{"id":` + itoa(id) + `,"created":"2030-01-02T03:04:05Z","time":3600,"user_name":"alice",` +
		`"user_id":3,"issue_id":1007,"issue":{"id":1007,"number":7,"title":"Crash","repository":` +
		`{"id":5,"name":"widget","owner":"acme","full_name":"` + repository + `"}}}`
}

func timesServer(p *product, change func(call) *http.Response) func(call) *http.Response {
	return issueServer(p, "open", func(c call) *http.Response {
		if c.path == widgetTimes && c.method == http.MethodGet {
			return jsonResponse(200, `[`+timeEntry(11, "acme/widget")+`,`+timeEntry(12, "acme/widget")+`]`,
				"X-Total-Count", "2")
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	})
}

func TestIssueTimesListFiltersAndPages(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, timesServer(p, nil))
		result, err := e.invoke(t, issueTimesList, "repo",
			`{"number":7,"user":"alice","since":"2030-01-01T00:00:00Z","before":"2031-01-01T00:00:00Z","limit":2}`)
		times, _ := result["times"].([]any)
		if err != nil || len(times) != 2 || result["has_more"] != false {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		first := times[0].(map[string]any)
		if first["id"] != float64(11) || first["time"] != float64(3600) || first["user_name"] != "alice" ||
			first["issue"] == nil {
			t.Errorf("%s first = %v", p.id, first)
		}
		for _, hidden := range []string{"user_id", "issue_id"} {
			if _, ok := first[hidden]; ok {
				t.Errorf("%s shows the deprecated member %s", p.id, hidden)
			}
		}
		c := (*e.calls)[0]
		for _, want := range []string{"user=alice", "since=2030-01-01T00%3A00%3A00Z", "limit=2", "page=1"} {
			if !strings.Contains(c.query, want) {
				t.Errorf("%s query %q lacks %s", p.id, c.query, want)
			}
		}
		for _, bad := range []string{`{"number":7,"user":"a/b"}`, `{"number":7,"since":"yesterday"}`, `{}`,
			`{"number":7,"repository":"secret-owner/hidden"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, issueTimesList, "repo", bad); !isInvalidRequest(err) ||
				strings.Contains(err.Error(), "secret-owner") || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestIssueTimesAddBooksOneEntry(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, timesServer(p, func(c call) *http.Response {
			if c.method == http.MethodPost && c.path == widgetTimes {
				return jsonResponse(200, timeEntry(13, "acme/widget"))
			}
			return notFoundResponse()
		}))
		arguments := `{"number":7,"time":1800,"created":"2030-01-02T03:04:05Z","user_name":"bob"}`
		if _, err := e.invokeAs(t, issueTimesAdd, "repo", arguments, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed add: %v", p.id, err)
		}
		result, err := e.invoke(t, issueTimesAdd, "repo", arguments)
		if err != nil || result["id"] != float64(13) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := []string{"GET /api/v1/repos/acme/widget/issues/7", "POST " + widgetTimes}
		if got := methods(e); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s calls = %v", p.id, got)
		}
		if sent := bodyOf(t, (*e.calls)[1]); !reflect.DeepEqual(sent, map[string]any{"time": float64(1800),
			"created": "2030-01-02T03:04:05Z", "user_name": "bob"}) {
			t.Errorf("%s sent %v", p.id, sent)
		}
		for _, bad := range []string{`{"number":7}`, `{"number":7,"time":0}`, `{"number":7,"time":1.5}`,
			`{"number":7,"time":5,"created":"soon"}`, `{"number":7,"time":5,"user_name":"a b"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, issueTimesAdd, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
		// A pull request index is accepted; an index whose canonical repository lies outside is refused.
		if _, err := e.invoke(t, issueTimesAdd, "repo", `{"number":8,"time":5}`); err == nil ||
			classOfError(err) != "not-found" {
			// issue 8 has no times endpoint in the fixture: the POST is a 404, after the evidence read passed
			t.Errorf("%s: pull request index: %v", p.id, err)
		}
		before := len(*e.calls)
		_, err = e.invoke(t, issueTimesAdd, "repo", `{"number":9,"time":5}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "other/place") || len(*e.calls) != before+1 {
			t.Errorf("%s: foreign index: %v, calls %v", p.id, err, methods(e))
		}
	}
}

func TestIssueTimesDisabledIsUnavailable(t *testing.T) {
	for _, p := range products {
		answer := func(c call) *http.Response {
			if strings.HasSuffix(c.path, "/times") || strings.Contains(c.path, "/times/") {
				return jsonResponse(400, `{"message":"`+bodyCanary+`"}`)
			}
			return notFoundResponse()
		}
		e := newEnvironment(t, p, issueServer(p, "open", answer))
		for name, run := range map[string]func() error{
			"list": func() error { _, err := e.invoke(t, issueTimesList, "repo", `{"number":7}`); return err },
			"add":  func() error { _, err := e.invoke(t, issueTimesAdd, "repo", `{"number":7,"time":5}`); return err },
			"repository": func() error {
				_, err := e.invoke(t, repositoryTimesList, "repo", `{}`)
				return err
			},
			"user": func() error { _, err := e.invoke(t, userTimesList, "repo", `{"user":"alice"}`); return err },
			"reset": func() error {
				_, err := e.invoke(t, issueTimesReset, "times-delete", `{"number":7}`)
				return err
			},
		} {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "not available") || strings.Contains(err.Error(), "may have been applied") {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
		}
	}
}

func TestIssueTimesDeleteAndResetAreListedOnly(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{issueTimesDelete, issueTimesReset} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Fatalf("%s descriptor = %+v", p.toolID(tl), d)
			}
		}
		for _, other := range reg.Provider(p.id) {
			if other.RequiresToolAllowList && !strings.Contains(other.ID, ".issue-times.") &&
				strings.Contains(other.ID, "times") {
				t.Errorf("%s is listed-only", other.ID)
			}
		}
		for _, id := range []string{"issue-times.add", "issue-stopwatches.stop", "issue-stopwatches.delete"} {
			if d, _, _ := reg.Lookup(p.id + "." + id); d.RequiresToolAllowList {
				t.Errorf("%s is listed-only", d.ID)
			}
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if strings.Contains(id, "times") || strings.Contains(id, "stopwatch") {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
				}
			}
		}
		e := newEnvironment(t, p, timesServer(p, func(c call) *http.Response {
			if c.method == http.MethodDelete {
				return jsonResponse(204, "")
			}
			return notFoundResponse()
		}))
		// Without a tools list naming them, neither tool is offered.
		for _, run := range []func() error{
			func() error { _, err := e.invoke(t, issueTimesDelete, "repo", `{"number":7,"id":11}`); return err },
			func() error { _, err := e.invoke(t, issueTimesReset, "repo", `{"number":7}`); return err },
		} {
			if err := run(); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s: offered without a tools list: %v", p.id, err)
			}
		}
		if _, err := e.invokeAs(t, issueTimesReset, "times-delete", `{"number":7}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed reset: %v", p.id, err)
		}
		result, err := e.invoke(t, issueTimesReset, "times-delete", `{"number":7}`)
		if err != nil || result["reset"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := []string{"GET /api/v1/repos/acme/widget/issues/7", "DELETE " + widgetTimes}
		if got := methods(e); !reflect.DeepEqual(got, want) {
			t.Errorf("%s reset calls = %v", p.id, got)
		}
	}
}

func TestIssueTimesDeleteProvesTheEntryBelongsToTheIssue(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, timesServer(p, func(c call) *http.Response {
			if c.method == http.MethodDelete {
				return jsonResponse(204, "")
			}
			return notFoundResponse()
		}))
		result, err := e.invoke(t, issueTimesDelete, "times-delete", `{"number":7,"id":12}`)
		if err != nil || result["deleted"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := []string{"GET /api/v1/repos/acme/widget/issues/7", "GET " + widgetTimes,
			"DELETE " + widgetTimes + "/12"}
		if got := methods(e); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s calls = %v", p.id, got)
		}
		// An ID the issue does not list is refused before the delete.
		before := len(*e.calls)
		_, err = e.invoke(t, issueTimesDelete, "times-delete", `{"number":7,"id":99}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "99") {
			t.Errorf("%s: foreign entry: %v", p.id, err)
		}
		for _, c := range (*e.calls)[before:] {
			if c.method != http.MethodGet {
				t.Errorf("%s sent %s %s for a foreign entry", p.id, c.method, c.path)
			}
		}
	}
}

func TestStopwatchesChangeExactlyOnce(t *testing.T) {
	for _, p := range products {
		for _, tc := range []struct {
			tool    *tool
			method  string
			action  string
			status  int
			resultK string
		}{{issueStopwatchesStart, http.MethodPost, "start", 201, "started"},
			{issueStopwatchesStop, http.MethodPost, "stop", 201, "stopped"},
			{issueStopwatchesDelete, http.MethodDelete, "delete", 204, "discarded"}} {
			e := newEnvironment(t, p, timesServer(p, func(c call) *http.Response {
				if c.method == tc.method && c.path == widgetStopper+tc.action {
					return jsonResponse(tc.status, "")
				}
				return notFoundResponse()
			}))
			if _, err := e.invokeAs(t, tc.tool, "repo", `{"number":7}`, false); err == nil || len(*e.calls) != 0 {
				t.Fatalf("%s %s: unconfirmed: %v", p.id, tc.action, err)
			}
			result, err := e.invoke(t, tc.tool, "repo", `{"number":7}`)
			if err != nil || result[tc.resultK] != true {
				t.Fatalf("%s %s: %v, %v", p.id, tc.action, result, err)
			}
			want := []string{"GET /api/v1/repos/acme/widget/issues/7", tc.method + " " + widgetStopper + tc.action}
			if got := methods(e); !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s calls = %v", p.id, tc.action, got)
			}
			// A pull request index passes the evidence read; an index of another repository does not.
			before := len(*e.calls)
			if _, err := e.invoke(t, tc.tool, "repo", `{"number":9}`); !isInvalidRequest(err) ||
				len(*e.calls) != before+1 {
				t.Errorf("%s %s: foreign index: %v", p.id, tc.action, err)
			}
		}
	}
}

func TestStopwatchDescriptionsSayWhatHappensToTheTime(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		stop, _, _ := reg.Lookup(p.toolID(issueStopwatchesStop))
		drop, _, _ := reg.Lookup(p.toolID(issueStopwatchesDelete))
		if !strings.Contains(stop.Description, "book the elapsed time") ||
			!strings.Contains(drop.Description, "without booking any time") {
			t.Errorf("%s: %q, %q", p.id, stop.Description, drop.Description)
		}
	}
}

func TestStopwatchChangeIsNeverRetried(t *testing.T) {
	for _, p := range products {
		for _, status := range []int{500, 503} {
			posts := 0
			e := newEnvironment(t, p, timesServer(p, func(c call) *http.Response {
				if c.method == http.MethodPost {
					posts++
					return jsonResponse(status, `{"message":"`+bodyCanary+`"}`)
				}
				return notFoundResponse()
			}))
			_, err := e.invoke(t, issueStopwatchesStop, "repo", `{"number":7}`)
			if err == nil || posts != 1 || !strings.Contains(err.Error(), "may have been applied") {
				t.Errorf("%s %d: %v, posts %d", p.id, status, err, posts)
			}
		}
	}
}

func TestRepositoryAndUserTimes(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			switch c.path {
			case "/api/v1/repos/acme/widget/times":
				return jsonResponse(200, `[`+timeEntry(11, "acme/widget")+`,`+timeEntry(12, "other/place")+`,`+
					`{"id":14,"time":5}]`, "Link", `<https://forge.example.test/api/v1/x?page=2>; rel="next"`)
			case "/api/v1/repos/acme/widget/times/alice":
				return jsonResponse(200, `[`+timeEntry(11, "acme/widget")+`]`)
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, repositoryTimesList, "repo", `{"user":"alice","since":"2030-01-01T00:00:00Z"}`)
		times, _ := result["times"].([]any)
		// Rows that name no repository or another one are dropped, although the path names the first.
		if err != nil || len(times) != 1 || result["has_more"] != true || result["next_cursor"] == "" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if !strings.Contains((*e.calls)[0].query, "user=alice") {
			t.Errorf("%s query = %q", p.id, (*e.calls)[0].query)
		}
		result, err = e.invoke(t, userTimesList, "repo", `{"user":"alice"}`)
		if times, _ := result["times"].([]any); err != nil || len(times) != 1 {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := (*e.calls)[1]; got.query != "" || got.path != "/api/v1/repos/acme/widget/times/alice" {
			t.Errorf("%s user request = %+v", p.id, got)
		}
		for _, bad := range []string{`{}`, `{"user":"a/b"}`, `{"user":"alice","repository":"secret-owner/hidden"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, userTimesList, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestMyTimesAndStopwatchesKeepRowsInsideTheTargets(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			switch c.path {
			case "/api/v1/user/times":
				return jsonResponse(200, `[`+timeEntry(11, "acme/widget")+`,`+timeEntry(12, "other/place")+`,`+
					`{"id":13,"time":5},`+timeEntry(14, "ACME/Widget")+`]`, "X-Total-Count", "4")
			case "/api/v1/user/stopwatches":
				return jsonResponse(200, `[{"created":"2030-01-02T03:04:05Z","duration":"1h","seconds":3600,`+
					`"issue_index":7,"issue_title":"Crash","repo_owner_name":"acme","repo_name":"widget"},`+
					`{"seconds":5,"issue_index":1,"repo_owner_name":"other","repo_name":"place"},{"seconds":6}]`)
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, myTimesList, "repo", `{"since":"2030-01-01T00:00:00Z","limit":10}`)
		times, _ := result["times"].([]any)
		if err != nil || len(times) != 2 || result["has_more"] != false {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if q := (*e.calls)[0].query; !strings.Contains(q, "since=") || !strings.Contains(q, "limit=10") ||
			strings.Contains(q, "user=") {
			t.Errorf("%s query = %q", p.id, q)
		}
		result, err = e.invoke(t, myStopwatchesList, "repo", `{}`)
		watches, _ := result["stopwatches"].([]any)
		if err != nil || len(watches) != 1 || watches[0].(map[string]any)["issue_title"] != "Crash" ||
			watches[0].(map[string]any)["duration"] != "1h" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		// Without targets everything the token reaches stays.
		result, err = e.invoke(t, myStopwatchesList, "open", `{}`)
		if watches, _ := result["stopwatches"].([]any); err != nil || len(watches) != 3 {
			t.Errorf("%s: open connection: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, myTimesList, "owner", `{}`)
		if times, _ := result["times"].([]any); err != nil || len(times) != 0 {
			t.Errorf("%s: owner connection: %v, %v", p.id, result, err)
		}
	}
}

func TestTimeToolsRefuseOutsideTargetsBeforeTheToken(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, timesServer(p, nil))
		for _, tl := range []struct {
			tool *tool
			args string
		}{{issueTimesList, `{"repository":"secret-owner/hidden","number":7}`},
			{issueTimesAdd, `{"repository":"secret-owner/hidden","number":7,"time":5}`},
			{issueStopwatchesStart, `{"repository":"secret-owner/hidden","number":7}`},
			{repositoryTimesList, `{"repository":"secret-owner/hidden"}`},
			{userTimesList, `{"repository":"secret-owner/hidden","user":"alice"}`}} {
			if _, err := e.invoke(t, tl.tool, "repo", tl.args); !isInvalidRequest(err) ||
				strings.Contains(err.Error(), "secret-owner") {
				t.Errorf("%s %s: %v", p.id, tl.tool.action, err)
			}
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: %d requests, %d token reads", p.id, len(*e.calls), *e.reads)
		}
	}
}

func TestExclusionsKeepExcludedRowsOutOfMyTimesAndStopwatches(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			switch c.path {
			case "/api/v1/user/times":
				return jsonResponse(200, `[`+timeEntry(11, "acme/widget")+`,`+timeEntry(12, "other/place")+`,`+
					`{"id":13,"time":5},`+timeEntry(14, "ACME/Gadget")+`,`+timeEntry(15, "elsewhere/x")+`]`)
			case "/api/v1/user/stopwatches":
				return jsonResponse(200, `[{"seconds":1,"issue_index":7,"repo_owner_name":"acme","repo_name":"widget"},`+
					`{"seconds":5,"issue_index":1,"repo_owner_name":"Other","repo_name":"place"},`+
					`{"seconds":7,"issue_index":2,"repo_owner_name":"acme","repo_name":"gadget"},{"seconds":6}]`)
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, myTimesList, "exclude", `{}`)
		if times := rowsOf(t, result, "times"); err != nil || len(times) != 2 {
			t.Errorf("%s times: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, myStopwatchesList, "exclude", `{}`)
		if watches := rowsOf(t, result, "stopwatches"); err != nil || len(watches) != 1 {
			t.Errorf("%s stopwatches: %v, %v", p.id, result, err)
		}
		// A change on an excluded repository is refused before any request.
		*e.calls = nil
		reads := *e.reads
		for _, c := range []struct {
			tl   *tool
			args string
		}{{issueTimesList, `{"repository":"other/place","number":7}`},
			{issueTimesAdd, `{"repository":"acme/gadget","number":7,"time":5}`},
			{repositoryTimesList, `{"repository":"Acme/Gadget"}`}} {
			if _, err := e.invoke(t, c.tl, "exclude", c.args); !isInvalidRequest(err) || len(*e.calls) != 0 ||
				*e.reads != reads {
				t.Errorf("%s %s: %v, calls %v", p.id, c.tl.action, err, e.paths())
			}
		}
	}
}
