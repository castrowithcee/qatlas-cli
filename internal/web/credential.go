package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// errPassphraseMismatch reports two typed passphrases of the vault's very first secret that did not match.
var errPassphraseMismatch = errors.New("the two passphrases did not match")

// roleField is one secret role offered on the new-credential form, together with the provider's own
// explanation of what it is for.
type roleField struct {
	Name        string
	Description string
}

// newCredentialData is what the new-credential template renders. Every field it shows back after a failed
// submission is something that was typed and is not a secret; Storage carries the choice back too, but never
// a role's own value.
type newCredentialData struct {
	Providers      []string
	Selected       string
	Roles          []roleField
	DefaultStorage string
	Storage        string
	Name           string
	CfgVer         string
	CSRF           string
	Error          string
}

// roleRow is one row of an existing credential's role table: where it stands, never what it holds.
// Replaceable is false for a role of an env credential, whose "secret" is the name of a variable this
// editor never treats as one to type over here (see the package comment of credential.go).
type roleRow struct {
	Role        string
	Description string
	State       string
	Replaceable bool
}

// credentialData is what the existing-credential template renders.
type credentialData struct {
	Name     string
	Provider string
	Type     string
	Roles    []roleRow
	CfgVer   string
	CSRF     string
	Notice   string
	Error    string
}

// credentialsUnavailable is the one fixed line every credential route shows when this run was not given a
// configuration store or a resolver at all (see New): a test server built for the overview and the admin
// guard alone, never a partial or inconsistent write.
const credentialsUnavailable = "credential management is not available for this run"

// credentialsReady reports whether this run can read and write credentials at all, and refuses the request
// with the one fixed line above when it cannot.
func (s *Server) credentialsReady(w http.ResponseWriter) bool {
	if s.svc == nil || s.secrets == nil {
		http.Error(w, credentialsUnavailable, http.StatusServiceUnavailable)
		return false
	}
	return true
}

// errConfigChanged is what every route shows when its form was opened against a configuration that has
// changed since, whether the stale form was noticed up front or only by the transaction that saves it.
const errConfigChanged = "the configuration changed since this form was opened; reload the page and try again"

// saveFailure words a failed configuration commit for a form: a conflict (see config.ErrConflict) gets the
// same reload-and-retry text as a stale form, anything else its own redacted message.
func (s *Server) saveFailure(err error) string {
	if errors.Is(err, config.ErrConflict) {
		return errConfigChanged
	}
	return s.redact(err.Error())
}

// configFingerprint hashes the configuration file's current bytes, so a form can carry, as an ordinary
// hidden field, proof of the exact file it was shown against: a save whose fingerprint no longer matches the
// file on disk stops as a conflict instead of overwriting a change nothing on this page ever saw (see
// handleCreateCredential and handleReplaceRole).
func (s *Server) configFingerprint() (string, error) {
	data, err := os.ReadFile(s.svc.Path())
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// redact removes any secret value this run's redactor has seen from s. It is applied to every error message
// a credential route ever shows, the same rule the TUI and the CLI already follow.
func (s *Server) redact(text string) string {
	if s.redactor == nil {
		return text
	}
	return s.redactor.Apply(text)
}

// registerSecret adds value to this run's redactor, when one is configured, before anything can fail and
// print it back. A server built without one (see New) simply never redacts, which happens only in a test
// that never reaches a credential route in the first place (see credentialsReady).
func (s *Server) registerSecret(value string) {
	if s.redactor != nil {
		s.redactor.Add(value)
	}
}

// csrfValue reads this run's current CSRF value under its own lock, for a GET page that has to embed it in
// the form it renders.
func (s *Server) csrfValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.csrf
}

// handleNewCredentialForm shows the new-credential form: a provider chooser, and once a provider is chosen
// (by its own small GET form, since this page ships no script to react to a selection), the name, the
// storage choice, and one row per secret role the provider defines.
func (s *Server) handleNewCredentialForm(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	selected := r.URL.Query().Get("provider")
	if !contains(cfg.Providers(), selected) {
		selected = ""
	}
	s.renderNewCredential(w, cfg, selected, "", cfg.SecretStore(), "")
}

