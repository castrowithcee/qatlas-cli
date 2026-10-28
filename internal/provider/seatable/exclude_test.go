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
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// internCanary stands for every value of the excluded table Intern, wherever it could surface.
const internCanary = "intern-value-canary-seatable-8f20"

// excludeMetadata holds Kunden (linked to Tickets and, through Geheim, to the excluded Intern, with a lookup
// through that link), Tickets, and Intern.
const excludeMetadata = `{"metadata":{"tables":[
 {"_id":"0000","name":"Kunden","columns":[
   {"key":"0000","name":"Name","type":"text"},
   {"key":"aaaa","name":"Vorgang","type":"link","data":{"table_id":"0000","other_table_id":"0001","link_id":"l1"}},
   {"key":"gggg","name":"Geheim","type":"link","data":{"table_id":"0000","other_table_id":"0002","link_id":"l2"}},
   {"key":"c001","name":"Titel","type":"link-formula","data":{"formula":"lookup","link_column_key":"gggg"}}],
  "views":[{"_id":"0000","name":"Standard"},{"_id":"Zz99","name":"Archiv"}]},
 {"_id":"0001","name":"Tickets","columns":[{"key":"0000","name":"Titel","type":"text"}],
  "views":[{"_id":"0000","name":"Standard"}]},
 {"_id":"0002","name":"Intern","columns":[{"key":"0000","name":"Secret","type":"text"},
   {"key":"rrrr","name":"Rueck","type":"link","data":{"table_id":"0002","other_table_id":"0000","link_id":"l2"}}],
  "views":[{"_id":"0000","name":"Standard"}]}
]}}`

const excludeRow = `{"_id":"` + rowID + `","Name":"Bike","Vorgang":[{"row_id":"` + otherRowID +
	`","display_value":"visible-link-value"}],"Geheim":[{"row_id":"` + otherRowID +
	`","display_value":"` + internCanary + `"}],"Titel":["` + internCanary + `"]}`

const excludeViews = `{"views":[{"_id":"0000","name":"Standard","type":"table","formula_rows":{"x":1},"link_rows":{"y":1}},
 {"_id":"Zz99","name":"Archiv","type":"table"}]}`

// excludeServer answers a base that holds the excluded table Intern and counts every request that mentions it.
type excludeServer struct {
	mu       sync.Mutex
	metadata string
	requests []string
	interns  []string
	metaRead int
}

func serveExclude(t *testing.T) *excludeServer {
	t.Helper()
	s := &excludeServer{metadata: excludeMetadata}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if request.URL.Path == metaRoute(salesBase) {
			s.metaRead++
			return jsonResponse(http.StatusOK, s.metadata), nil
		}
		var body string
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			body = string(data)
		}
		line := request.Method + " " + request.URL.Path + "?" + request.URL.RawQuery + " " + body
		s.requests = append(s.requests, line)
		if strings.Contains(line, "Intern") || strings.Contains(line, "0002") {
			s.interns = append(s.interns, line)
		}
		switch {
		case strings.HasSuffix(request.URL.Path, viewsPath):
			return jsonResponse(http.StatusOK, excludeViews), nil
		case strings.HasSuffix(request.URL.Path, sqlPath):
			return jsonResponse(http.StatusOK, `{"metadata":[],"results":[`+excludeRow+`]}`), nil
		case strings.HasSuffix(request.URL.Path, activitiesPath):
			return jsonResponse(http.StatusOK, `{"activities":[{"row_id":"`+rowID+`","row_data":{"Name":"Bike",
 "Geheim":[{"row_id":"`+otherRowID+`","display_value":"`+internCanary+`"}],"c001":"`+internCanary+`"}}]}`), nil
		case strings.HasSuffix(request.URL.Path, operationsPath):
			return jsonResponse(http.StatusOK, `{"operations":[
 {"op_type":"modify_row","table_name":"Intern","row_data":{"Note":"`+internCanary+`"}},
 {"op_type":"modify_row","table_id":"0002","row_data":{"Note":"`+internCanary+`"}},
 {"op_type":"modify_row","detail":{"nested":[{"other_table_id":"0002"}]},"v":"`+internCanary+`"},
 {"op_type":"modify_row","table_id":"9999","v":"`+internCanary+`"},
 {"op_type":"modify_row","table_name":"Kunden","row_data":{"Name":"ok-kunden"}},
 {"op_type":"link_rows","table1_name":"Kunden","table2_id":"0001","v":"ok-link"},
 {"op_type":"link_rows","table1_name":"Kunden","table2_id":"0002","v":"`+internCanary+`"},
 {"op_type":"login","who":"ok-no-table"}]}`), nil
		case strings.HasSuffix(request.URL.Path, rowID+"/"):
			return jsonResponse(http.StatusOK, excludeRow), nil
		case strings.HasSuffix(request.URL.Path, rowsPath):
			if request.Method == http.MethodGet {
				return jsonResponse(http.StatusOK, `{"rows":[`+excludeRow+`]}`), nil
			}
		}
		return jsonResponse(http.StatusOK, `{"success":true}`), nil
	})
	return s
}

