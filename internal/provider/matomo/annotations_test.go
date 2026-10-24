package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// annotationsFixture follows Annotations.getAll: an object keyed by website ID. The second website's entry and
// a row that names another website are not the requested one.
const annotationsFixture = `{
"1":[
 {"id":"5","idsite":"1","date":"2026-03-02 00:00:00","note":"Release &lt;b&gt;","starred":"1","user":"alice-login","idNote":"5","canEditOrDelete":false},
 {"id":"3","idsite":"1","date":"2026-03-02","note":"Campaign","starred":0,"user":"bob-login","idNote":3,"canEditOrDelete":true},
 {"id":"4","idsite":"1","date":"2026-01-10","note":"Earlier","starred":"0","user":"bob-login","idNote":"4","canEditOrDelete":true},
 {"id":"9","idsite":"2","date":"2026-01-11","note":"Foreign row","starred":"0","user":"x","idNote":"9","canEditOrDelete":true}
],
"2":[{"id":"8","idsite":"2","date":"2026-01-11","note":"Other site","starred":"0","user":"x","idNote":"8"}]
}`

const annotationOne = `{"id":"3","idsite":"1","date":"2026-03-02","note":"Campaign","starred":"1","user":"bob-login",
"idNote":"3","canEditOrDelete":true}`

func annotationAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), with(answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":true}`) },
	}, extra))
}

func TestAnnotationToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		confirm     capability.Confirmation
	}{
		"matomo.annotations.list":   {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone},
		"matomo.annotations.get":    {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone},
		"matomo.annotations.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired},
		"matomo.annotations.update": {capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired},
		"matomo.annotations.count":  {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone},
	}
	for id, tc := range want {
		d, _, found := reg.Lookup(id)
		if !found {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != tc.confirm ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.RequiresToolAllowList {
			t.Errorf("%s: risk = %+v", id, d.Risk)
		}
	}
	add, _, _ := reg.Lookup("matomo.annotations.add")
	for _, part := range []string{"Matomo 5", "view", "Matomo 6", "write"} {
		if !strings.Contains(add.Description, part) {
			t.Errorf("add description lacks %q", part)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := strings.Join(profile.Tools, " ")
		switch profile.ID {
		case "read":
			if !strings.Contains(joined, "matomo.annotations.list") || !strings.Contains(joined, "matomo.annotations.get") ||
				strings.Contains(joined, "matomo.annotations.add") {
				t.Errorf("read profile = %v", profile.Tools)
			}
		case "annotations":
			if profile.Recommended || !strings.Contains(joined, "matomo.annotations.add") ||
				!strings.Contains(joined, "matomo.annotations.update") || !strings.Contains(joined, "matomo.annotations.get") {
				t.Errorf("annotations profile = %+v", profile)
			}
		}
	}
}

func TestAnnotationsListOrdersPagesAndKeepsOnlyTheWebsite(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.getAll": func(call) *http.Response { return ok(annotationsFixture) },
	}).handler(t))
	result, err := env.invoke("matomo.annotations.list", "open", `{"idSite":1,"limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var out AnnotationsResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || len(out.Annotations) != 2 || out.Annotations[0].IDNote != 4 || out.Annotations[1].IDNote != 3 ||
		!out.HasMore || out.NextCursor == "" {
		t.Fatalf("out = %s", result)
	}
	first := out.Annotations[0]
	if first.Date != "2026-01-10" || first.User != "bob-login" || first.CanEditOrDelete == nil || !*first.CanEditOrDelete ||
		first.Starred == nil || *first.Starred {
		t.Errorf("first = %+v", first)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("idSite") != "1" || sent.form.Has("date") || sent.form.Has("period") {
		t.Errorf("request = %v", sent.form)
	}
	next, err := env.invoke("matomo.annotations.list", "open", `{"idSite":1,"cursor":"`+out.NextCursor+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var tail AnnotationsResult
	_ = json.Unmarshal([]byte(next), &tail)
	if len(tail.Annotations) != 1 || tail.Annotations[0].IDNote != 5 || tail.HasMore ||
		tail.Annotations[0].Note != "Release &lt;b&gt;" || tail.Annotations[0].Date != "2026-03-02" {
		t.Fatalf("tail = %s", next)
	}
	if _, err := env.invoke("matomo.annotations.list", "open", `{"idSite":1,"date":"2026-01-01","cursor":"`+out.NextCursor+`"}`); err == nil {
		t.Error("a cursor of another list was accepted")
	}
}

func TestAnnotationsListSendsTheDateRange(t *testing.T) {
	var calls2 []call
	env := newGoalsEnvironment(t, &calls2, annotationAnswers(answers{
		"Annotations.getAll": func(call) *http.Response { return ok(`{"1":[]}`) },
	}).handler(t))
	result, err := env.invoke("matomo.annotations.list", "open", `{"idSite":1,"date":"today","period":"month","lastN":3}`)
	if err != nil || strings.Contains(result, "idNote") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if sent := calls2[len(calls2)-1]; sent.form.Get("date") != "today" || sent.form.Get("period") != "month" ||
		sent.form.Get("lastN") != "3" {
		t.Errorf("request = %v", sent.form)
	}
	for _, bad := range []string{`{"idSite":1,"date":"last7"}`, `{"idSite":1,"period":"day"}`,
		`{"idSite":1,"date":"2026-01-01","period":"week","lastN":0}`, `{"idSite":1,"date":"2026-01-01,2026-02-01"}`,
		`{"idSite":1,"date":"2026-01-01","period":"range"}`, `{"idSite":"1,2"}`} {
		if _, err := env.invoke("matomo.annotations.list", "open", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if _, err := env.invoke("matomo.annotations.list", "open",
		`{"idSite":1,"date":"2026-01-01,2026-02-01","period":"range"}`); err != nil {
		t.Errorf("range: %v", err)
	}
}

func TestAnnotationsGetReadsOneNoteOrIsNotFound(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.get": func(c call) *http.Response {
			switch c.form.Get("idNote") {
			case "3":
				return ok(annotationOne)
			case "4":
				return ok(`{"id":"7","idsite":"1","date":"2026-01-10","note":"x","idNote":"7"}`)
			case "5":
				return ok(`{"id":"5","idsite":"2","date":"2026-01-10","note":"x","idNote":"5"}`)
			}
			return failure("There is no note with id '9' for site with id '1'.")
		},
	}).handler(t))
	result, err := env.invoke("matomo.annotations.get", "open", `{"idSite":1,"idNote":3}`)
	var out AnnotationResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || out.IDSite != 1 || out.IDNote != 3 || out.Note != "Campaign" || out.Starred == nil || !*out.Starred ||
		out.User != "bob-login" {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if last := calls[len(calls)-1]; last.form.Get("idSite") != "1" || last.form.Get("idNote") != "3" {
		t.Errorf("request = %v", last.form)
	}
	if _, err := env.invoke("matomo.annotations.get", "open", `{"idSite":1,"idNote":9}`); classOf(err) != provider.ClassNotFound {
		t.Errorf("unknown note: err = %v, want not-found", err)
	}
	for _, id := range []string{"4", "5"} {
		if _, err := env.invoke("matomo.annotations.get", "open", `{"idSite":1,"idNote":`+id+`}`); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("note %s: err = %v, want invalid-provider-response", id, err)
		}
	}
	for _, bad := range []string{`{"idSite":1}`, `{"idSite":1,"idNote":0}`} {
		if _, err := env.invoke("matomo.annotations.get", "open", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestAnnotationsAddSendsOneConfirmedRequest(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.add": func(call) *http.Response { return ok(annotationOne) },
	}).handler(t))
	args := `{"idSite":1,"date":"2026-03-02","note":"Campaign","starred":true}`
	if _, err := env.invoke("matomo.annotations.add", "writer", args); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("an unconfirmed call caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	result, err := env.change("matomo.annotations.add", "writer", args)
	var out AnnotationAdded
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Created || out.IDSite != 1 || out.IDNote != 3 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if countMethod(calls, "Annotations.add") != 1 {
		t.Fatalf("add was sent %d times", countMethod(calls, "Annotations.add"))
	}
	sent := calls[len(calls)-1]
	for name, want := range map[string]string{"idSite": "1", "date": "2026-03-02", "note": "Campaign", "starred": "1"} {
		if sent.form.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.form.Get(name), want)
		}
	}
	if sent.auth != "Bearer "+tokenValue || sent.form.Has("token_auth") || strings.Contains(sent.rawQuery, tokenValue) {
		t.Errorf("token handling: %+v", sent)
	}
	if _, err := env.change("matomo.annotations.add", "reader", args); err == nil {
		t.Error("a read connection offered annotations.add")
	}
	long := strings.Repeat("ä", 256)
	for _, bad := range []string{`{"idSite":1,"date":"2026-03-02","note":"` + long + `"}`,
		`{"idSite":1,"date":"2026-03-02","note":"  "}`, `{"idSite":1,"date":"soon","note":"a"}`,
		`{"idSite":1,"note":"a"}`, `{"idSite":1,"date":"2026-03-02"}`} {
		before := len(calls)
		if _, err := env.change("matomo.annotations.add", "writer", bad); err == nil || len(calls) != before {
			t.Errorf("%.60s: err = %v, %d new requests", bad, err, len(calls)-before)
		}
	}
	if _, err := env.change("matomo.annotations.add", "writer",
		`{"idSite":1,"date":"2026-03-02","note":"`+strings.Repeat("ä", 255)+`"}`); err != nil {
		t.Errorf("a note of 255 characters: %v", err)
	}
}

