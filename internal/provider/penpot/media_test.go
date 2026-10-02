package penpot

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
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	mediaA    = "00000000-0000-0000-0000-000000000091"
	storageA  = "00000000-0000-0000-0000-000000000092"
	exportObj = "00000000-0000-0000-0000-000000000093"
)

var transferTools = []string{mediaUpload.ID, mediaFromURL.ID, filesExport.ID, filesImport.ID}

func sumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// resolvedFor builds a resolved connection of the test configuration with released directories.
func resolvedFor(targets []string, read, write string) *config.Resolved {
	resolved := &config.Resolved{Name: "x", Provider: Provider, BaseURL: baseURL, Targets: targets, Credential: "token",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAccessToken: tokenEnv}}}
	if read != "" {
		resolved.Files.Read = []string{read}
	}
	if write != "" {
		resolved.Files.Write = []string{write}
	}
	return resolved
}

func parts(t *testing.T, c call) (map[string]string, map[string]string, []byte) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(c.contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q, %v", c.contentType, err)
	}
	reader := multipart.NewReader(bytes.NewReader(c.raw), params["boundary"])
	fields, headers, file := map[string]string{}, map[string]string{}, []byte(nil)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			headers["name"], headers["type"], headers["part"] = part.FileName(), part.Header.Get("Content-Type"), part.FormName()
			file = data
			continue
		}
		fields[part.FormName()] = string(data)
	}
	return fields, headers, file
}

func mediaHandler(answer string) func(call) (*http.Response, error) {
	files := filesHandler(0)
	return func(c call) (*http.Response, error) {
		switch c.command() {
		case cmdUploadMedia, cmdMediaFromURL:
			return jsonResponse(200, answer), nil
		}
		return files(c)
	}
}

const mediaBody = `{"id":"` + mediaA + `","fileId":"` + fileA1 + `","mediaId":"` + storageA +
	`","name":"logo.png","width":64,"height":32,"mtype":"image/png"}`

