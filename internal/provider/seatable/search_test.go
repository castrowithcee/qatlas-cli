package seatable

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const searchMetadata = `{"metadata":{"tables":[
 {"_id":"0000","name":"Kunden","columns":[
   {"key":"0000","name":"Name","type":"text"},
   {"key":"1111","name":"Betrag","type":"number"},
   {"key":"aaaa","name":"Vorgang","type":"link","data":{"table_id":"0000","other_table_id":"0001"}}],
  "views":[{"_id":"0000","name":"Standard"}]},
 {"_id":"0001","name":"Tickets","columns":[{"key":"0000","name":"Titel","type":"text"}],"views":[]},
 {"_id":"0002","name":"Bad` + "`" + `Name","columns":[{"key":"0000","name":"Name","type":"text"}],"views":[]}
]}}`

// searchServer answers the metadata and the SQL route, and records every SQL request.
type searchServer struct {
	metadataCalls atomic.Int32
	sqlBodies     []map[string]json.RawMessage
	results       string
}

func serveSearch(t *testing.T, results string) *searchServer {
	t.Helper()
	server := &searchServer{results: results}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case metaRoute(salesBase):
			server.metadataCalls.Add(1)
			return jsonResponse(http.StatusOK, searchMetadata), nil
		case gatewayPath + salesBase + sqlPath:
			if request.Method != http.MethodPost ||
				request.Header.Get("Authorization") != "Bearer "+salesBaseToken {
				t.Errorf("sql request = %s with an unexpected token", request.Method)
			}
			data, _ := io.ReadAll(request.Body)
			var body map[string]json.RawMessage
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("sql body: %v", err)
			}
			server.sqlBodies = append(server.sqlBodies, body)
			return jsonResponse(http.StatusOK, `{"metadata":[],"results":`+server.results+`}`), nil
		}
		t.Errorf("unexpected path %s", request.URL.Path)
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	return server
}

