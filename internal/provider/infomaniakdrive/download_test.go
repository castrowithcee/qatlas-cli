package infomaniakdrive

import (
	"crypto/sha256"
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
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// downloadEnvironment is the core with connections that release dir for writing, next to the plain ones.
func downloadEnvironment(t *testing.T, dir string, calls *[]call, handler func(*http.Request) (*http.Response, error),
	foreign func(*http.Request) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler, foreign)
	cfg := coreConfig()
	for _, name := range []string{"account", "drive", "driveforeign"} {
		connection := cfg.Connections[name]
		connection.Files = config.Files{Write: []string{dir}}
		cfg.Connections["files-"+name] = connection
	}
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// downloadFake answers the ownership check, the metadata read, and the content request.
func downloadFake(t *testing.T, content func() *http.Response) func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/download"):
			return content(), nil
		case r.URL.Path == fmt.Sprintf("/3/drive/%d/files/%d", ownDrive, childFileID):
			return jsonResponse(200, envelopeSuccess(fileJSONOf(childFileID, rootID, "report.pdf", "file"))), nil
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		return nil, errors.New("unexpected")
	})
}

func plainContent(body string) func() *http.Response {
	return func() *http.Response {
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: httpBody(body)}
	}
}

func downloadArgs(path string) string {
	return fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"local_path":%s}`, ownDrive, childFileID, strconv.Quote(path))
}

func TestDownloadDescriptorDeclaresLocalWrite(t *testing.T) {
	d := filesDownload
	if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone ||
		d.LocalFiles != config.LocalFilesWrite || d.RequiresToolAllowList || filesGet.LocalFiles != "" {
		t.Fatalf("descriptor = %+v", d)
	}
}

func TestDownloadToAReleasedPathWritesOnlyMetadataAndNeedsConfirmationToReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.pdf")
	var calls []call
	env := downloadEnvironment(t, dir, &calls, downloadFake(t, plainContent("file content")), nil)

	result, err := env.invoke(filesDownload.ID, "files-drive", downloadArgs(path))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	sum := sha256.Sum256([]byte("file content"))
	var out DownloadResult
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.Name != "report.pdf" || out.Size != 12 ||
		out.SHA256 != hex.EncodeToString(sum[:]) || out.FileID != childFileID || strings.Contains(result, "content") {
		t.Fatalf("result = %s, %v", result, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "file content" {
		t.Fatalf("file = %q", got)
	}

	// An existing file is kept without confirmation.
	env = downloadEnvironment(t, dir, &calls, downloadFake(t, plainContent("new content")), nil)
	if _, err := env.invoke(filesDownload.ID, "files-drive", downloadArgs(path)); application.ErrorCode(err) != "confirmation-required" {
		t.Fatalf("err = %v, want the overwrite confirmation", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "file content" {
		t.Fatalf("file = %q, want it unchanged", got)
	}
	if _, err := env.invokeConfirmed(filesDownload.ID, "files-drive", downloadArgs(path)); err != nil {
		t.Fatalf("confirmed invoke() = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new content" {
		t.Fatalf("file = %q, want it replaced", got)
	}
}

func TestDownloadRefusesAPathOutsideFilesWriteBeforeProviderIO(t *testing.T) {
	released, other := t.TempDir(), t.TempDir()
	for name, path := range map[string]string{"outside": filepath.Join(other, "x"), "dotdot": released + "/../x",
		"missing parent": filepath.Join(released, "none", "x")} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := downloadEnvironment(t, released, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			_, err := env.invoke(filesDownload.ID, "files-drive", downloadArgs(path))
			if err == nil || strings.Contains(err.Error(), other) || strings.Contains(err.Error(), released) {
				t.Fatalf("err = %v", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestDownloadLeavesNoPartialFile(t *testing.T) {
	cases := map[string]func() *http.Response{
		"reported too large": func() *http.Response {
			return &http.Response{StatusCode: 200, ContentLength: localfile.MaxFileBytes + 1, Body: httpBody("x")}
		},
		"longer than reported": func() *http.Response {
			return &http.Response{StatusCode: 200, ContentLength: 3, Body: httpBody("longer")}
		},
		"shorter than reported": func() *http.Response {
			return &http.Response{StatusCode: 200, ContentLength: 100, Body: httpBody("short")}
		},
		"failure status": func() *http.Response { return jsonResponse(500, `{"result":"error"}`) },
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var calls []call
			env := downloadEnvironment(t, dir, &calls, downloadFake(t, content), nil)
			if _, err := env.invoke(filesDownload.ID, "files-drive", downloadArgs(filepath.Join(dir, "out"))); err == nil {
				t.Fatal("invoke() succeeded, want a failure")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("directory = %v, want no file", entries)
			}
		})
	}
}

func TestDownloadRefusesForeignDriveBeforeProviderIO(t *testing.T) {
	dir := t.TempDir()
	var calls []call
	env := downloadEnvironment(t, dir, &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}), nil)
	args := fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"local_path":%s}`, foreignDrive, childFileID, strconv.Quote(filepath.Join(dir, "x")))
	for connection, wantCalls := range map[string]int{"files-drive": 0, "files-account": 1} {
		calls = nil
		if _, err := env.invoke(filesDownload.ID, connection, args); !isInvalidRequest(err) || len(calls) != wantCalls {
			t.Fatalf("%s: err = %v, calls = %+v", connection, err, calls)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("directory = %v, want no file", entries)
	}
}

func TestDownloadRedirectNeverSendsTheTokenToTheStorageHost(t *testing.T) {
	dir := t.TempDir()
	var calls []call
	var foreignAuth string
	env := downloadEnvironment(t, dir, &calls, downloadFake(t, func() *http.Response {
		return &http.Response{StatusCode: http.StatusFound,
			Header: http.Header{"Location": {"https://" + storageHost + "/blob/1"}}, Body: httpBody("")}
	}), func(r *http.Request) (*http.Response, error) {
		foreignAuth = r.Header.Get("Authorization")
		return &http.Response{StatusCode: 200, ContentLength: 4, Body: httpBody("blob")}, nil
	})
	path := filepath.Join(dir, "blob")
	if _, err := env.invoke(filesDownload.ID, "files-drive", downloadArgs(path)); err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if foreignAuth != "" {
		t.Fatalf("the Authorization header reached the storage host: %q", foreignAuth)
	}
	if got, _ := os.ReadFile(path); string(got) != "blob" {
		t.Fatalf("file = %q", got)
	}
}

func TestDownloadNeedsAReleasedDirectoryToBeOffered(t *testing.T) {
	cfg := coreConfig()
	if cfg.ConnectionAllows("drive", filesDownload.Tool()) {
		t.Fatal("a connection without files.write was offered files.download")
	}
	if !cfg.ConnectionAllows("drive", filesGet.Tool()) {
		t.Fatal("files.get is no longer offered to a connection without files.write")
	}
}
