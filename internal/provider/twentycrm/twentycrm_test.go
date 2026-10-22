package twentycrm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The canaries stand for an API key, for a workspace-specific custom field, and for provider content. No
// test reaches a productive workspace: every request is answered by the package's own transport seam.
const (
	cloudKey     = "canary-twenty-cloud-key-7be31d"
	internalKey  = "canary-twenty-internal-key-4c0a92"
	cloudEnv     = "TEST_TWENTY_CLOUD_KEY"
	internalEnv  = "TEST_TWENTY_INTERNAL_KEY"
	companyID    = "8f14e45f-ceea-467a-9e60-3b4b5e0f0e21"
	otherID      = "2b6f0cc9-04bb-4bd8-8e19-6ff4c7a2f0d3"
	bodyCanary   = "provider-body-canary-twenty-9d4f"
	customCanary = "custom-field-canary-twenty-6a71"
	selfHosted   = "https://crm.example.invalid"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// serve replaces the package transport for one test. Production keeps Go's default transport.
func serve(t *testing.T, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	previous := transport
	transport = roundTripFunc(handler)
	t.Cleanup(func() { transport = previous })
}

// refuse fails the test when any provider request is attempted.
func refuse(t *testing.T) {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", request.Method, request.URL.Path)
		return nil, errors.New("no request expected")
	})
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func resolvedConnection(name, credential, env, origin string) *config.Resolved {
	return &config.Resolved{
		Name: name, Provider: Provider, BaseURL: origin, Service: "twenty", Credential: credential,
		Secrets: config.Credential{
			Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: env},
		},
	}
}

func resolver(red *redact.Redactor) *secret.Resolver {
	return secret.NewWith(func(name string) string {
		switch name {
		case cloudEnv:
			return cloudKey
		case internalEnv:
			return internalKey
		}
		return ""
	}, nil, nil, red)
}

// freeLimiter is a limiter that never waits. The rate limit itself is proven by its own test.
func freeLimiter() *ratelimit.Limiter {
	return ratelimit.New(0, time.Now, ratelimit.Sleep)
}

// client opens the cloud connection with the package transport currently installed.
func client(t *testing.T) (*Client, *redact.Redactor) {
	t.Helper()
	red := &redact.Redactor{}
	c, err := open(context.Background(), resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin), resolver(red), red,
		freeLimiter())
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	return c, red
}

// listBody carries a custom field and a company whose link label is empty, so the stable projection can be
// proven to drop the one and to fall back for the other.
const listBody = `{
  "data": {"companies": [
    {"id":"8f14e45f-ceea-467a-9e60-3b4b5e0f0e21","name":"Bike & Ride GmbH",
     "domainName":{"primaryLinkLabel":"bike-ride.example","primaryLinkUrl":"https://bike-ride.example",
      "secondaryLinks":[{"label":"shop","url":"https://shop.bike-ride.example"}]},
     "createdAt":"2026-05-14T16:52:21.000Z","updatedAt":"2026-06-02T08:11:03.000Z",
     "deletedAt":null,"position":1,"annualRevenue":{"amountMicros":1000,"currencyCode":"EUR"},
     "riskScore":"` + customCanary + `"},
    {"id":"2b6f0cc9-04bb-4bd8-8e19-6ff4c7a2f0d3","name":"Test GmbH",
     "domainName":{"primaryLinkLabel":"","primaryLinkUrl":"https://test.example","secondaryLinks":null},
     "createdAt":"2026-01-09T10:00:00.000Z","updatedAt":"2026-01-09T10:00:00.000Z"}
  ]},
  "totalCount": 57,
  "pageInfo": {"hasNextPage": true, "hasPreviousPage": false, "startCursor": "c0", "endCursor": "cursor-page-2"}
}`

const companyBody = `{
  "data": {"company": {
    "id":"8f14e45f-ceea-467a-9e60-3b4b5e0f0e21","name":"Bike & Ride GmbH",
    "domainName":{"primaryLinkLabel":"bike-ride.example","primaryLinkUrl":"https://bike-ride.example",
     "secondaryLinks":[]},
    "createdAt":"2026-05-14T16:52:21.000Z","updatedAt":"2026-06-02T08:11:03.000Z","deletedAt":null,
    "riskScore":"` + customCanary + `"
  }}
}`

// schemaBody is the workspace document of a compatible workspace, shortened: company, person, a custom
// object (rocket), and a system object (workspaceMember). Descriptions carry a canary that objects.get
// reports and that no other tool may show.
const schemaBody = `{
  "openapi":"3.1.0",
  "paths":{
    "/companies":{"post":{"operationId":"createOneCompany"}},
    "/companies/{id}":{"get":{}},
    "/people":{"post":{"operationId":"createOnePerson"}},
    "/rockets":{"post":{"operationId":"createOneRocket"}},
    "/workspaceMembers":{"post":{"operationId":"createOneWorkspaceMember"}},
    "/batch/companies":{"post":{"operationId":"createManyCompanies"}}
  },
  "components":{"schemas":{
    "Company":{"type":"object","required":["name"],"properties":{"name":{"type":"string"},
      "domainName":{"type":"object","properties":{"primaryLinkUrl":{"type":"string"},"primaryLinkLabel":{"type":"string"}}},
      "riskScore":{"type":"string","description":"` + descriptionCanary + `"}}},
    "CompanyForUpdate":{"type":"object","properties":{"name":{"type":"string"},"domainName":{"type":"object"}}},
    "CompanyForResponse":{"type":"object","properties":{
      "id":{"type":"string","format":"uuid"},"name":{"type":"string"},"domainName":{"type":"object"},
      "createdAt":{"type":"string","format":"date-time"},"updatedAt":{"type":"string","format":"date-time"},
      "deletedAt":{},"riskScore":{"type":"string","enum":["` + enumCanary + `"]},
      "people":{"type":"array","items":{"$ref":"#/components/schemas/PersonForResponse"}}}},
    "Person":{"type":"object","properties":{"name":{"type":"object","properties":{"firstName":{"type":"string"},"lastName":{"type":"string"}}}}},
    "PersonForUpdate":{"type":"object","properties":{"name":{"type":"object"},"companyId":{"type":"string","format":"uuid"}}},
    "PersonForResponse":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"object"},
      "companyId":{"type":"string","format":"uuid"},"company":{"$ref":"#/components/schemas/CompanyForResponse"},
      "workspaceMember":{"$ref":"#/components/schemas/WorkspaceMemberForResponse"}}},
    "Rocket":{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"payload":{"type":["number","null"]}}},
    "RocketForUpdate":{"type":"object","properties":{"name":{"type":"string"}}},
    "RocketForResponse":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},"payload":{"type":"number"}}},
    "WorkspaceMember":{"type":"object","properties":{"name":{"type":"object"}}}
  }}
}`

const descriptionCanary = "description-canary-twenty-3c58"

// enumCanary is an enum value of the workspace document, which only objects.get reports.
const enumCanary = "ENUM_CANARY_TWENTY_8E21"

