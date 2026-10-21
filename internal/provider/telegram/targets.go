package telegram

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	botTarget      = "bot"
	businessPrefix = "business/"
	topicInfix     = "/topics/"
)

// targetSet is the parsed target boundary of one connection. Chats keep their configured spelling. A chat
// that only topics bind is in no entry of chats: every tool that reads chats alone therefore stays away
// from it.
type targetSet struct {
	chats      []string
	topics     []topicTarget
	bot        bool
	businesses []string
}

// topicTarget is one forum topic target; binding is its configured spelling.
type topicTarget struct {
	chat    string
	thread  int64
	binding string
}

// numericChatMethods lists the Bot API methods whose chat_id is documented as Integer only, without the
// "Integer or String" form of sendMessage, editMessageText, and deleteMessage. An @username target must never
// reach them.
var numericChatMethods = map[string]bool{
	"readBusinessMessage":  true,
	"sendChecklist":        true,
	"editMessageChecklist": true,
	"approveSuggestedPost": true,
	"declineSuggestedPost": true,
	"sendGame":             true,
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
	Unlisted:    true,
}, {
	Name: "topic",
	Description: "one forum topic of a chat; the chat tools then send and read only in this topic, and tools that " +
		"act on the whole chat or on a message by its ID need the chat itself; repeatable",
	Forms:    []string{"CHAT_ID" + topicInfix + "THREAD_ID"},
	Parent:   "chat",
	Unlisted: true,
}, {
	Name:        "bot",
	Description: "unlocks bot-wide tools that address no chat; optional",
	Forms:       []string{botTarget},
	Unlisted:    true,
}, {
	Name:        "business",
	Description: "unlocks the tools of one business connection; optional, repeatable",
	Forms:       []string{businessPrefix + "CONNECTION_ID"},
	Unlisted:    true,
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
	case strings.Contains(raw, topicInfix):
		if _, ok := parseTopic(raw); !ok {
			return "", "", errors.New("a topic target must be CHAT_ID/topics/THREAD_ID with a numeric chat ID " +
				"and a positive thread ID")
		}
		return "topic", raw, nil
	}
	if err := validateTarget(raw); err != nil {
		return "", "", errors.New("a Telegram target must be a chat ID, an @username, CHAT_ID/topics/THREAD_ID, " +
			"bot, or business/CONNECTION_ID")
	}
	return "chat", raw, nil
}

// parseTopic reads CHAT_ID/topics/THREAD_ID. Both numbers must be spelled canonically, so a binding compares
// exactly; an @username is refused because it cannot be matched to the numeric chat of an update.
func parseTopic(raw string) (topicTarget, bool) {
	chat, thread, _ := strings.Cut(raw, topicInfix)
	chatID, err := strconv.ParseInt(chat, 10, 64)
	if err != nil || chatID == 0 || strconv.FormatInt(chatID, 10) != chat {
		return topicTarget{}, false
	}
	threadID, err := strconv.ParseInt(thread, 10, 64)
	if err != nil || threadID < 1 || strconv.FormatInt(threadID, 10) != thread {
		return topicTarget{}, false
	}
	return topicTarget{chat: chat, thread: threadID, binding: raw}, true
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
		case "topic":
			topic, _ := parseTopic(value)
			set.topics = append(set.topics, topic)
		case "bot":
			set.bot = true
		case "business":
			set.businesses = append(set.businesses, value)
		}
	}
	return set, nil
}

// selectChat picks the chat a chat tool addresses from the bound chat targets only. The comparison is exact:
// an @username never equals a numeric ID. A chat that only topics bind is refused: the tool acts on the whole
// chat or on a message whose topic it cannot see. Errors never name a target.
func selectChat(resolved *config.Resolved, requested string) (string, error) {
	return selectChatArgument(resolved, requested, "chat")
}

// selectChatArgument is selectChat for a tool with several chat arguments; argument names the one being
// resolved so the refusal says which argument failed.
func selectChatArgument(resolved *config.Resolved, requested, argument string) (string, error) {
	set, err := targetsOf(resolved)
	if err != nil {
		return "", providerError("select chat", "the configured Telegram targets are unusable")
	}
	chat, named, err := set.pickChat(requested, argument)
	if err != nil {
		return "", err
	}
	if named != 0 {
		return "", providerError("select chat", "the "+argument+" names a forum topic; this tool needs the whole chat")
	}
	if !set.hasChat(chat) {
		return "", providerError("select chat", "the "+argument+" is bound only through forum topics; "+
			"this tool needs the whole chat bound")
	}
	return chat, nil
}

