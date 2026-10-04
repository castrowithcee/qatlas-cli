package makeapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

type queueEnv struct {
	*environment
	calls  []call
	bodies []string
}

type queueAnswer func(r *http.Request) (*http.Response, error)

func newQueueEnv(t *testing.T, teamID, scenarioID int64, answer queueAnswer) *queueEnv {
	q := &queueEnv{}
	q.environment = newEnvironment(t, &q.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		q.bodies = append(q.bodies, string(body))
		if r.URL.Path == apiPath+"/hooks/"+strconv.FormatInt(hookID, 10) {
			return jsonResponse(200, `{"hook":`+hookJSONOf(hookID, teamID, scenarioID, "Hook")+`}`), nil
		}
		return answer(r)
	})
	return q
}

func (q *queueEnv) queueCalls() []call {
	var out []call
	for _, c := range q.calls {
		if strings.Contains(c.path, "/incomings") {
			out = append(out, c)
		}
	}
	return out
}

func okQueue(r *http.Request) (*http.Response, error) {
	switch {
	case r.Method == http.MethodDelete:
		return jsonResponse(200, `{"incomings":["a1","b2","zz"]}`), nil
	case strings.HasSuffix(r.URL.Path, "/stats"):
		return jsonResponse(200, `{"incomingStat":{"queue":3,"limit":10000,"enabled":true}}`), nil
	case strings.HasSuffix(r.URL.Path, "/incomings"):
		return jsonResponse(200, `{"incomings":[{"id":"a1","scope":"s","size":12,"created":"2026-01-01T00:00:00Z",`+
			`"data":{"field":"`+canaryHeader+`"}}]}`), nil
	}
	return jsonResponse(200, `{"incoming":{"id":"a1","scope":"s","size":12,"created":"t","data":{"name":"x",`+
		`"url":"`+canaryHookURL+`","headers":{"Host":"hook.example.com","Authorization":"Bearer tok"}}}}`), nil
}

func TestHookQueueRequestsUseDocumentedShapes(t *testing.T) {
	id := strconv.FormatInt(hookID, 10)
	base := apiPath + "/hooks/" + id + "/incomings"
	cases := []struct {
		op, conn, args, method, path, body string
		confirm                            bool
	}{
		{hookQueueList.ID, "open", `{"hook_id":` + id + `,"offset":3,"limit":5}`, "GET", base, "", false},
		{hookQueueGet.ID, "open", `{"hook_id":` + id + `,"incoming_id":"a1"}`, "GET", base + "/a1", "", false},
		{hookQueueStats.ID, "open", `{"hook_id":` + id + `}`, "GET", base + "/stats", "", false},
		{hookQueueDelete.ID, "hookqueuedelete", `{"hook_id":` + id + `,"ids":["a1","b2"]}`, "DELETE", base,
			`{"ids":["a1","b2"]}`, true},
	}
	for _, c := range cases {
		q := newQueueEnv(t, ownTeam, ownScenario, okQueue)
		var err error
		if c.confirm {
			_, err = q.confirmed(c.op, c.conn, c.args)
		} else {
			_, err = q.invoke(c.op, c.conn, c.args)
		}
		if err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		qc := q.queueCalls()
		if len(qc) != 1 || qc[0].method != c.method || qc[0].path != c.path {
			t.Fatalf("%s queue calls = %+v", c.op, qc)
		}
		if c.op == hookQueueList.ID && (qc[0].query.Get("pg[offset]") != "3" || qc[0].query.Get("pg[limit]") != "5") {
			t.Fatalf("list query = %v", qc[0].query)
		}
		if qc[0].query.Get("confirmed") != "" {
			t.Fatalf("%s sent confirmed", c.op)
		}
		if c.body != "" && q.bodies[len(q.bodies)-1] != c.body {
			t.Fatalf("%s body = %q", c.op, q.bodies[len(q.bodies)-1])
		}
	}
}

func TestHookQueueDeleteSendsOnlyIDsAndReportsRequestedOnes(t *testing.T) {
	q := newQueueEnv(t, ownTeam, ownScenario, okQueue)
	result, err := q.confirmed(hookQueueDelete.ID, "hookqueuedelete", `{"hook_id":77,"ids":["a1","b2"]}`)
	if err != nil {
		t.Fatal(err)
	}
	body := q.bodies[len(q.bodies)-1]
	for _, bad := range []string{"all", "exceptIds"} {
		if strings.Contains(body, bad) {
			t.Fatalf("body %s holds %s", body, bad)
		}
	}
	if !strings.Contains(result, `"deleted_count":2`) || strings.Contains(result, "zz") {
		t.Fatalf("result = %s", result)
	}
}

