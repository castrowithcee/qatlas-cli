package makeapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type orgVariablesEnv struct {
	*environment
	calls  []call
	bodies []string
	status int
}

const defaultOrgVariables = `{"organizationVariables":[{"typeId":2,"name":"region","value":"eu","isSystem":false},` +
	`{"typeId":2,"name":"SYS_NAME","value":"x","isSystem":true}]}`

func newOrgVariablesEnv(t *testing.T) *orgVariablesEnv {
	h := &orgVariablesEnv{}
	h.environment = newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		h.bodies = append(h.bodies, string(body))
		path := strings.TrimPrefix(r.URL.EscapedPath(), apiPath)
		base := "/organizations/" + itoa64(ownOrg) + "/variables"
		if !strings.HasPrefix(path, base) {
			t.Fatalf("unexpected request %s %s", r.Method, path)
		}
		if r.Method == http.MethodGet {
			return jsonResponse(200, defaultOrgVariables), nil
		}
		if h.status != 0 {
			return jsonResponse(h.status, `{"message":"`+foreignCanary+`"}`), nil
		}
		if r.Method == http.MethodDelete {
			return jsonResponse(200, `{"ok":1}`), nil
		}
		name := "region"
		if r.Method == http.MethodPost {
			name = "fresh"
		}
		return jsonResponse(200, `{"organizationVariable":{"typeId":2,"name":"`+name+`","value":"v","isSystem":false}}`), nil
	})
	return h
}

func (h *orgVariablesEnv) changing() int {
	n := 0
	for _, c := range h.calls {
		if c.method != http.MethodGet {
			n++
		}
	}
	return n
}

var orgVariableChanges = []struct{ op, conn, args string }{
	{organizationVariablesCreate.ID, "orgmode", `{"name":"fresh","type":"text","value":"v"}`},
	{organizationVariablesUpdate.ID, "orgmode", `{"name":"region","type":"text","value":"v"}`},
	{organizationVariablesDelete.ID, "orgvariablesdelete", `{"name":"region","confirmed":true}`},
}

func TestOrganizationVariablesRefuseTeamModeBeforeSecretAndIO(t *testing.T) {
	for _, c := range orgVariableChanges {
		h := newOrgVariablesEnv(t)
		conn := "open"
		if c.op == organizationVariablesDelete.ID {
			_, handler, _ := registry(t).Lookup(c.op)
			_, err := handler(context.Background(), resolvedConnection("team/"+itoa64(ownTeam)),
				resolver(h.red, h.reads), h.red, []byte(c.args))
			if !isInvalidRequest(err) || len(h.calls) != 0 || *h.reads != 0 {
				t.Fatalf("%s err = %v", c.op, err)
			}
			continue
		}
		if _, err := h.confirmed(c.op, conn, c.args); !isInvalidRequest(err) || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s err = %v, calls = %d, reads = %d", c.op, err, len(h.calls), *h.reads)
		}
	}
	h := newOrgVariablesEnv(t)
	if _, err := h.invoke(organizationVariablesList.ID, "open", `{}`); !isInvalidRequest(err) || len(h.calls) != 0 ||
		*h.reads != 0 {
		t.Fatalf("list err = %v", err)
	}
}

func TestOrganizationVariablesListIsBoundToTheOrganization(t *testing.T) {
	h := newOrgVariablesEnv(t)
	result, err := h.invoke(organizationVariablesList.ID, "orgmode", `{}`)
	if err != nil || !strings.Contains(result, `"custom_count":1`) || !strings.Contains(result, `"system_count":1`) {
		t.Fatalf("list = %s, %v", result, err)
	}
	if len(h.calls) != 1 || h.calls[0].path != apiPath+"/organizations/"+itoa64(ownOrg)+"/variables" {
		t.Fatalf("calls = %+v", h.calls)
	}
	if _, err := h.invoke(organizationVariablesList.ID, "orgmode", `{"organization_id":7}`); err == nil || len(h.calls) != 1 {
		t.Fatalf("an organization argument was accepted: %v", err)
	}
}

