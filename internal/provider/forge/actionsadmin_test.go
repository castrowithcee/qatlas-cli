package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// secretCanary is the value of the released forward credential, with characters JSON and HTML escaping change.
const (
	secretCanary = "s3cr<et>&\"canary\nvalue-91f3"
	releasedRef  = "deploy-key"
	twoFieldRef  = "two-fields"
)

var adminTools = []*tool{actionsSecretsList, actionsSecretsSet, actionsSecretsDelete, actionsVariablesList,
	actionsVariablesGet, actionsVariablesCreate, actionsVariablesUpdate, actionsVariablesDelete}

type adminStore struct {
	entries map[string]string
	gets    int
}

func (s *adminStore) Get(_ context.Context, key string) (string, error) {
	s.gets++
	if value, ok := s.entries[key]; ok {
		return value, nil
	}
	return "", secret.ErrNoEntry
}
func (s *adminStore) Set(string, string) error { return nil }
func (s *adminStore) Delete(string) error      { return nil }

type adminEnv struct {
	*environment
	store *adminStore
	audit *bytes.Buffer
	// raw holds the compact result of every successful invoke.
	raw []string
}

// newAdminEnvironment is an instance of the product in a version whose connections list every administration
// tool and release a forward credential; the core writes its audit events to the environment. The invocation log
// holds sizes and codes only and is checked by the core's own tests; a provider may not import it.
func newAdminEnvironment(t *testing.T, p *product, v string, api func(call) *http.Response) *adminEnv {
	t.Helper()
	handler := giteaAt(v, api)
	if p == forgejo {
		handler = forgejoAt(v, api)
	}
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	store := &adminStore{entries: map[string]string{
		secret.StoreKey(releasedRef, "value"): secretCanary,
		secret.StoreKey(twoFieldRef, "a"):     "first-value-0001",
		secret.StoreKey(twoFieldRef, "b"):     "second-value-002",
	}}
	resolve := secret.NewWith(func(name string) string {
		reads++
		if name == tokenEnv {
			return tokenValue
		}
		return ""
	}, store, nil, red)
	cfg := testConfig(p)
	cfg.Credentials[releasedRef] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"value"}}
	cfg.Credentials[twoFieldRef] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"a", "b"}}
	var ids []string
	for _, tl := range slices.Concat(adminTools, runnerTools, instanceTools, adminUserTools, adminEmailTools,
		adminOrgTools, adminUnadoptedTools, adminCronTools, adminCredentialTools, adminBadgeTools, adminWebhookTools) {
		if _, ok := tl.since[p.id]; ok {
			ids = append(ids, p.toolID(tl))
		}
	}
	for name, targets := range map[string][]string{"admin-repo": {"repos/acme/widget"}, "admin-org": {"orgs/acme"},
		"admin-user": {"users/alice"}, "admin-open": nil} {
		cfg.Connections[name] = config.Connection{Service: "forge", Credential: "token", Targets: targets,
			Tools: ids, ForwardSecrets: []string{releasedRef, twoFieldRef}}
	}
	cfg.Connections["admin-exclude"] = config.Connection{Service: "forge", Credential: "token",
		Exclude: excluding["exclude"].excludes, Tools: ids, ForwardSecrets: []string{releasedRef, twoFieldRef}}
	core := application.New(registry(t), cfg, resolve, red)
	audit := &bytes.Buffer{}
	core.SetAudit(audit)
	return &adminEnv{environment: &environment{core: core, red: red, reads: &reads, calls: &calls, p: p},
		store: store, audit: audit}
}

func (e *adminEnv) call(t *testing.T, tl *tool, connection, arguments string, confirmed bool) (map[string]any, error) {
	t.Helper()
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: e.p.toolID(tl), Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: confirmed,
	})
	if err != nil {
		return nil, err
	}
	e.raw = append(e.raw, string(response.Result))
	var result map[string]any
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("result %s: %v", response.Result, err)
	}
	return result, nil
}

