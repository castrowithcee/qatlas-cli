package twentycrm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
)

const (
	attachID    = "aaaaaaaa-1111-4000-8000-000000000001"
	fileUUID    = "ffffffff-2222-4000-8000-000000000002"
	attPersonID = "cccccccc-3333-4000-8000-000000000003"
	attOtherID  = "dddddddd-4444-4000-8000-000000000004"
	fileToken   = "signed.file-token.sample"
)

const attachmentFields = `"id":{"type":"string","format":"uuid"},"createdAt":{"type":"string"},` +
	`"file":{"type":"array","items":{"type":"object"}},` +
	`"targetPersonId":{"type":"string","format":"uuid"},` +
	`"targetPerson":{"type":"object","oneOf":[{"$ref":"#/components/schemas/PersonForResponse"}]},` +
	`"targetCompanyId":{"type":"string","format":"uuid"},` +
	`"targetCompany":{"type":"object","oneOf":[{"$ref":"#/components/schemas/CompanyForResponse"}]},` +
	`"targetRocketId":{"type":"string","format":"uuid"},` +
	`"targetRocket":{"type":"object","oneOf":[{"$ref":"#/components/schemas/RocketForResponse"}]}`

var attachmentSchema = func() string {
	object := func(pascal, props string) string {
		return `"` + pascal + `":{"type":"object","properties":{` + props + `}},"` + pascal +
			`ForResponse":{"type":"object","properties":{"id":{"type":"string"}}}`
	}
	return `{"paths":{"/attachments":{"post":{"operationId":"createOneAttachment"}},` +
		`"/people":{"post":{"operationId":"createOnePerson"}},"/companies":{"post":{"operationId":"createOneCompany"}},` +
		`"/rockets":{"post":{"operationId":"createOneRocket"}},"/opportunities":{"post":{"operationId":"createOneOpportunity"}}},` +
		`"components":{"schemas":{` + object("Person", `"city":{"type":"string"}`) + `,` +
		object("Company", `"name":{"type":"string"}`) + `,` + object("Rocket", `"name":{"type":"string"}`) + `,` +
		object("Opportunity", `"name":{"type":"string"}`) + `,` +
		`"Attachment":{"type":"object","properties":{` + attachmentFields + `}},` +
		`"AttachmentForUpdate":{"type":"object","properties":{` + attachmentFields + `}},` +
		`"AttachmentForResponse":{"type":"object","properties":{` + attachmentFields + `}}}}}`
}()

func fileEntry(url string) string {
	return `[{"fileId":"` + fileUUID + `","label":"offer","extension":".pdf","url":"` + url + `"}]`
}

func goodURL() string {
	return "https://api.twenty.com/file/files-field/" + fileUUID + "?token=" + fileToken
}

func attachmentRowJSON(targets, file string) string {
	row := `{"id":"` + attachID + `","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z"`
	if targets != "" {
		row += "," + targets
	}
	if file != "" {
		row += `,"file":` + file
	}
	return row + "}"
}

func personTarget() string { return `"targetPersonId":"` + attPersonID + `"` }

type attachmentCall struct {
	Method, Host, Path, Query, Authorization string
}

type attachmentFake struct {
	// row is the attachment served for GET /rest/attachments/{id} and, in a list, the only item.
	row string
	// download answers the file request.
	download func() *http.Response
	calls    []attachmentCall
	listed   []string
}

func (f *attachmentFake) downloads() int {
	n := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call.Path, "/file/") {
			n++
		}
	}
	return n
}

