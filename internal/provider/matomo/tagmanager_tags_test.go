package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The fixtures follow the return shapes of TagManager.getContainerTags, getContainerTag, getContainerTriggers, and
// getContainerTrigger: database rows with decoded parameters, trigger IDs, and conditions, the dates formatted for
// the user, and the metadata of the template (which the tools leave out).

const tagRow = `{"idtag":4,"idcontainerversion":7,"idsite":1,"type":"CustomHtml","name":"Pixel","description":"d",
"status":"active","parameters":{"customHtml":"<script>alert(1)</script>","htmlPosition":"bodyEnd","nested":{"list":["a",2]}},
"fire_trigger_ids":[7,"8"],"block_trigger_ids":[],"fire_limit":"once_lifetime","priority":343,"fire_delay":1350,
"start_date":"2017-01-02 03:04:05","end_date":null,"created_date":"2026-01-02 03:04:05","updated_date":"2026-01-03 03:04:05",
"created_date_pretty":"Jan 2, 2026","updated_date_pretty":"Jan 3, 2026","typeMetadata":{"id":"CustomHtml","parameters":[{"name":"x","value":"SECRETMETA"}]}}`

const triggerRow = `{"idtrigger":7,"idcontainerversion":7,"idsite":1,"type":"CustomEvent","name":"Event","description":"e",
"status":"active","parameters":{"eventName":"foo"},"conditions":[{"actual":"ErrorUrl","comparison":"contains","expected":"<b>foo</b>"}],
"created_date":"2026-01-02 03:04:05","updated_date":"2026-01-03 03:04:05","created_date_pretty":"Jan 2, 2026",
"updated_date_pretty":"Jan 3, 2026","typeMetadata":{"id":"CustomEvent"}}`

func entryRowOf(key string, id, version, site int) string {
	return `{"` + key + `":` + itoa(id) + `,"idcontainerversion":` + itoa(version) + `,"idsite":` + itoa(site) +
		`,"type":"X","name":"n` + itoa(id) + `","parameters":[]}`
}

