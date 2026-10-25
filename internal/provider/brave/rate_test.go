package brave

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func ptr(n int64) *int64 { return &n }

func TestParseRateReadsEveryWindow(t *testing.T) {
	h := http.Header{}
	h.Set("X-RateLimit-Limit", "1, 15000")
	h.Set("X-RateLimit-Policy", "1;w=1, 15000;w=2592000")
	h.Set("X-RateLimit-Remaining", "0, 14523")
	h.Set("X-RateLimit-Reset", "1, 1234567")
	info := parseRate(h)
	if len(info.windows) != 2 {
		t.Fatalf("windows = %+v", info.windows)
	}
	month := info.windows[1]
	if *month.limit != 15000 || *month.remaining != 14523 || *month.reset != 1234567 || *month.seconds != 2592000 {
		t.Errorf("month = %+v", month)
	}
	if short, long := info.exhausted(); short != time.Second || long != 0 {
		t.Errorf("exhausted = %v, %v, want a used-up second", short, long)
	}
}

func TestParseRateToleratesMissingAndMalformedHeaders(t *testing.T) {
	if info := parseRate(http.Header{}); len(info.windows) != 0 || quotaOf(info) != nil {
		t.Errorf("no headers = %+v", info)
	}
	h := http.Header{}
	h.Set("X-RateLimit-Remaining", "x, -3, 5, 6, 7, 8, 9")
	h.Set("X-RateLimit-Policy", "garbage, 5;w=oops")
	info := parseRate(h)
	if len(info.windows) != maxWindows || info.windows[0].remaining != nil || info.windows[1].remaining != nil ||
		*info.windows[2].remaining != 5 || *info.windows[1].limit != 5 || info.windows[1].seconds != nil {
		t.Errorf("windows = %+v", info.windows)
	}
	// The limit falls back to the policy when its own header is missing.
	h = http.Header{}
	h.Set("X-RateLimit-Policy", "50;w=1")
	if info := parseRate(h); *info.windows[0].limit != 50 || *info.windows[0].seconds != 1 {
		t.Errorf("policy only = %+v", info.windows)
	}
}

func TestExhaustedSeparatesTheSecondFromTheQuota(t *testing.T) {
	info := rateInfo{windows: []window{
		{remaining: ptr(0), reset: ptr(1)}, {remaining: ptr(0), reset: ptr(1419704)}}}
	if short, long := info.exhausted(); short != time.Second || long != 1419704*time.Second {
		t.Errorf("both = %v, %v", short, long)
	}
	if short, long := (rateInfo{windows: []window{{remaining: ptr(0)}}}).exhausted(); short != time.Second || long != 0 {
		t.Errorf("no reset = %v, %v, want a second", short, long)
	}
	if short, long := (rateInfo{windows: []window{{remaining: ptr(3), reset: ptr(9)}}}).exhausted(); short != 0 || long != 0 {
		t.Errorf("left over = %v, %v", short, long)
	}
}

// pacing is a limiter on a fake clock, so a test sees how long the next request has to wait.
type pacing struct {
	now   time.Time
	slept []time.Duration
}

func (p *pacing) limiter(interval time.Duration) *ratelimit.Limiter {
	return ratelimit.New(interval, func() time.Time { return p.now }, func(_ context.Context, d time.Duration) error {
		p.slept = append(p.slept, d)
		p.now = p.now.Add(d)
		return nil
	})
}

func newPacing() *pacing { return &pacing{now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)} }

// pacedEnvironment is an environment whose key is spaced by the fake clock of p.
func pacedEnvironment(t *testing.T, f *fakeBrave, p *pacing, interval time.Duration) *environment {
	t.Helper()
	env := newEnvironment(t, f)
	t.Cleanup(limiters.Replace(tokenValue, p.limiter(interval)))
	return env
}

func (e *environment) search(t *testing.T) error {
	t.Helper()
	_, err := e.invoke("brave.news.search", "search", `{"q":"x"}`)
	return err
}

