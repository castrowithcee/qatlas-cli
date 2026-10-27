package forge

import (
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

var adminCredentialTools = []*tool{adminUserKeysDelete, adminAccessTokensList, adminAccessTokensDelete}

const tokenCanary = "canary-token-value-0123456789abcdef"

func credentialArguments(tl *tool) string {
	return map[*tool]string{adminUserKeysDelete: `{"username":"octo","id":12}`,
		adminAccessTokensList:   `{"username":"octo"}`,
		adminAccessTokensDelete: `{"username":"octo","token_id":7}`}[tl]
}

func TestAdminCredentialsRegisterPerProductWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminCredentialTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if ok != (p == forgejo || tl == adminUserKeysDelete) {
				t.Errorf("%s registered = %t", p.toolID(tl), ok)
			}
			if !ok {
				continue
			}
			isDelete := tl != adminAccessTokensList
			if d.RequiresToolAllowList != isDelete || !strings.HasSuffix(d.Risk.DataSensitivity, adminCredentialsSensitivity) {
				t.Errorf("%s descriptor = %+v", d.ID, d)
			}
			if isDelete && (d.Risk.Effect != capability.EffectDelete || d.Risk.Confirmation != capability.ConfirmationRequired) {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			if !isDelete && d.Risk.Effect != capability.EffectRead {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
	}
}

func TestAdminCredentialsRefuseTargetsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tl := range adminCredentialTools {
			if _, _, ok := registry(t).Lookup(p.toolID(tl)); !ok {
				continue
			}
			if _, err := e.call(t, tl, "admin-targets", credentialArguments(tl), true); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s with targets: %v", p.toolID(tl), err)
			}
		}
	}
}

func TestAdminUserKeysDeleteSendsOneRequestAndIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		f := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		var confirm *application.ConfirmationRequiredError
		if _, err := f.call(t, adminUserKeysDelete, "admin-open", credentialArguments(adminUserKeysDelete), false); !errors.As(err, &confirm) ||
			len(*f.calls) != 0 {
			t.Errorf("%s unconfirmed: %v", p.id, err)
		}
		result, err := f.call(t, adminUserKeysDelete, "admin-open", credentialArguments(adminUserKeysDelete), true)
		c := apiCalls(f.environment)
		if err != nil || result["deleted"] != true || result["id"] != float64(12) || len(c) != 1 ||
			c[0].method != "DELETE" || c[0].path != "/api/v1/admin/users/octo/keys/12" || c[0].query != "" || c[0].body != "" {
			t.Fatalf("%s delete: %v, %v, %+v", p.id, result, err, c)
		}
		for _, arguments := range []string{`{"username":"octo"}`, `{"username":"octo","id":0}`, `{"username":"octo","id":-1}`,
			`{"username":"octo","id":"1"}`, `{"username":"a/b","id":1}`, `{"username":"..","id":1}`,
			`{"username":"octo","id":1,"sudo":"root"}`} {
			before := len(*f.calls)
			if _, err := f.call(t, adminUserKeysDelete, "admin-open", arguments, true); err == nil || len(*f.calls) != before {
				t.Errorf("%s delete %s: %v", p.id, arguments, err)
			}
		}
		g := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		_, err = g.call(t, adminUserKeysDelete, "admin-open", credentialArguments(adminUserKeysDelete), true)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
			len(apiCalls(g.environment)) != 1 {
			t.Errorf("%s after 500: %v", p.id, err)
		}
	}
}

func TestAdminAccessTokensNeedForgejo16(t *testing.T) {
	for _, tl := range []*tool{adminAccessTokensList, adminAccessTokensDelete} {
		e := newAdminEnvironment(t, forgejo, "15.0.10", func(call) *http.Response { return jsonResponse(204, ``) })
		_, err := e.call(t, tl, "admin-open", credentialArguments(tl), true)
		if err == nil || !strings.Contains(err.Error(), "not available") || !strings.Contains(err.Error(), "16") ||
			len(apiCalls(e.environment)) != 0 {
			t.Errorf("%s forgejo 15: %v, %v", tl.action, err, apiMethods(e.environment))
		}
	}
}

