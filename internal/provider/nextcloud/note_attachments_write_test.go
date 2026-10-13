package nextcloud

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func noteUpload(t *testing.T, resolved *config.Resolved, id, path string) (any, error) {
	t.Helper()
	return runAttachmentWrite(invokeNoteAttachmentsUpload, resolved,
		`{"id":`+strconv.Quote(id)+`,"local_path":`+strconv.Quote(path)+`}`)
}

func postCount(calls *[]call) int {
	n := 0
	for _, c := range *calls {
		if c.method == http.MethodPost {
			n++
		}
	}
	return n
}

func TestNoteAttachmentsUploadSendsOneMultipartPost(t *testing.T) {
	dir, path := localFile(t, "image ä.png", "hello")
	var contentType string
	var length int64
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return noteResponse(200, nil, noteJSON(7, "C/sub", "")), nil
		}
		contentType, length = r.Header.Get("Content-Type"), r.ContentLength
		return noteResponse(200, nil, `{"filename":".attachments.7/image ä.png"}`), nil
	})
	result, err := noteUpload(t, uploadConnection(dir, "notes/C"), "7", path)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*NoteAttachmentUpload)
	if !got.Uploaded || got.ID != "7" || got.Path != ".attachments.7/image ä.png" || got.Name != "image ä.png" ||
		got.Size != 5 || got.SHA256 != helloSHA || got.Truncated {
		t.Errorf("result = %+v", got)
	}
	if eventMethods(calls) != "GET POST" {
		t.Fatalf("calls = %+v", *calls)
	}
	post := (*calls)[1]
	if post.url.Path != notesBase+"attachment/7" || int64(len(post.body)) != length {
		t.Errorf("post = %s, length %d of %d", post.url.Path, length, len(post.body))
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(strings.NewReader(post.body), params["boundary"])
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() != "image ä.png" {
		t.Fatalf("part = %+v, err = %v", part, err)
	}
	if data, _ := io.ReadAll(part); string(data) != "hello" {
		t.Errorf("content = %q", data)
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Errorf("more than one part: %v", err)
	}
}

func TestNoteAttachmentsUploadCutsAnUntrustedPath(t *testing.T) {
	dir, path := localFile(t, "a.png", "hello")
	serveNotes(t, map[int]string{7: "C"}, false, func() (*http.Response, error) {
		return noteResponse(200, nil, `{"filename":"`+strings.Repeat("x", maxValueLength+10)+`"}`), nil
	})
	result, err := noteUpload(t, uploadConnection(dir, "notes/C"), "7", path)
	got, _ := result.(*NoteAttachmentUpload)
	if err != nil || got == nil || !got.Truncated || len(got.Path) != maxValueLength {
		t.Errorf("result = %+v, err = %v", result, err)
	}
}

func TestNoteAttachmentsUploadRefusesForeignAndReadOnlyNotes(t *testing.T) {
	dir, path := localFile(t, "a.png", "hello")
	f := serveNotes(t, map[int]string{5: "Secret-Category"}, false, func() (*http.Response, error) {
		t.Error("a mutation was sent")
		return nil, errors.New("unreachable")
	})
	for _, id := range []string{"5", "9"} {
		_, err := noteUpload(t, uploadConnection(dir, "notes/C"), id, path)
		if classOf(err) != provider.ClassNotFound || strings.Contains(fmtErr(err), "Secret") {
			t.Errorf("id %s: err = %v", id, err)
		}
	}
	if eventMethods(f.calls) != "GET GET" {
		t.Errorf("calls = %s", eventMethods(f.calls))
	}
	f = serveNotes(t, map[int]string{1: "C"}, true, func() (*http.Response, error) {
		t.Error("a mutation was sent")
		return nil, errors.New("unreachable")
	})
	_, err := noteUpload(t, uploadConnection(dir, "notes/C"), "1", path)
	if classOf(err) != provider.ClassPermission || !strings.Contains(fmtErr(err), "read-only") || eventMethods(f.calls) != "GET" {
		t.Errorf("err = %v, calls = %s", err, eventMethods(f.calls))
	}
}

