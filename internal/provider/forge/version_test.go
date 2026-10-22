package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestParseVersionReadsBothSchemes(t *testing.T) {
	for raw, want := range map[string]version{
		"15.0.10":                              {15, 0, 10},
		"17.0.0":                               {17, 0, 0},
		"16.0.0-dev-785-465b60ef+gitea-1.22.0": {16, 0, 0},
		"15.0.10+gitea-1.22.0":                 {15, 0, 10},
		"1.27.3":                               {1, 27, 3},
		"28.2.0":                               {28, 2, 0},
		"29.0.0+dev-160-g55186ca39b":           {29, 0, 0},
		"v1.27.0-rc1":                          {1, 27, 0},
	} {
		got, ok := parseVersion(raw)
		if !ok || got != want {
			t.Errorf("parseVersion(%q) = %v, %t, want %v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "15", "15.0", "15.0.x", "1.2.3.4", "-1.0.0", "15..0", strings.Repeat("1", 200)} {
		if _, ok := parseVersion(raw); ok {
			t.Errorf("parseVersion(%q) accepted", raw)
		}
	}
	if !(version{1, 27, 3}).less(version{28, 0, 0}) || (version{28, 2, 0}).less(gitea.minimum) ||
		!(version{1, 26, 9}).less(gitea.minimum) || !(version{14, 9, 9}).less(forgejo.minimum) {
		t.Error("versions do not compare across the Gitea scheme change")
	}
}

// The connection test proves the product and the minimum version before the token is read, and fails closed on
// the other product, on an old version, and on an instance that hides the Forgejo path.
func TestConnectionProvesProductAndVersion(t *testing.T) {
	user := func(c call) *http.Response {
		if c.path == "/api/v1/user" && c.auth == "token "+tokenValue {
			return jsonResponse(http.StatusOK, `{"id":1,"login":"alice"}`)
		}
		return notFoundResponse()
	}
	blocked := func(apiVersion string) func(call) *http.Response {
		return func(c call) *http.Response {
			if c.path == "/api/forgejo/v1/version" {
				return jsonResponse(http.StatusForbidden, `{"message":"`+bodyCanary+`"}`)
			}
			return instance("", apiVersion, user)(c)
		}
	}
	cases := []struct {
		name    string
		p       *product
		handler func(call) *http.Response
		want    provider.Class
		reads   bool
	}{
		{"forgejo 15.0.10", forgejo, forgejoAt("15.0.10", user), provider.ClassOK, true},
		{"forgejo 17.0.0", forgejo, forgejoAt("17.0.0", user), provider.ClassOK, true},
		{"forgejo development build", forgejo, forgejoAt("16.0.0-dev-785-465b60ef+gitea-1.22.0", user), provider.ClassOK, true},
		{"gitea 1.27.3", gitea, giteaAt("1.27.3", user), provider.ClassOK, true},
		{"gitea 28.2.0", gitea, giteaAt("28.2.0", user), provider.ClassOK, true},
		{"gitea development build", gitea, giteaAt("29.0.0+dev-160-g55186ca39b", user), provider.ClassOK, true},
		{"forgejo connection at gitea", forgejo, giteaAt("1.27.3", user), provider.ClassProviderError, false},
		{"gitea connection at forgejo", gitea, forgejoAt("15.0.10+gitea-1.22.0", user), provider.ClassProviderError, false},
		{"forgejo with a blocked path", forgejo, blocked("15.0.10+gitea-1.22.0"), provider.ClassProviderError, false},
		{"gitea connection at forgejo with a blocked path", gitea, blocked("15.0.10+gitea-1.22.0"), provider.ClassProviderError, false},
		{"forgejo below the minimum", forgejo, forgejoAt("14.0.3", user), provider.ClassProviderError, false},
		{"gitea below the minimum", gitea, giteaAt("1.26.1", user), provider.ClassProviderError, false},
		{"gitea with an unreadable version", gitea, giteaAt("unknown", user), provider.ClassInvalidResponse, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			serve(t, &calls, tc.handler)
			reads := 0
			class, err := tc.p.testConnection(context.Background(), resolvedFor(tc.p), resolver(&redact.Redactor{}, &reads), &redact.Redactor{})
			if err != nil || class != tc.want {
				t.Fatalf("class = %q, %v, want %q", class, err, tc.want)
			}
			if (reads > 0) != tc.reads {
				t.Errorf("secret reads = %d, want reads %t", reads, tc.reads)
			}
			for _, c := range calls {
				if strings.HasSuffix(c.path, "/version") && c.auth != "" {
					t.Errorf("version read %s carried the token", c.path)
				}
			}
		})
	}
}

// A Gitea instance that refuses anonymous API access gets the version read once more with the token.
func TestConnectionRetriesTheGiteaVersionWithTheTokenOnlyWhenRefused(t *testing.T) {
	var calls []call
	serve(t, &calls, func(c call) *http.Response {
		switch {
		case c.path == "/api/v1/version" && c.auth == "":
			return jsonResponse(http.StatusUnauthorized, `{"message":"`+bodyCanary+`"}`)
		case c.path == "/api/v1/version":
			return jsonResponse(http.StatusOK, `{"version":"28.2.0"}`)
		case c.path == "/api/v1/user":
			return jsonResponse(http.StatusOK, `{"id":1,"login":"alice"}`)
		}
		return notFoundResponse()
	})
	class, err := gitea.testConnection(context.Background(), resolvedFor(gitea), resolver(&redact.Redactor{}, nil), &redact.Redactor{})
	if err != nil || class != provider.ClassOK {
		t.Fatalf("class = %q, %v", class, err)
	}
	want := []string{"/api/forgejo/v1/version", "/api/v1/version", "/api/v1/version", "/api/v1/user"}
	if len(calls) != len(want) || calls[1].auth != "" || calls[2].auth == "" {
		t.Fatalf("calls = %+v, want %v with the token only on the second version read", calls, want)
	}
}

// The token check reads the account without targets, the first target with them, and maps 401 to auth and 403
// and 404 alike to an unreachable target. The first target is checked by its canonical name.
func TestConnectionChecksTheTokenAgainstTheFirstTarget(t *testing.T) {
	cases := []struct {
		name    string
		targets []string
		path    string
		answer  *http.Response
		want    provider.Class
	}{
		{"account", nil, "/api/v1/user", jsonResponse(http.StatusOK, `{"login":"alice"}`), provider.ClassOK},
		{"rejected token", nil, "/api/v1/user", jsonResponse(http.StatusUnauthorized, `{}`), provider.ClassAuth},
		{"repository", []string{"repos/acme/widget", "users/bob"}, "/api/v1/repos/acme/widget",
			jsonResponse(http.StatusOK, `{"full_name":"Acme/Widget"}`), provider.ClassOK},
		{"renamed repository", []string{"repos/acme/widget"}, "/api/v1/repos/acme/widget",
			jsonResponse(http.StatusOK, `{"full_name":"acme/renamed"}`), provider.ClassNotFound},
		{"hidden repository", []string{"repos/acme/widget"}, "/api/v1/repos/acme/widget",
			jsonResponse(http.StatusForbidden, `{}`), provider.ClassNotFound},
		{"missing repository", []string{"repos/acme/widget"}, "/api/v1/repos/acme/widget",
			jsonResponse(http.StatusNotFound, `{}`), provider.ClassNotFound},
		{"owner", []string{"users/Alice"}, "/api/v1/users/Alice", jsonResponse(http.StatusOK, `{"login":"alice"}`),
			provider.ClassOK},
		{"other owner", []string{"orgs/acme"}, "/api/v1/users/acme", jsonResponse(http.StatusOK, `{"login":"other"}`),
			provider.ClassNotFound},
		{"redirected owner", []string{"orgs/acme"}, "/api/v1/users/acme",
			jsonResponse(http.StatusTemporaryRedirect, `{}`, "Location", "https://elsewhere.example.test/"), provider.ClassProviderError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			serve(t, &calls, giteaAt("1.27.3", func(c call) *http.Response {
				if c.path == tc.path {
					return tc.answer
				}
				t.Errorf("unexpected request %s", c.path)
				return notFoundResponse()
			}))
			class, err := gitea.testConnection(context.Background(), resolvedFor(gitea, tc.targets...),
				resolver(&redact.Redactor{}, nil), &redact.Redactor{})
			if err != nil || class != tc.want {
				t.Fatalf("class = %q, %v, want %q", class, err, tc.want)
			}
			if last := calls[len(calls)-1]; last.path != tc.path || last.auth != "token "+tokenValue {
				t.Errorf("last call = %+v", last)
			}
		})
	}
}

