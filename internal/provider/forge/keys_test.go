package forge

import (
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	keyTextFixture     = "fixture-public-key-text"
	gpgTextFixture     = "fixture-gpg-armor-text"
	deployTokenFixture = "fixture-deploy-token-value"
	clientSecretValue  = "fixture-client-secret-value"
	tokenSha1Fixture   = "fixture-token-sha1-value"
	tokenTailFixture   = "fixture-tail"
	fingerprintValue   = "SHA256:fixture-fingerprint"
)

// mailFixture is assembled so that the literal does not read like a mail address.
var mailFixture = "person" + "@" + "example.invalid"

var neverShown = []string{keyTextFixture, gpgTextFixture, deployTokenFixture, clientSecretValue, tokenSha1Fixture,
	tokenTailFixture, secretFixture}

func noneShown(t *testing.T, label string, value any) {
	t.Helper()
	text := toJSON(t, value)
	for _, banned := range neverShown {
		if strings.Contains(text, banned) {
			t.Errorf("%s shows %q: %s", label, banned, text)
		}
	}
}

func publicKeyFixture(id int) string {
	n := strconv.Itoa(id)
	return `{"id":` + n + `,"title":"laptop","key":"` + keyTextFixture + `","key_type":"user","fingerprint":"` +
		fingerprintValue + `","read_only":false,"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-03T03:04:05Z",` +
		`"last_used_at":"2026-01-04T03:04:05Z","verified":true,"url":"https://forge.example.test/api/v1/user/keys/` + n +
		`","user":{"id":1,"login":"alice","email":"` + mailFixture + `"}}`
}

func deployKeyFixture(id int, url string) string {
	n := strconv.Itoa(id)
	return `{"id":` + n + `,"key_id":77,"title":"ci","key":"` + keyTextFixture + `","key_type":"ssh","token":"` +
		deployTokenFixture + `","fingerprint":"` + fingerprintValue + `","read_only":true,"created_at":"2026-01-02T03:04:05Z",` +
		`"url":"` + url + `","repository":{"id":9,"name":"widget","full_name":"acme/widget","private":true,` +
		`"owner":{"id":2,"login":"acme","email":"` + mailFixture + `"}}}`
}

func gpgKeyFixture(id int) string {
	n := strconv.Itoa(id)
	return `{"id":` + n + `,"key_id":"FIXTUREKEY` + n + `","primary_key_id":"","public_key":"` + gpgTextFixture + `",` +
		`"emails":[{"email":"` + mailFixture + `","verified":true}],"subkeys":[{"id":` + strconv.Itoa(id+100) +
		`,"key_id":"FIXTURESUB","public_key":"` + gpgTextFixture + `","can_sign":true,"can_encrypt_comms":false,` +
		`"can_encrypt_storage":false,"can_certify":false,"verified":true,"created_at":"2026-01-02T03:04:05Z",` +
		`"expires_at":"2030-01-02T03:04:05Z","emails":[{"email":"` + mailFixture + `","verified":true}]}],` +
		`"can_sign":true,"can_encrypt_comms":true,"can_encrypt_storage":true,"can_certify":true,"verified":true,` +
		`"created_at":"2026-01-02T03:04:05Z","expires_at":"2030-01-02T03:04:05Z"}`
}

func appFixture(id int, uris string) string {
	return `{"id":` + strconv.Itoa(id) + `,"name":"ci-bot","client_id":"fixture-client-id","client_secret":"` +
		clientSecretValue + `","confidential_client":true,"skip_secondary_authorization":true,"redirect_uris":` + uris +
		`,"created":"2026-01-02T03:04:05Z"}`
}

func tokenFixture(id int) string {
	return `{"id":` + strconv.Itoa(id) + `,"name":"deploy","scopes":["read:user","write:repository"],` +
		`"sha1":"` + tokenSha1Fixture + `","token_last_eight":"` + tokenTailFixture + `","created_at":"2026-01-02T03:04:05Z",` +
		`"last_used_at":"2026-01-03T03:04:05Z","repositories":[{"id":9,"name":"widget","full_name":"acme/widget",` +
		`"owner":"acme"}]}`
}

