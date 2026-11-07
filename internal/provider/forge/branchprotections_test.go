package forge

import (
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

const protectionsAPI = "/api/v1/repos/acme/widget/branch_protections"

// The rule fixtures have the answer form of the swagger files: Forgejo 15 through 17 and Gitea 1.27 and 28.
const (
	sharedProtectionFixture = `"rule_name":"main","branch_name":"main","enable_push":true,"enable_push_whitelist":true,` +
		`"push_whitelist_usernames":["octo"],"push_whitelist_teams":["core"],"push_whitelist_deploy_keys":true,` +
		`"enable_merge_whitelist":true,"merge_whitelist_usernames":["octo"],"merge_whitelist_teams":["core"],` +
		`"enable_status_check":true,"status_check_contexts":["ci/*"],"required_approvals":2,` +
		`"enable_approvals_whitelist":true,"approvals_whitelist_username":["octo"],"approvals_whitelist_teams":["core"],` +
		`"block_on_rejected_reviews":true,"block_on_official_review_requests":true,"block_on_outdated_branch":true,` +
		`"dismiss_stale_approvals":true,"ignore_stale_approvals":true,"require_signed_commits":true,` +
		`"protected_file_patterns":"*.lock;docs/**","unprotected_file_patterns":"notes/**",` +
		`"created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-02T00:00:00Z"`
	forgejoProtectionFixture = `{` + sharedProtectionFixture + `,"apply_to_admins":true}`
	giteaProtectionFixture   = `{` + sharedProtectionFixture + `,"priority":4,"enable_force_push":true,` +
		`"enable_force_push_allowlist":true,"force_push_allowlist_usernames":["octo"],"force_push_allowlist_teams":["core"],` +
		`"force_push_allowlist_deploy_keys":true,"enable_bypass_allowlist":true,"bypass_allowlist_usernames":["octo"],` +
		`"bypass_allowlist_teams":["core"],"block_admin_merge_override":true,"block_on_codeowner_reviews":true}`
)

func protectionFixture(p *product) string {
	if p == forgejo {
		return forgejoProtectionFixture
	}
	return giteaProtectionFixture
}

// protectionServer answers the routes with the swagger shapes; status is the answer of every change.
func protectionServer(p *product, status *int) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.method != http.MethodGet:
			if *status == 201 || *status == 200 {
				return jsonResponse(*status, protectionFixture(p))
			}
			return jsonResponse(*status, "")
		case c.path == protectionsAPI:
			return jsonResponse(200, `[`+protectionFixture(p)+`,null]`)
		case c.path == protectionsAPI+"/main":
			return jsonResponse(200, protectionFixture(p))
		}
		return notFoundResponse()
	}
}

var protectionTools = []*tool{branchProtectionsList, branchProtectionsGet, branchProtectionsCreate,
	branchProtectionsUpdate, branchProtectionsReorder, branchProtectionsDelete}

func registeredFor(p *product, tl *tool) bool {
	_, ok := tl.since[p.id]
	return ok
}

func TestBranchProtectionsListAndGetShowEveryMember(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, protectionServer(p, &status))
		result, err := e.invoke(t, branchProtectionsList, "repo", `{}`)
		rules := rowsOf(t, result, "branch_protections")
		if err != nil || len(rules) != 1 || result["repository"] != "acme/widget" || result["truncated"] != nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + protectionsAPI}) || (*e.calls)[0].query != "" {
			t.Errorf("%s calls = %v %q", p.id, got, (*e.calls)[0].query)
		}
		d := e.descriptor(t, branchProtectionsGet)
		for _, f := range d.Fields {
			if f.Name == "branch_name" {
				t.Errorf("%s shows the deprecated branch_name", p.id)
			}
		}
		one, err := e.invoke(t, branchProtectionsGet, "repo", `{"rule_name":"main"}`)
		if err != nil || one["repository"] != "acme/widget" || one["rule_name"] != "main" {
			t.Fatalf("%s: %v, %v", p.id, one, err)
		}
		for _, f := range d.Fields {
			if _, ok := one[f.Name]; !ok {
				t.Errorf("%s: documented member %s is missing from the answer", p.id, f.Name)
			}
		}
		if _, ok := one["branch_name"]; ok {
			t.Errorf("%s shows branch_name", p.id)
		}
		if _, ok := one["priority"]; ok != (p == gitea) {
			t.Errorf("%s priority shown = %t", p.id, ok)
		}
		if _, ok := one["apply_to_admins"]; ok != (p == forgejo) {
			t.Errorf("%s apply_to_admins shown = %t", p.id, ok)
		}
		if _, ok := one["enable_force_push"]; ok != (p == gitea) {
			t.Errorf("%s enable_force_push shown = %t", p.id, ok)
		}
	}
}