func (s *excludeServer) changes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, line := range s.requests {
		if !strings.HasPrefix(line, "GET ") {
			out = append(out, line)
		}
	}
	return out
}

func excludeResolved(targets []string, excludes ...string) *config.Resolved {
	return &config.Resolved{
		Name: "sales", Provider: Provider, BaseURL: cloudOrigin, Service: "seatable", Credential: "sales-reader",
		Targets: targets, Exclude: excludes,
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIToken: salesEnv}},
	}
}

func excludeClient(t *testing.T, targets []string, excludes ...string) *Client {
	t.Helper()
	red := &redact.Redactor{}
	c, err := open(context.Background(), excludeResolved(targets, excludes...), resolver(red), red, freeLimiter())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseScopeReadsExclusionsAndRefusesBadOnesWithoutQuotingThem(t *testing.T) {
	for name, tt := range map[string]struct {
		targets, excludes []string
		wantErr           bool
		wildcard          bool
		wantExcluded      int
	}{
		"wildcard with names and ids": {[]string{"*"}, []string{"Intern", "id:0002", "Kunden/Archiv", "id:0000/id:Zz99"}, false, true, 4},
		"allow-list with a view":      {[]string{"Kunden", "Tickets"}, []string{"Kunden/Archiv"}, false, false, 1},
		"no exclusions":               {[]string{"*"}, nil, false, true, 0},
		"wildcard in exclude":         {[]string{"Kunden"}, []string{"*"}, true, false, 0},
		"duplicate exclusion":         {[]string{"*"}, []string{"Intern", "Intern"}, true, false, 0},
		"exclusion equal to target":   {[]string{"Kunden"}, []string{"Kunden"}, true, false, 0},
		"malformed exclusion":         {[]string{"*"}, []string{"id:"}, true, false, 0},
	} {
		got, err := parseScope(excludeResolved(tt.targets, tt.excludes...))
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if err != nil {
			for _, value := range tt.excludes {
				if value != "" && value != "*" && strings.Contains(err.Error(), value) {
					t.Errorf("%s: error quotes %q: %v", name, value, err)
				}
			}
			continue
		}
		if got.wildcard != tt.wildcard || len(got.excluded) != tt.wantExcluded {
			t.Errorf("%s: scope = %+v", name, got)
		}
	}
	bound, _ := parseScope(excludeResolved([]string{"*"}, "Intern"))
	if bound.wholeBase() {
		t.Error("the wildcard with an exclusion must not count as the whole base")
	}
	bound, _ = parseScope(excludeResolved([]string{"*"}))
	if !bound.wholeBase() {
		t.Error("the wildcard without exclusions is the whole base")
	}
}

func TestRegisterDeclaresBothTargetKindsExcludable(t *testing.T) {
	reg := registry(t)
	meta, ok := reg.ProviderMetadata(Provider)
	if !ok || !meta.Target.Excludes() {
		t.Fatal("seatable.base does not declare exclusions")
	}
	for _, kind := range meta.Target.Kinds {
		if !(len(kind.ExcludeForms) > 0) {
			t.Errorf("kind %s is not excludable", kind.Name)
		}
	}
}

func TestOfferAndReachesDoNotDependOnExclusions(t *testing.T) {
	for _, tool := range []string{baseOperations.ID, tablesCreate.ID, tablesRename.ID, viewsCreate.ID, rowsList.ID} {
		withStar, _ := reaches(tool, []string{"*"})
		if !withStar {
			t.Errorf("%s: * must reach every tool", tool)
		}
	}
	if ok, _ := reaches(baseOperations.ID, []string{"Kunden"}); ok {
		t.Error("base.operations must stay out of reach of an allow-list")
	}
	if ok, _ := reaches(tablesRename.ID, []string{"Kunden/Archiv"}); ok {
		t.Error("a table change must stay out of reach of a view target")
	}
}

// textual refusals: decided from the spelling before the secret is resolved and before any request.
func TestExcludedTableIsRefusedBeforeSecretAndIO(t *testing.T) {
	refuse(t)
	resolved := excludeResolved([]string{"*"}, "Intern", "id:0009", "Kunden/Archiv")
	for name, tt := range map[string]struct {
		handler capability.Handler
		raw     string
	}{
		"rows.list":      {invokeRowsList, `{"table":"Intern"}`},
		"rows.list view": {invokeRowsList, `{"table":"Kunden/Archiv"}`},
		"rows.get":       {invokeRowsGet, `{"table":"Intern","row_id":"` + rowID + `"}`},
		"rows.search":    {invokeRowsSearch, `{"table":"Intern"}`},
		"rows.create":    {invokeRowsCreate, `{"table":"Intern","values":{"Secret":"x"}}`},
		"rows.update":    {invokeRowsUpdate, `{"table":"Intern","row_id":"` + rowID + `","values":{"Secret":"x"}}`},
		"rows.delete":    {invokeRowsDelete, `{"table":"Intern","row_id":"` + rowID + `"}`},
		"batch create":   {invokeRowsBatchCreate, `{"table":"Intern","rows":[{"Secret":"x"}]}`},
		"batch update":   {invokeRowsBatchUpdate, `{"table":"Intern","rows":[{"row_id":"` + rowID + `","values":{"Secret":"x"}}]}`},
		"batch delete":   {invokeRowsBatchDelete, `{"table":"Intern","row_ids":["` + rowID + `"]}`},
		"columns.list":   {invokeColumnsList, `{"table":"Intern"}`},
		"activities":     {invokeRowsActivities, `{"table":"Intern","row_id":"` + rowID + `"}`},
		"comments.list":  {invokeCommentsList, `{"table":"Intern","row_id":"` + rowID + `"}`},
		"files.get":      {invokeFilesGet, `{"table":"Intern","row_id":"` + rowID + `","column":"Datei"}`},
		"links.list":     {invokeLinksList, `{"table":"Intern","column":"Rueck","row_ids":["` + rowID + `"]}`},
		"views.list":     {invokeViewsList, `{"table":"Intern"}`},
		"views.get":      {invokeViewsGet, `{"table":"Intern","view":"Standard"}`},
		"table delete":   {invokeTablesChange("delete table", "delete", (*Client).DeleteTable, "deleted"), `{"table":"Intern"}`},
		"table rename":   {invokeTablesChange("rename table", "rename", (*Client).RenameTable, "renamed"), `{"table":"Intern","name":"Neu"}`},
		"column create":  {invokeColumnsChange("create column", "create", (*Client).CreateColumn, "created"), `{"table":"Intern","name":"X","type":"text"}`},
	} {
		_, err := tt.handler(context.Background(), resolved, nil, &redact.Redactor{}, json.RawMessage(tt.raw))
		if err == nil || !strings.Contains(err.Error(), "outside this connection's allow-list") {
			t.Errorf("%s: err = %v, want a refusal by the allow-list", name, err)
			continue
		}
		for _, leak := range []string{"Intern", "Archiv", "0002"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: the refusal names %q: %v", name, leak, err)
			}
		}
	}
}

