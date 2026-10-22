package penpot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	photoID  = "00000000-0000-0000-0000-0000000000a1"
	thumbID  = "00000000-0000-0000-0000-0000000000a2"
	orgID    = "00000000-0000-0000-0000-0000000000a3"
	ownerID  = "00000000-0000-0000-0000-0000000000a4"
	imageURL = "https://assets.invalid/organization-image"
)

// fullTeamsBody answers get-teams as Penpot 2.18.0 does with the subscriptions and admin-console flags: every
// column of the team table, the permissions, the subscription, and the organization. The organization's image
// addresses, deleted-at, and share-id are sent, too, and must never reach the output.
func fullTeamsBody() string {
	return `[{"id":"` + teamA + `","name":"Kunde A","is-default":true,"created-at":"2026-01-02T03:04:05.123Z",` +
		`"modified-at":"2026-02-03T04:05:06.789Z","deleted-at":null,"photo-id":"` + photoID + `",` +
		`"features":["fdata/shape-data-type","components/v2"],"share-id":"x",` +
		`"permissions":{"type":"membership","is-owner":true,"is-admin":true,"can-edit":false},` +
		`"subscription":{"type":"unlimited","status":"active","seats":12},` +
		`"organization":{"id":"` + orgID + `","name":"Agentur\u0007 Org","slug":"agentur","owner-id":"` + ownerID + `",` +
		`"custom-photo":"` + imageURL + `","avatar-bg-url":"` + imageURL + `","expired-license":false,"sso-active":true,` +
		`"permissions":{"create-teams":"any","delete-teams":"onlyOwners","move-teams":"always","new-team-members":"anyone"}}},` +
		`{"id":"` + teamB + `","name":"Kunde B","permissions":{"type":"membership","is-owner":false,"is-admin":false,"can-edit":true}}]`
}

func fullProjectsBody() string {
	return `[{"id":"` + projectA1 + `","name":"Web","team-id":"` + teamA + `","count":3,"total-count":5,"is-pinned":true,` +
		`"is-default":true,"created-at":"2026-01-01T00:00:00Z","modified-at":"2026-09-01T10:00:00Z","deleted-at":null},` +
		`{"id":"` + projectA2 + `","name":"Alt","team-id":"` + teamA + `","count":0,"total-count":0,"is-default":false,` +
		`"created-at":"2025-12-01T00:00:00Z","modified-at":"2026-08-01T10:00:00Z","deleted-at":"2026-10-01T09:00:00Z"}]`
}

func fullFilesBody() string {
	return `[{"id":"` + fileA1 + `","project-id":"` + projectA1 + `","team-id":"` + teamA + `","name":"Startseite",` +
		`"created-at":"2026-03-01T08:00:00Z","modified-at":"2026-09-02T08:00:00Z","revn":7,"vern":2,"is-shared":true,` +
		`"thumbnail-id":"` + thumbID + `","deleted-at":null}]`
}

func fullLibrariesBody() string {
	return `[{"id":"` + libA1 + `","name":"Lib","project-id":"` + projectA1 + `","team-id":"` + teamA + `",` +
		`"features":["components/v2","fdata/path-data"],"created-at":"2026-03-01T08:00:00Z","modified-at":"2026-09-02T08:00:00Z",` +
		`"deleted-at":null,"revn":9,"vern":1,"version":67,"synced-at":"2026-09-03T08:00:00Z","is-shared":true,"is-indirect":true}]`
}

// fullThreadsBody answers get-comment-threads as Penpot 2.18.0 does: every column of the thread plus the joined
// author, file, team, project, content, and the counters.
func fullThreadsBody() string {
	return `[{"id":"` + threadA + `","file-id":"` + fileA1 + `","owner-id":"` + ownerA + `","page-id":"` + pageID + `",` +
		`"frame-id":"` + frameA + `","position":{"x":1.5,"y":2},"is-resolved":true,"seqn":3,` +
		`"participants":["` + ownerA + `","` + profileP + `"],"mentions":["` + profileQ + `"],` +
		`"created-at":"2026-09-03T08:00:00Z","modified-at":"2026-09-04T08:00:00Z","page-name":"Seite 1",` +
		`"content":"Erster Text","count-comments":2,"count-unread-comments":0,"file-name":"Start\u0007seite",` +
		`"team-id":"` + teamA + `","project-id":"` + projectA1 + `","owner-fullname":"Ada\u0007 Lovelace",` +
		`"owner-email":"` + personal + `","owner-photo-id":"` + avatarID + `"}]`
}

