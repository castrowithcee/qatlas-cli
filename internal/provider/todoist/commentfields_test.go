package todoist

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// richComment is a comment in the style of the Todoist documentation with every member of its answer set and
// of its attachment, including the deletion mark that is never shown.
const richComment = `{"id":"noteOne","posted_uid":"1234567","item_id":"taskDue","content":"Looks good",` +
	`"file_attachment":{"file_name":"image.gif","file_type":"image/gif","resource_type":"file",` +
	`"file_url":"https://example.net/image.gif","image":"https://example.org/image.gif","file_size":7962,` +
	`"upload_state":"completed","image_width":90,"image_height":76,"file_duration":12,` +
	`"tn_s":["https://example.com/s.gif",96,96],"tn_m":["https://example.com/m.gif",288,288],` +
	`"tn_l":["https://example.com/l.gif",528,528]},"uids_to_notify":["1234567","7654321"],"is_deleted":false,` +
	`"posted_at":"2026-09-20T10:00:00Z","reactions":{"👍":["1234567","7654321"],"🎉":["7654321"]}}`

const richCommentOutput = `{"id":"noteOne","content":"Looks good","posted_at":"2026-09-20T10:00:00Z",` +
	`"posted_by":"1234567","attachment":{"file_name":"image.gif","file_type":"image/gif","resource_type":"file",` +
	`"file_size":7962,"upload_state":"completed","file_url":"https://example.net/image.gif",` +
	`"image":"https://example.org/image.gif","image_width":90,"image_height":76,"file_duration":12,` +
	`"tn_s":{"url":"https://example.com/s.gif","width":96,"height":96},` +
	`"tn_m":{"url":"https://example.com/m.gif","width":288,"height":288},` +
	`"tn_l":{"url":"https://example.com/l.gif","width":528,"height":528}},` +
	`"uids_to_notify":["1234567","7654321"],"reactions":{"👍":["1234567","7654321"],"🎉":["7654321"]}}`

const richReminder = `{"id":"remOne","item_id":"taskDue","notify_uid":"1234567","type":"absolute","is_urgent":true,` +
	`"is_deleted":false,"minute_offset":null,"due":{"date":"2026-09-22T09:00:00","timezone":"Europe/Madrid",` +
	`"is_recurring":false,"string":"tomorrow 9am","lang":"en"}}`

const richReminderOutput = `{"id":"remOne","task_id":"taskDue","type":"absolute","due":{"date":"2026-09-22T09:00:00",` +
	`"string":"tomorrow 9am","is_recurring":false,"timezone":"Europe/Madrid","lang":"en"},"is_urgent":true,` +
	`"notify_uid":"1234567"}`

const richFilter = `{"id":"f1","name":"Important","query":"priority 1","description":"urgent work","color":"red",` +
	`"item_order":1,"order_key":"a3","is_deleted":false,"is_favorite":true,"is_frozen":false}`

const richFilterOutput = `{"id":"f1","name":"Important","query":"priority 1","description":"urgent work",` +
	`"color":"red","order":1,"is_favorite":true,"is_frozen":false,"order_key":"a3"}`

// answering serves the comment, the reminder, and the filter on every route that shows them.
func answering(comment, reminder, filter string) *fakeTodoist {
	return &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		get := r.Method == http.MethodGet
		w.Header().Set("Content-Type", "application/json")
		switch {
		case get && path == "/comments":
			fmt.Fprintf(w, `{"results":[%s],"next_cursor":null}`, comment)
		case !get && (path == "/comments" || path == "/comments/noteOne"):
			fmt.Fprint(w, comment)
		case get && path == "/reminders":
			fmt.Fprintf(w, `{"results":[%s],"next_cursor":null}`, reminder)
		case path == "/reminders/remOne" || (!get && path == "/reminders"):
			fmt.Fprint(w, reminder)
		case r.Method == http.MethodPost && path == "/sync":
			fmt.Fprintf(w, `{"full_sync":true,"sync_token":"abc","filters":[%s]}`, filter)
		default:
			return false
		}
		return true
	}}
}

var commentShowingTools = []struct{ tool, connection, arguments string }{
	{"todoist.comments.list", "one", `{"task_id":"taskDue"}`},
	{"todoist.comments.create", "writer", `{"task_id":"taskDue","content":"x"}`},
	{"todoist.comments.update", "writer", `{"comment_id":"noteOne","content":"x"}`},
}

