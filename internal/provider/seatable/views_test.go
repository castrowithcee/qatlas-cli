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

const viewMetadata = `{"metadata":{"tables":[
 {"_id":"0000","name":"Kunden","columns":[{"key":"0000","name":"Name","type":"text"},
  {"key":"a1b2","name":"Betrag","type":"number"},{"key":"c3d4","name":"Notiz","type":"text"}],
  "views":[{"_id":"0000","name":"Standard"},{"_id":"7Yk3","name":"Aktive"},{"_id":"Zz99","name":"Archiv"}]},
 {"_id":"0001","name":"Tickets","columns":[{"key":"0000","name":"Titel","type":"text"}],
  "views":[{"_id":"0000","name":"Standard"}]}
]}}`

const viewList = `{"views":[{"_id":"0000","name":"Standard","type":"table"},
 {"_id":"7Yk3","name":"Aktive","type":"table","filter_conjunction":"And",
  "filters":[{"column_key":"a1b2","filter_predicate":"greater","filter_term":"` + cellCanary + `"}],
  "sorts":[{"column_key":"0000","sort_type":"up"}],"hidden_columns":["c3d4"],"_creator":"x@example.invalid"},
 {"_id":"Zz99","name":"Archiv","type":"table"}]}`

type viewCall struct {
	method, path, query string
	body                map[string]any
}

func serveViews(t *testing.T, status int, answer string) *[]viewCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]viewCall{}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, viewMetadata), nil
		}
		var body map[string]any
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(data, &body)
		}
		mu.Lock()
		*calls = append(*calls, viewCall{request.Method, request.URL.EscapedPath(), request.URL.RawQuery, body})
		mu.Unlock()
		if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, viewsPath) {
			return jsonResponse(http.StatusOK, viewList), nil
		}
		return jsonResponse(status, answer), nil
	})
	return calls
}

func TestRegisterPublishesViewToolsWithRisk(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		effect capability.Effect
		allow  bool
	}{
		"seatable.views.list": {capability.EffectRead, false}, "seatable.views.get": {capability.EffectRead, false},
		"seatable.views.create": {capability.EffectCreate, false}, "seatable.views.update": {capability.EffectUpdate, false},
		"seatable.views.delete": {capability.EffectDelete, true},
	}
	seen := 0
	for _, d := range reg.Provider(Provider) {
		w, ok := want[d.ID]
		if !ok {
			continue
		}
		seen++
		if d.Risk.Effect != w.effect || d.RequiresToolAllowList != w.allow || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != dataSensitivity || d.Risk.Idempotency == "" {
			t.Errorf("%s = %+v", d.ID, d)
		}
		if w.effect != capability.EffectRead && d.Risk.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s needs confirmation", d.ID)
		}
	}
	if seen != 5 {
		t.Fatalf("view tools = %d, want 5", seen)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	profile := strings.Join(metadata.Profiles[0].Tools, ",")
	for _, id := range []string{"seatable.views.list", "seatable.views.get"} {
		if !strings.Contains(profile, id) {
			t.Errorf("the read profile lacks %s", id)
		}
	}
	for _, id := range []string{"create", "update", "delete"} {
		if strings.Contains(profile, "seatable.views."+id) {
			t.Errorf("the read profile holds views.%s", id)
		}
	}
}