// fullCommentsBody answers get-comments as Penpot 2.18.0 does: every column of the comment plus the file and
// the author.
func fullCommentsBody() string {
	return `[{"id":"` + commentA + `","thread-id":"` + threadA + `","owner-id":"` + ownerA + `",` +
		`"created-at":"2026-09-03T08:00:00Z","modified-at":"2026-09-04T08:00:00Z","content":"Erster Text",` +
		`"mentions":["` + profileP + `","` + profileQ + `"],"file-id":"` + fileA1 + `","owner-fullname":"Ada Lovelace",` +
		`"owner-email":"` + personal + `","owner-photo-id":"` + avatarID + `"}]`
}

func structureHandler(c call) (*http.Response, error) {
	switch c.command() {
	case cmdThreads:
		return jsonResponse(200, fullThreadsBody()), nil
	case cmdComments:
		return jsonResponse(200, fullCommentsBody()), nil
	case cmdTeams:
		return jsonResponse(200, fullTeamsBody()), nil
	case cmdProjects:
		if c.body["team-id"] == teamA {
			return jsonResponse(200, fullProjectsBody()), nil
		}
	case cmdProjectFile:
		if c.body["project-id"] == projectA1 {
			return jsonResponse(200, fullFilesBody()), nil
		}
	case cmdFileLibraries:
		return jsonResponse(200, fullLibrariesBody()), nil
	}
	return libraryHandler(200, fullLibrariesBody())(c)
}

// fieldCase is one tool with a fake answer that fills every member Penpot sends. A later tool of the metadata
// listings adds its case here.
type fieldCase struct {
	descriptor capability.Descriptor
	connection string
	arguments  string
}

func structureFieldCases() []fieldCase {
	return []fieldCase{
		{teamsList, "two", `{}`},
		{projectsList, "one", `{"team_id":"` + teamA + `"}`},
		{filesList, "one", `{"project_id":"` + projectA1 + `"}`},
		{librariesList, "one", `{` + fileArgs + `}`},
		{commentsThreads, "one", `{` + fileArgs + `}`},
		{commentsList, "one", `{` + fileArgs + `,"thread_id":"` + threadA + `"}`},
	}
}

// collectKeys lists the members of a decoded output against the schema and reports one that the schema lacks.
// seen records every schema path that the output filled.
func collectKeys(t *testing.T, tool, path string, value any, schema map[string]any, seen map[string]bool) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for key, member := range v {
			sub, ok := properties[key].(map[string]any)
			if !ok {
				t.Errorf("%s: output member %s.%s is not in the output schema", tool, path, key)
				continue
			}
			seen[path+"."+key] = true
			collectKeys(t, tool, path+"."+key, member, sub, seen)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, item := range v {
			collectKeys(t, tool, path, item, items, seen)
		}
	}
}