func serveAttachments(t *testing.T, f *attachmentFake) {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, attachmentSchema), nil
		}
		f.calls = append(f.calls, attachmentCall{Method: request.Method, Host: request.URL.Host, Path: request.URL.Path,
			Query: request.URL.RawQuery, Authorization: request.Header.Get("Authorization")})
		switch {
		case strings.HasPrefix(request.URL.Path, "/file/"):
			return f.download(), nil
		case request.URL.Path == "/rest/attachments":
			items := f.row
			if f.listed != nil {
				items = strings.Join(f.listed, ",")
			}
			return jsonResponse(http.StatusOK, `{"data":{"attachments":[`+items+`]},"pageInfo":{"hasNextPage":true,"endCursor":"cur1"}}`), nil
		case request.URL.Path == "/rest/attachments/"+attachID:
			return jsonResponse(http.StatusOK, `{"data":{"attachment":`+f.row+`}}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, cloudKey)
}

func fileResponse(body []byte, announce int64) func() *http.Response {
	return func() *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: announce,
			Body: readCloser{bytes.NewReader(body)}}
	}
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

func okFake(body string) *attachmentFake {
	return &attachmentFake{row: attachmentRowJSON(personTarget(), fileEntry(goodURL())),
		download: fileResponse([]byte(body), int64(len(body)))}
}

func filesConnection(dir string, targets ...string) *config.Resolved {
	resolved := targetConnection(targets...)
	if dir != "" {
		resolved.Files.Write = []string{dir}
	}
	return resolved
}

func runAttachment(t *testing.T, ctx context.Context, resolved *config.Resolved, get bool, args string) (string, error) {
	t.Helper()
	red := &redact.Redactor{}
	handler := invokeAttachmentsList
	if get {
		handler = invokeAttachmentsGet
	}
	result, err := handler(ctx, resolved, resolver(red), red, json.RawMessage(args))
	out, _ := json.Marshal(result)
	return string(out), err
}

func getArgs(extra string) string { return `{"id":"` + attachID + `"` + extra + `}` }

func TestAttachmentDescriptors(t *testing.T) {
	for _, d := range []capability.Descriptor{attachmentsList, attachmentsGet} {
		if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity != recordDataSensitivity || d.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
	}
	if attachmentsGet.LocalFiles != config.LocalFilesWrite || attachmentsList.LocalFiles != "" {
		t.Errorf("local files = %q, %q", attachmentsGet.LocalFiles, attachmentsList.LocalFiles)
	}
}

func TestAttachmentListSendsOneFixedFilterAndReportsNoAddress(t *testing.T) {
	fake := okFake("")
	fake.listed = []string{
		attachmentRowJSON(personTarget(), fileEntry(goodURL())),
		attachmentRowJSON(personTarget()+`,"targetCompanyId":"`+attOtherID+`"`, fileEntry(goodURL())),
		attachmentRowJSON(personTarget(), ""),
		attachmentRowJSON(`"targetRocketId":"`+attOtherID+`"`, fileEntry(goodURL())),
	}
	serveAttachments(t, fake)
	out, err := runAttachment(t, t.Context(), targetConnection("object/person", "object/company"), false,
		`{"object":"person","record_id":"`+attPersonID+`","limit":4}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "url") || strings.Contains(out, fileToken) || strings.Contains(out, "token") {
		t.Fatalf("result names an address: %s", out)
	}
	var list AttachmentList
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Attachments) != 1 || list.Omitted != 3 || !list.HasMore || list.NextCursor == "" ||
		list.Attachments[0] != (AttachmentSummary{ID: attachID, CreatedAt: "2026-01-01T00:00:00Z",
			UpdatedAt: "2026-01-02T00:00:00Z", Label: "offer", Extension: ".pdf", FileID: fileUUID}) {
		t.Fatalf("list = %+v", list)
	}
	if len(fake.calls) != 1 || fake.calls[0].Path != "/rest/attachments" ||
		fake.calls[0].Query != "depth=0&filter=targetPersonId%5Beq%5D%3A"+attPersonID+"&limit=4" {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func TestAttachmentListCapsThePageAndRejectsOversizedAnswers(t *testing.T) {
	refuse(t)
	if _, err := runAttachment(t, t.Context(), targetConnection(), false,
		`{"object":"person","record_id":"`+attPersonID+`","limit":101}`); err == nil {
		t.Fatal("limit above the cap was accepted")
	}
	fake := okFake("")
	fake.listed = []string{fake.row, fake.row}
	serveAttachments(t, fake)
	_, err := runAttachment(t, t.Context(), targetConnection(), false, `{"object":"person","record_id":"`+attPersonID+`","limit":1}`)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("more items than the limit: %v", err)
	}
}

func TestAttachmentListRefusesBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for name, c := range map[string]struct {
		resolved *config.Resolved
		args     string
	}{
		"system object":      {targetConnection(), `{"object":"message","record_id":"` + attPersonID + `"}`},
		"attachment itself":  {targetConnection(), `{"object":"attachment","record_id":"` + attPersonID + `"}`},
		"outside the target": {targetConnection("object/company"), `{"object":"person","record_id":"` + attPersonID + `"}`},
		"bad identifier":     {targetConnection(), `{"object":"person","record_id":"1 or 1"}`},
		"get argument":       {targetConnection(), `{"object":"person","record_id":"` + attPersonID + `","id":"` + attachID + `"}`},
	} {
		_, err := runAttachment(t, t.Context(), c.resolved, false, c.args)
		wantInvalid(t, err)
		if strings.Contains(err.Error(), "message") || strings.Contains(err.Error(), attPersonID) {
			t.Errorf("%s: error echoes input: %v", name, err)
		}
	}
}

func TestAttachmentListRefusesAnObjectWithoutATargetField(t *testing.T) {
	fake := okFake("")
	serveAttachments(t, fake)
	// opportunity exists but the attachment object has no target relation to it.
	_, err := runAttachment(t, t.Context(), targetConnection(), false, `{"object":"opportunity","record_id":"`+attPersonID+`"}`)
	wantInvalid(t, err)
	if len(fake.calls) != 0 {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func TestAttachmentGetReturnsInlineContentFetchedWithoutTheKey(t *testing.T) {
	fake := okFake("hello")
	serveAttachments(t, fake)
	out, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, fileToken) || strings.Contains(out, "url") {
		t.Fatalf("result names the address: %s", out)
	}
	var result AttachmentFile
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	content, _ := base64.StdEncoding.DecodeString(result.Content)
	if string(content) != "hello" || result.Size != 5 || result.ID != attachID || result.Label != "offer" ||
		result.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("result = %+v", result)
	}
	if len(fake.calls) != 2 || fake.calls[0].Path != "/rest/attachments/"+attachID || fake.calls[0].Query != "depth=0" ||
		fake.calls[0].Authorization != "Bearer "+cloudKey {
		t.Fatalf("calls = %+v", fake.calls)
	}
	download := fake.calls[1]
	if download.Method != http.MethodGet || download.Host != "api.twenty.com" || download.Path != "/file/files-field/"+fileUUID ||
		download.Query != "token="+fileToken || download.Authorization != "" {
		t.Fatalf("download = %+v", download)
	}
}

func TestAttachmentGetRefusesForeignAddressesWithoutFetching(t *testing.T) {
	for name, address := range map[string]string{
		"foreign host":  "https://files.example.net/file/files-field/" + fileUUID + "?token=" + fileToken,
		"subdomain":     "https://cdn.api.twenty.com/file/files-field/" + fileUUID + "?token=" + fileToken,
		"other port":    "https://api.twenty.com:8443/file/files-field/" + fileUUID + "?token=" + fileToken,
		"http":          "http://api.twenty.com/file/files-field/" + fileUUID + "?token=" + fileToken,
		"other file id": "https://api.twenty.com/file/files-field/" + attOtherID + "?token=" + fileToken,
		"other folder":  "https://api.twenty.com/file/attachment/" + fileUUID + "?token=" + fileToken,
		"longer path":   "https://api.twenty.com/file/files-field/" + fileUUID + "/x?token=" + fileToken,
		"extra query":   "https://api.twenty.com/file/files-field/" + fileUUID + "?token=" + fileToken + "&redirect=1",
		"query before":  "https://api.twenty.com/file/files-field/" + fileUUID + "?a=b&token=" + fileToken,
		"token missing": "https://api.twenty.com/file/files-field/" + fileUUID,
		"two tokens":    "https://api.twenty.com/file/files-field/" + fileUUID + "?token=" + fileToken + "&token=x",
		"user info": (&url.URL{Scheme: "https", User: url.User("someone"), Host: "api.twenty.com",
			Path: "/file/files-field/" + fileUUID, RawQuery: "token=" + fileToken}).String(),
		"fragment":         "https://api.twenty.com/file/files-field/" + fileUUID + "?token=" + fileToken + "#x",
		"encoded path":     "https://api.twenty.com/file/files-field/%66" + fileUUID[1:] + "?token=" + fileToken,
		"relative address": "/file/files-field/" + fileUUID + "?token=" + fileToken,
		"empty address":    "",
	} {
		fake := okFake("secret")
		fake.row = attachmentRowJSON(personTarget(), fileEntry(address))
		serveAttachments(t, fake)
		_, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if fake.downloads() != 0 {
			t.Errorf("%s: fetched %+v", name, fake.calls)
		}
		for _, leak := range []string{fileToken, "files.example.net", "8443"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error leaks %q: %v", name, leak, err)
			}
		}
	}
}

