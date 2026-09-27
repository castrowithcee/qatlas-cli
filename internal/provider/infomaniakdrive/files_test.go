package infomaniakdrive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// files.list reports an incomplete page transparently: has_more true and a cursor to continue with, never
// silently loading the next page itself.
func TestFilesListReportsAnIncompletePageTransparently(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		wantPath := fmt.Sprintf("/3/drive/%d/files/%d/files", ownDrive, rootID)
		if r.URL.Path != wantPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, wantPath)
		}
		if r.URL.Query().Get("cursor") != "" {
			t.Fatalf("first page carried a cursor: %s", r.URL.Query().Get("cursor"))
		}
		return jsonResponse(200, `{"result":"success","data":[`+
			fileJSONOf(childFolderID, rootID, "Reports", "dir")+`],"cursor":"page2-canary","has_more":true}`), nil
	}), nil)

	result, err := env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page FolderPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !page.HasMore || page.Cursor != "page2-canary" || page.Count != 1 || page.Entries[0].Type != "folder" {
		t.Fatalf("page = %+v, want an incomplete page with its continuation cursor", page)
	}
	// One extra request confirms the drive belongs to the bound account before the folder is listed.
	if len(calls) != 2 || calls[0].path != ownershipPath(ownDrive) || calls[1].query.Get("limit") != strconv.Itoa(defaultListLimit) {
		t.Fatalf("calls = %+v", calls)
	}

	// The reported cursor is passed straight back for the next page, unmodified.
	calls = nil
	env = newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("cursor") != "page2-canary" {
			t.Fatalf("cursor = %q, want it passed through unmodified", r.URL.Query().Get("cursor"))
		}
		return jsonResponse(200, `{"result":"success","data":[`+
			fileJSONOf(childFileID, rootID, "report.pdf", "file")+`],"cursor":"","has_more":false}`), nil
	}), nil)
	result, err = env.invoke(filesList.ID, "drive",
		fmt.Sprintf(`{"drive_id":%d,"cursor":"page2-canary"}`, ownDrive))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	// A fresh target: the previous page's cursor has no "cursor" key to overwrite it with once this page
	// omits an empty one, and reusing the same variable must not let the stale value survive.
	page = FolderPage{}
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if page.HasMore || page.Cursor != "" || page.Count != 1 || page.Entries[0].Type != "file" {
		t.Fatalf("page = %+v, want the final, complete page", page)
	}
}

// files.stat defaults to the drive's root when file_id is omitted, and reports a plain metadata envelope.
func TestFilesStatReadsMetadataAndDefaultsToTheRoot(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		wantPath := fmt.Sprintf("/3/drive/%d/files/%d", ownDrive, rootID)
		if r.URL.Path != wantPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, wantPath)
		}
		return jsonResponse(200, envelopeSuccess(fileJSONOf(rootID, 0, "Drive root", "dir"))), nil
	}), nil)
	result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var entry Entry
	if err := json.Unmarshal([]byte(result), &entry); err != nil || entry.Type != "folder" || entry.ID != rootID {
		t.Fatalf("entry = %+v, %v", entry, err)
	}
}

// files.get refuses content that exceeds the hard byte limit instead of returning a truncated, possibly
// corrupt payload, and reads bounded content otherwise.
func TestFilesGetRefusesContentOverTheSizeLimit(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		wantPath := fmt.Sprintf("/2/drive/%d/files/%d/download", ownDrive, childFileID)
		if r.URL.Path != wantPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, wantPath)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}},
			Body: httpBody(strings.Repeat("x", maxFileBytes+1))}, nil
	}), nil)
	_, err := env.invoke(filesGet.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID))
	if classOf(err) == "" || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("err = %v, want a refusal naming the size limit", err)
	}

	calls = nil
	env = newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}},
			Body: httpBody("small file content")}, nil
	}), nil)
	result, err := env.invoke(filesGet.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var content Content
	if err := json.Unmarshal([]byte(result), &content); err != nil || content.Size != len("small file content") {
		t.Fatalf("content = %+v, %v", content, err)
	}
}

