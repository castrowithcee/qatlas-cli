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
		15: "telegram.chats.settitle", 16: "telegram.contacts.send", 17: "telegram.dice.send",
		18: "telegram.documents.send", 19: "telegram.files.download", 20: "telegram.files.get",
		21: "telegram.invitelinks.primary", 22: "telegram.invitelinks.revoke", 23: "telegram.livephotos.send",
		24: "telegram.locations.editlive", 25: "telegram.locations.send", 26: "telegram.locations.stoplive",
		27: "telegram.mediagroups.send", 28: "telegram.members.ban", 29: "telegram.members.promote",
		30: "telegram.members.restrict", 31: "telegram.members.setadmintitle", 32: "telegram.members.settag",
		33: "telegram.members.unban", 34: "telegram.messages.copy", 35: "telegram.messages.delete",
		36: "telegram.messages.deletemany", 37: "telegram.messages.edit", 38: "telegram.messages.editcaption",
		39: "telegram.messages.editmedia", 40: "telegram.messages.editreplymarkup",
		41: "telegram.messages.forward", 42: "telegram.messages.send", 43: "telegram.photos.send",
		44: "telegram.pins.pin", 45: "telegram.pins.unpin", 46: "telegram.pins.unpinall",
		47: "telegram.polls.send", 48: "telegram.polls.stop", 49: "telegram.reactions.remove",
		50: "telegram.reactions.removeall", 51: "telegram.reactions.set", 52: "telegram.senderchats.ban",
		53: "telegram.senderchats.unban", 54: "telegram.stickers.customemoji", 55: "telegram.stickers.delete",
		56: "telegram.stickers.replace", 57: "telegram.stickers.send", 58: "telegram.stickers.setemojilist",
		59: "telegram.stickers.setkeywords", 60: "telegram.stickers.setmaskposition",
		61: "telegram.stickers.setposition", 62: "telegram.stickers.uploadfile",
		63: "telegram.stickersets.addsticker", 64: "telegram.stickersets.create",
		65: "telegram.stickersets.delete", 66: "telegram.stickersets.get",
		67: "telegram.stickersets.setcustomemojithumbnail", 68: "telegram.stickersets.setthumbnail",
		69: "telegram.stickersets.settitle", 70: "telegram.topics.close", 71: "telegram.topics.create",
		72: "telegram.topics.delete", 73: "telegram.topics.edit", 74: "telegram.topics.hidegeneral",
		75: "telegram.topics.iconstickers", 76: "telegram.topics.reopen", 77: "telegram.topics.unhidegeneral",
		78: "telegram.topics.unpinall", 79: "telegram.updates.confirm", 80: "telegram.updates.list",
		81: "telegram.venues.send", 82: "telegram.videonotes.send", 83: "telegram.videos.send",
		84: "telegram.voice.send", 85: "telegram.webhook.get",
	}
	ok2 := len(operations) == 86
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