func currentTokenFixture() string {
	return `{"id":8,"name":"deploy","scopes":["read:user"],"created_at":"2026-01-02T03:04:05Z",` +
		`"last_used_at":"2026-01-03T03:04:05Z","sha1":"` + tokenSha1Fixture + `","token_last_eight":"` + tokenTailFixture +
		`","user":{"id":1,"login":"alice","email":"` + mailFixture + `"}}`
}

// keyServer answers the routes of the key, token, and application tools.
func keyServer(login string) func(call) *http.Response {
	return accountServer(login, func(c call) *http.Response {
		if c.method == http.MethodDelete {
			return jsonResponse(204, "")
		}
		path := strings.TrimPrefix(c.path, "/api/v1")
		switch {
		case path == "/user/keys" || path == "/users/bob/keys":
			return jsonResponse(200, `[`+publicKeyFixture(1)+`,`+publicKeyFixture(2)+`]`, "X-Total-Count", "2")
		case strings.HasPrefix(path, "/user/keys/"):
			return jsonResponse(200, publicKeyFixture(5))
		case path == "/user/gpg_keys" || path == "/users/bob/gpg_keys":
			return jsonResponse(200, `[`+gpgKeyFixture(1)+`]`)
		case strings.HasPrefix(path, "/user/gpg_keys/"):
			return jsonResponse(200, gpgKeyFixture(5))
		case path == "/repos/acme/widget/keys":
			return jsonResponse(200, `[`+deployKeyFixture(1, "https://forge.example.test/api/v1/repos/acme/widget/keys/1")+
				`,`+deployKeyFixture(2, "https://forge.example.test/k?token=x")+`]`)
		case strings.HasPrefix(path, "/repos/acme/widget/keys/"):
			return jsonResponse(200, deployKeyFixture(5, ""))
		case path == "/user/applications/oauth2":
			return jsonResponse(200, `[`+appFixture(1, `["https://app.example.invalid/cb","https://app.example.invalid/cb?x=1"]`)+`]`)
		case strings.HasPrefix(path, "/user/applications/oauth2/"):
			return jsonResponse(200, appFixture(5, `["https://app.example.invalid/cb"]`))
		case path == "/users/"+login+"/tokens":
			return jsonResponse(200, `[`+tokenFixture(3)+`]`, "Link", `<https://forge.example.test/next>; rel="next"`)
		case path == "/token":
			return jsonResponse(200, currentTokenFixture())
		}
		return notFoundResponse()
	})
}

