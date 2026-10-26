package matomo

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// withClock fixes the clock that bounds the timestamp override.
func withClock(t *testing.T, now time.Time) {
	t.Helper()
	previous := trackingClock
	trackingClock = func() time.Time { return now }
	t.Cleanup(func() { trackingClock = previous })
}

var overrideNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// withOverride adds fields to the JSON argument object of a tracking tool.
func withOverride(arguments, fields string) string {
	return strings.TrimSuffix(arguments, "}") + "," + fields + "}"
}

func toolByID(t *testing.T, id string) string {
	t.Helper()
	for _, tool := range trackingTools {
		if tool.id == id {
			return tool.arguments
		}
	}
	t.Fatalf("no tool %s", id)
	return ""
}

func TestTrackingOverridesSendTheTokenInTheBodyOnly(t *testing.T) {
	withClock(t, overrideNow)
	stamp := overrideNow.Unix() - 3600
	for _, tc := range []struct {
		name, fields string
		want         map[string]string
	}{
		{"clientIp", `"clientIp":"192.0.2.7"`, map[string]string{"cip": "192.0.2.7"}},
		{"clientIp v6", `"clientIp":"2001:db8::1"`, map[string]string{"cip": "2001:db8::1"}},
		{"timestamp", `"timestamp":` + seconds(stamp), map[string]string{"cdt": seconds(stamp)}},
		{"country", `"country":"DE"`, map[string]string{"country": "de"}},
		{"region", `"region":"BY"`, map[string]string{"region": "BY"}},
		{"city", `"city":"Munich"`, map[string]string{"city": "Munich"}},
		{"latitude", `"latitude":48.137`, map[string]string{"lat": "48.137"}},
		{"longitude", `"longitude":-11.575`, map[string]string{"long": "-11.575"}},
	} {
		for _, id := range []string{"matomo.tracking.record-page-view", "matomo.tracking.record-goal",
			"matomo.tracking.record-content-interaction"} {
			var calls []call
			env := newTrackingEnvironment(t, &calls, noContent)
			if _, err := env.change(id, "trk", withOverride(toolByID(t, id), tc.fields)); err != nil {
				t.Fatalf("%s %s: %v", id, tc.name, err)
			}
			if len(calls) != 1 || *env.reads == 0 {
				t.Fatalf("%s %s: %d requests, %d secret reads", id, tc.name, len(calls), *env.reads)
			}
			c := calls[0]
			if c.form.Get("token_auth") != tokenValue || c.method != http.MethodPost || c.rawQuery != "" || c.auth != "" ||
				strings.Contains(c.path, tokenValue) {
				t.Errorf("%s %s: request = %+v", id, tc.name, c)
			}
			for name, value := range tc.want {
				if c.form.Get(name) != value {
					t.Errorf("%s %s: %s = %q, want %q", id, tc.name, name, c.form.Get(name), value)
				}
			}
			for _, name := range []string{"cip", "cdt", "cdo", "country", "region", "city", "lat", "long"} {
				if _, wanted := tc.want[name]; !wanted && c.form.Has(name) {
					t.Errorf("%s %s: sent %s", id, tc.name, name)
				}
			}
		}
	}
}

func TestTrackingOverridesWorkForEveryTool(t *testing.T) {
	for _, tool := range trackingTools {
		var calls []call
		env := newTrackingEnvironment(t, &calls, noContent)
		if _, err := env.change(tool.id, "trk", withOverride(tool.arguments, `"clientIp":"192.0.2.7"`)); err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		if len(calls) != 1 || calls[0].form.Get("token_auth") != tokenValue || calls[0].form.Get("cip") != "192.0.2.7" {
			t.Errorf("%s: calls = %+v", tool.id, calls)
		}
	}
}

func seconds(n int64) string { return strconv.FormatInt(n, 10) }

func TestTrackingOverridesAreRefusedBeforeAnySecretAccessOrRequest(t *testing.T) {
	withClock(t, overrideNow)
	future := overrideNow.Unix() + 1
	old := overrideNow.Unix() - 20*365*86400
	for name, fields := range map[string]string{
		"future":          `"timestamp":` + seconds(future),
		"far future":      `"timestamp":4102444800`,
		"20 years":        `"timestamp":` + seconds(old),
		"before 2000":     `"timestamp":946684799`,
		"ip text":         `"clientIp":"example.org"`,
		"ip zone":         `"clientIp":"fe80::1%eth0"`,
		"ip octet":        `"clientIp":"192.0.2.256"`,
		"country long":    `"country":"DEU"`,
		"country digits":  `"country":"d1"`,
		"region long":     `"region":"ABCD"`,
		"region symbol":   `"region":"B-"`,
		"city blank":      `"city":"  "`,
		"city long":       `"city":"` + strings.Repeat("a", 256) + `"`,
		"city nul":        `"city":"a\u0000"`,
		"latitude high":   `"latitude":90.5`,
		"latitude low":    `"latitude":-91`,
		"longitude high":  `"longitude":181`,
		"longitude low":   `"longitude":-180.1`,
		"timestamp float": `"timestamp":1.5`,
		"cdo":             `"cdo":60`,
		"token_auth":      `"token_auth":"x"`,
	} {
		for _, id := range []string{"matomo.tracking.record-event", "matomo.tracking.record-order"} {
			var calls []call
			env := newTrackingEnvironment(t, &calls, noContent)
			arguments := withOverride(toolByID(t, id), fields)
			if _, err := env.change(id, "trk", arguments); err == nil {
				t.Errorf("%s %s was accepted", id, name)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Errorf("%s %s: %d requests, %d secret reads", id, name, len(calls), *env.reads)
			}
		}
	}
}

