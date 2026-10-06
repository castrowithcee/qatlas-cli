package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Reproducible load profiles for large installations. Nothing here reads a real configuration, a secret,
// or a network: every fixture is generated into a temporary directory, its credentials name invented
// environment variables, and its endpoints are reserved .invalid hosts or the fixed public origins of the
// providers that insist on them. The sizes are synthetic probe points, not limits of the product.
//
// The benchmarks run only with -bench; the single regular test below uses a small shape.
//
//	go test ./internal/cli -run '^$' -bench 'BenchmarkScale' -benchtime 5x -count 3
//
// The fixture generator can also write the profiles to disk, for measuring a built binary:
//
//	QATLAS_SCALE_FIXTURE_DIR=/some/empty/dir go test ./internal/cli -run TestScaleWriteFixtures

// scaleShape sizes one fixture. Services, credentials, and connections vary independently; the providers
// are shared round-robin, so every provider owns several services, credentials, and connections.
type scaleShape struct {
	name                               string
	services, credentials, connections int
}

func (s scaleShape) entries() int { return s.services + s.credentials + s.connections }

// scaleProfiles returns the measured profiles. "total" is the sum of services, credentials, and
// connections. "balanced" has as many of each, "connheavy" many connections over few services and
// credentials, and "units" one service, credential, and connection per unit, the shape an earlier
// validation run used.
func scaleProfiles() []scaleShape {
	var profiles []scaleShape
	for _, total := range []int{1000, 5000, 10000} {
		third := total / 3
		profiles = append(profiles,
			scaleShape{fmt.Sprintf("balanced-%d", total), third, third, total - 2*third},
			scaleShape{fmt.Sprintf("connheavy-%d", total), total / 20, total / 20, total - 2*(total/20)},
		)
	}
	for _, units := range []int{1000, 5000, 10000} {
		profiles = append(profiles, scaleShape{fmt.Sprintf("units-%d", units), units, units, units})
	}
	return profiles
}

// scaleProvider describes how one statically registered provider appears in a fixture.
type scaleProvider struct {
	id      string
	baseURL string // a format with the service number, or a fixed origin
	roles   []string
	target  string
}

var scaleProviders = []scaleProvider{
	{"todoist", "https://api.todoist.com/api/v1", []string{"token"}, "p1"},
	{"github", "https://api.github.com", []string{"token"}, ""},
	{"seatable", "https://seatable-%d.example.invalid", []string{"api-token"}, "Table"},
	{"bookstack", "https://wiki-%d.example.invalid", []string{"token-id", "token-secret"}, ""},
	{"nextcloud", "https://cloud-%d.example.invalid", []string{"user-id", "app-password"}, "Folder"},
	{"twentycrm", "https://crm-%d.example.invalid", []string{"api-key"}, ""},
	{"n8n", "https://n8n-%d.example.invalid", []string{"api-key"}, ""},
}

func scaleServiceName(i int) string    { return fmt.Sprintf("svc-%05d", i) }
func scaleCredentialName(i int) string { return fmt.Sprintf("cred-%05d", i) }
func scaleConnectionName(i int) string { return fmt.Sprintf("conn-%05d", i) }

func scaleEnvName(credential int, role string) string {
	return fmt.Sprintf("SCALE_CRED_%05d_%s", credential, strings.ToUpper(strings.ReplaceAll(role, "-", "_")))
}