func TestKeyToolsShowMetadataAndNeverTheKeyText(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, keyServer("alice"))
		for _, c := range []struct {
			tl         *tool
			connection string
			args, key  string
		}{
			{userKeysList, "access-open", `{}`, "keys"}, {userKeysList, "access-open", `{"username":"bob"}`, "keys"},
			{userGPGKeysList, "access-open", `{}`, "gpg_keys"}, {userGPGKeysList, "access-open", `{"username":"bob"}`, "gpg_keys"},
			{deployKeysList, "access-repo", `{}`, "deploy_keys"}, {oauth2AppsList, "access-open", `{}`, "oauth2_apps"},
		} {
			result, err := e.invoke(t, c.tl, c.connection, c.args)
			if err != nil {
				t.Fatalf("%s %s %s: %v", p.id, c.tl.group, c.args, err)
			}
			noneShown(t, p.id+" "+c.tl.group, result)
			if len(rowsOf(t, result, c.key)) == 0 {
				t.Errorf("%s %s shows no rows: %v", p.id, c.tl.group, result)
			}
		}
		for _, c := range []struct {
			tl         *tool
			connection string
			args, key  string
		}{
			{userKeysGet, "access-open", `{"id":5}`, "key"}, {userGPGKeysGet, "access-open", `{"id":5}`, "gpg_key"},
			{deployKeysGet, "access-repo", `{"id":5}`, "deploy_key"}, {oauth2AppsGet, "access-open", `{"id":5}`, "oauth2_app"},
		} {
			result, err := e.invoke(t, c.tl, c.connection, c.args)
			if err != nil {
				t.Fatalf("%s %s: %v", p.id, c.tl.group, err)
			}
			noneShown(t, p.id+" "+c.tl.group+" get", result)
			if _, ok := result[c.key].(map[string]any); !ok {
				t.Errorf("%s %s: %v", p.id, c.tl.group, result)
			}
		}

		*e.calls = nil
		result, _ := e.invoke(t, userKeysList, "access-open", `{"fingerprint":"`+fingerprintValue+`","limit":7}`)
		keys := rowsOf(t, result, "keys")
		if len(keys) != 2 || keys[0]["id"] != float64(1) || keys[0]["title"] != "laptop" ||
			keys[0]["fingerprint"] != fingerprintValue || keys[0]["key_type"] != "user" {
			t.Fatalf("%s keys = %v", p.id, keys)
		}
		if owner, _ := keys[0]["user"].(map[string]any); owner["login"] != "alice" || len(owner) != 2 {
			t.Errorf("%s: key owner = %v", p.id, owner)
		}
		for _, member := range []string{"key", "public_key"} {
			if _, ok := keys[0][member]; ok {
				t.Errorf("%s: member %s is shown", p.id, member)
			}
		}
		_, forgejoMember := keys[0]["verified"]
		_, giteaMember := keys[0]["last_used_at"]
		if forgejoMember != (p == forgejo) || giteaMember != (p == gitea) {
			t.Errorf("%s: version members = %t, %t", p.id, forgejoMember, giteaMember)
		}
		if q := queryOf(t, (*e.calls)[0]); q.Get("fingerprint") != fingerprintValue || q.Get("limit") != "7" || q.Get("page") != "1" {
			t.Errorf("%s query = %v", p.id, q)
		}

		result, _ = e.invoke(t, userGPGKeysGet, "access-open", `{"id":5}`)
		gpg, _ := result["gpg_key"].(map[string]any)
		subkeys, _ := gpg["subkeys"].([]any)
		emails, _ := gpg["emails"].([]any)
		if gpg["key_id"] != "FIXTUREKEY5" || gpg["can_sign"] != true || len(subkeys) != 1 || len(emails) != 1 {
			t.Errorf("%s gpg key = %v", p.id, gpg)
		}
		if _, ok := gpg["public_key"]; ok {
			t.Errorf("%s: the GPG key text is shown", p.id)
		}
		if sub, _ := subkeys[0].(map[string]any); sub["key_id"] != "FIXTURESUB" || sub["public_key"] != nil || sub["emails"] != nil {
			t.Errorf("%s subkey = %v", p.id, sub)
		}

		result, _ = e.invoke(t, deployKeysList, "access-repo", `{}`)
		deploy := rowsOf(t, result, "deploy_keys")
		if result["repository"] != "acme/widget" || len(deploy) != 2 || deploy[0]["key_id"] != float64(77) ||
			deploy[0]["read_only"] != true || deploy[0]["fingerprint"] != fingerprintValue {
			t.Fatalf("%s deploy keys = %v", p.id, result)
		}
		if repo, _ := deploy[0]["repository"].(map[string]any); repo["full_name"] != "acme/widget" || len(repo) != 3 {
			t.Errorf("%s: repository = %v", p.id, repo)
		}
		if _, ok := deploy[1]["url"]; ok {
			t.Errorf("%s: an address with a query is shown", p.id)
		}
		if _, ok := deploy[0]["key_type"]; ok != (p == gitea) {
			t.Errorf("%s: key_type present = %t", p.id, ok)
		}
	}
}

