package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
)

const (
	boundRoom   = "abcd1234"
	secondRoom  = "wxyz5678"
	foreignRoom = "foreign9999canary"
	fileURL     = "https://cloud.example.invalid/s/file-share-token-canary-4f1c"
)

const capabilitiesBody = `{"version":{"string":"31"},"capabilities":{"spreed":{"features":["chat-v2","conversation-v4"]}}}`

func roomJSON(token, name string, kind int) string {
	return `{"token":"` + token + `","name":"` + name + `","displayName":"` + name + `","type":` +
		strconv.Itoa(kind) + `,"readOnly":0,"hasPassword":false,"participantType":3,` +
		`"unreadMessages":2,"unreadMention":true,"lastActivity":1767225600,"avatarId":"avatar-canary","remoteServer":"x"}`
}

func talkServer(t *testing.T, chat func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		path := request.URL.Path
		switch {
		case path == "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case path == "/ocs/v2.php/apps/spreed/api/v4/room":
			return ocsResponse(http.StatusOK, ocsData(`[`+roomJSON(boundRoom, "Team", 2)+`,`+roomJSON(secondRoom, "Other", 3)+`,`+
				roomJSON(foreignRoom, "Foreign", 1)+`]`)), nil
		case path == "/ocs/v2.php/apps/spreed/api/v4/room/"+boundRoom:
			return ocsResponse(http.StatusOK, ocsData(roomJSON(boundRoom, "Team", 2))), nil
		case path == "/ocs/v2.php/apps/spreed/api/v4/room/"+boundRoom+"/participants":
			return ocsResponse(http.StatusOK, ocsData(`[{"actorType":"users","actorId":"bob","displayName":"Bob",`+
				`"participantType":1,"inCall":0,"sessionIds":["`+textCanary+`"],"phoneNumber":"+49"},`+
				`{"actorType":"guests","actorId":"hash","displayName":"","participantType":4,"inCall":7}]`)), nil
		case strings.HasPrefix(path, "/ocs/v2.php/apps/spreed/api/v1/chat/"):
			return chat(request)
		}
		t.Errorf("unexpected request %s", path)
		return nil, http.ErrNotSupported
	})
}

// talkArgs builds the arguments of a Talk tool for one conversation, followed by further members.
func talkArgs(room, extra string) string {
	raw, _ := json.Marshal(map[string]string{"token": room})
	return strings.TrimSuffix(string(raw), "}") + extra + "}"
}

func talkInvokeCore(t *testing.T, targets []string, operation, args string) (application.InvokeResponse, error) {
	t.Helper()
	return accountClient(t, targets...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args),
	})
}

