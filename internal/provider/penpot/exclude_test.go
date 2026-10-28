package penpot

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	fileEx    = "00000000-0000-0000-0000-0000000000d1"
	fileP2    = "00000000-0000-0000-0000-0000000000d2"
	deletedEx = "00000000-0000-0000-0000-0000000000d3"
	deletedP2 = "00000000-0000-0000-0000-0000000000d4"
)

// excludeConfig adds connections with exclusions to the test configuration. Projects: A1 holds fileA1 and
// fileEx, A2 holds fileP2; the deleted files of team A are deletedA1 and deletedEx (both A1) and deletedP2 (A2).
func excludeConfig(t *testing.T, read, write string) *config.Config {
	cfg := withFiles(testConfig(), read, write)
	var tools []string
	for _, d := range registry(t).Provider(Provider) {
		tools = append(tools, d.ID)
	}
	add := func(name string, targets, excludes []string) {
		connection := config.Connection{Service: "penpot", Credential: "token", Effects: cfg.Connections["write"].Effects,
			Tools: tools, Targets: targets, Exclude: excludes}
		connection.Files = config.Files{Read: []string{read}, Write: []string{write}}
		cfg.Connections[name] = connection
	}
	team := "team/" + teamA
	add("exfile", []string{team}, []string{"file/" + fileEx})
	add("exdeleted", []string{team}, []string{"file/" + deletedEx})
	add("exproject", []string{team}, []string{"project/" + projectA2})
	add("exboth", []string{team}, []string{"project/" + projectA2, "file/" + fileEx})
	add("exfiletarget", []string{team, "file/" + fileP2}, []string{"project/" + projectA2})
	add("exprojectnarrow", []string{team, "project/" + projectA1}, []string{"file/" + fileEx})
	add("exother", []string{team}, []string{"file/" + fileOut, "project/" + projectB1})
	return cfg
}

func excludeHandler(change func(call)) func(call) (*http.Response, error) {
	base := recoveryHandler(recoveryAnswers)
	return func(c call) (*http.Response, error) {
		if isChange(c.command()) && change != nil {
			change(c)
		}
		switch c.command() {
		case cmdProjectFile:
			switch c.body["project-id"] {
			case projectA1:
				return jsonResponse(200, `[{"id":"`+fileA1+`","name":"Startseite","projectId":"`+projectA1+`"},`+
					`{"id":"`+fileEx+`","name":"Geheim","projectId":"`+projectA1+`"}]`), nil
			case projectA2:
				return jsonResponse(200, `[{"id":"`+fileP2+`","name":"Zwei","projectId":"`+projectA2+`"}]`), nil
			}
			return jsonResponse(200, `[]`), nil
		case cmdDeletedFiles:
			if c.body["team-id"] == teamA {
				return jsonResponse(200, `[{"id":"`+deletedA1+`","project-id":"`+projectA1+`","team-id":"`+teamA+`"},`+
					`{"id":"`+deletedEx+`","project-id":"`+projectA1+`","team-id":"`+teamA+`"},`+
					`{"id":"`+deletedP2+`","project-id":"`+projectA2+`","team-id":"`+teamA+`"}]`), nil
			}
		case cmdTeams:
			return jsonResponse(200, teamsBody()), nil
		case cmdTeamMembers, cmdSetMemberRole, cmdDeleteMember:
			return teamHandler(200, ``)(c)
		case cmdWebhooks, cmdUpdateWebhook, cmdDeleteWebhook:
			return webhookHandler(200, ``)(c)
		case cmdRestoreFiles, cmdPurgeFiles:
			return base(c)
		case cmdCreateProject, cmdCreateTeam:
			return jsonResponse(200, `{"id":"`+newProject+`"}`), nil
		}
		if isChange(c.command()) {
			return jsonResponse(200, ``), nil
		}
		return base(c)
	}
}

func newExcludeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	read, write := t.TempDir(), t.TempDir()
	return &environment{core: application.New(registry(t), excludeConfig(t, read, write), resolver(red, &reads), red),
		red: red, reads: &reads, read: read, write: write}
}

