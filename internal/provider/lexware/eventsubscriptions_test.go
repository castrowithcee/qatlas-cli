package lexware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	subscriptionID = "0f4a6c1e-5b7d-4c8e-9a3b-2d1e0f9a8b7c"
	hookCanary     = "hooktoken-canary"
)

func subscriptionBody(id, callback string) string {
	return `{"subscriptionId":"` + id + `","organizationId":"org-1","createdDate":"2026-01-01T00:00:00.000+01:00",` +
		`"eventType":"invoice.created","callbackUrl":` + fmt.Sprintf("%q", callback) + `}`
}

// callbackWithCredentials builds the URL at run time so that no credential literal sits in the source.
func callbackWithCredentials(host, rawQuery, fragment string) string {
	return (&url.URL{Scheme: "https", User: url.UserPassword("user", "secretpass"), Host: host,
		Path: "/path/" + hookCanary, RawQuery: rawQuery, Fragment: fragment}).String()
}

func assertNoCallbackLeak(t *testing.T, value any) {
	t.Helper()
	out, _ := json.Marshal(value)
	for _, leak := range []string{hookCanary, "user", "secretpass", "query", "frag", "8443", "/path"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("output leaks %q: %s", leak, out)
		}
	}
}

func TestListEventSubscriptionsMasksCallbackTargets(t *testing.T) {
	body := `{"content":[` +
		subscriptionBody(subscriptionID, callbackWithCredentials("hooks.example.com:8443", "query=1", "frag")) + `,` +
		subscriptionBody("11111111-2222-3333-4444-555555555555", "not a url "+hookCanary) + `,` +
		subscriptionBody("22222222-2222-3333-4444-555555555555", "ftp://hooks.example.com/"+hookCanary) + `]}`
	calls := serveOne(t, "/v1/event-subscriptions", body)
	c, _ := client(t)
	items, truncated, err := c.ListEventSubscriptions(context.Background())
	if err != nil || truncated || calls.Load() != 1 || len(items) != 3 {
		t.Fatalf("list = %+v, %v, %v, calls %d", items, truncated, err, calls.Load())
	}
	if items[0].CallbackTarget != "https://hooks.example.com/…" || items[0].EventType != "invoice.created" ||
		items[0].ID != subscriptionID || items[1].CallbackTarget != maskedCallback || items[2].CallbackTarget != maskedCallback {
		t.Errorf("items = %+v", items)
	}
	assertNoCallbackLeak(t, items)
}

func TestListEventSubscriptionsCapsAtTwoHundred(t *testing.T) {
	var entries []string
	for i := 0; i < maxEventSubscriptions+5; i++ {
		entries = append(entries, subscriptionBody(fmt.Sprintf("00000000-0000-4000-8000-%012d", i), "https://h.example.com/x"))
	}
	serveOne(t, "/v1/event-subscriptions", `{"content":[`+strings.Join(entries, ",")+`]}`)
	c, _ := client(t)
	items, truncated, err := c.ListEventSubscriptions(context.Background())
	if err != nil || !truncated || len(items) != maxEventSubscriptions {
		t.Fatalf("len = %d, truncated = %v, err = %v", len(items), truncated, err)
	}
}

func TestListEventSubscriptionsRejectsAnUnusableIdentifier(t *testing.T) {
	serveOne(t, "/v1/event-subscriptions", `{"content":[`+subscriptionBody("../x", "https://h.example.com/")+`]}`)
	c, _ := client(t)
	if _, _, err := c.ListEventSubscriptions(context.Background()); err == nil || classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v, want invalid response", err)
	}
}