func TestBranchProtectionsListCapsAtOneHundredAndSaysSo(t *testing.T) {
	for _, p := range products {
		rows := strings.TrimSuffix(strings.Repeat(protectionFixture(p)+",", 101), ",")
		e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, `[`+rows+`]`, "X-Total-Count", "101") })
		result, err := e.invoke(t, branchProtectionsList, "repo", `{}`)
		if err != nil || len(rowsOf(t, result, "branch_protections")) != 100 || result["truncated"] != true {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestBranchProtectionNameIsOneEscapedSegment(t *testing.T) {
	for _, p := range products {
		status := 204
		for _, tl := range []*tool{branchProtectionsGet, branchProtectionsUpdate, branchProtectionsDelete} {
			e := newEnvironment(t, p, protectionServer(p, &status))
			connection := "repo"
			if tl == branchProtectionsDelete {
				connection = "protection-delete"
			}
			args := `{"rule_name":"release/*?x=1 #"`
			if tl == branchProtectionsUpdate {
				args += `,"required_approvals":1`
			}
			_, _ = e.invoke(t, tl, connection, args+`}`)
			if len(*e.calls) == 0 {
				t.Fatalf("%s %s sent no request", p.id, tl.action)
			}
			last := (*e.calls)[len(*e.calls)-1]
			rest, ok := strings.CutPrefix(last.escaped, protectionsAPI+"/")
			if !ok || strings.ContainsAny(rest, "/?#") || last.query != "" ||
				rest != url.PathEscape("release/*?x=1 #") {
				t.Errorf("%s %s escaped path = %q query %q", p.id, tl.action, last.escaped, last.query)
			}
		}
	}
}

func TestBranchProtectionsAreBoundToTheTargetsBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		status := 204
		for _, tl := range protectionTools {
			if !registeredFor(p, tl) {
				continue
			}
			for _, connection := range []string{"repo", "exclude"} {
				repository := "other/tool"
				if connection == "exclude" {
					repository = "acme/gadget"
				}
				e := newEnvironment(t, p, protectionServer(p, &status))
				_, err := e.invoke(t, tl, connection,
					`{"repository":"`+repository+`","rule_name":"main","required_approvals":1,"ids":[1]}`)
				refused(t, e, tl.action+" "+connection, err)
				if err != nil && strings.Contains(err.Error(), repository) {
					t.Errorf("%s: the refusal names the repository: %v", p.id, err)
				}
			}
		}
	}
}

func TestBranchProtectionRuleNamesAreChecked(t *testing.T) {
	for _, p := range products {
		status := 204
		for _, tl := range []*tool{branchProtectionsGet, branchProtectionsCreate, branchProtectionsUpdate,
			branchProtectionsDelete} {
			for _, name := range []string{"", ".", "..", "a\\u0001b", "a\\nb", "a\\u007fb", strings.Repeat("a", 256)} {
				e := newEnvironment(t, p, protectionServer(p, &status))
				_, err := e.invoke(t, tl, "repo", `{"rule_name":"`+name+`","required_approvals":1}`)
				refused(t, e, tl.action+" name "+name, err)
			}
			e := newEnvironment(t, p, protectionServer(p, &status))
			_, err := e.invoke(t, tl, "repo", `{"required_approvals":1}`)
			refused(t, e, tl.action+" without a name", err)
		}
	}
}

