package telegram

import (
	"context"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	webHost         = "t.me"
	maxWebURLBytes  = 2048
	privateChatBase = "-100"
	// A supergroup or channel ID has at most 13 digits.
	maxPrivateChatDigits = 13
	minUsernameLength    = 5
	maxUsernameLength    = 32
)

// reservedWebPaths are first path segments of t.me that name a feature instead of a chat. Most are shorter
// than a username can be and only guard against a future longer feature path being read as a chat.
var reservedWebPaths = map[string]bool{
	"joinchat": true, "addstickers": true, "addemoji": true, "addtheme": true, "addlist": true, "share": true,
	"proxy": true, "socks": true, "login": true, "setlanguage": true, "confirmphone": true, "invoice": true,
	"boost": true, "giftcode": true, "contact": true, "iv": true, "bg": true, "c": true, "s": true,
}

// targetBrowser has no list: the Bot API cannot enumerate chats, so every kind is unlisted and a web address
// is the only way to choose a target.
func targetBrowser() capability.TargetBrowser {
	return capability.TargetBrowser{List: listTargets, ResolveURL: resolveTargetURL}
}

func listTargets(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
	config.TargetListRequest) (config.TargetPage, error) {
	return config.TargetPage{}, config.ErrTargetBrowseUnsupported
}

// resolveTargetURL reads a t.me link syntactically, without a request: origin is the Bot API's and says
// nothing about the web host. A link that names no chat yields no entry and no error; a link into a forum topic
// yields its chat and the topic.
func resolveTargetURL(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	_, rawURL string) ([]config.TargetEntry, error) {
	target, name, topic, ok := chatOfWebURL(rawURL)
	if !ok {
		return nil, nil
	}
	entries := []config.TargetEntry{{Target: target, Name: name, Kind: "chat"}}
	if topic != "" {
		entries = append(entries, config.TargetEntry{Target: topic, Name: topic, Kind: "topic", Path: target})
	}
	return entries, nil
}

// chatOfWebURL returns the chat of a t.me link and, for a private forum message link, its topic target.
func chatOfWebURL(rawURL string) (target, name, topic string, ok bool) {
	if len(rawURL) > maxWebURLBytes {
		return "", "", "", false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || strings.ToLower(u.Host) != webHost {
		return "", "", "", false
	}
	segments := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if segments[len(segments)-1] == "" {
		segments = segments[:len(segments)-1]
	}
	if len(segments) == 0 {
		return "", "", "", false
	}
	if segments[0] == "c" {
		// c/{id}[/{message}] or c/{id}/{topic}/{message}: only a forum message link has three numbers, so
		// c/{id}/{n} stays the chat alone.
		if len(segments) < 2 || len(segments) > 4 || !numericSegments(segments[1:]) ||
			len(segments[1]) > maxPrivateChatDigits || segments[1][0] == '0' {
			return "", "", "", false
		}
		target = privateChatBase + segments[1]
		if validateTarget(target) != nil {
			return "", "", "", false
		}
		if len(segments) == 4 {
			if _, valid := parseTopic(target + topicInfix + segments[2]); valid {
				topic = target + topicInfix + segments[2]
			}
		}
		return target, target, topic, true
	}
	if len(segments) > 2 || !numericSegments(segments[1:]) || !webUsername(segments[0]) {
		return "", "", "", false
	}
	target = "@" + segments[0]
	if validateTarget(target) != nil {
		return "", "", "", false
	}
	return target, segments[0], "", true
}

// webUsername applies Telegram's username rules, which are stricter than validateTarget: a letter first,
// 5 to 32 characters.
func webUsername(s string) bool {
	if len(s) < minUsernameLength || len(s) > maxUsernameLength || reservedWebPaths[strings.ToLower(s)] {
		return false
	}
	return (s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')
}

// numericSegments reports whether every segment is a non-empty run of ASCII digits.
func numericSegments(segments []string) bool {
	for _, s := range segments {
		if s == "" {
			return false
		}
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