func TestOAuth2AppsShowNoSecretAndOnlyPlainRedirects(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, keyServer("alice"))
		result, err := e.invoke(t, oauth2AppsList, "access-open", `{}`)
		apps := rowsOf(t, result, "oauth2_apps")
		if err != nil || len(apps) != 1 {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		app := apps[0]
		if _, ok := app["client_secret"]; ok {
			t.Errorf("%s: the client secret is shown", p.id)
		}
		if uris, _ := app["redirect_uris"].([]any); !reflect.DeepEqual(uris, []any{"https://app.example.invalid/cb"}) {
			t.Errorf("%s redirect_uris = %v", p.id, app["redirect_uris"])
		}
		if app["client_id"] != "fixture-client-id" || app["confidential_client"] != true || app["name"] != "ci-bot" {
			t.Errorf("%s app = %v", p.id, app)
		}
		if _, ok := app["skip_secondary_authorization"]; ok != (p == gitea) {
			t.Errorf("%s: skip_secondary_authorization present = %t", p.id, ok)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/user/applications/oauth2"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

func TestAccessTokenToolsAreProductSpecificAndShowNoValue(t *testing.T) {
	reg := registry(t)
	for _, only := range []struct {
		tl   *tool
		for_ *product
	}{{accessTokensList, forgejo}, {accessTokensGetCurrent, gitea}, {accessTokensDeleteCurrent, gitea}} {
		for _, p := range products {
			_, _, ok := reg.Lookup(p.toolID(only.tl))
			if ok != (p == only.for_) {
				t.Errorf("%s registered = %t", p.toolID(only.tl), ok)
			}
		}
	}
	// Neither the Gitea list nor the Forgejo deletion of a listed token is offered: no tool wires a token path
	// below a user name for them.
	for _, id := range []string{"gitea.access-tokens.list", "forgejo.access-tokens.delete", "gitea.access-tokens.delete",
		"forgejo.access-tokens.create", "gitea.access-tokens.create"} {
		if _, _, ok := reg.Lookup(id); ok {
			t.Errorf("%s is registered", id)
		}
	}

	e := newAccessEnvironment(t, forgejo, keyServer("alice"))
	result, err := e.invoke(t, accessTokensList, "access-open", `{"limit":5}`)
	tokens := rowsOf(t, result, "access_tokens")
	if err != nil || len(tokens) != 1 || result["username"] != "alice" || result["has_more"] != true || result["next_cursor"] == nil {
		t.Fatalf("list: %v, %v", result, err)
	}
	noneShown(t, "forgejo token list", result)
	for _, banned := range []string{"sha1", "token_last_eight"} {
		if _, ok := tokens[0][banned]; ok {
			t.Errorf("member %s is shown", banned)
		}
	}
	if scopes, _ := tokens[0]["scopes"].([]any); len(scopes) != 2 || tokens[0]["name"] != "deploy" || tokens[0]["id"] != float64(3) {
		t.Errorf("token = %v", tokens[0])
	}
	if _, ok := tokens[0]["last_used_at"]; ok {
		t.Errorf("Forgejo shows last_used_at")
	}
	if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/user", "GET /api/v1/users/alice/tokens"}) {
		t.Errorf("calls without targets = %v", got)
	}
	// The next batch asks for page 2 of the same list.
	*e.calls = nil
	if _, err := e.invoke(t, accessTokensList, "access-open", `{"limit":5,"cursor":"`+result["next_cursor"].(string)+`"}`); err != nil ||
		queryOf(t, (*e.calls)[1]).Get("page") != "2" {
		t.Errorf("continuation: %v, %v", err, methods(e))
	}

	g := newAccessEnvironment(t, gitea, keyServer("alice"))
	current, err := g.invoke(t, accessTokensGetCurrent, "access-owner", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	noneShown(t, "gitea current token", current)
	token, _ := current["access_token"].(map[string]any)
	if token["id"] != float64(8) || token["name"] != "deploy" || token["last_used_at"] == nil {
		t.Errorf("current token = %v", token)
	}
	if user, _ := token["user"].(map[string]any); user["login"] != "alice" || len(user) != 2 {
		t.Errorf("token user = %v", token["user"])
	}
	if got := methods(g); !reflect.DeepEqual(got, []string{"GET /api/v1/user", "GET /api/v1/token"}) {
		t.Errorf("gitea calls = %v", got)
	}
}

func TestAccountLevelKeyToolsProveTheAccountAndRefuseForeignOnes(t *testing.T) {
	type tcase struct {
		tl   *tool
		args string
		want string
	}
	cases := func(p *product) []tcase {
		list := []tcase{
			{userKeysList, `{}`, "GET /api/v1/user/keys"}, {userKeysGet, `{"id":5}`, "GET /api/v1/user/keys/5"},
			{userKeysDelete, `{"id":5}`, "DELETE /api/v1/user/keys/5"},
			{userGPGKeysList, `{}`, "GET /api/v1/user/gpg_keys"}, {userGPGKeysGet, `{"id":5}`, "GET /api/v1/user/gpg_keys/5"},
			{userGPGKeysDelete, `{"id":5}`, "DELETE /api/v1/user/gpg_keys/5"},
			{oauth2AppsList, `{}`, "GET /api/v1/user/applications/oauth2"},
			{oauth2AppsGet, `{"id":5}`, "GET /api/v1/user/applications/oauth2/5"},
			{oauth2AppsDelete, `{"id":5}`, "DELETE /api/v1/user/applications/oauth2/5"},
		}
		if p == forgejo {
			list = append(list, tcase{accessTokensList, `{}`, "GET /api/v1/users/alice/tokens"})
		} else {
			list = append(list, tcase{accessTokensGetCurrent, `{}`, "GET /api/v1/token"},
				tcase{accessTokensDeleteCurrent, `{}`, "DELETE /api/v1/token"})
		}
		return list
	}
	for _, p := range products {
		login := "alice"
		e := newAccessEnvironment(t, p, func(c call) *http.Response { return keyServer(login)(c) })
		for _, c := range cases(p) {
			label := p.id + " " + c.tl.group + "." + c.tl.action
			*e.calls = nil
			if _, err := e.invoke(t, c.tl, "access-owner", c.args); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if got := methods(e); len(got) != 2 || got[0] != "GET /api/v1/user" || got[1] != c.want {
				t.Errorf("%s calls = %v", label, got)
			}
			// Without targets there is nothing to prove, except where the login is needed for the path.
			*e.calls = nil
			if _, err := e.invoke(t, c.tl, "access-open", c.args); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if got := methods(e); got[len(got)-1] != c.want || (len(got) == 2) != (c.tl == accessTokensList) {
				t.Errorf("%s calls without targets = %v", label, got)
			}
			// Targets that name no user, or a repository, refuse before any request or token read.
			for _, connection := range []string{"access-repo", "access-org", "access-mixed"} {
				*e.calls, *e.reads = nil, 0
				_, err := e.invoke(t, c.tl, connection, c.args)
				refused(t, e, label+" on "+connection, err)
			}
			// The account behind the token is another one than the target names: only the proof is sent.
			login = "mallory"
			*e.calls = nil
			if _, err := e.invoke(t, c.tl, "access-owner", c.args); err == nil || !reflect.DeepEqual(methods(e), []string{"GET /api/v1/user"}) {
				t.Errorf("%s: foreign account: %v, %v", label, err, methods(e))
			}
			login = "alice"
		}
		// Another person's public keys reach past any target.
		for _, tl := range []*tool{userKeysList, userGPGKeysList} {
			for _, connection := range []string{"access-owner", "access-repo", "access-org", "access-mixed"} {
				*e.calls, *e.reads = nil, 0
				_, err := e.invoke(t, tl, connection, `{"username":"bob"}`)
				refused(t, e, tl.group+" of another person on "+connection, err)
			}
			*e.calls = nil
			result, err := e.invoke(t, tl, "access-open", `{"username":"bob"}`)
			if err != nil || result["username"] != "bob" || !reflect.DeepEqual(methods(e), []string{
				"GET /api/v1/users/bob/" + strings.TrimPrefix(strings.ReplaceAll(tl.group, "-", "_"), "user_")}) {
				t.Errorf("%s %s of another person: %v, %v, %v", p.id, tl.group, result, err, methods(e))
			}
		}
		for _, bad := range []string{`{"username":"../x"}`, `{"username":"a b"}`, `{"username":""}`} {
			*e.calls, *e.reads = nil, 0
			if _, err := e.invoke(t, userKeysList, "access-open", bad); err == nil && bad != `{"username":""}` {
				t.Errorf("%s: %s accepted", p.id, bad)
			}
		}
	}
}

func TestDeployKeysFollowTheRepositoryTargets(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, keyServer("alice"))
		for _, c := range []struct {
			tl         *tool
			connection string
			args       string
		}{
			{deployKeysList, "access-repo", `{"repository":"acme/other"}`}, {deployKeysGet, "access-repo", `{"repository":"acme/other","id":5}`},
			{deployKeysDelete, "access-repo", `{"repository":"acme/other","id":5}`},
			{deployKeysList, "access-org", `{"repository":"other/widget"}`}, {deployKeysList, "access-open", `{}`},
			{deployKeysList, "access-owner", `{"repository":"acme/widget"}`}, {deployKeysGet, "access-open", `{"id":5}`},
			{deployKeysGet, "access-repo", `{"id":0}`}, {deployKeysGet, "access-repo", `{"id":-3}`},
			{deployKeysDelete, "access-repo", `{"id":0}`}, {deployKeysList, "access-repo", `{"fingerprint":"a\nb"}`},
		} {
			*e.calls, *e.reads = nil, 0
			_, err := e.invoke(t, c.tl, c.connection, c.args)
			refused(t, e, c.tl.group+"."+c.tl.action+" "+c.args, err)
		}
		// The one repository of the targets is the default; no account proof is read.
		if _, err := e.invoke(t, deployKeysGet, "access-repo", `{"id":5}`); err != nil ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/repos/acme/widget/keys/5"}) {
			t.Errorf("%s: %v, %v", p.id, err, methods(e))
		}
		*e.calls = nil
		if _, err := e.invoke(t, deployKeysDelete, "access-repo", `{"id":5}`); err != nil ||
			!reflect.DeepEqual(methods(e), []string{"DELETE /api/v1/repos/acme/widget/keys/5"}) {
			t.Errorf("%s: %v, %v", p.id, err, methods(e))
		}
	}
}

