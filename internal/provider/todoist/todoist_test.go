package todoist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The canaries stand for the token, for everything of a project outside the connection, and for a page
// nobody asked for. No test reaches Todoist: every request is answered by a local test server installed
// through the package transport.
const (
	tokenValue    = "canary0todoist0token0123456789abcdef0123"
	tokenEnv      = "TEST_TODOIST_TOKEN"
	ownProject    = "projOwn1"
	otherProject  = "projOwn2"
	foreignID     = "projForeign9"
	foreignCanary = "foreign-project-canary-7c1d"
	followCursor  = "unaskedPage.canary"
)

// recorded is one request the fake Todoist received.
type recorded struct {
	method, path, auth string
	query, form        url.Values
	// body, contentType, and requestID are those of a change.
	body, contentType, requestID string
}

// fakeTodoist answers the API v1 routes this provider uses. Like a server whose filtering is broader than
// the contract, it ignores every project, section, and filter parameter and answers with the resources of
// every project, including the foreign one, so only the provider's own scope check keeps them out.
type fakeTodoist struct {
	mu       sync.Mutex
	requests []recorded
	// next is the cursor a first page announces; empty means the first page is the last.
	next    string
	failure func(http.ResponseWriter, *http.Request) bool
}

func (f *fakeTodoist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	record := recorded{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"), query: r.URL.Query()}
	if r.Method == http.MethodPost {
		data, _ := io.ReadAll(r.Body)
		record.form, _ = url.ParseQuery(string(data))
		record.body = string(data)
	}
	record.contentType, record.requestID = r.Header.Get("Content-Type"), r.Header.Get("X-Request-Id")
	f.mu.Lock()
	f.requests = append(f.requests, record)
	f.mu.Unlock()
	if f.failure != nil && f.failure(w, r) {
		return
	}
	if serveChange(w, r, record) || serveStructure(w, r, record) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	next := "null"
	if f.next != "" && record.query.Get("cursor") == "" {
		next = `"` + f.next + `"`
	}
	page := func(key string, entries ...string) {
		fmt.Fprintf(w, `{"%s":[%s],"next_cursor":%s}`, key, strings.Join(entries, ","), next)
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	switch {
	case path == "/projects" || path == "/projects/search":
		page("results", projectJSON(ownProject, "Own"), projectJSON(foreignID, foreignCanary),
			projectJSON(otherProject, "Other"))
	case strings.HasPrefix(path, "/projects/"):
		id := strings.TrimPrefix(path, "/projects/")
		if id == ownProject || id == otherProject || id == foreignID {
			fmt.Fprint(w, projectJSON(id, "Project "+id))
			return
		}
		notFound(w)
	case path == "/sections" || path == "/sections/search":
		page("results", sectionJSON("sectOwn", ownProject), sectionJSON("sectForeign", foreignID))
	case path == "/sections/sectOwn":
		fmt.Fprint(w, sectionJSON("sectOwn", ownProject))
	case path == "/sections/sectForeign":
		fmt.Fprint(w, sectionJSON("sectForeign", foreignID))
	case path == "/labels" || path == "/labels/search":
		page("results", `{"id":"2156154810","name":"waiting","color":"charcoal","order":1,"is_favorite":false}`)
	case path == "/tasks" || path == "/tasks/filter":
		page("results", taskJSON("taskDue", ownProject, `{"date":"2026-09-22","string":"every monday",`+
			`"lang":"en","is_recurring":true,"timezone":null}`), taskJSON("taskOpen", ownProject, "null"),
			taskJSON("taskForeign", foreignID, `{"date":"2026-09-22","is_recurring":false}`),
			taskJSON("taskOther", otherProject, `{"date":"2026-10-30T09:00:00Z","is_recurring":false}`))
	case path == "/tasks/completed/by_completion_date":
		page("items", taskJSON("taskDone", ownProject, "null"), taskJSON("taskForeignDone", foreignID, "null"))
	case path == "/tasks/taskDue" || path == "/tasks/taskOpen":
		fmt.Fprint(w, taskJSON(strings.TrimPrefix(path, "/tasks/"), ownProject, "null"))
	case path == "/tasks/taskForeign":
		fmt.Fprint(w, taskJSON("taskForeign", foreignID, "null"))
	case path == "/comments" && record.query.Get("project_id") != "":
		page("results", `{"id":"noteProject","project_id":"`+ownProject+`","content":"Kick-off notes","is_deleted":false}`,
			`{"id":"noteStrayProject","project_id":"`+foreignID+`","content":"`+foreignCanary+`","is_deleted":false}`,
			`{"id":"noteStrayTask","item_id":"taskForeign","content":"`+foreignCanary+`","is_deleted":false}`)
	case path == "/comments":
		page("results", `{"id":"noteOne","item_id":"taskDue","content":"Looks good","posted_at":"2026-09-20T10:00:00Z",`+
			`"posted_uid":"1234567","file_attachment":{"file_name":"plan.pdf","file_type":"application/pdf",`+
			`"file_url":"https://files.example.invalid/secret-link","resource_type":"file"},"is_deleted":false}`,
			`{"id":"noteStray","item_id":"taskForeign","content":"`+foreignCanary+`","is_deleted":false}`)
	case path == "/reminders":
		page("results", `{"id":"remOne","item_id":"taskDue","notify_uid":"1","type":"relative","minute_offset":30,`+
			`"is_urgent":false,"is_deleted":false,"due":{"date":"2026-09-22T09:00:00","is_recurring":false}}`,
			`{"id":"remForeign","item_id":"taskForeign","notify_uid":"1","type":"absolute","is_urgent":true,`+
				`"is_deleted":false,"due":{"date":"2026-09-22T08:00:00","string":"`+foreignCanary+`"}}`)
	case path == "/sync" && r.Method == http.MethodPost:
		fmt.Fprint(w, `{"full_sync":true,"sync_token":"abc","filters":[`+
			`{"id":"f2","name":"Later","query":"no date","color":"grey","item_order":2,"is_deleted":false,`+
			`"is_favorite":false,"is_frozen":false},`+
			`{"id":"f9","name":"Gone","query":"p1","item_order":0,"is_deleted":true},`+
			`{"id":"f1","name":"Important","query":"priority 1","description":"urgent work","color":"red",`+
			`"item_order":1,"is_deleted":false,"is_favorite":true,"is_frozen":false}]}`)
	default:
		notFound(w)
	}
}

func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `{"error":"Not found","error_code":1,"error_tag":"NOT_FOUND","http_code":404}`)
}

