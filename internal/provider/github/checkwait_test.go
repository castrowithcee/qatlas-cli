package github

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

const (
	waitOldHead = "aaaa111100000000000000000000000000000000"
	waitNewHead = "bbbb222200000000000000000000000000000000"
)

// waitFixture serves pull request 7 and lets onPoll change the fake server before the answer of each pull
// request read, which is the first request of every poll. A status other than zero answers that status instead.
type waitFixture struct {
	t    *testing.T
	core *application.Core
	f    *fakeGitHub
	p    *fakePulls
	base string
	mu   sync.Mutex
	poll int
}

func newWaitFixture(t *testing.T, onPoll func(poll int, p *fakePulls) int) *waitFixture {
	t.Helper()
	previousInterval, previousSlice, previousMargin := checksWaitInterval, checksWaitSlice, checksWaitMargin
	t.Cleanup(func() {
		checksWaitInterval, checksWaitSlice, checksWaitMargin = previousInterval, previousSlice, previousMargin
	})
	checksWaitInterval, checksWaitSlice, checksWaitMargin = 5*time.Millisecond, 2*time.Second, 20*time.Millisecond

	f, p, base := servePulls(t)
	p.pulls = append(p.pulls, fakePull{number: 7, title: "x", state: "open", headSHA: waitOldHead})
	w := &waitFixture{t: t, f: f, p: p, base: base}
	route := f.failure
	f.failure = func(rw http.ResponseWriter, r *http.Request) bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/7") {
			w.poll++
			if status := onPoll(w.poll, p); status != 0 {
				rw.WriteHeader(status)
				return true
			}
		}
		return route(rw, r)
	}
	red := &redact.Redactor{}
	w.core = application.New(registry(t), coreConfig(base), resolver(red, nil), red)
	return w
}

func (w *waitFixture) polls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.poll
}

func (w *waitFixture) wait(ctx context.Context, arguments string) (PullRequestChecksWait, error) {
	w.t.Helper()
	response, err := w.core.Invoke(ctx, application.InvokeRequest{Operation: "github.pull-request-checks.wait",
		Connection: "repo", Arguments: json.RawMessage(arguments)})
	var got PullRequestChecksWait
	if err == nil {
		if err := json.Unmarshal(response.Result, &got); err != nil {
			w.t.Fatalf("result %s: %v", response.Result, err)
		}
	}
	return got, err
}

func checkRun(name, status, conclusion string) fakeCheckRun {
	return fakeCheckRun{name: name, status: status, conclusion: conclusion}
}

// The CLI form: the context carries the wait options, the wait ends with the result of the checks.
func cliWait(limit time.Duration) context.Context {
	return capability.WithWait(context.Background(), capability.WaitOptions{Limit: limit, Interval: 5 * time.Millisecond})
}

func TestWaitAnswersPassOnlyAfterEveryCheckFinished(t *testing.T) {
	w := newWaitFixture(t, func(poll int, p *fakePulls) int {
		switch poll {
		case 1:
			p.runs = map[string][]fakeCheckRun{waitOldHead: {checkRun("build", "completed", "success"), checkRun("test", "queued", "")}}
		case 2:
			p.runs = map[string][]fakeCheckRun{waitOldHead: {checkRun("build", "completed", "success"), checkRun("test", "in_progress", "")}}
		default:
			p.runs = map[string][]fakeCheckRun{waitOldHead: {checkRun("build", "completed", "success"),
				checkRun("test", "completed", "success"), checkRun("lint", "completed", "skipped"), checkRun("scan", "completed", "neutral")}}
		}
		return 0
	})
	for name, ctx := range map[string]context.Context{"broker": context.Background(), "command": cliWait(5 * time.Second)} {
		w.poll = 0
		got, err := w.wait(ctx, `{"number":7}`)
		if err != nil || got.Result != "pass" || got.Polls != 3 || got.HeadSHA != waitOldHead || len(got.Checks) != 4 {
			t.Errorf("%s: got %+v, %v, want pass after 3 polls with 4 checks", name, got, err)
		}
		for _, check := range got.Checks {
			if check.Name == "" || check.Status != "completed" || check.Conclusion == "" || check.URL == "" {
				t.Errorf("%s: check %+v lacks name, status, conclusion, or link", name, check)
			}
		}
	}
}