func TestExcludeParse(t *testing.T) {
	team := "team/" + teamA
	for _, test := range []struct {
		name     string
		targets  []string
		excludes []string
		valid    bool
	}{
		{"project and file", []string{team}, []string{"project/" + projectA2, "file/" + fileEx}, true},
		{"outside every target", []string{team, "project/" + projectA1}, []string{"project/" + projectA2}, true},
		{"team", []string{team, "team/" + teamB}, []string{"team/" + teamB}, false},
		{"another team", []string{team}, []string{"team/" + teamB}, false},
		{"repeated project", []string{team}, []string{"project/" + projectA2, "project/" + projectA2}, false},
		{"repeated file", []string{team}, []string{"file/" + fileEx, "file/" + fileEx}, false},
		{"equal to a project target", []string{team, "project/" + projectA1}, []string{"project/" + projectA1}, false},
		{"equal to a file target", []string{team, "file/" + fileA1}, []string{"file/" + fileA1}, false},
		{"file and project of one id", []string{team, "file/" + fileA1}, []string{"project/" + fileA1}, true},
		{"malformed", []string{team}, []string{"project/x"}, false},
		{"unknown form", []string{team}, []string{"folder/" + projectA2}, false},
		{"empty", []string{team}, []string{""}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseScope(test.targets, test.excludes...)
			if (err == nil) != test.valid {
				t.Fatalf("parseScope() = %v, valid = %t", err, test.valid)
			}
			if err != nil {
				for _, value := range test.excludes {
					if id := value[strings.Index(value, "/")+1:]; len(id) == 36 && strings.Contains(err.Error(), id) {
						t.Fatalf("error quotes %q: %v", value, err)
					}
				}
			}
		})
	}
}

// An exclusion is no target: restrictions and reach stay as they are, and only the two checks read it.
func TestExcludeIsNoTarget(t *testing.T) {
	plain, err := parseScope([]string{"team/" + teamA})
	if err != nil {
		t.Fatal(err)
	}
	excluding, err := parseScope([]string{"team/" + teamA}, "project/"+projectA2, "file/"+fileEx)
	if err != nil {
		t.Fatal(err)
	}
	if excluding.narrowed() != plain.narrowed() || excluding.fileOnly() != plain.fileOnly() || excluding.narrowed() || excluding.fileOnly() {
		t.Fatalf("narrowed = %t, fileOnly = %t", excluding.narrowed(), excluding.fileOnly())
	}
	if excluding.allowsProject(projectA2) || !excluding.allowsProject(projectA1) ||
		excluding.allowsFile(projectA1, fileEx) || excluding.allowsFile(projectA2, fileP2) || !excluding.allowsFile(projectA1, fileA1) {
		t.Fatalf("scope = %+v", excluding)
	}
	// Exclusion wins against a file target and against a project target.
	both, err := parseScope([]string{"team/" + teamA, "project/" + projectA1, "file/" + fileP2}, "project/"+projectA2, "file/"+fileA1)
	if err != nil {
		t.Fatal(err)
	}
	if both.allowsFile(projectA2, fileP2) || both.allowsFile(projectA1, fileA1) || !both.allowsFile(projectA1, fileEx) {
		t.Fatalf("scope = %+v", both)
	}
	fileTargets := []string{"team/" + teamA, "file/" + fileA1}
	for _, tool := range registry(t).Provider(Provider) {
		wantOK, wantMissing := reaches(tool.ID, fileTargets)
		if ok, missing := reaches(tool.ID, fileTargets); ok != wantOK || len(missing) != len(wantMissing) {
			t.Fatalf("%s: reaches changed", tool.ID)
		}
		if ok, _ := reaches(tool.ID, []string{"team/" + teamA}); !ok {
			t.Fatalf("%s: not reached with a team target", tool.ID)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, kind := range metadata.Target.Kinds {
		if want := kind.Name != "team"; kind.Excludable != want {
			t.Fatalf("kind %s Excludable = %t", kind.Name, kind.Excludable)
		}
	}
}

func TestExcludeListsLeaveOutExcludedRows(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, excludeHandler(nil))
	result, err := env.invoke(projectsList.ID, "exproject", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, projectA1) || strings.Contains(result, projectA2) || strings.Contains(result, `"App"`) {
		t.Fatalf("projects = %s, err = %v", result, err)
	}
	result, err = env.invoke(filesList.ID, "exfile", `{"project_id":"`+projectA1+`"}`)
	if err != nil || !strings.Contains(result, fileA1) || strings.Contains(result, fileEx) || strings.Contains(result, "Geheim") ||
		!strings.Contains(result, `"count":1`) {
		t.Fatalf("files = %s, err = %v", result, err)
	}
	// The excluded project's own list is refused before any request; with a file target it stays refused.
	for _, connection := range []string{"exproject", "exfiletarget"} {
		calls = nil
		reads := *env.reads
		_, err = env.invoke(filesList.ID, connection, `{"project_id":"`+projectA2+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), projectA2) || strings.Contains(err.Error(), "exclu") ||
			len(calls) != 0 || *env.reads != reads {
			t.Fatalf("%s: err = %v, calls = %v", connection, err, commands(calls))
		}
	}
}

