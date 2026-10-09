package telegram

import (
	"bytes"
	"context"
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

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	filesChat   = "-100"
	rawFileID   = "RAWFILEID123"
	rawFilePath = "photos/my file#1.jpg"
)

// filesEnv bundles a connection that releases dir for writing with a counting secret resolver.
type filesEnv struct {
	resolved *config.Resolved
	secrets  *secret.Resolver
	red      *redact.Redactor
	lookups  *int
	token    string
}

func newFilesEnv(t *testing.T, dir, token string) filesEnv {
	t.Helper()
	red := &redact.Redactor{}
	lookups := 0
	resolver := secret.NewWith(func(name string) string {
		lookups++
		if name == "TEST_TELEGRAM_BOT_TOKEN" {
			return token
		}
		return ""
	}, nil, nil, red)
	resolved := resolvedWith(filesChat)
	resolved.Files = config.Files{Write: []string{dir}}
	return filesEnv{resolved, resolver, red, &lookups, token}
}

func (e filesEnv) ref(kind refKind, binding, id string) string {
	return signRef(testToken, kind, binding, id)
}

func (e filesEnv) download(t *testing.T, transport http.RoundTripper, ref, path string, confirmed bool) (any, error) {
	t.Helper()
	httpClient := newHTTPClient()
	httpClient.Transport = transport
	raw, _ := json.Marshal(map[string]string{"file_ref": ref, "local_path": path})
	ctx := context.Background()
	if confirmed {
		ctx = capability.WithConfirmed(ctx)
	}
	return invokeFilesDownloadWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

type fileServer struct {
	calls     []string
	getFile   string
	content   string
	status    int
	transport error
}

func (f *fileServer) RoundTrip(r *http.Request) (*http.Response, error) {
	var data []byte
	if r.Body != nil {
		data, _ = io.ReadAll(r.Body)
	}
	f.calls = append(f.calls, r.Method+" "+r.URL.EscapedPath()+" "+string(data))
	if f.transport != nil {
		return nil, f.transport
	}
	if strings.HasSuffix(r.URL.Path, "/getFile") {
		return response(200, `{"ok":true,"result":`+f.getFile+`}`), nil
	}
	status := f.status
	if status == 0 {
		status = 200
	}
	resp := response(status, f.content)
	resp.ContentLength = -1
	return resp, nil
}

func getFileJSON(size int, path string) string {
	out := `{"file_id":"` + rawFileID + `","file_unique_id":"UNIQ","file_path":` + strconv.Quote(path)
	if size > 0 {
		out += `,"file_size":` + strconv.Itoa(size)
	}
	return out + `}`
}

func TestFilesDescriptors(t *testing.T) {
	if filesDownload.LocalFiles != config.LocalFilesWrite || filesGet.LocalFiles != "" {
		t.Fatalf("local files = %q / %q", filesDownload.LocalFiles, filesGet.LocalFiles)
	}
	for _, d := range []capability.Descriptor{filesGet, filesDownload} {
		if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone ||
			d.RequiresToolAllowList || d.Group != groupFiles {
			t.Errorf("%s = %+v", d.ID, d)
		}
	}
	for _, p := range toolProfilesOf(t) {
		for _, id := range p.Tools {
			if id == filesGet.ID || id == filesDownload.ID {
				t.Errorf("profile %s contains %s", p.ID, id)
			}
		}
	}
}

func toolProfilesOf(t *testing.T) []config.ToolProfile {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	return metadata.Profiles
}

func TestFilesGetPostsExactBodyAndOmitsPath(t *testing.T) {
	server := &fileServer{getFile: getFileJSON(42, rawFilePath)}
	client, resolved := multiClient(t, testToken, server, filesChat)
	parsed, err := parseRef(resolved, refFile, signRef(testToken, refFile, filesChat, rawFileID))
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.fileMetadata(context.Background(), parsed)
	if err != nil {
		t.Fatal(err)
	}
	if len(server.calls) != 1 || server.calls[0] != `POST /bot`+testToken+`/getFile {"file_id":"`+rawFileID+`"}` {
		t.Fatalf("calls = %q", server.calls)
	}
	text, _ := json.Marshal(got)
	if string(text) != `{"file_size":42,"file_unique_id":"UNIQ"}` || strings.Contains(string(text), "photos") {
		t.Fatalf("output = %s", text)
	}
}

func TestFilesRefsAreRefusedBeforeIO(t *testing.T) {
	dir := t.TempDir()
	good := signRef(testToken, refFile, filesChat, rawFileID)
	cases := map[string]struct {
		ref        string
		wantLookup bool
	}{
		"raw file id":     {rawFileID, false},
		"foreign binding": {signRef(testToken, refFile, "-999", rawFileID), false},
		"callback kind":   {signRef(testToken, refCallback, filesChat, rawFileID), false},
		"other token":     {signRef("999:other_token", refFile, filesChat, rawFileID), true},
		"tampered":        {good[:len(good)-2] + "AA", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := newFilesEnv(t, dir, testToken)
			server := &fileServer{getFile: getFileJSON(1, "a/b")}
			path := filepath.Join(dir, "out.bin")
			httpClient := newHTTPClient()
			httpClient.Transport = server
			getRaw, _ := json.Marshal(map[string]string{"file_ref": tc.ref})
			_, err := invokeFilesGetWith(context.Background(), env.resolved, env.secrets, env.red, getRaw, httpClient)
			if err == nil {
				t.Fatal("get accepted the reference")
			}
			_, err2 := env.download(t, server, tc.ref, path, false)
			if err2 == nil {
				t.Fatal("download accepted the reference")
			}
			if len(server.calls) != 0 {
				t.Fatalf("provider I/O happened: %q", server.calls)
			}
			if !tc.wantLookup && *env.lookups != 0 {
				t.Fatalf("secret accessed %d times for a malformed or foreign reference", *env.lookups)
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Fatal("file left behind")
			}
			for _, e := range []error{err, err2} {
				if strings.Contains(e.Error(), testToken) || strings.Contains(e.Error(), rawFileID) {
					t.Fatalf("error leaks: %v", e)
				}
			}
		})
	}
}

