package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const talkWriteFeatures = `{"version":{"string":"31"},"capabilities":{"spreed":{"features":` +
	`["chat-v2","chat-reference-id","edit-messages","reactions"]}}}`

// talkWriter is a core whose connection may change Talk and lists the write tools, the delete tool included.
func talkWriter(t *testing.T, targets ...string) *application.Core {
	t.Helper()
	cfg := coreConfig()
	connection := cfg.Connections["reports"]
	connection.Target = ""
	connection.Targets = targets
	connection.Permissions = []config.Permission{config.PermissionRead, config.PermissionCreate,
		config.PermissionUpdate, config.PermissionDelete}
	connection.Tools = []string{talkMessagesSend.ID, talkMessagesEdit.ID, talkMessagesDelete.ID, talkReactionsSet.ID}
	cfg.Connections["reports"] = connection
	red := &redact.Redactor{}
	return application.New(registry(t), cfg, resolver(red), red)
}

type talkWriteCase struct {
	name, operation, extra, method, path string
	form                                 url.Values
}

func talkWriteCases() []talkWriteCase {
	const chat = "/ocs/v2.php/apps/spreed/api/v1/chat/" + boundRoom
	const reaction = "/ocs/v2.php/apps/spreed/api/v1/reaction/" + boundRoom + "/42"
	return []talkWriteCase{
		{"send", "nextcloud.talkmessages.send", `,"message":"Hello","reply_to":"7","silent":true`, http.MethodPost, chat,
			url.Values{"message": {"Hello"}, "replyTo": {"7"}, "silent": {"true"}}},
		{"edit", "nextcloud.talkmessages.edit", `,"message_id":"42","message":"Fixed"`, http.MethodPut, chat + "/42",
			url.Values{"message": {"Fixed"}}},
		{"delete", "nextcloud.talkmessages.delete", `,"message_id":"42"`, http.MethodDelete, chat + "/42", nil},
		{"react", "nextcloud.talkreactions.set", `,"message_id":"42","reaction":"👍","add":true`, http.MethodPost, reaction,
			url.Values{"reaction": {"👍"}}},
		{"unreact", "nextcloud.talkreactions.set", `,"message_id":"42","reaction":"👍","add":false`, http.MethodDelete, reaction, nil},
	}
}

// talkWriteServer answers the capability read normally and every other request with the given answer.
func talkWriteServer(t *testing.T, answer func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/ocs/v2.php/cloud/capabilities" {
			return ocsResponse(http.StatusOK, ocsData(talkWriteFeatures)), nil
		}
		return answer(request)
	})
}

func okAnswer(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/chat/") {
		return ocsResponse(http.StatusCreated, ocsData(`{"id":99,"timestamp":1767225600,"actorType":"users","actorId":"alice",`+
			`"messageType":"comment","message":"Hello {mention-user1}","referenceId":"echo",`+
			`"messageParameters":{"mention-user1":{"type":"user","id":"bob","name":"Bob"}}}`)), nil
	}
	return ocsResponse(http.StatusOK, ocsData(`[]`)), nil
}

func talkWrite(t *testing.T, c talkWriteCase, confirmed bool) error {
	t.Helper()
	_, err := talkWriter(t, "talk/"+boundRoom).Invoke(context.Background(), application.InvokeRequest{
		Operation: c.operation, Connection: "reports", Arguments: json.RawMessage(talkArgs(boundRoom, c.extra)), Confirmed: confirmed,
	})
	return err
}

func TestTalkWritesNeedConfirmationAndSendOneRequest(t *testing.T) {
	for _, c := range talkWriteCases() {
		t.Run(c.name, func(t *testing.T) {
			refuse(t)
			if err := talkWrite(t, c, false); err == nil || !strings.Contains(err.Error(), "confirm") {
				t.Fatal("ran without confirm")
			}
			calls := talkWriteServer(t, okAnswer)
			if err := talkWrite(t, c, true); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 2 || (*calls)[0].url.Path != "/ocs/v2.php/cloud/capabilities" {
				t.Fatalf("calls = %+v", *calls)
			}
			got := (*calls)[1]
			if got.method != c.method || got.url.Path != c.path {
				t.Errorf("request = %s %s", got.method, got.url.Path)
			}
			form, _ := url.ParseQuery(got.body)
			for name, want := range c.form {
				if form.Get(name) != want[0] {
					t.Errorf("form %s = %q", name, form.Get(name))
				}
			}
			if c.method == http.MethodDelete && c.form == nil && got.body != "" {
				t.Errorf("body = %q", got.body)
			}
			if c.name == "unreact" && got.url.Query().Get("reaction") != "👍" {
				t.Errorf("query = %s", got.url.RawQuery)
			}
		})
	}
}

