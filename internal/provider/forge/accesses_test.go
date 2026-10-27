package forge

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const secretFixture = "fixture-secret-value"

var accessReaders = []*tool{webhooksList, webhooksGet, gitHooksList, gitHooksGet, pushMirrorsList, pushMirrorsGet,
	deployKeysList, deployKeysGet, userKeysList, userKeysGet, userGPGKeysList, userGPGKeysGet, accessTokensList,
	accessTokensGetCurrent, oauth2AppsList, oauth2AppsGet}

var accessListedOnly = []*tool{webhooksDelete, gitHooksDelete, pushMirrorsDelete, deployKeysDelete, userKeysDelete,
	userGPGKeysDelete, accessTokensDeleteCurrent, oauth2AppsDelete}

// accessTools are the tools of the existing accesses.
var accessTools = append(append(slices.Clone(accessReaders), accessListedOnly...), pushMirrorsSync)

func accessToolIDs(p *product) []string {
	var ids []string
	for _, t := range accessTools {
		if _, ok := t.since[p.id]; ok {
			ids = append(ids, p.toolID(t))
		}
	}
	return ids
}

// newAccessEnvironment adds the connections these tests need. access-<scope> names every access tool in its
// tools list, so the listed-only ones are offered; scope is the target set: open, repo, org, or owner.
func newAccessEnvironment(t *testing.T, p *product, handler func(call) *http.Response) *environment {
	t.Helper()
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	cfg := testConfig(p)
	effects := append(slices.Clone(allEffects), config.EffectExecute)
	for name, targets := range map[string][]string{"open": nil, "repo": {"repos/acme/widget"}, "org": {"orgs/acme"},
		"owner": {"users/Alice"}, "mixed": {"users/alice", "repos/acme/widget"}} {
		cfg.Connections["access-"+name] = config.Connection{Service: "forge", Credential: "token", Effects: effects,
			Targets: targets, Tools: accessToolIDs(p)}
	}
	for name, connection := range cfg.Connections {
		if !strings.HasPrefix(name, "access-") {
			connection.Effects = effects
			cfg.Connections[name] = connection
		}
	}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}
}

func hookFixture(id int, extra string) string {
	return `{"id":` + strconv.Itoa(id) + `,"type":"gitea","name":"deploy","active":true,"events":["push","release"],` +
		`"branch_filter":"main","authorization_header":"` + secretFixture + `","secret":"` + secretFixture + `",` +
		`"config":{"url":"https://hooks.example.invalid/in","content_type":"json","secret":"` + secretFixture + `",` +
		`"authorization_header":"` + secretFixture + `","token":"` + secretFixture + `","channel":"#ops"},` +
		`"content_type":"json","url":"https://forge.example.test/acme/widget/settings/hooks/` + strconv.Itoa(id) + `",` +
		`"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:06Z","metadata":{"token":"` + secretFixture + `"}` +
		extra + `}`
}

func noSecret(t *testing.T, label string, value any) {
	t.Helper()
	if strings.Contains(strings.ToLower(toJSON(t, value)), secretFixture) {
		t.Errorf("%s shows a secret: %v", label, value)
	}
}

func toJSON(t *testing.T, value any) string {
	t.Helper()
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestAccessToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range accessTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if _, offered := tl.since[p.id]; !offered {
				if ok {
					t.Errorf("%s is registered for a product that lacks it", d.ID)
				}
				continue
			}
			if !ok {
				t.Fatalf("%s is missing", p.toolID(tl))
			}
			listed := slices.Contains(accessListedOnly, tl)
			if d.RequiresToolAllowList != listed {
				t.Errorf("%s listed-only = %t", d.ID, d.RequiresToolAllowList)
			}
			wantEffect := map[string]string{"list": "read", "get": "read", "get-current": "read", "delete": "delete",
				"delete-current": "delete", "sync": "execute"}[tl.action]
			if string(d.Risk.Effect) != wantEffect || (wantEffect != "read") != (d.Risk.Confirmation == "required") {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) && listed {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
	}
}

