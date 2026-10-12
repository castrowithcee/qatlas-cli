package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const storyOK = `{"ok":true,"result":{"chat":{"id":777,"type":"private","first_name":"PRIVATE"},"id":42}}`

func newStoryEnv(t *testing.T, targets ...string) *mediaEnv {
	t.Helper()
	if len(targets) == 0 {
		targets = []string{"business/b1", "business/b2"}
	}
	e := newMediaEnv(t, targets...)
	e.answer = func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getBusinessConnection"):
			return response(200, `{"ok":true,"result":{"id":"b2","user_chat_id":555}}`), nil
		case strings.HasSuffix(r.URL.Path, "/deleteStory"):
			return response(200, `{"ok":true,"result":true}`), nil
		}
		return response(200, storyOK), nil
	}
	return e
}

func (e *mediaEnv) story(tool string, args map[string]any) (any, error) {
	raw, _ := json.Marshal(args)
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	ctx := capability.WithConfirmed(context.Background())
	switch tool {
	case "post":
		return invokeStoriesPostWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "edit":
		return invokeStoriesEditWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	case "repost":
		return invokeStoriesRepostWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
	}
	return invokeStoriesDeleteWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

func TestStoriesPostSendsExactMultipart(t *testing.T) {
	e := newStoryEnv(t)
	photo := e.write(t, "private-name.JPG", 12)
	out, err := e.story("post", map[string]any{"business": "b1", "type": "photo", "local_path": photo,
		"active_period": 86400, "caption": "hi", "parse_mode": "HTML", "post_to_chat_page": true, "protect_content": true})
	if err != nil || len(out.(map[string]any)) != 1 || out.(map[string]any)["story_id"] != int64(42) {
		t.Fatalf("out = %v, %v", out, err)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/postStory" {
		t.Fatalf("requests = %+v", e.requests)
	}
	fields, files := parts(t, e.requests[0])
	want := map[string]string{"business_connection_id": "b1", "content": `{"type":"photo","photo":"attach://story"}`,
		"active_period": "86400", "caption": "hi", "parse_mode": "HTML", "post_to_chat_page": "true", "protect_content": "true"}
	if len(fields) != len(want) || len(files) != 1 || files["story"] != [2]string{"file.JPG", strings.Repeat("x", 12)} {
		t.Fatalf("fields = %v files = %v", fields, files)
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("field %s = %q, want %q", k, fields[k], v)
		}
	}
	if body := string(e.requests[0].body); strings.Contains(body, e.dir) || strings.Contains(body, "private-name") {
		t.Error("path or file name reached the request")
	}
}

func TestStoriesPostVideoOptionsAndEditBody(t *testing.T) {
	e := newStoryEnv(t)
	video := e.write(t, "clip.mp4", 8)
	if _, err := e.story("post", map[string]any{"business": "b1", "type": "video", "local_path": video,
		"active_period": 21600, "duration": 12.5, "cover_frame_timestamp": 1, "is_animation": true}); err != nil {
		t.Fatal(err)
	}
	fields, files := parts(t, e.requests[0])
	if fields["content"] != `{"type":"video","video":"attach://story","duration":12.5,"cover_frame_timestamp":1,"is_animation":true}` ||
		files["story"][0] != "file.mp4" || len(fields) != 3 {
		t.Errorf("fields = %v files = %v", fields, files)
	}
	e.requests = nil
	out, err := e.story("edit", map[string]any{"business": "b1", "story_id": 42, "type": "video", "local_path": video, "caption": "c"})
	if err != nil || out.(map[string]any)["story_id"] != int64(42) {
		t.Fatalf("out = %v, %v", out, err)
	}
	fields, _ = parts(t, e.requests[0])
	if e.requests[0].path != "POST /bot"+testToken+"/editStory" || len(fields) != 4 || fields["story_id"] != "42" ||
		fields["content"] != `{"type":"video","video":"attach://story"}` || fields["caption"] != "c" {
		t.Errorf("path = %s fields = %v", e.requests[0].path, fields)
	}
}

func TestStoriesEditRefusesAnAnswerForAnotherStory(t *testing.T) {
	e := newStoryEnv(t)
	photo := e.write(t, "a.jpg", 4)
	_, err := e.story("edit", map[string]any{"business": "b1", "story_id": 7, "type": "photo", "local_path": photo})
	if err == nil || !strings.Contains(err.Error(), "may have taken effect") || len(e.requests) != 1 {
		t.Errorf("err = %v requests = %d", err, len(e.requests))
	}
}

