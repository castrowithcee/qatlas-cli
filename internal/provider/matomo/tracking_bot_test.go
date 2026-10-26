package matomo

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const botTool = "matomo.tracking.record-bot-request"

const botArguments = `{"idSite":1,"userAgent":"Mozilla/5.0 (compatible; ChatGPT-User/1.0)","url":"https://example.org/docs",` +
	`"httpStatus":200,"bytes":1832,"responseTime":42,"source":"nginx"}`

// botHandler answers the reporting API of an instance and the tracker.
func botHandler(release string, plugin bool, tracker func(call) (*http.Response, error)) func(call) (*http.Response, error) {
	active := `{"value":false}`
	if plugin {
		active = `{"value":true}`
	}
	return func(c call) (*http.Response, error) {
		switch {
		case c.path == "/matomo/matomo.php":
			return tracker(c)
		case c.api() == "API.getMatomoVersion":
			return ok(`{"value":"` + release + `"}`), nil
		case c.api() == "API.isPluginActivated" && c.form.Get("pluginName") == botPlugin:
			return ok(active), nil
		}
		return failure("unexpected"), nil
	}
}

func trackerCalls(calls []call) []call {
	var out []call
	for _, c := range calls {
		if c.path == "/matomo/matomo.php" {
			out = append(out, c)
		}
	}
	return out
}

func TestBotRequestSendsExactlyTheBotFormAndNeverBots(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true, noContent))
	if _, err := env.invoke(botTool, "trk", botArguments); err == nil || !strings.Contains(err.Error(), "confirm") ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("unconfirmed: err = %v, calls = %d, reads = %d", err, len(calls), *env.reads)
	}
	result, err := env.change(botTool, "trk", botArguments)
	if err != nil {
		t.Fatal(err)
	}
	sent := trackerCalls(calls)
	if len(sent) != 1 {
		t.Fatalf("tracking requests = %d", len(sent))
	}
	c := sent[0]
	if c.rawQuery != "" || c.auth != "" || c.method != http.MethodPost {
		t.Errorf("request = %+v", c)
	}
	want := url.Values{"recMode": {"1"}, "ua": {"Mozilla/5.0 (compatible; ChatGPT-User/1.0)"}, "url": {"https://example.org/docs"},
		"http_status": {"200"}, "bw_bytes": {"1832"}, "pf_srv": {"42"}, "source": {"nginx"},
		"idsite": {"1"}, "rec": {"1"}, "apiv": {"1"}, "send_image": {"0"}, "rand": {c.form.Get("rand")}}
	if c.form.Encode() != want.Encode() || c.form.Get("rand") == "" {
		t.Errorf("form = %v, want %v", c.form, want)
	}
	if c.form.Has("bots") || c.form.Has("token_auth") || c.form.Has("cdt") {
		t.Errorf("form = %v", c.form)
	}
	if !strings.Contains(result, `"sent":true`) || !strings.Contains(result, `"processingConfirmed":false`) ||
		!strings.Contains(result, "list of AI chatbots") {
		t.Errorf("result = %s", result)
	}
	// The instance check used the bearer header against the reporting API only.
	for _, other := range calls {
		if other.path == "/matomo/matomo.php" {
			continue
		}
		if other.path != "/matomo/index.php" || other.auth != "Bearer "+tokenValue || other.form.Has("token_auth") {
			t.Errorf("instance call = %+v", other)
		}
	}
	if methods(calls[:len(calls)-1]) != "API.getMatomoVersion API.isPluginActivated" {
		t.Errorf("calls = %s", methods(calls))
	}
}

func TestBotRequestDownloadReplacesUrlAndTheOtherFieldsStayOptional(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true, noContent))
	if _, err := env.change(botTool, "trk", `{"idSite":1,"userAgent":"Claude-User","download":"https://example.org/a.pdf"}`); err != nil {
		t.Fatal(err)
	}
	c := trackerCalls(calls)[0]
	if c.form.Get("download") != "https://example.org/a.pdf" || c.form.Has("url") || c.form.Get("recMode") != "1" {
		t.Errorf("form = %v", c.form)
	}
	for _, name := range []string{"http_status", "bw_bytes", "pf_srv", "source", "bots"} {
		if c.form.Has(name) {
			t.Errorf("sent %s", name)
		}
	}
}

