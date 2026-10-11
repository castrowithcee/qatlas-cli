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
		3: "telegram.chatactions.send", 4: "telegram.chats.administrators", 5: "telegram.chats.deletephoto",
		6: "telegram.chats.deletestickerset", 7: "telegram.chats.get", 8: "telegram.chats.leave",
		9: "telegram.chats.member", 10: "telegram.chats.membercount", 11: "telegram.chats.setdescription",
		12: "telegram.chats.setpermissions", 13: "telegram.chats.setphoto", 14: "telegram.chats.setstickerset",
		15: "telegram.chats.settitle", 16: "telegram.commands.delete", 17: "telegram.commands.get",
		18: "telegram.commands.set", 19: "telegram.contacts.send", 20: "telegram.defaultadminrights.get",
		21: "telegram.defaultadminrights.set", 22: "telegram.dice.send", 23: "telegram.documents.send",
		24: "telegram.files.download", 25: "telegram.files.get", 26: "telegram.invitelinks.primary",
		27: "telegram.invitelinks.revoke", 28: "telegram.joinrequests.decline", 29: "telegram.livephotos.send",
		30: "telegram.locations.editlive", 31: "telegram.locations.send", 32: "telegram.locations.stoplive",
		33: "telegram.mediagroups.send", 34: "telegram.members.ban", 35: "telegram.members.promote",
		36: "telegram.members.restrict", 37: "telegram.members.setadmintitle", 38: "telegram.members.settag",
		39: "telegram.members.unban", 40: "telegram.menubutton.get", 41: "telegram.menubutton.set",
		42: "telegram.messages.copy", 43: "telegram.messages.delete", 44: "telegram.messages.deletemany",
		45: "telegram.messages.edit", 46: "telegram.messages.editcaption", 47: "telegram.messages.editmedia",
		48: "telegram.messages.editreplymarkup", 49: "telegram.messages.forward", 50: "telegram.messages.send",
		51: "telegram.photos.send", 52: "telegram.pins.pin", 53: "telegram.pins.unpin", 54: "telegram.pins.unpinall",
		55: "telegram.polls.send", 56: "telegram.polls.stop", 57: "telegram.profile.removephoto",
		58: "telegram.profile.setphoto", 59: "telegram.reactions.remove", 60: "telegram.reactions.removeall",
		61: "telegram.reactions.set", 62: "telegram.senderchats.ban", 63: "telegram.senderchats.unban",
		64: "telegram.stickers.customemoji", 65: "telegram.stickers.delete", 66: "telegram.stickers.replace",
		67: "telegram.stickers.send", 68: "telegram.stickers.setemojilist", 69: "telegram.stickers.setkeywords",
		70: "telegram.stickers.setmaskposition", 71: "telegram.stickers.setposition",
		72: "telegram.stickers.uploadfile", 73: "telegram.stickersets.addsticker", 74: "telegram.stickersets.create",
		75: "telegram.stickersets.delete", 76: "telegram.stickersets.get",
		77: "telegram.stickersets.setcustomemojithumbnail", 78: "telegram.stickersets.setthumbnail",
		79: "telegram.stickersets.settitle", 80: "telegram.suggestedposts.approve",
		81: "telegram.suggestedposts.decline", 82: "telegram.topics.close", 83: "telegram.topics.create",
		84: "telegram.topics.delete", 85: "telegram.topics.edit", 86: "telegram.topics.hidegeneral",
		87: "telegram.topics.iconstickers", 88: "telegram.topics.reopen", 89: "telegram.topics.unhidegeneral",
		90: "telegram.topics.unpinall", 91: "telegram.updates.confirm", 92: "telegram.updates.list",
		93: "telegram.venues.send", 94: "telegram.videonotes.send", 95: "telegram.videos.send",
		96: "telegram.voice.send", 97: "telegram.webhook.get",
	}
	ok2 := len(operations) == 98
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
