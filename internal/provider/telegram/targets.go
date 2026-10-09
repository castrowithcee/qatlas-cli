package telegram

import (
	"errors"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	botTarget      = "bot"
	businessPrefix = "business/"
)

// targetSet is the parsed target boundary of one connection. Chats keep their configured spelling.
type targetSet struct {
	chats      []string
	bot        bool
	businesses []string
}

// numericChatMethods lists the Bot API methods whose chat_id is documented as Integer only, without the
// "Integer or String" form of sendMessage, editMessageText, and deleteMessage. An @username target must never
// reach them.
var numericChatMethods = map[string]bool{
	"readBusinessMessage":  true,
	"approveSuggestedPost": true,
	"declineSuggestedPost": true,
	"setGameScore":         true,
	"getGameHighScores":    true,
	"setChatMenuButton":    true,
	"getChatMenuButton":    true,
	"sendMessageDraft":     true,
	"sendRichMessageDraft": true,
}

var targetKinds = []config.TargetKind{{
	Name:        "chat",
	Description: "a chat the chat tools may address; repeatable, a single chat is chosen implicitly",
	Forms:       []string{"CHAT_ID", "@CHANNEL_USERNAME"},
}, {
	Name:        "bot",
	Description: "unlocks bot-wide tools that address no chat; optional",
	Forms:       []string{botTarget},
}, {
	Name:        "business",
	Description: "unlocks the tools of one business connection; optional, repeatable",
	Forms:       []string{businessPrefix + "CONNECTION_ID"},
}}

func parseTarget(raw string) (kind, value string, err error) {
	switch {
	case raw == botTarget:
		return "bot", raw, nil
	case strings.HasPrefix(raw, businessPrefix):
		id := strings.TrimPrefix(raw, businessPrefix)
		if !validBusinessID(id) {
			return "", "", errors.New("a business target must be business/CONNECTION_ID with letters, digits, '-', '_' or '='")
		}
		return "business", id, nil
	}
	if err := validateTarget(raw); err != nil {
		return "", "", errors.New("a Telegram target must be a chat ID, an @username, bot, or business/CONNECTION_ID")
	}
	return "chat", raw, nil
}

func validBusinessID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '=' {
			continue
		}
		return false
	}
	return true
}

// validateConfiguredTarget is the per-entry check run by configuration validation.
func validateConfiguredTarget(raw string) error {
	_, _, err := parseTarget(raw)
	return err
}

// targetsOf parses every configured target; one unusable entry makes the whole connection unusable.
func targetsOf(resolved *config.Resolved) (targetSet, error) {
	var set targetSet
	values := provider.TargetsOf(resolved)
	if len(values) == 0 {
		return set, errors.New("no target is configured")
	}
	for _, raw := range values {
		kind, value, err := parseTarget(raw)
		if err != nil {
			return targetSet{}, err
		}
		switch kind {
		case "chat":
			set.chats = append(set.chats, value)
		case "bot":
			set.bot = true
		case "business":
			set.businesses = append(set.businesses, value)
		}
	}
	return set, nil
}

// selectChat picks the chat a chat tool addresses from the bound chat targets only. The comparison is exact:
// an @username never equals a numeric ID. Errors never name a target.
func selectChat(resolved *config.Resolved, requested string) (string, error) {
	set, err := targetsOf(resolved)
	if err != nil {
		return "", providerError("select chat", "the configured Telegram targets are unusable")
	}
	if requested == "" {
		if len(set.chats) == 1 {
			return set.chats[0], nil
		}
		if len(set.chats) == 0 {
			return "", providerError("select chat", "this connection binds no chat target")
		}
		return "", providerError("select chat", "this connection binds several chats; the chat argument is required")
	}
	for _, chat := range set.chats {
		if chat == requested {
			return chat, nil
		}
	}
	return "", providerError("select chat", "the chat is not bound to this connection")
}

// requireBotScope refuses a bot-wide tool unless the connection binds the bot target.
func requireBotScope(resolved *config.Resolved) error {
	set, err := targetsOf(resolved)
	if err != nil {
		return providerError("check scope", "the configured Telegram targets are unusable")
	}
	if !set.bot {
		return providerError("check scope", "this connection does not bind the bot target")
	}
	return nil
}

// requireBusiness refuses a business tool unless the connection binds exactly this business connection.
func requireBusiness(resolved *config.Resolved, id string) error {
	set, err := targetsOf(resolved)
	if err != nil {
		return providerError("check scope", "the configured Telegram targets are unusable")
	}
	for _, bound := range set.businesses {
		if bound == id {
			return nil
		}
	}
	return providerError("check scope", "this connection does not bind that business connection")
}

// requireNumericChat refuses locally, before any I/O, an @username for a method that accepts only a numeric
// chat_id.
func requireNumericChat(method, chat string) error {
	if numericChatMethods[method] {
		if _, err := strconv.ParseInt(chat, 10, 64); err != nil {
			return providerError("check chat", "this Telegram method needs a numeric chat ID")
		}
	}
	return nil
}