func TestBotRequestTimestampSendsTheTokenInTheBodyOnly(t *testing.T) {
	withClock(t, overrideNow)
	stamp := overrideNow.Unix() - 3*86400
	var calls []call
	env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true, noContent))
	if _, err := env.change(botTool, "trk", withOverride(botArguments, `"timestamp":`+seconds(stamp))); err != nil {
		t.Fatal(err)
	}
	c := trackerCalls(calls)[0]
	if c.form.Get("token_auth") != tokenValue || c.form.Get("cdt") != seconds(stamp) || c.auth != "" || c.rawQuery != "" ||
		strings.Contains(c.path, tokenValue) {
		t.Errorf("request = %+v", c)
	}
	if *env.reads != 1 {
		t.Errorf("the token was resolved %d times", *env.reads)
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("token not masked: %q", got)
	}
	// Without a timestamp the tracking request holds no token.
	calls = nil
	if _, err := env.change(botTool, "trk", botArguments); err != nil {
		t.Fatal(err)
	}
	if c := trackerCalls(calls)[0]; c.form.Has("token_auth") || c.auth != "" {
		t.Errorf("request = %+v", c)
	}
}

func TestBotRequestRefusesInvalidArgumentsBeforeAnySecretAccessOrRequest(t *testing.T) {
	withClock(t, overrideNow)
	for name, arguments := range map[string]string{
		"no user agent":    `{"idSite":1,"url":"https://example.org/"}`,
		"blank user agent": `{"idSite":1,"userAgent":"  ","url":"https://example.org/"}`,
		"long user agent":  `{"idSite":1,"userAgent":"` + strings.Repeat("a", 513) + `","url":"https://example.org/"}`,
		"neither address":  `{"idSite":1,"userAgent":"Claude-User"}`,
		"both addresses":   `{"idSite":1,"userAgent":"Claude-User","url":"https://example.org/","download":"https://example.org/a"}`,
		"script address":   `{"idSite":1,"userAgent":"Claude-User","url":"javascript:alert(1)"}`,
		"status high":      withOverride(botArguments, `"httpStatus":65536`),
		"status negative":  withOverride(botArguments, `"httpStatus":-1`),
		"bytes high":       withOverride(botArguments, `"bytes":4294967296`),
		"bytes negative":   withOverride(botArguments, `"bytes":-1`),
		"time high":        withOverride(botArguments, `"responseTime":4294967296`),
		"time negative":    withOverride(botArguments, `"responseTime":-1`),
		"source long":      withOverride(botArguments, `"source":"`+strings.Repeat("s", 51)+`"`),
		"source blank":     withOverride(botArguments, `"source":" "`),
		"timestamp future": withOverride(botArguments, `"timestamp":`+seconds(overrideNow.Unix()+1)),
		"timestamp old":    withOverride(botArguments, `"timestamp":`+seconds(overrideNow.Unix()-20*365*86400)),
		"bots":             withOverride(botArguments, `"bots":1`),
		"recMode":          withOverride(botArguments, `"recMode":2`),
		"token_auth":       withOverride(botArguments, `"token_auth":"x"`),
		"visitor id":       withOverride(botArguments, `"visitorId":"0123456789abcdef"`),
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true, noContent))
		if _, err := env.change(botTool, "trk", arguments); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(calls), *env.reads)
		}
	}
	// The limits themselves are accepted.
	var calls []call
	env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true, noContent))
	if _, err := env.change(botTool, "trk", `{"idSite":1,"userAgent":"Claude-User","url":"https://example.org/",`+
		`"httpStatus":65535,"bytes":4294967295,"responseTime":4294967295,"source":"`+strings.Repeat("s", 50)+`"}`); err != nil {
		t.Errorf("limits: %v", err)
	}
	if _, err := env.change(botTool, "trk", `{"idSite":1,"userAgent":"Claude-User","url":"https://example.org/",`+
		`"httpStatus":0,"bytes":0,"responseTime":0}`); err != nil {
		t.Errorf("zeros: %v", err)
	}
}

func TestBotRequestRefusesAnotherWebsiteWithoutSecretAccess(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true, noContent))
	_, err := env.change(botTool, "trk-two", botArguments)
	if !isInvalidRequest(err) || err.Error() != outsideTargets || len(calls) != 0 || *env.reads != 0 {
		t.Errorf("err = %v, calls = %d, reads = %d", err, len(calls), *env.reads)
	}
}

