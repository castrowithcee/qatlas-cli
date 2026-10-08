package makeapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// hookChangeEnv answers hook reads with a hook of the given team and enabled state, records the bodies of
// every changing request, and answers the changing requests with the documented shapes.
type hookChangeEnv struct {
	*environment
	calls   []call
	bodies  map[string]string
	enabled bool
	refuse  bool
}

func newHookChangeEnv(t *testing.T, teamID, scenarioID int64) *hookChangeEnv {
	h := &hookChangeEnv{bodies: map[string]string{}}
	id := strconv.FormatInt(hookID, 10)
	h.environment = newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		h.bodies[r.Method+" "+r.URL.Path] = string(body)
		hook := func(team int64, name string) string {
			j := hookJSONOf(hookID, team, scenarioID, name)
			return strings.Replace(j, `"enabled":true`, `"enabled":`+strconv.FormatBool(h.enabled), 1)
		}
		switch {
		case r.Method == http.MethodGet:
			return jsonResponse(200, `{"hook":`+hook(teamID, "Hook")+`}`), nil
		case r.Method == http.MethodPatch:
			return jsonResponse(200, `{"hook":`+hook(ownTeam, "Renamed "+canaryHookURL)+`}`), nil
		case strings.HasSuffix(r.URL.Path, "/enable"):
			h.enabled = true
			return jsonResponse(200, `{"success":true}`), nil
		case strings.HasSuffix(r.URL.Path, "/disable"):
			h.enabled = false
			return jsonResponse(200, `{"success":true}`), nil
		case r.Method == http.MethodDelete:
			if h.refuse && r.URL.Query().Get("confirmed") != "true" {
				return jsonResponse(400, `{"message":"`+foreignCanary+`","detail":{"scenarios":[`+
					`{"id":10,"name":"Uses `+canaryHookURL+`"},{"id":0},{"id":11,"name":"Other"}]}}`), nil
			}
			return jsonResponse(200, `{"hook":`+id+`}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	return h
}

func (h *hookChangeEnv) changing() []call {
	var out []call
	for _, c := range h.calls {
		if c.method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func TestHookChangesSendDocumentedRequests(t *testing.T) {
	id := strconv.FormatInt(hookID, 10)
	cases := []struct {
		op, conn, args, method, path, body string
	}{
		{hooksRename.ID, "open", `{"hook_id":` + id + `,"name":"New"}`, "PATCH", "/hooks/" + id, `{"name":"New"}`},
		{hooksEnable.ID, "open", `{"hook_id":` + id + `}`, "POST", "/hooks/" + id + "/enable", ``},
		{hooksDisable.ID, "open", `{"hook_id":` + id + `}`, "POST", "/hooks/" + id + "/disable", ``},
		{hooksDelete.ID, "hookdelete", `{"hook_id":` + id + `}`, "DELETE", "/hooks/" + id, ``},
	}
	for _, c := range cases {
		h := newHookChangeEnv(t, ownTeam, ownScenario)
		h.enabled = c.op == hooksDisable.ID
		if _, err := h.confirmed(c.op, c.conn, c.args); err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		ch := h.changing()
		if len(ch) != 1 || ch[0].method != c.method || ch[0].path != apiPath+c.path {
			t.Fatalf("%s changing calls = %+v", c.op, ch)
		}
		if ch[0].query.Get("confirmed") != "" {
			t.Fatalf("%s sent confirmed without being asked", c.op)
		}
		if got := h.bodies[c.method+" "+apiPath+c.path]; got != c.body && !jsonEqual(got, c.body) {
			t.Fatalf("%s body = %q, want %q", c.op, got, c.body)
		}
	}
}

func jsonEqual(a, b string) bool {
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil &&
		string(mustJSON(x)) == string(mustJSON(y))
}

func mustJSON(v any) []byte { out, _ := json.Marshal(v); return out }

func TestHookChangesRequireConfirmationAndSendNothingWithout(t *testing.T) {
	id := strconv.FormatInt(hookID, 10)
	for _, c := range []struct{ op, conn, args string }{
		{hooksRename.ID, "open", `{"hook_id":` + id + `,"name":"A"}`},
		{hooksEnable.ID, "open", `{"hook_id":` + id + `}`},
		{hooksDisable.ID, "open", `{"hook_id":` + id + `}`},
		{hooksDelete.ID, "hookdelete", `{"hook_id":` + id + `}`},
	} {
		h := newHookChangeEnv(t, ownTeam, ownScenario)
		_, err := h.invoke(c.op, c.conn, c.args)
		if !isConfirmationRequired(err) || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s err = %v, calls = %d, reads = %d", c.op, err, len(h.calls), *h.reads)
		}
	}
}

func TestHookMutationsRefuseForeignTeamBeforeTheMutation(t *testing.T) {
	id := strconv.FormatInt(hookID, 10)
	for _, c := range []struct{ op, conn, args string }{
		{hooksRename.ID, "open", `{"hook_id":` + id + `,"name":"A"}`},
		{hooksEnable.ID, "open", `{"hook_id":` + id + `}`},
		{hooksDisable.ID, "open", `{"hook_id":` + id + `}`},
		{hooksDelete.ID, "hookdelete", `{"hook_id":` + id + `}`},
	} {
		h := newHookChangeEnv(t, foreignTeam, ownScenario)
		_, err := h.confirmed(c.op, c.conn, c.args)
		if !isInvalidRequest(err) || len(h.changing()) != 0 || len(h.calls) != 1 {
			t.Fatalf("%s err = %v, calls = %+v", c.op, err, h.calls)
		}
		if strings.Contains(err.Error(), strconv.FormatInt(foreignTeam, 10)) {
			t.Fatalf("%s named the foreign team", c.op)
		}
	}
	// A hook outside a scenario allow-list is refused as well.
	h := newHookChangeEnv(t, ownTeam, foreignScenario)
	if _, err := h.confirmed(hooksEnable.ID, "scenario", `{"hook_id":`+id+`}`); !isInvalidRequest(err) ||
		len(h.changing()) != 0 {
		t.Fatalf("allow-list err = %v", err)
	}
}

func TestHooksDeleteConfirmationAndScenarioConflict(t *testing.T) {
	id := strconv.FormatInt(hookID, 10)
	h := newHookChangeEnv(t, ownTeam, ownScenario)
	h.refuse = true
	result, err := h.confirmed(hooksDelete.ID, "hookdelete", `{"hook_id":`+id+`}`)
	if err != nil {
		t.Fatalf("conflict err = %v", err)
	}
	var got HookDeletion
	if err := json.Unmarshal([]byte(result), &got); err != nil || got.Deleted || !got.ConfirmationRequired ||
		len(got.Scenarios) != 2 || got.Scenarios[0].ID != 10 || got.Scenarios[1].ID != 11 {
		t.Fatalf("result = %s, %v", result, err)
	}
	for _, leak := range []string{canaryHookURL, foreignCanary} {
		if strings.Contains(result, leak) {
			t.Fatalf("result leaked %q: %s", leak, result)
		}
	}
	if ch := h.changing(); len(ch) != 1 || ch[0].query.Get("confirmed") != "" {
		t.Fatalf("changing = %+v, want one DELETE without confirmed and no retry", ch)
	}
	h.calls = nil
	result, err = h.confirmed(hooksDelete.ID, "hookdelete", `{"hook_id":`+id+`,"confirm_scenarios_affected":true}`)
	ch := h.changing()
	if err != nil || !strings.Contains(result, `"deleted":true`) || len(ch) != 1 ||
		ch[0].query.Get("confirmed") != "true" {
		t.Fatalf("confirmed delete = %s, %v, %+v", result, err, ch)
	}
	// Offered only through a tools list, in no profile.
	if _, err := h.confirmed(hooksDelete.ID, "open", `{"hook_id":`+id+`}`); err == nil {
		t.Fatal("hooks.delete was offered without a tools list")
	}
	if !hooksDelete.RequiresToolAllowList {
		t.Fatal("hooks.delete must require a tools list")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == hooksDelete.ID {
				t.Fatalf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}

func TestHookChangesAreNeverRetriedAndNameTheScope(t *testing.T) {
	id := strconv.FormatInt(hookID, 10)
	for _, status := range []int{500, 403} {
		for _, c := range []struct{ op, conn, args string }{
			{hooksRename.ID, "open", `{"hook_id":` + id + `,"name":"A"}`},
			{hooksEnable.ID, "open", `{"hook_id":` + id + `}`},
			{hooksDelete.ID, "hookdelete", `{"hook_id":` + id + `}`},
		} {
			var calls []call
			mutations := 0
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return jsonResponse(200, `{"hook":`+hookJSONOf(hookID, ownTeam, ownScenario, "H")+`}`), nil
				}
				mutations++
				return jsonResponse(status, `{"message":"`+foreignCanary+`"}`), nil
			})
			_, err := env.confirmed(c.op, c.conn, c.args)
			if err == nil || mutations != 1 || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("%s %d err = %v, mutations = %d", c.op, status, err, mutations)
			}
			if status == 500 && !strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("%s err = %v, want uncertain", c.op, err)
			}
			var perr *provider.Error
			if status == 403 && (!errors.As(err, &perr) || perr.Class != provider.ClassPermission ||
				!strings.Contains(perr.Message, "hooks:write")) {
				t.Fatalf("%s err = %v, want the hooks:write hint", c.op, err)
			}
		}
	}
}

func TestHookChangesReportUnclearAnswersAsUncertainWithoutRetry(t *testing.T) {
	id := strconv.FormatInt(hookID, 10)
	for name, answer := range map[string]func() (*http.Response, error){
		"abort":      func() (*http.Response, error) { return nil, errUnclearTransport{} },
		"unreadable": func() (*http.Response, error) { return jsonResponse(200, `{not json`), nil },
	} {
		var calls []call
		mutations := 0
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return jsonResponse(200, `{"hook":`+hookJSONOf(hookID, ownTeam, ownScenario, "H")+`}`), nil
			}
			mutations++
			return answer()
		})
		_, err := env.confirmed(hooksDisable.ID, "open", `{"hook_id":`+id+`}`)
		if err == nil || mutations != 1 || !strings.Contains(err.Error(), "may have taken effect") {
			t.Fatalf("%s err = %v, mutations = %d", name, err, mutations)
		}
	}
}

func TestHooksEnableReportsAStateThatDidNotChange(t *testing.T) {
	h := newHookChangeEnv(t, ownTeam, ownScenario)
	id := strconv.FormatInt(hookID, 10)
	result, err := h.confirmed(hooksEnable.ID, "open", `{"hook_id":`+id+`}`)
	if err != nil || !strings.Contains(result, `"enabled":true`) || strings.Contains(result, canaryHookURL) {
		t.Fatalf("enable = %s, %v", result, err)
	}
}

func TestHooksManageProfileHasNoDelete(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if profile.ID != "hooks-manage" {
			continue
		}
		have := strings.Join(profile.Tools, ",")
		for _, want := range []string{hooksRename.ID, hooksEnable.ID, hooksDisable.ID} {
			if !strings.Contains(have, want) {
				t.Fatalf("hooks-manage lacks %s", want)
			}
		}
		return
	}
	t.Fatal("hooks-manage profile missing")
}
