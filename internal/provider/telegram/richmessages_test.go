package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func (e *mediaEnv) runRich(arguments any) (any, error) {
	raw, _ := json.Marshal(arguments)
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	return invokeRichMessagesSendWith(capability.WithConfirmed(context.Background()), e.resolved, e.secrets, e.red,
		raw, httpClient)
}

func sameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("body %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
}

func TestRichMessageBlocksBuildExactBodies(t *testing.T) {
	text := `[{"type":"url","text":"docs","url":"https://example.org/a"},"x"]`
	cases := map[string][2]string{
		"paragraph": {`{"type":"paragraph","text":"Hi"}`, `{"type":"paragraph","text":"Hi"}`},
		"styled text": {`{"type":"paragraph","text":["a",{"type":"bold","text":{"type":"italic","text":"b"}},` +
			`{"type":"mathematical_expression","expression":"x^2"},` + text + `]}`,
			`{"type":"paragraph","text":["a",{"type":"bold","text":{"type":"italic","text":"b"}},` +
				`{"type":"mathematical_expression","expression":"x^2"},` + text + `]}`},
		"heading": {`{"type":"heading","size":3,"text":"T"}`, `{"type":"heading","size":3,"text":"T"}`},
		"pre":     {`{"type":"pre","text":"go","language":"go"}`, `{"type":"pre","text":"go","language":"go"}`},
		"footer":  {`{"type":"footer","text":"f"}`, `{"type":"footer","text":"f"}`},
		"divider": {`{"type":"divider"}`, `{"type":"divider"}`},
		"math":    {`{"type":"mathematical_expression","expression":"a+b"}`, `{"type":"mathematical_expression","expression":"a+b"}`},
		"anchor":  {`{"type":"anchor","name":"top"}`, `{"type":"anchor","name":"top"}`},
		"list": {`{"type":"list","items":[{"blocks":[{"type":"paragraph","text":"a"}],"has_checkbox":true,` +
			`"is_checked":true,"value":3,"type":"I"},{"blocks":[{"type":"divider"}],"has_checkbox":false}]}`,
			`{"type":"list","items":[{"blocks":[{"type":"paragraph","text":"a"}],"has_checkbox":true,` +
				`"is_checked":true,"value":3,"type":"I"},{"blocks":[{"type":"divider"}]}]}`},
		"blockquote": {`{"type":"blockquote","blocks":[{"type":"paragraph","text":"q"}],"credit":"me"}`,
			`{"type":"blockquote","blocks":[{"type":"paragraph","text":"q"}],"credit":"me"}`},
		"expandable": {`{"type":"expandable_blockquote","text":"q","credit":"me"}`,
			`{"type":"expandable_blockquote","text":"q","credit":"me"}`},
		"pullquote": {`{"type":"pullquote","text":"q"}`, `{"type":"pullquote","text":"q"}`},
		"table": {`{"type":"table","cells":[[{"text":"h","is_header":true,"colspan":2,"align":"left","valign":"top"}],` +
			`[{"align":"right","valign":"bottom","rowspan":3}]],"is_bordered":true,"is_striped":false,"caption":"c"}`,
			`{"type":"table","cells":[[{"text":"h","is_header":true,"colspan":2,"align":"left","valign":"top"}],` +
				`[{"align":"right","valign":"bottom","rowspan":3}]],"is_bordered":true,"caption":"c"}`},
		"details": {`{"type":"details","summary":"s","blocks":[{"type":"paragraph","text":"b"}],"is_open":true}`,
			`{"type":"details","summary":"s","blocks":[{"type":"paragraph","text":"b"}],"is_open":true}`},
		"map": {`{"type":"map","location":{"latitude":52.5,"longitude":13.4},"zoom":12,"width":600,"height":400,` +
			`"caption":{"text":"c","credit":"o"}}`,
			`{"type":"map","location":{"latitude":52.5,"longitude":13.4},"zoom":12,"width":600,"height":400,` +
				`"caption":{"text":"c","credit":"o"}}`},
		"buttons": {`{"type":"buttons","align":"center","buttons":[{"text":"Open","url":"https://example.org","style":"primary"},` +
			`{"text":"Ok","callback_data":"ok","style":"link"},{"text":"App","url":"tg://resolve?domain=x"}]}`,
			`{"type":"buttons","align":"center","buttons":[{"text":"Open","url":"https://example.org","style":"primary"},` +
				`{"text":"Ok","callback_data":"ok","style":"link"},{"text":"App","url":"tg://resolve?domain=x"}]}`},
	}
	for name, c := range cases {
		e := newMediaEnv(t)
		if _, err := e.runRich(map[string]any{"blocks": []json.RawMessage{json.RawMessage(c[0])}}); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/sendRichMessage" ||
			e.requests[0].contentType != "application/json" {
			t.Errorf("%s requests = %+v", name, e.requests)
			continue
		}
		sameJSON(t, e.requests[0].body, `{"chat_id":"-1001","rich_message":{"blocks":[`+c[1]+`]}}`)
	}
}

