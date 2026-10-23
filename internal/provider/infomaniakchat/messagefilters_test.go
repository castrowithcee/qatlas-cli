package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func changedPost(id, channel string, updateAt, deleteAt int64) string {
	post := postJSONOf(id, channel, "", "user1", messageCanary, 1735689600000)
	return strings.Replace(post, `"type":""}`, `"type":"","update_at":`+strconv.FormatInt(updateAt, 10)+
		`,"delete_at":`+strconv.FormatInt(deleteAt, 10)+`}`, 1)
}

func postsServer(t *testing.T, answer string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/channels/" + chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case "/api/v4/channels/" + chanA + "/posts":
			return jsonResponse(200, answer), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}
}

func TestMessagesListSinceOrdersByChangeAndCutsToLimit(t *testing.T) {
	ids := []string{"postc00000000000000000003", "posta00000000000000000001", "postd00000000000000000004",
		"postb00000000000000000002", "poste00000000000000000005", "postf00000000000000000006"}
	posts := map[string]string{
		ids[0]: changedPost(ids[0], chanA, 1735689603000, 0),
		ids[1]: changedPost(ids[1], chanA, 1735689601000, 0),
		ids[2]: changedPost(ids[2], chanA, 1735689604000, 0),
		ids[3]: changedPost(ids[3], chanA, 1735689602000, 0),
		ids[4]: changedPost(ids[4], chanA, 1735689600500, 1735689700000),
		ids[5]: changedPost(ids[5], chanB, 1735689600600, 0),
	}
	var values []string
	for _, id := range ids {
		values = append(values, posts[id])
	}
	var calls []call
	env := newEnvironment(t, &calls, postsServer(t, postListJSONOf(ids, values, false, "")))
	result, err := env.invoke(messagesList.ID, "team",
		`{"channel_id":"`+chanA+`","since":"2025-01-01T00:00:00+01:00","limit":3}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page MessagesPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 3 || !page.HasMore || page.Page != 1 || len(page.Messages) != 3 {
		t.Fatalf("page = %+v, want 3 entries and has_more", page)
	}
	for i, want := range []string{ids[1], ids[3], ids[0]} {
		if page.Messages[i].ID != want {
			t.Fatalf("messages = %+v, want updated_at ascending %s", page.Messages, want)
		}
	}
	if strings.Contains(result, ids[4]) || strings.Contains(result, ids[5]) {
		t.Fatalf("result holds a deleted or foreign post: %s", result)
	}
	query := calls[1].query
	since := time.Date(2024, 12, 31, 23, 0, 0, 0, time.UTC).UnixMilli()
	if len(calls) != 2 || query.Get("since") != strconv.FormatInt(since, 10) || len(query) != 1 {
		t.Fatalf("calls = %+v, want one scope check and a read with since only", calls)
	}

	// Exactly limit changes leave nothing more.
	env = newEnvironment(t, &calls, postsServer(t, postListJSONOf(ids[:2], values[:2], false, "")))
	result, err = env.invoke(messagesList.ID, "team", `{"channel_id":"`+chanA+`","since":"2025-01-01T00:00:00Z","limit":2}`)
	if err != nil || strings.Contains(result, `"has_more":true`) {
		t.Fatalf("result = %s, %v, want has_more false", result, err)
	}
}

func TestMessagesListBeforeAndAfterReadOnePageNextToAMessage(t *testing.T) {
	for _, c := range []struct {
		key     string
		hasNext bool
		count   int
		more    bool
	}{{"before", false, 2, true}, {"after", true, 1, true}, {"before", false, 3, false}} {
		var ids, values []string
		for i := 0; i < c.count; i++ {
			id := "postx0000000000000000000" + strconv.Itoa(i)
			ids = append(ids, id)
			values = append(values, changedPost(id, chanA, 1, 0))
		}
		// A post of another channel is dropped but still counts towards the full page.
		foreign := "postforeign0000000000000000"
		ids = append(ids, foreign)
		values = append(values, changedPost(foreign, chanB, 1, 0))
		var calls []call
		env := newEnvironment(t, &calls, postsServer(t, postListJSONOf(ids, values, c.hasNext, "")))
		limit := len(ids)
		if !c.more {
			limit = len(ids) + 1
		}
		result, err := env.invoke(messagesList.ID, "team",
			`{"channel_id":"`+chanA+`","`+c.key+`":"`+postInChanA+`","limit":`+strconv.Itoa(limit)+`}`)
		if err != nil {
			t.Fatal(err)
		}
		var page MessagesPage
		if err := json.Unmarshal([]byte(result), &page); err != nil || page.Page != 1 || page.Count != c.count ||
			page.HasMore != c.more || strings.Contains(result, foreign) {
			t.Fatalf("%s: result = %s, %v", c.key, result, err)
		}
		query := calls[1].query
		if query.Get(c.key) != postInChanA || query.Get("page") != "0" || query.Get("per_page") != strconv.Itoa(limit) ||
			query.Has("since") || len(query) != 3 {
			t.Fatalf("%s: query = %v", c.key, query)
		}
	}
}

func TestMessagesListRefusesEveryUnsoundSelectionBeforeSecretsAndRequests(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	values := map[string]string{"page": "2", "since": `"2025-01-01T00:00:00Z"`, "before": `"` + postInChanA + `"`,
		"after": `"` + postInChanA + `"`}
	keys := []string{"page", "since", "before", "after"}
	var arguments []string
	for i, a := range keys {
		for _, b := range keys[i+1:] {
			arguments = append(arguments, `"`+a+`":`+values[a]+`,"`+b+`":`+values[b])
		}
	}
	arguments = append(arguments,
		`"since":"`+future+`"`, `"since":"yesterday"`, `"since":"2025-01-01"`, `"since":"1969-12-31T00:00:00Z"`,
		`"before":"../x"`, `"after":"UPPER"`)
	for _, extra := range arguments {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		})
		_, err := env.invoke(messagesList.ID, "team", `{"channel_id":"`+chanA+`",`+extra+`}`)
		if err == nil || (!isInvalidRequest(err) && !strings.Contains(err.Error(), "invalid")) {
			t.Fatalf("%s: err = %v, want a refusal", extra, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: calls = %+v, secret reads = %d, want none", extra, calls, *env.reads)
		}
		if strings.Contains(err.Error(), "yesterday") || strings.Contains(err.Error(), future) {
			t.Fatalf("%s: error quotes the value: %v", extra, err)
		}
	}
}

func TestMessagesListWithoutNewArgumentsKeepsRequestAndOutput(t *testing.T) {
	post := changedPost(postInChanA, chanA, 0, 0)
	var calls []call
	env := newEnvironment(t, &calls, postsServer(t, postListJSONOf([]string{postInChanA}, []string{post}, true, "")))
	result, err := env.invoke(messagesList.ID, "team", `{"channel_id":"`+chanA+`","page":2,"limit":10}`)
	want := `{"channel_id":"` + chanA + `","messages":[{"channel_id":"` + chanA + `","created_at":"2025-01-01T00:00:00Z",` +
		`"id":"` + postInChanA + `","message":"` + messageCanary + `","type":"","user_id":"user1"}],"page":2,"has_more":true,"count":1}`
	if err != nil {
		t.Fatal(err)
	}
	var got, expected map[string]any
	if json.Unmarshal([]byte(result), &got) != nil || json.Unmarshal([]byte(want), &expected) != nil {
		t.Fatal("bad JSON")
	}
	if a, b := mustJSON(t, got), mustJSON(t, expected); a != b {
		t.Fatalf("result = %s, want %s", a, b)
	}
	wantQuery := url.Values{"page": {"1"}, "per_page": {"10"}}
	if calls[1].query.Encode() != wantQuery.Encode() {
		t.Fatalf("query = %v", calls[1].query)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSearchSendsTimeZoneOffsetOnlyWhenGiven(t *testing.T) {
	for _, tool := range []string{messagesSearch.ID, filesSearch.ID} {
		for _, c := range []struct{ extra, want string }{{``, ``}, {`,"time_zone_offset":0`, `0`},
			{`,"time_zone_offset":-43200`, `-43200`}, {`,"time_zone_offset":50400`, `50400`}} {
			var calls []call
			env := newEnvironment(t, &calls, searchServer(t, searchPaths()))
			if _, err := env.invoke(tool, "team", `{"team_id":"`+teamA+`","terms":"x"`+c.extra+`}`); err != nil {
				t.Fatal(err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal([]byte(calls[1].body), &body); err != nil {
				t.Fatal(err)
			}
			got, present := body["time_zone_offset"]
			if (c.want == "") == present || string(got) != c.want {
				t.Fatalf("%s %q: body = %s", tool, c.extra, calls[1].body)
			}
		}
		for _, bad := range []string{`-43201`, `50401`, `1.5`, `"UTC"`} {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			})
			if _, err := env.invoke(tool, "team", `{"team_id":"`+teamA+`","terms":"x","time_zone_offset":`+bad+`}`); err == nil {
				t.Fatalf("%s %s: want a refusal", tool, bad)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("%s %s: calls = %+v, reads = %d", tool, bad, calls, *env.reads)
			}
		}
	}
}

func TestMessagesSearchMatchesCoverOnlyReportedMessagesAndAreBounded(t *testing.T) {
	var terms []string
	for i := 0; i < 25; i++ {
		terms = append(terms, `"t`+strconv.Itoa(i)+`"`)
	}
	long := strings.Repeat("é", 200)
	answer := strings.Replace(postsAnswer(), `"matches":{}`, `"matches":{"`+hitInChanA+`":[`+strings.Join(terms, ",")+`],"`+
		hitInChanC+`":["a\u0000b","","`+long+`"],"`+hitInChanB+`":["dropped"],"`+hitInDM+`":["dropped"],"`+
		hitInChanP+`":[],"ghost":["x"]}`, 1)
	var calls []call
	env := newEnvironment(t, &calls, searchServer(t, map[string]string{"/api/v4/teams/" + teamA + "/posts/search": answer}))
	result, err := env.invoke(messagesSearch.ID, "team", `{"team_id":"`+teamA+`","terms":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out SearchedMessages
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 2 || len(out.Matches[hitInChanA]) != 20 || out.Matches[hitInChanA][19] != "t19" {
		t.Fatalf("matches = %v", out.Matches)
	}
	got := out.Matches[hitInChanC]
	if len(got) != 2 || got[0] != "ab" || len(got[1]) != 256 {
		t.Fatalf("matches of the bounded post = %q", got)
	}
	if strings.Contains(result, "dropped") || strings.Contains(result, "ghost") {
		t.Fatalf("result = %s, want matches of reported messages only", result)
	}

	// Without matches the output has no matches member.
	env = newEnvironment(t, &calls, searchServer(t, map[string]string{"/api/v4/teams/" + teamA + "/posts/search": postsAnswer()}))
	result, err = env.invoke(messagesSearch.ID, "team", `{"team_id":"`+teamA+`","terms":"x"}`)
	if err != nil || strings.Contains(result, "matches") {
		t.Fatalf("result = %s, %v", result, err)
	}
}

func TestThreadsListSendsSinceAndReportsTotal(t *testing.T) {
	list := `{"total":42,"threads":[` + threadJSONOf(postInChanC, chanC, "") + `,` + threadJSONOf(postInChanA, chanA, "") + `]}`
	var calls []call
	env := newEnvironment(t, &calls, threadServer(t, list, nil))
	result, err := env.invoke(threadsList.ID, "team", `{"team_id":"`+teamA+`","since":"2025-01-01T00:00:00Z"}`)
	if err != nil {
		t.Fatal(err)
	}
	var page ThreadsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Total == nil || *page.Total != 42 || page.Count != 2 {
		t.Fatalf("result = %s, %v", result, err)
	}
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/threads") {
			if c.query.Get("since") != strconv.FormatInt(since, 10) {
				t.Fatalf("query = %v", c.query)
			}
			return
		}
	}
	t.Fatalf("calls = %+v, want a threads read", calls)
}

func TestThreadsListWithoutSinceOrTotalKeepsRequestAndOutput(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, threadServer(t, `{"threads":[]}`, nil))
	result, err := env.invoke(threadsList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil || strings.Contains(result, "total") {
		t.Fatalf("result = %s, %v", result, err)
	}
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/threads") && c.query.Has("since") {
			t.Fatalf("query = %v, want no since", c.query)
		}
	}
	for _, bad := range []string{`"2099-01-01T00:00:00Z"`, `"soon"`} {
		calls = nil
		env := newEnvironment(t, &calls, threadServer(t, `{}`, nil))
		if _, err := env.invoke(threadsList.ID, "team", `{"team_id":"`+teamA+`","since":`+bad+`}`); !isInvalidRequest(err) ||
			len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d", bad, err, calls, *env.reads)
		}
	}
}

func TestNewFilterAndOutputMembersAreInSchemaAndFields(t *testing.T) {
	registry := registry(t)
	want := map[string][]string{
		messagesList.ID:   {"since", "before", "after"},
		messagesSearch.ID: {"time_zone_offset", "matches"},
		filesSearch.ID:    {"time_zone_offset"},
		threadsList.ID:    {"since", "total"},
	}
	for _, d := range registry.Provider(Provider) {
		for _, name := range want[d.ID] {
			var input, output map[string]any
			if json.Unmarshal(d.InputSchema, &input) != nil || json.Unmarshal(d.OutputSchema, &output) != nil {
				t.Fatal("bad schema")
			}
			_, inInput := input["properties"].(map[string]any)[name]
			_, inOutput := output["properties"].(map[string]any)[name]
			inFields, inArguments := false, false
			for _, f := range d.Fields {
				inFields = inFields || f.Name == name
			}
			for _, a := range d.Arguments {
				inArguments = inArguments || a.Name == name
			}
			if name == "matches" || name == "total" {
				if !inOutput || !inFields {
					t.Errorf("%s: %s is missing in OutputSchema or Fields", d.ID, name)
				}
			} else if !inInput || !inArguments {
				t.Errorf("%s: %s is missing in InputSchema or Arguments", d.ID, name)
			}
		}
	}
}