func TestWaitFailsAsSoonAsAFinishedCheckFailed(t *testing.T) {
	for _, conclusion := range []string{"failure", "cancelled", "timed_out", "action_required", "startup_failure", "stale"} {
		w := newWaitFixture(t, func(poll int, p *fakePulls) int {
			runs := []fakeCheckRun{checkRun("build", "in_progress", "")}
			if poll >= 2 {
				runs = append(runs, checkRun("test", "completed", conclusion))
			}
			p.runs = map[string][]fakeCheckRun{waitOldHead: runs}
			return 0
		})
		got, err := w.wait(cliWait(5*time.Second), `{"number":7}`)
		if err != nil || got.Result != "fail" || got.Polls != 2 {
			t.Errorf("%s: got %+v, %v, want fail at once while another check still runs", conclusion, got, err)
		}
	}
	w := newWaitFixture(t, func(_ int, p *fakePulls) int {
		p.statuses = map[string][]fakeStatus{waitOldHead: {{context: "ci/legacy", state: "error"}}}
		return 0
	})
	if got, err := w.wait(context.Background(), `{"number":7}`); err != nil || got.Result != "fail" {
		t.Errorf("legacy status error: got %+v, %v, want fail", got, err)
	}
}

func TestWaitAnswersNoneOnlyAfterSeveralEmptyPolls(t *testing.T) {
	w := newWaitFixture(t, func(int, *fakePulls) int { return 0 })
	got, err := w.wait(context.Background(), `{"number":7}`)
	if err != nil || got.Result != "none" || got.Polls != checksWaitNonePolls || len(got.Checks) != 0 {
		t.Errorf("got %+v, %v, want none after %d polls", got, err, checksWaitNonePolls)
	}
	// A check that appears late ends the emptiness.
	w = newWaitFixture(t, func(poll int, p *fakePulls) int {
		if poll >= 3 {
			p.runs = map[string][]fakeCheckRun{waitOldHead: {checkRun("build", "completed", "success")}}
		}
		return 0
	})
	if got, err := w.wait(context.Background(), `{"number":7}`); err != nil || got.Result != "pass" || got.Polls != 3 {
		t.Errorf("late check: got %+v, %v, want pass", got, err)
	}
}

func TestWaitEndsWithTimeoutOrPendingAtItsLimit(t *testing.T) {
	w := newWaitFixture(t, func(_ int, p *fakePulls) int {
		p.runs = map[string][]fakeCheckRun{waitOldHead: {checkRun("build", "in_progress", "")}}
		return 0
	})
	got, err := w.wait(cliWait(60*time.Millisecond), `{"number":7}`)
	if err != nil || got.Result != "timeout" || got.HeadSHA != waitOldHead || len(got.Checks) != 1 || got.Polls < 2 {
		t.Errorf("command: got %+v, %v, want timeout with the checks of the last poll", got, err)
	}
	checksWaitSlice = 60 * time.Millisecond
	got, err = w.wait(context.Background(), `{"number":7}`)
	if err != nil || got.Result != "pending" || got.HeadSHA != waitOldHead || len(got.Checks) != 1 {
		t.Errorf("broker: got %+v, %v, want pending with the awaited head commit", got, err)
	}
}

// The broker's call ends before the invoke limit it runs under, whatever its slice is.
func TestWaitReturnsPendingBeforeTheInvokeLimit(t *testing.T) {
	w := newWaitFixture(t, func(_ int, p *fakePulls) int {
		p.runs = map[string][]fakeCheckRun{waitOldHead: {checkRun("build", "in_progress", "")}}
		return 0
	})
	checksWaitSlice = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	got, err := w.wait(ctx, `{"number":7}`)
	if err != nil || got.Result != "pending" || time.Since(start) >= 400*time.Millisecond {
		t.Errorf("got %+v, %v after %s, want pending before the limit", got, err, time.Since(start))
	}
}

