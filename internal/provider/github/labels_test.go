package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeLabelEntry is one label of the fake repository.
type fakeLabelEntry struct {
	name, color, description string
	isDefault                bool
}

// fakeLabels answers the label routes of the bound repository through the failure hook of fakeGitHub, and
// keeps the labels it holds by name, in insertion order for stable paging.
type fakeLabels struct {
	mu     sync.Mutex
	f      *fakeGitHub
	labels map[string]fakeLabelEntry
	order  []string
}

func serveLabels(t *testing.T) (*fakeGitHub, *fakeLabels, string) {
	t.Helper()
	l := &fakeLabels{labels: map[string]fakeLabelEntry{
		"bug":         {name: "bug", color: "d73a4a", description: "Something isn't working", isDefault: true},
		"help wanted": {name: "help wanted", color: "008672", description: "Extra attention is needed"},
	}, order: []string{"bug", "help wanted"}}
	f := &fakeGitHub{}
	l.f = f
	f.failure = l.route
	return f, l, serve(t, f)
}

func labelJSON(e fakeLabelEntry) string {
	description := "null"
	if e.description != "" {
		description = strconv.Quote(e.description)
	}
	return fmt.Sprintf(`{"id":1,"node_id":"MDU6TGFiZWwx","name":%q,"color":%q,"description":%s,"default":%t}`,
		e.name, e.color, description, e.isDefault)
}

