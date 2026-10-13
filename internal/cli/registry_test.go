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
		3: "telegram.business.deletemessages", 4: "telegram.business.get", 5: "telegram.business.gifts",
		6: "telegram.business.markread", 7: "telegram.business.removephoto", 8: "telegram.business.setbio",
		9: "telegram.business.setname", 10: "telegram.business.setphoto", 11: "telegram.business.setusername",
		12: "telegram.business.starbalance", 13: "telegram.callbacks.answer", 14: "telegram.chatactions.send",
		15: "telegram.chats.administrators", 16: "telegram.chats.deletephoto",
		17: "telegram.chats.deletestickerset", 18: "telegram.chats.get", 19: "telegram.chats.leave",
		20: "telegram.chats.member", 21: "telegram.chats.membercount", 22: "telegram.chats.setdescription",
		23: "telegram.chats.setpermissions", 24: "telegram.chats.setphoto", 25: "telegram.chats.setstickerset",
		26: "telegram.chats.settitle", 27: "telegram.checklists.edit", 28: "telegram.checklists.send",
		29: "telegram.commands.delete", 30: "telegram.commands.get", 31: "telegram.commands.set",
		32: "telegram.contacts.send", 33: "telegram.defaultadminrights.get", 34: "telegram.defaultadminrights.set",
		35: "telegram.dice.send", 36: "telegram.documents.send", 37: "telegram.files.download",
		38: "telegram.files.get", 39: "telegram.games.highscores", 40: "telegram.games.send",
		41: "telegram.games.setscore", 42: "telegram.inline.answer", 43: "telegram.invitelinks.primary",
		44: "telegram.invitelinks.revoke", 45: "telegram.joinrequestqueries.answer",
		46: "telegram.joinrequests.decline", 47: "telegram.livephotos.send", 48: "telegram.locations.editlive",
		49: "telegram.locations.send", 50: "telegram.locations.stoplive", 51: "telegram.mediagroups.send",
		52: "telegram.members.ban", 53: "telegram.members.promote", 54: "telegram.members.restrict",
		55: "telegram.members.setadmintitle", 56: "telegram.members.settag", 57: "telegram.members.unban",
		58: "telegram.menubutton.get", 59: "telegram.menubutton.set", 60: "telegram.messages.copy",
		61: "telegram.messages.delete", 62: "telegram.messages.deletemany", 63: "telegram.messages.edit",
		64: "telegram.messages.editcaption", 65: "telegram.messages.editmedia",
		66: "telegram.messages.editreplymarkup", 67: "telegram.messages.forward", 68: "telegram.messages.send",
		69: "telegram.photos.send", 70: "telegram.pins.pin", 71: "telegram.pins.unpin",
		72: "telegram.pins.unpinall", 73: "telegram.polls.send", 74: "telegram.polls.stop",
		75: "telegram.profile.get", 76: "telegram.profile.removephoto", 77: "telegram.profile.setdescription",
		78: "telegram.profile.setname", 79: "telegram.profile.setphoto", 80: "telegram.profile.setshortdescription",
		81: "telegram.reactions.remove", 82: "telegram.reactions.removeall", 83: "telegram.reactions.set",
		84: "telegram.senderchats.ban", 85: "telegram.senderchats.unban", 86: "telegram.stickers.customemoji",
		87: "telegram.stickers.delete", 88: "telegram.stickers.replace", 89: "telegram.stickers.send",
		90: "telegram.stickers.setemojilist", 91: "telegram.stickers.setkeywords",
		92: "telegram.stickers.setmaskposition", 93: "telegram.stickers.setposition",
		94: "telegram.stickers.uploadfile", 95: "telegram.stickersets.addsticker",
		96: "telegram.stickersets.create", 97: "telegram.stickersets.delete", 98: "telegram.stickersets.get",
		99: "telegram.stickersets.setcustomemojithumbnail", 100: "telegram.stickersets.setthumbnail",
		101: "telegram.stickersets.settitle", 102: "telegram.stories.delete", 103: "telegram.stories.edit",
		104: "telegram.stories.post", 105: "telegram.stories.repost", 106: "telegram.suggestedposts.approve",
		107: "telegram.suggestedposts.decline", 108: "telegram.topics.close", 109: "telegram.topics.create",
		110: "telegram.topics.delete", 111: "telegram.topics.edit", 112: "telegram.topics.hidegeneral",
		113: "telegram.topics.iconstickers", 114: "telegram.topics.reopen", 115: "telegram.topics.unhidegeneral",
		116: "telegram.topics.unpinall", 117: "telegram.updates.confirm", 118: "telegram.updates.list",
		119: "telegram.users.chatboosts", 120: "telegram.users.personalchatmessages",
		121: "telegram.users.profileaudios", 122: "telegram.users.profilephotos", 123: "telegram.venues.send",
		124: "telegram.videonotes.send", 125: "telegram.videos.send", 126: "telegram.voice.send",
		127: "telegram.webhook.get",
	}
	ok2 := len(operations) == 128
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
