package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// canaryPayload carries characters a careless echo, quoting, or YAML dump would show.
const canaryPayload = `pw-Ω;'"\${x}%s-7c1`
const canaryPayload2 = `n0t-5hown:{[#&*]}-d42`

// payloadHarness is an editor over a keyring or an unencrypted vault, plus the places its values end up.
type payloadHarness struct {
	m       *Model
	store   *config.Store
	path    string
	secrets *secret.Resolver
	mem     *secret.MemoryStore
	views   []string
}

func newPayloadHarness(t *testing.T, withVault bool) *payloadHarness {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	var secrets *secret.Resolver
	var mem *secret.MemoryStore
	if withVault {
		secrets, mem = newVaultResolver(t, dir)
	} else {
		secrets, mem = newResolver(t, dir, nil)
	}
	m, err := New(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return &payloadHarness{m: m, store: store, path: path, secrets: secrets, mem: mem}
}

// see records everything the screen and the messages show at this point.
func (h *payloadHarness) see() {
	h.views = append(h.views, h.m.View(), h.m.status, h.m.fail)
}

func (h *payloadHarness) press(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		pump(t, h.m, k)
		h.see()
	}
}

func (h *payloadHarness) typeText(t *testing.T, text string) {
	t.Helper()
	typeText(t, h.m, text)
	h.see()
}

// neverShown fails when value appeared in anything seen, the configuration file included.
func (h *payloadHarness) neverShown(t *testing.T, values ...string) {
	t.Helper()
	h.see()
	if raw, err := os.ReadFile(h.path); err == nil {
		h.views = append(h.views, string(raw))
	}
	for _, view := range h.views {
		for _, value := range values {
			if strings.Contains(view, value) {
				t.Fatalf("a secret value appeared on screen, in a message, or in the file:\n%s", view)
			}
		}
	}
}

// addField types a field name in the focused new-field row, adds it, and types its value.
func (h *payloadHarness) addField(t *testing.T, name, value string) {
	t.Helper()
	if h.m.fields[h.m.focus].kind != fieldPayloadNew {
		t.Fatalf("focus is on %q, want the new-field row", h.m.fields[h.m.focus].label)
	}
	h.typeText(t, name)
	h.press(t, "enter")
	if h.m.fail != "" || h.m.fields[h.m.focus].label != name {
		t.Fatalf("adding field %q: fail %q, focus %q", name, h.m.fail, h.m.fields[h.m.focus].label)
	}
	h.typeText(t, value)
	h.press(t, "tab")
}

// startPayload opens the form of a new payload secret and gets to the new-field row, with the storage and
// description chosen.
func (h *payloadHarness) startPayload(t *testing.T, name, storage, description string) {
	t.Helper()
	openSectionByName(t, h.m, sectionCredentials)
	h.press(t, "s")
	if h.m.screen != screenForm || !h.m.isPayloadForm() {
		t.Fatalf("s opened screen %v, want the payload form", h.m.screen)
	}
	h.typeText(t, name)
	h.press(t, "tab")
	selectChoice(t, h.m, storage)
	h.press(t, "tab")
	h.typeText(t, description)
	h.press(t, "tab")
}

func ctrlD() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlD} }