var reminderShowingTools = []struct{ tool, connection, arguments string }{
	{"todoist.reminders.list", "one", `{"task_id":"taskDue"}`},
	{"todoist.reminders.get", "one", `{"reminder_id":"remOne"}`},
	{"todoist.reminders.create", "writer", `{"task_id":"taskDue","minute_offset":30}`},
	{"todoist.reminders.update", "writer", `{"reminder_id":"remOne","minute_offset":30}`},
}

func TestCommentReminderAndFilterToolsShowTheDocumentedAnswerExactly(t *testing.T) {
	check := func(t *testing.T, tool, connection, arguments, key, want string) {
		env := newEnvironment(t, answering(richComment, richReminder, richFilter))
		result, got := shown(t, env, tool, connection, arguments, key)
		if strings.Contains(result, "is_deleted") {
			t.Errorf("result carries is_deleted: %s", result)
		}
		var expected map[string]any
		if err := json.Unmarshal([]byte(want), &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("answer = %v\nwant     %v", got, expected)
		}
	}
	for _, tt := range commentShowingTools {
		t.Run(tt.tool, func(t *testing.T) { check(t, tt.tool, tt.connection, tt.arguments, "comments", richCommentOutput) })
	}
	for _, tt := range reminderShowingTools {
		t.Run(tt.tool, func(t *testing.T) {
			check(t, tt.tool, tt.connection, tt.arguments, "reminders", richReminderOutput)
		})
	}
	t.Run("todoist.filters.list", func(t *testing.T) {
		check(t, "todoist.filters.list", "account", `{}`, "filters", richFilterOutput)
	})
}

func TestEveryCommentReminderAndFilterMemberIsInSchemaAndFields(t *testing.T) {
	reg := registry(t)
	check := func(t *testing.T, tool, connection, arguments, key string, member map[string]any) {
		var fields map[string]bool
		var properties map[string]any
		for _, d := range reg.Provider(Provider) {
			if d.ID != tool {
				continue
			}
			var schema map[string]any
			if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			properties, _ = schema["properties"].(map[string]any)
			if list, ok := properties[key].(map[string]any); ok {
				properties = list["items"].(map[string]any)["properties"].(map[string]any)
			}
			fields = map[string]bool{}
			for _, f := range d.Fields {
				fields[f.Name] = true
			}
		}
		var problems []string
		for name := range member {
			if _, ok := properties[name]; !ok || !fields[name] {
				problems = append(problems, name+" (output)")
			}
		}
		for name := range properties {
			if !fields[name] {
				problems = append(problems, name+" (schema)")
			}
		}
		if _, ok := properties["is_deleted"]; ok || fields["is_deleted"] {
			problems = append(problems, "is_deleted (published)")
		}
		sort.Strings(problems)
		if len(problems) > 0 {
			t.Errorf("not in schema or fields: %v", problems)
		}
	}
	// Every member of the rich answer must be present, so the check covers the whole set.
	full := func(t *testing.T, tool, connection, arguments, key string, wantMembers int) map[string]any {
		env := newEnvironment(t, answering(richComment, richReminder, richFilter))
		_, member := shown(t, env, tool, connection, arguments, key)
		if len(member) < wantMembers {
			t.Fatalf("%s showed %d members, want at least %d", tool, len(member), wantMembers)
		}
		return member
	}
	for _, tt := range commentShowingTools {
		t.Run(tt.tool, func(t *testing.T) {
			check(t, tt.tool, tt.connection, tt.arguments, "comments", full(t, tt.tool, tt.connection, tt.arguments, "comments", 7))
		})
	}
	for _, tt := range reminderShowingTools {
		t.Run(tt.tool, func(t *testing.T) {
			check(t, tt.tool, tt.connection, tt.arguments, "reminders", full(t, tt.tool, tt.connection, tt.arguments, "reminders", 6))
		})
	}
	t.Run("todoist.filters.list", func(t *testing.T) {
		check(t, "todoist.filters.list", "account", `{}`, "filters", full(t, "todoist.filters.list", "account", `{}`, "filters", 9))
	})
	// The attachment members and the reactions flag are in the schema as well.
	t.Run("nested", func(t *testing.T) {
		for _, name := range []string{"file_size", "upload_state", "file_url", "image", "image_width", "image_height",
			"file_duration", "tn_s", "tn_m", "tn_l"} {
			if !strings.Contains(attachmentSchema, `"`+name+`"`) {
				t.Errorf("attachment schema lacks %s", name)
			}
		}
		for _, name := range []string{"uids_to_notify", "uids_to_notify_truncated", "reactions", "reactions_truncated"} {
			if !strings.Contains(commentSchema, `"`+name+`"`) {
				t.Errorf("comment schema lacks %s", name)
			}
		}
	})
}

