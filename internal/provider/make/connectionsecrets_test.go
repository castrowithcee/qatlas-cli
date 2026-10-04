package makeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	canaryClientID     = "cl<id>&\"x\nend"
	canaryClientSecret = "se<cret>&\"word\nend"
	releasedRef        = "make-shop"
)

var connectionSecretTools = []string{connectionsCreate.ID, connectionsSetData.ID}

type secretStore struct {
	entries map[string]string
	gets    int
}

func (s *secretStore) Get(_ context.Context, key string) (string, error) {
	s.gets++
	if value, ok := s.entries[key]; ok {
		return value, nil
	}
	return "", secret.ErrNoEntry
}
func (s *secretStore) Set(string, string) error { return nil }
func (s *secretStore) Delete(string) error      { return nil }

type secretEnv struct {
	*environment
	store  *secretStore
	calls  []call
	bodies []string
	last   []string // "METHOD path?query" of every request
}

// newSecretEnv is a core whose connections release a forward credential. The handler sees every request after
// it was recorded.
func newSecretEnv(t *testing.T, handler func(*secretEnv, *http.Request) (*http.Response, error)) *secretEnv {
	t.Helper()
	h := &secretEnv{}
	serve(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		h.bodies = append(h.bodies, string(body))
		h.last = append(h.last, r.Method+" "+r.URL.RequestURI())
		return handler(h, r)
	})
	h.store = &secretStore{entries: map[string]string{
		secret.StoreKey(releasedRef, "clientId"):         canaryClientID,
		secret.StoreKey(releasedRef, "clientSecret"):     canaryClientSecret,
		secret.StoreKey("make-unlisted", "clientId"):     "unlisted-id-value",
		secret.StoreKey("make-unlisted", "clientSecret"): "unlisted-secret-value",
	}}
	reads := 0
	red := &redact.Redactor{}
	resolve := secret.NewWith(func(name string) string {
		if name == apiTokenEnv {
			return apiTokenValue
		}
		return ""
	}, h.store, nil, red)
	cfg := coreConfig()
	cfg.Credentials[releasedRef] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"clientId", "clientSecret"}}
	cfg.Credentials["make-unlisted"] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"clientId", "clientSecret"}}
	cfg.Credentials["make-plain"] = config.Credential{Type: config.CredentialTypeKeyring}
	manage := func(targets ...string) config.Connection {
		return config.Connection{Service: "make", Credential: "make-reader", Permissions: allPermissions,
			Targets: targets, Tools: connectionSecretTools, ForwardSecrets: []string{releasedRef}}
	}
	cfg.Connections["smanage"] = manage("team/" + itoa64(ownTeam))
	cfg.Connections["sscenario"] = manage("team/"+itoa64(ownTeam), "scenario/"+itoa64(ownScenario))
	h.environment = &environment{core: application.New(registry(t), cfg, resolve, red), red: red, reads: &reads}
	return h
}

func secretServer(foreign bool) func(*secretEnv, *http.Request) (*http.Response, error) {
	id := strconv.FormatInt(connectionID, 10)
	return func(h *secretEnv, r *http.Request) (*http.Response, error) {
		team := ownTeam
		if foreign {
			team = foreignTeam
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/connections":
			return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, h.bodies[len(h.bodies)-1])+`}`), nil
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/connections/"+id:
			return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, team, "Conn")+`}`), nil
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/connections/"+id+"/set-data":
			return jsonResponse(200, `{"changed":true,"echo":"`+canaryConnSecret+`"}`), nil
		}
		return jsonResponse(500, `{}`), nil
	}
}

