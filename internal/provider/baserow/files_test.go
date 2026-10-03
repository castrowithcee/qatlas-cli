package baserow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const fileSchemaBody = `[{"id":1,"name":"Name","type":"text","primary":true},` +
	`{"id":2,"name":"Anhang","type":"file"},{"id":3,"name":"Gesperrt","type":"file","read_only":true},` +
	`{"id":4,"name":"Notiz","type":"long_text"}]`

func sumOf(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func storedNameOf(unique string, data []byte, ext string) string {
	return unique + "_" + sumOf(data) + "." + ext
}

func mediaURL(name string) string { return baseURL + "/media/user_files/" + name }

type fileRequest struct {
	method, path, query, auth, contentType string
	length                                 int64
	body                                   []byte
}

type fileFake struct {
	mu           sync.Mutex
	requests     []fileRequest
	cell         string
	content      []byte
	announce     int64
	stored       string
	storedSize   int64
	uploadStatus int
	patchStatus  int
	mediaStatus  int
	mediaURL     string
}

var oldName = storedNameOf("Old0", []byte("old"), "txt")

func newFileFake() *fileFake {
	f := &fileFake{content: []byte("hello"), announce: -2, storedSize: -1, uploadStatus: 200, patchStatus: 200,
		mediaStatus: 200}
	f.stored = storedNameOf("Uniq1", f.content, "txt")
	f.cell = `[{"name":"` + oldName + `","visible_name":"alt.txt","url":"` + mediaURL(oldName) + `","size":3}]`
	return f
}

func (f *fileFake) find(method, prefix string) *fileRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.requests {
		if f.requests[i].method == method && strings.HasPrefix(f.requests[i].path, prefix) {
			return &f.requests[i]
		}
	}
	return nil
}

func (f *fileFake) count(method, prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.method == method && strings.HasPrefix(r.path, prefix) {
			n++
		}
	}
	return n
}

func (f *fileFake) handler(r *http.Request) (*http.Response, error) {
	var data []byte
	if r.Body != nil {
		data, _ = io.ReadAll(r.Body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, fileRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"),
		r.Header.Get("Content-Type"), r.ContentLength, data})
	f.mu.Unlock()
	size := f.storedSize
	if size < 0 {
		size = int64(len(f.content))
	}
	stored := `{"name":"` + f.stored + `","original_name":"from-url.txt","size":` + strconv.FormatInt(size, 10) + `}`
	switch {
	case r.URL.Path == "/api/database/fields/table/11/":
		return jsonResponse(200, fileSchemaBody), nil
	case r.Method == http.MethodGet && r.URL.Path == "/api/database/rows/table/11/7/":
		return jsonResponse(200, `{"id":7,"order":"1","Name":"x","Anhang":`+f.cell+`,"Gesperrt":[]}`), nil
	case r.URL.Path == uploadFilePath || r.URL.Path == uploadURLPath:
		if f.uploadStatus != 200 {
			return jsonResponse(f.uploadStatus, `{"error":"`+bodyCanary+`"}`), nil
		}
		return jsonResponse(200, stored), nil
	case r.Method == http.MethodPatch && r.URL.Path == "/api/database/rows/table/11/7/":
		return jsonResponse(f.patchStatus, `{"id":7,"error":"`+bodyCanary+`"}`), nil
	case strings.HasPrefix(r.URL.Path, "/media/user_files/"):
		response := &http.Response{StatusCode: f.mediaStatus, Header: http.Header{},
			Body: io.NopCloser(bytes.NewReader(f.content)), ContentLength: int64(len(f.content))}
		if f.announce != -2 {
			response.ContentLength = f.announce
		}
		return response, nil
	}
	return jsonResponse(404, `{}`), nil
}

func serveFiles(t *testing.T, f *fileFake) {
	t.Helper()
	serve(t, &[]call{}, f.handler)
}