func TestRichMessageCollagesAndSlideshowsHoldMediaBlocks(t *testing.T) {
	for _, kind := range []string{"collage", "slideshow"} {
		e := newMediaEnv(t)
		a, b := e.write(t, "a.jpg", 3), e.write(t, "b.mp4", 4)
		_, err := e.runRich(map[string]any{"blocks": []any{map[string]any{"type": kind, "caption": map[string]any{"text": "c"},
			"blocks": []any{map[string]any{"type": "photo", "local_path": a}, map[string]any{"type": "video", "file_ref": e.ref(mediaChat, "VID")}, map[string]any{"type": "animation", "local_path": b}}}}})
		if err != nil || len(e.requests) != 1 {
			t.Fatalf("%s: %v, %d requests", kind, err, len(e.requests))
		}
		fields, files := parts(t, e.requests[0])
		sameJSON(t, []byte(fields["rich_message"]), `{"blocks":[{"type":"`+kind+`","caption":{"text":"c"},"blocks":[`+
			`{"type":"photo","photo":{"type":"photo","media":"attach://file0"}},`+
			`{"type":"video","video":{"type":"video","media":"VID"}},`+
			`{"type":"animation","animation":{"type":"animation","media":"attach://file2"}}]}]}`)
		if len(files) != 2 || files["file0"] != [2]string{"file-1.jpg", "xxx"} || files["file2"] != [2]string{"file-3.mp4", "xxxx"} {
			t.Errorf("%s files = %v", kind, files)
		}
	}
	e := newMediaEnv(t)
	if _, err := e.runRich(map[string]any{"blocks": []any{map[string]any{"type": "collage",
		"blocks": []any{map[string]any{"type": "paragraph", "text": "x"}}}}}); err == nil {
		t.Error("a paragraph in a collage was accepted")
	}
}

