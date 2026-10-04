package makeapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	hookID         int64 = 77
	canaryUDID           = "udidcanary7f3a9c1e"
	canaryHookURL        = "https://hook.example.com/" + canaryUDID
	canaryMailAddr       = "mailcanary5d2b8e@example.com"
	canaryHeader         = "header-body-canary-1c9e"
)

func hookJSONOf(id, teamID, scenarioID int64, name string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"teamId":%d,"udid":%q,"type":"web","typeName":"gateway-webhook",`+
		`"packageName":"gateway","enabled":true,"gone":false,"queueCount":2,"queueLimit":10000,`+
		`"scenarioId":%d,"url":%q}`, id, name, teamID, canaryUDID, scenarioID, canaryHookURL)
}

func hookEnv(t *testing.T, calls *[]call, teamID, scenarioID int64) *environment {
	return newEnvironment(t, calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/hooks":
			return jsonResponse(200, `{"hooks":[`+hookJSONOf(hookID, ownTeam, ownScenario, "List "+canaryUDID)+`,`+
				hookJSONOf(88, foreignTeam, ownScenario, "Foreign")+`]}`), nil
		case strings.HasSuffix(r.URL.Path, "/ping"):
			return jsonResponse(200, fmt.Sprintf(`{"url":%q,"name":"Ping %s","attached":true,"learning":false,`+
				`"gone":false,"address":%q,"teamId":%d}`, canaryHookURL, canaryMailAddr, canaryMailAddr, teamID)), nil
		case strings.HasSuffix(r.URL.Path, "/logs"):
			return jsonResponse(200, `{"hookLogs":[{"id":5,"statusId":1,"loggedAt":"2026-01-01T00:00:00Z",`+
				`"parser":"`+canaryHookURL+`","replayable":true,"sizes":{"before":120,"after":80,"x":"bad"},`+
				`"udids":["`+canaryUDID+`"],"typeId":2,"headers":"`+canaryHeader+`"}]}`), nil
		default:
			return jsonResponse(200, `{"hook":`+hookJSONOf(hookID, teamID, scenarioID, "Hook "+canaryHookURL)+`}`), nil
		}
	})
}

func TestHooksRequestsUseDocumentedPathsAndQueries(t *testing.T) {
	var calls []call
	env := hookEnv(t, &calls, ownTeam, ownScenario)
	id := strconv.FormatInt(hookID, 10)
	cases := []struct{ op, args, path string }{
		{hooksList.ID, `{"offset":3,"limit":5,"type_name":"gateway-webhook","assigned":true}`, "/hooks"},
		{hooksGet.ID, `{"hook_id":` + id + `}`, "/hooks/" + id},
		{hooksPing.ID, `{"hook_id":` + id + `}`, "/hooks/" + id + "/ping"},
		{hooksLogs.ID, `{"hook_id":` + id + `,"offset":2,"limit":4,"from_ms":10,"to_ms":20}`, "/hooks/" + id + "/logs"},
	}
	for _, c := range cases {
		calls = nil
		if _, err := env.invoke(c.op, "open", c.args); err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		last := calls[len(calls)-1]
		if last.method != http.MethodGet || last.path != apiPath+c.path {
			t.Fatalf("%s last call = %+v", c.op, last)
		}
	}
	calls = nil
	_, _ = env.invoke(hooksList.ID, "open", `{"offset":3,"limit":5,"type_name":"gateway-webhook","assigned":true}`)
	q := calls[0].query
	if q.Get("teamId") != itoa64(ownTeam) || q.Get("pg[offset]") != "3" || q.Get("pg[limit]") != "5" ||
		q.Get("typeName") != "gateway-webhook" || q.Get("assigned") != "true" {
		t.Fatalf("list query = %v", q)
	}
	calls = nil
	_, _ = env.invoke(hooksLogs.ID, "open", `{"hook_id":`+id+`,"offset":2,"limit":4,"from_ms":10,"to_ms":20}`)
	q = calls[len(calls)-1].query
	if q.Get("pg[offset]") != "2" || q.Get("pg[limit]") != "4" || q.Get("from") != "10" || q.Get("to") != "20" {
		t.Fatalf("logs query = %v", q)
	}
}

func TestHooksNeverRevealTriggerSecretsExceptURLTool(t *testing.T) {
	var calls []call
	env := hookEnv(t, &calls, ownTeam, ownScenario)
	id := strconv.FormatInt(hookID, 10)
	for _, c := range []struct{ op, args string }{
		{hooksList.ID, `{}`}, {hooksGet.ID, `{"hook_id":` + id + `}`},
		{hooksPing.ID, `{"hook_id":` + id + `}`}, {hooksLogs.ID, `{"hook_id":` + id + `}`},
	} {
		result, err := env.invoke(c.op, "open", c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		for _, secret := range []string{canaryUDID, canaryHookURL, canaryMailAddr, "mailcanary5d2b8e", canaryHeader,
			"hook.example.com", "udids", "parser"} {
			if strings.Contains(result, secret) {
				t.Fatalf("%s leaked %q: %s", c.op, secret, result)
			}
		}
	}
	// The default connections do not offer hooks.url at all; only one naming it does.
	if _, err := env.invoke(hooksURL.ID, "open", `{"hook_id":`+id+`}`); err == nil {
		t.Fatal("hooks.url was offered without a tools list")
	}
	result, err := env.invoke(hooksURL.ID, "hookurl", `{"hook_id":`+id+`}`)
	if err != nil || !strings.Contains(result, canaryHookURL) {
		t.Fatalf("hooks.url = %s, %v", result, err)
	}
}

func TestHooksURLIsInNoProfile(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == hooksURL.ID {
				t.Fatalf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
	if !hooksURL.RequiresToolAllowList {
		t.Fatal("hooks.url must require a tools list")
	}
}

func TestHooksRebindForeignTeamWithoutLeak(t *testing.T) {
	var calls []call
	env := hookEnv(t, &calls, foreignTeam, ownScenario)
	id := strconv.FormatInt(hookID, 10)
	for _, c := range []struct{ op, conn string }{
		{hooksGet.ID, "open"}, {hooksPing.ID, "open"}, {hooksLogs.ID, "open"}, {hooksURL.ID, "hookurl"},
	} {
		calls = nil
		_, err := env.invoke(c.op, c.conn, `{"hook_id":`+id+`}`)
		if !isInvalidRequest(err) || len(calls) != 1 {
			t.Fatalf("%s err = %v, calls = %d, want refusal after the detail read only", c.op, err, len(calls))
		}
		if strings.Contains(err.Error(), strconv.FormatInt(foreignTeam, 10)) {
			t.Fatalf("%s error named the foreign team: %v", c.op, err)
		}
	}
	// The list drops the foreign-team hook.
	result, err := env.invoke(hooksList.ID, "open", `{}`)
	if err != nil || strings.Contains(result, "Foreign") {
		t.Fatalf("list = %s, %v", result, err)
	}
}

func TestHooksScenarioAllowListIsNarrower(t *testing.T) {
	var calls []call
	env := hookEnv(t, &calls, ownTeam, foreignScenario)
	id := strconv.FormatInt(hookID, 10)
	if _, err := env.invoke(hooksGet.ID, "scenario", `{"hook_id":`+id+`}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want a refusal for a hook of an unlisted scenario", err)
	}
	env = hookEnv(t, &calls, ownTeam, ownScenario)
	if _, err := env.invoke(hooksGet.ID, "scenario", `{"hook_id":`+id+`}`); err != nil {
		t.Fatalf("listed scenario: %v", err)
	}
}