// Register publishes the configuration metadata the TUI needs and exactly two read-only operations.
func TestRegisterPublishesMetadataAndTwoReadOnlyOperations(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Twenty CRM" || metadata.DefaultBaseURL != cloudOrigin ||
		metadata.Target.Required || len(metadata.SecretRoles) != 1 ||
		metadata.SecretRoles[0].Name != roleAPIKey || metadata.SecretRoles[0].Description == "" {
		t.Fatalf("metadata = %+v, %v", metadata, ok)
	}

	operations := reg.Provider(Provider)
	if len(operations) != 89 {
		t.Fatalf("operations = %d, want seven company, two object, five record read, two record write, three record removal, three batch, three merge, three activity link, two attachment read, one attachment upload, three workflow read, four workflow control, four webhook, two member read, seven role, two agent read, two skill read, two logic function read, twelve view, nine data model, two dashboard read, and five dashboard write operations", len(operations))
	}
	for _, descriptor := range operations {
		wantVersion := 1
		if descriptor.ID == "twenty-crm.companies.delete" {
			wantVersion = 2
		}
		guarded := descriptor.ID == "twenty-crm.companies.delete" || descriptor.ID == "twenty-crm.companies.destroy" ||
			descriptor.ID == "twenty-crm.records.delete" || descriptor.ID == "twenty-crm.records.destroy" ||
			descriptor.ID == "twenty-crm.records.batch-delete" || descriptor.ID == "twenty-crm.records.merge" ||
			descriptor.ID == "twenty-crm.activity-targets.delete" || descriptor.ID == "twenty-crm.webhooks.delete" ||
			strings.HasPrefix(descriptor.ID, "twenty-crm.workflow-versions.") ||
			descriptor.ID == "twenty-crm.workflow-runs.stop" || descriptor.ID == "twenty-crm.workflow-runs.retry" ||
			descriptor.ID == "twenty-crm.roles.create" || descriptor.ID == "twenty-crm.roles.update" ||
			descriptor.ID == "twenty-crm.roles.delete" || strings.HasPrefix(descriptor.ID, "twenty-crm.roles.set") ||
			descriptor.ID == "twenty-crm.meta-objects.create" || descriptor.ID == "twenty-crm.meta-objects.update" ||
			descriptor.ID == "twenty-crm.meta-fields.create" || descriptor.ID == "twenty-crm.meta-fields.update" ||
			descriptor.ID == "twenty-crm.meta-objects.delete" || descriptor.ID == "twenty-crm.meta-fields.delete" ||
			descriptor.ID == "twenty-crm.views.create" || descriptor.ID == "twenty-crm.views.update" ||
			descriptor.ID == "twenty-crm.views.delete" ||
			strings.HasPrefix(descriptor.ID, "twenty-crm.view-fields.") ||
			strings.HasPrefix(descriptor.ID, "twenty-crm.view-filters.") ||
			strings.HasPrefix(descriptor.ID, "twenty-crm.view-groups.") ||
			strings.HasPrefix(descriptor.ID, "twenty-crm.view-sorts.") || strings.HasPrefix(descriptor.ID, "twenty-crm.page-layout-widgets.") ||
			strings.HasPrefix(descriptor.ID, "twenty-crm.page-layout-tabs.")
		if descriptor.RequiresToolAllowList != guarded {
			t.Errorf("descriptor %s RequiresToolAllowList = %v, want %v", descriptor.ID, descriptor.RequiresToolAllowList, guarded)
		}
		if descriptor.Version != wantVersion || descriptor.Provider != Provider ||
			!descriptor.Risk.OpenWorld || descriptor.Risk.DataSensitivity != wantSensitivity(descriptor.ID) {
			t.Errorf("descriptor %s = %+v, want a bounded operation requiring an explicit connection",
				descriptor.ID, descriptor)
		}
		if len(descriptor.Examples) > 0 && strings.Contains(string(descriptor.Examples[0].Arguments), "key") {
			t.Errorf("descriptor %s examples = %s", descriptor.ID, descriptor.Examples)
		}
	}
	if operations[0].ID != "twenty-crm.activity-targets.create" ||
		operations[1].ID != "twenty-crm.activity-targets.delete" ||
		operations[2].ID != "twenty-crm.activity-targets.list" ||
		operations[3].ID != "twenty-crm.agents.get" ||
		operations[4].ID != "twenty-crm.agents.list" ||
		operations[5].ID != "twenty-crm.attachments.get" ||
		operations[6].ID != "twenty-crm.attachments.list" ||
		operations[7].ID != "twenty-crm.attachments.upload" ||
		operations[8].ID != "twenty-crm.companies.create" ||
		operations[9].ID != "twenty-crm.companies.delete" ||
		operations[10].ID != "twenty-crm.companies.destroy" ||
		operations[11].ID != "twenty-crm.companies.get" ||
		operations[12].ID != "twenty-crm.companies.list" ||
		operations[13].ID != "twenty-crm.companies.restore" ||
		operations[14].ID != "twenty-crm.companies.update" ||
		operations[15].ID != "twenty-crm.logic-functions.get" ||
		operations[16].ID != "twenty-crm.logic-functions.list" ||
		operations[17].ID != "twenty-crm.meta-fields.create" ||
		operations[18].ID != "twenty-crm.meta-fields.delete" ||
		operations[19].ID != "twenty-crm.meta-fields.get" ||
		operations[20].ID != "twenty-crm.meta-fields.update" ||
		operations[21].ID != "twenty-crm.meta-objects.create" ||
		operations[22].ID != "twenty-crm.meta-objects.delete" ||
		operations[23].ID != "twenty-crm.meta-objects.get" ||
		operations[24].ID != "twenty-crm.meta-objects.list" ||
		operations[25].ID != "twenty-crm.meta-objects.update" ||
		operations[26].ID != "twenty-crm.objects.get" ||
		operations[27].ID != "twenty-crm.objects.list" ||
		operations[28].ID != "twenty-crm.page-layout-tabs.create" ||
		operations[29].ID != "twenty-crm.page-layout-tabs.delete" ||
		operations[30].ID != "twenty-crm.page-layout-widgets.create" ||
		operations[31].ID != "twenty-crm.page-layout-widgets.delete" ||
		operations[32].ID != "twenty-crm.page-layout-widgets.update" ||
		operations[33].ID != "twenty-crm.page-layouts.get" ||
		operations[34].ID != "twenty-crm.page-layouts.list" ||
		operations[35].ID != "twenty-crm.records.batch-create" ||
		operations[36].ID != "twenty-crm.records.batch-delete" ||
		operations[37].ID != "twenty-crm.records.batch-update" ||
		operations[38].ID != "twenty-crm.records.create" ||
		operations[39].ID != "twenty-crm.records.delete" ||
		operations[40].ID != "twenty-crm.records.destroy" ||
		operations[41].ID != "twenty-crm.records.duplicates" ||
		operations[42].ID != "twenty-crm.records.get" ||
		operations[43].ID != "twenty-crm.records.group-by" ||
		operations[44].ID != "twenty-crm.records.list" ||
		operations[45].ID != "twenty-crm.records.merge" ||
		operations[46].ID != "twenty-crm.records.merge-preview" ||
		operations[47].ID != "twenty-crm.records.restore" ||
		operations[48].ID != "twenty-crm.records.search" ||
		operations[49].ID != "twenty-crm.records.search-all" ||
		operations[50].ID != "twenty-crm.records.update" ||
		operations[51].ID != "twenty-crm.roles.create" ||
		operations[52].ID != "twenty-crm.roles.delete" ||
		operations[53].ID != "twenty-crm.roles.list" ||
		operations[54].ID != "twenty-crm.roles.set-field-permissions" ||
		operations[55].ID != "twenty-crm.roles.set-object-permissions" ||
		operations[56].ID != "twenty-crm.roles.set-permission-flags" ||
		operations[57].ID != "twenty-crm.roles.update" ||
		operations[58].ID != "twenty-crm.skills.get" ||
		operations[59].ID != "twenty-crm.skills.list" ||
		operations[60].ID != "twenty-crm.view-fields.create" ||
		operations[61].ID != "twenty-crm.view-fields.delete" ||
		operations[62].ID != "twenty-crm.view-fields.update" ||
		operations[63].ID != "twenty-crm.view-filters.create" ||
		operations[64].ID != "twenty-crm.view-filters.delete" ||
		operations[65].ID != "twenty-crm.view-filters.update" ||
		operations[66].ID != "twenty-crm.view-groups.delete" ||
		operations[67].ID != "twenty-crm.view-groups.update" ||
		operations[68].ID != "twenty-crm.view-sorts.create" ||
		operations[69].ID != "twenty-crm.view-sorts.delete" ||
		operations[70].ID != "twenty-crm.view-sorts.update" ||
		operations[71].ID != "twenty-crm.views.create" ||
		operations[72].ID != "twenty-crm.views.delete" ||
		operations[73].ID != "twenty-crm.views.get" ||
		operations[74].ID != "twenty-crm.views.list" ||
		operations[75].ID != "twenty-crm.views.update" ||
		operations[76].ID != "twenty-crm.webhooks.delete" ||
		operations[77].ID != "twenty-crm.webhooks.get" ||
		operations[78].ID != "twenty-crm.webhooks.list" ||
		operations[79].ID != "twenty-crm.webhooks.update" ||
		operations[80].ID != "twenty-crm.workflow-runs.list" ||
		operations[81].ID != "twenty-crm.workflow-runs.retry" ||
		operations[82].ID != "twenty-crm.workflow-runs.stop" ||
		operations[83].ID != "twenty-crm.workflow-versions.activate" ||
		operations[84].ID != "twenty-crm.workflow-versions.deactivate" ||
		operations[85].ID != "twenty-crm.workflows.get" ||
		operations[86].ID != "twenty-crm.workflows.list" ||
		operations[87].ID != "twenty-crm.workspace-members.get" ||
		operations[88].ID != "twenty-crm.workspace-members.list" {
		t.Errorf("operation IDs are not sorted: %+v", operations)
	}
}

