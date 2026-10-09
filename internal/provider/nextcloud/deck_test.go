package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

const deckCanary = "deck-provider-canary-nextcloud-4a92"

func jsonResponse(status int, body string) *http.Response {
	response := ocsResponse(status, body)
	response.Header.Set("Content-Type", "application/json")
	return response
}

func deckInvoke(t *testing.T, targets []string, operation, args string) (json.RawMessage, error) {
	t.Helper()
	response, err := accountClient(t, targets...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args),
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func board(id, title string) string {
	return `{"id":` + id + `,"title":"` + title + `","color":"0087C5","archived":false,"deletedAt":0,` +
		`"owner":{"uid":"dora","displayname":"Dora","type":0},"acl":[],"users":[]}`
}

func TestDeckBoardsListKeepsOnlyBoundBoards(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[`+board("1", "One")+`,`+board("2", "Two")+`,`+
			strings.Replace(board("3", "Gone"), `"deletedAt":0`, `"deletedAt":99`, 1)+`]`), nil
	})
	result, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deckboards.list", `{}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var listed DeckBoardsResult
	if err := json.Unmarshal(result, &listed); err != nil || listed.Count != 1 || listed.Boards[0].ID != "2" ||
		listed.Boards[0].Owner.ID != "dora" {
		t.Errorf("result = %s, %v", result, err)
	}
	request := (*calls)[0]
	if request.method != http.MethodGet || request.url.Path != "/index.php/apps/deck/api/v1.1/boards" {
		t.Errorf("request = %s %s", request.method, request.url)
	}

	result, err = deckInvoke(t, []string{"deck"}, "nextcloud.deckboards.list", `{}`)
	if err != nil || !strings.Contains(string(result), `"count":2`) || strings.Contains(string(result), "Gone") {
		t.Errorf("all boards = %s, %v", result, err)
	}
}

func TestDeckRequestsAreFixedAndJSON(t *testing.T) {
	var header http.Header
	serve(t, func(request *http.Request) (*http.Response, error) {
		header = request.Header
		return jsonResponse(http.StatusOK, `[]`), nil
	})
	if _, err := deckInvoke(t, []string{"deck"}, "nextcloud.deckboards.list", `{}`); err != nil {
		t.Fatal(err)
	}
	if header.Get("OCS-APIRequest") != "true" || header.Get("Accept") != "application/json" {
		t.Errorf("headers = %v", header)
	}
}

func TestDeckUnboundBoardIsRefusedBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, nil
	})
	for name, tc := range map[string]struct {
		targets []string
		op, arg string
	}{
		"get":            {[]string{"deck/2"}, "nextcloud.deckboards.get", `{"board_id":"9"}`},
		"stacks":         {[]string{"deck/2"}, "nextcloud.deckstacks.list", `{"board_id":"9"}`},
		"card":           {[]string{"deck/2"}, "nextcloud.deckcards.get", `{"board_id":"9","stack_id":"1","card_id":"1"}`},
		"no deck target": {[]string{"folder/Reports"}, "nextcloud.deckboards.list", `{}`},
		"no deck, board": {[]string{"folder/Reports"}, "nextcloud.deckboards.get", `{"board_id":"2"}`},
		"non numeric":    {[]string{"deck"}, "nextcloud.deckboards.get", `{"board_id":"2/../3"}`},
		"non numeric id": {[]string{"deck"}, "nextcloud.deckcards.get", `{"board_id":"2","stack_id":"x","card_id":"1"}`},
		"free parameter": {[]string{"deck"}, "nextcloud.deckboards.get", `{"board_id":"2","path":"/x"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deckInvoke(t, tc.targets, tc.op, tc.arg)
			if err == nil {
				t.Fatal("the request was accepted")
			}
			if tc.targets[0] == "deck/2" && strings.Contains(err.Error(), "9") {
				t.Errorf("the refusal names the board: %v", err)
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestDeckBoardGetReportsLabelsACLAndMembersBounded(t *testing.T) {
	long := strings.Repeat("a", 300)
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/index.php/apps/deck/api/v1.1/boards/2" {
			t.Errorf("path = %s", request.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{"id":2,"title":"`+long+`","color":"fff","archived":false,"deletedAt":0,`+
			`"owner":{"uid":"dora","displayname":"Dora","type":0},`+
			`"labels":[{"id":5,"title":"Bug","color":"ff0000"}],`+
			`"acl":[{"id":1,"type":1,"permissionEdit":true,"permissionShare":false,"permissionManage":false,"owner":false,`+
			`"participant":{"uid":"team","displayname":"Team","type":1}}],`+
			`"users":[{"uid":"bob","displayname":"Bob","type":0}],"extra":"`+deckCanary+`"}`), nil
	})
	result, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deckboards.get", `{"board_id":"2"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var detail DeckBoardDetail
	if err := json.Unmarshal(result, &detail); err != nil || len(detail.Labels) != 1 || detail.Labels[0].ID != "5" ||
		len(detail.ACL) != 1 || detail.ACL[0].Participant.Type != "group" || !detail.ACL[0].CanEdit ||
		len(detail.Members) != 1 || detail.Members[0].ID != "bob" {
		t.Errorf("detail = %s, %v", result, err)
	}
	if len(detail.Title) != maxDeckTitleBytes || !detail.Truncated || strings.Contains(string(result), deckCanary) {
		t.Errorf("title length %d, truncated %v, result %s", len(detail.Title), detail.Truncated, result)
	}
}

