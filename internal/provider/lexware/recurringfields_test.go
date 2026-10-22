package lexware

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	templateTool      = "lexware.recurring-templates.get"
	templateListTool  = "lexware.recurring-templates.list"
	templateOrgID     = "aa93e8a8-2aa3-470b-b914-caad8a255dd8"
	templateSettings2 = "9c5b8bde-7d36-49e8-af5c-4fbe7dc9fa01"
)

// documentedTemplate is the sample response of the Lexware documentation, with its Lexware-internal notes.
const documentedTemplate = `{
 "id":"` + templateID + `","organizationId":"` + templateOrgID + `",
 "createdDate":"2023-02-10T09:00:00.000+01:00","updatedDate":"2023-02-10T09:00:00.000+01:00","version":0,
 "language":"de","archived":false,
 "address":{"contactId":"464f4881-7a8c-4dc4-87de-7c6fd9a506b8","name":"Bike & Ride GmbH & Co. KG",
  "supplement":"Gebäude 10","street":"Musterstraße 42","city":"Freiburg","zip":"79112","countryCode":"DE",
  "contactPerson":"` + bodyCanary + `"},
 "lineItems":[
  {"id":"97b98491-e953-4dc9-97a9-ae437a8052b4","type":"material","name":"Abus Kabelschloss Primo 590 ","quantity":2,
   "unitName":"Stück","unitPrice":{"currency":"EUR","netAmount":13.4,"grossAmount":15.95,"taxRatePercentage":19},
   "discountPercentage":50,"lineItemAmount":13.4},
  {"id":null,"type":"custom","name":"Energieriegel Testpaket","description":null,"quantity":1,"unitName":"Stück",
   "unitPrice":{"currency":"EUR","netAmount":5,"grossAmount":5,"taxRatePercentage":0},"discountPercentage":0,"lineItemAmount":5},
  {"id":"not-a-uuid","type":"text","name":"Freitextposition","optional":true,"subItems":[{"name":"x"}]}],
 "totalPrice":{"currency":"EUR","totalNetAmount":26.72,"totalGrossAmount":29.85,"totalTaxAmount":3.13,
  "totalDiscountAbsolute":1.5,"totalDiscountPercentage":2.25},
 "taxAmounts":[{"taxRatePercentage":0,"taxAmount":0,"netAmount":5},
  {"taxRatePercentage":19,"taxAmount":2.55,"netAmount":13.4}],
 "taxConditions":{"taxType":"net","taxSubType":"distanceSales","taxTypeNote":"` + bodyCanary + `"},
 "paymentConditions":{"paymentTermLabel":"10 Tage - 3 %, 30 Tage netto",
  "paymentTermLabelTemplate":"{discountRange} Tage -{discount}, {paymentRange} Tage netto","paymentTermDuration":30,
  "paymentDiscountConditions":{"discountPercentage":3,"discountRange":10}},
 "title":"Rechnung","introduction":"x","remark":"y",
 "recurringTemplateSettings":{"id":"` + templateSettings2 + `","startDate":"2023-03-01","endDate":"2023-06-30",
  "finalize":true,"shippingType":"service","retroactiveInvoice":false,"executionInterval":"MONTHLY",
  "nextExecutionDate":"2023-03-01","lastExecutionDate":"2023-02-01","lastExecutionFailed":false,
  "lastExecutionErrorMessage":"` + bodyCanary + `","executionStatus":"ACTIVE"}}`

// documentedTemplateEntry is a list entry of the documentation: only the data the list reports.
const documentedTemplateEntry = `{"id":"` + templateID + `","organizationId":"` + templateOrgID + `","title":"Rechnung",
 "createdDate":"2023-02-10T14:35:40.642+01:00","updatedDate":"2023-02-10T14:36:49.741+01:00",
 "address":{"contactId":"464f4881-7a8c-4dc4-87de-7c6fd9a506b8","name":"Bike & Ride GmbH & Co. KG"},
 "totalPrice":{"currency":"EUR","totalNetAmount":251.26,"totalGrossAmount":299},
 "paymentConditions":{"paymentTermLabel":"10 Tage abzüglich 2 % Skonto",
  "paymentTermLabelTemplate":"{paymentRange} Tage abzüglich {discount} Skonto","paymentTermDuration":10,
  "paymentDiscountConditions":{"discountPercentage":2,"discountRange":10}},
 "recurringTemplateSettings":{"id":"` + templateSettings2 + `","startDate":"2023-04-01","endDate":"2024-04-01",
  "finalize":false,"shippingType":"serviceperiod","retroactiveInvoice":false,"executionInterval":"QUARTERLY",
  "nextExecutionDate":null,"lastExecutionDate":"2023-01-01","lastExecutionFailed":false,"executionStatus":"PAUSED"}}`

