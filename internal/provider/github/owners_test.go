package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// ownerPage answers the owner lists of the organization octo-org page by page; any other owner, or octo-org
// asked for as a user, is one GitHub cannot resolve.
func (f *fakeGitHub) ownerPage(w http.ResponseWriter, document string, variables map[string]any) {
	login, _ := variables["owner"].(string)
	if !strings.EqualFold(login, "octo-org") || !strings.Contains(document, "owner:organization(") {
		fmt.Fprint(w, `{"data":{"owner":null},"errors":[{"type":"NOT_FOUND","path":["owner"],`+
			`"message":"Could not resolve to an Organization"}]}`)
		return
	}
	projects := strings.Contains(document, "projectsV2(first")
	total := len(f.ownerRepos)
	if projects {
		total = len(f.ownerProjects)
	}
	start := 0
	if after, ok := variables["after"].(string); ok {
		start, _ = strconv.Atoi(strings.TrimPrefix(after, "oc-"))
		start++
	}
	end := min(start+int(variables["first"].(float64)), total)
	edges := []string{}
	endCursor := ""
	for i := start; i < end; i++ {
		endCursor = "oc-" + strconv.Itoa(i)
		var node string
		if projects {
			number := f.ownerProjects[i]
			node = fmt.Sprintf(`{"number":%d,"title":"Plan %d","url":"https://github.com/orgs/octo-org/projects/%d",`+
				`"closed":%t}`, number, number, number, number%2 == 0)
		} else {
			node = fmt.Sprintf(`{"name":%q,"visibility":"PRIVATE","isArchived":%t}`, f.ownerRepos[i], i%2 == 1)
		}
		edges = append(edges, `{"cursor":"`+endCursor+`","node":`+node+`}`)
	}
	field := "repositories"
	if projects {
		field = "projectsV2"
	}
	fmt.Fprintf(w, `{"data":{"owner":{%q:{"pageInfo":{"hasNextPage":%t,"endCursor":%q},"edges":[%s]}}}}`,
		field, end < total, endCursor, strings.Join(edges, ","))
}

// ownersConfig holds connections with every shape of target list an owner tool meets.
func ownersConfig(base string) *config.Config {
	cfg := coreConfig(base)
	add := func(name string, targets ...string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets}
	}
	add("open")
	add("org", "orgs/octo-org")
	add("user", "users/octo-org")
	add("concrete", "repos/octo-org/repo-03", "repos/octo-org/repo-07", "orgs/octo-org/projects/2",
		"orgs/octo-org/projects/5")
	add("patterns", "repos/octo-org/*", "orgs/octo-org/projects/*")
	add("projects", "orgs/octo-org/projects/*")
	add("two", "orgs/octo-org", "users/octocat")
	add("far", "repos/octo-org/repo-00", "repos/octo-org/repo-249")
	return cfg
}

// listAll reads every batch of an owner list and returns the values of one field of its entries in order.
func listAll(t *testing.T, core *application.Core, operation, connection, owner, field string, limit int) ([]string,
	int) {
	t.Helper()
	values := []string{}
	cursor := ""
	for batches := 1; batches <= 60; batches++ {
		arguments := map[string]any{"limit": limit}
		if owner != "" {
			arguments["owner"] = owner
		}
		if cursor != "" {
			arguments["cursor"] = cursor
		}
		raw, _ := json.Marshal(arguments)
		result, err := invoke(t, core, operation, connection, string(raw), false)
		if err != nil {
			t.Fatalf("%s on %s = %v", operation, connection, err)
		}
		var page struct {
			Owner        string           `json:"owner"`
			Projects     []map[string]any `json:"projects"`
			Repositories []map[string]any `json:"repositories"`
			NextCursor   string           `json:"next_cursor"`
			HasMore      bool             `json:"has_more"`
		}
		if err := json.Unmarshal(result, &page); err != nil || page.Owner == "" {
			t.Fatalf("%s on %s = %s, %v; want the owner named", operation, connection, result, err)
		}
		for _, entry := range append(page.Projects, page.Repositories...) {
			values = append(values, fmt.Sprint(entry[field]))
		}
		if page.HasMore != (page.NextCursor != "") {
			t.Fatalf("has_more and next_cursor disagree: %s", result)
		}
		if !page.HasMore {
			return values, batches
		}
		cursor = page.NextCursor
	}
	t.Fatal("the list did not end")
	return nil, 0
}

func names(prefix string, numbers ...int) []string {
	out := make([]string, len(numbers))
	for i, number := range numbers {
		out[i] = fmt.Sprintf(prefix, number)
	}
	return out
}