func TestRichMessageMediaBlocksUploadWithAttachReferences(t *testing.T) {
	e := newMediaEnv(t)
	names := []string{"p.JPG", "v.mp4", "a.gif", "m.mp3", "d.pdf", "n.ogg"}
	types := []string{"photo", "video", "animation", "audio", "document", "voice_note"}
	blocks := []any{}
	for i, kind := range types {
		blocks = append(blocks, map[string]any{"type": kind, "local_path": e.write(t, names[i], int64(i+1)),
			"caption": map[string]any{"text": kind, "credit": "c"}})
	}
	out, err := e.runRich(map[string]any{"blocks": blocks, "is_rtl": true, "skip_entity_detection": true,
		"reply_to_message_id": 5, "message_thread_id": 9, "disable_notification": true, "protect_content": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/sendRichMessage" {
		t.Fatalf("requests = %+v", e.requests)
	}
	fields, files := parts(t, e.requests[0])
	var want []string
	wantFiles := map[string][2]string{}
	for i, kind := range types {
		part := "file" + string(rune('0'+i))
		want = append(want, `{"type":"`+kind+`","`+kind+`":{"type":"`+kind+`","media":"attach://`+part+`"},`+
			`"caption":{"text":"`+kind+`","credit":"c"}}`)
		wantFiles[part] = [2]string{"file-" + string(rune('1'+i)) + filepath.Ext(names[i]), strings.Repeat("x", i+1)}
	}
	sameJSON(t, []byte(fields["rich_message"]), `{"blocks":[`+strings.Join(want, ",")+`],"is_rtl":true,"skip_entity_detection":true}`)
	for name, value := range map[string]string{"chat_id": mediaChat, "reply_parameters": `{"message_id":5}`,
		"message_thread_id": "9", "disable_notification": "true", "protect_content": "true"} {
		if fields[name] != value {
			t.Errorf("field %s = %q, want %q", name, fields[name], value)
		}
	}
	if len(fields) != 6 || !reflect.DeepEqual(files, wantFiles) {
		t.Errorf("fields = %v, files = %v", fields, files)
	}
	if result := out.(map[string]any); len(result) != 2 || result["message_id"] != int64(7) || result["date"] != int64(1700000000) {
		t.Errorf("result = %v", result)
	}
	for _, leaked := range []string{e.dir, "p.JPG"} {
		if strings.Contains(string(e.requests[0].body), leaked) {
			t.Errorf("request body contains %q", leaked)
		}
	}
}

func TestRichMessageFileRefsUseAJSONBodyWithTheVerifiedFileID(t *testing.T) {
	e := newMediaEnv(t)
	_, err := e.runRich(map[string]any{"blocks": []any{
		map[string]any{"type": "document", "file_ref": e.ref(mediaChat, "DOC-1")},
		map[string]any{"type": "photo", "file_ref": e.ref(mediaChat, "PH-1")}},
		"reply_to_message_id": 5})
	if err != nil || len(e.requests) != 1 || e.requests[0].contentType != "application/json" {
		t.Fatalf("err = %v, requests = %+v", err, e.requests)
	}
	sameJSON(t, e.requests[0].body, `{"chat_id":"-1001","reply_parameters":{"message_id":5},"rich_message":{"blocks":[`+
		`{"type":"document","document":{"type":"document","media":"DOC-1"}},`+
		`{"type":"photo","photo":{"type":"photo","media":"PH-1"}}]}}`)
}

func TestRichMessageRefusesInvalidInputBeforeSecretsAndIO(t *testing.T) {
	outside := t.TempDir()
	e := newMediaEnv(t, mediaChat, "-1002")
	local := e.write(t, "a.jpg", 4)
	para := map[string]any{"type": "paragraph", "text": "x"}
	blocks := func(b ...any) map[string]any { return map[string]any{"chat": mediaChat, "blocks": b} }
	nest := func(depth int) map[string]any {
		block := para
		for i := 0; i < depth; i++ {
			block = map[string]any{"type": "blockquote", "blocks": []any{block}}
		}
		return block
	}
	many := make([]any, 101)
	for i := range many {
		many[i] = para
	}
	files := make([]any, 11)
	for i := range files {
		files[i] = map[string]any{"type": "photo", "local_path": local}
	}
	deepText := any("x")
	for i := 0; i < 10; i++ {
		deepText = map[string]any{"type": "bold", "text": deepText}
	}
	cases := map[string]any{
		"unknown block type":      blocks(map[string]any{"type": "marquee", "text": "x"}),
		"thinking block":          blocks(map[string]any{"type": "thinking", "text": "x"}),
		"missing type":            blocks(map[string]any{"text": "x"}),
		"unknown field":           blocks(map[string]any{"type": "paragraph", "text": "x", "color": "red"}),
		"unknown nested field":    blocks(map[string]any{"type": "list", "items": []any{map[string]any{"blocks": []any{para}, "x": 1}}}),
		"missing field":           blocks(map[string]any{"type": "paragraph"}),
		"null field":              blocks(map[string]any{"type": "paragraph", "text": nil}),
		"unknown text type":       blocks(map[string]any{"type": "paragraph", "text": map[string]any{"type": "mention", "text": "x"}}),
		"text custom emoji":       blocks(map[string]any{"type": "paragraph", "text": map[string]any{"type": "custom_emoji", "id": "1"}}),
		"text button":             blocks(map[string]any{"type": "paragraph", "text": map[string]any{"type": "button", "text": "x"}}),
		"unknown text field":      blocks(map[string]any{"type": "paragraph", "text": map[string]any{"type": "bold", "text": "x", "x": 1}}),
		"text link scheme":        blocks(map[string]any{"type": "paragraph", "text": map[string]any{"type": "url", "text": "x", "url": "http://example.org"}}),
		"text link javascript":    blocks(map[string]any{"type": "paragraph", "text": map[string]any{"type": "url", "text": "x", "url": "javascript:alert(1)"}}),
		"empty text":              blocks(map[string]any{"type": "paragraph", "text": ""}),
		"long text":               blocks(map[string]any{"type": "paragraph", "text": strings.Repeat("x", 4097)}),
		"deep text":               blocks(map[string]any{"type": "paragraph", "text": deepText}),
		"heading size":            blocks(map[string]any{"type": "heading", "size": 7, "text": "x"}),
		"url media":               blocks(map[string]any{"type": "photo", "url": "https://example.org/a.jpg"}),
		"url as media field":      blocks(map[string]any{"type": "photo", "photo": map[string]any{"type": "photo", "media": "https://example.org/a.jpg"}}),
		"url as local path":       blocks(map[string]any{"type": "photo", "local_path": "https://example.org/a.jpg"}),
		"raw file id":             blocks(map[string]any{"type": "photo", "file_id": "AgADBAADrawFileId"}),
		"raw file id as ref":      blocks(map[string]any{"type": "photo", "file_ref": "AgADBAADrawFileId"}),
		"media property":          blocks(map[string]any{"type": "video", "local_path": local, "has_spoiler": true}),
		"both sources":            blocks(map[string]any{"type": "photo", "local_path": local, "file_ref": e.ref(mediaChat, "X")}),
		"no source":               blocks(map[string]any{"type": "document"}),
		"outside release":         blocks(map[string]any{"type": "document", "local_path": filepath.Join(outside, "b.pdf")}),
		"other chat's ref":        blocks(map[string]any{"type": "photo", "file_ref": e.ref("-1002", "X")}),
		"unbound ref":             blocks(map[string]any{"type": "photo", "file_ref": e.ref("-777", "X")}),
		"callback ref":            blocks(map[string]any{"type": "photo", "file_ref": signRef(testToken, refCallback, mediaChat, "X")}),
		"too many files":          blocks(files...),
		"web app button":          blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "web_app": map[string]any{"url": "https://example.org"}}}}),
		"login button":            blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "login_url": map[string]any{"url": "https://example.org"}}}}),
		"pay button":              blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "pay": true}}}),
		"switch inline button":    blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "switch_inline_query": "q"}}}),
		"copy text button":        blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "copy_text": map[string]any{"text": "q"}}}}),
		"http button":             blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "url": "http://example.org"}}}),
		"credential button":       blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "url": "https://u:p@example.org"}}}),
		"two targets button":      blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "url": "https://example.org", "callback_data": "x"}}}),
		"long callback button":    blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "callback_data": strings.Repeat("x", 65)}}}),
		"link style on url":       blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "url": "https://example.org", "style": "link"}}}),
		"unknown button style":    blocks(map[string]any{"type": "buttons", "buttons": []any{map[string]any{"text": "a", "callback_data": "x", "style": "red"}}}),
		"nine buttons":            blocks(map[string]any{"type": "buttons", "buttons": []any{1, 2, 3, 4, 5, 6, 7, 8, 9}}),
		"no buttons":              blocks(map[string]any{"type": "buttons", "buttons": []any{}}),
		"map out of range":        blocks(map[string]any{"type": "map", "location": map[string]any{"latitude": 91, "longitude": 0}}),
		"map zoom":                blocks(map[string]any{"type": "map", "location": map[string]any{"latitude": 1, "longitude": 1}, "zoom": 25}),
		"map size":                blocks(map[string]any{"type": "map", "location": map[string]any{"latitude": 1, "longitude": 1}, "width": 6000, "height": 6000}),
		"map ratio":               blocks(map[string]any{"type": "map", "location": map[string]any{"latitude": 1, "longitude": 1}, "width": 2100, "height": 100}),
		"map location field":      blocks(map[string]any{"type": "map", "location": map[string]any{"latitude": 1, "longitude": 1, "live_period": 60}}),
		"table without align":     blocks(map[string]any{"type": "table", "cells": []any{[]any{map[string]any{"text": "x"}}}}),
		"table colspan":           blocks(map[string]any{"type": "table", "cells": []any{[]any{map[string]any{"align": "left", "valign": "top", "colspan": 1}}}}),
		"table alignment":         blocks(map[string]any{"type": "table", "cells": []any{[]any{map[string]any{"align": "justify", "valign": "top"}}}}),
		"checked without box":     blocks(map[string]any{"type": "list", "items": []any{map[string]any{"blocks": []any{para}, "is_checked": true}}}),
		"list label type":         blocks(map[string]any{"type": "list", "items": []any{map[string]any{"blocks": []any{para}, "type": "x"}}}),
		"empty details":           blocks(map[string]any{"type": "details", "summary": "s", "blocks": []any{}}),
		"nested too deep":         blocks(nest(5)),
		"too many blocks":         blocks(many...),
		"no blocks":               blocks(),
		"foreign chat":            map[string]any{"chat": "-999", "blocks": []any{para}},
		"several chats no chat":   map[string]any{"blocks": []any{para}},
		"html":                    map[string]any{"chat": mediaChat, "html": "<p>x</p>", "blocks": []any{para}},
		"markdown":                map[string]any{"chat": mediaChat, "markdown": "x", "blocks": []any{para}},
		"media list":              map[string]any{"chat": mediaChat, "media": []any{}, "blocks": []any{para}},
		"business":                map[string]any{"chat": mediaChat, "business": "business/x", "blocks": []any{para}},
		"business connection id":  map[string]any{"chat": mediaChat, "business_connection_id": "x", "blocks": []any{para}},
		"ephemeral":               map[string]any{"chat": mediaChat, "ephemeral_message_parameters": map[string]any{}, "blocks": []any{para}},
		"paid broadcast":          map[string]any{"chat": mediaChat, "allow_paid_broadcast": true, "blocks": []any{para}},
		"message effect":          map[string]any{"chat": mediaChat, "message_effect_id": "x", "blocks": []any{para}},
		"suggested post":          map[string]any{"chat": mediaChat, "suggested_post_parameters": map[string]any{}, "blocks": []any{para}},
		"reply markup":            map[string]any{"chat": mediaChat, "reply_markup": map[string]any{}, "blocks": []any{para}},
		"method":                  map[string]any{"chat": mediaChat, "method": "sendRichMessageDraft", "blocks": []any{para}},
		"negative reply":          map[string]any{"chat": mediaChat, "reply_to_message_id": -1, "blocks": []any{para}},
		"negative thread":         map[string]any{"chat": mediaChat, "message_thread_id": -1, "blocks": []any{para}},
		"larger than the maximum": blocks(map[string]any{"type": "paragraph", "text": bigText()}),
	}
	for name, arguments := range cases {
		_, err := e.runRich(arguments)
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		for _, leaked := range []string{outside, "-999", "-777", e.dir, "AgADBAAD", "marquee", "mention", "web_app"} {
			if strings.Contains(err.Error(), leaked) {
				t.Errorf("%s: error leaks %q: %v", name, leaked, err)
			}
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want none", e.lookups, len(e.requests))
	}
}

