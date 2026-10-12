package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	groupID       = "99999999-0000-4000-8000-0000000000b1"
	groupOfUnlist = "99999999-0000-4000-8000-0000000000b2"
	groupOfStd    = "99999999-0000-4000-8000-0000000000b3"
	groupOfIndex  = "99999999-0000-4000-8000-0000000000b4"
	groupDeleted  = "99999999-0000-4000-8000-0000000000b5"
	groupCanary   = "canary-group-value-5521"
)

func groupWire(id, view string, visible bool, position float64, extra string) string {
	flag := map[bool]string{true: "true", false: "false"}
	number, _ := json.Marshal(position)
	return `{"id":"` + id + `","viewId":"` + view + `","fieldMetadataId":"` + sortFieldTaken + `","fieldValue":"` +
		groupCanary + `","isVisible":` + flag[visible] + `,"position":` + string(number) + `,` +
		`"workspaceId":"` + appCn + `","applicationId":"` + appCn + `"` + extra + `}`
}

// serveGroups answers the reads of the group tool and answers every write with status and answer.
func serveGroups(t *testing.T, status int, answer string) *[]colCall {
	t.Helper()
	calls := &[]colCall{}
	views := map[string]string{
		colView:     sortViewJSON(colView, "WORKSPACE", "KANBAN", true, ""),
		colUnlisted: sortViewJSON(colUnlisted, "UNLISTED", "KANBAN", true, ""),
		colStandard: sortViewJSON(colStandard, "WORKSPACE", "KANBAN", false, ""),
		colIndex:    sortViewJSON(colIndex, "WORKSPACE", "KANBAN", true, "INDEX"),
	}
	groups := map[string]string{
		groupID:       groupWire(groupID, colView, true, 1, ""),
		groupOfUnlist: groupWire(groupOfUnlist, colUnlisted, true, 1, ""),
		groupOfStd:    groupWire(groupOfStd, colStandard, true, 1, ""),
		groupOfIndex:  groupWire(groupOfIndex, colIndex, true, 1, ""),
		groupDeleted:  groupWire(groupDeleted, colView, true, 1, `,"deletedAt":"2026-01-01T00:00:00Z"`),
	}
	serve(t, func(request *http.Request) (*http.Response, error) {
		call := colCall{Method: request.Method, URI: request.URL.RequestURI()}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			call.Body = string(data)
		}
		*calls = append(*calls, call)
		if request.Method != http.MethodGet {
			return jsonResponse(status, answer), nil
		}
		path := request.URL.Path
		switch {
		case strings.HasPrefix(path, viewsPath+"/"):
			if body, ok := views[strings.TrimPrefix(path, viewsPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		case strings.HasPrefix(path, viewGroupsPath+"/"):
			if body, ok := groups[strings.TrimPrefix(path, viewGroupsPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		}
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func TestViewGroupsUpdateReadsTheGroupThenSendsOnlyTheGivenValues(t *testing.T) {
	for name, tt := range map[string]struct {
		args, body string
		answer     string
	}{
		"visibility": {`{"id":"` + groupID + `","is_visible":false}`, `{"isVisible":false}`, groupWire(groupID, colView, false, 1, "")},
		"position":   {`{"id":"` + groupID + `","position":2.5}`, `{"position":2.5}`, groupWire(groupID, colView, true, 2.5, "")},
		"both": {`{"id":"` + groupID + `","is_visible":false,"position":3}`, `{"isVisible":false,"position":3}`,
			groupWire(groupID, colView, false, 3, "")},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveGroups(t, http.StatusOK, tt.answer)
			result, err := runColumn(invokeViewGroupsUpdate, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := json.Marshal(result)
			var group ViewGroupResult
			if json.Unmarshal(out, &group) != nil || group.ID != groupID || group.ViewID != colView {
				t.Fatalf("group = %s", out)
			}
			for _, canary := range []string{groupCanary, appCn, "applicationId", "workspaceId", "fieldValue"} {
				if strings.Contains(string(out), canary) {
					t.Errorf("%s reached the output: %s", canary, out)
				}
			}
			sent := columnWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].URI != viewGroupsPath+"/"+groupID ||
				sent[0].Body != tt.body {
				t.Errorf("writes = %+v, want body %s", sent, tt.body)
			}
			if (*calls)[0].URI != viewGroupsPath+"/"+groupID || (*calls)[1].URI != viewsPath+"/"+colView {
				t.Errorf("the group and its view are read first: %+v", *calls)
			}
		})
	}
}

func TestViewGroupsUpdateSchemaOffersOnlyVisibilityAndPosition(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Additional bool                       `json:"additionalProperties"`
	}
	if json.Unmarshal(viewgroupsUpdate.InputSchema, &schema) != nil || schema.Additional || len(schema.Properties) != 3 {
		t.Fatalf("schema = %s", viewgroupsUpdate.InputSchema)
	}
	for _, name := range []string{"id", "is_visible", "position"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("schema lacks %s", name)
		}
	}
	refuse(t)
	for _, args := range []string{
		`{"id":"` + groupID + `","is_visible":true,"field_value":"x"}`,
		`{"id":"` + groupID + `","position":1,"field_id":"` + sortFieldTaken + `"}`,
		`{"id":"` + groupID + `","position":1,"fieldValue":"x"}`,
		`{"id":"` + groupID + `","view_id":"` + colView + `","position":1}`,
	} {
		if _, err := runColumn(invokeViewGroupsUpdate, args); !asInvalidOK(err) {
			t.Errorf("%s: err = %v", args, err)
		}
	}
}

func TestViewGroupsUpdateRefusesInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for _, args := range []string{
		`{"id":"` + groupID + `"}`,
		`{"id":"nope","is_visible":true}`,
		`{"id":"` + groupID + `","position":-1}`,
		`{"id":"` + groupID + `","position":1000001}`,
		`{"id":"` + groupID + `","is_visible":"yes"}`,
	} {
		if _, err := runColumn(invokeViewGroupsUpdate, args); !asInvalidOK(err) {
			t.Errorf("%s: err = %v", args, err)
		}
	}
}

func TestViewGroupsUpdateIsBoundToTheViewOfTheGroup(t *testing.T) {
	for name, tt := range map[string]struct {
		id    string
		class provider.Class
	}{
		"group of an unlisted view": {groupOfUnlist, provider.ClassNotFound},
		"group of a standard view":  {groupOfStd, ""},
		"group of an index view":    {groupOfIndex, ""},
		"unknown group":             {colUnknown, provider.ClassNotFound},
		"deleted group":             {groupDeleted, provider.ClassNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveGroups(t, http.StatusOK, groupWire(groupID, colView, false, 1, ""))
			_, err := runColumn(invokeViewGroupsUpdate, `{"id":"`+tt.id+`","is_visible":false}`)
			if err == nil || len(columnWrites(calls)) != 0 || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, writes = %+v", err, columnWrites(calls))
			}
			if tt.class != "" && classOf(err) != tt.class {
				t.Errorf("class = %q, err = %v", classOf(err), err)
			}
			if tt.class == "" && !asInvalidOK(err) {
				t.Errorf("err = %v, want invalid-request", err)
			}
		})
	}
}

