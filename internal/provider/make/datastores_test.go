package makeapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	storeID     = "7"
	structureID = "3"
)

func dataStoreJSONOf(id string, team int64, name string) string {
	return `{"id":` + id + `,"name":"` + name + `","teamId":` + itoa64(team) + `,"records":4,"size":"1024",` +
		`"maxSize":"10485760","datastructureId":3,"secretField":"` + canaryConnSecret + `"}`
}

func dataStructureJSONOf(id string, team int64) string {
	return `{"id":` + id + `,"name":"Shape","teamId":` + itoa64(team) + `,"strict":true,"spec":[` +
		`{"type":"text","name":"title","label":"Title","required":true,"default":"` + canaryConnSecret + `"},` +
		`{"type":"collection","name":"c","spec":[{"type":"array","name":"a","spec":{"type":"number"}}]}]}`
}

type storeEnv struct {
	*environment
	calls         []call
	bodies        []string
	storeTeam     int64
	structureTeam int64
	deleteRefused bool
	status        int
}

func newStoreEnv(t *testing.T) *storeEnv {
	h := &storeEnv{storeTeam: ownTeam, structureTeam: ownTeam}
	h.environment = newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		h.bodies = append(h.bodies, string(body))
		path := strings.TrimPrefix(r.URL.Path, apiPath)
		if r.Method != http.MethodGet && h.status != 0 {
			return jsonResponse(h.status, `{"message":"`+foreignCanary+`"}`), nil
		}
		switch {
		case r.Method == http.MethodGet && path == "/data-stores":
			return jsonResponse(200, `{"dataStores":[`+dataStoreJSONOf(storeID, ownTeam, "Own")+`,`+
				dataStoreJSONOf("8", foreignTeam, "Foreign")+`]}`), nil
		case r.Method == http.MethodGet && path == "/data-structures":
			return jsonResponse(200, `{"dataStructures":[`+dataStructureJSONOf(structureID, ownTeam)+`,`+
				dataStructureJSONOf("4", foreignTeam)+`]}`), nil
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/data-stores/"):
			return jsonResponse(200, `{"dataStore":`+dataStoreJSONOf(storeID, h.storeTeam, "Own")+`}`), nil
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/data-structures/"):
			return jsonResponse(200, `{"dataStructure":`+dataStructureJSONOf(structureID, h.structureTeam)+`}`), nil
		case r.Method == http.MethodPost && path == "/data-stores", r.Method == http.MethodPatch:
			return jsonResponse(200, `{"dataStore":`+dataStoreJSONOf(storeID, ownTeam, "New")+`}`), nil
		case r.Method == http.MethodDelete && path == "/data-stores":
			if h.deleteRefused && r.URL.Query().Get("confirmed") != "true" {
				return jsonResponse(400, `{"message":"`+foreignCanary+`","detail":{"scenarios":[{"id":10,"name":"Uses it"}]}}`), nil
			}
			return jsonResponse(200, `{"dataStores":[`+storeID+`]}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, path)
		return nil, nil
	})
	return h
}

func (h *storeEnv) changing() []call {
	var out []call
	for _, c := range h.calls {
		if c.method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func (h *storeEnv) reset() { h.calls, h.bodies = nil, nil }

func TestDataStoreReadsAreTeamBoundAndAllowListed(t *testing.T) {
	h := newStoreEnv(t)
	result, err := h.invoke(dataStoresList.ID, "open", `{}`)
	if err != nil || !strings.Contains(result, `"count":1`) || strings.Contains(result, "Foreign") ||
		strings.Contains(result, canaryConnSecret) {
		t.Fatalf("list = %s, %v", result, err)
	}
	last := h.calls[len(h.calls)-1]
	if last.query.Get("teamId") != itoa64(ownTeam) || last.query.Get("pg[limit]") != "50" ||
		len(last.query["cols[]"]) != 7 {
		t.Fatalf("list query = %+v", last.query)
	}
	result, err = h.invoke(dataStoresGet.ID, "open", `{"datastore_id":`+storeID+`}`)
	if err != nil || !strings.Contains(result, `"max_size":"10485760"`) || strings.Contains(result, canaryConnSecret) {
		t.Fatalf("get = %s, %v", result, err)
	}
	h.storeTeam = foreignTeam
	h.reset()
	_, err = h.invoke(dataStoresGet.ID, "open", `{"datastore_id":`+storeID+`}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), itoa64(foreignTeam)) || len(h.calls) != 1 {
		t.Fatalf("foreign get err = %v, calls = %+v", err, h.calls)
	}
}

