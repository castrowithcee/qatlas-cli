package forge

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// labelFixture is the answer of both products for a label.
func labelFixture(id string) string {
	return `{"id":` + id + `,"name":"bug","description":"Something is broken","color":"d73a4a","exclusive":false,` +
		`"is_archived":false,"url":"https://` + apiHost + `/api/v1/repos/acme/widget/labels/` + id + `"}`
}

// labelEnvironment is an environment whose configuration also holds connections that list the label delete
// tools, and one that allows an organization next to a repository.
func labelEnvironment(t *testing.T, p *product, handler func(call) *http.Response) *environment {
	t.Helper()
	e := newEnvironment(t, p, handler)
	cfg := testConfig(p)
	cfg.Connections["del-repo"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: []string{p.toolID(labelsDelete)}}
	cfg.Connections["del-org"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme"}, Tools: []string{p.toolID(orgLabelsDelete)}}
	cfg.Connections["org-repo"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme", "repos/other/place"}}
	cfg.Connections["two-orgs"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme", "orgs/beta"}}
	e.core = application.New(registry(t), cfg, resolver(e.red, e.reads), e.red)
	return e
}

func labelServer(base string, change func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		if c.method == http.MethodGet && c.path == base+"/12" {
			return jsonResponse(200, labelFixture("12"))
		}
		if c.method == http.MethodGet && c.path == base+"/13" {
			return jsonResponse(200, labelFixture("99")) // an answer for another ID
		}
		if c.method == http.MethodGet {
			return notFoundResponse()
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	}
}

const (
	repoLabelBase = "/api/v1/repos/acme/widget/labels"
	orgLabelBase  = "/api/v1/orgs/acme/labels"
)

func TestLabelListReadsEveryMemberAndPages(t *testing.T) {
	cases := []struct {
		tl         *tool
		connection string
		arguments  string
		path       string
		key, value string
	}{
		{labelsList, "repo", `{"limit":2}`, repoLabelBase, "repository", "acme/widget"},
		{orgLabelsList, "org", `{"limit":2}`, orgLabelBase, "owner", "orgs/acme"},
	}
	for _, p := range products {
		for _, c := range cases {
			e := labelEnvironment(t, p, func(call) *http.Response {
				return jsonResponse(200, `[`+labelFixture("12")+`,`+labelFixture("13")+`]`,
					"Link", `<https://forge.example.test/next>; rel="next"`)
			})
			result, err := e.invoke(t, c.tl, c.connection, c.arguments)
			if err != nil {
				t.Fatalf("%s %s: %v", p.id, c.tl.group, err)
			}
			labels, _ := result["labels"].([]any)
			if len(labels) != 2 || result[c.key] != c.value || result["has_more"] != true || result["next_cursor"] == nil {
				t.Fatalf("%s %s result = %v", p.id, c.tl.group, result)
			}
			first := labels[0].(map[string]any)
			for _, name := range []string{"id", "name", "description", "color", "exclusive", "is_archived", "url"} {
				if _, ok := first[name]; !ok {
					t.Errorf("%s %s: label member %s is missing", p.id, c.tl.group, name)
				}
			}
			sent := (*e.calls)[0]
			if len(*e.calls) != 1 || sent.method != http.MethodGet || sent.path != c.path ||
				sent.query != "limit=2&page=1" {
				t.Errorf("%s %s: call %+v", p.id, c.tl.group, sent)
			}
			next, _ := result["next_cursor"].(string)
			if _, err := e.invoke(t, c.tl, c.connection, `{"cursor":"`+next+`"}`); err != nil {
				t.Fatalf("%s %s continuation: %v", p.id, c.tl.group, err)
			}
			if got := (*e.calls)[1].query; got != "limit=2&page=2" {
				t.Errorf("%s %s continuation query = %q", p.id, c.tl.group, got)
			}
			// A cursor of one list does not continue another.
			if _, err := e.invoke(t, c.tl, "open", `{"cursor":"`+next+`","`+map[string]string{
				"repository": "repository", "owner": "owner"}[c.key]+`":"`+map[string]string{
				"repository": "other/place", "owner": "orgs/beta"}[c.key]+`"}`); !isInvalidRequest(err) {
				t.Errorf("%s %s: cursor of another list: %v", p.id, c.tl.group, err)
			}
		}
	}
}

func TestLabelListSortIsForgejoOnly(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{labelsList, orgLabelsList} {
			connection, arguments := "repo", `{"sort":"mostissues"}`
			if tl == orgLabelsList {
				connection = "org"
			}
			e := labelEnvironment(t, p, func(call) *http.Response { return jsonResponse(200, `[]`) })
			_, err := e.invoke(t, tl, connection, arguments)
			if p == gitea {
				if !isInvalidRequest(err) || *e.reads != 0 || len(*e.calls) != 0 {
					t.Errorf("gitea %s sort: %v, reads %d, calls %d", tl.group, err, *e.reads, len(*e.calls))
				}
				continue
			}
			if err != nil || (*e.calls)[0].query != "limit=30&page=1&sort=mostissues" {
				t.Errorf("forgejo %s sort: %v, calls %v", tl.group, err, *e.calls)
			}
			if _, err := e.invoke(t, tl, connection, `{"sort":"other"}`); !isInvalidRequest(err) {
				t.Errorf("forgejo %s unknown sort: %v", tl.group, err)
			}
		}
	}
}

func TestLabelGetIsBoundByThePathAndChecksTheID(t *testing.T) {
	for _, p := range products {
		e := labelEnvironment(t, p, func(c call) *http.Response {
			return labelServer(repoLabelBase, nil)(c)
		})
		result, err := e.invoke(t, labelsGet, "repo", `{"id":12}`)
		if err != nil || result["id"] != float64(12) || result["name"] != "bug" || result["color"] != "d73a4a" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + repoLabelBase + "/12"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		// An answer for another ID is refused as invalid.
		if _, err := e.invoke(t, labelsGet, "repo", `{"id":13}`); err == nil || classOfError(err) != "invalid-provider-response" {
			t.Errorf("%s: answer with another ID: %v", p.id, err)
		}
		// A label of another repository is not found on this repository's path.
		if _, err := e.invoke(t, labelsGet, "repo", `{"id":14}`); classOfError(err) != "not-found" {
			t.Errorf("%s: foreign ID: %v", p.id, err)
		}
		for _, bad := range []string{`{}`, `{"id":0}`, `{"id":12,"owner":"orgs/acme"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, labelsGet, "repo", bad); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestOrganizationLabelsNeedAnOwnerTarget(t *testing.T) {
	for _, p := range products {
		e := labelEnvironment(t, p, labelServer(orgLabelBase, func(c call) *http.Response {
			if c.method == http.MethodPost {
				return jsonResponse(201, labelFixture("12"))
			}
			return notFoundResponse()
		}))
		// Refused before the secret and any request, without naming the owner.
		for connection, arguments := range map[string]string{
			"repo":     `{"owner":"orgs/acme","id":12}`,
			"repo ":    `{"id":12}`,
			"owner":    `{"owner":"orgs/alice","id":12}`,
			"org":      `{"owner":"orgs/secret-org","id":12}`,
			"open":     `{"id":12}`,
			"mixed":    `{"id":12}`,
			"two-orgs": `{"id":12}`,
			"org-repo": `{"owner":"orgs/other","id":12}`,
		} {
			connection = strings.TrimSpace(connection)
			if _, err := e.invoke(t, orgLabelsGet, connection, arguments); !isInvalidRequest(err) ||
				strings.Contains(err.Error(), "secret-org") || *e.reads != 0 || len(*e.calls) != 0 {
				t.Errorf("%s %s %s: %v, reads %d, calls %d", p.id, connection, arguments, err, *e.reads, len(*e.calls))
			}
		}
		// A user is no organization, also when the targets name a user.
		if _, err := e.invoke(t, orgLabelsList, "owner", `{"owner":"users/Alice"}`); !isInvalidRequest(err) ||
			len(*e.calls) != 0 {
			t.Errorf("%s: user owner: %v", p.id, err)
		}
		// The organization of an owner target is read, also by default.
		for connection, arguments := range map[string]string{"org": `{"id":12}`, "open": `{"owner":"orgs/acme","id":12}`,
			"org-repo": `{"owner":"orgs/acme","id":12}`, "two-orgs": `{"owner":"orgs/acme","id":12}`} {
			*e.calls = nil
			result, err := e.invoke(t, orgLabelsGet, connection, arguments)
			if err != nil || result["id"] != float64(12) {
				t.Fatalf("%s %s: %v, %v", p.id, connection, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + orgLabelBase + "/12"}) {
				t.Errorf("%s %s calls = %v", p.id, connection, got)
			}
		}
		// Names compare without case.
		*e.calls = nil
		if _, err := e.invoke(t, orgLabelsGet, "org", `{"owner":"orgs/ACME","id":99}`); classOfError(err) != "not-found" ||
			len(*e.calls) != 1 {
			t.Errorf("%s: case-insensitive owner: %v", p.id, err)
		}
		// The repository tools never take an owner, the organization tools never a repository.
		if _, err := e.invoke(t, orgLabelsList, "org", `{"repository":"acme/widget"}`); !isInvalidRequest(err) {
			t.Errorf("%s: repository on org-labels: %v", p.id, err)
		}
		if _, err := e.invoke(t, labelsList, "org", `{"owner":"orgs/acme"}`); !isInvalidRequest(err) {
			t.Errorf("%s: owner on labels: %v", p.id, err)
		}
		// Creation is refused the same way.
		*e.calls = nil
		if _, err := e.invoke(t, orgLabelsCreate, "repo", `{"owner":"orgs/acme","name":"bug","color":"d73a4a"}`); !isInvalidRequest(err) ||
			len(*e.calls) != 0 {
			t.Errorf("%s: create on a repository connection: %v", p.id, err)
		}
	}
}

func TestLabelCreateSendsOneRequestWithValidatedMembers(t *testing.T) {
	cases := []struct {
		tl         *tool
		connection string
		target     string
		path       string
	}{
		{labelsCreate, "repo", `"repository":"acme/widget",`, repoLabelBase},
		{orgLabelsCreate, "org", `"owner":"orgs/acme",`, orgLabelBase},
	}
	for _, p := range products {
		for _, c := range cases {
			e := labelEnvironment(t, p, func(call) *http.Response { return jsonResponse(201, labelFixture("12")) })
			arguments := `{` + c.target + `"name":"scope/bug","color":"#d73a4a","description":"Broken",` +
				`"exclusive":true,"is_archived":false}`
			want := map[string]any{"name": "scope/bug", "color": "#d73a4a", "description": "Broken", "exclusive": true,
				"is_archived": false}
			if _, err := e.invokeAs(t, c.tl, c.connection, arguments, false); err == nil ||
				!errors.As(err, new(*application.ConfirmationRequiredError)) || len(*e.calls) != 0 {
				t.Fatalf("%s %s: unconfirmed create: %v", p.id, c.tl.group, err)
			}
			result, err := e.invoke(t, c.tl, c.connection, arguments)
			if err != nil || result["id"] != float64(12) {
				t.Fatalf("%s %s: %v, %v", p.id, c.tl.group, result, err)
			}
			sent := (*e.calls)[0]
			if len(*e.calls) != 1 || sent.method != http.MethodPost || sent.path != c.path ||
				sent.contentType != "application/json" || !reflect.DeepEqual(bodyOf(t, sent), want) {
				t.Errorf("%s %s: call %+v", p.id, c.tl.group, sent)
			}
			for _, bad := range []string{`{"color":"d73a4a"}`, `{"name":"bug"}`, `{"name":"","color":"d73a4a"}`,
				`{"name":"bug","color":"red"}`, `{"name":"bug","color":"d73a4"}`, `{"name":"bug","color":"##d73a4a"}`,
				`{"name":"bug","color":"d73a4a","description":"` + strings.Repeat("x", 201) + `"}`,
				`{"name":"bug\u0000","color":"d73a4a"}`, `{"name":"bug","color":"d73a4a","id":3}`} {
				before := len(*e.calls)
				if _, err := e.invoke(t, c.tl, c.connection, bad); !isInvalidRequest(err) || len(*e.calls) != before {
					t.Errorf("%s %s %s: %v", p.id, c.tl.group, bad, err)
				}
			}
			// A short color without the hash is accepted.
			if _, err := e.invoke(t, c.tl, c.connection, `{"name":"x","color":"abc"}`); err != nil {
				t.Errorf("%s %s short color: %v", p.id, c.tl.group, err)
			}
		}
	}
}

func TestLabelUpdateReadsThenPatchesOnlyTheSetMembers(t *testing.T) {
	cases := []struct {
		tl         *tool
		connection string
		target     string
		base       string
	}{
		{labelsUpdate, "repo", `"repository":"acme/widget",`, repoLabelBase},
		{orgLabelsUpdate, "org", `"owner":"orgs/acme",`, orgLabelBase},
	}
	for _, p := range products {
		for _, c := range cases {
			e := labelEnvironment(t, p, labelServer(c.base, func(call) *http.Response {
				return jsonResponse(200, labelFixture("12"))
			}))
			arguments := `{` + c.target + `"id":12,"description":"","is_archived":true}`
			if _, err := e.invokeAs(t, c.tl, c.connection, arguments, false); err == nil || len(*e.calls) != 0 {
				t.Fatalf("%s %s: unconfirmed update: %v", p.id, c.tl.group, err)
			}
			if _, err := e.invoke(t, c.tl, c.connection, arguments); err != nil {
				t.Fatalf("%s %s: %v", p.id, c.tl.group, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + c.base + "/12", "PATCH " + c.base + "/12"}) {
				t.Fatalf("%s %s calls = %v", p.id, c.tl.group, got)
			}
			if body := bodyOf(t, (*e.calls)[1]); !reflect.DeepEqual(body, map[string]any{"description": "",
				"is_archived": true}) {
				t.Errorf("%s %s body = %v", p.id, c.tl.group, body)
			}
			// A label of another repository or organization fails on the evidence read; no change is sent.
			*e.calls = nil
			if _, err := e.invoke(t, c.tl, c.connection, `{`+c.target+`"id":14,"name":"x"}`); classOfError(err) != "not-found" ||
				len(*e.calls) != 1 || (*e.calls)[0].method != http.MethodGet {
				t.Errorf("%s %s foreign ID: %v, %v", p.id, c.tl.group, err, methods(e))
			}
			before := len(*e.calls)
			for _, bad := range []string{`{"id":12}`, `{"name":"x"}`, `{"id":12,"color":"zzz"}`} {
				if _, err := e.invoke(t, c.tl, c.connection, strings.Replace(bad, "{", `{`+c.target, 1)); !isInvalidRequest(err) ||
					len(*e.calls) != before {
					t.Errorf("%s %s %s: %v", p.id, c.tl.group, bad, err)
				}
			}
		}
	}
}

func TestLabelDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	cases := []struct {
		tl         *tool
		connection string
		target     string
		base       string
	}{
		{labelsDelete, "del-repo", `"repository":"acme/widget",`, repoLabelBase},
		{orgLabelsDelete, "del-org", `"owner":"orgs/acme",`, orgLabelBase},
	}
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, c := range cases {
			d, _, ok := reg.Lookup(p.toolID(c.tl))
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" || d.Risk.Effect != "delete" {
				t.Fatalf("%s %s descriptor = %+v", p.id, c.tl.group, d)
			}
			for _, profile := range metadata.Profiles {
				for _, id := range profile.Tools {
					if id == d.ID {
						t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
					}
				}
			}
			e := labelEnvironment(t, p, labelServer(c.base, func(c call) *http.Response {
				if c.method == http.MethodDelete {
					return jsonResponse(204, "")
				}
				return notFoundResponse()
			}))
			plain := map[string]string{"del-repo": "repo", "del-org": "org"}[c.connection]
			if _, err := e.invoke(t, c.tl, plain, `{`+c.target+`"id":12}`); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s: delete without a tools list: %v", p.id, c.tl.group, err)
			}
			if _, err := e.invokeAs(t, c.tl, c.connection, `{`+c.target+`"id":12}`, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s: unconfirmed delete: %v", p.id, c.tl.group, err)
			}
			result, err := e.invoke(t, c.tl, c.connection, `{`+c.target+`"id":12}`)
			if err != nil || result["deleted"] != true || result["id"] != float64(12) {
				t.Fatalf("%s %s: %v, %v", p.id, c.tl.group, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + c.base + "/12", "DELETE " + c.base + "/12"}) ||
				(*e.calls)[1].body != "" {
				t.Errorf("%s %s calls = %v", p.id, c.tl.group, got)
			}
			// A label of another repository or organization is never deleted.
			*e.calls = nil
			if _, err := e.invoke(t, c.tl, c.connection, `{`+c.target+`"id":14}`); classOfError(err) != "not-found" ||
				len(*e.calls) != 1 {
				t.Errorf("%s %s foreign ID: %v, %v", p.id, c.tl.group, err, methods(e))
			}
		}
	}
}

func TestLabelRepositoryOutsideTheTargetsIsRefusedBeforeTheSecret(t *testing.T) {
	for _, p := range products {
		e := labelEnvironment(t, p, labelServer(repoLabelBase, nil))
		for _, tl := range []*tool{labelsList, labelsGet, labelsCreate, labelsUpdate} {
			arguments := `{"repository":"secret-owner/hidden","id":12,"name":"x","color":"abc"}`
			if _, err := e.invoke(t, tl, "repo", arguments); !isInvalidRequest(err) ||
				strings.Contains(err.Error(), "secret-owner") || *e.reads != 0 || len(*e.calls) != 0 {
				t.Errorf("%s %s: %v, reads %d, calls %d", p.id, tl.action, err, *e.reads, len(*e.calls))
			}
		}
		if _, err := e.invoke(t, labelsList, "open", `{}`); !isInvalidRequest(err) {
			t.Errorf("%s: a repository is required without a single target: %v", p.id, err)
		}
	}
}
