package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// The canaries stand for a token and for provider content. No test reaches GitHub: every request is
// answered by a local test server installed through the package transport.
const (
	tokenValue    = "ghp_canaryTokenForQatlasTests0123456789"
	tokenEnv      = "TEST_GITHUB_TOKEN"
	projectTarget = "orgs/octo-org/projects/7"
	userTarget    = "users/octocat/projects/3"
	repoTarget    = "repos/octo-org/example"
	projectID     = "PVT_boundProject"
	foreignID     = "PVT_foreignProject"
	bodyCanary    = "issue-body-canary-5e1f"
)

// recorded is one request the fake GitHub received.
type recorded struct {
	method, path, document, auth, version, accept string
	variables                                     map[string]any
}

type fakeItem struct {
	id, kind, title, repo, status, body string
	number                              int
	assignees, labels                   []string
}

type fakeIssue struct {
	number int
	state  string
	labels []string
}

// fakeGitHub answers the GraphQL and REST routes this provider uses. Items are filtered with a small
// reading of the project filter grammar, unless ignoreQuery makes it answer every item like a server whose
// filter reading is broader than the contract.
type fakeGitHub struct {
	mu          sync.Mutex
	requests    []recorded
	items       []fakeItem
	foreign     []fakeItem
	issues      []fakeIssue
	noStatus    bool
	ignoreQuery bool
	failure     func(http.ResponseWriter, *http.Request) bool
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	record := recorded{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"),
		version: r.Header.Get("X-GitHub-Api-Version"), accept: r.Header.Get("Accept")}
	var payload struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if r.Method == http.MethodPost {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &payload)
		record.document, record.variables = payload.Query, payload.Variables
	}
	f.mu.Lock()
	f.requests = append(f.requests, record)
	f.mu.Unlock()

	if f.failure != nil && f.failure(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/graphql":
		f.graphql(w, payload.Query, payload.Variables)
	case r.URL.Path == "/api/v3/repos/octo-org/example":
		fmt.Fprint(w, `{"full_name":"octo-org/example"}`)
	case r.URL.Path == "/api/v3/repos/octo-org/example/issues/42":
		fmt.Fprint(w, `{"number":42,"title":"Crash on start","state":"open","state_reason":null,`+
			`"body":"`+bodyCanary+`","user":{"login":"octocat"},"assignees":[{"login":"hubot"}],`+
			`"labels":[{"name":"bug"}],"milestone":{"title":"v1"},"html_url":"https://github.com/octo-org/example/issues/42",`+
			`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","closed_at":null,"comments":3}`)
	case r.URL.Path == "/api/v3/repos/octo-org/example/issues/7":
		fmt.Fprint(w, `{"number":7,"title":"A change","state":"open","body":"pr body",`+
			`"pull_request":{"url":"https://api.github.com/repos/octo-org/example/pulls/7"}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}
}

func (f *fakeGitHub) graphql(w http.ResponseWriter, document string, variables map[string]any) {
	switch {
	case strings.Contains(document, "items(first"):
		f.itemsPage(w, variables)
	case strings.Contains(document, "item:node"):
		f.itemDetail(w, variables)
	case strings.Contains(document, "projectV2(number"):
		if variables["owner"] != "octo-org" || variables["number"] != float64(7) ||
			!strings.Contains(document, "owner:organization(") {
			fmt.Fprint(w, `{"data":{"owner":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve"}]}`)
			return
		}
		fmt.Fprintf(w, `{"data":{"owner":{"projectV2":%s}}}`, f.projectJSON())
	case strings.Contains(document, "repository(owner"):
		f.issuesPage(w, variables)
	default:
		fmt.Fprint(w, `{"errors":[{"message":"unexpected document"}]}`)
	}
}

func (f *fakeGitHub) projectJSON() string {
	status := `{"id":"F_status","name":"Status","dataType":"SINGLE_SELECT",` +
		`"options":[{"name":"Todo"},{"name":"In progress"},{"name":"Done"}]},`
	if f.noStatus {
		status = ""
	}
	return `{"id":"` + projectID + `","fields":{"nodes":[{"id":"F_title","name":"Title","dataType":"TITLE"},` + status +
		`{"id":"F_prio","name":"Priority","dataType":"SINGLE_SELECT","options":[{"name":"P1"}]},` +
		`{"id":"F_est","name":"Estimate","dataType":"NUMBER"},{"id":"F_due","name":"Due","dataType":"DATE"},` +
		`{"id":"F_sprint","name":"Sprint","dataType":"ITERATION"},{"id":"F_note","name":"Note","dataType":"TEXT"},` +
		`{"id":"F_assignees","name":"Assignees","dataType":"ASSIGNEES"}]}}`
}

func itemNodeJSON(item fakeItem, project string, withBody bool) string {
	values := []string{`{"text":"` + item.title + `","field":{"id":"F_title"}}`,
		`{"name":"P1","field":{"id":"F_prio"}}`, `{"number":3,"field":{"id":"F_est"}}`,
		`{"date":"2026-10-01","field":{"id":"F_due"}}`, `{"title":"Sprint 4","field":{"id":"F_sprint"}}`,
		`{"text":"short note","field":{"id":"F_note"}}`, `{}`}
	if item.status != "" {
		values = append(values, `{"name":"`+item.status+`","field":{"id":"F_status"}}`)
	}
	logins := make([]string, len(item.assignees))
	for i, login := range item.assignees {
		logins[i] = `{"login":"` + login + `"}`
	}
	labels := make([]string, len(item.labels))
	for i, label := range item.labels {
		labels[i] = `{"name":"` + label + `"}`
	}
	people := fmt.Sprintf(`"assignees":{"totalCount":%d,"nodes":[%s]}`, len(logins), strings.Join(logins, ","))
	content := `{"__typename":"DraftIssue","title":"` + item.title + `",` + people
	switch item.kind {
	case "ISSUE", "PULL_REQUEST":
		state := `"issueState":"OPEN"`
		typename := "Issue"
		if item.kind == "PULL_REQUEST" {
			state, typename = `"pullRequestState":"MERGED"`, "PullRequest"
		}
		content = fmt.Sprintf(`{"__typename":"%s","number":%d,"title":"%s",%s,"url":"https://github.com/%s/issues/%d",`+
			`"repository":{"nameWithOwner":"%s"},%s,"labels":{"totalCount":%d,"nodes":[%s]}`,
			typename, item.number, item.title, state, item.repo, item.number, item.repo, people, len(labels),
			strings.Join(labels, ","))
	}
	if withBody && item.kind != "PULL_REQUEST" {
		content += `,"body":"` + item.body + `"`
	}
	content += "}"
	node := fmt.Sprintf(`{"id":"%s","type":"%s","fieldValues":{"totalCount":%d,"nodes":[%s]},"content":%s`,
		item.id, item.kind, len(values), strings.Join(values, ","), content)
	if project != "" {
		node += `,"project":{"id":"` + project + `"}`
	}
	return node + "}"
}

func (f *fakeGitHub) itemsPage(w http.ResponseWriter, variables map[string]any) {
	if variables["project"] != projectID {
		fmt.Fprint(w, `{"data":{"project":null},"errors":[{"type":"NOT_FOUND","message":"no node"}]}`)
		return
	}
	query, _ := variables["query"].(string)
	matching := []int{}
	for i, item := range f.items {
		if f.ignoreQuery || fakeMatches(item, query) {
			matching = append(matching, i)
		}
	}
	start := 0
	if after, ok := variables["after"].(string); ok {
		index, _ := strconv.Atoi(strings.TrimPrefix(after, "cur-"))
		for start < len(matching) && matching[start] <= index {
			start++
		}
	}
	first := int(variables["first"].(float64))
	end := start + first
	if end > len(matching) {
		end = len(matching)
	}
	edges := []string{}
	endCursor := ""
	for _, index := range matching[start:end] {
		endCursor = "cur-" + strconv.Itoa(index)
		edges = append(edges, `{"cursor":"`+endCursor+`","node":`+itemNodeJSON(f.items[index], "", false)+`}`)
	}
	fmt.Fprintf(w, `{"data":{"project":{"items":{"pageInfo":{"hasNextPage":%t,"endCursor":%q},"edges":[%s]}}}}`,
		end < len(matching), endCursor, strings.Join(edges, ","))
}

// fakeMatches reads the filter terms this provider emits.
func fakeMatches(item fakeItem, query string) bool {
	for _, term := range splitTerms(query) {
		negated := strings.HasPrefix(term, "-")
		key, value, _ := strings.Cut(strings.TrimPrefix(term, "-"), ":")
		values := strings.Split(value, ",")
		for i := range values {
			values[i] = strings.Trim(values[i], `"`)
		}
		var hit bool
		switch key {
		case "status":
			hit = containsFold(values, item.status)
		case "is":
			hit = map[string]string{"issue": "ISSUE", "pr": "PULL_REQUEST", "draft": "DRAFT_ISSUE"}[value] == item.kind
		case "repo":
			hit = strings.EqualFold(value, item.repo)
		case "assignee":
			hit = containsFold(item.assignees, value)
		case "label":
			hit = anyFold(item.labels, values)
		}
		if hit == negated {
			return false
		}
	}
	return true
}

func splitTerms(query string) []string {
	var terms []string
	var current strings.Builder
	quoted := false
	for _, r := range query {
		switch {
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case r == ' ' && !quoted:
			if current.Len() > 0 {
				terms = append(terms, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		terms = append(terms, current.String())
	}
	return terms
}

func (f *fakeGitHub) itemDetail(w http.ResponseWriter, variables map[string]any) {
	id, _ := variables["item"].(string)
	item := "null"
	for _, candidate := range f.items {
		if candidate.id == id {
			item = itemNodeJSON(candidate, projectID, true)
		}
	}
	for _, candidate := range f.foreign {
		if candidate.id == id {
			item = itemNodeJSON(candidate, foreignID, true)
		}
	}
	if item == "null" {
		fmt.Fprintf(w, `{"data":{"owner":{"projectV2":%s},"item":null},"errors":[{"type":"NOT_FOUND",`+
			`"message":"Could not resolve to a node"}]}`, f.projectJSON())
		return
	}
	fmt.Fprintf(w, `{"data":{"owner":{"projectV2":%s},"item":%s}}`, f.projectJSON(), item)
}

func (f *fakeGitHub) issuesPage(w http.ResponseWriter, variables map[string]any) {
	if variables["owner"] != "octo-org" || variables["name"] != "example" {
		fmt.Fprint(w, `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"no repository"}]}`)
		return
	}
	states, _ := variables["states"].([]any)
	matching := []int{}
	for i, issue := range f.issues {
		if len(states) == 0 || strings.EqualFold(states[0].(string), issue.state) {
			matching = append(matching, i)
		}
	}
	start := 0
	if after, ok := variables["after"].(string); ok {
		index, _ := strconv.Atoi(strings.TrimPrefix(after, "icur-"))
		for start < len(matching) && matching[start] <= index {
			start++
		}
	}
	end := start + int(variables["first"].(float64))
	if end > len(matching) {
		end = len(matching)
	}
	nodes := []string{}
	endCursor := ""
	for _, index := range matching[start:end] {
		issue := f.issues[index]
		endCursor = "icur-" + strconv.Itoa(index)
		nodes = append(nodes, fmt.Sprintf(`{"number":%d,"title":"Issue %d","state":"%s","url":"https://github.com/`+
			`octo-org/example/issues/%d","updatedAt":"2026-01-01T00:00:00Z","assignees":{"nodes":[]},`+
			`"labels":{"nodes":[{"name":"bug"}]}}`, issue.number, issue.number, strings.ToUpper(issue.state), issue.number))
	}
	fmt.Fprintf(w, `{"data":{"repository":{"issues":{"pageInfo":{"hasNextPage":%t,"endCursor":%q},"nodes":[%s]}}}}`,
		end < len(matching), endCursor, strings.Join(nodes, ","))
}

func (f *fakeGitHub) recorded() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

// serve starts a local TLS server answering like GitHub Enterprise Server and routes the package transport
// to it. It returns the fake and the REST base URL a service would configure.
func serve(t *testing.T, f *fakeGitHub) string {
	t.Helper()
	server := httptest.NewTLSServer(f)
	t.Cleanup(server.Close)
	previous := transport
	transport = server.Client().Transport
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(tokenValue, freeLimiter()))
	return server.URL + "/api/v3"
}

func freeLimiter() *ratelimit.Limiter { return ratelimit.New(0, time.Now, ratelimit.Sleep) }

func resolvedConnection(name, base, target string) *config.Resolved {
	return &config.Resolved{
		Name: name, Provider: Provider, BaseURL: base, Target: target, Service: "gh", Credential: "gh-reader",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}},
	}
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

func client(t *testing.T, base, target string) *Client {
	t.Helper()
	red := &redact.Redactor{}
	c, err := open(resolvedConnection("gh", base, target), resolver(red, nil), red, freeLimiter())
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	return c
}

// roster builds 75 project items in project order. Every fifth item is Done, every seventh has no status,
// and the content types rotate between issues, pull requests, and draft issues.
func roster() []fakeItem {
	items := make([]fakeItem, 0, 75)
	for i := 0; i < 75; i++ {
		item := fakeItem{id: fmt.Sprintf("PVTI_item%02d", i), kind: "ISSUE", title: fmt.Sprintf("Item %d", i),
			repo: "octo-org/example", number: i + 1, status: "Todo", body: bodyCanary,
			assignees: []string{"octocat"}, labels: []string{"bug"}}
		switch {
		case i%5 == 4:
			item.status = "Done"
		case i%7 == 3:
			item.status = ""
		case i%2 == 0:
			item.status = "In progress"
		}
		switch i % 3 {
		case 1:
			item.kind = "PULL_REQUEST"
		case 2:
			item.kind, item.repo, item.number = "DRAFT_ISSUE", "", 0
		}
		if i%4 == 0 {
			item.repo, item.labels, item.assignees = "octo-org/other", []string{"help wanted"}, []string{"hubot"}
			if item.kind == "DRAFT_ISSUE" {
				item.repo = ""
			}
		}
		items = append(items, item)
	}
	return items
}

// walk reads every batch of a project list and returns the item IDs in order, failing on a repeated ID.
func walk(t *testing.T, c *Client, options ItemListOptions) ([]string, int) {
	t.Helper()
	seen := map[string]bool{}
	ids := []string{}
	batches := 0
	for {
		batches++
		if batches > 50 {
			t.Fatal("the list did not end")
		}
		page, err := c.ListItems(context.Background(), options)
		if err != nil {
			t.Fatalf("ListItems() = %v", err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("item %s was listed twice", item.ID)
			}
			seen[item.ID] = true
			ids = append(ids, item.ID)
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatalf("the last batch carries a cursor: %+v", page)
			}
			return ids, batches
		}
		if page.NextCursor == "" {
			t.Fatalf("has_more without next_cursor: %+v", page)
		}
		options.Cursor = page.NextCursor
	}
}

func expected(items []fakeItem, keep func(fakeItem) bool) []string {
	ids := []string{}
	for _, item := range items {
		if keep(item) {
			ids = append(ids, item.id)
		}
	}
	return ids
}

func equalIDs(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("items = %v\nwant    %v", got, want)
	}
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

func TestRegisterPublishesMetadataAndFourReadOperations(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || metadata.Name != "GitHub" || metadata.DefaultBaseURL != "https://api.github.com" ||
		!metadata.Target.Required || metadata.Target.Multiple || metadata.Target.Validate == nil ||
		len(metadata.SecretRoles) != 1 || metadata.SecretRoles[0].Name != "token" {
		t.Fatalf("metadata = %+v, %v", metadata, ok)
	}
	operations := reg.Provider(Provider)
	ids := []string{}
	for _, descriptor := range operations {
		ids = append(ids, descriptor.ID)
		if descriptor.Risk.Effect != capability.EffectRead || descriptor.Risk.Idempotency != capability.IdempotencySafe ||
			descriptor.Risk.Confirmation != capability.ConfirmationNone || !descriptor.RequiresExplicitConnection ||
			descriptor.Risk.DataSensitivity != dataSensitivity {
			t.Errorf("descriptor %s = %+v, want a safe read requiring an explicit connection", descriptor.ID, descriptor.Risk)
		}
		for _, forbidden := range []string{"owner", "base_url", "query\"", "project_id", "comments"} {
			if strings.Contains(string(descriptor.InputSchema), forbidden) {
				t.Errorf("descriptor %s input offers %q: %s", descriptor.ID, forbidden, descriptor.InputSchema)
			}
		}
	}
	equalIDs(t, ids, []string{"github.issues.get", "github.issues.list", "github.projectitems.get",
		"github.projectitems.list"})
	if len(metadata.Tools) != 4 {
		t.Errorf("tools = %+v, want the four operations offered to connection allow-lists", metadata.Tools)
	}
}

func TestTargetFormsAreValidated(t *testing.T) {
	valid := map[string]target{
		"users/octocat/projects/3":     {kind: kindProject, scope: "users", owner: "octocat", number: 3},
		"orgs/octo-org/projects/12":    {kind: kindProject, scope: "orgs", owner: "octo-org", number: 12},
		" repos/octo-org/example.go ":  {kind: kindRepository, owner: "octo-org", repo: "example.go"},
		"repos/octo_enterprise/a-b_c1": {kind: kindRepository, owner: "octo_enterprise", repo: "a-b_c1"},
	}
	for raw, want := range valid {
		if got, err := parseTarget(raw); err != nil || got != want {
			t.Errorf("parseTarget(%q) = %+v, %v; want %+v", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"", "octo-org/example", "repos/octo-org", "repos/octo-org/example/issues", "repos/-octo/example",
		"repos/octo-org/..", "repos/octo org/example", "orgs/octo-org/projects/0", "orgs/octo-org/projects/07",
		"orgs/octo-org/projects/x", "orgs/octo-org/projects/1234567890", "teams/octo-org/projects/1",
		"orgs/octo-org/project/1", "users/octocat/projects/3/items", "https://github.com/orgs/octo-org/projects/7",
		"orgs/octo-org/projects/-1", "repos/octo-org/example?x=1",
	} {
		if _, err := parseTarget(raw); err == nil {
			t.Errorf("parseTarget(%q) accepted an unusable target", raw)
		} else if raw != "" && strings.Contains(err.Error(), raw) {
			t.Errorf("parseTarget(%q) quoted the value: %v", raw, err)
		}
	}
}

// The configuration refuses a malformed target and a target list before any call.
func TestConfigurationValidatesTheTargetForm(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := func(target string) string {
		return `version: 1
services:
  gh:
    provider: github
    base_url: https://api.github.com
credentials:
  gh-reader:
    provider: github
    type: env
    values:
      token: QATLAS_GH_TOKEN
connections:
  planning:
    service: gh
    credential: gh-reader
` + target
	}
	for _, target := range []string{"    target: orgs/octo-org/projects/7\n", "    target: users/octocat/projects/1\n",
		"    target: repos/octo-org/example\n"} {
		if _, err := config.Decode(strings.NewReader(document(target)), reg); err != nil {
			t.Errorf("target %q was refused: %v", target, err)
		}
	}
	for _, target := range []string{"", "    target: octo-org/example\n", "    target: orgs/octo-org/projects/zero\n",
		"    targets: [repos/octo-org/example, repos/octo-org/other]\n"} {
		_, err := config.Decode(strings.NewReader(document(target)), reg)
		if err == nil || !strings.Contains(err.Error(), "connections.planning") {
			t.Errorf("target %q: err = %v, want a refused connection", target, err)
		}
	}
}

func TestEndpointsFollowGitHubAndEnterpriseServer(t *testing.T) {
	tests := map[string]endpoints{
		"https://api.github.com":                   {"https://api.github.com", "https://api.github.com/graphql"},
		"https://api.github.com/":                  {"https://api.github.com", "https://api.github.com/graphql"},
		"https://api.octo.ghe.com":                 {"https://api.octo.ghe.com", "https://api.octo.ghe.com/graphql"},
		"https://ghe.example.invalid/api/v3":       {"https://ghe.example.invalid/api/v3", "https://ghe.example.invalid/api/graphql"},
		"https://ghe.example.invalid:8443/api/v3/": {"https://ghe.example.invalid:8443/api/v3", "https://ghe.example.invalid:8443/api/graphql"},
	}
	for raw, want := range tests {
		if got, err := endpointsOf(raw); err != nil || got != want {
			t.Errorf("endpointsOf(%q) = %+v, %v; want %+v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"http://api.github.com", "https://github.com", "https://ghe.example.invalid",
		"https://ghe.example.invalid/api/graphql", "https://user:pw@api.github.com", "https://api.github.com?x=1",
		"https://api.github.com#x", "https://ghe.example.invalid/other/api/v3", ""} {
		if _, err := endpointsOf(raw); err == nil {
			t.Errorf("endpointsOf(%q) accepted an unusable base URL", raw)
		}
	}
}

// A first full batch of 30 reports has_more, and following the cursors reaches every item of the open
// roster exactly once, in project order. The default status filter reaches GitHub as a server filter.
func TestDefaultRosterIsBoundedFilteredAndGapless(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	c := client(t, base, projectTarget)

	first, err := c.ListItems(context.Background(), ItemListOptions{})
	if err != nil {
		t.Fatalf("ListItems() = %v", err)
	}
	if len(first.Items) != 30 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first batch = %d items, has_more=%v, cursor=%q; want a full batch that continues",
			len(first.Items), first.HasMore, first.NextCursor)
	}
	requests := f.recorded()
	if len(requests) != 2 || !strings.Contains(requests[0].document, "projectV2(number") ||
		!strings.Contains(requests[1].document, "items(first") {
		t.Fatalf("requests = %+v, want one field resolution and one item query", requests)
	}
	if got := requests[1].variables["query"]; got != `-status:"Done"` {
		t.Errorf("query = %q, want the default roster filter", got)
	}
	if requests[1].variables["first"] != float64(31) {
		t.Errorf("first = %v, want the batch plus one look-ahead item", requests[1].variables["first"])
	}
	for _, request := range requests {
		if request.method != http.MethodPost || request.path != "/api/graphql" ||
			request.auth != "Bearer "+tokenValue || request.version != apiVersion ||
			request.accept != "application/vnd.github+json" {
			t.Errorf("request = %+v, want an authenticated, versioned GraphQL call", request)
		}
		if strings.Contains(request.document, "body") || strings.Contains(request.document, "comments") {
			t.Errorf("the list asked for bodies or comments: %s", request.document)
		}
	}
	item := first.Items[0]
	if item.ID != "PVTI_item00" || item.Type != "issue" || item.Status != "In progress" ||
		item.Repository != "octo-org/other" || item.State != "open" || item.Number != 1 ||
		item.Fields["Priority"] != "P1" || item.Fields["Estimate"] != float64(3) || item.Fields["Due"] != "2026-10-01" ||
		item.Fields["Sprint"] != "Sprint 4" || item.Fields["Note"] != "short note" || item.Body != nil {
		t.Errorf("item = %+v", item)
	}
	for _, absent := range []string{"Title", "Status", "Assignees"} {
		if _, ok := item.Fields[absent]; ok {
			t.Errorf("fields carry %q: %+v", absent, item.Fields)
		}
	}

	ids, batches := walk(t, c, ItemListOptions{})
	equalIDs(t, ids, expected(roster(), func(item fakeItem) bool { return item.status != "Done" }))
	if batches != 2 {
		t.Errorf("batches = %d, want 60 items in two batches", batches)
	}
	// The same walk is reproducible.
	again, _ := walk(t, c, ItemListOptions{})
	equalIDs(t, again, ids)
}

func TestStatusFiltersAreTranslated(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	c := client(t, base, projectTarget)

	tests := []struct {
		name    string
		options ItemListOptions
		query   string
		keep    func(fakeItem) bool
	}{
		{"positive status in canonical spelling", ItemListOptions{Status: []string{"in progress"}},
			`status:"In progress"`, func(i fakeItem) bool { return i.status == "In progress" }},
		{"several positive values", ItemListOptions{Status: []string{"Todo", "Done"}},
			`status:"Todo","Done"`, func(i fakeItem) bool { return i.status == "Todo" || i.status == "Done" }},
		{"several negative values", ItemListOptions{StatusNot: []string{"done", "Todo"}},
			`-status:"Done" -status:"Todo"`, func(i fakeItem) bool { return i.status != "Done" && i.status != "Todo" }},
		{"an explicit empty exclusion lists every status", ItemListOptions{StatusNot: []string{}}, ``,
			func(fakeItem) bool { return true }},
		{"structured filters", ItemListOptions{Type: "issue", Repository: "octo-org/other", Assignee: "hubot",
			Labels: []string{"help wanted", "bug"}},
			`-status:"Done" is:issue repo:octo-org/other assignee:hubot label:"help wanted","bug"`,
			func(i fakeItem) bool { return i.status != "Done" && i.kind == "ISSUE" && i.repo == "octo-org/other" }},
		{"draft type", ItemListOptions{Type: "draft_issue", StatusNot: []string{}}, `is:draft`,
			func(i fakeItem) bool { return i.kind == "DRAFT_ISSUE" }},
		{"pull request type", ItemListOptions{Type: "pull_request", Status: []string{"Todo"}}, `status:"Todo" is:pr`,
			func(i fakeItem) bool { return i.kind == "PULL_REQUEST" && i.status == "Todo" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(f.recorded())
			ids, _ := walk(t, c, tt.options)
			equalIDs(t, ids, expected(roster(), tt.keep))
			requests := f.recorded()[before:]
			if got := requests[1].variables["query"]; got != tt.query {
				t.Errorf("query = %q, want %q", got, tt.query)
			}
		})
	}

	for _, options := range []ItemListOptions{{Status: []string{"Blocked"}}} {
		before := len(f.recorded())
		_, err := c.ListItems(context.Background(), options)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "Todo, In progress, Done") {
			t.Errorf("ListItems(%+v) = %v, want an invalid request naming the options", options, err)
		}
		if requests := f.recorded()[before:]; len(requests) != 1 {
			t.Errorf("requests = %d, want only the field resolution", len(requests))
		}
	}
}

// A project without a Status field keeps its default roster unfiltered and refuses an explicit status filter.
func TestProjectWithoutStatusField(t *testing.T) {
	f := &fakeGitHub{items: roster(), noStatus: true}
	base := serve(t, f)
	c := client(t, base, projectTarget)

	page, err := c.ListItems(context.Background(), ItemListOptions{Limit: 5})
	if err != nil || len(page.Items) != 5 {
		t.Fatalf("ListItems() = %+v, %v", page, err)
	}
	if got := f.recorded()[1].variables["query"]; got != "" {
		t.Errorf("query = %q, want no status filter", got)
	}
	for _, options := range []ItemListOptions{{Status: []string{"Todo"}}, {StatusNot: []string{"Done"}}} {
		if _, err := c.ListItems(context.Background(), options); !isInvalidRequest(err) {
			t.Errorf("ListItems(%+v) = %v, want an invalid request", options, err)
		}
	}
}

// When GitHub answers with more than the filters select, the provider verifies each item, and paging
// through the verified batches still reaches every matching item exactly once.
func TestClientVerificationKeepsPagingGapless(t *testing.T) {
	f := &fakeGitHub{items: roster(), ignoreQuery: true}
	base := serve(t, f)
	c := client(t, base, projectTarget)

	options := ItemListOptions{Type: "issue", Labels: []string{"bug"}, Limit: 4}
	ids, _ := walk(t, c, options)
	equalIDs(t, ids, expected(roster(), func(i fakeItem) bool {
		return i.kind == "ISSUE" && i.status != "Done" && containsFold(i.labels, "bug")
	}))

	// A filter that matches nothing reaches the scan bound, reports has_more instead of an end, and the
	// following batches finish the project.
	sparse := ItemListOptions{Assignee: "nobody", Limit: 2, StatusNot: []string{}}
	page, err := c.ListItems(context.Background(), sparse)
	if err != nil {
		t.Fatalf("ListItems() = %v", err)
	}
	if len(page.Items) != 0 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page = %+v, want an empty batch that is not presented as the end", page)
	}
	ids, _ = walk(t, c, sparse)
	if len(ids) != 0 {
		t.Errorf("items = %v, want none", ids)
	}
}

func TestCursorsAreBoundToFiltersAndTarget(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	c := client(t, base, projectTarget)
	page, err := c.ListItems(context.Background(), ItemListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.recorded())

	other := client(t, base, "orgs/octo-org/projects/8")
	for name, try := range map[string]func() error{
		"other filters": func() error {
			_, err := c.ListItems(context.Background(), ItemListOptions{Status: []string{"Todo"}, Cursor: page.NextCursor})
			return err
		},
		"other project": func() error {
			_, err := other.ListItems(context.Background(), ItemListOptions{Cursor: page.NextCursor})
			return err
		},
		"garbage": func() error {
			_, err := c.ListItems(context.Background(), ItemListOptions{Cursor: "not-a-cursor"})
			return err
		},
		"a raw GitHub cursor": func() error {
			_, err := c.ListItems(context.Background(), ItemListOptions{Cursor: "cur-3"})
			return err
		},
	} {
		if err := try(); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", name, err)
		}
	}
	if requests := f.recorded()[before:]; len(requests) != 0 {
		t.Errorf("requests = %d, want refusals before provider I/O", len(requests))
	}
	// Spelling the same filters differently keeps the cursor valid.
	if _, err := c.ListItems(context.Background(), ItemListOptions{StatusNot: []string{"done"}, Cursor: page.NextCursor}); err != nil {
		t.Errorf("an equivalent spelling was refused: %v", err)
	}
}

