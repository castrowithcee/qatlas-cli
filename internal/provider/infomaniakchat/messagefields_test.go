package infomaniakchat

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	fieldPost   = postInChanA
	fieldFile   = fileInChanA
	fieldDelRep = "post00000000000000000d1d"
	fieldAddr   = "https://attacker.example/track?t=secret-address-canary"
)

// fieldFilePicture is a file as kChat documents it; the numbers and the flag come as the API sends them.
func fieldFilePicture(id, postID, extra string) string {
	return `{"id":"` + id + `","user_id":"user1","post_id":"` + postID + `","channel_id":"` + chanA + `",` +
		`"create_at":1735689600000,"update_at":1735689700000,"delete_at":0,"name":"pic.png","extension":"png",` +
		`"size":10,"mime_type":"image/png","width":640,"height":480,"has_preview_image":true` + extra + `}`
}

func fieldFileLegacy(id, postID string) string {
	return `{"id":"` + id + `","user_id":"user1","post_id":"` + postID + `","channel_id":"` + chanA +
		`","create_at":1735689600000,"delete_at":0,"name":"pic.png","extension":"png","size":10,` +
		`"mime_type":"image/png"}`
}

// fieldPostExtra holds the members of the Post and PostMetadata schema that the entries did not carry before.
func fieldPostExtra() string {
	return `"update_at":"1735689700000","original_id":"orig0001","hashtag":"#a","pending_post_id":"user1:1735689600000",` +
		`"file_ids":["file00000000000000000a0a","file1"],` +
		`"props":{"from_bot":"true","attachments":[{"text":"hi\u0007there","n":3}],"unset":null},` +
		`"metadata":{"embeds":[{"type":"opengraph","url":"` + fieldAddr + `","data":{"title":"T\u0001itle"}},` +
		`{"type":"image","url":"https://img.example/a.png"}],` +
		`"emojis":[{"id":"emo1","creator_id":"user1","name":"party","create_at":1735689600000,` +
		`"update_at":"1735689700000","delete_at":0}],` +
		`"files":[` + fieldFilePicture(fieldFile, fieldPost, "") + `,` +
		strings.Replace(fieldFilePicture("file2", fieldPost, ""), `"delete_at":0`, `"delete_at":5`, 1) + `],` +
		`"images":{"https://img.example/a.png":{"width":"640","height":480}},` +
		`"reactions":[{"user_id":"user2","post_id":"` + fieldPost + `","emoji_name":"smile","create_at":1735689600000}]}`
}

func fieldPostOf(id, channel, extra string) string {
	post := postJSONOf(id, channel, "", "user1", messageCanary, 1735689600000)
	if extra == "" {
		return post
	}
	return strings.TrimSuffix(post, "}") + "," + extra + "}"
}

const fieldPostWant = `{"id":"` + fieldPost + `","channel_id":"` + chanA + `","user_id":"user1",` +
	`"message":"` + messageCanary + `","type":"","created_at":"2025-01-01T00:00:00Z",` +
	`"updated_at":"2025-01-01T00:01:40Z","original_id":"orig0001","hashtag":"#a",` +
	`"pending_post_id":"user1:1735689600000","file_ids":["file00000000000000000a0a","file1"],` +
	`"props":{"from_bot":"true","attachments":[{"text":"hithere","n":3}]},` +
	`"metadata":{"embeds":[{"type":"opengraph","url":"` + fieldAddr + `","data":{"title":"Title"}},` +
	`{"type":"image","url":"https://img.example/a.png"}],` +
	`"emojis":[{"id":"emo1","creator_id":"user1","name":"party","created_at":"2025-01-01T00:00:00Z",` +
	`"updated_at":"2025-01-01T00:01:40Z"}],` +
	`"files":[` + fieldFileWant + `],` +
	`"images":{"https://img.example/a.png":{"width":640,"height":480}},` +
	`"reactions":[{"user_id":"user2","post_id":"` + fieldPost + `","emoji_name":"smile",` +
	`"created_at":"2025-01-01T00:00:00Z"}]}}`

const fieldFileWant = `{"id":"` + fieldFile + `","post_id":"` + fieldPost + `","user_id":"user1","name":"pic.png",` +
	`"extension":"png","size":10,"mime_type":"image/png","created_at":"2025-01-01T00:00:00Z",` +
	`"updated_at":"2025-01-01T00:01:40Z","width":640,"height":480,"has_preview_image":true}`

