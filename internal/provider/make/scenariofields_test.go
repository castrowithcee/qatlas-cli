package makeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func scenarioWith(extra string) string {
	return fmt.Sprintf(`{"id":%d,"name":"Own","teamId":%d,"isActive":true,"islocked":false,"isPaused":false,%s}`,
		ownScenario, ownTeam, extra)
}

func decodeObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("result is not an object: %v: %s", err, raw)
	}
	return out
}

func firstScenario(t *testing.T, raw string) map[string]any {
	t.Helper()
	list, _ := decodeObject(t, raw)["scenarios"].([]any)
	if len(list) != 1 {
		t.Fatalf("scenarios = %s, want exactly one", raw)
	}
	entry, _ := list[0].(map[string]any)
	return entry
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Every new value arrives exactly, in the list and in the detail, and both ask for every documented column.
func TestScenariosListAndGetCarryEveryDocumentedField(t *testing.T) {
	scenario := scenarioWith(richScenarioDetails + `,"createdByUser":{"id":1,"name":"A","email":"a@example.test"},` +
		`"updatedByUser":{"id":2,"name":"B","email":"b@example.test"}`)
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == apiPath+"/scenarios" {
			return jsonResponse(200, `{"scenarios":[`+scenario+`]}`), nil
		}
		return jsonResponse(200, `{"scenario":`+scenario+`}`), nil
	})
	list, err := env.invoke(scenariosList.ID, "open", `{}`)
	if err != nil {
		t.Fatalf("list = %v", err)
	}
	get, err := env.invoke(scenariosGet.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("get = %v", err)
	}
	want := map[string]string{
		"hook_id": `77`, "devices": `[{"id":5,"scope":"team"}]`, "folder_path": `"CRM/cleanup"`, "concept": `false`,
		"used_packages": `["http"]`, "used_modules": `[{"module_name":"ActionSendData","package_name":"http"}]`,
		"is_waiting": `false`, "all_dlq_count": `3`, "scenario_version": `4`, "module_sequence_id": `9`,
		"type": `"scenario"`, "chaining_role": `"child"`, "is_shared": `true`, "executions": `8`, "errors": `1`,
		"custom_properties": `{"owner":"ops"}`,
		"template_metadata": `{"instantiable_id":3,"is_oem":true,"metadata_id":2,"version_id":1}`,
		"labels":            `[{"colour":"info","created":"2026-01-01T00:00:00Z","created_by":1,"description":"ld","id":6,"name":"L","scope":"team","team_id":1001,"updated":"2026-01-02T00:00:00Z"}]`,
		"deleted":           `true`, "deleted_at": `"2026-01-04T00:00:00Z"`,
		"created_by_user": `{"email":"a@example.test","id":1,"name":"A"}`,
		"updated_by_user": `{"email":"b@example.test","id":2,"name":"B"}`,
	}
	for name, tool := range map[string]map[string]any{"list": firstScenario(t, list), "get": decodeObject(t, get)} {
		for key, expected := range want {
			if got := jsonOf(t, tool[key]); got != expected {
				t.Errorf("%s %s = %s, want %s", name, key, got, expected)
			}
		}
	}
	for _, c := range calls {
		if got := c.query["cols[]"]; strings.Join(got, ",") != strings.Join(scenarioColumns, ",") {
			t.Errorf("%s cols[] = %v, want every documented column", c.path, got)
		}
	}
}

// A scenario without the new members is answered as before.
func TestScenarioWithoutNewFieldsIsUnchanged(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"scenarios":[`+scenarioWith(`"folderPath":null,"customProperties":null,"labels":[],`+
			`"templateMetadata":null,"chainingRole":null,"deletedAt":null`)+`]}`), nil
	})
	out, err := env.invoke(scenariosList.ID, "open", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	entry := jsonOf(t, firstScenario(t, out))
	want := fmt.Sprintf(`{"id":%d,"is_active":true,"is_locked":false,"is_paused":false,"name":"Own","team_id":%d}`, ownScenario, ownTeam)
	if entry != want {
		t.Fatalf("entry = %s, want %s", entry, want)
	}
}

func TestScenarioCustomPropertiesAreBoundedNeverCut(t *testing.T) {
	long := strings.Repeat("x", maxCustomString+1)
	many := make([]string, maxCustomEntries+1)
	for i := range many {
		many[i] = strconv.Itoa(i)
	}
	cases := []struct {
		name, raw string
		omitted   bool
	}{
		{"inside", `{"a":{"b":{"c":[1,2.50,"s"]}}}`, false},
		{"too deep", `{"a":{"b":{"c":{"d":{"e":1}}}}}`, true},
		{"too many entries", `{"a":[` + strings.Join(many, ",") + `]}`, true},
		{"long string", `{"a":"` + long + `"}`, true},
		{"not an object", `[1]`, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"scenarios":[`+scenarioWith(`"customProperties":`+tt.raw)+`]}`), nil
			})
			out, err := env.invoke(scenariosList.ID, "open", `{}`)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			entry := firstScenario(t, out)
			if tt.omitted {
				if _, present := entry["custom_properties"]; present || entry["custom_properties_omitted"] != true {
					t.Fatalf("entry = %v, want custom properties left out and marked", entry)
				}
				return
			}
			if !strings.Contains(out, `"custom_properties":{"a":{"b":{"c":[1,2.50,"s"]}}}`) {
				t.Fatalf("result = %s, want the custom properties as Make wrote them", out)
			}
			if _, marked := entry["custom_properties_omitted"]; marked {
				t.Fatalf("entry = %v, want no omission mark", entry)
			}
		})
	}
}

