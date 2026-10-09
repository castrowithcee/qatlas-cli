package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var interactionInvokers = map[string]invokeFunc{
	"pollssend": invokePollsSend, "pollsstop": invokePollsStop, "reactionsset": invokeReactionsSet,
	"chatactionssend": invokeChatActionsSend,
}

const pollOK = `{"ok":true,"result":{"message_id":7,"date":1787220000,"poll":{"id":"555","question":"secret"}}}`

func TestInteractionBodiesAndPaths(t *testing.T) {
	yes := false
	client, bodies, paths := capture(t, "-1001", 200, pollOK)
	ctx := context.Background()
	got, err := client.SendPoll(ctx, PollOptions{Question: "Q?", Options: []string{"a", "b"}})
	if err != nil || got["message_id"] != int64(7) || got["poll_id"] != "555" || got["date"] != int64(1787220000) ||
		len(got) != 3 {
		t.Fatalf("SendPoll = %v, %v", got, err)
	}
	if _, err := client.SendPoll(ctx, PollOptions{Question: "Q?", Options: []string{"a", "b", "c"}, Type: "quiz",
		CorrectOptionIDs: []int{0, 2}, AllowsMultipleAnswers: true, Explanation: "why", IsAnonymous: &yes,
		OpenPeriod: 60, ReplyToMessageID: 3, MessageThreadID: 4, DisableNotification: true, ProtectContent: true,
	}); err != nil {
		t.Fatal(err)
	}
	if (*bodies)[0] != `{"chat_id":"-1001","question":"Q?","options":[{"text":"a"},{"text":"b"}]}` ||
		(*paths)[0] != "sendPoll" {
		t.Errorf("poll 1 = %s %s", (*paths)[0], (*bodies)[0])
	}
	want := `{"chat_id":"-1001","message_thread_id":4,"question":"Q?","options":[{"text":"a"},{"text":"b"},` +
		`{"text":"c"}],"is_anonymous":false,"type":"quiz","allows_multiple_answers":true,` +
		`"correct_option_ids":[0,2],"explanation":"why","open_period":60,"disable_notification":true,` +
		`"protect_content":true,"reply_parameters":{"message_id":3}}`
	if (*bodies)[1] != want {
		t.Errorf("poll 2 = %s", (*bodies)[1])
	}
	if _, err := client.SendPoll(ctx, PollOptions{Question: "Q", Options: []string{"a"}, CloseDate: 99}); err != nil ||
		!strings.Contains((*bodies)[2], `"close_date":99`) {
		t.Errorf("close_date body = %v, %v", (*bodies)[2], err)
	}

	client, bodies, paths = capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	calls := []struct {
		run        func() (map[string]any, error)
		path, body string
		key        string
	}{
		{func() (map[string]any, error) {
			return client.SetReaction(ctx, 5, []reactionArgument{{Emoji: "👍"}}, true)
		}, "setMessageReaction", `{"chat_id":"-1001","message_id":5,"reaction":[{"type":"emoji","emoji":"👍"}],"is_big":true}`, "set"},
		{func() (map[string]any, error) {
			return client.SetReaction(ctx, 5, []reactionArgument{{CustomEmojiID: "5368324170671202286"}}, false)
		}, "setMessageReaction",
			`{"chat_id":"-1001","message_id":5,"reaction":[{"type":"custom_emoji","custom_emoji_id":"5368324170671202286"}]}`, "set"},
		{func() (map[string]any, error) { return client.SetReaction(ctx, 5, nil, false) },
			"setMessageReaction", `{"chat_id":"-1001","message_id":5,"reaction":[]}`, "set"},
		{func() (map[string]any, error) { return client.SendChatAction(ctx, "typing", 0) },
			"sendChatAction", `{"chat_id":"-1001","action":"typing"}`, "sent"},
		{func() (map[string]any, error) { return client.SendChatAction(ctx, "upload_video_note", 9) },
			"sendChatAction", `{"chat_id":"-1001","message_thread_id":9,"action":"upload_video_note"}`, "sent"},
	}
	for i, c := range calls {
		got, err := c.run()
		if err != nil || got[c.key] != true || len(got) != 1 || (*bodies)[i] != c.body || (*paths)[i] != c.path {
			t.Errorf("call %d = %v, %v, %s %s", i, got, err, (*paths)[i], (*bodies)[i])
		}
	}

	client, bodies, paths = capture(t, "-1001", 200, `{"ok":true,"result":{"id":"1","is_closed":true,"question":"x"}}`)
	got, err = client.StopPoll(ctx, 7)
	if err != nil || got["stopped"] != true || len(got) != 1 || (*paths)[0] != "stopPoll" ||
		(*bodies)[0] != `{"chat_id":"-1001","message_id":7}` {
		t.Errorf("StopPoll = %v, %v, %s %s", got, err, (*paths)[0], (*bodies)[0])
	}
	client, _, _ = capture(t, "-1001", 200, `{"ok":true,"result":{"is_closed":false}}`)
	if _, err := client.StopPoll(ctx, 7); err == nil || !strings.Contains(err.Error(), "may have taken effect") {
		t.Errorf("open poll err = %v", err)
	}
}

