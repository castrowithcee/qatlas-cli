package github

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func decodeRaw[T any](t *testing.T, in string) *T {
	t.Helper()
	var raw T
	if err := json.Unmarshal([]byte(in), &raw); err != nil {
		t.Fatal(err)
	}
	return &raw
}

func TestGraphQLAccountVariants(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"user", `{"__typename":"User","login":"octocat","id":"MDQ6VXNlcjE=","databaseId":1,"name":"monalisa octocat",` +
			`"email":"octocat@example.com","avatarUrl":"https://avatars.githubusercontent.com/u/1?v=4",` +
			`"url":"https://github.com/octocat","resourcePath":"/octocat","isSiteAdmin":false}`,
			`{"login":"octocat","type":"User","id":"MDQ6VXNlcjE=","database_id":1,"name":"monalisa octocat",` +
				`"email":"octocat@example.com","avatar_url":"https://avatars.githubusercontent.com/u/1?v=4",` +
				`"url":"https://github.com/octocat","links":{"resource_path":"/octocat"},"is_site_admin":false}`},
		{"user with private email", `{"__typename":"User","login":"u","email":"","name":null,"isSiteAdmin":true}`,
			`{"login":"u","type":"User","is_site_admin":true}`},
		{"bot", `{"__typename":"Bot","login":"dependabot","id":"BOT_1","databaseId":49699333,` +
			`"avatarUrl":"https://avatars/b","url":"https://github.com/apps/dependabot","resourcePath":"/apps/dependabot"}`,
			`{"login":"dependabot","type":"Bot","id":"BOT_1","database_id":49699333,"avatar_url":"https://avatars/b",` +
				`"url":"https://github.com/apps/dependabot","links":{"resource_path":"/apps/dependabot"}}`},
		{"organization", `{"__typename":"Organization","login":"github","id":"O_1","databaseId":9919,"name":"GitHub",` +
			`"email":null,"url":"https://github.com/github","resourcePath":"/github"}`,
			`{"login":"github","type":"Organization","id":"O_1","database_id":9919,"name":"GitHub",` +
				`"url":"https://github.com/github","links":{"resource_path":"/github"}}`},
		{"mannequin", `{"__typename":"Mannequin","login":"ghost-imported","id":"M_1","databaseId":5,"name":"Imported",` +
			`"email":"i@example.com","isSiteAdmin":true}`,
			`{"login":"ghost-imported","type":"Mannequin","id":"M_1","database_id":5,"name":"Imported","email":"i@example.com"}`},
		{"enterprise user account", `{"__typename":"EnterpriseUserAccount","login":"jane_corp","id":"EUA_1","name":"Jane",` +
			`"url":"https://github.com/enterprises/corp/people/jane_corp","resourcePath":"/enterprises/corp/people/jane_corp"}`,
			`{"login":"jane_corp","type":"EnterpriseUserAccount","id":"EUA_1","name":"Jane",` +
				`"url":"https://github.com/enterprises/corp/people/jane_corp",` +
				`"links":{"resource_path":"/enterprises/corp/people/jane_corp"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) { assertJSON(t, decodeRaw[gqlAccountRaw](t, tc.in).project(), tc.want) })
	}
}

func TestGraphQLEmptyAnswersGiveEmptyViews(t *testing.T) {
	assertJSON(t, decodeRaw[gqlAccountRaw](t, `{}`).project(), `{}`)
	assertJSON(t, decodeRaw[gqlGitActorRaw](t, `{}`).project(), `{}`)
	assertJSON(t, decodeRaw[gqlReactionGroupRaw](t, `{}`).project(), `{}`)
	assertJSON(t, decodeRaw[gqlLabelRaw](t, `{}`).project(), `{}`)
	assertJSON(t, decodeRaw[gqlMilestoneRaw](t, `{}`).project(), `{}`)
	assertJSON(t, decodeRaw[gqlIssueTypeRaw](t, `{}`).project(), `{}`)
	assertJSON(t, decodeRaw[gqlReferenceRaw](t, `{}`).project(), `{}`)
	assertJSON(t, decodeRaw[gqlReferenceRaw](t, `{"repository":null,"title":null,"url":""}`).project(), `{}`)
	var nilRaw *gqlMilestoneRaw
	if nilRaw.project() != nil {
		t.Error("a null object must stay absent")
	}
	if got := projectGQLReactionGroups(nil); got != nil {
		t.Errorf("groups = %v", got)
	}
}

func TestGraphQLGitActor(t *testing.T) {
	in := `{"name":"Mona Lisa","email":"mona@example.com","date":"2026-10-01T12:30:00+02:00",` +
		`"avatarUrl":"https://avatars/m","user":{"__typename":"User","id":"U_1","databaseId":7,"login":"mona",` +
		`"name":"Mona","email":"","avatarUrl":"https://avatars/m","url":"https://github.com/mona",` +
		`"resourcePath":"/mona","isSiteAdmin":false}}`
	assertJSON(t, decodeRaw[gqlGitActorRaw](t, in).project(), `{"name":"Mona Lisa","email":"mona@example.com",`+
		`"date":"2026-10-01T12:30:00+02:00","links":{"avatar_url":"https://avatars/m"},`+
		`"user":{"login":"mona","type":"User","id":"U_1","database_id":7,"name":"Mona","avatar_url":"https://avatars/m",`+
		`"url":"https://github.com/mona","links":{"resource_path":"/mona"},"is_site_admin":false}}`)
	assertJSON(t, decodeRaw[gqlGitActorRaw](t, `{"name":"x","user":null}`).project(), `{"name":"x"}`)
}

func TestGraphQLReactionGroups(t *testing.T) {
	in := `[{"content":"THUMBS_UP","createdAt":"2026-10-01T00:00:00Z","viewerHasReacted":true,"reactors":{"totalCount":3}},` +
		`{"content":"HEART","createdAt":null,"viewerHasReacted":false,"reactors":{"totalCount":0}},null]`
	raw := decodeRaw[[]*gqlReactionGroupRaw](t, in)
	assertJSON(t, projectGQLReactionGroups(*raw), `[{"content":"THUMBS_UP","created_at":"2026-10-01T00:00:00Z",`+
		`"viewer_has_reacted":true,"count":3},{"content":"HEART","viewer_has_reacted":false,"count":0}]`)
}

func TestGraphQLLabel(t *testing.T) {
	in := `{"id":"LA_1","name":"bug","description":"Something\u0007 broke","color":"d73a4a","isDefault":true,` +
		`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-02-01T00:00:00Z",` +
		`"url":"https://github.com/o/r/labels/bug","resourcePath":"/o/r/labels/bug"}`
	assertJSON(t, decodeRaw[gqlLabelRaw](t, in).project(), `{"id":"LA_1","name":"bug","description":"Something  broke",`+
		`"color":"d73a4a","is_default":true,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-02-01T00:00:00Z",`+
		`"url":"https://github.com/o/r/labels/bug","links":{"resource_path":"/o/r/labels/bug"}}`)
	assertJSON(t, decodeRaw[gqlLabelRaw](t, `{"name":"x","description":null,"isDefault":false,"createdAt":null}`).project(),
		`{"name":"x","is_default":false}`)
}

func TestGraphQLMilestone(t *testing.T) {
	in := `{"id":"MI_1","number":4,"title":"v1","description":"First","state":"CLOSED","closed":true,` +
		`"closedAt":"2026-03-01T00:00:00Z","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-03-01T00:00:00Z",` +
		`"dueOn":"2026-02-28T00:00:00Z","openIssueCount":0,"closedIssueCount":12,"progressPercentage":100.0,` +
		`"creator":{"__typename":"Organization","login":"acme","id":"O_2","databaseId":2},` +
		`"viewerCanClose":false,"viewerCanReopen":true,"url":"https://github.com/o/r/milestone/4",` +
		`"resourcePath":"/o/r/milestone/4"}`
	assertJSON(t, decodeRaw[gqlMilestoneRaw](t, in).project(), `{"id":"MI_1","number":4,"title":"v1","description":"First",`+
		`"state":"CLOSED","closed":true,"closed_at":"2026-03-01T00:00:00Z","created_at":"2026-01-01T00:00:00Z",`+
		`"updated_at":"2026-03-01T00:00:00Z","due_on":"2026-02-28T00:00:00Z","open_issue_count":0,`+
		`"closed_issue_count":12,"progress_percentage":100.0,`+
		`"creator":{"login":"acme","type":"Organization","id":"O_2","database_id":2},`+
		`"viewer_can_close":false,"viewer_can_reopen":true,"url":"https://github.com/o/r/milestone/4",`+
		`"links":{"resource_path":"/o/r/milestone/4"}}`)
}

func TestGraphQLProgressKeepsItsDigits(t *testing.T) {
	for _, number := range []string{"33.33333333333333333333", "0", "0.1", "1e2"} {
		out := marshalProjection(t, decodeRaw[gqlMilestoneRaw](t, `{"progressPercentage":`+number+`}`).project())
		if want := `{"progress_percentage":` + number + `}`; out != want {
			t.Errorf("got %s, want %s", out, want)
		}
	}
}

func TestGraphQLIssueTypeAndReferences(t *testing.T) {
	assertJSON(t, decodeRaw[gqlIssueTypeRaw](t, `{"id":"IT_1","name":"Bug","description":"A defect","color":"RED","isEnabled":false,"isPrivate":true}`).project(),
		`{"id":"IT_1","name":"Bug","description":"A defect","color":"RED","is_enabled":false,"is_private":true}`)
	in := `{"id":"I_1","number":9,"title":"Crash","state":"OPEN","url":"https://github.com/o/r/issues/9",` +
		`"repository":{"nameWithOwner":"o/r"}}`
	assertJSON(t, decodeRaw[gqlReferenceRaw](t, in).project(), `{"id":"I_1","number":9,"title":"Crash","state":"OPEN",`+
		`"url":"https://github.com/o/r/issues/9","repository":"o/r"}`)
	in = `{"id":"PR_1","number":10,"title":"Fix","state":"MERGED","url":"https://github.com/o/r/pull/10",` +
		`"repository":{"nameWithOwner":"o/r"}}`
	assertJSON(t, decodeRaw[gqlReferenceRaw](t, in).project(), `{"id":"PR_1","number":10,"title":"Fix","state":"MERGED",`+
		`"url":"https://github.com/o/r/pull/10","repository":"o/r"}`)
}

func TestGraphQLBoundsApply(t *testing.T) {
	long := strings.Repeat("ä", projectionTextLimit+5)
	name := strings.Repeat("n", projectionNameLimit+5)
	farURL := "https://github.com/" + strings.Repeat("p", projectionURLLimit)
	raw := decodeRaw[gqlLabelRaw](t, `{"name":"`+name+`","description":"`+long+`","url":"`+farURL+`",`+
		`"resourcePath":"/a\u0000b"}`)
	got := raw.project()
	if n := len([]rune(*got.Name)); n != projectionNameLimit {
		t.Errorf("name runes = %d", n)
	}
	if n := len([]rune(*got.Description)); n != projectionTextLimit {
		t.Errorf("description runes = %d", n)
	}
	if got.URL != nil || got.Links != nil {
		t.Errorf("an over-long or unclean address must be left out, not cut: %v %v", got.URL, got.Links)
	}
	account := decodeRaw[gqlAccountRaw](t, `{"login":"a\u0007b","avatarUrl":"`+farURL+`"}`).project()
	if *account.Login != "a b" || account.AvatarURL != nil {
		t.Errorf("account = %+v", account)
	}
}

// graphQLProjections ties each view to its raw type, selection, schema, and fields.
var graphQLProjections = []struct {
	name      string
	raw       any
	view      any
	selection string
	schema    string
	fields    []capability.Field
}{
	{"account", gqlAccountRaw{}, GraphQLAccount{}, gqlAccountSelection, gqlAccountSchema, gqlAccountFields},
	{"git actor", gqlGitActorRaw{}, GraphQLGitActor{}, gqlGitActorSelection, gqlGitActorSchema, gqlGitActorFields},
	{"reaction group", gqlReactionGroupRaw{}, GraphQLReactionGroup{}, gqlReactionGroupSelection, gqlReactionGroupSchema, gqlReactionGroupFields},
	{"label", gqlLabelRaw{}, GraphQLLabel{}, gqlLabelSelection, gqlLabelSchema, gqlLabelFields},
	{"milestone", gqlMilestoneRaw{}, GraphQLMilestone{}, gqlMilestoneSelection, gqlMilestoneSchema, gqlMilestoneFields},
	{"issue type", gqlIssueTypeRaw{}, GraphQLIssueType{}, gqlIssueTypeSelection, gqlIssueTypeSchema, gqlIssueTypeFields},
	{"reference", gqlReferenceRaw{}, GraphQLReference{}, gqlReferenceSelection, gqlReferenceSchema, gqlReferenceFields},
}

func TestGraphQLProjectionsAgreeWithSelectionSchemaAndFields(t *testing.T) {
	for _, p := range graphQLProjections {
		t.Run(p.name, func(t *testing.T) {
			// Every member the raw type decodes is asked for.
			rt := reflect.TypeOf(p.raw)
			for i := 0; i < rt.NumField(); i++ {
				tag, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
				if !strings.Contains(p.selection, tag) {
					t.Errorf("%s is decoded but not selected", tag)
				}
			}
			// Every member the view emits is in the schema, and fields and schema name the same members.
			var schema map[string]any
			if err := json.Unmarshal([]byte(p.schema), &schema); err != nil {
				t.Fatal(err)
			}
			if missing := outputMembers(reflect.TypeOf(p.view), schema, "", 0); len(missing) > 0 {
				t.Errorf("not in schema: %v", missing)
			}
			properties, _ := schema["properties"].(map[string]any)
			named := map[string]bool{}
			for _, f := range p.fields {
				named[f.Name] = true
				if _, ok := properties[f.Name]; !ok {
					t.Errorf("field %s without schema member", f.Name)
				}
			}
			for name := range properties {
				if !named[name] {
					t.Errorf("schema member %s without field", name)
				}
			}
		})
	}
}