func TestPayloadSecretWithTwoFieldsIsStored(t *testing.T) {
	for _, tt := range []struct {
		name      string
		storage   string
		withVault bool
	}{
		{"keyring", storageKeyring, false},
		{"vault", storageVault, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newPayloadHarness(t, tt.withVault)
			h.startPayload(t, "billing", tt.storage, "login of the billing portal")
			h.addField(t, "username", canaryPayload)
			h.addField(t, "password", canaryPayload2)
			h.press(t, "f2")
			if tt.withVault {
				// The vault's very first secret offers a passphrase; empty keeps it unencrypted.
				if h.m.screen != screenVaultOffer {
					t.Fatalf("screen = %v, want the vault passphrase offer", h.m.screen)
				}
				h.press(t, "enter")
			}
			if h.m.fail != "" {
				t.Fatalf("saving reported %q", h.m.fail)
			}
			if h.m.screen != screenList || !strings.Contains(h.m.status, "Saved payload secret billing") {
				t.Fatalf("screen %v status %q, want the list after saving", h.m.screen, h.m.status)
			}

			loaded, err := loadTestConfig(t, h.path)
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			cred := loaded.Credentials["billing"]
			wantType := config.CredentialTypeKeyring
			if tt.withVault {
				wantType = config.CredentialTypeVault
			}
			if !cred.Forward || cred.Type != wantType || cred.Provider != "" ||
				!slices.Equal(cred.Fields, []string{"username", "password"}) ||
				cred.Description != "login of the billing portal" {
				t.Fatalf("saved credential = %+v", cred)
			}
			for field, want := range map[string]string{"username": canaryPayload, "password": canaryPayload2} {
				var got string
				if tt.withVault {
					value, found, _, err := h.secrets.Vault().Get("billing", field, nil)
					if err != nil || !found {
						t.Fatalf("vault Get(%s) = found %v, %v", field, found, err)
					}
					got = value
				} else {
					got, err = h.mem.Get(context.Background(), secret.StoreKey("billing", field))
					if err != nil {
						t.Fatalf("keyring Get(%s) = %v", field, err)
					}
				}
				if got != want {
					t.Errorf("stored value of %s differs from what was typed", field)
				}
			}
			// The list names the fields, never a value.
			h.press(t, "esc")
			h.neverShown(t, canaryPayload, canaryPayload2)
			if !strings.Contains(strings.Join(h.views, "\n"), "(payload)") {
				t.Error("the list never marked the entry as a payload secret")
			}
		})
	}
}

func TestPayloadValueUnderFourCharactersIsRefusedWithoutShowingIt(t *testing.T) {
	h := newPayloadHarness(t, false)
	h.startPayload(t, "pin-code", storageKeyring, "")
	h.addField(t, "pin", "zq9")
	h.press(t, "f2")
	if !strings.Contains(h.m.fail, "shorter than") {
		t.Fatalf("fail = %q, want the length rule", h.m.fail)
	}
	if h.m.screen != screenForm {
		t.Fatalf("screen = %v, want the form kept open", h.m.screen)
	}
	h.neverShown(t, "zq9")
	if _, err := os.Stat(h.path); err == nil {
		t.Fatal("a refused value still wrote the configuration")
	}
	if _, err := h.mem.Get(context.Background(), secret.StoreKey("pin-code", "pin")); err == nil {
		t.Fatal("a refused value was stored")
	}
}

func TestPayloadFieldWithoutAValueIsRefused(t *testing.T) {
	h := newPayloadHarness(t, false)
	h.startPayload(t, "billing", storageKeyring, "")
	h.typeText(t, "username")
	h.press(t, "enter")
	h.press(t, "f2")
	if !strings.Contains(h.m.fail, `"username" needs a value`) {
		t.Fatalf("fail = %q", h.m.fail)
	}
	if _, err := os.Stat(h.path); err == nil {
		t.Fatal("the refused save wrote the configuration")
	}
}

func TestPayloadFieldsAreAddedAndRemoved(t *testing.T) {
	h := newPayloadHarness(t, false)
	h.startPayload(t, "billing", storageKeyring, "")

	// A name the core refuses, and an empty one, add no row.
	h.typeText(t, "bad name")
	h.press(t, "enter")
	if h.m.fail == "" || len(h.m.payloadRows()) != 0 {
		t.Fatalf("a field named with a space: fail %q rows %d, want a refusal", h.m.fail, len(h.m.payloadRows()))
	}
	clearField(t, h.m)
	h.press(t, "enter")
	if h.m.fail == "" {
		t.Fatal("an empty field name was accepted")
	}

	h.addField(t, "username", canaryPayload)
	h.addField(t, "password", canaryPayload2)
	h.typeText(t, "username")
	h.press(t, "enter")
	if !strings.Contains(h.m.fail, "exists already") {
		t.Fatalf("a duplicate field: fail %q", h.m.fail)
	}
	clearField(t, h.m)
	if got := len(h.m.payloadRows()); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}

	// Remove the password row: focus it, then ctrl+d.
	press(t, h.m, "shift+tab")
	if h.m.fields[h.m.focus].label != "password" {
		t.Fatalf("focus on %q, want password", h.m.fields[h.m.focus].label)
	}
	h.m.Update(ctrlD())
	if got := len(h.m.payloadRows()); got != 1 || h.m.fields[h.m.payloadRows()[0]].label != "username" {
		t.Fatalf("after ctrl+d the value rows are %v", h.m.payloadRows())
	}
	h.press(t, "f2")
	if h.m.fail != "" {
		t.Fatalf("saving reported %q", h.m.fail)
	}
	loaded, err := loadTestConfig(t, h.path)
	if err != nil || !slices.Equal(loaded.Credentials["billing"].Fields, []string{"username"}) {
		t.Fatalf("saved fields = %+v, %v", loaded.Credentials["billing"], err)
	}
	if _, err := h.mem.Get(context.Background(), secret.StoreKey("billing", "password")); err == nil {
		t.Error("the removed field's value was stored")
	}
	h.neverShown(t, canaryPayload, canaryPayload2)
}

