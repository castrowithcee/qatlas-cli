package forge

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const foreignOrg = "stranger"

func jsonOf(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func orgJSON(name string) string {
	return `{"id":3,"name":"` + name + `","username":"` + name + `","full_name":"Acme Inc","email":"org@example.invalid",` +
		`"avatar_url":"https://forge.example.test/avatars/3","description":"Widgets","website":"https://acme.example.invalid",` +
		`"location":"Nowhere","visibility":"private","repo_admin_change_team_access":true,"created":"2030-01-02T03:04:05Z"}`
}

// orgListedTools are the tools only a connection whose tools list names them offers.
func orgListedTools(p *product) []string { return []string{p.toolID(orgsRename), p.toolID(orgsDelete)} }

// newOrgEnvironment adds the connections these tests need: org-listed names the listed-only tools for one
// organization, open-listed does so without targets, org-files releases a directory for reading, orgs-two names
// two organizations.
func newOrgEnvironment(t *testing.T, p *product, handler func(call) *http.Response) (*environment, string) {
	t.Helper()
	dir := t.TempDir()
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	cfg := testConfig(p)
	cfg.Connections["org-listed"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme"}, Tools: orgListedTools(p)}
	cfg.Connections["open-listed"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Tools: orgListedTools(p)}
	cfg.Connections["org-files"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme"}, Files: config.Files{Read: []string{dir}}}
	cfg.Connections["orgs-two"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme", "orgs/beta"}}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}, dir
}

func orgServer(p *product, extra func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		if extra != nil {
			if response := extra(c); response != nil {
				return response
			}
		}
		switch {
		case c.method == http.MethodGet && strings.EqualFold(c.path, "/api/v1/orgs/acme"):
			return jsonResponse(200, orgJSON("acme"))
		case c.method == http.MethodPatch && c.path == "/api/v1/orgs/acme":
			return jsonResponse(200, orgJSON("acme"))
		case c.method == http.MethodPost && c.path == "/api/v1/orgs":
			return jsonResponse(201, orgJSON("acme"))
		case c.method == http.MethodDelete || (c.method == http.MethodPost && strings.HasSuffix(c.path, "/rename")) ||
			(c.method == http.MethodPost && strings.HasSuffix(c.path, "/avatar")):
			return jsonResponse(204, "")
		case c.path == "/api/v1/orgs/acme/activities/feeds":
			return jsonResponse(200, `[{"id":1,"op_type":"commit_repo","act_user":`+commitUser+`,"repo":`+
				metaRepo("acme/widget")+`},{"id":2,"op_type":"commit_repo","repo":`+metaRepo("stranger/place")+`},`+
				`{"id":3,"op_type":"star_repo"}]`, "X-Total-Count", "3")
		case c.path == "/api/v1/users/octo/orgs/acme/permissions":
			return jsonResponse(200, `{"is_owner":true,"is_admin":true,"can_write":true,"can_read":true,"can_create_repository":false}`)
		}
		return notFoundResponse()
	}
}

func TestOrgListsThatReachPastTheTargetsAreRefusedBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		for _, connection := range []string{"org", "repo", "owner", "orgs-two", "mixed"} {
			_, err := e.invoke(t, orgsList, connection, `{}`)
			refused(t, e, connection+" list", err)
			_, err = e.invoke(t, orgsListUser, connection, `{"username":"octo"}`)
			refused(t, e, connection+" list-user", err)
			if err != nil && (strings.Contains(err.Error(), "octo") || strings.Contains(err.Error(), "acme")) {
				t.Errorf("%s: the refusal quotes a name: %v", p.id, err)
			}
		}
		_, err := e.invoke(t, orgsListUser, "open", `{"username":"a/b"}`)
		refused(t, e, "bad username", err)
	}
}

func TestOrgListsPageWithoutTargets(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, func(c call) *http.Response {
			q, _ := url.ParseQuery(c.query)
			if c.path == "/api/v1/orgs" || c.path == "/api/v1/users/octo/orgs" {
				if q.Get("page") == "1" {
					return jsonResponse(200, `[`+orgJSON("acme")+`]`, "Link", `<https://forge.example.test/api/v1/orgs?page=2>; rel="next"`)
				}
				return jsonResponse(200, `[`+orgJSON("beta")+`]`)
			}
			return nil
		}))
		for _, c := range []struct {
			tool *tool
			args string
			path string
		}{{orgsList, `{"limit":1}`, "/api/v1/orgs"}, {orgsListUser, `{"username":"octo","limit":1}`, "/api/v1/users/octo/orgs"}} {
			*e.calls = nil
			first, err := e.invoke(t, c.tool, "open", c.args)
			rows, _ := first["organizations"].([]any)
			cursor, _ := first["next_cursor"].(string)
			if err != nil || len(rows) != 1 || first["has_more"] != true || cursor == "" {
				t.Fatalf("%s %s: %v, %v", p.id, c.path, first, err)
			}
			row := rows[0].(map[string]any)
			if row["name"] != "acme" || row["email"] != "org@example.invalid" || row["username"] != nil {
				t.Errorf("%s row = %v", p.id, row)
			}
			if _, has := row["created"]; has != (p == forgejo) {
				t.Errorf("%s: created present = %v", p.id, has)
			}
			second, err := e.invoke(t, c.tool, "open", strings.TrimSuffix(c.args, "}")+`,"cursor":"`+cursor+`"}`)
			rows, _ = second["organizations"].([]any)
			if err != nil || len(rows) != 1 || second["has_more"] != false || second["next_cursor"] != nil {
				t.Fatalf("%s second: %v, %v", p.id, second, err)
			}
			if q, _ := url.ParseQuery((*e.calls)[1].query); (*e.calls)[1].path != c.path || q.Get("page") != "2" || q.Get("limit") != "1" {
				t.Errorf("%s second request = %+v", p.id, (*e.calls)[1])
			}
		}
	}
}

