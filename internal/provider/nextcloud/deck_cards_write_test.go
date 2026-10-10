package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const deckCardsBase = deckBoardsPath + "/2/stacks"

var deckCardTools = []string{deckBoardsGet.ID, deckStacksList.ID, deckCardsGet.ID, deckCardsCreate.ID,
	deckCardsUpdate.ID, deckCardsMove.ID, deckCardsDelete.ID}

func deckCardWrite(t *testing.T, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	response, err := deckWriteCore(t, deckCardTools, targets...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirm,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

// fullCard carries every field Deck needs back on a change; the description has quotes and a line break.
func fullCard(id, stack, archived string) string {
	return `{"id":` + id + `,"stackId":` + stack + `,"title":"Plan","description":"line \"one\"\nline two","type":"plain",` +
		`"order":2,"archived":` + archived + `,"done":"2026-11-01T10:00:00+00:00","duedate":"2026-12-01T00:00:00+00:00",` +
		`"startdate":"2026-11-15T00:00:00+00:00","color":"ff0000","owner":{"uid":"dora","displayname":"Dora","type":0},` +
		`"labels":[],"assignedUsers":[],"createdAt":1700000000,"lastModified":1700000100}`
}

// cardStacksAnswer lists stack 5 with card 21 and an empty stack 6; a stack 7 is deleted.
func cardStacksAnswer(archived bool) string {
	card := fullCard("21", "5", "false")
	if archived {
		card = fullCard("21", "5", "true")
	}
	return `[{"id":5,"title":"Todo","order":0,"cards":[` + card + `]},{"id":6,"title":"Doing","order":1,"cards":[]},` +
		`{"id":7,"title":"Gone","order":2,"deletedAt":4,"cards":[]}]`
}

func cardServer(t *testing.T, write func(*http.Request) (*http.Response, error)) *[]call {
	return serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return jsonResponse(http.StatusOK, cardStacksAnswer(strings.HasSuffix(request.URL.Path, "/archived"))), nil
		}
		return write(request)
	})
}

var deckCardOps = []struct{ name, id, args, method string }{
	{"create", "nextcloud.deckcards.create", `{"board_id":"2","stack_id":"6","title":"x"}`, http.MethodPost},
	{"update", "nextcloud.deckcards.update", `{"board_id":"2","stack_id":"5","card_id":"21","last_modified":1700000100,"title":"x"}`, http.MethodPut},
	{"move", "nextcloud.deckcards.move", `{"board_id":"2","stack_id":"5","card_id":"21","target_stack_id":"6"}`, http.MethodPut},
	{"delete", "nextcloud.deckcards.delete", `{"board_id":"2","stack_id":"5","card_id":"21"}`, http.MethodDelete},
}

func TestDeckCardsRefuseBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	upd := `"board_id":"2","stack_id":"5","card_id":"21","last_modified":1,`
	for name, tc := range map[string]struct{ op, args string }{
		"create unbound":      {"create", `{"board_id":"9","stack_id":"6","title":"x"}`},
		"update unbound":      {"update", `{"board_id":"9","stack_id":"5","card_id":"21","last_modified":1,"title":"x"}`},
		"move unbound":        {"move", `{"board_id":"9","stack_id":"5","card_id":"21","target_stack_id":"6"}`},
		"delete unbound":      {"delete", `{"board_id":"9","stack_id":"5","card_id":"21"}`},
		"bad board":           {"create", `{"board_id":"2/..","stack_id":"6","title":"x"}`},
		"bad stack":           {"create", `{"board_id":"2","stack_id":"6/..","title":"x"}`},
		"bad card":            {"delete", `{"board_id":"2","stack_id":"5","card_id":"2x"}`},
		"bad target":          {"move", `{"board_id":"2","stack_id":"5","card_id":"21","target_stack_id":"6?"}`},
		"no title":            {"create", `{"board_id":"2","stack_id":"6"}`},
		"empty title":         {"create", `{"board_id":"2","stack_id":"6","title":" "}`},
		"control description": {"create", "{\"board_id\":\"2\",\"stack_id\":\"6\",\"title\":\"x\",\"description\":\"a\\u0007\"}"},
		"long description":    {"create", `{"board_id":"2","stack_id":"6","title":"x","description":"` + strings.Repeat("a", 8193) + `"}`},
		"bad due":             {"create", `{"board_id":"2","stack_id":"6","title":"x","duedate":"tomorrow"}`},
		"bad order":           {"create", `{"board_id":"2","stack_id":"6","title":"x","order":10001}`},
		"create + card":       {"create", `{"board_id":"2","stack_id":"6","card_id":"21","title":"x"}`},
		"no last_modified":    {"update", `{"board_id":"2","stack_id":"5","card_id":"21","title":"x"}`},
		"no change":           {"update", `{"board_id":"2","stack_id":"5","card_id":"21","last_modified":1}`},
		"restore + title":     {"update", `{` + upd[:len(upd)-1] + `,"archived":false,"title":"x"}`},
		"update + order":      {"update", `{` + upd + `"title":"x","order":1}`},
		"move + title":        {"move", `{"board_id":"2","stack_id":"5","card_id":"21","target_stack_id":"6","title":"x"}`},
		"move no target":      {"move", `{"board_id":"2","stack_id":"5","card_id":"21"}`},
		"delete + title":      {"delete", `{"board_id":"2","stack_id":"5","card_id":"21","title":"x"}`},
		"free parameter":      {"create", `{"board_id":"2","stack_id":"6","title":"x","path":"/y"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards."+tc.op, tc.args, true)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.HasSuffix(name, "unbound") && strings.Contains(err.Error(), "9") {
				t.Errorf("the refusal names the board: %v", err)
			}
		})
	}
	for _, op := range deckCardOps {
		if _, err := deckCardWrite(t, []string{"deck/2"}, op.id, op.args, false); err == nil {
			t.Errorf("%s without confirm was accepted", op.id)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestDeckCardsRefuseObjectsOutsideTheBoardWithoutMutation(t *testing.T) {
	for name, tc := range map[string]struct{ id, args string }{
		"create foreign stack": {"create", `{"board_id":"2","stack_id":"77","title":"x"}`},
		"create deleted stack": {"create", `{"board_id":"2","stack_id":"7","title":"x"}`},
		"move foreign target":  {"move", `{"board_id":"2","stack_id":"5","card_id":"21","target_stack_id":"77"}`},
		"move deleted target":  {"move", `{"board_id":"2","stack_id":"5","card_id":"21","target_stack_id":"7"}`},
		"move wrong stack":     {"move", `{"board_id":"2","stack_id":"6","card_id":"21","target_stack_id":"5"}`},
		"move foreign card":    {"move", `{"board_id":"2","stack_id":"5","card_id":"99","target_stack_id":"6"}`},
		"update wrong stack":   {"update", `{"board_id":"2","stack_id":"6","card_id":"21","last_modified":1700000100,"title":"x"}`},
		"update foreign card":  {"update", `{"board_id":"2","stack_id":"5","card_id":"99","last_modified":1700000100,"title":"x"}`},
		"delete wrong stack":   {"delete", `{"board_id":"2","stack_id":"6","card_id":"21"}`},
		"delete foreign stack": {"delete", `{"board_id":"2","stack_id":"77","card_id":"21"}`},
		"restore active card":  {"update", `{"board_id":"2","stack_id":"6","card_id":"21","last_modified":1700000100,"archived":false}`},
		"restore unknown card": {"update", `{"board_id":"2","stack_id":"5","card_id":"98","last_modified":1700000100,"archived":false}`},
		"changed card":         {"update", `{"board_id":"2","stack_id":"5","card_id":"21","last_modified":1700000099,"title":"x"}`},
		"changed card restore": {"update", `{"board_id":"2","stack_id":"5","card_id":"21","last_modified":5,"archived":false}`},
	} {
		t.Run(name, func(t *testing.T) {
			calls := cardServer(t, func(*http.Request) (*http.Response, error) {
				t.Error("a mutation was sent")
				return nil, errors.New("no mutation expected")
			})
			_, err := deckCardWrite(t, []string{"deck"}, "nextcloud.deckcards."+tc.id, tc.args, true)
			if err == nil {
				t.Fatal("accepted")
			}
			for _, leaked := range []string{"77", "99", "98"} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("the refusal names %s: %v", leaked, err)
				}
			}
			if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
				t.Errorf("calls = %+v, want one read", *calls)
			}
		})
	}
	// An archived card is neither changed, moved, nor deleted.
	for _, op := range []string{"move", "delete"} {
		calls := serve(t, func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodGet {
				t.Error("a mutation was sent")
			}
			return jsonResponse(http.StatusOK, `[{"id":5,"title":"Todo","order":0,"cards":[]},{"id":6,"title":"D","order":1,"cards":[]}]`), nil
		})
		args := `{"board_id":"2","stack_id":"5","card_id":"21"` + map[string]string{"move": `,"target_stack_id":"6"}`, "delete": `}`}[op]
		if _, err := deckCardWrite(t, []string{"deck"}, "nextcloud.deckcards."+op, args, true); err == nil || len(*calls) != 1 {
			t.Errorf("%s of an archived card: %v, calls %d", op, err, len(*calls))
		}
	}
}

func TestDeckCardsChangedCardMessage(t *testing.T) {
	cardServer(t, func(*http.Request) (*http.Response, error) { return status(500), nil })
	_, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.update",
		`{"board_id":"2","stack_id":"5","card_id":"21","last_modified":1,"title":"x"}`, true)
	if err == nil || !strings.Contains(err.Error(), "changed since it was read") {
		t.Errorf("err = %v", err)
	}
}

func TestDeckCardsUpdateSendsTheReadState(t *testing.T) {
	calls := cardServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, fullCard("21", "5", "false")), nil
	})
	result, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.update",
		`{"board_id":"2","stack_id":"5","card_id":"21","last_modified":1700000100,"title":"New \"t\""}`, true)
	if err != nil || !strings.Contains(string(result), `"updated":true`) || !strings.Contains(string(result), `"id":"21"`) {
		t.Fatalf("update = %s, %v", result, err)
	}
	want := `{"title":"New \"t\"","type":"plain","owner":"dora","description":"line \"one\"\nline two","order":2,` +
		`"duedate":"2026-12-01T00:00:00+00:00","startdate":"2026-11-15T00:00:00+00:00","archived":false,` +
		`"done":"2026-11-01T10:00:00+00:00","color":"ff0000"}`
	if len(*calls) != 2 {
		t.Fatalf("calls = %+v", *calls)
	}
	if c := (*calls)[1]; c.method != http.MethodPut || c.url.Path != deckCardsBase+"/5/cards/21" || c.body != want {
		t.Errorf("update call = %+v\nwant body %s", c, want)
	}
	if (*calls)[0].url.Path != deckCardsBase {
		t.Errorf("pre-read = %s", (*calls)[0].url.Path)
	}
	// description and due time change, an empty due time clears it
	if _, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.update",
		`{"board_id":"2","stack_id":"5","card_id":"21","last_modified":1700000100,"description":"","duedate":""}`, true); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte((*calls)[3].body), &body); err != nil {
		t.Fatal(err)
	}
	if body["description"] != "" || body["duedate"] != nil || body["title"] != "Plan" || body["done"] == nil {
		t.Errorf("clear body = %v", body)
	}
}

func TestDeckCardsRestoreReadsTheArchive(t *testing.T) {
	calls := cardServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, fullCard("21", "5", "false")), nil
	})
	if _, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.update",
		`{"board_id":"2","stack_id":"5","card_id":"21","last_modified":1700000100,"archived":false}`, true); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].url.Path != deckCardsBase+"/archived" || !strings.Contains((*calls)[1].body, `"archived":false`) {
		t.Errorf("calls = %+v", *calls)
	}
	// archiving reads the active cards and sends true
	if _, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.update",
		`{"board_id":"2","stack_id":"5","card_id":"21","last_modified":1700000100,"archived":true}`, true); err != nil {
		t.Fatal(err)
	}
	if (*calls)[2].url.Path != deckCardsBase || !strings.Contains((*calls)[3].body, `"archived":true`) {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestDeckCardsCreateMoveDeleteSendOneFixedRequest(t *testing.T) {
	calls := cardServer(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost:
			stack := "5"
			if strings.Contains(request.URL.Path, "/stacks/6/") {
				stack = "6"
			}
			return jsonResponse(http.StatusOK, fullCard("30", stack, "false")), nil
		case strings.HasSuffix(request.URL.Path, "/reorder"):
			return jsonResponse(http.StatusOK, `[`+fullCard("22", "6", "false")+`,`+fullCard("21", "6", "false")+`]`), nil
		}
		return jsonResponse(http.StatusOK, fullCard("21", "5", "false")), nil
	})
	result, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.create",
		`{"board_id":"2","stack_id":"5","title":" Do \"it\" ","description":"a\nb","duedate":"2026-12-24T10:00:00Z"}`, true)
	if err != nil || !strings.Contains(string(result), `"created":true`) || !strings.Contains(string(result), `"id":"30"`) {
		t.Fatalf("create = %s, %v", result, err)
	}
	if c := (*calls)[1]; len(*calls) != 2 || c.method != http.MethodPost || c.url.Path != deckCardsBase+"/5/cards" ||
		c.body != `{"title":"Do \"it\"","type":"plain","order":3,"description":"a\nb","duedate":"2026-12-24T10:00:00Z"}` {
		t.Errorf("create call = %+v", c)
	}
	if _, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.create",
		`{"board_id":"2","stack_id":"6","title":"x","order":0}`, true); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[3].body; got != `{"title":"x","type":"plain","order":0,"description":""}` {
		t.Errorf("explicit order body = %s", got)
	}
	result, err = deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.move",
		`{"board_id":"2","stack_id":"5","card_id":"21","target_stack_id":"6"}`, true)
	if err != nil || !strings.Contains(string(result), `"moved":true`) || !strings.Contains(string(result), `"id":"21"`) {
		t.Fatalf("move = %s, %v", result, err)
	}
	if c := (*calls)[5]; len(*calls) != 6 || c.method != http.MethodPut || c.url.Path != deckCardsBase+"/5/cards/21/reorder" ||
		c.body != `{"stackId":6,"order":0}` {
		t.Errorf("move call = %+v", c)
	}
	if _, err := deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.move",
		`{"board_id":"2","stack_id":"5","card_id":"21","target_stack_id":"5","order":4}`, true); err == nil {
		// the answer holds the card in stack 6, not in the requested stack 5
		t.Error("an answer from another stack was accepted")
	}
	if got := (*calls)[7].body; got != `{"stackId":5,"order":4}` {
		t.Errorf("explicit move body = %s", got)
	}
	result, err = deckCardWrite(t, []string{"deck/2"}, "nextcloud.deckcards.delete",
		`{"board_id":"2","stack_id":"5","card_id":"21"}`, true)
	if err != nil || !strings.Contains(string(result), `"deleted":true`) || !strings.Contains(string(result), `"card_id":"21"`) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if c := (*calls)[9]; len(*calls) != 10 || c.method != http.MethodDelete || c.url.Path != deckCardsBase+"/5/cards/21" || c.body != "" {
		t.Errorf("delete call = %+v", c)
	}
}

func TestDeckCardsAnswerOfAnotherCardIsUnclear(t *testing.T) {
	for _, op := range deckCardOps {
		cardServer(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, fullCard("99", "5", "false")), nil
		})
		_, err := deckCardWrite(t, []string{"deck/2"}, op.id, op.args, true)
		if op.name == "create" {
			continue // a creation answers a new ID
		}
		if err == nil || !strings.Contains(err.Error(), "may have been applied") {
			t.Errorf("%s: err = %v", op.name, err)
		}
	}
}

func TestDeckCardsUnclearOutcomeIsNeverRepeated(t *testing.T) {
	unclear := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbage": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `not json`), nil },
		"unreadable": func() (*http.Response, error) {
			response := jsonResponse(http.StatusOK, "")
			response.Body = failingBody{}
			return response, nil
		},
	}
	clear := map[string]func() (*http.Response, error){
		"302": func() (*http.Response, error) { return status(302), nil },
		"307": func() (*http.Response, error) {
			response := status(307)
			response.Header.Set("Location", "https://elsewhere.invalid/"+deckCanary)
			return response, nil
		},
		"403": func() (*http.Response, error) { return status(403), nil },
		"404": func() (*http.Response, error) { return status(404), nil },
	}
	for _, op := range deckCardOps {
		check := func(t *testing.T, answer func() (*http.Response, error), hint bool) {
			var methods []string
			serve(t, func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				if request.Method == http.MethodGet {
					return jsonResponse(http.StatusOK, cardStacksAnswer(false)), nil
				}
				return answer()
			})
			_, err := deckCardWrite(t, []string{"deck/2"}, op.id, op.args, true)
			if err == nil {
				t.Fatal("no error")
			}
			if len(methods) != 2 || methods[1] != op.method {
				t.Errorf("requests = %v", methods)
			}
			text := err.Error()
			if strings.Contains(text, bodyCanary) || strings.Contains(text, "elsewhere") || strings.Contains(text, deckCanary) {
				t.Errorf("provider text leaked: %v", err)
			}
			if has := strings.Contains(text, "may have been applied"); has != hint {
				t.Errorf("hint present = %v in %q", has, text)
			}
			if hint && !strings.Contains(text, "deckcards.get") {
				t.Errorf("no verification advice in %q", text)
			}
		}
		for name, answer := range unclear {
			t.Run(op.name+" unclear "+name, func(t *testing.T) { check(t, answer, true) })
		}
		for name, answer := range clear {
			t.Run(op.name+" clear "+name, func(t *testing.T) { check(t, answer, false) })
		}
	}
}

func TestDeckCardsDeleteIsAllowListOnly(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	_, err := deckWriteCore(t, []string{deckStacksList.ID, deckCardsUpdate.ID}, "deck/2").Invoke(context.Background(),
		application.InvokeRequest{Operation: deckCardsDelete.ID, Connection: "reports",
			Arguments: json.RawMessage(`{"board_id":"2","stack_id":"5","card_id":"21"}`), Confirmed: true})
	if err == nil || len(*calls) != 0 {
		t.Errorf("delete without a tools list: %v, calls %d", err, len(*calls))
	}
	if !deckCardsDelete.RequiresToolAllowList || deckCardsCreate.RequiresToolAllowList || deckCardsUpdate.RequiresToolAllowList ||
		deckCardsMove.RequiresToolAllowList || deckCardsDelete.Risk.Effect != capability.EffectDelete {
		t.Errorf("delete descriptor = %+v", deckCardsDelete)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.deckcards.") && id != deckCardsGet.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
	for _, d := range []capability.Descriptor{deckCardsCreate, deckCardsUpdate, deckCardsMove, deckCardsDelete} {
		if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Errorf("risk of %s = %+v", d.ID, d.Risk)
		}
	}
}
