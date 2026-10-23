package n8n

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const fileList = `[{"file":"workflows/W1.json","id":"W1","name":"Flow","type":"workflow","status":"modified",` +
	`"location":"remote","conflict":true,"publishingError":"PUBERR","contentImportPolicy":{"violations":` +
	`[{"kind":"k","checkId":"c1","message":"POLICYMSG"}],"checkErrors":[]}}]`

func scArgs() map[string]string {
	return map[string]string{
		auditGenerate.ID:       `{}`,
		sourceControlStatus.ID: `{"direction":"pull"}`,
		sourceControlPull.ID:   `{}`,
		sourceControlPush.ID:   `{"commit_message":"m","files":[{"id":"W1","type":"workflow"}]}`,
	}
}

func TestSourceControlToolsSendTheDocumentedRequests(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/audit":
			return jsonResponse(200, `{"Credentials Risk Report":{"risk":"credentials","sections":[{"title":"T",`+
				`"description":"D","recommendation":"R","location":[{"kind":"credential","id":"1","name":"N"}]}]}}`), nil
		case apiPath + "/source-control/status":
			return jsonResponse(200, `{"data":`+fileList+`}`), nil
		case apiPath + "/source-control/pull":
			return jsonResponse(200, fileList), nil
		}
		return jsonResponse(200, `{"data":`+fileList+`}`), nil
	})
	result, err := env.invoke(auditGenerate.ID, "open",
		`{"days_abandoned_workflow":30,"categories":["credentials","nodes"]}`)
	if err != nil || calls[0].method != http.MethodPost || calls[0].path != apiPath+"/audit" ||
		calls[0].body != `{"additionalOptions":{"categories":["credentials","nodes"],"daysAbandonedWorkflow":30}}` ||
		!strings.Contains(result, `"category":"credentials"`) || !strings.Contains(result, `"report_count":1`) {
		t.Fatalf("audit: %s, %v, %+v", result, err, calls)
	}
	if _, err := env.invoke(auditGenerate.ID, "open", `{}`); err != nil || calls[1].body != `{}` {
		t.Fatalf("audit default: %v, %+v", err, calls[1])
	}
	result, err = env.invoke(sourceControlStatus.ID, "open", `{"direction":"push"}`)
	if err != nil || calls[2].method != http.MethodGet || calls[2].path != apiPath+"/source-control/status" ||
		calls[2].query.Get("direction") != "push" || !strings.Contains(result, `"file":"workflows/W1.json"`) ||
		!strings.Contains(result, `"conflict":true`) {
		t.Fatalf("status: %s, %v, %+v", result, err, calls[2])
	}
	result, err = env.confirmed(sourceControlPull.ID, "pdelete-open", `{}`)
	if err != nil || calls[3].method != http.MethodPost || calls[3].path != apiPath+"/source-control/pull" ||
		calls[3].body != `{"autoPublish":"none","force":false}` || !strings.Contains(result, `"outcome":"pulled"`) {
		t.Fatalf("pull: %s, %v, %+v", result, err, calls[3])
	}
	for mode, want := range map[string]string{
		`{"force":true}`:                            `{"autoPublish":"none","force":true}`,
		`{"auto_publish":"all"}`:                    `{"autoPublish":"all","force":false}`,
		`{"auto_publish":"published","force":true}`: `{"autoPublish":"published","force":true}`,
	} {
		before := len(calls)
		if _, err := env.confirmed(sourceControlPull.ID, "pdelete-open", mode); err != nil ||
			len(calls) != before+1 || calls[before].body != want {
			t.Fatalf("pull %s: %v, %+v", mode, err, calls[before:])
		}
	}
	before := len(calls)
	result, err = env.confirmed(sourceControlPush.ID, "pdelete-open",
		`{"commit_message":"line1\nline2","files":[{"id":"W1","type":"workflow"}],"force":true}`)
	if err != nil || len(calls) != before+1 || calls[before].path != apiPath+"/source-control/push" ||
		calls[before].body != `{"commitMessage":"line1\nline2","fileNames":[{"id":"W1","type":"workflow"}],"force":true}` ||
		!strings.Contains(result, `"outcome":"pushed"`) {
		t.Fatalf("push: %s, %v, %+v", result, err, calls[before:])
	}
}

func TestSourceControlProviderTextStaysInTheResultOnly(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, fileList), nil
	})
	result, err := env.confirmed(sourceControlPull.ID, "pdelete-open", `{}`)
	if err != nil || !strings.Contains(result, "PUBERR") || !strings.Contains(result, "POLICYMSG") {
		t.Fatalf("result = %s, %v", result, err)
	}
}

