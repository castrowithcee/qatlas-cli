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
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const deckBoardsPath = "/index.php/apps/deck/api/v1.1/boards"

// deckWriteCore is a connection that may read, create, update, and delete and lists the Deck board tools.
func deckWriteCore(t *testing.T, tools []string, targets ...string) *application.Core {
	t.Helper()
	cfg := coreConfig()
	connection := cfg.Connections["reports"]
	connection.Target = ""
	connection.Targets = targets
	connection.Permissions = []config.Permission{config.PermissionRead, config.PermissionCreate,
		config.PermissionUpdate, config.PermissionDelete}
	connection.Tools = tools
	cfg.Connections["reports"] = connection
	red := &redact.Redactor{}
	return application.New(registry(t), cfg, resolver(red), red)
}

var deckWriteTools = []string{deckBoardsList.ID, deckBoardsGet.ID, deckBoardsCreate.ID, deckBoardsUpdate.ID, deckBoardsDelete.ID}

func deckWrite(t *testing.T, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	response, err := deckWriteCore(t, deckWriteTools, targets...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirm,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

// managedBoard is a board answer with the manage right, or without it.
func managedBoard(id, title string, manage bool) string {
	flag := "false"
	if manage {
		flag = "true"
	}
	return strings.Replace(board(id, title), `"acl":[]`, `"acl":[],"permissions":{"PERMISSION_READ":true,"PERMISSION_MANAGE":`+flag+`}`, 1)
}

func TestDeckBoardsCreateNeedsGeneralTargetAndConfirmation(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	if _, err := deckWrite(t, []string{"deck/2"}, "nextcloud.deckboards.create", `{"title":"New"}`, true); err == nil {
		t.Error("a deck/ID binding created a board")
	}
	if _, err := deckWrite(t, []string{"folder/Reports"}, "nextcloud.deckboards.create", `{"title":"New"}`, true); err == nil {
		t.Error("a connection without a deck target created a board")
	}
	if _, err := deckWrite(t, []string{"deck"}, "nextcloud.deckboards.create", `{"title":"New"}`, false); err == nil {
		t.Error("an unconfirmed creation was accepted")
	}
	for _, args := range []string{`{"title":""}`, `{"title":"` + strings.Repeat("a", 101) + `"}`, "{\"title\":\"a\\u0007b\"}",
		`{"title":"a","color":"#00ff00"}`, `{"title":"a","color":"fff"}`, `{"title":"a","archived":true}`,
		`{"title":"a","url":"/x"}`, `{}`} {
		if _, err := deckWrite(t, []string{"deck"}, "nextcloud.deckboards.create", args, true); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestDeckBoardsCreateSendsOneFixedPost(t *testing.T) {
	var contentType string
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		contentType = request.Header.Get("Content-Type")
		return jsonResponse(http.StatusOK, board("12", "Plan")), nil
	})
	result, err := deckWrite(t, []string{"deck"}, "nextcloud.deckboards.create", `{"title":" \"Plan\" ","color":"00FF00"}`, true)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodPost || (*calls)[0].url.Path != deckBoardsPath ||
		contentType != "application/json" {
		t.Fatalf("calls = %+v, content type %q", *calls, contentType)
	}
	if got := (*calls)[0].body; got != `{"title":"\"Plan\"","color":"00FF00"}` {
		t.Errorf("body = %s", got)
	}
	if !strings.Contains(string(result), `"created":true`) || !strings.Contains(string(result), `"id":"12"`) {
		t.Errorf("result = %s", result)
	}
	if _, err := deckWrite(t, []string{"deck"}, "nextcloud.deckboards.create", `{"title":"x"}`, true); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[1].body; got != `{"title":"x","color":"`+defaultDeckColor+`"}` {
		t.Errorf("default body = %s", got)
	}
}

func TestDeckBoardsChangesRefuseUnboundBoardBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	for name, tc := range map[string]struct{ op, args string }{
		"update":     {"nextcloud.deckboards.update", `{"board_id":"9","title":"x"}`},
		"delete":     {"nextcloud.deckboards.delete", `{"board_id":"9"}`},
		"update id":  {"nextcloud.deckboards.update", `{"board_id":"2/..","title":"x"}`},
		"no change":  {"nextcloud.deckboards.update", `{"board_id":"2"}`},
		"bad color":  {"nextcloud.deckboards.update", `{"board_id":"2","color":"zzzzzz"}`},
		"delete+tit": {"nextcloud.deckboards.delete", `{"board_id":"2","title":"x"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deckWrite(t, []string{"deck/2"}, tc.op, tc.args, true)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "9") {
				t.Errorf("the refusal names the board: %v", err)
			}
		})
	}
	if _, err := deckWrite(t, []string{"deck/2"}, "nextcloud.deckboards.update", `{"board_id":"2","title":"x"}`, false); err == nil {
		t.Error("an unconfirmed update was accepted")
	}
	if _, err := deckWrite(t, []string{"deck/2"}, "nextcloud.deckboards.delete", `{"board_id":"2"}`, false); err == nil {
		t.Error("an unconfirmed delete was accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestDeckBoardsUpdateKeepsFieldsNotGiven(t *testing.T) {
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return jsonResponse(http.StatusOK, managedBoard("2", "Old", true)), nil
		}
		return jsonResponse(http.StatusOK, board("2", "New")), nil
	})
	result, err := deckWrite(t, []string{"deck/2"}, "nextcloud.deckboards.update", `{"board_id":"2","title":"New"}`, true)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	if len(*calls) != 2 || (*calls)[0].method != http.MethodGet || (*calls)[1].method != http.MethodPut ||
		(*calls)[1].url.Path != deckBoardsPath+"/2" {
		t.Fatalf("calls = %+v", *calls)
	}
	if got := (*calls)[1].body; got != `{"title":"New","color":"0087C5","archived":false}` {
		t.Errorf("body = %s", got)
	}
	if !strings.Contains(string(result), `"updated":true`) {
		t.Errorf("result = %s", result)
	}
	if _, err := deckWrite(t, []string{"deck/2"}, "nextcloud.deckboards.update", `{"board_id":"2","archived":true}`, true); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[3].body; got != `{"title":"Old","color":"0087C5","archived":true}` {
		t.Errorf("archive body = %s", got)
	}
}

func TestDeckBoardsChangesNeedManageRightAndSendNoMutation(t *testing.T) {
	for name, answer := range map[string]string{
		"no manage right": managedBoard("2", "B", false),
		"no permissions":  board("2", "B"),
		"deleted":         strings.Replace(managedBoard("2", "B", true), `"deletedAt":0`, `"deletedAt":5`, 1),
		"other board":     managedBoard("3", "B", true),
	} {
		for _, op := range []struct{ id, args string }{
			{"nextcloud.deckboards.update", `{"board_id":"2","title":"x"}`},
			{"nextcloud.deckboards.delete", `{"board_id":"2"}`},
		} {
			t.Run(name+" "+op.id, func(t *testing.T) {
				calls := serve(t, func(*http.Request) (*http.Response, error) {
					return jsonResponse(http.StatusOK, answer), nil
				})
				if _, err := deckWrite(t, []string{"deck"}, op.id, op.args, true); err == nil {
					t.Fatal("accepted")
				}
				if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
					t.Errorf("calls = %+v, want one read", *calls)
				}
			})
		}
	}
}

func TestDeckBoardsUnclearOutcomeIsNeverRepeated(t *testing.T) {
	unclear := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"503":     func() (*http.Response, error) { return status(503), nil },
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
		"308": func() (*http.Response, error) { return status(308), nil },
		"400": func() (*http.Response, error) { return status(400), nil },
		"403": func() (*http.Response, error) { return status(403), nil },
		"404": func() (*http.Response, error) { return status(404), nil },
	}
	ops := []struct {
		name, id, args string
		method         string
		reads          int
		targets        []string
	}{
		{"create", "nextcloud.deckboards.create", `{"title":"x"}`, http.MethodPost, 0, []string{"deck"}},
		{"update", "nextcloud.deckboards.update", `{"board_id":"2","title":"x"}`, http.MethodPut, 1, []string{"deck/2"}},
		{"delete", "nextcloud.deckboards.delete", `{"board_id":"2"}`, http.MethodDelete, 1, []string{"deck/2"}},
	}
	for _, op := range ops {
		check := func(t *testing.T, answer func() (*http.Response, error), hint bool) {
			var methods []string
			serve(t, func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				if request.Method == http.MethodGet {
					return jsonResponse(http.StatusOK, managedBoard("2", "B", true)), nil
				}
				return answer()
			})
			_, err := deckWrite(t, op.targets, op.id, op.args, true)
			if err == nil {
				t.Fatal("no error")
			}
			if len(methods) != op.reads+1 || methods[len(methods)-1] != op.method {
				t.Errorf("requests = %v", methods)
			}
			text := err.Error()
			if strings.Contains(text, bodyCanary) {
				t.Errorf("provider text leaked: %v", err)
			}
			if has := strings.Contains(text, "may have been applied"); has != hint {
				t.Errorf("hint present = %v in %q", has, text)
			}
			if hint && !strings.Contains(text, "deckboards.list") {
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

func TestDeckBoardsRedirectNamesNoTarget(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		response := status(307)
		response.Header.Set("Location", "https://elsewhere.invalid/"+deckCanary)
		return response, nil
	})
	_, err := deckWrite(t, []string{"deck"}, "nextcloud.deckboards.create", `{"title":"x"}`, true)
	if err == nil || strings.Contains(err.Error(), "elsewhere") || strings.Contains(err.Error(), deckCanary) ||
		!strings.Contains(err.Error(), "redirect") {
		t.Errorf("err = %v", err)
	}
}

func TestDeckBoardsDeleteDeletesOnceAndIsAllowListOnly(t *testing.T) {
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return jsonResponse(http.StatusOK, managedBoard("2", "B", true)), nil
		}
		return jsonResponse(http.StatusOK, strings.Replace(board("2", "B"), `"deletedAt":0`, `"deletedAt":9`, 1)), nil
	})
	result, err := deckWrite(t, []string{"deck/2"}, "nextcloud.deckboards.delete", `{"board_id":"2"}`, true)
	if err != nil || !strings.Contains(string(result), `"deleted":true`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(*calls) != 2 || (*calls)[1].method != http.MethodDelete || (*calls)[1].url.Path != deckBoardsPath+"/2" ||
		(*calls)[1].body != "" {
		t.Errorf("calls = %+v", *calls)
	}
	// Without a tools list the delete is not offered, though the connection holds the delete permission.
	_, err = deckWriteCore(t, []string{deckBoardsList.ID, deckBoardsUpdate.ID}, "deck/2").Invoke(context.Background(),
		application.InvokeRequest{Operation: deckBoardsDelete.ID, Connection: "reports",
			Arguments: json.RawMessage(`{"board_id":"2"}`), Confirmed: true})
	if err == nil || len(*calls) != 2 {
		t.Errorf("delete without a tools list: %v, calls %d", err, len(*calls))
	}
	if !deckBoardsDelete.RequiresToolAllowList || deckBoardsCreate.RequiresToolAllowList || deckBoardsUpdate.RequiresToolAllowList ||
		deckBoardsDelete.Risk.Effect != capability.EffectDelete || !strings.Contains(deckBoardsDelete.Description, "softly") ||
		!strings.Contains(deckBoardsDelete.Description, "no restore") {
		t.Errorf("delete descriptor = %+v", deckBoardsDelete)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.deckboards.") && id != deckBoardsList.ID && id != deckBoardsGet.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
	for _, d := range []capability.Descriptor{deckBoardsCreate, deckBoardsUpdate, deckBoardsDelete} {
		if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Errorf("risk of %s = %+v", d.ID, d.Risk)
		}
	}
}
