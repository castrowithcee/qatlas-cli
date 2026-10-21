package nextcloud

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const filesHome = "/remote.php/dav/files/" + aliceUser + "/"

func draft() *config.Resolved {
	return resolvedConnection("draft", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "")
}

func listTargets(t *testing.T, req config.TargetListRequest) (config.TargetPage, error) {
	t.Helper()
	red := &redact.Redactor{}
	return ListTargets(context.Background(), draft(), resolver(red), red, req)
}

// choiceServer answers every list the target choice reads.
func choiceServer(t *testing.T, notes map[int]string) *[]call {
	t.Helper()
	return serve(t, func(r *http.Request) (*http.Response, error) {
		path := r.URL.Path
		switch {
		case r.Method == methodPropfind && strings.HasPrefix(path, filesHome):
			switch strings.TrimPrefix(path, filesHome) {
			case "":
				return xmlResponse(http.StatusMultiStatus, multistatus(
					folderXML(filesHome, "alice", "1", "0"),
					folderXML(filesHome+"Team/", "Team", "2", "0"),
					folderXML(filesHome+"50%25/", "50%", "3", "0"),
					folderXML(filesHome+"Docs/", "Do\tcs", "4", "0"),
					fileXML(filesHome+"a.pdf", "a.pdf", "5", "1"),
				)), nil
			case "Team":
				return xmlResponse(http.StatusMultiStatus, multistatus(
					folderXML(filesHome+"Team/", "Team", "2", "0"),
					folderXML(filesHome+"Team/Reports/", "Reports", "6", "0"),
					folderXML(filesHome+"Team/100%25/", "100%", "7", "0"),
				)), nil
			}
		case r.Method == methodPropfind && path == calHome:
			return xmlResponse(http.StatusMultiStatus, homeListing()), nil
		case r.Method == methodPropfind && path == contactHome:
			return xmlResponse(http.StatusMultiStatus, bookListing()), nil
		case path == "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case path == "/ocs/v2.php/apps/spreed/api/v4/room":
			return ocsResponse(http.StatusOK, ocsData(`[`+roomJSON(boundRoom, "Team", 2)+`,`+roomJSON(secondRoom, "Other", 3)+`]`)), nil
		case path == "/index.php/apps/deck/api/v1.1/boards":
			return jsonResponse(http.StatusOK, `[`+board("1", "One")+`,`+board("2", "Two")+`]`), nil
		case path == notesBase+"notes":
			var items []string
			for id := 1; id <= len(notes); id++ {
				items = append(items, noteJSON(id, notes[id], ""))
			}
			return noteResponse(200, nil, "["+strings.Join(items, ",")+"]"), nil
		}
		t.Errorf("unexpected request %s %s", r.Method, path)
		return xmlResponse(http.StatusNotFound, ""), nil
	})
}

func targetsOf(page config.TargetPage) []string {
	var out []string
	for _, e := range page.Entries {
		out = append(out, e.Target)
	}
	return out
}

func TestListTargetsOffersEachKindAndTheKindAlone(t *testing.T) {
	choiceServer(t, nil)
	for kind, want := range map[string]string{
		"calendar":    "calendar,calendar/personal,calendar/team_shared_by_bob",
		"addressbook": "addressbook,addressbook/contacts,addressbook/team",
		"talk":        "talk,talk/abcd1234,talk/wxyz5678",
		"deck":        "deck,deck/1,deck/2",
	} {
		page, err := listTargets(t, config.TargetListRequest{Kind: kind})
		if err != nil || strings.Join(targetsOf(page), ",") != want || page.Next != "" {
			t.Errorf("%s = %v, %v, want %s", kind, targetsOf(page), err, want)
		}
		if page.Entries[0].Kind != kind || page.Entries[1].Name == "" {
			t.Errorf("%s entries = %+v", kind, page.Entries)
		}
	}
}