// selectThread is selectChat for a tool that sends into or acts on one topic given by *thread. A chat that
// only topics bind is reached in those topics alone: a missing thread is set to the only bound topic of the
// chat, and any other thread is refused, as is any reply: Telegram returns and quotes the replied message,
// whose topic cannot be told without reading it. A wholly bound chat leaves *thread as it is. binding is the
// target spelling that references issued for the selection carry.
func selectThread(resolved *config.Resolved, requested, argument string, thread *int64, reply int64) (chat,
	binding string, err error) {
	set, err := targetsOf(resolved)
	if err != nil {
		return "", "", providerError("select chat", "the configured Telegram targets are unusable")
	}
	chat, named, err := set.pickChat(requested, argument)
	if err != nil {
		return "", "", err
	}
	if named != 0 {
		if *thread != 0 && *thread != named {
			return "", "", providerError("select chat", "message_thread_id differs from the topic the "+argument+
				" names")
		}
		*thread = named
	}
	if set.hasChat(chat) {
		return chat, chat, nil
	}
	if reply != 0 {
		return "", "", providerError("select chat", "a reply is not available in a chat bound only through "+
			"forum topics")
	}
	var topics []topicTarget
	for _, topic := range set.topics {
		if topic.chat == chat {
			topics = append(topics, topic)
		}
	}
	if *thread == 0 {
		if len(topics) != 1 {
			return "", "", providerError("select chat", "this chat is bound through several forum topics; "+
				"message_thread_id is required")
		}
		*thread = topics[0].thread
		return chat, topics[0].binding, nil
	}
	for _, topic := range topics {
		if topic.thread == *thread {
			return chat, topic.binding, nil
		}
	}
	return "", "", providerError("select chat", "the message_thread_id is not a forum topic bound to this connection")
}

// pickChat resolves requested, or the only bound chat when it is empty, to a chat that a chat or topic target
// binds. A requested topic spelling also returns its thread as named.
func (s targetSet) pickChat(requested, argument string) (chat string, named int64, err error) {
	if requested == "" {
		chats := slices.Clone(s.chats)
		for _, topic := range s.topics {
			if !slices.Contains(chats, topic.chat) {
				chats = append(chats, topic.chat)
			}
		}
		switch len(chats) {
		case 1:
			return chats[0], 0, nil
		case 0:
			return "", 0, providerError("select chat", "this connection binds no chat target")
		}
		return "", 0, providerError("select chat",
			"this connection binds several chats; the "+argument+" argument is required")
	}
	for _, topic := range s.topics {
		if topic.binding == requested {
			return topic.chat, topic.thread, nil
		}
	}
	if s.hasChat(requested) || slices.ContainsFunc(s.topics, func(t topicTarget) bool { return t.chat == requested }) {
		return requested, 0, nil
	}
	return "", 0, providerError("select chat", "the "+argument+" is not bound to this connection")
}

// hasChat reports whether a chat target binds exactly this chat as a whole.
func (s targetSet) hasChat(chat string) bool {
	return slices.Contains(s.chats, chat)
}

// topicBinding returns the spelling of the topic target that binds this thread of this chat.
func (s targetSet) topicBinding(chat, thread int64) (string, bool) {
	if thread == 0 {
		return "", false
	}
	for _, topic := range s.topics {
		if topic.thread == thread && topic.chat == strconv.FormatInt(chat, 10) {
			return topic.binding, true
		}
	}
	return "", false
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
	if !set.hasBusiness(id) {
		return providerError("check scope", "this connection does not bind that business connection")
	}
	return nil
}

// hasBusiness reports whether the connection binds exactly this business connection.
func (s targetSet) hasBusiness(id string) bool {
	for _, bound := range s.businesses {
		if bound == id {
			return true
		}
	}
	return false
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

// topicTools are the chat tools a chat that only topics bind still reaches: each sends into or manages the
// topic its message_thread_id names, which selectThread keeps to a bound topic.
var topicTools = map[string]bool{
	messagesSend.ID: true, richMessagesSend.ID: true, photosSend.ID: true, documentsSend.ID: true,
	videosSend.ID: true, animationsSend.ID: true, videoNotesSend.ID: true, audioSend.ID: true, voiceSend.ID: true,
	livePhotosSend.ID: true, mediaGroupsSend.ID: true, stickersSend.ID: true, pollsSend.ID: true,
	chatActionsSend.ID: true, locationsSend.ID: true, venuesSend.ID: true, contactsSend.ID: true,
	diceSend.ID: true, gamesSend.ID: true, topicsEdit.ID: true, topicsClose.ID: true, topicsReopen.ID: true,
	topicsDelete.ID: true, topicsUnpinAll.ID: true,
}

// topicReaches is the Reaches answer of the Telegram target. A connection whose chats only topics bind does
// not reach a chat tool outside topicTools, since the call refuses such a chat; the chat tools are those with
// a chat argument and the join request answer, whose reference only a whole chat issues. Every other binding
// reaches every tool.
func topicReaches(ops []capability.Operation) func(string, []string) (bool, []string) {
	chatTools := map[string]bool{joinrequestqueriesAnswer.ID: true}
	for _, op := range ops {
		for _, argument := range op.Descriptor.Arguments {
			if argument.Target == "chat" {
				chatTools[op.Descriptor.ID] = true
			}
		}
	}
	return func(tool string, targets []string) (bool, []string) {
		if !chatTools[tool] || topicTools[tool] {
			return true, nil
		}
		topics := false
		for _, raw := range targets {
			kind, _, err := parseTarget(raw)
			if err != nil || kind == "chat" {
				return true, nil
			}
			topics = topics || kind == "topic"
		}
		if !topics {
			return true, nil
		}
		return false, targetKinds[0].Forms
	}
}
