package forge

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	repoLabelsPath = "/api/v1/repos/acme/widget/labels"
	orgLabelsPath  = "/api/v1/orgs/acme/labels"
	issueLabels    = "/api/v1/repos/acme/widget/issues/7/labels"
)

// issueLabelServer answers the evidence read, the label sources (repository label 3 and 4, organization label 30),
// and the change.
func issueLabelServer(p *product, change func(call) *http.Response) func(call) *http.Response {
	return issueServer(p, "open", func(c call) *http.Response {
		switch {
		case c.path == repoLabelsPath && c.method == http.MethodGet:
			return jsonResponse(200, `[{"id":3,"name":"bug"},{"id":4,"name":"p1"}]`)
		case c.path == orgLabelsPath && c.method == http.MethodGet:
			return jsonResponse(200, `[{"id":30,"name":"org-wide"}]`)
		case c.path == "/api/v1/repos/acme/widget/issues/8/labels" && c.method == http.MethodGet:
			return jsonResponse(200, `[{"id":3,"name":"bug","color":"ff0000","exclusive":false}]`)
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	})
}

func TestIssueLabelsListIsBoundToTheRepositoryPath(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueLabelServer(p, nil))
		result, err := e.invoke(t, issueLabelsList, "repo", `{"number":8}`)
		labels, _ := result["labels"].([]any)
		if err != nil || len(labels) != 1 || labels[0].(map[string]any)["name"] != "bug" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/8/labels"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		_, err = e.invoke(t, issueLabelsList, "repo", `{"repository":"secret-owner/hidden","number":8}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || len(*e.calls) != 1 {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestIssueLabelsAddAndReplaceProveTheIDsFirst(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{issueLabelsAdd, issueLabelsReplace} {
			method := http.MethodPost
			if tl == issueLabelsReplace {
				method = http.MethodPut
			}
			e := newEnvironment(t, p, issueLabelServer(p, func(c call) *http.Response {
				if c.method == method && c.path == issueLabels {
					return jsonResponse(200, `[{"id":3,"name":"bug"},{"id":30,"name":"org-wide"}]`)
				}
				return notFoundResponse()
			}))
			arguments := `{"number":7,"labels":[3,30]`
			want := map[string]any{"labels": []any{float64(3), float64(30)}}
			if p == forgejo {
				arguments += `,"updated_at":"2030-01-02T03:04:05Z"`
				want["updated_at"] = "2030-01-02T03:04:05Z"
			}
			if _, err := e.invokeAs(t, tl, "repo", arguments+`}`, false); err == nil ||
				!errors.As(err, new(*application.ConfirmationRequiredError)) || len(*e.calls) != 0 {
				t.Fatalf("%s %s: unconfirmed: %v, calls %d", p.id, tl.action, err, len(*e.calls))
			}
			result, err := e.invoke(t, tl, "repo", arguments+`}`)
			if labels, _ := result["labels"].([]any); err != nil || len(labels) != 2 {
				t.Fatalf("%s %s: %v, %v", p.id, tl.action, result, err)
			}
			wantCalls := []string{"GET /api/v1/repos/acme/widget/issues/7", "GET " + repoLabelsPath,
				"GET " + orgLabelsPath, method + " " + issueLabels}
			if got := methods(e); !reflect.DeepEqual(got, wantCalls) {
				t.Fatalf("%s %s calls = %v", p.id, tl.action, got)
			}
			sent := (*e.calls)[3]
			if sent.contentType != "application/json" || !reflect.DeepEqual(bodyOf(t, sent), want) {
				t.Errorf("%s %s sent %+v", p.id, tl.action, sent)
			}
			// A label of another repository or organization stops the change before it is sent.
			before := len(*e.calls)
			_, err = e.invoke(t, tl, "repo", `{"number":7,"labels":[3,99]}`)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "99") {
				t.Errorf("%s %s: foreign label: %v", p.id, tl.action, err)
			}
			for _, c := range (*e.calls)[before:] {
				if c.method == method {
					t.Errorf("%s %s sent the change for a foreign label", p.id, tl.action)
				}
			}
		}
	}
}

