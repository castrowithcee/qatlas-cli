package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	coreWfID      = "aaaaaaaa-5555-4000-8000-000000000001"
	coreVersionID = "aaaaaaaa-5555-4000-8000-000000000002"
	bigPosition   = "9007199254740993"
	urlCanary     = "canary-signed-url-8801"
)

const (
	actorAda = `{"source":"MANUAL","workspaceMemberId":"` + memberID + `","name":"Ada\u0007 Lovelace","context":{}}`
	actorAPI = `{"source":"API","workspaceMemberId":null,"name":"","context":{"key":"value"}}`
)

// servePaths answers each path with its body and records the requests.
func servePaths(t *testing.T, bodies map[string]string) *[]wfCall {
	t.Helper()
	calls := &[]wfCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, wfCall{request.URL.Path, request.URL.RawQuery})
		body, ok := bodies[request.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s", request.URL.Path)
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
		return jsonResponse(http.StatusOK, body), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func wantOutput(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("output differs\n got: %s\nwant: %s", got, want)
	}
}

const fullWorkflow = `{"id":"` + wfID + `","name":"Onboarding","position":` + bigPosition + `,"coreWorkflowId":"` + coreWfID +
	`","statuses":["ACTIVE"],"lastPublishedVersionId":"` + wfVersion + `","createdBy":` + actorAda + `,"updatedBy":` + actorAPI +
	`,"createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z","deletedAt":null}`

const wantWorkflow = `{"id":"` + wfID + `","name":"Onboarding","position":` + bigPosition + `,"core_workflow_id":"` + coreWfID +
	`","statuses":["ACTIVE"],"last_published_version_id":"` + wfVersion +
	`","created_by":{"name":"Ada Lovelace","source":"MANUAL","workspace_member_id":"` + memberID + `"},` +
	`"updated_by":{"context":{"key":"value"},"source":"API"},` +
	`"created_at":"2026-01-01T00:00:00.000Z","updated_at":"2026-01-02T00:00:00.000Z"}`

func TestWorkflowsListReportsOrderOriginAndCoreIdentifier(t *testing.T) {
	servePaths(t, map[string]string{workflowsPath: `{"data":{"workflows":[` + fullWorkflow + `]},"pageInfo":{"hasNextPage":false}}`})
	out, err := runWorkflowTool(t, invokeWorkflowsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, `{"workflows":[`+wantWorkflow+`],"has_more":false}`)
}

func TestWorkflowsGetReportsWorkflowAndVersionFields(t *testing.T) {
	versions := `{"id":"` + wfVersion + `","workflowId":"` + wfID + `","coreWorkflowVersionId":"` + coreVersionID +
		`","position":3,"name":"v1","status":"ACTIVE","trigger":{"type":"MANUAL","settings":{"token":"` + settingsCn + `"}},` +
		`"steps":[{"type":"CODE","settings":{"x":"` + settingsCn + `"}}],"createdAt":"2026-01-01T00:00:00.000Z",` +
		`"updatedAt":"2026-01-02T00:00:00.000Z"}`
	servePaths(t, map[string]string{
		workflowsPath + "/" + wfID: `{"data":{"workflow":` + fullWorkflow + `}}`,
		workflowVersionsPath:       `{"data":{"workflowVersions":[` + versions + `]},"pageInfo":{"hasNextPage":false}}`,
	})
	out, err := runWorkflowTool(t, invokeWorkflowsGet, `{"id":"`+wfID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, strings.TrimSuffix(wantWorkflow, "}")+`,"versions":[{"id":"`+wfVersion+`","name":"v1","position":3,`+
		`"workflow_id":"`+wfID+`","core_workflow_version_id":"`+coreVersionID+`","status":"ACTIVE","trigger_type":"MANUAL",`+
		`"step_types":["CODE"],"step_count":1,"created_at":"2026-01-01T00:00:00.000Z","updated_at":"2026-01-02T00:00:00.000Z"}],`+
		`"has_more_versions":false}`)
}

