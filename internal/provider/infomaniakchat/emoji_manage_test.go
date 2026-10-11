package infomaniakchat

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	emojiNew     = "emjn0000000000000000000n1"
	emojiForeign = "emjf0000000000000000000f2"
)

var (
	pngImage  = append([]byte("\x89PNG\r\n\x1a\n"), []byte("png-body-canary")...)
	gifImage  = append([]byte("GIF89a"), []byte("gif-body-canary")...)
	jpegImage = append([]byte("\xff\xd8\xff\xe0"), []byte("jpeg-body-canary")...)
)

func emojiEnv(t *testing.T, dir string, calls *[]call, handler func(*http.Request) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	cfg := coreConfig()
	for _, name := range []string{"emojiadm", "emojiadmch", "emojiadm1", "emojinotool"} {
		connection := cfg.Connections[name]
		connection.Files = config.Files{Read: []string{dir}}
		cfg.Connections[name] = connection
	}
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func writeImage(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func createArgs(name, path string) string {
	return `{"name":` + strconv.Quote(name) + `,"local_path":` + strconv.Quote(path) + `}`
}

func emojiResource(id, name, creator string) string {
	return `{"id":"` + id + `","name":"` + name + `","creator_id":"` + creator + `","create_at":1735689600000,` +
		`"update_at":5,"image":"leak-canary"}`
}

// emojiWriteHandler answers the gate, users/me, the emoji read, and hands emoji writes to change.
func emojiWriteHandler(t *testing.T, teams string, change func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me/teams":
			return jsonResponse(200, teams), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me":
			return jsonResponse(200, `{"id":"`+selfID+`"}`), nil
		case r.URL.Path == "/api/v4/emoji" || strings.HasPrefix(r.URL.Path, "/api/v4/emoji/"):
			return change(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func emojiWrites(calls []call) int {
	n := 0
	for _, c := range calls {
		if c.method == http.MethodPost || c.method == http.MethodDelete {
			n++
		}
	}
	return n
}

func TestEmojiWriteDescriptorsAndProfiles(t *testing.T) {
	c, d := emojiCreate, emojiDelete
	if c.LocalFiles != config.LocalFilesRead || c.Risk.Effect != capability.EffectCreate ||
		c.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		c.Risk.Confirmation != capability.ConfirmationRequired || !c.Risk.OpenWorld ||
		c.Risk.DataSensitivity != administrationSensitivity || !c.RequiresToolAllowList ||
		withGroup(c).Group != "administration" {
		t.Fatalf("create = %+v", c)
	}
	if d.LocalFiles != "" || d.Risk.Effect != capability.EffectDelete ||
		d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
		d.Risk.DataSensitivity != administrationSensitivity || !d.RequiresToolAllowList ||
		withGroup(d).Group != "administration" {
		t.Fatalf("delete = %+v", d)
	}
}

func TestEmojiCreateSendsOneMultipartWithImageAndResource(t *testing.T) {
	for name, tc := range map[string]struct {
		data []byte
		mime string
	}{"png": {pngImage, "image/png"}, "gif": {gifImage, "image/gif"}, "jpeg": {jpegImage, "image/jpeg"}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeImage(t, dir, "party."+name, tc.data)
			var calls []call
			var contentType string
			var length int64
			env := emojiEnv(t, dir, &calls, emojiWriteHandler(t, bothTeams, func(r *http.Request) (*http.Response, error) {
				contentType, length = r.Header.Get("Content-Type"), r.ContentLength
				return jsonResponse(201, emojiResource(emojiNew, "party_1", selfID)), nil
			}))
			result, err := env.confirmed(emojiCreate.ID, "emojiadm", createArgs("party_1", path))
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			var out EmojiEntry
			if err := json.Unmarshal([]byte(result), &out); err != nil || out.ID != emojiNew || out.Name != "party_1" ||
				out.CreatorID != selfID || out.CreatedAt != "2025-01-01T00:00:00Z" || strings.Contains(result, "leak-canary") {
				t.Fatalf("result = %s, %v", result, err)
			}
			if len(calls) != 3 || calls[0].path != "/api/v4/users/me/teams" || calls[1].path != "/api/v4/users/me" ||
				calls[2].method != http.MethodPost || calls[2].path != "/api/v4/emoji" || emojiWrites(calls) != 1 {
				t.Fatalf("calls = %+v, want the gate, users/me, and one POST", calls)
			}
			media, params, err := mime.ParseMediaType(contentType)
			if err != nil || media != "multipart/form-data" || length != int64(len(calls[2].body)) {
				t.Fatalf("content type = %q (%v), length %d, body %d", contentType, err, length, len(calls[2].body))
			}
			reader := multipart.NewReader(strings.NewReader(calls[2].body), params["boundary"])
			var parts []string
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("part: %v", err)
				}
				data, _ := io.ReadAll(part)
				switch part.FormName() {
				case "emoji":
					if string(data) != `{"name":"party_1","creator_id":"`+selfID+`"}` || part.FileName() != "" {
						t.Fatalf("emoji part = %q", data)
					}
				case "image":
					if part.FileName() != "party."+name || part.Header.Get("Content-Type") != tc.mime ||
						!bytes.Equal(data, tc.data) {
						t.Fatalf("image part = %q %q %q", part.FileName(), part.Header.Get("Content-Type"), data)
					}
				default:
					t.Fatalf("unexpected part %q", part.FormName())
				}
				parts = append(parts, part.FormName())
			}
			if strings.Join(parts, ",") != "emoji,image" {
				t.Fatalf("parts = %v, want emoji then image", parts)
			}
		})
	}
}

