package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestUserReadsRequireVerifiedMembership(t *testing.T) {
	methods := []struct {
		name string
		read func(*Client) (any, error)
	}{
		{"photos", func(c *Client) (any, error) { return c.ProfilePhotos(context.Background(), 7, 0, 20) }},
		{"audios", func(c *Client) (any, error) { return c.ProfileAudios(context.Background(), 7, 0, 20) }},
		{"boosts", func(c *Client) (any, error) { return c.ChatBoosts(context.Background(), 7, 0, 20) }},
		{"messages", func(c *Client) (any, error) { return c.PersonalChatMessages(context.Background(), 7, 0, 20) }},
	}
	statuses := []struct {
		name, member string
		allowed      bool
	}{
		{"creator", `{"status":"creator","user":{"id":7}}`, true},
		{"administrator", `{"status":"administrator","user":{"id":7}}`, true},
		{"member", `{"status":"member","user":{"id":7}}`, true},
		{"restricted member", `{"status":"restricted","is_member":true,"user":{"id":7}}`, true},
		{"restricted outsider", `{"status":"restricted","is_member":false,"user":{"id":7}}`, false},
		{"left", `{"status":"left","user":{"id":7}}`, false},
		{"kicked", `{"status":"kicked","user":{"id":7}}`, false},
		{"wrong user", `{"status":"member","user":{"id":8}}`, false},
		{"unknown", `{"status":"unknown","user":{"id":7}}`, false},
	}
	for _, method := range methods {
		for _, status := range statuses {
			t.Run(method.name+"/"+status.name, func(t *testing.T) {
				var calls []recorded
				transport := func(r *http.Request) (*http.Response, error) {
					data, _ := io.ReadAll(r.Body)
					name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					calls = append(calls, recorded{name, string(data)})
					if name == "getChatMember" {
						return response(200, `{"ok":true,"result":`+status.member+`}`), nil
					}
					return response(200, `{"ok":true,"result":[]}`), nil
				}
				client, _ := telegramClient(t, "-1001", roundTripFunc(transport))
				_, err := method.read(client)
				if len(calls) == 0 || calls[0].method != "getChatMember" || calls[0].body != `{"chat_id":"-1001","user_id":7}` {
					t.Fatalf("membership call = %+v", calls)
				}
				if !status.allowed {
					if err == nil || len(calls) != 1 {
						t.Fatalf("err = %v, calls = %+v", err, calls)
					}
				} else if len(calls) != 2 {
					t.Fatalf("calls = %+v", calls)
				}
			})
		}
	}
}

func TestUserReadDescriptors(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	ids := []string{usersProfilePhotos.ID, usersProfileAudios.ID, usersChatBoosts.ID, usersPersonalChatMessages.ID}
	for _, id := range ids {
		d, _, ok := reg.Lookup(id)
		if !ok || d.Group != groupUsers || d.Risk.Effect != capability.EffectRead ||
			d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone ||
			d.Risk.DataSensitivity != memberDataSensitivity || d.RequiresToolAllowList {
			t.Errorf("%s: descriptor = %+v, found = %v", id, d, ok)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			for _, id := range ids {
				if tool == id {
					t.Errorf("profile %s contains %s", profile.ID, id)
				}
			}
		}
	}
}

func TestUserReadErrorsOmitTelegramDescription(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getChatMember") {
			return response(200, `{"ok":true,"result":{"status":"member","user":{"id":7}}}`), nil
		}
		return response(400, `{"ok":false,"error_code":400,"description":"SECRET-DESC"}`), nil
	})
	client, _ := telegramClient(t, "-1001", transport)
	_, err := client.ChatBoosts(context.Background(), 7, 0, 20)
	if err == nil || strings.Contains(err.Error(), "SECRET-DESC") {
		t.Fatalf("err = %v", err)
	}
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(400, `{"ok":false,"description":"SECRET-DESC"}`), nil
	})
	client, _ = telegramClient(t, "-1001", transport)
	_, err = client.ProfilePhotos(context.Background(), 7, 0, 20)
	if err == nil || strings.Contains(err.Error(), "SECRET-DESC") {
		t.Fatalf("membership err = %v", err)
	}
}

