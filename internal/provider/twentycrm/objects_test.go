package twentycrm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The 33 standard objects Twenty marks isSystem: true in twentyhq/twenty, commit
// 46fc01c38374c2b489b0d4755719da2d1e5408ac, file
// packages/twenty-server/src/engine/workspace-manager/twenty-standard-application/utils/object-metadata/
// create-standard-flat-object-metadata.util.ts. The same file lists company, dashboard, note, opportunity,
// person, task, and workflow without isSystem; workflow is added to the system list on purpose.
var twentySystemObjects = []string{
	"agentChatThread", "agentChatThreadParticipant", "agentChatThreadTarget", "agentMessage", "agentMessagePart",
	"agentTurn", "attachment", "blocklist", "calendarChannelEventAssociation", "calendarEvent",
	"calendarEventParticipant", "calendarEventTarget", "callRecording", "campaignDelivery", "message",
	"messageCampaign", "messageChannelMessageAssociation", "messageChannelMessageAssociationMessageFolder",
	"messageList", "messageListMember", "messageParticipant", "messageSuppression", "messageThread",
	"messageThreadTarget", "noteTarget", "recordShare", "shortLink", "taskTarget", "timelineActivity",
	"workflowAutomatedTrigger", "workflowRun", "workflowVersion", "workspaceMember",
}

func TestSystemObjectsMatchTheTwentySource(t *testing.T) {
	if len(twentySystemObjects) != 33 {
		t.Fatalf("source list has %d entries, want 33", len(twentySystemObjects))
	}
	want := append(append([]string{}, twentySystemObjects...), "workflow")
	sort.Strings(want)
	got := make([]string, 0, len(systemObjects))
	for name := range systemObjects {
		got = append(got, name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("systemObjects = %v, want %v", got, want)
	}
	for _, name := range []string{"company", "person", "opportunity", "note", "task", "dashboard"} {
		if systemObjects[name] {
			t.Errorf("%s is a system object", name)
		}
	}
}

func targetConnection(targets ...string) *config.Resolved {
	resolved := resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin)
	resolved.Targets = targets
	return resolved
}

func TestObjectTargetsAreValidatedWithoutQuotingTheValue(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	target := metadata.Target
	if target.Required || !target.Multiple || target.Wildcard != "" || len(target.Kinds) != 1 ||
		!reflect.DeepEqual(target.Kinds[0].Forms, []string{"object/NAME"}) {
		t.Fatalf("target metadata = %+v", target)
	}
	const canary = "Zz-canary-8841"
	for _, valid := range []string{"object/company", "object/person", "object/rocket", "object/a1B2"} {
		if err := target.Validate(valid); err != nil {
			t.Errorf("Validate(%q) = %v", valid, err)
		}
	}
	invalid := []string{
		"*", "object/*", "object/", "object/Person", "object/" + canary, "object/a b", "object/" + strings.Repeat("a", 64),
		"table/12", "workspace-" + canary, canary, "object/person/extra", "object/workspaceMember", "object/workflow",
		"object/attachment",
	}
	for _, value := range invalid {
		err := target.Validate(value)
		if err == nil {
			t.Errorf("Validate(%q) accepted the value", value)
			continue
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("Validate error quotes the value: %v", err)
		}
	}
	if err := target.Validate("workspace-" + canary); err == nil || !strings.Contains(err.Error(), "object/NAME") {
		t.Errorf("an old free value gets no hint at the form: %v", err)
	}
	if err := target.ValidateSet([]string{"object/person", "object/company"}); err != nil {
		t.Errorf("ValidateSet() = %v", err)
	}
	if err := target.ValidateSet([]string{"object/person", "object/person"}); err == nil {
		t.Error("ValidateSet() accepted a duplicate")
	}
	if err := target.ValidateSet([]string{"object/person", "*"}); err == nil {
		t.Error("ValidateSet() accepted a wildcard")
	}
}

func catalogOf(t *testing.T) (*catalog, *int) {
	t.Helper()
	reads := 0
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != schemaPath {
			t.Errorf("unexpected request %s", request.URL.Path)
		}
		reads++
		return jsonResponse(http.StatusOK, schemaBody), nil
	})
	c, _ := client(t)
	cat, err := c.workspaceCatalog(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := c.workspaceCatalog(context.Background(), "test"); err != nil || again != cat {
		t.Fatalf("second catalog = %v, %v", again, err)
	}
	return cat, &reads
}

func namesOf(objects []catalogObject) []string {
	names := []string{}
	for _, object := range objects {
		names = append(names, object.Name)
	}
	return names
}