func TestEmojiCreateRefusesLocallyBeforeSecretOrRequest(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	good := writeImage(t, dir, "good.png", pngImage)
	text := writeImage(t, dir, "fake.png", []byte("not an image at all"))
	empty := writeImage(t, dir, "empty.png", nil)
	exact := writeImage(t, dir, "exact.png", append(append([]byte{}, pngImage...), make([]byte, maxEmojiBytes-len(pngImage))...))
	big := writeImage(t, dir, "big.png", append(append([]byte{}, pngImage...), make([]byte, maxEmojiBytes)...))
	far := writeImage(t, outside, "far.png", pngImage)
	cases := []struct{ name, connection, args string }{
		{"channel target", "emojiadmch", createArgs("party", good)},
		{"wrong magic bytes", "emojiadm", createArgs("party", text)},
		{"empty file", "emojiadm", createArgs("party", empty)},
		{"over the size limit", "emojiadm", createArgs("party", big)},
		{"outside the release", "emojiadm", createArgs("party", far)},
		{"missing file", "emojiadm", createArgs("party", filepath.Join(dir, "missing.png"))},
		{"upper case name", "emojiadm", createArgs("Party", good)},
		{"colon in name", "emojiadm", createArgs("pa:rty", good)},
		{"empty name", "emojiadm", createArgs("", good)},
		{"name too long", "emojiadm", createArgs(strings.Repeat("a", 65), good)},
		{"missing path", "emojiadm", `{"name":"party"}`},
		{"inline content", "emojiadm", `{"name":"party","content_base64":"eA=="}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := emojiEnv(t, dir, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				return nil, nil
			})
			_, err := env.confirmed(emojiCreate.ID, tc.connection, tc.args)
			if err == nil || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), chanA) {
				t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
			}
		})
	}
	// A file of exactly the limit passes the local checks.
	var calls []call
	env := emojiEnv(t, dir, &calls, emojiWriteHandler(t, bothTeams, func(*http.Request) (*http.Response, error) {
		return jsonResponse(201, emojiResource(emojiNew, "party", selfID)), nil
	}))
	if _, err := env.confirmed(emojiCreate.ID, "emojiadm", createArgs("party", exact)); err != nil {
		t.Fatalf("exact-size file: %v", err)
	}
}

func TestEmojiCreateNeedsConfirmationAndBoundTeams(t *testing.T) {
	dir := t.TempDir()
	path := writeImage(t, dir, "party.png", pngImage)
	var calls []call
	env := emojiEnv(t, dir, &calls, emojiWriteHandler(t, bothTeams, func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected emoji write")
		return nil, nil
	}))
	_, err := env.invoke(emojiCreate.ID, "emojiadm", createArgs("party", path))
	if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
	_, err = env.confirmed(emojiCreate.ID, "emojiadm1", createArgs("party", path))
	if !isInvalidRequest(err) || strings.Contains(err.Error(), teamB) || len(calls) != 1 ||
		calls[0].path != "/api/v4/users/me/teams" {
		t.Fatalf("err = %v, calls = %+v, want the refusal after the team read alone", err, calls)
	}
}

func TestEmojiCreateIsRefusedWithoutToolsList(t *testing.T) {
	dir := t.TempDir()
	path := writeImage(t, dir, "party.png", pngImage)
	var calls []call
	env := emojiEnv(t, dir, &calls, emojiWriteHandler(t, bothTeams, func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected emoji write")
		return nil, nil
	}))
	_, err := env.confirmed(emojiCreate.ID, "emojinotool", createArgs("party", path))
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestEmojiCreateUnclearResultIsNeverRepeated(t *testing.T) {
	dir := t.TempDir()
	path := writeImage(t, dir, "party.png", pngImage)
	for name, handler := range map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) {
			return nil, timeoutError{}
		},
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"provider text canary"}`), nil
		},
		"invalid json": func(*http.Request) (*http.Response, error) { return jsonResponse(201, `not json`), nil },
		"other emoji": func(*http.Request) (*http.Response, error) {
			return jsonResponse(201, emojiResource(emojiNew, "other", selfID)), nil
		},
		"other creator": func(*http.Request) (*http.Response, error) {
			return jsonResponse(201, emojiResource(emojiNew, "party", "othr0000000000000000000x9")), nil
		},
		"bad id": func(*http.Request) (*http.Response, error) {
			return jsonResponse(201, emojiResource("../x", "party", selfID)), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := emojiEnv(t, dir, &calls, emojiWriteHandler(t, bothTeams, handler))
			_, err := env.confirmed(emojiCreate.ID, "emojiadm", createArgs("party", path))
			if err == nil || !strings.Contains(err.Error(), "the emoji may have been created, list the emoji before creating it again") ||
				strings.Contains(err.Error(), "canary") || emojiWrites(calls) != 1 {
				t.Fatalf("err = %v, calls = %+v, want one write and the unclear outcome named", err, calls)
			}
		})
	}
}