func TestStoriesRepostReadsTheSourceChatOnce(t *testing.T) {
	for _, from := range []string{"b2", "b1"} {
		e := newStoryEnv(t)
		e.answer = func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/getBusinessConnection") {
				return response(200, `{"ok":true,"result":{"id":"`+from+`","user_chat_id":555}}`), nil
			}
			return response(200, storyOK), nil
		}
		out, err := e.story("repost", map[string]any{"business": "b1", "from_business": from, "from_story_id": 9,
			"active_period": 43200, "protect_content": true})
		if err != nil || len(out.(map[string]any)) != 1 || out.(map[string]any)["story_id"] != int64(42) {
			t.Fatalf("out = %v, %v", out, err)
		}
		if len(e.requests) != 2 || !strings.HasSuffix(e.requests[0].path, "/getBusinessConnection") ||
			string(e.requests[0].body) != `{"business_connection_id":"`+from+`"}` ||
			!strings.HasSuffix(e.requests[1].path, "/repostStory") ||
			string(e.requests[1].body) != `{"business_connection_id":"b1","from_chat_id":555,"from_story_id":9,"active_period":43200,"protect_content":true}` {
			t.Errorf("requests = %+v", e.requests)
		}
	}
}

func TestStoriesRepostRefusesAForeignAnswerOrMissingChat(t *testing.T) {
	for name, body := range map[string]string{
		"foreign id": `{"ok":true,"result":{"id":"zzz","user_chat_id":555}}`,
		"zero chat":  `{"ok":true,"result":{"id":"b2","user_chat_id":0}}`,
		"no chat":    `{"ok":true,"result":{"id":"b2"}}`,
	} {
		e := newStoryEnv(t)
		e.answer = func(*http.Request) (*http.Response, error) { return response(200, body), nil }
		_, err := e.story("repost", map[string]any{"business": "b1", "from_business": "b2", "from_story_id": 9, "active_period": 86400})
		if err == nil || strings.Contains(err.Error(), "zzz") || len(e.requests) != 1 {
			t.Errorf("%s err = %v requests = %d", name, err, len(e.requests))
		}
		for _, r := range e.requests {
			if strings.HasSuffix(r.path, "/repostStory") {
				t.Errorf("%s sent a repost", name)
			}
		}
	}
}