func TestDataStructureReadsBoundTheSpec(t *testing.T) {
	h := newStoreEnv(t)
	result, err := h.invoke(dataStructuresList.ID, "open", `{"limit":2}`)
	if err != nil || !strings.Contains(result, `"count":1`) || strings.Contains(result, `"spec"`) ||
		strings.Contains(result, canaryConnSecret) {
		t.Fatalf("list = %s, %v", result, err)
	}
	result, err = h.invoke(dataStructuresGet.ID, "open", `{"datastructure_id":`+structureID+`}`)
	if err != nil || !strings.Contains(result, `"name":"title"`) || !strings.Contains(result, `"type":"number"`) ||
		strings.Contains(result, canaryConnSecret) {
		t.Fatalf("get = %s, %v", result, err)
	}
	h.structureTeam = foreignTeam
	_, err = h.invoke(dataStructuresGet.ID, "open", `{"datastructure_id":`+structureID+`}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), itoa64(foreignTeam)) {
		t.Fatalf("foreign get err = %v", err)
	}
	budget := &specBudget{left: 2}
	fields := parseSpec([]byte(`[{"type":"text","name":"a"},{"type":"text","name":"b"},{"type":"text","name":"c"}]`), 0, budget)
	if len(fields) != 2 || !budget.truncated {
		t.Fatalf("spec budget = %+v, %+v", fields, budget)
	}
	deep := []byte(`[{"type":"collection","name":"1","spec":[{"type":"collection","name":"2","spec":[` +
		`{"type":"collection","name":"3","spec":[{"type":"collection","name":"4","spec":[{"type":"text","name":"5"}]}]}]}]}]`)
	budget = &specBudget{left: 100}
	parseSpec(deep, 0, budget)
	if !budget.truncated {
		t.Fatal("a spec deeper than the limit was not marked truncated")
	}
}

func TestDataStoreChangesSendOneRequestAfterBinding(t *testing.T) {
	cases := []struct {
		op, conn, args, method, path, body string
	}{
		{dataStoresCreate.ID, "open", `{"name":"N","datastructure_id":` + structureID + `,"max_size_mb":5}`, "POST",
			"/data-stores", `{"datastructureId":3,"maxSizeMB":5,"name":"N","teamId":1001}`},
		{dataStoresUpdate.ID, "open", `{"datastore_id":` + storeID + `,"name":"N","max_size_mb":6}`, "PATCH",
			"/data-stores/" + storeID, `{"maxSizeMB":6,"name":"N"}`},
		{dataStoresUpdate.ID, "open", `{"datastore_id":` + storeID + `,"datastructure_id":` + structureID + `}`, "PATCH",
			"/data-stores/" + storeID, `{"datastructureId":3}`},
		{dataStoresDelete.ID, "datastoredelete", `{"datastore_id":` + storeID + `}`, "DELETE", "/data-stores",
			`{"ids":[7]}`},
	}
	for _, c := range cases {
		h := newStoreEnv(t)
		if _, err := h.invoke(c.op, c.conn, c.args); !isConfirmationRequired(err) || len(h.calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, calls = %d", c.op, err, len(h.calls))
		}
		if _, err := h.confirmed(c.op, c.conn, c.args); err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
		ch := h.changing()
		if len(ch) != 1 || ch[0].method != c.method || ch[0].path != apiPath+c.path {
			t.Fatalf("%s changing = %+v", c.op, ch)
		}
		if h.bodies[len(h.bodies)-1] != c.body {
			t.Fatalf("%s body = %q, want %q", c.op, h.bodies[len(h.bodies)-1], c.body)
		}
		if h.calls[0].method != http.MethodGet {
			t.Fatalf("%s did not read first: %+v", c.op, h.calls)
		}
		if c.method == "DELETE" && ch[0].query.Get("teamId") != itoa64(ownTeam) {
			t.Fatalf("delete query = %+v", ch[0].query)
		}
	}
}

func TestDataStoreChangesRefuseForeignStoreOrStructureBeforeAnyChange(t *testing.T) {
	for _, c := range []struct {
		op, conn, args   string
		store, structure bool
	}{
		{dataStoresUpdate.ID, "open", `{"datastore_id":` + storeID + `,"name":"N"}`, true, false},
		{dataStoresDelete.ID, "datastoredelete", `{"datastore_id":` + storeID + `}`, true, false},
		{dataStoresUpdate.ID, "open", `{"datastore_id":` + storeID + `,"datastructure_id":` + structureID + `}`, false, true},
		{dataStoresCreate.ID, "open", `{"name":"N","datastructure_id":` + structureID + `,"max_size_mb":5}`, false, true},
	} {
		h := newStoreEnv(t)
		if c.store {
			h.storeTeam = foreignTeam
		}
		if c.structure {
			h.structureTeam = foreignTeam
		}
		_, err := h.confirmed(c.op, c.conn, c.args)
		if !isInvalidRequest(err) || len(h.changing()) != 0 || strings.Contains(err.Error(), itoa64(foreignTeam)) {
			t.Fatalf("%s err = %v, calls = %+v", c.op, err, h.calls)
		}
	}
}

func TestDataStoreChangesRefuseBadInputAndScenarioListsBeforeAnyRead(t *testing.T) {
	for _, c := range []struct{ op, conn, args string }{
		{dataStoresCreate.ID, "open", `{"name":" ","datastructure_id":3,"max_size_mb":5}`},
		{dataStoresCreate.ID, "open", `{"name":"` + strings.Repeat("a", 129) + `","datastructure_id":3,"max_size_mb":5}`},
		{dataStoresCreate.ID, "open", `{"name":"N","datastructure_id":3,"max_size_mb":0}`},
		{dataStoresCreate.ID, "open", `{"name":"N","datastructure_id":3,"max_size_mb":99999999}`},
		{dataStoresCreate.ID, "open", `{"name":"N","datastructure_id":3,"max_size_mb":5,"team_id":9}`},
		{dataStoresUpdate.ID, "open", `{"datastore_id":7}`},
		{dataStoresUpdate.ID, "open", `{"datastore_id":0,"name":"N"}`},
		{dataStoresDelete.ID, "datastoredelete", `{"datastore_id":7,"ids":[1,2]}`},
		{dataStoresDelete.ID, "datastoredelete", `{"datastore_id":7,"all":true}`},
		{dataStoresCreate.ID, "scenario", `{"name":"N","datastructure_id":3,"max_size_mb":5}`},
		{dataStoresList.ID, "scenario", `{}`},
		{dataStructuresGet.ID, "scenario", `{"datastructure_id":3}`},
	} {
		h := newStoreEnv(t)
		_, err := h.confirmed(c.op, c.conn, c.args)
		if err == nil || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s %s err = %v, calls = %d, reads = %d", c.op, c.args, err, len(h.calls), *h.reads)
		}
	}
}

func TestDataStoresDeleteConfirmationAndScenarioConflict(t *testing.T) {
	h := newStoreEnv(t)
	h.deleteRefused = true
	result, err := h.confirmed(dataStoresDelete.ID, "datastoredelete", `{"datastore_id":`+storeID+`}`)
	if err != nil || !strings.Contains(result, `"confirmation_required":true`) || strings.Contains(result, `"deleted":true`) ||
		!strings.Contains(result, `"id":10`) || strings.Contains(result, foreignCanary) {
		t.Fatalf("conflict = %s, %v", result, err)
	}
	if ch := h.changing(); len(ch) != 1 || ch[0].query.Get("confirmed") != "" {
		t.Fatalf("changing = %+v, want one DELETE without confirmed", ch)
	}
	h.reset()
	result, err = h.confirmed(dataStoresDelete.ID, "datastoredelete",
		`{"datastore_id":`+storeID+`,"confirm_scenarios_affected":true}`)
	ch := h.changing()
	if err != nil || !strings.Contains(result, `"deleted":true`) || len(ch) != 1 || ch[0].query.Get("confirmed") != "true" {
		t.Fatalf("confirmed = %s, %v, %+v", result, err, ch)
	}
	if _, err := h.confirmed(dataStoresDelete.ID, "open", `{"datastore_id":`+storeID+`}`); err == nil {
		t.Fatal("datastores.delete was offered without a tools list")
	}
	if !dataStoresDelete.RequiresToolAllowList {
		t.Fatal("datastores.delete must require a tools list")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == dataStoresDelete.ID {
				t.Fatalf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}

func TestDataStoreChangesAreNeverRetriedAndNameTheScope(t *testing.T) {
	for _, status := range []int{500, 403} {
		for _, c := range []struct{ op, conn, args, scope string }{
			{dataStoresCreate.ID, "open", `{"name":"N","datastructure_id":3,"max_size_mb":5}`, "datastores:write"},
			{dataStoresUpdate.ID, "open", `{"datastore_id":7,"name":"N"}`, "datastores:write"},
			{dataStoresDelete.ID, "datastoredelete", `{"datastore_id":7}`, "datastores:write"},
		} {
			h := newStoreEnv(t)
			h.status = status
			_, err := h.confirmed(c.op, c.conn, c.args)
			if err == nil || len(h.changing()) != 1 || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("%s/%d err = %v, changing = %d", c.op, status, err, len(h.changing()))
			}
			if status == 500 && !strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("%s lacks the uncertain note: %v", c.op, err)
			}
			var perr *provider.Error
			if status == 403 && (!errors.As(err, &perr) || perr.Class != provider.ClassPermission ||
				!strings.Contains(perr.Message, c.scope)) {
				t.Fatalf("%s err = %v, want the %s hint", c.op, err, c.scope)
			}
		}
	}
	// An aborted request and an unreadable answer are uncertain as well, never repeated.
	for name, failure := range map[string]func() (*http.Response, error){
		"aborted": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbled": func() (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	} {
		var calls []call
		mutations := 0
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return jsonResponse(200, `{"dataStore":`+dataStoreJSONOf(storeID, ownTeam, "S")+`}`), nil
			}
			mutations++
			return failure()
		})
		_, err := env.confirmed(dataStoresUpdate.ID, "open", `{"datastore_id":7,"name":"N"}`)
		if err == nil || mutations != 1 || !strings.Contains(err.Error(), "may have taken effect") {
			t.Fatalf("%s err = %v, mutations = %d", name, err, mutations)
		}
	}
}

func TestDataStoreReadsNameBothScopesOn403(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(403, `{}`), nil })
	_, err := env.invoke(dataStoresGet.ID, "open", `{"datastore_id":7}`)
	var perr *provider.Error
	if !errors.As(err, &perr) || !strings.Contains(perr.Message, "datastores:read") ||
		!strings.Contains(perr.Message, "organizations:read") {
		t.Fatalf("err = %v", err)
	}
	_, err = env.invoke(dataStructuresGet.ID, "open", `{"datastructure_id":7}`)
	if !errors.As(err, &perr) || !strings.Contains(perr.Message, "udts:read") {
		t.Fatalf("err = %v", err)
	}
}

func TestDataStoreAnswersAreCappedAndMismatchesReported(t *testing.T) {
	long := strings.Repeat("x", 1000)
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodGet:
			return jsonResponse(200, `{"dataStore":{"id":7,"name":"`+long+`","teamId":1001,"size":12,"maxSize":"`+long+`"}}`), nil
		case http.MethodDelete:
			return jsonResponse(200, `{"dataStores":[9]}`), nil
		}
		return jsonResponse(200, `{"dataStore":{"id":7,"teamId":2002}}`), nil
	})
	result, err := env.invoke(dataStoresGet.ID, "open", `{"datastore_id":7}`)
	if err != nil || len(result) > 1000 || !strings.Contains(result, `"size":"12"`) {
		t.Fatalf("get = %d bytes, %v", len(result), err)
	}
	_, err = env.confirmed(dataStoresUpdate.ID, "open", `{"datastore_id":7,"name":"N"}`)
	if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "already took effect") {
		t.Fatalf("outside-team result err = %v", err)
	}
	_, err = env.confirmed(dataStoresDelete.ID, "datastoredelete", `{"datastore_id":7}`)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("delete of another id err = %v", err)
	}
}

func TestDataStoreChangeToolsDeclareACompleteRisk(t *testing.T) {
	for _, d := range []capability.Descriptor{dataStoresCreate, dataStoresUpdate, dataStoresDelete} {
		r := d.Risk
		if r.Effect == "" || r.Idempotency == "" || r.Confirmation != capability.ConfirmationRequired ||
			!r.OpenWorld || r.DataSensitivity == "" {
			t.Fatalf("%s has an incomplete risk: %+v", d.ID, r)
		}
	}
}
