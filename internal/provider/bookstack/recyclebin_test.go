package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const recycleBinEntry = `{"id":4,"deleted_by":2,"created_at":"2026-10-01T10:00:00Z","updated_at":"x","deletable_type":"page","deletable_id":9,` +
	`"deletable":{"id":9,"name":"Gone","slug":"gone","book_id":3,"chapter_id":5,"description":"Desc","html":"<p>body</p>","pages_count":0,` +
	`"parent":{"id":5,"name":"Parent","slug":"parent","type":"chapter","description":"Desc"}}}`

func TestRecycleBinDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	meta, _ := reg.ProviderMetadata(Provider)
	for _, d := range []capability.Descriptor{recycleBinList, recycleBinRestore, recycleBinDestroy} {
		for _, role := range []string{"settings-manage", "restrictions-manage-all"} {
			if !strings.Contains(d.Description, role) {
				t.Errorf("%s description lacks %s", d.ID, role)
			}
		}
		for _, profile := range meta.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("profile %s contains %s", profile.ID, d.ID)
				}
			}
		}
	}
	for _, d := range reg.Provider(Provider) {
		if strings.Contains(d.ID, ".recycle-bin.") {
			if d.Group != "administration" || d.RequiresToolAllowList != strings.HasSuffix(d.ID, ".destroy") {
				t.Errorf("%s: group %q allowlist %v", d.ID, d.Group, d.RequiresToolAllowList)
			}
		}
	}
	if recycleBinDestroy.Risk.Effect != capability.EffectDelete || !strings.Contains(recycleBinDestroy.Description, "Permanently") ||
		recycleBinRestore.Risk.Effect != capability.EffectUpdate || recycleBinList.Risk != bookstackReadRisk {
		t.Error("risk or description wrong")
	}
}

