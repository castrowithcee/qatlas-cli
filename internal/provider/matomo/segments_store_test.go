package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// segmentRow follows SegmentEditor.get: the columns of the segment table, numbers as strings.
func segmentRow(id, site, name, definition string) string {
	return `{"idsegment":"` + id + `","name":"` + name + `","definition":"` + definition + `","hash":"abc` + id +
		`","login":"admin","enable_all_users":"1","enable_only_idsite":` + site + `,"auto_archive":"0",` +
		`"ts_created":"2026-01-02 03:04:05","ts_last_edit":null,"deleted":"0","starred":"1","starred_by":"admin"}`
}

var storedSegments = "[" + strings.Join([]string{
	segmentRow("1", `"0"`, "All visits DE", "countryCode==de"),
	segmentRow("2", `"1"`, "Site one", "pageUrl=@shop"),
	segmentRow("3", `"2"`, "Site two", "visitCount%3E%3D2"),
	segmentRow("4", `"3"`, "Site three", "countryCode==fr"),
}, ",") + "]"

const segmentConnections = `
  seg-writer: {service: matomo, credential: token, effects: [read, create, update]}
  seg-general: {service: matomo, credential: token, effects: [read, create, update], targets: [site]}
  seg-one: {service: matomo, credential: token, effects: [read, create, update], targets: [site/2]}
  seg-two: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1, site/2]}
  seg-instance: {service: matomo, credential: token, effects: [read, create, update], targets: [instance]}
  seg-deleter: {service: matomo, credential: token, tools: [matomo.segments.get, matomo.segments.delete, matomo.segments.star, matomo.segments.unstar], targets: [site/1, site/2]}
`

func newSegmentsEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(segmentConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// storedAnswer serves SegmentEditor.get by ID from the stored fixture.
func storedAnswer(c call) *http.Response {
	rows := map[string]string{}
	var all []json.RawMessage
	_ = json.Unmarshal([]byte(storedSegments), &all)
	for _, row := range all {
		var head struct {
			ID string `json:"idsegment"`
		}
		_ = json.Unmarshal(row, &head)
		rows[head.ID] = string(row)
	}
	switch id := c.form.Get("idSegment"); id {
	case "8":
		return ok(segmentRow("9", `"1"`, "Another", "countryCode==de"))
	case "7":
		return ok(`"text"`)
	default:
		if row, found := rows[id]; found {
			return ok(row)
		}
		return ok(`null`)
	}
}

func segmentToolAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), with(answers{
		"API.isPluginActivated":   func(call) *http.Response { return ok(`{"value":true}`) },
		"API.getSegmentsMetadata": func(call) *http.Response { return ok(segmentsFixture) },
		"SegmentEditor.get":       storedAnswer,
	}, extra))
}

func methods(calls []call) string {
	var out []string
	for _, c := range calls {
		out = append(out, c.api())
	}
	return strings.Join(out, " ")
}

func TestSegmentToolsAreRegisteredWithTheirRiskAndProfile(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		confirm     capability.Confirmation
	}{
		"matomo.segments.list":   {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone},
		"matomo.segments.get":    {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone},
		"matomo.segments.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired},
		"matomo.segments.update": {capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired},
		"matomo.segments.star":   {capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired},
		"matomo.segments.unstar": {capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired},
		"matomo.segments.delete": {capability.EffectDelete, capability.IdempotencyIdempotent, capability.ConfirmationRequired},
	}
	for id, tc := range want {
		d, _, ok := reg.Lookup(id)
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != tc.confirm ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.RequiresToolAllowList != (id == "matomo.segments.delete") {
			t.Errorf("%s: risk = %+v", id, d.Risk)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := strings.Join(profile.Tools, " ")
		switch profile.ID {
		case "segments":
			if profile.Recommended || !strings.Contains(joined, "matomo.segments.add") ||
				!strings.Contains(joined, "matomo.segments.update") || !strings.Contains(joined, "matomo.segments.get") ||
				!strings.Contains(joined, "matomo.segments.star") || !strings.Contains(joined, "matomo.segments.unstar") ||
				strings.Contains(joined, "matomo.segments.delete") {
				t.Errorf("segments profile = %+v", profile)
			}
		default:
			if strings.Contains(joined, "matomo.segments.add") || strings.Contains(joined, "matomo.segments.update") ||
				strings.Contains(joined, "matomo.segments.star") || strings.Contains(joined, "matomo.segments.delete") {
				t.Errorf("profile %s selects a segment change", profile.ID)
			}
			if !strings.Contains(joined, "matomo.segments.list") || !strings.Contains(joined, "matomo.segments.get") {
				t.Errorf("profile %s lacks the segment reads", profile.ID)
			}
		}
	}
}

