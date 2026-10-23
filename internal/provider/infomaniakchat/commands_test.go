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
	cmdA = "cmda0000000000000000000a1" // teamA
	cmdB = "cmdb0000000000000000000b2" // teamB
	cmdE = "cmde0000000000000000000e5" // deleted, teamA
)

var commandTools = []capability.Descriptor{commandsList, commandsGet, commandsUpdate, commandsDelete}

var commandToolIDs = []string{commandsList.ID, commandsGet.ID, commandsUpdate.ID, commandsDelete.ID}

const cmdTokenCanary = "command-token-canary-91bb"

var cmdCallback = (&url.URL{Scheme: "https", User: url.UserPassword(userCanary, "pw"),
	Host: "cmd.example.invalid:8443", Path: "/" + pathCanary, RawQuery: "k=" + queryCanary, Fragment: "frag"}).String()

func commandWith(id, team string) string {
	return `{"id":"` + id + `","token":"` + cmdTokenCanary + `","team_id":"` + team + `","trigger":"deploy",` +
		`"method":"P","url":"` + cmdCallback + `","display_name":"Deploy","description":"old text",` +
		`"username":"bot-name","icon_url":"https://example.invalid/i.png","auto_complete":true,` +
		`"auto_complete_desc":"old desc","auto_complete_hint":"[env]","create_at":1735689600000,` +
		`"update_at":1735689700000,"delete_at":0,"creator_id":"u1","unknown_field":{"k":1}}`
}

type commandFixture struct {
	t       *testing.T
	listing string
	mutate  func(*http.Request) (*http.Response, error)
}

func newCommandFixture(t *testing.T, mutate func(*http.Request) (*http.Response, error)) *commandFixture {
	if mutate == nil {
		mutate = func(r *http.Request) (*http.Response, error) {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	}
	return &commandFixture{t: t, mutate: mutate}
}

func (f *commandFixture) handle(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		return f.mutate(r)
	}
	switch r.URL.Path {
	case "/api/v4/commands":
		return jsonResponse(200, f.listing), nil
	case "/api/v4/commands/" + cmdA:
		return jsonResponse(200, commandWith(cmdA, teamA)), nil
	case "/api/v4/commands/" + cmdB:
		return jsonResponse(200, commandWith(cmdB, teamB)), nil
	case "/api/v4/commands/" + cmdE:
		return jsonResponse(200, strings.Replace(commandWith(cmdE, teamA), `"delete_at":0`, `"delete_at":5`, 1)), nil
	}
	f.t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	return nil, nil
}

func noCommandSecrets(t *testing.T, what, text string) {
	t.Helper()
	for _, canary := range []string{cmdTokenCanary, pathCanary, queryCanary, userCanary, "frag", "unknown_field"} {
		if strings.Contains(text, canary) {
			t.Fatalf("%s shows %q: %s", what, canary, text)
		}
	}
}