func TestRecycleBinListReducesFields(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"data":[` + recycleBinEntry + `],"total":1}`))
	}))
	defer server.Close()
	got, err := newClient(t, server.URL, nil).ListRecycleBin(context.Background(), 0, 0, listQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %v", got.Rows)
	}
	row := got.Rows[0]
	want := map[string]any{"id": int64(4), "deleted_by": int64(2), "created_at": "2026-10-01T10:00:00Z", "deletable_type": "page",
		"deletable_id": int64(9), "name": "Gone", "slug": "gone", "book_id": int64(3), "chapter_id": int64(5),
		"pages_count": int64(0), "parent_type": "chapter", "parent_id": int64(5), "updated_at": "x",
		"deletable": map[string]any{"description": "Desc", "parent": map[string]any{"name": "Parent", "slug": "parent", "description": "Desc"}}}
	if !reflect.DeepEqual(map[string]any(row), want) {
		t.Errorf("row = %v, want %v", row, want)
	}
	if methods, paths, _, _ := rec.snapshot(); len(methods) != 1 || methods[0] != "GET" || paths[0] != "/api/recycle-bin" {
		t.Errorf("requests = %v %v", methods, paths)
	}
}

func TestRecycleBinMutationsUseFixedRequests(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.Method == http.MethodPut {
			_, _ = w.Write([]byte(`{"restore_count":3}`))
			return
		}
		_, _ = w.Write([]byte(`{"delete_count":2}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	restored, err := c.RestoreFromRecycleBin(context.Background(), 4)
	if err != nil || restored.Fields[0].Name != "restore_count" || restored.Fields[0].Value != int64(3) {
		t.Errorf("restore = %v, %v", restored, err)
	}
	destroyed, err := c.DestroyFromRecycleBin(context.Background(), 4)
	if err != nil || destroyed.Fields[0].Name != "delete_count" || destroyed.Fields[0].Value != int64(2) {
		t.Errorf("destroy = %v, %v", destroyed, err)
	}
	if got, want := requests(rec), []string{"PUT /api/recycle-bin/4", "DELETE /api/recycle-bin/4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestRecycleBinHandlersGateBeforeSecretAndIO(t *testing.T) {
	rec := &recorder{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { rec.record(r) }))
	defer server.Close()
	resolved := boundResolved(server.URL, "book/7")
	resolved.Secrets = envCredential(map[string]string{roleTokenID: "UNSET_ID", roleTokenSecret: "UNSET_SECRET"})
	ctx := context.Background()
	if _, err := invokeRecycleBinList(ctx, resolved, resolver(nil), nil, []byte(`{}`)); !isInvalidRequest(err) {
		t.Errorf("list: err = %v", err)
	}
	if _, err := invokeRecycleBinRestore(ctx, resolved, resolver(nil), nil, []byte(`{"deletion_id":1}`)); !isInvalidRequest(err) {
		t.Errorf("restore: err = %v", err)
	}
	if _, err := invokeRecycleBinDestroy(ctx, resolved, resolver(nil), nil, []byte(`{"deletion_id":1}`)); !isInvalidRequest(err) {
		t.Errorf("destroy: err = %v", err)
	}
	unbound := boundResolved(server.URL)
	if _, err := invokeRecycleBinDestroy(ctx, unbound, resolver(nil), nil, []byte(`{"deletion_id":0}`)); !isInvalidRequest(err) {
		t.Errorf("destroy 0: err = %v", err)
	}
	if got := requests(rec); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestRecycleBinMutationsReportUncertaintyWithoutRetry(t *testing.T) {
	const hint = "this change may have taken effect"
	for name, tt := range map[string]struct {
		handler http.HandlerFunc
		arrival bool
	}{
		"5xx":     {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, false},
		"timeout": {nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			handler, newCtx := tt.handler, func() context.Context { return context.Background() }
			if tt.arrival {
				handler, newCtx = cancelOnArrival(t)
			}
			rec := &recorder{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				handler(w, r)
			}))
			defer server.Close()
			c := newClient(t, server.URL, nil)
			for _, call := range []func(context.Context) error{
				func(ctx context.Context) error { _, err := c.RestoreFromRecycleBin(ctx, 4); return err },
				func(ctx context.Context) error { _, err := c.DestroyFromRecycleBin(ctx, 4); return err },
			} {
				err := call(newCtx())
				if tt.arrival {
					wantTimeout(t, err)
				}
				if err == nil || !strings.Contains(err.Error(), hint) {
					t.Errorf("err = %v, want uncertainty hint", err)
				}
			}
			if n := len(requests(rec)); n != 2 {
				t.Errorf("requests = %d, want 2 (no retry)", n)
			}
		})
	}
}

// binAreaServer adds a recycle bin to the area server. Entry 1 is a page of the chapter 1001, 2 a loose page of
// book 701, 3 a page of book 901, 4 the deleted book 801, 5 the deleted chapter 3001 of book 701, 6 the deleted
// shelf 302. Book 801 and chapter 3001 no longer answer a read; chapter 2001 and page 555 still do.
func binAreaServer(t *testing.T) *areaServer {
	t.Helper()
	s := newAreaServer(t)
	inner := s.Config.Handler
	page := func(id, book, chapter int, parent map[string]any) map[string]any {
		return map[string]any{"deletable_type": "page", "deletable_id": id, "deletable": map[string]any{
			"name": "N", "book_id": book, "chapter_id": chapter, "parent": parent}}
	}
	entries := []map[string]any{
		page(111, 701, 1001, map[string]any{"id": 1001, "type": "chapter", "name": "P"}),
		page(333, 701, 0, map[string]any{"id": 701, "type": "book", "name": "P"}),
		page(444, 901, 0, map[string]any{"id": 901, "type": "book", "name": "P"}),
		{"deletable_type": "book", "deletable_id": 801, "deletable": map[string]any{"name": "B"}},
		{"deletable_type": "chapter", "deletable_id": 3001, "deletable": map[string]any{"name": "C", "book_id": 701}},
		{"deletable_type": "bookshelf", "deletable_id": 302, "deletable": map[string]any{"name": "S"}},
	}
	for i, e := range entries {
		e["id"], e["deleted_by"], e["created_at"] = i+1, 2, "2026-10-01T10:00:00Z"
	}
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/recycle-bin":
			s.rec.record(r)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": entries, "total": len(entries)})
		case strings.HasPrefix(r.URL.Path, "/api/recycle-bin/"):
			s.rec.record(r)
			_, _ = w.Write([]byte(`{"restore_count":1,"delete_count":1}`))
		default:
			inner.ServeHTTP(w, r)
		}
	})
	return s
}

