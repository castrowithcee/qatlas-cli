package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	memberID   = "55555555-0000-4000-8000-000000000005"
	memberID2  = "66666666-0000-4000-8000-000000000006"
	avatarCn   = "canary-avatar-url-4411"
	userIDCn   = "canary-user-id-5522"
	schemeCn   = "canary-color-scheme-6633"
	keyNameCn  = "canary-api-key-name-7744"
	personCn   = "Ada-Canary"
	emailCn    = "ada.canary@example.test"
	roleID     = "77777777-0000-4000-8000-000000000007"
	roleIDEdit = "88888888-0000-4000-8000-000000000008"
)

const memberPageBody = `{"data":{"workspaceMembers":[{"id":"` + memberID + `","name":{"firstName":"` + personCn +
	`","lastName":"Lovelace\u0007"},"userEmail":"` + emailCn + `\n","timeZone":"Europe/Zurich","locale":"de-CH",` +
	`"avatarUrl":"` + avatarCn + `","userId":"` + userIDCn + `","colorScheme":"` + schemeCn + `","jobTitle":"` + schemeCn + `"},` +
	`{"id":"` + memberID2 + `","name":null,"userEmail":null,"timeZone":"<script>","locale":"EN_us!"}]},` +
	`"pageInfo":{"hasNextPage":true,"endCursor":"cursor1"}}`

func serveMembers(t *testing.T) *[]wfCall {
	t.Helper()
	calls := &[]wfCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, wfCall{request.URL.Path, request.URL.RawQuery})
		switch request.URL.Path {
		case membersPath:
			return jsonResponse(http.StatusOK, memberPageBody), nil
		case membersPath + "/" + memberID:
			return jsonResponse(http.StatusOK, `{"data":{"workspaceMember":`+
				strings.TrimSuffix(strings.TrimPrefix(memberPageBody, `{"data":{"workspaceMembers":[`),
					`,{"id":"`+memberID2+`","name":null,"userEmail":null,"timeZone":"<script>","locale":"EN_us!"}]},`+
						`"pageInfo":{"hasNextPage":true,"endCursor":"cursor1"}}`)+`}}`), nil
		case membersPath + "/" + memberID2:
			return jsonResponse(http.StatusOK, `{"data":{"workspaceMember":{"id":"`+memberID+`","name":{"firstName":"`+personCn+`"}}}}`), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

// runMemberTool runs a handler and fails when a canary of an excluded field appears in the output.
func runMemberTool(t *testing.T, handler capability.Handler, args string, targets ...string) (string, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return "", err
	}
	out, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, canary := range []string{avatarCn, userIDCn, schemeCn, keyNameCn} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	return string(out), nil
}

func TestMembersListProjectsAFixedAllowlist(t *testing.T) {
	calls := serveMembers(t)
	out, err := runMemberTool(t, invokeMembersList, `{"limit":10}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"members":[{"id":"` + memberID + `","name":"` + personCn + ` Lovelace","email":"` + emailCn +
		`","time_zone":"Europe/Zurich","locale":"de-CH"},{"id":"` + memberID2 + `"}],"next_cursor":`
	if !strings.HasPrefix(out, want) || !strings.Contains(out, `"has_more":true`) {
		t.Errorf("output = %s", out)
	}
	query, _ := url.ParseQuery((*calls)[0].Query)
	if len(*calls) != 1 || (*calls)[0].Path != membersPath || query.Get("limit") != "10" || query.Get("depth") != "0" ||
		query.Get("fields") != "id,name,userEmail,timeZone,locale" || query.Get("filter") != "" {
		t.Errorf("request = %+v", *calls)
	}
}

func TestMembersGetProjectsAndChecksTheIdentifier(t *testing.T) {
	calls := serveMembers(t)
	out, err := runMemberTool(t, invokeMembersGet, `{"id":"`+memberID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"email":"`+emailCn+`"`) || !strings.Contains(out, `"locale":"de-CH"`) || (*calls)[0].Path != membersPath+"/"+memberID {
		t.Errorf("output = %s, calls = %+v", out, *calls)
	}
	_, err = runMemberTool(t, invokeMembersGet, `{"id":"`+memberID2+`"}`)
	if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), personCn) {
		t.Errorf("foreign member err = %v", err)
	}
}

func TestMemberPersonalDataNeverReachesErrors(t *testing.T) {
	for name, body := range map[string]string{
		"bad id":      `{"data":{"workspaceMembers":[{"id":"` + personCn + `","name":{"firstName":"` + personCn + `"},"userEmail":"` + emailCn + `"}]},"pageInfo":{"hasNextPage":false}}`,
		"bad name":    `{"data":{"workspaceMembers":[{"id":"` + memberID + `","name":"` + personCn + `","userEmail":"` + emailCn + `"}]},"pageInfo":{"hasNextPage":false}}`,
		"bad shape":   `{"data":{"workspaceMembers":"` + emailCn + `"},"pageInfo":{"hasNextPage":false}}`,
		"bad cursor":  `{"data":{"workspaceMembers":[]},"pageInfo":{"hasNextPage":true,"endCursor":"` + emailCn + ` ` + personCn + `"}}`,
		"server text": `not json ` + personCn + ` ` + emailCn,
	} {
		serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
		stubLimiter(t, cloudKey)
		_, err := runMemberTool(t, invokeMembersList, `{}`)
		if err == nil || strings.Contains(err.Error(), personCn) || strings.Contains(err.Error(), emailCn) {
			t.Errorf("%s err = %v", name, err)
		}
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{"message":"`+emailCn+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	if _, err := runMemberTool(t, invokeMembersList, `{}`); err == nil || strings.Contains(err.Error(), emailCn) {
		t.Errorf("status err = %v", err)
	}
}

func TestMemberAndRoleToolsRefuseObjectTargetsBeforeSecretAndRequest(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for id, test := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"list":  {invokeMembersList, `{}`},
		"get":   {invokeMembersGet, `{"id":"` + memberID + `"}`},
		"roles": {invokeRolesList, `{}`},
	} {
		// A nil resolver would fail on any secret access.
		_, err := test.handler(context.Background(), targetConnection("object/person"), nil, red, json.RawMessage(test.args))
		if err == nil || classOf(err) != "" || !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s err = %v", id, err)
		}
	}
}

