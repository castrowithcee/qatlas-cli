package seatable

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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

const (
	linksRoute      = gatewayPath + salesBase + linksPath
	queryLinksRoute = gatewayPath + salesBase + queryLinksPath
	providerCanary  = "provider-link-error-canary-6e1c"
)

type linkCall struct {
	method string
	path   string
	body   map[string]any
}

// serveLinkRoutes answers the metadata with the link fixture and records every link request.
func serveLinkRoutes(t *testing.T, status int, answer string) *[]linkCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]linkCall{}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, linkMetadata), nil
		}
		data, _ := io.ReadAll(request.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		mu.Lock()
		*calls = append(*calls, linkCall{request.Method, request.URL.Path, body})
		mu.Unlock()
		return jsonResponse(status, answer), nil
	})
	return calls
}

func linksClient(t *testing.T, targets ...string) *Client {
	t.Helper()
	red := &redact.Redactor{}
	c, err := open(context.Background(), &config.Resolved{
		Name: "sales", Provider: Provider, BaseURL: cloudOrigin, Service: "seatable", Credential: "sales-reader",
		Targets: targets,
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIToken: salesEnv}},
	}, resolver(red), red, freeLimiter())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mutate(c *Client, op, method string, input linkInput) error {
	return c.ChangeLinks(context.Background(), op, method, input)
}

func TestRegisterPublishesLinkToolsWithRisk(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		effect capability.Effect
		allow  bool
	}{
		"seatable.links.list": {capability.EffectRead, false}, "seatable.links.create": {capability.EffectCreate, false},
		"seatable.links.update": {capability.EffectUpdate, false}, "seatable.links.delete": {capability.EffectDelete, true},
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
		for _, forbidden := range []string{"link_id", "other_table", "table_id"} {
			if strings.Contains(string(d.InputSchema), forbidden) {
				t.Errorf("%s input offers %q", d.ID, forbidden)
			}
		}
	}
	if seen != 4 {
		t.Fatalf("link tools = %d, want 4", seen)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	if !strings.Contains(strings.Join(metadata.Profiles[0].Tools, ","), "seatable.links.list") {
		t.Error("the read profile lacks seatable.links.list")
	}
}

func TestLinkRequestBodiesUseMetadataIdentifiers(t *testing.T) {
	ids := []string{otherRowID}
	for _, scope := range [][]string{{"*"}, {"Kunden", "id:0001"}} {
		calls := serveLinkRoutes(t, http.StatusOK, `{"success":true}`)
		c := linksClient(t, scope...)
		base := linkInput{Table: "Kunden", Column: "Vorgang", RowID: rowID, OtherRowIDs: ids}
		for _, tt := range []struct{ op, method string }{
			{"create links", http.MethodPost}, {"update links", http.MethodPut}, {"delete links", http.MethodDelete},
		} {
			if err := mutate(c, tt.op, tt.method, base); err != nil {
				t.Fatalf("%v %s = %v", scope, tt.op, err)
			}
		}
		if len(*calls) != 3 {
			t.Fatalf("calls = %d, want 3", len(*calls))
		}
		for i, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			call := (*calls)[i]
			encoded, _ := json.Marshal(call.body)
			want := `{"link_id":"l1","other_rows_ids_map":{"` + rowID + `":["` + otherRowID + `"]},` +
				`"other_table_id":"0001","table_id":"0000"}`
			if call.method != method || call.path != linksRoute || string(encoded) != want {
				t.Errorf("%v call %d = %s %s %s", scope, i, call.method, call.path, encoded)
			}
		}
		// A column can be named by key, and the reverse link column resolves its own direction.
		if err := mutate(c, "create links", http.MethodPost, linkInput{Table: "Kunden", Column: "bbbb", RowID: rowID, OtherRowIDs: ids}); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal((*calls)[3].body)
		if !strings.Contains(string(encoded), `"link_id":"l2"`) || !strings.Contains(string(encoded), `"other_table_id":"0001"`) {
			t.Errorf("reverse link body = %s", encoded)
		}
	}
}

