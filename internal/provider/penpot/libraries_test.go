package penpot

import (
	"net/http"
	"strings"
	"testing"
)

const (
	libA1      = "00000000-0000-0000-0000-000000000031"
	libA2      = "00000000-0000-0000-0000-000000000032"
	libForeign = "00000000-0000-0000-0000-000000000033"
)

var libraryTools = []string{librariesList.ID, librariesShare.ID, librariesLink.ID}

// libraryHandler adds the library files to the file lists: libA1 in projectA1 and libA2 in projectA2.
func libraryHandler(status int, answer string) func(call) (*http.Response, error) {
	files := filesHandler(0)
	return func(c call) (*http.Response, error) {
		switch c.command() {
		case cmdProjectFile:
			switch c.body["project-id"] {
			case projectA1:
				return jsonResponse(200, `[{"id":"`+fileA1+`","name":"Startseite","projectId":"`+projectA1+`"},`+
					`{"id":"`+libA1+`","name":"Lib","projectId":"`+projectA1+`","isShared":true}]`), nil
			case projectA2:
				return jsonResponse(200, `[{"id":"`+libA2+`","name":"Lib2","projectId":"`+projectA2+`"}]`), nil
			}
		case cmdFileLibraries:
			return jsonResponse(status, answer), nil
		case cmdSetShared, cmdLinkLibrary:
			return jsonResponse(status, answer), nil
		}
		return files(c)
	}
}

func TestLibrariesListFiltersToTheTargets(t *testing.T) {
	var calls []call
	answer := `[{"id":"` + libA1 + `","name":"Lib","projectId":"` + projectA1 + `","teamId":"` + teamA + `","isShared":true,"isIndirect":false},` +
		`{"id":"` + libA2 + `","name":"Lib2","projectId":"` + projectA2 + `","teamId":"` + teamA + `","isIndirect":true},` +
		`{"id":"` + libForeign + `","name":"Fremd","projectId":"` + projectOut + `","teamId":"` + teamForeign + `"}]`
	env := newEnvironment(t, &calls, libraryHandler(200, answer))
	result, err := env.invoke(librariesList.ID, "one", `{`+fileArgs+`}`)
	if err != nil || !strings.Contains(result, libA1) || !strings.Contains(result, `"outside_targets":1`) ||
		strings.Contains(result, libForeign) || strings.Contains(result, "Fremd") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdProjectFile+","+cmdFileLibraries {
		t.Fatalf("commands = %s", got)
	}
	if calls[2].body["file-id"] != fileA1 || len(changes(calls)) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
	// An allow-list narrows the libraries, too.
	calls = nil
	result, err = env.invoke(librariesList.ID, "narrow", `{`+fileArgs+`}`)
	if err != nil || strings.Contains(result, libA2) || !strings.Contains(result, `"outside_targets":2`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	// Foreign file or project: refused before the read.
	calls = nil
	for _, args := range []string{
		`{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `"}`,
		`{"project_id":"` + projectOut + `","file_id":"` + fileA1 + `"}`,
	} {
		if _, err := env.invoke(librariesList.ID, "one", args); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", args, err)
		}
	}
	for _, c := range calls {
		if c.command() == cmdFileLibraries {
			t.Error("libraries read for a foreign file")
		}
	}
}

func TestLibrariesShareAndLinkSendOneFixedCommand(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, libraryHandler(200, `null`))
	result, err := env.invokeConfirmed(librariesShare.ID, "write", `{`+fileArgs+`,"shared":false}`)
	sent := changes(calls)
	if err != nil || !strings.Contains(result, `"shared":false`) || len(sent) != 1 || sent[0].command() != cmdSetShared ||
		sent[0].body["id"] != fileA1 || sent[0].body["is-shared"] != false || len(sent[0].body) != 2 {
		t.Fatalf("result = %s, err = %v, sent = %+v", result, err, sent)
	}
	calls = nil
	args := `{` + fileArgs + `,"library_project_id":"` + projectA2 + `","library_id":"` + libA2 + `"}`
	result, err = env.invokeConfirmed(librariesLink.ID, "write", args)
	sent = changes(calls)
	if err != nil || !strings.Contains(result, `"linked":true`) || len(sent) != 1 || sent[0].command() != cmdLinkLibrary ||
		sent[0].body["file-id"] != fileA1 || sent[0].body["library-id"] != libA2 || len(sent[0].body) != 2 {
		t.Fatalf("result = %s, err = %v, sent = %+v", result, err, sent)
	}
}