func TestKeyReadsRefuseAnAnswerForAnotherEntry(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, keyServer("alice"))
		for _, c := range []struct {
			tl         *tool
			connection string
		}{{userKeysGet, "access-open"}, {userGPGKeysGet, "access-open"}, {deployKeysGet, "access-repo"},
			{oauth2AppsGet, "access-open"}} {
			// The fixtures answer id 5.
			if _, err := e.invoke(t, c.tl, c.connection, `{"id":6}`); err == nil || classOfError(err) != provider.ClassInvalidResponse {
				t.Errorf("%s %s: another entry accepted: %v", p.id, c.tl.group, err)
			}
		}
	}
}

func TestKeyListsPageThroughTheirOwnCursors(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, accountServer("alice", func(c call) *http.Response {
			return jsonResponse(200, `[`+publicKeyFixture(1)+`]`, "Link", `<https://forge.example.test/next>; rel="next"`)
		}))
		first, err := e.invoke(t, userKeysList, "access-open", `{"limit":1}`)
		cursor, _ := first["next_cursor"].(string)
		if err != nil || first["has_more"] != true || cursor == "" {
			t.Fatalf("%s: %v, %v", p.id, first, err)
		}
		*e.calls = nil
		if _, err := e.invoke(t, userKeysList, "access-open", `{"cursor":"`+cursor+`"}`); err != nil ||
			queryOf(t, (*e.calls)[0]).Get("page") != "2" || queryOf(t, (*e.calls)[0]).Get("limit") != "1" {
			t.Errorf("%s: continuation: %v, %v", p.id, err, *e.calls)
		}
		// A cursor of another list or another filter is refused before any request.
		for _, c := range []struct {
			tl   *tool
			args string
		}{{userGPGKeysList, `{"cursor":"` + cursor + `"}`}, {oauth2AppsList, `{"cursor":"` + cursor + `"}`},
			{userKeysList, `{"fingerprint":"x","cursor":"` + cursor + `"}`}, {userKeysList, `{"username":"bob","cursor":"` + cursor + `"}`}} {
			*e.calls, *e.reads = nil, 0
			_, err := e.invoke(t, c.tl, "access-open", c.args)
			refused(t, e, c.tl.group+" with a foreign cursor", err)
		}
	}
}

