package infomaniakdrive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	sharingPersonal  = "ann@example.invalid"
	sharingAvatarURL = "https://avatar.example.invalid/7.png"
)

// A dropbox answered with every documented member, including the security details of its creator that
// must never reach the output.
func dropboxFullBody(directoryID int64, parents string) string {
	return fmt.Sprintf(`{"id":12,"uuid":"0b1c2d3e","name":"Invoices","url":%q,"users_count":3,"created_by":7,`+
		`"created_at":1735689600,"updated_at":1735776000,"last_uploaded_at":null,"capabilities":{"has_password":true,`+
		`"has_notification":false,"has_validity":true,"has_size_limit":true,"validity":"2026-06-01 00:00:00","size":"1073741824"},`+
		`"directory":{"id":%d,"name":"Invoices","type":"dir","parent_id":42,"status":"ok","last_modified_at":1735776000,`+
		`"path":"/Common/Invoices","capabilities":{"can_read":true,"can_write":false},"parents":%s},`+
		`"user":{"id":7,"display_name":"Ann Example","first_name":"Ann","last_name":"Example","email":%q,"is_sso":false,`+
		`"avatar":%q,"deleted_at":1736035200,"type":"admin","teams":[3],"security":{"assistant_at":1,"double_auth_method":"totp",`+
		`"has_double_auth":true,"has_recovery_email":true,"has_valid_email":1,"has_valid_phone":true,`+
		`"last_password_at":1735689600,"score":9}}}`,
		dropboxURL, directoryID, parents, sharingPersonal, sharingAvatarURL)
}

const dropboxAllWith = `["user","directory","directory.capabilities","directory.path","directory.parents"]`

func accessFullBody() string {
	return `{"users":[{"id":7,"access":"user","name":"Ann","right":"read","color":2,"status":"accepted"}],` +
		`"teams":[{"id":3,"access":"team","name":"Team A","right":null,"color":null,"status":"accepted"}],` +
		`"invitations":[{"id":9,"access":"invitation","name":"` + inviteeEmail + `","right":"write","color":0,"status":"pending"}],` +
		`"parent_access":{"mismatch":true,"name":"Common","users":[{"id":8,"access":"user","name":"Bob","right":"manage","color":5,"status":"accepted"}],` +
		`"teams":[{"id":4,"access":"team","name":"Team B","right":"read","color":1,"status":"accepted"}]}}`
}

func sharingAnswer(body string) func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(body)), nil
	})
}

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result %q: %v", text, err)
	}
	return out
}

func objectAt(t *testing.T, root map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := root
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			t.Fatalf("no object at %v in %v", path, root)
		}
		cur = next
	}
	return cur
}

func TestLinksGetReturnsCreatorAndViewsOnRequest(t *testing.T) {
	body := strings.Replace(linkJSONOf(childFileID, "inherit", linkURL), `"access_blocked":false}`,
		`"access_blocked":false,"views":42}`, 1)
	for name, tc := range map[string]struct {
		args, query string
		views       any
	}{
		"without with": {linkArgs(""), "", nil},
		"views":        {linkArgs(`,"with":["views"]`), "with=views", float64(42)},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, sharingAnswer(body), nil)
			result, err := env.invoke(linksGet.ID, "links", tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || calls[1].query.Encode() != tc.query {
				t.Fatalf("calls = %+v", calls)
			}
			link := objectAt(t, decodeObject(t, result), "link")
			if link["created_by"] != float64(7) || link["views"] != tc.views {
				t.Fatalf("link = %v", link)
			}
		})
	}
}

