package provider_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNoRedirectClientBoundsTimeAndUsesTransport(t *testing.T) {
	used := false
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		used = true
		return nil, errors.New("stop")
	})
	client := provider.NoRedirectClient(7*time.Second, rt)
	if client.Timeout != 7*time.Second {
		t.Fatalf("timeout = %v", client.Timeout)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	if _, err := client.Do(req); err == nil || !used {
		t.Fatalf("transport not used: used=%v err=%v", used, err)
	}
	if provider.NoRedirectClient(time.Second, nil).Transport != nil {
		t.Fatal("a nil round tripper must stay nil")
	}
}

func TestNoRedirectClientReturnsTheRedirectResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the redirect target must never be requested")
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()

	response, err := provider.NoRedirectClient(time.Second, nil).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want the redirect itself", response.StatusCode)
	}
}

func TestTransportPublishesARefusedRedirectAsProviderError(t *testing.T) {
	refused := &provider.RedirectRefused{Message: "refused to follow a redirect"}
	wrapped := dial("dial", refused)
	failure := provider.Transport("read", "the server", wrapped)
	if failure.Class != provider.ClassProviderError || failure.Op != "read" || failure.Cause != "" ||
		failure.Message != "refused to follow a redirect" {
		t.Fatalf("failure = %#v", failure)
	}
	if refused.Error() != refused.Message {
		t.Fatalf("Error() = %q", refused.Error())
	}
}

func TestRetryAfterReadsOnlyPositiveSeconds(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"seconds", "7", 7 * time.Second},
		{"padded", " 3 ", 3 * time.Second},
		{"large stays uncapped", "86400", 24 * time.Hour},
		{"zero", "0", 0},
		{"negative", "-5", 0},
		{"date", "Wed, 21 Oct 2026 07:28:00 GMT", 0},
		{"empty", "", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{}
			if test.value != "" {
				header.Set("Retry-After", test.value)
			}
			if got := provider.RetryAfter(header); got != test.want {
				t.Fatalf("RetryAfter = %v, want %v", got, test.want)
			}
		})
	}
	if provider.RetryAfter(nil) != 0 {
		t.Fatal("a nil header names no time")
	}
}