func TestAttachmentAddressRule(t *testing.T) {
	shown := []string{"https://example.net/a.png", "http://example.com/a", "HTTPS://Example.ORG/a?x=1#y",
		"https://nottodoist.com/a", "https://todoist.com.example.net/a", "https://example.net:8443/a",
		"https://todoist.co/a", "https://xtodoist.com/a"}
	for _, address := range shown {
		if got := shownAddress(address); got != address {
			t.Errorf("shownAddress(%q) = %q, want it shown", address, got)
		}
	}
	hidden := []string{"", "https://files.todoist.com/a", "https://todoist.com/a", "https://TODOIST.COM./a",
		"https://Files.Todoist.Com/a", "https://a.b.todoist.com./a", "https://todoist.com:443/a",
		"https://user@example.net/a", "https://user:pw@example.net/a", "https://@example.net/a",
		"ftp://example.net/a", "javascript:alert(1)", "data:text/plain,x", "file:///etc/passwd", "/relative/a",
		"//example.net/a", "example.net/a", "https:example.net/a", "https://", "https:///a", "https://exa mple.net/a",
		"https://example.net/a\nb", "https://example.net\\@files.todoist.com/a", "https://example.net%2f" + "@todoist.com/a",
		"https://todoist%2ecom/a", "https://files.todoist.com%2e/a", "https://tödoist.com/a", "https://todoist。com/a",
		"https://example.net/" + strings.Repeat("a", maxAddressBytes), "http://[::1", "%zz", "mailto:a@example.net"}
	for _, address := range hidden {
		if got := shownAddress(address); got != "" {
			t.Errorf("shownAddress(%q) = %q, want it left out", address, got)
		}
	}
	if got := shownAddress("https://example.net/" + strings.Repeat("a", maxAddressBytes-len("https://example.net/"))); got == "" {
		t.Error("an address of exactly the bound was left out")
	}
}

func commentWith(attachment string) string {
	return `{"id":"noteOne","item_id":"taskDue","content":"c","file_attachment":` + attachment + `}`
}

func listedComment(t *testing.T, body string) (string, Comment) {
	t.Helper()
	env := newEnvironment(t, answering(body, richReminder, richFilter))
	result, err := env.invoke("todoist.comments.list", "one", `{"task_id":"taskDue"}`)
	if err != nil {
		t.Fatal(err)
	}
	var list CommentList
	if err := json.Unmarshal([]byte(result), &list); err != nil || len(list.Comments) != 1 {
		t.Fatalf("comments = %s, %v", result, err)
	}
	return result, list.Comments[0]
}

func TestAttachmentAddressesFollowTheRuleEndToEnd(t *testing.T) {
	result, comment := listedComment(t, commentWith(`{"file_name":"a","file_url":"https://files.todoist.com/secret-link",`+
		`"image":"javascript:alert(1)","tn_s":["https://TODOIST.COM./s.png",96,48],`+
		`"tn_m":["ftp://example.net/m.png",288,144],"tn_l":["https://user@example.net/l.png",528,264]}`))
	for _, leaked := range []string{"secret-link", "javascript", "TODOIST", "ftp:", "user@", "file_url", `"image"`} {
		if strings.Contains(result, leaked) {
			t.Errorf("result carries %q: %s", leaked, result)
		}
	}
	a := comment.Attachment
	for name, tn := range map[string]*Thumbnail{"tn_s": a.TnS, "tn_m": a.TnM, "tn_l": a.TnL} {
		if tn == nil || tn.URL != "" || tn.Width == nil || tn.Height == nil {
			t.Errorf("%s = %+v, want the sizes without the address", name, tn)
		}
	}
	if *a.TnS.Width != 96 || *a.TnS.Height != 48 || *a.TnL.Width != 528 || *a.TnL.Height != 264 {
		t.Errorf("sizes changed: %+v %+v", a.TnS, a.TnL)
	}
	_, comment = listedComment(t, commentWith(`{"file_url":"https://nottodoist.com/a","image":"https://example.net/i"}`))
	if comment.Attachment.FileURL != "https://nottodoist.com/a" || comment.Attachment.Image != "https://example.net/i" {
		t.Errorf("external addresses = %+v", comment.Attachment)
	}
}

