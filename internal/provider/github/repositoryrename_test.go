package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

const renameRoute = "/api/v3/repos/octo-org/example"

// renameCore serves the one rename route with answer and returns the fake, the credential read counter, and
// a core over connections that cover the rename in different ways.
func renameCore(t *testing.T, answer func(http.ResponseWriter, *http.Request)) (*fakeGitHub, *int, *application.Core) {
	t.Helper()
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPatch || r.URL.Path != renameRoute {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		answer(w, r)
		return true
	}
	base := serve(t, f)
	cfg := coreConfig(base)
	all := config.Effects()
	add := func(name string, targets, excludes []string, tools ...string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets,
			Exclude: excludes, Effects: all, Tools: tools}
	}
	rename := repositoriesRename.ID
	add("listed", []string{"repos/octo-org/*"}, nil, rename)
	add("unlisted", []string{"repos/octo-org/*"}, nil)
	add("exact-both", []string{"repos/octo-org/example", "repos/octo-org/Example-New"}, nil, rename)
	add("exact-old-only", []string{"repos/octo-org/example"}, nil, rename)
	add("exact-new-only", []string{"repos/octo-org/example-new"}, nil, rename)
	add("excluded-new", []string{"repos/octo-org/*"}, []string{"repos/octo-org/example-new"}, rename)
	add("excluded-old", []string{"repos/octo-org/*"}, []string{"repos/octo-org/example"}, rename)
	reads := 0
	red := &redact.Redactor{}
	return f, &reads, application.New(registry(t), cfg, resolver(red, &reads), red)
}

func renamed(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(w, `{"full_name":"octo-org/example-new","html_url":"https://github.com/octo-org/example-new",`+
		`"description":"SECRETTEXT"}`)
}

func patches(f *fakeGitHub) int {
	n := 0
	for _, r := range f.recorded() {
		if r.method == http.MethodPatch {
			n++
		}
	}
	return n
}

func TestRepositoriesRenameSendsOneNameOnlyPatch(t *testing.T) {
	f, _, core := renameCore(t, renamed)
	result, err := invoke(t, core, repositoriesRename.ID, "listed",
		`{"repository":"octo-org/example","name":"example-new"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(result, &got) != nil || len(got) != 3 || got["repository"] != "octo-org/example-new" ||
		got["previous"] != "octo-org/example" || got["url"] != "https://github.com/octo-org/example-new" {
		t.Errorf("result = %s, want repository, previous, and url only", result)
	}
	requests := f.recorded()
	if len(requests) != 1 || requests[0].method != http.MethodPatch || len(requests[0].body) != 1 ||
		requests[0].body["name"] != "example-new" {
		t.Errorf("requests = %+v, want one PATCH with only the name", requests)
	}
}

func TestRepositoriesRenameAllowsACaseOnlyChange(t *testing.T) {
	_, _, core := renameCore(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"full_name":"octo-org/Example","html_url":"https://github.com/octo-org/Example"}`)
	})
	result, err := invoke(t, core, repositoriesRename.ID, "listed", `{"repository":"octo-org/example","name":"Example"}`, true)
	if err != nil || !strings.Contains(string(result), `"repository":"octo-org/Example"`) {
		t.Errorf("case-only rename = %s, %v", result, err)
	}
}

func TestRepositoriesRenameChecksBothNamesBeforeAnySecretOrRequest(t *testing.T) {
	f, reads, core := renameCore(t, renamed)
	for name, tt := range map[string]struct{ connection, arguments string }{
		"new name outside the targets": {"exact-old-only", `{"repository":"octo-org/example","name":"example-new"}`},
		"old name outside the targets": {"exact-new-only", `{"repository":"octo-org/example","name":"example-new"}`},
		"new name excluded":            {"excluded-new", `{"repository":"octo-org/example","name":"Example-New"}`},
		"old name excluded":            {"excluded-old", `{"repository":"octo-org/example","name":"example-new"}`},
		"old name outside by owner":    {"listed", `{"repository":"other-org/example","name":"example-new"}`},
		"empty name":                   {"listed", `{"repository":"octo-org/example","name":""}`},
		"dot":                          {"listed", `{"repository":"octo-org/example","name":"."}`},
		"dot dot":                      {"listed", `{"repository":"octo-org/example","name":".."}`},
		"git suffix":                   {"listed", `{"repository":"octo-org/example","name":"x.git"}`},
		"slash":                        {"listed", `{"repository":"octo-org/example","name":"other/x"}`},
		"too long":                     {"listed", `{"repository":"octo-org/example","name":"` + strings.Repeat("a", 101) + `"}`},
		"same name":                    {"listed", `{"repository":"octo-org/example","name":"example"}`},
		"missing name":                 {"listed", `{"repository":"octo-org/example"}`},
	} {
		*reads = 0
		_, err := invoke(t, core, repositoriesRename.ID, tt.connection, tt.arguments, true)
		if !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", name, err)
			continue
		}
		if strings.Contains(err.Error(), "example-new") || strings.Contains(err.Error(), "Example-New") {
			t.Errorf("%s quotes a name: %v", name, err)
		}
		if *reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("%s reached the credential or GitHub", name)
		}
	}
	// Both names inside the targets, spelled without regard to case, go through.
	if _, err := invoke(t, core, repositoriesRename.ID, "exact-both",
		`{"repository":"octo-org/example","name":"example-new"}`, true); err != nil {
		t.Errorf("both names allowed = %v", err)
	}
}