func TestLinksGetWithoutNewFieldsKeepsItsFormerOutput(t *testing.T) {
	legacy := fmt.Sprintf(`{"url":%q,"file_id":%d,"right":"inherit","valid_until":null,"created_at":null,"updated_at":null,`+
		`"capabilities":{"can_edit":false,"can_see_stats":true,"can_see_info":false,"can_download":true,"can_comment":false,`+
		`"can_request_access":false},"access_blocked":false}`, linkURL, childFileID)
	var calls []call
	env := newEnvironment(t, &calls, sharingAnswer(legacy), nil)
	result, err := env.invoke(linksGet.ID, "links", linkArgs(""))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"drive_id":5001,"file_id":43,"link":{"access_blocked":false,"can_comment":false,"can_download":true,` +
		`"can_edit":false,"can_request_access":false,"can_see_info":false,"can_see_stats":true,"file_id":43,` +
		`"right":"inherit","url":"` + linkURL + `"}}`
	if result != want {
		t.Fatalf("result = %s\nwant   = %s", result, want)
	}
}

func TestDropboxGetWithoutNewFieldsKeepsItsFormerOutput(t *testing.T) {
	legacy := fmt.Sprintf(`{"id":12,"uuid":"0b1c2d3e","name":"Invoices","url":%q,"users_count":3,"created_at":1735689600,`+
		`"updated_at":1735776000,"last_uploaded_at":null,"capabilities":{"has_password":true,"has_notification":false,`+
		`"has_validity":true,"has_size_limit":false}}`, dropboxURL)
	var calls []call
	env := newEnvironment(t, &calls, sharingAnswer(legacy), nil)
	result, err := env.invoke(dropboxGet.ID, "dropbox", dropboxArgs(""))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"drive_id":5001,"dropbox":{"created_at":"2025-01-01T00:00:00Z","has_notification":false,"has_password":true,` +
		`"has_size_limit":false,"has_validity":true,"id":12,"name":"Invoices","updated_at":"2025-01-02T00:00:00Z",` +
		`"url":"` + dropboxURL + `","users_count":3,"uuid":"0b1c2d3e"},"file_id":43}`
	if result != want {
		t.Fatalf("result = %s\nwant   = %s", result, want)
	}
}

