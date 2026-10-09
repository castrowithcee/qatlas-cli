package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const mediaChat = "-1001"

type sentRequest struct {
	path        string
	contentType string
	body        []byte
}

type mediaEnv struct {
	dir      string
	resolved *config.Resolved
	secrets  *secret.Resolver
	red      *redact.Redactor
	lookups  int
	requests []sentRequest
	answer   func(*http.Request) (*http.Response, error)
}

func newMediaEnv(t *testing.T, chats ...string) *mediaEnv {
	t.Helper()
	if len(chats) == 0 {
		chats = []string{mediaChat}
	}
	e := &mediaEnv{dir: t.TempDir(), red: &redact.Redactor{}}
	e.secrets = secret.NewWith(func(name string) string {
		e.lookups++
		if name == "TEST_TELEGRAM_BOT_TOKEN" {
			return testToken
		}
		return ""
	}, nil, nil, e.red)
	e.resolved = resolvedWith(chats...)
	e.resolved.Files = config.Files{Read: []string{e.dir}}
	return e
}

func (e *mediaEnv) transport() http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		e.requests = append(e.requests, sentRequest{r.Method + " " + r.URL.Path, r.Header.Get("Content-Type"), data})
		if e.answer != nil {
			return e.answer(r)
		}
		return response(200, `{"ok":true,"result":{"message_id":7,"date":1700000000,`+
			`"photo":[{"file_id":"small"},{"file_id":"BIG"}],"document":{"file_id":"DOC"}}}`), nil
	})
}

func (e *mediaEnv) write(t *testing.T, name string, size int64) string {
	t.Helper()
	path := filepath.Join(e.dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if size > 64 {
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
	} else if _, err := f.WriteString(strings.Repeat("x", int(size))); err != nil {
		t.Fatal(err)
	}
	return path
}

func (e *mediaEnv) run(t *testing.T, tool string, arguments any) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(arguments)
	httpClient := newHTTPClient()
	httpClient.Transport = e.transport()
	ctx := capability.WithConfirmed(context.Background())
	switch tool {
	case "photo", "document":
		return invokeSingleSend(ctx, e.resolved, e.secrets, e.red, raw, httpClient, tool)
	}
	return invokeMediaGroupsSendWith(ctx, e.resolved, e.secrets, e.red, raw, httpClient)
}

// parts decodes a multipart request into its fields and files.
func parts(t *testing.T, r sentRequest) (fields map[string]string, files map[string][2]string) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q", r.contentType)
	}
	fields, files = map[string]string{}, map[string][2]string{}
	reader := multipart.NewReader(bytes.NewReader(r.body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			files[part.FormName()] = [2]string{part.FileName(), string(data)}
		} else {
			fields[part.FormName()] = string(data)
		}
	}
}

func (e *mediaEnv) ref(chat, id string) string { return signRef(testToken, refFile, chat, id) }

func TestMediaSendsUseTheFixedMethodWithAnExactMultipartBody(t *testing.T) {
	for kind, method := range map[string]string{kindPhoto: "sendPhoto", kindDocument: "sendDocument"} {
		e := newMediaEnv(t)
		path := e.write(t, "private-name.JPG", 12)
		out, err := e.run(t, kind, map[string]any{
			"local_path": path, "caption": "hi", "parse_mode": "HTML", "reply_to_message_id": 5,
			"message_thread_id": 9, "disable_notification": true, "protect_content": true,
		})
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/"+method {
			t.Fatalf("%s requests = %+v", kind, e.requests)
		}
		fields, files := parts(t, e.requests[0])
		want := map[string]string{"chat_id": mediaChat, "caption": "hi", "parse_mode": "HTML",
			"reply_parameters": `{"message_id":5}`, "message_thread_id": "9",
			"disable_notification": "true", "protect_content": "true"}
		if len(fields) != len(want) {
			t.Errorf("%s fields = %v, want %v", kind, fields, want)
		}
		for name, value := range want {
			if fields[name] != value {
				t.Errorf("%s field %s = %q, want %q", kind, name, fields[name], value)
			}
		}
		if len(files) != 1 || files[kind] != [2]string{"file.JPG", "xxxxxxxxxxxx"} {
			t.Errorf("%s files = %v", kind, files)
		}
		for _, secretText := range []string{e.dir, "private-name", path} {
			if bytes.Contains(e.requests[0].body, []byte(secretText)) {
				t.Errorf("%s request body contains %q", kind, secretText)
			}
		}
		result := out.(map[string]any)
		parsed, err := parseRef(e.resolved, refFile, result["file_ref"].(string))
		if err != nil || parsed.binding != mediaChat {
			t.Fatalf("%s file_ref = %v, %v", kind, result, err)
		}
		wantID := map[string]string{kindPhoto: "BIG", kindDocument: "DOC"}[kind]
		if id := parsed.id; id != wantID || result["message_id"] != int64(7) || result["date"] != int64(1700000000) || len(result) != 3 {
			t.Errorf("%s result = %v id %q", kind, result, id)
		}
	}
}