func TestCommandToolsNeedTheToolsListAndAreNotInAProfile(t *testing.T) {
	for _, d := range commandTools {
		if !d.RequiresToolAllowList || d.Risk.DataSensitivity != integrationSensitivity || !d.Risk.OpenWorld ||
			withGroup(d).Group != "integrations" {
			t.Fatalf("%s = %+v", d.ID, d)
		}
	}
	for _, d := range commandTools[2:] {
		if d.Risk.Confirmation != capability.ConfirmationRequired || d.Risk.Idempotency != capability.IdempotencyIdempotent {
			t.Fatalf("%s risk = %+v", d.ID, d.Risk)
		}
	}
	if commandsDelete.Risk.Effect != capability.EffectDelete || commandsUpdate.Risk.Effect != capability.EffectUpdate {
		t.Fatal("unexpected command effects")
	}
	var calls []call
	env := newCommandFixture(t, nil).env(&calls)
	for _, c := range []struct{ id, args string }{
		{commandsList.ID, `{"team_id":"` + teamA + `"}`},
		{commandsGet.ID, `{"command_id":"` + cmdA + `"}`},
		{commandsUpdate.ID, `{"command_id":"` + cmdA + `","description":"x"}`},
		{commandsDelete.ID, `{"command_id":"` + cmdA + `"}`},
	} {
		if _, err := env.confirmed(c.id, "hookno", c.args); err == nil {
			t.Fatalf("%s was reachable without the tools list", c.id)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d", calls, *env.reads)
	}
}

func (f *commandFixture) env(calls *[]call) *environment {
	return newEnvironment(f.t, calls, f.handle)
}

func TestCommandToolsRefuseAChannelAllowListLocally(t *testing.T) {
	var calls []call
	env := newCommandFixture(t, nil).env(&calls)
	for _, c := range []struct{ id, args string }{
		{commandsList.ID, `{"team_id":"` + teamA + `"}`},
		{commandsGet.ID, `{"command_id":"` + cmdA + `"}`},
		{commandsUpdate.ID, `{"command_id":"` + cmdA + `","description":"x"}`},
		{commandsDelete.ID, `{"command_id":"` + cmdA + `"}`},
	} {
		if _, err := env.confirmed(c.id, "cmdch", c.args); !isInvalidRequest(err) {
			t.Fatalf("%s: err = %v, want a local refusal", c.id, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want no request and no secret", calls, *env.reads)
	}
}

func TestCommandsListFiltersAndShowsOnlyTheCallbackOrigin(t *testing.T) {
	f := newCommandFixture(t, nil)
	f.listing = "[" + strings.Join([]string{commandWith(cmdA, teamA), commandWith(cmdB, teamB),
		strings.Replace(commandWith(cmdE, teamA), `"delete_at":0`, `"delete_at":5`, 1),
		commandWith("../bad", teamA)}, ",") + "]"
	var calls []call
	env := f.env(&calls)
	result, err := env.invoke(commandsList.ID, "cmder", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	noCommandSecrets(t, "list", result)
	if !strings.Contains(result, cmdA) || strings.Contains(result, cmdB) || strings.Contains(result, cmdE) ||
		strings.Contains(result, "bad") || !strings.Contains(result, `"callback_origin":"https://cmd.example.invalid:8443"`) ||
		!strings.Contains(result, `"total":1`) || len(calls) != 1 || calls[0].query.Get("team_id") != teamA ||
		calls[0].query.Get("custom_only") != "true" {
		t.Fatalf("result = %s, calls = %+v", result, calls)
	}
	calls, *env.reads = nil, 0
	if _, err := env.invoke(commandsList.ID, "cmder", `{"team_id":"`+teamB+`"}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
}

func TestCommandsGetBindsTheTeamAndHidesSecrets(t *testing.T) {
	var calls []call
	env := newCommandFixture(t, nil).env(&calls)
	result, err := env.invoke(commandsGet.ID, "cmder", `{"command_id":"`+cmdA+`"}`)
	noCommandSecrets(t, "get", result)
	if err != nil || strings.Contains(result, cmdA) || !strings.Contains(result, `"trigger":"deploy"`) ||
		!strings.Contains(result, `"creator_id":"u1"`) || len(calls) != 1 {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
	calls = nil
	_, err = env.invoke(commandsGet.ID, "cmder", `{"command_id":"`+cmdB+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), cmdB) || strings.Contains(err.Error(), teamB) ||
		len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	if _, err := env.invoke(commandsGet.ID, "cmder", `{"command_id":"`+cmdE+`"}`); classOf(err) != "not-found" {
		t.Fatalf("err = %v", err)
	}
	calls, *env.reads = nil, 0
	if _, err := env.invoke(commandsGet.ID, "cmder", `{"command_id":"../x"}`); err == nil || len(calls) != 0 ||
		*env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestCommandsUpdateWritesTheReadCommandBackAndChangesOnlyTexts(t *testing.T) {
	f := newCommandFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v4/commands/"+cmdA {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, strings.Replace(commandWith(cmdA, teamA), "old text", "new text", 1)), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"command_id":"` + cmdA + `","description":"new text","auto_complete":false}`
	if _, err := env.invoke(commandsUpdate.ID, "cmder", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(commandsUpdate.ID, "cmder", args)
	noCommandSecrets(t, "update", result)
	if err != nil || !strings.Contains(result, "new text") || strings.Contains(result, cmdA) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodPut || changeCount(calls) != 1 {
		t.Fatalf("calls = %+v, want the read and then exactly one PUT", calls)
	}
	var body, read map[string]any
	if err := json.Unmarshal([]byte(calls[1].body), &body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(commandWith(cmdA, teamA)), &read); err != nil {
		t.Fatal(err)
	}
	read["description"], read["auto_complete"] = "new text", false
	if !reflect.DeepEqual(body, read) {
		t.Fatalf("body = %v, want %v", body, read)
	}
	for _, key := range []string{"url", "method", "trigger", "token"} {
		if body[key] == nil || body[key] != read[key] {
			t.Fatalf("PUT body %s = %v, want it as read", key, body[key])
		}
	}
}

func TestCommandsUpdateAndDeleteRefuseForeignCommandsAndBadInputBeforeAnyChange(t *testing.T) {
	var calls []call
	env := newCommandFixture(t, nil).env(&calls)
	for _, c := range []struct{ operation, args string }{
		{commandsUpdate.ID, `{"command_id":"` + cmdB + `","description":"x"}`},
		{commandsDelete.ID, `{"command_id":"` + cmdB + `"}`},
	} {
		calls = nil
		_, err := env.confirmed(c.operation, "cmder", c.args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), cmdB) || strings.Contains(err.Error(), teamB) ||
			changeCount(calls) != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", c.operation, err, calls)
		}
	}
	calls, *env.reads = nil, 0
	for _, args := range []string{`{"command_id":"` + cmdA + `"}`, `{"command_id":"` + cmdA + `","description":"a\nb"}`,
		`{"command_id":"` + cmdA + `","display_name":"` + strings.Repeat("x", 65) + `"}`,
		`{"command_id":"` + cmdA + `","auto_complete_hint":"` + strings.Repeat("x", 129) + `"}`,
		`{"command_id":"../x","description":"x"}`} {
		if _, err := env.confirmed(commandsUpdate.ID, "cmder", args); err == nil {
			t.Fatalf("args %s were accepted", args)
		}
	}
	if _, err := env.confirmed(commandsDelete.ID, "cmder", `{"command_id":"../x"}`); err == nil {
		t.Fatal("an invalid ID was accepted")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want local refusals", calls, *env.reads)
	}
}

func TestCommandsDeleteSendsOneConfirmedDelete(t *testing.T) {
	f := newCommandFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v4/commands/"+cmdA {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"command_id":"` + cmdA + `"}`
	if _, err := env.invoke(commandsDelete.ID, "cmder", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	result, err := env.confirmed(commandsDelete.ID, "cmder", args)
	if err != nil || !strings.Contains(result, `"deleted":true`) || strings.Contains(result, cmdA) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodDelete || calls[1].body != "" || changeCount(calls) != 1 {
		t.Fatalf("calls = %+v, want the read and then exactly one DELETE", calls)
	}
}

func TestCommandChangesAreNeverRetriedAndNeverShowTheTokenOrID(t *testing.T) {
	for _, c := range []struct {
		operation, arguments, method, hint string
	}{
		{commandsUpdate.ID, `{"command_id":"` + cmdA + `","description":"x"}`, http.MethodPut, "may have been changed"},
		{commandsDelete.ID, `{"command_id":"` + cmdA + `"}`, http.MethodDelete, "may have been deleted"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(r *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+r.URL.Path+cmdTokenCanary+`"}`), nil
			},
			"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{not json`), nil },
			"another command": func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, commandWith(cmdB, teamA)), nil
			},
			"wrong status": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{"status":"FAIL"}`), nil },
		} {
			if name == "another command" && c.method == http.MethodDelete ||
				name == "wrong status" && c.method == http.MethodPut {
				continue
			}
			var calls []call
			env := newCommandFixture(t, fail).env(&calls)
			_, err := env.confirmed(c.operation, "cmder", c.arguments)
			if err == nil || !strings.Contains(err.Error(), c.hint) || strings.Contains(err.Error(), "message may") ||
				strings.Contains(err.Error(), messageCanary) || strings.Contains(err.Error(), cmdA) ||
				changeCount(calls) != 1 || calls[len(calls)-1].method != c.method {
				t.Fatalf("%s %s: err = %v, calls = %+v", c.operation, name, err, calls)
			}
			noCommandSecrets(t, "error", err.Error())
		}
	}
}

func TestCommandReadFailuresNeverNameTheCommand(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		for _, operation := range []string{commandsGet.ID, commandsUpdate.ID, commandsDelete.ID} {
			var calls []call
			e := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(status, `{"message":"`+r.URL.Path+`"}`), nil
			})
			args := `{"command_id":"` + cmdA + `"}`
			if operation == commandsUpdate.ID {
				args = `{"command_id":"` + cmdA + `","description":"x"}`
			}
			_, err := e.confirmed(operation, "cmder", args)
			if err == nil || strings.Contains(err.Error(), cmdA) || changeCount(calls) != 0 {
				t.Fatalf("%s %d: err = %v, calls = %+v", operation, status, err, calls)
			}
			if e.red.Apply("https://x/"+cmdA) == "https://x/"+cmdA {
				t.Fatalf("%s: the command ID was not registered with the redactor", operation)
			}
		}
	}
}