func (s *searchServer) statement(t *testing.T, i int) (string, []any) {
	t.Helper()
	if len(s.sqlBodies) <= i {
		t.Fatalf("sql requests = %d, want more than %d", len(s.sqlBodies), i)
	}
	var statement string
	var params []any
	_ = json.Unmarshal(s.sqlBodies[i]["sql"], &statement)
	_ = json.Unmarshal(s.sqlBodies[i]["parameters"], &params)
	if string(s.sqlBodies[i]["convert_keys"]) != "true" {
		t.Errorf("convert_keys = %s", s.sqlBodies[i]["convert_keys"])
	}
	return statement, params
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestSearchSendsFixedSQLWithValuesOnlyInParameters(t *testing.T) {
	server := serveSearch(t, `[{"_id":"`+rowID+`","Name":"Bike"}]`)
	c, _ := client(t, "Kunden")
	result, err := c.SearchRows(context.Background(), SearchOptions{
		Filters: []SearchFilter{
			{Column: "Name", Op: "like", Value: raw(`"` + cellCanary + `%"`)},
			{Column: "Betrag", Op: "gte", Value: raw(`10.5`)},
			{Column: "Betrag", Op: "in", Values: []json.RawMessage{raw(`1`), raw(`2`)}},
			{Column: "Name", Op: "is_null"},
			{Column: "Name", Op: "ne", Value: raw(`"x' OR 1=1 --"`)},
		},
		Sort:  []SearchSort{{Column: "_ctime", Direction: "desc"}, {Column: "Name"}},
		Start: 20000, Limit: 50,
	})
	if err != nil {
		t.Fatalf("SearchRows() = %v", err)
	}
	statement, params := server.statement(t, 0)
	want := "SELECT * FROM `Kunden` WHERE `Name` LIKE ? AND `Betrag` >= ? AND `Betrag` IN (?, ?) AND `Name` IS NULL " +
		"AND `Name` != ? ORDER BY `_ctime` DESC, `Name` ASC LIMIT 50 OFFSET 20000"
	if statement != want {
		t.Errorf("sql = %s\nwant %s", statement, want)
	}
	if strings.Contains(statement, cellCanary) || strings.Contains(statement, "OR 1=1") {
		t.Errorf("a value reached the statement: %s", statement)
	}
	wantParams := []any{cellCanary + "%", json.Number("10.5"), json.Number("1"), json.Number("2"), "x' OR 1=1 --"}
	gotEncoded, _ := json.Marshal(params)
	wantEncoded, _ := json.Marshal(wantParams)
	if string(gotEncoded) != string(wantEncoded) {
		t.Errorf("parameters = %s, want %s", gotEncoded, wantEncoded)
	}
	if len(result.Rows) != 1 || result.Rows[0].ID != rowID || result.Start != 20000 || result.HasMore {
		t.Errorf("result = %+v", result)
	}
}

func TestSearchWithoutConditionsAndByTableIdentifier(t *testing.T) {
	server := serveSearch(t, `[]`)
	c, _ := client(t, "id:0000")
	if _, err := c.SearchRows(context.Background(), SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	statement, params := server.statement(t, 0)
	if statement != "SELECT * FROM `Kunden` LIMIT 25 OFFSET 0" || len(params) != 0 {
		t.Errorf("sql = %s %v", statement, params)
	}
	if string(server.sqlBodies[0]["parameters"]) != "[]" {
		t.Errorf("parameters = %s, want an empty array", server.sqlBodies[0]["parameters"])
	}
}

// Everything that can be judged without the provider is refused before any request, and a view on the
// target does not narrow the search.
func TestSearchRefusesUnsafeRequestsBeforeProviderIO(t *testing.T) {
	refuse(t)
	c, _ := client(t, "Kunden")
	long := strings.Repeat("a", maxValueLength+1)
	tests := map[string]SearchOptions{
		"backtick column":     {Filters: []SearchFilter{{Column: "Na`me", Op: "eq", Value: raw(`1`)}}},
		"backslash column":    {Filters: []SearchFilter{{Column: `Na\me`, Op: "eq", Value: raw(`1`)}}},
		"backtick sort":       {Sort: []SearchSort{{Column: "x` DESC; DROP"}}},
		"operator injection":  {Filters: []SearchFilter{{Column: "Name", Op: "= 1 OR 1=1 --", Value: raw(`1`)}}},
		"symbolic operator":   {Filters: []SearchFilter{{Column: "Name", Op: "=", Value: raw(`1`)}}},
		"empty operator":      {Filters: []SearchFilter{{Column: "Name", Value: raw(`1`)}}},
		"direction injection": {Sort: []SearchSort{{Column: "Name", Direction: "ASC; DROP"}}},
		"object value":        {Filters: []SearchFilter{{Column: "Name", Op: "eq", Value: raw(`{"a":1}`)}}},
		"null value":          {Filters: []SearchFilter{{Column: "Name", Op: "eq", Value: raw(`null`)}}},
		"missing value":       {Filters: []SearchFilter{{Column: "Name", Op: "eq"}}},
		"like number":         {Filters: []SearchFilter{{Column: "Name", Op: "like", Value: raw(`1`)}}},
		"is_null with value":  {Filters: []SearchFilter{{Column: "Name", Op: "is_null", Value: raw(`1`)}}},
		"in without values":   {Filters: []SearchFilter{{Column: "Name", Op: "in"}}},
		"in with value":       {Filters: []SearchFilter{{Column: "Name", Op: "in", Value: raw(`1`)}}},
		"in too many":         {Filters: []SearchFilter{{Column: "Name", Op: "in", Values: make([]json.RawMessage, maxInValues+1)}}},
		"long value":          {Filters: []SearchFilter{{Column: "Name", Op: "eq", Value: raw(`"` + long + `"`)}}},
		"too many filters":    {Filters: make([]SearchFilter, maxFilters+1)},
		"too many sorts":      {Sort: make([]SearchSort, maxSorts+1)},
		"limit above bound":   {Limit: maxPageSize + 1},
		"negative limit":      {Limit: -1},
		"offset above bound":  {Start: maxSearchOffset + 1},
		"negative offset":     {Start: -1},
		"other table":         {Table: "Tickets"},
	}
	for name, options := range tests {
		if _, err := c.SearchRows(context.Background(), options); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A table with a view still resolves to the whole table.
	c, _ = client(t, "Kunden/Standard")
	if _, err := c.SearchRows(context.Background(), SearchOptions{Table: "Kunden"}); err == nil {
		t.Error("a table without the configured view was accepted")
	}
}

func TestSearchOffsetBoundIsAccepted(t *testing.T) {
	server := serveSearch(t, `[]`)
	c, _ := client(t, "Kunden/Standard")
	if _, err := c.SearchRows(context.Background(), SearchOptions{Start: maxSearchOffset, Limit: maxPageSize}); err != nil {
		t.Fatal(err)
	}
	statement, _ := server.statement(t, 0)
	if statement != "SELECT * FROM `Kunden` LIMIT 100 OFFSET 100000" {
		t.Errorf("sql = %s", statement)
	}
}

func TestSearchChecksColumnsAgainstMetadataBeforeTheQuery(t *testing.T) {
	for name, options := range map[string]SearchOptions{
		"unknown filter column": {Filters: []SearchFilter{{Column: "Unbekannt", Op: "eq", Value: raw(`1`)}}},
		"unknown sort column":   {Sort: []SearchSort{{Column: "Unbekannt"}}},
		"link filter":           {Filters: []SearchFilter{{Column: "Vorgang", Op: "is_null"}}},
		"link sort":             {Sort: []SearchSort{{Column: "Vorgang"}}},
	} {
		server := serveSearch(t, `[]`)
		c, _ := client(t, "Kunden")
		if _, err := c.SearchRows(context.Background(), options); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if len(server.sqlBodies) != 0 {
			t.Errorf("%s reached the SQL route", name)
		}
	}

	server := serveSearch(t, `[]`)
	c, _ := client(t, "id:0002")
	if _, err := c.SearchRows(context.Background(), SearchOptions{}); err == nil {
		t.Error("a table name with a backtick was accepted")
	}
	if len(server.sqlBodies) != 0 {
		t.Error("a table name with a backtick reached the SQL route")
	}
}

// A table outside the allow-list is refused by the core before the credential is resolved and before any
// provider request, and the refused table is not named.
func TestCoreRefusesSearchOutsideTheAllowListBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	var reads atomic.Int32
	red := &redact.Redactor{}
	resolve := secret.NewWith(func(string) string { reads.Add(1); return salesToken }, nil, nil, red)

	cfg := coreConfig()
	cfg.Connections["selected"] = config.Connection{
		Service: "sea-cloud", Credential: "sales-reader", Targets: []string{"id:0000", "id:0001"},
	}
	core := application.New(registry(t), cfg, resolve, red)
	for name, arguments := range map[string]string{
		"foreign table":    `{"table":"id:9999-secret-target"}`,
		"foreign by name":  `{"table":"Tickets"}`,
		"no table":         `{}`,
		"backtick column":  `{"table":"id:0000","filters":[{"column":"a` + "`" + `b","op":"eq","value":1}]}`,
		"operator":         `{"table":"id:0000","filters":[{"column":"a","op":"OR 1=1","value":1}]}`,
		"limit":            `{"table":"id:0000","limit":101}`,
		"offset":           `{"table":"id:0000","start":100001}`,
		"unknown argument": `{"table":"id:0000","sql":"SELECT 1"}`,
	} {
		before := reads.Load()
		_, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: "seatable.rows.search", Connection: "selected", Arguments: json.RawMessage(arguments),
		})
		if reads.Load() != before {
			t.Errorf("%s resolved the credential", name)
		}
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "9999-secret-target") {
			t.Errorf("%s: the error names the refused table: %v", name, err)
		}
	}
	if reads.Load() != 0 {
		t.Errorf("the credential was resolved %d times before the refusals", reads.Load())
	}
}