func filesConnection(read, write string) *config.Resolved {
	resolved := &config.Resolved{Name: "x", Provider: Provider, BaseURL: baseURL, Target: "table/11", Credential: "token",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleDatabaseToken: tokenEnv}}}
	if read != "" {
		resolved.Files.Read = []string{read}
	}
	if write != "" {
		resolved.Files.Write = []string{write}
	}
	return resolved
}

func uploadWith(t *testing.T, resolved *config.Resolved, arguments string) (*FileResult, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := invokeFilesUpload(t.Context(), resolved, resolver(red, nil), red,
		json.RawMessage(`{"table_id":11,"row_id":7,"field":"Anhang",`+arguments+`}`))
	if err != nil {
		return nil, err
	}
	return result.(*FileResult), nil
}

func getWith(t *testing.T, resolved *config.Resolved, extra string) (*FileResult, error) {
	t.Helper()
	return getCtx(t, t.Context(), resolved, extra)
}

func getCtx(t *testing.T, ctx context.Context, resolved *config.Resolved, extra string) (*FileResult, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := invokeFilesGet(ctx, resolved, resolver(red, nil), red,
		json.RawMessage(`{"table_id":11,"row_id":7,"field":"Anhang"`+extra+`}`))
	if err != nil {
		return nil, err
	}
	return result.(*FileResult), nil
}

func multipartFile(t *testing.T, r *fileRequest) (string, []byte) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q", r.contentType)
	}
	form := multipart.NewReader(bytes.NewReader(r.body), params["boundary"])
	part, err := form.NextPart()
	if err != nil || part.FormName() != "file" {
		t.Fatalf("part = %v, %v", part, err)
	}
	data, _ := io.ReadAll(part)
	if _, err := form.NextPart(); err != io.EOF {
		t.Fatalf("the upload carries more than the file: %v", err)
	}
	return part.FileName(), data
}

func TestFileDescriptorsCarryFullRiskAndLocalFiles(t *testing.T) {
	up, get := filesUpload, filesGet
	if up.Risk.Effect != capability.EffectUpdate || up.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		up.Risk.Confirmation != capability.ConfirmationRequired || !up.Risk.OpenWorld || up.Risk.DataSensitivity == "" ||
		up.LocalFiles != config.LocalFilesRead || up.RequiresToolAllowList {
		t.Errorf("upload = %+v", up)
	}
	if get.Risk.Effect != capability.EffectRead || get.Risk.Confirmation != capability.ConfirmationNone ||
		!get.Risk.OpenWorld || get.Risk.DataSensitivity == "" || get.LocalFiles != config.LocalFilesWrite {
		t.Errorf("get = %+v", get)
	}
}

func TestUploadFromAReleasedLocalFileSendsOneUploadAndOneRowChange(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("local file content")
	fake.stored = storedNameOf("Uniq2", fake.content, "txt")
	serveFiles(t, fake)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "offer.txt"), fake.content, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := uploadWith(t, filesConnection(dir, ""), `"local_path":`+strconv.Quote(filepath.Join(dir, "offer.txt")))
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "offer.txt" || result.Size != int64(len(fake.content)) || result.SHA256 != sumOf(fake.content) ||
		result.ID != fake.stored || result.Content != "" {
		t.Errorf("result = %+v", result)
	}
	if fake.count(http.MethodPost, "") != 1 || fake.count(http.MethodPatch, "") != 1 {
		t.Fatalf("requests = %+v, want one upload and one row change", fake.requests)
	}
	upload := fake.find(http.MethodPost, uploadFilePath)
	name, content := multipartFile(t, upload)
	if name != "offer.txt" || !bytes.Equal(content, fake.content) || upload.auth != "Token "+tokenValue ||
		upload.length != int64(len(upload.body)) {
		t.Errorf("upload = %q %q auth %q length %d/%d", name, content, upload.auth, upload.length, len(upload.body))
	}
	var body map[string][]map[string]string
	patch := fake.find(http.MethodPatch, "/api/database/rows/")
	if err := json.Unmarshal(patch.body, &body); err != nil || len(body) != 1 || len(body["Anhang"]) != 2 ||
		body["Anhang"][0]["name"] != oldName || body["Anhang"][0]["visible_name"] != "alt.txt" ||
		body["Anhang"][1]["name"] != fake.stored || body["Anhang"][1]["visible_name"] != "offer.txt" ||
		!strings.Contains(patch.query, "user_field_names=true") {
		t.Errorf("row change = %s (%v)", patch.body, err)
	}
}

