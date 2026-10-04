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

type recordsEnv struct {
	*environment
	calls     []call
	bodies    []string
	storeTeam int64
	status    int
	records   string
	deleted   string
}

func newRecordsEnv(t *testing.T) *recordsEnv {
	h := &recordsEnv{storeTeam: ownTeam, deleted: `{"keys":["a","b"]}`}
	h.environment = newEnvironment(t, &h.calls, func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		h.bodies = append(h.bodies, string(body))
		path := strings.TrimPrefix(r.URL.Path, apiPath)
		switch {
		case r.Method == http.MethodGet && path == "/data-stores/"+storeID:
			return jsonResponse(200, `{"dataStore":`+dataStoreJSONOf(storeID, h.storeTeam, "Own")+`}`), nil
		case path != "/data-stores/"+storeID+"/data":
			t.Fatalf("unexpected request %s %s", r.Method, path)
		case r.Method == http.MethodGet:
			if h.records != "" {
				return jsonResponse(200, h.records), nil
			}
			return jsonResponse(200, `{"records":[{"key":"a","data":{"title":"x"}},{"key":"b","data":null},`+
				`{"key":"c","data":{"n":1}}],"spec":[{"secret":"`+canaryConnSecret+`"}],"strict":true,"count":3}`), nil
		case h.status != 0:
			return jsonResponse(h.status, `{"message":"`+foreignCanary+`"}`), nil
		case r.Method == http.MethodPost:
			return jsonResponse(200, `{"key":"k1","data":{"title":"x"}}`), nil
		case r.Method == http.MethodDelete:
			return jsonResponse(200, h.deleted), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, path)
		return nil, nil
	})
	return h
}

func (h *recordsEnv) changing() int {
	n := 0
	for _, c := range h.calls {
		if c.method != http.MethodGet {
			n++
		}
	}
	return n
}

func TestRecordsListBindsTheStoreAndCapsTheAnswer(t *testing.T) {
	h := newRecordsEnv(t)
	result, err := h.invoke(dataStoreRecordsList.ID, "open", `{"datastore_id":`+storeID+`,"limit":2}`)
	if err != nil || !strings.Contains(result, `"count":2`) || strings.Contains(result, `"key":"c"`) ||
		!strings.Contains(result, `"has_more":true`) || strings.Contains(result, canaryConnSecret) {
		t.Fatalf("list = %s, %v", result, err)
	}
	if len(h.calls) != 2 || h.calls[0].path != apiPath+"/data-stores/"+storeID ||
		h.calls[1].query.Get("pg[limit]") != "2" || h.calls[1].query.Get("pg[offset]") != "0" {
		t.Fatalf("calls = %+v", h.calls)
	}
	h.records = `{"records":[{"key":"` + strings.Repeat("k", 400) + `","data":{"big":"` + strings.Repeat("x", 20000) + `"}}]}`
	result, err = h.invoke(dataStoreRecordsList.ID, "open", `{"datastore_id":`+storeID+`}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > 2000 {
		t.Fatalf("capped list = %d bytes, %v", len(result), err)
	}
	for _, args := range []string{`{"datastore_id":7,"limit":51}`, `{"datastore_id":7,"offset":-1}`, `{"datastore_id":0}`} {
		h2 := newRecordsEnv(t)
		if _, err := h2.invoke(dataStoreRecordsList.ID, "open", args); err == nil || len(h2.calls) != 0 {
			t.Fatalf("%s err = %v, calls = %d", args, err, len(h2.calls))
		}
	}
}

func TestRecordsForeignStoreIsRefusedBeforeListOrChange(t *testing.T) {
	for _, c := range []struct{ op, conn, args string }{
		{dataStoreRecordsList.ID, "open", `{"datastore_id":7}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"data":{"a":1}}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":["a"]}`},
	} {
		h := newRecordsEnv(t)
		h.storeTeam = foreignTeam
		_, err := h.confirmed(c.op, c.conn, c.args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), itoa64(foreignTeam)) || len(h.calls) != 1 ||
			h.changing() != 0 {
			t.Fatalf("%s err = %v, calls = %+v", c.op, err, h.calls)
		}
	}
}

func TestRecordsChangesSendOneRequestAfterBinding(t *testing.T) {
	cases := []struct{ op, conn, args, method, body string }{
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"key":"k1","data":{"title":"x"}}`, "POST",
			`{"data":{"title":"x"},"key":"k1"}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"data":{"title":"x"}}`, "POST", `{"data":{"title":"x"}}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":["a","b"]}`, "DELETE", `{"keys":["a","b"]}`},
	}
	for _, c := range cases {
		h := newRecordsEnv(t)
		if _, err := h.invoke(c.op, c.conn, c.args); !isConfirmationRequired(err) || len(h.calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, calls = %d", c.op, err, len(h.calls))
		}
		result, err := h.confirmed(c.op, c.conn, c.args)
		if err != nil || h.changing() != 1 || h.calls[0].method != http.MethodGet {
			t.Fatalf("%s: %v, calls = %+v", c.op, err, h.calls)
		}
		last := h.calls[len(h.calls)-1]
		if last.method != c.method || last.path != apiPath+"/data-stores/"+storeID+"/data" ||
			last.query.Get("confirmed") != "" || h.bodies[len(h.bodies)-1] != c.body {
			t.Fatalf("%s sent %+v %q, want %q", c.op, last, h.bodies[len(h.bodies)-1], c.body)
		}
		if c.method == "DELETE" && !strings.Contains(result, `"deleted_count":2`) {
			t.Fatalf("delete = %s", result)
		}
	}
}

