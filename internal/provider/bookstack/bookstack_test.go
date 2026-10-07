package bookstack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// TestMain trusts the certificate of httptest's TLS servers for every request of this package, so no test
// needs the Internet or a weaker URL rule.
func TestMain(m *testing.M) {
	trust := httptest.NewTLSServer(http.NotFoundHandler())
	transport = trust.Client().Transport
	code := m.Run()
	trust.Close()
	os.Exit(code)
}

// resolver returns a resolver that reads the process environment and an empty in-process credential store.
// No test in this package may reach the credential store of the machine it runs on.
func resolver(red *redact.Redactor) *secret.Resolver {
	return secret.NewWith(os.Getenv, secret.NewMemoryStore(), nil, red)
}

// envCredential is the credential shape this provider used before the store existed: it names variables.
func envCredential(values map[string]string) config.Credential {
	return config.Credential{Type: config.CredentialTypeEnv, Values: values}
}

// Canary values stand in for real tokens. No test needs a real secret.
const (
	canaryID     = "canary-token-id-4f21"
	canarySecret = "canary-token-secret-9ab3"
)

// recorder captures what a test server received, so a test can prove what was and was not sent.
type recorder struct {
	mu      sync.Mutex
	methods []string
	paths   []string
	queries []string
	auth    []string
}

func (r *recorder) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods = append(r.methods, req.Method)
	r.paths = append(r.paths, req.URL.Path)
	r.queries = append(r.queries, req.URL.RawQuery)
	r.auth = append(r.auth, req.Header.Get("Authorization"))
}

func (r *recorder) snapshot() ([]string, []string, []string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.methods...), append([]string(nil), r.paths...),
		append([]string(nil), r.queries...), append([]string(nil), r.auth...)
}

func page(id int64, name string) map[string]any {
	return map[string]any{
		"id": id, "book_id": 7, "chapter_id": 0, "name": name,
		"slug": strings.ToLower(name), "created_at": "2026-01-01T00:00:00.000000Z",
		"updated_at": "2026-01-02T00:00:00.000000Z",
	}
}

// newClient wires a client to a test server using the connection model the configuration produces.
func newClient(t *testing.T, baseURL string, red *redact.Redactor) *Client {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)

	client, err := Open(context.Background(), &config.Resolved{
		Name:     "wiki",
		Provider: Provider,
		BaseURL:  baseURL,
		Secrets:  envCredential(map[string]string{roleTokenID: "TEST_TOKEN_ID", roleTokenSecret: "TEST_TOKEN_SECRET"}),
	}, resolver(red), red)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	return client
}

func TestListPages(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{page(1, "Alpha"), page(2, "Beta")},
			"total": 2,
		})
	}))
	defer server.Close()

	got, err := newClient(t, server.URL, nil).ListPages(context.Background(), listTarget{}, 10, 0)

	if err != nil {
		t.Fatalf("ListPages() = %v", err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(got.Rows))
	}
	if got.Rows[0]["name"] != "Alpha" || got.Rows[1]["id"] != int64(2) {
		t.Errorf("rows = %v", got.Rows)
	}
	if want := []string{"id", "name", "slug", "book_id", "chapter_id", "created_at", "updated_at", "priority", "draft", "template", "owned_by"}; !equal(got.Columns, want) {
		t.Errorf("columns = %v, want %v", got.Columns, want)
	}

	methods, paths, queries, auth := rec.snapshot()
	if methods[0] != http.MethodGet {
		t.Errorf("method = %s, want GET", methods[0])
	}
	if paths[0] != "/api/pages" {
		t.Errorf("path = %s", paths[0])
	}
	if !strings.Contains(queries[0], "count=10") || !strings.Contains(queries[0], "offset=0") {
		t.Errorf("query = %s, want the limit and offset passed through", queries[0])
	}
	if want := "Token " + canaryID + ":" + canarySecret; auth[0] != want {
		t.Errorf("authorization header = %q, want the Token form", auth[0])
	}
}

// Limit and offset are pushed down to the provider, and more pages are fetched only while they are needed.
func TestListPagesPagination(t *testing.T) {
	const total = 7
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

		data := []map[string]any{}
		// The server never returns more than three records at once, whatever the client asked for.
		for i := offset; i < offset+count && i < total && len(data) < 3; i++ {
			data = append(data, page(int64(i+1), fmt.Sprintf("Page%d", i+1)))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": total})
	}))
	defer server.Close()

	client := newClient(t, server.URL, nil)

	t.Run("a limit is honoured across requests", func(t *testing.T) {
		got, err := client.ListPages(context.Background(), listTarget{}, 5, 0)
		if err != nil {
			t.Fatalf("ListPages() = %v", err)
		}
		if len(got.Rows) != 5 {
			t.Errorf("rows = %d, want 5", len(got.Rows))
		}
		if got.Rows[0]["id"] != int64(1) || got.Rows[4]["id"] != int64(5) {
			t.Errorf("rows = %v", got.Rows)
		}
	})

	t.Run("no limit fetches everything", func(t *testing.T) {
		got, err := client.ListPages(context.Background(), listTarget{}, 0, 0)
		if err != nil {
			t.Fatalf("ListPages() = %v", err)
		}
		if len(got.Rows) != total {
			t.Errorf("rows = %d, want %d", len(got.Rows), total)
		}
	})

	t.Run("offset skips records", func(t *testing.T) {
		got, err := client.ListPages(context.Background(), listTarget{}, 2, 4)
		if err != nil {
			t.Fatalf("ListPages() = %v", err)
		}
		if len(got.Rows) != 2 || got.Rows[0]["id"] != int64(5) {
			t.Errorf("rows = %v, want two records starting at id 5", got.Rows)
		}
	})
}

