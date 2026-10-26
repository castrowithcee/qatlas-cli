package lexware

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	regularBody = `{"timestamp":"2023-05-11T17:12:31.233+02:00","status":406,"error":"Not Acceptable",` +
		`"path":"/v1/invoices","traceId":"90d78d0777be","message":"Validation failed for request.",` +
		`"details":[{"violation":"NOTNULL","field":"lineItems[0].unitPrice","message":"darf nicht leer sein"}]}`
	legacyBody = `{"IssueList":[{"i18nKey":"missing_entity","source":"company.name",` +
		`"type":"validation_failure","additionalData":null,"args":null}]}`
)

func originalOf(t *testing.T, err error) *provider.Original {
	t.Helper()
	var failure *provider.Error
	if err == nil {
		t.Fatal("err = nil, want a provider error")
	}
	failure, ok := err.(*provider.Error)
	if !ok || failure.Original == nil {
		t.Fatalf("err = %#v, want a provider error with Original", err)
	}
	return failure.Original
}

func TestStatusErrorKeepsTheOriginalOfBothFormats(t *testing.T) {
	regular := []provider.OriginalEntry{
		{Message: "Validation failed for request."},
		{Code: "NOTNULL", Path: "lineItems[0].unitPrice", Message: "darf nicht leer sein"},
	}
	legacy := []provider.OriginalEntry{{Code: "missing_entity", Type: "validation_failure", Path: "company.name"}}
	cases := []struct {
		status  int
		body    string
		entries []provider.OriginalEntry
	}{
		{http.StatusUnauthorized, `{"message":"Unauthorized"}`, []provider.OriginalEntry{{Message: "Unauthorized"}}},
		{http.StatusForbidden, regularBody, regular},
		{http.StatusNotFound, legacyBody, legacy},
		{http.StatusNotAcceptable, legacyBody, legacy},
		{http.StatusNotAcceptable, regularBody, regular},
		{http.StatusBadRequest, "<html>Bad gateway</html>", []provider.OriginalEntry{{Message: "<html>Bad gateway</html>"}}},
		{http.StatusInternalServerError, "", nil},
	}
	for _, tt := range cases {
		err := statusError("op", resourceInvoice, tt.status, strings.NewReader(tt.body))
		original := originalOf(t, err)
		if original.Status != tt.status || !reflect.DeepEqual(original.Entries, tt.entries) {
			t.Errorf("status %d: Original = %+v, want entries %+v", tt.status, original, tt.entries)
		}
		for _, secret := range []string{"traceId", "90d78d0777be", "/v1/invoices", "darf nicht", "missing_entity"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("status %d: Error() = %q contains provider text", tt.status, err.Error())
			}
		}
	}
}

func TestDownloadStatusErrorOwnMessagesKeepTheOriginal(t *testing.T) {
	cases := []struct {
		name                string
		status              int
		explicit, draftable bool
		message             string
	}{
		{"draft 409", http.StatusConflict, false, true, notFinalizedMessage},
		{"draft 406", http.StatusNotAcceptable, false, true, notFinalizedMessage},
		{"no format 406", http.StatusNotAcceptable, false, false, noFormatMessage},
		{"no format 404", http.StatusNotFound, true, false, noFormatMessage},
	}
	for _, tt := range cases {
		err := downloadStatusError("op", resourceInvoice, tt.status, strings.NewReader(legacyBody), tt.explicit, tt.draftable)
		original := originalOf(t, err)
		if err.(*provider.Error).Message != tt.message || strings.Contains(err.Error(), "missing_entity") {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		want := []provider.OriginalEntry{{Code: "missing_entity", Type: "validation_failure", Path: "company.name"}}
		if original.Status != tt.status || !reflect.DeepEqual(original.Entries, want) {
			t.Errorf("%s: Original = %+v", tt.name, original)
		}
	}
	err := downloadStatusError("op", resourceInvoice, http.StatusForbidden, strings.NewReader(regularBody), false, false)
	if original := originalOf(t, err); original.Status != http.StatusForbidden || len(original.Entries) != 2 {
		t.Errorf("fallthrough Original = %+v", original)
	}
}

func TestFollowUpRewriteKeepsTheOriginal(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotAcceptable, legacyBody), nil
	})
	c, _ := client(t)
	input := minimalCreateInput()
	input.PrecedingVoucherID = precedeID
	_, err := c.CreateCreditNote(context.Background(), input)
	original := originalOf(t, err)
	if !strings.Contains(err.Error(), "followed up by a credit note,") || strings.Contains(err.Error(), "missing_entity") {
		t.Errorf("err = %v", err)
	}
	if original.Status != http.StatusNotAcceptable || len(original.Entries) != 1 ||
		original.Entries[0].Path != "company.name" {
		t.Errorf("Original = %+v", original)
	}
}
