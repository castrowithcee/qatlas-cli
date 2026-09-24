package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
)

// planningTargets binds the project together with the one repository its planning tools may use.
var planningTargets = []string{projectTarget, repoTarget}

// mutation answers the project changes this provider sends. A field batch answers every alias on its own,
// like GitHub, and fails the aliases that change failField.
func (f *fakeGitHub) mutation(w http.ResponseWriter, document string, variables map[string]any) {
	if variables["project"] != projectID {
		fmt.Fprint(w, `{"data":null,"errors":[{"type":"NOT_FOUND","message":"no project"}]}`)
		return
	}
	switch {
	case strings.Contains(document, "addProjectV2ItemById"):
		fmt.Fprintf(w, `{"data":{"add":{"item":{"id":"PVTI_for_%s"}}}}`, variables["content"])
	case strings.Contains(document, "addProjectV2DraftIssue"):
		fmt.Fprint(w, `{"data":{"add":{"projectItem":{"id":"PVTI_draft"}}}}`)
	case strings.Contains(document, "archiveProjectV2Item"):
		fmt.Fprintf(w, `{"data":{"archive":{"item":{"id":%q}}}}`, variables["item"])
	default:
		data, errs := []string{}, []string{}
		for i := 0; ; i++ {
			alias := "f" + strconv.Itoa(i)
			field, ok := variables[alias]
			if !ok {
				break
			}
			if field == f.failField {
				data = append(data, `"`+alias+`":null`)
				errs = append(errs, `{"type":"UNPROCESSABLE","message":"Did not update `+tokenValue+`","path":["`+alias+`"]}`)
				continue
			}
			data = append(data, fmt.Sprintf(`"%s":{"projectV2Item":{"id":%q}}`, alias, variables["item"]))
		}
		answer := `{"data":{` + strings.Join(data, ",") + `}`
		if len(errs) > 0 {
			answer += `,"errors":[` + strings.Join(errs, ",") + `]`
		}
		fmt.Fprint(w, answer+`}`)
	}
}

// issueLookup answers a planning query that resolves the project and one issue of a repository. Number 7
// is a pull request, which the issue field of a repository does not resolve.
func (f *fakeGitHub) issueLookup(w http.ResponseWriter, variables map[string]any) {
	if variables["issue"] == float64(7) {
		fmt.Fprintf(w, `{"data":{"owner":{"projectV2":%s},"repository":{"issue":null}},`+
			`"errors":[{"type":"NOT_FOUND","path":["repository","issue"],"message":"Could not resolve to an Issue"}]}`,
			f.projectJSON())
		return
	}
	fmt.Fprintf(w, `{"data":{"owner":{"projectV2":%s},"repository":{"issue":{"id":"I_%v_%v"}}}}`,
		f.projectJSON(), variables["repoName"], variables["issue"])
}

// commentsPage answers the comments of issue 42 in pages; every other number is no issue.
func (f *fakeGitHub) commentsPage(w http.ResponseWriter, variables map[string]any) {
	if variables["number"] != float64(42) {
		fmt.Fprint(w, `{"data":{"repository":{"issue":null}},"errors":[{"type":"NOT_FOUND",`+
			`"path":["repository","issue"],"message":"no issue"}]}`)
		return
	}
	start := 0
	if after, ok := variables["after"].(string); ok {
		start, _ = strconv.Atoi(strings.TrimPrefix(after, "ccur-"))
		start++
	}
	end := min(start+int(variables["first"].(float64)), f.comments)
	nodes := []string{}
	for i := start; i < end; i++ {
		nodes = append(nodes, fmt.Sprintf(`{"id":"IC_%d","author":{"login":"octocat"},"body":"comment %d",`+
			`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","url":"https://github.com/c/%d"}`,
			i, i, i))
	}
	fmt.Fprintf(w, `{"data":{"repository":{"issue":{"comments":{"pageInfo":{"hasNextPage":%t,"endCursor":"ccur-%d"},`+
		`"nodes":[%s]}}}}}`, end < f.comments, end-1, strings.Join(nodes, ","))
}

func planningClient(t *testing.T, base string, targets ...string) *Client {
	t.Helper()
	red := &redact.Redactor{}
	resolved := resolvedConnection("gh", base, "")
	resolved.Targets = targets
	c, err := open(resolved, resolver(red, nil), red, freeLimiter())
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	return c
}

func fields(t *testing.T, document string) FieldValues {
	t.Helper()
	var values FieldValues
	if err := json.Unmarshal([]byte(document), &values); err != nil {
		t.Fatal(err)
	}
	return values
}

// split counts the queries and the mutations among the GraphQL requests, and the REST requests by method.
func split(requests []recorded) (queries, mutations []recorded, rest map[string]int) {
	rest = map[string]int{}
	for _, request := range requests {
		switch {
		case request.path != "/api/graphql":
			rest[request.method]++
		case strings.HasPrefix(request.document, "mutation"):
			mutations = append(mutations, request)
		default:
			queries = append(queries, request)
		}
	}
	return queries, mutations, rest
}

func results(planning *Planning) string {
	out := []string{}
	for _, field := range planning.Fields {
		out = append(out, field.Field+"="+field.Result)
	}
	return strings.Join(out, ",")
}

