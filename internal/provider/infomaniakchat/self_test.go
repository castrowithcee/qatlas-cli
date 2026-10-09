package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

var fixedNow = time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)

// selfServer answers users/me with selfID; mutate handles the one changing request.
func selfServer(t *testing.T, mutate func(*http.Request) (*http.Response, error),
) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me" {
			return jsonResponse(200, `{"id":"`+selfID+`","email":"`+messageCanary+`"}`), nil
		}
		if r.Method != http.MethodGet && mutate != nil {
			return mutate(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func fixClock(t *testing.T) {
	previous := clock
	clock = func() time.Time { return fixedNow }
	t.Cleanup(func() { clock = previous })
}

func okAnswer(*http.Request) (*http.Response, error) {
	return jsonResponse(200, `{"status":"OK"}`), nil
}

func lastCall(calls []call) call { return calls[len(calls)-1] }

func TestStatusSetTargetsTheOwnUserWithOnlyTheTypedBody(t *testing.T) {
	fixClock(t)
	var calls []call
	env := newEnvironment(t, &calls, selfServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"user_id":"`+selfID+`","status":"dnd","manual":true,"email":"x"}`), nil
	}))
	result, err := env.confirmed(statusSet.ID, "editor", `{"status":"dnd","dnd_end_time":"2030-01-01T13:00:00Z"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	last := lastCall(calls)
	var body map[string]any
	if err := json.Unmarshal([]byte(last.body), &body); err != nil || len(body) != 3 || body["user_id"] != selfID ||
		body["status"] != "dnd" || body["dnd_end_time"] != float64(fixedNow.Add(time.Hour).Unix()) {
		t.Fatalf("body = %s, %v", last.body, err)
	}
	if len(calls) != 2 || last.method != http.MethodPut || last.path != "/api/v4/users/"+selfID+"/status" ||
		countMethod(calls, http.MethodPut) != 1 {
		t.Fatalf("calls = %+v, want users/me and one PUT on the own status", calls)
	}
	if !sameJSON(result, `{"user_id":"`+selfID+`","status":"dnd","dnd_end_time":"2030-01-01T13:00:00Z"}`) {
		t.Fatalf("result = %s", result)
	}
}

func TestSelfToolsRefuseInvalidArgumentsBeforeSecretAccess(t *testing.T) {
	fixClock(t)
	long := strings.Repeat("a", 101)
	cases := []struct{ id, args string }{
		{statusSet.ID, `{"status":"invisible"}`},
		{statusSet.ID, `{"status":"away","dnd_end_time":"2030-01-01T13:00:00Z"}`},
		{statusSet.ID, `{"status":"dnd","dnd_end_time":"2029-01-01T13:00:00Z"}`},
		{statusSet.ID, `{"status":"dnd","dnd_end_time":"tomorrow"}`},
		{customStatusSet.ID, `{}`},
		{customStatusSet.ID, `{"emoji":"Bad Emoji"}`},
		{customStatusSet.ID, `{"emoji":"../x"}`},
		{customStatusSet.ID, `{"text":"` + long + `"}`},
		{customStatusSet.ID, `{"text":"a\u0007b"}`},
		{customStatusSet.ID, `{"text":"x","duration":"forever"}`},
		{customStatusSet.ID, `{"text":"x","duration":"date_and_time"}`},
		{customStatusSet.ID, `{"text":"x","duration":"one_hour","expires_at":"2030-02-01T00:00:00Z"}`},
		{customStatusSet.ID, `{"text":"x","expires_at":"2030-02-01T00:00:00Z"}`},
		{customStatusSet.ID, `{"text":"x","duration":"date_and_time","expires_at":"2029-02-01T00:00:00Z"}`},
		{profileUpdate.ID, `{}`},
		{profileUpdate.ID, `{"email":"a@b.c"}`},
		{profileUpdate.ID, `{"username":"x"}`},
		{profileUpdate.ID, `{"nickname":"a\nb"}`},
		{profileUpdate.ID, `{"nickname":"` + strings.Repeat("a", 65) + `"}`},
		{profileUpdate.ID, `{"position":"` + strings.Repeat("a", 129) + `"}`},
		{statusSet.ID, `{"status":"away","user_id":"` + otherID + `"}`},
	}
	for _, tt := range cases {
		var calls []call
		env := newEnvironment(t, &calls, selfServer(t, okAnswer))
		if _, err := env.confirmed(tt.id, "editor", tt.args); err == nil || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s %s: err = %v, calls = %+v, reads = %d, want a local refusal", tt.id, tt.args, err, calls,
				*env.reads)
		}
	}
}

func TestCustomStatusSetSendsOnlyTheGivenFieldsToTheOwnUser(t *testing.T) {
	fixClock(t)
	var calls []call
	env := newEnvironment(t, &calls, selfServer(t, okAnswer))
	result, err := env.confirmed(customStatusSet.ID, "editor",
		`{"emoji":"calendar","text":"In a meeting","duration":"date_and_time","expires_at":"2030-01-02T10:00:00+01:00"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	last := lastCall(calls)
	var body map[string]any
	if err := json.Unmarshal([]byte(last.body), &body); err != nil || len(body) != 4 || body["emoji"] != "calendar" ||
		body["text"] != "In a meeting" || body["duration"] != "date_and_time" ||
		body["expires_at"] != "2030-01-02T09:00:00Z" {
		t.Fatalf("body = %s, %v", last.body, err)
	}
	if last.method != http.MethodPut || last.path != "/api/v4/users/"+selfID+"/status/custom" || len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if strings.Contains(result, messageCanary) || !strings.Contains(result, `"user_id":"`+selfID+`"`) {
		t.Fatalf("result = %s", result)
	}
	// An emoji alone and a duration without a time send nothing else.
	calls = nil
	if _, err := env.confirmed(customStatusSet.ID, "editor", `{"emoji":"zzz","duration":"today"}`); err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if lastCall(calls).body != `{"emoji":"zzz","duration":"today"}` {
		t.Fatalf("body = %s", lastCall(calls).body)
	}
}

func TestCustomStatusClearDeletesTheOwnCustomStatus(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, selfServer(t, okAnswer))
	result, err := env.confirmed(customStatusClear.ID, "editor", `{}`)
	if err != nil || !sameJSON(result, `{"user_id":"`+selfID+`","cleared":true}`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := lastCall(calls)
	if last.method != http.MethodDelete || last.path != "/api/v4/users/"+selfID+"/status/custom" ||
		last.body != "" || len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestProfileUpdateSendsOnlyTheAllowedFieldsToTheOwnUser(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, selfServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"id":"`+selfID+`","email":"`+messageCanary+`","position":"Engineer"}`), nil
	}))
	result, err := env.confirmed(profileUpdate.ID, "editor", `{"position":"Engineer","nickname":""}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	last := lastCall(calls)
	var body map[string]any
	if err := json.Unmarshal([]byte(last.body), &body); err != nil || len(body) != 2 || body["position"] != "Engineer" ||
		body["nickname"] != "" {
		t.Fatalf("body = %s, %v", last.body, err)
	}
	if last.method != http.MethodPut || last.path != "/api/v4/users/"+selfID+"/patch" || len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if strings.Contains(result, messageCanary) || !strings.Contains(result, `"position":"Engineer"`) ||
		!strings.Contains(result, `"nickname":""`) {
		t.Fatalf("result = %s", result)
	}
}

func TestSelfToolsRequireConfirmationAndUpdatePermission(t *testing.T) {
	args := map[string]string{statusSet.ID: `{"status":"away"}`, customStatusSet.ID: `{"text":"x"}`,
		customStatusClear.ID: `{}`, profileUpdate.ID: `{"position":"x"}`}
	for id, a := range args {
		var calls []call
		env := newEnvironment(t, &calls, selfServer(t, okAnswer))
		if _, err := env.invoke(id, "editor", a); !isConfirmationRequired(err) {
			t.Fatalf("%s err = %v, want confirmation-required", id, err)
		}
		if _, err := env.confirmed(id, "team", a); err == nil {
			t.Fatalf("%s: want a refusal without the update permission", id)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: calls = %+v, reads = %d, want none", id, calls, *env.reads)
		}
	}
}

func TestSelfToolsNeverRetryAnUnclearResult(t *testing.T) {
	args := map[string]string{statusSet.ID: `{"status":"away"}`, customStatusSet.ID: `{"text":"x"}`,
		customStatusClear.ID: `{}`, profileUpdate.ID: `{"position":"x"}`}
	for id, a := range args {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
			"mismatch": func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"id":"`+otherID+`","user_id":"`+otherID+`","status":"x"}`), nil
			},
		} {
			var calls []call
			env := newEnvironment(t, &calls, selfServer(t, fail))
			_, err := env.confirmed(id, "editor", a)
			if err == nil || !strings.Contains(err.Error(), "may have been") || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", id, name, err)
			}
			if n := len(calls) - countMethod(calls, http.MethodGet); n != 1 {
				t.Fatalf("%s %s: calls = %+v, want exactly one changing request", id, name, calls)
			}
		}
	}
}

