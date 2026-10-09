package lexware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// downloadCore is the core with a connection that releases dir for writing next to the plain one.
func downloadCore(t *testing.T, dir string) (*application.Core, *atomic.Int32) {
	t.Helper()
	stubLimiter(t, primaryKey)
	cfg := coreConfig()
	connection := cfg.Connections["lexware-primary"]
	connection.Files = config.Files{Write: []string{dir}}
	cfg.Connections["lexware-files"] = connection
	var reads atomic.Int32
	red := &redact.Redactor{}
	counting := secret.NewWith(func(string) string { reads.Add(1); return primaryKey }, nil, nil, red)
	return application.New(registry(t), cfg, counting, red), &reads
}

func invokeDownloadTool(core *application.Core, operation, connection, args string, confirmed bool) (string, error) {
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(args), Confirmed: confirmed})
	return string(response.Result), err
}

func fileResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body))}
}

func documentArgs(voucherType, format, path string) string {
	args := `{"voucher_type":"` + voucherType + `","id":"` + voucherID + `","local_path":` + strconv.Quote(path)
	if format != "" {
		args += `,"format":"` + format + `"`
	}
	return args + `}`
}

func TestDownloadDescriptorsDeclareLocalWriteAndBookkeepingData(t *testing.T) {
	for _, d := range []capability.Descriptor{documentsDownload, filesDownload} {
		if d.Risk.Effect != capability.EffectRead || d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld ||
			d.LocalFiles != config.LocalFilesWrite || d.RequiresToolAllowList {
			t.Errorf("%s = %+v", d.ID, d)
		}
	}
	if documentsDownload.Risk.DataSensitivity != dataSensitivity || filesDownload.Risk.DataSensitivity != bookkeepingSensitivity {
		t.Error("data classes differ from the voucher read tools")
	}
	if !strings.Contains(documentsDownload.Description, "only as a preview, which is not a valid e-invoice") {
		t.Errorf("description = %q", documentsDownload.Description)
	}
	cfg := coreConfig()
	if cfg.ConnectionAllows("lexware-primary", documentsDownload.Tool()) ||
		cfg.ConnectionAllows("lexware-primary", filesDownload.Tool()) {
		t.Error("a connection without files.write was offered a download tool")
	}
	if !cfg.ConnectionAllows("lexware-primary", vouchersGet.Tool()) {
		t.Error("vouchers.get is no longer offered without files.write")
	}
}

func TestDocumentDownloadUsesAFixedPathAndAcceptPerSelection(t *testing.T) {
	paths := map[string]string{"invoice": "/v1/invoices/", "credit_note": "/v1/credit-notes/",
		"delivery_note": "/v1/delivery-notes/", "dunning": "/v1/dunnings/",
		"down_payment_invoice": "/v1/down-payment-invoices/", "order_confirmation": "/v1/order-confirmations/",
		"quotation": "/v1/quotations/"}
	accepts := map[string]string{"": "*/*", "default": "*/*", "pdf": "application/pdf", "xml": "application/xml"}
	for voucherType, prefix := range paths {
		for format, accept := range accepts {
			dir := t.TempDir()
			var gotPath, gotAccept string
			serve(t, func(r *http.Request) (*http.Response, error) {
				gotPath, gotAccept = r.URL.Path, r.Header.Get("Accept")
				return fileResponse(http.StatusOK, "document"), nil
			})
			core, _ := downloadCore(t, dir)
			target := filepath.Join(dir, "out")
			result, err := invokeDownloadTool(core, documentsDownload.ID, "lexware-files", documentArgs(voucherType, format, target), false)
			if err != nil {
				t.Fatalf("%s/%s = %v", voucherType, format, err)
			}
			if gotPath != prefix+voucherID+"" || gotAccept != accept {
				t.Errorf("%s/%s: %s with Accept %q, want %s with %q", voucherType, format, gotPath, gotAccept, prefix+voucherID, accept)
			}
			sum := sha256.Sum256([]byte("document"))
			var out DownloadResult
			if err := json.Unmarshal([]byte(result), &out); err != nil ||
				out != (DownloadResult{ID: voucherID, Size: 8, SHA256: hex.EncodeToString(sum[:])}) {
				t.Errorf("result = %s, %v", result, err)
			}
			if got, _ := os.ReadFile(target); string(got) != "document" {
				t.Errorf("file = %q", got)
			}
		}
	}
}

