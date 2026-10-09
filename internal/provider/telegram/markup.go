package telegram

import (
	"bytes"
	"encoding/json"
	"net/url"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	maxKeyboardRows    = 8
	maxKeyboardButtons = 8
	// maxButtonText is a Qatlas bound; the Bot API documents no maximum for button text.
	maxButtonText = 64
	// maxCallbackBytes is the Bot API limit for callback_data.
	maxCallbackBytes = 64
	maxButtonURL     = 2048
)

// button is an inline keyboard button limited to a link or callback data. Other button kinds (web app,
// login, pay, switch_inline_query) are intentionally not representable.
type button struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}

type keyboard [][]button

// replyMarkup is the wire form of an inline keyboard.
type replyMarkup struct {
	InlineKeyboard keyboard `json:"inline_keyboard"`
}

func markupOf(k keyboard) *replyMarkup {
	if len(k) == 0 {
		return nil
	}
	return &replyMarkup{InlineKeyboard: k}
}

type replyParameters struct {
	MessageID int64 `json:"message_id"`
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

const keyboardSchema = `{"type":"array","maxItems":8,"items":{"type":"array","minItems":1,"maxItems":8,` +
	`"items":{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":64},` +
	`"url":{"type":"string","minLength":1,"maxLength":2048},` +
	`"callback_data":{"type":"string","minLength":1,"maxLength":64}},` +
	`"required":["text"],"additionalProperties":false}}}`

const keyboardArgument = "Inline keyboard as rows of buttons (at most 8 rows of 8); each button has text and " +
	"exactly one of a secure web or Telegram link as url or callback_data of 1 through 64 bytes"

var parseModeArgument = capability.Argument{Name: "parse_mode",
	Description: "Formatting of the text: HTML or MarkdownV2; plain text when omitted"}

func validParseMode(mode string) bool { return mode == "" || mode == "HTML" || mode == "MarkdownV2" }

// validateKeyboard enforces the keyboard limits locally, before any credential or request.
func validateKeyboard(k keyboard) string {
	if len(k) > maxKeyboardRows {
		return "the inline keyboard has too many rows"
	}
	for _, row := range k {
		if len(row) < 1 || len(row) > maxKeyboardButtons {
			return "an inline keyboard row must hold from 1 through 8 buttons"
		}
		for _, b := range row {
			if n := utf8.RuneCountInString(b.Text); !utf8.ValidString(b.Text) || n < 1 || n > maxButtonText {
				return "an inline keyboard button text must be from 1 through 64 characters"
			}
			if (b.URL == "") == (b.CallbackData == "") {
				return "an inline keyboard button needs exactly one of url and callback_data"
			}
			if b.CallbackData != "" && (len(b.CallbackData) > maxCallbackBytes || !utf8.ValidString(b.CallbackData)) {
				return "callback_data must be from 1 through 64 bytes"
			}
			if b.URL != "" && !validButtonURL(b.URL) {
				return "an inline keyboard url must be a plain https:// or tg:// link"
			}
		}
	}
	return ""
}

func validButtonURL(raw string) bool {
	if len(raw) > maxButtonURL || !utf8.ValidString(raw) {
		return false
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "tg"
}

// decodeStrict rejects unknown fields so a button cannot carry another button kind.
func decodeStrict(raw json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