func TestBranchProtectionProductSpecificMembersAreRefusedBeforeTheToken(t *testing.T) {
	forgejoOnlyMembers := []string{`"apply_to_admins":true`}
	giteaOnlyMembers := []string{`"priority":1`, `"enable_force_push":true`, `"enable_force_push_allowlist":true`,
		`"force_push_allowlist_usernames":["octo"]`, `"force_push_allowlist_teams":["core"]`,
		`"force_push_allowlist_deploy_keys":true`, `"enable_bypass_allowlist":true`,
		`"bypass_allowlist_usernames":["octo"]`, `"bypass_allowlist_teams":["core"]`,
		`"block_admin_merge_override":true`, `"block_on_codeowner_reviews":true`}
	for _, p := range products {
		status := 201
		refusedMembers := giteaOnlyMembers
		if p == gitea {
			refusedMembers = forgejoOnlyMembers
		}
		for _, tl := range []*tool{branchProtectionsCreate, branchProtectionsUpdate} {
			for _, member := range append(refusedMembers, `"branch_name":"main"`, `"unknown":true`) {
				e := newEnvironment(t, p, protectionServer(p, &status))
				_, err := e.invoke(t, tl, "repo", `{"rule_name":"main",`+member+`}`)
				refused(t, e, tl.action+" "+member, err)
			}
		}
		// The input schema names only what the product knows.
		for _, tl := range []*tool{branchProtectionsCreate, branchProtectionsUpdate} {
			schema := string(newEnvironment(t, p, protectionServer(p, &status)).descriptor(t, tl).InputSchema)
			for _, member := range forgejoOnlyMembers {
				name := strings.SplitN(member, ":", 2)[0]
				if strings.Contains(schema, name) != (p == forgejo) {
					t.Errorf("%s %s schema holds %s = %t", p.id, tl.action, name, !(p == forgejo))
				}
			}
			for _, member := range giteaOnlyMembers {
				name := strings.SplitN(member, ":", 2)[0]
				if strings.Contains(schema, name) != (p == gitea) {
					t.Errorf("%s %s schema holds %s = %t", p.id, tl.action, name, !(p == gitea))
				}
			}
			if strings.Contains(schema, `"branch_name"`) {
				t.Errorf("%s %s offers the deprecated branch_name", p.id, tl.action)
			}
		}
	}
}

func TestBranchProtectionCodeownerReviewsNeedGitea28(t *testing.T) {
	p := gitea
	status := 201
	api := func(c call) *http.Response { return protectionServer(p, &status)(c) }
	old := newEnvironment(t, p, giteaAt("1.27.3", api))
	_, err := old.invoke(t, branchProtectionsCreate, "repo", `{"rule_name":"main","block_on_codeowner_reviews":true}`)
	if err == nil || !strings.Contains(err.Error(), "not available") || slices.Contains(methods(old), "POST "+protectionsAPI) {
		t.Errorf("1.27.3: %v %v", err, methods(old))
	}
	// A call without the member needs no newer version than the connection's minimum.
	if _, err := old.invoke(t, branchProtectionsCreate, "repo", `{"rule_name":"main"}`); err != nil {
		t.Errorf("1.27.3 without the member: %v", err)
	}
	current := newEnvironment(t, p, giteaAt("28.2.0", api))
	if _, err := current.invoke(t, branchProtectionsUpdate, "repo",
		`{"rule_name":"main","block_on_codeowner_reviews":true}`); err != nil {
		t.Errorf("28.2.0: %v", err)
	}
}

func TestBranchProtectionCreateSendsOneRequestWithTheGivenMembers(t *testing.T) {
	for _, p := range products {
		status := 201
		e := newEnvironment(t, p, protectionServer(p, &status))
		args := `{"rule_name":"release/*","required_approvals":2,"enable_push":false,"status_check_contexts":["ci/*"],` +
			`"push_whitelist_usernames":[],"protected_file_patterns":"*.lock"}`
		if _, err := e.invokeAs(t, branchProtectionsCreate, "repo", args, false); err == nil || len(*e.calls) != 0 ||
			*e.reads != 0 {
			t.Fatalf("%s: unconfirmed create: %v", p.id, err)
		}
		result, err := e.invoke(t, branchProtectionsCreate, "repo", args)
		if err != nil || result["rule_name"] != "main" || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := `{"enable_push":false,"protected_file_patterns":"*.lock","push_whitelist_usernames":[],` +
			`"required_approvals":2,"rule_name":"release/*","status_check_contexts":["ci/*"]}`
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST " + protectionsAPI}) ||
			(*e.calls)[0].body != want {
			t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[0].body)
		}
		if p == forgejo {
			e = newEnvironment(t, p, protectionServer(p, &status))
			if _, err := e.invoke(t, branchProtectionsCreate, "repo", `{"rule_name":"main","apply_to_admins":true}`); err != nil ||
				(*e.calls)[0].body != `{"apply_to_admins":true,"rule_name":"main"}` {
				t.Errorf("%s: %v %v", p.id, err, *e.calls)
			}
		} else {
			e = newEnvironment(t, p, protectionServer(p, &status))
			args := `{"rule_name":"main","priority":3,"enable_force_push":true,"enable_force_push_allowlist":true,` +
				`"force_push_allowlist_usernames":["octo"],"bypass_allowlist_teams":["core"]}`
			if _, err := e.invoke(t, branchProtectionsCreate, "repo", args); err != nil ||
				(*e.calls)[0].body != `{"bypass_allowlist_teams":["core"],"enable_force_push":true,`+
					`"enable_force_push_allowlist":true,"force_push_allowlist_usernames":["octo"],"priority":3,`+
					`"rule_name":"main"}` {
				t.Errorf("%s: %v %v", p.id, err, *e.calls)
			}
		}
	}
}

