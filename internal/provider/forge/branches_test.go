package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func branchFixture(name string) string {
	return `{"name":"` + name + `","protected":true,"required_approvals":2,"enable_status_check":true,` +
		`"status_check_contexts":["ci"],"user_can_push":false,"user_can_merge":true,` +
		`"effective_branch_protection_name":"main-rule","commit":{"id":"c0ffee1","message":"fix","timestamp":"2030-01-02T03:04:05Z",` +
		`"url":"https://forge.example.test/acme/widget/commit/c0ffee1","added":["a.go"],"modified":[],"removed":[],` +
		`"author":{"name":"Ann","username":"ann","email":"ann@example.test"},` +
		`"committer":{"name":"Ann","username":"ann","email":"ann@example.test"},` +
		`"verification":{"verified":true,"reason":"ok","signature":"SIG","payload":"PAY",` +
		`"signer":{"name":"Ann","username":"ann","email":"ann@example.test"}}}}`
}

func branchServer(c call) *http.Response {
	const base = "/api/v1/repos/acme/widget/branches"
	switch {
	case c.path == base && c.method == http.MethodGet:
		return jsonResponse(200, `[`+branchFixture("main")+`]`, "Link", `<x?page=2>; rel="next"`)
	case c.path == base && c.method == http.MethodPost:
		return jsonResponse(201, branchFixture("feature/x"))
	case strings.HasPrefix(c.path, base+"/") && c.method == http.MethodGet:
		return jsonResponse(200, branchFixture(strings.TrimPrefix(c.path, base+"/")))
	case strings.HasPrefix(c.path, base+"/"):
		return jsonResponse(204, "")
	}
	return notFoundResponse()
}

func branchEnvironment(t *testing.T, p *product, handler func(call) *http.Response) *environment {
	e := newEnvironment(t, p, handler)
	cfg := testConfig(p)
	connection := cfg.Connections["delete"]
	connection.Tools = []string{p.id + ".branches.delete"}
	if p == gitea {
		connection.Tools = append(connection.Tools, p.id+".branches.update-ref")
	}
	cfg.Connections["branch-listed"] = connection
	red := &redact.Redactor{}
	e.core = application.New(registry(t), cfg, resolver(red, e.reads), red)
	e.red = red
	return e
}

func TestBranchListAndGetShowEveryMember(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, branchServer)
		result, err := e.invoke(t, branchesList, "repo", `{"limit":10}`)
		if err != nil || result["has_more"] != true || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		branch := result["branches"].([]any)[0].(map[string]any)
		commit := branch["commit"].(map[string]any)
		verification := commit["verification"].(map[string]any)
		if branch["protected"] != true || branch["effective_branch_protection_name"] != "main-rule" ||
			commit["id"] != "c0ffee1" || commit["author"].(map[string]any)["email"] != "ann@example.test" ||
			verification["verified"] != true || verification["signature"] != nil || verification["payload"] != nil {
			t.Errorf("%s branch = %v", p.id, branch)
		}
		got, err := e.invoke(t, branchesGet, "repo", `{"branch":"release/1.0"}`)
		if err != nil || got["name"] != "release/1.0" {
			t.Fatalf("%s get: %v, %v", p.id, got, err)
		}
		if last := (*e.calls)[len(*e.calls)-1]; last.escaped != "/api/v1/repos/acme/widget/branches/release%2F1.0" {
			t.Errorf("%s escaped path = %q", p.id, last.escaped)
		}
	}
}

func TestBranchListFilterIsGiteaOnly(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, branchServer)
		_, err := e.invoke(t, branchesList, "repo", `{"q":"feat"}`)
		if p == forgejo {
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 {
				t.Errorf("forgejo accepted q: %v", err)
			}
			continue
		}
		if err != nil || !strings.Contains((*e.calls)[0].query, "q=feat") {
			t.Errorf("gitea q: %v, %v", err, *e.calls)
		}
	}
}