func TestOrgListMineChecksEveryRowAgainstTheTargets(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, func(c call) *http.Response {
			if c.path == "/api/v1/user/orgs" {
				return jsonResponse(200, `[`+orgJSON("Acme")+`,`+orgJSON(foreignOrg)+`,{"id":9},`+orgJSON("beta")+`]`,
					"X-Total-Count", "4")
			}
			return nil
		}))
		result, err := e.invoke(t, orgsListMine, "org", `{}`)
		rows, _ := result["organizations"].([]any)
		if err != nil || len(rows) != 1 || rows[0].(map[string]any)["name"] != "Acme" || result["has_more"] != false {
			t.Fatalf("%s org: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, orgsListMine, "orgs-two", `{}`)
		if rows, _ = result["organizations"].([]any); err != nil || len(rows) != 2 {
			t.Errorf("%s orgs-two: %v, %v", p.id, result, err)
		}
		// A user target names no organization.
		result, err = e.invoke(t, orgsListMine, "owner", `{}`)
		if rows, _ = result["organizations"].([]any); err != nil || len(rows) != 0 {
			t.Errorf("%s owner: %v, %v", p.id, result, err)
		}
		result, err = e.invoke(t, orgsListMine, "open", `{}`)
		if rows, _ = result["organizations"].([]any); err != nil || len(rows) != 4 {
			t.Errorf("%s open: %v, %v", p.id, result, err)
		}
	}
}

