package makeapi

import (
	"context"
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

// Scenario 10 sits in the grandchild's parent folder 310 (below 300), 11 in the sibling 320, 12 in no folder,
// 13 directly in 300. Hook 77 belongs to scenario 10, 78 to 11, 79 to none.
var excludeFolders = map[int64]int64{10: childFolder, 11: siblingFolder, 12: 0, 13: ownFolder}

const (
	exTeamA int64 = 3001
	exTeamB int64 = 3002
)

func excludeConfig() *config.Config {
	cfg := coreConfig()
	team := "team/" + itoa64(ownTeam)
	add := func(name string, targets, excludes []string) {
		cfg.Connections[name] = config.Connection{Service: "make", Credential: "make-reader", Effects: allEffects,
			Targets: targets, Exclude: excludes}
	}
	add("exfolder", []string{team}, []string{"folder/" + itoa64(ownFolder)})
	add("exscenario", []string{team}, []string{"scenario/" + itoa64(ownScenario)})
	add("exunder", []string{team, "folder/" + itoa64(parentFolder)}, []string{"folder/" + itoa64(childFolder)})
	add("exinto", []string{team, "scenario/" + itoa64(ownScenario)}, []string{"folder/" + itoa64(ownFolder)})
	add("exorg", []string{"organization/" + itoa64(ownOrg)}, []string{"team/" + itoa64(exTeamB)})
	return cfg
}

// excludeEnvironment answers a team with the scenarios and hooks above and an organization with two teams. A
// request that changes anything is recorded in changes; a created scenario lands in landing.
func excludeEnvironment(t *testing.T, calls *[]call, changes *[]string, landing int64) *environment {
	t.Helper()
	serve(t, calls, func(r *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(r.URL.Path, apiPath)
		if r.Method != http.MethodGet {
			*changes = append(*changes, r.Method+" "+path)
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		switch {
		case path == "/scenarios-folders":
			return jsonResponse(200, folderTreeJSON), nil
		case path == "/scenarios" && r.Method == http.MethodPost:
			return jsonResponse(200, `{"scenario":`+scenarioInFolder(14, ownTeam, landing)+`}`), nil
		case path == "/scenarios":
			rows := []string{}
			for _, id := range []int64{10, 11, 12, 13} {
				rows = append(rows, scenarioInFolder(id, ownTeam, excludeFolders[id]))
			}
			return jsonResponse(200, `{"scenarios":[`+strings.Join(rows, ",")+`]}`), nil
		case path == "/hooks":
			return jsonResponse(200, `{"hooks":[`+hookJSONOf(77, ownTeam, 10, "A")+","+hookJSONOf(78, ownTeam, 11, "B")+
				","+hookJSONOf(79, ownTeam, 0, "C")+`]}`), nil
		case path == "/teams":
			return jsonResponse(200, `{"teams":[`+orgTeamBody(exTeamA, ownOrg, "A")+","+orgTeamBody(exTeamB, ownOrg, "B")+`]}`), nil
		case len(parts) >= 2 && parts[0] == "teams":
			id, _ := strconv.ParseInt(parts[1], 10, 64)
			return jsonResponse(200, `{"team":`+orgTeamBody(id, ownOrg, "T")+`}`), nil
		case len(parts) == 2 && parts[0] == "hooks":
			id, _ := strconv.ParseInt(parts[1], 10, 64)
			scenario := map[int64]int64{77: 10, 78: 11, 79: 0}[id]
			return jsonResponse(200, `{"hook":`+hookJSONOf(id, ownTeam, scenario, "H")+`}`), nil
		case len(parts) >= 2 && parts[0] == "hooks":
			return jsonResponse(200, `{"hookLogs":[],"queue":[],"hooks":[]}`), nil
		case len(parts) >= 2 && parts[0] == "scenarios":
			id, _ := strconv.ParseInt(parts[1], 10, 64)
			return jsonResponse(200, `{"scenario":`+scenarioInFolder(id, ownTeam, excludeFolders[id])+`}`), nil
		}
		return jsonResponse(200, `{}`), nil
	})
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), excludeConfig(), resolver(red, &reads), red), red: red, reads: &reads}
}