func TestEmojiCreateRejectionIsAProviderErrorWithoutRepeat(t *testing.T) {
	dir := t.TempDir()
	path := writeImage(t, dir, "party.png", pngImage)
	var calls []call
	env := emojiEnv(t, dir, &calls, emojiWriteHandler(t, bothTeams, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{"message":"provider text canary"}`), nil
	}))
	_, err := env.confirmed(emojiCreate.ID, "emojiadm", createArgs("party", path))
	if classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), "canary") ||
		strings.Contains(err.Error(), "may have been") || emojiWrites(calls) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func deleteHandler(t *testing.T, read func(*http.Request) (*http.Response, error),
	del func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return emojiWriteHandler(t, bothTeams, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/emoji/"+emojiNew:
			return read(r)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v4/emoji/"+emojiNew:
			return del(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
}

func okRead(*http.Request) (*http.Response, error) {
	return jsonResponse(200, emojiResource(emojiNew, "party", selfID)), nil
}

func okDelete(*http.Request) (*http.Response, error) {
	return jsonResponse(200, `{"status":"OK"}`), nil
}

func TestEmojiDeleteReadsThenDeletesOnce(t *testing.T) {
	var calls []call
	env := emojiEnv(t, t.TempDir(), &calls, deleteHandler(t, okRead, okDelete))
	result, err := env.confirmed(emojiDelete.ID, "emojiadm", `{"emoji_id":"`+emojiNew+`"}`)
	if err != nil || !strings.Contains(result, `"id":"`+emojiNew+`"`) || strings.Contains(result, "leak-canary") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if len(calls) != 3 || calls[0].path != "/api/v4/users/me/teams" || calls[1].method != http.MethodGet ||
		calls[1].path != "/api/v4/emoji/"+emojiNew || calls[2].method != http.MethodDelete ||
		calls[2].path != "/api/v4/emoji/"+emojiNew || calls[2].body != "" {
		t.Fatalf("calls = %+v, want the gate, the read, and one DELETE", calls)
	}
}

func TestEmojiDeleteSendsNoDeleteForAMismatchedOrMissingEmoji(t *testing.T) {
	for name, read := range map[string]func(*http.Request) (*http.Response, error){
		"other id": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, emojiResource(emojiForeign, "party", selfID)), nil
		},
		"empty":     func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{}`), nil },
		"not found": func(*http.Request) (*http.Response, error) { return jsonResponse(404, `{}`), nil },
		"forbidden": func(*http.Request) (*http.Response, error) { return jsonResponse(403, `{}`), nil },
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := emojiEnv(t, t.TempDir(), &calls, deleteHandler(t, read, okDelete))
			_, err := env.confirmed(emojiDelete.ID, "emojiadm", `{"emoji_id":"`+emojiNew+`"}`)
			if err == nil || emojiWrites(calls) != 0 || len(calls) != 2 {
				t.Fatalf("err = %v, calls = %+v, want no DELETE", err, calls)
			}
		})
	}
}