func TestStoriesDeleteSendsTheExactBody(t *testing.T) {
	e := newStoryEnv(t)
	out, err := e.story("delete", map[string]any{"business": "b1", "story_id": 5})
	if err != nil || len(out.(map[string]any)) != 1 || out.(map[string]any)["deleted"] != true {
		t.Fatalf("out = %v, %v", out, err)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/deleteStory" ||
		string(e.requests[0].body) != `{"business_connection_id":"b1","story_id":5}` {
		t.Errorf("requests = %+v", e.requests)
	}
}

func TestStoriesRefuseBeforeSecretAndRead(t *testing.T) {
	e := newStoryEnv(t, "business/b1", "-100")
	photo := e.write(t, "ok.jpg", 4)
	big := e.write(t, "big.jpg", maxPhotoBytes+1)
	bigVideo := e.write(t, "big.mp4", maxStoryVideoBytes+1)
	empty := e.write(t, "empty.jpg", 0)
	outside := t.TempDir() + "/a.jpg"
	base := func(extra map[string]any) map[string]any {
		m := map[string]any{"business": "b1", "type": "photo", "local_path": photo, "active_period": 86400}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for name, c := range map[string]struct {
		tool string
		args map[string]any
	}{
		"post no target":        {"post", base(map[string]any{"business": "b9"})},
		"post invalid id":       {"post", base(map[string]any{"business": "a b"})},
		"post outside":          {"post", base(map[string]any{"local_path": outside})},
		"post too large photo":  {"post", base(map[string]any{"local_path": big})},
		"post too large video":  {"post", base(map[string]any{"type": "video", "local_path": bigVideo})},
		"post empty":            {"post", base(map[string]any{"local_path": empty})},
		"post period":           {"post", base(map[string]any{"active_period": 3600})},
		"post caption":          {"post", base(map[string]any{"caption": strings.Repeat("a", 2049)})},
		"post parse mode":       {"post", base(map[string]any{"parse_mode": "Markdown"})},
		"post areas":            {"post", base(map[string]any{"areas": []any{}})},
		"post entities":         {"post", base(map[string]any{"caption_entities": []any{}})},
		"post file ref":         {"post", base(map[string]any{"file_ref": "x"})},
		"post url":              {"post", base(map[string]any{"local_path": "https://example.test/a.jpg"})},
		"post photo duration":   {"post", base(map[string]any{"duration": 3})},
		"post photo animation":  {"post", base(map[string]any{"is_animation": false})},
		"post bad duration":     {"post", base(map[string]any{"type": "video", "duration": 61})},
		"post bad type":         {"post", base(map[string]any{"type": "gif"})},
		"post no path":          {"post", map[string]any{"business": "b1", "type": "photo", "active_period": 86400}},
		"edit no target":        {"edit", map[string]any{"business": "b9", "story_id": 1, "type": "photo", "local_path": photo}},
		"edit bad story":        {"edit", map[string]any{"business": "b1", "story_id": 0, "type": "photo", "local_path": photo}},
		"edit outside":          {"edit", map[string]any{"business": "b1", "story_id": 1, "type": "photo", "local_path": outside}},
		"edit period":           {"edit", map[string]any{"business": "b1", "story_id": 1, "type": "photo", "local_path": photo, "active_period": 86400}},
		"edit caption":          {"edit", map[string]any{"business": "b1", "story_id": 1, "type": "photo", "local_path": photo, "caption": strings.Repeat("ä", 2049)}},
		"repost foreign":        {"repost", map[string]any{"business": "b1", "from_business": "b9", "from_story_id": 1, "active_period": 86400}},
		"repost target foreign": {"repost", map[string]any{"business": "b9", "from_business": "b1", "from_story_id": 1, "active_period": 86400}},
		"repost period":         {"repost", map[string]any{"business": "b1", "from_business": "b1", "from_story_id": 1, "active_period": 1}},
		"repost chat id":        {"repost", map[string]any{"business": "b1", "from_business": "b1", "from_story_id": 1, "active_period": 86400, "from_chat_id": 5}},
		"delete no target":      {"delete", map[string]any{"business": "b9", "story_id": 1}},
		"delete bad story":      {"delete", map[string]any{"business": "b1", "story_id": -1}},
		"delete extra":          {"delete", map[string]any{"business": "b1", "story_id": 1, "x": 1}},
	} {
		if _, err := e.story(c.tool, c.args); err == nil || strings.Contains(err.Error(), e.dir) || strings.Contains(err.Error(), "b9") {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("lookups = %d requests = %d, want 0", e.lookups, len(e.requests))
	}
	// A read release is required for an upload.
	e.resolved.Files = config.Files{}
	if _, err := e.story("post", base(nil)); err == nil || e.lookups != 0 {
		t.Errorf("post without read release err = %v lookups = %d", err, e.lookups)
	}
}

func TestStoryMutationsSendOneRequestAndReportUncertainty(t *testing.T) {
	for name, tool := range map[string]string{"post": "post", "edit": "edit", "repost": "repost", "delete": "delete"} {
		for _, status := range []int{500, 502} {
			e := newStoryEnv(t)
			e.answer = func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/getBusinessConnection") {
					return response(200, `{"ok":true,"result":{"id":"b2","user_chat_id":555}}`), nil
				}
				return response(status, `{"ok":false,"description":"SECRETDESC"}`), nil
			}
			err := runStory(t, e, tool)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "SECRETDESC") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			mutations := 0
			for _, r := range e.requests {
				if !strings.HasSuffix(r.path, "/getBusinessConnection") {
					mutations++
				}
			}
			if mutations != 1 {
				t.Errorf("%s status %d mutating requests = %d, want 1", name, status, mutations)
			}
		}
		e := newStoryEnv(t)
		calls := 0
		e.answer = func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/getBusinessConnection") {
				return response(200, `{"ok":true,"result":{"id":"b2","user_chat_id":555}}`), nil
			}
			calls++
			return nil, &timeoutError{}
		}
		if err := runStory(t, e, tool); err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
			t.Errorf("%s timeout err = %v calls = %d", name, err, calls)
		}
	}
}

func runStory(t *testing.T, e *mediaEnv, tool string) error {
	t.Helper()
	photo := e.write(t, "a.jpg", 4)
	args := map[string]map[string]any{
		"post":   {"business": "b1", "type": "photo", "local_path": photo, "active_period": 86400},
		"edit":   {"business": "b1", "story_id": 1, "type": "photo", "local_path": photo},
		"repost": {"business": "b1", "from_business": "b2", "from_story_id": 1, "active_period": 86400},
		"delete": {"business": "b1", "story_id": 1},
	}
	_, err := e.story(tool, args[tool])
	return err
}

func TestStoryDescriptors(t *testing.T) {
	for _, d := range []capability.Descriptor{storiesPost, storiesEdit, storiesRepost, storiesDelete} {
		if d.Group != groupStories || d.Risk.DataSensitivity != businessDataSensitivity ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.RequiresToolAllowList != (d.ID == storiesDelete.ID) ||
			(d.LocalFiles == config.LocalFilesRead) != (d.ID == storiesPost.ID || d.ID == storiesEdit.ID) {
			t.Errorf("%s risk, group, local files, or allow list wrong", d.ID)
		}
	}
	if storiesPost.Risk.Idempotency != capability.IdempotencyNonIdempotent || storiesRepost.Risk.Effect != capability.EffectCreate ||
		storiesEdit.Risk.Idempotency != capability.IdempotencyUnknown || storiesEdit.Risk.Effect != capability.EffectUpdate ||
		storiesDelete.Risk.Idempotency != capability.IdempotencyIdempotent || storiesDelete.Risk.Effect != capability.EffectDelete {
		t.Error("story risk wrong")
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if strings.HasPrefix(tool, "telegram.stories.") {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}
