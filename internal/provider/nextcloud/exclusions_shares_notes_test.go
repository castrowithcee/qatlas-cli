package nextcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func shareIDs(t *testing.T, result any) string {
	t.Helper()
	var ids []string
	for _, s := range result.(*SharesResult).Shares {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, ",")
}

func sharesInTree() string {
	return ocsData(`[` +
		shareJSON("1", "0", aliceUser, "/Reports/Private", "/Private", "bob", "1", "") + `,` +
		shareJSON("2", "0", aliceUser, "/Reports/Private/a.txt", "/a.txt", "bob", "1", "") + `,` +
		shareJSON("3", "0", aliceUser, "/Reports/PRIVATE/x", "/x", "bob", "1", "") + `,` +
		shareJSON("4", "0", aliceUser, "/Reports/Sub", "/Sub", "bob", "1", "") + `,` +
		shareJSON("5", "0", aliceUser, "/Reports/Other/a.txt", "/a.txt", "bob", "1", "") + `,` +
		shareJSON("6", "0", "dave", "/Elsewhere/a", "/Reports/Private/in.txt", "alice", "1", "") + `,` +
		shareJSON("7", "0", "dave", "/Elsewhere/b", "/Reports/Other/in.txt", "alice", "1", "") + `]`)
}

func TestSharesListHidesWhatTheExclusionsReach(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, sharesInTree()), nil })
	r := excludingConnection("Private", "Sub/Deep")
	for incoming, want := range map[bool]string{false: "5", true: "7"} {
		args := `{}`
		if incoming {
			args = `{"shared_with_me":true}`
		}
		got, _, err := callTool(invokeSharesList, r, args)
		if err != nil {
			t.Fatal(err)
		}
		if ids := shareIDs(t, got); ids != want {
			t.Errorf("incoming = %t: ids = %s, want %s (excluded, nested, and ancestor shares hidden)", incoming, ids, want)
		}
	}
}

func TestSharesGetAndChangesTreatReachedSharesAsMissing(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"on exclusion", "/Reports/Private"}, {"below exclusion", "/Reports/private/a.txt"},
		{"ancestor", "/Reports/Sub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := shareServer(t, "0", tc.path, changeOK)
			r := excludingConnection("Private", "Sub/Deep")
			for name, h := range map[string]struct {
				h    handlerFunc
				args string
			}{
				"get":    {invokeSharesGet, `{"share_id":"7"}`},
				"update": {invokeSharesUpdate, `{"share_id":"7","note":"x"}`},
			} {
				_, _, err := callTool(h.h, r, h.args)
				if perr := providerFailure(t, err); perr.Message != messageShareNotFound {
					t.Errorf("%s: err = %v", name, err)
				}
			}
			for _, c := range *calls {
				if c.method != http.MethodGet {
					t.Errorf("a change was sent: %s", c.method)
				}
			}
		})
	}
}

func TestSharesRevokeIsAllowedOnAnAncestorOnly(t *testing.T) {
	r := excludingConnection("Private", "Sub/Deep")
	calls := shareServer(t, "0", "/Reports/Sub", func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`[]`)), nil
	})
	if _, _, err := callTool(invokeSharesDelete, r, `{"share_id":"7"}`); err != nil {
		t.Fatalf("revoke on an ancestor = %v", err)
	}
	if got := (*calls)[len(*calls)-1].method; got != http.MethodDelete {
		t.Errorf("last call = %s, want DELETE", got)
	}
	for _, path := range []string{"/Reports/Private", "/Reports/Private/a.txt"} {
		calls := shareServer(t, "0", path, changeOK)
		_, _, err := callTool(invokeSharesDelete, r, `{"share_id":"7"}`)
		if perr := providerFailure(t, err); perr.Message != messageShareNotFound {
			t.Errorf("%s: err = %v", path, err)
		}
		for _, c := range *calls {
			if c.method != http.MethodGet {
				t.Errorf("%s: a change was sent: %s", path, c.method)
			}
		}
	}
}