func TestCompanyMutationsUseTheCompanyRESTRoute(t *testing.T) {
	methods := []string{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method+" "+request.URL.RequestURI())
		switch {
		case request.Method == http.MethodDelete:
			return jsonResponse(http.StatusOK, deleteBody(companyID)), nil
		case strings.HasSuffix(request.URL.Path, "/restore"):
			return jsonResponse(http.StatusOK, strings.Replace(companyBody, `"company"`, `"restoreCompany"`, 1)), nil
		}
		return jsonResponse(http.StatusOK, companyBody), nil
	})
	c, _ := client(t)
	domain := "example.invalid"
	if _, err := c.CreateCompany(context.Background(), "New", &domain); err != nil {
		t.Fatal(err)
	}
	name := "Changed"
	if _, err := c.UpdateCompany(context.Background(), companyID, &name, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCompany(context.Background(), companyID); err != nil {
		t.Fatal(err)
	}
	if err := c.DestroyCompany(context.Background(), companyID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RestoreCompany(context.Background(), companyID); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /rest/companies", "PATCH /rest/companies/" + companyID,
		"DELETE /rest/companies/" + companyID + "?soft_delete=true",
		"DELETE /rest/companies/" + companyID,
		"PATCH /rest/companies/" + companyID + "/restore",
	}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("requests = %v, want %v", methods, want)
	}
}

func deleteBody(id string) string {
	return `{"data":{"deleteCompany":{"id":"` + id + `"}}}`
}

// A removal or restore answer for another company, or without an identifier, is an invalid response.
func TestRemovalAndRestoreCheckTheAnsweredIdentifier(t *testing.T) {
	for _, body := range []string{deleteBody(otherID), `{"data":{"deleteCompany":{}}}`, `{}`} {
		serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
		c, _ := client(t)
		calls := map[string]func() error{
			"delete":  func() error { return c.DeleteCompany(context.Background(), companyID) },
			"destroy": func() error { return c.DestroyCompany(context.Background(), companyID) },
			"restore": func() error {
				_, err := c.RestoreCompany(context.Background(), companyID)
				return err
			},
		}
		for name, call := range calls {
			if got := classOf(call()); got != provider.ClassInvalidResponse {
				t.Errorf("%s with %s = %q, want invalid-response", name, body, got)
			}
		}
	}
}

// An unclear destroy result is reported as uncertain and the change is never repeated.
func TestDestroyReportsAnOpenResultWithoutRepeating(t *testing.T) {
	calls := 0
	serve(t, func(*http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusInternalServerError, `{"error":"`+bodyCanary+`"}`), nil
	})
	c, _ := client(t)
	err := c.DestroyCompany(context.Background(), companyID)
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), bodyCanary) {
		t.Fatalf("DestroyCompany() = %v, want an uncertain outcome without provider text", err)
	}
	if calls != 1 {
		t.Fatalf("requests = %d, want exactly one", calls)
	}
}

// The mutation path only ever carries a validated identifier.
func TestRemovalAndRestoreRefuseAnUnusableIdentifierBeforeIO(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	for _, id := range []string{"", "../x", "not-a-uuid"} {
		if c.DeleteCompany(context.Background(), id) == nil || c.DestroyCompany(context.Background(), id) == nil {
			t.Errorf("id %q was accepted", id)
		}
		if _, err := c.RestoreCompany(context.Background(), id); err == nil {
			t.Errorf("restore accepted %q", id)
		}
	}
}

// The deleted flag adds exactly the fixed trash filter, alone and combined with a search.
func TestListDeletedAddsOnlyTheFixedFilter(t *testing.T) {
	tests := []struct {
		options ListOptions
		want    string
	}{
		{ListOptions{Deleted: true}, `deletedAt[is]:NOT_NULL`},
		{ListOptions{Deleted: true, NameContains: "Bike"}, `name[ilike]:"%Bike%",deletedAt[is]:NOT_NULL`},
		{ListOptions{NameContains: "Bike"}, `name[ilike]:"%Bike%"`},
		{ListOptions{}, ``},
	}
	for _, tt := range tests {
		var got url.Values
		serve(t, func(request *http.Request) (*http.Response, error) {
			got = request.URL.Query()
			return jsonResponse(http.StatusOK, listBody), nil
		})
		c, _ := client(t)
		if _, err := c.ListCompanies(context.Background(), tt.options); err != nil {
			t.Fatal(err)
		}
		if got.Get("filter") != tt.want {
			t.Errorf("%+v filter = %q, want %q", tt.options, got.Get("filter"), tt.want)
		}
	}
}

