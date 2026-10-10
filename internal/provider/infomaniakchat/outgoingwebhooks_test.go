package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	ohookA = "ohooka00000000000000000a1" // chanA of teamA
	ohookB = "ohookb00000000000000000b2" // chanB of teamB
	ohookC = "ohookc00000000000000000c3" // claims teamA, but chanB lives in teamB
	ohookD = "ohookd00000000000000000d4" // chanC of teamA
	ohookE = "ohooke00000000000000000e5" // deleted
	ohookN = "ohookn00000000000000000n6" // no channel, teamA
	ohookT = "ohookt00000000000000000t7" // teamB, no channel

	tokenCanary = "hook-token-canary-77aa"
	pathCanary  = "callback-path-canary-31"
	queryCanary = "callback-query-canary-42"
	userCanary  = "callback-user-canary-53"
)

var outgoingTools = []capability.Descriptor{outgoingWebhooksList, outgoingWebhooksGet, outgoingWebhooksUpdate,
	outgoingWebhooksDelete}

var webhookToolIDs = []string{incomingWebhooksList.ID, incomingWebhooksGet.ID, incomingWebhooksUpdate.ID,
	incomingWebhooksDelete.ID, outgoingWebhooksList.ID, outgoingWebhooksGet.ID, outgoingWebhooksUpdate.ID,
	outgoingWebhooksDelete.ID}

// primaryCallback carries canaries in every part a callback URL may hide a secret in.
var primaryCallback = (&url.URL{Scheme: "https", User: url.UserPassword(userCanary, "pw"),
	Host: "hooks.example.invalid:8443", Path: "/" + pathCanary, RawQuery: "k=" + queryCanary, Fragment: "frag"}).String()

func ohookWith(id, team, channel, extra string) string {
	return `{"id":"` + id + `","token":"` + tokenCanary + `","team_id":"` + team + `","channel_id":"` + channel + `",` +
		`"display_name":"Alerts","description":"old text","trigger_words":["deploy","ship it"],"trigger_when":1,` +
		`"callback_urls":["` + primaryCallback + `","http://plain.example.invalid/x","::not a url"],` +
		`"content_type":"application/json","username":"bot-name","icon_url":"https://example.invalid/i.png",` +
		`"create_at":1735689600000,"update_at":1735689700000,"delete_at":0,"creator_id":"u1"` + extra + `}`
}

type outgoingFixture struct {
	t       *testing.T
	hooks   map[string]string
	listing string
	mutate  func(*http.Request) (*http.Response, error)
}

func newOutgoingFixture(t *testing.T, mutate func(*http.Request) (*http.Response, error)) *outgoingFixture {
	if mutate == nil {
		mutate = func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}
	return &outgoingFixture{t: t, mutate: mutate, hooks: map[string]string{
		ohookA: ohookWith(ohookA, teamA, chanA, ""), ohookB: ohookWith(ohookB, teamB, chanB, ""),
		ohookC: ohookWith(ohookC, teamA, chanB, ""), ohookD: ohookWith(ohookD, teamA, chanC, ""),
		ohookE: ohookWith(ohookE, teamA, chanA, ""), ohookN: ohookWith(ohookN, teamA, "", ""),
		ohookT: ohookWith(ohookT, teamB, "", ""),
	}}
}

func (f *outgoingFixture) handle(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		return f.mutate(r)
	}
	switch r.URL.Path {
	case "/api/v4/hooks/outgoing":
		return jsonResponse(200, f.listing), nil
	case "/api/v4/users/me/teams/" + teamA + "/channels":
		return jsonResponse(200, "["+strings.Join([]string{channelWith(chanA, teamA, "O", ""),
			channelWith(chanC, teamA, "P", ""), channelWith(chanE, teamA, "O", archivedAt)}, ",")+"]"), nil
	}
	for id, body := range f.hooks {
		if r.URL.Path == "/api/v4/hooks/outgoing/"+id {
			if id == ohookE {
				body = strings.Replace(body, `"delete_at":0`, `"delete_at":1735689800000`, 1)
			}
			return jsonResponse(200, body), nil
		}
	}
	for id, body := range map[string]string{chanA: channelWith(chanA, teamA, "O", ""),
		chanB: channelWith(chanB, teamB, "O", ""), chanC: channelWith(chanC, teamA, "P", "")} {
		if r.URL.Path == "/api/v4/channels/"+id {
			return jsonResponse(200, body), nil
		}
	}
	f.t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	return nil, nil
}

