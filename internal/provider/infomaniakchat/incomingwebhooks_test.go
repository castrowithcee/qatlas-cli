package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	hookA = "hooka000000000000000000a1" // chanA of teamA
	hookB = "hookb000000000000000000b2" // chanB of teamB
	hookC = "hookc000000000000000000c3" // claims teamA, but chanB lives in teamB
	hookD = "hookd000000000000000000d4" // chanC of teamA
	hookE = "hooke000000000000000000e5" // deleted
	hookF = "hookf000000000000000000f6" // direct channel
)

var hookTools = []capability.Descriptor{incomingWebhooksList, incomingWebhooksGet, incomingWebhooksUpdate,
	incomingWebhooksDelete}

var hookToolIDs = []string{incomingWebhooksList.ID, incomingWebhooksGet.ID, incomingWebhooksUpdate.ID,
	incomingWebhooksDelete.ID}

func hookWith(id, team, channel, extra string) string {
	return `{"id":"` + id + `","team_id":"` + team + `","channel_id":"` + channel + `","display_name":"Alerts",` +
		`"description":"old text","username":"bot-name","icon_url":"https://example.invalid/i.png",` +
		`"channel_locked":true,"create_at":1735689600000,"update_at":1735689700000,"delete_at":0,"user_id":"u1"` + extra + `}`
}

type hookFixture struct {
	t       *testing.T
	hooks   map[string]string
	listing string
	mutate  func(*http.Request) (*http.Response, error)
}

func newHookFixture(t *testing.T, mutate func(*http.Request) (*http.Response, error)) *hookFixture {
	if mutate == nil {
		mutate = func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}
	return &hookFixture{t: t, mutate: mutate, hooks: map[string]string{
		hookA: hookWith(hookA, teamA, chanA, ""), hookB: hookWith(hookB, teamB, chanB, ""),
		hookC: hookWith(hookC, teamA, chanB, ""), hookD: hookWith(hookD, teamA, chanC, ""),
		hookE: hookWith(hookE, teamA, chanA, "") + "", hookF: hookWith(hookF, "", dmA, ""),
	}}
}

