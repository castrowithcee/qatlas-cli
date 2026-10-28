package forge

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// lifecycleListedOnly are the tools only a connection whose tools list names them offers.
func lifecycleListedOnly(p *product) []string {
	ids := []string{p.toolID(repositoriesDelete), p.toolID(repositoriesTransfer),
		p.toolID(repositoriesAcceptTransfer), p.toolID(repositoriesRejectTransfer)}
	if p == forgejo {
		ids = append(ids, p.toolID(repositoriesConvert))
	}
	return ids
}

func lifecycleRepo(full string) string {
	owner, name, _ := strings.Cut(full, "/")
	return `{"id":7,"name":"` + name + `","full_name":"` + full + `","owner":{"id":1,"login":"` + owner + `"},` +
		`"private":true,"fork":false,"default_branch":"main","html_url":"https://forge.example.test/` + full + `"}`
}

// newLifecycleEnvironment adds the connections these tests need: life lists the listed-only tools for one
// repository, life-owners also names an owner, files releases a directory for reading, execute may execute.
func newLifecycleEnvironment(t *testing.T, p *product, handler func(call) *http.Response) (*environment, string) {
	t.Helper()
	dir := t.TempDir()
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	cfg := testConfig(p)
	cfg.Connections["life"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: lifecycleListedOnly(p)}
	cfg.Connections["life-owners"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget", "orgs/dest"}, Tools: lifecycleListedOnly(p)}
	cfg.Connections["widget-dest"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget", "orgs/dest"}}
	cfg.Connections["files"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Files: config.Files{Read: []string{dir}}}
	cfg.Connections["execute"] = config.Connection{Service: "forge", Credential: "token",
		Effects: append(slices.Clone(allEffects), config.EffectExecute), Targets: []string{"repos/acme/widget"}}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}, dir
}

// refused checks that a call failed as an invalid request without a request or a read of the token.
func refused(t *testing.T, e *environment, label string, err error) {
	t.Helper()
	if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 {
		t.Errorf("%s %s: err %v, calls %v, token reads %d", e.p.id, label, err, methods(e), *e.reads)
	}
}

func accountServer(login string, answer func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		if c.path == "/api/v1/user" {
			return jsonResponse(200, `{"id":1,"login":"`+login+`"}`)
		}
		return answer(c)
	}
}

func TestRepositoryCreateBindsTheNewOwnerBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		created := func(c call) *http.Response { return jsonResponse(201, lifecycleRepo("acme/project")) }
		e, _ := newLifecycleEnvironment(t, p, accountServer("alice", created))
		args := `{"organization":"acme","name":"project","private":true,"auto_init":true,"trust_model":"default"}`
		// Unconfirmed, outside organization targets, a repository target, and an unnamed account are all refused
		// before the token is read.
		if _, err := e.invokeAs(t, repositoriesCreate, "org", args, false); err == nil || len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: unconfirmed create: %v", p.id, err)
		}
		_, err := e.invoke(t, repositoriesCreate, "org", `{"organization":"other","name":"project"}`)
		refused(t, e, "other organization", err)
		_, err = e.invoke(t, repositoriesCreate, "repo", args)
		refused(t, e, "repository target", err)
		_, err = e.invoke(t, repositoriesCreate, "org", `{"name":"project"}`)
		refused(t, e, "account without a user target", err)
		_, err = e.invoke(t, repositoriesCreate, "owner", `{"organization":"alice","name":"project"}`)
		refused(t, e, "user as organization", err)
		for _, bad := range []string{`{"name":"a/b"}`, `{"name":".."}`, `{"name":"x","trust_model":"none"}`,
			`{"name":"x","default_branch":"a..b"}`, `{"name":"x","mirror":true}`, `{"name":"x","original_url":"https://x.example.invalid"}`} {
			_, err = e.invoke(t, repositoriesCreate, "open", bad)
			refused(t, e, bad, err)
		}

		result, err := e.invoke(t, repositoriesCreate, "org", args)
		if err != nil || result["full_name"] != "acme/project" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST /api/v1/orgs/acme/repos"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		body := bodyOf(t, (*e.calls)[0])
		if body["name"] != "project" || body["private"] != true || body["auto_init"] != true ||
			body["trust_model"] != "default" || len(body) != 4 {
			t.Errorf("%s body = %v", p.id, body)
		}

		// The account behind the token is proven before the change.
		*e.calls = nil
		result, err = e.invoke(t, repositoriesCreate, "owner", `{"name":"project"}`)
		if err != nil || result["full_name"] != "acme/project" ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/user", "POST /api/v1/user/repos"}) {
			t.Fatalf("%s own account: %v, %v, %v", p.id, result, err, methods(e))
		}
		e2, _ := newLifecycleEnvironment(t, p, accountServer("bob", created))
		if _, err := e2.invoke(t, repositoriesCreate, "owner", `{"name":"project"}`); !isInvalidRequest(err) ||
			!reflect.DeepEqual(methods(e2), []string{"GET /api/v1/user"}) {
			t.Errorf("%s other account: %v, %v", p.id, err, methods(e2))
		}
		e3, _ := newLifecycleEnvironment(t, p, accountServer("bob", created))
		if _, err := e3.invoke(t, repositoriesCreate, "open", `{"name":"project"}`); err != nil ||
			!reflect.DeepEqual(methods(e3), []string{"POST /api/v1/user/repos"}) {
			t.Errorf("%s open connection: %v, %v", p.id, err, methods(e3))
		}
	}
}

