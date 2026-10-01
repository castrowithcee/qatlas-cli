package penpot

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

const (
	threadA   = "00000000-0000-0000-0000-000000000031"
	threadOut = "00000000-0000-0000-0000-000000000032"
	commentA  = "00000000-0000-0000-0000-000000000041"
	commentB  = "00000000-0000-0000-0000-000000000042"
	commentX  = "00000000-0000-0000-0000-000000000043"
	frameA    = "00000000-0000-0000-0000-000000000051"
	newThread = "00000000-0000-0000-0000-000000000061"
	newNote   = "00000000-0000-0000-0000-000000000062"
	ownerA    = "00000000-0000-0000-0000-000000000071"
	personal  = "personal-canary@example.test"
)

// commentsHandler serves the project and file lists of filesHandler, one thread and two comments of fileA1,
// and answers every change command with the given status and body.
func commentsHandler(status int, answer string) func(call) (*http.Response, error) {
	files := filesHandler(0)
	return func(c call) (*http.Response, error) {
		switch c.command() {
		case cmdThreads:
			if c.body["file-id"] != fileA1 {
				return jsonResponse(200, `[]`), nil
			}
			return jsonResponse(200, `[{"id":"`+threadA+`","fileId":"`+fileA1+`","pageId":"`+pageID+`","pageName":"Seite 1",`+
				`"frameId":"`+frameA+`","ownerId":"`+ownerA+`","ownerEmail":"`+personal+`","ownerFullname":"Erika Mustermann",`+
				`"position":{"x":1.5,"y":2},"isResolved":false,"countComments":2,"seqn":3,"content":"Erster Text",`+
				`"createdAt":"2026-09-03T08:00:00Z"},`+
				`{"id":"`+threadOut+`","fileId":"`+fileOut+`","content":"fremd"}]`), nil
		case cmdComments:
			if c.body["thread-id"] != threadA {
				return jsonResponse(200, `[]`), nil
			}
			return jsonResponse(200, `[{"id":"`+commentA+`","threadId":"`+threadA+`","fileId":"`+fileA1+`","ownerId":"`+ownerA+`",`+
				`"ownerEmail":"`+personal+`","content":"Erster Text","createdAt":"2026-09-03T08:00:00Z"},`+
				`{"id":"`+commentB+`","threadId":"`+threadA+`","content":"Zweiter Text"},`+
				`{"id":"`+commentX+`","threadId":"`+threadOut+`","content":"fremd"}]`), nil
		case cmdPage:
			return jsonResponse(200, `{"id":"`+pageID+`","name":"Seite 1","objects":{"`+frameA+`":{"type":"frame"}}}`), nil
		case cmdCreateThread, cmdCreateComment, cmdUpdateThread, cmdUpdateComment, cmdDeleteThread, cmdDeleteComment:
			return jsonResponse(status, answer), nil
		}
		return files(c)
	}
}

func (e *environment) invokeConfirmed(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: []byte(arguments), Confirmed: true,
	})
	return string(response.Result), err
}

func changes(calls []call) []call {
	var out []call
	for _, c := range calls {
		if isChange(c.command()) {
			out = append(out, c)
		}
	}
	return out
}

const fileArgs = `"project_id":"` + projectA1 + `","file_id":"` + fileA1 + `"`