func TestHooksValidateIDLocallyBeforeSecretAccess(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatal("no request should have been sent")
		return nil, nil
	})
	for _, args := range []string{`{"hook_id":0}`, `{"hook_id":"7"}`, `{"hook_id":-1}`, `{}`} {
		if _, err := env.invoke(hooksGet.ID, "open", args); err == nil {
			t.Fatalf("%s accepted", args)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("reads = %d, calls = %d, want none", *env.reads, len(calls))
	}
}

func TestHooksForbiddenNamesScope(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
	})
	for _, c := range []struct{ op, args string }{
		{hooksList.ID, `{}`}, {hooksGet.ID, `{"hook_id":1}`}, {hooksLogs.ID, `{"hook_id":1}`},
	} {
		_, err := env.invoke(c.op, "open", c.args)
		var perr *provider.Error
		if !errors.As(err, &perr) || perr.Class != provider.ClassPermission ||
			!strings.Contains(perr.Message, "hooks:read") || strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("%s err = %v, want the hooks:read hint", c.op, err)
		}
	}
}

func TestHooksBoundStringsAndSizes(t *testing.T) {
	var calls []call
	long := strings.Repeat("a", 5000)
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		sizes := `{"k0":1,"k1":2,"k2":3,"k3":4,"k4":5,"k5":6,"k6":7,"k7":8,"k8":9,"` + long + `":1}`
		if strings.HasSuffix(r.URL.Path, "/logs") {
			return jsonResponse(200, `{"hookLogs":[{"id":1,"statusId":1,"sizes":`+sizes+`}]}`), nil
		}
		return jsonResponse(200, `{"hook":`+hookJSONOf(hookID, ownTeam, ownScenario, long)+`}`), nil
	})
	result, err := env.invoke(hooksGet.ID, "open", `{"hook_id":77}`)
	if err != nil || len(result) > 3000 {
		t.Fatalf("get = %d bytes, %v", len(result), err)
	}
	result, err = env.invoke(hooksLogs.ID, "open", `{"hook_id":77}`)
	if err != nil || strings.Contains(result, long) || strings.Count(result, `"k`) > maxHookLogSizes {
		t.Fatalf("logs = %s, %v", result, err)
	}
}
