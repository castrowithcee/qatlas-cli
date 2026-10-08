package excalidrawplus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var peopleToolIDs = []string{usersList.ID, usersGet.ID, usersUpdate.ID, usersRemove.ID}

const (
	linkCanary    = "https://plus.excalidraw.com/join/link-canary-77aa"
	personalCanry = "person-body-canary-31de"
)

func userBody(id string) string {
	return `{"id":"` + id + `","uid":"u","email":"a@example.com","name":"Ann","picture":"https://x.invalid/p.png",` +
		`"role":"member","created":"2026-01-01T00:00:00Z","lastActive":"2026-02-01T00:00:00Z",` +
		`"preferences":{"theme":"light"},"rateLimits":{},"workspaceTeams":{"g":["t1","t2"]}}`
}

func peopleHandler(sent *[]sentRequest, answer func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			*sent = append(*sent, sentRequest{r.Method, r.URL.Path, string(body)})
		}
		return answer(r)
	}
}

var allPeopleCalls = []struct{ op, args string }{
	{usersList.ID, `{}`},
	{usersGet.ID, `{"user_id":"u1"}`},
	{usersUpdate.ID, `{"user_id":"u1","role":"member"}`},
	{usersRemove.ID, `{"user_id":"u1"}`},
}

func TestPeopleDescriptorsCarryTheFullRisk(t *testing.T) {
	for _, d := range []capability.Descriptor{usersList, usersGet} {
		r := d.Risk
		if r.Effect != capability.EffectRead || r.Idempotency != capability.IdempotencySafe ||
			r.Confirmation != capability.ConfirmationNone || !r.OpenWorld || r.DataSensitivity != peopleSensitivity ||
			d.RequiresToolAllowList {
			t.Errorf("%s has an unexpected read contract: %+v", d.ID, d)
		}
	}
	for _, c := range []struct {
		d      capability.Descriptor
		effect capability.Effect
	}{{usersUpdate, capability.EffectUpdate}, {usersRemove, capability.EffectDelete}} {
		d, effect := c.d, c.effect
		r := d.Risk
		if r.Effect != effect || r.Idempotency != capability.IdempotencyUnknown ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != peopleSensitivity ||
			!d.RequiresToolAllowList {
			t.Errorf("%s has an unexpected change contract: %+v", d.ID, d)
		}
	}
}

func TestPeopleToolsAreOnlyInTheirOwnNotRecommendedProfile(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	recommended, _ := metadata.RecommendedProfile()
	for _, profile := range metadata.Profiles {
		for _, id := range peopleToolIDs {
			has := false
			for _, tool := range profile.Tools {
				has = has || tool == id
			}
			if has != (profile.ID == "people") {
				t.Errorf("profile %s: %s present = %t", profile.ID, id, has)
			}
		}
		if profile.ID == "people" && profile.Recommended || profile.ID == "people" && recommended.ID == "people" {
			t.Error("the people profile must not be recommended")
		}
	}
}

func TestPeopleToolsRefuseARestrictedConnectionBeforeSecretOrIO(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, peopleHandler(&sent, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{}`), nil
	}))
	for _, c := range allPeopleCalls {
		_, err := env.invokeConfirmed(c.op, "peopleOne", c.args)
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid request", c.op, err)
		} else if strings.Contains(err.Error(), ownCollection) {
			t.Errorf("%s: refusal names a target: %v", c.op, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestUsersListPaginatesAndBounds(t *testing.T) {
	var calls []call
	var sent []sentRequest
	long := strings.Repeat("n", 900)
	env := newEnvironment(t, &calls, peopleHandler(&sent, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"limit":2,"offset":4,"hasNextPage":true,"data":[`+userBody("u1")+`,`+
			`{"id":"bad id/..","name":"x"},{"id":"u2","name":"`+long+`","email":"b@example.com",`+
			`"workspaceTeams":{"g":["`+strings.Repeat("t", 500)+`"]}}]}`), nil
	}))
	out, err := env.invoke(usersList.ID, "peopleAll", `{"offset":4,"limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var page UsersPage
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 2 || page.Offset != 4 || page.Limit != 2 || !page.HasNextPage || page.NextOffset == nil ||
		*page.NextOffset != 6 || page.Users[0].Email != "a@example.com" || page.Users[0].Teams["g"][1] != "t2" {
		t.Fatalf("page = %+v", page)
	}
	if len(page.Users[1].Name) != 800 || len(page.Users[1].Teams["g"][0]) != maxTeamText {
		t.Fatalf("strings not bounded: %d %d", len(page.Users[1].Name), len(page.Users[1].Teams["g"][0]))
	}
	if strings.Contains(out, "picture") || strings.Contains(out, "preferences") || strings.Contains(out, "bad id") {
		t.Fatalf("unneeded fields passed on: %s", out)
	}
	if len(calls) != 1 || calls[0].method != http.MethodGet || calls[0].path != apiPath+"/workspaces/users" ||
		calls[0].query.Get("offset") != "4" || calls[0].query.Get("limit") != "2" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUsersGetReadsOneUser(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, peopleHandler(&sent, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, userBody(strings.TrimPrefix(r.URL.Path, apiPath+"/workspaces/users/"))), nil
	}))
	out, err := env.invoke(usersGet.ID, "peopleAll", `{"user_id":"u1"}`)
	if err != nil || !strings.Contains(out, `"id":"u1"`) || !strings.Contains(out, `"role":"member"`) ||
		len(calls) != 1 || calls[0].path != apiPath+"/workspaces/users/u1" {
		t.Fatalf("out = %s, err = %v, calls = %+v", out, err, calls)
	}
	if _, err := env.invoke(usersGet.ID, "peopleAll", `{"user_id":"a/b"}`); err == nil || len(calls) != 1 {
		t.Fatalf("invalid id: err = %v, calls = %d", err, len(calls))
	}
	// An answer for a different user is refused.
	other := newEnvironment(t, &calls, peopleHandler(&sent, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, userBody("u9")), nil
	}))
	if _, err := other.invoke(usersGet.ID, "peopleAll", `{"user_id":"u1"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("err = %v, want invalid response", err)
	}
}

