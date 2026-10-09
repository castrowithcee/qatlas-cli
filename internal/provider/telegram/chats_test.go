package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const inviteCanary = "https://t.me/+INVITECANARY"

func chatsFake(calls *[]recorded, status int, result string) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		*calls = append(*calls, recorded{r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], string(data)})
		return response(status, result), nil
	}
}

func TestChatsGetReturnsOnlyAllowListedFields(t *testing.T) {
	var calls []recorded
	body := `{"ok":true,"result":{"id":-1001,"type":"supergroup","title":"Team","username":"team",` +
		`"is_forum":true,"description":"About","invite_link":"` + inviteCanary + `",` +
		`"permissions":{"can_send_messages":true,"can_pin_messages":false,"EXTRA":true},` +
		`"slow_mode_delay":10,"linked_chat_id":-1002,` +
		`"pinned_message":{"message_id":55,"text":"PINNEDCANARY"},"photo":{"x":1},"EXTRA":"LEAK"}}`
	client, _ := multiClient(t, testToken, chatsFake(&calls, 200, body), "-1001")
	got, err := client.GetChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].method != "getChat" || calls[0].body != `{"chat_id":"-1001"}` {
		t.Fatalf("calls = %+v", calls)
	}
	encoded, _ := json.Marshal(got)
	want := `{"id":-1001,"type":"supergroup","title":"Team","username":"team","is_forum":true,` +
		`"description":"About","permissions":{"can_send_messages":true,"can_pin_messages":false},` +
		`"slow_mode_delay":10,"pinned_message_id":55,"linked_chat_id":-1002}`
	if string(encoded) != want {
		t.Errorf("output = %s", encoded)
	}
}

func TestChatsGetCapsStrings(t *testing.T) {
	var calls []recorded
	body := `{"ok":true,"result":{"id":1,"type":"group","title":"` + strings.Repeat("t", 1000) +
		`","description":"` + strings.Repeat("d", 9000) + `"}}`
	client, _ := multiClient(t, testToken, chatsFake(&calls, 200, body), "1")
	got, err := client.GetChat(context.Background())
	if err != nil || len(got.Title) != maxNameRunes || len(got.Description) != maxMessageLength {
		t.Fatalf("err = %v, title %d, description %d", err, len(got.Title), len(got.Description))
	}
}

func TestChatsMemberAllowListAndBody(t *testing.T) {
	var calls []recorded
	body := `{"ok":true,"result":{"status":"administrator","user":{"id":7,"is_bot":false,"first_name":"Ann",` +
		`"last_name":"Lee","username":"ann","language_code":"de","is_premium":true},` +
		`"can_be_edited":false,"is_anonymous":false,"can_delete_messages":true,"custom_title":"` +
		strings.Repeat("c", 400) + `","until_date":0,"invite_link":"` + inviteCanary + `","EXTRA":1}}`
	client, _ := multiClient(t, testToken, chatsFake(&calls, 200, body), "-1001")
	got, err := client.GetChatMember(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].method != "getChatMember" || calls[0].body != `{"chat_id":"-1001","user_id":7}` {
		t.Fatalf("calls = %+v", calls)
	}
	encoded, _ := json.Marshal(got)
	want := `{"user":{"id":7,"is_bot":false,"first_name":"Ann","last_name":"Lee","username":"ann"},` +
		`"status":"administrator","custom_title":"` + strings.Repeat("c", maxNameRunes) + `","is_anonymous":false,` +
		`"can_be_edited":false,"can_delete_messages":true}`
	if string(encoded) != want {
		t.Errorf("output = %s", encoded)
	}
	if strings.Contains(string(encoded), "INVITECANARY") || strings.Contains(string(encoded), "language_code") {
		t.Error("a forbidden field leaked")
	}
}

