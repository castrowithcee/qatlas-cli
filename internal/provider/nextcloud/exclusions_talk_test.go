package nextcloud

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	breakoutRoom = "brk00001"
	filesRoom    = "file0001"
	talkRoomsURL = "/ocs/v2.php/apps/spreed/api/v4/room"
)

// talkExcludeConnection binds the folder Reports and every conversation, and excludes the given entries.
func talkExcludeConnection(excludes ...string) *config.Resolved {
	r := resolvedConnection("reports", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "")
	r.Targets, r.Exclude = []string{"folder/Reports", "talk"}, excludes
	return r
}

func wantTalkMissing(t *testing.T, err error, secrets ...string) {
	t.Helper()
	perr := providerFailure(t, err)
	if perr.Class != provider.ClassNotFound || perr.Original == nil || perr.Original.Status != http.StatusNotFound {
		t.Errorf("err = %+v, want the answer for a conversation Nextcloud does not find", perr)
	}
	for _, secret := range secrets {
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(secret)) {
			t.Errorf("the error names %q: %v", secret, err)
		}
	}
}

func talkToolsWithToken(t *testing.T) []string {
	t.Helper()
	ids := toolsWithArgument(t, ".talk.", "token")
	for _, want := range []string{"nextcloud.talk.rooms.delete", "nextcloud.talk.files.share", "nextcloud.talk.polls.vote"} {
		if !slices.Contains(ids, want) {
			t.Fatalf("%s takes no token", want)
		}
	}
	return ids
}

func TestExcludedTalkToolsAnswerAsMissingBeforeSecretsAndRequests(t *testing.T) {
	for _, id := range talkToolsWithToken(t) {
		for _, spelling := range []string{boundRoom, strings.ToUpper(boundRoom), "AbCd1234"} {
			t.Run(id+" "+spelling, func(t *testing.T) {
				refuse(t)
				handler, args := exampleArguments(t, id, "token", spelling)
				_, touched, err := callTool(handler, talkExcludeConnection("talk/"+boundRoom), args)
				wantTalkMissing(t, err, boundRoom)
				if touched {
					t.Error("a secret was read")
				}
			})
		}
	}
}

// breakoutServer answers the capability read and the read of one conversation; any other request fails.
func breakoutServer(t *testing.T, token, state string) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case request.Method == http.MethodGet && request.URL.Path == talkRoomsURL+"/"+token:
			return ocsResponse(http.StatusOK, ocsData(state)), nil
		}
		t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		return status(http.StatusInternalServerError), nil
	})
}

func roomReads(calls []call, token string) int {
	n := 0
	for _, c := range calls {
		if c.method == http.MethodGet && c.url.Path == talkRoomsURL+"/"+token {
			n++
		}
	}
	return n
}

func TestBreakoutRoomOfAnExcludedParentIsExcludedInEveryTokenTool(t *testing.T) {
	state := `{"token":"` + breakoutRoom + `","type":2,"participantType":1,"objectType":"room","objectId":"ABCD1234"}`
	for _, id := range talkToolsWithToken(t) {
		t.Run(id, func(t *testing.T) {
			calls := breakoutServer(t, breakoutRoom, state)
			handler, args := exampleArguments(t, id, "token", breakoutRoom)
			_, _, err := callTool(handler, talkExcludeConnection("talk/"+boundRoom), args)
			wantTalkMissing(t, err, boundRoom, breakoutRoom)
			if n := roomReads(*calls, breakoutRoom); n != 1 {
				t.Errorf("room reads = %d, want 1", n)
			}
		})
	}
}

func TestBreakoutRoomOfAnotherParentStaysReachable(t *testing.T) {
	state := `{"token":"` + breakoutRoom + `","type":2,"participantType":1,"objectType":"room","objectId":"` + secondRoom + `"}`
	breakoutServer(t, breakoutRoom, state)
	handler, args := exampleArguments(t, "nextcloud.talk.rooms.update", "token", breakoutRoom)
	// The tool's own refusal for a conversation it does not manage, not the answer for a missing one.
	_, _, err := callTool(handler, talkExcludeConnection("talk/"+boundRoom), args)
	if perr := providerFailure(t, err); perr.Message != messageTalkNotInternal {
		t.Errorf("err = %+v", perr)
	}
}