func TestFilesDownloadWritesFileAndNeedsConfirmationToReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jpg")
	env := newFilesEnv(t, dir, testToken)
	ref := env.ref(refFile, filesChat, rawFileID)
	server := &fileServer{getFile: getFileJSON(7, rawFilePath), content: "content"}

	result, err := env.download(t, server, ref, path, false)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("content"))
	want := &DownloadResult{FileUniqueID: "UNIQ", Size: 7, SHA256: hex.EncodeToString(sum[:])}
	if *(result.(*DownloadResult)) != *want {
		t.Fatalf("result = %+v", result)
	}
	if len(server.calls) != 2 || server.calls[0] != `POST /bot`+testToken+`/getFile {"file_id":"`+rawFileID+`"}` ||
		server.calls[1] != `GET /file/bot`+testToken+`/photos/my%20file%231.jpg ` {
		t.Fatalf("calls = %q", server.calls)
	}
	if got, _ := os.ReadFile(path); string(got) != "content" {
		t.Fatalf("file = %q", got)
	}

	server = &fileServer{getFile: getFileJSON(3, "a/b.txt"), content: "new"}
	if _, err := env.download(t, server, ref, path, false); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Fatalf("err = %v, want the overwrite confirmation", err)
	}
	if len(server.calls) != 0 {
		t.Fatalf("provider I/O before confirmation: %q", server.calls)
	}
	if got, _ := os.ReadFile(path); string(got) != "content" {
		t.Fatalf("file = %q, want it unchanged", got)
	}
	if _, err := env.download(t, server, ref, path, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("file = %q, want it replaced", got)
	}
	assertNoLeftovers(t, dir, "out.jpg")
}

func assertNoLeftovers(t *testing.T, dir string, keep ...string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		ok := false
		for _, k := range keep {
			ok = ok || e.Name() == k
		}
		if !ok {
			t.Errorf("leftover file %s", e.Name())
		}
	}
}

func TestEscapeFilePath(t *testing.T) {
	for _, bad := range []string{"", "/abs/x", "a/../b", "..", "a//b", "a/./b", "a/b/", "a\x00b", "a\nb", "a\x7fb", `a\b`,
		strings.Repeat("a", maxFilePathLen+1), "a\xffb"} {
		if _, ok := escapeFilePath(bad); ok {
			t.Errorf("escapeFilePath(%q) accepted", bad)
		}
	}
	got, ok := escapeFilePath("documents/file 1?.pdf")
	if !ok || got != "documents/file%201%3F.pdf" {
		t.Errorf("escapeFilePath = %q, %v", got, ok)
	}
}

func TestFilesDownloadRefusesUnsafeFilePathWithoutDownload(t *testing.T) {
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../b", "", "a\x01b"} {
		dir := t.TempDir()
		env := newFilesEnv(t, dir, testToken)
		server := &fileServer{getFile: getFileJSON(3, bad)}
		_, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(dir, "o"), false)
		if err == nil || len(server.calls) != 1 {
			t.Fatalf("path %q: err = %v, calls = %q", bad, err, server.calls)
		}
		if bad != "" && strings.Contains(err.Error(), strings.TrimSpace(bad)) && strings.Contains(bad, "/") {
			t.Errorf("error names the path: %v", err)
		}
		assertNoLeftovers(t, dir)
	}
}

