package makeapi

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func scenarioInFolder(id, teamID, folderID int64) string {
	return fmt.Sprintf(`{"id":%d,"name":"S%d","teamId":%d,"folderId":%d,"isActive":true}`, id, id, teamID, folderID)
}

// folderTree is the team's folders: ownFolder holds a child which holds a grandchild; a sibling and a parent
// sit beside and above it.
const (
	childFolder      int64 = 310
	grandchildFolder int64 = 311
	siblingFolder    int64 = 320
	parentFolder     int64 = 290
)

const folderTreeJSON = `{"scenariosFolders":[{"id":290,"name":"P","children":[` +
	`{"id":300,"name":"O","children":[{"id":310,"name":"C","children":[{"id":311,"name":"G","children":[]}]}]},` +
	`{"id":320,"name":"Sib","children":[]}]},{"id":400,"name":"F","children":[]}]}`

// A folder target allows the scenarios filed directly in it. The proof is the folder of the scenario Make
// itself answered with; a scenario elsewhere or outside any folder is refused without naming its folder, and
// no change request follows a refusal.
func TestFolderScopeProvesMembershipFromTheReadScenario(t *testing.T) {
	cases := []struct {
		name       string
		connection string
		folder     int64
		scenario   int64
		allowed    bool
	}{
		{"inside the folder", "folder", ownFolder, ownScenario, true},
		{"other folder", "folder", foreignFolder, ownScenario, false},
		{"no folder", "folder", 0, ownScenario, false},
		{"listed scenario outside the folder", "folderscenario", 0, foreignScenario, true},
		{"unlisted scenario outside the folder", "folderscenario", foreignFolder, ownScenario, false},
		{"folder of the union", "folderscenario", ownFolder, ownScenario, true},
		{"child folder", "folder", childFolder, ownScenario, true},
		{"grandchild folder", "folder", grandchildFolder, ownScenario, true},
		{"sibling folder", "folder", siblingFolder, ownScenario, false},
		{"parent folder", "folder", parentFolder, ownScenario, false},
		{"grandchild through the union", "folderscenario", grandchildFolder, ownScenario, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Path == apiPath+"/scenarios-folders" {
					return jsonResponse(200, folderTreeJSON), nil
				}
				return jsonResponse(200, `{"scenario":`+scenarioInFolder(tt.scenario, ownTeam, tt.folder)+`}`), nil
			})
			args := fmt.Sprintf(`{"scenario_id":%d}`, tt.scenario)
			for _, op := range []string{scenariosGet.ID, scenariosBlueprint.ID, runsList.ID} {
				calls = nil
				_, err := env.invoke(op, tt.connection, args)
				if tt.allowed {
					if isInvalidRequest(err) {
						t.Fatalf("%s refused: %v", op, err)
					}
					continue
				}
				if !isInvalidRequest(err) {
					t.Fatalf("%s err = %v, want an invalid request", op, err)
				}
				if strings.Contains(err.Error(), strconv.FormatInt(tt.folder, 10)) && tt.folder != 0 {
					t.Fatalf("error named the folder: %v", err)
				}
				if len(calls) > 2 {
					t.Fatalf("%s calls = %+v, want the scenario read and at most one folder read", op, calls)
				}
			}
		})
	}
}

// Stopping a scenario outside the folder reads it and sends nothing else.
func TestFolderScopeSendsNoChangeToAScenarioOutside(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("change sent: %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"scenario":`+scenarioInFolder(ownScenario, ownTeam, foreignFolder)+`}`), nil
	})
	for _, op := range []string{scenariosStop.ID, scenariosStart.ID} {
		if _, err := env.confirmed(op, "folder", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario)); !isInvalidRequest(err) {
			t.Fatalf("%s err = %v", op, err)
		}
	}
	if _, err := env.confirmed(scenariosUpdate.ID, "folder", fmt.Sprintf(`{"scenario_id":%d,"name":"x"}`, ownScenario)); !isInvalidRequest(err) {
		t.Fatalf("update err = %v", err)
	}
}

// A scenario in scope only through its folder cannot be moved out of the listed folders: the check follows the
// scenario read and precedes any change; a move below a listed folder passes.
func TestFolderScopeKeepsAScenarioInside(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/scenarios-folders":
			return jsonResponse(200, folderTreeJSON), nil
		case r.Method != http.MethodGet:
			t.Fatalf("change sent: %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"scenario":`+scenarioInFolder(ownScenario, ownTeam, ownFolder)+`}`), nil
	})
	for _, args := range []string{
		fmt.Sprintf(`{"scenario_id":%d,"folder_id":%d}`, ownScenario, foreignFolder),
		fmt.Sprintf(`{"scenario_id":%d,"folder_id":%d}`, ownScenario, siblingFolder),
		fmt.Sprintf(`{"scenario_id":%d,"folder_id":%d}`, ownScenario, parentFolder),
		fmt.Sprintf(`{"scenario_id":%d,"clear_folder":true}`, ownScenario),
	} {
		if _, err := env.confirmed(scenariosUpdate.ID, "folder", args); !isInvalidRequest(err) {
			t.Fatalf("update %s err = %v", args, err)
		}
	}
}