func TestWebhooksReadOneLevelWithoutSecrets(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, accountServer("alice", func(c call) *http.Response {
			if c.path == "/api/v1/repos/acme/widget/hooks" || c.path == "/api/v1/orgs/acme/hooks" ||
				c.path == "/api/v1/user/hooks" {
				return jsonResponse(200, `[`+hookFixture(12, "")+`,`+strings.Replace(hookFixture(13, ""),
					"https://hooks.example.invalid/in", "https://hooks.example.invalid/in?key=x", 1)+`]`,
					"X-Total-Count", "2")
			}
			return jsonResponse(200, hookFixture(12, ""))
		}))
		for name, args := range map[string]string{
			"repository": `{"repository":"acme/widget"}`, "organization": `{"organization":"acme"}`,
			"account": `{"account":true}`} {
			*e.calls = nil
			result, err := e.invoke(t, webhooksList, "access-open", args)
			if err != nil || result["level"] != name {
				t.Fatalf("%s %s: %v, %v", p.id, name, result, err)
			}
			noSecret(t, p.id+" "+name, result)
			rows := rowsOf(t, result, "webhooks")
			if len(rows) != 2 || rows[0]["id"] != float64(12) || rows[0]["active"] != true {
				t.Fatalf("%s %s rows = %v", p.id, name, rows)
			}
			config, _ := rows[0]["config"].(map[string]any)
			if config["url"] != "https://hooks.example.invalid/in" || config["content_type"] != "json" ||
				config["channel"] != "#ops" || len(config) != 3 {
				t.Errorf("%s %s config = %v", p.id, name, config)
			}
			if second, _ := rows[1]["config"].(map[string]any); second["url"] != nil {
				t.Errorf("%s: an address with a query is shown: %v", p.id, second)
			}
			for _, banned := range []string{"authorization_header", "secret", "metadata", "token"} {
				if _, ok := rows[0][banned]; ok {
					t.Errorf("%s: member %s is shown", p.id, banned)
				}
			}
			if _, ok := rows[0]["name"]; ok != (p == gitea) {
				t.Errorf("%s: name present = %t", p.id, ok)
			}
			for _, member := range []string{"content_type", "url"} {
				if _, ok := rows[0][member]; ok != (p == forgejo) {
					t.Errorf("%s: member %s present = %t", p.id, member, ok)
				}
			}
			if result["has_more"] != false {
				t.Errorf("%s: has_more = %v", p.id, result["has_more"])
			}
		}
		*e.calls = nil
		for _, args := range []string{`{"repository":"acme/widget","hook_id":12}`, `{"organization":"acme","hook_id":12}`,
			`{"account":true,"hook_id":12}`} {
			result, err := e.invoke(t, webhooksGet, "access-open", args)
			if err != nil {
				t.Fatalf("%s: %v", p.id, err)
			}
			noSecret(t, p.id+" get", result)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/hooks/12",
			"GET /api/v1/orgs/acme/hooks/12", "GET /api/v1/user/hooks/12"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		if _, err := e.invoke(t, webhooksGet, "access-open", `{"repository":"acme/widget","hook_id":99}`); !isInvalidRequest(err) &&
			classOfError(err) == "" {
			t.Errorf("%s: another webhook than asked for: %v", p.id, err)
		}
	}
}

func TestWebhookLevelsAreBoundBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, accountServer("alice", func(c call) *http.Response {
			return jsonResponse(200, `[]`)
		}))
		cases := []struct{ label, connection, args string }{
			{"repository outside the targets", "access-repo", `{"repository":"acme/other"}`},
			{"organization without an orgs target", "access-repo", `{"organization":"acme"}`},
			{"organization named as a user", "access-owner", `{"organization":"alice"}`},
			{"other organization", "access-org", `{"organization":"other"}`},
			{"repository of an organization target only", "access-org", `{"repository":"other/widget"}`},
			{"account with repository targets", "access-repo", `{"account":true}`},
			{"account with mixed targets", "access-mixed", `{"account":true}`},
			{"account with organization targets", "access-org", `{"account":true}`},
			{"two levels", "access-open", `{"repository":"acme/widget","organization":"acme"}`},
			{"account false", "access-open", `{"account":false}`},
			{"no level and no single repository", "access-open", `{}`},
			{"bad hook id", "access-open", `{"repository":"acme/widget","hook_id":0}`},
		}
		for _, c := range cases {
			tl := webhooksList
			if strings.Contains(c.label, "hook id") {
				tl = webhooksGet
			}
			_, err := e.invoke(t, tl, c.connection, c.args)
			refused(t, e, c.label, err)
			if tl == webhooksList {
				_, err = e.invoke(t, webhooksDelete, c.connection, strings.TrimSuffix(c.args, "}")+hookIDTail(c.args))
				refused(t, e, c.label+" delete", err)
			}
		}
		// The only repository the targets name is the default level.
		if _, err := e.invoke(t, webhooksList, "access-repo", `{}`); err != nil ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/repos/acme/widget/hooks"}) {
			t.Errorf("%s: default repository: %v, %v", p.id, err, methods(e))
		}
	}
}

func hookIDTail(args string) string {
	if args == "{}" {
		return `"hook_id":1}`
	}
	return `,"hook_id":1}`
}

func TestWebhookAccountLevelProvesTheAccountFirst(t *testing.T) {
	for _, p := range products {
		login := "alice"
		e := newAccessEnvironment(t, p, func(c call) *http.Response {
			if c.path == "/api/v1/user" {
				return jsonResponse(200, `{"id":1,"login":"`+login+`"}`)
			}
			if c.method == http.MethodDelete {
				return jsonResponse(204, "")
			}
			return jsonResponse(200, `[]`)
		})
		if _, err := e.invoke(t, webhooksList, "access-owner", `{"account":true}`); err != nil ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/user", "GET /api/v1/user/hooks"}) {
			t.Fatalf("%s: %v, %v", p.id, err, methods(e))
		}
		*e.calls = nil
		if _, err := e.invoke(t, webhooksDelete, "access-owner", `{"account":true,"hook_id":4}`); err != nil ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/user", "DELETE /api/v1/user/hooks/4"}) {
			t.Fatalf("%s: %v, %v", p.id, err, methods(e))
		}
		// Without targets, no proof is needed.
		*e.calls = nil
		if _, err := e.invoke(t, webhooksList, "access-open", `{"account":true}`); err != nil ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/user/hooks"}) {
			t.Fatalf("%s: %v, %v", p.id, err, methods(e))
		}
		// The account behind the token is another one than the target names: no webhook is read or deleted.
		login = "mallory"
		*e.calls = nil
		for _, tl := range []*tool{webhooksList, webhooksDelete} {
			if _, err := e.invoke(t, tl, "access-owner", `{"account":true,"hook_id":4}`); err == nil && tl == webhooksDelete {
				t.Errorf("%s: foreign account accepted", p.id)
			}
		}
		for _, m := range methods(e) {
			if m != "GET /api/v1/user" {
				t.Errorf("%s: request %s after a foreign account", p.id, m)
			}
		}
	}
}

