package penpot

import (
	"net/http"
	"strings"
	"testing"
)

const (
	hookA       = "00000000-0000-0000-0000-000000000051"
	hookForeign = "00000000-0000-0000-0000-000000000052"
	hookURL     = "https://hooks.example.com/secret-path-canary?token=query-canary"
)

var webhookTools = []string{webhooksList.ID, webhooksUpdate.ID, webhooksDelete.ID}

// webhookHandler lists one webhook (hookA) for teamA and answers every change with the given status and body.
func webhookHandler(status int, answer string) func(call) (*http.Response, error) {
	return func(c call) (*http.Response, error) {
		if isChange(c.command()) {
			return jsonResponse(status, answer), nil
		}
		if c.command() == cmdWebhooks && c.body["team-id"] == teamA {
			return jsonResponse(200, `[{"id":"`+hookA+`","uri":"https://user:pw-canary@hooks.example.com/secret-path-canary?token=query-canary",`+
				`"mtype":"application/json","isActive":true,"errorCode":null,"errorCount":2}]`), nil
		}
		return jsonResponse(200, `[]`), nil
	}
}

func TestWebhooksListShowsHostOnly(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, webhookHandler(200, ``))
	result, err := env.invoke(webhooksList.ID, "one", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, hookA) || !strings.Contains(result, `"host":"hooks.example.com"`) ||
		strings.Contains(result, "canary") || strings.Contains(result, "pw-") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if len(calls) != 1 || calls[0].command() != cmdWebhooks || calls[0].body["team-id"] != teamA {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestWebhooksRefuseForeignTeamBeforeSecretAndIO(t *testing.T) {
	for _, test := range []struct{ tool, args string }{
		{webhooksList.ID, `{"team_id":"` + teamForeign + `"}`},
		{webhooksUpdate.ID, `{"team_id":"` + teamForeign + `","webhook_id":"` + hookA + `","url":"https://hooks.example.com/x","mtype":"application/json","is_active":true}`},
		{webhooksDelete.ID, `{"team_id":"` + teamForeign + `","webhook_id":"` + hookA + `"}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, webhookHandler(200, ``))
		_, err := env.invokeConfirmed(test.tool, "write", test.args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), teamForeign) || len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: err = %v, calls = %d, reads = %d", test.tool, err, len(calls), *env.reads)
		}
	}
}

func TestWebhookChangesSendOneFixedCommand(t *testing.T) {
	for _, test := range []struct {
		tool, args, command, want string
		body                      map[string]any
		reads                     int
		answer                    string
	}{
		{webhooksUpdate.ID, `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `","url":"` + hookURL + `","mtype":"application/json","is_active":false}`,
			cmdUpdateWebhook, `"updated":true`,
			map[string]any{"id": hookA, "uri": hookURL, "mtype": "application/json", "is-active": false}, 1, `{"id":"` + hookA + `"}`},
		{webhooksDelete.ID, `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `"}`, cmdDeleteWebhook, `"deleted":true`,
			map[string]any{"id": hookA}, 1, `null`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, webhookHandler(200, test.answer))
		result, err := env.invokeConfirmed(test.tool, "write", test.args)
		if err != nil || !strings.Contains(result, test.want) || strings.Contains(result, "canary") {
			t.Errorf("%s: result = %s, err = %v", test.tool, result, err)
			continue
		}
		sent := changes(calls)
		if len(sent) != 1 || sent[0].command() != test.command || len(sent[0].body) != len(test.body) || len(calls) != test.reads+1 {
			t.Errorf("%s: calls = %+v", test.tool, calls)
			continue
		}
		for key, want := range test.body {
			if sent[0].body[key] != want {
				t.Errorf("%s: body[%s] = %v, want %v", test.tool, key, sent[0].body[key], want)
			}
		}
	}
}

func TestWebhookChangesRefuseForeignIDsAndBadInput(t *testing.T) {
	good := `"url":"https://hooks.example.com/x","mtype":"application/json"`
	for name, test := range map[string]struct{ tool, connection, args string }{
		"foreign id update": {webhooksUpdate.ID, "write", `{"team_id":"` + teamA + `","webhook_id":"` + hookForeign + `",` + good + `,"is_active":true}`},
		"foreign id delete": {webhooksDelete.ID, "write", `{"team_id":"` + teamA + `","webhook_id":"` + hookForeign + `"}`},
		"http url":          {webhooksUpdate.ID, "write", `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `","url":"http://hooks.example.com/x","mtype":"application/json","is_active":true}`},
		"ip url":            {webhooksUpdate.ID, "write", `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `","url":"https://127.0.0.1/x","mtype":"application/json","is_active":true}`},
		"userinfo url":      {webhooksUpdate.ID, "write", `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `","url":"https://u:p@hooks.example.com/","mtype":"application/json","is_active":true}`},
		"bad mtype":         {webhooksUpdate.ID, "write", `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `","url":"https://hooks.example.com/x","mtype":"text/plain","is_active":true}`},
		"allow-list delete": {webhooksDelete.ID, "writenarrow", `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `"}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, webhookHandler(200, `{"id":"`+hookA+`"}`))
		_, err := env.invokeConfirmed(test.tool, test.connection, test.args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), hookForeign) || len(changes(calls)) != 0 {
			t.Errorf("%s: err = %v, changes = %d", name, err, len(changes(calls)))
		}
	}
}

func TestWebhookChangesNeedConfirmAndDeleteNeedsToolAllowList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, webhookHandler(200, ``))
	if _, err := env.invoke(webhooksDelete.ID, "write", `{"team_id":"`+teamA+`","webhook_id":"`+hookA+`"}`); err == nil || len(calls) != 0 {
		t.Errorf("unconfirmed delete: err = %v, calls = %d", err, len(calls))
	}
	if _, err := env.invokeConfirmed(webhooksDelete.ID, "one", `{"team_id":"`+teamA+`","webhook_id":"`+hookA+`"}`); err == nil || len(calls) != 0 {
		t.Errorf("delete without tool list: err = %v, calls = %d", err, len(calls))
	}
	if !webhooksDelete.RequiresToolAllowList {
		t.Error("webhooks.delete must require the tool allow-list")
	}
}

func TestWebhookChangesSendOnceAndReportUncertainty(t *testing.T) {
	args := map[string]string{
		webhooksUpdate.ID: `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `","url":"https://hooks.example.com/x","mtype":"application/json","is_active":true}`,
		webhooksDelete.ID: `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `"}`,
	}
	for tool, arguments := range args {
		for _, test := range []struct {
			status    int
			body      string
			uncertain bool
		}{{500, bodyCanary, true}, {502, bodyCanary, true}, {403, bodyCanary, false}, {429, bodyCanary, false},
			{200, `not json`, false}} {
			var calls []call
			env := newEnvironment(t, &calls, webhookHandler(test.status, test.body))
			_, err := env.invokeConfirmed(tool, "write", arguments)
			if test.status == 200 && !test.uncertain {
				if err != nil {
					t.Errorf("%s: err = %v", tool, err)
				}
				continue
			}
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "not json") ||
				strings.Contains(err.Error(), tokenValue) ||
				strings.Contains(err.Error(), "may have taken effect") != test.uncertain || len(changes(calls)) != 1 {
				t.Errorf("%s status %d: err = %v, changes = %d", tool, test.status, err, len(changes(calls)))
			}
		}
		var calls []call
		env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if isChange(c.command()) {
				return nil, errResetByPeer
			}
			return webhookHandler(200, ``)(c)
		})
		_, err := env.invokeConfirmed(tool, "write", arguments)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(changes(calls)) != 1 {
			t.Errorf("%s: err = %v, changes = %d", tool, err, len(changes(calls)))
		}
	}
}
