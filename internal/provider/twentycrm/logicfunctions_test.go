package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const wantLogicFunctionFields = `id name description runtime timeoutSeconds executionMode cronTriggerSettings ` +
	`databaseEventTriggerSettings httpRouteTriggerSettings toolTriggerSettings workflowActionTriggerSettings ` +
	`sourceHandlerPath handlerName applicationId universalIdentifier createdAt updatedAt`

const wantLogicFunctionsListDocument = `query FindLogicFunctions { findManyLogicFunctions { ` + wantLogicFunctionFields + ` } }`

const wantLogicFunctionsGetDocument = `query FindLogicFunction($input: LogicFunctionIdInput!) { ` +
	`findOneLogicFunction(input: $input) { ` + wantLogicFunctionFields + ` } }`

const (
	lfPathCn      = "/canary-route-path-1a"
	lfCronCn      = "canary-cron-pattern-2b"
	lfEventCn     = "canary-event-name-3c"
	lfCodeCn      = "canary-source-code-4d"
	lfHandlerCn   = "canary-handler-name-5e"
	lfLogCn       = "canary-execution-log-6f"
	lfSecretCn    = "canary-settings-secret-7a"
	lfHeaderCn    = "canary-forwarded-header-8b"
	lfAppCn       = "99999999-0000-4000-8000-000000000009"
	lfUniversalID = "98989898-0000-4000-8000-000000000098"
)

// logicFunctionOne is a function in the shape of the Twenty LogicFunctionDTO, with source code and logs as
// Twenty never sends them here.
const logicFunctionOne = `{"id":"` + roleID + `","name":"Sync\u0007","description":"Pushes data","runtime":"nodejs22.x",` +
	`"timeoutSeconds":30,"executionMode":"LIVE","sourceHandlerPath":"src/sync.ts\u0007","handlerName":"` + lfHandlerCn + `",` +
	`"sourceCode":"` + lfCodeCn + `","logs":"` + lfLogCn + `",` +
	`"cronTriggerSettings":{"pattern":"` + lfCronCn + `"},"databaseEventTriggerSettings":{"eventName":"` + lfEventCn + `",` +
	`"updatedFields":["name"],"batchMode":true},` +
	`"httpRouteTriggerSettings":{"path":"` + lfPathCn + `","httpMethod":"GET","isAuthRequired":false,` +
	`"forwardedRequestHeaders":["` + lfHeaderCn + `"]},` +
	`"toolTriggerSettings":{"inputSchema":{"type":"object","properties":{"n":{"type":"integer","maximum":9007199254740993}}},` +
	`"frontComponentUniversalIdentifier":"` + lfUniversalID + `"},` +
	`"workflowActionTriggerSettings":{"inputSchema":[{"type":"string","label":"Company"}],"label":"Sync","icon":"IconRefresh",` +
	`"outputSchema":[{"type":"number","label":"see https://hook.example/run?key=` + lfSecretCn + `"}]},` +
	`"applicationId":"` + lfAppCn + `","universalIdentifier":"` + lfUniversalID + `","createdAt":"2026-01-01T00:00:00Z",` +
	`"updatedAt":"2026-01-02T00:00:00Z"}`

const wantLogicFunctionOne = `{"id":"` + roleID + `","name":"Sync","description":"Pushes data","runtime":"nodejs22.x",` +
	`"timeout_seconds":30,"execution_mode":"LIVE","triggers":{"cron":true,"database_event":true,"http_route":true,` +
	`"tool":true,"workflow_action":true},"source_handler_path":"src/sync.ts","handler_name":"` + lfHandlerCn + `",` +
	`"application_id":"` + lfAppCn + `","universal_identifier":"` + lfUniversalID + `","created_at":"2026-01-01T00:00:00Z",` +
	`"updated_at":"2026-01-02T00:00:00Z","cron_trigger_settings":{"pattern":"` + lfCronCn + `"},` +
	`"database_event_trigger_settings":{"batchMode":true,"eventName":"` + lfEventCn + `","updatedFields":["name"]},` +
	`"tool_trigger_settings":{"frontComponentUniversalIdentifier":"` + lfUniversalID + `","inputSchema":{"properties":` +
	`{"n":{"maximum":9007199254740993,"type":"integer"}},"type":"object"}},"workflow_action_trigger_settings":` +
	`{"icon":"IconRefresh","inputSchema":[{"label":"Company","type":"string"}],"label":"Sync","outputSchema":[{"type":"number"}]}}`