func TestTagManagerTagsListReadsEveryField(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerTags": func(call) *http.Response {
			return ok(`{"0":` + tagRow + `,"1":` + entryRowOf("idtag", 5, 7, 9) + `,"2":` + entryRowOf("idtag", 6, 8, 1) + `,"3":{"idtag":9}}`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-tags.list", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)
	if err != nil {
		t.Fatal(err)
	}
	var out TagsResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || out.IDContainer != "6OMh6taM" || out.IDContainerVersion != 7 || out.Truncated || len(out.Tags) != 2 {
		t.Fatalf("out = %s", result)
	}
	tag := out.Tags[0]
	params, _ := tag.Parameters.(map[string]any)
	if tag.ID != 4 || tag.Type != "CustomHtml" || tag.Name != "Pixel" || tag.Description != "d" || tag.Status != "active" ||
		params["customHtml"] != "<script>alert(1)</script>" || len(tag.FireTriggerIDs) != 2 || tag.FireTriggerIDs[1] != 8 ||
		tag.FireLimit != "once_lifetime" || tag.Priority == nil || *tag.Priority != 343 || tag.FireDelay == nil || *tag.FireDelay != 1350 ||
		tag.StartDate != "2017-01-02 03:04:05" || tag.EndDate != "" || tag.Created != "2026-01-02 03:04:05" ||
		tag.Updated != "2026-01-03 03:04:05" || tag.CreatedShown != "Jan 2, 2026" || tag.UpdatedShown != "Jan 3, 2026" ||
		out.Tags[1].ID != 9 {
		t.Fatalf("tag = %+v", tag)
	}
	if strings.Contains(result, "SECRETMETA") || strings.Contains(result, "typeMetadata") {
		t.Errorf("template metadata leaked: %s", result)
	}
	last := calls[len(calls)-1]
	if last.api() != "TagManager.getContainerTags" || last.form.Get("idSite") != "1" || last.form.Get("idContainer") != "6OMh6taM" ||
		last.form.Get("idContainerVersion") != "7" {
		t.Fatalf("call = %+v", last)
	}
}

func TestTagManagerTriggersListReadsEveryField(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerTriggers": func(call) *http.Response {
			return ok(`[` + triggerRow + `,` + entryRowOf("idtrigger", 8, 7, 4) + `]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-triggers.list", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)
	if err != nil {
		t.Fatal(err)
	}
	var out TriggersResult
	_ = json.Unmarshal([]byte(result), &out)
	if len(out.Triggers) != 1 || out.IDContainerVersion != 7 {
		t.Fatalf("out = %s", result)
	}
	trigger := out.Triggers[0]
	conditions, _ := trigger.Conditions.([]any)
	first, _ := conditions[0].(map[string]any)
	if trigger.ID != 7 || trigger.Type != "CustomEvent" || trigger.Name != "Event" || trigger.Description != "e" ||
		trigger.Status != "active" || len(conditions) != 1 || first["actual"] != "ErrorUrl" || first["comparison"] != "contains" ||
		first["expected"] != "<b>foo</b>" || trigger.Created != "2026-01-02 03:04:05" || trigger.UpdatedShown != "Jan 3, 2026" {
		t.Fatalf("trigger = %+v", trigger)
	}
	if calls[len(calls)-1].api() != "TagManager.getContainerTriggers" || strings.Contains(result, "typeMetadata") {
		t.Errorf("call = %+v, result = %s", calls[len(calls)-1], result)
	}
}

func TestTagManagerListsAreEmptyArraysWhenNothingIsConfigured(t *testing.T) {
	for _, tool := range []string{"matomo.tag-manager-tags.list", "matomo.tag-manager-triggers.list"} {
		for _, body := range []string{`[]`, `{}`} {
			var calls []call
			env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
				"TagManager.getContainerTags":     func(call) *http.Response { return ok(body) },
				"TagManager.getContainerTriggers": func(call) *http.Response { return ok(body) },
			}).handler(t))
			result, err := env.invoke(tool, "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)
			// The result leaves empty members out.
			if err != nil || strings.Contains(result, `"id"`) || !strings.Contains(result, `"idContainerVersion":7`) {
				t.Errorf("%s %s: %s, %v", tool, body, result, err)
			}
		}
	}
}

func TestTagManagerTagsGetAndTriggersGetBindTheObject(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerTag": func(c call) *http.Response {
			switch c.form.Get("idTag") {
			case "5":
				return ok(tagRow) // another tag than asked for
			case "6":
				return ok(entryRowOf("idtag", 6, 7, 3)) // another website
			case "7":
				return ok(entryRowOf("idtag", 7, 2, 1)) // another version
			case "8":
				return ok(`{"idtag":8,"type":"X"}`) // bound by the request alone
			case "9":
				return ok(`{"value":false}`)
			case "10":
				return ok(`false`)
			}
			return ok(tagRow)
		},
		"TagManager.getContainerTrigger": func(c call) *http.Response {
			if c.form.Get("idTrigger") == "8" {
				return ok(entryRowOf("idtrigger", 99, 7, 1)) // another trigger than asked for
			}
			if c.form.Get("idTrigger") == "9" {
				return ok(`{"value":false}`)
			}
			return ok(triggerRow)
		},
	}).handler(t))
	base := `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,`
	result, err := env.invoke("matomo.tag-manager-tags.get", "tm-all", base+`"idTag":4}`)
	if err != nil {
		t.Fatal(err)
	}
	var tag EntryResult
	_ = json.Unmarshal([]byte(result), &tag)
	if tag.IDSite != 1 || tag.IDContainer != "6OMh6taM" || tag.IDContainerVersion != 7 || tag.ID != 4 || tag.Name != "Pixel" ||
		len(tag.FireTriggerIDs) != 2 || tag.Parameters == nil || strings.Contains(result, "SECRETMETA") {
		t.Fatalf("tag = %s", result)
	}
	last := calls[len(calls)-1]
	if last.api() != "TagManager.getContainerTag" || last.form.Get("idSite") != "1" || last.form.Get("idContainer") != "6OMh6taM" ||
		last.form.Get("idContainerVersion") != "7" || last.form.Get("idTag") != "4" {
		t.Fatalf("call = %+v", last)
	}
	for _, id := range []string{"5", "6", "7"} {
		if _, err := env.invoke("matomo.tag-manager-tags.get", "tm-all", base+`"idTag":`+id+`}`); classOf(err) != "invalid-provider-response" ||
			strings.Contains(err.Error(), "SECRETMETA") {
			t.Errorf("tag %s: %v", id, err)
		}
	}
	for _, id := range []string{"9", "10"} {
		if _, err := env.invoke("matomo.tag-manager-tags.get", "tm-all", base+`"idTag":`+id+`}`); classOf(err) != "not-found" {
			t.Errorf("missing tag %s: %v", id, err)
		}
	}
	if got, err := env.invoke("matomo.tag-manager-tags.get", "tm-all", base+`"idTag":8}`); err != nil || !strings.Contains(got, `"id":8`) {
		t.Errorf("unbound row: %s, %v", got, err)
	}
	result, err = env.invoke("matomo.tag-manager-triggers.get", "tm-all", base+`"idTrigger":7}`)
	var trigger EntryResult
	_ = json.Unmarshal([]byte(result), &trigger)
	if err != nil || trigger.ID != 7 || trigger.Type != "CustomEvent" || trigger.Conditions == nil ||
		calls[len(calls)-1].form.Get("idTrigger") != "7" || calls[len(calls)-1].api() != "TagManager.getContainerTrigger" {
		t.Fatalf("trigger = %s, %v", result, err)
	}
	if _, err := env.invoke("matomo.tag-manager-triggers.get", "tm-all", base+`"idTrigger":8}`); classOf(err) != "invalid-provider-response" {
		t.Errorf("another trigger: %v", err)
	}
	if _, err := env.invoke("matomo.tag-manager-triggers.get", "tm-all", base+`"idTrigger":9}`); classOf(err) != "not-found" {
		t.Errorf("missing trigger: %v", err)
	}
}

func TestTagManagerTagsBoundUntrustedContent(t *testing.T) {
	big := strings.Repeat("A", 20000)
	deep := `"x"`
	for i := 0; i < 10; i++ {
		deep = `{"k":` + deep + `}`
	}
	many := make([]string, 150)
	for i := range many {
		many[i] = `"v"`
	}
	rows := make([]string, 210)
	for i := range rows {
		rows[i] = `{"idtag":` + itoa(i+1) + `,"type":"CustomHtml","parameters":{"customHtml":"x"}}`
	}
	heavy := make([]string, 21)
	for i := range heavy {
		heavy[i] = `{"idtag":` + itoa(i+1) + `,"parameters":{"a":"` + strings.Repeat("B", 16000) + `"}}`
	}
	one := `{"idtag":1,"type":"CustomHtml","parameters":{"customHtml":"` + big + `","deep":` + deep + `,"many":[` +
		strings.Join(many, ",") + `]}}`
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerTags": func(c call) *http.Response {
			switch c.form.Get("idContainerVersion") {
			case "2":
				return ok(`[` + strings.Join(rows, ",") + `]`)
			case "3":
				return ok(`[` + strings.Join(heavy, ",") + `]`)
			}
			return ok(`[` + one + `]`)
		},
		"TagManager.getContainerTag": func(call) *http.Response { return ok(one) },
	}).handler(t))
	base := `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":`
	result, err := env.invoke("matomo.tag-manager-tags.list", "tm-all", base+`1}`)
	var out TagsResult
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
	result, err = env.invoke("matomo.tag-manager-tags.get", "tm-all", base+`1,"idTag":1}`)
	var tag EntryResult
	_ = json.Unmarshal([]byte(result), &tag)
	if err != nil || !tag.Truncated || len(result) > maxExportText {
		t.Fatalf("get: truncated %t, %d bytes, err %v", tag.Truncated, len(result), err)
	}
	result, err = env.invoke("matomo.tag-manager-tags.list", "tm-all", base+`3}`)
	if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > maxExportText+16384 {
		t.Fatalf("text budget: %d bytes, err %v", len(result), err)
	}
}