func TestWorkflowRunsReportFullFieldsAndNoRunData(t *testing.T) {
	run := `{"id":"` + wfRun + `","workflowId":"` + wfID + `","coreWorkflowId":"` + coreWfID + `","workflowVersionId":"` + wfVersion +
		`","coreWorkflowVersionId":"` + coreVersionID + `","position":2,"name":"Onboarding","status":"FAILED",` +
		`"createdBy":` + actorAda + `,"updatedBy":` + actorAPI + `,"output":{"e":"` + outputCn + `"},"context":{"c":"` + outputCn +
		`"},"state":{"s":"` + outputCn + `"},"enqueuedAt":"2026-01-01T00:00:00.000Z","startedAt":"2026-01-01T00:00:01.000Z",` +
		`"endedAt":"2026-01-01T00:00:02.000Z","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-01T00:00:03.000Z"}`
	calls := servePaths(t, map[string]string{workflowRunsPath: `{"data":{"workflowRuns":[` + run + `]},"pageInfo":{"hasNextPage":false}}`})
	out, err := runWorkflowTool(t, invokeWorkflowRunsList, `{"workflow_id":"`+wfID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, `{"runs":[{"id":"`+wfRun+`","name":"Onboarding","position":2,"status":"FAILED","workflow_id":"`+wfID+
		`","core_workflow_id":"`+coreWfID+`","workflow_version_id":"`+wfVersion+`","core_workflow_version_id":"`+coreVersionID+
		`","created_by":{"name":"Ada Lovelace","source":"MANUAL","workspace_member_id":"`+memberID+`"},`+
		`"updated_by":{"context":{"key":"value"},"source":"API"},"enqueued_at":"2026-01-01T00:00:00.000Z",`+
		`"started_at":"2026-01-01T00:00:01.000Z","ended_at":"2026-01-01T00:00:02.000Z","created_at":"2026-01-01T00:00:00.000Z",`+
		`"updated_at":"2026-01-01T00:00:03.000Z"}],"has_more":false}`)
	query, _ := url.ParseQuery((*calls)[0].Query)
	for _, banned := range []string{"output", "context", "state"} {
		for _, name := range strings.Split(query.Get("fields"), ",") {
			if name == banned {
				t.Errorf("fields ask for %s: %s", banned, query.Get("fields"))
			}
		}
	}
}

func TestOutputWithoutNewFieldsStaysUnchanged(t *testing.T) {
	servePaths(t, map[string]string{
		workflowsPath: `{"data":{"workflows":[{"id":"` + wfID + `","name":"Onboarding","statuses":["ACTIVE"],` +
			`"lastPublishedVersionId":"` + wfVersion + `","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z"}]},` +
			`"pageInfo":{"hasNextPage":false}}`,
		workflowRunsPath: `{"data":{"workflowRuns":[{"id":"` + wfRun + `","workflowId":"` + wfID + `","workflowVersionId":"` + wfVersion +
			`","name":"Onboarding","status":"FAILED","createdAt":"2026-01-01T00:00:00.000Z"}]},"pageInfo":{"hasNextPage":false}}`,
		membersPath: `{"data":{"workspaceMembers":[{"id":"` + memberID + `","name":{"firstName":"Ada","lastName":"L"},` +
			`"userEmail":"ada@example.test","timeZone":"Europe/Zurich","locale":"de-CH","avatarUrl":"` + urlCanary + `"}]},` +
			`"pageInfo":{"hasNextPage":false}}`,
	})
	out, err := runWorkflowTool(t, invokeWorkflowsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, `{"workflows":[{"id":"`+wfID+`","name":"Onboarding","statuses":["ACTIVE"],"last_published_version_id":"`+wfVersion+
		`","created_at":"2026-01-01T00:00:00.000Z","updated_at":"2026-01-02T00:00:00.000Z"}],"has_more":false}`)
	out, err = runWorkflowTool(t, invokeWorkflowRunsList, `{"workflow_id":"`+wfID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, `{"runs":[{"id":"`+wfRun+`","name":"Onboarding","status":"FAILED","workflow_id":"`+wfID+
		`","workflow_version_id":"`+wfVersion+`","created_at":"2026-01-01T00:00:00.000Z"}],"has_more":false}`)
	out, err = runMemberTool(t, invokeMembersList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, `{"members":[{"id":"`+memberID+`","name":"Ada L","email":"ada@example.test","time_zone":"Europe/Zurich",`+
		`"locale":"de-CH"}],"has_more":false}`)
}