func TestKeyTokenAndApplicationDeletesAreListedOnlyConfirmedAndSingle(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, accountServer("alice", func(c call) *http.Response {
			return jsonResponse(503, `{"message":"`+bodyCanary+`"}`)
		}))
		type tcase struct {
			tl   *tool
			args string
			want string
		}
		list := []tcase{
			{deployKeysDelete, `{"repository":"acme/widget","id":5}`, "DELETE /api/v1/repos/acme/widget/keys/5"},
			{userKeysDelete, `{"id":5}`, "DELETE /api/v1/user/keys/5"},
			{userGPGKeysDelete, `{"id":5}`, "DELETE /api/v1/user/gpg_keys/5"},
			{oauth2AppsDelete, `{"id":5}`, "DELETE /api/v1/user/applications/oauth2/5"},
		}
		if p == gitea {
			list = append(list, tcase{accessTokensDeleteCurrent, `{}`, "DELETE /api/v1/token"})
		}
		for _, c := range list {
			label := p.id + " " + c.tl.group + "." + c.tl.action
			// Without a tools list naming it, the tool is not offered.
			if _, err := e.invoke(t, c.tl, "repo", c.args); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s without a tools list: %v", label, err)
			}
			if _, err := e.invokeAs(t, c.tl, "access-open", c.args, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s unconfirmed: %v", label, err)
			}
			*e.calls = nil
			if _, err := e.invoke(t, c.tl, "access-open", c.args); err == nil {
				t.Fatalf("%s: a 5xx was accepted", label)
			}
			// One change request, never repeated after the unclear result, and without a body.
			if got := methods(e); !reflect.DeepEqual(got, []string{c.want}) {
				t.Errorf("%s calls = %v", label, got)
			}
			if (*e.calls)[0].body != "" {
				t.Errorf("%s sends a body %q", label, (*e.calls)[0].body)
			}
			*e.calls = nil
		}
		// The account level proves the account first and then sends its one change.
		if _, err := e.invoke(t, userKeysDelete, "access-owner", `{"id":5}`); err == nil ||
			!reflect.DeepEqual(methods(e), []string{"GET /api/v1/user", "DELETE /api/v1/user/keys/5"}) {
			t.Errorf("%s: %v, %v", p.id, err, methods(e))
		}
	}
}

