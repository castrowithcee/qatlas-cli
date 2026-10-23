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
		`"updatedAt":"2026-01-02T00:00:00Z","data":{"password":%q},"shared":[{"id":%q,"name":"p","role":"credential:owner",`+
		`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]}`, id, id, dataCanary, project)
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

// sharedOnlyBody lists the own credential owned by a foreign project and only shared into the allowed one.
func sharedOnlyBody() string {
	return fmt.Sprintf(`{"data":[{"id":%q,"name":"n","type":"httpBasicAuth","shared":[`+
		`{"id":%q,"name":"f","role":"credential:owner"},{"id":%q,"name":"p","role":"credential:user"}]}],"nextCursor":null}`,
		ownCredential, foreignProject, ownProject)
}

func TestCredentialSharedOnlyIsReadableButNotChangeable(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/credentials":
			return jsonResponse(200, sharedOnlyBody()), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/credentials/"+ownCredential:
			return jsonResponse(200, credentialJSONOf(ownCredential, ownProject)), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/credentials/"+ownCredential+"/test":
			return jsonResponse(200, `{"status":"OK","message":"ok"}`), nil
		}
		t.Errorf("unexpected request to %s %s", r.Method, r.URL.Path)
		return jsonResponse(500, `{}`), nil
	})
	arguments := fmt.Sprintf(`{"credential_id":%q}`, ownCredential)
	if _, err := env.invoke(credentialsGet.ID, "open", arguments); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := env.confirmed(credentialsTest.ID, "open", arguments); err != nil {
		t.Fatalf("test: %v", err)
	}
}

// richSharedCredential is a listed credential shared into an allowed and a foreign project.
func richSharedCredential() string {
	return fmt.Sprintf(`{"id":%q,"name":"n","type":"httpBasicAuth","createdAt":"2026-01-01T00:00:00Z",`+
		`"updatedAt":"2026-01-02T00:00:00Z","shared":[`+
		`{"id":%q,"name":"Own project","role":"credential:owner","createdAt":"2026-02-01T00:00:00Z","updatedAt":"2026-02-02T00:00:00Z"},`+
		`{"id":%q,"name":"Foreign secret name","role":"credential:user","createdAt":"2026-03-01T00:00:00Z","updatedAt":"2026-03-02T00:00:00Z"}]}`,
		ownCredential, ownProject, foreignProject)
}

func TestCredentialsListShowsSharesAndHidesForeignProjectDetails(t *testing.T) {
	body := `{"data":[` + richSharedCredential() + `],"nextCursor":null}`
	for connection, foreignFull := range map[string]bool{"open": true, "project": false} {
		var calls []call
		env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(200, body), nil })
		result, err := env.invoke(credentialsList.ID, connection, `{}`)
		if err != nil || !strings.Contains(result, `{"created_at":"2026-02-01T00:00:00Z","id":"`+ownProject+
			`","name":"Own project","role":"credential:owner","updated_at":"2026-02-02T00:00:00Z"}`) {
			t.Fatalf("%s: %s, %v", connection, result, err)
		}
		if got := strings.Contains(result, "Foreign secret name"); got != foreignFull {
			t.Fatalf("%s: foreign name shown = %v: %s", connection, got, result)
		}
		if !strings.Contains(result, `"id":"`+foreignProject+`"`) || !strings.Contains(result, `"role":"credential:user"`) {
			t.Fatalf("%s: foreign share lost its id and role: %s", connection, result)
		}
		if !foreignFull && (strings.Contains(result, "2026-03-01") || strings.Contains(result, "2026-03-02")) {
			t.Fatalf("%s: foreign share times shown: %s", connection, result)
		}
	}
}

func TestCredentialsListCapsShares(t *testing.T) {
	var shares []string
	for i := 0; i < maxCredentialShares+5; i++ {
		shares = append(shares, fmt.Sprintf(`{"id":"P%d","name":"p","role":"credential:user"}`, i))
	}
	body := `{"data":[{"id":"c","name":"n","type":"t","shared":[` + strings.Join(shares, ",") + `]}],"nextCursor":null}`
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(200, body), nil })
	result, err := env.invoke(credentialsList.ID, "open", `{}`)
	if err != nil || strings.Count(result, `"role":"credential:user"`) != maxCredentialShares ||
		!strings.Contains(result, `"shared_truncated":true`) {
		t.Fatalf("%s, %v", result, err)
	}
}