// The owner lists read every project and repository of an owner in order, batch by batch, and show only what
// the targets allow: every one for an owner target, a pattern, or no targets, and the named ones otherwise.
func TestOwnerListsShowWhatTheTargetsAllow(t *testing.T) {
	f := &fakeGitHub{ownerProjects: []int{1, 2, 3, 4, 5, 6, 7}}
	for i := 0; i < 10; i++ {
		f.ownerRepos = append(f.ownerRepos, fmt.Sprintf("repo-%02d", i))
	}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)
	all := names("orgs/octo-org/projects/%d", 1, 2, 3, 4, 5, 6, 7)

	for _, tt := range []struct {
		name, operation, connection, owner, field string
		want                                      []string
	}{
		{"no targets", repositoriesList.ID, "open", "orgs/octo-org", "repository", names("octo-org/repo-%02d",
			0, 1, 2, 3, 4, 5, 6, 7, 8, 9)},
		{"an owner target as the default", repositoriesList.ID, "org", "", "repository", names("octo-org/repo-%02d",
			0, 1, 2, 3, 4, 5, 6, 7, 8, 9)},
		{"an owner target named in another spelling", projectsList.ID, "org", "orgs/OCTO-ORG", "project", all},
		{"a pattern", repositoriesList.ID, "patterns", "orgs/octo-org", "repository", names("octo-org/repo-%02d",
			0, 1, 2, 3, 4, 5, 6, 7, 8, 9)},
		{"a project pattern", projectsList.ID, "projects", "orgs/octo-org", "project", all},
		{"concrete repositories", repositoriesList.ID, "concrete", "orgs/octo-org", "repository",
			names("octo-org/repo-%02d", 3, 7)},
		{"concrete projects", projectsList.ID, "concrete", "orgs/octo-org", "project",
			names("orgs/octo-org/projects/%d", 2, 5)},
		{"one of two owners", projectsList.ID, "two", "orgs/octo-org", "project", all},
	} {
		for _, limit := range []int{1, 3, 30} {
			got, _ := listAll(t, core, tt.operation, tt.connection, tt.owner, tt.field, limit)
			equalIDs(t, got, tt.want)
		}
	}

	// The entries carry what the lists promise, and the owner the call named.
	result, err := invoke(t, core, projectsList.ID, "org", `{"limit":2}`, false)
	if err != nil || string(result) != `{"has_more":true,"next_cursor":"`+nextCursorOf(result)+`","owner":"orgs/octo-org",`+
		`"projects":[{"closed":false,"number":1,"project":"orgs/octo-org/projects/1","title":"Plan 1",`+
		`"url":"https://github.com/orgs/octo-org/projects/1"},{"closed":true,"number":2,`+
		`"project":"orgs/octo-org/projects/2","title":"Plan 2","url":"https://github.com/orgs/octo-org/projects/2"}]}` {
		t.Errorf("projects = %s, %v", result, err)
	}
	result, err = invoke(t, core, repositoriesList.ID, "concrete", `{"owner":"orgs/octo-org"}`, false)
	if err != nil || string(result) != `{"has_more":false,"owner":"orgs/octo-org","repositories":[`+
		`{"archived":true,"repository":"octo-org/repo-03","visibility":"private"},`+
		`{"archived":true,"repository":"octo-org/repo-07","visibility":"private"}]}` {
		t.Errorf("repositories = %s, %v", result, err)
	}
	for _, request := range f.recorded() {
		if request.path == "/api/graphql" && strings.Contains(request.document, "repositories(first") &&
			!strings.Contains(request.document, "ownerAffiliations:[OWNER]") {
			t.Fatalf("the repository list asks for more than the owner owns: %s", request.document)
		}
	}
}

func nextCursorOf(result json.RawMessage) string {
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(result, &page)
	return page.NextCursor
}

// A filtered owner list scans a bounded number of pages per batch and never presents a short batch as the
// end: it continues where it stopped and reaches every shown entry exactly once.
func TestOwnerListsScanABoundedNumberOfPages(t *testing.T) {
	f := &fakeGitHub{}
	for i := 0; i < 250; i++ {
		f.ownerRepos = append(f.ownerRepos, fmt.Sprintf("repo-%02d", i))
	}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)

	got, batches := listAll(t, core, repositoriesList.ID, "far", "orgs/octo-org", "repository", 1)
	equalIDs(t, got, []string{"octo-org/repo-00", "octo-org/repo-249"})
	if batches < 2 {
		t.Errorf("batches = %d, want the scan bound to end a batch early", batches)
	}
}

