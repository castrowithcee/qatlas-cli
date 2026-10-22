package github

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

const secretUser = `{"login":"monalisa","id":2,"node_id":"MDQ6VXNlcjI=","avatar_url":"https://alambic.github.com/avatars/u/2?","gravatar_id":"","url":"https://api.github.com/users/monalisa","html_url":"https://github.com/monalisa","followers_url":"https://api.github.com/users/monalisa/followers","type":"User","site_admin":true}`

// The documented list example, with the location addresses of a commit and every comment field filled.
var secretListExample = fmt.Sprintf(`{"number":2,"created_at":"2020-11-06T18:48:51Z","url":"https://api.github.com/repos/owner/private-repo/secret-scanning/alerts/2","html_url":"https://github.com/owner/private-repo/security/secret-scanning/2","locations_url":"https://api.github.com/repos/owner/private-repo/secret-scanning/alerts/2/locations","state":"resolved","resolution":"false_positive","resolved_at":"2020-11-07T02:47:13Z","resolved_by":`+secretUser+`,"secret_type":"adafruit_io_key","secret_type_display_name":"Adafruit IO Key","provider":"Adafruit","provider_slug":"adafruit","secret":%[1]q,"push_protection_bypassed_by":`+secretUser+`,"push_protection_bypassed":true,"push_protection_bypassed_at":"2020-11-06T21:48:51Z","push_protection_bypass_request_reviewer":{"login":"octocat","id":3},"push_protection_bypass_request_reviewer_comment":"Example response","push_protection_bypass_request_comment":"Example comment","push_protection_bypass_request_html_url":"https://github.com/owner/repo/secret_scanning_exemptions/1","resolution_comment":"Example comment","validity":"inactive","publicly_leaked":false,"multi_repo":false,"is_base64_encoded":false,"first_location_detected":{"path":"/example/secrets.txt","start_line":1,"end_line":1,"start_column":1,"end_column":64,"blob_sha":"af5626b4a114abcb82d63db7c8082c3c4756e51b","blob_url":"https://api.github.com/repos/octocat/hello-world/git/blobs/af5626b4a114abcb82d63db7c8082c3c4756e51b","commit_sha":"f14d7debf9775f957cf4f1e8176da0786431f72b","commit_url":"https://api.github.com/repos/octocat/hello-world/git/commits/f14d7debf9775f957cf4f1e8176da0786431f72b","html_url":"https://github.com/octocat/hello-world/blob/af5626b/example/secrets.txt#L1-L1","page_url":"https://github.com/octocat/hello-world/wiki/Home","issue_comment_url":"https://api.github.com/repos/octocat/hello-world/issues/comments/1","snippet":%[1]q,"secret":%[1]q,"content":%[1]q,"resolution_comment":"Example comment"},"has_more_locations":true,"assigned_to":{"login":"octocat","id":1},"closure_request_comment":"Example comment","closure_request_reviewer_comment":"Example response","closure_request_reviewer":{"login":"hubot","id":4}}`, secretCanary)