func TestTagManagerEntryToolsRefuseMalformedMembers(t *testing.T) {
	for name, body := range map[string]string{
		"entry no id":    `[{"name":"x"}]`,
		"bad trigger id": `[{"idtag":1,"fire_trigger_ids":"{"}]`,
		"not a list":     `"x"`,
	} {
		var calls []call
		env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
			"TagManager.getContainerTags": func(call) *http.Response { return ok(body) },
		}).handler(t))
		result, err := env.invoke("matomo.tag-manager-tags.list", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)
		if classOf(err) != "invalid-provider-response" {
			t.Errorf("%s: %s, %v", name, result, err)
		}
	}
}

func TestTagManagerListsPageToTheEndAndBindTheCursor(t *testing.T) {
	rows := make([]string, 0, 61)
	for i := 1; i <= 60; i++ {
		rows = append(rows, entryRowOf("idtag", i, 7, 1))
		if i%20 == 0 {
			rows = append(rows, entryRowOf("idtag", 1000+i, 7, 9)) // another website, dropped
		}
	}
	trows := strings.ReplaceAll(strings.Join(rows, ","), `"idtag"`, `"idtrigger"`)
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerTags":     func(call) *http.Response { return ok(`[` + strings.Join(rows, ",") + `]`) },
		"TagManager.getContainerTriggers": func(call) *http.Response { return ok(`[` + trows + `]`) },
	}).handler(t))
	for _, tool := range []string{"matomo.tag-manager-tags.list", "matomo.tag-manager-triggers.list"} {
		args := `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"limit":25`
		seen, batches, cursor := 0, 0, ""
		var first string
		for {
			next := args + `}`
			if cursor != "" {
				next = args[:strings.LastIndex(args, `,"limit"`)] + `,"cursor":"` + cursor + `"}`
			}
			result, err := env.invoke(tool, "tm-all", next)
			if err != nil {
				t.Fatalf("%s: %v", tool, err)
			}
			var out struct {
				Tags, Triggers []ExportEntry
				HasMore        bool   `json:"has_more"`
				NextCursor     string `json:"next_cursor"`
			}
			_ = json.Unmarshal([]byte(result), &out)
			entries := append(out.Tags, out.Triggers...)
			if len(entries) == 0 || len(entries) > 25 || (out.HasMore != (out.NextCursor != "")) {
				t.Fatalf("%s batch %d: %s", tool, batches, result)
			}
			if seen == 0 {
				first = result
			}
			if entries[0].ID != seen+1 {
				t.Fatalf("%s batch %d starts at %d, want %d", tool, batches, entries[0].ID, seen+1)
			}
			seen += len(entries)
			batches++
			if !out.HasMore {
				break
			}
			cursor = out.NextCursor
			if batches == 1 {
				// A cursor of another website, container, or version is refused before any request.
				for _, other := range []string{`{"idSite":1,"idContainer":"other123","idContainerVersion":7`,
					`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":8`} {
					before := len(calls)
					if _, err := env.invoke(tool, "tm-all", other+`,"cursor":"`+cursor+`"}`); !isInvalidRequest(err) || len(calls) != before {
						t.Errorf("%s foreign cursor: %v", tool, err)
					}
				}
				if _, err := env.invoke(tool, "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"limit":10,"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
					t.Errorf("%s cursor with another limit: %v", tool, err)
				}
			}
		}
		if seen != 60 || batches != 3 || first == "" {
			t.Errorf("%s: %d entries in %d batches", tool, seen, batches)
		}
		if def, err := env.invoke(tool, "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`); err != nil ||
			strings.Count(def, `"id":`) != 25 {
			t.Errorf("%s default batch: %v", tool, err)
		}
		if big, err := env.invoke(tool, "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"limit":100}`); err != nil ||
			strings.Contains(big, `"has_more":true`) || strings.Count(big, `"id":`) != 60 {
			t.Errorf("%s limit 100: %v", tool, err)
		}
	}
}

func TestTagManagerListsRefuseBadLimitBeforeSecretAndRequest(t *testing.T) {
	for _, tool := range []string{"matomo.tag-manager-tags.list", "matomo.tag-manager-triggers.list"} {
		for _, extra := range []string{`"limit":0`, `"limit":101`, `"limit":-1`, `"cursor":"!"`, `"cursor":""`} {
			var calls []call
			env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{}).handler(t))
			_, err := env.invoke(tool, "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,`+extra+`}`)
			if err == nil || *env.reads != 0 || len(calls) != 0 {
				t.Errorf("%s %s: err = %v, reads = %d, calls = %d", tool, extra, err, *env.reads, len(calls))
			}
		}
	}
}
