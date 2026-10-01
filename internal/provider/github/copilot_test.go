package github

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// fakeCopilot answers the GraphQL assignment reads and mutation and the requested reviewers route. Issue 42
// exists and is assigned to actor U_hubot; other issues do not exist.
type fakeCopilot struct {
	noCopilot    bool
	copilotPage  int // the page of suggestedActors that holds Copilot
	reviewStatus int
	mutations    int
	pages        int
}

func (c *fakeCopilot) route(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/graphql":
		return false
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls/7/requested_reviewers"):
		if c.reviewStatus != 0 {
			w.WriteHeader(c.reviewStatus)
			fmt.Fprint(w, `{"message":"nope"}`)
			return true
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"number":7,"node_id":"PR_7","title":"t","state":"open","user":{"login":"octocat"},`+
			`"requested_reviewers":[{"login":"copilot-pull-request-reviewer[bot]"}],"head":{"sha":"`+
			strings.Repeat("a", 40)+`"},"base":{"ref":"main"}}`)
		return true
	}
	return false
}

// graphql is installed as the fake's GraphQL answer through the failure hook, which sees the recorded body.
func (c *fakeCopilot) graphql(f *fakeGitHub) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/graphql" {
			return c.route(w, r)
		}
		request := f.recorded()[len(f.recorded())-1]
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.document, "mutation") {
			c.mutations++
			fmt.Fprint(w, `{"data":{"updateIssue":{"issue":{"number":42,"url":"https://github.com/octo-org/example/issues/42"}}}}`)
			return true
		}
		page := c.pages
		c.pages++
		actors := `{"__typename":"User"}`
		if !c.noCopilot && page == c.copilotPage {
			actors = `{"__typename":"Bot","id":"BOT_copilot","login":"copilot-swe-agent"}`
		}
		hasNext := page < c.copilotPage || (c.noCopilot && page < 1)
		issue := `null`
		if request.variables["number"] == float64(42) {
			issue = `{"id":"I_42","assignees":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"U_hubot"}]}}`
		}
		fmt.Fprintf(w, `{"data":{"repository":{"id":"R_1","issue":%s,"suggestedActors":{"pageInfo":{"hasNextPage":%t,`+
			`"endCursor":"c%d"},"nodes":[%s]}}}}`, issue, hasNext, page, actors)
		return true
	}
}

func copilotRig(t *testing.T, c *fakeCopilot) (*fakeGitHub, *application.Core, *int) {
	t.Helper()
	f := &fakeGitHub{}
	f.failure = c.graphql(f)
	cfg := coreChangeConfig(serve(t, f))
	cfg.Connections["project"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: projectTarget,
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}}
	red := &redact.Redactor{}
	reads := 0
	return f, application.New(registry(t), cfg, resolver(red, &reads), red), &reads
}

func lastMutation(t *testing.T, f *fakeGitHub) map[string]any {
	t.Helper()
	requests := f.recorded()
	last := requests[len(requests)-1]
	if !strings.HasPrefix(last.document, "mutation") {
		t.Fatalf("last request = %+v, want the mutation", last)
	}
	input, _ := last.variables["input"].(map[string]any)
	return input
}

// Both tools satisfy their output contract through the application core once confirmed.
func TestCopilotToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	c := &fakeCopilot{copilotPage: 1}
	f, core, _ := copilotRig(t, c)

	result, err := invoke(t, core, "github.copilotassignments.create", "repo",
		`{"number":42,"base_ref":"main","custom_instructions":"Add tests."}`, true)
	if err != nil || !strings.Contains(string(result), `"number":42`) || !strings.Contains(string(result), `"is_suggestion":false`) {
		t.Fatalf("assign = %s, %v", result, err)
	}
	input := lastMutation(t, f)
	assignment, _ := input["agentAssignment"].(map[string]any)
	ids, _ := input["assigneeIds"].([]any)
	if len(ids) != 2 || ids[0] != "U_hubot" || ids[1] != "BOT_copilot" || assignment["baseRef"] != "main" ||
		assignment["customInstructions"] != "Add tests." || assignment["targetRepositoryId"] != "R_1" {
		t.Errorf("mutation input = %+v, want the kept assignee, Copilot, and the agent assignment", input)
	}
	if c.pages != 2 {
		t.Errorf("read %d actor pages, want 2 until Copilot is found", c.pages)
	}
	requests := f.recorded()
	if requests[len(requests)-1].path != "/api/graphql" {
		t.Errorf("last request = %+v", requests[len(requests)-1])
	}

	// With intent the object form is used, and a suggestion sends no agent assignment.
	c.pages = 0
	result, err = invoke(t, core, "github.copilotassignments.create", "repo",
		`{"number":42,"rationale":"Well scoped","confidence":"HIGH","is_suggestion":true}`, true)
	if err != nil || !strings.Contains(string(result), `"is_suggestion":true`) {
		t.Fatalf("suggest = %s, %v", result, err)
	}
	input = lastMutation(t, f)
	entries, _ := input["assignees"].([]any)
	entry, _ := entries[len(entries)-1].(map[string]any)
	if _, has := input["agentAssignment"]; has || input["assigneeIds"] != nil || len(entries) != 2 ||
		entry["actorId"] != "BOT_copilot" || entry["rationale"] != "Well scoped" || entry["confidence"] != "HIGH" ||
		entry["suggest"] != true {
		t.Errorf("suggestion input = %+v", input)
	}

	review, err := invoke(t, core, "github.copilotreviews.request", "repo", `{"number":7}`, true)
	if err != nil || !strings.Contains(string(review), "copilot-pull-request-reviewer[bot]") {
		t.Fatalf("review = %s, %v", review, err)
	}
	last := f.recorded()[len(f.recorded())-1]
	reviewers, _ := last.body["reviewers"].([]any)
	if last.path != "/api/v3/repos/octo-org/example/pulls/7/requested_reviewers" || len(reviewers) != 1 ||
		reviewers[0] != "copilot-pull-request-reviewer[bot]" {
		t.Errorf("review request = %+v", last)
	}
}