func TestFolderScopeAllowsAMoveBelowAListedFolder(t *testing.T) {
	var calls []call
	moved := false
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == apiPath+"/scenarios-folders":
			return jsonResponse(200, folderTreeJSON), nil
		case r.Method == http.MethodPatch:
			moved = true
			return jsonResponse(200, `{}`), nil
		}
		folder := ownFolder
		if moved {
			folder = grandchildFolder
		}
		return jsonResponse(200, `{"scenario":`+scenarioInFolder(ownScenario, ownTeam, folder)+`}`), nil
	})
	if _, err := env.confirmed(scenariosUpdate.ID, "folder",
		fmt.Sprintf(`{"scenario_id":%d,"folder_id":%d}`, ownScenario, grandchildFolder)); err != nil || !moved {
		t.Fatalf("err = %v, moved = %t", err, moved)
	}
}

func TestFolderScopeFiltersTheList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/scenarios-folders" {
			return jsonResponse(200, folderTreeJSON), nil
		}
		return jsonResponse(200, `{"scenarios":[`+scenarioInFolder(11, ownTeam, ownFolder)+`,`+
			scenarioInFolder(12, ownTeam, foreignFolder)+`,`+scenarioInFolder(13, ownTeam, 0)+`,`+
			scenarioInFolder(14, foreignTeam, ownFolder)+`]}`), nil
	})
	result, err := env.invoke(scenariosList.ID, "folder", `{}`)
	if err != nil || !strings.Contains(result, `"id":11`) || strings.Contains(result, `"id":12`) ||
		strings.Contains(result, `"id":13`) || strings.Contains(result, `"id":14`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	result, err = env.invoke(scenariosList.ID, "folderscenario", `{}`)
	if err != nil || !strings.Contains(result, `"id":11`) || strings.Contains(result, `"id":12`) {
		t.Fatalf("union result = %s, %v", result, err)
	}
}