func TestUpdateRemoveSendExactlyOneRequest(t *testing.T) {
	for _, c := range []struct {
		op, args, method, path, body string
	}{
		{usersUpdate.ID, `{"user_id":"u1","name":"Ann B","role":"admin"}`, http.MethodPatch,
			apiPath + "/workspaces/users/u1", `{"name":"Ann B","role":"admin"}`},
		{usersUpdate.ID, `{"user_id":"u1","role":"member"}`, http.MethodPatch,
			apiPath + "/workspaces/users/u1", `{"role":"member"}`},
		{usersRemove.ID, `{"user_id":"u1"}`, http.MethodDelete, apiPath + "/workspaces/users/u1", ``},
	} {
		var calls []call
		var sent []sentRequest
		env := newEnvironment(t, &calls, peopleHandler(&sent, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				return jsonResponse(200, `{"id":"inv-1","type":"email","status":"pending","email":"new.person@example.com",`+
					`"role":"member","link":"`+linkCanary+`","url":"`+linkCanary+`"}`), nil
			}
			return jsonResponse(200, userBody("u1")), nil
		}))
		out, err := env.invokeConfirmed(c.op, "peopleAll", c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		if len(sent) != 1 || sent[0].method != c.method || sent[0].path != c.path || sent[0].body != c.body ||
			len(calls) != 1 {
			t.Fatalf("%s: sent = %+v, calls = %d", c.op, sent, len(calls))
		}
		if strings.Contains(out, linkCanary) || strings.Contains(out, "link") {
			t.Fatalf("%s: a link was returned: %s", c.op, out)
		}
	}
}

func TestChangesNeedConfirmAndToolsListBeforeSecretOrIO(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, peopleHandler(&sent, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{}`), nil
	}))
	for _, c := range allPeopleCalls[2:] {
		if _, err := env.invokeConfirmed(c.op, "every", c.args); err == nil {
			t.Errorf("%s without tools list succeeded", c.op)
		}
		if _, err := env.core.Invoke(context.Background(), application.InvokeRequest{Operation: c.op,
			Connection: "peopleAll", Arguments: json.RawMessage(c.args)}); err == nil {
			t.Errorf("%s without confirm succeeded", c.op)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestPeopleValidationRefusesBeforeIO(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, peopleHandler(&sent, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{}`), nil
	}))
	for name, c := range map[string]struct{ op, args string }{
		"role":         {usersUpdate.ID, `{"user_id":"u1","role":"owner"}`},
		"empty update": {usersUpdate.ID, `{"user_id":"u1"}`},
		"name control": {usersUpdate.ID, `{"user_id":"u1","name":"a\u0000b"}`},
		"blank name":   {usersUpdate.ID, `{"user_id":"u1","name":"   "}`},
		"user id":      {usersUpdate.ID, `{"user_id":"u1/../x","role":"member"}`},
		"remove id":    {usersRemove.ID, `{"user_id":"../u"}`},
		"extra teams":  {usersUpdate.ID, `{"user_id":"u1","teams":["t"]}`},
	} {
		if _, err := env.invokeConfirmed(c.op, "peopleAll", c.args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestChangesAreNeverRepeatedAndSayTheyMayHaveTakenEffect(t *testing.T) {
	for name, answer := range map[string]func() (*http.Response, error){
		"500": func() (*http.Response, error) {
			return jsonResponse(500, `{"message":"`+personalCanry+linkCanary+`"}`), nil
		},
		"timeout": func() (*http.Response, error) { return nil, context.DeadlineExceeded },
		"reset":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbage": func() (*http.Response, error) { return jsonResponse(200, `not json `+linkCanary), nil },
		"no id":   func() (*http.Response, error) { return jsonResponse(200, `{"role":"member"}`), nil },
	} {
		for _, c := range allPeopleCalls[2:] {
			if name == "garbage" && c.op == usersRemove.ID {
				continue // a removal's answer body is dropped
			}
			var calls []call
			var sent []sentRequest
			env := newEnvironment(t, &calls, peopleHandler(&sent, func(*http.Request) (*http.Response, error) {
				return answer()
			}))
			_, err := env.invokeConfirmed(c.op, "peopleAll", c.args)
			if name == "no id" && c.op == usersRemove.ID {
				continue
			}
			if err == nil {
				t.Errorf("%s/%s: no error", name, c.op)
				continue
			}
			if len(calls) != 1 {
				t.Errorf("%s/%s: %d requests, want exactly one", name, c.op, len(calls))
			}
			if !strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("%s/%s: no uncertainty message: %v", name, c.op, err)
			}
			if strings.Contains(err.Error(), personalCanry) || strings.Contains(err.Error(), linkCanary) ||
				strings.Contains(err.Error(), "example.com") {
				t.Errorf("%s/%s: error leaked data: %v", name, c.op, err)
			}
		}
	}
}

func TestPeopleClientErrorsDoNotLeakProviderText(t *testing.T) {
	var calls []call
	var sent []sentRequest
	env := newEnvironment(t, &calls, peopleHandler(&sent, func(*http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+personalCanry+`a@example.com"}`), nil
	}))
	for _, c := range allPeopleCalls {
		_, err := env.invokeConfirmed(c.op, "peopleAll", c.args)
		if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), personalCanry) ||
			strings.Contains(err.Error(), "example.com") {
			t.Errorf("%s: err = %v", c.op, err)
		}
	}
}
