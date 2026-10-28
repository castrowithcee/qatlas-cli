package n8n

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	teamProject = "PROJECT_TEAM_0004DDDD"
	someUser    = "user-delete-0001"
)

// configText is a configuration file of one n8n connection with the given targets and exclude lines.
func configText(connection string) string {
	return `version: 1
services:
  svc:
    provider: n8n
    base_url: ` + baseURL + `
credentials:
  cred:
    type: env
    provider: n8n
    values:
      api-key: ` + apiKeyEnv + `
connections:
  conn:
    service: svc
    credential: cred
` + connection
}

// The core accepts exclude on the project and workflow kinds, and refuses wrong forms, repeats, wildcards and
// an exclusion equal to a target.
func TestCoreAcceptsOnlyValidExclusions(t *testing.T) {
	decode := func(connection string) error {
		_, err := config.Decode(strings.NewReader(configText(connection)), registry(t))
		return err
	}
	for _, valid := range []string{
		"    exclude: [project/p1, workflow/w1]\n",
		"    targets: [project/p2]\n    exclude: [project/p1]\n",
		"    targets: [workflow/w2]\n    exclude: [workflow/w1, project/p1]\n",
	} {
		if err := decode(valid); err != nil {
			t.Fatalf("%q: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"    exclude: [team/p1]\n",
		"    exclude: [project/p1, project/p1]\n",
		"    exclude: [project/*]\n",
		"    exclude: ['*']\n",
		"    exclude: ['']\n",
		"    targets: [project/p1]\n    exclude: [project/p1]\n",
		"    targets: [workflow/w1]\n    exclude: [workflow/w1]\n",
	} {
		if err := decode(invalid); err == nil {
			t.Fatalf("%q was accepted", invalid)
		} else if strings.Contains(err.Error(), "p1") || strings.Contains(err.Error(), "w1") {
			t.Fatalf("%q: error quotes a value: %v", invalid, err)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, kind := range metadata.Target.Kinds {
		if !kind.Excludable {
			t.Fatalf("kind %s is not excludable", kind.Name)
		}
	}
}

// An excluded connection works through the core: tools stay offered, a call on an excluded object is refused.
func TestCoreRunsAnExcludingConnection(t *testing.T) {
	cfg := coreConfig()
	cfg.Connections["excl"] = config.Connection{Service: "n8n", Credential: "n8n-reader", Effects: allEffects,
		Exclude: bothExclusions}
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"data":[],"nextCursor":null}`), nil
	})
	reads := 0
	env := &environment{core: application.New(registry(t), cfg, resolver(nil, &reads), nil)}
	if _, err := env.invoke(usersList.ID, "excl", `{}`); err != nil {
		t.Fatalf("an instance-wide tool must stay offered: %v", err)
	}
	if _, err := env.invoke(workflowsGet.ID, "excl", fmt.Sprintf(`{"workflow_id":%q}`, exclWorkflow)); err == nil ||
		len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
}

func membersBody(users ...string) string {
	rows := make([]string, len(users))
	for i, u := range users {
		rows[i] = fmt.Sprintf(`{"id":%q,"email":"e@example.invalid","role":"project:admin"}`, u)
	}
	return `{"data":[` + strings.Join(rows, ",") + `],"nextCursor":null}`
}

// usersDeleteServer answers members per project, the excluded workflow, and the delete.
func usersDeleteServer(t *testing.T, members map[string]string, workflow string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(r.URL.Path, apiPath)
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/projects/") && strings.HasSuffix(path, "/users"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/projects/"), "/users")
			if body, ok := members[id]; ok {
				return jsonResponse(200, body), nil
			}
			return jsonResponse(403, `{}`), nil
		case r.Method == http.MethodGet && path == "/workflows/"+exclWorkflow:
			return jsonResponse(200, workflow), nil
		case r.Method == http.MethodDelete && path == "/users/"+someUser:
			return jsonResponse(200, `{}`), nil
		}
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func TestUsersDeleteHonoursExclusions(t *testing.T) {
	resolved := excludingConnection(nil, bothExclusions...)
	deleteAll := fmt.Sprintf(`{"user_id":%q,"delete_owned_resources":true}`, someUser)
	transfer := func(project string) string {
		return fmt.Sprintf(`{"user_id":%q,"transfer_project_id":%q}`, someUser, project)
	}
	owned := workflowSharedJSON(exclWorkflow, teamProject)

	// A transfer into the excluded area is refused before any request; into a reachable project it is sent.
	_, calls, err := run(t, invokeUsersDelete, resolved, transfer(exclProject), nil)
	refused(t, err, calls, 0)
	out, calls, err := run(t, invokeUsersDelete, resolved, transfer(ownProject), usersDeleteServer(t, nil, owned))
	if err != nil || len(calls) != 1 || calls[0].query.Get("transferId") != ownProject || !strings.Contains(out, "transferred") {
		t.Fatalf("out = %s, err = %v, calls = %+v", out, err, calls)
	}

	// Deleting the resources of a member of the excluded project is refused.
	_, calls, err = run(t, invokeUsersDelete, resolved, deleteAll,
		usersDeleteServer(t, map[string]string{exclProject: membersBody("other", someUser)}, owned))
	refused(t, err, calls, 1)
	// A member of the project that owns the excluded workflow is refused.
	_, calls, err = run(t, invokeUsersDelete, resolved, deleteAll, usersDeleteServer(t,
		map[string]string{exclProject: membersBody("other"), teamProject: membersBody(someUser)}, owned))
	refused(t, err, calls, 3)
	// Membership that cannot be read cannot be disproved.
	_, calls, err = run(t, invokeUsersDelete, resolved, deleteAll, usersDeleteServer(t, nil, owned))
	refused(t, err, calls, 1)
	// A workflow whose owner is not reported cannot be placed.
	_, calls, err = run(t, invokeUsersDelete, resolved, deleteAll, usersDeleteServer(t,
		map[string]string{exclProject: membersBody("other")}, `{"id":"`+exclWorkflow+`","nodes":[],"connections":{}}`))
	refused(t, err, calls, 2)
	// Unrelated user: everything is disproved and the delete goes out.
	_, calls, err = run(t, invokeUsersDelete, resolved, deleteAll, usersDeleteServer(t,
		map[string]string{exclProject: membersBody("other"), teamProject: membersBody("other")}, owned))
	if err != nil || len(calls) != 4 || calls[3].method != http.MethodDelete {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	// Without exclusions nothing is read before the delete.
	_, calls, err = run(t, invokeUsersDelete, resolvedConnection(), deleteAll, usersDeleteServer(t, nil, owned))
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func auditLocations(locations ...string) string {
	return `{"Credentials Risk Report":{"risk":"credentials","sections":[` +
		`{"title":"Unused","description":"D","recommendation":"R","location":[` + strings.Join(locations, ",") + `]},` +
		`{"title":"Only excluded","description":"D","recommendation":"R","location":[` + locations[0] + `]},` +
		`{"title":"Settings","description":"D","recommendation":"R"}]}}`
}

func TestAuditDropsLocationsOfTheExcludedArea(t *testing.T) {
	node := func(workflow string) string {
		return fmt.Sprintf(`{"kind":"node","workflowId":%q,"workflowName":%q,"nodeId":"n","nodeName":"N","nodeType":"t"}`,
			workflow, exclCanary)
	}
	credential := func(id string) string { return fmt.Sprintf(`{"kind":"credential","id":%q,"name":%q}`, id, exclCanary) }
	body := auditLocations(credential("cred-excl"), credential("cred-ok"), node("wf-excl"), node("wf-ok"),
		node(exclWorkflow), `{"kind":"community","nodeType":"x","packageUrl":"u"}`)
	handler := func(r *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(r.URL.Path, apiPath)
		switch {
		case r.Method == http.MethodPost && path == "/audit":
			return jsonResponse(200, body), nil
		case path == "/credentials":
			return jsonResponse(200, `{"data":[`+
				`{"id":"cred-excl","name":"a","type":"t","shared":[{"id":"`+exclProject+`","role":"credential:owner"}]},`+
				`{"id":"cred-ok","name":"b","type":"t","shared":[{"id":"`+ownProject+`","role":"credential:owner"}]}`+
				`],"nextCursor":null}`), nil
		case path == "/workflows/wf-excl":
			return jsonResponse(200, workflowSharedJSON("wf-excl", exclProject)), nil
		case path == "/workflows/wf-ok":
			return jsonResponse(200, workflowSharedJSON("wf-ok", ownProject)), nil
		}
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
	out, calls, err := run(t, invokeAuditGenerate, excludingConnection(nil, bothExclusions...), `{}`, handler)
	if err != nil {
		t.Fatal(err)
	}
	var result AuditResult
	if err := json.Unmarshal([]byte(out), &result); err != nil || len(result.Reports) != 1 {
		t.Fatalf("%s, %v", out, err)
	}
	sections := result.Reports[0].Sections
	// The section whose only location is excluded is gone; the one without locations stays.
	if result.Reports[0].SectionCount != 2 || len(sections) != 2 || sections[0].Title != "Unused" ||
		sections[0].LocationCount != 3 || len(sections[0].Locations) != 3 || sections[1].Title != "Settings" {
		t.Fatalf("sections = %+v", sections)
	}
	for _, l := range sections[0].Locations {
		if l.ID == "cred-excl" || l.WorkflowID == "wf-excl" || l.WorkflowID == exclWorkflow {
			t.Fatalf("kept %+v", l)
		}
	}
	// The audit, one credential listing and each workflow once; the excluded workflow ID needs no request.
	if len(calls) != 4 {
		t.Fatalf("calls = %+v", calls)
	}
	// Only a workflow exclusion decides locally and reads nothing but the audit.
	_, calls, err = run(t, invokeAuditGenerate, excludingConnection(nil, "workflow/"+exclWorkflow), `{}`, handler)
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	// Without exclusions the report is unchanged and read alone.
	out, calls, err = run(t, invokeAuditGenerate, resolvedConnection(), `{}`, handler)
	if err != nil || len(calls) != 1 || !strings.Contains(out, "cred-excl") {
		t.Fatalf("out = %s, err = %v, calls = %+v", out, err, calls)
	}
	if !strings.Contains(auditGenerate.Description, "excludes projects or workflows") {
		t.Fatal("the audit description lacks the exclusion warning")
	}
}

func scFile(typ, id, ownerProject string) string {
	owner := ""
	if ownerProject != "" {
		owner = fmt.Sprintf(`,"owner":{"type":"team","projectId":%q,"projectName":%q}`, ownerProject, exclCanary)
	}
	return fmt.Sprintf(`{"id":%q,"name":%q,"type":%q,"status":"modified","location":"local","conflict":false%s}`,
		id, exclCanary, typ, owner)
}

func sourceControlServer(t *testing.T, push, pull []string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == apiPath+"/source-control/status":
			files := push
			if r.URL.Query().Get("direction") == "pull" {
				files = pull
			}
			return jsonResponse(200, `{"data":[`+strings.Join(files, ",")+`]}`), nil
		case r.Method == http.MethodPost && (r.URL.Path == apiPath+"/source-control/pull" ||
			r.URL.Path == apiPath+"/source-control/push"):
			if strings.HasSuffix(r.URL.Path, "/pull") {
				return jsonResponse(200, `[]`), nil
			}
			return jsonResponse(200, `{"data":[]}`), nil
		}
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func TestSourceControlStatusLeavesOutExcludedFiles(t *testing.T) {
	files := []string{scFile("workflow", "wf-ok", ownProject), scFile("workflow", "wf-excl", exclProject),
		scFile("workflow", exclWorkflow, ownProject), scFile("workflow", "wf-unowned", ""),
		scFile("credential", "c-excl", exclProject), scFile("credential", "c-ok", ownProject),
		scFile("folders", "f1", ""), scFile("datatable", "d-excl", exclProject), scFile("project", exclProject, ""),
		scFile("project", ownProject, ""), scFile("tags", "tags", ""), scFile("variables", "v", ""),
		scFile("file", "x", "")}
	out, _, err := run(t, invokeSourceControlStatus, excludingConnection(nil, bothExclusions...),
		`{"direction":"pull"}`, sourceControlServer(t, nil, files))
	if err != nil {
		t.Fatal(err)
	}
	var status SourceControlStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range status.Files {
		ids = append(ids, f.ID)
	}
	if got, want := strings.Join(ids, ","), "wf-ok,c-ok,"+ownProject+",tags,v"; got != want || status.Count != 5 {
		t.Fatalf("ids = %s, want %s (count %d)", got, want, status.Count)
	}
	// Only a workflow exclusion leaves owner-less files of other types alone.
	out, _, err = run(t, invokeSourceControlStatus, excludingConnection(nil, "workflow/"+exclWorkflow),
		`{"direction":"push"}`, sourceControlServer(t, []string{scFile("workflow", exclWorkflow, ""),
			scFile("folders", "f1", ""), scFile("workflow", "wf-ok", "")}, nil))
	if err != nil || strings.Contains(out, exclWorkflow) || !strings.Contains(out, "f1") || !strings.Contains(out, "wf-ok") {
		t.Fatalf("out = %s, err = %v", out, err)
	}
}

func TestSourceControlPullIsCheckedByItsPreview(t *testing.T) {
	resolved := excludingConnection(nil, bothExclusions...)
	for name, pull := range map[string][]string{
		"excluded workflow": {scFile("workflow", exclWorkflow, ownProject)},
		"excluded owner":    {scFile("credential", "c", exclProject)},
		"unplaced folder":   {scFile("folders", "f1", "")},
		"unknown type":      {scFile("file", "x", "")},
	} {
		_, calls, err := run(t, invokeSourceControlPull, resolved, `{}`, sourceControlServer(t, nil, pull))
		if !isInvalidRequest(err) || len(calls) != 1 || calls[0].method != http.MethodGet ||
			calls[0].query.Get("direction") != "pull" || strings.Contains(err.Error(), exclWorkflow) {
			t.Fatalf("%s: err = %v, calls = %+v", name, err, calls)
		}
	}
	_, calls, err := run(t, invokeSourceControlPull, resolved, `{}`, sourceControlServer(t, nil,
		[]string{scFile("workflow", "wf-ok", ownProject), scFile("tags", "tags", ""), scFile("variables", "v", "")}))
	if err != nil || len(calls) != 2 || calls[1].method != http.MethodPost {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	// Without exclusions the pull is a single request.
	_, calls, err = run(t, invokeSourceControlPull, resolvedConnection(), `{}`, sourceControlServer(t, nil, nil))
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestSourceControlPushRefusesExcludedFiles(t *testing.T) {
	push := func(files string) string { return `{"commit_message":"m","files":[` + files + `]}` }
	named := func(typ, id string) string { return fmt.Sprintf(`{"type":%q,"id":%q}`, typ, id) }
	preview := []string{scFile("workflow", "wf-ok", ownProject), scFile("workflow", "wf-excl", exclProject),
		scFile("credential", "c-excl", exclProject), scFile("datatable", "d-ok", ownProject)}
	server := sourceControlServer(t, preview, nil)
	both := excludingConnection(nil, bothExclusions...)

	for name, tt := range map[string]struct {
		files string
		reads int
	}{
		"excluded workflow":   {named("workflow", exclWorkflow), 1},
		"excluded owner":      {named("workflow", "wf-excl"), 1},
		"excluded credential": {named("credential", "c-excl"), 1},
		"unlisted":            {named("workflow", "wf-none"), 1},
		"excluded project":    {named("project", exclProject), 0},
		"folders":             {named("folders", "f1"), 0},
		"one of several":      {named("tags", "tags") + "," + named("workflow", "wf-excl"), 1},
	} {
		_, calls, err := run(t, invokeSourceControlPush, both, push(tt.files), server)
		if !isInvalidRequest(err) || len(calls) != tt.reads {
			t.Fatalf("%s: err = %v, calls = %+v", name, err, calls)
		}
		for _, c := range calls {
			if c.method != http.MethodGet {
				t.Fatalf("%s: a change was sent: %+v", name, c)
			}
		}
	}
	// Tags, variables, and reachable objects go out; tags and variables need no preview.
	_, calls, err := run(t, invokeSourceControlPush, both, push(named("tags", "tags")+","+named("variables", "v")), server)
	if err != nil || len(calls) != 1 || calls[0].method != http.MethodPost {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	_, calls, err = run(t, invokeSourceControlPush, both, push(named("workflow", "wf-ok")+","+named("datatable", "d-ok")), server)
	if err != nil || len(calls) != 2 || calls[1].method != http.MethodPost {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	// With only a workflow exclusion, folders are allowed and the owner is not needed.
	_, calls, err = run(t, invokeSourceControlPush, excludingConnection(nil, "workflow/"+exclWorkflow),
		push(named("folders", "f1")+","+named("workflow", "wf-excl")), server)
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}