// Team-wide tools stay refused with a folder target, without any request.
func TestFolderScopeRefusesTeamWideTools(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("no request expected, got %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	for op, args := range map[string]string{
		scenariosCreate.ID:        `{"blueprint":{},"scheduling":{"type":"on-demand"}}`,
		teamVariablesList.ID:      `{}`,
		connectionsList.ID:        `{}`,
		dataStoresList.ID:         `{}`,
		credentialRequestsList.ID: `{}`,
		dataStructuresList.ID:     `{}`,
	} {
		if _, err := env.confirmed(op, "folder", args); !isInvalidRequest(err) {
			t.Fatalf("%s err = %v", op, err)
		}
	}
	// A folder reaches no hook, whatever hook the team has.
	env2 := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"hooks":[{"id":7,"teamId":`+strconv.FormatInt(ownTeam, 10)+`,"scenarioId":10}]}`), nil
	})
	result, err := env2.invoke(hooksList.ID, "folder", `{}`)
	if err != nil || strings.Contains(result, `"id":7`) {
		t.Fatalf("hooks = %s, %v", result, err)
	}
}

func TestReachesFollowsWhatTheCallRefuses(t *testing.T) {
	meta, _ := registry(t).ProviderMetadata(Provider)
	cases := []struct {
		name    string
		targets []string
		tool    string
		reached bool
		hint    string
	}{
		{"team reaches create", []string{"team/1"}, scenariosCreate.ID, true, ""},
		{"team and scenario list reach hooks", []string{"team/1", "scenario/2"}, hooksList.ID, true, ""},
		{"scenario allow-list refuses create", []string{"team/1", "scenario/2"}, scenariosCreate.ID, false, "needs team/TEAM_ID"},
		{"scenario allow-list refuses variables", []string{"team/1", "scenario/2"}, teamVariablesList.ID, false, "needs team/TEAM_ID"},
		{"scenario allow-list reaches runs", []string{"team/1", "scenario/2"}, runsList.ID, true, ""},
		{"folder refuses create", []string{"team/1", "folder/2"}, scenariosCreate.ID, false, "needs team/TEAM_ID"},
		{"folder refuses connections", []string{"team/1", "folder/2"}, connectionsTest.ID, false, "needs team/TEAM_ID"},
		{"folder refuses data stores", []string{"team/1", "folder/2"}, dataStoreRecordsList.ID, false, "needs team/TEAM_ID"},
		{"folder refuses credential requests", []string{"team/1", "folder/2"}, credentialRequestsCreate.ID, false, "needs team/TEAM_ID"},
		{"folder refuses hooks", []string{"team/1", "folder/2"}, hookQueueList.ID, false, "needs team/TEAM_ID"},
		{"folder reaches scenarios", []string{"team/1", "folder/2"}, scenariosUpdate.ID, true, ""},
		{"folder reaches team details", []string{"team/1", "folder/2"}, teamGet.ID, true, ""},
		{"folder reaches organization details", []string{"team/1", "folder/2"}, organizationGet.ID, true, ""},
		{"team refuses organization tools", []string{"team/1"}, teamsList.ID, false, "needs organization/ORG_ID"},
		{"team refuses organization billing", []string{"team/1", "organization/2"}, organizationUsage.ID, false, "needs organization/ORG_ID"},
		{"organization reaches teams", []string{"organization/2"}, teamsList.ID, true, ""},
		{"organization reaches variables", []string{"organization/2"}, organizationVariablesList.ID, true, ""},
		{"organization refuses scenarios", []string{"organization/2"}, scenariosList.ID, false, "needs team/TEAM_ID"},
		{"organization refuses team details", []string{"organization/2"}, teamGet.ID, false, "needs team/TEAM_ID"},
		{"invalid binding reaches", []string{"scenario/2"}, scenariosCreate.ID, true, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			reached, hint := meta.Target.Reach(tt.tool, tt.targets)
			if reached != tt.reached || hint != tt.hint {
				t.Fatalf("Reach(%s, %v) = %v, %q", tt.tool, tt.targets, reached, hint)
			}
		})
	}
}

func browseTargets(t *testing.T, req config.TargetListRequest, targets ...string) (config.TargetPage, error) {
	t.Helper()
	red := &redact.Redactor{}
	return registry(t).ListTargets(context.Background(), resolvedConnection(targets...), resolver(red, nil), red, req)
}

func TestTargetBrowserListsEveryLevelWithoutFilteringByTargets(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/organizations":
			return jsonResponse(200, `{"organizations":[{"id":501,"name":"Org A"},{"id":502,"name":"Org B"}]}`), nil
		case apiPath + "/teams":
			return jsonResponse(200, `{"teams":[{"id":1001,"name":"Team A","organizationId":501},{"id":9,"name":"Odd","organizationId":777}]}`), nil
		case apiPath + "/scenarios-folders":
			return jsonResponse(200, `{"scenariosFolders":[{"id":300,"name":"Top","path":"Top","children":[`+
				`{"id":301,"name":"Sub","path":"Top/Sub","children":[]}]},{"id":0,"name":"bad"}]}`), nil
		case apiPath + "/scenarios":
			return jsonResponse(200, `{"scenarios":[`+scenarioInFolder(10, ownTeam, 300)+`,`+
				scenarioInFolder(11, ownTeam, 0)+`,`+scenarioInFolder(12, foreignTeam, 0)+`]}`), nil
		}
		t.Fatalf("unexpected %s", r.URL.Path)
		return nil, nil
	})
	page, err := browseTargets(t, config.TargetListRequest{Kind: "organization"})
	if err != nil || !reflect.DeepEqual(page.Entries, []config.TargetEntry{
		{Target: "organization/501", Name: "Org A", Kind: "organization", HasChildren: true},
		{Target: "organization/502", Name: "Org B", Kind: "organization", HasChildren: true},
	}) || page.Next != "" {
		t.Fatalf("organizations = %+v, %v", page, err)
	}
	page, err = browseTargets(t, config.TargetListRequest{Kind: "team", Parent: "organization/501"})
	if err != nil || !reflect.DeepEqual(page.Entries, []config.TargetEntry{
		{Target: "team/1001", Name: "Team A", Kind: "team", HasChildren: true}}) {
		t.Fatalf("teams = %+v, %v", page, err)
	}
	if got := calls[len(calls)-1].query.Get("organizationId"); got != "501" {
		t.Fatalf("organizationId = %q", got)
	}
	page, err = browseTargets(t, config.TargetListRequest{Kind: "folder", Parent: "team/1001"}, "team/5", "folder/6")
	if err != nil || !reflect.DeepEqual(page.Entries, []config.TargetEntry{
		{Target: "folder/300", Name: "Top", Kind: "folder", Path: "Top"},
		{Target: "folder/301", Name: "Sub", Kind: "folder", Path: "Top/Sub"},
	}) {
		t.Fatalf("folders = %+v, %v", page, err)
	}
	if q := calls[len(calls)-1].query; q.Get("teamId") != "1001" || q.Get("childrenDepth") != "all" {
		t.Fatalf("folder query = %v", q)
	}
	page, err = browseTargets(t, config.TargetListRequest{Kind: "scenario", Parent: "team/1001", Query: "pay"})
	if err != nil || !page.ServerSearch || !reflect.DeepEqual(page.Entries, []config.TargetEntry{
		{Target: "scenario/10", Name: "S10", Kind: "scenario", Path: "folder/300"},
		{Target: "scenario/11", Name: "S11", Kind: "scenario"},
	}) {
		t.Fatalf("scenarios = %+v, %v", page, err)
	}
	if q := calls[len(calls)-1].query; q.Get("name") != "pay" || q.Get("pg[limit]") == "" || q.Get("pg[offset]") != "0" {
		t.Fatalf("scenario query = %v", q)
	}
}

func TestTargetBrowserPagesByOffset(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		var rows []string
		for i := 0; i < defaultListLimit; i++ {
			rows = append(rows, scenarioInFolder(int64(100+i), ownTeam, 0))
		}
		return jsonResponse(200, `{"scenarios":[`+strings.Join(rows, ",")+`]}`), nil
	})
	page, err := browseTargets(t, config.TargetListRequest{Kind: "scenario", Parent: "team/1001", Cursor: "50"})
	if err != nil || page.Next != "100" || calls[0].query.Get("pg[offset]") != "50" {
		t.Fatalf("page next = %q, %v, query %v", page.Next, err, calls[0].query)
	}
	for _, cursor := range []string{"x", "-1", "05"} {
		_, err = browseTargets(t, config.TargetListRequest{Kind: "scenario", Parent: "team/1001", Cursor: cursor})
		if !isInvalidRequest(err) {
			t.Fatalf("cursor %q err = %v", cursor, err)
		}
	}
	for _, req := range []config.TargetListRequest{
		{Kind: "team"}, {Kind: "team", Parent: "team/1"}, {Kind: "folder", Parent: "organization/1"},
		{Kind: "scenario", Parent: "scenario/1"}, {Kind: "project"},
	} {
		if _, err = browseTargets(t, req); !isInvalidRequest(err) {
			t.Fatalf("%+v err = %v", req, err)
		}
	}
}

// No documentation of Make describes the routes of its web interface, so no URL names a target.
func TestTargetBrowserResolvesNoURL(t *testing.T) {
	for _, raw := range []string{
		"https://eu1.make.com/1001/scenarios/10/edit", "https://us1.make.com/1001/scenarios/10/edit",
		"https://eu1.make.com/organization/501", "", "not a url",
	} {
		entries, err := resolveTargetURL(context.Background(), nil, nil, nil, baseURL, raw)
		if err != nil || len(entries) != 0 {
			t.Fatalf("%q = %+v, %v", raw, entries, err)
		}
	}
}

// The folder tree is read at most once per call, and not at all when the scenario's folder is listed.
func TestFolderScopeReadsTheTreeAtMostOnce(t *testing.T) {
	for _, tt := range []struct {
		folder int64
		reads  int
	}{{ownFolder, 0}, {0, 0}, {grandchildFolder, 1}, {siblingFolder, 1}} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == apiPath+"/scenarios-folders" {
				return jsonResponse(200, folderTreeJSON), nil
			}
			return jsonResponse(200, `{"scenario":`+scenarioInFolder(ownScenario, ownTeam, tt.folder)+`}`), nil
		})
		_, _ = env.invoke(scenariosGet.ID, "folder", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
		reads := 0
		for _, c := range calls {
			if strings.HasSuffix(c.path, "/scenarios-folders") {
				reads++
			}
		}
		if reads != tt.reads || len(calls) != 1+tt.reads {
			t.Fatalf("folder %d: calls = %+v, want %d folder reads", tt.folder, calls, tt.reads)
		}
	}
}

func TestFolderScopeListIncludesSubfolders(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/scenarios-folders" {
			return jsonResponse(200, folderTreeJSON), nil
		}
		return jsonResponse(200, `{"scenarios":[`+scenarioInFolder(11, ownTeam, grandchildFolder)+`,`+
			scenarioInFolder(12, ownTeam, siblingFolder)+`,`+scenarioInFolder(13, ownTeam, childFolder)+`,`+
			scenarioInFolder(14, ownTeam, 0)+`,`+scenarioInFolder(15, ownTeam, parentFolder)+`]}`), nil
	})
	result, err := env.invoke(scenariosList.ID, "folder", `{}`)
	if err != nil || !strings.Contains(result, `"id":11`) || !strings.Contains(result, `"id":13`) ||
		strings.Contains(result, `"id":12`) || strings.Contains(result, `"id":14`) || strings.Contains(result, `"id":15`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	reads := 0
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/scenarios-folders") {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("folder reads = %d, want 1", reads)
	}
}
