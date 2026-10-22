package seatable

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// authDomain is the domain of SeaTable's internal user identifiers.
const authDomain = "@" + "auth.local"

// fieldsMetadata follows the example answer of Get Metadata and adds the column kinds the boundary concerns.
// Table 0001 is the only other allowed table; table 0002 is outside every allow-list.
const fieldsMetadata = `{"metadata":{"tables":[
 {"_id":"0000","name":"Kunden","columns":[
  {"key":"0000","name":"Name","type":"text","width":200,"editable":true,"resizable":true,"draggable":true,
   "permission_type":"","permitted_users":[]},
  {"key":"d1","name":"Seit","type":"date","width":141.5,"editable":false,"resizable":true,"draggable":true,
   "data":{"format":"YYYY-MM-DD"},"permission_type":"specific","permitted_users":["a1b2` + authDomain + `","c3d4` + authDomain + `"]},
  {"key":"s1","name":"Status","type":"single-select","width":12345678901234567890,
   "data":{"options":[{"id":"x","name":"Of\u0007fen","color":"#fff"}]}},
  {"key":"l1","name":"Tickets","type":"link","width":200,"editable":true,
   "data":{"display_column_key":"0000","table_id":"0000","other_table_id":"0001","is_internal_link":false,
    "is_multiple":true,"is_row_from_view":false,"other_view_id":"vw01","link_id":"lk01"}},
  {"key":"l2","name":"Privat","type":"link",
   "data":{"display_column_key":"0000","table_id":"0000","other_table_id":"0002","is_internal_link":false,
    "is_multiple":true,"is_row_from_view":false,"other_view_id":"vw02","link_id":"lk02"}},
  {"key":"f1","name":"Summe Tickets","type":"link-formula",
   "data":{"formula":"count","link_column_key":"l1","level1_linked_table_column_key":"0000","result_type":"number",
    "display_column_key":"0000"}},
  {"key":"f2","name":"Summe Privat","type":"link-formula",
   "data":{"formula":"count","link_column_key":"l2","level1_linked_table_column_key":"0000","result_type":"number",
    "display_column_key":"0000","other_table_view":"vw02"}},
  {"key":"f3","name":"Summe Unklar","type":"link-formula",
   "data":{"formula":"count","link_column_key":"nope","linked_column_key":"zz","result_type":"number"}}],
  "views":[]},
 {"_id":"0001","name":"Tickets","columns":[{"key":"0000","name":"Titel","type":"text"}],"views":[]},
 {"_id":"0002","name":"Geheim","columns":[{"key":"0000","name":"Wert","type":"text"}],"views":[]}
]}}`

