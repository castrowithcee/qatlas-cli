package n8n

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	ownCredential     = "CRED_OWN_0001AAAAA"
	foreignCredential = "CRED_FOREIGN_0002BB"
	dataCanary        = "credential-data-canary-77d1"
)

func credentialJSONOf(id, project string) string {
	return fmt.Sprintf(`{"id":%q,"name":"n-%s","type":"httpBasicAuth","createdAt":"2026-01-01T00:00:00Z",`+
		`"updatedAt":"2026-01-02T00:00:00Z","data":{"password":%q},"shared":[{"role":"credential:owner",`+
		`"projectId":%q,"project":{"id":%q}}]}`, id, id, dataCanary, project, project)
}

func credentialsBody() string {
	return fmt.Sprintf(`{"data":[%s,%s],"nextCursor":null}`, credentialJSONOf(ownCredential, ownProject),
		credentialJSONOf(foreignCredential, foreignProject))
}

func credentialServer(t *testing.T) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/credentials":
			return jsonResponse(200, credentialsBody()), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/credentials/"+ownCredential:
			return jsonResponse(200, credentialJSONOf(ownCredential, ownProject)), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/credentials/"+ownCredential+"/test":
			return jsonResponse(200, `{"status":"OK","message":"Connection tested successfully","data":"`+dataCanary+`"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/credentials/schema/httpBasicAuth":
			return jsonResponse(200, `{"type":"object","properties":{"user":{"type":"string"},`+
				`"password":{"type":"string","default":"`+dataCanary+`"}},"required":["user"]}`), nil
		}
		t.Errorf("unexpected request to %s %s", r.Method, r.URL.Path)
		return jsonResponse(500, `{}`), nil
	}
}

func TestCredentialToolsSendTheDocumentedRequestsWithoutValues(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, credentialServer(t))
	var all []string
	result, err := env.invoke(credentialsList.ID, "open", `{"limit":5,"cursor":"c1"}`)
	all = append(all, result)
	if err != nil || calls[0].method != http.MethodGet || calls[0].path != apiPath+"/credentials" ||
		calls[0].query.Get("limit") != "5" || calls[0].query.Get("cursor") != "c1" ||
		!strings.Contains(result, `"count":2`) {
		t.Fatalf("list: %s, %v, %+v", result, err, calls)
	}
	result, err = env.invoke(credentialsGet.ID, "open", fmt.Sprintf(`{"credential_id":%q}`, ownCredential))
	all = append(all, result)
	if err != nil || len(calls) != 2 || calls[1].method != http.MethodGet ||
		calls[1].path != apiPath+"/credentials/"+ownCredential || !strings.Contains(result, `"type":"httpBasicAuth"`) {
		t.Fatalf("get: %s, %v, %+v", result, err, calls)
	}
	result, err = env.confirmed(credentialsTest.ID, "open", fmt.Sprintf(`{"credential_id":%q}`, ownCredential))
	all = append(all, result)
	if err != nil || len(calls) != 3 || calls[2].method != http.MethodPost ||
		calls[2].path != apiPath+"/credentials/"+ownCredential+"/test" || !strings.Contains(result, `"status":"OK"`) {
		t.Fatalf("test: %s, %v, %+v", result, err, calls)
	}
	result, err = env.invoke(credentialsSchema.ID, "open", `{"credential_type":"httpBasicAuth"}`)
	all = append(all, result)
	if err != nil || len(calls) != 4 || calls[3].method != http.MethodGet ||
		calls[3].path != apiPath+"/credentials/schema/httpBasicAuth" ||
		!strings.Contains(result, `{"name":"user","required":true,"type":"string"}`) {
		t.Fatalf("schema: %s, %v, %+v", result, err, calls)
	}
	for _, r := range all {
		if strings.Contains(r, dataCanary) {
			t.Fatalf("a credential value leaked: %s", r)
		}
	}
}

func TestCredentialsListFiltersByProjectBinding(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, credentialServer(t))
	result, err := env.invoke(credentialsList.ID, "project", `{}`)
	if err != nil || !strings.Contains(result, ownCredential) || strings.Contains(result, foreignCredential) ||
		strings.Contains(result, foreignProject) || strings.Contains(result, dataCanary) {
		t.Fatalf("list: %s, %v", result, err)
	}
}

func TestCredentialGetAndTestFailClosed(t *testing.T) {
	pages := map[string]func(*http.Request) (*http.Response, error){
		"foreign": func(r *http.Request) (*http.Response, error) { return jsonResponse(200, credentialsBody()), nil },
		"missing": func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
		},
		"search fails": func(r *http.Request) (*http.Response, error) { return jsonResponse(500, `{}`), nil },
		"endless": func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"data":[],"nextCursor":"more"}`), nil
		},
		"no shared": func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, fmt.Sprintf(`{"data":[{"id":%q,"name":"x","type":"t"}]}`, ownCredential)), nil
		},
	}
	for name, handler := range pages {
		id := ownCredential
		if name == "foreign" {
			id = foreignCredential
		}
		for _, operation := range []string{credentialsGet.ID, credentialsTest.ID} {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != apiPath+"/credentials" {
					t.Errorf("%s %s: unexpected request %s %s", name, operation, r.Method, r.URL.Path)
				}
				return handler(r)
			})
			_, err := env.confirmed(operation, "project", fmt.Sprintf(`{"credential_id":%q}`, id))
			if err == nil || strings.Contains(err.Error(), foreignProject) {
				t.Fatalf("%s %s: err = %v", name, operation, err)
			}
			for _, c := range calls {
				if c.path != apiPath+"/credentials" {
					t.Fatalf("%s %s: request beyond the search: %+v", name, operation, calls)
				}
			}
			if name == "endless" && len(calls) != maxCredentialLookupPages {
				t.Fatalf("search not capped: %d pages", len(calls))
			}
		}
	}
}