func (l *fakeLabels) route(w http.ResponseWriter, req *http.Request) bool {
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok || rest != "labels" && !strings.HasPrefix(rest, "labels/") {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	if requests := l.f.recorded(); req.Method != http.MethodGet && len(requests) > 0 {
		body = requests[len(requests)-1].body
	}
	name := ""
	if tail, ok := strings.CutPrefix(rest, "labels/"); ok {
		var err error
		name, err = url.PathUnescape(tail)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
	}
	switch {
	case req.Method == http.MethodGet && rest == "labels":
		perPage, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(req.URL.Query().Get("page"))
		start := min((page-1)*perPage, len(l.order))
		end := min(start+perPage, len(l.order))
		entries := make([]string, 0, end-start)
		for _, n := range l.order[start:end] {
			entries = append(entries, labelJSON(l.labels[n]))
		}
		if end < len(l.order) {
			w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
		}
		fmt.Fprintf(w, `[%s]`, strings.Join(entries, ","))
	case req.Method == http.MethodPost && rest == "labels":
		createdName, _ := body["name"].(string)
		if _, exists := l.labels[createdName]; exists {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"Validation Failed","errors":[{"resource":"Label","code":"already_exists","field":"name"}]}`)
			return true
		}
		entry := fakeLabelEntry{name: createdName}
		entry.color, _ = body["color"].(string)
		entry.description, _ = body["description"].(string)
		l.labels[createdName] = entry
		l.order = append(l.order, createdName)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, labelJSON(entry))
	case req.Method == http.MethodGet:
		entry, exists := l.labels[name]
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return true
		}
		fmt.Fprint(w, labelJSON(entry))
	case req.Method == http.MethodPatch:
		entry, exists := l.labels[name]
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return true
		}
		delete(l.labels, name)
		final := name
		if newName, ok := body["new_name"].(string); ok && newName != "" {
			final = newName
			for i, n := range l.order {
				if n == name {
					l.order[i] = final
				}
			}
		}
		entry.name = final
		if color, ok := body["color"].(string); ok {
			entry.color = color
		}
		if description, ok := body["description"]; ok {
			entry.description, _ = description.(string)
		}
		l.labels[final] = entry
		fmt.Fprint(w, labelJSON(entry))
	case req.Method == http.MethodDelete:
		if _, exists := l.labels[name]; !exists {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return true
		}
		delete(l.labels, name)
		for i, n := range l.order {
			if n == name {
				l.order = append(l.order[:i], l.order[i+1:]...)
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

// labelsConfig binds a connection with full permissions and no tools list, and one that lists
// github.labels.delete, the way rulesetsConfig binds its own guarded tools.
func labelsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate,
		config.PermissionDelete}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all}
	cfg.Connections["repo-listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all, Tools: []string{labelsDelete.ID}}
	return cfg
}

// Every label tool satisfies its output contract through the application core once confirmed, and
// github.labels.delete, listed only, is sent exactly once.
func TestLabelsToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, _, base := serveLabels(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), labelsConfig(base), resolver(red, nil), red)

	listed, err := invoke(t, core, labelsList.ID, "repo", `{}`, false)
	if err != nil || !strings.Contains(string(listed), `"name":"bug"`) ||
		!strings.Contains(string(listed), `"name":"help wanted"`) ||
		!strings.Contains(string(listed), `"repository":"octo-org/example"`) {
		t.Fatalf("list = %s, %v", listed, err)
	}

	got, err := invoke(t, core, labelsGet.ID, "repo", `{"name":"bug"}`, false)
	if err != nil || !strings.Contains(string(got), `"color":"d73a4a"`) || !strings.Contains(string(got), `"default":true`) {
		t.Fatalf("get = %s, %v", got, err)
	}

	// A name with a space is escaped as one opaque path segment and still matched exactly.
	spaced, err := invoke(t, core, labelsGet.ID, "repo", `{"name":"help wanted"}`, false)
	if err != nil || !strings.Contains(string(spaced), `"color":"008672"`) {
		t.Fatalf("get with a space in the name = %s, %v", spaced, err)
	}

	created, err := invoke(t, core, labelsCreate.ID, "repo",
		`{"name":"needs docs","color":"0e8a16","description":"Documentation is missing"}`, true)
	if err != nil || !strings.Contains(string(created), `"name":"needs docs"`) ||
		!strings.Contains(string(created), `"color":"0e8a16"`) {
		t.Fatalf("create = %s, %v", created, err)
	}

	// A repeated create of the same name is refused with a clear message, without a second attempt.
	before := len(f.recorded())
	if _, err := invoke(t, core, labelsCreate.ID, "repo", `{"name":"bug","color":"ffffff"}`, true); !isInvalidRequest(err) ||
		!strings.Contains(err.Error(), "already holds a label named bug") {
		t.Errorf("a repeated create = %v, want an invalid request naming it", err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 {
		t.Errorf("requests = %+v, want one refused create, no retry", requests)
	}

	updated, err := invoke(t, core, labelsUpdate.ID, "repo",
		`{"name":"needs docs","new_name":"needs-docs","color":"c5def5"}`, true)
	if err != nil || !strings.Contains(string(updated), `"name":"needs-docs"`) ||
		!strings.Contains(string(updated), `"color":"c5def5"`) {
		t.Fatalf("update = %s, %v", updated, err)
	}

	// delete is offered only where a connection lists it.
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, labelsDelete.ID, "repo", `{"name":"needs-docs"}`, true); !errors.As(err, &unsupported) {
		t.Errorf("delete without a tools list = %v, want unsupported-capability", err)
	}
	before = len(f.recorded())
	unconfirmed := &application.ConfirmationRequiredError{}
	if _, err := invoke(t, core, labelsDelete.ID, "repo-listed", `{"name":"needs-docs"}`, false); !errors.As(err, &unconfirmed) {
		t.Errorf("delete without --confirm = %v, want confirmation-required", err)
	}
	if len(f.recorded()) != before {
		t.Error("delete without --confirm reached GitHub")
	}
	deleted, err := invoke(t, core, labelsDelete.ID, "repo-listed", `{"name":"needs-docs"}`, true)
	if err != nil || !strings.Contains(string(deleted), `"deleted":true`) || !strings.Contains(string(deleted), `"name":"needs-docs"`) {
		t.Fatalf("delete = %s, %v", deleted, err)
	}
	if _, err := invoke(t, core, labelsGet.ID, "repo", `{"name":"needs-docs"}`, false); classOf(err) != provider.ClassNotFound {
		t.Errorf("get of a deleted label = %v, want not-found", err)
	}
}

// Labels are listed and paged the same way branches and tags are, by the Link response header.
func TestLabelsAreListedAndPagedByLinkHeader(t *testing.T) {
	f, l, base := serveLabels(t)
	l.labels = map[string]fakeLabelEntry{}
	l.order = nil
	for i := 1; i <= 35; i++ {
		name := fmt.Sprintf("label%02d", i)
		l.labels[name] = fakeLabelEntry{name: name, color: "ededed"}
		l.order = append(l.order, name)
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), labelsConfig(base), resolver(red, nil), red)

	first, err := invoke(t, core, labelsList.ID, "repo", `{"limit":30}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var firstPage LabelList
	if json.Unmarshal(first, &firstPage) != nil || len(firstPage.Labels) != 30 || !firstPage.HasMore {
		t.Fatalf("first batch = %s", first)
	}
	if firstPage.Labels[0].Name != "label01" {
		t.Errorf("first label = %+v", firstPage.Labels[0])
	}
	second, err := invoke(t, core, labelsList.ID, "repo", `{"cursor":"`+firstPage.NextCursor+`"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var secondPage LabelList
	if json.Unmarshal(second, &secondPage) != nil || len(secondPage.Labels) != 5 || secondPage.HasMore {
		t.Fatalf("second batch = %s", second)
	}
	if len(f.recorded()) == 0 {
		t.Fatal("no request recorded")
	}
}

// Every check of these tools runs before a credential is resolved: the required name, the color pattern,
// the length bounds, and update's "at least one field" rule.
func TestLabelsArgumentsAreValidatedBeforeIO(t *testing.T) {
	f, _, base := serveLabels(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), labelsConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, id, connection, arguments string
	}{
		{"missing name", labelsGet.ID, "repo", `{}`},
		{"name with a control character", labelsGet.ID, "repo", "{\"name\":\"bad\x01name\"}"},
		{"missing color", labelsCreate.ID, "repo", `{"name":"x"}`},
		{"invalid color", labelsCreate.ID, "repo", `{"name":"x","color":"red"}`},
		{"color with a leading #", labelsCreate.ID, "repo", `{"name":"x","color":"#ffffff"}`},
		{"name too long", labelsCreate.ID, "repo", `{"name":"` + strings.Repeat("x", 51) + `","color":"ffffff"}`},
		{"description too long", labelsCreate.ID, "repo",
			`{"name":"x","color":"ffffff","description":"` + strings.Repeat("x", 101) + `"}`},
		{"no change field", labelsUpdate.ID, "repo", `{"name":"bug"}`},
		{"missing name", labelsDelete.ID, "repo-listed", `{}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s: %s(%s) = %v, want an invalid request", tt.name, tt.id, tt.arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s: %s reached the credential or GitHub", tt.name, tt.id)
		}
	}
}

// A connection whose targets name a project, not a repository, never resolves a credential for a label
// tool: the target is checked before a secret is read.
func TestLabelsRefuseANonRepositoryTargetBeforeIO(t *testing.T) {
	f, _, base := serveLabels(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), labelsConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct{ id, arguments string }{
		{labelsList.ID, `{}`}, {labelsGet.ID, `{"name":"bug"}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, "planning", tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s on a project-scoped connection = %v, want an invalid request", tt.id, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s on a project-scoped connection reached the credential or GitHub", tt.id)
		}
	}
}