func TestLibrariesRefuseForeignTargets(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, libraryHandler(200, `null`))
	for _, test := range []struct{ tool, connection, args string }{
		{librariesShare.ID, "write", `{` + fileArgs + `}`},
		{librariesShare.ID, "write", `{` + fileArgs + `,"shared":"yes"}`},
		{librariesShare.ID, "writenarrow", `{"project_id":"` + projectA2 + `","file_id":"` + libA2 + `","shared":true}`},
		{librariesLink.ID, "writenarrow", `{` + fileArgs + `,"library_project_id":"` + projectA2 + `","library_id":"` + libA2 + `"}`},
		{librariesLink.ID, "write", `{` + fileArgs + `,"library_project_id":"` + projectA1 + `","library_id":"` + fileA1 + `"}`},
		{librariesLink.ID, "write", `{` + fileArgs + `,"library_project_id":"x","library_id":"` + libA1 + `"}`},
		{librariesLink.ID, "write", `{` + fileArgs + `,"library_project_id":"` + projectA1 + `","library_id":"` + libA1 + `","command":"x"}`},
	} {
		if _, err := env.invokeConfirmed(test.tool, test.connection, test.args); err == nil {
			t.Errorf("%s %s: accepted", test.tool, test.args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %v, secret reads = %d, want none", commands(calls), *env.reads)
	}
	for _, test := range []struct{ tool, args string }{
		{librariesShare.ID, `{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `","shared":true}`},
		{librariesShare.ID, `{"project_id":"` + projectOut + `","file_id":"` + fileA1 + `","shared":true}`},
		{librariesLink.ID, `{` + fileArgs + `,"library_project_id":"` + projectOut + `","library_id":"` + libForeign + `"}`},
		{librariesLink.ID, `{` + fileArgs + `,"library_project_id":"` + projectB1 + `","library_id":"` + libForeign + `"}`},
		{librariesLink.ID, `{` + fileArgs + `,"library_project_id":"` + projectA2 + `","library_id":"` + libForeign + `"}`},
		{librariesLink.ID, `{"project_id":"` + projectA1 + `","file_id":"` + fileOut + `","library_project_id":"` + projectA2 + `","library_id":"` + libA2 + `"}`},
	} {
		calls = nil
		_, err := env.invokeConfirmed(test.tool, "write", test.args)
		if !isInvalidRequest(err) || len(changes(calls)) != 0 {
			t.Errorf("%s %s: err = %v, sent %v", test.tool, test.args, err, commands(calls))
			continue
		}
		for _, id := range []string{projectOut, fileOut, projectB1, libForeign} {
			if strings.Contains(err.Error(), id) {
				t.Errorf("%s: error names %s", test.tool, id)
			}
		}
	}
	// Confirmation is required.
	calls = nil
	for _, test := range []struct{ tool, args string }{
		{librariesShare.ID, `{` + fileArgs + `,"shared":true}`},
		{librariesLink.ID, `{` + fileArgs + `,"library_project_id":"` + projectA2 + `","library_id":"` + libA2 + `"}`},
	} {
		if _, err := env.invoke(test.tool, "write", test.args); err == nil || len(calls) != 0 {
			t.Errorf("%s: err = %v", test.tool, err)
		}
	}
}

func TestLibraryChangesSendOnceAndReportUncertainty(t *testing.T) {
	args := map[string]string{
		librariesShare.ID: `{` + fileArgs + `,"shared":true}`,
		librariesLink.ID:  `{` + fileArgs + `,"library_project_id":"` + projectA2 + `","library_id":"` + libA2 + `"}`,
	}
	for tool, arguments := range args {
		for _, test := range []struct {
			status    int
			uncertain bool
		}{{500, true}, {502, true}, {403, false}, {404, false}, {429, false}} {
			var calls []call
			env := newEnvironment(t, &calls, libraryHandler(test.status, bodyCanary))
			_, err := env.invokeConfirmed(tool, "write", arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) ||
				strings.Contains(err.Error(), "may have taken effect") != test.uncertain || len(changes(calls)) != 1 {
				t.Errorf("%s status %d: err = %v, changes = %d", tool, test.status, err, len(changes(calls)))
			}
		}
		var calls []call
		env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if isChange(c.command()) {
				return nil, errResetByPeer
			}
			return libraryHandler(200, `null`)(c)
		})
		_, err := env.invokeConfirmed(tool, "write", arguments)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(changes(calls)) != 1 {
			t.Errorf("%s: err = %v, changes = %d", tool, err, len(changes(calls)))
		}
	}
}
