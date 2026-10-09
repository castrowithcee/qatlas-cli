package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func batchAnswer(verb string, ids ...string) string {
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, `{"id":"`+id+`","city":"Berlin","createdAt":"2026-01-01T00:00:00.000Z"}`)
	}
	return `{"data":{"` + verb + `People":[` + strings.Join(items, ",") + `]}}`
}

func runBatch(t *testing.T, mode, args string, targets ...string) (json.RawMessage, error) {
	t.Helper()
	handler := map[string]capability.Handler{"create": invokeRecordsBatchCreate, "update": invokeRecordsBatchUpdate,
		"delete": invokeRecordsBatchDelete}[mode]
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(result)
	return out, nil
}

func idList(ids ...string) string {
	return `["` + strings.Join(ids, `","`) + `"]`
}

func TestRecordsBatchSendsTheFixedRoutes(t *testing.T) {
	filter := "id[in]:[" + personID + "," + personTwoID + "]"
	for _, tt := range []struct {
		name, mode, args, answer, method, uri, body, out string
	}{
		{"create", "create", `{"object":"person","records":[{` + coveredCreate + `},{` + coveredCreate + `,"city":"Berlin"}]}`,
			batchAnswer("create", personID, personTwoID), http.MethodPost, "/rest/batch/people?depth=0",
			`[{"name":{"firstName":"Ada"},"companyId":"` + otherID + `"},{"name":{"firstName":"Ada"},"companyId":"` +
				otherID + `","city":"Berlin"}]`, `"count":2`},
		{"upsert", "create", `{"object":"person","upsert":true,"records":[{` + coveredCreate + `}]}`,
			batchAnswer("create", personID), http.MethodPost, "/rest/batch/people?depth=0&upsert=true",
			`[{"name":{"firstName":"Ada"},"companyId":"` + otherID + `"}]`, `"count":1`},
		{"update", "update", `{"object":"person","ids":` + idList(personID, strings.ToUpper(personTwoID)) + `,"fields":{"city":"Berlin"}}`,
			batchAnswer("update", personTwoID, personID), http.MethodPatch,
			"/rest/people?depth=0&filter=" + url.QueryEscape(filter), `{"city":"Berlin"}`, `"count":2`},
		{"delete", "delete", `{"object":"person","ids":` + idList(personID, personTwoID) + `}`,
			batchAnswer("delete", personID, personTwoID), http.MethodDelete,
			"/rest/people?filter=" + url.QueryEscape(filter) + "&soft_delete=true", "", `{"deleted":true,"count":2}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := serveWrites(t, http.StatusOK, tt.answer)
			out, err := runBatch(t, tt.mode, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			sent := writes(*calls)
			if len(sent) != 1 || sent[0].Method != tt.method || sent[0].URI != tt.uri {
				t.Fatalf("writes = %+v, want one %s %s", sent, tt.method, tt.uri)
			}
			if tt.body == "" && sent[0].Body != "" {
				t.Errorf("body = %s", sent[0].Body)
			} else if tt.body != "" {
				sameJSON(t, sent[0].Body, tt.body)
			}
			if !strings.Contains(string(out), tt.out) {
				t.Errorf("result = %s", out)
			}
		})
	}
}

func TestRecordsBatchProjectsTheAnsweredRecords(t *testing.T) {
	serveWrites(t, http.StatusOK, `{"data":{"createPeople":[{"id":"`+personID+`","city":"Berlin",`+
		`"company":{"name":"embedded"},"bio":{"blocknote":"x","markdown":"# hi"}}]}}`)
	out, err := runBatch(t, "create", `{"object":"person","records":[{`+coveredCreate+`}]}`)
	if err != nil || strings.Contains(string(out), "embedded") || strings.Contains(string(out), "blocknote") ||
		!strings.Contains(string(out), `"markdown":"# hi"`) {
		t.Fatalf("out = %s, err = %v", out, err)
	}
}

func TestRecordsBatchRefusesBeforeAnyIO(t *testing.T) {
	many := func(n int, item string) string {
		return "[" + strings.TrimSuffix(strings.Repeat(item+",", n), ",") + "]"
	}
	var sixtyOne []string
	for i := 0; i < 61; i++ {
		sixtyOne = append(sixtyOne, fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
	}
	big := strings.Repeat("x", 60000)
	bigRecords := "[" + strings.TrimSuffix(strings.Repeat(`{`+coveredCreate+`,"city":"`+big+`"},`, 20), ",") + "]"
	for _, tt := range []struct {
		name, mode, args string
		targets          []string
	}{
		{"create empty list", "create", `{"object":"person","records":[]}`, nil},
		{"create 61 records", "create", `{"object":"person","records":` + many(61, `{`+coveredCreate+`}`) + `}`, nil},
		{"create null record", "create", `{"object":"person","records":[null]}`, nil},
		{"create system field", "create", `{"object":"person","records":[{` + coveredCreate + `},{"id":"` + personID + `"}]}`, nil},
		{"create unknown field", "create", `{"object":"person","records":[{` + coveredCreate + `},{"nope":1}]}`, nil},
		{"create invalid second record", "create", `{"object":"person","records":[{` + coveredCreate + `},{"city":5,` + coveredCreate + `}]}`, nil},
		{"create missing required", "create", `{"object":"person","records":[{` + coveredCreate + `},{"city":"x"}]}`, nil},
		{"create over the request cap", "create", `{"object":"person","records":` + bigRecords + `}`, nil},
		{"create unreachable object", "create", `{"object":"person","records":[{` + coveredCreate + `}]}`, []string{"object/rocket"}},
		{"update empty ids", "update", `{"object":"person","ids":[],"fields":{"city":"x"}}`, nil},
		{"update missing ids", "update", `{"object":"person","fields":{"city":"x"}}`, nil},
		{"update 61 ids", "update", `{"object":"person","ids":` + idList(sixtyOne...) + `,"fields":{"city":"x"}}`, nil},
		{"update duplicates", "update", `{"object":"person","ids":` + idList(personID, strings.ToUpper(personID)) + `,"fields":{"city":"x"}}`, nil},
		{"update bad id", "update", `{"object":"person","ids":` + idList(personID, "x],id[in]:[y") + `,"fields":{"city":"x"}}`, nil},
		{"update no fields", "update", `{"object":"person","ids":` + idList(personID) + `,"fields":{}}`, nil},
		{"update invalid value", "update", `{"object":"person","ids":` + idList(personID) + `,"fields":{"age":"x"}}`, nil},
		{"update unreachable object", "update", `{"object":"person","ids":` + idList(personID) + `,"fields":{"city":"x"}}`, []string{"object/rocket"}},
		{"delete empty ids", "delete", `{"object":"person","ids":[]}`, nil},
		{"delete missing ids", "delete", `{"object":"person"}`, nil},
		{"delete 61 ids", "delete", `{"object":"person","ids":` + idList(sixtyOne...) + `}`, nil},
		{"delete duplicates", "delete", `{"object":"person","ids":` + idList(personID, personID) + `}`, nil},
		{"delete bad id", "delete", `{"object":"person","ids":["not-a-uuid"]}`, nil},
		{"delete unreachable object", "delete", `{"object":"person","ids":` + idList(personID) + `}`, []string{"object/rocket"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := serveWrites(t, http.StatusOK, batchAnswer(tt.mode, personID))
			_, err := runBatch(t, tt.mode, tt.args, tt.targets...)
			// A request above the cap is refused as unreadable arguments, still before any I/O.
			if err == nil || (!asInvalidOK(err) && !strings.Contains(tt.name, "cap")) {
				t.Errorf("err = %v", err)
			}
			if len(writes(*calls)) != 0 {
				t.Errorf("writes = %+v", writes(*calls))
			}
		})
	}
}

func TestRecordsBatchChecksBoundsBeforeSecretAccessAndIO(t *testing.T) {
	refuse(t)
	for mode, args := range map[string]string{
		"create": `{"object":"person","records":[]}`,
		"update": `{"object":"person","ids":[],"fields":{"city":"x"}}`,
		"delete": `{"object":"person","ids":[]}`,
	} {
		handler := map[string]capability.Handler{"create": invokeRecordsBatchCreate, "update": invokeRecordsBatchUpdate,
			"delete": invokeRecordsBatchDelete}[mode]
		_, err := handler(context.Background(), targetConnection(), countingResolver(t), &redact.Redactor{}, json.RawMessage(args))
		if !asInvalidOK(err) {
			t.Errorf("%s: err = %v", mode, err)
		}
	}
}

func TestRecordsBatchReportsPartialEffectsAsUncertain(t *testing.T) {
	create := `{"object":"person","records":[{` + coveredCreate + `},{` + coveredCreate + `}]}`
	ids := `{"object":"person","ids":` + idList(personID, personTwoID)
	for _, tt := range []struct{ name, mode, args, answer string }{
		{"create fewer", "create", create, batchAnswer("create", personID)},
		{"create more", "create", create, batchAnswer("create", personID, personTwoID, otherID)},
		{"create same record twice", "create", create, batchAnswer("create", personID, personID)},
		{"create no list", "create", create, `{"data":{}}`},
		{"update fewer", "update", ids + `,"fields":{"city":"x"}}`, batchAnswer("update", personID)},
		{"update foreign id", "update", ids + `,"fields":{"city":"x"}}`, batchAnswer("update", personID, otherID)},
		{"update repeated id", "update", ids + `,"fields":{"city":"x"}}`, batchAnswer("update", personID, personID)},
		{"update wrong key", "update", ids + `,"fields":{"city":"x"}}`, batchAnswer("delete", personID, personTwoID)},
		{"delete fewer", "delete", ids + `}`, batchAnswer("delete", personID)},
		{"delete foreign id", "delete", ids + `}`, batchAnswer("delete", personID, otherID)},
		{"delete bad id", "delete", ids + `}`, batchAnswer("delete", personID, "nope")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := serveWrites(t, http.StatusOK, tt.answer)
			out, err := runBatch(t, tt.mode, tt.args)
			if err == nil || classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") {
				t.Errorf("out = %s, err = %v", out, err)
			}
			if len(writes(*calls)) != 1 {
				t.Errorf("writes = %d", len(writes(*calls)))
			}
		})
	}
}

func TestRecordsBatchUpsertMayAnswerTheSameRecordTwice(t *testing.T) {
	serveWrites(t, http.StatusOK, batchAnswer("create", personID, personID))
	out, err := runBatch(t, "create", `{"object":"person","upsert":true,"records":[{`+coveredCreate+`},{`+coveredCreate+`}]}`)
	if err != nil || !strings.Contains(string(out), `"count":2`) {
		t.Fatalf("out = %s, err = %v", out, err)
	}
}

func TestRecordsBatchNeverRepeatsAfterAnUnclearResult(t *testing.T) {
	args := map[string]string{
		"create": `{"object":"person","records":[{` + coveredCreate + `}]}`,
		"update": `{"object":"person","ids":` + idList(personID) + `,"fields":{"city":"x"}}`,
		"delete": `{"object":"person","ids":` + idList(personID) + `}`,
	}
	for name, tt := range map[string]struct {
		status int
		fail   error
		body   string
	}{
		"server error": {status: 500, body: `{"error":"` + bodyCanary + `"}`},
		"gateway":      {status: 504},
		"unreadable":   {status: 200, body: `not json ` + bodyCanary},
		"timeout":      {fail: &net.DNSError{IsTimeout: true, Err: bodyCanary}},
		"connection":   {fail: errors.New(bodyCanary)},
	} {
		for mode, input := range args {
			t.Run(name+" "+mode, func(t *testing.T) {
				seen := 0
				serve(t, func(request *http.Request) (*http.Response, error) {
					if request.URL.Path == schemaPath {
						return jsonResponse(http.StatusOK, writeSchema), nil
					}
					seen++
					if tt.fail != nil {
						return nil, tt.fail
					}
					return jsonResponse(tt.status, tt.body), nil
				})
				stubLimiter(t, cloudKey)
				_, err := runBatch(t, mode, input)
				if err == nil || seen != 1 {
					t.Fatalf("err = %v, writing requests = %d, want one", err, seen)
				}
				if strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("provider text in the error: %v", err)
				}
				var failure *provider.Error
				mayHave := errors.As(err, &failure) && (failure.Class != provider.ClassUnreachable || failure.MayHaveArrived())
				if mayHave && !strings.Contains(err.Error(), "before repeating") {
					t.Errorf("no uncertainty named: %v", err)
				}
			})
		}
	}
}

func TestRecordsBatchPermissionNamesTheRole(t *testing.T) {
	serveWrites(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
	for mode, want := range map[string]string{"create": "create or change records", "update": "create or change records",
		"delete": "Delete Records"} {
		args := `{"object":"person","ids":` + idList(personID) + `,"fields":{"city":"x"}}`
		if mode == "create" {
			args = `{"object":"person","records":[{` + coveredCreate + `}]}`
		}
		_, err := runBatch(t, mode, args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), want) ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", mode, err)
		}
	}
}

func TestRecordsBatchDescriptorsDeclareTheirRisk(t *testing.T) {
	for _, tt := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
		allowList   bool
	}{
		{recordsBatchCreate, capability.EffectUpdate, capability.IdempotencyNonIdempotent, false},
		{recordsBatchUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		{recordsBatchDelete, capability.EffectDelete, capability.IdempotencyIdempotent, true},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idempotency,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: recordDataSensitivity}
		if tt.d.Risk != want || tt.d.RequiresToolAllowList != tt.allowList || tt.d.Provider != Provider {
			t.Errorf("%s = %+v", tt.d.ID, tt.d)
		}
	}
	if !strings.Contains(recordsBatchCreate.Description, "unique fields") {
		t.Errorf("batchcreate description = %q", recordsBatchCreate.Description)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if strings.Contains(id, "batch") {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestRecordsBatchDeleteNeedsConfirmationAndTheAllowList(t *testing.T) {
	serveWrites(t, http.StatusOK, batchAnswer("delete", personID))
	cfg := coreConfig()
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	bare := cfg.Connections["crm"]
	bare.Permissions = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Permissions: all, Tools: []string{recordsBatchDelete.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	request := application.InvokeRequest{Operation: recordsBatchDelete.ID, Connection: "crm",
		Arguments: json.RawMessage(`{"object":"person","ids":` + idList(personID) + `}`), Confirmed: true}
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Error("batchdelete was offered without a tools list")
	}
	request.Connection = "crm-internal"
	request.Confirmed = false
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Error("batchdelete ran without confirmation")
	}
}
