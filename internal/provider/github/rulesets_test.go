package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeRulesets answers the repository and organization ruleset routes, through the failure hook of
// fakeGitHub, and keeps the rulesets it created or updated by target and identifier.
type fakeRulesets struct {
	mu      sync.Mutex
	f       *fakeGitHub
	nextID  int64
	byOwner map[string]map[int64]map[string]any // "repos/octo-org/example" or "orgs/octo-org" -> id -> ruleset
}

func serveRulesets(t *testing.T) (*fakeGitHub, *fakeRulesets, string) {
	t.Helper()
	r := &fakeRulesets{nextID: 1420, byOwner: map[string]map[int64]map[string]any{
		"repos/octo-org/example": {1420: {"id": 1420, "name": "protect-main", "target": "branch",
			"enforcement": "active", "conditions": map[string]any{"ref_name": map[string]any{
				"include": []string{"refs/heads/main"}, "exclude": []string{}}},
			"rules": []any{map[string]any{"type": "deletion"}}, "bypass_actors": []any{}}},
		"orgs/octo-org": {77: {"id": 77, "name": "org-wide", "target": "branch", "enforcement": "evaluate",
			"conditions": map[string]any{}, "rules": []any{}, "bypass_actors": []any{}}},
	}}
	f := &fakeGitHub{}
	r.f = f
	f.failure = r.route
	return f, r, serve(t, f)
}

// scope reports the store key a repository or an organization REST path below /api/v3/ uses.
func (r *fakeRulesets) scope(prefix string) (string, bool) {
	if tail, ok := strings.CutPrefix(prefix, "/api/v3/repos/"); ok {
		return "repos/" + tail, true
	}
	if tail, ok := strings.CutPrefix(prefix, "/api/v3/orgs/"); ok {
		return "orgs/" + tail, true
	}
	return "", false
}

func (r *fakeRulesets) route(w http.ResponseWriter, req *http.Request) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := req.URL.Path
	base, rest, ok := cutRulesetsPath(path)
	if !ok {
		return false
	}
	scope, ok := r.scope(base)
	if !ok {
		return false
	}
	store := r.byOwner[scope]
	if store == nil {
		store = map[int64]map[string]any{}
		r.byOwner[scope] = store
	}
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	if requests := r.f.recorded(); req.Method != http.MethodGet && len(requests) > 0 {
		body = requests[len(requests)-1].body
	}
	switch {
	case req.Method == http.MethodGet && rest == "":
		ids := make([]int64, 0, len(store))
		for id := range store {
			ids = append(ids, id)
		}
		entries := make([]string, 0, len(ids))
		for _, id := range ids {
			entry := store[id]
			data, _ := json.Marshal(map[string]any{"id": entry["id"], "name": entry["name"],
				"target": entry["target"], "enforcement": entry["enforcement"]})
			entries = append(entries, string(data))
		}
		fmt.Fprint(w, "["+strings.Join(entries, ",")+"]")
	case req.Method == http.MethodPost && rest == "":
		r.nextID++
		id := r.nextID
		entry := map[string]any{"id": id, "name": body["name"], "target": body["target"],
			"enforcement": body["enforcement"], "conditions": body["conditions"], "rules": body["rules"],
			"bypass_actors": body["bypass_actors"]}
		store[id] = entry
		data, _ := json.Marshal(entry)
		w.WriteHeader(http.StatusCreated)
		w.Write(data)
	case req.Method == http.MethodGet:
		entry, found := store[parseRulesetID(rest)]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return true
		}
		data, _ := json.Marshal(entry)
		w.Write(data)
	case req.Method == http.MethodPut:
		id := parseRulesetID(rest)
		if _, found := store[id]; !found {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return true
		}
		entry := map[string]any{"id": id, "name": body["name"], "target": body["target"],
			"enforcement": body["enforcement"], "conditions": body["conditions"], "rules": body["rules"],
			"bypass_actors": body["bypass_actors"]}
		store[id] = entry
		data, _ := json.Marshal(entry)
		w.Write(data)
	case req.Method == http.MethodDelete:
		id := parseRulesetID(rest)
		if _, found := store[id]; !found {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return true
		}
		delete(store, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

// cutRulesetsPath splits a request path into the prefix below /rulesets and what follows it, "" for the
// list and create routes, or the trailing identifier for get, update, and delete.
func cutRulesetsPath(path string) (prefix, rest string, ok bool) {
	prefix, rest, found := strings.Cut(path, "/rulesets")
	if !found {
		return "", "", false
	}
	return prefix, strings.TrimPrefix(rest, "/"), true
}

func parseRulesetID(rest string) int64 {
	var id int64
	fmt.Sscanf(rest, "%d", &id)
	return id
}

// rulesetsConfig binds every shape of connection the ruleset tools distinguish: a repository connection, an
// organization connection, one that lists no target at all (both are then required explicitly), and one
// that lists the guarded tools.
func rulesetsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all}
	cfg.Connections["repo-listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all, Tools: []string{rulesetsCreate.ID, rulesetsUpdate.ID, rulesetsDelete.ID}}
	cfg.Connections["org"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"orgs/octo-org"}, Permissions: all}
	cfg.Connections["org-listed"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"orgs/octo-org"}, Permissions: all,
		Tools: []string{rulesetsCreate.ID, rulesetsUpdate.ID, rulesetsDelete.ID}}
	cfg.Connections["open"] = config.Connection{Service: "gh", Credential: "gh-reader", Permissions: all}
	return cfg
}

