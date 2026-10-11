package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	botA = "bota0000000000000000000a1"
	botB = "botb0000000000000000000b2"
)

var botToolIDs = []string{botsList.ID, botsGet.ID, botsUpdate.ID, botsDisable.ID, botsEnable.ID}

func botWith(id, name string) string {
	return `{"user_id":"` + id + `","username":"` + name + `","display_name":"Deploy","description":"old",` +
		`"owner_id":"owner0000000000000000000a","create_at":1,"update_at":2,"delete_at":0,"last_icon_update":9,` +
		`"token":"leak-canary"}`
}

// botHandler answers the gate's team read with the given teams and the bot endpoints with fixed bots.
func botHandler(t *testing.T, teams string, change func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me/teams":
			return jsonResponse(200, teams), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/bots":
			return jsonResponse(200, "["+botWith(botA, "deploy")+","+botWith("../bad", "x")+"]"), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/bots/"+botA:
			return jsonResponse(200, botWith(botA, "deploy")), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/bots/"+botB:
			return jsonResponse(200, botWith(botA, "deploy")), nil
		case (r.Method == http.MethodPut || r.Method == http.MethodPost) && change != nil:
			return change(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

var bothTeams = "[" + teamJSONOf(teamA, "A") + "," + teamJSONOf(teamB, "B") + "]"

func TestBotToolsDescriptors(t *testing.T) {
	for _, d := range []capability.Descriptor{botsList, botsGet, botsUpdate} {
		if !d.RequiresToolAllowList || withGroup(d).Group != "administration" || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != administrationSensitivity ||
			!strings.Contains(d.Description, "Bot tokens and creating bots are not offered") {
			t.Fatalf("%s = %+v", d.ID, d)
		}
	}
	r := botsUpdate.Risk
	if r.Effect != capability.EffectUpdate || r.Idempotency != capability.IdempotencyIdempotent ||
		r.Confirmation != capability.ConfirmationRequired {
		t.Fatalf("risk = %+v", r)
	}
}

func TestBotGateRefusesChannelTargetsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, botHandler(t, bothTeams, nil))
	for _, c := range []struct{ id, args string }{
		{botsList.ID, `{}`}, {botsGet.ID, `{"bot_user_id":"` + botA + `"}`},
	} {
		_, err := env.invoke(c.id, "botch", c.args)
		if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d", c.id, err, calls, *env.reads)
		}
	}
	_, err := env.confirmed(botsUpdate.ID, "botch", `{"bot_user_id":"`+botA+`","description":"x"}`)
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), chanA) {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestBotGateRefusesUnboundTeamsAndFailsClosed(t *testing.T) {
	for _, c := range []struct {
		name  string
		teams string
		code  int
	}{
		{"unbound team", bothTeams, 200},
		{"empty", "[]", 200},
		{"invalid id", `[{"id":"../x"}]`, 200},
		{"unreadable", "not json", 200},
		{"error", "", 500},
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/v4/users/me/teams" {
				t.Fatalf("%s: unexpected request %s %s", c.name, r.Method, r.URL.Path)
			}
			return jsonResponse(c.code, c.teams), nil
		})
		result, err := env.confirmed(botsUpdate.ID, "botone", `{"bot_user_id":"`+botA+`","description":"x"}`)
		if err == nil || result != "" || len(calls) != 1 || changeCount(calls) != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", c.name, err, calls)
		}
		if c.name == "unbound team" && (!isInvalidRequest(err) || strings.Contains(err.Error(), teamB)) {
			t.Fatalf("err = %v, want an invalid request without the team", err)
		}
	}
}

