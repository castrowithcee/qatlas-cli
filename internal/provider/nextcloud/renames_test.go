package nextcloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

func renameRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestToolIDsHaveServiceLevel(t *testing.T) {
	reg := renameRegistry(t)
	all := reg.All()
	if len(all) != len(wantToolIDs) {
		t.Fatalf("tools = %d, want %d", len(all), len(wantToolIDs))
	}
	for i, d := range all {
		if d.ID != wantToolIDs[i] {
			t.Errorf("tool %d = %s, want %s", i, d.ID, wantToolIDs[i])
		}
	}
	for _, d := range all {
		segments := strings.Split(d.ID, ".")
		if len(segments) != 4 || segments[1] != d.Group {
			t.Errorf("%s: want four segments with the group %q as the second", d.ID, d.Group)
		}
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	reg := renameRegistry(t)
	if err := reg.ValidateRenames(); err != nil {
		t.Fatal(err)
	}
	current := map[string]bool{}
	for _, d := range reg.All() {
		current[d.ID] = true
	}
	if len(renamedTools) != 144 {
		t.Errorf("renamedTools has %d entries, want 144", len(renamedTools))
	}
	covered := map[string]bool{}
	for old, now := range renamedTools {
		if current[old] || !current[now] {
			t.Errorf("%s -> %s: old must be gone and new registered", old, now)
		}
		covered[now] = true
	}
	for id := range current {
		if !covered[id] {
			t.Errorf("%s is not the target of any rename", id)
		}
	}
	for old, want := range map[string]string{
		"nextcloud.files.list":               "nextcloud.files.files.list",
		"nextcloud.deckcards.get":            "nextcloud.deck.cards.get",
		"nextcloud.addressbooks.share":       "nextcloud.contacts.address-books.share",
		"nextcloud.notifications.dismissall": "nextcloud.account.notifications.dismiss-all",
		"nextcloud.talkrooms.markread":       "nextcloud.talk.rooms.mark-read",
		"nextcloud.filetags.add":             "nextcloud.files.file-tags.add",
	} {
		if got := renamedTools[old]; got != want {
			t.Errorf("renamedTools[%s] = %q, want %q", old, got, want)
		}
	}
}

func TestConfigWithFormerIDsLoadsWithRenameWarnings(t *testing.T) {
	const text = `version: 1
services:
  svc:
    provider: nextcloud
    base_url: https://cloud.example.invalid
credentials:
  cred:
    type: env
    provider: nextcloud
    values:
      user-id: NC_USER
      app-password: NC_PASS
connections:
  conn:
    service: svc
    credential: cred
    targets: [folder]
    tools: [nextcloud.files.list, nextcloud.folders.create]
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, renameRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(cfg.Connections["conn"].Tools, ",")
	if got != "nextcloud.files.files.list,nextcloud.files.folders.create" {
		t.Errorf("tools = %s", got)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for _, want := range []string{
		`tool "nextcloud.files.list" was renamed to "nextcloud.files.files.list"`,
		`tool "nextcloud.folders.create" was renamed to "nextcloud.files.folders.create"`,
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q:\n%s", want, warnings)
		}
	}
}

// wantToolIDs are the registered tool IDs in registry order (sorted).
var wantToolIDs = []string{
	"nextcloud.account.account.get",
	"nextcloud.account.activity.list",
	"nextcloud.account.notifications.dismiss",
	"nextcloud.account.notifications.dismiss-all",
	"nextcloud.account.notifications.list",
	"nextcloud.account.search.providers",
	"nextcloud.account.search.query",
	"nextcloud.account.status.clear",
	"nextcloud.account.status.get",
	"nextcloud.account.status.set",
	"nextcloud.admin.groups.list",
	"nextcloud.admin.groups.members",
	"nextcloud.admin.users.get",
	"nextcloud.admin.users.list",
	"nextcloud.calendar.calendars.create",
	"nextcloud.calendar.calendars.delete",
	"nextcloud.calendar.calendars.list",
	"nextcloud.calendar.calendars.share",
	"nextcloud.calendar.calendars.unshare",
	"nextcloud.calendar.calendars.update",
	"nextcloud.calendar.events.create",
	"nextcloud.calendar.events.delete",
	"nextcloud.calendar.events.get",
	"nextcloud.calendar.events.list",
	"nextcloud.calendar.events.update",
	"nextcloud.calendar.tasks.create",
	"nextcloud.calendar.tasks.delete",
	"nextcloud.calendar.tasks.get",
	"nextcloud.calendar.tasks.list",
	"nextcloud.calendar.tasks.update",
	"nextcloud.calendar.trash.delete",
	"nextcloud.calendar.trash.list",
	"nextcloud.calendar.trash.restore",
	"nextcloud.contacts.address-books.create",
	"nextcloud.contacts.address-books.delete",
	"nextcloud.contacts.address-books.list",
	"nextcloud.contacts.address-books.share",
	"nextcloud.contacts.address-books.unshare",
	"nextcloud.contacts.address-books.update",
	"nextcloud.contacts.contacts.create",
	"nextcloud.contacts.contacts.delete",
	"nextcloud.contacts.contacts.get",
	"nextcloud.contacts.contacts.list",
	"nextcloud.contacts.contacts.update",
	"nextcloud.deck.acl.add",
	"nextcloud.deck.acl.remove",
	"nextcloud.deck.acl.update",
	"nextcloud.deck.attachments.delete",
	"nextcloud.deck.attachments.get",
	"nextcloud.deck.attachments.list",
	"nextcloud.deck.attachments.upload",
	"nextcloud.deck.boards.create",
	"nextcloud.deck.boards.delete",
	"nextcloud.deck.boards.get",
	"nextcloud.deck.boards.list",
	"nextcloud.deck.boards.update",
	"nextcloud.deck.cards.assignees",
	"nextcloud.deck.cards.create",
	"nextcloud.deck.cards.delete",
	"nextcloud.deck.cards.get",
	"nextcloud.deck.cards.labels",
	"nextcloud.deck.cards.move",
	"nextcloud.deck.cards.update",
	"nextcloud.deck.comments.create",
	"nextcloud.deck.comments.delete",
	"nextcloud.deck.comments.list",
	"nextcloud.deck.comments.update",
	"nextcloud.deck.labels.create",
	"nextcloud.deck.labels.delete",
	"nextcloud.deck.labels.update",
	"nextcloud.deck.stacks.create",
	"nextcloud.deck.stacks.delete",
	"nextcloud.deck.stacks.list",
	"nextcloud.deck.stacks.update",
	"nextcloud.files.comments.create",
	"nextcloud.files.comments.delete",
	"nextcloud.files.comments.list",
	"nextcloud.files.comments.update",
	"nextcloud.files.favorites.list",
	"nextcloud.files.file-tags.add",
	"nextcloud.files.file-tags.list",
	"nextcloud.files.file-tags.remove",
	"nextcloud.files.files.copy",
	"nextcloud.files.files.create",
	"nextcloud.files.files.delete",
	"nextcloud.files.files.favorite",
	"nextcloud.files.files.get",
	"nextcloud.files.files.list",
	"nextcloud.files.files.move",
	"nextcloud.files.files.search",
	"nextcloud.files.files.stat",
	"nextcloud.files.files.update",
	"nextcloud.files.folders.create",
	"nextcloud.files.folders.delete",
	"nextcloud.files.system-tags.create",
	"nextcloud.files.system-tags.delete",
	"nextcloud.files.system-tags.list",
	"nextcloud.files.system-tags.update",
	"nextcloud.files.trash.delete",
	"nextcloud.files.trash.list",
	"nextcloud.files.trash.restore",
	"nextcloud.files.versions.get",
	"nextcloud.files.versions.list",
	"nextcloud.files.versions.restore",
	"nextcloud.notes.attachments.get",
	"nextcloud.notes.attachments.upload",
	"nextcloud.notes.notes.create",
	"nextcloud.notes.notes.delete",
	"nextcloud.notes.notes.get",
	"nextcloud.notes.notes.list",
	"nextcloud.notes.notes.update",
	"nextcloud.notes.settings.get",
	"nextcloud.shares.incoming-shares.accept",
	"nextcloud.shares.incoming-shares.decline",
	"nextcloud.shares.incoming-shares.pending",
	"nextcloud.shares.remote-shares.get",
	"nextcloud.shares.remote-shares.list",
	"nextcloud.shares.remote-shares.remove",
	"nextcloud.shares.sharees.search",
	"nextcloud.shares.shares.create",
	"nextcloud.shares.shares.delete",
	"nextcloud.shares.shares.get",
	"nextcloud.shares.shares.list",
	"nextcloud.shares.shares.update",
	"nextcloud.talk.files.share",
	"nextcloud.talk.messages.delete",
	"nextcloud.talk.messages.edit",
	"nextcloud.talk.messages.list",
	"nextcloud.talk.messages.send",
	"nextcloud.talk.participants.add",
	"nextcloud.talk.participants.list",
	"nextcloud.talk.participants.moderator",
	"nextcloud.talk.participants.remove",
	"nextcloud.talk.polls.close",
	"nextcloud.talk.polls.create",
	"nextcloud.talk.polls.get",
	"nextcloud.talk.polls.vote",
	"nextcloud.talk.reactions.set",
	"nextcloud.talk.rooms.create",
	"nextcloud.talk.rooms.delete",
	"nextcloud.talk.rooms.get",
	"nextcloud.talk.rooms.list",
	"nextcloud.talk.rooms.mark-read",
	"nextcloud.talk.rooms.update",
}