func TestListTargetsBrowsesTheFolderTreeAndDropsUnusablePaths(t *testing.T) {
	calls := choiceServer(t, nil)
	page, err := listTargets(t, config.TargetListRequest{Kind: "folder"})
	if err != nil || strings.Join(targetsOf(page), ",") != "folder,folder/Docs,folder/Team" {
		t.Fatalf("root = %v, %v", targetsOf(page), err)
	}
	team := page.Entries[2]
	if !team.HasChildren || team.Path != "/" || team.Name != "Team" || team.Kind != "folder" {
		t.Errorf("entry = %+v", team)
	}
	page, err = listTargets(t, config.TargetListRequest{Kind: "folder", Parent: team.Target})
	if err != nil || strings.Join(targetsOf(page), ",") != "folder/Team/Reports" || page.Entries[0].Path != "/Team" {
		t.Errorf("below Team = %+v, %v", page.Entries, err)
	}
	for _, c := range *calls {
		if c.depth != depthChildren {
			t.Errorf("depth = %q", c.depth)
		}
	}
	for _, parent := range []string{"folder/../x", "folder/a/*", "calendar/x", "folder//x"} {
		if _, err := listTargets(t, config.TargetListRequest{Kind: "folder", Parent: parent}); err == nil {
			t.Errorf("parent %q accepted", parent)
		}
	}
}

func TestListTargetsDerivesNoteCategories(t *testing.T) {
	choiceServer(t, map[int]string{1: "", 2: "Work/Plans/Q1", 3: "Work", 4: "Home", 5: "Bad/..", 6: "50%/x", 7: "Work/Ideas"})
	page, err := listTargets(t, config.TargetListRequest{Kind: "notes"})
	if err != nil || strings.Join(targetsOf(page), ",") != "notes,notes/Home,notes/Work" ||
		page.Entries[2].HasChildren != true || page.Entries[1].HasChildren {
		t.Errorf("top = %+v, %v", page.Entries, err)
	}
	page, err = listTargets(t, config.TargetListRequest{Kind: "notes", Parent: "notes/Work"})
	if err != nil || strings.Join(targetsOf(page), ",") != "notes/Work/Ideas,notes/Work/Plans" ||
		page.Entries[1].Path != "/Work" || !page.Entries[1].HasChildren || page.Entries[0].Name != "Ideas" {
		t.Errorf("below Work = %+v, %v", page.Entries, err)
	}
}