// logicFunctionTwo holds only the fields the logic function tools read before the details.
const logicFunctionTwo = `{"id":"` + roleIDEdit + `","name":null,"description":null,"runtime":"nodejs22.x",` +
	`"timeoutSeconds":5,"executionMode":"PREBUILT"}`

const wantLogicFunctionTwo = `{"id":"` + roleIDEdit + `","name":"","runtime":"nodejs22.x","timeout_seconds":5,` +
	`"execution_mode":"PREBUILT","triggers":{"cron":false,"database_event":false,"http_route":false,"tool":false,` +
	`"workflow_action":false}}`

func serveLogicFunctions(t *testing.T, status int, answer string) *[]rolesCall {
	t.Helper()
	return serveRoles(t, status, answer)
}

// assertNoLogicFunctionDetails checks the values that never reach an output: source code, logs, the settings
// of an HTTP route trigger, and texts that carry an access address.
func assertNoLogicFunctionDetails(t *testing.T, out string) {
	t.Helper()
	for _, leaked := range []string{lfPathCn, lfCodeCn, lfLogCn, lfSecretCn, lfHeaderCn, "httpMethod", "isAuthRequired",
		"http_route_trigger_settings", "hook.example", "sourceCode", "logs", "\\u0007"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%s reached the output: %s", leaked, out)
		}
	}
}

func TestLogicFunctionsListSendsTheFixedDocumentAndReadsEveryDocumentedField(t *testing.T) {
	calls := serveLogicFunctions(t, http.StatusOK, `{"data":{"findManyLogicFunctions":[`+logicFunctionOne+`,`+logicFunctionTwo+`]}}`)
	out, err := runMemberTool(t, invokeLogicFunctionsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].path != metadataPath || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].query != wantLogicFunctionsListDocument || len((*calls)[0].variables) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
	want := `{"logic_functions":[` + wantLogicFunctionOne + `,` + wantLogicFunctionTwo + `],"truncated":false}`
	if out != want {
		t.Errorf("output = %s\nwant     %s", out, want)
	}
	assertNoLogicFunctionDetails(t, out)
	for _, name := range []string{"createdByUserWorkspaceId", "sourceCode", "getAvailablePackages", "getLogicFunctionSourceCode",
		"logs", "builtHandler"} {
		if strings.Contains(wantLogicFunctionsListDocument, name) {
			t.Errorf("document selects %s", name)
		}
	}
}

func TestLogicFunctionsGetSendsTheFixedDocumentWithTheValidatedID(t *testing.T) {
	calls := serveLogicFunctions(t, http.StatusOK, `{"data":{"findOneLogicFunction":`+logicFunctionOne+`}}`)
	out, err := runMemberTool(t, invokeLogicFunctionsGet, `{"id":"`+roleID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := (*calls)[0].variables["input"].(map[string]any)
	if len(*calls) != 1 || (*calls)[0].path != metadataPath || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].query != wantLogicFunctionsGetDocument || len((*calls)[0].variables) != 1 ||
		len(input) != 1 || input["id"] != roleID {
		t.Fatalf("calls = %+v", *calls)
	}
	if out != wantLogicFunctionOne {
		t.Errorf("output = %s\nwant     %s", out, wantLogicFunctionOne)
	}
	assertNoLogicFunctionDetails(t, out)
}