// secretForms are the spellings the canary takes as it is, quoted by Go, and inside JSON with and without HTML
// escaping.
func secretForms() []string {
	quoted := fmt.Sprintf("%q", secretCanary)
	forms := []string{secretCanary, quoted[1 : len(quoted)-1]}
	for _, escapeHTML := range []bool{true, false} {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(escapeHTML)
		_ = enc.Encode(secretCanary)
		text := strings.TrimSpace(buf.String())
		forms = append(forms, text[1:len(text)-1])
	}
	return forms
}

func jsonText(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func carriesSecret(text string) bool {
	for _, form := range secretForms() {
		if strings.Contains(text, form) {
			return true
		}
	}
	return false
}

// assertNoSecretOutside checks every channel but the request body: results, the error, its redacted form, the
// audit events, and every request path and query.
func (e *adminEnv) assertNoSecretOutside(t *testing.T, err error) {
	t.Helper()
	for _, raw := range e.raw {
		if carriesSecret(raw) {
			t.Errorf("a result carries the secret: %s", raw)
		}
	}
	if err != nil && (carriesSecret(err.Error()) || carriesSecret(e.red.Error(err))) {
		t.Errorf("the error carries the secret: %v", err)
	}
	if carriesSecret(e.audit.String()) {
		t.Errorf("the audit carries the secret: %s", e.audit.String())
	}
	for _, c := range *e.calls {
		if carriesSecret(c.path) || carriesSecret(c.escaped) || carriesSecret(c.query) || carriesSecret(c.auth) {
			t.Errorf("request %s %s carries the secret outside its body", c.method, c.path)
		}
	}
}

// secretBodies counts the requests whose body carries the canary and checks that each such body holds it once.
func (e *adminEnv) secretBodies(t *testing.T) int {
	t.Helper()
	n := 0
	for _, c := range *e.calls {
		count := 0
		for _, form := range secretForms() {
			count = max(count, strings.Count(c.body, form))
		}
		if count > 1 {
			t.Errorf("request %s %s carries the secret %d times", c.method, c.path, count)
		}
		n += count
	}
	return n
}

func TestActionsAdminToolsRegisterForBothWithTheirRisk(t *testing.T) {
	reg := registry(t)
	listed := map[*tool]bool{actionsSecretsSet: true, actionsSecretsDelete: true, actionsVariablesDelete: true}
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Fatalf("%s is not registered", p.toolID(tl))
			}
			if d.RequiresToolAllowList != listed[tl] {
				t.Errorf("%s listed-only = %t", d.ID, d.RequiresToolAllowList)
			}
			wantClass := p.id + variablesSensitivity
			if tl.group == "actions-secrets" {
				wantClass = p.id + secretsSensitivity
			}
			if d.Risk.DataSensitivity != wantClass {
				t.Errorf("%s sensitivity = %s", d.ID, d.Risk.DataSensitivity)
			}
			if d.Risk.Effect != capability.EffectRead && (d.Risk.Confirmation != capability.ConfirmationRequired ||
				d.Risk.Idempotency != capability.IdempotencyIdempotent || !d.Risk.OpenWorld) {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
		d, _, _ := reg.Lookup(p.toolID(actionsSecretsSet))
		secretRefs := 0
		for _, argument := range d.Arguments {
			if argument.SecretRef {
				secretRefs++
				if argument.Name != "secret_ref" || !argument.Required {
					t.Errorf("%s: secret argument %+v", d.ID, argument)
				}
			}
		}
		if secretRefs != 1 || strings.Contains(string(d.InputSchema), `"value"`) {
			t.Errorf("%s: the value must come from a secret reference only: %s", d.ID, d.InputSchema)
		}
		// description is Gitea's alone.
		for _, tl := range []*tool{actionsSecretsSet, actionsVariablesCreate, actionsVariablesUpdate} {
			d, _, _ := reg.Lookup(p.toolID(tl))
			if strings.Contains(string(d.InputSchema), `"description"`) != (p == gitea) {
				t.Errorf("%s: description in %s", d.ID, d.InputSchema)
			}
		}
	}
}

