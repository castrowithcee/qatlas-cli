package infomaniakdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// Values that grant access without an account; none of them may leave a file tool, except the share link URL
// in link of links.list.
const (
	lockTokenCanary   = "onlyoffice:lock-token-canary"
	shareURLCanary    = "https://kdrive.infomaniak.com/app/share/example-share-canary"
	dropboxURLCanary  = "https://kdrive.infomaniak.com/app/collaborate/example-dropbox-url-canary"
	dropboxUUIDCanary = "example-dropbox-uuid-canary"
)

var allFileWith = func() string { quoted, _ := json.Marshal(fileWithValues); return string(quoted) }()

func shareLinkOf(id int64) string {
	return fmt.Sprintf(`{"url":%q,"file_id":%d,"right":"inherit","valid_until":null,"created_by":2001,`+
		`"created_at":1735689600,"updated_at":1735776000,"capabilities":{"can_edit":false,"can_see_stats":true,`+
		`"can_see_info":true,"can_download":true,"can_comment":false,"can_request_access":true},`+
		`"access_blocked":false,"views":4}`, shareURLCanary, id)
}

func wantShareLinkOf(id int64) string {
	return fmt.Sprintf(`{"file_id":%d,"right":"inherit","created_by":2001,"created_at":"2025-01-01T00:00:00Z",`+
		`"updated_at":"2025-01-02T00:00:00Z","capabilities":{"can_edit":false,"can_see_stats":true,"can_see_info":true,`+
		`"can_download":true,"can_comment":false,"can_request_access":true},"access_blocked":false,"views":4}`, id)
}

