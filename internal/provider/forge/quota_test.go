package forge

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// quotaInfoFixture is a QuotaInfo of the Forgejo swagger: every member, group names left out as for a non-administrator.
const quotaInfoFixture = `{"groups":[{"rules":[{"limit":1000,"subjects":["size:all","size:repos:all"]}]}],` +
	`"used":{"size":{"repos":{"public":1,"private":2},"git":{"LFS":3},"assets":{"artifacts":4,` +
	`"attachments":{"issues":5,"releases":6},"packages":{"all":7}}}}}`

const (
	artifactsFixture = `[{"name":"a1","size":10,"html_url":"https://forge.example.test/acme/widget/actions/runs/1"},` +
		`{"name":"a2","size":20,"html_url":"https://forge.example.test/other/tool/actions/runs/2"},null,` +
		`{"name":"a3","size":30,"html_url":"https://forge.example.test/acme/gadget/actions/runs/3"},` +
		`{"name":"a4","size":40,"html_url":"https://forge.example.test/oddly/placed"}]`
	attachmentsFixture = `[{"name":"f1","size":1,"api_url":"https://forge.example.test/api/v1/repos/acme/widget/issues/1/assets/5",` +
		`"contained_in":{"api_url":"https://forge.example.test/api/v1/repos/acme/widget/issues/1",` +
		`"html_url":"https://forge.example.test/acme/widget/issues/1"}},` +
		`{"name":"f2","size":2,"api_url":"https://forge.example.test/api/v1/repos/other/tool/releases/3/assets/6",` +
		`"contained_in":{"api_url":"https://forge.example.test/api/v1/repos/other/tool/releases/3",` +
		`"html_url":"https://forge.example.test/other/tool/releases/tag/v1"}},` +
		`{"name":"f3","size":3,"api_url":"https://forge.example.test/api/v1/repos/acme/widget/issues/comments/4/assets/7",` +
		`"contained_in":{"api_url":"https://forge.example.test/api/v1/repos/other/tool/issues/comments/4",` +
		`"html_url":"https://forge.example.test/acme/widget/issues/1#issuecomment-4"}},` +
		`{"name":"f4","size":4,"api_url":"/assets/8","contained_in":{}}]`
	packagesFixture = `[{"name":"p1","type":"generic","version":"1.0","size":100,` +
		`"html_url":"https://forge.example.test/acme/-/packages/generic/p1/1.0"}]`
)

// quotaServer answers the quota routes. on switches the quota routes off like an instance does (404); baseUp says
// whether the base routes /user and /orgs/acme answer; status, when not 200, replaces the answer of every quota route.
type quotaState struct {
	on, baseUp bool
	status     int
	login      string
}

func quotaServer(s *quotaState) func(call) *http.Response {
	return func(c call) *http.Response {
		path := strings.TrimPrefix(c.path, "/api/v1")
		quotaRoute := strings.Contains(path, "/quota")
		if !quotaRoute {
			if !s.baseUp {
				return notFoundResponse()
			}
			if path == "/user" {
				return jsonResponse(200, `{"id":1,"login":"`+s.login+`"}`)
			}
			if path == "/orgs/acme" {
				return jsonResponse(200, `{"id":2,"name":"acme"}`)
			}
			return notFoundResponse()
		}
		if !s.on {
			return notFoundResponse()
		}
		if s.status != 200 {
			return jsonResponse(s.status, `{"message":"`+bodyCanary+`"}`)
		}
		switch {
		case strings.HasSuffix(path, "/quota/check"):
			return jsonResponse(200, `true`)
		case strings.HasSuffix(path, "/quota/artifacts"):
			return jsonResponse(200, artifactsFixture, "X-Total-Count", "9")
		case strings.HasSuffix(path, "/quota/attachments"):
			return jsonResponse(200, attachmentsFixture, "Link", `<https://forge.example.test/next>; rel="next"`)
		case strings.HasSuffix(path, "/quota/packages"):
			return jsonResponse(200, packagesFixture)
		case strings.HasSuffix(path, "/quota"):
			return jsonResponse(200, quotaInfoFixture)
		}
		return notFoundResponse()
	}
}

