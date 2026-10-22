package baserow

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Each entry mirrors the field schema of its type in the OpenAPI document, including the deprecated members.
const typedFieldsBody = `[
 {"id":1,"table_id":11,"name":"Name","order":0,"type":"text","primary":true,"read_only":false,"immutable_type":false,
  "immutable_properties":false,"description":"Der Name\u0007","database_id":1,"workspace_id":7,"db_index":true,
  "field_constraints":[{"type":"unique_with_empty"}],"text_default":"n/a"},
 {"id":2,"table_id":11,"name":"Notiz","order":1,"type":"long_text","long_text_enable_rich_text":true,"description":null},
 {"id":3,"table_id":11,"name":"Preis","order":2,"type":"number","number_decimal_places":2,"number_negative":false,
  "number_prefix":"EUR ","number_suffix":"","number_separator":"COMMA_PERIOD","number_default":"12.50"},
 {"id":4,"table_id":11,"name":"Bewertung","order":3,"type":"rating","max_value":5,"color":"dark-orange","style":"star"},
 {"id":5,"table_id":11,"name":"Aktiv","order":4,"type":"boolean","boolean_default":false},
 {"id":6,"table_id":11,"name":"Dauer","order":5,"type":"duration","duration_format":"h:mm:ss"},
 {"id":7,"table_id":11,"name":"Datum","order":6,"type":"date","date_format":"ISO","date_include_time":true,
  "date_time_format":"24","date_show_tzinfo":true,"date_force_timezone":"Europe/Berlin"},
 {"id":8,"table_id":11,"name":"Erstellt","order":7,"type":"created_on","date_format":"EU","date_include_time":false,
  "date_time_format":"12","date_show_tzinfo":false,"date_force_timezone":null},
 {"id":9,"table_id":11,"name":"Von","order":8,"type":"created_by","available_collaborators":[{"id":1,"name":"Ada\u0000"}]},
 {"id":10,"table_id":11,"name":"Team","order":9,"type":"multiple_collaborators","notify_user_when_added":true,
  "available_collaborators":[{"id":1,"name":"Ada"},{"id":2,"name":"Bob"}]},
 {"id":11,"table_id":11,"name":"Status","order":10,"type":"single_select","single_select_default":5,
  "select_options":[{"id":5,"value":"offen","color":"red"}]},
 {"id":12,"table_id":11,"name":"Tags","order":11,"type":"multiple_select","multiple_select_default":[5,6],
  "select_options":[{"id":5,"value":"a","color":"red"}]},
 {"id":13,"table_id":11,"name":"Passwort","order":12,"type":"password","allow_endpoint_authentication":true},
 {"id":14,"table_id":11,"name":"Bearbeiten","order":13,"type":"form_view_edit_row","form_view_id":31},
 {"id":15,"table_id":11,"name":"Knopf","order":14,"type":"button","label":"Go","has_workflow_actions":false,
  "requires_reconfiguration":true,"opens_new_tab":false},
 {"id":16,"table_id":11,"name":"KI","order":15,"type":"ai","ai_generative_ai_type":"openai","ai_generative_ai_model":"gpt",
  "ai_output_type":"text","ai_temperature":0.7,"ai_prompt":{"formula":"'x'","version":"0.1","mode":"simple"},
  "ai_file_field_id":null,"ai_auto_update":false,"long_text_enable_rich_text":false,"error":"boom"}]`

