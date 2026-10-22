package github

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// The documented examples of the Dependabot alert, the open one with a dismissal request added.
const dependabotOpenAlert = `{"number":1,"state":"open","dependency":{"package":{"ecosystem":"pip","name":"ansible"},"manifest_path":"path/to/requirements.txt","version_requirement":"= 2.8.18","scope":"runtime","relationship":"direct"},"security_advisory":{"ghsa_id":"GHSA-8f4m-hccc-8qph","cve_id":"CVE-2021-20191","summary":"Insertion of Sensitive Information into Log File in ansible","description":"A flaw was found in ansible. Credentials, such as secrets, are being disclosed in console log by default and not protected by no_log feature when using those modules. An attacker can take advantage of this information to steal those credentials. The highest threat from this vulnerability is to data confidentiality.","vulnerabilities":[{"package":{"ecosystem":"pip","name":"ansible"},"severity":"medium","vulnerable_version_range":">= 2.9.0, < 2.9.18","first_patched_version":{"identifier":"2.9.18"}},{"package":{"ecosystem":"pip","name":"ansible"},"severity":"medium","vulnerable_version_range":"< 2.8.19","first_patched_version":{"identifier":"2.8.19"}},{"package":{"ecosystem":"pip","name":"ansible"},"severity":"medium","vulnerable_version_range":">= 2.10.0, < 2.10.7","first_patched_version":{"identifier":"2.10.7"}}],"severity":"medium","cvss":{"vector_string":"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N","score":5.5},"cvss_severities":{"cvss_v3":{"vector_string":"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N","score":5.5},"cvss_v4":{"vector_string":"CVSS:4.0/AV:L/AC:L/AT:N/PR:N/UI:P/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N","score":8.5}},"epss":{"percentage":0.00045,"percentile":"0.16001e0"},"cwes":[{"cwe_id":"CWE-532","name":"Insertion of Sensitive Information into Log File"}],"identifiers":[{"type":"GHSA","value":"GHSA-8f4m-hccc-8qph"},{"type":"CVE","value":"CVE-2021-20191"}],"references":[{"url":"https://nvd.nist.gov/vuln/detail/CVE-2021-20191"},{"url":"https://access.redhat.com/security/cve/cve-2021-20191"},{"url":"https://bugzilla.redhat.com/show_bug.cgi?id=1916813"}],"published_at":"2021-06-01T17:38:00Z","updated_at":"2021-08-12T23:06:00Z","withdrawn_at":null},"security_vulnerability":{"package":{"ecosystem":"pip","name":"ansible"},"severity":"medium","vulnerable_version_range":"< 2.8.19","first_patched_version":{"identifier":"2.8.19"}},"url":"https://api.github.com/repos/octocat/hello-world/dependabot/alerts/1","html_url":"https://github.com/octocat/hello-world/security/dependabot/1","created_at":"2022-06-14T15:21:52Z","updated_at":"2022-06-14T15:21:52Z","dismissed_at":null,"dismissed_by":null,"dismissed_reason":null,"dismissed_comment":null,"fixed_at":null,"assignees":[{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","avatar_url":"https://github.com/images/error/octocat_happy.gif","gravatar_id":"","url":"https://api.github.com/users/octocat","html_url":"https://github.com/octocat","followers_url":"https://api.github.com/users/octocat/followers","following_url":"https://api.github.com/users/octocat/following{/other_user}","gists_url":"https://api.github.com/users/octocat/gists{/gist_id}","starred_url":"https://api.github.com/users/octocat/starred{/owner}{/repo}","subscriptions_url":"https://api.github.com/users/octocat/subscriptions","organizations_url":"https://api.github.com/users/octocat/orgs","repos_url":"https://api.github.com/users/octocat/repos","events_url":"https://api.github.com/users/octocat/events{/privacy}","received_events_url":"https://api.github.com/users/octocat/received_events","type":"User","site_admin":false}],"dismissal_request":{"id":9,"status":"pending","requester":{"id":3,"login":"hubot"},"created_at":"2022-06-15T00:00:00Z","url":"https://api.github.com/repos/octocat/hello-world/dependabot/alerts/1/dismissal-request/9"},"auto_dismissed_at":null}`

