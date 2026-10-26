package matomo

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The tracker answers an empty 204 or, for an image request, a 43 byte GIF; every other answer is unclear.

const trackingConnections = `
  trk: {service: matomo, credential: token, tools: [matomo.tracking.record-page-view, matomo.tracking.record-event, matomo.tracking.record-site-search, matomo.tracking.record-link], targets: [site]}
  trk-two: {service: matomo, credential: token, tools: [matomo.tracking.record-page-view, matomo.tracking.record-event, matomo.tracking.record-site-search, matomo.tracking.record-link], targets: [site/2]}
  trk-effects: {service: matomo, credential: token, effects: [read, create], targets: [site]}
`

const gifAnswer = "GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x01D\x00;"

func noContent(call) (*http.Response, error) { return respond(http.StatusNoContent, ""), nil }

func newTrackingEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	t.Cleanup(limiters.Replace("tracking\x00"+baseURL,
		ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })))
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(trackingConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

var trackingTools = []struct{ id, arguments string }{
	{"matomo.tracking.record-page-view", `{"idSite":1,"url":"https://example.org/pricing","pageTitle":"Pricing"}`},
	{"matomo.tracking.record-event", `{"idSite":1,"url":"https://example.org/video","category":"Video","action":"Play"}`},
	{"matomo.tracking.record-site-search", `{"idSite":1,"url":"https://example.org/search","keyword":"invoice"}`},
	{"matomo.tracking.record-link", `{"idSite":1,"url":"https://example.org/docs","kind":"download","target":"https://example.org/guide.pdf"}`},
}

func TestTrackingToolsAreListedOnlyNonIdempotentConfirmedAndInNoProfile(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, tool := range trackingTools {
		d, _, found := reg.Lookup(tool.id)
		if !found || d.Risk.Effect != capability.EffectCreate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" ||
			!d.RequiresToolAllowList {
			t.Fatalf("%s: found %t, %+v", tool.id, found, d)
		}
		for _, text := range []string{"Not idempotent", "does not confirm", "never retries", "merge or take over"} {
			if !strings.Contains(d.Description, text) {
				t.Errorf("%s: description lacks %q", tool.id, text)
			}
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == tool.id {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
		for _, name := range []string{"cip", "cdt", "cdo", "country", "region", "city", "lat", "long", "token_auth", "token"} {
			if strings.Contains(string(d.InputSchema), `"`+name+`"`) {
				t.Errorf("%s offers %s", tool.id, name)
			}
		}
	}
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	for _, tool := range trackingTools {
		if _, err := env.change(tool.id, "trk-effects", tool.arguments); err == nil {
			t.Errorf("%s was offered by an effects-only connection", tool.id)
		}
	}
	if len(calls) != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}

func TestTrackingSendsOneConfirmedFormPostWithoutTokenOrSecretAccess(t *testing.T) {
	random := regexp.MustCompile(`^[0-9]+$`)
	for _, tool := range trackingTools {
		var calls []call
		env := newTrackingEnvironment(t, &calls, noContent)
		if _, err := env.invoke(tool.id, "trk", tool.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: err = %v", tool.id, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: unconfirmed call caused %d requests and %d secret reads", tool.id, len(calls), *env.reads)
		}
		result, err := env.change(tool.id, "trk", tool.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		if len(calls) != 1 || *env.reads != 0 {
			t.Fatalf("%s: %d requests, %d secret reads", tool.id, len(calls), *env.reads)
		}
		c := calls[0]
		if c.method != http.MethodPost || c.path != "/matomo/matomo.php" || c.rawQuery != "" || c.auth != "" ||
			c.contentType != "application/x-www-form-urlencoded" {
			t.Errorf("%s: request = %+v", tool.id, c)
		}
		for name, want := range map[string]string{"rec": "1", "apiv": "1", "send_image": "0", "idsite": "1",
			"url": c.form.Get("url")} {
			if c.form.Get(name) != want || want == "" {
				t.Errorf("%s: %s = %q, want %q", tool.id, name, c.form.Get(name), want)
			}
		}
		if !random.MatchString(c.form.Get("rand")) {
			t.Errorf("%s: rand = %q", tool.id, c.form.Get("rand"))
		}
		for _, name := range []string{"token_auth", "module", "method", "format", "cip", "cdt", "country", "lat"} {
			if c.form.Has(name) {
				t.Errorf("%s sent %s", tool.id, name)
			}
		}
		if strings.Contains(c.path+c.rawQuery+c.form.Encode(), tokenValue) {
			t.Errorf("%s leaked the token", tool.id)
		}
		if !strings.Contains(result, `"sent":true`) || !strings.Contains(result, `"processingConfirmed":false`) ||
			!strings.Contains(result, "does not confirm") {
			t.Errorf("%s: result = %s", tool.id, result)
		}
	}
}

func TestTrackingSendsEveryParameterInTheBodyOnly(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	_, err := env.change("matomo.tracking.record-page-view", "trk", `{"idSite":3,"url":"https://example.org/a?x=1","referrer":"https://example.com/",`+
		`"pageTitle":"A","pageViewId":"abc123","visitorId":"0123456789abcdef","userId":"user-1","overrideVisitorId":"fedcba9876543210",`+
		`"newVisit":true,"language":"de-DE","screenResolution":"1920x1080","userAgent":"Example/1.0","hour":13,"minute":5,"second":9,`+
		`"campaignName":"spring","campaignKeyword":"shoes","visitCount":4,"firstVisitTime":1700000000,"previousVisitTime":1700001000,`+
		`"dimensions":{"2":"gold","10":"b"},"performance":{"network":1,"server":2,"transfer":3,"domProcessing":4,"domCompletion":5,"onLoad":6},`+
		`"generationTime":7}`)
	if err != nil || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
	want := map[string]string{"idsite": "3", "url": "https://example.org/a?x=1", "urlref": "https://example.com/", "action_name": "A",
		"pv_id": "abc123", "_id": "0123456789abcdef", "uid": "user-1", "cid": "fedcba9876543210", "new_visit": "1", "lang": "de-DE",
		"res": "1920x1080", "ua": "Example/1.0", "h": "13", "m": "5", "s": "9", "_rcn": "spring", "_rck": "shoes", "_idvc": "4",
		"_idts": "1700000000", "_viewts": "1700001000", "dimension2": "gold", "dimension10": "b", "pf_net": "1", "pf_srv": "2",
		"pf_tfr": "3", "pf_dm1": "4", "pf_dm2": "5", "pf_onl": "6", "gt_ms": "7"}
	for name, value := range want {
		if calls[0].form.Get(name) != value {
			t.Errorf("%s = %q, want %q", name, calls[0].form.Get(name), value)
		}
	}
	if calls[0].rawQuery != "" || strings.Contains(calls[0].path, "example") {
		t.Errorf("parameters in the URL: %+v", calls[0])
	}
}

func TestTrackingEventSearchAndLinkParameters(t *testing.T) {
	for _, tc := range []struct {
		id, arguments string
		want, absent  map[string]string
	}{
		{"matomo.tracking.record-event", `{"idSite":1,"url":"https://example.org/","category":"C","action":"A","name":"N","value":2.5}`,
			map[string]string{"e_c": "C", "e_a": "A", "e_n": "N", "e_v": "2.5"}, nil},
		{"matomo.tracking.record-site-search", `{"idSite":1,"url":"https://example.org/","keyword":"k","category":"c","resultCount":0}`,
			map[string]string{"search": "k", "search_cat": "c", "search_count": "0"}, nil},
		{"matomo.tracking.record-link", `{"idSite":1,"url":"https://example.org/","kind":"download","target":"https://example.org/f.zip"}`,
			map[string]string{"download": "https://example.org/f.zip"}, map[string]string{"link": ""}},
		{"matomo.tracking.record-link", `{"idSite":1,"url":"https://example.org/","kind":"outlink","target":"https://example.com/x"}`,
			map[string]string{"link": "https://example.com/x"}, map[string]string{"download": ""}},
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, noContent)
		if _, err := env.change(tc.id, "trk", tc.arguments); err != nil || len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %d", tc.id, err, len(calls))
		}
		for name, value := range tc.want {
			if calls[0].form.Get(name) != value {
				t.Errorf("%s: %s = %q, want %q", tc.id, name, calls[0].form.Get(name), value)
			}
		}
		for name := range tc.absent {
			if calls[0].form.Has(name) {
				t.Errorf("%s sent %s", tc.id, name)
			}
		}
	}
}

func TestTrackingAcceptsOnlyAnEmpty204OrAGIFAndNeverRetries(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		body    string
		class   provider.Class
		clean   bool
		invalid bool
	}{
		"204":          {http.StatusNoContent, "", "", true, false},
		"gif":          {http.StatusOK, gifAnswer, "", true, false},
		"unknown site": {http.StatusBadRequest, bodyCanary, "", true, true},
		"500":          {http.StatusInternalServerError, bodyCanary, provider.ClassProviderError, false, false},
		"503":          {http.StatusServiceUnavailable, bodyCanary, provider.ClassUnreachable, true, false},
		"504":          {http.StatusGatewayTimeout, "", provider.ClassTimeout, false, false},
		"429":          {http.StatusTooManyRequests, "", provider.ClassRateLimited, true, false},
		"html":         {http.StatusOK, "<html>" + bodyCanary + "</html>", provider.ClassInvalidResponse, false, false},
		"json":         {http.StatusOK, `{"status":"success"}`, provider.ClassInvalidResponse, false, false},
		"empty 200":    {http.StatusOK, "", provider.ClassInvalidResponse, false, false},
		"202":          {http.StatusAccepted, "", provider.ClassProviderError, false, false},
		"redirect":     {http.StatusFound, "", provider.ClassProviderError, true, false},
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, func(call) (*http.Response, error) { return respond(tc.status, tc.body), nil })
		_, err := env.change("matomo.tracking.record-event", "trk", trackingTools[1].arguments)
		if len(calls) != 1 {
			t.Errorf("%s: %d requests, want no retry", name, len(calls))
		}
		switch {
		case tc.invalid:
			if !isInvalidRequest(err) {
				t.Errorf("%s: err = %v", name, err)
			}
		case tc.class == "":
			if err != nil {
				t.Errorf("%s: err = %v", name, err)
			}
		default:
			if classOf(err) != tc.class || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s: err = %v, want %q", name, err, tc.class)
			}
			if !tc.clean && !strings.Contains(err.Error(), "never repeated") && tc.status != http.StatusAccepted {
				t.Errorf("%s: unclear outcome not marked: %v", name, err)
			}
		}
	}
}