func projectJSON(id, name string) string {
	return `{"id":"` + id + `","name":"` + name + `","color":"blue","parent_id":null,"is_favorite":false,` +
		`"is_shared":false,"is_archived":false,"inbox_project":false,"view_style":"list","description":"about ` +
		name + `","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`
}

func sectionJSON(id, project string) string {
	name := "Doing"
	if project == foreignID {
		name = foreignCanary
	}
	return `{"id":"` + id + `","project_id":"` + project + `","name":"` + name + `","section_order":1,` +
		`"description":null,"is_archived":false,"is_deleted":false}`
}

func taskJSON(id, project, due string) string {
	content := "Task " + id
	if project == foreignID {
		content = foreignCanary
	}
	return `{"id":"` + id + `","project_id":"` + project + `","section_id":null,"parent_id":null,` +
		`"content":"` + content + `","description":"details of ` + id + `","labels":["waiting"],"priority":4,` +
		`"due":` + due + `,"deadline":{"date":"2026-09-30","lang":"en"},"note_count":1,"checked":false,` +
		`"is_deleted":false,"added_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-02T00:00:00Z",` +
		`"completed_at":null}`
}

func (f *fakeTodoist) recorded() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

// rewrite sends every request for the official API root to the local test server instead, and refuses
// any other host.
type rewrite struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.todoist.com" {
		return nil, errors.New("unexpected host")
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host, clone.Host = r.target.Scheme, r.target.Host, ""
	return r.base.RoundTrip(clone)
}

// serve starts a local TLS server and routes the package transport to it.
func serve(t *testing.T, f *fakeTodoist) {
	t.Helper()
	server := httptest.NewTLSServer(f)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	previous := transport
	transport = rewrite{target: target, base: server.Client().Transport}
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, freeLimiter()))
}

// freeLimiter spaces nothing and never sleeps.
func freeLimiter() *ratelimit.Limiter {
	return ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })
}

func resolver(red *redact.Redactor, reads *int) *secret.Resolver {
	return secret.NewWith(func(name string) string {
		if reads != nil {
			*reads++
		}
		if name == tokenEnv {
			return tokenValue
		}
		return ""
	}, nil, nil, red)
}

func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

func coreConfig() *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"td": {Provider: Provider, BaseURL: apiRoot}},
		Credentials: map[string]config.Credential{"td-reader": credential},
		Connections: map[string]config.Connection{
			"one":     {Service: "td", Credential: "td-reader", Target: ownProject},
			"multi":   {Service: "td", Credential: "td-reader", Targets: []string{ownProject, otherProject}},
			"account": {Service: "td", Credential: "td-reader", Target: wildcard},
			"writer":  {Service: "td", Credential: "td-reader", Target: ownProject, Permissions: changePermissions},
			"multiwriter": {Service: "td", Credential: "td-reader", Targets: []string{ownProject, otherProject},
				Permissions: changePermissions},
			"accountwriter": {Service: "td", Credential: "td-reader", Target: wildcard, Permissions: changePermissions},
			"limited": {Service: "td", Credential: "td-reader", Target: ownProject, Permissions: changePermissions,
				Tools: []string{"todoist.tasks.get", "todoist.tasks.create"}},
			// The organizers list every tool, so they also offer the deletes a tools list must name.
			"organizer": {Service: "td", Credential: "td-reader", Target: wildcard, Permissions: changePermissions,
				Tools: allTools()},
			"multiorganizer": {Service: "td", Credential: "td-reader", Targets: []string{ownProject, otherProject},
				Permissions: changePermissions, Tools: allTools()},
		},
	}
}

// environment is an application core over the fake Todoist with a counting credential resolver.
type environment struct {
	fake  *fakeTodoist
	core  *application.Core
	red   *redact.Redactor
	reads *int
}

func newEnvironment(t *testing.T, f *fakeTodoist) *environment {
	t.Helper()
	serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	return &environment{fake: f, core: application.New(registry(t), coreConfig(), resolver(red, &reads), red),
		red: red, reads: &reads}
}