func TestAnnotationsUpdateSendsOnlyGivenValuesWithoutPriorRead(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.save": func(c call) *http.Response {
			if c.form.Get("idNote") == "4" {
				return ok(`{"id":"8","idsite":"1","date":"2026-03-02","note":"x","idNote":"8"}`)
			}
			if c.form.Get("idNote") == "9" {
				return failure("There is no note with id '9' for site with id '1'.")
			}
			return ok(annotationOne)
		},
	}).handler(t))
	result, err := env.change("matomo.annotations.update", "writer", `{"idSite":1,"idNote":3,"starred":false}`)
	var out AnnotationUpdated
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Updated || out.IDNote != 3 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.form.Get("starred") != "0" || sent.form.Has("note") || sent.form.Has("date") ||
		sent.form.Get("idSite") != "1" || sent.form.Get("idNote") != "3" {
		t.Errorf("request = %v", sent.form)
	}
	if countMethod(calls, "Annotations.save") != 1 || countMethod(calls, "Annotations.get") != 0 {
		t.Errorf("save %d, get %d", countMethod(calls, "Annotations.save"), countMethod(calls, "Annotations.get"))
	}
	if _, err := env.change("matomo.annotations.update", "writer", `{"idSite":1,"idNote":3,"note":"New","date":"yesterday"}`); err != nil {
		t.Fatal(err)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("note") != "New" || sent.form.Get("date") != "yesterday" || sent.form.Has("starred") {
		t.Errorf("request = %v", sent.form)
	}
	if _, err := env.change("matomo.annotations.update", "writer", `{"idSite":1,"idNote":9,"note":"a"}`); classOf(err) != provider.ClassNotFound {
		t.Errorf("unknown note: err = %v", err)
	}
	if _, err := env.change("matomo.annotations.update", "writer", `{"idSite":1,"idNote":4,"note":"a"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("other note: err = %v", err)
	}
	for _, bad := range []string{`{"idSite":1,"idNote":3}`, `{"idSite":1,"idNote":3,"note":""}`,
		`{"idSite":1,"idNote":3,"date":"2026-13"}`, `{"idSite":1,"note":"a"}`} {
		before := len(calls)
		if _, err := env.change("matomo.annotations.update", "writer", bad); err == nil || len(calls) != before {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
}

func TestAnnotationsForeignWebsiteIsRefusedBeforeSecretAndRequest(t *testing.T) {
	for _, tool := range []struct{ name, args string }{
		{"matomo.annotations.list", `{"idSite":1}`},
		{"matomo.annotations.get", `{"idSite":1,"idNote":3}`},
		{"matomo.annotations.add", `{"idSite":1,"date":"today","note":"a"}`},
		{"matomo.annotations.update", `{"idSite":1,"idNote":3,"note":"a"}`},
	} {
		var calls []call
		env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{}).handler(t))
		_, err := env.change(tool.name, "writer-two", tool.args)
		if err == nil || !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s: err = %v, want an invalid request that names no target", tool.name, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests and %d secret reads before the refusal", tool.name, len(calls), *env.reads)
		}
	}
}

func TestAnnotationErrorsMapToClassesWithoutTheMatomoText(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		class         provider.Class
		unsupported   bool
	}{
		{"permission", "The current user is not allowed to add notes for site #1.", provider.ClassPermission, false},
		{"unknown website", "An unexpected website was found in the request: website id was set to '1' .", provider.ClassNotFound, false},
		{"method missing", "The method 'Annotations.add' does not exist or is not available in the module 'Annotations'.", "", true},
		{"plugin inactive", "The plugin Annotations is not activated.", "", true},
		{"bad date", "Date format must be: YYYY-MM-DD", provider.ClassInvalidResponse, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
				"Annotations.add": func(call) *http.Response { return failure(tc.message + bodyCanary) },
			}).handler(t))
			_, err := env.change("matomo.annotations.add", "writer", `{"idSite":1,"date":"today","note":"a"}`)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
				classOf(err) != tc.class {
				t.Fatalf("err = %v (class %q), want class %q unsupported %t", err, classOf(err), tc.class, tc.unsupported)
			}
			if countMethod(calls, "Annotations.add") > 1 {
				t.Errorf("add was sent %d times", countMethod(calls, "Annotations.add"))
			}
		})
	}
}

func TestAnnotationUnclearChangeIsNotRepeated(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.add": func(call) *http.Response { return ok(`{"result":"success"}`) },
	}).handler(t))
	_, err := env.change("matomo.annotations.add", "writer", `{"idSite":1,"date":"today","note":"a"}`)
	if err == nil || !strings.Contains(err.Error(), "never repeated") || countMethod(calls, "Annotations.add") != 1 {
		t.Fatalf("err = %v, sent %d times", err, countMethod(calls, "Annotations.add"))
	}
	var calls2 []call
	env = newGoalsEnvironment(t, &calls2, annotationAnswers(answers{
		"Annotations.save": func(call) *http.Response { return respond(http.StatusBadGateway, "") },
	}).handler(t))
	_, err = env.change("matomo.annotations.update", "writer", `{"idSite":1,"idNote":3,"note":"a"}`)
	if err == nil || countMethod(calls2, "Annotations.save") != 1 {
		t.Fatalf("err = %v, sent %d times", err, countMethod(calls2, "Annotations.save"))
	}
}

func TestInactiveAnnotationsPluginRefusesToolsWithoutFurtherRequests(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) },
	}).handler(t))
	_, err := env.invoke("matomo.annotations.get", "open", `{"idSite":1,"idNote":3}`)
	if !isUnsupported(err) || countMethod(calls, "Annotations.get") != 0 {
		t.Fatalf("err = %v", err)
	}
}

const countFixture = `{"1":[["2026-01-01",{"count":2,"starred":1}],["2026-02-01",{"count":1,"starred":"0","note":"Only &lt;one&gt;"}],
["2026-03-01",{"count":0,"starred":0}]],"2":[["2026-01-01",{"count":9,"starred":9}]]}`

func TestAnnotationsDeleteTools(t *testing.T) {
	reg := registry(t)
	for id, effect := range map[string]capability.Effect{"matomo.annotations.delete": capability.EffectDelete,
		"matomo.annotations.delete-all": capability.EffectDelete} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != effect || d.Risk.Confirmation != capability.ConfirmationRequired ||
			!d.RequiresToolAllowList || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Fatalf("%s: found %t, %+v", id, found, d)
		}
	}
	all, _, _ := reg.Lookup("matomo.annotations.delete-all")
	for _, part := range []string{"every annotation", "Super User", "cannot be undone"} {
		if !strings.Contains(all.Description, part) {
			t.Errorf("delete-all description lacks %q", part)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := strings.Join(profile.Tools, " ")
		if strings.Contains(joined, "matomo.annotations.delete") {
			t.Errorf("profile %s selects a delete tool", profile.ID)
		}
		if (profile.ID == "read") != strings.Contains(joined, "matomo.annotations.count") && profile.ID != "annotations" &&
			profile.ID != "live" && profile.ID != "goals" && profile.ID != "segments" && profile.ID != "sites" {
			t.Errorf("profile %s: count placement", profile.ID)
		}
	}
}

func TestAnnotationsDeleteSendsOneRequestWithoutPriorRead(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.delete": func(c call) *http.Response {
			if c.form.Get("idNote") == "9" {
				return failure("There is no note with id '9' for site with id '1'.")
			}
			return ok(`{"result":"success","message":"ok"}`)
		},
	}).handler(t))
	result, err := env.change("matomo.annotations.delete", "admin", `{"idSite":1,"idNote":3}`)
	var out AnnotationDeleted
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Deleted || out.IDNote != 3 || out.IDSite != 1 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("idSite") != "1" || sent.form.Get("idNote") != "3" ||
		countMethod(calls, "Annotations.delete") != 1 || countMethod(calls, "Annotations.get") != 0 {
		t.Errorf("request = %v", sent.form)
	}
	if _, err := env.change("matomo.annotations.delete", "admin", `{"idSite":1,"idNote":9}`); classOf(err) != provider.ClassNotFound {
		t.Errorf("unknown note: err = %v", err)
	}
	if _, err := env.change("matomo.annotations.delete", "admin", `{"idSite":1}`); err == nil {
		t.Error("a missing idNote was accepted")
	}
	if _, err := env.change("matomo.annotations.delete", "admin", `{"idSite":2,"idNote":3}`); err == nil || !isInvalidRequest(err) {
		t.Errorf("foreign website: err = %v", err)
	}
}

func TestAnnotationsDeleteUnclearAndPermission(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.delete": func(call) *http.Response { return respond(http.StatusBadGateway, "") },
	}).handler(t))
	_, err := env.change("matomo.annotations.delete", "admin", `{"idSite":1,"idNote":3}`)
	if err == nil || countMethod(calls, "Annotations.delete") != 1 {
		t.Fatalf("err = %v, sent %d times", err, countMethod(calls, "Annotations.delete"))
	}
	var calls2 []call
	env = newGoalsEnvironment(t, &calls2, annotationAnswers(answers{
		"Annotations.delete": func(call) *http.Response {
			return failure("The current user is not allowed to modify or delete notes for site #1" + bodyCanary)
		},
		"Annotations.deleteAll": func(call) *http.Response { return ok(`[]`) },
	}).handler(t))
	_, err = env.change("matomo.annotations.delete", "admin", `{"idSite":1,"idNote":3}`)
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("permission: err = %v", err)
	}
	_, err = env.change("matomo.annotations.delete-all", "admin", `{"idSite":1}`)
	if err == nil || !strings.Contains(err.Error(), "never repeated") || countMethod(calls2, "Annotations.deleteAll") != 1 {
		t.Errorf("unclear answer: err = %v", err)
	}
}

func TestAnnotationsDeleteAllNeedsInstanceAndWebsite(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.deleteAll": func(c call) *http.Response {
			if c.form.Get("idSite") != "1" {
				t.Errorf("idSite = %q", c.form.Get("idSite"))
			}
			return ok(`{"result":"success"}`)
		},
	}).handler(t))
	result, err := env.change("matomo.annotations.delete-all", "admin", `{"idSite":1}`)
	var out AnnotationsDeleted
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Deleted || out.IDSite != 1 || countMethod(calls, "Annotations.deleteAll") != 1 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	before, readsBefore := len(calls), *env.reads
	if _, err := env.change("matomo.annotations.delete-all", "admin", `{"idSite":2}`); err == nil || !isInvalidRequest(err) ||
		strings.Contains(err.Error(), "site/") {
		t.Errorf("foreign website: err = %v", err)
	}
	if _, err := env.change("matomo.annotations.delete-all", "no-instance", `{"idSite":1}`); err == nil {
		t.Error("delete-all ran on a connection without the instance")
	}
	if len(calls) != before || *env.reads != readsBefore {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls)-before, *env.reads-readsBefore)
	}
	if ok, _ := reaches("matomo.annotations.delete-all", []string{"site/1"}); ok {
		t.Error("delete-all reaches a connection without the instance")
	}
	if ok, _ := reaches("matomo.annotations.delete-all", []string{"instance"}); ok {
		t.Error("delete-all reaches a connection without a website")
	}
	if ok, _ := reaches("matomo.annotations.delete-all", []string{"site", "instance"}); !ok {
		t.Error("delete-all does not reach a connection with site and instance")
	}
}

func TestAnnotationsCountReadsOneWebsite(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
		"Annotations.getAnnotationCountForDates": func(call) *http.Response { return ok(countFixture) },
	}).handler(t))
	result, err := env.invoke("matomo.annotations.count", "open",
		`{"idSite":1,"date":"2026-03-01","period":"month","lastN":3,"getAnnotationText":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var out AnnotationCountsResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || len(out.Counts) != 3 || out.Counts[0] != (AnnotationCount{Date: "2026-01-01", Count: 2, Starred: 1}) ||
		out.Counts[1].Note != "Only &lt;one&gt;" || out.Counts[1].Starred != 0 || out.Counts[2].Count != 0 {
		t.Fatalf("out = %s", result)
	}
	sent := calls[len(calls)-1].form
	if sent.Get("idSite") != "1" || sent.Get("period") != "month" || sent.Get("lastN") != "3" ||
		sent.Get("getAnnotationText") != "1" || sent.Get("date") != "2026-03-01" {
		t.Errorf("request = %v", sent)
	}
	for _, bad := range []string{`{"idSite":1,"period":"day"}`, `{"idSite":1,"date":"today"}`,
		`{"idSite":1,"date":"today","period":"year"x}`, `{"idSite":1,"date":"2026-01-01,2026-01-05","period":"day"}`,
		`{"idSite":1,"date":"today","period":"day","lastN":0}`, `{"idSite":"1,2","date":"today","period":"day"}`,
		`{"idSite":2,"date":"today","period":"day"}`} {
		before := len(calls)
		if _, err := env.invoke("matomo.annotations.count", "one", bad); err == nil || len(calls) != before {
			if err == nil && strings.Contains(bad, `"idSite":2`) {
				continue
			}
			t.Errorf("%s: err = %v", bad, err)
		}
	}
}

func TestAnnotationsCountRejectsMalformedAnswers(t *testing.T) {
	for _, body := range []string{`{"1":"x"}`, `{"1":[["2026-01-01"]]}`, `{"1":[["2026-01-01",{"count":"a"}]]}`} {
		var calls []call
		env := newGoalsEnvironment(t, &calls, annotationAnswers(answers{
			"Annotations.getAnnotationCountForDates": func(call) *http.Response { return ok(body) },
		}).handler(t))
		_, err := env.invoke("matomo.annotations.count", "open", `{"idSite":1,"date":"today","period":"day"}`)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v", body, err)
		}
	}
}
