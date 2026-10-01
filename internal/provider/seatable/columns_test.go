package seatable

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const columnMetadata = `{"metadata":{"tables":[
 {"_id":"0000","name":"Kunden","columns":[{"key":"0000","name":"Name","type":"text"},
  {"key":"a1","name":"Betrag","type":"number"},{"key":"a2","name":"Notiz","type":"text"},
  {"key":"l1","name":"Tickets","type":"link","data":{"table_id":"0000","other_table_id":"0001","link_id":"lk01"}},
  {"key":"l2","name":"Privat","type":"link","data":{"table_id":"0000","other_table_id":"0002","link_id":"lk02"}}],"views":[]},
 {"_id":"0001","name":"Tickets","columns":[{"key":"0000","name":"Titel","type":"text"}],"views":[]},
 {"_id":"0002","name":"Geheim","columns":[{"key":"0000","name":"Wert","type":"text"}],"views":[]}
]}}`

func serveColumns(t *testing.T, status int, answer string) *[]viewCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]viewCall{}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, columnMetadata), nil
		}
		var body map[string]any
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(data, &body)
		}
		mu.Lock()
		defer mu.Unlock()
		*calls = append(*calls, viewCall{request.Method, request.URL.EscapedPath(), request.URL.RawQuery, body})
		return jsonResponse(status, answer), nil
	})
	return calls
}

func encoded(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestRegisterPublishesColumnToolsWithRisk(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		effect capability.Effect
		allow  bool
	}{
		"seatable.columns.create": {capability.EffectCreate, false},
		"seatable.columns.update": {capability.EffectUpdate, false},
		"seatable.columns.delete": {capability.EffectDelete, true},
	}
	seen := 0
	for _, d := range reg.Provider(Provider) {
		w, ok := want[d.ID]
		if !ok {
			continue
		}
		seen++
		if d.Risk.Effect != w.effect || d.RequiresToolAllowList != w.allow || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity == "" || d.Risk.Idempotency == "" || d.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s = %+v", d.ID, d)
		}
	}
	if seen != 3 {
		t.Fatalf("column tools = %d, want 3", seen)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	if strings.Contains(strings.Join(metadata.Profiles[0].Tools, ","), "columns.create") {
		t.Error("the read profile holds a column change")
	}
}