// The folders above both entries, with what the parents.* values load.
const (
	parentsWith = `[{"id":1,"name":"Root","type":"dir","parent_id":0,"status":"ok","path":"/","sorted_name":"root",` +
		`"capabilities":{"can_read":true,"can_upload":true},"users":[2001],"teams":[10]},` +
		`{"id":4,"name":"Projects","type":"dir","parent_id":1,"status":"ok","path":"/Projects","sorted_name":"projects",` +
		`"capabilities":{"can_read":true,"can_move_into":false},"users":[2002],"teams":[11]}]`
	wantParents = `[{"id":1,"name":"Root","type":"folder","parent_id":0,"status":"ok","path":"/","sorted_name":"root",` +
		`"capabilities":{"can_read":true,"can_upload":true},"users":[2001],"teams":[10]},` +
		`{"id":4,"name":"Projects","type":"folder","parent_id":1,"status":"ok","path":"/Projects","sorted_name":"projects",` +
		`"capabilities":{"can_read":true,"can_move_into":false},"users":[2002],"teams":[11]}]`

	commonWith = `"is_favorite":true,"activity":{"last_accessed_at":1735689600},` +
		`"lock":{"locked_at":1735689600,"unlocked_at":null,"description":"365","token":"` + lockTokenCanary + `"},` +
		`"categories":[{"category_id":1,"added_at":1735689600,"user_validation":"TO_VALIDATE","is_generated_by_ai":false,` +
		`"user_id":2001,"category":{"id":1,"name":"Invoices","color":"#FF1493","is_predefined":true,"created_by":2001,` +
		`"created_at":1735689600,"user_uses":5}}],"users":[2001,2002],"teams":[10],"parents":` + parentsWith
	wantCommonWith = `"is_favorite":true,"activity":{"last_accessed_at":"2025-01-01T00:00:00Z"},` +
		`"lock":{"locked_at":"2025-01-01T00:00:00Z","description":"365"},` +
		`"categories":[{"category_id":1,"added_at":"2025-01-01T00:00:00Z","user_validation":"TO_VALIDATE",` +
		`"is_generated_by_ai":false,"user_id":2001,"category":{"id":1,"name":"Invoices","color":"#FF1493",` +
		`"is_predefined":true,"created_by":2001,"created_at":"2025-01-01T00:00:00Z"}}],"users":[2001,2002],"teams":[10],` +
		`"parents":` + wantParents

	fileWithMembers = `"capabilities":{"can_use_favorite":true,"can_become_sharelink":true,"can_use_team":false,` +
		`"can_show":true,"can_read":true,"can_write":true,"can_share":true,"can_leave":false,"can_delete":true,` +
		`"can_rename":true,"can_move":true},"version":{"is_multiple":true,"number":3,"total_size":1700,"keep_forever":false},` +
		`"conversion_capabilities":{"when_downloading":true,"download_extensions":["pdf","odt"],` +
		`"when_onlyoffice_opening":false,"onlyoffice_extension":"docx"},"hash":"xxh3:00ec7bec63e548df",` +
		`"path":"/Projects/a.pdf","sorted_name":"a.pdf","etag":"example-etag","supported_by":["365","onlyoffice"],` +
		commonWith
	wantFileWithMembers = `"capabilities":{"can_use_favorite":true,"can_become_sharelink":true,"can_use_team":false,` +
		`"can_show":true,"can_read":true,"can_write":true,"can_share":true,"can_leave":false,"can_delete":true,` +
		`"can_rename":true,"can_move":true},"version":{"is_multiple":true,"number":3,"total_size":1700,"keep_forever":false},` +
		`"conversion_capabilities":{"when_downloading":true,"download_extensions":["pdf","odt"],` +
		`"when_onlyoffice_opening":false,"onlyoffice_extension":"docx"},"hash":"xxh3:00ec7bec63e548df",` +
		`"path":"/Projects/a.pdf","sorted_name":"a.pdf","etag":"example-etag","supported_by":["365","onlyoffice"],` +
		wantCommonWith

	folderWithMembers = `"capabilities":{"can_use_favorite":true,"can_become_sharelink":true,"can_use_team":true,` +
		`"can_show":true,"can_read":true,"can_write":true,"can_share":true,"can_leave":false,"can_delete":true,` +
		`"can_rename":true,"can_move":true,"can_create_directory":true,"can_create_file":true,"can_upload":true,` +
		`"can_move_into":true,"can_become_dropbox":false,"colorable":true},"path":"/Projects/dir","sorted_name":"dir",` +
		`"external_import":{"id":3,"application":"dropbox","account_name":"example-account","status":"done",` +
		`"error_code":"example-error","path":"/Imports","directory_id":8,"has_shared_files":"false",` +
		`"created_at":1735689600,"updated_at":1735776000,"count_success_files":10,"count_failed_files":0},` +
		`"rewind":{"id":55,"status":"done","mode":"directory","archiving":"all","directory_id":8,` +
		`"directory_name":"Archive","user_id":null,"rewind_at":1735689600,"created_at":1735776000,` +
		`"expires_at":1767225600,"created_by":2001,"is_approved":true,"approval_at":1735948800,"rewind_name":"example-rewind"},` +
		`"dropbox":{"id":12,"uuid":"` + dropboxUUIDCanary + `","name":"Uploads","url":"` + dropboxURLCanary + `",` +
		`"users_count":2,"created_by":2001,"created_at":1735689600,"updated_at":1735776000,"last_uploaded_at":1735862400,` +
		`"capabilities":{"has_password":true,"has_notification":false,"has_validity":true,"has_size_limit":false,` +
		`"validity":"example-validity","size":"example-size"}},` + commonWith
	wantFolderWithMembers = `"capabilities":{"can_use_favorite":true,"can_become_sharelink":true,"can_use_team":true,` +
		`"can_show":true,"can_read":true,"can_write":true,"can_share":true,"can_leave":false,"can_delete":true,` +
		`"can_rename":true,"can_move":true,"can_create_directory":true,"can_create_file":true,"can_upload":true,` +
		`"can_move_into":true,"can_become_dropbox":false,"colorable":true},"path":"/Projects/dir","sorted_name":"dir",` +
		`"external_import":{"id":3,"application":"dropbox","account_name":"example-account","status":"done",` +
		`"error_code":"example-error","path":"/Imports","directory_id":8,"has_shared_files":"false",` +
		`"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-02T00:00:00Z","count_success_files":10,"count_failed_files":0},` +
		`"rewind":{"id":55,"status":"done","mode":"directory","archiving":"all","directory_id":8,` +
		`"directory_name":"Archive","rewind_at":"2025-01-01T00:00:00Z","created_at":"2025-01-02T00:00:00Z",` +
		`"expires_at":"2026-01-01T00:00:00Z","created_by":2001,"is_approved":true,"approval_at":"2025-01-04T00:00:00Z",` +
		`"rewind_name":"example-rewind"},` +
		`"dropbox":{"id":12,"name":"Uploads","users_count":2,"created_by":2001,"created_at":"2025-01-01T00:00:00Z",` +
		`"updated_at":"2025-01-02T00:00:00Z","last_uploaded_at":"2025-01-03T00:00:00Z",` +
		`"capabilities":{"has_password":true,"has_notification":false,"has_validity":true,"has_size_limit":false,` +
		`"validity":"example-validity","size":"example-size"}},` + wantFolderCommon
	wantFolderCommon = wantCommonWith
)

