package github

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	repositoryRoute  = "/api/v3/repos/octo-org/example"
	repositoryAnswer = `{"id":1,"name":"example","full_name":"octo-org/example","description":"description-canary",` +
		`"default_branch":"main","visibility":"private","private":true,"archived":false,` +
		`"allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":false,"allow_auto_merge":false,` +
		`"allow_update_branch":true,"delete_branch_on_merge":false,"squash_merge_commit_title":"PR_TITLE",` +
		`"squash_merge_commit_message":"PR_BODY","merge_commit_title":"MERGE_MESSAGE","merge_commit_message":"BLANK",` +
		`"web_commit_signoff_required":false,"homepage":"homepage-canary","security_and_analysis":{"x":1}}`
)

// serveRepositorySettings answers the repository route with answer for a read and with reply for a change.
func serveRepositorySettings(t *testing.T, status int, reply string) (*fakeGitHub, *application.Core, *int) {
	t.Helper()
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != repositoryRoute {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(repositoryAnswer))
			return true
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
		return true
	}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	return f, application.New(registry(t), maintainConfig(base), resolver(red, &reads), red), &reads
}

func TestRepositorySettingsGetAnswersOnlyTheFieldList(t *testing.T) {
	_, core, _ := serveRepositorySettings(t, http.StatusOK, "")
	result, err := invoke(t, core, repositorySettingsGet.ID, "admin", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"description-canary", "homepage-canary", "security_and_analysis", "full_name"} {
		if strings.Contains(string(result), canary) {
			t.Errorf("result %s carries %s", result, canary)
		}
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	// The core names the repository in every result.
	delete(got, "repository")
	keys := make([]string, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{"allow_auto_merge", "allow_merge_commit", "allow_rebase_merge", "allow_squash_merge",
		"allow_update_branch", "archived", "default_branch", "delete_branch_on_merge", "merge_commit_message",
		"merge_commit_title", "squash_merge_commit_message", "squash_merge_commit_title", "visibility",
		"web_commit_signoff_required"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("fields = %v, want %v", keys, want)
	}
	if got["allow_rebase_merge"] != false || got["default_branch"] != "main" {
		t.Errorf("result = %s", result)
	}
}

func TestRepositorySettingsUpdateSendsOnlyTheGivenFieldsOnce(t *testing.T) {
	f, core, _ := serveRepositorySettings(t, http.StatusOK, repositoryAnswer)
	result, err := invoke(t, core, repositorySettingsUpdate.ID, "admin",
		`{"allow_rebase_merge":false,"delete_branch_on_merge":true,"merge_commit_title":"PR_TITLE"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	requests := f.recorded()
	if len(requests) != 1 || requests[0].method != http.MethodPatch || requests[0].path != repositoryRoute {
		t.Fatalf("requests = %+v, want one PATCH", requests)
	}
	want := map[string]any{"allow_rebase_merge": false, "delete_branch_on_merge": true, "merge_commit_title": "PR_TITLE"}
	if !reflect.DeepEqual(requests[0].body, want) {
		t.Errorf("body = %v, want %v", requests[0].body, want)
	}
	// The result names the given settings with the values GitHub reports, and nothing else.
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	delete(got, "repository")
	if wantResult := (map[string]any{"allow_rebase_merge": false, "delete_branch_on_merge": false,
		"merge_commit_title": "MERGE_MESSAGE"}); !reflect.DeepEqual(got, wantResult) {
		t.Errorf("result = %v, want %v", got, wantResult)
	}
}

func TestRepositorySettingsUpdateIsBoundedBeforeIO(t *testing.T) {
	f, core, reads := serveRepositorySettings(t, http.StatusOK, repositoryAnswer)
	for name, arguments := range map[string]string{
		"empty":                    `{}`,
		"all merge methods off":    `{"allow_merge_commit":false,"allow_squash_merge":false,"allow_rebase_merge":false}`,
		"an unknown squash title":  `{"squash_merge_commit_title":"TITLE"}`,
		"a squash title as merge":  `{"merge_commit_title":"COMMIT_OR_PR_TITLE"}`,
		"an unknown squash text":   `{"squash_merge_commit_message":"PR_TITLE"}`,
		"an unknown merge message": `{"merge_commit_message":"COMMIT_MESSAGES"}`,
		"a free field":             `{"allow_squash_merge":true,"name":"renamed"}`,
		"visibility":               `{"visibility":"public"}`,
		"archiving":                `{"archived":true}`,
		"the default branch":       `{"default_branch":"dev"}`,
		"a feature switch":         `{"has_issues":false}`,
	} {
		*reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", name, err)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s: secret reads = %d, requests = %d; want none", name, *reads, len(f.recorded())-before)
		}
	}
	// The handler applies the same checks when the schema is not in front of it.
	_, handler, _ := registry(t).Lookup(repositorySettingsUpdate.ID)
	for _, arguments := range []string{`{}`, `{"allow_merge_commit":false,"allow_squash_merge":false,` +
		`"allow_rebase_merge":false}`, `{"merge_commit_message":"x"}`} {
		*reads = 0
		if _, err := handler(t.Context(), resolvedConnection("admin", "http://127.0.0.1:1", repoTarget), nil, nil,
			[]byte(arguments)); !isInvalidRequest(err) || *reads != 0 {
			t.Errorf("handler %s = %v", arguments, err)
		}
	}
	// Switching off two methods is fine.
	if _, err := invoke(t, core, repositorySettingsUpdate.ID, "admin",
		`{"allow_merge_commit":false,"allow_squash_merge":false}`, true); err != nil {
		t.Errorf("two methods off = %v", err)
	}
}

func TestRepositorySettingsRefuseAForeignRepositoryBeforeIO(t *testing.T) {
	f, core, reads := serveRepositorySettings(t, http.StatusOK, repositoryAnswer)
	for _, id := range []string{repositorySettingsGet.ID, repositorySettingsUpdate.ID} {
		arguments := `{"repository":"other/secret-repo"}`
		if id == repositorySettingsUpdate.ID {
			arguments = `{"repository":"other/secret-repo","allow_squash_merge":true}`
		}
		_, err := invoke(t, core, id, "admin", arguments, true)
		if err == nil {
			t.Errorf("%s accepted a foreign repository", id)
			continue
		}
		if strings.Contains(err.Error(), "secret-repo") {
			t.Errorf("%s names the foreign target: %v", id, err)
		}
	}
	if *reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("secret reads = %d, requests = %d; want none", *reads, len(f.recorded()))
	}
}

func TestRepositorySettingsUpdateIsNeverRepeated(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		reply  string
	}{
		"a server error":              {http.StatusBadGateway, `{"message":"provider-text-canary"}`},
		"an unreadable reply":         {http.StatusOK, `not json`},
		"a reply without the setting": {http.StatusOK, `{"default_branch":"main","visibility":"private"}`},
	} {
		t.Run(name, func(t *testing.T) {
			f, core, _ := serveRepositorySettings(t, tt.status, tt.reply)
			_, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", `{"allow_squash_merge":true}`, true)
			if err == nil || !strings.Contains(err.Error(), uncertain) || strings.Contains(err.Error(), "canary") {
				t.Errorf("err = %v, want the uncertainty and no provider text", err)
			}
			if got := len(f.recorded()); got != 1 {
				t.Errorf("requests = %d, want exactly one", got)
			}
		})
	}
}

func TestRepositorySettingsNameThePermissionOnRefusal(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		_, core, _ := serveRepositorySettings(t, status, `{"message":"provider-text-canary"}`)
		_, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", `{"allow_squash_merge":true}`, true)
		if err == nil || !strings.Contains(err.Error(), "Administration: write") || strings.Contains(err.Error(), "canary") {
			t.Errorf("status %d: err = %v, want the permission hint", status, err)
		}
	}
}

func TestRepositorySettingsAreReachedOnlyThroughTheToolsList(t *testing.T) {
	f, core, reads := serveRepositorySettings(t, http.StatusOK, repositoryAnswer)
	for _, connection := range []string{"reader", "planner", "observer", "listed", "operator", "open", "maintainer"} {
		for _, id := range []string{repositorySettingsGet.ID, repositorySettingsUpdate.ID} {
			if _, err := invoke(t, core, id, connection, validArguments(id), true); err == nil {
				t.Errorf("%s ran on %s", id, connection)
			}
		}
	}
	if *reads != 0 || len(f.recorded()) != 0 {
		t.Errorf("secret reads = %d, requests = %d; want none", *reads, len(f.recorded()))
	}
}