func TestAttachmentGetNamesExternalStorageForAnotherHost(t *testing.T) {
	fake := okFake("x")
	fake.row = attachmentRowJSON(personTarget(), fileEntry("https://bucket.s3.amazonaws.com/file/files-field/"+fileUUID+"?token="+fileToken))
	serveAttachments(t, fake)
	_, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
	if err == nil || !strings.Contains(err.Error(), "external storage") || fake.downloads() != 0 {
		t.Fatalf("err = %v, calls = %+v", err, fake.calls)
	}
}

func TestAttachmentGetDoesNotFollowARedirect(t *testing.T) {
	fake := okFake("x")
	fake.download = func() *http.Response {
		return &http.Response{StatusCode: http.StatusFound, Body: readCloser{bytes.NewReader(nil)},
			Header: http.Header{"Location": []string{"https://bucket.s3.amazonaws.com/signed?X-Amz-Signature=abc"}}}
	}
	serveAttachments(t, fake)
	_, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
	if err == nil || !strings.Contains(err.Error(), "external storage") || fake.downloads() != 1 ||
		strings.Contains(err.Error(), "amazonaws") || strings.Contains(err.Error(), fileToken) {
		t.Fatalf("err = %v, calls = %+v", err, fake.calls)
	}
}

func TestAttachmentGetFailuresNeverQuoteTheToken(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		fake := okFake("x")
		fake.download = func() *http.Response {
			return &http.Response{StatusCode: status, Header: http.Header{},
				Body: readCloser{bytes.NewReader([]byte("token=" + fileToken))}}
		}
		serveAttachments(t, fake)
		_, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
		if err == nil || strings.Contains(err.Error(), fileToken) {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
	// A transport failure of the download names no address either.
	fake := okFake("x")
	serveAttachments(t, fake)
	previous := transport
	t.Cleanup(func() { transport = previous })
	transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasPrefix(request.URL.Path, "/file/") {
			return nil, errors.New("dial " + request.URL.String())
		}
		return previous.RoundTrip(request)
	})
	_, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
	if err == nil || strings.Contains(err.Error(), fileToken) {
		t.Fatalf("transport failure: %v", err)
	}
}

func TestAttachmentGetChecksTheTargetBeforeAnyDownload(t *testing.T) {
	for name, c := range map[string]struct {
		resolved *config.Resolved
		targets  string
	}{
		"unreachable target": {targetConnection("object/company"), personTarget()},
		"no target":          {targetConnection(), ""},
		"two targets":        {targetConnection(), personTarget() + `,"targetCompanyId":"` + attOtherID + `"`},
		"unknown target":     {targetConnection(), `"targetGhostId":"` + attOtherID + `"`},
		"system target":      {targetConnection(), `"targetWorkspaceMemberId":"` + attOtherID + `"`},
	} {
		fake := okFake("secret")
		fake.row = attachmentRowJSON(c.targets, fileEntry(goodURL()))
		serveAttachments(t, fake)
		_, err := runAttachment(t, t.Context(), c.resolved, true, getArgs(""))
		if classOf(err) != provider.ClassNotFound || fake.downloads() != 0 {
			t.Errorf("%s: err = %v, calls = %+v", name, err, fake.calls)
		}
	}
}