func TestViewGroupsUpdateNeedsAConnectionWithoutObjectTargets(t *testing.T) {
	refuse(t)
	_, err := invokeViewGroupsUpdate(context.Background(), targetConnection("object/person"), countingResolver(t),
		&redact.Redactor{}, json.RawMessage(`{"id":"`+groupID+`","is_visible":false}`))
	if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
		t.Errorf("err = %v", err)
	}
}

func TestViewGroupsUpdateNeverRepeatsAfterAnUnclearResult(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		fail   error
		body   string
	}{
		"server error": {status: 500, body: `{"error":"` + bodyCanary + `"}`},
		"gateway":      {status: 504},
		"unreadable":   {status: 200, body: `not json ` + bodyCanary},
		"other group":  {status: 200, body: groupWire(colUnknown, colView, false, 1, "")},
		"other value":  {status: 200, body: groupWire(groupID, colView, true, 1, "")},
		"timeout":      {fail: &net.DNSError{IsTimeout: true, Err: bodyCanary}},
		"connection":   {fail: errors.New(bodyCanary)},
	} {
		t.Run(name, func(t *testing.T) {
			written := 0
			serveGroups(t, http.StatusOK, `{}`)
			inner := transport
			serve(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return inner.RoundTrip(request)
				}
				written++
				if tt.fail != nil {
					return nil, tt.fail
				}
				return jsonResponse(tt.status, tt.body), nil
			})
			_, err := runColumn(invokeViewGroupsUpdate, `{"id":"`+groupID+`","is_visible":false}`)
			if err == nil || written != 1 || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, writes = %d, want one", err, written)
			}
			var failure *provider.Error
			mayHave := errors.As(err, &failure) && (failure.Class != provider.ClassUnreachable || failure.MayHaveArrived())
			if mayHave && !strings.Contains(err.Error(), "before repeating") {
				t.Errorf("no uncertainty named: %v", err)
			}
		})
	}
}

func TestViewGroupsUpdatePermissionNamesTheViewsRight(t *testing.T) {
	serveGroups(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
	_, err := runColumn(invokeViewGroupsUpdate, `{"id":"`+groupID+`","is_visible":false}`)
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "called Views") ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestViewGroupsUpdateDescriptorDeclaresItsRisk(t *testing.T) {
	want := capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: viewDataSensitivity}
	if viewgroupsUpdate.Risk != want || !viewgroupsUpdate.RequiresToolAllowList || viewgroupsUpdate.Provider != Provider {
		t.Errorf("descriptor = %+v", viewgroupsUpdate)
	}
}