// argumentsFor drops the subject, which only quota.check takes.
func argumentsFor(tl *tool, args string) string {
	if tl == quotaCheck {
		return args
	}
	return strings.NewReplacer(`,"subject":"size:all"`, "", `{"subject":"size:all"}`, `{}`).Replace(args)
}

func upQuota() *quotaState { return &quotaState{on: true, baseUp: true, status: 200, login: "alice"} }

func TestQuotaGetShowsEveryMemberOfTheAccountAndOfAnOrganization(t *testing.T) {
	for _, c := range []struct{ connection, args, path string }{
		{"open", `{}`, "/api/v1/user/quota"},
		{"org", `{"owner":"orgs/acme"}`, "/api/v1/orgs/acme/quota"},
		{"open", `{"owner":"orgs/ACME"}`, "/api/v1/orgs/ACME/quota"},
	} {
		e := newEnvironment(t, forgejo, quotaServer(upQuota()))
		result, err := e.invoke(t, quotaGet, c.connection, c.args)
		if err != nil {
			t.Fatalf("%s %s: %v", c.connection, c.args, err)
		}
		used := result["used"].(map[string]any)["size"].(map[string]any)
		assets := used["assets"].(map[string]any)
		if used["repos"].(map[string]any)["public"] != float64(1) || used["git"].(map[string]any)["LFS"] != float64(3) ||
			assets["artifacts"] != float64(4) || assets["attachments"].(map[string]any)["releases"] != float64(6) ||
			assets["packages"].(map[string]any)["all"] != float64(7) {
			t.Errorf("used = %v", used)
		}
		rule := result["groups"].([]any)[0].(map[string]any)["rules"].([]any)[0].(map[string]any)
		if rule["limit"] != float64(1000) || !reflect.DeepEqual(rule["subjects"], []any{"size:all", "size:repos:all"}) {
			t.Errorf("rule = %v", rule)
		}
		if _, ok := result["owner"]; ok != strings.Contains(c.args, "owner") {
			t.Errorf("owner in %v for %s", result, c.args)
		}
		if got := methods(e); !slices.Equal(got, []string{"GET " + c.path}) {
			t.Errorf("calls = %v, want %s", got, c.path)
		}
	}
}