func (e *environment) invoke(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments)})
	return string(response.Result), err
}

func classOf(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return ""
}

func isInvalidRequest(err error) bool {
	var invalid *application.InvalidRequestError
	return errors.As(err, &invalid)
}

func TestRegisterPublishesMetadataAndTools(t *testing.T) {
	reg := registry(t)
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "Todoist" || metadata.DefaultBaseURL != "https://api.todoist.com/api/v1" ||
		len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != "token" {
		t.Fatalf("metadata = %+v", metadata)
	}
	if !reflect.DeepEqual(metadata.DefaultPermissions, []config.Permission{config.PermissionRead}) ||
		!reflect.DeepEqual(metadata.SupportedPermissions, []config.Permission{config.PermissionRead,
			config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}) {
		t.Errorf("permissions = %v / %v, want reads by default and no execute", metadata.DefaultPermissions,
			metadata.SupportedPermissions)
	}
	target := metadata.Target
	if !target.Required || !target.Multiple || target.Wildcard != "*" || target.WildcardWarning == "" {
		t.Errorf("target = %+v, want a required project list with an explicit, warned wildcard", target)
	}
	want := []string{"todoist.comments.create", "todoist.comments.delete", "todoist.comments.list",
		"todoist.comments.update", "todoist.completedtasks.list", "todoist.filters.list", "todoist.labels.create",
		"todoist.labels.delete", "todoist.labels.list", "todoist.labels.reorder", "todoist.labels.update",
		"todoist.projects.archive", "todoist.projects.create", "todoist.projects.delete", "todoist.projects.get",
		"todoist.projects.list", "todoist.projects.unarchive", "todoist.projects.update", "todoist.reminders.create",
		"todoist.reminders.delete", "todoist.reminders.get", "todoist.reminders.list", "todoist.reminders.update",
		"todoist.sections.create", "todoist.sections.delete", "todoist.sections.get", "todoist.sections.list",
		"todoist.sections.move", "todoist.sections.reorder", "todoist.sections.update", "todoist.tasks.close",
		"todoist.tasks.create", "todoist.tasks.delete", "todoist.tasks.filter", "todoist.tasks.get",
		"todoist.tasks.list", "todoist.tasks.move", "todoist.tasks.reopen", "todoist.tasks.update"}
	// Every change needs confirmation; a create and a close are not safe to repeat. Deleting a project, a
	// section, or a label takes more than itself, so only a connection whose tools list names it offers it.
	listedOnly := map[string]bool{"todoist.projects.delete": true, "todoist.sections.delete": true,
		"todoist.labels.delete": true}
	changes := map[string]capability.Risk{
		"todoist.projects.create":    changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"todoist.projects.update":    changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.projects.archive":   changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.projects.unarchive": changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.projects.delete":    changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		"todoist.sections.create":    changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"todoist.sections.update":    changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.sections.reorder":   changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.sections.move":      changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.sections.delete":    changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		"todoist.labels.create":      changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"todoist.labels.update":      changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.labels.reorder":     changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.labels.delete":      changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		"todoist.reminders.create":   changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"todoist.reminders.update":   changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.reminders.delete":   changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		"todoist.tasks.create":       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"todoist.tasks.update":       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.tasks.move":         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.tasks.close":        changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
		"todoist.tasks.reopen":       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.tasks.delete":       changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		"todoist.comments.create":    changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"todoist.comments.update":    changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"todoist.comments.delete":    changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	}
	var got []string
	for _, descriptor := range reg.Provider(Provider) {
		got = append(got, descriptor.ID)
		wantRisk, change := changes[descriptor.ID]
		if !change {
			wantRisk = readRisk
		}
		if descriptor.Risk != wantRisk || !descriptor.RequiresExplicitConnection ||
			descriptor.RequiresToolAllowList != listedOnly[descriptor.ID] {
			t.Errorf("%s risk = %+v, listed only = %t, want %+v on an explicit connection", descriptor.ID,
				descriptor.Risk, descriptor.RequiresToolAllowList, wantRisk)
		}
		if change && wantRisk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s needs no confirmation", descriptor.ID)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
	profile, ok := metadata.RecommendedProfile()
	if !ok || profile.ID != "read" || contains(profile.Tools, filtersList.ID) {
		t.Errorf("recommended profile = %+v, want the project reads without saved filters", profile)
	}
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if _, change := changes[id]; change && (profile.Recommended || strings.HasSuffix(id, ".delete") ||
				strings.HasSuffix(id, "archive")) {
				t.Errorf("profile %s ticks %s, want changes only in a chosen profile and no delete or archive",
					profile.ID, id)
			}
		}
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// A target is one or more project IDs or the wildcard alone; the configuration core refuses anything else
// without quoting it.
func TestTargetsAreValidated(t *testing.T) {
	for _, values := range [][]string{{"*"}, {ownProject}, {ownProject, otherProject}} {
		if _, err := parseScope(values); err != nil {
			t.Errorf("parseScope(%v) = %v", values, err)
		}
	}
	for _, values := range [][]string{{}, {"*", ownProject}, {ownProject, ownProject}, {"proj/1"},
		{"#Work"}, {strings.Repeat("a", 65)}} {
		if _, err := parseScope(values); err == nil {
			t.Errorf("parseScope(%v) accepted", values)
		}
	}
	document := `version: 1
services:
  td: {provider: todoist, base_url: https://api.todoist.com/api/v1}
credentials:
  td-reader: {provider: todoist, type: keyring}
connections:
  work: {service: td, credential: td-reader, targets: ["` + ownProject + `", "#Secret Project"]}
  all: {service: td, credential: td-reader, targets: ["*", "` + ownProject + `"]}
`
	_, err := config.Decode(strings.NewReader(document), registry(t))
	if err == nil || !strings.Contains(err.Error(), "connections.work.target") ||
		!strings.Contains(err.Error(), "wildcard") || strings.Contains(err.Error(), "Secret Project") {
		t.Fatalf("config validation = %v, want both connections refused without quoting a target", err)
	}
}

// A project connection answers with nothing of a project outside it, even when Todoist answers broader
// than asked: lists are filtered, and a resource of another project is refused.
func TestAProjectConnectionNeverReturnsForeignProjectData(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, call := range []struct{ operation, connection, arguments, wantID string }{
		{"todoist.projects.list", "one", `{}`, ownProject},
		{"todoist.projects.list", "multi", `{"search":"Own"}`, otherProject},
		{"todoist.projects.get", "one", `{"project_id":"` + ownProject + `"}`, ownProject},
		{"todoist.sections.list", "one", `{}`, "sectOwn"},
		{"todoist.sections.list", "multi", `{"search":"Doing"}`, "sectOwn"},
		{"todoist.sections.get", "one", `{"section_id":"sectOwn"}`, "sectOwn"},
		{"todoist.tasks.list", "one", `{}`, "taskDue"},
		{"todoist.tasks.list", "multi", `{}`, "taskOther"},
		{"todoist.tasks.filter", "multi", `{"query":"today | ##` + foreignCanary + `"}`, "taskOther"},
		{"todoist.tasks.get", "one", `{"task_id":"taskDue"}`, "taskDue"},
		{"todoist.completedtasks.list", "multi",
			`{"since":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00+02:00"}`, "taskDone"},
		{"todoist.comments.list", "one", `{"task_id":"taskDue"}`, "noteOne"},
		{"todoist.comments.list", "one", `{"project_id":"` + ownProject + `"}`, "noteProject"},
		{"todoist.reminders.list", "one", `{"task_id":"taskDue"}`, "remOne"},
	} {
		result, err := env.invoke(call.operation, call.connection, call.arguments)
		if err != nil {
			t.Errorf("%s %s = %v", call.operation, call.arguments, err)
			continue
		}
		if strings.Contains(result, foreignCanary) || strings.Contains(result, foreignID) ||
			strings.Contains(result, "sectForeign") || strings.Contains(result, "taskForeign") {
			t.Errorf("%s %s returned foreign project data: %s", call.operation, call.arguments, result)
		}
		if !strings.Contains(result, `"`+call.wantID+`"`) {
			t.Errorf("%s %s = %s, want %s", call.operation, call.arguments, result, call.wantID)
		}
	}

	// No request ever names the foreign project.
	for _, request := range env.fake.recorded() {
		if request.query.Get("project_id") == foreignID {
			t.Errorf("a request named the foreign project: %+v", request)
		}
	}

	// A resource of another project is refused, and its content never reaches the error.
	for _, call := range []struct{ operation, arguments string }{
		{"todoist.sections.get", `{"section_id":"sectForeign"}`},
		{"todoist.tasks.get", `{"task_id":"taskForeign"}`},
		{"todoist.comments.list", `{"task_id":"taskForeign"}`},
		{"todoist.reminders.list", `{"task_id":"taskForeign"}`},
	} {
		before := len(env.fake.recorded())
		_, err := env.invoke(call.operation, "one", call.arguments)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignCanary) {
			t.Errorf("%s %s = %v, want a scope refusal", call.operation, call.arguments, err)
		}
		// Only the task or the section itself was read: its comments and reminders never were.
		if requests := env.fake.recorded()[before:]; len(requests) != 1 {
			t.Errorf("%s %s sent %d requests, want only the lookup: %+v", call.operation, call.arguments,
				len(requests), requests)
		}
	}
}

// Everything a project connection cannot answer is refused before a credential is resolved or Todoist is
// contacted, and so is every malformed request.
func TestRefusalsHappenBeforeAnyIO(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, tt := range []struct{ name, operation, connection, arguments, code string }{
		{"a foreign project", "todoist.projects.get", "one", `{"project_id":"` + foreignID + `"}`, "invalid"},
		{"sections of a foreign project", "todoist.sections.list", "one", `{"project_id":"` + foreignID + `"}`, "invalid"},
		{"tasks of a foreign project", "todoist.tasks.list", "multi", `{"project_id":"` + foreignID + `"}`, "invalid"},
		{"completed tasks of a foreign project", "todoist.completedtasks.list", "one",
			`{"since":"2026-09-01T00:00:00Z","until":"2026-09-02T00:00:00Z","project_id":"` + foreignID + `"}`, "invalid"},
		{"comments of a foreign project", "todoist.comments.list", "one", `{"project_id":"` + foreignID + `"}`, "invalid"},
		{"comments of task and project", "todoist.comments.list", "account",
			`{"task_id":"taskDue","project_id":"` + ownProject + `"}`, "invalid"},
		{"comments of nothing", "todoist.comments.list", "account", `{}`, "invalid"},
		{"reminders without a task", "todoist.reminders.list", "one", `{}`, "invalid"},
		{"saved filters on a project connection", "todoist.filters.list", "multi", `{}`, "unsupported"},
		{"a scope argument", "todoist.tasks.list", "one", `{"projects":["` + foreignID + `"]}`, "invalid"},
		{"a filter expression on the structured list", "todoist.tasks.list", "one", `{"query":"today"}`, "invalid"},
		{"a filter on completed tasks", "todoist.completedtasks.list", "one",
			`{"since":"2026-09-01T00:00:00Z","until":"2026-09-02T00:00:00Z","filter_query":"today"}`, "invalid"},
		{"a limit beyond the bound", "todoist.tasks.list", "one", `{"limit":201}`, "invalid"},
		{"a malformed date", "todoist.tasks.list", "one", `{"due_from":"2026-13-40"}`, "invalid"},
		{"an inverted due window", "todoist.tasks.list", "one", `{"due_from":"2026-10-01","due_to":"2026-09-01"}`, "invalid"},
		{"without_due with a bound", "todoist.tasks.list", "one", `{"without_due":true,"due_to":"2026-09-01"}`, "invalid"},
		{"an inverted window", "todoist.completedtasks.list", "one",
			`{"since":"2026-09-02T00:00:00Z","until":"2026-09-01T00:00:00Z"}`, "invalid"},
		{"a control character", "todoist.tasks.filter", "account", `{"query":"today\n"}`, "invalid"},
		{"a foreign cursor", "todoist.tasks.list", "one", `{"cursor":"AAAAAAAAAAAAAAAAMmFiYy5kZWY"}`, "invalid"},
		{"no explicit connection", "todoist.tasks.list", "", `{}`, "selection"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			*env.reads = 0
			before := len(env.fake.recorded())
			_, err := env.invoke(tt.operation, tt.connection, tt.arguments)
			var (
				unsupported *capability.UnsupportedError
				selection   *application.ConnectionSelectionError
			)
			switch tt.code {
			case "invalid":
				if !isInvalidRequest(err) {
					t.Errorf("err = %v, want an invalid request", err)
				}
			case "unsupported":
				if !errors.As(err, &unsupported) {
					t.Errorf("err = %v, want an unsupported capability", err)
				}
			case "selection":
				if !errors.As(err, &selection) {
					t.Errorf("err = %v, want an explicit connection to be required", err)
				}
			}
			if *env.reads != 0 || len(env.fake.recorded()) != before {
				t.Errorf("secret reads = %d, requests = %d; want none", *env.reads, len(env.fake.recorded())-before)
			}
		})
	}
}

