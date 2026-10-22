package infomaniakdrive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// rewindDirectory is the folder a rewind targets, in the shape of a file resource.
const rewindDirectory = `{"id":6001,"name":"Archive","type":"dir","status":"ok","parent_id":1,"created_at":1735689600,"last_modified_at":1735776000}`

func withBody(extra string) string {
	return strings.TrimSuffix(strings.TrimSpace(fullDriveBody), "}") + "," + extra + "}"
}

const allWithMembers = `
 "account":{"id":1001,"beta":false,"name":"Secret Organization","legal_entity_type":"company","phone":"example-phone",
  "support_level":2,"has_2fa_required":true,"is_sso":false,"website":"https://www.infomaniak.com","created_at":1735689600},
 "capabilities":{"use_versioning":true,"use_upload_compression":false,"use_team_space":true,"can_add_user":true,
  "can_see_stats":true,"can_upgrade_to_ksuite":false,"can_rewind":true,"can_use_rag":false,"search":"content"},
 "pack":{"id":9,"name":"team","display_name":"Team","is_free":false,
  "capabilities":{"use_vault":true,"use_manage_right":true,"use_dropbox":true,"can_rewind":true,
   "use_folder_custom_color":true,"can_access_dashboard":true,"can_set_sharelink_password":true,"can_join_sharelink":true,
   "can_set_sharelink_expiration":true,"can_set_sharelink_custom_url":false,"can_use_content_search":true,
   "can_use_activity_reports":true,"can_use_signatures":false,"can_use_statistics":true,"can_edit_pdf":true,
   "can_use_office365":true,"can_set_office":true,"start_guide_pdf":false,"can_use_rag":false,"can_use_webdav":true,
   "can_use_storage_report":true,"can_use_photo":false,"can_set_trash_duration":true},
  "limits":{"users":{"base":3,"min":null,"max":100},"trash":{"retention_options":[30,60,90]},
   "storage":{"giga":3000,"min":0,"max":null}},
  "prices":[{"type":"base","unit":1,"period":12,"currency_id":1,"amount_excl_vat":12.30,"amount_incl_vat":13.2}]},
 "settings":{"ai_scan":{"is_enabled":true,"has_approved":null,"has_approved_ai_categories":false,
   "has_approved_content_search":true,"updated_at":1735862400},
  "versioning":{"max_numbers":20,"max_days":60},"office":{"presentation":"365","spreadsheet":"365","text":"365","default_mode":"desktop"},
  "max_keep_deleted_user":"one_month","files":{"private":{"size_threshold":1048576}},
  "report":{"storage_report":{"frequency":"monthly"}}},
 "preferences":{"color":"blue","hide":false,"default":true,"default_page":"files",
  "ui":{"storage_warning_alert":true,"storage_limit_alert":false,"rag_ocr_promotion":true}},
 "k_suite":{"id":4,"name":"Business","pack_id":12,"pack":"business"},
 "tags":[{"id":1234,"name":"My tag name","color":4,"product_count":2}],
 "rights":{"technical":true,"statistic":false,"check":true,"sale":false},
 "categories":[{"id":1,"name":"Invoices","color":"#FF1493","is_predefined":true,"created_by":77,"created_at":1735689600,"user_uses":5}],
 "categories_permissions":{"can_create":true,"can_edit":true,"can_delete":false,"can_read_on_file":true,"can_put_on_file":true},
 "users":{"account":[1,2],"drive":[3],"external":[4],"internal":[5,6]},
 "teams":{"account":[10],"drive":[11,12]},
 "invitations_count":0,
 "rewind":{"id":55,"status":"canceled","mode":"directory","archiving":"all","directory_id":6001,
  "directory":` + rewindDirectory + `,"directory_name":"Archive","user_id":null,"rewind_at":1735689600,
  "created_at":1735776000,"finished_at":1735862400,"expires_at":1767225600,"created_by":77,"is_approved":true,
  "approval_at":1735948800,"rewind_name":"Before the incident",
  "sanitizer":{"sanitized":3,"total":{"files_found":10,"to_move":1},"team":{"files_found":4},"me":{"files_found":6},"others":"none"},
  "summary":{"total":{"executed":1,"archived":2,"conflicted":0},"team":{"executed":1,"directory_id":null},
   "me":{"executed":0},"others":[{"user_id":9,"executed":2}]}},
 "bill_reference":"INV-1","description":"Main drive","quota":{"dropbox":"unlimited","shared_link":"limited"}`