func TestAccessGetWithoutNewFieldsKeepsItsFormerOutput(t *testing.T) {
	legacy := `{"users":[{"id":7,"access":"user","name":"Ann","right":"read","status":"accepted"}],"teams":[],"invitations":[]}`
	var calls []call
	env := newEnvironment(t, &calls, sharingAnswer(legacy), nil)
	result, err := env.invoke(accessGet.ID, "access", accessArgs(""))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"drive_id":5001,"file_id":43,"truncated":false,` +
		`"users":[{"access":"user","id":7,"name":"Ann","right":"read","status":"accepted"}]}`
	if result != want {
		t.Fatalf("result = %s\nwant   = %s", result, want)
	}
}

func TestDropboxGetReturnsEveryDocumentedMemberAndNoSecurityDetails(t *testing.T) {
	parents := `[{"id":1,"name":"Root","type":"dir","parent_id":0},{"id":42,"name":"Common","type":"dir","parent_id":1}]`
	var calls []call
	env := newEnvironment(t, &calls, sharingAnswer(dropboxFullBody(childFileID, parents)), nil)
	result, err := env.invoke(dropboxGet.ID, "dropbox", dropboxArgs(`,"with":`+dropboxAllWith))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1].query.Get("with") != "capabilities,user,directory,directory.capabilities,directory.path,directory.parents" {
		t.Fatalf("calls = %+v", calls)
	}
	for _, leaked := range []string{"security", "score", "double_auth", "last_password", "admin", `"teams":[3]`} {
		if strings.Contains(result, leaked) {
			t.Fatalf("result leaks %q: %s", leaked, result)
		}
	}
	box := objectAt(t, decodeObject(t, result), "dropbox")
	if box["created_by"] != float64(7) || box["validity"] != "2026-06-01 00:00:00" || box["size"] != "1073741824" ||
		box["has_size_limit"] != true {
		t.Fatalf("dropbox = %v", box)
	}
	user := objectAt(t, box, "user")
	want := map[string]any{"id": float64(7), "display_name": "Ann Example", "first_name": "Ann", "last_name": "Example",
		"email": sharingPersonal, "is_sso": false, "avatar": sharingAvatarURL, "deleted_at": "2025-01-05T00:00:00Z"}
	if len(user) != len(want) {
		t.Fatalf("user = %v", user)
	}
	for key, value := range want {
		if user[key] != value {
			t.Fatalf("user[%s] = %v, want %v", key, user[key], value)
		}
	}
	directory := objectAt(t, box, "directory")
	parentList, _ := directory["parents"].([]any)
	if directory["id"] != float64(childFileID) || directory["path"] != "/Common/Invoices" || len(parentList) != 2 ||
		objectAt(t, directory, "capabilities")["can_read"] != true {
		t.Fatalf("directory = %v", directory)
	}
}

func TestDropboxGetLoadsOnlyWhatWithAsks(t *testing.T) {
	for name, tc := range map[string]struct {
		with, query string
		directory   bool
		user        bool
		path        bool
		parents     bool
		capability  bool
	}{
		"none":         {"", "capabilities", false, false, false, false, false},
		"user":         {`"user"`, "capabilities,user", false, true, false, false, false},
		"directory":    {`"directory"`, "capabilities,directory", true, false, false, false, false},
		"path":         {`"directory","directory.path"`, "capabilities,directory,directory.path", true, false, true, false, false},
		"parents":      {`"directory.parents"`, "capabilities,directory.parents", true, false, false, true, false},
		"capabilities": {`"directory.capabilities"`, "capabilities,directory.capabilities", true, false, false, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			parents := `[{"id":1,"name":"Root","type":"dir","parent_id":0}]`
			env := newEnvironment(t, &calls, sharingAnswer(dropboxFullBody(childFileID, parents)), nil)
			args := dropboxArgs("")
			if tc.with != "" {
				args = dropboxArgs(`,"with":[` + tc.with + `]`)
			}
			result, err := env.invoke(dropboxGet.ID, "dropbox", args)
			if err != nil {
				t.Fatal(err)
			}
			if calls[1].query.Get("with") != tc.query {
				t.Fatalf("with = %q, want %q", calls[1].query.Get("with"), tc.query)
			}
			box := objectAt(t, decodeObject(t, result), "dropbox")
			_, hasUser := box["user"]
			directory, hasDirectory := box["directory"].(map[string]any)
			if hasUser != tc.user || hasDirectory != tc.directory {
				t.Fatalf("dropbox = %v", box)
			}
			if tc.directory {
				_, path := directory["path"]
				_, parentsOut := directory["parents"]
				_, capabilities := directory["capabilities"]
				if path != tc.path || parentsOut != tc.parents || capabilities != tc.capability {
					t.Fatalf("directory = %v", directory)
				}
			}
		})
	}
}

func TestDropboxGetUnsafeAddressesAndTextsAreDropped(t *testing.T) {
	long := "https://avatar.example.invalid/" + strings.Repeat("a", 1100)
	for name, avatar := range map[string]string{"http": "http://avatar.example.invalid/a.png", "javascript": "javascript:alert(1)",
		"userinfo": "https://u:p" + "@" + "avatar.example.invalid/a.png", "long": long, "empty": ""} {
		t.Run(name, func(t *testing.T) {
			body := strings.Replace(dropboxFullBody(childFileID, `[]`), sharingAvatarURL, avatar, 1)
			body = strings.Replace(body, `"Ann Example"`, `"Ann\u0007 `+strings.Repeat("é", 300)+`"`, 1)
			var calls []call
			env := newEnvironment(t, &calls, sharingAnswer(body), nil)
			result, err := env.invoke(dropboxGet.ID, "dropbox", dropboxArgs(`,"with":["user"]`))
			if err != nil {
				t.Fatal(err)
			}
			user := objectAt(t, decodeObject(t, result), "dropbox", "user")
			name, _ := user["display_name"].(string)
			if _, has := user["avatar"]; has || strings.ContainsRune(name, 7) || len(name) > maxDropboxText {
				t.Fatalf("user = %v", user)
			}
		})
	}
}

func TestAccessGetReturnsColorsAndParentAccess(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, sharingAnswer(accessFullBody()), nil)
	result, err := env.invoke(accessGet.ID, "access", accessArgs(`,"with":["parent_access"]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1].query.Encode() != "with=parent_access" {
		t.Fatalf("calls = %+v", calls)
	}
	out := decodeObject(t, result)
	users := out["users"].([]any)
	teams := out["teams"].([]any)
	invitations := out["invitations"].([]any)
	if users[0].(map[string]any)["color"] != float64(2) || invitations[0].(map[string]any)["color"] != float64(0) {
		t.Fatalf("colours = %v %v", users, invitations)
	}
	if _, has := teams[0].(map[string]any)["color"]; has {
		t.Fatalf("a null colour was returned: %v", teams)
	}
	parent := objectAt(t, out, "parent_access")
	wantUser := `{"access":"user","color":5,"id":8,"name":"Bob","right":"manage","status":"accepted"}`
	wantTeam := `{"access":"team","color":1,"id":4,"name":"Team B","right":"read","status":"accepted"}`
	gotUser, _ := json.Marshal(parent["users"].([]any)[0])
	gotTeam, _ := json.Marshal(parent["teams"].([]any)[0])
	if parent["mismatch"] != true || parent["name"] != "Common" || string(gotUser) != wantUser || string(gotTeam) != wantTeam {
		t.Fatalf("parent_access = %v", parent)
	}
}