// direct runs a tool's handler against a connection the allow-list of tools of the core would not offer it on.
func (e *environment) direct(t *testing.T, op string, resolved *config.Resolved, args string) error {
	t.Helper()
	_, handler, ok := registry(t).Lookup(op)
	if !ok {
		t.Fatalf("no handler for %s", op)
	}
	_, err := handler(context.Background(), resolved, resolver(e.red, e.reads), e.red, json.RawMessage(args))
	return err
}

func excluding(resolved *config.Resolved, excludes ...string) *config.Resolved {
	resolved.Exclude = excludes
	return resolved
}

func countCalls(calls []call, path string) int {
	n := 0
	for _, c := range calls {
		if c.path == apiPath+path {
			n++
		}
	}
	return n
}

func TestExcludeFormsPerMode(t *testing.T) {
	team, org := []string{"team/1"}, []string{"organization/2"}
	cases := []struct {
		name     string
		targets  []string
		excludes []string
		valid    bool
	}{
		{"scenario and folder in team mode", team, []string{"scenario/3", "folder/4"}, true},
		{"team in organization mode", org, []string{"team/3"}, true},
		{"organization", team, []string{"organization/2"}, false},
		{"organization in organization mode", org, []string{"organization/3"}, false},
		{"team in team mode", team, []string{"team/3"}, false},
		{"scenario in organization mode", org, []string{"scenario/3"}, false},
		{"folder in organization mode", org, []string{"folder/3"}, false},
		{"repeated", team, []string{"scenario/3", "scenario/3"}, false},
		{"equal to a scenario target", []string{"team/1", "scenario/3"}, []string{"scenario/3"}, false},
		{"equal to a folder target", []string{"team/1", "folder/3"}, []string{"folder/3"}, false},
		{"scenario and folder of one number", team, []string{"scenario/3", "folder/3"}, true},
		{"malformed", team, []string{"scenario/x"}, false},
		{"empty", team, []string{""}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseScope(tt.targets, tt.excludes...)
			if (err == nil) != tt.valid {
				t.Fatalf("parseScope() = %v, valid = %t", err, tt.valid)
			}
			if err != nil {
				for _, value := range tt.excludes {
					if value != "" && strings.Contains(err.Error(), value) {
						t.Fatalf("error quotes %q: %v", value, err)
					}
				}
			}
		})
	}
}

// An exclusion is never a target: the restrictions and the reach of the connection stay as they are.
func TestExcludeIsNoTarget(t *testing.T) {
	bound, err := parseScope([]string{"team/1"}, "scenario/3", "folder/4")
	if err != nil || bound.restricted() || !bound.mayAllowScenario(3) || bound.listsScenario(3) {
		t.Fatalf("scope = %+v, err = %v", bound, err)
	}
	for _, d := range registry(t).Provider(Provider) {
		if ok, _ := reaches(d.ID, []string{"team/1"}); ok == orgTool(d.ID) {
			t.Fatalf("%s: reached = %t by a team binding", d.ID, ok)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, kind := range metadata.Target.Kinds {
		if want := kind.Name != "organization"; (len(kind.ExcludeForms) > 0) != want {
			t.Fatalf("kind %s Excludable = %t", kind.Name, (len(kind.ExcludeForms) > 0))
		}
	}
}

func TestExcludeInvalidIsRefusedBeforeSecretAndIO(t *testing.T) {
	var calls []call
	var changes []string
	env := excludeEnvironment(t, &calls, &changes, 0)
	cfg := excludeConfig()
	cfg.Connections["bad"] = config.Connection{Service: "make", Credential: "make-reader", Effects: allEffects,
		Targets: []string{"team/" + itoa64(ownTeam)}, Exclude: []string{"team/" + itoa64(foreignTeam)}}
	env.core = application.New(registry(t), cfg, resolver(env.red, env.reads), env.red)
	if _, err := env.invoke(scenariosList.ID, "bad", `{}`); err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %d, reads = %d", err, len(calls), *env.reads)
	}
}

func TestExcludedScenarioIsRefusedLocallyAndBeforeContent(t *testing.T) {
	var calls []call
	var changes []string
	env := excludeEnvironment(t, &calls, &changes, 0)
	args := fmt.Sprintf(`{"scenario_id":%d}`, ownScenario)
	for _, op := range []string{scenariosGet.ID, scenariosBlueprint.ID, runsList.ID, runsGet.ID, scenariosStart.ID,
		scenariosStop.ID, scenariosRun.ID, scenariosUpdate.ID} {
		a := args
		if op == scenariosUpdate.ID {
			a = fmt.Sprintf(`{"scenario_id":%d,"name":"x"}`, ownScenario)
		}
		if op == runsGet.ID {
			a = fmt.Sprintf(`{"scenario_id":%d,"execution_id":"e"}`, ownScenario)
		}
		_, err := env.confirmed(op, "exscenario", a)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "exclu") {
			t.Fatalf("%s err = %v", op, err)
		}
	}
	if len(calls) != 0 || len(changes) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, changes = %v, reads = %d", calls, changes, *env.reads)
	}
	// Other scenarios and the list keep their requests: no folder tree is read for a scenario exclusion.
	if _, err := env.invoke(scenariosGet.ID, "exscenario", `{"scenario_id":11}`); err != nil {
		t.Fatalf("get 11: %v", err)
	}
	out, err := env.invoke(scenariosList.ID, "exscenario", `{}`)
	if err != nil || strings.Contains(out, `"id":10,`) || !strings.Contains(out, `"id":11`) {
		t.Fatalf("list = %s, %v", out, err)
	}
	if countCalls(calls, "/scenarios-folders") != 0 {
		t.Fatalf("a scenario exclusion read the folder tree: %+v", calls)
	}
}