func TestSegmentsListCutsRowsToTheTargets(t *testing.T) {
	for _, tc := range []struct {
		connection string
		want       []int
	}{
		{"seg-writer", []int{1, 2, 3, 4}},
		{"seg-general", []int{1, 2, 3, 4}},
		{"seg-one", []int{1, 3}},
		{"seg-two", []int{1, 2, 3}},
	} {
		var calls []call
		env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
			"SegmentEditor.getAll": func(call) *http.Response { return ok(storedSegments) },
		}).handler(t))
		result, err := env.invoke("matomo.segments.list", tc.connection, `{}`)
		if err != nil {
			t.Fatalf("%s: %v", tc.connection, err)
		}
		var out SegmentsResult
		_ = json.Unmarshal([]byte(result), &out)
		var got []int
		for _, s := range out.Segments {
			got = append(got, s.ID)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: segments = %v, want %v", tc.connection, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: segments = %v, want %v", tc.connection, got, tc.want)
			}
		}
		if last := calls[len(calls)-1]; last.api() != "SegmentEditor.getAll" || last.form.Has("idSite") {
			t.Errorf("%s: request = %v", tc.connection, last.form)
		}
	}
}

func TestSegmentsListWithASiteReadsEveryFieldAndPages(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.getAll": func(call) *http.Response { return ok(storedSegments) },
	}).handler(t))
	result, err := env.invoke("matomo.segments.list", "seg-general", `{"idSite":1,"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out SegmentsResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || len(out.Segments) != 1 || !out.HasMore || out.NextCursor == "" {
		t.Fatalf("out = %s", result)
	}
	first := out.Segments[0]
	if first.ID != 1 || first.Name != "All visits DE" || first.Definition != "countryCode==de" || first.Hash != "abc1" ||
		first.Login != "admin" || first.EnableAllUsers == nil || !*first.EnableAllUsers || first.EnableOnlyIDSite != 0 ||
		first.AutoArchive == nil || *first.AutoArchive || first.Created != "2026-01-02 03:04:05" ||
		first.LastEdit != "" || first.Deleted == nil || *first.Deleted || first.Starred == nil || !*first.Starred ||
		first.StarredBy != "admin" {
		t.Errorf("segment = %+v", first)
	}
	if last := calls[len(calls)-1]; last.form.Get("idSite") != "1" {
		t.Errorf("request = %v", last.form)
	}
	next, err := env.invoke("matomo.segments.list", "seg-general", `{"idSite":1,"cursor":"`+out.NextCursor+`"}`)
	if err != nil || !strings.Contains(next, `"id":2`) || strings.Contains(next, "next_cursor") || strings.Contains(next, `"id":1,`) {
		t.Fatalf("next = %s, err = %v", next, err)
	}
	if _, err := env.invoke("matomo.segments.list", "seg-general", `{"idSite":2,"cursor":"`+out.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another website: err = %v", err)
	}
}

