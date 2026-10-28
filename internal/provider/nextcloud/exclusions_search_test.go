package nextcloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func searchHit(title, link, attributes string) string {
	if attributes == "" {
		attributes = "{}"
	}
	return `{"title":"` + title + `","subline":"s","resourceUrl":"` + link + `","attributes":` + attributes + `}`
}

func searchPage(cursor string, hits ...string) string {
	return `{"name":"x","isPaginated":true,"cursor":"` + cursor + `","entries":[` + strings.Join(hits, ",") + `]}`
}

func searchTitles(r *SearchResult) string {
	var titles []string
	for _, e := range r.Entries {
		titles = append(titles, e.Title)
	}
	return strings.Join(titles, ",")
}

func TestSearchDropsHitsThatNameExcludedObjects(t *testing.T) {
	c := feedClient(t, []string{"folder", "calendar", "addressbook", "deck", "talk"},
		"folder/Private", "calendar/SecretCal", "addressbook/SecretBook", "deck/7", "talk/"+feedTalkExcluded)
	davLink := base64.RawURLEncoding.EncodeToString([]byte("/remote.php/dav/calendars/alice/secretcal/e.ics"))
	hits := []string{
		searchHit("f1", "/index.php/apps/files/?dir=/Private/sub&scrollto=a.txt&openfile=12", `{"path":"/Private/sub/a.txt","fileId":"12"}`),
		searchHit("f2", "/index.php/apps/files/?dir=/Open&scrollto=b.txt&openfile=13", `{"path":"/Open/b.txt","fileId":"13"}`),
		searchHit("f3", "/index.php/apps/files/?openfile=99", ``),
		searchHit("f4", "/index.php/apps/files/?openfile=98", `{"fileId":"98"}`),
		searchHit("n1", "/index.php/apps/deck/#/board/7/card/9", ``),
		searchHit("n2", "/index.php/apps/deck/#/board/3/card/9", ``),
		searchHit("t1", "/index.php/call/"+feedTalkExcluded, ``),
		searchHit("t2", "/index.php/call/"+feedTalkOpen+"#message_5", `{"conversation":"`+feedTalkOpen+`"}`),
		searchHit("t3", "/index.php/call/"+feedTalkBreakout, ``),
		searchHit("c1", "/index.php/apps/calendar/edit/"+davLink, ``),
		searchHit("c2", "/index.php/apps/calendar/edit/"+base64.RawURLEncoding.EncodeToString([]byte("/remote.php/dav/calendars/alice/personal/e.ics")), ``),
		searchHit("k1", "/index.php/apps/contacts/direct/contact/uid1~secretbook", ``),
		searchHit("k2", "/index.php/apps/contacts/direct/contact/uid2~contacts", ``),
		searchHit("u1", "https://x.example/other", ``),
		searchHit("u2", "", `{"x":"y"}`),
	}
	feedServer(t, c, feedRoutes{
		search: map[string]string{"files": searchPage("next1", hits...)},
		rooms: map[string]string{
			feedTalkOpen:     `{"token":"` + feedTalkOpen + `","type":2}`,
			feedTalkBreakout: `{"token":"` + feedTalkBreakout + `","type":2,"objectType":"room","objectId":"TOKX1234"}`,
		},
		files: map[string]string{"99": "/remote.php/dav/files/alice/Private/a.txt", "98": "/remote.php/dav/files/alice/Open/a.txt"},
	})
	got, err := c.Search(context.Background(), "files", "term", 25, "", searchFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "f2,f4,n2,t2,c2,k2,u1,u2"; searchTitles(got) != want || got.Count != 8 {
		t.Errorf("titles = %s count = %d, want %s", searchTitles(got), got.Count, want)
	}
	if got.NextCursor != "next1" {
		t.Errorf("next cursor = %q, want the server's", got.NextCursor)
	}
}

func TestSearchDropsFileAndNoteHitsByEvidence(t *testing.T) {
	c := feedClient(t, []string{"folder", "notes"}, "notes/Work/Secret")
	feedServer(t, c, feedRoutes{
		notesPath: "Notes",
		search: map[string]string{"files": searchPage("",
			searchHit("a", "/index.php/apps/files/?dir=/Notes/work/secret", `{"path":"/Notes/Work/Secret/x.md"}`),
			searchHit("b", "/index.php/apps/files/?dir=/Notes/Work", `{"path":"/Notes/Work/open.md"}`),
			searchHit("c", "/index.php/apps/notes/note/5", ``),
			searchHit("d", "/index.php/apps/notes/note/6", ``),
			searchHit("e", "/index.php/apps/notes/note/7", ``),
		)},
		notes: map[string]string{"5": "Work/Secret/deep", "6": "Other"},
	})
	if err := c.deriveCross(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := c.Search(context.Background(), "files", "term", 25, "", searchFilters{})
	if err != nil || searchTitles(got) != "b,d" || got.NextCursor != "" {
		t.Errorf("got = %+v, err = %v", got, err)
	}
}

func TestSearchDropsHitsWhoseEvidenceReadFails(t *testing.T) {
	c := feedClient(t, []string{"folder", "notes", "talk"}, "folder/Private", "notes/Secret", "talk/"+feedTalkExcluded)
	feedServer(t, c, feedRoutes{
		notesPath: "Notes", filesStatus: 500,
		search: map[string]string{"files": searchPage("",
			searchHit("file", "/index.php/apps/files/?openfile=55", ``),
			searchHit("note", "/index.php/apps/notes/note/8", ``),
			searchHit("room", "/index.php/call/"+feedTalkOpen, ``),
			searchHit("none", "https://x.example/", ``),
		)},
	})
	got, err := c.Search(context.Background(), "files", "term", 25, "", searchFilters{})
	if err != nil || searchTitles(got) != "none" {
		t.Errorf("got = %+v, err = %v", got, err)
	}
}

func TestSearchWithoutExclusionsReadsNothingMore(t *testing.T) {
	c := feedClient(t, []string{"folder"})
	calls := feedServer(t, c, feedRoutes{search: map[string]string{"files": searchPage("",
		searchHit("a", "/index.php/apps/files/?openfile=55", ``))}})
	got, err := c.Search(context.Background(), "files", "term", 25, "", searchFilters{})
	if err != nil || got.Count != 1 || len(*calls) != 2 {
		t.Errorf("got = %+v, err = %v, calls = %d", got, err, len(*calls))
	}
}

func TestSearchFailsClosedWhenTheNotesPathCannotBeRead(t *testing.T) {
	c := feedClient(t, []string{"folder"}, "folder/Private")
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

func TestAccountConnectionWithExclusionsFiltersTheAccountWideTools(t *testing.T) {
	resolved := accountConnection()
	resolved.Exclude = []string{"folder/Private", "notes/Secret", "talk/" + feedTalkExcluded}
	if _, err := scopeOf(resolved); err != nil {
		t.Fatalf("scopeOf = %v", err)
	}
	c, _ := client(t)
	feedServer(t, c, feedRoutes{
		notesPath:     "Notes",
		activity:      "[" + act(1, `,"object_type":"files","object_id":1,"object_name":"/Private/a.txt"`) + "," + act(2, ``) + "]",
		notifications: feedNotifications,
		remote: `[` + remoteJSON("5", "https://192.0.2.7", "/Private/x") + `,` + remoteJSON("7", "https://192.0.2.9", "/Notes/secret/y") + `,` +
			remoteJSON("8", "https://192.0.2.8", "/Open") + `]`,
		search: map[string]string{"files": searchPage("", searchHit("a", "/index.php/call/"+feedTalkExcluded, ``), searchHit("b", "", ``))},
	})
	run := func(h tool, args string) any {
		red := &redact.Redactor{}
		got, err := accountBound(capability.Handler(h))(capability.WithConfirmed(context.Background()), resolved, resolver(red), red, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run(invokeActivityList, `{}`).(*ActivityList); activityIDs(got) != "2" {
		t.Errorf("activity = %s", activityIDs(got))
	}
	if got := run(invokeNotificationsList, `{}`).(*NotificationList); got.Count != 3 {
		t.Errorf("notifications = %d", got.Count)
	}
	if got := run(invokeRemoteSharesList, `{}`).(*RemoteSharesResult); got.Count != 1 || got.Shares[0].ID != "8" {
		t.Errorf("remote shares = %+v", got)
	}
	if got := run(invokeSearchQuery, `{"provider":"files","term":"x"}`).(*SearchResult); searchTitles(got) != "b" {
		t.Errorf("search = %s", searchTitles(got))
	}
}