func TestChatsAdministratorsCapsList(t *testing.T) {
	var calls []recorded
	var members []string
	for i := 1; i <= maxAdministrators+5; i++ {
		members = append(members, `{"status":"administrator","user":{"id":`+strconv.Itoa(i)+`,"is_bot":false}}`)
	}
	body := `{"ok":true,"result":[` + strings.Join(members, ",") + `]}`
	client, _ := multiClient(t, testToken, chatsFake(&calls, 200, body), "-1001")
	got, err := client.GetChatAdministrators(context.Background())
	if err != nil || len(got.Administrators) != maxAdministrators || !got.Truncated {
		t.Fatalf("err = %v, count %d, truncated %v", err, len(got.Administrators), got.Truncated)
	}
	if calls[0].method != "getChatAdministrators" || calls[0].body != `{"chat_id":"-1001"}` {
		t.Fatalf("calls = %+v", calls)
	}
	calls = nil
	client, _ = multiClient(t, testToken, chatsFake(&calls, 200, `{"ok":true,"result":[]}`), "-1001")
	got, _ = client.GetChatAdministrators(context.Background())
	if encoded, _ := json.Marshal(got); string(encoded) != `{"administrators":[]}` {
		t.Errorf("output = %s", encoded)
	}
}

func TestChatsMemberCount(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, chatsFake(&calls, 200, `{"ok":true,"result":123}`), "-1001")
	got, err := client.GetChatMemberCount(context.Background())
	if err != nil || got.Count != 123 || calls[0].method != "getChatMemberCount" ||
		calls[0].body != `{"chat_id":"-1001"}` {
		t.Fatalf("got %+v, err %v, calls %+v", got, err, calls)
	}
}

func TestChatsToolsRefuseUnboundChatBeforeSecretAccess(t *testing.T) {
	accessed := false
	resolver := secret.NewWith(func(string) string { accessed = true; return testToken }, nil, nil, &redact.Redactor{})
	invokes := map[string]func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){
		"get": invokeChatsGet, "administrators": invokeChatsAdministrators,
		"membercount": invokeChatsMemberCount, "member": invokeChatsMember,
	}
	for name, invoke := range invokes {
		args := `{"chat":"-9999","user_id":5}`
		if name != "member" {
			args = `{"chat":"-9999"}`
		}
		_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, &redact.Redactor{}, json.RawMessage(args))
		if err == nil || accessed || strings.Contains(err.Error(), "-9999") {
			t.Errorf("%s: err = %v, accessed = %v", name, err, accessed)
		}
	}
}

func TestChatsMemberRejectsInvalidUserIDBeforeIO(t *testing.T) {
	accessed := false
	resolver := secret.NewWith(func(string) string { accessed = true; return testToken }, nil, nil, &redact.Redactor{})
	for _, args := range []string{`{"user_id":0}`, `{"user_id":-3}`, `{}`, `{"user_id":1,"x":2}`} {
		_, err := invokeChatsMember(context.Background(), resolvedWith("-1001"), resolver, &redact.Redactor{},
			json.RawMessage(args))
		if err == nil || accessed {
			t.Errorf("%s: err = %v, accessed = %v", args, err, accessed)
		}
	}
	client, _ := multiClient(t, testToken, chatsFake(new([]recorded), 200, `{}`), "-1001")
	if _, err := client.GetChatMember(context.Background(), 0); err == nil {
		t.Error("user 0 accepted")
	}
}

func TestChatsToolsErrorsCarryNoTelegramText(t *testing.T) {
	body := `{"ok":false,"error_code":400,"description":"LEAKEDDESCRIPTION"}`
	for _, status := range []int{400, 403, 500} {
		var calls []recorded
		client, _ := multiClient(t, testToken, chatsFake(&calls, status, body), "-1001")
		_, e1 := client.GetChat(context.Background())
		_, e2 := client.GetChatAdministrators(context.Background())
		_, e3 := client.GetChatMemberCount(context.Background())
		_, e4 := client.GetChatMember(context.Background(), 5)
		for _, err := range []error{e1, e2, e3, e4} {
			if err == nil || strings.Contains(err.Error(), "LEAKEDDESCRIPTION") {
				t.Errorf("status %d error = %v", status, err)
			}
		}
	}
}

