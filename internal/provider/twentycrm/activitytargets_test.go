package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	linkID     = "aaaaaaaa-0000-4000-8000-000000000001"
	noteID     = "bbbbbbbb-0000-4000-8000-000000000002"
	recordID   = "cccccccc-0000-4000-8000-000000000003"
	foreignID  = "dddddddd-0000-4000-8000-000000000004"
	linkFields = `"id":{"type":"string","format":"uuid"},"noteId":{"type":"string","format":"uuid"},` +
		`"note":{"type":"object","oneOf":[{"$ref":"#/components/schemas/NoteForResponse"}]},` +
		`"targetPersonId":{"type":"string","format":"uuid"},` +
		`"targetPerson":{"type":"object","oneOf":[{"$ref":"#/components/schemas/PersonForResponse"}]},` +
		`"targetCompanyId":{"type":"string","format":"uuid"},` +
		`"targetCompany":{"type":"object","oneOf":[{"$ref":"#/components/schemas/CompanyForResponse"}]},` +
		`"rocketId":{"type":"string","format":"uuid"},` +
		`"rocket":{"type":"object","oneOf":[{"$ref":"#/components/schemas/RocketForResponse"}]},` +
		`"memberId":{"type":"string","format":"uuid"},` +
		`"member":{"type":"object","oneOf":[{"$ref":"#/components/schemas/WorkspaceMemberForResponse"}]}`
)

// linkSchema holds note and task with their link objects. A link points to the activity and to person,
// company, the custom object rocket, or the system object workspaceMember.
var linkSchema = func() string {
	object := func(pascal, props string) string {
		return `"` + pascal + `":{"type":"object","properties":{` + props + `}},"` + pascal +
			`ForResponse":{"type":"object","properties":{"id":{"type":"string"}}}`
	}
	task := strings.NewReplacer("noteId", "taskId", `"note"`, `"task"`, "NoteForResponse", "TaskForResponse").Replace(linkFields)
	return `{"paths":{"/notes":{"post":{"operationId":"createOneNote"}},"/tasks":{"post":{"operationId":"createOneTask"}},` +
		`"/noteTargets":{"post":{"operationId":"createOneNoteTarget"}},"/taskTargets":{"post":{"operationId":"createOneTaskTarget"}},` +
		`"/people":{"post":{"operationId":"createOnePerson"}},"/companies":{"post":{"operationId":"createOneCompany"}},` +
		`"/rockets":{"post":{"operationId":"createOneRocket"}},"/workspaceMembers":{"post":{"operationId":"createOneWorkspaceMember"}}},` +
		`"components":{"schemas":{` +
		object("Note", `"title":{"type":"string"}`) + `,` + object("Task", `"title":{"type":"string"}`) + `,` +
		object("Person", `"city":{"type":"string"}`) + `,` + object("Company", `"name":{"type":"string"}`) + `,` +
		object("Rocket", `"name":{"type":"string"}`) + `,` + object("WorkspaceMember", `"locale":{"type":"string"}`) + `,` +
		`"NoteTarget":{"type":"object","properties":{` + linkFields + `}},"NoteTargetForUpdate":{"type":"object","properties":{` + linkFields + `}},` +
		`"NoteTargetForResponse":{"type":"object","properties":{` + linkFields + `}},` +
		`"TaskTarget":{"type":"object","properties":{` + task + `}},"TaskTargetForUpdate":{"type":"object","properties":{` + task + `}},` +
		`"TaskTargetForResponse":{"type":"object","properties":{` + task + `}}}}}`
}()

type linkCall struct {
	Method, Path, Query, Body string
}

// serveLinks serves the schema and answers every other request with respond.
func serveLinks(t *testing.T, respond func(call linkCall) (int, string)) *[]linkCall {
	t.Helper()
	calls := &[]linkCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, linkSchema), nil
		}
		call := linkCall{Method: request.Method, Path: request.URL.Path, Query: request.URL.Query().Encode()}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			call.Body = string(data)
		}
		*calls = append(*calls, call)
		status, body := respond(call)
		return jsonResponse(status, body), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func linkRow(extra string) string {
	return `{"id":"` + linkID + `","noteId":"` + noteID + `",` + extra + `}`
}