// Every list reads exactly one page. A page Todoist continues always says so, even when the scope left
// nothing of it; its cursor is opaque, bound to the request, and continues with the first batch size.
func TestListsReadOnePageAndCursorsStayOpaque(t *testing.T) {
	f := &fakeTodoist{next: followCursor}
	env := newEnvironment(t, f)

	result, err := env.invoke("todoist.tasks.list", "one", `{"label":"waiting","limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Tasks      []Task `json:"tasks"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(result), &listed); err != nil || !listed.HasMore || listed.NextCursor == "" ||
		strings.Contains(listed.NextCursor, ".") || strings.Contains(result, followCursor) {
		t.Fatalf("first page = %s, %v; want has_more with an opaque cursor", result, err)
	}
	requests := f.recorded()
	if len(requests) != 1 || requests[0].query.Get("cursor") != "" || requests[0].query.Get("limit") != "2" ||
		requests[0].query.Get("project_id") != ownProject || requests[0].query.Get("label") != "waiting" {
		t.Fatalf("requests = %+v, want exactly one first page narrowed to the project", requests)
	}
	if requests[0].auth != "Bearer "+tokenValue {
		t.Errorf("authorization = %q, want the bearer token", requests[0].auth)
	}

	// The continuation passes the Todoist cursor on unchanged and keeps the first batch size.
	result, err = env.invoke("todoist.tasks.list", "one",
		`{"label":"waiting","limit":50,"cursor":"`+listed.NextCursor+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	requests = f.recorded()
	if len(requests) != 2 || requests[1].query.Get("cursor") != followCursor || requests[1].query.Get("limit") != "2" {
		t.Fatalf("continuation = %+v, want the Todoist cursor with the first batch size", requests[1:])
	}
	if !strings.Contains(result, `"has_more":false`) || strings.Contains(result, "next_cursor") {
		t.Errorf("last page = %s, want has_more false without a cursor", result)
	}

	// A cursor belongs to its arguments, its tool, and its connection.
	for _, call := range []struct{ operation, connection, arguments string }{
		{"todoist.tasks.list", "one", `{"label":"other","cursor":"` + listed.NextCursor + `"}`},
		{"todoist.tasks.filter", "one", `{"query":"today","cursor":"` + listed.NextCursor + `"}`},
		{"todoist.tasks.list", "multi", `{"label":"waiting","cursor":"` + listed.NextCursor + `"}`},
	} {
		if _, err := env.invoke(call.operation, call.connection, call.arguments); !isInvalidRequest(err) ||
			err.Error() != "cursor is not a next_cursor of this list; start the list again without cursor" {
			t.Errorf("%s on %s with a foreign cursor = %v, want an invalid request", call.operation, call.connection, err)
		}
	}
	if len(f.recorded()) != 2 {
		t.Errorf("a refused cursor reached Todoist: %+v", f.recorded()[2:])
	}

	// A page the scope emptied still announces the next one.
	result, err = env.invoke("todoist.tasks.list", "one", `{"due_from":"2030-01-01"}`)
	if err != nil || !strings.Contains(result, `"tasks":[]`) || !strings.Contains(result, `"has_more":true`) {
		t.Errorf("filtered page = %s, %v; want an empty page that continues", result, err)
	}

	// Every paginated list follows the same contract.
	for _, call := range []struct{ operation, connection, arguments string }{
		{"todoist.projects.list", "account", `{}`},
		{"todoist.sections.list", "account", `{}`},
		{"todoist.labels.list", "one", `{"search":"wait"}`},
		{"todoist.tasks.filter", "account", `{"query":"today"}`},
		{"todoist.completedtasks.list", "account", `{"since":"2026-09-01T00:00:00Z","until":"2026-09-30T00:00:00Z"}`},
		{"todoist.comments.list", "account", `{"project_id":"` + ownProject + `"}`},
		{"todoist.reminders.list", "account", `{}`},
	} {
		before := len(f.recorded())
		result, err := env.invoke(call.operation, call.connection, call.arguments)
		if err != nil || !strings.Contains(result, `"has_more":true`) || !strings.Contains(result, `"next_cursor":"`) {
			t.Errorf("%s = %s, %v; want a continued page", call.operation, result, err)
		}
		if requests := f.recorded()[before:]; len(requests) != 1 || requests[0].query.Get("cursor") != "" {
			t.Errorf("%s sent %+v, want exactly one first page", call.operation, requests)
		}
	}
}

// The structured filters reach Todoist as parameters, and the due window is applied to the date part.
func TestStructuredTaskFilters(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	cases := []struct {
		arguments string
		want      []string
	}{
		{`{}`, []string{"taskDue", "taskOpen", "taskOther"}},
		{`{"due_from":"2026-09-22","due_to":"2026-09-22"}`, []string{"taskDue"}},
		{`{"due_from":"2026-10-01"}`, []string{"taskOther"}},
		{`{"without_due":true}`, []string{"taskOpen"}},
		{`{"project_id":"` + otherProject + `","section_id":"sectOwn","parent_id":"taskDue"}`,
			[]string{"taskDue", "taskOpen", "taskOther"}},
	}
	for _, tt := range cases {
		result, err := env.invoke("todoist.tasks.list", "multi", tt.arguments)
		if err != nil {
			t.Fatalf("%s = %v", tt.arguments, err)
		}
		var listed TaskList
		_ = json.Unmarshal([]byte(result), &listed)
		var got []string
		for _, task := range listed.Tasks {
			got = append(got, task.ID)
			if task.Description != "" {
				t.Errorf("a list task carries its description: %+v", task)
			}
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s = %v, want %v", tt.arguments, got, tt.want)
		}
	}
	last := env.fake.recorded()[len(env.fake.recorded())-1]
	if last.query.Get("project_id") != otherProject || last.query.Get("section_id") != "sectOwn" ||
		last.query.Get("parent_id") != "taskDue" {
		t.Errorf("query = %v, want the structured filters as parameters", last.query)
	}

	result, err := env.invoke("todoist.tasks.get", "one", `{"task_id":"taskDue"}`)
	if err != nil || !strings.Contains(result, `"description":"details of taskDue"`) ||
		!strings.Contains(result, `"deadline":"2026-09-30"`) || !strings.Contains(result, `"priority":4`) {
		t.Errorf("task = %s, %v", result, err)
	}
	result, err = env.invoke("todoist.comments.list", "one", `{"task_id":"taskDue"}`)
	if err != nil || strings.Contains(result, "secret-link") || !strings.Contains(result, `"file_name":"plan.pdf"`) {
		t.Errorf("comments = %s, %v; want attachments described without their address", result, err)
	}
}

// The saved filters are read through one Sync request that asks for filters only and carries no command.
func TestSavedFiltersUseOneReadOnlySyncRequest(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	result, err := env.invoke("todoist.filters.list", "account", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var listed FilterList
	if err := json.Unmarshal([]byte(result), &listed); err != nil || len(listed.Filters) != 2 ||
		listed.Filters[0].ID != "f1" || listed.Filters[1].ID != "f2" || listed.Filters[0].Query != "priority 1" {
		t.Fatalf("filters = %s, %v; want the two live filters in order", result, err)
	}
	requests := env.fake.recorded()
	if len(requests) != 1 || requests[0].method != http.MethodPost || requests[0].path != "/api/v1/sync" ||
		requests[0].form.Get("sync_token") != "*" || requests[0].form.Get("resource_types") != `["filters"]` ||
		len(requests[0].form) != 2 {
		t.Errorf("requests = %+v, want one read-only sync of filters", requests)
	}
}

// Every failure keeps its own class: a plan limit is a permission failure of its own, apart from a
// rejected token, a rate limit, and a missing resource. No error carries the token or a provider body.
func TestProviderFailuresAreClassified(t *testing.T) {
	tests := []struct {
		name   string
		answer func(http.ResponseWriter)
		class  provider.Class
		detail string
	}{
		{"unauthorized", func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error_tag":"UNAUTHORIZED","error_code":477,"error":"Unauthorized `+tokenValue+`"}`)
		}, provider.ClassAuth, "rejected the token"},
		{"plan limit", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error_tag":"PREMIUM_ONLY","error_code":32,"error":"Premium only feature","http_code":403}`)
		}, provider.ClassPermission, planMessage},
		{"plan limit in the text", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"This feature is not available on your plan"}`)
		}, provider.ClassPermission, planMessage},
		{"forbidden", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error_tag":"FORBIDDEN","error":"Forbidden `+foreignCanary+`"}`)
		}, provider.ClassPermission, permissionMessage},
		{"rate limit", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error_tag":"TOO_MANY_REQUESTS","error_extra":{"retry_after":17}}`)
		}, provider.ClassRateLimited, "retry after 17 seconds"},
		{"not found", notFound, provider.ClassNotFound, notFoundMessage},
		{"stale cursor", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error_tag":"INVALID_ARGUMENT_VALUE","error_extra":{"argument":"cursor"}}`)
		}, provider.ClassProviderError, "start the list again"},
		{"redirect", func(w http.ResponseWriter) {
			w.Header().Set("Location", "https://elsewhere.example.invalid/")
			w.WriteHeader(http.StatusFound)
		}, provider.ClassProviderError, "redirect"},
		{"gateway timeout", func(w http.ResponseWriter) { w.WriteHeader(http.StatusGatewayTimeout) }, provider.ClassTimeout, ""},
		{"server error", func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }, provider.ClassProviderError, "HTTP 502"},
		{"invalid json", func(w http.ResponseWriter) { fmt.Fprint(w, `{"results":`) }, provider.ClassInvalidResponse, ""},
		{"oversized", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"results":[],"x":"`+strings.Repeat("x", maxResponseBytes)+`"}`)
		}, provider.ClassInvalidResponse, "size limit"},
		{"an unusable cursor", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"results":[],"next_cursor":"a b"}`)
		}, provider.ClassInvalidResponse, "cursor"},
		{"an entry without identifier", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"results":[{"content":"x"}],"next_cursor":null}`)
		}, provider.ClassInvalidResponse, "identifier"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnvironment(t, &fakeTodoist{failure: func(w http.ResponseWriter, _ *http.Request) bool {
				tt.answer(w)
				return true
			}})
			_, err := env.invoke("todoist.reminders.list", "account", `{}`)
			if classOf(err) != tt.class || !strings.Contains(err.Error(), tt.detail) {
				t.Fatalf("err = %v (class %q), want class %q with %q", err, classOf(err), tt.class, tt.detail)
			}
			if tt.class == provider.ClassPermission && !strings.Contains(err.Error(), "; check ") {
				t.Errorf("a refusal names no next step: %v", err)
			}
			message := env.red.Error(err) + err.Error()
			for _, leak := range []string{tokenValue, foreignCanary, "api.todoist.com", "PREMIUM_ONLY"} {
				if strings.Contains(message, leak) {
					t.Errorf("the error carries %q: %v", leak, err)
				}
			}
		})
	}
}

// A rate limit holds the next request of the same token for the time Todoist named, within a bound.
func TestARateLimitHoldsTheNextRequest(t *testing.T) {
	f := &fakeTodoist{failure: func(w http.ResponseWriter, _ *http.Request) bool {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}}
	serve(t, f)
	var waited []time.Duration
	limited := ratelimit.New(0, time.Now, func(_ context.Context, d time.Duration) error {
		waited = append(waited, d)
		return nil
	})
	red := &redact.Redactor{}
	resolved := &config.Resolved{Name: "account", Provider: Provider, BaseURL: apiRoot, Target: wildcard,
		Credential: "td-reader", Secrets: config.Credential{Type: config.CredentialTypeEnv,
			Values: map[string]string{roleToken: tokenEnv}}}
	c, err := open(context.Background(), resolved, resolver(red, nil), red, limited)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := c.getTask(context.Background(), "taskDue"); classOf(err) != provider.ClassRateLimited {
			t.Fatalf("err = %v, want a rate limit", err)
		}
	}
	if len(waited) == 0 || waited[0] < 3*time.Second || waited[0] > 5*time.Second {
		t.Errorf("waits = %v, want the named wait to be honoured", waited)
	}
	if capHold(2*maxHold) != maxHold {
		t.Error("the hold is not bounded")
	}
}

func TestTestConnectionReadsOnlyTheScope(t *testing.T) {
	f := &fakeTodoist{}
	serve(t, f)
	red := &redact.Redactor{}
	resolved := func(base string, targets ...string) *config.Resolved {
		return &config.Resolved{Name: "td", Provider: Provider, BaseURL: base, Targets: targets, Credential: "td-reader",
			Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}}
	}
	class, err := TestConnection(context.Background(), resolved(apiRoot, ownProject, otherProject), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("project test = %q, %v", class, err)
	}
	class, err = TestConnection(context.Background(), resolved(apiRoot+"/", wildcard), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("account test = %q, %v", class, err)
	}
	requests := f.recorded()
	if len(requests) != 3 || requests[0].path != "/api/v1/projects/"+ownProject ||
		requests[1].path != "/api/v1/projects/"+otherProject || requests[2].path != "/api/v1/projects" ||
		requests[2].query.Get("limit") != "1" {
		t.Errorf("requests = %+v, want one read per project and one bounded account read", requests)
	}
	class, _ = TestConnection(context.Background(), resolved(apiRoot, "projMissing"), resolver(red, nil), red)
	if class != provider.ClassNotFound {
		t.Errorf("missing project test = %q, want not-found", class)
	}
	for _, base := range []string{"https://todoist.example.invalid/api/v1", "http://api.todoist.com/api/v1",
		"https://api.todoist.com/rest/v2"} {
		before := len(f.recorded())
		class, _ = TestConnection(context.Background(), resolved(base, ownProject), resolver(red, nil), red)
		if class != provider.ClassProviderError || len(f.recorded()) != before {
			t.Errorf("base %s test = %q, want a refusal before any request", base, class)
		}
	}
	f.failure = func(w http.ResponseWriter, _ *http.Request) bool { w.WriteHeader(http.StatusUnauthorized); return true }
	class, _ = TestConnection(context.Background(), resolved(apiRoot, ownProject), resolver(red, nil), red)
	if class != provider.ClassAuth {
		t.Errorf("rejected token test = %q, want auth", class)
	}
}

// A resolved token never reaches a result, whatever Todoist answers.
func TestTheTokenNeverReachesTheOutput(t *testing.T) {
	f := &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/tasks/taskDue" {
			return false
		}
		fmt.Fprint(w, `{"id":"taskDue","project_id":"`+ownProject+`","content":"Bearer `+tokenValue+`","labels":[],`+
			`"priority":1,"description":"<script>alert(1)</script> [x](javascript:alert(1))"}`)
		return true
	}}
	env := newEnvironment(t, f)
	result, err := env.invoke("todoist.tasks.get", "one", `{"task_id":"taskDue"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, tokenValue) || !strings.Contains(result, redact.Marker) {
		t.Errorf("result = %s, want the token redacted", result)
	}
	// Markup is data: it is passed on as a JSON string, never rendered or interpreted.
	var task Task
	if err := json.Unmarshal([]byte(result), &task); err != nil ||
		task.Description != "<script>alert(1)</script> [x](javascript:alert(1))" {
		t.Errorf("description = %q, %v; want the markup unchanged as data", task.Description, err)
	}
}