func TestSecretScanningAlertGetAndListReportEveryDocumentedMember(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.secretScanning = secretListExample
	want := map[string]any{
		"url":                         "https://github.com/owner/private-repo/security/secret-scanning/2",
		"resolved_by":                 "monalisa",
		"provider":                    "Adafruit",
		"provider_slug":               "adafruit",
		"is_base64_encoded":           false,
		"has_more_locations":          true,
		"push_protection_bypassed":    true,
		"push_protection_bypassed_at": "2020-11-06T21:48:51Z",
		"path":                        "/example/secrets.txt",
		"links.url":                   "https://api.github.com/repos/owner/private-repo/secret-scanning/alerts/2",
		"links.locations_url":         "https://api.github.com/repos/owner/private-repo/secret-scanning/alerts/2/locations",
		"links.push_protection_bypass_request_html_url":   "https://github.com/owner/repo/secret_scanning_exemptions/1",
		"resolved_by_detail.login":                        "monalisa",
		"resolved_by_detail.id":                           float64(2),
		"resolved_by_detail.site_admin":                   true,
		"resolved_by_detail.url":                          "https://github.com/monalisa",
		"resolved_by_detail.links.url":                    "https://api.github.com/users/monalisa",
		"push_protection_bypassed_by.login":               "monalisa",
		"push_protection_bypass_request_reviewer.login":   "octocat",
		"assigned_to.login":                               "octocat",
		"assigned_to.id":                                  float64(1),
		"closure_request_reviewer.login":                  "hubot",
		"first_location_detected.path":                    "/example/secrets.txt",
		"first_location_detected.start_line":              float64(1),
		"first_location_detected.end_line":                float64(1),
		"first_location_detected.start_column":            float64(1),
		"first_location_detected.end_column":              float64(64),
		"first_location_detected.blob_sha":                "af5626b4a114abcb82d63db7c8082c3c4756e51b",
		"first_location_detected.commit_sha":              "f14d7debf9775f957cf4f1e8176da0786431f72b",
		"first_location_detected.url":                     "https://github.com/octocat/hello-world/blob/af5626b/example/secrets.txt#L1-L1",
		"first_location_detected.links.blob_url":          "https://api.github.com/repos/octocat/hello-world/git/blobs/af5626b4a114abcb82d63db7c8082c3c4756e51b",
		"first_location_detected.links.commit_url":        "https://api.github.com/repos/octocat/hello-world/git/commits/f14d7debf9775f957cf4f1e8176da0786431f72b",
		"first_location_detected.links.page_url":          "https://github.com/octocat/hello-world/wiki/Home",
		"first_location_detected.links.issue_comment_url": "https://api.github.com/repos/octocat/hello-world/issues/comments/1",
	}
	for _, tt := range []struct{ id, arguments, prefix string }{
		{secretScanningAlertsGet.ID, `{"alert_number":2}`, ""},
		{secretScanningAlertsList.ID, `{}`, "alerts.0."},
	} {
		got, err := invoke(t, core, tt.id, "repo", tt.arguments, false)
		if err != nil {
			t.Fatal(err)
		}
		expected := map[string]any{}
		for path, value := range want {
			expected[tt.prefix+path] = value
		}
		checkPaths(t, decodeAlert(t, got), expected)
		assertWithheld(t, tt.id, got)
	}
}

// assertWithheld fails when the answer holds the secret, a snippet, or a comment, by value or by name.
func assertWithheld(t *testing.T, id string, got []byte) {
	t.Helper()
	for _, banned := range []string{"aio_", "XXXX", "Example comment", "Example response", secretCanary, "CANARY",
		`"secret"`, `"snippet"`, `"content"`, "_comment\"", "resolution_comment", "request_comment", "reviewer_comment"} {
		if strings.Contains(string(got), banned) {
			t.Errorf("%s reports %q: %s", id, banned, got)
		}
	}
}

