package forge

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

const templatesJSON = `[{"name":"Bug","title":"[bug] ","about":"Report","file_name":"bug.yaml","content":"",` +
	`"ref":"refs/heads/main","labels":["bug"],"assignees":["alice"],"undocumented":"x",` +
	`"body":[{"id":"what","type":"textarea","attributes":{"label":"What","nested":{"a":[1,2]}},` +
	`"validations":{"required":true},"visible":["form","content"]}]}]`

func issueConfigHandler(c call) *http.Response {
	switch c.path {
	case "/api/v1/repos/acme/widget/issue_templates":
		return jsonResponse(http.StatusOK, templatesJSON)
	case "/api/v1/repos/acme/widget/issue_config":
		return jsonResponse(http.StatusOK, `{"blank_issues_enabled":false,"contact_links":[`+
			`{"name":"Chat","about":"Ask","url":"https://chat.example.test/room"},`+
			`{"name":"Bad","about":"x","url":"https://chat.example.test/room?key=1"}]}`)
	case "/api/v1/repos/acme/widget/issue_config/validate":
		return jsonResponse(http.StatusOK, `{"valid":false,"message":"bad file"}`)
	}
	return notFoundResponse()
}

func TestIssueConfigToolsReadFixedPaths(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueConfigHandler)
		for tool, path := range map[*tool]string{issueTemplatesList: "/api/v1/repos/acme/widget/issue_templates",
			issueConfigGet:      "/api/v1/repos/acme/widget/issue_config",
			issueConfigValidate: "/api/v1/repos/acme/widget/issue_config/validate"} {
			*e.calls = nil
			result, err := e.invoke(t, tool, "repo", `{}`)
			if err != nil || len(*e.calls) != 1 || (*e.calls)[0].method != http.MethodGet || (*e.calls)[0].path != path {
				t.Fatalf("%s %s: %v, calls %v", p.id, path, err, *e.calls)
			}
			switch tool {
			case issueTemplatesList:
				row := result["templates"].([]any)[0].(map[string]any)
				_, assignees := row["assignees"]
				field := row["body"].([]any)[0].(map[string]any)
				if row["name"] != "Bug" || row["undocumented"] != nil || assignees != (p == gitea) ||
					field["type"] != "textarea" || field["attributes"] == nil || len(field["visible"].([]any)) != 2 {
					t.Errorf("%s: %v", p.id, row)
				}
			case issueConfigGet:
				links := result["contact_links"].([]any)
				if result["blank_issues_enabled"] != false || len(links) != 2 ||
					links[0].(map[string]any)["url"] == nil || links[1].(map[string]any)["url"] != nil {
					t.Errorf("%s: %v", p.id, result)
				}
			default:
				if result["valid"] != false || result["message"] != "bad file" {
					t.Errorf("%s: %v", p.id, result)
				}
			}
		}
	}
}

func TestIssueConfigToolsCheckTheTargets(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueConfigHandler)
		for _, tool := range []*tool{issueTemplatesList, issueConfigGet, issueConfigValidate} {
			for _, tc := range []struct{ connection, arguments string }{
				{"repo", `{"repository":"other/thing"}`}, {"two", `{}`}, {"org", `{"repository":"bob/widget"}`}} {
				*e.calls = nil
				_, err := e.invoke(t, tool, tc.connection, tc.arguments)
				if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 ||
					strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "bob") {
					t.Errorf("%s %s: %v after %d calls and %d reads", p.id, tc.arguments, err, len(*e.calls), *e.reads)
				}
			}
		}
		if _, err := e.invoke(t, issueConfigGet, "org", `{"repository":"acme/widget"}`); err != nil {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestIssueTemplatesAreBounded(t *testing.T) {
	rows := strings.TrimSuffix(strings.Repeat(`{"name":"t"},`, maxListItems+5), ",")
	e := newEnvironment(t, gitea, func(c call) *http.Response {
		if c.path == "/api/v1/repos/acme/widget/issue_templates" {
			return jsonResponse(http.StatusOK, "["+rows+"]")
		}
		return notFoundResponse()
	})
	result, err := e.invoke(t, issueTemplatesList, "repo", `{}`)
	if err != nil || len(result["templates"].([]any)) != maxListItems || result["truncated"] != true {
		t.Fatalf("%v, %v", len(result), err)
	}
}

func TestIssueConfigNotFoundMeansUnreachable(t *testing.T) {
	e := newEnvironment(t, forgejo, func(call) *http.Response { return notFoundResponse() })
	_, err := e.invoke(t, issueConfigGet, "repo", `{}`)
	if err == nil || classOfError(err) != "not-found" || !slices.Equal(e.paths(), []string{"/api/v1/repos/acme/widget/issue_config"}) {
		t.Errorf("%v %v", err, e.paths())
	}
}