func TestScenarioTextIsCleanedAndListsAreBounded(t *testing.T) {
	var packages []string
	for i := 0; i <= maxScenarioPackages; i++ {
		packages = append(packages, `"p`+strconv.Itoa(i)+`"`)
	}
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"scenarios":[`+scenarioWith(`"folderPath":"a\u001b[31m/\nb","usedPackages":[`+
			strings.Join(packages, ",")+`],"createdByUser":{"id":1,"name":"A","email":"x\u0007@example.test"},`+
			`"labels":[{"id":1,"name":"bad\u0000name"}]`)+`]}`), nil
	})
	out, err := env.invoke(scenariosList.ID, "open", `{}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	entry := firstScenario(t, out)
	if entry["folder_path"] != "a [31m/ b" {
		t.Errorf("folder_path = %q, want control characters replaced", entry["folder_path"])
	}
	if got := jsonOf(t, entry["created_by_user"]); got != `{"email":"x @example.test","id":1,"name":"A"}` {
		t.Errorf("created_by_user = %s", got)
	}
	if got := jsonOf(t, entry["labels"]); got != `[{"id":1,"name":"bad name"}]` {
		t.Errorf("labels = %s", got)
	}
	if used, _ := entry["used_packages"].([]any); len(used) != maxScenarioPackages || entry["used_packages_truncated"] != true {
		t.Errorf("used_packages = %d, truncated = %v, want the cap and the mark", len(used), entry["used_packages_truncated"])
	}
}

func TestScenariosListFiltersReachTheRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"scenarios":[]}`), nil
	})
	_, err := env.invoke(scenariosList.ID, "open", `{"is_active":true,"name":"n","ids":[3,4],"folder_id":300,`+
		`"include_subfolders":false,"label_ids":[7,8],"concept":true,"type":"tool","deleted":false,"search":"s",`+
		`"is_invalid":false,"is_locked":true,"created_by_user_name":"c","updated_by_user_name":"u","dlq_count":0,`+
		`"sort_by":"lastEdit","sort_dir":"desc","offset":5,"limit":7}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	want := url.Values{"teamId": {itoa64(ownTeam)}, "pg[offset]": {"5"}, "pg[limit]": {"7"}, "cols[]": scenarioColumns,
		"isActive": {"true"}, "name": {"n"}, "id[]": {"3", "4"}, "folderId": {"300"}, "includeSubfolders": {"false"},
		"labelIds[]": {"7", "8"}, "concept": {"true"}, "type": {"tool"}, "deleted": {"false"}, "search": {"s"},
		"isinvalid": {"false"}, "islocked": {"true"}, "createdByUserName": {"c"}, "updatedByUserName": {"u"},
		"dlqCount": {"0"}, "pg[sortBy]": {"lastEdit"}, "pg[sortDir]": {"desc"}}
	if len(calls) != 1 || calls[0].query.Encode() != want.Encode() {
		t.Fatalf("calls = %+v, want one request with %s", calls, want.Encode())
	}
}

