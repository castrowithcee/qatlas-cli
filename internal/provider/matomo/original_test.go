package matomo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// sendOnce sends one fixed method against a transport that answers it with response.
func sendOnce(t *testing.T, response func() *http.Response) error {
	t.Helper()
	var calls []call
	handler := answers{"API.getMatomoVersion": func(call) *http.Response { return response() }}.handler(t)
	env := newEnvironment(t, &calls, handler)
	client, err := Open(context.Background(), mustResolve(t, env, "open"), resolver(env.red, nil), env.red)
	if err != nil {
		t.Fatal(err)
	}
	var out json.RawMessage
	return client.send(context.Background(), "test", "API.getMatomoVersion", nil, false, &out)
}

// wantOriginal checks the class, that Error() holds no provider text, and the Original of err.
func wantOriginal(t *testing.T, name string, err error, class provider.Class, status int,
	entries []provider.OriginalEntry) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error", name)
	}
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Class != class {
		t.Fatalf("%s: err = %v, want class %s", name, err, class)
	}
	if strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("%s: Error() holds the provider text: %v", name, err)
	}
	if failure.Original == nil || failure.Original.Status != status || len(failure.Original.Entries) != len(entries) {
		t.Fatalf("%s: Original = %+v, want status %d and %d entries", name, failure.Original, status, len(entries))
	}
	for i, entry := range entries {
		if failure.Original.Entries[i] != entry {
			t.Errorf("%s: entry %d = %+v, want %+v", name, i, failure.Original.Entries[i], entry)
		}
	}
}

func TestApplicationErrorsCarryTheMessageAsOriginal(t *testing.T) {
	for name, tc := range map[string]struct {
		message string
		class   provider.Class
	}{
		"auth":         {"The token_auth is not valid " + bodyCanary, provider.ClassAuth},
		"permission":   {permissionMsg + " " + bodyCanary, provider.ClassPermission},
		"not found":    {"An unexpected website was found " + bodyCanary, provider.ClassNotFound},
		"unrecognised": {"Something odd happened " + bodyCanary, provider.ClassInvalidResponse},
		"password":     {"The current password you entered is not correct " + bodyCanary, provider.ClassAuth},
		"config":       {"The setting is controlled by the config file " + bodyCanary, provider.ClassPermission},
		"ldap":         {"Could not connect to the LDAP server " + bodyCanary, provider.ClassProviderError},
	} {
		err := sendOnce(t, func() *http.Response { return failure(tc.message) })
		wantOriginal(t, name, err, tc.class, http.StatusOK, []provider.OriginalEntry{{Message: tc.message}})
	}
}

func TestApplicationErrorsWithoutOriginal(t *testing.T) {
	for name, message := range map[string]string{
		"invalid": "The specified segment is invalid " + bodyCanary,
		"missing": "The method 'X.y' does not exist or is not available in the module 'X'. " + bodyCanary,
	} {
		err := sendOnce(t, func() *http.Response { return failure(message) })
		var failure *provider.Error
		if err == nil || errors.As(err, &failure) && failure.Original != nil {
			t.Errorf("%s: err = %v, want an error without Original", name, err)
		}
		if err != nil && strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: Error() holds the provider text: %v", name, err)
		}
	}
}

func TestHTTPFailuresCarryTheEnvelopeAsOriginal(t *testing.T) {
	envelope := `{"result":"error","message":"` + bodyCanary + `"}`
	for status, class := range map[int]provider.Class{
		http.StatusUnauthorized: provider.ClassAuth, http.StatusForbidden: provider.ClassPermission,
		http.StatusNotFound: provider.ClassNotFound, http.StatusBadRequest: provider.ClassProviderError,
		http.StatusInternalServerError: provider.ClassProviderError,
	} {
		err := sendOnce(t, func() *http.Response { return respond(status, envelope) })
		wantOriginal(t, http.StatusText(status), err, class, status, []provider.OriginalEntry{{Message: bodyCanary}})
	}
	// JSON that is not the envelope keeps only the status, a non-JSON answer keeps its text.
	err := sendOnce(t, func() *http.Response { return respond(500, `{"message":"`+bodyCanary+`"}`) })
	wantOriginal(t, "json without envelope", err, provider.ClassProviderError, 500, nil)
	err = sendOnce(t, func() *http.Response { return respond(502, "<html>"+bodyCanary+"</html>") })
	wantOriginal(t, "html", err, provider.ClassProviderError, 502,
		[]provider.OriginalEntry{{Message: "<html>" + bodyCanary + "</html>"}})
	err = sendOnce(t, func() *http.Response { return respond(500, "") })
	wantOriginal(t, "empty", err, provider.ClassProviderError, 500, nil)
}

func TestVersionRewriteKeepsTheOriginal(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, answers{"API.getMatomoVersion": func(call) *http.Response {
		return failure(permissionMsg + " " + bodyCanary)
	}}.handler(t))
	_, err := env.invoke("matomo.version.get", "open", `{}`)
	wantOriginal(t, "rewrite", err, provider.ClassAuth, http.StatusOK,
		[]provider.OriginalEntry{{Message: permissionMsg + " " + bodyCanary}})
}
