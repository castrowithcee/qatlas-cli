package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The fixtures follow the return shapes of TagManager.getContainerVersions, getContainerVersion, and
// exportContainerVersion of Matomo 5.14: version rows with their releases, and the export of the container with
// the tags, triggers, and variables of one version (list members as arrays or as keyed objects).

const versionRow = `{"idcontainerversion":7,"idcontainer":"6OMh6taM","idsite":1,"status":"active","revision":1,
"name":"v1","description":"first","created_date":"2026-01-02 03:04:05","updated_date":"2026-01-03 03:04:05",
"created_date_pretty":"Jan 2, 2026","updated_date_pretty":"Jan 3, 2026",
"releases":[{"idcontainerrelease":3,"idcontainer":"6OMh6taM","idcontainerversion":7,"idsite":1,"status":"active",
"environment":"live","release_login":"admin","release_date":"2026-01-05 03:04:05","release_date_pretty":"Jan 5, 2026"}]}`

func versionRowOf(id, revision int, container string, site int) string {
	return `{"idcontainerversion":` + itoa(id) + `,"idcontainer":"` + container + `","idsite":` + itoa(site) +
		`,"revision":` + itoa(revision) + `,"name":"v` + itoa(revision) + `","releases":[]}`
}

const versionExportRow = `{"idcontainer":"6OMh6taM","idsite":1,"context":"web","name":"Main","description":"Shop",
"ignoreGtmDataLayer":"0","activelySyncGtmDataLayer":"1","isTagFireLimitAllowedInPreviewMode":"0",
"created_date":"2026-01-02 03:04:05","updated_date":"2026-02-03 04:05:06","created_date_pretty":"Jan 2, 2026",
"updated_date_pretty":"Feb 3, 2026","revision":1,
"version":{"name":"v1","description":"first","revision":1,"created_date":"2026-01-02 03:04:05",
"created_date_pretty":"Jan 2, 2026","updated_date":"2026-01-03 03:04:05","updated_date_pretty":"Jan 3, 2026"},
"tags":[{"idtag":4,"type":"CustomHtml","name":"Pixel","description":"d","parameters":{"customHtml":"<script>alert(1)</script>","htmlPosition":"bodyEnd","nested":{"list":["a",2]}},
"fire_trigger_ids":[7],"block_trigger_ids":[],"fire_limit":"once_lifetime","priority":343,"fire_delay":1350,
"start_date":"2017-01-02 03:04:05","end_date":null,"status":"active","created_date":"2026-01-02 03:04:05",
"updated_date":"2026-01-02 03:04:05","created_date_pretty":"Jan 2, 2026","updated_date_pretty":"Jan 2, 2026"}],
"triggers":{"1":{"idtrigger":7,"type":"CustomEvent","name":"Event","parameters":{"eventName":"foo"},
"conditions":[{"actual":"ErrorUrl","comparison":"contains","expected":"foo"}]}},
"variables":[{"idvariable":6,"type":"DataLayer","name":"Var","parameters":[],"default_value":"10",
"lookup_table":[{"match_value":"foo","comparison":"equals","out_value":"bar"}]}]}`