func TestUploadInlineNeedsANameAndStaysBelow4MiB(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("hi")
	fake.stored = storedNameOf("Uniq3", fake.content, "txt")
	serveFiles(t, fake)
	call := func(extra string) (*FileResult, error) { return uploadWith(t, filesConnection(t.TempDir(), ""), extra) }
	for name, extra := range map[string]string{
		"no name":     `"content_base64":"aGk="`,
		"bad base64":  `"content_base64":"!!","name":"a.txt"`,
		"path name":   `"content_base64":"aGk=","name":"../a.txt"`,
		"no source":   `"name":"a.txt"`,
		"two sources": `"content_base64":"aGk=","local_path":"/x","name":"a.txt"`,
		"url+inline":  `"content_base64":"aGk=","url":"https://files.example.org/a.txt"`,
		"too large": `"content_base64":"` + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), maxInlineFileBytes+1)) +
			`","name":"a.txt"`,
	} {
		if _, err := call(extra); err == nil {
			t.Errorf("%s: an invalid upload succeeded", name)
		}
	}
	if len(fake.requests) != 0 {
		t.Fatalf("a refused upload reached the provider: %+v", fake.requests)
	}
	result, err := call(`"content_base64":"aGk=","name":"hi.txt"`)
	if err != nil {
		t.Fatal(err)
	}
	if result.SHA256 != sumOf([]byte("hi")) || result.Size != 2 || result.Name != "hi.txt" {
		t.Errorf("result = %+v", result)
	}
	if _, content := multipartFile(t, fake.find(http.MethodPost, uploadFilePath)); string(content) != "hi" {
		t.Errorf("uploaded = %q", content)
	}
}

func TestUploadRefusesPathsOutsideTheReleaseBeforeAnyProviderIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s", r.URL.Path)
		return nil, errors.New("no request expected")
	})
	released, other := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"outside": filepath.Join(other, "secret.txt"), "dotdot": released + "/../x"} {
		red := &redact.Redactor{}
		_, err := invokeFilesUpload(t.Context(), filesConnection(released, ""), resolver(red, env.reads), red,
			json.RawMessage(`{"table_id":11,"row_id":7,"field":"Anhang","local_path":`+strconv.Quote(path)+`}`))
		var pathErr *localfile.PathError
		if !errors.As(err, &pathErr) || strings.Contains(err.Error(), other) || strings.Contains(err.Error(), released) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := uploadWith(t, filesConnection("", ""), `"local_path":`+strconv.Quote(filepath.Join(other, "secret.txt"))); err == nil {
		t.Error("an upload without a released directory succeeded")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d", len(calls), *env.reads)
	}
}

