package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
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
)

const uploadArgs = `"board_id":"2","stack_id":"5","card_id":"21"`

func uploadConnection(dir string, targets ...string) *config.Resolved {
	resolved := localConnection(dir, "")
	resolved.Target = ""
	resolved.Targets = targets
	return resolved
}

func runAttachmentWrite(handler capability.Handler, resolved *config.Resolved, args string) (any, error) {
	red := &redact.Redactor{}
	return handler(context.Background(), resolved, resolver(red), red, json.RawMessage(args))
}

// writeServer answers the two reads and gives the one change to change.
func writeServer(t *testing.T, change func(*http.Request) (*http.Response, error)) *[]call {
	return serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/stacks"):
			return jsonResponse(http.StatusOK, cardStacksAnswer(false)), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/attachments"):
			return jsonResponse(http.StatusOK, attachmentList), nil
		case request.Method == http.MethodGet:
			t.Errorf("unexpected read %s", request.URL.Path)
			return nil, errors.New("unexpected")
		}
		return change(request)
	})
}

func localFile(t *testing.T, name, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// The quote case stays off the file system, since Windows forbids quotes in file names.
func TestDeckAttachmentMultipartFrameQuotesTheName(t *testing.T) {
	head, tail, contentType, err := multipartFrame(`my "plan".pdf`, true)
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(strings.NewReader(string(head)+"hello"+string(tail)), params["boundary"])
	if _, err := reader.NextPart(); err != nil {
		t.Fatal(err)
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() != `my "plan".pdf` {
		t.Fatalf("part = %+v, err = %v", part, err)
	}
}

func TestDeckAttachmentsUploadSendsOneMultipartRequest(t *testing.T) {
	dir, path := localFile(t, "my plan ä.pdf", "hello")
	var contentType string
	var length int64
	calls := writeServer(t, func(request *http.Request) (*http.Response, error) {
		contentType, length = request.Header.Get("Content-Type"), request.ContentLength
		return jsonResponse(http.StatusOK, `{"id":9,"cardId":21,"type":"deck_file","data":"x"}`), nil
	})
	result, err := runAttachmentWrite(invokeDeckAttachmentsUpload, uploadConnection(dir, "deck/2"),
		`{`+uploadArgs+`,"local_path":`+strconv.Quote(path)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*DeckAttachmentUpload)
	if !got.Uploaded || got.AttachmentID != "9" || got.Name != "my plan ä.pdf" || got.Size != 5 || got.SHA256 != helloSHA {
		t.Errorf("result = %+v", got)
	}
	if len(*calls) != 2 || (*calls)[0].url.Path != deckCardsBase {
		t.Fatalf("calls = %+v", *calls)
	}
	post := (*calls)[1]
	if post.method != http.MethodPost || post.url.Path != attachmentsPath || int64(len(post.body)) != length {
		t.Errorf("post = %s %s, length %d of %d", post.method, post.url.Path, length, len(post.body))
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q", contentType)
	}
	reader := multipart.NewReader(strings.NewReader(post.body), params["boundary"])
	fields := map[string]string{}
	var filename string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(part)
		fields[part.FormName()] = string(data)
		if part.FormName() == "file" {
			filename = part.FileName()
		}
	}
	if len(fields) != 2 || fields["type"] != "deck_file" || fields["file"] != "hello" || filename != "my plan ä.pdf" {
		t.Errorf("fields = %v, filename = %q", fields, filename)
	}
	if encoded, _ := json.Marshal(result); strings.Contains(string(encoded), "hello") {
		t.Errorf("result carries content: %s", encoded)
	}
}

func TestDeckAttachmentsUploadRefusalsMakeNoPost(t *testing.T) {
	dir, path := localFile(t, "plan.pdf", "hello")
	other, outside := localFile(t, "x.pdf", "hello")
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, []byte("123456"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := maxPathUploadBytes
	maxPathUploadBytes = 5
	t.Cleanup(func() { maxPathUploadBytes = previous })
	for name, tc := range map[string]struct {
		args      string
		wantCalls int
		want      string
	}{
		"card in another stack": {`"board_id":"2","stack_id":"6","card_id":"21","local_path":` + strconv.Quote(path), 1, ""},
		"unknown card":          {`"board_id":"2","stack_id":"5","card_id":"99","local_path":` + strconv.Quote(path), 1, ""},
		"too large":             {uploadArgs + `,"local_path":` + strconv.Quote(big), 0, "64 MiB"},
		"outside the release":   {uploadArgs + `,"local_path":` + strconv.Quote(outside), 0, ""},
		"missing file":          {uploadArgs + `,"local_path":` + strconv.Quote(filepath.Join(dir, "none")), 0, ""},
	} {
		t.Run(name, func(t *testing.T) {
			calls := writeServer(t, func(request *http.Request) (*http.Response, error) {
				t.Errorf("a change was sent: %s", request.Method)
				return nil, errors.New("unexpected")
			})
			_, err := runAttachmentWrite(invokeDeckAttachmentsUpload, uploadConnection(dir, "deck/2"), `{`+tc.args+`}`)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), other) {
				t.Errorf("err = %v", err)
			}
			if len(*calls) != tc.wantCalls {
				t.Errorf("calls = %d, want %d", len(*calls), tc.wantCalls)
			}
		})
	}
	var pathErr *localfile.PathError
	refuse(t)
	if _, err := runAttachmentWrite(invokeDeckAttachmentsUpload, uploadConnection(dir, "deck/2"),
		`{`+uploadArgs+`,"local_path":`+strconv.Quote(outside)+`}`); !errors.As(err, &pathErr) {
		t.Errorf("outside release: %v", err)
	}
}

