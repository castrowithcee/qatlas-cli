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
		3: "telegram.business.delete-messages", 4: "telegram.business.get", 5: "telegram.business.gifts",
		6: "telegram.business.mark-read", 7: "telegram.business.remove-photo", 8: "telegram.business.set-bio",
		9: "telegram.business.set-name", 10: "telegram.business.set-photo", 11: "telegram.business.set-username",
		12: "telegram.business.star-balance", 13: "telegram.callbacks.answer", 14: "telegram.chat-actions.send",
		15: "telegram.chats.administrators", 16: "telegram.chats.delete-photo",
		17: "telegram.chats.delete-sticker-set", 18: "telegram.chats.get", 19: "telegram.chats.leave",
		20: "telegram.chats.member", 21: "telegram.chats.member-count", 22: "telegram.chats.set-description",
		23: "telegram.chats.set-permissions", 24: "telegram.chats.set-photo", 25: "telegram.chats.set-sticker-set",
		26: "telegram.chats.set-title", 27: "telegram.checklists.edit", 28: "telegram.checklists.send",
		29: "telegram.commands.delete", 30: "telegram.commands.get", 31: "telegram.commands.set",
		32: "telegram.contacts.send", 33: "telegram.default-admin-rights.get",
		34: "telegram.default-admin-rights.set", 35: "telegram.dice.send", 36: "telegram.documents.send",
		37: "telegram.files.download", 38: "telegram.files.get", 39: "telegram.games.high-scores",
		40: "telegram.games.send", 41: "telegram.games.set-score", 42: "telegram.gifts.available",
		43: "telegram.gifts.chat", 44: "telegram.gifts.user", 45: "telegram.inline.answer",
		46: "telegram.invite-links.primary", 47: "telegram.invite-links.revoke",
		48: "telegram.join-request-queries.answer", 49: "telegram.join-requests.decline",
		50: "telegram.live-photos.send", 51: "telegram.locations.edit-live", 52: "telegram.locations.send",
		53: "telegram.locations.stop-live", 54: "telegram.media-groups.send", 55: "telegram.members.ban",
		56: "telegram.members.promote", 57: "telegram.members.restrict", 58: "telegram.members.set-admin-title",
		59: "telegram.members.set-tag", 60: "telegram.members.unban", 61: "telegram.menu-button.get",
		62: "telegram.menu-button.set", 63: "telegram.messages.copy", 64: "telegram.messages.delete",
		65: "telegram.messages.delete-many", 66: "telegram.messages.edit", 67: "telegram.messages.edit-caption",
		68: "telegram.messages.edit-media", 69: "telegram.messages.edit-reply-markup",
		70: "telegram.messages.forward", 71: "telegram.messages.send", 72: "telegram.payments.star-balance",
		73: "telegram.payments.star-transactions", 74: "telegram.photos.send", 75: "telegram.pins.pin",
		76: "telegram.pins.unpin", 77: "telegram.pins.unpin-all", 78: "telegram.polls.send",
		79: "telegram.polls.stop", 80: "telegram.profile.get", 81: "telegram.profile.remove-photo",
		82: "telegram.profile.set-description", 83: "telegram.profile.set-name", 84: "telegram.profile.set-photo",
		85: "telegram.profile.set-short-description", 86: "telegram.reactions.remove",
		87: "telegram.reactions.remove-all", 88: "telegram.reactions.set", 89: "telegram.rich-messages.send",
		90: "telegram.sender-chats.ban", 91: "telegram.sender-chats.unban", 92: "telegram.sticker-sets.add-sticker",
		93: "telegram.sticker-sets.create", 94: "telegram.sticker-sets.delete", 95: "telegram.sticker-sets.get",
		96: "telegram.sticker-sets.set-custom-emoji-thumbnail", 97: "telegram.sticker-sets.set-thumbnail",
		98: "telegram.sticker-sets.set-title", 99: "telegram.stickers.custom-emoji", 100: "telegram.stickers.delete",
		101: "telegram.stickers.replace", 102: "telegram.stickers.send", 103: "telegram.stickers.set-emoji-list",
		104: "telegram.stickers.set-keywords", 105: "telegram.stickers.set-mask-position",
		106: "telegram.stickers.set-position", 107: "telegram.stickers.upload-file", 108: "telegram.stories.delete",
		109: "telegram.stories.edit", 110: "telegram.stories.post", 111: "telegram.stories.repost",
		112: "telegram.suggested-posts.approve", 113: "telegram.suggested-posts.decline",
		114: "telegram.topics.close", 115: "telegram.topics.create", 116: "telegram.topics.delete",
		117: "telegram.topics.edit", 118: "telegram.topics.hide-general", 119: "telegram.topics.icon-stickers",
		120: "telegram.topics.reopen", 121: "telegram.topics.unhide-general", 122: "telegram.topics.unpin-all",
		123: "telegram.updates.confirm", 124: "telegram.updates.list", 125: "telegram.users.chat-boosts",
		126: "telegram.users.personal-chat-messages", 127: "telegram.users.profile-audios",
		128: "telegram.users.profile-photos", 129: "telegram.venues.send", 130: "telegram.video-notes.send",
		131: "telegram.videos.send", 132: "telegram.voice.send", 133: "telegram.webhook.get",
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

// The Infomaniak providers are listed under their current ID only; a former ID resolves through a rename and
// is not a namespace of its own.
func TestProvidersListsVendorFamilyProvidersUnderTheirCurrentIDs(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	path := writeConfig(t, "version: 2\ndefaults: {}\n")
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
	for current, former := range map[string]string{"infomaniak.chat": "infomaniakchat", "infomaniak.drive": "infomaniakdrive", "infomaniak.mail": "infomaniakmail", "infomaniak.dav": "infomaniakdav", "seatable.account": "seatableaccount"} {
		var hasCurrent, hasFormer bool
		for _, p := range listed.Providers {
			hasCurrent = hasCurrent || p.Provider == current
			hasFormer = hasFormer || p.Provider == former
		}
		if !hasCurrent || hasFormer {
			t.Errorf("providers = %+v, want %s and not %s", listed.Providers, current, former)
		}
	}
}