// schemaPaths lists every member path of a schema, below arrays as well.
func schemaPaths(path string, schema map[string]any, out *[]string) {
	if items, ok := schema["items"].(map[string]any); ok {
		schemaPaths(path, items, out)
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, member := range properties {
		*out = append(*out, path+"."+key)
		if sub, ok := member.(map[string]any); ok {
			schemaPaths(path+"."+key, sub, out)
		}
	}
}

func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, structureHandler)
	for _, tc := range structureFieldCases() {
		t.Run(tc.descriptor.ID, func(t *testing.T) {
			result, err := env.invoke(tc.descriptor.ID, tc.connection, tc.arguments)
			if err != nil {
				t.Fatal(err)
			}
			var output map[string]any
			if err := json.Unmarshal([]byte(result), &output); err != nil {
				t.Fatal(err)
			}
			var schema map[string]any
			if err := json.Unmarshal(tc.descriptor.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			collectKeys(t, tc.descriptor.ID, "", output, schema, seen)
			var all []string
			schemaPaths("", schema, &all)
			sort.Strings(all)
			for _, path := range all {
				if !seen[path] {
					t.Errorf("%s: schema member %s is not filled by the fake answer", tc.descriptor.ID, path)
				}
			}
			described := map[string]string{}
			for _, field := range tc.descriptor.Fields {
				described[field.Name] = field.Description
			}
			for key, member := range output {
				description, ok := described[key]
				if !ok {
					t.Errorf("%s: output member %s has no field description", tc.descriptor.ID, key)
					continue
				}
				names := map[string]bool{}
				collectNames(member, names)
				for name := range names {
					if !strings.Contains(description, name) {
						t.Errorf("%s: field %s does not describe %s", tc.descriptor.ID, key, name)
					}
				}
			}
			for _, secret := range []string{"custom_photo", "custom-photo", "avatar", imageURL, "share_id", "share-id", "deleted-at"} {
				if strings.Contains(result, secret) {
					t.Errorf("%s: output carries %q: %s", tc.descriptor.ID, secret, result)
				}
			}
			if tc.descriptor.ID != projectsList.ID && strings.Contains(result, "deleted_at") {
				t.Errorf("%s: output carries deleted_at: %s", tc.descriptor.ID, result)
			}
		})
	}
}

// collectNames gathers the member names of the entries of a container, at every depth.
func collectNames(value any, out map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for key, member := range v {
			out[key] = true
			collectNames(member, out)
		}
	case []any:
		for _, item := range v {
			collectNames(item, out)
		}
	}
}

func decodeList(t *testing.T, result, container string) []map[string]any {
	t.Helper()
	var decoded map[string]json.RawMessage
	var entries []map[string]any
	if err := json.Unmarshal([]byte(result), &decoded); err != nil || json.Unmarshal(decoded[container], &entries) != nil {
		t.Fatalf("%v: %s", err, result)
	}
	return entries
}

func TestTeamsListReportsPermissionsSubscriptionAndOrganization(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, structureHandler)
	result, err := env.invoke(teamsList.ID, "two", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	teams := decodeList(t, result, "teams")
	if len(teams) != 2 {
		t.Fatalf("result = %s", result)
	}
	wantA := map[string]any{"id": teamA, "name": "Kunde A", "is_default": true, "created_at": "2026-01-02T03:04:05.123Z",
		"modified_at": "2026-02-03T04:05:06.789Z", "photo_id": photoID,
		"features":     []any{"fdata/shape-data-type", "components/v2"},
		"permissions":  map[string]any{"type": "membership", "is_owner": true, "is_admin": true, "can_edit": false},
		"subscription": map[string]any{"type": "unlimited", "status": "active", "seats": 12.0},
		"organization": map[string]any{"id": orgID, "name": "Agentur  Org", "slug": "agentur", "owner_id": ownerID,
			"expired_license": false, "sso_active": true,
			"permissions": map[string]any{"create_teams": "any", "delete_teams": "onlyOwners", "move_teams": "always",
				"new_team_members": "anyone"}}}
	if !reflect.DeepEqual(teams[0], wantA) {
		t.Errorf("team A = %#v\nwant   = %#v", teams[0], wantA)
	}
	wantB := map[string]any{"id": teamB, "name": "Kunde B",
		"permissions": map[string]any{"type": "membership", "is_owner": false, "is_admin": false, "can_edit": true}}
	if !reflect.DeepEqual(teams[1], wantB) {
		t.Errorf("team B = %#v\nwant   = %#v", teams[1], wantB)
	}
}