func TestAttachmentNumbersStayWholeAndOddValuesAreLeftOut(t *testing.T) {
	result, comment := listedComment(t, commentWith(`{"file_name":"a","file_size":9007199254740993,"image_width":0,`+
		`"image_height":1.5,"file_duration":"12","upload_state":"p\u0007ending","tn_s":"x","tn_m":[7,"a",null],`+
		`"tn_l":[]}`))
	a := comment.Attachment
	if a.FileSize == nil || *a.FileSize != 9007199254740993 || a.ImageWidth == nil || *a.ImageWidth != 0 ||
		a.ImageHeight != nil || a.FileDuration != nil || a.UploadState != "p ending" || a.TnS != nil || a.TnM != nil ||
		a.TnL != nil {
		t.Errorf("attachment = %s", result)
	}
	if strings.Contains(result, `"tn_`) || strings.Contains(result, "file_duration") {
		t.Errorf("odd members shown: %s", result)
	}
}

func reactionsBody(reactions string) string {
	return `{"id":"noteOne","item_id":"taskDue","content":"c","reactions":` + reactions + `}`
}

func TestReactionsAreShownOnlyWithinTheirBounds(t *testing.T) {
	users := func(n int) string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf(`"%d"`, 1000+i)
		}
		return "[" + strings.Join(ids, ",") + "]"
	}
	emoji := func(n int) string {
		entries := make([]string, n)
		for i := range entries {
			entries[i] = fmt.Sprintf(`"e%d":["1"]`, i)
		}
		return "{" + strings.Join(entries, ",") + "}"
	}
	tests := []struct {
		name, reactions string
		shown, flagged  bool
	}{
		{"50 emoji", emoji(maxReactionEmoji), true, false},
		{"51 emoji", emoji(maxReactionEmoji + 1), false, true},
		{"100 users", `{"👍":` + users(maxReactionUsers) + `}`, true, false},
		{"101 users", `{"👍":` + users(maxReactionUsers+1) + `}`, false, true},
		{"32 characters", `{"` + strings.Repeat("ä", maxEmojiChars) + `":["1"]}`, true, false},
		{"33 characters", `{"` + strings.Repeat("ä", maxEmojiChars+1) + `":["1"]}`, false, true},
		{"one oversized among valid", `{"👍":["1"],"` + strings.Repeat("x", 33) + `":["1"]}`, false, true},
		{"emoji that clash after cleaning", `{"a\u0007":["1"],"a\u0008":["2"]}`, false, true},
		{"none", `{}`, false, false},
		{"null", `null`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, comment := listedComment(t, reactionsBody(tt.reactions))
			if (len(comment.Reactions) > 0) != tt.shown || comment.ReactionsTruncated != tt.flagged {
				t.Errorf("reactions shown %v, flagged %v; want %v, %v: %.300s", len(comment.Reactions) > 0,
					comment.ReactionsTruncated, tt.shown, tt.flagged, result)
			}
		})
	}
	_, comment := listedComment(t, reactionsBody(`{"a\u0007b":["12\n3"]}`))
	if got := comment.Reactions["a b"]; len(got) != 1 || got[0] != "12 3" {
		t.Errorf("reactions = %v, want cleaned emoji and identifiers", comment.Reactions)
	}
}

func TestNotifiedUsersAreBoundedAndMarked(t *testing.T) {
	ids := func(n int) string {
		entries := make([]string, n)
		for i := range entries {
			entries[i] = fmt.Sprintf(`"%d"`, 1000+i)
		}
		return "[" + strings.Join(entries, ",") + "]"
	}
	_, comment := listedComment(t, `{"id":"noteOne","item_id":"taskDue","content":"c","uids_to_notify":`+ids(maxNotified)+`}`)
	if len(comment.UIDsToNotify) != maxNotified || comment.UIDsToNotifyTruncated {
		t.Errorf("at the bound: %d ids, truncated %v", len(comment.UIDsToNotify), comment.UIDsToNotifyTruncated)
	}
	_, comment = listedComment(t, `{"id":"noteOne","item_id":"taskDue","content":"c","uids_to_notify":`+ids(maxNotified+5)+`}`)
	if len(comment.UIDsToNotify) != maxNotified || !comment.UIDsToNotifyTruncated || comment.UIDsToNotify[0] != "1000" {
		t.Errorf("above the bound: %d ids, truncated %v", len(comment.UIDsToNotify), comment.UIDsToNotifyTruncated)
	}
}

