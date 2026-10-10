package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestTopicMethodSelectionAndBodies(t *testing.T) {
	ctx := context.Background()
	client, bodies, paths := capture(t, "-1001", 200, `{"ok":true,"result":true}`)
	name, emoji, empty := "Neu", "5312", ""
	calls := []struct {
		run        func() (map[string]any, error)
		path, body string
		resultKey  string
	}{
		{func() (map[string]any, error) {
			return client.EditTopic(ctx, TopicEdit{MessageThreadID: 7, Name: &name})
		},
			"editForumTopic", `{"chat_id":"-1001","message_thread_id":7,"name":"Neu"}`, "updated"},
		{func() (map[string]any, error) {
			return client.EditTopic(ctx, TopicEdit{MessageThreadID: 7, IconCustomEmojiID: &emoji})
		}, "editForumTopic", `{"chat_id":"-1001","message_thread_id":7,"icon_custom_emoji_id":"5312"}`, "updated"},
		{func() (map[string]any, error) {
			return client.EditTopic(ctx, TopicEdit{MessageThreadID: 7, IconCustomEmojiID: &empty})
		}, "editForumTopic", `{"chat_id":"-1001","message_thread_id":7,"icon_custom_emoji_id":""}`, "updated"},
		{func() (map[string]any, error) { return client.EditTopic(ctx, TopicEdit{General: true, Name: &name}) },
			"editGeneralForumTopic", `{"chat_id":"-1001","name":"Neu"}`, "updated"},
		{func() (map[string]any, error) { return client.CloseTopic(ctx, 7, false) },
			"closeForumTopic", `{"chat_id":"-1001","message_thread_id":7}`, "closed"},
		{func() (map[string]any, error) { return client.CloseTopic(ctx, 0, true) },
			"closeGeneralForumTopic", `{"chat_id":"-1001"}`, "closed"},
		{func() (map[string]any, error) { return client.ReopenTopic(ctx, 7, false) },
			"reopenForumTopic", `{"chat_id":"-1001","message_thread_id":7}`, "reopened"},
		{func() (map[string]any, error) { return client.ReopenTopic(ctx, 0, true) },
			"reopenGeneralForumTopic", `{"chat_id":"-1001"}`, "reopened"},
	}
	for i, c := range calls {
		got, err := c.run()
		if err != nil || got[c.resultKey] != true || len(got) != 1 {
			t.Fatalf("call %d = %v, %v", i, got, err)
		}
		if (*bodies)[i] != c.body || (*paths)[i] != c.path {
			t.Errorf("call %d = %s %s, want %s %s", i, (*paths)[i], (*bodies)[i], c.path, c.body)
		}
	}
}

