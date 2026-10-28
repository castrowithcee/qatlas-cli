package github

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func decodeAny(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func marshalProjection(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertJSON(t *testing.T, got any, want string) {
	t.Helper()
	data := marshalProjection(t, got)
	if g, w := decodeAny(t, []byte(data)), decodeAny(t, []byte(want)); !reflect.DeepEqual(g, w) {
		t.Errorf("projection differs\n got: %s\nwant: %s", data, want)
	}
}

func TestRepositoryProjectionMatchesDocumentedExamples(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"minimal", exampleMinimalRepository, wantMinimalRepository},
		{"full", exampleFullRepository, wantFullRepository},
	} {
		var raw repositoryRaw
		if err := json.Unmarshal([]byte(tc.in), &raw); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.name, func(t *testing.T) { assertJSON(t, raw.project(allowlist{}), tc.want) })
	}
}

func TestRepositoryProjectionNeverCarriesTheCloneToken(t *testing.T) {
	var raw repositoryRaw
	if err := json.Unmarshal([]byte(exampleFullRepository), &raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exampleFullRepository, "temp_clone_token") {
		t.Fatal("the documented example no longer carries the token")
	}
	out := marshalProjection(t, raw.project(allowlist{}))
	if strings.Contains(out, "temp_clone_token") || strings.Contains(out, "ABTLWHOULUVAXGTRYU7OC2876QJ2O") {
		t.Errorf("clone token in output: %s", out)
	}
	for _, name := range []string{"has_downloads", "use_squash_pr_title_as_default", "organization", "parent"} {
		if strings.Contains(out, `"`+name+`"`) {
			t.Errorf("%s in output", name)
		}
	}
}

