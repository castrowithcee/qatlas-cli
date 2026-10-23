package nextcloud

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The fixtures follow the examples of the Deck REST documentation.
const deckDocBoard = `{"title":"Board title","owner":{"primaryKey":"admin","uid":"admin","displayname":"Administrator"},` +
	`"color":"ff0000","archived":false,` +
	`"labels":[{"title":"Finished","color":"31CC7C","boardId":"10","cardId":null,"id":37}],` +
	`"acl":[{"id":1,"type":0,"permissionEdit":true,"permissionShare":false,"permissionManage":false,"owner":false,` +
	`"participant":{"primaryKey":"bob","uid":"bob","displayname":"Bob","type":0}}],` +
	`"permissions":{"PERMISSION_READ":true,"PERMISSION_EDIT":true,"PERMISSION_MANAGE":false,"PERMISSION_SHARE":true},` +
	`"users":[{"primaryKey":"admin","uid":"admin","displayname":"Administrator"}],` +
	`"shared":0,"deletedAt":0,"id":10,"lastModified":1586269585,` +
	`"settings":{"notify-due":"off","calendar":true},"ETag":"abc123",` +
	`"stacks":[{"id":4,"title":"ToDo","order":999,"deletedAt":0,"cards":[{"id":1,"title":"never reported"}]},` +
	`{"id":5,"title":"Gone","order":1,"deletedAt":9,"cards":[]}]}`

const deckDocCard = `{"title":"Test","description":"d","stackId":3,"type":"plain","lastModified":1541528026,` +
	`"createdAt":1541528026,"labels":[{"id":5,"title":"Bug","color":"f00","boardId":2,"cardId":21}],"assignedUsers":[],` +
	`"attachments":[{"cardId":21,"type":"deck_file","data":"a.eml","lastModified":1541529048,"createdAt":1541529048,` +
	`"createdBy":"admin","deletedAt":0,"extendedData":{"filesize":922258,"mimetype":"application/octet-stream"},"id":6},` +
	`{"cardId":21,"type":"file","data":"gone.txt","deletedAt":7,"id":7}],` +
	`"attachmentCount":1,"owner":{"primaryKey":"admin","uid":"admin","displayname":"Administrator"},"order":999,` +
	`"archived":false,"duedate":"2019-12-24T19:29:30+00:00","deletedAt":0,"commentsUnread":0,"id":21,"overdue":1,"ETag":"cardtag"}`

const deckDocStack = `{"title":"ToDo","boardId":2,"deletedAt":0,"lastModified":1541426139,"ETag":"stacktag",` +
	`"cards":[` + deckDocCard + `],"order":999,"id":3}`

func deckHeader(t *testing.T, calls *[]call, key string) string {
	t.Helper()
	return (*calls)[len(*calls)-1].header.Get(key)
}

func TestDeckBoardsListReportsEveryDocumentedField(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusOK, `[`+deckDocBoard+`]`)
		response.Header.Set("ETag", `"listtag"`)
		return response, nil
	})
	result, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.boards.list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"boards":[{"id":"10","title":"Board title","color":"ff0000","archived":false,` +
		`"owner":{"id":"admin","name":"Administrator","primary_key":"admin"},` +
		`"permissions":{"read":true,"edit":true,"manage":false,"share":true},"shared":0,"last_modified":1586269585,` +
		`"settings":{"notify_due":"off","calendar":true},"etag":"abc123"}],"count":1,"etag":"\"listtag\""}`
	assertSameJSON(t, result, want)
	checkOutputDeclared(t, deckBoardsList, "boards", result)
	checkDeckDeclared(t, deckBoardsList, result)
}

func TestDeckBoardsGetReportsNestedFields(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, deckDocBoard), nil })
	result, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.boards.get", `{"board_id":"10"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(result, &out)
	labels := out["labels"].([]any)[0].(map[string]any)
	members := out["members"].([]any)[0].(map[string]any)
	if labels["board_id"] != "10" || labels["card_id"] != nil || members["primary_key"] != "admin" ||
		out["etag"] != "abc123" || out["shared"] != float64(0) {
		t.Errorf("result = %s", result)
	}
	checkDeckDeclared(t, deckBoardsGet, result)
}