func runLinks(t *testing.T, handler func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error),
	args string, targets ...string) (string, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	out, _ := json.Marshal(result)
	return string(out), err
}

func wantInvalid(t *testing.T, err error) {
	t.Helper()
	var invalid *provider.InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want an invalid request", err)
	}
}

func TestActivityTargetDescriptors(t *testing.T) {
	for _, w := range []struct {
		d           capability.Descriptor
		effect      capability.Effect
		idempotency capability.Idempotency
		allow       bool
	}{
		{activitytargetsList, capability.EffectRead, capability.IdempotencySafe, false},
		{activitytargetsCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		{activitytargetsDelete, capability.EffectDelete, capability.IdempotencyIdempotent, true},
	} {
		d := w.d
		if d.Risk.Effect != w.effect || d.Risk.Idempotency != w.idempotency || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != recordDataSensitivity || d.RequiresToolAllowList != w.allow {
			t.Errorf("%s = %+v allow=%v", d.ID, d.Risk, d.RequiresToolAllowList)
		}
		if (w.effect != capability.EffectRead) != (d.Risk.Confirmation == capability.ConfirmationRequired) {
			t.Errorf("%s confirmation = %v", d.ID, d.Risk.Confirmation)
		}
	}
}

func TestActivityTargetCreateSendsOnePostWithDerivedField(t *testing.T) {
	calls := serveLinks(t, func(call linkCall) (int, string) {
		return http.StatusCreated, `{"data":{"createNoteTarget":` + linkRow(`"targetPersonId":"`+recordID+`"`) + `}}`
	})
	out, err := runLinks(t, invokeActivityTargetsCreate,
		`{"activity":"note","activity_id":"`+noteID+`","object":"person","record_id":"`+recordID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, out, `{"id":"`+linkID+`","activity_id":"`+noteID+`","object":"person","record_id":"`+recordID+`"}`)
	got := linkWrites(*calls)
	if len(got) != 1 || got[0].Method != http.MethodPost || got[0].Path != "/rest/noteTargets" || got[0].Query != "depth=0" {
		t.Fatalf("writes = %+v", got)
	}
	sameJSON(t, got[0].Body, `{"noteId":"`+noteID+`","targetPersonId":"`+recordID+`"}`)
	// A custom object is linked through its own relation, found in the schema.
	*calls = nil
	serveLinks(t, func(call linkCall) (int, string) {
		return http.StatusCreated, `{"data":{"createTaskTarget":{"id":"` + linkID + `","taskId":"` + noteID + `","rocketId":"` + recordID + `"}}}`
	})
	if _, err := runLinks(t, invokeActivityTargetsCreate,
		`{"activity":"task","activity_id":"`+noteID+`","object":"rocket","record_id":"`+recordID+`"}`); err != nil {
		t.Fatal(err)
	}
}

func TestActivityTargetRefusalsPrecedeSecretAndRequests(t *testing.T) {
	refuse(t)
	valid := `"activity_id":"` + noteID + `","record_id":"` + recordID + `"`
	for name, tc := range map[string]struct {
		args    string
		targets []string
	}{
		"note outside targets":   {`{"activity":"note",` + valid + `,"object":"person"}`, []string{"object/person"}},
		"object outside targets": {`{"activity":"note",` + valid + `,"object":"company"}`, []string{"object/note", "object/person"}},
		"system object":          {`{"activity":"note",` + valid + `,"object":"workspaceMember"}`, nil},
		"link object itself":     {`{"activity":"note",` + valid + `,"object":"noteTarget"}`, nil},
		"unknown activity":       {`{"activity":"message",` + valid + `,"object":"person"}`, nil},
		"bad uuid":               {`{"activity":"note","activity_id":"x","record_id":"` + recordID + `","object":"person"}`, nil},
		"missing record":         {`{"activity":"note","activity_id":"` + noteID + `","object":"person"}`, nil},
	} {
		// The environment variable of the key is not set, so a secret access would fail differently.
		if _, err := runLinks(t, invokeActivityTargetsCreate, tc.args, tc.targets...); err == nil {
			t.Errorf("%s: no error", name)
		} else {
			wantInvalid(t, err)
		}
	}
}

func TestActivityTargetCreateRefusesObjectWithoutRelation(t *testing.T) {
	calls := serveLinks(t, func(call linkCall) (int, string) { return http.StatusOK, `{}` })
	_, err := runLinks(t, invokeActivityTargetsCreate,
		`{"activity":"note","activity_id":"`+noteID+`","object":"task","record_id":"`+recordID+`"}`)
	wantInvalid(t, err)
	if len(linkWrites(*calls)) != 0 {
		t.Errorf("writes = %+v", *calls)
	}
}

func TestActivityTargetNoRetryAndAnswerCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"server error": {http.StatusInternalServerError, `{"error":"canary"}`},
		"other link":   {http.StatusCreated, `{"data":{"createNoteTarget":` + linkRow(`"targetPersonId":"`+foreignID+`"`) + `}}`},
		"unreadable":   {http.StatusCreated, `not json`},
	} {
		calls := serveLinks(t, func(call linkCall) (int, string) { return tc.status, tc.body })
		_, err := runLinks(t, invokeActivityTargetsCreate,
			`{"activity":"note","activity_id":"`+noteID+`","object":"person","record_id":"`+recordID+`"}`)
		if err == nil || !strings.Contains(err.Error(), "repeating the create can add a duplicate") ||
			strings.Contains(err.Error(), "canary") {
			t.Errorf("%s: error = %v", name, err)
		}
		if len(linkWrites(*calls)) != 1 {
			t.Errorf("%s: writes = %+v", name, *calls)
		}
	}
}