func TestOrgToolsRefuseOrganizationsOutsideTheTargetsBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		e, dir := newOrgEnvironment(t, p, orgServer(p, nil))
		image := filepath.Join(dir, "logo.png")
		if err := os.WriteFile(image, append([]byte("\x89PNG\r\n\x1a\n"), 'x'), 0o600); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			tool *tool
			args string
		}{
			{orgsGet, `{"organization":"` + foreignOrg + `"}`},
			{orgsUpdate, `{"organization":"` + foreignOrg + `","description":"x"}`},
			{orgsRename, `{"organization":"` + foreignOrg + `","new_name":"acme"}`},
			{orgsDelete, `{"organization":"` + foreignOrg + `"}`},
			{orgAvatarsUpdate, `{"organization":"` + foreignOrg + `","local_path":` + quote(image) + `}`},
			{orgAvatarsDelete, `{"organization":"` + foreignOrg + `"}`},
			{orgActivitiesList, `{"organization":"` + foreignOrg + `"}`},
			{orgPermissionsGet, `{"organization":"` + foreignOrg + `","username":"octo"}`},
			{orgsCreate, `{"username":"` + foreignOrg + `"}`},
		}
		for _, c := range cases {
			connection := "org-files"
			switch c.tool {
			case orgsRename, orgsDelete:
				connection = "org-listed"
			}
			_, err := e.invoke(t, c.tool, connection, c.args)
			refused(t, e, c.tool.group+"."+c.tool.action, err)
			if err != nil && strings.Contains(err.Error(), foreignOrg) {
				t.Errorf("%s %s: the refusal quotes the name: %v", p.id, c.tool.action, err)
			}
		}
		// A user or repository target names no organization.
		for _, connection := range []string{"owner", "repo", "mixed"} {
			_, err := e.invoke(t, orgsGet, connection, `{"organization":"alice"}`)
			refused(t, e, connection+" get", err)
			_, err = e.invoke(t, orgsGet, connection, `{"organization":"acme"}`)
			refused(t, e, connection+" get acme", err)
		}
		_, err := e.invoke(t, orgsGet, "org", `{}`)
		refused(t, e, "missing organization", err)
		_, err = e.invoke(t, orgsGet, "org", `{"organization":"a/b"}`)
		refused(t, e, "bad organization", err)
	}
}

func TestOrgGetReadsEveryMemberAndShowsCreatedOnForgejoOnly(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		result, err := e.invoke(t, orgsGet, "org", `{"organization":"ACME"}`)
		if err != nil || result["name"] != "acme" || result["full_name"] != "Acme Inc" ||
			result["email"] != "org@example.invalid" || result["repo_admin_change_team_access"] != true ||
			result["visibility"] != "private" || result["username"] != nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if _, has := result["created"]; has != (p == forgejo) {
			t.Errorf("%s: created present = %v", p.id, has)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/orgs/ACME"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		// An answer for another organization is not shown.
		other, _ := newOrgEnvironment(t, p, func(call) *http.Response { return jsonResponse(200, orgJSON(foreignOrg)) })
		if result, err := other.invoke(t, orgsGet, "org", `{"organization":"acme"}`); err == nil || result != nil ||
			strings.Contains(err.Error(), foreignOrg) {
			t.Errorf("%s: another organization: %v, %v", p.id, result, err)
		}
		d := registry(t)
		descriptor, _, _ := d.Lookup(p.toolID(orgsGet))
		if has := strings.Contains(string(descriptor.OutputSchema), `"created"`); has != (p == forgejo) {
			t.Errorf("%s: created in the schema = %v", p.id, has)
		}
	}
}

func TestOrgCreateNeedsTheNameInTheTargets(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		args := `{"username":"acme","full_name":"Acme Inc","email":"org@example.invalid","description":"Line one\nLine two",` +
			`"website":"https://acme.example.invalid","location":"Nowhere","visibility":"private","repo_admin_change_team_access":true}`
		if _, err := e.invokeAs(t, orgsCreate, "org", args, false); err == nil || len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: unconfirmed create: %v", p.id, err)
		}
		for _, bad := range []string{`{"username":"` + foreignOrg + `"}`, `{}`, `{"username":"a/b"}`,
			`{"username":"acme","visibility":"secret"}`, `{"username":"acme","pronouns":"x"}`,
			`{"username":"acme","description":"a\u0000b"}`, `{"username":"acme","full_name":"a\nb"}`} {
			_, err := e.invoke(t, orgsCreate, "org", bad)
			refused(t, e, bad, err)
		}
		for _, connection := range []string{"owner", "repo"} {
			_, err := e.invoke(t, orgsCreate, connection, `{"username":"acme"}`)
			refused(t, e, connection, err)
		}
		result, err := e.invoke(t, orgsCreate, "org", args)
		if err != nil || result["name"] != "acme" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		body := bodyOf(t, (*e.calls)[0])
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST /api/v1/orgs"}) || body["username"] != "acme" ||
			body["visibility"] != "private" || body["description"] != "Line one\nLine two" || len(body) != 8 {
			t.Errorf("%s calls = %v, body %v", p.id, got, body)
		}
		// Without targets any name is created.
		open, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		if _, err := open.invoke(t, orgsCreate, "open", `{"username":"acme"}`); err != nil {
			t.Errorf("%s: open create: %v", p.id, err)
		}
		// An answer for another name is an open outcome.
		odd, _ := newOrgEnvironment(t, p, func(call) *http.Response { return jsonResponse(201, orgJSON(foreignOrg)) })
		if _, err := odd.invoke(t, orgsCreate, "org", `{"username":"acme"}`); err == nil ||
			!strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), foreignOrg) {
			t.Errorf("%s: odd answer: %v", p.id, err)
		}
	}
}