func TestRegisterPublishesTheChangeContracts(t *testing.T) {
	reg := registry(t)
	want := map[string]capability.Risk{
		"github.issues.create":            changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"github.issues.update":            changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"github.issues.close":             changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"github.issues.reopen":            changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"github.comments.list":            readRisk,
		"github.comments.create":          changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"github.projectitems.update":      changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"github.projectitems.add":         changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
		"github.projectitems.archive":     changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		"github.projectdrafts.create":     changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"github.projectissues.create":     changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		"github.projectitems.list":        readRisk,
		"github.projectitems.get":         readRisk,
		"github.issues.list":              readRisk,
		"github.issues.get":               readRisk,
		"github.workflows.list":           readRisk,
		"github.workflows.get":            readRisk,
		"github.workflowruns.list":        readRisk,
		"github.workflowruns.get":         readRisk,
		"github.workflowjobs.list":        readRisk,
		"github.workflowjobs.get":         readRisk,
		"github.workflowjobs.log":         jobsLog.Risk,
		"github.workflowartifacts.list":   readRisk,
		"github.workflows.dispatch":       changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
		"github.workflowruns.rerun":       changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
		"github.workflowruns.rerunfailed": changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
		"github.workflowruns.cancel":      changeRisk(capability.EffectExecute, capability.IdempotencyIdempotent),
		"github.workflowfiles.list": guardedRisk(capability.EffectRead, capability.IdempotencySafe,
			workflowFileSensitivity),
		"github.workflowfiles.get": guardedRisk(capability.EffectRead, capability.IdempotencySafe,
			workflowFileSensitivity),
		"github.workflowfiles.create": guardedRisk(capability.EffectCreate, capability.IdempotencyIdempotent,
			workflowFileSensitivity),
		"github.workflowfiles.update": guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent,
			workflowFileSensitivity),
		"github.workflows.enable": guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent,
			workflowFileSensitivity),
		"github.workflows.disable": guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent,
			workflowFileSensitivity),
		"github.actionspermissions.get": guardedRisk(capability.EffectRead, capability.IdempotencySafe,
			actionsSettingSensitivity),
		"github.actionspermissions.update": guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent,
			actionsSettingSensitivity),
		"github.workflowpermissions.get": guardedRisk(capability.EffectRead, capability.IdempotencySafe,
			actionsSettingSensitivity),
		"github.workflowpermissions.update": guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent,
			actionsSettingSensitivity),
	}
	operations := reg.Provider(Provider)
	if len(operations) != len(want) {
		t.Fatalf("operations = %d, want %d", len(operations), len(want))
	}
	for _, descriptor := range operations {
		risk, ok := want[descriptor.ID]
		if !ok || descriptor.Risk != risk || !descriptor.RequiresExplicitConnection {
			t.Errorf("%s risk = %+v, want %+v", descriptor.ID, descriptor.Risk, risk)
		}
		if descriptor.Risk.Effect != capability.EffectRead && descriptor.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s changes without confirmation", descriptor.ID)
		}
		for _, forbidden := range []string{"owner", "base_url", "query\"", "project_id", "document"} {
			if strings.Contains(string(descriptor.InputSchema), forbidden) {
				t.Errorf("%s input offers %q: %s", descriptor.ID, forbidden, descriptor.InputSchema)
			}
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	if len(metadata.DefaultPermissions) != 1 || metadata.DefaultPermissions[0] != config.PermissionRead {
		t.Errorf("default permissions = %v, want reads only", metadata.DefaultPermissions)
	}
}