func TestBotsListGetReturnOnlyTheAllowedFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, botHandler(t, bothTeams, nil))
	result, err := env.invoke(botsList.ID, "botall", `{"page":3,"per_page":2}`)
	if err != nil || strings.Contains(result, "bad") || strings.Contains(result, "leak-canary") ||
		strings.Contains(result, "create_at") || !strings.Contains(result, `"count":1`) ||
		!strings.Contains(result, `"has_more":true`) || len(calls) != 2 || calls[1].path != "/api/v4/bots" ||
		calls[1].query.Get("page") != "2" || calls[1].query.Get("per_page") != "2" {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
	result, err = env.invoke(botsGet.ID, "botall", `{"bot_user_id":"`+botA+`"}`)
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(result), &got) != nil || len(got) != 6 || got["deleted"] != false ||
		got["username"] != "deploy" || got["owner_id"] == "" {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	// An answer for another bot than the one asked for is invalid.
	if _, err := env.invoke(botsGet.ID, "botall", `{"bot_user_id":"`+botB+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("err = %v", err)
	}
}

func TestBotsUpdateSendsOnlyTheGivenFieldsInOneChangingRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, botHandler(t, bothTeams, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, botWith(botA, "renamed")), nil
	}))
	args := `{"bot_user_id":"` + botA + `","username":"renamed","description":""}`
	if _, err := env.invoke(botsUpdate.ID, "botall", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	result, err := env.confirmed(botsUpdate.ID, "botall", args)
	if err != nil || !strings.Contains(result, `"username":"renamed"`) || strings.Contains(result, "leak-canary") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	last := calls[len(calls)-1]
	if changeCount(calls) != 1 || last.method != http.MethodPut || last.path != "/api/v4/bots/"+botA ||
		last.body != `{"username":"renamed","description":""}` {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestBotsUpdateRefusesBadInputBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, botHandler(t, bothTeams, nil))
	for _, args := range []string{
		`{"bot_user_id":"` + botA + `"}`,
		`{"bot_user_id":"` + botA + `","username":"Ab"}`,
		`{"bot_user_id":"` + botA + `","username":"UPPER-case"}`,
		`{"bot_user_id":"` + botA + `","username":"` + strings.Repeat("a", 23) + `"}`,
		`{"bot_user_id":"` + botA + `","display_name":"a\u0001b"}`,
		`{"bot_user_id":"` + botA + `","display_name":"` + strings.Repeat("a", 65) + `"}`,
		`{"bot_user_id":"` + botA + `","role":"admin"}`,
		`{"bot_user_id":"../x","description":"x"}`,
		`{"bot_user_id":"` + botA + `/token","description":"x"}`,
	} {
		if _, err := env.confirmed(botsUpdate.ID, "botall", args); err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d", args, err, calls, *env.reads)
		}
	}
	for _, args := range []string{`{"bot_user_id":"../x"}`, `{"bot_user_id":"` + botA + `%2ftokens"}`} {
		if _, err := env.invoke(botsGet.ID, "botall", args); err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", args, err, calls)
		}
	}
}

func TestBotToolsNeedTheToolsList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, botHandler(t, bothTeams, nil))
	if _, err := env.confirmed(botsUpdate.ID, "botno", `{"bot_user_id":"`+botA+`","description":"x"}`); err == nil ||
		len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestBotsUpdateIsNeverRepeatedAfterAnUncertainResult(t *testing.T) {
	for name, answer := range map[string]func() (*http.Response, error){
		"timeout":  func() (*http.Response, error) { return nil, timeoutError{} },
		"5xx":      func() (*http.Response, error) { return jsonResponse(502, ""), nil },
		"unusable": func() (*http.Response, error) { return jsonResponse(200, "not json"), nil },
		"other bot": func() (*http.Response, error) {
			return jsonResponse(200, botWith(botB, "x")), nil
		},
	} {
		var calls []call
		env := newEnvironment(t, &calls, botHandler(t, bothTeams, func(*http.Request) (*http.Response, error) {
			return answer()
		}))
		_, err := env.confirmed(botsUpdate.ID, "botall", `{"bot_user_id":"`+botA+`","description":"x"}`)
		if err == nil || !strings.Contains(err.Error(), "the bot may have been changed") ||
			strings.Contains(err.Error(), "message") || changeCount(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %+v", name, err, calls)
		}
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "context deadline exceeded" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestBotStateToolsDescriptors(t *testing.T) {
	for _, d := range []capability.Descriptor{botsDisable, botsEnable} {
		r := d.Risk
		if !d.RequiresToolAllowList || withGroup(d).Group != "administration" || !r.OpenWorld ||
			r.Effect != capability.EffectUpdate || r.Idempotency != capability.IdempotencyIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || r.DataSensitivity != administrationSensitivity {
			t.Fatalf("%s = %+v", d.ID, d)
		}
	}
}

func TestBotStateToolsSendOneBodylessPost(t *testing.T) {
	for _, c := range []struct {
		id, action string
		deleteAt   int
	}{{botsDisable.ID, "disable", 5}, {botsEnable.ID, "enable", 0}} {
		var calls []call
		env := newEnvironment(t, &calls, botHandler(t, bothTeams, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, strings.Replace(botWith(botA, "deploy"), `"delete_at":0`,
				`"delete_at":`+itoa(c.deleteAt), 1)), nil
		}))
		args := `{"bot_user_id":"` + botA + `"}`
		if _, err := env.invoke(c.id, "botall", args); !isConfirmationRequired(err) || len(calls) != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", c.id, err, calls)
		}
		result, err := env.confirmed(c.id, "botall", args)
		var got map[string]any
		last := calls[len(calls)-1]
		if err != nil || json.Unmarshal([]byte(result), &got) != nil || len(got) != 6 ||
			got["deleted"] != (c.deleteAt != 0) || strings.Contains(result, "leak-canary") ||
			changeCount(calls) != 1 || last.method != http.MethodPost ||
			last.path != "/api/v4/bots/"+botA+"/"+c.action || last.body != "" {
			t.Fatalf("%s: result = %s, err = %v, calls = %+v", c.id, result, err, calls)
		}
	}
}