func TestRepositoryOutsideTheTargetsShowsOnlyIdentifiers(t *testing.T) {
	var raw repositoryRaw
	if err := json.Unmarshal([]byte(exampleFullRepository), &raw); err != nil {
		t.Fatal(err)
	}
	other, err := parseAllowlist([]string{"repos/someone/else"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, raw.project(other), `{"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","full_name":"octocat/Hello-World"}`)
	for _, targets := range [][]string{{"repos/octocat/Hello-World"}, {"repos/OCTOCAT/hello-world"}, {"repos/octocat/*"},
		{"users/octocat"}} {
		list, err := parseAllowlist(targets)
		if err != nil {
			t.Fatal(err)
		}
		if got := raw.project(list); got.Owner == nil || got.Name == nil {
			t.Errorf("targets %v withheld an admitted repository", targets)
		}
	}
	var bad repositoryRaw
	if err := json.Unmarshal([]byte(`{"id":5,"full_name":"no-slash","name":"x"}`), &bad); err != nil {
		t.Fatal(err)
	}
	assertJSON(t, bad.project(allowlist{}), `{"id":5,"full_name":"no-slash"}`)
}

func TestRepositoryProjectionDropsMissingAndNull(t *testing.T) {
	var raw repositoryRaw
	in := `{"id":1,"full_name":"a/b","name":"b","description":null,"homepage":null,"license":null,"language":null,` +
		`"mirror_url":null,"url":"https://api.github.com/repos/a/b","owner":null,"permissions":null,"forks_count":0,` +
		`"private":false,"topics":[],"security_and_analysis":null,"custom_properties":null}`
	if err := json.Unmarshal([]byte(in), &raw); err != nil {
		t.Fatal(err)
	}
	assertJSON(t, raw.project(allowlist{}), `{"id":1,"full_name":"a/b","name":"b","forks_count":0,"private":false,`+
		`"links":{"url":"https://api.github.com/repos/a/b"}}`)
}

func TestRepositoryNestedObjects(t *testing.T) {
	var raw repositoryRaw
	in := `{"id":1,"full_name":"a/b","code_of_conduct":{"key":"contributor_covenant","name":"CoC","url":"https://api.github.com/codes_of_conduct/x",` +
		`"body":"line1\nline2\u0007","html_url":"https://github.com/codes_of_conduct/x"},` +
		`"security_and_analysis":{"advanced_security":{"status":"enabled"},"secret_scanning_delegated_bypass_options":` +
		`{"reviewers":[{"reviewer_id":9,"reviewer_type":"TEAM","mode":"ALWAYS"}]}},` +
		`"code_search_index_status":{"lexical_search_ok":true,"lexical_commit_sha":"abc"},"permissions":{"maintain":false,"admin":true}}`
	if err := json.Unmarshal([]byte(in), &raw); err != nil {
		t.Fatal(err)
	}
	assertJSON(t, raw.project(allowlist{}), `{"id":1,"full_name":"a/b","code_of_conduct":{"key":"contributor_covenant","name":"CoC",`+
		`"body":"line1\nline2 ","url":"https://github.com/codes_of_conduct/x","links":{"url":"https://api.github.com/codes_of_conduct/x"}},`+
		`"security_and_analysis":{"advanced_security":{"status":"enabled"},"secret_scanning_delegated_bypass_options":`+
		`{"reviewers":[{"reviewer_id":9,"reviewer_type":"TEAM","mode":"ALWAYS"}]}},`+
		`"code_search_index_status":{"lexical_search_ok":true,"lexical_commit_sha":"abc"},"permissions":{"maintain":false,"admin":true}}`)
}

func TestCustomPropertiesAreMarkedNotCut(t *testing.T) {
	project := func(props string) *RESTRepository {
		var raw repositoryRaw
		if err := json.Unmarshal([]byte(`{"id":1,"full_name":"a/b","custom_properties":`+props+`}`), &raw); err != nil {
			t.Fatal(err)
		}
		return raw.project(allowlist{})
	}
	assertJSON(t, project(`{"team":"core","tags":["a","b"],"n":12345678901234567,"on":true,"unset":null}`),
		`{"id":1,"full_name":"a/b","custom_properties":{"team":"core","tags":["a","b"],"n":12345678901234567,"on":true}}`)
	if got := marshalProjection(t, project(`{"n":12345678901234567}`)); !strings.Contains(got, "12345678901234567") {
		t.Errorf("number changed: %s", got)
	}
	tooMany := make([]string, customPropertiesNodes+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf(`"k%d":"v"`, i)
	}
	for _, props := range map[string]string{
		"nodes":  "{" + strings.Join(tooMany, ",") + "}",
		"depth":  `{"a":{"b":{"c":"d"}}}`,
		"text":   `{"a":"` + strings.Repeat("x", customPropertiesText+1) + `"}`,
		"key":    `{"` + strings.Repeat("k", customPropertiesKeySize+1) + `":"v"}`,
		"shape":  `["a"]`,
		"nested": `{"a":[{"b":{"c":1}}]}`,
	} {
		assertJSON(t, project(props), `{"id":1,"full_name":"a/b","custom_properties_omitted":true}`)
	}
}

func TestTopicsAndTextsAreBounded(t *testing.T) {
	topics := make([]string, projectionListLimit+1)
	for i := range topics {
		topics[i] = fmt.Sprintf(`"t%d"`, i)
	}
	long := strings.Repeat("ä", projectionTextLimit+5)
	var raw repositoryRaw
	in := `{"id":1,"full_name":"a/b","topics":[` + strings.Join(topics, ",") + `],"description":"` + long + `",` +
		`"name":"a\u0000b","homepage":"https://x/` + strings.Repeat("p", projectionURLLimit) + `"}`
	if err := json.Unmarshal([]byte(in), &raw); err != nil {
		t.Fatal(err)
	}
	got := raw.project(allowlist{})
	if len(got.Topics) != projectionListLimit || !got.TopicsTruncated {
		t.Errorf("topics = %d, truncated %v", len(got.Topics), got.TopicsTruncated)
	}
	if n := len([]rune(*got.Description)); n != projectionTextLimit {
		t.Errorf("description runes = %d", n)
	}
	if *got.Name != "a b" {
		t.Errorf("name = %q", *got.Name)
	}
	if got.Homepage != nil {
		t.Error("an over-long address must be left out, not cut")
	}
}

func TestAppProjection(t *testing.T) {
	var raw appRaw
	if err := json.Unmarshal([]byte(exampleIntegration), &raw); err != nil {
		t.Fatal(err)
	}
	assertJSON(t, raw.project(), wantIntegration)
}

func TestAppEnterpriseOwnerKeepsSharedMembers(t *testing.T) {
	var raw appRaw
	in := `{"id":2,"name":"E","owner":{"id":7,"node_id":"E_1","name":"Corp","slug":"corp","description":"d",` +
		`"html_url":"https://github.com/enterprises/corp","website_url":null,"created_at":"2020-01-01T00:00:00Z",` +
		`"avatar_url":"https://avatars/e"}}`
	if err := json.Unmarshal([]byte(in), &raw); err != nil {
		t.Fatal(err)
	}
	assertJSON(t, raw.project(), `{"id":2,"name":"E","owner":{"id":7,"node_id":"E_1","name":"Corp",`+
		`"url":"https://github.com/enterprises/corp","avatar_url":"https://avatars/e"}}`)
}

func TestAppListsAreBounded(t *testing.T) {
	permissions := map[string]string{}
	events := make([]string, projectionListLimit+3)
	for i := range events {
		permissions[fmt.Sprintf("p%03d", i)] = "read"
		events[i] = fmt.Sprintf("e%d", i)
	}
	data, _ := json.Marshal(map[string]any{"id": 1, "permissions": permissions, "events": events})
	var raw appRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	app := raw.project()
	if len(app.Permissions) != projectionListLimit || !app.PermissionsCut || len(app.Events) != projectionListLimit || !app.EventsCut {
		t.Errorf("permissions %d/%v events %d/%v", len(app.Permissions), app.PermissionsCut, len(app.Events), app.EventsCut)
	}
	if _, ok := app.Permissions["p000"]; !ok {
		t.Error("the cut is not deterministic")
	}
}

func TestIssueCommentProjection(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{exampleIssueComment, wantIssueComment},
		{exampleIssueCommentBare, wantIssueCommentBare},
	} {
		var raw issueCommentRaw
		if err := json.Unmarshal([]byte(tc.in), &raw); err != nil {
			t.Fatal(err)
		}
		got := raw.project()
		assertJSON(t, got, tc.want)
		if out := marshalProjection(t, got); strings.Contains(out, "body_html") || strings.Contains(out, "body_text") {
			t.Errorf("rendered body variant in output: %s", out)
		}
	}
}

