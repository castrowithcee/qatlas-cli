package seatable

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
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	linkCanaryToken = "bearer-link-canary-seatable-9e41"
	uploadAddress   = "https://cloud.seatable.io/seafhttp/upload-api/" + linkCanaryToken
	downloadAddress = "https://cloud.seatable.io/seafhttp/files/" + linkCanaryToken + "/x.txt"
	fileColumnName  = "Anhang"
	imageColumnName = "Bilder"
)

const fileMetadata = `{"metadata":{"tables":[
 {"_id":"0000","name":"Kunden","columns":[{"key":"0000","name":"Name","type":"text"},
   {"key":"f001","name":"Anhang","type":"file"},{"key":"f002","name":"Bilder","type":"image"}],
  "views":[{"_id":"0000","name":"Standard"}]},
 {"_id":"0001","name":"Tickets","columns":[{"key":"0000","name":"Titel","type":"text"}],
  "views":[{"_id":"0000","name":"Standard"}]}
]}}`

var existingFile = `{"name":"a.txt","size":3,"type":"file","url":"https://cloud.seatable.io/workspace/7/asset/` +
	salesBase + `/files/2026-03/a.txt"}`

type fileCall struct {
	method, path, query, auth, contentType string
	length                                 int64
	body                                   []byte
}

type fileFake struct {
	mu          sync.Mutex
	calls       []fileCall
	uploadLink  string
	downloadURL string
	cell        string
	postStatus  int
	putStatus   int
	content     []byte
	announce    int64
	name        string
}

func newFileFake() *fileFake {
	return &fileFake{uploadLink: uploadAddress, downloadURL: downloadAddress, cell: "[" + existingFile + "]",
		postStatus: http.StatusOK, putStatus: http.StatusOK, content: []byte("hello"), announce: -2, name: "x.txt"}
}

func (f *fileFake) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if call.method == method {
			n++
		}
	}
	return n
}

func (f *fileFake) find(method, path string) *fileCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.calls {
		if f.calls[i].method == method && strings.HasPrefix(f.calls[i].path, path) {
			return &f.calls[i]
		}
	}
	return nil
}

