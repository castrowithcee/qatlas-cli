package bookstack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

// commentServer holds pages 1 (book 7) and 2 (book 9) and comments on them. Comment 11 and 12 (a reply to
// local 1) are on page 1, 21 is on page 2, 31 is not a page comment.
type commentServer struct {
	*httptest.Server
	rec          *recorder
	ignoreFilter bool
	mu           sync.Mutex
	bodies       []map[string]any
}

func comment(id, page, local int64, parent any, kind string) map[string]any {
	return map[string]any{
		"id": id, "commentable_id": page, "commentable_type": kind, "parent_id": parent, "local_id": local,
		"content_ref": "ref", "created_by": 3, "updated_by": 3,
		"created_at": "2026-01-01T00:00:00.000000Z", "updated_at": "2026-01-02T00:00:00.000000Z",
	}
}

func newCommentServer(t *testing.T, ignoreFilter bool) *commentServer {
	t.Helper()
	s := &commentServer{rec: &recorder{}, ignoreFilter: ignoreFilter}
	all := []map[string]any{
		comment(11, 1, 1, nil, "page"), comment(12, 1, 2, 1, "page"), comment(21, 2, 1, nil, "page"),
		comment(31, 1, 3, nil, "chapter"),
	}
	byID := map[string]map[string]any{}
	for _, c := range all {
		byID[fmt.Sprint(c["id"])] = c
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/comments", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		if r.Method == http.MethodPost {
			s.capture(r)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 40, "commentable_id": 1, "commentable_type": "page", "parent_id": 1, "local_id": 4,
				"content_ref": "", "archived": false, "created_by": 3, "updated_by": 3,
			})
			return
		}
		q := r.URL.Query()
		offset, _ := strconv.Atoi(q.Get("offset"))
		count, _ := strconv.Atoi(q.Get("count"))
		var matching []map[string]any
		for _, c := range all {
			if !ignoreFilter {
				if v := q.Get("filter[commentable_type]"); v != "" && v != c["commentable_type"] {
					continue
				}
				if v := q.Get("filter[commentable_id]"); v != "" && v != fmt.Sprint(c["commentable_id"]) {
					continue
				}
			}
			matching = append(matching, c)
		}
		end := min(offset+count, len(matching))
		window := []map[string]any{}
		if offset < end {
			window = matching[offset:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": window, "total": len(matching)})
	})
	mux.HandleFunc("/api/comments/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		id := strings.TrimPrefix(r.URL.Path, "/api/comments/")
		c, ok := byID[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPut:
			s.capture(r)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 11, "commentable_id": 1, "commentable_type": "page", "parent_id": nil, "local_id": 1,
				"content_ref": "ref", "archived": true, "created_by": 3, "updated_by": 3,
			})
		default:
			out := map[string]any{}
			for k, v := range c {
				out[k] = v
			}
			out["html"] = "<p>text</p>"
			out["created_by"] = testUser
			if id == "11" {
				reply := comment(12, 1, 2, 1, "page")
				reply["html"] = "<p>reply</p>"
				foreignPage := comment(99, 2, 2, 1, "page")
				foreignPage["html"] = "<p>other page</p>"
				otherParent := comment(98, 1, 5, 4, "page")
				otherParent["html"] = "x"
				out["replies"] = []any{reply, foreignPage, otherParent}
			}
			_ = json.NewEncoder(w).Encode(out)
		}
	})
	mux.HandleFunc("/api/pages/", func(w http.ResponseWriter, r *http.Request) {
		s.rec.record(r)
		switch strings.TrimPrefix(r.URL.Path, "/api/pages/") {
		case "1":
			_, _ = w.Write([]byte(`{"id":1,"book_id":7}`))
		case "2":
			_, _ = w.Write([]byte(`{"id":2,"book_id":9}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *commentServer) capture(r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()
}

func (s *commentServer) requests() []string { return requests(s.rec) }

func call(t *testing.T, op, url, args string, targets ...string) (any, error) {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	return lookup(t, op)(context.Background(), boundResolved(url, targets...), resolver(nil), nil, json.RawMessage(args))
}

func rowIDs(t *testing.T, result any) []int64 {
	t.Helper()
	c, ok := result.(output.Collection)
	if !ok {
		t.Fatalf("result = %T", result)
	}
	return ids(c.Rows)
}

func TestCommentToolsAreGroupedAndRisked(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	hasGroup := false
	for _, g := range meta.Groups {
		hasGroup = hasGroup || g.ID == "comments"
	}
	if !hasGroup {
		t.Error("group comments is missing")
	}
	inProfile := map[string]bool{}
	for _, id := range meta.Profiles[0].Tools {
		inProfile[id] = true
	}
	for _, d := range []capability.Descriptor{commentsList, commentsGet, commentsCreate, commentsUpdate, commentsDelete} {
		registered := false
		for _, candidate := range reg.Provider(Provider) {
			if candidate.ID == d.ID {
				registered = true
				if candidate.Group != "comments" {
					t.Errorf("%s group = %q", d.ID, candidate.Group)
				}
			}
		}
		if !registered {
			t.Errorf("%s is not registered", d.ID)
		}
		read := d.Risk.Effect == capability.EffectRead
		if inProfile[d.ID] != read {
			t.Errorf("%s in read profile = %v, want %v", d.ID, inProfile[d.ID], read)
		}
		if !read && (d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity == "" || d.Risk.Idempotency == "") {
			t.Errorf("%s risk incomplete: %+v", d.ID, d.Risk)
		}
		if d.RequiresToolAllowList != (d.ID == commentsDelete.ID) {
			t.Errorf("%s allow list = %v", d.ID, d.RequiresToolAllowList)
		}
	}
	if commentsCreate.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		commentsUpdate.Risk.Idempotency != capability.IdempotencyIdempotent ||
		commentsDelete.Risk.Effect != capability.EffectDelete {
		t.Error("write risks are wrong")
	}
	if !strings.Contains(commentsDelete.Description, "final") || !strings.Contains(commentsDelete.Description, "no recycle bin") {
		t.Errorf("delete description = %q", commentsDelete.Description)
	}
}

func TestCommentsListBindsThePageAndFiltersRows(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		server := newCommentServer(t, ignore)
		result, err := call(t, commentsList.ID, server.URL, `{"page_id":1}`, "book/7")
		if err != nil {
			t.Fatal(err)
		}
		if got := rowIDs(t, result); !reflect.DeepEqual(got, []int64{11, 12}) {
			t.Errorf("ignoreFilter=%v: ids = %v, want [11 12]", ignore, got)
		}
		rows := result.(output.Collection).Rows
		if rows[1]["parent_id"] != int64(1) || rows[0]["parent_id"] != int64(0) || rows[1]["page_id"] != int64(1) {
			t.Errorf("rows = %v", rows)
		}
		if got := server.requests(); got[0] != "GET /api/pages/1" || got[1] != "GET /api/comments" {
			t.Errorf("requests = %v", got)
		}
		_, _, queries, _ := server.rec.snapshot()
		if !strings.Contains(queries[1], "filter%5Bcommentable_id%5D=1") || !strings.Contains(queries[1], "filter%5Bcommentable_type%5D=page") {
			t.Errorf("query = %q", queries[1])
		}
	}
}

func TestCommentsListPaging(t *testing.T) {
	server := newCommentServer(t, true)
	result, err := call(t, commentsList.ID, server.URL, `{"page_id":1,"limit":1,"offset":1}`, "book/7")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(t, result); !reflect.DeepEqual(got, []int64{12}) {
		t.Errorf("ids = %v, want [12]", got)
	}
}

func TestCommentsListBindingRules(t *testing.T) {
	server := newCommentServer(t, false)
	if _, err := call(t, commentsList.ID, server.URL, `{}`, "book/7"); !isInvalidRequest(err) {
		t.Errorf("bound without page_id: err = %v", err)
	}
	_, err := call(t, commentsList.ID, server.URL, `{"page_id":2}`, "book/7")
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
		t.Errorf("foreign page: err = %v", err)
	}
	if got := server.requests(); !reflect.DeepEqual(got, []string{"GET /api/pages/2"}) {
		t.Errorf("requests = %v, want only the evidence read", got)
	}
	// Without targets page_id is optional; page comments of every page come back, other types never.
	server = newCommentServer(t, true)
	result, err := call(t, commentsList.ID, server.URL, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(t, result); !reflect.DeepEqual(got, []int64{11, 12, 21}) {
		t.Errorf("unbound ids = %v", got)
	}
	if got := server.requests(); got[0] != "GET /api/comments" {
		t.Errorf("unbound requests = %v", got)
	}
}

func TestCommentsGet(t *testing.T) {
	server := newCommentServer(t, false)
	result, err := call(t, commentsGet.ID, server.URL, `{"id":11}`, "book/7")
	if err != nil {
		t.Fatal(err)
	}
	obj := result.(output.Object)
	fields := map[string]any{}
	for _, f := range obj.Fields {
		fields[f.Name] = f.Value
	}
	if fields["html"] != "<p>text</p>" || fields["page_id"] != int64(1) || fields["truncated"] != false {
		t.Errorf("fields = %v", fields)
	}
	replies := fields["replies"].([]map[string]any)
	if len(replies) != 1 || replies[0]["id"] != int64(12) || replies[0]["parent_id"] != int64(1) || replies[0]["html"] != "<p>reply</p>" {
		t.Errorf("replies = %v, want only the direct reply of the same page", replies)
	}
	if got := server.requests(); !reflect.DeepEqual(got, []string{"GET /api/comments/11", "GET /api/pages/1"}) {
		t.Errorf("requests = %v", got)
	}
}

func TestCommentsGetRefusesForeignAndNonPageComments(t *testing.T) {
	server := newCommentServer(t, false)
	_, err := call(t, commentsGet.ID, server.URL, `{"id":21}`, "book/7")
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") || strings.Contains(err.Error(), "text") {
		t.Errorf("foreign comment: err = %v", err)
	}
	if _, err := call(t, commentsGet.ID, server.URL, `{"id":31}`, "book/7"); !isInvalidRequest(err) {
		t.Errorf("non-page comment bound: err = %v", err)
	}
	if _, err := call(t, commentsGet.ID, server.URL, `{"id":31}`); !isInvalidRequest(err) {
		t.Errorf("non-page comment unbound: err = %v", err)
	}
	result, err := call(t, commentsGet.ID, server.URL, `{"id":21}`)
	if err != nil || result == nil {
		t.Errorf("unbound foreign-book comment: err = %v", err)
	}
}

func TestCommentsGetBoundsTextAndReplies(t *testing.T) {
	long := strings.Repeat("é", 40000) // 80000 bytes
	mux := http.NewServeMux()
	mux.HandleFunc("/api/comments/5", func(w http.ResponseWriter, r *http.Request) {
		var replies []any
		for i := int64(0); i < 205; i++ {
			c := comment(100+i, 1, 10+i, 1, "page")
			c["html"] = "r"
			replies = append(replies, c)
		}
		c := comment(5, 1, 1, nil, "page")
		c["html"], c["replies"] = long, replies
		_ = json.NewEncoder(w).Encode(c)
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	result, err := call(t, commentsGet.ID, server.URL, `{"id":5}`)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	for _, f := range result.(output.Object).Fields {
		fields[f.Name] = f.Value
	}
	html := fields["html"].(string)
	if len(html) > 64<<10 || len(html) < 64<<10-3 || !strings.HasPrefix(long, html) {
		t.Errorf("html bytes = %d", len(html))
	}
	if n := len(fields["replies"].([]map[string]any)); n != 200 || fields["truncated"] != true {
		t.Errorf("replies = %d, truncated = %v", n, fields["truncated"])
	}
}

func TestCommentWriteBodiesAndEvidence(t *testing.T) {
	for _, tt := range []struct {
		name, op, args string
		targets        []string
		want           map[string]any
		requests       []string
	}{
		{"create", commentsCreate.ID, `{"page_id":1,"html":"<p>a & b</p>"}`, []string{"book/7"},
			map[string]any{"page_id": 1.0, "html": "<p>a & b</p>"}, []string{"GET /api/pages/1", "POST /api/comments"}},
		{"reply with content_ref", commentsCreate.ID, `{"page_id":1,"html":"<p>r</p>","reply_to":1,"content_ref":"bkmrk-x:1:2-3"}`, []string{"book/7"},
			map[string]any{"page_id": 1.0, "html": "<p>r</p>", "reply_to": 1.0, "content_ref": "bkmrk-x:1:2-3"}, []string{"GET /api/pages/1", "POST /api/comments"}},
		{"create unbound reads nothing", commentsCreate.ID, `{"page_id":2,"html":"x"}`, nil,
			map[string]any{"page_id": 2.0, "html": "x"}, []string{"POST /api/comments"}},
		{"update html", commentsUpdate.ID, `{"id":11,"html":"<p>n</p>"}`, []string{"book/7"},
			map[string]any{"html": "<p>n</p>"}, []string{"GET /api/comments/11", "GET /api/pages/1", "PUT /api/comments/11"}},
		{"archive", commentsUpdate.ID, `{"id":11,"archived":true}`, []string{"book/7"},
			map[string]any{"archived": true}, []string{"GET /api/comments/11", "GET /api/pages/1", "PUT /api/comments/11"}},
		{"reopen sends false", commentsUpdate.ID, `{"id":11,"archived":false,"html":"h"}`, nil,
			map[string]any{"archived": false, "html": "h"}, []string{"PUT /api/comments/11"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newCommentServer(t, false)
			result, err := call(t, tt.op, server.URL, tt.args, tt.targets...)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil {
				t.Error("no result")
			}
			if len(server.bodies) != 1 || !reflect.DeepEqual(server.bodies[0], tt.want) {
				t.Errorf("body = %v, want %v", server.bodies, tt.want)
			}
			if got := server.requests(); !reflect.DeepEqual(got, tt.requests) {
				t.Errorf("requests = %v, want %v", got, tt.requests)
			}
		})
	}
}

func TestCommentWriteResultShape(t *testing.T) {
	server := newCommentServer(t, false)
	result, err := call(t, commentsCreate.ID, server.URL, `{"page_id":1,"html":"x","reply_to":1}`)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	for _, f := range result.(output.Object).Fields {
		fields[f.Name] = f.Value
	}
	if _, ok := fields["html"]; ok || fields["parent_id"] != int64(1) || fields["local_id"] != int64(4) || fields["archived"] != false {
		t.Errorf("fields = %v", fields)
	}
}

func TestCommentMutationsRefuseForeignTargetsWithoutChange(t *testing.T) {
	for _, tt := range []struct {
		name, op, args string
		reads          []string
	}{
		{"create on foreign page", commentsCreate.ID, `{"page_id":2,"html":"x"}`, []string{"GET /api/pages/2"}},
		{"update foreign comment", commentsUpdate.ID, `{"id":21,"html":"x"}`, []string{"GET /api/comments/21", "GET /api/pages/2"}},
		{"delete foreign comment", commentsDelete.ID, `{"id":21}`, []string{"GET /api/comments/21", "GET /api/pages/2"}},
		{"delete non-page comment", commentsDelete.ID, `{"id":31}`, []string{"GET /api/comments/31"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newCommentServer(t, false)
			_, err := call(t, tt.op, server.URL, tt.args, "book/7")
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "9") {
				t.Fatalf("err = %v", err)
			}
			if got := server.requests(); !reflect.DeepEqual(got, tt.reads) {
				t.Errorf("requests = %v, want %v", got, tt.reads)
			}
		})
	}
}

func TestCommentDeleteSendsOneDelete(t *testing.T) {
	server := newCommentServer(t, false)
	result, err := call(t, commentsDelete.ID, server.URL, `{"id":11}`, "book/7")
	if err != nil || !reflect.DeepEqual(result, map[string]bool{"deleted": true}) {
		t.Fatalf("result = %v, err = %v", result, err)
	}
	want := []string{"GET /api/comments/11", "GET /api/pages/1", "DELETE /api/comments/11"}
	if got := server.requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	server = newCommentServer(t, false)
	if _, err := call(t, commentsDelete.ID, server.URL, `{"id":21}`); err != nil {
		t.Fatal(err)
	}
	if got := server.requests(); !reflect.DeepEqual(got, []string{"DELETE /api/comments/21"}) {
		t.Errorf("unbound requests = %v", got)
	}
}

func TestCommentWritesAreRefusedLocallyBeforeSecretsAndIO(t *testing.T) {
	server := newCommentServer(t, false)
	t.Setenv("TEST_TOKEN_ID", "")
	t.Setenv("TEST_TOKEN_SECRET", "")
	for _, tt := range []struct {
		name, op, args string
		targets        []string
	}{
		{"list bound without page", commentsList.ID, `{}`, []string{"book/7"}},
		{"create without html", commentsCreate.ID, `{"page_id":1}`, nil},
		{"create long html", commentsCreate.ID, `{"page_id":1,"html":"` + strings.Repeat("é", 65537) + `"}`, nil},
		{"create empty html", commentsCreate.ID, `{"page_id":1,"html":""}`, nil},
		{"create long content_ref", commentsCreate.ID, `{"page_id":1,"html":"x","content_ref":"` + strings.Repeat("é", 256) + `"}`, nil},
		{"create bad reply_to", commentsCreate.ID, `{"page_id":1,"html":"x","reply_to":0}`, nil},
		{"update nothing", commentsUpdate.ID, `{"id":11}`, nil},
		{"update empty html", commentsUpdate.ID, `{"id":11,"html":""}`, nil},
		{"update long html", commentsUpdate.ID, `{"id":11,"html":"` + strings.Repeat("a", 65537) + `"}`, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lookup(t, tt.op)(context.Background(), boundResolved(server.URL, tt.targets...), resolver(nil), nil, json.RawMessage(tt.args))
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid-request", err)
			}
		})
	}
	if got := server.requests(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestCommentMutationsReportUncertainty(t *testing.T) {
	const hint = "this change may have taken effect"
	for status, wantHint := range map[int]bool{http.StatusBadGateway: true, http.StatusUnprocessableEntity: false} {
		rec := &recorder{}
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			w.WriteHeader(status)
		}))
		c := newClient(t, server.URL, nil)
		for name, run := range map[string]func() error{
			"create": func() error {
				html := "x"
				_, err := c.CreateComment(context.Background(), commentMutation{PageID: 1, HTML: &html})
				return err
			},
			"update": func() error {
				archived := true
				_, err := c.UpdateComment(context.Background(), 11, commentMutation{Archived: &archived})
				return err
			},
			"delete": func() error { return c.DeleteComment(context.Background(), 11) },
		} {
			err := run()
			if err == nil || strings.Contains(err.Error(), hint) != wantHint {
				t.Errorf("%s %d: err = %v, hint wanted %v", name, status, err, wantHint)
			}
		}
		if n := len(requests(rec)); n != 3 {
			t.Errorf("status %d: requests = %d, want 3 (no retry)", status, n)
		}
		server.Close()
	}
}
