package provider

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NoRedirectClient returns an HTTP client that bounds every request by timeout and never follows a
// redirect, so a credential in a request can only reach the origin the caller configured. rt is the
// round tripper to use; nil is Go's default transport. Callers pass their test-replaceable transport at
// the time they build the client.
func NoRedirectClient(timeout time.Duration, rt http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     rt,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// RedirectRefused reports a redirect a provider deliberately did not follow. Message is safe to publish:
// it names neither the location nor a credential. Return it from an http.Client CheckRedirect; Transport
// maps it to a provider error instead of an unreachable server, because a refused redirect is a policy
// decision.
type RedirectRefused struct{ Message string }

func (e *RedirectRefused) Error() string { return e.Message }

// RetryAfter reads how long a provider asks a client to wait from the Retry-After header in seconds. Zero
// means the header is absent, not a positive whole number of seconds, or otherwise unusable. The wait is
// not capped; callers that bound it do so themselves.
func RetryAfter(h http.Header) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