// invokeTemplate returns one template tool's output as a generic object through the application core, so
// the output schema is enforced on the way.
func invokeTemplate(t *testing.T, tool, arguments, body string) map[string]any {
	t.Helper()
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	stubLimiter(t, primaryKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	got, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: tool, Connection: "lexware-primary", Arguments: json.RawMessage(arguments)})
	if err != nil {
		t.Fatalf("%s = %v", tool, err)
	}
	var out map[string]any
	if err := json.Unmarshal(got.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRecurringTemplateGetReportsEveryDocumentedMember(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, documentedTemplate), nil
	})
	c, _ := client(t)
	template, err := c.GetRecurringTemplate(context.Background(), templateID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(template)
	out := string(encoded)
	for _, want := range []string{
		`"organization_id":"` + templateOrgID + `"`, `"contact_person":"` + bodyCanary + `"`,
		`"id":"97b98491-e953-4dc9-97a9-ae437a8052b4"`, `"unit_currency":"EUR"`,
		`"total_discount_absolute":1.5`, `"total_discount_percentage":2.25`,
		`"tax_amounts":[{"tax_rate_percentage":0,"tax_amount":0,"net_amount":5},` +
			`{"tax_rate_percentage":19,"tax_amount":2.55,"net_amount":13.4}]`,
		`"tax_sub_type":"distanceSales"`, `"tax_type_note":"` + bodyCanary + `"`,
		`"payment_term_label":"10 Tage - 3 %, 30 Tage netto"`,
		`"payment_conditions":{"payment_term_label":"10 Tage - 3 %, 30 Tage netto",` +
			`"payment_term_label_template":"{discountRange} Tage -{discount}, {paymentRange} Tage netto",` +
			`"payment_term_duration":30,"payment_discount_conditions":{"discount_percentage":3,"discount_range":10}}`,
		`"settings":{"id":"` + templateSettings2 + `"`, `"last_execution_date":"2023-02-01"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s\n%s", want, out)
		}
	}
	for _, absent := range []string{"ErrorMessage", "error_message", `"optional"`, `"alternative"`, "sub_items",
		"not-a-uuid", "archived"} {
		if strings.Contains(out, absent) {
			t.Errorf("output carries %s\n%s", absent, out)
		}
	}
}

func TestRecurringTemplateListReportsEveryDocumentedMember(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"content":[`+documentedTemplateEntry+`],"last":true,"size":25}`), nil
	})
	c, _ := client(t)
	list, err := c.ListRecurringTemplates(context.Background(), RecurringTemplateListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(list.Templates[0])
	encoded = bytes.ReplaceAll(encoded, []byte(`\u0026`), []byte("&"))
	want := `{"id":"` + templateID + `","organization_id":"` + templateOrgID + `",` +
		`"created_date":"2023-02-10T14:35:40.642+01:00","updated_date":"2023-02-10T14:36:49.741+01:00",` +
		`"title":"Rechnung","payment_term_label":"10 Tage abzüglich 2 % Skonto",` +
		`"payment_conditions":{"payment_term_label":"10 Tage abzüglich 2 % Skonto",` +
		`"payment_term_label_template":"{paymentRange} Tage abzüglich {discount} Skonto","payment_term_duration":10,` +
		`"payment_discount_conditions":{"discount_percentage":2,"discount_range":10}},` +
		`"currency":"EUR","total_net_amount":251.26,"total_gross_amount":299,` +
		`"contact":{"id":"464f4881-7a8c-4dc4-87de-7c6fd9a506b8","name":"Bike & Ride GmbH & Co. KG"},` +
		`"settings":{"id":"` + templateSettings2 + `","start_date":"2023-04-01","end_date":"2024-04-01","finalize":false,` +
		`"shipping_type":"serviceperiod","execution_interval":"QUARTERLY","last_execution_date":"2023-01-01",` +
		`"last_execution_failed":false,"execution_status":"PAUSED","retroactive_invoice":false}}`
	if string(encoded) != want {
		t.Errorf("entry:\n got %s\nwant %s", encoded, want)
	}
}

