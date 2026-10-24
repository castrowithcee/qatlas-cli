package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// documentedMedia carries every member of the Bot API media types, with file_path and the blocked members as
// canaries. The thumbnails reuse one shape, so each one differs by its identifier.
const documentedMedia = `"animation":{"file_id":"ANIM","file_unique_id":"UANIM","width":320,"height":240,"duration":3,` +
	`"thumbnail":{"file_id":"ANIMTHUMB","file_unique_id":"UANIMTHUMB","width":90,"height":60,"file_size":11},` +
	`"file_name":"a.gif","mime_type":"video/mp4","file_size":1000,"invite_link":"CANARY"},` +
	`"audio":{"file_id":"AUDIO","file_unique_id":"UAUDIO","duration":200,"performer":"Artist","title":"Track",` +
	`"file_name":"t.mp3","mime_type":"audio/mpeg","file_size":2000,` +
	`"thumbnail":{"file_id":"AUDIOTHUMB","file_unique_id":"UAUDIOTHUMB","width":50,"height":50,"file_size":5}},` +
	`"document":{"file_id":"DOC","file_unique_id":"UDOC",` +
	`"thumbnail":{"file_id":"DOCTHUMB","file_unique_id":"UDOCTHUMB","width":10,"height":12,"file_size":3},` +
	`"file_name":"d.pdf","mime_type":"application/pdf","file_size":3000,"passport_data":{"data":"CANARY"}},` +
	`"live_photo":{"photo":[{"file_id":"LIVEPHOTO1","file_unique_id":"ULIVEPHOTO1","width":1,"height":2,"file_size":3},` +
	`{"file_id":"LIVEPHOTO2","file_unique_id":"ULIVEPHOTO2","width":4,"height":5,"file_size":6}],` +
	`"file_id":"LIVE","file_unique_id":"ULIVE","width":720,"height":1280,"duration":2,"mime_type":"video/mp4","file_size":4000},` +
	`"photo":[{"file_id":"PHOTOSMALL","file_unique_id":"UPHOTOSMALL","width":90,"height":60,"file_size":10},` +
	`{"file_id":"PHOTOBIG","file_unique_id":"UPHOTOBIG","width":1280,"height":960,"file_size":99}],` +
	`"sticker":{"file_id":"STICKER","file_unique_id":"USTICKER","type":"mask","width":512,"height":512,` +
	`"is_animated":false,"is_video":true,` +
	`"thumbnail":{"file_id":"STICKERTHUMB","file_unique_id":"USTICKERTHUMB","width":128,"height":128,"file_size":7},` +
	`"emoji":"😀","set_name":"pack","premium_animation":{"file_id":"PREMIUM","file_unique_id":"UPREMIUM","file_size":8,"file_path":"FILEPATH"},` +
	`"mask_position":{"point":"eyes","x_shift":0,"y_shift":-0.25,"scale":1.3333333333333333},` +
	`"custom_emoji_id":"EMOJI","needs_repainting":true,"file_size":5000},` +
	`"video":{"file_id":"VIDEO","file_unique_id":"UVIDEO","width":1920,"height":1080,"duration":60,` +
	`"thumbnail":{"file_id":"VIDEOTHUMB","file_unique_id":"UVIDEOTHUMB","width":320,"height":180,"file_size":9},` +
	`"cover":[{"file_id":"COVER","file_unique_id":"UCOVER","width":640,"height":360,"file_size":13}],` +
	`"start_timestamp":12,"qualities":[{"file_id":"QUALITY","file_unique_id":"UQUALITY","width":1280,"height":720,"codec":"h264","file_size":14}],` +
	`"file_name":"v.mp4","mime_type":"video/mp4","file_size":6000},` +
	`"video_note":{"file_id":"NOTE","file_unique_id":"UNOTE","length":240,"duration":15,` +
	`"thumbnail":{"file_id":"NOTETHUMB","file_unique_id":"UNOTETHUMB","width":240,"height":240,"file_size":15},"file_size":7000},` +
	`"voice":{"file_id":"VOICE","file_unique_id":"UVOICE","duration":8,"mime_type":"audio/ogg","file_size":8000}`

