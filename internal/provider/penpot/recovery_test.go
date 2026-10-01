package penpot

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

const (
	snapshotA  = "00000000-0000-0000-0000-000000000051"
	snapshotNo = "00000000-0000-0000-0000-000000000052"
	deletedA1  = "00000000-0000-0000-0000-000000000061"
	deletedA2  = "00000000-0000-0000-0000-000000000062"
	deletedB1  = "00000000-0000-0000-0000-000000000063"
	deletedOut = "00000000-0000-0000-0000-000000000064"
)

var recoveryTools = []string{snapshotsCreate.ID, snapshotsRestore.ID, filesDelete.ID, filesRestore.ID, filesPurge.ID}

func sseResponse(status int, body string) *http.Response {
	response := jsonResponse(status, body)
	response.Header = http.Header{"Content-Type": {"text/event-stream;charset=UTF-8"}}
	return response
}

func sseEnd(ids ...string) string {
	return "event: progress\ndata: {\"index\":1}\n\nevent: end\ndata: [\"" + strings.Join(ids, `","`) + "\"]\n\n"
}

// recoveryHandler serves the lists of filesHandler, the snapshots and deleted files, and answers the changes.
func recoveryHandler(change func(call) *http.Response) func(call) (*http.Response, error) {
	files := filesHandler(0)
	return func(c call) (*http.Response, error) {
		switch c.command() {
		case cmdSnapshots:
			if c.body["file-id"] != fileA1 {
				return jsonResponse(200, `[]`), nil
			}
			return jsonResponse(200, `[{"id":"`+snapshotA+`","label":"Vorher","revn":3,"created-by":"user"}]`), nil
		case cmdDeletedFiles:
			switch c.body["team-id"] {
			case teamA:
				return jsonResponse(200, `[{"id":"`+deletedA1+`","name":"Alt","project-id":"`+projectA1+`","team-id":"`+teamA+`"},`+
					`{"id":"`+deletedA2+`","name":"Alt 2","project-id":"`+projectA2+`","team-id":"`+teamA+`"},`+
					`{"id":"`+deletedOut+`","name":"Fremd","project-id":"`+projectOut+`","team-id":"`+teamForeign+`"}]`), nil
			case teamB:
				return jsonResponse(200, `[{"id":"`+deletedB1+`","project-id":"`+projectB1+`","team-id":"`+teamB+`"}]`), nil
			}
			return jsonResponse(404, bodyCanary), nil
		}
		if isChange(c.command()) {
			return change(c), nil
		}
		return files(c)
	}
}

func recoveryAnswers(c call) *http.Response {
	switch c.command() {
	case cmdCreateSnapshot:
		return jsonResponse(200, `{"id":"`+snapshotA+`","file-id":"`+fileA1+`","label":"Vorher","revn":3}`)
	case cmdRestoreFiles, cmdPurgeFiles:
		ids, _ := c.body["ids"].([]any)
		return sseResponse(200, sseEnd(ids[0].(string)))
	}
	return jsonResponse(200, ``)
}

func recoveryArgs(tool string) string {
	switch tool {
	case snapshotsCreate.ID:
		return `{` + fileArgs + `,"label":"Vorher"}`
	case snapshotsRestore.ID:
		return `{` + fileArgs + `,"snapshot_id":"` + snapshotA + `"}`
	case filesDelete.ID:
		return `{` + fileArgs + `}`
	}
	return `{"project_id":"` + projectA1 + `","file_id":"` + deletedA1 + `"}`
}