func TestCredentialToolsAreRefusedOnAWorkflowAllowList(t *testing.T) {
	args := map[string]string{
		credentialsList.ID:   `{}`,
		credentialsGet.ID:    fmt.Sprintf(`{"credential_id":%q}`, ownCredential),
		credentialsTest.ID:   fmt.Sprintf(`{"credential_id":%q}`, ownCredential),
		credentialsSchema.ID: `{"credential_type":"httpBasicAuth"}`,
	}
	for operation, a := range args {
		for _, connection := range []string{"workflow", "both"} {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(operation, connection, a)
			if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), ownWorkflow) {
				t.Fatalf("%s on %s: err = %v", operation, connection, err)
			}
		}
	}
}

func TestCredentialTestNeedsConfirmationAndNeverRepeats(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	if _, err := env.invoke(credentialsTest.ID, "open", fmt.Sprintf(`{"credential_id":%q}`, ownCredential)); !isConfirmationRequired(err) {
		t.Fatalf("err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("a request was sent without confirmation: %+v", calls)
	}
	for name, respond := range map[string]func(*http.Request) (*http.Response, error){
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(500, `{"message":"`+dataCanary+`"}`), nil
		},
		"abort": func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("connection reset by peer") },
		"junk":  func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	} {
		var c []call
		e := newEnvironment(t, &c, respond)
		_, err := e.confirmed(credentialsTest.ID, "open", fmt.Sprintf(`{"credential_id":%q}`, ownCredential))
		if err == nil || len(c) != 1 || strings.Contains(err.Error(), dataCanary) ||
			!strings.Contains(err.Error(), "may have taken effect") {
			t.Fatalf("%s: err = %v, calls = %d", name, err, len(c))
		}
	}
}

func TestCredentialTestMessageIsCapped(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"status":"Error","message":"`+strings.Repeat("x", 5000)+`\u0007"}`), nil
	})
	result, err := env.confirmed(credentialsTest.ID, "open", fmt.Sprintf(`{"credential_id":%q}`, ownCredential))
	if err != nil || len(result) > 1200 {
		t.Fatalf("result length %d, err %v", len(result), err)
	}
}

func TestCredentialSchemaTypeValidation(t *testing.T) {
	for _, bad := range []string{"", "a/b", "a b", "..", "-x", "a?x=1", "a%2Fb", strings.Repeat("a", 101), "a\n"} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.invoke(credentialsSchema.ID, "open", fmt.Sprintf(`{"credential_type":%q}`, bad))
		if err == nil || len(calls) != 0 {
			t.Fatalf("%q: err = %v", bad, err)
		}
	}
	if err := validCredentialType("google.OAuth2-Api_x"); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialToolsClassifyForbidden(t *testing.T) {
	for _, operation := range []string{credentialsList.ID, credentialsGet.ID, credentialsTest.ID, credentialsSchema.ID} {
		var calls []call
		env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
			return jsonResponse(403, `{"message":"`+dataCanary+`"}`), nil
		})
		args := fmt.Sprintf(`{"credential_id":%q}`, ownCredential)
		switch operation {
		case credentialsList.ID:
			args = `{}`
		case credentialsSchema.ID:
			args = `{"credential_type":"httpBasicAuth"}`
		}
		_, err := env.confirmed(operation, "open", args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "license or role") ||
			strings.Contains(err.Error(), dataCanary) {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
}

func TestCredentialRiskAndProfiles(t *testing.T) {
	if credentialsTest.Risk != credentialTestRisk || credentialTestRisk.Effect != "execute" ||
		credentialTestRisk.Confirmation != "required" || !credentialTestRisk.OpenWorld ||
		credentialTestRisk.DataSensitivity == "" {
		t.Fatalf("risk = %+v", credentialTestRisk)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	found := map[string]string{}
	for _, p := range metadata.Profiles {
		found[p.ID] = strings.Join(p.Tools, ",")
	}
	if found["credentials-read"] != credentialsList.ID+","+credentialsGet.ID+","+credentialsSchema.ID ||
		found["credentials-test"] != credentialsTest.ID {
		t.Fatalf("profiles = %v", found)
	}
}
