package infomaniakchat

import (
	"encoding/base64"
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
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	uploadedFile   = "file00000000000000000f0f"
	uploadContent  = "upload-content-canary-3b7d"
	uploadResponse = `{"file_infos":[{"id":"` + uploadedFile + `","user_id":"` + selfID + `","post_id":"",` +
		`"create_at":1735689600000,"delete_at":0,"name":"report.txt","extension":"txt","size":26,` +
		`"mime_type":"text/plain"}],"client_ids":[]}`
)

func uploadEnv(t *testing.T, dir string, calls *[]call, handler func(*http.Request) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	cfg := coreConfig()
	for _, name := range []string{"team", "channel"} {
		connection := cfg.Connections[name]
		connection.Files = config.Files{Read: []string{dir}}
		cfg.Connections["files-"+name] = connection
	}
	reads := 0
	red := &redact.Redactor{}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// uploadServer answers the scope checks and the upload; upload handles POST /api/v4/files.
func uploadServer(t *testing.T, upload func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	posts := postServer(t, nil)
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v4/files" {
			return upload(r)
		}
		return posts(r)
	}
}

func okUpload(*http.Request) (*http.Response, error) { return jsonResponse(201, uploadResponse), nil }

func inlineArgs(channel, name, content string) string {
	return `{"channel_id":"` + channel + `","name":` + strconv.Quote(name) + `,"content_base64":"` +
		base64.StdEncoding.EncodeToString([]byte(content)) + `"}`
}

func uploadCalls(calls []call) int {
	n := 0
	for _, c := range calls {
		if c.method == http.MethodPost && c.path == "/api/v4/files" {
			n++
		}
	}
	return n
}

func TestFilesUploadDescriptorAndProfile(t *testing.T) {
	d := filesUpload
	if d.LocalFiles != config.LocalFilesRead || d.Risk.Effect != capability.EffectCreate ||
		d.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.RequiresToolAllowList ||
		withGroup(d).Group != "files" || messagesSend.Version != 2 {
		t.Fatalf("descriptor = %+v", d)
	}
	if !strings.Contains(d.Description, "stays in kChat") {
		t.Fatalf("description = %q, want the leftover upload named", d.Description)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		has := false
		for _, id := range profile.Tools {
			has = has || id == filesUpload.ID
		}
		if has != (profile.ID == "messaging") {
			t.Fatalf("profile %s = %v", profile.ID, profile.Tools)
		}
	}
}

func TestFilesUploadSendsOneMultipartWithChannelAndFileOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte(uploadContent), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]string{
		"local":  `{"channel_id":"` + chanA + `","local_path":` + strconv.Quote(filepath.Join(dir, "report.txt")) + `}`,
		"inline": inlineArgs(chanA, "report.txt", uploadContent),
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			var contentType, length string
			env := uploadEnv(t, dir, &calls, uploadServer(t, func(r *http.Request) (*http.Response, error) {
				contentType, length = r.Header.Get("Content-Type"), strconv.FormatInt(r.ContentLength, 10)
				return okUpload(r)
			}))
			result, err := env.confirmed(filesUpload.ID, "files-channel", args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			var out UploadResult
			if err := json.Unmarshal([]byte(result), &out); err != nil || out.FileID != uploadedFile ||
				out.Name != "report.txt" || out.Size != int64(len(uploadContent)) || out.MimeType != "text/plain" {
				t.Fatalf("result = %s, %v", result, err)
			}
			if uploadCalls(calls) != 1 {
				t.Fatalf("calls = %+v, want exactly one upload", calls)
			}
			media, params, err := mime.ParseMediaType(contentType)
			if err != nil || media != "multipart/form-data" {
				t.Fatalf("content type = %q, %v", contentType, err)
			}
			var body string
			for _, c := range calls {
				if c.path == "/api/v4/files" {
					body = c.body
				}
			}
			if length != strconv.Itoa(len(body)) {
				t.Fatalf("content length %s, body %d", length, len(body))
			}
			reader := multipart.NewReader(strings.NewReader(body), params["boundary"])
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
				case "channel_id":
					if string(data) != chanA {
						t.Fatalf("channel_id = %q", data)
					}
				case "files":
					if part.FileName() != "report.txt" || string(data) != uploadContent {
						t.Fatalf("file = %q %q", part.FileName(), data)
					}
				default:
					t.Fatalf("unexpected part %q", part.FormName())
				}
				parts = append(parts, part.FormName())
			}
			if strings.Join(parts, ",") != "channel_id,files" {
				t.Fatalf("parts = %v, want channel_id then files", parts)
			}
		})
	}
}

