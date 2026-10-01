package penpot

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const summaryBody = `{"name":"Startseite","components":{"count":2,"sample":[{"id":"` + fileOut + `","name":"Button"}]},` +
	`"variants":{"count":0,"sample":[]},"colors":{"count":1,"sample":[{"id":"` + pageID + `","name":"Brand"}]},` +
	`"typographies":{"count":0}}`

func pageBody(n int) string {
	var b strings.Builder
	b.WriteString(`{"id":"` + pageID + `","name":"Seite 1","objects":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"00000000-0000-0000-1000-%012d":{"id":"x","type":"rect","name":"R%d","parent-id":"%s",`+
			`"frame-id":"%s","x":1.5,"y":2,"width":10,"height":20,"fills":[{"fill-image":{"id":"http://media.invalid/x"}}],`+
			`"content":{"text":"geheim"},"metadata":"data:image/png;base64,AAAA"}`, i, i, projectA1, projectA1)
	}
	b.WriteString(`}}`)
	return b.String()
}

func filesHandler(pages int) func(call) (*http.Response, error) {
	return func(c call) (*http.Response, error) {
		switch c.command() {
		case cmdProjects:
			return projectsHandler(c)
		case cmdProjectFile:
			if c.body["project-id"] == projectA1 {
				return jsonResponse(200, `[{"id":"`+fileA1+`","name":"Startseite","projectId":"`+projectA1+`","revn":7,"isShared":true,`+
					`"modifiedAt":"2026-09-02T08:00:00Z"},{"id":"`+fileOut+`","name":"Woanders","projectId":"`+projectA2+`"}]`), nil
			}
			return jsonResponse(200, `[]`), nil
		case cmdSummary:
			return jsonResponse(200, summaryBody), nil
		case cmdPage:
			return jsonResponse(200, pageBody(pages)), nil
		}
		return jsonResponse(404, bodyCanary), nil
	}
}

func TestFilesListProvesProjectThenListsItsFiles(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(0))
	result, err := env.invoke(filesList.ID, "one", `{"project_id":"`+projectA1+`"}`)
	if err != nil || !strings.Contains(result, `"Startseite"`) || strings.Contains(result, "Woanders") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdProjectFile {
		t.Fatalf("commands = %s", got)
	}
}

func TestFilesRefuseProjectsOutsideTheTargets(t *testing.T) {
	// Allow-list violation: refused before the secret and any request.
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(0))
	for _, tool := range []string{filesList.ID, filesGet.ID} {
		arguments := `{"project_id":"` + projectA2 + `","file_id":"` + fileA1 + `"}`
		if tool == filesList.ID {
			arguments = `{"project_id":"` + projectA2 + `"}`
		}
		_, err := env.invoke(tool, "narrow", arguments)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), projectA2) {
			t.Errorf("%s: err = %v", tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d, want none", len(calls), *env.reads)
	}

	// A project of a foreign team passes the local check; the proof request finds it in no bound team and
	// nothing beyond the project lists is read.
	calls = nil
	for _, tool := range []string{filesList.ID, filesGet.ID} {
		arguments := `{"project_id":"` + projectOut + `","file_id":"` + fileA1 + `"}`
		if tool == filesList.ID {
			arguments = `{"project_id":"` + projectOut + `"}`
		}
		_, err := env.invoke(tool, "two", arguments)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), projectOut) {
			t.Errorf("%s: err = %v", tool, err)
		}
	}
	for _, c := range calls {
		if c.command() != cmdProjects {
			t.Errorf("unexpected command %s for a foreign project", c.command())
		}
	}
}

func TestFilesGetReadsSummaryAndBoundedPage(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(maxShapes+50))
	result, err := env.invoke(filesGet.ID, "one",
		`{"project_id":"`+projectA1+`","file_id":"`+fileA1+`","page_id":"`+pageID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"Startseite"`, `"count":2`, `"Button"`, `"shape_count":250`, `"truncated":true`, `"type":"rect"`} {
		if !strings.Contains(result, want) {
			t.Errorf("result lacks %s: %.400s", want, result)
		}
	}
	for _, absent := range []string{"media.invalid", "geheim", "base64", "fills", "content"} {
		if strings.Contains(result, absent) {
			t.Errorf("result contains %q: %.400s", absent, result)
		}
	}
	if n := strings.Count(result, `"type":"rect"`); n != maxShapes {
		t.Errorf("shapes = %d, want %d", n, maxShapes)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdProjectFile+","+cmdSummary+","+cmdPage {
		t.Fatalf("commands = %s", got)
	}
	if last := calls[len(calls)-1]; last.body["file-id"] != fileA1 || last.body["page-id"] != pageID {
		t.Errorf("get-page body = %+v", last.body)
	}
	if calls[2].body["id"] != fileA1 {
		t.Errorf("get-file-summary body = %+v", calls[2].body)
	}
}

func TestFilesGetFirstPageWhenNoPageGiven(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(1))
	if _, err := env.invoke(filesGet.ID, "one", `{"project_id":"`+projectA1+`","file_id":"`+fileA1+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, has := calls[len(calls)-1].body["page-id"]; has {
		t.Errorf("page-id sent without being asked: %+v", calls[len(calls)-1].body)
	}
}

func TestFilesGetBindsFileToItsProject(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(1))
	// fileOut exists, but belongs to another project: it is not in the list of projectA1.
	_, err := env.invoke(filesGet.ID, "one", `{"project_id":"`+projectA1+`","file_id":"`+fileOut+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), fileOut) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range calls {
		if c.command() == cmdSummary || c.command() == cmdPage {
			t.Errorf("content of a foreign file was requested: %s", c.command())
		}
	}
	if len(calls) != 2 {
		t.Errorf("proof requests = %v, want projects and file list", commands(calls))
	}
}

func TestFilesGetRejectsMalformedIDsBeforeIO(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, filesHandler(1))
	for _, arguments := range []string{
		`{"project_id":"` + projectA1 + `","file_id":"../x"}`,
		`{"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `","page_id":"nope"}`,
	} {
		if _, err := env.invoke(filesGet.ID, "one", arguments); err == nil {
			t.Errorf("%s must be refused", arguments)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d", len(calls), *env.reads)
	}
}

func TestFilesListBoundsCount(t *testing.T) {
	var calls []call
	var body strings.Builder
	body.WriteString(`[`)
	for i := 0; i < maxFilesListed+5; i++ {
		if i > 0 {
			body.WriteString(",")
		}
		body.WriteString(`{"id":"` + fileA1 + `","name":"x"}`)
	}
	body.WriteString(`]`)
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.command() == cmdProjects {
			return projectsHandler(c)
		}
		return jsonResponse(200, body.String()), nil
	})
	result, err := env.invoke(filesList.ID, "one", `{"project_id":"`+projectA1+`"}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) {
		t.Fatalf("result = %.200s, err = %v", result, err)
	}
}