// withEntry adds members to an entry resource.
func withEntry(entry, members string) string {
	return strings.TrimSuffix(entry, "}") + "," + members + "}"
}

var (
	fileWithFull   = withEntry(entryFileFull, fileWithMembers+`,"sharelink":`+shareLinkOf(7))
	folderWithFull = withEntry(entryFolderFull, folderWithMembers+`,"sharelink":`+shareLinkOf(8))
)

// fileWithArguments adds a with list to a case's arguments.
func fileWithArguments(arguments, with string) string {
	return strings.TrimSuffix(arguments, "}") + `,"with":` + with + `}`
}

func entriesOf(t *testing.T, result string) []map[string]any {
	t.Helper()
	var page struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("result = %s: %v", result, err)
	}
	return page.Entries
}

func checkMembers(t *testing.T, label string, got map[string]any, want string) {
	t.Helper()
	for key, value := range mustMap(t, want) {
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s: %s = %v\nwant %v", label, key, got[key], value)
		}
	}
}

func noAccessValues(t *testing.T, label, result string, allowed ...string) {
	t.Helper()
	for _, canary := range []string{lockTokenCanary, shareURLCanary, dropboxURLCanary, dropboxUUIDCanary} {
		if strings.Contains(result, canary) && !slices.Contains(allowed, canary) {
			t.Errorf("%s: access value %s in the output", label, canary)
		}
	}
}

func fileRead(body string) func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(body)), nil
	})
}

