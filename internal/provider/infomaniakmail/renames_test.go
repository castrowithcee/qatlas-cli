package infomaniakmail

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of Infomaniak Mail tool IDs.
var currentToolIDs = []string{
	"infomaniak.mail.attachments.get",
	"infomaniak.mail.drafts.create",
	"infomaniak.mail.drafts.delete",
	"infomaniak.mail.drafts.send",
	"infomaniak.mail.drafts.update",
	"infomaniak.mail.folders.list",
	"infomaniak.mail.messages.delete",
	"infomaniak.mail.messages.expunge",
	"infomaniak.mail.messages.flag",
	"infomaniak.mail.messages.get",
	"infomaniak.mail.messages.list",
	"infomaniak.mail.messages.move",
	"infomaniak.mail.messages.send",
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 13 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 13 {
		t.Errorf("renamedTools has %d entries, want 13", len(renamedTools))
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
		if strings.TrimPrefix(old, "infomaniakmail.") != strings.TrimPrefix(next, "infomaniak.mail.") ||
			!strings.HasPrefix(old, "infomaniakmail.") {
			t.Errorf("rename %s -> %s changes more than the provider ID", old, next)
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

func TestFormerMailProviderAndToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  mail:
    provider: infomaniakmail
credentials:
  mail-secret:
    provider: infomaniakmail
    type: env
    values:
      password: QATLAS_MAIL_PASSWORD
connections:
  main:
    service: mail
    credential: mail-secret
    targets: [mailbox/user@example.net]
    tools: [infomaniakmail.folders.list, infomaniakmail.messages.list]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for _, old := range []string{"infomaniakmail.folders.list", "infomaniakmail.messages.list"} {
		next := renamedTools[old]
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
	if !strings.Contains(warnings, "infomaniakmail") || !strings.Contains(warnings, `"infomaniak.mail"`) {
		t.Errorf("warnings lack the provider rename:\n%s", warnings)
	}
}