func TestConnectionRefusesAnUnusableBaseURLWithoutARequest(t *testing.T) {
	var calls []call
	serve(t, &calls, func(call) *http.Response { return notFoundResponse() })
	resolved := resolvedFor(forgejo)
	resolved.BaseURL = "http://forge.example.test"
	reads := 0
	class, err := forgejo.testConnection(context.Background(), resolved, resolver(&redact.Redactor{}, &reads), nil)
	if err != nil || class != provider.ClassProviderError || len(calls) != 0 || reads != 0 {
		t.Fatalf("class = %q, %v, calls %d, reads %d", class, err, len(calls), reads)
	}
}

// gatedTool stands in for an operation of a later version: Gitea 28 and Forgejo 17.
func gatedTool(sent *int) *tool {
	return &tool{group: "probes", action: "get",
		since: map[string]version{forgejo.id: {17, 0, 0}, gitea.id: {28, 0, 0}},
		describe: func(*product) capability.Descriptor {
			return capability.Descriptor{}
		},
		prepare: func(_ *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments struct {
				Repository string `json:"repository"`
			}
			_ = json.Unmarshal(raw, &arguments)
			chosen, err := allowed.chooseRepository(arguments.Repository)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				*sent++
				var answer object
				_, err := c.get(ctx, "probe", "/repos/"+escape(chosen.owner)+"/"+escape(chosen.repo), nil, &answer)
				return map[string]any{}, err
			}, nil
		},
	}
}