// Neither deleting tool is offered by a connection without a tools list; a tools list that names them
// offers them.
func TestDeletingToolsNeedTheToolAllowList(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, deleteBody(companyID)), nil
	})
	stubLimiter(t, cloudKey)
	cfg := coreConfig()
	all := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}
	bare := cfg.Connections["crm"]
	bare.Effects = all
	cfg.Connections["crm"] = bare
	listed := bare
	listed.Tools = []string{"twenty-crm.companies.delete", "twenty-crm.companies.destroy"}
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Effects: all, Tools: listed.Tools}
	stubLimiter(t, internalKey)

	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	for _, operation := range []string{"twenty-crm.companies.delete", "twenty-crm.companies.destroy"} {
		request := application.InvokeRequest{Operation: operation, Connection: "crm",
			Arguments: json.RawMessage(`{"id":"` + companyID + `"}`), Confirmed: true}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s was offered without a tools list", operation)
		}
		request.Connection = "crm-internal"
		if _, err := core.Invoke(context.Background(), request); err != nil {
			t.Errorf("%s with a tools list = %v", operation, err)
		}
	}
}

// The list request carries exactly the allowed search, sort, and paging parameters and nothing else. The
// origin comes from the service, never from an argument.
func TestListCompaniesSendsOnlyTheAllowedParameters(t *testing.T) {
	var got url.Values
	calls := 0
	serve(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || request.URL.Scheme != "https" ||
			request.URL.Host != "api.twenty.com" || request.URL.Path != "/rest/companies" {
			t.Errorf("request = %s %s", request.Method, request.URL.Redacted())
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer "+cloudKey {
			t.Errorf("Authorization header = %q, want the bearer API key", authorization)
		}
		got = request.URL.Query()
		return jsonResponse(http.StatusOK, listBody), nil
	})

	c, _ := client(t)
	if _, err := c.ListCompanies(context.Background(), ListOptions{
		NameContains: "Bike", DomainContains: "bike-ride.example", Limit: 50,
		Sort: "name", Direction: "asc", Cursor: "cursor-page-2",
	}); err != nil {
		t.Fatalf("ListCompanies() = %v", err)
	}

	if calls != 1 {
		t.Fatalf("requests = %d, want exactly one", calls)
	}
	want := url.Values{
		"limit": {"50"}, "depth": {"0"}, "order_by": {"name[AscNullsFirst]"},
		"filter":         {`name[ilike]:"%Bike%",domainName.primaryLinkUrl[ilike]:"%bike-ride.example%"`},
		"starting_after": {"cursor-page-2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}
}

// Omitted arguments produce the deterministic default page, without a filter and without a cursor.
func TestListCompaniesUsesDeterministicDefaults(t *testing.T) {
	var got url.Values
	serve(t, func(request *http.Request) (*http.Response, error) {
		got = request.URL.Query()
		return jsonResponse(http.StatusOK, listBody), nil
	})

	c, _ := client(t)
	if _, err := c.ListCompanies(context.Background(), ListOptions{}); err != nil {
		t.Fatalf("ListCompanies() = %v", err)
	}
	want := url.Values{"limit": {"25"}, "depth": {"0"}, "order_by": {"createdAt[DescNullsLast]"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}
}

// The result carries every field of the record; a workspace-specific field sits in custom_fields.
func TestListCompaniesReportsEveryField(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, listBody), nil
	})

	c, _ := client(t)
	result, err := c.ListCompanies(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListCompanies() = %v", err)
	}
	if len(result.Companies) != 2 || result.NextCursor != "cursor-page-2" || !result.HasMore ||
		result.TotalCount == nil || *result.TotalCount != 57 {
		t.Fatalf("result = %#v", result)
	}
	first, second := result.Companies[0], result.Companies[1]
	if first.ID != companyID || first.Name != "Bike & Ride GmbH" || first.Domain != "bike-ride.example" ||
		first.CustomFields["riskScore"] != customCanary || first.AnnualRevenue["currency_code"] == nil ||
		first.DomainName["primary_link_url"] != "https://bike-ride.example" {
		t.Errorf("first = %#v", first)
	}
	// An empty link label falls back to the link URL rather than reporting no domain.
	if second.ID != otherID || second.Domain != "https://test.example" || second.CustomFields != nil {
		t.Errorf("second = %#v", second)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	for _, forbidden := range []string{"annualRevenue", "secondaryLinks", "addressOld"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("the list projection carries %q: %s", forbidden, encoded)
		}
	}
}

// Cursor pagination stays deterministic: the cursor of a page reaches the provider, and the last page
// reports no follow-up cursor.
func TestListCompaniesPaginatesByCursor(t *testing.T) {
	pages := map[string]string{
		"": `{"data":{"companies":[]},"totalCount":2,` +
			`"pageInfo":{"hasNextPage":true,"endCursor":"cursor-page-2"}}`,
		"cursor-page-2": `{"data":{"companies":[]},"totalCount":2,` +
			`"pageInfo":{"hasNextPage":false,"endCursor":"cursor-page-3"}}`,
	}
	requested := make([]string, 0, 2)
	serve(t, func(request *http.Request) (*http.Response, error) {
		cursor := request.URL.Query().Get("starting_after")
		requested = append(requested, cursor)
		return jsonResponse(http.StatusOK, pages[cursor]), nil
	})

	c, _ := client(t)
	first, err := c.ListCompanies(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListCompanies() = %v", err)
	}
	if !first.HasMore || first.NextCursor != "cursor-page-2" {
		t.Fatalf("first page = %#v", first)
	}

	second, err := c.ListCompanies(context.Background(), ListOptions{Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("ListCompanies(cursor) = %v", err)
	}
	// The last page hands out no cursor, so a caller cannot loop past the end of the result.
	if second.HasMore || second.NextCursor != "" {
		t.Errorf("second page = %#v", second)
	}
	if !reflect.DeepEqual(requested, []string{"", "cursor-page-2"}) {
		t.Errorf("requested cursors = %v", requested)
	}
}

// Bounds, formats, and anything that could leave a filter value are refused before any provider I/O.
func TestListCompaniesRejectsUnusableOptionsBeforeIO(t *testing.T) {
	refuse(t)
	c, _ := client(t)

	tests := map[string]ListOptions{
		"negative page size":           {Limit: -1},
		"page size above the bound":    {Limit: maxPageSize + 1},
		"unknown sort property":        {Sort: "annual_revenue"},
		"unknown sort direction":       {Direction: "sideways"},
		"oversized name filter":        {NameContains: strings.Repeat("a", searchNameMax+1)},
		"quote in the name filter":     {NameContains: `Acme" or name[ilike]:"%`},
		"comma in the name filter":     {NameContains: "Acme,Other"},
		"bracket in the name filter":   {NameContains: "Acme[ilike]"},
		"domain filter with a slash":   {DomainContains: "acme.example/path"},
		"domain filter with a quote":   {DomainContains: `acme"`},
		"oversized domain filter":      {DomainContains: strings.Repeat("a", searchDomainMax+1)},
		"cursor with a separator":      {Cursor: "cursor&limit=200"},
		"cursor beyond the length cap": {Cursor: strings.Repeat("c", maxCursorLength+1)},
	}
	for name, options := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := c.ListCompanies(context.Background(), options); err == nil {
				t.Fatal("ListCompanies() accepted the options")
			}
		})
	}
}

