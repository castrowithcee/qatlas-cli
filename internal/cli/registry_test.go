package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
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
		41: "telegram.games.setscore", 42: "telegram.gifts.available", 43: "telegram.gifts.chat",
		44: "telegram.gifts.user", 45: "telegram.inline.answer", 46: "telegram.invitelinks.primary",
		47: "telegram.invitelinks.revoke", 48: "telegram.joinrequestqueries.answer",
		49: "telegram.joinrequests.decline", 50: "telegram.livephotos.send", 51: "telegram.locations.editlive",
		52: "telegram.locations.send", 53: "telegram.locations.stoplive", 54: "telegram.mediagroups.send",
		55: "telegram.members.ban", 56: "telegram.members.promote", 57: "telegram.members.restrict",
		58: "telegram.members.setadmintitle", 59: "telegram.members.settag", 60: "telegram.members.unban",
		61: "telegram.menubutton.get", 62: "telegram.menubutton.set", 63: "telegram.messages.copy",
		64: "telegram.messages.delete", 65: "telegram.messages.deletemany", 66: "telegram.messages.edit",
		67: "telegram.messages.editcaption", 68: "telegram.messages.editmedia",
		69: "telegram.messages.editreplymarkup", 70: "telegram.messages.forward", 71: "telegram.messages.send",
		72: "telegram.payments.starbalance", 73: "telegram.payments.startransactions", 74: "telegram.photos.send",
		75: "telegram.pins.pin", 76: "telegram.pins.unpin", 77: "telegram.pins.unpinall", 78: "telegram.polls.send",
		79: "telegram.polls.stop", 80: "telegram.profile.get", 81: "telegram.profile.removephoto",
		82: "telegram.profile.setdescription", 83: "telegram.profile.setname", 84: "telegram.profile.setphoto",
		85: "telegram.profile.setshortdescription", 86: "telegram.reactions.remove",
		87: "telegram.reactions.removeall", 88: "telegram.reactions.set", 89: "telegram.richmessages.send",
		90: "telegram.senderchats.ban", 91: "telegram.senderchats.unban", 92: "telegram.stickers.customemoji",
		93: "telegram.stickers.delete", 94: "telegram.stickers.replace", 95: "telegram.stickers.send",
		96: "telegram.stickers.setemojilist", 97: "telegram.stickers.setkeywords",
		98: "telegram.stickers.setmaskposition", 99: "telegram.stickers.setposition",
		100: "telegram.stickers.uploadfile", 101: "telegram.stickersets.addsticker",
		102: "telegram.stickersets.create", 103: "telegram.stickersets.delete", 104: "telegram.stickersets.get",
		105: "telegram.stickersets.setcustomemojithumbnail", 106: "telegram.stickersets.setthumbnail",
		107: "telegram.stickersets.settitle", 108: "telegram.stories.delete", 109: "telegram.stories.edit",
		110: "telegram.stories.post", 111: "telegram.stories.repost", 112: "telegram.suggestedposts.approve",
		113: "telegram.suggestedposts.decline", 114: "telegram.topics.close", 115: "telegram.topics.create",
		116: "telegram.topics.delete", 117: "telegram.topics.edit", 118: "telegram.topics.hidegeneral",
		119: "telegram.topics.iconstickers", 120: "telegram.topics.reopen", 121: "telegram.topics.unhidegeneral",
		122: "telegram.topics.unpinall", 123: "telegram.updates.confirm", 124: "telegram.updates.list",
		125: "telegram.users.chatboosts", 126: "telegram.users.personalchatmessages",
		127: "telegram.users.profileaudios", 128: "telegram.users.profilephotos", 129: "telegram.venues.send",
		130: "telegram.videonotes.send", 131: "telegram.videos.send", 132: "telegram.voice.send",
		133: "telegram.webhook.get",
	}
	ok2 := len(operations) == 134
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

// The kChat provider is listed under its current ID only; the former ID resolves through a rename and is not
// a namespace of its own.
func TestProvidersListsKChatUnderItsCurrentID(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	path := writeConfig(t, "version: 1\ndefaults: {}\n")
	var reads atomic.Int32
	code, stdout, stderr := runTools(t, &reads, "providers", "--config", path, "--output", "json")
	if code != exitOK || stderr != "" {
		t.Fatalf("providers: exit=%d stderr=%q", code, stderr)
	}
	var listed struct {
		Providers []struct {
			Provider string `json:"provider"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatal(err)
	}
	var current, former bool
	for _, p := range listed.Providers {
		current = current || p.Provider == "infomaniak.chat"
		former = former || p.Provider == "infomaniakchat"
	}
	if !current || former {
		t.Errorf("providers = %+v, want infomaniak.chat and not infomaniakchat", listed.Providers)
	}
}
