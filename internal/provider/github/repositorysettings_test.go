package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

const (
	repositoryRoute  = "/api/v3/repos/octo-org/example"
	repositoryAnswer = `{"id":1,"name":"example","full_name":"octo-org/example","description":"description-canary",` +
		`"default_branch":"main","visibility":"private","private":true,"archived":false,` +
		`"allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":false,"allow_auto_merge":false,` +
		`"allow_update_branch":true,"delete_branch_on_merge":false,"squash_merge_commit_title":"PR_TITLE",` +
		`"squash_merge_commit_message":"PR_BODY","merge_commit_title":"MERGE_MESSAGE","merge_commit_message":"BLANK",` +
		`"web_commit_signoff_required":false,"homepage":"homepage-canary","topics":["go","cli"],"has_issues":true,` +
		`"has_projects":false,"has_wiki":true,"has_discussions":false,"is_template":false,"allow_forking":true,` +
		`"security_and_analysis":{"x":1}}`
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
	for _, canary := range []string{"security_and_analysis", "full_name"} {
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
	want := []string{"allow_auto_merge", "allow_forking", "allow_merge_commit", "allow_rebase_merge",
		"allow_squash_merge", "allow_update_branch", "archived", "default_branch", "delete_branch_on_merge",
		"description", "has_discussions", "has_issues", "has_projects", "has_wiki", "homepage", "is_template",
		"merge_commit_message", "merge_commit_title", "squash_merge_commit_message", "squash_merge_commit_title",
		"topics", "visibility", "web_commit_signoff_required"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("fields = %v, want %v", keys, want)
	}
	if got["allow_rebase_merge"] != false || got["default_branch"] != "main" || got["description"] != "description-canary" ||
		got["homepage"] != "homepage-canary" || !reflect.DeepEqual(got["topics"], []any{"go", "cli"}) ||
		got["has_wiki"] != true || got["has_projects"] != false || got["allow_forking"] != true {
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
		"empty":                     `{}`,
		"all merge methods off":     `{"allow_merge_commit":false,"allow_squash_merge":false,"allow_rebase_merge":false}`,
		"an unknown squash title":   `{"squash_merge_commit_title":"TITLE"}`,
		"a squash title as merge":   `{"merge_commit_title":"COMMIT_OR_PR_TITLE"}`,
		"an unknown squash text":    `{"squash_merge_commit_message":"PR_TITLE"}`,
		"an unknown merge message":  `{"merge_commit_message":"COMMIT_MESSAGES"}`,
		"a free field":              `{"allow_squash_merge":true,"name":"renamed"}`,
		"visibility":                `{"visibility":"public"}`,
		"an invalid default branch": `{"default_branch":"a..b"}`,
		"a branch with a space":     `{"default_branch":"a b"}`,
		"a text with a newline":     `{"description":"a\nb"}`,
		"a long homepage":           `{"homepage":"` + strings.Repeat("a", 2049) + `"}`,
		"a topic":                   `{"topics":["x"]}`,
		"a wrong type":              `{"has_issues":"yes"}`,
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
		"a gateway timeout":           {http.StatusGatewayTimeout, `{"message":"provider-text-canary"}`},
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

func TestRepositorySettingsUpdateSendsEachNewFieldAlone(t *testing.T) {
	for name, tt := range map[string]struct {
		arguments string
		want      map[string]any
	}{
		"description":     {`{"description":"new text"}`, map[string]any{"description": "new text"}},
		"homepage":        {`{"homepage":"https://example.org"}`, map[string]any{"homepage": "https://example.org"}},
		"has_issues":      {`{"has_issues":false}`, map[string]any{"has_issues": false}},
		"has_projects":    {`{"has_projects":true}`, map[string]any{"has_projects": true}},
		"has_wiki":        {`{"has_wiki":false}`, map[string]any{"has_wiki": false}},
		"has_discussions": {`{"has_discussions":true}`, map[string]any{"has_discussions": true}},
		"is_template":     {`{"is_template":true}`, map[string]any{"is_template": true}},
		"allow_forking":   {`{"allow_forking":false}`, map[string]any{"allow_forking": false}},
		"default_branch":  {`{"default_branch":"release/1.x"}`, map[string]any{"default_branch": "release/1.x"}},
		"archived":        {`{"archived":true}`, map[string]any{"archived": true}},
		"unarchived":      {`{"archived":false}`, map[string]any{"archived": false}},
	} {
		t.Run(name, func(t *testing.T) {
			reply := `{"default_branch":"release/1.x","visibility":"private","archived":true,"description":"new text",` +
				`"homepage":"https://example.org","has_issues":false,"has_projects":true,"has_wiki":false,` +
				`"has_discussions":true,"is_template":true,"allow_forking":false}`
			if name == "unarchived" {
				reply = `{"default_branch":"main","visibility":"private","archived":false}`
			}
			f, core, _ := serveRepositorySettings(t, http.StatusOK, reply)
			result, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", tt.arguments, true)
			if err != nil {
				t.Fatal(err)
			}
			requests := f.recorded()
			if len(requests) != 1 || requests[0].method != http.MethodPatch || !reflect.DeepEqual(requests[0].body, tt.want) {
				t.Errorf("requests = %+v, want one PATCH with %v", requests, tt.want)
			}
			var got map[string]any
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatal(err)
			}
			delete(got, "repository")
			if name == "unarchived" {
				tt.want = map[string]any{"archived": false}
			}
			if len(got) != 1 {
				t.Errorf("result = %v, want only the given field", got)
			}
			for key := range tt.want {
				if _, ok := got[key]; !ok {
					t.Errorf("result = %v lacks %s", got, key)
				}
			}
		})
	}
}

func TestRepositorySettingsUpdateSendsAllNewFieldsTogether(t *testing.T) {
	reply := `{"default_branch":"dev","visibility":"private","archived":false,"description":"d","homepage":"h",` +
		`"has_issues":true,"has_projects":true,"has_wiki":true,"has_discussions":true,"is_template":true,` +
		`"allow_forking":true,"allow_squash_merge":true}`
	f, core, _ := serveRepositorySettings(t, http.StatusOK, reply)
	arguments := `{"description":"d","homepage":"h","has_issues":true,"has_projects":true,"has_wiki":true,` +
		`"has_discussions":true,"is_template":true,"allow_forking":true,"default_branch":"dev","archived":false,` +
		`"allow_squash_merge":true}`
	result, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", arguments, true)
	if err != nil {
		t.Fatal(err)
	}
	requests := f.recorded()
	want := map[string]any{"description": "d", "homepage": "h", "has_issues": true, "has_projects": true,
		"has_wiki": true, "has_discussions": true, "is_template": true, "allow_forking": true,
		"default_branch": "dev", "archived": false, "allow_squash_merge": true}
	if len(requests) != 1 || !reflect.DeepEqual(requests[0].body, want) {
		t.Fatalf("requests = %+v, want one PATCH with %v", requests, want)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	delete(got, "repository")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("result = %v, want %v", got, want)
	}
}

func TestRepositorySettingsUpdateClearsTexts(t *testing.T) {
	f, core, _ := serveRepositorySettings(t, http.StatusOK,
		`{"default_branch":"main","visibility":"private","description":null,"homepage":null}`)
	result, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", `{"description":"","homepage":""}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]any{"description": "", "homepage": ""}); !reflect.DeepEqual(f.recorded()[0].body, want) {
		t.Errorf("body = %v, want %v", f.recorded()[0].body, want)
	}
	if !strings.Contains(string(result), `"description":""`) || !strings.Contains(string(result), `"homepage":""`) {
		t.Errorf("result = %s", result)
	}
}

func TestRepositorySettingsUpdateNeedsTheGivenFieldInTheAnswer(t *testing.T) {
	for name, arguments := range map[string]string{
		"description": `{"description":"x"}`, "has_wiki": `{"has_wiki":true}`, "archived": `{"archived":true}`,
	} {
		f, core, _ := serveRepositorySettings(t, http.StatusOK, `{"default_branch":"main","visibility":"private"}`)
		_, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", arguments, true)
		if err == nil || !strings.Contains(err.Error(), uncertain) || len(f.recorded()) != 1 {
			t.Errorf("%s: err = %v, requests = %d", name, err, len(f.recorded()))
		}
	}
}

func TestRepositorySettingsUpdateMapsRefusalsWithoutProviderText(t *testing.T) {
	_, core, _ := serveRepositorySettings(t, http.StatusUnprocessableEntity, `{"message":"provider-text-canary"}`)
	_, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", `{"default_branch":"missing"}`, true)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "canary") {
		t.Errorf("422 = %v, want an invalid request without provider text", err)
	}
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		_, core, _ := serveRepositorySettings(t, status, `{"message":"provider-text-canary"}`)
		_, err := invoke(t, core, repositorySettingsUpdate.ID, "admin", `{"archived":true}`, true)
		var failure *provider.Error
		if !errors.As(err, &failure) || strings.Contains(err.Error(), "canary") ||
			!strings.Contains(err.Error(), "Administration: write") ||
			(status == http.StatusForbidden && failure.Class != provider.ClassPermission) ||
			(status == http.StatusNotFound && failure.Class != provider.ClassNotFound) {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
}

func TestRepositorySettingsGetLeavesOutMissingTexts(t *testing.T) {
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != repositoryRoute {
			return false
		}
		_, _ = w.Write([]byte(`{"default_branch":"main","visibility":"public","archived":true,"description":null,"topics":[]}`))
		return true
	}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), maintainConfig(base), resolver(red, &reads), red)
	result, err := invoke(t, core, repositorySettingsGet.ID, "admin", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"description", "homepage", "topics", "has_wiki"} {
		if strings.Contains(string(result), absent) {
			t.Errorf("result %s carries %s", result, absent)
		}
	}
	if !strings.Contains(string(result), `"archived":true`) {
		t.Errorf("result = %s", result)
	}
}
