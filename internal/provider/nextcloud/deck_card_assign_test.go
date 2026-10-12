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

var deckAssignTools = []string{deckBoardsGet.ID, deckStacksList.ID, deckCardsLabels.ID, deckCardsAssignees.ID}

const assignBoard = `{"id":2,"title":"Plan","color":"0082c9","archived":false,"owner":{"uid":"dora","displayname":"Dora","type":0},` +
	`"labels":[{"id":4,"title":"Bug","color":"ff0000"}],"acl":[],` +
	`"users":[{"uid":"emil","displayname":"Emil","type":0},{"uid":"crew","displayname":"Crew","type":1}]}`

func assignServer(t *testing.T, board string, write func(*http.Request) (*http.Response, error)) *[]call {
	return serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method != http.MethodGet:
			return write(request)
		case strings.HasSuffix(request.URL.Path, "/stacks"):
			return jsonResponse(http.StatusOK, cardStacksAnswer(false)), nil
		}
		return jsonResponse(http.StatusOK, board), nil
	})
}

func deckAssign(t *testing.T, operation, args string) error {
	t.Helper()
	_, err := deckWriteCore(t, deckAssignTools, "deck/2").Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: true,
	})
	return err
}

func TestDeckAssignSendsOneFixedRequest(t *testing.T) {
	for name, tc := range map[string]struct{ op, args, action, body string }{
		"label assign":   {"nextcloud.deckcards.labels", `"label_id":"4","assign":true`, "assignLabel", `{"labelId":4}`},
		"label remove":   {"nextcloud.deckcards.labels", `"label_id":"4","assign":false`, "removeLabel", `{"labelId":4}`},
		"user assign":    {"nextcloud.deckcards.assignees", `"user_id":"emil","assign":true`, "assignUser", `{"userId":"emil","type":0}`},
		"user unassign":  {"nextcloud.deckcards.assignees", `"user_id":"emil","assign":false`, "unassignUser", `{"userId":"emil","type":0}`},
		"owner assigned": {"nextcloud.deckcards.assignees", `"user_id":"dora","assign":true`, "assignUser", `{"userId":"dora","type":0}`},
	} {
		t.Run(name, func(t *testing.T) {
			calls := assignServer(t, assignBoard, func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, fullCard("21", "5", "false")), nil
			})
			args := `{"board_id":"2","stack_id":"5","card_id":"21",` + tc.args + `}`
			if err := deckAssign(t, tc.op, args); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 3 || (*calls)[0].method != http.MethodGet || (*calls)[1].method != http.MethodGet {
				t.Fatalf("calls = %+v", *calls)
			}
			if c := (*calls)[2]; c.method != http.MethodPut || c.url.Path != deckCardsBase+"/5/cards/21/"+tc.action || c.body != tc.body {
				t.Errorf("mutation = %+v", c)
			}
		})
	}
}

func TestDeckAssignRefusesWithoutMutation(t *testing.T) {
	const base = `"board_id":"2","stack_id":"5","card_id":"21",`
	for name, tc := range map[string]struct{ op, args string }{
		"foreign label":     {"nextcloud.deckcards.labels", base + `"label_id":"9","assign":true`},
		"foreign label out": {"nextcloud.deckcards.labels", base + `"label_id":"9","assign":false`},
		"not a member":      {"nextcloud.deckcards.assignees", base + `"user_id":"mallory","assign":true`},
		"group member":      {"nextcloud.deckcards.assignees", base + `"user_id":"crew","assign":true`},
		"card in no stack":  {"nextcloud.deckcards.labels", `"board_id":"2","stack_id":"6","card_id":"21","label_id":"4","assign":true`},
		"unknown card":      {"nextcloud.deckcards.assignees", `"board_id":"2","stack_id":"5","card_id":"99","user_id":"emil","assign":true`},
		"deleted stack":     {"nextcloud.deckcards.labels", `"board_id":"2","stack_id":"7","card_id":"21","label_id":"4","assign":true`},
	} {
		t.Run(name, func(t *testing.T) {
			calls := assignServer(t, assignBoard, func(*http.Request) (*http.Response, error) {
				t.Error("a mutation was sent")
				return nil, errors.New("no mutation expected")
			})
			err := deckAssign(t, tc.op, `{`+tc.args+`}`)
			if err == nil || strings.Contains(err.Error(), "mallory") || strings.Contains(err.Error(), `"9"`) {
				t.Errorf("err = %v", err)
			}
			for _, c := range *calls {
				if c.method != http.MethodGet {
					t.Errorf("call = %+v", c)
				}
			}
		})
	}
}

func TestDeckAssignRefusesBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	for name, args := range map[string]struct{ op, args string }{
		"unbound board": {"nextcloud.deckcards.labels", `{"board_id":"9","stack_id":"5","card_id":"21","label_id":"4","assign":true}`},
		"unbound user":  {"nextcloud.deckcards.assignees", `{"board_id":"9","stack_id":"5","card_id":"21","user_id":"emil","assign":true}`},
		"no assign":     {"nextcloud.deckcards.labels", `{"board_id":"2","stack_id":"5","card_id":"21","label_id":"4"}`},
		"bad label":     {"nextcloud.deckcards.labels", `{"board_id":"2","stack_id":"5","card_id":"21","label_id":"4/..","assign":true}`},
		"bad user":      {"nextcloud.deckcards.assignees", `{"board_id":"2","stack_id":"5","card_id":"21","user_id":"a/b","assign":true}`},
		"user on label": {"nextcloud.deckcards.labels", `{"board_id":"2","stack_id":"5","card_id":"21","label_id":"4","user_id":"emil","assign":true}`},
		"label on user": {"nextcloud.deckcards.assignees", `{"board_id":"2","stack_id":"5","card_id":"21","label_id":"4","user_id":"emil","assign":true}`},
	} {
		if err := deckAssign(t, args.op, args.args); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d", len(*calls))
	}
}

func TestDeckAssignUnclearOutcomeIsNeverRepeated(t *testing.T) {
	unclear := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"garbage": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `not json`), nil },
	}
	for _, op := range []struct{ id, args string }{
		{"nextcloud.deckcards.labels", `"label_id":"4","assign":true`},
		{"nextcloud.deckcards.assignees", `"user_id":"emil","assign":true`},
	} {
		for name, answer := range unclear {
			t.Run(op.id+" "+name, func(t *testing.T) {
				calls := assignServer(t, assignBoard, func(*http.Request) (*http.Response, error) { return answer() })
				err := deckAssign(t, op.id, `{"board_id":"2","stack_id":"5","card_id":"21",`+op.args+`}`)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") ||
					!strings.Contains(err.Error(), "deckcards.get") || len(*calls) != 3 {
					t.Errorf("err = %v, calls %d", err, len(*calls))
				}
			})
		}
	}
}

func TestDeckAssignRisk(t *testing.T) {
	for _, descriptor := range []capability.Descriptor{deckCardsLabels, deckCardsAssignees} {
		if !descriptor.Risk.OpenWorld || descriptor.RequiresToolAllowList || descriptor.Risk.Effect != capability.EffectUpdate ||
			descriptor.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s risk = %+v", descriptor.ID, descriptor.Risk)
		}
	}
}
