package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	queriesChat = "-100"
	rawQueryID  = "RAWQUERYID42"
)

type queryEnv struct {
	filesEnv
	calls  []string
	status int
	fail   error
}

func newQueryEnv(t *testing.T) *queryEnv {
	return &queryEnv{filesEnv: newFilesEnv(t, t.TempDir(), testToken), status: 200}
}

func (e *queryEnv) transport() roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		e.calls = append(e.calls, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]+" "+string(data))
		if e.fail != nil {
			return nil, e.fail
		}
		if e.status != 200 {
			return response(e.status, `{"ok":false,"description":"SECRETDESC"}`), nil
		}
		return response(200, `{"ok":true,"result":true}`), nil
	}
}

func (e *queryEnv) run(t *testing.T, tool string, args map[string]any, confirmed bool) (any, error) {
	t.Helper()
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	raw, _ := json.Marshal(args)
	ctx := context.Background()
	if confirmed {
		ctx = capability.WithConfirmed(ctx)
	}
	if tool == "callback" {
		return invokeCallbacksAnswerWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	}
	return invokeJoinRequestQueriesAnswerWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

func TestQueryAnswersSendFixedBodies(t *testing.T) {
	cb := signRef(testToken, refCallback, queriesChat, rawQueryID)
	jr := signRef(testToken, refJoinRequestQuery, queriesChat, rawQueryID)
	for name, c := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"callback plain":     {"callback", map[string]any{"callback_ref": cb}, `answerCallbackQuery {"callback_query_id":"` + rawQueryID + `"}`},
		"callback text":      {"callback", map[string]any{"callback_ref": cb, "text": "Done", "show_alert": true}, `answerCallbackQuery {"callback_query_id":"` + rawQueryID + `","text":"Done","show_alert":true}`},
		"callback 200 runes": {"callback", map[string]any{"callback_ref": cb, "text": strings.Repeat("ä", 200)}, `answerCallbackQuery {"callback_query_id":"` + rawQueryID + `","text":"` + strings.Repeat("ä", 200) + `"}`},
		"join decline":       {"join", map[string]any{"join_request_ref": jr, "result": "decline"}, `answerChatJoinRequestQuery {"chat_join_request_query_id":"` + rawQueryID + `","result":"decline"}`},
		"join queue":         {"join", map[string]any{"join_request_ref": jr, "result": "queue"}, `answerChatJoinRequestQuery {"chat_join_request_query_id":"` + rawQueryID + `","result":"queue"}`},
	} {
		t.Run(name, func(t *testing.T) {
			env := newQueryEnv(t)
			got, err := env.run(t, c.tool, c.args, true)
			if err != nil || got.(map[string]any)["answered"] != true {
				t.Fatalf("got %v, %v", got, err)
			}
			if len(env.calls) != 1 || env.calls[0] != c.want {
				t.Fatalf("calls = %q, want %q", env.calls, c.want)
			}
		})
	}
}

