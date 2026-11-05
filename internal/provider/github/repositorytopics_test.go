package github

import (
	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const topicsRoute = repositoryRoute + "/topics"

func serveTopics(t *testing.T, status int, reply string) (*fakeGitHub, func(args string) error, *int) {
	t.Helper()
	f, core, reads := serveRepositorySettings(t, http.StatusOK, repositoryAnswer)
	inner := f.failure
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == topicsRoute {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(reply))
			return true
		}
		return inner(w, r)
	}
	return f, func(args string) error {
		_, err := invoke(t, core, repositoryTopicsSet.ID, "admin", args, true)
		return err
	}, reads
}

func TestRepositoryTopicsSetSendsOnlyNamesOnce(t *testing.T) {
	f, core, _ := serveRepositorySettings(t, http.StatusOK, repositoryAnswer)
	inner := f.failure
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == topicsRoute {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"names":["cli","go"]}`))
			return true
		}
		return inner(w, r)
	}
	result, err := invoke(t, core, repositoryTopicsSet.ID, "admin", `{"names":["go","cli"]}`, true)
	if err != nil {
		t.Fatal(err)
	}
	requests := f.recorded()
	if len(requests) != 1 || requests[0].method != http.MethodPut || requests[0].path != topicsRoute ||
		!reflect.DeepEqual(requests[0].body, map[string]any{"names": []any{"go", "cli"}}) {
		t.Fatalf("requests = %+v, want one PUT with names only", requests)
	}
	if !strings.Contains(string(result), `"topics":["cli","go"]`) || !strings.Contains(string(result), `"repository"`) {
		t.Errorf("result = %s", result)
	}
}

func TestRepositoryTopicsSetEmptyListRemovesAll(t *testing.T) {
	f, call, _ := serveTopics(t, http.StatusOK, `{"names":[]}`)
	if err := call(`{"names":[]}`); err != nil {
		t.Fatal(err)
	}
	if r := f.recorded(); len(r) != 1 || !reflect.DeepEqual(r[0].body, map[string]any{"names": []any{}}) {
		t.Errorf("requests = %+v", r)
	}
}

func TestRepositoryTopicsSetRefusesBadListsBeforeIO(t *testing.T) {
	var many []string
	for i := 0; i < 21; i++ {
		many = append(many, `"t`+string(rune('a'+i))+`"`)
	}
	f, call, reads := serveTopics(t, http.StatusOK, `{"names":[]}`)
	for name, args := range map[string]string{
		"missing":    `{}`,
		"21 topics":  `{"names":[` + strings.Join(many, ",") + `]}`,
		"uppercase":  `{"names":["Canary"]}`,
		"too long":   `{"names":["` + strings.Repeat("a", 51) + `"]}`,
		"hyphen":     `{"names":["-canary"]}`,
		"duplicate":  `{"names":["go","go"]}`,
		"empty name": `{"names":[""]}`,
	} {
		err := call(args)
		if !isInvalidRequest(err) || strings.Contains(strings.ToLower(err.Error()), "canary") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if *reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("secret reads = %d, requests = %d; want none", *reads, len(f.recorded()))
	}
}

func TestRepositoryTopicsSetMapsFailures(t *testing.T) {
	for status, want := range map[int]string{http.StatusForbidden: "Administration: write",
		http.StatusNotFound: "Administration: write", http.StatusUnprocessableEntity: "rejected"} {
		_, call, _ := serveTopics(t, status, `{"message":"provider-text-canary"}`)
		err := call(`{"names":["go"]}`)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "canary") {
			t.Errorf("status %d: err = %v", status, err)
		}
		if status == http.StatusUnprocessableEntity && !isInvalidRequest(err) {
			t.Errorf("422 = %v, want invalid-request", err)
		}
	}
}

func TestRepositoryTopicsSetIsNeverRepeated(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		reply  string
	}{
		"server error": {http.StatusBadGateway, `{"message":"provider-text-canary"}`},
		"unreadable":   {http.StatusOK, `not json`},
		"differs":      {http.StatusOK, `{"names":["other"]}`},
		"extra":        {http.StatusOK, `{"names":["go","other"]}`},
		"repeated":     {http.StatusOK, `{"names":["go","go"]}`},
	} {
		f, call, _ := serveTopics(t, tt.status, tt.reply)
		err := call(`{"names":["go","cli"]}`)
		if err == nil || !strings.Contains(err.Error(), uncertain) || strings.Contains(err.Error(), "canary") {
			t.Errorf("%s: err = %v", name, err)
		}
		if got := len(f.recorded()); got != 1 {
			t.Errorf("%s: requests = %d, want one", name, got)
		}
	}
}

func TestRepositoryTopicsSetRefusesForeignTargetsBeforeIO(t *testing.T) {
	f, call, reads := serveTopics(t, http.StatusOK, `{"names":["go"]}`)
	if err := call(`{"repository":"other/secret-repo","names":["go"]}`); err == nil ||
		strings.Contains(err.Error(), "secret-repo") {
		t.Errorf("err = %v", err)
	}
	if *reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("secret reads = %d, requests = %d; want none", *reads, len(f.recorded()))
	}
}

func TestRepositoryTopicsSetIsReachedOnlyThroughTheToolsList(t *testing.T) {
	f, core, reads := serveRepositorySettings(t, http.StatusOK, repositoryAnswer)
	for _, connection := range []string{"reader", "planner", "observer", "listed", "operator", "open", "maintainer"} {
		if _, err := invoke(t, core, repositoryTopicsSet.ID, connection, `{"names":["go"]}`, true); err == nil {
			t.Errorf("ran on %s", connection)
		}
	}
	if *reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("secret reads = %d, requests = %d; want none", *reads, len(f.recorded()))
	}
}

func TestRepositoryTopicsSetRefusesAnExcludedRepositoryBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	cfg := coreConfig(base)
	cfg.Connections["excluded"] = config.Connection{Service: "gh", Credential: "gh-reader",
		Targets: []string{"repos/octo-org/*"}, Exclude: []string{"repos/octo-org/example"},
		Effects: config.Effects(), Tools: []string{repositoryTopicsSet.ID}}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red, &reads), red)
	_, err := invoke(t, core, repositoryTopicsSet.ID, "excluded", `{"repository":"octo-org/example","names":["go"]}`, true)
	if err == nil {
		t.Fatal("excluded repository accepted")
	}
	if reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("secret reads = %d, requests = %d; want none", reads, len(f.recorded()))
	}
}