func TestExclusionsInTheOtherSpellingAreDecidedByTheMetadata(t *testing.T) {
	server := serveExclude(t)
	for _, tt := range []struct {
		name, table string
		excludes    []string
	}{
		{"name against id", "Intern", []string{"id:0002"}},
		{"id against name", "id:0002", []string{"Intern"}},
		{"view name against id", "id:0000/id:Zz99", []string{"Kunden/Archiv"}},
		{"view id against name", "Kunden/Archiv", []string{"id:0000/id:Zz99"}},
		{"table of an excluded view name", "Kunden/id:Zz99", []string{"Kunden/Archiv"}},
	} {
		c := excludeClient(t, []string{"*"}, tt.excludes...)
		if _, err := c.ListRows(context.Background(), ListOptions{Table: tt.table, Limit: 5}); err == nil ||
			!strings.Contains(err.Error(), "outside this connection's allow-list") {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		if _, err := c.GetRowFrom(context.Background(), tt.table, rowID); err == nil {
			t.Errorf("%s: row read must be refused", tt.name)
		}
	}
	allow := excludeClient(t, []string{"Kunden", "Intern"}, "id:0002")
	if _, err := allow.ListRows(context.Background(), ListOptions{Table: "Intern", Limit: 5}); err == nil {
		t.Error("an allow-listed table must be refused when an exclusion names it by identifier")
	}
	if len(server.interns) != 0 || len(server.requests) != 0 {
		t.Errorf("requests = %v, want only metadata reads", server.requests)
	}
}

func TestTheRowsOfTheTableStayReachableWhenOnlyAViewIsExcluded(t *testing.T) {
	serveExclude(t)
	c := excludeClient(t, []string{"*"}, "Kunden/Archiv")
	if _, err := c.ListRows(context.Background(), ListOptions{Table: "Kunden", Limit: 5}); err != nil {
		t.Errorf("rows of the table = %v", err)
	}
	if _, err := c.ListRows(context.Background(), ListOptions{Table: "Kunden/Standard", Limit: 5}); err != nil {
		t.Errorf("rows of another view = %v", err)
	}
	if _, err := c.SearchRows(context.Background(), SearchOptions{Table: "Kunden"}); err != nil {
		t.Errorf("search = %v", err)
	}
}

func TestTablesListLeavesTheExcludedTableOutAndKeepsAValidityRule(t *testing.T) {
	server := serveExclude(t)
	c := excludeClient(t, []string{"*"}, "Intern")
	result, err := c.ListTables(context.Background(), PageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if len(result.Tables) != 2 || strings.Contains(string(encoded), "Intern") || strings.Contains(string(encoded), "0002") {
		t.Errorf("tables = %s", encoded)
	}
	for _, table := range result.Tables {
		if !strings.HasPrefix(table.Reference, "id:") {
			t.Errorf("reference = %q, want the id form", table.Reference)
		}
	}

	// An invalid entry of an excluded table is ignored; one of a reachable table fails the call.
	server.metadata = strings.Replace(excludeMetadata, `"name":"Intern"`, `"name":""`, 1)
	bad := excludeClient(t, []string{"*"}, "id:0002")
	if result, err = bad.ListTables(context.Background(), PageOptions{}); err != nil || len(result.Tables) != 2 {
		t.Errorf("excluded invalid table: %v %v", result, err)
	}
	server.metadata = strings.Replace(excludeMetadata, `"name":"Tickets"`, `"name":""`, 1)
	worse := excludeClient(t, []string{"*"}, "id:0002")
	if _, err := worse.ListTables(context.Background(), PageOptions{}); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("reachable invalid table: %v", err)
	}
}

func TestExclusionsByNameFailClosedWhenTheyResolveToNothing(t *testing.T) {
	server := serveExclude(t)
	// Intern was renamed behind the connection's back: the name finds nothing, so nothing is answered.
	server.metadata = strings.ReplaceAll(excludeMetadata, `"name":"Intern"`, `"name":"Intern2"`)
	for name, excludes := range map[string][]string{
		"table name": {"Intern"},
		"view name":  {"Kunden/Weg"},
	} {
		c := excludeClient(t, []string{"*"}, excludes...)
		_, err := c.ListRows(context.Background(), ListOptions{Table: "Tickets", Limit: 5})
		if err == nil || strings.Contains(err.Error(), excludes[0]) {
			t.Errorf("%s: err = %v, want a refusal without the value", name, err)
		}
		if _, err := c.ListTables(context.Background(), PageOptions{}); err == nil {
			t.Errorf("%s: tables.list must fail too", name)
		}
	}
	if len(server.requests) != 0 {
		t.Errorf("requests = %v, want none beyond the metadata", server.requests)
	}
	// An exclusion by identifier that matches nothing is valid and has no effect.
	server.metadata = excludeMetadata
	c := excludeClient(t, []string{"*"}, "id:9999", "id:0001/id:nope")
	if _, err := c.ListRows(context.Background(), ListOptions{Table: "Tickets", Limit: 5}); err != nil {
		t.Errorf("unmatched id exclusion = %v", err)
	}
	// The metadata is read once per client.
	if _, err := c.ListTables(context.Background(), PageOptions{}); err != nil {
		t.Fatal(err)
	}
	before := server.metaRead
	if _, err := c.ListColumns(context.Background(), ColumnsOptions{Table: "Tickets"}); err != nil {
		t.Fatal(err)
	}
	if server.metaRead != before {
		t.Errorf("metadata reads grew from %d to %d", before, server.metaRead)
	}
}

func TestConnectionTestResolvesExclusionsAndRefusesOnesEqualToATarget(t *testing.T) {
	server := serveExclude(t)
	for name, tt := range map[string]struct {
		targets, excludes []string
		metadata          string
		want              string
	}{
		"wildcard and exclusions":     {[]string{"*"}, []string{"Intern", "id:9999"}, excludeMetadata, "ok"},
		"unresolved name":             {[]string{"*"}, []string{"Weg"}, excludeMetadata, "invalid-response"},
		"name against id":             {[]string{"Kunden"}, []string{"id:0000"}, excludeMetadata, "invalid-response"},
		"id against name":             {[]string{"id:0001"}, []string{"Tickets"}, excludeMetadata, "invalid-response"},
		"view target and its view":    {[]string{"Kunden/Archiv"}, []string{"id:0000/id:Zz99"}, excludeMetadata, "invalid-response"},
		"view target and its table":   {[]string{"Kunden/Archiv"}, []string{"id:0000"}, excludeMetadata, "invalid-response"},
		"table target, view excluded": {[]string{"Kunden"}, []string{"Kunden/Archiv"}, excludeMetadata, "ok"},
		"allow-list, other excluded":  {[]string{"Kunden"}, []string{"Intern"}, excludeMetadata, "ok"},
	} {
		server.metadata = tt.metadata
		c := excludeClient(t, tt.targets, tt.excludes...)
		class, err := c.testConnection(context.Background())
		if tt.want == "ok" && (err != nil || class != "ok") || tt.want != "ok" && (err == nil && string(class) == "ok") {
			t.Errorf("%s: class %q err %v", name, class, err)
		}
		if err != nil {
			for _, value := range tt.excludes {
				if strings.Contains(err.Error(), value) {
					t.Errorf("%s: error quotes %q: %v", name, value, err)
				}
			}
		}
	}
}

func TestEveryOutputOmitsTheExcludedTableAndNoRequestReachesIt(t *testing.T) {
	server := serveExclude(t)
	c := excludeClient(t, []string{"*"}, "Intern")
	ctx := context.Background()
	var outputs []any
	collect := func(value any, err error) {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		outputs = append(outputs, value)
	}
	collect(c.ListTables(ctx, PageOptions{}))
	collect(c.ListColumns(ctx, ColumnsOptions{Table: "Kunden"}))
	collect(c.ListRows(ctx, ListOptions{Table: "Kunden", Limit: 5}))
	collect(c.GetRowFrom(ctx, "Kunden", rowID))
	collect(c.SearchRows(ctx, SearchOptions{Table: "Kunden"}))
	collect(c.RowActivities(ctx, ActivitiesOptions{Table: "Kunden", RowID: rowID}))
	collect(c.ListViews(ctx, ViewInput{Table: "Kunden"}))
	collect(c.BaseOperations(ctx, OperationsOptions{}))
	for i, output := range outputs {
		encoded, _ := json.Marshal(output)
		if strings.Contains(string(encoded), internCanary) {
			t.Errorf("output %d leaked a value of the excluded table: %s", i, encoded)
		}
		if i != 1 && i != 6 && !strings.Contains(string(encoded), `"Name"`) && i != 0 && i != 7 {
			t.Errorf("output %d lost its plain content: %s", i, encoded)
		}
	}
	if len(server.interns) != 0 {
		t.Errorf("requests reached the excluded table: %v", server.interns)
	}
	// The link into the excluded table keeps only its row identifier, the lookup through it is omitted.
	rows, _ := json.Marshal(outputs[2])
	if !strings.Contains(string(rows), `"Geheim":[{"row_id":"`+otherRowID+`"}]`) ||
		!strings.Contains(string(rows), `"omitted_fields":["Titel"]`) ||
		!strings.Contains(string(rows), "visible-link-value") {
		t.Errorf("rows = %s", rows)
	}
	columns, _ := json.Marshal(outputs[1])
	if strings.Contains(string(columns), `"other_table_id":"0002"`) {
		t.Errorf("columns still name the excluded table: %s", columns)
	}
}

func TestLinksColumnsAndTableChangesRefuseReachIntoTheExcludedTable(t *testing.T) {
	server := serveExclude(t)
	ctx := context.Background()
	c := excludeClient(t, []string{"*"}, "Intern")
	const refusal = "outside this connection's allow-list"
	_, err := c.ListLinks(ctx, linkInput{Table: "Kunden", Column: "Geheim", RowIDs: []string{rowID}})
	check := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), refusal) || strings.Contains(err.Error(), "Intern") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	check("links.list", err)
	check("links.create", c.ChangeLinks(ctx, "create links", http.MethodPost,
		linkInput{Table: "Kunden", Column: "Geheim", RowID: rowID, OtherRowIDs: []string{otherRowID}}))
	check("links.update", c.ChangeLinks(ctx, "update links", http.MethodPut,
		linkInput{Table: "Kunden", Column: "Geheim", RowID: rowID, OtherRowIDs: []string{otherRowID}}))
	check("links.delete", c.ChangeLinks(ctx, "delete links", http.MethodDelete,
		linkInput{Table: "Kunden", Column: "Geheim", RowID: rowID, OtherRowIDs: []string{otherRowID}}))
	check("column create link", c.CreateColumn(ctx, "create column", ColumnInput{Table: "Kunden", Name: "Neu", Type: "link",
		Data: &ColumnData{LinkTable: "Intern"}}))
	check("column create link by id", c.CreateColumn(ctx, "create column", ColumnInput{Table: "Kunden", Name: "Neu2", Type: "link",
		Data: &ColumnData{LinkTable: "id:0002"}}))
	check("column update", c.UpdateColumn(ctx, "update column", ColumnInput{Table: "Kunden", Column: "Geheim", Name: "Anders"}))
	check("column delete", c.DeleteColumn(ctx, "delete column", ColumnInput{Table: "Kunden", Column: "Geheim"}))
	check("table delete", c.DeleteTable(ctx, "delete table", TableInput{Table: "Kunden"}))
	check("table duplicate", c.DuplicateTable(ctx, "duplicate table", TableInput{Table: "Kunden"}))
	if changes := server.changes(); len(changes) != 0 {
		t.Errorf("changes were sent: %v", changes)
	}
	if err := c.DeleteTable(ctx, "delete table", TableInput{Table: "Tickets"}); err != nil {
		t.Errorf("a table without such a link can be deleted: %v", err)
	}
	if err := c.CreateTable(ctx, "create table", TableInput{Name: "Neu"}); err != nil {
		t.Errorf("a wildcard with exclusions can create tables: %v", err)
	}
}