var allWith = `["account","capabilities","pack","pack.capabilities","pack.limits","pack.prices","settings",` +
	`"preferences","k_suite","tags","tags.#products","rights","categories","categories.user_uses",` +
	`"categories_permissions","users","teams","invitations_count","rewind","rewind.directory",` +
	`"rewind.sanitizer","rewind.summary","bill_reference","description","quota"]`

const expectedWith = `{
 "account":{"id":1001,"beta":false,"name":"Secret Organization","legal_entity_type":"company","phone":"example-phone",
  "support_level":2,"has_2fa_required":true,"is_sso":false,"website":"https://www.infomaniak.com","created_at":"2025-01-01T00:00:00Z"},
 "capabilities":{"use_versioning":true,"use_upload_compression":false,"use_team_space":true,"can_add_user":true,
  "can_see_stats":true,"can_upgrade_to_ksuite":false,"can_rewind":true,"can_use_rag":false,"search":"content"},
 "pack":{"id":9,"name":"team","display_name":"Team","is_free":false,
  "capabilities":{"use_vault":true,"use_manage_right":true,"use_dropbox":true,"can_rewind":true,
   "use_folder_custom_color":true,"can_access_dashboard":true,"can_set_sharelink_password":true,"can_join_sharelink":true,
   "can_set_sharelink_expiration":true,"can_set_sharelink_custom_url":false,"can_use_content_search":true,
   "can_use_activity_reports":true,"can_use_signatures":false,"can_use_statistics":true,"can_edit_pdf":true,
   "can_use_office365":true,"can_set_office":true,"start_guide_pdf":false,"can_use_rag":false,"can_use_webdav":true,
   "can_use_storage_report":true,"can_use_photo":false,"can_set_trash_duration":true},
  "limits":{"users":{"base":3,"max":100},"trash":{"retention_options":[30,60,90]},"storage":{"giga":3000,"min":0}},
  "prices":[{"type":"base","unit":1,"period":12,"currency_id":1,"amount_excl_vat":12.30,"amount_incl_vat":13.2}]},
 "settings":{"ai_scan":{"is_enabled":true,"has_approved_ai_categories":false,
   "has_approved_content_search":true,"updated_at":"2025-01-03T00:00:00Z"},
  "versioning":{"max_numbers":20,"max_days":60},"office":{"presentation":"365","spreadsheet":"365","text":"365","default_mode":"desktop"},
  "max_keep_deleted_user":"one_month","files":{"private":{"size_threshold":1048576}},
  "report":{"storage_report":{"frequency":"monthly"}}},
 "preferences":{"color":"blue","hide":false,"default":true,"default_page":"files",
  "ui":{"storage_warning_alert":true,"storage_limit_alert":false,"rag_ocr_promotion":true}},
 "k_suite":{"id":4,"name":"Business","pack_id":12,"pack":"business"},
 "tags":[{"id":1234,"name":"My tag name","color":4,"product_count":2}],
 "rights":{"technical":true,"statistic":false,"check":true,"sale":false},
 "categories":[{"id":1,"name":"Invoices","color":"#FF1493","is_predefined":true,"created_by":77,"created_at":"2025-01-01T00:00:00Z","user_uses":5}],
 "categories_permissions":{"can_create":true,"can_edit":true,"can_delete":false,"can_read_on_file":true,"can_put_on_file":true},
 "users":{"account":[1,2],"drive":[3],"external":[4],"internal":[5,6]},
 "teams":{"account":[10],"drive":[11,12]},
 "invitations_count":0,
 "rewind":{"id":55,"status":"canceled","mode":"directory","archiving":"all","directory_id":6001,
  "directory":{"id":6001,"name":"Archive","type":"folder","status":"ok","parent_id":1,"created_at":"2025-01-01T00:00:00Z","modified_at":"2025-01-02T00:00:00Z"},
  "directory_name":"Archive","rewind_at":"2025-01-01T00:00:00Z",
  "created_at":"2025-01-02T00:00:00Z","finished_at":"2025-01-03T00:00:00Z","expires_at":"2026-01-01T00:00:00Z","created_by":77,"is_approved":true,
  "approval_at":"2025-01-04T00:00:00Z","rewind_name":"Before the incident",
  "sanitizer":{"sanitized":3,"total":{"files_found":10,"to_move":1},"team":{"files_found":4},"me":{"files_found":6},"others":"none"},
  "summary":{"total":{"executed":1,"archived":2,"conflicted":0},"team":{"executed":1},
   "me":{"executed":0},"others":[{"user_id":9,"executed":2}]}},
 "bill_reference":"INV-1","description":"Main drive","quota":{"dropbox":"unlimited","shared_link":"limited"}}`