func TestSegmentsListWithASiteKeepsOnlyThatSiteAndAllWebsites(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.getAll": func(call) *http.Response { return ok(storedSegments) },
	}).handler(t))
	result, err := env.invoke("matomo.segments.list", "seg-general", `{"idSite":1}`)
	var out SegmentsResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Segments) != 2 || out.Segments[0].ID != 1 || out.Segments[1].ID != 2 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	// With site/ID targets, a segment for all websites is readable.
	result, err = env.invoke("matomo.segments.list", "seg-two", `{"idSite":1}`)
	out = SegmentsResult{}
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Segments) != 2 || out.Segments[0].ID != 1 || out.Segments[1].ID != 2 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestSegmentsListDropsUnreadableRowsAndBoundsText(t *testing.T) {
	var calls []call
	long := strings.Repeat("n", 900)
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.getAll": func(call) *http.Response {
			return ok(`[{"idsegment":"5","name":"` + long + `","definition":"countryCode==de","enable_only_idsite":null},` +
				`{"idsegment":"6","name":"bad site","definition":"x","enable_only_idsite":"north"},` +
				`{"idsegment":"x","name":"bad id","enable_only_idsite":"0"},` +
				`{"idsegment":"7","name":"negative","definition":"x","enable_only_idsite":"-1"},"text"]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.segments.list", "seg-writer", `{}`)
	var out SegmentsResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Segments) != 1 || out.Segments[0].ID != 5 || len(out.Segments[0].Name) != maxStringLength ||
		out.Segments[0].EnableOnlyIDSite != 0 {
		t.Fatalf("result = %.200s, err = %v", result, err)
	}
	empty := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.getAll": func(call) *http.Response { return ok(`[]`) },
	}).handler(t))
	if got, err := empty.invoke("matomo.segments.list", "seg-writer", `{}`); err != nil || got != `{"has_more":false}` {
		t.Errorf("empty = %s, err = %v", got, err)
	}
}

func TestSegmentsGetProvesTheBindingWithOneRead(t *testing.T) {
	for _, tc := range []struct {
		connection, id string
		found          bool
	}{
		{"seg-writer", "1", true}, {"seg-writer", "4", true},
		{"seg-general", "1", true}, {"seg-general", "3", true},
		{"seg-one", "3", true}, {"seg-one", "2", false}, {"seg-one", "1", true}, {"seg-one", "4", false},
		{"seg-two", "1", true}, {"seg-two", "4", false}, {"seg-two", "2", true},
		{"seg-writer", "99", false},
	} {
		var calls []call
		env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{}).handler(t))
		result, err := env.invoke("matomo.segments.get", tc.connection, `{"idSegment":`+tc.id+`}`)
		if tc.found {
			var out Segment
			_ = json.Unmarshal([]byte(result), &out)
			if err != nil || out.ID == 0 || out.Definition == "" {
				t.Errorf("%s/%s: result = %s, err = %v", tc.connection, tc.id, result, err)
			}
		} else if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), "Site") ||
			strings.Contains(err.Error(), "site/") {
			t.Errorf("%s/%s: err = %v, want not-found without a value", tc.connection, tc.id, err)
		}
		if n := countMethod(calls, "SegmentEditor.get"); n != 1 {
			t.Errorf("%s/%s: SegmentEditor.get was sent %d times", tc.connection, tc.id, n)
		}
	}
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{}).handler(t))
	if _, err := env.invoke("matomo.segments.get", "seg-writer", `{"idSegment":8}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("another segment: err = %v", err)
	}
	if _, err := env.invoke("matomo.segments.get", "seg-writer", `{"idSegment":7}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("text answer: err = %v", err)
	}
	for _, bad := range []string{`{}`, `{"idSegment":0}`, `{"idSegment":"x"}`, `{"idSegment":1,"idSite":1}`} {
		if _, err := env.invoke("matomo.segments.get", "seg-writer", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSegmentsListForeignWebsiteIsRefusedBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{}).handler(t))
	_, err := env.invoke("matomo.segments.list", "seg-one", `{"idSite":1}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") || len(calls) != 0 || *env.reads != 0 {
		t.Errorf("err = %v, %d requests, %d secret reads", err, len(calls), *env.reads)
	}
}