func serveFiles(t *testing.T, f *fileFake) {
	t.Helper()
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		var data []byte
		if request.Body != nil {
			data, _ = io.ReadAll(request.Body)
		}
		f.mu.Lock()
		f.calls = append(f.calls, fileCall{request.Method, request.URL.Path, request.URL.RawQuery,
			request.Header.Get("Authorization"), request.Header.Get("Content-Type"), request.ContentLength, data})
		f.mu.Unlock()
		switch {
		case request.URL.Path == metaRoute(salesBase):
			return jsonResponse(http.StatusOK, fileMetadata), nil
		case request.URL.Path == rowRoute(salesBase, rowID):
			return jsonResponse(http.StatusOK, `{"_id":"`+rowID+`","Name":"Bike","Anhang":`+f.cell+`,"Bilder":`+f.cell+`}`), nil
		case request.URL.Path == uploadLinkPath:
			return jsonResponse(http.StatusOK, `{"upload_link":"`+f.uploadLink+`","workspace_id":7,"parent_path":"/asset/`+
				salesBase+`","img_relative_path":"images/2026-03","file_relative_path":"files/2026-03"}`), nil
		case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/seafhttp/upload-api/"):
			if f.postStatus != http.StatusOK {
				return jsonResponse(f.postStatus, `{"error":"`+bodyCanary+`"}`), nil
			}
			return jsonResponse(http.StatusOK, `[{"name":"`+f.name+`","id":"abc123","size":`+
				itoa(int64(len(f.content)))+`}]`), nil
		case request.Method == http.MethodPut && request.URL.Path == rowsRoute(salesBase):
			return jsonResponse(f.putStatus, `{"success":true}`), nil
		case request.URL.Path == downloadLinkPath:
			return jsonResponse(http.StatusOK, `{"download_link":"`+f.downloadURL+`"}`), nil
		case strings.HasPrefix(request.URL.Path, "/seafhttp/files/"):
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{},
				Body: io.NopCloser(bytes.NewReader(f.content)), ContentLength: int64(len(f.content))}
			if f.announce != -2 {
				response.ContentLength = f.announce
			}
			return response, nil
		case request.Method == http.MethodDelete && request.URL.Path == assetPath:
			return jsonResponse(http.StatusOK, `{"success":true}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, salesToken)
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func sumOf(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func fileInput(t *testing.T, extra string) FileInput {
	t.Helper()
	var input FileInput
	raw := `{"row_id":"` + rowID + `","column":"` + fileColumnName + `"` + extra + `}`
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	return input
}

func multipartFields(t *testing.T, call *fileCall) (map[string]string, []byte) {
	t.Helper()
	_, params, err := mime.ParseMediaType(call.contentType)
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(bytes.NewReader(call.body), params["boundary"])
	fields := map[string]string{}
	var file []byte
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		if part.FormName() == "file" {
			file = data
			fields["filename"] = part.FileName()
			continue
		}
		fields[part.FormName()] = string(data)
	}
	return fields, file
}

func TestUploadSendsOneUploadAndOneRowUpdate(t *testing.T) {
	fake := newFileFake()
	serveFiles(t, fake)
	c, red := client(t, "Kunden")
	payload := []byte("hello")
	fake.content = payload
	result, err := c.UploadFile(context.Background(), fileInput(t, ""), "x.txt", int64(len(payload)), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "abc123" || result.Name != "x.txt" || result.Size != 5 {
		t.Errorf("result = %+v", result)
	}
	if fake.count(http.MethodPost) != 1 || fake.count(http.MethodPut) != 1 {
		t.Fatalf("calls = %+v, want one upload and one update", fake.calls)
	}
	post := fake.find(http.MethodPost, "/seafhttp/")
	if post.auth != "" || post.query != "ret-json=1" || post.length != int64(len(post.body)) {
		t.Errorf("upload = %+v, want no credential, ret-json and an exact length", post)
	}
	fields, file := multipartFields(t, post)
	if fields["parent_dir"] != "/asset/"+salesBase || fields["relative_path"] != "files/2026-03" ||
		fields["filename"] != "x.txt" || string(file) != "hello" {
		t.Errorf("multipart = %v %q", fields, file)
	}
	var update struct {
		Updates []struct {
			RowID string                       `json:"row_id"`
			Row   map[string][]json.RawMessage `json:"row"`
		} `json:"updates"`
		Table string `json:"table_name"`
	}
	put := fake.find(http.MethodPut, rowsRoute(salesBase))
	if err := json.Unmarshal(put.body, &update); err != nil || len(update.Updates) != 1 || update.Table != "Kunden" {
		t.Fatalf("update = %s: %v", put.body, err)
	}
	cell := update.Updates[0].Row[fileColumnName]
	if update.Updates[0].RowID != rowID || len(cell) != 2 || string(cell[0]) != existingFile ||
		!strings.Contains(string(cell[1]), `"url":"/workspace/7/asset/`+salesBase+`/files/2026-03/x.txt"`) ||
		!strings.Contains(string(cell[1]), `"type":"file"`) || !strings.Contains(string(cell[1]), `"size":5`) {
		t.Errorf("cell = %q", cell)
	}
	if strings.Contains(string(put.body), linkCanaryToken) {
		t.Error("the upload link reached the row update")
	}
	if got := red.Apply(uploadAddress); strings.Contains(got, linkCanaryToken) {
		t.Errorf("the upload link is not registered with the redactor: %q", got)
	}
}

func TestUploadToAnImageColumnAppendsTheAddressAsString(t *testing.T) {
	fake := newFileFake()
	fake.cell = `null`
	fake.name = "p.png"
	serveFiles(t, fake)
	c, _ := client(t, "Kunden")
	input := fileInput(t, "")
	input.Column = imageColumnName
	if _, err := c.UploadFile(context.Background(), input, "p.png", 5, bytes.NewReader(fake.content)); err != nil {
		t.Fatal(err)
	}
	fields, _ := multipartFields(t, fake.find(http.MethodPost, "/seafhttp/"))
	put := fake.find(http.MethodPut, rowsRoute(salesBase))
	if fields["relative_path"] != "images/2026-03" ||
		!strings.Contains(string(put.body), `"`+imageColumnName+`":["/workspace/7/asset/`+salesBase+`/images/2026-03/p.png"]`) {
		t.Errorf("fields = %v body = %s", fields, put.body)
	}
}

func TestUploadRefusesForeignHostsAndUnsafeLocations(t *testing.T) {
	for name, link := range map[string]string{
		"other host":  "https://evil.example.invalid/seafhttp/upload-api/" + linkCanaryToken,
		"plain http":  "http://cloud.seatable.io/seafhttp/upload-api/" + linkCanaryToken,
		"userinfo":    "https://user@cloud.seatable.io/seafhttp/upload-api/" + linkCanaryToken,
		"empty":       "",
		"other port":  "https://cloud.seatable.io:8443/seafhttp/upload-api/" + linkCanaryToken,
		"host prefix": "https://cloud.seatable.io.evil.example.invalid/" + linkCanaryToken,
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFileFake()
			fake.uploadLink = link
			serveFiles(t, fake)
			c, _ := client(t, "Kunden")
			_, err := c.UploadFile(context.Background(), fileInput(t, ""), "x.txt", 5, bytes.NewReader(fake.content))
			if classOf(err) != provider.ClassInvalidResponse {
				t.Fatalf("err = %v", err)
			}
			if fake.count(http.MethodPost) != 0 || fake.count(http.MethodPut) != 0 {
				t.Errorf("calls = %+v", fake.calls)
			}
			if strings.Contains(err.Error(), linkCanaryToken) || strings.Contains(err.Error(), "evil") {
				t.Errorf("the error names the link: %v", err)
			}
		})
	}
}

func TestUploadRefusesAnUploadLinkOfAnotherBase(t *testing.T) {
	fake := newFileFake()
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case metaRoute(salesBase):
			return jsonResponse(http.StatusOK, fileMetadata), nil
		case rowRoute(salesBase, rowID):
			return jsonResponse(http.StatusOK, `{"_id":"`+rowID+`","Anhang":[]}`), nil
		case uploadLinkPath:
			return jsonResponse(http.StatusOK, `{"upload_link":"`+uploadAddress+`","workspace_id":7,"parent_path":"/asset/`+
				supportBase+`","file_relative_path":"files/2026-03"}`), nil
		}
		fake.mu.Lock()
		fake.calls = append(fake.calls, fileCall{method: request.Method, path: request.URL.Path})
		fake.mu.Unlock()
		return jsonResponse(http.StatusOK, `{}`), nil
	})
	stubLimiter(t, salesToken)
	c, _ := client(t, "Kunden")
	if _, err := c.UploadFile(context.Background(), fileInput(t, ""), "x.txt", 5, strings.NewReader("hello")); classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("err = %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("calls = %+v", fake.calls)
	}
}

