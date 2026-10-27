package seatable

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const lookupCanary = "lookup-value-canary-seatable-3c71"

// lookupMetadata joins Kunden to Tickets through the link column aaaa. The lookup columns copy a value of
// one linked row; the rollup, the count and the plain formula aggregate. Two lookups name a link column
// that does not exist or none at all.
const lookupMetadata = `{"metadata":{"tables":[
 {"_id":"0000","name":"Kunden","columns":[
   {"key":"0000","name":"Name","type":"text"},
   {"key":"aaaa","name":"Vorgang","type":"link","data":{"table_id":"0000","other_table_id":"0001","link_id":"l1"}},
   {"key":"c001","name":"Titel","type":"link-formula","data":{"formula":"lookup","link_column_key":"aaaa"}},
   {"key":"c002","name":"Frueh","type":"link-formula","data":{"formula":"findmin","link_column_key":"aaaa"}},
   {"key":"c003","name":"Spaet","type":"link-formula","data":{"formula":"findmax","link_column_key":"aaaa"}},
   {"key":"c004","name":"Summe","type":"link-formula","data":{"formula":"rollup","link_column_key":"aaaa"}},
   {"key":"c005","name":"Anzahl","type":"link-formula","data":{"formula":"count_links","link_column_key":"aaaa"}},
   {"key":"c006","name":"Rechnung","type":"formula","data":{"formula":"1+1"}},
   {"key":"c007","name":"Ohne","type":"link-formula","data":{"formula":"lookup"}},
   {"key":"c008","name":"Unbekannt","type":"link-formula","data":{"formula":"lookup","link_column_key":"zzzz"}}],
  "views":[{"_id":"0000","name":"Standard"}]},
 {"_id":"0001","name":"Tickets","columns":[{"key":"0000","name":"Titel","type":"text"}],
  "views":[{"_id":"0000","name":"Standard"}]}
]}}`

const lookupRow = `{"_id":"` + rowID + `","Name":"Bike","Titel":["` + lookupCanary + `"],"Frueh":"` + lookupCanary +
	`","Spaet":"` + lookupCanary + `","Summe":42,"Anzahl":1,"Rechnung":2,"Ohne":["` + lookupCanary +
	`"],"Unbekannt":["` + lookupCanary + `"]}`

var lookupColumns = []string{"Titel", "Frueh", "Spaet", "Ohne", "Unbekannt"}

func serveLookups(t *testing.T) *int {
	t.Helper()
	metadataCalls := 0
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == metaRoute(salesBase):
			metadataCalls++
			return jsonResponse(http.StatusOK, lookupMetadata), nil
		case request.URL.Path == gatewayPath+salesBase+sqlPath:
			return jsonResponse(http.StatusOK, `{"metadata":[],"results":[`+lookupRow+`]}`), nil
		case strings.HasSuffix(request.URL.Path, activitiesPath):
			return jsonResponse(http.StatusOK, `{"activities":[{"row_id":"`+rowID+`","row_data":{"Name":"Bike",
 "Titel":["`+lookupCanary+`"],"c002":"`+lookupCanary+`","Summe":42}}]}`), nil
		case strings.HasSuffix(request.URL.Path, rowID+"/"):
			return jsonResponse(http.StatusOK, lookupRow), nil
		}
		return jsonResponse(http.StatusOK, `{"rows":[`+lookupRow+`]}`), nil
	})
	return &metadataCalls
}