func TestCommentsThreadsAndListAreBoundAndMinimized(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, commentsHandler(200, `null`))
	result, err := env.invoke(commentsThreads.ID, "one", `{`+fileArgs+`}`)
	if err != nil || !strings.Contains(result, `"Erster Text"`) || strings.Contains(result, "fremd") ||
		strings.Contains(result, personal) || strings.Contains(result, "Mustermann") || !strings.Contains(result, `"x":1.5`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdProjectFile+","+cmdThreads {
		t.Fatalf("commands = %s", got)
	}

	calls = nil
	result, err = env.invoke(commentsList.ID, "one", `{`+fileArgs+`,"thread_id":"`+threadA+`"}`)
	if err != nil || !strings.Contains(result, "Zweiter Text") || strings.Contains(result, "fremd") ||
		strings.Contains(result, personal) || !strings.Contains(result, `"count":2`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdProjectFile+","+cmdThreads+","+cmdComments {
		t.Fatalf("commands = %s", got)
	}
}

func TestCommentsRefuseForeignTargetsBeforeMutationAndSecret(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, commentsHandler(200, `null`))
	// Local refusals: project outside the allow-list and malformed identifiers, before secret and request.
	for _, tool := range []string{commentsThreads.ID, commentsList.ID, commentsCreate.ID, commentsUpdate.ID, commentsDelete.ID} {
		_, err := env.invokeConfirmed(tool, "writenarrow", `{"project_id":"`+projectA2+`","file_id":"`+fileA1+`","thread_id":"`+
			threadA+`","content":"x","is_resolved":true}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), projectA2) {
			t.Errorf("%s: err = %v", tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d, want none", len(calls), *env.reads)
	}

	// A foreign file, thread, or comment is refused after reads only, and no change is sent or named.
	for _, test := range []struct{ tool, args string }{
		{commentsList.ID, `"file_id":"` + fileOut + `","thread_id":"` + threadA + `"`},
		{commentsList.ID, `"file_id":"` + fileA1 + `","thread_id":"` + threadOut + `"`},
		{commentsUpdate.ID, `"file_id":"` + fileOut + `","thread_id":"` + threadA + `","is_resolved":true`},
		{commentsUpdate.ID, `"file_id":"` + fileA1 + `","thread_id":"` + threadOut + `","is_resolved":true`},
		{commentsUpdate.ID, `"file_id":"` + fileA1 + `","thread_id":"` + threadA + `","comment_id":"` + commentX + `","content":"x"`},
		{commentsDelete.ID, `"file_id":"` + fileA1 + `","thread_id":"` + threadOut + `"`},
		{commentsDelete.ID, `"file_id":"` + fileA1 + `","thread_id":"` + threadA + `","comment_id":"` + commentX + `"`},
		{commentsCreate.ID, `"file_id":"` + fileA1 + `","thread_id":"` + threadOut + `","content":"x"`},
		{commentsCreate.ID, `"file_id":"` + fileA1 + `","page_id":"` + pageID + `","frame_id":"` + commentX +
			`","position":{"x":1,"y":2},"content":"x"`},
	} {
		calls = nil
		_, err := env.invokeConfirmed(test.tool, "write", `{"project_id":"`+projectA1+`",`+test.args+`}`)
		if !isInvalidRequest(err) {
			t.Errorf("%s %s: err = %v", test.tool, test.args, err)
			continue
		}
		for _, id := range []string{fileOut, threadOut, commentX} {
			if strings.Contains(err.Error(), id) {
				t.Errorf("%s: error names %s", test.tool, id)
			}
		}
		if len(changes(calls)) != 0 {
			t.Errorf("%s %s: sent %v", test.tool, test.args, commands(calls))
		}
	}
}