func TestUploadViaURLSendsTheURLToBaserowOnly(t *testing.T) {
	fake := newFileFake()
	serveFiles(t, fake)
	result, err := uploadWith(t, filesConnection("", ""), `"url":"https://files.example.org/docs/a.txt?x=1"`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "from-url.txt" || result.ID != fake.stored || result.SHA256 != sumOf(fake.content) {
		t.Errorf("result = %+v", result)
	}
	via := fake.find(http.MethodPost, uploadURLPath)
	if via == nil || via.auth != "Token "+tokenValue || !strings.Contains(via.contentType, "application/json") ||
		string(via.body) != `{"url":"https://files.example.org/docs/a.txt?x=1"}` {
		t.Fatalf("via url = %+v", via)
	}
	if fake.count(http.MethodPost, uploadFilePath) != 0 || fake.count(http.MethodPatch, "") != 1 {
		t.Errorf("requests = %+v", fake.requests)
	}
	if patch := fake.find(http.MethodPatch, "/api/database/rows/"); !strings.Contains(string(patch.body), `"visible_name":"from-url.txt"`) {
		t.Errorf("row change = %s", patch.body)
	}
	// An explicit name wins.
	fake2 := newFileFake()
	serveFiles(t, fake2)
	if result, err := uploadWith(t, filesConnection("", ""), `"url":"https://files.example.org/a.txt","name":"mine.txt"`); err != nil || result.Name != "mine.txt" {
		t.Errorf("named = %+v, %v", result, err)
	}
}

func TestUploadViaURLPositiveList(t *testing.T) {
	fake := newFileFake()
	serveFiles(t, fake)
	good := []string{"https://files.example.org/a.txt", "https://a.b.example.co.uk/x/y.pdf?q=1", "https://EXAMPLE.org/a",
		"https://xn--bcher-kva.example.org/a", "https://cdn-1.example.org/a"}
	bad := []string{"", "http://files.example.org/a", "ftp://files.example.org/a", "file:///etc/passwd",
		"https://user@files.example.org/a", "https://user:pw@files.example.org/a", "https://files.example.org:8443/a",
		"https://files.example.org:443/a", "https://127.0.0.1/a", "https://10.0.0.1/a", "https://2130706433/a",
		"https://0x7f.0.0.1/a", "https://127.1/a", "https://[::1]/a", "https://[2001:db8::1]/a", "https://localhost/a",
		"https://files.localhost/a", "https://intranet/a", "https://host.internal/a", "https://db.local/a",
		"https://router.lan/a", "https://nas.home.arpa/a", "https://files.example.org./a", "https://files.example.org/a#frag",
		"https://files.example.org/a b", "https://files.exämple.org/a", "https://-x.example.org/a", "https://x..example.org/a",
		"//files.example.org/a", "https:files.example.org/a", "https://files.example.org\\@evil.test/a",
		"https://1.2.3.4.5/a", "https://host.123/a", "https://" + strings.Repeat("a", 64) + ".example.org/a",
		"https://files.example.org/" + strings.Repeat("a", maxFileURLLen)}
	for _, raw := range good {
		if err := checkPublicURL(raw); err != nil {
			t.Errorf("checkPublicURL(%q) = %v", raw, err)
		}
	}
	for _, raw := range bad {
		err := checkPublicURL(raw)
		if err == nil {
			t.Errorf("checkPublicURL(%q) accepted", raw)
		} else if !isInvalidRequest(err) || (raw != "" && len(raw) < 100 && strings.Contains(err.Error(), raw)) {
			t.Errorf("checkPublicURL(%q) = %v", raw, err)
		}
	}
}

func TestUploadViaURLRefusalsReachNeitherSecretNorProvider(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("no request expected")
	})
	for _, raw := range []string{"http://files.example.org/a", "https://127.0.0.1/a", "https://user@files.example.org/a"} {
		red := &redact.Redactor{}
		_, err := invokeFilesUpload(t.Context(), filesConnection("", ""), resolver(red, env.reads), red,
			json.RawMessage(`{"table_id":11,"row_id":7,"field":"Anhang","url":"`+raw+`"}`))
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", raw, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d", len(calls), *env.reads)
	}
}

