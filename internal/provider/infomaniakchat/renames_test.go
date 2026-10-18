package infomaniakchat

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of kChat tool IDs.
var currentToolIDs = []string{
	"infomaniak.chat.archived-channels.list",
	"infomaniak.chat.bots.disable",
	"infomaniak.chat.bots.enable",
	"infomaniak.chat.bots.get",
	"infomaniak.chat.bots.list",
	"infomaniak.chat.bots.update",
	"infomaniak.chat.categories.create",
	"infomaniak.chat.categories.delete",
	"infomaniak.chat.categories.list",
	"infomaniak.chat.categories.update",
	"infomaniak.chat.channel-members.add",
	"infomaniak.chat.channel-members.list",
	"infomaniak.chat.channel-members.remove",
	"infomaniak.chat.channel-members.roles",
	"infomaniak.chat.channel-notifications.update",
	"infomaniak.chat.channels.archive",
	"infomaniak.chat.channels.browse",
	"infomaniak.chat.channels.create",
	"infomaniak.chat.channels.get",
	"infomaniak.chat.channels.list",
	"infomaniak.chat.channels.mark-read",
	"infomaniak.chat.channels.privacy",
	"infomaniak.chat.channels.restore",
	"infomaniak.chat.channels.search",
	"infomaniak.chat.channels.unread",
	"infomaniak.chat.channels.update",
	"infomaniak.chat.commands.delete",
	"infomaniak.chat.commands.get",
	"infomaniak.chat.commands.list",
	"infomaniak.chat.commands.update",
	"infomaniak.chat.custom-status.clear",
	"infomaniak.chat.custom-status.set",
	"infomaniak.chat.direct.list",
	"infomaniak.chat.direct.open",
	"infomaniak.chat.emoji.create",
	"infomaniak.chat.emoji.delete",
	"infomaniak.chat.emoji.list",
	"infomaniak.chat.files.download",
	"infomaniak.chat.files.info",
	"infomaniak.chat.files.search",
	"infomaniak.chat.files.upload",
	"infomaniak.chat.flagged.list",
	"infomaniak.chat.group-messages.open",
	"infomaniak.chat.incoming-webhooks.delete",
	"infomaniak.chat.incoming-webhooks.get",
	"infomaniak.chat.incoming-webhooks.list",
	"infomaniak.chat.incoming-webhooks.update",
	"infomaniak.chat.insights.top",
	"infomaniak.chat.messages.delete",
	"infomaniak.chat.messages.files",
	"infomaniak.chat.messages.flag",
	"infomaniak.chat.messages.get",
	"infomaniak.chat.messages.list",
	"infomaniak.chat.messages.mark-unread",
	"infomaniak.chat.messages.pin",
	"infomaniak.chat.messages.remind",
	"infomaniak.chat.messages.search",
	"infomaniak.chat.messages.send",
	"infomaniak.chat.messages.thread",
	"infomaniak.chat.messages.unflag",
	"infomaniak.chat.messages.unpin",
	"infomaniak.chat.messages.update",
	"infomaniak.chat.outgoing-webhooks.delete",
	"infomaniak.chat.outgoing-webhooks.get",
	"infomaniak.chat.outgoing-webhooks.list",
	"infomaniak.chat.outgoing-webhooks.update",
	"infomaniak.chat.pins.list",
	"infomaniak.chat.profile.update",
	"infomaniak.chat.reactions.add",
	"infomaniak.chat.reactions.list",
	"infomaniak.chat.reactions.remove",
	"infomaniak.chat.roles.get",
	"infomaniak.chat.status.set",
	"infomaniak.chat.team-members.list",
	"infomaniak.chat.team-members.roles",
	"infomaniak.chat.teams.get",
	"infomaniak.chat.teams.list",
	"infomaniak.chat.threads.follow",
	"infomaniak.chat.threads.get",
	"infomaniak.chat.threads.list",
	"infomaniak.chat.threads.mark-read",
	"infomaniak.chat.threads.unfollow",
	"infomaniak.chat.user-groups.list",
	"infomaniak.chat.users.get",
	"infomaniak.chat.users.list",
	"infomaniak.chat.users.search",
	"infomaniak.chat.users.status",
}

func TestRegistryHoldsExactlyTheCurrentToolIDs(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range reg.All() {
		got = append(got, d.ID)
	}
	sort.Strings(got)
	want := append([]string(nil), currentToolIDs...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 87 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 87 {
		t.Errorf("renamedTools has %d entries, want 87", len(renamedTools))
	}
	current := map[string]bool{}
	for _, id := range currentToolIDs {
		current[id] = true
	}
	targets := map[string]bool{}
	for old, next := range renamedTools {
		if current[old] || !current[next] {
			t.Errorf("rename %s -> %s: old must be gone and new registered", old, next)
		}
		if !strings.HasPrefix(old, "infomaniakchat.") {
			t.Errorf("former ID %s does not carry the former provider ID", old)
		}
		if strings.ReplaceAll(strings.TrimPrefix(next, "infomaniak.chat."), "-", "") !=
			strings.TrimPrefix(old, "infomaniakchat.") {
			t.Errorf("rename %s -> %s changes more than the provider ID and word boundaries", old, next)
		}
		targets[next] = true
	}
	// Every current ID is the target of exactly one rename.
	for id := range current {
		if !targets[id] {
			t.Errorf("tool %s has no former ID", id)
		}
	}
}

func TestFormerKChatProviderAndToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  chat:
    provider: infomaniakchat
    base_url: https://team.kchat.infomaniak.com
credentials:
  chat-token:
    provider: infomaniakchat
    type: env
    values:
      token: QATLAS_KCHAT_TOKEN
connections:
  team:
    service: chat
    credential: chat-token
    targets: [team/` + teamA + `]
    permissions: [read, update]
    tools: [infomaniakchat.channelmembers.list, infomaniakchat.channels.markread, infomaniakchat.usergroups.list]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range map[string]string{
		"infomaniakchat.channelmembers.list": "infomaniak.chat.channel-members.list",
		"infomaniakchat.channels.markread":   "infomaniak.chat.channels.mark-read",
		"infomaniakchat.usergroups.list":     "infomaniak.chat.user-groups.list",
	} {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
	if !strings.Contains(warnings, "infomaniakchat") || !strings.Contains(warnings, `"infomaniak.chat"`) {
		t.Errorf("warnings lack the provider rename:\n%s", warnings)
	}
}