// One item is read with its body in exactly one query, without listing or reading comments first.
func TestGetItemLoadsExactlyOneBody(t *testing.T) {
	f := &fakeGitHub{items: roster(), foreign: []fakeItem{{id: "PVTI_foreign", kind: "ISSUE", title: "Elsewhere",
		repo: "octo-org/example", number: 99, body: "foreign-body-canary"}}}
	base := serve(t, f)
	c := client(t, base, projectTarget)

	item, err := c.GetItem(context.Background(), "PVTI_item00")
	if err != nil {
		t.Fatalf("GetItem() = %v", err)
	}
	if item.Body == nil || *item.Body != bodyCanary || item.Status != "In progress" || item.Fields["Priority"] != "P1" {
		t.Fatalf("item = %+v", item)
	}
	requests := f.recorded()
	if len(requests) != 1 || requests[0].variables["item"] != "PVTI_item00" ||
		strings.Contains(requests[0].document, "items(first") || strings.Contains(requests[0].document, "comments") {
		t.Fatalf("requests = %+v, want one targeted item query", requests)
	}

	draft, err := c.GetItem(context.Background(), "PVTI_item02")
	if err != nil || draft.Type != "draft_issue" || draft.Body == nil {
		t.Errorf("draft = %+v, %v; want the draft body", draft, err)
	}
	pull, err := c.GetItem(context.Background(), "PVTI_item01")
	if err != nil || pull.Type != "pull_request" || pull.Body != nil || pull.State != "merged" {
		t.Errorf("pull request = %+v, %v; want no body", pull, err)
	}

	foreign, err := c.GetItem(context.Background(), "PVTI_foreign")
	if err == nil || foreign != nil || strings.Contains(err.Error(), "foreign-body-canary") {
		t.Errorf("GetItem(foreign) = %+v, %v; want a refusal", foreign, err)
	}
	if _, err := c.GetItem(context.Background(), "PVTI_absent"); classOf(err) != provider.ClassProviderError {
		t.Errorf("GetItem(absent) = %v, want a provider error", err)
	}
}