func TestSharesCreateIsRefusedBeforeSecretsOnReachedPaths(t *testing.T) {
	r := excludingConnection("Private", "Sub/Deep")
	for _, tc := range []struct {
		path    string
		blocked bool
	}{{"Private", false}, {"private/a.txt", false}, {"Sub/Deep/x", false}, {"Sub/Deep", false}, {"Sub", true}} {
		refuse(t)
		args := `{"type":"user","path":"` + tc.path + `","share_with":"bob"}`
		_, touched, err := callTool(invokeSharesCreate, r, args)
		if err == nil || touched {
			t.Fatalf("%s: err = %v, secret read = %t", tc.path, err, touched)
		}
		if tc.blocked {
			wantBlocked(t, err)
		} else if perr := providerFailure(t, err); strings.Contains(perr.Message, tc.path) {
			t.Errorf("%s: the refusal names the path: %v", tc.path, err)
		}
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, answerShare("7", "0", "/Reports/Other/a.txt", "1")), nil
	})
	if _, _, err := callTool(invokeSharesCreate, r, `{"type":"user","path":"Other/a.txt","share_with":"bob"}`); err != nil {
		t.Errorf("a sibling path = %v", err)
	}
}

func TestSharesListPathArgumentIsGated(t *testing.T) {
	refuse(t)
	_, touched, err := callTool(invokeSharesList, excludingConnection("Private"), `{"path":"Private/a"}`)
	if err == nil || touched {
		t.Errorf("err = %v, secret read = %t", err, touched)
	}
}

// notesConnection binds the whole Notes kind (and a Files folder when given), with exclusions.
func notesConnection(targets []string, excludes ...string) *config.Resolved {
	r := listed(targets...)
	r.Exclude = excludes
	return r
}

var excludedNotes = map[int]string{1: "", 2: "Work", 3: "Work/Secret", 4: "work/SECRET/deep", 5: "Work/Secrets", 6: "Wörk/Sëcret"}

func TestNotesListAndReadHideExcludedCategories(t *testing.T) {
	r := notesConnection([]string{"notes"}, "notes/Work/Secret")
	noteServer(t, excludedNotes, "")
	got, _, err := callTool(invokeNotesList, r, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*NoteList)
	var ids []string
	for _, n := range list.Notes {
		ids = append(ids, n.ID)
	}
	if strings.Join(ids, ",") != "1,2,5,6" || list.Count != 4 {
		t.Errorf("ids = %v, want the excluded category and its sub-categories left out and uncounted", ids)
	}
	for _, id := range []string{"3", "4"} {
		_, _, err := callTool(invokeNotesGet, r, `{"id":"`+id+`"}`)
		if perr := providerFailure(t, err); perr.Message != messageNoteNotFound {
			t.Errorf("get %s = %v", id, err)
		}
		_, _, err = callTool(invokeNotesAttachmentsGet, r, `{"id":"`+id+`","path":"a.png"}`)
		if perr := providerFailure(t, err); perr.Message != messageNoteNotFound {
			t.Errorf("attachment %s = %v", id, err)
		}
	}
	if _, _, err := callTool(invokeNotesGet, r, `{"id":"5"}`); err != nil {
		t.Errorf("a sibling category = %v", err)
	}
}