func TestMemberToolsRefuseBadArgumentsBeforeIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, test := range []struct {
		handler capability.Handler
		args    string
	}{
		{invokeMembersGet, `{"id":"../etc"}`},
		{invokeMembersGet, `{"id":"` + memberID + `,x[eq]:1"}`},
		{invokeMembersList, `{"limit":101}`},
		{invokeMembersList, `{"cursor":"AAAA"}`},
	} {
		if _, err := test.handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(test.args)); err == nil || classOf(err) != "" {
			t.Errorf("%s: err = %v", test.args, err)
		}
	}
}

func TestMembersCursorIsBoundToTheConnectionRequest(t *testing.T) {
	serveMembers(t)
	out, err := runMemberTool(t, invokeMembersList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var page WorkspaceMemberList
	if err := json.Unmarshal([]byte(out), &page); err != nil || page.NextCursor == "" {
		t.Fatal(out, err)
	}
	if _, err := runMemberTool(t, invokeMembersList, `{"cursor":"`+page.NextCursor+`"}`); err != nil {
		t.Errorf("same request: %v", err)
	}
	if _, err := runMemberTool(t, invokeMembersList, `{"cursor":"`+page.NextCursor+`x"}`); err == nil {
		t.Error("altered cursor accepted")
	}
}

func TestGenericRecordToolsDoNotReachWorkspaceMembers(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	if !systemObjects["workspaceMember"] || scopeAllows("workspaceMember") {
		t.Error("workspaceMember is reachable")
	}
	if _, err := parseTarget("object/workspaceMember"); err == nil {
		t.Error("workspaceMember is a valid target")
	}
	for _, handler := range []capability.Handler{invokeRecordsList, invokeRecordsGet, invokeRecordsSearch, invokeRecordsGroupBy,
		invokeRecordsCreate, invokeRecordsUpdate, invokeRecordsDelete} {
		args := `{"object":"workspaceMember","id":"` + memberID + `","fields":{"name":"x"},"group_by":["locale"],"conditions":[{"field":"locale","operator":"eq","value":"de"}]}`
		if _, err := handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args)); err == nil {
			t.Error("workspaceMember reachable through a generic tool")
		}
	}
}

func TestMemberAndRoleToolsDeclareTheirRiskAndProfiles(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, test := range []struct {
		d           capability.Descriptor
		sensitivity string
	}{
		{membersList, "twentycrm-member-data"}, {membersGet, "twentycrm-member-data"}, {rolesList, "twentycrm-role-data"},
	} {
		d, sensitivity := test.d, test.sensitivity
		if d.Risk.DataSensitivity != sensitivity || d.Risk.Effect != capability.EffectRead ||
			d.Risk.Confirmation != capability.ConfirmationNone || d.Risk.Idempotency != capability.IdempotencySafe ||
			!d.Risk.OpenWorld || d.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		for i, profile := range metadata.Profiles {
			ticked := strings.Contains(" "+strings.Join(profile.Tools, " ")+" ", " "+d.ID+" ")
			// roles.list needs the Roles right, which also changes roles, so no profile ticks it.
			if ticked != (d.ID != rolesList.ID) {
				t.Errorf("%s in profile %d = %v", d.ID, i, ticked)
			}
		}
	}
}