func TestListingsWithoutNewMembersKeepTheirFormerOutput(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		switch c.command() {
		case cmdTeams:
			return jsonResponse(200, `[{"id":"`+teamA+`","name":"Kunde A","isDefault":true}]`), nil
		case cmdProjects:
			return jsonResponse(200, `[{"id":"`+projectA1+`","name":"Web","team-id":"`+teamA+`","count":3,"is-pinned":true,`+
				`"modified-at":"2026-09-01T10:00:00Z"}]`), nil
		case cmdProjectFile:
			return jsonResponse(200, `[{"id":"`+fileA1+`","name":"Start","projectId":"`+projectA1+`","revn":7,"isShared":true,`+
				`"modifiedAt":"2026-09-02T08:00:00Z"}]`), nil
		}
		return libraryHandler(200, `[{"id":"`+libA1+`","name":"Lib","projectId":"`+projectA1+`","teamId":"`+teamA+`",`+
			`"isShared":true,"isIndirect":false,"features":[],"permissions":{}}]`)(c)
	})
	for _, tc := range []struct{ tool, arguments, want string }{
		{teamsList.ID, `{}`, `{"teams":[{"id":"` + teamA + `","name":"Kunde A","is_default":true}],"count":1,"truncated":false}`},
		{projectsList.ID, `{"team_id":"` + teamA + `"}`, `{"projects":[{"id":"` + projectA1 + `","name":"Web","team_id":"` + teamA +
			`","file_count":3,"is_pinned":true,"modified_at":"2026-09-01T10:00:00Z"}],"count":1,"truncated":false}`},
		{filesList.ID, `{"project_id":"` + projectA1 + `"}`, `{"files":[{"id":"` + fileA1 + `","name":"Start","project_id":"` +
			projectA1 + `","modified_at":"2026-09-02T08:00:00Z","revn":7,"is_shared":true}],"count":1,"truncated":false}`},
		{librariesList.ID, `{` + fileArgs + `}`, `{"libraries":[{"id":"` + libA1 + `","name":"Lib","project_id":"` + projectA1 +
			`","team_id":"` + teamA + `","is_shared":true}],"count":1,"outside_targets":0,"truncated":false}`},
	} {
		result, err := env.invoke(tc.tool, "one", tc.arguments)
		var got, want any
		if err != nil || json.Unmarshal([]byte(result), &got) != nil || json.Unmarshal([]byte(tc.want), &want) != nil ||
			!reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %s\nwant %s\nerr %v", tc.tool, result, tc.want, err)
		}
	}
}

func TestProjectsListKeepsDeletedProjectsWithDeletedAt(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, structureHandler)
	result, err := env.invoke(projectsList.ID, "one", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	projects := decodeList(t, result, "projects")
	want := []map[string]any{
		{"id": projectA1, "name": "Web", "team_id": teamA, "file_count": 3.0, "total_count": 5.0, "is_pinned": true,
			"is_default": true, "created_at": "2026-01-01T00:00:00Z", "modified_at": "2026-09-01T10:00:00Z"},
		{"id": projectA2, "name": "Alt", "team_id": teamA, "total_count": 0.0, "is_default": false,
			"created_at": "2025-12-01T00:00:00Z", "modified_at": "2026-08-01T10:00:00Z", "deleted_at": "2026-10-01T09:00:00Z"},
	}
	if !reflect.DeepEqual(projects, want) {
		t.Errorf("projects = %#v\nwant     = %#v", projects, want)
	}
	// The target binding still goes through the project list, deleted projects included.
	calls = nil
	if _, err := env.invoke(filesList.ID, "one", `{"project_id":"`+projectA2+`"}`); err != nil {
		t.Errorf("a deleted project is no longer found: %v", err)
	}
}

func TestFilesListReportsTheListedMetadata(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, structureHandler)
	result, err := env.invoke(filesList.ID, "one", `{"project_id":"`+projectA1+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"id": fileA1, "name": "Startseite", "project_id": projectA1, "team_id": teamA,
		"created_at": "2026-03-01T08:00:00Z", "modified_at": "2026-09-02T08:00:00Z", "revn": 7.0, "vern": 2.0,
		"is_shared": true, "thumbnail_id": thumbID}}
	if got := decodeList(t, result, "files"); !reflect.DeepEqual(got, want) {
		t.Errorf("files = %#v\nwant  = %#v", got, want)
	}
}