var mediaIDs = []string{"ANIM", "ANIMTHUMB", "AUDIO", "AUDIOTHUMB", "DOC", "DOCTHUMB", "LIVEPHOTO1", "LIVEPHOTO2", "LIVE",
	"PHOTOSMALL", "PHOTOBIG", "STICKER", "STICKERTHUMB", "PREMIUM", "VIDEO", "VIDEOTHUMB", "COVER", "QUALITY",
	"NOTE", "NOTETHUMB", "VOICE"}

// expectedMediaItems is what the Bot API objects of documentedMedia print as: kind added, file_path and the
// blocked members removed. Identifiers stay in place; restoreFileIDs maps the output back onto them.
func expectedMediaItems(t *testing.T) []any {
	t.Helper()
	var message map[string]any
	if err := json.Unmarshal([]byte(`{`+documentedMedia+`}`), &message); err != nil {
		t.Fatal(err)
	}
	var strip func(v any)
	strip = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for name := range x {
				if isBlockedField(name) {
					delete(x, name)
				}
			}
			for _, child := range x {
				strip(child)
			}
		case []any:
			for _, child := range x {
				strip(child)
			}
		}
	}
	strip(message)
	var items []any
	for _, kind := range []string{"animation", "audio", "document", "live_photo", "photo", "sticker", "video", "video_note", "voice"} {
		if kind == "photo" {
			for _, size := range message["photo"].([]any) {
				size.(map[string]any)["kind"] = "photo"
				items = append(items, size)
			}
			continue
		}
		message[kind].(map[string]any)["kind"] = kind
		items = append(items, message[kind])
	}
	return items
}

// restoreFileIDs replaces every file_ref by the file_id behind it, after both check stages of the reference.
func restoreFileIDs(t *testing.T, client *Client, resolved *config.Resolved, binding string, v any) any {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for name, child := range x {
			if name == "file_ref" {
				parsed, err := parseRef(resolved, refFile, child.(string))
				if err != nil || parsed.binding != binding {
					t.Fatalf("file_ref = %+v, %v", parsed, err)
				}
				id, err := client.checkRef(parsed)
				if err != nil {
					t.Fatal(err)
				}
				delete(x, name)
				x["file_id"] = id
				continue
			}
			x[name] = restoreFileIDs(t, client, resolved, binding, child)
		}
	case []any:
		for i, child := range x {
			x[i] = restoreFileIDs(t, client, resolved, binding, child)
		}
	}
	return v
}

func assertNoIdentifiers(t *testing.T, encoded string) {
	t.Helper()
	for _, forbidden := range []string{`"file_id"`, `"file_path"`, "FILEPATH", "CANARY", "invite_link", "passport_data",
		"small_file_id", "big_file_id"} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("output contains %q: %s", forbidden, encoded)
		}
	}
	for _, id := range mediaIDs {
		if strings.Contains(encoded, `"`+id+`"`) {
			t.Errorf("output contains the identifier %s: %s", id, encoded)
		}
	}
}

