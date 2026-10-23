package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// documentedUser carries every member of the Bot API type User.
const documentedUser = `{"id":7,"is_bot":false,"first_name":"Ann","last_name":"Lee","username":"ann",` +
	`"language_code":"de","is_premium":true,"added_to_attachment_menu":true,"can_join_groups":true,` +
	`"can_read_all_group_messages":false,"supports_guest_queries":true,"supports_inline_queries":true,` +
	`"can_connect_to_business":true,"has_main_web_app":true,"has_topics_enabled":true,` +
	`"allows_users_to_create_topics":false,"can_manage_bots":true,"supports_join_request_queries":true}`

const memberAnswer = `{"status":"member","user":{"id":7}}`

// outputCase runs one tool against canned Bot API answers. Answers map a Bot API method to its raw result;
// USER stands for documentedUser. Later tools join outputCases with a sample response of their own, so
// TestEveryOutputMemberIsInSchemaAndFields covers their output members as well. userAt names the
// output User, if the tool prints one, as a path of keys; "[]" steps into the first list element.
type outputCase struct {
	tool    string
	targets []string
	args    string
	answers map[string]string
	userAt  []string
}

func outputCases() []outputCase {
	gift := `{"total_count":1,"gifts":[{"type":"regular","send_date":5,"is_saved":true,"sender_user":USER,` +
		`"gift":{"id":"g1","star_count":50}}]}`
	return []outputCase{
		{"telegram.bot.get", []string{"-1001"}, `{}`, map[string]string{"getMe": `USER`}, []string{}},
		{"telegram.webhook.get", []string{"bot"}, `{}`, map[string]string{"getWebhookInfo": `{"url":"https://hook.example/s3cret",` +
			`"has_custom_certificate":true,"pending_update_count":2,"ip_address":"203.0.113.9","last_error_date":10,` +
			`"last_error_message":"timeout","last_synchronization_error_date":11,"max_connections":40,` +
			`"allowed_updates":["message","edited_message"]}`}, nil},
		{"telegram.commands.get", []string{"bot"}, `{}`, map[string]string{"getMyCommands": `[{"command":"help",` +
			`"description":"Help","is_ephemeral":true}]`}, nil},
		{"telegram.menu-button.get", []string{"bot"}, `{}`, map[string]string{"getChatMenuButton": `{"type":"web_app",` +
			`"text":"Open","web_app":{"url":"https://app.example/x"}}`}, nil},
		{"telegram.chats.get", []string{"-1001"}, `{}`,
			map[string]string{"getChat": fullChatInfo}, []string{"guard_bot"}},
		{"telegram.chats.administrators", []string{"-1001"}, `{}`,
			map[string]string{"getChatAdministrators": `[{"status":"creator","user":USER}]`},
			[]string{"administrators", "[]", "user"}},
		{"telegram.chats.member", []string{"-1001"}, `{"user_id":7}`,
			map[string]string{"getChatMember": `{"status":"member","tag":"vip","user":USER}`}, []string{"user"}},
		{"telegram.updates.list", []string{"-1001"}, `{}`,
			map[string]string{"getUpdates": `[{"update_id":1,"message":{"message_id":2,"date":3,` +
				`"chat":{"id":-1001},"from":USER,"text":"hi"}}]`}, []string{"updates", "[]", "from"}},
		{"telegram.updates.list", []string{"-1001"}, `{}`,
			map[string]string{"getUpdates": `[{"update_id":1,"message":{"message_id":2,"date":3,` +
				`"chat":{"id":-1001},` + documentedMedia + `}}]`}, nil},
		{"telegram.users.personal-chat-messages", []string{"-1001"}, `{"user_id":7}`,
			map[string]string{"getChatMember": memberAnswer,
				"getUserPersonalChatMessages": `[{"message_id":1,"date":2,` + documentedMedia + `}]`}, nil},
		{"telegram.users.personal-chat-messages", []string{"-1001"}, `{"user_id":7}`,
			map[string]string{"getChatMember": memberAnswer,
				"getUserPersonalChatMessages": `[{"message_id":1,"date":2,"text":"hi"}]`}, nil},
		{"telegram.users.profile-photos", []string{"-1001"}, `{"user_id":7}`,
			map[string]string{"getChatMember": memberAnswer, "getUserProfilePhotos": documentedProfilePhotos}, nil},
		{"telegram.users.profile-audios", []string{"-1001"}, `{"user_id":7}`,
			map[string]string{"getChatMember": memberAnswer, "getUserProfileAudios": documentedProfileAudios}, nil},
		{"telegram.users.chat-boosts", []string{"-1001"}, `{"user_id":7}`,
			map[string]string{"getChatMember": memberAnswer, "getUserChatBoosts": documentedBoosts},
			[]string{"boosts", "[]", "source_info", "user"}},
		{"telegram.games.high-scores", []string{"-1001"}, `{"message_id":4,"user_id":7}`,
			map[string]string{"getGameHighScores": `[{"position":1,"score":9,"user":USER}]`},
			[]string{"scores", "[]", "user"}},
		{"telegram.business.get", []string{"business/b1"}, `{"business":"b1"}`,
			map[string]string{"getBusinessConnection": `{"id":"b1","user":USER,"user_chat_id":777,"date":5,` +
				`"is_enabled":true,"rights":{"can_reply":true}}`}, []string{"user"}},
		{"telegram.business.gifts", []string{"business/b1"}, `{"business":"b1"}`,
			map[string]string{"getBusinessAccountGifts": gift}, []string{"gifts", "[]", "sender"}},
		{"telegram.gifts.chat", []string{"-1001"}, `{}`,
			map[string]string{"getChatGifts": gift}, []string{"gifts", "[]", "sender"}},
		{"telegram.sticker-sets.get", []string{"bot"}, `{"name":"pack_by_bot"}`,
			map[string]string{"getStickerSet": fullStickerSet}, nil},
		{"telegram.stickers.custom-emoji", []string{"bot"}, `{"custom_emoji_ids":["9"]}`,
			map[string]string{"getCustomEmojiStickers": `[` + fullSticker + `]`}, nil},
		{"telegram.topics.icon-stickers", []string{"bot"}, `{}`,
			map[string]string{"getForumTopicIconStickers": `[` + fullSticker + `]`}, nil},
		{"telegram.gifts.user", []string{"-1001"}, `{"user_id":7}`,
			map[string]string{"getChatMember": memberAnswer, "getUserGifts": gift},
			[]string{"gifts", "[]", "sender"}},
	}
}