func TestQueryAnswersRejectBeforeSecretAccess(t *testing.T) {
	cb := signRef(testToken, refCallback, queriesChat, rawQueryID)
	jr := signRef(testToken, refJoinRequestQuery, queriesChat, rawQueryID)
	cases := map[string]struct {
		tool string
		args map[string]any
	}{
		"raw callback id":        {"callback", map[string]any{"callback_ref": rawQueryID}},
		"file kind":              {"callback", map[string]any{"callback_ref": signRef(testToken, refFile, queriesChat, rawQueryID)}},
		"join kind for callback": {"callback", map[string]any{"callback_ref": jr}},
		"foreign binding":        {"callback", map[string]any{"callback_ref": signRef(testToken, refCallback, "-5", rawQueryID)}},
		"text too long":          {"callback", map[string]any{"callback_ref": cb, "text": strings.Repeat("a", 201)}},
		"raw query id":           {"join", map[string]any{"join_request_ref": rawQueryID, "result": "decline"}},
		"callback kind for join": {"join", map[string]any{"join_request_ref": cb, "result": "decline"}},
		"retired kind":           {"join", map[string]any{"join_request_ref": signRef(testToken, refRetired, queriesChat, "55"), "result": "decline"}},
		"foreign join binding":   {"join", map[string]any{"join_request_ref": signRef(testToken, refJoinRequestQuery, "-5", rawQueryID), "result": "decline"}},
		"approve":                {"join", map[string]any{"join_request_ref": jr, "result": "approve"}},
		"other result":           {"join", map[string]any{"join_request_ref": jr, "result": "Decline"}},
		"empty result":           {"join", map[string]any{"join_request_ref": jr, "result": ""}},
		"unknown field":          {"join", map[string]any{"join_request_ref": jr, "result": "queue", "user_id": 1}},
		"callback unknown field": {"callback", map[string]any{"callback_ref": cb, "url": "https://example.test"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newQueryEnv(t)
			_, err := env.run(t, c.tool, c.args, true)
			if err == nil {
				t.Fatal("accepted")
			}
			if *env.lookups != 0 || len(env.calls) != 0 {
				t.Fatalf("secret lookups = %d, requests = %q", *env.lookups, env.calls)
			}
			if strings.Contains(err.Error(), rawQueryID) || strings.Contains(err.Error(), testToken) {
				t.Fatalf("error leaks: %v", err)
			}
		})
	}
}

func TestQueryAnswersRejectForgedSignatureBeforeRequest(t *testing.T) {
	for _, c := range []struct {
		tool, field string
		ref         string
		extra       map[string]any
	}{
		{"callback", "callback_ref", flipLast(signRef(testToken, refCallback, queriesChat, rawQueryID)), nil},
		{"callback", "callback_ref", signRef("999:other-token", refCallback, queriesChat, rawQueryID), nil},
		{"join", "join_request_ref", flipLast(signRef(testToken, refJoinRequestQuery, queriesChat, rawQueryID)), map[string]any{"result": "decline"}},
		{"join", "join_request_ref", signRef("999:other-token", refJoinRequestQuery, queriesChat, rawQueryID), map[string]any{"result": "queue"}},
	} {
		env := newQueryEnv(t)
		args := map[string]any{c.field: c.ref}
		for k, v := range c.extra {
			args[k] = v
		}
		if _, err := env.run(t, c.tool, args, true); err == nil {
			t.Fatalf("%s accepted a forged reference", c.tool)
		}
		if len(env.calls) != 0 {
			t.Fatalf("%s sent %q", c.tool, env.calls)
		}
	}
}

func TestQueryAnswersSendOneRequestAndReportUncertainty(t *testing.T) {
	cb := signRef(testToken, refCallback, queriesChat, rawQueryID)
	jr := signRef(testToken, refJoinRequestQuery, queriesChat, rawQueryID)
	for _, tool := range []string{"callback", "join"} {
		args := map[string]any{"callback_ref": cb}
		if tool == "join" {
			args = map[string]any{"join_request_ref": jr, "result": "decline"}
		}
		for name, setup := range map[string]func(*queryEnv){
			"500":     func(e *queryEnv) { e.status = 500 },
			"502":     func(e *queryEnv) { e.status = 502 },
			"timeout": func(e *queryEnv) { e.fail = &timeoutError{} },
		} {
			env := newQueryEnv(t)
			setup(env)
			_, err := env.run(t, tool, args, true)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), "SECRETDESC") || len(env.calls) != 1 {
				t.Errorf("%s %s: err = %v, calls = %d", tool, name, err, len(env.calls))
			}
		}
		env := newQueryEnv(t)
		env.status = 400
		if _, err := env.run(t, tool, args, true); err == nil || strings.Contains(err.Error(), "SECRETDESC") || len(env.calls) != 1 {
			t.Errorf("%s 400: err = %v", tool, err)
		}
	}
}