func TestRecurringTemplateAnswersWithoutNewMembersKeepTheirFormerOutput(t *testing.T) {
	settings := `"recurringTemplateSettings":{"startDate":"2026-06-01","finalize":true,"executionInterval":"MONTHLY",
 "nextExecutionDate":"2026-11-01","lastExecutionFailed":false,"executionStatus":"ACTIVE"}`
	wantSettings := `"settings":{"start_date":"2026-06-01","finalize":true,"execution_interval":"MONTHLY",` +
		`"next_execution_date":"2026-11-01","last_execution_failed":false,"execution_status":"ACTIVE"}`
	label := `"paymentConditions":{"paymentTermLabel":"14 Tage"}`
	body := `{"id":"` + templateID + `","version":2,"language":"de","title":"Wartung",` + label + `,
 "address":{"name":"Bike Ride GmbH"},"taxConditions":{"taxType":"net"},
 "totalPrice":{"currency":"EUR","totalNetAmount":200,"totalGrossAmount":238,"totalTaxAmount":38},
 "lineItems":[{"type":"custom","name":"Beratung","quantity":2,"unitName":"Std",
  "unitPrice":{"netAmount":100,"grossAmount":119,"taxRatePercentage":19},"discountPercentage":0,"lineItemAmount":200}],` +
		settings + `}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	c, _ := client(t)
	got, err := c.GetRecurringTemplate(context.Background(), templateID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	want := `{"id":"` + templateID + `","version":2,"language":"de","title":"Wartung","payment_term_label":"14 Tage",` +
		`"tax_type":"net","currency":"EUR","total_net_amount":200,"total_gross_amount":238,"total_tax_amount":38,` +
		`"contact":{"name":"Bike Ride GmbH"},` + wantSettings + `,` +
		`"line_items":[{"type":"custom","name":"Beratung","quantity":2,"unit_name":"Std","unit_net_amount":100,` +
		`"unit_gross_amount":119,"tax_rate_percentage":19,"discount_percentage":0,"amount":200}],` +
		`"payment_conditions":{"payment_term_label":"14 Tage"}}`
	if string(encoded) != want {
		t.Errorf("get output changed:\n got %s\nwant %s", encoded, want)
	}

	entry := `{"id":"` + templateID + `","version":2,"title":"Wartung","address":{"name":"Bike Ride GmbH"},
 "totalPrice":{"currency":"EUR","totalNetAmount":200,"totalGrossAmount":238},` + label + `,` + settings + `}`
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"content":[`+entry+`],"last":true,"size":25}`), nil
	})
	c, _ = client(t)
	list, err := c.ListRecurringTemplates(context.Background(), RecurringTemplateListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(list.Templates[0])
	want = `{"id":"` + templateID + `","version":2,"title":"Wartung","payment_term_label":"14 Tage",` +
		`"payment_conditions":{"payment_term_label":"14 Tage"},` +
		`"currency":"EUR","total_net_amount":200,"total_gross_amount":238,"contact":{"name":"Bike Ride GmbH"},` +
		wantSettings + `}`
	if string(encoded) != want {
		t.Errorf("list output changed:\n got %s\nwant %s", encoded, want)
	}
}

func TestRecurringTemplateDropsMalformedIdentifiers(t *testing.T) {
	body := `{"id":"` + templateID + `","organizationId":"x","recurringTemplateSettings":{"id":"y","finalize":true},
 "lineItems":[{"id":"z","type":"custom","name":"A"}]}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	c, _ := client(t)
	got, err := c.GetRecurringTemplate(context.Background(), templateID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OrganizationID != "" || got.Settings.ID != "" || got.LineItems[0].ID != "" {
		t.Errorf("malformed identifiers were passed on: %#v", got)
	}
}

func TestRecurringTemplateSortsByLastExecutionDate(t *testing.T) {
	var query string
	serve(t, func(request *http.Request) (*http.Response, error) {
		query = request.URL.RawQuery
		return jsonResponse(http.StatusOK, `{"content":[],"last":true,"size":25}`), nil
	})
	c, _ := client(t)
	_, err := c.ListRecurringTemplates(context.Background(), RecurringTemplateListOptions{Sort: "last_execution_date", Direction: "desc"})
	if err != nil || query != "page=0&size=25&sort=lastExecutionDate%2CDESC" {
		t.Errorf("query = %q, %v", query, err)
	}
}

func TestEveryRecurringTemplateOutputMemberIsInSchemaAndFields(t *testing.T) {
	registry := registry(t)
	for _, call := range []struct {
		tool, arguments, body string
		nested                []string
	}{
		{templateTool, `{"id":"` + templateID + `"}`, documentedTemplate, []string{"settings", "payment_conditions"}},
		{templateListTool, `{}`, `{"content":[` + documentedTemplateEntry + `],"last":true,"size":25}`, nil},
	} {
		t.Run(call.tool, func(t *testing.T) {
			out := invokeTemplate(t, call.tool, call.arguments, call.body)
			if call.tool == templateListTool {
				templates := out["templates"].([]any)
				out = templates[0].(map[string]any)
			}
			var schema map[string]any
			fields := map[string]bool{}
			for _, d := range registry.Provider(Provider) {
				if d.ID != call.tool {
					continue
				}
				if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
					t.Fatal(err)
				}
				for _, f := range d.Fields {
					fields[f.Name] = true
				}
			}
			properties, _ := schema["properties"].(map[string]any)
			if call.tool == templateListTool {
				items := properties["templates"].(map[string]any)["items"].(map[string]any)
				properties = items["properties"].(map[string]any)
			}
			var missing []string
			for key := range out {
				if _, ok := properties[key]; !ok {
					missing = append(missing, key+" (schema)")
				}
				if call.tool == templateTool && !fields[key] {
					missing = append(missing, key+" (fields)")
				}
			}
			if call.tool == templateTool {
				for key := range properties {
					if !fields[key] {
						missing = append(missing, key+" (schema without field)")
					}
				}
			} else if !fields["templates"] {
				missing = append(missing, "templates (fields)")
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("not in schema or fields: %v", missing)
			}
			// Nested objects: every member of the answer is a schema member.
			for _, name := range []string{"settings", "payment_conditions"} {
				object, _ := out[name].(map[string]any)
				nested, _ := properties[name].(map[string]any)["properties"].(map[string]any)
				for key := range object {
					if _, ok := nested[key]; !ok {
						t.Errorf("%s.%s is not in the schema", name, key)
					}
				}
			}
		})
	}
}