func listServer(t *testing.T, items ...string) {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case talkRoomsURL:
			return ocsResponse(http.StatusOK, ocsData(`[`+strings.Join(items, ",")+`]`)), nil
		}
		t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		return status(http.StatusInternalServerError), nil
	})
}

func objectRoom(token, objectType, objectID string) string {
	return strings.TrimSuffix(roomJSON(token, "R-"+token, 2), "}") + `,"objectType":"` + objectType + `","objectId":"` + objectID + `"}`
}

func TestTalkListLeavesOutExcludedConversationsAndTheirBreakoutRooms(t *testing.T) {
	listServer(t, roomJSON(boundRoom, "Hidden", 2), objectRoom(breakoutRoom, "room", boundRoom), roomJSON(secondRoom, "Open", 2),
		objectRoom("brk00002", "room", secondRoom), objectRoom("ext00001", "extended_conversation", boundRoom))
	raw, _, err := callTool(invokeTalkRoomsList, talkExcludeConnection("talk/ABCD1234"), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	out := mustJSON(t, raw)
	// A reachable conversation keeps the identifier of the excluded one it points to.
	if !strings.Contains(out, `"count":3`) || strings.Contains(out, `"`+boundRoom+`"`+`,"name"`) ||
		strings.Contains(out, breakoutRoom) || !strings.Contains(out, secondRoom) || !strings.Contains(out, "brk00002") ||
		!strings.Contains(out, `"object_id":"`+boundRoom+`"`) {
		t.Errorf("result = %s", out)
	}
}

func TestTalkListWithoutExclusionsKeepsEverything(t *testing.T) {
	listServer(t, roomJSON(boundRoom, "Open", 2), objectRoom(breakoutRoom, "room", boundRoom))
	raw, _, err := callTool(invokeTalkRoomsList, talkExcludeConnection(), `{}`)
	if err != nil || !strings.Contains(mustJSON(t, raw), `"count":2`) {
		t.Errorf("result = %v, %v", raw, err)
	}
}

func paths(calls []call) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.method+" "+strings.TrimPrefix(c.url.Path, "/ocs/v2.php/"))
	}
	return out
}

func TestTalkToolsReadTheConversationOnlyWhenExclusionsExist(t *testing.T) {
	room := "GET apps/spreed/api/v4/room/" + boundRoom
	for name, c := range map[string]struct {
		excludes []string
		want     []string
	}{
		"none":            {nil, []string{"GET cloud/capabilities", room + "/participants"}[0:2]},
		"talk exclusion":  {[]string{"talk/zzzz9999"}, []string{room, "GET cloud/capabilities", room + "/participants"}},
		"folder only":     {[]string{"folder/Reports/Private"}, []string{room, "GET cloud/capabilities", room + "/participants"}},
		"both exclusions": {[]string{"folder/Reports/Private", "talk/zzzz9999"}, []string{room, "GET cloud/capabilities", room + "/participants"}},
	} {
		t.Run(name, func(t *testing.T) {
			calls := talkServer(t, nil)
			if _, _, err := callTool(invokeTalkParticipantsList, talkExcludeConnection(c.excludes...), talkArgs(boundRoom, "")); err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(c.want)
			want[len(want)-1] = "GET apps/spreed/api/v4/room/" + boundRoom + "/participants"
			if got := paths(*calls); !slices.Equal(got, want) {
				t.Errorf("calls = %v, want %v", got, want)
			}
		})
	}
}

