package n8n

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Canaries with every character that JSON or HTML escaping would change, one per secret field.
const (
	canaryUser = "us<er>&\"x\nend"
	canaryPass = "pa<ss>&\"word\nend"

	secondProject = "PROJECT_SECOND_0003C"
	releasedRef   = "n8n-login"
)

var credentialChangeTools = []string{credentialsCreate.ID, credentialsUpdate.ID, credentialsTransfer.ID,
	credentialsDelete.ID}

// changeStore is a credential store over a map that counts its reads.
type changeStore struct {
	entries map[string]string
	gets    int
}

func (s *changeStore) Get(_ context.Context, key string) (string, error) {
	s.gets++
	if value, ok := s.entries[key]; ok {
		return value, nil
	}
	return "", secret.ErrNoEntry
}
func (s *changeStore) Set(string, string) error { return nil }
func (s *changeStore) Delete(string) error      { return nil }

// changeEnvironment is a core whose connections release a forward credential to the four change tools.
type changeEnvironment struct {
	*environment
	store *changeStore
}

func newChangeEnvironment(t *testing.T, calls *[]call, handler func(*http.Request) (*http.Response, error)) *changeEnvironment {
	t.Helper()
	serve(t, calls, handler)
	store := &changeStore{entries: map[string]string{
		secret.StoreKey(releasedRef, "username"): canaryUser, secret.StoreKey(releasedRef, "password"): canaryPass,
		secret.StoreKey("n8n-unlisted", "username"): "unlisted-user-value",
		secret.StoreKey("n8n-unlisted", "password"): "unlisted-pass-value",
	}}
	reads := 0
	red := &redact.Redactor{}
	resolve := secret.NewWith(func(name string) string {
		reads++
		if name == apiKeyEnv {
			return apiKeyValue
		}
		return ""
	}, countingStore{store: store, reads: &reads}, nil, red)

	cfg := coreConfig()
	cfg.Credentials[releasedRef] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"username", "password"}}
	cfg.Credentials["n8n-unlisted"] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"username", "password"}}
	cfg.Credentials["n8n-plain"] = config.Credential{Type: config.CredentialTypeKeyring}
	connection := func(targets ...string) config.Connection {
		return config.Connection{Service: "n8n", Credential: "n8n-reader", Permissions: allPermissions,
			Targets: targets, Tools: credentialChangeTools, ForwardSecrets: []string{releasedRef}}
	}
	cfg.Connections["cmanage"] = connection()
	cfg.Connections["cproject"] = connection("project/" + ownProject)
	cfg.Connections["ctwo"] = connection("project/"+ownProject, "project/"+secondProject)
	cfg.Connections["cworkflow"] = connection("workflow/" + ownWorkflow)
	core := application.New(registry(t), cfg, resolve, red)
	return &changeEnvironment{environment: &environment{core: core, red: red, reads: &reads}, store: store}
}

// countingStore counts store reads as secret reads of the environment too.
type countingStore struct {
	store *changeStore
	reads *int
}

func (s countingStore) Get(ctx context.Context, key string) (string, error) {
	*s.reads++
	return s.store.Get(ctx, key)
}
func (s countingStore) Set(k, v string) error { return s.store.Set(k, v) }
func (s countingStore) Delete(k string) error { return s.store.Delete(k) }