func fieldMaps(t *testing.T, result string) map[int64]map[string]any {
	t.Helper()
	var decoded struct {
		Fields []map[string]any `json:"fields"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("%v: %s", err, result)
	}
	out := map[int64]map[string]any{}
	for _, field := range decoded.Fields {
		out[int64(field["id"].(float64))] = field
	}
	return out
}

func listFields(t *testing.T, connection, body string) (string, []call) {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == allTablesPath {
			return jsonResponse(200, allTablesBody), nil
		}
		return jsonResponse(200, body), nil
	})
	result, err := env.invoke(fieldsList.ID, connection, `{"table_id":11}`)
	if err != nil {
		t.Fatalf("%s: %v", connection, err)
	}
	return result, calls
}

func TestFieldsListReportsTypeSpecificProperties(t *testing.T) {
	result, calls := listFields(t, "one", typedFieldsBody)
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	got := fieldMaps(t, result)
	want := map[int64]map[string]any{
		1: {"table_id": 11.0, "order": 0.0, "primary": true, "immutable_type": false, "immutable_properties": false,
			"description": "Der Name ", "database_id": 1.0, "workspace_id": 7.0, "db_index": true, "text_default": "n/a",
			"field_constraints": []any{map[string]any{"type": "unique_with_empty"}}},
		2: {"long_text_enable_rich_text": true},
		3: {"number_decimal_places": 2.0, "number_negative": false, "number_prefix": "EUR ",
			"number_separator": "COMMA_PERIOD", "number_default": "12.50"},
		4: {"max_value": 5.0, "color": "dark-orange", "style": "star"},
		5: {"boolean_default": false},
		6: {"duration_format": "h:mm:ss"},
		7: {"date_format": "ISO", "date_include_time": true, "date_time_format": "24", "date_show_tzinfo": true,
			"date_force_timezone": "Europe/Berlin"},
		8: {"date_format": "EU", "date_include_time": false, "date_time_format": "12", "date_show_tzinfo": false},
		9: {"available_collaborators": []any{map[string]any{"id": 1.0, "name": "Ada "}}},
		10: {"notify_user_when_added": true, "available_collaborators": []any{
			map[string]any{"id": 1.0, "name": "Ada"}, map[string]any{"id": 2.0, "name": "Bob"}}},
		11: {"single_select_default": 5.0},
		12: {"multiple_select_default": []any{5.0, 6.0}},
		13: {"allow_endpoint_authentication": true},
		14: {"form_view_id": 31.0},
		15: {"label": "Go", "has_workflow_actions": false, "requires_reconfiguration": true, "opens_new_tab": false},
		16: {"ai_generative_ai_type": "openai", "ai_generative_ai_model": "gpt", "ai_output_type": "text",
			"ai_temperature": 0.7, "ai_prompt": map[string]any{"formula": "'x'", "version": "0.1", "mode": "simple"},
			"ai_auto_update": false, "long_text_enable_rich_text": false, "error": "boom"},
	}
	base := map[string]bool{"id": true, "name": true, "type": true, "select_options": true, "read_only": true}
	for id, members := range want {
		field := got[id]
		for key, value := range members {
			if !reflect.DeepEqual(field[key], value) {
				t.Errorf("field %d %s = %#v, want %#v", id, key, field[key], value)
			}
		}
		for key := range field {
			if _, ok := members[key]; !ok && !base[key] && key != "table_id" && key != "order" {
				t.Errorf("field %d carries unexpected %s = %#v", id, key, field[key])
			}
		}
	}
	if strings.Contains(result, "\u0007") || strings.Contains(result, `\u0000`) {
		t.Errorf("control characters in %s", result)
	}
}

func TestFieldsListWithoutNewMembersKeepsFormerOutput(t *testing.T) {
	body := `[{"id":1,"name":"Name","type":"text","primary":true},` +
		`{"id":2,"name":"S","type":"single_select","select_options":[{"id":5,"value":"offen","color":"red"}]},` +
		`{"id":3,"name":"L","type":"link_row","link_row_table_id":12}]`
	result, _ := listFields(t, "list", body)
	want := `{"fields":[{"id":1,"name":"Name","type":"text","primary":true},` +
		`{"id":2,"name":"S","type":"single_select","select_options":[{"id":5,"value":"offen","color":"red"}]},` +
		`{"id":3,"name":"L","type":"link_row","link_table_id":12}],"count":3,"truncated":false}`
	var gotValue, wantValue any
	if json.Unmarshal([]byte(result), &gotValue) != nil || json.Unmarshal([]byte(want), &wantValue) != nil ||
		!reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("result = %s\nwant   = %s", result, want)
	}
}

// computedBody has a link into table 12 (field 3) and one into table 99 (field 4), each with a lookup and a rollup.
func computedBody(withForeignLink bool) string {
	body := `[{"id":3,"name":"Auftraege","type":"link_row","link_row_table_id":12,"link_row_related_field_id":30,` +
		`"link_row_limit_selection_view_id":40,"link_row_table_primary_field":"Titel","link_row_multiple_relationships":true,` +
		`"link_row_table":12,"link_row_related_field":30},` +
		`{"id":6,"name":"L1","type":"lookup","through_field_id":3,"through_field_name":"Auftraege","target_field_id":50,` +
		`"target_field_name":"Titel","formula_type":"array","array_formula_type":"text","nullable":true,"error":"e1"},` +
		`{"id":7,"name":"R1","type":"rollup","through_field_id":3,"target_field_id":50,"rollup_function":"sum",` +
		`"formula_type":"number","number_decimal_places":0,"error":null},` +
		`{"id":8,"name":"F","type":"formula","formula":"field('Auftraege')","formula_type":"text","nullable":false,"error":"e2"},` +
		`{"id":9,"name":"C","type":"count","through_field_id":3,"formula_type":"number"},` +
		`{"id":10,"name":"L2","type":"lookup","through_field_name":"Auftraege","target_field_id":50,"target_field_name":"Titel"},` +
		`{"id":11,"name":"L3","type":"lookup","through_field_id":999,"target_field_id":50,"target_field_name":"Titel"}`
	if withForeignLink {
		body += `,{"id":4,"name":"Lieferant","type":"link_row","link_row_table_id":99,"link_row_related_field_id":31,` +
			`"link_row_limit_selection_view_id":41,"link_row_table_primary_field":"Geheim"},` +
			`{"id":12,"name":"L4","type":"lookup","through_field_id":4,"through_field_name":"Lieferant","target_field_id":51,"target_field_name":"Geheim"},` +
			`{"id":13,"name":"R4","type":"rollup","through_field_id":4,"target_field_id":51,"rollup_function":"count"},` +
			`{"id":14,"name":"Leer","type":"link_row","link_row_table_id":null,"link_row_related_field_id":32}`
	}
	return body + `]`
}

func has(field map[string]any, keys ...string) []string {
	var present []string
	for _, key := range keys {
		if _, ok := field[key]; ok {
			present = append(present, key)
		}
	}
	return present
}

func TestFieldsListBindsRelationPropertiesToTargets(t *testing.T) {
	linkKeys := []string{"link_row_related_field_id", "link_row_limit_selection_view_id", "link_row_table_primary_field"}
	targetKeys := []string{"target_field_id", "target_field_name"}
	type expectation struct{ linkOpen, targetOpen, formulaOpen bool }
	cases := []struct {
		name, connection string
		foreign          bool
		want             expectation
	}{
		{"link inside", "list", false, expectation{true, true, true}},
		{"link outside the only target", "one", false, expectation{false, false, false}},
		{"foreign link present", "list", true, expectation{true, true, false}},
		{"wildcard", "all", true, expectation{true, true, true}},
		{"database target", "db", false, expectation{true, true, true}},
		{"database target with foreign link", "db", true, expectation{true, true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, calls := listFields(t, c.connection, computedBody(c.foreign))
			wantCalls := 1
			if c.connection == "db" {
				wantCalls = 2 // the table list that proves the link targets, as before
			}
			if len(calls) != wantCalls {
				t.Errorf("calls = %d, want %d", len(calls), wantCalls)
			}
			got := fieldMaps(t, result)
			check := func(label string, field map[string]any, keys []string, open bool) {
				t.Helper()
				present := has(field, keys...)
				if open && len(present) == 0 || !open && len(present) != 0 {
					t.Errorf("%s: %v present, open = %t: %v", label, present, open, field)
				}
			}
			check("link", got[3], linkKeys, c.want.linkOpen)
			check("lookup target", got[6], targetKeys, c.want.targetOpen)
			check("rollup target", got[7], []string{"target_field_id"}, c.want.targetOpen)
			check("formula text", got[8], []string{"formula"}, c.want.formulaOpen)
			check("lookup error", got[6], []string{"error"}, c.want.formulaOpen)
			check("formula error", got[8], []string{"error"}, c.want.formulaOpen)
			// A lookup without a determinable link field never shows its target.
			for _, id := range []int64{10, 11} {
				if present := has(got[id], targetKeys...); len(present) != 0 {
					t.Errorf("field %d shows %v without a determinable link table", id, present)
				}
			}
			if got[6]["through_field_id"] != 3.0 || got[9]["through_field_id"] != 3.0 || got[6]["formula_type"] != "array" {
				t.Errorf("through members missing: %v %v", got[6], got[9])
			}
			if c.foreign {
				for _, id := range []int64{12, 13} {
					wantOpen := c.connection == "all"
					if present := has(got[id], "target_field_id"); (len(present) != 0) != wantOpen {
						t.Errorf("foreign field %d target present = %v, want %t", id, present, wantOpen)
					}
				}
				if present := has(got[14], linkKeys...); len(present) != 0 {
					t.Errorf("unresolvable link shows %v", present)
				}
			}
			for _, field := range got {
				if len(has(field, "link_row_table", "link_row_related_field", "data_sync")) != 0 {
					t.Errorf("deprecated member in %v", field)
				}
			}
		})
	}
}

func TestFieldsListLeavesOutFreeValuesBeyondTheLimits(t *testing.T) {
	long := strings.Repeat("x", maxCellString+1)
	deep := `{"a":{"b":{"c":{"d":{"e":{"f":{"g":1}}}}}}}`
	var many []string
	for i := 0; i < maxCollaborators+5; i++ {
		many = append(many, `{"id":`+strings.Repeat("1", 1)+`,"name":"n"}`)
	}
	body := `[{"id":1,"name":"a","type":"ai","ai_prompt":{"formula":"` + long + `"},"field_constraints":` + deep + `},` +
		`{"id":2,"name":"b","type":"ai","ai_prompt":{"formula":"ok"},"field_constraints":[{"type":"x"}]},` +
		`{"id":3,"name":"c","type":"created_by","available_collaborators":[` + strings.Join(many, ",") + `]},` +
		`{"id":4,"name":"d","type":"ai","ai_prompt":{},"field_constraints":[]},` +
		`{"id":5,"name":"e","type":"number","number_default":"1e999999999999999999999999999999999999999999999999999999999999999999"}]`
	result, _ := listFields(t, "one", body)
	got := fieldMaps(t, result)
	if _, ok := got[1]["ai_prompt"]; ok || got[1]["ai_prompt_omitted"] != true ||
		got[1]["field_constraints_omitted"] != true || has(got[1], "field_constraints") != nil {
		t.Errorf("oversized free values = %v", got[1])
	}
	if got[2]["ai_prompt"] == nil || got[2]["ai_prompt_omitted"] != nil || got[2]["field_constraints"] == nil {
		t.Errorf("bounded free values = %v", got[2])
	}
	if n := len(got[3]["available_collaborators"].([]any)); n != maxCollaborators {
		t.Errorf("collaborators = %d", n)
	}
	if len(has(got[4], "ai_prompt", "ai_prompt_omitted", "field_constraints", "field_constraints_omitted")) != 0 {
		t.Errorf("empty free values = %v", got[4])
	}
	if has(got[5], "number_default") != nil {
		t.Errorf("invalid decimal = %v", got[5])
	}
}

func TestTablesListReportsOrder(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return jsonResponse(200, allTablesBody), nil })
	result, err := env.invoke(tablesList.ID, "list", `{}`)
	if err != nil || strings.Contains(result, "data_sync") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	var tables struct{ Tables []map[string]any }
	if json.Unmarshal([]byte(result), &tables) != nil || len(tables.Tables) != 2 ||
		tables.Tables[0]["order"] != 0.0 || tables.Tables[1]["order"] != 1.0 {
		t.Fatalf("tables = %s", result)
	}
	legacy := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `[{"id":11,"name":"Kunden","database_id":1}]`), nil
	})
	result, err = legacy.invoke(tablesList.ID, "one", `{}`)
	if err != nil || result != `{"count":1,"tables":[{"database_id":1,"id":11,"name":"Kunden"}],"truncated":false}` {
		t.Fatalf("legacy result = %s, err = %v", result, err)
	}
}

// Every member that fields.list or tables.list can output is in the output schema and in the field list.
func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	registry := registry(t)
	members := map[string][]string{}
	for tool, members0 := range map[string]any{
		fieldsList.ID: reflect.TypeOf(Field{}), tablesList.ID: reflect.TypeOf(Table{}),
	} {
		members[tool] = jsonMembers(members0.(reflect.Type))
	}
	for _, d := range registry.Provider(Provider) {
		want, ok := members[d.ID]
		if !ok {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		list := "fields"
		if d.ID == tablesList.ID {
			list = "tables"
		}
		items := schema["properties"].(map[string]any)[list].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
		described := map[string]bool{}
		for _, f := range d.Fields {
			described[f.Name] = true
		}
		var problems []string
		for _, member := range want {
			if _, ok := items[member]; !ok {
				problems = append(problems, member+" (schema)")
			}
			if !described[list+"[]."+member] {
				problems = append(problems, member+" (fields)")
			}
		}
		for member := range items {
			if !described[list+"[]."+member] {
				problems = append(problems, member+" (schema without field)")
			}
		}
		sort.Strings(problems)
		if len(problems) > 0 {
			t.Errorf("%s: %v", d.ID, problems)
		}
	}
}

// jsonMembers lists the JSON names of a struct, flattening embedded structs.
func jsonMembers(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous {
			out = append(out, jsonMembers(field.Type)...)
			continue
		}
		if name := strings.Split(field.Tag.Get("json"), ",")[0]; name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// Every member of a row or a rows page is in the output schema and in the field list of the tools that return it.
func TestEveryRowMemberIsInSchemaAndFields(t *testing.T) {
	registry := registry(t)
	want := map[string][]string{
		rowsGet.ID: jsonMembers(reflect.TypeOf(Row{})), rowsList.ID: jsonMembers(reflect.TypeOf(RowsPage{})),
		rowsSearch.ID: jsonMembers(reflect.TypeOf(RowsPage{})),
	}
	for _, d := range registry.Provider(Provider) {
		members, ok := want[d.ID]
		if !ok {
			continue
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		described := map[string]bool{}
		for _, f := range d.Fields {
			described[f.Name] = true
		}
		for _, member := range members {
			if _, ok := schema.Properties[member]; !ok || !described[member] {
				t.Errorf("%s: %s is missing in the schema or the fields", d.ID, member)
			}
		}
		if len(schema.Properties) != len(members) || len(described) != len(members) {
			t.Errorf("%s: schema has %d members and fields %d, want %d", d.ID, len(schema.Properties), len(described), len(members))
		}
	}
}