func TestKeyTokenAndApplicationDeletesSucceed(t *testing.T) {
	for _, p := range products {
		e := newAccessEnvironment(t, p, keyServer("alice"))
		list := []struct {
			tl   *tool
			args string
		}{
			{deployKeysDelete, `{"repository":"acme/widget","id":5}`}, {userKeysDelete, `{"id":5}`},
			{userGPGKeysDelete, `{"id":5}`}, {oauth2AppsDelete, `{"id":5}`},
		}
		if p == gitea {
			list = append(list, struct {
				tl   *tool
				args string
			}{accessTokensDeleteCurrent, `{}`})
		}
		for _, c := range list {
			result, err := e.invoke(t, c.tl, "access-open", c.args)
			if err != nil || result["deleted"] != true {
				t.Errorf("%s %s: %v, %v", p.id, c.tl.group, result, err)
			}
		}
		if len(*e.calls) != len(list) {
			t.Errorf("%s calls = %v", p.id, methods(e))
		}
	}
}

// Every request of the key, token, and application tools, run through the transport, is one of the wired ones.
func TestKeyTokenAndApplicationToolsSendOnlyTheListedRequests(t *testing.T) {
	common := []string{"GET /api/v1/user", "GET /api/v1/user/keys", "GET /api/v1/user/keys/5", "DELETE /api/v1/user/keys/5",
		"GET /api/v1/users/bob/keys", "GET /api/v1/user/gpg_keys", "GET /api/v1/user/gpg_keys/5",
		"DELETE /api/v1/user/gpg_keys/5", "GET /api/v1/users/bob/gpg_keys", "GET /api/v1/repos/acme/widget/keys",
		"GET /api/v1/repos/acme/widget/keys/5", "DELETE /api/v1/repos/acme/widget/keys/5",
		"GET /api/v1/user/applications/oauth2", "GET /api/v1/user/applications/oauth2/5",
		"DELETE /api/v1/user/applications/oauth2/5"}
	runs := []struct {
		tl   *tool
		args string
	}{
		{userKeysList, `{}`}, {userKeysGet, `{"id":5}`}, {userKeysDelete, `{"id":5}`}, {userKeysList, `{"username":"bob"}`},
		{userGPGKeysList, `{}`}, {userGPGKeysGet, `{"id":5}`}, {userGPGKeysDelete, `{"id":5}`},
		{userGPGKeysList, `{"username":"bob"}`}, {deployKeysList, `{"repository":"acme/widget"}`},
		{deployKeysGet, `{"repository":"acme/widget","id":5}`}, {deployKeysDelete, `{"repository":"acme/widget","id":5}`},
		{oauth2AppsList, `{}`}, {oauth2AppsGet, `{"id":5}`}, {oauth2AppsDelete, `{"id":5}`},
	}
	perProduct := map[*product]struct {
		allowed []string
		extra   []struct {
			tl   *tool
			args string
		}
	}{
		forgejo: {[]string{"GET /api/v1/users/alice/tokens"}, []struct {
			tl   *tool
			args string
		}{{accessTokensList, `{}`}}},
		gitea: {[]string{"GET /api/v1/token", "DELETE /api/v1/token"}, []struct {
			tl   *tool
			args string
		}{{accessTokensGetCurrent, `{}`}, {accessTokensDeleteCurrent, `{}`}}},
	}
	for _, p := range products {
		e := newAccessEnvironment(t, p, keyServer("alice"))
		allowed := slices.Concat(common, perProduct[p].allowed)
		for _, run := range slices.Concat(runs, perProduct[p].extra) {
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
			if c.method == http.MethodPost || c.method == http.MethodPatch || c.method == http.MethodPut ||
				strings.Contains(c.path, "/admin/") || strings.Contains(c.path, "gpg_key_") || strings.HasSuffix(c.path, "/keys/tokens") {
				t.Errorf("%s sent %s %s", p.id, c.method, c.path)
			}
		}
	}
}