func TestSecretScanningAlertWithoutTheNewMembersIsUnchanged(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.secretScanning = `{"number":5,"state":"open","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` +
		`"html_url":"https://x/5","secret_type":"t","validity":"unknown","resolved_by":null,"assigned_to":null,` +
		`"first_location_detected":null,"provider":null,"has_more_locations":null}`
	want := `{"created_at":"2026-01-01T00:00:00Z","number":5,"repository":"octo-org/example","secret_type":"t",` +
		`"state":"open","updated_at":"2026-01-02T00:00:00Z","url":"https://x/5","validity":"unknown"}`
	got, err := invoke(t, core, secretScanningAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil || string(got) != want {
		t.Errorf("get = %s, %v; want %s", got, err, want)
	}
	// A member of an unexpected shape only drops itself.
	a.secretScanning = `{"number":5,"state":"open","provider":7,"is_base64_encoded":"yes","assigned_to":"x",` +
		`"first_location_detected":{"path":"a.go","end_column":"x"}}`
	got, err = invoke(t, core, secretScanningAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil || !strings.Contains(string(got), `"path":"a.go"`) && !strings.Contains(string(got), `"state":"open"`) {
		t.Errorf("get = %s, %v", got, err)
	}
	if strings.Contains(string(got), "provider") || strings.Contains(string(got), "assigned_to") {
		t.Errorf("unusable members reported: %s", got)
	}
}

func TestSecretScanningFlatMembersKeepTheirHandling(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	long := strings.Repeat("d", 1100)
	a.secretScanning = `{"number":5,"state":"open","secret_type":"t\u0007x","secret_type_display_name":"` + long + `",` +
		`"resolved_by":{"login":"oct\u0007o"},"first_location_detected":{"type":"co\u0007mmit","path":"a\u0007b/` + long + `"}}`
	got, err := invoke(t, core, secretScanningAlertsGet.ID, "repo", `{"alert_number":5}`, false)
	if err != nil {
		t.Fatal(err)
	}
	alert := decodeAlert(t, got)
	checkPaths(t, alert, map[string]any{"secret_type": "t\ax", "secret_type_display_name": long,
		"resolved_by": "oct\ao", "location_type": "co\ammit", "path": "a\ab/" + long,
		"resolved_by_detail.login": "oct o", "first_location_detected.path_truncated": true})
	if path, _ := at(t, alert, "first_location_detected", "path").(string); !strings.HasPrefix(path, "a b/") ||
		len([]rune(path)) != secretPathLimit {
		t.Errorf("the new path is not cleaned and cut: %q", path)
	}
	if _, ok := alert["truncated"]; ok {
		t.Errorf("truncated reported for secret scanning: %s", got)
	}
}

func TestSecretScanningNeverReportsSecretOrComments(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	a.secretScanning = strings.Replace(secretListExample, `"number":2,`, `"number":2,"secret_value":"`+secretCanary+`",`, 1)
	for _, tt := range []struct{ id, arguments string }{
		{secretScanningAlertsGet.ID, `{"alert_number":2}`}, {secretScanningAlertsList.ID, `{}`},
	} {
		got, err := invoke(t, core, tt.id, "repo", tt.arguments, false)
		if err != nil {
			t.Fatal(err)
		}
		assertWithheld(t, tt.id, got)
	}
	a.mode = "status"
	if _, err := invoke(t, core, secretScanningAlertsList.ID, "repo", `{}`, false); err == nil ||
		strings.Contains(err.Error(), "CANARY") {
		t.Errorf("error = %v", err)
	}
}

func TestSecretScanningFiltersReachTheQueryAndBindTheCursor(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	hash := strings.Repeat("ab", 32)
	filters := `"exclude_secret_types":["slack_token","aws_key"],"providers":["adafruit","clojars"],"assignee":"Octocat",` +
		`"validity":["active","unknown"],"is_publicly_leaked":true,"is_multi_repo":false,"is_bypassed":true,` +
		`"included_metadata":["owner-email","last-used-date"],"owner_email_hash":"` + hash + `",` +
		`"sort":"updated","direction":"asc"`
	first, err := invoke(t, core, secretScanningAlertsList.ID, "repo", `{`+filters+`,"limit":1}`, false)
	var page SecretScanningAlertList
	if err != nil || json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first = %s, %v", first, err)
	}
	query := a.queries[len(a.queries)-1]
	for _, want := range []string{"hide_secret=true", "exclude_secret_types=slack_token%2Caws_key",
		"providers=adafruit%2Cclojars", "assignee=octocat", "validity=active%2Cunknown", "is_publicly_leaked=true",
		"is_multi_repo=false", "is_bypassed=true", "included_metadata=owner-email%2Clast-used-date",
		"owner_email_hash=" + hash, "sort=updated", "direction=asc"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
	if _, err := invoke(t, core, secretScanningAlertsList.ID, "repo", `{"exclude_providers":["clojars"],"assignee":"none"}`,
		false); err != nil {
		t.Fatal(err)
	}
	if q := a.queries[len(a.queries)-1]; !strings.Contains(q, "exclude_providers=clojars") || !strings.Contains(q, "assignee=none") {
		t.Errorf("query %q lacks exclude_providers or assignee", q)
	}
	cursor := `"cursor":"` + page.NextCursor + `"`
	for _, other := range []string{
		`"exclude_secret_types":["slack_token"]`, `"providers":["adafruit"]`, `"assignee":"*"`, `"validity":["active"]`,
		`"is_publicly_leaked":false`, `"is_multi_repo":true`, `"is_bypassed":false`, `"included_metadata":["owner-id"]`,
		`"owner_email_hash":"` + strings.Repeat("cd", 32) + `"`, `"sort":"created"`, `"direction":"desc"`, `"state":"open"`,
	} {
		name := other[:strings.Index(other, ":")]
		replaced := replaceMember(filters, name, other)
		if _, err := invoke(t, core, secretScanningAlertsList.ID, "repo", `{`+replaced+`,`+cursor+`}`,
			false); !isInvalidRequest(err) {
			t.Errorf("cursor with %s = %v, want invalid request", other, err)
		}
	}
	if _, err := invoke(t, core, secretScanningAlertsList.ID, "repo", `{`+filters+`,`+cursor+`}`, false); err != nil {
		t.Errorf("continuation = %v", err)
	}
	// Every request, with or without filters, asks GitHub to hide the secret; one without filters sends none.
	_, plainFake, plain, _ := serveAlerts(t, "")
	if _, err := invoke(t, plain, secretScanningAlertsList.ID, "repo", `{"state":"open"}`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, plain, secretScanningAlertsGet.ID, "repo", `{"alert_number":3}`, false); err != nil {
		t.Fatal(err)
	}
	for _, q := range append(plainFake.queries, a.queries...) {
		if strings.HasPrefix(q, "secret-scanning") && !strings.Contains(q, "hide_secret=true") {
			t.Errorf("request %q does not hide the secret", q)
		}
	}
	for _, absent := range []string{"exclude_secret_types=", "providers=", "assignee=", "validity=", "is_publicly_leaked=",
		"is_multi_repo=", "is_bypassed=", "included_metadata=", "owner_email_hash=", "sort=", "direction="} {
		if strings.Contains(strings.Join(plainFake.queries, "\n"), absent) {
			t.Errorf("a request without the new filters sends %s", absent)
		}
	}
	bound, _ := parseTarget(repoTarget)
	args := alertArguments{State: "open", Limit: 1}
	if err := listAlertsCheck("secret-scanning", secretStates, nil)(&args, bound); err != nil {
		t.Fatal(err)
	}
	old := fingerprint("security-alerts", "secret-scanning", bound.String(), "open", "", "", "", "", "")
	if string(args.binding) != string(old) {
		t.Errorf("fingerprint without the new filters changed")
	}
}

func TestSecretScanningFilterValuesAreCheckedBeforeIO(t *testing.T) {
	f, _, core, reads := serveAlerts(t, "")
	names := func(n int) string {
		var items []string
		for i := 0; i < n; i++ {
			items = append(items, `"a_`+strconv.Itoa(i)+`"`)
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	for _, arguments := range []string{
		`{"secret_type":"a","exclude_secret_types":["b"]}`,
		`{"providers":["a"],"exclude_providers":["b"]}`,
		`{"exclude_secret_types":` + names(21) + `}`, `{"providers":` + names(21) + `}`, `{"exclude_providers":` + names(21) + `}`,
		`{"exclude_secret_types":["a b"]}`, `{"providers":["a,b"]}`, `{"exclude_providers":[""]}`,
		`{"providers":["` + strings.Repeat("p", 101) + `"]}`,
		`{"assignee":"bad login"}`, `{"assignee":""}`, `{"assignee":["octocat"]}`,
		`{"validity":["maybe"]}`, `{"validity":["active","ACTIVE"]}`,
		`{"included_metadata":["owner-phone"]}`, `{"included_metadata":["owner-id","owner-id"]}`,
		`{"owner_email_hash":"abc"}`, `{"owner_email_hash":"` + strings.Repeat("AB", 32) + `"}`,
		`{"owner_email_hash":"` + strings.Repeat("g", 64) + `"}`, `{"owner_email_hash":"` + strings.Repeat("a", 65) + `"}`,
		`{"is_bypassed":"yes"}`, `{"sort":"name"}`, `{"direction":"up"}`,
	} {
		*reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, secretScanningAlertsList.ID, "repo", arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", arguments, err)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", arguments)
		}
	}
	for _, arguments := range []string{`{"assignee":"*"}`, `{"assignee":"none"}`, `{"providers":` + names(20) + `}`,
		`{"owner_email_hash":"` + strings.Repeat("0f", 32) + `"}`, `{"is_bypassed":false}`} {
		if _, err := invoke(t, core, secretScanningAlertsList.ID, "repo", arguments, false); err != nil {
			t.Errorf("%s = %v", arguments, err)
		}
	}
}