func TestActionsSecretsSetSendsTheValueOnlyInTheBody(t *testing.T) {
	for _, p := range products {
		for _, status := range []int{201, 204} {
			echo := func(c call) *http.Response {
				// A hostile instance echoes the body; nothing of the answer may be read.
				return jsonResponse(status, `{"echo":`+c.body+`,"message":"`+bodyCanary+`"}`)
			}
			e := newAdminEnvironment(t, p, defaultVersion(p), echo)
			arguments := `{"name":"DEPLOY_KEY","secret_ref":"` + releasedRef + `"}`
			if p == gitea {
				arguments = `{"name":"DEPLOY_KEY","secret_ref":"` + releasedRef + `","description":"Key"}`
			}
			result, err := e.call(t, actionsSecretsSet, "admin-repo", arguments, true)
			want := map[int]string{201: "created", 204: "replaced"}[status]
			if err != nil || result["outcome"] != want || result["name"] != "DEPLOY_KEY" ||
				result["repository"] != "acme/widget" || len(result) != 3 {
				t.Fatalf("%s %d: %v, %v", p.id, status, result, err)
			}
			if got := apiMethods(e.environment); !reflect.DeepEqual(got,
				[]string{"PUT " + actionsBase + "/secrets/DEPLOY_KEY"}) {
				t.Fatalf("%s calls = %v", p.id, got)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(apiCalls(e.environment)[0].body), &body); err != nil {
				t.Fatal(err)
			}
			wantBody := map[string]any{"data": secretCanary}
			if p == gitea {
				wantBody["description"] = "Key"
			}
			if !reflect.DeepEqual(body, wantBody) {
				t.Errorf("%s body = %v", p.id, body)
			}
			if n := e.secretBodies(t); n != 1 {
				t.Errorf("%s: the secret is in %d request bodies, want exactly one", p.id, n)
			}
			e.assertNoSecretOutside(t, nil)
			if e.red.Apply(secretCanary) == secretCanary {
				t.Errorf("%s: the value is not registered with the redactor", p.id)
			}
		}
	}
}

func TestActionsSecretsSetNeverReportsTheValueOfAFailure(t *testing.T) {
	for _, p := range products {
		for _, status := range []int{400, 404, 422, 500, 502, 503} {
			e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
				return jsonResponse(status, `{"message":"invalid data `+strings.ReplaceAll(c.body, `"`, `'`)+`",`+
					`"echo":`+c.body+`}`)
			})
			_, err := e.call(t, actionsSecretsSet, "admin-repo", `{"name":"DEPLOY_KEY","secret_ref":"`+releasedRef+`"}`, true)
			if err == nil {
				t.Fatalf("%s %d: no error", p.id, status)
			}
			if got := apiMethods(e.environment); len(got) != 1 {
				t.Errorf("%s %d: %d requests, want one without a retry: %v", p.id, status, len(got), got)
			}
			if status >= 500 != strings.Contains(err.Error(), "may have been applied") {
				t.Errorf("%s %d: %v", p.id, status, err)
			}
			if strings.Contains(err.Error(), "invalid data") {
				t.Errorf("%s %d: the provider text reached the error: %v", p.id, status, err)
			}
			if n := e.secretBodies(t); n != 1 {
				t.Errorf("%s %d: the secret is in %d request bodies", p.id, status, n)
			}
			e.assertNoSecretOutside(t, err)
		}
	}
}