func TestRecoverySendsOneFixedCommand(t *testing.T) {
	for _, test := range []struct {
		tool, command, want string
		body                map[string]any
		reads               []string
	}{
		{snapshotsCreate.ID, cmdCreateSnapshot, snapshotA, map[string]any{"file-id": fileA1, "label": "Vorher"},
			[]string{cmdProjects, cmdProjectFile}},
		{snapshotsRestore.ID, cmdRestoreSnapshot, `"restored":true`, map[string]any{"file-id": fileA1, "id": snapshotA},
			[]string{cmdProjects, cmdProjectFile, cmdSnapshots}},
		{filesDelete.ID, cmdDeleteFile, `"deleted":true`, map[string]any{"id": fileA1},
			[]string{cmdProjects, cmdProjectFile}},
		{filesRestore.ID, cmdRestoreFiles, `"restored":true`, map[string]any{"team-id": teamA, "ids": []any{deletedA1}},
			[]string{cmdDeletedFiles}},
		{filesPurge.ID, cmdPurgeFiles, `"purged":true`, map[string]any{"team-id": teamA, "ids": []any{deletedA1}},
			[]string{cmdDeletedFiles}},
	} {
		var calls []call
		env := newEnvironment(t, &calls, recoveryHandler(recoveryAnswers))
		result, err := env.invokeConfirmed(test.tool, "write", recoveryArgs(test.tool))
		if err != nil || !strings.Contains(result, test.want) || strings.Contains(result, "Vorher") {
			t.Errorf("%s: result = %s, err = %v", test.tool, result, err)
			continue
		}
		sent := changes(calls)
		if len(sent) != 1 || sent[0].command() != test.command || sent[0].method != http.MethodPost ||
			len(sent[0].body) != len(test.body) {
			t.Errorf("%s: sent = %+v", test.tool, sent)
			continue
		}
		for key, want := range test.body {
			got := sent[0].body[key]
			if list, ok := want.([]any); ok {
				gotList, _ := got.([]any)
				if len(gotList) != 1 || gotList[0] != list[0] {
					t.Errorf("%s: body[%s] = %v", test.tool, key, got)
				}
			} else if got != want {
				t.Errorf("%s: body[%s] = %v, want %v", test.tool, key, got, want)
			}
		}
		if got, want := strings.Join(commands(calls), ","), strings.Join(append(test.reads, test.command), ","); got != want {
			t.Errorf("%s: commands = %s, want %s", test.tool, got, want)
		}
	}
}

func TestRecoveryBindsDeletedFilesThroughTeamAndProject(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recoveryHandler(recoveryAnswers))

	// A file of the second bound team is sent with the team of its binding.
	result, err := env.invokeConfirmed(filesPurge.ID, "writetwo", `{"project_id":"`+projectB1+`","file_id":"`+deletedB1+`"}`)
	sent := changes(calls)
	if err != nil || !strings.Contains(result, `"purged":true`) || len(sent) != 1 || sent[0].body["team-id"] != teamB {
		t.Fatalf("result = %s, err = %v, sent = %+v", result, err, sent)
	}

	// Local refusals: before the secret and any request, never naming the ID.
	calls = nil
	reads := *env.reads
	for _, test := range []struct{ tool, connection, args string }{
		{filesPurge.ID, "writenarrow", `{"project_id":"` + projectA2 + `","file_id":"` + deletedA2 + `"}`},
		{filesRestore.ID, "writenarrow", `{"project_id":"` + projectA2 + `","file_id":"` + deletedA2 + `"}`},
		{filesDelete.ID, "writenarrow", `{"project_id":"` + projectA2 + `","file_id":"` + fileA1 + `"}`},
		{snapshotsCreate.ID, "writenarrow", `{"project_id":"` + projectA2 + `","file_id":"` + fileA1 + `"}`},
		{snapshotsRestore.ID, "writenarrow", `{"project_id":"` + projectA2 + `","file_id":"` + fileA1 + `","snapshot_id":"` + snapshotA + `"}`},
		{filesPurge.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"x"}`},
		{filesRestore.ID, "write", `{"project_id":"x","file_id":"` + deletedA1 + `"}`},
		{snapshotsRestore.ID, "write", `{` + fileArgs + `,"snapshot_id":"x"}`},
		{snapshotsCreate.ID, "write", `{` + fileArgs + `,"label":"   "}`},
		{filesPurge.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + deletedA1 + `","team_id":"` + teamB + `"}`},
		{filesRestore.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + deletedA1 + `","command":"x"}`},
		{filesPurge.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + deletedA1 + `","ids":["` + deletedA2 + `"]}`},
	} {
		_, err := env.invokeConfirmed(test.tool, test.connection, test.args)
		if err == nil {
			t.Errorf("%s %s: accepted", test.tool, test.args)
			continue
		}
		if isInvalidRequest(err) && (strings.Contains(err.Error(), projectA2) || strings.Contains(err.Error(), deletedA2)) {
			t.Errorf("%s: error names a foreign ID", test.tool)
		}
	}
	if len(calls) != 0 || *env.reads != reads {
		t.Fatalf("calls = %v, secret reads = %d, want none", commands(calls), *env.reads-reads)
	}

	// Objects that only a read can refuse: no change is sent, and nothing is named.
	for _, test := range []struct{ tool, connection, args string }{
		// not deleted (an active file) or unknown
		{filesPurge.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `"}`},
		{filesRestore.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `"}`},
		// another project of the same team
		{filesPurge.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + deletedA2 + `"}`},
		// a foreign team, and a team that is not bound
		{filesPurge.ID, "write", `{"project_id":"` + projectOut + `","file_id":"` + deletedOut + `"}`},
		{filesRestore.ID, "write", `{"project_id":"` + projectB1 + `","file_id":"` + deletedB1 + `"}`},
		// snapshots
		{snapshotsRestore.ID, "write", `{` + fileArgs + `,"snapshot_id":"` + snapshotNo + `"}`},
		// files outside the project or the targets
		{snapshotsCreate.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `"}`},
		{filesDelete.ID, "write", `{"project_id":"` + projectOut + `","file_id":"` + fileA1 + `"}`},
		{filesDelete.ID, "write", `{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `"}`},
	} {
		calls = nil
		_, err := env.invokeConfirmed(test.tool, test.connection, test.args)
		if !isInvalidRequest(err) {
			t.Errorf("%s %s: err = %v", test.tool, test.args, err)
			continue
		}
		for _, id := range []string{projectOut, fileOut, deletedOut, deletedA2, deletedB1, snapshotNo, teamForeign} {
			if strings.Contains(err.Error(), id) {
				t.Errorf("%s: error names %s", test.tool, id)
			}
		}
		if len(changes(calls)) != 0 {
			t.Errorf("%s %s: sent %v", test.tool, test.args, commands(calls))
		}
	}
}

