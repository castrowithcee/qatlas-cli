package nextcloud

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// ocsRefusal is the OCS envelope of a refusal; the data holds a second canary that no original may carry.
func ocsRefusal(code int) string {
	return `{"ocs":{"meta":{"status":"failure","statuscode":` + strconv.Itoa(code) + `,"message":"` + bodyCanary +
		`"},"data":{"secret":"` + nameCanary + `"}}}`
}

// deckRefusal is the answer of the Deck API to a refused request.
func deckRefusal(code int) string {
	return `{"status":` + strconv.Itoa(code) + `,"message":"` + bodyCanary + `"}`
}

func wantMeta(code int) []provider.OriginalEntry {
	return []provider.OriginalEntry{{Code: strconv.Itoa(code), Message: bodyCanary}}
}

func checkNoOtherCanary(t *testing.T, err error) {
	t.Helper()
	if original := originalOf(err); original != nil {
		for _, entry := range original.Entries {
			if entry.Message == nameCanary || entry.Code == nameCanary || entry.Type == nameCanary {
				t.Errorf("the original carries the data of the envelope: %+v", original)
			}
		}
	}
}

// An OCS refusal passes status, meta status code, and meta message on, never the data, for a failing HTTP
// status and for a successful status that repeats the refusal in its meta.
func TestOCSErrorsCarryTheOriginal(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 500} {
		t.Run("http "+strconv.Itoa(code), func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(code, ocsRefusal(code)), nil })
			_, err := adminCall(invokeAdminUsersList, adminConnection(), `{}`)
			checkOriginal(t, err, code, wantMeta(code))
			checkNoOtherCanary(t, err)
		})
		t.Run("meta "+strconv.Itoa(code), func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsRefusal(code)), nil })
			_, err := adminCall(invokeAdminUsersList, adminConnection(), `{}`)
			checkOriginal(t, err, 200, wantMeta(code))
			checkNoOtherCanary(t, err)
		})
	}
}

func TestOCSErrorWithoutJSONKeepsTheText(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(502, bodyCanary), nil })
	_, err := adminCall(invokeAdminUsersList, adminConnection(), `{}`)
	checkOriginal(t, err, 502, []provider.OriginalEntry{{Message: bodyCanary}})
	serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(500, ""), nil })
	_, err = adminCall(invokeAdminUsersList, adminConnection(), `{}`)
	checkOriginal(t, err, 500, nil)
}

// A successful answer with an implausible meta stays an invalid response without an original.
func TestOCSImplausibleMetaCarriesNoOriginal(t *testing.T) {
	for _, body := range []string{
		`{"ocs":{"meta":{"status":"failure","statuscode":200,"message":"` + bodyCanary + `"},"data":[]}}`,
		`{"ocs":{"meta":{"status":"ok","statuscode":100,"message":"` + bodyCanary + `"},"data":[]}}`,
	} {
		serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, body), nil })
		_, err := adminCall(invokeAdminUsersList, adminConnection(), `{}`)
		if err == nil || classOf(err) != provider.ClassInvalidResponse {
			t.Fatalf("err = %v, want an invalid response", err)
		}
		if original := originalOf(err); original != nil {
			t.Errorf("original = %+v, want none", original)
		}
	}
}

// The tools that give a refusal their own message keep the original of the answer.
func TestOCSReplacementsKeepTheOriginal(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(403, ocsRefusal(403)), nil })
	_, err := adminCall(invokeAdminUsersGet, adminConnection(), `{"user_id":"bob"}`)
	if classOf(err) != provider.ClassPermission {
		t.Fatalf("admin 403: err = %v", err)
	}
	checkOriginal(t, err, 403, wantMeta(403))

	serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(404, ocsRefusal(404)), nil })
	_, err = adminCall(invokeAdminUsersGet, adminConnection(), `{"user_id":"bob"}`)
	if err == nil || err.Error() == "" {
		t.Fatal("admin 404 was accepted")
	}
	checkOriginal(t, err, 404, wantMeta(404))

	c, _ := client(t)
	_, err = c.GetShare(context.Background(), "5")
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("share 404: err = %v", err)
	}
	checkOriginal(t, err, 404, wantMeta(404))

	_, err = remoteTool(invokeRemoteSharesList, true, `{}`)
	checkOriginal(t, err, 404, wantMeta(404))
	_, err = remoteTool(invokeRemoteSharesGet, true, `{"share_id":"5"}`)
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("remote share 404: err = %v", err)
	}
	checkOriginal(t, err, 404, wantMeta(404))
}