func TestCoreSearchesThroughTheApplicationCore(t *testing.T) {
	server := serveSearch(t, `[{"_id":"`+rowID+`","Name":"Bike"}]`)
	stubLimiter(t, salesToken)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	response, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "seatable.rows.search", Connection: "sales",
		Arguments: json.RawMessage(`{"filters":[{"column":"Name","op":"eq","value":"Bike"}],"limit":1}`),
	})
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	statement, _ := server.statement(t, 0)
	if statement != "SELECT * FROM `Kunden` WHERE `Name` = ? LIMIT 1 OFFSET 0" {
		t.Errorf("sql = %s", statement)
	}
	var result ListResult
	if err := json.Unmarshal(response.Result, &result); err != nil || len(result.Rows) != 1 ||
		!result.HasMore || result.NextStart != 1 {
		t.Errorf("result = %s (%v)", response.Result, err)
	}
}

func TestSearchAnswerAndErrorsAreBounded(t *testing.T) {
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, searchMetadata), nil
		}
		return jsonResponse(http.StatusOK, `{"results":[{"_id":"`+rowID+`","Name":"`+
			strings.Repeat("a", maxResponseBytes)+`"}]}`), nil
	})
	c, _ := client(t, "Kunden")
	if _, err := c.SearchRows(context.Background(), SearchOptions{}); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("oversized answer error = %v", err)
	}

	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			return jsonResponse(http.StatusOK, searchMetadata), nil
		}
		return jsonResponse(http.StatusBadRequest, `{"error_msg":"`+bodyCanary+`"}`), nil
	})
	c, _ = client(t, "Kunden")
	_, err := c.SearchRows(context.Background(), SearchOptions{})
	if err == nil || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("error = %v, want a class without provider text", err)
	}

	serveSearch(t, `[{"_id":"a"},{"_id":"b"}]`)
	c, _ = client(t, "Kunden")
	if _, err := c.SearchRows(context.Background(), SearchOptions{Limit: 1}); err == nil {
		t.Error("more rows than the limit were accepted")
	}
}