func TestTalkRoomsListReportsOnlyBoundConversations(t *testing.T) {
	calls := talkServer(t, nil)
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom, "talk/" + secondRoom}, "nextcloud.talkrooms.list", `{}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var result RoomsResult
	if err := json.Unmarshal(response.Result, &result); err != nil || result.Count != 2 ||
		result.Rooms[0].Token != boundRoom || result.Rooms[0].Type != "group" || result.Rooms[0].LastActivity != "2026-01-01T00:00:00Z" ||
		result.Rooms[1].Type != "public" || !result.Rooms[0].UnreadMention {
		t.Errorf("result = %s, %v", response.Result, err)
	}
	for _, canary := range []string{foreignRoom, "avatar-canary", "remoteServer"} {
		if strings.Contains(string(response.Result), canary) {
			t.Errorf("the result carries %q", canary)
		}
	}
	if len(*calls) != 2 || (*calls)[0].url.Path != "/ocs/v2.php/cloud/capabilities" || (*calls)[0].method != http.MethodGet ||
		(*calls)[1].url.Query().Get("noStatusUpdate") != "1" {
		t.Errorf("calls = %+v", *calls)
	}

	all, err := talkInvokeCore(t, []string{"talk"}, "nextcloud.talkrooms.list", `{}`)
	if err != nil || !strings.Contains(string(all.Result), foreignRoom) {
		t.Errorf("a talk target binds every conversation: %s, %v", all.Result, err)
	}
}

func TestTalkRoomAndParticipantReads(t *testing.T) {
	talkServer(t, nil)
	response, err := talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talkrooms.get", talkArgs(boundRoom, ""))
	if err != nil || !strings.Contains(string(response.Result), strings.Trim(talkArgs(boundRoom, ""), "{}")) {
		t.Errorf("rooms.get = %s, %v", response.Result, err)
	}
	response, err = talkInvokeCore(t, []string{"talk/" + boundRoom}, "nextcloud.talkparticipants.list", talkArgs(boundRoom, ""))
	var result ParticipantsResult
	if err := json.Unmarshal(response.Result, &result); err != nil || result.Count != 2 ||
		result.Participants[0] != (Participant{ActorType: "users", ActorID: "bob", DisplayName: "Bob", ParticipantType: "owner"}) ||
		!result.Participants[1].InCall || strings.Contains(string(response.Result), textCanary) {
		t.Errorf("participants = %s, %v", response.Result, err)
	}
}

func TestTalkReadsRefuseAnUnboundConversationBeforeAnyIO(t *testing.T) {
	refuse(t)
	for _, operation := range []string{"nextcloud.talkrooms.get", "nextcloud.talkparticipants.list", "nextcloud.talkmessages.list"} {
		for name, targets := range map[string][]string{
			"other token": {"talk/" + boundRoom}, "folder only": {"folder/Reports"}, "no talk": {"account"},
		} {
			_, err := talkInvokeCore(t, targets, operation, talkArgs(foreignRoom, ""))
			if err == nil || strings.Contains(err.Error(), foreignRoom) {
				t.Errorf("%s %s: err = %v", operation, name, err)
			}
		}
	}
	for _, targets := range [][]string{{"folder/Reports"}, {"account"}} {
		if _, err := talkInvokeCore(t, targets, "nextcloud.talkrooms.list", `{}`); err == nil {
			t.Errorf("rooms.list ran without a talk target on %v", targets)
		}
	}
	c, _ := client(t)
	if _, err := c.ListMessages(context.Background(), "../x", 10, ""); err == nil {
		t.Error("a path-shaped token was accepted")
	}
}

func messageJSON(id, text, params string) string {
	return `{"id":` + id + `,"timestamp":1767225600,"actorType":"users","actorId":"bob","actorDisplayName":"Bob",` +
		`"messageType":"comment","systemMessage":"","message":"` + text + `","messageParameters":` + params + `}`
}

func TestTalkMessagesPageThroughTheLastGivenHeader(t *testing.T) {
	var queries []string
	calls := talkServer(t, func(request *http.Request) (*http.Response, error) {
		queries = append(queries, request.URL.RawQuery)
		response := ocsResponse(http.StatusOK, ocsData(`[`+messageJSON("30", "newest", `[]`)+`,`+messageJSON("29", "older", `[]`)+`]`))
		if request.URL.Query().Get("lastKnownMessageId") == "" {
			response.Header.Set("X-Chat-Last-Given", "29")
			return response, nil
		}
		response = ocsResponse(http.StatusOK, ocsData(`[`+messageJSON("28", "oldest", `[]`)+`]`))
		response.Header.Set("X-Chat-Last-Given", "28")
		return response, nil
	})
	targets := []string{"talk/" + boundRoom}
	response, err := talkInvokeCore(t, targets, "nextcloud.talkmessages.list", talkArgs(boundRoom, `,"limit":2`))
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var page MessagesResult
	if err := json.Unmarshal(response.Result, &page); err != nil || page.Count != 2 || page.NextCursor != "29" ||
		page.Messages[0].Text != "newest" || page.Messages[0].Time != "2026-01-01T00:00:00Z" {
		t.Fatalf("page 1 = %s, %v", response.Result, err)
	}
	response, err = talkInvokeCore(t, targets, "nextcloud.talkmessages.list",
		talkArgs(boundRoom, `,"limit":2,"cursor":"`+page.NextCursor+`"`))
	var last MessagesResult
	if err := json.Unmarshal(response.Result, &last); err != nil || last.Count != 1 || last.NextCursor != "" {
		t.Fatalf("page 2 = %s, %v", response.Result, err)
	}
	for _, want := range []string{"lookIntoFuture=0", "setReadMarker=0", "markNotificationsAsRead=0", "limit=2"} {
		if !strings.Contains(queries[0], want) {
			t.Errorf("query %q lacks %s", queries[0], want)
		}
	}
	if !strings.Contains(queries[1], "lastKnownMessageId=29") || strings.Contains(queries[0], "lastKnownMessageId") ||
		strings.Contains(queries[0], "timeout") {
		t.Errorf("queries = %v", queries)
	}
	if (*calls)[1].url.Path != "/ocs/v2.php/apps/spreed/api/v1/chat/"+boundRoom {
		t.Errorf("path = %s", (*calls)[1].url.Path)
	}
}

func TestTalkMessagesRefuseUnusableArguments(t *testing.T) {
	refuse(t)
	for name, args := range map[string]string{
		"limit above the cap": `{"token":"abcd1234","limit":101}`,
		"limit zero":          `{"token":"abcd1234","limit":0}`,
		"bad cursor":          `{"token":"abcd1234","cursor":"1/2"}`,
		"free parameter":      `{"token":"abcd1234","lookIntoFuture":1}`,
		"no token":            `{}`,
	} {
		if _, err := talkInvokeCore(t, []string{"talk"}, "nextcloud.talkmessages.list", args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	c, _ := client(t)
	if _, err := c.ListMessages(context.Background(), boundRoom, maxMessagesPerPage+1, ""); err == nil {
		t.Error("ListMessages accepted a page beyond the cap")
	}
}

func TestTalkMessagesNormaliseRichObjectsWithoutLinks(t *testing.T) {
	params := `{"actor":{"type":"user","id":"bob","name":"Bob","link":"` + fileURL + `"},` +
		`"file":{"type":"file","id":"77","name":"plan.pdf","path":"Talk/plan.pdf","link":"` + fileURL + `",` +
		`"preview-available":"yes","mimetype":"application/pdf"}}`
	talkServer(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`[`+
			messageJSON("5", "{actor} shared {file} {unknown}", params)+`,`+
			`{"id":4,"timestamp":1767225000,"messageType":"system","systemMessage":"user_added","message":"{actor} added you",`+
			`"messageParameters":{"actor":{"type":"user","id":"carl","name":"Carl"}},"parent":{"id":3}}]`)), nil
	})
	response, err := talkInvokeCore(t, []string{"talk"}, "nextcloud.talkmessages.list", talkArgs(boundRoom, ""))
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var page MessagesResult
	_ = json.Unmarshal(response.Result, &page)
	if page.Messages[0].Text != "@Bob shared plan.pdf {unknown}" || len(page.Messages[0].Objects) != 2 ||
		page.Messages[0].Objects[1] != (MessageObject{Key: "file", Type: "file", Name: "plan.pdf"}) ||
		page.Messages[1].Text != "@Carl added you" || page.Messages[1].SystemMessage != "user_added" ||
		page.Messages[1].ParentID != "3" {
		t.Errorf("messages = %s", response.Result)
	}
	for _, leak := range []string{fileURL, "file-share-token-canary", "Talk/plan.pdf", "preview", "https://"} {
		if strings.Contains(string(response.Result), leak) {
			t.Errorf("the result carries %q: %s", leak, response.Result)
		}
	}
}

func TestTalkMessageTextIsCutAndFlagged(t *testing.T) {
	long := strings.Repeat("ä", maxMessageText)
	talkServer(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`[`+messageJSON("5", long, `[]`)+`]`)), nil
	})
	response, err := talkInvokeCore(t, []string{"talk"}, "nextcloud.talkmessages.list", talkArgs(boundRoom, ""))
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var page MessagesResult
	if err := json.Unmarshal(response.Result, &page); err != nil || !page.Messages[0].Truncated ||
		len(page.Messages[0].Text) > maxMessageText || !strings.HasPrefix(long, page.Messages[0].Text) {
		t.Errorf("message = %.80s, %v", response.Result, err)
	}
}

func TestTalkListsAreCappedAndFlagged(t *testing.T) {
	var rooms, people []string
	for i := 0; i < maxRooms+1; i++ {
		rooms = append(rooms, roomJSON("room"+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676)), "R", 2))
	}
	for i := 0; i < maxParticipants+1; i++ {
		people = append(people, `{"actorType":"users","actorId":"u","participantType":3,"inCall":0}`)
	}
	serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(request.URL.Path, "capabilities"):
			return ocsResponse(http.StatusOK, ocsData(capabilitiesBody)), nil
		case strings.HasSuffix(request.URL.Path, "participants"):
			return ocsResponse(http.StatusOK, ocsData(`[`+strings.Join(people, ",")+`]`)), nil
		}
		return ocsResponse(http.StatusOK, ocsData(`[`+strings.Join(rooms, ",")+`]`)), nil
	})
	response, err := talkInvokeCore(t, []string{"talk"}, "nextcloud.talkrooms.list", `{}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var list RoomsResult
	if err := json.Unmarshal(response.Result, &list); err != nil || list.Count != maxRooms || !list.Truncated {
		t.Errorf("rooms = %d truncated=%v, %v", list.Count, list.Truncated, err)
	}
	response, err = talkInvokeCore(t, []string{"talk"}, "nextcloud.talkparticipants.list", talkArgs(boundRoom, ""))
	var members ParticipantsResult
	if err := json.Unmarshal(response.Result, &members); err != nil || members.Count != maxParticipants || !members.Truncated {
		t.Errorf("participants = %d truncated=%v, %v", members.Count, members.Truncated, err)
	}
}