func TestTopicArgumentsRejectedBeforeIO(t *testing.T) {
	resolutions := 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	resolved := resolvedWith("-1001")
	long := strings.Repeat("ä", 129)
	cases := []struct {
		invoke invokeFunc
		args   string
	}{
		{invokeTopicsEdit, `{"name":"x"}`},
		{invokeTopicsEdit, `{"general":false,"name":"x"}`},
		{invokeTopicsEdit, `{"general":true,"message_thread_id":3,"name":"x"}`},
		{invokeTopicsEdit, `{"message_thread_id":3}`},
		{invokeTopicsEdit, `{"general":true}`},
		{invokeTopicsEdit, `{"general":true,"name":"x","icon_custom_emoji_id":"1"}`},
		{invokeTopicsEdit, `{"message_thread_id":3,"name":""}`},
		{invokeTopicsEdit, `{"message_thread_id":3,"name":"` + long + `"}`},
		{invokeTopicsEdit, `{"message_thread_id":3,"icon_custom_emoji_id":"12a"}`},
		{invokeTopicsClose, `{}`},
		{invokeTopicsClose, `{"general":false}`},
		{invokeTopicsClose, `{"general":true,"message_thread_id":3}`},
		{invokeTopicsReopen, `{}`},
		{invokeTopicsReopen, `{"general":true,"message_thread_id":3}`},
		{invokeTopicsCreate, `{"name":""}`},
		{invokeTopicsCreate, `{"name":"` + long + `"}`},
		{invokeTopicsCreate, `{"name":"x","icon_color":1}`},
		{invokeTopicsCreate, `{"name":"x","icon_custom_emoji_id":""}`},
		{invokeTopicsCreate, `{"name":"x","icon_custom_emoji_id":"` + strings.Repeat("1", 21) + `"}`},
		{invokeTopicsCreate, `{"chat":"-2002","name":"x"}`},
		{invokeTopicsEdit, `{"chat":"-2002","message_thread_id":3,"name":"x"}`},
		{invokeTopicsClose, `{"chat":"-2002","general":true}`},
		{invokeTopicsReopen, `{"chat":"-2002","general":true}`},
	}
	for _, c := range cases {
		_, err := c.invoke(context.Background(), resolved, resolver, nil, json.RawMessage(c.args))
		if err == nil || strings.Contains(err.Error(), "-2002") {
			t.Errorf("%s err = %v", c.args, err)
		}
	}
	if resolutions != 0 {
		t.Errorf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestTopicNameAndColorBoundaries(t *testing.T) {
	client, bodies, _ := capture(t, "-1001", 200, `{"ok":true,"result":{"message_thread_id":5}}`)
	for _, color := range topicIconColors {
		c := color
		if _, err := client.CreateTopic(context.Background(), TopicCreate{Name: strings.Repeat("ä", 128), IconColor: &c}); err != nil {
			t.Errorf("color %d refused: %v", color, err)
		}
	}
	if len(*bodies) != len(topicIconColors) {
		t.Errorf("requests = %d", len(*bodies))
	}
}

func TestTopicCreateOutputAllowlistAndBody(t *testing.T) {
	result := `{"ok":true,"result":{"message_thread_id":5,"name":"Neu","icon_color":7322096,` +
		`"icon_custom_emoji_id":"99","extra":"x","is_name_implicit":false}}`
	client, bodies, paths := capture(t, "-1001", 200, result)
	color, emoji := int64(7322096), "99"
	got, err := client.CreateTopic(context.Background(), TopicCreate{Name: "Neu", IconColor: &color, IconCustomEmojiID: &emoji})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"message_thread_id": int64(5), "name": "Neu", "icon_color": int64(7322096),
		"icon_custom_emoji_id": "99"}
	if len(got) != len(want) {
		t.Fatalf("got = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if (*paths)[0] != "createForumTopic" ||
		(*bodies)[0] != `{"chat_id":"-1001","name":"Neu","icon_color":7322096,"icon_custom_emoji_id":"99"}` {
		t.Errorf("request = %s %s", (*paths)[0], (*bodies)[0])
	}
	client, _, _ = capture(t, "-1001", 200, `{"ok":true,"result":{"name":"x"}}`)
	if _, err := client.CreateTopic(context.Background(), TopicCreate{Name: "x"}); err == nil {
		t.Error("response without message_thread_id accepted")
	}
}

func TestTopicSingleRequestAndUncertaintyAfterFailure(t *testing.T) {
	name := "x"
	ops := map[string]func(*Client) error{
		"create": func(c *Client) error {
			_, err := c.CreateTopic(context.Background(), TopicCreate{Name: "x"})
			return err
		},
		"edit": func(c *Client) error {
			_, err := c.EditTopic(context.Background(), TopicEdit{General: true, Name: &name})
			return err
		},
		"close":  func(c *Client) error { _, err := c.CloseTopic(context.Background(), 3, false); return err },
		"reopen": func(c *Client) error { _, err := c.ReopenTopic(context.Background(), 0, true); return err },
	}
	for opName, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") {
				t.Errorf("%s status %d err = %v", opName, status, err)
			}
			if len(*bodies) != 1 {
				t.Errorf("%s requests = %d, want 1", opName, len(*bodies))
			}
		}
	}
}

func TestTopicDescriptorsRiskAndGroup(t *testing.T) {
	if topicsCreate.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		topicsEdit.Risk.Idempotency != capability.IdempotencyIdempotent {
		t.Errorf("idempotency = %v, %v", topicsCreate.Risk.Idempotency, topicsEdit.Risk.Idempotency)
	}
	for _, id := range []bool{topicsCreate.RequiresToolAllowList, topicsEdit.RequiresToolAllowList,
		topicsClose.RequiresToolAllowList, topicsReopen.RequiresToolAllowList} {
		if id {
			t.Error("topic tool requires a tool allow list")
		}
	}
}
