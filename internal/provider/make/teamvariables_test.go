package makeapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

type variablesEnv struct {
	*environment
	calls  []call
	bodies []string
	status int
	list   string
	answer string
}

const defaultVariables = `{"teamVariables":[{"typeId":2,"name":"region","value":"eu","isSystem":false},` +
	`{"typeId":1,"name":"limit","value":5,"isSystem":false},{"typeId":2,"name":"SYS_NAME","value":"x","isSystem":true}]}`

func newVariablesEnv(t *testing.T) *variablesEnv {
	h := &variablesEnv{list: defaultVariables}
	h.environment = newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		h.bodies = append(h.bodies, string(body))
		path := strings.TrimPrefix(r.URL.EscapedPath(), apiPath)
		base := "/teams/" + itoa64(ownTeam) + "/variables"
		if r.Method == http.MethodGet && path == base {
			return jsonResponse(200, h.list), nil
		}
		if !strings.HasPrefix(path, base) {
			t.Fatalf("unexpected request %s %s", r.Method, path)
		}
		if h.status != 0 {
			return jsonResponse(h.status, `{"message":"`+foreignCanary+`"}`), nil
		}
		if h.answer != "" {
			return jsonResponse(200, h.answer), nil
		}
		if r.Method == http.MethodDelete {
			return jsonResponse(200, `{"ok":1}`), nil
		}
		name := "region"
		if r.Method == http.MethodPost {
			name = "fresh"
		}
		return jsonResponse(200, `{"teamVariable":{"typeId":2,"name":"`+name+`","value":"v","isSystem":false}}`), nil
	})
	return h
}

func (h *variablesEnv) changing() int {
	n := 0
	for _, c := range h.calls {
		if c.method != http.MethodGet {
			n++
		}
	}
	return n
}

var variableChanges = []struct{ op, conn, args string }{
	{teamVariablesCreate.ID, "open", `{"name":"fresh","type":"text","value":"v"}`},
	{teamVariablesUpdate.ID, "open", `{"name":"region","type":"text","value":"v"}`},
	{teamVariablesDelete.ID, "teamvariablesdelete", `{"name":"region","confirmed":true}`},
}