func TestViewsHideExcludedViewsAndOnlyShowLinkMembersWithoutLinksIntoExcludedTables(t *testing.T) {
	serveExclude(t)
	ctx := context.Background()
	c := excludeClient(t, []string{"*"}, "Kunden/Archiv")
	list, err := c.ListViews(ctx, ViewInput{Table: "Tickets"})
	if err != nil || len(list.Views) != 2 {
		t.Fatalf("views = %+v %v", list, err)
	}
	if list.Views[0].FormulaRows == nil || list.Views[0].LinkRows == nil {
		t.Errorf("a table without links keeps formula_rows and link_rows: %+v", list.Views[0])
	}
	list, err = c.ListViews(ctx, ViewInput{Table: "Kunden"})
	if err != nil || len(list.Views) != 1 || list.Views[0].Name != "Standard" {
		t.Fatalf("views = %+v %v", list, err)
	}
	// Kunden links into Tickets only here, so the members stay; with Intern excluded they go.
	hidden := excludeClient(t, []string{"*"}, "Intern")
	list, err = hidden.ListViews(ctx, ViewInput{Table: "Kunden"})
	if err != nil || list.Views[0].FormulaRows != nil || list.Views[0].LinkRows != nil {
		t.Errorf("link members of a table that links into the excluded table: %+v %v", list, err)
	}
	for name, run := range map[string]func() error{
		"get":       func() error { _, err := c.GetView(ctx, ViewInput{Table: "Kunden", View: "Archiv"}); return err },
		"get by id": func() error { _, err := c.GetView(ctx, ViewInput{Table: "Kunden", View: "id:Zz99"}); return err },
		"update": func() error {
			return c.UpdateView(ctx, "update view", ViewInput{Table: "Kunden", View: "Archiv", Name: "Neu"})
		},
		"delete": func() error { return c.DeleteView(ctx, "delete view", ViewInput{Table: "Kunden", View: "id:Zz99"}) },
	} {
		err := run()
		if err == nil || !strings.Contains(err.Error(), "has no view with this name or identifier") {
			t.Errorf("%s: err = %v, want the answer for a missing view", name, err)
		}
	}
}

