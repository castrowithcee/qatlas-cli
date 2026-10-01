package penpot

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

const (
	newProject = "00000000-0000-0000-0000-000000000081"
	newFile    = "00000000-0000-0000-0000-000000000082"
)

var errResetByPeer = errors.New("connection reset by peer")

var manageTools = []string{projectsCreate.ID, projectsRename.ID, projectsDelete.ID, filesCreate.ID, filesRename.ID,
	filesMove.ID}

// manageHandler serves the project and file lists of filesHandler and answers every management change with
// the given status and body.
func manageHandler(status int, answer string) func(call) (*http.Response, error) {
	files := filesHandler(0)
	return func(c call) (*http.Response, error) {
		if isChange(c.command()) {
			return jsonResponse(status, answer), nil
		}
		return files(c)
	}
}

func TestManageCreateAndRenameSendOneFixedCommand(t *testing.T) {
	for _, test := range []struct {
		tool, connection, args, command, want string
		body                                  map[string]any
		reads                                 []string
		answer                                string
	}{
		{projectsCreate.ID, "write", `{"team_id":"` + teamA + `","name":"Neu"}`, cmdCreateProject, newProject,
			map[string]any{"team-id": teamA, "name": "Neu"}, nil, `{"id":"` + newProject + `","teamId":"` + teamA + `"}`},
		{projectsRename.ID, "write", `{"project_id":"` + projectA1 + `","name":"Umbenannt"}`, cmdRenameProject, `"renamed":true`,
			map[string]any{"id": projectA1, "name": "Umbenannt"}, []string{cmdProjects}, `null`},
		{projectsDelete.ID, "write", `{"project_id":"` + projectA2 + `"}`, cmdDeleteProject, `"deleted":true`,
			map[string]any{"id": projectA2}, []string{cmdProjects}, ``},
		{filesCreate.ID, "write", `{"project_id":"` + projectA1 + `","name":"Datei"}`, cmdCreateFile, newFile,
			map[string]any{"project-id": projectA1, "name": "Datei"}, []string{cmdProjects}, `{"id":"` + newFile + `","name":"Datei"}`},
		{filesRename.ID, "write", `{` + fileArgs + `,"name":"Anders"}`, cmdRenameFile, `"renamed":true`,
			map[string]any{"id": fileA1, "name": "Anders"}, []string{cmdProjects, cmdProjectFile}, `{"id":"` + fileA1 + `"}`},
	} {
		var calls []call
		env := newEnvironment(t, &calls, manageHandler(200, test.answer))
		result, err := env.invokeConfirmed(test.tool, test.connection, test.args)
		if err != nil || !strings.Contains(result, test.want) || strings.Contains(result, "Neu") ||
			strings.Contains(result, "Anders") || strings.Contains(result, "Datei") {
			t.Errorf("%s: result = %s, err = %v", test.tool, result, err)
			continue
		}
		sent := changes(calls)
		if len(sent) != 1 || sent[0].command() != test.command || sent[0].method != http.MethodPost {
			t.Errorf("%s: sent = %+v", test.tool, sent)
			continue
		}
		for key, want := range test.body {
			if sent[0].body[key] != want {
				t.Errorf("%s: body[%s] = %v, want %v", test.tool, key, sent[0].body[key], want)
			}
		}
		if len(sent[0].body) != len(test.body) {
			t.Errorf("%s: body = %v", test.tool, sent[0].body)
		}
		if got, want := strings.Join(commands(calls), ","), strings.Join(append(test.reads, test.command), ","); got != want {
			t.Errorf("%s: commands = %s, want %s", test.tool, got, want)
		}
	}
}

func TestManageMoveSendsOneMoveBetweenBoundProjects(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, manageHandler(200, `null`))
	result, err := env.invokeConfirmed(filesMove.ID, "writetwo", `{`+fileArgs+`,"target_project_id":"`+projectB1+`"}`)
	if err != nil || !strings.Contains(result, `"moved":true`) || !strings.Contains(result, projectB1) ||
		!strings.Contains(result, projectA1) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := changes(calls)
	ids, _ := sent[0].body["ids"].([]any)
	if len(sent) != 1 || sent[0].command() != cmdMoveFiles || sent[0].body["project-id"] != projectB1 ||
		len(ids) != 1 || ids[0] != fileA1 || len(sent[0].body) != 2 {
		t.Fatalf("sent = %+v", sent)
	}

	// Within one team, too.
	calls = nil
	if _, err := env.invokeConfirmed(filesMove.ID, "write", `{`+fileArgs+`,"target_project_id":"`+projectA2+`"}`); err != nil ||
		len(changes(calls)) != 1 {
		t.Fatalf("err = %v, commands = %v", err, commands(calls))
	}
}

