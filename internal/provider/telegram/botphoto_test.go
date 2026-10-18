package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func (e *mediaEnv) setProfilePhoto(arguments any) (any, error) {
	raw, _ := json.Marshal(arguments)
	return invokeProfileSetPhotoWith(capability.WithConfirmed(context.Background()), e.resolved, e.secrets, e.red,
		raw, e.client())
}

func TestProfileSetPhotoSendsExactNeutralMultipart(t *testing.T) {
	e := newMediaEnv(t, "bot")
	e.answer = func(*http.Request) (*http.Response, error) { return response(200, `{"ok":true,"result":true}`), nil }
	path := e.write(t, "private-name.JPG", 12)
	out, err := e.setProfilePhoto(map[string]any{"local_path": path})
	if err != nil || out.(map[string]any)["updated"] != true || len(out.(map[string]any)) != 1 {
		t.Fatalf("out = %v, %v", out, err)
	}
	if len(e.requests) != 1 || e.requests[0].path != "POST /bot"+testToken+"/setMyProfilePhoto" {
		t.Fatalf("requests = %+v", e.requests)
	}
	fields, files := parts(t, e.requests[0])
	if len(fields) != 1 || fields["photo"] != `{"type":"static","photo":"attach://profile"}` || len(files) != 1 ||
		files["profile"] != [2]string{"file.JPG", strings.Repeat("x", 12)} {
		t.Errorf("fields = %v files = %v", fields, files)
	}
	body := string(e.requests[0].body)
	if strings.Contains(body, e.dir) || strings.Contains(body, "private-name") {
		t.Error("path or file name reached the request")
	}
}

func TestProfileSetPhotoRefusesBeforeSecretAndRead(t *testing.T) {
	e := newMediaEnv(t, "bot")
	outside := t.TempDir() + "/a.jpg"
	for name, arguments := range map[string]map[string]any{
		"outside release": {"local_path": outside},
		"too large":       {"local_path": e.write(t, "big.jpg", maxPhotoBytes+1)},
		"missing path":    {},
		"file ref":        {"file_ref": "x"},
		"url":             {"local_path": "https://example.test/a.jpg"},
		"animated":        {"local_path": e.write(t, "a.mp4", 4)},
		"png":             {"local_path": e.write(t, "a.png", 4)},
		"chat argument":   {"local_path": e.write(t, "ok.jpg", 4), "chat": "-1001"},
	} {
		if _, err := e.setProfilePhoto(arguments); err == nil || strings.Contains(err.Error(), e.dir) {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("lookups = %d requests = %d, want 0", e.lookups, len(e.requests))
	}
}

func TestProfileToolsRefuseWithoutBotTargetBeforeSecret(t *testing.T) {
	e := newMediaEnv(t, mediaChat)
	path := e.write(t, "ok.jpg", 4)
	if _, err := e.setProfilePhoto(map[string]any{"local_path": path}); err == nil {
		t.Error("setphoto accepted without bot target")
	}
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	if _, err := invokeProfileRemovePhoto(context.Background(), resolvedWith("42"), resolver, nil, json.RawMessage(`{}`)); err == nil {
		t.Error("removephoto accepted without bot target")
	}
	if resolutions != 0 || e.lookups != 0 || len(e.requests) != 0 {
		t.Errorf("resolutions = %d lookups = %d requests = %d", resolutions, e.lookups, len(e.requests))
	}
}

func TestProfileRemovePhotoSendsOneEmptyRequest(t *testing.T) {
	client, bodies, paths := capture(t, "bot", 200, `{"ok":true,"result":true}`)
	out, err := client.RemoveProfilePhoto(context.Background())
	if err != nil || out["deleted"] != true || len(*paths) != 1 || (*paths)[0] != "removeMyProfilePhoto" ||
		(*bodies)[0] != `{}` {
		t.Errorf("out = %v, %v, %v, %v", out, err, *paths, *bodies)
	}
}

func TestProfileMutationsSendOneRequestAndReportUncertainty(t *testing.T) {
	ops := map[string]func(*Client) error{
		"setphoto": func(c *Client) error {
			_, err := c.SetProfilePhoto(context.Background(), mediaItem{name: "file.jpg", data: []byte("x")})
			return err
		},
		"removephoto": func(c *Client) error { _, err := c.RemoveProfilePhoto(context.Background()); return err },
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "bot", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			if len(*bodies) != 1 {
				t.Errorf("%s requests = %d, want 1", name, len(*bodies))
			}
		}
		calls := 0
		client, _ := telegramClient(t, "bot", roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, &timeoutError{}
		}))
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
			t.Errorf("%s timeout err = %v calls = %d", name, err, calls)
		}
	}
}

func TestProfileToolDescriptors(t *testing.T) {
	if profileSetPhoto.LocalFiles != config.LocalFilesRead || profileSetPhoto.Risk.Idempotency != capability.IdempotencyUnknown ||
		profileSetPhoto.Risk.Effect != capability.EffectUpdate || profileSetPhoto.RequiresToolAllowList ||
		profileRemovePhoto.Risk.Effect != capability.EffectDelete || !profileRemovePhoto.RequiresToolAllowList {
		t.Error("profile tool risk, local files, or allow list wrong")
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if strings.HasPrefix(tool, "telegram.profile.") || strings.HasPrefix(tool, "telegram.default-admin-rights.") {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
}
