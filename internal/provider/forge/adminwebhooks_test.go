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

var adminWebhookTools = []*tool{adminWebhooksList, adminWebhooksGet, adminWebhooksDelete}

func adminWebhookArguments(tl *tool) string {
	return map[*tool]string{adminWebhooksList: `{"limit":20}`, adminWebhooksGet: `{"hook_id":4}`,
		adminWebhooksDelete: `{"hook_id":4}`}[tl]
}

func TestAdminWebhooksRegisterWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminWebhookTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Fatalf("%s is not registered", p.toolID(tl))
			}
			isDelete := tl == adminWebhooksDelete
			if d.RequiresToolAllowList != isDelete || d.Risk.DataSensitivity != p.id+accessSensitivity ||
				(isDelete && (d.Risk.Effect != capability.EffectDelete || d.Risk.Confirmation != capability.ConfirmationRequired ||
					d.Risk.Idempotency != capability.IdempotencyIdempotent)) ||
				(!isDelete && d.Risk.Effect != capability.EffectRead) {
				t.Errorf("%s descriptor = %+v", d.ID, d)
			}
			for _, profile := range metadata.Profiles {
				if isDelete && slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
	}
}

func TestAdminWebhooksRefuseTargetsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		for _, tl := range adminWebhookTools {
			for _, connection := range []string{"admin-repo", "admin-org", "admin-user"} {
				reads := *e.reads
				_, err := e.call(t, tl, connection, adminWebhookArguments(tl), true)
				if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads ||
					(err != nil && strings.Contains(err.Error(), "alice")) {
					t.Errorf("%s %s on %s: %v", p.id, tl.action, connection, err)
				}
			}
		}
	}
}

func TestAdminWebhooksListProjectsWithoutSecretsAndPagesByHeader(t *testing.T) {
	body := `[` + hookFixture(4, "") + `,` + hookFixture(5, `,"extra":"x"`) + `]`
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, body, "X-Total-Count", "31")
		})
		result, err := e.call(t, adminWebhooksList, "admin-open", `{"limit":2}`, false)
		rows := rowsOf(t, result, "webhooks")
		c := apiCalls(e.environment)
		if err != nil || len(rows) != 2 || len(c) != 1 || c[0].method != "GET" || c[0].path != "/api/v1/admin/hooks" ||
			result["has_more"] != true || result["next_cursor"] == nil {
			t.Fatalf("%s: %v, %v, %+v", p.id, result, err, c)
		}
		noSecret(t, p.id+" list", result)
		cfg, _ := rows[0]["config"].(map[string]any)
		if rows[0]["id"] != float64(4) || cfg["channel"] != "#ops" || rows[0]["authorization_header"] != nil ||
			rows[0]["extra"] != nil || rows[1]["extra"] != nil || cfg["token"] != nil || cfg["secret"] != nil {
			t.Errorf("%s row = %v", p.id, rows[0])
		}
		if q := queryOf(t, c[0]); q.Get("limit") != "2" || q.Get("page") != "1" || q.Get("type") != "" {
			t.Errorf("query = %q", c[0].query)
		}
		last := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, body, "X-Total-Count", "2")
		})
		end, err := last.call(t, adminWebhooksList, "admin-open", `{"limit":2}`, false)
		if err != nil || end["has_more"] != false || end["next_cursor"] != nil {
			t.Errorf("%s last page: %v, %v", p.id, end, err)
		}
	}
}

func TestAdminWebhooksTypeFilterIsGiteaOnly(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(200, `[]`) })
		reads := *e.reads
		_, err := e.call(t, adminWebhooksList, "admin-open", `{"type":"system"}`, false)
		if p == forgejo {
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads {
				t.Errorf("forgejo type: %v, calls %d", err, len(*e.calls))
			}
			continue
		}
		c := apiCalls(e.environment)
		if err != nil || len(c) != 1 || queryOf(t, c[0]).Get("type") != "system" {
			t.Fatalf("gitea type: %v, %+v", err, c)
		}
		for _, bad := range []string{`{"type":"other"}`, `{"type":""}x`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminWebhooksList, "admin-open", bad, false); err == nil || len(*e.calls) != before {
				t.Errorf("gitea %s: %v", bad, err)
			}
		}
	}
}

func TestAdminWebhooksGetRefusesAnotherID(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(200, hookFixture(4, ""))
		})
		result, err := e.call(t, adminWebhooksGet, "admin-open", `{"hook_id":4}`, false)
		c := apiCalls(e.environment)
		if err != nil || result["id"] != float64(4) || len(c) != 1 || c[0].path != "/api/v1/admin/hooks/4" {
			t.Fatalf("%s: %v, %v, %+v", p.id, result, err, c)
		}
		noSecret(t, p.id+" get", result)
		if result["authorization_header"] != nil {
			t.Errorf("%s shows the authorization header", p.id)
		}
		_, err = e.call(t, adminWebhooksGet, "admin-open", `{"hook_id":9}`, false)
		if err == nil || !strings.Contains(err.Error(), "another webhook") {
			t.Errorf("%s another ID: %v", p.id, err)
		}
		for _, args := range []string{`{"hook_id":0}`, `{"hook_id":-1}`, `{}`, `{"hook_id":4,"path":"/x"}`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminWebhooksGet, "admin-open", args, false); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, args, err)
			}
		}
	}
}

func TestAdminWebhooksDeleteSendsOneRequestAndIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response { return jsonResponse(204, ``) })
		var confirm *application.ConfirmationRequiredError
		if _, err := e.call(t, adminWebhooksDelete, "admin-open", `{"hook_id":4}`, false); !errors.As(err, &confirm) ||
			len(*e.calls) != 0 {
			t.Errorf("unconfirmed: %v", err)
		}
		result, err := e.call(t, adminWebhooksDelete, "admin-open", `{"hook_id":4}`, true)
		c := apiCalls(e.environment)
		if err != nil || result["deleted"] != true || len(c) != 1 || c[0].method != "DELETE" ||
			c[0].path != "/api/v1/admin/hooks/4" || c[0].body != "" {
			t.Fatalf("%s delete: %v, %v, %+v", p.id, result, err, c)
		}
		g := newAdminEnvironment(t, p, defaultVersion(p), func(call) *http.Response {
			return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
		})
		_, err = g.call(t, adminWebhooksDelete, "admin-open", `{"hook_id":4}`, true)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
			len(apiCalls(g.environment)) != 1 {
			t.Errorf("%s after 500: %v", p.id, err)
		}
	}
}

func TestAdminWebhooksSourceWiresNoWrite(t *testing.T) {
	src, err := os.ReadFile("adminwebhooks.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"MethodPatch", "MethodPost", "MethodPut"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("adminwebhooks.go uses %s", banned)
		}
	}
}
