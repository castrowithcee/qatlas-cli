package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const thirdID = "3c7f1dd0-15cc-4ce9-9f2a-7ffd5b3a1e04"

func idList(ids ...string) string { return `["` + strings.Join(ids, `","`) + `"]` }

func mergeArgs(ids string, index int) string {
	return `{"object":"person","ids":` + ids + `,"conflict_priority_index":` + strconv.Itoa(index) + `}`
}

func mergeAnswer(id string) string {
	return strings.Replace(createdAnswer("merge"), personID, id, 1)
}

func duplicatesAnswer(entries ...string) string {
	return `{"data":[` + strings.Join(entries, ",") + `]}`
}

func duplicatesEntry(total int, ids ...string) string {
	records := make([]string, 0, len(ids))
	for _, id := range ids {
		records = append(records, `{"id":"`+id+`","city":"Berlin","company":{"id":"`+otherID+`"},"companyId":"`+otherID+`"}`)
	}
	return `{"personDuplicates":[` + strings.Join(records, ",") + `],"totalCount":` + strconv.Itoa(total) +
		`,"pageInfo":{"hasNextPage":false}}`
}

func runMergeTool(t *testing.T, handler capability.Handler, args string, targets ...string) (any, error) {
	t.Helper()
	red := &redact.Redactor{}
	return handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
}

func TestMergeToolsSendTheFixedRouteAndDryRun(t *testing.T) {
	for _, tt := range []struct {
		name    string
		handler capability.Handler
		dryRun  string
	}{
		{"preview", invokeRecordsMergePreview, "true"},
		{"merge", invokeRecordsMerge, "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := serveWrites(t, http.StatusOK, mergeAnswer(personTwoID))
			result, err := runMergeTool(t, tt.handler, mergeArgs(idList(personID, personTwoID), 1))
			if err != nil {
				t.Fatal(err)
			}
			sent := writes(*calls)
			want := `{"conflictPriorityIndex":1,"dryRun":` + tt.dryRun + `,"ids":["` + personID + `","` + personTwoID + `"]}`
			if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].URI != "/rest/people/merge?depth=0" ||
				sent[0].Body != want {
				t.Fatalf("writes = %+v, want one PATCH with %s", sent, want)
			}
			out, _ := json.Marshal(result)
			if !strings.Contains(string(out), personTwoID) || strings.Contains(string(out), "blocknote") ||
				strings.Contains(string(out), `"company":`) || !strings.Contains(string(out), `"markdown":"# hi"`) {
				t.Errorf("result is not projected like records.get: %s", out)
			}
		})
	}
}

func TestMergeToolsCheckTheResultIdentifier(t *testing.T) {
	for _, tt := range []struct {
		name    string
		handler capability.Handler
		note    string
	}{
		{"preview", invokeRecordsMergePreview, ""},
		{"merge", invokeRecordsMerge, "may have taken effect"},
	} {
		for name, answer := range map[string]string{
			"unrelated": mergeAnswer(thirdID),
			"no record": `{"data":{}}`,
			"wrong key": strings.Replace(mergeAnswer(personID), "mergePerson", "updatePerson", 1),
		} {
			t.Run(tt.name+" "+name, func(t *testing.T) {
				calls := serveWrites(t, http.StatusOK, answer)
				_, err := runMergeTool(t, tt.handler, mergeArgs(idList(personID, personTwoID), 0))
				if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), tt.note) {
					t.Errorf("err = %v", err)
				}
				if len(writes(*calls)) != 1 {
					t.Errorf("writes = %d", len(writes(*calls)))
				}
			})
		}
	}
}