func TestUploadReportsAnOpenResultAndNeverRepeats(t *testing.T) {
	for name, tc := range map[string]struct {
		post, put int
		hint      string
	}{
		"upload 503": {http.StatusServiceUnavailable, 200, "may have been stored"},
		"update 504": {200, http.StatusGatewayTimeout, "may have been stored or attached"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFileFake()
			fake.postStatus, fake.putStatus = tc.post, tc.put
			serveFiles(t, fake)
			c, _ := client(t, "Kunden")
			_, err := c.UploadFile(context.Background(), fileInput(t, ""), "x.txt", 5, bytes.NewReader(fake.content))
			if err == nil || !strings.Contains(err.Error(), tc.hint) {
				t.Fatalf("err = %v", err)
			}
			if fake.count(http.MethodPost) != 1 || fake.count(http.MethodPut) > 1 {
				t.Errorf("calls = %+v, want no repetition", fake.calls)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("the error copies a provider body: %v", err)
			}
		})
	}
}

func TestFileToolsStayInsideTheReleasedTableAndColumn(t *testing.T) {
	refuse(t)
	c, _ := client(t, "Kunden")
	input := fileInput(t, "")
	input.Table = "Tickets"
	if _, err := c.UploadFile(context.Background(), input, "x.txt", 1, strings.NewReader("x")); err == nil {
		t.Error("an upload into a table outside the connection succeeded")
	}
	if _, err := c.DownloadFile(context.Background(), input, nil); err == nil {
		t.Error("a download from a table outside the connection succeeded")
	}
	if err := c.DeleteFile(context.Background(), input); err == nil {
		t.Error("a delete in a table outside the connection succeeded")
	}
}

