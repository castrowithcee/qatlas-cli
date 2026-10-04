package n8n

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const ownTag = "TAG_OWN_0001AAAA"

const tagBody = `{"id":"TAG_OWN_0001AAAA","name":"prod","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z"}`

func tagServer(t *testing.T, mutations *[]string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		raw := ""
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			raw = string(b)
		}
		if r.Method != http.MethodGet {
			*mutations = append(*mutations, r.Method+" "+r.URL.Path+" "+raw)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/tags":
			return jsonResponse(200, `{"data":[`+tagBody+`],"nextCursor":"next"}`), nil
		case r.URL.Path == apiPath+"/tags/"+ownTag, r.URL.Path == apiPath+"/tags":
			return jsonResponse(200, tagBody), nil
		}
		t.Errorf("unexpected request to %s %s", r.Method, r.URL.Path)
		return jsonResponse(500, `{}`), nil
	}
}

func TestTagToolsSendTheDocumentedRequests(t *testing.T) {
	var calls []call
	var mutations []string
	env := newEnvironment(t, &calls, tagServer(t, &mutations))
	result, err := env.invoke(tagsList.ID, "pdelete-open", `{"limit":5,"cursor":"c1"}`)
	var page InstanceTagsPage
	if err != nil || calls[0].method != http.MethodGet || calls[0].path != apiPath+"/tags" ||
		calls[0].query.Get("limit") != "5" || calls[0].query.Get("cursor") != "c1" ||
		!strings.Contains(result, `"has_more":true`) || !strings.Contains(result, `"created_at"`) {
		t.Fatalf("list: %s, %v, %+v, %v", result, err, calls, page)
	}
	result, err = env.invoke(tagsGet.ID, "pdelete-open", fmt.Sprintf(`{"tag_id":%q}`, ownTag))
	if err != nil || !strings.Contains(result, `"name":"prod"`) || calls[1].method != http.MethodGet ||
		calls[1].path != apiPath+"/tags/"+ownTag {
		t.Fatalf("get: %s, %v, %+v", result, err, calls)
	}
	for _, s := range []struct{ operation, arguments string }{
		{tagsCreate.ID, `{"name":"prod"}`},
		{tagsUpdate.ID, fmt.Sprintf(`{"tag_id":%q,"name":"staging"}`, ownTag)},
		{tagsDelete.ID, fmt.Sprintf(`{"tag_id":%q}`, ownTag)},
	} {
		if _, err := env.confirmed(s.operation, "pdelete-open", s.arguments); err != nil {
			t.Fatalf("%s: %v", s.operation, err)
		}
	}
	want := []string{
		fmt.Sprintf(`POST %s/tags {"name":"prod"}`, apiPath),
		fmt.Sprintf(`PUT %s/tags/%s {"name":"staging"}`, apiPath, ownTag),
		fmt.Sprintf(`DELETE %s/tags/%s `, apiPath, ownTag),
	}
	if strings.Join(mutations, "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %q, want %q", mutations, want)
	}
}

func TestTagToolsAreRefusedLocallyOnATargetedConnection(t *testing.T) {
	arguments := map[string]string{
		tagsList.ID:   `{}`,
		tagsGet.ID:    fmt.Sprintf(`{"tag_id":%q}`, ownTag),
		tagsCreate.ID: `{"name":"prod"}`,
		tagsUpdate.ID: fmt.Sprintf(`{"tag_id":%q,"name":"x"}`, ownTag),
		tagsDelete.ID: fmt.Sprintf(`{"tag_id":%q}`, ownTag),
	}
	for operation, args := range arguments {
		for _, connection := range []string{"project", "workflow", "both", "pdelete", "pdelete-workflow"} {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(operation, connection, args)
			if err == nil || len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("%s on %s: err = %v, calls = %+v", operation, connection, err, calls)
			}
			for _, leaked := range []string{ownProject, ownWorkflow} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("%s on %s leaked a target: %v", operation, connection, err)
				}
			}
		}
	}
}

func TestRequireInstanceScope(t *testing.T) {
	for _, tt := range []struct {
		name    string
		targets []string
		ok      bool
	}{
		{"none", nil, true},
		{"project", []string{"project/" + ownProject}, false},
		{"workflow", []string{"workflow/" + ownWorkflow}, false},
		{"both", []string{"project/" + ownProject, "workflow/" + ownWorkflow}, false},
	} {
		err := requireInstanceScope(resolvedConnection(tt.targets...), "things")
		if tt.ok != (err == nil) {
			t.Fatalf("%s: err = %v", tt.name, err)
		}
		if err != nil && (!isInvalidRequest(err) || strings.Contains(err.Error(), ownProject) ||
			strings.Contains(err.Error(), ownWorkflow) || !strings.Contains(err.Error(), "things")) {
			t.Fatalf("%s: err = %v", tt.name, err)
		}
	}
	if err := requireInstanceScope(nil, "things"); err == nil {
		t.Fatal("a missing connection must be refused")
	}
	if err := requireInstanceScope(&config.Resolved{Targets: []string{"bogus"}}, "things"); err == nil {
		t.Fatal("a malformed target must be refused")
	}
}

