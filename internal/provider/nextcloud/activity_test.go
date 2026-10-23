package nextcloud

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const activityJSON = `[{"activity_id":30,"datetime":"2026-10-01T08:00:00+00:00","app":"files","type":"file_changed","user":"bob",
"subject":"You changed a.txt","subject_rich":["You changed {file}",{"file":{"type":"file","id":12,"name":"a.txt","path":"/a.txt","link":"` + linkCanary + `"}}],
"message":"m","message_rich":{"0":"{share}","1":{"share":{"type":"share","id":"3","name":"Link","link":"` + shareCanary + `"}}},
"icon":"` + linkCanary + `","link":"` + linkCanary + `","object_type":"files","object_id":12,"object_name":"/a.txt",
"objects":{"12":"/a.txt","13":"/b.txt"},
"previews":[{"source":"` + linkCanary + `/p","link":"` + shareCanary + `","mimeType":"text/plain","view":"files","fileId":12,"isMimeTypeIcon":false,"filename":"a.txt"}]},
{"activity_id":29,"datetime":"bad","app":"files","subject":"older","subject_rich":["",[]],"objects":[],"previews":[]}]`

func activityServer(t *testing.T, filters string, list func() *http.Response) *[]call {
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/activity/filters") {
			return ocsResponse(200, ocsData(filters)), nil
		}
		return list(), nil
	})
}