func TestUserReadsRejectForeignChatBeforeSecretAccess(t *testing.T) {
	accessed := false
	resolver := secret.NewWith(func(string) string { accessed = true; return testToken }, nil, nil, &redact.Redactor{})
	invokes := []func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){
		invokeUsersProfilePhotos, invokeUsersProfileAudios, invokeUsersChatBoosts, invokeUsersPersonalChatMessages,
	}
	for _, invoke := range invokes {
		_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, &redact.Redactor{},
			json.RawMessage(`{"chat":"-9999","user_id":7}`))
		if err == nil || accessed || strings.Contains(err.Error(), "-9999") {
			t.Fatalf("foreign target: err = %v, accessed = %v", err, accessed)
		}
		_, err = invoke(context.Background(), resolvedWith("-1001"), resolver, &redact.Redactor{},
			json.RawMessage(`{"user_id":0}`))
		if err == nil || accessed {
			t.Fatalf("invalid user: err = %v, accessed = %v", err, accessed)
		}
	}
}

func TestUserReadsMethodBodyAndAllowList(t *testing.T) {
	tests := []struct {
		name, method, body, result string
		read                       func(*Client) (any, error)
		want, absent               string
	}{
		{"photos", "getUserProfilePhotos", `{"user_id":7,"offset":2,"limit":3}`,
			`{"total_count":1,"photos":[[{"file_id":"small","width":20},{"file_id":"big","width":40,"height":50,"file_size":90}]],"secret":"HIDE"}`,
			func(c *Client) (any, error) { return c.ProfilePhotos(context.Background(), 7, 2, 3) }, "photos", "HIDE"},
		{"audios", "getUserProfileAudios", `{"user_id":7,"offset":2,"limit":3}`,
			`{"total_count":1,"audios":[{"file_id":"audio","duration":4,"title":"Track","performer":"Artist","file_name":"x.mp3"}],"secret":"HIDE"}`,
			func(c *Client) (any, error) { return c.ProfileAudios(context.Background(), 7, 2, 3) }, "audios", "HIDE"},
		{"boosts", "getUserChatBoosts", `{"chat_id":"-1001","user_id":7}`,
			`{"boosts":[{"boost_id":"b","add_date":1,"expiration_date":2,"source":{"source":"premium","secret":"HIDE"}}],"secret":"HIDE"}`,
			func(c *Client) (any, error) { return c.ChatBoosts(context.Background(), 7, 0, 20) }, "boosts", "HIDE"},
		{"messages", "getUserPersonalChatMessages", `{"user_id":7,"limit":3}`,
			`[{"message_id":9,"date":10,"text":"Hello","chat":{"title":"HIDE"},"from":{"first_name":"HIDE"}},` +
				`{"message_id":10,"date":11,"photo":[{"file_id":"HIDE"}]}]`,
			func(c *Client) (any, error) { return c.PersonalChatMessages(context.Background(), 7, 0, 3) }, "Hello", "HIDE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []recorded
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				data, _ := io.ReadAll(r.Body)
				name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				calls = append(calls, recorded{name, string(data)})
				if name == "getChatMember" {
					return response(200, `{"ok":true,"result":{"status":"member","user":{"id":7}}}`), nil
				}
				return response(200, `{"ok":true,"result":`+tc.result+`}`), nil
			})
			client, _ := telegramClient(t, "-1001", transport)
			got, err := tc.read(client)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || calls[1].method != tc.method || calls[1].body != tc.body {
				t.Fatalf("calls = %+v", calls)
			}
			encoded, _ := json.Marshal(got)
			if !strings.Contains(string(encoded), tc.want) || strings.Contains(string(encoded), tc.absent) {
				t.Fatalf("output = %s", encoded)
			}
			if tc.name == "photos" || tc.name == "audios" {
				ref := signRef(testToken, refFile, "-1001", map[string]string{"photos": "big", "audios": "audio"}[tc.name])
				if !strings.Contains(string(encoded), ref) || strings.Contains(string(encoded), `"file_id"`) {
					t.Fatalf("file reference = %s", encoded)
				}
			}
			if tc.name == "messages" && (!strings.Contains(string(encoded), `"media_kind":"photo"`) ||
				!strings.Contains(string(encoded), signRef(testToken, refFile, "-1001", "HIDE"))) {
				t.Fatalf("media message has no signed file reference: %s", encoded)
			}
		})
	}
}
