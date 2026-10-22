package infomaniakdrive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// ListDrives reports the account's own page and total transparently, while filtering the entries themselves
// to the connection's drive allow-list and to drives that actually belong to the bound account.
func TestDrivesListFiltersToTheConnectionAndReportsPagingTransparently(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/2/drive" {
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
		return jsonResponse(200, `{"result":"success","data":[`+
			driveJSONOf(ownDrive, ownAccount, "Own")+`,`+
			driveJSONOf(otherOwnDrive, ownAccount, "Other")+`,`+
			driveJSONOf(foreignDrive, otherAccount, foreignCanary)+
			`],"page":2,"pages":3,"total":21}`), nil
	}, nil)

	// The "drive" connection allows only ownDrive, so the same account's other drive and the foreign
	// account's drive are both left out, even though the fake server (like a broader token) returned them.
	result, err := env.invoke(drivesList.ID, "drive", `{"page":2}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page DrivesPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if page.Page != 2 || page.Pages != 3 || page.Total != 21 {
		t.Fatalf("page = %+v, want the account's own pagination reported transparently", page)
	}
	if page.Count != 1 || len(page.Drives) != 1 || page.Drives[0].ID != ownDrive {
		t.Fatalf("drives = %+v, want only the allow-listed drive of the bound account", page.Drives)
	}
	for _, drive := range page.Drives {
		if drive.Name == foreignCanary {
			t.Fatalf("a foreign drive's name leaked into the result: %+v", page.Drives)
		}
	}
	if len(calls) != 1 || calls[0].query.Get("account_id") != strconv.FormatInt(ownAccount, 10) ||
		calls[0].query.Get("page") != "2" {
		t.Fatalf("calls = %+v", calls)
	}

	// An account-only connection (no drive allow-list) keeps every drive of the bound account, but never one
	// of a foreign account, even if the fake server answered with one.
	result, err = env.invoke(drivesList.ID, "account", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if page.Count != 2 {
		t.Fatalf("drives = %+v, want both of the bound account and none of the foreign one", page.Drives)
	}
	for _, drive := range page.Drives {
		if drive.Name == foreignCanary {
			t.Fatalf("a foreign account's drive leaked into the result: %+v", page.Drives)
		}
	}
}

const legacyDriveBody = `{"id":5001,"name":"Own","size":1000000,"used_size":500000,"in_maintenance":false,"account_id":1001}`

// A response with only the former members keeps its former output; account_id is the one member that is
// added, because the drive filter already depends on it.
func TestDrivesListKeepsTheFormerOutputForAFormerResponse(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+legacyDriveBody+`],"page":1,"pages":1,"total":1}`), nil
	}, nil)
	result, err := env.invoke(drivesList.ID, "drive", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	// The core renders results with sorted keys.
	want := `{"count":1,"drives":[{"account_id":1001,"id":5001,"in_maintenance":false,"name":"Own",` +
		`"size":1000000,"used_size":500000}],"page":1,"pages":1,"total":1}`
	if result != want {
		t.Fatalf("result = %s\nwant     %s", result, want)
	}
	query := calls[0].query
	for _, name := range []string{"with", "in_maintenance", "maintenance_reasons", "tags"} {
		if _, ok := query[name]; ok {
			t.Fatalf("query = %v, want no %s without the argument", query, name)
		}
	}
}

const fullDriveBody = `{"id":5001,"name":"Own","size":1000000,"used_size":500000,"in_maintenance":true,"account_id":1001,
 "created_at":1735689600,"updated_at":1735776000,"maintenance_at":1735862400,"expired_at":1767225600,
 "maintenance_reason":"demo_end",
 "maintenance_types":[{"name":"Asleep","code":"asleep","description":"The Drive is inactive"}],
 "version":"v4.2","users_count":0,"users_quota":10,"product_id":77,
 "is_locked":false,"is_demo":true,"is_in_app_subscription":false,"role":"admin","account_admin":true,
 "bdd_storage_host":"db1.example.test","status":"ok"}`

func TestDrivesListReturnsEveryDriveFieldWithoutWith(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+fullDriveBody+`],"page":1,"pages":1,"total":1,"items_per_page":100}`), nil
	}, nil)
	result, err := env.invoke(drivesList.ID, "drive", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":1,"drives":[{"account_admin":true,"account_id":1001,"bdd_storage_host":"db1.example.test",` +
		`"created_at":"2025-01-01T00:00:00Z","expired_at":"2026-01-01T00:00:00Z","id":5001,"in_maintenance":true,` +
		`"is_demo":true,"is_in_app_subscription":false,"is_locked":false,"maintenance_at":"2025-01-03T00:00:00Z",` +
		`"maintenance_reason":"demo_end","maintenance_types":[{"code":"asleep","description":"The Drive is inactive",` +
		`"name":"Asleep"}],"name":"Own","product_id":77,"role":"admin","size":1000000,"status":"ok",` +
		`"updated_at":"2025-01-02T00:00:00Z","used_size":500000,"users_count":0,"users_quota":10,"version":"v4.2"}],` +
		`"items_per_page":100,"page":1,"pages":1,"total":1}`
	if result != want {
		t.Fatalf("result = %s\nwant     %s", result, want)
	}
}

