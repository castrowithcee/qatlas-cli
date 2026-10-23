package n8n

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func auditBody(settings, nextVersions string) string {
	return `{"Instance Risk Report":{"risk":"instance","sections":[{"title":"Security settings",` +
		`"description":"D","recommendation":"R","settings":` + settings + `,"nextVersions":` + nextVersions + `}]}}`
}

func auditResult(t *testing.T, body string) (string, AuditSection) {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) { return jsonResponse(200, body), nil })
	out, err := env.invoke(auditGenerate.ID, "open", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var result AuditResult
	if err := json.Unmarshal([]byte(out), &result); err != nil || len(result.Reports) != 1 ||
		len(result.Reports[0].Sections) != 1 {
		t.Fatalf("%s, %v", out, err)
	}
	return out, result.Reports[0].Sections[0]
}

const richAuditSettings = `{"features":{"communityPackagesEnabled":true,"limit":9007199254740993},` +
	`"nodes":{"nodesExclude":"none","list":["a","b"]},"telemetry":{"diagnosticsEnabled":false,"unset":null}}`

func TestAuditReportsSettingsAndNextVersions(t *testing.T) {
	versions := `[{"name":"1.2.3","nodes":[{"name":"Node A","x":1},{"id":"no-name"},{"name":5},"text"],` +
		`"createdAt":"2026-01-01T00:00:00Z","description":"D\u0007x","documentationUrl":"https://docs.example/1.2.3",` +
		`"hasBreakingChange":false,"hasSecurityFix":null,"hasSecurityIssue":true,"securityIssueFixVersion":"1.2.4"}]`
	out, section := auditResult(t, auditBody(richAuditSettings, versions))
	for _, want := range []string{
		`"settings":{"features":{"communityPackagesEnabled":true,"limit":9007199254740993},` +
			`"nodes":{"list":["a","b"],"nodesExclude":"none"},"telemetry":{"diagnosticsEnabled":false}}`,
		`"next_version_count":1`,
		`"next_versions":[{"created_at":"2026-01-01T00:00:00Z","description":"D x",` +
			`"documentation_url":"https://docs.example/1.2.3","has_breaking_change":false,"has_security_issue":true,` +
			`"name":"1.2.3","node_count":4,"node_names":["Node A"],"security_issue_fix_version":"1.2.4"}]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	if section.SettingsOmitted || strings.Contains(out, "settings_omitted") || strings.Contains(out, "has_security_fix") {
		t.Errorf("unexpected marker or null member: %s", out)
	}
	if seen := assertOutputDeclared(t, auditGenerate.ID, []byte(out)); seen < 10 {
		t.Errorf("checked %d members: %s", seen, out)
	}
}

func TestAuditSettingsBeyondTheBoundsAreOmittedAndMarked(t *testing.T) {
	var many []string
	for i := 0; i < 51; i++ {
		many = append(many, `"k`+string(rune('a'+i%26))+string(rune('a'+i/26))+`":1`)
	}
	var list []string
	for i := 0; i < 51; i++ {
		list = append(list, "1")
	}
	for name, settings := range map[string]string{
		"keys":        `{` + strings.Join(many, ",") + `}`,
		"deep":        `{"a":{"b":{"c":{}}}}`,
		"deep list":   `{"a":{"b":{"c":[1]}}}`,
		"long list":   `{"a":[` + strings.Join(list, ",") + `]}`,
		"object item": `{"a":[{"b":1}]}`,
		"nested list": `{"a":[[1]]}`,
		"not object":  `[1]`,
	} {
		out, section := auditResult(t, auditBody(settings, `[]`))
		if !section.SettingsOmitted || section.Settings != nil || !strings.Contains(out, `"settings_omitted":true`) ||
			strings.Contains(out, `"settings":`) {
			t.Errorf("%s: %s", name, out)
		}
	}
	// Three levels, a list on the third, and exactly 50 keys stay.
	if out, section := auditResult(t, auditBody(`{"a":{"b":{"c":1}},"d":{"e":[1]}}`, `[]`)); section.SettingsOmitted ||
		!strings.Contains(out, `"settings":{"a":{"b":{"c":1}},"d":{"e":[1]}}`) {
		t.Errorf("depth 3 must stay: %s", out)
	}
	if _, section := auditResult(t, auditBody(`{`+strings.Join(many[:50], ",")+`}`, `[]`)); section.SettingsOmitted ||
		len(section.Settings) != 50 {
		t.Error("50 keys must stay")
	}
}

func TestAuditNextVersionsAreBounded(t *testing.T) {
	var versions, nodes []string
	for i := 0; i < 25; i++ {
		versions = append(versions, `{"name":"v","nodes":[]}`)
	}
	for i := 0; i < 60; i++ {
		nodes = append(nodes, `{"name":"n"}`)
	}
	_, section := auditResult(t, auditBody(`{}`, `[`+strings.Join(versions, ",")+`]`))
	if len(section.NextVersions) != 20 || section.NextVersionCount != 25 || !section.NextVersionsCapped {
		t.Errorf("versions = %d, count %d, truncated %v", len(section.NextVersions), section.NextVersionCount,
			section.NextVersionsCapped)
	}
	_, section = auditResult(t, auditBody(`{}`, `[{"name":"v","nodes":[`+strings.Join(nodes, ",")+`]}]`))
	if v := section.NextVersions[0]; v.NodeCount != 60 || len(v.NodeNames) != 50 {
		t.Errorf("nodes = %d, names %d", v.NodeCount, len(v.NodeNames))
	}
}