func TestTargetListsAreAllowListsOfAnyMix(t *testing.T) {
	valid := [][]string{
		{projectTarget, repoTarget},
		{"repos/octo-org/other", userTarget, repoTarget},
		{repoTarget, "repos/octo-org/other"},
		{projectTarget, "orgs/octo-org/projects/8", "users/octocat/projects/*", "repos/octo-org/*"},
	}
	for _, values := range valid {
		if list, err := parseAllowlist(values); err != nil || len(list) != len(values) {
			t.Errorf("parseAllowlist(%v) = %+v, %v", values, list, err)
		}
	}
	for _, values := range [][]string{
		{projectTarget, repoTarget, "repos/Octo-Org/Example"},
		{projectTarget, "repos/octo-org"},
		{"repos/*/example"},
		{"repos/octo-org/ex*"},
		{"orgs/*/projects/7"},
		{"orgs/octo-org/projects/1-9"},
		{"repos/octo-org/*/x"},
		{"orgs/octo-org/*"},
	} {
		if _, err := parseAllowlist(values); err == nil {
			t.Errorf("parseAllowlist(%v) accepted an unusable target list", values)
		}
	}

	list, _ := parseAllowlist([]string{"repos/octo-org/*", "repos/hubot/example", "users/octocat/projects/*",
		projectTarget})
	for raw, want := range map[string]bool{
		"repos/Octo-Org/anything":   true,
		"repos/hubot/EXAMPLE":       true,
		"repos/hubot/other":         false,
		"repos/octo-org2/example":   false,
		"users/octocat/projects/12": true,
		"orgs/octocat/projects/12":  false,
		"orgs/octo-org/projects/7":  true,
		"orgs/octo-org/projects/70": false,
	} {
		parsed, _ := parseTarget(raw)
		if list.allows(parsed) != want {
			t.Errorf("allows(%s) = %v, want %v", raw, !want, want)
		}
	}
	if !(allowlist(nil)).allows(target{kind: kindRepository, owner: "any", repo: "thing"}) {
		t.Error("an empty allow-list refused a repository; without targets the token decides")
	}

	reg := registry(t)
	document := func(targets string) string {
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
    targets: ` + targets + "\n"
	}
	// Existing single targets and project-first lists stay valid, and so does any other mix.
	for _, targets := range []string{"[orgs/octo-org/projects/7, repos/octo-org/example]",
		"[repos/octo-org/example, repos/octo-org/other]", "[orgs/octo-org/projects/7, users/octocat/projects/1]",
		"[repos/octo-org/*, orgs/octo-org/projects/*]"} {
		if _, err := config.Decode(strings.NewReader(document(targets)), reg); err != nil {
			t.Errorf("targets %s were refused: %v", targets, err)
		}
	}
	if _, err := config.Decode(strings.NewReader(strings.Replace(document("[]"), "    targets: []\n", "", 1)),
		reg); err != nil {
		t.Errorf("a connection without targets was refused: %v", err)
	}
	for _, targets := range []string{"[repos/octo-org/example, repos/Octo-Org/Example]",
		"[repos/octo-org/ex*]"} {
		if _, err := config.Decode(strings.NewReader(document(targets)), reg); err == nil ||
			!strings.Contains(err.Error(), "connections.planning.target") {
			t.Errorf("targets %s: err = %v, want a refused target list", targets, err)
		}
	}
}

// Issue changes go through REST; an existing issue is read first so a pull request is never changed.
func TestIssueChangesUseRESTAndRefusePullRequests(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	c := client(t, base, repoTarget)
	title, body, labels := "Crash on start", "Steps", []string{"bug"}

	issue, err := c.CreateIssue(context.Background(), IssueContent{Title: &title, Body: &body, Labels: &labels})
	if err != nil || issue.Number != 101 || issue.Title != title || issue.Body != body {
		t.Fatalf("CreateIssue() = %+v, %v", issue, err)
	}
	created := f.recorded()[0]
	if created.method != http.MethodPost || created.path != "/api/v3/repos/octo-org/example/issues" ||
		created.body["title"] != title || fmt.Sprint(created.body["labels"]) != "[bug]" ||
		created.body["assignees"] != nil || created.auth != "Bearer "+tokenValue {
		t.Errorf("create request = %+v", created)
	}

	empty := []string{}
	if _, err := c.UpdateIssue(context.Background(), 42, IssueContent{Body: &body, Assignees: &empty}); err != nil {
		t.Fatalf("UpdateIssue() = %v", err)
	}
	closed, err := c.CloseIssue(context.Background(), 42, "not_planned")
	if err != nil || closed.State != "closed" || closed.StateReason != "not_planned" {
		t.Fatalf("CloseIssue() = %+v, %v", closed, err)
	}
	reopened, err := c.ReopenIssue(context.Background(), 42)
	if err != nil || reopened.State != "open" {
		t.Fatalf("ReopenIssue() = %+v, %v", reopened, err)
	}
	requests := f.recorded()[1:]
	if len(requests) != 6 {
		t.Fatalf("requests = %+v, want a read and a change per call", requests)
	}
	for i, want := range []map[string]any{
		{"body": body, "assignees": []any{}},
		{"state": "closed", "state_reason": "not_planned"},
		{"state": "open", "state_reason": "reopened"},
	} {
		read, change := requests[2*i], requests[2*i+1]
		if read.method != http.MethodGet || change.method != http.MethodPatch ||
			change.path != "/api/v3/repos/octo-org/example/issues/42" || fmt.Sprint(change.body) != fmt.Sprint(want) {
			t.Errorf("change %d = %+v then %+v, want %v", i, read, change, want)
		}
	}

	before := len(f.recorded())
	for name, try := range map[string]func() error{
		"update": func() error { _, err := c.UpdateIssue(context.Background(), 7, IssueContent{Body: &body}); return err },
		"close":  func() error { _, err := c.CloseIssue(context.Background(), 7, ""); return err },
		"comment": func() error {
			_, err := c.CreateComment(context.Background(), 7, "hello")
			return err
		},
	} {
		if err := try(); err == nil || !strings.Contains(err.Error(), "pull request") {
			t.Errorf("%s on a pull request = %v, want a refusal", name, err)
		}
	}
	if _, _, rest := split(f.recorded()[before:]); rest[http.MethodPatch] != 0 || rest[http.MethodPost] != 0 {
		t.Errorf("a pull request was changed: %v", rest)
	}
	for _, try := range []func() error{
		func() error { _, err := c.UpdateIssue(context.Background(), 42, IssueContent{}); return err },
		func() error { _, err := c.CloseIssue(context.Background(), 42, "wontfix"); return err },
		func() error { _, err := c.CreateIssue(context.Background(), IssueContent{Body: &body}); return err },
	} {
		if err := try(); !isInvalidRequest(err) {
			t.Errorf("err = %v, want an invalid request", err)
		}
	}
}

// Comments are read and written only by their own tools, for one issue at a time.
func TestCommentsAreListedAndWrittenOnlyOnRequest(t *testing.T) {
	f := &fakeGitHub{comments: 45}
	base := serve(t, f)
	c := client(t, base, repoTarget)

	options := CommentListOptions{Number: 42}
	var ids []string
	for batch := 0; ; batch++ {
		page, err := c.ListComments(context.Background(), options)
		if err != nil {
			t.Fatalf("ListComments() = %v", err)
		}
		if batch == 0 && (len(page.Comments) != 30 || !page.HasMore || page.NextCursor == "") {
			t.Fatalf("first batch = %+v, want 30 comments that continue", page)
		}
		for _, comment := range page.Comments {
			ids = append(ids, comment.ID)
		}
		if !page.HasMore {
			break
		}
		options.Cursor = page.NextCursor
	}
	if len(ids) != 45 || ids[0] != "IC_0" || ids[44] != "IC_44" {
		t.Errorf("comments = %v, want every comment once, oldest first", ids)
	}
	if _, err := c.ListComments(context.Background(), CommentListOptions{Number: 43, Cursor: options.Cursor}); !isInvalidRequest(err) {
		t.Errorf("a cursor of another issue = %v, want an invalid request", err)
	}
	if _, err := c.ListComments(context.Background(), CommentListOptions{Number: 5}); classOf(err) != provider.ClassNotFound ||
		!strings.Contains(err.Error(), "GitHub does not hold issue #5 in repository octo-org/example") {
		t.Errorf("comments of no issue = %v, want not-found naming the issue", err)
	}

	before := len(f.recorded())
	comment, err := c.CreateComment(context.Background(), 42, "Fixed "+bodyCanary)
	if err != nil || comment.ID != "IC_new" || comment.Body != "Fixed "+bodyCanary || comment.Author != "octocat" {
		t.Fatalf("CreateComment() = %+v, %v", comment, err)
	}
	requests := f.recorded()[before:]
	if len(requests) != 2 || requests[0].method != http.MethodGet ||
		requests[1].path != "/api/v3/repos/octo-org/example/issues/42/comments" || len(requests[1].body) != 1 {
		t.Errorf("requests = %+v, want the issue read and one comment", requests)
	}

	// The issue reads never ask for comments.
	before = len(f.recorded())
	if _, err := c.GetIssue(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	for _, request := range f.recorded()[before:] {
		if strings.Contains(request.path, "comments") || strings.Contains(request.document, "comments") {
			t.Errorf("an issue read asked for comments: %+v", request)
		}
	}
}

// Field values are resolved against one query of the project, then written in batches of aliased
// mutations; nothing is resolved twice.
func TestFieldChangesResolveOnceAndWriteInBatches(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	c := planningClient(t, base, planningTargets...)

	values := fields(t, `{"Status":"in progress","priority":"P1","Estimate":3.5,"Due":"2026-10-01",`+
		`"Sprint":"sprint 4","Note":null,"T1":"a","T2":"b","T3":"c","T4":"d","T5":"e","T6":"f"}`)
	planning, err := c.UpdateItemFields(context.Background(), "PVTI_item00", values)
	if err != nil || !planning.Complete || planning.Error != "" || planning.ItemID != "PVTI_item00" {
		t.Fatalf("UpdateItemFields() = %+v, %v", planning, err)
	}
	if got := results(planning); got != "Due=updated,Estimate=updated,Note=updated,Priority=updated,Sprint=updated,"+
		"Status=updated,T1=updated,T2=updated,T3=updated,T4=updated,T5=updated,T6=updated" {
		t.Errorf("results = %s", got)
	}
	queries, mutations, rest := split(f.recorded())
	if len(queries) != 1 || len(mutations) != 2 || len(rest) != 0 {
		t.Fatalf("requests = %d queries, %d mutations, %v REST; want one resolution and two batches",
			len(queries), len(mutations), rest)
	}
	if !strings.Contains(queries[0].document, "options{id name}") || queries[0].variables["item"] != "PVTI_item00" {
		t.Errorf("resolution = %s", queries[0].document)
	}
	first, second := mutations[0], mutations[1]
	if strings.Count(first.document, "ProjectV2ItemFieldValue(") != 10 ||
		strings.Count(second.document, "ProjectV2ItemFieldValue(") != 2 {
		t.Errorf("batches = %s\n%s", first.document, second.document)
	}
	vars := first.variables
	if vars["project"] != projectID || vars["item"] != "PVTI_item00" || vars["f0"] != "F_due" ||
		fmt.Sprint(vars["v0"]) != "map[date:2026-10-01]" || fmt.Sprint(vars["v1"]) != "map[number:3.5]" ||
		!strings.Contains(first.document, "f2:clearProjectV2ItemFieldValue") || vars["v2"] != nil ||
		fmt.Sprint(vars["v3"]) != "map[singleSelectOptionId:O_p1]" || fmt.Sprint(vars["v4"]) != "map[iterationId:I_s4]" ||
		fmt.Sprint(vars["v5"]) != "map[singleSelectOptionId:O_progress]" || fmt.Sprint(vars["v6"]) != "map[text:a]" {
		t.Errorf("first batch variables = %v", vars)
	}
	for _, mutation := range mutations {
		if strings.Contains(mutation.document, tokenValue) || strings.Contains(mutation.document, "P1") ||
			strings.Contains(mutation.document, "2026") {
			t.Errorf("a value reached the document instead of a variable: %s", mutation.document)
		}
	}
}

// A field, an option, or a value that does not fit is refused after the one resolution and before any
// change; an item of another project is never changed.
func TestFieldChangesAreCheckedBeforeTheFirstChange(t *testing.T) {
	f := &fakeGitHub{items: roster(), foreign: []fakeItem{{id: "PVTI_foreign", kind: "ISSUE", title: "Elsewhere",
		repo: "octo-org/example", number: 99}}}
	base := serve(t, f)
	c := planningClient(t, base, planningTargets...)

	for name, document := range map[string]string{
		"unknown field":              `{"Status":"Todo","Owner":"x"}`,
		"unknown option":             `{"Status":"Blocked"}`,
		"unknown iteration":          `{"Sprint":"Sprint 9"}`,
		"text for a number":          `{"Estimate":"three"}`,
		"number for a text":          `{"Note":3}`,
		"malformed date":             `{"Due":"01.10.2026"}`,
		"unsupported type":           `{"Assignees":"octocat"}`,
		"title is not a field value": `{"Title":"x"}`,
	} {
		before := len(f.recorded())
		_, err := c.UpdateItemFields(context.Background(), "PVTI_item00", fields(t, document))
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", name, err)
		}
		if _, mutations, _ := split(f.recorded()[before:]); len(mutations) != 0 {
			t.Errorf("%s: a change was sent", name)
		}
	}
	for name, document := range map[string]string{
		"no field":        `{}`,
		"a boolean":       `{"Note":true}`,
		"an object":       `{"Note":{"text":"x"}}`,
		"one field twice": `{"Note":"a","note":"b"}`,
		"a control name":  `{"No\u0007te":"a"}`,
	} {
		before := len(f.recorded())
		if _, err := c.UpdateItemFields(context.Background(), "PVTI_item00", fields(t, document)); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", name, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s: GitHub was contacted", name)
		}
	}

	before := len(f.recorded())
	for name, try := range map[string]func() error{
		"update": func() error {
			_, err := c.UpdateItemFields(context.Background(), "PVTI_foreign", fields(t, `{"Status":"Todo"}`))
			return err
		},
		"archive": func() error { _, err := c.ArchiveItem(context.Background(), "PVTI_foreign"); return err },
	} {
		if err := try(); classOf(err) != provider.ClassNotFound ||
			!strings.Contains(err.Error(), "this item in project orgs/octo-org/projects/7") {
			t.Errorf("%s of a foreign item = %v, want a refusal", name, err)
		}
	}
	if _, mutations, _ := split(f.recorded()[before:]); len(mutations) != 0 {
		t.Errorf("a foreign item was changed: %d mutations", len(mutations))
	}
}

// A field that fails inside a batch is reported next to the ones that were written, and the batches after
// it are not sent. A request that certainly changed nothing is an error.
func TestPartialFieldChangesAreReported(t *testing.T) {
	f := &fakeGitHub{items: roster(), failField: "F_due"}
	base := serve(t, f)
	c := planningClient(t, base, planningTargets...)

	planning, err := c.UpdateItemFields(context.Background(), "PVTI_item00",
		fields(t, `{"Due":"2026-10-01","Estimate":1,"Status":"Todo","T1":"a","T2":"b","T3":"c","T4":"d",`+
			`"T5":"e","T6":"f","Note":"n","Priority":"P1","Sprint":"Sprint 5"}`))
	if err != nil || planning.Complete || planning.Error == "" {
		t.Fatalf("UpdateItemFields() = %+v, %v; want a partial answer", planning, err)
	}
	if got := results(planning); got != "Due=failed,Estimate=updated,Note=updated,Priority=updated,Sprint=updated,"+
		"Status=updated,T1=updated,T2=updated,T3=updated,T4=updated,T5=not_sent,T6=not_sent" {
		t.Errorf("results = %s", got)
	}
	if strings.Contains(planning.Fields[0].Message, tokenValue) || strings.Contains(planning.Error, tokenValue) {
		t.Errorf("a GitHub message was copied: %+v", planning)
	}
	if _, mutations, _ := split(f.recorded()); len(mutations) != 1 {
		t.Errorf("mutations = %d, want the batches after a failure left out", len(mutations))
	}

	if _, err := c.UpdateItemFields(context.Background(), "PVTI_item00", fields(t, `{"Due":"2026-10-01"}`)); err == nil ||
		classOf(err) != provider.ClassProviderError {
		t.Errorf("a change that failed as a whole = %v, want an error", err)
	}

	// A batch without an answer may have been written: its fields are unknown, not failed.
	f.failField = ""
	f.failure = func(w http.ResponseWriter, _ *http.Request) bool {
		requests := f.recorded()
		if !strings.HasPrefix(requests[len(requests)-1].document, "mutation") {
			return false
		}
		w.WriteHeader(http.StatusBadGateway)
		return true
	}
	planning, err = c.UpdateItemFields(context.Background(), "PVTI_item00", fields(t, `{"Note":"n","T1":"a"}`))
	if err != nil || planning.Complete || results(planning) != "Note=unknown,T1=unknown" ||
		!strings.Contains(planning.Error, "may have been applied") {
		t.Errorf("UpdateItemFields() = %+v, %v; want unknown outcomes", planning, err)
	}
}

// Adding an existing issue and writing its fields are separate requests, after one resolution of the
// project, its fields, and the issue.
func TestAddIssueSeparatesTheAddFromTheFieldChanges(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	c := planningClient(t, base, planningTargets...)

	planning, err := c.AddIssue(context.Background(), "octo-org/EXAMPLE", 42, fields(t, `{"Status":"Todo"}`))
	if err != nil || !planning.Complete || planning.ItemID != "PVTI_for_I_example_42" || results(planning) != "Status=updated" {
		t.Fatalf("AddIssue() = %+v, %v", planning, err)
	}
	queries, mutations, _ := split(f.recorded())
	if len(queries) != 1 || len(mutations) != 2 {
		t.Fatalf("requests = %d queries, %d mutations", len(queries), len(mutations))
	}
	if queries[0].variables["repoOwner"] != "octo-org" || queries[0].variables["repoName"] != "example" ||
		queries[0].variables["issue"] != float64(42) {
		t.Errorf("resolution variables = %v", queries[0].variables)
	}
	if !strings.Contains(mutations[0].document, "addProjectV2ItemById") ||
		strings.Contains(mutations[0].document, "updateProjectV2ItemFieldValue") ||
		strings.Contains(mutations[1].document, "addProjectV2ItemById") ||
		mutations[1].variables["item"] != "PVTI_for_I_example_42" {
		t.Errorf("mutations = %s\n%s", mutations[0].document, mutations[1].document)
	}

	before := len(f.recorded())
	if _, err := c.AddIssue(context.Background(), "octo-org/other", 42, nil); !isInvalidRequest(err) {
		t.Errorf("an issue of another repository = %v, want an invalid request", err)
	}
	if len(f.recorded()) != before {
		t.Error("an issue of another repository reached GitHub")
	}
	if _, err := c.AddIssue(context.Background(), "octo-org/example", 7, nil); classOf(err) != provider.ClassNotFound ||
		!strings.Contains(err.Error(), "issue #7 in repository octo-org/example") {
		t.Errorf("a pull request = %v, want not-found naming the issue", err)
	}
	if _, mutations, _ := split(f.recorded()[before:]); len(mutations) != 0 {
		t.Error("a refused add sent a change")
	}
	without := planningClient(t, base, projectTarget)
	if _, err := without.AddIssue(context.Background(), "octo-org/example", 42, nil); !isInvalidRequest(err) {
		t.Errorf("a project without repositories = %v, want an invalid request", err)
	}
}

// The planning tool creates the issue in a configured repository, adds it, and sets its fields, each in a
// request of its own. Everything is resolved first; once the issue exists, it is always reported.
func TestCreatePlannedIssue(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	c := planningClient(t, base, planningTargets...)
	title := "Crash on start"

	planning, err := c.CreatePlannedIssue(context.Background(), "octo-org/example", IssueContent{Title: &title},
		fields(t, `{"Status":"Todo","Priority":"P1"}`))
	if err != nil || !planning.Complete || planning.Issue == nil || planning.Issue.Number != 101 ||
		planning.Issue.Repository != "octo-org/example" || planning.ItemID != "PVTI_for_I_created" ||
		results(planning) != "Priority=updated,Status=updated" {
		t.Fatalf("CreatePlannedIssue() = %+v, %v", planning, err)
	}
	requests := f.recorded()
	order := []string{}
	for _, request := range requests {
		kind := request.method + " " + request.path
		if request.path == "/api/graphql" {
			kind = strings.SplitN(request.document, "(", 2)[0]
		}
		order = append(order, kind)
	}
	if strings.Join(order, "|") != "query|POST /api/v3/repos/octo-org/example/issues|mutation|mutation" {
		t.Errorf("requests = %v, want resolution, create, add, fields", order)
	}

	// An unknown field is refused before the issue exists.
	before := len(f.recorded())
	_, err = c.CreatePlannedIssue(context.Background(), "octo-org/example", IssueContent{Title: &title},
		fields(t, `{"Status":"Blocked"}`))
	if !isInvalidRequest(err) {
		t.Errorf("an unknown option = %v, want an invalid request", err)
	}
	if _, _, rest := split(f.recorded()[before:]); rest[http.MethodPost] != 0 {
		t.Error("an issue was created although a field value was refused")
	}

	// A failed add still reports the created issue and sends no field change.
	f.failure = func(w http.ResponseWriter, _ *http.Request) bool {
		requests := f.recorded()
		if !strings.Contains(requests[len(requests)-1].document, "addProjectV2ItemById") {
			return false
		}
		fmt.Fprint(w, `{"data":null,"errors":[{"type":"FORBIDDEN","message":"no"}]}`)
		return true
	}
	before = len(f.recorded())
	planning, err = c.CreatePlannedIssue(context.Background(), "octo-org/example", IssueContent{Title: &title},
		fields(t, `{"Status":"Todo"}`))
	if err != nil || planning.Complete || planning.Issue == nil || planning.ItemID != "" ||
		results(planning) != "Status=not_sent" || !strings.Contains(planning.Error, "created but not added") {
		t.Errorf("CreatePlannedIssue() = %+v, %v; want the created issue with the failed add", planning, err)
	}
	if _, mutations, _ := split(f.recorded()[before:]); len(mutations) != 1 {
		t.Errorf("mutations = %d, want only the failed add", len(mutations))
	}
}

func TestDraftsAndArchive(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	c := planningClient(t, base, projectTarget)

	body := "details"
	planning, err := c.CreateDraft(context.Background(), "An idea", &body, nil)
	if err != nil || !planning.Complete || planning.ItemID != "PVTI_draft" || len(planning.Fields) != 0 {
		t.Fatalf("CreateDraft() = %+v, %v", planning, err)
	}
	queries, mutations, _ := split(f.recorded())
	if len(queries) != 1 || strings.Contains(queries[0].document, "fields(") || len(mutations) != 1 ||
		mutations[0].variables["title"] != "An idea" || mutations[0].variables["body"] != body {
		t.Errorf("draft requests = %+v %+v", queries, mutations)
	}

	archived, err := c.ArchiveItem(context.Background(), "PVTI_item03")
	if err != nil || !archived.Archived || archived.ItemID != "PVTI_item03" {
		t.Fatalf("ArchiveItem() = %+v, %v", archived, err)
	}
	_, mutations, _ = split(f.recorded())
	if last := mutations[len(mutations)-1]; last.variables["item"] != "PVTI_item03" || last.variables["project"] != projectID {
		t.Errorf("archive = %+v", last)
	}
}

// A change whose outcome is unclear is sent exactly once and says that it may have been applied.
func TestUnclearChangesAreNeverRepeated(t *testing.T) {
	title := "Crash on start"
	for name, answer := range map[string]func(http.ResponseWriter){
		"server error":    func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
		"gateway timeout": func(w http.ResponseWriter) { w.WriteHeader(http.StatusGatewayTimeout) },
		"invalid answer":  func(w http.ResponseWriter) { w.WriteHeader(http.StatusCreated); fmt.Fprint(w, `{"number":`) },
		"dropped connection": func(w http.ResponseWriter) {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeGitHub{}
			f.failure = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodPost {
					return false
				}
				answer(w)
				return true
			}
			base := serve(t, f)
			c := client(t, base, repoTarget)
			_, err := c.CreateIssue(context.Background(), IssueContent{Title: &title})
			if err == nil || !strings.Contains(err.Error(), "may have been applied") {
				t.Fatalf("CreateIssue() = %v, want an unclear outcome", err)
			}
			if _, _, rest := split(f.recorded()); rest[http.MethodPost] != 1 {
				t.Errorf("POST requests = %d, want exactly one", rest[http.MethodPost])
			}
			_, err = c.CreateComment(context.Background(), 42, "hello")
			if err == nil || !strings.Contains(err.Error(), "may have been applied") {
				t.Fatalf("CreateComment() = %v, want an unclear outcome", err)
			}
			if _, _, rest := split(f.recorded()); rest[http.MethodPost] != 2 {
				t.Errorf("POST requests = %d, want one per call", rest[http.MethodPost])
			}
		})
	}

	// A refusal is clear: nothing happened, and the message does not claim otherwise.
	f := &fakeGitHub{failure: func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost {
			return false
		}
		w.WriteHeader(http.StatusForbidden)
		return true
	}}
	base := serve(t, f)
	c := client(t, base, repoTarget)
	_, err := c.CreateIssue(context.Background(), IssueContent{Title: &title})
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "may have been applied") ||
		!strings.Contains(err.Error(), "may not change") {
		t.Errorf("CreateIssue() = %v, want a clear permission refusal", err)
	}
}

// Every change holds the next request of the same token for the mutation interval.
func TestChangesAreSpaced(t *testing.T) {
	f := &fakeGitHub{items: roster()}
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
	resolved := resolvedConnection("gh", base, "")
	resolved.Targets = planningTargets
	c, err := open(resolved, resolver(red, nil), red, limited)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddIssue(context.Background(), "octo-org/example", 42, fields(t, `{"Status":"Todo"}`)); err != nil {
		t.Fatal(err)
	}
	if len(waited) != 1 || waited[0] < mutationInterval/2 {
		t.Errorf("waits = %v, want the field change to wait after the add", waited)
	}
}

// coreChangeConfig binds a repository connection and a project connection with its repository, both
// allowed to change, and two connections that may not.
func coreChangeConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: all}
	cfg.Connections["planning"] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: planningTargets,
		Permissions: all}
	cfg.Connections["reader"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget}
	cfg.Connections["listed"] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: planningTargets,
		Permissions: all, Tools: []string{"github.projectitems.list", "github.projectitems.update"}}
	cfg.Connections["open"] = config.Connection{Service: "gh", Credential: "gh-reader", Permissions: all}
	return cfg
}

// Changes that are unconfirmed, outside the local permissions or tools, outside the connection's targets, or
// without a target the connection can settle end before a credential is resolved and before GitHub is
// contacted. A tool that touches a project and a repository checks both.
func TestTheCoreRefusesChangesBeforeIO(t *testing.T) {
	f := &fakeGitHub{items: roster()}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, &reads), red)

	tests := []struct {
		name, operation, connection, arguments string
		confirmed                              bool
		want                                   any
	}{
		{"unconfirmed create", "github.issues.create", "repo", `{"title":"x"}`, false,
			&application.ConfirmationRequiredError{}},
		{"unconfirmed comment", "github.comments.create", "repo", `{"number":42,"body":"x"}`, false,
			&application.ConfirmationRequiredError{}},
		{"unconfirmed field change", "github.projectitems.update", "planning",
			`{"item_id":"PVTI_item00","fields":{"Status":"Todo"}}`, false, &application.ConfirmationRequiredError{}},
		{"unconfirmed planned issue", "github.projectissues.create", "planning",
			`{"repository":"octo-org/example","title":"x"}`, false, &application.ConfirmationRequiredError{}},
		{"reads only by default", "github.issues.create", "reader", `{"title":"x"}`, true,
			&capability.UnsupportedError{}},
		{"a tools list without the tool", "github.projectitems.archive", "listed", `{"item_id":"PVTI_item00"}`, true,
			&capability.UnsupportedError{}},
		{"an issue change outside the targets", "github.issues.update", "planning",
			`{"number":42,"title":"x","repository":"octo-org/other"}`, true, &application.InvalidRequestError{}},
		{"a repository pattern as argument", "github.comments.list", "planning",
			`{"number":42,"repository":"octo-org/*"}`, true, &application.InvalidRequestError{}},
		{"a project change without a project target", "github.projectitems.update", "repo",
			`{"item_id":"PVTI_item00","fields":{"Status":"Todo"}}`, true, &application.InvalidRequestError{}},
		{"a planned issue without a project target", "github.projectissues.create", "repo",
			`{"repository":"octo-org/example","title":"x"}`, true, &application.InvalidRequestError{}},
		{"a project outside the targets", "github.projectitems.update", "planning",
			`{"item_id":"PVTI_item00","fields":{"Status":"Todo"},"project":"orgs/octo-org/projects/8"}`, true,
			&application.InvalidRequestError{}},
		{"a project of the other owner kind", "github.projectitems.archive", "planning",
			`{"item_id":"PVTI_item00","project":"users/octo-org/projects/7"}`, true, &application.InvalidRequestError{}},
		{"a planned issue in a project outside the targets", "github.projectissues.create", "planning",
			`{"repository":"octo-org/example","title":"x","project":"orgs/other/projects/1"}`, true,
			&application.InvalidRequestError{}},
		{"no repository on an open connection", "github.issues.update", "open", `{"number":42,"title":"x"}`, true,
			&application.InvalidRequestError{}},
		{"no project on an open connection", "github.projectdrafts.create", "open", `{"title":"x"}`, true,
			&application.InvalidRequestError{}},
		{"a repository outside the targets", "github.projectissues.create", "planning",
			`{"repository":"octo-org/other","title":"x"}`, true, &application.InvalidRequestError{}},
		{"an issue outside the targets", "github.projectitems.add", "planning",
			`{"repository":"other-org/example","number":1}`, true, &application.InvalidRequestError{}},
		{"an owner argument", "github.issues.create", "repo", `{"title":"x","owner":"other-org"}`, true,
			&application.InvalidRequestError{}},
		{"a project argument", "github.projectdrafts.create", "planning", `{"title":"x","project":8}`, true,
			&application.InvalidRequestError{}},
		{"too many fields", "github.projectitems.update", "planning",
			`{"item_id":"PVTI_item00","fields":{` + manyFields(21) + `}}`, true, &application.InvalidRequestError{}},
		{"a blank title", "github.issues.create", "repo", `{"title":"   "}`, true, &application.InvalidRequestError{}},
		{"an unknown close reason", "github.issues.close", "repo", `{"number":42,"state_reason":"wontfix"}`, true,
			&application.InvalidRequestError{}},
		{"too many labels", "github.issues.update", "repo", `{"number":42,"labels":["a","b","c","d","e","f","g",` +
			`"h","i","j","k","l","m","n","o","p","q","r","s","t","u"]}`, true, &application.InvalidRequestError{}},
		{"a foreign comment cursor", "github.comments.list", "repo", `{"number":42,"cursor":"AAAAAAAAAAAAAAAAY3VyLTM"}`,
			true, &application.InvalidRequestError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reads = 0
			before := len(f.recorded())
			_, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: tt.operation,
				Connection: tt.connection, Arguments: json.RawMessage(tt.arguments), Confirmed: tt.confirmed})
			if want := fmt.Sprintf("%T", tt.want); fmt.Sprintf("%T", err) != want {
				t.Errorf("err = %T %v, want %s", err, err, want)
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("secret reads = %d, requests = %d; want none", reads, len(f.recorded())-before)
			}
		})
	}
}

func manyFields(n int) string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf(`"F%02d":1`, i)
	}
	return strings.Join(names, ",")
}

// Every change satisfies its output contract through the application core once it is confirmed.
func TestChangesSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{items: roster(), comments: 3}
	base := serve(t, f)
	red := &redact.Redactor{}
	var audit strings.Builder
	core := application.New(registry(t), coreChangeConfig(base), resolver(red, nil), red)
	core.SetAudit(&audit)

	for _, request := range []application.InvokeRequest{
		{Operation: "github.issues.create", Connection: "repo", Arguments: json.RawMessage(`{"title":"x","labels":["bug"]}`)},
		{Operation: "github.issues.update", Connection: "repo", Arguments: json.RawMessage(`{"number":42,"title":"y"}`)},
		{Operation: "github.issues.close", Connection: "repo", Arguments: json.RawMessage(`{"number":42}`)},
		{Operation: "github.issues.reopen", Connection: "repo", Arguments: json.RawMessage(`{"number":42}`)},
		{Operation: "github.comments.list", Connection: "repo", Arguments: json.RawMessage(`{"number":42,"limit":2}`)},
		{Operation: "github.comments.create", Connection: "repo", Arguments: json.RawMessage(`{"number":42,"body":"z"}`)},
		{Operation: "github.projectitems.update", Connection: "planning",
			Arguments: json.RawMessage(`{"item_id":"PVTI_item00","fields":{"Status":"Done","Estimate":null}}`)},
		{Operation: "github.projectitems.add", Connection: "planning",
			Arguments: json.RawMessage(`{"repository":"octo-org/example","number":42}`)},
		{Operation: "github.projectitems.archive", Connection: "planning", Arguments: json.RawMessage(`{"item_id":"PVTI_item00"}`)},
		{Operation: "github.projectdrafts.create", Connection: "planning",
			Arguments: json.RawMessage(`{"title":"idea","fields":{"Status":"Todo"}}`)},
		{Operation: "github.projectissues.create", Connection: "planning",
			Arguments: json.RawMessage(`{"repository":"octo-org/example","title":"x","fields":{"Status":"Todo"}}`)},
		{Operation: "github.projectitems.update", Connection: "listed",
			Arguments: json.RawMessage(`{"item_id":"PVTI_item00","fields":{"Note":"n"}}`)},
	} {
		request.Confirmed = true
		response, err := core.Invoke(context.Background(), request)
		if err != nil {
			t.Errorf("%s %s = %v", request.Operation, request.Arguments, err)
			continue
		}
		if strings.Contains(string(response.Result), tokenValue) {
			t.Errorf("%s answered with the token: %s", request.Operation, response.Result)
		}
	}
	if strings.Count(audit.String(), `"result":"success"`) != 11 {
		t.Errorf("audit = %s, want one success event per confirmed change", audit.String())
	}
}
