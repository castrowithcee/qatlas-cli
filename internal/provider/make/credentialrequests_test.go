package makeapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	credRequestID    = "123e4567-e89b-12d3-a456-426614174000"
	credLink         = "https://eu1.make.com/credential-requests/link-canary-5e1d"
	credEmailCanary  = "email-canary-3b7a@example.com"
	credSecretCanary = "secret-value-canary-8c2f"
)

type credEnv struct {
	*environment
	calls  []call
	bodies map[string]string
	team   int64
	list   string
	status int
}

func credRequestJSON(id string, team int64) string {
	return `{"id":"` + id + `","teamId":` + itoa64(team) + `,"organizationId":501,"name":"Req","description":"d",` +
		`"status":"pending","createdAt":"2026-01-01T00:00:00Z","token":"` + credSecretCanary + `",` +
		`"publicUri":"` + credLink + `","makeProvider":{"id":7,"name":"Pat","email":"` + credEmailCanary + `"}}`
}

func newCredEnv(t *testing.T, team int64) *credEnv {
	h := &credEnv{bodies: map[string]string{}, team: team}
	h.environment = newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		h.bodies[r.Method+" "+r.URL.Path] = string(body)
		if h.status != 0 && r.Method != http.MethodGet {
			return jsonResponse(h.status, `{"message":"`+foreignCanary+`"}`), nil
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, apiPath+"/users/"):
			parts := strings.Split(r.URL.Path, "/")
			if parts[len(parts)-3] == "99" {
				return jsonResponse(404, `{"message":"`+foreignCanary+`"}`), nil
			}
			return jsonResponse(200, `{"userTeamRole":{"userId":`+parts[len(parts)-3]+`,"teamId":`+
				parts[len(parts)-1]+`}}`), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/credential-requests/requests":
			if h.list != "" {
				return jsonResponse(200, h.list), nil
			}
			return jsonResponse(200, `{"requests":[`+credRequestJSON(credRequestID, ownTeam)+`,`+
				credRequestJSON("223e4567-e89b-12d3-a456-426614174000", foreignTeam)+`]}`), nil
		case r.Method == http.MethodGet:
			return jsonResponse(200, `{"request":`+credRequestJSON(credRequestID, h.team)+`}`), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/credential-requests/requests/v2":
			return jsonResponse(200, `{"request":`+credRequestJSON(credRequestID, ownTeam)+`,"publicUri":"`+credLink+`"}`), nil
		case r.Method == http.MethodDelete:
			return jsonResponse(200, `{"deleted":true}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	return h
}

func (h *credEnv) changing() []call {
	var out []call
	for _, c := range h.calls {
		if c.method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

const credCreateArgs = `{"name":"Shop","credentials":[{"app_name":"google-sheets","app_modules":["*"],` +
	`"app_version":2,"name_override":"Sheets"}],"provider_user_id":7}`

func TestCredentialRequestsReadsHideLinkAndEmail(t *testing.T) {
	h := newCredEnv(t, ownTeam)
	list, err := h.invoke(credentialRequestsList.ID, "open", `{"status":"pending","name":"Req"}`)
	if err != nil || !strings.Contains(list, `"count":1`) || strings.Contains(list, "223e4567") {
		t.Fatalf("list = %s, %v", list, err)
	}
	if h.calls[0].query.Get("teamId") != itoa64(ownTeam) || h.calls[0].query.Get("status") != "pending" {
		t.Fatalf("query = %+v", h.calls[0].query)
	}
	got, err := h.invoke(credentialRequestsGet.ID, "open", `{"request_id":"`+credRequestID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{list, got} {
		for _, leak := range []string{credLink, credEmailCanary, credSecretCanary, "publicUri", "token"} {
			if strings.Contains(out, leak) {
				t.Fatalf("output leaked %q: %s", leak, out)
			}
		}
	}
	if !strings.Contains(got, `"provider_id":7`) {
		t.Fatalf("get = %s", got)
	}
}

func TestCredentialRequestsListIsCapped(t *testing.T) {
	h := newCredEnv(t, ownTeam)
	var many []string
	for i := 0; i < maxCredentialRequestList+5; i++ {
		many = append(many, `{"id":"`+credRequestID+`","teamId":1001,"name":"`+strings.Repeat("n", 2000)+`"}`)
	}
	h.list = `{"requests":[` + strings.Join(many, ",") + `]}`
	out, err := h.invoke(credentialRequestsList.ID, "open", `{}`)
	if err != nil || !strings.Contains(out, `"truncated":true`) || len(out) > maxCredentialRequestList*(maxCredentialRequestText+200)+300 {
		t.Fatalf("len = %d, %v", len(out), err)
	}
}

func TestCredentialRequestsRefuseForeignTeamBeforeDetailOrChange(t *testing.T) {
	args := `{"request_id":"` + credRequestID + `"}`
	for _, c := range []struct {
		op, conn string
		confirm  bool
	}{{credentialRequestsGet.ID, "open", false}, {credentialRequestsDelete.ID, "credentialdelete", true}} {
		h := newCredEnv(t, foreignTeam)
		var err error
		if c.confirm {
			_, err = h.confirmed(c.op, c.conn, args)
		} else {
			_, err = h.invoke(c.op, c.conn, args)
		}
		if !isInvalidRequest(err) || len(h.changing()) != 0 || len(h.calls) != 1 ||
			strings.Contains(err.Error(), "2002") || strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("%s err = %v, calls = %+v", c.op, err, h.calls)
		}
	}
}