func TestGetEventSubscriptionReadsOneAndChecksTheAnswerIdentifier(t *testing.T) {
	serveOne(t, "/v1/event-subscriptions/"+subscriptionID,
		subscriptionBody(subscriptionID, callbackWithCredentials("hooks.example.com", "query=1", "")))
	c, _ := client(t)
	got, err := c.GetEventSubscription(context.Background(), subscriptionID)
	if err != nil || got.ID != subscriptionID || got.CallbackTarget != "https://hooks.example.com/…" {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
	assertNoCallbackLeak(t, got)

	serveOne(t, "/v1/event-subscriptions/"+subscriptionID,
		subscriptionBody("11111111-2222-3333-4444-555555555555", "https://h.example.com/"))
	c, _ = client(t)
	if _, err := c.GetEventSubscription(context.Background(), subscriptionID); err == nil || classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("mismatching answer err = %v, want invalid response", err)
	}
}

func TestGetEventSubscriptionNotFoundNamesNoIdentifier(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(404, `{"message":"`+bodyCanary+`"}`), nil
	})
	c, _ := client(t)
	_, err := c.GetEventSubscription(context.Background(), subscriptionID)
	if err == nil || classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), subscriptionID) ||
		strings.Contains(err.Error(), bodyCanary) || !strings.Contains(err.Error(), resourceEventSubscription) {
		t.Errorf("err = %v", err)
	}
}

func TestEventSubscriptionIdentifiersAreCheckedBeforeAnyRequest(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	if _, err := c.GetEventSubscription(context.Background(), "../v1/profile"); err == nil {
		t.Error("get accepted a non-UUID")
	}
	if _, err := c.DeleteEventSubscription(context.Background(), "../v1/profile"); err == nil {
		t.Error("delete accepted a non-UUID")
	}
}

func TestDeleteEventSubscriptionSendsOneDeleteToTheFixedPath(t *testing.T) {
	requests := scripted(t, storedGet, deleteAnswer)
	c, _ := client(t)
	got, err := c.DeleteEventSubscription(context.Background(), subscriptionID)
	if err != nil || got == nil || got.ID != subscriptionID || !got.Deleted {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
	if len(*requests) != 1 || (*requests)[0].method != http.MethodDelete ||
		(*requests)[0].path != "/v1/event-subscriptions/"+subscriptionID {
		t.Errorf("requests = %+v, want exactly one DELETE", *requests)
	}
}

func TestDeleteEventSubscriptionFailuresHintOnlyWhenTheOutcomeIsUnclear(t *testing.T) {
	tests := []struct {
		name  string
		reply func() (*http.Response, error)
		class provider.Class
		hint  bool
	}{
		{"404", func() (*http.Response, error) { return jsonResponse(404, `{"message":"`+bodyCanary+`"}`), nil }, provider.ClassNotFound, false},
		{"500", func() (*http.Response, error) { return jsonResponse(500, `{"message":"`+bodyCanary+`"}`), nil }, provider.ClassProviderError, true},
		{"timeout", func() (*http.Response, error) { return nil, context.DeadlineExceeded }, provider.ClassTimeout, true},
		{"403", func() (*http.Response, error) { return jsonResponse(403, "{}"), nil }, provider.ClassPermission, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := scripted(t, storedGet, tt.reply)
			c, _ := client(t)
			_, err := c.DeleteEventSubscription(context.Background(), subscriptionID)
			if err == nil || classOf(err) != tt.class || strings.Contains(err.Error(), bodyCanary) ||
				strings.Contains(err.Error(), subscriptionID) {
				t.Fatalf("err = %v, want class %q", err, tt.class)
			}
			if got := strings.Contains(err.Error(), eventSubscriptionMayBeDeleted); got != tt.hint {
				t.Errorf("hint = %v, want %v (%v)", got, tt.hint, err)
			}
			if len(*requests) != 1 {
				t.Errorf("requests = %d, want exactly one", len(*requests))
			}
		})
	}
}