func TestFileToolsRefuseForeignTablesAndUnsuitableFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s", r.URL.Path)
		return nil, errors.New("no request expected")
	})
	red := &redact.Redactor{}
	for _, table := range []string{"99", "0"} {
		arguments := json.RawMessage(`{"table_id":` + table + `,"row_id":7,"field":"Anhang","content_base64":"aGk=","name":"a"}`)
		if _, err := invokeFilesUpload(t.Context(), filesConnection("", ""), resolver(red, env.reads), red, arguments); !isInvalidRequest(err) {
			t.Errorf("upload table %s: err = %v", table, err)
		}
		arguments = json.RawMessage(`{"table_id":` + table + `,"row_id":7,"field":"Anhang"}`)
		if _, err := invokeFilesGet(t.Context(), filesConnection("", ""), resolver(red, env.reads), red, arguments); !isInvalidRequest(err) {
			t.Errorf("get table %s: err = %v", table, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d", len(calls), *env.reads)
	}

	fake := newFileFake()
	serveFiles(t, fake)
	for name, field := range map[string]string{"text": "Name", "unknown": "Nope", "readonly": "Gesperrt", "notes": "Notiz"} {
		_, err := invokeFilesUpload(t.Context(), filesConnection("", ""), resolver(red, nil), red,
			json.RawMessage(`{"table_id":11,"row_id":7,"field":"`+field+`","content_base64":"aGk=","name":"a.txt"}`))
		if !isInvalidRequest(err) || strings.Contains(err.Error(), field) && name != "text" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if fake.count(http.MethodPost, "") != 0 || fake.count(http.MethodPatch, "") != 0 {
		t.Fatalf("an unsuitable field reached a change: %+v", fake.requests)
	}
	for _, field := range []string{"Name", "Nope"} {
		if _, err := invokeFilesGet(t.Context(), filesConnection("", ""), resolver(red, nil), red,
			json.RawMessage(`{"table_id":11,"row_id":7,"field":"`+field+`"}`)); !isInvalidRequest(err) {
			t.Errorf("get %s: err = %v", field, err)
		}
	}
	if fake.count(http.MethodGet, "/media/") != 0 {
		t.Fatal("a refused download fetched a file")
	}
}

func TestUploadFailuresAreClassifiedUncertainOnesSayAndNothingIsRepeated(t *testing.T) {
	for status, class := range map[int]provider.Class{401: provider.ClassAuth, 403: provider.ClassPermission,
		400: provider.ClassProviderError, 413: provider.ClassProviderError, 500: provider.ClassProviderError, 504: provider.ClassTimeout} {
		fake := newFileFake()
		fake.uploadStatus = status
		serveFiles(t, fake)
		_, err := uploadWith(t, filesConnection("", ""), `"content_base64":"aGVsbG8=","name":"a.txt"`)
		if classOf(err) != class || err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), tokenValue) {
			t.Errorf("upload %d: err = %v", status, err)
			continue
		}
		if uncertain := strings.Contains(err.Error(), "read the row before repeating"); uncertain != (status >= 500) {
			t.Errorf("upload %d: uncertain = %t, err = %v", status, uncertain, err)
		}
		if fake.count(http.MethodPost, "") != 1 || fake.count(http.MethodPatch, "") != 0 {
			t.Errorf("upload %d: requests = %+v", status, fake.requests)
		}
	}
	for status, class := range map[int]provider.Class{403: provider.ClassPermission, 400: provider.ClassProviderError, 500: provider.ClassProviderError} {
		fake := newFileFake()
		fake.patchStatus = status
		serveFiles(t, fake)
		_, err := uploadWith(t, filesConnection("", ""), `"content_base64":"aGVsbG8=","name":"a.txt"`)
		if classOf(err) != class || err == nil || strings.Contains(err.Error(), bodyCanary) ||
			!strings.Contains(err.Error(), "not attached") {
			t.Errorf("attach %d: err = %v", status, err)
			continue
		}
		if status == 403 && !strings.Contains(err.Error(), "update") {
			t.Errorf("attach 403 does not name the right: %v", err)
		}
		if fake.count(http.MethodPost, "") != 1 || fake.count(http.MethodPatch, "") != 1 {
			t.Errorf("attach %d: requests = %+v", status, fake.requests)
		}
	}
}