func TestIssueLabelsAcceptAPullRequestAndRefuseOtherRepositories(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueLabelServer(p, func(c call) *http.Response {
			if c.method == http.MethodPost && strings.HasSuffix(c.path, "/issues/8/labels") {
				return jsonResponse(200, `[{"id":3,"name":"bug"}]`)
			}
			return notFoundResponse()
		}))
		if _, err := e.invoke(t, issueLabelsAdd, "repo", `{"number":8,"labels":[3]}`); err != nil {
			t.Errorf("%s: a pull request index: %v", p.id, err)
		}
		// The canonical repository of the evidence read lies outside the targets.
		before := len(*e.calls)
		_, err := e.invoke(t, issueLabelsAdd, "repo", `{"number":9,"labels":[3]}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "other/place") || len(*e.calls) != before+1 {
			t.Errorf("%s: %v, calls %v", p.id, err, methods(e))
		}
	}
}

func TestIssueLabelsRemoveAndClear(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueLabelServer(p, func(c call) *http.Response {
			if c.method == http.MethodDelete {
				return jsonResponse(204, ``)
			}
			return notFoundResponse()
		}))
		if _, err := e.invokeAs(t, issueLabelsRemove, "repo", `{"number":7,"label":3}`, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed remove: %v", p.id, err)
		}
		result, err := e.invoke(t, issueLabelsRemove, "repo", `{"number":7,"label":3}`)
		if err != nil || result["removed"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
			"GET " + repoLabelsPath, "DELETE " + issueLabels + "/3"}) {
			t.Errorf("%s remove calls = %v", p.id, got)
		}
		before := len(*e.calls)
		if _, err := e.invoke(t, issueLabelsRemove, "repo", `{"number":7,"label":99}`); !isInvalidRequest(err) {
			t.Errorf("%s: foreign label: %v", p.id, err)
		}
		for _, c := range (*e.calls)[before:] {
			if c.method == http.MethodDelete {
				t.Errorf("%s removed a foreign label", p.id)
			}
		}
		if _, err := e.invokeAs(t, issueLabelsClear, "repo", `{"number":7}`, false); err == nil {
			t.Fatalf("%s: unconfirmed clear", p.id)
		}
		before = len(*e.calls)
		result, err = e.invoke(t, issueLabelsClear, "repo", `{"number":7}`)
		if err != nil || result["cleared"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e)[before:]; !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
			"DELETE " + issueLabels}) || (*e.calls)[before+1].body != "" {
			t.Errorf("%s clear calls = %v", p.id, got)
		}
	}
}

func TestIssueLabelsRefuseBadArgumentsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueLabelServer(p, nil))
		bad := []string{`{"number":7}`, `{"number":7,"labels":[]}`, `{"number":7,"labels":[0]}`,
			`{"number":7,"labels":["bug"]}`, `{"number":7,"labels":[1],"sudo":"x"}`, `{"labels":[1]}`}
		for _, tl := range []*tool{issueLabelsAdd, issueLabelsReplace} {
			for _, arguments := range bad {
				if tl == issueLabelsReplace && arguments == `{"number":7,"labels":[]}` {
					continue
				}
				if _, err := e.invoke(t, tl, "repo", arguments); !isInvalidRequest(err) {
					t.Errorf("%s %s %s: %v", p.id, tl.action, arguments, err)
				}
			}
		}
		if p == gitea {
			if _, err := e.invoke(t, issueLabelsAdd, "repo",
				`{"number":7,"labels":[3],"updated_at":"2030-01-02T03:04:05Z"}`); !isInvalidRequest(err) {
				t.Errorf("updated_at is Forgejo only: %v", err)
			}
		}
		for _, arguments := range []string{`{"number":7}`, `{"number":7,"label":0}`, `{"number":7,"label":"bug"}`} {
			if _, err := e.invoke(t, issueLabelsRemove, "repo", arguments); !isInvalidRequest(err) {
				t.Errorf("%s remove %s: %v", p.id, arguments, err)
			}
		}
		if _, err := e.invoke(t, issueLabelsClear, "open", `{"number":7}`); !isInvalidRequest(err) {
			t.Errorf("%s: repository required: %v", p.id, err)
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: requests %v, secret reads %d", p.id, methods(e), *e.reads)
		}
	}
}

func TestIssueLabelsReplaceWithAnEmptyListSendsPut(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueLabelServer(p, func(c call) *http.Response {
			if c.method == http.MethodPut {
				return jsonResponse(200, `[]`)
			}
			return notFoundResponse()
		}))
		result, err := e.invoke(t, issueLabelsReplace, "repo", `{"number":7,"labels":[]}`)
		// The core leaves an empty member out of the result.
		if labels, _ := result["labels"].([]any); err != nil || len(labels) != 0 {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
			"PUT " + issueLabels}) || (*e.calls)[1].body != `{"labels":[]}` {
			t.Errorf("%s calls = %v %+v", p.id, got, *e.calls)
		}
	}
}

func TestLabelProofIgnoresAnOwnerThatIsNoOrganization(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueServer(p, "open", func(c call) *http.Response {
			if c.path == repoLabelsPath {
				return jsonResponse(200, `[{"id":3}]`)
			}
			return notFoundResponse() // the owner has no organization labels
		}))
		_, err := e.invoke(t, issueLabelsAdd, "repo", `{"number":7,"labels":[30]}`)
		var failure *provider.Error
		if !isInvalidRequest(err) || errors.As(err, &failure) {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}