func TestViewsListAndGetUseTheTableNameAndNormalizeTheAnswer(t *testing.T) {
	calls := serveViews(t, http.StatusOK, `{}`)
	c := linksClient(t, "*")
	list, err := c.ListViews(context.Background(), ViewInput{Table: "id:0000"})
	if err != nil || len(list.Views) != 3 {
		t.Fatalf("ListViews = %+v, %v", list, err)
	}
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), "x@example.invalid") || !strings.Contains(string(encoded), `"hidden_columns":["c3d4"]`) {
		t.Errorf("list = %s", encoded)
	}
	(*calls) = nil
	got, err := c.GetView(context.Background(), ViewInput{Table: "Kunden", View: "id:7Yk3"})
	if err != nil {
		t.Fatal(err)
	}
	if got.View.Name != "Aktive" || got.View.ID != "7Yk3" {
		t.Errorf("view = %+v", got.View)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet || (*calls)[0].query != "table_name=Kunden" ||
		(*calls)[0].path != gatewayPath+salesBase+viewsPath+"Aktive/" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestViewsStayInsideTheTableAndViewBinding(t *testing.T) {
	calls := serveViews(t, http.StatusOK, `{}`)
	c := linksClient(t, "Kunden/Aktive", "Tickets")
	// A table outside the allow-list is refused without any request.
	if _, err := c.ListViews(context.Background(), ViewInput{Table: "Kunden"}); err == nil {
		t.Error("table outside the allow-list was listed")
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %+v", *calls)
	}
	list, err := c.ListViews(context.Background(), ViewInput{Table: "Kunden/Aktive"})
	if err != nil || len(list.Views) != 1 || list.Views[0].Name != "Aktive" {
		t.Fatalf("bound list = %+v, %v", list, err)
	}
	if _, err := c.GetView(context.Background(), ViewInput{Table: "Kunden/Aktive", View: "Archiv"}); err == nil ||
		strings.Contains(err.Error(), "Aktive") {
		t.Errorf("foreign view = %v", err)
	}
	if _, err := c.GetView(context.Background(), ViewInput{Table: "Kunden/Aktive", View: "id:7Yk3"}); err != nil {
		t.Errorf("bound view by id = %v", err)
	}
	// A view of another table cannot be reached through this table.
	c = linksClient(t, "Kunden", "Tickets")
	if _, err := c.GetView(context.Background(), ViewInput{Table: "Tickets", View: "Aktive"}); err == nil {
		t.Error("view of another table was read")
	}
	if err := c.CreateView(context.Background(), "create view", ViewInput{Table: "Kunden/Aktive", Name: "X"}); err == nil {
		t.Error("view creation in a narrowed selection")
	}
	for _, call := range *calls {
		if call.method != http.MethodGet {
			t.Errorf("unexpected call %+v", call)
		}
	}
}

func TestTargetViewsCannotBeChangedOrDeleted(t *testing.T) {
	calls := serveViews(t, http.StatusOK, `{}`)
	for _, scope := range [][]string{{"Kunden/Aktive", "Kunden"}, {"id:0000/id:7Yk3", "Kunden"}} {
		c := linksClient(t, scope...)
		for _, ref := range []string{"Aktive", "id:7Yk3"} {
			in := ViewInput{Table: "Kunden", View: ref, Name: "Neu"}
			if err := c.UpdateView(context.Background(), "update view", in); err == nil {
				t.Errorf("%v update %s", scope, ref)
			}
			if err := c.DeleteView(context.Background(), "delete view", in); err == nil {
				t.Errorf("%v delete %s", scope, ref)
			}
		}
	}
	if len(*calls) != 0 {
		t.Errorf("mutation requests = %+v", *calls)
	}
	c := linksClient(t, "Kunden/Aktive", "Kunden")
	if err := c.DeleteView(context.Background(), "delete view", ViewInput{Table: "Kunden", View: "Archiv"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodDelete || (*calls)[0].query != "table_name=Kunden" ||
		(*calls)[0].path != gatewayPath+salesBase+viewsPath+"Archiv/" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestViewCreateAndUpdateSendOneRequestWithMetadataKeys(t *testing.T) {
	calls := serveViews(t, http.StatusOK, `{"success":true}`)
	c := linksClient(t, "*")
	if err := c.CreateView(context.Background(), "create view", ViewInput{Table: "Kunden", Name: "Offen"}); err != nil {
		t.Fatal(err)
	}
	filters := []ViewFilterInput{{Column: "Betrag", Predicate: "greater", Term: json.RawMessage(`100`)}}
	sorts := []ViewSortInput{{Column: "a1b2", Direction: "desc"}}
	hidden := []string{"Notiz"}
	in := ViewInput{Table: "Kunden", View: "Archiv", Name: "Alt", Filters: &filters, FilterConjunction: "or",
		Sorts: &sorts, HiddenColumns: &hidden}
	if err := c.UpdateView(context.Background(), "update view", in); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("calls = %+v", *calls)
	}
	created, _ := json.Marshal((*calls)[0].body)
	if (*calls)[0].method != http.MethodPost || (*calls)[0].query != "table_name=Kunden" || string(created) != `{"name":"Offen"}` {
		t.Errorf("create = %+v %s", (*calls)[0], created)
	}
	updated, _ := json.Marshal((*calls)[1].body)
	want := `{"filter_conjunction":"Or","filters":[{"column_key":"a1b2","filter_predicate":"greater","filter_term":100}],` +
		`"hidden_columns":["c3d4"],"name":"Alt","sorts":[{"column_key":"a1b2","sort_type":"down"}]}`
	if (*calls)[1].method != http.MethodPut || (*calls)[1].path != gatewayPath+salesBase+viewsPath+"Archiv/" ||
		string(updated) != want {
		t.Errorf("update = %+v\n%s", (*calls)[1], updated)
	}
}

func TestViewUpdateChecksColumnsAndShapeBeforeAnyChange(t *testing.T) {
	calls := serveViews(t, http.StatusOK, `{}`)
	c := linksClient(t, "*")
	unknown := []string{"Gibt es nicht"}
	badFilter := []ViewFilterInput{{Column: "Name", Predicate: "Drop Table"}}
	tooMany := make([]ViewSortInput, maxViewSorts+1)
	for i := range tooMany {
		tooMany[i] = ViewSortInput{Column: "Name"}
	}
	dup := []string{"Name", "0000"}
	for name, in := range map[string]ViewInput{
		"hidden unknown":  {Table: "Kunden", View: "Archiv", HiddenColumns: &unknown},
		"hidden twice":    {Table: "Kunden", View: "Archiv", HiddenColumns: &dup},
		"predicate":       {Table: "Kunden", View: "Archiv", Filters: &badFilter},
		"too many sorts":  {Table: "Kunden", View: "Archiv", Sorts: &tooMany},
		"no change":       {Table: "Kunden", View: "Archiv"},
		"conjunction":     {Table: "Kunden", View: "Archiv", FilterConjunction: "xor"},
		"slash in name":   {Table: "Kunden", View: "Archiv", Name: "a/b"},
		"unknown view":    {Table: "Kunden", View: "Fehlt", Name: "x"},
		"filter column":   {Table: "Kunden", View: "Archiv", Filters: &[]ViewFilterInput{{Column: "Fehlt", Predicate: "is"}}},
		"sort column key": {Table: "Kunden", View: "Archiv", Sorts: &[]ViewSortInput{{Column: "Fehlt"}}},
	} {
		if err := c.UpdateView(context.Background(), "update view", in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %+v", *calls)
	}
}

func TestViewMutationsNeverRetryAndReportUncertainty(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway} {
		calls := serveViews(t, status, `{"detail":"`+providerCanary+`"}`)
		c := linksClient(t, "*")
		in := ViewInput{Table: "Kunden", View: "Archiv"}
		err := c.DeleteView(context.Background(), "delete view", in)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), providerCanary) {
			t.Errorf("status %d error = %v", status, err)
		}
		if len(*calls) != 1 {
			t.Errorf("status %d calls = %d, want 1", status, len(*calls))
		}
	}
	serveViews(t, http.StatusOK, `{"success":false}`)
	if err := linksClient(t, "*").DeleteView(context.Background(), "delete view", ViewInput{Table: "Kunden", View: "Archiv"}); err == nil {
		t.Error("an unsuccessful answer was accepted")
	}
	serveViews(t, http.StatusNotFound, `{"detail":"`+providerCanary+`"}`)
	err := linksClient(t, "*").CreateView(context.Background(), "create view", ViewInput{Table: "Kunden", Name: "N"})
	if classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), providerCanary) {
		t.Errorf("404 = %v", err)
	}
}