func TestRepositoryChangeWithAnOpenOutcomeIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		e, _ := newLifecycleEnvironment(t, p, func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		_, err := e.invoke(t, repositoriesCreate, "open", `{"organization":"acme","name":"project"}`)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
			t.Errorf("%s: %v, %d calls", p.id, err, len(*e.calls))
		}
	}
}

func TestRepositoryUpdateOffersNoMirrorCredentialNorExternalService(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		d, _, _ := reg.Lookup(p.toolID(repositoriesUpdate))
		schema := string(d.InputSchema)
		for _, never := range []string{"mirror_username", "mirror_password", "mirror_token", "external_tracker",
			"external_wiki"} {
			if strings.Contains(schema, never) {
				t.Errorf("%s offers %s", p.id, never)
			}
		}
		for name, only := range map[string]*product{"wiki_branch": forgejo, "globally_editable_wiki": forgejo,
			"projects_mode": gitea, "has_code": gitea, "allow_manual_merge": gitea} {
			if strings.Contains(schema, `"`+name+`"`) != (only == p) {
				t.Errorf("%s: %s is %s only", p.id, name, only.id)
			}
		}
		if strings.Contains(schema, "rebase-update-only") != (p == forgejo) {
			t.Errorf("%s: rebase-update-only is Forgejo's merge style", p.id)
		}

		e, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response {
			return jsonResponse(200, lifecycleRepo("acme/widget"))
		})
		for _, bad := range []string{
			`{"mirror_username":"x"}`, `{"mirror_password":"x"}`, `{"mirror_token":"x"}`,
			`{"external_tracker":{"external_tracker_url":"https://x.example.invalid"}}`,
			`{"external_wiki":{"external_wiki_url":"https://x.example.invalid"}}`, `{"archived":"yes"}`,
			`{"default_merge_style":"nonsense"}`, `{"internal_tracker":{"enable_time_tracker":true}}`, `{}`,
			`{"repository":"other/place","archived":true}`, `{"name":"a/b"}`, `{"description":"line\nbreak"}`,
		} {
			_, err := e.invoke(t, repositoriesUpdate, "repo", bad)
			refused(t, e, bad, err)
		}
	}
}