func TestFileWithToolsReturnEveryWithMemberExactly(t *testing.T) {
	wantFile := `{` + strings.TrimPrefix(strings.TrimSuffix(wantFileFull, "}"), "{") + "," + wantFileWithMembers
	wantFolder := `{` + strings.TrimPrefix(strings.TrimSuffix(wantFolderFull, "}"), "{") + "," + wantFolderWithMembers
	for _, c := range entryFieldCases() {
		t.Run(c.descriptor.ID, func(t *testing.T) {
			c.arguments = fileWithArguments(c.arguments, allFileWith)
			result, calls := runEntryField(t, c, c.wrap(fileWithFull+","+folderWithFull))
			entries := entriesOf(t, result)
			if len(entries) != 2 || len(calls) != 2 {
				t.Fatalf("entries = %d, calls = %d", len(entries), len(calls))
			}
			wantQuery := strings.Join(fileWithValues, ",")
			allowed := []string(nil)
			wantFileEntry, wantFolderEntry := wantFile+`,"sharelink":`+wantShareLinkOf(7)+`}`,
				wantFolder+`,"sharelink":`+wantShareLinkOf(8)+`}`
			if c.descriptor.ID == linksList.ID {
				wantQuery = "sharelink," + strings.Join(slices.DeleteFunc(slices.Clone(fileWithValues),
					func(v string) bool { return v == "sharelink" }), ",")
				allowed = []string{shareURLCanary}
				wantFileEntry, wantFolderEntry = wantFile+"}", wantFolder+"}"
				for i, entry := range entries {
					if _, ok := entry["sharelink"]; ok {
						t.Errorf("links.list reports sharelink besides link")
					}
					link := entry["link"].(map[string]any)
					if link["url"] != shareURLCanary || link["created_by"] != float64(2001) || link["views"] != float64(4) ||
						link["file_id"] != float64(7+i) {
						t.Errorf("link = %v", link)
					}
				}
			}
			if got := calls[1].query.Get("with"); got != wantQuery {
				t.Errorf("with = %q, want %q", got, wantQuery)
			}
			checkMembers(t, "file", entries[0], wantFileEntry)
			checkMembers(t, "folder", entries[1], wantFolderEntry)
			noAccessValues(t, c.descriptor.ID, result, allowed...)
		})
	}

	for name, tc := range map[string]struct{ body, want string }{
		"file":   {fileWithFull, wantFile + `,"sharelink":` + wantShareLinkOf(7) + `}`},
		"folder": {folderWithFull, wantFolder + `,"sharelink":` + wantShareLinkOf(8) + `}`},
	} {
		t.Run("files.stat "+name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, fileRead(tc.body), nil)
			result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":7,"with":%s}`, ownDrive, allFileWith))
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || calls[1].query.Get("with") != strings.Join(fileWithValues, ",") {
				t.Fatalf("calls = %+v", calls)
			}
			checkMembers(t, name, mustMap(t, result), tc.want)
			noAccessValues(t, "files.stat", result)
		})
	}
}

// Without with, an answer full of with members gives exactly the output without them, and nothing extra is
// asked for.
func TestFileWithToolsWithoutWithKeepTheirOutput(t *testing.T) {
	for _, c := range entryFieldCases() {
		t.Run(c.descriptor.ID, func(t *testing.T) {
			result, calls := runEntryField(t, c, c.wrap(fileWithFull+","+folderWithFull))
			want := c.wantWrap(wantFileFull + "," + wantFolderFull)
			if c.descriptor.ID == linksList.ID {
				var page map[string]any
				_ = json.Unmarshal([]byte(result), &page)
				for _, entry := range page["entries"].([]any) {
					delete(entry.(map[string]any), "link")
				}
				encoded, _ := json.Marshal(page)
				result = string(encoded)
			}
			if !jsonEqual(result, want) {
				t.Fatalf("result = %s\nwant   = %s", result, want)
			}
			wantWith := ""
			if c.descriptor.ID == linksList.ID {
				wantWith = "sharelink"
			}
			if got := calls[1].query.Get("with"); got != wantWith {
				t.Fatalf("with = %q, want %q", got, wantWith)
			}
		})
	}
	var calls []call
	env := newEnvironment(t, &calls, fileRead(folderWithFull), nil)
	result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":8}`, ownDrive))
	if err != nil || !jsonEqual(result, wantFolderFull) || calls[1].query.Has("with") {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
}

// Members whose with value was not asked for never appear, even when Infomaniak reports them.
func TestFileWithToolsDropMembersThatWereNotAskedFor(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fileRead(folderWithFull), nil)
	result, err := env.invoke(filesStat.ID, "drive",
		fmt.Sprintf(`{"drive_id":%d,"file_id":8,"with":["categories","dropbox","sharelink","parents"]}`, ownDrive))
	if err != nil {
		t.Fatal(err)
	}
	entry := mustMap(t, result)
	for _, key := range []string{"capabilities", "path", "sorted_name", "external_import", "rewind", "users", "teams",
		"is_favorite", "activity", "lock"} {
		if _, ok := entry[key]; ok {
			t.Errorf("%s present without its with value", key)
		}
	}
	if _, ok := entry["categories"].([]any)[0].(map[string]any)["category"]; ok {
		t.Error("categories.category present without its with value")
	}
	if _, ok := entry["dropbox"].(map[string]any)["capabilities"]; ok {
		t.Error("dropbox.capabilities present without its with value")
	}
	if _, ok := entry["sharelink"].(map[string]any)["views"]; ok {
		t.Error("sharelink.views present without its with value")
	}
	for _, parent := range entry["parents"].([]any) {
		for _, key := range []string{"path", "sorted_name", "capabilities", "users", "teams"} {
			if _, ok := parent.(map[string]any)[key]; ok {
				t.Errorf("parents.%s present without its with value", key)
			}
		}
	}

	// parents.* alone brings the parents with just that member.
	calls = nil
	env = newEnvironment(t, &calls, fileRead(folderWithFull), nil)
	result, err = env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":8,"with":["parents.teams"]}`, ownDrive))
	if err != nil {
		t.Fatal(err)
	}
	parents := mustMap(t, result)["parents"].([]any)
	if len(parents) != 2 || parents[0].(map[string]any)["teams"] == nil || parents[0].(map[string]any)["path"] != nil {
		t.Fatalf("parents = %v", parents)
	}
}

