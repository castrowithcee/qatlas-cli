package nextcloud

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const noteHint = "may have been applied"

var noteWriteTools = []string{notesList.ID, notesGet.ID, notesCreate.ID, notesUpdate.ID, notesDelete.ID}

// noteWriteFixture answers the pre-read of a note and records the mutation with its headers and body.
type noteWriteFixture struct {
	calls   *[]call
	headers http.Header
	body    string
}

func noteWrite(t *testing.T, tools []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	return eventWrite(t, tools, []string{"notes/C"}, operation, args, confirm)
}

// serveNotes answers GET of a note from notes (id -> category) and every mutation with mutate.
func serveNotes(t *testing.T, notes map[int]string, readonly bool, mutate func() (*http.Response, error)) *noteWriteFixture {
	t.Helper()
	f := &noteWriteFixture{}
	f.calls = serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, notesBase+"notes/"))
			category, ok := notes[id]
			if !ok {
				return noteResponse(404, nil, bodyCanary), nil
			}
			body := noteJSON(id, category, "")
			if readonly {
				body = strings.Replace(body, `"readonly":false`, `"readonly":true`, 1)
			}
			return noteResponse(200, nil, body), nil
		}
		f.headers = r.Header.Clone()
		return mutate()
	})
	return f
}

// storedNote answers a mutation with the note as Nextcloud stores it.
func storedNote(id int, category string) func() (*http.Response, error) {
	return func() (*http.Response, error) { return noteResponse(200, nil, noteJSON(id, category, "x")), nil }
}

func (f *noteWriteFixture) sent(t *testing.T) call {
	t.Helper()
	for _, c := range *f.calls {
		if c.method != http.MethodGet {
			return c
		}
	}
	t.Fatalf("no mutation was sent: %+v", *f.calls)
	return call{}
}

func TestNotesCreatePostsOneNoteInABoundCategory(t *testing.T) {
	f := serveNotes(t, nil, false, storedNote(7, "C/sub"))
	result, err := noteWrite(t, noteWriteTools, "nextcloud.notes.create",
		`{"title":"Ideas","content":"First","category":"C/sub","favorite":true}`, true)
	var got NoteWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil || !got.Created || got.ID != "7" || got.Category != "C/sub" {
		t.Fatalf("create = %s, %v", result, err)
	}
	post := f.sent(t)
	if eventMethods(f.calls) != "POST" || post.url.Path != notesBase+"notes" || f.headers.Get("If-Match") != "" ||
		f.headers.Get("Content-Type") != "application/json" {
		t.Errorf("calls = %+v, headers = %v", *f.calls, f.headers)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(post.body), &sent); err != nil || len(sent) != 4 || sent["title"] != "Ideas" ||
		sent["content"] != "First" || sent["category"] != "C/sub" || sent["favorite"] != true {
		t.Errorf("body = %s", post.body)
	}
}

func TestNotesCreateRefusesAnUnboundCategoryWithoutAnyRequest(t *testing.T) {
	for _, args := range []string{
		`{"title":"T","category":"D"}`, `{"title":"T","category":"Cx"}`, `{"title":"T","category":"C/../D"}`,
		`{"title":"T"}`, `{"title":"T","category":"c"}`,
	} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
			return nil, errors.New("unreachable")
		})
		_, err := noteWrite(t, noteWriteTools, "nextcloud.notes.create", args, true)
		if classOf(err) != provider.ClassPermission && classOf(err) != provider.ClassProviderError || len(*calls) != 0 {
			t.Errorf("%s: err = %v, calls = %d", args, err, len(*calls))
		}
	}
}

func TestNotesCreateInTheRootNeedsAWholeKindBinding(t *testing.T) {
	f := serveNotes(t, nil, false, storedNote(7, ""))
	_, err := eventWrite(t, noteWriteTools, []string{"notes"}, "nextcloud.notes.create", `{"title":"T"}`, true)
	if err != nil || eventMethods(f.calls) != "POST" {
		t.Errorf("err = %v, calls = %+v", err, *f.calls)
	}
}

func TestNotesUpdateSendsOneETagBoundPutAfterOneRead(t *testing.T) {
	f := serveNotes(t, map[int]string{1: "C"}, false, storedNote(1, "C/new"))
	result, err := noteWrite(t, noteWriteTools, "nextcloud.notes.update",
		`{"id":"1","etag":"e1","category":"C/new","favorite":false}`, true)
	var got NoteWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil || !got.Updated || got.ID != "1" {
		t.Fatalf("update = %s, %v", result, err)
	}
	put := f.sent(t)
	if eventMethods(f.calls) != "GET PUT" || put.url.Path != notesBase+"notes/1" || f.headers.Get("If-Match") != `"e1"` {
		t.Errorf("calls = %+v, headers = %v", *f.calls, f.headers)
	}
	if put.body != `{"category":"C/new","favorite":false}` {
		t.Errorf("body = %s, want only the given fields", put.body)
	}
}

