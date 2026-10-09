package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	webhookOne    = "aaaaaaaa-1111-2222-3333-444444444444"
	webhookTwo    = "bbbbbbbb-1111-2222-3333-444444444444"
	signingSecret = "whsec-canary-9f3a"
)

// webhookJSON is a webhook as Twenty returns it, with a signing secret and a target that carries credentials,
// a query, and a fragment.
func webhookJSON(id string) string {
	target := (&url.URL{Scheme: "https", User: url.UserPassword("user", "pw-canary"), Host: "hooks.example.com:8443",
		Path: "/in/events", RawQuery: "token=query-canary", Fragment: "frag-canary"}).String()
	return `{"id":"` + id + `","targetUrl":"` + target + `",` +
		`"operations":["person.created","*.*"],"description":"sync","secret":"` + signingSecret + `",` +
		`"applicationId":"` + webhookTwo + `","createdAt":"2025-01-02T03:04:05.000Z","updatedAt":"2025-01-03T03:04:05.000Z","deletedAt":null}`
}

func assertNoLeak(t *testing.T, label string, value any) {
	t.Helper()
	out, _ := json.Marshal(value)
	for _, canary := range []string{signingSecret, "pw-canary", "query-canary", "frag-canary", "user:", "secret", "targetUrl"} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s output contains %q: %s", label, canary, out)
		}
	}
}

type webhookCall struct{ Method, URI, Body string }

func serveWebhooks(t *testing.T, status int, answer string) *[]webhookCall {
	t.Helper()
	calls := &[]webhookCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		call := webhookCall{Method: request.Method, URI: request.URL.RequestURI()}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			call.Body = string(data)
		}
		*calls = append(*calls, call)
		return jsonResponse(status, answer), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func runWebhook(handler capability.Handler, args string, targets ...string) (any, error) {
	red := &redact.Redactor{}
	return handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
}

func webhookIDArgs(id string) string { return `{"id":"` + id + `"}` }