func TestBranchProtectionUpdateSendsOnlyTheGivenMembers(t *testing.T) {
	for _, p := range products {
		status := 200
		e := newEnvironment(t, p, protectionServer(p, &status))
		if _, err := e.invokeAs(t, branchProtectionsUpdate, "repo", `{"rule_name":"main","enable_push":true}`,
			false); err == nil || len(*e.calls) != 0 || *e.reads != 0 {
			t.Fatalf("%s: unconfirmed update: %v", p.id, err)
		}
		_, err := e.invoke(t, branchProtectionsUpdate, "repo", `{"rule_name":"main"}`)
		refused(t, e, "update without members", err)
		result, err := e.invoke(t, branchProtectionsUpdate, "repo",
			`{"rule_name":"main","require_signed_commits":false,"required_approvals":0,"merge_whitelist_teams":["core"]}`)
		if err != nil || result["rule_name"] != "main" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"PATCH " + protectionsAPI + "/main"}) ||
			(*e.calls)[0].body != `{"merge_whitelist_teams":["core"],"require_signed_commits":false,"required_approvals":0}` {
			t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[0].body)
		}
	}
}

func TestBranchProtectionMembersAreBounded(t *testing.T) {
	long := strings.Repeat("a", 256)
	many := `[` + strings.TrimSuffix(strings.Repeat(`"a",`, 101), ",") + `]`
	for _, p := range products {
		status := 200
		for _, members := range []string{`"required_approvals":-1`, `"required_approvals":101`, `"required_approvals":1.5`,
			`"required_approvals":"2"`, `"required_approvals":null`, `"enable_push":"yes"`, `"enable_push":null`,
			`"push_whitelist_usernames":"octo"`, `"push_whitelist_usernames":["a/b"]`,
			`"push_whitelist_usernames":["a b"]`, `"push_whitelist_usernames":[""]`, `"push_whitelist_usernames":null`,
			`"push_whitelist_teams":["a\u0001"]`, `"push_whitelist_teams":[""]`, `"merge_whitelist_usernames":` + many,
			`"status_check_contexts":["` + long + `"]`, `"status_check_contexts":["a\nb"]`, `"status_check_contexts":[1]`,
			`"protected_file_patterns":"` + strings.Repeat("a", 4097) + `"`, `"protected_file_patterns":"a\u0000"`,
			`"unprotected_file_patterns":5`} {
			for _, tl := range []*tool{branchProtectionsCreate, branchProtectionsUpdate} {
				e := newEnvironment(t, p, protectionServer(p, &status))
				_, err := e.invoke(t, tl, "repo", `{"rule_name":"main",`+members+`}`)
				refused(t, e, tl.action+" "+members, err)
			}
		}
	}
}