func TestDrivesListCapsMaintenanceTypesAndCleansTexts(t *testing.T) {
	types := strings.Repeat(`{"name":"n","code":"c","description":"d"},`, 100) + `{"name":"x","code":"x","description":"x"}`
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[{"id":5001,"name":"Own","size":1,"used_size":1,`+
			`"in_maintenance":true,"account_id":1001,"version":"v\u0001\u001b4","maintenance_types":[`+types+`]}],`+
			`"page":1,"pages":1,"total":1}`), nil
	}, nil)
	result, err := env.invoke(drivesList.ID, "drive", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var page DrivesPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatal(err)
	}
	drive := page.Drives[0]
	if len(drive.MaintenanceTypes) != 100 || !drive.MaintenanceTypesTruncated || drive.Version != "v4" {
		t.Fatalf("drive = %+v, want 100 types, flagged truncated, and a cleaned version", drive)
	}
}

func TestDrivesListSendsFiltersAndWithInTheRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+legacyDriveBody+`],"page":1,"pages":1,"total":1}`), nil
	}, nil)
	if _, err := env.invoke(drivesList.ID, "drive",
		`{"in_maintenance":false,"maintenance_reasons":["technical","demo_end"],"tags":[3,1],`+
			`"with":["account","tags.#products","pack.prices"]}`); err != nil {
		t.Fatal(err)
	}
	query := calls[0].query
	if query.Get("in_maintenance") != "false" || !reflect.DeepEqual(query["maintenance_reasons"], []string{"technical", "demo_end"}) ||
		!reflect.DeepEqual(query["tags"], []string{"3", "1"}) || query.Get("with") != "account,tags.#products,pack.prices" ||
		query.Get("per_page") != "100" || query.Get("account_id") != strconv.FormatInt(ownAccount, 10) {
		t.Fatalf("query = %v", query)
	}
}

func TestDrivesListRefusesInvalidArgumentsBeforeAnySecretRead(t *testing.T) {
	tags := make([]string, 51)
	for i := range tags {
		tags[i] = strconv.Itoa(i + 1)
	}
	cases := map[string]string{
		"unknown reason":   `{"maintenance_reasons":["gone"]}`,
		"repeated reason":  `{"maintenance_reasons":["technical","technical"]}`,
		"empty reasons":    `{"maintenance_reasons":[]}`,
		"unknown with":     `{"with":["limits"]}`,
		"unoffered total":  `{"with":["total"]}`,
		"repeated with":    `{"with":["account","account"]}`,
		"too many tags":    `{"tags":[` + strings.Join(tags, ",") + `]}`,
		"repeated tag":     `{"tags":[2,2]}`,
		"non-positive tag": `{"tags":[0]}`,
		"fractional tag":   `{"tags":[1.5]}`,
		"string flag":      `{"in_maintenance":"yes"}`,
		"extra argument":   `{"account_id":1}`,
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"result":"success","data":[],"page":1,"pages":1,"total":0}`), nil
			}, nil)
			if _, err := env.invoke(drivesList.ID, "drive", arguments); err == nil {
				t.Fatal("invoke() = nil, want a refusal")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestDriveFilterValidateRefusesWhatTheSchemaWould(t *testing.T) {
	for _, filter := range []DriveFilter{
		{With: []string{"limits"}}, {With: []string{"account", "account"}},
		{MaintenanceReasons: []string{"x"}}, {Tags: []int64{1, 1}}, {Tags: []int64{0}},
	} {
		if err := filter.validate(); !isInvalidRequest(err) {
			t.Fatalf("validate(%+v) = %v, want an invalid request", filter, err)
		}
	}
	if err := (DriveFilter{}).validate(); err != nil {
		t.Fatal(err)
	}
}

// The ownership check and the target picker read drives without any with value or filter.
func TestOwnershipCheckAndTargetPickerSendNoWithOrFilter(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/2/drive" {
			return jsonResponse(200, `{"result":"success","data":[`+legacyDriveBody+`],"page":1,"pages":1,"total":1}`), nil
		}
		return jsonResponse(200, `{"result":"success","data":{"id":1,"parent_id":0,"name":"r","type":"dir"}}`), nil
	}), nil)
	_, _ = env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
	if _, err := browseList(t, config.TargetListRequest{Kind: "drive", Parent: "account/" + strconv.FormatInt(ownAccount, 10)}); err != nil {
		t.Fatal(err)
	}
	seenOwnership, seenPicker := false, false
	for _, c := range calls {
		if c.path == ownershipPath(ownDrive) {
			seenOwnership = true
		}
		if c.path == "/2/drive" {
			seenPicker = true
		}
		for _, name := range []string{"with", "in_maintenance", "maintenance_reasons", "tags"} {
			if _, ok := c.query[name]; ok {
				t.Fatalf("%s sent %s: %v", c.path, name, c.query)
			}
		}
	}
	if !seenOwnership || !seenPicker {
		t.Fatalf("calls = %+v, want the ownership check and the picker listing", calls)
	}
}

func TestEveryDriveOutputMemberIsInSchemaAndFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+withBody(allWithMembers)+`],"page":1,"pages":1,"total":1,"items_per_page":100}`), nil
	}, nil)
	result, err := env.invoke(drivesList.ID, "drive", `{"with":`+allWith+`}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Drives []map[string]json.RawMessage `json:"drives"`
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result), &top); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]any `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(drivesList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, f := range drivesList.Fields {
		fields[f.Name] = true
	}
	check := func(where string, members map[string]json.RawMessage, properties map[string]any) {
		for key := range members {
			if _, ok := properties[key]; !ok {
				t.Errorf("%s member %s is not in the output schema", where, key)
			}
			if !fields[key] {
				t.Errorf("%s member %s is not in Fields", where, key)
			}
		}
	}
	topProperties := map[string]any{}
	for key := range schema.Properties {
		topProperties[key] = true
	}
	delete(top, "drives") // the list itself is described by its items
	check("page", top, topProperties)
	check("drive", out.Drives[0], schema.Properties["drives"].Items.Properties)
	for key := range schema.Properties["drives"].Items.Properties {
		if !fields[key] {
			t.Errorf("schema member %s is not in Fields", key)
		}
	}
}