func TestActionsSecretsSetRefusesBeforeTheSecretAndAnyRequest(t *testing.T) {
	for _, p := range products {
		cases := []struct {
			name, connection, arguments string
			confirmed                   bool
		}{
			{"unconfirmed", "admin-repo", `{"name":"KEY","secret_ref":"` + releasedRef + `"}`, false},
			{"owner without an owner target", "admin-repo", `{"owner":"orgs/acme","name":"KEY","secret_ref":"` +
				releasedRef + `"}`, true},
			{"owner of the other kind", "admin-org", `{"owner":"users/acme","name":"KEY","secret_ref":"` +
				releasedRef + `"}`, true},
			{"foreign repository", "admin-repo", `{"repository":"acme/other","name":"KEY","secret_ref":"` +
				releasedRef + `"}`, true},
			{"both levels", "admin-open", `{"repository":"acme/widget","owner":"orgs/acme","name":"KEY",` +
				`"secret_ref":"` + releasedRef + `"}`, true},
			{"two fields", "admin-repo", `{"name":"KEY","secret_ref":"` + twoFieldRef + `"}`, true},
			{"unreleased", "admin-repo", `{"name":"KEY","secret_ref":"token"}`, true},
			{"value as reference", "admin-repo", `{"name":"KEY","secret_ref":` + jsonText(secretCanary) + `}`, true},
			{"digit first", "admin-repo", `{"name":"1KEY","secret_ref":"` + releasedRef + `"}`, true},
			{"reserved prefix", "admin-repo", `{"name":"gitea_KEY","secret_ref":"` + releasedRef + `"}`, true},
			{"reserved GitHub prefix", "admin-repo", `{"name":"GITHUB_TOKEN","secret_ref":"` + releasedRef + `"}`, true},
			{"path characters", "admin-repo", `{"name":"A/../B","secret_ref":"` + releasedRef + `"}`, true},
		}
		if p == forgejo {
			cases = append(cases, struct {
				name, connection, arguments string
				confirmed                   bool
			}{"description on Forgejo", "admin-repo", `{"name":"KEY","secret_ref":"` + releasedRef +
				`","description":"x"}`, true})
		}
		for _, tc := range cases {
			e := newAdminEnvironment(t, p, defaultVersion(p), nil)
			_, err := e.call(t, actionsSecretsSet, tc.connection, tc.arguments, tc.confirmed)
			if err == nil || len(*e.calls) != 0 || e.store.gets != 0 || *e.reads != 0 {
				t.Errorf("%s %s: %v after %d calls, %d store reads, %d token reads", p.id, tc.name, err,
					len(*e.calls), e.store.gets, *e.reads)
			}
			if err != nil && (strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "users/acme")) {
				t.Errorf("%s %s: the refusal names the foreign target: %v", p.id, tc.name, err)
			}
			e.assertNoSecretOutside(t, err)
		}
		// Without a tools list entry the tool is not offered at all.
		e := newEnvironment(t, p, forgejoAt("17.0.0", nil))
		var unsupported *capability.UnsupportedError
		if _, err := e.invoke(t, actionsSecretsSet, "repo", `{"name":"KEY","secret_ref":"x"}`); !errors.As(err, &unsupported) {
			t.Errorf("%s: set without a tools list: %v", p.id, err)
		}
	}
}

// The user level acts on the account behind the token, proven by reading it; the owner level reaches the
// organization's path.
func TestActionsSecretsSetOnTheOwnerLevels(t *testing.T) {
	for _, p := range products {
		for _, login := range []string{"alice", "bob"} {
			e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
				if c.path == "/api/v1/user" {
					return jsonResponse(200, `{"id":3,"login":"`+login+`"}`)
				}
				return jsonResponse(201, ``)
			})
			result, err := e.call(t, actionsSecretsSet, "admin-user", `{"owner":"users/Alice","name":"KEY",`+
				`"secret_ref":"`+releasedRef+`"}`, true)
			if login == "bob" {
				if !isInvalidRequest(err) || !reflect.DeepEqual(apiMethods(e.environment), []string{"GET /api/v1/user"}) ||
					e.store.gets != 0 || strings.Contains(err.Error(), "bob") {
					t.Errorf("%s foreign login: %v, %v, %d store reads", p.id, err, apiMethods(e.environment), e.store.gets)
				}
				continue
			}
			if err != nil || result["owner"] != "users/Alice" || !reflect.DeepEqual(apiMethods(e.environment),
				[]string{"GET /api/v1/user", "PUT /api/v1/user/actions/secrets/KEY"}) {
				t.Errorf("%s user: %v, %v, %v", p.id, result, err, apiMethods(e.environment))
			}
			e.assertNoSecretOutside(t, err)
		}
		for _, connection := range []string{"admin-org", "admin-open"} {
			e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
			result, err := e.call(t, actionsSecretsSet, connection, `{"owner":"orgs/acme","name":"KEY","secret_ref":"`+
				releasedRef+`"}`, true)
			if err != nil || result["outcome"] != "replaced" || !reflect.DeepEqual(apiMethods(e.environment),
				[]string{"PUT /api/v1/orgs/acme/actions/secrets/KEY"}) {
				t.Errorf("%s %s: %v, %v, %v", p.id, connection, result, err, apiMethods(e.environment))
			}
		}
	}
}