func TestExcludedFolderHidesItsSubtree(t *testing.T) {
	var calls []call
	var changes []string
	env := excludeEnvironment(t, &calls, &changes, 0)
	out, err := env.invoke(scenariosList.ID, "exfolder", `{}`)
	if err != nil || strings.Contains(out, `"id":10,`) || strings.Contains(out, `"id":13,`) ||
		!strings.Contains(out, `"id":11`) || !strings.Contains(out, `"id":12`) {
		t.Fatalf("list = %s, %v", out, err)
	}
	out, err = env.invoke(scenariosList.ID, "exfolder", `{"ids":[10,11]}`)
	if err != nil || strings.Contains(out, `"id":10,`) || !strings.Contains(out, `"id":11`) {
		t.Fatalf("list ids = %s, %v", out, err)
	}
	calls = nil
	for _, folder := range []int64{ownFolder, childFolder, grandchildFolder} {
		out, err = env.invoke(scenariosList.ID, "exfolder", fmt.Sprintf(`{"folder_id":%d}`, folder))
		if err != nil || strings.Contains(out, `"id":`) {
			t.Fatalf("list folder %d = %s, %v", folder, out, err)
		}
	}
	if countCalls(calls, "/scenarios") != 0 {
		t.Fatalf("a list of an excluded folder was requested: %+v", calls)
	}
	for _, id := range []int64{10, 13} {
		for _, op := range []string{scenariosGet.ID, scenariosBlueprint.ID, runsList.ID, scenariosStop.ID} {
			if _, err := env.confirmed(op, "exfolder", fmt.Sprintf(`{"scenario_id":%d}`, id)); !isInvalidRequest(err) {
				t.Fatalf("%s %d err = %v", op, id, err)
			}
		}
	}
	for _, id := range []int64{11, 12} {
		if _, err := env.invoke(scenariosGet.ID, "exfolder", fmt.Sprintf(`{"scenario_id":%d}`, id)); err != nil {
			t.Fatalf("get %d: %v", id, err)
		}
	}
	if len(changes) != 0 {
		t.Fatalf("a change was sent: %v", changes)
	}
}