func TestMediaSendFromAFileRefUsesAJSONBody(t *testing.T) {
	e := newMediaEnv(t)
	_, err := e.run(t, kindDocument, map[string]any{"file_ref": e.ref(mediaChat, "FID-1"), "caption": "c",
		"reply_to_message_id": 3})
	if err != nil {
		t.Fatal(err)
	}
	r := e.requests[0]
	if len(e.requests) != 1 || r.path != "POST /bot"+testToken+"/sendDocument" || r.contentType != "application/json" {
		t.Fatalf("request = %+v", r)
	}
	var body map[string]any
	_ = json.Unmarshal(r.body, &body)
	want := `{"caption":"c","chat_id":"-1001","document":"FID-1","reply_parameters":{"message_id":3}}`
	got, _ := json.Marshal(body)
	if string(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestMediaSendRejectsSourcesBeforeSecretsAndIO(t *testing.T) {
	outside := t.TempDir()
	e := newMediaEnv(t, mediaChat, "-1002")
	local := e.write(t, "a.jpg", 4)
	cases := map[string]map[string]any{
		"raw file_id":      {"file_ref": "AgADBAADrawFileId", "chat": mediaChat},
		"foreign chat":     {"file_ref": e.ref(mediaChat, "X"), "chat": "-999"},
		"several chats":    {"file_ref": e.ref(mediaChat, "X")},
		"other chat's ref": {"file_ref": e.ref("-1002", "X"), "chat": mediaChat},
		"unbound ref":      {"file_ref": e.ref("-777", "X"), "chat": mediaChat},
		"callback ref":     {"file_ref": signRef(testToken, refCallback, mediaChat, "X"), "chat": mediaChat},
		"both sources":     {"file_ref": e.ref(mediaChat, "X"), "local_path": local, "chat": mediaChat},
		"no source":        {"chat": mediaChat},
		"outside release":  {"local_path": filepath.Join(outside, "b.jpg"), "chat": mediaChat},
		"long caption":     {"local_path": local, "caption": strings.Repeat("x", 1025), "chat": mediaChat},
		"parse mode":       {"local_path": local, "parse_mode": "Markdown", "chat": mediaChat},
	}
	for name, arguments := range cases {
		for _, kind := range []string{kindPhoto, kindDocument} {
			_, err := e.run(t, kind, arguments)
			if err == nil {
				t.Errorf("%s/%s was accepted", name, kind)
				continue
			}
			for _, leaked := range []string{outside, "-999", "-777", e.dir} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("%s/%s error leaks %q: %v", name, kind, leaked, err)
				}
			}
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want none", e.lookups, len(e.requests))
	}
}

func TestMediaSendRejectsAReferenceOfAnotherBotBeforeAnyRequest(t *testing.T) {
	e := newMediaEnv(t)
	forged := signRef("999:other_bot-token", refFile, mediaChat, "X")
	if _, err := e.run(t, kindPhoto, map[string]any{"file_ref": forged}); err == nil {
		t.Fatal("a reference signed for another bot was accepted")
	}
	if len(e.requests) != 0 {
		t.Errorf("requests = %d, want 0", len(e.requests))
	}
}

func TestMediaSendChecksSizeLimitsBeforeSecretsAndUpload(t *testing.T) {
	e := newMediaEnv(t)
	bigPhoto := e.write(t, "big.jpg", maxPhotoBytes+1)
	bigDoc := e.write(t, "big.bin", maxUploadBytes+1)
	if _, err := e.run(t, kindPhoto, map[string]any{"local_path": bigPhoto}); err == nil {
		t.Error("a photo above 10 MB was accepted")
	}
	if _, err := e.run(t, kindDocument, map[string]any{"local_path": bigDoc}); err == nil {
		t.Error("a document above 50 MB was accepted")
	}
	if _, err := e.run(t, "album", map[string]any{"type": "photo", "items": []any{
		map[string]any{"local_path": bigPhoto}, map[string]any{"local_path": bigPhoto}}}); err == nil {
		t.Error("an album photo above 10 MB was accepted")
	}
	half := e.write(t, "half.bin", maxUploadBytes/2+1)
	if _, err := e.run(t, "album", map[string]any{"type": "document", "items": []any{
		map[string]any{"local_path": half}, map[string]any{"local_path": half}}}); err == nil {
		t.Error("an album above 50 MB in total was accepted")
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want none", e.lookups, len(e.requests))
	}
	// A photo of exactly 10 MB and a document of exactly 50 MB stay within the limits.
	okPhoto := e.write(t, "ok.jpg", maxPhotoBytes)
	if _, err := e.run(t, kindPhoto, map[string]any{"local_path": okPhoto}); err != nil {
		t.Errorf("10 MB photo: %v", err)
	}
	okDoc := e.write(t, "ok.bin", maxUploadBytes)
	if _, err := e.run(t, kindDocument, map[string]any{"local_path": okDoc}); err != nil {
		t.Errorf("50 MB document: %v", err)
	}
}

func TestAlbumBuildsMediaWithAttachReferencesInOneRequest(t *testing.T) {
	e := newMediaEnv(t)
	a, b := e.write(t, "one.png", 3), e.write(t, "two.jpeg", 4)
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":[`+
			`{"message_id":1,"date":10,"photo":[{"file_id":"P1"}]},{"message_id":2,"date":10,"photo":[{"file_id":"P2"}]},`+
			`{"message_id":3,"date":10,"photo":[{"file_id":"P3"}]}]}`), nil
	}
	out, err := e.run(t, "album", map[string]any{"type": "photo", "caption": "cap", "parse_mode": "HTML",
		"protect_content": true, "items": []any{
			map[string]any{"local_path": a}, map[string]any{"file_ref": e.ref(mediaChat, "KNOWN")},
			map[string]any{"local_path": b}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/sendMediaGroup" {
		t.Fatalf("requests = %+v", e.requests)
	}
	fields, files := parts(t, e.requests[0])
	wantMedia := `[{"type":"photo","media":"attach://file0","caption":"cap","parse_mode":"HTML"},` +
		`{"type":"photo","media":"KNOWN"},{"type":"photo","media":"attach://file2"}]`
	if fields["media"] != wantMedia || fields["chat_id"] != mediaChat || fields["protect_content"] != "true" || len(fields) != 3 {
		t.Errorf("fields = %v", fields)
	}
	if len(files) != 2 || files["file0"] != [2]string{"file-1.png", "xxx"} || files["file2"] != [2]string{"file-3.jpeg", "xxxx"} {
		t.Errorf("files = %v", files)
	}
	if bytes.Contains(e.requests[0].body, []byte(e.dir)) || bytes.Contains(e.requests[0].body, []byte("one.png")) {
		t.Error("the request body names a local path")
	}
	messages := out.(map[string]any)["messages"].([]map[string]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %v", messages)
	}
	for i, m := range messages {
		parsed, err := parseRef(e.resolved, refFile, m["file_ref"].(string))
		if err != nil || parsed.id != []string{"P1", "P2", "P3"}[i] || m["message_id"] != int64(i+1) {
			t.Errorf("message %d = %v, %v", i, m, err)
		}
	}
}

func TestAlbumOfReferencesUsesAJSONBodyAndDocuments(t *testing.T) {
	e := newMediaEnv(t)
	e.answer = func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true,"result":[`+
			`{"message_id":1,"date":10,"document":{"file_id":"D1"}},{"message_id":2,"date":10,"document":{"file_id":"D2"}}]}`), nil
	}
	_, err := e.run(t, "album", map[string]any{"type": "document", "items": []any{
		map[string]any{"file_ref": e.ref(mediaChat, "A")}, map[string]any{"file_ref": e.ref(mediaChat, "B")}}})
	if err != nil {
		t.Fatal(err)
	}
	r := e.requests[0]
	want := `{"chat_id":"-1001","media":[{"type":"document","media":"A"},{"type":"document","media":"B"}]}`
	if len(e.requests) != 1 || r.contentType != "application/json" || string(r.body) != want {
		t.Errorf("request = %s %s", r.contentType, r.body)
	}
}

func TestAlbumBoundsAndNoMixing(t *testing.T) {
	e := newMediaEnv(t)
	item := map[string]any{"file_ref": e.ref(mediaChat, "A")}
	list := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = item
		}
		return out
	}
	for name, arguments := range map[string]map[string]any{
		"one item":     {"type": "photo", "items": list(1)},
		"eleven items": {"type": "photo", "items": list(11)},
		"audio":        {"type": "audio", "items": list(2)},
		"mixed":        {"type": "photo", "items": []any{item, map[string]any{"type": "document", "file_ref": e.ref(mediaChat, "B")}}},
		"no type":      {"items": list(2)},
	} {
		if _, err := e.run(t, "album", arguments); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("secret lookups = %d, requests = %d, want none", e.lookups, len(e.requests))
	}
	for _, n := range []int{2, 10} {
		e.answer = func(*http.Request) (*http.Response, error) {
			var messages []string
			for i := 0; i < n; i++ {
				messages = append(messages, `{"message_id":1,"date":1,"photo":[{"file_id":"P"}]}`)
			}
			return response(200, `{"ok":true,"result":[`+strings.Join(messages, ",")+`]}`), nil
		}
		if _, err := e.run(t, "album", map[string]any{"type": "photo", "items": list(n)}); err != nil {
			t.Errorf("%d items: %v", n, err)
		}
	}
}

func TestMediaSendsNeverRetryAndReportUncertainty(t *testing.T) {
	for name, answer := range map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) { return nil, &timeoutError{} },
		"server": func(*http.Request) (*http.Response, error) {
			return response(502, `{"ok":false,"description":"private"}`), nil
		},
		"unreadable": func(*http.Request) (*http.Response, error) { return response(200, `not json`), nil },
		"no message": func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":{}}`), nil },
	} {
		for _, kind := range []string{kindPhoto, kindDocument, "album"} {
			e := newMediaEnv(t)
			e.answer = answer
			path := e.write(t, "f.bin", 2)
			arguments := map[string]any{"local_path": path}
			if kind == "album" {
				arguments = map[string]any{"type": "photo", "items": []any{map[string]any{"local_path": path}, map[string]any{"local_path": path}}}
			}
			_, err := e.run(t, kind, arguments)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "private") ||
				strings.Contains(err.Error(), e.dir) {
				t.Errorf("%s/%s err = %v", name, kind, err)
			}
			if len(e.requests) != 1 {
				t.Errorf("%s/%s requests = %d, want 1", name, kind, len(e.requests))
			}
		}
	}
}

func TestMediaToolsDeclareRiskGroupAndProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []capability.Descriptor{photosSend, documentsSend, mediaGroupsSend} {
		if d.Risk != mediaRisk || d.Group != groupMedia || d.LocalFiles != config.LocalFilesRead || d.RequiresToolAllowList ||
			d.Risk.Effect != capability.EffectCreate || d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Errorf("%s descriptor = %+v", d.ID, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	found := false
	for _, profile := range metadata.Profiles {
		if profile.ID == "media" {
			found = !profile.Recommended && len(profile.Tools) == 3 && profile.Tools[0] == photosSend.ID &&
				profile.Tools[1] == documentsSend.ID && profile.Tools[2] == mediaGroupsSend.ID
		}
	}
	if !found {
		t.Errorf("profiles = %+v, want media with exactly the three send tools", metadata.Profiles)
	}
}