func TestHookQueueDeleteRefusesBadIDListsLocally(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatal("no request should have been sent")
		return nil, nil
	})
	many := make([]string, maxQueueDeleteIDs+1)
	for i := range many {
		many[i] = "id" + strconv.Itoa(i)
	}
	long, _ := json.Marshal(many)
	for _, ids := range []string{`[]`, `["a","a"]`, string(long), `["a b"]`, `["../x"]`, `"all"`} {
		_, err := env.confirmed(hookQueueDelete.ID, "hookqueuedelete", `{"hook_id":77,"ids":`+ids+`}`)
		if err == nil {
			t.Fatalf("ids %.40s accepted", ids)
		}
	}
	for _, args := range []string{`{"hook_id":77,"ids":["a"],"all":true}`,
		`{"hook_id":77,"ids":["a"],"exceptIds":["b"]}`, `{"hook_id":77}`} {
		if _, err := env.confirmed(hookQueueDelete.ID, "hookqueuedelete", args); err == nil {
			t.Fatalf("%s accepted", args)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("reads = %d, calls = %d", *env.reads, len(calls))
	}
}

func TestHookQueueDeleteNeedsConfirmationAndAllowList(t *testing.T) {
	q := newQueueEnv(t, ownTeam, ownScenario, okQueue)
	_, err := q.invoke(hookQueueDelete.ID, "hookqueuedelete", `{"hook_id":77,"ids":["a"]}`)
	if !isConfirmationRequired(err) || len(q.calls) != 0 {
		t.Fatalf("err = %v, calls = %d", err, len(q.calls))
	}
	_, err = q.confirmed(hookQueueDelete.ID, "open", `{"hook_id":77,"ids":["a"]}`)
	if err == nil || len(q.calls) != 0 {
		t.Fatalf("delete offered without a tools list: %v", err)
	}
	if !hookQueueDelete.RequiresToolAllowList {
		t.Fatal("delete must require a tools list")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == hookQueueDelete.ID {
				t.Fatalf("profile %s contains %s", profile.ID, tool)
			}
		}
		if profile.ID == "hookqueue-read" && len(profile.Tools) != 3 {
			t.Fatalf("hookqueue-read = %v", profile.Tools)
		}
	}
}

func TestHookQueueRefusesForeignHookBeforeAnyQueueRequest(t *testing.T) {
	for _, c := range []struct {
		teamID, scenarioID int64
		conn               string
	}{{foreignTeam, ownScenario, "open"}, {ownTeam, foreignScenario, "scenario"}} {
		for _, op := range []struct{ id, conn, args string }{
			{hookQueueList.ID, c.conn, `{"hook_id":77}`},
			{hookQueueGet.ID, c.conn, `{"hook_id":77,"incoming_id":"a1"}`},
			{hookQueueStats.ID, c.conn, `{"hook_id":77}`},
			{hookQueueDelete.ID, "hookqueuedelete", `{"hook_id":77,"ids":["a1"]}`},
		} {
			q := newQueueEnv(t, c.teamID, c.scenarioID, okQueue)
			if op.id == hookQueueDelete.ID && c.conn == "scenario" {
				continue
			}
			_, err := q.confirmed(op.id, op.conn, op.args)
			if !isInvalidRequest(err) || len(q.queueCalls()) != 0 || len(q.calls) != 1 {
				t.Fatalf("%s err = %v, calls = %+v", op.id, err, q.calls)
			}
			if strings.Contains(err.Error(), strconv.FormatInt(foreignTeam, 10)) {
				t.Fatalf("%s named the foreign team", op.id)
			}
		}
	}
}