func TestQuotaOfTheAccountNeedsTheAccountInTheTargets(t *testing.T) {
	// users/Alice names the account: it is proven by reading /user first.
	e := newEnvironment(t, forgejo, quotaServer(upQuota()))
	if _, err := e.invoke(t, quotaGet, "owner", `{}`); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if got := methods(e); !slices.Equal(got, []string{"GET /api/v1/user", "GET /api/v1/user/quota"}) {
		t.Errorf("calls = %v", got)
	}
	other := upQuota()
	other.login = "mallory"
	e = newEnvironment(t, forgejo, quotaServer(other))
	_, err := e.invoke(t, quotaGet, "owner", `{}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "mallory") || len(*e.calls) != 1 {
		t.Errorf("another account: err %v, calls %v", err, methods(e))
	}
	// Targets that do not name the account, or name a repository, refuse before any I/O.
	for _, connection := range []string{"org", "repo", "mixed", "two"} {
		e := newEnvironment(t, forgejo, quotaServer(upQuota()))
		_, err := e.invoke(t, quotaGet, connection, `{}`)
		refused(t, e, connection, err)
	}
}

func TestQuotaOfAnOrganizationOutsideTheTargetsIsRefusedBeforeAnyIO(t *testing.T) {
	for _, tl := range []*tool{quotaGet, quotaListArtifacts, quotaListAttachments, quotaListPackages, quotaCheck} {
		for _, c := range []struct{ connection, args string }{
			{"org", `{"owner":"orgs/secretorg","subject":"size:all"}`},
			{"owner", `{"owner":"orgs/secretorg","subject":"size:all"}`},
			{"owner", `{"owner":"orgs/acme","subject":"size:all"}`},
			{"repo", `{"owner":"orgs/acme","subject":"size:all"}`},
			{"mixed", `{"owner":"orgs/acme","subject":"size:all"}`},
			{"org", `{"owner":"users/acme","subject":"size:all"}`},
			{"open", `{"owner":"acme","subject":"size:all"}`},
			{"open", `{"owner":"orgs/bad name","subject":"size:all"}`},
		} {
			e := newEnvironment(t, forgejo, quotaServer(upQuota()))
			_, err := e.invoke(t, tl, c.connection, argumentsFor(tl, c.args))
			refused(t, e, tl.action+" "+c.args, err)
			if err != nil && strings.Contains(err.Error(), "secretorg") {
				t.Errorf("the refusal names the organization: %v", err)
			}
		}
	}
}

func TestQuotaListsKeepOnlyRowsInsideTheTargets(t *testing.T) {
	for _, c := range []struct {
		tl         *tool
		key        string
		connection string
		args       string
		want       []string
		more       bool
	}{
		{quotaListArtifacts, "artifacts", "open", `{}`, []string{"a1", "a2", "a3", "a4"}, true},
		{quotaListArtifacts, "artifacts", "org", `{"owner":"orgs/acme"}`, []string{"a1", "a3"}, true},
		{quotaListAttachments, "attachments", "org", `{"owner":"orgs/acme"}`, []string{"f1"}, true},
		{quotaListAttachments, "attachments", "open", `{"owner":"orgs/acme"}`, []string{"f1", "f2", "f3", "f4"}, true},
		{quotaListPackages, "packages", "org", `{"owner":"orgs/acme"}`, []string{"p1"}, false},
		{quotaListArtifacts, "artifacts", "owner", `{}`, nil, true},
	} {
		e := newEnvironment(t, forgejo, quotaServer(upQuota()))
		result, err := e.invoke(t, c.tl, c.connection, c.args)
		if err != nil {
			t.Fatalf("%s %s on %s: %v", c.tl.action, c.args, c.connection, err)
		}
		var got []string
		for _, row := range rowsOf(t, result, c.key) {
			got = append(got, row["name"].(string))
		}
		// The users/Alice connection reads rows of the account: none names a repository of Alice.
		if !slices.Equal(got, c.want) {
			t.Errorf("%s %s on %s = %v, want %v", c.tl.action, c.args, c.connection, got, c.want)
		}
		if result["has_more"] != c.more || (c.more && result["next_cursor"] == nil) {
			t.Errorf("%s: has_more = %v", c.tl.action, result)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.query != "limit=30&page=1" {
			t.Errorf("%s query = %q", c.tl.action, last.query)
		}
	}
}

func TestQuotaListRowsCarryEveryDocumentedMember(t *testing.T) {
	e := newEnvironment(t, forgejo, quotaServer(upQuota()))
	result, err := e.invoke(t, quotaListAttachments, "open", `{"owner":"orgs/acme","limit":5}`)
	if err != nil {
		t.Fatal(err)
	}
	row := rowsOf(t, result, "attachments")[0]
	contained := row["contained_in"].(map[string]any)
	if row["name"] != "f1" || row["size"] != float64(1) || row["api_url"] == nil || contained["api_url"] == nil ||
		contained["html_url"] == nil || result["owner"] != "orgs/acme" {
		t.Errorf("attachment = %v", result)
	}
	e = newEnvironment(t, forgejo, quotaServer(upQuota()))
	result, _ = e.invoke(t, quotaListPackages, "open", `{}`)
	pkg := rowsOf(t, result, "packages")[0]
	for _, member := range []string{"name", "type", "version", "size", "html_url"} {
		if pkg[member] == nil {
			t.Errorf("package lacks %s: %v", member, pkg)
		}
	}
	e = newEnvironment(t, forgejo, quotaServer(upQuota()))
	result, _ = e.invoke(t, quotaListArtifacts, "open", `{}`)
	if artifact := rowsOf(t, result, "artifacts")[0]; artifact["name"] == nil || artifact["size"] == nil ||
		artifact["html_url"] == nil {
		t.Errorf("artifact = %v", artifact)
	}
}

func TestQuotaListEndsByTheHeadersAndContinuesWithItsCursor(t *testing.T) {
	var header string
	e := newEnvironment(t, forgejo, func(c call) *http.Response {
		if c.query == "limit=4&page=2" {
			return jsonResponse(200, packagesFixture, "X-Total-Count", "5")
		}
		return jsonResponse(200, packagesFixture, "Link", header)
	})
	header = `<https://forge.example.test/next>; rel="next"`
	first, err := e.invoke(t, quotaListPackages, "open", `{"limit":4}`)
	if err != nil || first["has_more"] != true {
		t.Fatalf("first = %v, %v", first, err)
	}
	second, err := e.invoke(t, quotaListPackages, "open", `{"cursor":"`+first["next_cursor"].(string)+`"}`)
	if err != nil || second["has_more"] != false || second["next_cursor"] != nil {
		t.Fatalf("second = %v, %v", second, err)
	}
	// A cursor of the account does not continue the list of an organization.
	_, err = e.invoke(t, quotaListPackages, "open", `{"owner":"orgs/acme","cursor":"`+first["next_cursor"].(string)+`"}`)
	if !isInvalidRequest(err) {
		t.Errorf("foreign cursor: %v", err)
	}
}

func TestQuotaCheckAsksOneSubjectOfTheSwaggerEnum(t *testing.T) {
	e := newEnvironment(t, forgejo, quotaServer(upQuota()))
	result, err := e.invoke(t, quotaCheck, "org", `{"owner":"orgs/acme","subject":"size:assets:packages:all"}`)
	if err != nil || result["allowed"] != true || result["subject"] != "size:assets:packages:all" ||
		result["owner"] != "orgs/acme" {
		t.Fatalf("result %v, %v", result, err)
	}
	if got := *e.calls; len(got) != 1 || got[0].path != "/api/v1/orgs/acme/quota/check" ||
		got[0].query != "subject=size%3Aassets%3Apackages%3Aall" {
		t.Errorf("calls = %v", got)
	}
	for _, bad := range []string{`{}`, `{"subject":"none"}`, `{"subject":"size:everything"}`, `{"subject":""}`} {
		e := newEnvironment(t, forgejo, quotaServer(upQuota()))
		_, err := e.invoke(t, quotaCheck, "open", bad)
		refused(t, e, bad, err)
	}
	if len(quotaSubjects) != 13 {
		t.Errorf("subjects = %v", quotaSubjects)
	}
}

func TestSwitchedOffQuotaIsNotAvailableButAnUnreachableOneIsNotFound(t *testing.T) {
	for _, tl := range []*tool{quotaGet, quotaListArtifacts, quotaListAttachments, quotaListPackages, quotaCheck} {
		for _, c := range []struct {
			connection, args string
			state            quotaState
			unavailable      bool
			calls            int
		}{
			// The quota routes answer 404 but the base route of the scope answers: the quota is switched off.
			{"open", `{"subject":"size:all"}`, quotaState{baseUp: true, status: 200, login: "alice"}, true, 2},
			{"org", `{"owner":"orgs/acme","subject":"size:all"}`, quotaState{baseUp: true, status: 200}, true, 2},
			// The base route answers 404 as well: the token does not reach the scope.
			{"open", `{"subject":"size:all"}`, quotaState{status: 200}, false, 2},
			{"org", `{"owner":"orgs/acme","subject":"size:all"}`, quotaState{status: 200}, false, 2},
			// A 403 is an unreachable quota without any further request.
			{"org", `{"owner":"orgs/acme","subject":"size:all"}`, quotaState{on: true, status: 403}, false, 1},
			{"open", `{"subject":"size:all"}`, quotaState{on: true, status: 403}, false, 1},
		} {
			args := argumentsFor(tl, c.args)
			e := newEnvironment(t, forgejo, quotaServer(&c.state))
			_, err := e.invoke(t, tl, c.connection, args)
			var failure *provider.Error
			if err == nil || !errors.As(err, &failure) {
				t.Fatalf("%s %s: err %v", tl.action, c.args, err)
			}
			wantClass := provider.ClassNotFound
			if c.unavailable {
				wantClass = provider.ClassProviderError
				if !strings.Contains(failure.Message, "not available") {
					t.Errorf("%s: message %q", tl.action, failure.Message)
				}
			}
			if failure.Class != wantClass || len(*e.calls) != c.calls {
				t.Errorf("%s %s: class %s, calls %v", tl.action, c.args, failure.Class, methods(e))
			}
			if c.unavailable {
				base := "/api/v1/user"
				if strings.Contains(c.args, "owner") {
					base = "/api/v1/orgs/acme"
				}
				if last := (*e.calls)[len(*e.calls)-1]; last.path != base || last.method != http.MethodGet {
					t.Errorf("%s probes %s %s", tl.action, last.method, last.path)
				}
			}
		}
	}
}

func TestQuotaToolsAreOnlyAdvertisedByForgejoAndRead(t *testing.T) {
	reg := registry(t)
	for _, tl := range []*tool{quotaGet, quotaListArtifacts, quotaListAttachments, quotaListPackages, quotaCheck} {
		if _, ok := tl.since[gitea.id]; ok {
			t.Errorf("%s is registered for Gitea", tl.action)
		}
		if _, _, ok := reg.Lookup(forgejo.toolID(tl)); !ok {
			t.Errorf("%s is missing", tl.action)
		}
		if d := tl.descriptor(forgejo); d.Risk.Effect != "read" || d.Risk.DataSensitivity != "forgejo-quota-data" {
			t.Errorf("%s risk = %+v", tl.action, d.Risk)
		}
	}
}

func TestExclusionsKeepExcludedRowsAndScopesOutOfTheQuota(t *testing.T) {
	// Rows of excluded repositories are dropped, and a row that names none is, since the connection restricts.
	for _, c := range []struct {
		tl         *tool
		key        string
		connection string
		want       []string
	}{
		{quotaListArtifacts, "artifacts", "exclude", []string{"a1"}},
		{quotaListArtifacts, "artifacts", "owner-exclude", []string{"a1"}},
		{quotaListAttachments, "attachments", "exclude", []string{"f1"}},
		{quotaListPackages, "packages", "exclude", []string{"p1"}},
	} {
		state := upQuota()
		state.login = "acme"
		e := newEnvironment(t, forgejo, quotaServer(state))
		args := `{"owner":"orgs/acme"}`
		if c.connection == "owner-exclude" {
			args = `{}`
		}
		result, err := e.invoke(t, c.tl, c.connection, args)
		if err != nil {
			t.Fatalf("%s %s on %s: %v", c.tl.action, args, c.connection, err)
		}
		var got []string
		for _, row := range rowsOf(t, result, c.key) {
			got = append(got, row["name"].(string))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s on %s = %v, want %v", c.tl.action, c.connection, got, c.want)
		}
	}
	// The quota of an account or organization that an exclusion takes out stays readable, since it is a setting
	// of the owner; exclusions alone prove nothing about the account.
	e := newEnvironment(t, forgejo, quotaServer(upQuota()))
	if _, err := e.invoke(t, quotaGet, "exclude", `{}`); err != nil ||
		!slices.Equal(methods(e), []string{"GET /api/v1/user/quota"}) {
		t.Errorf("account: %v, %v", err, methods(e))
	}
	excluded := upQuota()
	excluded.login = "Other"
	e = newEnvironment(t, forgejo, quotaServer(excluded))
	if _, err := e.invoke(t, quotaGet, "exclude", `{}`); err != nil {
		t.Errorf("quota of an excluded account: %v", err)
	}
	for _, tl := range []*tool{quotaGet, quotaListArtifacts, quotaListAttachments, quotaCheck} {
		e := newEnvironment(t, forgejo, quotaServer(upQuota()))
		if _, err := e.invoke(t, tl, "exclude", argumentsFor(tl, `{"owner":"orgs/OTHER","subject":"size:all"}`)); err != nil {
			t.Errorf("%s of an excluded organization: %v", tl.action, err)
		}
	}
	// What an owner holds, its packages, is out with the owner; the refusal comes before any request for an
	// organization and after the proof for the account.
	e = newEnvironment(t, forgejo, quotaServer(upQuota()))
	_, err := e.invoke(t, quotaListPackages, "exclude", `{"owner":"orgs/OTHER"}`)
	refused(t, e, "list-packages", err)
	e = newEnvironment(t, forgejo, quotaServer(excluded))
	if _, err := e.invoke(t, quotaListPackages, "exclude", `{}`); !isInvalidRequest(err) || len(*e.calls) != 1 ||
		strings.Contains(err.Error(), "Other") {
		t.Errorf("packages of an excluded account: %v, %v", err, methods(e))
	}
	e = newEnvironment(t, forgejo, quotaServer(upQuota()))
	if _, err := e.invoke(t, quotaGet, "exclude", `{"owner":"orgs/acme"}`); err != nil {
		t.Errorf("organization that no exclusion names: %v", err)
	}
}
