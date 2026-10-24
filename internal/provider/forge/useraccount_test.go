package forge

import (
	"encoding/base64"
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

const settingsFixture = `{"full_name":"Octo Cat","description":"Hi","location":"Nowhere","website":"https://octo.example.invalid",` +
	`"pronouns":"they/them","language":"en-US","theme":"auto","diff_view_style":"unified","hide_activity":false,` +
	`"hide_email":true,"hide_pronouns":false,"enable_repo_unit_hints":true}`

func accountServerFor(p *product) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.path == "/api/v1/user":
			return jsonResponse(200, `{"id":1,"login":"alice"}`)
		case c.path == "/api/v1/user/settings":
			return jsonResponse(200, settingsFixture)
		case c.method == http.MethodGet && c.path == "/api/v1/user/emails":
			return jsonResponse(200, `[{"email":"octo@example.invalid","primary":true,"verified":true,"user_id":1,"username":"alice"}]`)
		case c.method == http.MethodGet && (c.path == "/api/v1/user/blocks" || c.path == "/api/v1/user/list_blocked"):
			if p == forgejo {
				return jsonResponse(200, `[{"block_id":3,"created_at":"2030-01-01T00:00:00Z"}]`, "X-Total-Count", "1")
			}
			return jsonResponse(200, `[`+userFixture+`]`, "X-Total-Count", "1")
		case c.method == http.MethodGet && c.path == "/api/v1/user/blocks/octo":
			return jsonResponse(204, "")
		case c.method == http.MethodGet && strings.HasPrefix(c.path, "/api/v1/user/blocks/"):
			return notFoundResponse()
		case strings.HasPrefix(c.path, "/api/v1/user/"):
			return jsonResponse(204, "")
		}
		return notFoundResponse()
	}
}

func newAccountEnvironment(t *testing.T, p *product, handler func(call) *http.Response) (*environment, string) {
	t.Helper()
	dir := t.TempDir()
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	cfg := testConfig(p)
	listed := []string{p.id + ".user-emails.delete", p.id + ".user-emails.list"}
	cfg.Connections["mail-open"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects, Tools: listed}
	cfg.Connections["mail-owner"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"users/Alice"}, Tools: listed}
	cfg.Connections["mail-org"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme"}, Tools: listed}
	cfg.Connections["files"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Files: config.Files{Read: []string{dir}}}
	cfg.Connections["owner-files"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"users/Alice"}, Files: config.Files{Read: []string{dir}}}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}, dir
}

func TestUserAccountToolsRegisteredPerProduct(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		_, _, ok := reg.Lookup(p.toolID(userBlocksCheck))
		if ok != (p == gitea) {
			t.Errorf("%s: user-blocks.check registered = %t", p.id, ok)
		}
		for _, other := range reg.Provider(p.id) {
			if strings.HasPrefix(other.ID, p.id+".user-emails.") && other.ID != p.toolID(userEmailsList) &&
				other.ID != p.toolID(userEmailsDelete) {
				t.Errorf("unexpected email tool %s", other.ID)
			}
		}
	}
}

func TestUserEmailsDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		d, _, ok := reg.Lookup(p.toolID(userEmailsDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" || d.Risk.Effect != "delete" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			if slices.Contains(profile.Tools, d.ID) {
				t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
			}
		}
		e, _ := newAccountEnvironment(t, p, accountServerFor(p))
		if _, err := e.invoke(t, userEmailsDelete, "open", `{"emails":["octo@example.invalid"]}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, userEmailsDelete, "mail-open", `{"emails":["octo@example.invalid"]}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed: %v", p.id, err)
		}
		for _, bad := range []string{`{}`, `{"emails":[]}`, `{"emails":["nope"]}`, `{"emails":["a b@example.invalid"]}`} {
			_, err := e.invoke(t, userEmailsDelete, "mail-open", bad)
			refused(t, e, bad, err)
		}
		result, err := e.invoke(t, userEmailsDelete, "mail-open", `{"emails":["octo@example.invalid"]}`)
		body := bodyOf(t, (*e.calls)[0])
		if err != nil || result["deleted"] != true || len(*e.calls) != 1 || (*e.calls)[0].method != http.MethodDelete ||
			(*e.calls)[0].path != "/api/v1/user/emails" || !reflect.DeepEqual(body, map[string]any{"emails": []any{"octo@example.invalid"}}) {
			t.Errorf("%s: %v, %v, %v, %v", p.id, result, err, *e.calls, body)
		}
	}
}

