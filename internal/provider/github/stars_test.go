package github

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func TestStarsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{
		starList: []fakeStar{
			{fullName: "octo-org/example", owner: "octo-org", visibility: "public"},
			{fullName: "octocat/hello-world", owner: "octocat", visibility: "public", archived: true},
		},
	}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, starsList.ID, "open", `{}`, false)
	if err != nil || !strings.Contains(string(result), `"repository":"octo-org/example"`) ||
		!strings.Contains(string(result), `"repository":"octocat/hello-world"`) ||
		!strings.Contains(string(result), `"archived":true`) {
		t.Fatalf("%s = %s, %v", starsList.ID, result, err)
	}

	// A connection whose targets name only an owner sees only the starred repositories of that owner.
	result, err = invoke(t, core, starsList.ID, "org", `{}`, false)
	if err != nil || !strings.Contains(string(result), `"repository":"octo-org/example"`) ||
		strings.Contains(string(result), "hello-world") {
		t.Fatalf("%s on an owner-scoped connection = %s, %v", starsList.ID, result, err)
	}

	// Starring an unstarred repository sends the change once and reports it.
	result, err = invoke(t, core, starsAdd.ID, "starrepo", `{"repository":"octo-org/example"}`, true)
	if err != nil || !strings.Contains(string(result), `"starred":true`) || !strings.Contains(string(result), `"changed":true`) {
		t.Fatalf("%s = %s, %v", starsAdd.ID, result, err)
	}
	before := len(f.recorded())
	// Starring it again reads the state, finds it already starred, and sends nothing further.
	result, err = invoke(t, core, starsAdd.ID, "starrepo", `{"repository":"octo-org/example"}`, true)
	if err != nil || !strings.Contains(string(result), `"starred":true`) || !strings.Contains(string(result), `"changed":false`) {
		t.Fatalf("an idempotent %s = %s, %v", starsAdd.ID, result, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 || requests[0].method != http.MethodGet {
		t.Errorf("an already-starred repository sent %+v, want only the state read", requests)
	}

	// Unstarring it sends the change once and reports it.
	result, err = invoke(t, core, starsRemove.ID, "starrepo", `{"repository":"octo-org/example"}`, true)
	if err != nil || !strings.Contains(string(result), `"starred":false`) || !strings.Contains(string(result), `"changed":true`) {
		t.Fatalf("%s = %s, %v", starsRemove.ID, result, err)
	}
	before = len(f.recorded())
	// Unstarring it again finds it already unstarred and sends nothing further.
	result, err = invoke(t, core, starsRemove.ID, "starrepo", `{"repository":"octo-org/example"}`, true)
	if err != nil || !strings.Contains(string(result), `"changed":false`) {
		t.Fatalf("an idempotent %s = %s, %v", starsRemove.ID, result, err)
	}
	if requests := f.recorded()[before:]; len(requests) != 1 || requests[0].method != http.MethodGet {
		t.Errorf("an already-unstarred repository sent %+v, want only the state read", requests)
	}
}

// A star or an unstar without --confirm is refused before GitHub is contacted.
func TestStarChangesNeedConfirmation(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(base), resolver(red, nil), red)

	for _, operation := range []string{starsAdd.ID, starsRemove.ID} {
		before := len(f.recorded())
		_, err := invoke(t, core, operation, "starrepo", `{"repository":"octo-org/example"}`, false)
		if want := fmt.Sprintf("%T", &application.ConfirmationRequiredError{}); fmt.Sprintf("%T", err) != want {
			t.Errorf("%s without --confirm = %T %v, want %s", operation, err, err, want)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s without --confirm reached GitHub", operation)
		}
	}
}

// A connection whose targets name a repository or a project may not list the account's stars as a whole;
// a star or an unstar still checks its repository against the targets before a credential is resolved.
func TestStarRefusalsBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), discoveryConfig(serve(t, f)), resolver(red, &reads), red)

	for _, connection := range []string{"repo", "planning"} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, starsList.ID, connection, `{}`, false); !isInvalidRequest(err) ||
			!strings.Contains(err.Error(), "name a repository or a project") {
			t.Errorf("%s on %s = %v, want an invalid request", starsList.ID, connection, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s on %s reached the credential or GitHub", starsList.ID, connection)
		}
	}

	reads = 0
	before := len(f.recorded())
	if _, err := invoke(t, core, starsAdd.ID, "starrepo", `{"repository":"hubot/example"}`, true); !isInvalidRequest(err) {
		t.Errorf("%s outside the targets = %v, want an invalid request", starsAdd.ID, err)
	}
	if reads != 0 || len(f.recorded()) != before {
		t.Errorf("%s outside the targets reached the credential or GitHub", starsAdd.ID)
	}
}
