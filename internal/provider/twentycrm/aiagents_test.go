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

const wantAgentsListDocument = `query FindAgents { findManyAgents { id name label description modelId roleId isCustom isSystem triggers } }`

const wantAgentsGetDocument = `query FindAgent($input: AgentIdInput!) { findOneAgent(input: $input) { ` +
	`id name label description modelId roleId isCustom isSystem triggers } }`

const wantSkillsListDocument = `query FindSkills { skills { id name label description isCustom isSystem isActive } }`

const wantSkillsGetDocument = `query FindSkill($id: UUID!) { skill(id: $id) { id name label description isCustom isSystem isActive } }`

const (
	aiPromptCn   = "canary-agent-prompt-1a"
	aiContentCn  = "canary-skill-content-2b"
	aiInstrCn    = "canary-trigger-instructions-3c"
	aiSettingsCn = "canary-trigger-settings-4d"
	aiFormatCn   = "canary-response-format-5e"
	aiAppCn      = "99999999-0000-4000-8000-000000000009"
	aiUnknownCn  = "canary-unknown-kind-6f"
)

const agentOne = `{"id":"` + roleID + `","name":"sales-bot\u0007","label":"Sales bot","description":"Helps sales",` +
	`"icon":"IconRobot","prompt":"` + aiPromptCn + `","modelId":"gpt-4o","responseFormat":{"type":"` + aiFormatCn + `"},` +
	`"roleId":"` + roleIDEdit + `","isCustom":true,"isSystem":false,"applicationId":"` + aiAppCn + `",` +
	`"createdAt":"2026-01-01T00:00:00Z","modelConfiguration":{"x":"` + aiFormatCn + `"},"triggers":[` +
	`{"id":"` + aiAppCn + `","isActive":true,"instructions":"` + aiInstrCn + `","type":"CRON","settings":{"pattern":"` + aiSettingsCn + `"}},` +
	`{"id":"` + aiAppCn + `","isActive":false,"instructions":"` + aiInstrCn + `","type":"DATABASE_EVENT","settings":{"eventName":"` + aiSettingsCn + `"}},` +
	`{"type":"` + aiUnknownCn + `","instructions":"` + aiInstrCn + `"},"` + aiInstrCn + `",null]}`

const agentTwo = `{"id":"` + roleIDEdit + `","name":null,"label":null,"description":null,"modelId":null,"roleId":null,` +
	`"isCustom":false,"isSystem":true,"triggers":[]}`

const skillOne = `{"id":"` + roleID + `","name":"summarize\u0007","label":"Summarize","description":"Shortens text",` +
	`"icon":"IconX","content":"` + aiContentCn + `","isCustom":true,"isSystem":false,"isActive":true,` +
	`"applicationId":"` + aiAppCn + `","createdAt":"2026-01-01T00:00:00Z"}`

const skillTwo = `{"id":"` + roleIDEdit + `","name":null,"label":null,"description":null,"isCustom":false,"isSystem":true,"isActive":false}`

func assertNoAIDetails(t *testing.T, out string) {
	t.Helper()
	for _, leaked := range []string{aiPromptCn, aiContentCn, aiInstrCn, aiSettingsCn, aiFormatCn, aiAppCn, aiUnknownCn,
		"prompt", "content", "instructions", "settings", "responseFormat", "modelConfiguration", "applicationId", "createdAt"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%s reached the output: %s", leaked, out)
		}
	}
}

func TestAIDocumentsNeverSelectPromptOrContent(t *testing.T) {
	for _, doc := range []string{wantAgentsListDocument, wantAgentsGetDocument, wantSkillsListDocument, wantSkillsGetDocument} {
		for _, name := range []string{"prompt", "content", "responseFormat", "modelConfiguration", "applicationId",
			"createdByUserWorkspaceId", "icon"} {
			if strings.Contains(doc, name) {
				t.Errorf("document selects %s: %s", name, doc)
			}
		}
	}
}

func TestAgentsListSendsTheFixedDocumentAndReducesTriggersToKinds(t *testing.T) {
	calls := serveRoles(t, http.StatusOK, `{"data":{"findManyAgents":[`+agentOne+`,`+agentTwo+`]}}`)
	out, err := runMemberTool(t, invokeAgentsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].path != metadataPath || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].query != wantAgentsListDocument || len((*calls)[0].variables) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
	for _, want := range []string{`"id":"` + roleID + `","name":"sales-bot","label":"Sales bot","description":"Helps sales",` +
		`"model_id":"gpt-4o","role_id":"` + roleIDEdit + `","custom":true,"system":false,` +
		`"trigger_kinds":["CRON","DATABASE_EVENT"]}`,
		`{"id":"` + roleIDEdit + `","name":"","custom":false,"system":true}`, `"truncated":false`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s: %s", want, out)
		}
	}
	assertNoAIDetails(t, out)
}