const fieldPostLegacyWant = `{"id":"` + fieldPost + `","channel_id":"` + chanA + `","user_id":"user1",` +
	`"message":"` + messageCanary + `","type":"","created_at":"2025-01-01T00:00:00Z"}`

const fieldFileLegacyWant = `{"id":"` + fieldFile + `","post_id":"` + fieldPost + `","user_id":"user1","name":"pic.png",` +
	`"extension":"png","size":10,"mime_type":"image/png","created_at":"2025-01-01T00:00:00Z"}`

// fieldServer answers every read of the message and file tools. post is the content of the one post that is
// named everywhere; the list answers add a deleted post and a post of another channel.
func fieldServer(t *testing.T, post, file string) func(*http.Request) (*http.Response, error) {
	deleted := strings.Replace(fieldPostOf(fieldDelRep, chanA, ""), `"edit_at":0`, `"edit_at":0,"delete_at":9`, 1)
	foreign := fieldPostOf(postInChanB, chanB, "")
	list := func() string {
		return postListJSONOf([]string{fieldPost, fieldDelRep, postInChanB}, []string{post, deleted, foreign}, false, "")
	}
	single := `{"order":["` + fieldPost + `"],"posts":{"` + fieldPost + `":` + post + `}}`
	threadList := `{"threads":[{"id":"` + fieldPost + `","reply_count":1,"last_reply_at":0,"last_viewed_at":0,` +
		`"participants":[],"post":` + post + `}],"total":1}`
	return func(r *http.Request) (*http.Response, error) {
		p := r.URL.Path
		switch {
		case p == "/api/v4/channels/"+chanA:
			return jsonResponse(200, channelJSONOf(chanA, teamA, "Channel A")), nil
		case p == "/api/v4/users/me/teams/"+teamA+"/channels":
			return jsonResponse(200, searchChannels()), nil
		case p == "/api/v4/posts/"+fieldPost:
			return jsonResponse(200, post), nil
		case p == "/api/v4/posts/"+fieldPost+"/thread" || p == "/api/v4/channels/"+chanA+"/posts":
			return jsonResponse(200, list()), nil
		case p == "/api/v4/channels/"+chanA+"/pinned" || p == "/api/v4/teams/"+teamA+"/posts/search":
			return jsonResponse(200, list()), nil
		case p == "/api/v4/users/me/posts/flagged":
			return jsonResponse(200, single), nil
		case p == "/api/v4/users/me/teams/"+teamA+"/threads":
			return jsonResponse(200, threadList), nil
		case p == "/api/v4/users/me/teams/"+teamA+"/threads/"+fieldPost:
			return jsonResponse(200, strings.TrimSuffix(strings.TrimPrefix(threadList, `{"threads":[`), `],"total":1}`)), nil
		case p == "/api/v4/posts/"+fieldPost+"/files/info":
			return jsonResponse(200, "["+file+"]"), nil
		case p == "/api/v4/files/"+fieldFile+"/info":
			return jsonResponse(200, file), nil
		case p == "/api/v4/teams/"+teamA+"/files/search":
			return jsonResponse(200, `{"order":["`+fieldFile+`"],"file_infos":{"`+fieldFile+`":`+file+`}}`), nil
		}
		t.Errorf("unexpected request %s %s", r.Method, p)
		return jsonResponse(404, `{}`), nil
	}
}

type fieldTool struct {
	id, args string
	// entries returns the message or file entries of a result.
	entries func(t *testing.T, result string) []json.RawMessage
	// schema returns the schema of one entry within the output schema.
	schema func(output map[string]any) map[string]any
	file   bool
	// root marks the thread tools, whose Fields name the root message as one member.
	root bool
}

