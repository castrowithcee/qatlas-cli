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
		26: "telegram.chats.settitle", 27: "telegram.commands.delete", 28: "telegram.commands.get",
		29: "telegram.commands.set", 30: "telegram.contacts.send", 31: "telegram.defaultadminrights.get",
		32: "telegram.defaultadminrights.set", 33: "telegram.dice.send", 34: "telegram.documents.send",
		35: "telegram.files.download", 36: "telegram.files.get", 37: "telegram.games.highscores",
		38: "telegram.games.send", 39: "telegram.games.setscore", 40: "telegram.inline.answer",
		41: "telegram.invitelinks.primary", 42: "telegram.invitelinks.revoke",
		43: "telegram.joinrequestqueries.answer", 44: "telegram.joinrequests.decline",
		45: "telegram.livephotos.send", 46: "telegram.locations.editlive", 47: "telegram.locations.send",
		48: "telegram.locations.stoplive", 49: "telegram.mediagroups.send", 50: "telegram.members.ban",
		51: "telegram.members.promote", 52: "telegram.members.restrict", 53: "telegram.members.setadmintitle",
		54: "telegram.members.settag", 55: "telegram.members.unban", 56: "telegram.menubutton.get",
		57: "telegram.menubutton.set", 58: "telegram.messages.copy", 59: "telegram.messages.delete",
		60: "telegram.messages.deletemany", 61: "telegram.messages.edit", 62: "telegram.messages.editcaption",
		63: "telegram.messages.editmedia", 64: "telegram.messages.editreplymarkup", 65: "telegram.messages.forward",
		66: "telegram.messages.send", 67: "telegram.photos.send", 68: "telegram.pins.pin",
		69: "telegram.pins.unpin", 70: "telegram.pins.unpinall", 71: "telegram.polls.send",
		72: "telegram.polls.stop", 73: "telegram.profile.get", 74: "telegram.profile.removephoto",
		75: "telegram.profile.setdescription", 76: "telegram.profile.setname", 77: "telegram.profile.setphoto",
		78: "telegram.profile.setshortdescription", 79: "telegram.reactions.remove",
		80: "telegram.reactions.removeall", 81: "telegram.reactions.set", 82: "telegram.senderchats.ban",
		83: "telegram.senderchats.unban", 84: "telegram.stickers.customemoji", 85: "telegram.stickers.delete",
		86: "telegram.stickers.replace", 87: "telegram.stickers.send", 88: "telegram.stickers.setemojilist",
		89: "telegram.stickers.setkeywords", 90: "telegram.stickers.setmaskposition",
		91: "telegram.stickers.setposition", 92: "telegram.stickers.uploadfile",
		93: "telegram.stickersets.addsticker", 94: "telegram.stickersets.create", 95: "telegram.stickersets.delete",
		96: "telegram.stickersets.get", 97: "telegram.stickersets.setcustomemojithumbnail",
		98: "telegram.stickersets.setthumbnail", 99: "telegram.stickersets.settitle",
		100: "telegram.stories.delete", 101: "telegram.stories.edit", 102: "telegram.stories.post",
		103: "telegram.stories.repost", 104: "telegram.suggestedposts.approve",
		105: "telegram.suggestedposts.decline", 106: "telegram.topics.close", 107: "telegram.topics.create",
		108: "telegram.topics.delete", 109: "telegram.topics.edit", 110: "telegram.topics.hidegeneral",
		111: "telegram.topics.iconstickers", 112: "telegram.topics.reopen", 113: "telegram.topics.unhidegeneral",
		114: "telegram.topics.unpinall", 115: "telegram.updates.confirm", 116: "telegram.updates.list",
		117: "telegram.venues.send", 118: "telegram.videonotes.send", 119: "telegram.videos.send",
		120: "telegram.voice.send", 121: "telegram.webhook.get",
	}
	ok2 := len(operations) == 122
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
