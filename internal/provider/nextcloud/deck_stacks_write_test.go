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

const deckStacksPath = deckBoardsPath + "/2/stacks"

var deckStackTools = []string{deckBoardsGet.ID, deckStacksList.ID, deckStacksCreate.ID, deckStacksUpdate.ID, deckStacksDelete.ID}

func deckStackWrite(t *testing.T, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	response, err := deckWriteCore(t, deckStackTools, targets...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirm,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

// boardWithStacks is a managed board answer that lists stacks 5 and 6.
func boardWithStacks(manage bool) string {
	return strings.Replace(managedBoard("2", "B", manage), `"acl":[]`,
		`"acl":[],"stacks":[{"id":5,"title":"Todo","order":3,"deletedAt":0},{"id":6,"title":"Gone","order":9,"deletedAt":4}]`, 1)
}

func stackAnswer(id, title string, order int) string {
	return `{"id":` + id + `,"title":"` + title + `","order":` + string(rune('0'+order)) + `}`
}

func TestDeckStacksRefuseBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	for name, tc := range map[string]struct{ op, args string }{
		"create unbound":  {"create", `{"board_id":"9","title":"x"}`},
		"update unbound":  {"update", `{"board_id":"9","stack_id":"5","title":"x"}`},
		"delete unbound":  {"delete", `{"board_id":"9","stack_id":"5"}`},
		"bad board":       {"create", `{"board_id":"2/..","title":"x"}`},
		"bad stack":       {"delete", `{"board_id":"2","stack_id":"5/.."}`},
		"no stack":        {"update", `{"board_id":"2","title":"x"}`},
		"no title":        {"create", `{"board_id":"2"}`},
		"empty title":     {"create", `{"board_id":"2","title":"  "}`},
		"long title":      {"create", `{"board_id":"2","title":"` + strings.Repeat("a", 101) + `"}`},
		"control":         {"create", "{\"board_id\":\"2\",\"title\":\"a\\u0007\"}"},
		"fraction":        {"create", `{"board_id":"2","title":"x","order":1.5}`},
		"negative":        {"create", `{"board_id":"2","title":"x","order":-1}`},
		"too large":       {"update", `{"board_id":"2","stack_id":"5","order":10001}`},
		"no change":       {"update", `{"board_id":"2","stack_id":"5"}`},
		"delete + title":  {"delete", `{"board_id":"2","stack_id":"5","title":"x"}`},
		"create + stack":  {"create", `{"board_id":"2","stack_id":"5","title":"x"}`},
		"free parameter":  {"create", `{"board_id":"2","title":"x","path":"/y"}`},
		"unconfirmed upd": {"update", `{"board_id":"2","stack_id":"5","title":"x"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deckStackWrite(t, []string{"deck/2"}, "nextcloud.deckstacks."+tc.op, tc.args, name != "unconfirmed upd")
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "9") {
				t.Errorf("the refusal names the board: %v", err)
			}
		})
	}
	if _, err := deckStackWrite(t, []string{"deck/2"}, "nextcloud.deckstacks.delete", `{"board_id":"2","stack_id":"5"}`, false); err == nil {
		t.Error("an unconfirmed delete was accepted")
	}
	if _, err := deckStackWrite(t, []string{"deck/2"}, "nextcloud.deckstacks.create", `{"board_id":"2","title":"x"}`, false); err == nil {
		t.Error("an unconfirmed creation was accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestDeckStacksRefuseWithoutMutation(t *testing.T) {
	for name, answer := range map[string]string{
		"no manage right": boardWithStacks(false),
		"no stack list":   managedBoard("2", "B", true),
	} {
		for _, op := range []struct{ id, args string }{
			{"nextcloud.deckstacks.create", `{"board_id":"2","title":"x"}`},
			{"nextcloud.deckstacks.update", `{"board_id":"2","stack_id":"5","title":"x"}`},
			{"nextcloud.deckstacks.delete", `{"board_id":"2","stack_id":"5"}`},
		} {
			t.Run(name+" "+op.id, func(t *testing.T) {
				calls := serve(t, func(*http.Request) (*http.Response, error) {
					return jsonResponse(http.StatusOK, answer), nil
				})
				if _, err := deckStackWrite(t, []string{"deck"}, op.id, op.args, true); err == nil {
					t.Fatal("accepted")
				}
				if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
					t.Errorf("calls = %+v, want one read", *calls)
				}
			})
		}
	}
	// A stack of another board, an unknown stack, and a deleted one are refused alike, without naming it.
	for _, stackID := range []string{"77", "6"} {
		for _, op := range []struct{ id, args string }{
			{"nextcloud.deckstacks.update", `{"board_id":"2","stack_id":"` + stackID + `","title":"x"}`},
			{"nextcloud.deckstacks.delete", `{"board_id":"2","stack_id":"` + stackID + `"}`},
		} {
			calls := serve(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, boardWithStacks(true)), nil
			})
			_, err := deckStackWrite(t, []string{"deck"}, op.id, op.args, true)
			if err == nil || strings.Contains(err.Error(), stackID) {
				t.Errorf("%s %s: err = %v", op.id, stackID, err)
			}
			if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
				t.Errorf("calls = %+v, want one read", *calls)
			}
		}
	}
}

func TestDeckStacksSendOneFixedRequest(t *testing.T) {
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet:
			return jsonResponse(http.StatusOK, boardWithStacks(true)), nil
		case request.Method == http.MethodPost:
			return jsonResponse(http.StatusOK, stackAnswer("8", "New", 4)), nil
		case request.Method == http.MethodPut:
			return jsonResponse(http.StatusOK, stackAnswer("5", "Todo", 3)), nil
		}
		return jsonResponse(http.StatusOK, stackAnswer("5", "Todo", 3)), nil
	})
	result, err := deckStackWrite(t, []string{"deck/2"}, "nextcloud.deckstacks.create", `{"board_id":"2","title":" \"New\" "}`, true)
	if err != nil || !strings.Contains(string(result), `"created":true`) || !strings.Contains(string(result), `"id":"8"`) {
		t.Fatalf("create = %s, %v", result, err)
	}
	if len(*calls) != 2 || (*calls)[1].method != http.MethodPost || (*calls)[1].url.Path != deckStacksPath ||
		(*calls)[1].body != `{"title":"\"New\"","order":4}` {
		t.Fatalf("calls = %+v", *calls)
	}
	if _, err := deckStackWrite(t, []string{"deck/2"}, "nextcloud.deckstacks.create", `{"board_id":"2","title":"x","order":0}`, true); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[3].body; got != `{"title":"x","order":0}` {
		t.Errorf("explicit order body = %s", got)
	}
	// update keeps what is not given
	if _, err := deckStackWrite(t, []string{"deck/2"}, "nextcloud.deckstacks.update", `{"board_id":"2","stack_id":"5","title":"Next"}`, true); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[5]; c.method != http.MethodPut || c.url.Path != deckStacksPath+"/5" || c.body != `{"title":"Next","order":3}` {
		t.Errorf("title update = %+v", c)
	}
	if _, err := deckStackWrite(t, []string{"deck/2"}, "nextcloud.deckstacks.update", `{"board_id":"2","stack_id":"5","order":7}`, true); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[7].body; got != `{"title":"Todo","order":7}` {
		t.Errorf("order update body = %s", got)
	}
	result, err = deckStackWrite(t, []string{"deck/2"}, "nextcloud.deckstacks.delete", `{"board_id":"2","stack_id":"5"}`, true)
	if err != nil || !strings.Contains(string(result), `"deleted":true`) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if c := (*calls)[9]; len(*calls) != 10 || c.method != http.MethodDelete || c.url.Path != deckStacksPath+"/5" || c.body != "" {
		t.Errorf("delete call = %+v", c)
	}
}

func TestDeckStacksUnclearOutcomeIsNeverRepeated(t *testing.T) {
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
	for _, op := range []struct{ name, id, args, method string }{
		{"create", "nextcloud.deckstacks.create", `{"board_id":"2","title":"x"}`, http.MethodPost},
		{"update", "nextcloud.deckstacks.update", `{"board_id":"2","stack_id":"5","title":"x"}`, http.MethodPut},
		{"delete", "nextcloud.deckstacks.delete", `{"board_id":"2","stack_id":"5"}`, http.MethodDelete},
	} {
		check := func(t *testing.T, answer func() (*http.Response, error), hint bool) {
			var methods []string
			serve(t, func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				if request.Method == http.MethodGet {
					return jsonResponse(http.StatusOK, boardWithStacks(true)), nil
				}
				return answer()
			})
			_, err := deckStackWrite(t, []string{"deck/2"}, op.id, op.args, true)
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
			if hint && !strings.Contains(text, "deckstacks.list") {
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

func TestDeckStacksDeleteIsAllowListOnly(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	_, err := deckWriteCore(t, []string{deckStacksList.ID, deckStacksUpdate.ID}, "deck/2").Invoke(context.Background(),
		application.InvokeRequest{Operation: deckStacksDelete.ID, Connection: "reports",
			Arguments: json.RawMessage(`{"board_id":"2","stack_id":"5"}`), Confirmed: true})
	if err == nil || len(*calls) != 0 {
		t.Errorf("delete without a tools list: %v, calls %d", err, len(*calls))
	}
	if !deckStacksDelete.RequiresToolAllowList || deckStacksCreate.RequiresToolAllowList || deckStacksUpdate.RequiresToolAllowList ||
		deckStacksDelete.Risk.Effect != capability.EffectDelete || !strings.Contains(deckStacksDelete.Description, "cards") {
		t.Errorf("delete descriptor = %+v", deckStacksDelete)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.deckstacks.") && id != deckStacksList.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
	for _, d := range []capability.Descriptor{deckStacksCreate, deckStacksUpdate, deckStacksDelete} {
		if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Errorf("risk of %s = %+v", d.ID, d.Risk)
		}
	}
}
