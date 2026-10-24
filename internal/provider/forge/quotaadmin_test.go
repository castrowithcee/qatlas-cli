package forge

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var (
	quotaAdminDeleteIDs = []string{"forgejo.admin-quota-groups.delete", "forgejo.admin-quota-rules.delete"}
	quotaAdminToolIDs   = []string{
		"forgejo.admin-quota-groups.list", "forgejo.admin-quota-groups.get", "forgejo.admin-quota-groups.create",
		"forgejo.admin-quota-groups.delete", "forgejo.admin-quota-groups.add-rule",
		"forgejo.admin-quota-groups.remove-rule", "forgejo.admin-quota-group-users.list",
		"forgejo.admin-quota-group-users.add", "forgejo.admin-quota-group-users.remove",
		"forgejo.admin-quota-rules.list", "forgejo.admin-quota-rules.get", "forgejo.admin-quota-rules.create",
		"forgejo.admin-quota-rules.update", "forgejo.admin-quota-rules.delete", "forgejo.admin-user-quota.get",
		"forgejo.admin-user-quota.set-groups",
	}
)

// quotaAdminCases are the tools with valid arguments and the request each sends.
var quotaAdminCases = []struct {
	tl      *tool
	effect  string
	args    string
	request string
	body    string
}{
	{quotaAdminGroupsList, "read", `{}`, "GET /api/v1/admin/quota/groups", ""},
	{quotaAdminGroupsGet, "read", `{"group":"std"}`, "GET /api/v1/admin/quota/groups/std", ""},
	{quotaAdminGroupsCreate, "create", `{"name":"std"}`, "POST /api/v1/admin/quota/groups", `{"name":"std"}`},
	{quotaAdminGroupsDelete, "delete", `{"group":"std"}`, "DELETE /api/v1/admin/quota/groups/std", ""},
	{quotaAdminGroupsAddRule, "update", `{"group":"std","rule":"r1"}`, "PUT /api/v1/admin/quota/groups/std/rules/r1", ""},
	{quotaAdminGroupsRemoveRule, "update", `{"group":"std","rule":"r1"}`,
		"DELETE /api/v1/admin/quota/groups/std/rules/r1", ""},
	{quotaAdminGroupUsersList, "read", `{"group":"std"}`, "GET /api/v1/admin/quota/groups/std/users", ""},
	{quotaAdminGroupUsersAdd, "update", `{"group":"std","username":"octo"}`,
		"PUT /api/v1/admin/quota/groups/std/users/octo", ""},
	{quotaAdminGroupUsersRemove, "update", `{"group":"std","username":"octo"}`,
		"DELETE /api/v1/admin/quota/groups/std/users/octo", ""},
	{quotaAdminRulesList, "read", `{}`, "GET /api/v1/admin/quota/rules", ""},
	{quotaAdminRulesGet, "read", `{"rule":"r1"}`, "GET /api/v1/admin/quota/rules/r1", ""},
	{quotaAdminRulesCreate, "create", `{"name":"r1","limit":-1,"subjects":["size:all","size:assets:wiki"]}`,
		"POST /api/v1/admin/quota/rules", `{"limit":-1,"name":"r1","subjects":["size:all","size:assets:wiki"]}`},
	{quotaAdminRulesUpdate, "update", `{"rule":"r1","limit":5}`, "PATCH /api/v1/admin/quota/rules/r1", `{"limit":5}`},
	{quotaAdminRulesDelete, "delete", `{"rule":"r1"}`, "DELETE /api/v1/admin/quota/rules/r1", ""},
	{quotaAdminUserQuotaGet, "read", `{"username":"octo"}`, "GET /api/v1/admin/users/octo/quota", ""},
	{quotaAdminUserQuotaSetGroups, "update", `{"username":"octo","groups":["std","big"]}`,
		"POST /api/v1/admin/users/octo/quota/groups", `{"groups":["std","big"]}`},
}

type quotaAdminState struct {
	on, baseUp bool
	status     int
	rows       int
}

const (
	adminRuleFixture  = `{"name":"r1","limit":1000,"subjects":["size:all","size:repos:all"]}`
	adminGroupFixture = `{"name":"std","rules":[` + adminRuleFixture + `]}`
	adminUserFixture  = `{"id":7,"login":"octo","full_name":"Octo","email":"octo@example.test"}`
)