func TestSourceControlToolsAreRefusedLocallyOnATargetedConnection(t *testing.T) {
	for operation, args := range scArgs() {
		for _, connection := range []string{"project", "workflow", "both", "pdelete", "pdelete-workflow"} {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(operation, connection, args)
			if !isInvalidRequest(err) && err == nil || len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("%s on %s: err = %v, calls = %+v", operation, connection, err, calls)
			}
			if err != nil && (strings.Contains(err.Error(), ownProject) || strings.Contains(err.Error(), ownWorkflow)) {
				t.Fatalf("%s on %s leaked a target: %v", operation, connection, err)
			}
		}
	}
}

func TestSourceControlConflictIsAResultAndNotRetried(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/push") {
			return jsonResponse(409, `{"message":"M","conflicts":`+fileList+`}`), nil
		}
		return jsonResponse(409, fileList), nil
	})
	for _, operation := range []string{sourceControlPull.ID, sourceControlPush.ID} {
		calls = nil
		result, err := env.confirmed(operation, "pdelete-open", scArgs()[operation])
		if err != nil || len(calls) != 1 || !strings.Contains(result, `"outcome":"conflict"`) ||
			!strings.Contains(result, `"conflict":true`) || !strings.Contains(result, `"id":"W1"`) {
			t.Fatalf("%s: %s, %v, %d calls", operation, result, err, len(calls))
		}
	}
}

func TestSourceControlUnclearOutcomeIsUncertainWithOneRequest(t *testing.T) {
	for name, handler := range map[string]func(*http.Request) (*http.Response, error){
		"5xx":      func(*http.Request) (*http.Response, error) { return jsonResponse(500, `{}`), nil },
		"abort":    func(*http.Request) (*http.Response, error) { return nil, http.ErrHandlerTimeout },
		"unusable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	} {
		for _, operation := range []string{sourceControlPull.ID, sourceControlPush.ID} {
			var calls []call
			env := newEnvironment(t, &calls, handler)
			_, err := env.confirmed(operation, "pdelete-open", scArgs()[operation])
			if err == nil || len(calls) != 1 || !strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("%s %s: err = %v, calls = %d", name, operation, err, len(calls))
			}
		}
	}
}

