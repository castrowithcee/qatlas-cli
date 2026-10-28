package nextcloud

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// feedRoutes is what the fake instance answers for the account-wide feeds and the reads behind them.
type feedRoutes struct {
	activity, notifications string
	notification            map[string]string // id -> body of one notification
	remote                  string
	rooms                   map[string]string // token -> state
	files                   map[string]string // file ID -> href; absent resolves to nothing
	filesStatus             int
	notes                   map[string]string // id -> category; absent fails
	notesPath               string
	settingsStatus          int
	search                  map[string]string // provider id -> data of its search answer
}

func feedServer(t *testing.T, c *Client, f feedRoutes) *[]call {
	t.Helper()
	var calls *[]call
	calls = serve(t, func(r *http.Request) (*http.Response, error) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/search/providers"):
			var list []string
			for id := range f.search {
				list = append(list, `{"id":"`+id+`","name":"x"}`)
			}
			return ocsResponse(200, ocsData("["+strings.Join(list, ",")+"]")), nil
		case strings.Contains(p, "/search/providers/"):
			id := strings.TrimSuffix(p[strings.Index(p, "/search/providers/")+len("/search/providers/"):], "/search")
			return ocsResponse(200, ocsData(f.search[id])), nil
		case strings.HasSuffix(p, "/activity/filters"):
			return ocsResponse(200, ocsData(`[]`)), nil
		case strings.HasSuffix(p, "/api/v2/activity") || strings.HasSuffix(p, "/activity/filter"):
			resp := ocsResponse(200, ocsData(f.activity))
			resp.Header.Set("X-Activity-Last-Given", "29")
			return resp, nil
		case strings.HasSuffix(p, "/api/v2/notifications") && r.Method == http.MethodGet:
			resp := ocsResponse(200, ocsData(f.notifications))
			resp.Header.Set("ETag", `"srv1"`)
			return resp, nil
		case strings.Contains(p, "/api/v2/notifications/") && r.Method == http.MethodGet:
			return ocsResponse(200, ocsData(f.notification[p[strings.LastIndex(p, "/")+1:]])), nil
		case strings.Contains(p, "/api/v2/notifications"):
			return ocsResponse(200, ocsData(`[]`)), nil
		case p == remoteBase:
			return ocsResponse(200, ocsData(f.remote)), nil
		case strings.HasPrefix(p, remoteBase+"/"):
			if r.Method == http.MethodGet {
				for _, id := range []string{"5", "6", "7", "8"} {
					if p == remoteBase+"/"+id {
						return ocsResponse(200, ocsData(remoteByID(f.remote, id))), nil
					}
				}
				return status(404), nil
			}
			return ocsResponse(200, ocsData(`[]`)), nil
		case strings.HasPrefix(p, talkRoomsURL+"/"):
			if state, ok := f.rooms[strings.TrimPrefix(p, talkRoomsURL+"/")]; ok {
				return ocsResponse(200, ocsData(state)), nil
			}
			return status(404), nil
		case r.Method == methodSearch:
			if f.filesStatus != 0 {
				return status(f.filesStatus), nil
			}
			body := (*calls)[len(*calls)-1].body
			for id, href := range f.files {
				if strings.Contains(body, `<d:literal>`+id+`</d:literal>`) {
					return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(href, "x.txt", id, "1"))), nil
				}
			}
			return xmlResponse(http.StatusMultiStatus, multistatus()), nil
		case strings.HasPrefix(p, notesBase):
			rest := strings.TrimPrefix(p, notesBase)
			if rest == "settings" {
				if f.settingsStatus != 0 {
					return noteResponse(f.settingsStatus, nil, "{}"), nil
				}
				return noteResponse(200, nil, `{"notesPath":`+jsonString(f.notesPath)+`}`), nil
			}
			id := strings.TrimPrefix(rest, "notes/")
			if category, ok := f.notes[id]; ok {
				var n int
				_, _ = fmt.Sscan(id, &n)
				return noteResponse(200, nil, noteJSON(n, category, "")), nil
			}
			return noteResponse(500, nil, "{}"), nil
		}
		t.Errorf("unexpected request %s %s", r.Method, p)
		return status(500), nil
	})
	c.http = provider.NoRedirectClient(defaultTimeout, transport)
	return calls
}

// remoteByID picks one object out of a list body made of the remoteJSON items.
func remoteByID(list, id string) string {
	for _, item := range strings.Split(strings.Trim(list, "[]"), `},{`) {
		item = strings.TrimPrefix(strings.TrimSuffix(item, "}"), "{")
		if strings.Contains(item, `"id":"`+id+`"`) {
			return "{" + item + "}"
		}
	}
	return "{}"
}