func quotaAdminServer(s *quotaAdminState) func(call) *http.Response {
	return func(c call) *http.Response {
		path := strings.TrimPrefix(c.path, "/api/v1")
		if !strings.Contains(path, "/quota") {
			if s.baseUp && path == "/admin/users" && c.method == http.MethodGet {
				return jsonResponse(200, `[]`)
			}
			return notFoundResponse()
		}
		if !s.on {
			return notFoundResponse()
		}
		if s.status != 200 {
			return jsonResponse(s.status, `{"message":"`+bodyCanary+`"}`)
		}
		if c.method != http.MethodGet {
			return jsonResponse(204, "")
		}
		many := func(row string) string {
			rows := make([]string, s.rows)
			for i := range rows {
				rows[i] = row
			}
			return `[` + strings.Join(rows, ",") + `]`
		}
		switch {
		case path == "/admin/quota/groups":
			return jsonResponse(200, many(adminGroupFixture))
		case path == "/admin/quota/rules":
			return jsonResponse(200, many(adminRuleFixture))
		case strings.HasSuffix(path, "/users"):
			return jsonResponse(200, many(adminUserFixture))
		case strings.HasPrefix(path, "/admin/quota/groups/"):
			return jsonResponse(200, adminGroupFixture)
		case strings.HasPrefix(path, "/admin/quota/rules/"):
			return jsonResponse(200, adminRuleFixture)
		case strings.HasSuffix(path, "/quota"):
			return jsonResponse(200, quotaInfoFixture)
		}
		return notFoundResponse()
	}
}

func upAdmin() *quotaAdminState {
	return &quotaAdminState{on: true, baseUp: true, status: 200, rows: 2}
}

// connectionFor is the connection that offers the tool: the listed-only ones need the tools list.
func connectionFor(tl *tool) string {
	if tl == quotaAdminGroupsDelete || tl == quotaAdminRulesDelete {
		return "quota-admin"
	}
	return "open"
}