func TestUploadRefusesAnUnusableOrDifferentStoredFileBeforeAttaching(t *testing.T) {
	for name, edit := range map[string]func(*fileFake){
		"bad name":      func(f *fileFake) { f.stored = "../x" },
		"no pattern":    func(f *fileFake) { f.stored = "plain.txt" },
		"wrong size":    func(f *fileFake) { f.storedSize = 99 },
		"wrong content": func(f *fileFake) { f.stored = storedNameOf("Uniq9", []byte("other"), "txt") },
	} {
		fake := newFileFake()
		fake.content = []byte("hi")
		fake.stored = storedNameOf("Uniq4", fake.content, "txt")
		edit(fake)
		serveFiles(t, fake)
		_, err := uploadWith(t, filesConnection("", ""), `"content_base64":"aGk=","name":"a.txt"`)
		if classOf(err) != provider.ClassInvalidResponse || fake.count(http.MethodPatch, "") != 0 {
			t.Errorf("%s: err = %v, requests = %+v", name, err, fake.requests)
		}
	}
}

func TestDownloadReturnsInlineContentFromTheCellWithoutTheToken(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("old")
	serveFiles(t, fake)
	result, err := getWith(t, filesConnection("", ""), ``)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "alt.txt" || result.ID != oldName || result.Size != 3 || result.SHA256 != sumOf([]byte("old")) ||
		result.Content != base64.StdEncoding.EncodeToString([]byte("old")) {
		t.Errorf("result = %+v", result)
	}
	media := fake.find(http.MethodGet, "/media/")
	if media == nil || media.auth != "" || media.path != "/media/user_files/"+oldName {
		t.Fatalf("media request = %+v", media)
	}
}

func TestDownloadSelectsOnlyFromTheCurrentCell(t *testing.T) {
	fake := newFileFake()
	second := storedNameOf("Two2", []byte("two"), "pdf")
	fake.cell = `[{"name":"` + oldName + `","visible_name":"a.txt","url":"` + mediaURL(oldName) + `","size":3},` +
		`{"name":"` + second + `","visible_name":"b.pdf","url":"` + mediaURL(second) + `","size":3},` +
		`{"name":"` + storedNameOf("Two3", []byte("x"), "pdf") + `","visible_name":"b.pdf","url":"` + mediaURL(storedNameOf("Two3", []byte("x"), "pdf")) + `","size":1}]`
	fake.content = []byte("two")
	serveFiles(t, fake)
	if _, err := getWith(t, filesConnection("", ""), ``); err == nil {
		t.Error("a cell with three files needs a selector")
	}
	if _, err := getWith(t, filesConnection("", ""), `,"name":"b.pdf"`); err == nil {
		t.Error("an ambiguous name succeeded")
	}
	if _, err := getWith(t, filesConnection("", ""), `,"name":"a.txt","index":0`); err == nil {
		t.Error("name and index together succeeded")
	}
	for _, extra := range []string{`,"name":"missing"`, `,"index":3`} {
		if _, err := getWith(t, filesConnection("", ""), extra); err == nil {
			t.Errorf("%s succeeded", extra)
		}
	}
	if fake.count(http.MethodGet, "/media/") != 0 {
		t.Fatal("a refused selection fetched a file")
	}
	for _, extra := range []string{`,"index":1`, `,"name":"` + second + `"`} {
		if result, err := getWith(t, filesConnection("", ""), extra); err != nil || result.ID != second {
			t.Errorf("%s = %+v, %v", extra, result, err)
		}
	}
}