func TestMergeToolsRefuseBadArgumentsBeforeAnyIO(t *testing.T) {
	refuse(t)
	nine, ten := []string{}, []string{}
	for i := 0; i < 10; i++ {
		id := "11111111-2222-4333-8444-5555555555" + strconv.Itoa(10+i)
		if i < 9 {
			nine = append(nine, id)
		}
		ten = append(ten, id)
	}
	for name, tt := range map[string]struct {
		args    string
		targets []string
	}{
		"ten ids":           {mergeArgs(idList(ten...), 0), nil},
		"one id":            {mergeArgs(idList(personID), 0), nil},
		"no ids":            {mergeArgs(`[]`, 0), nil},
		"repeated id":       {mergeArgs(idList(personID, personID), 0), nil},
		"repeated id case":  {mergeArgs(idList(personID, strings.ToUpper(personID)), 0), nil},
		"bad uuid":          {mergeArgs(idList(personID, "nope"), 0), nil},
		"negative index":    {mergeArgs(idList(personID, personTwoID), -1), nil},
		"index past ids":    {mergeArgs(idList(personID, personTwoID), 2), nil},
		"no index":          {`{"object":"person","ids":` + idList(personID, personTwoID) + `}`, nil},
		"unreachable":       {mergeArgs(idList(personID, personTwoID), 0), []string{"object/rocket"}},
		"system object":     {`{"object":"workspaceMember","ids":` + idList(personID, personTwoID) + `,"conflict_priority_index":0}`, nil},
		"nine ids bad idx":  {mergeArgs(idList(nine...), 9), nil},
		"nine ids no match": {mergeArgs(idList(nine[:8]...), 8), nil},
	} {
		for tool, handler := range map[string]capability.Handler{"preview": invokeRecordsMergePreview, "merge": invokeRecordsMerge} {
			_, err := handler(context.Background(), targetConnection(tt.targets...), countingResolver(t), &redact.Redactor{}, json.RawMessage(tt.args))
			if !asInvalidOK(err) || strings.Contains(err.Error(), "workspaceMember") {
				t.Errorf("%s %s: err = %v", tool, name, err)
			}
		}
	}
	// Nine ids with a valid index pass the local checks.
	if _, err := newRecordMerge(targetConnection(), "merge records", json.RawMessage(mergeArgs(idList(nine...), 8)), true); err != nil {
		t.Errorf("nine ids: %v", err)
	}
}

func TestDuplicatesSendsTheFixedRequestAndProjectsMatches(t *testing.T) {
	calls := serveWrites(t, http.StatusOK, duplicatesAnswer(duplicatesEntry(3, thirdID), duplicatesEntry(0)))
	result, err := runMergeTool(t, invokeRecordsDuplicates, `{"object":"person","ids":`+idList(personID, personTwoID)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	sent := writes(*calls)
	if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].URI != "/rest/people/duplicates?depth=0" ||
		sent[0].Body != `{"ids":["`+personID+`","`+personTwoID+`"]}` {
		t.Fatalf("writes = %+v", sent)
	}
	out, _ := json.Marshal(result)
	want := `{"matches":[{"id":"` + personID + `","total_count":3,"duplicates":[{"id":"` + thirdID +
		`","fields":{"city":"Berlin","companyId":"` + otherID + `"}}]},{"id":"` + personTwoID +
		`","total_count":0,"duplicates":[]}]}`
	if string(out) != want {
		t.Errorf("result = %s, want %s", out, want)
	}
}