func TestOrgUpdateSendsOnlyTheGivenDocumentedSettings(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		if _, err := e.invokeAs(t, orgsUpdate, "org", `{"organization":"acme","description":"x"}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed update: %v", p.id, err)
		}
		// Arguments neither product documents, or the other product's, are refused before the token is read.
		for _, bad := range []string{`{"organization":"acme"}`, `{"organization":"acme","pronouns":"x"}`,
			`{"organization":"acme","name":"new"}`, `{"organization":"acme","avatar_url":"https://x.example.invalid"}`,
			`{"organization":"acme","visibility":"secret"}`, `{"organization":"acme","repo_admin_change_team_access":"yes"}`,
			`{"organization":"acme","description":"` + strings.Repeat("x", 2049) + `"}`} {
			_, err := e.invoke(t, orgsUpdate, "org", bad)
			refused(t, e, bad, err)
		}
		result, err := e.invoke(t, orgsUpdate, "org",
			`{"organization":"acme","email":"","description":"New","website":"https://acme.example.invalid","visibility":"limited"}`)
		if err != nil || result["name"] != "acme" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		body := bodyOf(t, (*e.calls)[0])
		if got := methods(e); !reflect.DeepEqual(got, []string{"PATCH /api/v1/orgs/acme"}) || body["email"] != "" ||
			body["description"] != "New" || body["visibility"] != "limited" || len(body) != 4 {
			t.Errorf("%s calls = %v, body %v", p.id, got, body)
		}
	}
}

func TestOrgRenameAndDeleteAreListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{orgsRename, orgsDelete} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Fatalf("%s descriptor = %+v", p.id, d)
			}
			metadata, _ := reg.ProviderMetadata(p.id)
			for _, profile := range metadata.Profiles {
				for _, id := range profile.Tools {
					if id == d.ID {
						t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
					}
				}
			}
		}
		e, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		for _, tl := range []*tool{orgsRename, orgsDelete} {
			if _, err := e.invoke(t, tl, "org", `{"organization":"acme","new_name":"beta"}`); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s without a tools list: %v", p.id, tl.action, err)
			}
		}
		if _, err := e.invokeAs(t, orgsDelete, "org-listed", `{"organization":"acme"}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, orgsDelete, "org-listed", `{"organization":"acme"}`)
		if err != nil || result["deleted"] != true || result["organization"] != "acme" {
			t.Fatalf("%s delete: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"DELETE /api/v1/orgs/acme"}) || (*e.calls)[0].body != "" {
			t.Errorf("%s delete calls = %v", p.id, got)
		}
	}
}

