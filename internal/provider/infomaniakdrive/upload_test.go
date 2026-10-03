package infomaniakdrive

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	uploadedID   int64 = 600
	sessionToken       = "0f3c9a52-7d4e-4b1a-9c11-5a2b7e8d9f01"
	knownEtag          = "0123456789abcdef0123"
	tokenArg           = "replay-token-0123456789"
)

var uploadDrivePath = fmt.Sprintf("/3/drive/%d/upload", ownDrive)
var sessionBase = fmt.Sprintf("/3/drive/%d/upload/session", ownDrive)
var chunkPath = sessionBase + "/" + sessionToken + "/chunk"

func uploadedFile(id, parent int64, name string, size int) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"type":"file","parent_id":%d,"size":%d,"status":"ok","last_modified_at":1735776000}`,
		id, name, parent, size)
}

// uploadEnvironment is the core with connections that release dir for reading, next to the plain ones.
func uploadEnvironment(t *testing.T, dir string, calls *[]call, handler func(*http.Request) (*http.Response, error),
	foreign func(*http.Request) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler, foreign)
	cfg := coreConfig()
	for _, name := range []string{"account", "drive", "driveforeign"} {
		connection := cfg.Connections[name]
		connection.Files = config.Files{Read: []string{dir}}
		cfg.Connections["files-"+name] = connection
	}
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func directFake(t *testing.T, status int, body string) func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != uploadDrivePath {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(status, body), nil
	})
}

func inline(content []byte) string { return base64.StdEncoding.EncodeToString(content) }

func decodeUpload(t *testing.T, result string, err error) UploadResult {
	t.Helper()
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var out UploadResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result = %s, %v", result, err)
	}
	return out
}

func TestUploadDescriptorCarriesFullRiskAndLocalFiles(t *testing.T) {
	d := filesUpload
	if d.Risk.Effect != capability.EffectUpdate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" ||
		d.LocalFiles != config.LocalFilesRead {
		t.Fatalf("descriptor = %+v", d)
	}
}

func TestUploadInlineSendsOneDirectRequestToANewFile(t *testing.T) {
	var calls []call
	content := []byte("inline content")
	env := uploadEnvironment(t, t.TempDir(), &calls, directFake(t, 200, envelopeSuccess(uploadedFile(uploadedID, rootID, "a b.txt", len(content)))), nil)
	args := fmt.Sprintf(`{"drive_id":%d,"directory_id":%d,"name":"a b.txt","content_base64":%q}`, ownDrive, rootID, inline(content))
	out := okUpload(t, env, "files-drive", args)
	if len(calls) != 2 || calls[0].path != ownershipPath(ownDrive) {
		t.Fatalf("calls = %+v, want the ownership check and one upload", calls)
	}
	got := calls[1]
	if got.method != http.MethodPost || got.path != uploadDrivePath || got.body != string(content) ||
		got.contentType != "application/octet-stream" || got.host != apiHost {
		t.Fatalf("upload call = %+v", got)
	}
	q := got.query
	if q.Get("total_size") != strconv.Itoa(len(content)) || q.Get("directory_id") != "1" || q.Get("file_name") != "a b.txt" ||
		q.Get("conflict") != "error" || q.Get("file_id") != "" || len(q.Get("client_token")) < 16 {
		t.Fatalf("query = %v", q)
	}
	if out.Status != statusDone || out.Method != methodDirect || out.FileID != uploadedID || out.ClientToken != q.Get("client_token") ||
		out.SHA256 == "" || out.Size != int64(len(content)) || out.DriveID != ownDrive {
		t.Fatalf("result = %+v", out)
	}
}

func okUpload(t *testing.T, env *environment, connection, args string) UploadResult {
	t.Helper()
	result, err := mustInvoke(t, env, connection, args)
	return decodeUpload(t, result, err)
}

func mustInvoke(t *testing.T, env *environment, connection, args string) (string, error) {
	t.Helper()
	return env.invokeConfirmed(filesUpload.ID, connection, args)
}

func TestUploadFromReleasedLocalFileReturnsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	content := []byte("local file content")
	path := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []call
	env := uploadEnvironment(t, dir, &calls, directFake(t, 200, envelopeSuccess(uploadedFile(uploadedID, 77, "report.pdf", len(content)))), nil)
	args := fmt.Sprintf(`{"drive_id":%d,"directory_id":77,"conflict":"rename","local_path":%s}`, ownDrive, strconv.Quote(path))
	result, err := mustInvoke(t, env, "files-drive", args)
	out := decodeUpload(t, result, err)
	if calls[1].query.Get("file_name") != "report.pdf" || calls[1].query.Get("conflict") != "rename" || calls[1].body != string(content) {
		t.Fatalf("upload call = %+v", calls[1])
	}
	if digest := sha256.Sum256(content); out.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("result = %s", result)
	}
	if strings.Contains(result, string(content)) || strings.Contains(result, dir) || strings.Contains(result, "content") {
		t.Fatalf("result %s carries content or the path", result)
	}
}

func TestUploadReplaceSendsIfMatchAndNoPlacement(t *testing.T) {
	var calls []call
	content := []byte("new version")
	env := uploadEnvironment(t, t.TempDir(), &calls, directFake(t, 200, envelopeSuccess(uploadedFile(childFileID, rootID, "x.txt", len(content)))), nil)
	args := fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"etag":%q,"content_base64":%q}`, ownDrive, childFileID, knownEtag, inline(content))
	out := okUpload(t, env, "files-drive", args)
	var upload *call
	for i := range calls {
		if calls[i].path == uploadDrivePath {
			upload = &calls[i]
		}
	}
	if upload == nil || upload.query.Get("file_id") != strconv.FormatInt(childFileID, 10) ||
		upload.query.Get("file_name") != "" || upload.query.Get("directory_id") != "" || upload.query.Get("conflict") != "" {
		t.Fatalf("calls = %+v", calls)
	}
	if out.FileID != childFileID || out.Status != statusDone {
		t.Fatalf("result = %+v", out)
	}
}