func TestQuotaAdminToolsAreRegisteredForForgejoOnlyWithTheirRisk(t *testing.T) {
	reg := registry(t)
	if len(quotaAdminCases) != len(quotaAdminToolIDs) {
		t.Fatalf("%d cases for %d tools", len(quotaAdminCases), len(quotaAdminToolIDs))
	}
	metadata, _ := reg.ProviderMetadata(forgejo.id)
	for _, c := range quotaAdminCases {
		d, _, ok := reg.Lookup(forgejo.toolID(c.tl))
		if !ok || !slices.Contains(quotaAdminToolIDs, d.ID) {
			t.Fatalf("%s is not registered", forgejo.toolID(c.tl))
		}
		if _, ok := c.tl.since[gitea.id]; ok {
			t.Errorf("%s is registered for Gitea", d.ID)
		}
		if _, _, ok := reg.Lookup("gitea" + strings.TrimPrefix(d.ID, "forgejo")); ok {
			t.Errorf("%s exists for Gitea", d.ID)
		}
		if string(d.Risk.Effect) != c.effect || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		mutates := c.effect != "read"
		if mutates && (d.Risk.Confirmation != "required" || d.Risk.Idempotency != "idempotent" ||
			d.Risk.DataSensitivity != "forgejo-quota-administration") {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		if !mutates && (d.Risk.Confirmation != "none" || d.Risk.Idempotency != "safe") {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		if d.RequiresToolAllowList != (c.effect == "delete") {
			t.Errorf("%s listed-only = %t", d.ID, d.RequiresToolAllowList)
		}
		for _, profile := range metadata.Profiles {
			if slices.Contains(profile.Tools, d.ID) {
				t.Errorf("profile %s holds %s", profile.ID, d.ID)
			}
		}
	}
}

func TestQuotaAdminToolsSendTheirOneFixedRequest(t *testing.T) {
	for _, c := range quotaAdminCases {
		e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
		if _, err := e.invoke(t, c.tl, connectionFor(c.tl), c.args); err != nil {
			t.Fatalf("%s: %v", c.tl.action, err)
		}
		if got := methods(e); !slices.Equal(got, []string{c.request}) {
			t.Errorf("%s calls = %v, want %s", c.tl.action, got, c.request)
		}
		if body := (*e.calls)[0].body; body != c.body {
			t.Errorf("%s body = %q, want %q", c.tl.action, body, c.body)
		}
	}
}

func TestQuotaAdminRefusesAnyTargetBeforeIO(t *testing.T) {
	for _, c := range quotaAdminCases {
		for _, connection := range []string{"org", "repo", "owner", "mixed", "two"} {
			e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
			_, err := e.invoke(t, c.tl, connection, c.args)
			// A listed-only tool is not offered at all to a connection that does not list it.
			if err == nil || len(*e.calls) != 0 || *e.reads != 0 {
				t.Errorf("%s on %s: err %v, calls %v, token reads %d", c.tl.action, connection, err, methods(e), *e.reads)
			}
			if err != nil && strings.Contains(err.Error(), "acme") {
				t.Errorf("the refusal names a target: %v", err)
			}
			if c.effect != "delete" && !isInvalidRequest(err) {
				t.Errorf("%s on %s: %v", c.tl.action, connection, err)
			}
		}
	}
}

func TestQuotaAdminDeletesAreListedOnly(t *testing.T) {
	for _, c := range quotaAdminCases {
		if c.effect != "delete" {
			continue
		}
		e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
		if _, err := e.invoke(t, c.tl, "open", c.args); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s without a tools list: %v, calls %v", c.tl.action, err, methods(e))
		}
		if _, err := e.invokeAs(t, c.tl, "quota-admin", c.args, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s unconfirmed: %v, calls %v", c.tl.action, err, methods(e))
		}
		result, err := e.invoke(t, c.tl, "quota-admin", c.args)
		if err != nil || result["deleted"] != true || !slices.Equal(methods(e), []string{c.request}) {
			t.Errorf("%s = %v, %v, calls %v", c.tl.action, result, err, methods(e))
		}
	}
}

func TestQuotaAdminChangeNeedsConfirmation(t *testing.T) {
	for _, c := range quotaAdminCases {
		if c.effect == "read" {
			continue
		}
		e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
		_, err := e.invokeAs(t, c.tl, connectionFor(c.tl), c.args, false)
		if err == nil || len(*e.calls) != 0 {
			t.Errorf("%s unconfirmed: %v, calls %v", c.tl.action, err, methods(e))
		}
	}
}

func TestQuotaAdminChangeIsSentOnceAndNeverRepeated(t *testing.T) {
	for _, c := range quotaAdminCases {
		if c.effect == "read" {
			continue
		}
		state := upAdmin()
		state.status = 500
		e := newEnvironment(t, forgejo, quotaAdminServer(state))
		_, err := e.invoke(t, c.tl, connectionFor(c.tl), c.args)
		if err == nil || len(*e.calls) != 1 {
			t.Errorf("%s after a 500: %v, calls %v", c.tl.action, err, methods(e))
		}
	}
}

func TestQuotaAdminReadsShowEveryDocumentedMember(t *testing.T) {
	e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
	groups, err := e.invoke(t, quotaAdminGroupsList, "open", `{}`)
	if err != nil || groups["has_more"] != false {
		t.Fatalf("groups = %v, %v", groups, err)
	}
	group := rowsOf(t, groups, "groups")[0]
	rule := group["rules"].([]any)[0].(map[string]any)
	if group["name"] != "std" || rule["name"] != "r1" || rule["limit"] != float64(1000) ||
		!reflect.DeepEqual(rule["subjects"], []any{"size:all", "size:repos:all"}) {
		t.Errorf("group = %v", group)
	}
	one, err := e.invoke(t, quotaAdminGroupsGet, "open", `{"group":"std"}`)
	if err != nil || one["name"] != "std" || len(one["rules"].([]any)) != 1 {
		t.Errorf("group = %v, %v", one, err)
	}
	rules, err := e.invoke(t, quotaAdminRulesList, "open", `{}`)
	if err != nil || rowsOf(t, rules, "rules")[1]["name"] != "r1" {
		t.Errorf("rules = %v, %v", rules, err)
	}
	single, err := e.invoke(t, quotaAdminRulesGet, "open", `{"rule":"r1"}`)
	if err != nil || single["limit"] != float64(1000) || single["name"] != "r1" || single["subjects"] == nil {
		t.Errorf("rule = %v, %v", single, err)
	}
	users, err := e.invoke(t, quotaAdminGroupUsersList, "open", `{"group":"std"}`)
	user := rowsOf(t, users, "users")[0]
	if err != nil || user["login"] != "octo" || user["email"] != "octo@example.test" || user["id"] != float64(7) {
		t.Errorf("users = %v, %v", users, err)
	}
	info, err := e.invoke(t, quotaAdminUserQuotaGet, "open", `{"username":"octo"}`)
	if err != nil || info["used"] == nil || info["groups"] == nil {
		t.Errorf("quota = %v, %v", info, err)
	}
	if d := quotaAdminGroupUsersList.descriptor(forgejo); d.Risk.DataSensitivity != "forgejo-personal-data" {
		t.Errorf("group users sensitivity = %s", d.Risk.DataSensitivity)
	}
}

func TestQuotaAdminListsAreCappedWithHasMore(t *testing.T) {
	state := upAdmin()
	state.rows = maxQuotaAdminRows + 3
	for _, c := range []struct {
		tl   *tool
		args string
		key  string
	}{
		{quotaAdminGroupsList, `{}`, "groups"}, {quotaAdminRulesList, `{}`, "rules"},
		{quotaAdminGroupUsersList, `{"group":"std"}`, "users"},
	} {
		e := newEnvironment(t, forgejo, quotaAdminServer(state))
		result, err := e.invoke(t, c.tl, "open", c.args)
		if err != nil || len(rowsOf(t, result, c.key)) != maxQuotaAdminRows || result["has_more"] != true {
			t.Errorf("%s: %d rows, has_more %v, %v", c.key, len(rowsOf(t, result, c.key)), result["has_more"], err)
		}
	}
}

func TestQuotaAdminBodiesCarryOnlyTheDocumentedFields(t *testing.T) {
	for _, c := range []struct {
		tl   *tool
		args string
		body string
	}{
		{quotaAdminRulesUpdate, `{"rule":"r1","subjects":["size:git:lfs"]}`, `{"subjects":["size:git:lfs"]}`},
		{quotaAdminRulesUpdate, `{"rule":"r1","limit":0,"subjects":["size:all"]}`, `{"limit":0,"subjects":["size:all"]}`},
		{quotaAdminUserQuotaSetGroups, `{"username":"octo","groups":[]}`, `{"groups":[]}`},
	} {
		e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
		if _, err := e.invoke(t, c.tl, "open", c.args); err != nil {
			t.Fatalf("%s %s: %v", c.tl.action, c.args, err)
		}
		if body := (*e.calls)[0].body; body != c.body {
			t.Errorf("%s body = %q, want %q", c.tl.action, body, c.body)
		}
	}
}

func TestQuotaAdminRefusesUnusableArgumentsBeforeIO(t *testing.T) {
	long := strings.Repeat("g", 256)
	for _, c := range []struct {
		tl   *tool
		args string
	}{
		{quotaAdminGroupsGet, `{"group":"a/b"}`},
		{quotaAdminGroupsGet, `{"group":".."}`},
		{quotaAdminGroupsGet, `{"group":"."}`},
		{quotaAdminGroupsGet, `{"group":""}`},
		{quotaAdminGroupsGet, `{"group":"a\nb"}`},
		{quotaAdminGroupsGet, `{"group":"` + long + `"}`},
		{quotaAdminGroupsDelete, `{"group":"a/b"}`},
		{quotaAdminGroupsCreate, `{"name":"a/b"}`},
		{quotaAdminGroupsAddRule, `{"group":"std","rule":"../x"}`},
		{quotaAdminGroupUsersAdd, `{"group":"std","username":"a/b"}`},
		{quotaAdminGroupUsersAdd, `{"group":"std","username":".."}`},
		{quotaAdminRulesGet, `{"rule":"x/y"}`},
		{quotaAdminRulesCreate, `{"name":"r","limit":1,"subjects":["size:nothing"]}`},
		{quotaAdminRulesCreate, `{"name":"r","limit":1,"subjects":[]}`},
		{quotaAdminRulesCreate, `{"name":"r","limit":1,"subjects":["size:all","size:all"]}`},
		{quotaAdminRulesCreate, `{"name":"r","limit":-2,"subjects":["size:all"]}`},
		{quotaAdminRulesCreate, `{"name":"r","subjects":["size:all"]}`},
		{quotaAdminRulesCreate, `{"name":"r","limit":1,"subjects":["size:all"],"rules":[]}`},
		{quotaAdminRulesUpdate, `{"rule":"r"}`},
		{quotaAdminRulesDelete, `{"rule":"x/y"}`},
		{quotaAdminUserQuotaGet, `{"username":"a b"}`},
		{quotaAdminUserQuotaSetGroups, `{"username":"octo","groups":["a","a"]}`},
		{quotaAdminUserQuotaSetGroups, `{"username":"octo","groups":["a/b"]}`},
		{quotaAdminUserQuotaSetGroups, `{"username":"octo"}`},
	} {
		e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
		_, err := e.invoke(t, c.tl, connectionFor(c.tl), c.args)
		if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s %s: err %v, calls %v, token reads %d", c.tl.action, c.args, err, methods(e), *e.reads)
		}
	}
	// 101 distinct groups are too many.
	groups := make([]string, 101)
	for i := range groups {
		groups[i] = `"g` + strconv.Itoa(i) + `"`
	}
	e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
	_, err := e.invoke(t, quotaAdminUserQuotaSetGroups, "open", `{"username":"octo","groups":[`+strings.Join(groups, ",")+`]}`)
	if !isInvalidRequest(err) || len(*e.calls) != 0 {
		t.Errorf("101 groups: %v", err)
	}
}

