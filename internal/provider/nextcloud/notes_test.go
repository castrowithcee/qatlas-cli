package nextcloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const notesBase = "/index.php/apps/notes/api/v1/"

func jsonResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func noteJSON(id int, category, content string) string {
	return `{"id":` + strconv.Itoa(id) + `,"etag":"e` + strconv.Itoa(id) + `","readonly":false,"modified":1760000000,` +
		`"title":"T` + strconv.Itoa(id) + `","category":` + strconv.Quote(category) + `,"content":` + strconv.Quote(content) + `,"favorite":true}`
}

// noteServer answers the Notes API from a fixed set of notes (id -> category).
func noteServer(t *testing.T, notes map[int]string, cursor string) *[]call {
	return serve(t, func(r *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(r.URL.Path, notesBase)
		switch {
		case path == "notes":
			var items []string
			for id := 1; id <= len(notes); id++ {
				items = append(items, noteJSON(id, notes[id], ""))
			}
			header := http.Header{}
			if cursor != "" {
				header.Set("X-Notes-Chunk-Cursor", cursor)
				header.Set("X-Notes-Chunk-Pending", "3")
			}
			return jsonResponse(200, header, "["+strings.Join(items, ",")+"]"), nil
		case strings.HasPrefix(path, "notes/"):
			id, _ := strconv.Atoi(strings.TrimPrefix(path, "notes/"))
			category, ok := notes[id]
			if !ok {
				return jsonResponse(404, nil, bodyCanary), nil
			}
			return jsonResponse(200, nil, noteJSON(id, category, "body of "+strconv.Itoa(id))), nil
		case path == "settings":
			return jsonResponse(200, nil, `{"notesPath":"Notes","fileSuffix":".md","other":"x"}`), nil
		case strings.HasPrefix(path, "attachment/"):
			return &http.Response{StatusCode: 200, ContentLength: 3, Header: http.Header{"Content-Type": {"image/png"}},
				Body: io.NopCloser(strings.NewReader("PNG"))}, nil
		}
		t.Errorf("unexpected request %s", r.URL)
		return jsonResponse(500, nil, ""), nil
	})
}

var sampleNotes = map[int]string{1: "", 2: "C", 3: "C/x", 4: "Cx", 5: "D", 6: "c", 7: "C/../D"}

func runNote(fn capability.Handler, resolved *config.Resolved, args string) (any, error) {
	red := &redact.Redactor{}
	return notesBound(fn)(context.Background(), resolved, resolver(red), red, json.RawMessage(args))
}

func TestNotesListKeepsOnlyTheBoundCategoriesAndTheirSubcategories(t *testing.T) {
	calls := noteServer(t, sampleNotes, "")
	got, err := runNote(invokeNotesList, listed("notes/C"), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*NoteList)
	var ids []string
	for _, n := range list.Notes {
		ids = append(ids, n.ID)
	}
	if strings.Join(ids, ",") != "2,3" || list.Count != 2 {
		t.Errorf("notes = %v, want 2 and 3 (C and C/x, not Cx, c, D, C/../D, uncategorised)", ids)
	}
	q := (*calls)[0].url.Query()
	if len(*calls) != 1 || q.Get("exclude") != "content" || q.Get("category") != "" || q.Get("chunkSize") != "50" {
		t.Errorf("request = %+v", (*calls)[0].url)
	}
	all, err := runNote(invokeNotesList, listed("notes"), `{}`)
	if err != nil || all.(*NoteList).Count != len(sampleNotes) {
		t.Errorf("whole kind = %v, %v", all, err)
	}
	two, err := runNote(invokeNotesList, listed("notes/Cx", "notes/D"), `{}`)
	if err != nil || two.(*NoteList).Count != 2 {
		t.Errorf("two categories = %v, %v", two, err)
	}
	narrowed, err := runNote(invokeNotesList, listed("notes/C"), `{"category":"C/x"}`)
	if err != nil || narrowed.(*NoteList).Count != 1 {
		t.Errorf("narrowed = %v, %v", narrowed, err)
	}
	if encoded, _ := json.Marshal(got); strings.Contains(string(encoded), "Cx") || strings.Contains(string(encoded), "Pending") {
		t.Errorf("result leaks foreign data: %s", encoded)
	}
}

func TestNotesListRefusesACategoryOutsideTheBindingLocally(t *testing.T) {
	refuse(t)
	for _, category := range []string{"D", "Cx", "c", "../C", "C/../D"} {
		_, err := runNote(invokeNotesList, listed("notes/C"), `{"category":`+strconv.Quote(category)+`}`)
		if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "C") && strings.Contains(err.Error(), "bound to") {
			t.Errorf("category %q = %v", category, err)
		}
		if err != nil && len(category) > 2 && strings.Contains(err.Error(), category) {
			t.Errorf("the refusal names %q: %v", category, err)
		}
	}
}

