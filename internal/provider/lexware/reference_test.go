package lexware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const profileBody = `{"organizationId":"org-1","companyName":"Muster GmbH","created":{"userId":"user-canary",
  "userName":"Erika Canary","userEmail":"erika.canary@example.invalid","date":"2026-01-01"},"connectionId":"conn-canary",
  "features":["INVOICING"],"businessFeatures":["BASIC"],"subscriptionStatus":"ACTIVE","taxType":"net",
  "smallBusiness":false,"distanceSalesPrinciple":"ORIGIN"}`

func serveOne(t *testing.T, wantPath, body string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	serve(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != wantPath || request.URL.RawQuery != "" {
			t.Errorf("request = %s %s", request.Method, request.URL.Redacted())
		}
		return jsonResponse(http.StatusOK, body), nil
	})
	return &calls
}

func TestProfileOutputIsAnAllowlist(t *testing.T) {
	calls := serveOne(t, "/v1/profile", profileBody)
	c, _ := client(t)
	profile, err := c.GetProfile(context.Background())
	if err != nil || calls.Load() != 1 {
		t.Fatalf("GetProfile() = %v, calls %d", err, calls.Load())
	}
	out := fmt.Sprintf("%+v", *profile)
	for _, leak := range []string{"canary", "Erika", "user-", "conn-", "created", "ACTIVE"} {
		if strings.Contains(out, leak) {
			t.Errorf("profile leaks %q: %s", leak, out)
		}
	}
	if profile.OrganizationID != "org-1" || profile.CompanyName != "Muster GmbH" || profile.TaxType != "net" ||
		profile.SmallBusiness == nil || *profile.SmallBusiness || profile.DistanceSalesPrinciple != "ORIGIN" ||
		len(profile.Features) != 1 || len(profile.BusinessFeatures) != 1 {
		t.Errorf("profile = %+v", profile)
	}
}

func TestReferenceListsReadOneFixedPath(t *testing.T) {
	ctx := context.Background()
	var c *Client
	calls := serveOne(t, "/v1/countries", `[{"countryCode":"DE","countryNameDE":"Deutschland","countryNameEN":"Germany","taxClassification":"de"}]`)
	c, _ = client(t)
	if got, truncated, err := c.ListCountries(ctx); err != nil || truncated || len(got) != 1 || got[0].NameEN != "Germany" || calls.Load() != 1 {
		t.Errorf("countries = %+v %v %v", got, truncated, err)
	}
	calls = serveOne(t, "/v1/payment-conditions", `[{"id":"p1","paymentTermLabelTemplate":"14 Tage","paymentTermDuration":14,
	  "paymentDiscountConditions":{"discountPercentage":2.5,"discountRange":7},"organizationDefault":true}]`)
	c, _ = client(t)
	if got, _, err := c.ListPaymentConditions(ctx); err != nil || len(got) != 1 || got[0].DurationDays != 14 ||
		got[0].DiscountPercentage != "2.5" || got[0].DiscountRangeDays != 7 || !got[0].OrganizationDefault || calls.Load() != 1 {
		t.Errorf("conditions = %+v %v", got, err)
	}
	calls = serveOne(t, "/v1/print-layouts", `[{"id":"l1","name":"Standard","default":true}]`)
	c, _ = client(t)
	if got, _, err := c.ListPrintLayouts(ctx); err != nil || len(got) != 1 || !got[0].Default || calls.Load() != 1 {
		t.Errorf("layouts = %+v %v", got, err)
	}
}

func TestPostingCategoriesFilterLocally(t *testing.T) {
	calls := serveOne(t, "/v1/posting-categories", `[{"id":"a","name":"A","type":"income","contactRequired":true},
	  {"id":"b","name":"B","type":"outgo","splitAllowed":true,"groupName":"G"}]`)
	c, _ := client(t)
	got, _, err := c.ListPostingCategories(context.Background(), "outgo")
	if err != nil || len(got) != 1 || got[0].ID != "b" || !got[0].SplitAllowed || calls.Load() != 1 {
		t.Errorf("filtered = %+v %v", got, err)
	}
	if got, _, _ := c.ListPostingCategories(context.Background(), ""); len(got) != 2 {
		t.Errorf("unfiltered = %+v", got)
	}
	if _, _, err := c.ListPostingCategories(context.Background(), "x"); err == nil {
		t.Error("an unknown type was accepted")
	}
}

func TestReferenceListsAreCapped(t *testing.T) {
	build := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(`{"id":"i%d","countryCode":"C%d"}`, i, i)
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	serveOne(t, "/v1/countries", build(maxCountries+5))
	c, _ := client(t)
	if got, truncated, err := c.ListCountries(context.Background()); err != nil || !truncated || len(got) != maxCountries {
		t.Errorf("countries = %d %v %v", len(got), truncated, err)
	}
	serveOne(t, "/v1/print-layouts", build(maxReferenceItems))
	c, _ = client(t)
	if got, truncated, err := c.ListPrintLayouts(context.Background()); err != nil || truncated || len(got) != maxReferenceItems {
		t.Errorf("exact cap = %d %v %v", len(got), truncated, err)
	}
	serveOne(t, "/v1/payment-conditions", build(maxReferenceItems+1))
	c, _ = client(t)
	if got, truncated, _ := c.ListPaymentConditions(context.Background()); !truncated || len(got) != maxReferenceItems {
		t.Errorf("conditions = %d %v", len(got), truncated)
	}
}

func TestPaymentRequiredHidesProviderText(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusPaymentRequired, `{"message":"PROVIDER-CANARY"}`), nil
	})
	c, _ := client(t)
	_, _, err := c.ListPrintLayouts(context.Background())
	if err == nil || classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "CANARY") {
		t.Errorf("err = %v", err)
	}
}