// ifMatch is not recorded by serve, so a dedicated fake reads the header.
func TestUploadReplaceCarriesTheEtagAsIfMatch(t *testing.T) {
	var calls []call
	var seen string
	env := uploadEnvironment(t, t.TempDir(), &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Get("If-Match")
		return jsonResponse(200, envelopeSuccess(uploadedFile(childFileID, rootID, "x.txt", 1))), nil
	}), nil)
	args := fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"etag":%q,"content_base64":"eA=="}`, ownDrive, childFileID, knownEtag)
	if _, err := mustInvoke(t, env, "files-drive", args); err != nil || seen != knownEtag {
		t.Fatalf("err = %v, If-Match = %q", err, seen)
	}
}

func TestUploadEtagConflictIsADistinctErrorAndNotRepeated(t *testing.T) {
	for _, status := range []int{412, 409} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls []call
			env := uploadEnvironment(t, t.TempDir(), &calls, directFake(t, status, `{"result":"error","error":{"description":"`+foreignCanary+`"}}`), nil)
			args := fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"etag":%q,"content_base64":"eA=="}`, ownDrive, childFileID, knownEtag)
			_, err := mustInvoke(t, env, "files-drive", args)
			if err == nil || !strings.Contains(err.Error(), "etag conflict") || strings.Contains(err.Error(), foreignCanary) ||
				strings.Contains(err.Error(), "may have been") {
				t.Fatalf("err = %v, want a clear etag conflict", err)
			}
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want the check and one upload", len(calls))
			}
		})
	}
}