func TestBotStateToolsRefuseBeforeAnyChangingRequest(t *testing.T) {
	for _, id := range []string{botsDisable.ID, botsEnable.ID} {
		args := `{"bot_user_id":"` + botA + `"}`
		var calls []call
		env := newEnvironment(t, &calls, botHandler(t, bothTeams, nil))
		if _, err := env.confirmed(id, "botch", args); !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s channel: err = %v, calls = %+v", id, err, calls)
		}
		if _, err := env.confirmed(id, "botno", args); err == nil || len(calls) != 0 {
			t.Fatalf("%s no tools: err = %v, calls = %+v", id, err, calls)
		}
		for _, bad := range []string{`{"bot_user_id":"../x"}`, `{"bot_user_id":"` + botA + `/token"}`, `{}`} {
			if _, err := env.confirmed(id, "botall", bad); err == nil || len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("%s %s: err = %v, calls = %+v", id, bad, err, calls)
			}
		}
		calls = nil
		env = newEnvironment(t, &calls, botHandler(t, bothTeams, nil))
		if _, err := env.confirmed(id, "botone", args); !isInvalidRequest(err) || len(calls) != 1 || changeCount(calls) != 0 {
			t.Fatalf("%s unbound: err = %v, calls = %+v", id, err, calls)
		}
	}
}

func TestBotStateToolsAreNeverRepeatedAfterAnUncertainResult(t *testing.T) {
	for _, c := range []struct{ id, want string }{
		{botsDisable.ID, "the bot may have been disabled, read it before changing"},
		{botsEnable.ID, "the bot may have been enabled, read it before changing"},
	} {
		for name, answer := range map[string]func() (*http.Response, error){
			"timeout":   func() (*http.Response, error) { return nil, timeoutError{} },
			"5xx":       func() (*http.Response, error) { return jsonResponse(502, ""), nil },
			"unusable":  func() (*http.Response, error) { return jsonResponse(200, "not json"), nil },
			"other bot": func() (*http.Response, error) { return jsonResponse(200, botWith(botB, "x")), nil },
		} {
			var calls []call
			env := newEnvironment(t, &calls, botHandler(t, bothTeams, func(*http.Request) (*http.Response, error) {
				return answer()
			}))
			_, err := env.confirmed(c.id, "botall", `{"bot_user_id":"`+botA+`"}`)
			if err == nil || !strings.Contains(err.Error(), c.want) || changeCount(calls) != 1 {
				t.Fatalf("%s %s: err = %v, calls = %+v", c.id, name, err, calls)
			}
		}
	}
}
