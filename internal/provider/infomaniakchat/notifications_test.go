package infomaniakchat

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func notifyFixture(t *testing.T, mutate func(*http.Request) (*http.Response, error)) *categoryFixture {
	return newCategoryFixture(t, mutate)
}

func TestChannelNotificationsSendOneTypedPutForTheOwnUser(t *testing.T) {
	f := notifyFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v4/channels/"+chanA+"/members/me/notify_props" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, `{"status":"OK"}`), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"channel_id":"` + chanA + `","desktop":"mention","email":"false","mark_unread":"mention"}`
	if _, err := env.invoke(channelNotificationsUpdate.ID, "editor", args); !isConfirmationRequired(err) ||
		len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(channelNotificationsUpdate.ID, "editor", args)
	if err != nil || !strings.Contains(result, `"desktop":"mention"`) || strings.Contains(result, `"push"`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	last := calls[len(calls)-1]
	if changes(calls) != 1 || last.method != http.MethodPut ||
		last.body != `{"desktop":"mention","email":"false","mark_unread":"mention"}` {
		t.Fatalf("calls = %+v, want one read and exactly one PUT with only the given fields", calls)
	}
}

func TestChannelNotificationsRefuseValuesOutsideTheFixedLists(t *testing.T) {
	var calls []call
	env := notifyFixture(t, nil).env(&calls)
	prefix := `{"channel_id":"` + chanA + `",`
	for name, args := range map[string]string{
		"no field":      `{"channel_id":"` + chanA + `"}`,
		"desktop":       prefix + `"desktop":"true"}`,
		"push":          prefix + `"push":"ALL"}`,
		"email":         prefix + `"email":"mention"}`,
		"mark_unread":   prefix + `"mark_unread":"none"}`,
		"extra field":   prefix + `"desktop":"all","channel_auto_follow_threads":"on"}`,
		"malformed id":  `{"channel_id":"../` + chanA + `","desktop":"all"}`,
		"outside list":  `{"channel_id":"` + chanC + `","desktop":"all"}`,
		"empty value":   prefix + `"desktop":""}`,
		"injected path": `{"channel_id":"` + chanA + `/x","desktop":"all"}`,
	} {
		if _, err := env.confirmed(channelNotificationsUpdate.ID, "editorch", args); err == nil || len(calls) != 0 ||
			*env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d, want a local refusal", name, err, calls, *env.reads)
		}
	}
}

func TestChannelNotificationsRefuseForeignDirectAndArchivedChannelsBeforeThePut(t *testing.T) {
	var calls []call
	env := notifyFixture(t, nil).env(&calls)
	for _, id := range []string{chanB, chanD, chanE} {
		calls = nil
		_, err := env.confirmed(channelNotificationsUpdate.ID, "editor", `{"channel_id":"`+id+`","push":"none"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), id) || strings.Contains(err.Error(), teamB) {
			t.Fatalf("%s: err = %v", id, err)
		}
		if len(calls) != 1 || calls[0].method != http.MethodGet {
			t.Fatalf("%s: calls = %+v, want only the channel read", id, calls)
		}
	}
}

func TestChannelNotificationsAreNeverRetriedAfterAnUnclearResult(t *testing.T) {
	for name, fail := range map[string]func(*http.Request) (*http.Response, error){
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
		},
		"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{not json`), nil },
		"no status":  func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{"status":"FAIL"}`), nil },
	} {
		var calls []call
		env := notifyFixture(t, fail).env(&calls)
		_, err := env.confirmed(channelNotificationsUpdate.ID, "editor", `{"channel_id":"`+chanA+`","push":"none"}`)
		if err == nil || !strings.Contains(err.Error(), "may have been changed") ||
			strings.Contains(err.Error(), messageCanary) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if countMethod(calls, http.MethodPut) != 1 || changes(calls) != 1 {
			t.Fatalf("%s: calls = %+v, want exactly one change request", name, calls)
		}
	}
}