func linkWrites(calls []linkCall) []linkCall {
	var result []linkCall
	for _, c := range calls {
		if c.Method != http.MethodGet {
			result = append(result, c)
		}
	}
	return result
}

func TestActivityTargetListFiltersAndCountsForeignLinks(t *testing.T) {
	page := `{"data":{"noteTargets":[` + linkRow(`"targetPersonId":"`+recordID+`"`) + `,` +
		`{"id":"` + foreignID + `","noteId":"` + noteID + `","memberId":"` + recordID + `"},` +
		`{"id":"` + foreignID + `","noteId":"` + noteID + `","targetCompanyId":"` + recordID + `"}]},` +
		`"pageInfo":{"hasNextPage":true,"endCursor":"abc"}}`
	calls := serveLinks(t, func(call linkCall) (int, string) { return http.StatusOK, page })
	out, err := runLinks(t, invokeActivityTargetsList, `{"activity":"note","activity_id":"`+noteID+`"}`, "object/note", "object/person")
	if err != nil {
		t.Fatal(err)
	}
	var list ActivityTargetList
	if json.Unmarshal([]byte(out), &list) != nil || len(list.Links) != 1 || list.Omitted != 2 || !list.HasMore ||
		list.NextCursor == "" || list.Links[0].Object != "person" || strings.Contains(out, "member") {
		t.Fatalf("list = %s", out)
	}
	if len(*calls) != 1 || (*calls)[0].Method != http.MethodGet || (*calls)[0].Path != "/rest/noteTargets" {
		t.Fatalf("calls = %+v", *calls)
	}
	query, _ := url.ParseQuery((*calls)[0].Query)
	if query.Get("filter") != "noteId[eq]:"+noteID || query.Get("depth") != "0" || query.Get("limit") != "25" {
		t.Errorf("query = %v", query)
	}
	// The cursor continues the same list and no other one.
	args := `{"activity":"note","activity_id":"` + noteID + `","cursor":"` + list.NextCursor + `"}`
	if _, err := runLinks(t, invokeActivityTargetsList, args); err != nil {
		t.Fatal(err)
	}
	query, _ = url.ParseQuery((*calls)[1].Query)
	if query.Get("starting_after") != "abc" {
		t.Errorf("query = %v", query)
	}
	_, err = runLinks(t, invokeActivityTargetsList, `{"activity":"task","activity_id":"`+noteID+`","cursor":"`+list.NextCursor+`"}`)
	wantInvalid(t, err)
}