func TestRecycleBinHonorsExclusions(t *testing.T) {
	s := binAreaServer(t)
	call := func(tool, args string, excludes ...string) (any, error) {
		t.Helper()
		s.requests()
		return s.callExcluding(t, tool, args, nil, excludes)
	}
	mutated := func() bool {
		for _, r := range s.requests() {
			if strings.HasPrefix(r, "PUT ") || strings.HasPrefix(r, "DELETE ") {
				return true
			}
		}
		return false
	}

	// Entries of an excluded chapter, book, page, or shelf leave the list, whatever names them. With an excluded
	// shelf the deleted book 4 goes too: it no longer shows on the shelf and cannot be proven off it.
	got, err := call(recycleBinList.ID, `{}`, "chapter/1001", "book/901", "shelf/302")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{2, 5}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("ids = %v, want %v", resultIDs(t, got), want)
	}
	got, err = call(recycleBinList.ID, `{"limit":2}`, "page/333", "shelf/301")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{3, 6}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("ids = %v, want %v", resultIDs(t, got), want)
	}

	for _, tool := range []string{recycleBinRestore.ID, recycleBinDestroy.ID} {
		for id, exclude := range map[string]string{"1": "chapter/1001", "3": "book/901", "6": "shelf/302", "2": "page/333"} {
			_, err := call(tool, `{"deletion_id":`+id+`}`, exclude)
			refused(t, tool+" "+id, err)
			noExclusionWord(t, tool+" "+id, err)
			if mutated() {
				t.Errorf("%s %s: a change was sent", tool, id)
			}
		}
		// An entry that belongs to nothing excluded still goes through.
		if _, err := call(tool, `{"deletion_id":2}`, "chapter/1001"); err != nil || !mutated() {
			t.Errorf("%s: err = %v", tool, err)
		}
	}

	// The deleted book may hold the excluded chapter or page, unless that still answers elsewhere: chapter 2001
	// lies in book 901 and page 555 in book 701, so neither is below book 801; chapter 8001 does not answer.
	if _, err := call(recycleBinDestroy.ID, `{"deletion_id":4}`, "chapter/2001", "page/555"); err != nil || !mutated() {
		t.Errorf("destroy of a book whose excluded objects live elsewhere: err = %v", err)
	}
	for _, exclude := range []string{"chapter/8001", "page/9999"} {
		_, err := call(recycleBinDestroy.ID, `{"deletion_id":4}`, exclude)
		refused(t, "destroy of a book with "+exclude, err)
		if mutated() {
			t.Errorf("%s: a destroy was sent", exclude)
		}
	}
	// A deleted chapter holds pages only; page 555 sits in chapter 1001, not in the deleted chapter 3001.
	if _, err := call(recycleBinDestroy.ID, `{"deletion_id":5}`, "page/555", "chapter/8001"); err != nil || !mutated() {
		t.Errorf("destroy of a chapter: err = %v", err)
	}
	_, err = call(recycleBinDestroy.ID, `{"deletion_id":5}`, "page/9999")
	refused(t, "destroy of a chapter with an unanswered page", err)

	// An entry the list does not show cannot be proven and is refused.
	_, err = call(recycleBinDestroy.ID, `{"deletion_id":77}`, "page/555")
	refused(t, "unknown entry", err)
	if mutated() {
		t.Error("a destroy was sent for an unknown entry")
	}
	// Without exclusions nothing is read first.
	if _, err := call(recycleBinDestroy.ID, `{"deletion_id":4}`); err != nil {
		t.Fatal(err)
	}
	if got := s.requests(); !reflect.DeepEqual(got, []string{"DELETE /api/recycle-bin/4"}) {
		t.Errorf("requests = %v", got)
	}
}