func TestSearchMasksLinksOutsideTheAllowList(t *testing.T) {
	// Vorgang links to Tickets, which is outside the allow-list of "Kunden". The provider answers with the
	// expected entries, and with shapes nobody promised.
	for name, cell := range map[string]string{
		"entries":       `[{"row_id":"` + otherRowID + `","display_value":"` + linkCanary + `"}]`,
		"mixed entries": `[{"row_id":"` + otherRowID + `","display_value":"` + linkCanary + `"},"` + linkCanary + `"]`,
	} {
		serveSearch(t, `[{"_id":"`+rowID+`","Name":"Bike","Vorgang":`+cell+`}]`)
		c, _ := client(t, "Kunden")
		result, err := c.SearchRows(context.Background(), SearchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), linkCanary) || strings.Contains(string(encoded), "display_value") ||
			!strings.Contains(string(encoded), `"Vorgang":[{"row_id":"`+otherRowID+`"}]`) {
			t.Errorf("%s: link output = %s", name, encoded)
		}
	}
	for name, cell := range map[string]string{
		"string":         `"` + linkCanary + `"`,
		"plain strings":  `["` + linkCanary + `"]`,
		"object":         `{"display_value":"` + linkCanary + `"}`,
		"no identifiers": `[{"display_value":"` + linkCanary + `"}]`,
	} {
		serveSearch(t, `[{"_id":"`+rowID+`","Name":"Bike","Vorgang":`+cell+`}]`)
		c, _ := client(t, "Kunden")
		result, err := c.SearchRows(context.Background(), SearchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), linkCanary) || strings.Contains(string(encoded), "Vorgang") {
			t.Errorf("%s: an unexpected link shape was passed on: %s", name, encoded)
		}
	}

	// An allow-listed target table keeps its display values, and a wildcard keeps all of them.
	serveSearch(t, `[{"_id":"`+rowID+`","Vorgang":[{"row_id":"`+otherRowID+`","display_value":"`+linkCanary+`"}]}]`)
	red := &redact.Redactor{}
	allowed, err := open(context.Background(), &config.Resolved{
		Name: "sales", Provider: Provider, BaseURL: cloudOrigin, Service: "seatable", Credential: "sales-reader",
		Targets: []string{"Kunden", "id:0001"},
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIToken: salesEnv}},
	}, resolver(red), red, freeLimiter())
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*Client{"allow-list": allowed, "wildcard": func() *Client { c, _ := client(t, "*"); return c }()} {
		table := "Kunden"
		result, err := c.SearchRows(context.Background(), SearchOptions{Table: table})
		if err != nil {
			t.Fatal(err)
		}
		if encoded, _ := json.Marshal(result); !strings.Contains(string(encoded), linkCanary) {
			t.Errorf("%s dropped an allowed display value: %s", name, encoded)
		}
	}
}

// One client reads the base metadata once, however many searches, column checks and link masks need it.
func TestClientReadsTheMetadataOnce(t *testing.T) {
	server := serveSearch(t, `[{"_id":"`+rowID+`","Vorgang":[{"row_id":"`+otherRowID+`","display_value":"x"}]}]`)
	c, _ := client(t, "Kunden")
	for i := 0; i < 3; i++ {
		if _, err := c.SearchRows(context.Background(), SearchOptions{
			Filters: []SearchFilter{{Column: "Name", Op: "eq", Value: raw(`"a"`)}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if n := server.metadataCalls.Load(); n != 1 {
		t.Errorf("metadata calls = %d, want 1", n)
	}
	if len(server.sqlBodies) != 3 {
		t.Errorf("sql requests = %d, want 3", len(server.sqlBodies))
	}
	// Every search of the client keeps its own statement and parameters.
	first, _ := server.statement(t, 0)
	last, _ := server.statement(t, 2)
	if !reflect.DeepEqual(first, last) {
		t.Errorf("statements differ: %s %s", first, last)
	}
}

func TestSearchDescriptorHidesSQLAndKeepsFullRisk(t *testing.T) {
	if rowsSearch.Risk != seatableReadRisk {
		t.Errorf("risk = %+v, want the risk of the other row reads", rowsSearch.Risk)
	}
	for _, forbidden := range []string{"sql", "view_name", "view_id", "url", "token", "base_uuid"} {
		if strings.Contains(string(rowsSearch.InputSchema), forbidden) {
			t.Errorf("the input schema offers %q", forbidden)
		}
	}
	if !strings.Contains(rowsSearch.Description, "does not apply a configured view") {
		t.Error("the description does not say that a view is not applied")
	}
}