func TestWorkflowNameFilterIsMaskedAndBoundToTheCursor(t *testing.T) {
	calls := servePaths(t, map[string]string{workflowsPath: `{"data":{"workflows":[]},"pageInfo":{"hasNextPage":true,"endCursor":"cur1"}}`})
	out, err := runWorkflowTool(t, invokeWorkflowsList, `{"name":"a%b_c\"d"}`)
	if err != nil {
		t.Fatal(err)
	}
	query, _ := url.ParseQuery((*calls)[0].Query)
	if want := `name[ilike]:"%a\%b\_c\"d%"`; query.Get("filter") != want {
		t.Errorf("filter = %q, want %q", query.Get("filter"), want)
	}
	var page WorkflowList
	if err := json.Unmarshal([]byte(out), &page); err != nil || page.NextCursor == "" {
		t.Fatal(out, err)
	}
	if _, err := runWorkflowTool(t, invokeWorkflowsList, `{"name":"a%b_c\"d","cursor":"`+page.NextCursor+`"}`); err != nil {
		t.Errorf("same filter: %v", err)
	}
	for _, args := range []string{`{"name":"other","cursor":"` + page.NextCursor + `"}`, `{"cursor":"` + page.NextCursor + `"}`} {
		if _, err := runWorkflowTool(t, invokeWorkflowsList, args); err == nil {
			t.Errorf("cursor accepted for %s", args)
		}
	}
}

func TestWorkflowNameFilterRefusesBadValuesBeforeAnyRequest(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, name := range []string{`"   "`, `"` + strings.Repeat("a", 101) + `"`, `"a\u0007b"`, `"a​b"`, `"a\\b"`, `"a\nb"`} {
		args := `{"name":` + name + `}`
		if _, err := invokeWorkflowsList(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args)); err == nil || classOf(err) != "" {
			t.Errorf("%s: err = %v", args, err)
		}
	}
}

func TestWorkflowRunsFilterByVersionAndBindTheCursor(t *testing.T) {
	calls := servePaths(t, map[string]string{workflowRunsPath: `{"data":{"workflowRuns":[]},"pageInfo":{"hasNextPage":true,"endCursor":"cur1"}}`})
	args := `{"workflow_id":"` + wfID + `","workflow_version_id":"` + wfVersion + `","status":"FAILED"`
	out, err := runWorkflowTool(t, invokeWorkflowRunsList, args+`}`)
	if err != nil {
		t.Fatal(err)
	}
	query, _ := url.ParseQuery((*calls)[0].Query)
	if want := "workflowId[eq]:" + wfID + ",workflowVersionId[eq]:" + wfVersion + ",status[eq]:FAILED"; query.Get("filter") != want {
		t.Errorf("filter = %q, want %q", query.Get("filter"), want)
	}
	var page WorkflowRunList
	if err := json.Unmarshal([]byte(out), &page); err != nil || page.NextCursor == "" {
		t.Fatal(out, err)
	}
	if _, err := runWorkflowTool(t, invokeWorkflowRunsList, args+`,"cursor":"`+page.NextCursor+`"}`); err != nil {
		t.Errorf("same filter: %v", err)
	}
	other := `{"workflow_id":"` + wfID + `","workflow_version_id":"44444444-0000-4000-8000-000000000004","status":"FAILED"`
	for _, rejected := range []string{other, `{"workflow_id":"` + wfID + `","status":"FAILED"`} {
		if _, err := runWorkflowTool(t, invokeWorkflowRunsList, rejected+`,"cursor":"`+page.NextCursor+`"}`); err == nil {
			t.Errorf("cursor accepted for %s", rejected)
		}
	}
}

func TestWorkflowRunVersionFilterRefusesBadValuesBeforeAnyRequest(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, version := range []string{"not-a-uuid", wfVersion + ",status[eq]:x", "../x"} {
		args := `{"workflow_id":"` + wfID + `","workflow_version_id":"` + version + `"}`
		if _, err := invokeWorkflowRunsList(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args)); err == nil || classOf(err) != "" {
			t.Errorf("%s: err = %v", args, err)
		}
	}
}

const fullMember = `{"id":"` + memberID + `","name":{"firstName":"Ada","lastName":"L"},"userEmail":"ada@example.test",` +
	`"timeZone":"Europe/Zurich","locale":"de-CH","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z",` +
	`"position":` + bigPosition + `,"jobTitle":"Engineer\u0007 (Lead)","colorScheme":"Dark","uiScale":1.25,"openRecordIn":"SIDE_PANEL",` +
	`"calendarStartDay":1,"dateFormat":"DAY_FIRST","timeFormat":"HOUR_24","numberFormat":"COMMAS_AND_DOT",` +
	`"avatarUrl":"` + avatarCn + `","userId":"` + userIDCn + `"}`

