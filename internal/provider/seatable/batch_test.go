package seatable

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

type batchCall struct {
	method, path string
	raw          string
}

// serveBatch answers the link fixture metadata and records every non-metadata request.
func serveBatch(t *testing.T, status int, answer string) *[]batchCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]batchCall{}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, linkMetadata), nil
		}
		data, _ := io.ReadAll(request.Body)
		mu.Lock()
		*calls = append(*calls, batchCall{request.Method, request.URL.Path, string(data)})
		mu.Unlock()
		return jsonResponse(status, answer), nil
	})
	return calls
}

func idN(i int) string {
	return rowID[:18] + strconv.Itoa(1000+i)
}

func TestRegisterPublishesBatchToolsWithRisk(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		effect capability.Effect
		allow  bool
	}{
		"seatable.rows.batchcreate": {capability.EffectCreate, false}, "seatable.rows.batchupdate": {capability.EffectUpdate, false},
		"seatable.rows.batchdelete": {capability.EffectDelete, true}, "seatable.snapshots.create": {capability.EffectCreate, false},
	}
	seen := 0
	for _, d := range reg.Provider(Provider) {
		w, ok := want[d.ID]
		if !ok {
			continue
		}
		seen++
		if d.Risk.Effect != w.effect || d.RequiresToolAllowList != w.allow || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != dataSensitivity || d.Risk.Idempotency == "" ||
			d.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s = %+v", d.ID, d)
		}
	}
	if seen != 4 {
		t.Fatalf("tools = %d, want 4", seen)
	}
}