// A refused change keeps the original on the uncertain path as well.
func TestOCSChangeErrorsCarryTheOriginal(t *testing.T) {
	for _, code := range []int{400, 403, 404, 500} {
		serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(code, ocsRefusal(code)), nil })
		_, err := remoteTool(invokeRemoteSharesRemove, true, `{"share_id":"5"}`)
		checkOriginal(t, err, code, wantMeta(code))
		serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsRefusal(code)), nil })
		_, err = remoteTool(invokeRemoteSharesRemove, true, `{"share_id":"5"}`)
		checkOriginal(t, err, 200, wantMeta(code))
	}
}

// Deck passes its status code and message on, also for the direct 403 and 404 answers.
func TestDeckErrorsCarryTheOriginal(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 422, 500} {
		t.Run("read "+strconv.Itoa(code), func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(code, deckRefusal(code)), nil
			})
			_, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deck.boards.list", `{}`)
			checkOriginal(t, err, code, wantMeta(code))
		})
		t.Run("write "+strconv.Itoa(code), func(t *testing.T) {
			serve(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return jsonResponse(http.StatusOK, managedBoard("2", "B", true)), nil
				}
				return jsonResponse(code, deckRefusal(code)), nil
			})
			_, err := deckWrite(t, []string{"deck/2"}, "nextcloud.deck.boards.update", `{"board_id":"2","title":"x"}`, true)
			checkOriginal(t, err, code, wantMeta(code))
		})
	}
}

func TestDeckErrorWithoutJSONKeepsTheText(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(404, bodyCanary), nil })
	_, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deck.boards.list", `{}`)
	checkOriginal(t, err, 404, []provider.OriginalEntry{{Message: bodyCanary}})
}

// The comment tools give a refusal their own message and keep the original of the OCS answer.
func TestDeckCommentRefusalsKeepTheOriginal(t *testing.T) {
	for _, code := range []int{403, 404} {
		deckCommentServer(t, func(*http.Request) (*http.Response, error) {
			return ocsResponse(code, ocsRefusal(code)), nil
		})
		_, err := deckCommentCall(t, "nextcloud.deck.comments.create", `{`+deckCommentRef+`,"message":"hi"}`)
		if err == nil {
			t.Fatalf("%d: the refusal was accepted", code)
		}
		checkOriginal(t, err, code, wantMeta(code))
	}
}

// The Deck attachment download reads a failed answer in the Deck format.
func TestDeckAttachmentErrorsCarryTheOriginal(t *testing.T) {
	for _, code := range []int{403, 404, 500} {
		attachmentServer(t, func() *http.Response { return jsonResponse(code, deckRefusal(code)) })
		_, err := getAttachment(attachmentConnection("", "deck/2"), `{`+getArgs+`"5"}`)
		if err == nil {
			t.Fatalf("%d: the refusal was accepted", code)
		}
		checkOriginal(t, err, code, wantMeta(code))

		dir := t.TempDir()
		args := `{` + getArgs + `"5","local_path":` + strconv.Quote(filepath.Join(dir, "out.pdf")) + `}`
		_, err = getAttachment(attachmentConnection(dir, "deck/2"), args)
		if err == nil {
			t.Fatalf("%d: the download refusal was accepted", code)
		}
		checkOriginal(t, err, code, wantMeta(code))
		if _, statErr := os.Stat(filepath.Join(dir, "out.pdf")); statErr == nil {
			t.Errorf("%d: a file was written", code)
		}
	}
}

