package lexware

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const templateID = "7a1d2c3b-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

const templateSettings = `"recurringTemplateSettings":{"id":"` + invoiceID + `","startDate":"2026-06-01",
 "endDate":"2027-06-01","finalize":true,"shippingType":"service","executionInterval":"MONTHLY",
 "nextExecutionDate":"2026-11-01","lastExecutionFailed":true,"lastExecutionErrorMessage":"` + bodyCanary + `",
 "executionStatus":"ACTIVE","retroactiveInvoice":false}`

func templateEntry(id string) string {
	return `{"id":"` + id + `","organizationId":"aa93e8a8-2aa3-470b-b914-caad8a255dd8",
 "createdDate":"2026-05-14T16:52:21.000+02:00","updatedDate":"2026-05-15T10:00:00.000+02:00","version":2,
 "address":{"contactId":"777c7793-9fbb-4ec7-9254-0619c199761e","name":"Bike & Ride GmbH"},
 "title":"Wartung","paymentTermLabel":"` + bodyCanary + `",
 "totalPrice":{"currency":"EUR","totalNetAmount":100,"totalGrossAmount":119,"totalTaxAmount":19},` + templateSettings + `}`
}

func templateDetail(id string) string {
	return `{"id":"` + id + `","version":2,"language":"de","title":"Wartung","introduction":"Einleitung","remark":"Ende",
 "paymentTermLabel":"14 Tage","address":{"contactId":"777c7793-9fbb-4ec7-9254-0619c199761e","name":"Bike & Ride GmbH"},
 "shippingConditions":{"shippingType":"service"},` + pricedExtra + `,` + templateSettings + `}`
}

func TestRecurringTemplateListSendsExactQueryAndMapsSettings(t *testing.T) {
	var requests []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path+"?"+request.URL.RawQuery)
		return jsonResponse(http.StatusOK, `{"content":[`+templateEntry(templateID)+`],"first":true,"last":false,
 "totalPages":3,"totalElements":50,"numberOfElements":1,"size":25,"number":1}`), nil
	})
	c, _ := client(t)
	got, err := c.ListRecurringTemplates(context.Background(), RecurringTemplateListOptions{
		Page: 1, Sort: "next_execution_date", Direction: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /v1/recurring-templates?page=1&size=25&sort=nextExecutionDate%2CASC"}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	if got.Page != 1 || got.Size != 25 || got.TotalPages != 3 || got.TotalElements != 50 || got.LastPage || len(got.Templates) != 1 {
		t.Fatalf("result = %#v", got)
	}
	entry := got.Templates[0]
	retroactive := false
	if entry.ID != templateID || entry.Title != "Wartung" || entry.Currency != "EUR" || entry.TotalGrossAmount != "119" ||
		entry.Contact == nil || entry.Contact.Name != "Bike & Ride GmbH" ||
		entry.Settings == nil || entry.Settings.ExecutionInterval != "MONTHLY" || entry.Settings.NextExecutionDate != "2026-11-01" ||
		entry.Settings.ExecutionStatus != "ACTIVE" || !entry.Settings.LastExecutionFailed || !entry.Settings.Finalize ||
		entry.Settings.RetroactiveInvoice == nil || *entry.Settings.RetroactiveInvoice != retroactive {
		t.Errorf("entry = %#v settings = %#v", entry, entry.Settings)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "ErrorMessage") || strings.Contains(string(encoded), "error_message") {
		t.Errorf("output carries the execution error message: %s", encoded)
	}
}

func TestRecurringTemplateListDefaultsAndBounds(t *testing.T) {
	var query string
	serve(t, func(request *http.Request) (*http.Response, error) {
		query = request.URL.RawQuery
		return jsonResponse(http.StatusOK, `{"content":[],"last":true,"size":25}`), nil
	})
	c, _ := client(t)
	if _, err := c.ListRecurringTemplates(context.Background(), RecurringTemplateListOptions{}); err != nil || query != "page=0&size=25&sort=createdDate%2CDESC" {
		t.Errorf("query = %q, %v", query, err)
	}
	refuse(t)
	for _, o := range []RecurringTemplateListOptions{
		{Size: 101}, {Size: -1}, {Page: 201}, {Page: -1}, {Sort: "title"}, {Direction: "up"},
	} {
		if _, err := c.ListRecurringTemplates(context.Background(), o); err == nil {
			t.Errorf("options %+v were accepted", o)
		}
	}
}

