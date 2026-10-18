package lexware

import (
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// currentToolIDs is the complete set of Lexware tool IDs.
var currentToolIDs = []string{
	"lexware.articles.create",
	"lexware.articles.delete",
	"lexware.articles.get",
	"lexware.articles.list",
	"lexware.articles.update",
	"lexware.contacts.create",
	"lexware.contacts.get",
	"lexware.contacts.list",
	"lexware.contacts.update",
	"lexware.countries.list",
	"lexware.credit-notes.create",
	"lexware.credit-notes.get",
	"lexware.credit-notes.issue",
	"lexware.delivery-notes.create",
	"lexware.delivery-notes.get",
	"lexware.delivery-notes.issue",
	"lexware.documents.download",
	"lexware.down-payment-invoices.get",
	"lexware.dunnings.create",
	"lexware.dunnings.get",
	"lexware.event-subscriptions.delete",
	"lexware.event-subscriptions.get",
	"lexware.event-subscriptions.list",
	"lexware.files.download",
	"lexware.files.upload",
	"lexware.invoices.create",
	"lexware.invoices.get",
	"lexware.invoices.issue",
	"lexware.invoices.list",
	"lexware.order-confirmations.create",
	"lexware.order-confirmations.get",
	"lexware.order-confirmations.issue",
	"lexware.payment-conditions.list",
	"lexware.payments.get",
	"lexware.posting-categories.list",
	"lexware.print-layouts.list",
	"lexware.profile.get",
	"lexware.quotations.create",
	"lexware.quotations.get",
	"lexware.quotations.issue",
	"lexware.recurring-templates.get",
	"lexware.recurring-templates.list",
	"lexware.voucher-list.list",
	"lexware.vouchers.attach",
	"lexware.vouchers.book",
	"lexware.vouchers.create",
	"lexware.vouchers.get",
	"lexware.vouchers.update",
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
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(got) != 48 {
		t.Errorf("registry holds %d tools, differing from the expected %d IDs", len(got), len(want))
	}
	if err := reg.ValidateRenames(); err != nil {
		t.Errorf("ValidateRenames() = %v", err)
	}
}

func TestRenamedToolsCoverEveryChangedID(t *testing.T) {
	if len(renamedTools) != 19 {
		t.Errorf("renamedTools has %d entries, want 19", len(renamedTools))
	}
	current := map[string]bool{}
	for _, id := range currentToolIDs {
		current[id] = true
	}
	targets := map[string]bool{}
	for old, next := range renamedTools {
		targets[next] = true
		if current[old] || !current[next] {
			t.Errorf("rename %s -> %s: old must be gone and new registered", old, next)
		}
		if strings.ReplaceAll(next, "-", "") != old {
			t.Errorf("rename %s -> %s changes more than word boundaries", old, next)
		}
	}
	for id := range current {
		if strings.Contains(id, "-") && !targets[id] {
			t.Errorf("hyphenated tool %s has no former ID", id)
		}
	}
}

func TestFormerLexwareToolIDsLoadWithWarnings(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	document := `version: 1
services:
  lx:
    provider: lexware
credentials:
  lx-key:
    provider: lexware
    type: env
    values:
      api-key: QATLAS_LEXWARE_KEY
connections:
  office:
    service: lx
    credential: lx-key
    permissions: [read]
    tools: [lexware.voucherlist.list, lexware.creditnotes.get, lexware.eventsubscriptions.list]
`
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for old, next := range map[string]string{
		"lexware.voucherlist.list":        "lexware.voucher-list.list",
		"lexware.creditnotes.get":         "lexware.credit-notes.get",
		"lexware.eventsubscriptions.list": "lexware.event-subscriptions.list",
	} {
		if !strings.Contains(warnings, `tool "`+old+`" was renamed to "`+next+`"`) {
			t.Errorf("warnings lack the rename %s -> %s:\n%s", old, next, warnings)
		}
	}
}