// An operation the instance's version lacks is refused before its request, naming only the version it
// needs; the version is read once per process and instance, and a target refusal comes before that read.
func TestVersionGateRefusesBeforeTheRequest(t *testing.T) {
	cases := []struct {
		p       *product
		handler func(call) *http.Response
		allowed bool
		needs   string
		hidden  string
	}{
		{gitea, giteaAt("1.27.3", nil), false, "Gitea 28.0.0", "1.27.3"},
		{gitea, giteaAt("28.2.0", nil), true, "", ""},
		{gitea, giteaAt("29.0.0+dev-160-g55186ca39b", nil), true, "", ""},
		{forgejo, forgejoAt("15.0.10", nil), false, "Forgejo 17.0.0", "15.0.10"},
		{forgejo, forgejoAt("16.0.0-dev-785-465b60ef+gitea-1.22.0", nil), false, "Forgejo 17.0.0", "16.0.0"},
		{forgejo, forgejoAt("17.0.0", nil), true, "", ""},
	}
	for _, tc := range cases {
		var calls []call
		serve(t, &calls, func(c call) *http.Response {
			if strings.HasPrefix(c.path, "/api/v1/repos/") {
				return jsonResponse(http.StatusOK, `{}`)
			}
			return tc.handler(c)
		})
		sent, reads := 0, 0
		handler := gatedTool(&sent).handler(tc.p)
		for i := 0; i < 2; i++ {
			_, err := handler(context.Background(), resolvedFor(tc.p, "repos/acme/widget"),
				resolver(&redact.Redactor{}, &reads), &redact.Redactor{}, json.RawMessage(`{}`))
			if tc.allowed != (err == nil) {
				t.Fatalf("%s %s: err = %v, want allowed %t", tc.p.id, tc.needs, err, tc.allowed)
			}
			if err != nil && (!strings.Contains(err.Error(), tc.needs) || strings.Contains(err.Error(), tc.hidden)) {
				t.Errorf("%s: message %q, want %q without the instance version", tc.p.id, err, tc.needs)
			}
		}
		versionReads := 0
		for _, c := range calls {
			if strings.HasSuffix(c.path, "/version") {
				versionReads++
			}
		}
		wantReads := 1
		if tc.p == gitea {
			wantReads = 2
		}
		if versionReads != wantReads {
			t.Errorf("%s: %d version reads for two calls, want %d", tc.p.id, versionReads, wantReads)
		}
		if tc.allowed != (sent == 2) || (!tc.allowed && reads != 0) {
			t.Errorf("%s: operation sent %d times, %d secret reads", tc.p.id, sent, reads)
		}
		forgetVersions(t)
	}

	var calls []call
	serve(t, &calls, giteaAt("28.2.0", nil))
	sent := 0
	_, err := gatedTool(&sent).handler(gitea)(context.Background(), resolvedFor(gitea, "repos/acme/widget"),
		resolver(&redact.Redactor{}, nil), &redact.Redactor{}, json.RawMessage(`{"repository":"other/thing"}`))
	if !isInvalidRequest(err) || len(calls) != 0 || strings.Contains(err.Error(), "other") {
		t.Fatalf("outside target: %v after %d calls", err, len(calls))
	}
}

// A cached version expires, so a long-running process sees an upgrade.
func TestVersionCacheExpires(t *testing.T) {
	forgetVersions(t)
	current := now
	t.Cleanup(func() { now = current })
	remember(gitea, baseURL, version{1, 27, 3})
	if v, ok := cachedVersionOf(gitea, baseURL); !ok || v != (version{1, 27, 3}) {
		t.Fatalf("cached = %v, %t", v, ok)
	}
	if _, ok := cachedVersionOf(forgejo, baseURL); ok {
		t.Error("a version is cached per product")
	}
	later := current().Add(versionTTL + 1)
	now = func() time.Time { return later }
	if _, ok := cachedVersionOf(gitea, baseURL); ok {
		t.Error("an expired version is still served")
	}
}
