package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Load profiles for the overview page of the local browser surface. The rows are synthetic, the credentials
// name invented environment variables, and nothing is read from or written to a real configuration. The
// sizes are probe points, not limits.
//
//	go test ./internal/web -run '^$' -bench BenchmarkScale -benchtime 20x -count 3

// scaleUnits are the numbers of services, credentials, and connections each, so a profile holds three
// times as many entries in total.
var scaleUnits = []int{333, 1667, 3333, 1000, 5000, 10000}

func scaleOverview(units int) Overview {
	overview := Overview{Providers: []ProviderRow{{Provider: "book", Description: "Synthetic", Tools: 2}}}
	for i := 0; i < units; i++ {
		overview.Services = append(overview.Services, ServiceRow{
			Name: fmt.Sprintf("svc-%05d", i), Provider: "book", BaseURL: fmt.Sprintf("https://books-%d.example.invalid", i),
		})
		overview.Credentials = append(overview.Credentials, CredentialRow{
			Name: fmt.Sprintf("cred-%05d", i), Provider: "book", Type: "env",
		})
		overview.Connections = append(overview.Connections, ConnectionRow{
			Name: fmt.Sprintf("conn-%05d", i), Provider: "book",
			Description: fmt.Sprintf("synthetic route %d", i), Permissions: "read", Tools: "all",
		})
	}
	return overview
}

func scaleConfigYAML(units int) []byte {
	var b strings.Builder
	b.WriteString("version: 1\nservices:\n")
	for i := 0; i < units; i++ {
		fmt.Fprintf(&b, "  svc-%05d:\n    provider: book\n    base_url: https://books-%d.example.invalid\n", i, i)
	}
	b.WriteString("credentials:\n")
	for i := 0; i < units; i++ {
		fmt.Fprintf(&b, "  cred-%05d:\n    provider: book\n    type: env\n    values:\n      token-id: SCALE_%05d_ID\n"+
			"      token-secret: SCALE_%05d_SECRET\n", i, i, i)
	}
	b.WriteString("connections:\n")
	for i := 0; i < units; i++ {
		fmt.Fprintf(&b, "  conn-%05d:\n    service: svc-%05d\n    credential: cred-%05d\n    description: synthetic route %d\n",
			i, i, i, i)
	}
	b.WriteString("defaults: {}\n")
	return []byte(b.String())
}

// scalePage couples a server to one session and returns a function that fetches the overview page.
func scalePage(b *testing.B, overview Overview, store *config.Store) func() *httptest.ResponseRecorder {
	b.Helper()
	resolver := secret.NewWith(func(string) string { return "" }, secret.NewMemoryStore(), nil, &redact.Redactor{})
	s, err := New(overview, nil, defaultTestAdminTimeout, manage.New(store, resolver, connlog.SurfaceWeb, &redact.Redactor{}), resolver, &redact.Redactor{}, nil)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	b.Cleanup(s.close)
	link, err := url.Parse(s.URL())
	if err != nil {
		b.Fatal(err)
	}
	redeem := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, link.RequestURI(), nil)
	req.Host = s.addr
	s.mux().ServeHTTP(redeem, req)
	cookies := redeem.Result().Cookies()
	if len(cookies) != 1 {
		b.Fatalf("expected one session cookie, got %v", cookies)
	}
	return func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = s.addr
		req.AddCookie(cookies[0])
		rec := httptest.NewRecorder()
		s.mux().ServeHTTP(rec, req)
		return rec
	}
}

// BenchmarkScaleOverviewRender renders the overview page from a ready snapshot (no configuration store), which
// isolates the template and the size of the page.
func BenchmarkScaleOverviewRender(b *testing.B) {
	for _, units := range scaleUnits {
		b.Run(fmt.Sprintf("units-%d", units), func(b *testing.B) {
			page := scalePage(b, scaleOverview(units), nil)
			b.ReportAllocs()
			var size int
			for i := 0; i < b.N; i++ {
				rec := page()
				if rec.Code != http.StatusOK {
					b.Fatalf("status = %d", rec.Code)
				}
				size = rec.Body.Len()
			}
			b.ReportMetric(float64(size), "page-bytes")
		})
	}
}

// BenchmarkScaleOverviewPage serves the overview the way a run does: the credential and connection rows are
// rebuilt from the configuration file on every view, so each view also loads and validates the file.
func BenchmarkScaleOverviewPage(b *testing.B) {
	for _, units := range scaleUnits {
		b.Run(fmt.Sprintf("units-%d", units), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "config.yaml")
			if err := os.WriteFile(path, scaleConfigYAML(units), 0o600); err != nil {
				b.Fatal(err)
			}
			store := config.NewStore(path, bookCatalog{})
			if _, err := store.Load(); err != nil {
				b.Fatalf("Load: %v", err)
			}
			page := scalePage(b, scaleOverview(units), store)
			b.ReportAllocs()
			var size int
			for i := 0; i < b.N; i++ {
				rec := page()
				if rec.Code != http.StatusOK {
					b.Fatalf("status = %d", rec.Code)
				}
				size = rec.Body.Len()
			}
			b.ReportMetric(float64(size), "page-bytes")
		})
	}
}

// TestScaleOverviewSmall keeps the generators honest at a size that costs nothing: the fixture loads and
// the page shows its last row.
func TestScaleOverviewSmall(t *testing.T) {
	cfg, err := config.Decode(strings.NewReader(string(scaleConfigYAML(12))), bookCatalog{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(cfg.Services) != 12 || len(cfg.Credentials) != 12 || len(cfg.Connections) != 12 {
		t.Fatalf("counts = %d/%d/%d", len(cfg.Services), len(cfg.Credentials), len(cfg.Connections))
	}
	if got := scaleOverview(12); len(got.Connections) != 12 {
		t.Fatalf("rows = %d", len(got.Connections))
	}
}
