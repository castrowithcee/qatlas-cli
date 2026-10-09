package lexware

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	orgA = "aa93e8a8-2aa3-470b-b914-caad8a255dd8"
	orgB = "bb93e8a8-2aa3-470b-b914-caad8a255dd8"
)

// resetOrganizations isolates a test from the process-local organization cache.
func resetOrganizations(t *testing.T) {
	t.Helper()
	clear := func() {
		organizationsMu.Lock()
		organizations = map[string]*organizationEntry{}
		organizationsMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

func boundConnection(name, target string) *config.Resolved {
	resolved := resolvedConnection(name, "lexware-key", primaryEnv)
	resolved.Target = target
	return resolved
}

func openBound(t *testing.T, resolved *config.Resolved) *Client {
	t.Helper()
	red := &redact.Redactor{}
	c, err := open(context.Background(), resolved, resolver(red), red, freeLimiter())
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	return c
}

// serveOrganization answers the profile with org and counts profile and business requests.
func serveOrganization(t *testing.T, org string) (profiles, business *atomic.Int32) {
	t.Helper()
	profiles, business = &atomic.Int32{}, &atomic.Int32{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/profile" {
			profiles.Add(1)
			return jsonResponse(200, `{"organizationId":"`+strings.ToUpper(org)+`","companyName":"Example"}`), nil
		}
		business.Add(1)
		return jsonResponse(200, listBody), nil
	})
	return profiles, business
}

func TestOrganizationTargetValidation(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	const prefix = `version: 1
services:
  main: {provider: lexware, base_url: https://api.lexware.io}
credentials:
  reader: {type: keyring}
connections:
  route:
    service: main
    credential: reader
    target: TARGET
defaults: {}
`
	decode := func(target string) error {
		_, err := config.Decode(strings.NewReader(strings.Replace(prefix, "TARGET", target, 1)), reg)
		return err
	}
	if err := decode("organization/" + strings.ToUpper(orgA)); err != nil {
		t.Errorf("valid target = %v", err)
	}
	for _, bad := range []string{"organization/not-a-uuid", "team/" + orgA, orgA, "organization/" + orgA + "x"} {
		err := decode(bad)
		if err == nil || !strings.Contains(err.Error(), "connections.route.target") || strings.Contains(err.Error(), orgA) {
			t.Errorf("target %q error = %v", bad, err)
		}
	}
	list := strings.Replace(prefix, "target: TARGET", "targets: [organization/"+orgA+"]", 1)
	if _, err := config.Decode(strings.NewReader(list), reg); err == nil {
		t.Error("a target list was accepted")
	}
}

func TestBoundOrganizationReadsTheProfileOncePerProcess(t *testing.T) {
	resetOrganizations(t)
	profiles, business := serveOrganization(t, orgA)
	resolved := boundConnection("bound-once", "organization/"+strings.ToUpper(orgA))
	for i := 0; i < 2; i++ {
		c := openBound(t, resolved)
		if _, err := c.ListInvoices(context.Background(), ListOptions{Size: 1}); err != nil {
			t.Fatalf("ListInvoices() = %v", err)
		}
	}
	if profiles.Load() != 1 || business.Load() != 2 {
		t.Errorf("profile requests = %d, business requests = %d, want 1 and 2", profiles.Load(), business.Load())
	}
}

func TestBoundOrganizationReadsTheProfileOnceUnderConcurrency(t *testing.T) {
	resetOrganizations(t)
	profiles, _ := serveOrganization(t, orgA)
	resolved := boundConnection("bound-concurrent", "organization/"+orgA)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := openBound(t, resolved)
			if _, err := c.ListInvoices(context.Background(), ListOptions{Size: 1}); err != nil {
				t.Errorf("ListInvoices() = %v", err)
			}
		}()
	}
	wg.Wait()
	if profiles.Load() != 1 {
		t.Errorf("profile requests = %d, want 1", profiles.Load())
	}
}

func TestForeignOrganizationIsRefusedBeforeAnyBusinessRequest(t *testing.T) {
	resetOrganizations(t)
	profiles, business := serveOrganization(t, orgB)
	resolved := boundConnection("bound-foreign", "organization/"+orgA)
	for i := 0; i < 2; i++ {
		c := openBound(t, resolved)
		_, err := c.ListInvoices(context.Background(), ListOptions{Size: 1})
		if classOf(err) != provider.ClassPermission {
			t.Fatalf("ListInvoices() = %v, want permission", err)
		}
		if strings.Contains(strings.ToLower(err.Error()), orgA[:8]) || strings.Contains(strings.ToLower(err.Error()), orgB[:8]) {
			t.Errorf("error names an organization: %v", err)
		}
	}
	// Even the profile tool is a business request and is refused for a foreign key.
	if _, err := openBound(t, resolved).GetProfile(context.Background()); classOf(err) != provider.ClassPermission {
		t.Errorf("GetProfile() = %v, want permission", err)
	}
	if business.Load() != 0 || profiles.Load() != 1 {
		t.Errorf("profile requests = %d, business requests = %d, want 1 and 0", profiles.Load(), business.Load())
	}
}