func TestNotesExclusionComparesAfterNormalisationAndFolding(t *testing.T) {
	r := notesConnection([]string{"notes"}, "notes/Work/Secret")
	for _, category := range []string{"Work/Secret", "work/SECRET", "Work/ .Secret", "Work/S*ecret", "Work/Sëcret"[:0] + "Work/Secret/x/y"} {
		refuse(t)
		args := `{"title":"T","category":` + jsonString(category) + `}`
		_, touched, err := callTool(invokeNotesCreate, r, args)
		if err == nil || touched {
			t.Errorf("create in %q: err = %v, secret read = %t", category, err, touched)
		}
		_, touched, err = callTool(invokeNotesList, r, `{"category":`+jsonString(category)+`}`)
		if err == nil || touched {
			t.Errorf("list of %q: err = %v, secret read = %t", category, err, touched)
		}
	}
	r = notesConnection([]string{"notes"}, "notes/Ä")
	refuse(t)
	if _, touched, err := callTool(invokeNotesCreate, r, `{"title":"T","category":"Ä"}`); err == nil || touched {
		t.Errorf("NFD spelling: err = %v, secret read = %t", err, touched)
	}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestNotesWritesRefuseExcludedCategoriesAndMovesIntoThem(t *testing.T) {
	r := notesConnection([]string{"notes"}, "notes/Work/Secret")
	calls := noteServer(t, excludedNotes, "")
	// Reaching the excluded note and moving a reachable one into the exclusion are both refused.
	_, _, err := callTool(invokeNotesUpdate, r, `{"id":"3","etag":"e3","title":"x"}`)
	if perr := providerFailure(t, err); perr.Message != messageNoteNotFound {
		t.Errorf("update of an excluded note = %v", err)
	}
	_, _, err = callTool(invokeNotesDelete, r, `{"id":"4"}`)
	if perr := providerFailure(t, err); perr.Message != messageNoteNotFound {
		t.Errorf("delete of an excluded note = %v", err)
	}
	_, _, err = callTool(invokeNotesUpdate, r, `{"id":"2","etag":"e2","category":"Work/Secret"}`)
	wantBlocked(t, err)
	for _, c := range *calls {
		if c.method != http.MethodGet {
			t.Errorf("a change was sent: %s %s", c.method, c.url.Path)
		}
	}
}

func TestNoteAttachmentUploadFollowsItsNote(t *testing.T) {
	dir, path := localFile(t, "a.png", "hello")
	r := uploadConnection(dir, "notes")
	r.Exclude = []string{"notes/Work/Secret"}
	f := serveNotes(t, map[int]string{3: "Work/Secret"}, false, func() (*http.Response, error) {
		t.Error("a mutation was sent")
		return nil, errors.New("unreachable")
	})
	_, err := noteUpload(t, r, "3", path)
	if classOf(err) != provider.ClassNotFound || eventMethods(f.calls) != "GET" {
		t.Errorf("err = %v, calls = %s", err, eventMethods(f.calls))
	}
}

// crossServer answers the Notes API (with a notes path) and the Files tree.
func crossServer(t *testing.T, notesPath string, notes map[int]string, settingsStatus int) *[]call {
	return serve(t, func(r *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(r.URL.Path, notesBase)
		switch {
		case path == "settings":
			if settingsStatus != 200 {
				return noteResponse(settingsStatus, nil, "{}"), nil
			}
			return noteResponse(200, nil, `{"notesPath":`+jsonString(notesPath)+`,"fileSuffix":".md"}`), nil
		case path == "notes":
			var items []string
			for id := 1; id <= len(notes); id++ {
				items = append(items, noteJSON(id, notes[id], ""))
			}
			return noteResponse(200, nil, "["+strings.Join(items, ",")+"]"), nil
		case r.Method == http.MethodGet && strings.HasPrefix(path, "notes/"):
			return noteResponse(200, nil, noteJSON(1, "Work", "c")), nil
		case strings.HasPrefix(r.URL.Path, "/ocs/"):
			return ocsResponse(200, sharesInTree()), nil
		}
		return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(strings.TrimSuffix(aliceRoot, "/Reports")+"/Notes/Work/a.txt", "a.txt", "7", "1"))), nil
	})
}