// files.get, like files.list and files.stat, refuses a file_id of a drive outside the connection's
// allow-list before any request is sent.
func TestFilesGetRefusesForeignDrive(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}, nil)
	_, err := env.invoke(filesGet.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, foreignDrive, childFileID))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request for a foreign drive", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none", calls)
	}
}

// Without any drive allow-list, a connection still only reaches drives of the account it is bound to: the
// ownership check itself, not local configuration, is what keeps a drive of another account (that the same
// token can otherwise see) out of reach. This holds for every one of the three file tools.
func TestFilesToolsVerifyDriveOwnershipWithoutAnAllowList(t *testing.T) {
	for _, tool := range []struct {
		id        string
		arguments string
	}{
		{filesList.ID, fmt.Sprintf(`{"drive_id":%d}`, foreignDrive)},
		{filesStat.ID, fmt.Sprintf(`{"drive_id":%d}`, foreignDrive)},
		{filesGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, foreignDrive, childFileID)},
	} {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to the file endpoint: %s", r.URL.Path)
				return nil, nil
			}), nil)
			_, err := env.invoke(tool.id, "account", tool.arguments)
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request for a drive of another account", err)
			}
			if len(calls) != 1 || calls[0].path != ownershipPath(foreignDrive) {
				t.Fatalf("calls = %+v, want exactly the ownership check and no file endpoint", calls)
			}
		})
	}
}

// A drive named in the connection's own drive allow-list is still refused when it turns out, on Infomaniak's
// own answer, to belong to another account: the allow-list is local configuration a person wrote, and it is
// never trusted over what the account actually is.
func TestFilesGetRefusesAnAllowListedDriveThatBelongsToAnotherAccount(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to the file endpoint: %s", r.URL.Path)
		return nil, nil
	}), nil)
	_, err := env.invoke(filesGet.ID, "driveforeign", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, foreignDrive, childFileID))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request even though the allow-list names this drive", err)
	}
	if len(calls) != 1 || calls[0].path != ownershipPath(foreignDrive) {
		t.Fatalf("calls = %+v, want exactly the ownership check and no file endpoint", calls)
	}
}

// A drive that both the allow-list and the live ownership check agree belongs to the bound account is read
// normally: the check is not a blanket refusal, only a confirmation.
func TestFilesStatSucceedsOnceOwnershipIsConfirmed(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		wantPath := fmt.Sprintf("/3/drive/%d/files/%d", ownDrive, childFileID)
		if r.URL.Path != wantPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, wantPath)
		}
		return jsonResponse(200, envelopeSuccess(fileJSONOf(childFileID, rootID, "report.pdf", "file"))), nil
	}), nil)
	result, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if len(calls) != 2 || calls[0].path != ownershipPath(ownDrive) {
		t.Fatalf("calls = %+v, want the ownership check followed by the file endpoint", calls)
	}
	var entry Entry
	if err := json.Unmarshal([]byte(result), &entry); err != nil || entry.ID != childFileID {
		t.Fatalf("entry = %+v, %v", entry, err)
	}
}

// An unclear or failed answer to the ownership check aborts the request before the file endpoint is ever
// reached, whether Infomaniak rejects the check itself or answers it unreadably. There is no silent
// fallback that would let the file endpoint decide instead.
func TestFilesListAbortsWhenTheOwnershipCheckFailsOrIsUnreadable(t *testing.T) {
	cases := []struct {
		name    string
		respond func(*http.Request) (*http.Response, error)
	}{
		{"a server error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(500, `{"result":"error"}`), nil
		}},
		{"an unreadable body", func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `not json`), nil
		}},
		{"a rate limit", func(*http.Request) (*http.Response, error) {
			return jsonResponse(429, `{"result":"error"}`), nil
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == ownershipPath(ownDrive) {
					return tt.respond(r)
				}
				t.Fatalf("unexpected request to the file endpoint: %s", r.URL.Path)
				return nil, nil
			}, nil)
			_, err := env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
			if err == nil {
				t.Fatal("invoke() succeeded, want the unclear ownership check to abort the request")
			}
			if len(calls) != 1 {
				t.Fatalf("calls = %+v, want only the ownership check, no file endpoint", calls)
			}
		})
	}
}