func TestRecoveryNeedsConfirmationAndToolAllowList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recoveryHandler(recoveryAnswers))
	for _, tool := range recoveryTools {
		if _, err := env.invoke(tool, "write", recoveryArgs(tool)); application.ErrorCode(err) != "confirmation-required" {
			t.Errorf("%s: err = %v", tool, err)
		}
		// Neither a read-only connection nor one whose tools do not name the tool offers it.
		for _, connection := range []string{"one", "nodelete"} {
			if _, err := env.invokeConfirmed(tool, connection, recoveryArgs(tool)); err == nil {
				t.Errorf("%s: offered on %s", tool, connection)
			}
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %v, secret reads = %d, want none", commands(calls), *env.reads)
	}
}

func TestRecoveryChangesSendOnceAndReportUncertainty(t *testing.T) {
	for _, tool := range recoveryTools {
		for _, test := range []struct {
			name      string
			response  *http.Response
			fails     bool
			uncertain bool
		}{
			{"500", jsonResponse(500, bodyCanary), true, true},
			{"502", jsonResponse(502, bodyCanary), true, true},
			{"403", jsonResponse(403, bodyCanary), true, false},
			{"404", jsonResponse(404, bodyCanary), true, false},
			{"429", jsonResponse(429, bodyCanary), true, false},
			{"unreadable", sseResponse(200, "not an event stream"), tool == filesRestore.ID || tool == filesPurge.ID, true},
			{"cut stream", sseResponse(200, "event: progress\ndata: {}\n\n"), tool == filesRestore.ID || tool == filesPurge.ID, true},
			{"error event", sseResponse(200, "event: error\ndata: {\"hint\":\""+bodyCanary+"\"}\n\n"),
				tool == filesRestore.ID || tool == filesPurge.ID, false},
			{"nothing done", sseResponse(200, sseEnd()), tool == filesRestore.ID || tool == filesPurge.ID, false},
			{"other file", sseResponse(200, sseEnd(deletedA2)), tool == filesRestore.ID || tool == filesPurge.ID, false},
		} {
			var calls []call
			env := newEnvironment(t, &calls, recoveryHandler(func(call) *http.Response { return test.response }))
			_, err := env.invokeConfirmed(tool, "write", recoveryArgs(tool))
			if tool == snapshotsCreate.ID && test.response.StatusCode == 200 {
				// The answer must carry the snapshot identifier, else the result is open.
				if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
					t.Errorf("%s %s: err = %v", tool, test.name, err)
				}
				continue
			}
			if !test.fails {
				if err != nil {
					t.Errorf("%s %s: err = %v", tool, test.name, err)
				}
				continue
			}
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), tokenValue) {
				t.Errorf("%s %s: err = %v", tool, test.name, err)
				continue
			}
			if got := strings.Contains(err.Error(), "may have taken effect"); got != test.uncertain {
				t.Errorf("%s %s: uncertain = %v, err = %v", tool, test.name, got, err)
			}
			if len(changes(calls)) != 1 {
				t.Errorf("%s %s: sent %d changes, want one", tool, test.name, len(changes(calls)))
			}
		}

		var calls []call
		env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if isChange(c.command()) {
				return nil, errResetByPeer
			}
			return recoveryHandler(recoveryAnswers)(c)
		})
		_, err := env.invokeConfirmed(tool, "write", recoveryArgs(tool))
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(changes(calls)) != 1 {
			t.Errorf("%s: err = %v, changes = %d", tool, err, len(changes(calls)))
		}
	}
}
