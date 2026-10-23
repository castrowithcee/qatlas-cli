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
			"hook.example.com", "udids"} {
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

const hookDocExample = `{"id":58,"name":"Test 1","teamId":4,"udid":"` + canaryUDID + `","type":"web","packageName":null,` +
	`"theme":"#c73a63","flags":{"form":true},"editable":false,"queueCount":0,"queueLimit":10000,"enabled":true,` +
	`"gone":false,"typeName":"gateway-webhook","data":{"ip":"2001:db8::1","udt":12,"headers":true,"method":false,` +
	`"stringify":false,"teamId":4,"secretKey":"` + canaryHeader + `"},"scenarioId":112,"url":"` + canaryHookURL + `"}`

func TestHooksListAndGetReportSettings(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		doc := strings.Replace(strings.Replace(hookDocExample, `"teamId":4,"udid"`, `"teamId":`+itoa64(ownTeam)+`,"udid"`, 1),
			`"scenarioId":112`, `"scenarioId":`+itoa64(ownScenario), 1)
		if r.URL.Path == apiPath+"/hooks" {
			return jsonResponse(200, `{"hooks":[`+doc+`]}`), nil
		}
		return jsonResponse(200, `{"hook":`+doc+`}`), nil
	})
	want := []string{`"theme":"#c73a63"`, `"flags":{"form":true}`, `"editable":false`,
		`"data":{"headers":true,"ip":"2001:db8::1","method":false,"stringify":false,"team_id":4,"udt":12}`}
	for _, c := range []struct{ op, args string }{{hooksList.ID, `{}`}, {hooksGet.ID, `{"hook_id":58}`}} {
		out, err := env.invoke(c.op, "open", c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		compact := strings.NewReplacer(" ", "").Replace(out)
		for _, w := range want {
			if !strings.Contains(compact, w) {
				t.Errorf("%s lacks %s: %s", c.op, w, out)
			}
		}
		for _, bad := range []string{"secretKey", canaryHeader, canaryUDID, canaryHookURL} {
			if strings.Contains(out, bad) {
				t.Errorf("%s leaked %q: %s", c.op, bad, out)
			}
		}
		assertOutputDeclared(t, c.op, []byte(out))
	}
}

func TestHooksWithoutSettingsKeepTheirOutput(t *testing.T) {
	var calls []call
	env := hookEnv(t, &calls, ownTeam, ownScenario)
	out, err := env.invoke(hooksGet.ID, "open", `{"hook_id":77}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"theme"`, `"flags"`, `"editable"`, `"data"`} {
		if strings.Contains(out, key) {
			t.Fatalf("output holds %s without Make reporting it: %s", key, out)
		}
	}
}

func TestHooksMaskTriggerPartsInNewTexts(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ping"):
			return jsonResponse(200, fmt.Sprintf(`{"address":%q,"teamId":%d,"attached":true,"dataStructure":[`+
				`{"name":"id-%s","type":"text"},{"name":"b","type":"%s\u0007"},{}]}`, canaryHookURL, ownTeam,
				canaryUDID, canaryHookURL)), nil
		case strings.HasSuffix(r.URL.Path, "/logs"):
			return jsonResponse(200, `{"hookLogs":[{"id":5,"statusId":1,"imtId":"imt-`+canaryUDID+`","parser":"p `+
				canaryHookURL+`","appParser":"app\u0007x","udids":["`+canaryMailAddr+`"]}]}`), nil
		}
		doc := strings.Replace(hookDocExample, `"teamId":4,"udid"`, `"teamId":`+itoa64(ownTeam)+`,"udid"`, 1)
		doc = strings.Replace(doc, `"scenarioId":112`, `"scenarioId":`+itoa64(ownScenario), 1)
		doc = strings.Replace(doc, `"#c73a63"`, `"x `+canaryUDID+`"`, 1)
		doc = strings.Replace(doc, `"2001:db8::1"`, `"`+canaryHookURL+`"`, 1)
		return jsonResponse(200, `{"hook":`+doc+`}`), nil
	})
	for _, c := range []struct{ op, args string }{{hooksGet.ID, `{"hook_id":58}`},
		{hooksPing.ID, `{"hook_id":58}`}, {hooksLogs.ID, `{"hook_id":58}`}} {
		out, err := env.invoke(c.op, "open", c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		for _, bad := range []string{canaryUDID, canaryHookURL, canaryMailAddr, "\\u0007"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s leaked %q: %s", c.op, bad, out)
			}
		}
		if !strings.Contains(out, redactedMarker) {
			t.Errorf("%s masked nothing: %s", c.op, out)
		}
		assertOutputDeclared(t, c.op, []byte(out))
	}
}