func TestFilesDownloadSizeLimits(t *testing.T) {
	t.Run("reported above 20 MB is refused before download", func(t *testing.T) {
		dir := t.TempDir()
		env := newFilesEnv(t, dir, testToken)
		server := &fileServer{getFile: getFileJSON(maxDownloadBytes+1, "a/b")}
		_, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(dir, "o"), false)
		if err == nil || len(server.calls) != 1 {
			t.Fatalf("err = %v, calls = %q", err, server.calls)
		}
		assertNoLeftovers(t, dir)
	})
	t.Run("body longer than announced", func(t *testing.T) {
		dir := t.TempDir()
		env := newFilesEnv(t, dir, testToken)
		server := &fileServer{getFile: getFileJSON(3, "a/b"), content: "toolong"}
		_, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(dir, "o"), false)
		if err == nil {
			t.Fatal("longer body accepted")
		}
		assertNoLeftovers(t, dir)
	})
	t.Run("body shorter than announced", func(t *testing.T) {
		dir := t.TempDir()
		env := newFilesEnv(t, dir, testToken)
		server := &fileServer{getFile: getFileJSON(30, "a/b"), content: "short"}
		if _, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(dir, "o"), false); err == nil {
			t.Fatal("short body accepted")
		}
		assertNoLeftovers(t, dir)
	})
	t.Run("unreported size is capped at 20 MB", func(t *testing.T) {
		dir := t.TempDir()
		env := newFilesEnv(t, dir, testToken)
		server := &fileServer{getFile: getFileJSON(0, "a/b"), content: string(bytes.Repeat([]byte{'x'}, maxDownloadBytes+10))}
		if _, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(dir, "o"), false); err == nil {
			t.Fatal("oversized body accepted")
		}
		assertNoLeftovers(t, dir)
	})
	t.Run("unreported size within the cap is written", func(t *testing.T) {
		dir := t.TempDir()
		env := newFilesEnv(t, dir, testToken)
		server := &fileServer{getFile: getFileJSON(0, "a/b"), content: "abc"}
		if _, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(dir, "o"), false); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFilesDownloadErrorsNeverLeakTokenURLOrPath(t *testing.T) {
	cases := map[string]*fileServer{
		"transport error":       {getFile: getFileJSON(3, rawFilePath), transport: errors.New("dial tcp: boom")},
		"download status error": {getFile: getFileJSON(3, rawFilePath), status: 404, content: "SECRETBODY " + rawFilePath},
		"redirect":              {getFile: getFileJSON(3, rawFilePath), status: 302, content: "SECRETBODY"},
	}
	for name, server := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			env := newFilesEnv(t, dir, testToken)
			// The transport error also fails getFile; make the content request the failing one otherwise.
			_, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(dir, "o"), false)
			if err == nil {
				t.Fatal("expected an error")
			}
			text := err.Error()
			for _, forbidden := range []string{testToken, "bot" + testToken, "photos", "my file", "my%20file", "SECRETBODY",
				"api.telegram.test", rawFileID, dir} {
				if strings.Contains(text, forbidden) {
					t.Errorf("error contains %q: %s", forbidden, text)
				}
			}
			assertNoLeftovers(t, dir)
		})
	}
}

// contentFailing answers getFile normally and fails the content request at the transport.
type contentFailing struct{ fileServer }

func (c *contentFailing) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/getFile") {
		return c.fileServer.RoundTrip(r)
	}
	return nil, errors.New("Get \"" + r.URL.String() + "\": connection reset")
}

func TestFilesDownloadTransportErrorOfContentRequestHidesURL(t *testing.T) {
	dir := t.TempDir()
	env := newFilesEnv(t, dir, testToken)
	server := &contentFailing{fileServer{getFile: getFileJSON(3, rawFilePath)}}
	_, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(dir, "o"), false)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, forbidden := range []string{testToken, "photos", "my%20file", "api.telegram.test", "/file/bot"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("error contains %q: %v", forbidden, err)
		}
	}
	assertNoLeftovers(t, dir)
}

func TestFilesDownloadRefusesPathOutsideReleaseBeforeSecretAccess(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	env := newFilesEnv(t, dir, testToken)
	server := &fileServer{getFile: getFileJSON(3, "a/b")}
	_, err := env.download(t, server, env.ref(refFile, filesChat, rawFileID), filepath.Join(other, "o"), false)
	if err == nil || *env.lookups != 0 || len(server.calls) != 0 {
		t.Fatalf("err = %v, lookups = %d, calls = %q", err, *env.lookups, server.calls)
	}
}