// driveOf runs drives.list on connection with one answering drive and returns the single drive as a generic
// object, so a test sees exactly what a caller sees.
func driveOf(t *testing.T, connection, arguments, body string) (map[string]any, []call) {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+body+`],"page":1,"pages":1,"total":1}`), nil
	}, nil)
	result, err := env.invoke(drivesList.ID, connection, arguments)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page struct {
		Drives []map[string]any `json:"drives"`
	}
	if err := json.Unmarshal([]byte(result), &page); err != nil || len(page.Drives) != 1 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	return page.Drives[0], calls
}

func mustMap(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDrivesListReturnsEveryWithMemberExactly(t *testing.T) {
	drive, calls := driveOf(t, "drive", `{"with":`+allWith+`}`, withBody(allWithMembers))
	want := mustMap(t, expectedWith)
	for key, value := range want {
		if !reflect.DeepEqual(drive[key], value) {
			t.Errorf("%s = %v\nwant %v", key, drive[key], value)
		}
	}
	if len(calls) != 1 || !strings.Contains(calls[0].query.Get("with"), "rewind.summary") {
		t.Fatalf("calls = %+v", calls)
	}
}

// Members whose with value was not asked for never appear, even when Infomaniak reports them.
func TestDrivesListDropsMembersThatWereNotAskedFor(t *testing.T) {
	drive, _ := driveOf(t, "drive", `{"with":["tags","pack","categories","rewind"]}`, withBody(allWithMembers))
	for _, key := range []string{"account", "capabilities", "settings", "preferences", "k_suite", "rights",
		"categories_permissions", "users", "teams", "invitations_count", "bill_reference", "description", "quota"} {
		if _, ok := drive[key]; ok {
			t.Errorf("%s present without its with value", key)
		}
	}
	pack := drive["pack"].(map[string]any)
	for _, key := range []string{"capabilities", "limits", "prices"} {
		if _, ok := pack[key]; ok {
			t.Errorf("pack.%s present without its with value", key)
		}
	}
	if _, ok := drive["tags"].([]any)[0].(map[string]any)["product_count"]; ok {
		t.Error("tags.product_count present without tags.#products")
	}
	if _, ok := drive["categories"].([]any)[0].(map[string]any)["user_uses"]; ok {
		t.Error("categories.user_uses present without its with value")
	}
	rewind := drive["rewind"].(map[string]any)
	for _, key := range []string{"directory", "sanitizer", "summary"} {
		if _, ok := rewind[key]; ok {
			t.Errorf("rewind.%s present without its with value", key)
		}
	}
}

func repeat(item string, n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = item
	}
	return strings.Join(items, ",")
}

func numbers(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = strconv.Itoa(i + 1)
	}
	return strings.Join(items, ",")
}

func TestDrivesListCapsListsAndFlagsThem(t *testing.T) {
	body := withBody(`"tags":[` + repeat(`{"id":1,"name":"t"}`, 101) + `],` +
		`"categories":[` + repeat(`{"id":1,"name":"c"}`, 101) + `],` +
		`"pack":{"id":1,"prices":[` + repeat(`{"type":"base"}`, 101) + `],"limits":{"trash":{"retention_options":[` + numbers(101) + `]}}},` +
		`"users":{"account":[` + numbers(1001) + `],"drive":[` + numbers(1000) + `]},` +
		`"teams":{"account":[` + numbers(1001) + `]}`)
	drive, _ := driveOf(t, "drive", `{"with":["tags","categories","pack.prices","pack.limits","users","teams"]}`, body)
	length := func(v any) int { return len(v.([]any)) }
	pack := drive["pack"].(map[string]any)
	users := drive["users"].(map[string]any)
	teams := drive["teams"].(map[string]any)
	trash := pack["limits"].(map[string]any)["trash"].(map[string]any)
	checks := []struct {
		name      string
		got, want int
		flag      any
		flagged   bool
	}{
		{"tags", length(drive["tags"]), 100, drive["tags_truncated"], true},
		{"categories", length(drive["categories"]), 100, drive["categories_truncated"], true},
		{"prices", length(pack["prices"]), 100, pack["prices_truncated"], true},
		{"retention_options", length(trash["retention_options"]), 100, trash["retention_options_truncated"], true},
		{"users.account", length(users["account"]), 1000, users["account_truncated"], true},
		{"users.drive", length(users["drive"]), 1000, users["drive_truncated"], false},
		{"teams.account", length(teams["account"]), 1000, teams["account_truncated"], true},
	}
	for _, c := range checks {
		if c.got != c.want || (c.flag == true) != c.flagged {
			t.Errorf("%s: len = %d (want %d), flag = %v (want %v)", c.name, c.got, c.want, c.flag, c.flagged)
		}
	}
}