func TestWaitEndsWithHeadChangedInsteadOfTheOldRun(t *testing.T) {
	changed := func(poll int, p *fakePulls) int {
		p.runs = map[string][]fakeCheckRun{
			waitOldHead: {checkRun("build", "in_progress", "")},
			waitNewHead: {checkRun("build", "completed", "success")},
		}
		if poll >= 2 {
			p.pulls[0].headSHA = waitNewHead
		}
		return 0
	}
	// The awaited head is the one at the start of the call, or the one named.
	for name, arguments := range map[string]string{"start": `{"number":7}`, "named": `{"number":7,"head_sha":"` + waitOldHead + `"}`} {
		w := newWaitFixture(t, changed)
		got, err := w.wait(cliWait(5*time.Second), arguments)
		if err != nil || got.Result != "head-changed" || got.HeadSHA != waitOldHead || got.CurrentHeadSHA != waitNewHead ||
			len(got.Checks) != 0 {
			t.Errorf("%s: got %+v, %v, want head-changed without the checks of either run", name, got, err)
		}
	}
	// A named head that is not the current one ends the first poll.
	w := newWaitFixture(t, changed)
	got, err := w.wait(context.Background(), `{"number":7,"head_sha":"`+waitNewHead+`"}`)
	if err != nil || got.Result != "head-changed" || got.Polls != 1 || got.CurrentHeadSHA != waitOldHead {
		t.Errorf("named newer head: got %+v, %v, want head-changed at the first poll", got, err)
	}
}

func TestWaitRepeatsATransientFailureAMeasuredNumberOfTimes(t *testing.T) {
	green := func(p *fakePulls) {
		p.runs = map[string][]fakeCheckRun{waitOldHead: {checkRun("build", "completed", "success")}}
	}
	w := newWaitFixture(t, func(poll int, p *fakePulls) int {
		green(p)
		if poll <= 2 {
			return http.StatusBadGateway
		}
		return 0
	})
	if got, err := w.wait(context.Background(), `{"number":7}`); err != nil || got.Result != "pass" {
		t.Errorf("two failures: got %+v, %v, want pass after retries", got, err)
	}

	w = newWaitFixture(t, func(_ int, p *fakePulls) int { green(p); return http.StatusServiceUnavailable })
	_, err := w.wait(context.Background(), `{"number":7}`)
	if classOf(err) != provider.ClassProviderError || w.polls() != checksWaitFailures {
		t.Errorf("endless failure: err = %v after %d polls, want the last error after %d", err, w.polls(), checksWaitFailures)
	}

	// What waiting does not change is not repeated.
	for status, class := range map[int]provider.Class{http.StatusNotFound: provider.ClassNotFound,
		http.StatusUnauthorized: provider.ClassAuth} {
		w = newWaitFixture(t, func(int, *fakePulls) int { return status })
		if _, err := w.wait(context.Background(), `{"number":7}`); classOf(err) != class || w.polls() != 1 {
			t.Errorf("status %d: err = %v after %d polls, want %s at once", status, err, w.polls(), class)
		}
	}
}

func TestWaitRefusesATargetOutsideTheConnectionBeforeAnyRequest(t *testing.T) {
	w := newWaitFixture(t, func(int, *fakePulls) int { return 0 })
	reads := 0
	red := &redact.Redactor{}
	w.core = application.New(registry(t), coreConfig(w.base), resolver(red, &reads), red)
	before := len(w.f.recorded())
	for _, arguments := range []string{`{"number":7,"repository":"octo-org/other"}`, `{"number":7,"head_sha":"abc"}`} {
		if _, err := w.wait(context.Background(), arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if _, err := w.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "github.pull-request-checks.wait", Connection: "planning", Arguments: json.RawMessage(`{"number":7}`)}); err == nil {
		t.Error("a project connection was accepted")
	}
	if requests := len(w.f.recorded()) - before; requests != 0 || reads != 0 {
		t.Errorf("requests = %d, secret reads = %d, want none", requests, reads)
	}
}

// Agents with a shell find the command that waits for the end in the tool's description.
func TestWaitDescriptionNamesTheCommandForAgentsWithAShell(t *testing.T) {
	if !strings.Contains(pullChecksWait.Description, "qconnect wait github.pull-request-checks.wait") ||
		!pullChecksWait.Waits || pullChecksWait.Risk != pullChecksList.Risk {
		t.Errorf("descriptor = %+v, want the command named, Waits set, and the risk of the list", pullChecksWait)
	}
}