// changeServer answers the credential list and the four change endpoints. With mirror set, a created or
// updated credential reports the request body it received as its name.
func changeServer(t *testing.T, calls *[]call, mirror bool) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		last := (*calls)[len(*calls)-1]
		name := "new-name"
		if mirror {
			name = last.body
		}
		item := func(id string) *http.Response {
			encoded, _ := json.Marshal(map[string]any{"id": id, "name": name, "type": "httpBasicAuth",
				"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-02T00:00:00Z",
				"data": map[string]string{"password": dataCanary}})
			return jsonResponse(200, string(encoded))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/credentials":
			return jsonResponse(200, credentialsBody()), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/credentials":
			return item("NEWCRED0001"), nil
		case r.Method == http.MethodPatch && r.URL.Path == apiPath+"/credentials/"+ownCredential:
			return item(ownCredential), nil
		case r.Method == http.MethodPut && r.URL.Path == apiPath+"/credentials/"+ownCredential+"/transfer":
			return jsonResponse(204, ``), nil
		case r.Method == http.MethodDelete && r.URL.Path == apiPath+"/credentials/"+ownCredential:
			return jsonResponse(200, `{"id":"`+ownCredential+`","name":"x","type":"t"}`), nil
		}
		t.Errorf("unexpected request to %s %s", r.Method, r.URL.Path)
		return jsonResponse(500, `{}`), nil
	}
}

func decodeBody(t *testing.T, c call) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(c.body), &body); err != nil {
		t.Fatalf("body %q: %v", c.body, err)
	}
	return body
}

func hasCanary(text string) bool {
	for _, canary := range []string{canaryUser, canaryPass} {
		escaped, _ := json.Marshal(canary)
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(canary)
		for _, form := range []string{canary, string(escaped[1 : len(escaped)-1]),
			strings.TrimSpace(buf.String()[1 : len(buf.String())-2])} {
			if strings.Contains(text, form) {
				return true
			}
		}
	}
	return strings.Contains(text, dataCanary)
}

func changeArguments(operation string) string {
	switch operation {
	case credentialsCreate.ID:
		return fmt.Sprintf(`{"name":"n","credential_type":"httpBasicAuth","data":{"host":"h"},"secret_ref":%q,"project_id":%q}`,
			releasedRef, ownProject)
	case credentialsUpdate.ID:
		return fmt.Sprintf(`{"credential_id":%q,"name":"n","secret_ref":%q}`, ownCredential, releasedRef)
	case credentialsTransfer.ID:
		return fmt.Sprintf(`{"credential_id":%q,"destination_project_id":%q}`, ownCredential, secondProject)
	}
	return fmt.Sprintf(`{"credential_id":%q}`, ownCredential)
}

