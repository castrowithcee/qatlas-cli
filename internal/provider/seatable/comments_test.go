package seatable

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const commentCanary = "comment-text-canary-seatable-4c7a"

type commentCall struct{ method, path, query, body string }

func commentList() string {
	return `[{"id":11,"author":"a@example.invalid","comment":"` + commentCanary + `","dtable_uuid":"x","row_id":"` + rowID +
		`","created_at":"2026-01-01T00:00:00+00:00","updated_at":"2026-01-02T00:00:00+00:00","resolved":1},
 {"id":12,"author":"b@example.invalid","comment":"fremd","row_id":"` + otherRowID + `","resolved":0}]`
}

// serveComments answers metadata, the row read, the comment list and every change, and records the calls.
func serveComments(t *testing.T, changeStatus int, listing string) *[]commentCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]commentCall{}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		var data []byte
		if request.Body != nil {
			data, _ = io.ReadAll(request.Body)
		}
		mu.Lock()
		*calls = append(*calls, commentCall{request.Method, request.URL.Path, request.URL.RawQuery, string(data)})
		mu.Unlock()
		switch {
		case request.URL.Path == metaRoute(salesBase):
			return jsonResponse(http.StatusOK, linkMetadata), nil
		case strings.HasSuffix(request.URL.Path, rowsPath+rowID+"/"):
			return jsonResponse(http.StatusOK, `{"_id":"`+rowID+`","Name":"Bike"}`), nil
		case strings.HasSuffix(request.URL.Path, rowsPath+otherRowID+"/"):
			return jsonResponse(http.StatusNotFound, `{"error_msg":"`+bodyCanary+`"}`), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, commentsPath):
			return jsonResponse(http.StatusOK, listing), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, collaboratorsPath):
			return jsonResponse(http.StatusOK, `{"user_list":[{"name":"Ada","email":"ada@example.invalid","contact_email":"c@example.invalid","avatar_url":"https://x.invalid/a.png"}]}`), nil
		}
		return jsonResponse(changeStatus, `{"success":true,"error_msg":"`+bodyCanary+`"}`), nil
	})
	return calls
}

func changes(calls []commentCall) []commentCall {
	out := []commentCall{}
	for _, c := range calls {
		if c.method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func TestCommentToolsRiskAndSensitivity(t *testing.T) {
	want := map[string]struct {
		effect      capability.Effect
		sensitivity string
		allowList   bool
	}{
		"seatable.comments.list":      {capability.EffectRead, peopleSensitivity, false},
		"seatable.comments.create":    {capability.EffectCreate, dataSensitivity, false},
		"seatable.comments.delete":    {capability.EffectDelete, dataSensitivity, true},
		"seatable.collaborators.list": {capability.EffectRead, peopleSensitivity, false},
	}
	seen := 0
	for _, d := range registry(t).Provider(Provider) {
		w, ok := want[d.ID]
		if !ok {
			continue
		}
		seen++
		if d.Risk.Effect != w.effect || d.Risk.DataSensitivity != w.sensitivity || d.RequiresToolAllowList != w.allowList ||
			!d.Risk.OpenWorld || d.Risk.Idempotency == "" {
			t.Errorf("%s = %+v allow=%v", d.ID, d.Risk, d.RequiresToolAllowList)
		}
		if w.effect != capability.EffectRead && d.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s needs confirmation", d.ID)
		}
	}
	if seen != 4 {
		t.Fatalf("registered %d of 4 tools", seen)
	}
	if peopleSensitivity != "seatable-base-people" {
		t.Errorf("class = %s", peopleSensitivity)
	}
}

func TestCollaboratorsAreInNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, profile := range meta.Profiles {
		for _, id := range profile.Tools {
			if strings.HasPrefix(id, "seatable.collaborators.") || strings.HasPrefix(id, "seatable.comments.") {
				t.Errorf("profile %s contains %s", profile.ID, id)
			}
		}
	}
}