func TestTalkMessagesAnswerNotModifiedAsEmpty(t *testing.T) {
	talkServer(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotModified, Header: http.Header{}, Body: http.NoBody}, nil
	})
	response, err := talkInvokeCore(t, []string{"talk"}, "nextcloud.talkmessages.list", talkArgs(boundRoom, ""))
	if err != nil || strings.Contains(string(response.Result), "messages") || strings.Contains(string(response.Result), "next_cursor") {
		t.Errorf("result = %s, %v", response.Result, err)
	}
}

func TestTalkFailuresAreClearAndLeakNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		capabilities string
		status       int
		want         string
	}{
		"no spreed app":      {`{"capabilities":{"files":{}}}`, 200, "Talk app"},
		"missing feature":    {`{"capabilities":{"spreed":{"features":["chat-v2"]}}}`, 200, "does not offer a feature"},
		"room is missing":    {capabilitiesBody, http.StatusNotFound, "does not exist"},
		"redirect":           {capabilitiesBody, http.StatusFound, "redirect"},
		"forbidden":          {capabilitiesBody, http.StatusForbidden, "may not"},
		"capabilities moved": {"", http.StatusMovedPermanently, "redirect"},
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "capabilities") {
					if tc.capabilities == "" {
						response := ocsResponse(tc.status, textCanary)
						response.Header.Set("Location", fileURL)
						return response, nil
					}
					return ocsResponse(http.StatusOK, ocsData(tc.capabilities)), nil
				}
				response := ocsResponse(tc.status, textCanary)
				response.Header.Set("Location", fileURL)
				return response, nil
			})
			_, err := talkInvokeCore(t, []string{"talk"}, "nextcloud.talkrooms.get", talkArgs(boundRoom, ""))
			if err == nil || !strings.Contains(err.Error(), tc.want) ||
				strings.Contains(err.Error(), textCanary) || strings.Contains(err.Error(), fileURL) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTalkProfileHoldsExactlyTheReadTools(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if profile.ID != "talk-read" {
			for _, id := range profile.Tools {
				if strings.Contains(id, ".talk") {
					t.Errorf("profile %s offers %s", profile.ID, id)
				}
			}
			continue
		}
		want := []string{talkRoomsList.ID, talkRoomsGet.ID, talkParticipantsList.ID, talkMessagesList.ID}
		if strings.Join(profile.Tools, ",") != strings.Join(want, ",") {
			t.Errorf("talk-read tools = %v", profile.Tools)
		}
		return
	}
	t.Error("no talk-read profile")
}