func TestHookQueueOutputsNeverRevealTriggerSecretsOrListPayloads(t *testing.T) {
	q := newQueueEnv(t, ownTeam, ownScenario, okQueue)
	for _, c := range []struct{ op, args string }{
		{hookQueueList.ID, `{"hook_id":77}`}, {hookQueueGet.ID, `{"hook_id":77,"incoming_id":"a1"}`},
		{hookQueueStats.ID, `{"hook_id":77}`},
	} {
		result, err := q.invoke(c.op, "open", c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		for _, s := range []string{canaryUDID, canaryHookURL, "hook.example.com", "Bearer tok"} {
			if strings.Contains(result, s) {
				t.Fatalf("%s leaked %q: %s", c.op, s, result)
			}
		}
		if c.op == hookQueueList.ID && (strings.Contains(result, canaryHeader) || strings.Contains(result, "data")) {
			t.Fatalf("list returned a payload: %s", result)
		}
	}
}

func TestHookQueueGetCapsPayload(t *testing.T) {
	long := strings.Repeat("x", 1000)
	deep := `"leaf"`
	for i := 0; i < 12; i++ {
		deep = `{"n":` + deep + `}`
	}
	var many []string
	for i := 0; i < 400; i++ {
		many = append(many, strconv.Itoa(i))
	}
	for name, data := range map[string]string{
		"string": `{"a":"` + long + `"}`, "deep": deep, "fields": `{"list":[` + strings.Join(many, ",") + `]}`,
	} {
		q := newQueueEnv(t, ownTeam, ownScenario, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"incoming":{"id":"a1","data":`+data+`}}`), nil
		})
		result, err := q.invoke(hookQueueGet.ID, "open", `{"hook_id":77,"incoming_id":"a1"}`)
		if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > maxPayloadBytes+512 ||
			strings.Contains(result, long) {
			t.Fatalf("%s: %d bytes, %v", name, len(result), err)
		}
	}
	q := newQueueEnv(t, ownTeam, ownScenario, okQueue)
	result, _ := q.invoke(hookQueueGet.ID, "open", `{"hook_id":77,"incoming_id":"a1"}`)
	if !strings.Contains(result, `"truncated":false`) {
		t.Fatalf("small payload marked truncated: %s", result)
	}
}

func TestHookQueueValidatesIDsLocally(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatal("no request should have been sent")
		return nil, nil
	})
	for _, id := range []string{`""`, `"a/b"`, `"a?b"`, `".."`, `1`, `"` + strings.Repeat("a", 65) + `"`} {
		if _, err := env.invoke(hookQueueGet.ID, "open", `{"hook_id":77,"incoming_id":`+id+`}`); err == nil {
			t.Fatalf("incoming_id %.20s accepted", id)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("reads = %d, calls = %d", *env.reads, len(calls))
	}
}

func TestHookQueueDeleteIsNeverRetriedAndForbiddenNamesScope(t *testing.T) {
	for name, answer := range map[string]func() (*http.Response, error){
		"500": func() (*http.Response, error) {
			return jsonResponse(500, `{"message":"`+foreignCanary+`"}`), nil
		},
		"abort":      func() (*http.Response, error) { return nil, errUnclearTransport{} },
		"unreadable": func() (*http.Response, error) { return jsonResponse(200, `{not json`), nil },
		"error body": func() (*http.Response, error) {
			return jsonResponse(200, `{"error":{"message":"`+foreignCanary+`"}}`), nil
		},
	} {
		mutations := 0
		q := newQueueEnv(t, ownTeam, ownScenario, func(r *http.Request) (*http.Response, error) {
			mutations++
			return answer()
		})
		_, err := q.confirmed(hookQueueDelete.ID, "hookqueuedelete", `{"hook_id":77,"ids":["a1"]}`)
		if err == nil || mutations != 1 || !strings.Contains(err.Error(), "may have been deleted") ||
			strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("%s err = %v, mutations = %d", name, err, mutations)
		}
	}
	q := newQueueEnv(t, ownTeam, ownScenario, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
	})
	_, err := q.confirmed(hookQueueDelete.ID, "hookqueuedelete", `{"hook_id":77,"ids":["a1"]}`)
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.ClassPermission ||
		!strings.Contains(perr.Message, "hooks:write") {
		t.Fatalf("err = %v, want the hooks:write hint", err)
	}
	for _, args := range []struct{ op, a string }{{hookQueueList.ID, `{"hook_id":77}`},
		{hookQueueStats.ID, `{"hook_id":77}`}, {hookQueueGet.ID, `{"hook_id":77,"incoming_id":"a1"}`}} {
		_, err := q.invoke(args.op, "open", args.a)
		if !errors.As(err, &perr) || perr.Class != provider.ClassPermission ||
			!strings.Contains(perr.Message, "hooks:read") {
			t.Fatalf("%s err = %v, want the hooks:read hint", args.op, err)
		}
	}
}
