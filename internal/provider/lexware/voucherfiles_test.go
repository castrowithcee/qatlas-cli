package lexware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
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
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const uploadFileID = "7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

type uploadRequest struct {
	method, path, accept string
	fields               map[string]string
	filename, partType   string
	content              []byte
}

func uploadCore(t *testing.T, dir string) (*application.Core, *atomic.Int32) {
	t.Helper()
	stubLimiter(t, primaryKey)
	cfg := coreConfig()
	cfg.Connections["lexware-uploader"] = config.Connection{Service: "lexware", Credential: "primary-key",
		Effects: []config.Effect{config.EffectRead, config.EffectCreate},
		Files:   config.Files{Read: []string{dir}}}
	var reads atomic.Int32
	red := &redact.Redactor{}
	counting := secret.NewWith(func(string) string { reads.Add(1); return primaryKey }, nil, nil, red)
	return application.New(registry(t), cfg, counting, red), &reads
}

// serveUploads records every multipart request and answers with reply.
func serveUploads(t *testing.T, reply func() (*http.Response, error)) *[]uploadRequest {
	t.Helper()
	var requests []uploadRequest
	serve(t, func(r *http.Request) (*http.Response, error) {
		got := uploadRequest{method: r.Method, path: r.URL.Path, accept: r.Header.Get("Accept"), fields: map[string]string{}}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		} else {
			reader := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				data, _ := io.ReadAll(part)
				if part.FormName() == "file" {
					got.filename, got.partType, got.content = part.FileName(), part.Header.Get("Content-Type"), data
				} else {
					got.fields[part.FormName()] = string(data)
				}
			}
		}
		requests = append(requests, got)
		return reply()
	})
	return &requests
}

func invokeUpload(core *application.Core, operation, args string, confirmed bool) (string, error) {
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "lexware-uploader", Arguments: json.RawMessage(args), Confirmed: confirmed})
	return string(response.Result), err
}

func writeReceipt(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func localArgs(path string) string { return `{"local_path":` + strconv.Quote(path) + `}` }

func attachArgs(id, path string) string {
	return `{"id":"` + id + `","local_path":` + strconv.Quote(path) + `}`
}

func TestVoucherFileDescriptors(t *testing.T) {
	for _, d := range []capability.Descriptor{filesUpload, vouchersAttach} {
		if d.Risk.Effect != capability.EffectCreate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != bookkeepingSensitivity || d.LocalFiles != config.LocalFilesRead {
			t.Errorf("%s = %+v", d.ID, d)
		}
	}
	for _, want := range []string{"checksum", "blank", "Zu prüfen"} {
		if !strings.Contains(filesUpload.Description, want) {
			t.Errorf("upload description lacks %q", want)
		}
	}
	if !strings.Contains(vouchersAttach.Description, "checksum") {
		t.Error("attach description lacks the duplicate detection")
	}
}

func TestFilesUploadSendsOneMultipartWithFixedType(t *testing.T) {
	dir := t.TempDir()
	content := []byte("%PDF-1.4\x00\xff binary bytes")
	path := writeReceipt(t, dir, "Hotel.PDF", content)
	requests := serveUploads(t, func() (*http.Response, error) {
		return jsonResponse(http.StatusAccepted, `{"id":"`+uploadFileID+`","voucherId":"`+voucherID+`","x":"y"}`), nil
	})
	core, _ := uploadCore(t, dir)
	result, err := invokeUpload(core, "lexware.files.upload", localArgs(path), true)
	if err != nil {
		t.Fatal(err)
	}
	var out VoucherFile
	if json.Unmarshal([]byte(result), &out) != nil || out != (VoucherFile{FileID: uploadFileID, VoucherID: voucherID}) {
		t.Errorf("result = %s", result)
	}
	if len(*requests) != 1 {
		t.Fatalf("requests = %+v", *requests)
	}
	got := (*requests)[0]
	if got.method != http.MethodPost || got.path != "/v1/files" || got.fields["type"] != "voucher" || len(got.fields) != 1 ||
		got.filename != "Hotel.PDF" || got.partType != "application/pdf" || !bytes.Equal(got.content, content) {
		t.Errorf("request = %+v", got)
	}
}

func TestVouchersAttachSendsOneMultipartToTheVoucher(t *testing.T) {
	dir := t.TempDir()
	content := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}
	path := writeReceipt(t, dir, "scan.png", content)
	requests := serveUploads(t, func() (*http.Response, error) {
		return jsonResponse(http.StatusAccepted, `{"id":"`+uploadFileID+`"}`), nil
	})
	core, _ := uploadCore(t, dir)
	result, err := invokeUpload(core, "lexware.vouchers.attach", attachArgs(voucherID, path), true)
	if err != nil {
		t.Fatal(err)
	}
	var out VoucherFile
	if json.Unmarshal([]byte(result), &out) != nil || out != (VoucherFile{FileID: uploadFileID, VoucherID: voucherID}) {
		t.Errorf("result = %s", result)
	}
	got := (*requests)[0]
	if len(*requests) != 1 || got.method != http.MethodPost || got.path != "/v1/vouchers/"+voucherID+"/files" ||
		len(got.fields) != 0 || got.partType != "image/png" || !bytes.Equal(got.content, content) {
		t.Errorf("request = %+v", *requests)
	}
}

