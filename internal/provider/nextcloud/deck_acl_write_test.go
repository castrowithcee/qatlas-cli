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

const deckACLPath = deckBoardsPath + "/2/acl"

var deckACLTools = []string{deckBoardsGet.ID, deckACLAdd.ID, deckACLUpdate.ID, deckACLRemove.ID}

func deckACLWrite(t *testing.T, tools []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	response, err := deckWriteCore(t, tools, "deck/2").Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirm,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

// boardWithACL lists a user entry 4, a group entry 5, a team entry 6, and the owner entry 7.
func boardWithACL(manage bool) string {
	return strings.Replace(managedBoard("2", "B", manage), `"acl":[]`, `"acl":[`+
		`{"id":4,"type":0,"participant":{"uid":"anna","displayname":"Anna","type":0},"permissionEdit":true,`+
		`"permissionShare":false,"permissionManage":false,"owner":false},`+
		`{"id":5,"type":1,"participant":{"uid":"devs","displayname":"Devs","type":1},"permissionEdit":false,`+
		`"permissionShare":false,"permissionManage":false,"owner":false},`+
		`{"id":6,"type":7,"participant":{"uid":"crew","displayname":"Crew","type":7},"permissionEdit":false,`+
		`"permissionShare":false,"permissionManage":false,"owner":false},`+
		`{"id":7,"type":0,"participant":{"uid":"dora","displayname":"Dora","type":0},"permissionEdit":true,`+
		`"permissionShare":true,"permissionManage":true,"owner":true}]`, 1)
}

func aclAnswer(id, edit, share, manage string) string {
	return `{"id":` + id + `,"boardId":2,"type":0,"participant":"x","permissionEdit":` + edit +
		`,"permissionShare":` + share + `,"permissionManage":` + manage + `,"owner":false}`
}

var deckACLCalls = []struct{ name, id, args string }{
	{"add", "nextcloud.deckacl.add", `{"board_id":"2","type":"user","participant":"bob"}`},
	{"update", "nextcloud.deckacl.update", `{"board_id":"2","acl_id":"4","can_share":true}`},
	{"remove", "nextcloud.deckacl.remove", `{"board_id":"2","acl_id":"4"}`},
}

func TestDeckACLRefuseBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	for name, tc := range map[string]struct{ op, args string }{
		"add unbound":      {"add", `{"board_id":"9","type":"user","participant":"bob"}`},
		"update unbound":   {"update", `{"board_id":"9","acl_id":"4","can_edit":true}`},
		"remove unbound":   {"remove", `{"board_id":"9","acl_id":"4"}`},
		"team type":        {"add", `{"board_id":"2","type":"team","participant":"crew"}`},
		"number type":      {"add", `{"board_id":"2","type":7,"participant":"crew"}`},
		"no type":          {"add", `{"board_id":"2","participant":"bob"}`},
		"no participant":   {"add", `{"board_id":"2","type":"user"}`},
		"slash":            {"add", `{"board_id":"2","type":"user","participant":"a/b"}`},
		"backslash":        {"add", `{"board_id":"2","type":"user","participant":"a\\b"}`},
		"control":          {"add", `{"board_id":"2","type":"user","participant":"a\nb"}`},
		"long participant": {"add", `{"board_id":"2","type":"user","participant":"` + strings.Repeat("a", 65) + `"}`},
		"add + acl":        {"add", `{"board_id":"2","acl_id":"4","type":"user","participant":"bob"}`},
		"bad board":        {"add", `{"board_id":"2/..","type":"user","participant":"bob"}`},
		"bad acl":          {"remove", `{"board_id":"2","acl_id":"4/.."}`},
		"no acl":           {"update", `{"board_id":"2","can_edit":true}`},
		"no change":        {"update", `{"board_id":"2","acl_id":"4"}`},
		"remove + right":   {"remove", `{"board_id":"2","acl_id":"4","can_edit":true}`},
		"update + type":    {"update", `{"board_id":"2","acl_id":"4","type":"group","can_edit":true}`},
		"free parameter":   {"add", `{"board_id":"2","type":"user","participant":"bob","path":"/y"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deckACLWrite(t, deckACLTools, "nextcloud.deckacl."+tc.op, tc.args, true)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "9") {
				t.Errorf("the refusal names the board: %v", err)
			}
		})
	}
	for _, op := range deckACLCalls {
		if _, err := deckACLWrite(t, deckACLTools, op.id, op.args, false); err == nil {
			t.Errorf("unconfirmed %s accepted", op.name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestDeckACLRefuseWithoutMutation(t *testing.T) {
	// No manage right: all three stop after the read.
	for _, op := range deckACLCalls {
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, boardWithACL(false)), nil
		})
		if _, err := deckACLWrite(t, deckACLTools, op.id, op.args, true); err == nil {
			t.Fatalf("%s accepted without the manage right", op.name)
		}
		if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
			t.Errorf("%s calls = %+v, want one read", op.name, *calls)
		}
	}
	// Unknown, team, and owner entries and a duplicate participant stop after the read.
	for name, op := range map[string]struct{ id, args string }{
		"update unknown": {"nextcloud.deckacl.update", `{"board_id":"2","acl_id":"77","can_edit":true}`},
		"remove unknown": {"nextcloud.deckacl.remove", `{"board_id":"2","acl_id":"77"}`},
		"update team":    {"nextcloud.deckacl.update", `{"board_id":"2","acl_id":"6","can_edit":true}`},
		"remove team":    {"nextcloud.deckacl.remove", `{"board_id":"2","acl_id":"6"}`},
		"update owner":   {"nextcloud.deckacl.update", `{"board_id":"2","acl_id":"7","can_edit":false}`},
		"remove owner":   {"nextcloud.deckacl.remove", `{"board_id":"2","acl_id":"7"}`},
		"add duplicate":  {"nextcloud.deckacl.add", `{"board_id":"2","type":"user","participant":"anna"}`},
	} {
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, boardWithACL(true)), nil
		})
		_, err := deckACLWrite(t, deckACLTools, op.id, op.args, true)
		if err == nil || strings.Contains(err.Error(), "77") {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
			t.Errorf("%s calls = %+v, want one read", name, *calls)
		}
	}
}

func TestDeckACLSendOneFixedRequest(t *testing.T) {
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodGet:
			return jsonResponse(http.StatusOK, boardWithACL(true)), nil
		case http.MethodPost:
			return jsonResponse(http.StatusOK, aclAnswer("9", "true", "false", "false")), nil
		case http.MethodPut:
			return jsonResponse(http.StatusOK, aclAnswer("4", "true", "true", "false")), nil
		}
		return jsonResponse(http.StatusOK, `[]`), nil
	})
	result, err := deckACLWrite(t, deckACLTools, "nextcloud.deckacl.add",
		`{"board_id":"2","type":"group","participant":"ops","can_edit":true}`, true)
	if err != nil || !strings.Contains(string(result), `"added":true`) || !strings.Contains(string(result), `"id":"9"`) {
		t.Fatalf("add = %s, %v", result, err)
	}
	if len(*calls) != 2 || (*calls)[1].method != http.MethodPost || (*calls)[1].url.Path != deckACLPath ||
		(*calls)[1].body != `{"type":1,"participant":"ops","permissionEdit":true,"permissionShare":false,"permissionManage":false}` {
		t.Fatalf("calls = %+v", *calls)
	}
	// A user of the same name as a group is a different participant.
	if _, err := deckACLWrite(t, deckACLTools, "nextcloud.deckacl.add", `{"board_id":"2","type":"user","participant":"devs"}`, true); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[3].body; got != `{"type":0,"participant":"devs","permissionEdit":false,"permissionShare":false,"permissionManage":false}` {
		t.Errorf("user add body = %s", got)
	}
	// update keeps what is not given
	result, err = deckACLWrite(t, deckACLTools, "nextcloud.deckacl.update", `{"board_id":"2","acl_id":"4","can_share":true}`, true)
	if err != nil || !strings.Contains(string(result), `"updated":true`) || !strings.Contains(string(result), `"participant":"anna"`) {
		t.Fatalf("update = %s, %v", result, err)
	}
	if c := (*calls)[5]; c.method != http.MethodPut || c.url.Path != deckACLPath+"/4" ||
		c.body != `{"permissionEdit":true,"permissionShare":true,"permissionManage":false}` {
		t.Errorf("update = %+v", c)
	}
	if _, err := deckACLWrite(t, deckACLTools, "nextcloud.deckacl.update", `{"board_id":"2","acl_id":"4","can_edit":false}`, true); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[7].body; got != `{"permissionEdit":false,"permissionShare":false,"permissionManage":false}` {
		t.Errorf("edit update body = %s", got)
	}
	result, err = deckACLWrite(t, deckACLTools, "nextcloud.deckacl.remove", `{"board_id":"2","acl_id":"5"}`, true)
	if err != nil || !strings.Contains(string(result), `"removed":true`) {
		t.Fatalf("remove = %s, %v", result, err)
	}
	if c := (*calls)[9]; len(*calls) != 10 || c.method != http.MethodDelete || c.url.Path != deckACLPath+"/5" || c.body != "" {
		t.Errorf("remove call = %+v", c)
	}
}

func TestDeckACLUnclearOutcomeIsNeverRepeated(t *testing.T) {
	unclear := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbage": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `not json`), nil },
	}
	clear := map[string]func() (*http.Response, error){
		"307": func() (*http.Response, error) {
			response := status(307)
			response.Header.Set("Location", "https://elsewhere.invalid/"+deckCanary)
			return response, nil
		},
		"403": func() (*http.Response, error) { return status(403), nil },
		"404": func() (*http.Response, error) { return status(404), nil },
	}
	for _, op := range deckACLCalls {
		method := map[string]string{"add": http.MethodPost, "update": http.MethodPut, "remove": http.MethodDelete}[op.name]
		check := func(t *testing.T, answer func() (*http.Response, error), hint, garbage bool) {
			var methods []string
			serve(t, func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				if request.Method == http.MethodGet {
					return jsonResponse(http.StatusOK, boardWithACL(true)), nil
				}
				return answer()
			})
			_, err := deckACLWrite(t, deckACLTools, op.id, op.args, true)
			// A revocation needs no answer body, so unreadable text after a 200 is not an error there.
			if garbage && op.name == "remove" {
				if err != nil {
					t.Errorf("remove with a 200 failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("no error")
			}
			if len(methods) != 2 || methods[1] != method {
				t.Errorf("requests = %v", methods)
			}
			text := err.Error()
			if strings.Contains(text, bodyCanary) || strings.Contains(text, "elsewhere") || strings.Contains(text, deckCanary) {
				t.Errorf("provider text leaked: %v", err)
			}
			if has := strings.Contains(text, "may have been applied"); has != hint {
				t.Errorf("hint present = %v in %q", has, text)
			}
			if hint && !strings.Contains(text, "deckboards.get") {
				t.Errorf("no verification advice in %q", text)
			}
		}
		for name, answer := range unclear {
			t.Run(op.name+" unclear "+name, func(t *testing.T) { check(t, answer, true, name == "garbage") })
		}
		for name, answer := range clear {
			t.Run(op.name+" clear "+name, func(t *testing.T) { check(t, answer, false, false) })
		}
	}
}

func TestDeckACLToolsAreAllowListOnly(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	for _, op := range deckACLCalls {
		if _, err := deckACLWrite(t, []string{deckBoardsGet.ID}, op.id, op.args, true); err == nil {
			t.Errorf("%s without a tools list entry accepted", op.name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.deckacl.") {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
	effects := map[string]capability.Effect{deckACLAdd.ID: capability.EffectCreate,
		deckACLUpdate.ID: capability.EffectUpdate, deckACLRemove.ID: capability.EffectDelete}
	for _, d := range []capability.Descriptor{deckACLAdd, deckACLUpdate, deckACLRemove} {
		if !d.RequiresToolAllowList || d.Risk.Effect != effects[d.ID] || d.Risk.Confirmation != capability.ConfirmationRequired ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.Risk.Idempotency == "" {
			t.Errorf("descriptor %s = %+v", d.ID, d)
		}
	}
}