// A reminder names its task as task_id or item_id. Either form is read; a reminder of another task is
// dropped, and one without a parent belongs to the task the request was narrowed to.
func TestRemindersReadEitherParentField(t *testing.T) {
	f := &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/reminders" {
			return false
		}
		fmt.Fprint(w, `{"results":[`+
			`{"id":"remTaskForm","task_id":"taskDue","type":"relative","minute_offset":30,"is_urgent":false},`+
			`{"id":"remItemForm","item_id":"taskDue","type":"absolute","is_urgent":true,`+
			`"due":{"date":"2026-09-22T09:00:00","is_recurring":false}},`+
			`{"id":"remNoParent","type":"relative","minute_offset":10,"is_urgent":false},`+
			`{"id":"remOtherTask","task_id":"taskOpen","type":"absolute","is_urgent":false,`+
			`"due":{"date":"2026-09-22T08:00:00","string":"`+foreignCanary+`"}},`+
			`{"id":"remOtherItem","item_id":"taskOpen","type":"absolute","is_urgent":false,`+
			`"due":{"date":"2026-09-22T08:00:00","string":"`+foreignCanary+`"}}],"next_cursor":null}`)
		return true
	}}
	env := newEnvironment(t, f)
	for _, connection := range []string{"one", "account"} {
		result, err := env.invoke("todoist.reminders.list", connection, `{"task_id":"taskDue"}`)
		if err != nil {
			t.Fatalf("%s: %v", connection, err)
		}
		var listed ReminderList
		if err := json.Unmarshal([]byte(result), &listed); err != nil || strings.Contains(result, foreignCanary) {
			t.Fatalf("%s: reminders = %s, %v; want no reminder of another task", connection, result, err)
		}
		var got []string
		for _, reminder := range listed.Reminders {
			got = append(got, reminder.ID)
			if reminder.TaskID != "taskDue" {
				t.Errorf("%s: reminder %s names task %q, want taskDue", connection, reminder.ID, reminder.TaskID)
			}
		}
		if want := []string{"remTaskForm", "remItemForm", "remNoParent"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: reminders = %v, want %v", connection, got, want)
		}
	}

	// Without a task the account connection keeps every reminder with the parent it names.
	result, err := env.invoke("todoist.reminders.list", "account", `{}`)
	var listed ReminderList
	if err != nil || json.Unmarshal([]byte(result), &listed) != nil || len(listed.Reminders) != 5 ||
		listed.Reminders[3].TaskID != "taskOpen" || listed.Reminders[4].TaskID != "taskOpen" ||
		listed.Reminders[2].TaskID != "" {
		t.Errorf("account reminders = %s, %v; want every reminder with the task it names", result, err)
	}
}
