package infomaniakchat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	fileInChanA  = "file00000000000000000a0a"
	fileInChanB  = "file00000000000000000b0b"
	fileInChanC  = "file00000000000000000c0c"
	fileNoPost   = "file00000000000000000e0e"
	fileDeleted  = "file00000000000000000d0d"
	fileContent  = "attachment-content-canary-5c1e"
	fileNameSeen = "report.pdf"
)

func fileJSONOf(id, postID, name string, size int, extra string) string {
	return `{"id":"` + id + `","user_id":"user1","post_id":"` + postID + `","create_at":1735689600000,"update_at":0,` +
		`"delete_at":0,"name":"` + name + `","extension":"pdf","size":` + strconv.Itoa(size) +
		`,"mime_type":"application/pdf","width":0,"height":0,"has_preview_image":false` + extra + `}`
}

// fileServer answers the binding reads and the file endpoints. content serves GET /api/v4/files/{id}.
func fileServer(t *testing.T, content func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	posts := postServer(t, nil)
	size := len(fileContent)
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/v4/files/" + fileInChanA + "/info":
			return jsonResponse(200, fileJSONOf(fileInChanA, postInChanA, fileNameSeen, size, "")), nil
		case "/api/v4/files/" + fileInChanB + "/info":
			return jsonResponse(200, fileJSONOf(fileInChanB, postInChanB, fileNameSeen, size, "")), nil
		case "/api/v4/files/" + fileInChanC + "/info":
			return jsonResponse(200, fileJSONOf(fileInChanC, postInChanC, fileNameSeen, size, "")), nil
		case "/api/v4/files/" + fileNoPost + "/info":
			return jsonResponse(200, fileJSONOf(fileNoPost, "", fileNameSeen, size, "")), nil
		case "/api/v4/files/" + fileDeleted + "/info":
			return jsonResponse(200, strings.Replace(fileJSONOf(fileDeleted, postInChanA, fileNameSeen, size, ""),
				`"delete_at":0`, `"delete_at":1735689900000`, 1)), nil
		case "/api/v4/posts/" + postInChanA + "/files/info":
			return jsonResponse(200, `[`+fileJSONOf(fileInChanA, postInChanA, fileNameSeen, size, "")+`,`+
				strings.Replace(fileJSONOf(fileDeleted, postInChanA, "gone.pdf", size, ""), `"delete_at":0`,
					`"delete_at":1735689900000`, 1)+`]`), nil
		case "/api/v4/files/" + fileInChanA, "/api/v4/files/" + fileInChanB, "/api/v4/files/" + fileInChanC,
			"/api/v4/files/" + fileNoPost:
			if content != nil {
				return content(r)
			}
		default:
			return posts(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func plainContent(body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: httpBody(body)}, nil
	}
}

// failingBody delivers some bytes and then fails, like a connection that breaks during the transfer.
type failingBody struct{ data string }

func (b *failingBody) Read(p []byte) (int, error) {
	if b.data == "" {
		return 0, errors.New("connection reset")
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}
func (*failingBody) Close() error { return nil }

func downloadEnv(t *testing.T, dir string, calls *[]call, handler func(*http.Request) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	cfg := coreConfig()
	for _, name := range []string{"team", "channel"} {
		connection := cfg.Connections[name]
		connection.Files = config.Files{Write: []string{dir}}
		cfg.Connections["files-"+name] = connection
	}
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func downloadArgs(fileID, path string) string {
	return `{"file_id":"` + fileID + `","local_path":` + strconv.Quote(path) + `}`
}

func requestedFileContent(calls []call) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c.path, "/api/v4/files/") && !strings.HasSuffix(c.path, "/info") {
			n++
		}
	}
	return n
}

func emptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("directory = %v, %v, want it empty", entries, err)
	}
}

func TestFileToolsAreReadOnlyGroupedAndInBothProfiles(t *testing.T) {
	if filesDownload.LocalFiles != config.LocalFilesWrite || filesInfo.LocalFiles != "" || messagesFiles.LocalFiles != "" {
		t.Fatalf("local files = %q %q %q", filesDownload.LocalFiles, filesInfo.LocalFiles, messagesFiles.LocalFiles)
	}
	for _, d := range []capability.Descriptor{messagesFiles, filesInfo, filesDownload} {
		if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone ||
			d.Risk.DataSensitivity != "infomaniak-kchat-files" || d.RequiresToolAllowList {
			t.Fatalf("%s risk = %+v", d.ID, d.Risk)
		}
	}
	if withGroup(filesInfo).Group != "files" || withGroup(filesDownload).Group != "files" || withGroup(messagesFiles).Group != "messages" {
		t.Fatal("groups are wrong")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		got := map[string]bool{}
		for _, id := range profile.Tools {
			got[id] = true
		}
		if !got[messagesFiles.ID] || !got[filesInfo.ID] || !got[filesDownload.ID] {
			t.Fatalf("profile %s = %v", profile.ID, profile.Tools)
		}
	}
}