func TestFileToolsRefuseColumnsWithoutFiles(t *testing.T) {
	fake := newFileFake()
	serveFiles(t, fake)
	c, _ := client(t, "Kunden")
	input := fileInput(t, "")
	input.Column = "Name"
	if _, err := c.UploadFile(context.Background(), input, "x.txt", 5, bytes.NewReader(fake.content)); err == nil {
		t.Fatal("upload into a text column succeeded")
	}
	input.Column = "Fehlt"
	if _, err := c.DownloadFile(context.Background(), input, nil); err == nil {
		t.Fatal("download from an unknown column succeeded")
	}
	if fake.count(http.MethodPost) != 0 || fake.find(http.MethodGet, uploadLinkPath) != nil ||
		fake.find(http.MethodGet, downloadLinkPath) != nil {
		t.Errorf("calls = %+v", fake.calls)
	}
}

func TestDownloadReturnsInlineContentFromTheCellAddress(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("hello")
	serveFiles(t, fake)
	c, red := client(t, "Kunden")
	result, err := c.DownloadFile(context.Background(), fileInput(t, `,"name":"a.txt"`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "a.txt" || result.Size != 5 || result.SHA256 != sumOf(fake.content) ||
		result.Content != base64.StdEncoding.EncodeToString(fake.content) {
		t.Errorf("result = %+v", result)
	}
	link := fake.find(http.MethodGet, downloadLinkPath)
	if link.query != "path=files%2F2026-03%2Fa.txt" || link.auth != "Bearer "+salesToken {
		t.Errorf("link request = %+v", link)
	}
	if get := fake.find(http.MethodGet, "/seafhttp/files/"); get == nil || get.auth != "" {
		t.Errorf("file request = %+v, want no credential", get)
	}
	if strings.Contains(red.Apply(downloadAddress), linkCanaryToken) {
		t.Error("the download link is not registered with the redactor")
	}
}

func TestDownloadSelectsEntriesOnlyFromTheCell(t *testing.T) {
	twoSame := `[` + existingFile + `,` + strings.Replace(existingFile, "2026-03", "2026-04", 1) + `]`
	custom := `[{"name":"c.txt","size":1,"type":"file","url":"custom-asset://abc.txt"},` +
		`{"name":"o.txt","size":1,"type":"file","url":"https://cloud.seatable.io/workspace/7/asset/` + supportBase + `/files/2026-03/o.txt"},` +
		`{"name":"t.txt","size":1,"type":"file","url":"https://cloud.seatable.io/workspace/7/asset/` + salesBase + `/files/../../x/t.txt"},` +
		`{"name":"f.txt","size":1,"type":"file","url":"https://evil.example.invalid/workspace/7/asset/` + salesBase + `/files/2026-03/f.txt"},` +
		existingFile + `]`
	cases := map[string]struct {
		cell, extra string
		ok          bool
	}{
		"sole entry":        {"[" + existingFile + "]", ``, true},
		"ambiguous name":    {twoSame, `,"name":"a.txt"`, false},
		"index":             {twoSame, `,"index":1`, true},
		"no selector, many": {twoSame, ``, false},
		"index out of cell": {twoSame, `,"index":2`, false},
		"unknown name":      {twoSame, `,"name":"zzz"`, false},
		"custom asset":      {custom, `,"index":0`, false},
		"other base":        {custom, `,"index":1`, false},
		"dot segments":      {custom, `,"index":2`, false},
		"other origin":      {custom, `,"index":3`, false},
		"valid after odd":   {custom, `,"name":"a.txt"`, true},
		"empty cell":        {`[]`, ``, false},
		"no cell":           {`null`, ``, false},
		"name and index":    {twoSame, `,"name":"a.txt","index":0`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFileFake()
			fake.cell = tc.cell
			serveFiles(t, fake)
			c, _ := client(t, "Kunden")
			_, err := c.DownloadFile(context.Background(), fileInput(t, tc.extra), nil)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if !tc.ok && fake.find(http.MethodGet, downloadLinkPath) != nil {
				t.Errorf("a link was requested for a refused selection: %+v", fake.calls)
			}
		})
	}
}

func TestDownloadRefusesAForeignLinkHostBeforeFetching(t *testing.T) {
	fake := newFileFake()
	fake.downloadURL = "https://evil.example.invalid/seafhttp/files/" + linkCanaryToken + "/x.txt"
	serveFiles(t, fake)
	c, _ := client(t, "Kunden")
	_, err := c.DownloadFile(context.Background(), fileInput(t, ""), nil)
	if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), linkCanaryToken) {
		t.Fatalf("err = %v", err)
	}
	if fake.find(http.MethodGet, "/seafhttp/") != nil {
		t.Errorf("the foreign link was fetched: %+v", fake.calls)
	}
}