func TestDeckBoardsListDetailsKeepsBindingAndShapesStacks(t *testing.T) {
	other := strings.Replace(strings.Replace(deckDocBoard, `"id":10,`, `"id":11,`, 1), `"Board title"`, `"Unbound"`, 1)
	gone := strings.Replace(strings.Replace(deckDocBoard, `"id":10,`, `"id":12,`, 1), `"deletedAt":0,"id"`, `"deletedAt":5,"id"`, 1)
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[`+deckDocBoard+`,`+other+`,`+gone+`]`), nil
	})
	result, err := deckInvoke(t, []string{"deck/10"}, "nextcloud.deck.boards.list", `{"details":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].url.RawQuery != "details=true" {
		t.Errorf("query = %q", (*calls)[0].url.RawQuery)
	}
	if strings.Contains(string(result), "Unbound") || strings.Contains(string(result), `"id":"12"`) {
		t.Errorf("an unbound or deleted board is reported: %s", result)
	}
	var listed struct {
		Boards []map[string]any `json:"boards"`
		Count  int              `json:"count"`
	}
	if err := json.Unmarshal(result, &listed); err != nil || listed.Count != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	entry := listed.Boards[0]
	stacks := entry["stacks"].([]any)
	if len(stacks) != 1 || !reflect.DeepEqual(stacks[0], map[string]any{"id": "4", "title": "ToDo", "order": float64(999)}) {
		t.Errorf("stacks = %v", stacks)
	}
	for _, key := range []string{"labels", "members", "acl"} {
		if list, _ := entry[key].([]any); len(list) != 1 {
			t.Errorf("%s = %v", key, entry[key])
		}
	}
	checkOutputDeclared(t, deckBoardsList, "boards", result)
	checkDeckDeclared(t, deckBoardsList, result)

	// Without details the entries carry no detail members and no query is sent.
	if result, err = deckInvoke(t, []string{"deck/10"}, "nextcloud.deck.boards.list", `{}`); err != nil ||
		(*calls)[1].url.RawQuery != "" || strings.Contains(string(result), `"stacks"`) || strings.Contains(string(result), `"labels"`) {
		t.Errorf("plain result = %s, query %q, %v", result, (*calls)[1].url.RawQuery, err)
	}
}

func TestDeckListFiltersReachTheRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, `[]`), nil })
	args := `"modified_since":"2018-11-05T10:28:00+01:00","etag":"\"tag-1\""`
	if _, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.boards.list", `{`+args+`}`); err != nil {
		t.Fatal(err)
	}
	if got := deckHeader(t, calls, "If-Modified-Since"); got != "Mon, 05 Nov 2018 09:28:00 GMT" {
		t.Errorf("If-Modified-Since = %q", got)
	}
	if got := deckHeader(t, calls, "If-None-Match"); got != `"tag-1"` {
		t.Errorf("If-None-Match = %q", got)
	}
	if _, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.stacks.list", `{"board_id":"2",`+args+`}`); err != nil {
		t.Fatal(err)
	}
	if deckHeader(t, calls, "If-Modified-Since") != "Mon, 05 Nov 2018 09:28:00 GMT" || deckHeader(t, calls, "If-None-Match") != `"tag-1"` {
		t.Errorf("stacks headers = %v", (*calls)[len(*calls)-1].header)
	}
	// Without the filters no precondition is sent.
	if _, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.boards.list", `{}`); err != nil {
		t.Fatal(err)
	}
	if h := (*calls)[len(*calls)-1].header; h.Get("If-Modified-Since") != "" || h.Get("If-None-Match") != "" {
		t.Errorf("headers = %v", h)
	}
}

func TestDeckListFiltersAreValidatedBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		t.Error("the provider was contacted")
		return nil, nil
	})
	for _, args := range []string{
		`"modified_since":"yesterday"`, `"modified_since":"2018-11-05 10:28:00+01:00"`, `"modified_since":"1960-01-01T00:00:00Z"`,
		`"etag":"a b"`, `"etag":"x\r\nHost: evil"`, `"etag":"` + strings.Repeat("a", 129) + `"`, `"details":"yes"`,
	} {
		if _, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.boards.list", `{`+args+`}`); err == nil {
			t.Errorf("boards.list accepted %s", args)
		}
		if strings.HasPrefix(args, `"details"`) {
			continue
		}
		if _, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.stacks.list", `{"board_id":"2",`+args+`}`); err == nil {
			t.Errorf("stacks.list accepted %s", args)
		}
	}
	if _, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.stacks.list", `{"board_id":"2","details":true}`); err == nil {
		t.Error("stacks.list accepted details")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d", len(*calls))
	}
}