func TestFolderExclusionHidesNotesUnderTheNotesPath(t *testing.T) {
	r := notesConnection([]string{"folder", "notes"}, "folder/Notes/Work/Secret")
	crossServer(t, "Notes", map[int]string{1: "Work", 2: "Work/Secret", 3: "work/secret/x", 4: "Other"}, 200)
	got, _, err := callTool(invokeNotesList, r, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range got.(*NoteList).Notes {
		ids = append(ids, n.ID)
	}
	if strings.Join(ids, ",") != "1,4" {
		t.Errorf("ids = %v", ids)
	}
	_, _, err = callTool(invokeNotesCreate, r, `{"title":"T","category":"Work/Secret"}`)
	wantBlocked(t, err)
	// An exclusion on the notes path itself hides every note.
	r = notesConnection([]string{"folder", "notes"}, "folder/Notes")
	crossServer(t, "Notes", map[int]string{1: "Work", 2: "", 3: "x"}, 200)
	got, _, err = callTool(invokeNotesList, r, `{}`)
	if err != nil || got.(*NoteList).Count != 0 {
		t.Errorf("list = %v, %v", got, err)
	}
	_, _, err = callTool(invokeNotesCreate, r, `{"title":"T"}`)
	wantBlocked(t, err)
}

func TestNotesExclusionHidesTheCategoryFolderInFilesAndShares(t *testing.T) {
	r := notesConnection([]string{"folder", "notes"}, "notes/Work/Secret")
	calls := crossServer(t, "Notes", map[int]string{1: "Work"}, 200)
	for _, path := range []string{"Notes/Work/Secret", "Notes/work/SECRET/a.txt"} {
		_, _, err := callTool(invokeFilesStat, r, `{"path":"`+path+`"}`)
		wantMissing(t, err)
	}
	if _, _, err := callTool(invokeFilesStat, r, `{"path":"Notes/Work/a.txt"}`); err != nil {
		t.Errorf("a sibling folder = %v", err)
	}
	_, _, err := callTool(invokeFilesDelete, r, `{"path":"Notes/Work","etag":"abc"}`)
	wantBlocked(t, err)
	got, _, err := callTool(invokeSharesList, r, `{}`)
	if err != nil || shareIDs(t, got) != "1,2,3,4,5" {
		t.Errorf("shares = %v, %v", got, err)
	}
	for _, c := range *calls {
		if c.method != http.MethodGet && c.method != "PROPFIND" {
			t.Errorf("unexpected change %s %s", c.method, c.url.Path)
		}
	}
}

func TestFolderExclusionReachesSharesOfNotesFolder(t *testing.T) {
	r := notesConnection([]string{"folder", "notes"}, "notes/Private")
	serve(t, func(rq *http.Request) (*http.Response, error) {
		if strings.HasPrefix(rq.URL.Path, notesBase) {
			return noteResponse(200, nil, `{"notesPath":"Reports/Notes"}`), nil
		}
		return ocsResponse(200, ocsData(`[`+
			shareJSON("1", "0", aliceUser, "/Reports/Notes/Private/a", "/a", "bob", "1", "")+`,`+
			shareJSON("2", "0", aliceUser, "/Reports/Notes/Open/a", "/a", "bob", "1", "")+`]`)), nil
	})
	got, _, err := callTool(invokeSharesList, r, `{}`)
	if err != nil || shareIDs(t, got) != "2" {
		t.Errorf("shares = %v, %v", got, err)
	}
}

func TestCrossExclusionFailsClosedWhenTheNotesPathCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		path   string
	}{{"error", 500, "Notes"}, {"missing", 404, "Notes"}, {"empty path", 200, ""}, {"unusable path", 200, "../x"}} {
		t.Run(tc.name, func(t *testing.T) {
			r := notesConnection([]string{"folder", "notes"}, "notes/Secret")
			calls := crossServer(t, tc.path, map[int]string{1: "Work"}, tc.status)
			for _, h := range []struct {
				h    handlerFunc
				args string
			}{{invokeFilesList, `{}`}, {invokeSharesList, `{}`}, {invokeNotesList, `{}`}, {invokeNotesCreate, `{"title":"T"}`}} {
				_, _, err := callTool(h.h, r, h.args)
				wantBlocked(t, err)
			}
			for _, c := range *calls {
				if !strings.HasPrefix(c.url.Path, notesBase+"settings") {
					t.Errorf("a request beyond the settings read: %s", c.url.Path)
				}
			}
		})
	}
}

func TestNoExtraReadWithoutCrossingExclusions(t *testing.T) {
	for _, r := range []*config.Resolved{
		notesConnection([]string{"notes"}, "notes/Secret"),
		notesConnection([]string{"folder", "notes"}),
		excludingConnection("Private"),
	} {
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/", "Reports", "7", "1"))), nil
		})
		_, _, _ = callTool(invokeFilesList, r, `{}`)
		for _, c := range *calls {
			if strings.Contains(c.url.Path, "/notes/") {
				t.Errorf("the notes settings were read: %s", c.url.Path)
			}
		}
	}
}
