package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// fakeRegistry registers two capabilities for the provider the test configuration uses. No provider
// implementation is involved: discovery answers from the registry alone.
func registerBookstackTestMetadata(t *testing.T, reg *capability.Registry) {
	t.Helper()
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "bookstack", Name: "BookStack",
		SecretRoles: []config.SecretRole{{Name: "token-id"}, {Name: "token-secret"}},
		Target:      config.TargetMetadata{Label: "target"},
	}, nil); err != nil {
		t.Fatalf("RegisterProvider() = %v", err)
	}
}

func fakeRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	registerBookstackTestMetadata(t, reg)
	err := reg.Register("bookstack",
		capability.Operation{
			Descriptor: capability.Descriptor{
				ID:          "bookstack.pages.list",
				Version:     1,
				Description: "List pages",
				Risk: capability.Risk{
					Effect:          capability.EffectRead,
					Idempotency:     capability.IdempotencySafe,
					Confirmation:    capability.ConfirmationNone,
					DataSensitivity: "test-data",
				},
				Provider:     "bookstack",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"array"}`),
				Arguments:    []capability.Argument{{Name: "limit", Description: "Maximum number of pages"}},
				Fields:       []capability.Field{{Name: "id"}, {Name: "name"}},
			},
			Handler: capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error) {
				return []map[string]any{{"id": 1, "name": "Page"}}, nil
			}),
		},
		capability.Operation{
			Descriptor: capability.Descriptor{
				ID:          "bookstack.pages.get",
				Version:     1,
				Description: "Read one page",
				Risk: capability.Risk{
					Effect:          capability.EffectRead,
					Idempotency:     capability.IdempotencySafe,
					Confirmation:    capability.ConfirmationNone,
					DataSensitivity: "test-data",
				},
				Provider:     "bookstack",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"object"}`),
				Arguments:    []capability.Argument{{Name: "id", Description: "Page identifier", Required: true}},
				Fields:       []capability.Field{{Name: "html"}},
			},
			Handler: capability.Handler(func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error) {
				return map[string]any{"html": "<p>Page</p>"}, nil
			}),
		},
	)
	if err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

