package lexware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// storedArticle carries members Qatlas does not model (top level and inside price) next to modelled ones.
const storedArticle = `{"id":"` + articleID + `","organizationId":"org-1","createdDate":"2026-01-01T00:00:00.000+01:00",
  "title":"Beratung","description":"alt","type":"SERVICE","articleNumber":"A-100","gtin":"4006381333931",
  "note":"intern","unitName":"Stunde","futureField":{"a":[1,2.50]},
  "price":{"netPrice":84.034482758620689,"grossPrice":99.9,"leadingPrice":"NET","taxRate":19,"currency":"EUR"},
  "archived":false,"version":7}`

const writeAnswer = `{"id":"` + articleID + `","resourceUri":"x","createdDate":"c","updatedDate":"u","version":8}`

type recordedRequest struct {
	method, path string
	body         map[string]json.RawMessage
}

// scripted answers GET with the stored article and every write with reply, and records each request.
func scripted(t *testing.T, get func() (*http.Response, error), write func() (*http.Response, error)) *[]recordedRequest {
	t.Helper()
	var requests []recordedRequest
	serve(t, func(request *http.Request) (*http.Response, error) {
		recorded := recordedRequest{method: request.Method, path: request.URL.Path}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			if len(data) > 0 {
				if err := json.Unmarshal(data, &recorded.body); err != nil {
					t.Errorf("request body is no JSON object: %s", data)
				}
			}
		}
		requests = append(requests, recorded)
		if request.Method == http.MethodGet {
			return get()
		}
		return write()
	})
	return &requests
}

func storedGet() (*http.Response, error) { return jsonResponse(http.StatusOK, storedArticle), nil }
func okWrite() (*http.Response, error)   { return jsonResponse(http.StatusCreated, writeAnswer), nil }

func str(value string) *string { return &value }

func writeCore(t *testing.T) (*application.Core, *atomic.Int32) {
	t.Helper()
	stubLimiter(t, primaryKey)
	cfg := coreConfig()
	cfg.Connections["lexware-writer"] = config.Connection{Service: "lexware", Credential: "primary-key",
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}}
	var reads atomic.Int32
	red := &redact.Redactor{}
	counting := secret.NewWith(func(string) string { reads.Add(1); return primaryKey }, nil, nil, red)
	return application.New(registry(t), cfg, counting, red), &reads
}

func TestCreateArticleSendsExactlyOneControlledPost(t *testing.T) {
	requests := scripted(t, storedGet, okWrite)
	core, _ := writeCore(t)
	result, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "lexware.articles.create", Connection: "lexware-writer", Confirmed: true,
		Arguments: json.RawMessage(`{"title":"Consulting","type":"SERVICE","unit_name":"hour","gtin":"4006381333931",` +
			`"price":{"net_price":120.5,"leading_price":"NET","tax_rate":19}}`),
	})
	if err != nil || !strings.Contains(string(result.Result), `"id":"`+articleID+`"`) ||
		!strings.Contains(string(result.Result), `"version":8`) {
		t.Fatalf("invoke = %s, %v", result.Result, err)
	}
	if len(*requests) != 1 || (*requests)[0].method != http.MethodPost || (*requests)[0].path != "/v1/articles" {
		t.Fatalf("requests = %+v", *requests)
	}
	body := map[string]any{}
	for key, value := range (*requests)[0].body {
		var decoded any
		_ = json.Unmarshal(value, &decoded)
		body[key] = decoded
	}
	want := map[string]any{"title": "Consulting", "type": "SERVICE", "unitName": "hour", "gtin": "4006381333931",
		"price": map[string]any{"netPrice": 120.5, "leadingPrice": "NET", "taxRate": 19.0}}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("payload = %v, want %v", body, want)
	}
}

