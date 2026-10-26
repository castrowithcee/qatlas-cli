package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const batchTool = "matomo.tracking.record-batch"

// batchEntries holds one entry per type with the query parameters the single tool of that type sends.
var batchEntries = []struct {
	entry string
	want  map[string]string
}{
	{`{"type":"page-view","url":"https://example.org/a","pageTitle":"A","performance":{"network":1},"generationTime":7}`,
		map[string]string{"url": "https://example.org/a", "action_name": "A", "pf_net": "1", "gt_ms": "7"}},
	{`{"type":"event","url":"https://example.org/v","category":"Video","action":"Play","name":"N","value":2.5}`,
		map[string]string{"url": "https://example.org/v", "e_c": "Video", "e_a": "Play", "e_n": "N", "e_v": "2.5"}},
	{`{"type":"site-search","url":"https://example.org/s","keyword":"invoice","category":"c","resultCount":0}`,
		map[string]string{"url": "https://example.org/s", "search": "invoice", "search_cat": "c", "search_count": "0"}},
	{`{"type":"link","url":"https://example.org/d","kind":"outlink","target":"https://example.com/x"}`,
		map[string]string{"url": "https://example.org/d", "link": "https://example.com/x"}},
	{`{"type":"goal","url":"https://example.org/t","goalId":3,"revenue":19.9}`,
		map[string]string{"url": "https://example.org/t", "idgoal": "3", "revenue": "19.9"}},
	{`{"type":"order","url":"https://example.org/o","orderId":"o-1","revenue":10,"tax":2}`,
		map[string]string{"url": "https://example.org/o", "idgoal": "0", "ec_id": "o-1", "revenue": "10", "ec_tx": "2"}},
	{`{"type":"cart-update","url":"https://example.org/c","revenue":5,"items":[{"sku":"A","price":5}]}`,
		map[string]string{"url": "https://example.org/c", "idgoal": "0", "revenue": "5", "ec_items": `[["A","","",5,1]]`}},
	{`{"type":"content-impression","url":"https://example.org/","contentName":"banner","contentPiece":"p"}`,
		map[string]string{"url": "https://example.org/", "c_n": "banner", "c_p": "p"}},
	{`{"type":"content-interaction","url":"https://example.org/","interaction":"click","contentName":"banner","idSite":1,` +
		`"visitorId":"0123456789abcdef","dimensions":{"2":"gold"}}`,
		map[string]string{"url": "https://example.org/", "c_i": "click", "c_n": "banner", "_id": "0123456789abcdef", "dimension2": "gold"}},
}

func batchArguments(site string, entries ...string) string {
	return `{` + site + `"entries":[` + strings.Join(entries, ",") + `]}`
}

func batchAnswerOf(tracked, invalid int) string {
	return `{"status":"success","tracked":` + strconv.Itoa(tracked) + `,"invalid":` + strconv.Itoa(invalid) + `}`
}

func batchAnswering(status int, body string) func(call) (*http.Response, error) {
	return func(call) (*http.Response, error) { return respond(status, body), nil }
}