func TestTagManagerVersionsListReadsEveryFieldAndPages(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerVersions": func(call) *http.Response {
			return ok(`[` + versionRow + `,` + versionRowOf(9, 3, "6OMh6taM", 9) + `,` + versionRowOf(8, 2, "other123", 1) + `,` +
				versionRowOf(6, 0, "6OMh6taM", 1) + `]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-versions.list", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out ContainerVersionsResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || out.IDContainer != "6OMh6taM" || len(out.Versions) != 1 || !out.HasMore || out.NextCursor == "" {
		t.Fatalf("out = %s", result)
	}
	v := out.Versions[0]
	if v.ID != 7 || v.Revision == nil || *v.Revision != 1 || v.Name != "v1" || v.Description != "first" || v.Status != "active" ||
		v.Created != "2026-01-02 03:04:05" || v.Updated != "2026-01-03 03:04:05" || v.CreatedShown != "Jan 2, 2026" ||
		v.UpdatedShown != "Jan 3, 2026" || len(v.Releases) != 1 || v.Releases[0].ID != 3 || v.Releases[0].VersionID != 7 ||
		v.Releases[0].Environment != "live" || v.Releases[0].Login != "admin" || v.Releases[0].Released != "2026-01-05 03:04:05" ||
		v.Releases[0].ReleasedShow != "Jan 5, 2026" || v.Releases[0].Status != "active" {
		t.Fatalf("version = %+v", v)
	}
	last := calls[len(calls)-1]
	if last.api() != "TagManager.getContainerVersions" || last.form.Get("idSite") != "1" || last.form.Get("idContainer") != "6OMh6taM" {
		t.Fatalf("call = %+v", last)
	}
	next, err := env.invoke("matomo.tag-manager-versions.list", "tm-all",
		`{"idSite":1,"idContainer":"6OMh6taM","cursor":"`+out.NextCursor+`"}`)
	if err != nil || !strings.Contains(next, `"id":6`) || strings.Contains(next, `"id":9`) || strings.Contains(next, `"id":8`) ||
		strings.Contains(next, `"has_more":true`) {
		t.Fatalf("next = %s, err = %v", next, err)
	}
	for name, args := range map[string]string{"other website": `{"idSite":2,"idContainer":"6OMh6taM","cursor":"`,
		"other container": `{"idSite":1,"idContainer":"other123","cursor":"`} {
		if _, err := env.invoke("matomo.tag-manager-versions.list", "tm-all", args+out.NextCursor+`"}`); !isInvalidRequest(err) {
			t.Errorf("cursor of %s: %v", name, err)
		}
	}
}

func TestTagManagerVersionsListEmptyAndCapsReleases(t *testing.T) {
	releases := make([]string, 60)
	for i := range releases {
		releases[i] = `{"idcontainerrelease":` + itoa(i+1) + `,"idcontainerversion":7,"environment":"live"}`
	}
	row := `{"idcontainerversion":7,"revision":1,"releases":[` + strings.Join(releases, ",") + `]}`
	for name, body := range map[string]string{"empty": `[]`, "keyed": `{"2":` + row + `}`} {
		var calls []call
		env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
			"TagManager.getContainerVersions": func(call) *http.Response { return ok(body) },
		}).handler(t))
		result, err := env.invoke("matomo.tag-manager-versions.list", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM"}`)
		if err != nil || !strings.Contains(result, `"has_more":false`) || strings.Contains(result, "null") {
			t.Fatalf("%s: %s, %v", name, result, err)
		}
		if name == "keyed" {
			var out ContainerVersionsResult
			_ = json.Unmarshal([]byte(result), &out)
			if len(out.Versions) != 1 || len(out.Versions[0].Releases) != 50 || !out.Versions[0].ReleasesTruncated {
				t.Fatalf("out = %s", result)
			}
		}
	}
}