func TestCatalogWithoutTargetsHoldsEveryNonSystemObject(t *testing.T) {
	cat, reads := catalogOf(t)
	if *reads != 1 {
		t.Errorf("document reads = %d, want 1", *reads)
	}
	if got := namesOf(cat.reachable(scope{})); !reflect.DeepEqual(got, []string{"company", "person", "rocket"}) {
		t.Errorf("reachable = %v", got)
	}
	if _, ok := cat.object("workspaceMember"); !ok {
		t.Error("the system object is missing from the raw catalog")
	}
	company, _ := cat.object("company")
	if company.Plural != "companies" {
		t.Errorf("company plural = %q", company.Plural)
	}
	fields := map[string]catalogField{}
	for _, field := range company.Fields {
		fields[field.Name] = field
	}
	if f := fields["name"]; !f.Required || !f.Writable || f.Type != "string" {
		t.Errorf("name = %+v", f)
	}
	if f := fields["createdAt"]; f.Writable || f.Required || f.Format != "date-time" {
		t.Errorf("createdAt = %+v", f)
	}
	if f := fields["domainName"]; f.Type != "object" || len(f.Subfields) != 2 || f.Subfields[0].Name != "primaryLinkLabel" {
		t.Errorf("domainName = %+v", f)
	}
	if f := fields["people"]; f.Relation != "person" || f.Type != "array" {
		t.Errorf("people = %+v", f)
	}
	rocket, _ := cat.object("rocket")
	for _, field := range rocket.Fields {
		if field.Name == "payload" && field.Type != "number" {
			t.Errorf("payload = %+v", field)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", cat.reachable(scope{})), descriptionCanary) {
		t.Error("a description reached the catalog")
	}
}

func TestCatalogProjectionFollowsTheBoundObjects(t *testing.T) {
	cat, _ := catalogOf(t)
	bound := scope{objects: []string{"person"}}
	reachable := cat.reachable(bound)
	if got := namesOf(reachable); !reflect.DeepEqual(got, []string{"person"}) {
		t.Fatalf("reachable = %v", got)
	}
	for _, field := range reachable[0].Fields {
		if field.Name == "company" || field.Name == "workspaceMember" {
			t.Errorf("relation into an unreachable object stays: %+v", field)
		}
	}
	if _, err := cat.resolve(bound, "company"); err == nil {
		t.Error("resolve() reached an unbound object")
	}
	if _, err := cat.resolve(scope{}, "workspaceMember"); err == nil {
		t.Error("resolve() reached a system object")
	}
	if _, err := cat.resolve(scope{}, "ghost"); classOf(err) != provider.ClassNotFound {
		t.Errorf("resolve(ghost) = %v", err)
	}
	if object, err := cat.resolve(bound, "person"); err != nil || object.Plural != "people" {
		t.Errorf("resolve(person) = %v, %v", object, err)
	}
}

// countingResolver fails the test when a secret is read.
func countingResolver(t *testing.T) *secret.Resolver {
	t.Helper()
	return secret.NewWith(func(string) string {
		t.Error("the secret was read")
		return ""
	}, nil, nil, &redact.Redactor{})
}

func TestCompanyToolsNeedTheCompanyObjectBeforeSecretAccess(t *testing.T) {
	refuse(t)
	handlers := companyHandlers()
	if len(handlers) != 7 {
		t.Fatalf("handlers = %d", len(handlers))
	}
	for id, handler := range handlers {
		t.Run(id, func(t *testing.T) {
			_, err := handler(context.Background(), targetConnection("object/person"), countingResolver(t),
				&redact.Redactor{}, json.RawMessage(`{"id":"`+companyID+`","name":"x"}`))
			var refusal *provider.InvalidRequestError
			if !asInvalid(err, &refusal) {
				t.Fatalf("err = %v, want invalid-request", err)
			}
			if strings.Contains(err.Error(), "company") || strings.Contains(err.Error(), "person") {
				t.Errorf("the refusal names an object: %v", err)
			}
		})
	}
}

func companyHandlers() map[string]capability.Handler {
	return map[string]capability.Handler{
		companiesList.ID: invokeCompaniesList, companiesGet.ID: invokeCompaniesGet,
		companiesCreate.ID: invokeCompaniesCreate, companiesUpdate.ID: invokeCompaniesUpdate,
		companiesDelete.ID: invokeCompaniesDelete, companiesDestroy.ID: invokeCompaniesDestroy,
		companiesRestore.ID: invokeCompaniesRestore,
	}
}

func asInvalid(err error, target **provider.InvalidRequestError) bool {
	refusal, ok := err.(*provider.InvalidRequestError)
	*target = refusal
	return ok
}

func TestCompanyToolsWorkWhenTheCompanyObjectIsBound(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, listBody), nil
	})
	stubLimiter(t, cloudKey)
	operation := companyHandlers()["twentycrm.companies.list"]
	red := &redact.Redactor{}
	if _, err := operation(context.Background(), targetConnection("object/person", "object/company"),
		resolver(red), red, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("list with company bound = %v", err)
	}
	if _, err := operation(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("list without targets = %v", err)
	}
}