func TestSourceControlArgumentsAreValidatedBeforeAnyRequest(t *testing.T) {
	long := strings.Repeat("a", 1001)
	for _, tt := range []struct{ operation, arguments string }{
		{sourceControlStatus.ID, `{}`},
		{sourceControlStatus.ID, `{"direction":"both"}`},
		{sourceControlPull.ID, `{"auto_publish":"everything"}`},
		{sourceControlPull.ID, `{"force":"yes"}`},
		{sourceControlPush.ID, `{"commit_message":"m","files":[]}`},
		{sourceControlPush.ID, `{"commit_message":"` + long + `","files":[{"id":"W1","type":"workflow"}]}`},
		{sourceControlPush.ID, `{"commit_message":"","files":[{"id":"W1","type":"workflow"}]}`},
		{sourceControlPush.ID, `{"commit_message":"a\u0007b","files":[{"id":"W1","type":"workflow"}]}`},
		{sourceControlPush.ID, `{"commit_message":"m","files":[{"id":"W1","type":"file"}]}`},
		{sourceControlPush.ID, `{"commit_message":"m","files":[{"id":"../x","type":"workflow"}]}`},
		{sourceControlPush.ID, `{"commit_message":"m","files":[{"id":"W1","type":"workflow"},{"id":"W1","type":"workflow"}]}`},
		{auditGenerate.ID, `{"categories":["secrets"]}`},
		{auditGenerate.ID, `{"categories":["nodes","nodes"]}`},
		{auditGenerate.ID, `{"days_abandoned_workflow":0}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		if _, err := env.confirmed(tt.operation, "pdelete-open", tt.arguments); err == nil || len(calls) != 0 {
			t.Fatalf("%s %s: err = %v, calls = %d", tt.operation, tt.arguments, err, len(calls))
		}
	}
}

func TestSourceControlChangesRequireConfirmationAndAToolList(t *testing.T) {
	for _, operation := range []string{sourceControlPull.ID, sourceControlPush.ID} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		if _, err := env.invoke(operation, "pdelete-open", scArgs()[operation]); !isConfirmationRequired(err) {
			t.Fatalf("%s without confirm: %v", operation, err)
		}
		if _, err := env.confirmed(operation, "open", scArgs()[operation]); err == nil || len(calls) != 0 {
			t.Fatalf("%s without a tools list: %v, %d calls", operation, err, len(calls))
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == sourceControlPull.ID || tool == sourceControlPush.ID {
				t.Fatalf("profile %s offers %s", profile.ID, tool)
			}
		}
	}
}

func TestSourceControlForbiddenIsNeutral(t *testing.T) {
	for operation, args := range scArgs() {
		var calls []call
		env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
			return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
		})
		_, err := env.confirmed(operation, "pdelete-open", args)
		if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), foreignCanary) ||
			!strings.Contains(err.Error(), "cannot tell which") {
			t.Fatalf("%s: %v", operation, err)
		}
	}
}

const richStatusFile = `{"file":"workflows/W1.json","id":"W1","name":"Flow","type":"workflow","status":"modified",` +
	`"location":"local","conflict":false,"updatedAt":"2026-01-02T00:00:00.000Z","pushed":false,` +
	`"isLocalPublished":true,"isRemoteArchived":false,"parentFolderId":"F1","folderPath":["A","B\u0007"],` +
	`"remoteFolderPath":["A"],"owner":{"type":"team","projectId":"P1","projectName":"Marketing"},` +
	`"publishingError":"PUBERR","publishingErrorDetails":{"reason":"review_pending",` +
	`"workflowReviewRequestId":"RR1","versionId":"V1"},"contentImportPolicy":{"violations":[{"kind":"k",` +
	`"checkId":"c1","message":"POLICYMSG","subject":"S","subjectType":"node","scope":"workflow",` +
	`"matchedRuleId":"R1"}],"checkErrors":[{"checkId":"c2","correlationId":"corr-1"},{"checkId":"c3",` +
	`"correlationId":"corr-2"}]}}`

func TestSourceControlStatusReportsEveryDocumentedMember(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[`+richStatusFile+`,{"file":"x","id":"N","name":"n","type":"tags",`+
			`"status":"new","location":"remote","conflict":false,"updatedAt":"u","parentFolderId":null}]}`), nil
	})
	out, err := env.invoke(sourceControlStatus.ID, "open", `{"direction":"push"}`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Files []json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || len(result.Files) != 2 {
		t.Fatalf("%s, %v", out, err)
	}
	want := `{"id":"W1","name":"Flow","type":"workflow","status":"modified","location":"local",` +
		`"publishing_error":"PUBERR","publishing_error_reason":"review_pending",` +
		`"policy_violations":[{"kind":"k","check_id":"c1","message":"POLICYMSG","subject":"S",` +
		`"subject_type":"node","scope":"workflow","matched_rule_id":"R1"}],"policy_check_errors":2,` +
		`"file":"workflows/W1.json","updated_at":"2026-01-02T00:00:00.000Z","pushed":false,` +
		`"is_local_published":true,"is_remote_archived":false,"parent_folder_id":"F1","folder_path":["A","B "],` +
		`"remote_folder_path":["A"],"owner":{"type":"team","project_id":"P1","project_name":"Marketing"},` +
		`"publishing_error_review_request_id":"RR1","publishing_error_version_id":"V1",` +
		`"check_errors":[{"check_id":"c2","correlation_id":"corr-1"},{"check_id":"c3","correlation_id":"corr-2"}]}`
	var got, wanted map[string]any
	if err := json.Unmarshal(result.Files[0], &got); err != nil || json.Unmarshal([]byte(want), &wanted) != nil ||
		!reflect.DeepEqual(got, wanted) {
		t.Errorf("file = %s\nwant   %s", result.Files[0], want)
	}
	if got := string(result.Files[1]); strings.Contains(got, "parent_folder_id") || strings.Contains(got, "owner") ||
		strings.Contains(got, "pushed") {
		t.Errorf("absent members must stay out: %s", got)
	}
	if seen := assertOutputDeclared(t, sourceControlStatus.ID, []byte(out)); seen < 20 {
		t.Errorf("checked %d members: %s", seen, out)
	}
}

func TestSourceControlPullAndPushOutputIgnoreTheStatusOnlyMembers(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/push") {
			return jsonResponse(200, `{"data":[`+richStatusFile+`]}`), nil
		}
		return jsonResponse(200, `[`+richStatusFile+`]`), nil
	})
	for tool, args := range map[string]string{sourceControlPull.ID: `{}`,
		sourceControlPush.ID: `{"commit_message":"m","files":[{"id":"W1","type":"workflow"}]}`} {
		out, err := env.confirmed(tool, "pdelete-open", args)
		if err != nil {
			t.Fatal(err)
		}
		want := `"files":[{"id":"W1","location":"local","name":"Flow","policy_check_errors":2,` +
			`"policy_violations":[{"check_id":"c1","kind":"k","message":"POLICYMSG"}],"publishing_error":"PUBERR",` +
			`"publishing_error_reason":"review_pending","status":"modified","type":"workflow"}]`
		if !strings.Contains(out, want) {
			t.Errorf("%s: %s", tool, out)
		}
	}
}