func TestVoucherFileToolsRefuseLocallyWithoutRequest(t *testing.T) {
	refuse(t)
	dir, outside := t.TempDir(), t.TempDir()
	ok := writeReceipt(t, dir, "ok.pdf", []byte("x"))
	big := writeReceipt(t, dir, "big.pdf", make([]byte, maxVoucherFileBytes+1))
	empty := writeReceipt(t, dir, "empty.pdf", nil)
	exe := writeReceipt(t, dir, "tool.exe", []byte("x"))
	noExt := writeReceipt(t, dir, "receipt", []byte("x"))
	foreign := writeReceipt(t, outside, "foreign.pdf", []byte("x"))
	core, reads := uploadCore(t, dir)
	tests := []struct {
		name, operation, args string
		confirm               bool
	}{
		{"upload without confirm", "lexware.files.upload", localArgs(ok), false},
		{"attach without confirm", "lexware.vouchers.attach", attachArgs(voucherID, ok), false},
		{"upload extension", "lexware.files.upload", localArgs(exe), true},
		{"attach extension", "lexware.vouchers.attach", attachArgs(voucherID, noExt), true},
		{"upload too large", "lexware.files.upload", localArgs(big), true},
		{"attach too large", "lexware.vouchers.attach", attachArgs(voucherID, big), true},
		{"upload empty", "lexware.files.upload", localArgs(empty), true},
		{"upload outside release", "lexware.files.upload", localArgs(foreign), true},
		{"attach outside release", "lexware.vouchers.attach", attachArgs(voucherID, foreign), true},
		{"upload missing file", "lexware.files.upload", localArgs(filepath.Join(dir, "none.pdf")), true},
		{"upload directory", "lexware.files.upload", localArgs(dir), true},
		{"attach bad id", "lexware.vouchers.attach", attachArgs("../"+voucherID, ok), true},
		{"upload passthrough type", "lexware.files.upload", `{"local_path":` + strconv.Quote(ok) + `,"type":"other"}`, true},
		{"upload content argument", "lexware.files.upload", `{"content_base64":"eA=="}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := invokeUpload(core, tt.operation, tt.args, tt.confirm); err == nil {
				t.Fatal("the core accepted the request")
			}
		})
	}
	if reads.Load() != 0 {
		t.Errorf("secret reads = %d, want 0", reads.Load())
	}
}

func TestVoucherFileUploadIsUncertainAfterUnclearOutcomeAndNeverRetried(t *testing.T) {
	cases := []struct {
		name  string
		reply func() (*http.Response, error)
	}{
		{"timeout", func() (*http.Response, error) { return nil, context.DeadlineExceeded }},
		{"502", func() (*http.Response, error) { return jsonResponse(http.StatusBadGateway, `{}`), nil }},
		{"202 without ids", func() (*http.Response, error) { return jsonResponse(http.StatusAccepted, `{"id":"x"}`), nil }},
	}
	for _, tool := range []string{"lexware.files.upload", "lexware.vouchers.attach"} {
		for _, tt := range cases {
			t.Run(tool+"/"+tt.name, func(t *testing.T) {
				dir := t.TempDir()
				path := writeReceipt(t, dir, "a.xml", []byte("<a/>"))
				requests := serveUploads(t, tt.reply)
				core, _ := uploadCore(t, dir)
				args := localArgs(path)
				if tool == "lexware.vouchers.attach" {
					args = attachArgs(voucherID, path)
				}
				_, err := invokeUpload(core, tool, args, true)
				if err == nil || !strings.Contains(err.Error(), "may have been") {
					t.Fatalf("err = %v, want an uncertainty hint", err)
				}
				if len(*requests) != 1 {
					t.Errorf("requests = %d, want exactly 1", len(*requests))
				}
			})
		}
	}
}

func TestVoucherFileRejectsAnAnswerForAnotherVoucher(t *testing.T) {
	dir := t.TempDir()
	path := writeReceipt(t, dir, "a.jpeg", []byte("jpg"))
	serveUploads(t, func() (*http.Response, error) {
		return jsonResponse(http.StatusAccepted, `{"id":"`+uploadFileID+`","voucherId":"`+uploadFileID+`"}`), nil
	})
	core, _ := uploadCore(t, dir)
	_, err := invokeUpload(core, "lexware.vouchers.attach", attachArgs(voucherID, path), true)
	if err == nil || classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestVoucherFile406IsAFixedMessage(t *testing.T) {
	dir := t.TempDir()
	path := writeReceipt(t, dir, "a.pdf", []byte("pdf"))
	serveUploads(t, func() (*http.Response, error) {
		return jsonResponse(http.StatusNotAcceptable, `{"message":"SECRET-PROVIDER-TEXT"}`), nil
	})
	core, _ := uploadCore(t, dir)
	for _, tool := range []string{"lexware.files.upload", "lexware.vouchers.attach"} {
		args := localArgs(path)
		if tool == "lexware.vouchers.attach" {
			args = attachArgs(voucherID, path)
		}
		_, err := invokeUpload(core, tool, args, true)
		if err == nil || !strings.Contains(err.Error(), fileFormatMessage) || strings.Contains(err.Error(), "SECRET") ||
			strings.Contains(err.Error(), "may have been") {
			t.Errorf("%s err = %v", tool, err)
		}
	}
}