func TestAdminAccessTokensListShowsNoTokenValueAndPagesByHeader(t *testing.T) {
	body := `[{"id":7,"name":"ci","scopes":["read:user"],"sha1":"` + tokenCanary + `","token_last_eight":"` +
		tokenCanary[:8] + `","created_at":"2030-01-01T00:00:00Z"}]`
	e := newAdminEnvironment(t, forgejo, "16.0.6", func(call) *http.Response {
		return jsonResponse(200, body, "X-Total-Count", "40")
	})
	result, err := e.call(t, adminAccessTokensList, "admin-open", `{"username":"octo","limit":1}`, false)
	rows := rowsOf(t, result, "access_tokens")
	c := apiCalls(e.environment)
	if err != nil || len(rows) != 1 || rows[0]["name"] != "ci" || result["has_more"] != true ||
		result["username"] != "octo" || len(c) != 1 || c[0].method != "GET" || c[0].path != "/api/v1/admin/users/octo/tokens" {
		t.Fatalf("list: %v, %v, %+v", result, err, c)
	}
	if out := strings.Join([]string{toJSON(t, result)}, ""); strings.Contains(out, tokenCanary) ||
		strings.Contains(out, tokenCanary[:8]) {
		t.Errorf("a token value was shown: %s", out)
	}
	last := newAdminEnvironment(t, forgejo, "16.0.6", func(call) *http.Response { return jsonResponse(200, body, "X-Total-Count", "1") })
	result, err = last.call(t, adminAccessTokensList, "admin-open", `{"username":"octo","limit":5}`, false)
	if err != nil || result["has_more"] != false {
		t.Errorf("last page: %v, %v", result, err)
	}
	for _, arguments := range []string{`{}`, `{"username":"a/b"}`, `{"username":"octo","limit":0}`, `{"username":"octo","x":1}`} {
		before := len(*e.calls)
		if _, err := e.call(t, adminAccessTokensList, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
			t.Errorf("list %s: %v", arguments, err)
		}
	}
}

func TestAdminAccessTokensDeleteTakesOnlyTheNumericID(t *testing.T) {
	f := newAdminEnvironment(t, forgejo, "16.0.6", func(call) *http.Response { return jsonResponse(204, ``) })
	var confirm *application.ConfirmationRequiredError
	if _, err := f.call(t, adminAccessTokensDelete, "admin-open", credentialArguments(adminAccessTokensDelete), false); !errors.As(err, &confirm) ||
		len(*f.calls) != 0 {
		t.Errorf("unconfirmed: %v", err)
	}
	result, err := f.call(t, adminAccessTokensDelete, "admin-open", credentialArguments(adminAccessTokensDelete), true)
	c := apiCalls(f.environment)
	if err != nil || result["deleted"] != true || result["token_id"] != float64(7) || len(c) != 1 ||
		c[0].method != "DELETE" || c[0].path != "/api/v1/admin/users/octo/tokens/7" || c[0].body != "" {
		t.Fatalf("delete: %v, %v, %+v", result, err, c)
	}
	for _, arguments := range []string{`{"username":"octo"}`, `{"username":"octo","token_id":0}`,
		`{"username":"octo","token_id":"ci"}`, `{"username":"octo","token":"ci"}`, `{"username":"a/b","token_id":1}`,
		`{"username":"octo","token_id":1,"sudo":"root"}`} {
		before := len(*f.calls)
		if _, err := f.call(t, adminAccessTokensDelete, "admin-open", arguments, true); err == nil || len(*f.calls) != before {
			t.Errorf("delete %s: %v", arguments, err)
		}
	}
	g := newAdminEnvironment(t, forgejo, "16.0.6", func(call) *http.Response {
		return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
	})
	_, err = g.call(t, adminAccessTokensDelete, "admin-open", credentialArguments(adminAccessTokensDelete), true)
	if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
		len(apiCalls(g.environment)) != 1 {
		t.Errorf("after 500: %v", err)
	}
}

func TestAdminCredentialsNeverCreate(t *testing.T) {
	src, err := os.ReadFile("admincredentials.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "MethodPost") || strings.Contains(string(src), "MethodPut") ||
		strings.Contains(string(src), "MethodPatch") {
		t.Error("admincredentials.go wires a creating or changing method")
	}
}