func TestTrackingBatchIsListedLikeTheOtherTrackingToolsAndInNoProfile(t *testing.T) {
	reg := registry(t)
	d, _, found := reg.Lookup(batchTool)
	if !found || d.Risk != trackingRisk() || !d.RequiresToolAllowList {
		t.Fatalf("found %t, %+v", found, d)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == batchTool {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
	for _, text := range []string{"Not idempotent", "does not confirm", "never retries", "merge or take over", "no token",
		"skips an invalid entry", "not which of them", "cannot be undone"} {
		if !strings.Contains(d.Description, strings.Replace(text, "cannot be undone", "no API call can undo", 1)) {
			t.Errorf("description lacks %q", text)
		}
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(d.InputSchema, &schema); err != nil || len(schema.Properties) != 2 {
		t.Fatalf("schema = %s, err = %v", d.InputSchema, err)
	}
	var items struct {
		Items struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"items"`
		MaxItems int `json:"maxItems"`
	}
	if err := json.Unmarshal(schema.Properties["entries"], &items); err != nil || items.MaxItems != 100 {
		t.Fatalf("entries = %s, err = %v", schema.Properties["entries"], err)
	}
	for _, name := range []string{"clientIp", "timestamp", "country", "region", "city", "latitude", "longitude", "cip", "cdt",
		"cdo", "lat", "long", "token_auth", "token", "bots", "recMode", "httpStatus"} {
		if _, found := items.Items.Properties[name]; found {
			t.Errorf("an entry offers %s", name)
		}
	}
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	if _, err := env.change(batchTool, "trk-effects", batchArguments("", batchEntries[0].entry)); err == nil {
		t.Error("an effects-only connection offered the tool")
	}
	if len(calls) != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestTrackingBatchSendsOneConfirmedJSONBodyWithoutTokenOrSecretAccess(t *testing.T) {
	random := regexp.MustCompile(`^[0-9]+$`)
	var calls []call
	env := newTrackingEnvironment(t, &calls, batchAnswering(http.StatusOK, batchAnswerOf(9, 0)))
	entries := make([]string, len(batchEntries))
	for i, e := range batchEntries {
		entries[i] = e.entry
	}
	arguments := batchArguments(`"idSite":1,`, entries...)
	if _, err := env.invoke(batchTool, "trk", arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("an unconfirmed call caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	result, err := env.change(batchTool, "trk", arguments)
	if err != nil || len(calls) != 1 || *env.reads != 0 {
		t.Fatalf("err = %v, %d requests, %d secret reads", err, len(calls), *env.reads)
	}
	c := calls[0]
	if c.method != http.MethodPost || c.path != "/matomo/matomo.php" || c.rawQuery != "" || c.auth != "" ||
		c.contentType != "application/json" || !strings.HasPrefix(c.body, `{"requests":["?`) {
		t.Fatalf("request = %+v", c)
	}
	var body map[string][]string
	if err := json.Unmarshal([]byte(c.body), &body); err != nil || len(body) != 1 || len(body["requests"]) != len(batchEntries) {
		t.Fatalf("body = %s, err = %v", c.body, err)
	}
	for i, query := range body["requests"] {
		params, err := url.ParseQuery(strings.TrimPrefix(query, "?"))
		if err != nil || !strings.HasPrefix(query, "?") {
			t.Fatalf("entry %d = %q, err = %v", i, query, err)
		}
		want := map[string]string{"idsite": "1", "rec": "1", "apiv": "1", "send_image": "0", "rand": params.Get("rand")}
		for name, value := range batchEntries[i].want {
			want[name] = value
		}
		if len(params) != len(want) || !random.MatchString(params.Get("rand")) {
			t.Errorf("entry %d = %v, want %v", i, params, want)
		}
		for name, value := range want {
			if params.Get(name) != value {
				t.Errorf("entry %d: %s = %q, want %q", i, name, params.Get(name), value)
			}
		}
	}
	for _, name := range []string{"token_auth", "cip", "cdt", "country", "lat", "bots", "type"} {
		if strings.Contains(c.body, name+"=") || strings.Contains(c.body, `"`+name+`"`) {
			t.Errorf("the body carries %s", name)
		}
	}
	if strings.Contains(c.body, tokenValue) || strings.Contains(c.path+c.rawQuery, tokenValue) {
		t.Error("the token leaked")
	}
	var answer TrackingBatchSent
	if err := json.Unmarshal([]byte(result), &answer); err != nil || answer.IDSite != 1 || !answer.Sent || answer.ProcessingConfirmed ||
		answer.Entries != 9 || answer.Tracked != 9 || answer.Invalid != 0 || !strings.Contains(answer.Message, "does not confirm") {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestTrackingBatchReportsPartialSuccess(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, batchAnswering(http.StatusOK,
		`{"status":"success","tracked":1,"invalid":1,"invalid_indices":[1]}`))
	result, err := env.change(batchTool, "trk", batchArguments(`"idSite":1,`, batchEntries[0].entry, batchEntries[1].entry, batchEntries[2].entry))
	var answer TrackingBatchSent
	if err != nil || json.Unmarshal([]byte(result), &answer) != nil || len(calls) != 1 {
		t.Fatalf("err = %v, result = %s, calls = %d", err, result, len(calls))
	}
	if !answer.Sent || answer.Entries != 3 || answer.Tracked != 1 || answer.Invalid != 1 ||
		!strings.Contains(answer.Message, "1 entries were neither tracked nor rejected") {
		t.Errorf("answer = %+v", answer)
	}
}

func TestTrackingBatchUsesTheOnlyBoundWebsite(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, batchAnswering(http.StatusOK, batchAnswerOf(1, 0)))
	if _, err := env.change(batchTool, "trk-two", batchArguments("", batchEntries[0].entry)); err != nil || len(calls) != 1 ||
		!strings.Contains(calls[0].body, "idsite=2") {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestTrackingBatchRefusesBadCallsBeforeAnyRequestWithoutQuotingValues(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, batchAnswering(http.StatusOK, batchAnswerOf(1, 0)))
	hundredOne := make([]string, maxBatchEntries+1)
	for i := range hundredOne {
		hundredOne[i] = batchEntries[0].entry
	}
	wide := `https://example.org/` + strings.Repeat("?", 2000)
	heavy := make([]string, maxBatchEntries)
	for i := range heavy {
		heavy[i] = `{"type":"page-view","url":"` + wide + `","referrer":"` + wide + `","userAgent":"` + strings.Repeat("?", 500) + `"}`
	}
	const page = `"type":"page-view","url":"https://example.org/"`
	for name, tc := range map[string]struct{ arguments, connection, absent string }{
		"101 entries":         {batchArguments(`"idSite":1,`, hundredOne...), "trk", ""},
		"no entries":          {batchArguments(`"idSite":1,`), "trk", ""},
		"too large a body":    {batchArguments(`"idSite":1,`, heavy...), "trk", ""},
		"another site":        {batchArguments(`"idSite":1,`, `{`+page+`,"idSite":4242}`), "trk", "4242"},
		"foreign target":      {batchArguments(`"idSite":1,`, `{`+page+`}`), "trk-two", ""},
		"clientIp":            {batchArguments(`"idSite":1,`, `{`+page+`,"clientIp":"203.0.113.9"}`), "trk", "203.0.113.9"},
		"timestamp":           {batchArguments(`"idSite":1,`, `{`+page+`,"timestamp":1700000000}`), "trk", ""},
		"country":             {batchArguments(`"idSite":1,`, `{`+page+`,"country":"de"}`), "trk", ""},
		"latitude":            {batchArguments(`"idSite":1,`, `{`+page+`,"latitude":1.5}`), "trk", ""},
		"token_auth":          {batchArguments(`"idSite":1,`, `{`+page+`,"token_auth":"secret0value"}`), "trk", "secret0value"},
		"cdo":                 {batchArguments(`"idSite":1,`, `{`+page+`,"cdo":5}`), "trk", ""},
		"bot type":            {batchArguments(`"idSite":1,`, `{"type":"bot-request","url":"https://example.org/","userAgent":"x"}`), "trk", "bot-request"},
		"no type":             {batchArguments(`"idSite":1,`, `{"url":"https://example.org/"}`), "trk", ""},
		"argument of another": {batchArguments(`"idSite":1,`, `{`+page+`,"category":"C"}`), "trk", ""},
		"blank category":      {batchArguments(`"idSite":1,`, `{"type":"event","url":"https://example.org/","category":"  ","action":"A"}`), "trk", ""},
		"script url":          {batchArguments(`"idSite":1,`, `{"type":"page-view","url":"javascript:alert(1)"}`), "trk", "alert"},
		"missing action":      {batchArguments(`"idSite":1,`, `{"type":"event","url":"https://example.org/","category":"C"}`), "trk", ""},
		"partial time":        {batchArguments(`"idSite":1,`, `{`+page+`,"hour":3}`), "trk", ""},
		"missing items":       {batchArguments(`"idSite":1,`, `{"type":"cart-update","url":"https://example.org/","revenue":1}`), "trk", ""},
		"second entry fails":  {batchArguments(`"idSite":1,`, batchEntries[0].entry, `{"type":"goal","url":"https://example.org/","goalId":0}`), "trk", ""},
	} {
		_, err := env.change(batchTool, tc.connection, tc.arguments)
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		if tc.absent != "" && strings.Contains(err.Error(), tc.absent) {
			t.Errorf("%s: the error quotes %q: %v", name, tc.absent, err)
		}
		if name == "foreign target" && (!isInvalidRequest(err) || err.Error() != outsideTargets) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
	if _, err := env.change(batchTool, "trk", batchArguments(`"idSite":1,`, `{`+page+`,"idSite":1}`)); err != nil || len(calls) != 1 {
		t.Errorf("an entry that repeats idSite: err = %v, calls = %d", err, len(calls))
	}
}

func TestTrackingBatchTakesAnImplausibleAnswerForUnreadable(t *testing.T) {
	for name, body := range map[string]string{
		"more tracked than sent": batchAnswerOf(3, 0),
		"more invalid than sent": batchAnswerOf(0, 3),
		"sum above sent":         batchAnswerOf(2, 1),
		"negative":               `{"status":"success","tracked":-1,"invalid":0}`,
		"fraction":               `{"status":"success","tracked":0.5,"invalid":0}`,
		"text":                   `{"status":"success","tracked":"1","invalid":0}`,
		"missing invalid":        `{"status":"success","tracked":1}`,
		"missing tracked":        `{"status":"success","invalid":0}`,
		"huge":                   `{"status":"success","tracked":99999999999999999999,"invalid":0}`,
		"other status":           `{"status":"queued","tracked":1,"invalid":0}`,
		"html":                   "<html>" + bodyCanary + "</html>",
		"empty":                  "",
		"gif":                    gifAnswer,
		"oversize":               `{"status":"success","tracked":1,"invalid":0,"pad":"` + strings.Repeat("a", maxBatchAnswer) + `"}`,
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, batchAnswering(http.StatusOK, body))
		_, err := env.change(batchTool, "trk", batchArguments(`"idSite":1,`, batchEntries[0].entry, batchEntries[1].entry))
		if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), bodyCanary) ||
			!strings.Contains(err.Error(), "never repeated") || len(calls) != 1 {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(calls))
		}
	}
}

func TestTrackingBatchMapsFailuresAndNeverRetries(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		body    string
		class   provider.Class
		invalid bool
		unclear bool
		text    string
	}{
		"204":             {http.StatusNoContent, "", provider.ClassInvalidResponse, false, true, ""},
		"400 page":        {http.StatusBadRequest, "<html>" + bodyCanary + "</html>", "", true, false, "BulkTracking"},
		"400 error body":  {http.StatusBadRequest, bodyCanary, "", true, false, "nothing was recorded"},
		"429":             {http.StatusTooManyRequests, "", provider.ClassRateLimited, false, false, ""},
		"503":             {http.StatusServiceUnavailable, bodyCanary, provider.ClassUnreachable, false, false, ""},
		"504":             {http.StatusGatewayTimeout, "", provider.ClassTimeout, false, true, ""},
		"500":             {http.StatusInternalServerError, bodyCanary, provider.ClassProviderError, false, true, ""},
		"500 abort":       {http.StatusInternalServerError, `{"status":"error","tracked":1,"invalid":0}`, provider.ClassProviderError, false, true, "aborted"},
		"500 abort debug": {http.StatusInternalServerError, `{"status":"error","tracked":0,"invalid":1,"message":"` + bodyCanary + `"}`, provider.ClassProviderError, false, true, "aborted"},
		"500 implausible": {http.StatusInternalServerError, `{"status":"error","tracked":7,"invalid":0}`, provider.ClassProviderError, false, true, "HTTP 500"},
		"401":             {http.StatusUnauthorized, "", provider.ClassAuth, false, false, ""},
		"403":             {http.StatusForbidden, "", provider.ClassPermission, false, false, ""},
		"404":             {http.StatusNotFound, "", provider.ClassNotFound, false, false, ""},
		"redirect":        {http.StatusFound, "", provider.ClassProviderError, false, true, ""},
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, batchAnswering(tc.status, tc.body))
		_, err := env.change(batchTool, "trk", batchArguments(`"idSite":1,`, batchEntries[0].entry, batchEntries[1].entry))
		if len(calls) != 1 || err == nil {
			t.Errorf("%s: %d requests, err = %v", name, len(calls), err)
			continue
		}
		if strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), tokenValue) {
			t.Errorf("%s: the error leaks provider text: %v", name, err)
		}
		switch {
		case tc.invalid:
			if !isInvalidRequest(err) {
				t.Errorf("%s: err = %v", name, err)
			}
		case classOf(err) != tc.class:
			t.Errorf("%s: err = %v, want %q", name, err, tc.class)
		}
		if tc.unclear != strings.Contains(err.Error(), "never repeated") {
			t.Errorf("%s: unclear outcome marked wrongly: %v", name, err)
		}
		if tc.text != "" && !strings.Contains(err.Error(), tc.text) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.text)
		}
	}
}