// An instance that ignores the offset must not produce a list that looks complete but repeats records.
func TestListPagesStopsWithoutProgress(t *testing.T) {
	var requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		// The server claims nine pages but always answers with the same three.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 9,
			"data":  []map[string]any{page(1, "A"), page(2, "B"), page(3, "C")},
		})
	}))
	defer server.Close()

	got, err := newClient(t, server.URL, nil).ListPages(context.Background(), listTarget{}, 0, 0)

	if err != nil {
		t.Fatalf("ListPages() = %v", err)
	}
	if len(got.Rows) != 3 {
		t.Errorf("rows = %d, want the three distinct records", len(got.Rows))
	}
	ids := map[any]int{}
	for _, r := range got.Rows {
		ids[r["id"]]++
	}
	for id, n := range ids {
		if n != 1 {
			t.Errorf("id %v appears %d times", id, n)
		}
	}
	if requests > 2 {
		t.Errorf("sent %d requests, want the loop to stop as soon as nothing new arrives", requests)
	}
}

func TestGetPage(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		p := page(42, "Runbook")
		// Content that would break a naive encoder, plus HTML and Markdown.
		p["html"] = "<p>a|b</p>\n<p>c=d</p>"
		p["markdown"] = "# Title\n\n- a\\b\n- c|d"
		_ = json.NewEncoder(w).Encode(p)
	}))
	defer server.Close()

	got, err := newClient(t, server.URL, nil).GetPage(context.Background(), "42")

	if err != nil {
		t.Fatalf("GetPage() = %v", err)
	}
	_, paths, _, _ := rec.snapshot()
	if paths[0] != "/api/pages/42" {
		t.Errorf("path = %s, want /api/pages/42", paths[0])
	}

	byName := map[string]any{}
	for _, f := range got.Fields {
		byName[f.Name] = f.Value
	}
	if byName["id"] != int64(42) || byName["name"] != "Runbook" {
		t.Errorf("fields = %v", byName)
	}
	// Content is passed through unchanged: nothing is rendered, escaped, or interpreted here.
	if byName["html"] != "<p>a|b</p>\n<p>c=d</p>" {
		t.Errorf("html = %q, want the response verbatim", byName["html"])
	}
	if byName["markdown"] != "# Title\n\n- a\\b\n- c|d" {
		t.Errorf("markdown = %q, want the response verbatim", byName["markdown"])
	}
}

// Every read path must use GET. A mutating request would be a contract violation.
func TestOnlyReadRequests(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{page(1, "A")}, "total": 1})
	}))
	defer server.Close()

	client := newClient(t, server.URL, nil)
	ctx := context.Background()
	if _, err := client.ListPages(ctx, listTarget{}, 1, 0); err != nil {
		t.Fatalf("ListPages() = %v", err)
	}
	if _, err := client.GetPage(ctx, "1"); err != nil {
		t.Fatalf("GetPage() = %v", err)
	}
	if got := client.TestConnection(ctx); got != provider.ClassOK {
		t.Fatalf("TestConnection() = %q, want ok", got)
	}

	methods, _, _, _ := rec.snapshot()
	if len(methods) == 0 {
		t.Fatal("no request was recorded")
	}
	for i, m := range methods {
		if m != http.MethodGet {
			t.Errorf("request %d used %s, want GET", i, m)
		}
	}
}

func TestTestConnectionClasses(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    provider.Class
	}{
		{
			name: "ok",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
			},
			want: provider.ClassOK,
		},
		{
			name: "unauthorized is auth",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":401,"message":"No authorization token found on the request"}}`))
			},
			want: provider.ClassAuth,
		},
		{
			name: "forbidden is auth for the connection test",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"message":"denied"}}`))
			},
			want: provider.ClassAuth,
		},
		{
			name: "too many requests is rate limited",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Too Many Attempts."}}`))
			},
			want: provider.ClassRateLimited,
		},
		{
			name: "server error is a provider error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			want: provider.ClassProviderError,
		},
		{
			name: "unparsable body is a provider error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("not json"))
			},
			want: provider.ClassProviderError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(tt.handler)
			defer server.Close()

			if got := newClient(t, server.URL, nil).TestConnection(context.Background()); got != tt.want {
				t.Errorf("TestConnection() = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("a closed server is unreachable", func(t *testing.T) {
		server := httptest.NewTLSServer(http.NotFoundHandler())
		url := server.URL
		server.Close()

		if got := newClient(t, url, nil).TestConnection(context.Background()); got != provider.ClassUnreachable {
			t.Errorf("TestConnection() = %q, want unreachable", got)
		}
	})

	t.Run("a name that does not resolve is unreachable", func(t *testing.T) {
		got := newClient(t, "https://qatlas-test.invalid", nil).TestConnection(context.Background())

		if got != provider.ClassUnreachable {
			t.Errorf("TestConnection() = %q, want unreachable", got)
		}
	})

	t.Run("an untrusted certificate is a TLS failure", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
		}))
		defer server.Close()

		// The client uses the system roots, so the test server's own certificate is not trusted.
		previous := transport
		transport = nil
		defer func() { transport = previous }()
		if got := newClient(t, server.URL, nil).TestConnection(context.Background()); got != provider.ClassTLS {
			t.Errorf("TestConnection() = %q, want tls", got)
		}
	})

	// A request that ran into its deadline may still have arrived, so it keeps the unambiguous timeout
	// class every provider reports, not the unreachable one that claims nothing was sent.
	t.Run("an exhausted deadline is a timeout", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(200 * time.Millisecond)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		if got := newClient(t, server.URL, nil).TestConnection(ctx); got != provider.ClassTimeout {
			t.Errorf("TestConnection() = %q, want timeout", got)
		}
	})
}