func TestEmojiDeleteRefusesBeforeAnyEmojiRequest(t *testing.T) {
	cases := []struct {
		name, connection, args string
		confirmed              bool
		reads, calls           int
	}{
		{"malformed id", "emojiadm", `{"emoji_id":"../x"}`, true, 0, 0},
		{"channel target", "emojiadmch", `{"emoji_id":"` + emojiNew + `"}`, true, 0, 0},
		{"no tools list", "emojinotool", `{"emoji_id":"` + emojiNew + `"}`, true, 0, 0},
		{"no confirmation", "emojiadm", `{"emoji_id":"` + emojiNew + `"}`, false, 0, 0},
		{"unbound team", "emojiadm1", `{"emoji_id":"` + emojiNew + `"}`, true, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := emojiEnv(t, t.TempDir(), &calls, deleteHandler(t, okRead, okDelete))
			var err error
			if tc.confirmed {
				_, err = env.confirmed(emojiDelete.ID, tc.connection, tc.args)
			} else {
				_, err = env.invoke(emojiDelete.ID, tc.connection, tc.args)
			}
			if err == nil || len(calls) != tc.calls || *env.reads != tc.reads || emojiWrites(calls) != 0 {
				t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
			}
		})
	}
}

func TestEmojiDeleteUnclearResultIsNeverRepeated(t *testing.T) {
	for name, del := range map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) { return nil, timeoutError{} },
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusBadGateway, `{"message":"provider text canary"}`), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := emojiEnv(t, t.TempDir(), &calls, deleteHandler(t, okRead, del))
			_, err := env.confirmed(emojiDelete.ID, "emojiadm", `{"emoji_id":"`+emojiNew+`"}`)
			if err == nil || !strings.Contains(err.Error(), "the emoji may have been deleted, list the emoji before deleting it again") ||
				strings.Contains(err.Error(), "canary") || emojiWrites(calls) != 1 {
				t.Fatalf("err = %v, calls = %+v", err, calls)
			}
		})
	}
}