// The Notes API documents no error body: a text is kept, a JSON object contributes no field.
func TestNotesErrorsCarryTheOriginal(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 423, 500} {
		serve(t, func(*http.Request) (*http.Response, error) {
			return noteResponse(code, nil, bodyCanary), nil
		})
		_, err := runNote(invokeNotesList, listed("notes/C"), `{}`)
		checkOriginal(t, err, code, []provider.OriginalEntry{{Message: bodyCanary}})
		serve(t, func(*http.Request) (*http.Response, error) {
			return noteResponse(code, nil, `{"message":"`+bodyCanary+`"}`), nil
		})
		_, err = runNote(invokeNotesList, listed("notes/C"), `{}`)
		checkOriginal(t, err, code, nil)
	}
}

// A missing note and a missing attachment keep the original of the answer under their own message.
func TestNotesReplacementsKeepTheOriginal(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return noteResponse(404, nil, bodyCanary), nil })
	_, err := runNote(invokeNotesGet, listed("notes/C"), `{"id":"9"}`)
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("note 404: err = %v", err)
	}
	checkOriginal(t, err, 404, []provider.OriginalEntry{{Message: bodyCanary}})

	serve(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == notesBase+"notes/2" {
			return noteResponse(200, nil, noteJSON(2, "C", "")), nil
		}
		return noteResponse(404, nil, bodyCanary), nil
	})
	_, err = runNote(invokeNotesAttachmentsGet, listed("notes/C"), `{"id":"2","path":".attachments.2/a.png"}`)
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("attachment 404: err = %v", err)
	}
	checkOriginal(t, err, 404, []provider.OriginalEntry{{Message: bodyCanary}})

	dir := t.TempDir()
	args := `{"id":"2","path":".attachments.2/a.png","local_path":` + strconv.Quote(filepath.Join(dir, "o.png")) + `}`
	resolved := localConnection("", dir)
	resolved.Targets = []string{"notes/C"}
	_, err = runNote(invokeNotesAttachmentsGet, resolved, args)
	checkOriginal(t, err, 404, []provider.OriginalEntry{{Message: bodyCanary}})
}

func TestNoteWriteErrorsCarryTheOriginal(t *testing.T) {
	cases := []struct {
		name, op, args string
		status         int
	}{
		{"create 403", "create", `{"title":"T","category":"C"}`, 403},
		{"create 507", "create", `{"title":"T","category":"C"}`, 507},
		{"create 500", "create", `{"title":"T","category":"C"}`, 500},
		{"update 404", "update", `{"id":"1","etag":"e1","category":"C"}`, 404},
		{"update 412", "update", `{"id":"1","etag":"e1","category":"C"}`, 412},
		{"update 400", "update", `{"id":"1","etag":"e1","category":"C"}`, 400},
		{"update 423", "update", `{"id":"1","etag":"e1","category":"C"}`, 423},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serveNotes(t, map[int]string{1: "C"}, false, func() (*http.Response, error) {
				return noteResponse(tc.status, nil, bodyCanary), nil
			})
			_, err := noteWrite(t, noteWriteTools, "nextcloud.notes.notes."+tc.op, tc.args, true)
			checkOriginal(t, err, tc.status, []provider.OriginalEntry{{Message: bodyCanary}})
		})
	}
}

func TestNoteAttachmentUploadErrorsCarryTheOriginal(t *testing.T) {
	for _, status := range []int{400, 403, 404, 500, 507} {
		dir, path := localFile(t, "a.png", "hello")
		serveNotes(t, map[int]string{7: "C"}, false, func() (*http.Response, error) {
			return noteResponse(status, nil, bodyCanary), nil
		})
		_, err := noteUpload(t, uploadConnection(dir, "notes/C"), "7", path)
		checkOriginal(t, err, status, []provider.OriginalEntry{{Message: bodyCanary}})
	}
}