func TestMessagesFilesListsLiveFilesOfABoundPost(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, fileServer(t, nil))
	result, err := env.invoke(messagesFiles.ID, "team", `{"post_id":"`+postInChanA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var out PostFiles
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.Count != 1 || out.Files[0].ID != fileInChanA ||
		out.Files[0].CreatedAt != "2025-01-01T00:00:00Z" || strings.Contains(result, "gone.pdf") {
		t.Fatalf("result = %s, %v", result, err)
	}
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/files/info") && (c.method != http.MethodGet || len(c.query) != 0) {
			t.Fatalf("call = %+v, want a plain GET without include_deleted", c)
		}
	}
}

func TestFileToolsRefuseForeignUnboundAndMalformedTargets(t *testing.T) {
	cases := []struct {
		name, tool, connection, args string
		local                        bool
	}{
		{"post in foreign team", messagesFiles.ID, "team", `{"post_id":"` + postInChanB + `"}`, false},
		{"post outside allow-list", messagesFiles.ID, "channel", `{"post_id":"` + postInChanC + `"}`, false},
		{"malformed post", messagesFiles.ID, "team", `{"post_id":"../x"}`, true},
		{"info foreign team", filesInfo.ID, "team", `{"file_id":"` + fileInChanB + `"}`, false},
		{"info outside allow-list", filesInfo.ID, "channel", `{"file_id":"` + fileInChanC + `"}`, false},
		{"info without post", filesInfo.ID, "team", `{"file_id":"` + fileNoPost + `"}`, false},
		{"info deleted", filesInfo.ID, "team", `{"file_id":"` + fileDeleted + `"}`, false},
		{"info malformed", filesInfo.ID, "team", `{"file_id":"../x"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, fileServer(t, plainContent(fileContent)))
			result, err := env.invoke(tc.tool, tc.connection, tc.args)
			if !isInvalidRequest(err) && !(tc.local && err != nil) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			text := result
			if err != nil {
				text += err.Error()
			}
			for _, secret := range []string{messageCanary, fileNameSeen, postInChanB, chanB, teamB, fileInChanB} {
				if strings.Contains(text, secret) && !strings.Contains(tc.args, secret) {
					t.Fatalf("refusal %q leaks %q", text, secret)
				}
			}
			if tc.local && (len(calls) != 0 || *env.reads != 0) {
				t.Fatalf("calls = %v, reads = %d, want none", calls, *env.reads)
			}
			if requestedFileContent(calls) != 0 {
				t.Fatalf("calls = %v, want no content request", calls)
			}
		})
	}
}

func TestDownloadWritesOnlyMetadataAndNeedsConfirmationToReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.pdf")
	var calls []call
	env := downloadEnv(t, dir, &calls, fileServer(t, plainContent(fileContent)))

	result, err := env.invoke(filesDownload.ID, "files-team", downloadArgs(fileInChanA, path))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	sum := sha256.Sum256([]byte(fileContent))
	var out DownloadResult
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.Name != fileNameSeen || out.FileID != fileInChanA ||
		out.Size != int64(len(fileContent)) || out.SHA256 != hex.EncodeToString(sum[:]) || strings.Contains(result, fileContent) {
		t.Fatalf("result = %s, %v", result, err)
	}
	var generic map[string]any
	_ = json.Unmarshal([]byte(result), &generic)
	if len(generic) != 4 {
		t.Fatalf("result = %v, want exactly file_id, name, size, sha256", generic)
	}
	if got, _ := os.ReadFile(path); string(got) != fileContent {
		t.Fatalf("file = %q", got)
	}

	other := strings.Replace(fileContent, "attachment", "ATTACHMENT", 1)
	env = downloadEnv(t, dir, &calls, fileServer(t, plainContent(other)))
	if _, err := env.invoke(filesDownload.ID, "files-team", downloadArgs(fileInChanA, path)); application.ErrorCode(err) != "confirmation-required" {
		t.Fatalf("err = %v, want the overwrite confirmation", err)
	}
	if got, _ := os.ReadFile(path); string(got) != fileContent {
		t.Fatalf("file = %q, want it unchanged", got)
	}
	if _, err := env.confirmed(filesDownload.ID, "files-team", downloadArgs(fileInChanA, path)); err != nil {
		t.Fatalf("confirmed invoke() = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != other {
		t.Fatalf("file = %q, want it replaced", got)
	}
	last := calls[len(calls)-1]
	if last.method != http.MethodGet || last.host != strings.TrimPrefix(origin, "https://") || len(last.query) != 0 {
		t.Fatalf("last call = %+v", last)
	}
}

func TestDownloadRefusesBeforeProviderIOOrBeforeTheContentRequest(t *testing.T) {
	released, other := t.TempDir(), t.TempDir()
	local := map[string]string{"outside": filepath.Join(other, "x"), "dotdot": released + "/../x",
		"missing parent": filepath.Join(released, "none", "x")}
	for name, path := range local {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := downloadEnv(t, released, &calls, fileServer(t, plainContent(fileContent)))
			_, err := env.invoke(filesDownload.ID, "files-team", downloadArgs(fileInChanA, path))
			if err == nil || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), other) {
				t.Fatalf("err = %v, calls = %v, reads = %d", err, calls, *env.reads)
			}
		})
	}
	t.Run("malformed id", func(t *testing.T) {
		var calls []call
		env := downloadEnv(t, released, &calls, fileServer(t, plainContent(fileContent)))
		_, err := env.invoke(filesDownload.ID, "files-team", downloadArgs("../x", filepath.Join(released, "x")))
		if err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("err = %v, calls = %v, reads = %d", err, calls, *env.reads)
		}
	})
	for name, file := range map[string]string{"foreign channel": fileInChanB, "no post": fileNoPost, "deleted": fileDeleted} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := downloadEnv(t, released, &calls, fileServer(t, plainContent(fileContent)))
			_, err := env.invoke(filesDownload.ID, "files-team", downloadArgs(file, filepath.Join(released, "x")))
			if !isInvalidRequest(err) || requestedFileContent(calls) != 0 {
				t.Fatalf("err = %v, calls = %v, want an invalid request before the content", err, calls)
			}
			emptyDir(t, released)
		})
	}
	t.Run("outside allow-list", func(t *testing.T) {
		var calls []call
		env := downloadEnv(t, released, &calls, fileServer(t, plainContent(fileContent)))
		_, err := env.invoke(filesDownload.ID, "files-channel", downloadArgs(fileInChanC, filepath.Join(released, "x")))
		if !isInvalidRequest(err) || requestedFileContent(calls) != 0 {
			t.Fatalf("err = %v, calls = %v", err, calls)
		}
		emptyDir(t, released)
	})
}

func TestDownloadLeavesNoFileOnSizeMismatchOrBrokenStream(t *testing.T) {
	size := len(fileContent)
	cases := map[string]func(*http.Request) (*http.Response, error){
		"shorter body": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: -1, Body: httpBody(fileContent[:size-3])}, nil
		},
		"longer body": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: -1, Body: httpBody(fileContent + "extra")}, nil
		},
		"announced length differs": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: int64(size + 1), Body: httpBody(fileContent)}, nil
		},
		"broken stream": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: -1, Body: &failingBody{data: fileContent[:size/2]}}, nil
		},
		"server error": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(messageCanary))}, nil
		},
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var calls []call
			env := downloadEnv(t, dir, &calls, fileServer(t, content))
			_, err := env.invoke(filesDownload.ID, "files-team", downloadArgs(fileInChanA, filepath.Join(dir, "out.pdf")))
			if err == nil || strings.Contains(err.Error(), messageCanary) || strings.Contains(err.Error(), "connection reset") {
				t.Fatalf("err = %v", err)
			}
			emptyDir(t, dir)
		})
	}
}

func TestDownloadDoesNotFollowARedirect(t *testing.T) {
	dir := t.TempDir()
	var calls []call
	env := downloadEnv(t, dir, &calls, fileServer(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://storage.example/file"}},
			Body: httpBody("")}, nil
	}))
	_, err := env.invoke(filesDownload.ID, "files-team", downloadArgs(fileInChanA, filepath.Join(dir, "out.pdf")))
	if classOf(err) != "provider-error" || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v, want a provider error about the redirect", err)
	}
	for _, c := range calls {
		if c.host != strings.TrimPrefix(origin, "https://") {
			t.Fatalf("call = %+v, want only the instance host", c)
		}
	}
	emptyDir(t, dir)
}