func TestListCommentsReadsTheRowFirstAndFiltersForeignEntries(t *testing.T) {
	calls := serveComments(t, 200, commentList())
	c, _ := client(t, "*")
	result, err := c.ListComments(context.Background(), CommentInput{Table: "Kunden", RowID: rowID})
	if err != nil || len(result.Comments) != 1 || result.Comments[0].ID != 11 || !result.Comments[0].Resolved ||
		result.Comments[0].Comment != commentCanary {
		t.Fatalf("ListComments = %+v, %v", result, err)
	}
	if len(*calls) < 2 || !strings.HasSuffix((*calls)[0].path, rowsPath+rowID+"/") {
		t.Fatalf("calls = %+v, want the row read first", *calls)
	}
	last := (*calls)[len(*calls)-1]
	if !strings.HasSuffix(last.path, commentsPath) || last.query != "row_id="+rowID {
		t.Errorf("list call = %+v", last)
	}
}

func TestCommentsOfAForeignRowAreRefusedWithoutNamingIt(t *testing.T) {
	calls := serveComments(t, 200, commentList())
	c, _ := client(t, "*")
	for name, run := range map[string]func() error{
		"list": func() error {
			_, err := c.ListComments(context.Background(), CommentInput{Table: "Kunden", RowID: otherRowID})
			return err
		},
		"create": func() error {
			return c.CreateComment(context.Background(), CommentInput{Table: "Kunden", RowID: otherRowID, Comment: "x"})
		},
		"delete": func() error {
			return c.DeleteComment(context.Background(), CommentInput{Table: "Kunden", RowID: otherRowID, CommentID: 12})
		},
	} {
		err := run()
		if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), otherRowID) {
			t.Errorf("%s = %v", name, err)
		}
	}
	for _, call := range *calls {
		if strings.HasSuffix(call.path, commentsPath) {
			t.Errorf("a comment route was used: %+v", call)
		}
	}
}

func TestCreateCommentSendsOneFixedRequest(t *testing.T) {
	calls := serveComments(t, 200, commentList())
	c, _ := client(t, "*")
	if err := c.CreateComment(context.Background(), CommentInput{Table: "Kunden", RowID: rowID, Comment: "Hallo"}); err != nil {
		t.Fatal(err)
	}
	got := changes(*calls)
	if len(got) != 1 || got[0].method != http.MethodPost || !strings.HasSuffix(got[0].path, commentsPath) ||
		got[0].query != "row_id="+rowID+"&table_id=0000" {
		t.Fatalf("changes = %+v", got)
	}
	var body map[string]string
	if json.Unmarshal([]byte(got[0].body), &body) != nil || len(body) != 1 || body["comment"] != "Hallo" {
		t.Errorf("body = %s", got[0].body)
	}
}

func TestCreateCommentDoesNotRetryAndReportsUncertainty(t *testing.T) {
	for _, status := range []int{500, 502} {
		calls := serveComments(t, status, commentList())
		c, _ := client(t, "*")
		err := c.CreateComment(context.Background(), CommentInput{Table: "Kunden", RowID: rowID, Comment: "Hallo"})
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("status %d: %v", status, err)
		}
		if n := len(changes(*calls)); n != 1 {
			t.Errorf("status %d: %d change requests", status, n)
		}
	}
}

func TestDeleteCommentBindsTheCommentToTheRow(t *testing.T) {
	calls := serveComments(t, 200, commentList())
	c, _ := client(t, "*")
	// Comment 12 belongs to another row: refused before any change.
	err := c.DeleteComment(context.Background(), CommentInput{Table: "Kunden", RowID: rowID, CommentID: 12})
	if err == nil || strings.Contains(err.Error(), otherRowID) || len(changes(*calls)) != 0 {
		t.Fatalf("foreign comment: %v, changes %+v", err, changes(*calls))
	}
	if err := c.DeleteComment(context.Background(), CommentInput{Table: "Kunden", RowID: rowID, CommentID: 11}); err != nil {
		t.Fatal(err)
	}
	got := changes(*calls)
	if len(got) != 1 || got[0].method != http.MethodDelete || !strings.HasSuffix(got[0].path, commentsPath+"11/") {
		t.Fatalf("changes = %+v", got)
	}
}