func hasSecretCanary(text string) bool {
	for _, canary := range []string{canaryClientID, canaryClientSecret} {
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
	return strings.Contains(text, canaryConnSecret)
}

func (h *secretEnv) changing() []int {
	var out []int
	for i, c := range h.calls {
		if c.method != http.MethodGet {
			out = append(out, i)
		}
	}
	return out
}

func secretArguments(operation string) string {
	if operation == connectionsCreate.ID {
		return fmt.Sprintf(`{"name":"Shop","connection_type":"example-type","scopes":["read"],`+
			`"fields":{"host":"h","port":8080,"tls":true},"secret_ref":%q}`, releasedRef)
	}
	return fmt.Sprintf(`{"connection_id":%d,"fields":{"host":"h"},"secret_ref":%q}`, connectionID, releasedRef)
}

func TestConnectionSecretToolsSendTheDocumentedRequests(t *testing.T) {
	h := newSecretEnv(t, secretServer(false))
	result, err := h.confirmed(connectionsCreate.ID, "smanage", secretArguments(connectionsCreate.ID))
	if err != nil || len(h.calls) != 1 || h.last[0] != "POST "+apiPath+"/connections?teamId="+itoa64(ownTeam) {
		t.Fatalf("create: %s, %v, %v", result, err, h.last)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(h.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body["accountName"] != "Shop" || body["accountType"] != "example-type" || body["host"] != "h" ||
		body["port"] != float64(8080) || body["tls"] != true || body["clientId"] != canaryClientID ||
		body["clientSecret"] != canaryClientSecret || len(body) != 8 || strings.Contains(h.bodies[0], "teamId") {
		t.Fatalf("create body = %v", body)
	}
	if hasSecretCanary(result) || !strings.Contains(result, `"id":`+strconv.FormatInt(connectionID, 10)) {
		t.Fatalf("create result = %s", result)
	}

	h = newSecretEnv(t, secretServer(false))
	result, err = h.confirmed(connectionsSetData.ID, "smanage", secretArguments(connectionsSetData.ID))
	id := strconv.FormatInt(connectionID, 10)
	if err != nil || len(h.calls) != 2 || h.calls[0].method != http.MethodGet ||
		h.last[1] != "POST "+apiPath+"/connections/"+id+"/set-data" || len(h.changing()) != 1 {
		t.Fatalf("set data: %s, %v, %v", result, err, h.last)
	}
	body = nil
	_ = json.Unmarshal([]byte(h.bodies[1]), &body)
	if body["host"] != "h" || body["clientId"] != canaryClientID || body["clientSecret"] != canaryClientSecret ||
		len(body) != 3 {
		t.Fatalf("set data body = %v", body)
	}
	if hasSecretCanary(result) || !strings.Contains(result, `"changed":true`) {
		t.Fatalf("set data result = %s", result)
	}
}

func TestConnectionSecretNeverSurfacesFromAMirroringServer(t *testing.T) {
	mirror := func(h *secretEnv, r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return secretServer(false)(h, r)
		}
		body := h.bodies[len(h.bodies)-1]
		if strings.HasSuffix(r.URL.Path, "/set-data") {
			return jsonResponse(200, `{"changed":true,"echo":`+body+`}`), nil
		}
		encoded, _ := json.Marshal(body)
		return jsonResponse(200, `{"connection":{"id":55,"name":`+string(encoded)+`,"teamId":1001}}`), nil
	}
	for _, operation := range connectionSecretTools {
		h := newSecretEnv(t, mirror)
		result, err := h.confirmed(operation, "smanage", secretArguments(operation))
		if err != nil || hasSecretCanary(result) {
			t.Fatalf("%s: %s, %v", operation, result, err)
		}
		if operation == connectionsCreate.ID && !strings.Contains(result, redact.Marker) {
			// The name is a bounded copy of the mirrored body; it is redacted, not shown.
			t.Fatalf("%s: mirrored body was not redacted: %s", operation, result)
		}
	}
	for _, operation := range connectionSecretTools {
		h := newSecretEnv(t, func(h *secretEnv, r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return secretServer(false)(h, r)
			}
			return jsonResponse(500, h.bodies[len(h.bodies)-1]), nil
		})
		_, err := h.confirmed(operation, "smanage", secretArguments(operation))
		if err == nil || hasSecretCanary(err.Error()) || hasSecretCanary(h.red.Error(err)) {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
}

func TestConnectionSecretToolsNeedConfirmationAndReadNoStore(t *testing.T) {
	for _, operation := range connectionSecretTools {
		h := newSecretEnv(t, secretServer(false))
		_, err := h.invoke(operation, "smanage", secretArguments(operation))
		if !isConfirmationRequired(err) || len(h.calls) != 0 || h.store.gets != 0 || *h.reads != 0 {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
}

func TestConnectionSecretReferenceRefusals(t *testing.T) {
	for _, ref := range []string{"make-unlisted", "make-plain", "make-reader", "unknown-ref", canaryClientSecret} {
		for _, operation := range connectionSecretTools {
			h := newSecretEnv(t, secretServer(false))
			arguments := strings.Replace(secretArguments(operation), releasedRef, strings.Trim(mustJSONString(ref), `"`), 1)
			_, err := h.confirmed(operation, "smanage", arguments)
			var refused *application.SecretRefNotAllowedError
			if !errors.As(err, &refused) || len(h.calls) != 0 || h.store.gets != 0 || hasSecretCanary(err.Error()) {
				t.Fatalf("%s %q: err = %v", operation, ref, err)
			}
		}
	}
}

func mustJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestConnectionSecretFieldCollisionIsRefused(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{connectionsCreate.ID, fmt.Sprintf(`{"name":"n","connection_type":"t","fields":{"clientSecret":"plain"},"secret_ref":%q}`, releasedRef)},
		{connectionsSetData.ID, fmt.Sprintf(`{"connection_id":%d,"fields":{"clientId":"plain"},"secret_ref":%q}`, connectionID, releasedRef)},
	} {
		h := newSecretEnv(t, secretServer(false))
		_, err := h.confirmed(tt.operation, "smanage", tt.arguments)
		if !isInvalidRequest(err) || len(h.changing()) != 0 || hasSecretCanary(err.Error()) || hasSecretCanary(h.red.Error(err)) {
			t.Fatalf("%s: err = %v, calls = %v", tt.operation, err, h.last)
		}
	}
}

func TestConnectionSetDataRefusesAForeignTeamBeforeAnySecretAccess(t *testing.T) {
	h := newSecretEnv(t, secretServer(true))
	_, err := h.confirmed(connectionsSetData.ID, "smanage", secretArguments(connectionsSetData.ID))
	if !isInvalidRequest(err) || len(h.changing()) != 0 || h.store.gets != 0 ||
		strings.Contains(err.Error(), itoa64(foreignTeam)) {
		t.Fatalf("err = %v, calls = %v, store reads = %d", err, h.last, h.store.gets)
	}
}

func TestConnectionSecretToolsAreRefusedOnAScenarioAllowList(t *testing.T) {
	for _, operation := range connectionSecretTools {
		h := newSecretEnv(t, secretServer(false))
		_, err := h.confirmed(operation, "sscenario", secretArguments(operation))
		if !isInvalidRequest(err) || len(h.calls) != 0 || h.store.gets != 0 {
			t.Fatalf("%s: err = %v, calls = %v", operation, err, h.last)
		}
	}
}

func TestConnectionSecretArgumentsAreValidatedBeforeAnyRequest(t *testing.T) {
	long := strings.Repeat("a", 2000)
	manyFields := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		manyFields = append(manyFields, fmt.Sprintf(`"f%d":"v"`, i))
	}
	cases := map[string]struct{ operation, arguments string }{
		"empty name":      {connectionsCreate.ID, `{"name":" ","connection_type":"t"}`},
		"control name":    {connectionsCreate.ID, "{\"name\":\"a\\u0001b\",\"connection_type\":\"t\"}"},
		"long name":       {connectionsCreate.ID, `{"name":"` + strings.Repeat("n", 129) + `","connection_type":"t"}`},
		"bad type":        {connectionsCreate.ID, `{"name":"n","connection_type":"a/b"}`},
		"too many scopes": {connectionsCreate.ID, `{"name":"n","connection_type":"t","scopes":[` + strings.TrimSuffix(strings.Repeat(`"s",`, 51), ",") + `]}`},
		"control scope":   {connectionsCreate.ID, "{\"name\":\"n\",\"connection_type\":\"t\",\"scopes\":[\"a\\nb\"]}"},
		"reserved field":  {connectionsCreate.ID, `{"name":"n","connection_type":"t","fields":{"accountName":"x"}}`},
		"team field":      {connectionsCreate.ID, `{"name":"n","connection_type":"t","fields":{"teamId":9}}`},
		"bad field name":  {connectionsCreate.ID, `{"name":"n","connection_type":"t","fields":{"a b":"x"}}`},
		"object value":    {connectionsCreate.ID, `{"name":"n","connection_type":"t","fields":{"a":{"b":1}}}`},
		"null value":      {connectionsCreate.ID, `{"name":"n","connection_type":"t","fields":{"a":null}}`},
		"long value":      {connectionsCreate.ID, `{"name":"n","connection_type":"t","fields":{"a":"` + long + `"}}`},
		"many fields":     {connectionsCreate.ID, `{"name":"n","connection_type":"t","fields":{` + strings.Join(manyFields, ",") + `}}`},
		"free argument":   {connectionsCreate.ID, `{"name":"n","connection_type":"t","team_id":5}`},
		"password value":  {connectionsCreate.ID, `{"name":"n","connection_type":"t","password":"x"}`},
		"set no data":     {connectionsSetData.ID, fmt.Sprintf(`{"connection_id":%d}`, connectionID)},
		"set empty data":  {connectionsSetData.ID, fmt.Sprintf(`{"connection_id":%d,"fields":{}}`, connectionID)},
		"set bad id":      {connectionsSetData.ID, `{"connection_id":0,"fields":{"a":"b"}}`},
		"set long value":  {connectionsSetData.ID, fmt.Sprintf(`{"connection_id":%d,"fields":{"a":"%s"}}`, connectionID, long)},
	}
	for name, c := range cases {
		h := newSecretEnv(t, secretServer(false))
		_, err := h.confirmed(c.operation, "smanage", c.arguments)
		if err == nil || len(h.calls) != 0 || h.store.gets != 0 {
			t.Fatalf("%s: err = %v, calls = %v", name, err, h.last)
		}
	}
}

func TestConnectionSecretToolsAreSentOnceAndNeverRepeated(t *testing.T) {
	failures := map[string]func(*secretEnv, *http.Request) (*http.Response, error){
		"5xx": func(*secretEnv, *http.Request) (*http.Response, error) {
			return jsonResponse(503, `{"message":"`+foreignCanary+`"}`), nil
		},
		"abort": func(*secretEnv, *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection reset by peer")
		},
		"junk":  func(*secretEnv, *http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"empty": func(*secretEnv, *http.Request) (*http.Response, error) { return jsonResponse(200, `{}`), nil },
	}
	for name, respond := range failures {
		for _, operation := range connectionSecretTools {
			h := newSecretEnv(t, func(h *secretEnv, r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return secretServer(false)(h, r)
				}
				return respond(h, r)
			})
			_, err := h.confirmed(operation, "smanage", secretArguments(operation))
			if err == nil || len(h.changing()) != 1 || strings.Contains(err.Error(), foreignCanary) ||
				hasSecretCanary(err.Error()) || !strings.Contains(err.Error(), "; the ") {
				t.Fatalf("%s %s: err = %v, calls = %v", name, operation, err, h.last)
			}
		}
	}
}

func TestConnectionSecretToolsClassifyForbiddenAndTeamMismatch(t *testing.T) {
	for _, operation := range connectionSecretTools {
		h := newSecretEnv(t, func(h *secretEnv, r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return secretServer(false)(h, r)
			}
			return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
		})
		_, err := h.confirmed(operation, "smanage", secretArguments(operation))
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "connections:write") ||
			strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("%s: err = %v", operation, err)
		}
	}
	h := newSecretEnv(t, func(h *secretEnv, r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, foreignTeam, "x")+`}`), nil
	})
	_, err := h.confirmed(connectionsCreate.ID, "smanage", secretArguments(connectionsCreate.ID))
	if err == nil || !strings.Contains(err.Error(), "already created") || strings.Contains(err.Error(), itoa64(foreignTeam)) {
		t.Fatalf("team mismatch: %v", err)
	}
}

func TestConnectionSecretDescriptorsAndProfiles(t *testing.T) {
	for _, id := range connectionSecretTools {
		descriptor, _, ok := registry(t).Lookup(id)
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		refs := 0
		for _, argument := range descriptor.Arguments {
			if argument.SecretRef {
				refs++
			}
		}
		risk := descriptor.Risk
		if !descriptor.RequiresToolAllowList || risk.Confirmation != "required" || !risk.OpenWorld ||
			risk.DataSensitivity != connectionSecretsSensitivity || risk.Effect == "" || risk.Idempotency == "" ||
			refs != 1 {
			t.Fatalf("%s: %+v", id, descriptor)
		}
	}
	if !strings.Contains(connectionsSetData.Description, "REPLACE") ||
		!strings.Contains(connectionsSetData.Description, "reauthorized") ||
		!strings.Contains(connectionsCreate.Description, "OAuth") {
		t.Fatal("descriptions miss their warnings")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, tool := range p.Tools {
			if tool == connectionsCreate.ID || tool == connectionsSetData.ID {
				t.Fatalf("profile %s offers %s", p.ID, tool)
			}
		}
	}
}
