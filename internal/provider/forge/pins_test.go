package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func pinServer(p *product, change func(call) *http.Response) func(call) *http.Response {
	issues := issueServer(p, "open", change)
	return func(c call) *http.Response {
		switch {
		case c.path == "/api/v1/repos/acme/widget/issues/pinned":
			return jsonResponse(200, `[`+widgetIssue(p, 7, "open")+`,`+issueFixture(p, 8, "open", true, "acme/widget")+
				`,`+issueFixture(p, 9, "open", false, "other/place")+`]`)
		case c.path == "/api/v1/repos/acme/widget/new_pin_allowed":
			return jsonResponse(200, `{"issues":true,"pull_requests":false}`)
		}
		return issues(c)
	}
}

func TestPinReads(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, pinServer(p, nil))
		result, err := e.invoke(t, issuePinsList, "repo", `{}`)
		issues, _ := result["issues"].([]any)
		if err != nil || len(issues) != 1 || result["repository"] != "acme/widget" || result["truncated"] != nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, issuePinsCheckAllowed, "repo", `{"repository":"acme/widget"}`)
		if err != nil || !reflect.DeepEqual(result, map[string]any{"issues": true, "pull_requests": false}) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		for _, tl := range []*tool{issuePinsList, issuePinsCheckAllowed} {
			before := len(*e.calls)
			reads := *e.reads
			if _, err := e.invoke(t, tl, "repo", `{"repository":"other/place"}`); !isInvalidRequest(err) ||
				len(*e.calls) != before || *e.reads != reads || strings.Contains(err.Error(), "other") {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
	}
}

func TestPinChangeReadsFirstAndSendsOneRequest(t *testing.T) {
	cases := []struct {
		tool   *tool
		args   string
		method string
		path   string
		key    string
	}{
		{issuePinsPin, `{"number":7}`, "POST", "/pin", "pinned"},
		{issuePinsUnpin, `{"number":7}`, "DELETE", "/pin", "unpinned"},
		{issuePinsMove, `{"number":7,"position":2}`, "PATCH", "/pin/2", "moved"},
	}
	for _, p := range products {
		for _, c := range cases {
			e := newEnvironment(t, p, pinServer(p, func(call) *http.Response { return jsonResponse(204, "") }))
			if _, err := e.invokeAs(t, c.tool, "repo", c.args, false); err == nil || len(*e.calls) != 0 {
				t.Fatalf("%s: unconfirmed %s: %v", p.id, c.tool.action, err)
			}
			result, err := e.invoke(t, c.tool, "repo", c.args)
			if err != nil || result[c.key] != true {
				t.Fatalf("%s %s: %v, %v", p.id, c.tool.action, result, err)
			}
			want := []string{"GET /api/v1/repos/acme/widget/issues/7",
				c.method + " /api/v1/repos/acme/widget/issues/7" + c.path}
			if got := methods(e); !reflect.DeepEqual(got, want) {
				t.Errorf("%s calls = %v, want %v", p.id, got, want)
			}
		}
	}
}

func TestPinChangeRefusals(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, pinServer(p, func(call) *http.Response { return jsonResponse(204, "") }))
		reads := *e.reads
		for _, tl := range []*tool{issuePinsPin, issuePinsUnpin, issuePinsMove} {
			for _, bad := range []string{`{"repository":"other/place","number":7}`, `{"number":0}`} {
				if tl == issuePinsMove {
					bad = strings.TrimSuffix(bad, "}") + `,"position":1}`
				}
				if _, err := e.invoke(t, tl, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != 0 ||
					*e.reads != reads || strings.Contains(err.Error(), "other") {
					t.Errorf("%s %s %s: %v", p.id, tl.action, bad, err)
				}
			}
		}
		for _, bad := range []string{`{"number":7}`, `{"number":7,"position":0}`, `{"number":7,"position":-1}`,
			`{"number":7,"position":1.5}`, `{"number":7,"position":"1"}`} {
			if _, err := e.invoke(t, issuePinsMove, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != 0 ||
				*e.reads != reads {
				t.Errorf("%s position %s: %v", p.id, bad, err)
			}
		}
		// An index whose canonical repository lies outside the targets gets no change.
		for _, tl := range []*tool{issuePinsPin, issuePinsUnpin, issuePinsMove} {
			args := `{"number":9}`
			if tl == issuePinsMove {
				args = `{"number":9,"position":1}`
			}
			if _, err := e.invoke(t, tl, "repo", args); err == nil || strings.Contains(err.Error(), "place") {
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

func TestPinChangeIsNotRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{issuePinsPin, issuePinsUnpin, issuePinsMove} {
			e := newEnvironment(t, p, pinServer(p, func(call) *http.Response { return jsonResponse(502, "bad gateway") }))
			args := `{"number":7}`
			if tl == issuePinsMove {
				args = `{"number":7,"position":1}`
			}
			_, err := e.invoke(t, tl, "repo", args)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 2 {
				t.Errorf("%s %s: %v, %d calls", p.id, tl.action, err, len(*e.calls))
			}
		}
	}
}