func fieldTools() []fieldTool {
	member := func(name string) func(*testing.T, string) []json.RawMessage {
		return func(t *testing.T, result string) []json.RawMessage {
			var out map[string]json.RawMessage
			if err := json.Unmarshal([]byte(result), &out); err != nil {
				t.Fatal(err)
			}
			var list []json.RawMessage
			if err := json.Unmarshal(out[name], &list); err != nil {
				t.Fatalf("%s: %v in %s", name, err, result)
			}
			return list
		}
	}
	whole := func(_ *testing.T, result string) []json.RawMessage { return []json.RawMessage{json.RawMessage(result)} }
	root := func(t *testing.T, result string) []json.RawMessage {
		var out struct {
			Root    json.RawMessage                  `json:"root"`
			Threads []struct{ Root json.RawMessage } `json:"threads"`
		}
		if err := json.Unmarshal([]byte(result), &out); err != nil {
			t.Fatal(err)
		}
		if out.Root != nil {
			return []json.RawMessage{out.Root}
		}
		var roots []json.RawMessage
		for _, thread := range out.Threads {
			roots = append(roots, thread.Root)
		}
		return roots
	}
	object := func(schema map[string]any, name string) map[string]any {
		properties, _ := schema["properties"].(map[string]any)
		out, _ := properties[name].(map[string]any)
		return out
	}
	whole2 := func(output map[string]any) map[string]any { return output }
	items := func(name string) func(map[string]any) map[string]any {
		return func(output map[string]any) map[string]any {
			list := object(output, name)
			entry, _ := list["items"].(map[string]any)
			return entry
		}
	}
	rootSchema := func(output map[string]any) map[string]any {
		if r := object(output, "root"); r != nil {
			return r
		}
		return object(items("threads")(output), "root")
	}
	team := `{"team_id":"` + teamA + `"`
	return []fieldTool{
		{id: messagesList.ID, args: `{"channel_id":"` + chanA + `"}`, entries: member("messages"), schema: items("messages")},
		{id: messagesThread.ID, args: `{"post_id":"` + fieldPost + `"}`, entries: member("messages"), schema: items("messages")},
		{id: messagesGet.ID, args: `{"post_id":"` + fieldPost + `"}`, entries: whole, schema: whole2},
		{id: messagesSearch.ID, args: team + `,"terms":"x"}`, entries: member("messages"), schema: items("messages")},
		{id: pinsList.ID, args: `{"channel_id":"` + chanA + `"}`, entries: member("messages"), schema: items("messages")},
		{id: flaggedList.ID, args: team + `}`, entries: member("messages"), schema: items("messages")},
		{id: threadsList.ID, args: team + `}`, entries: root, schema: rootSchema, root: true},
		{id: threadsGet.ID, args: team + `,"thread_id":"` + fieldPost + `"}`, entries: root, schema: rootSchema, root: true},
		{id: messagesFiles.ID, args: `{"post_id":"` + fieldPost + `"}`, entries: member("files"), schema: items("files"), file: true},
		{id: filesInfo.ID, args: `{"file_id":"` + fieldFile + `"}`, entries: whole, schema: whole2, file: true},
		{id: filesSearch.ID, args: team + `,"terms":"x"}`, entries: member("files"), schema: items("files"), file: true},
	}
}

func equalJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("entry differs\n got %s\nwant %s", got, want)
	}
}

func TestEveryNewMessageAndFileFieldArrivesInAllTools(t *testing.T) {
	for _, tool := range fieldTools() {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, fieldServer(t, fieldPostOf(fieldPost, chanA, fieldPostExtra()),
				fieldFilePicture(fieldFile, fieldPost, "")))
			result, err := env.invoke(tool.id, "team", tool.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			entries := tool.entries(t, result)
			if len(entries) != 1 {
				t.Fatalf("entries = %d in %s", len(entries), result)
			}
			want := fieldPostWant
			if tool.file {
				want = fieldFileWant
			}
			equalJSON(t, string(entries[0]), want)
			for _, c := range calls {
				if c.host != "acme-support.kchat.infomaniak.com" || !strings.HasPrefix(c.path, "/api/v4/") {
					t.Errorf("request to an address of the answer: %+v", c)
				}
			}
		})
	}
}

func TestAnswersWithoutNewFieldsKeepTheFormerOutputByteForByte(t *testing.T) {
	// The former entry in the order the core writes its members.
	post := `{"channel_id":"` + chanA + `","created_at":"2025-01-01T00:00:00Z","id":"` + fieldPost + `","message":"` +
		messageCanary + `","type":"","user_id":"user1"}`
	file := `{"created_at":"2025-01-01T00:00:00Z","extension":"png","id":"` + fieldFile + `","mime_type":"image/png",` +
		`"name":"pic.png","post_id":"` + fieldPost + `","size":10,"user_id":"user1"}`
	for _, tool := range fieldTools() {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, fieldServer(t, fieldPostOf(fieldPost, chanA, ""),
				fieldFileLegacy(fieldFile, fieldPost)))
			result, err := env.invoke(tool.id, "team", tool.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			want := post
			if tool.file {
				want = file
			}
			entries := tool.entries(t, result)
			if len(entries) != 1 || string(entries[0]) != want {
				t.Fatalf("entries = %q, want [%s]", entries, want)
			}
		})
	}
}