func TestFileWithToolsRefuseInvalidWithBeforeAnySecretRead(t *testing.T) {
	tools := map[string]string{filesList.ID: "drive", filesStat.ID: "drive", trashListDescriptor.ID: "drive",
		linksList.ID: "links"}
	for tool, connection := range tools {
		for name, with := range map[string]string{
			"unknown": `["highlights"]`, "drive value": `["account"]`, "repeated": `["path","path"]`, "empty": `[]`,
			"not a list": `"path"`,
		} {
			t.Run(tool+" "+name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, fileRead(`{}`), nil)
				if _, err := env.invoke(tool, connection, fmt.Sprintf(`{"drive_id":%d,"with":%s}`, ownDrive, with)); err == nil {
					t.Fatal("invoke() succeeded")
				}
				if len(calls) != 0 || *env.reads != 0 {
					t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
				}
			})
		}
	}
}

// The client methods refuse what the input schema would, before any request.
func TestFileWithClientMethodsRefuseInvalidWith(t *testing.T) {
	c, ctx := &Client{}, context.Background()
	for _, with := range [][]string{{"highlights"}, {"path", "path"}} {
		_, errs := c.ListFolder(ctx, 1, 1, "", 10, with)
		_, errStat := c.StatFile(ctx, 1, 1, with)
		_, errProven := c.statProven(ctx, 1, 1, with)
		_, errTrash := c.ListTrash(ctx, 1, "", 10, with)
		_, errLinks := c.ListLinks(ctx, 1, "", 10, with)
		for _, err := range []error{errs, errStat, errProven, errTrash, errLinks} {
			if !isInvalidRequest(err) {
				t.Fatalf("with %v: err = %v, want an invalid request", with, err)
			}
		}
	}
}

// subtreeFileFake answers the drive tree of scope_test.go with the with members of a folder: path, parents
// with paths, an import path and the rewind folder names. A list answers one child of the listed folder.
func subtreeFileFake(t *testing.T) func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		object := func(id int64, chain []int64) string {
			var parents []string
			for i, p := range chain {
				parent := int64(0)
				if i > 0 {
					parent = chain[i-1]
				}
				parents = append(parents, fmt.Sprintf(`{"id":%d,"name":"folder-%d","type":"dir","parent_id":%d,`+
					`"path":"/path-%d","sorted_name":"s-%d","users":[2001]}`, p, p, parent, p, p))
			}
			return withEntry(fmt.Sprintf(`{"id":%d,"name":"entry-%d","type":"dir","parent_id":%d,"status":"ok"}`,
				id, id, chain[len(chain)-1]),
				`"path":"/outside/path","external_import":{"id":3,"path":"/outside/import","directory_id":8},`+
					`"rewind":{"id":55,"directory_id":8,"directory_name":"outside-name","rewind_name":"outside-rewind"},`+
					`"parents":[`+strings.Join(parents, ",")+`]`)
		}
		if match := fileReadPath.FindStringSubmatch(r.URL.Path); match != nil {
			var id int64
			_, _ = fmt.Sscan(match[1], &id)
			chain, ok := subParents[id]
			if !ok {
				return jsonResponse(404, `{"result":"error"}`), nil
			}
			return jsonResponse(200, envelopeSuccess(object(id, chain))), nil
		}
		if strings.HasSuffix(r.URL.Path, "/files") {
			var folder int64
			_, _ = fmt.Sscanf(r.URL.Path, "/3/drive/5001/files/%d/files", &folder)
			child := object(9001, append(slices.Clone(subParents[folder]), folder))
			return jsonResponse(200, `{"result":"success","data":[`+child+`],"cursor":"","has_more":false}`), nil
		}
		return jsonResponse(404, `{"result":"error"}`), nil
	})
}