func serveFieldsMetadata(t *testing.T, body string) *int {
	t.Helper()
	metadataReads := 0
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metaRoute(salesBase) {
			metadataReads++
			return jsonResponse(http.StatusOK, body), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	return &metadataReads
}

// listedColumns returns the columns of table Kunden by name, as the tool reports them.
func listedColumns(t *testing.T, c *Client) map[string]map[string]any {
	t.Helper()
	result, err := c.ListColumns(context.Background(), ColumnsOptions{Table: "id:0000", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Columns []map[string]any `json:"columns"`
	}
	if err := json.Unmarshal([]byte(encoded(result)), &decoded); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, column := range decoded.Columns {
		out[column["name"].(string)] = column
	}
	return out
}

func dataOf(column map[string]any) map[string]any {
	data, _ := column["data"].(map[string]any)
	return data
}

func TestColumnsListReportsEveryColumnMember(t *testing.T) {
	reads := serveFieldsMetadata(t, fieldsMetadata)
	c := linksClient(t, "*")
	result, err := c.ListColumns(context.Background(), ColumnsOptions{Table: "id:0000", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if *reads != 1 {
		t.Errorf("metadata reads = %d, want 1", *reads)
	}
	got := map[string]string{}
	for _, column := range result.Columns {
		got[column.Name] = encoded(column)
	}
	want := map[string]string{
		"Name": `{"key":"0000","name":"Name","type":"text","width":200,"editable":true,"resizable":true,"draggable":true}`,
		"Seit": `{"key":"d1","name":"Seit","type":"date","width":141.5,"editable":false,"resizable":true,"draggable":true,` +
			`"data":{"format":"YYYY-MM-DD"},"permission_type":"specific","permitted_users":["a1b2` + authDomain + `","c3d4` + authDomain + `"]}`,
		"Status": `{"key":"s1","name":"Status","type":"single-select","width":12345678901234567890,` +
			`"data":{"options":[{"color":"#fff","id":"x","name":"Of fen"}]}}`,
		"Tickets": `{"key":"l1","name":"Tickets","type":"link","width":200,"editable":true,` +
			`"data":{"display_column_key":"0000","is_internal_link":false,"is_multiple":true,"is_row_from_view":false,` +
			`"link_id":"lk01","other_table_id":"0001","other_view_id":"vw01","table_id":"0000"}}`,
	}
	for name, line := range want {
		if got[name] != line {
			t.Errorf("%s\n got %s\nwant %s", name, got[name], line)
		}
	}
	if !strings.Contains(got["Privat"], `"link_id":"lk02"`) || !strings.Contains(got["Summe Privat"], "other_table_view") {
		t.Errorf("a wildcard connection lost joined-table members: %s / %s", got["Privat"], got["Summe Privat"])
	}
}

func TestColumnsListKeepsFormerOutputWithoutNewMembers(t *testing.T) {
	serveColumns(t, http.StatusOK, `{}`)
	c := linksClient(t, "*")
	result, err := c.ListColumns(context.Background(), ColumnsOptions{Table: "id:0000", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"table":"id:0000","columns":[{"key":"0000","name":"Name","type":"text"},` +
		`{"key":"a1","name":"Betrag","type":"number"},{"key":"a2","name":"Notiz","type":"text"}],` +
		`"start":0,"limit":3,"next_start":3,"has_more":true}`
	if got := encoded(result); got != want {
		t.Errorf("output changed:\n got %s\nwant %s", got, want)
	}
}

func TestColumnsListMasksJoinedTablesOutsideTheAllowList(t *testing.T) {
	serveFieldsMetadata(t, fieldsMetadata)
	columns := listedColumns(t, linksClient(t, "id:0000", "id:0001"))

	if data := dataOf(columns["Tickets"]); data["other_table_id"] != "0001" || data["link_id"] != "lk01" ||
		data["other_view_id"] != "vw01" || data["is_multiple"] != true {
		t.Errorf("allowed link data = %v", data)
	}
	if data := dataOf(columns["Summe Tickets"]); data["link_column_key"] != "l1" || data["level1_linked_table_column_key"] != "0000" {
		t.Errorf("allowed link-formula data = %v", data)
	}
	secrets := regexp.MustCompile(`0002|lk02|vw02|zz|table|_view_|link_id|display_column_key`)
	for _, name := range []string{"Privat", "Summe Privat", "Summe Unklar"} {
		line := encoded(columns[name])
		if loc := secrets.FindString(line); loc != "" {
			t.Errorf("%s leaks %q: %s", name, loc, line)
		}
	}
	if data := dataOf(columns["Privat"]); data["is_multiple"] != true || data["is_internal_link"] != false {
		t.Errorf("unrelated link members lost: %v", data)
	}
	if data := dataOf(columns["Summe Privat"]); data["formula"] != "count" || data["link_column_key"] != "l2" {
		t.Errorf("unrelated link-formula members lost: %v", data)
	}
}

func TestColumnsListOmitsOversizedMembers(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 9) + `1` + strings.Repeat(`}`, 9)
	many := "[" + strings.TrimSuffix(strings.Repeat("1,", 1001), ",") + "]"
	body := `{"metadata":{"tables":[{"_id":"0000","name":"Kunden","columns":[
 {"key":"b1","name":"Gross","type":"text","data":{"note":"` + strings.Repeat("x", 4100) + `"},"editable":true},
 {"key":"b2","name":"Tief","type":"text","data":` + deep + `,"width":5},
 {"key":"b3","name":"Viel","type":"text","data":{"list":` + many + `},"permitted_users":[1]},
 {"key":"b4","name":"Grenze","type":"text","data":{"list":[1,2,3],"x":{"a":{"b":{"c":{"d":{"e":{"f":1}}}}}}}},
 {"key":"b5","name":"Falsch","type":"text","width":"breit","editable":"ja"}],"views":[]}]}}`
	serveFieldsMetadata(t, body)
	columns := listedColumns(t, linksClient(t, "*"))
	cases := map[string]string{
		"Gross":  `{"key":"b1","name":"Gross","type":"text","editable":true,"omitted_fields":["data"]}`,
		"Tief":   `{"key":"b2","name":"Tief","type":"text","width":5,"omitted_fields":["data"]}`,
		"Viel":   `{"key":"b3","name":"Viel","type":"text","omitted_fields":["data","permitted_users"]}`,
		"Grenze": `{"key":"b4","name":"Grenze","type":"text","data":{"list":[1,2,3],"x":{"a":{"b":{"c":{"d":{"e":{"f":1}}}}}}}}`,
		"Falsch": `{"key":"b5","name":"Falsch","type":"text","omitted_fields":["width","editable"]}`,
	}
	for name, want := range cases {
		var expected any
		if err := json.Unmarshal([]byte(want), &expected); err != nil {
			t.Fatal(err)
		}
		if got := encoded(columns[name]); got != encoded(expected) {
			t.Errorf("%s\n got %s\nwant %s", name, got, want)
		}
	}
}

// outputCoverage checks every member a tool returned against its output schema and its field descriptions.
// A schema object without properties is open: its members are free provider data and are not listed. The
// members of a list entry are described by the list's own field, which has to name each of them.
func outputCoverage(t *testing.T, d capability.Descriptor, out any) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{}
	for _, f := range d.Fields {
		fields[f.Name] = f.Description
	}
	var walk func(path, field string, value any, schema map[string]any)
	walk = func(path, field string, value any, schema map[string]any) {
		switch typed := value.(type) {
		case map[string]any:
			properties, closed := schema["properties"].(map[string]any)
			if !closed {
				return
			}
			for key, item := range typed {
				sub, ok := properties[key].(map[string]any)
				if !ok {
					t.Errorf("%s: %s %q is not in the output schema", d.ID, path, key)
					continue
				}
				owner := field
				if owner == "" {
					owner = key
					if _, ok := fields[key]; !ok {
						t.Errorf("%s: %q has no field description", d.ID, key)
					}
				} else if !regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\b`).MatchString(fields[owner]) {
					t.Errorf("%s: field %q does not describe %q", d.ID, owner, key)
				}
				walk(path+"."+key, owner, item, sub)
			}
		case []any:
			items, _ := schema["items"].(map[string]any)
			for _, item := range typed {
				walk(path+"[]", field, item, items)
			}
		}
	}
	walk("", "", out, schema)
	properties, _ := schema["properties"].(map[string]any)
	var unnamed []string
	for key := range properties {
		if _, ok := fields[key]; !ok {
			unnamed = append(unnamed, key)
		}
	}
	sort.Strings(unnamed)
	if len(unnamed) > 0 {
		t.Errorf("%s: schema members without field description: %v", d.ID, unnamed)
	}
}