func TestColumnChangesSendOneFixedRequest(t *testing.T) {
	calls := serveColumns(t, http.StatusOK, `{"key":"zz","name":"x"}`)
	previous := newOptionID
	newOptionID = func() string { return "Ab12Cd" }
	t.Cleanup(func() { newOptionID = previous })
	c := linksClient(t, "*")
	ctx := context.Background()
	steps := []struct {
		name   string
		run    func() error
		method string
		body   string
	}{
		{"text", func() error {
			return c.CreateColumn(ctx, "create", ColumnInput{Table: "Kunden", Name: "Ort", Type: "text"})
		}, http.MethodPost, `{"column_name":"Ort","column_type":"text","table_name":"Kunden"}`},
		{"number after", func() error {
			return c.CreateColumn(ctx, "create", ColumnInput{Table: "Kunden", Name: "Summe", Type: "number", After: "a1",
				Data: &ColumnData{Format: "euro", Decimal: "comma"}})
		}, http.MethodPost, `{"anchor_column":"Betrag","column_data":{"decimal":"comma","format":"euro","thousands":"no"},` +
			`"column_name":"Summe","column_type":"number","table_name":"Kunden"}`},
		{"select", func() error {
			return c.CreateColumn(ctx, "create", ColumnInput{Table: "id:0000", Name: "Status", Type: "single-select",
				Data: &ColumnData{Options: []OptionInput{{Name: "Offen", Color: "#FFE9A8"}}}})
		}, http.MethodPost, `{"column_data":{"options":[{"color":"#FFE9A8","id":"Ab12Cd","name":"Offen"}]},` +
			`"column_name":"Status","column_type":"single-select","table_name":"Kunden"}`},
		{"link", func() error {
			return c.CreateColumn(ctx, "create", ColumnInput{Table: "Kunden", Name: "Neu", Type: "link",
				Data: &ColumnData{LinkTable: "id:0001"}})
		}, http.MethodPost, `{"column_data":{"other_table":"Tickets","table":"Kunden"},"column_name":"Neu",` +
			`"column_type":"link","table_name":"Kunden"}`},
		{"rename", func() error {
			return c.UpdateColumn(ctx, "update", ColumnInput{Table: "Kunden", Column: "a2", Name: "Bemerkung"})
		}, http.MethodPut, `{"column":"Notiz","new_column_name":"Bemerkung","op_type":"rename_column","table_name":"Kunden"}`},
		{"type", func() error {
			return c.UpdateColumn(ctx, "update", ColumnInput{Table: "Kunden", Column: "Notiz", Type: "long-text"})
		}, http.MethodPut, `{"column":"Notiz","new_column_type":"long-text","op_type":"modify_column_type","table_name":"Kunden"}`},
		{"width", func() error {
			return c.UpdateColumn(ctx, "update", ColumnInput{Table: "Kunden", Column: "Notiz", Width: 200})
		}, http.MethodPut, `{"column":"Notiz","new_column_width":200,"op_type":"resize_column","table_name":"Kunden"}`},
		{"move", func() error {
			return c.UpdateColumn(ctx, "update", ColumnInput{Table: "Kunden", Column: "Notiz", Target: "Name"})
		}, http.MethodPut, `{"column":"Notiz","op_type":"move_column","table_name":"Kunden","target_column":"Name"}`},
		{"delete", func() error {
			return c.DeleteColumn(ctx, "delete", ColumnInput{Table: "Kunden", Column: "Betrag"})
		}, http.MethodDelete, `{"column":"Betrag","table_name":"Kunden"}`},
		{"delete allowed link", func() error {
			return c.DeleteColumn(ctx, "delete", ColumnInput{Table: "Kunden", Column: "Tickets"})
		}, http.MethodDelete, `{"column":"Tickets","table_name":"Kunden"}`},
	}
	for i, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("%s = %v", step.name, err)
		}
		got := (*calls)[len(*calls)-1]
		if len(*calls) != i+1 || got.method != step.method || got.path != gatewayPath+salesBase+columnsPath ||
			encoded(got.body) != step.body {
			t.Errorf("%s call = %+v %s", step.name, got, encoded(got.body))
		}
	}
}

func TestNewOptionIDHasTheDocumentedForm(t *testing.T) {
	if !regexp.MustCompile(`^[A-Za-z0-9]{6}$`).MatchString(newOptionID()) {
		t.Error("option id form")
	}
}