func TestTalkRoomsDeleteIsRefusedWhenABreakoutRoomIsExcluded(t *testing.T) {
	const parent = `{"token":"` + boundRoom + `","type":2,"participantType":1,"breakoutRoomMode":"2"}`
	serveDelete := func(t *testing.T, state string, list func() *http.Response) *[]call {
		return serve(t, func(request *http.Request) (*http.Response, error) {
			switch {
			case request.Method == http.MethodGet && request.URL.Path == talkRoomsURL+"/"+boundRoom:
				return ocsResponse(http.StatusOK, ocsData(state)), nil
			case request.Method == http.MethodGet && request.URL.Path == talkRoomsURL+"/"+boundRoom+"/breakout-rooms":
				return list(), nil
			case request.Method == http.MethodDelete:
				return ocsResponse(http.StatusOK, ocsData(`[]`)), nil
			}
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			return status(http.StatusInternalServerError), nil
		})
	}
	listOf := func(tokens ...string) func() *http.Response {
		return func() *http.Response {
			var items []string
			for _, token := range tokens {
				items = append(items, `{"token":"`+token+`","objectType":"room","objectId":"`+boundRoom+`"}`)
			}
			return ocsResponse(http.StatusOK, ocsData(`[`+strings.Join(items, ",")+`]`))
		}
	}
	deleted := func(calls []call) bool {
		return slices.ContainsFunc(calls, func(c call) bool { return c.method == http.MethodDelete })
	}
	args := talkArgs(boundRoom, "")

	t.Run("excluded breakout room", func(t *testing.T) {
		calls := serveDelete(t, parent, listOf("brk00002", "BRK00001"))
		_, _, err := callTool(invokeTalkRoomsDelete, talkExcludeConnection("talk/"+breakoutRoom), args)
		perr := providerFailure(t, err)
		if perr.Class != provider.ClassPermission || perr.Message != messageTalkHoldsExcluded ||
			strings.Contains(strings.ToLower(err.Error()), breakoutRoom) || deleted(*calls) {
			t.Errorf("err = %+v, calls = %v", perr, paths(*calls))
		}
	})
	t.Run("no excluded breakout room", func(t *testing.T) {
		calls := serveDelete(t, parent, listOf("brk00002"))
		if _, _, err := callTool(invokeTalkRoomsDelete, talkExcludeConnection("talk/"+breakoutRoom), args); err != nil || !deleted(*calls) {
			t.Errorf("err = %v, calls = %v", err, paths(*calls))
		}
	})
	t.Run("no exclusions reads no list", func(t *testing.T) {
		calls := serveDelete(t, parent, listOf(breakoutRoom))
		if _, _, err := callTool(invokeTalkRoomsDelete, talkExcludeConnection(), args); err != nil || !deleted(*calls) ||
			len(*calls) != 2 {
			t.Errorf("err = %v, calls = %v", err, paths(*calls))
		}
	})
	t.Run("only folder exclusions read no list", func(t *testing.T) {
		calls := serveDelete(t, parent, listOf(breakoutRoom))
		if _, _, err := callTool(invokeTalkRoomsDelete, talkExcludeConnection("folder/Reports/Private"), args); err != nil ||
			len(*calls) != 2 {
			t.Errorf("err = %v, calls = %v", err, paths(*calls))
		}
	})
	t.Run("no breakout rooms configured reads no list", func(t *testing.T) {
		room := boundRoom
		calls := serveDelete(t, `{"token":"`+room+`","type":2,"participantType":1,"breakoutRoomMode":0}`, listOf(breakoutRoom))
		if _, _, err := callTool(invokeTalkRoomsDelete, talkExcludeConnection("talk/"+breakoutRoom), args); err != nil ||
			len(*calls) != 2 {
			t.Errorf("err = %v, calls = %v", err, paths(*calls))
		}
	})
	for name, answer := range map[string]func() *http.Response{
		"server error": func() *http.Response { return status(http.StatusInternalServerError) },
		"bad request":  func() *http.Response { return status(http.StatusBadRequest) },
		"unreadable":   func() *http.Response { return ocsResponse(http.StatusOK, ocsData(`{"x":1}`)) },
	} {
		t.Run("list fails closed: "+name, func(t *testing.T) {
			calls := serveDelete(t, parent, answer)
			if _, _, err := callTool(invokeTalkRoomsDelete, talkExcludeConnection("talk/"+breakoutRoom), args); err == nil || deleted(*calls) {
				t.Errorf("err = %v, calls = %v", err, paths(*calls))
			}
		})
	}
}