func TestRepositoryUpdateSendsOnlyTheGivenSettings(t *testing.T) {
	for _, p := range products {
		answer := lifecycleRepo("acme/widget")
		e, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, answer) })
		if _, err := e.invokeAs(t, repositoriesUpdate, "repo", `{"archived":true}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed update: %v", p.id, err)
		}
		args := `{"description":"A tool","archived":false,"default_merge_style":"squash",` +
			`"internal_tracker":{"enable_time_tracker":true,"allow_only_contributors_to_track_time":false,` +
			`"enable_issue_dependencies":true}`
		if p == forgejo {
			args += `,"wiki_branch":"docs","globally_editable_wiki":true}`
		} else {
			args += `,"projects_mode":"owner","has_code":true,"allow_manual_merge":false}`
		}
		result, err := e.invoke(t, repositoriesUpdate, "repo", args)
		if err != nil || result["updated"] != true || result["reachable"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"PATCH /api/v1/repos/acme/widget"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		body := bodyOf(t, (*e.calls)[0])
		want := 7
		if p == forgejo {
			want = 6
		}
		if body["description"] != "A tool" || body["archived"] != false || body["default_merge_style"] != "squash" ||
			len(body) != want {
			t.Errorf("%s body = %v", p.id, body)
		}
		for key := range body {
			if strings.HasPrefix(key, "mirror_") || strings.HasPrefix(key, "external_") || key == "repository" {
				t.Errorf("%s sends %s", p.id, key)
			}
		}

		// A rename that leaves a repository target shows no details of the new name.
		answer = lifecycleRepo("acme/renamed")
		result, err = e.invoke(t, repositoriesUpdate, "repo", `{"name":"renamed"}`)
		if err != nil || result["updated"] != true || result["reachable"] != false || result["repository"] != nil {
			t.Errorf("%s rename: %v, %v", p.id, result, err)
		}
	}
}

func TestRepositoryUpdateRefusesTheOtherProductsFields(t *testing.T) {
	foreign := map[*product][]string{
		forgejo: {`"projects_mode":"all"`, `"has_code":true`, `"allow_manual_merge":true`},
		gitea:   {`"wiki_branch":"docs"`, `"globally_editable_wiki":true`},
	}
	for _, p := range products {
		e, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, lifecycleRepo("acme/widget")) })
		for _, field := range append(foreign[p], `"mirror_username":"x"`, `"mirror_password":"x"`,
			`"mirror_token":"x"`, `"external_tracker":{}`, `"external_wiki":{}`) {
			_, err := e.invoke(t, repositoriesUpdate, "repo", `{`+field+`}`)
			refused(t, e, field, err)
			// The handler refuses it as well when the schema was not applied.
			arguments := json.RawMessage(`{"repository":"acme/widget",` + field + `}`)
			if _, err := repositoriesUpdate.prepare(p, allowlist{}, arguments); !isInvalidRequest(err) {
				t.Errorf("%s: handler accepted %s: %v", p.id, field, err)
			}
		}
	}
}

func TestRepositoryForkChecksSourceAndDestinationBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		forked := func(c call) *http.Response { return jsonResponse(202, lifecycleRepo("acme/widget-fork")) }
		e, _ := newLifecycleEnvironment(t, p, accountServer("alice", forked))
		_, err := e.invoke(t, repositoriesFork, "repo", `{"repository":"other/place","organization":"acme"}`)
		refused(t, e, "other source", err)
		_, err = e.invoke(t, repositoriesFork, "repo", `{"organization":"other"}`)
		refused(t, e, "other organization", err)
		_, err = e.invoke(t, repositoriesFork, "repo", `{}`)
		refused(t, e, "account without a user target", err)
		_, err = e.invoke(t, repositoriesFork, "org", `{"repository":"acme/widget","organization":"other","name":"x"}`)
		refused(t, e, "other organization of an org connection", err)
		_, err = e.invoke(t, repositoriesFork, "org", `{"repository":"acme/widget","name":"a/b"}`)
		refused(t, e, "bad name", err)

		result, err := e.invoke(t, repositoriesFork, "org", `{"repository":"acme/widget","organization":"acme","name":"widget-fork"}`)
		if err != nil || result["full_name"] != "acme/widget-fork" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		body := bodyOf(t, (*e.calls)[0])
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST /api/v1/repos/acme/widget/forks"}) ||
			body["organization"] != "acme" || body["name"] != "widget-fork" || len(body) != 2 {
			t.Errorf("%s calls = %v, body %v", p.id, got, body)
		}
		// Into the own account: proven first.
		e2, _ := newLifecycleEnvironment(t, p, accountServer("alice", forked))
		if _, err := e2.invoke(t, repositoriesFork, "mixed", `{"repository":"acme/widget"}`); err != nil ||
			!reflect.DeepEqual(methods(e2), []string{"GET /api/v1/user", "POST /api/v1/repos/acme/widget/forks"}) ||
			(*e2.calls)[1].body != `{}` {
			t.Errorf("%s own account: %v, %v", p.id, err, methods(e2))
		}
	}
}

func TestRepositoryListForksPagesByHeadersAndChecksRows(t *testing.T) {
	for _, p := range products {
		e, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response {
			if strings.Contains(c.query, "page=2") {
				return jsonResponse(200, `[`+lifecycleRepo("acme/third")+`]`, "X-Total-Count", "3")
			}
			return jsonResponse(200, `[`+lifecycleRepo("acme/first")+`,`+lifecycleRepo("other/second")+`]`,
				"X-Total-Count", "3")
		})
		result, err := e.invoke(t, repositoriesListForks, "org", `{"repository":"acme/widget","limit":2}`)
		rows, _ := result["repositories"].([]any)
		if err != nil || len(rows) != 1 || rows[0].(map[string]any)["full_name"] != "acme/first" ||
			result["has_more"] != true || result["repository"] != "acme/widget" {
			t.Fatalf("%s page 1: %v, %v", p.id, result, err)
		}
		cursor, _ := result["next_cursor"].(string)
		result, err = e.invoke(t, repositoriesListForks, "org", `{"repository":"acme/widget","cursor":"`+cursor+`"}`)
		rows, _ = result["repositories"].([]any)
		if err != nil || len(rows) != 1 || result["has_more"] != false || result["next_cursor"] != nil {
			t.Fatalf("%s page 2: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/forks",
			"GET /api/v1/repos/acme/widget/forks"}) || (*e.calls)[0].query != "limit=2&page=1" || (*e.calls)[1].query != "limit=2&page=2" {
			t.Errorf("%s calls = %v %q", p.id, got, (*e.calls)[1].query)
		}
		if _, err := e.invoke(t, repositoriesListForks, "repo", `{"repository":"other/place"}`); !isInvalidRequest(err) {
			t.Errorf("%s: fork list of another repository: %v", p.id, err)
		}
		// A Link header decides, whatever the count says.
		e2, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response {
			return jsonResponse(200, `[`+lifecycleRepo("acme/first")+`]`, "Link", `<x?page=2>; rel="next"`)
		})
		result, err = e2.invoke(t, repositoriesListForks, "repo", `{}`)
		if err != nil || result["has_more"] != true {
			t.Errorf("%s link: %v, %v", p.id, result, err)
		}
	}
}

func TestRepositoryGenerateBindsTemplateAndOwner(t *testing.T) {
	for _, p := range products {
		e, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response { return jsonResponse(201, lifecycleRepo("dest/new")) })
		for _, bad := range []string{
			`{"template":"other/starter","owner":"orgs/dest","name":"new"}`,
			`{"template":"acme/widget","owner":"orgs/elsewhere","name":"new"}`,
			`{"template":"acme/widget","owner":"users/dest","name":"new"}`,
			`{"template":"acme/widget","owner":"dest","name":"new"}`,
			`{"template":"acme/widget","owner":"orgs/dest","name":"a/b"}`,
			`{"owner":"orgs/dest","name":"new"}`,
		} {
			_, err := e.invoke(t, repositoriesGenerate, "widget-dest", bad)
			refused(t, e, bad, err)
		}
		result, err := e.invoke(t, repositoriesGenerate, "widget-dest",
			`{"template":"acme/widget","owner":"orgs/dest","name":"new","private":true,"git_content":true,"topics":false}`)
		if err != nil || result["full_name"] != "dest/new" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		body := bodyOf(t, (*e.calls)[0])
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST /api/v1/repos/acme/widget/generate"}) ||
			body["owner"] != "dest" || body["name"] != "new" || body["private"] != true ||
			body["git_content"] != true || body["topics"] != false || len(body) != 5 {
			t.Errorf("%s calls = %v, body %v", p.id, got, body)
		}
	}
}

func TestForkSyncUsesTheProductsOwnPath(t *testing.T) {
	for _, p := range products {
		e, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response {
			switch {
			case c.method == http.MethodGet:
				return jsonResponse(200, `{"allowed":true,"base_commit":"abc","commits_behind":3,"fork_commit":"def"}`)
			case strings.HasSuffix(c.path, "/merge-upstream"):
				return jsonResponse(200, `{"merge_type":"fast-forward"}`)
			}
			return jsonResponse(204, "")
		})
		if _, err := e.invoke(t, repositoriesSyncFork, "repo", `{"branch":"a..b"}`); !isInvalidRequest(err) {
			t.Errorf("%s: bad branch: %v", p.id, err)
		}
		result, err := e.invoke(t, repositoriesSyncFork, "repo", `{}`)
		if err != nil || result["synced"] != true || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, repositoriesSyncFork, "repo", `{"branch":"dev/x"}`)
		if err != nil || result["synced"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		calls := *e.calls
		if p == forgejo {
			if calls[0].path != "/api/v1/repos/acme/widget/sync_fork" || calls[1].escaped != "/api/v1/repos/acme/widget/sync_fork/dev%2Fx" ||
				calls[0].body != "" || calls[1].body != "" || result["merge_type"] != nil {
				t.Errorf("forgejo calls = %+v", calls)
			}
			if _, err := e.invoke(t, repositoriesSyncFork, "repo", `{"ff_only":true}`); !isInvalidRequest(err) {
				t.Errorf("forgejo: ff_only: %v", err)
			}
			state, err := e.invoke(t, repositoriesGetSyncState, "repo", `{"branch":"dev/x"}`)
			if err != nil || state["commits_behind"] != float64(3) || state["allowed"] != true || state["base_commit"] != "abc" ||
				state["fork_commit"] != "def" {
				t.Errorf("forgejo state: %v, %v", state, err)
			}
			if _, err := e.invoke(t, repositoriesGetSyncState, "repo", `{}`); err != nil ||
				(*e.calls)[len(*e.calls)-1].path != "/api/v1/repos/acme/widget/sync_fork" ||
				(*e.calls)[len(*e.calls)-2].escaped != "/api/v1/repos/acme/widget/sync_fork/dev%2Fx" {
				t.Errorf("forgejo state calls = %v", methods(e))
			}
		} else {
			if calls[0].path != "/api/v1/repos/acme/widget/merge-upstream" || calls[0].body != `{}` ||
				calls[1].body != `{"branch":"dev/x"}` || result["merge_type"] != "fast-forward" {
				t.Errorf("gitea calls = %+v, %v", calls, result)
			}
			if _, err := e.invoke(t, repositoriesSyncFork, "repo", `{"branch":"main","ff_only":true}`); err != nil ||
				(*e.calls)[len(*e.calls)-1].body != `{"branch":"main","ff_only":true}` {
				t.Errorf("gitea ff_only: %v, %v", err, methods(e))
			}
		}
	}
}

func TestSyncStateAndConvertExistOnForgejoOnly(t *testing.T) {
	reg := registry(t)
	for _, only := range []*tool{repositoriesGetSyncState, repositoriesConvert} {
		if _, _, ok := reg.Lookup(forgejo.toolID(only)); !ok {
			t.Errorf("%s is missing", forgejo.toolID(only))
		}
		if _, _, ok := reg.Lookup(gitea.toolID(only)); ok {
			t.Errorf("%s is registered for Gitea", gitea.toolID(only))
		}
	}
	for _, both := range []*tool{repositoriesCreate, repositoriesUpdate, repositoriesFork, repositoriesListForks,
		repositoriesGenerate, repositoriesSyncFork, repositoriesDelete, repositoriesTransfer,
		repositoriesAcceptTransfer, repositoriesRejectTransfer, repositoryMirrorsSync, repositoryAvatarsUpdate,
		repositoryAvatarsDelete} {
		for _, p := range products {
			if _, _, ok := reg.Lookup(p.toolID(both)); !ok {
				t.Errorf("%s is missing", p.toolID(both))
			}
		}
	}
}

func TestDestructiveRepositoryToolsAreListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		listed := lifecycleListedOnly(p)
		for _, tl := range []*tool{repositoriesCreate, repositoriesUpdate, repositoriesFork, repositoriesListForks,
			repositoriesGenerate, repositoriesSyncFork, repositoriesGetSyncState, repositoryMirrorsSync,
			repositoryAvatarsUpdate, repositoryAvatarsDelete, repositoriesConvert, repositoriesDelete,
			repositoriesTransfer, repositoriesAcceptTransfer, repositoriesRejectTransfer} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				continue
			}
			if d.RequiresToolAllowList != slices.Contains(listed, d.ID) {
				t.Errorf("%s listed-only = %v", d.ID, d.RequiresToolAllowList)
			}
			change := d.Risk.Effect != "read"
			if change != (d.Risk.Confirmation == "required") || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
		for tl, effect := range map[*tool]string{repositoriesDelete: "delete", repositoriesTransfer: "update",
			repositoryMirrorsSync: "execute", repositoryAvatarsDelete: "delete", repositoriesCreate: "create"} {
			if d, _, _ := reg.Lookup(p.toolID(tl)); string(d.Risk.Effect) != effect {
				t.Errorf("%s effect = %s", d.ID, d.Risk.Effect)
			}
		}

		e, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response {
			switch {
			case c.method == http.MethodDelete:
				return jsonResponse(204, "")
			case strings.HasSuffix(c.path, "/convert"):
				return jsonResponse(200, lifecycleRepo("acme/widget"))
			case strings.HasSuffix(c.path, "/transfer"):
				return jsonResponse(202, lifecycleRepo("dest/widget"))
			case strings.HasSuffix(c.path, "/transfer/accept"):
				return jsonResponse(202, lifecycleRepo("dest/widget"))
			case strings.HasSuffix(c.path, "/transfer/reject"):
				return jsonResponse(200, lifecycleRepo("acme/widget"))
			}
			return notFoundResponse()
		})
		// Without the tools list a connection does not offer them.
		for _, tl := range []*tool{repositoriesDelete, repositoriesTransfer, repositoriesAcceptTransfer,
			repositoriesRejectTransfer} {
			args := `{"repository":"acme/widget","new_owner":"orgs/dest"}`
			if _, err := e.invoke(t, tl, "repo", args); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s: %s offered without a tools list: %v", p.id, tl.action, err)
			}
			if _, err := e.invokeAs(t, tl, "life-owners", args, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s: %s unconfirmed: %v", p.id, tl.action, err)
			}
		}
		result, err := e.invoke(t, repositoriesDelete, "life", `{}`)
		if err != nil || result["deleted"] != true || result["repository"] != "acme/widget" ||
			!reflect.DeepEqual(methods(e), []string{"DELETE /api/v1/repos/acme/widget"}) {
			t.Fatalf("%s delete: %v, %v, %v", p.id, result, err, methods(e))
		}
		*e.calls = nil
		// The new owner must be named by the targets; a repository target alone names none.
		for _, bad := range []string{`{"new_owner":"orgs/elsewhere"}`, `{"new_owner":"orgs/acme"}`, `{"new_owner":"dest"}`,
			`{"new_owner":"orgs/dest","team_ids":[0]}`} {
			_, err := e.invoke(t, repositoriesTransfer, "life-owners", bad)
			if !isInvalidRequest(err) || len(*e.calls) != 0 {
				t.Errorf("%s: transfer %s: %v, %v", p.id, bad, err, methods(e))
			}
		}
		if _, err := e.invoke(t, repositoriesTransfer, "life", `{"new_owner":"orgs/dest"}`); !isInvalidRequest(err) {
			t.Errorf("%s: transfer with a repository target only: %v", p.id, err)
		}
		before := *e.reads
		result, err = e.invoke(t, repositoriesTransfer, "life-owners", `{"new_owner":"orgs/dest","team_ids":[4,5]}`)
		if err != nil || result["transferred"] != true || result["reachable"] != true {
			t.Fatalf("%s transfer: %v, %v", p.id, result, err)
		}
		if body := bodyOf(t, (*e.calls)[0]); body["new_owner"] != "dest" || len(body) != 2 ||
			!reflect.DeepEqual(body["team_ids"], []any{float64(4), float64(5)}) || *e.reads == before {
			t.Errorf("%s transfer body = %v", p.id, body)
		}
		// Accepting hands the repository to an owner the targets do not reach: no details.
		result, err = e.invoke(t, repositoriesAcceptTransfer, "life", `{}`)
		if err != nil || result["accepted"] != true || result["reachable"] != false || result["repository"] != nil {
			t.Errorf("%s accept: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, repositoriesRejectTransfer, "life", `{}`)
		if err != nil || result["rejected"] != true || result["reachable"] != true ||
			result["repository"].(map[string]any)["full_name"] != "acme/widget" {
			t.Errorf("%s reject: %v, %v", p.id, result, err)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodPost || last.path != "/api/v1/repos/acme/widget/transfer/reject" || last.body != "" {
			t.Errorf("%s reject call = %+v", p.id, last)
		}
		if p == forgejo {
			result, err = e.invoke(t, repositoriesConvert, "life", `{}`)
			if err != nil || result["full_name"] != "acme/widget" ||
				(*e.calls)[len(*e.calls)-1].path != "/api/v1/repos/acme/widget/convert" {
				t.Errorf("forgejo convert: %v, %v", result, err)
			}
		}
	}
}

func TestMirrorSyncAndAvatarDeleteAreSingleFixedRequests(t *testing.T) {
	for _, p := range products {
		e, _ := newLifecycleEnvironment(t, p, func(c call) *http.Response {
			if c.method == http.MethodDelete {
				return jsonResponse(204, "")
			}
			return jsonResponse(200, "")
		})
		if _, err := e.invoke(t, repositoryMirrorsSync, "repo", `{}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: execute without the effect: %v", p.id, err)
		}
		result, err := e.invoke(t, repositoryMirrorsSync, "execute", `{}`)
		if err != nil || result["requested"] != true || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, repositoryAvatarsDelete, "repo", `{}`)
		if err != nil || result["deleted"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST /api/v1/repos/acme/widget/mirror-sync",
			"DELETE /api/v1/repos/acme/widget/avatar"}) || (*e.calls)[0].body != "" || (*e.calls)[1].body != "" {
			t.Errorf("%s calls = %v", p.id, got)
		}
		_, err = e.invoke(t, repositoryMirrorsSync, "execute", `{"repository":"other/place"}`)
		if !isInvalidRequest(err) || len(*e.calls) != 2 {
			t.Errorf("%s: mirror sync of another repository: %v", p.id, err)
		}
	}
}