func TestBatchRequestBodiesAndReportedCounts(t *testing.T) {
	ctx := context.Background()
	calls := serveBatch(t, http.StatusOK, `{"inserted_row_count":2,"row_ids":[{"_id":"x"}]}`)
	c := linksClient(t, "Kunden")
	created, err := c.BatchCreateRows(ctx, "", []map[string]json.RawMessage{
		{"Name": json.RawMessage(`"a"`)}, {"Name": json.RawMessage(`"b"`)}})
	if err != nil || created.Requested != 2 || created.Reported == nil || *created.Reported != 2 {
		t.Fatalf("create = %+v, %v", created, err)
	}
	serveBatch(t, http.StatusOK, `{"success":true}`)
	updated, err := linksClient(t, "Kunden").BatchUpdateRows(ctx, "", []BatchUpdate{
		{RowID: rowID, Values: map[string]json.RawMessage{"Name": json.RawMessage(`"a"`)}},
		{RowID: otherRowID, Values: map[string]json.RawMessage{"Name": json.RawMessage(`"b"`)}}})
	if err != nil || updated.Requested != 2 || updated.Reported != nil {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	serveBatch(t, http.StatusOK, `{"deleted_rows":1,"not_exist_row_ids":[]}`)
	deleted, err := linksClient(t, "Kunden").BatchDeleteRows(ctx, "", []string{rowID, otherRowID})
	if err != nil || deleted.Requested != 2 || deleted.Reported == nil || *deleted.Reported != 1 {
		t.Fatalf("delete = %+v, %v", deleted, err)
	}

	if len(*calls) != 1 || (*calls)[0].method != http.MethodPost || (*calls)[0].path != rowsRoute(salesBase) ||
		(*calls)[0].raw != `{"rows":[{"Name":"a"},{"Name":"b"}],"table_name":"Kunden"}` {
		t.Errorf("create call = %+v", *calls)
	}
}

func TestBatchUpdateAndDeleteBodies(t *testing.T) {
	ctx := context.Background()
	calls := serveBatch(t, http.StatusOK, `{"success":true}`)
	c := linksClient(t, "Kunden")
	if _, err := c.BatchUpdateRows(ctx, "", []BatchUpdate{
		{RowID: rowID, Values: map[string]json.RawMessage{"Name": json.RawMessage(`"a"`)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BatchDeleteRows(ctx, "", []string{rowID, otherRowID}); err != nil {
		t.Fatal(err)
	}
	want := []batchCall{
		{http.MethodPut, rowsRoute(salesBase), `{"table_name":"Kunden","updates":[{"row":{"Name":"a"},"row_id":"` + rowID + `"}]}`},
		{http.MethodDelete, rowsRoute(salesBase), `{"row_ids":["` + rowID + `","` + otherRowID + `"],"table_name":"Kunden"}`},
	}
	if len(*calls) != 2 || (*calls)[0] != want[0] || (*calls)[1] != want[1] {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestBatchLimitsApplyBeforeAnyIO(t *testing.T) {
	refuse(t)
	ctx := context.Background()
	c := linksClient(t, "Kunden")
	row := map[string]json.RawMessage{"Name": json.RawMessage(`"a"`)}
	tooMany := make([]map[string]json.RawMessage, maxBatchRows+1)
	updates := make([]BatchUpdate, maxBatchRows+1)
	ids := make([]string, maxBatchRows+1)
	for i := range tooMany {
		tooMany[i] = row
		ids[i] = idN(i)
		updates[i] = BatchUpdate{RowID: ids[i], Values: row}
	}
	if _, err := c.BatchCreateRows(ctx, "", tooMany); err == nil {
		t.Error("101 created rows accepted")
	}
	if _, err := c.BatchCreateRows(ctx, "", nil); err == nil {
		t.Error("an empty batch was accepted")
	}
	if _, err := c.BatchUpdateRows(ctx, "", updates); err == nil {
		t.Error("101 updates accepted")
	}
	if _, err := c.BatchDeleteRows(ctx, "", ids); err == nil {
		t.Error("101 deletes accepted")
	}
	if _, err := c.BatchDeleteRows(ctx, "", []string{rowID, rowID}); err == nil {
		t.Error("duplicate delete ids accepted")
	}
	if _, err := c.BatchUpdateRows(ctx, "", []BatchUpdate{{RowID: rowID, Values: row}, {RowID: rowID, Values: row}}); err == nil {
		t.Error("duplicate update ids accepted")
	}
	if _, err := c.BatchDeleteRows(ctx, "", []string{"../x"}); err == nil {
		t.Error("a bad row id was accepted")
	}

	// Handlers refuse an oversized call before the secret lookup.
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
	big := `{"rows":[{"Name":"` + strings.Repeat("x", maxRequestBytes) + `"}]}`
	for name, handler := range map[string]capability.Handler{
		"create": invokeRowsBatchCreate, "update": invokeRowsBatchUpdate, "delete": invokeRowsBatchDelete,
	} {
		for _, raw := range []string{big, `{"rows":[],"row_ids":[]}`} {
			if _, err := handler(ctx, resolved, secrets, &redact.Redactor{}, json.RawMessage(raw)); err == nil {
				t.Errorf("%s accepted %.20s", name, raw)
			}
		}
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d, want 0", lookups)
	}

	// A body that only exceeds the limit once the table name is added is refused before the request.
	calls := serveBatch(t, http.StatusOK, `{}`)
	huge := map[string]json.RawMessage{"Name": json.RawMessage(`"` + strings.Repeat("x", maxRequestBytes) + `"`)}
	if _, err := linksClient(t, "Kunden").BatchCreateRows(ctx, "", []map[string]json.RawMessage{huge}); err == nil {
		t.Error("an oversized body was accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v", *calls)
	}
}

func TestBatchColumnsAreCheckedAndSystemAndLinkColumnsRefused(t *testing.T) {
	ctx := context.Background()
	calls := serveBatch(t, http.StatusOK, `{}`)
	c := linksClient(t, "Kunden")
	ok := map[string]json.RawMessage{"Name": json.RawMessage(`"a"`)}
	for name, row := range map[string]map[string]json.RawMessage{
		"unknown": {"Gibtsnicht": json.RawMessage(`1`)}, "system": {"_id": json.RawMessage(`"x"`)},
		"link": {"Vorgang": json.RawMessage(`["` + otherRowID + `"]`)}, "empty": {},
	} {
		if _, err := c.BatchCreateRows(ctx, "", []map[string]json.RawMessage{ok, row}); err == nil {
			t.Errorf("create accepted the %s column", name)
		}
		if _, err := c.BatchUpdateRows(ctx, "", []BatchUpdate{{RowID: rowID, Values: row}}); err == nil {
			t.Errorf("update accepted the %s column", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v, want none", *calls)
	}
}

func TestBatchTableOutsideTheAllowListIsRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
	for name, tt := range map[string]struct {
		handler capability.Handler
		raw     string
	}{
		"create": {invokeRowsBatchCreate, `{"table":"Tickets","rows":[{"Name":"a"}]}`},
		"update": {invokeRowsBatchUpdate, `{"table":"Tickets","rows":[{"row_id":"` + rowID + `","values":{"Name":"a"}}]}`},
		"delete": {invokeRowsBatchDelete, `{"table":"Tickets","row_ids":["` + rowID + `"]}`},
	} {
		_, err := tt.handler(context.Background(), resolved, secrets, &redact.Redactor{}, json.RawMessage(tt.raw))
		if err == nil {
			t.Errorf("%s accepted a table outside the allow-list", name)
		} else if strings.Contains(err.Error(), "Tickets") {
			t.Errorf("%s names the foreign table: %v", name, err)
		}
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d, want 0", lookups)
	}
}

func TestBatchChangesSendOneRequestAndReportUncertainty(t *testing.T) {
	ctx := context.Background()
	row := []map[string]json.RawMessage{{"Name": json.RawMessage(`"a"`)}}
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		uncertain bool
	}{
		{"server error", http.StatusInternalServerError, `{"error_msg":"` + providerCanary + `"}`, true},
		{"gateway timeout", http.StatusGatewayTimeout, ``, true},
		{"unreadable answer", http.StatusOK, `not json`, true},
		{"rejected", http.StatusBadRequest, `{"error_msg":"` + providerCanary + `"}`, false},
		{"not applied", http.StatusOK, `{"success":false,"error_msg":"` + providerCanary + `"}`, false},
	} {
		calls := serveBatch(t, tt.status, tt.body)
		_, err := linksClient(t, "Kunden").BatchCreateRows(ctx, "", row)
		if err == nil {
			t.Fatalf("%s: no error", tt.name)
		}
		if len(*calls) != 1 {
			t.Errorf("%s: requests = %d, want exactly one", tt.name, len(*calls))
		}
		if strings.Contains(err.Error(), providerCanary) {
			t.Errorf("%s: provider text in the error: %v", tt.name, err)
		}
		if got := strings.Contains(err.Error(), "may have taken effect"); got != tt.uncertain {
			t.Errorf("%s: uncertainty = %v, want %v (%v)", tt.name, got, tt.uncertain, err)
		}
	}
	for _, failure := range []error{context.DeadlineExceeded, errors.New("connection reset by peer")} {
		requests := 0
		serveBase(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == metaRoute(salesBase) {
				return jsonResponse(http.StatusOK, linkMetadata), nil
			}
			requests++
			return nil, &url.Error{Op: "Post", URL: "https://x.invalid", Err: failure}
		})
		_, err := linksClient(t, "Kunden").BatchDeleteRows(ctx, "", []string{rowID})
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || requests != 1 {
			t.Errorf("%v: err = %v, requests = %d", failure, err, requests)
		}
	}
}

func TestSnapshotSendsOneRequestAndClassifiesTheRefusal(t *testing.T) {
	ctx := context.Background()
	calls := serveBatch(t, http.StatusOK, `{"status":"created","snapshot":{"commit_id":"x"}}`)
	if err := linksClient(t, "Kunden").CreateSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodPost ||
		(*calls)[0].path != gatewayPath+salesBase+snapshotPath || (*calls)[0].raw != `{"dtable_name":"Sales"}` {
		t.Errorf("calls = %+v", *calls)
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusConflict} {
		calls := serveBatch(t, status, `{"error_msg":"`+providerCanary+`"}`)
		err := linksClient(t, "Kunden").CreateSnapshot(ctx)
		if err == nil || !strings.Contains(err.Error(), "10 minutes") || strings.Contains(err.Error(), providerCanary) ||
			strings.Contains(err.Error(), "may have been created") || len(*calls) != 1 {
			t.Errorf("status %d: err = %v, calls = %d", status, err, len(*calls))
		}
	}
	calls = serveBatch(t, http.StatusForbidden, `{}`)
	if err := linksClient(t, "Kunden").CreateSnapshot(ctx); classOf(err) != provider.ClassPermission || len(*calls) != 1 {
		t.Errorf("forbidden = %v", err)
	}
	calls = serveBatch(t, http.StatusInternalServerError, `{}`)
	if err := linksClient(t, "Kunden").CreateSnapshot(ctx); err == nil || !strings.Contains(err.Error(), "may have been created") || len(*calls) != 1 {
		t.Errorf("server error = %v", err)
	}
	requests := 0
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		requests++
		return nil, &url.Error{Op: "Post", URL: "https://x.invalid", Err: context.DeadlineExceeded}
	})
	if err := linksClient(t, "Kunden").CreateSnapshot(ctx); err == nil || !strings.Contains(err.Error(), "may have been created") || requests != 1 {
		t.Errorf("timeout = %v, requests = %d", err, requests)
	}
}

func TestBatchAndSnapshotNeedConfirmationPermissionsAndTheToolList(t *testing.T) {
	calls := serveBatch(t, http.StatusOK, `{"inserted_row_count":1}`)
	stubLimiter(t, salesToken)
	cfg := coreConfig()
	cfg.Connections["batch"] = config.Connection{Service: "sea-cloud", Credential: "sales-reader", Targets: []string{"*"},
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete},
		Tools:       []string{"seatable.rows.batchcreate", "seatable.rows.batchdelete", "seatable.snapshots.create"}}
	cfg.Connections["batch-read"] = config.Connection{Service: "sea-cloud", Credential: "sales-reader", Targets: []string{"*"}}
	cfg.Connections["batch-all"] = config.Connection{Service: "sea-cloud", Credential: "sales-reader", Targets: []string{"*"},
		Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}}
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	create := json.RawMessage(`{"table":"Kunden","rows":[{"Name":"a"}]}`)
	remove := json.RawMessage(`{"table":"Kunden","row_ids":["` + rowID + `"]}`)

	for _, request := range []application.InvokeRequest{
		{Operation: "seatable.rows.batchcreate", Connection: "batch", Arguments: create},
		{Operation: "seatable.rows.batchdelete", Connection: "batch", Arguments: remove},
		{Operation: "seatable.snapshots.create", Connection: "batch", Arguments: json.RawMessage(`{}`)},
	} {
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirm", request.Operation)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("requests without confirm = %v", *calls)
	}
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "seatable.rows.batchcreate", Connection: "batch", Arguments: create, Confirmed: true})
	if err != nil || string(response.Result) != `{"reported":1,"requested":1}` {
		t.Fatalf("create = %s, %v", response.Result, err)
	}
	// batch delete is offered only through the tool list, and a read connection offers no mutation.
	for _, request := range []application.InvokeRequest{
		{Operation: "seatable.rows.batchdelete", Connection: "batch-all", Arguments: remove, Confirmed: true},
		{Operation: "seatable.rows.batchupdate", Connection: "batch", Arguments: json.RawMessage(`{"table":"Kunden","rows":[{"row_id":"` + rowID + `","values":{"Name":"a"}}]}`), Confirmed: true},
		{Operation: "seatable.rows.batchcreate", Connection: "batch-read", Arguments: create, Confirmed: true},
		{Operation: "seatable.snapshots.create", Connection: "batch-read", Arguments: json.RawMessage(`{}`), Confirmed: true},
		{Operation: "seatable.rows.batchcreate", Connection: "batch", Confirmed: true,
			Arguments: json.RawMessage(`{"table":"Kunden","rows":[]}`)},
	} {
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran on %s", request.Operation, request.Connection)
		}
	}
	if len(*calls) != 1 {
		t.Errorf("requests = %d, want 1", len(*calls))
	}
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "seatable.snapshots.create", Connection: "batch", Arguments: json.RawMessage(`{}`), Confirmed: true}); err != nil {
		t.Errorf("snapshot = %v", err)
	}
}