func TestTalkSendReportsReferenceAndNormalisedMessage(t *testing.T) {
	calls := talkWriteServer(t, okAnswer)
	c := talkWriteCases()[0]
	out, err := talkWriter(t, "talk/"+boundRoom).Invoke(context.Background(), application.InvokeRequest{
		Operation: c.operation, Connection: "reports", Arguments: json.RawMessage(talkArgs(boundRoom, `,"message":"Hello"`)), Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var result SendResult
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatal(err)
	}
	form, _ := url.ParseQuery((*calls)[len(*calls)-1].body)
	reference := form.Get("referenceId")
	if len(reference) != 64 || result.ReferenceID != reference || !result.Sent || result.Message.ID != "99" ||
		result.Message.Text != "Hello @Bob" || form.Has("replyTo") || form.Has("silent") {
		t.Errorf("result = %s, form = %v", out.Result, form)
	}
}

func TestTalkWritesClassifyFailures(t *testing.T) {
	for _, c := range talkWriteCases() {
		t.Run(c.name, func(t *testing.T) {
			answers := map[string]struct {
				answer    func(*http.Request) (*http.Response, error)
				class     provider.Class
				uncertain bool
			}{
				"429":  {func(*http.Request) (*http.Response, error) { return status(429), nil }, provider.ClassRateLimited, false},
				"500":  {func(*http.Request) (*http.Response, error) { return status(500), nil }, provider.ClassProviderError, true},
				"503":  {func(*http.Request) (*http.Response, error) { return status(503), nil }, provider.ClassUnreachable, true},
				"drop": {func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") }, "", true},
				"body": {func(*http.Request) (*http.Response, error) {
					return ocsResponse(http.StatusOK, `{"nope":true}`), nil
				}, provider.ClassInvalidResponse, true},
				"403": {func(*http.Request) (*http.Response, error) { return status(403), nil }, provider.ClassPermission, false},
				"400": {func(*http.Request) (*http.Response, error) { return status(400), nil }, provider.ClassProviderError, false},
				"404": {func(*http.Request) (*http.Response, error) { return status(404), nil }, provider.ClassNotFound, false},
				"302": {func(*http.Request) (*http.Response, error) { return status(302), nil }, provider.ClassProviderError, false},
			}
			for name, a := range answers {
				calls := talkWriteServer(t, a.answer)
				err := talkWrite(t, c, true)
				if err == nil {
					t.Fatalf("%s: no error", name)
				}
				text := err.Error()
				var providerErr *provider.Error
				if a.class != "" && (!errors.As(err, &providerErr) || providerErr.Class != a.class) {
					t.Errorf("%s: err = %v", name, err)
				}
				if has := strings.Contains(text, "may have been"); has != a.uncertain {
					t.Errorf("%s: hint present = %v in %q", name, has, text)
				}
				if c.name == "send" && a.uncertain {
					form, _ := url.ParseQuery((*calls)[1].body)
					if !strings.Contains(text, form.Get("referenceId")) {
						t.Errorf("%s: no reference in %q", name, text)
					}
				}
				for _, leaked := range []string{bodyCanary, boundRoom} {
					if strings.Contains(text, leaked) {
						t.Errorf("%s: leaked %q in %q", name, leaked, text)
					}
				}
				if len(*calls) != 2 {
					t.Errorf("%s: %d requests, want the capability read and one change", name, len(*calls))
				}
			}
		})
	}
}

func TestTalkWritesRefuseUnusableArgumentsBeforeAnyIO(t *testing.T) {
	refuse(t)
	cases := map[string][2]string{
		"unbound room":        {"nextcloud.talkmessages.send", talkArgs(foreignRoom, `,"message":"x"`)},
		"reply not numeric":   {"nextcloud.talkmessages.send", talkArgs(boundRoom, `,"message":"x","reply_to":"1/../2"`)},
		"empty text":          {"nextcloud.talkmessages.send", talkArgs(boundRoom, `,"message":"  "`)},
		"long text":           {"nextcloud.talkmessages.send", talkArgs(boundRoom, `,"message":"`+strings.Repeat("a", 4001)+`"`)},
		"edit id not numeric": {"nextcloud.talkmessages.edit", talkArgs(boundRoom, `,"message_id":"4x","message":"x"`)},
		"edit id empty":       {"nextcloud.talkmessages.edit", talkArgs(boundRoom, `,"message_id":"","message":"x"`)},
		"delete id traversal": {"nextcloud.talkmessages.delete", talkArgs(boundRoom, `,"message_id":"../42"`)},
		"delete unbound":      {"nextcloud.talkmessages.delete", talkArgs(foreignRoom, `,"message_id":"42"`)},
		"reaction text":       {"nextcloud.talkreactions.set", talkArgs(boundRoom, `,"message_id":"42","reaction":"ok","add":true`)},
		"reaction two words":  {"nextcloud.talkreactions.set", talkArgs(boundRoom, `,"message_id":"42","reaction":"👍 👍","add":true`)},
		"reaction no add":     {"nextcloud.talkreactions.set", talkArgs(boundRoom, `,"message_id":"42","reaction":"👍"`)},
		"reaction id":         {"nextcloud.talkreactions.set", talkArgs(boundRoom, `,"message_id":"x","reaction":"👍","add":true`)},
	}
	for name, c := range cases {
		_, err := talkWriter(t, "talk/"+boundRoom).Invoke(context.Background(), application.InvokeRequest{
			Operation: c[0], Connection: "reports", Arguments: json.RawMessage(c[1]), Confirmed: true,
		})
		if err == nil || strings.Contains(err.Error(), foreignRoom) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, targets := range [][]string{{"folder/Reports"}, {"account"}} {
		_, err := talkWriter(t, targets...).Invoke(context.Background(), application.InvokeRequest{
			Operation: "nextcloud.talkmessages.send", Connection: "reports", Confirmed: true,
			Arguments: json.RawMessage(talkArgs(boundRoom, `,"message":"x"`)),
		})
		if err == nil || strings.Contains(err.Error(), boundRoom) {
			t.Errorf("%v: err = %v", targets, err)
		}
	}
}

func TestTalkWritesNeedTheirCapabilityFeature(t *testing.T) {
	for _, c := range talkWriteCases() {
		calls := serve(t, func(request *http.Request) (*http.Response, error) {
			return ocsResponse(http.StatusOK, ocsData(`{"capabilities":{"spreed":{"features":["chat-v2"]}}}`)), nil
		})
		err := talkWrite(t, c, true)
		needs := c.name != "delete"
		if needs && (err == nil || len(*calls) != 1) || !needs && len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", c.name, err, len(*calls))
		}
	}
}