func TestAnswerWithoutTheNewCommentReminderAndFilterMembersStaysAsItWas(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	result, err := env.invoke("todoist.comments.list", "one", `{"task_id":"taskDue"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"comments":[{"id":"noteOne","content":"Looks good","posted_at":"2026-09-20T10:00:00Z",` +
		`"posted_by":"1234567","attachment":{"file_name":"plan.pdf","file_type":"application/pdf",` +
		`"resource_type":"file"}}],"has_more":false}`
	if !sameJSON(result, want) {
		t.Errorf("comments = %s\nwant     %s", result, want)
	}
	result, err = env.invoke("todoist.filters.list", "account", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want = `{"filters":[{"id":"f1","name":"Important","query":"priority 1","description":"urgent work","color":"red",` +
		`"order":1,"is_favorite":true,"is_frozen":false},{"id":"f2","name":"Later","query":"no date","color":"grey",` +
		`"order":2,"is_favorite":false,"is_frozen":false}]}`
	if !sameJSON(result, want) {
		t.Errorf("filters = %s\nwant     %s", result, want)
	}
	env = newEnvironment(t, &fakeTodoist{})
	if _, err = env.invoke("todoist.filters.list", "account", `{}`); err != nil {
		t.Fatal(err)
	}
	if requests := env.fake.recorded(); len(requests) != 1 || requests[0].path != "/api/v1/sync" ||
		requests[0].form.Get("resource_types") != `["filters"]` {
		t.Errorf("requests = %+v, want one sync of filters", requests)
	}
}

func TestOrderKeyOfAFilterIsBoundedAndCleaned(t *testing.T) {
	filter := `{"id":"f1","name":"n","query":"q","item_order":1,"order_key":"a\u0007b` + strings.Repeat("ä", 200) + `"}`
	env := newEnvironment(t, answering(richComment, richReminder, filter))
	result, err := env.invoke("todoist.filters.list", "account", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var list FilterList
	if err := json.Unmarshal([]byte(result), &list); err != nil || len(list.Filters) != 1 {
		t.Fatalf("filters = %s, %v", result, err)
	}
	if key := list.Filters[0].OrderKey; !strings.HasPrefix(key, "a b") || len(key) > maxOrderKey {
		t.Errorf("order_key = %q (%d bytes)", key, len(key))
	}
	filter = `{"id":"f1","name":"n","query":"q","item_order":0,"order_key":null}`
	env = newEnvironment(t, answering(richComment, richReminder, filter))
	if result, _ = env.invoke("todoist.filters.list", "account", `{}`); strings.Contains(result, "order_key") {
		t.Errorf("absent order_key shown: %s", result)
	}
}

func TestReminderNotifyUserIsBoundedAndAbsentStaysOut(t *testing.T) {
	reminder := `{"id":"remOne","item_id":"taskDue","notify_uid":"` + strings.Repeat("9", 200) + `","type":"relative",` +
		`"minute_offset":0,"is_urgent":false}`
	env := newEnvironment(t, answering(richComment, reminder, richFilter))
	result, err := env.invoke("todoist.reminders.get", "one", `{"reminder_id":"remOne"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got Reminder
	if err := json.Unmarshal([]byte(result), &got); err != nil || got.NotifyUID != strings.Repeat("9", maxTagText) {
		t.Errorf("reminder = %s, %v", result, err)
	}
	reminder = `{"id":"remOne","item_id":"taskDue","notify_uid":null,"type":"relative","minute_offset":0,"is_urgent":false}`
	env = newEnvironment(t, answering(richComment, reminder, richFilter))
	if result, _ = env.invoke("todoist.reminders.get", "one", `{"reminder_id":"remOne"}`); strings.Contains(result, "notify_uid") {
		t.Errorf("absent notify_uid shown: %s", result)
	}
}