func TestQuotaAdminEscapesNamesAsOnePathSegment(t *testing.T) {
	e := newEnvironment(t, forgejo, quotaAdminServer(upAdmin()))
	if _, err := e.invoke(t, quotaAdminGroupsGet, "open", `{"group":"my group%x?#"}`); err != nil {
		t.Fatal(err)
	}
	if got := (*e.calls)[0].escaped; got != "/api/v1/admin/quota/groups/my%20group%25x%3F%23" {
		t.Errorf("path = %s", got)
	}
}

func TestQuotaAdminSwitchedOffQuotaIsNotAvailableAndAMissingObjectIsNotFound(t *testing.T) {
	for _, c := range []struct {
		tl          *tool
		state       quotaAdminState
		unavailable bool
		want        []string
	}{
		// A collection route answers 404, the admin base route answers: the quota is off.
		{quotaAdminGroupsList, quotaAdminState{baseUp: true, status: 200},
			true, []string{"GET /api/v1/admin/quota/groups", "GET /api/v1/admin/users"}},
		{quotaAdminRulesCreate, quotaAdminState{baseUp: true, status: 200}, true,
			[]string{"POST /api/v1/admin/quota/rules", "GET /api/v1/admin/users"}},
		// A named route: the rule list answers 404 as well, then the base route decides.
		{quotaAdminGroupsGet, quotaAdminState{baseUp: true, status: 200}, true,
			[]string{"GET /api/v1/admin/quota/groups/std", "GET /api/v1/admin/quota/rules", "GET /api/v1/admin/users"}},
		{quotaAdminGroupsAddRule, quotaAdminState{baseUp: true, status: 200}, true,
			[]string{"PUT /api/v1/admin/quota/groups/std/rules/r1", "GET /api/v1/admin/quota/rules",
				"GET /api/v1/admin/users"}},
		{quotaAdminUserQuotaGet, quotaAdminState{baseUp: true, status: 200}, true,
			[]string{"GET /api/v1/admin/users/octo/quota", "GET /api/v1/admin/quota/rules", "GET /api/v1/admin/users"}},
		// The base route does not answer either: not reachable.
		{quotaAdminGroupsList, quotaAdminState{status: 200}, false,
			[]string{"GET /api/v1/admin/quota/groups", "GET /api/v1/admin/users"}},
		// A 403 is not reachable without any further request.
		{quotaAdminGroupsList, quotaAdminState{on: true, status: 403}, false, []string{"GET /api/v1/admin/quota/groups"}},
		{quotaAdminRulesUpdate, quotaAdminState{on: true, status: 403}, false,
			[]string{"PATCH /api/v1/admin/quota/rules/r1"}},
	} {
		args := ""
		for _, k := range quotaAdminCases {
			if k.tl == c.tl {
				args = k.args
			}
		}
		e := newEnvironment(t, forgejo, quotaAdminServer(&c.state))
		_, err := e.invoke(t, c.tl, "open", args)
		var failure *provider.Error
		if err == nil || !errors.As(err, &failure) {
			t.Fatalf("%s: err %v", c.tl.action, err)
		}
		wantClass := provider.ClassNotFound
		if c.unavailable {
			wantClass = provider.ClassProviderError
			if !strings.Contains(failure.Message, "not available") {
				t.Errorf("%s: message %q", c.tl.action, failure.Message)
			}
		}
		if failure.Class != wantClass || !slices.Equal(methods(e), c.want) {
			t.Errorf("%s: class %s, calls %v, want %v", c.tl.action, failure.Class, methods(e), c.want)
		}
		if mutations := countMutations(e); mutations > 1 {
			t.Errorf("%s sent %d changing requests", c.tl.action, mutations)
		}
	}
	// The quota is on and the group is missing: the rule list answers, so the object is reported as not found.
	e := newEnvironment(t, forgejo, func(c call) *http.Response {
		if strings.HasPrefix(c.path, "/api/v1/admin/quota/groups/") {
			return notFoundResponse()
		}
		return jsonResponse(200, `[]`)
	})
	_, err := e.invoke(t, quotaAdminGroupUsersAdd, "open", `{"group":"gone","username":"octo"}`)
	if classOfError(err) != provider.ClassNotFound ||
		!slices.Equal(methods(e), []string{"PUT /api/v1/admin/quota/groups/gone/users/octo", "GET /api/v1/admin/quota/rules"}) {
		t.Errorf("missing group: %v, calls %v", err, methods(e))
	}
}

func countMutations(e *environment) int {
	n := 0
	for _, c := range *e.calls {
		if c.method != http.MethodGet {
			n++
		}
	}
	return n
}