func TestAccessDeletesAreListedOnlyConfirmedAndSingle(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, accountServer("alice", func(c call) *http.Response {
			return jsonResponse(503, `{"message":"`+bodyCanary+`"}`)
		}))
		cases := []struct {
			tl   *tool
			args string
			want string
		}{
			{webhooksDelete, `{"repository":"acme/widget","hook_id":5}`, "DELETE /api/v1/repos/acme/widget/hooks/5"},
			{webhooksDelete, `{"organization":"acme","hook_id":5}`, "DELETE /api/v1/orgs/acme/hooks/5"},
			{webhooksDelete, `{"account":true,"hook_id":5}`, "DELETE /api/v1/user/hooks/5"},
			{gitHooksDelete, `{"repository":"acme/widget","name":"pre-receive"}`,
				"DELETE /api/v1/repos/acme/widget/hooks/git/pre-receive"},
			{pushMirrorsDelete, `{"repository":"acme/widget","name":"remote_mirror_1a2b"}`,
				"DELETE /api/v1/repos/acme/widget/push_mirrors/remote_mirror_1a2b"},
			{pushMirrorsSync, `{"repository":"acme/widget"}`, "POST /api/v1/repos/acme/widget/push_mirrors-sync"},
		}
		for _, c := range cases {
			if c.tl != pushMirrorsSync {
				if _, err := e.invoke(t, c.tl, "repo", c.args); err == nil || len(*e.calls) != 0 {
					t.Errorf("%s %s without a tools list: %v", p.id, c.tl.action, err)
				}
			}
			if _, err := e.invokeAs(t, c.tl, "access-open", c.args, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s unconfirmed: %v", p.id, c.tl.action, err)
			}
			*e.calls = nil
			_, err := e.invoke(t, c.tl, "access-open", c.args)
			if err == nil {
				t.Fatalf("%s %s: a 5xx was accepted", p.id, c.tl.action)
			}
			// One change request, never repeated after the unclear result.
			if got := methods(e); !reflect.DeepEqual(got, []string{c.want}) {
				t.Errorf("%s %s calls = %v", p.id, c.tl.action, got)
			}
			if (*e.calls)[0].body != "" {
				t.Errorf("%s %s sends a body %q", p.id, c.tl.action, (*e.calls)[0].body)
			}
			*e.calls = nil
		}
	}
}

func TestAccessDeletesAndSyncSucceed(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, func(c call) *http.Response {
			if c.method == http.MethodDelete {
				return jsonResponse(204, "")
			}
			return jsonResponse(200, "")
		})
		for _, c := range []struct {
			tl   *tool
			args string
			key  string
		}{
			{webhooksDelete, `{"organization":"acme","hook_id":5}`, "deleted"},
			{gitHooksDelete, `{"repository":"acme/widget","name":"update"}`, "deleted"},
			{pushMirrorsDelete, `{"repository":"acme/widget","name":"remote_mirror_1a2b"}`, "deleted"},
			{pushMirrorsSync, `{"repository":"acme/widget"}`, "requested"},
		} {
			result, err := e.invoke(t, c.tl, "access-open", c.args)
			if err != nil || result[c.key] != true {
				t.Errorf("%s %s: %v, %v", p.id, c.tl.action, result, err)
			}
		}
		if len(*e.calls) != 4 {
			t.Errorf("%s calls = %v", p.id, methods(e))
		}
		// Sync is an execution: a connection without the effect does not offer it.
		cfgEnv := newLifecycleEnvironmentless(t, p)
		if _, err := cfgEnv.invoke(t, pushMirrorsSync, "repo", `{"repository":"acme/widget"}`); err == nil ||
			len(*cfgEnv.calls) != 0 {
			t.Errorf("%s: sync without the execute effect: %v", p.id, err)
		}
	}
}

// newLifecycleEnvironmentless is the plain test environment, whose connections lack the execute effect.
func newLifecycleEnvironmentless(t *testing.T, p *product) *environment {
	return newEnvironment(t, p, func(call) *http.Response { return jsonResponse(200, "") })
}