// feedClient is a client whose scope is built from targets and exclusions, with the account target added.
func feedClient(t *testing.T, targets []string, excludes ...string) *Client {
	t.Helper()
	c, _ := client(t)
	s, err := parseScopeExcluding(append(slices.Clone(targets), "account"), excludes)
	if err != nil {
		t.Fatal(err)
	}
	c.scope = s
	return c
}

func act(id int, extra string) string {
	return fmt.Sprintf(`{"activity_id":%d,"datetime":"2026-10-01T08:00:00+00:00","app":"files","subject":"s"%s}`, id, extra)
}

func activityIDs(list *ActivityList) string {
	var ids []string
	for _, a := range list.Activities {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ",")
}

func richFile(path, id string) string {
	return `,"subject_rich":["{f}",{"f":{"type":"file","id":"` + id + `","name":"n","path":"` + path + `"}}]`
}

func richOf(kind, id string) string {
	return `,"message_rich":{"0":"{o}","1":{"o":{"type":"` + kind + `","id":"` + id + `","name":"n"}}}`
}

const (
	feedTalkExcluded = "tokx1234"
	feedTalkOpen     = "open0001"
	feedTalkBreakout = "brk00001"
)

func TestActivityDropsEntriesThatNameExcludedObjects(t *testing.T) {
	c := feedClient(t, []string{"folder", "calendar", "deck", "talk"},
		"folder/Private", "calendar/SecretCal", "deck/7", "talk/"+feedTalkExcluded)
	entries := []string{
		act(1, `,"object_type":"files","object_id":12,"object_name":"/Private/a.txt","objects":{"12":"/Private/a.txt"}`),
		act(2, `,"object_type":"files","object_id":13,"object_name":"/Open/b.txt","objects":{"13":"/Open/b.txt"}`),
		act(3, richFile("Private/x/y.txt", "")),
		act(4, richOf("calendar", "secretcal")),
		act(5, `,"app":"deck","object_type":"deck_board","object_id":"007"`),
		act(6, `,"app":"deck","link":"https://x.example/apps/deck/#/board/7/card/9"`),
		act(7, `,"app":"deck","object_type":"deck_card","object_id":"9"`),
		act(8, richOf("call", strings.ToUpper(feedTalkExcluded))),
		act(9, richOf("call", feedTalkOpen)),
		act(10, richOf("call", feedTalkBreakout)),
		act(11, richFile("", "99")),
		act(12, richFile("", "98")),
		act(13, richFile("", "97")),
		act(14, richFile("", "99")),
		act(15, `,"app":"deck","object_type":"deck_card","object_id":"9","objects":{"9":"c"},"subject_rich":["{b}",{"b":{"type":"deck-board","id":"3","name":"b"}}]`),
		act(16, `,"app":"calendar","link":"https://x.example/remote.php/dav/calendars/alice/SECRETCAL/e.ics"`),
		act(17, ``),
		act(18, `,"link":"https://x.example/apps/files/?dir=/Private/sub&fileid=5"`),
	}
	calls := feedServer(t, c, feedRoutes{
		activity: "[" + strings.Join(entries, ",") + "]",
		rooms: map[string]string{
			feedTalkOpen:     `{"token":"` + feedTalkOpen + `","type":2}`,
			feedTalkBreakout: `{"token":"` + feedTalkBreakout + `","type":2,"objectType":"room","objectId":"TOKX1234"}`,
		},
		files: map[string]string{"99": "/remote.php/dav/files/alice/Private/a.txt", "98": "/remote.php/dav/files/alice/Open/a.txt"},
	})
	got, err := c.ListActivity(context.Background(), activityQuery{Filter: activityAll}, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "2,9,12,15,17"; activityIDs(got) != want || got.Count != 5 {
		t.Errorf("ids = %s count = %d, want %s", activityIDs(got), got.Count, want)
	}
	searches := 0
	for _, call := range *calls {
		if call.method == methodSearch {
			searches++
		}
	}
	if searches != 3 {
		t.Errorf("file reads = %d, want one per distinct file ID", searches)
	}
}

func TestActivityPageCursorIsTheServersAndCountsOnlyKeptEntries(t *testing.T) {
	c := feedClient(t, []string{"folder"}, "folder/Private")
	hidden := act(1, `,"object_type":"files","object_id":1,"object_name":"/Private/a.txt"`)
	feedServer(t, c, feedRoutes{activity: "[" + hidden + "," + act(2, ``) + "]"})
	got, err := c.ListActivity(context.Background(), activityQuery{Filter: activityAll}, 2, "")
	if err != nil || got.Count != 1 || got.NextCursor != "29" {
		t.Errorf("got = %+v, err = %v", got, err)
	}
	feedServer(t, c, feedRoutes{activity: "[" + hidden + "," + hidden + "]"})
	got, err = c.ListActivity(context.Background(), activityQuery{Filter: activityAll}, 2, "")
	if err != nil || got.Count != 0 || got.NextCursor != "29" {
		t.Errorf("a page of hidden entries = %+v, err = %v", got, err)
	}
}

func TestActivityObjectFilterOnAnExcludedObjectIsMissingWithoutIO(t *testing.T) {
	c := feedClient(t, []string{"folder", "calendar", "deck", "talk"},
		"folder/Private", "calendar/secretcal", "deck/7", "talk/"+feedTalkExcluded)
	for _, q := range []activityQuery{
		{Filter: activityAll, ObjectType: "deck_board", ObjectID: "07"},
		{Filter: activityAll, ObjectType: "calendar", ObjectID: "SecretCal"},
		{Filter: activityAll, ObjectType: "chat", ObjectID: "TOKX1234"},
	} {
		refuse(t)
		c.http = provider.NoRedirectClient(defaultTimeout, transport)
		_, err := c.ListActivity(context.Background(), q, 25, "")
		perr := providerFailure(t, err)
		if perr.Class != provider.ClassNotFound || perr.Original == nil || perr.Original.Status != http.StatusNotFound ||
			strings.Contains(strings.ToLower(err.Error()), strings.ToLower(q.ObjectID)) {
			t.Errorf("%+v: err = %v", q, err)
		}
		if !c.scope.activityObjectHidden(q.ObjectType, q.ObjectID) {
			t.Errorf("%+v not recognised locally", q)
		}
	}
	// An object that names nothing excluded is read, and every row is still checked.
	feedServer(t, c, feedRoutes{activity: "[" + act(1, `,"object_type":"files","object_id":1,"object_name":"/Private/a.txt"`) + "," + act(2, ``) + "]"})
	got, err := c.ListActivity(context.Background(), activityQuery{Filter: activityAll, ObjectType: "files", ObjectID: "1"}, 25, "")
	if err != nil || activityIDs(got) != "2" {
		t.Errorf("got = %+v, err = %v", got, err)
	}
}

func TestActivityDropsEntriesWhoseEvidenceReadFails(t *testing.T) {
	c := feedClient(t, []string{"folder", "notes"}, "notes/Work/Secret")
	c.scope.cross = c.scope.derive([]string{"Notes"})
	entries := []string{
		act(1, `,"link":"https://x.example/apps/notes/note/5"`),
		act(2, `,"link":"https://x.example/apps/notes/note/6"`),
		act(3, `,"link":"https://x.example/apps/notes/note/7"`),
		act(4, `,"object_type":"files","object_id":1,"object_name":"/Notes/work/SECRET/x.md"`),
		act(5, `,"object_type":"files","object_id":1,"object_name":"/Notes/Work/open.md"`),
		act(6, richFile("", "55")),
	}
	feedServer(t, c, feedRoutes{
		activity:    "[" + strings.Join(entries, ",") + "]",
		notes:       map[string]string{"6": "Work/Secret/deep", "7": "Other"},
		files:       map[string]string{"55": "/remote.php/dav/files/alice/Open/a.txt"},
		filesStatus: 500,
	})
	got, err := c.ListActivity(context.Background(), activityQuery{Filter: activityAll}, 25, "")
	if err != nil || activityIDs(got) != "3,5" {
		t.Errorf("ids = %v, err = %v", got, err)
	}
}

func TestFeedFailsClosedWhenTheNotesPathCannotBeRead(t *testing.T) {
	c := feedClient(t, []string{"folder", "notes"}, "notes/Work/Secret")
	calls := feedServer(t, c, feedRoutes{settingsStatus: 500})
	err := c.deriveCross(context.Background())
	if perr := providerFailure(t, err); perr.Class != provider.ClassPermission {
		t.Errorf("err = %v", err)
	}
	for _, call := range *calls {
		if !strings.HasPrefix(call.url.Path, notesBase+"settings") {
			t.Errorf("request beyond the settings read: %s", call.url.Path)
		}
	}
}

const feedNotifications = `[
{"notification_id":1,"app":"files_sharing","subject":"a","object_type":"share","object_id":"3","subjectRich":"{f}","subjectRichParameters":{"f":{"type":"file","id":"12","name":"a","path":"Private/a.txt"}}},
{"notification_id":2,"app":"files_sharing","subject":"b","subjectRich":"{f}","subjectRichParameters":{"f":{"type":"file","id":"13","name":"b","path":"Open/b.txt"}}},
{"notification_id":3,"app":"spreed","subject":"c","object_type":"chat","object_id":"tokx1234"},
{"notification_id":4,"app":"deck","subject":"d","object_type":"deck_card","object_id":"9","messageRichParameters":{"b":{"type":"deck-board","id":"7","name":"b"}}},
{"notification_id":5,"app":"x","subject":"nothing placeable"}]`

func TestNotificationsDropEntriesThatNameExcludedObjectsAndKeepTheETag(t *testing.T) {
	c := feedClient(t, []string{"folder", "deck", "talk"}, "folder/Private", "deck/7", "talk/"+feedTalkExcluded)
	feedServer(t, c, feedRoutes{notifications: feedNotifications})
	got, err := c.ListNotifications(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range got.Notifications {
		ids = append(ids, n.ID)
	}
	if !slices.Equal(ids, []string{"2", "5"}) || got.Count != 2 || got.ETag != `"srv1"` {
		t.Errorf("ids = %v, got = %+v", ids, got)
	}
}

func TestNotificationsDismissOfAHiddenOneIsMissingWithoutDelete(t *testing.T) {
	c := feedClient(t, []string{"folder"}, "folder/Private")
	calls := feedServer(t, c, feedRoutes{notification: map[string]string{
		"1": `{"notification_id":1,"app":"x","subject":"a","subjectRichParameters":{"f":{"type":"file","path":"Private/a.txt"}}}`,
		"2": `{"notification_id":2,"app":"x","subject":"b"}`,
	}})
	_, err := c.DismissNotification(context.Background(), "1")
	perr := providerFailure(t, err)
	if perr.Class != provider.ClassNotFound || perr.Message != messageNoNotification || strings.Contains(err.Error(), "Private") {
		t.Errorf("err = %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
		t.Errorf("calls = %+v", *calls)
	}
	if _, err := c.DismissNotification(context.Background(), "2"); err != nil {
		t.Errorf("a visible notification = %v", err)
	}
	if last := (*calls)[len(*calls)-1]; last.method != http.MethodDelete {
		t.Errorf("last call = %s", last.method)
	}
	*calls = nil
	if _, err := c.DismissAllNotifications(context.Background()); err != nil || len(*calls) != 1 || (*calls)[0].method != http.MethodDelete {
		t.Errorf("dismiss-all = %v, calls = %+v", err, *calls)
	}
}

func remoteList() string {
	return `[` + remoteJSON("5", "https://192.0.2.7", "/Shared/Private") + `,` +
		remoteJSON("6", "https://192.0.2.8", "/Shared/Private/deep") + `,` +
		remoteJSON("7", "https://192.0.2.9", "/Shared") + `,` +
		remoteJSON("8", "https://192.0.2.10", "/Shared/Open") + `]`
}

func TestRemoteSharesHideMountsOnOrBelowAnExcludedFolder(t *testing.T) {
	c := feedClient(t, []string{"folder"}, "folder/shared/private")
	calls := feedServer(t, c, feedRoutes{remote: remoteList()})
	got, err := c.ListRemoteShares(context.Background())
	if err != nil || got.Count != 1 || got.Shares[0].ID != "8" {
		t.Fatalf("list = %+v, err = %v", got, err)
	}
	for _, id := range []string{"5", "6"} {
		_, err := c.GetRemoteShare(context.Background(), "get remote share", id)
		perr := providerFailure(t, err)
		if perr.Class != provider.ClassNotFound || perr.Message != messageRemoteShareNotFound || strings.Contains(err.Error(), "rivate") {
			t.Errorf("get %s = %v", id, err)
		}
		*calls = nil
		if _, err := c.RemoveRemoteShare(context.Background(), id); providerFailure(t, err).Class != provider.ClassNotFound {
			t.Errorf("remove %s = %v", id, err)
		}
		for _, call := range *calls {
			if call.method == http.MethodDelete {
				t.Errorf("remove %s sent a DELETE", id)
			}
		}
	}
	// A mount above the exclusion is hidden too, but removing it is the revocation.
	if _, err := c.GetRemoteShare(context.Background(), "get remote share", "7"); providerFailure(t, err).Class != provider.ClassNotFound {
		t.Errorf("get of a mount above the exclusion = %v", err)
	}
	*calls = nil
	if _, err := c.RemoveRemoteShare(context.Background(), "7"); err != nil {
		t.Fatal(err)
	}
	if last := (*calls)[len(*calls)-1]; last.method != http.MethodDelete || last.url.Path != remoteBase+"/7" {
		t.Errorf("last call = %s %s", last.method, last.url.Path)
	}
}

func TestRemoteSharesFollowNotesExclusionsThroughTheNotesPath(t *testing.T) {
	c := feedClient(t, []string{"folder", "notes"}, "notes/Work")
	feedServer(t, c, feedRoutes{notesPath: "Notes", remote: `[` + remoteJSON("5", "https://192.0.2.7", "/Notes/work/x") + `,` +
		remoteJSON("7", "https://192.0.2.9", "/Notes/Open") + `]`})
	if err := c.deriveCross(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := c.ListRemoteShares(context.Background())
	if err != nil || got.Count != 1 || got.Shares[0].ID != "7" {
		t.Errorf("list = %+v, err = %v", got, err)
	}
}
