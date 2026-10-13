package nextcloud

import (
	"net/http"
	"strings"
	"testing"
)

const activityJSON = `[{"activity_id":30,"datetime":"2026-10-01T08:00:00+00:00","app":"files","type":"file_changed","user":"bob",
"subject":"You changed a.txt","subject_rich":["You changed {file}",{"file":{"type":"file","link":"` + linkCanary + `"}}],"message":"m",
"icon":"` + linkCanary + `","link":"` + linkCanary + `","object_type":"files","object_id":12,"object_name":"/a.txt","previews":[{"source":"` + linkCanary + `"}]},
{"activity_id":29,"datetime":"bad","app":"files","subject":"older"}]`

func activityServer(t *testing.T, filters string, list func() *http.Response) *[]call {
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/activity/filters") {
			return ocsResponse(200, ocsData(filters)), nil
		}
		return list(), nil
	})
}

func TestActivityListPagesWithLastGivenAndHidesLinks(t *testing.T) {
	calls := activityServer(t, `[]`, func() *http.Response {
		resp := ocsResponse(200, ocsData(activityJSON))
		resp.Header.Set("X-Activity-Last-Given", "29")
		return resp
	})
	got, err := runAccount(invokeActivityList, `{"limit":2,"since":"31"}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*ActivityList)
	if list.Count != 2 || list.NextCursor != "29" || list.Activities[0].ID != "30" || list.Activities[0].ObjectID != "12" ||
		list.Activities[0].Time != "2026-10-01T08:00:00Z" || list.Activities[1].Time != "" {
		t.Fatalf("list = %+v", list)
	}
	out := marshal(got)
	if strings.Contains(out, "canary") || strings.Contains(out, "http") {
		t.Errorf("output carries a URL: %s", out)
	}
	q := (*calls)[0].url.Query()
	if len(*calls) != 1 || q.Get("since") != "31" || q.Get("limit") != "2" || !strings.HasSuffix((*calls)[0].url.Path, "/api/v2/activity") {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestActivityListLastPageHasNoCursorAndEmptyAnswers(t *testing.T) {
	code := 200
	activityServer(t, `[]`, func() *http.Response {
		if code == 200 {
			resp := ocsResponse(200, ocsData(activityJSON))
			resp.Header.Set("X-Activity-Last-Given", "29")
			return resp
		}
		return ocsResponse(code, "")
	})
	got, _ := runAccount(invokeActivityList, `{"limit":50}`)
	if got.(*ActivityList).NextCursor != "" {
		t.Errorf("cursor on a short page: %+v", got)
	}
	for _, code = range []int{304, 204} {
		got, err := runAccount(invokeActivityList, `{}`)
		if err != nil || got.(*ActivityList).Count != 0 || got.(*ActivityList).NextCursor != "" {
			t.Errorf("%d: %+v, %v", code, got, err)
		}
	}
}

func TestActivityFilterIsCheckedLocallyAndLive(t *testing.T) {
	calls := activityServer(t, `[{"id":"all"},{"id":"files","name":"Files"}]`, func() *http.Response {
		return ocsResponse(200, ocsData(`[]`))
	})
	if _, err := runAccount(invokeActivityList, `{"filter":"files"}`); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.HasSuffix((*calls)[0].url.Path, "/activity/filters") || !strings.HasSuffix((*calls)[1].url.Path, "/activity/files") {
		t.Errorf("calls = %+v", *calls)
	}
	*calls = nil
	if _, err := runAccount(invokeActivityList, `{"filter":"shares"}`); err == nil || !strings.Contains(err.Error(), "does not offer") || len(*calls) != 1 {
		t.Errorf("not offered: %v, calls = %d", err, len(*calls))
	}
	refuse(t)
	for _, bad := range []string{`{"filter":"../x"}`, `{"filter":"nope"}`, `{"limit":101}`, `{"limit":0}`, `{"since":"1a"}`, `{"since":"1234567890123456789"}`} {
		if _, err := runAccount(invokeActivityList, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestActivityRedirectAndAppOff(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return status(302), nil })
	if _, err := runAccount(invokeActivityList, `{}`); err == nil || !strings.Contains(err.Error(), "redirect") || strings.Contains(err.Error(), redirectTarget) {
		t.Errorf("err = %v", err)
	}
	serve(t, func(*http.Request) (*http.Response, error) { return status(404), nil })
	if _, err := runAccount(invokeActivityList, `{}`); err == nil || !strings.Contains(err.Error(), "Activity app is off") || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}