// fileServer answers the Talk reads and the SEARCH that resolves a file ID with the given locations.
func fileServer(t *testing.T, locations map[string]string, searchStatus int, rooms ...string) *[]call {
	t.Helper()
	var calls *[]call
	calls = serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case request.Method == methodSearch:
			if searchStatus != http.StatusMultiStatus {
				return status(searchStatus), nil
			}
			body := (*calls)[len(*calls)-1].body
			for id, href := range locations {
				if strings.Contains(body, `<d:literal>`+id+`</d:literal>`) {
					return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(href, "x.txt", id, "1"))), nil
				}
			}
			return xmlResponse(http.StatusMultiStatus, multistatus()), nil
		case request.URL.Path == talkRoomsURL:
			return ocsResponse(http.StatusOK, ocsData(`[`+strings.Join(rooms, ",")+`]`)), nil
		case strings.HasPrefix(request.URL.Path, talkRoomsURL+"/"):
			for _, room := range rooms {
				if strings.Contains(room, `"`+strings.TrimPrefix(request.URL.Path, talkRoomsURL+"/")+`"`) {
					return ocsResponse(http.StatusOK, ocsData(room)), nil
				}
			}
		}
		t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		return status(http.StatusInternalServerError), nil
	})
	return calls
}

const (
	privateFile = "/remote.php/dav/files/alice/Reports/Private/a.txt"
	openFile    = "/remote.php/dav/files/alice/Reports/Open/b.txt"
	outsideFile = "/remote.php/dav/files/alice/Elsewhere/c.txt"
)

var fileLocations = map[string]string{"4711": privateFile, "4712": openFile, "4714": outsideFile}

func searches(calls []call) int {
	n := 0
	for _, c := range calls {
		if c.method == methodSearch {
			n++
		}
	}
	return n
}

func TestFileConversationsFollowTheFolderExclusions(t *testing.T) {
	rooms := []string{objectRoom("file0001", "file", "4711"), objectRoom("file0002", "file", "4712"),
		objectRoom("file0003", "file", "4713"), objectRoom("file0004", "file", "4714"), roomJSON(boundRoom, "Plain", 2)}
	connection := talkExcludeConnection("folder/Reports/Private")

	t.Run("list", func(t *testing.T) {
		calls := fileServer(t, fileLocations, http.StatusMultiStatus, rooms...)
		raw, _, err := callTool(invokeTalkRoomsList, connection, `{}`)
		out := mustJSON(t, raw)
		// Not resolvable (4713) is left out like the excluded file; a file outside the connection root is not
		// below any exclusion.
		if err != nil || !strings.Contains(out, `"count":3`) || strings.Contains(out, "file0001") || strings.Contains(out, "file0003") ||
			!strings.Contains(out, "file0002") || !strings.Contains(out, "file0004") || !strings.Contains(out, boundRoom) {
			t.Errorf("result = %s, %v", out, err)
		}
		if searches(*calls) != 4 {
			t.Errorf("searches = %d, want one per file conversation", searches(*calls))
		}
		for _, c := range *calls {
			if c.method == methodSearch && (!strings.Contains(c.body, `<d:href>/files/alice</d:href>`) ||
				!strings.Contains(c.body, `<d:eq><d:prop><oc:fileid/></d:prop><d:literal>`)) {
				t.Errorf("search body = %s", c.body)
			}
		}
	})
	for token, wantMissing := range map[string]bool{"file0001": true, "file0002": false, "file0003": true, "file0004": false} {
		t.Run("rooms.get "+token, func(t *testing.T) {
			fileServer(t, fileLocations, http.StatusMultiStatus, rooms...)
			handler, args := exampleArguments(t, "nextcloud.talk.rooms.get", "token", token)
			_, _, err := callTool(handler, connection, args)
			if wantMissing {
				wantTalkMissing(t, err, token, "Private", "4711")
			} else if err != nil {
				t.Errorf("err = %v", err)
			}
		})
	}
	t.Run("a failed resolution is an error, never an answer", func(t *testing.T) {
		fileServer(t, fileLocations, http.StatusInternalServerError, rooms...)
		if _, _, err := callTool(invokeTalkRoomsList, connection, `{}`); err == nil {
			t.Error("the list was answered")
		}
		handler, args := exampleArguments(t, "nextcloud.talk.participants.list", "token", "file0002")
		if _, _, err := callTool(handler, connection, args); err == nil {
			t.Error("the call was answered")
		}
	})
	t.Run("an unusable file ID is not resolved", func(t *testing.T) {
		calls := fileServer(t, fileLocations, http.StatusMultiStatus, objectRoom("file0005", "file", "../x"))
		handler, args := exampleArguments(t, "nextcloud.talk.rooms.get", "token", "file0005")
		_, _, err := callTool(handler, connection, args)
		wantTalkMissing(t, err)
		if searches(*calls) != 0 {
			t.Error("a search was sent")
		}
	})
	t.Run("no folder exclusion, no search", func(t *testing.T) {
		for _, excludes := range [][]string{nil, {"talk/zzzz9999"}} {
			calls := fileServer(t, fileLocations, http.StatusMultiStatus, rooms...)
			raw, _, err := callTool(invokeTalkRoomsList, talkExcludeConnection(excludes...), `{}`)
			if err != nil || !strings.Contains(mustJSON(t, raw), `"count":5`) || searches(*calls) != 0 {
				t.Errorf("result = %s, %v, searches = %d", mustJSON(t, raw), err, searches(*calls))
			}
		}
	})
	t.Run("a spelling of the folder", func(t *testing.T) {
		fileServer(t, fileLocations, http.StatusMultiStatus, rooms...)
		handler, args := exampleArguments(t, "nextcloud.talk.rooms.get", "token", "file0001")
		_, _, err := callTool(handler, talkExcludeConnection("folder/Reports/PRIVATE"), args)
		wantTalkMissing(t, err)
	})
}