func TestRedirects(t *testing.T) {
	for _, target := range []string{"/moved", "elsewhere"} {
		t.Run("a redirect is not followed to "+target, func(t *testing.T) {
			rec := &recorder{}
			elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
			}))
			defer elsewhere.Close()
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				location := target
				if target == "elsewhere" {
					location = elsewhere.URL + "/api/pages"
				}
				rec.record(r)
				http.Redirect(w, r, location, http.StatusFound)
			}))
			defer origin.Close()

			c := newClient(t, origin.URL, nil)
			_, err := c.ListPages(context.Background(), listTarget{}, 1, 0)
			var perr *provider.Error
			if !errors.As(err, &perr) || perr.Class != provider.ClassProviderError {
				t.Fatalf("error = %v, want a provider error", err)
			}
			if methods, _, _, _ := rec.snapshot(); len(methods) != 1 {
				t.Errorf("requests = %v, want exactly the first", methods)
			}
			if err := c.DeletePage(context.Background(), "1"); err == nil {
				t.Error("DeletePage() followed a redirect")
			}
		})
	}
}

func TestErrorClassesHideProviderText(t *testing.T) {
	const canary = "canary-provider-text-77"
	for _, tt := range []struct {
		status int
		body   string
		class  provider.Class
		want   string
	}{
		{401, "", provider.ClassAuth, ""},
		{403, "", provider.ClassPermission, "lacks the BookStack role permission"},
		{404, "", provider.ClassNotFound, ""},
		{429, "", provider.ClassRateLimited, ""},
		{422, `{"error":{"validation":{"name":["` + canary + `"],"other":["x"],"id":["y"]}}}`, provider.ClassProviderError, "invalid: id"},
		{418, "", provider.ClassProviderError, "(HTTP 418)"},
	} {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				body := tt.body
				if body == "" {
					body = `{"error":{"code":1,"message":"` + canary + `"}}`
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			_, err := newClient(t, server.URL, nil).GetPage(context.Background(), "1")
			var perr *provider.Error
			if !errors.As(err, &perr) || perr.Class != tt.class {
				t.Fatalf("error = %v, want class %s", err, tt.class)
			}
			if strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), "other") {
				t.Errorf("error leaks provider text: %q", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestValidationNamesOnlyArguments(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"validation":{"name":["bad"],"secret_field":["x"]}}}`))
	}))
	defer server.Close()
	_, err := newClient(t, server.URL, nil).CreatePage(context.Background(), pageMutation{Name: "n", BookID: 1, Markdown: "m"})
	if err == nil || !strings.Contains(err.Error(), "invalid: name") || strings.Contains(err.Error(), "secret_field") ||
		strings.Contains(err.Error(), "bad") || strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("error = %v", err)
	}
}

func TestMutationsReportUncertainty(t *testing.T) {
	const hint = "this change may have taken effect, read the current state in BookStack before repeating it"
	run := func(name string, handler http.HandlerFunc, ctxTimeout time.Duration, wantHint bool) {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				handler(w, r)
			}))
			defer server.Close()
			c := newClient(t, server.URL, nil)
			calls := []func(context.Context) error{
				func(ctx context.Context) error {
					_, err := c.CreatePage(ctx, pageMutation{Name: "n", BookID: 1, Markdown: "m"})
					return err
				},
				func(ctx context.Context) error { _, err := c.UpdatePage(ctx, "1", pageMutation{Name: "n"}); return err },
				func(ctx context.Context) error { return c.DeletePage(ctx, "1") },
			}
			for n, call := range calls {
				ctx := context.Background()
				if ctxTimeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, ctxTimeout)
					defer cancel()
				}
				err := call(ctx)
				// A delete reads no response body, so an unreadable body cannot fail it.
				if n == 2 && name == "unreadable response" {
					if err != nil {
						t.Errorf("delete error = %v", err)
					}
					continue
				}
				if err == nil || strings.Contains(err.Error(), hint) != wantHint {
					t.Errorf("error = %v, hint wanted %v", err, wantHint)
				}
			}
			if methods, _, _, _ := rec.snapshot(); len(methods) != 3 {
				t.Errorf("requests = %d, want 3 (one per call, no retry)", len(methods))
			}
		})
	}
	run("5xx", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, 0, true)
	// The handler answers long after the deadline, which only has to outlast the TLS handshake for the request
	// to arrive.
	run("timeout", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}, 500*time.Millisecond, true)
	run("unreadable response", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }, 0, true)
	run("422 is certain", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(422) }, 0, false)
	run("404 is certain", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }, 0, false)
}

func TestReadResponseCap(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1,"html":"`))
		chunk := []byte(strings.Repeat("a", 1<<20))
		for i := 0; i < 17; i++ {
			_, _ = w.Write(chunk)
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	defer server.Close()
	_, err := newClient(t, server.URL, nil).GetPage(context.Background(), "1")
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.ClassInvalidResponse {
		t.Fatalf("error = %v, want invalid-provider-response", err)
	}
}

