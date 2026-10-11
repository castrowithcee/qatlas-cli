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
		21: "telegram.invitelinks.primary", 22: "telegram.invitelinks.revoke", 23: "telegram.joinrequests.decline",
		24: "telegram.livephotos.send", 25: "telegram.locations.editlive", 26: "telegram.locations.send",
		27: "telegram.locations.stoplive", 28: "telegram.mediagroups.send", 29: "telegram.members.ban",
		30: "telegram.members.promote", 31: "telegram.members.restrict", 32: "telegram.members.setadmintitle",
		33: "telegram.members.settag", 34: "telegram.members.unban", 35: "telegram.messages.copy",
		36: "telegram.messages.delete", 37: "telegram.messages.deletemany", 38: "telegram.messages.edit",
		39: "telegram.messages.editcaption", 40: "telegram.messages.editmedia",
		41: "telegram.messages.editreplymarkup", 42: "telegram.messages.forward", 43: "telegram.messages.send",
		44: "telegram.photos.send", 45: "telegram.pins.pin", 46: "telegram.pins.unpin", 47: "telegram.pins.unpinall",
		48: "telegram.polls.send", 49: "telegram.polls.stop", 50: "telegram.profile.get",
		51: "telegram.profile.setdescription", 52: "telegram.profile.setname",
		53: "telegram.profile.setshortdescription", 54: "telegram.reactions.remove",
		55: "telegram.reactions.removeall", 56: "telegram.reactions.set", 57: "telegram.senderchats.ban",
		58: "telegram.senderchats.unban", 59: "telegram.stickers.customemoji", 60: "telegram.stickers.delete",
		61: "telegram.stickers.replace", 62: "telegram.stickers.send", 63: "telegram.stickers.setemojilist",
		64: "telegram.stickers.setkeywords", 65: "telegram.stickers.setmaskposition",
		66: "telegram.stickers.setposition", 67: "telegram.stickers.uploadfile",
		68: "telegram.stickersets.addsticker", 69: "telegram.stickersets.create", 70: "telegram.stickersets.delete",
		71: "telegram.stickersets.get", 72: "telegram.stickersets.setcustomemojithumbnail",
		73: "telegram.stickersets.setthumbnail", 74: "telegram.stickersets.settitle",
		75: "telegram.suggestedposts.approve", 76: "telegram.suggestedposts.decline", 77: "telegram.topics.close",
		78: "telegram.topics.create", 79: "telegram.topics.delete", 80: "telegram.topics.edit",
		81: "telegram.topics.hidegeneral", 82: "telegram.topics.iconstickers", 83: "telegram.topics.reopen",
		84: "telegram.topics.unhidegeneral", 85: "telegram.topics.unpinall", 86: "telegram.updates.confirm",
		87: "telegram.updates.list", 88: "telegram.venues.send", 89: "telegram.videonotes.send",
		90: "telegram.videos.send", 91: "telegram.voice.send", 92: "telegram.webhook.get",
	}
	ok2 := len(operations) == 93
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