func TestEditingAPayloadSecretKeepsEmptyValuesAndDeletesRemovedFields(t *testing.T) {
	h := newPayloadHarness(t, false)
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("billing", config.Credential{
		Type: config.CredentialTypeKeyring, Forward: true, Fields: []string{"username", "password"}}))
	mustNoError(t, h.store.Save(cfg))
	mustNoError(t, h.secrets.Set("billing", "username", "old-username-1"))
	mustNoError(t, h.secrets.Set("billing", "password", "old-password-1"))
	h.m.cfg = cfg
	syncRevision(t, h.m)

	openEntryForm(t, h.m, sectionCredentials, "billing")
	if !h.m.isPayloadForm() {
		t.Fatal("opening a payload secret did not build the payload form")
	}
	// The name and the place of the values are locked; the form opens on the description.
	if !h.m.fields[0].readOnly || !h.m.field(storageLabel).readOnly {
		t.Error("the name or the storage row of an existing payload secret is editable")
	}
	// Rotate username only, drop password.
	for h.m.fields[h.m.focus].label != "username" {
		h.press(t, "tab")
	}
	h.typeText(t, canaryPayload)
	h.press(t, "tab")
	h.m.Update(ctrlD())
	h.press(t, "f2")
	if h.m.fail != "" {
		t.Fatalf("saving reported %q", h.m.fail)
	}
	if got, _ := h.mem.Get(context.Background(), secret.StoreKey("billing", "username")); got != canaryPayload {
		t.Error("the typed value did not replace the stored one")
	}
	if _, err := h.mem.Get(context.Background(), secret.StoreKey("billing", "password")); err == nil {
		t.Error("the removed field's stored value was kept")
	}
	h.neverShown(t, canaryPayload, "old-username-1", "old-password-1")

	// Saving again with every row empty keeps what is stored.
	openEntryForm(t, h.m, sectionCredentials, "billing")
	h.press(t, "f2")
	if got, _ := h.mem.Get(context.Background(), secret.StoreKey("billing", "username")); got != canaryPayload {
		t.Error("an empty row replaced or removed the stored value")
	}
}

func TestPayloadSecretIsNotSavedWithoutTheAdminSession(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, store := newEncryptedVaultModel(t, dir, passphrase, true)

	openSectionByName(t, m, sectionCredentials)
	pump(t, m, "s")
	typeText(t, m, "billing")
	press(t, m, "tab")
	selectChoice(t, m, storageVault)
	press(t, m, "tab", "tab")
	typeText(t, m, "username")
	pump(t, m, "enter")
	typeText(t, m, canaryPayload)
	pump(t, m, "f2")
	if m.screen != screenAdminAuth {
		t.Fatalf("saving without an admin session = screen %v, want the admin dialog", m.screen)
	}
	pump(t, m, "esc")
	if m.screen != screenForm || m.fields[0].value() != "billing" {
		t.Fatalf("cancelling the dialog lost the form: screen %v", m.screen)
	}
	if _, err := store.Load(); err == nil {
		if loaded, _ := store.Load(); len(loaded.Credentials) != 0 {
			t.Fatalf("a cancelled admin check still saved %+v", loaded.Credentials)
		}
	}
	if _, found, _, _ := m.secrets.Vault().Get("billing", "username", nil); found {
		t.Fatal("a cancelled admin check still stored a value")
	}
	// An encrypted vault binds the release, so no weaker-binding note.
	if strings.Contains(m.View(), "no encrypted vault binds") {
		t.Error("the weak-binding note shows although the vault is encrypted")
	}

	pump(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("saving in the admin session: fail %q screen %v", m.fail, m.screen)
	}
	if value, found, _, err := m.secrets.Vault().Get("billing", "username", nil); err != nil || !found ||
		value != canaryPayload {
		t.Fatalf("the vault does not hold the value: found %v, %v", found, err)
	}
}