func TestBranchProtectionReorderIsGiteaOnly(t *testing.T) {
	reg := registry(t)
	if _, _, ok := reg.Lookup("forgejo.branch-protections.reorder"); ok {
		t.Error("reorder is registered for Forgejo")
	}
	d, _, ok := reg.Lookup("gitea.branch-protections.reorder")
	if !ok || d.RequiresToolAllowList || d.Risk.Confirmation != capability.ConfirmationRequired ||
		d.Risk.Effect != capability.EffectUpdate {
		t.Fatalf("gitea reorder = %+v", d)
	}
	status := 204
	e := newEnvironment(t, gitea, protectionServer(gitea, &status))
	if _, err := e.invokeAs(t, branchProtectionsReorder, "repo", `{"ids":[3,1,2]}`, false); err == nil ||
		len(*e.calls) != 0 || *e.reads != 0 {
		t.Fatalf("unconfirmed reorder: %v", err)
	}
	for _, args := range []string{`{"ids":[]}`, `{"ids":[1,1]}`, `{"ids":[0]}`, `{"ids":[-1]}`, `{"ids":["1"]}`, `{}`,
		`{"ids":[1],"rule_name":"main"}`} {
		e := newEnvironment(t, gitea, protectionServer(gitea, &status))
		_, err := e.invoke(t, branchProtectionsReorder, "repo", args)
		refused(t, e, "reorder "+args, err)
	}
	e = newEnvironment(t, gitea, protectionServer(gitea, &status))
	result, err := e.invoke(t, branchProtectionsReorder, "repo", `{"ids":[3,1,2]}`)
	if err != nil || result["reordered"] != true || result["repository"] != "acme/widget" {
		t.Fatalf("%v, %v", result, err)
	}
	if got := methods(e); !reflect.DeepEqual(got, []string{"POST " + protectionsAPI + "/priority"}) ||
		(*e.calls)[0].body != `{"ids":[3,1,2]}` {
		t.Errorf("calls = %v body %q", got, (*e.calls)[0].body)
	}
}

func TestBranchProtectionDeleteIsListedOnly(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, protectionServer(p, &status))
		if _, err := e.invoke(t, branchProtectionsDelete, "repo", `{"rule_name":"main"}`); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, branchProtectionsDelete, "protection-delete", `{"rule_name":"main"}`, false); err == nil ||
			len(*e.calls) != 0 || *e.reads != 0 {
			t.Fatalf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, branchProtectionsDelete, "protection-delete", `{"rule_name":"main"}`)
		if err != nil || result["deleted"] != true || result["rule_name"] != "main" || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"DELETE " + protectionsAPI + "/main"}) ||
			(*e.calls)[0].body != "" {
			t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[0].body)
		}
	}
}

func TestBranchProtectionChangesAreNeverRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		for _, tl := range protectionTools[2:] {
			if !registeredFor(p, tl) {
				continue
			}
			args, connection := `{"rule_name":"main","required_approvals":1}`, "repo"
			switch tl {
			case branchProtectionsReorder:
				args = `{"ids":[1,2]}`
			case branchProtectionsDelete:
				args, connection = `{"rule_name":"main"}`, "protection-delete"
			}
			for _, status := range []int{502, 500} {
				e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(status, bodyCanary) })
				_, err := e.invoke(t, tl, connection, args)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
					t.Errorf("%s %s %d: %v after %d calls", p.id, tl.action, status, err, len(*e.calls))
				}
			}
		}
	}
}

func TestBranchProtectionToolsRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range protectionTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if ok != registeredFor(p, tl) {
				t.Fatalf("%s registered = %t", p.toolID(tl), ok)
			}
			if !ok {
				continue
			}
			change := tl != branchProtectionsList && tl != branchProtectionsGet
			effect := map[*tool]capability.Effect{branchProtectionsList: capability.EffectRead,
				branchProtectionsGet: capability.EffectRead, branchProtectionsCreate: capability.EffectCreate,
				branchProtectionsUpdate: capability.EffectUpdate, branchProtectionsReorder: capability.EffectUpdate,
				branchProtectionsDelete: capability.EffectDelete}[tl]
			if d.Risk.Effect != effect || d.Risk.Idempotency == "" || !d.Risk.OpenWorld ||
				d.Risk.DataSensitivity != p.id+"-access-data" ||
				(change != (d.Risk.Confirmation == capability.ConfirmationRequired)) ||
				d.RequiresToolAllowList != (tl == branchProtectionsDelete) {
				t.Errorf("%s risk = %+v listed-only %t", d.ID, d.Risk, d.RequiresToolAllowList)
			}
			if (tl == branchProtectionsCreate) != (d.Risk.Idempotency == capability.IdempotencyNonIdempotent) && change {
				t.Errorf("%s idempotency = %s", d.ID, d.Risk.Idempotency)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) && change {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
	}
}

func (e *environment) descriptor(t *testing.T, tl *tool) capability.Descriptor {
	t.Helper()
	d, _, ok := registry(t).Lookup(e.p.toolID(tl))
	if !ok {
		t.Fatalf("%s is missing", e.p.toolID(tl))
	}
	return d
}
