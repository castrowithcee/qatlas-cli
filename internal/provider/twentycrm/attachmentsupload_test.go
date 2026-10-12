package twentycrm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	uploadToken = "signed.upload-token.sample"
	uploadBody  = "offer content"
)

func uploadAddress(host, path, query string) string {
	return "https://" + host + path + "?" + query
}

func goodUploadURL() string {
	return uploadAddress("api.twenty.com", "/file-upload/"+fileUUID, "token="+uploadToken)
}

type uploadCall struct {
	Method, Path, Query, Authorization, ContentType string
	Length                                          int64
	Body                                            []byte
}

// uploadFake answers every step of an upload. A nil answer is the success answer of the step.
type uploadFake struct {
	record, create, put, complete, attach func(*http.Request) (*http.Response, error)
	calls                                 []uploadCall
}

func (f *uploadFake) count(method, path string) int {
	n := 0
	for _, call := range f.calls {
		if call.Method == method && strings.HasPrefix(call.Path, path) {
			n++
		}
	}
	return n
}

func (f *uploadFake) steps() string {
	var steps []string
	for _, call := range f.calls {
		steps = append(steps, call.Method+" "+call.Path)
	}
	return strings.Join(steps, "; ")
}

func createAnswer(address, contentType string) string {
	return `{"data":{"createFileUpload":{"fileId":"` + fileUUID + `","uploadUrl":"` + address +
		`","contentType":"` + contentType + `"}}}`
}

const (
	completeAnswer = `{"data":{"completeFileUpload":{"id":"` + fileUUID + `"}}}`
	attachAnswer   = `{"data":{"createAttachment":{"id":"` + attachID + `","targetPersonId":"` + attPersonID + `",` +
		`"file":[{"fileId":"` + fileUUID + `","label":"offer.pdf","extension":".pdf","url":""}]}}}`
)

func graphqlErrors(code string) string {
	return `{"errors":[{"message":"` + bodyCanary + ` ` + uploadToken + `","extensions":{"code":"` + code + `"}}],"data":null}`
}

func serveUpload(t *testing.T, f *uploadFake) {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, attachmentSchema), nil
		}
		var body []byte
		if request.Body != nil {
			body, _ = io.ReadAll(request.Body)
		}
		f.calls = append(f.calls, uploadCall{Method: request.Method, Path: request.URL.Path, Query: request.URL.RawQuery,
			Authorization: request.Header.Get("Authorization"), ContentType: request.Header.Get("Content-Type"),
			Length: request.ContentLength, Body: body})
		request.Body = io.NopCloser(bytes.NewReader(body))
		answer := func(step func(*http.Request) (*http.Response, error), ok string) (*http.Response, error) {
			if step != nil {
				return step(request)
			}
			return jsonResponse(http.StatusOK, ok), nil
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/rest/people/"+attPersonID:
			return answer(f.record, `{"data":{"person":{"id":"`+attPersonID+`","city":"Berlin"}}}`)
		case request.URL.Path == "/metadata" && bytes.Contains(body, []byte("createFileUpload(")):
			return answer(f.create, createAnswer(goodUploadURL(), uploadContentType))
		case request.URL.Path == "/metadata" && bytes.Contains(body, []byte("completeFileUpload(")):
			return answer(f.complete, completeAnswer)
		case request.Method == http.MethodPut:
			return answer(f.put, "")
		case request.Method == http.MethodPost && request.URL.Path == "/rest/attachments":
			return answer(f.attach, attachAnswer)
		}
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, cloudKey)
}

func status(code int) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return jsonResponse(code, `{"message":"`+bodyCanary+`"}`), nil
	}
}

func answerWith(body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil }
}

func fail(err error) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) { return nil, err }
}

func uploadConnection(dir string, targets ...string) *config.Resolved {
	resolved := targetConnection(targets...)
	if dir != "" {
		resolved.Files.Read = []string{dir}
	}
	return resolved
}

func inlineArgs(content string) string {
	return `{"object":"person","record_id":"` + attPersonID + `","name":"offer.pdf","content_base64":"` +
		base64.StdEncoding.EncodeToString([]byte(content)) + `"}`
}

func runUpload(t *testing.T, resolved *config.Resolved, args string) (*AttachmentUpload, *redact.Redactor, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := invokeAttachmentsUpload(t.Context(), resolved, resolver(red), red, json.RawMessage(args))
	if err != nil {
		return nil, red, err
	}
	return result.(*AttachmentUpload), red, nil
}