// An unusable filter is refused before any request and before any secret is read.
func TestScenariosListRejectsInvalidFiltersBeforeAnyRequest(t *testing.T) {
	over := make([]string, 101)
	for i := range over {
		over[i] = "1"
	}
	for name, arguments := range map[string]string{
		"include_subfolders alone": `{"include_subfolders":true}`,
		"unknown sort_by":          `{"sort_by":"proprietal"}`,
		"unknown sort_dir":         `{"sort_dir":"up"}`,
		"unknown type":             `{"type":"other"}`,
		"empty ids":                `{"ids":[]}`,
		"zero id":                  `{"ids":[0]}`,
		"too many ids":             `{"ids":[` + strings.Join(over, ",") + `]}`,
		"empty label_ids":          `{"label_ids":[]}`,
		"negative folder":          `{"folder_id":-1}`,
		"negative dlq_count":       `{"dlq_count":-1}`,
		"empty search":             `{"search":""}`,
		"blank search":             `{"search":"   "}`,
		"long user name":           `{"created_by_user_name":"` + strings.Repeat("x", 257) + `"}`,
		"unknown filter":           `{"organization_id":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Error("no request should have been sent")
				return jsonResponse(500, `{}`), nil
			})
			if _, err := env.invoke(scenariosList.ID, "open", arguments); !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

// The per-row scope check still applies to rows a filter asked for.
func TestScenariosListFilteredRowsStayScopeChecked(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, scenariosPageJSONOf(scenarioJSONOf(ownScenario, ownTeam, "Own"),
			scenarioJSONOf(foreignScenario, foreignTeam, "Foreign"), scenarioJSONOf(99, ownTeam, "Other"))), nil
	})
	out, err := env.invoke(scenariosList.ID, "scenario", fmt.Sprintf(`{"ids":[%d,%d,99],"deleted":true}`, ownScenario, foreignScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if strings.Contains(out, "Foreign") || strings.Contains(out, "Other") || !strings.Contains(out, "Own") {
		t.Fatalf("result = %s, want only the own, allow-listed scenario", out)
	}
}

func blueprintWithScheduling(t *testing.T, scheduling string) map[string]any {
	t.Helper()
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/blueprint") {
			return jsonResponse(200, `{"code":"OK","response":{"blueprint":{"name":"Own"},`+scheduling+`}}`), nil
		}
		return jsonResponse(200, `{"scenario":`+scenarioJSONOf(ownScenario, ownTeam, "Own")+`}`), nil
	})
	out, err := env.invoke(scenariosBlueprint.ID, "open", fmt.Sprintf(`{"scenario_id":%d}`, ownScenario))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	return decodeObject(t, out)
}

func TestScenariosBlueprintCarriesSchedulingAndMetadata(t *testing.T) {
	got := blueprintWithScheduling(t, `"scheduling":{"type":"daily","interval":0,"date":"2026-01-01T00:00:00Z",`+
		`"between":["a","b"],"time":"10:00","days":[1,31],"months":[2],"restrict":[{"time":["10:00",null],`+
		`"days":[3],"months":[4]}],"maximum_runs_per_minute":5,"extra":"ignored"},"idSequence":12,`+
		`"created":"2026-01-01T00:00:00Z","last_edit":"2026-01-02T00:00:00Z","version":0`)
	want := map[string]string{
		"scheduling": `{"between":["a","b"],"date":"2026-01-01T00:00:00Z","days":[1,31],"interval":0,` +
			`"maximum_runs_per_minute":5,"months":[2],"restrict":[{"days":[3],"months":[4],"time":["10:00",null]}],` +
			`"time":"10:00","type":"daily"}`,
		"code": `"OK"`, "id_sequence": `12`, "created": `"2026-01-01T00:00:00Z"`, "last_edit": `"2026-01-02T00:00:00Z"`,
		"version": `0`, "blueprint": `{"name":"Own"}`,
	}
	for key, expected := range want {
		if actual := jsonOf(t, got[key]); actual != expected {
			t.Errorf("%s = %s, want %s", key, actual, expected)
		}
	}
}

func TestScenariosBlueprintSchedulingOutsideLimitsIsLeftOutAndMarked(t *testing.T) {
	days := make([]string, maxSchedulingList+1)
	for i := range days {
		days[i] = "1"
	}
	for name, scheduling := range map[string]string{
		"too many days": `"scheduling":{"days":[` + strings.Join(days, ",") + `]}`,
		"long type":     `"scheduling":{"type":"` + strings.Repeat("x", maxSchedulingText+1) + `"}`,
		"wrong shape":   `"scheduling":{"interval":"soon"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := blueprintWithScheduling(t, scheduling)
			if _, present := got["scheduling"]; present || got["scheduling_omitted"] != true {
				t.Fatalf("result = %v, want scheduling left out and marked", got)
			}
		})
	}
	got := blueprintWithScheduling(t, `"scheduling":{}`)
	if _, present := got["scheduling"]; present || got["scheduling_omitted"] != nil {
		t.Fatalf("result = %v, want an empty scheduling to be plain absence", got)
	}
}
