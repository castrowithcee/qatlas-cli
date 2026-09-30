package seatable

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const historyCanary = "history-value-canary-seatable-9e4d"

func activityBody() string {
	return `{"activities":[{"op_type":"modify_row","author":"person@example.invalid","row_id":"` + rowID + `",
 "row_data":{"Name":"` + historyCanary + `","Vorgang":[{"row_id":"` + otherRowID + `","display_value":"` + linkCanary + `"}],
  "Rueck":[{"row_id":"` + otherRowID + `","display_value":"` + linkCanary + `"}],
  "aaaa":[{"row_id":"` + otherRowID + `","display_value":"` + linkCanary + `"}]}}]}`
}

type historyCall struct{ path, query string }

func serveHistory(t *testing.T, answer string) *[]historyCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]historyCall{}
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		*calls = append(*calls, historyCall{request.URL.Path, request.URL.RawQuery})
		mu.Unlock()
		switch {
		case request.URL.Path == metaRoute(salesBase):
			return jsonResponse(http.StatusOK, linkMetadata), nil
		case strings.HasSuffix(request.URL.Path, rowID+"/"):
			return jsonResponse(http.StatusOK, `{"_id":"`+rowID+`","Name":"Bike"}`), nil
		case strings.HasSuffix(request.URL.Path, rowsPath+otherRowID+"/"):
			return jsonResponse(http.StatusNotFound, `{"error_msg":"`+bodyCanary+`"}`), nil
		}
		return jsonResponse(http.StatusOK, answer), nil
	})
	return calls
}

func TestHistoryToolsCarryTheHistorySensitivity(t *testing.T) {
	for _, d := range []string{rowsActivities.ID, baseOperations.ID} {
		reg := registry(t)
		found := false
		for _, descriptor := range reg.Provider(Provider) {
			if descriptor.ID == d {
				found = true
				if descriptor.Risk.DataSensitivity != "seatable-base-history" || descriptor.Risk.Confirmation != "none" {
					t.Errorf("%s risk = %+v", d, descriptor.Risk)
				}
			}
		}
		if !found {
			t.Errorf("%s is not registered", d)
		}
	}
}

func TestRowActivitiesReadTheRowFirstAndSendFixedQuery(t *testing.T) {
	calls := serveHistory(t, activityBody())
	c, _ := client(t, "*")
	result, err := c.RowActivities(context.Background(), ActivitiesOptions{Table: "Kunden", RowID: rowID, Page: 2, PerPage: 10})
	if err != nil || len(result.Items) != 1 || result.Page != 2 || result.PerPage != 10 {
		t.Fatalf("RowActivities = %+v, %v", result, err)
	}
	if len(*calls) != 2 || !strings.HasSuffix((*calls)[0].path, rowsPath+rowID+"/") ||
		!strings.HasSuffix((*calls)[1].path, activitiesPath) {
		t.Fatalf("calls = %+v, want the row read before the activities", *calls)
	}
	query := (*calls)[1].query
	for _, want := range []string{"table_name=Kunden", "row_id=" + rowID, "page=2", "per_page=10"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %s", query, want)
		}
	}
}

func TestRowActivitiesRefuseARowOutsideTheTableWithoutReadingHistory(t *testing.T) {
	calls := serveHistory(t, activityBody())
	c, _ := client(t, "Kunden")
	_, err := c.RowActivities(context.Background(), ActivitiesOptions{RowID: otherRowID})
	if err == nil || classOf(err) != "provider-error" {
		t.Fatalf("err = %v (%s)", err, classOf(err))
	}
	if strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("provider text leaked: %v", err)
	}
	for _, call := range *calls {
		if strings.HasSuffix(call.path, activitiesPath) {
			t.Errorf("the history was requested: %+v", call)
		}
	}
}

func TestRowActivitiesRefuseAForeignTableBeforeAnyIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, table := range []string{"Tickets", "id:0001"} {
		_, err := invokeRowsActivities(context.Background(), resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, "Kunden"),
			resolver(red), red, json.RawMessage(`{"table":"`+table+`","row_id":"`+rowID+`"}`))
		if err == nil || strings.Contains(err.Error(), table) {
			t.Errorf("table %q: err = %v", table, err)
		}
	}
}

func TestRowActivitiesReduceLinksOutsideTheAllowList(t *testing.T) {
	serveHistory(t, activityBody())
	c, _ := client(t, "Kunden")
	result, err := c.RowActivities(context.Background(), ActivitiesOptions{RowID: rowID})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), linkCanary) || strings.Contains(string(encoded), "display_value") ||
		strings.Count(string(encoded), `"row_id":"`+otherRowID+`"`) != 3 {
		t.Errorf("result = %s", encoded)
	}
	if !strings.Contains(string(encoded), historyCanary) {
		t.Errorf("plain values were dropped: %s", encoded)
	}
}