func TestCommentsCreateChoosesTheFixedCommandByArgumentForm(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, commentsHandler(200, `{"id":"`+newThread+`","commentId":"`+newNote+`"}`))
	result, err := env.invokeConfirmed(commentsCreate.ID, "write", `{`+fileArgs+`,"page_id":"`+pageID+`","frame_id":"`+frameA+
		`","position":{"x":10,"y":20.5},"content":"Bitte pruefen"}`)
	if err != nil || !strings.Contains(result, newThread) || !strings.Contains(result, newNote) || strings.Contains(result, "Bitte") {
		t.Fatalf("thread: %s, %v", result, err)
	}
	sent := changes(calls)
	if len(sent) != 1 || sent[0].command() != cmdCreateThread || sent[0].method != "POST" ||
		sent[0].body["file-id"] != fileA1 || sent[0].body["content"] != "Bitte pruefen" || sent[0].body["frame-id"] != frameA {
		t.Fatalf("sent = %+v", sent)
	}
	if got := strings.Join(commands(calls), ","); got != cmdProjects+","+cmdProjectFile+","+cmdPage+","+cmdCreateThread {
		t.Fatalf("commands = %s", got)
	}

	calls = nil
	env = newEnvironment(t, &calls, commentsHandler(200, `{"id":"`+newNote+`","threadId":"`+threadA+`"}`))
	result, err = env.invokeConfirmed(commentsCreate.ID, "write", `{`+fileArgs+`,"thread_id":"`+threadA+`","content":"Antwort"}`)
	if err != nil || !strings.Contains(result, newNote) || !strings.Contains(result, threadA) {
		t.Fatalf("comment: %s, %v", result, err)
	}
	sent = changes(calls)
	if len(sent) != 1 || sent[0].command() != cmdCreateComment || sent[0].body["thread-id"] != threadA {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestCommentsCreateRejectsBadForms(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, commentsHandler(200, `{}`))
	long := strings.Repeat("a", 751)
	for _, args := range []string{
		`"content":"x"`, // neither form
		`"page_id":"` + pageID + `","frame_id":"` + frameA + `","content":"x"`,   // no position
		`"page_id":"` + pageID + `","position":{"x":1,"y":2},"content":"x"`,      // no frame
		`"thread_id":"` + threadA + `","page_id":"` + pageID + `","content":"x"`, // both forms
		`"thread_id":"` + threadA + `","position":{"x":1,"y":2},"content":"x"`,   // both forms
		`"thread_id":"` + threadA + `","content":"   "`,                          // blank
		`"thread_id":"` + threadA + `","content":"` + long + `"`,                 // too long
		`"thread_id":"` + threadA + `","content":"a\u0000b"`,                     // control
		`"thread_id":"not-a-uuid","content":"x"`,                                 // bad id
		`"thread_id":"` + threadA + `","content":"x","command":"delete-file"`,    // passthrough
	} {
		_, err := env.invokeConfirmed(commentsCreate.ID, "write", `{`+fileArgs+`,`+args+`}`)
		if err == nil {
			t.Errorf("accepted %s", args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %v, secret reads = %d, want none", commands(calls), *env.reads)
	}
}

func TestCommentsCreateNeedsConfirmation(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, commentsHandler(200, `{}`))
	_, err := env.invoke(commentsCreate.ID, "write", `{`+fileArgs+`,"thread_id":"`+threadA+`","content":"x"}`)
	if err == nil || application.ErrorCode(err) != "confirmation-required" || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %v", err, commands(calls))
	}
}

func TestCommentsUpdateChoosesTheFixedCommandByArgumentForm(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, commentsHandler(204, ``))
	result, err := env.invokeConfirmed(commentsUpdate.ID, "write", `{`+fileArgs+`,"thread_id":"`+threadA+`","is_resolved":true}`)
	if err != nil || !strings.Contains(result, `"updated":true`) {
		t.Fatalf("resolve: %s, %v", result, err)
	}
	sent := changes(calls)
	if len(sent) != 1 || sent[0].command() != cmdUpdateThread || sent[0].body["id"] != threadA || sent[0].body["is-resolved"] != true {
		t.Fatalf("sent = %+v", sent)
	}

	calls = nil
	result, err = env.invokeConfirmed(commentsUpdate.ID, "write", `{`+fileArgs+`,"thread_id":"`+threadA+`","comment_id":"`+commentA+`","content":"Neu"}`)
	if err != nil || !strings.Contains(result, commentA) || strings.Contains(result, "Neu") {
		t.Fatalf("edit: %s, %v", result, err)
	}
	sent = changes(calls)
	if len(sent) != 1 || sent[0].command() != cmdUpdateComment || sent[0].body["id"] != commentA || sent[0].body["content"] != "Neu" {
		t.Fatalf("sent = %+v", sent)
	}

	calls = nil
	for _, args := range []string{
		`"thread_id":"` + threadA + `"`,
		`"thread_id":"` + threadA + `","is_resolved":true,"content":"x"`,
		`"thread_id":"` + threadA + `","comment_id":"` + commentA + `","is_resolved":true,"content":"x"`,
		`"thread_id":"` + threadA + `","comment_id":"` + commentA + `"`,
	} {
		if _, err := env.invokeConfirmed(commentsUpdate.ID, "write", `{`+fileArgs+`,`+args+`}`); err == nil {
			t.Errorf("accepted %s", args)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v", commands(calls))
	}
}

