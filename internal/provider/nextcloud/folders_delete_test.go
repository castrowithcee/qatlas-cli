package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func deleteFolder(confirmed bool, args string) error {
	red := &redact.Redactor{}
	ctx := context.Background()
	if confirmed {
		ctx = capability.WithConfirmed(ctx)
	}
	_, err := invokeFoldersDelete(ctx, reportsConnection(), resolver(red), red, json.RawMessage(args))
	return err
}

func folderAnswer(entry string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == methodPropfind {
			return xmlResponse(http.StatusMultiStatus, multistatus(entry)), nil
		}
		return status(204), nil
	}
}

func TestDeleteToolsRequireAToolAllowListAndSitInNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, d := range reg.Provider(Provider) {
		if d.ID != filesDelete.ID && d.ID != foldersDelete.ID && d.ID != trashDelete.ID && d.ID != systemtagsDelete.ID &&
			d.ID != sharesCreate.ID && d.ID != sharesUpdate.ID && d.ID != sharesDelete.ID && d.ID != commentsDelete.ID && d.ID != deckBoardsDelete.ID &&
			d.ID != deckStacksDelete.ID && d.ID != talkMessagesDelete.ID && d.ID != deckCardsDelete.ID &&
			d.ID != talkRoomsDelete.ID && d.ID != talkParticipantsRemove.ID && d.ID != talkParticipantsModerator.ID &&
			d.ID != eventsDelete.ID {
			if d.RequiresToolAllowList {
				t.Errorf("%s requires a tools list", d.ID)
			}
			continue
		}
		found++
		if !d.RequiresToolAllowList || d.Risk.Effect != capability.EffectDelete && d.Group != groupShares &&
			d.ID != talkParticipantsModerator.ID ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Errorf("%s = %+v", d.ID, d)
		}
	}
	if found != 16 || filesDelete.Version != 2 || foldersDelete.Version != 1 {
		t.Fatalf("found = %d, versions = %d/%d", found, filesDelete.Version, foldersDelete.Version)
	}
	if !strings.Contains(foldersDelete.Description, "files_trashbin") || !strings.Contains(foldersDelete.Description, "together with everything") {
		t.Errorf("description = %q", foldersDelete.Description)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if id == filesDelete.ID || id == foldersDelete.ID || id == trashDelete.ID || id == systemtagsDelete.ID || id == commentsDelete.ID ||
				id == deckBoardsDelete.ID || id == deckStacksDelete.ID || id == talkMessagesDelete.ID ||
				id == deckCardsDelete.ID || id == talkRoomsDelete.ID || id == talkParticipantsRemove.ID ||
				id == talkParticipantsModerator.ID || id == talkParticipantsAdd.ID || id == eventsDelete.ID {
				t.Errorf("profile %s contains %s", p.ID, id)
			}
		}
	}
}

func TestFoldersDeleteSendsOneETagBoundDelete(t *testing.T) {
	var ifMatch string
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			ifMatch = r.Header.Get("If-Match")
		}
		return folderAnswer(folderXML(aliceRoot+"/2026/Old/", "Old", "9", "0"))(r)
	})
	if err := deleteFolder(true, `{"path":"2026/Old","etag":"abc"}`); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].method != methodPropfind || (*calls)[0].depth != "0" ||
		(*calls)[1].method != http.MethodDelete || ifMatch != `"abc"` {
		t.Fatalf("calls = %+v, If-Match = %q", *calls, ifMatch)
	}
}

func TestFoldersDeleteRefusesLocally(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, args := range []string{
		`{"path":"","etag":"abc"}`, `{"path":"../x","etag":"abc"}`, `{"path":"a","etag":"*"}`, `{"path":"a","etag":"\"*\""}`,
	} {
		_, err := invokeFoldersDelete(capability.WithConfirmed(context.Background()), reportsConnection(), res, &redact.Redactor{}, json.RawMessage(args))
		if err == nil {
			t.Errorf("accepted %s", args)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestFoldersDeleteRefusesAFileAfterTheStat(t *testing.T) {
	calls := serve(t, folderAnswer(fileXML(aliceRoot+"/a.txt", "a.txt", "1", "5")))
	err := deleteFolder(true, `{"path":"a.txt","etag":"abc"}`)
	if err == nil || len(*calls) != 1 || (*calls)[0].method != methodPropfind {
		t.Fatalf("err = %v, calls = %+v", err, *calls)
	}
}

func TestFoldersDeleteRequiresConfirmation(t *testing.T) {
	// The application core enforces confirmation from the descriptor; the handler is never reached without it.
	if foldersDelete.Risk.Confirmation != capability.ConfirmationRequired {
		t.Fatal("confirmation is not required")
	}
}

func TestFoldersDeleteOutcomes(t *testing.T) {
	for _, code := range []int{412, 301, 307, 404} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete {
				return status(code), nil
			}
			return folderAnswer(folderXML(aliceRoot+"/Old/", "Old", "9", "0"))(r)
		})
		err := deleteFolder(true, `{"path":"Old","etag":"abc"}`)
		if err == nil || strings.Contains(err.Error(), "may have been") || strings.Contains(err.Error(), bodyCanary) || len(*calls) != 2 {
			t.Errorf("%d: err = %v, calls = %d", code, err, len(*calls))
		}
	}
	answers := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"503":     func() (*http.Response, error) { return status(503), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
	}
	for name, answer := range answers {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete {
				return answer()
			}
			return folderAnswer(folderXML(aliceRoot+"/Old/", "Old", "9", "0"))(r)
		})
		err := deleteFolder(true, `{"path":"Old","etag":"abc"}`)
		if err == nil || !strings.Contains(err.Error(), "folder may have been deleted") ||
			strings.Contains(err.Error(), bodyCanary) || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(*calls))
		}
	}
}