// Every tool that acts on one file refuses an excluded file, or a file of an excluded project, before the secret
// and any request, without naming either.
func TestExcludeRefusesDetailsAndChangesLocally(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, excludeHandler(nil))
	onEx := `"project_id":"` + projectA1 + `","file_id":"` + fileEx + `"`
	onProject := `"project_id":"` + projectA2 + `","file_id":"` + fileP2 + `"`
	local := `"local_path":"/nonexistent/a.penpot"`
	for _, test := range []struct{ tool, connection, args string }{
		{filesGet.ID, "exfile", `{` + onEx + `}`},
		{filesGet.ID, "exproject", `{` + onProject + `}`},
		{filesGet.ID, "exfiletarget", `{` + onProject + `}`},
		{filesGet.ID, "exprojectnarrow", `{` + onEx + `}`},
		{filesRename.ID, "exfile", `{` + onEx + `,"name":"x"}`},
		{filesDelete.ID, "exfile", `{` + onEx + `}`},
		{filesExport.ID, "exfile", `{` + onEx + `,` + local + `}`},
		{filesExport.ID, "exfiletarget", `{` + onProject + `,` + local + `}`},
		{filesImport.ID, "exproject", `{"project_id":"` + projectA2 + `","name":"x",` + local + `}`},
		{filesCreate.ID, "exproject", `{"project_id":"` + projectA2 + `","name":"x"}`},
		{projectsRename.ID, "exproject", `{"project_id":"` + projectA2 + `","name":"x"}`},
		{projectsDelete.ID, "exproject", `{"project_id":"` + projectA2 + `"}`},
		{filesMove.ID, "exfile", `{` + onEx + `,"target_project_id":"` + projectA2 + `"}`},
		{filesMove.ID, "exproject", `{` + fileArgs + `,"target_project_id":"` + projectA2 + `"}`},
		{filesMove.ID, "exproject", `{` + onProject + `,"target_project_id":"` + projectA1 + `"}`},
		{commentsThreads.ID, "exfile", `{` + onEx + `}`},
		{commentsList.ID, "exproject", `{` + onProject + `,"thread_id":"` + pageID + `"}`},
		{commentsCreate.ID, "exfile", `{` + onEx + `,"page_id":"` + pageID + `","content":"x","x":1,"y":1}`},
		{librariesList.ID, "exfile", `{` + onEx + `}`},
		{librariesShare.ID, "exfile", `{` + onEx + `,"shared":true}`},
		{librariesLink.ID, "exfile", `{` + onEx + `,"library_project_id":"` + projectA1 + `","library_id":"` + fileA1 + `"}`},
		{librariesLink.ID, "exfile", `{` + fileArgs + `,"library_project_id":"` + projectA1 + `","library_id":"` + fileEx + `"}`},
		{librariesLink.ID, "exproject", `{` + fileArgs + `,"library_project_id":"` + projectA2 + `","library_id":"` + fileP2 + `"}`},
		{snapshotsCreate.ID, "exfile", `{` + onEx + `}`},
		{snapshotsRestore.ID, "exfile", `{` + onEx + `,"snapshot_id":"` + snapshotA + `"}`},
		{filesRestore.ID, "exdeleted", `{"project_id":"` + projectA1 + `","file_id":"` + deletedEx + `"}`},
		{filesPurge.ID, "exdeleted", `{"project_id":"` + projectA1 + `","file_id":"` + deletedEx + `"}`},
		{filesRestore.ID, "exproject", `{"project_id":"` + projectA2 + `","file_id":"` + deletedP2 + `"}`},
		{filesPurge.ID, "exproject", `{"project_id":"` + projectA2 + `","file_id":"` + deletedP2 + `"}`},
		{mediaUpload.ID, "exfile", `{` + onEx + `,"name":"a.png","content_base64":"aGk="}`},
		{mediaFromURL.ID, "exproject", `{` + onProject + `,"url":"https://images.example.com/a.png"}`},
	} {
		_, err := env.invokeConfirmed(test.tool, test.connection, test.args)
		if !isInvalidRequest(err) {
			t.Errorf("%s on %s: err = %v", test.tool, test.connection, err)
			continue
		}
		for _, id := range []string{fileEx, fileP2, deletedEx, deletedP2, projectA2} {
			if strings.Contains(err.Error(), id) {
				t.Errorf("%s: error names %s", test.tool, id)
			}
		}
		if strings.Contains(err.Error(), "exclu") {
			t.Errorf("%s: error names the exclusion: %v", test.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %v, secret reads = %d, want none", commands(calls), *env.reads)
	}
}

// Next to the exclusions, the rest of the team stays reachable.
func TestExcludeLeavesTheRestReachable(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, excludeHandler(nil))
	if _, err := env.invoke(filesGet.ID, "exboth", `{`+fileArgs+`}`); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := env.invokeConfirmed(filesRename.ID, "exboth", `{`+fileArgs+`,"name":"x"}`); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := env.invoke(filesGet.ID, "exprojectnarrow", `{`+fileArgs+`}`); err != nil {
		t.Fatalf("get in the targeted project: %v", err)
	}
	if _, err := env.invokeConfirmed(filesMove.ID, "exfile", `{`+fileArgs+`,"target_project_id":"`+projectA2+`"}`); err != nil {
		t.Fatalf("move: %v", err)
	}
	if _, err := env.invokeConfirmed(filesRestore.ID, "exfile", `{"project_id":"`+projectA1+`","file_id":"`+deletedA1+`"}`); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

func TestExcludeLibrariesCountExcludedAsOutside(t *testing.T) {
	var calls []call
	answer := `[{"id":"` + libA1 + `","name":"Lib","projectId":"` + projectA1 + `","teamId":"` + teamA + `"},` +
		`{"id":"` + fileEx + `","name":"Geheim","projectId":"` + projectA1 + `","teamId":"` + teamA + `"},` +
		`{"id":"` + fileP2 + `","name":"Zwei","projectId":"` + projectA2 + `","teamId":"` + teamA + `"}]`
	base := excludeHandler(nil)
	env := newExcludeEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.command() == cmdFileLibraries {
			return jsonResponse(200, answer), nil
		}
		return base(c)
	})
	result, err := env.invoke(librariesList.ID, "exboth", `{`+fileArgs+`}`)
	if err != nil || !strings.Contains(result, libA1) || strings.Contains(result, fileEx) || strings.Contains(result, fileP2) ||
		strings.Contains(result, "Geheim") || strings.Contains(result, "Zwei") || !strings.Contains(result, `"outside_targets":2`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestExcludeProjectDeleteProvesNoExcludedFileBelow(t *testing.T) {
	var calls []call
	var changed []string
	env := newExcludeEnvironment(t, &calls, excludeHandler(func(c call) { changed = append(changed, c.command()) }))
	delete := func(connection, project string) error {
		calls, changed = nil, nil
		_, err := env.invokeConfirmed(projectsDelete.ID, connection, `{"project_id":"`+project+`"}`)
		return err
	}
	// Live and deleted excluded files below the project refuse it; nothing is changed and no error names them.
	for _, connection := range []string{"exfile", "exdeleted", "exboth"} {
		err := delete(connection, projectA1)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), fileEx) || strings.Contains(err.Error(), deletedEx) ||
			strings.Contains(err.Error(), projectA1) || strings.Contains(err.Error(), "exclu") || len(changed) != 0 {
			t.Fatalf("%s: err = %v, changes = %v", connection, err, changed)
		}
	}
	// An excluded file elsewhere, an excluded project, or none at all leave the delete allowed.
	for _, connection := range []string{"write", "exother", "exproject"} {
		if err := delete(connection, projectA1); err != nil || len(changed) != 1 {
			t.Fatalf("%s: err = %v, changes = %v", connection, err, changed)
		}
	}
	// Without a file exclusion no file list is read.
	if err := delete("exproject", projectA1); err != nil {
		t.Fatal(err)
	}
	for _, name := range commands(calls) {
		if name == cmdProjectFile || name == cmdDeletedFiles {
			t.Fatalf("commands = %v", commands(calls))
		}
	}
}

