package brave

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestRefusalsCarryBravesOriginalApart(t *testing.T) {
	for _, tc := range []struct {
		status  int
		class   provider.Class
		code    string
		detail  string
		meta    string
		entries []provider.OriginalEntry
	}{
		{http.StatusUnauthorized, provider.ClassAuth, "SUBSCRIPTION_TOKEN_INVALID", "The token is invalid",
			`{"component":"authentication"}`, nil},
		{http.StatusPaymentRequired, provider.ClassPermission, "USAGE_LIMIT_EXCEEDED", "Plan limit reached",
			`{"plan":"Free","errors":[{"msg":"nested"}]}`, nil},
		{http.StatusUnprocessableEntity, provider.ClassInvalidResponse, "VALIDATION", "Unable to validate request",
			`{"errors":[{"loc":["query","q"]}]}`, nil},
		{http.StatusTooManyRequests, provider.ClassRateLimited, "RATE_LIMITED", "Request rate limit exceeded",
			`{"component":"rate_limiter","retries":3}`, nil},
		{http.StatusForbidden, provider.ClassPermission, "FORBIDDEN", "Not allowed", `{}`, nil},
		{http.StatusNotFound, provider.ClassNotFound, "NOT_FOUND", "No such endpoint", `{}`, nil},
		{http.StatusBadRequest, provider.ClassInvalidResponse, "BAD", "Bad", `{}`, nil},
	} {
		body := `{"type":"ErrorResponse","error":{"id":"req-id-canary","status":"` + http.StatusText(tc.status) +
			`","detail":"` + tc.detail + `","meta":` + tc.meta + `,"code":"` + tc.code + `"},"time":1700000000}`
		env := newEnvironment(t, &fakeBrave{answer: reply(tc.status, nil, body)})
		_, err := env.invoke("brave.web.search", "search", `{"q":"x"}`)
		if err == nil {
			t.Fatalf("HTTP %d: no error", tc.status)
		}
		var failed *provider.Error
		if !errors.As(err, &failed) || failed.Class != tc.class || failed.Original == nil {
			t.Fatalf("HTTP %d: err = %v, want class %q with Original", tc.status, err, tc.class)
		}
		if strings.Contains(failed.Message, tc.detail) || strings.Contains(failed.Message, tc.code) {
			t.Errorf("HTTP %d: message %q carries the provider text", tc.status, failed.Message)
		}
		original := failed.Original
		if original.Status != tc.status || len(original.Entries) == 0 {
			t.Fatalf("HTTP %d: original = %+v", tc.status, original)
		}
		first := original.Entries[0]
		if first.Code != tc.code || first.Message != tc.detail {
			t.Errorf("HTTP %d: first entry = %+v", tc.status, first)
		}
		for _, entry := range original.Entries {
			if strings.Contains(entry.Message, "nested") || strings.Contains(entry.Message, "req-id-canary") {
				t.Errorf("HTTP %d: entry %+v carries nested or id data", tc.status, entry)
			}
		}
	}
}

func TestRefusalMetaScalarsAreEntries(t *testing.T) {
	body := `{"type":"ErrorResponse","error":{"id":"i","status":401,"detail":"d","meta":{"component":"authentication"},` +
		`"code":"C"},"time":1}`
	env := newEnvironment(t, &fakeBrave{answer: reply(http.StatusUnauthorized, nil, body)})
	_, err := env.invoke("brave.web.search", "search", `{"q":"x"}`)
	var failed *provider.Error
	if !errors.As(err, &failed) || failed.Original == nil || len(failed.Original.Entries) != 2 ||
		failed.Original.Entries[1].Path != "component" || failed.Original.Entries[1].Message != "authentication" {
		t.Fatalf("err = %v, want a meta entry for component", err)
	}
}

func TestRefusalsWithoutJSONKeepTextOrStatus(t *testing.T) {
	for name, tc := range map[string]struct{ body, text string }{
		"not json": {"<html>gateway page</html>", "<html>gateway page</html>"},
		"empty":    {"", ""},
	} {
		env := newEnvironment(t, &fakeBrave{answer: reply(http.StatusUnauthorized, nil, tc.body)})
		_, err := env.invoke("brave.web.search", "search", `{"q":"x"}`)
		var failed *provider.Error
		if !errors.As(err, &failed) || failed.Class != provider.ClassAuth || failed.Original == nil ||
			failed.Original.Status != http.StatusUnauthorized {
			t.Fatalf("%s: err = %v", name, err)
		}
		switch {
		case tc.text == "" && len(failed.Original.Entries) != 0,
			tc.text != "" && (len(failed.Original.Entries) != 1 || failed.Original.Entries[0].Message != tc.text):
			t.Errorf("%s: entries = %+v", name, failed.Original.Entries)
		}
	}
}