const dependabotDismissedAlert = `{"number":2,"state":"dismissed","dependency":{"package":{"ecosystem":"pip","name":"django"},"manifest_path":"path/to/requirements.txt","version_requirement":">= 2.0.0, < 2.0.2","scope":"runtime","relationship":"direct"},"security_advisory":{"ghsa_id":"GHSA-rf4j-j272-fj86","cve_id":"CVE-2018-6188","summary":"Django allows remote attackers to obtain potentially sensitive information by leveraging data exposure from the confirm_login_allowed() method, as demonstrated by discovering whether a user account is inactive","description":"django.contrib.auth.forms.AuthenticationForm in Django 2.0 before 2.0.2, and 1.11.8 and 1.11.9, allows remote attackers to obtain potentially sensitive information by leveraging data exposure from the confirm_login_allowed() method, as demonstrated by discovering whether a user account is inactive.","vulnerabilities":[{"package":{"ecosystem":"pip","name":"django"},"severity":"high","vulnerable_version_range":">= 2.0.0, < 2.0.2","first_patched_version":{"identifier":"2.0.2"}},{"package":{"ecosystem":"pip","name":"django"},"severity":"high","vulnerable_version_range":">= 1.11.8, < 1.11.10","first_patched_version":{"identifier":"1.11.10"}}],"severity":"high","cvss":{"vector_string":"CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:N","score":7.5},"cvss_severities":{"cvss_v3":{"vector_string":"CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:N","score":7.5},"cvss_v4":{"vector_string":"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N","score":8.7}},"cwes":[{"cwe_id":"CWE-200","name":"Exposure of Sensitive Information to an Unauthorized Actor"}],"identifiers":[{"type":"GHSA","value":"GHSA-rf4j-j272-fj86"},{"type":"CVE","value":"CVE-2018-6188"}],"references":[{"url":"https://nvd.nist.gov/vuln/detail/CVE-2018-6188"},{"url":"https://github.com/advisories/GHSA-rf4j-j272-fj86"},{"url":"https://usn.ubuntu.com/3559-1/"},{"url":"https://www.djangoproject.com/weblog/2018/feb/01/security-releases/"},{"url":"http://www.securitytracker.com/id/1040422"}],"published_at":"2018-10-03T21:13:54Z","updated_at":"2022-04-26T18:35:37Z","withdrawn_at":null},"security_vulnerability":{"package":{"ecosystem":"pip","name":"django"},"severity":"high","vulnerable_version_range":">= 2.0.0, < 2.0.2","first_patched_version":{"identifier":"2.0.2"}},"url":"https://api.github.com/repos/octocat/hello-world/dependabot/alerts/2","html_url":"https://github.com/octocat/hello-world/security/dependabot/2","created_at":"2022-06-15T07:43:03Z","updated_at":"2022-08-23T14:29:47Z","dismissed_at":"2022-08-23T14:29:47Z","dismissed_by":{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","avatar_url":"https://github.com/images/error/octocat_happy.gif","gravatar_id":"","url":"https://api.github.com/users/octocat","html_url":"https://github.com/octocat","followers_url":"https://api.github.com/users/octocat/followers","following_url":"https://api.github.com/users/octocat/following{/other_user}","gists_url":"https://api.github.com/users/octocat/gists{/gist_id}","starred_url":"https://api.github.com/users/octocat/starred{/owner}{/repo}","subscriptions_url":"https://api.github.com/users/octocat/subscriptions","organizations_url":"https://api.github.com/users/octocat/orgs","repos_url":"https://api.github.com/users/octocat/repos","events_url":"https://api.github.com/users/octocat/events{/privacy}","received_events_url":"https://api.github.com/users/octocat/received_events","type":"User","site_admin":false},"dismissed_reason":"tolerable_risk","dismissed_comment":"This alert is accurate but we use a sanitizer.","fixed_at":null,"assignees":[]}`