func lookupClient(t *testing.T, targets ...string) *Client {
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

func lookupOutputs(t *testing.T, c *Client) []string {
	t.Helper()
	ctx := context.Background()
	list, err := c.ListRows(ctx, ListOptions{Table: "Kunden", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	row, err := c.GetRowFrom(ctx, "Kunden", rowID)
	if err != nil {
		t.Fatal(err)
	}
	search, err := c.SearchRows(ctx, SearchOptions{Table: "Kunden"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := c.RowActivities(ctx, ActivitiesOptions{Table: "Kunden", RowID: rowID})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, value := range []any{list, row, search, history} {
		encoded, _ := json.Marshal(value)
		out = append(out, string(encoded))
	}
	return out
}

func TestLookupsFromTablesOutsideTheAllowListAreOmitted(t *testing.T) {
	calls := serveLookups(t)
	for i, output := range lookupOutputs(t, lookupClient(t, "Kunden")) {
		if strings.Contains(output, lookupCanary) {
			t.Errorf("output %d leaked a lookup value: %s", i, output)
		}
		if !strings.Contains(output, `"Summe":42`) {
			t.Errorf("output %d lost the rollup: %s", i, output)
		}
		if i < 3 {
			if !strings.Contains(output, `"Anzahl":1`) || !strings.Contains(output, `"Rechnung":2`) {
				t.Errorf("output %d lost an aggregate: %s", i, output)
			}
			for _, name := range lookupColumns {
				if !strings.Contains(output, `"`+name+`"`) {
					t.Errorf("output %d does not name omitted column %s: %s", i, name, output)
				}
				if strings.Contains(output, `"`+name+`":`) {
					t.Errorf("output %d still holds column %s: %s", i, name, output)
				}
			}
		} else if !strings.Contains(output, `"omitted_fields":["Frueh","Titel"]`) {
			t.Errorf("history does not name the omitted columns: %s", output)
		}
	}
	if *calls != 1 {
		t.Errorf("metadata calls = %d, want one shared read", *calls)
	}
}

func TestLookupsFromAnAllowedTableStayExceptUndeterminableOnes(t *testing.T) {
	serveLookups(t)
	for i, output := range lookupOutputs(t, lookupClient(t, "Kunden", "id:0001")) {
		if i < 3 {
			if strings.Count(output, lookupCanary) != 3 {
				t.Errorf("output %d = %s, want the three determinable lookups", i, output)
			}
			for _, name := range []string{"Ohne", "Unbekannt"} {
				if strings.Contains(output, `"`+name+`":`) || !strings.Contains(output, `"`+name+`"`) {
					t.Errorf("output %d did not omit undeterminable %s: %s", i, name, output)
				}
			}
		} else if !strings.Contains(output, lookupCanary) {
			t.Errorf("history dropped an allowed lookup: %s", output)
		}
	}
}

func TestWildcardKeepsLookupsWithoutMetadata(t *testing.T) {
	calls := serveLookups(t)
	ctx := context.Background()
	c := lookupClient(t, "*")
	list, err := c.ListRows(ctx, ListOptions{Table: "Kunden", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	row, err := c.GetRowFrom(ctx, "Kunden", rowID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := c.RowActivities(ctx, ActivitiesOptions{Table: "Kunden", RowID: rowID})
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range []any{list, row, history} {
		output, _ := json.Marshal(value)
		if strings.Contains(string(output), "omitted_fields") || !strings.Contains(string(output), lookupCanary) {
			t.Errorf("output %d = %s, want every value", i, output)
		}
	}
	if *calls != 0 {
		t.Errorf("metadata calls = %d, want none for a wildcard", *calls)
	}
}

func TestListShapedActivitiesOmitLookupValues(t *testing.T) {
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == metaRoute(salesBase):
			return jsonResponse(http.StatusOK, lookupMetadata), nil
		case strings.HasSuffix(request.URL.Path, activitiesPath):
			return jsonResponse(http.StatusOK, `{"activities":[{"row_id":"`+rowID+`","row_data":[
 {"column_key":"c001","column_name":"x","column_type":"link-formula","value":["`+lookupCanary+`"],"old_value":"`+lookupCanary+`"},
 {"column_key":"zz","column_name":"Frueh","column_type":"link-formula","value":"`+lookupCanary+`","extra":"`+lookupCanary+`"},
 {"column_key":"0000","column_name":"Name","column_type":"text","value":"Bike"},
 {"column_key":"c004","column_name":"Summe","value":42}]}]}`), nil
		case strings.HasSuffix(request.URL.Path, rowID+"/"):
			return jsonResponse(http.StatusOK, lookupRow), nil
		}
		return jsonResponse(http.StatusOK, `{}`), nil
	})
	result, err := lookupClient(t, "Kunden").RowActivities(context.Background(), ActivitiesOptions{Table: "Kunden", RowID: rowID})
	if err != nil {
		t.Fatal(err)
	}
	output, _ := json.Marshal(result)
	if strings.Contains(string(output), lookupCanary) || strings.Count(string(output), `"omitted_fields"`) != 2 ||
		!strings.Contains(string(output), `"Bike"`) || !strings.Contains(string(output), `"value":42`) {
		t.Errorf("output = %s", output)
	}
}