// A list record without a usable identifier is a contract violation, not a result.
func TestListCompaniesRejectsAnUnusableIdentifier(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":{"companies":[{"id":"not-a-uuid","name":"Acme"}]}}`), nil
	})

	c, _ := client(t)
	_, err := c.ListCompanies(context.Background(), ListOptions{})
	if class := classOf(err); class != provider.ClassInvalidResponse {
		t.Fatalf("class = %q, want %q (%v)", class, provider.ClassInvalidResponse, err)
	}
}

// The get operation reads exactly the requested company and produces no other provider I/O.
func TestGetCompanyReadsExactlyTheRequestedCompany(t *testing.T) {
	requests := make([]string, 0, 1)
	serve(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path+"?"+request.URL.RawQuery)
		return jsonResponse(http.StatusOK, companyBody), nil
	})

	c, _ := client(t)
	company, err := c.GetCompany(context.Background(), companyID)
	if err != nil {
		t.Fatalf("GetCompany() = %v", err)
	}
	if !reflect.DeepEqual(requests, []string{"GET /rest/companies/" + companyID + "?depth=0"}) {
		t.Fatalf("requests = %v, want exactly one read of the company", requests)
	}

	if company.ID != companyID || company.Name != "Bike & Ride GmbH" || company.Domain != "bike-ride.example" ||
		company.CreatedAt != "2026-05-14T16:52:21.000Z" || company.UpdatedAt != "2026-06-02T08:11:03.000Z" ||
		company.CustomFields["riskScore"] != customCanary {
		t.Errorf("company = %#v", company)
	}
}

// Only a validated UUID reaches the provider, and an answer about another company is refused.
func TestGetCompanyGuardsTheIdentifier(t *testing.T) {
	t.Run("a malformed identifier stops before the provider", func(t *testing.T) {
		refuse(t)
		c, _ := client(t)
		for _, id := range []string{"", "42", "../people", companyID + "0", "zz" + companyID[2:]} {
			if _, err := c.GetCompany(context.Background(), id); err == nil {
				t.Errorf("GetCompany(%q) was accepted", id)
			}
		}
	})

	t.Run("another company is an invalid response", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"data":{"company":{"id":"`+otherID+`","name":"Test GmbH"}}}`), nil
		})
		c, _ := client(t)
		_, err := c.GetCompany(context.Background(), companyID)
		if class := classOf(err); class != provider.ClassInvalidResponse {
			t.Fatalf("class = %q, want %q (%v)", class, provider.ClassInvalidResponse, err)
		}
	})
}

// Every provider status becomes a stable class, and no provider body reaches the message.
func TestProviderStatusesAreNormalized(t *testing.T) {
	tests := []struct {
		status int
		want   provider.Class
	}{
		{http.StatusUnauthorized, provider.ClassAuth},
		{http.StatusForbidden, provider.ClassPermission},
		{http.StatusNotFound, provider.ClassNotFound},
		{http.StatusBadRequest, provider.ClassProviderError},
		{http.StatusUnprocessableEntity, provider.ClassProviderError},
		{http.StatusTooManyRequests, provider.ClassRateLimited},
		{http.StatusGatewayTimeout, provider.ClassTimeout},
		{http.StatusInternalServerError, provider.ClassProviderError},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(tt.status, `{"statusCode":`+strconv.Itoa(tt.status)+
					`,"error":"`+bodyCanary+`","messages":["`+bodyCanary+`"]}`), nil
			})
			c, _ := client(t)
			_, err := c.ListCompanies(context.Background(), ListOptions{})
			if class := classOf(err); class != tt.want {
				t.Errorf("class = %q, want %q (%v)", class, tt.want, err)
			}
			if tt.want == provider.ClassPermission && !strings.Contains(err.Error(), "; check ") {
				t.Errorf("a refusal names no next step: %v", err)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("error carries the provider body: %v", err)
			}
		})
	}
}

// A failure before a status code exists is classified without copying the transport message.
func TestTransportFailuresAreNormalized(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want provider.Class
	}{
		{"timeout", &net.DNSError{IsTimeout: true, Err: bodyCanary}, provider.ClassTimeout},
		{"tls", &tls.CertificateVerificationError{Err: errors.New(bodyCanary)}, provider.ClassTLS},
		{"unreachable", errors.New(bodyCanary), provider.ClassUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) { return nil, tt.err })
			c, _ := client(t)
			_, err := c.ListCompanies(context.Background(), ListOptions{})
			if class := classOf(err); class != tt.want {
				t.Errorf("class = %q, want %q (%v)", class, tt.want, err)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("error carries the transport message: %v", err)
			}
		})
	}
}

// An answer beyond the size limit is refused instead of being read into memory.
func TestOversizedResponsesAreRefused(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, strings.Repeat("x", maxResponseBytes+1)), nil
	})
	c, _ := client(t)
	_, err := c.ListCompanies(context.Background(), ListOptions{})
	if class := classOf(err); class != provider.ClassInvalidResponse {
		t.Fatalf("class = %q, want %q (%v)", class, provider.ClassInvalidResponse, err)
	}
}

// Opening a connection registers the API key and the derived bearer value with the redactor, accepts the
// cloud origin and a self-hosted https origin, and refuses everything that is not a bare https origin.
func TestOpenRedactsTheKeyAndBindsTheConfiguredOrigin(t *testing.T) {
	red := &redact.Redactor{}
	if _, err := open(context.Background(), resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin),
		resolver(red), red, freeLimiter()); err != nil {
		t.Fatalf("open() = %v", err)
	}
	for _, value := range []string{cloudKey, "Bearer " + cloudKey} {
		if got := red.Apply("diagnostic " + value); strings.Contains(got, cloudKey) {
			t.Errorf("redactor keeps %q: %q", value, got)
		}
	}

	for _, origin := range []string{cloudOrigin, cloudOrigin + "/", selfHosted, selfHosted + "/"} {
		if _, err := open(context.Background(), resolvedConnection("crm", "crm-cloud-reader", cloudEnv, origin),
			resolver(red), red, freeLimiter()); err != nil {
			t.Errorf("open() refused the configured origin %q: %v", origin, err)
		}
	}

	for _, origin := range []string{
		"http://api.twenty.com",                  // plain text would carry the key in the clear
		"https://user:pass@api.twenty.com",       // userinfo is a second credential
		"https://api.twenty.com/rest",            // a path would silently rewrite every route
		"https://api.twenty.com?token=x",         // a query would ride along on every request
		"https://api.twenty.com#fragment",        //
		"https:api.twenty.com",                   // opaque, without a host
		"//api.twenty.com", "api.twenty.com", "", //
	} {
		connection := resolvedConnection("crm", "crm-cloud-reader", cloudEnv, origin)
		if _, err := open(context.Background(), connection, resolver(red), red, freeLimiter()); err == nil {
			t.Errorf("open() accepted the origin %q", origin)
		}
	}

	missing := resolvedConnection("crm", "crm-cloud-reader", "TEST_TWENTY_ABSENT", cloudOrigin)
	if _, err := open(context.Background(), missing, resolver(red), red, freeLimiter()); err == nil {
		t.Error("open() accepted a credential without a secret")
	}
}

// Two workspaces are two connections with two keys and two origins. Each request uses the key and the
// origin of its own connection, and the keys get separate rate-limit budgets.
func TestWorkspacesKeepTheirOwnOriginKeyAndRateBudget(t *testing.T) {
	type call struct{ host, authorization string }
	calls := make([]call, 0, 2)
	serve(t, func(request *http.Request) (*http.Response, error) {
		calls = append(calls, call{request.URL.Host, request.Header.Get("Authorization")})
		return jsonResponse(http.StatusOK, `{"data":{"companies":[]},"pageInfo":{"hasNextPage":false}}`), nil
	})

	red := &redact.Redactor{}
	secrets := resolver(red)
	for _, connection := range []*config.Resolved{
		resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin),
		resolvedConnection("crm-internal", "crm-selfhosted-reader", internalEnv, selfHosted),
	} {
		c, err := open(context.Background(), connection, secrets, red, freeLimiter())
		if err != nil {
			t.Fatalf("open(%s) = %v", connection.Name, err)
		}
		if _, err := c.ListCompanies(context.Background(), ListOptions{}); err != nil {
			t.Fatalf("ListCompanies(%s) = %v", connection.Name, err)
		}
	}

	want := []call{
		{"api.twenty.com", "Bearer " + cloudKey},
		{"crm.example.invalid", "Bearer " + internalKey},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want each connection to use its own origin and key", calls)
	}
	if limiters.For(cloudKey) == limiters.For(internalKey) {
		t.Error("two API keys share one rate-limit budget")
	}
	if limiters.For(cloudKey) != limiters.For(cloudKey) {
		t.Error("one API key does not keep one rate-limit budget")
	}
}

// Requests that share a key are spaced by the documented limit of 100 requests per minute.
func TestRateLimitSpacesRequestsOfOneKey(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":{"companies":[]},"pageInfo":{"hasNextPage":false}}`), nil
	})

	var (
		mu     sync.Mutex
		now    = time.Unix(0, 0)
		waited []time.Duration
	)
	limited := ratelimit.New(minInterval,
		func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		func(_ context.Context, d time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			waited = append(waited, d)
			now = now.Add(d)
			return nil
		})

	red := &redact.Redactor{}
	c, err := open(context.Background(), resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin), resolver(red), red, limited)
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.ListCompanies(context.Background(), ListOptions{}); err != nil {
			t.Fatalf("ListCompanies() = %v", err)
		}
	}

	if !reflect.DeepEqual(waited, []time.Duration{minInterval, minInterval}) {
		t.Errorf("waits = %v, want two waits of %v", waited, minInterval)
	}
	if minInterval*100 < time.Minute {
		t.Errorf("interval %v allows more than 100 requests per minute", minInterval)
	}
}