func TestManageRefusesForeignTargetsBeforeSecretAndChange(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, manageHandler(200, `{}`))

	// Local refusals: before secret and request, never naming the ID.
	for _, test := range []struct{ tool, connection, args string }{
		{projectsCreate.ID, "write", `{"team_id":"` + teamB + `","name":"x"}`},
		{projectsCreate.ID, "write", `{"team_id":"` + teamForeign + `","name":"x"}`},
		{projectsCreate.ID, "writenarrow", `{"team_id":"` + teamA + `","name":"x"}`},
		{projectsRename.ID, "writenarrow", `{"project_id":"` + projectA2 + `","name":"x"}`},
		{filesCreate.ID, "writenarrow", `{"project_id":"` + projectA2 + `","name":"x"}`},
		{filesRename.ID, "writenarrow", `{"project_id":"` + projectA2 + `","file_id":"` + fileA1 + `","name":"x"}`},
		{filesMove.ID, "writenarrow", `{` + fileArgs + `,"target_project_id":"` + projectA2 + `"}`},
		{filesMove.ID, "write", `{` + fileArgs + `,"target_project_id":"` + projectA1 + `"}`},
		{filesMove.ID, "write", `{` + fileArgs + `,"target_project_id":"not-a-uuid"}`},
		{filesRename.ID, "write", `{` + fileArgs + `,"name":"   "}`},
		{projectsRename.ID, "write", `{"project_id":"` + projectA1 + `","name":"a\u0000b"}`},
		{projectsRename.ID, "write", `{"project_id":"` + projectA1 + `","name":"` + strings.Repeat("a", 251) + `"}`},
		{projectsRename.ID, "write", `{"project_id":"` + projectA1 + `","name":"x","command":"delete-file"}`},
	} {
		_, err := env.invokeConfirmed(test.tool, test.connection, test.args)
		if err == nil {
			t.Errorf("%s %s: accepted", test.tool, test.args)
			continue
		}
		for _, id := range []string{teamB, teamForeign, projectA2, projectOut} {
			if isInvalidRequest(err) && strings.Contains(err.Error(), id) {
				t.Errorf("%s: error names %s", test.tool, id)
			}
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %v, secret reads = %d, want none", commands(calls), *env.reads)
	}

	// Foreign objects that only a read can reveal: no change is sent.
	for _, test := range []struct{ tool, connection, args string }{
		{projectsRename.ID, "write", `{"project_id":"` + projectOut + `","name":"x"}`},
		{projectsDelete.ID, "write", `{"project_id":"` + projectOut + `"}`},
		{filesCreate.ID, "write", `{"project_id":"` + projectOut + `","name":"x"}`},
		{projectsRename.ID, "write", `{"project_id":"` + projectB1 + `","name":"x"}`},
		{filesRename.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `","name":"x"}`},
		{filesRename.ID, "write", `{"project_id":"` + projectOut + `","file_id":"` + fileA1 + `","name":"x"}`},
		{filesMove.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `","target_project_id":"` + projectA2 + `"}`},
		{filesMove.ID, "write", `{` + fileArgs + `,"target_project_id":"` + projectOut + `"}`},
		{filesMove.ID, "write", `{` + fileArgs + `,"target_project_id":"` + projectB1 + `"}`},
		{filesMove.ID, "write", `{"project_id":"` + projectB1 + `","file_id":"` + fileA1 + `","target_project_id":"` + projectA1 + `"}`},
	} {
		calls = nil
		_, err := env.invokeConfirmed(test.tool, test.connection, test.args)
		if !isInvalidRequest(err) {
			t.Errorf("%s %s: err = %v", test.tool, test.args, err)
			continue
		}
		for _, id := range []string{projectOut, fileOut, projectB1} {
			if strings.Contains(err.Error(), id) {
				t.Errorf("%s: error names %s", test.tool, id)
			}
		}
		if len(changes(calls)) != 0 {
			t.Errorf("%s %s: sent %v", test.tool, test.args, commands(calls))
		}
	}
}