func TestFileDownloadUsesTheFilesPathAndAcceptPerFormat(t *testing.T) {
	for format, accept := range map[string]string{"": "*/*", "default": "*/*", "pdf": "application/pdf",
		"xml": "application/xml", "image": "image/png"} {
		dir := t.TempDir()
		var gotPath, gotAccept string
		serve(t, func(r *http.Request) (*http.Response, error) {
			gotPath, gotAccept = r.URL.Path, r.Header.Get("Accept")
			return fileResponse(http.StatusOK, "receipt"), nil
		})
		core, _ := downloadCore(t, dir)
		args := `{"id":"` + voucherID + `","local_path":` + strconv.Quote(filepath.Join(dir, "r"))
		if format != "" {
			args += `,"format":"` + format + `"`
		}
		if _, err := invokeDownloadTool(core, filesDownload.ID, "lexware-files", args+"}", false); err != nil {
			t.Fatalf("%s = %v", format, err)
		}
		if gotPath != "/v1/files/"+voucherID || gotAccept != accept {
			t.Errorf("%s: %s with Accept %q", format, gotPath, gotAccept)
		}
	}
}

func TestDownloadNeedsConfirmationToReplaceAndKeepsTheFileOtherwise(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	refuse(t)
	core, _ := downloadCore(t, dir)
	if _, err := invokeDownloadTool(core, documentsDownload.ID, "lexware-files", documentArgs("invoice", "", target), false); application.ErrorCode(err) != "confirmation-required" {
		t.Fatalf("err = %v", err)
	}
	serve(t, func(*http.Request) (*http.Response, error) { return fileResponse(http.StatusOK, "new"), nil })
	if _, err := invokeDownloadTool(core, documentsDownload.ID, "lexware-files", documentArgs("invoice", "", target), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Errorf("file = %q", got)
	}
}