func TestAccountSettingsStayReachableWhenAnExclusionTakesTheAccountOut(t *testing.T) {
	for _, p := range products {
		// The account behind the token is the excluded owner "other"; its keys, tokens, and webhooks are settings of
		// the account and are neither proven against the exclusions nor refused by them.
		e := newAccessEnvironment(t, p, accountServer("Other", func(c call) *http.Response {
			if c.method == http.MethodGet && strings.Contains(c.path, "/hooks") {
				return jsonResponse(200, `[`+hookFixture(3, "")+`]`)
			}
			if c.method == http.MethodGet {
				return jsonResponse(200, `[`+publicKeyFixture(5)+`]`)
			}
			return jsonResponse(204, ``)
		}))
		for _, c := range []struct {
			tl        *tool
			arguments string
			want      string
		}{
			{userKeysList, `{}`, "GET /api/v1/user/keys"},
			{userKeysDelete, `{"id":5}`, "DELETE /api/v1/user/keys/5"},
			{userGPGKeysDelete, `{"id":5}`, "DELETE /api/v1/user/gpg_keys/5"},
			{webhooksList, `{"account":true}`, "GET /api/v1/user/hooks"},
			{webhooksList, `{"organization":"other"}`, "GET /api/v1/orgs/other/hooks"},
		} {
			*e.calls = nil
			if _, err := e.invoke(t, c.tl, "access-exclude", c.arguments); err != nil ||
				!reflect.DeepEqual(methods(e), []string{c.want}) {
				t.Errorf("%s %s.%s: %v, %v", p.id, c.tl.group, c.tl.action, err, methods(e))
			}
		}
		// A repository inside an exclusion stays refused before any request.
		*e.calls = nil
		if _, err := e.invoke(t, webhooksList, "access-exclude", `{"repository":"acme/gadget"}`); !isInvalidRequest(err) ||
			len(*e.calls) != 0 {
			t.Errorf("%s: webhooks of an excluded repository: %v", p.id, err)
		}
	}
}