func TestMediaUploadFromAReleasedLocalFile(t *testing.T) {
	var calls []call
	serve(t, &calls, mediaHandler(mediaBody))
	dir := t.TempDir()
	content := []byte("\x89PNG not really")
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	red := &redact.Redactor{}
	raw := json.RawMessage(`{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `","local_path":"` + filepath.Join(dir, "logo.png") + `"}`)
	result, err := invokeMediaUpload(context.Background(), resolvedFor([]string{"team/" + teamA}, dir, ""), resolver(red, nil), red, raw)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*MediaStored)
	if got.ID != mediaA || got.MediaID != storageA || got.Width != 64 || got.Size == nil || *got.Size != int64(len(content)) ||
		got.SHA256 != sumHex(content) || got.ProjectID != projectA1 {
		t.Errorf("result = %+v", got)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdProjectFile+","+cmdUploadMedia {
		t.Fatalf("commands = %s", got)
	}
	sent := calls[len(calls)-1]
	fields, headers, file := parts(t, sent)
	if fields["file-id"] != fileA1 || fields["is-local"] != "true" || fields["name"] != "logo.png" ||
		headers["part"] != "content" || headers["type"] != "image/png" || headers["name"] != "logo.png" || !bytes.Equal(file, content) {
		t.Errorf("fields = %v, headers = %v, file = %q", fields, headers, file)
	}
	if sent.auth != "Token "+tokenValue {
		t.Errorf("auth = %q", sent.auth)
	}
}

func TestMediaUploadInline(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, mediaHandler(mediaBody))
	content := []byte("<svg/>")
	encoded := base64.StdEncoding.EncodeToString(content)
	args := `{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `","name":"icon.svg","is_local":false,"content_base64":"` + encoded + `"}`
	result, err := env.invokeConfirmed(mediaUpload.ID, "write", args)
	if err != nil || !strings.Contains(result, sumHex(content)) || !strings.Contains(result, `"size":6`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	fields, headers, file := parts(t, calls[len(calls)-1])
	if fields["is-local"] != "false" || headers["type"] != "image/svg+xml" || !bytes.Equal(file, content) {
		t.Errorf("fields = %v, headers = %v", fields, headers)
	}
	// Unconfirmed, without a name, with a foreign type, or above 4 MiB: nothing is sent.
	calls = nil
	for name, arguments := range map[string]string{
		"noname":   `"content_base64":"` + encoded + `"`,
		"badtype":  `"name":"a.exe","content_base64":"` + encoded + `"`,
		"badb64":   `"name":"a.png","content_base64":"%%%"`,
		"toolarge": `"name":"a.png","content_base64":"` + strings.Repeat("A", maxInlineBase64+4) + `"`,
		"both":     `"name":"a.png","content_base64":"` + encoded + `","local_path":"/x/a.png"`,
		"neither":  `"name":"a.png"`,
		"sep":      `"name":"a/b.png","content_base64":"` + encoded + `"`,
	} {
		if _, err := env.invokeConfirmed(mediaUpload.ID, "write", `{"project_id":"`+projectA1+`","file_id":"`+fileA1+`",`+arguments+`}`); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(changes(calls)) != 0 {
		t.Errorf("a refused upload sent a change: %v", commands(calls))
	}
}

func TestMediaToolsRefuseForeignTargetsBeforeAnySecretOrRequest(t *testing.T) {
	for _, test := range []struct{ tool, connection, args string }{
		{mediaUpload.ID, "narrow", `{"project_id":"` + projectA2 + `","file_id":"` + fileA1 + `","name":"a.png","content_base64":"aGk="}`},
		{mediaFromURL.ID, "narrow", `{"project_id":"` + projectA2 + `","file_id":"` + fileA1 + `","url":"https://images.example.com/a.png"}`},
		{filesExport.ID, "narrow", `{"project_id":"` + projectA2 + `","file_id":"` + fileA1 + `","local_path":"/nonexistent/a.penpot"}`},
		{filesImport.ID, "narrow", `{"project_id":"` + projectA2 + `","name":"x","local_path":"/nonexistent/a.penpot"}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, mediaHandler(mediaBody))
		_, err := env.invokeConfirmed(test.tool, "writenarrow", test.args)
		if err == nil || !isInvalidRequest(err) || strings.Contains(err.Error(), projectA2) {
			t.Errorf("%s: err = %v", test.tool, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: calls = %v, secret reads = %d", test.tool, commands(calls), *env.reads)
		}
	}
}

func TestMediaToolsBindFileAndProjectThroughTheTargets(t *testing.T) {
	for _, test := range []struct{ tool, args string }{
		{mediaUpload.ID, `{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `","name":"a.png","content_base64":"aGk="}`},
		{mediaFromURL.ID, `{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `","url":"https://images.example.com/a.png"}`},
		{mediaFromURL.ID, `{"project_id":"` + projectOut + `","file_id":"` + fileA1 + `","url":"https://images.example.com/a.png"}`},
		{filesImport.ID, `{"project_id":"` + projectOut + `","name":"x","local_path":"IMPORT"}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, mediaHandler(mediaBody))
		args := test.args
		if strings.Contains(args, "IMPORT") {
			args = strings.Replace(args, "IMPORT", writeArchive(t, env), 1)
		}
		_, err := env.invokeConfirmed(test.tool, "write", args)
		if err == nil || !isInvalidRequest(err) || len(changes(calls)) != 0 {
			t.Errorf("%s: err = %v, commands = %v", test.tool, err, commands(calls))
		}
	}
}

// writeArchive writes a small archive into the directory the environment releases for reading.
func writeArchive(t *testing.T, env *environment) string {
	t.Helper()
	path := filepath.Join(env.read, "a.penpot")
	if err := os.WriteFile(path, []byte("PK archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMediaFromURLRefusesUnsafeAddresses(t *testing.T) {
	good := []string{"https://images.example.com/a.png", "https://cdn.example.org:443/x?y=1", "https://xn--bcher-kva.example/a.svg",
		"HTTPS://Images.Example.com/a.png"}
	bad := []string{"", "http://images.example.com/a.png", "ftp://images.example.com/a.png", "file:///etc/passwd",
		"//images.example.com/a.png", "https:///a.png", "https://user:pw@images.example.com/a.png", "https://user@images.example.com/",
		"https://images.example.com/a.png#frag", "https://images.example.com:8443/a.png", "https://images.example.com:80/a.png",
		"https://localhost/a.png", "https://LOCALHOST./a.png", "https://app.localhost/a.png", "https://intranet/a.png",
		"https://printer.local/a.png", "https://metadata.google.internal/", "https://host.internal/a.png", "https://nas.lan/a.png",
		"https://router.home.arpa/", "https://127.0.0.1/a.png", "https://10.0.0.1/a.png", "https://192.168.1.1/", "https://169.254.169.254/latest",
		"https://0.0.0.0/", "https://[::1]/a.png", "https://[fe80::1]/", "https://[::ffff:127.0.0.1]/", "https://2130706433/a.png",
		"https://0x7f.0.0.1/", "https://017700000001/", "https://127.1/", "https://example.com./x.png/../..\\", "https://exa mple.com/",
		"https://images.example.com/a b.png", "https://images.example.com/\x00", "https://-bad.example.com/", "https://a..example.com/",
		"https://bücher.example/a.png", "https://example.com\\@evil.test/", "https://images.example.com/" + strings.Repeat("a", maxURLLength)}
	for _, raw := range good {
		if err := checkMediaURL(raw); err != nil {
			t.Errorf("checkMediaURL(%q) = %v, want accepted", raw, err)
		}
	}
	for _, raw := range bad {
		if err := checkMediaURL(raw); err == nil {
			t.Errorf("checkMediaURL(%q) accepted", raw)
		}
	}
}

func TestMediaFromURLSendsOneFixedCommandAndRefusesBeforeIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, mediaHandler(mediaBody))
	result, err := env.invokeConfirmed(mediaFromURL.ID, "write",
		`{"project_id":"`+projectA1+`","file_id":"`+fileA1+`","url":"https://images.example.com/logo.png","name":"Logo"}`)
	if err != nil || !strings.Contains(result, mediaA) || strings.Contains(result, "images.example.com") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := changes(calls)
	if len(sent) != 1 || sent[0].command() != cmdMediaFromURL || sent[0].body["file-id"] != fileA1 ||
		sent[0].body["url"] != "https://images.example.com/logo.png" || sent[0].body["name"] != "Logo" ||
		sent[0].body["is-local"] != true || len(sent[0].body) != 4 {
		t.Fatalf("sent = %+v", sent)
	}
	calls = nil
	*env.reads = 0
	_, err = env.invokeConfirmed(mediaFromURL.ID, "write",
		`{"project_id":"`+projectA1+`","file_id":"`+fileA1+`","url":"https://127.0.0.1/a.png"}`)
	if err == nil || !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %v, reads = %d", err, commands(calls), *env.reads)
	}
}

func TestMediaChangesAreConfirmedSentOnceAndReportUncertainty(t *testing.T) {
	args := map[string]string{
		mediaUpload.ID:  `{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `","name":"a.png","content_base64":"aGk="}`,
		mediaFromURL.ID: `{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `","url":"https://images.example.com/a.png"}`,
	}
	for _, tool := range []string{mediaUpload.ID, mediaFromURL.ID} {
		var calls []call
		env := newEnvironment(t, &calls, mediaHandler(mediaBody))
		if _, err := env.invoke(tool, "write", args[tool]); err == nil || application.ErrorCode(err) != "confirmation-required" {
			t.Errorf("%s: unconfirmed err = %v", tool, err)
		}
		if len(calls) != 0 {
			t.Errorf("%s: unconfirmed call reached the provider", tool)
		}
		for _, test := range []struct {
			status    int
			body      string
			uncertain bool
		}{{500, bodyCanary, true}, {200, `not json`, true}, {200, `{"name":"x"}`, true}, {403, bodyCanary, false}, {429, bodyCanary, false}} {
			calls = nil
			env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
				if isChange(c.command()) {
					return jsonResponse(test.status, test.body), nil
				}
				return filesHandler(0)(c)
			})
			_, err := env.invokeConfirmed(tool, "write", args[tool])
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), tokenValue) ||
				strings.Contains(err.Error(), "may have taken effect") != test.uncertain || len(changes(calls)) != 1 {
				t.Errorf("%s status %d: err = %v, changes = %d", tool, test.status, err, len(changes(calls)))
			}
		}
		calls = nil
		env = newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if isChange(c.command()) {
				return nil, errResetByPeer
			}
			return filesHandler(0)(c)
		})
		_, err := env.invokeConfirmed(tool, "write", args[tool])
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(changes(calls)) != 1 {
			t.Errorf("%s: reset err = %v, changes = %d", tool, err, len(changes(calls)))
		}
	}
}