func TestAttachmentGetAnswersForAnotherAttachmentAndMissingFile(t *testing.T) {
	fake := okFake("x")
	fake.row = strings.Replace(attachmentRowJSON(personTarget(), fileEntry(goodURL())), attachID, attOtherID, 1)
	serveAttachments(t, fake)
	_, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
	if classOf(err) != provider.ClassInvalidResponse || fake.downloads() != 0 {
		t.Fatalf("different attachment: %v", err)
	}
	for _, file := range []string{"", "null", "[]"} {
		fake = okFake("x")
		fake.row = attachmentRowJSON(personTarget(), file)
		serveAttachments(t, fake)
		_, err = runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
		if err == nil || !strings.Contains(err.Error(), "no file") || fake.downloads() != 0 {
			t.Errorf("file %q: err = %v", file, err)
		}
	}
	fake = okFake("x")
	fake.row = attachmentRowJSON(personTarget(), `[`+fileEntry(goodURL())[1:len(fileEntry(goodURL()))-1]+`,`+fileEntry(goodURL())[1:])
	serveAttachments(t, fake)
	if _, err = runAttachment(t, t.Context(), targetConnection(), true, getArgs("")); classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("two files: %v", err)
	}
}

func TestAttachmentGetRefusesInlineContentAbove4MiB(t *testing.T) {
	big := bytes.Repeat([]byte("a"), maxInlineFileBytes+1)
	for name, announce := range map[string]int64{"announced": int64(len(big)), "unannounced": -1} {
		fake := okFake("")
		fake.download = fileResponse(big, announce)
		serveAttachments(t, fake)
		_, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
		if err == nil || !strings.Contains(err.Error(), "4 MiB") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	fake := okFake("")
	fake.download = fileResponse(big[:maxInlineFileBytes], int64(maxInlineFileBytes))
	serveAttachments(t, fake)
	if _, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs("")); err != nil {
		t.Fatalf("exactly 4 MiB: %v", err)
	}
}

func TestAttachmentGetWritesOnlyToAReleasedPathAndReplacesOnlyWithConfirmation(t *testing.T) {
	fake := okFake("first")
	serveAttachments(t, fake)
	dir := t.TempDir()
	target := filepath.Join(dir, "offer.pdf")
	extra := `,"local_path":` + strconv.Quote(target)

	out, err := runAttachment(t, t.Context(), filesConnection(dir), true, getArgs(extra))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "content_base64") || !strings.Contains(out, `"size":5`) {
		t.Errorf("result = %s, want metadata only", out)
	}
	if data, _ := os.ReadFile(target); string(data) != "first" {
		t.Errorf("file = %q", data)
	}

	fake.download = fileResponse([]byte("second"), 6)
	if _, err := runAttachment(t, t.Context(), filesConnection(dir), true, getArgs(extra)); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Fatalf("unconfirmed overwrite: %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "first" {
		t.Errorf("file = %q, want it unchanged", data)
	}
	if _, err := runAttachment(t, capability.WithConfirmed(t.Context()), filesConnection(dir), true, getArgs(extra)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "second" {
		t.Errorf("file = %q", data)
	}

	// A length that disagrees with the announced one leaves the file as it was.
	fake.download = fileResponse([]byte("third"), 1)
	if _, err := runAttachment(t, capability.WithConfirmed(t.Context()), filesConnection(dir), true, getArgs(extra)); err == nil {
		t.Fatal("size mismatch was accepted")
	}
	if data, _ := os.ReadFile(target); string(data) != "second" {
		t.Errorf("file = %q, want it unchanged", data)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("directory holds %d entries, want no temporary file", len(entries))
	}
}

func TestAttachmentGetRefusesPathsOutsideTheReleaseBeforeAnyRequest(t *testing.T) {
	refuse(t)
	released, other := t.TempDir(), t.TempDir()
	for name, resolved := range map[string]*config.Resolved{
		"outside the release": filesConnection(released),
		"no release":          filesConnection(""),
	} {
		_, err := runAttachment(t, t.Context(), resolved, true,
			getArgs(`,"local_path":`+strconv.Quote(filepath.Join(other, "a.pdf"))))
		var pathErr *localfile.PathError
		if !errors.As(err, &pathErr) || strings.Contains(err.Error(), other) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if entries, _ := os.ReadDir(other); len(entries) != 0 {
		t.Errorf("outside directory holds %d entries", len(entries))
	}
}

func TestAttachmentGetRefusesBadArgumentsBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for _, args := range []string{`{"id":"x"}`, `{"id":"` + attachID + `","object":"person"}`, `{"id":"` + attachID + `","record_id":"` + attPersonID + `"}`} {
		_, err := runAttachment(t, t.Context(), targetConnection(), true, args)
		wantInvalid(t, err)
	}
}
