package nextcloud

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const statusJSON = `{"userId":"alice","message":"In a meeting","messageId":"meeting","messageIsPredefined":true,"icon":"x",
"clearAt":1893456000,"status":"dnd","statusIsUserDefined":true,"extra":"canary-extra"}`

func TestStatusGetReportsFixedFields(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsData(statusJSON)), nil })
	got, err := runAccount(invokeStatusGet, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	s := got.(*UserStatus)
	if s.Status != "dnd" || !s.StatusIsUserSet || s.Message != "In a meeting" || s.MessageID != "meeting" || !s.MessageIsPresetID ||
		s.ClearAt != "2030-01-01T00:00:00Z" || strings.Contains(marshal(got), "canary") || strings.Contains(marshal(got), "alice") {
		t.Errorf("status = %+v", s)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet || !strings.HasSuffix((*calls)[0].url.Path, "/ocs/v2.php/apps/user_status/api/v1/user_status") {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestStatusGetCutsAndDropsUnknownType(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`{"status":"weird","message":"`+strings.Repeat("m", 400)+`"}`)), nil
	})
	got, err := runAccount(invokeStatusGet, `{}`)
	s := got.(*UserStatus)
	if err != nil || s.Status != "" || len(s.Message) != maxStatusText || !s.Truncated {
		t.Errorf("status = %+v, err = %v", s, err)
	}
}

func TestStatusAppOffIsAClearError(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return status(404), nil })
	for _, h := range []tool{invokeStatusGet, invokeStatusClear} {
		_, err := runAccount(h, `{}`)
		if err == nil || !strings.Contains(err.Error(), "User status app is off") || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("err = %v", err)
		}
	}
}

func TestStatusSetSendsOneFixedRequestPerMode(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	for name, tc := range map[string]struct {
		args, path string
		form       url.Values
	}{
		"type":       {`{"type":"away"}`, "/user_status/status", url.Values{"statusType": {"away"}}},
		"predefined": {`{"predefined_id":"remote-work","expires":"` + future.Format(time.RFC3339) + `"}`, "/user_status/message/predefined", url.Values{"messageId": {"remote-work"}, "clearAt": {itoa(int(future.Unix()))}}},
		"custom":     {`{"message":"Fokus ✓","expires":"` + itoa(int(future.Unix())) + `"}`, "/user_status/message/custom", url.Values{"message": {"Fokus ✓"}, "clearAt": {itoa(int(future.Unix()))}}},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsData(statusJSON)), nil })
			got, err := runAccount(invokeStatusSet, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].method != http.MethodPut || !strings.HasSuffix((*calls)[0].url.Path, "/api/v1"+tc.path) {
				t.Fatalf("calls = %+v", *calls)
			}
			form, _ := url.ParseQuery((*calls)[0].body)
			if form.Encode() != tc.form.Encode() {
				t.Errorf("form = %v, want %v", form, tc.form)
			}
			if got.(map[string]any)["changed"] != true || strings.Contains(marshal(got), "canary") {
				t.Errorf("got = %s", marshal(got))
			}
		})
	}
}

func TestStatusSetRefusesLocallyBeforeSecretAccess(t *testing.T) {
	refuse(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for name, args := range map[string]string{
		"empty":           `{}`,
		"bad type":        `{"type":"busy"}`,
		"type and msg":    `{"type":"away","message":"x"}`,
		"msg and preset":  `{"predefined_id":"meeting","message":"x"}`,
		"too long":        `{"message":"` + strings.Repeat("ä", 81) + `"}`,
		"control":         `{"message":"a\nb"}`,
		"past":            `{"message":"x","expires":"` + past + `"}`,
		"past unix":       `{"message":"x","expires":"1000"}`,
		"garbage expires": `{"message":"x","expires":"tomorrow"}`,
		"expires on type": `{"type":"dnd","expires":"` + future + `"}`,
		"preset segment":  `{"predefined_id":"a/../b"}`,
		"preset upper":    `{"predefined_id":"Meeting"}`,
	} {
		if _, err := runAccount(invokeStatusSet, args); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestStatusChangeOutcomes(t *testing.T) {
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
		for _, tl := range []struct {
			h    tool
			args string
		}{{invokeStatusSet, `{"type":"dnd"}`}, {invokeStatusClear, `{}`}} {
			t.Run(name, func(t *testing.T) {
				requests := 0
				serve(t, func(*http.Request) (*http.Response, error) { requests++; return tc.answer() })
				_, err := runAccount(tl.h, tl.args)
				if err == nil || requests != 1 || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), redirectTarget) ||
					strings.Contains(err.Error(), "check account.status.get before repeating") != tc.uncertn {
					t.Errorf("err = %v, requests = %d", err, requests)
				}
				if name == "302" && !strings.Contains(err.Error(), "redirect") {
					t.Errorf("err = %v", err)
				}
			})
		}
	}
}

func TestStatusClearSendsOneDelete(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsData(`[]`)), nil })
	got, err := runAccount(invokeStatusClear, `{}`)
	if err != nil || got.(map[string]any)["cleared"] != true || len(*calls) != 1 || (*calls)[0].method != http.MethodDelete ||
		!strings.HasSuffix((*calls)[0].url.Path, "/user_status/message") {
		t.Errorf("got = %+v, err = %v, calls = %+v", got, err, *calls)
	}
}