// Prices are never converted to floating point: the exact decimal text of Infomaniak arrives.
func TestDrivesListKeepsPriceAmountsExact(t *testing.T) {
	body := withBody(`"pack":{"prices":[{"type":"base","amount_excl_vat":12345678901234567.25,"amount_incl_vat":0.10}]}`)
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+body+`],"page":1,"pages":1,"total":1}`), nil
	}, nil)
	result, err := env.invoke(drivesList.ID, "drive", `{"with":["pack.prices"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"amount_excl_vat":12345678901234567.25`) || !strings.Contains(result, `"amount_incl_vat":0.10`) {
		t.Fatalf("result = %s, want the exact decimal amounts", result)
	}
}

func TestDrivesListBoundsFreeRewindObjects(t *testing.T) {
	deep := `{"a":{"b":{"c":{"d":1}}}}`
	wide := make([]string, 120)
	for i := range wide {
		wide[i] = fmt.Sprintf(`"k%d":1`, i)
	}
	long := `{"x":"` + strings.Repeat("y", 200) + `"}`
	body := withBody(`"rewind":{"id":1,"sanitizer":{"sanitized":1,"total":` + deep + `,"team":{` + strings.Join(wide, ",") +
		`},"me":` + long + `,"others":"ok"},"summary":{"total":{"executed":1},"team":[1],"me":{"k":"v\u0001w"}}}`)
	drive, _ := driveOf(t, "drive", `{"with":["rewind.sanitizer","rewind.summary"]}`, body)
	rewind := drive["rewind"].(map[string]any)
	sanitizer := rewind["sanitizer"].(map[string]any)
	for _, name := range []string{"total", "team", "me"} {
		if _, ok := sanitizer[name]; ok || sanitizer[name+"_truncated"] != true {
			t.Errorf("sanitizer.%s = %v, flag = %v, want it left out and flagged", name, sanitizer[name], sanitizer[name+"_truncated"])
		}
	}
	if sanitizer["others"] != "ok" || sanitizer["sanitized"] != float64(1) {
		t.Errorf("sanitizer = %v", sanitizer)
	}
	summary := rewind["summary"].(map[string]any)
	if !reflect.DeepEqual(summary["total"], map[string]any{"executed": float64(1)}) ||
		summary["team_truncated"] != true || summary["team"] != nil ||
		!reflect.DeepEqual(summary["me"], map[string]any{"k": "vw"}) {
		t.Errorf("summary = %v", summary)
	}
}

// A drive bound only through files targets never shows the rewind members that name a folder outside them.
func TestDrivesListHidesRewindFolderNamesForASubtreeBinding(t *testing.T) {
	arguments := `{"with":["rewind","rewind.directory"]}`
	body := withBody(allWithMembers)
	hidden := []string{"directory", "directory_name", "rewind_name"}

	var calls []call
	env := subtreeEnvironment(t, t.TempDir(), &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+body+`],"page":1,"pages":1,"total":1}`), nil
	}, subTargets(subTarget)...)
	result, err := env.invoke(drivesList.ID, "sub", arguments)
	if err != nil {
		t.Fatal(err)
	}
	rewind := mustMap(t, result)["drives"].([]any)[0].(map[string]any)["rewind"].(map[string]any)
	for _, key := range hidden {
		if _, ok := rewind[key]; ok {
			t.Errorf("rewind.%s present for a subtree binding", key)
		}
	}
	if rewind["directory_id"] != float64(6001) || rewind["id"] != float64(55) {
		t.Errorf("rewind = %v, want the identifiers kept", rewind)
	}

	whole, _ := driveOf(t, "drive", arguments, body)
	wholeRewind := whole["rewind"].(map[string]any)
	for _, key := range hidden {
		if _, ok := wholeRewind[key]; !ok {
			t.Errorf("rewind.%s missing for a whole-drive binding", key)
		}
	}
}

func TestDrivesListCleansForeignTextsOfWithMembers(t *testing.T) {
	body := withBody(`"description":"a\u0001b","account":{"name":"n\u001bm"},"tags":[{"id":1,"name":"t\u0007"}]`)
	drive, _ := driveOf(t, "drive", `{"with":["description","account","tags"]}`, body)
	if drive["description"] != "ab" || drive["account"].(map[string]any)["name"] != "nm" ||
		drive["tags"].([]any)[0].(map[string]any)["name"] != "t" {
		t.Fatalf("drive = %v", drive)
	}
}