func TestDeleteCommentDoesNotRetry(t *testing.T) {
	calls := serveComments(t, 503, commentList())
	c, _ := client(t, "*")
	err := c.DeleteComment(context.Background(), CommentInput{Table: "Kunden", RowID: rowID, CommentID: 11})
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(changes(*calls)) != 1 {
		t.Errorf("err = %v, changes %d", err, len(changes(*calls)))
	}
}

func TestCommentRequestsAreRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	handlers := map[string]capability.Handler{
		"list":   capability.Handler(invokeCommentsList),
		"create": invokeCommentsChange("create comment", "create", (*Client).CreateComment, "created"),
		"delete": invokeCommentsChange("delete comment", "delete", (*Client).DeleteComment, "deleted"),
	}
	cases := map[string]string{
		"list":   `{"table":"Geheim","row_id":"` + rowID + `"}`,
		"create": `{"table":"Geheim","row_id":"` + rowID + `","comment":"x"}`,
		"delete": `{"table":"Geheim","row_id":"` + rowID + `","comment_id":1}`,
	}
	for id, handler := range handlers {
		resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
		if _, err := handler(tableCtx(), resolved, secrets, &redact.Redactor{}, json.RawMessage(cases[id])); err == nil ||
			strings.Contains(err.Error(), "Geheim") {
			t.Errorf("%s outside the allow-list = %v", id, err)
		}
	}
	wild := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "*")
	bad := map[string]string{
		"list":   `{"row_id":"kurz"}`,
		"create": `{"table":"Kunden","row_id":"` + rowID + `","comment":"   "}`,
		"delete": `{"table":"Kunden","row_id":"` + rowID + `","comment_id":0}`,
	}
	for id, raw := range bad {
		if _, err := handlers[id](tableCtx(), wild, secrets, &redact.Redactor{}, json.RawMessage(raw)); err == nil {
			t.Errorf("%s accepted %s", id, raw)
		}
	}
	long := `{"table":"Kunden","row_id":"` + rowID + `","comment":"` + strings.Repeat("a", maxCommentBytes+1) + `"}`
	if _, err := handlers["create"](tableCtx(), wild, secrets, &redact.Redactor{}, json.RawMessage(long)); err == nil {
		t.Error("an over-long comment was accepted")
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d, want 0", lookups)
	}
}

func TestCommentStatusErrorsDoNotCopyProviderText(t *testing.T) {
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == metaRoute(salesBase):
			return jsonResponse(http.StatusOK, linkMetadata), nil
		case strings.HasSuffix(request.URL.Path, rowsPath+rowID+"/"):
			return jsonResponse(http.StatusOK, `{"_id":"`+rowID+`"}`), nil
		}
		return jsonResponse(http.StatusForbidden, `{"error_msg":"`+bodyCanary+`"}`), nil
	})
	c, _ := client(t, "*")
	_, err := c.ListComments(context.Background(), CommentInput{Table: "Kunden", RowID: rowID})
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestListCollaboratorsReportsNamesAndMailsOnly(t *testing.T) {
	calls := serveComments(t, 200, "[]")
	for _, target := range []string{"*", "Kunden"} {
		c, _ := client(t, target)
		result, err := c.ListCollaborators(context.Background())
		if err != nil || len(result.Collaborators) != 1 || result.Collaborators[0].Email != "ada@example.invalid" {
			t.Fatalf("%s: %+v, %v", target, result, err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "avatar") || strings.Contains(string(encoded), "x.invalid") {
			t.Errorf("avatar leaked: %s", encoded)
		}
	}
	last := (*calls)[len(*calls)-1]
	if last.method != http.MethodGet || !strings.HasSuffix(last.path, collaboratorsPath) || last.query != "" {
		t.Errorf("call = %+v", last)
	}
}