func TestNoToolAddsEmailAddresses(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, d := range reg.Provider(p.id) {
			if strings.Contains(d.ID, "user-emails") && !strings.HasSuffix(d.ID, ".list") && !strings.HasSuffix(d.ID, ".delete") {
				t.Errorf("%s adds or changes emails", d.ID)
			}
		}
	}
	source, err := os.ReadFile("useraccount.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "http.MethodPost, \"/user/emails\"") {
		t.Error("POST /user/emails is wired")
	}
	for _, tl := range tools {
		if tl.group == "user-emails" && tl.action != "list" && tl.action != "delete" {
			t.Errorf("tool %s.%s", tl.group, tl.action)
		}
	}
}

func TestUserAccountToolsFollowTheAccountBinding(t *testing.T) {
	for _, p := range products {
		e, _ := newAccountEnvironment(t, p, accountServerFor(p))
		cases := []struct {
			tool *tool
			conn string
			args string
		}{
			{userSettingsGet, "", `{}`}, {userSettingsUpdate, "", `{"hide_email":true}`}, {userAvatarsDelete, "", `{}`},
			{userEmailsList, "", `{}`}, {userBlocksList, "", `{}`}, {userBlocksAdd, "", `{"username":"octo"}`},
			{userBlocksRemove, "", `{"username":"octo"}`},
		}
		for _, c := range cases {
			for _, connection := range []string{"org", "repo", "mixed", "two"} {
				_, err := e.invoke(t, c.tool, connection, c.args)
				refused(t, e, connection+" "+c.tool.group+"."+c.tool.action, err)
			}
		}
		for _, tl := range []*tool{userEmailsList, userEmailsDelete} {
			args := `{}`
			if tl == userEmailsDelete {
				args = `{"emails":["octo@example.invalid"]}`
			}
			_, err := e.invoke(t, tl, "mail-org", args)
			refused(t, e, "mail-org "+tl.action, err)
		}
		for _, c := range cases {
			e2, _ := newAccountEnvironment(t, p, accountServerFor(p))
			if _, err := e2.invoke(t, c.tool, "owner", c.args); err != nil {
				t.Errorf("%s %s: %v", p.id, c.tool.action, err)
				continue
			}
			if len(*e2.calls) < 2 || (*e2.calls)[0].path != "/api/v1/user" {
				t.Errorf("%s %s: no proof read first: %v", p.id, c.tool.action, e2.paths())
			}
			other := newEnvironment(t, p, accountServer("bob", accountServerFor(p)))
			if _, err := other.invoke(t, c.tool, "owner", c.args); !isInvalidRequest(err) || len(*other.calls) != 1 {
				t.Errorf("%s %s: foreign account: %v, %v", p.id, c.tool.action, err, other.paths())
			}
		}
		open := newEnvironment(t, p, accountServerFor(p))
		if _, err := open.invoke(t, userSettingsGet, "open", `{}`); err != nil || len(*open.calls) != 1 {
			t.Errorf("%s: open settings: %v, %v", p.id, err, open.paths())
		}
	}
}