func TestExcludedFolderRefusesCreateAndMoveInto(t *testing.T) {
	var calls []call
	var changes []string
	env := excludeEnvironment(t, &calls, &changes, siblingFolder)
	create := `{"blueprint":{"flow":[]},"scheduling":{"type":"indefinitely","interval":900},"folder_id":%d}`
	for _, folder := range []int64{ownFolder, childFolder, grandchildFolder} {
		if _, err := env.confirmed(scenariosCreate.ID, "exfolder", fmt.Sprintf(create, folder)); !isInvalidRequest(err) {
			t.Fatalf("create in %d err = %v", folder, err)
		}
		args := fmt.Sprintf(`{"scenario_id":11,"folder_id":%d}`, folder)
		if _, err := env.confirmed(scenariosUpdate.ID, "exfolder", args); !isInvalidRequest(err) {
			t.Fatalf("move into %d err = %v", folder, err)
		}
	}
	if len(changes) != 0 {
		t.Fatalf("changes = %v", changes)
	}
	// Elsewhere both stay possible.
	if _, err := env.confirmed(scenariosCreate.ID, "exfolder", fmt.Sprintf(create, siblingFolder)); err != nil {
		t.Fatalf("create in the sibling: %v", err)
	}
	if _, err := env.confirmed(scenariosUpdate.ID, "exfolder", `{"scenario_id":11,"folder_id":400}`); err != nil {
		t.Fatalf("move to 400: %v", err)
	}
	// A result that lands in an excluded folder anyway is a provider error, as for any other target.
	changes = nil
	env = excludeEnvironment(t, &calls, &changes, childFolder)
	_, err := env.confirmed(scenariosCreate.ID, "exfolder", fmt.Sprintf(create, siblingFolder))
	if classOf(err) != "provider-error" || len(changes) != 1 {
		t.Fatalf("err = %v, changes = %v", err, changes)
	}
}

// A subfolder excluded below a folder target, and a scenario target filed in an excluded folder, are out.
func TestExclusionBeatsTargets(t *testing.T) {
	var calls []call
	var changes []string
	env := excludeEnvironment(t, &calls, &changes, 0)
	out, err := env.invoke(scenariosList.ID, "exunder", `{}`)
	if err != nil || strings.Contains(out, `"id":10,`) || !strings.Contains(out, `"id":11`) ||
		!strings.Contains(out, `"id":13`) {
		t.Fatalf("list = %s, %v", out, err)
	}
	if _, err := env.invoke(scenariosGet.ID, "exunder", `{"scenario_id":10}`); !isInvalidRequest(err) {
		t.Fatalf("get 10 err = %v", err)
	}
	if _, err := env.invoke(scenariosGet.ID, "exunder", `{"scenario_id":13}`); err != nil {
		t.Fatalf("get 13: %v", err)
	}
	if _, err := env.invoke(scenariosGet.ID, "exinto", `{"scenario_id":10}`); !isInvalidRequest(err) {
		t.Fatalf("get a target in an excluded folder err = %v", err)
	}
}