// Every ruleset tool satisfies its output contract through the application core once confirmed, addresses
// exactly the repository or the organization the caller named, and, listed only, is sent exactly once.
func TestRulesetsToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, _, base := serveRulesets(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), rulesetsConfig(base), resolver(red, nil), red)

	listed, err := invoke(t, core, rulesetsList.ID, "repo", `{"repository":"octo-org/example"}`, false)
	if err != nil || !strings.Contains(string(listed), `"name":"protect-main"`) ||
		!strings.Contains(string(listed), `"repository":"octo-org/example"`) {
		t.Fatalf("list repository rulesets = %s, %v", listed, err)
	}
	listedOrg, err := invoke(t, core, rulesetsList.ID, "org", `{"organization":"orgs/octo-org"}`, false)
	if err != nil || !strings.Contains(string(listedOrg), `"name":"org-wide"`) ||
		!strings.Contains(string(listedOrg), `"organization":"orgs/octo-org"`) {
		t.Fatalf("list organization rulesets = %s, %v", listedOrg, err)
	}

	got, err := invoke(t, core, rulesetsGet.ID, "repo", `{"repository":"octo-org/example","id":1420}`, false)
	if err != nil || !strings.Contains(string(got), `"enforcement":"active"`) ||
		!strings.Contains(string(got), `"repository":"octo-org/example"`) ||
		!strings.Contains(string(got), `"rules":[{"type":"deletion"}]`) {
		t.Fatalf("get repository ruleset = %s, %v", got, err)
	}

	before := len(f.recorded())
	created, err := invoke(t, core, rulesetsCreate.ID, "repo-listed",
		`{"repository":"octo-org/example","name":"require-review","target":"branch",`+
			`"enforcement":"active","rules":[{"type":"pull_request"}]}`, true)
	if err != nil || !strings.Contains(string(created), `"name":"require-review"`) ||
		!strings.Contains(string(created), `"repository":"octo-org/example"`) {
		t.Fatalf("create = %s, %v", created, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("requests = %+v, want one create, sent once", requests)
	}
	var createdBody struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(created, &createdBody)

	before = len(f.recorded())
	updated, err := invoke(t, core, rulesetsUpdate.ID, "repo-listed",
		fmt.Sprintf(`{"repository":"octo-org/example","id":%d,"name":"require-review","target":"branch",`+
			`"enforcement":"evaluate","rules":[{"type":"pull_request"}]}`, createdBody.ID), true)
	if err != nil || !strings.Contains(string(updated), `"enforcement":"evaluate"`) {
		t.Fatalf("update = %s, %v", updated, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("requests = %+v, want one update, sent once", requests)
	}

	before = len(f.recorded())
	deleted, err := invoke(t, core, rulesetsDelete.ID, "repo-listed",
		fmt.Sprintf(`{"repository":"octo-org/example","id":%d}`, createdBody.ID), true)
	if err != nil || !strings.Contains(string(deleted), `"deleted":true`) {
		t.Fatalf("delete = %s, %v", deleted, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("requests = %+v, want one delete, sent once", requests)
	}

	// A ruleset ID from one target is unknown under another: the repository create above never reaches
	// orgs/octo-org.
	if _, err := invoke(t, core, rulesetsGet.ID, "org", fmt.Sprintf(`{"organization":"orgs/octo-org","id":%d}`,
		createdBody.ID), false); classOf(err) != provider.ClassNotFound {
		t.Errorf("get a repository ruleset ID under an organization = %v, want not-found", err)
	}
}

// repository and organization are exclusive: giving both or neither is refused before a credential is
// resolved, and an organization outside the connection's targets is refused the same way.
func TestRulesetsTargetIsExclusive(t *testing.T) {
	f, _, base := serveRulesets(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), rulesetsConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, connection, arguments string
	}{
		{"neither", "open", `{"id":1420}`},
		{"both", "open", `{"repository":"octo-org/example","organization":"orgs/octo-org","id":1420}`},
		{"an organization outside the targets", "repo", `{"organization":"orgs/other-org","id":1420}`},
		{"a user owner instead of an organization", "open", `{"organization":"users/octocat","id":1420}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, rulesetsGet.ID, tt.connection, tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.name, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}
}

// The ruleset changes are offered only where a connection's tools list names them, and refused otherwise
// before a secret is read.
func TestRulesetsChangesNeedTheirNameInTheToolsList(t *testing.T) {
	f, _, base := serveRulesets(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), rulesetsConfig(base), resolver(red, &reads), red)

	guarded := []struct{ id, arguments string }{
		{rulesetsCreate.ID, `{"name":"x","target":"branch","enforcement":"active"}`},
		{rulesetsUpdate.ID, `{"id":1420,"name":"x","target":"branch","enforcement":"active"}`},
		{rulesetsDelete.ID, `{"id":1420}`},
	}
	for _, connection := range []string{"repo", "org", "open"} {
		for _, tt := range guarded {
			var unsupported *capability.UnsupportedError
			if _, err := core.Describe(application.DescribeRequest{Operation: tt.id, Connection: connection}); !errors.As(err, &unsupported) {
				t.Errorf("describe %s on %s = %v, want unsupported", tt.id, connection, err)
			}
			reads = 0
			before := len(f.recorded())
			arguments := tt.arguments
			if connection != "open" && !strings.Contains(arguments, "repository") && !strings.Contains(arguments, "organization") {
				arguments = strings.TrimSuffix(arguments, "}")
				if connection == "repo" {
					arguments += `,"repository":"octo-org/example"}`
				} else {
					arguments += `,"organization":"orgs/octo-org"}`
				}
			}
			if _, err := invoke(t, core, tt.id, connection, arguments, true); !errors.As(err, &unsupported) {
				t.Errorf("invoke %s on %s = %v, want unsupported", tt.id, connection, err)
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("%s on %s reached the credential or GitHub", tt.id, connection)
			}
		}
	}

	// repo-listed and org-listed name the three tools and offer exactly those, unconfirmed refused with
	// confirmation-required before GitHub is reached.
	unconfirmed := &application.ConfirmationRequiredError{}
	for _, connection := range []string{"repo-listed", "org-listed"} {
		for _, tt := range guarded {
			before := len(f.recorded())
			if _, err := invoke(t, core, tt.id, connection, tt.arguments, false); !errors.As(err, &unconfirmed) {
				t.Errorf("%s on %s without --confirm = %v, want confirmation-required", tt.id, connection, err)
			}
			if len(f.recorded()) != before {
				t.Errorf("%s on %s reached GitHub before confirmation", tt.id, connection)
			}
		}
	}
}

// Every check of these tools runs before a credential is resolved: the definition's required fields, the
// bounds a JSON schema's maxItems cannot enforce, and the ruleset ID.
func TestRulesetsArgumentsAreValidatedBeforeIO(t *testing.T) {
	f, _, base := serveRulesets(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), rulesetsConfig(base), resolver(red, &reads), red)

	manyRules := make([]string, maxRulesetRules+1)
	for i := range manyRules {
		manyRules[i] = `{"type":"deletion"}`
	}
	oversizedRules := `[` + strings.Join(manyRules, ",") + `]`

	for _, tt := range []struct {
		name, id, connection, arguments string
	}{
		{"missing name", rulesetsCreate.ID, "repo-listed", `{"repository":"octo-org/example","target":"branch","enforcement":"active"}`},
		{"invalid target", rulesetsCreate.ID, "repo-listed", `{"repository":"octo-org/example","name":"x","target":"invalid","enforcement":"active"}`},
		{"invalid enforcement", rulesetsCreate.ID, "repo-listed", `{"repository":"octo-org/example","name":"x","target":"branch","enforcement":"invalid"}`},
		{"a rule without a type", rulesetsCreate.ID, "repo-listed", `{"repository":"octo-org/example","name":"x","target":"branch","enforcement":"active","rules":[{}]}`},
		{"too many rules", rulesetsCreate.ID, "repo-listed", fmt.Sprintf(`{"repository":"octo-org/example","name":"x","target":"branch","enforcement":"active","rules":%s}`, oversizedRules)},
		{"a non-positive ID", rulesetsGet.ID, "repo", `{"repository":"octo-org/example","id":0}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", tt.name, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}
}