func TestAgentsGetSendsTheFixedDocumentWithTheValidatedID(t *testing.T) {
	calls := serveRoles(t, http.StatusOK, `{"data":{"findOneAgent":`+agentOne+`}}`)
	out, err := runMemberTool(t, invokeAgentsGet, `{"id":"`+roleID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := (*calls)[0].variables["input"].(map[string]any)
	if len(*calls) != 1 || (*calls)[0].path != metadataPath || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].query != wantAgentsGetDocument || len((*calls)[0].variables) != 1 ||
		len(input) != 1 || input["id"] != roleID {
		t.Fatalf("calls = %+v", *calls)
	}
	if !strings.Contains(out, `"trigger_kinds":["CRON","DATABASE_EVENT"]`) {
		t.Errorf("output = %s", out)
	}
	assertNoAIDetails(t, out)
}

func TestAgentTriggerKindsAreCappedAndOnlyKnownKindsPass(t *testing.T) {
	var triggers []string
	for i := 0; i < agentTriggersMax+5; i++ {
		triggers = append(triggers, `{"type":"CRON","instructions":"`+aiInstrCn+`"}`)
	}
	serveRoles(t, http.StatusOK, `{"data":{"findOneAgent":{"id":"`+roleID+`","name":"a","isCustom":false,"isSystem":false,"triggers":[`+
		strings.Join(triggers, ",")+`]}}}`)
	out, err := runMemberTool(t, invokeAgentsGet, `{"id":"`+roleID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var agent Agent
	if json.Unmarshal([]byte(out), &agent) != nil || len(agent.TriggerKinds) != agentTriggersMax || !agent.TriggersTruncated {
		t.Errorf("agent = %+v", agent)
	}
	assertNoAIDetails(t, out)
}

func TestAgentsGetRejectsAnotherAgent(t *testing.T) {
	serveRoles(t, http.StatusOK, `{"data":{"findOneAgent":`+agentTwo+`}}`)
	_, err := runMemberTool(t, invokeAgentsGet, `{"id":"`+roleID+`"}`)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestSkillsListSendsTheFixedDocument(t *testing.T) {
	calls := serveRoles(t, http.StatusOK, `{"data":{"skills":[`+skillOne+`,`+skillTwo+`]}}`)
	out, err := runMemberTool(t, invokeSkillsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].path != metadataPath || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].query != wantSkillsListDocument || len((*calls)[0].variables) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
	for _, want := range []string{`"id":"` + roleID + `","name":"summarize","label":"Summarize","description":"Shortens text",` +
		`"custom":true,"system":false,"active":true}`,
		`{"id":"` + roleIDEdit + `","name":"","custom":false,"system":true,"active":false}`, `"truncated":false`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s: %s", want, out)
		}
	}
	assertNoAIDetails(t, out)
}

func TestSkillsGetSendsTheFixedDocumentWithTheValidatedID(t *testing.T) {
	calls := serveRoles(t, http.StatusOK, `{"data":{"skill":`+skillOne+`}}`)
	out, err := runMemberTool(t, invokeSkillsGet, `{"id":"`+roleID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].path != metadataPath || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].query != wantSkillsGetDocument || len((*calls)[0].variables) != 1 || (*calls)[0].variables["id"] != roleID {
		t.Fatalf("calls = %+v", *calls)
	}
	assertNoAIDetails(t, out)
}

func TestSkillsGetRejectsAnotherSkillAndMapsNullToNotFound(t *testing.T) {
	serveRoles(t, http.StatusOK, `{"data":{"skill":`+skillTwo+`}}`)
	if _, err := runMemberTool(t, invokeSkillsGet, `{"id":"`+roleID+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("another skill: err = %v", err)
	}
	serveRoles(t, http.StatusOK, `{"data":{"skill":null}}`)
	if _, err := runMemberTool(t, invokeSkillsGet, `{"id":"`+roleID+`"}`); classOf(err) != provider.ClassNotFound {
		t.Errorf("null skill: err = %v", err)
	}
}