func TestIssuesArePagedWithoutPullRequests(t *testing.T) {
	f := &fakeGitHub{}
	for n := 45; n >= 1; n-- {
		state := "open"
		if n%4 == 0 {
			state = "closed"
		}
		f.issues = append(f.issues, fakeIssue{number: n, state: state})
	}
	base := serve(t, f)
	c := client(t, base, repoTarget)

	options := IssueListOptions{Labels: []string{"bug"}, Assignee: "octocat"}
	var numbers []int
	for batch := 0; ; batch++ {
		page, err := c.ListIssues(context.Background(), options)
		if err != nil {
			t.Fatalf("ListIssues() = %v", err)
		}
		if batch == 0 && (len(page.Issues) != 30 || !page.HasMore) {
			t.Fatalf("first batch = %+v, want 30 issues that continue", page)
		}
		for _, issue := range page.Issues {
			numbers = append(numbers, issue.Number)
		}
		if !page.HasMore {
			break
		}
		options.Cursor = page.NextCursor
	}
	if len(numbers) != 34 || numbers[0] != 45 || numbers[33] != 1 {
		t.Errorf("numbers = %v, want every open issue once, newest first", numbers)
	}
	request := f.recorded()[0]
	vars := request.variables
	if fmt.Sprint(vars["states"]) != "[OPEN]" || fmt.Sprint(vars["labels"]) != "[bug]" || vars["assignee"] != "octocat" ||
		vars["owner"] != "octo-org" || vars["name"] != "example" || vars["first"] != float64(30) ||
		strings.Contains(request.document, "body") || strings.Contains(request.document, "comments") {
		t.Errorf("request = %+v", request)
	}
	all, err := c.ListIssues(context.Background(), IssueListOptions{State: "all", Limit: 100})
	if err != nil || len(all.Issues) != 45 || all.HasMore {
		t.Errorf("all issues = %d, %v", len(all.Issues), err)
	}
	if _, err := c.ListIssues(context.Background(), IssueListOptions{State: "closed", Cursor: options.Cursor}); !isInvalidRequest(err) {
		t.Errorf("a cursor of other filters = %v, want an invalid request", err)
	}
}