func TestDeckNotModifiedIsReportedAsUnchanged(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusNotModified, "")
		response.Header.Set("ETag", `"fresh"`)
		return response, nil
	})
	for _, tc := range []struct{ op, args string }{
		{"nextcloud.deck.boards.list", `{"etag":"\"old\""}`},
		{"nextcloud.deck.stacks.list", `{"board_id":"2","modified_since":"2026-01-01T00:00:00Z"}`},
	} {
		result, err := deckInvoke(t, []string{"deck"}, tc.op, tc.args)
		if err != nil || !strings.Contains(string(result), `"unchanged":true`) || !strings.Contains(string(result), `"count":0`) ||
			!strings.Contains(string(result), `"etag":"\"fresh\""`) {
			t.Errorf("%s = %s, %v", tc.op, result, err)
		}
	}
	// A 304 without a precondition is not a result.
	if _, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.boards.list", `{}`); err == nil {
		t.Error("a 304 without a precondition was accepted")
	}
}

func TestDeckStacksAndCardsReportEveryDocumentedField(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/stacks"):
			return jsonResponse(http.StatusOK, `[`+deckDocStack+`]`), nil
		case strings.HasSuffix(request.URL.Path, "/cards/21"):
			return jsonResponse(http.StatusOK, deckDocCard), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return nil, nil
	})
	stacks, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.stacks.list", `{"board_id":"2"}`)
	if err != nil {
		t.Fatal(err)
	}
	card, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.cards.get", `{"board_id":"2","stack_id":"3","card_id":"21"}`)
	if err != nil {
		t.Fatal(err)
	}
	var listed DeckStacksResult
	if err := json.Unmarshal(stacks, &listed); err != nil {
		t.Fatal(err)
	}
	stack := listed.Stacks[0]
	if stack.BoardID != "2" || stack.LastModified != 1541426139 || stack.ETag != "stacktag" {
		t.Errorf("stack = %+v", stack)
	}
	var got DeckCard
	if err := json.Unmarshal(card, &got); err != nil {
		t.Fatal(err)
	}
	for _, c := range []DeckCard{stack.Cards[0], got} {
		if c.Type != "plain" || c.AttachmentCount == nil || *c.AttachmentCount != 1 || c.CommentsUnread == nil ||
			*c.CommentsUnread != 0 || c.Overdue == nil || *c.Overdue != 1 || c.ETag != "cardtag" ||
			c.Owner == nil || c.Owner.PrimaryKey != "admin" || c.Labels[0].BoardID != "2" || c.Labels[0].CardID != "21" {
			t.Errorf("card = %+v", c)
		}
		if len(c.Attachments) != 1 || c.Attachments[0].ID != "6" || c.Attachments[0].Name != "a.eml" ||
			c.Attachments[0].Size != 922258 || c.Attachments[0].ContentType != "application/octet-stream" ||
			c.Attachments[0].CreatedBy != "admin" || !c.Attachments[0].Downloadable {
			t.Errorf("attachments = %+v", c.Attachments)
		}
	}
	checkOutputDeclared(t, deckStacksList, "stacks", stacks)
	checkDeckDeclared(t, deckStacksList, stacks)
	checkDeckDeclared(t, deckCardsGet, card)
}

