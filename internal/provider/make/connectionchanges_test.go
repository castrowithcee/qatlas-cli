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

type connChangeEnv struct {
	*environment
	calls   []call
	bodies  map[string]string
	team    int64
	members string
	refuse  bool
	name    string
	// teamRoleStatus answers the team-membership read; a user other than 8 and 7 is no member.
	nonMember bool
}

func newConnChangeEnv(t *testing.T, teamID int64) *connChangeEnv {
	h := &connChangeEnv{bodies: map[string]string{}, team: teamID, name: "Conn",
		members: `[{"membershipType":"user","membershipId":7,"role":"entity:entity-member","name":"N","email":"` +
			canaryConnSecret + `"},{"membershipType":"group","membershipId":9,"role":"entity:entity-admin"}]`}
	id := strconv.FormatInt(connectionID, 10)
	h.environment = newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		h.bodies[r.Method+" "+r.URL.Path] = string(body)
		access := apiPath + "/teams/" + itoa64(ownTeam) + "/connections/" + id + "/access-list"
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, apiPath+"/users/"):
			if h.nonMember {
				return jsonResponse(404, `{"message":"`+foreignCanary+`"}`), nil
			}
			parts := strings.Split(r.URL.Path, "/")
			return jsonResponse(200, `{"userTeamRole":{"userId":`+parts[len(parts)-3]+`,"teamId":`+
				parts[len(parts)-1]+`}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == access:
			return jsonResponse(200, `{"accessList":`+h.members+`}`), nil
		case r.Method == http.MethodGet:
			return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, h.team, h.name)+`}`), nil
		case r.Method == http.MethodPatch && r.URL.Path == apiPath+"/connections/"+id:
			h.name = "Renamed"
			return jsonResponse(200, `{"connection":{}}`), nil
		case r.Method == http.MethodDelete:
			if h.refuse && r.URL.Query().Get("confirmed") != "true" {
				return jsonResponse(400, `{"message":"`+foreignCanary+`","detail":{"scenarios":[`+
					`{"id":10,"name":"Uses it"},{"id":0},{"id":11,"name":"Other"}]}}`), nil
			}
			return jsonResponse(200, `{"connection":`+id+`}`), nil
		case r.Method == http.MethodPost && r.URL.Path == access+"/users":
			return jsonResponse(200, `{"member":{"membershipType":"user","membershipId":8,"role":"entity:entity-admin",`+
				`"email":"`+canaryConnSecret+`"}}`), nil
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, access+"/users/"):
			return jsonResponse(200, `{"member":{"membershipType":"user","membershipId":7,"role":"entity:entity-admin"}}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	return h
}

