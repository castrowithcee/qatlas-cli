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
		3: "telegram.callbacks.answer", 4: "telegram.chatactions.send", 5: "telegram.chats.administrators",
		6: "telegram.chats.deletephoto", 7: "telegram.chats.deletestickerset", 8: "telegram.chats.get",
		9: "telegram.chats.leave", 10: "telegram.chats.member", 11: "telegram.chats.membercount",
		12: "telegram.chats.setdescription", 13: "telegram.chats.setpermissions", 14: "telegram.chats.setphoto",
		15: "telegram.chats.setstickerset", 16: "telegram.chats.settitle", 17: "telegram.commands.delete",
		18: "telegram.commands.get", 19: "telegram.commands.set", 20: "telegram.contacts.send",
		21: "telegram.defaultadminrights.get", 22: "telegram.defaultadminrights.set", 23: "telegram.dice.send",
		24: "telegram.documents.send", 25: "telegram.files.download", 26: "telegram.files.get",
		27: "telegram.invitelinks.primary", 28: "telegram.invitelinks.revoke",
		29: "telegram.joinrequestqueries.answer", 30: "telegram.joinrequests.decline",
		31: "telegram.livephotos.send", 32: "telegram.locations.editlive", 33: "telegram.locations.send",
		34: "telegram.locations.stoplive", 35: "telegram.mediagroups.send", 36: "telegram.members.ban",
		37: "telegram.members.promote", 38: "telegram.members.restrict", 39: "telegram.members.setadmintitle",
		40: "telegram.members.settag", 41: "telegram.members.unban", 42: "telegram.menubutton.get",
		43: "telegram.menubutton.set", 44: "telegram.messages.copy", 45: "telegram.messages.delete",
		46: "telegram.messages.deletemany", 47: "telegram.messages.edit", 48: "telegram.messages.editcaption",
		49: "telegram.messages.editmedia", 50: "telegram.messages.editreplymarkup",
		51: "telegram.messages.forward", 52: "telegram.messages.send", 53: "telegram.photos.send",
		54: "telegram.pins.pin", 55: "telegram.pins.unpin", 56: "telegram.pins.unpinall",
		57: "telegram.polls.send", 58: "telegram.polls.stop", 59: "telegram.profile.get",
		60: "telegram.profile.removephoto", 61: "telegram.profile.setdescription", 62: "telegram.profile.setname",
		63: "telegram.profile.setphoto", 64: "telegram.profile.setshortdescription",
		65: "telegram.reactions.remove", 66: "telegram.reactions.removeall", 67: "telegram.reactions.set",
		68: "telegram.senderchats.ban", 69: "telegram.senderchats.unban", 70: "telegram.stickers.customemoji",
		71: "telegram.stickers.delete", 72: "telegram.stickers.replace", 73: "telegram.stickers.send",
		74: "telegram.stickers.setemojilist", 75: "telegram.stickers.setkeywords",
		76: "telegram.stickers.setmaskposition", 77: "telegram.stickers.setposition",
		78: "telegram.stickers.uploadfile", 79: "telegram.stickersets.addsticker",
		80: "telegram.stickersets.create", 81: "telegram.stickersets.delete", 82: "telegram.stickersets.get",
		83: "telegram.stickersets.setcustomemojithumbnail", 84: "telegram.stickersets.setthumbnail",
		85: "telegram.stickersets.settitle", 86: "telegram.suggestedposts.approve",
		87: "telegram.suggestedposts.decline", 88: "telegram.topics.close", 89: "telegram.topics.create",
		90: "telegram.topics.delete", 91: "telegram.topics.edit", 92: "telegram.topics.hidegeneral",
		93: "telegram.topics.iconstickers", 94: "telegram.topics.reopen", 95: "telegram.topics.unhidegeneral",
		96: "telegram.topics.unpinall", 97: "telegram.updates.confirm", 98: "telegram.updates.list",
		99: "telegram.venues.send", 100: "telegram.videonotes.send", 101: "telegram.videos.send",
		102: "telegram.voice.send", 103: "telegram.webhook.get",
	}
	ok2 := len(operations) == 104
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