func TestCopilotChangesNeedConfirmation(t *testing.T) {
	f, core, _ := copilotRig(t, &fakeCopilot{})
	unconfirmed := &application.ConfirmationRequiredError{}
	for id, arguments := range map[string]string{"github.copilotassignments.create": `{"number":42}`,
		"github.copilotreviews.request": `{"number":7}`} {
		before := len(f.recorded())
		if _, err := invoke(t, core, id, "repo", arguments, false); !errors.As(err, &unconfirmed) {
			t.Errorf("%s without --confirm = %v, want confirmation-required", id, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s without --confirm reached GitHub", id)
		}
	}
}

// A connection without a repository target and invalid arguments are refused before any credential is read.
func TestCopilotRefuseTargetsAndArgumentsBeforeIO(t *testing.T) {
	f, core, reads := copilotRig(t, &fakeCopilot{})
	for _, tt := range []struct{ id, connection, arguments string }{
		{"github.copilotassignments.create", "project", `{"number":42}`},
		{"github.copilotreviews.request", "project", `{"number":7}`},
		{"github.copilotassignments.create", "repo", `{"number":42,"rationale":"only"}`},
		{"github.copilotassignments.create", "repo", `{"number":42,"confidence":"HIGH","rationale":"x","is_suggestion":true,"base_ref":"main"}`},
		{"github.copilotassignments.create", "repo", `{"number":42,"base_ref":"a..b"}`},
		{"github.copilotassignments.create", "repo", `{"number":0}`},
	} {
		if _, err := invoke(t, core, tt.id, tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s %s = %v, want an invalid request", tt.id, tt.arguments, err)
		}
	}
	if *reads != 0 || len(f.recorded()) != 0 {
		t.Error("a refused call reached the credential or GitHub")
	}
}

// A repository without Copilot among its assignable actors is a permission refusal with the reason, and the
// issue is not changed.
func TestCopilotAssignmentWithoutCopilotChangesNothing(t *testing.T) {
	c := &fakeCopilot{noCopilot: true}
	_, core, _ := copilotRig(t, c)
	_, err := invoke(t, core, "github.copilotassignments.create", "repo", `{"number":42}`, true)
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Class != provider.ClassPermission ||
		!strings.Contains(failure.Message, "Copilot license") || c.mutations != 0 {
		t.Errorf("err = %v, mutations = %d, want a permission refusal naming the license and no mutation", err, c.mutations)
	}

	_, err = invoke(t, core, "github.copilotassignments.create", "repo", `{"number":43}`, true)
	if !errors.As(err, &failure) || failure.Class != provider.ClassInvalidResponse && failure.Class != provider.ClassNotFound {
		t.Errorf("missing issue = %v", err)
	}
}

// A refused Copilot review request names its reason by status.
func TestCopilotReviewRefusals(t *testing.T) {
	for status, want := range map[int]string{http.StatusUnprocessableEntity: "Copilot license",
		http.StatusForbidden: "write access"} {
		_, core, _ := copilotRig(t, &fakeCopilot{reviewStatus: status})
		_, err := invoke(t, core, "github.copilotreviews.request", "repo", `{"number":7}`, true)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %d = %v, want a message naming %q", status, err, want)
		}
	}
}

func TestCopilotProfileIsNotRecommended(t *testing.T) {
	reg := registry(t)
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	var profile config.ToolProfile
	for _, candidate := range metadata.Profiles {
		if candidate.ID == "copilot" {
			profile = candidate
		}
	}
	if profile.ID == "" || profile.Recommended || strings.Join(profile.Tools, ",") != strings.Join(copilotTools, ",") {
		t.Errorf("profile = %+v", profile)
	}
}