func TestListAndThreadDropDeletedAndForeignPosts(t *testing.T) {
	for _, tool := range fieldTools()[:2] {
		var calls []call
		env := newEnvironment(t, &calls, fieldServer(t, fieldPostOf(fieldPost, chanA, ""), ""))
		result, err := env.invoke(tool.id, "team", tool.args)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		var out struct {
			Messages []MessageEntry `json:"messages"`
			Count    int            `json:"count"`
		}
		if err := json.Unmarshal([]byte(result), &out); err != nil || out.Count != 1 || len(out.Messages) != 1 ||
			out.Messages[0].ID != fieldPost {
			t.Fatalf("%s: result = %s, %v", tool.id, result, err)
		}
		if strings.Contains(result, fieldDelRep) || strings.Contains(result, postInChanB) {
			t.Errorf("%s: result holds a deleted or foreign post: %s", tool.id, result)
		}
	}
}

func TestFreeObjectsBeyondTheBoundAreLeftOutAndNamed(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 7) + `1` + strings.Repeat(`}`, 7) // seven levels
	wide := make([]string, 101)
	for i := range wide {
		wide[i] = `"k` + strconv.Itoa(i) + `":1`
	}
	long := `"` + strings.Repeat("x", 1025) + `"`
	big := make([]string, 20)
	for i := range big {
		big[i] = `"k` + strconv.Itoa(i) + `":"` + strings.Repeat("y", 900) + `"`
	}
	cases := map[string]string{
		"deep":   deep,
		"wide":   `{` + strings.Join(wide, ",") + `}`,
		"text":   `{"k":` + long + `}`,
		"key":    `{"` + strings.Repeat("k", 1025) + `":1}`,
		"array":  `{"k":[` + strings.TrimSuffix(strings.Repeat("1,", 101), ",") + `]}`,
		"size":   `{` + strings.Join(big, ",") + `}`,
		"scalar": `"text"`,
	}
	for name, props := range cases {
		t.Run(name, func(t *testing.T) {
			meta := `"metadata":{"embeds":[{"type":"link","url":"u","data":{"ok":1}},{"type":"opengraph","data":` + props + `}]}`
			post := fieldPostOf(fieldPost, chanA, `"props":`+props+`,`+meta)
			var calls []call
			env := newEnvironment(t, &calls, fieldServer(t, post, ""))
			result, err := env.invoke(messagesGet.ID, "team", `{"post_id":"`+fieldPost+`"}`)
			if err != nil {
				t.Fatal(err)
			}
			var out MessageEntry
			if err := json.Unmarshal([]byte(result), &out); err != nil {
				t.Fatal(err)
			}
			if out.Props != nil || !reflect.DeepEqual(out.Omitted, []string{"props", "metadata.embeds[1].data"}) {
				t.Fatalf("props = %v, omitted = %v", out.Props, out.Omitted)
			}
			if len(out.Metadata.Embeds) != 2 || out.Metadata.Embeds[0].Data["ok"] == nil || out.Metadata.Embeds[1].Data != nil ||
				out.Metadata.Embeds[1].Type != "opengraph" {
				t.Fatalf("embeds = %+v", out.Metadata.Embeds)
			}
		})
	}
}

func TestFreeObjectAtTheBoundIsKeptWithoutControlCharacters(t *testing.T) {
	nested := strings.Repeat(`{"a":`, 5) + `"v\u0000\u0007\tw\n"` + strings.Repeat(`}`, 5) // depth 6
	object, within := boundedFreeObject(json.RawMessage(`{"k":` + nested + `,"` + strings.Repeat("é", 512) + `":"` +
		strings.Repeat("é", 512) + `","n":12345678901234567890,"\u0001key":true}`))
	if !within || object == nil {
		t.Fatalf("object = %v, within = %v", object, within)
	}
	encoded, _ := json.Marshal(object)
	if !strings.Contains(string(encoded), `"v\tw\n"`) || strings.Contains(string(encoded), `\u0007`) ||
		!strings.Contains(string(encoded), `12345678901234567890`) || !strings.Contains(string(encoded), `"key":true`) {
		t.Fatalf("object = %s", encoded)
	}
	if object, within := boundedFreeObject(json.RawMessage(`{"a":null}`)); object != nil || !within {
		t.Fatalf("empty object = %v, %v", object, within)
	}
}