func TestIssueCommentBodyBound(t *testing.T) {
	var raw issueCommentRaw
	body := strings.Repeat("a\n", projectionBodyLimit)
	data, _ := json.Marshal(map[string]any{"id": 1, "body": body + "\x1b", "pin": nil, "minimized": map[string]any{"reason": nil}})
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	got := raw.project()
	if !got.BodyTruncated || len([]rune(*got.Body)) != projectionBodyLimit || !strings.HasPrefix(*got.Body, "a\na\n") {
		t.Errorf("body not bounded: truncated %v, %d runes", got.BodyTruncated, len([]rune(*got.Body)))
	}
	assertJSON(t, got.Minimized, `{}`)
	if got.Pin != nil {
		t.Error("null pin must stay out")
	}
}

func TestAccountLinksOmitEmptyValues(t *testing.T) {
	var raw accountRaw
	if err := json.Unmarshal([]byte(`{"login":"x","site_admin":false,"url":"https://api/x","followers_url":"","repos_url":null,"html_url":"https://github.com/x"}`), &raw); err != nil {
		t.Fatal(err)
	}
	assertJSON(t, raw.project(), `{"login":"x","site_admin":false,"url":"https://github.com/x","links":{"url":"https://api/x"}}`)
}

func TestDetailHelpers(t *testing.T) {
	if detailName("user", false) != "user_detail" || detailName("labels", true) != "labels_details" {
		t.Error("sibling names")
	}
	one := detailProperty("user", restAccountSchema, false)
	many := detailProperty("assignees", restAccountSchema, true)
	for _, schema := range []string{one, many} {
		var v map[string]any
		if err := json.Unmarshal([]byte("{"+schema+"}"), &v); err != nil {
			t.Fatalf("%s: %v", schema, err)
		}
	}
	if !strings.HasPrefix(one, `"user_detail":{"type":"object"`) || !strings.HasPrefix(many, `"assignees_details":{"type":"array","items":{"type":"object"`) {
		t.Errorf("schema: %s / %s", one, many)
	}
	if f := detailField("user", "Account", false); f.Name != "user_detail" || !strings.Contains(f.Description, "untrusted data") {
		t.Errorf("field: %+v", f)
	}
	if f := detailField("assignees", "Accounts", true); f.Name != "assignees_details" {
		t.Errorf("field: %+v", f)
	}
}