func TestCredentialRequestsCreateSendsBoundTeamAndReturnsLink(t *testing.T) {
	h := newCredEnv(t, ownTeam)
	out, err := h.confirmed(credentialRequestsCreate.ID, "open", credCreateArgs)
	if err != nil || !strings.Contains(out, credLink) || strings.Contains(out, credEmailCanary) ||
		strings.Contains(out, credSecretCanary) {
		t.Fatalf("out = %s, %v", out, err)
	}
	ch := h.changing()
	if len(ch) != 1 || ch[0].method != http.MethodPost || ch[0].path != apiPath+"/credential-requests/requests/v2" {
		t.Fatalf("changing = %+v", ch)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(h.bodies["POST "+apiPath+"/credential-requests/requests/v2"]), &body); err != nil ||
		body["teamId"] != float64(ownTeam) || body["name"] != "Shop" ||
		body["provider"].(map[string]any)["providerMakeUserId"] != float64(7) {
		t.Fatalf("body = %v, %v", body, err)
	}
	item := body["credentials"].([]any)[0].(map[string]any)
	if item["appName"] != "google-sheets" || item["appVersion"] != float64(2) || item["nameOverride"] != "Sheets" {
		t.Fatalf("item = %v", item)
	}
	if h.calls[0].path != apiPath+"/users/7/user-team-roles/"+itoa64(ownTeam) {
		t.Fatalf("team membership was not proven first: %+v", h.calls)
	}
}

func TestCredentialRequestsCreateRefusesBadInputBeforeAnyRequest(t *testing.T) {
	cred := `"credentials":[{"app_name":"slack","app_modules":["a"]}]`
	for _, args := range []string{
		`{"name":"","` + cred[1:] + `,"provider_user_id":7}`,
		`{"name":"a\nb",` + cred + `,"provider_user_id":7}`,
		`{"name":"n",` + cred + `}`,
		`{"name":"n",` + cred + `,"provider_user_id":0}`,
		`{"name":"n",` + cred + `,"new_user":{"name":"k","email":"k@example.com"}}`,
		`{"name":"n",` + cred + `,"provider_user_id":7,"new_user":{"name":"k","email":"k@example.com"}}`,
		`{"name":"n","credentials":[],"provider_user_id":7}`,
		`{"name":"n","credentials":[{"app_name":"Bad Name","app_modules":["a"]}],"provider_user_id":7}`,
		`{"name":"n","credentials":[{"app_name":"slack","app_modules":["../x"]}],"provider_user_id":7}`,
		`{"name":"n","credentials":[{"app_name":"slack","app_modules":[]}],"provider_user_id":7}`,
		`{"name":"n",` + cred + `,"provider_user_id":7,"teamId":5}`,
		`{"name":"n",` + cred + `,"provider_user_id":7,"secret":"` + credSecretCanary + `"}`,
		`{"name":"n","credentials":[{"app_name":"slack","app_modules":["a"],"token":"x"}],"provider_user_id":7}`,
		`{"name":"` + strings.Repeat("a", 256) + `",` + cred + `,"provider_user_id":7}`,
	} {
		h := newCredEnv(t, ownTeam)
		if _, err := h.confirmed(credentialRequestsCreate.ID, "open", args); err == nil || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s accepted or sent: err = %v, calls = %d", args, err, len(h.calls))
		}
	}
	h := newCredEnv(t, ownTeam)
	if _, err := h.confirmed(credentialRequestsCreate.ID, "open", `{"name":"n","credentials":[{"app_name":"slack",`+
		`"app_modules":["a"]}],"provider_user_id":99}`); !isInvalidRequest(err) || len(h.changing()) != 0 ||
		strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("non-member err = %v, calls = %+v", err, h.calls)
	}
	if _, err := h.invoke(credentialRequestsCreate.ID, "open", credCreateArgs); !isConfirmationRequired(err) {
		t.Fatalf("create without confirm err = %v", err)
	}
}