// bigText is one paragraph list that stays within every per-item bound but not within the request bound.
func bigText() []any {
	items := make([]any, 100)
	for i := range items {
		items[i] = strings.Repeat("x", 4000)
	}
	return items
}

func TestRichMessageRejectsOversizedUploadsBeforeSecrets(t *testing.T) {
	e := newMediaEnv(t)
	bigPhoto := e.write(t, "big.jpg", maxPhotoBytes+1)
	bigDoc := e.write(t, "big.bin", maxUploadBytes+1)
	half := e.write(t, "half.bin", maxUploadBytes/2+1)
	for name, blocks := range map[string][]any{
		"photo":    {map[string]any{"type": "photo", "local_path": bigPhoto}},
		"document": {map[string]any{"type": "document", "local_path": bigDoc}},
		"total": {map[string]any{"type": "document", "local_path": half},
			map[string]any{"type": "document", "local_path": half}},
	} {
		if _, err := e.runRich(map[string]any{"blocks": blocks}); err == nil {
			t.Errorf("%s above the limit was accepted", name)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want none", e.lookups, len(e.requests))
	}
	ok := e.write(t, "ok.jpg", maxPhotoBytes)
	if _, err := e.runRich(map[string]any{"blocks": []any{map[string]any{"type": "photo", "local_path": ok}}}); err != nil {
		t.Errorf("10 MB photo: %v", err)
	}
}

func TestRichMessageChoosesTheOnlyBoundChatAndRefusesAForeignRefOfThatChat(t *testing.T) {
	e := newMediaEnv(t)
	if _, err := e.runRich(map[string]any{"blocks": []any{map[string]any{"type": "divider"}}}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ChatID string `json:"chat_id"`
	}
	if json.Unmarshal(e.requests[0].body, &body) != nil || body.ChatID != mediaChat {
		t.Errorf("body = %s", e.requests[0].body)
	}
	// A reference signed by another bot is refused after the token resolved but before the request.
	forged := signRef("999:other_bot-token", refFile, mediaChat, "X")
	if _, err := e.runRich(map[string]any{"blocks": []any{map[string]any{"type": "photo", "file_ref": forged}}}); err == nil {
		t.Error("a reference of another bot was accepted")
	}
	if len(e.requests) != 1 {
		t.Errorf("requests = %d, want 1", len(e.requests))
	}
}

func TestRichMessageNeverRetriesAndReportsUncertainty(t *testing.T) {
	for name, answer := range map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) { return nil, &timeoutError{} },
		"server": func(*http.Request) (*http.Response, error) {
			return response(502, `{"ok":false,"description":"private"}`), nil
		},
		"unreadable": func(*http.Request) (*http.Response, error) { return response(200, `not json`), nil },
		"no message": func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":{}}`), nil },
	} {
		for _, upload := range []bool{false, true} {
			e := newMediaEnv(t)
			e.answer = answer
			block := map[string]any{"type": "divider"}
			if upload {
				block = map[string]any{"type": "document", "local_path": e.write(t, "f.bin", 2)}
			}
			_, err := e.runRich(map[string]any{"blocks": []any{block}})
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "private") ||
				strings.Contains(err.Error(), e.dir) {
				t.Errorf("%s/%t err = %v", name, upload, err)
			}
			if len(e.requests) != 1 {
				t.Errorf("%s/%t requests = %d, want 1", name, upload, len(e.requests))
			}
		}
	}
}