func TestDeckCardKeepsAbsentCountsOut(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[`+stackJSON("3", strings.Replace(cardJSON("21", "3", "d"), `"order":1`,
			`"order":1,"attachments":null,"attachmentCount":null,"commentsUnread":"x","overdue":null`, 1))+`]`), nil
	})
	result, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.stacks.list", `{"board_id":"2"}`)
	if err != nil {
		t.Fatal(err)
	}
	var listed DeckStacksResult
	if err := json.Unmarshal(result, &listed); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(listed.Stacks[0].Cards[0])
	var card map[string]any
	_ = json.Unmarshal(raw, &card)
	for _, key := range []string{"attachments", "attachment_count", "comments_unread", "overdue", "etag", "type"} {
		if _, ok := card[key]; ok {
			t.Errorf("%s is reported: %s", key, raw)
		}
	}
}

func TestDeckCommentsReportMentionsAndReply(t *testing.T) {
	parent := `{"id":175,"objectId":21,"message":"parent @carol","actorId":"admin","actorType":"users",` +
		`"actorDisplayName":"Administrator","creationDateTime":"2020-03-10T10:23:07+00:00",` +
		`"mentions":[{"mentionId":"carol","mentionType":"user","mentionDisplayName":"carol"}],` +
		`"replyTo":{"id":1,"message":"nested must stay out"}}`
	comment := `{"id":176,"objectId":21,"message":"answer","actorId":"dora","actorType":"users","actorDisplayName":"Dora",` +
		`"creationDateTime":"2020-03-11T10:23:07+00:00","mentions":[{"mentionId":"bob","mentionType":"user","mentionDisplayName":"Bob"}],` +
		`"replyTo":` + parent + `}`
	deckCommentServer(t, func(*http.Request) (*http.Response, error) { return ocsOK(`[` + comment + `]`), nil })
	result, err := deckCommentCall(t, "nextcloud.deck.comments.list", `{`+deckCommentRef+`}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"card_id":"21","comments":[{"comment_id":"176","parent_id":"175","author_id":"dora","author_name":"Dora",` +
		`"created":"2020-03-11T10:23:07+00:00","message":"answer","author_type":"users","card_id":"21",` +
		`"mentions":[{"id":"bob","type":"user","display_name":"Bob"}],` +
		`"reply_to":{"comment_id":"175","author_id":"admin","author_name":"Administrator",` +
		`"created":"2020-03-10T10:23:07+00:00","message":"parent @carol","author_type":"users","card_id":"21",` +
		`"mentions":[{"id":"carol","type":"user","display_name":"carol"}]}}],"count":1,"offset":0,"more":false}`
	assertSameJSON(t, result, want)
	checkOutputDeclared(t, deckCommentsList, "comments", result)
	checkDeckDeclared(t, deckCommentsList, result)
}

func TestDeckCommentsBoundTheMentions(t *testing.T) {
	var mentions []string
	for i := 0; i < maxDeckMentions+3; i++ {
		mentions = append(mentions, `{"mentionId":"u","mentionType":"user","mentionDisplayName":"U"}`)
	}
	comment := `{"id":3,"objectId":21,"message":"m","actorId":"d","mentions":[` + strings.Join(mentions, ",") + `]}`
	deckCommentServer(t, func(*http.Request) (*http.Response, error) { return ocsOK(`[` + comment + `]`), nil })
	result, err := deckCommentCall(t, "nextcloud.deck.comments.list", `{`+deckCommentRef+`}`)
	var list DeckCommentList
	if err != nil || json.Unmarshal(result, &list) != nil || len(list.Comments[0].Mentions) != maxDeckMentions || !list.Truncated {
		t.Errorf("result = %s, %v", result, err)
	}
}

func TestDeckPlainAnswersStayByteIdentical(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[`+board("1", "One")+`]`), nil
	})
	result, err := deckInvoke(t, []string{"deck"}, "nextcloud.deck.boards.list", `{}`)
	want := `{"boards":[{"id":"1","title":"One","color":"0087C5","archived":false,` +
		`"owner":{"id":"dora","name":"Dora","type":"user"}}],"count":1}`
	if err != nil {
		t.Fatal(err)
	}
	assertSameJSON(t, result, want)
	deckCommentServer(t, func(*http.Request) (*http.Response, error) {
		return ocsOK(`[{"id":3,"message":"hi","actorId":"dora","actorDisplayName":"Dora",` +
			`"creationDateTime":"2026-01-02T03:04:05+00:00","replyTo":null}]`), nil
	})
	result, err = deckCommentCall(t, "nextcloud.deck.comments.list", `{`+deckCommentRef+`}`)
	want = `{"card_id":"21","comments":[{"comment_id":"3","author_id":"dora","author_name":"Dora",` +
		`"created":"2026-01-02T03:04:05+00:00","message":"hi"}],"count":1,"offset":0,"more":false}`
	if err != nil {
		t.Fatal(err)
	}
	assertSameJSON(t, result, want)
}

// assertSameJSON compares two JSON documents by value; the framework writes members in sorted order.
func assertSameJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil || json.Unmarshal([]byte(want), &b) != nil || !reflect.DeepEqual(a, b) {
		t.Errorf("result =\n%s\nwant\n%s", got, want)
	}
}

// checkDeckDeclared walks a tool result and checks that every member at every depth is in the output schema at
// that place and that its name is listed in the fields of the tool.
func checkDeckDeclared(t *testing.T, d capability.Descriptor, result json.RawMessage) {
	t.Helper()
	var schema map[string]any
	var value any
	if err := json.Unmarshal(d.OutputSchema, &schema); err != nil || json.Unmarshal(result, &value) != nil {
		t.Fatalf("%s: schema or result unreadable", d.ID)
	}
	fields := map[string]bool{}
	for _, f := range d.Fields {
		fields[f.Name] = true
	}
	var walk func(path string, schema map[string]any, value any)
	walk = func(path string, schema map[string]any, value any) {
		switch v := value.(type) {
		case map[string]any:
			props, _ := schema["properties"].(map[string]any)
			for key, member := range v {
				sub, ok := props[key].(map[string]any)
				if !ok {
					t.Errorf("%s: %s%s is not in the output schema", d.ID, path, key)
					continue
				}
				if !fields[key] {
					t.Errorf("%s: %s%s is not in Fields", d.ID, path, key)
				}
				walk(path+key+".", sub, member)
			}
		case []any:
			items, _ := schema["items"].(map[string]any)
			for _, member := range v {
				walk(path, items, member)
			}
		}
	}
	walk("", schema, value)
}
