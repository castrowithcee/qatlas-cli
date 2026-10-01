package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// A connection names the directories it releases, per direction and only when set, in describe and in the
// connection list; a connection without them publishes no files key. A tool with local file access is
// offered only by a connection that releases a directory of its direction.
func TestDiscoveryNamesReleasedDirectories(t *testing.T) {
	core, _ := testCore(t, []string{"plain", "reader", "both"}, nil, true)
	upload := testDescriptor("fake.files.upload", capability.EffectRead, capability.ConfirmationNone)
	upload.LocalFiles = config.LocalFilesRead
	if err := core.registry.Register("fake", capability.Operation{Descriptor: upload,
		Handler: func(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
			_ json.RawMessage) (any, error) {
			return nil, nil
		}}); err != nil {
		t.Fatal(err)
	}
	for name, files := range map[string]config.Files{
		"reader": {Read: []string{"/srv/in"}},
		"both":   {Read: []string{"/srv/in", "~/more"}, Write: []string{"/srv/out"}},
	} {
		connection := core.all.Connections[name]
		connection.Files = files
		core.all.Connections[name] = connection
	}

	described, err := core.Describe(DescribeRequest{Operation: "fake.files.upload"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*FilesRef{}
	for _, ref := range described.Connections {
		got[ref.Name] = ref.Files
	}
	want := map[string]*FilesRef{
		"reader": {Read: []string{"/srv/in"}},
		"both":   {Read: []string{"/srv/in", "~/more"}, Write: []string{"/srv/out"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("describe connections = %v, want only the readers %v", got, want)
	}
	if _, err := core.Describe(DescribeRequest{Operation: "fake.files.upload", Connection: "plain"}); err == nil {
		t.Error("describe through a connection without files succeeded")
	}

	summaries := map[string]*FilesRef{}
	for _, summary := range core.Connections("", nil).Connections {
		summaries[summary.Name] = summary.Files
	}
	wantSummaries := map[string]*FilesRef{"plain": nil, "reader": want["reader"], "both": want["both"]}
	if !reflect.DeepEqual(summaries, wantSummaries) {
		t.Errorf("connections = %v, want %v", summaries, wantSummaries)
	}
	var plain map[string]any
	for _, summary := range core.Connections("", nil).Connections {
		if summary.Name == "plain" {
			data, _ := json.Marshal(summary)
			_ = json.Unmarshal(data, &plain)
		}
	}
	if _, ok := plain["files"]; ok {
		t.Errorf("a connection without files publishes a files key: %v", plain)
	}
}

// downloadTool registers a download tool that writes the local file named by local_path, and returns the
// core with a connection releasing a directory named after a canary for writing, plus the file's path.
func downloadTool(t *testing.T, confirmation capability.Confirmation, body func(*localfile.Download) error) (
	*Core, *bytes.Buffer, string) {
	t.Helper()
	core, _ := testCore(t, []string{"files"}, nil, true)
	descriptor := testDescriptor("fake.files.download", capability.EffectRead, confirmation)
	descriptor.LocalFiles = config.LocalFilesWrite
	descriptor.InputSchema = json.RawMessage(
		`{"type":"object","properties":{"local_path":{"type":"string"}},"required":["local_path"],"additionalProperties":false}`)
	handler := func(ctx context.Context, resolved *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
		arguments json.RawMessage) (any, error) {
		var input struct {
			LocalPath string `json:"local_path"`
		}
		if err := json.Unmarshal(arguments, &input); err != nil {
			return nil, err
		}
		download, err := localfile.CreateForDownload(ctx, resolved, input.LocalPath)
		if err != nil {
			return nil, err
		}
		if err := body(download); err != nil {
			_ = download.Abort()
			return nil, err
		}
		if err := download.Commit(); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	}
	if err := core.registry.Register("fake", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "canary-kunde-verzeichnis")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	connection := core.all.Connections["files"]
	connection.Files = config.Files{Write: []string{dir}}
	core.all.Connections["files"] = connection
	var audit bytes.Buffer
	core.SetAudit(&audit)
	return core, &audit, dir
}

func writeNew(download *localfile.Download) error {
	_, err := download.Write([]byte("new"))
	return err
}

func invokeDownload(core *Core, path string, confirmed bool) error {
	_, err := core.Invoke(context.Background(), InvokeRequest{
		Operation: "fake.files.download", Connection: "files", Confirmed: confirmed,
		Arguments: json.RawMessage(`{"local_path":` + strconv.Quote(path) + `}`),
	})
	return err
}

func TestLocalFileOverwriteNeedsTheRequestConfirmation(t *testing.T) {
	logs := t.TempDir()
	core, audit, dir := downloadTool(t, capability.ConfirmationNone, writeNew)
	core.SetInvokeLog(invokelog.New(logs, 90), "cli", nil)
	target := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := invokeDownload(core, target, false)
	if ErrorCode(err) != output.CodeConfirmationRequired {
		t.Fatalf("unconfirmed overwrite = %v (%s), want confirmation-required", err, ErrorCode(err))
	}
	if !strings.Contains(err.Error(), "existing local file") || strings.Contains(err.Error(), "canary") {
		t.Errorf("message = %q", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("file = %q after refusal, want it unchanged", got)
	}
	if audit.Len() != 0 {
		t.Errorf("audit after refusal = %q", audit)
	}

	if err := invokeDownload(core, target, true); err != nil {
		t.Fatalf("confirmed overwrite = %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("file = %q, want it replaced", got)
	}
	lines := strings.Split(strings.TrimSpace(audit.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit = %q, want exactly one event", audit)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event["event"] != "local-file-replaced" || event["operation"] != "fake.files.download" ||
		event["connection"] != "files" || event["request_id"] == "" || event["time"] == nil || len(event) != 5 {
		t.Errorf("audit event = %v", event)
	}

	// A new file is no replacement, confirmed or not.
	audit.Reset()
	if err := invokeDownload(core, filepath.Join(dir, "fresh.txt"), true); err != nil || audit.Len() != 0 {
		t.Errorf("new file: err = %v, audit = %q", err, audit)
	}

	// Neither the audit nor the invocation log names a path.
	if err := invokeDownload(core, filepath.Join(dir, "..", "x"), true); ErrorCode(err) != output.CodeInvalidRequest {
		t.Fatalf("path violation = %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(logs, "logs", "*"))
	for _, name := range files {
		data, _ := os.ReadFile(name)
		if strings.Contains(string(data), "canary") || strings.Contains(string(data), "out.txt") {
			t.Errorf("invocation log %s names a path: %s", name, data)
		}
	}
	if strings.Contains(audit.String(), "canary") || strings.Contains(audit.String(), "out.txt") {
		t.Errorf("audit names a path: %s", audit)
	}
}

func TestLocalFileErrorsMapToCodes(t *testing.T) {
	core, _, dir := downloadTool(t, capability.ConfirmationNone, func(d *localfile.Download) error {
		return d.ExpectSize(5) // the tool writes nothing, so Commit finds it shorter
	})
	err := invokeDownload(core, filepath.Join(dir, "short.txt"), true)
	if ErrorCode(err) != output.CodeInvalidProviderResult {
		t.Errorf("integrity error = %v (%s), want invalid-provider-response", err, ErrorCode(err))
	}
	if _, statErr := os.Stat(filepath.Join(dir, "short.txt")); statErr == nil {
		t.Error("a file exists after an integrity error")
	}

	err = invokeDownload(core, filepath.Join(dir, "..", "escape.txt"), true)
	if ErrorCode(err) != output.CodeInvalidRequest {
		t.Fatalf("path violation = %v (%s), want invalid-request", err, ErrorCode(err))
	}
	if !strings.Contains(err.Error(), "local_path") || strings.Contains(err.Error(), "canary") ||
		strings.Contains(err.Error(), "escape.txt") {
		t.Errorf("message = %q", err)
	}
}

func TestConfirmationRequiredToolStaysRefusedBeforeTheHandler(t *testing.T) {
	core, audit, dir := downloadTool(t, capability.ConfirmationRequired, writeNew)
	target := filepath.Join(dir, "out.txt")
	if err := invokeDownload(core, target, false); ErrorCode(err) != output.CodeConfirmationRequired {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("the handler ran without confirmation")
	}
	if audit.Len() != 0 {
		t.Errorf("audit = %q", audit)
	}
}
