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
		24: "telegram.documents.send", 25: "telegram.files.download", 26: "telegram.files.get", 27: "telegram.inline.answer",
		28: "telegram.invitelinks.primary", 29: "telegram.invitelinks.revoke",
		30: "telegram.joinrequestqueries.answer", 31: "telegram.joinrequests.decline",
		32: "telegram.livephotos.send", 33: "telegram.locations.editlive", 34: "telegram.locations.send",
		35: "telegram.locations.stoplive", 36: "telegram.mediagroups.send", 37: "telegram.members.ban",
		38: "telegram.members.promote", 39: "telegram.members.restrict", 40: "telegram.members.setadmintitle",
		41: "telegram.members.settag", 42: "telegram.members.unban", 43: "telegram.menubutton.get",
		44: "telegram.menubutton.set", 45: "telegram.messages.copy", 46: "telegram.messages.delete",
		47: "telegram.messages.deletemany", 48: "telegram.messages.edit", 49: "telegram.messages.editcaption",
		50: "telegram.messages.editmedia", 51: "telegram.messages.editreplymarkup",
		52: "telegram.messages.forward", 53: "telegram.messages.send", 54: "telegram.photos.send",
		55: "telegram.pins.pin", 56: "telegram.pins.unpin", 57: "telegram.pins.unpinall",
		58: "telegram.polls.send", 59: "telegram.polls.stop", 60: "telegram.profile.get",
		61: "telegram.profile.removephoto", 62: "telegram.profile.setdescription", 63: "telegram.profile.setname",
		64: "telegram.profile.setphoto", 65: "telegram.profile.setshortdescription",
		66: "telegram.reactions.remove", 67: "telegram.reactions.removeall", 68: "telegram.reactions.set",
		69: "telegram.senderchats.ban", 70: "telegram.senderchats.unban", 71: "telegram.stickers.customemoji",
		72: "telegram.stickers.delete", 73: "telegram.stickers.replace", 74: "telegram.stickers.send",
		75: "telegram.stickers.setemojilist", 76: "telegram.stickers.setkeywords",
		77: "telegram.stickers.setmaskposition", 78: "telegram.stickers.setposition",
		79: "telegram.stickers.uploadfile", 80: "telegram.stickersets.addsticker",
		81: "telegram.stickersets.create", 82: "telegram.stickersets.delete", 83: "telegram.stickersets.get",
		84: "telegram.stickersets.setcustomemojithumbnail", 85: "telegram.stickersets.setthumbnail",
		86: "telegram.stickersets.settitle", 87: "telegram.suggestedposts.approve",
		88: "telegram.suggestedposts.decline", 89: "telegram.topics.close", 90: "telegram.topics.create",
		91: "telegram.topics.delete", 92: "telegram.topics.edit", 93: "telegram.topics.hidegeneral",
		94: "telegram.topics.iconstickers", 95: "telegram.topics.reopen", 96: "telegram.topics.unhidegeneral",
		97: "telegram.topics.unpinall", 98: "telegram.updates.confirm", 99: "telegram.updates.list",
		100: "telegram.venues.send", 101: "telegram.videonotes.send", 102: "telegram.videos.send",
		103: "telegram.voice.send", 104: "telegram.webhook.get",
	}
	ok2 := len(operations) == 105
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