func TestCredentialChangesSendTheDocumentedRequests(t *testing.T) {
	var calls []call
	env := newChangeEnvironment(t, &calls, changeServer(t, &calls, false))
	var all []string

	result, err := env.confirmed(credentialsCreate.ID, "cmanage",
		fmt.Sprintf(`{"name":"My cred","credential_type":"httpBasicAuth","data":{"host":"h","port":8080,"tls":true},`+
			`"secret_ref":%q,"project_id":%q}`, releasedRef, ownProject))
	all = append(all, result)
	if err != nil || len(calls) != 1 || calls[0].method != http.MethodPost || calls[0].path != apiPath+"/credentials" {
		t.Fatalf("create: %s, %v, %+v", result, err, calls)
	}
	body := decodeBody(t, calls[0])
	data, _ := body["data"].(map[string]any)
	if body["name"] != "My cred" || body["type"] != "httpBasicAuth" || body["projectId"] != ownProject ||
		data["host"] != "h" || data["port"] != float64(8080) || data["tls"] != true ||
		data["username"] != canaryUser || data["password"] != canaryPass || len(data) != 5 || len(body) != 4 {
		t.Fatalf("create body = %s", calls[0].body)
	}

	result, err = env.confirmed(credentialsUpdate.ID, "cmanage", changeArguments(credentialsUpdate.ID))
	all = append(all, result)
	if err != nil || len(calls) != 2 || calls[1].method != http.MethodPatch ||
		calls[1].path != apiPath+"/credentials/"+ownCredential {
		t.Fatalf("update: %s, %v, %+v", result, err, calls)
	}
	body = decodeBody(t, calls[1])
	data, _ = body["data"].(map[string]any)
	if body["name"] != "n" || data["username"] != canaryUser ||
		data["password"] != canaryPass || len(data) != 2 || len(body) != 2 ||
		strings.Contains(calls[1].body, "isPartialData") {
		t.Fatalf("update body = %s", calls[1].body)
	}

	result, err = env.confirmed(credentialsUpdate.ID, "cmanage", fmt.Sprintf(`{"credential_id":%q,"name":"only"}`, ownCredential))
	all = append(all, result)
	if err != nil || len(calls) != 3 || calls[2].body != `{"name":"only"}` {
		t.Fatalf("rename: %s, %v, %+v", result, err, calls)
	}

	result, err = env.confirmed(credentialsDelete.ID, "cmanage", changeArguments(credentialsDelete.ID))
	all = append(all, result)
	if err != nil || len(calls) != 4 || calls[3].method != http.MethodDelete ||
		calls[3].path != apiPath+"/credentials/"+ownCredential || calls[3].body != "" ||
		!strings.Contains(result, `"deleted":true`) {
		t.Fatalf("delete: %s, %v, %+v", result, err, calls)
	}

	// Transfer needs a project allow-list: the binding search is the only read before the one change.
	result, err = env.confirmed(credentialsTransfer.ID, "ctwo", changeArguments(credentialsTransfer.ID))
	all = append(all, result)
	if err != nil || len(calls) != 6 || calls[4].method != http.MethodGet || calls[4].path != apiPath+"/credentials" ||
		calls[5].method != http.MethodPut || calls[5].path != apiPath+"/credentials/"+ownCredential+"/transfer" ||
		calls[5].body != `{"destinationProjectId":"`+secondProject+`"}` ||
		!strings.Contains(result, `"transferred":true`) {
		t.Fatalf("transfer: %s, %v, %+v", result, err, calls)
	}
	for _, r := range all {
		if hasCanary(r) {
			t.Fatalf("a secret leaked: %s", r)
		}
	}
}

func TestCredentialSecretNeverSurfacesFromAMirroringServer(t *testing.T) {
	for _, operation := range []string{credentialsCreate.ID, credentialsUpdate.ID} {
		var calls []call
		env := newChangeEnvironment(t, &calls, changeServer(t, &calls, true))
		result, err := env.confirmed(operation, "cmanage", changeArguments(operation))
		if err != nil || !strings.Contains(result, redact.Marker) || hasCanary(result) {
			t.Fatalf("%s: %s, %v", operation, result, err)
		}
		if len(calls) != 1 || !strings.Contains(calls[0].body, `"username"`) {
			t.Fatalf("%s: calls = %+v", operation, calls)
		}
	}
	// A failing answer neither mirrors a body into the error nor shows a value.
	for _, operation := range []string{credentialsCreate.ID, credentialsUpdate.ID} {
		var calls []call
		env := newChangeEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(500, calls[len(calls)-1].body), nil
		})
		_, err := env.confirmed(operation, "cmanage", changeArguments(operation))
		if err == nil || hasCanary(err.Error()) || hasCanary(env.red.Error(err)) {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
}

func TestCredentialChangesNeedConfirmationAndReadNoStore(t *testing.T) {
	for _, operation := range credentialChangeTools {
		var calls []call
		env := newChangeEnvironment(t, &calls, noRequest(t))
		_, err := env.invoke(operation, "ctwo", changeArguments(operation))
		if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 || env.store.gets != 0 {
			t.Fatalf("%s: err = %v, reads %d", operation, err, *env.reads)
		}
	}
}

func TestCredentialSecretReferenceRefusals(t *testing.T) {
	for _, ref := range []string{"n8n-unlisted", "n8n-plain", "n8n-reader", "unknown-ref", canaryPass} {
		for _, operation := range []string{credentialsCreate.ID, credentialsUpdate.ID} {
			var calls []call
			env := newChangeEnvironment(t, &calls, noRequest(t))
			arguments := strings.Replace(changeArguments(operation), releasedRef, strings.Trim(mustJSON(ref), `"`), 1)
			_, err := env.confirmed(operation, "cmanage", arguments)
			var refused *application.SecretRefNotAllowedError
			if !errors.As(err, &refused) || len(calls) != 0 || env.store.gets != 0 || hasCanary(err.Error()) {
				t.Fatalf("%s %q: err = %v", operation, ref, err)
			}
		}
	}
}

func mustJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestCredentialFieldCollisionIsRefused(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{credentialsCreate.ID, fmt.Sprintf(`{"name":"n","credential_type":"httpBasicAuth","data":{"password":"plain"},"secret_ref":%q}`, releasedRef)},
		{credentialsUpdate.ID, fmt.Sprintf(`{"credential_id":%q,"data":{"username":"plain"},"secret_ref":%q}`, ownCredential, releasedRef)},
	} {
		var calls []call
		env := newChangeEnvironment(t, &calls, changeServer(t, &calls, false))
		_, err := env.confirmed(tt.operation, "cmanage", tt.arguments)
		if !isInvalidRequest(err) || len(calls) != 0 || hasCanary(err.Error()) || hasCanary(env.red.Error(err)) {
			t.Fatalf("%s: err = %v, calls = %+v", tt.operation, err, calls)
		}
	}
}

func TestCredentialChangesAreBoundToTheProjectAllowList(t *testing.T) {
	// create: project_id is required and must be inside the allow-list; nothing is sent either way.
	for name, arguments := range map[string]string{
		"missing project": fmt.Sprintf(`{"name":"n","credential_type":"httpBasicAuth","data":{"a":"b"},"secret_ref":%q}`, releasedRef),
		"foreign project": fmt.Sprintf(`{"name":"n","credential_type":"httpBasicAuth","data":{"a":"b"},"project_id":%q}`, foreignProject),
	} {
		var calls []call
		env := newChangeEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(credentialsCreate.ID, "cproject", arguments)
		if !isInvalidRequest(err) || len(calls) != 0 || strings.Contains(err.Error(), foreignProject) {
			t.Fatalf("create %s: err = %v", name, err)
		}
	}
	var created []call
	env := newChangeEnvironment(t, &created, changeServer(t, &created, false))
	if _, err := env.confirmed(credentialsCreate.ID, "cproject", changeArguments(credentialsCreate.ID)); err != nil ||
		len(created) != 1 || created[0].method != http.MethodPost {
		t.Fatalf("create in the allowed project: %v, %+v", err, created)
	}

	// update, transfer, delete: every foreign or unfindable credential stops at the read-only search.
	searches := map[string]func(*http.Request) (*http.Response, error){
		"foreign":     func(*http.Request) (*http.Response, error) { return jsonResponse(200, credentialsBody()), nil },
		"shared only": func(*http.Request) (*http.Response, error) { return jsonResponse(200, sharedOnlyBody()), nil },
		"no role": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"data":[{"id":"`+ownCredential+`","shared":[{"id":"`+ownProject+`"}]}]}`), nil
		},
		"missing": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{"data":[]}`), nil },
		"search fails": func(*http.Request) (*http.Response, error) {
			return jsonResponse(500, `{}`), nil
		},
		"endless": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"data":[],"nextCursor":"more"}`), nil
		},
	}
	for name, search := range searches {
		for _, operation := range []string{credentialsUpdate.ID, credentialsTransfer.ID, credentialsDelete.ID} {
			var calls []call
			env := newChangeEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != apiPath+"/credentials" {
					t.Errorf("%s %s: a change was sent: %s %s", name, operation, r.Method, r.URL.Path)
				}
				return search(r)
			})
			id := ownCredential
			if name == "foreign" {
				id = foreignCredential
			}
			arguments := strings.ReplaceAll(changeArguments(operation), ownCredential, id)
			_, err := env.confirmed(operation, "ctwo", arguments)
			if err == nil || strings.Contains(err.Error(), foreignProject) {
				t.Fatalf("%s %s: err = %v", name, operation, err)
			}
			for _, c := range calls {
				if c.method != http.MethodGet {
					t.Fatalf("%s %s: %+v", name, operation, calls)
				}
			}
			if name == "endless" && len(calls) != maxCredentialLookupPages {
				t.Fatalf("%s: search not capped: %d", operation, len(calls))
			}
		}
	}
}

func TestCredentialTransferIsRefusedLocally(t *testing.T) {
	for name, tt := range map[string]struct{ connection, arguments string }{
		"no allow-list": {"cmanage", changeArguments(credentialsTransfer.ID)},
		"destination outside": {"ctwo", fmt.Sprintf(`{"credential_id":%q,"destination_project_id":%q}`,
			ownCredential, foreignProject)},
		"bad destination": {"ctwo", fmt.Sprintf(`{"credential_id":%q,"destination_project_id":"a/b"}`, ownCredential)},
		"bad credential":  {"ctwo", fmt.Sprintf(`{"credential_id":"a/b","destination_project_id":%q}`, secondProject)},
		"workflow list":   {"cworkflow", changeArguments(credentialsTransfer.ID)},
	} {
		var calls []call
		env := newChangeEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(credentialsTransfer.ID, tt.connection, tt.arguments)
		if !isInvalidRequest(err) || len(calls) != 0 || strings.Contains(err.Error(), foreignProject) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestCredentialChangesAreRefusedOnAWorkflowAllowList(t *testing.T) {
	for _, operation := range credentialChangeTools {
		var calls []call
		env := newChangeEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(operation, "cworkflow", changeArguments(operation))
		if !isInvalidRequest(err) || len(calls) != 0 || env.store.gets != 0 || strings.Contains(err.Error(), ownWorkflow) {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
}

func TestCredentialChangeArgumentsAreValidatedBeforeAnyRequest(t *testing.T) {
	long := strings.Repeat("a", maxCredentialFieldTextLength+1)
	var many strings.Builder
	for i := 0; i <= maxCredentialDataFields; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `"f%d":"v"`, i)
	}
	create := func(rest string) string {
		return `{"name":"n","credential_type":"httpBasicAuth"` + rest + `}`
	}
	update := func(rest string) string { return fmt.Sprintf(`{"credential_id":%q%s}`, ownCredential, rest) }
	cases := map[string][2]string{
		"create without data":     {credentialsCreate.ID, create(``)},
		"create empty data":       {credentialsCreate.ID, create(`,"data":{}`)},
		"create empty name":       {credentialsCreate.ID, `{"name":"","credential_type":"httpBasicAuth","data":{"a":"b"}}`},
		"create control name":     {credentialsCreate.ID, `{"name":"a\u0007b","credential_type":"httpBasicAuth","data":{"a":"b"}}`},
		"create bad type":         {credentialsCreate.ID, `{"name":"n","credential_type":"a/b","data":{"a":"b"}}`},
		"create bad field name":   {credentialsCreate.ID, create(`,"data":{"a b":"c"}`)},
		"create digit field name": {credentialsCreate.ID, create(`,"data":{"1a":"c"}`)},
		"create nested value":     {credentialsCreate.ID, create(`,"data":{"a":{"b":1}}`)},
		"create null value":       {credentialsCreate.ID, create(`,"data":{"a":null}`)},
		"create long value":       {credentialsCreate.ID, create(`,"data":{"a":"` + long + `"}`)},
		"create many fields":      {credentialsCreate.ID, create(`,"data":{` + many.String() + `}`)},
		"create bad project":      {credentialsCreate.ID, create(`,"data":{"a":"b"},"project_id":"a/b"`)},
		"create unknown member":   {credentialsCreate.ID, create(`,"data":{"a":"b"},"password":"x"`)},
		"update nothing":          {credentialsUpdate.ID, update(``)},
		"update empty data":       {credentialsUpdate.ID, update(`,"data":{}`)},
		"update empty name":       {credentialsUpdate.ID, update(`,"name":""`)},
		"update bad id":           {credentialsUpdate.ID, `{"credential_id":"a/b","name":"n"}`},
		"update type change":      {credentialsUpdate.ID, update(`,"type":"x"`)},
		"delete bad id":           {credentialsDelete.ID, `{"credential_id":"a/b"}`},
	}
	for name, tt := range cases {
		var calls []call
		env := newChangeEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(tt[0], "cmanage", tt[1])
		if err == nil || len(calls) != 0 || env.store.gets != 0 {
			t.Fatalf("%s: err = %v, calls %d", name, err, len(calls))
		}
	}
}

func TestCredentialChangesAreSentOnceAndNeverRepeated(t *testing.T) {
	failures := map[string]func(*http.Request) (*http.Response, error){
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(500, `{"message":"`+dataCanary+`"}`), nil
		},
		"abort": func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("connection reset by peer") },
		"junk":  func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	}
	for name, respond := range failures {
		for _, operation := range credentialChangeTools {
			if name == "junk" && (operation == credentialsTransfer.ID || operation == credentialsDelete.ID) {
				continue // these decode no answer
			}
			var calls []call
			env := newChangeEnvironment(t, &calls, respond)
			_, err := env.confirmed(operation, "cmanage", changeArguments(operation))
			if operation == credentialsTransfer.ID {
				// Transfer needs an allow-list: use one whose search answers, then fail the change.
				calls = nil
				env = newChangeEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet {
						return jsonResponse(200, credentialsBody()), nil
					}
					return respond(r)
				})
				_, err = env.confirmed(operation, "ctwo", changeArguments(operation))
			}
			changes := 0
			for _, c := range calls {
				if c.method != http.MethodGet {
					changes++
				}
			}
			if err == nil || changes != 1 || strings.Contains(err.Error(), dataCanary) ||
				!strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("%s %s: err = %v, changes = %d", name, operation, err, changes)
			}
		}
	}
}

func TestCredentialChangesClassifyForbidden(t *testing.T) {
	for _, operation := range credentialChangeTools {
		var calls []call
		env := newChangeEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return jsonResponse(200, credentialsBody()), nil
			}
			return jsonResponse(403, `{"message":"`+dataCanary+`"}`), nil
		})
		_, err := env.confirmed(operation, "ctwo", changeArguments(operation))
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "license or role") ||
			strings.Contains(err.Error(), dataCanary) {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
}

func TestCredentialChangeDescriptorsAndProfiles(t *testing.T) {
	for _, d := range []struct {
		id         string
		secretRefs int
	}{{credentialsCreate.ID, 1}, {credentialsUpdate.ID, 1}, {credentialsTransfer.ID, 0}, {credentialsDelete.ID, 0}} {
		descriptor, _, ok := registry(t).Lookup(d.id)
		if !ok {
			t.Fatalf("%s is not registered", d.id)
		}
		refs := 0
		for _, argument := range descriptor.Arguments {
			if argument.SecretRef {
				refs++
			}
		}
		risk := descriptor.Risk
		if !descriptor.RequiresToolAllowList || risk.Confirmation != "required" || !risk.OpenWorld ||
			risk.DataSensitivity == "" || risk.Effect == "" || risk.Idempotency == "" || refs != d.secretRefs {
			t.Fatalf("%s: %+v", d.id, descriptor)
		}
	}
	if !strings.Contains(credentialsDelete.Description, "fails afterwards") ||
		!strings.Contains(credentialsUpdate.Description, "REPLACES the whole stored data") {
		t.Fatal("descriptions miss their warnings")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, tool := range p.Tools {
			for _, change := range credentialChangeTools {
				if tool == change {
					t.Fatalf("profile %s offers %s", p.ID, tool)
				}
			}
		}
	}
}