func (f *hookFixture) handle(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		return f.mutate(r)
	}
	switch r.URL.Path {
	case "/api/v4/hooks/incoming":
		return jsonResponse(200, f.listing), nil
	case "/api/v4/users/me/teams/" + teamA + "/channels":
		return jsonResponse(200, "["+strings.Join([]string{channelWith(chanA, teamA, "O", ""),
			channelWith(chanC, teamA, "P", ""), channelWith(chanE, teamA, "O", archivedAt)}, ",")+"]"), nil
	}
	for id, body := range f.hooks {
		if r.URL.Path == "/api/v4/hooks/incoming/"+id {
			if id == hookE {
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

func (f *hookFixture) env(calls *[]call) *environment {
	return newEnvironment(f.t, calls, f.handle)
}

func changeCount(calls []call) int {
	n := 0
	for _, c := range calls {
		if c.method != http.MethodGet {
			n++
		}
	}
	return n
}

func TestIncomingWebhookToolsNeedTheToolsList(t *testing.T) {
	for _, d := range hookTools {
		if !d.RequiresToolAllowList || d.Risk.DataSensitivity != integrationSensitivity || !d.Risk.OpenWorld ||
			withGroup(d).Group != "integrations" {
			t.Fatalf("%s = %+v", d.ID, d)
		}
	}
	for _, d := range hookTools[2:] {
		if d.Risk.Confirmation != capability.ConfirmationRequired || d.Risk.Idempotency != capability.IdempotencyIdempotent {
			t.Fatalf("%s risk = %+v", d.ID, d.Risk)
		}
	}
	if incomingWebhooksDelete.Risk.Effect != capability.EffectDelete || incomingWebhooksUpdate.Risk.Effect != capability.EffectUpdate {
		t.Fatal("unexpected webhook effects")
	}
	var calls []call
	env := newHookFixture(t, nil).env(&calls)
	for _, c := range []struct{ id, args string }{
		{incomingWebhooksList.ID, `{"team_id":"` + teamA + `"}`},
		{incomingWebhooksGet.ID, `{"hook_id":"` + hookA + `"}`},
		{incomingWebhooksUpdate.ID, `{"hook_id":"` + hookA + `","description":"x"}`},
		{incomingWebhooksDelete.ID, `{"hook_id":"` + hookA + `"}`},
	} {
		if _, err := env.confirmed(c.id, "hookno", c.args); err == nil {
			t.Fatalf("%s was reachable without the tools list", c.id)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d", calls, *env.reads)
	}
}

func TestIncomingWebhooksListFiltersToReachableChannelsOfTheTeam(t *testing.T) {
	f := newHookFixture(t, nil)
	f.listing = "[" + strings.Join([]string{
		hookWith(hookA, teamA, chanA, ""),
		hookWith(hookD, teamA, chanC, ""),
		hookWith(hookB, teamB, chanB, ""),
		hookWith(hookC, teamA, chanB, ""),
		strings.Replace(hookWith(hookE, teamA, chanA, ""), `"delete_at":0`, `"delete_at":5`, 1),
		hookWith(hookF, teamA, chanE, ""),
		hookWith("../bad", teamA, chanA, ""),
	}, ",") + "]"
	var calls []call
	env := f.env(&calls)

	result, err := env.invoke(incomingWebhooksList.ID, "hooker", `{"team_id":"`+teamA+`","page":3,"per_page":7}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	for _, id := range []string{hookA, hookD} {
		if !strings.Contains(result, id) {
			t.Fatalf("result = %s, want %s", result, id)
		}
	}
	for _, id := range []string{hookB, hookC, hookE, hookF, "bad"} {
		if strings.Contains(result, id) {
			t.Fatalf("result = %s, want %s dropped", result, id)
		}
	}
	if !strings.Contains(result, `"count":2`) || !strings.Contains(result, `"has_more":true`) ||
		strings.Contains(result, "user_id") || calls[0].method != http.MethodGet ||
		calls[0].query.Get("team_id") != teamA || calls[0].query.Get("page") != "2" ||
		calls[0].query.Get("per_page") != "7" || len(calls) != 2 {
		t.Fatalf("result = %s, calls = %+v, want one request for kChat page 3 and the channel read", result, calls)
	}

	// The channel allow-list narrows the listing to chanA.
	result, err = env.invoke(incomingWebhooksList.ID, "hookch", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, hookA) || strings.Contains(result, hookD) {
		t.Fatalf("result = %s, %v", result, err)
	}

	calls, *env.reads = nil, 0
	if _, err := env.invoke(incomingWebhooksList.ID, "hooker", `{"team_id":"`+teamB+`"}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
}

func TestIncomingWebhooksGetBindsTheHookThroughItsChannel(t *testing.T) {
	var calls []call
	env := newHookFixture(t, nil).env(&calls)
	result, err := env.invoke(incomingWebhooksGet.ID, "hooker", `{"hook_id":"`+hookA+`"}`)
	if err != nil || !strings.Contains(result, `"channel_id":"`+chanA+`"`) || !strings.Contains(result, `"username":"bot-name"`) ||
		strings.Contains(result, hookA) || strings.Contains(result, "user_id") || len(calls) != 2 {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}

	// Outside the allow-list or the connection's teams: refused with the hook ID and the target unnamed.
	for _, c := range []struct {
		connection, id string
		requests       int
	}{{"hookch", hookD, 1}, {"hooker", hookB, 1}, {"hooker", hookC, 2}, {"hooker", hookF, 1}} {
		calls = nil
		_, err := env.invoke(incomingWebhooksGet.ID, c.connection, `{"hook_id":"`+c.id+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), c.id) || strings.Contains(err.Error(), teamB) ||
			strings.Contains(err.Error(), chanB) || len(calls) != c.requests {
			t.Fatalf("%s: err = %v, calls = %+v", c.id, err, calls)
		}
	}
	// A deleted hook is not found.
	if _, err := env.invoke(incomingWebhooksGet.ID, "hooker", `{"hook_id":"`+hookE+`"}`); classOf(err) != "not-found" {
		t.Fatalf("err = %v", err)
	}
	// A malformed ID is refused before any secret is read.
	calls, *env.reads = nil, 0
	if _, err := env.invoke(incomingWebhooksGet.ID, "hooker", `{"hook_id":"../x"}`); err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestIncomingWebhooksUpdateWritesTheHookBackWithOnlyNameAndDescriptionChanged(t *testing.T) {
	f := newHookFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v4/hooks/incoming/"+hookA {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, strings.Replace(hookWith(hookA, teamA, chanA, ""), "old text", "new text", 1)), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"hook_id":"` + hookA + `","description":"new text"}`
	if _, err := env.invoke(incomingWebhooksUpdate.ID, "hooker", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(incomingWebhooksUpdate.ID, "hooker", args)
	if err != nil || !strings.Contains(result, "new text") || strings.Contains(result, hookA) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 3 || calls[2].method != http.MethodPut || changeCount(calls) != 1 {
		t.Fatalf("calls = %+v, want the reads and then exactly one PUT", calls)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(calls[2].body), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": hookA, "channel_id": chanA, "display_name": "Alerts", "description": "new text",
		"username": "bot-name", "icon_url": "https://example.invalid/i.png", "channel_locked": true}
	if len(body) != len(want) {
		t.Fatalf("body = %v, want %v", body, want)
	}
	for key, value := range want {
		if body[key] != value {
			t.Fatalf("body[%s] = %v, want %v", key, body[key], value)
		}
	}

	// A display name alone keeps the description; both may be cleared.
	calls = nil
	if _, err := env.confirmed(incomingWebhooksUpdate.ID, "hooker", `{"hook_id":"`+hookA+`","display_name":""}`); err != nil ||
		!strings.Contains(calls[2].body, `"display_name":""`) || !strings.Contains(calls[2].body, `"description":"old text"`) {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestIncomingWebhooksUpdateAndDeleteRefuseForeignHooksBeforeAnyChange(t *testing.T) {
	f := newHookFixture(t, nil)
	var calls []call
	env := f.env(&calls)
	for _, operation := range []string{incomingWebhooksUpdate.ID, incomingWebhooksDelete.ID} {
		for _, c := range []struct{ connection, id string }{{"hookch", hookD}, {"hooker", hookB}, {"hooker", hookC}, {"hooker", hookF}} {
			calls = nil
			args := `{"hook_id":"` + c.id + `","description":"x"}`
			if operation == incomingWebhooksDelete.ID {
				args = `{"hook_id":"` + c.id + `"}`
			}
			_, err := env.confirmed(operation, c.connection, args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), c.id) || changeCount(calls) != 0 {
				t.Fatalf("%s %s: err = %v, calls = %+v", operation, c.id, err, calls)
			}
		}
	}
	// Invalid input is refused before any request.
	calls, *env.reads = nil, 0
	for _, args := range []string{`{"hook_id":"` + hookA + `"}`, `{"hook_id":"` + hookA + `","description":"a\nb"}`,
		`{"hook_id":"` + hookA + `","display_name":"` + strings.Repeat("x", 65) + `"}`} {
		if _, err := env.confirmed(incomingWebhooksUpdate.ID, "hooker", args); err == nil {
			t.Fatalf("args %s were accepted", args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want local refusals", calls, *env.reads)
	}
}

func TestIncomingWebhooksDeleteSendsOneConfirmedDelete(t *testing.T) {
	f := newHookFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v4/hooks/incoming/"+hookA {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"hook_id":"` + hookA + `"}`
	if _, err := env.invoke(incomingWebhooksDelete.ID, "hooker", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	result, err := env.confirmed(incomingWebhooksDelete.ID, "hooker", args)
	if err != nil || !strings.Contains(result, `"deleted":true`) || strings.Contains(result, hookA) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 3 || calls[2].method != http.MethodDelete || calls[2].body != "" || changeCount(calls) != 1 {
		t.Fatalf("calls = %+v, want the reads and then exactly one DELETE", calls)
	}
}

func TestIncomingWebhookChangesAreNeverRetriedAndNeverNameTheHook(t *testing.T) {
	for _, c := range []struct {
		operation, arguments, method, hint string
	}{
		{incomingWebhooksUpdate.ID, `{"hook_id":"` + hookA + `","description":"x"}`, http.MethodPut, "may have been changed"},
		{incomingWebhooksDelete.ID, `{"hook_id":"` + hookA + `"}`, http.MethodDelete, "may have been deleted"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(r *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+r.URL.Path+`"}`), nil
			},
			"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{not json`), nil },
			"another hook": func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, hookWith(hookD, teamA, chanC, "")), nil
			},
			"another channel": func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, hookWith(hookA, teamA, chanC, "")), nil
			},
			"wrong status": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{"status":"FAIL"}`), nil },
		} {
			if (name == "another hook" || name == "another channel") && c.method == http.MethodDelete ||
				name == "wrong status" && c.method == http.MethodPut {
				continue
			}
			var calls []call
			env := newHookFixture(t, fail).env(&calls)
			_, err := env.confirmed(c.operation, "hooker", c.arguments)
			if err == nil || !strings.Contains(err.Error(), c.hint) || strings.Contains(err.Error(), messageCanary) ||
				strings.Contains(err.Error(), hookA) || changeCount(calls) != 1 || calls[len(calls)-1].method != c.method {
				t.Fatalf("%s %s: err = %v, calls = %+v", c.operation, name, err, calls)
			}
		}
	}
}

func TestIncomingWebhookReadFailuresNeverNameTheHook(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		for _, operation := range []string{incomingWebhooksGet.ID, incomingWebhooksUpdate.ID, incomingWebhooksDelete.ID} {
			var calls []call
			handler := func(r *http.Request) (*http.Response, error) {
				return jsonResponse(status, `{"message":"`+r.URL.Path+`"}`), nil
			}
			e := newEnvironment(t, &calls, handler)
			args := `{"hook_id":"` + hookA + `"}`
			if operation == incomingWebhooksUpdate.ID {
				args = `{"hook_id":"` + hookA + `","description":"x"}`
			}
			_, err := e.confirmed(operation, "hooker", args)
			if err == nil || strings.Contains(err.Error(), hookA) || changeCount(calls) != 0 {
				t.Fatalf("%s %d: err = %v, calls = %+v", operation, status, err, calls)
			}
			if e.red.Apply("https://x/"+hookA) == "https://x/"+hookA {
				t.Fatalf("%s: the hook ID was not registered with the redactor", operation)
			}
		}
	}
}