func TestTagManagerVersionsGetBindsTheVersionToContainerAndWebsite(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerVersion": func(c call) *http.Response {
			switch c.form.Get("idContainerVersion") {
			case "8":
				return ok(versionRowOf(7, 1, "6OMh6taM", 1)) // another version than asked for
			case "9":
				return ok(versionRowOf(9, 1, "6OMh6taM", 3)) // another website
			case "10":
				return ok(versionRowOf(10, 1, "other123", 1)) // another container
			case "11":
				return ok(`{"idcontainerversion":11,"revision":0,"name":"draft"}`) // bound by the request alone
			}
			return ok(versionRow)
		},
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-versions.get", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)
	if err != nil {
		t.Fatal(err)
	}
	var out ContainerVersionResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || out.IDContainer != "6OMh6taM" || out.ID != 7 || out.Name != "v1" || len(out.Releases) != 1 ||
		out.Releases[0].Environment != "live" {
		t.Fatalf("out = %s", result)
	}
	last := calls[len(calls)-1]
	if last.api() != "TagManager.getContainerVersion" || last.form.Get("idSite") != "1" ||
		last.form.Get("idContainer") != "6OMh6taM" || last.form.Get("idContainerVersion") != "7" {
		t.Fatalf("call = %+v", last)
	}
	for _, id := range []string{"8", "9", "10"} {
		if _, err := env.invoke("matomo.tag-manager-versions.get", "tm-all",
			`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":`+id+`}`); classOf(err) != "invalid-provider-response" {
			t.Errorf("version %s: %v", id, err)
		}
	}
	draft, err := env.invoke("matomo.tag-manager-versions.get", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":11}`)
	if err != nil || !strings.Contains(draft, `"revision":0`) {
		t.Fatalf("draft = %s, %v", draft, err)
	}
}