func TestAttachmentUploadDescriptor(t *testing.T) {
	d := attachmentsUpload
	want := recordWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent)
	if d.Risk != want || d.RequiresToolAllowList || d.LocalFiles != config.LocalFilesRead || d.Provider != Provider {
		t.Errorf("descriptor = %+v", d)
	}
	for _, profile := range []string{"read", "write"} {
		reg := capability.NewRegistry()
		if err := Register(reg); err != nil {
			t.Fatal(err)
		}
		metadata, _ := reg.ProviderMetadata(Provider)
		for _, p := range metadata.Profiles {
			for _, id := range p.Tools {
				if id == d.ID && p.ID == profile {
					t.Errorf("profile %s offers %s", profile, id)
				}
			}
		}
	}
}

func TestAttachmentUploadSendsFixedRequestsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "offer.pdf"), []byte(uploadBody), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]string{
		"inline": inlineArgs(uploadBody),
		"local":  `{"object":"person","record_id":"` + attPersonID + `","local_path":` + strconv.Quote(filepath.Join(dir, "offer.pdf")) + `}`,
	} {
		fake := &uploadFake{}
		serveUpload(t, fake)
		result, red, err := runUpload(t, uploadConnection(dir, "object/person"), args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if *result != (AttachmentUpload{ID: attachID, FileID: fileUUID, Label: "offer.pdf", Size: int64(len(uploadBody))}) {
			t.Errorf("%s: result = %+v", name, result)
		}
		if got := fake.steps(); got != "GET /rest/people/"+attPersonID+"; POST /metadata; PUT /file-upload/"+fileUUID+
			"; POST /metadata; POST /rest/attachments" {
			t.Fatalf("%s: requests = %s", name, got)
		}
		create, put, complete, attach := fake.calls[1], fake.calls[2], fake.calls[3], fake.calls[4]
		for _, call := range []uploadCall{create, complete, attach} {
			if call.Authorization != "Bearer "+cloudKey {
				t.Errorf("%s: %s lacks the key", name, call.Path)
			}
		}
		wantCreate := `{"query":"mutation CreateFileUpload($filename: String!, $size: Float!, $fileFolder: FileFolder!, ` +
			`$fieldMetadataUniversalIdentifier: String) { createFileUpload(filename: $filename, size: $size, ` +
			`fileFolder: $fileFolder, fieldMetadataUniversalIdentifier: $fieldMetadataUniversalIdentifier) { fileId ` +
			`uploadUrl contentType } }","variables":{"fieldMetadataUniversalIdentifier":"20202020-15db-460e-8166-c7b5d87ad4be",` +
			`"fileFolder":"FilesField","filename":"offer.pdf","size":` + strconv.Itoa(len(uploadBody)) + `}}`
		wantComplete := `{"query":"mutation CompleteFileUpload($fileId: String!) { completeFileUpload(fileId: $fileId) { id } }",` +
			`"variables":{"fileId":"` + fileUUID + `"}}`
		wantAttach := `{"file":[{"fileId":"` + fileUUID + `","label":"offer.pdf"}],"targetPersonId":"` + attPersonID + `"}`
		for _, c := range []struct{ got, want string }{{string(create.Body), wantCreate},
			{string(complete.Body), wantComplete}, {string(attach.Body), wantAttach}} {
			if c.got != c.want {
				t.Errorf("%s: body\n got %s\nwant %s", name, c.got, c.want)
			}
		}
		if attach.Query != "depth=0" {
			t.Errorf("%s: attach query = %q", name, attach.Query)
		}
		if put.Authorization != "" || put.ContentType != "application/octet-stream" || put.Query != "token="+uploadToken ||
			put.Length != int64(len(uploadBody)) || string(put.Body) != uploadBody {
			t.Errorf("%s: put = %+v", name, put)
		}
		if strings.Contains(red.Apply(goodUploadURL()+" "+uploadToken), uploadToken) {
			t.Errorf("%s: the upload token is not registered with the redactor", name)
		}
	}
}