func TestARequestWaitsForTheSpacingTheLimitImplies(t *testing.T) {
	p := newPacing()
	env := pacedEnvironment(t, &fakeBrave{}, p, 0)
	for i := 0; i < 3; i++ {
		if err := env.search(t); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture reports one request per second, so the second and third request wait a second each.
	if len(p.slept) != 2 || p.slept[0] != time.Second || p.slept[1] != time.Second {
		t.Errorf("slept = %v, want one second before each follow-up", p.slept)
	}

	// A fast plan adds nothing beyond the start spacing.
	p = newPacing()
	fast := &fakeBrave{answer: reply(200, map[string]string{"X-RateLimit-Limit": "50, 0",
		"X-RateLimit-Remaining": "49, 0", "X-RateLimit-Reset": "1, 0"}, `{"results":[]}`)}
	env = pacedEnvironment(t, fast, p, minInterval)
	for i := 0; i < 2; i++ {
		if err := env.search(t); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.slept) != 1 || p.slept[0] != minInterval {
		t.Errorf("slept = %v, want the start spacing", p.slept)
	}
}

func TestAUsedUpSecondHoldsTheNextRequest(t *testing.T) {
	p := newPacing()
	f := &fakeBrave{answer: reply(200, map[string]string{"X-RateLimit-Limit": "50, 15000",
		"X-RateLimit-Policy": "50;w=1, 15000;w=2592000", "X-RateLimit-Remaining": "0, 14000",
		"X-RateLimit-Reset": "3, 1000000"}, `{"results":[]}`)}
	env := pacedEnvironment(t, f, p, 0)
	for i := 0; i < 2; i++ {
		if err := env.search(t); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.slept) != 1 || p.slept[0] != 3*time.Second {
		t.Errorf("slept = %v, want the 3 seconds to the reset, and no wait for the month", p.slept)
	}
}

func TestAnExhaustedSecondAnswersRateLimitedAndHolds(t *testing.T) {
	p := newPacing()
	f := &fakeBrave{answer: reply(429, map[string]string{"X-RateLimit-Limit": "1, 15000",
		"X-RateLimit-Policy": "1;w=1, 15000;w=2592000", "X-RateLimit-Remaining": "0, 14000",
		"X-RateLimit-Reset": "2, 1000000"}, errorBody(429))}
	env := pacedEnvironment(t, f, p, 0)
	err := env.search(t)
	if classOf(err) != provider.ClassRateLimited || !strings.Contains(err.Error(), "retry after 2 seconds") ||
		strings.Contains(err.Error(), "quota") {
		t.Fatalf("err = %v, want rate-limited with the seconds to wait", err)
	}
	// The next call waits the window out first and sends its one request; nothing is repeated.
	f.answer = nil
	if err := env.search(t); err != nil {
		t.Fatal(err)
	}
	if len(p.slept) != 1 || p.slept[0] != 2*time.Second || len(f.recorded()) != 2 {
		t.Errorf("slept = %v, requests = %d", p.slept, len(f.recorded()))
	}
}

func TestAnExhaustedQuotaEndsTheCallWithoutWaiting(t *testing.T) {
	p := newPacing()
	f := &fakeBrave{answer: reply(429, map[string]string{"X-RateLimit-Limit": "1, 15000",
		"X-RateLimit-Policy": "1;w=1, 15000;w=2592000", "X-RateLimit-Remaining": "0, 0",
		"X-RateLimit-Reset": "1, 1419704"}, errorBody(429))}
	env := pacedEnvironment(t, f, p, 0)
	err := env.search(t)
	if classOf(err) != provider.ClassRateLimited || !strings.Contains(err.Error(), "quota") ||
		!strings.Contains(err.Error(), "1419704 seconds") || len(p.slept) != 0 || len(f.recorded()) != 1 {
		t.Fatalf("err = %v, slept %v, requests %d, want the quota named and no wait", err, p.slept, len(f.recorded()))
	}
	// Only the sliding second is held for the next call, never the month.
	_ = env.search(t)
	if len(p.slept) != 1 || p.slept[0] != time.Second {
		t.Errorf("slept = %v, want the second held and not the month", p.slept)
	}
}

func TestARateLimitWithoutHeadersStillHoldsASecond(t *testing.T) {
	p := newPacing()
	env := pacedEnvironment(t, &fakeBrave{answer: reply(429, nil, errorBody(429))}, p, 0)
	err := env.search(t)
	if classOf(err) != provider.ClassRateLimited || strings.Contains(err.Error(), "retry after") {
		t.Fatalf("err = %v", err)
	}
	_ = env.search(t)
	if len(p.slept) != 1 || p.slept[0] != time.Second {
		t.Errorf("slept = %v, want a second after the headerless 429", p.slept)
	}
}

func TestAWaitBeyondTheRequestEndsBeforeSending(t *testing.T) {
	p := newPacing()
	lim := p.limiter(0)
	lim.HoldFor(10 * time.Minute)
	f := &fakeBrave{}
	serve(t, f)
	client, err := open(context.Background(), openable(), resolver(&redact.Redactor{}, nil), &redact.Redactor{}, lim)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var out map[string]any
	_, err = client.get(ctx, "search news", newsPath, nil, nil, &out)
	if classOf(err) != provider.ClassRateLimited || !strings.Contains(err.Error(), "retry after 600 seconds") ||
		len(f.recorded()) != 0 {
		t.Errorf("err = %v, requests = %d, want an immediate rate-limited", err, len(f.recorded()))
	}
}

func openable() *config.Resolved {
	return &config.Resolved{Name: "b", Provider: Provider, BaseURL: apiRoot, Credential: "k", Secrets: credential(),
		Effects: []config.Effect{config.EffectRead}}
}

func TestQuotaIsOmittedWithoutHeaders(t *testing.T) {
	env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, `{"results":[]}`)})
	result, err := env.invoke("brave.news.search", "search", `{"q":"x"}`)
	var got map[string]json.RawMessage
	if err != nil || json.Unmarshal([]byte(result), &got) != nil {
		t.Fatalf("result %s, err %v", result, err)
	}
	if _, ok := got["quota"]; ok {
		t.Errorf("quota present without headers: %s", got["quota"])
	}
}

func TestQuotaKeepsAZeroRemaining(t *testing.T) {
	env := newEnvironment(t, &fakeBrave{answer: reply(200, map[string]string{"X-RateLimit-Limit": "0, 15000",
		"X-RateLimit-Remaining": "0, 0", "X-RateLimit-Reset": "1, 5"}, `{"results":[]}`)})
	result, err := env.invoke("brave.news.search", "search", `{"q":"x"}`)
	if err != nil || !strings.Contains(result, `"remaining":0`) || !strings.Contains(result, `"limit":0`) {
		t.Errorf("result %s, err %v, want zero values kept", result, err)
	}
}

func TestTheRedactorMasksTheKeyAfterOpen(t *testing.T) {
	serve(t, &fakeBrave{})
	red := &redact.Redactor{}
	if _, err := Open(context.Background(), openable(), resolver(red, nil), red); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(red.Apply("token "+tokenValue), tokenValue) {
		t.Error("the key is not registered")
	}
}