func subtreeHidden(t *testing.T, label string, entry map[string]any, wantParents []float64) {
	t.Helper()
	if _, ok := entry["path"]; ok {
		t.Errorf("%s: path present", label)
	}
	if _, ok := entry["external_import"].(map[string]any)["path"]; ok {
		t.Errorf("%s: external_import.path present", label)
	}
	rewind := entry["rewind"].(map[string]any)
	for _, key := range []string{"directory", "directory_name", "rewind_name"} {
		if _, ok := rewind[key]; ok {
			t.Errorf("%s: rewind.%s present", label, key)
		}
	}
	var ids []float64
	for _, p := range entry["parents"].([]any) {
		parent := p.(map[string]any)
		ids = append(ids, parent["id"].(float64))
		if _, ok := parent["path"]; ok {
			t.Errorf("%s: parents[].path present", label)
		}
		if parent["sorted_name"] == nil || parent["users"] == nil {
			t.Errorf("%s: parent = %v, want sorted_name and users kept", label, parent)
		}
	}
	if !reflect.DeepEqual(ids, wantParents) {
		t.Errorf("%s: parents = %v, want %v", label, ids, wantParents)
	}
}

// A drive bound only by files targets never shows a path or a folder outside them; the requests stay the same.
func TestFileWithSubtreeHidesWhatLiesOutsideTheTargets(t *testing.T) {
	with := `["path","external_import","rewind","parents","parents.path","parents.sorted_name","parents.users"]`
	run := func(tool, arguments string) (map[string]any, []call) {
		t.Helper()
		var calls []call
		env := subtreeEnvironment(t, t.TempDir(), &calls, subtreeFileFake(t), subTargets(subTarget)...)
		result, err := env.invoke(tool, "sub", arguments)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return mustMap(t, result), calls
	}

	entry, calls := run(filesStat.ID, driveArgs(fmt.Sprintf(`,"file_id":%d,"with":%s`, subInside, with)))
	subtreeHidden(t, "files.stat", entry, []float64{float64(subTarget)})
	_, plain := run(filesStat.ID, driveArgs(fmt.Sprintf(`,"file_id":%d`, subInside)))
	objectReads := 0
	for _, c := range calls {
		if fileReadPath.MatchString(c.path) {
			objectReads++
		}
	}
	if objectReads != 1 || len(calls) != len(plain) {
		t.Errorf("files.stat calls = %d (object reads %d), want %d with one object read", len(calls), objectReads, len(plain))
	}
	wantQuery := "parents,path,external_import,rewind,parents.path,parents.sorted_name,parents.users"
	if got := calls[len(calls)-1].query.Get("with"); got != wantQuery {
		t.Errorf("files.stat with = %q, want %q", got, wantQuery)
	}

	// The target itself has only folders outside above it.
	entry, _ = run(filesStat.ID, driveArgs(fmt.Sprintf(`,"file_id":%d,"with":["parents"]`, subTarget)))
	if _, ok := entry["parents"]; ok {
		t.Errorf("files.stat of the target: parents = %v, want none", entry["parents"])
	}

	page, calls := run(filesList.ID, driveArgs(fmt.Sprintf(`,"folder_id":%d,"with":%s`, subInside, with)))
	subtreeHidden(t, "files.list", page["entries"].([]any)[0].(map[string]any), []float64{float64(subTarget), float64(subInside)})
	_, plain = run(filesList.ID, driveArgs(fmt.Sprintf(`,"folder_id":%d`, subInside)))
	if len(calls) != len(plain) {
		t.Errorf("files.list calls = %d, want %d", len(calls), len(plain))
	}

	// The same answers for a drive bound as a whole keep every member and every parent.
	var wholeCalls []call
	env := newEnvironment(t, &wholeCalls, subtreeFileFake(t), nil)
	result, err := env.invoke(filesStat.ID, "drive", driveArgs(fmt.Sprintf(`,"file_id":%d,"with":%s`, subInside, with)))
	if err != nil {
		t.Fatal(err)
	}
	whole := mustMap(t, result)
	if whole["path"] != "/outside/path" || whole["external_import"].(map[string]any)["path"] != "/outside/import" ||
		whole["rewind"].(map[string]any)["directory_name"] != "outside-name" ||
		whole["rewind"].(map[string]any)["rewind_name"] != "outside-rewind" || len(whole["parents"].([]any)) != 2 ||
		whole["parents"].([]any)[0].(map[string]any)["path"] != fmt.Sprintf("/path-%d", rootID) {
		t.Errorf("whole drive = %v", whole)
	}
	if wholeCalls[len(wholeCalls)-1].query.Get("with") != strings.Join(mustStrings(t, with), ",") {
		t.Errorf("whole drive with = %q", wholeCalls[len(wholeCalls)-1].query.Get("with"))
	}
}

