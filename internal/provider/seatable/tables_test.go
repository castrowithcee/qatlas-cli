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

type tableCall struct {
	method, path string
	body         map[string]any
}

// serveTables answers the metadata read with the view fixture and records every other request.
func serveTables(t *testing.T, status int, answer string) (*[]tableCall, *int) {
	t.Helper()
	var mu sync.Mutex
	calls := &[]tableCall{}
	metadataReads := new(int)
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if request.URL.Path == metaRoute(salesBase) {
			*metadataReads++
			return jsonResponse(http.StatusOK, viewMetadata), nil
		}
		var body map[string]any
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(data, &body)
		}
		*calls = append(*calls, tableCall{request.Method, request.URL.EscapedPath(), body})
		return jsonResponse(status, answer), nil
	})
	return calls, metadataReads
}

func tableCtx() context.Context { return context.Background() }

func TestRegisterPublishesTableToolsWithRisk(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		allow       bool
	}{
		"seatable.tables.create":    {capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		"seatable.tables.rename":    {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		"seatable.tables.duplicate": {capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		"seatable.tables.delete":    {capability.EffectDelete, capability.IdempotencyIdempotent, true},
	}
	seen := 0
	for _, d := range reg.Provider(Provider) {
		w, ok := want[d.ID]
		if !ok {
			continue
		}
		seen++
		if d.Risk.Effect != w.effect || d.Risk.Idempotency != w.idempotency || d.RequiresToolAllowList != w.allow ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s = %+v", d.ID, d)
		}
	}
	if seen != 4 {
		t.Fatalf("table mutation tools = %d, want 4", seen)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	if profile := strings.Join(metadata.Profiles[0].Tools, ","); strings.Contains(profile, "seatable.tables.c") ||
		strings.Contains(profile, "tables.rename") || strings.Contains(profile, "tables.delete") ||
		strings.Contains(profile, "tables.duplicate") {
		t.Errorf("the read profile holds a table change: %s", profile)
	}
}

func TestTableChangesSendOneFixedRequestAndDropTheMetadata(t *testing.T) {
	calls, reads := serveTables(t, http.StatusOK, `{"success":true}`)
	c := linksClient(t, "*")
	steps := []struct {
		run    func() error
		method string
		suffix string
		body   string
	}{
		{func() error { return c.CreateTable(tableCtx(), "create table", TableInput{Name: "Angebote"}) },
			http.MethodPost, "/tables/", `{"table_name":"Angebote"}`},
		{func() error {
			return c.RenameTable(tableCtx(), "rename table", TableInput{Table: "id:0001", Name: "Faelle"})
		},
			http.MethodPut, "/tables/", `{"new_table_name":"Faelle","table_name":"Tickets"}`},
		{func() error {
			return c.DuplicateTable(tableCtx(), "duplicate table", TableInput{Table: "Kunden", WithRows: true})
		}, http.MethodPost, "/tables/duplicate-table/", `{"is_duplicate_records":true,"table_name":"Kunden"}`},
		{func() error { return c.DeleteTable(tableCtx(), "delete table", TableInput{Table: "id:0001"}) },
			http.MethodDelete, "/tables/", `{"table_name":"Tickets"}`},
	}
	for i, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("step %d = %v", i, err)
		}
		got := (*calls)[len(*calls)-1]
		encoded, _ := json.Marshal(got.body)
		if len(*calls) != i+1 || got.method != step.method || got.path != gatewayPath+salesBase+step.suffix ||
			string(encoded) != step.body {
			t.Errorf("step %d call = %+v (%d calls)", i, got, len(*calls))
		}
	}
	// The metadata is read again after a change, never served from the cache of before it.
	before := *reads
	if _, err := c.metadata(tableCtx(), "list"); err != nil || *reads != before+1 {
		t.Errorf("metadata after a change: reads %d -> %d, %v", before, *reads, err)
	}
}

func TestTableCreateAndDuplicateNeedTheWildcard(t *testing.T) {
	refuse(t)
	for _, targets := range [][]string{{"Kunden"}, {"id:0000", "id:0001"}} {
		c := linksClient(t, targets...)
		if err := c.CreateTable(tableCtx(), "create table", TableInput{Name: "Neu"}); err == nil {
			t.Errorf("create on %v was accepted", targets)
		}
		if err := c.DuplicateTable(tableCtx(), "duplicate table", TableInput{Table: targets[0]}); err == nil {
			t.Errorf("duplicate on %v was accepted", targets)
		}
	}
}

func TestTableRenameKeepsTheBoundaryOfTheAllowList(t *testing.T) {
	refuse(t)
	rename := func(targets []string, table, name string) error {
		return linksClient(t, targets...).RenameTable(tableCtx(), "rename table", TableInput{Table: table, Name: name})
	}
	if err := rename([]string{"Kunden"}, "Kunden", "Neu"); err == nil {
		t.Error("a name-bound target was renamed")
	}
	// The new name may not be claimed by an allow-list entry.
	if err := rename([]string{"id:0001", "Ghost"}, "id:0001", "Ghost"); err == nil {
		t.Error("a rename onto a target name was accepted")
	}
	// A table outside the allow-list, or narrowed to a view, is refused before any I/O.
	if err := rename([]string{"id:0001"}, "id:0000", "Neu"); err == nil {
		t.Error("a foreign table was renamed")
	}
	if err := rename([]string{"id:0001/id:0000"}, "id:0001/id:0000", "Neu"); err == nil {
		t.Error("a view-narrowed table was renamed")
	}
	for _, name := range []string{"", "a/b", " a", "id:0002", "*", strings.Repeat("x", 256), "a\nb"} {
		if err := rename([]string{"*"}, "id:0001", name); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}
}

