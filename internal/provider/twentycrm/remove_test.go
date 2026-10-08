package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func removeAnswer(verb, id string) string {
	return `{"data":{"` + verb + `Person":{"id":"` + id + `"}}}`
}

func runRemoval(t *testing.T, action, args string, targets ...string) (any, error) {
	t.Helper()
	handlers := map[string]capability.Handler{
		"delete": invokeRecordsDelete, "destroy": invokeRecordsDestroy, "restore": invokeRecordsRestore,
	}
	red := &redact.Redactor{}
	return handlers[action](context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
}

func removeArgs(id string) string { return `{"object":"person","id":"` + id + `"}` }

func TestRecordsRemovalSendsTheFixedRoutePerAction(t *testing.T) {
	for _, tt := range []struct {
		action, method, uri, answer string
	}{
		{"delete", http.MethodDelete, "/rest/people/" + personID + "?soft_delete=true", removeAnswer("delete", personID)},
		{"destroy", http.MethodDelete, "/rest/people/" + personID, removeAnswer("delete", personID)},
		{"restore", http.MethodPatch, "/rest/people/" + personID + "/restore", createdAnswer("restore")},
	} {
		t.Run(tt.action, func(t *testing.T) {
			calls := serveWrites(t, http.StatusOK, tt.answer)
			result, err := runRemoval(t, tt.action, removeArgs(personID))
			if err != nil {
				t.Fatal(err)
			}
			sent := writes(*calls)
			if len(sent) != 1 || sent[0].Method != tt.method || sent[0].URI != tt.uri || sent[0].Body != "" {
				t.Fatalf("writes = %+v, want one %s %s", sent, tt.method, tt.uri)
			}
			out, _ := json.Marshal(result)
			if tt.action == "restore" {
				if !strings.Contains(string(out), personID) {
					t.Errorf("restore result = %s", out)
				}
			} else if string(out) != `{"deleted":true}` {
				t.Errorf("result = %s", out)
			}
		})
	}
}

func TestRecordsRemovalChecksTheAnsweredRecord(t *testing.T) {
	for _, action := range []string{"delete", "destroy", "restore"} {
		verb := "delete"
		if action == "restore" {
			verb = "restore"
		}
		for name, answer := range map[string]string{
			"other record": removeAnswer(verb, personTwoID),
			"no record":    `{"data":{}}`,
			"wrong key":    strings.Replace(removeAnswer(verb, personID), verb+"Person", "updatePerson", 1),
			"bad id":       removeAnswer(verb, "nope"),
		} {
			t.Run(action+" "+name, func(t *testing.T) {
				calls := serveWrites(t, http.StatusOK, answer)
				_, err := runRemoval(t, action, removeArgs(personID))
				if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") {
					t.Errorf("err = %v", err)
				}
				if len(writes(*calls)) != 1 {
					t.Errorf("writes = %d", len(writes(*calls)))
				}
			})
		}
	}
}

func TestRecordsRemovalRefusesBeforeSecretAccessAndAnyIO(t *testing.T) {
	refuse(t)
	handlers := map[string]capability.Handler{
		"delete": invokeRecordsDelete, "destroy": invokeRecordsDestroy, "restore": invokeRecordsRestore,
	}
	for _, tt := range []struct {
		args    string
		targets []string
	}{
		{removeArgs(personID), []string{"object/rocket"}},
		{`{"object":"workspaceMember","id":"` + personID + `"}`, nil},
		{`{"object":"noteTarget","id":"` + personID + `"}`, nil},
		{`{"object":"../x","id":"` + personID + `"}`, nil},
		{removeArgs("not-a-uuid"), nil},
		{removeArgs(personID + "/restore"), nil},
		{removeArgs(""), nil},
	} {
		for action, handler := range handlers {
			_, err := handler(context.Background(), targetConnection(tt.targets...), countingResolver(t), &redact.Redactor{}, json.RawMessage(tt.args))
			if !asInvalidOK(err) || strings.Contains(err.Error(), "workspaceMember") || strings.Contains(err.Error(), "noteTarget") {
				t.Errorf("%s %s: err = %v", action, tt.args, err)
			}
		}
	}
}

