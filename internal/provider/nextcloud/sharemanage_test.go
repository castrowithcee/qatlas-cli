package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

type shareTool func(context.Context, *redact.Redactor, json.RawMessage) (any, error)

func createShareTool(ctx context.Context, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSharesCreate(ctx, reportsConnection(), resolver(red), red, raw)
}

func updateShareTool(ctx context.Context, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSharesUpdate(ctx, reportsConnection(), resolver(red), red, raw)
}

func deleteShareTool(ctx context.Context, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeSharesDelete(ctx, reportsConnection(), resolver(red), red, raw)
}

func runShare(tool shareTool, args any) (any, error) {
	raw, _ := json.Marshal(args)
	return tool(capability.WithConfirmed(context.Background()), &redact.Redactor{}, raw)
}

type obj = map[string]any

func answerShare(id, shareType, path string, perms string) string {
	return ocsData(shareJSON(id, shareType, aliceUser, path, path, "bob", perms, ""))
}

// shareServer answers the pre-read of share 7 with the given type and path and every change with the given
// response.
func shareServer(t *testing.T, shareType, path string, change func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return ocsResponse(200, ocsData(`[`+shareJSON("7", shareType, aliceUser, path, path, "bob", "3", "")+`]`)), nil
		}
		return change(r)
	})
}

func changeOK(r *http.Request) (*http.Response, error) {
	return ocsResponse(200, ocsData(shareJSON("7", "0", aliceUser, "/Reports/a.txt", "/a.txt", "bob", "3", ""))), nil
}

func formOf(t *testing.T, c call) url.Values {
	t.Helper()
	values, err := url.ParseQuery(c.body)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func TestSharesCreateBuildsTheMaskFromTheFlags(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		args := obj{"type": "user", "path": "2026/q1.pdf", "share_with": "bob"}
		want := permRead
		for i, name := range []string{"update", "create", "delete", "share"} {
			set := mask&(1<<i) != 0
			args[name] = set
			if set {
				want |= []int{permUpdate, permCreate, permDelete, permShare}[i]
			}
		}
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			return ocsResponse(200, answerShare("7", "0", "/Reports/2026/q1.pdf", "1")), nil
		})
		if _, err := runShare(createShareTool, args); err != nil {
			t.Fatalf("mask %d: %v", mask, err)
		}
		if len(*calls) != 1 || formOf(t, (*calls)[0]).Get("permissions") != itoa(want) {
			t.Errorf("mask %d: calls = %+v, want %d", mask, *calls, want)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestSharesCreateSendsOneFixedPost(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, answerShare("7", "1", "/Reports/2026", "1")), nil
	})
	result, err := runShare(createShareTool, obj{"type": "group", "path": "2026", "share_with": "team a",
		"expires_at": "2027-01-31", "note": "hello"})
	if err != nil || len(*calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(*calls))
	}
	request := (*calls)[0]
	form := formOf(t, request)
	if request.method != http.MethodPost || request.url.Path != "/ocs/v2.php/apps/files_sharing/api/v1/shares" ||
		request.auth != basicAuth(aliceUser, aliceToken) || form.Get("shareType") != "1" ||
		form.Get("path") != "/Reports/2026" || form.Get("shareWith") != "team a" || form.Get("expireDate") != "2027-01-31" ||
		form.Get("note") != "hello" || form.Get("permissions") != "1" || len(form) != 6 {
		t.Errorf("request = %+v, form = %v", request, form)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), `"created":true`) || !strings.Contains(string(encoded), `"id":"7"`) {
		t.Errorf("result = %s", encoded)
	}
}

func TestSharesCreateRefusesLocallyBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return status(500), nil })
	for _, args := range []obj{
		{"type": "link", "path": "a", "share_with": "x"},
		{"type": "email", "path": "a", "share_with": "x@example.invalid"},
		{"type": "federated", "path": "a", "share_with": "x@remote"},
		{"type": "team", "path": "a", "share_with": "x"},
		{"type": "talk", "path": "a", "share_with": "x"},
		{"type": "user", "path": "../ForeignAudit", "share_with": "x"},
		{"type": "user", "path": "/ForeignAudit/x", "share_with": "x"},
		{"type": "user", "path": "https://foreign.example.invalid/x", "share_with": "x"},
		{"type": "user", "path": "", "share_with": "x"},
		{"type": "user", "path": "a", "share_with": ""},
		{"type": "user", "path": "a", "share_with": "x", "expires_at": "tomorrow"},
		{"type": "user", "path": "a", "share_with": "x", "sendMail": true},
		{"type": "user", "path": "a", "share_with": "x", "share_id": "1"},
	} {
		_, err := runShare(createShareTool, args)
		if err == nil || strings.Contains(err.Error(), "ForeignAudit") || strings.Contains(err.Error(), "foreign.example") {
			t.Errorf("args %v: err = %v", args, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %v", *calls)
	}
}

func TestSharesCreateSchemaOnlyAllowsUserAndGroup(t *testing.T) {
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(sharesCreate.InputSchema, &schema); err != nil ||
		strings.Join(schema.Properties.Type.Enum, ",") != "user,group" {
		t.Errorf("schema = %s, err = %v", sharesCreate.InputSchema, err)
	}
}

func TestSharesUpdateMergesRightsAndSendsOnePut(t *testing.T) {
	calls := shareServer(t, "0", "/Reports/a.txt", changeOK)
	// The share has read and update (3); share=true adds 16, update=false drops 2.
	if _, err := runShare(updateShareTool, obj{"share_id": "7", "share": true, "update": false, "note": "", "expires_at": "2027-02-01"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].method != http.MethodGet || (*calls)[1].method != http.MethodPut ||
		(*calls)[1].url.Path != "/ocs/v2.php/apps/files_sharing/api/v1/shares/7" {
		t.Fatalf("calls = %+v", *calls)
	}
	form := formOf(t, (*calls)[1])
	if form.Get("permissions") != "17" || form.Get("expireDate") != "2027-02-01" || len(form["note"]) != 1 || form.Get("note") != "" || len(form) != 3 {
		t.Errorf("form = %v", form)
	}
}

func TestSharesUpdateAndDeleteRefuseAfterTheReadWithoutAChange(t *testing.T) {
	for _, shareType := range []string{"3", "4", "6", "7", "10", "9", "99"} {
		calls := shareServer(t, shareType, "/Reports/a.txt", changeOK)
		_, err := runShare(updateShareTool, obj{"share_id": "7", "update": true})
		if err == nil || len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
			t.Errorf("type %s: err = %v, calls = %v", shareType, err, *calls)
		}
		calls = shareServer(t, shareType, "/Reports/a.txt", func(*http.Request) (*http.Response, error) {
			return ocsResponse(200, ocsData(`[]`)), nil
		})
		if _, err := runShare(deleteShareTool, obj{"share_id": "7"}); err != nil ||
			len(*calls) != 2 || (*calls)[1].method != http.MethodDelete {
			t.Errorf("delete type %s: err = %v, calls = %v", shareType, err, *calls)
		}
	}
}

func TestSharesUpdateAndDeleteRefuseForeignSharesWithoutAChange(t *testing.T) {
	for _, tool := range []struct {
		run  shareTool
		args obj
	}{{updateShareTool, obj{"share_id": "7", "update": true}}, {deleteShareTool, obj{"share_id": "7"}}} {
		for _, path := range []string{"/ForeignAudit/a.txt", "/ReportsX/a.txt"} {
			calls := shareServer(t, "0", path, changeOK)
			_, err := runShare(tool.run, tool.args)
			if err == nil || strings.Contains(err.Error(), "ForeignAudit") || strings.Contains(err.Error(), "ReportsX") ||
				len(*calls) != 1 {
				t.Errorf("path %s: err = %v, calls = %v", path, err, *calls)
			}
		}
		// A share made to the identity is not one it can change or revoke.
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			return ocsResponse(200, ocsData(`[`+shareJSON("7", "0", "dave", "/Elsewhere/a.txt", "/Reports/a.txt", "alice", "1", "")+`]`)), nil
		})
		if _, err := runShare(tool.run, tool.args); err == nil || len(*calls) != 1 {
			t.Errorf("incoming share: err = %v, calls = %v", err, *calls)
		}
		// The answer of another share than the one asked for is not acted on.
		calls = serve(t, func(*http.Request) (*http.Response, error) {
			return ocsResponse(200, answerShare("8", "0", "/Reports/a.txt", "1")), nil
		})
		if _, err := runShare(tool.run, tool.args); err == nil || len(*calls) != 1 {
			t.Errorf("other share: err = %v, calls = %v", err, *calls)
		}
	}
}

func TestSharesChangesValidateIDsAndFieldsLocally(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return status(500), nil })
	for _, c := range []struct {
		run  shareTool
		args obj
	}{
		{updateShareTool, obj{"share_id": "1/2", "update": true}},
		{updateShareTool, obj{"share_id": "", "update": true}},
		{updateShareTool, obj{"share_id": "7"}},
		{updateShareTool, obj{"share_id": "7", "path": "x", "update": true}},
		{updateShareTool, obj{"share_id": "7", "type": "group", "update": true}},
		{updateShareTool, obj{"share_id": "7", "expires_at": "x"}},
		{deleteShareTool, obj{"share_id": "x"}},
		{deleteShareTool, obj{"share_id": "7", "update": true}},
	} {
		if _, err := runShare(c.run, c.args); err == nil {
			t.Errorf("accepted %v", c.args)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %v", *calls)
	}
}