func TestGetIssueReadsOneIssueThroughREST(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	c := client(t, base, repoTarget)

	issue, err := c.GetIssue(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetIssue() = %v", err)
	}
	if issue.Body != bodyCanary || issue.Author != "octocat" || issue.Milestone != "v1" ||
		strings.Join(issue.Assignees, ",") != "hubot" || strings.Join(issue.Labels, ",") != "bug" {
		t.Errorf("issue = %+v", issue)
	}
	request := f.recorded()[0]
	if request.method != http.MethodGet || request.path != "/api/v3/repos/octo-org/example/issues/42" ||
		request.version != apiVersion || request.auth != "Bearer "+tokenValue {
		t.Errorf("request = %+v", request)
	}
	if _, err := c.GetIssue(context.Background(), 7); err == nil || !strings.Contains(err.Error(), "pull request") {
		t.Errorf("GetIssue(pull request) = %v, want a refusal", err)
	}
	if _, err := c.GetIssue(context.Background(), 5); classOf(err) != provider.ClassProviderError {
		t.Errorf("GetIssue(absent) = %v", err)
	}
}

// Tools of the other target kind, unknown arguments, and invalid cursors are refused before a credential is
// resolved or GitHub is contacted.
func TestTheCoreRefusesRequestsOutsideTheTargetBeforeIO(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, &reads), red)

	tests := []struct {
		name, operation, connection, arguments, code string
	}{
		{"project tool on a repository", "github.projectitems.list", "repo", `{}`, "unsupported"},
		{"item on a repository", "github.projectitems.get", "repo", `{"item_id":"PVTI_item00"}`, "unsupported"},
		{"issue tool on a project", "github.issues.list", "planning", `{}`, "unsupported"},
		{"issue on a project", "github.issues.get", "planning", `{"number":1}`, "unsupported"},
		{"an owner argument", "github.projectitems.list", "planning", `{"owner":"other-org"}`, "invalid"},
		{"a project argument", "github.projectitems.get", "planning", `{"item_id":"PVTI_item00","project":8}`, "invalid"},
		{"a repository argument", "github.issues.get", "repo", `{"number":1,"repo":"other/repo"}`, "invalid"},
		{"a free filter", "github.projectitems.list", "planning", `{"query":"is:open"}`, "invalid"},
		{"a filter separator", "github.projectitems.list", "planning", `{"status":["Done\" -status:\"x"]}`, "invalid"},
		{"a comma in a label", "github.projectitems.list", "planning", `{"labels":["a,b"]}`, "invalid"},
		{"a limit beyond the bound", "github.projectitems.list", "planning", `{"limit":101}`, "invalid"},
		{"too many values", "github.projectitems.list", "planning",
			`{"labels":["a","b","c","d","e","f","g","h","i","j","k"]}`, "invalid"},
		{"a foreign cursor", "github.projectitems.list", "planning", `{"cursor":"AAAAAAAAAAAAAAAAY3VyLTM"}`, "invalid"},
		{"no explicit connection", "github.projectitems.list", "", `{}`, "selection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reads = 0
			before := len(f.recorded())
			_, err := core.Invoke(context.Background(), application.InvokeRequest{
				Operation: tt.operation, Connection: tt.connection, Arguments: json.RawMessage(tt.arguments)})
			var (
				unsupported *capability.UnsupportedError
				selection   *application.ConnectionSelectionError
			)
			switch tt.code {
			case "unsupported":
				if !errors.As(err, &unsupported) {
					t.Errorf("err = %v, want an unsupported capability", err)
				}
			case "invalid":
				if !isInvalidRequest(err) {
					t.Errorf("err = %v, want an invalid request", err)
				}
			case "selection":
				if !errors.As(err, &selection) {
					t.Errorf("err = %v, want an explicit connection to be required", err)
				}
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("secret reads = %d, requests = %d; want none", reads, len(f.recorded())-before)
			}
		})
	}
}

func TestOperationsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	for n := 3; n >= 1; n-- {
		f.issues = append(f.issues, fakeIssue{number: n, state: "open"})
	}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	for _, request := range []application.InvokeRequest{
		{Operation: "github.projectitems.list", Connection: "planning", Arguments: json.RawMessage(`{}`)},
		{Operation: "github.projectitems.list", Connection: "planning",
			Arguments: json.RawMessage(`{"status":["Todo"],"type":"issue","limit":2}`)},
		{Operation: "github.projectitems.get", Connection: "planning", Arguments: json.RawMessage(`{"item_id":"PVTI_item02"}`)},
		{Operation: "github.issues.list", Connection: "repo", Arguments: json.RawMessage(`{"state":"all","limit":2}`)},
		{Operation: "github.issues.get", Connection: "repo", Arguments: json.RawMessage(`{"number":42}`)},
	} {
		response, err := core.Invoke(context.Background(), request)
		if err != nil {
			t.Errorf("%s %s = %v", request.Operation, request.Arguments, err)
			continue
		}
		if strings.Contains(string(response.Result), "comments") {
			t.Errorf("%s answered with comments: %s", request.Operation, response.Result)
		}
	}

	first, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "github.projectitems.list", Connection: "planning", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Items      []map[string]any `json:"items"`
		HasMore    bool             `json:"has_more"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Result, &listed); err != nil || len(listed.Items) != 30 || !listed.HasMore {
		t.Fatalf("list = %s, %v", first.Result, err)
	}
	if _, ok := listed.Items[0]["body"]; ok {
		t.Errorf("a list item carries a body: %v", listed.Items[0])
	}
	next, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "github.projectitems.list", Connection: "planning",
		Arguments: json.RawMessage(`{"cursor":"` + listed.NextCursor + `"}`)})
	if err != nil || !strings.Contains(string(next.Result), `"has_more":false`) {
		t.Errorf("second batch = %s, %v", next.Result, err)
	}
}

func TestProviderFailuresAreNormalized(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(30*time.Second).Unix(), 10)
	tests := []struct {
		name   string
		answer func(http.ResponseWriter)
		class  provider.Class
		detail string
	}{
		{"unauthorized", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Bad credentials `+tokenValue+`"}`)
		}, provider.ClassAuth, ""},
		{"forbidden", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
		}, provider.ClassPermission, ""},
		{"primary rate limit", func(w http.ResponseWriter) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", reset)
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
		}, provider.ClassRateLimited, "retry after"},
		{"secondary rate limit", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"You have exceeded a secondary rate limit."}`)
		}, provider.ClassRateLimited, ""},
		{"too many requests", func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "17")
			w.WriteHeader(http.StatusTooManyRequests)
		}, provider.ClassRateLimited, "retry after 17 seconds"},
		{"not found", func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }, provider.ClassProviderError, ""},
		{"redirect", func(w http.ResponseWriter) {
			w.Header().Set("Location", "https://elsewhere.example.invalid/")
			w.WriteHeader(http.StatusMovedPermanently)
		}, provider.ClassProviderError, "redirect"},
		{"gateway timeout", func(w http.ResponseWriter) { w.WriteHeader(http.StatusGatewayTimeout) }, provider.ClassTimeout, ""},
		{"server error", func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }, provider.ClassProviderError, "HTTP 502"},
		{"invalid json", func(w http.ResponseWriter) { fmt.Fprint(w, `{"data":`) }, provider.ClassInvalidResponse, ""},
		{"oversized", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"data":"`+strings.Repeat("x", maxResponseBytes)+`"}`)
		}, provider.ClassInvalidResponse, "size limit"},
		{"graphql not found", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"data":{"owner":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve `+tokenValue+`"}]}`)
		}, provider.ClassProviderError, ""},
		{"graphql forbidden", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"data":null,"errors":[{"type":"FORBIDDEN","message":"no"}]}`)
		}, provider.ClassPermission, ""},
		{"graphql scopes", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"errors":[{"type":"INSUFFICIENT_SCOPES","message":"needs read:project"}]}`)
		}, provider.ClassPermission, ""},
		{"graphql rate limit", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`)
		}, provider.ClassRateLimited, ""},
		{"graphql partial data", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"data":{"owner":{"projectV2":{"id":"PVT_x"}}},"errors":[{"message":"something"}]}`)
		}, provider.ClassProviderError, "rejected the query"},
		{"graphql without query support", func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"errors":[{"message":"Field 'items' doesn't accept argument 'query'"}]}`)
		}, provider.ClassProviderError, "does not support filtered project item queries"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeGitHub{failure: func(w http.ResponseWriter, _ *http.Request) bool { tt.answer(w); return true }}
			base := serve(t, f)
			red := &redact.Redactor{}
			c, err := open(resolvedConnection("gh", base, projectTarget), resolver(red, nil), red, freeLimiter())
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.ListItems(context.Background(), ItemListOptions{})
			if classOf(err) != tt.class || !strings.Contains(err.Error(), tt.detail) {
				t.Fatalf("err = %v (class %q), want class %q with %q", err, classOf(err), tt.class, tt.detail)
			}
			if strings.Contains(red.Error(err), tokenValue) || strings.Contains(err.Error(), tokenValue) {
				t.Errorf("the error carries the token: %v", err)
			}
		})
	}
}

// A spent budget holds the next request of the same token until the reported reset, within a bound.
func TestRateLimitHeadersHoldTheNextRequest(t *testing.T) {
	reset := time.Now().Add(5 * time.Second).Unix()
	f := &fakeGitHub{items: roster(), failure: func(w http.ResponseWriter, _ *http.Request) bool {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		return false
	}}
	base := serve(t, f)
	var (
		mu     sync.Mutex
		waited []time.Duration
	)
	limited := ratelimit.New(0, time.Now, func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		waited = append(waited, d)
		return nil
	})
	red := &redact.Redactor{}
	c, err := open(resolvedConnection("gh", base, repoTarget), resolver(red, nil), red, limited)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetIssue(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetIssue(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if len(waited) == 0 || waited[0] < 3*time.Second || waited[0] > 5*time.Second {
		t.Fatalf("waits = %v, want the reported reset to be honoured", waited)
	}
	if retryAfter(http.Header{"Retry-After": {"7"}}) != 7*time.Second || capHold(2*maxHold) != maxHold {
		t.Error("the retry hint or its bound is wrong")
	}
}

func TestTestConnectionReadsOnlyTheTarget(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	red := &redact.Redactor{}

	class, err := TestConnection(context.Background(), resolvedConnection("gh", base, projectTarget), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("project test = %q, %v", class, err)
	}
	class, err = TestConnection(context.Background(), resolvedConnection("gh", base, repoTarget), resolver(red, nil), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("repository test = %q, %v", class, err)
	}
	requests := f.recorded()
	if len(requests) != 2 || !strings.Contains(requests[0].document, "projectV2(number") ||
		strings.Contains(requests[0].document, "items") || requests[1].path != "/api/v3/repos/octo-org/example" {
		t.Errorf("requests = %+v, want one read of each target", requests)
	}
	class, _ = TestConnection(context.Background(), resolvedConnection("gh", base, userTarget), resolver(red, nil), red)
	if class != provider.ClassProviderError {
		t.Errorf("unknown project test = %q, want a provider error", class)
	}

	f.failure = func(w http.ResponseWriter, _ *http.Request) bool { w.WriteHeader(http.StatusUnauthorized); return true }
	class, _ = TestConnection(context.Background(), resolvedConnection("gh", base, repoTarget), resolver(red, nil), red)
	if class != provider.ClassAuth {
		t.Errorf("rejected token test = %q, want auth", class)
	}
	class, _ = TestConnection(context.Background(), resolvedConnection("gh", "http://api.github.com", repoTarget),
		resolver(red, nil), red)
	if class != provider.ClassProviderError {
		t.Errorf("plain http test = %q, want a refusal", class)
	}
}

// A resolved token never reaches a result, whatever GitHub answers.
func TestTheTokenNeverReachesTheOutput(t *testing.T) {
	items := roster()[:1]
	items[0].title = "Bearer " + tokenValue
	f := &fakeGitHub{items: items}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "github.projectitems.list", Connection: "planning", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response.Result), tokenValue) || !strings.Contains(string(response.Result), redact.Marker) {
		t.Errorf("result = %s, want the token redacted", response.Result)
	}
}

func registry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

func coreConfig(base string) *config.Config {
	credential := config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}
	return &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"gh": {Provider: Provider, BaseURL: base}},
		Credentials: map[string]config.Credential{"gh-reader": credential},
		Connections: map[string]config.Connection{
			"planning": {Service: "gh", Credential: "gh-reader", Target: projectTarget},
			"repo":     {Service: "gh", Credential: "gh-reader", Target: repoTarget},
		},
	}
}