func checkPaths(t *testing.T, alert any, want map[string]any) {
	t.Helper()
	for path, expected := range want {
		var steps []any
		for _, part := range strings.Split(path, ".") {
			if n, err := strconv.Atoi(part); err == nil {
				steps = append(steps, n)
			} else {
				steps = append(steps, part)
			}
		}
		if got := at(t, alert, steps...); got != expected {
			t.Errorf("%s = %v, want %v", path, got, expected)
		}
	}
}

func TestDependabotAlertGetReportsEveryDocumentedMember(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.dependabot = dependabotOpenAlert
	got, err := invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":1}`, false)
	if err != nil {
		t.Fatal(err)
	}
	alert := decodeAlert(t, got)
	checkPaths(t, alert, map[string]any{
		"url":                            "https://github.com/octocat/hello-world/security/dependabot/1",
		"links.url":                      "https://api.github.com/repos/octocat/hello-world/dependabot/alerts/1",
		"package":                        "ansible",
		"cvss_score":                     5.5,
		"first_patched_version":          "2.8.19",
		"dependency.package.ecosystem":   "pip",
		"dependency.package.name":        "ansible",
		"dependency.manifest_path":       "path/to/requirements.txt",
		"dependency.version_requirement": "= 2.8.18",
		"dependency.scope":               "runtime",
		"dependency.relationship":        "direct",
		"security_advisory.ghsa_id":      "GHSA-8f4m-hccc-8qph",
		"security_advisory.cve_id":       "CVE-2021-20191",
		"security_advisory.summary":      "Insertion of Sensitive Information into Log File in ansible",
		"security_advisory.severity":     "medium",
		"security_advisory.vulnerabilities.0.package.name":             "ansible",
		"security_advisory.vulnerabilities.0.severity":                 "medium",
		"security_advisory.vulnerabilities.0.vulnerable_version_range": ">= 2.9.0, < 2.9.18",
		"security_advisory.vulnerabilities.0.first_patched_version":    "2.9.18",
		"security_advisory.vulnerabilities.2.first_patched_version":    "2.10.7",
		"security_advisory.cvss.score":                                 5.5,
		"security_advisory.cvss.vector_string":                         "CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N",
		"security_advisory.cvss_severities.cvss_v3.score":              5.5,
		"security_advisory.cvss_severities.cvss_v4.score":              8.5,
		"security_advisory.cvss_severities.cvss_v4.vector_string":      "CVSS:4.0/AV:L/AC:L/AT:N/PR:N/UI:P/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N",
		"security_advisory.epss.percentage":                            0.00045,
		"security_advisory.epss.percentile":                            0.16001,
		"security_advisory.cwes.0.cwe_id":                              "CWE-532",
		"security_advisory.cwes.0.name":                                "Insertion of Sensitive Information into Log File",
		"security_advisory.identifiers.1.type":                         "CVE",
		"security_advisory.identifiers.1.value":                        "CVE-2021-20191",
		"security_advisory.references.2.url":                           "https://bugzilla.redhat.com/show_bug.cgi?id=1916813",
		"security_advisory.published_at":                               "2021-06-01T17:38:00Z",
		"security_advisory.updated_at":                                 "2021-08-12T23:06:00Z",
		"security_vulnerability.package.ecosystem":                     "pip",
		"security_vulnerability.severity":                              "medium",
		"security_vulnerability.vulnerable_version_range":              "< 2.8.19",
		"security_vulnerability.first_patched_version":                 "2.8.19",
		"assignees.0.login":                                            "octocat",
		"assignees.0.id":                                               float64(1),
		"assignees.0.url":                                              "https://github.com/octocat",
		"assignees.0.links.url":                                        "https://api.github.com/users/octocat",
		"dismissal_request.id":                                         float64(9),
		"dismissal_request.status":                                     "pending",
		"dismissal_request.requester.login":                            "hubot",
		"dismissal_request.requester.id":                               float64(3),
		"dismissal_request.created_at":                                 "2022-06-15T00:00:00Z",
		"dismissal_request.links.url":                                  "https://api.github.com/repos/octocat/hello-world/dependabot/alerts/1/dismissal-request/9",
	})
	// Numbers pass as GitHub wrote them, not through a float.
	for _, want := range []string{`"percentage":0.00045`, `"percentile":0.16001e0`, `"score":5.5`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("answer lacks %s: %s", want, got)
		}
	}
	for _, absent := range []string{"truncated", "dismissed_by_detail", "withdrawn_at", "classification"} {
		if strings.Contains(string(got), absent) {
			t.Errorf("answer carries %s without a value or cut: %s", absent, got)
		}
	}

	a.dependabot = dependabotDismissedAlert
	got, err = invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":2}`, false)
	if err != nil {
		t.Fatal(err)
	}
	alert = decodeAlert(t, got)
	checkPaths(t, alert, map[string]any{"dismissed_by": "octocat", "dismissed_by_detail.login": "octocat",
		"dismissed_by_detail.id": float64(1), "dismissed_by_detail.url": "https://github.com/octocat",
		"dismissed_by_detail.links.url": "https://api.github.com/users/octocat",
		"dismissed_reason":              "tolerable_risk", "security_advisory.cvss_severities.cvss_v4.score": 8.7})
	if _, ok := alert["assignees"]; ok {
		t.Errorf("empty assignees reported: %s", got)
	}
}