func TestRecordsRemovalNeverRepeatsAfterAnUnclearResult(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		fail   error
		body   string
	}{
		"server error":   {status: 500, body: `{"error":"` + bodyCanary + `"}`},
		"gateway":        {status: 504},
		"unreadable":     {status: 200, body: `not json ` + bodyCanary},
		"timeout":        {fail: &net.DNSError{IsTimeout: true, Err: bodyCanary}},
		"connection end": {fail: errors.New(bodyCanary)},
	} {
		for _, action := range []string{"delete", "destroy", "restore"} {
			t.Run(name+" "+action, func(t *testing.T) {
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
				_, err := runRemoval(t, action, removeArgs(personID))
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

func TestRecordsRemovalPermissionNamesTheRoleRight(t *testing.T) {
	for action, right := range map[string]string{"delete": "Delete Records", "destroy": "Destroy Records"} {
		serveWrites(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
		_, err := runRemoval(t, action, removeArgs(personID))
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), right) ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", action, err)
		}
	}
	serveWrites(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
	_, err := runRemoval(t, "restore", removeArgs(personID))
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("restore: err = %v", err)
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity} {
		serveWrites(t, status, `{"messages":["`+bodyCanary+`"]}`)
		if _, err := runRemoval(t, "delete", removeArgs(personID)); err == nil || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
}

func TestRecordsRemovalDescriptorsDeclareTheirRisk(t *testing.T) {
	for _, tt := range []struct {
		d         capability.Descriptor
		effect    capability.Effect
		allowList bool
	}{
		{recordsDelete, capability.EffectDelete, true},
		{recordsDestroy, capability.EffectDelete, true},
		{recordsRestore, capability.EffectUpdate, false},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: capability.IdempotencyIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: recordDataSensitivity}
		if tt.d.Risk != want || tt.d.RequiresToolAllowList != tt.allowList || tt.d.Provider != Provider {
			t.Errorf("%s = %+v", tt.d.ID, tt.d)
		}
	}
	if !strings.Contains(recordsDestroy.Description, "cannot be undone") ||
		!strings.Contains(recordsDestroy.Description, "does not tell a soft from a permanent deletion") {
		t.Errorf("destroy description = %q", recordsDestroy.Description)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == recordsDelete.ID || id == recordsDestroy.ID || id == recordsRestore.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestRecordsRemovalToolsNeedConfirmationAndTheAllowList(t *testing.T) {
	serveWrites(t, http.StatusOK, removeAnswer("delete", personID))
	cfg := coreConfig()
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	bare := cfg.Connections["crm"]
	bare.Permissions = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Permissions: all, Tools: []string{recordsDelete.ID, recordsDestroy.ID, recordsRestore.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	for _, operation := range []string{recordsDelete.ID, recordsDestroy.ID} {
		request := application.InvokeRequest{Operation: operation, Connection: "crm",
			Arguments: json.RawMessage(removeArgs(personID)), Confirmed: true}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s was offered without a tools list", operation)
		}
		request.Connection = "crm-internal"
		request.Confirmed = false
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirmation", operation)
		}
	}
}

func TestRecordsListDeletedSendsTheFixedTrashFilter(t *testing.T) {
	list := `{"data":{"people":[{"id":"` + personID + `","city":"x"}]},"pageInfo":{"hasNextPage":false}}`
	for _, tt := range []struct {
		args, want string
	}{
		{`{"object":"person","deleted":true}`, "deletedAt[is]:NOT_NULL"},
		{`{"object":"person","deleted":false}`, ""},
		{`{"object":"person"}`, ""},
	} {
		var filter string
		listed := false
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == schemaPath {
				return jsonResponse(http.StatusOK, writeSchema), nil
			}
			listed = true
			filter = request.URL.Query().Get("filter")
			return jsonResponse(http.StatusOK, list), nil
		})
		stubLimiter(t, cloudKey)
		red := &redact.Redactor{}
		if _, err := invokeRecordsList(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(tt.args)); err != nil {
			t.Fatal(err)
		}
		if !listed || filter != tt.want {
			t.Errorf("%s: filter = %q, want %q", tt.args, filter, tt.want)
		}
	}
}

func TestRecordsListDeletedBindsTheCursor(t *testing.T) {
	list := `{"data":{"people":[{"id":"` + personID + `"}]},"pageInfo":{"hasNextPage":true,"endCursor":"abc"}}`
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, writeSchema), nil
		}
		return jsonResponse(http.StatusOK, list), nil
	})
	stubLimiter(t, cloudKey)
	red := &redact.Redactor{}
	result, err := invokeRecordsList(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(`{"object":"person","deleted":true}`))
	if err != nil {
		t.Fatal(err)
	}
	cursor := result.(*RecordList).NextCursor
	refuse(t)
	_, err = invokeRecordsList(context.Background(), targetConnection(), resolver(red), red,
		json.RawMessage(`{"object":"person","cursor":"`+cursor+`"}`))
	if !asInvalidOK(err) {
		t.Errorf("a trash cursor continued the live list: %v", err)
	}
}
