package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	commentCollection = "/remote.php/dav/comments/files/1003"
	commentText       = "text-canary-comment-4e7d"
)

func commentXML(id, actor, message string) string {
	return `<d:response><d:href>` + commentCollection + `/` + id + `</d:href><d:propstat><d:prop>` +
		`<oc:id>` + id + `</oc:id><oc:verb>comment</oc:verb><oc:actorType>users</oc:actorType>` +
		`<oc:actorId>` + actor + `</oc:actorId><oc:actorDisplayName>Name of ` + actor + `</oc:actorDisplayName>` +
		`<oc:creationDateTime>Mon, 02 Mar 2026 11:15:00 GMT</oc:creationDateTime>` +
		`<oc:objectType>files</oc:objectType><oc:objectId>1003</oc:objectId><oc:message>` + message + `</oc:message>` +
		`<oc:mentions><oc:mention><oc:mentionId>x</oc:mentionId></oc:mention></oc:mentions>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

// commentServer answers the stat of note.txt (a file) and of Docs (a folder) and lets the test answer the
// comment requests; every request after the stat is recorded.
func commentServer(t *testing.T, answer func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == methodPropfind && strings.HasPrefix(request.URL.Path, "/remote.php/dav/files/") {
			if strings.HasSuffix(request.URL.Path, "/Docs") {
				return xmlResponse(http.StatusMultiStatus, multistatus(folderXML(aliceRoot+"/Docs/", "Docs", "1004", "0"))), nil
			}
			return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
		}
		return answer(request)
	})
}

func invokeComment(t *testing.T, fn capability.Handler, args string) (any, error) {
	t.Helper()
	red := &redact.Redactor{}
	return folderBound(fn)(capability.WithConfirmed(context.Background()), localConnection("", ""), resolver(red), red, json.RawMessage(args))
}

func commentRequests(calls *[]call) []call {
	var out []call
	for _, c := range *calls {
		if !strings.HasPrefix(c.url.Path, "/remote.php/dav/files/") {
			out = append(out, c)
		}
	}
	return out
}

func TestCommentsListPagesWithOneReportAndCutsLongText(t *testing.T) {
	long := strings.Repeat("ä", maxCommentChars+5)
	calls := commentServer(t, func(request *http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(
			`<d:response><d:href>`+commentCollection+`/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop>`+
				`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
			commentXML("12", "alice", "first &amp; &lt;b&gt;"), commentXML("11", "bobby", long), commentXML("10", "carol", "third"))), nil
	})
	got, err := invokeComment(t, invokeCommentsList, `{"path":"note.txt","limit":2,"offset":4}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*FileCommentList)
	if list.Count != 2 || !list.Truncated || list.Offset != 4 || list.Comments[0].CommentID != "12" ||
		list.Comments[0].Message != "first & <b>" || list.Comments[0].AuthorID != "alice" {
		t.Errorf("list = %+v", list)
	}
	if !list.Comments[1].MessageTruncated || len([]rune(list.Comments[1].Message)) != maxCommentChars {
		t.Errorf("long comment = %d runes, truncated %v", len([]rune(list.Comments[1].Message)), list.Comments[1].MessageTruncated)
	}
	requests := commentRequests(calls)
	if len(requests) != 1 || requests[0].method != methodReport || requests[0].url.Path != commentCollection ||
		!strings.Contains(requests[0].body, "<oc:limit>3</oc:limit>") || !strings.Contains(requests[0].body, "<oc:offset>4</oc:offset>") ||
		!strings.Contains(requests[0].body, "oc:filter-comments") {
		t.Errorf("requests = %+v", requests)
	}
}

func TestCommentsListDefaultsAndLastPage(t *testing.T) {
	calls := commentServer(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(commentXML("12", "alice", "only"))), nil
	})
	got, err := invokeComment(t, invokeCommentsList, `{"path":"note.txt"}`)
	list, _ := got.(*FileCommentList)
	if err != nil || list.Count != 1 || list.Truncated {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if body := commentRequests(calls)[0].body; !strings.Contains(body, "<oc:limit>21</oc:limit>") || !strings.Contains(body, "<oc:offset>0</oc:offset>") {
		t.Errorf("body = %s", body)
	}
	for _, args := range []string{`{"path":"note.txt","limit":51}`, `{"path":"note.txt","limit":0}`, `{"path":"note.txt","offset":-1}`, `{"path":"note.txt","offset":100001}`} {
		if _, err := invokeComment(t, invokeCommentsList, args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
}

func TestCommentsListIgnoresEntriesOfAnotherFileAndAnEmptyAnswer(t *testing.T) {
	foreign := strings.Replace(commentXML("13", "alice", "x"), "<oc:objectId>1003</oc:objectId>", "<oc:objectId>9999</oc:objectId>", 1)
	mismatch := strings.Replace(commentXML("14", "alice", "x"), "<oc:id>14</oc:id>", "<oc:id>15</oc:id>", 1)
	nonDigit := strings.Replace(commentXML("16", "alice", "x"), "/16<", "/abc<", 1)
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == methodReport {
			return xmlResponse(http.StatusMultiStatus, multistatus(foreign, mismatch, nonDigit, commentXML("12", "alice", "ok"))), nil
		}
		return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
	})
	got, err := invokeComment(t, invokeCommentsList, `{"path":"note.txt"}`)
	if list, _ := got.(*FileCommentList); err != nil || list.Count != 1 || list.Comments[0].CommentID != "12" {
		t.Errorf("list = %+v, %v", got, err)
	}
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == methodReport {
			return xmlResponse(http.StatusMultiStatus, multistatus()), nil
		}
		return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
	})
	got, err = invokeComment(t, invokeCommentsList, `{"path":"note.txt"}`)
	if list, _ := got.(*FileCommentList); err != nil || list.Count != 0 || list.Comments == nil {
		t.Errorf("empty list = %+v, %v", got, err)
	}
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == methodReport {
			return xmlResponse(http.StatusMultiStatus, multistatus(strings.Replace(commentXML("12", "a", "x"), "/comments/files/1003/", "/comments/files/2000/", 1))), nil
		}
		return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
	})
	if _, err := invokeComment(t, invokeCommentsList, `{"path":"note.txt"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("foreign collection: err = %v", err)
	}
}

func TestCommentToolsRefuseBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	reads := 0
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(string) string { reads++; return "x" }, nil, nil, red)
	cases := map[string]struct {
		fn   capability.Handler
		args string
	}{
		"list outside":        {invokeCommentsList, `{"path":"../secret.txt"}`},
		"list absolute":       {invokeCommentsList, `{"path":"/etc/passwd"}`},
		"create outside":      {invokeCommentsCreate, `{"path":"../secret.txt","message":"hi"}`},
		"update outside":      {invokeCommentsUpdate, `{"path":"../x","comment_id":"1","message":"hi"}`},
		"delete outside":      {invokeCommentsDelete, `{"path":"a/../../x","comment_id":"1"}`},
		"root":                {invokeCommentsList, `{"path":""}`},
		"file_id argument":    {invokeCommentsList, `{"path":"note.txt","file_id":"7"}`},
		"url argument":        {invokeCommentsCreate, `{"path":"note.txt","message":"hi","url":"https://x.invalid"}`},
		"letters id":          {invokeCommentsUpdate, `{"path":"note.txt","comment_id":"12a","message":"hi"}`},
		"path id":             {invokeCommentsDelete, `{"path":"note.txt","comment_id":"12/../13"}`},
		"empty id":            {invokeCommentsDelete, `{"path":"note.txt","comment_id":""}`},
		"signed id":           {invokeCommentsDelete, `{"path":"note.txt","comment_id":"-1"}`},
		"long id":             {invokeCommentsDelete, `{"path":"note.txt","comment_id":"123456789012345678901"}`},
		"blank message":       {invokeCommentsCreate, `{"path":"note.txt","message":"  \n"}`},
		"long message":        {invokeCommentsCreate, `{"path":"note.txt","message":"` + strings.Repeat("x", maxCommentChars+1) + `"}`},
		"control in message":  {invokeCommentsUpdate, `{"path":"note.txt","comment_id":"1","message":"a\u0000b"}`},
		"message without id":  {invokeCommentsUpdate, `{"path":"note.txt","message":"hi"}`},
		"account only target": {invokeCommentsList, `{"path":"note.txt"}`},
	}
	for name, c := range cases {
		resolved := localConnection("", "")
		if name == "account only target" {
			resolved = accountConnection()
		}
		_, err := folderBound(c.fn)(capability.WithConfirmed(context.Background()), resolved, secrets, red, json.RawMessage(c.args))
		if err == nil || reads != 0 {
			t.Errorf("%s: err = %v, secret reads = %d", name, err, reads)
		}
		if err != nil && (strings.Contains(err.Error(), "secret.txt") || strings.Contains(err.Error(), "passwd")) {
			t.Errorf("%s: the foreign target is named: %v", name, err)
		}
	}
}