func TestDuplicatesRefusesUnusableAnswers(t *testing.T) {
	for name, answer := range map[string]string{
		"missing entry":    duplicatesAnswer(duplicatesEntry(1, thirdID)),
		"no data":          `{}`,
		"bad record":       duplicatesAnswer(strings.Replace(duplicatesEntry(1, thirdID), thirdID, "nope", 1), duplicatesEntry(0)),
		"count below list": duplicatesAnswer(duplicatesEntry(0, thirdID), duplicatesEntry(0)),
		"no list":          duplicatesAnswer(`{"totalCount":0}`, duplicatesEntry(0)),
	} {
		t.Run(name, func(t *testing.T) {
			serveWrites(t, http.StatusOK, answer)
			_, err := runMergeTool(t, invokeRecordsDuplicates, `{"object":"person","ids":`+idList(personID, personTwoID)+`}`)
			if classOf(err) != provider.ClassInvalidResponse {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestDuplicatesRefusesBadArgumentsBeforeAnyIO(t *testing.T) {
	refuse(t)
	twenty, twentyOne := []string{}, []string{}
	for i := 0; i < 21; i++ {
		id := "11111111-2222-4333-8444-5555555555" + strconv.Itoa(10+i)
		if i < 20 {
			twenty = append(twenty, id)
		}
		twentyOne = append(twentyOne, id)
	}
	for name, tt := range map[string]struct {
		ids     string
		targets []string
	}{
		"twenty-one ids": {idList(twentyOne...), nil},
		"no ids":         {`[]`, nil},
		"repeated":       {idList(personID, personID), nil},
		"bad uuid":       {idList("nope"), nil},
		"unreachable":    {idList(personID), []string{"object/rocket"}},
	} {
		_, err := invokeRecordsDuplicates(context.Background(), targetConnection(tt.targets...), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(`{"object":"person","ids":`+tt.ids+`}`))
		if !asInvalidOK(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := newRecordMerge(targetConnection(), "find duplicates", json.RawMessage(`{"object":"person","ids":`+idList(twenty...)+`}`), false); err != nil {
		t.Errorf("twenty ids: %v", err)
	}
}

func TestMergeToolsNeverRepeatAfterAnUnclearResult(t *testing.T) {
	handlers := map[string]capability.Handler{
		"duplicates": invokeRecordsDuplicates, "preview": invokeRecordsMergePreview, "merge": invokeRecordsMerge,
	}
	for name, tt := range map[string]struct {
		status int
		fail   error
		body   string
	}{
		"server error":   {status: 500, body: `{"error":"` + bodyCanary + `"}`},
		"unreadable":     {status: 200, body: `not json ` + bodyCanary},
		"timeout":        {fail: &net.DNSError{IsTimeout: true, Err: bodyCanary}},
		"connection end": {fail: errors.New(bodyCanary)},
	} {
		for tool, handler := range handlers {
			t.Run(name+" "+tool, func(t *testing.T) {
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
				args := mergeArgs(idList(personID, personTwoID), 0)
				if tool == "duplicates" {
					args = `{"object":"person","ids":` + idList(personID, personTwoID) + `}`
				}
				_, err := runMergeTool(t, handler, args)
				if err == nil || seen != 1 || strings.Contains(err.Error(), bodyCanary) {
					t.Fatalf("err = %v, requests = %d, want one and no provider text", err, seen)
				}
				var failure *provider.Error
				mayHave := errors.As(err, &failure) && (failure.Class != provider.ClassUnreachable || failure.MayHaveArrived())
				if tool == "merge" && mayHave && !strings.Contains(err.Error(), "may have taken effect") {
					t.Errorf("no uncertainty named: %v", err)
				}
				if tool != "merge" && strings.Contains(err.Error(), "may have taken effect") {
					t.Errorf("a read claims an effect: %v", err)
				}
			})
		}
	}
}

func TestMergePermissionNamesTheRoleNotTheProvider(t *testing.T) {
	serveWrites(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
	_, err := runMergeTool(t, invokeRecordsMerge, mergeArgs(idList(personID, personTwoID), 0))
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "object permissions") ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestMergeDescriptorsDeclareTheirRisk(t *testing.T) {
	read := capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: recordDataSensitivity}
	for _, d := range []capability.Descriptor{recordsDuplicates, recordsMergePreview} {
		if d.Risk != read || d.RequiresToolAllowList {
			t.Errorf("%s = %+v", d.ID, d)
		}
	}
	wantMerge := capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: recordDataSensitivity}
	if recordsMerge.Risk != wantMerge || !recordsMerge.RequiresToolAllowList ||
		!strings.Contains(recordsMerge.Description, "deleted") || !strings.Contains(recordsMerge.Description, "relations") {
		t.Errorf("merge = %+v", recordsMerge)
	}
	for _, d := range []capability.Descriptor{recordsMergePreview, recordsMerge} {
		if strings.Contains(string(d.InputSchema), "dryRun") || strings.Contains(string(d.InputSchema), "dry_run") {
			t.Errorf("%s offers a dry run argument", d.ID)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == recordsMerge.ID || id == recordsMergePreview.ID || id == recordsDuplicates.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestMergeNeedsConfirmationAndTheAllowList(t *testing.T) {
	serveWrites(t, http.StatusOK, mergeAnswer(personID))
	cfg := coreConfig()
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	bare := cfg.Connections["crm"]
	bare.Permissions = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Permissions: all, Tools: []string{recordsMerge.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	request := application.InvokeRequest{Operation: recordsMerge.ID, Connection: "crm",
		Arguments: json.RawMessage(mergeArgs(idList(personID, personTwoID), 0)), Confirmed: true}
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Error("merge was offered without a tools list")
	}
	request.Connection, request.Confirmed = "crm-internal", false
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Error("merge ran without confirmation")
	}
	request.Confirmed = true
	if _, err := core.Invoke(context.Background(), request); err != nil {
		t.Errorf("confirmed merge with a tools list: %v", err)
	}
}