func TestLinksListSendsMetadataKeysAndBoundsTheResult(t *testing.T) {
	answer := `{"` + rowID + `":[{"row_id":"` + otherRowID + `","display_value":"Ticket 1"},` +
		`{"row_id":"Zz12Cd34Ef56Gh78Ij90Kl","display_value":["a"]}]}`
	calls := serveLinkRoutes(t, http.StatusOK, answer)
	c := linksClient(t, "*")
	result, err := c.ListLinks(context.Background(), linkInput{Table: "Kunden", Column: "Vorgang", RowIDs: []string{rowID}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal((*calls)[0].body)
	want := `{"link_column_key":"aaaa","rows":[{"limit":2,"offset":0,"row_id":"` + rowID + `"}],"table_id":"0000"}`
	if (*calls)[0].path != queryLinksRoute || (*calls)[0].method != http.MethodPost || string(encoded) != want {
		t.Errorf("request = %+v %s", (*calls)[0], encoded)
	}
	if len(result.Rows) != 1 || len(result.Rows[0].Links) != 2 || !result.Rows[0].HasMore ||
		string(result.Rows[0].Links[0].DisplayValue) != `"Ticket 1"` {
		t.Errorf("result = %+v", result)
	}

	// More entries than the requested limit, or a link without a row identifier, is an invalid answer.
	for _, bad := range []string{
		`{"` + rowID + `":[{"row_id":"a"},{"row_id":"b"},{"row_id":"c"}]}`, `{"` + rowID + `":[{"display_value":"x"}]}`,
	} {
		serveLinkRoutes(t, http.StatusOK, bad)
		c = linksClient(t, "*")
		_, err := c.ListLinks(context.Background(), linkInput{Table: "Kunden", Column: "Vorgang", RowIDs: []string{rowID}, Limit: 2})
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("answer %s = %v", bad, err)
		}
	}

	// The shape and the counts are checked before any request.
	refuse(t)
	c = linksClient(t, "*")
	many := make([]string, maxLinkSourceRows+1)
	for i := range many {
		many[i] = rowID[:20] + string(rune('a'+i/10)) + string(rune('a'+i%10))
	}
	for name, input := range map[string]linkInput{
		"no rows":      {Table: "Kunden", Column: "Vorgang"},
		"too many":     {Table: "Kunden", Column: "Vorgang", RowIDs: many},
		"bad row id":   {Table: "Kunden", Column: "Vorgang", RowIDs: []string{"../x"}},
		"duplicate id": {Table: "Kunden", Column: "Vorgang", RowIDs: []string{rowID, rowID}},
		"page size":    {Table: "Kunden", Column: "Vorgang", RowIDs: []string{rowID}, Limit: 101},
	} {
		if _, err := c.ListLinks(context.Background(), input); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestLinkMutationsRefuseBadShapesBeforeAnyRequest(t *testing.T) {
	refuse(t)
	c := linksClient(t, "*")
	targets := make([]string, maxLinkTargets+1)
	for i := range targets {
		targets[i] = rowID[:20] + string(rune('a'+i/10)) + string(rune('a'+i%10))
	}
	for name, input := range map[string]linkInput{
		"bad source": {Table: "Kunden", Column: "Vorgang", RowID: "x", OtherRowIDs: []string{otherRowID}},
		"no targets": {Table: "Kunden", Column: "Vorgang", RowID: rowID},
		"bad target": {Table: "Kunden", Column: "Vorgang", RowID: rowID, OtherRowIDs: []string{"a b"}},
		"too many":   {Table: "Kunden", Column: "Vorgang", RowID: rowID, OtherRowIDs: targets},
		"duplicates": {Table: "Kunden", Column: "Vorgang", RowID: rowID, OtherRowIDs: []string{otherRowID, otherRowID}},
	} {
		if err := mutate(c, "create links", http.MethodPost, input); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestEmptyTargetListIsRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
	for _, tt := range []struct{ op, method string }{
		{"create links", http.MethodPost}, {"update links", http.MethodPut}, {"delete links", http.MethodDelete},
	} {
		for _, others := range []string{`[]`, `null`} {
			_, err := invokeLinksChange(tt.op, tt.method, "done")(context.Background(), resolved, secrets, &redact.Redactor{},
				json.RawMessage(`{"column":"Vorgang","row_id":"`+rowID+`","other_row_ids":`+others+`}`))
			if err == nil {
				t.Errorf("%s accepted an empty list (%s)", tt.op, others)
			}
		}
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d, want 0", lookups)
	}
	if !strings.Contains(string(linksUpdate.InputSchema), `"minItems":1`) {
		t.Errorf("update schema = %s", linksUpdate.InputSchema)
	}
}

func TestOwnTableOutsideTheAllowListIsRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	lookups := 0
	secrets := secret.NewWith(func(string) string { lookups++; return salesToken }, nil, nil, &redact.Redactor{})
	resolved := resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden")
	for id, handler := range map[string]capability.Handler{
		"list": invokeLinksList, "create": invokeLinksChange("create links", http.MethodPost, "created"),
		"update": invokeLinksChange("update links", http.MethodPut, "updated"),
		"delete": invokeLinksChange("delete links", http.MethodDelete, "deleted"),
	} {
		_, err := handler(context.Background(), resolved, secrets, &redact.Redactor{}, json.RawMessage(
			`{"table":"Tickets","column":"Vorgang","row_ids":["`+rowID+`"],"row_id":"`+rowID+`","other_row_ids":["`+otherRowID+`"]}`))
		if err == nil {
			t.Errorf("%s accepted a table outside the allow-list", id)
		}
	}
	if lookups != 0 {
		t.Errorf("secret lookups = %d, want 0", lookups)
	}
}

func TestCounterpartTableOutsideTheAllowListIsRefusedBeforeTheLinkRequest(t *testing.T) {
	for _, scope := range [][]string{{"Kunden"}, {"id:0000"}, {"Kunden/Standard"}} {
		calls := serveLinkRoutes(t, http.StatusOK, `{"success":true}`)
		c := linksClient(t, scope...)
		ctx := context.Background()
		errs := []error{
			mutate(c, "create links", http.MethodPost, linkInput{Table: "Kunden", Column: "Vorgang", RowID: rowID, OtherRowIDs: []string{otherRowID}}),
			mutate(c, "update links", http.MethodPut, linkInput{Table: "id:0000", Column: "Vorgang", RowID: rowID, OtherRowIDs: []string{otherRowID}}),
			mutate(c, "delete links", http.MethodDelete, linkInput{Column: "Vorgang", RowID: rowID, OtherRowIDs: []string{otherRowID}}),
		}
		_, listErr := c.ListLinks(ctx, linkInput{Column: "Vorgang", RowIDs: []string{rowID}})
		errs = append(errs, listErr)
		for _, err := range errs {
			if err == nil {
				t.Fatalf("%v: a link into a table outside the allow-list was accepted", scope)
			}
			if strings.Contains(err.Error(), "Tickets") || strings.Contains(err.Error(), "0001") {
				t.Errorf("%v: the message names the counterpart table: %v", scope, err)
			}
		}
		if len(*calls) != 0 {
			t.Errorf("%v: link requests = %v, want none", scope, *calls)
		}
	}
}

func TestUnknownAndNonLinkColumnsAreRefusedBeforeTheLinkRequest(t *testing.T) {
	calls := serveLinkRoutes(t, http.StatusOK, `{"success":true}`)
	c := linksClient(t, "*")
	for _, column := range []string{"Gibtsnicht", "Name", "0000", "Unklar"} {
		if err := mutate(c, "create links", http.MethodPost, linkInput{Table: "Kunden", Column: column, RowID: rowID, OtherRowIDs: []string{otherRowID}}); err == nil {
			t.Errorf("column %q was accepted", column)
		}
		if _, err := c.ListLinks(context.Background(), linkInput{Table: "Kunden", Column: column, RowIDs: []string{rowID}}); err == nil {
			t.Errorf("list accepted column %q", column)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("link requests = %v, want none", *calls)
	}
}

func TestLinkMutationsSendOneRequestAndReportUncertainty(t *testing.T) {
	input := linkInput{Table: "Kunden", Column: "Vorgang", RowID: rowID, OtherRowIDs: []string{otherRowID}}
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		uncertain bool
	}{
		{"server error", http.StatusInternalServerError, `{"error_msg":"` + providerCanary + `"}`, true},
		{"gateway timeout", http.StatusGatewayTimeout, ``, true},
		{"unreadable answer", http.StatusOK, `not json`, true},
		{"duplicate link", http.StatusBadRequest, `{"error_msg":"` + providerCanary + `"}`, false},
		{"not applied", http.StatusOK, `{"success":false,"error_msg":"` + providerCanary + `"}`, false},
	} {
		calls := serveLinkRoutes(t, tt.status, tt.body)
		err := mutate(linksClient(t, "*"), "create links", http.MethodPost, input)
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

	t.Run("timeout and reset", func(t *testing.T) {
		for _, failure := range []error{context.DeadlineExceeded, errors.New("connection reset by peer")} {
			requests := 0
			serveBase(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == metaRoute(salesBase) {
					return jsonResponse(http.StatusOK, linkMetadata), nil
				}
				requests++
				return nil, &url.Error{Op: "Post", URL: "https://x.invalid", Err: failure}
			})
			err := mutate(linksClient(t, "*"), "delete links", http.MethodDelete, input)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || requests != 1 {
				t.Errorf("%v: err = %v, requests = %d", failure, err, requests)
			}
		}
	})
}

func TestLinkTheCoreNeedsConfirmationAndPermissions(t *testing.T) {
	calls := serveLinkRoutes(t, http.StatusOK, `{"success":true}`)
	stubLimiter(t, salesToken)
	cfg := coreConfig()
	cfg.Connections["links"] = config.Connection{Service: "sea-cloud", Credential: "sales-reader",
		Targets: []string{"*"}, Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete},
		Tools: []string{"seatable.links.list", "seatable.links.create", "seatable.links.delete"}}
	cfg.Connections["links-read"] = config.Connection{Service: "sea-cloud", Credential: "sales-reader", Targets: []string{"*"}}
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	args := json.RawMessage(`{"table":"Kunden","column":"Vorgang","row_id":"` + rowID + `","other_row_ids":["` + otherRowID + `"]}`)

	for _, op := range []string{"seatable.links.create", "seatable.links.delete"} {
		if _, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: op, Connection: "links", Arguments: args}); err == nil {
			t.Errorf("%s ran without confirm", op)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("requests without confirm = %v", *calls)
	}
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "seatable.links.create", Connection: "links", Arguments: args, Confirmed: true})
	if err != nil || string(response.Result) != `{"created":true}` {
		t.Fatalf("create = %s, %v", response.Result, err)
	}
	// update is not in the tool list; delete needs the tool list, and a read connection offers no mutation.
	for _, request := range []application.InvokeRequest{
		{Operation: "seatable.links.update", Connection: "links", Arguments: args, Confirmed: true},
		{Operation: "seatable.links.create", Connection: "links-read", Arguments: args, Confirmed: true},
		{Operation: "seatable.links.delete", Connection: "links-read", Arguments: args, Confirmed: true},
	} {
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran on %s", request.Operation, request.Connection)
		}
	}
	list, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: "seatable.links.list", Connection: "links-read",
		Arguments: json.RawMessage(`{"table":"Kunden","column":"Vorgang","row_ids":["` + rowID + `"]}`)})
	if err != nil || !strings.Contains(string(list.Result), `"has_more":false`) {
		t.Errorf("list = %s, %v", list.Result, err)
	}
}