// A cancelled request never becomes a provider call while it waits for the rate limit.
func TestRateLimitRespectsCancellation(t *testing.T) {
	refuse(t)
	limited := ratelimit.New(minInterval, time.Now, ratelimit.Sleep)
	limited.HoldFor(time.Hour)

	red := &redact.Redactor{}
	c, err := open(context.Background(), resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin), resolver(red), red, limited)
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.ListCompanies(ctx, ListOptions{})
	if class := classOf(err); class != provider.ClassTimeout {
		t.Fatalf("class = %q, want %q (%v)", class, provider.ClassTimeout, err)
	}
}

// The connection test reads one company and then verifies the workspace schema, so an incompatible
// workspace fails here instead of answering a business call with an incomplete record.
func TestTestConnectionChecksAccessAndWorkspaceSchema(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		requests := make([]string, 0, 2)
		serve(t, func(request *http.Request) (*http.Response, error) {
			requests = append(requests, request.URL.Path+"?"+request.URL.RawQuery)
			if request.URL.Path == schemaPath {
				return jsonResponse(http.StatusOK, schemaBody), nil
			}
			return jsonResponse(http.StatusOK, `{"data":{"companies":[]},"pageInfo":{"hasNextPage":false}}`), nil
		})
		stubLimiter(t, cloudKey)

		class, err := TestConnection(context.Background(),
			resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin), resolver(nil), nil)
		if err != nil || class != provider.ClassOK {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
		want := []string{"/rest/companies?depth=0&limit=1", "/open-api/core?"}
		if !reflect.DeepEqual(requests, want) {
			t.Errorf("requests = %v, want %v", requests, want)
		}
	})

	t.Run("an unusable key is an auth failure, not a schema problem", func(t *testing.T) {
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == schemaPath {
				t.Error("the schema was read although the key was rejected")
			}
			return jsonResponse(http.StatusUnauthorized, `{"messages":["`+bodyCanary+`"]}`), nil
		})
		stubLimiter(t, cloudKey)
		class, err := TestConnection(context.Background(),
			resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin), resolver(nil), nil)
		if err != nil || class != provider.ClassAuth {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
	})

	t.Run("a key without company permission reports permission", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`), nil
		})
		stubLimiter(t, cloudKey)
		class, err := TestConnection(context.Background(),
			resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin), resolver(nil), nil)
		if err != nil || class != provider.ClassPermission {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
	})

	incompatible := map[string]string{
		"a workspace without the company routes": `{"paths":{"/people":{"post":{"operationId":"createOnePerson"}}},"components":{"schemas":{` +
			`"CompanyForResponse":{"properties":{"id":{},"name":{},"domainName":{},"createdAt":{},"updatedAt":{}}}}}}`,
		"a workspace without a company object": `{"paths":{"/companies":{},"/companies/{id}":{}},` +
			`"components":{"schemas":{"Person":{"properties":{"id":{}}}}}}`,
		"a workspace missing a company field": `{"paths":{"/companies":{"post":{"operationId":"createOneCompany"}}},` +
			`"components":{"schemas":{"Company":{},"CompanyForResponse":{"properties":{"id":{},"name":{},"createdAt":{}}}}}}`,
	}
	for name, document := range incompatible {
		t.Run(name, func(t *testing.T) {
			serve(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == schemaPath {
					return jsonResponse(http.StatusOK, document), nil
				}
				return jsonResponse(http.StatusOK, `{"data":{"companies":[]},"pageInfo":{"hasNextPage":false}}`), nil
			})
			stubLimiter(t, cloudKey)

			class, err := TestConnection(context.Background(),
				resolvedConnection("crm", "crm-cloud-reader", cloudEnv, cloudOrigin), resolver(nil), nil)
			if err == nil {
				t.Fatalf("TestConnection() = %q, want an explained incompatibility", class)
			}
			if class != "" {
				t.Errorf("class = %q, want none beside the explanation", class)
			}
			// The explanation names the workspace problem so a human can act on it.
			if !strings.Contains(err.Error(), "workspace") {
				t.Errorf("error = %v, want it to explain the workspace problem", err)
			}
			if strings.Contains(err.Error(), cloudKey) {
				t.Errorf("error carries the API key: %v", err)
			}
		})
	}

	t.Run("an unusable connection reports its class instead of failing", func(t *testing.T) {
		refuse(t)
		class, err := TestConnection(context.Background(),
			resolvedConnection("crm", "crm-cloud-reader", cloudEnv, "http://api.twenty.com"), resolver(nil), nil)
		if err != nil || class != provider.ClassProviderError {
			t.Fatalf("TestConnection() = %q, %v", class, err)
		}
	})
}

// Both operations satisfy their registered contract end to end through the application core: the core
// validates the arguments, selects the explicit connection, resolves the key of that workspace, and
// validates the normalised result against the descriptor.
func TestOperationsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == companiesPath {
			return jsonResponse(http.StatusOK, listBody), nil
		}
		return jsonResponse(http.StatusOK, companyBody), nil
	})
	stubLimiter(t, cloudKey)
	stubLimiter(t, internalKey)

	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)

	list, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "twenty-crm.companies.list", Connection: "crm",
		Arguments: json.RawMessage(`{"name_contains":"Bike","limit":25,"sort":"created_at","direction":"desc"}`),
	})
	if err != nil {
		t.Fatalf("invoke list = %v", err)
	}
	var listed struct {
		Companies []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Domain string `json:"domain"`
		} `json:"companies"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	if err := json.Unmarshal(list.Result, &listed); err != nil {
		t.Fatalf("list result = %s: %v", list.Result, err)
	}
	if len(listed.Companies) != 2 || listed.Companies[0].ID != companyID ||
		listed.Companies[0].Domain != "bike-ride.example" || listed.NextCursor != "cursor-page-2" ||
		!listed.HasMore {
		t.Errorf("list result = %s", list.Result)
	}
	if !strings.Contains(string(list.Result), `"custom_fields":{"riskScore":"`+customCanary+`"}`) {
		t.Errorf("the core left out the workspace-specific field: %s", list.Result)
	}

	got, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "twenty-crm.companies.get", Connection: "crm-internal",
		Arguments: json.RawMessage(`{"id":"` + companyID + `"}`),
	})
	if err != nil {
		t.Fatalf("invoke get = %v", err)
	}
	if !strings.Contains(string(got.Result), `"domain":"bike-ride.example"`) ||
		!strings.Contains(string(got.Result), `"updated_at":"2026-06-02T08:11:03.000Z"`) {
		t.Errorf("get result = %s", got.Result)
	}
}