func TestManageDeleteAndNeedsToolAllowListAndConfirmation(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, manageHandler(200, ``))
	if _, err := env.invokeConfirmed(projectsDelete.ID, "nodelete", `{"project_id":"`+projectA1+`"}`); err == nil ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("delete without the tool allow-list: err = %v, calls = %v", err, commands(calls))
	}
	for _, test := range []struct{ tool, args string }{
		{projectsCreate.ID, `{"team_id":"` + teamA + `","name":"x"}`},
		{projectsRename.ID, `{"project_id":"` + projectA1 + `","name":"x"}`},
		{projectsDelete.ID, `{"project_id":"` + projectA1 + `"}`},
		{filesCreate.ID, `{"project_id":"` + projectA1 + `","name":"x"}`},
		{filesRename.ID, `{` + fileArgs + `,"name":"x"}`},
		{filesMove.ID, `{` + fileArgs + `,"target_project_id":"` + projectA2 + `"}`},
	} {
		_, err := env.invoke(test.tool, "write", test.args)
		if err == nil || application.ErrorCode(err) != "confirmation-required" || len(calls) != 0 {
			t.Errorf("%s: err = %v, calls = %v", test.tool, err, commands(calls))
		}
	}
	// A read-only connection offers none of them.
	if _, err := env.invokeConfirmed(projectsRename.ID, "one", `{"project_id":"`+projectA1+`","name":"x"}`); err == nil {
		t.Error("a read-only connection offered projects.rename")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %v, secret reads = %d", commands(calls), *env.reads)
	}

	// A project of the allow-list may be deleted, and nothing else is widened.
	result, err := env.invokeConfirmed(projectsDelete.ID, "writenarrow", `{"project_id":"`+projectA1+`"}`)
	if err != nil || !strings.Contains(result, `"deleted":true`) || len(changes(calls)) != 1 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestManageChangesSendOnceAndReportUncertainty(t *testing.T) {
	args := map[string]string{
		projectsCreate.ID: `{"team_id":"` + teamA + `","name":"x"}`,
		projectsRename.ID: `{"project_id":"` + projectA1 + `","name":"x"}`,
		projectsDelete.ID: `{"project_id":"` + projectA1 + `"}`,
		filesCreate.ID:    `{"project_id":"` + projectA1 + `","name":"x"}`,
		filesRename.ID:    `{` + fileArgs + `,"name":"x"}`,
		filesMove.ID:      `{` + fileArgs + `,"target_project_id":"` + projectA2 + `"}`,
	}
	for _, tool := range manageTools {
		for _, test := range []struct {
			status    int
			body      string
			uncertain bool
		}{
			{500, bodyCanary, true}, {502, bodyCanary, true}, {200, `not json`, tool == filesCreate.ID || tool == projectsCreate.ID},
			{403, bodyCanary, false}, {404, bodyCanary, false}, {429, bodyCanary, false},
		} {
			var calls []call
			env := newEnvironment(t, &calls, manageHandler(test.status, test.body))
			_, err := env.invokeConfirmed(tool, "write", args[tool])
			if test.status == 200 && !test.uncertain {
				if err != nil {
					t.Errorf("%s: err = %v", tool, err)
				}
				continue
			}
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "not json") ||
				strings.Contains(err.Error(), tokenValue) {
				t.Errorf("%s status %d: err = %v", tool, test.status, err)
				continue
			}
			if got := strings.Contains(err.Error(), "may have taken effect"); got != test.uncertain {
				t.Errorf("%s status %d: uncertain = %v, err = %v", tool, test.status, got, err)
			}
			if len(changes(calls)) != 1 {
				t.Errorf("%s status %d: sent %d changes, want one", tool, test.status, len(changes(calls)))
			}
		}

		// A transport failure of the change is uncertain and not repeated.
		var calls []call
		env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if isChange(c.command()) {
				return nil, errResetByPeer
			}
			return manageHandler(200, ``)(c)
		})
		_, err := env.invokeConfirmed(tool, "write", args[tool])
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(changes(calls)) != 1 {
			t.Errorf("%s: err = %v, changes = %d", tool, err, len(changes(calls)))
		}
	}
}

func TestManageCreateNeedsAnIdentifierInTheAnswer(t *testing.T) {
	for _, tool := range []string{projectsCreate.ID, filesCreate.ID} {
		var calls []call
		env := newEnvironment(t, &calls, manageHandler(200, `{"name":"x"}`))
		arguments := `{"team_id":"` + teamA + `","name":"x"}`
		if tool == filesCreate.ID {
			arguments = `{"project_id":"` + projectA1 + `","name":"x"}`
		}
		_, err := env.invokeConfirmed(tool, "write", arguments)
		if classOf(err) != "invalid-provider-response" && !strings.Contains(err.Error(), "may have taken effect") {
			t.Errorf("%s: err = %v", tool, err)
		}
	}
}