func TestExcludedScenarioHooksAreOutOfReach(t *testing.T) {
	var calls []call
	var changes []string
	env := excludeEnvironment(t, &calls, &changes, 0)
	for _, connection := range []string{"exscenario", "exfolder"} {
		calls = nil
		out, err := env.invoke(hooksList.ID, connection, `{}`)
		if err != nil || strings.Contains(out, `"id":77,`) || !strings.Contains(out, `"id":79`) {
			t.Fatalf("%s list = %s, %v", connection, out, err)
		}
		if connection == "exscenario" && (countCalls(calls, "/scenarios-folders") != 0 || countCalls(calls, "/scenarios/10") != 0) {
			t.Fatalf("scenario exclusion read more: %+v", calls)
		}
		if connection == "exfolder" && (!strings.Contains(out, `"id":78`) || countCalls(calls, "/scenarios-folders") != 1) {
			t.Fatalf("exfolder list = %s, calls = %+v", out, calls)
		}
		hook := `{"hook_id":77}`
		resolved := excluding(resolvedConnection("team/"+itoa64(ownTeam)), "scenario/10")
		if connection == "exfolder" {
			resolved = excluding(resolvedConnection("team/"+itoa64(ownTeam)), "folder/"+itoa64(ownFolder))
		}
		for _, op := range []string{hooksGet.ID, hooksPing.ID, hooksLogs.ID, hooksURL.ID, hookQueueList.ID,
			hookQueueStats.ID, hookQueueGet.ID, hookQueueDelete.ID, hooksDelete.ID} {
			if err := env.direct(t, op, resolved, hook); !isInvalidRequest(err) {
				t.Fatalf("%s %s err = %v", connection, op, err)
			}
		}
		if _, err := env.confirmed(hooksRename.ID, connection, `{"hook_id":77,"name":"x"}`); !isInvalidRequest(err) {
			t.Fatalf("%s rename err = %v", connection, err)
		}
		if _, err := env.invoke(hooksList.ID, connection, `{"view_for_scenario_id":10}`); !isInvalidRequest(err) {
			t.Fatalf("%s view_for_scenario_id err = %v", connection, err)
		}
		for _, op := range []string{hooksGet.ID, hooksPing.ID, hooksLogs.ID, hooksURL.ID, hookQueueList.ID} {
			if err := env.direct(t, op, resolved, `{"hook_id":79}`); isInvalidRequest(err) {
				t.Fatalf("%s %s of an unassigned hook: %v", connection, op, err)
			}
		}
	}
	if _, err := env.invoke(hooksGet.ID, "exfolder", `{"hook_id":78}`); err != nil {
		t.Fatalf("hook of a reachable scenario: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("a change was sent: %v", changes)
	}
}

func TestExcludedTeamsLeaveTheOrganizationList(t *testing.T) {
	var calls []call
	var changes []string
	env := excludeEnvironment(t, &calls, &changes, 0)
	out, err := env.invoke(teamsList.ID, "exorg", `{}`)
	if err != nil || strings.Contains(out, `"id":3002`) || !strings.Contains(out, `"id":3001`) {
		t.Fatalf("list = %s, %v", out, err)
	}
	calls = nil
	*env.reads = 0
	resolved := excluding(resolvedConnection("organization/"+itoa64(ownOrg)), "team/3002")
	for _, op := range []struct{ id, args string }{
		{teamsUpdate.ID, `{"team_id":3002,"name":"x"}`}, {teamsDelete.ID, `{"team_id":3002,"confirmed":true}`},
	} {
		err := env.direct(t, op.id, resolved, op.args)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), foreignTeamMsg) {
			t.Fatalf("%s err = %v", op.id, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 || len(changes) != 0 {
		t.Fatalf("calls = %+v, reads = %d, changes = %v", calls, *env.reads, changes)
	}
	if _, err := env.confirmed(teamsUpdate.ID, "exorg", `{"team_id":3001,"name":"x"}`); err != nil {
		t.Fatalf("update of a reachable team: %v", err)
	}
}

// Objects without a scenario reference stay reachable on a connection with exclusions.
func TestExcludeLeavesTeamObjectsReachable(t *testing.T) {
	var calls []call
	var changes []string
	env := excludeEnvironment(t, &calls, &changes, 0)
	for _, op := range []string{connectionsList.ID, dataStoresList.ID, teamVariablesList.ID, credentialRequestsList.ID,
		teamGet.ID} {
		for _, connection := range []string{"exfolder", "exscenario"} {
			if _, err := env.invoke(op, connection, `{}`); isInvalidRequest(err) {
				t.Fatalf("%s on %s: %v", op, connection, err)
			}
		}
	}
	if _, err := env.confirmed(scenariosCreate.ID, "exscenario",
		`{"blueprint":{"flow":[]},"scheduling":{"type":"indefinitely","interval":900}}`); isInvalidRequest(err) {
		t.Fatalf("create: %v", err)
	}
}

// The scenarios a refused hook deletion lists leave out the excluded ones.
func TestExcludedScenariosAreNotListedByARefusedHookDeletion(t *testing.T) {
	h := newHookChangeEnv(t, ownTeam, 0)
	h.refuse = true
	resolved := excluding(resolvedConnection("team/"+itoa64(ownTeam)), "scenario/10")
	_, handler, _ := registry(t).Lookup(hooksDelete.ID)
	result, err := handler(context.Background(), resolved, resolver(h.red, h.reads), h.red,
		json.RawMessage(`{"hook_id":77}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	got, ok := result.(*HookDeletion)
	if !ok || !got.ConfirmationRequired || len(got.Scenarios) != 1 || got.Scenarios[0].ID != 11 {
		t.Fatalf("result = %+v", result)
	}
}

// A refused connection or data store deletion lists only reachable scenarios, fails when none is left and
// fails closed when membership cannot be checked.
func TestExcludedScenariosAreNotListedByARefusedDeletion(t *testing.T) {
	connID, storeNo := strconv.FormatInt(connectionID, 10), storeID
	deletions := []struct {
		name, args string
		op         string
	}{
		{"connection", `{"connection_id":` + connID + `}`, connectionsDelete.ID},
		{"data store", `{"datastore_id":` + storeNo + `}`, dataStoresDelete.ID},
	}
	cases := []struct {
		name     string
		listed   string
		excludes []string
		failRead bool
		wantIDs  []int64
		wantErr  bool
	}{
		{"direct", `[{"id":10,"name":"Hidden"},{"id":12,"name":"Open"}]`, []string{"scenario/10"}, false, []int64{12}, false},
		{"folder", `[{"id":13,"name":"Hidden"},{"id":12,"name":"Open"}]`, []string{"folder/" + itoa64(ownFolder)}, false,
			[]int64{12}, false},
		{"only excluded", `[{"id":10,"name":"HiddenName"},{"id":13,"name":"HiddenName"}]`,
			[]string{"scenario/10", "folder/" + itoa64(ownFolder)}, false, nil, true},
		{"check fails", `[{"id":12,"name":"Open"}]`, []string{"folder/" + itoa64(ownFolder)}, true, nil, true},
	}
	for _, d := range deletions {
		for _, c := range cases {
			t.Run(d.name+" "+c.name, func(t *testing.T) {
				var calls []call
				serve(t, &calls, func(r *http.Request) (*http.Response, error) {
					path := strings.TrimPrefix(r.URL.Path, apiPath)
					parts := strings.Split(strings.Trim(path, "/"), "/")
					switch {
					case r.Method == http.MethodDelete:
						return jsonResponse(400, `{"message":"`+foreignCanary+`","detail":{"scenarios":`+c.listed+`}}`), nil
					case path == "/scenarios-folders":
						return jsonResponse(200, folderTreeJSON), nil
					case len(parts) == 2 && parts[0] == "scenarios":
						if c.failRead {
							return jsonResponse(500, `{"message":"`+foreignCanary+`"}`), nil
						}
						id, _ := strconv.ParseInt(parts[1], 10, 64)
						return jsonResponse(200, `{"scenario":`+scenarioInFolder(id, ownTeam, excludeFolders[id])+`}`), nil
					case path == "/data-stores/"+storeNo:
						return jsonResponse(200, `{"dataStore":`+dataStoreJSONOf(storeNo, ownTeam, "Own")+`}`), nil
					case len(parts) == 2 && parts[0] == "connections":
						return jsonResponse(200, `{"connection":`+connectionJSONOf(connectionID, ownTeam, "C")+`}`), nil
					}
					return jsonResponse(200, `{}`), nil
				})
				red := &redact.Redactor{}
				reads := 0
				resolved := excluding(resolvedConnection("team/"+itoa64(ownTeam)), c.excludes...)
				_, handler, _ := registry(t).Lookup(d.op)
				result, err := handler(context.Background(), resolved, resolver(red, &reads), red, json.RawMessage(d.args))
				if c.wantErr {
					if err == nil || strings.Contains(err.Error(), "Hidden") || strings.Contains(err.Error(), foreignCanary) ||
						strings.Contains(err.Error(), "10") && strings.Contains(err.Error(), "scenario") {
						t.Fatalf("result = %+v, err = %v", result, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				var ids []int64
				switch got := result.(type) {
				case *ConnectionDeletion:
					for _, s := range got.Scenarios {
						ids = append(ids, s.ID)
					}
					if !got.ConfirmationRequired {
						t.Fatalf("result = %+v", got)
					}
				case *DataStoreDeletion:
					for _, s := range got.Scenarios {
						ids = append(ids, s.ID)
					}
					if !got.ConfirmationRequired {
						t.Fatalf("result = %+v", got)
					}
				}
				if fmt.Sprint(ids) != fmt.Sprint(c.wantIDs) {
					t.Fatalf("ids = %v, want %v", ids, c.wantIDs)
				}
			})
		}
	}
}
