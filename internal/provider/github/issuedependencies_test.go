package github

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// Every issue dependency tool satisfies its output contract through the application core once confirmed:
// listing both directions, adding, and removing, in the bound repository and, for add, in another repository
// the connection's targets allow as well. The fake router, repository index, and connection config are
// shared with the sub-issue tests, since both groups address the same fake issues.
func TestIssueDependenciesToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, g, base := serveIssueGraph(t)
	g.blockedBy[issueKey("octo-org/example", 42)] = []int64{fakeIssueID("octo-org/example", 40)}
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)

	listed, err := invoke(t, core, issueDependenciesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil || !strings.Contains(string(listed), `"blocked_by":[{`) || !strings.Contains(string(listed), `"number":40`) ||
		strings.Contains(string(listed), `"blocking"`) {
		t.Fatalf("list = %s, %v", listed, err)
	}

	// Issue 42 blocks issue 41, so listing 41's dependencies reports it under blocking.
	g.blockedBy[issueKey("octo-org/example", 41)] = []int64{fakeIssueID("octo-org/example", 42)}
	blocking, err := invoke(t, core, issueDependenciesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil || !strings.Contains(string(blocking), `"blocking":[{"number":41`) {
		t.Fatalf("list blocking = %s, %v", blocking, err)
	}

	added, err := invoke(t, core, issueDependenciesAdd.ID, "repo",
		`{"repository":"octo-org/example","number":50,"blocking_issue_number":40}`, true)
	if err != nil || !strings.Contains(string(added), `"number":40`) {
		t.Fatalf("add = %s, %v", added, err)
	}
	if last := f.recorded()[len(f.recorded())-1]; last.method != http.MethodPost ||
		!strings.HasSuffix(last.path, "/issues/50/dependencies/blocked_by") ||
		last.body["issue_id"] != float64(fakeIssueID("octo-org/example", 40)) {
		t.Errorf("add request = %+v", last)
	}

	// A blocking issue in another repository the connection's targets allow is added the same way, and its
	// repository is reported because it differs from the blocked issue's.
	addedOther, err := invoke(t, core, issueDependenciesAdd.ID, "repo",
		`{"repository":"octo-org/example","number":50,"blocking_issue_number":9,"blocking_issue_repository":"octo-org/other"}`,
		true)
	if err != nil || !strings.Contains(string(addedOther), `"repository":"octo-org/other"`) {
		t.Fatalf("cross-repository add = %s, %v", addedOther, err)
	}

	removed, err := invoke(t, core, issueDependenciesRemove.ID, "repo",
		`{"repository":"octo-org/example","number":50,"blocking_issue_number":40}`, true)
	if err != nil || !strings.Contains(string(removed), `"removed":true`) {
		t.Fatalf("remove = %s, %v", removed, err)
	}
	if last := f.recorded()[len(f.recorded())-1]; last.method != http.MethodDelete ||
		!strings.HasSuffix(last.path, "/issues/50/dependencies/blocked_by/"+
			strconv.FormatInt(fakeIssueID("octo-org/example", 40), 10)) {
		t.Errorf("remove request = %+v", last)
	}
}

// A dependency whose repository lies outside the connection's targets is withheld from both blocked_by and
// blocking and only counted in withheld, while a dependency in the bound repository or in another repository
// the targets allow is still listed; a connection without targets withholds nothing.
func TestIssueDependenciesListWithholdsDependenciesOutsideTheTargets(t *testing.T) {
	_, g, base := serveIssueGraph(t)
	g.blockedBy[issueKey("octo-org/example", 42)] = []int64{
		fakeIssueID("octo-org/example", 40),
		fakeIssueID("octo-org/other", 8),
		fakeIssueID("octo-org/blocked", 88),
	}
	g.blockedBy[issueKey("octo-org/example", 41)] = []int64{fakeIssueID("octo-org/blocked", 89)}
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)

	listed, err := invoke(t, core, issueDependenciesList.ID, "repo", `{"repository":"octo-org/example","number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(listed), `"number":40`) || !strings.Contains(string(listed), `"repository":"octo-org/other"`) {
		t.Fatalf("list = %s, want the bound and the allowed cross-repository dependency", listed)
	}
	if strings.Contains(string(listed), `"octo-org/blocked"`) {
		t.Fatalf("list = %s, leaked a dependency outside the targets", listed)
	}
	if !strings.Contains(string(listed), `"withheld":1`) {
		t.Fatalf("list = %s, want withheld:1", listed)
	}

	// Issue 42 blocks issue 41, whose only blocker lies outside the targets; that withheld entry is counted
	// too, across both directions.
	blocking, err := invoke(t, core, issueDependenciesList.ID, "repo", `{"repository":"octo-org/example","number":41}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blocking), `"octo-org/blocked"`) {
		t.Fatalf("list = %s, leaked a dependency outside the targets", blocking)
	}
	if !strings.Contains(string(blocking), `"withheld":1`) {
		t.Fatalf("list = %s, want withheld:1", blocking)
	}

	unrestricted := coreConfig(base)
	unrestricted.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader"}
	coreUnrestricted := application.New(registry(t), unrestricted, resolver(red, nil), red)
	all, err := invoke(t, coreUnrestricted, issueDependenciesList.ID, "repo",
		`{"repository":"octo-org/example","number":42}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(all), `"octo-org/blocked"`) || strings.Contains(string(all), `"withheld"`) {
		t.Fatalf("unrestricted list = %s, want the withheld dependency included and no withheld field", all)
	}
}

// A blocking issue in a repository the connection's targets do not name is refused before any credential is
// resolved.
func TestIssueDependenciesRefuseARepositoryOutsideTheTargetsBeforeIO(t *testing.T) {
	f, _, base := serveIssueGraph(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, &reads), red)

	before := len(f.recorded())
	_, err := invoke(t, core, issueDependenciesAdd.ID, "repo",
		`{"repository":"octo-org/example","number":42,"blocking_issue_number":1,"blocking_issue_repository":"octo-org/blocked"}`,
		true)
	if !isInvalidRequest(err) {
		t.Errorf("add outside the targets = %v, want an invalid request", err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("add outside the targets reached the credential or GitHub")
	}
}

// Every check of the issue dependency tools runs before a credential is resolved.
func TestIssueDependenciesArgumentsAreValidatedBeforeIO(t *testing.T) {
	f, _, base := serveIssueGraph(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, id, arguments string
	}{
		{"missing number", issueDependenciesList.ID, `{"repository":"octo-org/example"}`},
		{"missing blocking_issue_number", issueDependenciesAdd.ID, `{"repository":"octo-org/example","number":42}`},
		{"malformed blocking_issue_repository", issueDependenciesAdd.ID,
			`{"repository":"octo-org/example","number":42,"blocking_issue_number":1,` +
				`"blocking_issue_repository":"not-a-repo"}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, "repo", tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s: %s(%s) = %v, want an invalid request", tt.name, tt.id, tt.arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s: %s reached the credential or GitHub", tt.name, tt.id)
		}
	}
}

