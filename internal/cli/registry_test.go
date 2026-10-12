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
		6: "telegram.business.markread", 7: "telegram.business.removephoto", 8: "telegram.business.setphoto",
		9: "telegram.business.starbalance", 10: "telegram.callbacks.answer", 11: "telegram.chatactions.send",
		12: "telegram.chats.administrators", 13: "telegram.chats.deletephoto", 14: "telegram.chats.deletestickerset",
		15: "telegram.chats.get", 16: "telegram.chats.leave", 17: "telegram.chats.member",
		18: "telegram.chats.membercount", 19: "telegram.chats.setdescription", 20: "telegram.chats.setpermissions",
		21: "telegram.chats.setphoto", 22: "telegram.chats.setstickerset", 23: "telegram.chats.settitle",
		24: "telegram.commands.delete", 25: "telegram.commands.get", 26: "telegram.commands.set",
		27: "telegram.contacts.send", 28: "telegram.defaultadminrights.get", 29: "telegram.defaultadminrights.set",
		30: "telegram.dice.send", 31: "telegram.documents.send", 32: "telegram.files.download",
		33: "telegram.files.get", 34: "telegram.games.highscores", 35: "telegram.games.send",
		36: "telegram.games.setscore", 37: "telegram.inline.answer", 38: "telegram.invitelinks.primary",
		39: "telegram.invitelinks.revoke", 40: "telegram.joinrequestqueries.answer",
		41: "telegram.joinrequests.decline", 42: "telegram.livephotos.send", 43: "telegram.locations.editlive",
		44: "telegram.locations.send", 45: "telegram.locations.stoplive", 46: "telegram.mediagroups.send",
		47: "telegram.members.ban", 48: "telegram.members.promote", 49: "telegram.members.restrict",
		50: "telegram.members.setadmintitle", 51: "telegram.members.settag", 52: "telegram.members.unban",
		53: "telegram.menubutton.get", 54: "telegram.menubutton.set", 55: "telegram.messages.copy",
		56: "telegram.messages.delete", 57: "telegram.messages.deletemany", 58: "telegram.messages.edit",
		59: "telegram.messages.editcaption", 60: "telegram.messages.editmedia",
		61: "telegram.messages.editreplymarkup", 62: "telegram.messages.forward", 63: "telegram.messages.send",
		64: "telegram.photos.send", 65: "telegram.pins.pin", 66: "telegram.pins.unpin", 67: "telegram.pins.unpinall",
		68: "telegram.polls.send", 69: "telegram.polls.stop", 70: "telegram.profile.get",
		71: "telegram.profile.removephoto", 72: "telegram.profile.setdescription", 73: "telegram.profile.setname",
		74: "telegram.profile.setphoto", 75: "telegram.profile.setshortdescription", 76: "telegram.reactions.remove",
		77: "telegram.reactions.removeall", 78: "telegram.reactions.set", 79: "telegram.senderchats.ban",
		80: "telegram.senderchats.unban", 81: "telegram.stickers.customemoji", 82: "telegram.stickers.delete",
		83: "telegram.stickers.replace", 84: "telegram.stickers.send", 85: "telegram.stickers.setemojilist",
		86: "telegram.stickers.setkeywords", 87: "telegram.stickers.setmaskposition",
		88: "telegram.stickers.setposition", 89: "telegram.stickers.uploadfile",
		90: "telegram.stickersets.addsticker", 91: "telegram.stickersets.create", 92: "telegram.stickersets.delete",
		93: "telegram.stickersets.get", 94: "telegram.stickersets.setcustomemojithumbnail",
		95: "telegram.stickersets.setthumbnail", 96: "telegram.stickersets.settitle",
		97: "telegram.suggestedposts.approve", 98: "telegram.suggestedposts.decline", 99: "telegram.topics.close",
		100: "telegram.topics.create", 101: "telegram.topics.delete", 102: "telegram.topics.edit",
		103: "telegram.topics.hidegeneral", 104: "telegram.topics.iconstickers", 105: "telegram.topics.reopen",
		106: "telegram.topics.unhidegeneral", 107: "telegram.topics.unpinall", 108: "telegram.updates.confirm",
		109: "telegram.updates.list", 110: "telegram.venues.send", 111: "telegram.videonotes.send",
		112: "telegram.videos.send", 113: "telegram.voice.send", 114: "telegram.webhook.get",
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
