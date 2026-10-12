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

const deckLabelsPath = deckBoardsPath + "/2/labels"

var deckLabelTools = []string{deckBoardsGet.ID, deckLabelsCreate.ID, deckLabelsUpdate.ID, deckLabelsDelete.ID}

func deckLabelWrite(t *testing.T, tools []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	response, err := deckWriteCore(t, tools, "deck/2").Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirm,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

// boardWithLabels is a managed board answer that lists label 5 only.
func boardWithLabels(manage bool) string {
	return strings.Replace(managedBoard("2", "B", manage), `"acl":[]`,
		`"acl":[],"labels":[{"id":5,"title":"Bug","color":"E9322D"}]`, 1)
}

func labelAnswer(id, title, color string) string {
	return `{"id":` + id + `,"title":"` + title + `","color":"` + color + `","boardId":2}`
}

var deckLabelCalls = []struct{ name, id, args string }{
	{"create", "nextcloud.decklabels.create", `{"board_id":"2","title":"x","color":"31CC7C"}`},
	{"update", "nextcloud.decklabels.update", `{"board_id":"2","label_id":"5","title":"x"}`},
	{"delete", "nextcloud.decklabels.delete", `{"board_id":"2","label_id":"5"}`},
}

func TestDeckLabelsRefuseBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	for name, tc := range map[string]struct{ op, args string }{
		"create unbound": {"create", `{"board_id":"9","title":"x","color":"31CC7C"}`},
		"update unbound": {"update", `{"board_id":"9","label_id":"5","title":"x"}`},
		"delete unbound": {"delete", `{"board_id":"9","label_id":"5"}`},
		"bad board":      {"create", `{"board_id":"2/..","title":"x","color":"31CC7C"}`},
		"bad label":      {"delete", `{"board_id":"2","label_id":"5/.."}`},
		"no label":       {"update", `{"board_id":"2","title":"x"}`},
		"no title":       {"create", `{"board_id":"2","color":"31CC7C"}`},
		"no color":       {"create", `{"board_id":"2","title":"x"}`},
		"empty title":    {"create", `{"board_id":"2","title":"  ","color":"31CC7C"}`},
		"long title":     {"create", `{"board_id":"2","title":"` + strings.Repeat("a", 101) + `","color":"31CC7C"}`},
		"hash color":     {"create", `{"board_id":"2","title":"x","color":"#31CC7C"}`},
		"short color":    {"create", `{"board_id":"2","title":"x","color":"31CC7"}`},
		"word color":     {"update", `{"board_id":"2","label_id":"5","color":"green!"}`},
		"no change":      {"update", `{"board_id":"2","label_id":"5"}`},
		"delete + color": {"delete", `{"board_id":"2","label_id":"5","color":"31CC7C"}`},
		"create + label": {"create", `{"board_id":"2","label_id":"5","title":"x","color":"31CC7C"}`},
		"free parameter": {"create", `{"board_id":"2","title":"x","color":"31CC7C","path":"/y"}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deckLabelWrite(t, deckLabelTools, "nextcloud.decklabels."+tc.op, tc.args, true)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "9") {
				t.Errorf("the refusal names the board: %v", err)
			}
		})
	}
	for _, op := range deckLabelCalls {
		if _, err := deckLabelWrite(t, deckLabelTools, op.id, op.args, false); err == nil {
			t.Errorf("unconfirmed %s accepted", op.name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestDeckLabelsRefuseWithoutMutation(t *testing.T) {
	// No manage right: all three stop after the read.
	for _, op := range deckLabelCalls {
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, boardWithLabels(false)), nil
		})
		if _, err := deckLabelWrite(t, deckLabelTools, op.id, op.args, true); err == nil {
			t.Fatalf("%s accepted without the manage right", op.name)
		}
		if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
			t.Errorf("%s calls = %+v, want one read", op.name, *calls)
		}
	}
	// A label of another board or an unknown one is refused alike, without naming it.
	for _, op := range []struct{ id, args string }{
		{"nextcloud.decklabels.update", `{"board_id":"2","label_id":"77","title":"x"}`},
		{"nextcloud.decklabels.delete", `{"board_id":"2","label_id":"77"}`},
	} {
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, boardWithLabels(true)), nil
		})
		_, err := deckLabelWrite(t, deckLabelTools, op.id, op.args, true)
		if err == nil || strings.Contains(err.Error(), "77") {
			t.Errorf("%s: err = %v", op.id, err)
		}
		if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
			t.Errorf("%s calls = %+v, want one read", op.id, *calls)
		}
	}
}

func TestDeckLabelsSendOneFixedRequest(t *testing.T) {
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodGet:
			return jsonResponse(http.StatusOK, boardWithLabels(true)), nil
		case http.MethodPost:
			return jsonResponse(http.StatusOK, labelAnswer("8", "New", "31CC7C")), nil
		case http.MethodPut:
			return jsonResponse(http.StatusOK, labelAnswer("5", "Bug", "31CC7C")), nil
		}
		return jsonResponse(http.StatusOK, `[]`), nil
	})
	result, err := deckLabelWrite(t, deckLabelTools, "nextcloud.decklabels.create", `{"board_id":"2","title":" \"New\" ","color":"31CC7C"}`, true)
	if err != nil || !strings.Contains(string(result), `"created":true`) || !strings.Contains(string(result), `"id":"8"`) {
		t.Fatalf("create = %s, %v", result, err)
	}
	if len(*calls) != 2 || (*calls)[1].method != http.MethodPost || (*calls)[1].url.Path != deckLabelsPath ||
		(*calls)[1].body != `{"title":"\"New\"","color":"31CC7C"}` {
		t.Fatalf("calls = %+v", *calls)
	}
	// update keeps what is not given
	if _, err := deckLabelWrite(t, deckLabelTools, "nextcloud.decklabels.update", `{"board_id":"2","label_id":"5","title":"Next"}`, true); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[3]; c.method != http.MethodPut || c.url.Path != deckLabelsPath+"/5" || c.body != `{"title":"Next","color":"E9322D"}` {
		t.Errorf("title update = %+v", c)
	}
	if _, err := deckLabelWrite(t, deckLabelTools, "nextcloud.decklabels.update", `{"board_id":"2","label_id":"5","color":"31CC7C"}`, true); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[5].body; got != `{"title":"Bug","color":"31CC7C"}` {
		t.Errorf("color update body = %s", got)
	}
	result, err = deckLabelWrite(t, deckLabelTools, "nextcloud.decklabels.delete", `{"board_id":"2","label_id":"5"}`, true)
	if err != nil || !strings.Contains(string(result), `"deleted":true`) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if c := (*calls)[7]; len(*calls) != 8 || c.method != http.MethodDelete || c.url.Path != deckLabelsPath+"/5" || c.body != "" {
		t.Errorf("delete call = %+v", c)
	}
}

func TestDeckLabelsUnclearOutcomeIsNeverRepeated(t *testing.T) {
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
	for _, op := range deckLabelCalls {
		method := map[string]string{"create": http.MethodPost, "update": http.MethodPut, "delete": http.MethodDelete}[op.name]
		check := func(t *testing.T, answer func() (*http.Response, error), hint, garbage bool) {
			var methods []string
			serve(t, func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				if request.Method == http.MethodGet {
					return jsonResponse(http.StatusOK, boardWithLabels(true)), nil
				}
				return answer()
			})
			_, err := deckLabelWrite(t, deckLabelTools, op.id, op.args, true)
			// A deletion needs no answer body, so unreadable text after a 200 is not an error there.
			if garbage && op.name == "delete" {
				if err != nil {
					t.Errorf("delete with a 200 failed: %v", err)
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

func TestDeckLabelsDeleteIsAllowListOnly(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, errors.New("no request expected")
	})
	_, err := deckLabelWrite(t, []string{deckBoardsGet.ID, deckLabelsUpdate.ID}, deckLabelsDelete.ID,
		`{"board_id":"2","label_id":"5"}`, true)
	if err == nil || len(*calls) != 0 {
		t.Errorf("delete without a tools list: %v, calls %d", err, len(*calls))
	}
	if !deckLabelsDelete.RequiresToolAllowList || deckLabelsCreate.RequiresToolAllowList || deckLabelsUpdate.RequiresToolAllowList ||
		deckLabelsDelete.Risk.Effect != capability.EffectDelete || deckLabelsCreate.Risk.Effect != capability.EffectCreate ||
		deckLabelsUpdate.Risk.Effect != capability.EffectUpdate {
		t.Errorf("descriptors = %+v", deckLabelsDelete)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.decklabels.") {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
	for _, d := range []capability.Descriptor{deckLabelsCreate, deckLabelsUpdate, deckLabelsDelete} {
		if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Errorf("risk of %s = %+v", d.ID, d.Risk)
		}
	}
}