func TestCredentialRequestsRefuseScenarioAllowList(t *testing.T) {
	h := newCredEnv(t, ownTeam)
	for _, c := range []struct{ op, args string }{
		{credentialRequestsList.ID, `{}`}, {credentialRequestsGet.ID, `{"request_id":"` + credRequestID + `"}`},
		{credentialRequestsCreate.ID, credCreateArgs},
	} {
		if _, err := h.confirmed(c.op, "scenario", c.args); err == nil || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s on scenario allow-list: err = %v, calls = %d", c.op, err, len(h.calls))
		}
	}
}

func TestCredentialRequestsDeleteConfirmedFlagAndNoRetry(t *testing.T) {
	args := `{"request_id":"` + credRequestID + `"}`
	h := newCredEnv(t, ownTeam)
	out, err := h.confirmed(credentialRequestsDelete.ID, "credentialdelete", args)
	ch := h.changing()
	if err != nil || len(ch) != 1 || ch[0].method != http.MethodDelete || ch[0].query.Get("confirmed") != "" ||
		!strings.Contains(out, `"deleted":true`) || !strings.Contains(out, `"credentials_deleted":false`) {
		t.Fatalf("out = %s, %v, %+v", out, err, ch)
	}
	h = newCredEnv(t, ownTeam)
	out, err = h.confirmed(credentialRequestsDelete.ID, "credentialdelete",
		`{"request_id":"`+credRequestID+`","confirm_credentials_deleted":true}`)
	ch = h.changing()
	if err != nil || len(ch) != 1 || ch[0].query.Get("confirmed") != "true" || !strings.Contains(out, `"credentials_deleted":true`) {
		t.Fatalf("out = %s, %v, %+v", out, err, ch)
	}
	if _, err := h.confirmed(credentialRequestsDelete.ID, "open", args); err == nil {
		t.Fatal("delete was offered without a tools list")
	}
	if !credentialRequestsDelete.RequiresToolAllowList {
		t.Fatal("delete must require a tools list")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == credentialRequestsDelete.ID {
				t.Fatalf("profile %s contains the delete tool", profile.ID)
			}
		}
	}
	if _, err := h.invoke(credentialRequestsDelete.ID, "credentialdelete", args); !isConfirmationRequired(err) {
		t.Fatalf("delete without confirm err = %v", err)
	}
	for _, bad := range []string{`{"request_id":"1"}`, `{"request_id":"../x"}`, `{"request_id":"` + credRequestID + `","confirmed":true}`} {
		h = newCredEnv(t, ownTeam)
		if _, err := h.confirmed(credentialRequestsDelete.ID, "credentialdelete", bad); err == nil || len(h.calls) != 0 {
			t.Fatalf("%s accepted: %v", bad, err)
		}
	}
}

func TestCredentialRequestChangesAreNeverRetried(t *testing.T) {
	for _, c := range []struct{ op, conn, args string }{
		{credentialRequestsCreate.ID, "open", credCreateArgs},
		{credentialRequestsDelete.ID, "credentialdelete", `{"request_id":"` + credRequestID + `"}`},
	} {
		for _, status := range []int{500, 403} {
			h := newCredEnv(t, ownTeam)
			h.status = status
			_, err := h.confirmed(c.op, c.conn, c.args)
			var perr *provider.Error
			if err == nil || strings.Contains(err.Error(), foreignCanary) || len(h.changing()) != 1 {
				t.Fatalf("%s %d err = %v, changing = %+v", c.op, status, err, h.changing())
			}
			if status == 500 && !strings.Contains(err.Error(), "may have been") {
				t.Fatalf("%s 500 is not reported as uncertain: %v", c.op, err)
			}
			if status == 403 && (!asProviderError(err, &perr) || perr.Class != provider.ClassPermission ||
				!strings.Contains(perr.Message, "credential-requests:write")) {
				t.Fatalf("%s 403 err = %v", c.op, err)
			}
		}
	}
	// An unreadable answer and a missing link are uncertain, not repeated.
	h := newCredEnv(t, ownTeam)
	h.environment = newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/users/") {
			return jsonResponse(200, `{"userTeamRole":{"userId":7,"teamId":1001}}`), nil
		}
		return jsonResponse(200, `{"request":`+credRequestJSON(credRequestID, ownTeam)+`,"publicUri":"http://x"}`), nil
	})
	_, err := h.confirmed(credentialRequestsCreate.ID, "open", credCreateArgs)
	if err == nil || classOf(err) != provider.ClassInvalidResponse || len(h.changing()) != 1 {
		t.Fatalf("err = %v, changing = %+v", err, h.changing())
	}
}

func asProviderError(err error, target **provider.Error) bool {
	perr, ok := err.(*provider.Error)
	*target = perr
	return ok
}

func TestCredentialRequestReadPermissionNamesScope(t *testing.T) {
	h := newEnvironment(t, &[]call{}, func(*http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
	})
	_, err := h.invoke(credentialRequestsList.ID, "open", `{}`)
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "credential-requests:read") ||
		strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("err = %v", err)
	}
}