func exportHandler(content []byte) func(call) (*http.Response, error) {
	files := filesHandler(0)
	return func(c call) (*http.Response, error) {
		switch {
		case c.command() == cmdExportFile:
			return sseResponse(200, "event: progress\ndata: {}\n\nevent: end\ndata: \"https://elsewhere.invalid/assets/by-id/"+exportObj+"\"\n\n"), nil
		case c.path == exportPath+exportObj:
			response := jsonResponse(200, string(content))
			response.ContentLength = int64(len(content))
			return response, nil
		}
		return files(c)
	}
}

func TestFilesExportWritesToAReleasedPathAndReturnsMetadataOnly(t *testing.T) {
	var calls []call
	content := []byte("PK zip content")
	serve(t, &calls, exportHandler(content))
	dir := t.TempDir()
	target := filepath.Join(dir, "out.penpot")
	red := &redact.Redactor{}
	raw := json.RawMessage(`{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `","local_path":"` + target + `"}`)
	result, err := invokeFilesExport(context.Background(), resolvedFor([]string{"team/" + teamA}, "", dir), resolver(red, nil), red, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(*FileExported); got.Size != int64(len(content)) || got.SHA256 != sumHex(content) || got.FileID != fileA1 {
		t.Errorf("result = %+v", got)
	}
	if data, _ := os.ReadFile(target); !bytes.Equal(data, content) {
		t.Errorf("file = %q", data)
	}
	if got := strings.Join(commands(calls[:3]), ","); got != cmdProjects+","+cmdProjectFile+","+cmdExportFile {
		t.Errorf("commands = %s", got)
	}
	command := calls[2]
	if command.body["file-id"] != fileA1 || command.body["include-libraries"] != false || command.body["embed-assets"] != false || len(command.body) != 3 {
		t.Errorf("body = %v", command.body)
	}
	download := calls[3]
	if download.method != http.MethodGet || download.host != apiHost || download.path != exportPath+exportObj || download.auth != "" {
		t.Errorf("download = %+v (the host must be the connection's own and no token may be sent)", download)
	}
	if len(calls) != 4 {
		t.Errorf("calls = %v", commands(calls))
	}

	// An existing file is replaced only with confirmation, and nothing is requested before the refusal.
	calls = nil
	if _, err := invokeFilesExport(context.Background(), resolvedFor([]string{"team/" + teamA}, "", dir), resolver(red, nil), red, raw); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Fatalf("unconfirmed overwrite: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("an unconfirmed overwrite sent %v", commands(calls))
	}
	if _, err := invokeFilesExport(capability.WithConfirmed(context.Background()), resolvedFor([]string{"team/" + teamA}, "", dir), resolver(red, nil), red, raw); err != nil {
		t.Fatal(err)
	}
}

func TestFilesExportRefusesPathsOutsideTheReleaseBeforeAnyIO(t *testing.T) {
	var calls []call
	serve(t, &calls, exportHandler([]byte("x")))
	released, other := t.TempDir(), t.TempDir()
	red := &redact.Redactor{}
	reads := 0
	for name, resolved := range map[string]*config.Resolved{
		"outside": resolvedFor([]string{"team/" + teamA}, "", released),
		"readdir": resolvedFor([]string{"team/" + teamA}, other, ""),
		"none":    resolvedFor([]string{"team/" + teamA}, "", ""),
	} {
		raw := json.RawMessage(`{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `","local_path":"` + filepath.Join(other, "a.penpot") + `"}`)
		_, err := invokeFilesExport(context.Background(), resolved, resolver(red, &reads), red, raw)
		if err == nil || strings.Contains(err.Error(), other) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(calls) != 0 || reads != 0 {
		t.Errorf("calls = %v, reads = %d", commands(calls), reads)
	}
}

func TestFilesExportRejectsUnusableAnswers(t *testing.T) {
	for name, handler := range map[string]func(call) (*http.Response, error){
		"no id": func(c call) (*http.Response, error) {
			if c.command() == cmdExportFile {
				return sseResponse(200, "event: end\ndata: \"https://x.invalid/other\"\n\n"), nil
			}
			return filesHandler(0)(c)
		},
		"error event": func(c call) (*http.Response, error) {
			if c.command() == cmdExportFile {
				return sseResponse(200, "event: error\ndata: {\"hint\":\""+bodyCanary+"\"}\n\n"), nil
			}
			return filesHandler(0)(c)
		},
		"download fails": func(c call) (*http.Response, error) {
			if c.path == exportPath+exportObj {
				return jsonResponse(404, bodyCanary), nil
			}
			return exportHandler(nil)(c)
		},
		"size mismatch": func(c call) (*http.Response, error) {
			if c.path == exportPath+exportObj {
				response := jsonResponse(200, "short")
				response.ContentLength = 99
				return response, nil
			}
			return exportHandler(nil)(c)
		},
	} {
		var calls []call
		serve(t, &calls, handler)
		dir := t.TempDir()
		red := &redact.Redactor{}
		_, err := invokeFilesExport(context.Background(), resolvedFor([]string{"team/" + teamA}, "", dir), resolver(red, nil), red,
			json.RawMessage(`{"project_id":"`+projectA1+`","file_id":"`+fileA1+`","local_path":"`+filepath.Join(dir, "o.penpot")+`"}`))
		if err == nil || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s: left %d entries", name, len(entries))
		}
	}
}

func importHandler(answer func() *http.Response) func(call) (*http.Response, error) {
	files := filesHandler(0)
	return func(c call) (*http.Response, error) {
		if c.command() == cmdImportFile {
			return answer(), nil
		}
		return files(c)
	}
}

func TestFilesImportSendsOneMultipartRequestFromAReleasedPath(t *testing.T) {
	var calls []call
	serve(t, &calls, importHandler(func() *http.Response {
		return sseResponse(200, "event: progress\ndata: {}\n\nevent: end\ndata: {\"~#set\":[\"~u"+newFile+"\"]}\n\n")
	}))
	dir := t.TempDir()
	content := []byte("PK archive bytes")
	if err := os.WriteFile(filepath.Join(dir, "a.penpot"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	red := &redact.Redactor{}
	raw := json.RawMessage(`{"project_id":"` + projectA1 + `","name":"Importiert","local_path":"` + filepath.Join(dir, "a.penpot") + `"}`)
	result, err := invokeFilesImport(context.Background(), resolvedFor([]string{"team/" + teamA}, dir, ""), resolver(red, nil), red, raw)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*FileImported)
	if !got.Imported || len(got.FileIDs) != 1 || got.FileIDs[0] != newFile || got.Size != int64(len(content)) || got.SHA256 != sumHex(content) {
		t.Errorf("result = %+v", got)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdImportFile {
		t.Fatalf("commands = %s", got)
	}
	fields, headers, file := parts(t, calls[1])
	if fields["project-id"] != projectA1 || fields["name"] != "Importiert" || headers["part"] != "file" || !bytes.Equal(file, content) || len(fields) != 2 {
		t.Errorf("fields = %v, headers = %v", fields, headers)
	}
}

func TestFilesImportRefusesPathsOutsideTheReleaseBeforeAnyIO(t *testing.T) {
	var calls []call
	serve(t, &calls, importHandler(func() *http.Response { return sseResponse(200, "") }))
	released, other := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "secret.penpot"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	red := &redact.Redactor{}
	reads := 0
	for name, path := range map[string]string{"outside": filepath.Join(other, "secret.penpot"), "dotdot": released + "/../x"} {
		_, err := invokeFilesImport(context.Background(), resolvedFor([]string{"team/" + teamA}, released, ""), resolver(red, &reads), red,
			json.RawMessage(`{"project_id":"`+projectA1+`","name":"x","local_path":"`+path+`"}`))
		var pathErr *localfile.PathError
		if !errors.As(err, &pathErr) || strings.Contains(err.Error(), other) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Without a released directory no file is read at all, and an empty archive is refused.
	if _, err := invokeFilesImport(context.Background(), resolvedFor([]string{"team/" + teamA}, "", ""), resolver(red, &reads), red,
		json.RawMessage(`{"project_id":"`+projectA1+`","name":"x","local_path":"`+filepath.Join(other, "secret.penpot")+`"}`)); err == nil {
		t.Error("an import without a released directory succeeded")
	}
	if err := os.WriteFile(filepath.Join(released, "empty.penpot"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeFilesImport(context.Background(), resolvedFor([]string{"team/" + teamA}, released, ""), resolver(red, &reads), red,
		json.RawMessage(`{"project_id":"`+projectA1+`","name":"x","local_path":"`+filepath.Join(released, "empty.penpot")+`"}`)); err == nil {
		t.Error("an empty archive was imported")
	}
	if len(calls) != 0 || reads != 0 {
		t.Errorf("calls = %v, reads = %d", commands(calls), reads)
	}
}

func TestFilesImportSendsOnceAndReportsUncertainty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.penpot"), []byte("PK"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"project_id":"` + projectA1 + `","name":"x","local_path":"` + filepath.Join(dir, "a.penpot") + `"}`)
	for name, test := range map[string]struct {
		answer    func() (*http.Response, error)
		uncertain bool
	}{
		"5xx":        {func() (*http.Response, error) { return jsonResponse(500, bodyCanary), nil }, true},
		"reset":      {func() (*http.Response, error) { return nil, errResetByPeer }, true},
		"cut stream": {func() (*http.Response, error) { return sseResponse(200, "event: progress\ndata: {}\n\n"), nil }, true},
		"error":      {func() (*http.Response, error) { return sseResponse(200, "event: error\ndata: {}\n\n"), nil }, false},
		"forbidden":  {func() (*http.Response, error) { return jsonResponse(403, bodyCanary), nil }, false},
	} {
		var calls []call
		serve(t, &calls, func(c call) (*http.Response, error) {
			if c.command() == cmdImportFile {
				return test.answer()
			}
			return filesHandler(0)(c)
		})
		red := &redact.Redactor{}
		_, err := invokeFilesImport(context.Background(), resolvedFor([]string{"team/" + teamA}, dir, ""), resolver(red, nil), red, raw)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "may have taken effect") != test.uncertain ||
			len(changes(calls)) != 1 {
			t.Errorf("%s: err = %v, changes = %d", name, err, len(changes(calls)))
		}
	}
}

func TestTransferToolDescriptors(t *testing.T) {
	for _, test := range []struct {
		d       capability.Descriptor
		effect  capability.Effect
		idem    capability.Idempotency
		confirm capability.Confirmation
		files   config.LocalFiles
	}{
		{mediaUpload, capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired, config.LocalFilesRead},
		{mediaFromURL, capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired, ""},
		{filesExport, capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone, config.LocalFilesWrite},
		{filesImport, capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired, config.LocalFilesRead},
	} {
		risk := test.d.Risk
		if risk.Effect != test.effect || risk.Idempotency != test.idem || risk.Confirmation != test.confirm || !risk.OpenWorld ||
			risk.DataSensitivity == "" || test.d.LocalFiles != test.files || test.d.RequiresToolAllowList {
			t.Errorf("%s: %+v", test.d.ID, test.d)
		}
	}
	if !isCommand(cmdExportFile) || isChange(cmdExportFile) || !isChange(cmdUploadMedia) || !isChange(cmdMediaFromURL) || !isChange(cmdImportFile) {
		t.Error("the transfer commands are classified wrongly")
	}
	var providerErr *provider.Error
	if _, err := (&Client{}).sendChange(context.Background(), "x", cmdExportFile, nil, 0, "", 0); !errors.As(err, &providerErr) {
		t.Error("a read must not be sent as a change")
	}
}

func TestTransferToolsThroughTheCoreNeedReleasedDirectories(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, exportHandler([]byte("zip")))
	target := filepath.Join(env.write, "o.penpot")
	args := `{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `","local_path":"` + target + `"}`
	if _, err := env.invoke(filesExport.ID, "write", args); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := env.invoke(filesExport.ID, "write", args); err == nil || application.ErrorCode(err) != "confirmation-required" {
		t.Fatalf("export over an existing file: %v", err)
	}
	// A connection without released directories is not offered the file tools.
	bare := testConfig()
	env.core = application.New(registry(t), bare, resolver(env.red, env.reads), env.red)
	if _, err := env.invoke(filesExport.ID, "write", args); err == nil {
		t.Error("export was offered without a released directory")
	}
}