func TestNotesUpdateRefusesAnUnboundTargetCategoryBeforeAnyRequest(t *testing.T) {
	for _, category := range []string{"D", "Cx", "C/../D", "c"} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
			return nil, errors.New("unreachable")
		})
		_, err := noteWrite(t, noteWriteTools, "nextcloud.notes.update",
			`{"id":"1","etag":"e1","category":"`+category+`"}`, true)
		if classOf(err) != provider.ClassPermission || len(*calls) != 0 || strings.Contains(fmtErr(err), "C/") {
			t.Errorf("%s: err = %v, calls = %d", category, err, len(*calls))
		}
	}
}

func TestNotesUpdateAndDeleteReadAForeignNoteLikeAMissingOne(t *testing.T) {
	for op, args := range map[string]string{
		"update": `{"id":"5","etag":"e5","title":"x"}`, "delete": `{"id":"5"}`,
		"missing update": `{"id":"9","etag":"e9","title":"x"}`,
	} {
		f := serveNotes(t, map[int]string{5: "Secret-Category"}, false, func() (*http.Response, error) {
			t.Errorf("%s: a mutation was sent", op)
			return nil, errors.New("unreachable")
		})
		_, err := noteWrite(t, noteWriteTools, "nextcloud.notes."+strings.Fields(op)[len(strings.Fields(op))-1], args, true)
		if classOf(err) != provider.ClassNotFound || err.Error() == "" || strings.Contains(fmtErr(err), "Secret") ||
			eventMethods(f.calls) != "GET" {
			t.Errorf("%s: err = %v, calls = %+v", op, err, *f.calls)
		}
	}
}

func TestNotesWritesRefuseAReadOnlyNoteWithoutMutation(t *testing.T) {
	for op, args := range map[string]string{"update": `{"id":"1","etag":"e1","title":"x"}`, "delete": `{"id":"1"}`} {
		f := serveNotes(t, map[int]string{1: "C"}, true, func() (*http.Response, error) {
			t.Errorf("%s: a mutation was sent", op)
			return nil, errors.New("unreachable")
		})
		_, err := noteWrite(t, noteWriteTools, "nextcloud.notes."+op, args, true)
		if classOf(err) != provider.ClassPermission || !strings.Contains(fmtErr(err), "read-only") || eventMethods(f.calls) != "GET" {
			t.Errorf("%s: err = %v, calls = %+v", op, err, *f.calls)
		}
	}
}

func TestNotesUpdateRefusesAChangedETagBeforeThePut(t *testing.T) {
	f := serveNotes(t, map[int]string{1: "C"}, false, func() (*http.Response, error) {
		t.Error("a mutation was sent")
		return nil, errors.New("unreachable")
	})
	_, err := noteWrite(t, noteWriteTools, "nextcloud.notes.update", `{"id":"1","etag":"old","title":"x"}`, true)
	if !strings.Contains(fmtErr(err), "etag") || strings.Contains(fmtErr(err), noteHint) || eventMethods(f.calls) != "GET" {
		t.Errorf("err = %v, calls = %+v", err, *f.calls)
	}
}

func TestNotesDeleteSendsOneDeleteAfterOneRead(t *testing.T) {
	f := serveNotes(t, map[int]string{1: "C/x"}, false, func() (*http.Response, error) { return noteResponse(200, nil, ``), nil })
	result, err := noteWrite(t, noteWriteTools, "nextcloud.notes.delete", `{"id":"1"}`, true)
	var got NoteDeleteResult
	if err != nil || json.Unmarshal(result, &got) != nil || got != (NoteDeleteResult{ID: "1", Deleted: true}) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if eventMethods(f.calls) != "GET DELETE" || f.sent(t).url.Path != notesBase+"notes/1" {
		t.Errorf("calls = %+v", *f.calls)
	}
}

func TestNotesWritesNeedConfirmation(t *testing.T) {
	for op, args := range map[string]string{"create": `{"title":"T","category":"C"}`,
		"update": `{"id":"1","etag":"e1","title":"x"}`, "delete": `{"id":"1"}`} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			t.Errorf("%s: the provider was contacted: %s", op, r.Method)
			return nil, errors.New("unreachable")
		})
		if _, err := noteWrite(t, noteWriteTools, "nextcloud.notes."+op, args, false); err == nil || len(*calls) != 0 {
			t.Errorf("unconfirmed %s: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestNotesDeleteNeedsAToolsListEntry(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s", r.Method)
		return nil, errors.New("unreachable")
	})
	_, err := noteWrite(t, []string{notesList.ID, notesGet.ID, notesCreate.ID, notesUpdate.ID}, "nextcloud.notes.delete",
		`{"id":"1"}`, true)
	if err == nil || len(*calls) != 0 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestNotesWriteContentIsBoundedLocally(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s", r.Method)
		return nil, errors.New("unreachable")
	})
	big := strings.Repeat("a", maxNoteWriteContent+1)
	for op, args := range map[string]string{"create": `{"title":"T","category":"C","content":"` + big + `"}`,
		"update": `{"id":"1","etag":"e1","content":"` + big + `"}`} {
		if _, err := noteWrite(t, noteWriteTools, "nextcloud.notes."+op, args, true); err == nil {
			t.Errorf("%s: accepted content beyond the limit", op)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %d", len(*calls))
	}
	// Multi-byte text counts in bytes, which is what the request carries.
	_, err := noteWrite(t, noteWriteTools, "nextcloud.notes.create",
		`{"title":"T","category":"C","content":"`+strings.Repeat("ä", maxNoteWriteContent/2+1)+`"}`, true)
	if err == nil || len(*calls) != 0 {
		t.Errorf("multi-byte content: err = %v, calls = %d", err, len(*calls))
	}
}