func TestBranchOutsideTheTargetsAndBadNamesAreRefusedBeforeIO(t *testing.T) {
	for _, p := range products {
		e := branchEnvironment(t, p, branchServer)
		outside := map[*tool]string{branchesList: `{"repository":"secret-owner/hidden"}`,
			branchesGet:    `{"repository":"secret-owner/hidden","branch":"main"}`,
			branchesCreate: `{"repository":"secret-owner/hidden","new_branch_name":"x"}`,
			branchesRename: `{"repository":"secret-owner/hidden","branch":"a","name":"b"}`}
		for tl, args := range outside {
			_, err := e.invoke(t, tl, "repo", args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || *e.reads != 0 || len(*e.calls) != 0 {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
		for _, tl := range []*tool{branchesDelete, branchesUpdateRef} {
			if tl == branchesUpdateRef && p == forgejo {
				continue
			}
			args := `{"repository":"secret-owner/hidden","branch":"a","new_commit_id":"abcdef1"}`
			if tl == branchesDelete {
				args = `{"repository":"secret-owner/hidden","branch":"a"}`
			}
			if _, err := e.invoke(t, tl, "branch-listed", args); !isInvalidRequest(err) || *e.reads != 0 || len(*e.calls) != 0 {
				t.Errorf("%s %s outside: %v", p.id, tl.action, err)
			}
		}
		for _, name := range []string{"", "a b", "a..b", "a\nb", "/a", "a/", "a//b", strings.Repeat("a", 256)} {
			arg, _ := jsonString(name)
			for tl, args := range map[*tool]string{branchesGet: `{"branch":` + arg + `}`,
				branchesCreate: `{"new_branch_name":` + arg + `}`, branchesRename: `{"branch":"a","name":` + arg + `}`} {
				if _, err := e.invoke(t, tl, "repo", args); err == nil {
					t.Errorf("%s %s accepted %q", p.id, tl.action, name)
				}
			}
			if _, err := e.invoke(t, branchesDelete, "branch-listed", `{"branch":`+arg+`}`); err == nil {
				t.Errorf("%s delete accepted %q", p.id, name)
			}
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: a refused name reached the instance", p.id)
		}
	}
}

func TestBranchMutationsSendOneRequest(t *testing.T) {
	for _, p := range products {
		e := branchEnvironment(t, p, branchServer)
		if _, err := e.invokeAs(t, branchesCreate, "repo", `{"new_branch_name":"x"}`, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed create: %v", p.id, err)
		}
		if _, err := e.invoke(t, branchesCreate, "repo", `{"new_branch_name":"feature/x","old_ref_name":"main"}`); err != nil {
			t.Fatalf("%s create: %v", p.id, err)
		}
		if body := bodyOf(t, (*e.calls)[0]); !reflect.DeepEqual(body, map[string]any{"new_branch_name": "feature/x",
			"old_ref_name": "main"}) {
			t.Errorf("%s create body = %v", p.id, body)
		}
		result, err := e.invoke(t, branchesRename, "repo", `{"branch":"feature/x","name":"feature/y"}`)
		if err != nil || result["renamed"] != true {
			t.Fatalf("%s rename: %v, %v", p.id, result, err)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodPatch || last.escaped != "/api/v1/repos/acme/widget/branches/feature%2Fx" ||
			!reflect.DeepEqual(bodyOf(t, last), map[string]any{"name": "feature/y"}) {
			t.Errorf("%s rename call = %+v", p.id, last)
		}
		if len(*e.calls) != 2 {
			t.Errorf("%s: %d calls", p.id, len(*e.calls))
		}
	}
}

func TestBranchDeleteAndUpdateRefAreListedOnly(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{branchesDelete, branchesUpdateRef} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if tl == branchesUpdateRef && p == forgejo {
				if ok {
					t.Errorf("forgejo registers update-ref")
				}
				continue
			}
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Fatalf("%s %s descriptor = %+v", p.id, tl.action, d)
			}
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if strings.HasPrefix(id, p.id+".branches.") {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
				}
			}
		}
		e := branchEnvironment(t, p, branchServer)
		if _, err := e.invoke(t, branchesDelete, "repo", `{"branch":"x"}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, branchesDelete, "branch-listed", `{"branch":"x"}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, branchesDelete, "branch-listed", `{"branch":"feature/x"}`)
		if err != nil || result["deleted"] != true {
			t.Fatalf("%s delete: %v, %v", p.id, result, err)
		}
		if last := (*e.calls)[0]; last.method != http.MethodDelete || last.escaped != "/api/v1/repos/acme/widget/branches/feature%2Fx" {
			t.Errorf("%s delete call = %+v", p.id, last)
		}
		if p != gitea {
			continue
		}
		if _, err := e.invoke(t, branchesUpdateRef, "repo", `{"branch":"x","new_commit_id":"abcdef1"}`); err == nil {
			t.Errorf("update-ref without a tools list")
		}
		if _, err := e.invoke(t, branchesUpdateRef, "branch-listed", `{"branch":"x","new_commit_id":"zz"}`); !isInvalidRequest(err) {
			t.Errorf("bad commit id: %v", err)
		}
		if _, err := e.invoke(t, branchesUpdateRef, "branch-listed", `{"branch":"x","new_commit_id":"abcdef1","force":true}`); err == nil {
			t.Errorf("force accepted")
		}
		n := len(*e.calls)
		result, err = e.invoke(t, branchesUpdateRef, "branch-listed",
			`{"branch":"release/1","new_commit_id":"abcdef1","old_commit_id":"1234567"}`)
		if err != nil || result["updated"] != true || len(*e.calls) != n+1 {
			t.Fatalf("update-ref: %v, %v", result, err)
		}
		last := (*e.calls)[n]
		if last.method != http.MethodPut || last.escaped != "/api/v1/repos/acme/widget/branches/release%2F1" ||
			!reflect.DeepEqual(bodyOf(t, last), map[string]any{"new_commit_id": "abcdef1", "old_commit_id": "1234567"}) {
			t.Errorf("update-ref call = %+v", last)
		}
	}
}

func TestBranchChangeWithAnOpenOutcomeIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(500, `{"message":"`+bodyCanary+`"}`) })
		_, err := e.invoke(t, branchesRename, "repo", `{"branch":"a","name":"b"}`)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
			t.Errorf("%s: %v, calls %d", p.id, err, len(*e.calls))
		}
	}
}