func TestDeleteNeedsToolAllowList(t *testing.T) {
	if !pagesDelete.RequiresToolAllowList {
		t.Error("pages.delete must require a tool allow list")
	}
	for _, d := range []capability.Descriptor{pagesList, pagesGet, pagesCreate, pagesUpdate} {
		if d.RequiresToolAllowList {
			t.Errorf("%s must not require a tool allow list", d.ID)
		}
	}
	if !strings.Contains(pagesDelete.Description, "recycle bin") || strings.Contains(pagesDelete.Description, "Permanently") {
		t.Errorf("description = %q", pagesDelete.Description)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, ok := reg.ProviderMetadata(Provider)
	if !ok {
		t.Fatal("provider metadata missing")
	}
	for _, profile := range meta.Profiles {
		for _, id := range profile.Tools {
			if id == pagesDelete.ID {
				t.Errorf("profile %s contains pages.delete", profile.ID)
			}
		}
	}
}

// Two connections to different servers and two credentials on one server stay separate.
func TestConnectionsStaySeparate(t *testing.T) {
	seen := map[string][]string{}
	var mu sync.Mutex
	handler := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen[name] = append(seen[name], r.Header.Get("Authorization"))
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{page(1, name)}, "total": 1,
			})
		}
	}
	first := httptest.NewTLSServer(handler("first"))
	defer first.Close()
	second := httptest.NewTLSServer(handler("second"))
	defer second.Close()

	t.Setenv("READER_ID", "reader-id-0001")
	t.Setenv("READER_SECRET", "reader-secret-0001")
	t.Setenv("AUDITOR_ID", "auditor-id-0002")
	t.Setenv("AUDITOR_SECRET", "auditor-secret-0002")

	open := func(baseURL, idEnv, secretEnv string) *Client {
		t.Helper()
		c, err := Open(context.Background(), &config.Resolved{
			Name: "c", Provider: Provider, BaseURL: baseURL,
			Secrets: envCredential(map[string]string{roleTokenID: idEnv, roleTokenSecret: secretEnv}),
		}, resolver(nil), nil)
		if err != nil {
			t.Fatalf("Open() = %v", err)
		}
		return c
	}

	reader := open(first.URL, "READER_ID", "READER_SECRET")
	auditor := open(first.URL, "AUDITOR_ID", "AUDITOR_SECRET")
	other := open(second.URL, "READER_ID", "READER_SECRET")

	ctx := context.Background()
	for _, c := range []*Client{reader, auditor, other} {
		if _, err := c.ListPages(ctx, listTarget{}, 1, 0); err != nil {
			t.Fatalf("ListPages() = %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen["first"]) != 2 || len(seen["second"]) != 1 {
		t.Fatalf("requests = %v", seen)
	}
	if seen["first"][0] == seen["first"][1] {
		t.Errorf("two credentials on one server sent the same authorization")
	}
	if !strings.Contains(seen["first"][0], "reader-id-0001") || !strings.Contains(seen["first"][1], "auditor-id-0002") {
		t.Errorf("authorization headers = %v", seen["first"])
	}
	if seen["second"][0] != seen["first"][0] {
		t.Errorf("the same credential produced different headers on two servers")
	}
}

// Secrets must not reach any message, and the redactor must know them.
func TestNoSecretsInErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"denied"}}`))
	}))
	defer server.Close()

	red := &redact.Redactor{}
	client := newClient(t, server.URL, red)

	_, err := client.ListPages(context.Background(), listTarget{}, 1, 0)

	if err == nil {
		t.Fatal("ListPages() = nil, want an error")
	}
	for _, canary := range []string{canaryID, canarySecret} {
		if strings.Contains(err.Error(), canary) {
			t.Errorf("error leaks a secret: %s", err)
		}
	}
	if got := red.Apply(canaryID + ":" + canarySecret); strings.Contains(got, canaryID) {
		t.Errorf("the redactor does not know the token: %q", got)
	}
}

// A credential that yields no secret is reported by its configuration key. The message repeats neither the
// secret nor the text the user put in the field, because that text may itself be a pasted token.
func TestOpenRequiresSecrets(t *testing.T) {
	// pasted is shaped like a real BookStack token: letters and digits only, so it also satisfies every
	// rule a legal environment variable name has to satisfy.
	const pasted = "Pasted7Token9Value2Canary4Kx8Qm1"

	tests := []struct {
		name     string
		envNames map[string]string
		set      map[string]string
	}{
		{"no token id role", map[string]string{roleTokenSecret: "S"}, map[string]string{"S": "x"}},
		{"no token secret role", map[string]string{roleTokenID: "I"}, map[string]string{"I": "x"}},
		{
			"variable not set",
			map[string]string{roleTokenID: "I", roleTokenSecret: "MISSING_ON_PURPOSE"},
			map[string]string{"I": "x"},
		},
		{
			"a token pasted into the field instead of a variable name",
			map[string]string{roleTokenID: pasted, roleTokenSecret: pasted},
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.set {
				t.Setenv(k, v)
			}
			t.Setenv("MISSING_ON_PURPOSE", "")
			t.Setenv(pasted, "")

			_, err := Open(context.Background(), &config.Resolved{
				Name: "c", Provider: Provider, BaseURL: "https://x.invalid",
				Credential: "reader", Secrets: envCredential(tt.envNames),
			}, resolver(nil), nil)

			var missing *secret.MissingSecretError
			if !errors.As(err, &missing) {
				t.Fatalf("Open() = %v, want a *MissingSecretError", err)
			}
			if !strings.Contains(err.Error(), "credentials.reader.values.") {
				t.Errorf("error = %q, want it to name the configuration key", err)
			}
			for _, forbidden := range []string{pasted, "MISSING_ON_PURPOSE"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Errorf("error = %q, want it to keep %q out", err, forbidden)
				}
			}
		})
	}
}

func TestRegister(t *testing.T) {
	reg := capability.NewRegistry()

	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	got := reg.Provider(Provider)
	if len(got) != 29 {
		t.Fatalf("capabilities = %d, want 29", len(got))
	}
	wantRisk := capability.Risk{
		Effect:          capability.EffectRead,
		Idempotency:     capability.IdempotencySafe,
		Confirmation:    capability.ConfirmationNone,
		OpenWorld:       true,
		DataSensitivity: dataSensitivity,
	}
	for _, tt := range []struct {
		id   string
		risk capability.Risk
	}{
		{Provider + ".pages.get", wantRisk},
		{Provider + ".pages.list", wantRisk},
		{Provider + ".pages.create", pagesCreate.Risk},
		{Provider + ".pages.update", pagesUpdate.Risk},
		{Provider + ".pages.delete", pagesDelete.Risk},
		{Provider + ".content.search", wantRisk},
		{Provider + ".books.list", wantRisk},
		{Provider + ".books.get", wantRisk},
		{Provider + ".chapters.list", wantRisk},
		{Provider + ".chapters.get", wantRisk},
		{Provider + ".books.create", booksCreate.Risk},
		{Provider + ".books.update", booksUpdate.Risk},
		{Provider + ".chapters.create", chaptersCreate.Risk},
		{Provider + ".chapters.update", chaptersUpdate.Risk},
		{Provider + ".shelves.list", wantRisk},
		{Provider + ".shelves.get", wantRisk},
		{Provider + ".shelves.create", shelvesCreate.Risk},
		{Provider + ".shelves.update", shelvesUpdate.Risk},
		{Provider + ".books.delete", booksDelete.Risk},
		{Provider + ".chapters.delete", chaptersDelete.Risk},
		{Provider + ".shelves.delete", shelvesDelete.Risk},
	} {
		if tt.risk.Effect != capability.EffectRead && (tt.risk.Confirmation != capability.ConfirmationRequired ||
			tt.risk.Idempotency == "" || !tt.risk.OpenWorld || tt.risk.DataSensitivity == "") {
			t.Errorf("%s risk is incomplete: %+v", tt.id, tt.risk)
		}
		t.Run(tt.id, func(t *testing.T) {
			var descriptor capability.Descriptor
			for _, candidate := range got {
				if candidate.ID == tt.id {
					descriptor = candidate
					break
				}
			}
			if descriptor.ID == "" {
				t.Fatalf("operation %q is not registered", tt.id)
			}
			if descriptor.Risk != tt.risk {
				t.Errorf("risk = %+v, want %+v", descriptor.Risk, tt.risk)
			}
			if descriptor.RequiresToolAllowList != strings.HasSuffix(tt.id, ".delete") {
				t.Errorf("requires_tool_allow_list = %v", descriptor.RequiresToolAllowList)
			}
			if descriptor.Group != "content" {
				t.Errorf("group = %q, want content", descriptor.Group)
			}
			if descriptor.Provider != Provider || descriptor.Version != 1 {
				t.Errorf("operation = %+v, want provider %q version 1", descriptor, Provider)
			}
			if _, _, ok := reg.Lookup(descriptor.ID); !ok {
				t.Errorf("operation %q has no registered handler", descriptor.ID)
			}
		})
	}
}

func TestPageMutationsUseOnlyThePagesRoute(t *testing.T) {
	methods := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page(42, "Changed"))
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	if _, err := c.CreatePage(context.Background(), pageMutation{Name: "New", BookID: 7, Markdown: "body"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdatePage(context.Background(), "42", pageMutation{Name: "Changed"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePage(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /api/pages", "PUT /api/pages/42", "DELETE /api/pages/42"}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("requests = %v, want %v", methods, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// scopedServer serves pages and chapters of several books and records every request. Page 1 and chapter 10
// belong to book 7, page 2 and chapter 20 to book 9. With ignoreFilter set, the list route answers like an
// instance that does not know the filter and returns every page.
func scopedServer(t *testing.T, rec *recorder, ignoreFilter bool) *httptest.Server {
	t.Helper()
	pages := []map[string]any{
		{"id": 1, "book_id": 7, "chapter_id": 10, "name": "Own"},
		{"id": 2, "book_id": 9, "chapter_id": 20, "name": "Foreign"},
		{"id": 3, "book_id": 7, "chapter_id": 0, "name": "Own loose"},
		{"id": 4, "book_id": 9, "chapter_id": 0, "name": "Foreign loose"},
		{"id": 5, "book_id": 7, "chapter_id": 10, "name": "Own two"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pages", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(page(50, "Created"))
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		var matching []map[string]any
		for _, p := range pages {
			if !ignoreFilter {
				if v := r.URL.Query().Get("filter[book_id]"); v != "" && v != fmt.Sprint(p["book_id"]) {
					continue
				}
				if v := r.URL.Query().Get("filter[chapter_id]"); v != "" && v != fmt.Sprint(p["chapter_id"]) {
					continue
				}
			}
			matching = append(matching, p)
		}
		end := min(offset+count, len(matching))
		window := []map[string]any{}
		if offset < end {
			window = matching[offset:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": window, "total": len(matching)})
	})
	mux.HandleFunc("/api/pages/", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/pages/"))
		if r.Method != http.MethodGet {
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_ = json.NewEncoder(w).Encode(page(int64(id), "Changed"))
			return
		}
		for _, p := range pages {
			if p["id"] == id {
				_ = json.NewEncoder(w).Encode(p)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/chapters/", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch strings.TrimPrefix(r.URL.Path, "/api/chapters/") {
		case "10":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 10, "book_id": 7})
		case "20":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 20, "book_id": 9})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

func boundResolved(baseURL string, targets ...string) *config.Resolved {
	return &config.Resolved{
		Name: "wiki", Provider: Provider, BaseURL: baseURL, Targets: targets,
		Secrets: envCredential(map[string]string{roleTokenID: "TEST_TOKEN_ID", roleTokenSecret: "TEST_TOKEN_SECRET"}),
	}
}

func boundClient(t *testing.T, baseURL string, targets ...string) *Client {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	client, err := Open(context.Background(), boundResolved(baseURL, targets...), resolver(nil), nil)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	return client
}

func isInvalidRequest(err error) bool {
	var invalid *application.InvalidRequestError
	return errors.As(err, &invalid)
}

func ids(rows []output.Row) []int64 {
	out := []int64{}
	for _, row := range rows {
		out = append(out, row["id"].(int64))
	}
	return out
}

// A book_id outside the targets is refused before any secret is read and before any request is sent.
func TestForeignBookIsRefusedBeforeSecretsAndIO(t *testing.T) {
	rec := &recorder{}
	server := scopedServer(t, rec, false)
	// No token variables are set: a secret access would fail with a different error.
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	resolved := boundResolved(server.URL, "book/7")
	for _, tt := range []struct{ id, args string }{
		{pagesList.ID, `{"book_id":9}`},
		{pagesCreate.ID, `{"name":"x","book_id":9,"markdown":"m"}`},
	} {
		_, handler, _ := reg.Lookup(tt.id)
		_, err := handler(context.Background(), resolved, resolver(nil), nil, json.RawMessage(tt.args))
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "outside the books") || strings.Contains(err.Error(), "9") {
			t.Errorf("%s: err = %v, want an invalid-request without the book", tt.id, err)
		}
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}

func TestSeveralBooksNeedABookID(t *testing.T) {
	rec := &recorder{}
	server := scopedServer(t, rec, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	reg := capability.NewRegistry()
	_ = Register(reg)
	_, handler, _ := reg.Lookup(pagesList.ID)
	_, err := handler(context.Background(), boundResolved(server.URL, "book/7", "book/9"), resolver(nil), nil, json.RawMessage(`{}`))
	if !isInvalidRequest(err) || err.Error() != "book_id is required for a connection bound to several books" {
		t.Errorf("err = %v", err)
	}
	_, err = handler(context.Background(), boundResolved(server.URL, "book/7", "book/9"), resolver(nil), nil,
		json.RawMessage(`{"book_id":7,"chapter_id":10}`))
	if !isInvalidRequest(err) {
		t.Errorf("book_id and chapter_id together: err = %v", err)
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
		t.Errorf("requests = %v, want none", methods)
	}
}

func TestOneBoundBookIsTheDefault(t *testing.T) {
	rec := &recorder{}
	server := scopedServer(t, rec, false)
	got, err := boundClient(t, server.URL, "book/7").ListPages(context.Background(), listTarget{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 3, 5}; !reflect.DeepEqual(ids(got.Rows), want) {
		t.Errorf("ids = %v, want %v", ids(got.Rows), want)
	}
	_, _, queries, _ := rec.snapshot()
	if len(queries) != 1 || !strings.Contains(queries[0], "filter%5Bbook_id%5D=7") {
		t.Errorf("queries = %v, want one request filtered to book 7", queries)
	}
}

// BookStack ignores filters it does not know; the client check is what holds the boundary.
func TestIgnoredFilterIsEnforcedOnTheClient(t *testing.T) {
	for _, tt := range []struct {
		name          string
		target        listTarget
		limit, offset int
		want          []int64
	}{
		{"book", listTarget{BookID: 7}, 0, 0, []int64{1, 3, 5}},
		{"book limit", listTarget{BookID: 7}, 2, 0, []int64{1, 3}},
		{"book offset", listTarget{BookID: 7}, 0, 1, []int64{3, 5}},
		{"chapter", listTarget{ChapterID: 10}, 0, 0, []int64{1, 5}},
		{"implicit book", listTarget{}, 0, 0, []int64{1, 3, 5}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			server := scopedServer(t, rec, true)
			got, err := boundClient(t, server.URL, "book/7").ListPages(context.Background(), tt.target, tt.limit, tt.offset)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ids(got.Rows), tt.want) {
				t.Errorf("ids = %v, want %v", ids(got.Rows), tt.want)
			}
		})
	}
}

func TestListWithoutTargetsFiltersForTheGivenBookOnly(t *testing.T) {
	rec := &recorder{}
	server := scopedServer(t, rec, true)
	got, err := newClient(t, server.URL, nil).ListPages(context.Background(), listTarget{BookID: 9}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{2, 4}; !reflect.DeepEqual(ids(got.Rows), want) {
		t.Errorf("ids = %v, want %v", ids(got.Rows), want)
	}
	// An unbound connection reads no evidence for a chapter either.
	if _, err := newClient(t, server.URL, nil).ListPages(context.Background(), listTarget{ChapterID: 20}, 0, 0); err != nil {
		t.Fatal(err)
	}
	_, recorded, _, _ := rec.snapshot()
	for _, path := range recorded {
		if strings.HasPrefix(path, "/api/chapters/") {
			t.Errorf("unexpected evidence read %s", path)
		}
	}
}

// A server that never stops delivering new rows cannot keep a filtered listing reading.
func TestFilteredListingIsBounded(t *testing.T) {
	var requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		data := []map[string]any{}
		for i := 0; i < 500; i++ {
			data = append(data, map[string]any{"id": offset + i + 1, "book_id": 9})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "total": 1 << 30})
	}))
	defer server.Close()
	_, err := boundClient(t, server.URL, "book/7").ListPages(context.Background(), listTarget{}, 0, 0)
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.ClassInvalidResponse {
		t.Fatalf("err = %v, want invalid-provider-response", err)
	}
	if requests != maxScanRequests {
		t.Errorf("requests = %d, want %d", requests, maxScanRequests)
	}
}

func TestChapterIsBoundThroughOneRead(t *testing.T) {
	rec := &recorder{}
	server := scopedServer(t, rec, false)
	c := boundClient(t, server.URL, "book/7")

	got, err := c.ListPages(context.Background(), listTarget{ChapterID: 10}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 5}; !reflect.DeepEqual(ids(got.Rows), want) {
		t.Errorf("ids = %v, want %v", ids(got.Rows), want)
	}
	if _, paths, _, _ := rec.snapshot(); len(paths) != 2 {
		t.Errorf("requests = %v, want one chapter read and one list", paths)
	}

	rec2 := &recorder{}
	server2 := scopedServer(t, rec2, false)
	c2 := boundClient(t, server2.URL, "book/7")
	_, err = c2.ListPages(context.Background(), listTarget{ChapterID: 20}, 0, 0)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "20") || strings.Contains(err.Error(), "9") {
		t.Errorf("foreign chapter: err = %v", err)
	}
	_, err = c2.CreatePage(context.Background(), pageMutation{Name: "x", ChapterID: 20, Markdown: "m"})
	if !isInvalidRequest(err) {
		t.Errorf("create in a foreign chapter: err = %v", err)
	}
	methods, paths, _, _ := rec2.snapshot()
	want := []string{"GET /api/chapters/20", "GET /api/chapters/20"}
	got2 := []string{}
	for i := range methods {
		got2 = append(got2, methods[i]+" "+paths[i])
	}
	if !reflect.DeepEqual(got2, want) {
		t.Errorf("requests = %v, want %v", got2, want)
	}

	// Creating in an own chapter reads the chapter once, then posts.
	rec3 := &recorder{}
	server3 := scopedServer(t, rec3, false)
	if _, err := boundClient(t, server3.URL, "book/7").CreatePage(context.Background(), pageMutation{Name: "x", ChapterID: 10, Markdown: "m"}); err != nil {
		t.Fatal(err)
	}
	methods, paths, _, _ = rec3.snapshot()
	if len(methods) != 2 || methods[0] != "GET" || paths[0] != "/api/chapters/10" || methods[1] != "POST" {
		t.Errorf("requests = %v %v", methods, paths)
	}
}

// A foreign page costs exactly one evidence read and no change.
func TestForeignPageIsRefusedAfterOneRead(t *testing.T) {
	rec := &recorder{}
	server := scopedServer(t, rec, false)
	c := boundClient(t, server.URL, "book/7")

	_, err := c.UpdatePage(context.Background(), "2", pageMutation{Name: "x"})
	if !isInvalidRequest(err) || err.Error() != "the page is outside the books this connection is bound to" {
		t.Errorf("update: err = %v", err)
	}
	err = c.DeletePage(context.Background(), "2")
	if !isInvalidRequest(err) {
		t.Errorf("delete: err = %v", err)
	}
	_, err = c.GetPage(context.Background(), "2")
	if !isInvalidRequest(err) {
		t.Errorf("get: err = %v", err)
	}
	methods, paths, _, _ := rec.snapshot()
	for i := range methods {
		if methods[i] != http.MethodGet || paths[i] != "/api/pages/2" {
			t.Errorf("request %d = %s %s, want only the evidence read", i, methods[i], paths[i])
		}
	}
	if len(methods) != 3 {
		t.Errorf("requests = %d, want 3 (one per call)", len(methods))
	}
}

func TestOwnPageChangesReadOnceThenChange(t *testing.T) {
	rec := &recorder{}
	server := scopedServer(t, rec, false)
	c := boundClient(t, server.URL, "book/7")
	if _, err := c.UpdatePage(context.Background(), "1", pageMutation{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePage(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	methods, paths, _, _ := rec.snapshot()
	got := []string{}
	for i := range methods {
		got = append(got, methods[i]+" "+paths[i])
	}
	want := []string{"GET /api/pages/1", "PUT /api/pages/1", "GET /api/pages/1", "DELETE /api/pages/1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// pages.get on a bound connection is its own evidence: one request.
func TestBoundGetIsOneRequestAndUnboundAddsNoRead(t *testing.T) {
	rec := &recorder{}
	server := scopedServer(t, rec, false)
	obj, err := boundClient(t, server.URL, "book/7").GetPage(context.Background(), "1")
	if err != nil || len(obj.Fields) != 19 {
		t.Fatalf("GetPage() = %v, %v", obj, err)
	}
	if methods, _, _, _ := rec.snapshot(); len(methods) != 1 {
		t.Errorf("requests = %d, want 1", len(methods))
	}

	rec2 := &recorder{}
	server2 := scopedServer(t, rec2, false)
	c := newClient(t, server2.URL, nil)
	if _, err := c.UpdatePage(context.Background(), "2", pageMutation{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePage(context.Background(), "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreatePage(context.Background(), pageMutation{Name: "x", ChapterID: 20, Markdown: "m"}); err != nil {
		t.Fatal(err)
	}
	methods, _, _, _ := rec2.snapshot()
	if want := []string{"PUT", "DELETE", "POST"}; !reflect.DeepEqual(methods, want) {
		t.Errorf("requests = %v, want %v and no evidence read", methods, want)
	}
}

func TestInvalidTargetsFailClosedWithoutQuotingTheValue(t *testing.T) {
	huge := make([]string, 101)
	for i := range huge {
		huge[i] = "book/" + strconv.Itoa(i+1)
	}
	for _, tt := range []struct {
		name    string
		targets []string
	}{
		{"leading zero", []string{"book/07"}},
		{"zero", []string{"book/0"}},
		{"negative", []string{"book/-4"}},
		{"duplicate", []string{"book/7", "book/7"}},
		{"wildcard", []string{"*"}},
		{"wrong form", []string{"shelf/7"}},
		{"text id", []string{"book/seven77"}},
		{"too many", huge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			server := scopedServer(t, rec, false)
			t.Setenv("TEST_TOKEN_ID", canaryID)
			t.Setenv("TEST_TOKEN_SECRET", canarySecret)
			_, err := Open(context.Background(), boundResolved(server.URL, tt.targets...), resolver(nil), nil)
			if err == nil {
				t.Fatal("Open() accepted an invalid target")
			}
			for _, value := range tt.targets[:1] {
				if strings.Contains(err.Error(), value) {
					t.Errorf("error = %q quotes %q", err, value)
				}
			}
			if methods, _, _, _ := rec.snapshot(); len(methods) != 0 {
				t.Errorf("requests = %v, want none", methods)
			}
			meta := targetMetadata(t)
			var verr error
			if len(tt.targets) == 1 {
				verr = meta.Validate(tt.targets[0])
			}
			if verr == nil {
				verr = meta.ValidateSet(tt.targets)
			}
			if verr == nil {
				t.Error("the configuration validators accept the invalid target")
			}
		})
	}
	// A legacy single target is read too, and an unreadable one stays closed.
	_, err := Open(context.Background(), &config.Resolved{Name: "w", Provider: Provider, BaseURL: "https://x.invalid", Target: "bogus"}, resolver(nil), nil)
	if err == nil {
		t.Error("Open() accepted an invalid single target")
	}
}

func targetMetadata(t *testing.T) config.TargetMetadata {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	target := meta.Target
	if target.Label != "books" || !target.Multiple || len(target.Kinds) != 1 || target.Kinds[0].Name != "book" ||
		!equal(target.Kinds[0].Forms, []string{"book/BOOK_ID"}) || target.Required || target.Wildcard != "" {
		t.Fatalf("target metadata = %+v", target)
	}
	return target
}

func TestValidTargetsAreAccepted(t *testing.T) {
	meta := targetMetadata(t)
	if err := meta.Validate("book/12"); err != nil {
		t.Errorf("Validate() = %v", err)
	}
	if err := meta.ValidateSet([]string{"book/12", "book/13"}); err != nil {
		t.Errorf("ValidateSet() = %v", err)
	}
}

func TestOpenRejectsUnusableBaseURL(t *testing.T) {
	for _, base := range []string{
		"http://wiki.example.com", "http://localhost", "http://127.0.0.1:6875", "ftp://wiki.example.com",
		"https://user@wiki.example.com", "https://user:pw@wiki.example.com", "https://wiki.example.com?x=1",
		"https://wiki.example.com/wiki?", "https://wiki.example.com#frag", "wiki.example.com", "https://",
	} {
		t.Run(base, func(t *testing.T) {
			reads := 0
			secrets := secret.NewWith(func(string) string { reads++; return "x" }, secret.NewMemoryStore(), nil, nil)
			_, err := Open(context.Background(), &config.Resolved{
				Name: "wiki", Provider: Provider, BaseURL: base,
				Secrets: envCredential(map[string]string{roleTokenID: "A", roleTokenSecret: "B"}),
			}, secrets, nil)
			var perr *provider.Error
			if !errors.As(err, &perr) || perr.Class != provider.ClassProviderError {
				t.Fatalf("Open() error = %v, want a provider error", err)
			}
			if !strings.Contains(err.Error(), "a BookStack service needs a usable https URL") ||
				strings.Contains(err.Error(), "wiki.example.com") || strings.Contains(err.Error(), "127.0.0.1") {
				t.Errorf("message = %q", err.Error())
			}
			if reads != 0 {
				t.Errorf("secret reads = %d, want 0", reads)
			}
		})
	}
}

func TestInstallationPathIsKept(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
	}))
	defer server.Close()

	if _, err := newClient(t, server.URL+"/wiki/", nil).ListPages(context.Background(), listTarget{}, 1, 0); err != nil {
		t.Fatalf("ListPages() = %v", err)
	}
	if _, paths, _, _ := rec.snapshot(); len(paths) != 1 || paths[0] != "/wiki/api/pages" {
		t.Errorf("paths = %v, want /wiki/api/pages", paths)
	}
}