func TestNamesThatAnExclusionClaimsCannotBeCreatedOrRenamedTo(t *testing.T) {
	refuse(t)
	resolved := excludeResolved([]string{"*"}, "Intern", "Kunden/Archiv")
	secrets := secret.NewWith(func(string) string { return salesToken }, nil, nil, &redact.Redactor{})
	for name, tt := range map[string]struct {
		handler capability.Handler
		raw     string
	}{
		"table create": {invokeTablesChange("create table", "create", (*Client).CreateTable, "created"), `{"name":"Intern"}`},
		"table rename": {invokeTablesChange("rename table", "rename", (*Client).RenameTable, "renamed"), `{"table":"Tickets","name":"Intern"}`},
		"view create":  {invokeViewsChange("create view", "create", (*Client).CreateView, "created"), `{"table":"Kunden","name":"Archiv"}`},
		"view rename":  {invokeViewsChange("update view", "update", (*Client).UpdateView, "updated"), `{"table":"Kunden","view":"Standard","name":"Archiv"}`},
	} {
		_, err := tt.handler(context.Background(), resolved, secrets, &redact.Redactor{}, json.RawMessage(tt.raw))
		if err == nil || !strings.Contains(err.Error(), "claimed by an exclusion") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestNamesClaimedInTheOtherSpellingAreRefusedAfterTheMetadata(t *testing.T) {
	server := serveExclude(t)
	ctx := context.Background()
	c := excludeClient(t, []string{"*"}, "id:0000/Archiv")
	err := c.CreateView(ctx, "create view", ViewInput{Table: "Kunden", Name: "Archiv"})
	if err == nil || !strings.Contains(err.Error(), "claimed by an exclusion") {
		t.Errorf("create view = %v", err)
	}
	err = c.UpdateView(ctx, "update view", ViewInput{Table: "Kunden", View: "Standard", Name: "Archiv"})
	if err == nil || !strings.Contains(err.Error(), "claimed by an exclusion") {
		t.Errorf("rename view = %v", err)
	}
	// A table that a name-form exclusion depends on cannot be renamed.
	named := excludeClient(t, []string{"*"}, "Kunden/Archiv")
	err = named.RenameTable(ctx, "rename table", TableInput{Table: "id:0000", Name: "Anders"})
	if err == nil || !strings.Contains(err.Error(), "by name cannot be renamed") {
		t.Errorf("rename table = %v", err)
	}
	if changes := server.changes(); len(changes) != 0 {
		t.Errorf("changes were sent: %v", changes)
	}
}

func TestBaseOperationsDropEntriesOfExcludedOrUnknownTablesAndWarn(t *testing.T) {
	serveExclude(t)
	c := excludeClient(t, []string{"*"}, "Intern")
	result, err := c.BaseOperations(context.Background(), OperationsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	out := string(encoded)
	if strings.Contains(out, internCanary) {
		t.Errorf("operations leaked: %s", out)
	}
	for _, want := range []string{"ok-kunden", "ok-link", "ok-no-table"} {
		if !strings.Contains(out, want) {
			t.Errorf("operations lost %s: %s", want, out)
		}
	}
	if len(result.Items) != 3 || len(result.Warnings) != 1 || strings.Contains(result.Warnings[0], "Intern") {
		t.Errorf("items %d warnings %v", len(result.Items), result.Warnings)
	}

	// Without exclusions the log is unchanged and carries no warning.
	plain := excludeClient(t, []string{"*"})
	all, err := plain.BaseOperations(context.Background(), OperationsOptions{})
	if err != nil || len(all.Items) != 8 || len(all.Warnings) != 0 {
		t.Errorf("unfiltered log: %v %v", all, err)
	}

	// An allow-list still may not read the log, with or without exclusions.
	narrow := excludeClient(t, []string{"Kunden"}, "Kunden/Archiv")
	if _, err := narrow.BaseOperations(context.Background(), OperationsOptions{}); err == nil {
		t.Error("an allow-list must not read the operation log")
	}
	_, err = invokeBaseOperations(context.Background(), excludeResolved([]string{"Kunden"}, "Kunden/Archiv"), nil,
		&redact.Redactor{}, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "needs a connection with the * wildcard") {
		t.Errorf("handler = %v", err)
	}
}

func TestUnaffectedToolsStayAllowedWithExclusions(t *testing.T) {
	server := serveExclude(t)
	c := excludeClient(t, []string{"*"}, "Intern")
	ctx := context.Background()
	if err := c.DeleteRowFrom(ctx, "Tickets", rowID); err != nil {
		t.Errorf("rows.delete on a reachable table = %v", err)
	}
	if err := c.CreateRowIn(ctx, "Tickets", map[string]json.RawMessage{"Titel": json.RawMessage(`"x"`)}); err != nil {
		t.Errorf("rows.create on a reachable table = %v", err)
	}
	if len(server.interns) != 0 {
		t.Errorf("requests reached the excluded table: %v", server.interns)
	}
}