func TestUserSettingsSendOnlyDocumentedFields(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, accountServerFor(p))
		result, err := e.invoke(t, userSettingsUpdate, "open", `{"full_name":"Octo","hide_email":false,"theme":"auto"}`)
		body := bodyOf(t, (*e.calls)[0])
		if err != nil || len(*e.calls) != 1 || (*e.calls)[0].method != http.MethodPatch ||
			(*e.calls)[0].path != "/api/v1/user/settings" ||
			!reflect.DeepEqual(body, map[string]any{"full_name": "Octo", "hide_email": false, "theme": "auto"}) {
			t.Fatalf("%s: %v, %v, %v, %v", p.id, result, err, *e.calls, body)
		}
		got := jsonOf(result)
		if p == gitea && (strings.Contains(got, "pronouns") || strings.Contains(got, "enable_repo_unit_hints")) {
			t.Errorf("gitea shows forgejo settings: %s", got)
		}
		if p == forgejo && !strings.Contains(got, "enable_repo_unit_hints") {
			t.Errorf("forgejo misses settings: %s", got)
		}
		multiline := newEnvironment(t, p, accountServerFor(p))
		if _, err := multiline.invoke(t, userSettingsUpdate, "open", `{"description":"line one\nline two"}`); err != nil ||
			!reflect.DeepEqual(bodyOf(t, (*multiline.calls)[0]), map[string]any{"description": "line one\nline two"}) {
			t.Errorf("%s multi-line description: %v %v", p.id, err, *multiline.calls)
		}
		fresh := newEnvironment(t, p, accountServerFor(p))
		for _, bad := range []string{`{}`, `{"admin":true}`, `{"hide_email":"yes"}`, `{"theme":"a\nb"}`} {
			_, err := fresh.invoke(t, userSettingsUpdate, "open", bad)
			refused(t, fresh, bad, err)
		}
		_, err = fresh.invoke(t, userSettingsUpdate, "open", `{"pronouns":"x"}`)
		if p == gitea {
			refused(t, fresh, "gitea pronouns", err)
		}
		if p == forgejo {
			_, err = fresh.invoke(t, userSettingsUpdate, "open", `{"pronouns":"x","hide_pronouns":true,"enable_repo_unit_hints":false}`)
			if err != nil {
				t.Errorf("forgejo fields: %v", err)
			}
			body := bodyOf(t, (*fresh.calls)[len(*fresh.calls)-1])
			if !reflect.DeepEqual(body, map[string]any{"pronouns": "x", "hide_pronouns": true, "enable_repo_unit_hints": false}) {
				t.Errorf("forgejo body %v", body)
			}
		}
	}
}

func TestUserBlocksPathsPerProduct(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, accountServerFor(p))
		list, err := e.invoke(t, userBlocksList, "open", `{}`)
		blocked, _ := list["blocked"].([]any)
		if err != nil || len(blocked) != 1 || list["has_more"] != false {
			t.Fatalf("%s list: %v, %v", p.id, list, err)
		}
		entry := jsonOf(blocked[0])
		if p == forgejo && (!strings.Contains(entry, "block_id") || strings.Contains(entry, "login")) {
			t.Errorf("forgejo entry: %s", entry)
		}
		if p == gitea && !strings.Contains(entry, `"login":"octo"`) {
			t.Errorf("gitea entry: %s", entry)
		}
		add, err := e.invoke(t, userBlocksAdd, "open", `{"username":"octo"}`)
		remove, err2 := e.invoke(t, userBlocksRemove, "open", `{"username":"octo"}`)
		if err != nil || err2 != nil || add["blocked"] != true || remove["unblocked"] != true {
			t.Fatalf("%s: %v %v", p.id, err, err2)
		}
		want := []string{"GET /api/v1/user/blocks", "PUT /api/v1/user/blocks/octo", "DELETE /api/v1/user/blocks/octo"}
		if p == forgejo {
			want = []string{"GET /api/v1/user/list_blocked", "PUT /api/v1/user/block/octo", "PUT /api/v1/user/unblock/octo"}
		}
		if !reflect.DeepEqual(methods(e), want) {
			t.Errorf("%s calls = %v, want %v", p.id, methods(e), want)
		}
		note, err := e.invoke(t, userBlocksAdd, "open", `{"username":"octo","note":"spam"}`)
		last := (*e.calls)[len(*e.calls)-1]
		if p == gitea && (err != nil || note["blocked"] != true || last.query != "note=spam") {
			t.Errorf("gitea note: %v, %v, %q", note, err, last.query)
		}
		if p == forgejo {
			fresh := newEnvironment(t, p, accountServerFor(p))
			_, err := fresh.invoke(t, userBlocksAdd, "open", `{"username":"octo","note":"spam"}`)
			refused(t, fresh, "forgejo note", err)
		}
		for _, bad := range []string{`{}`, `{"username":"a/b"}`} {
			fresh := newEnvironment(t, p, accountServerFor(p))
			_, err := fresh.invoke(t, userBlocksAdd, "open", bad)
			refused(t, fresh, bad, err)
		}
	}
}