func TestConnectionFormOffersOnlyPayloadSecretsAndSavesTheList(t *testing.T) {
	h := newPayloadHarness(t, false)
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Provider: "bookstack", Type: config.CredentialTypeKeyring}))
	mustNoError(t, cfg.SetCredential("billing", config.Credential{
		Type: config.CredentialTypeKeyring, Forward: true, Fields: []string{"username"}}))
	mustNoError(t, cfg.SetCredential("crm", config.Credential{
		Type: config.CredentialTypeVault, Forward: true, Fields: []string{"token"}}))
	mustNoError(t, cfg.SetConnection("personal", config.Connection{
		Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead}}))
	mustNoError(t, h.store.Save(cfg))
	h.m.cfg = cfg
	syncRevision(t, h.m)

	openEntryForm(t, h.m, sectionConnections, "personal")
	if got := h.m.field("credential").choices; !slices.Equal(got, []string{"reader"}) {
		t.Fatalf("the credential row offers %v, want only the provider credential", got)
	}
	row := h.m.field(forwardLabel)
	if row == nil || !slices.Equal(row.choices, []string{"billing", "crm"}) {
		t.Fatalf("the forward row = %+v, want the two payload secrets only", row)
	}
	for i := range h.m.fields {
		if h.m.fields[i].label == forwardLabel {
			h.m.focus = i
		}
	}
	h.m.applyFocus()
	press(t, h.m, "enter")
	if h.m.screen != screenPicker {
		t.Fatalf("enter on the forward row = screen %v, want the picker", h.m.screen)
	}
	press(t, h.m, " ", "down", " ", "enter")
	if got := h.m.field(forwardLabel).marked(); !slices.Equal(got, []string{"billing", "crm"}) {
		t.Fatalf("ticked = %v", got)
	}
	// Untick the first again, so the saved list is a proper subset.
	press(t, h.m, "enter")
	press(t, h.m, " ", "enter")
	if got := h.m.field(forwardLabel).marked(); !slices.Equal(got, []string{"crm"}) {
		t.Fatalf("ticked = %v", got)
	}
	pump(t, h.m, "f2")
	if h.m.fail != "" {
		t.Fatalf("saving reported %q", h.m.fail)
	}
	loaded, err := loadTestConfig(t, h.path)
	if err != nil || !slices.Equal(loaded.Connections["personal"].ForwardSecrets, []string{"crm"}) {
		t.Fatalf("saved forward_secrets = %v, %v", loaded.Connections["personal"].ForwardSecrets, err)
	}

	// An untouched save keeps the list.
	openEntryForm(t, h.m, sectionConnections, "personal")
	pump(t, h.m, "f2")
	loaded, _ = loadTestConfig(t, h.path)
	if !slices.Equal(loaded.Connections["personal"].ForwardSecrets, []string{"crm"}) {
		t.Fatalf("an untouched save changed forward_secrets to %v", loaded.Connections["personal"].ForwardSecrets)
	}
}

func TestConnectionFormWithoutPayloadSecretsStaysAsItWas(t *testing.T) {
	m, _, _ := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "A", "B")
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	if m.field(forwardLabel) != nil {
		t.Fatal("a connection form shows the forward row although no payload secret exists")
	}
	if reason := m.newEntryBlockedFor(sectionConnections); reason != "" {
		t.Fatalf("a provider credential exists, yet new connection is blocked: %q", reason)
	}
}