func TestTalkFilesShareChecksThePathSideBeforeSecretsAndRequests(t *testing.T) {
	for name, c := range map[string]struct {
		path, exclude string
		blocked       bool
	}{
		"on":                 {"Private", "Private", false},
		"on, other spelling": {"PRIVATE", "private", false},
		"below":              {"private/2026/q1.pdf", "Private", false},
		"ancestor":           {"Parent", "Parent/Private", true},
	} {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			handler, args := exampleArguments(t, "nextcloud.talk.files.share", "path", c.path)
			_, touched, err := callTool(handler, talkExcludeConnection("folder/Reports/"+c.exclude), args)
			if c.blocked {
				wantBlocked(t, err)
			} else {
				wantMissing(t, err)
			}
			if touched {
				t.Error("a secret was read")
			}
		})
	}
}

func TestTalkFilesShareAnExcludedConversationIsMissingBeforeTheTypeRefusal(t *testing.T) {
	// Without the exclusion a breakout room is refused as not private; with it the answer is "missing".
	state := `{"token":"` + boundRoom + `","type":2,"participantType":3,"objectType":"room","objectId":"` + secondRoom + `"}`
	serve(t, func(request *http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(state)), nil
	})
	handler, args := exampleArguments(t, "nextcloud.talk.files.share", "token", boundRoom)
	_, _, err := callTool(handler, talkExcludeConnection("talk/"+secondRoom), args)
	wantTalkMissing(t, err, secondRoom)
	_, _, err = callTool(handler, talkExcludeConnection("talk/zzzz9999"), args)
	if perr := providerFailure(t, err); perr.Message != messageTalkFilesPublic {
		t.Errorf("err = %+v", perr)
	}
}

func TestExclusionsDoNotLimitTalkRoomsCreate(t *testing.T) {
	with, _ := reaches("nextcloud.talk.rooms.create", []string{"talk"})
	if !with {
		t.Error("talk.rooms.create is not offered under the kind alone")
	}
	s, err := parseScopeExcluding([]string{"talk"}, []string{"talk/abcd1234"})
	if err != nil || !s.talks.all || !s.talkHides("ABCD1234") || s.talkHides("abcd12345") {
		t.Errorf("scope = %+v, %v", s, err)
	}
}