func TestDeckAttachmentsWriteUnboundBoardIsRefusedLocally(t *testing.T) {
	refuse(t)
	dir, path := localFile(t, "plan.pdf", "hello")
	for name, tc := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"upload": {invokeDeckAttachmentsUpload, `{"board_id":"3","stack_id":"5","card_id":"21","local_path":` + strconv.Quote(path) + `}`},
		"delete": {invokeDeckAttachmentsDelete, `{"board_id":"3","stack_id":"5","card_id":"21","attachment_id":"5"}`},
	} {
		_, err := runAttachmentWrite(tc.handler, uploadConnection(dir, "deck/2"), tc.args)
		if err == nil || !strings.Contains(err.Error(), "not bound") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestDeckAttachmentsDeleteSendsOneRequest(t *testing.T) {
	calls := writeServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{}`), nil
	})
	result, err := runAttachmentWrite(invokeDeckAttachmentsDelete, uploadConnection("", "deck/2"),
		`{`+uploadArgs+`,"attachment_id":"5"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(map[string]any); got["deleted"] != true || got["attachment_id"] != "5" {
		t.Errorf("result = %v", result)
	}
	if len(*calls) != 3 || (*calls)[2].method != http.MethodDelete || (*calls)[2].url.Path != attachmentsPath+"/deck_file/5" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestDeckAttachmentsDeleteRefusalsMakeNoRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		args      string
		wantCalls int
		want      string
	}{
		"other card":       {`{` + uploadArgs + `,"attachment_id":"8"}`, 2, ""},
		"unknown":          {`{` + uploadArgs + `,"attachment_id":"99"}`, 2, ""},
		"deleted":          {`{` + uploadArgs + `,"attachment_id":"7"}`, 2, ""},
		"files reference":  {`{` + uploadArgs + `,"attachment_id":"6"}`, 2, ""},
		"card in no stack": {`{"board_id":"2","stack_id":"6","card_id":"21","attachment_id":"5"}`, 1, ""},
	} {
		t.Run(name, func(t *testing.T) {
			calls := writeServer(t, func(request *http.Request) (*http.Response, error) {
				t.Errorf("a change was sent: %s", request.Method)
				return nil, errors.New("unexpected")
			})
			_, err := runAttachmentWrite(invokeDeckAttachmentsDelete, uploadConnection("", "deck/2"), tc.args)
			if err == nil || strings.Contains(err.Error(), "q1.pdf") || strings.Contains(err.Error(), "other.pdf") {
				t.Errorf("err = %v", err)
			}
			if len(*calls) != tc.wantCalls {
				t.Errorf("calls = %d, want %d", len(*calls), tc.wantCalls)
			}
		})
	}
}

func TestDeckAttachmentsWriteUnclearOutcomeIsNeverRepeated(t *testing.T) {
	dir, path := localFile(t, "plan.pdf", "hello")
	unclear := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"garbage": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `not json`), nil },
	}
	for _, op := range []struct {
		name    string
		handler capability.Handler
		args    string
		method  string
		reads   int
	}{
		{"upload", invokeDeckAttachmentsUpload, `{` + uploadArgs + `,"local_path":` + strconv.Quote(path) + `}`, http.MethodPost, 1},
		{"delete", invokeDeckAttachmentsDelete, `{` + uploadArgs + `,"attachment_id":"5"}`, http.MethodDelete, 2},
	} {
		for name, answer := range unclear {
			if op.name == "delete" && name == "garbage" {
				continue // a deletion needs no answer body
			}
			t.Run(op.name+" "+name, func(t *testing.T) {
				calls := writeServer(t, func(*http.Request) (*http.Response, error) { return answer() })
				_, err := runAttachmentWrite(op.handler, uploadConnection(dir, "deck/2"), op.args)
				if err == nil || strings.Contains(err.Error(), bodyCanary) ||
					!strings.Contains(err.Error(), "may have been applied, check deckattachments.list before repeating") {
					t.Errorf("err = %v", err)
				}
				if len(*calls) != op.reads+1 || (*calls)[op.reads].method != op.method {
					t.Errorf("calls = %+v", *calls)
				}
			})
		}
		t.Run(op.name+" redirect", func(t *testing.T) {
			calls := writeServer(t, func(*http.Request) (*http.Response, error) {
				response := status(http.StatusTemporaryRedirect)
				response.Header.Set("Location", mainInstance+"/index.php/outside-canary")
				return response, nil
			})
			_, err := runAttachmentWrite(op.handler, uploadConnection(dir, "deck/2"), op.args)
			if err == nil || strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), "outside-canary") {
				t.Errorf("err = %v", err)
			}
			if len(*calls) != op.reads+1 {
				t.Errorf("calls = %d", len(*calls))
			}
		})
	}
}

func TestDeckAttachmentsWriteDescriptors(t *testing.T) {
	if !deckAttachmentsDelete.RequiresToolAllowList || deckAttachmentsDelete.Risk.Effect != capability.EffectDelete ||
		deckAttachmentsUpload.Risk.Effect != capability.EffectCreate || deckAttachmentsUpload.LocalFiles != config.LocalFilesRead {
		t.Errorf("descriptors = %+v, %+v", deckAttachmentsUpload, deckAttachmentsDelete)
	}
	for _, d := range []capability.Descriptor{deckAttachmentsUpload, deckAttachmentsDelete} {
		r := d.Risk
		if r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity == "" || r.Idempotency == "" {
			t.Errorf("risk of %s = %+v", d.ID, r)
		}
		metadata, _ := registry(t).ProviderMetadata(Provider)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("%s is in profile %s", d.ID, profile.ID)
				}
			}
		}
	}
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	_, err := deckACLWrite(t, []string{deckBoardsGet.ID}, deckAttachmentsDelete.ID,
		`{`+uploadArgs+`,"attachment_id":"5"}`, true)
	if err == nil || len(*calls) != 0 {
		t.Errorf("delete without a tools list entry: err = %v, requests = %d", err, len(*calls))
	}
}
