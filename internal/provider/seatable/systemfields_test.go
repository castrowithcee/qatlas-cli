package seatable

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const systemRow = `{"_id":"` + rowID + `","_ctime":"c","_mtime":"m","_creator":"a1` + authDomain + `",` +
	`"_last_modifier":"b2` + authDomain + `","_locked":true,"_locked_by":"c3` + authDomain + `","_archived":true,` +
	`"_unknown":"x","Name":"Bike"}`

func checkSystemRow(t *testing.T, row Row) {
	t.Helper()
	if row.Creator != "a1"+authDomain || row.LastModifier != "b2"+authDomain || row.Locked == nil || !*row.Locked ||
		row.LockedBy != "c3"+authDomain || row.Archived == nil || !*row.Archived {
		t.Errorf("envelope = %+v", row)
	}
	if len(row.Values) != 1 || string(row.Values["Name"]) != `"Bike"` {
		t.Errorf("values = %v, want only Name", row.Values)
	}
}

func TestRowsCarrySystemFieldsInTheEnvelope(t *testing.T) {
	serveBase(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, rowsPath+rowID+"/") {
			return jsonResponse(http.StatusOK, systemRow), nil
		}
		return jsonResponse(http.StatusOK, `{"rows":[`+systemRow+`,{"_id":"`+otherRowID+`","Name":"Plain"}]}`), nil
	})
	c, _ := client(t, "Kunden")
	page, err := c.ListRows(context.Background(), ListOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	checkSystemRow(t, page.Rows[0])
	plain, _ := json.Marshal(page.Rows[1])
	if string(plain) != `{"id":"`+otherRowID+`","values":{"Name":"Plain"}}` {
		t.Errorf("plain row = %s", plain)
	}
	row, err := c.GetRow(context.Background(), rowID)
	if err != nil {
		t.Fatal(err)
	}
	checkSystemRow(t, *row)
	outputCoverage(t, descriptorOf(t, "seatable.base.rows.get"), toMap(t, row))
	outputCoverage(t, descriptorOf(t, "seatable.base.rows.list"), toMap(t, page))
}

func toMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, _ := json.Marshal(v)
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSearchRowsCarrySystemFieldsWhenTheAnswerHasThem(t *testing.T) {
	serveSearch(t, `[`+systemRow+`]`)
	c, _ := client(t, "Kunden")
	result, err := c.SearchRows(context.Background(), SearchOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	checkSystemRow(t, result.Rows[0])
}

func TestSystemFieldsAcceptOnlyRealTypes(t *testing.T) {
	row, err := normalizeRow("t", map[string]json.RawMessage{"_id": raw(`"` + rowID + `"`),
		"_locked": raw(`null`), "_archived": raw(`"true"`), "_creator": raw(`5`),
		"_locked_by": raw(`"a\u0000b"`)})
	if err != nil {
		t.Fatal(err)
	}
	if row.Locked != nil || row.Archived != nil || row.Creator != "" || row.LockedBy != "a b" {
		t.Errorf("row = %+v", row)
	}
}

func TestRowActivitiesReportTheExactTotalCount(t *testing.T) {
	serveHistory(t, `{"activities":[],"total_count":9007199254740993}`)
	c, _ := client(t, "*")
	result, err := c.RowActivities(context.Background(), ActivitiesOptions{Table: "Kunden", RowID: rowID, Page: 1, PerPage: 10})
	if err != nil || result.TotalCount == nil || *result.TotalCount != 9007199254740993 {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if data, _ := json.Marshal(result); !strings.Contains(string(data), `"total_count":9007199254740993`) {
		t.Errorf("encoded = %s", data)
	}
	outputCoverage(t, descriptorOf(t, "seatable.base.rows.activities"), toMap(t, result))
	if strings.Contains(string(descriptorOf(t, "seatable.base.base.operations").OutputSchema), "total_count") {
		t.Error("base.operations schema changed")
	}
}

func TestBaseOperationsOutputHasNoTotalCount(t *testing.T) {
	serveHistory(t, `{"operations":[],"total_count":5}`)
	c, _ := client(t, "*")
	result, err := c.BaseOperations(context.Background(), OperationsOptions{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(result); strings.Contains(string(data), "total_count") {
		t.Errorf("encoded = %s", data)
	}
}

func TestAvatarAddressWithAccessPartsIsLeftOut(t *testing.T) {
	for in, want := range map[string]string{
		"https://x.invalid/a.png":                        "https://x.invalid/a.png",
		"https://x.invalid/a.png?sig=1":                  "",
		"https://x.invalid/a.png#f":                      "",
		"https://u:p" + "@" + "x.invalid/a.png":          "",
		"ftp://x.invalid/a.png":                          "",
		"/relative.png":                                  "",
		"https://x.invalid/a\x00.png":                    "",
		"https://x.invalid/" + strings.Repeat("a", 4100): "",
	} {
		if got := plainAddress(in); got != want {
			t.Errorf("plainAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFalseSystemBooleansStayVisible(t *testing.T) {
	row, err := normalizeRow("t", map[string]json.RawMessage{"_id": raw(`"` + rowID + `"`),
		"_locked": raw(`false`), "_archived": raw(`false`)})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(row)
	if string(data) != `{"id":"`+rowID+`","locked":false,"archived":false,"values":{}}` {
		t.Errorf("row = %s", data)
	}
}