func TestAttachmentUploadRefusesForeignUploadAddressesBeforeThePut(t *testing.T) {
	good := "/file-upload/" + fileUUID
	for name, c := range map[string]struct {
		answer, want string
	}{
		"foreign host":     {createAnswer(uploadAddress("storage.example.com", good, "token="+uploadToken), uploadContentType), errExternalStorage},
		"foreign port":     {createAnswer(uploadAddress("api.twenty.com:8443", good, "token="+uploadToken), uploadContentType), errExternalStorage},
		"http":             {createAnswer("http://api.twenty.com"+good+"?token="+uploadToken, uploadContentType), "outside the configured service"},
		"wrong path":       {createAnswer(uploadAddress("api.twenty.com", "/file/files-field/"+fileUUID, "token="+uploadToken), uploadContentType), "outside the configured service"},
		"other file id":    {createAnswer(uploadAddress("api.twenty.com", "/file-upload/"+attOtherID, "token="+uploadToken), uploadContentType), "outside the configured service"},
		"extra query":      {createAnswer(uploadAddress("api.twenty.com", good, strings.Join([]string{"token=" + uploadToken, "x=1"}, "&")), uploadContentType), "outside the configured service"},
		"no token":         {createAnswer(uploadAddress("api.twenty.com", good, "x=1"), uploadContentType), "outside the configured service"},
		"user info":        {createAnswer("https://"+url.UserPassword("u", "p").String()+"@api.twenty.com"+good+"?token="+uploadToken, uploadContentType), "outside the configured service"},
		"wrong type":       {createAnswer(goodUploadURL(), "application/pdf"), "unusable target"},
		"no file id":       {`{"data":{"createFileUpload":{"fileId":"x","uploadUrl":"` + goodUploadURL() + `","contentType":"application/octet-stream"}}}`, "unusable target"},
		"no target at all": {`{"data":{"createFileUpload":null}}`, "unusable target"},
	} {
		fake := &uploadFake{create: answerWith(c.answer)}
		serveUpload(t, fake)
		_, _, err := runUpload(t, uploadConnection("", "object/person"), inlineArgs(uploadBody))
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), uploadToken) {
			t.Errorf("%s: err = %v", name, err)
		}
		if fake.count(http.MethodPut, "") != 0 || fake.count(http.MethodPost, "/rest") != 0 || len(fake.calls) != 2 {
			t.Errorf("%s: requests = %s", name, fake.steps())
		}
	}
}

func TestAttachmentUploadDoesNotFollowARedirectOnThePut(t *testing.T) {
	fake := &uploadFake{put: func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusFound, "")
		response.Header.Set("Location", "https://storage.example.com/x?token="+uploadToken)
		return response, nil
	}}
	serveUpload(t, fake)
	_, _, err := runUpload(t, uploadConnection("", "object/person"), inlineArgs(uploadBody))
	if err == nil || !strings.Contains(err.Error(), errExternalStorage) || strings.Contains(err.Error(), uploadToken) {
		t.Fatalf("err = %v", err)
	}
	if fake.count(http.MethodPut, "") != 1 || fake.count(http.MethodPost, "/metadata") != 1 || len(fake.calls) != 3 {
		t.Errorf("requests = %s", fake.steps())
	}
}

func TestAttachmentUploadReportsUncertaintyWithTheFileIDAndNeverRetries(t *testing.T) {
	for name, c := range map[string]struct {
		fake  *uploadFake
		class provider.Class
		calls int
		puts  int
		done  int
		att   int
	}{
		"timeout on the put":      {&uploadFake{put: fail(context.DeadlineExceeded)}, provider.ClassTimeout, 3, 1, 0, 0},
		"reset on the put":        {&uploadFake{put: fail(errors.New("connection reset by peer " + uploadToken))}, provider.ClassUnreachable, 3, 1, 0, 0},
		"5xx on the put":          {&uploadFake{put: status(http.StatusInternalServerError)}, provider.ClassProviderError, 3, 1, 0, 0},
		"errors on complete":      {&uploadFake{complete: answerWith(graphqlErrors("INTERNAL_SERVER_ERROR"))}, provider.ClassProviderError, 4, 1, 1, 0},
		"5xx on complete":         {&uploadFake{complete: status(http.StatusBadGateway)}, provider.ClassProviderError, 4, 1, 1, 0},
		"other file on complete":  {&uploadFake{complete: answerWith(`{"data":{"completeFileUpload":{"id":"` + attOtherID + `"}}}`)}, provider.ClassInvalidResponse, 4, 1, 1, 0},
		"5xx on the attachment":   {&uploadFake{attach: status(http.StatusInternalServerError)}, provider.ClassProviderError, 5, 1, 1, 1},
		"4xx on the attachment":   {&uploadFake{attach: status(http.StatusBadRequest)}, provider.ClassProviderError, 5, 1, 1, 1},
		"unreadable attachment":   {&uploadFake{attach: answerWith(`not json`)}, provider.ClassInvalidResponse, 5, 1, 1, 1},
		"other attachment target": {&uploadFake{attach: answerWith(`{"data":{"createAttachment":{"id":"` + attachID + `","file":[]}}}`)}, provider.ClassInvalidResponse, 5, 1, 1, 1},
	} {
		serveUpload(t, c.fake)
		_, _, err := runUpload(t, uploadConnection("", "object/person"), inlineArgs(uploadBody))
		var failure *provider.Error
		if !errors.As(err, &failure) || failure.Class != c.class || !strings.Contains(err.Error(), fileUUID) ||
			strings.Contains(err.Error(), uploadToken) || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(c.fake.calls) != c.calls || c.fake.count(http.MethodPut, "") != c.puts ||
			c.fake.count(http.MethodPost, "/metadata") != 1+c.done || c.fake.count(http.MethodPost, "/rest") != c.att {
			t.Errorf("%s: requests = %s", name, c.fake.steps())
		}
	}
}

