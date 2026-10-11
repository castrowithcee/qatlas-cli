package infomaniakchat

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func remindArgs(post, when string) string {
	return `{"post_id":"` + post + `","target_time":"` + when + `"}`
}

func TestRemindSendsOneFixedBodyInUnixSeconds(t *testing.T) {
	fixClock(t)
	var calls []call
	env := newEnvironment(t, &calls, flagServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"status":"OK"}`), nil
	}))
	target := fixedNow.Add(48 * time.Hour)
	result, err := env.confirmed(messagesRemind.ID, "team", remindArgs(postInChanA, target.Format(time.RFC3339)))
	if err != nil || !strings.Contains(result, postInChanA) || strings.Contains(result, "status") {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	want := `{"target_time":` + strconv.FormatInt(target.Unix(), 10) + `}`
	if last.method != http.MethodPost || last.path != "/api/v4/users/me/posts/"+postInChanA+"/reminder" ||
		last.body != want || len(calls)-countMethod(calls, http.MethodGet) != 1 {
		t.Fatalf("calls = %+v, want one %s", calls, want)
	}
}

func TestRemindRefusesBadTimesBeforeSecretAndIO(t *testing.T) {
	fixClock(t)
	for name, when := range map[string]string{
		"past":     fixedNow.Add(-time.Hour).Format(time.RFC3339),
		"now":      fixedNow.Format(time.RFC3339),
		"too far":  fixedNow.AddDate(1, 0, 1).Format(time.RFC3339),
		"not 3339": "tomorrow",
		"unix":     "1893456000",
	} {
		var calls []call
		env := newEnvironment(t, &calls, flagServer(t, nil))
		_, err := env.confirmed(messagesRemind.ID, "team", remindArgs(postInChanA, when))
		if !isInvalidRequest(err) || *env.reads != 0 || len(calls) != 0 {
			t.Fatalf("%s: err = %v, reads = %d, calls = %+v, want a local invalid request", name, err, *env.reads, calls)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, flagServer(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"status":"OK"}`), nil
	}))
	if _, err := env.confirmed(messagesRemind.ID, "team",
		remindArgs(postInChanA, fixedNow.AddDate(1, 0, 0).Format(time.RFC3339))); err != nil {
		t.Fatalf("one year ahead: err = %v", err)
	}
}

func TestRemindRefusesForeignDeletedAndMalformedPosts(t *testing.T) {
	fixClock(t)
	when := fixedNow.Add(time.Hour).Format(time.RFC3339)
	for _, tt := range []struct {
		name, post, connection string
		local                  bool
	}{
		{"foreign team", postInChanB, "team", false},
		{"outside allow-list", postInChanC, "channel", false},
		{"deleted", postDeleted, "team", false},
		{"malformed", "../" + chanA, "team", true},
	} {
		var calls []call
		env := newEnvironment(t, &calls, flagServer(t, nil))
		_, err := env.confirmed(messagesRemind.ID, tt.connection, remindArgs(tt.post, when))
		if !isInvalidRequest(err) || len(calls) != countMethod(calls, http.MethodGet) ||
			(tt.local && (*env.reads != 0 || len(calls) != 0)) {
			t.Fatalf("%s: err = %v, reads = %d, calls = %+v", tt.name, err, *env.reads, calls)
		}
		for _, leaked := range []string{messageCanary, chanB, teamB, postInChanB} {
			if strings.Contains(err.Error(), leaked) {
				t.Fatalf("%s: error leaked %q: %v", tt.name, leaked, err)
			}
		}
	}
}

func TestRemindRequiresConfirmationAndCreatePermission(t *testing.T) {
	fixClock(t)
	args := remindArgs(postInChanA, fixedNow.Add(time.Hour).Format(time.RFC3339))
	var calls []call
	env := newEnvironment(t, &calls, flagServer(t, nil))
	if _, err := env.invoke(messagesRemind.ID, "team", args); !isConfirmationRequired(err) {
		t.Fatalf("err = %v, want confirmation-required", err)
	}
	if _, err := env.confirmed(messagesRemind.ID, "editor", args); err == nil {
		t.Fatal("want a refusal without the create permission")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
	}
}

func TestRemindUnclearResultIsNeverRetried(t *testing.T) {
	fixClock(t)
	args := remindArgs(postInChanA, fixedNow.Add(time.Hour).Format(time.RFC3339))
	for name, fail := range map[string]func(*http.Request) (*http.Response, error){
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
		},
		"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"no status":  func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{}`), nil },
	} {
		var calls []call
		env := newEnvironment(t, &calls, flagServer(t, fail))
		_, err := env.confirmed(messagesRemind.ID, "team", args)
		if err == nil || !strings.Contains(err.Error(), "may have been set") || strings.Contains(err.Error(), messageCanary) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if len(calls)-countMethod(calls, http.MethodGet) != 1 {
			t.Fatalf("%s: calls = %+v, want exactly one changing request", name, calls)
		}
	}
}