func TestActivityTargetListByRecord(t *testing.T) {
	calls := serveLinks(t, func(call linkCall) (int, string) {
		return http.StatusOK, `{"data":{"taskTargets":[]},"pageInfo":{"hasNextPage":false}}`
	})
	out, err := runLinks(t, invokeActivityTargetsList, `{"activity":"task","object":"rocket","record_id":"`+recordID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, out, `{"links":[],"omitted":0,"has_more":false}`)
	query, _ := url.ParseQuery((*calls)[0].Query)
	if (*calls)[0].Path != "/rest/taskTargets" || query.Get("filter") != "rocketId[eq]:"+recordID {
		t.Errorf("call = %+v", (*calls)[0])
	}
	for _, args := range []string{
		`{"activity":"task"}`,
		`{"activity":"task","activity_id":"` + noteID + `","object":"rocket","record_id":"` + recordID + `"}`,
		`{"activity":"task","object":"rocket"}`,
		`{"activity":"task","object":"workspaceMember","record_id":"` + recordID + `"}`,
	} {
		_, err := runLinks(t, invokeActivityTargetsList, args)
		wantInvalid(t, err)
	}
}

func TestActivityTargetDeleteReadsFirstAndChecksBothSides(t *testing.T) {
	row := linkRow(`"targetPersonId":"` + recordID + `"`)
	calls := serveLinks(t, func(call linkCall) (int, string) {
		if call.Method == http.MethodDelete {
			return http.StatusOK, `{"data":{"deleteNoteTarget":{"id":"` + linkID + `"}}}`
		}
		return http.StatusOK, `{"data":{"noteTarget":` + row + `}}`
	})
	out, err := runLinks(t, invokeActivityTargetsDelete, `{"activity":"note","id":"`+linkID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, out, `{"deleted":true}`)
	if len(*calls) != 2 || (*calls)[0].Method != http.MethodGet || (*calls)[0].Query != "depth=0" ||
		(*calls)[1].Method != http.MethodDelete || (*calls)[1].Query != "" || (*calls)[1].Body != "" ||
		(*calls)[0].Path != "/rest/noteTargets/"+linkID || (*calls)[1].Path != "/rest/noteTargets/"+linkID {
		t.Fatalf("calls = %+v", *calls)
	}
	// A link to an object outside the targets, to a system object, or to nothing is not removed.
	for name, row := range map[string]string{
		"outside targets": linkRow(`"targetCompanyId":"` + recordID + `"`),
		"system object":   linkRow(`"memberId":"` + recordID + `"`),
		"no target":       linkRow(`"targetPersonId":null`),
		"two targets":     linkRow(`"targetPersonId":"` + recordID + `","rocketId":"` + recordID + `"`),
	} {
		row := row
		calls := serveLinks(t, func(call linkCall) (int, string) {
			return http.StatusOK, `{"data":{"noteTarget":` + row + `}}`
		})
		_, err := runLinks(t, invokeActivityTargetsDelete, `{"activity":"note","id":"`+linkID+`"}`, "object/note", "object/person", "object/rocket")
		wantInvalid(t, err)
		if countMethod(*calls, http.MethodDelete) != 0 {
			t.Errorf("%s: calls = %+v", name, *calls)
		}
	}
}

func countMethod(calls []linkCall, method string) int {
	n := 0
	for _, c := range calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func TestActivityTargetDeleteDoesNotRetry(t *testing.T) {
	row := linkRow(`"targetPersonId":"` + recordID + `"`)
	calls := serveLinks(t, func(call linkCall) (int, string) {
		if call.Method == http.MethodDelete {
			return http.StatusInternalServerError, `{"error":"canary"}`
		}
		return http.StatusOK, `{"data":{"noteTarget":` + row + `}}`
	})
	_, err := runLinks(t, invokeActivityTargetsDelete, `{"activity":"note","id":"`+linkID+`"}`)
	if err == nil || !strings.Contains(err.Error(), "may have been removed") || strings.Contains(err.Error(), "canary") {
		t.Errorf("error = %v", err)
	}
	if countMethod(*calls, http.MethodDelete) != 1 {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestActivityTargetDeleteRefusesBeforeSecretOrRequest(t *testing.T) {
	refuse(t)
	for _, args := range []string{
		`{"activity":"note","id":"x"}`,
		`{"activity":"note"}`,
		`{"activity":"note","id":"` + linkID + `","record_id":"` + recordID + `"}`,
	} {
		_, err := runLinks(t, invokeActivityTargetsDelete, args)
		wantInvalid(t, err)
	}
	_, err := runLinks(t, invokeActivityTargetsDelete, `{"activity":"note","id":"`+linkID+`"}`, "object/person")
	wantInvalid(t, err)
}