func TestArticleWritesAreRefusedBeforeIOAndSecretAccess(t *testing.T) {
	refuse(t)
	core, reads := writeCore(t)
	const price = `"price":{"net_price":1,"leading_price":"NET","tax_rate":19}`
	const create = "lexware.articles.create"
	const update = "lexware.articles.update"
	tests := []struct {
		name, operation, args string
		confirm               bool
	}{
		{"create without confirm", create, `{"title":"A","type":"SERVICE","unit_name":"h",` + price + `}`, false},
		{"update without confirm", update, `{"id":"` + articleID + `","title":"A"}`, false},
		{"create missing price", create, `{"title":"A","type":"SERVICE","unit_name":"h"}`, true},
		{"create passthrough field", create, `{"title":"A","type":"SERVICE","unit_name":"h",` + price + `,"archived":true}`, true},
		{"create id", create, `{"id":"` + articleID + `","title":"A","type":"SERVICE","unit_name":"h",` + price + `}`, true},
		{"create bad type", create, `{"title":"A","type":"GOODS","unit_name":"h",` + price + `}`, true},
		{"create long title", create, `{"title":"` + strings.Repeat("x", 256) + `","type":"SERVICE","unit_name":"h",` + price + `}`, true},
		{"create tax rate", create, `{"title":"A","type":"SERVICE","unit_name":"h","price":{"net_price":1,"leading_price":"NET","tax_rate":101}}`, true},
		{"create leading amount missing", create, `{"title":"A","type":"SERVICE","unit_name":"h","price":{"gross_price":1,"leading_price":"NET","tax_rate":19}}`, true},
		{"update bad id", update, `{"id":"../contacts/` + contactID + `","title":"A"}`, true},
		{"update no field", update, `{"id":"` + articleID + `"}`, true},
		{"update passthrough field", update, `{"id":"` + articleID + `","archived":true}`, true},
		{"update bad gtin", update, `{"id":"` + articleID + `","gtin":"12"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: tt.operation, Connection: "lexware-writer", Arguments: json.RawMessage(tt.args), Confirmed: tt.confirm})
			if err == nil {
				t.Fatal("the core accepted the request")
			}
			if !tt.confirm {
				var confirmation *application.ConfirmationRequiredError
				if !errors.As(err, &confirmation) {
					t.Errorf("err = %v, want a confirmation-required error", err)
				}
			}
		})
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
}

func TestUpdateArticleRoundTripsUnchangedMembersWithTheReadVersion(t *testing.T) {
	requests := scripted(t, storedGet, okWrite)
	c, _ := client(t)
	result, err := c.UpdateArticle(context.Background(), articleChanges{ID: articleID, Title: str("Neu"), Note: str("")})
	if err != nil || result.ID != articleID || result.Version != 8 {
		t.Fatalf("UpdateArticle() = %+v, %v", result, err)
	}
	if len(*requests) != 2 || (*requests)[0].method != http.MethodGet || (*requests)[1].method != http.MethodPut ||
		(*requests)[0].path != "/v1/articles/"+articleID || (*requests)[1].path != "/v1/articles/"+articleID {
		t.Fatalf("requests = %+v", *requests)
	}
	var stored map[string]json.RawMessage
	_ = json.Unmarshal([]byte(storedArticle), &stored)
	want := stored
	want["title"] = json.RawMessage(`"Neu"`)
	want["note"] = json.RawMessage(`""`)
	got := (*requests)[1].body
	if len(got) != len(want) {
		t.Errorf("members = %d, want %d", len(got), len(want))
	}
	for key, value := range want {
		if string(got[key]) != string(value) {
			t.Errorf("%s = %s, want %s", key, got[key], value)
		}
	}
	if string(got["version"]) != "7" || string(got["futureField"]) != `{"a":[1,2.50]}` {
		t.Errorf("version = %s, futureField = %s", got["version"], got["futureField"])
	}
}

func TestUpdateArticleReplacesThePricePairAndKeepsUnmodelledPriceMembers(t *testing.T) {
	requests := scripted(t, storedGet, okWrite)
	c, _ := client(t)
	price := &ArticlePrice{GrossPrice: "119", LeadingPrice: "GROSS", TaxRate: "19"}
	if _, err := c.UpdateArticle(context.Background(), articleChanges{ID: articleID, Price: price}); err != nil {
		t.Fatalf("UpdateArticle() = %v", err)
	}
	var sent map[string]json.RawMessage
	_ = json.Unmarshal((*requests)[1].body["price"], &sent)
	want := map[string]string{"grossPrice": "119", "leadingPrice": `"GROSS"`, "taxRate": "19", "currency": `"EUR"`}
	if len(sent) != len(want) {
		t.Fatalf("price = %v", sent)
	}
	for key, value := range want {
		if string(sent[key]) != value {
			t.Errorf("price.%s = %s, want %s", key, sent[key], value)
		}
	}
}

func TestArticleWriteFailuresAreClassifiedWithoutProviderText(t *testing.T) {
	reset := &net.OpError{Op: "read", Err: syscall.ECONNRESET}
	status := func(code int) func() (*http.Response, error) {
		return func() (*http.Response, error) { return jsonResponse(code, `{"message":"`+bodyCanary+`"}`), nil }
	}
	tests := []struct {
		name  string
		reply func() (*http.Response, error)
		class provider.Class
		text  string
		hint  bool
	}{
		{"409", status(409), provider.ClassProviderError, conflictMessage, false},
		{"406", status(406), provider.ClassProviderError, validationMessage, false},
		{"500", status(500), provider.ClassProviderError, "", true},
		{"504", status(504), provider.ClassTimeout, "", true},
		{"timeout", func() (*http.Response, error) { return nil, context.DeadlineExceeded }, provider.ClassTimeout, "", true},
		{"reset", func() (*http.Response, error) { return nil, reset }, provider.ClassUnreachable, "", true},
		{"unusable answer", func() (*http.Response, error) { return jsonResponse(200, "<html>"), nil }, provider.ClassInvalidResponse, "", true},
		{"other article", func() (*http.Response, error) {
			return jsonResponse(200, `{"id":"`+otherArticleID+`","version":9}`), nil
		}, provider.ClassInvalidResponse, "", true},
		{"429", status(429), provider.ClassRateLimited, "", false},
	}
	for _, tt := range tests {
		t.Run("update "+tt.name, func(t *testing.T) {
			requests := scripted(t, storedGet, tt.reply)
			c, _ := client(t)
			_, err := c.UpdateArticle(context.Background(), articleChanges{ID: articleID, Title: str("Neu")})
			if err == nil || classOf(err) != tt.class || strings.Contains(err.Error(), bodyCanary) ||
				strings.Contains(err.Error(), otherArticleID) {
				t.Fatalf("err = %v, want class %q", err, tt.class)
			}
			if got := strings.Contains(err.Error(), articleMayChanged); got != tt.hint {
				t.Errorf("hint = %v, want %v (%v)", got, tt.hint, err)
			}
			if tt.text != "" && !strings.Contains(err.Error(), tt.text) {
				t.Errorf("err = %v, want %q", err, tt.text)
			}
			if len(*requests) != 2 {
				t.Errorf("requests = %d, want one read and exactly one write", len(*requests))
			}
		})
		t.Run("create "+tt.name, func(t *testing.T) {
			requests := scripted(t, storedGet, tt.reply)
			c, _ := client(t)
			_, err := c.CreateArticle(context.Background(), articleChanges{Title: str("A"), Type: str("SERVICE"),
				UnitName: str("h"), Price: &ArticlePrice{NetPrice: "1", LeadingPrice: "NET", TaxRate: "19"}})
			if tt.name == "other article" {
				// A creation answers with a new identifier, so another UUID is a valid answer.
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || classOf(err) != tt.class || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, want class %q", err, tt.class)
			}
			if got := strings.Contains(err.Error(), articleMayExist); got != tt.hint {
				t.Errorf("hint = %v, want %v (%v)", got, tt.hint, err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodPost {
				t.Errorf("requests = %+v, want exactly one POST", *requests)
			}
		})
	}
}

func TestUpdateArticleStopsBeforeTheWriteWhenTheReadIsUnusable(t *testing.T) {
	tests := map[string]func() (*http.Response, error){
		"read fails":     func() (*http.Response, error) { return jsonResponse(500, "{}"), nil },
		"read times out": func() (*http.Response, error) { return nil, context.DeadlineExceeded },
		"not found":      func() (*http.Response, error) { return jsonResponse(404, "{}"), nil },
		"other article": func() (*http.Response, error) {
			return jsonResponse(200, `{"id":"`+otherArticleID+`","version":1}`), nil
		},
		"no version":      func() (*http.Response, error) { return jsonResponse(200, `{"id":"`+articleID+`"}`), nil },
		"null":            func() (*http.Response, error) { return jsonResponse(200, `null`), nil },
		"version as text": func() (*http.Response, error) { return jsonResponse(200, `{"id":"`+articleID+`","version":"7"}`), nil },
	}
	for name, get := range tests {
		t.Run(name, func(t *testing.T) {
			requests := scripted(t, get, okWrite)
			c, _ := client(t)
			_, err := c.UpdateArticle(context.Background(), articleChanges{ID: articleID, Title: str("Neu")})
			if err == nil || strings.Contains(err.Error(), articleMayChanged) {
				t.Fatalf("err = %v, want a failure without an uncertainty hint", err)
			}
			if len(*requests) != 1 || (*requests)[0].method != http.MethodGet {
				t.Errorf("requests = %+v, want the read only", *requests)
			}
		})
	}
}

func TestArticleWriteToolsDeclareTheirRiskAndJoinOnlyTheWriteProfile(t *testing.T) {
	want := map[string]struct{ effect, idempotency string }{
		"lexware.articles.create": {"create", "non_idempotent"},
		"lexware.articles.update": {"update", "idempotent"},
	}
	reg := registry(t)
	for _, descriptor := range reg.Provider(Provider) {
		expected, ok := want[descriptor.ID]
		if !ok {
			continue
		}
		delete(want, descriptor.ID)
		risk := descriptor.Risk
		if string(risk.Effect) != expected.effect || string(risk.Idempotency) != expected.idempotency ||
			risk.Confirmation != "required" || !risk.OpenWorld || risk.DataSensitivity != articleSensitivity ||
			descriptor.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", descriptor.ID, risk)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing tools: %v", want)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		has := map[string]bool{}
		for _, id := range profile.Tools {
			has[id] = true
		}
		if has["lexware.articles.create"] != (profile.ID == "write") || has["lexware.articles.update"] != (profile.ID == "write") {
			t.Errorf("profile %s tools = %v", profile.ID, profile.Tools)
		}
	}
}