func TestSharesChangesReportUncertainOutcomesWithoutRepeating(t *testing.T) {
	failures := map[string]func(*http.Request) (*http.Response, error){
		"500":     func(*http.Request) (*http.Response, error) { return status(500), nil },
		"503":     func(*http.Request) (*http.Response, error) { return status(503), nil },
		"timeout": func(*http.Request) (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") },
		"garbage": func(*http.Request) (*http.Response, error) { return ocsResponse(200, "<html>"), nil },
		"ocs 500": func(*http.Request) (*http.Response, error) {
			return ocsResponse(200, `{"ocs":{"meta":{"status":"failure","statuscode":500},"data":[]}}`), nil
		},
	}
	for name, failure := range failures {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return failure(nil) })
		_, err := runShare(createShareTool, obj{"type": "user", "path": "a", "share_with": "bob"})
		if err == nil || !strings.Contains(err.Error(), "may have been created") || !strings.Contains(err.Error(), "shares.list") || len(*calls) != 1 {
			t.Errorf("create %s: err = %v, calls = %d", name, err, len(*calls))
		}
		calls = shareServer(t, "0", "/Reports/a.txt", failure)
		_, err = runShare(updateShareTool, obj{"share_id": "7", "update": true})
		if err == nil || !strings.Contains(err.Error(), "may have been changed") || len(*calls) != 2 {
			t.Errorf("update %s: err = %v, calls = %d", name, err, len(*calls))
		}
		calls = shareServer(t, "0", "/Reports/a.txt", failure)
		_, err = runShare(deleteShareTool, obj{"share_id": "7"})
		if err == nil || !strings.Contains(err.Error(), "may have been revoked") || len(*calls) != 2 {
			t.Errorf("delete %s: err = %v, calls = %d", name, err, len(*calls))
		}
	}
}

func TestSharesChangesReportPolicyRefusalsClearlyWithoutProviderText(t *testing.T) {
	for _, code := range []int{400, 403, 404, 301, 307} {
		refusal := func(*http.Request) (*http.Response, error) { return status(code), nil }
		ocsBody := func(*http.Request) (*http.Response, error) {
			return ocsResponse(200, `{"ocs":{"meta":{"status":"failure","statuscode":`+itoa(code)+`,"message":"`+textCanary+`"},"data":[]}}`), nil
		}
		answers := []func(*http.Request) (*http.Response, error){refusal}
		if code >= 400 {
			answers = append(answers, ocsBody)
		}
		for _, answer := range answers {
			for name, run := range map[string]func() error{
				"create": func() error {
					serve(t, answer)
					_, err := runShare(createShareTool, obj{"type": "user", "path": "a", "share_with": "bob"})
					return err
				},
				"update": func() error {
					shareServer(t, "0", "/Reports/a.txt", answer)
					_, err := runShare(updateShareTool, obj{"share_id": "7", "update": true})
					return err
				},
				"delete": func() error {
					shareServer(t, "0", "/Reports/a.txt", answer)
					_, err := runShare(deleteShareTool, obj{"share_id": "7"})
					return err
				},
			} {
				err := run()
				if err == nil || strings.Contains(err.Error(), "may have been") || strings.Contains(err.Error(), textCanary) ||
					strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("%s %d: err = %v", name, code, err)
				}
			}
		}
	}
	serve(t, func(*http.Request) (*http.Response, error) { return status(403), nil })
	_, err := runShare(createShareTool, obj{"type": "user", "path": "a", "share_with": "bob"})
	if err == nil || !strings.Contains(err.Error(), "instance policy") {
		t.Errorf("err = %v", err)
	}
}

func TestShareManagementIsConfirmedAllowListedAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, c := range []struct {
		d      capability.Descriptor
		effect capability.Effect
	}{{sharesCreate, capability.EffectCreate}, {sharesUpdate, capability.EffectUpdate}, {sharesDelete, capability.EffectDelete}} {
		r := c.d.Risk
		if r.Effect != c.effect || r.Idempotency == "" || r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld ||
			r.DataSensitivity == "" || !c.d.RequiresToolAllowList || c.d.Group != groupShares || strings.Count(c.d.ID, ".") != 2 {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if id == sharesCreate.ID || id == sharesUpdate.ID || id == sharesDelete.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
	calls := serve(t, changeOK)
	red := &redact.Redactor{}
	core := application.New(reg, coreConfig(), resolver(red), red)
	for op, args := range map[string]string{
		"nextcloud.shares.create": `{"type":"user","path":"a","share_with":"bob"}`,
		"nextcloud.shares.update": `{"share_id":"7","update":true}`,
		"nextcloud.shares.delete": `{"share_id":"7"}`,
	} {
		if _, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: op, Connection: "reports", Arguments: json.RawMessage(args),
		}); err == nil {
			t.Errorf("%s ran without confirmation or a tools list", op)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %v", *calls)
	}
}