func TestBoundOrganizationDoesNotCacheAFailedProfileRead(t *testing.T) {
	resetOrganizations(t)
	var fail atomic.Bool
	fail.Store(true)
	var profiles, business atomic.Int32
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/profile" {
			profiles.Add(1)
			if fail.Load() {
				return jsonResponse(500, `{}`), nil
			}
			return jsonResponse(200, `{"organizationId":"`+orgA+`"}`), nil
		}
		business.Add(1)
		return jsonResponse(200, listBody), nil
	})
	resolved := boundConnection("bound-retry", "organization/"+orgA)
	if _, err := openBound(t, resolved).ListInvoices(context.Background(), ListOptions{Size: 1}); classOf(err) != provider.ClassProviderError {
		t.Fatalf("ListInvoices() = %v, want provider-error", err)
	}
	if business.Load() != 0 {
		t.Fatal("a business request followed a failed profile read")
	}
	fail.Store(false)
	if _, err := openBound(t, resolved).ListInvoices(context.Background(), ListOptions{Size: 1}); err != nil {
		t.Fatalf("ListInvoices() = %v", err)
	}
	if profiles.Load() != 2 || business.Load() != 1 {
		t.Errorf("profile requests = %d, business requests = %d, want 2 and 1", profiles.Load(), business.Load())
	}
}

func TestBoundOrganizationCoversEveryBusinessRequestOfAMutation(t *testing.T) {
	resetOrganizations(t)
	_, business := serveOrganization(t, orgB)
	c := openBound(t, boundConnection("bound-post", "organization/"+orgA))
	err := c.post(context.Background(), "create invoice", "", "/v1/invoices", nil, map[string]string{}, nil, "")
	if classOf(err) != provider.ClassPermission || business.Load() != 0 {
		t.Errorf("post() = %v, business requests = %d", err, business.Load())
	}
}

func TestUnboundConnectionNeverReadsTheProfile(t *testing.T) {
	resetOrganizations(t)
	profiles, business := serveOrganization(t, orgB)
	c, _ := client(t)
	if _, err := c.ListInvoices(context.Background(), ListOptions{Size: 1}); err != nil {
		t.Fatalf("ListInvoices() = %v", err)
	}
	if class := c.testConnection(context.Background()); class != provider.ClassOK {
		t.Errorf("testConnection() = %s", class)
	}
	if profiles.Load() != 0 || business.Load() != 2 {
		t.Errorf("profile requests = %d, business requests = %d, want 0 and 2", profiles.Load(), business.Load())
	}
}

func TestConnectionTestChecksTheBoundOrganization(t *testing.T) {
	for _, tt := range []struct {
		name   string
		actual string
		want   provider.Class
		wantBS int32
	}{
		{"matching", orgA, provider.ClassOK, 1},
		{"foreign", orgB, provider.ClassPermission, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetOrganizations(t)
			_, business := serveOrganization(t, tt.actual)
			red := &redact.Redactor{}
			class, err := TestConnection(context.Background(), boundConnection("bound-test-"+tt.name, "organization/"+orgA), resolver(red), red)
			if err != nil || class != tt.want || business.Load() != tt.wantBS {
				t.Errorf("TestConnection() = %s, %v, business requests = %d", class, err, business.Load())
			}
		})
	}
}

func TestOpenRefusesAnUnusableOrganizationTarget(t *testing.T) {
	red := &redact.Redactor{}
	for _, target := range []string{"organization/x", "other/" + orgA} {
		if _, err := open(context.Background(), boundConnection("bound-bad", target), resolver(red), red, freeLimiter()); err == nil ||
			strings.Contains(err.Error(), orgA) {
			t.Errorf("open(%q) = %v", target, err)
		}
	}
	two := boundConnection("bound-two", "organization/"+orgA)
	two.Targets = []string{"organization/" + orgB}
	if _, err := open(context.Background(), two, resolver(red), red, freeLimiter()); err == nil {
		t.Error("open() accepted two organizations")
	}
}

func TestSuggestTargetReadsTheProfileOnceForAnUnboundConnection(t *testing.T) {
	resetOrganizations(t)
	profiles, business := serveOrganization(t, orgA)
	red := &redact.Redactor{}
	got, err := SuggestTarget(context.Background(), resolvedConnection("suggest-unbound", "lexware-key", primaryEnv), resolver(red), red)
	if err != nil {
		t.Fatalf("SuggestTarget() = %v", err)
	}
	if want := "organization/" + orgA; got != want {
		t.Errorf("SuggestTarget() = %q, want %q", got, want)
	}
	if profiles.Load() != 1 || business.Load() != 0 {
		t.Errorf("profile requests = %d, business requests = %d, want 1 and 0", profiles.Load(), business.Load())
	}
}

func TestSuggestTargetStaysSilentForABoundConnection(t *testing.T) {
	resetOrganizations(t)
	profiles, business := serveOrganization(t, orgA)
	red := &redact.Redactor{}
	got, err := SuggestTarget(context.Background(), boundConnection("suggest-bound", "organization/"+orgB), resolver(red), red)
	if err != nil || got != "" {
		t.Errorf("SuggestTarget() = %q, %v, want empty", got, err)
	}
	if profiles.Load()+business.Load() != 0 {
		t.Error("a bound connection caused requests")
	}
}

func TestSuggestTargetIgnoresAnInvalidOrganization(t *testing.T) {
	resetOrganizations(t)
	serveOrganization(t, "not-a-uuid")
	red := &redact.Redactor{}
	got, err := SuggestTarget(context.Background(), resolvedConnection("suggest-invalid", "lexware-key", primaryEnv), resolver(red), red)
	if err != nil || got != "" {
		t.Errorf("SuggestTarget() = %q, %v, want empty", got, err)
	}
}

func TestRegistryOffersTheLexwareSuggester(t *testing.T) {
	resetOrganizations(t)
	serveOrganization(t, orgA)
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	red := &redact.Redactor{}
	resolved := resolvedConnection("suggest-registry", "lexware-key", primaryEnv)
	resolved.Provider = Provider
	got, err := reg.SuggestTarget(context.Background(), resolved, resolver(red), red)
	if err != nil || got != "organization/"+orgA {
		t.Errorf("SuggestTarget() = %q, %v", got, err)
	}
}
