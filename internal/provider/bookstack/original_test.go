package bookstack

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const originalCanary = "canary-original-text-91"

func failWith(t *testing.T, status int, body string, call func(c *Client) error) *provider.Error {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	err := call(newClient(t, server.URL, nil))
	var perr *provider.Error
	if err == nil || !errors.As(err, &perr) {
		t.Fatalf("status %d: error = %v, want a provider error", status, err)
	}
	if strings.Contains(err.Error(), originalCanary) {
		t.Errorf("status %d: message leaks provider text: %q", status, err)
	}
	if perr.Original == nil || perr.Original.Status != status {
		t.Fatalf("status %d: original = %+v", status, perr.Original)
	}
	return perr
}

func TestErrorOriginalPerStatus(t *testing.T) {
	get := func(c *Client) error { _, err := c.GetPage(context.Background(), "1"); return err }
	for _, tt := range []struct {
		status  int
		body    string
		class   provider.Class
		entries []provider.OriginalEntry
	}{
		{401, `{"error":{"code":401,"message":"` + originalCanary + `"}}`, provider.ClassAuth,
			[]provider.OriginalEntry{{Code: "401", Message: originalCanary}}},
		{403, `{"error":{"code":403,"message":"` + originalCanary + `"}}`, provider.ClassPermission,
			[]provider.OriginalEntry{{Code: "403", Message: originalCanary}}},
		{404, `{"error":{"code":404,"message":"` + originalCanary + `"}}`, provider.ClassNotFound,
			[]provider.OriginalEntry{{Code: "404", Message: originalCanary}}},
		{429, `{"error":{"code":429,"message":"` + originalCanary + `"}}`, provider.ClassRateLimited,
			[]provider.OriginalEntry{{Code: "429", Message: originalCanary}}},
		{500, `{"error":{"code":500,"message":"` + originalCanary + `"}}`, provider.ClassProviderError,
			[]provider.OriginalEntry{{Code: "500", Message: originalCanary}}},
		{502, `<html>` + originalCanary + `</html>`, provider.ClassProviderError,
			[]provider.OriginalEntry{{Message: `<html>` + originalCanary + `</html>`}}},
		{404, "", provider.ClassNotFound, nil},
		{401, `{"error":"Unauthenticated."}`, provider.ClassAuth, nil},
	} {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			perr := failWith(t, tt.status, tt.body, get)
			if perr.Class != tt.class {
				t.Errorf("class = %s, want %s", perr.Class, tt.class)
			}
			if !reflect.DeepEqual(perr.Original.Entries, tt.entries) {
				t.Errorf("entries = %+v, want %+v", perr.Original.Entries, tt.entries)
			}
		})
	}
}

func TestErrorOriginalValidationPerField(t *testing.T) {
	const body = `{"error":{"code":422,"message":"The given data was invalid.","validation":{` +
		`"name":["` + originalCanary + `","too long"],"secret_field":["x"]}}}`
	perr := failWith(t, 422, body, func(c *Client) error {
		_, err := c.CreatePage(context.Background(), pageMutation{Name: "n", BookID: 1, Markdown: "m"})
		return err
	})
	if !strings.Contains(perr.Error(), "invalid: name") || strings.Contains(perr.Error(), "secret_field") {
		t.Errorf("message = %q", perr.Error())
	}
	want := []provider.OriginalEntry{
		{Code: "422", Message: "The given data was invalid."},
		{Path: "name", Message: originalCanary},
		{Path: "name", Message: "too long"},
		{Path: "secret_field", Message: "x"},
	}
	if !reflect.DeepEqual(perr.Original.Entries, want) {
		t.Errorf("entries = %+v, want %+v", perr.Original.Entries, want)
	}
}

func TestErrorOriginalValidationNamesSurviveRealisticBody(t *testing.T) {
	validation := `"name":["` + strings.Repeat("n", 300) + `"],"html":["` + strings.Repeat("h", 300) + `"]`
	body := `{"error":{"code":422,"message":"The given data was invalid.","validation":{` + validation + `}}}`
	if len(body) >= provider.ErrorBodyLimit {
		t.Fatalf("fixture too large: %d", len(body))
	}
	perr := failWith(t, 422, body, func(c *Client) error {
		_, err := c.CreatePage(context.Background(), pageMutation{Name: "n", BookID: 1, Markdown: "m"})
		return err
	})
	if !strings.Contains(perr.Error(), "invalid: name") {
		t.Errorf("message = %q", perr.Error())
	}
}

func TestErrorOriginalOfExport(t *testing.T) {
	perr := failWith(t, 403, `{"error":{"code":403,"message":"`+originalCanary+`"}}`, func(c *Client) error {
		resp, err := c.openExport(context.Background(), "export content", "/api/pages/1/export/html", c.http)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	})
	want := []provider.OriginalEntry{{Code: "403", Message: originalCanary}}
	if perr.Class != provider.ClassPermission || !reflect.DeepEqual(perr.Original.Entries, want) {
		t.Errorf("class = %s, entries = %+v", perr.Class, perr.Original.Entries)
	}
}

func TestErrorOriginalLargeValidationKeepsAllFieldNames(t *testing.T) {
	var fields []string
	var names []string
	for i := 0; i < 200; i++ {
		name := "field_" + strconv.Itoa(i)
		fields = append(fields, `"`+name+`":["`+strings.Repeat("x", 30)+`"]`)
		names = append(names, name)
	}
	body := `{"error":{"code":422,"message":"The given data was invalid.","validation":{` +
		strings.Join(fields, ",") + `}}}`
	if len(body) <= provider.ErrorBodyLimit {
		t.Fatalf("fixture too small: %d", len(body))
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	err := c.get(context.Background(), "test", "/api/pages", nil, nil, names, provider.ClassPermission)
	var perr *provider.Error
	if !errors.As(err, &perr) {
		t.Fatalf("error = %v", err)
	}
	if want := "invalid: " + strings.Join(names, ", "); !strings.Contains(perr.Message, want) {
		t.Errorf("message = %q, want all field names", perr.Message)
	}
	if got := len(perr.Original.Entries); got != 201 {
		t.Fatalf("entries = %d, want 201", got)
	}
	if first := perr.Original.Entries[1]; first.Path == "" || first.Message != strings.Repeat("x", 30) {
		t.Errorf("entry = %+v", first)
	}
}