func TestSegmentsAddSendsOneConfirmedRequest(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.add": func(call) *http.Response { return ok(`{"value":12}`) },
	}).handler(t))
	args := `{"idSite":1,"name":"Germany","definition":"countryCode==de;pageUrl=@shop%2C1","autoArchive":true,"enabledAllUsers":true}`
	if _, err := env.invoke("matomo.segments.add", "seg-two", args); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("an unconfirmed call caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	result, err := env.change("matomo.segments.add", "seg-two", args)
	if err != nil || result != `{"created":true,"idSegment":12,"idSite":1}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if countMethod(calls, "SegmentEditor.add") != 1 || countMethod(calls, "API.getSegmentsMetadata") != 1 {
		t.Fatalf("requests = %s", methods(calls))
	}
	if metadata := calls[len(calls)-2]; metadata.api() != "API.getSegmentsMetadata" || metadata.form.Get("idSites") != "1" {
		t.Errorf("metadata request = %v", metadata.form)
	}
	sent := calls[len(calls)-1]
	for name, want := range map[string]string{"idSite": "1", "name": "Germany",
		"definition": "countryCode==de;pageUrl=@shop%2C1", "autoArchive": "1", "enabledAllUsers": "1"} {
		if sent.form.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.form.Get(name), want)
		}
	}
	if sent.auth != "Bearer "+tokenValue || sent.form.Has("token_auth") || strings.Contains(sent.rawQuery, tokenValue) {
		t.Errorf("token handling: %+v", sent)
	}
	if _, err := env.change("matomo.segments.add", "seg-two", `{"idSite":1,"name":"a","definition":"countryCode==de"}`); err != nil {
		t.Fatal(err)
	}
	if last := calls[len(calls)-1]; last.form.Get("autoArchive") != "0" || last.form.Get("enabledAllUsers") != "0" {
		t.Errorf("defaults = %v", last.form)
	}
}

func TestSegmentsAddBindsAllWebsitesOnlyToAWideConnection(t *testing.T) {
	const allWebsites = `{"name":"Germany","definition":"countryCode==de"}`
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.add": func(call) *http.Response { return ok(`{"value":12}`) },
	}).handler(t))
	for _, args := range []string{allWebsites, `{"idSite":0,"name":"Germany","definition":"countryCode==de"}`} {
		for _, connection := range []string{"seg-writer", "seg-general"} {
			result, err := env.change("matomo.segments.add", connection, args)
			if err != nil || result != `{"created":true,"idSegment":12,"idSite":0}` {
				t.Fatalf("%s %s: result = %s, err = %v", connection, args, result, err)
			}
			sent := calls[len(calls)-1]
			if sent.api() != "SegmentEditor.add" || sent.form.Has("idSite") {
				t.Errorf("all-websites request = %v", sent.form)
			}
			if metadata := calls[len(calls)-2]; metadata.form.Has("idSites") {
				t.Errorf("metadata request = %v", metadata.form)
			}
		}
	}
	// Single-website targets never reach a segment for all websites.
	before, reads := len(calls), *env.reads
	for _, tc := range []struct{ connection, args string }{
		{"seg-two", allWebsites},
		{"seg-two", `{"idSite":0,"name":"a","definition":"countryCode==de"}`},
		{"seg-one", `{"idSite":0,"name":"a","definition":"countryCode==de"}`},
		{"seg-one", `{"idSite":1,"name":"a","definition":"countryCode==de"}`},
	} {
		if _, err := env.change("matomo.segments.add", tc.connection, tc.args); err == nil || !isInvalidRequest(err) {
			t.Errorf("%s %s: err = %v, want an invalid request", tc.connection, tc.args, err)
		}
	}
	if len(calls) != before || *env.reads != reads {
		t.Errorf("refusals caused %d requests and %d secret reads", len(calls)-before, *env.reads-reads)
	}
	// A connection that binds exactly one website uses it when idSite is omitted.
	if result, err := env.change("matomo.segments.add", "seg-one", allWebsites); err != nil ||
		result != `{"created":true,"idSegment":12,"idSite":2}` {
		t.Fatalf("one website: result = %s, err = %v", result, err)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("idSite") != "2" {
		t.Errorf("request = %v", sent.form)
	}
}

func TestSegmentDefinitionIsCheckedStructurally(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{}).handler(t))
	for name, args := range map[string]string{
		"no name":           `{"idSite":1,"definition":"countryCode==de"}`,
		"blank name":        `{"idSite":1,"name":"  ","definition":"countryCode==de"}`,
		"long name":         `{"idSite":1,"name":"` + strings.Repeat("n", 256) + `","definition":"countryCode==de"}`,
		"no definition":     `{"idSite":1,"name":"a"}`,
		"free text":         `{"idSite":1,"name":"a","definition":"all german visitors"}`,
		"no operator":       `{"idSite":1,"name":"a","definition":"countryCode"}`,
		"dangling and":      `{"idSite":1,"name":"a","definition":"countryCode==de;"}`,
		"double or":         `{"idSite":1,"name":"a","definition":"countryCode==de,,pageUrl=@x"}`,
		"control character": `{"idSite":1,"name":"a","definition":"pageUrl==x\ny"}`,
		"too many terms":    `{"idSite":1,"name":"a","definition":"` + strings.TrimSuffix(strings.Repeat("countryCode==de;", 21), ";") + `"}`,
		"too long":          `{"idSite":1,"name":"a","definition":"pageUrl==` + strings.Repeat("x", 1001) + `"}`,
		"unknown argument":  `{"idSite":1,"name":"a","definition":"countryCode==de","idSegment":3}`,
		"negative site":     `{"idSite":-1,"name":"a","definition":"countryCode==de"}`,
		"text flag":         `{"idSite":1,"name":"a","definition":"countryCode==de","autoArchive":"yes"}`,
	} {
		if _, err := env.change("matomo.segments.add", "seg-two", args); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := env.change("matomo.segments.update", "seg-two", `{"idSite":1,"name":"a","definition":"countryCode==de"}`); err == nil {
		t.Error("update without idSegment accepted")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("refused arguments caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	// A well-formed definition with a field the website does not offer is refused before the change.
	for _, definition := range []string{"noSuchField==x", "countryCode==de;noSuchField==x"} {
		_, err := env.change("matomo.segments.add", "seg-two", `{"idSite":1,"name":"a","definition":"`+definition+`"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "noSuchField") {
			t.Errorf("%s: err = %v, want an invalid request that quotes no field", definition, err)
		}
	}
	if countMethod(calls, "SegmentEditor.add") != 0 {
		t.Error("an unknown field reached SegmentEditor.add")
	}
}