func TestRowActivitiesKeepAllowedLinksAndWildcardValues(t *testing.T) {
	serveHistory(t, activityBody())
	red := &redact.Redactor{}
	c, err := open(context.Background(), &config.Resolved{
		Name: "sales", Provider: Provider, BaseURL: cloudOrigin, Service: "seatable", Credential: "sales-reader",
		Targets: []string{"Kunden", "id:0001"},
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAPIToken: salesEnv}},
	}, resolver(red), red, freeLimiter())
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.RowActivities(context.Background(), ActivitiesOptions{Table: "Kunden", RowID: rowID})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	// Vorgang, Rueck and the key of Vorgang resolve to the allowed table.
	if strings.Count(string(encoded), linkCanary) != 3 {
		t.Errorf("allow-listed links = %s", encoded)
	}
	w, _ := client(t, "*")
	result, err = w.RowActivities(context.Background(), ActivitiesOptions{Table: "Kunden", RowID: rowID})
	encoded, _ = json.Marshal(result)
	if err != nil || strings.Count(string(encoded), linkCanary) != 3 {
		t.Errorf("wildcard = %s, %v", encoded, err)
	}
}

func TestRowActivitiesBoundStringsAndPageSize(t *testing.T) {
	long := strings.Repeat("ä", 5000)
	serveHistory(t, `{"activities":[{"text":"`+long+`"},{"text":"b"}]}`)
	c, _ := client(t, "*")
	if _, err := c.RowActivities(context.Background(), ActivitiesOptions{RowID: rowID, PerPage: 1, Table: "Kunden"}); err == nil {
		t.Error("more entries than the page allows were accepted")
	}
	result, err := c.RowActivities(context.Background(), ActivitiesOptions{RowID: rowID, PerPage: 5, Table: "Kunden"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items[0]) > maxDisplayBytes+64 {
		t.Errorf("string not clipped: %d bytes", len(result.Items[0]))
	}
	for _, bad := range []ActivitiesOptions{{RowID: "x"}, {RowID: rowID, PerPage: 101}, {RowID: rowID, Page: 1001}, {RowID: rowID, Page: -1}} {
		if _, err := c.RowActivities(context.Background(), bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestBaseOperationsNeedAWildcardBeforeSecretOrIO(t *testing.T) {
	refuse(t)
	for _, target := range []string{"Kunden", "id:0000/id:0000"} {
		_, err := invokeBaseOperations(context.Background(), resolvedConnection("sales", "sales-reader", salesEnv, cloudOrigin, target),
			nil, nil, json.RawMessage(`{}`))
		if err == nil || !strings.Contains(err.Error(), "wildcard") {
			t.Errorf("target %q: err = %v", target, err)
		}
	}
	c, _ := client(t, "Kunden")
	if _, err := c.BaseOperations(context.Background(), OperationsOptions{}); err == nil {
		t.Error("the client served the log to a table connection")
	}
}

func TestBaseOperationsReadOnePageAndCapIt(t *testing.T) {
	entries := make([]string, 120)
	for i := range entries {
		entries[i] = `{"op_type":"insert_row","author":"` + historyCanary + `"}`
	}
	calls := serveHistory(t, `{"operations":[`+strings.Join(entries, ",")+`]}`)
	c, _ := client(t, "*")
	result, err := c.BaseOperations(context.Background(), OperationsOptions{Page: 3})
	if err != nil || len(result.Items) != maxPageSize || !result.HasMore || result.Page != 3 {
		t.Fatalf("BaseOperations = %+v, %v", result, err)
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0].path, operationsPath) || (*calls)[0].query != "page=3" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestHistoryRejectsUnusableAnswersWithoutProviderText(t *testing.T) {
	for _, answer := range []string{`{}`, `{"activities":"` + bodyCanary + `"}`, `{"activities":[1]}`} {
		serveHistory(t, answer)
		c, _ := client(t, "*")
		_, err := c.RowActivities(context.Background(), ActivitiesOptions{Table: "Kunden", RowID: rowID})
		if err == nil || classOf(err) != "invalid-provider-response" || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("answer %q: err = %v", answer, err)
		}
	}
}

func TestRowActivitiesDropEntriesOfOtherRowsAndTables(t *testing.T) {
	foreign := "foreign-activity-canary-seatable-2c7a"
	serveHistory(t, `{"activities":[`+
		`{"row_id":"`+rowID+`","table_id":"0000","text":"keep-own"},`+
		`{"row_id":"`+otherRowID+`","text":"`+foreign+`"},`+
		`{"row_id":"`+rowID+`","table_name":"Tickets","text":"`+foreign+`"},`+
		`{"row_id":"`+rowID+`","table_id":"0001","text":"`+foreign+`"},`+
		`{"row_id":7,"text":"`+foreign+`"},`+
		`{"text":"keep-plain"}]}`)
	for _, target := range []string{"*", "Kunden"} {
		c, _ := client(t, target)
		result, err := c.RowActivities(context.Background(), ActivitiesOptions{Table: "Kunden", RowID: rowID})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), foreign) || len(result.Items) != 2 ||
			!strings.Contains(string(encoded), "keep-own") || !strings.Contains(string(encoded), "keep-plain") {
			t.Errorf("target %q result = %s", target, encoded)
		}
	}
}