func TestDependabotAlertListReportsTheDocumentedMembers(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.dependabot = dependabotOpenAlert
	got, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	checkPaths(t, decodeAlert(t, got), map[string]any{"alerts.0.dependency.package.name": "ansible",
		"alerts.0.security_advisory.cwes.0.cwe_id": "CWE-532", "alerts.0.security_advisory.epss.percentile": 0.16001,
		"alerts.0.assignees.0.login": "octocat", "alerts.0.links.url": "https://api.github.com/repos/octocat/hello-world/dependabot/alerts/1"})
	if strings.Contains(string(got), `"description"`) == false {
		t.Errorf("the advisory description is missing: %s", got)
	}
}

func TestDependabotAlertWithoutTheNewMembersIsUnchanged(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.dependabot = `{"number":5,"state":"open","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
		`"html_url":"https://x/5","dismissed_reason":"tolerable_risk","dismissed_comment":"ok","dismissed_by":{"login":"octo"},` +
		`"assignees":[],"dismissal_request":null,"dependency":{},"security_advisory":null}`
	want := `{"created_at":"2026-01-01T00:00:00Z","dismissed_by":"octo","dismissed_comment":"ok",` +
		`"dismissed_reason":"tolerable_risk","number":5,"repository":"octo-org/example","state":"open",` +
		`"updated_at":"2026-01-02T00:00:00Z","url":"https://x/5"}`
	// The dismissed_by account is a new member too: it reports next to the flat login.
	want = strings.Replace(want, `"dismissed_comment"`, `"dismissed_by_detail":{"login":"octo"},"dismissed_comment"`, 1)
	got, err := invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil || string(got) != want {
		t.Errorf("get = %s, %v; want %s", got, err, want)
	}
	a.dependabot = `{"number":5,"state":"open","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
		`"html_url":"https://x/5","dismissed_reason":"tolerable_risk","dismissed_comment":"ok"}`
	want = `{"created_at":"2026-01-01T00:00:00Z","dismissed_comment":"ok","dismissed_reason":"tolerable_risk",` +
		`"number":5,"repository":"octo-org/example","state":"open","updated_at":"2026-01-02T00:00:00Z",` +
		`"url":"https://x/5"}`
	got, err = invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil || string(got) != want {
		t.Errorf("get = %s, %v; want %s", got, err, want)
	}
}

