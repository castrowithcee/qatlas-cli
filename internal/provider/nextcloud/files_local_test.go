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
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const helloSHA = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

func localConnection(read, write string) *config.Resolved {
	resolved := resolvedConnection("reports", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "Reports")
	if read != "" {
		resolved.Files.Read = []string{read}
	}
	if write != "" {
		resolved.Files.Write = []string{write}
	}
	return resolved
}

func noteHandler(t *testing.T, content *string, puts *[]string) func(*http.Request) (*http.Response, error) {
	return func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodPut:
			*puts = append(*puts, request.Header.Get("If-Match")+"|"+request.Header.Get("If-None-Match"))
			return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{"Etag": []string{`"v2"`}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		case http.MethodGet:
			return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(*content)), Header: http.Header{"Etag": []string{`"v1"`}},
				Body: io.NopCloser(strings.NewReader(*content))}, nil
		case methodPropfind:
			return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
		}
		t.Errorf("unexpected method %s", request.Method)
		return nil, errors.New("unexpected")
	}
}

func TestUploadFromAReleasedLocalPath(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := ""
	var puts []string
	calls := serve(t, noteHandler(t, &content, &puts))
	red := &redact.Redactor{}
	args := `{"path":"note.txt","local_path":` + strconv.Quote(source) + `}`
	result, err := invokeFilesCreate(context.Background(), localConnection(dir, ""), resolver(red), red, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)
	if got["created"] != true || got["size"] != int64(5) || got["sha256"] != helloSHA || got["name"] != "note.txt" || got["etag"] != "v2" {
		t.Errorf("result = %+v", got)
	}
	if len(*calls) != 1 || len(puts) != 1 || puts[0] != "|*" || (*calls)[0].body != "hello" {
		t.Errorf("calls = %d, puts = %v", len(*calls), puts)
	}

	args = `{"path":"note.txt","etag":"v1","local_path":` + strconv.Quote(source) + `}`
	if _, err := invokeFilesUpdate(context.Background(), localConnection(dir, ""), resolver(red), red, json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
	if len(puts) != 2 || puts[1] != `"v1"|` || (*calls)[1].body != "hello" {
		t.Errorf("puts = %v", puts)
	}
}

func TestLocalUploadIsRefusedBeforeSecretAndIO(t *testing.T) {
	released, other := t.TempDir(), t.TempDir()
	outside := filepath.Join(other, "x.txt")
	_ = os.WriteFile(outside, []byte("x"), 0o600)
	refuse(t)
	red := &redact.Redactor{}
	var pathErr *localfile.PathError
	_, err := invokeFilesCreate(context.Background(), localConnection(released, ""), resolver(red), red,
		json.RawMessage(`{"path":"a.txt","local_path":`+strconv.Quote(outside)+`}`))
	if !errors.As(err, &pathErr) || strings.Contains(err.Error(), other) {
		t.Errorf("err = %v", err)
	}
	for _, args := range []string{
		`{"path":"a.txt"}`,
		`{"path":"a.txt","content_base64":"aGk=","local_path":` + strconv.Quote(outside) + `}`,
	} {
		if _, err := invokeFilesCreate(context.Background(), localConnection(released, ""), resolver(red), red, json.RawMessage(args)); !errors.As(err, &pathErr) {
			t.Errorf("args %s: err = %v", args, err)
		}
	}
	if _, err := invokeFilesCreate(context.Background(), localConnection("", ""), resolver(red), red,
		json.RawMessage(`{"path":"a.txt","local_path":`+strconv.Quote(outside)+`}`)); !errors.As(err, &pathErr) {
		t.Errorf("no release: err = %v", err)
	}
}

func TestLocalUploadFailureIsNotRetried(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "in.txt")
	_ = os.WriteFile(source, []byte("hello"), 0o600)
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(bodyCanary))}, nil
	})
	red := &redact.Redactor{}
	_, err := invokeFilesCreate(context.Background(), localConnection(dir, ""), resolver(red), red,
		json.RawMessage(`{"path":"a.txt","local_path":`+strconv.Quote(source)+`}`))
	if err == nil || strings.Contains(err.Error(), bodyCanary) || len(*calls) != 1 || !strings.Contains(err.Error(), "stat the file before repeating") {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestDownloadToAReleasedPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	content := "hello"
	serve(t, noteHandler(t, &content, nil))
	red := &redact.Redactor{}
	args := json.RawMessage(`{"path":"note.txt","local_path":` + strconv.Quote(target) + `}`)
	result, err := invokeFilesGet(context.Background(), localConnection("", dir), resolver(red), red, args)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*DownloadResult)
	if got.Size != 5 || got.SHA256 != helloSHA || got.Name != "note.txt" || got.Path != "note.txt" || got.ETag == "" {
		t.Errorf("result = %+v", got)
	}
	if data, _ := os.ReadFile(target); string(data) != "hello" {
		t.Errorf("file = %q", data)
	}
	if _, err := invokeFilesGet(context.Background(), localConnection("", dir), resolver(red), red, args); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Fatalf("unconfirmed overwrite: %v", err)
	}
	content = "world"
	if _, err := invokeFilesGet(capability.WithConfirmed(context.Background()), localConnection("", dir), resolver(red), red, args); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "world" {
		t.Errorf("file = %q, want it replaced", data)
	}
}

func TestInlineDownloadAndSizeRefusal(t *testing.T) {
	content := "hello"
	serve(t, noteHandler(t, &content, nil))
	red := &redact.Redactor{}
	result, err := invokeFilesGet(context.Background(), localConnection("", ""), resolver(red), red, json.RawMessage(`{"path":"note.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(*Content); got.ContentBase64 != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Errorf("result = %+v", got)
	}
	content = strings.Repeat("x", maxFileBytes+1)
	_, err = invokeFilesGet(context.Background(), localConnection("", ""), resolver(red), red, json.RawMessage(`{"path":"note.txt"}`))
	if err == nil || !strings.Contains(err.Error(), "local_path") {
		t.Errorf("err = %v, want the size refusal naming local_path", err)
	}
}

func TestLocalDownloadIsRefusedOutsideTheRootAndTheRelease(t *testing.T) {
	dir := t.TempDir()
	refuse(t)
	red := &redact.Redactor{}
	_, err := invokeFilesGet(context.Background(), localConnection("", dir), resolver(red), red,
		json.RawMessage(`{"path":"../x.txt","local_path":`+strconv.Quote(filepath.Join(dir, "o.txt"))+`}`))
	if err == nil {
		t.Error("a path outside the root was accepted")
	}
	var pathErr *localfile.PathError
	_, err = invokeFilesGet(context.Background(), localConnection("", dir), resolver(red), red,
		json.RawMessage(`{"path":"note.txt","local_path":`+strconv.Quote(filepath.Join(t.TempDir(), "o.txt"))+`}`))
	if !errors.As(err, &pathErr) {
		t.Errorf("err = %v", err)
	}
}

func TestLocalFileDescriptors(t *testing.T) {
	want := map[string]config.LocalFiles{
		"nextcloud.files.create": config.LocalFilesRead, "nextcloud.files.update": config.LocalFilesRead,
		"nextcloud.files.get": config.LocalFilesWrite, "nextcloud.files.list": "", "nextcloud.files.delete": "",
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, d := range reg.Provider(Provider) {
		if w, ok := want[d.ID]; ok && d.LocalFiles != w {
			t.Errorf("%s local files = %q, want %q", d.ID, d.LocalFiles, w)
		}
	}
}