func TestUserBlocksCheckIsGiteaOnlyAndReadsStatus(t *testing.T) {
	e := newEnvironment(t, gitea, accountServerFor(gitea))
	yes, err := e.invoke(t, userBlocksCheck, "open", `{"username":"octo"}`)
	no, err2 := e.invoke(t, userBlocksCheck, "open", `{"username":"ghost"}`)
	if err != nil || err2 != nil || yes["blocked"] != true || no["blocked"] != false {
		t.Errorf("check: %v %v, %v %v", yes, err, no, err2)
	}
	failing := newEnvironment(t, gitea, func(call) *http.Response { return jsonResponse(500, `{"message":"`+bodyCanary+`"}`) })
	if _, err := failing.invoke(t, userBlocksCheck, "open", `{"username":"octo"}`); err == nil || len(*failing.calls) != 1 {
		t.Errorf("500: %v", err)
	}
}

func TestUserEmailsListAndAccountMutationsSendOneRequest(t *testing.T) {
	for _, p := range products {
		e, _ := newAccountEnvironment(t, p, accountServerFor(p))
		list, err := e.invoke(t, userEmailsList, "mail-open", `{}`)
		if emails, _ := list["emails"].([]any); err != nil || len(emails) != 1 || list["truncated"] != false ||
			!strings.Contains(jsonOf(list), "octo@example.invalid") {
			t.Errorf("%s: %v, %v", p.id, list, err)
		}
		for _, c := range []struct {
			tool *tool
			args string
		}{{userSettingsUpdate, `{"hide_email":true}`}, {userAvatarsDelete, `{}`}, {userBlocksAdd, `{"username":"octo"}`},
			{userBlocksRemove, `{"username":"octo"}`}, {userEmailsDelete, `{"emails":["octo@example.invalid"]}`}} {
			conn := "open"
			if c.tool == userEmailsDelete {
				conn = "mail-open"
			}
			if _, err := e.invokeAs(t, c.tool, conn, c.args, false); err == nil {
				t.Errorf("%s %s: unconfirmed", p.id, c.tool.action)
			}
			failing, _ := newAccountEnvironment(t, p, func(cl call) *http.Response {
				if cl.path == "/api/v1/user" {
					return jsonResponse(200, `{"id":1,"login":"alice"}`)
				}
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			})
			if _, err := failing.invoke(t, c.tool, conn, c.args); err == nil || len(*failing.calls) != 1 {
				t.Errorf("%s %s: 5xx: %v, %d calls", p.id, c.tool.action, err, len(*failing.calls))
			}
			bound, _ := newAccountEnvironment(t, p, accountServerFor(p))
			bconn := "owner"
			if c.tool == userEmailsDelete {
				bconn = "mail-owner"
			}
			if _, err := bound.invoke(t, c.tool, bconn, c.args); err != nil || len(*bound.calls) != 2 {
				t.Errorf("%s %s bound: %v, %v", p.id, c.tool.action, err, bound.paths())
			}
		}
	}
}

func TestUserAvatarsUpdate(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("avatar-bytes")...)
	for _, p := range products {
		e, dir := newAccountEnvironment(t, p, accountServerFor(p))
		image := filepath.Join(dir, "me.png")
		text := filepath.Join(dir, "notes.txt")
		if err := os.WriteFile(image, png, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(text, []byte("not an image"), 0o600); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "me.png")
		if err := os.WriteFile(outside, png, 0o600); err != nil {
			t.Fatal(err)
		}
		for name, path := range map[string]string{"outside": outside, "text": text, "missing": filepath.Join(dir, "none.png")} {
			_, err := e.invoke(t, userAvatarsUpdate, "files", `{"local_path":`+quote(path)+`}`)
			if err == nil || len(*e.calls) != 0 || *e.reads != 0 {
				t.Errorf("%s %s: %v, calls %d, token reads %d", p.id, name, err, len(*e.calls), *e.reads)
			}
		}
		if _, err := e.invoke(t, userAvatarsUpdate, "open", `{"local_path":`+quote(image)+`}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: no released directory: %v", p.id, err)
		}
		if _, err := e.invoke(t, userAvatarsUpdate, "org", `{"local_path":`+quote(image)+`}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: org target: %v", p.id, err)
		}
		result, err := e.invoke(t, userAvatarsUpdate, "owner-files", `{"local_path":`+quote(image)+`}`)
		if err != nil || result["updated"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		body := bodyOf(t, (*e.calls)[1])
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/user", "POST /api/v1/user/avatar"}) ||
			body["image"] != base64.StdEncoding.EncodeToString(png) || len(body) != 1 {
			t.Errorf("%s calls = %v, body %v", p.id, got, body)
		}
	}
}