func TestFilesUploadRefusesBeforeSecretOrRequest(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	for _, p := range []string{filepath.Join(dir, "a.txt"), filepath.Join(outside, "b.txt")} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ name, connection, args string }{
		{"channel outside allow-list", "files-channel", inlineArgs(chanC, "a.txt", "x")},
		{"source outside release", "files-team", `{"channel_id":"` + chanA + `","local_path":` +
			strconv.Quote(filepath.Join(outside, "b.txt")) + `}`},
		{"both sources", "files-team", `{"channel_id":"` + chanA + `","local_path":"` + filepath.Join(dir, "a.txt") +
			`","content_base64":"eA=="}`},
		{"no source", "files-team", `{"channel_id":"` + chanA + `"}`},
		{"inline without name", "files-team", `{"channel_id":"` + chanA + `","content_base64":"eA=="}`},
		{"name with local path", "files-team", `{"channel_id":"` + chanA + `","name":"x","local_path":` +
			strconv.Quote(filepath.Join(dir, "a.txt")) + `}`},
		{"path in name", "files-team", inlineArgs(chanA, "../x.txt", "x")},
		{"control character in name", "files-team", inlineArgs(chanA, "a\nb.txt", "x")},
		{"name too long", "files-team", inlineArgs(chanA, strings.Repeat("a", 256), "x")},
		{"invalid base64", "files-team", `{"channel_id":"` + chanA + `","name":"a","content_base64":"***"}`},
		{"malformed channel", "files-team", inlineArgs("../x", "a", "x")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := uploadEnv(t, dir, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				return nil, nil
			})
			_, err := env.confirmed(filesUpload.ID, tc.connection, tc.args)
			if err == nil {
				t.Fatal("err = nil, want a refusal")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestFilesUploadRefusesForeignTeamChannelBeforeTheUpload(t *testing.T) {
	var calls []call
	env := uploadEnv(t, t.TempDir(), &calls, uploadServer(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("upload request sent")
		return nil, nil
	}))
	_, err := env.confirmed(filesUpload.ID, "files-team", inlineArgs(chanB, "a.txt", uploadContent))
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want an invalid request", err)
	}
	if len(calls) != 1 || calls[0].path != "/api/v4/channels/"+chanB {
		t.Fatalf("calls = %+v, want the one scope check", calls)
	}
	if strings.Contains(err.Error(), chanB) || strings.Contains(err.Error(), teamB) {
		t.Fatalf("error names the foreign target: %v", err)
	}
}

func TestFilesUploadRequiresConfirmation(t *testing.T) {
	var calls []call
	env := uploadEnv(t, t.TempDir(), &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})
	_, err := env.invoke(filesUpload.ID, "files-team", inlineArgs(chanA, "a.txt", "x"))
	if !isConfirmationRequired(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestFilesUploadTooLargeIs413AndLocalLimit(t *testing.T) {
	var calls []call
	env := uploadEnv(t, t.TempDir(), &calls, uploadServer(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusRequestEntityTooLarge, `{"message":"`+messageCanary+`"}`), nil
	}))
	_, err := env.confirmed(filesUpload.ID, "files-team", inlineArgs(chanA, "a.txt", uploadContent))
	if err == nil || !strings.Contains(err.Error(), "too large") || strings.Contains(err.Error(), messageCanary) ||
		strings.Contains(err.Error(), "may have been uploaded") {
		t.Fatalf("err = %v, want too large without the provider text or an uncertainty hint", err)
	}
	if uploadCalls(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestFilesUploadUnclearResultIsNeverRepeated(t *testing.T) {
	for name, handler := range map[string]func(*http.Request) (*http.Response, error){
		"5xx": func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+uploadContent+`"}`), nil
		},
		"transport": func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") },
		"invalid json": func(*http.Request) (*http.Response, error) {
			return jsonResponse(201, `not json`), nil
		},
		"no file": func(*http.Request) (*http.Response, error) {
			return jsonResponse(201, `{"file_infos":[]}`), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := uploadEnv(t, t.TempDir(), &calls, uploadServer(t, handler))
			_, err := env.confirmed(filesUpload.ID, "files-team", inlineArgs(chanA, "a.txt", uploadContent))
			if err == nil || !strings.Contains(err.Error(), "may have been uploaded") ||
				strings.Contains(err.Error(), uploadContent) {
				t.Fatalf("err = %v, want the unclear outcome named without content", err)
			}
			if uploadCalls(calls) != 1 {
				t.Fatalf("calls = %+v, want exactly one upload", calls)
			}
		})
	}
}
