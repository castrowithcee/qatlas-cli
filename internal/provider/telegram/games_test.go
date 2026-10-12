package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func TestGamesBodiesPathsAndResults(t *testing.T) {
	ctx := context.Background()
	score, zero := int64(120), int64(0)
	force := false
	yes := true
	tests := []struct {
		name, reply, path, body string
		run                     func(*Client) (map[string]any, error)
		want                    string
	}{
		{"send", structuredOK, "sendGame", `{"chat_id":-1001,"game_short_name":"snake"}`,
			func(c *Client) (map[string]any, error) { return c.SendGame(ctx, GameOptions{GameShortName: "snake"}) },
			`{"date":1787220000,"message_id":7}`},
		{"send options", structuredOK, "sendGame",
			`{"chat_id":-1001,"game_short_name":"a_1","message_thread_id":4,"disable_notification":true,"protect_content":true,"reply_parameters":{"message_id":3}}`,
			func(c *Client) (map[string]any, error) {
				return c.SendGame(ctx, GameOptions{GameShortName: "a_1",
					SendCommon: SendCommon{ReplyToMessageID: 3, MessageThreadID: 4, DisableNotification: true, ProtectContent: true}})
			}, `{"date":1787220000,"message_id":7}`},
		{"score true", `{"ok":true,"result":true}`, "setGameScore",
			`{"user_id":42,"score":120,"chat_id":-1001,"message_id":9}`,
			func(c *Client) (map[string]any, error) {
				return c.SetGameScore(ctx, ScoreOptions{MessageID: 9, UserID: 42, Score: &score})
			}, `{"set":true}`},
		{"score message", `{"ok":true,"result":{"message_id":9,"text":"x"}}`, "setGameScore",
			`{"user_id":42,"score":0,"force":true,"disable_edit_message":true,"chat_id":-1001,"message_id":9}`,
			func(c *Client) (map[string]any, error) {
				return c.SetGameScore(ctx, ScoreOptions{MessageID: 9, UserID: 42, Score: &zero, Force: &yes, DisableEditMessage: true})
			}, `{"message_id":9,"set":true}`},
		{"score force false", `{"ok":true,"result":true}`, "setGameScore",
			`{"user_id":42,"score":120,"force":false,"chat_id":-1001,"message_id":9}`,
			func(c *Client) (map[string]any, error) {
				return c.SetGameScore(ctx, ScoreOptions{MessageID: 9, UserID: 42, Score: &score, Force: &force})
			}, `{"set":true}`},
		{"highscores", `{"ok":true,"result":[{"position":1,"score":50,"user":{"id":5,"is_bot":false,"first_name":"Al","username":"al","language_code":"de"},"extra":1},{"position":2,"score":40}]}`,
			"getGameHighScores", `{"user_id":42,"chat_id":-1001,"message_id":9}`,
			func(c *Client) (map[string]any, error) { return c.GameHighScores(ctx, 9, 42) },
			`{"scores":[{"position":1,"score":50,"user":{"id":5,"is_bot":false,"first_name":"Al","username":"al"}},{"position":2,"score":40}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, bodies, paths := capture(t, "-1001", 200, tt.reply)
			got, err := tt.run(client)
			if err != nil {
				t.Fatal(err)
			}
			if len(*bodies) != 1 || (*paths)[0] != tt.path || (*bodies)[0] != tt.body {
				t.Errorf("request = %q %q, want %q %q", *paths, *bodies, tt.path, tt.body)
			}
			out, _ := json.Marshal(got)
			if string(out) != tt.want {
				t.Errorf("result = %s, want %s", out, tt.want)
			}
		})
	}
}

func TestGamesHighScoresAreCapped(t *testing.T) {
	var entries []string
	for i := 1; i <= maxHighScores+3; i++ {
		entries = append(entries, `{"position":1,"score":1}`)
	}
	client, _, _ := capture(t, "-1001", 200, `{"ok":true,"result":[`+strings.Join(entries, ",")+`]}`)
	got, err := client.GameHighScores(context.Background(), 1, 1)
	if err != nil || len(got["scores"].([]map[string]any)) != maxHighScores || got["truncated"] != true {
		t.Errorf("got = %v, %v", got, err)
	}
}

func TestGamesRefuseBeforeSecretAndIO(t *testing.T) {
	resolutions, requests := 0, 0
	resolver := secret.NewWith(func(string) string { resolutions++; return testToken }, nil, nil, nil)
	invokes := map[string]invokeFunc{
		"send": invokeGamesSend, "setscore": invokeGamesSetScore, "highscores": invokeGamesHighScores,
	}
	valid := map[string]string{
		"send":       `"game_short_name":"snake"`,
		"setscore":   `"message_id":1,"user_id":2,"score":3`,
		"highscores": `"message_id":1,"user_id":2`,
	}
	bad := map[string][]string{
		"send": {`"game_short_name":"bad name"`, `"game_short_name":""`, `"game_short_name":"snake","reply_to_message_id":-1`,
			`"game_short_name":"snake","reply_markup":{}`, `"game_short_name":"snake","allow_paid_broadcast":true`},
		"setscore": {`"message_id":1,"user_id":2,"score":-1`, `"message_id":0,"user_id":2,"score":1`,
			`"message_id":1,"user_id":0,"score":1`, `"message_id":1,"user_id":2`, `"message_id":1,"user_id":2,"score":1,"x":1`,
			`"message_id":1,"user_id":2,"score":1,"inline_message_id":"a"`},
		"highscores": {`"message_id":0,"user_id":2`, `"message_id":1,"user_id":-2`, `"message_id":1,"user_id":2,"x":1`},
	}
	for name, invoke := range invokes {
		resolved := resolvedWith("-1001")
		for _, args := range bad[name] {
			if _, err := invoke(context.Background(), resolved, resolver, nil, json.RawMessage("{"+args+"}")); err == nil {
				t.Errorf("%s accepted %s", name, args)
			}
		}
		for _, c := range []struct {
			resolved *config.Resolved
			chat     string
		}{{resolvedWith("-1001", "-1002"), ""}, {resolvedWith("-1001"), "-1002"}, {resolvedWith("@news"), ""},
			{resolvedWith("bot"), ""}, {resolvedWith("-1001", "-1002"), "@news"}} {
			args := valid[name]
			if c.chat != "" {
				args += `,"chat":"` + c.chat + `"`
			}
			before := resolutions
			if _, err := invoke(context.Background(), c.resolved, resolver, nil, json.RawMessage("{"+args+"}")); err == nil {
				t.Errorf("%s accepted targets %v chat %q", name, c.resolved.Targets, c.chat)
			}
			if resolutions != before {
				t.Errorf("%s resolved secret for %v %q", name, c.resolved.Targets, c.chat)
			}
		}
	}
	_ = requests
	if resolutions != 0 {
		t.Fatalf("secret resolutions = %d, want 0", resolutions)
	}
}

func TestGamesMutationsNeverRetry(t *testing.T) {
	ctx := context.Background()
	score := int64(1)
	ops := map[string]func(*Client) error{
		"send": func(c *Client) error { _, err := c.SendGame(ctx, GameOptions{GameShortName: "snake"}); return err },
		"setscore": func(c *Client) error {
			_, err := c.SetGameScore(ctx, ScoreOptions{MessageID: 1, UserID: 2, Score: &score})
			return err
		},
	}
	for name, op := range ops {
		for _, status := range []int{500, 502} {
			client, bodies, _ := capture(t, "-1001", status, `{"ok":false,"description":"secret text"}`)
			err := op(client)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), "secret text") ||
				len(*bodies) != 1 {
				t.Errorf("%s status %d err = %v requests = %d", name, status, err, len(*bodies))
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
	}
	client, _, _ := capture(t, "-1001", 200, `{"ok":true,"result":{"x":1}}`)
	if _, err := client.SetGameScore(ctx, ScoreOptions{MessageID: 1, UserID: 2, Score: &score}); err == nil ||
		!strings.Contains(err.Error(), "may have taken effect") {
		t.Errorf("unreadable result err = %v", err)
	}
}

func TestGamesRiskGroupAndNoProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		d       capability.Descriptor
		effect  capability.Effect
		idem    capability.Idempotency
		confirm capability.Confirmation
	}{{gamesSend, capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired},
		{gamesSetScore, capability.EffectUpdate, capability.IdempotencyUnknown, capability.ConfirmationRequired},
		{gamesHighScores, capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone}} {
		r := c.d.Risk
		if r.Effect != c.effect || r.Idempotency != c.idem || r.Confirmation != c.confirm || !r.OpenWorld ||
			r.DataSensitivity == "" || c.d.Group != groupGames || c.d.RequiresToolAllowList {
			t.Errorf("%s = %+v", c.d.ID, c.d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, tool := range profile.Tools {
			if strings.HasPrefix(tool, "telegram.games.") {
				t.Errorf("profile %s contains %s", profile.ID, tool)
			}
		}
	}
	_ = redact.Redactor{}
}