func TestNotesWriteOutcomes(t *testing.T) {
	ops := map[string]string{"create": `{"title":"T","category":"C"}`,
		"update": `{"id":"1","etag":"e1","title":"x"}`, "delete": `{"id":"1"}`}
	check := func(t *testing.T, op string, answer func() (*http.Response, error), wantHint bool, contains string) {
		f := serveNotes(t, map[int]string{1: "C"}, false, answer)
		_, err := noteWrite(t, noteWriteTools, "nextcloud.notes."+op, ops[op], true)
		mutations := 0
		for _, c := range *f.calls {
			if c.method != http.MethodGet {
				mutations++
			}
		}
		text := fmtErr(err)
		if err == nil || mutations != 1 || strings.Contains(text, noteHint) != wantHint || !strings.Contains(text, contains) ||
			strings.Contains(text, "canary") || strings.Contains(text, "evil.example") {
			t.Errorf("%s: err = %v, mutations = %d", op, err, mutations)
		}
	}
	for op := range ops {
		t.Run(op, func(t *testing.T) {
			for _, code := range []int{500, 502, 503, 504} {
				check(t, op, func() (*http.Response, error) { return status(code), nil }, true, "")
			}
			check(t, op, func() (*http.Response, error) { return nil, timeoutError{} }, true, "")
			check(t, op, func() (*http.Response, error) { return nil, errors.New("connection reset by peer") }, true, "")
			if op != "delete" { // the answer of a delete is never read
				check(t, op, func() (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(failingBody{})}, nil
				}, true, "")
			}
			check(t, op, func() (*http.Response, error) { return status(507), nil }, false, "quota")
			check(t, op, func() (*http.Response, error) { return status(403), nil }, false, "")
			check(t, op, func() (*http.Response, error) {
				response := status(http.StatusFound)
				response.Header.Set("Location", "https://evil.example.invalid/redirect-canary")
				return response, nil
			}, false, "redirect")
		})
	}
	t.Run("unreadable answer", func(t *testing.T) {
		for _, op := range []string{"create", "update"} {
			check(t, op, func() (*http.Response, error) { return noteResponse(200, nil, `not json`), nil }, true, "")
			check(t, op, func() (*http.Response, error) { return noteResponse(200, nil, `{"id":0}`), nil }, true, "")
		}
	})
}

func TestNotesUpdateConflictIsClear(t *testing.T) {
	f := serveNotes(t, map[int]string{1: "C"}, false, func() (*http.Response, error) { return status(412), nil })
	_, err := noteWrite(t, noteWriteTools, "nextcloud.notes.update", `{"id":"1","etag":"e1","title":"x"}`, true)
	text := fmtErr(err)
	if !strings.Contains(text, "precondition failed") || strings.Contains(text, noteHint) || strings.Contains(text, "canary") ||
		eventMethods(f.calls) != "GET PUT" {
		t.Errorf("err = %v, calls = %+v", err, *f.calls)
	}
}

func TestNotesWritesAreNeverRepeatedAfterAnUnclearResult(t *testing.T) {
	for op, args := range map[string]string{"create": `{"title":"T","category":"C"}`,
		"update": `{"id":"1","etag":"e1","title":"x"}`, "delete": `{"id":"1"}`} {
		f := serveNotes(t, map[int]string{1: "C"}, false, func() (*http.Response, error) { return nil, timeoutError{} })
		_, err := noteWrite(t, noteWriteTools, "nextcloud.notes."+op, args, true)
		mutations := 0
		for _, c := range *f.calls {
			if c.method != http.MethodGet {
				mutations++
			}
		}
		if err == nil || mutations != 1 || !strings.Contains(fmtErr(err), noteHint) {
			t.Errorf("%s: err = %v, mutations = %d", op, err, mutations)
		}
	}
}

func TestNotesWriteDescriptorsDeclareTheirRisk(t *testing.T) {
	for _, tc := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		list   bool
	}{{notesCreate, capability.EffectCreate, false}, {notesUpdate, capability.EffectUpdate, false},
		{notesDelete, capability.EffectDelete, true}} {
		r := tc.d.Risk
		if r.Effect != tc.effect || r.Idempotency == "" || r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld ||
			r.DataSensitivity != notesSensitivity || tc.d.RequiresToolAllowList != tc.list || tc.d.Group != "" && tc.d.Group != groupNotes {
			t.Errorf("%s = %+v", tc.d.ID, tc.d)
		}
	}
}
