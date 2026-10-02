package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/secretcommit"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// A payload credential (forward: true) holds freely named fields whose values a tool may pass on to a third
// party by reference; a connection releases it with forward_secrets. Its form is one page for a new and for an
// existing credential: the name, where the values are kept, an optional description, one masked row per stored
// field, and a few blank rows that add a field with its value. The values leave the form only towards the
// credential store, through secretcommit.Commit; they are never rendered back, never put into a redirect or a
// message, and never written into the configuration.

// payloadNewRows is how many blank rows of "new field name, its value" a payload form offers. The page ships
// no script, so a person who needs more fields saves and opens the credential again.
const payloadNewRows = 4

const (
	payloadStorageText = "system keyring keeps the values in the keyring of this machine, vault keeps them in a " +
		"file beside the configuration; a payload credential never uses environment variables"
	payloadDescriptionText = "optional; what these values are for. Discovery publishes it, so it must never carry " +
		"a secret or personal data"
	forwardText = "forward_secrets: the payload credentials a tool of this connection may pass on by reference; " +
		"none ticked releases none"
)

// payloadRowData is one stored field of an existing payload credential: where its value stands, never what it
// holds.
type payloadRowData struct {
	Name   string
	State  string
	Remove bool
}

// payloadNewRowData is one blank row of the form; only its name is ever carried back after a failure.
type payloadNewRowData struct {
	Index int
	Name  string
}

// payloadFormData is what the payload page renders.
type payloadFormData struct {
	Edit            bool
	Action          string
	Name            string
	Storage         string
	Description     string
	Rows            []payloadRowData
	NewRows         []payloadNewRowData
	MinLength       int
	BindingNote     string
	ShowPassphrase  bool
	StorageText     string
	DescriptionText string
	CfgVer          string
	CSRF            string
	Notice          string
	Error           string
}

// payloadNameProblem asks the configuration core whether name is a valid field name, so the rule stays the
// core's. It returns "" for a valid one.
func payloadNameProblem(name string) string {
	trial := config.New()
	_ = trial.SetCredential("trial", config.Credential{
		Type: config.CredentialTypeVault, Forward: true, Fields: []string{name}})
	err := trial.Validate()
	if err == nil {
		return ""
	}
	for _, line := range strings.Split(err.Error(), "\n") {
		if _, rule, ok := strings.Cut(line, "fields[0]: "); ok {
			return "the field name is refused: " + rule
		}
	}
	return "the field name is refused"
}

// forwardBindingNote says when no approval binds the release of a payload credential, so that only the
// forward_secrets list of the configuration limits it, and "" when one does. connCredential is the credential
// of the connection the note is about, "" when that is not known (the payload credential's own page).
func (s *Server) forwardBindingNote(cfg *config.Config, connCredential string) string {
	const only = "only the forward_secrets list in this configuration limits which payload credentials a tool may use"
	v := s.secrets.Vault()
	if v == nil {
		return "no encrypted vault binds this release: " + only
	}
	state, err := v.State()
	if err != nil || (state != vault.StateLocked && state != vault.StateUnlocked) {
		return "no encrypted vault binds this release: " + only
	}
	if connCredential != "" && cfg.Credentials[connCredential].Type != config.CredentialTypeVault {
		return "this connection's own credential is not in the encrypted vault, so no approval binds this " +
			"release: " + only
	}
	return ""
}

// forwardOpenNote names the connections that release the payload credential and now wait for a person's
// approval. Saving never approves anything: a changed release is a decision of its own. "" when nothing can be
// said, for lack of an unlocked vault.
func (s *Server) forwardOpenNote(cand *config.Config, name string) string {
	v := s.secrets.Vault()
	if v == nil {
		return ""
	}
	if state, err := v.State(); err != nil || state != vault.StateUnlocked {
		return ""
	}
	report, err := approval.Pending(cand, v)
	if err != nil {
		return ""
	}
	var open []string
	for _, c := range report.Open {
		if slices.Contains(cand.Connections[c.Connection].ForwardSecrets, name) {
			open = append(open, c.Connection)
		}
	}
	if len(open) == 0 {
		return ""
	}
	return fmt.Sprintf("%s release(s) it and now wait for approval, which saving does not give: approve with "+
		"`qatlas vault approve` or in the TUI's Approvals section", strings.Join(open, ", "))
}

// handleNewPayloadForm shows the form of a new payload credential.
func (s *Server) handleNewPayloadForm(w http.ResponseWriter, _ *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	storage := config.CredentialTypeKeyring
	if cfg.SecretStore() == config.CredentialTypeVault {
		storage = config.CredentialTypeVault
	}
	s.renderPayload(w, cfg, payloadFormData{Storage: storage}, "", "")
}