func TestWebhooksListAndGetProjectWithoutSecretOrURLDetail(t *testing.T) {
	calls := serveWebhooks(t, http.StatusOK, `[`+webhookJSON(webhookOne)+`]`)
	result, err := runWebhook(invokeWebhooksList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	assertNoLeak(t, "list", result)
	out, _ := json.Marshal(result)
	want := `{"webhooks":[{"id":"` + webhookOne + `","target":"https://hooks.example.com:8443/in/events",` +
		`"operations":["person.created","*.*"],"description":"sync","created_at":"2025-01-02T03:04:05.000Z",` +
		`"updated_at":"2025-01-03T03:04:05.000Z"}],"truncated":false}`
	if string(out) != want {
		t.Errorf("list = %s", out)
	}
	if len(*calls) != 1 || (*calls)[0].Method != http.MethodGet || (*calls)[0].URI != "/rest/webhooks" {
		t.Errorf("calls = %+v", *calls)
	}

	calls = serveWebhooks(t, http.StatusOK, webhookJSON(webhookOne))
	result, err = runWebhook(invokeWebhooksGet, webhookIDArgs(webhookOne))
	if err != nil {
		t.Fatal(err)
	}
	assertNoLeak(t, "get", result)
	if (*calls)[0].URI != "/rest/webhooks/"+webhookOne {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestWebhooksListIsCappedAndFlagged(t *testing.T) {
	items := make([]string, 0, webhooksListMax+1)
	for i := 0; i <= webhooksListMax; i++ {
		items = append(items, webhookJSON(webhookOne))
	}
	serveWebhooks(t, http.StatusOK, `[`+strings.Join(items, ",")+`]`)
	result, err := runWebhook(invokeWebhooksList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := result.(*WebhookList)
	if len(list.Webhooks) != webhooksListMax || !list.Truncated {
		t.Errorf("len = %d, truncated = %v", len(list.Webhooks), list.Truncated)
	}
}

func TestWebhooksOutputNeverEchoesUnexpectedProviderValues(t *testing.T) {
	body := `{"id":"` + webhookOne + `","targetUrl":"ftp://evil/` + signingSecret + `","operations":["person.created","x y","*.created"],` +
		`"description":"a\u0007b\nc","secret":"` + signingSecret + `","createdAt":"not a time","updatedAt":"2025-01-03T03:04:05Z"}`
	serveWebhooks(t, http.StatusOK, body)
	result, err := runWebhook(invokeWebhooksGet, webhookIDArgs(webhookOne))
	if err != nil {
		t.Fatal(err)
	}
	view := result.(*Webhook)
	if view.Target != "unknown" || strings.Join(view.Operations, ",") != "person.created,unknown,unknown" ||
		view.Description != "abc" || view.CreatedAt != "" {
		t.Errorf("view = %+v", view)
	}
	assertNoLeak(t, "get", result)
	for _, raw := range []string{"::notaurl", "https:///path", "mailto:x@example.com", "https://h/\x00"} {
		if got := webhookTarget(raw); got != "unknown" {
			t.Errorf("webhookTarget(%q) = %q", raw, got)
		}
	}
	long := strings.Repeat("d", webhookDescMax+50)
	if got := capWebhookText(long); len([]rune(got)) != webhookDescMax {
		t.Errorf("description length = %d", len([]rune(got)))
	}
}

func TestWebhooksGetNullIsNotFoundWithoutProviderText(t *testing.T) {
	for _, body := range []string{"null", ""} {
		serveWebhooks(t, http.StatusOK, body)
		_, err := runWebhook(invokeWebhooksGet, webhookIDArgs(webhookOne))
		if classOf(err) != provider.ClassNotFound {
			t.Errorf("body %q: err = %v", body, err)
		}
	}
	serveWebhooks(t, http.StatusNotFound, `{"messages":["`+bodyCanary+`"]}`)
	if _, err := runWebhook(invokeWebhooksGet, webhookIDArgs(webhookOne)); classOf(err) != provider.ClassNotFound ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("404: err = %v", err)
	}
}

func TestWebhooksUpdateSendsExactlyTheFixedBody(t *testing.T) {
	for _, tt := range []struct{ args, body string }{
		{`{"id":"` + webhookOne + `","operations":["person.created","person.created","*.*"]}`, `{"operations":["person.created","*.*"]}`},
		{`{"id":"` + webhookOne + `","description":"new text"}`, `{"description":"new text"}`},
		{`{"id":"` + webhookOne + `","operations":["company.updated"],"description":"d"}`, `{"operations":["company.updated"],"description":"d"}`},
	} {
		calls := serveWebhooks(t, http.StatusOK, webhookJSON(webhookOne))
		result, err := runWebhook(invokeWebhooksUpdate, tt.args)
		if err != nil {
			t.Fatal(err)
		}
		assertNoLeak(t, "update", result)
		if len(*calls) != 1 || (*calls)[0].Method != http.MethodPatch || (*calls)[0].URI != "/rest/webhooks/"+webhookOne ||
			(*calls)[0].Body != tt.body {
			t.Errorf("calls = %+v, want body %s", *calls, tt.body)
		}
	}
}

func TestWebhooksUpdateInputSchemaKnowsNeitherTargetNorSecret(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Additional bool                       `json:"additionalProperties"`
	}
	if err := json.Unmarshal(webhooksUpdate.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties["targetUrl"]; ok || schema.Additional || len(schema.Properties) != 3 {
		t.Errorf("properties = %v additional = %v", schema.Properties, schema.Additional)
	}
	for _, name := range []string{"secret", "target", "url"} {
		if _, ok := schema.Properties[name]; ok {
			t.Errorf("property %s", name)
		}
	}
	if len(webhooksUpdate.Arguments) != 3 {
		t.Errorf("arguments = %+v", webhooksUpdate.Arguments)
	}
}

func TestWebhooksUpdateRefusesBeforeAnyRequest(t *testing.T) {
	refuse(t)
	fifty := make([]string, 51)
	for i := range fifty {
		fifty[i] = `"p` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `.created"`
	}
	for _, args := range []string{
		`{"id":"` + webhookOne + `"}`,
		`{"id":"nope","operations":["person.created"]}`,
		`{"id":"` + webhookOne + `","operations":[]}`,
		`{"id":"` + webhookOne + `","operations":["person.created","person"]}`,
		`{"id":"` + webhookOne + `","operations":["Person.created"]}`,
		`{"id":"` + webhookOne + `","operations":["person.*"]}`,
		`{"id":"` + webhookOne + `","operations":["*.created"]}`,
		`{"id":"` + webhookOne + `","operations":["person.created\n"]}`,
		`{"id":"` + webhookOne + `","operations":[` + strings.Join(fifty, ",") + `]}`,
		`{"id":"` + webhookOne + `","description":""}`,
		`{"id":"` + webhookOne + `","description":"a\nb"}`,
		`{"id":"` + webhookOne + `","description":"` + strings.Repeat("x", webhookDescMax+1) + `"}`,
	} {
		_, err := invokeWebhooksUpdate(context.Background(), targetConnection(), countingResolver(t), &redact.Redactor{}, json.RawMessage(args))
		if !asInvalidOK(err) {
			t.Errorf("%.80s: err = %v", args, err)
		}
	}
}

func TestWebhookToolsNeedAConnectionWithoutObjectTargets(t *testing.T) {
	refuse(t)
	for name, handler := range map[string]capability.Handler{
		"list": invokeWebhooksList, "get": invokeWebhooksGet, "update": invokeWebhooksUpdate, "delete": invokeWebhooksDelete,
	} {
		args := `{"id":"` + webhookOne + `","operations":["person.created"]}`
		_, err := handler(context.Background(), targetConnection("object/person"), countingResolver(t), &redact.Redactor{}, json.RawMessage(args))
		if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWebhooksUpdateAndDeleteNeverRepeatAfterAnUnclearResult(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		fail   error
		body   string
	}{
		"server error": {status: 500, body: `{"error":"` + bodyCanary + `"}`},
		"gateway":      {status: 504},
		"unreadable":   {status: 200, body: `not json ` + bodyCanary},
		"timeout":      {fail: &net.DNSError{IsTimeout: true, Err: bodyCanary}},
		"connection":   {fail: errors.New(bodyCanary)},
	} {
		for action, call := range map[string]struct {
			handler capability.Handler
			args    string
		}{
			"update": {invokeWebhooksUpdate, `{"id":"` + webhookOne + `","description":"x"}`},
			"delete": {invokeWebhooksDelete, webhookIDArgs(webhookOne)},
		} {
			t.Run(name+" "+action, func(t *testing.T) {
				seen := 0
				serve(t, func(request *http.Request) (*http.Response, error) {
					seen++
					if tt.fail != nil {
						return nil, tt.fail
					}
					return jsonResponse(tt.status, tt.body), nil
				})
				stubLimiter(t, cloudKey)
				_, err := runWebhook(call.handler, call.args)
				if err == nil || seen != 1 {
					t.Fatalf("err = %v, requests = %d, want one", err, seen)
				}
				if strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("provider text in the error: %v", err)
				}
				var failure *provider.Error
				mayHave := errors.As(err, &failure) && (failure.Class != provider.ClassUnreachable || failure.MayHaveArrived())
				if mayHave && !strings.Contains(err.Error(), "before repeating") {
					t.Errorf("no uncertainty named: %v", err)
				}
			})
		}
	}
}

func TestWebhooksAnswerMustNameTheRequestedWebhook(t *testing.T) {
	serveWebhooks(t, http.StatusOK, webhookJSON(webhookTwo))
	_, err := runWebhook(invokeWebhooksGet, webhookIDArgs(webhookOne))
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("get: err = %v", err)
	}
	calls := serveWebhooks(t, http.StatusOK, webhookJSON(webhookTwo))
	_, err = runWebhook(invokeWebhooksUpdate, `{"id":"`+webhookOne+`","description":"x"}`)
	if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") || len(*calls) != 1 {
		t.Errorf("update: err = %v, calls = %d", err, len(*calls))
	}
	serveWebhooks(t, http.StatusOK, `{"id":"nope"}`)
	if _, err = runWebhook(invokeWebhooksGet, webhookIDArgs(webhookOne)); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("bad id: err = %v", err)
	}
	for _, answer := range []string{"false", "null", `{"id":"` + webhookOne + `"}`} {
		calls = serveWebhooks(t, http.StatusOK, answer)
		_, err = runWebhook(invokeWebhooksDelete, webhookIDArgs(webhookOne))
		if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") || len(*calls) != 1 {
			t.Errorf("delete %s: err = %v", answer, err)
		}
	}
}