func TestColumnChangesRefuseBadRequestsBeforeTheChange(t *testing.T) {
	calls := serveColumns(t, http.StatusOK, `{"success":true}`)
	c := linksClient(t, "Kunden", "Tickets")
	ctx := context.Background()
	create := func(in ColumnInput) func() error {
		in.Table = "Kunden"
		return func() error { return c.CreateColumn(ctx, "create", in) }
	}
	update := func(in ColumnInput) func() error {
		in.Table = "Kunden"
		return func() error { return c.UpdateColumn(ctx, "update", in) }
	}
	remove := func(in ColumnInput) func() error {
		in.Table = "Kunden"
		return func() error { return c.DeleteColumn(ctx, "delete", in) }
	}
	many := make([]OptionInput, maxColumnOptions+1)
	for i := range many {
		many[i] = OptionInput{Name: strings.Repeat("x", i+1)}
	}
	for name, run := range map[string]func() error{
		"unknown type":          create(ColumnInput{Name: "A", Type: "formula"}),
		"button type":           create(ColumnInput{Name: "A", Type: "button"}),
		"no type":               create(ColumnInput{Name: "A"}),
		"existing name":         create(ColumnInput{Name: "Notiz", Type: "text"}),
		"system name":           create(ColumnInput{Name: "_id", Type: "text"}),
		"dotted name":           create(ColumnInput{Name: "a.b", Type: "text"}),
		"padded name":           create(ColumnInput{Name: " a", Type: "text"}),
		"long name":             create(ColumnInput{Name: strings.Repeat("x", 256), Type: "text"}),
		"unknown anchor":        create(ColumnInput{Name: "A", Type: "text", After: "Fehlt"}),
		"system anchor":         create(ColumnInput{Name: "A", Type: "text", After: "_id"}),
		"data on text":          create(ColumnInput{Name: "A", Type: "text", Data: &ColumnData{Format: "euro"}}),
		"bad number format":     create(ColumnInput{Name: "A", Type: "number", Data: &ColumnData{Format: "yen"}}),
		"date field on number":  create(ColumnInput{Name: "A", Type: "number", Data: &ColumnData{DurationFormat: "h:mm"}}),
		"bad rate":              create(ColumnInput{Name: "A", Type: "rate", Data: &ColumnData{RateMax: 11}}),
		"bad option color":      create(ColumnInput{Name: "A", Type: "single-select", Data: &ColumnData{Options: []OptionInput{{Name: "x", Color: "red"}}}}),
		"repeated option":       create(ColumnInput{Name: "A", Type: "single-select", Data: &ColumnData{Options: []OptionInput{{Name: "x"}, {Name: "x"}}}}),
		"too many options":      create(ColumnInput{Name: "A", Type: "single-select", Data: &ColumnData{Options: many}}),
		"link without table":    create(ColumnInput{Name: "A", Type: "link"}),
		"link outside":          create(ColumnInput{Name: "A", Type: "link", Data: &ColumnData{LinkTable: "Geheim"}}),
		"link outside by id":    create(ColumnInput{Name: "A", Type: "link", Data: &ColumnData{LinkTable: "id:0002"}}),
		"link unknown":          create(ColumnInput{Name: "A", Type: "link", Data: &ColumnData{LinkTable: "Nie"}}),
		"create with column":    create(ColumnInput{Name: "A", Type: "text", Column: "Notiz"}),
		"update nothing":        update(ColumnInput{Column: "Notiz"}),
		"update two":            update(ColumnInput{Column: "Notiz", Name: "B", Width: 100}),
		"update system column":  update(ColumnInput{Column: "_ctime", Name: "B"}),
		"update unknown column": update(ColumnInput{Column: "Fehlt", Name: "B"}),
		"rename to existing":    update(ColumnInput{Column: "Notiz", Name: "Name"}),
		"rename to same":        update(ColumnInput{Column: "Notiz", Name: "Notiz"}),
		"rename to system":      update(ColumnInput{Column: "Notiz", Name: "_mtime"}),
		"same type":             update(ColumnInput{Column: "Notiz", Type: "text"}),
		"formula type":          update(ColumnInput{Column: "Notiz", Type: "formula"}),
		"data without type":     update(ColumnInput{Column: "Notiz", Width: 100, Data: &ColumnData{Format: "euro"}}),
		"width too small":       update(ColumnInput{Column: "Notiz", Width: 10}),
		"width too large":       update(ColumnInput{Column: "Notiz", Width: 5000}),
		"move onto itself":      update(ColumnInput{Column: "Notiz", Target: "a2"}),
		"move to unknown":       update(ColumnInput{Column: "Notiz", Target: "Fehlt"}),
		"move to system":        update(ColumnInput{Column: "Notiz", Target: "_id"}),
		"to link outside":       update(ColumnInput{Column: "Notiz", Type: "link", Data: &ColumnData{LinkTable: "Geheim"}}),
		"rename foreign link":   update(ColumnInput{Column: "Privat", Name: "Z"}),
		"retype foreign link":   update(ColumnInput{Column: "l2", Type: "text"}),
		"delete foreign link":   remove(ColumnInput{Column: "Privat"}),
		"delete system column":  remove(ColumnInput{Column: "_id"}),
		"delete unknown":        remove(ColumnInput{Column: "Fehlt"}),
		"delete with name":      remove(ColumnInput{Column: "Notiz", Name: "x"}),
		"other table":           func() error { return c.DeleteColumn(ctx, "delete", ColumnInput{Table: "Geheim", Column: "Wert"}) },
		"view narrowed table": func() error {
			return c.DeleteColumn(ctx, "delete", ColumnInput{Table: "Kunden/Standard", Column: "Notiz"})
		},
	} {
		if err := run(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, call := range *calls {
		t.Errorf("request sent: %+v", call)
	}
}

func TestColumnMutationsNeverRetryAndReportUncertainty(t *testing.T) {
	in := ColumnInput{Table: "Kunden", Column: "Notiz"}
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway} {
		calls := serveColumns(t, status, `{"detail":"`+providerCanary+`"}`)
		err := linksClient(t, "*").DeleteColumn(context.Background(), "delete", in)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), providerCanary) {
			t.Errorf("status %d error = %v", status, err)
		}
		if len(*calls) != 1 {
			t.Errorf("status %d calls = %d, want 1", status, len(*calls))
		}
	}
	serveColumns(t, http.StatusOK, `{"success":false}`)
	if err := linksClient(t, "*").DeleteColumn(context.Background(), "delete", in); err == nil {
		t.Error("an unsuccessful answer was accepted")
	}
}