func TestClientMethodsKeepTheObjectBinding(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	c, err := open(context.Background(), targetConnection("object/person"), resolver(red), red, freeLimiter())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListCompanies(context.Background(), ListOptions{}); err == nil {
		t.Error("ListCompanies reached the provider")
	}
	if _, err := c.GetCompany(context.Background(), companyID); err == nil {
		t.Error("GetCompany reached the provider")
	}
	if err := c.DestroyCompany(context.Background(), companyID); err == nil {
		t.Error("DestroyCompany reached the provider")
	}
}

func TestWorkspaceGateRefusesObjectTargetsBeforeSecretAccess(t *testing.T) {
	refuse(t)
	if err := requireWorkspaceScope(targetConnection("object/person")); err == nil ||
		!asInvalidOK(err) || strings.Contains(err.Error(), "person") {
		t.Errorf("requireWorkspaceScope(targets) = %v", err)
	}
	if err := requireWorkspaceScope(targetConnection()); err != nil {
		t.Errorf("requireWorkspaceScope(no targets) = %v", err)
	}
	if err := selectObject(targetConnection("object/person"), "workspaceMember"); err == nil {
		t.Error("selectObject accepted a system object")
	}
	if err := selectObject(targetConnection(), "workspaceMember"); err == nil {
		t.Error("selectObject accepted a system object without targets")
	}
	if err := selectObject(targetConnection(), "../x"); err == nil {
		t.Error("selectObject accepted a malformed name")
	}
	if err := selectObject(targetConnection(), "rocket"); err != nil {
		t.Errorf("selectObject(rocket) = %v", err)
	}
	if err := selectObject(targetConnection("object/person"), "rocket"); err == nil {
		t.Error("selectObject accepted an unbound object")
	}
}

func asInvalidOK(err error) bool {
	var refusal *provider.InvalidRequestError
	return asInvalid(err, &refusal)
}

func TestTestConnectionChecksTheBoundObjects(t *testing.T) {
	t.Run("a bound object is read through its plural", func(t *testing.T) {
		requests := []string{}
		serve(t, func(request *http.Request) (*http.Response, error) {
			requests = append(requests, request.URL.Path+"?"+request.URL.RawQuery)
			if request.URL.Path == schemaPath {
				return jsonResponse(http.StatusOK, schemaBody), nil
			}
			return jsonResponse(http.StatusOK, `{"data":{"people":[]}}`), nil
		})
		stubLimiter(t, cloudKey)
		class, err := TestConnection(context.Background(), targetConnection("object/person"), resolver(nil), nil)
		if err != nil || class != provider.ClassOK {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
		want := []string{"/open-api/core?", "/rest/people?depth=0&limit=1"}
		if !reflect.DeepEqual(requests, want) {
			t.Errorf("requests = %v, want %v", requests, want)
		}
	})
	t.Run("a bound object the workspace lacks is unusable", func(t *testing.T) {
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != schemaPath {
				t.Errorf("unexpected request %s", request.URL.Path)
			}
			return jsonResponse(http.StatusOK, schemaBody), nil
		})
		stubLimiter(t, cloudKey)
		class, err := TestConnection(context.Background(), targetConnection("object/person", "object/ghost"), resolver(nil), nil)
		if err == nil || class != "" || strings.Contains(err.Error(), "ghost") {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
	})
	t.Run("an unreadable bound object reports its class", func(t *testing.T) {
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == schemaPath {
				return jsonResponse(http.StatusOK, schemaBody), nil
			}
			return jsonResponse(http.StatusForbidden, `{}`), nil
		})
		stubLimiter(t, cloudKey)
		class, err := TestConnection(context.Background(), targetConnection("object/rocket"), resolver(nil), nil)
		if err != nil || class != provider.ClassPermission {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
	})
	t.Run("an empty workspace document means an unusable key", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"paths":{},"components":{"schemas":{}}}`), nil
		})
		stubLimiter(t, cloudKey)
		class, err := TestConnection(context.Background(), targetConnection("object/person"), resolver(nil), nil)
		if err != nil || class != provider.ClassAuth {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
	})
}

func TestRateLimitHoldsForRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var slept []time.Duration
	limiter := ratelimit.New(0, func() time.Time { return now },
		func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil })
	serve(t, func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusTooManyRequests, `{"error":"`+bodyCanary+`"}`)
		response.Header.Set("Retry-After", "7")
		return response, nil
	})
	red := &redact.Redactor{}
	c, err := open(context.Background(), targetConnection(), resolver(red), red, limiter)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListCompanies(context.Background(), ListOptions{})
	if classOf(err) != provider.ClassRateLimited || strings.Contains(err.Error(), bodyCanary) {
		t.Fatalf("err = %v", err)
	}
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] < 7*time.Second {
		t.Errorf("slept = %v, want a hold of 7s", slept)
	}
}