func TestAttachmentUploadStatesWhatRemainsInTwenty(t *testing.T) {
	fake := &uploadFake{put: status(http.StatusInternalServerError)}
	serveUpload(t, fake)
	_, _, err := runUpload(t, uploadConnection("", "object/person"), inlineArgs(uploadBody))
	if err == nil || !strings.Contains(err.Error(), "after 24 hours") {
		t.Errorf("put: %v", err)
	}
	fake = &uploadFake{complete: status(http.StatusInternalServerError)}
	serveUpload(t, fake)
	_, _, err = runUpload(t, uploadConnection("", "object/person"), inlineArgs(uploadBody))
	if err == nil || !strings.Contains(err.Error(), "stays in Twenty") || strings.Contains(err.Error(), "24 hours") {
		t.Errorf("complete: %v", err)
	}
	fake = &uploadFake{attach: status(http.StatusForbidden)}
	serveUpload(t, fake)
	_, _, err = runUpload(t, uploadConnection("", "object/person"), inlineArgs(uploadBody))
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "not attached") || !strings.Contains(err.Error(), fileUUID) {
		t.Errorf("attach: %v", err)
	}
}

func TestAttachmentUploadNamesTheUploadFilePermission(t *testing.T) {
	for name, fake := range map[string]*uploadFake{
		"create http":     {create: status(http.StatusForbidden)},
		"create graphql":  {create: answerWith(graphqlErrors("FORBIDDEN"))},
		"complete http":   {complete: status(http.StatusForbidden)},
		"complete errors": {complete: answerWith(graphqlErrors("FORBIDDEN"))},
	} {
		serveUpload(t, fake)
		_, _, err := runUpload(t, uploadConnection("", "object/person"), inlineArgs(uploadBody))
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Upload File") ||
			strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), uploadToken) {
			t.Errorf("%s: err = %v", name, err)
		}
		if strings.HasPrefix(name, "create") && len(fake.calls) != 2 {
			t.Errorf("%s: requests = %s", name, fake.steps())
		}
	}
}

