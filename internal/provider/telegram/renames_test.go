package telegram

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of Telegram tool IDs.
var currentToolIDs = []string{
	"telegram.animations.send",
	"telegram.audio.send",
	"telegram.bot.get",
	"telegram.business.delete-messages",
	"telegram.business.get",
	"telegram.business.gifts",
	"telegram.business.mark-read",
	"telegram.business.remove-photo",
	"telegram.business.set-bio",
	"telegram.business.set-name",
	"telegram.business.set-photo",
	"telegram.business.set-username",
	"telegram.business.star-balance",
	"telegram.callbacks.answer",
	"telegram.chat-actions.send",
	"telegram.chats.administrators",
	"telegram.chats.delete-photo",
	"telegram.chats.delete-sticker-set",
	"telegram.chats.get",
	"telegram.chats.leave",
	"telegram.chats.member",
	"telegram.chats.member-count",
	"telegram.chats.set-description",
	"telegram.chats.set-permissions",
	"telegram.chats.set-photo",
	"telegram.chats.set-sticker-set",
	"telegram.chats.set-title",
	"telegram.checklists.edit",
	"telegram.checklists.send",
	"telegram.commands.delete",
	"telegram.commands.get",
	"telegram.commands.set",
	"telegram.contacts.send",
	"telegram.default-admin-rights.get",
	"telegram.default-admin-rights.set",
	"telegram.dice.send",
	"telegram.documents.send",
	"telegram.files.download",
	"telegram.files.get",
	"telegram.games.high-scores",
	"telegram.games.send",
	"telegram.games.set-score",
	"telegram.gifts.available",
	"telegram.gifts.chat",
	"telegram.gifts.user",
	"telegram.inline.answer",
	"telegram.invite-links.primary",
	"telegram.invite-links.revoke",
	"telegram.join-request-queries.answer",
	"telegram.join-requests.decline",
	"telegram.live-photos.send",
	"telegram.locations.edit-live",
	"telegram.locations.send",
	"telegram.locations.stop-live",
	"telegram.media-groups.send",
	"telegram.members.ban",
	"telegram.members.promote",
	"telegram.members.restrict",
	"telegram.members.set-admin-title",
	"telegram.members.set-tag",
	"telegram.members.unban",
	"telegram.menu-button.get",
	"telegram.menu-button.set",
	"telegram.messages.copy",
	"telegram.messages.delete",
	"telegram.messages.delete-many",
	"telegram.messages.edit",
	"telegram.messages.edit-caption",
	"telegram.messages.edit-media",
	"telegram.messages.edit-reply-markup",
	"telegram.messages.forward",
	"telegram.messages.send",
	"telegram.payments.star-balance",
	"telegram.payments.star-transactions",
	"telegram.photos.send",
	"telegram.pins.pin",
	"telegram.pins.unpin",
	"telegram.pins.unpin-all",
	"telegram.polls.send",
	"telegram.polls.stop",
	"telegram.profile.get",
	"telegram.profile.remove-photo",
	"telegram.profile.set-description",
	"telegram.profile.set-name",
	"telegram.profile.set-photo",
	"telegram.profile.set-short-description",
	"telegram.reactions.remove",
	"telegram.reactions.remove-all",
	"telegram.reactions.set",
	"telegram.rich-messages.send",
	"telegram.sender-chats.ban",
	"telegram.sender-chats.unban",
	"telegram.sticker-sets.add-sticker",
	"telegram.sticker-sets.create",
	"telegram.sticker-sets.delete",
	"telegram.sticker-sets.get",
	"telegram.sticker-sets.set-custom-emoji-thumbnail",
	"telegram.sticker-sets.set-thumbnail",
	"telegram.sticker-sets.set-title",
	"telegram.stickers.custom-emoji",
	"telegram.stickers.delete",
	"telegram.stickers.replace",
	"telegram.stickers.send",
	"telegram.stickers.set-emoji-list",
	"telegram.stickers.set-keywords",
	"telegram.stickers.set-mask-position",
	"telegram.stickers.set-position",
	"telegram.stickers.upload-file",
	"telegram.stories.delete",
	"telegram.stories.edit",
	"telegram.stories.post",
	"telegram.stories.repost",
	"telegram.suggested-posts.approve",
	"telegram.suggested-posts.decline",
	"telegram.topics.close",
	"telegram.topics.create",
	"telegram.topics.delete",
	"telegram.topics.edit",
	"telegram.topics.hide-general",
	"telegram.topics.icon-stickers",
	"telegram.topics.reopen",
	"telegram.topics.unhide-general",
	"telegram.topics.unpin-all",
	"telegram.updates.confirm",
	"telegram.updates.list",
	"telegram.users.chat-boosts",
	"telegram.users.personal-chat-messages",
	"telegram.users.profile-audios",
	"telegram.users.profile-photos",
	"telegram.venues.send",
	"telegram.video-notes.send",
	"telegram.videos.send",
	"telegram.voice.send",
	"telegram.webhook.get",
}

func TestRegistryHoldsExactlyTheCurrentToolIDs(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range reg.All() {
		got = append(got, d.ID)
	}
	sort.Strings(got)
	want := append([]string(nil), currentToolIDs...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 134 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 73 {
		t.Errorf("renamedTools has %d entries, want 73", len(renamedTools))
	}
	current := map[string]bool{}
	for _, id := range currentToolIDs {
		current[id] = true
	}
	targets := map[string]bool{}
	for old, next := range renamedTools {
		targets[next] = true
		if current[old] || !current[next] {
			t.Errorf("rename %s -> %s: old must be gone and new registered", old, next)
		}
		if strings.ReplaceAll(next, "-", "") != old {
			t.Errorf("rename %s -> %s changes more than word boundaries", old, next)
		}
	}
	for id := range current {
		if strings.Contains(id, "-") && !targets[id] {
			t.Errorf("hyphenated tool %s has no former ID", id)
		}
	}
}

func TestFormerTelegramToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  tg:
    provider: telegram
    base_url: https://api.telegram.org
credentials:
  tg-bot:
    provider: telegram
    type: env
    values:
      bot-token: QATLAS_TG_TOKEN
connections:
  alerts:
    service: tg
    credential: tg-bot
    target: "-1001111111111"
    permissions: [create, update]
    tools: [telegram.messages.editreplymarkup, telegram.stickersets.create, telegram.chats.setphoto]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range map[string]string{
		"telegram.messages.editreplymarkup": "telegram.messages.edit-reply-markup",
		"telegram.stickersets.create":       "telegram.sticker-sets.create",
		"telegram.chats.setphoto":           "telegram.chats.set-photo",
	} {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
}