func jsonRoundTrip(t *testing.T, v any) any {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUpdatesListMapsEveryMediaMember(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{"message_id":2,"date":3,"chat":{"id":-100},"invite_link":"CANARY",` + documentedMedia + `}}]`
	client, resolved := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-100")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].method != "getUpdates" {
		t.Fatalf("calls = %+v", calls)
	}
	encoded, _ := json.Marshal(got)
	assertNoIdentifiers(t, string(encoded))
	list := got["updates"].([]updateOut)
	if len(list) != 1 || len(list[0].OmittedFields) != 0 {
		t.Fatalf("updates = %+v", list)
	}
	items := restoreFileIDs(t, client, resolved, "-100", jsonRoundTrip(t, list[0].MediaItems))
	if want := jsonRoundTrip(t, expectedMediaItems(t)); !reflect.DeepEqual(items, want) {
		gotJSON, _ := json.MarshalIndent(items, "", " ")
		wantJSON, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("media_items =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
	// The summary keeps its former value: the largest photo.
	if m := list[0].Media; m == nil || m.Kind != "photo" || m.Size != 99 || m.FileRef == "" {
		t.Errorf("media = %+v", m)
	}
}

func TestMaskPositionPassesFloatsThroughUnrounded(t *testing.T) {
	const mask = `{"point":"chin","x_shift":0,"y_shift":0.30000000000000004,"scale":2.5e-7}`
	var s sticker
	if err := json.Unmarshal([]byte(`{"file_id":"X","mask_position":`+mask+`}`), &s); err != nil {
		t.Fatal(err)
	}
	var omitted []string
	encoded, _ := json.Marshal(fileRefFunc(nil).sticker(s, &omitted).MaskPosition)
	var got, want map[string]any
	_ = json.Unmarshal(encoded, &got)
	_ = json.Unmarshal([]byte(mask), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mask_position = %s, want %s", encoded, mask)
	}
}

func TestMediaFileRefsRedeemLikeTheSummaryReference(t *testing.T) {
	var calls []recorded
	transport := func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		calls = append(calls, recorded{method, string(data)})
		if method == "getUpdates" {
			return response(200, `{"ok":true,"result":[{"update_id":1,"message":{"message_id":2,"chat":{"id":-100},`+documentedMedia+`}}]}`), nil
		}
		return response(200, `{"ok":true,"result":{"file_id":"x","file_unique_id":"U","file_size":3,"file_path":"p"}}`), nil
	}
	client, resolved := multiClient(t, testToken, roundTripFunc(transport), "-100")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	var thumbnail string
	for _, item := range got["updates"].([]updateOut)[0].MediaItems {
		if a, ok := item.(animation); ok {
			thumbnail = a.Thumbnail.FileRef
		}
	}
	parsed, err := parseRef(resolved, refFile, thumbnail)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.fileMetadata(context.Background(), parsed); err != nil {
		t.Fatal(err)
	}
	if last := calls[len(calls)-1]; last.method != "getFile" || last.body != `{"file_id":"ANIMTHUMB"}` {
		t.Errorf("last call = %+v", last)
	}
	other, _ := multiClient(t, "999:other-token", nil, "-100")
	if _, err := other.checkRef(parsed); err == nil {
		t.Error("a reference of another token passed")
	}
}

func TestBusinessMediaItemsCarryNoReferencesAndNoRawChat(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"business_message":{"message_id":5,"business_connection_id":"b1","date":100,` +
		`"chat":{"id":777123,"type":"private"},` + documentedMedia + `}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "business/b1")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	assertNoIdentifiers(t, string(encoded))
	for _, forbidden := range []string{`"file_ref"`, "777123"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("output contains %q: %s", forbidden, encoded)
		}
	}
	list := got["updates"].([]updateOut)
	if len(list) != 1 || len(list[0].MediaItems) != 10 || list[0].BusinessChatRef == "" || list[0].Media == nil {
		t.Fatalf("updates = %s", encoded)
	}
}

func TestOversizeNestedMediaObjectsAreOmittedAndNamed(t *testing.T) {
	qualities := func(n, idLength int) string {
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, `{"file_id":"`+strings.Repeat("q", idLength)+`","file_unique_id":"U","width":1}`)
		}
		return `[` + strings.Join(parts, ",") + `]`
	}
	sizes := func(n int) string {
		return `[` + strings.TrimSuffix(strings.Repeat(`{"file_id":"P","width":1},`, n), ",") + `]`
	}
	tests := []struct {
		name, message string
		omitted       string
		items         int
	}{
		{"too many bytes", `"video":{"file_id":"V","qualities":` + qualities(60, 400) + `,"width":7}`, "media_items.video.qualities", 1},
		{"too many elements", `"video":{"file_id":"V","qualities":` + qualities(101, 1) + `,"width":7}`, "media_items.video.qualities", 1},
		{"too many cover sizes", `"video":{"file_id":"V","cover":` + sizes(101) + `,"width":7}`, "media_items.video.cover", 1},
		{"too many live photo sizes", `"live_photo":{"file_id":"V","photo":` + sizes(101) + `,"width":7}`, "media_items.live_photo.photo", 1},
		{"too many photo sizes", `"photo":` + sizes(101), "media_items.photo", 0},
		{"photo sizes and others past the list limit", `"photo":` + sizes(100) + `,"voice":{"file_id":"V"}`, "media_items.photo", 1},
		{"photo sizes at the list limit", `"photo":` + sizes(100), "", 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []recorded
			updates := `[{"update_id":1,"message":{"message_id":2,"chat":{"id":-100},` + tc.message + `}}]`
			client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-100")
			got, err := client.ListUpdates(context.Background(), 20, 0)
			if err != nil {
				t.Fatal(err)
			}
			u := got["updates"].([]updateOut)[0]
			if len(u.MediaItems) != tc.items {
				t.Errorf("items = %d, want %d", len(u.MediaItems), tc.items)
			}
			want := []string(nil)
			if tc.omitted != "" {
				want = []string{tc.omitted}
			}
			if !reflect.DeepEqual(u.OmittedFields, want) {
				t.Errorf("omitted_fields = %v, want %v", u.OmittedFields, want)
			}
			if tc.items == 1 && strings.Contains(tc.name, "too many") && tc.omitted != "media_items.photo" {
				if !strings.Contains(string(mustMarshal(u.MediaItems[0])), `"width":7`) {
					t.Errorf("the entry lost its other members: %s", mustMarshal(u.MediaItems[0]))
				}
			}
		})
	}
}

func mustMarshal(v any) []byte {
	out, _ := json.Marshal(v)
	return out
}

func TestWithinLimits(t *testing.T) {
	nest := func(depth int) any {
		var v any = "leaf"
		for i := 0; i < depth; i++ {
			v = map[string]any{"a": v}
		}
		return v
	}
	list := func(n int) []int { return make([]int, n) }
	for name, tt := range map[string]struct {
		v    any
		want bool
	}{
		"eight levels":     {nest(8), true},
		"nine levels":      {nest(9), false},
		"100 elements":     {list(100), true},
		"101 elements":     {list(101), false},
		"nested 101":       {map[string]any{"a": []any{list(101)}}, false},
		"16 KiB":           {strings.Repeat("x", maxNestedBytes-2), true},
		"over 16 KiB":      {strings.Repeat("x", maxNestedBytes), false},
		"missing":          {(*photoSize)(nil), true},
		"unmarshalable":    {make(chan int), false},
		"many small lists": {[]any{list(100), list(100)}, true},
	} {
		if got := withinLimits(tt.v); got != tt.want {
			t.Errorf("%s: withinLimits = %v, want %v", name, got, tt.want)
		}
	}
}

func TestBlockedFieldsAndReferenceHelpers(t *testing.T) {
	for _, name := range []string{"invite_link", "passport_data", "file_path"} {
		if !isBlockedField(name) {
			t.Errorf("%s is not blocked", name)
		}
	}
	if isBlockedField("file_ref") {
		t.Error("file_ref is blocked")
	}
	client, _ := multiClient(t, testToken, nil, "-100")
	refs := client.fileRefs("-100")
	small, big := refs.pair("SMALL", "")
	if small != signRef(testToken, refFile, "-100", "SMALL") || small == "" || big != "" {
		t.Errorf("pair = %q, %q", small, big)
	}
	if s, b := fileRefFunc(nil).pair("SMALL", "BIG"); s != "" || b != "" {
		t.Errorf("a nil function signed %q, %q", s, b)
	}
}

func TestMediaTextsAreCleanedAndBounded(t *testing.T) {
	long := strings.Repeat("y", maxNameRunes+40)
	var d document
	raw := `{"file_id":"D","file_unique_id":"U\u0000\u001b[31m","file_name":"a\u0007b\n` + long + `","mime_type":"` + long + `","file_ref":"INJECTED","kind":"INJECTED"}`
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	var omitted []string
	out := fileRefFunc(nil).document(d, &omitted)
	if out.FileName != "ab"+strings.Repeat("y", maxNameRunes-2) || len([]rune(out.MimeType)) != maxNameRunes ||
		out.FileUniqueID != "U[31m" || out.FileRef != "" || out.Kind != "document" {
		t.Errorf("document = %+v", out)
	}
}

func TestMediaSummaryKeepsItsFormerValues(t *testing.T) {
	for kind, tt := range map[string]struct {
		body string
		want string
	}{
		"document":   {`"document":{"file_id":"F","file_size":5,"mime_type":"a/b","file_name":"n"}`, `{"kind":"document","size":5,"mime_type":"a/b","file_name":"n"}`},
		"audio":      {`"audio":{"file_id":"F","file_size":5,"mime_type":"a/b","file_name":"n","title":"T"}`, `{"kind":"audio","size":5,"mime_type":"a/b","file_name":"n"}`},
		"video":      {`"video":{"file_id":"F","file_size":5,"mime_type":"a/b","file_name":"n"}`, `{"kind":"video","size":5,"mime_type":"a/b","file_name":"n"}`},
		"voice":      {`"voice":{"file_id":"F","file_size":5,"mime_type":"a/b"}`, `{"kind":"voice","size":5,"mime_type":"a/b"}`},
		"video_note": {`"video_note":{"file_id":"F","file_size":5,"length":3}`, `{"kind":"video_note","size":5}`},
		"animation":  {`"animation":{"file_id":"F","file_size":5,"mime_type":"a/b","file_name":"n"}`, `{"kind":"animation","size":5,"mime_type":"a/b","file_name":"n"}`},
		"sticker":    {`"sticker":{"file_id":"F","file_size":5,"emoji":"x"}`, `{"kind":"sticker","size":5}`},
	} {
		t.Run(kind, func(t *testing.T) {
			var m tgMessage
			if err := json.Unmarshal([]byte(`{`+tt.body+`}`), &m); err != nil {
				t.Fatal(err)
			}
			k, file := m.media()
			out := mediaOut{Kind: k, Size: file.FileSize, MimeType: file.MimeType, FileName: file.FileName}
			if got := string(mustMarshal(out)); got != tt.want || file.FileID != "F" {
				t.Errorf("media = %s (id %q), want %s", got, file.FileID, tt.want)
			}
		})
	}
	var m tgMessage
	_ = json.Unmarshal([]byte(`{"live_photo":{"file_id":"F"}}`), &m)
	if k, file := m.media(); k != "" || file != nil {
		t.Error("a live photo must not change the media summary")
	}
}

func TestPersonalChatMessagesListMediaItems(t *testing.T) {
	transport := func(r *http.Request) (*http.Response, error) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "getChatMember" {
			return response(200, `{"ok":true,"result":{"status":"member","user":{"id":7}}}`), nil
		}
		return response(200, `{"ok":true,"result":[{"message_id":1,"date":2,"text":"hi","chat":{"id":55},`+documentedMedia+`},`+
			`{"message_id":2,"date":3,"business_connection_id":"b1","photo":[{"file_id":"BUSINESSPHOTO"}]}]}`), nil
	}
	client, _ := telegramClient(t, "-1001", roundTripFunc(transport))
	resolved := resolvedWith("-1001")
	got, err := client.PersonalChatMessages(context.Background(), 7, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(mustMarshal(got))
	assertNoIdentifiers(t, encoded)
	if strings.Contains(encoded, "BUSINESSPHOTO") {
		t.Errorf("output contains a business identifier: %s", encoded)
	}
	messages := jsonRoundTrip(t, got).(map[string]any)["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["media_kind"] != "photo" || first["file_ref"] != signRef(testToken, refFile, "-1001", "PHOTOBIG") {
		t.Errorf("summary = %v", first)
	}
	items := restoreFileIDs(t, client, resolved, "-1001", first["media_items"])
	if want := jsonRoundTrip(t, expectedMediaItems(t)); !reflect.DeepEqual(items, want) {
		t.Errorf("media_items = %v\nwant %v", items, want)
	}
	second := messages[1].(map[string]any)
	if _, ok := second["file_ref"]; ok || strings.Contains(string(mustMarshal(second["media_items"])), "file_ref") || second["media_kind"] != "photo" {
		t.Errorf("business message = %v", second)
	}
}

func TestOutputWithoutMediaHasNoNewMembers(t *testing.T) {
	var calls []recorded
	updates := `[{"update_id":1,"message":{"message_id":2,"date":3,"chat":{"id":-100},"text":"hi"}}]`
	client, _ := multiClient(t, testToken, fakeTelegram(&calls, updates, "0"), "-100")
	got, err := client.ListUpdates(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"update_id":1,"type":"message","chat":"-100","message_id":2,"date":3,"text":"hi"}`; string(mustMarshal(got["updates"].([]updateOut)[0])) != want {
		t.Errorf("update = %s, want %s", mustMarshal(got["updates"].([]updateOut)[0]), want)
	}
}