// invokeOutputCase runs the registered handler of the tool and returns its output as a generic object.
func invokeOutputCase(t *testing.T, c outputCase) (capability.Descriptor, map[string]any) {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	descriptor, handler, ok := reg.Lookup(c.tool)
	if !ok {
		t.Fatalf("%s is not registered", c.tool)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.ReadAll(r.Body)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		answer, ok := c.answers[method]
		if !ok {
			t.Errorf("%s: unexpected request %s", c.tool, method)
			return response(404, `{"ok":false}`), nil
		}
		return response(200, `{"ok":true,"result":`+strings.ReplaceAll(answer, "USER", documentedUser)+`}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(name string) string {
		if name == "TEST_TELEGRAM_BOT_TOKEN" {
			return testToken
		}
		return ""
	}, nil, nil, red)
	value, err := handler(context.Background(), resolvedWith(c.targets...), secrets, red, json.RawMessage(c.args))
	if err != nil {
		t.Fatalf("%s = %v", c.tool, err)
	}
	encoded, _ := json.Marshal(value)
	if err := application.ValidateJSON(descriptor.OutputSchema, encoded); err != nil {
		t.Fatalf("%s violates its output schema: %v", c.tool, err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return descriptor, out
}

func TestEveryOutputMemberIsInSchemaAndFields(t *testing.T) {
	for _, c := range outputCases() {
		t.Run(c.tool, func(t *testing.T) {
			descriptor, out := invokeOutputCase(t, c)
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(descriptor.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			fields := map[string]bool{}
			for _, f := range descriptor.Fields {
				fields[f.Name] = true
			}
			var missing []string
			for key := range out {
				if _, ok := schema.Properties[key]; !ok {
					missing = append(missing, key+" (schema)")
				}
				if !fields[key] {
					missing = append(missing, key+" (fields)")
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("not declared: %v in %v", missing, out)
			}
		})
	}
}

func TestEveryToolPrintsEveryDocumentedUserField(t *testing.T) {
	var want map[string]any
	if err := json.Unmarshal([]byte(documentedUser), &want); err != nil {
		t.Fatal(err)
	}
	for _, c := range outputCases() {
		if c.userAt == nil {
			continue
		}
		t.Run(c.tool, func(t *testing.T) {
			_, out := invokeOutputCase(t, c)
			var at any = out
			for _, step := range c.userAt {
				if step == "[]" {
					list, _ := at.([]any)
					if len(list) == 0 {
						t.Fatalf("no list element in %v", out)
					}
					at = list[0]
					continue
				}
				object, _ := at.(map[string]any)
				at = object[step]
			}
			if !reflect.DeepEqual(at, want) {
				t.Errorf("user = %v\nwant %v", at, want)
			}
		})
	}
}

func TestUserMapperKeepsTheFormerOutputWithoutNewFields(t *testing.T) {
	var u tgUser
	if err := json.Unmarshal([]byte(`{"id":5,"is_bot":true,"first_name":"Al","username":"al","EXTRA":1}`), &u); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(sender(&u))
	if want := `{"id":5,"is_bot":true,"first_name":"Al","username":"al"}`; string(encoded) != want {
		t.Errorf("sender = %s, want %s", encoded, want)
	}
	if sender(nil) != nil || sender(&tgUser{}) != nil {
		t.Error("a missing user must stay missing")
	}
}

func TestUserMapperKeepsFalseFlagsAndBoundsTexts(t *testing.T) {
	long := strings.Repeat("x", maxNameRunes+50)
	var u tgUser
	raw := `{"id":5,"is_bot":true,"first_name":"` + long + `","language_code":"` + long +
		`","can_join_groups":false,"supports_inline_queries":true}`
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	got := sender(&u)
	if got.FirstName != strings.Repeat("x", maxNameRunes) || got.LanguageCode != strings.Repeat("x", maxNameRunes) {
		t.Error("texts are not bounded")
	}
	encoded, _ := json.Marshal(got)
	if !strings.Contains(string(encoded), `"can_join_groups":false`) || strings.Contains(string(encoded), "is_premium") {
		t.Errorf("flags = %s", encoded)
	}
}

func TestNewReferenceKindsAreSignedAndBound(t *testing.T) {
	resolved := resolvedWith("-1001", "bot")
	for kind, want := range map[refKind]byte{refGuestQuery: 7, refInlineMessage: 8} {
		if byte(kind) != want || !kind.valid() {
			t.Errorf("reference kind %d = %d, valid %v", want, kind, kind.valid())
		}
	}
	client, _ := multiClient(t, testToken, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(404, `{"ok":false}`), nil
	}), "-1001", "bot")
	for _, kind := range []refKind{refGuestQuery, refInlineMessage} {
		raw := signRef(testToken, kind, "-1001", "ID1")
		parsed, err := parseRef(resolved, kind, raw)
		if err != nil || parsed.id != "ID1" {
			t.Fatalf("parseRef(%d) = %+v, %v", kind, parsed, err)
		}
		if id, err := client.checkRef(parsed); err != nil || id != "ID1" {
			t.Errorf("checkRef = %q, %v", id, err)
		}
		other := refGuestQuery
		if kind == refGuestQuery {
			other = refInlineMessage
		}
		for name, tt := range map[string]struct {
			want refKind
			raw  string
		}{
			"other kind":      {other, raw},
			"foreign binding": {kind, signRef(testToken, kind, "-5", "ID1")},
			"malformed":       {kind, raw[:len(raw)-3]},
			"older kind":      {refFile, raw},
		} {
			if _, err := parseRef(resolved, tt.want, tt.raw); err == nil {
				t.Errorf("kind %d, %s: accepted", kind, name)
			}
		}
		foreign := signRef("999:other-token", kind, "-1001", "ID1")
		parsed, err = parseRef(resolved, kind, foreign)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.checkRef(parsed); err == nil {
			t.Errorf("kind %d: a reference of another token passed", kind)
		}
	}
	if (refInlineMessage + 1).valid() {
		t.Error("a kind after the last one is valid")
	}
}
