package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const linkCanary = "https://cloud.example.org/index.php/apps/secret/link-canary"

const notificationsJSON = `[{"notification_id":7,"app":"spreed","user":"alice","datetime":"2026-10-01T08:00:00+00:00",
"object_type":"call","object_id":"abc","subject":"Call from Bob","message":"Join now","link":"` + linkCanary + `","icon":"` + linkCanary + `",
"subjectRich":"{u}","subjectRichParameters":{"u":{"type":"user","id":"bob","name":"Bob","link":"` + linkCanary + `"}},
"actions":[{"label":"Accept","link":"` + linkCanary + `/a","type":"POST","primary":true},{"label":"Open","link":"` + linkCanary + `/w","type":"WEB"},{"label":"Odd","link":"x","type":"TRACE"}]},
{"notification_id":"x1","app":"bad","subject":"skipped"}]`

type tool func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)

func runAccount(h tool, args string) (any, error) {
	red := &redact.Redactor{}
	return accountBound(capability.Handler(h))(capability.WithConfirmed(context.Background()), accountConnection(), resolver(red), red, json.RawMessage(args))
}

func TestAccountToolsRefuseWithoutAccountTargetBeforeSecretAccess(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for name, h := range map[string]capability.Handler{"list": invokeNotificationsList, "dismiss": invokeNotificationsDismiss,
		"dismissall": invokeNotificationsDismissAll, "activity": invokeActivityList} {
		_, err := accountBound(h)(capability.WithConfirmed(context.Background()), reportsConnection(), res, &redact.Redactor{}, json.RawMessage(`{"notification_id":"1"}`))
		if err == nil {
			t.Errorf("%s: no error without the account target", name)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestNotificationsListReportsNoLinksAndReducesActions(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		resp := ocsResponse(200, ocsData(notificationsJSON))
		resp.Header.Set("ETag", `"abc123"`)
		return resp, nil
	})
	got, err := runAccount(invokeNotificationsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*NotificationList)
	if list.Count != 1 || list.ETag != `"abc123"` || list.Unchanged || len(list.Notifications[0].Actions) != 3 {
		t.Fatalf("list = %+v", list)
	}
	n := list.Notifications[0]
	if n.ID != "7" || n.Time != "2026-10-01T08:00:00Z" || n.Subject != "Call from Bob" ||
		n.Actions[0] != (NotificationAction{"Accept", "POST"}) || n.Actions[1].Method != "WEB" || n.Actions[2].Method != "UNKNOWN" {
		t.Errorf("notification = %+v", n)
	}
	out, _ := json.Marshal(got)
	if strings.Contains(string(out), "canary") || strings.Contains(string(out), "http") {
		t.Errorf("output carries a URL: %s", out)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet ||
		!strings.HasSuffix((*calls)[0].url.Path, "/ocs/v2.php/apps/notifications/api/v2/notifications") {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestNotificationsListNotModifiedAndNoContent(t *testing.T) {
	var inm string
	status := 304
	serve(t, func(r *http.Request) (*http.Response, error) {
		inm = r.Header.Get("If-None-Match")
		resp := ocsResponse(status, "")
		return resp, nil
	})
	got, err := runAccount(invokeNotificationsList, `{"etag":"\"abc123\""}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*NotificationList)
	if !list.Unchanged || list.Count != 0 || len(list.Notifications) != 0 || inm != `"abc123"` || list.ETag != `"abc123"` {
		t.Errorf("list = %+v, If-None-Match = %q", list, inm)
	}
	status = 204
	got, err = runAccount(invokeNotificationsList, `{}`)
	if err != nil || got.(*NotificationList).Unchanged || got.(*NotificationList).Count != 0 {
		t.Errorf("204 = %+v, %v", got, err)
	}
	if _, err := runAccount(invokeNotificationsList, `{"etag":"a b\r\nX: y"}`); err == nil {
		t.Error("an unsafe etag was accepted")
	}
}

func TestNotificationsListCapsAndCuts(t *testing.T) {
	var items []string
	for i := 1; i <= 105; i++ {
		items = append(items, `{"notification_id":`+itoa(i)+`,"app":"a","subject":"`+strings.Repeat("s", 2000)+`"}`)
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData("["+strings.Join(items, ",")+"]")), nil
	})
	got, err := runAccount(invokeNotificationsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*NotificationList)
	if list.Count != 100 || !list.Truncated || len(list.Notifications[0].Subject) != maxNotificationTxt || !list.Notifications[0].Truncated {
		t.Errorf("count = %d, truncated = %v", list.Count, list.Truncated)
	}
}

func marshal(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestNotificationsDismissReadsThenDeletesOnce(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`[]`)), nil
	})
	got, err := runAccount(invokeNotificationsDismiss, `{"notification_id":"7"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["dismissed"] != true || len(*calls) != 2 || (*calls)[0].method != http.MethodGet ||
		(*calls)[1].method != http.MethodDelete || !strings.HasSuffix((*calls)[1].url.Path, "/notifications/7") {
		t.Errorf("got = %+v, calls = %+v", got, *calls)
	}
	for _, bad := range []string{`{"notification_id":"7/../8"}`, `{"notification_id":"x"}`, `{"notification_id":""}`} {
		if _, err := runAccount(invokeNotificationsDismiss, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestNotificationsDismissUnknownIDChangesNothing(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) { return status(404), nil })
	_, err := runAccount(invokeNotificationsDismiss, `{"notification_id":"99"}`)
	if err == nil || !strings.Contains(err.Error(), "no such notification") || strings.Contains(err.Error(), bodyCanary) ||
		len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
		t.Errorf("err = %v, calls = %+v", err, *calls)
	}
}

func TestNotificationsDismissOutcomes(t *testing.T) {
	for name, tc := range map[string]struct {
		answer  func() (*http.Response, error)
		uncertn bool
	}{
		"500":     {func() (*http.Response, error) { return status(500), nil }, true},
		"timeout": {func() (*http.Response, error) { return nil, timeoutError{} }, true},
		"abort":   {func() (*http.Response, error) { return nil, errors.New("connection reset") }, true},
		"garbage": {func() (*http.Response, error) { return ocsResponse(200, "not json"), nil }, true},
		"403":     {func() (*http.Response, error) { return status(403), nil }, false},
		"302":     {func() (*http.Response, error) { return status(302), nil }, false},
	} {
		t.Run(name, func(t *testing.T) {
			deletes := 0
			serve(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodDelete {
					deletes++
					return tc.answer()
				}
				return ocsResponse(200, ocsData(`[]`)), nil
			})
			_, err := runAccount(invokeNotificationsDismiss, `{"notification_id":"7"}`)
			if err == nil || deletes != 1 || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), redirectTarget) ||
				strings.Contains(err.Error(), "check account.notifications.list before repeating") != tc.uncertn {
				t.Errorf("err = %v, deletes = %d", err, deletes)
			}
			if name == "302" && !strings.Contains(err.Error(), "redirect") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestNotificationsDismissAllSendsOneDelete(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) { return ocsResponse(200, ocsData(`[]`)), nil })
	got, err := runAccount(invokeNotificationsDismissAll, `{}`)
	if err != nil || got.(map[string]any)["dismissed_all"] != true || len(*calls) != 1 || (*calls)[0].method != http.MethodDelete ||
		!strings.HasSuffix((*calls)[0].url.Path, "/api/v2/notifications") {
		t.Errorf("got = %+v, err = %v, calls = %+v", got, err, *calls)
	}
	serve(t, func(r *http.Request) (*http.Response, error) { return status(503), nil })
	if _, err := runAccount(invokeNotificationsDismissAll, `{}`); err == nil || !strings.Contains(err.Error(), "check account.notifications.list before repeating") {
		t.Errorf("err = %v", err)
	}
}

func TestNotificationsAppOffIsAClearError(t *testing.T) {
	serve(t, func(r *http.Request) (*http.Response, error) { return status(404), nil })
	_, err := runAccount(invokeNotificationsList, `{}`)
	if err == nil || !strings.Contains(err.Error(), "Notifications app is off") || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}