const wantMember = `{"id":"` + memberID + `","name":"Ada L","email":"ada@example.test","job_title":"Engineer (Lead)",` +
	`"time_zone":"Europe/Zurich","locale":"de-CH","color_scheme":"Dark","ui_scale":1.25,"open_record_in":"SIDE_PANEL",` +
	`"calendar_start_day":1,"date_format":"DAY_FIRST","time_format":"HOUR_24","number_format":"COMMAS_AND_DOT",` +
	`"position":` + bigPosition + `,"created_at":"2026-01-01T00:00:00.000Z","updated_at":"2026-01-02T00:00:00.000Z"}`

func TestMembersReportPreferencesAndTimestampsButNeitherAvatarNorUserID(t *testing.T) {
	calls := servePaths(t, map[string]string{
		membersPath:                  `{"data":{"workspaceMembers":[` + fullMember + `]},"pageInfo":{"hasNextPage":false}}`,
		membersPath + "/" + memberID: `{"data":{"workspaceMember":` + fullMember + `}}`,
	})
	out, err := runMemberTool(t, invokeMembersList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, `{"members":[`+wantMember+`],"has_more":false}`)
	out, err = runMemberTool(t, invokeMembersGet, `{"id":"`+memberID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, wantMember)
	query, _ := url.ParseQuery((*calls)[0].Query)
	for _, name := range strings.Split(query.Get("fields"), ",") {
		if name == "avatarUrl" || name == "userId" {
			t.Errorf("fields ask for %s", name)
		}
	}
}

func TestMemberSettingsOutsideTheirShapeAreLeftOut(t *testing.T) {
	servePaths(t, map[string]string{membersPath: `{"data":{"workspaceMembers":[{"id":"` + memberID + `","colorScheme":"<script>",` +
		`"uiScale":"1","calendarStartDay":null,"dateFormat":"","position":"abc","createdAt":"yesterday"}]},"pageInfo":{"hasNextPage":false}}`})
	out, err := runMemberTool(t, invokeMembersList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, out, `{"members":[{"id":"`+memberID+`"}],"has_more":false}`)
}

func TestActivityTargetListReportsTimestamps(t *testing.T) {
	page := `{"data":{"noteTargets":[` + linkRow(`"targetPersonId":"`+recordID+`","createdAt":"2026-01-01T00:00:00.000Z",`+
		`"updatedAt":"2026-01-02T00:00:00.000Z"`) + `,` + linkRow(`"targetPersonId":"`+recordID+`"`) + `]},"pageInfo":{"hasNextPage":false}}`
	serveLinks(t, func(call linkCall) (int, string) { return http.StatusOK, page })
	out, err := runLinks(t, invokeActivityTargetsList, `{"activity":"note","activity_id":"`+noteID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	link := `{"id":"` + linkID + `","activity_id":"` + noteID + `","object":"person","record_id":"` + recordID + `"`
	wantOutput(t, out, `{"links":[`+link+`,"created_at":"2026-01-01T00:00:00.000Z","updated_at":"2026-01-02T00:00:00.000Z"},`+
		link+`}],"omitted":0,"has_more":false}`)
}

// withActors adds the origin and the deprecated or address-bearing members of the source to an attachment row.
func withActors(row string) string {
	return strings.Replace(row, `"createdAt"`, `"createdBy":`+actorAda+`,"updatedBy":`+actorAPI+
		`,"name":"`+urlCanary+`","fullPath":"`+urlCanary+`","type":"`+urlCanary+`","fileCategory":"`+urlCanary+
		`","author":{"id":"`+urlCanary+`"},"createdAt"`, 1)
}

func TestAttachmentsReportOriginAndNoDeprecatedMembers(t *testing.T) {
	wantActors := `"created_by":{"name":"Ada Lovelace","source":"MANUAL","workspace_member_id":"` + memberID + `"},` +
		`"updated_by":{"context":{"key":"value"},"source":"API"}`
	fake := okFake("data")
	fake.row = withActors(fake.row)
	serveAttachments(t, fake)
	out, err := runAttachment(t, t.Context(), targetConnection(), false, `{"object":"person","record_id":"`+attPersonID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, wantActors) || strings.Contains(out, urlCanary) || strings.Contains(out, fileToken) {
		t.Errorf("list = %s", out)
	}
	dir := t.TempDir()
	out, err = runAttachment(t, t.Context(), filesConnection(dir), true, getArgs(``))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, wantActors) || strings.Contains(out, urlCanary) || strings.Contains(out, fileToken) ||
		strings.Contains(out, "url") {
		t.Errorf("get = %s", out)
	}
}