func TestDownloadRefusesForeignAndMalformedAddressesBeforeFetching(t *testing.T) {
	name := oldName
	for label, address := range map[string]string{
		"foreign host": "https://cdn.example.org/media/user_files/" + name,
		"http":         "http://baserow.example.test/media/user_files/" + name,
		"userinfo":     "https://user@baserow.example.test/media/user_files/" + name,
		"other path":   baseURL + "/media/other/" + name,
		"other file":   mediaURL(storedNameOf("Else", []byte("z"), "txt")),
		"traversal":    baseURL + "/media/user_files/../" + name,
		"subdomain":    "https://media.baserow.example.test/media/user_files/" + name,
		"port":         "https://baserow.example.test:8443/media/user_files/" + name,
		"empty":        "",
		"fragment":     mediaURL(name) + "#x",
	} {
		fake := newFileFake()
		fake.cell = `[{"name":"` + name + `","visible_name":"a.txt","url":"` + address + `","size":3}]`
		serveFiles(t, fake)
		_, err := getWith(t, filesConnection("", ""), ``)
		if classOf(err) != provider.ClassInvalidResponse || fake.count(http.MethodGet, "/media/") != 0 ||
			(address != "" && strings.Contains(err.Error(), address)) {
			t.Errorf("%s: err = %v, requests = %+v", label, err, fake.requests)
		}
	}
	// A query, as a signed address carries it, is kept and never reported.
	fake := newFileFake()
	fake.content = []byte("old")
	fake.cell = `[{"name":"` + name + `","visible_name":"a.txt","url":"` + mediaURL(name) + `?md=sig-canary-1","size":3}]`
	serveFiles(t, fake)
	if _, err := getWith(t, filesConnection("", ""), ``); err != nil {
		t.Fatal(err)
	}
	if media := fake.find(http.MethodGet, "/media/"); media == nil || media.query != "md=sig-canary-1" {
		t.Errorf("media request = %+v", media)
	}
}

func TestDownloadFollowsNoRedirectAndClassifiesFailures(t *testing.T) {
	for status, class := range map[int]provider.Class{302: provider.ClassProviderError, 403: provider.ClassPermission,
		404: provider.ClassNotFound, 429: provider.ClassRateLimited, 500: provider.ClassProviderError} {
		fake := newFileFake()
		fake.mediaStatus = status
		serveFiles(t, fake)
		_, err := getWith(t, filesConnection("", ""), `,"index":0`)
		if classOf(err) != class || fake.count(http.MethodGet, "/media/") != 1 || strings.Contains(err.Error(), tokenValue) {
			t.Errorf("media %d: err = %v, requests = %d", status, err, fake.count(http.MethodGet, "/media/"))
		}
	}
}

func TestDownloadChecksTheHashOfTheStoredName(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("not what the name says")
	serveFiles(t, fake)
	if _, err := getWith(t, filesConnection("", ""), ``); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("inline: err = %v", err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	_, err := getWith(t, filesConnection("", dir), `,"local_path":`+strconv.Quote(target))
	var integrity *localfile.IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("path: err = %v, want an integrity error", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("directory holds %d entries after a failed download", len(entries))
	}
}

func TestDownloadRefusesInlineContentAbove4MiB(t *testing.T) {
	fake := newFileFake()
	fake.content = bytes.Repeat([]byte("a"), maxInlineFileBytes+1)
	serveFiles(t, fake)
	if _, err := getWith(t, filesConnection("", ""), ``); err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Errorf("err = %v", err)
	}
	fake.announce = -1
	if _, err := getWith(t, filesConnection("", ""), ``); err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Errorf("unannounced length: err = %v", err)
	}
}

func TestDownloadToAReleasedPathWritesOnlyMetadataAndNeedsConfirmationToReplace(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("old")
	serveFiles(t, fake)
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	extra := `,"local_path":` + strconv.Quote(target)

	result, err := getWith(t, filesConnection("", dir), extra)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "" || result.Name != "alt.txt" || result.Size != 3 || result.SHA256 != sumOf(fake.content) {
		t.Errorf("result = %+v, want metadata only", result)
	}
	if data, _ := os.ReadFile(target); string(data) != "old" {
		t.Errorf("file = %q", data)
	}
	if _, err := getWith(t, filesConnection("", dir), extra); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Fatalf("unconfirmed overwrite: %v", err)
	}
	if _, err := getCtx(t, capability.WithConfirmed(t.Context()), filesConnection("", dir), extra); err != nil {
		t.Fatal(err)
	}

	// A length that disagrees with the announced one is discarded and leaves the file as it was.
	fake.announce = 1
	_, err = getCtx(t, capability.WithConfirmed(t.Context()), filesConnection("", dir), extra)
	var integrity *localfile.IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("err = %v, want an integrity error", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "old" {
		t.Errorf("file = %q, want it unchanged", data)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("directory holds %d entries, want no temporary file", len(entries))
	}
}

