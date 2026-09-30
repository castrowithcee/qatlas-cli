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
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const optionMetadata = `{"metadata":{"tables":[
 {"_id":"0000","name":"Kunden","columns":[{"key":"0000","name":"Name","type":"text"},
  {"key":"s1","name":"Status","type":"single-select","data":{"options":[{"name":"Offen"},{"name":"Zu"}]}},
  {"key":"s2","name":"Tags","type":"multiple-select","data":{"options":[{"name":"a"}]}}],"views":[]},
 {"_id":"0001","name":"Tickets","columns":[{"key":"0000","name":"Titel","type":"text"}],"views":[]}
]}}`

func serveOptions(t *testing.T, status int, answer string) *[]viewCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]viewCall{}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, optionMetadata), nil
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

func TestRegisterPublishesOptionToolsWithRisk(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		effect capability.Effect
		allow  bool
	}{
		"seatable.columns.optionsadd":    {capability.EffectCreate, false},
		"seatable.columns.optionsupdate": {capability.EffectUpdate, false},
		"seatable.columns.optionsdelete": {capability.EffectDelete, true},
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
		t.Fatalf("option tools = %d, want 3", seen)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	if strings.Contains(strings.Join(metadata.Profiles[0].Tools, ","), "options") {
		t.Error("the read profile holds an option change")
	}
}

func TestOptionChangesSendOneFixedRequest(t *testing.T) {
	calls := serveOptions(t, http.StatusOK, `{"success":true}`)
	c := linksClient(t, "*")
	ctx := context.Background()
	steps := []struct {
		run    func() error
		method string
		body   string
	}{
		{func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Kunden", Column: "Status",
				Options: []OptionInput{{Name: "Neu", Color: "#FFE9A8", TextColor: "#000000"}}})
		}, http.MethodPost, `{"column":"Status","options":[{"color":"#FFE9A8","name":"Neu","textColor":"#000000"}],"table_name":"Kunden"}`},
		{func() error {
			return c.UpdateOptions(ctx, "update", OptionsInput{Table: "id:0000", Column: "s1",
				Options: []OptionInput{{Name: "Offen", NewName: "Auf"}}})
		}, http.MethodPut, `{"column":"Status","options":[{"name":"Auf","old_name":"Offen"}],"table_name":"Kunden"}`},
		{func() error {
			return c.DeleteOptions(ctx, "delete", OptionsInput{Table: "Kunden", Column: "Tags", Names: []string{"a"}})
		}, http.MethodDelete, `{"column":"Tags","options":["a"],"table_name":"Kunden"}`},
	}
	for i, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("step %d = %v", i, err)
		}
		got := (*calls)[len(*calls)-1]
		encoded, _ := json.Marshal(got.body)
		if len(*calls) != i+1 || got.method != step.method || got.path != gatewayPath+salesBase+optionsPath ||
			string(encoded) != step.body {
			t.Errorf("step %d call = %+v %s", i, got, encoded)
		}
	}
}

func TestOptionChangesRefuseBadColumnsAndShapesBeforeTheChange(t *testing.T) {
	calls := serveOptions(t, http.StatusOK, `{"success":true}`)
	c := linksClient(t, "*")
	ctx := context.Background()
	opts := func(o ...OptionInput) []OptionInput { return o }
	many := make([]OptionInput, maxOptionChanges+1)
	for i := range many {
		many[i] = OptionInput{Name: strings.Repeat("x", i+1)}
	}
	for name, run := range map[string]func() error{
		"text column": func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Kunden", Column: "Name", Options: opts(OptionInput{Name: "x"})})
		},
		"unknown column": func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Kunden", Column: "Fehlt", Options: opts(OptionInput{Name: "x"})})
		},
		"other table column": func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Tickets", Column: "Status", Options: opts(OptionInput{Name: "x"})})
		},
		"existing option": func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Kunden", Column: "Status", Options: opts(OptionInput{Name: "Offen"})})
		},
		"missing option update": func() error {
			return c.UpdateOptions(ctx, "update", OptionsInput{Table: "Kunden", Column: "Status",
				Options: opts(OptionInput{Name: "Nie", NewName: "x"})})
		},
		"rename onto existing": func() error {
			return c.UpdateOptions(ctx, "update", OptionsInput{Table: "Kunden", Column: "Status",
				Options: opts(OptionInput{Name: "Offen", NewName: "Zu"})})
		},
		"missing option delete": func() error {
			return c.DeleteOptions(ctx, "delete", OptionsInput{Table: "Kunden", Column: "Status", Names: []string{"Nie"}})
		},
		"bad color": func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Kunden", Column: "Status", Options: opts(OptionInput{Name: "x", Color: "red"})})
		},
		"empty update": func() error {
			return c.UpdateOptions(ctx, "update", OptionsInput{Table: "Kunden", Column: "Status", Options: opts(OptionInput{Name: "Offen"})})
		},
		"duplicate": func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Kunden", Column: "Status", Options: opts(OptionInput{Name: "x"}, OptionInput{Name: "x"})})
		},
		"too many": func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Kunden", Column: "Status", Options: many})
		},
		"long name": func() error {
			return c.AddOptions(ctx, "add", OptionsInput{Table: "Kunden", Column: "Status", Options: opts(OptionInput{Name: strings.Repeat("x", 256)})})
		},
		"none": func() error {
			return c.DeleteOptions(ctx, "delete", OptionsInput{Table: "Kunden", Column: "Status"})
		},
	} {
		if err := run(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %+v", *calls)
	}
}

func TestOptionMutationsNeverRetryAndReportUncertainty(t *testing.T) {
	in := OptionsInput{Table: "Kunden", Column: "Status", Names: []string{"Offen"}}
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway} {
		calls := serveOptions(t, status, `{"detail":"`+providerCanary+`"}`)
		err := linksClient(t, "*").DeleteOptions(context.Background(), "delete", in)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), providerCanary) {
			t.Errorf("status %d error = %v", status, err)
		}
		if len(*calls) != 1 {
			t.Errorf("status %d calls = %d, want 1", status, len(*calls))
		}
	}
	serveOptions(t, http.StatusOK, `{"success":false}`)
	if err := linksClient(t, "*").DeleteOptions(context.Background(), "delete", in); err == nil {
		t.Error("an unsuccessful answer was accepted")
	}
}

func TestOptionRequestsOutsideTheAllowListAreRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
	handlers := map[string]capability.Handler{
		"add":    invokeOptionsChange("add", "add", (*Client).AddOptions, "added"),
		"update": invokeOptionsChange("update", "update", (*Client).UpdateOptions, "updated"),
		"delete": invokeOptionsChange("delete", "delete", (*Client).DeleteOptions, "deleted"),
	}
	for kind, handler := range handlers {
		for _, table := range []string{"Tickets", "id:0001", "Kunden/Standard", ""} {
			raw, _ := json.Marshal(map[string]any{"table": table, "column": "Status",
				"options": []map[string]string{{"name": "x", "color": "#000000"}}, "names": []string{}})
			if kind == "delete" {
				raw, _ = json.Marshal(map[string]any{"table": table, "column": "Status", "names": []string{"x"}})
			}
			_, err := handler(context.Background(), resolved, secrets, &redact.Redactor{}, raw)
			if err == nil || strings.Contains(err.Error(), "Tickets") || strings.Contains(err.Error(), "0001") {
				t.Errorf("%s %q = %v", kind, table, err)
			}
		}
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d", lookups)
	}
}