func (h *connChangeEnv) changing() []call {
	var out []call
	for _, c := range h.calls {
		if c.method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func TestConnectionChangesSendDocumentedRequests(t *testing.T) {
	id := strconv.FormatInt(connectionID, 10)
	access := "/teams/" + itoa64(ownTeam) + "/connections/" + id + "/access-list"
	cases := []struct {
		op, conn, args, method, path, body string
	}{
		{connectionsRename.ID, "open", `{"connection_id":` + id + `,"name":"New"}`, "PATCH", "/connections/" + id,
			`{"name":"New"}`},
		{connectionsDelete.ID, "connectiondelete", `{"connection_id":` + id + `}`, "DELETE", "/connections/" + id, ``},
		{connectionsAccessSet.ID, "open", `{"connection_id":` + id + `,"user_id":8,"role":"admin"}`, "POST",
			access + "/users", `{"role":"entity:entity-admin","userId":8}`},
		{connectionsAccessSet.ID, "open", `{"connection_id":` + id + `,"user_id":7,"role":"admin"}`, "PATCH",
			access + "/users/7", `{"role":"entity:entity-admin"}`},
	}
	for _, c := range cases {
		h := newConnChangeEnv(t, ownTeam)
		if _, err := h.confirmed(c.op, c.conn, c.args); err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		ch := h.changing()
		if len(ch) != 1 || ch[0].method != c.method || ch[0].path != apiPath+c.path ||
			h.bodies[c.method+" "+apiPath+c.path] != c.body {
			t.Fatalf("%s changing = %+v body %q", c.op, ch, h.bodies[c.method+" "+apiPath+c.path])
		}
		if h.calls[0].method != http.MethodGet || h.calls[0].path != apiPath+"/connections/"+id {
			t.Fatalf("%s did not bind the connection first: %+v", c.op, h.calls)
		}
	}
}

func TestConnectionChangesNeedConfirmationAndRefuseForeignTeam(t *testing.T) {
	id := strconv.FormatInt(connectionID, 10)
	for _, c := range []struct{ op, conn, args string }{
		{connectionsRename.ID, "open", `{"connection_id":` + id + `,"name":"A"}`},
		{connectionsDelete.ID, "connectiondelete", `{"connection_id":` + id + `}`},
		{connectionsAccessSet.ID, "open", `{"connection_id":` + id + `,"user_id":7,"role":"member"}`},
	} {
		h := newConnChangeEnv(t, ownTeam)
		if _, err := h.invoke(c.op, c.conn, c.args); !isConfirmationRequired(err) || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s err = %v, calls = %d, reads = %d", c.op, err, len(h.calls), *h.reads)
		}
		h = newConnChangeEnv(t, foreignTeam)
		_, err := h.confirmed(c.op, c.conn, c.args)
		if !isInvalidRequest(err) || len(h.changing()) != 0 || len(h.calls) != 1 {
			t.Fatalf("%s err = %v, calls = %+v", c.op, err, h.calls)
		}
		if strings.Contains(err.Error(), strconv.FormatInt(foreignTeam, 10)) {
			t.Fatalf("%s named the foreign team", c.op)
		}
	}
	h := newConnChangeEnv(t, foreignTeam)
	if _, err := h.invoke(connectionsAccessList.ID, "open", `{"connection_id":`+id+`}`); !isInvalidRequest(err) ||
		len(h.calls) != 1 {
		t.Fatalf("access.list err = %v, calls = %+v", err, h.calls)
	}
}

func TestConnectionChangesRefuseBadInputBeforeAnyRead(t *testing.T) {
	for _, c := range []struct{ op, args string }{
		{connectionsRename.ID, `{"connection_id":55,"name":""}`},
		{connectionsRename.ID, `{"connection_id":55,"name":"` + strings.Repeat("a", 129) + `"}`},
		{connectionsRename.ID, `{"connection_id":55,"name":"a\nb"}`},
		{connectionsAccessSet.ID, `{"connection_id":55,"user_id":7,"role":"owner"}`},
		{connectionsAccessSet.ID, `{"connection_id":55,"user_id":0,"role":"admin"}`},
		{connectionsAccessSet.ID, `{"connection_id":55,"role":"admin"}`},
		{connectionsDelete.ID, `{"connection_id":0}`},
		{connectionsDelete.ID, `{"connection_id":55,"confirmed":true}`},
	} {
		h := newConnChangeEnv(t, ownTeam)
		conn := "open"
		if c.op == connectionsDelete.ID {
			conn = "connectiondelete"
		}
		if _, err := h.confirmed(c.op, conn, c.args); err == nil || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s %s accepted or sent: err = %v, calls = %d", c.op, c.args, err, len(h.calls))
		}
	}
	h := newConnChangeEnv(t, ownTeam)
	for _, op := range []string{connectionsRename.ID, connectionsAccessList.ID, connectionsAccessSet.ID} {
		_, err := h.confirmed(op, "scenario", `{"connection_id":55,"name":"A","user_id":7,"role":"admin"}`)
		if err == nil || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s on scenario allow-list: err = %v, calls = %d", op, err, len(h.calls))
		}
	}
}