func TestDownloadRefusesBeforeSecretAccessAndProviderIO(t *testing.T) {
	released, other := t.TempDir(), t.TempDir()
	refuse(t)
	core, reads := downloadCore(t, released)
	okPath := filepath.Join(released, "out")
	cases := map[string]string{
		"outside the release": documentArgs("invoice", "", filepath.Join(other, "x")),
		"dotdot":              documentArgs("invoice", "", released+"/../x"),
		"missing parent":      documentArgs("invoice", "", filepath.Join(released, "none", "x")),
		"id is no UUID":       `{"voucher_type":"invoice","id":"../v1/profile","local_path":` + strconv.Quote(okPath) + `}`,
		"unknown type":        documentArgs("voucher", "", okPath),
		"unknown format":      documentArgs("invoice", "image", okPath),
		"free argument":       `{"voucher_type":"invoice","id":"` + voucherID + `","accept":"*/*","local_path":` + strconv.Quote(okPath) + `}`,
	}
	for name, args := range cases {
		_, err := invokeDownloadTool(core, documentsDownload.ID, "lexware-files", args, false)
		if err == nil || strings.Contains(err.Error(), other) || strings.Contains(err.Error(), released) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := invokeDownloadTool(core, filesDownload.ID, "lexware-files",
		`{"id":"nope","local_path":`+strconv.Quote(okPath)+`}`, false); err == nil {
		t.Error("files.download accepted an id that is no UUID")
	}
	if _, err := invokeDownloadTool(core, filesDownload.ID, "lexware-files",
		`{"id":"`+voucherID+`","local_path":`+strconv.Quote(filepath.Join(other, "x"))+`}`, false); err == nil {
		t.Error("files.download accepted a path outside the release")
	}
	if reads.Load() != 0 {
		t.Errorf("secret reads = %d, want none", reads.Load())
	}
	if _, err := invokeDownloadTool(core, documentsDownload.ID, "lexware-primary", documentArgs("invoice", "", okPath), false); err == nil {
		t.Error("a connection without files.write ran the tool")
	}
}

func TestDownloadLeavesNoPartialFile(t *testing.T) {
	cases := map[string]*http.Response{
		"reported too large":    {StatusCode: 200, ContentLength: maxDownloadBytes + 1, Body: io.NopCloser(strings.NewReader("x"))},
		"longer than reported":  {StatusCode: 200, ContentLength: 3, Body: io.NopCloser(strings.NewReader("longer"))},
		"shorter than reported": {StatusCode: 200, ContentLength: 100, Body: io.NopCloser(strings.NewReader("short"))},
		"unreported and too large": {StatusCode: 200, ContentLength: -1,
			Body: io.NopCloser(io.LimitReader(zeroReader{}, maxDownloadBytes+1))},
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			serve(t, func(*http.Request) (*http.Response, error) { return response, nil })
			core, _ := downloadCore(t, dir)
			for _, call := range []struct{ operation, args string }{
				{documentsDownload.ID, documentArgs("invoice", "", filepath.Join(dir, "out"))},
				{filesDownload.ID, `{"id":"` + voucherID + `","local_path":` + strconv.Quote(filepath.Join(dir, "out")) + `}`},
			} {
				if _, err := invokeDownloadTool(core, call.operation, "lexware-files", call.args, false); err == nil {
					t.Fatalf("%s succeeded", call.operation)
				}
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Fatalf("directory = %v, want no file", entries)
				}
				// the response body is consumed once
				response = &http.Response{StatusCode: response.StatusCode, ContentLength: response.ContentLength,
					Body: io.NopCloser(io.LimitReader(zeroReader{}, maxDownloadBytes+1))}
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestDownloadMapsStatusesToFixedMessages(t *testing.T) {
	dir := t.TempDir()
	core, _ := downloadCore(t, dir)
	run := func(operation, args string, status int) error {
		serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(status, bodyCanary), nil })
		_, err := invokeDownloadTool(core, operation, "lexware-files", args, false)
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("directory = %v, want no file", entries)
		}
		if err != nil && strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("error repeats provider text: %v", err)
		}
		return err
	}
	target := filepath.Join(dir, "out")
	fileArgs := `{"id":"` + voucherID + `","format":"xml","local_path":` + strconv.Quote(target) + `}`
	for _, status := range []int{http.StatusConflict, http.StatusNotAcceptable} {
		if err := run(documentsDownload.ID, documentArgs("invoice", "pdf", target), status); err == nil ||
			!strings.Contains(err.Error(), notFinalizedMessage) {
			t.Errorf("draft %d: err = %v", status, err)
		}
	}
	if err := run(documentsDownload.ID, documentArgs("invoice", "xml", target), http.StatusNotFound); err == nil ||
		!strings.Contains(err.Error(), noFormatMessage) {
		t.Errorf("format 404: err = %v", err)
	}
	if err := run(documentsDownload.ID, documentArgs("invoice", "", target), http.StatusNotFound); classOf(err) != "not-found" ||
		strings.Contains(err.Error(), noFormatMessage) {
		t.Errorf("default 404: err = %v", err)
	}
	if err := run(filesDownload.ID, fileArgs, http.StatusNotFound); err == nil || !strings.Contains(err.Error(), noFormatMessage) {
		t.Errorf("file format 404: err = %v", err)
	}
	if err := run(filesDownload.ID, fileArgs, http.StatusNotAcceptable); err == nil || !strings.Contains(err.Error(), noFormatMessage) {
		t.Errorf("file 406: err = %v", err)
	}
	if err := run(documentsDownload.ID, documentArgs("invoice", "", target), http.StatusForbidden); classOf(err) != "permission" {
		t.Errorf("403: err = %v", err)
	}
}

func TestDownloadIgnoresTheServerChosenFileName(t *testing.T) {
	dir := t.TempDir()
	serve(t, func(*http.Request) (*http.Response, error) {
		response := fileResponse(http.StatusOK, "x")
		response.Header = http.Header{"Content-Disposition": {`attachment; filename="../../escape.pdf"`}}
		return response, nil
	})
	core, _ := downloadCore(t, dir)
	target := filepath.Join(dir, "chosen")
	if _, err := invokeDownloadTool(core, documentsDownload.ID, "lexware-files", documentArgs("invoice", "", target), false); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "chosen" {
		t.Errorf("directory = %v", entries)
	}
}