func TestNoteAttachmentsUploadLocalRefusalsMakeNoRequest(t *testing.T) {
	dir, path := localFile(t, "a.png", "hello")
	_, other := localFile(t, "b.png", "hello")
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s", r.Method)
		return nil, errors.New("unreachable")
	})
	for name, tc := range map[string]struct {
		resolved *config.Resolved
		id, path string
	}{
		"bad id":          {uploadConnection(dir, "notes/C"), "0", path},
		"not released":    {uploadConnection(dir, "notes/C"), "7", other},
		"no read release": {uploadConnection("", "notes/C"), "7", path},
		"no notes":        {uploadConnection(dir, "deck/2"), "7", path},
	} {
		if _, err := noteUpload(t, tc.resolved, tc.id, tc.path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := runAttachmentWrite(invokeNoteAttachmentsUpload, uploadConnection(dir, "notes/C"), `{"id":"7"}`); err == nil {
		t.Error("missing local_path accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %d", len(*calls))
	}
}

func TestNoteAttachmentsUploadOutcomes(t *testing.T) {
	dir, path := localFile(t, "a.png", "hello")
	check := func(answer func() (*http.Response, error), wantHint bool, contains string) {
		t.Helper()
		f := serveNotes(t, map[int]string{1: "C"}, false, answer)
		_, err := noteUpload(t, uploadConnection(dir, "notes/C"), "1", path)
		text := fmtErr(err)
		if err == nil || postCount(f.calls) != 1 || strings.Contains(text, noteHint) != wantHint ||
			!strings.Contains(text, contains) || strings.Contains(text, "canary") || strings.Contains(text, "evil.example") {
			t.Errorf("err = %v, posts = %d", err, postCount(f.calls))
		}
	}
	for _, code := range []int{500, 502, 503, 504} {
		check(func() (*http.Response, error) { return status(code), nil }, true, "")
	}
	check(func() (*http.Response, error) { return nil, timeoutError{} }, true, "")
	check(func() (*http.Response, error) { return nil, errors.New("connection reset by peer") }, true, "")
	check(func() (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(failingBody{})}, nil
	}, true, "")
	check(func() (*http.Response, error) { return noteResponse(200, nil, `not json`), nil }, true, "")
	check(func() (*http.Response, error) { return noteResponse(200, nil, `{"filename":""}`), nil }, true, "")
	check(func() (*http.Response, error) { return status(507), nil }, false, "quota")
	check(func() (*http.Response, error) { return status(403), nil }, false, "")
	check(func() (*http.Response, error) {
		response := status(http.StatusFound)
		response.Header.Set("Location", "https://evil.example.invalid/redirect-canary")
		return response, nil
	}, false, "redirect")
}

func TestNoteAttachmentsUploadNeedsConfirmationAndIsInNoProfile(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s", r.Method)
		return nil, errors.New("unreachable")
	})
	tools := append(append([]string{}, noteWriteTools...), noteAttachmentsUpload.ID)
	if _, err := noteWrite(t, tools, noteAttachmentsUpload.ID, `{"id":"1","local_path":"/tmp/x"}`, false); err == nil || len(*calls) != 0 {
		t.Errorf("unconfirmed: err = %v, calls = %d", err, len(*calls))
	}
	r := noteAttachmentsUpload.Risk
	if r.Effect != capability.EffectCreate || r.Idempotency == "" || r.Confirmation != capability.ConfirmationRequired ||
		!r.OpenWorld || r.DataSensitivity != notesSensitivity || noteAttachmentsUpload.LocalFiles != config.LocalFilesRead {
		t.Errorf("descriptor = %+v", noteAttachmentsUpload)
	}
}
