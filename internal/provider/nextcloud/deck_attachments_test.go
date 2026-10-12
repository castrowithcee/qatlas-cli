package nextcloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const attachmentsPath = deckCardsBase + "/5/cards/21/attachments"

// attachmentList has a Deck file, a reference into the Files tree, a deleted file, and a file of another card.
const attachmentList = `[` +
	`{"id":5,"cardId":21,"type":"deck_file","data":"plan.pdf","extendedData":{"filesize":5,"mimetype":"application/pdf"},` +
	`"createdBy":"dora","createdAt":1700000000,"lastModified":1700000100,"deletedAt":0},` +
	`{"id":6,"cardId":21,"type":"file","data":"Reports/q1.pdf","extendedData":{"filesize":9,"mimetype":"application/pdf"},"deletedAt":0},` +
	`{"id":7,"cardId":21,"type":"deck_file","data":"old.pdf","deletedAt":1700000200},` +
	`{"id":8,"cardId":22,"type":"deck_file","data":"other.pdf","deletedAt":0}]`

func attachmentConnection(dir string, targets ...string) *config.Resolved {
	resolved := localConnection("", dir)
	resolved.Target = ""
	resolved.Targets = targets
	return resolved
}

// attachmentServer answers the stack read, the attachment list, and the download of attachment 5.
func attachmentServer(t *testing.T, download func() *http.Response) *[]call {
	return serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/stacks"):
			return jsonResponse(http.StatusOK, cardStacksAnswer(false)), nil
		case strings.HasSuffix(request.URL.Path, "/attachments"):
			return jsonResponse(http.StatusOK, attachmentList), nil
		case strings.HasSuffix(request.URL.Path, "/attachments/deck_file/5"):
			return download(), nil
		}
		t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		return nil, errors.New("unexpected")
	})
}

func fileAnswer(body string) func() *http.Response {
	return func() *http.Response {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(body)),
			Header: http.Header{"Content-Type": {"application/pdf"}}, Body: io.NopCloser(strings.NewReader(body))}
	}
}

func getAttachment(resolved *config.Resolved, args string) (any, error) {
	red := &redact.Redactor{}
	return invokeDeckAttachmentsGet(context.Background(), resolved, resolver(red), red, json.RawMessage(args))
}

const getArgs = `"board_id":"2","stack_id":"5","card_id":"21","attachment_id":`