func TestTeamVariablesListIsBoundToTheTeamAndCounts(t *testing.T) {
	h := newVariablesEnv(t)
	result, err := h.invoke(teamVariablesList.ID, "open", `{}`)
	if err != nil || !strings.Contains(result, `"custom_count":2`) || !strings.Contains(result, `"system_count":1`) ||
		!strings.Contains(result, `"type":"number"`) || !strings.Contains(result, `"is_system":true`) {
		t.Fatalf("list = %s, %v", result, err)
	}
	if len(h.calls) != 1 || h.calls[0].path != apiPath+"/teams/"+itoa64(ownTeam)+"/variables" {
		t.Fatalf("calls = %+v", h.calls)
	}
	if _, err := h.invoke(teamVariablesList.ID, "open", `{"team_id":7}`); err == nil || len(h.calls) != 1 {
		t.Fatalf("a team argument was accepted: %v", err)
	}
	h.list = `{"teamVariables":[{"typeId":2,"name":"big","value":"` + strings.Repeat("x", 20000) + `","isSystem":false}]}`
	result, err = h.invoke(teamVariablesList.ID, "open", `{}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > 5000 {
		t.Fatalf("capped list = %d bytes, %v", len(result), err)
	}
}

func TestTeamVariablesScenarioConnectionIsRefusedBeforeIO(t *testing.T) {
	for _, op := range []string{teamVariablesList.ID, teamVariablesCreate.ID} {
		h := newVariablesEnv(t)
		args := `{}`
		if op == teamVariablesCreate.ID {
			args = `{"name":"a","type":"text","value":"v"}`
		}
		_, err := h.confirmed(op, "scenario", args)
		if !isInvalidRequest(err) || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s err = %v, calls = %d, reads = %d", op, err, len(h.calls), *h.reads)
		}
	}
}

func TestTeamVariablesChangesNeedConfirmAndSendOneRequest(t *testing.T) {
	want := []struct{ method, path, query, body string }{
		{"POST", "/variables", "", `{"name":"fresh","typeId":2,"value":"v"}`},
		{"PATCH", "/variables/region", "", `{"typeId":2,"value":"v"}`},
		{"DELETE", "/variables/region", "confirmed=true", ""},
	}
	for i, c := range variableChanges {
		h := newVariablesEnv(t)
		if _, err := h.invoke(c.op, c.conn, c.args); !isConfirmationRequired(err) || len(h.calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, calls = %d", c.op, err, len(h.calls))
		}
		if _, err := h.confirmed(c.op, c.conn, c.args); err != nil || h.changing() != 1 {
			t.Fatalf("%s: %v, calls = %+v", c.op, err, h.calls)
		}
		last := h.calls[len(h.calls)-1]
		if last.method != want[i].method || last.path != apiPath+"/teams/"+itoa64(ownTeam)+want[i].path ||
			last.query.Encode() != want[i].query || h.bodies[len(h.bodies)-1] != want[i].body {
			t.Fatalf("%s sent %+v %q", c.op, last, h.bodies[len(h.bodies)-1])
		}
	}
}

func TestTeamVariablesSystemAndUnknownVariablesAreRefusedWithoutChange(t *testing.T) {
	for _, name := range []string{"SYS_NAME", "missing"} {
		for _, c := range variableChanges[1:] {
			h := newVariablesEnv(t)
			args := strings.Replace(c.args, "region", name, 1)
			_, err := h.confirmed(c.op, c.conn, args)
			if !isInvalidRequest(err) || h.changing() != 0 || len(h.calls) != 1 {
				t.Fatalf("%s %s err = %v, calls = %+v", c.op, name, err, h.calls)
			}
		}
	}
}

func TestTeamVariablesInputIsValidatedBeforeAnyRequest(t *testing.T) {
	long := strings.Repeat("a", maxVariableNameLength+1)
	for _, args := range []string{
		`{"name":"a b","type":"text","value":"v"}`,
		`{"name":"../x","type":"text","value":"v"}`,
		`{"name":"` + long + `","type":"text","value":"v"}`,
		`{"name":"a","type":"text","value":5}`,
		`{"name":"a","type":"number","value":"5"}`,
		`{"name":"a","type":"boolean","value":"true"}`,
		`{"name":"a","type":"date","value":"yesterday"}`,
		`{"name":"a","type":"text","value":"` + strings.Repeat("x", maxVariableTextBytes+1) + `"}`,
		`{"name":"a","type":"object","value":"v"}`,
		`{"name":"a","type":"text"}`,
		`{"name":"a","type":"boolean","value":null}`,
		`{"name":"a","type":"text","value":null}`,
		`{"name":"a","type":"text","value":"v","typeId":2}`,
		`{"name":"a","type":"text","value":"v","url":"https://x"}`,
	} {
		for _, op := range []string{teamVariablesCreate.ID, teamVariablesUpdate.ID} {
			h := newVariablesEnv(t)
			if _, err := h.confirmed(op, "open", args); err == nil || len(h.calls) != 0 || *h.reads != 0 {
				t.Fatalf("%s %.60s err = %v, calls = %d", op, args, err, len(h.calls))
			}
		}
	}
	for _, args := range []string{`{"name":"region"}`, `{"name":"region","confirmed":false}`,
		`{"name":"a/b","confirmed":true}`, `{"name":"region","confirmed":true,"all":true}`} {
		h := newVariablesEnv(t)
		if _, err := h.confirmed(teamVariablesDelete.ID, "teamvariablesdelete", args); err == nil || len(h.calls) != 0 {
			t.Fatalf("delete %s err = %v, calls = %d", args, err, len(h.calls))
		}
	}
	for _, args := range []string{`{"name":"a","type":"number","value":1.5}`, `{"name":"a","type":"boolean","value":true}`,
		`{"name":"a","type":"date","value":"2026-10-05"}`, `{"name":"a","type":"date","value":"2026-10-05T10:00:00Z"}`} {
		h := newVariablesEnv(t)
		h.answer = `{"teamVariable":{"typeId":2,"name":"a","value":"v","isSystem":false}}`
		if _, err := h.confirmed(teamVariablesCreate.ID, "open", args); err != nil {
			t.Fatalf("%s err = %v", args, err)
		}
	}
}

func TestTeamVariablesDeleteIsOnlyOfferedThroughAToolsListAndInNoProfile(t *testing.T) {
	h := newVariablesEnv(t)
	if _, err := h.confirmed(teamVariablesDelete.ID, "open", `{"name":"region","confirmed":true}`); err == nil {
		t.Fatal("teamvariables.delete was offered without a tools list")
	}
	if !teamVariablesDelete.RequiresToolAllowList {
		t.Fatal("teamvariables.delete must require a tools list")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == teamVariablesDelete.ID {
				t.Fatalf("profile %s contains %s", profile.ID, tool)
			}
		}
		if (profile.ID == "teamvariables-read" || profile.ID == "teamvariables-manage") && profile.Recommended {
			t.Fatalf("profile %s is recommended", profile.ID)
		}
	}
}

func TestTeamVariablesChangesAreNeverRetriedAndNameTheScope(t *testing.T) {
	for _, status := range []int{500, 403} {
		for _, c := range variableChanges {
			h := newVariablesEnv(t)
			h.status = status
			_, err := h.confirmed(c.op, c.conn, c.args)
			if err == nil || h.changing() != 1 || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("%s/%d err = %v, changing = %d", c.op, status, err, h.changing())
			}
			if status == 500 && !strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("%s lacks the uncertain note: %v", c.op, err)
			}
			if status == 403 && !strings.Contains(err.Error(), "team-variables:write") {
				t.Fatalf("%s err = %v, want the scope hint", c.op, err)
			}
		}
	}
	for name, failure := range map[string]func() (*http.Response, error){
		"aborted": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbled": func() (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	} {
		var calls []call
		mutations := 0
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return jsonResponse(200, defaultVariables), nil
			}
			mutations++
			return failure()
		})
		_, err := env.confirmed(teamVariablesUpdate.ID, "open", `{"name":"region","type":"text","value":"v"}`)
		if err == nil || mutations != 1 || !strings.Contains(err.Error(), "may have taken effect") {
			t.Fatalf("%s err = %v, mutations = %d", name, err, mutations)
		}
	}
}

func TestTeamVariablesReadNamesTheScopeOn403(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(403, `{}`), nil })
	_, err := env.invoke(teamVariablesList.ID, "open", `{}`)
	if err == nil || !strings.Contains(err.Error(), "team-variables:read") {
		t.Fatalf("err = %v", err)
	}
}

func TestTeamVariablesAnswersAreChecked(t *testing.T) {
	for _, answer := range []string{`{"teamVariable":{"typeId":2,"name":"other","value":"v","isSystem":false}}`,
		`{"teamVariable":{"typeId":2,"name":"fresh","value":"v","isSystem":true}}`, `{}`} {
		h := newVariablesEnv(t)
		h.answer = answer
		_, err := h.confirmed(teamVariablesCreate.ID, "open", `{"name":"fresh","type":"text","value":"v"}`)
		if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "may have taken effect") {
			t.Fatalf("%s err = %v", answer, err)
		}
	}
}

func TestTeamVariableToolsDeclareACompleteRisk(t *testing.T) {
	for _, d := range []capability.Descriptor{teamVariablesList, teamVariablesCreate, teamVariablesUpdate, teamVariablesDelete} {
		r := d.Risk
		if r.Effect == "" || r.Idempotency == "" || !r.OpenWorld || r.DataSensitivity == "" {
			t.Fatalf("%s has an incomplete risk: %+v", d.ID, r)
		}
		if d.ID != teamVariablesList.ID && r.Confirmation != capability.ConfirmationRequired {
			t.Fatalf("%s needs confirmation: %+v", d.ID, r)
		}
	}
}