func TestLogicFunctionSettingsBeyondTheirLimitsAreLeftOutAndMarked(t *testing.T) {
	large := `{"x":"` + strings.Repeat("s", freeObjectBytesMax) + `"}`
	deep := strings.Repeat(`{"a":`, freeObjectDepthMax+1) + `1` + strings.Repeat(`}`, freeObjectDepthMax+1)
	for _, member := range []struct{ twenty, output string }{
		{"cronTriggerSettings", "cron_trigger_settings"},
		{"databaseEventTriggerSettings", "database_event_trigger_settings"},
		{"toolTriggerSettings", "tool_trigger_settings"},
		{"workflowActionTriggerSettings", "workflow_action_trigger_settings"},
	} {
		for _, value := range []string{large, deep} {
			serveLogicFunctions(t, http.StatusOK, `{"data":{"findOneLogicFunction":{"id":"`+roleID+`","name":"f","`+
				member.twenty+`":`+value+`}}}`)
			out, err := runMemberTool(t, invokeLogicFunctionsGet, `{"id":"`+roleID+`"}`)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"`+member.output+`_omitted":true`) || strings.Contains(out, `"`+member.output+`":`) {
				t.Errorf("%s: output = %.300s", member.twenty, out)
			}
		}
	}
}

func TestLogicFunctionsGetRejectsAnotherFunction(t *testing.T) {
	serveLogicFunctions(t, http.StatusOK, `{"data":{"findOneLogicFunction":`+logicFunctionTwo+`}}`)
	_, err := runMemberTool(t, invokeLogicFunctionsGet, `{"id":"`+roleID+`"}`)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestLogicFunctionsListCapsTheListAndText(t *testing.T) {
	var functions []string
	for i := 0; i < logicFunctionsMax+1; i++ {
		functions = append(functions, `{"id":"`+roleID+`","name":"`+strings.Repeat("n", 300)+`","description":"`+
			strings.Repeat("d", 900)+`","runtime":"`+strings.Repeat("r", 200)+`"}`)
	}
	serveLogicFunctions(t, http.StatusOK, `{"data":{"findManyLogicFunctions":[`+strings.Join(functions, ",")+`]}}`)
	out, err := runMemberTool(t, invokeLogicFunctionsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var list LogicFunctionList
	if json.Unmarshal([]byte(out), &list) != nil || len(list.LogicFunctions) != logicFunctionsMax || !list.Truncated ||
		len(list.LogicFunctions[0].Name) != logicFunctionNameMax ||
		len(list.LogicFunctions[0].Description) != logicFunctionDescriptionMax ||
		len(list.LogicFunctions[0].Runtime) != logicFunctionShortMax {
		t.Errorf("logic functions = %d, truncated = %v", len(list.LogicFunctions), list.Truncated)
	}
}

func TestLogicFunctionToolsFailWithClassesAndWithoutProviderText(t *testing.T) {
	const canary = "provider-text-canary-4f7"
	for answer, class := range map[string]provider.Class{
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"BAD_USER_INPUT"}}]}`:  provider.ClassProviderError,
		`{"data":{"findManyLogicFunctions":[]},"errors":[{"message":"` + canary + `"}]}`:                provider.ClassProviderError,
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"UNAUTHENTICATED"}}]}`: provider.ClassAuth,
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"FORBIDDEN"}}]}`:       provider.ClassPermission,
		`{"data":{"findManyLogicFunctions":null,"findOneLogicFunction":null}}`:                          provider.ClassInvalidResponse,
		`{"data":{}}`: provider.ClassInvalidResponse,
		`{"data":{"findManyLogicFunctions":[{"id":"` + canary + `","name":"x"}],"findOneLogicFunction":{"id":"` + canary +
			`"}}}`: provider.ClassInvalidResponse,
	} {
		for _, call := range []struct {
			handler capability.Handler
			args    string
		}{{invokeLogicFunctionsList, `{}`}, {invokeLogicFunctionsGet, `{"id":"` + roleID + `"}`}} {
			serveLogicFunctions(t, http.StatusOK, answer)
			_, err := runMemberTool(t, call.handler, call.args)
			if classOf(err) != class || strings.Contains(err.Error(), canary) {
				t.Errorf("%s = %v, want class %s", answer, err, class)
			}
		}
	}
}

func TestLogicFunctionToolsRefuseBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	noSecret := secret.NewWith(func(name string) string {
		t.Errorf("the secret %s was read", name)
		return ""
	}, nil, nil, red)
	for _, test := range []struct {
		handler capability.Handler
		args    string
		targets []string
	}{
		{invokeLogicFunctionsList, `{}`, []string{"object/companies"}},
		{invokeLogicFunctionsGet, `{"id":"` + roleID + `"}`, []string{"object/companies"}},
		{invokeLogicFunctionsGet, `{"id":"../etc"}`, nil},
		{invokeLogicFunctionsGet, `{"id":"` + roleID + `{x}"}`, nil},
		{invokeLogicFunctionsGet, `{}`, nil},
	} {
		_, err := test.handler(context.Background(), targetConnection(test.targets...), noSecret, red, json.RawMessage(test.args))
		if _, ok := err.(*provider.InvalidRequestError); !ok && classOf(err) != provider.ClassProviderError {
			t.Errorf("%s %v: err = %v", test.args, test.targets, err)
		}
		if err == nil || strings.Contains(err.Error(), "companies") {
			t.Errorf("%s %v: err = %v", test.args, test.targets, err)
		}
	}
}