func TestActionsSecretsListShowsNamesOnly(t *testing.T) {
	for _, p := range products {
		row := `{"name":"DEPLOY_KEY","created_at":"2030-01-01T00:00:00Z","description":"Key","data":"leaked-value"}`
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, `[`+row+`,null]`, "X-Total-Count", "31")
		})
		result, err := e.call(t, actionsSecretsList, "admin-repo", `{"limit":30}`, false)
		rows := rowsOf(t, result, "secrets")
		if err != nil || len(rows) != 1 || result["total_count"] != float64(31) || result["has_more"] != true ||
			result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := map[string]any{"name": "DEPLOY_KEY", "created_at": "2030-01-01T00:00:00Z"}
		if p == gitea {
			want["description"] = "Key"
		}
		if !reflect.DeepEqual(rows[0], want) {
			t.Errorf("%s row = %v", p.id, rows[0])
		}
		c := apiCalls(e.environment)[0]
		if c.path != actionsBase+"/secrets" || queryOf(t, c).Get("limit") != "30" || queryOf(t, c).Get("page") != "1" {
			t.Errorf("%s call = %+v", p.id, c)
		}
		next, err := e.call(t, actionsSecretsList, "admin-repo", `{"cursor":"`+result["next_cursor"].(string)+`"}`, false)
		if err != nil || queryOf(t, apiCalls(e.environment)[1]).Get("page") != "2" || next["has_more"] != false {
			t.Errorf("%s next: %v, %v", p.id, next, err)
		}
		org := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		if result, err := org.call(t, actionsSecretsList, "admin-org", `{"owner":"orgs/acme"}`, false); err != nil ||
			result["owner"] != "orgs/acme" || result["has_more"] != false ||
			!reflect.DeepEqual(apiMethods(org.environment), []string{"GET /api/v1/orgs/acme/actions/secrets"}) {
			t.Errorf("%s org: %v, %v, %v", p.id, result, err, apiMethods(org.environment))
		}
		if _, err := org.call(t, actionsSecretsList, "admin-repo", `{"owner":"orgs/acme"}`, false); !isInvalidRequest(err) ||
			len(apiCalls(org.environment)) != 1 {
			t.Errorf("%s: owner without an owner target: %v", p.id, err)
		}
	}
}

// Only Forgejo 17 lists the secrets of a user; older Forgejo and Gitea refuse before any request.
func TestActionsSecretsListOfAUserNeedsForgejo17(t *testing.T) {
	user := func(c call) *http.Response {
		if c.path == "/api/v1/user" {
			return jsonResponse(200, `{"id":3,"login":"alice"}`)
		}
		return jsonResponse(200, `[{"name":"KEY"}]`)
	}
	for _, v := range []string{"15.0.10", "16.0.6"} {
		e := newAdminEnvironment(t, forgejo, v, user)
		_, err := e.call(t, actionsSecretsList, "admin-user", `{"owner":"users/alice"}`, false)
		if err == nil || !strings.Contains(err.Error(), "not available") || !strings.Contains(err.Error(), "17") ||
			len(apiCalls(e.environment)) != 0 {
			t.Errorf("forgejo %s: %v, %v", v, err, apiMethods(e.environment))
		}
		// The repository and organization levels have no such floor.
		if _, err := e.call(t, actionsSecretsList, "admin-repo", `{}`, false); err != nil {
			t.Errorf("forgejo %s repository: %v", v, err)
		}
	}
	e := newAdminEnvironment(t, forgejo, "17.0.0", user)
	result, err := e.call(t, actionsSecretsList, "admin-user", `{"owner":"users/alice"}`, false)
	if err != nil || len(rowsOf(t, result, "secrets")) != 1 || !reflect.DeepEqual(apiMethods(e.environment),
		[]string{"GET /api/v1/user", "GET /api/v1/user/actions/secrets"}) {
		t.Errorf("forgejo 17: %v, %v, %v", result, err, apiMethods(e.environment))
	}
	for _, v := range []string{"1.27.3", "28.2.0"} {
		g := newAdminEnvironment(t, gitea, v, user)
		if _, err := g.call(t, actionsSecretsList, "admin-user", `{"owner":"users/alice"}`, false); !isInvalidRequest(err) ||
			len(*g.calls) != 0 {
			t.Errorf("gitea %s: %v after %d calls", v, err, len(*g.calls))
		}
	}
}