func TestConnectionsDeleteConfirmationAndScenarioConflict(t *testing.T) {
	id := strconv.FormatInt(connectionID, 10)
	h := newConnChangeEnv(t, ownTeam)
	h.refuse = true
	result, err := h.confirmed(connectionsDelete.ID, "connectiondelete", `{"connection_id":`+id+`}`)
	if err != nil {
		t.Fatalf("conflict err = %v", err)
	}
	var got ConnectionDeletion
	if err := json.Unmarshal([]byte(result), &got); err != nil || got.Deleted || !got.ConfirmationRequired ||
		len(got.Scenarios) != 2 || got.Scenarios[0].ID != 10 || got.Scenarios[1].ID != 11 ||
		strings.Contains(result, foreignCanary) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if ch := h.changing(); len(ch) != 1 || ch[0].query.Get("confirmed") != "" {
		t.Fatalf("changing = %+v, want one DELETE without confirmed and no retry", ch)
	}
	h.calls = nil
	result, err = h.confirmed(connectionsDelete.ID, "connectiondelete",
		`{"connection_id":`+id+`,"confirm_scenarios_affected":true}`)
	ch := h.changing()
	if err != nil || !strings.Contains(result, `"deleted":true`) || len(ch) != 1 || ch[0].query.Get("confirmed") != "true" {
		t.Fatalf("confirmed delete = %s, %v, %+v", result, err, ch)
	}
	// A refusal that names no scenario is an error, not a confirmation prompt.
	h = newConnChangeEnv(t, ownTeam)
	h.refuse = true
	h.bodies = nil
	env := newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			return jsonResponse(400, `{"message":"`+foreignCanary+`"}`), nil
		}
		return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, "C")+`}`), nil
	})
	_, err = env.confirmed(connectionsDelete.ID, "connectiondelete", `{"connection_id":`+id+`}`)
	if err == nil || strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("err = %v", err)
	}
	if _, err := env.confirmed(connectionsDelete.ID, "open", `{"connection_id":`+id+`}`); err == nil {
		t.Fatal("connections.delete was offered without a tools list")
	}
	if !connectionsDelete.RequiresToolAllowList {
		t.Fatal("connections.delete must require a tools list")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == connectionsDelete.ID {
				t.Fatalf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}

func TestConnectionAccessListReturnsOnlyIDsAndRoles(t *testing.T) {
	id := strconv.FormatInt(connectionID, 10)
	h := newConnChangeEnv(t, ownTeam)
	result, err := h.invoke(connectionsAccessList.ID, "open", `{"connection_id":`+id+`}`)
	if err != nil || !strings.Contains(result, `"user_id":7`) || !strings.Contains(result, `"member"`) ||
		!strings.Contains(result, `"count":1`) || strings.Contains(result, canaryConnSecret) ||
		strings.Contains(result, `"email"`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	var many []string
	for i := 1; i <= maxAccessMembers+5; i++ {
		many = append(many, `{"membershipType":"user","membershipId":`+strconv.Itoa(i)+`,"role":"`+
			strings.Repeat("r", 1000)+`"}`)
	}
	h.members = "[" + strings.Join(many, ",") + "]"
	result, err = h.invoke(connectionsAccessList.ID, "open", `{"connection_id":`+id+`}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > maxAccessMembers*(maxConnectionText+60)+300 {
		t.Fatalf("len = %d, %v", len(result), err)
	}
}

func TestConnectionAccessSetSkipsWhenRoleAlreadyHeld(t *testing.T) {
	id := strconv.FormatInt(connectionID, 10)
	h := newConnChangeEnv(t, ownTeam)
	result, err := h.confirmed(connectionsAccessSet.ID, "open", `{"connection_id":`+id+`,"user_id":7,"role":"member"}`)
	if err != nil || len(h.changing()) != 0 || !strings.Contains(result, `"changed":false`) {
		t.Fatalf("result = %s, %v, changing = %+v", result, err, h.changing())
	}
	result, _ = h.confirmed(connectionsAccessSet.ID, "open", `{"connection_id":`+id+`,"user_id":8,"role":"admin"}`)
	if !strings.Contains(result, `"role":"admin"`) || !strings.Contains(result, `"changed":true`) ||
		strings.Contains(result, canaryConnSecret) {
		t.Fatalf("result = %s", result)
	}
}

func TestConnectionChangesAreNeverRetriedAndNameTheScope(t *testing.T) {
	id := strconv.FormatInt(connectionID, 10)
	access := apiPath + "/teams/" + itoa64(ownTeam) + "/connections/" + id + "/access-list"
	for _, status := range []int{500, 403} {
		for _, c := range []struct{ op, conn, args, scope string }{
			{connectionsRename.ID, "open", `{"connection_id":` + id + `,"name":"A"}`, "connections:write"},
			{connectionsDelete.ID, "connectiondelete", `{"connection_id":` + id + `}`, "connections:write"},
			{connectionsAccessSet.ID, "open", `{"connection_id":` + id + `,"user_id":8,"role":"admin"}`, "entity manage"},
		} {
			var calls []call
			mutations := 0
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.URL.Path, apiPath+"/users/") {
					return jsonResponse(200, `{"userTeamRole":{"userId":8,"teamId":`+itoa64(ownTeam)+`}}`), nil
				}
				if r.Method == http.MethodGet && r.URL.Path == access {
					return jsonResponse(200, `{"accessList":[]}`), nil
				}
				if r.Method == http.MethodGet {
					return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, "C")+`}`), nil
				}
				mutations++
				return jsonResponse(status, `{"message":"`+foreignCanary+`"}`), nil
			})
			_, err := env.confirmed(c.op, c.conn, c.args)
			if err == nil || mutations != 1 || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("%s/%d err = %v, mutations = %d", c.op, status, err, mutations)
			}
			if status == 500 && !strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("%s lacks the uncertain note: %v", c.op, err)
			}
			var perr *provider.Error
			if status == 403 && (!errors.As(err, &perr) || perr.Class != provider.ClassPermission ||
				!strings.Contains(perr.Message, c.scope)) {
				t.Fatalf("%s err = %v, want the %s hint", c.op, err, c.scope)
			}
		}
	}
	// An aborted request and an unreadable answer are uncertain as well, never repeated.
	for name, failure := range map[string]func() (*http.Response, error){
		"aborted": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbled": func() (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	} {
		var calls []call
		mutations := 0
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if strings.HasPrefix(r.URL.Path, apiPath+"/users/") {
				return jsonResponse(200, `{"userTeamRole":{"userId":8,"teamId":`+itoa64(ownTeam)+`}}`), nil
			}
			if r.Method == http.MethodGet && r.URL.Path == access {
				return jsonResponse(200, `{"accessList":[]}`), nil
			}
			if r.Method == http.MethodGet {
				return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, "C")+`}`), nil
			}
			mutations++
			return failure()
		})
		_, err := env.confirmed(connectionsAccessSet.ID, "open", `{"connection_id":`+id+`,"user_id":8,"role":"admin"}`)
		if err == nil || mutations != 1 || !strings.Contains(err.Error(), "may have taken effect") {
			t.Fatalf("%s err = %v, mutations = %d", name, err, mutations)
		}
	}
}

func TestConnectionRenameReportsTheNewState(t *testing.T) {
	id := strconv.FormatInt(connectionID, 10)
	h := newConnChangeEnv(t, ownTeam)
	result, err := h.confirmed(connectionsRename.ID, "open", `{"connection_id":`+id+`,"name":"Renamed"}`)
	if err != nil || !strings.Contains(result, `"name":"Renamed"`) || strings.Contains(result, canaryConnSecret) {
		t.Fatalf("result = %s, %v", result, err)
	}
}

func TestConnectionChangeToolsAreDeclaredCompletely(t *testing.T) {
	for _, d := range []struct {
		id         string
		confirm    string
		effect     string
		allowList  bool
		openWorld  bool
		sensitivty string
	}{
		{connectionsRename.ID, string(connectionsRename.Risk.Confirmation), string(connectionsRename.Risk.Effect), false,
			connectionsRename.Risk.OpenWorld, connectionsRename.Risk.DataSensitivity},
		{connectionsDelete.ID, string(connectionsDelete.Risk.Confirmation), string(connectionsDelete.Risk.Effect), true,
			connectionsDelete.Risk.OpenWorld, connectionsDelete.Risk.DataSensitivity},
		{connectionsAccessSet.ID, string(connectionsAccessSet.Risk.Confirmation), string(connectionsAccessSet.Risk.Effect),
			false, connectionsAccessSet.Risk.OpenWorld, connectionsAccessSet.Risk.DataSensitivity},
	} {
		if d.confirm != "required" || d.effect == "" || !d.openWorld || d.sensitivty == "" {
			t.Fatalf("%s risk incomplete: %+v", d.id, d)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	found := map[string][]string{}
	for _, profile := range metadata.Profiles {
		found[profile.ID] = profile.Tools
	}
	if len(found["connections-manage"]) != 1 || found["connections-manage"][0] != connectionsRename.ID ||
		len(found["connections-access-read"]) != 1 || found["connections-access-read"][0] != connectionsAccessList.ID ||
		len(found["connections-access-manage"]) != 1 || found["connections-access-manage"][0] != connectionsAccessSet.ID {
		t.Fatalf("profiles = %+v", found)
	}
}

func TestConnectionAccessSetRefusesNonTeamMemberBeforeAnyChange(t *testing.T) {
	id := strconv.FormatInt(connectionID, 10)
	args := `{"connection_id":` + id + `,"user_id":8,"role":"admin"}`
	h := newConnChangeEnv(t, ownTeam)
	h.nonMember = true
	_, err := h.confirmed(connectionsAccessSet.ID, "open", args)
	if !isInvalidRequest(err) || len(h.changing()) != 0 || strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("err = %v, changing = %+v", err, h.changing())
	}
	last := h.calls[len(h.calls)-1]
	if last.method != http.MethodGet || last.path != apiPath+"/users/8/user-team-roles/"+itoa64(ownTeam) {
		t.Fatalf("last call = %+v", last)
	}
	// A role of another team, or of another user, is no proof either.
	for name, body := range map[string]string{
		"other team": `{"userTeamRole":{"userId":8,"teamId":` + itoa64(foreignTeam) + `}}`,
		"other user": `{"userTeamRole":{"userId":9,"teamId":` + itoa64(ownTeam) + `}}`,
		"empty":      `{}`,
	} {
		var calls []call
		mutations := 0
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.HasPrefix(r.URL.Path, apiPath+"/users/"):
				return jsonResponse(200, body), nil
			case r.Method == http.MethodGet:
				return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, "C")+`}`), nil
			}
			mutations++
			return jsonResponse(200, `{}`), nil
		})
		if _, err := env.confirmed(connectionsAccessSet.ID, "open", args); !isInvalidRequest(err) || mutations != 0 {
			t.Fatalf("%s: err = %v, mutations = %d", name, err, mutations)
		}
	}
	// A 403 on the membership read names user:read.
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, apiPath+"/users/") {
			return jsonResponse(403, `{}`), nil
		}
		return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, "C")+`}`), nil
	})
	_, err = env.confirmed(connectionsAccessSet.ID, "open", args)
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.ClassPermission || !strings.Contains(perr.Message, "user:read") {
		t.Fatalf("err = %v", err)
	}
}