func TestRepositoriesRenameIsOfferedOnlyWhereListedAndNeedsConfirmation(t *testing.T) {
	f, reads, core := renameCore(t, renamed)
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, repositoriesRename.ID, "unlisted",
		`{"repository":"octo-org/example","name":"example-new"}`, true); !errors.As(err, &unsupported) {
		t.Errorf("without a tools entry = %v, want unsupported-capability", err)
	}
	if _, err := invoke(t, core, repositoriesRename.ID, "listed",
		`{"repository":"octo-org/example","name":"example-new"}`, false); err == nil || *reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("unconfirmed = %v, reads %d", err, *reads)
	}
	reg := registry(t)
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == repositoriesRename.ID {
				t.Errorf("profile %s contains the rename", profile.ID)
			}
		}
	}
	risk := repositoriesRename.Risk
	if risk.Effect != capability.EffectUpdate || risk.Idempotency != capability.IdempotencyUnknown ||
		risk.Confirmation != capability.ConfirmationRequired || !risk.OpenWorld {
		t.Errorf("risk = %+v", risk)
	}
}

func TestRepositoriesRenameMapsRefusalsWithoutProviderText(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		class  provider.Class
		want   string
	}{
		{"name taken", http.StatusUnprocessableEntity, "", "already taken"},
		{"not found", http.StatusNotFound, provider.ClassNotFound, ""},
		{"forbidden", http.StatusForbidden, provider.ClassPermission, "Administration"},
		{"moved", http.StatusMovedPermanently, provider.ClassProviderError, "moved"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, _, core := renameCore(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.status == http.StatusMovedPermanently {
					w.Header().Set("Location", "https://example.invalid/moved")
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, `{"message":"SECRETTEXT"}`)
			})
			_, err := invoke(t, core, repositoriesRename.ID, "listed",
				`{"repository":"octo-org/example","name":"example-new"}`, true)
			if err == nil || strings.Contains(err.Error(), "SECRETTEXT") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q without provider text", err, tt.want)
			}
			if tt.status == http.StatusUnprocessableEntity && !isInvalidRequest(err) {
				t.Errorf("422 = %v, want an invalid request", err)
			}
			if tt.class != "" && classOf(err) != tt.class {
				t.Errorf("class = %s, want %s", classOf(err), tt.class)
			}
			if len(f.recorded()) != 1 {
				t.Errorf("requests = %d, want 1 and no redirect followed", len(f.recorded()))
			}
		})
	}
}

func TestRepositoriesRenameNeverRepeatsAnUnclearRename(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"server error": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"unreadable":   func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "x") },
		"different owner": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"full_name":"other-org/example-new"}`)
		},
		"different name": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"full_name":"octo-org/something-else"}`)
		},
		"no name": func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{}`) },
		"dropped connection": func(w http.ResponseWriter, _ *http.Request) {
			if connection, _, err := w.(http.Hijacker).Hijack(); err == nil {
				connection.Close()
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, core := renameCore(t, answer)
			_, err := invoke(t, core, repositoriesRename.ID, "listed",
				`{"repository":"octo-org/example","name":"example-new"}`, true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") {
				t.Errorf("err = %v, want an unclear outcome", err)
			}
			if patches(f) != 1 {
				t.Errorf("patches = %d, want exactly 1", patches(f))
			}
		})
	}
}

func TestRepositoriesRenameReportsATimeoutOnceAndUncertain(t *testing.T) {
	f, _, core := renameCore(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := core.Invoke(ctx, application.InvokeRequest{Operation: repositoriesRename.ID, Connection: "listed",
		Arguments: json.RawMessage(`{"repository":"octo-org/example","name":"example-new"}`), Confirmed: true})
	if classOf(err) != provider.ClassTimeout || !strings.Contains(err.Error(), "may have been applied") {
		t.Errorf("err = %v, want a timeout with an unclear outcome", err)
	}
	if patches(f) != 1 {
		t.Errorf("patches = %d, want exactly 1", patches(f))
	}
}