func TestInteractionValuesRejectedBeforeIO(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	opts := func(n int) string {
		return `["` + strings.TrimSuffix(strings.Repeat(`a","`, n), `,"`) + `]`
	}
	cases := map[string][]string{
		"pollssend": {
			`{"question":"","options":["a"]}`, `{"question":"` + long(301) + `","options":["a"]}`,
			`{"question":"q","options":[]}`, `{"question":"q","options":` + opts(13) + `}`,
			`{"question":"q","options":["` + long(101) + `"]}`, `{"question":"q","options":[""]}`,
			`{"question":"q","options":["a"],"type":"poll"}`,
			`{"question":"q","options":["a","b"],"type":"quiz"}`,
			`{"question":"q","options":["a","b"],"type":"quiz","correct_option_ids":[2]}`,
			`{"question":"q","options":["a","b"],"type":"quiz","correct_option_ids":[1,0]}`,
			`{"question":"q","options":["a","b"],"type":"quiz","correct_option_ids":[0,0]}`,
			`{"question":"q","options":["a","b"],"correct_option_ids":[0]}`,
			`{"question":"q","options":["a","b"],"explanation":"e"}`,
			`{"question":"q","options":["a","b"],"type":"quiz","correct_option_ids":[0],"explanation":"` + long(201) + `"}`,
			`{"question":"q","options":["a","b"],"type":"quiz","correct_option_ids":[0],"explanation":"a\nb\nc\nd"}`,
			`{"question":"q","options":["a"],"open_period":4}`, `{"question":"q","options":["a"],"open_period":2628001}`,
			`{"question":"q","options":["a"],"open_period":60,"close_date":1}`,
			`{"question":"q","options":["a"],"close_date":-1}`, `{"question":"q","options":["a"],"extra":1}`,
			`{"question":"q","options":["a"],"reply_to_message_id":-1}`,
		},
		"pollsstop": {`{}`, `{"message_id":0}`, `{"message_id":1,"extra":1}`},
		"reactionsset": {
			`{"message_id":1}`, `{"message_id":0,"reactions":[]}`, `{"message_id":1,"reactions":[{"emoji":"x"}]}`,
			`{"message_id":1,"reactions":[{"emoji":"👍"},{"emoji":"❤"}]}`,
			`{"message_id":1,"reactions":[{"emoji":"👍","custom_emoji_id":"1"}]}`,
			`{"message_id":1,"reactions":[{}]}`, `{"message_id":1,"reactions":[{"custom_emoji_id":"12a"}]}`,
			`{"message_id":1,"reactions":[{"custom_emoji_id":"123456789012345678901"}]}`,
			`{"message_id":1,"reactions":[{"type":"paid"}]}`, `{"message_id":1,"reactions":[{"emoji":"⭐"}]}`,
		},
		"chatactionssend": {`{}`, `{"action":"dance"}`, `{"action":"typing","message_thread_id":-1}`,
			`{"action":"Typing"}`},
	}
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	for name, list := range cases {
		for _, args := range list {
			if _, err := interactionInvokers[name](context.Background(), resolvedWith("-1001"), resolver, nil,
				json.RawMessage(args)); err == nil {
				t.Errorf("%s accepted %s", name, args)
			}
			if resolutions != 0 {
				t.Fatalf("%s resolved the secret for %s", name, args)
			}
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestInteractionsRejectUnboundChatBeforeSecret(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	args := map[string]string{
		"pollssend": `{"chat":"-2002","question":"q","options":["a"]}`, "pollsstop": `{"chat":"-2002","message_id":1}`,
		"reactionsset":    `{"chat":"-2002","message_id":1,"reactions":[]}`,
		"chatactionssend": `{"chat":"-2002","action":"typing"}`,
	}
	for name, invoke := range interactionInvokers {
		_, err := invoke(context.Background(), resolvedWith("-1001"), resolver, nil, json.RawMessage(args[name]))
		if err == nil || strings.Contains(err.Error(), "-2002") {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestInteractionsSingleRequestAndUncertainty(t *testing.T) {
	ops := map[string]func(*Client) error{
		"send": func(c *Client) error {
			_, err := c.SendPoll(context.Background(), PollOptions{Question: "q", Options: []string{"a"}})
			return err
		},
		"stop":     func(c *Client) error { _, err := c.StopPoll(context.Background(), 5); return err },
		"reaction": func(c *Client) error { _, err := c.SetReaction(context.Background(), 5, nil, false); return err },
		"action":   func(c *Client) error { _, err := c.SendChatAction(context.Background(), "typing", 0); return err },
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", name, status, err)
			}
			if len(*bodies) != 1 {
				t.Errorf("%s requests = %d, want 1", name, len(*bodies))
			}
		}
		calls := 0
		client, _ := telegramClient(t, "-1001", roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, &timeoutError{}
		}))
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") || calls != 1 {
			t.Errorf("%s timeout err = %v calls = %d", name, err, calls)
		}
		client, _, _ = capture(t, "-1001", 200, `{"ok":true,"result":"garbage"}`)
		if err := op(client); err == nil || !strings.Contains(err.Error(), "may have taken effect") {
			t.Errorf("%s invalid result err = %v", name, err)
		}
	}
}

func TestInteractionsRiskGroupAndAllowList(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if strings.HasPrefix(tool, "telegram.polls.") || strings.HasPrefix(tool, "telegram.reactions.") ||
				strings.HasPrefix(tool, "telegram.chatactions.") {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
	risks := map[string][2]string{
		pollsSend.ID:       {string(capability.EffectCreate), string(capability.IdempotencyNonIdempotent)},
		pollsStop.ID:       {string(capability.EffectUpdate), string(capability.IdempotencyIdempotent)},
		reactionsSet.ID:    {string(capability.EffectUpdate), string(capability.IdempotencyIdempotent)},
		chatActionsSend.ID: {string(capability.EffectCreate), string(capability.IdempotencyIdempotent)},
	}
	for _, d := range []capability.Descriptor{pollsSend, pollsStop, reactionsSet, chatActionsSend} {
		r := d.Risk
		if d.Group != groupInteractions || string(r.Effect) != risks[d.ID][0] || string(r.Idempotency) != risks[d.ID][1] ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != dataSensitivity ||
			d.RequiresToolAllowList != (d.ID == pollsStop.ID) {
			t.Errorf("descriptor %s = %+v", d.ID, d)
		}
	}
}

func TestReactionEmojiListSize(t *testing.T) {
	if len(reactionEmoji) != 73 {
		t.Errorf("reaction emoji = %d, want 73", len(reactionEmoji))
	}
}