func TestGitHooksCapTheScriptAndRejectBadNames(t *testing.T) {
	long := strings.Repeat("echo x\n", 3000)
	for _, p := range products {
		e := newAccessEnvironment(t, p, func(c call) *http.Response {
			switch c.path {
			case "/api/v1/repos/acme/widget/hooks/git":
				return jsonResponse(200, `[{"name":"pre-receive","is_active":true,"content":"`+strings.ReplaceAll(long, "\n", `\n`)+
					`"},{"name":"update","is_active":false,"content":""}]`)
			case "/api/v1/repos/acme/widget/hooks/git/update":
				return jsonResponse(200, `{"name":"update","is_active":false,"content":"exit 0\n"}`)
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, gitHooksList, "access-repo", `{}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		rows := rowsOf(t, result, "git_hooks")
		if len(rows) != 2 || len(rows[0]["content"].(string)) != maxHookScript || rows[0]["content_truncated"] != true ||
			rows[1]["content_truncated"] != false {
			t.Errorf("%s: rows = %v", p.id, rows)
		}
		result, err = e.invoke(t, gitHooksGet, "access-repo", `{"name":"update"}`)
		if hook, _ := result["git_hook"].(map[string]any); err != nil || hook["content"] != "exit 0\n" {
			t.Errorf("%s: %v, %v", p.id, result, err)
		}
		*e.calls, *e.reads = nil, 0
		for _, name := range []string{"../x", "Pre-Receive", "", "a/b", "pre_receive", "x%2fy"} {
			_, err := e.invoke(t, gitHooksGet, "access-repo", `{"name":"`+name+`"}`)
			refused(t, e, "git hook name "+name, err)
		}
		_, err = e.invoke(t, gitHooksList, "access-repo", `{"repository":"other/widget"}`)
		refused(t, e, "git hooks of another repository", err)
		if _, err := e.invoke(t, gitHooksGet, "access-repo", `{"name":"pre-receive"}`); err == nil {
			t.Errorf("%s: a git hook answer for another name was accepted", p.id)
		}
	}
}

// userInfoAddress carries user info, which the tools must never show; it is assembled so that the literal
// does not read like a mail address.
var userInfoAddress = "https://user:pw" + "@" + "mirror.example.invalid/x.git"

func TestPushMirrorsShowTheTargetNeverCredentials(t *testing.T) {
	mirror := func(name, address string) string {
		return `{"remote_name":"` + name + `","remote_address":"` + address + `","repo_name":"widget","interval":"8h0m0s",` +
			`"sync_on_commit":true,"branch_filter":"main","created":"2026-01-02T03:04:05Z","last_update":"2026-01-03T03:04:05Z",` +
			`"last_error":"` + strings.Repeat("e", 5000) + `","public_key":"ssh-ed25519 ` + secretFixture + `",` +
			`"remote_username":"` + secretFixture + `","remote_password":"` + secretFixture + `","use_ssh":true}`
	}
	for _, p := range products {
		e := newAccessEnvironment(t, p, func(c call) *http.Response {
			switch c.path {
			case "/api/v1/repos/acme/widget/push_mirrors":
				return jsonResponse(200, `[`+mirror("remote_mirror_1a2b", "https://mirror.example.invalid/acme/widget.git")+`,`+
					mirror("remote_mirror_3c4d", userInfoAddress)+`]`)
			case "/api/v1/repos/acme/widget/push_mirrors/remote_mirror_1a2b":
				return jsonResponse(200, mirror("remote_mirror_1a2b", "https://mirror.example.invalid/acme/widget.git"))
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, pushMirrorsList, "access-repo", `{"limit":10}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		noSecret(t, p.id+" list", result)
		rows := rowsOf(t, result, "push_mirrors")
		if len(rows) != 2 || rows[0]["remote_address"] != "https://mirror.example.invalid/acme/widget.git" ||
			rows[0]["sync_on_commit"] != true || len(rows[0]["last_error"].(string)) > maxMirrorError {
			t.Fatalf("%s rows = %v", p.id, rows)
		}
		if _, ok := rows[1]["remote_address"]; ok {
			t.Errorf("%s: an address with user info is shown", p.id)
		}
		for _, banned := range []string{"public_key", "remote_username", "remote_password", "use_ssh"} {
			if _, ok := rows[0][banned]; ok {
				t.Errorf("%s: member %s is shown", p.id, banned)
			}
		}
		if _, ok := rows[0]["branch_filter"]; ok != (p == forgejo) {
			t.Errorf("%s: branch_filter present = %t", p.id, ok)
		}
		if q := queryOf(t, (*e.calls)[0]); q.Get("limit") != "10" || q.Get("page") != "1" {
			t.Errorf("%s query = %v", p.id, q)
		}
		got, err := e.invoke(t, pushMirrorsGet, "access-repo", `{"name":"remote_mirror_1a2b"}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		noSecret(t, p.id+" get", got)
		*e.calls = nil
		if _, err := e.invoke(t, pushMirrorsGet, "access-repo", `{"name":"remote_mirror_zzzz"}`); err == nil {
			t.Errorf("%s: unknown mirror accepted", p.id)
		}
		for _, name := range []string{"..", ".", "a/b", "a b", ""} {
			e2 := newAccessEnvironment(t, p, func(call) *http.Response { return notFoundResponse() })
			_, err := e2.invoke(t, pushMirrorsGet, "access-repo", `{"name":"`+name+`"}`)
			refused(t, e2, "mirror name "+name, err)
		}
	}
}

// accessWiring lists, per source file, the methods and path literals the tools of the existing accesses wire.
// GET is the default of a read; the listed methods are the changes. A later group adds its entries here.
var accessWiring = map[string]struct{ methods, paths []string }{
	"webhooks.go":         {[]string{"MethodDelete"}, []string{"/hooks", "/user/hooks"}},
	"githooks.go":         {[]string{"MethodDelete"}, []string{"/hooks/git", "/hooks/git/"}},
	"pushmirrors.go":      {[]string{"MethodDelete", "MethodPost"}, []string{"/push_mirrors", "/push_mirrors/", "/push_mirrors-sync"}},
	"keys.go":             {[]string{"MethodDelete"}, []string{"/user/keys", "/user/gpg_keys", "/keys", "/gpg_keys", "/keys/"}},
	"tokens.go":           {[]string{"MethodDelete"}, []string{"/tokens", "/token"}},
	"oauth2apps.go":       {nil, []string{"/user/applications/oauth2"}},
	"admincredentials.go": {[]string{"MethodDelete"}, []string{"/keys/", "/tokens", "/tokens/"}},
	"adminwebhooks.go":    {[]string{"MethodDelete"}, []string{"/admin/hooks"}},
}

// accessRouteWords mark a path literal as an access route that accessWiring must list.
var accessRouteWords = []string{"hooks", "push_mirrors", "keys", "token", "applications", "oauth2"}

// forbiddenPaths are access routes that create or change an access or reach beyond the account's own scope.
var forbiddenPaths = []string{"/tests", "/admin/hooks", "/admin/", "/keys/tokens", "/gpg_key_token", "/gpg_key_verify"}

// Every literal in the package that names an access route is one of the wired ones, and no file wires another
// method than the listed ones. The scan reads the source, so a new route cannot hide behind a test fixture.
func TestOnlyTheListedAccessRoutesAndMethodsAreWired(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		wired, isAccess := accessWiring[name]
		var usedMethods []string
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := node.X.(*ast.Ident); ok && id.Name == "http" && strings.HasPrefix(node.Sel.Name, "Method") &&
					node.Sel.Name != "MethodGet" {
					usedMethods = append(usedMethods, node.Sel.Name)
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING {
					return true
				}
				value, _ := strconv.Unquote(node.Value)
				for _, bad := range forbiddenPaths {
					// The administration tools are the only files that wire administrator routes.
					if bad == "/admin/" && slices.Contains([]string{"instanceadmin.go", "quotaadmin.go", "adminusers.go",
						"adminemails.go", "adminorgs.go", "adminunadopted.go", "admincron.go", "admincredentials.go", "adminbadges.go", "adminwebhooks.go"}, name) {
						continue
					}
					if bad == "/admin/hooks" && name == "adminwebhooks.go" {
						continue
					}
					if strings.Contains(value, bad) && strings.Contains(value, "/") {
						t.Errorf("%s names the forbidden route %q", name, value)
					}
				}
				if strings.HasPrefix(value, "/") && slices.ContainsFunc(accessRouteWords, func(w string) bool {
					return strings.Contains(value, w)
				}) {
					if !isAccess || !slices.Contains(wired.paths, value) {
						t.Errorf("%s names the route %q, which is not wired", name, value)
					} else if slices.Contains(wired.paths, value) {
						seen[name+" "+value] = true
					}
				}
			}
			return true
		})
		if isAccess {
			sort.Strings(usedMethods)
			for _, m := range slices.Compact(usedMethods) {
				if !slices.Contains(wired.methods, m) {
					t.Errorf("%s wires the method %s", name, m)
				}
			}
		}
	}
	for name, wired := range accessWiring {
		for _, path := range wired.paths {
			if !seen[name+" "+path] {
				t.Errorf("%s does not wire %q any more; update accessWiring", name, path)
			}
		}
	}
}

// The requests of every access tool, run through the transport, are within the wired set and carry the exact
// verbs.
func TestAccessToolsSendOnlyTheListedRequests(t *testing.T) {
	allowed := []string{
		"GET /api/v1/repos/acme/widget/hooks", "GET /api/v1/repos/acme/widget/hooks/1",
		"DELETE /api/v1/repos/acme/widget/hooks/1", "GET /api/v1/orgs/acme/hooks", "GET /api/v1/orgs/acme/hooks/1",
		"DELETE /api/v1/orgs/acme/hooks/1", "GET /api/v1/user/hooks", "GET /api/v1/user/hooks/1",
		"DELETE /api/v1/user/hooks/1", "GET /api/v1/repos/acme/widget/hooks/git",
		"GET /api/v1/repos/acme/widget/hooks/git/update", "DELETE /api/v1/repos/acme/widget/hooks/git/update",
		"GET /api/v1/repos/acme/widget/push_mirrors", "GET /api/v1/repos/acme/widget/push_mirrors/m1",
		"POST /api/v1/repos/acme/widget/push_mirrors-sync", "DELETE /api/v1/repos/acme/widget/push_mirrors/m1",
	}
	for _, p := range products {
		e := newAccessEnvironment(t, p, func(c call) *http.Response {
			switch {
			case c.method == http.MethodDelete:
				return jsonResponse(204, "")
			case strings.HasSuffix(c.path, "/hooks") || strings.HasSuffix(c.path, "/push_mirrors") ||
				strings.HasSuffix(c.path, "/hooks/git"):
				return jsonResponse(200, `[]`)
			case strings.Contains(c.path, "/hooks/git/"):
				return jsonResponse(200, `{"name":"update"}`)
			case strings.Contains(c.path, "push_mirrors/"):
				return jsonResponse(200, `{"remote_name":"m1"}`)
			case strings.Contains(c.path, "/hooks/"):
				return jsonResponse(200, `{"id":1}`)
			}
			return jsonResponse(200, "")
		})
		for _, run := range []struct {
			tl   *tool
			args string
		}{
			{webhooksList, `{"repository":"acme/widget"}`}, {webhooksGet, `{"repository":"acme/widget","hook_id":1}`},
			{webhooksDelete, `{"repository":"acme/widget","hook_id":1}`}, {webhooksList, `{"organization":"acme"}`},
			{webhooksGet, `{"organization":"acme","hook_id":1}`}, {webhooksDelete, `{"organization":"acme","hook_id":1}`},
			{webhooksList, `{"account":true}`}, {webhooksGet, `{"account":true,"hook_id":1}`},
			{webhooksDelete, `{"account":true,"hook_id":1}`},
			{gitHooksList, `{"repository":"acme/widget"}`}, {gitHooksGet, `{"repository":"acme/widget","name":"update"}`},
			{gitHooksDelete, `{"repository":"acme/widget","name":"update"}`},
			{pushMirrorsList, `{"repository":"acme/widget"}`}, {pushMirrorsGet, `{"repository":"acme/widget","name":"m1"}`},
			{pushMirrorsSync, `{"repository":"acme/widget"}`}, {pushMirrorsDelete, `{"repository":"acme/widget","name":"m1"}`},
		} {
			if _, err := e.invoke(t, run.tl, "access-open", run.args); err != nil {
				t.Errorf("%s %s %s: %v", p.id, run.tl.group, run.tl.action, err)
			}
		}
		for _, got := range methods(e) {
			if !slices.Contains(allowed, got) {
				t.Errorf("%s sent %s", p.id, got)
			}
		}
		for _, c := range *e.calls {
			if strings.Contains(c.path, "/admin/") || strings.HasSuffix(c.path, "/tests") || c.method == http.MethodPatch {
				t.Errorf("%s sent %s %s", p.id, c.method, c.path)
			}
		}
	}
}
