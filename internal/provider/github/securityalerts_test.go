package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const secretCanary = "ghp_CANARYsecretValue0123456789abcdef"

// alertFake answers the three alert routes of the bound repository. mode selects a failure shape for the
// secret scanning routes; every secret scanning answer carries the canary in every value-bearing field.
type alertFake struct {
	f    *fakeGitHub
	mode string
	// queries records the raw query of every request by route.
	queries []string
}

func secretAlertJSON(number int) string {
	return fmt.Sprintf(`{"number":%d,"state":"open","created_at":"2026-09-01T00:00:00Z","html_url":"https://x/%d",`+
		`"secret_type":"github_personal_access_token","secret_type_display_name":"GitHub PAT","validity":"active",`+
		`"secret":%q,"secret_value":%q,"resolution_comment":%q,"first_location_detected":{"type":"commit",`+
		`"path":"src/a.go","start_line":3,"end_line":3,"blob_sha":"b","snippet":%q,"secret":%q},`+
		`"locations":[{"secret":%q}],"variants":[%q],"resolved_by":null}`,
		number, number, secretCanary, secretCanary, secretCanary, secretCanary, secretCanary, secretCanary, secretCanary)
}

func (a *alertFake) route(w http.ResponseWriter, req *http.Request) bool {
	rest, ok := strings.CutPrefix(req.URL.Path, actionsPrefix)
	if !ok {
		return false
	}
	kind, tail, _ := strings.Cut(rest, "/alerts")
	if kind != "code-scanning" && kind != "dependabot" && kind != "secret-scanning" {
		return false
	}
	a.queries = append(a.queries, kind+"?"+req.URL.RawQuery)
	w.Header().Set("Content-Type", "application/json")
	if kind == "secret-scanning" {
		switch a.mode {
		case "bad-json":
			fmt.Fprint(w, `{"secret":"`+secretCanary+`" broken`)
			return true
		case "status":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"boom `+secretCanary+`"}`)
			return true
		case "forbidden":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"no `+secretCanary+`"}`)
			return true
		case "huge":
			fmt.Fprint(w, `[{"number":1,"state":"open","secret":"`+secretCanary+strings.Repeat("x", maxResponseBytes)+`"}]`)
			return true
		}
	}
	number := strings.Trim(tail, "/")
	if number == "404" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
		return true
	}
	var single, list string
	switch kind {
	case "code-scanning":
		single = `{"number":42,"state":"open","html_url":"https://x/42","created_at":"2026-09-01T00:00:00Z",` +
			`"dismissed_by":{"login":"octocat"},"dismissed_comment":"` + strings.Repeat("c", 600) + `",` +
			`"rule":{"id":"go/sql-injection","name":"sql","severity":"error","security_severity_level":"high",` +
			`"description":"SQL injection","help":"help text","tags":["security"]},"tool":{"name":"CodeQL","version":"2.1"},` +
			`"most_recent_instance":{"ref":"refs/heads/main","commit_sha":"abc","message":{"text":"tainted"},` +
			`"location":{"path":"a.go","start_line":7,"end_line":8}}}`
	case "dependabot":
		single = `{"number":7,"state":"open","html_url":"https://x/7","dependency":{"package":{"ecosystem":"npm",` +
			`"name":"lodash"},"manifest_path":"package.json","scope":"runtime","relationship":"direct"},` +
			`"security_advisory":{"ghsa_id":"GHSA-xxxx","cve_id":"CVE-2026-1","summary":"Prototype pollution",` +
			`"description":"details","severity":"high","cvss":{"score":7.5}},"security_vulnerability":` +
			`{"vulnerable_version_range":"<4.17.21","first_patched_version":{"identifier":"4.17.21"}}}`
	default:
		single = secretAlertJSON(3)
	}
	list = "[" + single + "]"
	if number == "" {
		if a.f != nil && req.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
		}
		fmt.Fprint(w, list)
		return true
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(number, "/")); err != nil {
		w.WriteHeader(http.StatusNotFound)
		return true
	}
	fmt.Fprint(w, single)
	return true
}

func serveAlerts(t *testing.T, mode string) (*fakeGitHub, *alertFake, *application.Core, *int) {
	t.Helper()
	a := &alertFake{mode: mode}
	f := &fakeGitHub{}
	a.f = f
	f.failure = a.route
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	cfg := coreConfig(base)
	cfg.Connections["repo"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget,
		Permissions: []config.Permission{config.PermissionRead}}
	return f, a, application.New(registry(t), cfg, resolver(red, &reads), red), &reads
}