func TestExcludeProjectDeleteFailsClosed(t *testing.T) {
	for name, handler := range map[string]func(call) (*http.Response, error){
		"files fail": func(c call) (*http.Response, error) {
			if c.command() == cmdProjectFile {
				return jsonResponse(500, bodyCanary), nil
			}
			return excludeHandler(nil)(c)
		},
		"deleted files fail": func(c call) (*http.Response, error) {
			if c.command() == cmdDeletedFiles {
				return jsonResponse(500, bodyCanary), nil
			}
			return excludeHandler(nil)(c)
		},
		"deleted files not a list": func(c call) (*http.Response, error) {
			if c.command() == cmdDeletedFiles {
				return jsonResponse(200, `{"files":[]}`), nil
			}
			return excludeHandler(nil)(c)
		},
		"deleted file without id": func(c call) (*http.Response, error) {
			if c.command() == cmdDeletedFiles {
				return jsonResponse(200, `[{"project-id":"`+projectA1+`"}]`), nil
			}
			return excludeHandler(nil)(c)
		},
		"deleted file excluded without project": func(c call) (*http.Response, error) {
			if c.command() == cmdDeletedFiles {
				return jsonResponse(200, `[{"id":"`+deletedEx+`"}]`), nil
			}
			return excludeHandler(nil)(c)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newExcludeEnvironment(t, &calls, handler)
			_, err := env.invokeConfirmed(projectsDelete.ID, "exdeleted", `{"project_id":"`+projectA1+`"}`)
			if err == nil || len(changes(calls)) != 0 {
				t.Fatalf("err = %v, commands = %v", err, commands(calls))
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestExcludeTeamDeleteProvesNoExcludedObjectBelow(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, excludeHandler(nil))
	args := `{"team_id":"` + teamA + `"}`
	for _, connection := range []string{"exfile", "exdeleted", "exproject", "exboth"} {
		calls = nil
		_, err := env.invokeConfirmed(teamsDelete.ID, connection, args)
		if !isInvalidRequest(err) || len(changes(calls)) != 0 || strings.Contains(err.Error(), teamA) ||
			strings.Contains(err.Error(), "exclu") {
			t.Fatalf("%s: err = %v, commands = %v", connection, err, commands(calls))
		}
	}
	// Without an exclusion in the team the delete stays allowed, and without any exclusion it needs no proof.
	for connection, want := range map[string][]string{
		"write":   {cmdDeleteTeam},
		"exother": {cmdProjects, cmdProjectFile, cmdProjectFile, cmdProjectFile, cmdDeletedFiles, cmdDeleteTeam},
	} {
		calls = nil
		if _, err := env.invokeConfirmed(teamsDelete.ID, connection, args); err != nil {
			t.Fatalf("%s: %v", connection, err)
		}
		if got := strings.Join(commands(calls), ","); got != strings.Join(want, ",") {
			t.Fatalf("%s: commands = %s", connection, got)
		}
	}
	// A narrowed connection keeps its refusal; exclusions alone do not trigger it.
	if _, err := env.invokeConfirmed(teamsDelete.ID, "exprojectnarrow", args); !isInvalidRequest(err) {
		t.Fatalf("narrowed: %v", err)
	}
}

func TestExcludeTeamDeleteFailsClosed(t *testing.T) {
	for name, handler := range map[string]func(call) (*http.Response, error){
		"projects fail": func(c call) (*http.Response, error) {
			if c.command() == cmdProjects {
				return jsonResponse(500, bodyCanary), nil
			}
			return excludeHandler(nil)(c)
		},
		"files fail": func(c call) (*http.Response, error) {
			if c.command() == cmdProjectFile && c.body["project-id"] == projectA2 {
				return jsonResponse(500, bodyCanary), nil
			}
			return excludeHandler(nil)(c)
		},
		"deleted files fail": func(c call) (*http.Response, error) {
			if c.command() == cmdDeletedFiles {
				return jsonResponse(500, bodyCanary), nil
			}
			return excludeHandler(nil)(c)
		},
		"project without id": func(c call) (*http.Response, error) {
			if c.command() == cmdProjects {
				return jsonResponse(200, `[{"name":"x"}]`), nil
			}
			return excludeHandler(nil)(c)
		},
		"too many projects": func(c call) (*http.Response, error) {
			if c.command() == cmdProjects {
				var b strings.Builder
				b.WriteString("[")
				for i := 0; i <= maxEvidenceProjects; i++ {
					if i > 0 {
						b.WriteString(",")
					}
					b.WriteString(`{"id":"00000000-0000-0000-0001-` + strings.Repeat("0", 8) + hex4(i) + `"}`)
				}
				b.WriteString("]")
				return jsonResponse(200, b.String()), nil
			}
			return jsonResponse(200, `[]`), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newExcludeEnvironment(t, &calls, handler)
			_, err := env.invokeConfirmed(teamsDelete.ID, "exdeleted", `{"team_id":"`+teamA+`"}`)
			if err == nil || len(changes(calls)) != 0 || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, commands = %v", err, commands(calls))
			}
		})
	}
}

func hex4(i int) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[i>>12&15], digits[i>>8&15], digits[i>>4&15], digits[i&15]})
}