func TestActionsVariablesReadEveryMemberOfTheirLevel(t *testing.T) {
	for _, p := range products {
		own := `{"name":"REGISTRY","data":"registry.example.com","description":"Where","owner_id":1,"repo_id":0}`
		repoRow := `{"name":"OTHER","data":"x","owner_id":0,"repo_id":9}`
		e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			if strings.HasSuffix(c.path, "/variables") {
				return jsonResponse(200, `[`+own+`,`+repoRow+`]`)
			}
			if strings.HasSuffix(strings.ToUpper(c.path), "/REGISTRY") {
				return jsonResponse(200, own)
			}
			return jsonResponse(200, repoRow)
		})
		result, err := e.call(t, actionsVariablesList, "admin-org", `{"owner":"orgs/acme"}`, false)
		rows := rowsOf(t, result, "variables")
		want := map[string]any{"name": "REGISTRY", "data": "registry.example.com", "owner_id": float64(1),
			"repo_id": float64(0)}
		if p == gitea {
			want["description"] = "Where"
		}
		if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], want) {
			t.Fatalf("%s list: %v, %v", p.id, result, err)
		}
		got, err := e.call(t, actionsVariablesGet, "admin-org", `{"owner":"orgs/acme","name":"registry"}`, false)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s get: %v, %v", p.id, got, err)
		}
		// A variable of a repository is not one of the organization.
		if _, err := e.call(t, actionsVariablesGet, "admin-org", `{"owner":"orgs/acme","name":"OTHER"}`, false); err == nil ||
			!strings.Contains(err.Error(), "other than the requested") {
			t.Errorf("%s get foreign: %v", p.id, err)
		}
		// So is an answer for another name.
		if _, err := e.call(t, actionsVariablesGet, "admin-repo", `{"name":"THIRD"}`, false); err == nil {
			t.Errorf("%s get other name: no error", p.id)
		}
		wantCalls := []string{"GET /api/v1/orgs/acme/actions/variables",
			"GET /api/v1/orgs/acme/actions/variables/registry", "GET /api/v1/orgs/acme/actions/variables/OTHER",
			"GET " + actionsBase + "/variables/THIRD"}
		if got := apiMethods(e.environment); !reflect.DeepEqual(got, wantCalls) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		repo := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, `[`+repoRow+`]`)
		})
		if result, err := repo.call(t, actionsVariablesList, "admin-repo", `{}`, false); err != nil ||
			len(rowsOf(t, result, "variables")) != 1 {
			t.Errorf("%s repository list: %v, %v", p.id, result, err)
		}
	}
}

func TestActionsVariablesChangeWithOneRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			return jsonResponse(map[string]int{"POST": 201, "PUT": 204, "DELETE": 204}[c.method], ``)
		})
		description := ""
		if p == gitea {
			description = `,"description":"Where"`
		}
		created, err := e.call(t, actionsVariablesCreate, "admin-repo", `{"name":"REGISTRY","value":"a\nb"`+description+`}`, true)
		if err != nil || created["created"] != true || created["name"] != "REGISTRY" {
			t.Fatalf("%s create: %v, %v", p.id, created, err)
		}
		updated, err := e.call(t, actionsVariablesUpdate, "admin-repo", `{"name":"REGISTRY","new_name":"MIRROR",`+
			`"value":"c"}`, true)
		if err != nil || updated["name"] != "MIRROR" || updated["previous_name"] != "REGISTRY" || updated["updated"] != true {
			t.Fatalf("%s update: %v, %v", p.id, updated, err)
		}
		deleted, err := e.call(t, actionsVariablesDelete, "admin-repo", `{"name":"MIRROR"}`, true)
		if err != nil || deleted["deleted"] != true {
			t.Fatalf("%s delete: %v, %v", p.id, deleted, err)
		}
		calls := apiCalls(e.environment)
		if got := apiMethods(e.environment); !reflect.DeepEqual(got, []string{"POST " + actionsBase + "/variables/REGISTRY",
			"PUT " + actionsBase + "/variables/REGISTRY", "DELETE " + actionsBase + "/variables/MIRROR"}) {
			t.Fatalf("%s calls = %v", p.id, got)
		}
		wantCreate := `{"value":"a\nb"}`
		if p == gitea {
			wantCreate = `{"description":"Where","value":"a\nb"}`
		}
		if calls[0].body != wantCreate || calls[1].body != `{"name":"MIRROR","value":"c"}` || calls[2].body != "" {
			t.Errorf("%s bodies = %q %q %q", p.id, calls[0].body, calls[1].body, calls[2].body)
		}
		for _, arguments := range []string{`{"name":"REGISTRY"}`, `{"name":"GITEA_X","value":"v"}`,
			`{"name":"X","value":"bell\u0007"}`, `{"name":"X","value":"v","owner":"orgs/acme"}`} {
			before := len(*e.calls)
			if _, err := e.call(t, actionsVariablesCreate, "admin-repo", arguments, true); err == nil || len(*e.calls) != before {
				t.Errorf("%s create %s: %v", p.id, arguments, err)
			}
		}
		if _, err := e.call(t, actionsVariablesUpdate, "admin-repo", `{"name":"X","new_name":"GITHUB_X","value":"v"}`,
			true); !isInvalidRequest(err) {
			t.Errorf("%s update to a reserved name: %v", p.id, err)
		}
		if p == forgejo {
			if _, err := e.call(t, actionsVariablesCreate, "admin-repo", `{"name":"X","value":"v","description":"d"}`,
				true); err == nil {
				t.Error("forgejo create with description: no error")
			}
		}
		// A change after an unclear outcome is never repeated.
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		for _, tc := range []struct {
			tl        *tool
			arguments string
		}{{actionsVariablesCreate, `{"name":"X","value":"v"}`}, {actionsVariablesUpdate, `{"name":"X","value":"v"}`},
			{actionsVariablesDelete, `{"name":"X"}`}, {actionsSecretsDelete, `{"name":"X"}`}} {
			before := len(apiCalls(f.environment))
			_, err := f.call(t, tc.tl, "admin-repo", tc.arguments, true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") ||
				strings.Contains(err.Error(), bodyCanary) || len(apiCalls(f.environment)) != before+1 {
				t.Errorf("%s %s after 500: %v", p.id, tc.tl.action, err)
			}
		}
	}
}

func TestActionsAdminDeletesAreListedOnly(t *testing.T) {
	for _, p := range products {
		for _, tc := range []struct {
			tl   *tool
			path string
		}{{actionsSecretsDelete, "/api/v1/user/actions/secrets/KEY"}, {actionsVariablesDelete, "/api/v1/user/actions/variables/KEY"}} {
			e := newEnvironment(t, p, forgejoAt("17.0.0", nil))
			var unsupported *capability.UnsupportedError
			if _, err := e.invoke(t, tc.tl, "open", `{"name":"KEY"}`); !errors.As(err, &unsupported) || len(*e.calls) != 0 {
				t.Errorf("%s without a tools list: %v", p.toolID(tc.tl), err)
			}
			f := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
				if c.path == "/api/v1/user" {
					return jsonResponse(200, `{"login":"alice"}`)
				}
				return jsonResponse(204, ``)
			})
			var confirm *application.ConfirmationRequiredError
			if _, err := f.call(t, tc.tl, "admin-user", `{"owner":"users/alice","name":"KEY"}`, false); !errors.As(err, &confirm) ||
				len(*f.calls) != 0 {
				t.Errorf("%s unconfirmed: %v", p.toolID(tc.tl), err)
			}
			result, err := f.call(t, tc.tl, "admin-user", `{"owner":"users/alice","name":"KEY"}`, true)
			if err != nil || result["deleted"] != true || !reflect.DeepEqual(apiMethods(f.environment),
				[]string{"GET /api/v1/user", "DELETE " + tc.path}) {
				t.Errorf("%s: %v, %v, %v", p.toolID(tc.tl), result, err, apiMethods(f.environment))
			}
		}
	}
}