func TestDeckAttachmentsListHidesDeletedAndForeign(t *testing.T) {
	calls := attachmentServer(t, fileAnswer(""))
	red := &redact.Redactor{}
	result, err := invokeDeckAttachmentsList(context.Background(), attachmentConnection("", "deck/2"), resolver(red), red,
		json.RawMessage(`{"board_id":"2","stack_id":"5","card_id":"21"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*DeckAttachmentsResult)
	if got.Count != 2 || got.Truncated || got.Attachments[0].ID != "5" || got.Attachments[0].Name != "plan.pdf" ||
		got.Attachments[0].Size != 5 || got.Attachments[0].ContentType != "application/pdf" ||
		got.Attachments[0].CreatedBy != "dora" || !got.Attachments[0].Downloadable ||
		got.Attachments[1].Type != "file" || got.Attachments[1].Downloadable {
		t.Errorf("result = %+v", got)
	}
	if len(*calls) != 2 || (*calls)[0].url.Path != deckCardsBase || (*calls)[1].url.Path != attachmentsPath {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestDeckAttachmentsListCapsAndCutsUntrustedText(t *testing.T) {
	var items []string
	for i := 0; i < maxDeckAttachments+5; i++ {
		items = append(items, `{"id":`+strconv.Itoa(100+i)+`,"cardId":21,"type":"deck_file","data":"`+strings.Repeat("n", 300)+`"}`)
	}
	serve(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/stacks") {
			return jsonResponse(http.StatusOK, cardStacksAnswer(false)), nil
		}
		return jsonResponse(http.StatusOK, `[`+strings.Join(items, ",")+`]`), nil
	})
	red := &redact.Redactor{}
	result, err := invokeDeckAttachmentsList(context.Background(), attachmentConnection("", "deck"), resolver(red), red,
		json.RawMessage(`{"board_id":"2","stack_id":"5","card_id":"21"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*DeckAttachmentsResult)
	if got.Count != maxDeckAttachments || !got.Truncated || len(got.Attachments[0].Name) != maxDeckTitleBytes {
		t.Errorf("count = %d, truncated = %v, name = %d", got.Count, got.Truncated, len(got.Attachments[0].Name))
	}
}

func TestDeckAttachmentsGetInline(t *testing.T) {
	calls := attachmentServer(t, fileAnswer("hello"))
	result, err := getAttachment(attachmentConnection("", "deck/2"), `{`+getArgs+`"5"}`)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*DeckAttachmentContent)
	if got.ContentBase64 != base64.StdEncoding.EncodeToString([]byte("hello")) || got.Size != 5 || got.Name != "plan.pdf" ||
		got.ContentType != "application/pdf" {
		t.Errorf("result = %+v", got)
	}
	if len(*calls) != 3 || (*calls)[2].method != http.MethodGet || (*calls)[2].url.Path != attachmentsPath+"/deck_file/5" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestDeckAttachmentsGetTooLargeNamesLocalPath(t *testing.T) {
	attachmentServer(t, fileAnswer(strings.Repeat("x", maxFileBytes+1)))
	_, err := getAttachment(attachmentConnection("", "deck/2"), `{`+getArgs+`"5"}`)
	if err == nil || !strings.Contains(err.Error(), "use local_path") {
		t.Errorf("err = %v", err)
	}
}

func TestDeckAttachmentsGetToLocalPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "plan.pdf")
	attachmentServer(t, fileAnswer("hello"))
	result, err := getAttachment(attachmentConnection(dir, "deck/2"), `{`+getArgs+`"5","local_path":`+strconv.Quote(target)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(*DeckAttachmentDownload)
	if got.Size != 5 || got.SHA256 != helloSHA || got.Name != "plan.pdf" || got.ID != "5" {
		t.Errorf("result = %+v", got)
	}
	if encoded, _ := json.Marshal(result); strings.Contains(string(encoded), "content_base64") {
		t.Errorf("result carries content: %s", encoded)
	}
	if data, _ := os.ReadFile(target); string(data) != "hello" {
		t.Errorf("file = %q", data)
	}
	if _, err := getAttachment(attachmentConnection(dir, "deck/2"), `{`+getArgs+`"5","local_path":`+strconv.Quote(target)+`}`); !errors.Is(err, localfile.ErrOverwriteNeedsConfirmation) {
		t.Errorf("unconfirmed overwrite: %v", err)
	}
}

func TestDeckAttachmentsLocalPathOutsideReleaseIsRefusedBeforeIO(t *testing.T) {
	released, other := t.TempDir(), t.TempDir()
	refuse(t)
	var pathErr *localfile.PathError
	_, err := getAttachment(attachmentConnection(released, "deck/2"),
		`{`+getArgs+`"5","local_path":`+strconv.Quote(filepath.Join(other, "x.pdf"))+`}`)
	if !errors.As(err, &pathErr) || strings.Contains(err.Error(), other) {
		t.Errorf("err = %v", err)
	}
}

func TestDeckAttachmentsRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		args      string
		wantCalls int
		want      string
	}{
		"card in another stack": {`{"board_id":"2","stack_id":"6","card_id":"21","attachment_id":"5"}`, 1, ""},
		"unknown card":          {`{"board_id":"2","stack_id":"5","card_id":"99","attachment_id":"5"}`, 1, ""},
		"deleted stack":         {`{"board_id":"2","stack_id":"7","card_id":"21","attachment_id":"5"}`, 1, ""},
		"attachment of a card":  {`{` + getArgs + `"8"}`, 2, ""},
		"unknown attachment":    {`{` + getArgs + `"99"}`, 2, ""},
		"deleted attachment":    {`{` + getArgs + `"7"}`, 2, ""},
		"files reference":       {`{` + getArgs + `"6"}`, 2, "deck_file"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := attachmentServer(t, func() *http.Response {
				t.Error("a download was requested")
				return nil
			})
			_, err := getAttachment(attachmentConnection("", "deck/2"), tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "q1.pdf") ||
				strings.Contains(err.Error(), "other.pdf") {
				t.Errorf("err = %v", err)
			}
			if len(*calls) != tc.wantCalls {
				t.Errorf("calls = %d, want %d", len(*calls), tc.wantCalls)
			}
		})
	}
}