func TestProjectionSchemasAndFieldsMatchTheOutputTypes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sample     any
		properties string
		fields     []capability.Field
	}{
		{"account", RESTAccount{}, restAccountProperties, restAccountFields},
		{"repository", RESTRepository{}, restRepositoryProperties, restRepositoryFields},
		{"app", RESTApp{}, restAppProperties, restAppFields},
		{"reactions", RESTReactions{}, restReactionsProperties, restReactionsFields},
		{"issue comment", RESTIssueComment{}, restIssueCommentProperties, restIssueCommentFields},
		{"pull request", RESTPullSimple{}, restPullSimpleProperties, restPullSimpleFields},
	} {
		schema := json.RawMessage(`{"type":"object","properties":{` + tc.properties + `},"additionalProperties":false}`)
		if findings := checkOutput(tc.sample, schema, tc.fields, nil); len(findings) > 0 {
			t.Errorf("%s: %s", tc.name, strings.Join(findings, "; "))
		}
		for _, field := range tc.fields {
			if strings.TrimSpace(field.Description) == "" {
				t.Errorf("%s.%s has no description", tc.name, field.Name)
			}
		}
	}
}

func TestLinkSchemasCoverTheEmittedLinks(t *testing.T) {
	var raw repositoryRaw
	if err := json.Unmarshal([]byte(exampleMinimalRepository), &raw); err != nil {
		t.Fatal(err)
	}
	got := raw.project(allowlist{})
	if len(got.Links) != len(restRepositoryLinkKeys) {
		t.Errorf("links = %d, want %d", len(got.Links), len(restRepositoryLinkKeys))
	}
	for key := range got.Links {
		if !strings.Contains(restRepositoryProperties, `"`+key+`":{"type":"string"}`) {
			t.Errorf("link %s missing from the schema", key)
		}
	}
}

// A linked repository an exclusion covers shows only its identifiers, on both the REST and the GraphQL
// shape, although the targets would admit it; an unrelated exclusion changes nothing.
func TestExcludedLinkedRepositoryShowsOnlyIdentifiers(t *testing.T) {
	var raw repositoryRaw
	if err := json.Unmarshal([]byte(exampleFullRepository), &raw); err != nil {
		t.Fatal(err)
	}
	for _, excludes := range [][]string{{"repos/octocat/Hello-World"}, {"repos/OCTOCAT/*"}, {"users/octocat"}} {
		list, err := parseAllowlistExcluding(nil, excludes)
		if err != nil {
			t.Fatal(err)
		}
		assertJSON(t, raw.project(list), `{"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","full_name":"octocat/Hello-World"}`)
		nested := gqlNestedRepositoryRaw{NameWithOwner: ptr("octocat/Hello-World"), Name: ptr("Hello-World")}
		if got := nested.project(list); got.Name != nil {
			t.Errorf("excludes %v showed the name of a linked GraphQL repository", excludes)
		}
	}
	list, err := parseAllowlistExcluding(nil, []string{"repos/someone/else"})
	if err != nil {
		t.Fatal(err)
	}
	if got := raw.project(list); got.Owner == nil || got.Name == nil {
		t.Error("an unrelated exclusion withheld a repository")
	}
}