func TestFilesOfAForeignTeamAreDropped(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.command() == cmdProjectFile {
			return jsonResponse(200, `[{"id":"`+fileA1+`","name":"Eigen","project-id":"`+projectA1+`","team-id":"`+teamA+`"},`+
				`{"id":"`+fileOut+`","name":"Fremd","project-id":"`+projectA1+`","team-id":"`+teamForeign+`"}]`), nil
		}
		return projectsHandler(c)
	})
	result, err := env.invoke(filesList.ID, "one", `{"project_id":"`+projectA1+`"}`)
	if err != nil || strings.Contains(result, "Fremd") || !strings.Contains(result, "Eigen") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestLibrariesListReportsMetadataOfLibrariesInsideTheTargets(t *testing.T) {
	var calls []call
	answer := `[` + strings.Trim(fullLibrariesBody(), "[]") + `,{"id":"` + libForeign + `","name":"Fremd","project-id":"` + projectOut +
		`","team-id":"` + teamForeign + `","features":["secret/feature"],"revn":3,"version":1}]`
	env := newEnvironment(t, &calls, libraryHandler(200, answer))
	result, err := env.invoke(librariesList.ID, "one", `{`+fileArgs+`}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"id": libA1, "name": "Lib", "project_id": projectA1, "team_id": teamA, "is_shared": true,
		"is_indirect": true, "features": []any{"components/v2", "fdata/path-data"}, "created_at": "2026-03-01T08:00:00Z",
		"modified_at": "2026-09-02T08:00:00Z", "revn": 9.0, "vern": 1.0, "version": 67.0, "synced_at": "2026-09-03T08:00:00Z"}}
	if got := decodeList(t, result, "libraries"); !reflect.DeepEqual(got, want) {
		t.Errorf("libraries = %#v\nwant      = %#v", got, want)
	}
	if !strings.Contains(result, `"outside_targets":1`) || strings.Contains(result, "secret/feature") ||
		strings.Contains(result, "Fremd") {
		t.Errorf("result = %s", result)
	}
}

func TestFeatureListsAndWholeNumbersAreBounded(t *testing.T) {
	var list []string
	for i := 0; i < 150; i++ {
		list = append(list, `"f`+strings.Repeat("x", i%3)+`"`)
	}
	var calls []call
	env := newEnvironment(t, &calls, func(call) (*http.Response, error) {
		return jsonResponse(200, `[{"id":"`+teamA+`","name":"T","features":[`+strings.Join(list, ",")+`,5,null],`+
			`"subscription":{"seats":1.5,"type":"`+strings.Repeat("ä", 400)+`"}}]`), nil
	})
	result, err := env.invoke(teamsList.ID, "one", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	team := decodeList(t, result, "teams")[0]
	if got := len(team["features"].([]any)); got != 100 {
		t.Errorf("features = %d, want 100", got)
	}
	subscription := team["subscription"].(map[string]any)
	if _, ok := subscription["seats"]; ok || len(subscription["type"].(string)) > maxStringLength {
		t.Errorf("subscription = %#v", subscription)
	}
}

func decodeOne(t *testing.T, result, container string) map[string]any {
	t.Helper()
	items := decodeList(t, result, container)
	if len(items) != 1 {
		t.Fatalf("%s: %s", container, result)
	}
	return items[0]
}

func TestCommentsReportParticipantsMentionsAndAuthors(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, structureHandler)
	result, err := env.invoke(commentsThreads.ID, "one", `{`+fileArgs+`}`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": threadA, "file_id": fileA1, "page_id": pageID, "page_name": "Seite 1", "frame_id": frameA,
		"owner_id": ownerA, "x": 1.5, "y": 2.0, "is_resolved": true, "comment_count": 2.0, "seqn": 3.0, "excerpt": "Erster Text",
		"created_at": "2026-09-03T08:00:00Z", "modified_at": "2026-09-04T08:00:00Z",
		"participants": []any{ownerA, profileP}, "mentions": []any{profileQ}, "count_unread_comments": 0.0,
		"file_name": "Start seite", "team_id": teamA, "project_id": projectA1, "owner_fullname": "Ada  Lovelace",
		"owner_email": personal, "owner_photo_id": avatarID}
	if got := decodeOne(t, result, "threads"); !reflect.DeepEqual(got, want) {
		t.Errorf("thread:\n got %v\nwant %v", got, want)
	}
	result, err = env.invoke(commentsList.ID, "one", `{`+fileArgs+`,"thread_id":"`+threadA+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]any{"id": commentA, "thread_id": threadA, "owner_id": ownerA, "content": "Erster Text",
		"created_at": "2026-09-03T08:00:00Z", "modified_at": "2026-09-04T08:00:00Z",
		"mentions": []any{profileP, profileQ}, "file_id": fileA1, "owner_fullname": "Ada Lovelace",
		"owner_email": personal, "owner_photo_id": avatarID}
	if got := decodeOne(t, result, "comments"); !reflect.DeepEqual(got, want) {
		t.Errorf("comment:\n got %v\nwant %v", got, want)
	}
}

func TestCommentsWithoutNewMembersKeepTheirFormerOutput(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		switch c.command() {
		case cmdThreads:
			return jsonResponse(200, `[{"id":"`+threadA+`","fileId":"`+fileA1+`","ownerId":"`+ownerA+`","isResolved":false,`+
				`"countComments":2,"content":"Erster Text","participants":[],"mentions":null}]`), nil
		case cmdComments:
			return jsonResponse(200, `[{"id":"`+commentA+`","threadId":"`+threadA+`","content":"Text","mentions":[]}]`), nil
		}
		return filesHandler(0)(c)
	})
	for _, tc := range []struct{ tool, arguments, want string }{
		{commentsThreads.ID, `{` + fileArgs + `}`, `{"threads":[{"id":"` + threadA + `","file_id":"` + fileA1 + `","owner_id":"` +
			ownerA + `","is_resolved":false,"comment_count":2,"excerpt":"Erster Text"}],"count":1,"truncated":false}`},
		{commentsList.ID, `{` + fileArgs + `,"thread_id":"` + threadA + `"}`, `{"comments":[{"id":"` + commentA +
			`","thread_id":"` + threadA + `","content":"Text"}],"count":1,"truncated":false}`},
	} {
		result, err := env.invoke(tc.tool, "one", tc.arguments)
		var got, want any
		if err != nil || json.Unmarshal([]byte(result), &got) != nil || json.Unmarshal([]byte(tc.want), &want) != nil ||
			!reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %s\nwant %s\nerr %v", tc.tool, result, tc.want, err)
		}
	}
}