func TestSegmentsUpdateReadsOnceThenReplaces(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.update": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}).handler(t))
	args := `{"idSegment":2,"idSite":1,"name":"Renamed","definition":"pageUrl=@shop","autoArchive":false}`
	if _, err := env.invoke("matomo.segments.update", "seg-two", args); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("an unconfirmed call caused %d requests", len(calls))
	}
	result, err := env.change("matomo.segments.update", "seg-two", args)
	if err != nil || result != `{"idSegment":2,"idSite":1,"updated":true}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated SegmentEditor.get API.getSegmentsMetadata SegmentEditor.update" {
		t.Errorf("requests = %s", got)
	}
	sent := calls[len(calls)-1]
	for name, want := range map[string]string{"idSegment": "2", "idSite": "1", "name": "Renamed",
		"definition": "pageUrl=@shop", "autoArchive": "0", "enabledAllUsers": "0"} {
		if sent.form.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.form.Get(name), want)
		}
	}
	// Without idSite the segment keeps its binding.
	if result, err := env.change("matomo.segments.update", "seg-general",
		`{"idSegment":3,"name":"a","definition":"countryCode==de"}`); err != nil ||
		result != `{"idSegment":3,"idSite":2,"updated":true}` {
		t.Fatalf("kept binding: result = %s, err = %v", result, err)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("idSite") != "2" {
		t.Errorf("request = %v", sent.form)
	}
	if result, err := env.change("matomo.segments.update", "seg-general",
		`{"idSegment":1,"name":"a","definition":"countryCode==de"}`); err != nil ||
		result != `{"idSegment":1,"idSite":0,"updated":true}` {
		t.Fatalf("kept all websites: result = %s, err = %v", result, err)
	}
	if sent := calls[len(calls)-1]; sent.form.Has("idSite") {
		t.Errorf("request = %v", sent.form)
	}
	// Moving a segment to all websites needs a wide connection; a wide one may do it.
	if result, err := env.change("matomo.segments.update", "seg-general",
		`{"idSegment":2,"idSite":0,"name":"All","definition":"pageUrl=@shop"}`); err != nil ||
		result != `{"idSegment":2,"idSite":0,"updated":true}` {
		t.Fatalf("all websites: result = %s, err = %v", result, err)
	}
	if sent := calls[len(calls)-1]; sent.form.Has("idSite") {
		t.Errorf("all-websites request = %v", sent.form)
	}
}

func TestSegmentsUpdateRefusesSegmentsOutsideTheTargets(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
		"SegmentEditor.update": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}).handler(t))
	definition := `"name":"a","definition":"countryCode==de"`
	for name, args := range map[string]string{
		"all websites segment":    `{"idSegment":1,"idSite":1,` + definition + `}`,
		"missing segment":         `{"idSegment":99,"idSite":1,` + definition + `}`,
		"third website segment":   `{"idSegment":4,"idSite":1,` + definition + `}`,
		"move into a foreign one": `{"idSegment":2,"idSite":3,` + definition + `}`,
		"move to all websites":    `{"idSegment":2,"idSite":0,` + definition + `}`,
	} {
		calls = nil
		reads := *env.reads
		_, err := env.change("matomo.segments.update", "seg-two", args)
		switch name {
		case "all websites segment", "missing segment", "third website segment":
			if classOf(err) != provider.ClassNotFound || countMethod(calls, "SegmentEditor.get") != 1 {
				t.Errorf("%s: err = %v, reads = %d", name, err, countMethod(calls, "SegmentEditor.get"))
			}
		default:
			if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != reads {
				t.Errorf("%s: err = %v, %d requests", name, err, len(calls))
			}
		}
		if countMethod(calls, "SegmentEditor.update") != 0 {
			t.Errorf("%s: the update was sent", name)
		}
	}
}

func TestSegmentErrorsMapToClassesWithoutTheMatomoText(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		class         provider.Class
		invalid       bool
		unsupported   bool
	}{
		{"permission", "You can't access this resource as it requires 'view' access for the website id = 1.", provider.ClassPermission, false, false},
		{"creation right", "You don't have the required access level to create and edit segments.", provider.ClassPermission, false, false},
		{"super user", "enabledAllUsers=1 requires Super User access", provider.ClassPermission, false, false},
		{"all websites off", "Adding segments for all websites has been disabled.", provider.ClassPermission, false, false},
		{"browser archiving", "Pre-processed segments can only be created if browser triggered archiving is disabled.", provider.ClassPermission, false, false},
		{"realtime off", "Real time segments are disabled. You need to enable auto archiving.", provider.ClassPermission, false, false},
		{"invalid segment", "The specified segment is invalid: Segment 'x' is not a supported segment", "", true, false},
		{"invalid name", "Invalid name for this custom segment.", "", true, false},
		{"plugin inactive", "The plugin SegmentEditor is not activated.", "", false, true},
		{"unknown text", "Something went wrong", provider.ClassInvalidResponse, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
				"SegmentEditor.add": func(call) *http.Response { return failure(tc.message + bodyCanary) },
			}).handler(t))
			_, err := env.change("matomo.segments.add", "seg-two", `{"idSite":1,"name":"a","definition":"countryCode==de"}`)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "supported segment") ||
				tc.invalid != isInvalidRequest(err) || tc.unsupported != isUnsupported(err) || classOf(err) != tc.class {
				t.Fatalf("err = %v (class %q), want class %q invalid %t unsupported %t", err, classOf(err), tc.class, tc.invalid, tc.unsupported)
			}
			if countMethod(calls, "SegmentEditor.add") != 1 {
				t.Errorf("SegmentEditor.add was sent %d times, want once", countMethod(calls, "SegmentEditor.add"))
			}
		})
	}
}

func TestSegmentReadErrorsMapToClasses(t *testing.T) {
	for _, tc := range []struct {
		message string
		class   provider.Class
	}{
		{"You can't access this resource as it requires 'view' access for the website id = 3.", provider.ClassPermission},
		{"You can only edit and delete custom segments that you have created yourself. This segment was created and 'shared with you' by the Super User.", provider.ClassPermission},
		{"Requested segment not found", provider.ClassNotFound},
		{"This segment is marked as deleted. ", provider.ClassNotFound},
	} {
		var calls []call
		env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{
			"SegmentEditor.get": func(call) *http.Response { return failure(tc.message + bodyCanary) },
		}).handler(t))
		_, err := env.invoke("matomo.segments.get", "seg-writer", `{"idSegment":3}`)
		if classOf(err) != tc.class || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%q: err = %v, want %s", tc.message, err, tc.class)
		}
	}
}

func TestInactiveSegmentEditorRefusesTheToolsWithoutFurtherRequests(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) },
	}).handler(t))
	_, err := env.change("matomo.segments.add", "seg-two", `{"idSite":1,"name":"a","definition":"countryCode==de"}`)
	if !isUnsupported(err) || !strings.Contains(err.Error(), "SegmentEditor") {
		t.Fatalf("err = %v", err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated" {
		t.Errorf("requests = %s", got)
	}
}

func TestUnclearSegmentChangeIsNotRepeatedAndSaysSo(t *testing.T) {
	const add = `{"idSite":1,"name":"a","definition":"countryCode==de"}`
	for name, handler := range map[string]func(call) *http.Response{
		"html":       func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
		"no ID":      func(call) *http.Response { return ok(`{"value":"none"}`) },
		"server":     func(call) *http.Response { return respond(500, bodyCanary) },
		"redirected": func(call) *http.Response { return respond(302, "") },
	} {
		var calls []call
		env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{"SegmentEditor.add": handler}).handler(t))
		_, err := env.change("matomo.segments.add", "seg-two", add)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || !strings.Contains(err.Error(), "never repeated") ||
			!strings.Contains(err.Error(), "matomo.segments.get") || countMethod(calls, "SegmentEditor.add") != 1 {
			t.Errorf("%s: err = %v, add sent %d times", name, err, countMethod(calls, "SegmentEditor.add"))
		}
	}
	const update = `{"idSegment":2,"idSite":1,"name":"a","definition":"countryCode==de"}`
	var calls []call
	env := newSegmentsEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.api() == "SegmentEditor.update" {
			return nil, context.DeadlineExceeded
		}
		return segmentToolAnswers(answers{}).handler(t)(c)
	})
	_, err := env.change("matomo.segments.update", "seg-two", update)
	if classOf(err) != provider.ClassTimeout || !strings.Contains(err.Error(), "never repeated") ||
		countMethod(calls, "SegmentEditor.update") != 1 {
		t.Errorf("timeout: err = %v, update sent %d times", err, countMethod(calls, "SegmentEditor.update"))
	}
	var calls2 []call
	env2 := newSegmentsEnvironment(t, &calls2, segmentToolAnswers(answers{
		"SegmentEditor.update": func(call) *http.Response { return ok(`{"value":3}`) },
	}).handler(t))
	if _, err := env2.change("matomo.segments.update", "seg-two", update); err == nil || !strings.Contains(err.Error(), "never repeated") {
		t.Errorf("unconfirmed update: err = %v", err)
	}
}

func TestRateLimitedSegmentChangeIsACleanRefusal(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.api() == "SegmentEditor.add" {
			return respond(429, ""), nil
		}
		return segmentToolAnswers(answers{}).handler(t)(c)
	})
	_, err := env.change("matomo.segments.add", "seg-two", `{"idSite":1,"name":"a","definition":"countryCode==de"}`)
	if classOf(err) != provider.ClassRateLimited || strings.Contains(err.Error(), "never repeated") ||
		countMethod(calls, "SegmentEditor.add") != 1 {
		t.Errorf("err = %v, add sent %d times", err, countMethod(calls, "SegmentEditor.add"))
	}
}

func TestSegmentToolsNeedAWebsiteBinding(t *testing.T) {
	var calls []call
	env := newSegmentsEnvironment(t, &calls, segmentToolAnswers(answers{}).handler(t))
	for _, tool := range []struct{ name, args string }{
		{"matomo.segments.list", `{}`},
		{"matomo.segments.get", `{"idSegment":1}`},
		{"matomo.segments.add", `{"name":"a","definition":"countryCode==de"}`},
		{"matomo.segments.update", `{"idSegment":1,"idSite":0,"name":"a","definition":"countryCode==de"}`},
	} {
		if _, err := env.change(tool.name, "seg-instance", tool.args); err == nil {
			t.Errorf("%s ran on an instance-only binding", tool.name)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads", len(calls), *env.reads)
	}
}