func TestCredentialsGetShowsManagementAttributes(t *testing.T) {
	body := fmt.Sprintf(`{"id":%q,"name":"n","type":"httpBasicAuth","isManaged":false,"isGlobal":true,`+
		`"isResolvable":false,"resolvableAllowFallback":true,"resolverId":"res-1",`+
		`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","description":"Read-only\u0007 key"}`, ownCredential)
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(200, body), nil })
	result, err := env.invoke(credentialsGet.ID, "open", fmt.Sprintf(`{"credential_id":%q}`, ownCredential))
	if err != nil || len(calls) != 1 {
		t.Fatalf("%s, %v, %d calls", result, err, len(calls))
	}
	for _, want := range []string{`"is_managed":false`, `"is_global":true`, `"is_resolvable":false`,
		`"resolvable_allow_fallback":true`, `"resolver_id":"res-1"`, `"description":"Read-only  key"`} {
		if !strings.Contains(result, want) {
			t.Fatalf("missing %s in %s", want, result)
		}
	}
	assertOutputDeclared(t, credentialsGet.ID, []byte(result))
	// A read without the attributes (and a null resolver) reports none of them.
	env = newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"id":"c","name":"n","type":"t","resolverId":null,"description":null}`), nil
	})
	result, err = env.invoke(credentialsGet.ID, "open", `{"credential_id":"c"}`)
	if err != nil || result != `{"id":"c","name":"n","type":"t"}` {
		t.Fatalf("%s, %v", result, err)
	}
}

func schemaResult(t *testing.T, body string) string {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(200, body), nil })
	result, err := env.invoke(credentialsSchema.ID, "open", `{"credential_type":"httpBasicAuth"}`)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCredentialsSchemaShowsEnumAndAllOf(t *testing.T) {
	result := schemaResult(t, `{"additionalProperties":false,"type":"object","properties":{`+
		`"authType":{"type":"string","enum":["basic","oauth\u0007",42,9007199254740993,true,{"x":1},null]},`+
		`"user":{"type":"string"},"token":{"type":"string"}},"required":["authType"],"allOf":[`+
		`{"if":{"properties":{"authType":{"enum":["oauth"]}},"required":["authType"]},`+
		`"then":{"allOf":[{"required":["token"]}]}}]}`)
	for _, want := range []string{`"enum":["basic","oauth ",42,9007199254740993,true]`,
		`"all_of":[{"if":{"properties":{"authType":{"enum":["oauth"]}},"required":["authType"]},` +
			`"then":{"allOf":[{"required":["token"]}]}}]`} {
		if !strings.Contains(result, want) {
			t.Fatalf("missing %s in %s", want, result)
		}
	}
	if strings.Contains(result, "additionalProperties") || strings.Contains(result, `"type":"object"`) ||
		strings.Contains(result, "all_of_omitted") {
		t.Fatalf("constants or marker in %s", result)
	}
	assertOutputDeclared(t, credentialsSchema.ID, []byte(result))
}

func TestCredentialsSchemaBoundsEnumAndOmitsOversizedAllOf(t *testing.T) {
	values := make([]string, maxSchemaEnumValues+3)
	for i := range values {
		values[i] = fmt.Sprintf("%d", i)
	}
	rules := make([]string, maxSchemaAllOfEntries)
	for i := range rules {
		rules[i] = `{"required":["a"]}`
	}
	deep := `{"a":` + strings.Repeat(`[`, maxSchemaAllOfDepth+1) + `1` + strings.Repeat(`]`, maxSchemaAllOfDepth+1) + `}`
	for name, allOf := range map[string]string{"count": `[` + strings.Join(rules, ",") + `]`, "depth": `[` + deep + `]`} {
		result := schemaResult(t, `{"properties":{"o":{"type":"string","enum":[`+strings.Join(values, ",")+
			`]}},"required":[],"allOf":`+allOf+`}`)
		if !strings.Contains(result, `"all_of_omitted":true`) || strings.Contains(result, `"all_of":`) {
			t.Fatalf("%s: %s", name, result)
		}
		if strings.Count(result, ",")+1 < maxSchemaEnumValues || strings.Contains(result, `,200,`) ||
			!strings.Contains(result, `"enum_truncated":true`) || !strings.Contains(result, `,199]`) {
			t.Fatalf("%s: enum not capped: %s", name, result)
		}
	}
	// Exactly at the bounds the rules are kept.
	result := schemaResult(t, `{"properties":{},"required":[],"allOf":[{"a":[[[[[1]]]]]}]}`)
	if !strings.Contains(result, `"all_of":[{"a":[[[[[1]]]]]}]`) {
		t.Fatalf("%s", result)
	}
}