func TestTagArgumentsAreValidatedBeforeAnyRequest(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{tagsGet.ID, `{"tag_id":"a/b"}`},
		{tagsUpdate.ID, `{"tag_id":"../x","name":"n"}`},
		{tagsDelete.ID, `{"tag_id":"a b"}`},
		{tagsCreate.ID, `{"name":"` + strings.Repeat("a", 25) + `"}`},
		{tagsCreate.ID, `{"name":"  "}`},
		{tagsCreate.ID, "{\"name\":\"a\\nb\"}"},
		{tagsUpdate.ID, fmt.Sprintf(`{"tag_id":%q,"name":""}`, ownTag)},
	} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(tt.operation, "pdelete-open", tt.arguments)
		if err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s %s: err = %v", tt.operation, tt.arguments, err)
		}
	}
}

func TestTagChangesRequireConfirmation(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{tagsCreate.ID, `{"name":"prod"}`},
		{tagsUpdate.ID, fmt.Sprintf(`{"tag_id":%q,"name":"x"}`, ownTag)},
		{tagsDelete.ID, fmt.Sprintf(`{"tag_id":%q}`, ownTag)},
	} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.invoke(tt.operation, "pdelete-open", tt.arguments)
		if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v", tt.operation, err)
		}
	}
}

func TestTagDeleteIsOnlyOfferedByAToolsListAndProfilesAreOwn(t *testing.T) {
	if !tagsDelete.RequiresToolAllowList || tagsDelete.Risk.Effect != "delete" ||
		tagsDelete.Risk.Confirmation != "required" || tagsCreate.RequiresToolAllowList ||
		tagsUpdate.RequiresToolAllowList || tagsList.Risk.Confirmation != "none" ||
		tagsGet.Risk.Confirmation != "none" {
		t.Fatalf("descriptor = %+v", tagsDelete)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	got := map[string][]string{}
	for _, profile := range metadata.Profiles {
		got[profile.ID] = profile.Tools
		for _, id := range profile.Tools {
			if id == tagsDelete.ID {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
			if strings.HasPrefix(id, "n8n.tags.") && !strings.HasPrefix(profile.ID, "tags") {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
		}
	}
	if len(got["tags-read"]) != 2 || len(got["tags-manage"]) != 4 {
		t.Fatalf("profiles = %v", got)
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(tagsDelete.ID, "open", fmt.Sprintf(`{"tag_id":%q}`, ownTag))
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestTagForbiddenIsReportedAsScopeOrRoleError(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{tagsList.ID, `{}`},
		{tagsGet.ID, fmt.Sprintf(`{"tag_id":%q}`, ownTag)},
		{tagsCreate.ID, `{"name":"prod"}`},
		{tagsUpdate.ID, fmt.Sprintf(`{"tag_id":%q,"name":"x"}`, ownTag)},
		{tagsDelete.ID, fmt.Sprintf(`{"tag_id":%q}`, ownTag)},
	} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
		})
		_, err := env.confirmed(tt.operation, "pdelete-open", tt.arguments)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "role") ||
			strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("%s: err = %v", tt.operation, err)
		}
	}
}

func TestTagChangesAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	for _, tt := range []struct {
		name, operation, arguments string
		respond                    func() (*http.Response, error)
	}{
		{"create 5xx", tagsCreate.ID, `{"name":"prod"}`,
			func() (*http.Response, error) { return jsonResponse(500, `{}`), nil }},
		{"update transport", tagsUpdate.ID, fmt.Sprintf(`{"tag_id":%q,"name":"x"}`, ownTag),
			func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"delete 502", tagsDelete.ID, fmt.Sprintf(`{"tag_id":%q}`, ownTag),
			func() (*http.Response, error) { return jsonResponse(502, `{}`), nil }},
		{"create unreadable", tagsCreate.ID, `{"name":"prod"}`,
			func() (*http.Response, error) { return jsonResponse(200, `not json`), nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return tt.respond() })
			_, err := env.confirmed(tt.operation, "pdelete-open", tt.arguments)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(calls) != 1 {
				t.Fatalf("err = %v, calls = %+v", err, calls)
			}
		})
	}
}