func TestSelfToolsRefuseAnUnreadableOwnUserWithoutChange(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"id":"../x"}`), nil
	})
	if _, err := env.confirmed(statusSet.ID, "editor", `{"status":"away"}`); err == nil ||
		countMethod(calls, http.MethodPut) != 0 {
		t.Fatalf("err = %v, calls = %+v, want no change", err, calls)
	}
}

func TestSelfToolsAreOnlyInTheMessagingProfileExceptTheProfileUpdate(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == profileUpdate.ID {
				t.Fatalf("profile %s selects profile.update", profile.ID)
			}
			if profile.ID != "messaging" && (tool == statusSet.ID || tool == customStatusSet.ID ||
				tool == customStatusClear.ID) {
				t.Fatalf("profile %s selects %s", profile.ID, tool)
			}
		}
	}
	found := map[string]bool{}
	for _, tool := range metadata.Tools {
		if tool.ID == statusSet.ID || tool.ID == customStatusSet.ID || tool.ID == customStatusClear.ID ||
			tool.ID == profileUpdate.ID {
			found[tool.ID] = true
			if tool.Group != "users" || tool.Effect != config.PermissionUpdate || tool.RequiresToolAllowList {
				t.Fatalf("tool metadata = %+v", tool)
			}
		}
	}
	if len(found) != 4 {
		t.Fatalf("found = %v, want all four tools", found)
	}
	for _, profile := range metadata.Profiles {
		if profile.ID == "messaging" {
			for _, want := range []string{statusSet.ID, customStatusSet.ID, customStatusClear.ID} {
				if !contains(profile.Tools, want) {
					t.Fatalf("messaging lacks %s", want)
				}
			}
		}
	}
}

func sameJSON(a, b string) bool {
	var left, right map[string]any
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil || len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