func TestDownloadRefusesPathsOutsideTheReleaseBeforeAnyProviderIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("no request expected")
	})
	released, other := t.TempDir(), t.TempDir()
	red := &redact.Redactor{}
	_, err := invokeFilesGet(t.Context(), filesConnection("", released), resolver(red, env.reads), red,
		json.RawMessage(`{"table_id":11,"row_id":7,"field":"Anhang","local_path":`+strconv.Quote(filepath.Join(other, "a.txt"))+`}`))
	var pathErr *localfile.PathError
	if !errors.As(err, &pathErr) || strings.Contains(err.Error(), other) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %d, reads = %d", err, len(calls), *env.reads)
	}
}

func TestFileToolsThroughTheCoreNeedReleasedDirectoriesAndConfirmation(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("hi")
	fake.stored = storedNameOf("Uniq5", fake.content, "txt")
	serveFiles(t, fake)
	dir := t.TempDir()
	cfg := testConfig()
	allRights := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}
	cfg.Connections["files"] = config.Connection{Service: "baserow", Credential: "token", Permissions: allRights,
		Target: "table/11", Files: config.Files{Read: []string{dir}, Write: []string{dir}}}
	cfg.Connections["nofiles"] = config.Connection{Service: "baserow", Credential: "token", Permissions: allRights,
		Target: "table/11"}
	cfg.Connections["readonly"] = config.Connection{Service: "baserow", Credential: "token",
		Permissions: []config.Permission{config.PermissionRead}, Target: "table/11",
		Files: config.Files{Read: []string{dir}, Write: []string{dir}}}
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red, nil), red)
	upload := `{"table_id":11,"row_id":7,"field":"Anhang","name":"hi.txt","content_base64":"aGk="}`
	invoke := func(operation, connection, arguments string, confirmed bool) (application.InvokeResponse, error) {
		return core.Invoke(t.Context(), application.InvokeRequest{Operation: operation, Connection: connection,
			Arguments: json.RawMessage(arguments), Confirmed: confirmed})
	}

	if _, err := invoke(filesUpload.ID, "files", upload, false); application.ErrorCode(err) != "confirmation-required" {
		t.Fatalf("unconfirmed upload: %v", err)
	}
	if len(fake.requests) != 0 {
		t.Fatal("an unconfirmed upload reached the provider")
	}
	response, err := invoke(filesUpload.ID, "files", upload, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Result), sumOf([]byte("hi"))) || strings.Contains(string(response.Result), bodyCanary) {
		t.Errorf("result = %s", response.Result)
	}
	if _, err := invoke(filesUpload.ID, "nofiles", upload, true); err == nil {
		t.Error("a connection without released directories offered the upload")
	}
	if _, err := invoke(filesUpload.ID, "readonly", upload, true); err == nil {
		t.Error("a connection without the update right offered the upload")
	}
	fake.content = []byte("old")
	if _, err := invoke(filesGet.ID, "readonly", `{"table_id":11,"row_id":7,"field":"Anhang","index":0}`, false); err != nil {
		t.Errorf("a read connection could not download: %v", err)
	}
	if _, err := invoke(filesGet.ID, "nofiles", `{"table_id":11,"row_id":7,"field":"Anhang","index":0}`, false); err == nil {
		t.Error("a connection without released directories offered the download")
	}
}