func TestRecurringTemplateListRejectsEntryWithoutUsableID(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"content":[{"id":"x"}]}`), nil
	})
	c, _ := client(t)
	_, err := c.ListRecurringTemplates(context.Background(), RecurringTemplateListOptions{})
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestRecurringTemplateGetReadsOneFixedPathAndMaps(t *testing.T) {
	var requests []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path+"?"+request.URL.RawQuery)
		return jsonResponse(http.StatusOK, templateDetail(templateID)), nil
	})
	c, _ := client(t)
	got, err := c.GetRecurringTemplate(context.Background(), templateID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /v1/recurring-templates/" + templateID + "?"}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	if got.ID != templateID || got.PaymentTermLabel != "14 Tage" || got.Title != "Wartung" || got.TaxType != "net" ||
		got.TotalNetAmount != "200" || got.Contact == nil || got.Shipping == nil || len(got.LineItems) != 1 ||
		got.Settings == nil || got.Settings.ExecutionInterval != "MONTHLY" || got.Settings.RetroactiveInvoice == nil {
		t.Errorf("template = %#v", got)
	}
}

func TestRecurringTemplateGetGuardsTheIdentifier(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	for _, id := range []string{"", "42", "../../v1/contacts", templateID + "0"} {
		if _, err := c.GetRecurringTemplate(context.Background(), id); err == nil {
			t.Errorf("get(%q) was accepted", id)
		}
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, templateDetail(invoiceID)), nil
	})
	c, _ = client(t)
	_, err := c.GetRecurringTemplate(context.Background(), templateID)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	})
	c, _ = client(t)
	_, err = c.GetRecurringTemplate(context.Background(), templateID)
	if classOf(err) != provider.ClassNotFound ||
		!strings.Contains(err.Error(), "Lexware does not hold this recurring template or does not show it to this API key") ||
		strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), templateID) {
		t.Errorf("err = %v", err)
	}
}

func TestRecurringTemplateToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/recurring-templates" {
			return jsonResponse(http.StatusOK, `{"content":[`+templateEntry(templateID)+`],"last":true,"size":25,"totalPages":1,"totalElements":1}`), nil
		}
		return jsonResponse(http.StatusOK, templateDetail(templateID)), nil
	})
	stubLimiter(t, primaryKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	for _, call := range []struct{ tool, arguments, want string }{
		{"lexware.recurringtemplates.list", `{"size":25,"sort":"created_date","direction":"desc"}`, `"templates"`},
		{"lexware.recurringtemplates.get", `{"id":"` + templateID + `"}`, `"payment_term_label":"14 Tage"`},
	} {
		got, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: call.tool, Connection: "lexware-primary", Arguments: json.RawMessage(call.arguments)})
		if err != nil || !strings.Contains(string(got.Result), call.want) {
			t.Fatalf("%s = %s, %v", call.tool, got.Result, err)
		}
	}
	refuse(t)
	for _, call := range []struct{ tool, arguments string }{
		{"lexware.recurringtemplates.list", `{"size":101}`},
		{"lexware.recurringtemplates.list", `{"page":201}`},
		{"lexware.recurringtemplates.list", `{"sort":"title"}`},
		{"lexware.recurringtemplates.get", `{"id":"../../v1/contacts"}`},
	} {
		if _, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: call.tool, Connection: "lexware-primary", Arguments: json.RawMessage(call.arguments)}); err == nil {
			t.Errorf("core accepted %s %s", call.tool, call.arguments)
		}
	}
}