func TestOrganizationVariablesChangesNeedConfirmAndSendOneRequest(t *testing.T) {
	want := []struct{ method, path, query, body string }{
		{"POST", "/variables", "", `{"name":"fresh","typeId":2,"value":"v"}`},
		{"PATCH", "/variables/region", "", `{"typeId":2,"value":"v"}`},
		{"DELETE", "/variables/region", "confirmed=true", ""},
	}
	for i, c := range orgVariableChanges {
		h := newOrgVariablesEnv(t)
		if _, err := h.invoke(c.op, c.conn, c.args); !isConfirmationRequired(err) || len(h.calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, calls = %d", c.op, err, len(h.calls))
		}
		if _, err := h.confirmed(c.op, c.conn, c.args); err != nil || h.changing() != 1 {
			t.Fatalf("%s: %v, calls = %+v", c.op, err, h.calls)
		}
		last := h.calls[len(h.calls)-1]
		if last.method != want[i].method || last.path != apiPath+"/organizations/"+itoa64(ownOrg)+want[i].path ||
			last.query.Encode() != want[i].query || h.bodies[len(h.bodies)-1] != want[i].body {
			t.Fatalf("%s sent %+v %q", c.op, last, h.bodies[len(h.bodies)-1])
		}
	}
}

func TestOrganizationVariablesRefuseSystemUnknownAndInvalidInput(t *testing.T) {
	for _, name := range []string{"SYS_NAME", "missing"} {
		for _, c := range orgVariableChanges[1:] {
			h := newOrgVariablesEnv(t)
			_, err := h.confirmed(c.op, c.conn, strings.Replace(c.args, "region", name, 1))
			if !isInvalidRequest(err) || h.changing() != 0 {
				t.Fatalf("%s %s err = %v", c.op, name, err)
			}
		}
	}
	for _, args := range []string{`{"name":"../x","type":"text","value":"v"}`, `{"name":"a","type":"number","value":"5"}`,
		`{"name":"a","type":"text","value":"v","organizationId":1}`} {
		h := newOrgVariablesEnv(t)
		if _, err := h.confirmed(organizationVariablesCreate.ID, "orgmode", args); err == nil || len(h.calls) != 0 {
			t.Fatalf("%s err = %v", args, err)
		}
	}
	for _, args := range []string{`{"name":"region"}`, `{"name":"region","confirmed":false}`, `{"name":"a/b","confirmed":true}`} {
		h := newOrgVariablesEnv(t)
		if _, err := h.confirmed(organizationVariablesDelete.ID, "orgvariablesdelete", args); err == nil || len(h.calls) != 0 {
			t.Fatalf("delete %s err = %v", args, err)
		}
	}
}

func TestOrganizationVariablesDeleteIsOnlyOfferedThroughAToolsListAndInNoProfile(t *testing.T) {
	h := newOrgVariablesEnv(t)
	if _, err := h.confirmed(organizationVariablesDelete.ID, "orgmode", `{"name":"region","confirmed":true}`); err == nil {
		t.Fatal("delete was offered without a tools list")
	}
	if !organizationVariablesDelete.RequiresToolAllowList {
		t.Fatal("delete must require a tools list")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == organizationVariablesDelete.ID {
				t.Fatalf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}

func TestOrganizationVariablesChangesAreNeverRetried(t *testing.T) {
	for name, respond := range map[string]func() (*http.Response, error){
		"5xx":      func() (*http.Response, error) { return jsonResponse(503, `{"message":"`+foreignCanary+`"}`), nil },
		"timeout":  func() (*http.Response, error) { return nil, context.DeadlineExceeded },
		"reset":    func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"unusable": func() (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	} {
		for _, c := range orgVariableChanges {
			if c.op == organizationVariablesDelete.ID && name == "unusable" {
				continue // a delete answer has no body to judge
			}
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return jsonResponse(200, defaultOrgVariables), nil
				}
				return respond()
			})
			_, err := env.confirmed(c.op, c.conn, c.args)
			changes := 0
			for _, k := range calls {
				if k.method != http.MethodGet {
					changes++
				}
			}
			if err == nil || changes != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("%s %s err = %v, changes = %d", name, c.op, err, changes)
			}
		}
	}
	h := newOrgVariablesEnv(t)
	h.status = 403
	if _, err := h.confirmed(organizationVariablesCreate.ID, "orgmode", orgVariableChanges[0].args); err == nil ||
		!strings.Contains(err.Error(), "organization-variables:write") || strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("403 err = %v", err)
	}
}