func TestOnlyPayloadSecretsBlockANewConnection(t *testing.T) {
	h := newPayloadHarness(t, false)
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, cfg.SetCredential("billing", config.Credential{
		Type: config.CredentialTypeKeyring, Forward: true, Fields: []string{"username"}}))
	h.m.cfg = cfg
	syncRevision(t, h.m)
	if !strings.Contains(h.m.newEntryBlockedFor(sectionConnections), "a credential") {
		t.Fatal("a payload secret alone must not satisfy a connection's need for a credential")
	}
}

func TestWeakBindingNoteWithoutAnEncryptedVault(t *testing.T) {
	h := newPayloadHarness(t, true)
	// In the payload form itself.
	openSectionByName(t, h.m, sectionCredentials)
	pump(t, h.m, "s")
	if !strings.Contains(h.m.View(), "no encrypted vault binds this release") {
		t.Errorf("the payload form does not say the binding is weaker:\n%s", h.m.View())
	}

	// And where forward_secrets is ticked, in the form and in the picker.
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Provider: "bookstack", Type: config.CredentialTypeVault}))
	mustNoError(t, cfg.SetCredential("billing", config.Credential{
		Type: config.CredentialTypeVault, Forward: true, Fields: []string{"username"}}))
	mustNoError(t, cfg.SetConnection("personal", config.Connection{
		Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead}}))
	mustNoError(t, h.store.Save(cfg))
	h.m.cfg = cfg
	syncRevision(t, h.m)
	h.m.fields = nil
	openEntryForm(t, h.m, sectionConnections, "personal")
	for i := range h.m.fields {
		if h.m.fields[i].label == forwardLabel {
			h.m.focus = i
		}
	}
	h.m.applyFocus()
	press(t, h.m, "enter")
	if !strings.Contains(h.m.View(), "no encrypted vault binds this release") {
		t.Errorf("the picker does not say the binding is weaker:\n%s", h.m.View())
	}
	press(t, h.m, " ", "enter")
	if !strings.Contains(h.m.View(), "no encrypted vault binds this release") {
		t.Errorf("the connection form does not say the binding is weaker:\n%s", h.m.View())
	}
}

func TestSavingAConnectionNeverApprovesAChangedRelease(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, v := newApprovalFixture(t, dir, passphrase)
	withPayload := cfg.Clone()
	mustNoError(t, withPayload.SetCredential("billing", config.Credential{
		Type: config.CredentialTypeVault, Forward: true, Fields: []string{"username"}}))
	mustNoError(t, store.Save(withPayload))
	if _, _, err := approval.Approve(context.Background(), withPayload, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	m := openApprovalModel(t, store, dir)
	openEntryForm(t, m, sectionConnections, "personal")
	for i := range m.fields {
		if m.fields[i].label == forwardLabel {
			m.focus = i
		}
	}
	m.applyFocus()
	press(t, m, "enter", " ", "enter")
	pump(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)
	if m.fail != "" {
		t.Fatalf("saving reported %q", m.fail)
	}
	if !slices.Equal(m.cfg.Connections["personal"].ForwardSecrets, []string{"billing"}) {
		t.Fatalf("forward_secrets = %v", m.cfg.Connections["personal"].ForwardSecrets)
	}
	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("saving the connection approved a changed forward_secrets in passing")
	}
	if !strings.Contains(m.status, "forward_secrets") || !strings.Contains(m.status, "Approvals") {
		t.Errorf("status = %q, want it to say the release stays open in Approvals", m.status)
	}
}

func TestSavingAPayloadSecretLeavesItsConnectionsOpen(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, v := newApprovalFixture(t, dir, passphrase)
	released := cfg.Clone()
	mustNoError(t, released.SetCredential("billing", config.Credential{
		Type: config.CredentialTypeVault, Forward: true, Fields: []string{"username"}}))
	mustNoError(t, released.SetConnection("personal", config.Connection{
		Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead},
		ForwardSecrets: []string{"billing"}}))
	mustNoError(t, store.Save(released))
	if _, _, err := approval.Approve(context.Background(), released, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	m := openApprovalModel(t, store, dir)
	openEntryForm(t, m, sectionCredentials, "billing")
	for m.fields[m.focus].kind != fieldPayloadNew {
		press(t, m, "tab")
	}
	typeText(t, m, "password")
	pump(t, m, "enter")
	typeText(t, m, canaryPayload)
	pump(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)
	if m.fail != "" {
		t.Fatalf("saving reported %q", m.fail)
	}
	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("adding a field approved the connection that releases the payload secret")
	}
	if !strings.Contains(m.status, "personal") || !strings.Contains(m.status, "Approvals") {
		t.Errorf("status = %q, want it to name the connection waiting in Approvals", m.status)
	}
	if strings.Contains(m.View()+m.status, canaryPayload) {
		t.Error("the value appeared on screen")
	}
}