func TestDownloadRefusesInlineContentAbove4MiB(t *testing.T) {
	for name, announce := range map[string]int64{"announced": maxInlineFileBytes + 1, "unannounced": -1} {
		t.Run(name, func(t *testing.T) {
			fake := newFileFake()
			fake.content = bytes.Repeat([]byte("a"), maxInlineFileBytes+1)
			fake.announce = announce
			serveFiles(t, fake)
			c, _ := client(t, "Kunden")
			if _, err := c.DownloadFile(context.Background(), fileInput(t, ""), nil); err == nil ||
				!strings.Contains(err.Error(), "4 MiB") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	fake := newFileFake()
	fake.content = bytes.Repeat([]byte("a"), maxInlineFileBytes)
	serveFiles(t, fake)
	c, _ := client(t, "Kunden")
	if result, err := c.DownloadFile(context.Background(), fileInput(t, ""), nil); err != nil || result.Size != maxInlineFileBytes {
		t.Fatalf("a file of exactly 4 MiB: %v", err)
	}
}

func TestDeleteSendsOneRequestForTheAssetOfTheCell(t *testing.T) {
	fake := newFileFake()
	serveFiles(t, fake)
	c, _ := client(t, "Kunden")
	if err := c.DeleteFile(context.Background(), fileInput(t, "")); err == nil {
		t.Fatal("a delete without a selector succeeded")
	}
	if fake.count(http.MethodDelete) != 0 {
		t.Fatal("a delete was sent without a selector")
	}
	if err := c.DeleteFile(context.Background(), fileInput(t, `,"index":0`)); err != nil {
		t.Fatal(err)
	}
	call := fake.find(http.MethodDelete, assetPath)
	if fake.count(http.MethodDelete) != 1 || call.query != "path=files%2F2026-03%2Fa.txt" || call.auth != "Bearer "+salesToken {
		t.Errorf("delete = %+v", call)
	}
	if fake.count(http.MethodPut) != 0 {
		t.Error("deleting the asset changed the cell")
	}
}

func TestDeleteReportsAnOpenResultAndNeverRepeats(t *testing.T) {
	fake := newFileFake()
	calls := 0
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case metaRoute(salesBase):
			return jsonResponse(http.StatusOK, fileMetadata), nil
		case rowRoute(salesBase, rowID):
			return jsonResponse(http.StatusOK, `{"_id":"`+rowID+`","Anhang":[`+existingFile+`]}`), nil
		case assetPath:
			calls++
			return jsonResponse(http.StatusBadGateway, `{"error":"`+bodyCanary+`"}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	_ = fake
	stubLimiter(t, salesToken)
	c, _ := client(t, "Kunden")
	err := c.DeleteFile(context.Background(), fileInput(t, `,"index":0`))
	if err == nil || !strings.Contains(err.Error(), "may have been deleted") || strings.Contains(err.Error(), bodyCanary) || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

func TestFileToolDescriptors(t *testing.T) {
	want := map[string]struct {
		effect    capability.Effect
		files     config.LocalFiles
		allowList bool
		confirm   capability.Confirmation
	}{
		"seatable.files.upload": {capability.EffectCreate, config.LocalFilesRead, false, capability.ConfirmationRequired},
		"seatable.files.get":    {capability.EffectRead, config.LocalFilesWrite, false, capability.ConfirmationNone},
		"seatable.files.delete": {capability.EffectDelete, "", true, capability.ConfirmationRequired},
	}
	seen := 0
	for _, d := range registry(t).Provider(Provider) {
		w, ok := want[d.ID]
		if !ok {
			continue
		}
		seen++
		if d.Risk.Effect != w.effect || d.LocalFiles != w.files || d.RequiresToolAllowList != w.allowList ||
			d.Risk.Confirmation != w.confirm || !d.Risk.OpenWorld || d.Risk.DataSensitivity != dataSensitivity ||
			d.Risk.Idempotency == "" {
			t.Errorf("descriptor %s = %+v", d.ID, d)
		}
		for _, property := range []string{`"table"`, `"row_id"`, `"column"`} {
			if !strings.Contains(string(d.InputSchema), property) {
				t.Errorf("%s misses %s", d.ID, property)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("file tools = %d", seen)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if strings.HasPrefix(id, "seatable.files.") {
				t.Errorf("profile %s preselects %s", profile.ID, id)
			}
		}
	}
}

func filesConnection(read, write string) *config.Resolved {
	resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
	if read != "" {
		resolved.Files.Read = []string{read}
	}
	if write != "" {
		resolved.Files.Write = []string{write}
	}
	return resolved
}

func TestUploadFromAReleasedLocalFile(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("local file content")
	fake.name = "offer.txt"
	serveFiles(t, fake)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "offer.txt"), fake.content, 0o600); err != nil {
		t.Fatal(err)
	}
	red := &redact.Redactor{}
	arguments := `{"row_id":"` + rowID + `","column":"Anhang","local_path":"` + filepath.Join(dir, "offer.txt") + `"}`
	result, err := invokeFilesUpload(context.Background(), filesConnection(dir, ""), resolver(red), red, json.RawMessage(arguments))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*FileResult)
	if got.Name != "offer.txt" || got.Size != int64(len(fake.content)) || got.SHA256 != sumOf(fake.content) || got.ID == "" || got.Content != "" {
		t.Errorf("result = %+v", got)
	}
	_, file := multipartFields(t, fake.find(http.MethodPost, "/seafhttp/"))
	if !bytes.Equal(file, fake.content) {
		t.Errorf("uploaded content = %q", file)
	}
}

func TestUploadRefusesPathsOutsideTheReleaseBeforeAnyProviderIO(t *testing.T) {
	refuse(t)
	released, other := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	red := &redact.Redactor{}
	for name, arguments := range map[string]string{
		"outside": `"local_path":"` + filepath.Join(other, "secret.txt") + `"`,
		"dotdot":  `"local_path":"` + released + `/../x"`,
		"both":    `"local_path":"` + released + `/a","content_base64":"aGk=","name":"a"`,
		"neither": `"name":"a"`,
	} {
		_, err := invokeFilesUpload(context.Background(), filesConnection(released, ""), resolver(red), red,
			json.RawMessage(`{"row_id":"`+rowID+`","column":"Anhang",`+arguments+`}`))
		var pathErr *localfile.PathError
		if err == nil || (name != "neither" && name != "both" && !errors.As(err, &pathErr)) {
			t.Errorf("%s: err = %v", name, err)
		}
		if err != nil && (strings.Contains(err.Error(), other) || strings.Contains(err.Error(), released)) {
			t.Errorf("%s: the error names a path: %v", name, err)
		}
	}
	// Without a released directory no file is read at all.
	if _, err := invokeFilesUpload(context.Background(), filesConnection("", ""), resolver(red), red,
		json.RawMessage(`{"row_id":"`+rowID+`","column":"Anhang","local_path":"`+filepath.Join(other, "secret.txt")+`"}`)); err == nil {
		t.Error("an upload without a released directory succeeded")
	}
}

func TestUploadInlineNeedsANameAndStaysBelow4MiB(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("hi")
	fake.name = "hi.txt"
	serveFiles(t, fake)
	red := &redact.Redactor{}
	call := func(extra string) (any, error) {
		return invokeFilesUpload(context.Background(), filesConnection(t.TempDir(), ""), resolver(red), red,
			json.RawMessage(`{"row_id":"`+rowID+`","column":"Anhang"`+extra+`}`))
	}
	if _, err := call(`,"content_base64":"aGk="`); err == nil {
		t.Error("an inline upload without name succeeded")
	}
	if _, err := call(`,"content_base64":"!!","name":"a.txt"`); err == nil {
		t.Error("invalid base64 succeeded")
	}
	if _, err := call(`,"content_base64":"aGk=","name":"../a.txt"`); err == nil {
		t.Error("a name with a path succeeded")
	}
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), maxInlineFileBytes+1))
	if _, err := call(`,"content_base64":"` + big + `","name":"a.txt"`); err == nil {
		t.Error("an inline upload above 4 MiB succeeded")
	}
	if fake.count(http.MethodPost) != 0 {
		t.Fatalf("a refused upload reached the provider: %+v", fake.calls)
	}
	result, err := call(`,"content_base64":"aGk=","name":"hi.txt"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(*FileResult); got.SHA256 != sumOf([]byte("hi")) || got.Size != 2 {
		t.Errorf("result = %+v", got)
	}
}

func TestDownloadToAReleasedPathWritesOnlyMetadataAndChecksTheSize(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("hello")
	serveFiles(t, fake)
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	red := &redact.Redactor{}
	arguments := json.RawMessage(`{"row_id":"` + rowID + `","column":"Anhang","local_path":"` + target + `"}`)

	result, err := invokeFilesGet(context.Background(), filesConnection("", dir), resolver(red), red, arguments)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*FileResult)
	if got.Content != "" || got.Name != "a.txt" || got.Size != 5 || got.SHA256 != sumOf(fake.content) {
		t.Errorf("result = %+v, want metadata only", got)
	}
	if data, _ := os.ReadFile(target); string(data) != "hello" {
		t.Errorf("file = %q", data)
	}

	// An existing file is replaced only with confirmation.
	if _, err := invokeFilesGet(context.Background(), filesConnection("", dir), resolver(red), red, arguments); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Fatalf("unconfirmed overwrite: %v", err)
	}
	fake.content = []byte("world")
	if _, err := invokeFilesGet(capability.WithConfirmed(context.Background()), filesConnection("", dir), resolver(red), red, arguments); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "world" {
		t.Errorf("file = %q, want it replaced", data)
	}

	// Content that disagrees with the announced size is discarded and leaves the file as it was.
	fake.content, fake.announce = []byte("longer than announced"), 3
	_, err = invokeFilesGet(capability.WithConfirmed(context.Background()), filesConnection("", dir), resolver(red), red, arguments)
	var integrity *localfile.IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("err = %v, want an integrity error", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "world" {
		t.Errorf("file = %q, want it unchanged", data)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want no temporary file", len(entries))
	}
}

func TestDownloadRefusesPathsOutsideTheReleaseBeforeAnyProviderIO(t *testing.T) {
	refuse(t)
	released, other := t.TempDir(), t.TempDir()
	red := &redact.Redactor{}
	_, err := invokeFilesGet(context.Background(), filesConnection("", released), resolver(red), red,
		json.RawMessage(`{"row_id":"`+rowID+`","column":"Anhang","local_path":"`+filepath.Join(other, "a.txt")+`"}`))
	var pathErr *localfile.PathError
	if !errors.As(err, &pathErr) || strings.Contains(err.Error(), other) {
		t.Fatalf("err = %v", err)
	}
}

func TestFileToolsThroughTheCoreNeedReleasedDirectoriesAndConfirmation(t *testing.T) {
	fake := newFileFake()
	fake.content = []byte("hi")
	fake.name = "hi.txt"
	serveFiles(t, fake)
	cfg := coreConfig()
	dir := t.TempDir()
	connection := cfg.Connections["sales"]
	connection.Permissions = []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	connection.Files = config.Files{Read: []string{dir}, Write: []string{dir}}
	cfg.Connections["sales"] = connection
	plain := cfg.Connections["support"]
	plain.Permissions = []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	cfg.Connections["support"] = plain
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)

	upload := `{"row_id":"` + rowID + `","column":"Anhang","name":"hi.txt","content_base64":"aGk="}`
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "seatable.files.upload", Connection: "sales", Arguments: json.RawMessage(upload)}); err == nil ||
		application.ErrorCode(err) != "confirmation-required" {
		t.Fatalf("unconfirmed upload: %v", err)
	}
	if fake.count(http.MethodPost) != 0 {
		t.Fatal("an unconfirmed upload reached the provider")
	}
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "seatable.files.upload", Connection: "sales", Arguments: json.RawMessage(upload), Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response.Result), linkCanaryToken) || !strings.Contains(string(response.Result), sumOf([]byte("hi"))) {
		t.Errorf("result = %s", response.Result)
	}
	// A connection without a released directory is not offered the file tools.
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "seatable.files.upload", Connection: "support", Arguments: json.RawMessage(upload), Confirmed: true}); err == nil {
		t.Error("a connection without files offered the upload")
	}
	// The deletion needs the tool allow-list even with write permission.
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "seatable.files.delete", Connection: "sales",
		Arguments: json.RawMessage(`{"row_id":"` + rowID + `","column":"Anhang","index":0}`), Confirmed: true}); err == nil {
		t.Error("the deletion was offered without a tool allow-list")
	}
}