func TestColumnRequestsOutsideTheAllowListAreRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
	handlers := map[string]capability.Handler{
		"create": invokeColumnsChange("create", "create", (*Client).CreateColumn, "created"),
		"update": invokeColumnsChange("update", "update", (*Client).UpdateColumn, "updated"),
		"delete": invokeColumnsChange("delete", "delete", (*Client).DeleteColumn, "deleted"),
	}
	for kind, handler := range handlers {
		for _, table := range []string{"Tickets", "id:0001", "Kunden/Standard", ""} {
			raw, _ := json.Marshal(map[string]any{"table": table, "column": "Notiz", "name": "Neu", "type": "text"})
			if kind == "create" {
				raw, _ = json.Marshal(map[string]any{"table": table, "name": "Neu", "type": "text"})
			}
			if kind == "delete" {
				raw, _ = json.Marshal(map[string]any{"table": table, "column": "Notiz"})
			}
			_, err := handler(context.Background(), resolved, secrets, &redact.Redactor{}, raw)
			if err == nil || strings.Contains(err.Error(), "Tickets") || strings.Contains(err.Error(), "0001") {
				t.Errorf("%s %q = %v", kind, table, err)
			}
		}
		// A malformed request on an allowed table is refused before the credential as well.
		bad, _ := json.Marshal(map[string]any{"table": "Kunden", "column": "_id", "name": "_id", "type": "formula"})
		if _, err := handler(context.Background(), resolved, secrets, &redact.Redactor{}, bad); err == nil {
			t.Errorf("%s accepted a malformed request", kind)
		}
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d", lookups)
	}
}

func TestColumnChangeRefusalNamesNoForeignTarget(t *testing.T) {
	serveColumns(t, http.StatusOK, `{"success":true}`)
	err := linksClient(t, "Kunden", "Tickets").CreateColumn(context.Background(), "create", ColumnInput{
		Table: "Kunden", Name: "A", Type: "link", Data: &ColumnData{LinkTable: "Geheim"}})
	if err == nil || strings.Contains(err.Error(), "Geheim") || strings.Contains(err.Error(), "0002") {
		t.Errorf("error = %v", err)
	}
	err = linksClient(t, "Kunden", "Tickets").DeleteColumn(context.Background(), "delete", ColumnInput{Table: "Kunden", Column: "Privat"})
	if err == nil || strings.Contains(err.Error(), "Geheim") || strings.Contains(err.Error(), "0002") {
		t.Errorf("error = %v", err)
	}
}