func TestCommentsAreForFilesOnly(t *testing.T) {
	calls := commentServer(t, func(request *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		return status(500), nil
	})
	for name, fn := range map[string]capability.Handler{"list": invokeCommentsList, "create": invokeCommentsCreate} {
		if _, err := invokeComment(t, fn, `{"path":"Docs","message":"hi"}`); err == nil || !strings.Contains(err.Error(), "files only") {
			t.Errorf("%s on a folder: err = %v", name, err)
		}
	}
	if len(commentRequests(calls)) != 0 {
		t.Errorf("requests = %+v", commentRequests(calls))
	}
}

func TestCommentsCreateSendsOnePostWithEncodedJSON(t *testing.T) {
	message := "He said \"hi\"\n\\ <b>&</b> @bobby ä"
	calls := commentServer(t, func(request *http.Request) (*http.Response, error) {
		response := status(201)
		response.Header.Set("Content-Location", "/remote.php/dav/comments/files/1003/77")
		return response, nil
	})
	encoded, _ := json.Marshal(message)
	got, err := invokeComment(t, invokeCommentsCreate, `{"path":"note.txt","message":`+string(encoded)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(map[string]any)
	if result["created"] != true || result["comment_id"] != "77" || result["path"] != "note.txt" {
		t.Errorf("result = %+v", result)
	}
	requests := commentRequests(calls)
	var body map[string]string
	if len(requests) != 1 || requests[0].method != http.MethodPost || requests[0].url.Path != commentCollection ||
		json.Unmarshal([]byte(requests[0].body), &body) != nil || body["message"] != message ||
		body["actorType"] != "users" || body["verb"] != "comment" || len(body) != 3 {
		t.Errorf("requests = %+v", requests)
	}
}

func TestCommentsCreateWithoutLocationStillReportsTheComment(t *testing.T) {
	commentServer(t, func(*http.Request) (*http.Response, error) { return status(201), nil })
	got, err := invokeComment(t, invokeCommentsCreate, `{"path":"note.txt","message":"hi"}`)
	if result, _ := got.(map[string]any); err != nil || result["created"] != true || result["comment_id"] != nil {
		t.Errorf("result = %+v, %v", got, err)
	}
	for _, location := range []string{"/remote.php/dav/comments/files/2000/77", "/remote.php/dav/comments/files/1003/7a", "https://evil.invalid/remote.php/dav/comments/files/1003/77", "/remote.php/dav/comments/files/1003/77/9"} {
		commentServer(t, func(*http.Request) (*http.Response, error) {
			response := status(201)
			response.Header.Set("Content-Location", location)
			return response, nil
		})
		got, err := invokeComment(t, invokeCommentsCreate, `{"path":"note.txt","message":"hi"}`)
		if result, _ := got.(map[string]any); err != nil || result["comment_id"] != nil {
			t.Errorf("%s: result = %+v, %v", location, got, err)
		}
	}
}

func TestCommentsUpdateSendsOneEscapedProppatch(t *testing.T) {
	calls := commentServer(t, func(request *http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(`<d:response><d:href>`+commentCollection+`/42</d:href><d:propstat>`+
			`<d:prop><oc:message/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)), nil
	})
	got, err := invokeComment(t, invokeCommentsUpdate, `{"path":"note.txt","comment_id":"42","message":"a <b> & \"c\""}`)
	if result, _ := got.(map[string]any); err != nil || result["updated"] != true || result["comment_id"] != "42" {
		t.Fatalf("result = %+v, %v", got, err)
	}
	requests := commentRequests(calls)
	if len(requests) != 1 || requests[0].method != methodProppatch || requests[0].url.Path != commentCollection+"/42" ||
		!strings.Contains(requests[0].body, "<oc:message>a &lt;b&gt; &amp; &#34;c&#34;</oc:message>") {
		t.Errorf("requests = %+v", requests)
	}
}

func TestCommentsDeleteSendsOneDelete(t *testing.T) {
	calls := commentServer(t, func(*http.Request) (*http.Response, error) { return status(204), nil })
	got, err := invokeComment(t, invokeCommentsDelete, `{"path":"note.txt","comment_id":"42"}`)
	if result, _ := got.(map[string]any); err != nil || result["deleted"] != true {
		t.Fatalf("result = %+v, %v", got, err)
	}
	requests := commentRequests(calls)
	if len(requests) != 1 || requests[0].method != http.MethodDelete || requests[0].url.Path != commentCollection+"/42" {
		t.Errorf("requests = %+v", requests)
	}
}

func commentMutations() map[string]struct {
	fn   capability.Handler
	args string
	at   string
	hint string
} {
	type m = struct {
		fn   capability.Handler
		args string
		at   string
		hint string
	}
	return map[string]m{
		"create": {invokeCommentsCreate, `{"path":"note.txt","message":"` + commentText + `"}`, http.MethodPost, "may have been created"},
		"update": {invokeCommentsUpdate, `{"path":"note.txt","comment_id":"42","message":"` + commentText + `"}`, methodProppatch, "may have been changed"},
		"delete": {invokeCommentsDelete, `{"path":"note.txt","comment_id":"42"}`, http.MethodDelete, "may have been deleted"},
	}
}

func TestCommentMutationsAreNeverRepeatedAfterAnUnclearOutcome(t *testing.T) {
	unclear := map[string]func() (*http.Response, error){
		"500":        func() (*http.Response, error) { return status(500), nil },
		"502":        func() (*http.Response, error) { return status(502), nil },
		"503":        func() (*http.Response, error) { return status(503), nil },
		"504":        func() (*http.Response, error) { return status(504), nil },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
		"unreadable": func() (*http.Response, error) {
			response := xmlResponse(http.StatusMultiStatus, "")
			response.Body = failingBody{}
			return response, nil
		},
		"not xml": func() (*http.Response, error) { return xmlResponse(http.StatusMultiStatus, "<html>"+bodyCanary), nil },
	}
	for name, m := range commentMutations() {
		for kind, answer := range unclear {
			if name != "update" && (kind == "unreadable" || kind == "not xml") {
				continue
			}
			t.Run(name+" "+kind, func(t *testing.T) {
				var methods []string
				commentServer(t, func(request *http.Request) (*http.Response, error) {
					methods = append(methods, request.Method)
					return answer()
				})
				_, err := invokeComment(t, m.fn, m.args)
				if err == nil || !strings.Contains(err.Error(), m.hint) || !strings.Contains(err.Error(), "before repeating") {
					t.Fatalf("err = %v, want the hint %q", err, m.hint)
				}
				if len(methods) != 1 || methods[0] != m.at {
					t.Errorf("methods = %v, want exactly one %s", methods, m.at)
				}
				for _, leak := range []string{bodyCanary, commentText} {
					if strings.Contains(err.Error(), leak) {
						t.Errorf("error leaks %q: %v", leak, err)
					}
				}
			})
		}
	}
}

func TestCommentMutationsReportClearRefusalsWithoutHintOrProviderText(t *testing.T) {
	for name, m := range commentMutations() {
		for _, code := range []int{400, 401, 403, 404, 409, 412, 301, 302, 307, 308} {
			t.Run(fmt.Sprint(name, " ", code), func(t *testing.T) {
				var methods []string
				commentServer(t, func(request *http.Request) (*http.Response, error) {
					methods = append(methods, request.Method)
					response := status(code)
					response.Header.Set("Location", "https://elsewhere.invalid/target-canary")
					return response, nil
				})
				_, err := invokeComment(t, m.fn, m.args)
				if err == nil || strings.Contains(err.Error(), "before repeating") || strings.Contains(err.Error(), bodyCanary) ||
					strings.Contains(err.Error(), "target-canary") || strings.Contains(err.Error(), "elsewhere") {
					t.Fatalf("err = %v", err)
				}
				if len(methods) != 1 {
					t.Errorf("methods = %v", methods)
				}
				switch code {
				case 403:
					if name != "create" && !strings.Contains(err.Error(), messageCommentNotAuthor) || classOf(err) != provider.ClassPermission {
						t.Errorf("403 err = %v", err)
					}
				case 301, 302, 307, 308:
					if !strings.Contains(err.Error(), messageRedirect) {
						t.Errorf("redirect err = %v", err)
					}
				}
			})
		}
	}
}

func TestCommentUpdateRefusedInsideTheMultistatusIsAPermissionError(t *testing.T) {
	commentServer(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(`<d:response><d:href>`+commentCollection+`/42</d:href><d:propstat>`+
			`<d:prop><oc:message/></d:prop><d:status>HTTP/1.1 403 Forbidden</d:status></d:propstat></d:response>`)), nil
	})
	_, err := invokeComment(t, invokeCommentsUpdate, `{"path":"note.txt","comment_id":"42","message":"hi"}`)
	if err == nil || classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), messageCommentNotAuthor) ||
		strings.Contains(err.Error(), "before repeating") {
		t.Errorf("err = %v", err)
	}
}

func TestCommentListRedirectIsAClearError(t *testing.T) {
	for _, code := range []int{301, 302, 307, 308} {
		commentServer(t, func(*http.Request) (*http.Response, error) {
			response := status(code)
			response.Header.Set("Location", "https://elsewhere.invalid/target-canary")
			return response, nil
		})
		_, err := invokeComment(t, invokeCommentsList, `{"path":"note.txt"}`)
		if err == nil || !strings.Contains(err.Error(), messageRedirect) || strings.Contains(err.Error(), "target-canary") {
			t.Errorf("%d: err = %v", code, err)
		}
	}
}

func TestCommentDescriptorsCarryTheContract(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect capability.Effect
		write  bool
	}{
		commentsList.ID: {capability.EffectRead, false}, commentsCreate.ID: {capability.EffectCreate, true},
		commentsUpdate.ID: {capability.EffectUpdate, true}, commentsDelete.ID: {capability.EffectDelete, true},
	}
	seen := 0
	for _, d := range reg.Provider(Provider) {
		w, ok := want[d.ID]
		if !ok {
			continue
		}
		seen++
		risk := d.Risk
		if d.Group != "files" || risk.Effect != w.effect || risk.Idempotency == "" || !risk.OpenWorld || risk.DataSensitivity == "" ||
			(risk.Confirmation == capability.ConfirmationRequired) != w.write ||
			d.RequiresToolAllowList != (d.ID == commentsDelete.ID) {
			t.Errorf("%s: group %q, risk %+v, allow-list %v", d.ID, d.Group, risk, d.RequiresToolAllowList)
		}
		for _, forbidden := range []string{"file_id", "fileid", "href", "url", "method", "header"} {
			if strings.Contains(string(d.InputSchema), forbidden) {
				t.Errorf("%s offers %q", d.ID, forbidden)
			}
		}
	}
	if seen != 4 {
		t.Fatalf("comment tools = %d", seen)
	}
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == commentsCreate.ID || id == commentsUpdate.ID || id == commentsDelete.ID {
				t.Errorf("profile %s offers %s", profile.ID, id)
			}
		}
		listed := false
		for _, id := range profile.Tools {
			listed = listed || id == commentsList.ID
		}
		if listed != (profile.ID == "read" || profile.ID == "write") {
			t.Errorf("profile %s lists comments.list = %v", profile.ID, listed)
		}
	}
}

// commentCore is a core with the real registry and one connection per access level: reading, everything
// but the tools list, and the tools list that names the delete.
func commentCore(t *testing.T) (*application.Core, *redact.Redactor) {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	cfg.Services["cloud"] = config.Service{Provider: Provider, BaseURL: mainInstance}
	cfg.Credentials["alice"] = config.Credential{Type: config.CredentialTypeEnv,
		Values: map[string]string{roleUserID: aliceUserEnv, roleAppPassword: aliceTokenEnv}}
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	cfg.Connections["rw"] = config.Connection{Service: "cloud", Credential: "alice", Target: "Reports", Permissions: all}
	cfg.Connections["listed"] = config.Connection{Service: "cloud", Credential: "alice", Target: "Reports", Permissions: all,
		Tools: []string{commentsList.ID, commentsDelete.ID}}
	red := &redact.Redactor{}
	return application.New(reg, cfg, resolver(red), red), red
}

func TestCommentDeleteIsReachableOnlyThroughTheToolsList(t *testing.T) {
	calls := commentServer(t, func(*http.Request) (*http.Response, error) { return status(204), nil })
	core, _ := commentCore(t)
	args := json.RawMessage(`{"path":"note.txt","comment_id":"42"}`)
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: commentsDelete.ID, Connection: "rw", Arguments: args, Confirmed: true}); err == nil {
		t.Error("delete ran on a connection without a tools list")
	}
	if len(*calls) != 0 {
		t.Fatalf("requests = %+v", *calls)
	}
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: commentsDelete.ID, Connection: "listed", Arguments: args}); err == nil {
		t.Error("delete ran without confirm")
	}
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: commentsDelete.ID, Connection: "listed", Arguments: args, Confirmed: true}); err != nil {
		t.Errorf("delete on the connection that lists it: %v", err)
	}
	if got := commentRequests(calls); len(got) != 1 || got[0].method != http.MethodDelete {
		t.Errorf("requests = %+v", got)
	}
}

// The invocation record of the core has no field for arguments or results (see the invokelog tests); the
// audit stream the core writes for the same call must not carry the comment text or the path either.
func TestCommentTextNeverReachesTheAuditStream(t *testing.T) {
	commentServer(t, func(*http.Request) (*http.Response, error) {
		response := status(201)
		response.Header.Set("Content-Location", "/remote.php/dav/comments/files/1003/77")
		return response, nil
	})
	core, _ := commentCore(t)
	var audit strings.Builder
	core.SetAudit(&audit)
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: commentsCreate.ID, Connection: "rw",
		Arguments: json.RawMessage(`{"path":"note.txt","message":"` + commentText + `"}`), Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audit.String(), commentsCreate.ID) {
		t.Errorf("the invocation was not audited: %q", audit.String())
	}
	if strings.Contains(audit.String(), commentText) || strings.Contains(audit.String(), "note.txt") {
		t.Errorf("the audit stream holds the comment text or the path: %s", audit.String())
	}
}