// renderPayloadDetail shows an existing payload credential from the shared credential route.
func (s *Server) renderPayloadDetail(w http.ResponseWriter, r *http.Request, cfg *config.Config, name string,
	cred config.Credential) {
	notice := ""
	switch {
	case r.URL.Query().Get("created") == "1":
		notice = "Payload credential created."
	case r.URL.Query().Get("saved") == "1":
		notice = "Payload credential saved."
	}
	if warning := r.URL.Query().Get("warning"); warning != "" {
		notice += " " + warning
	}
	if extra := s.takeNotice(); extra != "" {
		notice += " " + extra
	}
	s.renderPayload(w, cfg, payloadStateOf(name, cred), strings.TrimSpace(notice), "")
}

// payloadStateOf is the form state of a stored payload credential.
func payloadStateOf(name string, cred config.Credential) payloadFormData {
	data := payloadFormData{Edit: true, Name: name, Storage: cred.Type, Description: cred.Description}
	for _, field := range cred.Fields {
		data.Rows = append(data.Rows, payloadRowData{Name: field})
	}
	return data
}

// renderPayload completes data with everything the page derives from the configuration and renders it.
// data never holds a value.
func (s *Server) renderPayload(w http.ResponseWriter, cfg *config.Config, data payloadFormData, notice, errText string) {
	data.Action = "/credentials/new/payload"
	if data.Edit {
		data.Action = "/credentials/" + url.PathEscape(data.Name) + "/payload"
		cred := cfg.Credentials[data.Name]
		for i := range data.Rows {
			if cred.Type == config.CredentialTypeVault {
				data.Rows[i].State = s.vaultRoleState(data.Name, data.Rows[i].Name)
			} else {
				data.Rows[i].State = s.keyringRoleState(data.Name, cred, data.Rows[i].Name)
			}
		}
	}
	for i := len(data.NewRows); i < payloadNewRows; i++ {
		data.NewRows = append(data.NewRows, payloadNewRowData{Index: i})
	}
	data.MinLength = config.MinForwardValueLength
	data.BindingNote = s.forwardBindingNote(cfg, "")
	data.ShowPassphrase = data.Storage == config.CredentialTypeVault || !data.Edit
	data.StorageText = payloadStorageText
	data.DescriptionText = payloadDescriptionText
	data.CSRF = s.csrfValue()
	data.Notice, data.Error = notice, errText
	if fp, err := s.configFingerprint(); err == nil {
		data.CfgVer = fp
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.credTmpl.ExecuteTemplate(w, "payload", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleCreatePayload and handleSavePayload are the one write of the payload form, behind withAdminGuard.
func (s *Server) handleCreatePayload(w http.ResponseWriter, r *http.Request) {
	s.savePayload(w, r, false)
}
func (s *Server) handleSavePayload(w http.ResponseWriter, r *http.Request) { s.savePayload(w, r, true) }

// savePayload checks the form against a candidate configuration, the core's Validate and the value rule, and
// then commits the values and the configuration entry together through secretcommit.Commit. Values the form
// left empty keep what is stored; the stored value of a removed field is deleted after the commit.
func (s *Server) savePayload(w http.ResponseWriter, r *http.Request, edit bool) {
	if !s.credentialsReady(w) {
		return
	}
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}

	var existing config.Credential
	name := strings.TrimSpace(r.PostFormValue("name"))
	if edit {
		name = r.PathValue("name")
		var ok bool
		if existing, ok = cfg.Credentials[name]; !ok || !existing.Forward {
			http.Error(w, "unknown credential", http.StatusNotFound)
			return
		}
	}

	// The state below is everything a failed save shows again; no value is among it.
	state := payloadFormData{
		Edit: edit, Name: name, Storage: r.PostFormValue("storage"),
		Description: r.PostFormValue("description"),
	}
	if edit {
		state.Storage = existing.Type
	}
	removeSet := map[string]bool{}
	for _, f := range r.PostForm["remove"] {
		removeSet[f] = true
	}
	for _, f := range existing.Fields {
		state.Rows = append(state.Rows, payloadRowData{Name: f, Remove: removeSet[f]})
	}
	newNames := make([]string, payloadNewRows)
	for i := range newNames {
		newNames[i] = strings.TrimSpace(r.PostFormValue("newname_" + strconv.Itoa(i)))
		state.NewRows = append(state.NewRows, payloadNewRowData{Index: i, Name: newNames[i]})
	}
	fail := func(errText string) { s.renderPayload(w, cfg, state, "", errText) }

	// Every typed value is known to the redactor before anything can fail and print it back.
	values := map[string]string{}
	var roles []string
	for _, f := range existing.Fields {
		if v := r.PostFormValue("value_" + f); v != "" {
			s.registerSecret(v)
			if !removeSet[f] {
				values[f] = v
				roles = append(roles, f)
			}
		}
	}
	for i, f := range newNames {
		if f == "" {
			continue
		}
		if v := r.PostFormValue("newvalue_" + strconv.Itoa(i)); v != "" {
			s.registerSecret(v)
			values[f] = v
		}
	}

	if fp, err := s.configFingerprint(); err != nil || fp != r.PostFormValue("cfgver") {
		fail("the configuration changed since this form was opened; reload the page and try again")
		return
	}
	if !edit {
		if name == "" {
			fail("a credential name must not be empty")
			return
		}
		if _, taken := cfg.Credentials[name]; taken {
			fail(fmt.Sprintf("a credential named %q already exists", name))
			return
		}
		if state.Storage != config.CredentialTypeKeyring && state.Storage != config.CredentialTypeVault {
			fail("choose where the values are kept: the system keyring or the vault")
			return
		}
	}

	cred := config.Credential{Type: state.Storage, Forward: true, Description: state.Description}
	var removed []string
	for _, f := range existing.Fields {
		if removeSet[f] {
			removed = append(removed, f)
			continue
		}
		cred.Fields = append(cred.Fields, f)
	}
	for _, f := range newNames {
		if f == "" {
			continue
		}
		if slices.Contains(cred.Fields, f) || slices.Contains(existing.Fields, f) {
			fail(fmt.Sprintf("the field %q exists already", f))
			return
		}
		if problem := payloadNameProblem(f); problem != "" {
			fail(problem)
			return
		}
		cred.Fields = append(cred.Fields, f)
		if _, ok := values[f]; !ok {
			fail(fmt.Sprintf("the field %q needs a value", f))
			return
		}
		roles = append(roles, f)
	}
	for _, f := range roles {
		if err := cred.CheckForwardValue(f, values[f]); err != nil {
			fail(fmt.Sprintf("credential %s: %v", name, err))
			return
		}
	}

	candidate := cfg.Clone()
	if err := candidate.SetCredential(name, cred); err != nil {
		fail(s.redact(err.Error()))
		return
	}
	if err := candidate.Validate(); err != nil {
		fail(s.redact(err.Error()))
		return
	}

	toVault := cred.Type == config.CredentialTypeVault
	warning, err := secretcommit.Commit(s.store, s.secrets, candidate, name, toVault, roles, values,
		vaultOfferFromForm(r))
	if err != nil {
		fail(s.redact(err.Error()) + "; the configuration was not changed")
		return
	}
	for _, f := range removed {
		var rmErr error
		if toVault {
			rmErr = s.secrets.DeleteVault(name, f)
		} else {
			_, rmErr = s.secrets.Delete(name, f)
		}
		if rmErr != nil && !errors.Is(rmErr, secret.ErrNoEntry) {
			warning += fmt.Sprintf("; warning: the stored value of %s could not be removed, remove it with "+
				"'qatlas credential delete %s %s'", f, name, f)
		}
	}
	warning = strings.TrimPrefix(warning, "; ")
	if note := s.forwardOpenNote(candidate, name); note != "" {
		s.setNotice(note)
	}

	flag := "?saved=1"
	if !edit {
		flag = "?created=1"
	}
	target := "/credentials/" + url.PathEscape(name) + flag
	if warning != "" {
		target += "&warning=" + url.QueryEscape(s.redact(warning))
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// forwardChoices are the payload credentials a connection may release, in stable order.
func forwardChoices(cfg *config.Config) []string {
	var names []string
	for _, name := range sortedNames(cfg.Credentials) {
		if cfg.Credentials[name].Forward {
			names = append(names, name)
		}
	}
	return names
}

// handleSetForward saves the forward_secrets list of an existing connection. It writes the configuration only
// and never approves: a changed release of a connection that was approved stays open until a person approves
// it with `qatlas vault approve` or in the TUI's Approvals section.
func (s *Server) handleSetForward(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	name := r.PathValue("name")
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	conn, ok := cfg.Connections[name]
	if !ok {
		http.Error(w, "unknown connection", http.StatusNotFound)
		return
	}
	fail := func(errText string) { s.renderConnectionResult(w, cfg, name, conn, "", errText) }
	if fp, err := s.configFingerprint(); err != nil || fp != r.PostFormValue("cfgver") {
		fail("the configuration changed since this form was opened; reload the page and try again")
		return
	}
	changed := conn
	changed.ForwardSecrets = append([]string(nil), r.PostForm["forward"]...)
	cand := cfg.Clone()
	if err := cand.SetConnection(name, changed); err != nil {
		fail(s.redact(err.Error()))
		return
	}
	if err := cand.Validate(); err != nil {
		fail(s.redact(err.Error()))
		return
	}
	if err := s.store.Save(cand); err != nil {
		fail(s.redact(err.Error()))
		return
	}
	s.setNotice(s.forwardChangeNotice(cand, name))
	http.Redirect(w, r, "/connections/"+url.PathEscape(name)+"?forward=1", http.StatusSeeOther)
}

// forwardChangeNotice says what a saved release list of one connection means for its approval.
func (s *Server) forwardChangeNotice(cand *config.Config, connName string) string {
	v := s.secrets.Vault()
	if v == nil {
		return ""
	}
	state, err := v.State()
	switch {
	case err != nil:
		return ""
	case state == vault.StateLocked:
		return "the vault is locked, so whether this connection now waits for approval was not checked"
	case state != vault.StateUnlocked:
		return ""
	}
	report, err := approval.Pending(cand, v)
	if err != nil {
		return "approval state not checked: " + s.redact(err.Error())
	}
	change, open := approvalOpenChange(report, connName)
	if !open {
		return ""
	}
	return fmt.Sprintf("stays open: %s; saving does not approve it, approve it with `qatlas vault approve` or in "+
		"the TUI's Approvals section", approvalStaysOpenReason(change))
}

// payloadTemplates defines the payload credential page. Like the other pages it ships no script and no
// external asset, and every masked input is rendered empty.
const payloadTemplates = `
{{define "payload"}}
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web · {{if .Edit}}{{.Name}}{{else}}add a payload credential{{end}}</title></head>
<body>
<p><a href="/">&larr; overview</a></p>
<h1>{{if .Edit}}{{.Name}}{{else}}Add a payload credential{{end}}</h1>
<p>A payload credential holds freely named fields. A tool may pass their values on to a third party by reference, but only through a connection that lists it in forward_secrets.</p>
{{if .Notice}}<p>{{.Notice}}</p>{{end}}
{{if .Error}}<p>{{.Error}}</p>{{end}}
{{if .BindingNote}}<p>{{.BindingNote}}</p>{{end}}
<form method="post" action="{{.Action}}">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="cfgver" value="{{.CfgVer}}">
{{if .Edit}}
<p>Type: {{.Storage}}</p>
{{else}}
<p><label>Name <input type="text" name="name" value="{{.Name}}" autocomplete="off"></label></p>
<p>Values are kept in:</p>
<label><input type="radio" name="storage" value="keyring"{{if ne .Storage "vault"}} checked{{end}}> the system keyring</label><br>
<label><input type="radio" name="storage" value="vault"{{if eq .Storage "vault"}} checked{{end}}> the vault</label>
<p>{{.StorageText}}</p>
{{end}}
<p><label>Description <input type="text" name="description" value="{{.Description}}" autocomplete="off"></label><br>{{.DescriptionText}}</p>

{{if .Rows}}
<h2>Fields</h2>
<p>Values are typed masked, at least {{.MinLength}} characters, and are never shown. Leave a value empty to keep the stored one.</p>
<table border="1" cellpadding="4">
<tr><th>Field</th><th>Status</th><th>New value</th><th>Remove</th></tr>
{{range .Rows}}<tr>
<td>{{.Name}}</td>
<td>{{.State}}</td>
<td><input type="password" name="value_{{.Name}}" autocomplete="new-password"></td>
<td><label><input type="checkbox" name="remove" value="{{.Name}}"{{if .Remove}} checked{{end}}> remove, with its stored value</label></td>
</tr>
{{end}}
</table>
{{end}}

<h2>{{if .Rows}}Add fields{{else}}Fields{{end}}</h2>
<p>A field name uses letters, digits, - _ . ; its value is typed masked, at least {{.MinLength}} characters. Leave a row empty to add none.</p>
{{range .NewRows}}<p><label>Field name <input type="text" name="newname_{{.Index}}" value="{{.Name}}" autocomplete="off"></label>
<label>Value <input type="password" name="newvalue_{{.Index}}" autocomplete="new-password"></label></p>
{{end}}

{{if .ShowPassphrase}}
<fieldset>
<legend>Vault passphrase</legend>
<p>Only used if the values go to the vault and this is the vault's very first secret. Leave both empty and it stays unencrypted. Type a passphrase, twice, to encrypt it instead.</p>
<p><label>New passphrase <input type="password" name="vault_passphrase" autocomplete="new-password"></label></p>
<p><label>Confirm <input type="password" name="vault_passphrase_confirm" autocomplete="new-password"></label></p>
</fieldset>
{{end}}

<button type="submit">{{if .Edit}}Save{{else}}Create payload credential{{end}}</button>
</form>
</body>
</html>
{{end}}
`