func mustStrings(t *testing.T, text string) []string {
	t.Helper()
	var out []string
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A parent chain that never reaches a target, or loops, keeps no parent.
func TestParentsOfKeepsOnlyChainsThatReachATarget(t *testing.T) {
	target := func(id int64) bool { return id == 10 }
	r := reach{isTarget: target}
	parents := []fileJSON{
		{ID: 12, ParentID: 11}, {ID: 1, ParentID: 0}, {ID: 10, ParentID: 1}, {ID: 11, ParentID: 10},
		{ID: 20, ParentID: 21}, {ID: 21, ParentID: 20},
	}
	got, _ := parentsOf(parents, nil, r)
	var ids []int64
	for _, p := range got {
		ids = append(ids, p.ID)
	}
	if !reflect.DeepEqual(ids, []int64{12, 10, 11}) {
		t.Fatalf("parents = %v, want 12, 10, 11", ids)
	}
}

func TestFileWithCapsListsAndFlagsThem(t *testing.T) {
	var parents []string
	for i := range 65 {
		parents = append(parents, fmt.Sprintf(`{"id":%d,"name":"p","type":"dir","parent_id":%d,"users":[%s]}`, i+1, i, numbers(1001)))
	}
	body := withEntry(entryFileFull, `"categories":[`+repeat(`{"category_id":1}`, 101)+`],`+
		`"supported_by":[`+repeat(`"365"`, 101)+`],`+
		`"conversion_capabilities":{"download_extensions":[`+repeat(`"pdf"`, 101)+`]},`+
		`"users":[`+numbers(1001)+`],"teams":[`+numbers(1000)+`],"parents":[`+strings.Join(parents, ",")+`]`)
	var calls []call
	env := newEnvironment(t, &calls, fileRead(body), nil)
	result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":7,"with":`+
		`["categories","supported_by","conversion_capabilities","users","teams","parents.users"]}`, ownDrive))
	if err != nil {
		t.Fatal(err)
	}
	entry := mustMap(t, result)
	length := func(v any) int { return len(v.([]any)) }
	conversion := entry["conversion_capabilities"].(map[string]any)
	parent := entry["parents"].([]any)[0].(map[string]any)
	checks := []struct {
		name      string
		got, want int
		flag      any
		flagged   bool
	}{
		{"categories", length(entry["categories"]), 100, entry["categories_truncated"], true},
		{"supported_by", length(entry["supported_by"]), 100, entry["supported_by_truncated"], true},
		{"download_extensions", length(conversion["download_extensions"]), 100, conversion["download_extensions_truncated"], true},
		{"users", length(entry["users"]), 1000, entry["users_truncated"], true},
		{"teams", length(entry["teams"]), 1000, entry["teams_truncated"], false},
		{"parents", length(entry["parents"]), 64, entry["parents_truncated"], true},
		{"parents.users", length(parent["users"]), 1000, parent["users_truncated"], true},
	}
	for _, c := range checks {
		if c.got != c.want || (c.flag == true) != c.flagged {
			t.Errorf("%s: len = %d (want %d), flag = %v (want %v)", c.name, c.got, c.want, c.flag, c.flagged)
		}
	}
}