func TestReadProfileContainsChatTools(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if profile.ID != "read" {
			if profile.Recommended && profile.ID != "send" {
				t.Errorf("unexpected recommended profile %s", profile.ID)
			}
			continue
		}
		if profile.Recommended {
			t.Error("profile read must not be recommended")
		}
		have := strings.Join(profile.Tools, " ")
		for _, want := range []string{chatsGet.ID, chatsAdministrators.ID, chatsMemberCount.ID, chatsMember.ID} {
			if !strings.Contains(have+" ", want+" ") {
				t.Errorf("profile read misses %s", want)
			}
		}
		return
	}
	t.Error("profile read is missing")
}

// schemaKeys fails for every serialized key that the output schema does not declare, recursively.
func schemaKeys(t *testing.T, path string, value any, schema map[string]any) {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, child := range object {
		declared, ok := properties[key].(map[string]any)
		if !ok {
			t.Errorf("%s.%s is missing in the output schema", path, key)
			continue
		}
		schemaKeys(t, path+"."+key, child, declared)
	}
}

func TestOutputsMatchTheirSchemas(t *testing.T) {
	yes := true
	member := memberOut{User: memberUserOut{ID: 1, IsBot: true, FirstName: "a", LastName: "b", Username: "c"},
		Status: "s", CustomTitle: "t", UntilDate: 1, IsAnonymous: &yes, IsMember: &yes,
		CanBeEdited: &yes, CanManageChat: &yes, CanDeleteMessages: &yes, CanManageVideoChats: &yes,
		CanRestrictMembers: &yes, CanPromoteMembers: &yes, CanChangeInfo: &yes, CanInviteUsers: &yes,
		CanPostStories: &yes, CanEditStories: &yes, CanDeleteStories: &yes, CanPostMessages: &yes,
		CanEditMessages: &yes, CanPinMessages: &yes, CanManageTopics: &yes, CanSendMessages: &yes,
		CanSendAudios: &yes, CanSendDocuments: &yes, CanSendPhotos: &yes, CanSendVideos: &yes,
		CanSendVideoNotes: &yes, CanSendVoiceNotes: &yes, CanSendPolls: &yes, CanSendOtherMessages: &yes,
		CanAddWebPagePreviews: &yes}
	chat := chatOut{ID: 1, Type: "t", Title: "t", Username: "u", IsForum: true, Description: "d",
		Permissions: &permissionsOut{CanSendMessages: &yes, CanSendAudios: &yes, CanSendDocuments: &yes,
			CanSendPhotos: &yes, CanSendVideos: &yes, CanSendVideoNotes: &yes, CanSendVoiceNotes: &yes,
			CanSendPolls: &yes, CanSendOtherMessages: &yes, CanAddWebPagePreviews: &yes, CanChangeInfo: &yes,
			CanInviteUsers: &yes, CanPinMessages: &yes, CanManageTopics: &yes},
		SlowModeDelay: 1, PinnedMessageID: 1, LinkedChatID: 1}
	for name, tt := range map[string]struct {
		value  any
		schema json.RawMessage
	}{
		"member": {member, chatsMember.OutputSchema},
		"administrators": {administratorsOut{Administrators: []memberOut{member}, Truncated: true},
			chatsAdministrators.OutputSchema},
		"chat":  {chat, chatsGet.OutputSchema},
		"count": {countOut{Count: 1}, chatsMemberCount.OutputSchema},
	} {
		encoded, _ := json.Marshal(tt.value)
		var value, schema map[string]any
		if json.Unmarshal(encoded, &value) != nil || json.Unmarshal(tt.schema, &schema) != nil {
			t.Fatalf("%s: undecodable", name)
		}
		if name == "administrators" {
			items := schema["properties"].(map[string]any)["administrators"].(map[string]any)["items"]
			list := value["administrators"].([]any)
			schemaKeys(t, name, list[0], items.(map[string]any))
			delete(value, "administrators")
			value["truncated"] = true
		}
		schemaKeys(t, name, value, schema)
	}
}