// scaleYAML renders the configuration of one shape. Service s and credential c belong to provider s%P and
// c%P; connection k binds service k%S to a credential of the same provider.
func scaleYAML(shape scaleShape) []byte {
	providers := len(scaleProviders)
	var b strings.Builder
	b.WriteString("version: 1\nservices:\n")
	for s := 0; s < shape.services; s++ {
		p := scaleProviders[s%providers]
		baseURL := p.baseURL
		if strings.Contains(baseURL, "%d") {
			baseURL = fmt.Sprintf(baseURL, s)
		}
		fmt.Fprintf(&b, "  %s:\n    provider: %s\n    base_url: %s\n", scaleServiceName(s), p.id, baseURL)
	}
	b.WriteString("credentials:\n")
	for c := 0; c < shape.credentials; c++ {
		p := scaleProviders[c%providers]
		fmt.Fprintf(&b, "  %s:\n    provider: %s\n    type: env\n    values:\n", scaleCredentialName(c), p.id)
		for _, role := range p.roles {
			fmt.Fprintf(&b, "      %s: %s\n", role, scaleEnvName(c, role))
		}
	}
	b.WriteString("connections:\n")
	for k := 0; k < shape.connections; k++ {
		s := k % shape.services
		provider := s % providers
		// The credentials of this provider are provider, provider+P, provider+2P, ...
		owned := (shape.credentials - provider + providers - 1) / providers
		c := provider + providers*(k%owned)
		p := scaleProviders[provider]
		fmt.Fprintf(&b, "  %s:\n    service: %s\n    credential: %s\n", scaleConnectionName(k),
			scaleServiceName(s), scaleCredentialName(c))
		if p.target != "" {
			fmt.Fprintf(&b, "    target: %s\n", p.target)
		}
		fmt.Fprintf(&b, "    description: synthetic %s route %d for load profile %s\n", p.id, k, shape.name)
		if k%2 == 0 {
			b.WriteString("    permissions: [read]\n")
		} else {
			b.WriteString("    permissions: [read, create]\n")
		}
	}
	b.WriteString("defaults: {}\n")
	return []byte(b.String())
}