func TestCommentsDeleteNeedsAllowListAndChoosesCommandByForm(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, commentsHandler(200, `null`))
	_, err := env.invokeConfirmed(commentsDelete.ID, "nodelete", `{`+fileArgs+`,"thread_id":"`+threadA+`"}`)
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("delete without the tool allow-list: err = %v, calls = %v", err, commands(calls))
	}

	result, err := env.invokeConfirmed(commentsDelete.ID, "write", `{`+fileArgs+`,"thread_id":"`+threadA+`"}`)
	if err != nil || !strings.Contains(result, `"deleted":true`) {
		t.Fatalf("thread: %s, %v", result, err)
	}
	sent := changes(calls)
	if len(sent) != 1 || sent[0].command() != cmdDeleteThread || sent[0].body["id"] != threadA {
		t.Fatalf("sent = %+v", sent)
	}

	calls = nil
	if _, err = env.invokeConfirmed(commentsDelete.ID, "write", `{`+fileArgs+`,"thread_id":"`+threadA+`","comment_id":"`+commentB+`"}`); err != nil {
		t.Fatal(err)
	}
	sent = changes(calls)
	if len(sent) != 1 || sent[0].command() != cmdDeleteComment || sent[0].body["id"] != commentB {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestCommentsChangesSendOnceAndReportUncertainty(t *testing.T) {
	for _, test := range []struct {
		status    int
		body      string
		uncertain bool
	}{
		{500, bodyCanary, true},
		{502, bodyCanary, true},
		{200, `not json`, true},
		{403, bodyCanary, false},
		{404, bodyCanary, false},
		{429, bodyCanary, false},
	} {
		var calls []call
		env := newEnvironment(t, &calls, commentsHandler(test.status, test.body))
		_, err := env.invokeConfirmed(commentsCreate.ID, "write", `{`+fileArgs+`,"thread_id":"`+threadA+`","content":"x"}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "not json") ||
			strings.Contains(err.Error(), tokenValue) {
			t.Errorf("status %d: err = %v", test.status, err)
			continue
		}
		if got := strings.Contains(err.Error(), "may have taken effect"); got != test.uncertain {
			t.Errorf("status %d: uncertain = %v, err = %v", test.status, got, err)
		}
		if len(changes(calls)) != 1 {
			t.Errorf("status %d: sent %d changes, want exactly one", test.status, len(changes(calls)))
		}
	}

	// A transport failure of a change is reported as uncertain and not repeated.
	var calls []call
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if isChange(c.command()) {
			return nil, errors.New("connection reset by peer")
		}
		return commentsHandler(200, ``)(c)
	})
	_, err := env.invokeConfirmed(commentsDelete.ID, "write", `{`+fileArgs+`,"thread_id":"`+threadA+`"}`)
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(changes(calls)) != 1 {
		t.Fatalf("err = %v, changes = %d", err, len(changes(calls)))
	}
}

func TestCommentTextsAreBounded(t *testing.T) {
	long := strings.Repeat("ä", 5000)
	var calls []call
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.command() == cmdThreads {
			return jsonResponse(200, `[{"id":"`+threadA+`","content":"`+long+`"}]`), nil
		}
		return commentsHandler(200, ``)(c)
	})
	result, err := env.invoke(commentsThreads.ID, "one", `{`+fileArgs+`}`)
	if err != nil || strings.Count(result, "ä") > maxExcerptBytes/2 {
		t.Fatalf("excerpt not bounded: %d, %v", len(result), err)
	}
}