func TestTrackingOverrideOnAnotherWebsiteIsRefusedWithoutSecretAccess(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	for _, tool := range trackingTools {
		_, err := env.change(tool.id, "trk-two", withOverride(tool.arguments, `"clientIp":"192.0.2.7","country":"de"`))
		if !isInvalidRequest(err) || err.Error() != outsideTargets {
			t.Errorf("%s: err = %v", tool.id, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
}

func TestTrackingRefusalsWithATokenNameTheCauseAndLeakNothing(t *testing.T) {
	withClock(t, overrideNow)
	for name, tc := range map[string]struct {
		status  int
		class   provider.Class
		invalid bool
		text    string
	}{
		"400 token or age": {http.StatusBadRequest, "", true, "Write role"},
		"401":              {http.StatusUnauthorized, provider.ClassAuth, false, "Write role"},
		"403":              {http.StatusForbidden, provider.ClassPermission, false, "Write role"},
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, func(call) (*http.Response, error) {
			return respond(tc.status, "token "+tokenValue+" "+bodyCanary), nil
		})
		_, err := env.change("matomo.tracking.record-event", "trk",
			withOverride(trackingTools[1].arguments, `"clientIp":"192.0.2.7","timestamp":`+seconds(overrideNow.Unix()-7*86400)))
		if err == nil || len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %d", name, err, len(calls))
		}
		if tc.invalid && !isInvalidRequest(err) || !tc.invalid && classOf(err) != tc.class {
			t.Errorf("%s: err = %v", name, err)
		}
		text := err.Error()
		if !strings.Contains(text, tc.text) || strings.Contains(text, tokenValue) || strings.Contains(text, bodyCanary) {
			t.Errorf("%s: message = %q", name, text)
		}
		if tc.invalid && !strings.Contains(text, "older than the raw data") {
			t.Errorf("%s: message does not name the age limit: %q", name, text)
		}
	}
}

func TestTrackingWithoutTokenKeepsTheShortRefusalText(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, func(call) (*http.Response, error) { return respond(http.StatusBadRequest, ""), nil })
	_, err := env.change("matomo.tracking.record-event", "trk", trackingTools[1].arguments)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), "Write role") {
		t.Errorf("err = %v", err)
	}
}

func TestTrackingTokenIsRegisteredForRedaction(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	if _, err := env.change("matomo.tracking.record-event", "trk",
		withOverride(trackingTools[1].arguments, `"city":"Munich"`)); err != nil {
		t.Fatal(err)
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("token not masked: %q", got)
	}
}

func TestTrackingOverrideRequestsAreSentOnceOnEveryUncertainOutcome(t *testing.T) {
	for name, handler := range map[string]func(call) (*http.Response, error){
		"429":     func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil },
		"timeout": func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"unreadable": func(call) (*http.Response, error) {
			return respond(http.StatusOK, "<html>"+bodyCanary+"</html>"), nil
		},
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, handler)
		_, err := env.change("matomo.tracking.record-page-view", "trk",
			withOverride(trackingTools[0].arguments, `"clientIp":"192.0.2.7"`))
		if err == nil || len(calls) != 1 {
			t.Errorf("%s: err = %v, requests = %d", name, err, len(calls))
		}
		if err != nil && (strings.Contains(err.Error(), tokenValue) || strings.Contains(err.Error(), bodyCanary)) {
			t.Errorf("%s: message leaks: %v", name, err)
		}
	}
}

func TestTrackingSenderCarriesOverridesOnlyThroughTheAuthPath(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	sender, err := openTracking(mustResolve(t, env, "open"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range neverSentParameters {
		auth := &trackingAuth{token: tokenValue, overrides: url.Values{"cip": {"192.0.2.7"}}}
		if err := sender.send(ctx, "test", 1, url.Values{name: {"x"}}, auth); err == nil {
			t.Errorf("form parameter %s was sent next to an auth", name)
		}
	}
	for _, auth := range []*trackingAuth{
		{token: tokenValue, overrides: url.Values{"cdo": {"60"}}},
		{token: tokenValue, overrides: url.Values{"token_auth": {"x"}}},
		{token: tokenValue, overrides: url.Values{"cip": {"a", "b"}}},
		{token: tokenValue},
		{overrides: url.Values{"cip": {"192.0.2.7"}}},
	} {
		if err := sender.send(ctx, "test", 1, url.Values{}, auth); err == nil {
			t.Errorf("auth %+v was sent", auth)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
	if err := sender.send(ctx, "test", 1, url.Values{}, &trackingAuth{token: tokenValue,
		overrides: url.Values{"cip": {"192.0.2.7"}}}); err != nil || len(calls) != 1 ||
		calls[0].form.Get("token_auth") != tokenValue || calls[0].auth != "" {
		t.Errorf("err = %v, calls = %+v", err, calls)
	}
}