func TestTagManagerVersionsExportReadsEveryField(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.exportContainerVersion": func(call) *http.Response { return ok(versionExportRow) },
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)
	if err != nil {
		t.Fatal(err)
	}
	var out VersionExportResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || out.IDContainer != "6OMh6taM" || out.IDContainerVersion == nil || *out.IDContainerVersion != 7 ||
		out.Context != "web" || out.Name != "Main" || out.Description != "Shop" || out.IgnoreGTM == nil || *out.IgnoreGTM ||
		out.SyncGTM == nil || !*out.SyncGTM || out.FireLimit == nil || *out.FireLimit || out.Created != "2026-01-02 03:04:05" ||
		out.UpdatedShown != "Feb 3, 2026" || out.Revision == nil || *out.Revision != 1 || out.Version == nil ||
		out.Version.Name != "v1" || out.Version.Description != "first" || out.Version.UpdatedShown != "Jan 3, 2026" ||
		len(out.Tags) != 1 || len(out.Triggers) != 1 || len(out.Variables) != 1 || out.Truncated {
		t.Fatalf("out = %s", result)
	}
	tag := out.Tags[0]
	params, _ := tag.Parameters.(map[string]any)
	if tag.ID != 4 || tag.Type != "CustomHtml" || tag.Name != "Pixel" || tag.Description != "d" ||
		params["customHtml"] != "<script>alert(1)</script>" || params["htmlPosition"] != "bodyEnd" ||
		len(tag.FireTriggerIDs) != 1 || tag.FireTriggerIDs[0] != 7 || len(tag.BlockTriggerIDs) != 0 ||
		tag.FireLimit != "once_lifetime" || tag.FireDelay == nil || *tag.FireDelay != 1350 || tag.Priority == nil ||
		*tag.Priority != 343 || tag.StartDate != "2017-01-02 03:04:05" || tag.EndDate != "" || tag.Status != "active" ||
		tag.Created != "2026-01-02 03:04:05" || tag.UpdatedShown != "Jan 2, 2026" {
		t.Fatalf("tag = %+v", tag)
	}
	if trigger := out.Triggers[0]; trigger.ID != 7 || trigger.Type != "CustomEvent" || trigger.Conditions == nil {
		t.Fatalf("trigger = %+v", trigger)
	}
	if variable := out.Variables[0]; variable.ID != 6 || variable.DefaultValue != "10" || variable.LookupTable == nil ||
		variable.Parameters != nil {
		t.Fatalf("variable = %+v", variable)
	}
	if strings.Contains(result, "typeMetadata") || strings.Contains(result, "idContainerVersion\":null") {
		t.Fatalf("result = %s", result)
	}
	last := calls[len(calls)-1]
	if last.api() != "TagManager.exportContainerVersion" || last.form.Get("idSite") != "1" ||
		last.form.Get("idContainer") != "6OMh6taM" || last.form.Get("idContainerVersion") != "7" {
		t.Fatalf("call = %+v", last)
	}
	draft, err := env.invoke("matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM"}`)
	if err != nil || strings.Contains(draft, `"idContainerVersion"`) {
		t.Fatalf("draft = %s, %v", draft, err)
	}
	if last := calls[len(calls)-1]; last.form.Has("idContainerVersion") {
		t.Errorf("draft export sent a version: %+v", last)
	}
}

func TestTagManagerVersionsExportBoundsUntrustedContent(t *testing.T) {
	big := strings.Repeat("A", 20000)
	deep := `"x"`
	for i := 0; i < 10; i++ {
		deep = `{"k":` + deep + `}`
	}
	many := make([]string, 150)
	for i := range many {
		many[i] = `"v"`
	}
	tags := make([]string, 210)
	for i := range tags {
		tags[i] = `{"idtag":` + itoa(i+1) + `,"type":"CustomHtml","name":"t","parameters":{"customHtml":"x"}}`
	}
	row := `{"idcontainer":"6OMh6taM","idsite":1,"tags":[{"idtag":1,"type":"CustomHtml","parameters":{"customHtml":"` + big +
		`","deep":` + deep + `,"many":[` + strings.Join(many, ",") + `]}}],"triggers":[],"variables":[]}`
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.exportContainerVersion": func(c call) *http.Response {
			if c.form.Get("idContainerVersion") == "2" {
				return ok(`{"idcontainer":"6OMh6taM","tags":[` + strings.Join(tags, ",") + `]}`)
			}
			if c.form.Get("idContainerVersion") == "3" {
				return ok(`{"idcontainer":"6OMh6taM","tags":[` + strings.Repeat(`{"idtag":1,"parameters":{"a":"`+strings.Repeat("B", 16000)+`"}},`, 20) +
					`{"idtag":2}]}`)
			}
			return ok(row)
		},
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM"}`)
	var out VersionExportResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Truncated || len(out.Tags) != 1 {
		t.Fatalf("out = %s, err = %v", result, err)
	}
	params := out.Tags[0].Parameters.(map[string]any)
	if got := params["customHtml"].(string); len(got) != maxCodeLength {
		t.Errorf("customHtml = %d bytes", len(got))
	}
	if list := params["many"].([]any); len(list) != maxExportMembers {
		t.Errorf("many = %d", len(list))
	}
	if strings.Contains(result, `"x"`) {
		t.Errorf("nesting was not cut: %s", result)
	}
	result, err = env.invoke("matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":2}`)
	out = VersionExportResult{}
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Tags) != 200 || !out.Truncated {
		t.Fatalf("entry cap: %d tags, truncated %t, err %v", len(out.Tags), out.Truncated, err)
	}
	result, err = env.invoke("matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":3}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > maxExportText+16384 {
		t.Fatalf("text budget: %d bytes, err %v", len(result), err)
	}
}

func TestTagManagerVersionsExportRefusesAnotherContainerOrWebsite(t *testing.T) {
	for name, body := range map[string]string{
		"container":     `{"idcontainer":"other123","idsite":1,"tags":[],"triggers":[],"variables":[]}`,
		"website":       `{"idcontainer":"6OMh6taM","idsite":3,"tags":[],"triggers":[],"variables":[]}`,
		"entry no id":   `{"idcontainer":"6OMh6taM","tags":[{"name":"x"}]}`,
		"bad parameter": `{"idcontainer":"6OMh6taM","tags":[{"idtag":1,"parameters":"{"}]}`,
	} {
		var calls []call
		env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
			"TagManager.exportContainerVersion": func(call) *http.Response { return ok(body) },
		}).handler(t))
		result, err := env.invoke("matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM"}`)
		if name == "bad parameter" {
			// A text where a map is expected is data, not a refusal.
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if classOf(err) != "invalid-provider-response" || strings.Contains(err.Error(), "other123") {
			t.Errorf("%s: %s, %v", name, result, err)
		}
	}
}
