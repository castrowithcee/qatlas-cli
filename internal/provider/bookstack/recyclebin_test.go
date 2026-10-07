package bookstack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const recycleBinEntry = `{"id":4,"deleted_by":2,"created_at":"2026-10-01T10:00:00Z","updated_at":"x","deletable_type":"page","deletable_id":9,` +
	`"deletable":{"id":9,"name":"Gone","slug":"gone","book_id":3,"chapter_id":5,"description":"SECRET","html":"<p>body</p>","pages_count":0,` +
	`"parent":{"id":5,"name":"Parent","slug":"parent","type":"chapter","description":"SECRET"}}}`

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
		if strings.Contains(d.ID, ".recyclebin.") {
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
	got, err := newClient(t, server.URL, nil).ListRecycleBin(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %v", got.Rows)
	}
	row := got.Rows[0]
	want := map[string]any{"id": int64(4), "deleted_by": int64(2), "created_at": "2026-10-01T10:00:00Z", "deletable_type": "page",
		"deletable_id": int64(9), "name": "Gone", "slug": "gone", "book_id": int64(3), "chapter_id": int64(5),
		"pages_count": int64(0), "parent_type": "chapter", "parent_id": int64(5)}
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
		timeout time.Duration
	}{
		"5xx": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, 0},
		"timeout": {func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}, 500 * time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				tt.handler(w, r)
			}))
			defer server.Close()
			c := newClient(t, server.URL, nil)
			for _, call := range []func(context.Context) error{
				func(ctx context.Context) error { _, err := c.RestoreFromRecycleBin(ctx, 4); return err },
				func(ctx context.Context) error { _, err := c.DestroyFromRecycleBin(ctx, 4); return err },
			} {
				ctx := context.Background()
				if tt.timeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tt.timeout)
					defer cancel()
				}
				if err := call(ctx); err == nil || !strings.Contains(err.Error(), hint) {
					t.Errorf("err = %v, want uncertainty hint", err)
				}
			}
			if n := len(requests(rec)); n != 2 {
				t.Errorf("requests = %d, want 2 (no retry)", n)
			}
		})
	}
}
