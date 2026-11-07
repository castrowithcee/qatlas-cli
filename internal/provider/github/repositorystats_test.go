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

var statsTools = []struct {
	id, route, right, body string
}{
	{statsContributorsGet.ID, "/stats/contributors", "the contributor statistics of this repository",
		`[{"author":{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","type":"User","site_admin":false},` +
			`"total":3,"weeks":[{"w":1367712000,"a":6898,"d":77,"c":10}]}]`},
	{statsCommitActivityGet.ID, "/stats/commit_activity", "the commit activity of this repository",
		`[{"days":[0,3,26,20,39,1,0],"total":89,"week":1336280400}]`},
	{statsCodeFrequencyGet.ID, "/stats/code_frequency", "the code frequency of this repository",
		`[[1302998400,1124,-435]]`},
	{statsParticipationGet.ID, "/stats/participation", "the participation statistics of this repository",
		`{"all":[3,5,7],"owner":[0,1,2]}`},
	{statsPunchCardGet.ID, "/stats/punch_card", "the punch card of this repository", `[[0,0,5],[0,1,43]]`},
}

func TestStatsToolsReportEveryDocumentedMember(t *testing.T) {
	want := map[string]string{
		statsContributorsGet.ID: `{"repository":"octo-org/example","state":"ready","contributors":[{"author":{"login":"octocat","id":1,` +
			`"node_id":"MDQ6VXNlcjE=","type":"User","site_admin":false},"total":3,"weeks":[{"week":1367712000,` +
			`"additions":6898,"deletions":77,"commits":10}]}]}`,
		statsCommitActivityGet.ID: `{"repository":"octo-org/example","state":"ready","weeks":[{"week":1336280400,"total":89,"days":[0,3,26,20,39,1,0]}]}`,
		statsCodeFrequencyGet.ID:  `{"repository":"octo-org/example","state":"ready","weeks":[{"week":1302998400,"additions":1124,"deletions":-435}]}`,
		statsParticipationGet.ID:  `{"repository":"octo-org/example","state":"ready","all":[3,5,7],"owner":[0,1,2]}`,
		statsPunchCardGet.ID: `{"repository":"octo-org/example","state":"ready","entries":[{"day":0,"hour":0,"commits":5},` +
			`{"day":0,"hour":1,"commits":43}]}`,
	}
	for _, tt := range statsTools {
		f, core := cannedCore(t, tt.body, false, relationConfig)
		got, err := invoke(t, core, tt.id, "repo", `{}`, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		coversSchema(t, tt.id, got)
		sameJSON(t, tt.id, got, want[tt.id])
		if request := lastRequest(t, f); request.method != http.MethodGet || request.path != detailRoute+tt.route ||
			request.query != "" {
			t.Errorf("%s request = %+v, want the fixed route", tt.id, request)
		}
	}
}

func TestStatsToolsReportComputingAndEmptyWithoutRetry(t *testing.T) {
	for _, tool := range statsTools {
		for _, tt := range []struct {
			status      int
			body, state string
		}{
			{http.StatusAccepted, ``, stateComputing}, {http.StatusAccepted, `{}`, stateComputing},
			{http.StatusNoContent, ``, stateEmpty},
		} {
			f := &fakeGitHub{failure: func(w http.ResponseWriter, _ *http.Request) bool {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
				return true
			}}
			red := &redact.Redactor{}
			core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
			got, err := invoke(t, core, tool.id, "repo", `{}`, false)
			if err != nil {
				t.Fatalf("%s %d %q: %v", tool.id, tt.status, tt.body, err)
			}
			coversSchema(t, tool.id, got)
			sameJSON(t, tool.id+" state", members(t, got)["state"], `"`+tt.state+`"`)
			if n := len(f.recorded()); n != 1 {
				t.Errorf("%s status %d caused %d requests, want one", tool.id, tt.status, n)
			}
		}
	}
}

func TestStatsContributorsBoundsWeeksAndContributorsAndCleansNames(t *testing.T) {
	var body strings.Builder
	body.WriteString(`[{"author":{"login":"bad\u0007login","id":1},"total":1,"weeks":[]}`)
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&body, `,{"author":null,"total":%d,"weeks":[`, i+2)
		for w := 0; w < 60; w++ {
			if w > 0 {
				body.WriteString(",")
			}
			fmt.Fprintf(&body, `{"w":%d,"a":1,"d":2,"c":3}`, w)
		}
		body.WriteString(`]}`)
	}
	body.WriteString(`]`)
	_, core := cannedCore(t, body.String(), false, relationConfig)
	got, err := invoke(t, core, statsContributorsGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, statsContributorsGet.ID, got)
	var result ContributorStats
	if json.Unmarshal(got, &result) != nil || len(result.Contributors) != 100 || !result.ContributorsTruncated {
		t.Fatalf("contributors = %d, truncated %v, want 100 and the cut visible", len(result.Contributors),
			result.ContributorsTruncated)
	}
	first := result.Contributors[0]
	if first.Total != 121 || len(first.Weeks) != 52 || !first.WeeksTruncated || first.Weeks[51].Week != 59 ||
		first.Weeks[0].Week != 8 || first.Author != nil {
		t.Errorf("first contributor = %+v, want the most active with the 52 most recent weeks", first)
	}

	_, core = cannedCore(t, `[{"author":{"login":"bad\u0007login","id":1},"total":1,"weeks":[]}]`, false, relationConfig)
	got, err = invoke(t, core, statsContributorsGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"bad login"`) {
		t.Errorf("login not cleaned: %s", got)
	}
}

func TestStatsToolsBoundTheirLists(t *testing.T) {
	week := func(n int) string {
		var b strings.Builder
		b.WriteString(`[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `[%d,1,-1]`, i)
		}
		return b.String() + `]`
	}
	_, core := cannedCore(t, week(300), false, relationConfig)
	got, err := invoke(t, core, statsCodeFrequencyGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var frequency CodeFrequency
	if json.Unmarshal(got, &frequency) != nil || len(frequency.Weeks) != 260 || !frequency.WeeksTruncated ||
		frequency.Weeks[259].Week != 299 {
		t.Errorf("code frequency = %d weeks, truncated %v", len(frequency.Weeks), frequency.WeeksTruncated)
	}
	_, core = cannedCore(t, week(200), false, relationConfig)
	got, err = invoke(t, core, statsPunchCardGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var card PunchCard
	if json.Unmarshal(got, &card) != nil || len(card.Entries) != 168 || !card.EntriesTruncated {
		t.Errorf("punch card = %d entries, truncated %v", len(card.Entries), card.EntriesTruncated)
	}
	long := strings.TrimSuffix(strings.Repeat("1,", 60), ",")
	_, core = cannedCore(t, `{"all":[`+long+`],"owner":[1]}`, false, relationConfig)
	got, err = invoke(t, core, statsParticipationGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var participation Participation
	if json.Unmarshal(got, &participation) != nil || len(participation.All) != 52 || !participation.AllTruncated ||
		participation.OwnerTruncated {
		t.Errorf("participation = %+v", participation)
	}
	weeks := `[` + strings.TrimSuffix(strings.Repeat(`{"days":[0,0,0,0,0,0,0],"total":1,"week":5},`, 60), ",") + `]`
	_, core = cannedCore(t, weeks, false, relationConfig)
	got, err = invoke(t, core, statsCommitActivityGet.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var activity CommitActivity
	if json.Unmarshal(got, &activity) != nil || len(activity.Weeks) != 52 || !activity.WeeksTruncated {
		t.Errorf("commit activity = %d weeks, truncated %v", len(activity.Weeks), activity.WeeksTruncated)
	}
}

func TestStatsToolsRefuseMalformedEntries(t *testing.T) {
	for id, body := range map[string]string{
		statsContributorsGet.ID:   `[{"author":null}]`,
		statsCommitActivityGet.ID: `[{"days":[1],"total":1,"week":1}]`,
		statsCodeFrequencyGet.ID:  `[[1,2]]`,
		statsParticipationGet.ID:  `{"all":[1]}`,
		statsPunchCardGet.ID:      `[[1]]`,
	} {
		_, core := cannedCore(t, body, false, relationConfig)
		if _, err := invoke(t, core, id, "repo", `{}`, false); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s malformed entry = %v, want an invalid response", id, err)
		}
	}
}

const activityBody = `[{"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","before":"6dcb09b5b57875f334f61aebed695e2e4193db5e",` +
	`"after":"827efc6d56897b048c772eb4087f854f46256132","ref":"refs/heads/main\u0007x",` +
	`"timestamp":"2011-01-26T19:06:43Z","activity_type":"force_push","actor":{"login":"octocat","id":1,` +
	`"node_id":"MDQ6VXNlcjE=","type":"User","site_admin":false,"html_url":"https://github.com/octocat"}},` +
	`{"id":2,"activity_type":"push","actor":null}]`

func TestActivityListReportsEveryMemberFiltersAndPages(t *testing.T) {
	f := &fakeGitHub{failure: func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			w.Header().Set("Link", `<https://api.github.com/repos/octo-org/example/activity?per_page=2&after=Y3Vyc29y_1=>; rel="next"`)
		}
		fmt.Fprint(w, activityBody)
		return true
	}}
	red := &redact.Redactor{}
	core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
	arguments := `{"ref":"main","actor":"octocat","time_period":"week","activity_type":"force_push",` +
		`"direction":"asc","limit":2}`
	got, err := invoke(t, core, activityList.ID, "repo", arguments, false)
	if err != nil {
		t.Fatal(err)
	}
	coversSchema(t, activityList.ID, got)
	request := lastRequest(t, f)
	query, _ := url.ParseQuery(request.query)
	if request.path != detailRoute+"/activity" || query.Get("ref") != "main" || query.Get("actor") != "octocat" ||
		query.Get("time_period") != "week" || query.Get("activity_type") != "force_push" ||
		query.Get("direction") != "asc" || query.Get("per_page") != "2" || query.Has("after") {
		t.Errorf("request = %+v, want the fixed route with every filter", request)
	}
	var list ActivityList
	if json.Unmarshal(got, &list) != nil || len(list.Activities) != 2 || !list.HasMore || list.NextCursor == "" {
		t.Fatalf("answer = %s", got)
	}
	first := list.Activities[0]
	if first.ID != 1296269 || valueOr(first.Ref) != "refs/heads/main x" || valueOr(first.Before) == "" ||
		valueOr(first.After) == "" || valueOr(first.Timestamp) != "2011-01-26T19:06:43Z" ||
		first.ActivityType != "force_push" || valueOr(first.NodeID) == "" || valueOr(first.Actor.Login) != "octocat" ||
		list.Activities[1].Actor != nil {
		t.Errorf("activities = %+v", list.Activities)
	}

	got, err = invoke(t, core, activityList.ID, "repo", `{"cursor":"`+list.NextCursor+`","ref":"main",`+
		`"actor":"octocat","time_period":"week","activity_type":"force_push","direction":"asc"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	query, _ = url.ParseQuery(lastRequest(t, f).query)
	if query.Get("after") != "Y3Vyc29y_1=" || query.Get("per_page") != "2" || query.Get("direction") != "asc" {
		t.Errorf("continuation query = %v, want the cursor and the same batch size and filters", query)
	}
	if strings.Contains(string(got), "next_cursor") {
		t.Errorf("last page offers a cursor: %s", got)
	}
	before := len(f.recorded())
	if _, err := invoke(t, core, activityList.ID, "repo", `{"cursor":"`+list.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("a cursor of other filters = %v, want an invalid request", err)
	}
	if len(f.recorded()) != before {
		t.Errorf("a refused cursor reached GitHub")
	}
}

func TestActivityListRefusesBadArgumentsBeforeIO(t *testing.T) {
	f, core, reads := guardedCore(t)
	for _, arguments := range []string{`{"time_period":"decade"}`, `{"activity_type":"merge"}`,
		`{"direction":"up"}`, `{"actor":"bad actor"}`, `{"actor":"a/b"}`, `{"ref":"a..b"}`, `{"ref":"x?y=1"}`,
		`{"limit":0}`, `{"limit":101}`, `{"cursor":"AAAA"}`, `{"extra":1}`} {
		before := len(f.recorded())
		if _, err := invoke(t, core, activityList.ID, "repo", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", arguments)
		}
	}
}

func TestStatsAndActivityReportNotFoundAndPermissionRefusals(t *testing.T) {
	tools := append([]struct{ id, route, right, body string }{}, statsTools...)
	tools = append(tools, struct{ id, route, right, body string }{activityList.ID, "/activity",
		"the activity feed of this repository", ""})
	for _, tt := range tools {
		for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
			f := &fakeGitHub{failure: func(w http.ResponseWriter, _ *http.Request) bool {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"message":"refused"}`)
				return true
			}}
			red := &redact.Redactor{}
			core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
			_, err := invoke(t, core, tt.id, "repo", `{}`, false)
			want := provider.ClassNotFound
			if status == http.StatusForbidden {
				want = provider.ClassPermission
				if err == nil || !strings.Contains(err.Error(), tt.right) {
					t.Errorf("%s 403 = %v, want the right it needs naming %q", tt.id, err, tt.right)
				}
			}
			if classOf(err) != want {
				t.Errorf("%s status %d = %v, want %s", tt.id, status, err, want)
			}
			if status == http.StatusNotFound && (err == nil || !strings.Contains(err.Error(), tt.right)) {
				t.Errorf("%s 404 = %v, want it to name %q", tt.id, err, tt.right)
			}
			if got := lastRequest(t, f).path; got != detailRoute+tt.route {
				t.Errorf("%s path = %q", tt.id, got)
			}
		}
	}
}

func TestStatsAndActivityRefuseForeignAndExcludedRepositoriesBeforeIO(t *testing.T) {
	f, core, reads := guardedCore(t)
	ids := []string{activityList.ID}
	for _, tool := range statsTools {
		ids = append(ids, tool.id)
	}
	for _, id := range ids {
		for _, tt := range []struct{ name, connection, arguments string }{
			{"foreign repository", "repo", `{"repository":"other/secret"}`},
			{"excluded repository", "repoexcluded", `{"repository":"octo-org/example"}`},
			{"malformed repository", "repo", `{"repository":"octo-org"}`},
		} {
			*reads = 0
			before := len(f.recorded())
			_, err := invoke(t, core, id, tt.connection, tt.arguments, false)
			if !isInvalidRequest(err) {
				t.Errorf("%s %s: %v, want an invalid request", id, tt.name, err)
			}
			if *reads != 0 || len(f.recorded()) != before {
				t.Errorf("%s %s reached the credential or GitHub", id, tt.name)
			}
		}
	}
}

func TestActivityListRefusesANextPageItCannotContinue(t *testing.T) {
	for _, link := range []string{
		`<https://api.github.com/repos/octo-org/example/activity?per_page=2&after=not%20a%20cursor>; rel="next"`,
		`<https://api.github.com/repos/octo-org/example/activity?per_page=2&page=2>; rel="next"`,
	} {
		f := &fakeGitHub{failure: func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Link", link)
			fmt.Fprint(w, activityBody)
			return true
		}}
		red := &redact.Redactor{}
		core := application.New(registry(t), relationConfig(serve(t, f)), resolver(red, nil), red)
		if got, err := invoke(t, core, activityList.ID, "repo", `{"limit":2}`, false); err == nil {
			t.Errorf("link %q: answer = %s, want an error instead of a feed that looks complete", link, got)
		}
	}
}