func TestAttachmentUploadRefusesBadSourcesBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	dir, other := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "a.pdf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.pdf")
	big := filepath.Join(dir, "big.bin")
	for path, size := range map[string]int64{empty: 0, big: maxUploadBytes + 1} {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Skipf("no sparse files: %v", err)
		}
		f.Close()
	}
	base := `{"object":"person","record_id":"` + attPersonID + `"`
	asked := 0
	counting := secret.NewWith(func(string) string { asked++; return cloudKey }, nil, nil, nil)
	tooBig := strings.Repeat("A", maxInlineBase64+4)
	for name, c := range map[string]struct {
		resolved *config.Resolved
		args     string
		path     bool
	}{
		"empty inline":     {uploadConnection("", "object/person"), inlineArgs(""), false},
		"empty file":       {uploadConnection(dir), base + `,"local_path":` + strconv.Quote(empty) + `}`, false},
		"file too large":   {uploadConnection(dir), base + `,"local_path":` + strconv.Quote(big) + `}`, false},
		"inline too large": {uploadConnection(""), base + `,"name":"a.bin","content_base64":"` + tooBig + `"}`, false},
		"bad base64":       {uploadConnection(""), base + `,"name":"a.bin","content_base64":"***"}`, false},
		"no name":          {uploadConnection(""), base + `,"content_base64":"QQ=="}`, false},
		"path in name":     {uploadConnection(""), base + `,"name":"../a.bin","content_base64":"QQ=="}`, false},
		"control in name":  {uploadConnection(""), base + `,"name":"a\u0000.bin","content_base64":"QQ=="}`, false},
		"both sources":     {uploadConnection(dir), base + `,"name":"a","content_base64":"QQ==","local_path":"/x"}`, false},
		"no source":        {uploadConnection(""), base + `}`, false},
		"name with path":   {uploadConnection(dir), base + `,"name":"a","local_path":` + strconv.Quote(empty) + `}`, false},
		"bad record":       {uploadConnection(""), `{"object":"person","record_id":"x","name":"a","content_base64":"QQ=="}`, false},
		"other object":     {uploadConnection("", "object/company"), inlineArgs("x"), false},
		"system object":    {uploadConnection(""), `{"object":"attachment","record_id":"` + attPersonID + `","name":"a","content_base64":"QQ=="}`, false},
		"outside release":  {uploadConnection(dir), base + `,"local_path":` + strconv.Quote(filepath.Join(other, "a.pdf")) + `}`, true},
		"no release":       {uploadConnection(""), base + `,"local_path":` + strconv.Quote(filepath.Join(other, "a.pdf")) + `}`, true},
	} {
		_, err := invokeAttachmentsUpload(t.Context(), c.resolved, counting, &redact.Redactor{}, json.RawMessage(c.args))
		var pathErr *localfile.PathError
		var invalid *provider.InvalidRequestError
		if err == nil || (!errors.As(err, &invalid) && !errors.As(err, &pathErr)) {
			t.Errorf("%s: err = %v", name, err)
		}
		if c.path && (pathErr == nil || strings.Contains(err.Error(), other)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if asked != 0 {
		t.Errorf("the credential was resolved %d times", asked)
	}
}

func TestAttachmentUploadReadsTheRecordFirstAndLeavesNoFileBehind(t *testing.T) {
	for name, c := range map[string]struct {
		fake     *uploadFake
		resolved *config.Resolved
		args     string
	}{
		"record not found": {&uploadFake{record: status(http.StatusNotFound)}, uploadConnection("", "object/person"), inlineArgs("x")},
		"record forbidden": {&uploadFake{record: status(http.StatusForbidden)}, uploadConnection("", "object/person"), inlineArgs("x")},
		"other record":     {&uploadFake{record: answerWith(`{"data":{"person":{"id":"` + attOtherID + `"}}}`)}, uploadConnection("", "object/person"), inlineArgs("x")},
		"no target field":  {&uploadFake{}, uploadConnection("", "object/opportunity"), strings.Replace(inlineArgs("x"), "person", "opportunity", 1)},
	} {
		serveUpload(t, c.fake)
		_, _, err := runUpload(t, c.resolved, c.args)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if c.fake.count(http.MethodPost, "") != 0 || c.fake.count(http.MethodPut, "") != 0 {
			t.Errorf("%s: requests = %s", name, c.fake.steps())
		}
	}
}

func TestAttachmentUploadStopsBeforeCompletingWhenTheLocalFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "offer.pdf")
	if err := os.WriteFile(path, []byte(uploadBody), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &uploadFake{create: func(*http.Request) (*http.Response, error) {
		// The file grows after it was opened: the upload read stops at the announced size and notices.
		_ = os.WriteFile(path, []byte(uploadBody+"more"), 0o600)
		return jsonResponse(http.StatusOK, createAnswer(goodUploadURL(), uploadContentType)), nil
	}}
	serveUpload(t, fake)
	_, _, err := runUpload(t, uploadConnection(dir, "object/person"),
		`{"object":"person","record_id":"`+attPersonID+`","local_path":`+strconv.Quote(path)+`}`)
	if err == nil || !strings.Contains(err.Error(), "changed while it was uploaded") || fake.count(http.MethodPost, "/metadata") != 1 ||
		fake.count(http.MethodPost, "/rest") != 0 {
		t.Fatalf("err = %v, requests = %s", err, fake.steps())
	}
}