func TestBotRequestNeedsMatomo57AndTheBotTrackingPlugin(t *testing.T) {
	for name, handler := range map[string]func(call) (*http.Response, error){
		"too old":  botHandler("5.6.9", true, noContent),
		"inactive": botHandler("5.14.2", false, noContent),
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, handler)
		_, err := env.change(botTool, "trk", botArguments)
		if !isUnsupported(err) || len(trackerCalls(calls)) != 0 {
			t.Errorf("%s: err = %v, tracking requests = %d", name, err, len(trackerCalls(calls)))
		}
		want := map[string]string{"too old": "5.7.0", "inactive": botPlugin}[name]
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	var calls []call
	env := newTrackingEnvironment(t, &calls, botHandler("5.7.0", true, noContent))
	if _, err := env.change(botTool, "trk", botArguments); err != nil || len(trackerCalls(calls)) != 1 {
		t.Errorf("5.7.0: err = %v", err)
	}
}

func TestBotRequestMapsTrackerAnswersAndSendsOnce(t *testing.T) {
	withClock(t, overrideNow)
	for name, tc := range map[string]struct {
		status  int
		body    string
		class   provider.Class
		invalid bool
	}{
		"204":          {http.StatusNoContent, "", "", false},
		"gif":          {http.StatusOK, gifAnswer, "", false},
		"unknown site": {http.StatusBadRequest, bodyCanary, "", true},
		"503":          {http.StatusServiceUnavailable, bodyCanary, provider.ClassUnreachable, false},
		"429":          {http.StatusTooManyRequests, "", provider.ClassRateLimited, false},
		"html":         {http.StatusOK, "<html>" + bodyCanary + "</html>", provider.ClassInvalidResponse, false},
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true,
			func(call) (*http.Response, error) { return respond(tc.status, tc.body), nil }))
		_, err := env.change(botTool, "trk", withOverride(botArguments, `"timestamp":`+seconds(overrideNow.Unix()-7*86400)))
		if len(trackerCalls(calls)) != 1 {
			t.Errorf("%s: %d tracking requests", name, len(trackerCalls(calls)))
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
			if classOf(err) != tc.class {
				t.Errorf("%s: err = %v, want %q", name, err, tc.class)
			}
		}
		if err != nil && (strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), tokenValue)) {
			t.Errorf("%s: message leaks: %v", name, err)
		}
	}
	var calls []call
	env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true,
		func(call) (*http.Response, error) { return nil, context.DeadlineExceeded }))
	_, err := env.change(botTool, "trk", botArguments)
	if classOf(err) != provider.ClassTimeout || !strings.Contains(err.Error(), "never repeated") || len(trackerCalls(calls)) != 1 {
		t.Errorf("timeout: err = %v, tracking requests = %d", err, len(trackerCalls(calls)))
	}
}

func TestBotRequestIsListedLikeTheOtherTrackingTools(t *testing.T) {
	reg := registry(t)
	d, _, found := reg.Lookup(botTool)
	if !found || d.Risk.Effect != capability.EffectCreate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		d.Risk.Confirmation != capability.ConfirmationRequired || !d.RequiresToolAllowList {
		t.Fatalf("found %t, %+v", found, d)
	}
	for _, text := range []string{"Not idempotent", "5.7.0", "BotTracking", "list of AI chatbots", "Forged bot requests",
		"never retries", "bearer header", "token_auth"} {
		if !strings.Contains(d.Description, text) {
			t.Errorf("description lacks %q", text)
		}
	}
	for _, name := range []string{"bots", "recMode", "token_auth", "cdt", "visitorId"} {
		if strings.Contains(string(d.InputSchema), `"`+name+`"`) {
			t.Errorf("schema offers %s", name)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == botTool {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
	var calls []call
	env := newTrackingEnvironment(t, &calls, botHandler("5.14.2", true, noContent))
	if _, err := env.change(botTool, "trk-effects", botArguments); err == nil || len(calls) != 0 {
		t.Errorf("an effects-only connection offered the tool: %v", err)
	}
}

func TestTrackingSenderRefusesBots(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	sender, err := openTracking(mustResolve(t, env, "open"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.send(context.Background(), "test", 1, url.Values{"recMode": {"1"}, "bots": {"1"}}, nil); err == nil || len(calls) != 0 {
		t.Errorf("bots was sent: %v", err)
	}
}