// runFakeCLI drives the real command tree with a fake provider registry.
func runFakeCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts := &Options{}
	code := run(newRootCommand(opts, fakeRegistry(t)), opts, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// runFakeCLIInput is runFakeCLI with stdin content, for the commands that read arguments from stdin.
func runFakeCLIInput(t *testing.T, request string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	redactor := &redact.Redactor{}
	opts := &Options{
		Input: strings.NewReader(request), Redactor: redactor,
		Secrets: secret.NewWith(nil, nil, nil, redactor),
	}
	code := run(newRootCommand(opts, fakeRegistry(t)), opts, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// The shipped registry must be wirable with every operation and provider metadata contract.
func TestDefaultRegistry(t *testing.T) {
	reg := defaultRegistry()
	if reg == nil {
		t.Fatal("defaultRegistry() = nil")
	}
	got := reg.Provider("bookstack")
	if len(got) != 68 {
		t.Fatalf("provider capabilities = %v, want sixty-eight BookStack capabilities", got)
	}
	if got[1].ID != "bookstack.attachments.download" || got[9].ID != "bookstack.books.create" {
		t.Errorf("capabilities = %v", got)
	}
	telegram, ok := reg.ProviderMetadata("telegram")
	if !ok || telegram.DefaultBaseURL != "https://api.telegram.org" || !telegram.Target.Required ||
		len(telegram.SecretRoles) != 1 || telegram.SecretRoles[0].Name != "bot-token" {
		t.Errorf("Telegram metadata = %+v, %v", telegram, ok)
	}
	operations := reg.Provider("telegram")
	wantIDs := map[int]string{
		0: "telegram.animations.send", 1: "telegram.audio.send", 2: "telegram.bot.get",
		3: "telegram.business.deletemessages", 4: "telegram.business.get", 5: "telegram.business.markread",
		6: "telegram.callbacks.answer", 7: "telegram.chatactions.send", 8: "telegram.chats.administrators",
		9: "telegram.chats.deletephoto", 10: "telegram.chats.deletestickerset", 11: "telegram.chats.get",
		12: "telegram.chats.leave", 13: "telegram.chats.member", 14: "telegram.chats.membercount",
		15: "telegram.chats.setdescription", 16: "telegram.chats.setpermissions", 17: "telegram.chats.setphoto",
		18: "telegram.chats.setstickerset", 19: "telegram.chats.settitle", 20: "telegram.commands.delete",
		21: "telegram.commands.get", 22: "telegram.commands.set", 23: "telegram.contacts.send",
		24: "telegram.defaultadminrights.get", 25: "telegram.defaultadminrights.set", 26: "telegram.dice.send",
		27: "telegram.documents.send", 28: "telegram.files.download", 29: "telegram.files.get",
		30: "telegram.games.highscores", 31: "telegram.games.send", 32: "telegram.games.setscore",
		33: "telegram.inline.answer", 34: "telegram.invitelinks.primary", 35: "telegram.invitelinks.revoke",
		36: "telegram.joinrequestqueries.answer", 37: "telegram.joinrequests.decline",
		38: "telegram.livephotos.send", 39: "telegram.locations.editlive", 40: "telegram.locations.send",
		41: "telegram.locations.stoplive", 42: "telegram.mediagroups.send", 43: "telegram.members.ban",
		44: "telegram.members.promote", 45: "telegram.members.restrict", 46: "telegram.members.setadmintitle",
		47: "telegram.members.settag", 48: "telegram.members.unban", 49: "telegram.menubutton.get",
		50: "telegram.menubutton.set", 51: "telegram.messages.copy", 52: "telegram.messages.delete",
		53: "telegram.messages.deletemany", 54: "telegram.messages.edit", 55: "telegram.messages.editcaption",
		56: "telegram.messages.editmedia", 57: "telegram.messages.editreplymarkup",
		58: "telegram.messages.forward", 59: "telegram.messages.send", 60: "telegram.photos.send",
		61: "telegram.pins.pin", 62: "telegram.pins.unpin", 63: "telegram.pins.unpinall",
		64: "telegram.polls.send", 65: "telegram.polls.stop", 66: "telegram.profile.get",
		67: "telegram.profile.removephoto", 68: "telegram.profile.setdescription",
		69: "telegram.profile.setname", 70: "telegram.profile.setphoto",
		71: "telegram.profile.setshortdescription", 72: "telegram.reactions.remove",
		73: "telegram.reactions.removeall", 74: "telegram.reactions.set", 75: "telegram.senderchats.ban",
		76: "telegram.senderchats.unban", 77: "telegram.stickers.customemoji", 78: "telegram.stickers.delete",
		79: "telegram.stickers.replace", 80: "telegram.stickers.send", 81: "telegram.stickers.setemojilist",
		82: "telegram.stickers.setkeywords", 83: "telegram.stickers.setmaskposition",
		84: "telegram.stickers.setposition", 85: "telegram.stickers.uploadfile",
		86: "telegram.stickersets.addsticker", 87: "telegram.stickersets.create",
		88: "telegram.stickersets.delete", 89: "telegram.stickersets.get",
		90: "telegram.stickersets.setcustomemojithumbnail", 91: "telegram.stickersets.setthumbnail",
		92: "telegram.stickersets.settitle", 93: "telegram.stories.delete", 94: "telegram.stories.edit",
		95: "telegram.stories.post", 96: "telegram.stories.repost", 97: "telegram.suggestedposts.approve",
		98: "telegram.suggestedposts.decline", 99: "telegram.topics.close", 100: "telegram.topics.create",
		101: "telegram.topics.delete", 102: "telegram.topics.edit", 103: "telegram.topics.hidegeneral",
		104: "telegram.topics.iconstickers", 105: "telegram.topics.reopen", 106: "telegram.topics.unhidegeneral",
		107: "telegram.topics.unpinall", 108: "telegram.updates.confirm", 109: "telegram.updates.list",
		110: "telegram.venues.send", 111: "telegram.videonotes.send", 112: "telegram.videos.send",
		113: "telegram.voice.send", 114: "telegram.webhook.get",
	}
	ok2 := len(operations) == 115
	for i, id := range wantIDs {
		ok2 = ok2 && i < len(operations) && operations[i].ID == id
	}
	if !ok2 {
		t.Errorf("Telegram operations = %v, want the explicit message, member, pin, update, chat, invite link, bot, file, media, interaction, structured send, transfer, and sticker operations",
			operations)
	}
}

// Every shipped provider starts new connections with a valid recommended profile that selects reads only,
// except where the provider states why a change is safe to preselect. Telegram, whose read tools expose the
// content of incoming messages, is the only such provider.
func TestShippedProfilesAreValidAndSafe(t *testing.T) {
	reg := defaultRegistry()
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	for _, metadata := range reg.ProviderMetadataAll() {
		profile, ok := metadata.RecommendedProfile()
		if !ok {
			t.Errorf("provider %s has no recommended profile", metadata.ID)
			continue
		}
		permissions := metadata.ProfilePermissions(profile)
		readOnly := len(permissions) == 1 && permissions[0] == config.PermissionRead
		switch {
		case metadata.ID == "telegram":
			if profile.MutationReason == "" || len(profile.Tools) != 1 || profile.Tools[0] != "telegram.messages.send" {
				t.Errorf("telegram recommended profile = %+v, want only the explained send", profile)
			}
		case !readOnly || profile.MutationReason != "":
			t.Errorf("provider %s recommended profile %s selects %v, want reads only", metadata.ID, profile.ID,
				permissions)
		}
	}
}