func TestOrgRenameShowsTheOrganizationOnlyUnderANameInTheTargets(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		for _, bad := range []string{`{"organization":"acme"}`, `{"organization":"acme","new_name":"a/b"}`,
			`{"organization":"acme","new_name":""}`} {
			_, err := e.invoke(t, orgsRename, "org-listed", bad)
			refused(t, e, bad, err)
		}
		hidden, err := e.invoke(t, orgsRename, "org-listed", `{"organization":"acme","new_name":"`+foreignOrg+`"}`)
		if err != nil || hidden["renamed"] != true || hidden["reachable"] != false || hidden["organization"] != nil ||
			strings.Contains(strings.ToLower(jsonOf(hidden)), foreignOrg) {
			t.Errorf("%s hidden: %v, %v", p.id, hidden, err)
		}
		if body := bodyOf(t, (*e.calls)[0]); (*e.calls)[0].path != "/api/v1/orgs/acme/rename" || body["new_name"] != foreignOrg ||
			len(body) != 1 {
			t.Errorf("%s rename request = %+v", p.id, (*e.calls)[0])
		}
		// Without targets every new name is reachable; a name inside the targets is shown.
		open, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		shown, err := open.invoke(t, orgsRename, "open-listed", `{"organization":"acme","new_name":"beta"}`)
		if err != nil || shown["reachable"] != true || shown["organization"] != "beta" {
			t.Errorf("%s open: %v, %v", p.id, shown, err)
		}
		in, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		same, err := in.invoke(t, orgsRename, "org-listed", `{"organization":"acme","new_name":"ACME"}`)
		if err != nil || same["reachable"] != true || same["organization"] != "ACME" {
			t.Errorf("%s same: %v, %v", p.id, same, err)
		}
	}
}

func TestOrgChangesAreNeverRepeatedAfterAServerError(t *testing.T) {
	for _, p := range products {
		for _, c := range []struct {
			tool       *tool
			connection string
			args       string
		}{
			{orgsCreate, "org", `{"username":"acme"}`},
			{orgsUpdate, "org", `{"organization":"acme","description":"x"}`},
			{orgsRename, "org-listed", `{"organization":"acme","new_name":"beta"}`},
			{orgsDelete, "org-listed", `{"organization":"acme"}`},
			{orgAvatarsDelete, "org", `{"organization":"acme"}`},
		} {
			e, _ := newOrgEnvironment(t, p, func(call) *http.Response {
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			})
			_, err := e.invoke(t, c.tool, c.connection, c.args)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
				t.Errorf("%s %s: %v, calls %v", p.id, c.tool.action, err, methods(e))
			}
		}
	}
}

func TestOrgAvatars(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("avatar-bytes")...)
	for _, p := range products {
		e, dir := newOrgEnvironment(t, p, orgServer(p, nil))
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
			_, err := e.invoke(t, orgAvatarsUpdate, "org-files", `{"organization":"acme","local_path":`+quote(path)+`}`)
			if err == nil || len(*e.calls) != 0 || *e.reads != 0 {
				t.Errorf("%s %s: %v, calls %d, token reads %d", p.id, name, err, len(*e.calls), *e.reads)
			}
		}
		if _, err := e.invoke(t, orgAvatarsUpdate, "org", `{"organization":"acme","local_path":`+quote(image)+`}`); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: upload without released directories: %v", p.id, err)
		}
		if _, err := e.invoke(t, orgAvatarsUpdate, "org-files", `{"organization":"acme","local_path":`+quote(image)+`,"image":"AAAA"}`); !isInvalidRequest(err) {
			t.Errorf("%s: raw image argument: %v", p.id, err)
		}
		result, err := e.invoke(t, orgAvatarsUpdate, "org-files", `{"organization":"acme","local_path":`+quote(image)+`}`)
		if err != nil || result["updated"] != true || result["organization"] != "acme" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		body := bodyOf(t, (*e.calls)[0])
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST /api/v1/orgs/acme/avatar"}) ||
			body["image"] != base64.StdEncoding.EncodeToString(png) || len(body) != 1 {
			t.Errorf("%s calls = %v, body %v", p.id, got, body)
		}
		*e.calls = nil
		deleted, err := e.invoke(t, orgAvatarsDelete, "org", `{"organization":"acme"}`)
		if err != nil || deleted["deleted"] != true || !reflect.DeepEqual(methods(e), []string{"DELETE /api/v1/orgs/acme/avatar"}) {
			t.Errorf("%s delete: %v, %v, %v", p.id, deleted, err, methods(e))
		}
	}
}