func TestTableRenameByIdentifierOnAnAllowListAndAgainstMetadata(t *testing.T) {
	calls, _ := serveTables(t, http.StatusOK, `{"success":true}`)
	if err := linksClient(t, "id:0001").RenameTable(tableCtx(), "rename table", TableInput{Table: "id:0001", Name: "Faelle"}); err != nil || len(*calls) != 1 {
		t.Fatalf("rename = %v, %d calls", err, len(*calls))
	}
	// The same table is also bound by name through a second entry, which only the metadata reveals.
	*calls = nil
	err := linksClient(t, "id:0001", "Tickets").RenameTable(tableCtx(), "rename table", TableInput{Table: "id:0001", Name: "Faelle"})
	if err == nil || len(*calls) != 0 {
		t.Errorf("name-bound through the metadata: %v, %d calls", err, len(*calls))
	}
	err = linksClient(t, "*").RenameTable(tableCtx(), "rename table", TableInput{Table: "Tickets", Name: "Tickets"})
	if err == nil || len(*calls) != 0 {
		t.Errorf("same name: %v, %d calls", err, len(*calls))
	}
}

func TestTableDeleteAndDuplicateStayInsideTheTableBoundary(t *testing.T) {
	calls, _ := serveTables(t, http.StatusOK, `{"success":true}`)
	if err := linksClient(t, "Tickets").DeleteTable(tableCtx(), "delete table", TableInput{Table: "Tickets"}); err != nil || len(*calls) != 1 {
		t.Fatalf("delete of an allowed table = %v, %d calls", err, len(*calls))
	}
	*calls = nil
	for _, in := range []TableInput{{Table: "Kunden"}, {Table: "Tickets/Standard"}, {Table: "id:9999"}, {}} {
		if err := linksClient(t, "Tickets").DeleteTable(tableCtx(), "delete table", in); err == nil {
			t.Errorf("delete %+v was accepted", in)
		}
	}
	// A wildcard selection of a table that does not exist asks the metadata and is refused.
	if err := linksClient(t, "*").DeleteTable(tableCtx(), "delete table", TableInput{Table: "id:9999"}); err == nil {
		t.Error("an unknown table was deleted")
	}
	if err := linksClient(t, "*").DuplicateTable(tableCtx(), "duplicate table", TableInput{Table: "Kunden/Aktive"}); err == nil {
		t.Error("a view-narrowed table was duplicated")
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %d, want 0", len(*calls))
	}
}

func TestTableMutationsNeverRetryAndReportUncertainty(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway} {
		calls, _ := serveTables(t, status, `{"detail":"`+providerCanary+`"}`)
		err := linksClient(t, "*").DeleteTable(tableCtx(), "delete table", TableInput{Table: "Kunden"})
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), providerCanary) {
			t.Errorf("status %d error = %v", status, err)
		}
		if len(*calls) != 1 {
			t.Errorf("status %d calls = %d, want 1", status, len(*calls))
		}
	}
	serveTables(t, http.StatusOK, `{"success":false}`)
	if err := linksClient(t, "*").CreateTable(tableCtx(), "create table", TableInput{Name: "N"}); err == nil {
		t.Error("an unsuccessful answer was accepted")
	}
	serveTables(t, http.StatusOK, `not json`)
	if err := linksClient(t, "*").CreateTable(tableCtx(), "create table", TableInput{Name: "N"}); err == nil ||
		!strings.Contains(err.Error(), "may have taken effect") {
		t.Errorf("unreadable answer = %v", err)
	}
	serveTables(t, http.StatusNotFound, `{"detail":"`+providerCanary+`"}`)
	err := linksClient(t, "*").CreateTable(tableCtx(), "create table", TableInput{Name: "N"})
	if classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), providerCanary) {
		t.Errorf("404 = %v", err)
	}
}

func TestTableRequestsOutsideTheBoundaryAreRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	handlers := map[string]capability.Handler{
		"create":    invokeTablesChange("create table", "create", (*Client).CreateTable, "created"),
		"rename":    invokeTablesChange("rename table", "rename", (*Client).RenameTable, "renamed"),
		"duplicate": invokeTablesChange("duplicate table", "duplicate", (*Client).DuplicateTable, "duplicated"),
		"delete":    invokeTablesChange("delete table", "delete", (*Client).DeleteTable, "deleted"),
	}
	cases := map[string]string{
		"create": `{"name":"Geheim"}`, "rename": `{"table":"Geheim","name":"Neu"}`,
		"duplicate": `{"table":"Geheim"}`, "delete": `{"table":"Geheim"}`,
	}
	for id, handler := range handlers {
		for _, target := range []string{"Kunden", "id:0000"} {
			resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, target)
			_, err := handler(tableCtx(), resolved, secrets, &redact.Redactor{}, json.RawMessage(cases[id]))
			if err == nil || strings.Contains(err.Error(), "Geheim") {
				t.Errorf("%s on %s = %v", id, target, err)
			}
		}
	}
	wild := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "*")
	for id, raw := range map[string]string{"create": `{"name":"a/b"}`, "rename": `{"table":"Kunden","name":"*"}`,
		"duplicate": `{"table":""}`, "delete": `{"table":""}`} {
		if _, err := handlers[id](tableCtx(), wild, secrets, &redact.Redactor{}, json.RawMessage(raw)); err == nil {
			t.Errorf("%s accepted %s", id, raw)
		}
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d, want 0", lookups)
	}
}
