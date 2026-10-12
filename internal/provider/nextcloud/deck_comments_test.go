package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const deckCommentsPath = "/ocs/v2.php/apps/deck/api/v1.0/cards/21/comments"

var deckCommentTools = []string{deckStacksList.ID, deckCommentsList.ID, deckCommentsCreate.ID, deckCommentsUpdate.ID,
	deckCommentsDelete.ID}

const deckCommentRef = `"board_id":"2","stack_id":"5","card_id":"21"`

func deckComment(id, card, message string) string {
	return `{"id":` + id + `,"objectId":` + card + `,"message":"` + message + `","actorId":"dora",` +
		`"actorType":"users","actorDisplayName":"Dora","creationDateTime":"2026-01-02T03:04:05+00:00","replyTo":null}`
}

// deckCommentServer answers the stack read and sends every other request to the handler.
func deckCommentServer(t *testing.T, other func(*http.Request) (*http.Response, error)) *[]call {
	return serve(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasPrefix(request.URL.Path, deckBoardsPath) {
			return jsonResponse(http.StatusOK, cardStacksAnswer(false)), nil
		}
		return other(request)
	})
}

func deckCommentCall(t *testing.T, operation, args string) (json.RawMessage, error) {
	t.Helper()
	response, err := deckWriteCore(t, deckCommentTools, "deck/2").Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: true,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func ocsOK(data string) *http.Response { return ocsResponse(http.StatusOK, ocsData(data)) }

func TestDeckCommentsListPaginatesAndMarksTheNextPage(t *testing.T) {
	long := strings.Repeat("x", maxCommentChars+5)
	calls := deckCommentServer(t, func(*http.Request) (*http.Response, error) {
		return ocsOK(`[` + deckComment("3", "21", long) + `,` + deckComment("2", "21", "two") + `,` +
			deckComment("1", "21", "one") + `]`), nil
	})
	result, err := deckCommentCall(t, "nextcloud.deckcomments.list", `{`+deckCommentRef+`,"limit":2,"offset":4}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].method != http.MethodGet || (*calls)[1].method != http.MethodGet ||
		(*calls)[1].url.Path != deckCommentsPath {
		t.Fatalf("calls = %+v", *calls)
	}
	query := (*calls)[1].url.Query()
	if query.Get("limit") != "3" || query.Get("offset") != "4" {
		t.Errorf("query = %v", query)
	}
	var list DeckCommentList
	if err := json.Unmarshal(result, &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 2 || !list.More || list.Offset != 4 || list.Comments[0].CommentID != "3" ||
		!list.Comments[0].MessageTruncated || len(list.Comments[0].Message) != maxCommentChars {
		t.Errorf("list = %+v", list)
	}
}

func TestDeckCommentsListLastPageHasNoMore(t *testing.T) {
	deckCommentServer(t, func(*http.Request) (*http.Response, error) {
		return ocsOK(`[` + deckComment("1", "21", "one") + `]`), nil
	})
	result, err := deckCommentCall(t, "nextcloud.deckcomments.list", `{`+deckCommentRef+`}`)
	var list DeckCommentList
	if err != nil || json.Unmarshal(result, &list) != nil || list.Count != 1 || list.More {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
}

func TestDeckCommentsMutationsSendOneFixedRequest(t *testing.T) {
	for name, tc := range map[string]struct{ op, args, method, path, body string }{
		"create": {"nextcloud.deckcomments.create", `"message":"Hi @emil"`, http.MethodPost, deckCommentsPath, "message=Hi+%40emil"},
		"update": {"nextcloud.deckcomments.update", `"comment_id":"42","message":"Again"`, http.MethodPut,
			deckCommentsPath + "/42", "message=Again"},
		"delete": {"nextcloud.deckcomments.delete", `"comment_id":"42"`, http.MethodDelete, deckCommentsPath + "/42", ""},
	} {
		t.Run(name, func(t *testing.T) {
			calls := deckCommentServer(t, func(*http.Request) (*http.Response, error) {
				return ocsOK(deckComment("42", "21", "x")), nil
			})
			result, err := deckCommentCall(t, tc.op, `{`+deckCommentRef+`,`+tc.args+`}`)
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 2 || (*calls)[0].method != http.MethodGet {
				t.Fatalf("calls = %+v", *calls)
			}
			if c := (*calls)[1]; c.method != tc.method || c.url.Path != tc.path || c.body != tc.body {
				t.Errorf("mutation = %+v", c)
			}
			if !strings.Contains(string(result), `"card_id":"21"`) || !strings.Contains(string(result), `"comment_id":"42"`) {
				t.Errorf("result = %s", result)
			}
		})
	}
}

func TestDeckCommentsCreateWithParentReadsTheNewestPageFirst(t *testing.T) {
	for name, tc := range map[string]struct {
		list    string
		mutates bool
	}{
		"parent listed":  {`[` + deckComment("7", "21", "p") + `]`, true},
		"parent missing": {`[` + deckComment("8", "21", "p") + `]`, false},
		"parent of card": {`[` + deckComment("7", "99", "p") + `]`, false},
	} {
		t.Run(name, func(t *testing.T) {
			calls := deckCommentServer(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return ocsOK(tc.list), nil
				}
				return ocsOK(deckComment("42", "21", "x")), nil
			})
			_, err := deckCommentCall(t, "nextcloud.deckcomments.create", `{`+deckCommentRef+`,"message":"Re","parent_id":"7"}`)
			if tc.mutates && (err != nil || len(*calls) != 3 || (*calls)[2].body != "message=Re&parentId=7") {
				t.Fatalf("calls = %+v, err = %v", *calls, err)
			}
			if !tc.mutates {
				if err == nil || len(*calls) != 2 {
					t.Fatalf("calls = %+v, err = %v", *calls, err)
				}
			}
			if q := (*calls)[1].url.Query(); q.Get("limit") != strconv.Itoa(maxDeckCommentLimit) || q.Get("offset") != "0" {
				t.Errorf("query = %v", q)
			}
		})
	}
}

func TestDeckCommentsRefuseCardOutsideTheStackWithoutACommentRequest(t *testing.T) {
	for name, args := range map[string]struct{ op, args string }{
		"list other stack":   {"list", `"board_id":"2","stack_id":"6","card_id":"21"`},
		"create unknown":     {"create", `"board_id":"2","stack_id":"5","card_id":"99","message":"x"`},
		"update deleted":     {"update", `"board_id":"2","stack_id":"7","card_id":"21","comment_id":"1","message":"x"`},
		"delete other stack": {"delete", `"board_id":"2","stack_id":"6","card_id":"21","comment_id":"1"`},
		"parent card":        {"create", `"board_id":"2","stack_id":"6","card_id":"21","message":"x","parent_id":"1"`},
	} {
		t.Run(name, func(t *testing.T) {
			calls := deckCommentServer(t, func(*http.Request) (*http.Response, error) {
				t.Error("a comment request was sent")
				return nil, errors.New("no request expected")
			})
			_, err := deckCommentCall(t, "nextcloud.deckcomments."+args.op, `{`+args.args+`}`)
			if err == nil || len(*calls) != 1 {
				t.Errorf("calls = %+v, err = %v", *calls, err)
			}
		})
	}
}

func TestDeckCommentsRefuseLocallyBeforeAnyRequest(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s", request.URL.Path)
		return nil, errors.New("no request expected")
	})
	for name, tc := range map[string]struct{ op, args string }{
		"unbound board": {"list", `{"board_id":"9","stack_id":"5","card_id":"21"}`},
		"unbound write": {"create", `{"board_id":"9","stack_id":"5","card_id":"21","message":"x"}`},
		"blank text":    {"create", `{` + deckCommentRef + `,"message":"  "}`},
		"long text":     {"update", `{` + deckCommentRef + `,"comment_id":"1","message":"` + strings.Repeat("x", 1001) + `"}`},
		"bad comment":   {"delete", `{` + deckCommentRef + `,"comment_id":"1/.."}`},
		"bad parent":    {"create", `{` + deckCommentRef + `,"message":"x","parent_id":"a"}`},
		"limit too big": {"list", `{` + deckCommentRef + `,"limit":101}`},
		"unknown field": {"delete", `{` + deckCommentRef + `,"comment_id":"1","message":"x"}`},
	} {
		if _, err := deckCommentCall(t, "nextcloud.deckcomments."+tc.op, tc.args); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestDeckCommentsForeignCommentIsRefusedWithoutProviderText(t *testing.T) {
	for name, tc := range map[string]struct{ op, args, want string }{
		"update": {"update", `"comment_id":"42","message":"x"`, "only the author"},
		"delete": {"delete", `"comment_id":"42"`, "only the author"},
		"create": {"create", `"message":"x"`, "may not comment"},
	} {
		t.Run(name, func(t *testing.T) {
			deckCommentServer(t, func(*http.Request) (*http.Response, error) { return status(http.StatusForbidden), nil })
			_, err := deckCommentCall(t, "nextcloud.deckcomments."+tc.op, `{`+deckCommentRef+`,`+tc.args+`}`)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("err = %v", err)
			}
		})
	}
	deckCommentServer(t, func(*http.Request) (*http.Response, error) { return status(http.StatusNotFound), nil })
	_, err := deckCommentCall(t, "nextcloud.deckcomments.delete", `{`+deckCommentRef+`,"comment_id":"42"}`)
	if err == nil || !strings.Contains(err.Error(), messageDeckForeign) || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestDeckCommentsUnclearOutcomeIsNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"garbage": func() (*http.Response, error) { return ocsResponse(http.StatusOK, `not json`), nil },
	}
	ops := map[string]string{
		"create": `"message":"x"`, "update": `"comment_id":"42","message":"x"`, "delete": `"comment_id":"42"`,
	}
	for op, args := range ops {
		for name, answer := range answers {
			t.Run(op+" "+name, func(t *testing.T) {
				calls := deckCommentServer(t, func(*http.Request) (*http.Response, error) { return answer() })
				_, err := deckCommentCall(t, "nextcloud.deckcomments."+op, `{`+deckCommentRef+`,`+args+`}`)
				if err == nil || len(*calls) != 2 || !strings.Contains(err.Error(), "may have been applied, check deckcomments.list") ||
					strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("calls = %d, err = %v", len(*calls), err)
				}
			})
		}
	}
}

func TestDeckCommentsDeleteNeedsAToolsListAndWritesNeedConfirmation(t *testing.T) {
	if !deckCommentsDelete.RequiresToolAllowList || deckCommentsCreate.RequiresToolAllowList || deckCommentsUpdate.RequiresToolAllowList {
		t.Error("only the delete requires a tools list")
	}
	for _, d := range []capability.Descriptor{deckCommentsCreate, deckCommentsUpdate, deckCommentsDelete} {
		if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity != deckSensitivity {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
	}
	calls := deckCommentServer(t, func(*http.Request) (*http.Response, error) { return ocsOK(`[]`), nil })
	_, err := deckWriteCore(t, deckCommentTools[:4], "deck/2").Invoke(context.Background(), application.InvokeRequest{
		Operation: deckCommentsDelete.ID, Connection: "reports", Confirmed: true,
		Arguments: json.RawMessage(`{` + deckCommentRef + `,"comment_id":"42"}`),
	})
	if err == nil || len(*calls) != 0 {
		t.Errorf("calls = %d, err = %v", len(*calls), err)
	}
}