func TestCommentPeopleListsAreBoundedAndOutsideTargetsKeepIdentifiersOnly(t *testing.T) {
	var ids []string
	for i := 0; i < 120; i++ {
		ids = append(ids, fmt.Sprintf(`"00000000-0000-0000-0001-%012d"`, i), `"not-a-uuid"`)
	}
	var calls []call
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.command() == cmdThreads {
			return jsonResponse(200, `[{"id":"`+threadA+`","fileId":"`+fileA1+`","teamId":"`+teamB+`","fileName":"Fremd",`+
				`"ownerId":"`+ownerA+`","ownerFullname":"Ada","ownerEmail":"`+personal+`","ownerPhotoId":"`+avatarID+`",`+
				`"participants":[`+strings.Join(ids, ",")+`],"countUnreadComments":4.5}]`), nil
		}
		return filesHandler(0)(c)
	})
	result, err := env.invoke(commentsThreads.ID, "one", `{`+fileArgs+`}`)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeOne(t, result, "threads")
	if n := len(got["participants"].([]any)); n != 100 {
		t.Errorf("participants = %d", n)
	}
	for _, key := range []string{"file_name", "owner_fullname", "owner_email", "count_unread_comments"} {
		if _, ok := got[key]; ok {
			t.Errorf("%s given out: %s", key, result)
		}
	}
	if got["team_id"] != teamB || got["owner_photo_id"] != avatarID || got["owner_id"] != ownerA {
		t.Errorf("identifiers missing: %s", result)
	}
}