func TestDependabotFlatMembersKeepTheirHandling(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.dependabot = `{"number":5,"state":"open","dismissed_comment":"c\u0007` + strings.Repeat("c", 600) + `",` +
		`"dismissed_by":{"login":"oct\u0007o"},"dependency":{"package":{"ecosystem":"npm","name":"p\u0007"},` +
		`"manifest_path":"m\u0007` + strings.Repeat("m", 1100) + `"},"security_advisory":{"ghsa_id":"G\u0007",` +
		`"summary":"s\u0007` + strings.Repeat("s", 600) + `","description":"d\u0007` + strings.Repeat("d", 5000) + `",` +
		`"cwes":[{"cwe_id":"CWE-1","name":"n\u0007"}],"cvss":{"score":7.5}}}`
	got, err := invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil {
		t.Fatal(err)
	}
	alert := decodeAlert(t, got)
	for member, want := range map[string]string{"dismissed_by": "oct\x07o", "package": "p\x07", "ghsa_id": "G\x07",
		"dismissed_comment": "c\x07" + strings.Repeat("c", 498), "summary": "s\x07" + strings.Repeat("s", 498),
		"description": "d\x07" + strings.Repeat("d", 3998)} {
		if alert[member] != want {
			t.Errorf("%s = %q, want %q", member, alert[member], want)
		}
	}
	if alert["truncated"] != true || alert["cvss_score"] != 7.5 {
		t.Errorf("truncated or cvss_score changed: %s", got)
	}
	advisory := alert["security_advisory"].(map[string]any)
	if advisory["ghsa_id"] != "G " || advisory["summary"] != "s "+strings.Repeat("s", 600) ||
		advisory["summary_truncated"] != nil || advisory["description"] != "d "+strings.Repeat("d", 3998) ||
		advisory["description_truncated"] != true {
		t.Errorf("security_advisory = %v", advisory)
	}
	checkPaths(t, alert, map[string]any{"dependency.package.name": "p ",
		"dismissed_by_detail.login": "oct o", "security_advisory.cwes.0.name": "n "})
	if len([]rune(at(t, alert, "dependency", "manifest_path").(string))) != 1024 ||
		at(t, alert, "dependency", "manifest_path_truncated") != true {
		t.Errorf("manifest_path not cut with its marker")
	}
	// A list cuts the advisory description shorter.
	got, err = invoke(t, core, dependabotAlertsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := at(t, decodeAlert(t, got), "alerts", 0)
	if d := at(t, entry, "security_advisory", "description").(string); len([]rune(d)) != 500 ||
		at(t, entry, "security_advisory", "description_truncated") != true {
		t.Errorf("list description = %d runes", len([]rune(d)))
	}
	if _, ok := entry.(map[string]any)["description"]; ok {
		t.Errorf("list carries the flat description")
	}
}

func TestDependabotListsAreCappedWithMarkers(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	many := func(item string) string { return "[" + strings.TrimSuffix(strings.Repeat(item+",", 101), ",") + "]" }
	a.dependabot = `{"number":5,"state":"open","assignees":` + many(`{"login":"a"}`) + `,"security_advisory":{` +
		`"ghsa_id":"G","vulnerabilities":` + many(`{"severity":"low"}`) + `,"cwes":` + many(`{"cwe_id":"CWE-1"}`) +
		`,"identifiers":` + many(`{"type":"CVE","value":"v"}`) + `,"references":` + many(`{"url":"https://x"}`) + `}}`
	got, err := invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil {
		t.Fatal(err)
	}
	alert := decodeAlert(t, got)
	if len(alert["assignees"].([]any)) != 100 || alert["assignees_truncated"] != true {
		t.Errorf("assignees not capped with a marker")
	}
	advisory := alert["security_advisory"].(map[string]any)
	for _, name := range []string{"vulnerabilities", "cwes", "identifiers", "references"} {
		if len(advisory[name].([]any)) != 100 || advisory[name+"_truncated"] != true {
			t.Errorf("%s not capped with a marker", name)
		}
	}
}

func TestDependabotUnusableNewMembersOnlyDropThemselves(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.dependabot = `{"number":5,"state":"open","assignees":{"a":1},"dismissal_request":[],` +
		`"security_advisory":{"ghsa_id":"G","epss":{"percentage":"x"},"cwes":"x"}}`
	got, err := invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil {
		t.Fatal(err)
	}
	alert := decodeAlert(t, got)
	if alert["number"] != float64(5) || strings.Contains(string(got), "epss") || alert["assignees"] != nil {
		t.Errorf("answer = %s", got)
	}
}

func TestDependabotFiltersReachTheQueryAndBindTheCursor(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	filters := `"classification":["malware","general"],"ecosystem":["npm","go"],"package":["lodash","@a/b"],` +
		`"manifest":["package.json","go.mod"],"epss_min":0.25,"epss_max":0.75,"has":["patch"],` +
		`"assignee":["octocat","hubot"],"scope":"runtime","relationship":["direct","transitive"],` +
		`"sort":"epss_percentage","direction":"asc"`
	first, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{`+filters+`,"limit":1}`, false)
	var page DependabotAlertList
	if err != nil || json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first = %s, %v", first, err)
	}
	query := a.queries[len(a.queries)-1]
	for _, want := range []string{"classification=malware%2Cgeneral", "ecosystem=npm%2Cgo", "package=lodash%2C%40a%2Fb",
		"manifest=package.json%2Cgo.mod", "epss_percentage=0.25..0.75", "has=patch", "assignee=octocat%2Chubot",
		"scope=runtime", "relationship=direct%2Ctransitive", "sort=epss_percentage", "direction=asc"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
	for form, want := range map[string]string{`"epss_min":0.5`: "epss_percentage=%3E%3D0.5", `"epss_max":0.5`: "epss_percentage=%3C%3D0.5",
		`"epss_min":0.5,"epss_max":0.5`: "epss_percentage=0.5", `"epss_min":0,"epss_max":1`: "epss_percentage=0..1"} {
		if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{`+form+`}`, false); err != nil {
			t.Fatal(err)
		}
		if q := a.queries[len(a.queries)-1]; !strings.Contains(q, want) {
			t.Errorf("%s: query %q lacks %q", form, q, want)
		}
	}
	cursor := `"cursor":"` + page.NextCursor + `"`
	for _, other := range []string{
		`"classification":["malware"]`, `"ecosystem":["npm"]`, `"package":["lodash"]`, `"manifest":["go.mod"]`,
		`"epss_min":0.3`, `"epss_max":0.8`, `"has":[]`, `"assignee":["octocat"]`, `"scope":"development"`,
		`"relationship":["direct"]`, `"sort":"created"`, `"direction":"desc"`, `"state":"open"`,
	} {
		// Each variation replaces the matching filter and keeps the rest.
		name := other[:strings.Index(other, ":")]
		replaced := replaceMember(filters, name, other)
		if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{`+replaced+`,`+cursor+`}`,
			false); !isInvalidRequest(err) {
			t.Errorf("cursor with %s = %v, want invalid request", other, err)
		}
	}
	if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{`+filters+`,`+cursor+`}`, false); err != nil {
		t.Errorf("continuation = %v", err)
	}
	// Without the new filters the request and the fingerprint stay as they were.
	_, plainFake, plain, _ := serveAlerts(t, "")
	firstPlain, _ := invoke(t, plain, dependabotAlertsList.ID, "repo", `{"state":"open","limit":1}`, false)
	var plainPage DependabotAlertList
	if json.Unmarshal(firstPlain, &plainPage) != nil || plainPage.NextCursor == "" {
		t.Fatalf("plain first = %s", firstPlain)
	}
	if _, err := invoke(t, plain, dependabotAlertsList.ID, "repo",
		`{"state":"open","cursor":"`+plainPage.NextCursor+`"}`, false); err != nil {
		t.Errorf("plain continuation = %v", err)
	}
	for _, absent := range []string{"classification=", "ecosystem=", "package=", "manifest=", "epss_percentage=", "has=",
		"assignee=", "scope=", "relationship=", "sort=", "direction="} {
		if strings.Contains(strings.Join(plainFake.queries, "\n"), absent) {
			t.Errorf("a request without the new filters sends %s", absent)
		}
	}
	bound, _ := parseTarget(repoTarget)
	args := alertArguments{State: "open", Limit: 1}
	if err := listAlertsCheck("dependabot", dependabotStates, dependabotSeverities)(&args, bound); err != nil {
		t.Fatal(err)
	}
	old := fingerprint("security-alerts", "dependabot", bound.String(), "open", "", "", "", "", "")
	if string(args.binding) != string(old) {
		t.Errorf("fingerprint without the new filters changed")
	}
}