func TestEventSubscriptionToolsAreDeclaredAsContracted(t *testing.T) {
	reg := registry(t)
	seen := map[string]bool{}
	for _, d := range reg.Provider(Provider) {
		if strings.Contains(d.ID, "eventsubscriptions") {
			seen[d.ID] = true
			if !d.Risk.OpenWorld || d.Risk.DataSensitivity != integrationSensitivity {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
			isDelete := strings.HasSuffix(d.ID, ".delete")
			if isDelete != (d.Risk.Effect == capability.EffectDelete) || isDelete != d.RequiresToolAllowList ||
				isDelete != (d.Risk.Confirmation == capability.ConfirmationRequired) ||
				(isDelete && d.Risk.Idempotency != capability.IdempotencyIdempotent) ||
				(!isDelete && (d.Risk.Effect != capability.EffectRead || d.Risk.Idempotency != capability.IdempotencySafe)) {
				t.Errorf("%s = %+v", d.ID, d)
			}
		}
		// Creating a subscription is deliberately not offered.
		if strings.Contains(d.ID, "eventsubscription") && !strings.HasSuffix(d.ID, ".list") &&
			!strings.HasSuffix(d.ID, ".get") && !strings.HasSuffix(d.ID, ".delete") {
			t.Errorf("unexpected subscription tool %s", d.ID)
		}
	}
	if len(seen) != 3 {
		t.Errorf("tools = %v, want list, get and delete", seen)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		has := map[string]bool{}
		for _, id := range profile.Tools {
			has[id] = true
		}
		if !has[eventSubscriptionsList.ID] || !has[eventSubscriptionsGet.ID] || has[eventSubscriptionsDelete.ID] {
			t.Errorf("profile %s tools = %v", profile.ID, profile.Tools)
		}
	}
}

func TestNoCodePathPostsToEventSubscriptions(t *testing.T) {
	// The sending helpers take the method explicitly; no source file may combine POST with the path.
	entries, err := filepathGlob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data := readFile(t, name)
		for _, line := range strings.Split(data, "\n") {
			if strings.Contains(line, "event-subscriptions") && (strings.Contains(line, "post(") ||
				strings.Contains(line, "MethodPost") || strings.Contains(line, "put(")) {
				t.Errorf("%s writes to event subscriptions: %s", name, line)
			}
		}
	}
}

func TestDeleteEventSubscriptionIsOfferedOnlyThroughAToolEntry(t *testing.T) {
	reg := registry(t)
	stubLimiter(t, primaryKey)
	var methods []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		return jsonResponse(http.StatusNoContent, ""), nil
	})
	cfg := coreConfig()
	cfg.Connections["lexware-plain"] = config.Connection{Service: "lexware", Credential: "primary-key",
		Permissions: []config.Permission{config.PermissionRead, config.PermissionDelete}}
	cfg.Connections["lexware-deleter"] = config.Connection{Service: "lexware", Credential: "primary-key",
		Permissions: []config.Permission{config.PermissionDelete}, Tools: []string{"lexware.eventsubscriptions.delete"}}
	red := &redact.Redactor{}
	core := application.New(reg, cfg, resolver(red), red)
	invoke := func(connection, id string, confirm bool) error {
		_, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: "lexware.eventsubscriptions.delete", Connection: connection,
			Arguments: json.RawMessage(`{"id":"` + id + `"}`), Confirmed: confirm})
		return err
	}
	for _, connection := range []string{"lexware-plain", "lexware-primary"} {
		if invoke(connection, subscriptionID, true) == nil {
			t.Errorf("%s offered the delete tool without a tools entry", connection)
		}
	}
	if invoke("lexware-deleter", subscriptionID, false) == nil {
		t.Error("delete without confirm was accepted")
	}
	if invoke("lexware-deleter", "../v1/contacts/"+bodyCanary, true) == nil {
		t.Error("an identifier that is no UUID was accepted")
	}
	if len(methods) != 0 {
		t.Fatalf("requests before a valid call = %v, want none", methods)
	}
	if err := invoke("lexware-deleter", subscriptionID, true); err != nil {
		t.Fatalf("delete on a connection that names it = %v", err)
	}
	if len(methods) != 1 || methods[0] != http.MethodDelete {
		t.Errorf("methods = %v, want one DELETE", methods)
	}
}

func filepathGlob(pattern string) ([]string, error) { return filepath.Glob(pattern) }

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