func TestActivityListReportsEveryDocumentedFieldAndPaging(t *testing.T) {
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
	first := list.Activities[0]
	yes := int64(12)
	no := false
	if first.User != "bob" || first.ObjectName != "/a.txt" || first.Link != linkCanary || first.Icon != linkCanary ||
		first.SubjectRich != "You changed {file}" || first.MessageRich != "{share}" ||
		!reflect.DeepEqual(first.Objects, map[string]string{"12": "/a.txt", "13": "/b.txt"}) ||
		!reflect.DeepEqual(first.Previews, []ActivityPreview{{Source: linkCanary + "/p", MimeType: "text/plain", View: "files",
			FileID: &yes, IsMimeTypeIcon: &no, Filename: "a.txt"}}) ||
		!reflect.DeepEqual(first.SubjectObjects, []MessageObject{{Key: "file", Type: "file", Name: "a.txt",
			Details: map[string]any{"link": linkCanary, "path": "/a.txt"}}}) ||
		len(first.MessageObjects) != 1 || first.MessageObjects[0].Details != nil {
		t.Errorf("activity = %+v", first)
	}
	if list.Activities[1].SubjectRich != "" || list.Activities[1].Objects != nil || list.Activities[1].Previews != nil {
		t.Errorf("empty members reported: %+v", list.Activities[1])
	}
	out := marshal(got)
	if strings.Contains(out, "tok-canary") {
		t.Errorf("output carries an access value: %s", out)
	}
	checkDeclared(t, activityList, []byte(out), activityList.OutputSchema)
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
	*calls = nil
	if _, err := runAccount(invokeActivityList, `{"filter":"custom-app_1"}`); err == nil || !strings.Contains(err.Error(), "does not offer") || len(*calls) != 1 {
		t.Errorf("free filter name: %v, calls = %d", err, len(*calls))
	}
	refuse(t)
	for _, bad := range []string{`{"filter":"../x"}`, `{"filter":"Files"}`, `{"limit":101}`, `{"limit":0}`, `{"since":"1a"}`, `{"since":"1234567890123456789"}`} {
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

func TestActivityFiltersReachTheRequest(t *testing.T) {
	calls := activityServer(t, `[{"id":"files"}]`, func() *http.Response { return ocsResponse(200, ocsData(`[]`)) })
	_, err := runAccount(invokeActivityList, `{"sort":"asc","search":"rep","from":10,"to":20,"actor":"bob"}`)
	if err != nil {
		t.Fatal(err)
	}
	q := (*calls)[0].url.Query()
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0].url.Path, "/api/v2/activity") || q.Get("sort") != "asc" ||
		q.Get("search") != "rep" || q.Get("from") != "10" || q.Get("to") != "20" || q.Get("actor") != "bob" || q.Has("object_type") {
		t.Errorf("calls = %+v", *calls)
	}
	*calls = nil
	if _, err := runAccount(invokeActivityList, `{"filter":"files","search":"rep"}`); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.HasSuffix((*calls)[0].url.Path, "/activity/filters") || !strings.HasSuffix((*calls)[1].url.Path, "/activity/files") ||
		(*calls)[1].url.Query().Get("search") != "rep" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestActivityObjectFilterUsesTheFixedPathWithoutFilterRead(t *testing.T) {
	calls := activityServer(t, `[]`, func() *http.Response { return ocsResponse(200, ocsData(`[]`)) })
	if _, err := runAccount(invokeActivityList, `{"object_type":"files","object_id":"12"}`); err != nil {
		t.Fatal(err)
	}
	q := (*calls)[0].url.Query()
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0].url.Path, "/api/v2/activity/filter") ||
		q.Get("object_type") != "files" || q.Get("object_id") != "12" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestActivityRefusesUnusableCombinationsBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for _, bad := range []string{
		`{"object_type":"files"}`, `{"object_id":"12"}`, `{"object_type":"files","object_id":"12","filter":"files"}`,
		`{"object_type":"files","object_id":"12","filter":"all"}`, `{"object_type":"Files","object_id":"12"}`,
		`{"object_type":"files","object_id":"a/b"}`, `{"object_type":"files","object_id":"` + strings.Repeat("1", 65) + `"}`,
		`{"from":20,"to":10}`, `{"to":2147483648}`, `{"from":-1}`, `{"search":"a"}`, `{"search":"` + strings.Repeat("a", 256) + `"}`,
		`{"search":"a\u0001b"}`, `{"sort":"up"}`, `{"actor":"a/b"}`, `{"actor":"` + strings.Repeat("a", 65) + `"}`,
		`{"since":"~abc"}`, `{"since":"` + strings.Repeat("1", 19) + `"}`,
	} {
		if _, err := runAccount(invokeActivityList, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestActivityCursorBindsTheFilters(t *testing.T) {
	calls := activityServer(t, `[]`, func() *http.Response {
		resp := ocsResponse(200, ocsData(activityJSON))
		resp.Header.Set("X-Activity-Last-Given", "29")
		return resp
	})
	first, err := runAccount(invokeActivityList, `{"limit":2,"search":"rep","sort":"asc"}`)
	if err != nil {
		t.Fatal(err)
	}
	cursor := first.(*ActivityList).NextCursor
	if !strings.HasPrefix(cursor, "~") || len(cursor) > 64 {
		t.Fatalf("cursor = %q", cursor)
	}
	if _, err := runAccount(invokeActivityList, `{"limit":2,"search":"rep","sort":"asc","since":"`+cursor+`"}`); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[len(*calls)-1].url.Query().Get("since"); got != "29" {
		t.Errorf("since = %q", got)
	}
	before := len(*calls)
	for _, args := range []string{
		`{"search":"other","sort":"asc","since":"` + cursor + `"}`, `{"search":"rep","since":"` + cursor + `"}`,
		`{"since":"` + cursor + `"}`, `{"search":"rep","sort":"asc","since":"29"}`, `{"search":"rep","sort":"asc","since":"~` + cursor[2:] + `"}`,
	} {
		if _, err := runAccount(invokeActivityList, args); err == nil {
			t.Errorf("%s accepted", args)
		}
	}
	if len(*calls) != before {
		t.Errorf("a refused cursor sent %d requests", len(*calls)-before)
	}
	plain, err := runAccount(invokeActivityList, `{"limit":2,"filter":"all"}`)
	if err != nil || plain.(*ActivityList).NextCursor != "29" {
		t.Errorf("plain = %+v, %v", plain, err)
	}
}