func TestRichMessageErrorsNeverCarryTelegramsDescription(t *testing.T) {
	for _, status := range []int{400, 403, 404, 409, 429} {
		e := newMediaEnv(t)
		e.answer = func(*http.Request) (*http.Response, error) {
			return response(status, `{"ok":false,"description":"private text"}`), nil
		}
		_, err := e.runRich(map[string]any{"blocks": []any{map[string]any{"type": "divider"}}})
		if err == nil || strings.Contains(err.Error(), "private text") || len(e.requests) != 1 {
			t.Errorf("status %d: err = %v, requests = %d", status, err, len(e.requests))
		}
	}
}

func TestRichMessageOnlyCopiesAllowListedResultFields(t *testing.T) {
	e := newMediaEnv(t)
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":{"message_id":8,"date":1700000001,"chat":{"id":-555},`+
			`"text":"private","document":{"file_id":"X"}}}`), nil
	}
	out, err := e.runRich(map[string]any{"blocks": []any{map[string]any{"type": "divider"}}})
	if err != nil || !reflect.DeepEqual(out, map[string]any{"message_id": int64(8), "date": int64(1700000001)}) {
		t.Errorf("out = %v, err = %v", out, err)
	}
}

func TestRichMessageDescriptorDeclaresRiskGroupAndNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	d, _, ok := reg.Lookup("telegram.rich-messages.send")
	if !ok {
		t.Fatal("rich message descriptor is missing")
	}
	if d.Risk != (capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}) ||
		d.Group != groupMessages || d.LocalFiles != config.LocalFilesRead || d.RequiresToolAllowList {
		t.Errorf("descriptor = %+v", d)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if tool == d.ID {
				t.Errorf("profile %s contains %s", profile.ID, d.ID)
			}
		}
	}
	if _, numeric := numericChatMethods["sendRichMessage"]; numeric {
		t.Error("sendRichMessage takes an @username chat_id and must not be numeric-only")
	}
	for _, blockType := range richBlockTypes {
		if blockType == "thinking" {
			t.Error("the draft-only thinking block is offered")
		}
	}
	for _, name := range []string{"business", "html", "markdown", "media", "reply_markup", "message_effect_id"} {
		if strings.Contains(string(d.InputSchema), `"`+name+`":`) {
			t.Errorf("input schema offers %s", name)
		}
	}
}