func TestOwnerSettingsStayReachableWhenAnExclusionTakesTheOwnerOut(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			switch {
			case c.path == "/api/v1/user":
				return jsonResponse(200, `{"id":3,"login":"Other"}`)
			case c.method == http.MethodGet && strings.HasSuffix(c.path, "/runners"):
				if p == forgejo {
					return jsonResponse(200, `[{"id":1,"name":"r"}]`)
				}
				return jsonResponse(200, `{"runners":[{"id":1,"name":"r"}]}`)
			case c.method == http.MethodGet && strings.HasSuffix(c.path, "/variables"):
				return jsonResponse(200, `[{"name":"V","data":"1"}]`)
			case c.method == http.MethodGet:
				return jsonResponse(200, `[{"name":"S"}]`)
			}
			return jsonResponse(204, ``)
		})
		for _, c := range []struct {
			tl        *tool
			arguments string
			confirmed bool
		}{
			{actionsSecretsList, `{"owner":"orgs/other"}`, false},
			{actionsVariablesList, `{"owner":"orgs/Other"}`, false},
			{actionsVariablesCreate, `{"owner":"orgs/other","name":"V","value":"1"}`, true},
			{actionsRunnersList, `{"owner":"orgs/other"}`, false},
		} {
			if _, err := e.call(t, c.tl, "admin-exclude", c.arguments, c.confirmed); err != nil {
				t.Errorf("%s %s %s: %v", p.id, c.tl.group, c.tl.action, err)
			}
		}
		// The user level is proven by the account behind the token, whose own settings stay reachable.
		if _, err := e.call(t, actionsVariablesList, "admin-exclude", `{"owner":"users/other"}`, false); p == gitea && err != nil {
			t.Errorf("%s: user level of an excluded account: %v", p.id, err)
		}
		// A repository inside an exclusion stays refused before any request.
		before := len(*e.calls)
		for _, tl := range []*tool{actionsSecretsList, actionsVariablesList, actionsRunnersList} {
			_, err := e.call(t, tl, "admin-exclude", `{"repository":"acme/gadget"}`, false)
			if err == nil || !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s of an excluded repository: %v", p.id, tl.group, err)
			}
		}
	}
}

func TestRunnerJobsOfAnOwnerLevelDropExcludedRepositories(t *testing.T) {
	e := newAdminEnvironment(t, forgejo, defaultVersion(forgejo), func(call) *http.Response {
		return jsonResponse(200, `[{"id":1,"html_url":"https://forge.example.test/acme/widget/actions/runs/1/jobs/1"},`+
			`{"id":2,"html_url":"https://forge.example.test/acme/gadget/actions/runs/1/jobs/2"},`+
			`{"id":3,"html_url":"https://forge.example.test/other/x/actions/runs/1/jobs/3"},{"id":4,"repo_id":5}]`)
	})
	kept, err := e.call(t, actionsRunnerJobsSearch, "admin-exclude", `{"owner":"orgs/acme"}`, false)
	if rows := rowsOf(t, kept, "jobs"); err != nil || len(rows) != 1 || rows[0]["id"] != float64(1) {
		t.Errorf("jobs of a reachable organization: %v, %v", kept, err)
	}
	none, err := e.call(t, actionsRunnerJobsSearch, "admin-exclude", `{"owner":"orgs/other"}`, false)
	if err != nil || len(rowsOf(t, none, "jobs")) != 0 {
		t.Errorf("jobs of an excluded organization: %v, %v", none, err)
	}
	all, err := e.call(t, actionsRunnerJobsSearch, "admin-org", `{"owner":"orgs/acme"}`, false)
	if err != nil || len(rowsOf(t, all, "jobs")) != 4 {
		t.Errorf("jobs without exclusions: %v, %v", all, err)
	}
}