func TestUploadValidatesArgumentsBeforeAnySecretOrRequest(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := fmt.Sprintf(`"drive_id":%d`, ownDrive)
	c := `"content_base64":"eA=="`
	lp := `"local_path":` + strconv.Quote(file)
	over := base64.StdEncoding.EncodeToString(make([]byte, maxInlineFileBytes+1))
	cases := map[string]string{
		"no target":            `{` + d + `,"name":"a",` + c + `}`,
		"both targets":         `{` + d + `,"directory_id":1,"file_id":5,"etag":"` + knownEtag + `",` + c + `}`,
		"replace without etag": `{` + d + `,"file_id":5,` + c + `}`,
		"bad etag":             `{` + d + `,"file_id":5,"etag":"zz",` + c + `}`,
		"etag without file":    `{` + d + `,"directory_id":1,"name":"a","etag":"` + knownEtag + `",` + c + `}`,
		"replace root":         `{` + d + `,"file_id":1,"etag":"` + knownEtag + `",` + c + `}`,
		"replace with name":    `{` + d + `,"file_id":5,"etag":"` + knownEtag + `","name":"a",` + c + `}`,
		"replace conflict":     `{` + d + `,"file_id":5,"etag":"` + knownEtag + `","conflict":"rename",` + c + `}`,
		"zero directory":       `{` + d + `,"directory_id":0,"name":"a",` + c + `}`,
		"negative directory":   `{` + d + `,"directory_id":-4,"name":"a",` + c + `}`,
		"huge directory":       `{` + d + `,"directory_id":9999999999999999999,"name":"a",` + c + `}`,
		"string directory":     `{` + d + `,"directory_id":"1/../2","name":"a",` + c + `}`,
		"conflict version":     `{` + d + `,"directory_id":1,"name":"a","conflict":"version",` + c + `}`,
		"missing name":         `{` + d + `,"directory_id":1,` + c + `}`,
		"slash name":           `{` + d + `,"directory_id":1,"name":"a/b",` + c + `}`,
		"both sources":         `{` + d + `,"directory_id":1,"name":"a",` + c + `,` + lp + `}`,
		"no source":            `{` + d + `,"directory_id":1,"name":"a"}`,
		"name with path":       `{` + d + `,"directory_id":1,"name":"a",` + lp + `}`,
		"bad base64":           `{` + d + `,"directory_id":1,"name":"a","content_base64":"***"}`,
		"inline over 4 MiB":    `{` + d + `,"directory_id":1,"name":"a","content_base64":"` + over + `"}`,
		"short client token":   `{` + d + `,"directory_id":1,"name":"a","client_token":"abc",` + c + `}`,
		"free url":             `{` + d + `,"directory_id":1,"name":"a","url":"https://x.invalid",` + c + `}`,
		"free path":            `{` + d + `,"directory_id":1,"name":"a","directory_path":"/x",` + c + `}`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := uploadEnvironment(t, dir, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			if _, err := mustInvoke(t, env, "files-drive", args); err == nil {
				t.Fatal("a malformed upload was accepted")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestUploadInlineAcceptsExactlyFourMiB(t *testing.T) {
	var calls []call
	content := make([]byte, maxInlineFileBytes)
	env := uploadEnvironment(t, t.TempDir(), &calls, directFake(t, 200, envelopeSuccess(uploadedFile(uploadedID, rootID, "big.bin", len(content)))), nil)
	args := fmt.Sprintf(`{"drive_id":%d,"directory_id":1,"name":"big.bin","content_base64":%q}`, ownDrive, inline(content))
	out := okUpload(t, env, "files-drive", args)
	if out.Method != methodDirect || len(calls[1].body) != len(content) {
		t.Fatalf("result = %+v", out)
	}
}

func TestUploadRefusesForeignDrivesAndRequiresConfirmationAndPermission(t *testing.T) {
	var calls []call
	env := uploadEnvironment(t, t.TempDir(), &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}, nil)
	args := fmt.Sprintf(`{"drive_id":%d,"directory_id":1,"name":"a","content_base64":"eA=="}`, foreignDrive)
	_, err := mustInvoke(t, env, "files-drive", args)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), fmt.Sprint(foreignDrive)) {
		t.Fatalf("err = %v, want a refusal without the foreign value", err)
	}
	own := fmt.Sprintf(`{"drive_id":%d,"directory_id":1,"name":"a","content_base64":"eA=="}`, ownDrive)
	var needed *application.ConfirmationRequiredError
	if _, err := env.invoke(filesUpload.ID, "files-drive", own); !errors.As(err, &needed) {
		t.Fatalf("err = %v, want confirmation required", err)
	}
	if _, err := mustInvoke(t, env, "readonly", own); err == nil {
		t.Fatal("a read-only connection ran an upload")
	}
	if _, err := mustInvoke(t, env, "drive", own); err == nil {
		t.Fatal("a connection without released directories offered the upload")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
	}

	// A drive of the allow-list that belongs to another account stops at the ownership check.
	calls = nil
	env = uploadEnvironment(t, t.TempDir(), &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("upload request %s reached a drive of another account", r.URL.Path)
		return nil, nil
	}), nil)
	_, err = mustInvoke(t, env, "files-driveforeign", args)
	if !isInvalidRequest(err) || len(calls) != 1 || calls[0].method != http.MethodGet {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestUploadRefusesAPathOutsideFilesReadBeforeProviderIO(t *testing.T) {
	released, other := t.TempDir(), t.TempDir()
	secretFile := filepath.Join(other, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"outside": secretFile, "dotdot": released + "/../x", "missing": filepath.Join(released, "none")} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := uploadEnvironment(t, released, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			args := fmt.Sprintf(`{"drive_id":%d,"directory_id":1,"local_path":%s}`, ownDrive, strconv.Quote(path))
			_, err := mustInvoke(t, env, "files-drive", args)
			if err == nil || strings.Contains(err.Error(), other) || strings.Contains(err.Error(), released) {
				t.Fatalf("err = %v", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestUploadUnclearOutcomesReportTheTokenAndAreNotRepeated(t *testing.T) {
	failures := map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"500":     func(*http.Request) (*http.Response, error) { return jsonResponse(500, `{"result":"error"}`), nil },
		"garbage": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"error result": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"result":"error"}`), nil
		},
		"no file": func(*http.Request) (*http.Response, error) { return jsonResponse(200, envelopeSuccess(`{}`)), nil },
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := uploadEnvironment(t, t.TempDir(), &calls, withOwnership(ownDrive, ownAccount, failure), nil)
			args := fmt.Sprintf(`{"drive_id":%d,"directory_id":1,"name":"a","client_token":%q,"content_base64":"eA=="}`, ownDrive, tokenArg)
			_, err := mustInvoke(t, env, "files-drive", args)
			if err == nil || !strings.Contains(err.Error(), tokenArg) || !strings.Contains(err.Error(), "may have been uploaded") {
				t.Fatalf("err = %v, want the uncertainty with the client token", err)
			}
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want the check and exactly one upload", len(calls))
			}
		})
	}
}

func TestUploadReplayWithTheSameClientTokenSendsTheSameToken(t *testing.T) {
	var calls []call
	env := uploadEnvironment(t, t.TempDir(), &calls, directFake(t, 200, envelopeSuccess(uploadedFile(uploadedID, rootID, "a", 1))), nil)
	args := fmt.Sprintf(`{"drive_id":%d,"directory_id":1,"name":"a","client_token":%q,"content_base64":"eA=="}`, ownDrive, tokenArg)
	first := okUpload(t, env, "files-drive", args)
	second := okUpload(t, env, "files-drive", args)
	var tokens []string
	for _, c := range calls {
		if c.path == uploadDrivePath {
			tokens = append(tokens, c.query.Get("client_token"))
		}
	}
	if len(tokens) != 2 || tokens[0] != tokenArg || tokens[1] != tokenArg || first != second || first.ClientToken != tokenArg {
		t.Fatalf("tokens = %v, first = %+v, second = %+v", tokens, first, second)
	}
}

func TestUploadPlanRejectionIsAPermissionError(t *testing.T) {
	var calls []call
	env := uploadEnvironment(t, t.TempDir(), &calls, directFake(t, 403, `{"result":"error","error":{"description":"`+foreignCanary+`"}}`), nil)
	args := fmt.Sprintf(`{"drive_id":%d,"directory_id":1,"name":"a","content_base64":"eA=="}`, ownDrive)
	_, err := mustInvoke(t, env, "files-drive", args)
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), foreignCanary) {
		t.Fatalf("err = %v", err)
	}
}

// sessionFake answers one session. chunkURL is what start names as the upload location; chunkStatus and
// finishStatus, when not zero, fail that step.
type sessionFake struct {
	t            *testing.T
	chunkURL     string
	chunkStatus  map[int]int
	finishStatus int
	deleteStatus int
	deletes      int
}

func (f *sessionFake) handler() func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == sessionBase+"/start":
			return jsonResponse(200, envelopeSuccess(fmt.Sprintf(`{"token":%q,"upload_url":%q,"result":true}`, sessionToken, f.chunkURL))), nil
		case r.Method == http.MethodPost && r.URL.Path == chunkPath:
			number, _ := strconv.Atoi(r.URL.Query().Get("chunk_number"))
			size, _ := strconv.Atoi(r.URL.Query().Get("chunk_size"))
			if status := f.chunkStatus[number]; status != 0 {
				return jsonResponse(status, `{"result":"error"}`), nil
			}
			return jsonResponse(200, envelopeSuccess(fmt.Sprintf(`{"number":%d,"status":"ok","size":%d}`, number, size))), nil
		case r.Method == http.MethodPost && r.URL.Path == sessionBase+"/"+sessionToken+"/finish":
			if f.finishStatus != 0 {
				return jsonResponse(f.finishStatus, `{"result":"error"}`), nil
			}
			return jsonResponse(200, envelopeSuccess(fmt.Sprintf(`{"token":%q,"result":true,"file":%s}`, sessionToken, uploadedFile(uploadedID, rootID, "big.bin", 11)))), nil
		case r.Method == http.MethodDelete && r.URL.Path == sessionBase+"/"+sessionToken:
			f.deletes++
			if f.deleteStatus != 0 {
				return jsonResponse(f.deleteStatus, `{"result":"error"}`), nil
			}
			return jsonResponse(200, envelopeSuccess(`true`)), nil
		}
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		return jsonResponse(404, `{}`), nil
	})
}

func lowerLimits(t *testing.T, direct, chunk int64) {
	t.Helper()
	oldDirect, oldChunk := directUploadLimit, chunkSize
	directUploadLimit, chunkSize = direct, chunk
	t.Cleanup(func() { directUploadLimit, chunkSize = oldDirect, oldChunk })
}

func sessionUpload(t *testing.T, fake *sessionFake, extra string) (*environment, *[]call, string, error) {
	t.Helper()
	lowerLimits(t, 10, 4)
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(path, []byte("0123456789A"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := new([]call)
	env := uploadEnvironment(t, dir, calls, fake.handler(), func(r *http.Request) (*http.Response, error) {
		t.Errorf("a request reached the foreign host %s", r.URL.Host)
		return nil, errors.New("foreign")
	})
	args := fmt.Sprintf(`{"drive_id":%d,"directory_id":1,%s"local_path":%s}`, ownDrive, extra, strconv.Quote(path))
	result, err := mustInvoke(t, env, "files-drive", args)
	return env, calls, result, err
}

func TestUploadAboveTheDirectLimitUsesASessionOfChunks(t *testing.T) {
	fake := &sessionFake{t: t, chunkURL: "https://" + uploadHost + chunkPath}
	_, calls, result, err := sessionUpload(t, fake, "")
	out := decodeUpload(t, result, err)
	var steps []string
	var chunks []string
	for _, c := range *calls {
		steps = append(steps, c.method+" "+c.host+c.path)
		if c.path == chunkPath {
			chunks = append(chunks, c.query.Get("chunk_number")+":"+c.query.Get("chunk_size")+":"+c.body)
			if c.auth != "Bearer "+tokenValue || c.host != uploadHost || c.contentType != "application/octet-stream" {
				t.Fatalf("chunk call = %+v", c)
			}
		}
	}
	if strings.Join(chunks, ",") != "1:4:0123,2:4:4567,3:3:89A" {
		t.Fatalf("chunks = %v", chunks)
	}
	if len(steps) != 6 || steps[1] != "POST "+apiHost+sessionBase+"/start" || !strings.HasSuffix(steps[5], "/finish") {
		t.Fatalf("steps = %v", steps)
	}
	start := (*calls)[1]
	var body map[string]any
	if err := json.Unmarshal([]byte(start.body), &body); err != nil || body["total_size"] != float64(11) ||
		body["total_chunks"] != float64(3) || body["directory_id"] != float64(1) || body["file_name"] != "big.bin" ||
		body["conflict"] != "error" {
		t.Fatalf("start body = %s", start.body)
	}
	if out.Method != methodSession || out.Status != statusDone || out.FileID != uploadedID || out.Size != 11 ||
		out.SHA256 == "" || out.ClientToken != "" {
		t.Fatalf("result = %+v", out)
	}
}

func TestUploadSessionRefusesAClientTokenItCannotHonour(t *testing.T) {
	fake := &sessionFake{t: t, chunkURL: "https://" + uploadHost + chunkPath}
	env, calls, _, err := sessionUpload(t, fake, `"client_token":"`+tokenArg+`",`)
	if !isInvalidRequest(err) || len(*calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v", err, *calls)
	}
}

func TestUploadSessionNeverSendsToAForeignOrInsecureUploadURL(t *testing.T) {
	cases := map[string]string{
		"foreign host":       "https://" + storageHost + chunkPath,
		"plain http":         "http://" + uploadHost + chunkPath,
		"lookalike suffix":   "https://upload.infomaniak.com.evil.invalid" + chunkPath,
		"lookalike prefix":   "https://evilinfomaniak.com" + chunkPath,
		"user info":          "https://user:pw@" + uploadHost + chunkPath,
		"other port":         "https://" + uploadHost + ":8443" + chunkPath,
		"query":              "https://" + uploadHost + chunkPath + "?x=1",
		"other session path": "https://" + uploadHost + sessionBase + "/other/chunk",
		"empty":              "",
	}
	for name, location := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &sessionFake{t: t, chunkURL: location}
			_, calls, _, err := sessionUpload(t, fake, "")
			if err == nil || !strings.Contains(err.Error(), "cancelled") {
				t.Fatalf("err = %v, want a refusal with the cancelled session", err)
			}
			for _, c := range *calls {
				if c.path == chunkPath || c.host == storageHost || c.host != apiHost {
					t.Fatalf("a request went to %s%s", c.host, c.path)
				}
			}
			if fake.deletes != 1 {
				t.Fatalf("deletes = %d, want one cancellation", fake.deletes)
			}
		})
	}
}

func TestUploadSessionFailureCancelsOnceAndDoesNotRepeat(t *testing.T) {
	fake := &sessionFake{t: t, chunkURL: "https://" + uploadHost + chunkPath, chunkStatus: map[int]int{2: 500}}
	_, calls, _, err := sessionUpload(t, fake, "")
	if err == nil || !strings.Contains(err.Error(), "cancelled") || !strings.Contains(err.Error(), "may have been received") {
		t.Fatalf("err = %v", err)
	}
	chunkCalls := 0
	for _, c := range *calls {
		if c.path == chunkPath {
			chunkCalls++
		}
	}
	if chunkCalls != 2 || fake.deletes != 1 {
		t.Fatalf("chunk calls = %d, deletes = %d", chunkCalls, fake.deletes)
	}

	// A cancellation that fails is reported with the session, never hidden.
	fake = &sessionFake{t: t, chunkURL: "https://" + uploadHost + chunkPath, chunkStatus: map[int]int{1: 403}, deleteStatus: 500}
	_, _, _, err = sessionUpload(t, fake, "")
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "could not be cancelled") ||
		!strings.Contains(err.Error(), sessionToken) || fake.deletes != 1 {
		t.Fatalf("err = %v, deletes = %d", err, fake.deletes)
	}
}

func TestUploadSessionUnclearFinishKeepsTheSessionAndReportsItsToken(t *testing.T) {
	fake := &sessionFake{t: t, chunkURL: "https://" + uploadHost + chunkPath, finishStatus: 500}
	_, calls, _, err := sessionUpload(t, fake, "")
	if err == nil || !strings.Contains(err.Error(), sessionToken) || !strings.Contains(err.Error(), "may have been saved") {
		t.Fatalf("err = %v", err)
	}
	if fake.deletes != 0 {
		t.Fatalf("deletes = %d, an unclear finish must not erase the upload", fake.deletes)
	}
	finishes := 0
	for _, c := range *calls {
		if strings.HasSuffix(c.path, "/finish") {
			finishes++
		}
	}
	if finishes != 1 {
		t.Fatalf("finish calls = %d, want one", finishes)
	}

	// A refused finish is certain and cancels the session.
	fake = &sessionFake{t: t, chunkURL: "https://" + uploadHost + chunkPath, finishStatus: 400}
	if _, _, _, err = sessionUpload(t, fake, ""); err == nil || fake.deletes != 1 {
		t.Fatalf("err = %v, deletes = %d", err, fake.deletes)
	}
}

func TestUploadSessionReplaceSendsIfMatchOnStart(t *testing.T) {
	lowerLimits(t, 10, 4)
	var calls []call
	var ifMatch string
	handler := (&sessionFake{t: t, chunkURL: "https://" + uploadHost + chunkPath}).handler()
	env := uploadEnvironment(t, t.TempDir(), &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			ifMatch = r.Header.Get("If-Match")
			return jsonResponse(412, `{"result":"error"}`), nil
		}
		return handler(r)
	}, nil)
	args := fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"etag":%q,"content_base64":%q}`, ownDrive, childFileID, knownEtag, inline(bytes.Repeat([]byte("x"), 11)))
	_, err := mustInvoke(t, env, "files-drive", args)
	if err == nil || !strings.Contains(err.Error(), "etag conflict") || ifMatch != knownEtag || len(calls) != 2 {
		t.Fatalf("err = %v, If-Match = %q, calls = %d", err, ifMatch, len(calls))
	}
}

func TestChunkTargetAcceptsOnlyTheSessionsOwnInfomaniakAddress(t *testing.T) {
	good := "https://" + uploadHost + chunkPath
	if _, ok := chunkTarget(good, ownDrive, sessionToken); !ok {
		t.Fatal("the session's own upload address was refused")
	}
	if _, ok := chunkTarget("https://"+apiHost+chunkPath, ownDrive, sessionToken); !ok {
		t.Fatal("the API host was refused")
	}
	if _, ok := chunkTarget(good, ownDrive+1, sessionToken); ok {
		t.Fatal("another drive was accepted")
	}
	if infomaniakHost("infomaniak.com") || infomaniakHost(".infomaniak.com") || infomaniakHost("x.infomaniak.com.evil.invalid") {
		t.Fatal("a foreign host was accepted")
	}
}