func TestRecordsInputIsBoundedBeforeAnyRequest(t *testing.T) {
	big := `{"x":"` + strings.Repeat("a", maxRecordDataBytes) + `"}`
	deep := strings.Repeat(`{"a":`, maxRecordDataDepth+1) + `1` + strings.Repeat(`}`, maxRecordDataDepth+1)
	keys := make([]string, maxRecordDeleteKeys+1)
	for i := range keys {
		keys[i] = `"k` + strings.Repeat("x", i) + `"`
	}
	for _, c := range []struct{ op, conn, args string }{
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"data":` + big + `}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"data":` + deep + `}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"data":[1]}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"key":"../x","data":{}}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"key":".hidden","data":{}}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"key":"` + strings.Repeat("k", 129) + `","data":{}}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"key":"a b","data":{}}`},
		{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"data":{},"url":"https://x"}`},
		{dataStoreRecordsCreate.ID, "scenario", `{"datastore_id":7,"data":{}}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":[]}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":["a","a"]}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":["a/b"]}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":[` + strings.Join(keys, ",") + `]}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"all":true}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"all":true,"keys":["a"]}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":["a"],"exceptKeys":["b"]}`},
		{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":["a"],"confirmed":true}`},
	} {
		h := newRecordsEnv(t)
		_, err := h.confirmed(c.op, c.conn, c.args)
		if err == nil || len(h.calls) != 0 || *h.reads != 0 {
			t.Fatalf("%s %.80s err = %v, calls = %d, reads = %d", c.op, c.args, err, len(h.calls), *h.reads)
		}
	}
}

func TestRecordsDeleteIsOnlyOfferedThroughAToolsListAndInNoProfile(t *testing.T) {
	h := newRecordsEnv(t)
	if _, err := h.confirmed(dataStoreRecordsDelete.ID, "open", `{"datastore_id":7,"keys":["a"]}`); err == nil {
		t.Fatal("datastorerecords.delete was offered without a tools list")
	}
	if !dataStoreRecordsDelete.RequiresToolAllowList {
		t.Fatal("datastorerecords.delete must require a tools list")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == dataStoreRecordsDelete.ID {
				t.Fatalf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}

func TestRecordsChangesAreNeverRetriedAndNameTheScope(t *testing.T) {
	for _, status := range []int{500, 403} {
		for _, c := range []struct{ op, conn, args string }{
			{dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"data":{"a":1}}`},
			{dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":["a"]}`},
		} {
			h := newRecordsEnv(t)
			h.status = status
			_, err := h.confirmed(c.op, c.conn, c.args)
			if err == nil || h.changing() != 1 || strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("%s/%d err = %v, changing = %d", c.op, status, err, h.changing())
			}
			if status == 500 && !strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("%s lacks the uncertain note: %v", c.op, err)
			}
			if status == 403 && !strings.Contains(err.Error(), "datastores:write") {
				t.Fatalf("%s err = %v, want the scope hint", c.op, err)
			}
		}
	}
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
		_, err := env.confirmed(dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"data":{"a":1}}`)
		if err == nil || mutations != 1 || !strings.Contains(err.Error(), "may have taken effect") {
			t.Fatalf("%s err = %v, mutations = %d", name, err, mutations)
		}
	}
}

func TestRecordsReadsNameBothScopesOn403(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(403, `{}`), nil })
	_, err := env.invoke(dataStoreRecordsList.ID, "open", `{"datastore_id":7}`)
	if err == nil || !strings.Contains(err.Error(), "datastores:read") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordsAnswersAreCheckedAndCapped(t *testing.T) {
	h := newRecordsEnv(t)
	h.deleted = `{"keys":["a","zzz"]}`
	result, err := h.confirmed(dataStoreRecordsDelete.ID, "recordsdelete", `{"datastore_id":7,"keys":["a","b"]}`)
	if err != nil || !strings.Contains(result, `"deleted_count":1`) || strings.Contains(result, "zzz") {
		t.Fatalf("delete = %s, %v", result, err)
	}
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return jsonResponse(200, `{"dataStore":`+dataStoreJSONOf(storeID, ownTeam, "S")+`}`), nil
		}
		return jsonResponse(200, `{"key":"other","data":{}}`), nil
	})
	_, err = env.confirmed(dataStoreRecordsCreate.ID, "open", `{"datastore_id":7,"key":"mine","data":{"a":1}}`)
	if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "may have taken effect") {
		t.Fatalf("mismatched key err = %v", err)
	}
}

func TestRecordToolsDeclareACompleteRisk(t *testing.T) {
	for _, d := range []capability.Descriptor{dataStoreRecordsList, dataStoreRecordsCreate, dataStoreRecordsDelete} {
		r := d.Risk
		if r.Effect == "" || r.Idempotency == "" || !r.OpenWorld || r.DataSensitivity == "" {
			t.Fatalf("%s has an incomplete risk: %+v", d.ID, r)
		}
		if d.ID != dataStoreRecordsList.ID && r.Confirmation != capability.ConfirmationRequired {
			t.Fatalf("%s needs confirmation: %+v", d.ID, r)
		}
	}
}