func writeScaleFixture(tb testing.TB, dir string, shape scaleShape) string {
	tb.Helper()
	path := filepath.Join(dir, shape.name+".yaml")
	if err := os.WriteFile(path, scaleYAML(shape), 0o600); err != nil {
		tb.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestScaleFixtureIsValid keeps the generator honest at a size that costs next to nothing: the fixture
// loads under the shipped registry, every provider of the table owns routes, and every route can be
// resolved by name.
func TestScaleFixtureIsValid(t *testing.T) {
	shape := scaleShape{"small", 21, 14, 70}
	reg := defaultRegistry()
	cfg, err := config.Load(writeScaleFixture(t, t.TempDir(), shape), reg)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if len(cfg.Services) != shape.services || len(cfg.Credentials) != shape.credentials ||
		len(cfg.Connections) != shape.connections {
		t.Fatalf("counts = %d/%d/%d", len(cfg.Services), len(cfg.Credentials), len(cfg.Connections))
	}
	core := application.New(reg, cfg, secret.NewWith(nil, nil, nil, nil), &redact.Redactor{})
	byProvider := map[string]int{}
	for _, conn := range core.Connections("", nil).Connections {
		byProvider[conn.Provider]++
	}
	if len(byProvider) != len(scaleProviders) {
		t.Fatalf("providers with routes = %v, want %d", byProvider, len(scaleProviders))
	}
	if strings.Contains(string(scaleYAML(shape)), "://localhost") {
		t.Fatal("a fixture must not name a reachable host")
	}
}

// TestScaleWriteFixtures writes every profile into QATLAS_SCALE_FIXTURE_DIR so a built binary can be
// measured with --config. It does nothing without the variable.
func TestScaleWriteFixtures(t *testing.T) {
	dir := os.Getenv("QATLAS_SCALE_FIXTURE_DIR")
	if dir == "" {
		t.Skip("QATLAS_SCALE_FIXTURE_DIR is not set")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, shape := range scaleProfiles() {
		writeScaleFixture(t, dir, shape)
	}
}

// scaleRegistry is the shipped registry plus one tool without effect on any provider, so the preparation of
// a call can be measured without a handler behind it.
func scaleRegistry(tb testing.TB) *capability.Registry {
	tb.Helper()
	reg := defaultRegistry()
	err := reg.Register("bookstack", capability.Operation{
		Descriptor: capability.Descriptor{
			ID: "bookstack.scale.noop", Version: 1, Description: "Scale probe without handler work",
			Provider: "bookstack",
			Risk: capability.Risk{
				Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
				Confirmation: capability.ConfirmationNone, DataSensitivity: "synthetic",
			},
			InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
			json.RawMessage) (any, error) {
			return map[string]any{}, nil
		},
	})
	if err != nil {
		tb.Fatalf("Register() = %v", err)
	}
	return reg
}

func scaleCore(tb testing.TB, reg *capability.Registry, path string) *application.Core {
	tb.Helper()
	cfg, err := config.Load(path, reg)
	if err != nil {
		tb.Fatalf("Load() = %v", err)
	}
	return application.New(reg, cfg, secret.NewWith(nil, nil, nil, nil), &redact.Redactor{})
}

// forEachProfile runs fn once per profile with its fixture path, generated once per profile.
func forEachProfile(b *testing.B, fn func(b *testing.B, shape scaleShape, path string)) {
	dir := b.TempDir()
	// A call that forgets its configuration path must find an empty home, never a real configuration.
	b.Setenv("HOME", dir)
	b.Setenv("QATLAS_CLI_HOME", dir)
	b.Setenv("QATLAS_CONFIG", "")
	for _, shape := range scaleProfiles() {
		path := writeScaleFixture(b, dir, shape)
		b.Run(shape.name, func(b *testing.B) { fn(b, shape, path) })
	}
}

func BenchmarkScaleConfigLoad(b *testing.B) {
	reg := defaultRegistry()
	forEachProfile(b, func(b *testing.B, _ scaleShape, path string) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := config.Load(path, reg); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkScaleConfigSave measures the write path of the store: validate, encode, check that the encoding
// loads back, write a temporary file, sync, and rename it over the target. Every change rewrites the whole
// file, so this is the cost of one saved edit.
func BenchmarkScaleConfigSave(b *testing.B) {
	reg := defaultRegistry()
	forEachProfile(b, func(b *testing.B, _ scaleShape, path string) {
		store := config.NewStore(filepath.Join(b.TempDir(), "config.yaml"), reg)
		cfg, err := config.Load(path, reg)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := store.Save(cfg); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkScaleCoreDiscovery measures the answers of the application core on an already loaded
// configuration, which is the cost a long-lived caller pays per request.
func BenchmarkScaleCoreDiscovery(b *testing.B) {
	reg := defaultRegistry()
	queries := []struct {
		name string
		run  func(core *application.Core, shape scaleShape) error
	}{
		{"providers", func(c *application.Core, _ scaleShape) error { c.Providers(); return nil }},
		{"connections-all", func(c *application.Core, _ scaleShape) error { c.Connections("", nil); return nil }},
		{"connections-provider", func(c *application.Core, _ scaleShape) error {
			c.Connections("bookstack", nil)
			return nil
		}},
		{"tools-provider", func(c *application.Core, _ scaleShape) error {
			_, err := c.Tools(application.SearchRequest{Provider: "bookstack"})
			return err
		}},
		{"search-query", func(c *application.Core, _ scaleShape) error {
			_, err := c.Search(application.SearchRequest{Query: "list pages"})
			return err
		}},
		{"search-query-provider", func(c *application.Core, _ scaleShape) error {
			_, err := c.Search(application.SearchRequest{Query: "list", Provider: "bookstack"})
			return err
		}},
		{"search-query-connection", func(c *application.Core, shape scaleShape) error {
			_, err := c.Search(application.SearchRequest{Query: "list", Connection: scaleConnectionName(3)})
			return err
		}},
		{"describe", func(c *application.Core, _ scaleShape) error {
			_, err := c.Describe(application.DescribeRequest{Operation: "bookstack.pages.list"})
			return err
		}},
		{"describe-connection", func(c *application.Core, _ scaleShape) error {
			_, err := c.Describe(application.DescribeRequest{
				Operation: "bookstack.pages.list", Connection: scaleConnectionName(3),
			})
			return err
		}},
	}
	forEachProfile(b, func(b *testing.B, shape scaleShape, path string) {
		core := scaleCore(b, reg, path)
		for _, q := range queries {
			b.Run(q.name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if err := q.run(core, shape); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	})
}

// BenchmarkScaleInvokePrepare measures a call up to, and not including, the handler: argument and schema
// validation, connection selection, and policy. "explicit" names the connection, "ambiguous" does not and
// ends in the error that lists every candidate.
func BenchmarkScaleInvokePrepare(b *testing.B) {
	reg := scaleRegistry(b)
	forEachProfile(b, func(b *testing.B, _ scaleShape, path string) {
		core := scaleCore(b, reg, path)
		// The last bookstack route is the one a name lookup reaches last.
		routes := core.Connections("bookstack", nil).Connections
		name := routes[len(routes)-1].Name
		b.Run("explicit", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := core.Invoke(context.Background(), application.InvokeRequest{
					Operation: "bookstack.scale.noop", Connection: name,
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("ambiguous", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := core.Invoke(context.Background(), application.InvokeRequest{
					Operation: "bookstack.scale.noop",
				})
				if err == nil {
					b.Fatal("a call without a connection must be ambiguous")
				}
			}
		})
	})
}

// scaleOptions builds options for an in-process command: the credential resolver finds no value anywhere.
func scaleOptions(path string) *Options {
	redactor := &redact.Redactor{}
	return &Options{
		Config: path, Redactor: redactor, Input: strings.NewReader(""),
		Secrets: secret.NewWith(func(string) string { return "" }, nil, nil, redactor),
	}
}

// BenchmarkScaleCLI runs the discovery commands through the real command tree, so every iteration loads
// and validates the configuration like a separate process would, without the process start.
func BenchmarkScaleCLI(b *testing.B) {
	reg := defaultRegistry()
	commands := map[string][]string{
		"providers":   {"providers"},
		"connections": {"connections", "bookstack"},
		"tools":       {"tools", "bookstack"},
		"describe":    {"describe", "bookstack.pages.list"},
		"tools-query": {"tools", "--query", "list pages"},
	}
	forEachProfile(b, func(b *testing.B, _ scaleShape, path string) {
		for name, args := range commands {
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					opts := scaleOptions(path)
					var stderr strings.Builder
					// The flag default of the command tree overwrites Options.Config, so the path travels as a flag.
					full := append([]string{"--config", path}, args...)
					if code := run(newRootCommand(opts, reg), opts, full, io.Discard, &stderr); code != exitOK {
						b.Fatalf("exit code %d for %v: %s", code, args, stderr.String())
					}
				}
			})
		}
	})
}

// BenchmarkScaleMCP serves one tools/call per iteration, which loads the configuration for that call.
func BenchmarkScaleMCP(b *testing.B) {
	reg := scaleRegistry(b)
	calls := map[string]string{
		"search":          `"name":"qatlas.search","arguments":{"query":"list pages"}`,
		"search-provider": `"name":"qatlas.search","arguments":{"query":"list","provider":"bookstack"}`,
		"providers":       `"name":"qatlas.search","arguments":{"list":"providers"}`,
		"connections":     `"name":"qatlas.search","arguments":{"list":"connections","provider":"bookstack"}`,
		"describe":        `"name":"qatlas.describe","arguments":{"operation":"bookstack.pages.list"}`,
	}
	forEachProfile(b, func(b *testing.B, _ scaleShape, path string) {
		for name, call := range calls {
			input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta + `,` + call + `}}` + "\n"
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					server := newMCPServer(scaleOptions(path), reg, io.Discard, io.Discard)
					if err := server.serve(context.Background(), strings.NewReader(input)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	})
}

// BenchmarkScaleParallelReaders runs five agents at once, each loading the configuration and answering a
// discovery call per iteration, the way five concurrent CLI or MCP calls would. One iteration is one round
// in which all five finish.
func BenchmarkScaleParallelReaders(b *testing.B) {
	const readers = 5
	reg := defaultRegistry()
	forEachProfile(b, func(b *testing.B, _ scaleShape, path string) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var wg sync.WaitGroup
			errs := make(chan error, readers)
			for r := 0; r < readers; r++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					opts := scaleOptions(path)
					if code := run(newRootCommand(opts, reg), opts, []string{"--config", path, "tools", "bookstack"},
						io.Discard, io.Discard); code != exitOK {
						errs <- fmt.Errorf("exit code %d", code)
					}
				}()
			}
			wg.Wait()
			close(errs)
			if err := <-errs; err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkScaleRegistry measures building the static provider registry, the cost every process pays once
// no matter how little configuration it has.
func BenchmarkScaleRegistry(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = defaultRegistry()
	}
}

// scaleExtraProviders registers count more synthetic providers with toolsEach read tools each on top of the
// shipped registry, to measure what a growing static provider list costs while the configuration stays
// small. The configuration never uses them.
func scaleExtraProviders(tb testing.TB, count, toolsEach int) *capability.Registry {
	tb.Helper()
	reg := defaultRegistry()
	for p := 0; p < count; p++ {
		id := fmt.Sprintf("synth%03d", p)
		if err := reg.RegisterProvider(config.ProviderMetadata{ID: id, Name: id, Description: "Synthetic provider"}, nil); err != nil {
			tb.Fatalf("RegisterProvider() = %v", err)
		}
		operations := make([]capability.Operation, toolsEach)
		for i := range operations {
			operations[i] = capability.Operation{
				Descriptor: capability.Descriptor{
					ID: fmt.Sprintf("%s.object%d.list", id, i), Version: 1, Provider: id,
					Description: fmt.Sprintf("List synthetic object %d", i),
					Risk: capability.Risk{
						Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
						Confirmation: capability.ConfirmationNone, DataSensitivity: "synthetic",
					},
					InputSchema:  json.RawMessage(`{"type":"object"}`),
					OutputSchema: json.RawMessage(`{"type":"array"}`),
				},
				Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
					json.RawMessage) (any, error) {
					return []any{}, nil
				},
			}
		}
		if err := reg.Register(id, operations...); err != nil {
			tb.Fatalf("Register() = %v", err)
		}
	}
	return reg
}

// BenchmarkScaleProviderCount grows the static provider list by synthetic providers of 30 tools each, over a
// small configuration of the shipped providers. "build" is the registration itself, the rest are the
// discovery answers over the larger catalog.
func BenchmarkScaleProviderCount(b *testing.B) {
	const toolsEach = 30
	dir := b.TempDir()
	path := writeScaleFixture(b, dir, scaleShape{"small", 21, 14, 70})
	for _, extra := range []int{0, 20, 100, 500} {
		b.Run(fmt.Sprintf("extra-%d", extra), func(b *testing.B) {
			b.Run("build", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					scaleExtraProviders(b, extra, toolsEach)
				}
			})
			core := scaleCore(b, scaleExtraProviders(b, extra, toolsEach), path)
			for name, run := range map[string]func() error{
				"providers": func() error { core.Providers(); return nil },
				"search-query": func() error {
					_, err := core.Search(application.SearchRequest{Query: "list pages"})
					return err
				},
				"search-all": func() error {
					_, err := core.Search(application.SearchRequest{Query: "list", All: true})
					return err
				},
				"tools-provider": func() error {
					_, err := core.Tools(application.SearchRequest{Provider: "bookstack"})
					return err
				},
			} {
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						if err := run(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