func TestDeckAttachmentsUnboundBoardIsRefusedLocally(t *testing.T) {
	refuse(t)
	dir := t.TempDir()
	for _, args := range []string{
		`{"board_id":"3","stack_id":"5","card_id":"21"}`,
		`{"board_id":"3","stack_id":"5","card_id":"21","attachment_id":"5"}`,
		`{"board_id":"3","stack_id":"5","card_id":"21","attachment_id":"5","local_path":` + strconv.Quote(filepath.Join(dir, "o")) + `}`,
	} {
		red := &redact.Redactor{}
		resolved := attachmentConnection(dir, "deck/2")
		var err error
		if strings.Contains(args, "attachment_id") {
			_, err = invokeDeckAttachmentsGet(context.Background(), resolved, resolver(red), red, json.RawMessage(args))
		} else {
			_, err = invokeDeckAttachmentsList(context.Background(), resolved, resolver(red), red, json.RawMessage(args))
		}
		if err == nil || !strings.Contains(err.Error(), "not bound") {
			t.Errorf("args %s: err = %v", args, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("an unbound board left %d local files", len(entries))
	}
}

func TestDeckAttachmentsDownloadFailures(t *testing.T) {
	dir := t.TempDir()
	local := `,"local_path":` + strconv.Quote(filepath.Join(dir, "plan.pdf"))
	for name, tc := range map[string]struct {
		status int
		want   string
	}{"server error": {http.StatusBadGateway, "Nextcloud"}, "redirect": {http.StatusFound, "redirect"}} {
		for mode, extra := range map[string]string{"inline": "", "local": local} {
			t.Run(name+" "+mode, func(t *testing.T) {
				calls := attachmentServer(t, func() *http.Response {
					return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {mainInstance + "/index.php/outside-canary"}},
						Body: io.NopCloser(strings.NewReader(bodyCanary))}
				})
				_, err := getAttachment(attachmentConnection(dir, "deck/2"), `{`+getArgs+`"5"`+extra+`}`)
				if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "outside-canary") ||
					!strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
					t.Errorf("err = %v", err)
				}
				for _, c := range *calls {
					if strings.Contains(c.url.Path, "outside-canary") {
						t.Errorf("the redirect was followed: %s", c.url)
					}
				}
				if len(*calls) != 3 {
					t.Errorf("calls = %d, want 3", len(*calls))
				}
			})
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "plan.pdf")); err == nil {
		t.Error("a failed download left a file")
	}
}

func TestDeckAttachmentDescriptors(t *testing.T) {
	if deckAttachmentsGet.LocalFiles != config.LocalFilesWrite || deckAttachmentsList.LocalFiles != "" {
		t.Errorf("local files = %q, %q", deckAttachmentsGet.LocalFiles, deckAttachmentsList.LocalFiles)
	}
	for _, d := range []string{deckAttachmentsGet.ID, deckAttachmentsList.ID} {
		metadata, _ := registry(t).ProviderMetadata(Provider)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == d {
					t.Errorf("%s is in profile %s", d, profile.ID)
				}
			}
		}
	}
}