func TestListTargetsPagesAndRefusesOtherRequests(t *testing.T) {
	var boards []string
	for i := 1; i <= 120; i++ {
		boards = append(boards, board(strconv.Itoa(i), "B"))
	}
	serve(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, "["+strings.Join(boards, ",")+"]"), nil
	})
	page, err := listTargets(t, config.TargetListRequest{Kind: "deck"})
	if err != nil || len(page.Entries) != 51 || page.Next != "2" {
		t.Fatalf("first = %d, %q, %v", len(page.Entries), page.Next, err)
	}
	page, err = listTargets(t, config.TargetListRequest{Kind: "deck", Cursor: "3"})
	if err != nil || len(page.Entries) != 20 || page.Next != "" {
		t.Errorf("last = %d, %q, %v", len(page.Entries), page.Next, err)
	}
	refuse(t)
	for _, req := range []config.TargetListRequest{
		{Kind: "account"}, {Kind: "admin"}, {Kind: "other"}, {Kind: "deck", Cursor: "0"}, {Kind: "deck", Cursor: "x"},
		{Kind: "deck", Parent: "deck/1"},
	} {
		if _, err := listTargets(t, req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
}

func TestResolveTargetURLTable(t *testing.T) {
	refuse(t)
	sub := &config.Resolved{Provider: Provider, BaseURL: partnerInstance}
	main := draft()
	type row struct {
		resolved *config.Resolved
		url      string
		want     string
	}
	for _, c := range []row{
		{main, mainInstance + "/apps/files/files/12?dir=/Team/Reports", "folder/Team/Reports"},
		{main, mainInstance + "/index.php/apps/files/files/12?dir=/Team/Reports&openfile=true", "folder/Team/Reports"},
		{main, mainInstance + "/apps/files/files/12?dir=/Team/Reports/", "folder/Team/Reports"},
		{main, "HTTPS://CLOUD.example.invalid/apps/files/files/12?dir=%2FTeam", "folder/Team"},
		{main, mainInstance + "/apps/deck/board/7", "deck/7"},
		{main, mainInstance + "/index.php/apps/deck/board/7/card/9", "deck/7"},
		{main, mainInstance + "/call/abcd1234", "talk/abcd1234"},
		{main, mainInstance + "/index.php/call/abcd1234?x=1", "talk/abcd1234"},
		{sub, partnerInstance + "/index.php/call/abcd1234", "talk/abcd1234"},
		{sub, partnerInstance + "/apps/deck/board/7", "deck/7"},
		{sub, partnerOrigin + "/call/abcd1234", ""},
		{main, "https://other.example.invalid/call/abcd1234", ""},
		{main, "http://cloud.example.invalid/call/abcd1234", ""},
		{main, strings.Replace(mainInstance, "https://", "https://x:y@", 1) + "/call/abcd1234", ""},
		{main, "https://cloud.example.invalid:8443/call/abcd1234", ""},
		{main, mainInstance + "/apps/files/files/12", ""},
		{main, mainInstance + "/apps/files/files/12?dir=/", ""},
		{main, mainInstance + "/apps/files/files/12?dir=Team", ""},
		{main, mainInstance + "/apps/files/files/12?dir=/a&dir=/b", ""},
		{main, mainInstance + "/apps/files/files/12?dir=/Team/../x", ""},
		{main, mainInstance + "/apps/files/files/12?dir=/50%25", ""},
		{main, mainInstance + "/apps/files/files/12?dir=/a//b", ""},
		{main, mainInstance + "/apps/files/files/12?dir=/a%5Cb", ""},
		{main, mainInstance + "/apps/files/files/x?dir=/Team", ""},
		{main, mainInstance + "/apps/files/files?dir=/Team", ""},
		{main, mainInstance + "/apps/deck/#/board/7", ""},
		{main, mainInstance + "/apps/deck/board/x", ""},
		{main, mainInstance + "/apps/deck/board/", ""},
		{main, mainInstance + "/call/ab", ""},
		{main, mainInstance + "/call/ab%2Fcd12", ""},
		{main, mainInstance + "/call/abcd1234/extra", ""},
		{main, mainInstance + "/s/abcd1234", ""},
		{main, "::not a url", ""},
		{main, "", ""},
		{&config.Resolved{Provider: Provider, BaseURL: "http://cloud.example.invalid"}, mainInstance + "/call/abcd1234", ""},
		{nil, mainInstance + "/call/abcd1234", ""},
	} {
		got, err := ResolveTargetURL(context.Background(), c.resolved, nil, nil, "", c.url)
		if err != nil {
			t.Errorf("%q: %v", c.url, err)
			continue
		}
		switch {
		case c.want == "" && len(got) != 0, c.want != "" && (len(got) != 1 || got[0].Target != c.want):
			t.Errorf("%q = %+v, want %q", c.url, got, c.want)
		}
	}
}

func TestResolvedFolderURLEntryIsAFolderWithPlace(t *testing.T) {
	got, _ := ResolveTargetURL(context.Background(), draft(), nil, nil, "", mainInstance+"/apps/files/files/1?dir=/Team/Reports")
	if len(got) != 1 || got[0].Kind != "folder" || got[0].Name != "Reports" || got[0].Path != "/Team" || !got[0].HasChildren {
		t.Errorf("entry = %+v", got)
	}
}

func TestEveryRegisteredToolHasAReachRule(t *testing.T) {
	reg := registry(t)
	tools := reg.Provider(Provider)
	if len(tools) < 100 {
		t.Fatalf("only %d tools registered", len(tools))
	}
	for _, tool := range tools {
		if len(toolKinds[tool.ID]) == 0 {
			t.Errorf("%s has no reach rule", tool.ID)
		}
	}
	if len(toolKinds) != len(tools) {
		t.Errorf("the table holds %d tools, %d are registered", len(toolKinds), len(tools))
	}
}

func TestReachesFollowTheCallCheck(t *testing.T) {
	reg := registry(t)
	meta, _ := reg.ProviderMetadata(Provider)
	target := meta.Target
	single := map[string]string{
		kindFolder: "folder/Reports", kindCalendar: "calendar/personal", kindAddressbook: "addressbook/contacts",
		kindTalk: "talk/abcd1234", kindDeck: "deck/1", kindNotes: "notes/Work", kindAccount: "account", kindAdmin: "admin",
	}
	for _, tool := range reg.Provider(Provider) {
		want := toolKinds[tool.ID]
		for kind, value := range single {
			reached, hint := target.Reach(tool.ID, []string{value})
			needs := false
			for _, w := range want {
				needs = needs || w != kind
			}
			// A tool that creates a whole kind is reached by the kind alone only.
			if wanted, ok := createsWholeKind[tool.ID]; ok && wanted == kind {
				needs = true
			}
			if reached == needs {
				t.Errorf("%s with %s: reached %v", tool.ID, value, reached)
			}
			if !reached && hint == "" {
				t.Errorf("%s with %s: no hint", tool.ID, value)
			}
		}
	}
	for tool, kind := range createsWholeKind {
		if r, _ := target.Reach(tool, []string{kind}); !r {
			t.Errorf("%s not reached by %s", tool, kind)
		}
		if r, hint := target.Reach(tool, []string{single[kind]}); r || hint != "needs "+kind {
			t.Errorf("%s with one object: %v %q", tool, r, hint)
		}
	}
	if r, hint := target.Reach(accountGet.ID, []string{"folder"}); r || hint != "needs account" {
		t.Errorf("account.get = %v %q", r, hint)
	}
	if r, hint := target.Reach(adminUsersList.ID, []string{"folder"}); r || hint != "needs admin" {
		t.Errorf("admin = %v %q", r, hint)
	}
	if r, hint := target.Reach(talkFilesShare.ID, []string{"talk"}); r || hint != "needs folder or folder/PATH" {
		t.Errorf("talk.files.share = %v %q", r, hint)
	}
	if r, _ := target.Reach(talkFilesShare.ID, []string{"talk", "folder/A"}); !r {
		t.Error("talk.files.share not reached with both")
	}
}

// A configuration that exists today keeps its tools: the legacy root folder reaches the Files tools only,
// exactly as the call checks it, and a malformed list reaches everything.
func TestReachesLeaveExistingConfigsAlone(t *testing.T) {
	reg := registry(t)
	meta, _ := reg.ProviderMetadata(Provider)
	for _, targets := range [][]string{{"Reports"}, {"Team/Reports"}, {"/"}} {
		if r, _ := meta.Target.Reach(filesList.ID, targets); !r {
			t.Errorf("%v does not reach files.list", targets)
		}
		if r, _ := meta.Target.Reach(calendarsList.ID, targets); r {
			t.Errorf("%v reaches calendars.list", targets)
		}
	}
	if r, _ := meta.Target.Reach(calendarsList.ID, []string{"calendar/a", "calendar/a"}); !r {
		t.Error("a malformed list must reach everything")
	}
}

// The core keeps only entries that are valid targets of their kind and keeps HasChildren only where the
// kind holds children, so every listed entry and every parent a listing hands back must pass that filter.
func TestListedEntriesAndParentsPassTheCoreFilter(t *testing.T) {
	reg := registry(t)
	meta, _ := reg.ProviderMetadata(Provider)
	target := meta.Target
	choiceServer(t, map[int]string{1: "Work/Plans/Q1", 2: "Work", 3: "Home"})
	check := func(req config.TargetListRequest, wantChildren bool) []config.TargetEntry {
		t.Helper()
		page, err := listTargets(t, req)
		if err != nil || len(page.Entries) == 0 {
			t.Fatalf("%+v = %v, %v", req, page.Entries, err)
		}
		anyChildren := false
		for _, e := range page.Entries {
			if e.Kind != req.Kind || !target.ValidTarget(e.Kind, e.Target) {
				t.Errorf("%+v: entry %+v fails the core filter", req, e)
			}
			if e.HasChildren {
				anyChildren = true
				if !target.HasChildKind(e.Kind) {
					t.Errorf("%+v: HasChildren on a kind without children", req)
				}
			}
		}
		if anyChildren != wantChildren {
			t.Errorf("%+v: children = %v", req, anyChildren)
		}
		return page.Entries
	}
	for _, kind := range []string{"calendar", "addressbook", "talk", "deck"} {
		check(config.TargetListRequest{Kind: kind}, false)
	}
	check(config.TargetListRequest{Kind: "folder"}, true)
	nested := check(config.TargetListRequest{Kind: "folder", Parent: "folder/Team"}, true)
	if nested[0].Target != "folder/Team/Reports" {
		t.Errorf("nested = %+v", nested)
	}
	check(config.TargetListRequest{Kind: "notes"}, true)
	subs := check(config.TargetListRequest{Kind: "notes", Parent: "notes/Work"}, true)
	if subs[0].Target != "notes/Work/Plans" {
		t.Errorf("subcategories = %+v", subs)
	}
	for kind, parent := range map[string]string{"folder": "folder/Team", "notes": "notes/Work"} {
		if !target.ValidTarget(kind, parent) || !target.ValidTarget(kind, parent+"/Deeper") {
			t.Errorf("%s parent %q is not a valid target", kind, parent)
		}
	}
}