func TestAIListsCapTheListAndText(t *testing.T) {
	var agents, skills []string
	for i := 0; i < aiItemsMax+1; i++ {
		text := `"name":"` + strings.Repeat("n", 300) + `","label":"` + strings.Repeat("l", 300) + `","description":"` +
			strings.Repeat("d", 900) + `"`
		agents = append(agents, `{"id":"`+roleID+`",`+text+`,"modelId":"`+strings.Repeat("m", 300)+`"}`)
		skills = append(skills, `{"id":"`+roleID+`",`+text+`}`)
	}
	serveRoles(t, http.StatusOK, `{"data":{"findManyAgents":[`+strings.Join(agents, ",")+`]}}`)
	out, err := runMemberTool(t, invokeAgentsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var agentList AgentList
	if json.Unmarshal([]byte(out), &agentList) != nil || len(agentList.Agents) != aiItemsMax || !agentList.Truncated ||
		len(agentList.Agents[0].Name) != aiNameMax || len(agentList.Agents[0].Label) != aiNameMax ||
		len(agentList.Agents[0].Description) != aiDescriptionMax || len(agentList.Agents[0].ModelID) != aiModelIDMax {
		t.Errorf("agents = %d, truncated = %v", len(agentList.Agents), agentList.Truncated)
	}
	serveRoles(t, http.StatusOK, `{"data":{"skills":[`+strings.Join(skills, ",")+`]}}`)
	out, err = runMemberTool(t, invokeSkillsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var skillList SkillList
	if json.Unmarshal([]byte(out), &skillList) != nil || len(skillList.Skills) != aiItemsMax || !skillList.Truncated ||
		len(skillList.Skills[0].Name) != aiNameMax || len(skillList.Skills[0].Description) != aiDescriptionMax {
		t.Errorf("skills = %d, truncated = %v", len(skillList.Skills), skillList.Truncated)
	}
}

func TestAIToolsFailWithClassesAndWithoutProviderText(t *testing.T) {
	const canary = "provider-text-canary-8e2"
	for answer, class := range map[string]provider.Class{
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"BAD_USER_INPUT"}}]}`:  provider.ClassProviderError,
		`{"data":{"findManyAgents":[],"skills":[]},"errors":[{"message":"` + canary + `"}]}`:            provider.ClassProviderError,
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"UNAUTHENTICATED"}}]}`: provider.ClassAuth,
		`{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"FORBIDDEN"}}]}`:       provider.ClassPermission,
		`{"data":{"findManyAgents":null,"findOneAgent":null,"skills":null}}`:                            provider.ClassInvalidResponse,
		`{"data":{}}`: provider.ClassInvalidResponse,
		`{"data":{"findManyAgents":[{"id":"` + canary + `","name":"x"}],"findOneAgent":{"id":"` + canary +
			`"},"skills":[{"id":"` + canary + `"}],"skill":{"id":"` + canary + `"}}}`: provider.ClassInvalidResponse,
	} {
		for _, call := range []struct {
			handler capability.Handler
			args    string
		}{{invokeAgentsList, `{}`}, {invokeAgentsGet, `{"id":"` + roleID + `"}`},
			{invokeSkillsList, `{}`}, {invokeSkillsGet, `{"id":"` + roleID + `"}`}} {
			serveRoles(t, http.StatusOK, answer)
			_, err := runMemberTool(t, call.handler, call.args)
			if classOf(err) != class || strings.Contains(err.Error(), canary) {
				t.Errorf("%s = %v, want class %s", answer, err, class)
			}
		}
	}
}

func TestAIToolsForbiddenNameTheRightWithoutProviderText(t *testing.T) {
	const canary = "provider-text-canary-9d3"
	for name, test := range map[string]struct {
		status int
		body   string
	}{
		"graphql": {http.StatusOK, `{"data":null,"errors":[{"message":"` + canary + `","extensions":{"code":"FORBIDDEN"}}]}`},
		"http":    {http.StatusForbidden, `{"message":"` + canary + `"}`},
	} {
		for _, call := range []struct {
			handler capability.Handler
			args    string
		}{{invokeAgentsList, `{}`}, {invokeAgentsGet, `{"id":"` + roleID + `"}`},
			{invokeSkillsList, `{}`}, {invokeSkillsGet, `{"id":"` + roleID + `"}`}} {
			serveRoles(t, test.status, test.body)
			_, err := runMemberTool(t, call.handler, call.args)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), `"AI"`) ||
				strings.Contains(err.Error(), canary) {
				t.Errorf("%s err = %v", name, err)
			}
		}
	}
}

func TestAIToolsRefuseBeforeSecretAndIO(t *testing.T) {
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
		{invokeAgentsList, `{}`, []string{"object/companies"}},
		{invokeAgentsGet, `{"id":"` + roleID + `"}`, []string{"object/companies"}},
		{invokeAgentsGet, `{"id":"../etc"}`, nil},
		{invokeAgentsGet, `{"id":"` + roleID + `{x}"}`, nil},
		{invokeAgentsGet, `{}`, nil},
		{invokeSkillsList, `{}`, []string{"object/companies"}},
		{invokeSkillsGet, `{"id":"` + roleID + `"}`, []string{"object/companies"}},
		{invokeSkillsGet, `{"id":"../etc"}`, nil},
		{invokeSkillsGet, `{"id":"` + roleID + `{x}"}`, nil},
		{invokeSkillsGet, `{}`, nil},
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

func TestAIToolsDeclareTheirRiskAndStayOutOfProfiles(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, d := range []capability.Descriptor{agentsList, agentsGet, skillsList, skillsGet} {
		if d.Risk.DataSensitivity != aiDataSensitivity || d.Risk.Effect != capability.EffectRead ||
			d.Risk.Confirmation != capability.ConfirmationNone || d.Risk.Idempotency != capability.IdempotencySafe ||
			!d.Risk.OpenWorld || d.RequiresToolAllowList || !strings.Contains(d.Description, "untrusted") ||
			!strings.Contains(d.Description, `"AI"`) {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		for i, profile := range metadata.Profiles {
			if strings.Contains(" "+strings.Join(profile.Tools, " ")+" ", " "+d.ID+" ") {
				t.Errorf("%s in profile %d", d.ID, i)
			}
		}
	}
}