// Team-wide objects keep their reach with exclusions.
func TestExcludeLeavesTeamObjectsReachable(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, excludeHandler(nil))
	for tool, args := range map[string]string{
		teamsList.ID:      `{}`,
		membersList.ID:    teamArgs()[membersList.ID],
		membersSetRole.ID: teamArgs()[membersSetRole.ID],
		membersRemove.ID:  teamArgs()[membersRemove.ID],
		teamsCreate.ID:    teamArgs()[teamsCreate.ID],
		webhooksList.ID:   `{"team_id":"` + teamA + `"}`,
		webhooksUpdate.ID: `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `","mtype":"application/json","is_active":true}`,
		webhooksDelete.ID: `{"team_id":"` + teamA + `","webhook_id":"` + hookA + `"}`,
		projectsCreate.ID: `{"team_id":"` + teamA + `","name":"x"}`,
		projectsList.ID:   `{"team_id":"` + teamA + `"}`,
	} {
		if _, err := env.invokeConfirmed(tool, "exboth", args); err != nil {
			t.Errorf("%s: %v", tool, err)
		}
	}
}

func TestExcludeInvalidIsRefusedBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newExcludeEnvironment(t, &calls, excludeHandler(nil))
	resolved := &config.Resolved{Provider: Provider, BaseURL: baseURL, Targets: []string{"team/" + teamA},
		Exclude: []string{"team/" + teamB}}
	if _, err := boundScope(resolved); err == nil || strings.Contains(err.Error(), teamB) {
		t.Fatalf("err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %v, reads = %d", commands(calls), *env.reads)
	}
}