func descriptorOf(t *testing.T, id string) capability.Descriptor {
	t.Helper()
	for _, d := range registry(t).Provider(Provider) {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no descriptor %s", id)
	return capability.Descriptor{}
}

func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	serveFieldsMetadata(t, fieldsMetadata)
	result, err := linksClient(t, "*").ListColumns(context.Background(), ColumnsOptions{Table: "id:0000", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(encoded(result)), &out); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, column := range out["columns"].([]any) {
		for key := range column.(map[string]any) {
			seen[key] = true
		}
	}
	for _, key := range []string{"key", "name", "type", "width", "editable", "resizable", "draggable", "data",
		"permission_type", "permitted_users"} {
		if !seen[key] {
			t.Errorf("the full answer never produced %q", key)
		}
	}
	outputCoverage(t, descriptorOf(t, "seatable.base.columns.list"), out)

	// omitted_fields only appears for a left-out member, so a second answer covers it.
	serveFieldsMetadata(t, `{"metadata":{"tables":[{"_id":"0000","name":"Kunden","columns":[
 {"key":"b1","name":"Gross","type":"text","data":{"note":"`+strings.Repeat("x", 4100)+`"}}],"views":[]}]}}`)
	result, err = linksClient(t, "*").ListColumns(context.Background(), ColumnsOptions{Table: "id:0000"})
	if err != nil {
		t.Fatal(err)
	}
	out = nil
	_ = json.Unmarshal([]byte(encoded(result)), &out)
	if !strings.Contains(encoded(out), "omitted_fields") {
		t.Fatalf("no omitted_fields in %v", out)
	}
	outputCoverage(t, descriptorOf(t, "seatable.base.columns.list"), out)

	viewsOutputCoverage(t)
}