func TestPayloadSecretListAndFormFitNarrowTerminals(t *testing.T) {
	for _, width := range []int{79, 80} {
		h := newPayloadHarness(t, false)
		cfg := newTestConfig(t)
		mustNoError(t, cfg.SetCredential("billing-portal-login", config.Credential{
			Type: config.CredentialTypeKeyring, Forward: true, Description: "login of the billing portal",
			Fields: []string{"username", "password", "totp-seed-for-recovery"}}))
		mustNoError(t, h.store.Save(cfg))
		h.m.cfg = cfg
		syncRevision(t, h.m)
		h.m.Update(tea.WindowSizeMsg{Width: width, Height: 30})

		openSectionByName(t, h.m, sectionCredentials)
		assertViewFits(t, h.m.View(), width, 30)
		pump(t, h.m, "enter")
		if !h.m.isPayloadForm() {
			t.Fatalf("width %d: enter did not open the payload form", width)
		}
		assertViewFits(t, h.m.View(), width, 30)
		for range h.m.fields {
			press(t, h.m, "tab")
			assertViewFits(t, h.m.View(), width, 30)
		}
		press(t, h.m, "esc", "s")
		assertViewFits(t, h.m.View(), width, 30)
		if !strings.Contains(h.m.View(), "payload secret") {
			t.Errorf("width %d: the form does not say what it is:\n%s", width, h.m.View())
		}
	}
}

func TestPayloadSecretCanBeDeleted(t *testing.T) {
	h := newPayloadHarness(t, false)
	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetCredential("billing", config.Credential{
		Type: config.CredentialTypeKeyring, Forward: true, Fields: []string{"username"}}))
	mustNoError(t, h.store.Save(cfg))
	h.m.cfg = cfg
	syncRevision(t, h.m)
	openSectionByName(t, h.m, sectionCredentials)
	press(t, h.m, "enter", "esc") // opening and leaving changes nothing
	if h.m.screen != screenList {
		t.Fatalf("screen = %v, want the list", h.m.screen)
	}
	press(t, h.m, "d")
	if !strings.Contains(h.m.View(), "billing <field>") {
		t.Errorf("the delete question does not say how to remove the stored values:\n%s", h.m.View())
	}
	pump(t, h.m, "y")
	loaded, err := loadTestConfig(t, h.path)
	if err != nil || len(loaded.Credentials) != 0 {
		t.Fatalf("after deleting: %+v, %v", loaded.Credentials, err)
	}
}

func TestForwardBindingNoteNamesAConnectionCredentialOutsideTheVault(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, _ := newApprovalFixture(t, dir, passphrase)
	other := cfg.Clone()
	mustNoError(t, other.SetCredential("keyring-reader", config.Credential{
		Provider: "bookstack", Type: config.CredentialTypeKeyring}))
	mustNoError(t, store.Save(other))
	m := openApprovalModel(t, store, dir)
	if state, _ := m.secrets.Vault().State(); state != vault.StateLocked {
		t.Fatalf("vault state = %v, want locked", state)
	}
	if note := m.forwardBindingNote("reader"); note != "" {
		t.Errorf("a vault credential in an encrypted vault: note %q, want none", note)
	}
	if note := m.forwardBindingNote("keyring-reader"); !strings.Contains(note, "not in the encrypted vault") {
		t.Errorf("a keyring credential: note %q", note)
	}
	if note := m.forwardBindingNote(""); note != "" {
		t.Errorf("the payload form against an encrypted vault: note %q, want none", note)
	}
}