func TestTrackingBatchAbortReportsTheCountsAndKeepsRecordedEntriesInMind(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, batchAnswering(http.StatusInternalServerError, `{"status":"error","tracked":1,"invalid":1}`))
	_, err := env.change(batchTool, "trk", batchArguments(`"idSite":1,`, batchEntries[0].entry, batchEntries[1].entry, batchEntries[2].entry))
	if err == nil || !strings.Contains(err.Error(), "tracked 1 and rejected 1 of 3 entries") ||
		!strings.Contains(err.Error(), "stay recorded unless") || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
}

func TestTrackingBatchTimeoutAndResetAreUnclearAndNotRepeated(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, func(call) (*http.Response, error) { return nil, context.DeadlineExceeded })
	_, err := env.change(batchTool, "trk", batchArguments(`"idSite":1,`, batchEntries[0].entry))
	if classOf(err) != provider.ClassTimeout || !strings.Contains(err.Error(), "counts the entries Matomo already recorded twice") ||
		len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
}

func TestTrackingBatchSenderRefusesForbiddenParametersInEveryEntry(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, batchAnswering(http.StatusOK, batchAnswerOf(2, 0)))
	sender, err := openTracking(mustResolve(t, env, "open"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range neverSentParameters {
		_, err := sender.sendBatch(context.Background(), "test", 1,
			[]url.Values{{"url": {"https://example.org/"}}, {name: {"x"}}})
		if err == nil {
			t.Errorf("parameter %s was sent", name)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
}

func TestTrackingBatchDescriptorKeepsTheConformanceShape(t *testing.T) {
	d := trackingRecordBatch
	if d.Risk.Effect != capability.EffectCreate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		d.Risk.Confirmation != capability.ConfirmationRequired {
		t.Errorf("risk = %+v", d.Risk)
	}
	for _, entry := range batchKinds {
		args, shared, extend := entry()
		if args == nil || shared == nil || extend == nil {
			t.Error("an entry builder returns nothing")
		}
	}
	if len(batchKinds) != 9 {
		t.Errorf("entry types = %d", len(batchKinds))
	}
}