func TestFileWithCleansForeignTexts(t *testing.T) {
	body := withEntry(entryFileFull, `"path":"/a\u0001b","sorted_name":"s\u001bt","supported_by":"x\u0007",`+
		`"lock":{"description":"d\u0000e"},"external_import":{"account_name":"n\u0001m","has_shared_files":true}`)
	var calls []call
	env := newEnvironment(t, &calls, fileRead(body), nil)
	result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":7,"with":`+
		`["path","sorted_name","supported_by","lock","external_import"]}`, ownDrive))
	if err != nil {
		t.Fatal(err)
	}
	entry := mustMap(t, result)
	imported := entry["external_import"].(map[string]any)
	if entry["path"] != "/ab" || entry["sorted_name"] != "st" || !reflect.DeepEqual(entry["supported_by"], []any{"x"}) ||
		entry["lock"].(map[string]any)["description"] != "de" || imported["account_name"] != "nm" ||
		imported["has_shared_files"] != "true" {
		t.Fatalf("entry = %v", entry)
	}
}

// schemaAllows reports every member of value that its schema does not declare, at any depth.
func schemaAllows(t *testing.T, label string, schema map[string]any, value any) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for key, member := range v {
			sub, ok := properties[key].(map[string]any)
			if !ok {
				t.Errorf("%s: member %s missing in the schema", label, key)
				continue
			}
			schemaAllows(t, label+"."+key, sub, member)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, item := range v {
			schemaAllows(t, label+"[]", items, item)
		}
	}
}

func TestEveryWithOutputMemberIsInSchemaAndFields(t *testing.T) {
	check := func(t *testing.T, d capability.Descriptor, result string, entries []any) {
		t.Helper()
		schema := mustMap(t, string(d.OutputSchema))
		schemaAllows(t, d.ID, schema, mustMap(t, result))
		fields := map[string]bool{}
		for _, f := range d.Fields {
			fields[f.Name] = true
		}
		for _, entry := range entries {
			for key := range entry.(map[string]any) {
				if !fields[key] {
					t.Errorf("%s: entry member %s missing in Fields", d.ID, key)
				}
			}
		}
		itemSchema := schema
		if items, ok := schema["properties"].(map[string]any)["entries"]; ok {
			itemSchema = items.(map[string]any)["items"].(map[string]any)
		}
		for key := range itemSchema["properties"].(map[string]any) {
			if !fields[key] {
				t.Errorf("%s: schema member %s missing in Fields", d.ID, key)
			}
		}
	}
	for _, c := range entryFieldCases() {
		c.arguments = fileWithArguments(c.arguments, allFileWith)
		result, _ := runEntryField(t, c, c.wrap(fileWithFull+","+folderWithFull))
		check(t, c.descriptor, result, mustMap(t, result)["entries"].([]any))
	}
	for _, body := range []string{fileWithFull, folderWithFull} {
		var calls []call
		env := newEnvironment(t, &calls, fileRead(body), nil)
		result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":7,"with":%s}`, ownDrive, allFileWith))
		if err != nil {
			t.Fatal(err)
		}
		check(t, filesStat, result, []any{mustMap(t, result)})
	}
}