// A change of an issue dependency needs its own confirmation and is refused without one, before it reaches
// GitHub.
func TestIssueDependenciesChangesNeedConfirmation(t *testing.T) {
	f, _, base := serveIssueGraph(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, nil), red)

	unconfirmed := &application.ConfirmationRequiredError{}
	for _, tt := range []struct{ id, arguments string }{
		{issueDependenciesAdd.ID, `{"repository":"octo-org/example","number":42,"blocking_issue_number":1}`},
		{issueDependenciesRemove.ID, `{"repository":"octo-org/example","number":42,"blocking_issue_number":1}`},
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, "repo", tt.arguments, false); !errors.As(err, &unconfirmed) {
			t.Errorf("%s without --confirm = %v, want confirmation-required", tt.id, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s without --confirm reached GitHub", tt.id)
		}
	}
}

// A connection whose targets name a project, not a repository, never resolves a credential for an issue
// dependency tool.
func TestIssueDependenciesRefuseANonRepositoryTargetBeforeIO(t *testing.T) {
	f, _, base := serveIssueGraph(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), issueGraphConfig(base), resolver(red, &reads), red)

	before := len(f.recorded())
	if _, err := invoke(t, core, issueDependenciesList.ID, "planning", `{"number":42}`, false); !isInvalidRequest(err) {
		t.Errorf("list on a project-scoped connection = %v, want an invalid request", err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Error("list on a project-scoped connection reached the credential or GitHub")
	}
}
