package infomaniakdav

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
)

const (
	davErrorBody = `<?xml version="1.0"?><d:error xmlns:d="DAV:"><d:no-conflicting-lock/>` +
		`<d:responsedescription>` + bodyCanary + `</d:responsedescription></d:error>`
	sabreErrorBody = `<?xml version="1.0"?><d:error xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns">` +
		`<s:sabredav-version>4.6</s:sabredav-version><s:exception>Sabre\DAV\Exception\Forbidden</s:exception>` +
		`<s:message>` + bodyCanary + `</s:message></d:error>`
)

func originalOf(t *testing.T, err error) *provider.Original {
	t.Helper()
	var providerErr *provider.Error
	if err == nil {
		t.Fatal("no error")
	}
	if !errors.As(err, &providerErr) {
		t.Fatalf("not a provider error: %v", err)
	}
	if strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("message has provider text: %v", err)
	}
	if providerErr.Original == nil {
		t.Fatalf("no original: %v", err)
	}
	return providerErr.Original
}

func respond(status int, body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) { return xmlResponse(status, body), nil }
}

func checkOriginal(t *testing.T, err error, status int, body string) {
	t.Helper()
	got := originalOf(t, err)
	if got.Status != status {
		t.Errorf("status = %d, want %d", got.Status, status)
	}
	switch body {
	case davErrorBody:
		if len(got.Entries) != 2 || got.Entries[0].Type != "no-conflicting-lock" || got.Entries[1].Message != bodyCanary {
			t.Errorf("entries = %+v", got.Entries)
		}
	case sabreErrorBody:
		if len(got.Entries) != 1 || got.Entries[0].Type != `Sabre\DAV\Exception\Forbidden` ||
			got.Entries[0].Message != bodyCanary {
			t.Errorf("entries = %+v", got.Entries)
		}
	default:
		if len(got.Entries) != 1 || got.Entries[0].Message != bodyCanary {
			t.Errorf("entries = %+v", got.Entries)
		}
	}
}

func TestReadErrorsCarryOriginal(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500} {
		for _, body := range []string{davErrorBody, sabreErrorBody, bodyCanary} {
			serve(t, contactsFake(t, "", respond(status, body)))
			_, err := getContactOf(`{"addressbook":"main","id":"a.vcf"}`)
			checkOriginal(t, err, status, body)
		}
	}
}

func TestReadErrorWithoutBodyKeepsStatusOnly(t *testing.T) {
	serve(t, contactsFake(t, "", respond(404, "")))
	_, err := getContactOf(`{"addressbook":"main","id":"a.vcf"}`)
	got := originalOf(t, err)
	if got.Status != 404 || len(got.Entries) != 0 {
		t.Errorf("original = %+v", got)
	}
}

func TestMultiStatusResourceFailureHasNoOriginal(t *testing.T) {
	err := failure(&dav.Resource{Status: "HTTP/1.1 404 Not Found"}, "op")
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) || providerErr.Original != nil {
		t.Errorf("err = %+v", err)
	}
}

func TestWriteErrorsCarryOriginal(t *testing.T) {
	for _, status := range []int{403, 412, 500, 503, 302} {
		for _, body := range []string{davErrorBody, sabreErrorBody} {
			serve(t, writeFake(t, respond(status, body)))
			_, err := invokeWrite(invokeEventsDelete, `{"calendar":"work","id":"x.ics","etag":"e1"}`)
			checkOriginal(t, err, status, body)
			serve(t, writeFake(t, respond(status, body)))
			_, err = invokeWrite(invokeEventsCreate, `{`+baseEvent+`}`)
			checkOriginal(t, err, status, body)
			serve(t, writeFake(t, respond(status, body)))
			_, err = invokeContactWrite(invokeContactsDelete, `{"addressbook":"main","id":"x.vcf","etag":"e1"}`)
			checkOriginal(t, err, status, body)
		}
	}
}
