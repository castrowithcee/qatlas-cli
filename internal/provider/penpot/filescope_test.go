package penpot

import (
	"strings"
	"testing"
)

func TestFileTargetAllowsOnlyItsFile(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(0))
	result, err := env.invoke(filesGet.ID, "file", `{"project_id":"`+projectA1+`","file_id":"`+fileA1+`"}`)
	if err != nil || !strings.Contains(result, `"Startseite"`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdProjectFile+","+cmdSummary+","+cmdPage {
		t.Fatalf("commands = %s", got)
	}

	// Another file of the same project: refused before the secret and any request, without naming it.
	calls = nil
	reads := *env.reads
	_, err = env.invoke(filesGet.ID, "file", `{"project_id":"`+projectA1+`","file_id":"`+fileOut+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), fileOut) || strings.Contains(err.Error(), projectA1) {
		t.Fatalf("err = %v", err)
	}
	if len(calls) != 0 || *env.reads != reads {
		t.Fatalf("calls = %d, secret reads = %d, want none", len(calls), *env.reads-reads)
	}

	// The listed file claimed in a project of no bound team: the project lists prove it is not reachable and the
	// project's files are never read.
	calls = nil
	_, err = env.invoke(filesGet.ID, "file", `{"project_id":"`+projectB1+`","file_id":"`+fileA1+`"}`)
	if !isInvalidRequest(err) || strings.Contains(strings.Join(commands(calls), ","), cmdProjectFile) {
		t.Fatalf("err = %v, commands = %v", err, commands(calls))
	}
	// The listed file claimed in another project of a bound team: not a file of that project.
	calls = nil
	_, err = env.invoke(filesGet.ID, "file", `{"project_id":"`+projectA2+`","file_id":"`+fileA1+`"}`)
	if !isInvalidRequest(err) || strings.Contains(strings.Join(commands(calls), ","), cmdSummary) {
		t.Fatalf("err = %v, commands = %v", err, commands(calls))
	}
}

func TestFileOnlyBindingRefusesProjectAndTeamLevelTools(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(0))
	for tool, arguments := range map[string]string{
		filesList.ID:    `{"project_id":"` + projectA1 + `"}`,
		projectsList.ID: `{"team_id":"` + teamA + `"}`,
		membersList.ID:  `{"team_id":"` + teamA + `"}`,
		webhooksList.ID: `{"team_id":"` + teamA + `"}`,
	} {
		if _, err := env.invoke(tool, "file", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", tool, err)
		}
	}
	for tool, arguments := range map[string]string{
		filesCreate.ID:    `{"project_id":"` + projectA1 + `","name":"x"}`,
		projectsCreate.ID: `{"team_id":"` + teamA + `","name":"x"}`,
		projectsRename.ID: `{"project_id":"` + projectA1 + `","name":"x"}`,
	} {
		if _, err := env.invokeConfirmed(tool, "writefile", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %v, secret reads = %d, want none", commands(calls), *env.reads)
	}
}

func TestProjectAndFileTargetsFormOneAllowList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(0))
	// The listed project is reachable as a whole, the listed file of another project singly.
	if _, err := env.invoke(filesList.ID, "mixed", `{"project_id":"`+projectA2+`"}`); err != nil {
		t.Fatalf("listed project: %v", err)
	}
	if _, err := env.invoke(filesGet.ID, "mixed", `{"project_id":"`+projectA1+`","file_id":"`+fileA1+`"}`); err != nil {
		t.Fatalf("listed file: %v", err)
	}
	calls = nil
	if _, err := env.invoke(filesGet.ID, "mixed", `{"project_id":"`+projectA1+`","file_id":"`+fileOut+`"}`); !isInvalidRequest(err) {
		t.Fatalf("other file of the unlisted project: %v", err)
	}
	if _, err := env.invoke(filesList.ID, "mixed", `{"project_id":"`+projectA1+`"}`); !isInvalidRequest(err) {
		t.Fatalf("a listed file does not open its project: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want none", commands(calls))
	}
}
