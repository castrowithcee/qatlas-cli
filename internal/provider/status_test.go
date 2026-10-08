package provider_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("broken") }

func TestClassifyStatus(t *testing.T) {
	texts := provider.StatusTexts{Subject: "Acme", Auth: "auth text", Permission: "permission text", NotFound: "missing text"}
	tests := []struct {
		status  int
		class   provider.Class
		message string
	}{
		{401, provider.ClassAuth, "auth text"},
		{403, provider.ClassPermission, "permission text"},
		{404, provider.ClassNotFound, "missing text"},
		{429, provider.ClassRateLimited, "Acme rate-limited the operation"},
		{503, provider.ClassUnreachable, "Acme is unavailable or in maintenance"},
		{504, provider.ClassTimeout, "Acme did not answer in time"},
		{301, provider.ClassProviderError, "Acme answered with a redirect, which Qatlas does not follow for this request"},
		{399, provider.ClassProviderError, "Acme answered with a redirect, which Qatlas does not follow for this request"},
		{400, provider.ClassProviderError, "Acme rejected the operation (HTTP 400)"},
		{500, provider.ClassProviderError, "Acme rejected the operation (HTTP 500)"},
		{502, provider.ClassProviderError, "Acme rejected the operation (HTTP 502)"},
	}
	for _, test := range tests {
		got := provider.ClassifyStatus("acme.read", test.status, texts)
		if got.Class != test.class || got.Message != test.message || got.Op != "acme.read" || got.Cause != "" {
			t.Errorf("status %d = %+v, want class %s message %q", test.status, got, test.class, test.message)
		}
	}
}

func TestReadJSON(t *testing.T) {
	type answer struct {
		Name string `json:"name"`
	}
	t.Run("success", func(t *testing.T) {
		var out answer
		if err := provider.ReadJSON("op", "Acme", strings.NewReader(`{"name":"x"}`), 12, &out); err != nil || out.Name != "x" {
			t.Fatalf("err = %v, out = %+v", err, out)
		}
	})
	tests := []struct {
		name    string
		body    func() *strings.Reader
		limit   int64
		message string
	}{
		{"over limit", func() *strings.Reader { return strings.NewReader(`{"name":"x"}`) }, 11,
			"the Acme response could not be read within the size limit"},
		{"invalid JSON", func() *strings.Reader { return strings.NewReader(`not json`) }, 100,
			"Acme returned an invalid response"},
	}
	for _, test := range tests {
		var out answer
		err := provider.ReadJSON("op", "Acme", test.body(), test.limit, &out)
		if err == nil || err.Class != provider.ClassInvalidResponse || err.Op != "op" || err.Message != test.message {
			t.Errorf("%s: err = %+v, want invalid-response %q", test.name, err, test.message)
		}
	}
	var out answer
	err := provider.ReadJSON("op", "Acme", failingReader{}, 100, &out)
	if err == nil || err.Class != provider.ClassInvalidResponse ||
		err.Message != "the Acme response could not be read within the size limit" {
		t.Errorf("read failure: err = %+v", err)
	}
}