// The core refuses what the contract does not allow before a provider is contacted, and it never guesses
// a workspace when two connections could serve the request.
func TestTheCoreRefusesUnsupportedRequestsBeforeProviderIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)

	tests := []struct {
		name    string
		request application.InvokeRequest
	}{
		{"two workspaces without an explicit connection", application.InvokeRequest{
			Operation: "twenty-crm.companies.list", Arguments: json.RawMessage(`{}`)}},
		{"unknown argument", application.InvokeRequest{
			Operation: "twenty-crm.companies.list", Connection: "crm",
			Arguments: json.RawMessage(`{"depth":1}`)}},
		{"a base url as an argument", application.InvokeRequest{
			Operation: "twenty-crm.companies.list", Connection: "crm",
			Arguments: json.RawMessage(`{"base_url":"https://attacker.example.invalid"}`)}},
		{"page size above the bound", application.InvokeRequest{
			Operation: "twenty-crm.companies.list", Connection: "crm",
			Arguments: json.RawMessage(`{"limit":200}`)}},
		{"a filter separator in the search term", application.InvokeRequest{
			Operation: "twenty-crm.companies.list", Connection: "crm",
			Arguments: json.RawMessage(`{"name_contains":"Acme\",or(name[ilike]:\"%"}`)}},
		{"unknown sort property", application.InvokeRequest{
			Operation: "twenty-crm.companies.list", Connection: "crm",
			Arguments: json.RawMessage(`{"sort":"annual_revenue"}`)}},
		{"a cursor that carries a parameter", application.InvokeRequest{
			Operation: "twenty-crm.companies.list", Connection: "crm",
			Arguments: json.RawMessage(`{"cursor":"abc&limit=200"}`)}},
		{"identifier that is not a UUID", application.InvokeRequest{
			Operation: "twenty-crm.companies.get", Connection: "crm",
			Arguments: json.RawMessage(`{"id":"../people/1234567890123456789012345"}`)}},
		{"a connection of another provider", application.InvokeRequest{
			Operation: "twenty-crm.companies.get", Connection: "wiki",
			Arguments: json.RawMessage(`{"id":"` + companyID + `"}`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := core.Invoke(context.Background(), tt.request); err == nil {
				t.Fatal("the core accepted the request")
			}
		})
	}
}

// A resolved API key never reaches a result, a diagnostic, or an error, whatever the workspace answers.
func TestTheAPIKeyNeverReachesTheOutput(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":{"companies":[{"id":"`+companyID+`",`+
			`"name":"`+cloudKey+`","domainName":{"primaryLinkLabel":"Bearer `+cloudKey+`",`+
			`"primaryLinkUrl":"https://acme.example"}}]},"pageInfo":{"hasNextPage":false}}`), nil
	})
	stubLimiter(t, cloudKey)

	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "twenty-crm.companies.list", Connection: "crm", Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	if strings.Contains(string(response.Result), cloudKey) {
		t.Errorf("the result carries the API key: %s", response.Result)
	}
	if !strings.Contains(string(response.Result), redact.Marker) {
		t.Errorf("the result was not redacted: %s", response.Result)
	}
	var value any
	if err := json.Unmarshal(response.Result, &value); err != nil {
		t.Errorf("the redacted result is not valid JSON: %v", err)
	}
}

// registry returns a registry with Twenty and one foreign provider, so a wrong route is provable.
func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	if err := reg.RegisterProvider(config.ProviderMetadata{ID: "wiki", Name: "Wiki"}, nil); err != nil {
		t.Fatalf("RegisterProvider() = %v", err)
	}
	return reg
}