func stackJSON(id string, cards ...string) string {
	return `{"id":` + id + `,"title":"S` + id + `","order":0,"boardId":2,"cards":[` + strings.Join(cards, ",") + `]}`
}

func cardJSON(id, stack, description string) string {
	return `{"id":` + id + `,"stackId":` + stack + `,"title":"C` + id + `","description":"` + description +
		`","order":1,"archived":false,"done":null,"duedate":"2026-12-01T00:00:00+00:00",` +
		`"owner":"dora","labels":[{"id":5,"title":"Bug","color":"f00"}],` +
		`"assignedUsers":[{"participant":{"uid":"bob","displayname":"Bob","type":0},"type":0}],` +
		`"createdAt":1700000000,"lastModified":1700000100}`
}

func TestDeckStacksListMarksCutDescriptions(t *testing.T) {
	long := strings.Repeat("ä", maxDeckListText)
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[`+stackJSON("3", cardJSON("21", "3", long), cardJSON("22", "3", "short"))+`]`), nil
	})
	result, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deckstacks.list", `{"board_id":"2"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var listed DeckStacksResult
	if err := json.Unmarshal(result, &listed); err != nil || listed.Cards != 2 || !listed.Truncated {
		t.Fatalf("result = %s, %v", result, err)
	}
	first := listed.Stacks[0].Cards[0]
	if !first.Truncated || len(first.Description) > maxDeckListText || !strings.HasPrefix(long, first.Description) ||
		listed.Stacks[0].Cards[1].Truncated || first.Owner.ID != "dora" || first.AssignedTo[0].ID != "bob" {
		t.Errorf("card = %+v", first)
	}
	if (*calls)[0].url.Path != "/index.php/apps/deck/api/v1.1/boards/2/stacks" {
		t.Errorf("path = %s", (*calls)[0].url.Path)
	}

	_, err = deckInvoke(t, []string{"deck/2"}, "nextcloud.deckstacks.list", `{"board_id":"2","archived":true}`)
	if err != nil || (*calls)[1].url.Path != "/index.php/apps/deck/api/v1.1/boards/2/stacks/archived" {
		t.Errorf("archived request = %v, %v", (*calls)[1].url, err)
	}
}

func TestDeckStacksListCapsCards(t *testing.T) {
	cards := make([]string, 0, maxDeckCards+5)
	for i := 0; i < maxDeckCards+5; i++ {
		cards = append(cards, cardJSON("1", "3", ""))
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[`+stackJSON("3", cards...)+`]`), nil
	})
	result, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deckstacks.list", `{"board_id":"2"}`)
	var listed DeckStacksResult
	if err != nil || json.Unmarshal(result, &listed) != nil || listed.Cards != maxDeckCards || !listed.Truncated {
		t.Errorf("cards = %d, truncated = %v, err = %v", listed.Cards, listed.Truncated, err)
	}
}

func TestDeckCardGetChecksTheBoardHierarchyFirst(t *testing.T) {
	var paths []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/index.php/apps/deck/api/v1.1/boards/2/stacks":
			return jsonResponse(http.StatusOK, `[`+stackJSON("3", cardJSON("21", "3", "d"))+`]`), nil
		case "/index.php/apps/deck/api/v1.1/boards/2/stacks/archived":
			return jsonResponse(http.StatusOK, `[`+stackJSON("3", cardJSON("23", "3", "d"))+`]`), nil
		case "/index.php/apps/deck/api/v1.1/boards/2/stacks/3/cards/21":
			return jsonResponse(http.StatusOK, cardJSON("21", "3", "full text")), nil
		case "/index.php/apps/deck/api/v1.1/boards/2/stacks/3/cards/23":
			return jsonResponse(http.StatusOK, cardJSON("23", "3", "archived text")), nil
		}
		// A foreign card is reachable by ID alone on the instance; it must never be asked for.
		t.Errorf("unexpected request %s", request.URL.Path)
		return jsonResponse(http.StatusOK, cardJSON("99", "8", "foreign "+deckCanary)), nil
	})
	result, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deckcards.get", `{"board_id":"2","stack_id":"3","card_id":"21"}`)
	var card DeckCard
	if err != nil || json.Unmarshal(result, &card) != nil || card.ID != "21" || card.Description != "full text" ||
		card.StackID != "3" || card.Labels[0].Title != "Bug" {
		t.Fatalf("card = %s, %v", result, err)
	}
	if result, err = deckInvoke(t, []string{"deck/2"}, "nextcloud.deckcards.get", `{"board_id":"2","stack_id":"3","card_id":"23"}`); err != nil ||
		!strings.Contains(string(result), "archived text") {
		t.Errorf("archived card = %s, %v", result, err)
	}

	for name, args := range map[string]string{
		"card of another board": `{"board_id":"2","stack_id":"3","card_id":"99"}`,
		"foreign stack":         `{"board_id":"2","stack_id":"8","card_id":"21"}`,
		"card in wrong stack":   `{"board_id":"2","stack_id":"3","card_id":"98"}`,
	} {
		before := len(paths)
		_, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deckcards.get", args)
		if err == nil || strings.Contains(err.Error(), deckCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
		for _, path := range paths[before:] {
			if strings.Contains(path, "/cards/") {
				t.Errorf("%s: card was requested: %s", name, path)
			}
		}
	}
}

func TestDeckCardGetRejectsAnAnswerFromAnotherStack(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/stacks") {
			return jsonResponse(http.StatusOK, `[`+stackJSON("3", cardJSON("21", "3", "d"))+`]`), nil
		}
		return jsonResponse(http.StatusOK, cardJSON("21", "8", "other "+deckCanary)), nil
	})
	result, err := deckInvoke(t, []string{"deck/2"}, "nextcloud.deckcards.get", `{"board_id":"2","stack_id":"3","card_id":"21"}`)
	if err == nil || strings.Contains(string(result), deckCanary) {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestDeckCardDescriptionIsCappedAtEightKiB(t *testing.T) {
	long := strings.Repeat("x", maxDeckCardText+10)
	serve(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/stacks") {
			return jsonResponse(http.StatusOK, `[`+stackJSON("3", cardJSON("21", "3", "d"))+`]`), nil
		}
		return jsonResponse(http.StatusOK, cardJSON("21", "3", long)), nil
	})
	result, err := deckInvoke(t, []string{"deck"}, "nextcloud.deckcards.get", `{"board_id":"2","stack_id":"3","card_id":"21"}`)
	var card DeckCard
	if err != nil || json.Unmarshal(result, &card) != nil || len(card.Description) != maxDeckCardText || !card.Truncated {
		t.Errorf("len = %d, truncated = %v, err = %v", len(card.Description), card.Truncated, err)
	}
}

func TestDeckRedirectAndMissingAppAreClear(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		want   string
	}{
		"redirect": {http.StatusFound, messageRedirect},
		"missing":  {http.StatusNotFound, messageDeckNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, func(*http.Request) (*http.Response, error) {
				response := jsonResponse(tc.status, bodyCanary)
				response.Header.Set("Location", "https://evil.example.invalid/"+bodyCanary)
				return response, nil
			})
			_, err := deckInvoke(t, []string{"deck"}, "nextcloud.deckboards.list", `{}`)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), bodyCanary) ||
				strings.Contains(err.Error(), "evil") {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDeckReadProfileHoldsExactlyTheFourReads(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if profile.ID != "deck-read" {
			continue
		}
		want := "nextcloud.deckboards.list nextcloud.deckboards.get nextcloud.deckstacks.list nextcloud.deckcards.get"
		if strings.Join(profile.Tools, " ") != want {
			t.Errorf("deck-read = %v", profile.Tools)
		}
		return
	}
	t.Error("no deck-read profile")
}