func TestSecurityAlertToolsSatisfyTheirContract(t *testing.T) {
	_, a, core, _ := serveAlerts(t, "")
	for _, tt := range []struct{ id, arguments, want string }{
		{codeScanningAlertsList.ID, `{"state":"open","severity":"high","tool_name":"CodeQL","ref":"refs/heads/main"}`, `"rule_id":"go/sql-injection"`},
		{codeScanningAlertsGet.ID, `{"alert_number":42}`, `"help":"help text"`},
		{dependabotAlertsList.ID, `{"state":"open","severity":"high"}`, `"package":"lodash"`},
		{dependabotAlertsGet.ID, `{"alert_number":7}`, `"first_patched_version":"4.17.21"`},
		{secretScanningAlertsList.ID, `{"state":"open","secret_type":"github_personal_access_token","resolution":"revoked"}`, `"secret_type":"github_personal_access_token"`},
		{secretScanningAlertsGet.ID, `{"alert_number":3}`, `"path":"src/a.go"`},
	} {
		got, err := invoke(t, core, tt.id, "repo", tt.arguments, false)
		if err != nil || !strings.Contains(string(got), tt.want) ||
			!strings.Contains(string(got), `"repository":"octo-org/example"`) {
			t.Errorf("%s = %s, %v; want %s", tt.id, got, err, tt.want)
		}
	}
	joined := strings.Join(a.queries, "\n")
	for _, want := range []string{"code-scanning?", "state=open", "severity=high", "tool_name=CodeQL",
		"ref=refs%2Fheads%2Fmain", "hide_secret=true", "secret_type=github_personal_access_token", "resolution=revoked"} {
		if !strings.Contains(joined, want) {
			t.Errorf("queries lack %q:\n%s", want, joined)
		}
	}
	// A dismissed comment is cut and says so.
	got, _ := invoke(t, core, codeScanningAlertsGet.ID, "repo", `{"alert_number":42}`, false)
	if !strings.Contains(string(got), `"truncated":true`) {
		t.Errorf("get = %s, want a visible cut", got)
	}
	// A missing alert is not found.
	if _, err := invoke(t, core, dependabotAlertsGet.ID, "repo", `{"alert_number":404}`, false); err == nil ||
		!strings.Contains(err.Error(), "does not hold") {
		t.Errorf("missing alert = %v, want not found", err)
	}
}

func TestSecurityAlertListsPageByCursor(t *testing.T) {
	_, _, core, _ := serveAlerts(t, "")
	first, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{"limit":1}`, false)
	var page DependabotAlertList
	if err != nil || json.Unmarshal(first, &page) != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first = %s, %v", first, err)
	}
	if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{"state":"fixed","cursor":"`+page.NextCursor+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("cursor with other filters = %v, want invalid request", err)
	}
	if _, err := invoke(t, core, dependabotAlertsList.ID, "repo", `{"cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("continuation = %v", err)
	}
}

func TestSecurityAlertArgumentsAndTargetsAreCheckedBeforeIO(t *testing.T) {
	f, _, core, reads := serveAlerts(t, "")
	for _, tt := range []struct{ id, connection, arguments string }{
		{codeScanningAlertsGet.ID, "repo", `{"alert_number":0}`},
		{codeScanningAlertsList.ID, "repo", `{"state":"weird"}`},
		{dependabotAlertsList.ID, "repo", `{"severity":"warning"}`},
		{secretScanningAlertsList.ID, "repo", `{"secret_type":"a b"}`},
		{secretScanningAlertsList.ID, "repo", `{"resolution":"x"}`},
		{secretScanningAlertsList.ID, "planning", `{}`},
		{secretScanningAlertsGet.ID, "planning", `{"alert_number":1}`},
		{codeScanningAlertsList.ID, "planning", `{}`},
		{dependabotAlertsGet.ID, "planning", `{"alert_number":1}`},
	} {
		*reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.id, tt.connection, tt.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s(%s) on %s = %v, want an invalid request", tt.id, tt.arguments, tt.connection, err)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s(%s) on %s reached the credential or GitHub", tt.id, tt.arguments, tt.connection)
		}
	}
}

func TestSecretScanningNeverExposesTheSecret(t *testing.T) {
	for _, mode := range []string{"", "bad-json", "status", "forbidden", "huge"} {
		_, _, core, _ := serveAlerts(t, mode)
		for _, tt := range []struct{ id, arguments string }{
			{secretScanningAlertsList.ID, `{}`}, {secretScanningAlertsGet.ID, `{"alert_number":3}`},
		} {
			got, err := invoke(t, core, tt.id, "repo", tt.arguments, false)
			text := string(got)
			if err != nil {
				text += err.Error()
			}
			if strings.Contains(text, secretCanary) || strings.Contains(text, "CANARY") {
				t.Errorf("mode %q %s leaked the secret: %s", mode, tt.id, text)
			}
			if mode == "" && err != nil {
				t.Errorf("mode %q %s = %v", mode, tt.id, err)
			}
			if mode != "" && err == nil {
				t.Errorf("mode %q %s = %s, want an error", mode, tt.id, got)
			}
		}
	}
}

func TestSecurityProfileIsReadOnlyAndNotRecommended(t *testing.T) {
	reg := registry(t)
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		switch profile.ID {
		case "security":
			if profile.Recommended || len(profile.Tools) != 6 {
				t.Errorf("security = %+v", profile)
			}
			if got := metadata.ProfilePermissions(profile); len(got) != 1 || got[0] != config.PermissionRead {
				t.Errorf("security permissions = %v", got)
			}
		case "read":
			if !profile.Recommended || len(profile.Tools) != 7 {
				t.Errorf("read profile changed: %+v", profile)
			}
		}
	}
}