// Every refusal of an owner, a limit, or a cursor is an invalid request that ends before a secret is read and
// before GitHub is contacted, and names the next step.
func TestOwnerRefusalsTouchNeitherTheSecretNorGitHub(t *testing.T) {
	f := &fakeGitHub{ownerRepos: []string{"a", "b"}, ownerProjects: []int{1, 2}}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, &reads), red)

	first, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org","limit":1}`, false)
	cursor := nextCursorOf(first)
	if err != nil || cursor == "" {
		t.Fatalf("first batch = %s, %v", first, err)
	}
	for _, tt := range []struct{ name, operation, connection, arguments, want string }{
		{"no owner and no targets", repositoriesList.ID, "open", `{}`,
			"owner is required because the connection's targets do not name exactly one; pass owner as " +
				"users/LOGIN or orgs/LOGIN"},
		{"no owner beside two owners", projectsList.ID, "two", `{}`, "pass owner as users/LOGIN or orgs/LOGIN"},
		{"no owner beside concrete targets", projectsList.ID, "concrete", `{}`, "pass owner as users/LOGIN"},
		{"no owner beside patterns", repositoriesList.ID, "patterns", `{}`, "pass owner as users/LOGIN"},
		{"an owner outside the targets", repositoriesList.ID, "org", `{"owner":"orgs/hubot"}`,
			"owner is outside the targets of this connection for its repositories; pass one they allow, or add " +
				"the owner or one of its repositories to the connection's targets"},
		{"a user where an organization is listed", projectsList.ID, "org", `{"owner":"users/octo-org"}`,
			"owner is outside the targets"},
		{"repositories of an owner only its projects allow", repositoriesList.ID, "projects",
			`{"owner":"orgs/octo-org"}`, "for its repositories"},
		{"projects of an owner only its repositories allow", projectsList.ID, "far", `{"owner":"orgs/octo-org"}`,
			"for its projects"},
		{"a malformed owner", repositoriesList.ID, "open", `{"owner":"octo-org"}`,
			"$.owner does not have the required form users/LOGIN or orgs/LOGIN"},
		{"a repository as the owner", repositoriesList.ID, "open", `{"owner":"repos/octo-org/a"}`,
			"$.owner does not have the required form users/LOGIN or orgs/LOGIN"},
		{"a cursor of another owner", repositoriesList.ID, "open",
			`{"owner":"orgs/hubot","cursor":"` + cursor + `"}`, "cursor is not a next_cursor of this list"},
		{"a cursor of the other list", projectsList.ID, "open",
			`{"owner":"orgs/octo-org","cursor":"` + cursor + `"}`, "cursor is not a next_cursor of this list"},
		{"a limit out of bounds", projectsList.ID, "open", `{"owner":"orgs/octo-org","limit":101}`, "$.limit"},
	} {
		reads = 0
		before := len(f.recorded())
		_, err := invoke(t, core, tt.operation, tt.connection, tt.arguments, false)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s = %v, want an invalid request with %q", tt.name, err, tt.want)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", tt.name)
		}
	}
	if _, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org","cursor":"`+cursor+`"}`,
		false); err != nil {
		t.Errorf("the cursor of the same owner = %v, want the next batch", err)
	}
	// A pattern argument or an owner with padding never reaches the list either.
	for _, value := range []string{"orgs/*", " orgs/octo-org"} {
		allowed, _ := parseAllowlist(nil)
		if _, err := allowed.chooseOwner(kindRepository, value); !isInvalidRequest(err) {
			t.Errorf("owner %q = %v, want an invalid request", value, err)
		}
	}
}