func TestHooksPingReportsDataStructureAndLogsReportParsers(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ping"):
			return jsonResponse(200, fmt.Sprintf(`{"address":%q,"teamId":%d,"attached":false,"learning":false,`+
				`"gone":false,"dataStructure":[{"name":"email","type":"text"},{"name":"n","type":"number"}]}`,
				canaryHookURL, ownTeam)), nil
		case strings.HasSuffix(r.URL.Path, "/logs"):
			return jsonResponse(200, `{"hookLogs":[{"id":5,"statusId":1,"imtId":"abc","parser":"json","appParser":"app"}]}`), nil
		}
		return jsonResponse(200, `{"hook":`+hookJSONOf(hookID, ownTeam, ownScenario, "Hook")+`}`), nil
	})
	out, err := env.invoke(hooksPing.ID, "open", `{"hook_id":77}`)
	if err != nil || !strings.Contains(out, `"data_structure":[{"name":"email","type":"text"},{"name":"n","type":"number"}]`) {
		t.Fatalf("ping = %s, %v", out, err)
	}
	out, err = env.invoke(hooksLogs.ID, "open", `{"hook_id":77}`)
	if err != nil || !strings.Contains(out, `"imt_id":"abc"`) || !strings.Contains(out, `"parser":"json"`) ||
		!strings.Contains(out, `"app_parser":"app"`) {
		t.Fatalf("logs = %s, %v", out, err)
	}
}

func TestHooksPingBoundsDataStructure(t *testing.T) {
	var calls []call
	var fields []string
	for i := 0; i < maxDataStructureEntries+5; i++ {
		fields = append(fields, `{"name":"f`+strconv.Itoa(i)+`","type":"text"}`)
	}
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/ping") {
			return jsonResponse(200, fmt.Sprintf(`{"teamId":%d,"dataStructure":[%s]}`, ownTeam, strings.Join(fields, ","))), nil
		}
		return jsonResponse(200, `{"hook":`+hookJSONOf(hookID, ownTeam, ownScenario, "Hook")+`}`), nil
	})
	out, err := env.invoke(hooksPing.ID, "open", `{"hook_id":77}`)
	if err != nil || strings.Count(out, `"type":"text"`) != maxDataStructureEntries ||
		!strings.Contains(out, `"data_structure_truncated":true`) {
		t.Fatalf("ping = %d bytes, %v", len(out), err)
	}
}

func TestHooksListNewFiltersReachTheRequest(t *testing.T) {
	var calls []call
	env := hookEnv(t, &calls, ownTeam, ownScenario)
	if _, err := env.invoke(hooksList.ID, "open", `{"view_for_scenario_id":`+itoa64(ownScenario)+
		`,"sort_by":"hookName","sort_dir":"desc"}`); err != nil {
		t.Fatal(err)
	}
	q := calls[0].query
	if q.Get("viewForScenarioId") != itoa64(ownScenario) || q.Get("pg[sortBy]") != "hookName" || q.Get("pg[sortDir]") != "desc" {
		t.Fatalf("list query = %v", q)
	}
	calls = nil
	if _, err := env.invoke(hooksList.ID, "open", `{}`); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"viewForScenarioId", "pg[sortBy]", "pg[sortDir]"} {
		if _, ok := calls[0].query[key]; ok {
			t.Fatalf("%s sent without a filter: %v", key, calls[0].query)
		}
	}
	calls = nil
	if _, err := env.invoke(hooksLogs.ID, "open", `{"hook_id":77,"sort_dir":"asc"}`); err != nil {
		t.Fatal(err)
	}
	if got := calls[len(calls)-1].query.Get("pg[sortDir]"); got != "asc" {
		t.Fatalf("logs sort = %q", got)
	}
}

func TestHooksListRefusesBadFiltersBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := hookEnv(t, &calls, ownTeam, ownScenario)
	for _, args := range []string{`{"view_for_scenario_id":0}`, `{"view_for_scenario_id":-1}`,
		`{"view_for_scenario_id":"7"}`, `{"sort_by":"id"}`, `{"sort_dir":"up"}`} {
		if _, err := env.invoke(hooksList.ID, "open", args); err == nil {
			t.Fatalf("%s accepted", args)
		}
	}
	if _, err := env.invoke(hooksLogs.ID, "open", `{"hook_id":77,"sort_dir":"up"}`); err == nil {
		t.Fatal("logs accepted a bad sort_dir")
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none", calls)
	}
	// The scenario allow-list admits only its own scenario as a view filter.
	if _, err := env.invoke(hooksList.ID, "scenario", `{"view_for_scenario_id":`+itoa64(foreignScenario)+`}`); !isInvalidRequest(err) {
		t.Fatalf("err = %v, want a refusal for an unlisted scenario", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want none", calls)
	}
	if _, err := env.invoke(hooksList.ID, "scenario", `{"view_for_scenario_id":`+itoa64(ownScenario)+`}`); err != nil {
		t.Fatalf("listed scenario: %v", err)
	}
}