func TestTrackingTimeoutIsUnclearAndNotRepeated(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, func(call) (*http.Response, error) { return nil, context.DeadlineExceeded })
	_, err := env.change("matomo.tracking.record-page-view", "trk", trackingTools[0].arguments)
	if classOf(err) != provider.ClassTimeout || !strings.Contains(err.Error(), "never repeated") || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
}

func TestTrackingRefusesAnotherWebsiteBeforeAnyIOWithoutQuotingIt(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	for _, tool := range trackingTools {
		_, err := env.change(tool.id, "trk-two", tool.arguments)
		if !isInvalidRequest(err) || err.Error() != outsideTargets {
			t.Errorf("%s: err = %v", tool.id, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
}

func TestTrackingRefusesInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	for name, arguments := range map[string]string{
		"missing url":       `{"idSite":1,"category":"C","action":"A"}`,
		"script url":        `{"idSite":1,"url":"javascript:alert(1)","category":"C","action":"A"}`,
		"blank category":    `{"idSite":1,"url":"https://example.org/","category":"  ","action":"A"}`,
		"bad visitor id":    `{"idSite":1,"url":"https://example.org/","category":"C","action":"A","visitorId":"xyz"}`,
		"partial time":      `{"idSite":1,"url":"https://example.org/","category":"C","action":"A","hour":3}`,
		"token_auth":        `{"idSite":1,"url":"https://example.org/","category":"C","action":"A","token_auth":"x"}`,
		"cip":               `{"idSite":1,"url":"https://example.org/","category":"C","action":"A","cip":"203.0.113.1"}`,
		"country":           `{"idSite":1,"url":"https://example.org/","category":"C","action":"A","country":"de"}`,
		"nul in text":       `{"idSite":1,"url":"https://example.org/","category":"C\u0000","action":"A"}`,
		"dimension key":     `{"idSite":1,"url":"https://example.org/","category":"C","action":"A","dimensions":{"x":"y"}}`,
		"too long category": `{"idSite":1,"url":"https://example.org/","category":"` + strings.Repeat("a", 513) + `","action":"A"}`,
	} {
		if _, err := env.change("matomo.tracking.record-event", "trk", arguments); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := env.change("matomo.tracking.record-link", "trk",
		`{"idSite":1,"url":"https://example.org/","kind":"other","target":"https://example.org/f"}`); err == nil {
		t.Error("an unknown kind was accepted")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
}

func TestTrackingSenderNeverSendsForbiddenParameters(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	sender, err := openTracking(mustResolve(t, env, "open"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range neverSentParameters {
		if err := sender.send(context.Background(), "test", 1, url.Values{name: {"x"}}); err == nil {
			t.Errorf("parameter %s was sent", name)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
}