func TestMetadataListsBeyondTheBoundAreLeftOutWhole(t *testing.T) {
	many := func(item string, n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(item+",", n), ",") + "]"
	}
	images := make([]string, 51)
	for i := range images {
		images[i] = `"https://img.example/` + strconv.Itoa(i) + `":{"width":1,"height":1}`
	}
	meta := `"metadata":{"embeds":` + many(`{"type":"link"}`, 101) + `,"emojis":` + many(`{"id":"e"}`, 101) +
		`,"files":` + many(fieldFilePicture("f", fieldPost, ""), 101) + `,"reactions":` + many(`{"emoji_name":"x"}`, 101) +
		`,"images":{` + strings.Join(images, ",") + `}},"file_ids":` + many(`"f"`, 101)
	var calls []call
	env := newEnvironment(t, &calls, fieldServer(t, fieldPostOf(fieldPost, chanA, meta), ""))
	result, err := env.invoke(messagesGet.ID, "team", `{"post_id":"`+fieldPost+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out MessageEntry
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"file_ids", "metadata.embeds", "metadata.emojis", "metadata.files", "metadata.images",
		"metadata.reactions"}
	if out.Metadata != nil || out.FileIDs != nil || !reflect.DeepEqual(out.Omitted, want) {
		t.Fatalf("metadata = %+v, omitted = %v", out.Metadata, out.Omitted)
	}
}

func TestNumbersAreReadAsNumbersOrStringsAndOtherValuesDrop(t *testing.T) {
	post := fieldPostOf(fieldPost, chanA, `"update_at":"x","metadata":{"images":{"a":{"width":"7","height":true}}},`+
		`"file_ids":"bad","hashtag":5,"props":[]`)
	var calls []call
	env := newEnvironment(t, &calls, fieldServer(t, post, ""))
	result, err := env.invoke(messagesGet.ID, "team", `{"post_id":"`+fieldPost+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out MessageEntry
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.UpdatedAt != "" || out.Hashtag != "" || out.FileIDs != nil || out.Metadata.Images["a"].Width != 7 ||
		out.Metadata.Images["a"].Height != 0 || !reflect.DeepEqual(out.Omitted, []string{"props"}) {
		t.Fatalf("entry = %+v", out)
	}
}

func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	registry := registry(t)
	for _, tool := range fieldTools() {
		t.Run(tool.id, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, fieldServer(t, fieldPostOf(fieldPost, chanA, fieldPostExtra()),
				fieldFilePicture(fieldFile, fieldPost, "")))
			result, err := env.invoke(tool.id, "team", tool.args)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range registry.Provider(Provider) {
				if d.ID != tool.id {
					continue
				}
				var schema map[string]any
				if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
					t.Fatal(err)
				}
				fields := map[string]bool{}
				for _, f := range d.Fields {
					fields[f.Name] = true
				}
				entrySchema := tool.schema(schema)
				for _, raw := range tool.entries(t, result) {
					var entry map[string]any
					if err := json.Unmarshal(raw, &entry); err != nil {
						t.Fatal(err)
					}
					for key := range entry {
						if !fields[key] && !tool.root {
							t.Errorf("entry member %s is not in Fields", key)
						}
					}
					checkSchema(t, entry, entrySchema, "")
				}
				if tool.root && !fields["root"] {
					t.Error("root is not in Fields")
				}
				for key := range entrySchema["properties"].(map[string]any) {
					if !fields[key] && !tool.root {
						t.Errorf("schema member %s is not in Fields", key)
					}
				}
			}
		})
	}
}

// checkSchema walks an output next to its schema: every member must be a schema property. Free objects have
// no properties and are not checked.
func checkSchema(t *testing.T, value any, schema map[string]any, path string) {
	t.Helper()
	switch v := value.(type) {
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, item := range v {
			checkSchema(t, item, items, path+"[]")
		}
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		additional, _ := schema["additionalProperties"].(map[string]any)
		if properties == nil && additional == nil {
			return
		}
		for key, item := range v {
			sub, ok := properties[key].(map[string]any)
			if !ok {
				if sub, ok = additional, additional != nil; !ok {
					t.Errorf("%s.%s is not in the output schema", path, key)
					continue
				}
			}
			checkSchema(t, item, sub, path+"."+key)
		}
	}
}