func TestAvatarUploadReadsOnlyAReleasedImage(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("avatar-bytes")...)
	for _, p := range products {
		e, dir := newLifecycleEnvironment(t, p, func(c call) *http.Response { return jsonResponse(204, "") })
		image := filepath.Join(dir, "logo.png")
		text := filepath.Join(dir, "notes.txt")
		if err := os.WriteFile(image, png, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(text, []byte("not an image"), 0o600); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "logo.png")
		if err := os.WriteFile(outside, png, 0o600); err != nil {
			t.Fatal(err)
		}
		for name, path := range map[string]string{"outside": outside, "text": text, "missing": filepath.Join(dir, "none.png")} {
			_, err := e.invoke(t, repositoryAvatarsUpdate, "files", `{"local_path":`+quote(path)+`}`)
			if err == nil || len(*e.calls) != 0 || *e.reads != 0 {
				t.Errorf("%s %s: %v, calls %d, token reads %d", p.id, name, err, len(*e.calls), *e.reads)
			}
		}
		if _, err := e.invoke(t, repositoryAvatarsUpdate, "repo", `{"local_path":`+quote(image)+`}`); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: upload without released directories: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, repositoryAvatarsUpdate, "files", `{"local_path":`+quote(image)+`}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed upload: %v", p.id, err)
		}
		if _, err := e.invoke(t, repositoryAvatarsUpdate, "files", `{"local_path":`+quote(image)+`,"image":"AAAA"}`); !isInvalidRequest(err) {
			t.Errorf("%s: raw image argument: %v", p.id, err)
		}
		result, err := e.invoke(t, repositoryAvatarsUpdate, "files", `{"local_path":`+quote(image)+`}`)
		if err != nil || result["updated"] != true || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		body := bodyOf(t, (*e.calls)[0])
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST /api/v1/repos/acme/widget/avatar"}) ||
			body["image"] != base64.StdEncoding.EncodeToString(png) || len(body) != 1 {
			t.Errorf("%s calls = %v, body %v", p.id, got, body)
		}
	}
}

func TestRepositoryAvatarSizeLimit(t *testing.T) {
	previous := maxAvatarBytes
	t.Cleanup(func() { maxAvatarBytes = previous })
	maxAvatarBytes = 8
	e, dir := newLifecycleEnvironment(t, gitea, func(c call) *http.Response { return jsonResponse(204, "") })
	image := filepath.Join(dir, "big.png")
	if err := os.WriteFile(image, append([]byte("\x89PNG\r\n\x1a\n"), 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := e.invoke(t, repositoryAvatarsUpdate, "files", `{"local_path":`+quote(image)+`}`)
	refused(t, e, "oversized image", err)
}