// An owner GitHub cannot resolve is not-found naming the owner and what to check; a token GitHub refuses the
// projects of an owner is permission naming them.
func TestOwnerListFailuresNameTheOwner(t *testing.T) {
	f := &fakeGitHub{ownerRepos: []string{"a"}, ownerProjects: []int{1}}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)

	_, err := invoke(t, core, projectsList.ID, "user", `{}`, false)
	if want := "list projects: GitHub does not hold owner users/octo-org or does not show it to this token; " +
		"check the login, and users/ for a user or orgs/ for an organization"; classOf(err) != provider.ClassNotFound ||
		err.Error() != want {
		t.Errorf("an owner of the wrong kind = %v, want not-found %q", err, want)
	}

	for _, tt := range []struct {
		name, operation, answer string
		class                   provider.Class
		want                    string
	}{
		{"missing scopes", projectsList.ID, `{"errors":[{"type":"INSUFFICIENT_SCOPES","message":"Your token has not ` +
			`been granted the required scopes to execute this query. The 'projectsV2' field requires one of the ` +
			`following scopes: ['read:project']"}]}`, provider.ClassPermission,
			"list projects: this GitHub token may not read the projects of owner orgs/octo-org; check its scopes " +
				"or permissions; classic: scope read:project; fine-grained: Projects: read of the organization, as " +
				"the projects of a user need a classic token"},
		{"a refused owner", projectsList.ID, `{"data":{"owner":{"projectsV2":null}},"errors":[{"type":"FORBIDDEN",` +
			`"path":["owner","projectsV2"],"message":"Resource not accessible by personal access token"}]}`,
			provider.ClassPermission, "list projects: this GitHub token may not read the projects of owner " +
				"orgs/octo-org; check its scopes or permissions"},
		{"refused repositories", repositoriesList.ID, `{"data":{"owner":null},"errors":[{"type":"FORBIDDEN",` +
			`"path":["owner","repositories"],"message":"Resource not accessible"}]}`, provider.ClassPermission,
			"this GitHub token may not read the repositories of owner orgs/octo-org"},
		{"an answer without the list", repositoriesList.ID, `{"data":{"owner":{}}}`, provider.ClassInvalidResponse,
			"GitHub returned an owner without its repositories"},
		{"an entry without a cursor", repositoriesList.ID, `{"data":{"owner":{"repositories":{"pageInfo":` +
			`{"hasNextPage":false},"edges":[{"node":{"name":"a"}}]}}}}`, provider.ClassInvalidResponse,
			"GitHub returned repositories without a cursor"},
	} {
		answer := tt.answer
		f.failure = func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/api/graphql" {
				return false
			}
			_, _ = w.Write([]byte(answer))
			return true
		}
		_, err := invoke(t, core, tt.operation, "org", `{}`, false)
		if classOf(err) != tt.class || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s = %v (class %q), want %q with %q", tt.name, err, classOf(err), tt.class, tt.want)
		}
	}
	f.failure = nil
}

// An owner target never becomes the target of a repository or project tool, nor of the connection test.
func TestAnOwnerTargetAllowsNoRepositoryOrProject(t *testing.T) {
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v3/user" {
			return false
		}
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
		return true
	}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, &reads), red)
	for _, tt := range []struct{ operation, arguments string }{
		{"github.issues.list", `{}`},
		{"github.issues.list", `{"repository":"octo-org/example"}`},
		{"github.projectitems.list", `{"project":"orgs/octo-org/projects/7"}`},
	} {
		if _, err := invoke(t, core, tt.operation, "org", tt.arguments, false); !isInvalidRequest(err) || reads != 0 {
			t.Errorf("%s %s on an owner target = %v, want an invalid request before the secret", tt.operation,
				tt.arguments, err)
		}
	}
	resolved := resolvedConnection("gh", base, "")
	resolved.Targets = []string{"orgs/octo-org", repoTarget}
	c, err := open(context.Background(), resolved, resolver(red, nil), red, freeLimiter())
	if err != nil || c.target.String() != repoTarget {
		t.Errorf("default client target = %v, %v; want the repository", c.target, err)
	}
	resolved.Targets = []string{"orgs/octo-org"}
	before := len(f.recorded())
	class, err := TestConnection(context.Background(), resolved, resolver(red, nil), red)
	if requests := f.recorded()[before:]; err != nil || class != provider.ClassOK || len(requests) != 1 ||
		requests[0].path != "/api/v3/user" {
		t.Errorf("test of an owner target = %q, %v with %+v; want the user", class, err, requests)
	}
}

// The target metadata names every kind with forms the configuration accepts, so an editor can offer them.
func TestTargetMetadataDescribesEveryKind(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	kinds := []string{}
	fill := strings.NewReplacer("OWNER", "octo-org", "LOGIN", "octocat", "REPO", "example", "NUMBER", "3")
	for _, kind := range metadata.Target.Kinds {
		kinds = append(kinds, kind.Name)
		if kind.Description == "" || len(kind.Forms) == 0 {
			t.Errorf("kind %s = %+v, want a description and forms", kind.Name, kind)
		}
		for _, form := range kind.Forms {
			parsed, err := parseTarget(fill.Replace(form))
			if err != nil || parsed.argumentName() != kind.Name || metadata.Target.Validate(fill.Replace(form)) != nil {
				t.Errorf("form %s of %s = %+v, %v", form, kind.Name, parsed, err)
			}
		}
	}
	equalIDs(t, kinds, []string{"repository", "project", "owner"})

	metadata.Target.Kinds[0].Forms[0] = "changed"
	again, _ := reg.ProviderMetadata(Provider)
	if again.Target.Kinds[0].Forms[0] != "repos/OWNER/REPO" {
		t.Error("the published metadata shares its target kinds with a caller")
	}
}