// renderNewCredential builds and renders the new-credential page. name and storage are what a failed
// submission had already typed, so it does not have to be typed again; a secret value never is, since this
// function is never handed one.
func (s *Server) renderNewCredential(w http.ResponseWriter, cfg *config.Config, selected, name, storage, errText string) {
	data := newCredentialData{
		Providers: cfg.Providers(), Selected: selected, Name: name, Storage: storage,
		DefaultStorage: cfg.SecretStore(), CSRF: s.csrfValue(), Error: errText,
	}
	if selected != "" {
		for _, role := range cfg.SecretRolesOf(selected) {
			data.Roles = append(data.Roles, roleField{Name: role, Description: cfg.SecretRoleDescriptionOf(selected, role)})
		}
	}
	if fp, err := s.configFingerprint(); err == nil {
		data.CfgVer = fp
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.credTmpl.ExecuteTemplate(w, "new", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleCreateCredential creates a credential with type keyring, vault, or env, and, for a keyring or vault
// credential, every one of its provider's secret roles in the same commit as the credential entry itself,
// through manage.Service.CommitSecrets: the same boundary internal/tui's guided setup uses, so a store that turns out
// to be locked or missing after some roles were written leaves neither a stray secret nor a stray
// credential. An env credential only ever writes variable names, never a value, straight into the
// configuration.
func (s *Server) handleCreateCredential(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	provider := r.PostFormValue("provider")
	name := r.PostFormValue("name")
	storage := r.PostFormValue("storage")

	cfg, rev, err := s.svc.Load()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	fail := func(errText string) { s.renderNewCredential(w, cfg, provider, name, storage, errText) }

	if !contains(cfg.Providers(), provider) {
		fail("choose a provider first")
		return
	}
	if string(rev) != r.PostFormValue("cfgver") {
		fail(errConfigChanged)
		return
	}
	n := manage.NewCredential{Name: name, Provider: provider, Type: storage, Offer: vaultOfferFromForm(r)}
	roles := manage.AcceptedRoles(cfg, config.Credential{Provider: provider})
	if storage == config.CredentialTypeEnv {
		n.EnvNames = map[string]string{}
		for _, role := range roles {
			if v := r.PostFormValue("envname_" + role); v != "" {
				n.EnvNames[role] = v
			}
		}
	} else {
		n.Secrets = map[string]string{}
		for _, role := range roles {
			v := r.PostFormValue("secret_" + role)
			if v != "" {
				n.Secrets[role] = v
				s.registerSecret(v)
			}
		}
	}

	created, err := s.svc.CreateCredential(cfg, rev, n)
	if err != nil {
		fail(s.createFailure(err))
		return
	}
	warning := created.Warning
	target := "/credentials/" + url.PathEscape(name) + "?created=1"
	if warning != "" {
		target += "&warning=" + url.QueryEscape(warning)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// createFailure words a refused or failed creation for the form: input the form can fix is shown in the
// form's own words, anything else like a failed commit.
func (s *Server) createFailure(err error) string {
	var missing *manage.MissingValueError
	switch {
	case errors.As(err, &missing) && missing.Env:
		return fmt.Sprintf("%s names no environment variable; name the variable that holds it", missing.Role)
	case errors.As(err, &missing):
		return fmt.Sprintf("%s is empty; type its secret", missing.Role)
	case manage.IsInputError(err):
		return err.Error()
	}
	return s.saveFailure(err)
}

// handleCredentialForm shows one existing credential: its provider, its type, and, for each secret role its
// provider defines, where it now stands (never what it holds) and, for a keyring or vault credential, a form
// to replace that one role. An env credential's roles are shown as the variable name configured for them,
// with nothing to replace here: that name is configuration, not a secret this editor manages a value for.
func (s *Server) handleCredentialForm(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	name := r.PathValue("name")
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	cred, ok := cfg.Credentials[name]
	if !ok {
		http.Error(w, "unknown credential", http.StatusNotFound)
		return
	}

	if cred.Forward {
		s.renderPayloadDetail(w, r, cfg, name, cred)
		return
	}

	notice := ""
	switch {
	case r.URL.Query().Get("created") == "1":
		notice = "Credential created."
	case r.URL.Query().Get("replaced") != "":
		notice = fmt.Sprintf("Replaced the secret for %s.", r.URL.Query().Get("replaced"))
	}
	if warning := r.URL.Query().Get("warning"); warning != "" {
		if notice != "" {
			notice += " "
		}
		notice += warning
	}
	s.renderCredential(w, cfg, name, cred, notice, "")
}

// renderCredential builds and renders one credential's detail page.
func (s *Server) renderCredential(w http.ResponseWriter, cfg *config.Config, name string, cred config.Credential,
	notice, errText string) {
	data := credentialData{
		Name: name, Provider: cred.Provider, Type: cred.Type, CSRF: s.csrfValue(), Notice: notice, Error: errText,
	}
	for _, role := range manage.OfferedRoles(cfg, name, cred) {
		data.Roles = append(data.Roles, s.roleRow(cfg, name, cred, role))
	}
	if fp, err := s.configFingerprint(); err == nil {
		data.CfgVer = fp
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.credTmpl.ExecuteTemplate(w, "detail", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// roleRow reports where one secret role of an existing credential currently stands, never what it holds.
func (s *Server) roleRow(cfg *config.Config, name string, cred config.Credential, role string) roleRow {
	row := roleRow{Role: role, Description: cfg.SecretRoleDescriptionOf(manage.CredentialProvider(cfg, name, cred), role)}
	switch cred.Type {
	case config.CredentialTypeEnv:
		if v := cred.Values[role]; v != "" {
			row.State = "environment variable $" + v
		} else {
			row.State = "no variable named"
		}
	case config.CredentialTypeVault:
		row.Replaceable = true
		row.State = s.roleState(name, cred, role)
	default:
		row.Replaceable = true
		row.State = s.roleState(name, cred, role)
	}
	return row
}

// roleState reports where a role of an existing keyring or vault credential's secret currently sits. This
// page does not tell a vault locked here from one unlocked in the vault process.
func (s *Server) roleState(credential string, cred config.Credential, role string) string {
	in := manage.RoleStateInput{Type: cred.Type, Credential: credential, Role: role, Vault: s.secrets.Vault()}
	if cred.Type != config.CredentialTypeVault {
		in.Source, in.Checked = s.secrets.Status(credential, cred, role)
	}
	return s.redact(manage.RoleState(in))
}

// handleReplaceRole replaces the stored secret of one role of one existing credential, without touching any
// other role and without changing the configuration file: a keyring or vault credential's roles are never
// named in it. A vault write follows the exact path 'qatlas credential set' already takes for a vault
// credential (secrets.SetVault, then a vault process outside this run is told); it never sweeps any
// connection approval, the same restraint the CLI already keeps, so replacing a secret here never grants an
// approval nobody gave.
func (s *Server) handleReplaceRole(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	name := r.PathValue("name")
	role := r.PostFormValue("role")
	value := r.PostFormValue("value")

	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	cred, ok := cfg.Credentials[name]
	if !ok {
		http.Error(w, "unknown credential", http.StatusNotFound)
		return
	}
	fail := func(errText string) { s.renderCredential(w, cfg, name, cred, "", errText) }

	if _, err := manage.CheckSecretTarget(cfg, name, role); err != nil {
		var notStorable *manage.NotStorableError
		if errors.As(err, &notStorable) {
			fail("this credential's secrets come from the environment variables it names, so there is nothing to replace")
		} else {
			fail(fmt.Sprintf("unknown secret role %q", role))
		}
		return
	}
	if !contains(manage.AcceptedRoles(cfg, cred), role) {
		fail(fmt.Sprintf("unknown secret role %q", role))
		return
	}
	if fp, err := s.configFingerprint(); err != nil || fp != r.PostFormValue("cfgver") {
		fail("the configuration changed since this form was opened; reload the page and try again")
		return
	}
	if value == "" {
		fail("nothing was typed, so nothing was stored")
		return
	}
	s.registerSecret(value)

	// Replacing a vault secret here approves no connection: nobody gave an approval for it.
	out, err := s.svc.SetSecret(context.Background(), cfg, manage.SecretWrite{
		Name: name, Entry: cred, Role: role, Value: value, Offer: vaultOfferFromForm(r),
		Approval: manage.ApprovalNone,
	})
	if err != nil {
		fail(s.redact(err.Error()))
		return
	}
	warning := out.ProcessWarning

	target := "/credentials/" + url.PathEscape(name) + "?replaced=" + url.QueryEscape(role)
	if warning != "" {
		target += "&warning=" + url.QueryEscape(warning)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// vaultOfferFromForm reads a masked vault passphrase form's two fields, the same offer 'qatlas credential
// set' and internal/tui's own guided setup make for the vault's very first secret: a passphrase typed twice
// becomes the vault's encryption; leaving both empty keeps the vault unencrypted. It is harmless to pass on
// every vault write, since vault.Vault.Set only ever consults it when the vault does not exist yet.
func vaultOfferFromForm(r *http.Request) vault.PassphraseFunc {
	return func(string) (string, error) {
		first := r.PostFormValue("vault_passphrase")
		confirm := r.PostFormValue("vault_passphrase_confirm")
		if first == "" {
			return "", nil
		}
		if first != confirm {
			return "", errPassphraseMismatch
		}
		return first, nil
	}
}

// contains reports whether needle is among haystack.
func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// credentialTemplates defines the "new" and "detail" pages, parsed together as one template set. Like
// overviewTemplate they ship no script and no external asset, and their forms carry no secret value back:
// a secret field is always rendered empty.
const credentialTemplates = `
{{define "new"}}
{{template "layout-top" (page "Add a credential" "credentials")}}
<h1>Add a credential</h1>
{{if .Error}}<p class="notice notice-error" role="alert">{{.Error}}</p>{{end}}

<section class="section" aria-labelledby="provider-heading">
<h2 id="provider-heading">1. Provider</h2>
<form method="get" action="/credentials/new">
<div class="field">
<label for="provider">Provider</label>
<select id="provider" name="provider">
<option value="">choose a provider</option>
{{range .Providers}}<option value="{{.}}"{{if eq . $.Selected}} selected{{end}}>{{.}}</option>
{{end}}
</select>
</div>
<div class="actions"><button type="submit">Choose</button></div>
</form>
</section>

{{if .Selected}}
<section class="section" aria-labelledby="secrets-heading">
<h2 id="secrets-heading">2. Name and secrets</h2>
<form method="post" action="/credentials/new">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="cfgver" value="{{.CfgVer}}">
<input type="hidden" name="provider" value="{{.Selected}}">
<div class="field">
<label for="cred-name">Name</label>
<input id="cred-name" type="text" name="name" value="{{.Name}}" autocomplete="off">
</div>

<fieldset class="field">
<legend>Secrets are kept in</legend>
<div class="choice"><input id="storage-keyring" type="radio" name="storage" value="keyring"{{if eq .Storage "keyring"}} checked{{end}}> <label for="storage-keyring">the system keyring</label></div>
<div class="choice"><input id="storage-vault" type="radio" name="storage" value="vault"{{if eq .Storage "vault"}} checked{{end}}> <label for="storage-vault">the vault</label></div>
<div class="choice"><input id="storage-env" type="radio" name="storage" value="env"{{if eq .Storage "env"}} checked{{end}}> <label for="storage-env">environment variables</label></div>
</fieldset>

{{range .Roles}}
<fieldset class="field">
<legend>{{.Name}}</legend>
{{if .Description}}<p class="hint">{{.Description}}</p>{{end}}
<div class="field">
<label for="secret-{{.Name}}">Secret value, if secrets are kept in the system keyring or the vault</label>
<input id="secret-{{.Name}}" type="password" name="secret_{{.Name}}" autocomplete="new-password">
</div>
<div class="field">
<label for="envname-{{.Name}}">Environment variable name, if secrets are kept in environment variables</label>
<input id="envname-{{.Name}}" type="text" name="envname_{{.Name}}" autocomplete="off">
</div>
</fieldset>
{{end}}

<fieldset class="field">
<legend>Vault passphrase</legend>
<p class="hint">Only used if this credential's secrets go to the vault and this is the vault's very first secret.
Leave both empty and it stays unencrypted: its secrets are then readable by anyone who can read that file.
Type a passphrase, twice, to encrypt it instead.</p>
<div class="field">
<label for="vault-passphrase">New passphrase</label>
<input id="vault-passphrase" type="password" name="vault_passphrase" autocomplete="new-password">
</div>
<div class="field">
<label for="vault-passphrase-confirm">Confirm</label>
<input id="vault-passphrase-confirm" type="password" name="vault_passphrase_confirm" autocomplete="new-password">
</div>
</fieldset>

<div class="actions"><button type="submit">Create credential</button></div>
</form>
</section>
{{end}}
{{template "layout-bottom"}}
{{end}}

{{define "detail"}}
{{template "layout-top" (page .Name "credentials")}}
<h1>{{.Name}}</h1>
<p>Provider: {{.Provider}} · Type: {{.Type}}</p>
{{if .Notice}}<p class="notice notice-ok" role="status">{{.Notice}}</p>{{end}}
{{if .Error}}<p class="notice notice-error" role="alert">{{.Error}}</p>{{end}}

<section class="section" aria-labelledby="roles-heading">
<h2 id="roles-heading">Roles</h2>
<div class="table-wrap" role="region" aria-labelledby="roles-heading" tabindex="0">
<table>
<thead><tr><th scope="col">Role</th><th scope="col">Status</th><th scope="col">Replace</th></tr></thead>
<tbody>
{{$root := .}}
{{range .Roles}}
<tr>
<td>{{.Role}}{{if .Description}}<br>{{.Description}}{{end}}</td>
<td>{{.State}}</td>
<td>
{{if .Replaceable}}
<form method="post" action="/credentials/{{$root.Name}}/role">
<input type="hidden" name="csrf" value="{{$root.CSRF}}">
<input type="hidden" name="cfgver" value="{{$root.CfgVer}}">
<input type="hidden" name="role" value="{{.Role}}">
<div class="field">
<label for="value-{{.Role}}">New secret</label>
<input id="value-{{.Role}}" type="password" name="value" autocomplete="new-password">
</div>
<div class="field">
<label for="vault-passphrase-{{.Role}}">Vault passphrase, only for the vault's very first secret</label>
<input id="vault-passphrase-{{.Role}}" type="password" name="vault_passphrase" autocomplete="new-password">
</div>
<div class="field">
<label for="vault-passphrase-confirm-{{.Role}}">Confirm</label>
<input id="vault-passphrase-confirm-{{.Role}}" type="password" name="vault_passphrase_confirm" autocomplete="new-password">
</div>
<div class="actions"><button type="submit">Replace</button></div>
</form>
{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
{{if not .Roles}}<p class="empty">No roles.</p>{{end}}
</div>
</section>
{{template "layout-bottom"}}
{{end}}

{{define "connection-provider"}}
{{template "layout-top" (page "Set up a connection" "connections")}}
<h1>Set up a connection</h1>
<p class="step">Step 1 of 3 · Provider</p>
<p>Choose the system to connect to. The next steps offer only what this provider defines.</p>
{{if .Error}}<p class="notice notice-error" role="alert">{{.Error}}</p>{{end}}
<form method="get" action="/connections/new">
<div class="field">
<label for="provider">Provider</label>
<select id="provider" name="provider">
<option value="">choose a provider</option>
{{range .Providers}}<option value="{{.}}">{{.}}</option>
{{end}}
</select>
</div>
<div class="actions"><button type="submit">Choose</button></div>
</form>
{{template "layout-bottom"}}
{{end}}

{{define "connection-build"}}
{{template "layout-top" (page "Set up a connection" "connections")}}
<h1>Set up a connection</h1>
<p class="step">Step 2 of 3 · Service, credential, scope, permissions</p>
<p class="actions"><a href="/connections/new">Choose another provider</a></p>
{{if .Error}}<p class="notice notice-error" role="alert">{{.Error}}</p>{{end}}
<form method="get" action="/connections/new/review">
<input type="hidden" name="provider" value="{{.Provider}}">

<section class="section" aria-labelledby="service-heading">
<h2 id="service-heading">Service</h2>
<div class="field">
<label for="service">Reuse a service, or choose {{printf "%s" "(new service)"}}</label>
<select id="service" name="service">
{{range .Services}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>
{{end}}
</select>
</div>
<div class="field">
<label for="svcname">New service name</label>
<input id="svcname" type="text" name="svcname" value="{{.Form.SvcName}}" autocomplete="off">
</div>
<div class="field">
<label for="svcbaseurl">New service base URL</label>
<input id="svcbaseurl" type="text" name="svcbaseurl" value="{{.Form.SvcBaseURL}}" autocomplete="off">
</div>
</section>

<section class="section" aria-labelledby="credential-heading">
<h2 id="credential-heading">Credential</h2>
<div class="field">
<label for="credential">Reuse a credential, or choose {{printf "%s" "(new credential)"}}</label>
<select id="credential" name="credential">
{{range .Credentials}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>
{{end}}
</select>
</div>
<div class="field">
<label for="credname">New credential name</label>
<input id="credname" type="text" name="credname" value="{{.Form.CredName}}" autocomplete="off">
</div>
<fieldset class="field">
<legend>Secrets of a new credential are kept in</legend>
<div class="choice"><input id="credstorage-keyring" type="radio" name="credstorage" value="keyring"{{if eq .Form.CredStorage "keyring"}} checked{{end}}> <label for="credstorage-keyring">the system keyring</label></div>
<div class="choice"><input id="credstorage-vault" type="radio" name="credstorage" value="vault"{{if eq .Form.CredStorage "vault"}} checked{{end}}> <label for="credstorage-vault">the vault</label></div>
<div class="choice"><input id="credstorage-env" type="radio" name="credstorage" value="env"{{if eq .Form.CredStorage "env"}} checked{{end}}> <label for="credstorage-env">environment variables</label></div>
<p class="hint">{{.StorageHint}}</p>
</fieldset>
{{$form := .Form}}
{{range .Roles}}
<div class="field">
<label for="envname-{{.Name}}">{{.Name}} environment variable name, only used if a new credential keeps its secrets in environment variables</label>
<input id="envname-{{.Name}}" type="text" name="envname_{{.Name}}" value="{{index $form.EnvNames .Name}}" autocomplete="off">
{{if .Description}}<p class="hint">{{.Description}}</p>{{end}}
{{if ne $form.CredStorage "env"}}<p class="hint">Its secret value, if kept in the system keyring or the vault, is typed on the next page.</p>{{end}}
</div>
{{end}}
</section>

<section class="section" aria-labelledby="scope-heading">
<h2 id="scope-heading">Scope</h2>
<div class="field">
<label for="connname">Connection name</label>
<input id="connname" type="text" name="connname" value="{{.Form.ConnName}}" autocomplete="off">
</div>
<div class="field">
<label for="targets">Targets</label>
<input id="targets" type="text" name="targets" value="{{.Form.Targets}}" autocomplete="off">
<p class="hint">{{.TargetHint}}</p>
</div>
<div class="field">
<label for="description">Description</label>
<input id="description" type="text" name="description" value="{{.Form.Description}}" autocomplete="off">
</div>
</section>

{{if or .FilesRead .FilesWrite}}
<section class="section" aria-labelledby="files-heading">
<h2 id="files-heading">Local files</h2>
<p>Directories on this machine the tools of this connection may use, absolute or starting with ~/, one per line. A directory gives access to it and everything below. Leave a field empty to release nothing. A change later needs a new approval.</p>
{{if .FilesRead}}<div class="field">
<label for="filesread">Upload directories (files the tools may read)</label>
<textarea id="filesread" name="filesread" rows="3" cols="60" autocomplete="off">{{.Form.FilesRead}}</textarea>
</div>{{end}}
{{if .FilesWrite}}<div class="field">
<label for="fileswrite">Download directories (where the tools may write files)</label>
<textarea id="fileswrite" name="fileswrite" rows="3" cols="60" autocomplete="off">{{.Form.FilesWrite}}</textarea>
</div>{{end}}
</section>
{{end}}

<section class="section" aria-labelledby="permissions-heading">
<h2 id="permissions-heading">Permissions</h2>
<fieldset class="field">
<legend>Permission mode</legend>
<div class="choice"><input id="permmode-default" type="radio" name="permmode" value="default"{{if eq .Form.PermMode "default"}} checked{{end}}> <label for="permmode-default">use the provider's default</label></div>
<div class="choice"><input id="permmode-custom" type="radio" name="permmode" value="custom"{{if eq .Form.PermMode "custom"}} checked{{end}}> <label for="permmode-custom">choose explicitly</label></div>
</fieldset>
{{if .Permissions}}<fieldset class="field">
<legend>Permissions</legend>
{{range $i, $p := .Permissions}}<div class="choice"><input id="perm-{{$i}}" type="checkbox" name="perm" value="{{$p.Value}}"{{if $p.Selected}} checked{{end}}> <label for="perm-{{$i}}">{{$p.Label}}</label></div>
{{end}}</fieldset>{{end}}
</section>

{{if .ForwardChoices}}
<section class="section" aria-labelledby="forward-heading">
<h2 id="forward-heading">Payload credentials</h2>
<p>{{.ForwardText}}</p>
{{if .BindingNote}}<p class="notice" role="status">{{.BindingNote}}</p>{{end}}
<fieldset class="field">
<legend>Released payload credentials</legend>
{{range $i, $c := .ForwardChoices}}<div class="choice"><input id="forward-{{$i}}" type="checkbox" name="forward" value="{{$c.Value}}"{{if $c.Selected}} checked{{end}}> <label for="forward-{{$i}}">{{$c.Label}}</label></div>
{{end}}</fieldset>
</section>
{{end}}

<section class="section" aria-labelledby="tools-heading">
<h2 id="tools-heading">Tools</h2>
<fieldset class="field">
<legend>Tool mode</legend>
<div class="choice"><input id="toolsmode-all" type="radio" name="toolsmode" value="all"{{if eq .Form.ToolsMode "all"}} checked{{end}}> <label for="toolsmode-all">offer every tool the permissions allow</label></div>
<div class="choice"><input id="toolsmode-selected" type="radio" name="toolsmode" value="selected"{{if eq .Form.ToolsMode "selected"}} checked{{end}}> <label for="toolsmode-selected">offer only the tools ticked below</label></div>
</fieldset>
{{if .Tools}}<fieldset class="field">
<legend>Tools</legend>
{{range $i, $t := .Tools}}<div class="choice"><input id="tool-{{$i}}" type="checkbox" name="tool" value="{{$t.ID}}"{{if $t.Selected}} checked{{end}}> <label for="tool-{{$i}}">{{$t.ID}} ({{$t.Effect}})</label></div>
{{end}}</fieldset>{{end}}
</section>

<div class="actions"><button type="submit">Review</button></div>
</form>
{{template "layout-bottom"}}
{{end}}

{{define "connection-review"}}
{{template "layout-top" (page "Set up a connection" "connections")}}
<h1>Set up a connection</h1>
<p class="step">Step 3 of 3 · Review</p>
<p class="actions"><a href="/connections/new?provider={{.Form.Provider}}">Back to the settings</a></p>
{{if .Error}}<p class="notice notice-error" role="alert">{{.Error}}</p>{{end}}
<p>Nothing is written yet. This summary carries no secret.</p>
{{if .BindingNote}}<p class="notice" role="status">{{.BindingNote}}</p>{{end}}
<section class="section" aria-labelledby="summary-heading">
<h2 id="summary-heading">Summary</h2>
<div class="table-wrap" role="region" aria-labelledby="summary-heading" tabindex="0">
<table>
<tbody>
{{range .Summary}}<tr><th scope="row">{{.Label}}</th><td>{{.Value}}</td></tr>
{{end}}</tbody>
</table>
</div>
</section>

<form method="post" action="/connections/new/review">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="cfgver" value="{{.CfgVer}}">
<input type="hidden" name="provider" value="{{.Form.Provider}}">
<input type="hidden" name="service" value="{{.Form.Service}}">
<input type="hidden" name="svcname" value="{{.Form.SvcName}}">
<input type="hidden" name="svcbaseurl" value="{{.Form.SvcBaseURL}}">
<input type="hidden" name="credential" value="{{.Form.Credential}}">
<input type="hidden" name="credname" value="{{.Form.CredName}}">
<input type="hidden" name="credstorage" value="{{.Form.CredStorage}}">
{{$form := .Form}}
{{range $role, $value := .Form.EnvNames}}<input type="hidden" name="envname_{{$role}}" value="{{$value}}">
{{end}}
<input type="hidden" name="connname" value="{{.Form.ConnName}}">
<input type="hidden" name="targets" value="{{.Form.Targets}}">
<input type="hidden" name="description" value="{{.Form.Description}}">
<input type="hidden" name="filesread" value="{{.Form.FilesRead}}">
<input type="hidden" name="fileswrite" value="{{.Form.FilesWrite}}">
<input type="hidden" name="permmode" value="{{.Form.PermMode}}">
{{range .Form.Perms}}<input type="hidden" name="perm" value="{{.}}">
{{end}}
{{range .Form.Forward}}<input type="hidden" name="forward" value="{{.}}">
{{end}}
<input type="hidden" name="toolsmode" value="{{.Form.ToolsMode}}">
{{range .Form.Tools}}<input type="hidden" name="tool" value="{{.}}">
{{end}}

{{if .NewCredential}}
<section class="section" aria-labelledby="newsecrets-heading">
<h2 id="newsecrets-heading">Secrets for the new credential {{.CredentialName}}</h2>
{{if eq .Storage "env"}}
<p>This credential keeps its secrets in environment variables, already named on the previous page: nothing more to type here.</p>
{{else}}
{{range .Roles}}
<fieldset class="field">
<legend>{{.Name}}</legend>
{{if .Description}}<p class="hint">{{.Description}}</p>{{end}}
<div class="field">
<label for="secret-{{.Name}}">Secret value</label>
<input id="secret-{{.Name}}" type="password" name="secret_{{.Name}}" autocomplete="new-password">
</div>
</fieldset>
{{end}}
<fieldset class="field">
<legend>Vault passphrase</legend>
<p class="hint">Only used if this credential's secrets go to the vault and this is the vault's very first secret.
Leave both empty and it stays unencrypted. Type a passphrase, twice, to encrypt it instead.</p>
<div class="field">
<label for="vault-passphrase">New passphrase</label>
<input id="vault-passphrase" type="password" name="vault_passphrase" autocomplete="new-password">
</div>
<div class="field">
<label for="vault-passphrase-confirm">Confirm</label>
<input id="vault-passphrase-confirm" type="password" name="vault_passphrase_confirm" autocomplete="new-password">
</div>
</fieldset>
{{end}}
</section>
{{end}}

<div class="actions"><button type="submit">Create connection</button></div>
</form>
{{template "layout-bottom"}}
{{end}}

{{define "connection-result"}}
{{template "layout-top" (page .Name "connections")}}
<h1>{{.Name}}</h1>
{{if .Notice}}<p class="notice notice-ok" role="status">{{.Notice}}</p>{{end}}
{{if .Error}}<p class="notice notice-error" role="alert">{{.Error}}</p>{{end}}
<section class="section" aria-labelledby="details-heading">
<h2 id="details-heading">Details</h2>
<div class="table-wrap" role="region" aria-labelledby="details-heading" tabindex="0">
<table>
<tbody>
<tr><th scope="row">Provider</th><td>{{.Provider}}</td></tr>
<tr><th scope="row">Service</th><td>{{.Service}}</td></tr>
<tr><th scope="row">Credential</th><td>{{.Credential}}</td></tr>
<tr><th scope="row">Targets</th><td>{{.Targets}}</td></tr>
<tr><th scope="row">Files</th><td>{{.Files}}</td></tr>
<tr><th scope="row">Description</th><td>{{.Description}}</td></tr>
<tr><th scope="row">Permissions</th><td>{{.Permissions}}</td></tr>
<tr><th scope="row">Tools</th><td>{{.Tools}}</td></tr>
</tbody>
</table>
</div>
</section>

{{if .ShowForward}}
<section class="section" aria-labelledby="forward-heading">
<h2 id="forward-heading">Payload credentials</h2>
<p>{{.ForwardText}}</p>
{{if .BindingNote}}<p class="notice" role="status">{{.BindingNote}}</p>{{end}}
<p>Saving a changed list never approves it: a connection that was approved stays open until a person approves it.</p>
<form method="post" action="/connections/{{.Name}}/forward">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="cfgver" value="{{.CfgVer}}">
<fieldset class="field">
<legend>Released payload credentials</legend>
{{range $i, $c := .ForwardChoices}}<div class="choice"><input id="forward-{{$i}}" type="checkbox" name="forward" value="{{$c.Value}}"{{if $c.Selected}} checked{{end}}> <label for="forward-{{$i}}">{{$c.Label}}</label></div>
{{end}}</fieldset>
<div class="actions"><button type="submit">Save released payload credentials</button></div>
</form>
</section>
{{end}}

<section class="section" aria-labelledby="test-heading">
<h2 id="test-heading">Test</h2>
{{if .TestResult}}<p class="notice" role="status">{{.TestResult}}</p>{{end}}
{{if .TesterAvailable}}
<form method="post" action="/connections/{{.Name}}/test">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<div class="actions"><button type="submit">Test this connection</button></div>
</form>
{{else}}
<p class="empty">Connection testing is not available for this run.</p>
{{end}}
</section>
{{template "layout-bottom"}}
{{end}}
`
