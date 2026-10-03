package excalidrawplus

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const logsBody = `{"logs":[{"id":"l1","action":"scene:create","operation":"create","created_at":"2026-01-02T03:04:05Z",` +
	`"user_id":"u1","user_email":"a@example.com","user_full_name":"name-canary","ip_address":"ip-canary",` +
	`"details":"details-canary","user_picture":"pic-canary","status":"ok"}],"hasMore":true,"totalCount":9}`

func logsHandler(calls *[]call) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, logsBody), nil
	}
}

func TestLogsRefusedOnRestrictedConnection(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, logsHandler(&calls))
	for _, name := range []string{"one", "two"} {
		_, err := env.invoke(logsList.ID, name, `{}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), ownCollection) || strings.Contains(err.Error(), otherAllowed) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d, want none", len(calls), *env.reads)
	}
}

func TestLogsQueryAndReducedResult(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, logsHandler(&calls))
	out, err := env.invoke(logsList.ID, "every", `{"user_id":"u1","action":"scene:create",`+
		`"from":"2026-01-01T01:00:00+01:00","to":"2026-02-01T00:00:00Z","offset":10,"limit":20}`)
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
	q := calls[0].query
	if calls[0].path != apiPath+"/logs" || q.Get("user") != "u1" || q.Get("action") != "scene:create" ||
		q.Get("dateFrom") != "2026-01-01T00:00:00Z" || q.Get("dateTo") != "2026-02-01T00:00:00Z" ||
		q.Get("offset") != "10" || q.Get("limit") != "20" || len(q) != 6 {
		t.Fatalf("query = %v", q)
	}
	for _, leak := range []string{"name-canary", "ip-canary", "details-canary", "pic-canary"} {
		if strings.Contains(out, leak) {
			t.Fatalf("result leaks %s: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"next_offset":30`) || !strings.Contains(out, `"user_email":"a@example.com"`) {
		t.Fatalf("result = %s", out)
	}
}

func TestLogsDefaultPagination(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, logsHandler(&calls))
	out, err := env.invoke(logsList.ID, "every", `{}`)
	if err != nil || calls[0].query.Get("limit") != "50" || calls[0].query.Get("offset") != "0" ||
		!strings.Contains(out, `"next_offset":50`) {
		t.Fatalf("err = %v, query = %v, out = %s", err, calls[0].query, out)
	}
}

func TestLogsValidation(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, logsHandler(&calls))
	cases := map[string]string{
		"id":       `{"user_id":"a/b"}`,
		"action":   `{"action":"Scene Create&x=1"}`,
		"swapped":  `{"from":"2026-02-01T00:00:00Z","to":"2026-01-01T00:00:00Z"}`,
		"too long": `{"from":"2025-01-01T00:00:00Z","to":"2026-06-01T00:00:00Z"}`,
		"one end":  `{"from":"2026-01-01T00:00:00Z"}`,
		"format":   `{"from":"2026-01-01","to":"2026-01-02"}`,
	}
	for name, args := range cases {
		if _, err := env.invoke(logsList.ID, "every", args); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %d, want none", len(calls))
	}
}

func TestLogsCapAndNoProviderTextInErrors(t *testing.T) {
	var calls []call
	long := strings.Repeat("x", 5000)
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("offset") == "1" {
			return jsonResponse(500, `{"message":"`+foreignCanary+`"}`), nil
		}
		return jsonResponse(200, `{"logs":[{"id":"1","action":"`+long+`"},{"id":"2","action":"b"}],"hasMore":false}`), nil
	})
	out, err := env.invoke(logsList.ID, "every", `{"limit":1}`)
	if err != nil || strings.Contains(out, long) || strings.Count(out, `"id"`) != 1 {
		t.Fatalf("err = %v, out length = %d", err, len(out))
	}
	_, err = env.invoke(logsList.ID, "every", `{"offset":1}`)
	if err == nil || strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("err = %v", err)
	}
}

func TestLogsRiskAndProfile(t *testing.T) {
	r := logsList.Risk
	if r.Effect != capability.EffectRead || r.Idempotency != capability.IdempotencySafe ||
		r.Confirmation != capability.ConfirmationNone || !r.OpenWorld || r.DataSensitivity != "excalidrawplus-workspace-activity" {
		t.Fatalf("risk = %+v", r)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		if p.ID == "activity" {
			if p.Recommended || len(p.Tools) != 1 || p.Tools[0] != logsList.ID {
				t.Fatalf("profile = %+v", p)
			}
			return
		}
	}
	t.Fatal("no activity profile")
}