// replaceMember swaps the member name in a comma-separated JSON member list for other, or appends it.
func replaceMember(members, name, other string) string {
	var out []string
	var current string
	depth := 0
	var parts []string
	for _, r := range members {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		}
		if r == ',' && depth == 0 {
			parts = append(parts, current)
			current = ""
			continue
		}
		current += string(r)
	}
	parts = append(parts, current)
	found := false
	for _, part := range parts {
		if strings.HasPrefix(part, name+":") {
			out = append(out, other)
			found = true
			continue
		}
		out = append(out, part)
	}
	if !found {
		out = append(out, other)
	}
	return strings.Join(out, ",")
}

func TestDependabotFilterValuesAreCheckedBeforeIO(t *testing.T) {
	f, _, core, reads := serveAlerts(t, "")
	names := func(n int, value string) string {
		return "[" + strings.TrimSuffix(strings.Repeat(`"`+value+`",`, n), ",") + "]"
	}
	for _, arguments := range []string{
		`{"classification":["other"]}`, `{"classification":["malware","MALWARE"]}`, `{"ecosystem":["cargo"]}`,
		`{"has":["fix"]}`, `{"has":["patch","patch"]}`, `{"scope":"test"}`, `{"relationship":["child"]}`,
		`{"sort":"name"}`, `{"direction":"up"}`, `{"epss_min":1.5}`, `{"epss_max":-0.1}`, `{"epss_min":0.8,"epss_max":0.2}`,
		`{"package":` + names(11, "p") + `}`, `{"package":["` + strings.Repeat("p", 257) + `"]}`, `{"package":["a,b"]}`,
		`{"package":[""]}`, `{"package":["a\u0007b"]}`, `{"manifest":` + names(11, "m") + `}`,
		`{"manifest":["` + strings.Repeat("m", 1025) + `"]}`, `{"manifest":["a,b"]}`,
		`{"assignee":` + names(11, "a") + `}`, `{"assignee":["*","octocat"]}`, `{"assignee":["none","octocat"]}`,
		`{"assignee":["bad login"]}`, `{"assignee":[""]}`,
	} {
		*reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", arguments, err)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", arguments)
		}
	}
	for _, arguments := range []string{`{"assignee":["*"]}`, `{"assignee":["none"]}`, `{"package":["` + strings.Repeat("p", 256) + `"]}`,
		`{"manifest":["` + strings.Repeat("m", 1024) + `"]}`, `{"epss_min":0,"epss_max":1}`} {
		if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", arguments, false); err != nil {
			t.Errorf("%s = %v", arguments, err)
		}
	}
}