// coreConfig configures two Twenty workspaces with separate origins and credentials, plus one foreign
// connection.
func coreConfig() *config.Config {
	return &config.Config{
		Version: 1,
		Services: map[string]config.Service{
			"crm-cloud":      {Provider: Provider, BaseURL: cloudOrigin},
			"crm-selfhosted": {Provider: Provider, BaseURL: selfHosted},
			"wiki":           {Provider: "wiki", BaseURL: "https://wiki.example.invalid"},
		},
		Credentials: map[string]config.Credential{
			"crm-cloud-reader": {
				Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: cloudEnv},
			},
			"crm-selfhosted-reader": {
				Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIKey: internalEnv},
			},
			"wiki-reader": {Type: config.CredentialTypeEnv, Values: map[string]string{"token-id": "WIKI_ID"}},
		},
		Connections: map[string]config.Connection{
			"crm":          {Service: "crm-cloud", Credential: "crm-cloud-reader"},
			"crm-internal": {Service: "crm-selfhosted", Credential: "crm-selfhosted-reader"},
			"wiki":         {Service: "wiki", Credential: "wiki-reader"},
		},
	}
}

// stubLimiter gives one API key a limiter without waiting time, so a test that performs several requests
// does not sleep. The spacing itself is proven by TestRateLimitSpacesRequestsOfOneKey.
func stubLimiter(t *testing.T, key string) {
	t.Helper()
	t.Cleanup(limiters.Replace(key, freeLimiter()))
}

func classOf(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return ""
}

// wantSensitivity is the data class a tool must declare: record tools carry their own.
func wantSensitivity(id string) string {
	if strings.HasPrefix(id, Provider+".records.") || strings.HasPrefix(id, Provider+".activity-targets.") ||
		strings.HasPrefix(id, Provider+".attachments.") {
		return recordDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".workflow") {
		return workflowDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".webhooks.") {
		return webhookDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".workspace-members.") {
		return memberDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".roles.") {
		return roleDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".logic-functions.") {
		return logicFunctionDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".agents.") || strings.HasPrefix(id, Provider+".skills.") {
		return aiDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".views.") || strings.HasPrefix(id, Provider+".view-fields.") ||
		strings.HasPrefix(id, Provider+".view-filters.") || strings.HasPrefix(id, Provider+".view-groups.") ||
		strings.HasPrefix(id, Provider+".view-sorts.") {
		return viewDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".page-layout") {
		return layoutDataSensitivity
	}
	if strings.HasPrefix(id, Provider+".meta") {
		return metadataSensitivity
	}
	return dataSensitivity
}

func companyOutput(t *testing.T, handler capability.Handler, args, body string) map[string]any {
	t.Helper()
	var requests []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.URL.Path)
		return jsonResponse(http.StatusOK, body), nil
	})
	stubLimiter(t, cloudKey)
	raw, err := runTool(t, handler, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || !strings.HasPrefix(requests[0], companiesPath) {
		t.Fatalf("requests = %v, want exactly one company request", requests)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCompanyReadsEveryFieldWithOneRequest(t *testing.T) {
	signedFile := signedAddress
	body := strings.Replace(richCompany, `"employees":12`, `"employees":12,"cv":[{"fileId":"`+otherID+`","label":"cv.pdf","url":"`+signedFile+`"}],`+
		`"avatar":"`+signedFile+`","notes":{"blocknote":"[]","markdown":"hello"},"homepage":"`+plainAddr+`"`, 1)
	for name, test := range map[string]struct {
		handler capability.Handler
		args    string
		body    string
		pick    func(map[string]any) map[string]any
	}{
		"list": {invokeCompaniesList, `{}`, `{"data":{"companies":[` + body + `]},"pageInfo":{"hasNextPage":false}}`,
			func(out map[string]any) map[string]any { return out["companies"].([]any)[0].(map[string]any) }},
		"get": {invokeCompaniesGet, `{"id":"` + companyID + `"}`, `{"data":{"company":` + body + `}}`,
			func(out map[string]any) map[string]any { return out }},
	} {
		t.Run(name, func(t *testing.T) {
			company := test.pick(companyOutput(t, test.handler, test.args, test.body))
			if got := company["annual_revenue"].(map[string]any)["amount_micros"]; got != json.Number("9007199254740993") {
				t.Errorf("amount_micros = %v", got)
			}
			domain := company["domain_name"].(map[string]any)
			if domain["primary_link_url"] != "https://bike.example" || domain["primary_link_label"] != "bike.example" ||
				len(domain["secondary_links"].([]any)) != 1 || company["domain"] != "bike.example" {
				t.Errorf("domain = %v / %v", domain, company["domain"])
			}
			if _, ok := company["linkedin_link"].(map[string]any)["primary_link_label"]; ok {
				t.Error("an empty subfield was reported")
			}
			address := company["address"].(map[string]any)
			if address["address_city"] != "Zuerich" || address["address_lat"] != json.Number("47.37") {
				t.Errorf("address = %v", address)
			}
			if _, ok := address["address_street2"]; ok {
				t.Error("an empty address part was reported")
			}
			createdBy := company["created_by"].(map[string]any)
			if createdBy["workspace_member_id"] != memberID || createdBy["source"] != "API" {
				t.Errorf("created_by = %v", createdBy)
			}
			if _, ok := company["updated_by"].(map[string]any)["workspace_member_id"]; ok {
				t.Error("a null member was reported")
			}
			if company["position"] != json.Number("2") || company["account_owner_id"] != memberID {
				t.Errorf("position/owner = %v / %v", company["position"], company["account_owner_id"])
			}
			custom := company["custom_fields"].(map[string]any)
			if custom["employees"] != json.Number("12") || custom["idealCustomerProfile"] != true {
				t.Errorf("custom_fields = %v", custom)
			}
			if !reflect.DeepEqual(custom["notes"], map[string]any{"markdown": "hello"}) {
				t.Errorf("rich text = %v", custom["notes"])
			}
			if custom["homepage"] != plainAddr {
				t.Errorf("homepage = %v", custom["homepage"])
			}
			if _, ok := custom["avatar"]; ok {
				t.Error("a signed address was reported")
			}
			if file := custom["cv"].([]any)[0].(map[string]any); file["url"] != nil || file["fileId"] != otherID {
				t.Errorf("file = %v", file)
			}
			for _, banned := range []string{"addressOld", "searchVector", "address_old", "deleted_at"} {
				if _, ok := company[banned]; ok {
					t.Errorf("%s was reported", banned)
				}
				if _, ok := custom[banned]; ok {
					t.Errorf("%s was reported in custom_fields", banned)
				}
			}
			if strings.Contains(fmt.Sprint(company), signedToken) {
				t.Error("a file token was reported")
			}
		})
	}
}

func TestCompanyLeavesOutAnOversizedValueAndNamesIt(t *testing.T) {
	huge := strings.Repeat("x", maxValueString+1)
	body := `{"data":{"company":{"id":"` + companyID + `","name":"Bike","note":"` + huge + `","fine":"ok",` +
		`"address":{"addressCity":"` + huge + `"}}}}`
	company := companyOutput(t, invokeCompaniesGet, `{"id":"`+companyID+`"}`, body)
	custom := company["custom_fields"].(map[string]any)
	if _, ok := custom["note"]; ok || custom["fine"] != "ok" {
		t.Errorf("custom_fields = %v", custom)
	}
	if _, ok := company["address"]; ok {
		t.Error("an oversized address was reported")
	}
	if got := fmt.Sprint(company["omitted_fields"]); got != "[address custom_fields.note]" {
		t.Errorf("omitted_fields = %s", got)
	}
}