func (f *outgoingFixture) env(calls *[]call) *environment {
	return newEnvironment(f.t, calls, f.handle)
}

// noSecrets fails the test when a result or error shows the token or any part of a callback URL beyond its
// origin.
func noSecrets(t *testing.T, what, text string) {
	t.Helper()
	for _, canary := range []string{tokenCanary, pathCanary, queryCanary, userCanary, "frag", "not a url"} {
		if strings.Contains(text, canary) {
			t.Fatalf("%s shows %q: %s", what, canary, text)
		}
	}
}

func TestOutgoingWebhookToolsNeedTheToolsList(t *testing.T) {
	for _, d := range outgoingTools {
		if !d.RequiresToolAllowList || d.Risk.DataSensitivity != integrationSensitivity || !d.Risk.OpenWorld ||
			withGroup(d).Group != "integrations" {
			t.Fatalf("%s = %+v", d.ID, d)
		}
	}
	for _, d := range outgoingTools[2:] {
		if d.Risk.Confirmation != capability.ConfirmationRequired || d.Risk.Idempotency != capability.IdempotencyIdempotent {
			t.Fatalf("%s risk = %+v", d.ID, d.Risk)
		}
	}
	if outgoingWebhooksDelete.Risk.Effect != capability.EffectDelete ||
		outgoingWebhooksUpdate.Risk.Effect != capability.EffectUpdate {
		t.Fatal("unexpected webhook effects")
	}
	var calls []call
	env := newOutgoingFixture(t, nil).env(&calls)
	for _, c := range []struct{ id, args string }{
		{outgoingWebhooksList.ID, `{"team_id":"` + teamA + `"}`},
		{outgoingWebhooksGet.ID, `{"hook_id":"` + ohookA + `"}`},
		{outgoingWebhooksUpdate.ID, `{"hook_id":"` + ohookA + `","description":"x"}`},
		{outgoingWebhooksDelete.ID, `{"hook_id":"` + ohookA + `"}`},
	} {
		if _, err := env.confirmed(c.id, "hookno", c.args); err == nil {
			t.Fatalf("%s was reachable without the tools list", c.id)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d", calls, *env.reads)
	}
}

func TestOutgoingWebhooksListFiltersAndShowsOnlyCallbackOrigins(t *testing.T) {
	f := newOutgoingFixture(t, nil)
	f.listing = "[" + strings.Join([]string{
		ohookWith(ohookA, teamA, chanA, ""),
		ohookWith(ohookD, teamA, chanC, ""),
		ohookWith(ohookN, teamA, "", ""),
		ohookWith(ohookB, teamB, chanB, ""),
		ohookWith(ohookC, teamA, chanB, ""),
		strings.Replace(ohookWith(ohookE, teamA, chanA, ""), `"delete_at":0`, `"delete_at":5`, 1),
		ohookWith(ohookT, teamB, "", ""),
		ohookWith("../bad", teamA, chanA, ""),
	}, ",") + "]"
	var calls []call
	env := f.env(&calls)

	result, err := env.invoke(outgoingWebhooksList.ID, "hooker", `{"team_id":"`+teamA+`","page":3,"per_page":9}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	noSecrets(t, "list", result)
	for _, id := range []string{ohookA, ohookD, ohookN} {
		if !strings.Contains(result, id) {
			t.Fatalf("result = %s, want %s", result, id)
		}
	}
	for _, id := range []string{ohookB, ohookC, ohookE, ohookT, "bad"} {
		if strings.Contains(result, id) {
			t.Fatalf("result = %s, want %s dropped", result, id)
		}
	}
	if !strings.Contains(result, `"callback_origins":["https://hooks.example.invalid:8443","http://plain.example.invalid"]`) ||
		!strings.Contains(result, `"count":3`) || !strings.Contains(result, `"has_more":false`) ||
		strings.Contains(result, "creator_id") || calls[0].query.Get("team_id") != teamA ||
		calls[0].query.Get("page") != "2" || calls[0].query.Get("per_page") != "9" ||
		calls[0].query.Has("channel_id") || len(calls) != 2 {
		t.Fatalf("result = %s, calls = %+v", result, calls)
	}

	// With a channel allow-list the channelless hook is dropped as well as chanC's.
	result, err = env.invoke(outgoingWebhooksList.ID, "hookch", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, ohookA) || strings.Contains(result, ohookD) ||
		strings.Contains(result, ohookN) {
		t.Fatalf("result = %s, %v", result, err)
	}

	// A channel argument is sent to kChat and narrows the result to that channel.
	calls = nil
	result, err = env.invoke(outgoingWebhooksList.ID, "hooker", `{"team_id":"`+teamA+`","channel_id":"`+chanA+`"}`)
	if err != nil || calls[0].query.Get("channel_id") != chanA || !strings.Contains(result, ohookA) ||
		strings.Contains(result, ohookD) || strings.Contains(result, ohookN) {
		t.Fatalf("result = %s, %v, calls = %+v", result, err, calls)
	}

	// A foreign team or a channel outside the allow-list is refused before any secret or request.
	for _, c := range []struct{ connection, args string }{
		{"hooker", `{"team_id":"` + teamB + `"}`},
		{"hookch", `{"team_id":"` + teamA + `","channel_id":"` + chanC + `"}`},
	} {
		calls, *env.reads = nil, 0
		if _, err := env.invoke(outgoingWebhooksList.ID, c.connection, c.args); !isInvalidRequest(err) ||
			len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d, want a local refusal", c.args, err, calls, *env.reads)
		}
	}
}

func TestOutgoingWebhooksGetBindsTheHookAndHidesSecrets(t *testing.T) {
	var calls []call
	env := newOutgoingFixture(t, nil).env(&calls)
	result, err := env.invoke(outgoingWebhooksGet.ID, "hooker", `{"hook_id":"`+ohookA+`"}`)
	noSecrets(t, "get", result)
	if err != nil || !strings.Contains(result, `"channel_id":"`+chanA+`"`) || strings.Contains(result, ohookA) ||
		!strings.Contains(result, `"trigger_words":["deploy","ship it"]`) || !strings.Contains(result, `"trigger_when":1`) ||
		strings.Contains(result, "creator_id") || len(calls) != 2 {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}

	// A hook without a channel is readable only without a channel allow-list.
	calls = nil
	result, err = env.invoke(outgoingWebhooksGet.ID, "hooker", `{"hook_id":"`+ohookN+`"}`)
	if err != nil || strings.Contains(result, `"channel_id"`) || len(calls) != 1 {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}

	for _, c := range []struct {
		connection, id string
		requests       int
	}{{"hookch", ohookD, 1}, {"hookch", ohookN, 1}, {"hooker", ohookB, 1}, {"hooker", ohookC, 2}, {"hooker", ohookT, 1}} {
		calls = nil
		_, err := env.invoke(outgoingWebhooksGet.ID, c.connection, `{"hook_id":"`+c.id+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), c.id) || strings.Contains(err.Error(), teamB) ||
			strings.Contains(err.Error(), chanB) || len(calls) != c.requests {
			t.Fatalf("%s %s: err = %v, calls = %+v", c.connection, c.id, err, calls)
		}
		noSecrets(t, "error", err.Error())
	}
	if _, err := env.invoke(outgoingWebhooksGet.ID, "hooker", `{"hook_id":"`+ohookE+`"}`); classOf(err) != "not-found" {
		t.Fatalf("err = %v", err)
	}
	calls, *env.reads = nil, 0
	if _, err := env.invoke(outgoingWebhooksGet.ID, "hooker", `{"hook_id":"../x"}`); err == nil || len(calls) != 0 ||
		*env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestOutgoingWebhooksUpdateWritesBackEveryUnchangedFieldInFull(t *testing.T) {
	f := newOutgoingFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v4/hooks/outgoing/"+ohookA {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, strings.Replace(ohookWith(ohookA, teamA, chanA, ""), "old text", "new text", 1)), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"hook_id":"` + ohookA + `","description":"new text"}`
	if _, err := env.invoke(outgoingWebhooksUpdate.ID, "hooker", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(outgoingWebhooksUpdate.ID, "hooker", args)
	noSecrets(t, "update", result)
	if err != nil || !strings.Contains(result, "new text") || strings.Contains(result, ohookA) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 3 || calls[2].method != http.MethodPut || changeCount(calls) != 1 {
		t.Fatalf("calls = %+v, want the reads and then exactly one PUT", calls)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(calls[2].body), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": ohookA, "channel_id": chanA, "display_name": "Alerts", "description": "new text",
		"trigger_words": []any{"deploy", "ship it"}, "trigger_when": float64(1),
		"callback_urls": []any{primaryCallback, "http://plain.example.invalid/x", "::not a url"},
		"content_type":  "application/json", "username": "bot-name", "icon_url": "https://example.invalid/i.png"}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v, want %v", body, want)
	}
	if strings.Contains(calls[2].body, tokenCanary) {
		t.Fatal("the PUT body carries the token")
	}

	// A display name alone keeps the description; a channelless hook is written back channelless.
	calls = nil
	if _, err := env.confirmed(outgoingWebhooksUpdate.ID, "hooker", `{"hook_id":"`+ohookA+`","display_name":""}`); err != nil ||
		!strings.Contains(calls[2].body, `"display_name":""`) || !strings.Contains(calls[2].body, `"description":"old text"`) {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestOutgoingWebhooksUpdateAndDeleteRefuseForeignHooksBeforeAnyChange(t *testing.T) {
	f := newOutgoingFixture(t, nil)
	var calls []call
	env := f.env(&calls)
	for _, operation := range []string{outgoingWebhooksUpdate.ID, outgoingWebhooksDelete.ID} {
		for _, c := range []struct{ connection, id string }{{"hookch", ohookD}, {"hookch", ohookN}, {"hooker", ohookB},
			{"hooker", ohookC}, {"hooker", ohookT}} {
			calls = nil
			args := `{"hook_id":"` + c.id + `","description":"x"}`
			if operation == outgoingWebhooksDelete.ID {
				args = `{"hook_id":"` + c.id + `"}`
			}
			_, err := env.confirmed(operation, c.connection, args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), c.id) || changeCount(calls) != 0 {
				t.Fatalf("%s %s: err = %v, calls = %+v", operation, c.id, err, calls)
			}
		}
	}
	calls, *env.reads = nil, 0
	for _, args := range []string{`{"hook_id":"` + ohookA + `"}`, `{"hook_id":"` + ohookA + `","description":"a\nb"}`,
		`{"hook_id":"` + ohookA + `","display_name":"` + strings.Repeat("x", 65) + `"}`} {
		if _, err := env.confirmed(outgoingWebhooksUpdate.ID, "hooker", args); err == nil {
			t.Fatalf("args %s were accepted", args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want local refusals", calls, *env.reads)
	}
}

func TestOutgoingWebhooksDeleteSendsOneConfirmedDelete(t *testing.T) {
	f := newOutgoingFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v4/hooks/outgoing/"+ohookA {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"hook_id":"` + ohookA + `"}`
	if _, err := env.invoke(outgoingWebhooksDelete.ID, "hooker", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	result, err := env.confirmed(outgoingWebhooksDelete.ID, "hooker", args)
	if err != nil || !strings.Contains(result, `"deleted":true`) || strings.Contains(result, ohookA) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 3 || calls[2].method != http.MethodDelete || calls[2].body != "" || changeCount(calls) != 1 {
		t.Fatalf("calls = %+v, want the reads and then exactly one DELETE", calls)
	}
}

func TestOutgoingWebhookChangesAreNeverRetriedAndNeverNameTheHook(t *testing.T) {
	for _, c := range []struct {
		operation, arguments, method, hint string
	}{
		{outgoingWebhooksUpdate.ID, `{"hook_id":"` + ohookA + `","description":"x"}`, http.MethodPut, "may have been changed"},
		{outgoingWebhooksDelete.ID, `{"hook_id":"` + ohookA + `"}`, http.MethodDelete, "may have been deleted"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(r *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+r.URL.Path+tokenCanary+`"}`), nil
			},
			"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{not json`), nil },
			"another hook": func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, ohookWith(ohookD, teamA, chanC, "")), nil
			},
			"another channel": func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, ohookWith(ohookA, teamA, chanC, "")), nil
			},
			"wrong status": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{"status":"FAIL"}`), nil },
		} {
			if (name == "another hook" || name == "another channel") && c.method == http.MethodDelete ||
				name == "wrong status" && c.method == http.MethodPut {
				continue
			}
			var calls []call
			env := newOutgoingFixture(t, fail).env(&calls)
			_, err := env.confirmed(c.operation, "hooker", c.arguments)
			if err == nil || !strings.Contains(err.Error(), c.hint) || strings.Contains(err.Error(), messageCanary) ||
				strings.Contains(err.Error(), ohookA) || changeCount(calls) != 1 || calls[len(calls)-1].method != c.method {
				t.Fatalf("%s %s: err = %v, calls = %+v", c.operation, name, err, calls)
			}
			noSecrets(t, "error", err.Error())
		}
	}
}

func TestOutgoingWebhookReadFailuresNeverNameTheHook(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		for _, operation := range []string{outgoingWebhooksGet.ID, outgoingWebhooksUpdate.ID, outgoingWebhooksDelete.ID} {
			var calls []call
			handler := func(r *http.Request) (*http.Response, error) {
				return jsonResponse(status, `{"message":"`+r.URL.Path+`"}`), nil
			}
			e := newEnvironment(t, &calls, handler)
			args := `{"hook_id":"` + ohookA + `"}`
			if operation == outgoingWebhooksUpdate.ID {
				args = `{"hook_id":"` + ohookA + `","description":"x"}`
			}
			_, err := e.confirmed(operation, "hooker", args)
			if err == nil || strings.Contains(err.Error(), ohookA) || changeCount(calls) != 0 {
				t.Fatalf("%s %d: err = %v, calls = %+v", operation, status, err, calls)
			}
			if e.red.Apply("https://x/"+ohookA) == "https://x/"+ohookA {
				t.Fatalf("%s: the hook ID was not registered with the redactor", operation)
			}
		}
	}
}

func TestCallbackOriginKeepsOnlySchemeAndHost(t *testing.T) {
	for raw, want := range map[string]string{
		(&url.URL{Scheme: "https", User: url.UserPassword("u", "p"), Host: "Host.example:8443", Path: "/a/b",
			RawQuery: "c=d", Fragment: "e"}).String(): "https://Host.example:8443",
		"http://plain.example/x": "http://plain.example",
		"":                       "",
		"::bad":                  "",
		"ftp://files.example/x":  "",
		"/relative/path":         "",
	} {
		if got, ok := callbackOrigin(raw); got != want || ok != (want != "") {
			t.Fatalf("callbackOrigin(%q) = %q, %v, want %q", raw, got, ok, want)
		}
	}
}