func TestLogicFunctionToolsDeclareTheirRiskAndStayOutOfProfiles(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, d := range []capability.Descriptor{logicFunctionsList, logicFunctionsGet} {
		if d.Risk.DataSensitivity != logicFunctionDataSensitivity || d.Risk.Effect != capability.EffectRead ||
			d.Risk.Confirmation != capability.ConfirmationNone || d.Risk.Idempotency != capability.IdempotencySafe ||
			!d.Risk.OpenWorld || d.RequiresToolAllowList || !strings.Contains(d.Description, "untrusted") {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		for i, profile := range metadata.Profiles {
			if strings.Contains(" "+strings.Join(profile.Tools, " ")+" ", " "+d.ID+" ") {
				t.Errorf("%s in profile %d", d.ID, i)
			}
		}
	}
}

// The trigger canaries sit next to every kind of failure; none of them may reach the error text.
func TestLogicFunctionToolErrorsNeverCarryTriggerContent(t *testing.T) {
	for name, answer := range map[string]string{
		"another function": `{"data":{"findOneLogicFunction":` + logicFunctionOne + `}}`,
		"invalid id": `{"data":{"findManyLogicFunctions":[` + strings.Replace(logicFunctionOne, roleID, "not-a-uuid", 1) +
			`],"findOneLogicFunction":` + strings.Replace(logicFunctionOne, roleID, "not-a-uuid", 1) + `}}`,
		"graphql errors": `{"data":{"findManyLogicFunctions":[` + logicFunctionOne + `],"findOneLogicFunction":` + logicFunctionOne +
			`},"errors":[{"message":"` + lfPathCn + ` ` + lfCronCn + `"}]}`,
		"http failure": logicFunctionOne,
	} {
		status := http.StatusOK
		if name == "http failure" {
			status = http.StatusInternalServerError
		}
		for _, call := range []struct {
			handler capability.Handler
			args    string
		}{{invokeLogicFunctionsList, `{}`}, {invokeLogicFunctionsGet, `{"id":"` + roleIDEdit + `"}`}} {
			serveLogicFunctions(t, status, answer)
			_, err := runMemberTool(t, call.handler, call.args)
			if err == nil {
				if name == "another function" || name == "invalid id" || name == "graphql errors" || status != http.StatusOK {
					t.Errorf("%s: want an error", name)
				}
				continue
			}
			assertNoLogicFunctionDetails(t, err.Error())
			for _, value := range []string{lfCronCn, lfEventCn, lfHandlerCn, lfUniversalID} {
				if strings.Contains(err.Error(), value) {
					t.Errorf("%s: %s reached the error", name, value)
				}
			}
		}
	}
}