func TestAccessGetParentAccessIsBoundedAndWithoutWithAbsent(t *testing.T) {
	var many []string
	for i := 1; i <= maxParentAccessEntries+5; i++ {
		many = append(many, fmt.Sprintf(`{"id":%d,"access":"user","name":"n%d","right":"read","color":1,"status":"accepted"}`, i, i))
	}
	body := `{"users":[],"teams":[],"invitations":[],"parent_access":{"mismatch":false,"name":"P","users":[` +
		strings.Join(many, ",") + `],"teams":[]}}`
	var calls []call
	env := newEnvironment(t, &calls, sharingAnswer(body), nil)
	result, err := env.invoke(accessGet.ID, "access", accessArgs(`,"with":["parent_access"]`))
	if err != nil {
		t.Fatal(err)
	}
	parent := objectAt(t, decodeObject(t, result), "parent_access")
	if len(parent["users"].([]any)) != maxParentAccessEntries || parent["users_truncated"] != true {
		t.Fatalf("parent_access = %.300v", parent)
	}
	result, err = env.invoke(accessGet.ID, "access", accessArgs(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, has := decodeObject(t, result)["parent_access"]; has {
		t.Fatalf("parent_access without with: %s", result)
	}
}

func TestSharingReadsRefuseInvalidWithBeforeAnySecret(t *testing.T) {
	cases := []struct{ name, tool, connection, args string }{
		{"links unknown", linksGet.ID, "links", linkArgs(`,"with":["stats"]`)},
		{"links twice", linksGet.ID, "links", linkArgs(`,"with":["views","views"]`)},
		{"links list value", linksGet.ID, "links", linkArgs(`,"with":["sharelink"]`)},
		{"links empty", linksGet.ID, "links", linkArgs(`,"with":[]`)},
		{"dropbox unknown", dropboxGet.ID, "dropbox", dropboxArgs(`,"with":["security"]`)},
		{"dropbox user.security", dropboxGet.ID, "dropbox", dropboxArgs(`,"with":["user.security"]`)},
		{"dropbox twice", dropboxGet.ID, "dropbox", dropboxArgs(`,"with":["directory","directory"]`)},
		{"dropbox capabilities", dropboxGet.ID, "dropbox", dropboxArgs(`,"with":["capabilities"]`)},
		{"dropbox directory.users", dropboxGet.ID, "dropbox", dropboxArgs(`,"with":["directory.users"]`)},
		{"access user", accessGet.ID, "access", accessArgs(`,"with":["user"]`)},
		{"access invitation_drive", accessGet.ID, "access", accessArgs(`,"with":["invitation_drive"]`)},
		{"access twice", accessGet.ID, "access", accessArgs(`,"with":["parent_access","parent_access"]`)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			if _, err := env.invoke(tt.tool, tt.connection, tt.args); err == nil {
				t.Fatal("an invalid with was accepted")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

// sharingSubtree answers the drive tree of subtreeFake and, besides, the given sharing reads by path.
func sharingSubtree(t *testing.T, answers map[string]string) func(*http.Request) (*http.Response, error) {
	base := subtreeFake{}.handler(t)
	return func(r *http.Request) (*http.Response, error) {
		if body, ok := answers[r.URL.Path]; ok {
			return jsonResponse(200, envelopeSuccess(body)), nil
		}
		return base(r)
	}
}

func subtreeParentAccessBody() string {
	return `{"users":[],"teams":[],"invitations":[],"parent_access":{"mismatch":true,"name":"` + subName + `",` +
		`"users":[{"id":8,"access":"user","name":"Bob","right":"manage","color":5,"status":"accepted"}],` +
		`"teams":[{"id":4,"access":"team","name":"Team B","right":"read","color":1,"status":"accepted"}]}}`
}

func TestAccessGetParentAccessFollowsTheTargets(t *testing.T) {
	path := func(id int64) string { return fmt.Sprintf("/2/drive/%d/files/%d/access", ownDrive, id) }
	args := func(id int64) string {
		return fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"with":["parent_access"]}`, ownDrive, id)
	}
	answers := map[string]string{path(subTarget): subtreeParentAccessBody(), path(subInside): subtreeParentAccessBody()}

	t.Run("target folder as object", func(t *testing.T) {
		var calls []call
		env := subtreeEnvironment(t, t.TempDir(), &calls, sharingSubtree(t, answers), subTargets(subTarget)...)
		result, err := env.invoke(accessGet.ID, "sub", args(subTarget))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result, subName) || len(calls) != 3 || calls[2].path != path(subTarget) {
			t.Fatalf("result = %s, calls = %+v", result, calls)
		}
		parent := objectAt(t, decodeObject(t, result), "parent_access")
		if _, has := parent["name"]; has || parent["mismatch"] != true {
			t.Fatalf("parent_access = %v", parent)
		}
		for _, list := range []string{"users", "teams"} {
			entries := parent[list].([]any)
			if len(entries) != 1 || len(entries[0].(map[string]any)) != 1 || entries[0].(map[string]any)["id"] == nil {
				t.Fatalf("%s = %v, want identifiers only", list, entries)
			}
		}
	})
	t.Run("object below the target", func(t *testing.T) {
		var calls []call
		env := subtreeEnvironment(t, t.TempDir(), &calls, sharingSubtree(t, answers), subTargets(subTarget)...)
		result, err := env.invoke(accessGet.ID, "sub", args(subInside))
		if err != nil {
			t.Fatal(err)
		}
		parent := objectAt(t, decodeObject(t, result), "parent_access")
		user := parent["users"].([]any)[0].(map[string]any)
		if parent["name"] != subName || user["name"] != "Bob" || user["color"] != float64(5) || user["right"] != "manage" ||
			parent["teams"].([]any)[0].(map[string]any)["name"] != "Team B" || len(calls) != 3 {
			t.Fatalf("parent_access = %v, calls = %d", parent, len(calls))
		}
	})
	t.Run("whole drive", func(t *testing.T) {
		var calls []call
		env := subtreeEnvironment(t, t.TempDir(), &calls, sharingSubtree(t, answers),
			append(subTargets(), fmt.Sprintf("drive/%d", ownDrive))...)
		result, err := env.invoke(accessGet.ID, "sub", args(subTarget))
		if err != nil {
			t.Fatal(err)
		}
		parent := objectAt(t, decodeObject(t, result), "parent_access")
		if parent["name"] != subName || parent["users"].([]any)[0].(map[string]any)["name"] != "Bob" || len(calls) != 2 {
			t.Fatalf("parent_access = %v, calls = %d", parent, len(calls))
		}
	})
}

func TestDropboxGetDirectoryFollowsTheTargets(t *testing.T) {
	path := func(id int64) string { return fmt.Sprintf("/2/drive/%d/files/%d/dropbox", ownDrive, id) }
	args := func(id int64) string {
		return fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"with":%s}`, ownDrive, id, dropboxAllWith)
	}
	root := `{"id":1,"name":"outside-root","type":"dir","parent_id":0}`
	target := fmt.Sprintf(`{"id":%d,"name":"target","type":"dir","parent_id":1}`, subTarget)
	answers := map[string]string{
		path(subTarget): dropboxFullBody(subTarget, `[`+root+`]`),
		path(subInside): dropboxFullBody(subInside, `[`+root+`,`+target+`]`),
	}
	t.Run("target folder as object", func(t *testing.T) {
		var calls []call
		env := subtreeEnvironment(t, t.TempDir(), &calls, sharingSubtree(t, answers), subTargets(subTarget)...)
		result, err := env.invoke(dropboxGet.ID, "sub", args(subTarget))
		if err != nil {
			t.Fatal(err)
		}
		directory := objectAt(t, decodeObject(t, result), "dropbox", "directory")
		if _, has := directory["path"]; has || directory["parents"] != nil || strings.Contains(result, "outside-root") ||
			strings.Contains(result, "/Common") || len(calls) != 3 {
			t.Fatalf("directory = %v, calls = %d", directory, len(calls))
		}
		if directory["capabilities"] == nil || directory["id"] != float64(subTarget) {
			t.Fatalf("directory = %v", directory)
		}
	})
	t.Run("object below the target", func(t *testing.T) {
		var calls []call
		env := subtreeEnvironment(t, t.TempDir(), &calls, sharingSubtree(t, answers), subTargets(subTarget)...)
		result, err := env.invoke(dropboxGet.ID, "sub", args(subInside))
		if err != nil {
			t.Fatal(err)
		}
		directory := objectAt(t, decodeObject(t, result), "dropbox", "directory")
		parents, _ := directory["parents"].([]any)
		if _, has := directory["path"]; has || len(parents) != 1 || parents[0].(map[string]any)["id"] != float64(subTarget) ||
			strings.Contains(result, "outside-root") || len(calls) != 3 {
			t.Fatalf("directory = %v, calls = %d", directory, len(calls))
		}
	})
	t.Run("whole drive", func(t *testing.T) {
		var calls []call
		env := subtreeEnvironment(t, t.TempDir(), &calls, sharingSubtree(t, answers),
			append(subTargets(), fmt.Sprintf("drive/%d", ownDrive))...)
		result, err := env.invoke(dropboxGet.ID, "sub", args(subTarget))
		if err != nil {
			t.Fatal(err)
		}
		directory := objectAt(t, decodeObject(t, result), "dropbox", "directory")
		if directory["path"] != "/Common/Invoices" || len(directory["parents"].([]any)) != 1 || len(calls) != 2 {
			t.Fatalf("directory = %v, calls = %d", directory, len(calls))
		}
	})
}

// schemaCheck reports every member of a value that the schema does not declare, and, to the depth given and
// outside the nested objects described by their parent, every member that Fields does not name.
func schemaCheck(t *testing.T, id string, schema map[string]any, value any, fields map[string]bool, depth int, path string) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for key, item := range v {
			sub, ok := props[key].(map[string]any)
			if !ok {
				t.Errorf("%s: %s.%s is not in the output schema", id, path, key)
				continue
			}
			if depth <= 2 && !fields[key] {
				t.Errorf("%s: %s.%s is not in Fields", id, path, key)
			}
			next := depth + 1
			if key == "directory" || key == "user" || key == "parent_access" || key == "users" || key == "teams" ||
				key == "invitations" {
				next = 3
			}
			schemaCheck(t, id, sub, item, fields, next, path+"."+key)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, item := range v {
			schemaCheck(t, id, items, item, fields, depth, path)
		}
	}
}

func TestSharingOutputMembersAreInSchemaAndFields(t *testing.T) {
	linkBody := strings.Replace(linkJSONOf(childFileID, "inherit", linkURL), `"access_blocked":false}`,
		`"access_blocked":false,"views":42}`, 1)
	cases := []struct {
		d          capability.Descriptor
		connection string
		args       string
		body       string
	}{
		{linksGet, "links", linkArgs(`,"with":["views"]`), linkBody},
		{dropboxGet, "dropbox", dropboxArgs(`,"with":` + dropboxAllWith),
			dropboxFullBody(childFileID, `[{"id":42,"name":"Common","type":"dir","parent_id":1,"sorted_name":"c"}]`)},
		{accessGet, "access", accessArgs(`,"with":["parent_access"]`), accessFullBody()},
	}
	for _, tc := range cases {
		t.Run(tc.d.ID, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, sharingAnswer(tc.body), nil)
			result, err := env.invoke(tc.d.ID, tc.connection, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			var schema map[string]any
			if err := json.Unmarshal(tc.d.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			fields := map[string]bool{}
			for _, f := range tc.d.Fields {
				fields[f.Name] = true
			}
			schemaCheck(t, tc.d.ID, schema, decodeObject(t, result), fields, 1, "")
		})
	}

	t.Run(linksList.ID, func(t *testing.T) {
		body := `[{"id":43,"name":"a","type":"file","parent_id":42,"status":"ok","last_modified_at":1735776000,` +
			`"sharelink":` + linkBody + `}]`
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"result":"success","data":`+body+`,"cursor":"","has_more":false}`), nil
		}), nil)
		result, err := env.invoke(linksList.ID, "links", fmt.Sprintf(`{"drive_id":%d,"with":["sharelink.views"]}`, ownDrive))
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(linksList.OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		link := objectAt(t, decodeObject(t, result)["entries"].([]any)[0].(map[string]any), "link")
		if link["views"] != float64(42) || link["created_by"] != float64(7) {
			t.Fatalf("link = %v", link)
		}
		schemaCheck(t, linksList.ID, schema, decodeObject(t, result), map[string]bool{}, 3, "")
	})
}