func TestViewRequestsOutsideTheAllowListAreRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
	handlers := map[string]capability.Handler{
		"list": invokeViewsList, "get": invokeViewsGet,
		"create": invokeViewsChange("create view", "create", (*Client).CreateView, "created"),
		"update": invokeViewsChange("update view", "update", (*Client).UpdateView, "updated"),
		"delete": invokeViewsChange("delete view", "delete", (*Client).DeleteView, "deleted"),
	}
	for id, handler := range handlers {
		_, err := handler(context.Background(), resolved, secrets, &redact.Redactor{},
			json.RawMessage(`{"table":"Geheim","view":"Standard","name":"x"}`))
		if err == nil || strings.Contains(err.Error(), "Geheim") {
			t.Errorf("%s foreign table = %v", id, err)
		}
	}
	// A malformed request is refused before the credential is resolved.
	for id, raw := range map[string]string{
		"get": `{"view":"id:a b"}`, "create": `{"name":"a/b"}`, "update": `{"view":"Standard"}`, "delete": `{"view":""}`,
	} {
		if _, err := handlers[id](context.Background(), resolved, secrets, &redact.Redactor{}, json.RawMessage(raw)); err == nil {
			t.Errorf("%s accepted %s", id, raw)
		}
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d, want 0", lookups)
	}
}

func TestViewAnswersAreBounded(t *testing.T) {
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, viewMetadata), nil
		}
		var b strings.Builder
		b.WriteString(`{"views":[`)
		for i := 0; i < maxViews+5; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"_id":"v` + strings.Repeat("x", 3) + `","name":"n"}`)
		}
		b.WriteString(`]}`)
		return jsonResponse(http.StatusOK, b.String()), nil
	})
	list, err := linksClient(t, "*").ListViews(context.Background(), ViewInput{Table: "Kunden"})
	if err != nil || len(list.Views) != maxViews || !list.Truncated {
		t.Errorf("list = %d, %v, %v", len(list.Views), list.Truncated, err)
	}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, viewMetadata), nil
		}
		return jsonResponse(http.StatusOK, `{"views":[{"name":""}]}`), nil
	})
	if _, err := linksClient(t, "*").ListViews(context.Background(), ViewInput{Table: "Kunden"}); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("nameless view = %v", err)
	}
}
