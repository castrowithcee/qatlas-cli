package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const subscriberFixture = `{"id":5,"login":"alice","email":"a@example.test","is_admin":false}`

func subscriptionServer(p *product, status *int) func(call) *http.Response {
	issues := issueServer(p, "open", nil)
	return func(c call) *http.Response {
		switch {
		case strings.HasSuffix(c.path, "/subscriptions/check"):
			return jsonResponse(200, `{"subscribed":true,"ignored":false,"reason":"manual","created_at":"2026-01-01T00:00:00Z",`+
				`"url":"https://forge.test/x","repository_url":"https://forge.test/acme/widget"}`)
		case strings.HasSuffix(c.path, "/subscriptions"):
			return jsonResponse(200, `[`+subscriberFixture+`,null]`, "X-Total-Count", "5")
		case strings.Contains(c.path, "/subscriptions/"):
			return jsonResponse(*status, "")
		}
		return issues(c)
	}
}

func TestSubscriptionReads(t *testing.T) {
	for _, p := range products {
		status := 201
		e := newEnvironment(t, p, subscriptionServer(p, &status))
		result, err := e.invoke(t, issueSubscriptionsList, "repo", `{"number":7,"limit":2}`)
		users, _ := result["users"].([]any)
		if err != nil || len(users) != 1 || result["has_more"] != true || result["next_cursor"] == nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
			"GET /api/v1/repos/acme/widget/issues/7/subscriptions"}) || (*e.calls)[1].query != "limit=2&page=1" {
			t.Errorf("%s calls = %v", p.id, got)
		}
		user, _ := users[0].(map[string]any)
		if user["login"] != "alice" {
			t.Errorf("%s user = %v", p.id, user)
		}
		result, err = e.invoke(t, issueSubscriptionsCheck, "repo", `{"number":7}`)
		if err != nil || result["subscribed"] != true || result["reason"] != "manual" || result["ignored"] != false {
			t.Errorf("%s check = %v, %v", p.id, result, err)
		}
	}
}

func TestSubscriptionChangeReportsTheStatus(t *testing.T) {
	cases := []struct {
		tool   *tool
		method string
		key    string
	}{{issueSubscriptionsAdd, "PUT", "added"}, {issueSubscriptionsRemove, "DELETE", "removed"}}
	for _, p := range products {
		for _, c := range cases {
			status := 201
			e := newEnvironment(t, p, subscriptionServer(p, &status))
			if _, err := e.invokeAs(t, c.tool, "repo", `{"number":7,"user":"alice"}`, false); err == nil || len(*e.calls) != 0 {
				t.Fatalf("%s: unconfirmed %s: %v", p.id, c.tool.action, err)
			}
			result, err := e.invoke(t, c.tool, "repo", `{"number":7,"user":"alice"}`)
			if err != nil || result[c.key] != true || result["user"] != "alice" {
				t.Fatalf("%s %s: %v, %v", p.id, c.tool.action, result, err)
			}
			want := []string{"GET /api/v1/repos/acme/widget/issues/7",
				c.method + " /api/v1/repos/acme/widget/issues/7/subscriptions/alice"}
			if got := methods(e); !reflect.DeepEqual(got, want) {
				t.Errorf("%s calls = %v", p.id, got)
			}
			status = 200
			if result, err = e.invoke(t, c.tool, "repo", `{"number":8,"user":"alice"}`); err != nil || result[c.key] != false {
				t.Errorf("%s %s existing: %v, %v", p.id, c.tool.action, result, err)
			}
			status = 304
			if _, err = e.invoke(t, c.tool, "repo", `{"number":7,"user":"bob"}`); err == nil ||
				!strings.Contains(err.Error(), "administrator") {
				t.Errorf("%s %s 304: %v", p.id, c.tool.action, err)
			}
		}
	}
}

func TestSubscriptionRefusalsHappenBeforeIO(t *testing.T) {
	for _, p := range products {
		status := 201
		e := newEnvironment(t, p, subscriptionServer(p, &status))
		reads := *e.reads
		for _, tl := range []*tool{issueSubscriptionsList, issueSubscriptionsCheck, issueSubscriptionsAdd, issueSubscriptionsRemove} {
			for _, bad := range []string{`{"repository":"other/place","number":7,"user":"alice"}`, `{"number":0,"user":"alice"}`,
				`{"number":7,"user":"a/b"}`, `{"number":7,"user":"../x"}`, `{"number":7,"user":""}`} {
				needsUser := tl == issueSubscriptionsAdd || tl == issueSubscriptionsRemove
				if !needsUser && strings.Contains(bad, `"user"`) && !strings.Contains(bad, "other") && !strings.Contains(bad, `"number":0`) {
					continue
				}
				if _, err := e.invoke(t, tl, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != 0 ||
					*e.reads != reads || strings.Contains(err.Error(), "other") {
					t.Errorf("%s %s %s: %v", p.id, tl.action, bad, err)
				}
			}
		}
		// An index whose canonical repository lies outside the targets gets no change.
		for _, tl := range []*tool{issueSubscriptionsAdd, issueSubscriptionsRemove} {
			if _, err := e.invoke(t, tl, "repo", `{"number":9,"user":"alice"}`); err == nil || strings.Contains(err.Error(), "place") {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
		for _, c := range *e.calls {
			if c.method != http.MethodGet {
				t.Errorf("%s: change request %s %s", p.id, c.method, c.path)
			}
		}
	}
}

func TestSubscriptionChangeIsNotRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		status := 502
		e := newEnvironment(t, p, subscriptionServer(p, &status))
		for _, tl := range []*tool{issueSubscriptionsAdd, issueSubscriptionsRemove} {
			before := len(*e.calls)
			_, err := e.invoke(t, tl, "repo", `{"number":7,"user":"alice"}`)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls)-before != 2 {
				t.Errorf("%s %s: %v, %d calls", p.id, tl.action, err, len(*e.calls)-before)
			}
		}
	}
}

func TestSubscriptionChangesAreNotListedOnly(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{issueSubscriptionsAdd, issueSubscriptionsRemove} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Errorf("%s descriptor = %+v", p.toolID(tl), d)
			}
		}
	}
}