func TestOrgActivitiesFilterRowsAndTakeADate(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		for _, bad := range []string{`{"organization":"acme","date":"2030-13-45"}`, `{"organization":"acme","date":"yesterday"}`} {
			_, err := e.invoke(t, orgActivitiesList, "org", bad)
			refused(t, e, bad, err)
		}
		result, err := e.invoke(t, orgActivitiesList, "org", `{"organization":"acme","date":"2030-01-02","limit":5}`)
		rows, _ := result["activities"].([]any)
		if err != nil || len(rows) != 1 || result["organization"] != "acme" ||
			rows[0].(map[string]any)["act_user"].(map[string]any)["login"] != "alice" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if q, _ := url.ParseQuery((*e.calls)[0].query); q.Get("date") != "2030-01-02" || q.Get("limit") != "5" ||
			q.Get("page") != "1" || (*e.calls)[0].path != "/api/v1/orgs/acme/activities/feeds" {
			t.Errorf("%s request = %+v", p.id, (*e.calls)[0])
		}
		descriptor, _, _ := registry(t).Lookup(p.toolID(orgActivitiesList))
		if descriptor.Risk.DataSensitivity != p.id+activitySensitivity {
			t.Errorf("%s sensitivity = %s", p.id, descriptor.Risk.DataSensitivity)
		}
	}
}

func TestOrgPermissionsReadOneUserInOneOrganization(t *testing.T) {
	for _, p := range products {
		e, _ := newOrgEnvironment(t, p, orgServer(p, nil))
		_, err := e.invoke(t, orgPermissionsGet, "org", `{"organization":"acme","username":"a/b"}`)
		refused(t, e, "bad username", err)
		result, err := e.invoke(t, orgPermissionsGet, "org", `{"organization":"acme","username":"octo"}`)
		permissions, _ := result["permissions"].(map[string]any)
		if err != nil || result["organization"] != "acme" || result["username"] != "octo" ||
			permissions["is_owner"] != true || permissions["can_create_repository"] != false || len(permissions) != 5 {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/users/octo/orgs/acme/permissions"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

// The mass deletion of an organization's repositories is not offered: no organization tool sends a DELETE to a
// repositories path, and the source of the organization tools does not name one.
func TestOrganizationRepositoriesAreNeverMassDeleted(t *testing.T) {
	source, err := os.ReadFile("orgs.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "/repos") {
		t.Error("orgs.go names a repositories path")
	}
	for _, p := range products {
		e, dir := newOrgEnvironment(t, p, orgServer(p, nil))
		image := filepath.Join(dir, "logo.png")
		if err := os.WriteFile(image, append([]byte("\x89PNG\r\n\x1a\n"), 'x'), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			tool       *tool
			connection string
			args       string
		}{
			{orgsGet, "org-files", `{"organization":"acme"}`},
			{orgsUpdate, "org-files", `{"organization":"acme","description":"x"}`},
			{orgsRename, "org-listed", `{"organization":"acme","new_name":"beta"}`},
			{orgsDelete, "org-listed", `{"organization":"acme"}`},
			{orgAvatarsUpdate, "org-files", `{"organization":"acme","local_path":` + quote(image) + `}`},
			{orgAvatarsDelete, "org-files", `{"organization":"acme"}`},
			{orgActivitiesList, "org-files", `{"organization":"acme"}`},
			{orgPermissionsGet, "org-files", `{"organization":"acme","username":"octo"}`},
			{orgsCreate, "org-files", `{"username":"acme"}`},
			{orgsListMine, "org-files", `{}`},
			{orgsList, "open", `{}`},
			{orgsListUser, "open", `{"username":"octo"}`},
		} {
			if _, err := e.invoke(t, c.tool, c.connection, c.args); err != nil {
				t.Logf("%s %s: %v", p.id, c.tool.action, err)
			}
		}
		for _, c := range *e.calls {
			if strings.HasSuffix(c.path, "/repos") {
				t.Errorf("%s: request %s %s", p.id, c.method, c.path)
			}
		}
		if len(*e.calls) == 0 {
			t.Errorf("%s: no request was made", p.id)
		}
	}
}
