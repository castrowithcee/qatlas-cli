package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func lockServer(p *product, v string, change func(call) *http.Response) func(call) *http.Response {
	return giteaOrForgejo(p, v, issueServer(p, "open", change))
}

func TestLockSendsTheReasonUnderTheNameOfTheProduct(t *testing.T) {
	for _, p := range products {
		field := map[string]string{"forgejo": "reason", "gitea": "lock_reason"}[p.id]
		other := map[string]string{"forgejo": "lock_reason", "gitea": "reason"}[p.id]
		e := newEnvironment(t, p, lockServer(p, "17.0.0", func(call) *http.Response { return jsonResponse(204, "") }))
		if _, err := e.invokeAs(t, issueLocksLock, "repo", `{"number":7}`, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed lock: %v", p.id, err)
		}
		result, err := e.invoke(t, issueLocksLock, "repo", `{"number":7,"reason":"resolved"}`)
		if err != nil || result["locked"] != true || result["number"] != float64(7) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := []string{"GET /api/v1/repos/acme/widget/issues/7", "PUT /api/v1/repos/acme/widget/issues/7/lock"}
		if got := methods(e); !reflect.DeepEqual(got[len(got)-2:], want) {
			t.Errorf("%s calls = %v, want %v", p.id, got, want)
		}
		body := bodyOf(t, (*e.calls)[len(*e.calls)-1])
		if body[field] != "resolved" || body[other] != nil || len(body) != 1 {
			t.Errorf("%s body = %v, want only %s", p.id, body, field)
		}
	}
}

func TestUnlockSendsNoBody(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, lockServer(p, "17.0.0", func(call) *http.Response { return jsonResponse(204, "") }))
		result, err := e.invoke(t, issueLocksUnlock, "repo", `{"number":7}`)
		if err != nil || result["unlocked"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodDelete || last.path != "/api/v1/repos/acme/widget/issues/7/lock" || last.body != "" {
			t.Errorf("%s last call = %+v", p.id, last)
		}
	}
}

func TestLockIsRefusedBeforeTheRequestOnOldForgejo(t *testing.T) {
	for _, v := range []string{"15.0.10", "16.0.6"} {
		for _, tl := range []*tool{issueLocksLock, issueLocksUnlock} {
			e := newEnvironment(t, forgejo, lockServer(forgejo, v, func(call) *http.Response { return jsonResponse(204, "") }))
			_, err := e.invoke(t, tl, "repo", `{"number":7}`)
			if err == nil || !strings.Contains(err.Error(), "17.0.0") || strings.Contains(err.Error(), v) {
				t.Errorf("forgejo %s %s: %v", v, tl.action, err)
			}
			for _, c := range *e.calls {
				if strings.Contains(c.path, "/issues/") {
					t.Errorf("forgejo %s %s: request %s %s", v, tl.action, c.method, c.path)
				}
			}
		}
	}
	for _, v := range []string{"1.27.3", "28.2.0"} {
		e := newEnvironment(t, gitea, lockServer(gitea, v, func(call) *http.Response { return jsonResponse(204, "") }))
		if _, err := e.invoke(t, issueLocksUnlock, "repo", `{"number":7}`); err != nil {
			t.Errorf("gitea %s: %v", v, err)
		}
	}
}

func TestLockRefusals(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, lockServer(p, "17.0.0", func(call) *http.Response { return jsonResponse(204, "") }))
		reads := *e.reads
		for _, tl := range []*tool{issueLocksLock, issueLocksUnlock} {
			for _, bad := range []string{`{"repository":"other/place","number":7}`, `{"number":0}`, `{"number":7,"x":1}`} {
				if _, err := e.invoke(t, tl, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != 0 ||
					*e.reads != reads || strings.Contains(err.Error(), "other") {
					t.Errorf("%s %s %s: %v", p.id, tl.action, bad, err)
				}
			}
		}
		for _, bad := range []string{`{"number":7,"reason":"a\nb"}`, `{"number":7,"reason":"` + strings.Repeat("x", 256) + `"}`} {
			if _, err := e.invoke(t, issueLocksLock, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != 0 ||
				*e.reads != reads {
				t.Errorf("%s reason %q: %v", p.id, bad, err)
			}
		}
		if _, err := e.invoke(t, issueLocksUnlock, "repo", `{"number":7,"reason":"x"}`); !isInvalidRequest(err) ||
			len(*e.calls) != 0 {
			t.Errorf("%s unlock with reason: %v", p.id, err)
		}
		// An index whose canonical repository lies outside the targets gets no change.
		for _, tl := range []*tool{issueLocksLock, issueLocksUnlock} {
			if _, err := e.invoke(t, tl, "repo", `{"number":9}`); err == nil || strings.Contains(err.Error(), "place") {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
		for _, c := range *e.calls {
			if c.method != http.MethodGet && !strings.HasSuffix(c.path, "/version") {
				t.Errorf("%s: change request %s %s", p.id, c.method, c.path)
			}
		}
	}
}

func TestLockIsNotRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{issueLocksLock, issueLocksUnlock} {
			e := newEnvironment(t, p, lockServer(p, "17.0.0", func(call) *http.Response { return jsonResponse(502, "bad gateway") }))
			_, err := e.invoke(t, tl, "repo", `{"number":7}`)
			changes := 0
			for _, c := range *e.calls {
				if c.method != http.MethodGet {
					changes++
				}
			}
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || changes != 1 {
				t.Errorf("%s %s: %v, %d changes", p.id, tl.action, err, changes)
			}
		}
	}
}