func TestWebhooksDeleteSendsOneDeleteAndConfirms(t *testing.T) {
	calls := serveWebhooks(t, http.StatusOK, `true`)
	result, err := runWebhook(invokeWebhooksDelete, webhookIDArgs(webhookOne))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	if string(out) != `{"deleted":true}` || len(*calls) != 1 || (*calls)[0].Method != http.MethodDelete ||
		(*calls)[0].URI != "/rest/webhooks/"+webhookOne || (*calls)[0].Body != "" {
		t.Errorf("result = %s, calls = %+v", out, *calls)
	}
}

func TestWebhooksPermissionNamesTheSettingsRight(t *testing.T) {
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"list":   {invokeWebhooksList, `{}`},
		"get":    {invokeWebhooksGet, webhookIDArgs(webhookOne)},
		"update": {invokeWebhooksUpdate, `{"id":"` + webhookOne + `","description":"x"}`},
		"delete": {invokeWebhooksDelete, webhookIDArgs(webhookOne)},
	} {
		serveWebhooks(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
		_, err := runWebhook(call.handler, call.args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "API keys and webhooks") ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWebhooksDescriptorsDeclareTheirRiskAndProfiles(t *testing.T) {
	for _, tt := range []struct {
		d         capability.Descriptor
		effect    capability.Effect
		confirm   capability.Confirmation
		idem      capability.Idempotency
		allowList bool
	}{
		{webhooksList, capability.EffectRead, capability.ConfirmationNone, capability.IdempotencySafe, false},
		{webhooksGet, capability.EffectRead, capability.ConfirmationNone, capability.IdempotencySafe, false},
		{webhooksUpdate, capability.EffectUpdate, capability.ConfirmationRequired, capability.IdempotencyIdempotent, false},
		{webhooksDelete, capability.EffectDelete, capability.ConfirmationRequired, capability.IdempotencyIdempotent, true},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idem, Confirmation: tt.confirm, OpenWorld: true,
			DataSensitivity: "twentycrm-webhook-config"}
		if tt.d.Risk != want || tt.d.RequiresToolAllowList != tt.allowList || tt.d.Provider != Provider {
			t.Errorf("%s = %+v", tt.d.ID, tt.d)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == webhooksUpdate.ID || id == webhooksDelete.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
	if !strings.Contains(strings.Join(readTools, " "), webhooksList.ID) || !strings.Contains(strings.Join(readTools, " "), webhooksGet.ID) {
		t.Errorf("readTools = %v", readTools)
	}
}

func TestWebhooksDeleteAndUpdateNeedConfirmationAndDeleteTheAllowList(t *testing.T) {
	serveWebhooks(t, http.StatusOK, `true`)
	cfg := coreConfig()
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	bare := cfg.Connections["crm"]
	bare.Permissions = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Permissions: all, Tools: []string{webhooksDelete.ID, webhooksUpdate.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	request := application.InvokeRequest{Operation: webhooksDelete.ID, Connection: "crm",
		Arguments: json.RawMessage(webhookIDArgs(webhookOne)), Confirmed: true}
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Errorf("delete was offered without a tools list")
	}
	for _, operation := range []string{webhooksDelete.ID, webhooksUpdate.ID} {
		request = application.InvokeRequest{Operation: operation, Connection: "crm-internal",
			Arguments: json.RawMessage(`{"id":"` + webhookOne + `","description":"x"}`)}
		if operation == webhooksDelete.ID {
			request.Arguments = json.RawMessage(webhookIDArgs(webhookOne))
		}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirmation", operation)
		}
	}
}