func TestNotesListSendsTheCursorAndReportsTheNext(t *testing.T) {
	calls := noteServer(t, sampleNotes, "next-cursor_1")
	got, err := runNote(invokeNotesList, listed("notes"), `{"chunk_size":7,"chunk_cursor":"abc,12=="}`)
	if err != nil {
		t.Fatal(err)
	}
	q := (*calls)[0].url.Query()
	if q.Get("chunkCursor") != "abc,12==" || q.Get("chunkSize") != "7" {
		t.Errorf("query = %v", q)
	}
	if list := got.(*NoteList); list.NextCursor != "next-cursor_1" {
		t.Errorf("next cursor = %q", list.NextCursor)
	}
	if !strings.HasSuffix((*calls)[0].url.Path, "/notes") {
		t.Errorf("path = %s", (*calls)[0].url.Path)
	}
}

func TestNotesListRefusesBadArgumentsAndAHostileCursorFromTheServer(t *testing.T) {
	refuse(t)
	for _, args := range []string{`{"chunk_size":0}`, `{"chunk_size":201}`, `{"chunk_cursor":"a b"}`, `{"chunk_cursor":"a&x=1"}`,
		`{"chunk_cursor":"` + strings.Repeat("a", 257) + `"}`, `{"unknown":1}`} {
		if _, err := runNote(invokeNotesList, listed("notes"), args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	noteServer(t, sampleNotes, "../x?y")
	if _, err := runNote(invokeNotesList, listed("notes"), `{}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("hostile cursor = %v", err)
	}
}

func TestNotesListCapsAndReportsTruncation(t *testing.T) {
	var items []string
	for i := 1; i <= 205; i++ {
		items = append(items, noteJSON(i, "C", ""))
	}
	items[0] = strings.Replace(items[0], `"T1"`, `"`+strings.Repeat("é", 700)+`"`, 1)
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, http.Header{"X-Notes-Chunk-Cursor": {"n"}}, "["+strings.Join(items, ",")+"]"), nil
	})
	got, err := runNote(invokeNotesList, listed("notes"), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*NoteList)
	if list.Count != 200 || !list.Truncated || list.NextCursor != "" {
		t.Errorf("count=%d truncated=%v cursor=%q", list.Count, list.Truncated, list.NextCursor)
	}
	if first := list.Notes[0]; !first.Truncated || len(first.Title) > maxValueLength || !strings.HasSuffix(first.Title, "é") {
		t.Errorf("title not cut cleanly: %d truncated=%v", len(first.Title), first.Truncated)
	}
}

func TestNotesGetAnswersAForeignNoteLikeAMissingOne(t *testing.T) {
	noteServer(t, sampleNotes, "")
	_, errMissing := runNote(invokeNotesGet, listed("notes/C"), `{"id":"99"}`)
	_, errForeign := runNote(invokeNotesGet, listed("notes/C"), `{"id":"5"}`)
	_, errPrefix := runNote(invokeNotesGet, listed("notes/C"), `{"id":"4"}`)
	for _, err := range []error{errForeign, errPrefix} {
		if classOf(err) != provider.ClassNotFound || err.Error() != errMissing.Error() {
			t.Errorf("foreign = %v, missing = %v", err, errMissing)
		}
	}
	if strings.Contains(errMissing.Error(), bodyCanary) {
		t.Errorf("provider text in error: %v", errMissing)
	}
	got, err := runNote(invokeNotesGet, listed("notes/C"), `{"id":"3"}`)
	if err != nil {
		t.Fatal(err)
	}
	if note := got.(*NoteContent); note.Content != "body of 3" || note.Category != "C/x" || note.ContentSize != 9 || note.ModifiedAt == "" {
		t.Errorf("note = %+v", note)
	}
}

func TestNotesGetCapsTheContent(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, nil, noteJSON(8, "C", strings.Repeat("ä", maxNoteContent))), nil
	})
	got, err := runNote(invokeNotesGet, listed("notes"), `{"id":"8"}`)
	if err != nil {
		t.Fatal(err)
	}
	note := got.(*NoteContent)
	if !note.ContentTruncated || len(note.Content) > maxNoteContent || note.ContentSize != 2*maxNoteContent {
		t.Errorf("truncated=%v len=%d size=%d", note.ContentTruncated, len(note.Content), note.ContentSize)
	}
}

func TestNotesToolsRefuseWithoutANotesTargetBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	handlers := map[string]struct {
		fn   capability.Handler
		args string
	}{
		"list":       {invokeNotesList, `{}`},
		"get":        {invokeNotesGet, `{"id":"1"}`},
		"attachment": {invokeNotesAttachmentsGet, `{"id":"1","path":"a.png"}`},
		"settings":   {invokeNotesSettingsGet, `{}`},
	}
	for _, targets := range [][]string{{"folder/Reports"}, {"calendar/SECRETID", "account"}, {"admin"}} {
		for name, h := range handlers {
			_, err := notesBound(h.fn)(context.Background(), listed(targets...), res, &redact.Redactor{}, json.RawMessage(h.args))
			if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "SECRETID") {
				t.Errorf("%s %v = %v", name, targets, err)
			}
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestNotesAttachmentPathsAreRefusedBeforeIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, path := range []string{"", "/etc/passwd", "../x.png", ".attachments.1/../../x", "a/./b", "a//b", "a\\b", "\\a", ".", "..",
		"a/b/", "a\u0000b", "a\nb", strings.Repeat("a/", 40), strings.Repeat("a", 1025)} {
		args := `{"id":"1","path":` + strconv.Quote(path) + `}`
		if _, err := notesBound(invokeNotesAttachmentsGet)(context.Background(), listed("notes"), res, &redact.Redactor{}, json.RawMessage(args)); err == nil {
			t.Errorf("path %q was accepted", path)
		}
	}
	for _, id := range []string{"", "0", "01", "-1", "1/2", "1.5", "abc", strings.Repeat("9", 19)} {
		args := `{"id":` + strconv.Quote(id) + `,"path":"a.png"}`
		if _, err := notesBound(invokeNotesAttachmentsGet)(context.Background(), listed("notes"), res, &redact.Redactor{}, json.RawMessage(args)); err == nil {
			t.Errorf("id %q was accepted", id)
		}
		if _, err := notesBound(invokeNotesGet)(context.Background(), listed("notes"), res, &redact.Redactor{}, json.RawMessage(`{"id":`+strconv.Quote(id)+`}`)); err == nil {
			t.Errorf("get id %q was accepted", id)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestNotesAttachmentOfAForeignNoteSendsNoAttachmentRequest(t *testing.T) {
	calls := noteServer(t, sampleNotes, "")
	_, errMissing := runNote(invokeNotesAttachmentsGet, listed("notes/C"), `{"id":"99","path":"a.png"}`)
	_, errForeign := runNote(invokeNotesAttachmentsGet, listed("notes/C"), `{"id":"5","path":".attachments.5/a.png"}`)
	if classOf(errForeign) != provider.ClassNotFound || errForeign.Error() != errMissing.Error() {
		t.Errorf("foreign = %v, missing = %v", errForeign, errMissing)
	}
	for _, c := range *calls {
		if strings.Contains(c.url.Path, "attachment") {
			t.Errorf("attachment requested: %s", c.url)
		}
	}
	*calls = nil
	got, err := runNote(invokeNotesAttachmentsGet, listed("notes/C"), `{"id":"2","path":".attachments.2/a b.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	att := got.(*NoteAttachment)
	if raw, _ := base64.StdEncoding.DecodeString(att.ContentBase64); string(raw) != "PNG" || att.Size != 3 || att.ContentType != "image/png" {
		t.Errorf("attachment = %+v", att)
	}
	if len(*calls) != 2 || (*calls)[0].url.Query().Get("exclude") != "content" ||
		(*calls)[1].url.Path != notesBase+"attachment/2" || (*calls)[1].url.Query().Get("path") != ".attachments.2/a b.png" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestNotesAttachmentToALocalPath(t *testing.T) {
	dir := t.TempDir()
	noteServer(t, sampleNotes, "")
	target := filepath.Join(dir, "out.png")
	args := `{"id":"2","path":".attachments.2/a.png","local_path":` + strconv.Quote(target) + `}`
	red := &redact.Redactor{}
	resolved := localConnection("", dir)
	resolved.Targets = []string{"notes/C"}
	run := func(ctx context.Context) (any, error) {
		return notesBound(invokeNotesAttachmentsGet)(ctx, resolved, resolver(red), red, json.RawMessage(args))
	}
	got, err := run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d := got.(*NoteAttachmentDownload); d.Size != 3 || d.Name != "a.png" || d.SHA256 == "" {
		t.Errorf("download = %+v", d)
	}
	if data, _ := os.ReadFile(target); string(data) != "PNG" {
		t.Errorf("file = %q", data)
	}
	if _, err := run(context.Background()); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Errorf("unconfirmed overwrite = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "x.png")
	refuse(t)
	bad := strings.Replace(args, strconv.Quote(target), strconv.Quote(outside), 1)
	if _, err := notesBound(invokeNotesAttachmentsGet)(context.Background(), resolved, resolver(red), red, json.RawMessage(bad)); err == nil {
		t.Error("a path outside the release was written")
	}
}

func TestNotesSettingsReportOnlyTheDocumentedFields(t *testing.T) {
	calls := noteServer(t, sampleNotes, "")
	got, err := runNote(invokeNotesSettingsGet, listed("notes/C"), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.(*NotesSettings); s.NotesPath != "Notes" || s.FileSuffix != ".md" {
		t.Errorf("settings = %+v", s)
	}
	if len(*calls) != 1 || (*calls)[0].url.Path != notesBase+"settings" || (*calls)[0].method != http.MethodGet {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestNotesRequestsUseTheInstallationPathAndNeverFollowRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 307, 308} {
		for name, run := range map[string]func() error{
			"list":     func() error { _, err := runNote(invokeNotesList, listed("notes"), `{}`); return err },
			"get":      func() error { _, err := runNote(invokeNotesGet, listed("notes"), `{"id":"1"}`); return err },
			"settings": func() error { _, err := runNote(invokeNotesSettingsGet, listed("notes"), `{}`); return err },
			"attachment": func() error {
				_, err := runNote(invokeNotesAttachmentsGet, listed("notes"), `{"id":"1","path":"a.png"}`)
				return err
			},
		} {
			t.Run(strconv.Itoa(status)+" "+name, func(t *testing.T) {
				calls := serve(t, func(r *http.Request) (*http.Response, error) {
					if strings.Contains(r.URL.Path, "outside-canary") {
						t.Errorf("redirect followed: %s", r.URL)
					}
					// A redirect on the first request; the attachment case redirects its second one.
					if name != "attachment" || strings.Contains(r.URL.Path, "attachment/") {
						return &http.Response{StatusCode: status, Header: http.Header{"Location": {mainInstance + redirectTarget}},
							Body: io.NopCloser(strings.NewReader(bodyCanary))}, nil
					}
					return jsonResponse(200, nil, noteJSON(1, "", "")), nil
				})
				err := run()
				if err == nil || classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "does not follow") {
					t.Fatalf("err = %v", err)
				}
				for _, bad := range []string{redirectTarget, "outside-canary", "cloud.example.invalid", bodyCanary} {
					if strings.Contains(err.Error(), bad) {
						t.Errorf("error carries %q: %v", bad, err)
					}
				}
				if want := 1; name != "attachment" && len(*calls) != want {
					t.Errorf("calls = %d", len(*calls))
				}
			})
		}
	}

}

func TestNotesRequestsKeepTheInstallationPath(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, nil, `{"notesPath":"Notes","fileSuffix":".txt"}`), nil
	})
	red := &redact.Redactor{}
	resolved := resolvedConnection("x", "cloud-partner", carolUserEnv, carolTokenEnv, partnerInstance, "")
	resolved.Targets = []string{"notes"}
	if _, err := notesBound(invokeNotesSettingsGet)(context.Background(), resolved, resolver(red), red, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].url.Path != "/nextcloud"+notesBase+"settings" || (*calls)[0].auth != basicAuth(carolUser, carolToken) ||
		(*calls)[0].url.Host != "partner.example.invalid" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestNotesRegistrationGroupProfileAndRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	var profile *config.ToolProfile
	for i := range metadata.Profiles {
		if metadata.Profiles[i].ID == "notes-read" {
			profile = &metadata.Profiles[i]
		}
	}
	want := []string{notesList.ID, notesGet.ID, notesAttachmentsGet.ID, notesSettingsGet.ID}
	if profile == nil || strings.Join(profile.Tools, ",") != strings.Join(want, ",") || profile.Recommended {
		t.Fatalf("profile = %+v", profile)
	}
	for _, id := range want {
		var found bool
		for _, d := range reg.Provider(Provider) {
			if d.ID != id {
				continue
			}
			found = true
			if d.Group != groupNotes || d.Risk.Effect != capability.EffectRead || d.Risk.DataSensitivity != notesSensitivity ||
				d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld {
				t.Errorf("%s = group %q risk %+v", id, d.Group, d.Risk)
			}
			if (id == notesAttachmentsGet.ID) != (d.LocalFiles == config.LocalFilesWrite) {
				t.Errorf("%s local files = %v", id, d.LocalFiles)
			}
		}
		if !found {
			t.Errorf("%s is not registered", id)
		}
	}
}

func TestNotesListDropsPrunedEntries(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, nil, "["+noteJSON(1, "", "")+","+noteJSON(2, "C", "")+`,{"id":3},{"id":4}]`), nil
	})
	got, err := runNote(invokeNotesList, listed("notes"), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*NoteList)
	if list.Count != 2 || list.Notes[0].ID != "1" || list.Notes[1].Category != "C" {
		t.Errorf("notes = %+v", list.Notes)
	}
}
