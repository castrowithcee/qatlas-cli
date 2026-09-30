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
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/secretcommit"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
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
	if s.store == nil || s.secrets == nil {
		http.Error(w, credentialsUnavailable, http.StatusServiceUnavailable)
		return false
	}
	return true
}

// configFingerprint hashes the configuration file's current bytes, so a form can carry, as an ordinary
// hidden field, proof of the exact file it was shown against: a save whose fingerprint no longer matches the
// file on disk stops as a conflict instead of overwriting a change nothing on this page ever saw (see
// handleCreateCredential and handleReplaceRole).
func (s *Server) configFingerprint() (string, error) {
	data, err := os.ReadFile(s.store.Path())
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
	cfg, err := s.store.Load()
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
			data.Roles = append(data.Roles, roleField{Name: role, Description: cfg.SecretRoleDescription(role)})
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
// through secretcommit.Commit: the same boundary internal/tui's guided setup uses, so a store that turns out
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

	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	fail := func(errText string) { s.renderNewCredential(w, cfg, provider, name, storage, errText) }

	if !contains(cfg.Providers(), provider) {
		fail("choose a provider first")
		return
	}
	if fp, err := s.configFingerprint(); err != nil || fp != r.PostFormValue("cfgver") {
		fail("the configuration changed since this form was opened; reload the page and try again")
		return
	}
	if name == "" {
		fail("a credential name must not be empty")
		return
	}
	if _, taken := cfg.Credentials[name]; taken {
		fail(fmt.Sprintf("a credential named %q already exists", name))
		return
	}
	if storage != config.CredentialTypeKeyring && storage != config.CredentialTypeVault &&
		storage != config.CredentialTypeEnv {
		fail("choose where the secrets are kept")
		return
	}

	roles := cfg.SecretRolesOf(provider)
	cred := config.Credential{Provider: provider, Type: storage}
	values := map[string]string{}
	if storage == config.CredentialTypeEnv {
		cred.Values = map[string]string{}
		for _, role := range roles {
			v := r.PostFormValue("envname_" + role)
			if v == "" {
				fail(fmt.Sprintf("%s names no environment variable; name the variable that holds it", role))
				return
			}
			cred.Values[role] = v
		}
	} else {
		for _, role := range roles {
			v := r.PostFormValue("secret_" + role)
			if v == "" {
				fail(fmt.Sprintf("%s is empty; type its secret", role))
				return
			}
			values[role] = v
			s.registerSecret(v)
		}
	}

	if err := cfg.SetCredential(name, cred); err != nil {
		fail(s.redact(err.Error()))
		return
	}
	if err := cfg.Validate(); err != nil {
		fail(s.redact(err.Error()))
		return
	}

	var warning string
	if storage == config.CredentialTypeEnv {
		if err := s.store.Save(cfg); err != nil {
			fail(s.redact(err.Error()))
			return
		}
	} else {
		toVault := storage == config.CredentialTypeVault
		warning, err = secretcommit.Commit(s.store, s.secrets, cfg, name, toVault, roles, values, vaultOfferFromForm(r))
		if err != nil {
			fail(s.redact(err.Error()))
			return
		}
	}

	target := "/credentials/" + url.PathEscape(name) + "?created=1"
	if warning != "" {
		target += "&warning=" + url.QueryEscape(warning)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
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
	cfg, err := s.store.Load()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	cred, ok := cfg.Credentials[name]
	if !ok {
		http.Error(w, "unknown credential", http.StatusNotFound)
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
	for _, role := range cfg.SecretRolesOf(cred.Provider) {
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
	row := roleRow{Role: role, Description: cfg.SecretRoleDescription(role)}
	switch cred.Type {
	case config.CredentialTypeEnv:
		if v := cred.Values[role]; v != "" {
			row.State = "environment variable $" + v
		} else {
			row.State = "no variable named"
		}
	case config.CredentialTypeVault:
		row.Replaceable = true
		row.State = s.vaultRoleState(name, role)
	default:
		row.Replaceable = true
		row.State = s.keyringRoleState(name, cred, role)
	}
	return row
}

// vaultRoleState reports where a role of a vault credential's secret currently sits, the same way
// internal/tui's own vaultRoleState does: it never asks for a passphrase, so an encrypted, locked vault
// answers "vault locked" instead of blocking on one nobody typed here.
func (s *Server) vaultRoleState(credential, role string) string {
	v := s.secrets.Vault()
	if v == nil {
		return "no vault is configured for this run"
	}
	state, err := v.State()
	if err != nil {
		return s.redact(err.Error())
	}
	switch state {
	case vault.StateAbsent:
		return "not stored yet"
	case vault.StateLocked:
		return "vault locked"
	}
	_, found, _, err := v.Get(credential, role, nil)
	if err != nil {
		return s.redact(err.Error())
	}
	if found {
		return "in the vault"
	}
	return "not stored yet"
}

// keyringRoleState reports where a role of a keyring credential's secret currently sits, the same words
// internal/tui's own storedSource uses.
func (s *Server) keyringRoleState(credential string, cred config.Credential, role string) string {
	source, checked := s.secrets.Status(credential, cred, role)
	switch source {
	case secret.SourceStore:
		return "in system keyring"
	case secret.SourceEnv:
		return "environment variable, overrides the keyring"
	case secret.SourcePlaintext:
		return "unencrypted file"
	}
	switch secret.StoreStage(checked) {
	case secret.StoreEmpty:
		return "not stored yet"
	case secret.StoreLocked:
		return "keyring locked"
	case secret.StoreUnavailable, secret.StoreTimedOut:
		return "keyring unreachable"
	case secret.StoreOff:
		return "keyring switched off"
	}
	return string(source)
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

	cfg, err := s.store.Load()
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

	if cred.Type != config.CredentialTypeKeyring && cred.Type != config.CredentialTypeVault {
		fail("this credential's secrets come from the environment variables it names, so there is nothing to replace")
		return
	}
	if !contains(cfg.SecretRolesOf(cred.Provider), role) {
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

	var warning string
	if cred.Type == config.CredentialTypeVault {
		if err := s.secrets.SetVault(name, role, value, vaultOfferFromForm(r)); err != nil {
			fail(s.redact(err.Error()))
			return
		}
		warning = s.syncVaultProcess(func(ctx context.Context, client *vaultproc.Client) error {
			return client.Set(ctx, name, role, value)
		})
	} else {
		if err := s.secrets.Set(name, role, value); err != nil {
			fail(s.redact(err.Error()))
			return
		}
	}

	target := "/credentials/" + url.PathEscape(name) + "?replaced=" + url.QueryEscape(role)
	if warning != "" {
		target += "&warning=" + url.QueryEscape(warning)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// syncVaultProcess hands a change already written to the vault on to a vault process that holds it unlocked
// outside this run, the same way internal/tui/vaultsettings.go's own helper of this name does for the
// editor, and internal/cli/vault.go's own does for the CLI.
func (s *Server) syncVaultProcess(change func(context.Context, *vaultproc.Client) error) string {
	v := s.secrets.Vault()
	if !vaultproc.Supported || v == nil {
		return ""
	}
	if err := vaultmigrate.SyncChange(context.Background(), v, change); err != nil {
		return fmt.Sprintf("warning: the vault holds the change, but the vault process that holds it "+
			"unlocked could not take it and still answers with what it held before: %s; %s",
			s.redact(err.Error()), s.redact(secret.VaultProcessRemedy(err)))
	}
	return ""
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
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web · add a credential</title></head>
<body>
<p><a href="/">&larr; overview</a></p>
<h1>Add a credential</h1>
{{if .Error}}<p>{{.Error}}</p>{{end}}

<h2>1. Provider</h2>
<form method="get" action="/credentials/new">
<label>Provider
<select name="provider">
<option value="">choose a provider</option>
{{range .Providers}}<option value="{{.}}"{{if eq . $.Selected}} selected{{end}}>{{.}}</option>
{{end}}
</select>
</label>
<button type="submit">Choose</button>
</form>

{{if .Selected}}
<h2>2. Name and secrets</h2>
<form method="post" action="/credentials/new">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="cfgver" value="{{.CfgVer}}">
<input type="hidden" name="provider" value="{{.Selected}}">
<p><label>Name <input type="text" name="name" value="{{.Name}}" autocomplete="off"></label></p>

<p>Secrets are kept in:</p>
<label><input type="radio" name="storage" value="keyring"{{if eq .Storage "keyring"}} checked{{end}}> the system keyring</label><br>
<label><input type="radio" name="storage" value="vault"{{if eq .Storage "vault"}} checked{{end}}> the vault</label><br>
<label><input type="radio" name="storage" value="env"{{if eq .Storage "env"}} checked{{end}}> environment variables</label>

{{range .Roles}}
<fieldset>
<legend>{{.Name}}</legend>
{{if .Description}}<p>{{.Description}}</p>{{end}}
<p><label>Secret value, if secrets are kept in the system keyring or the vault
<input type="password" name="secret_{{.Name}}" autocomplete="new-password"></label></p>
<p><label>Environment variable name, if secrets are kept in environment variables
<input type="text" name="envname_{{.Name}}" autocomplete="off"></label></p>
</fieldset>
{{end}}

<fieldset>
<legend>Vault passphrase</legend>
<p>Only used if this credential's secrets go to the vault and this is the vault's very first secret.
Leave both empty and it stays unencrypted: its secrets are then readable by anyone who can read that file.
Type a passphrase, twice, to encrypt it instead.</p>
<p><label>New passphrase <input type="password" name="vault_passphrase" autocomplete="new-password"></label></p>
<p><label>Confirm <input type="password" name="vault_passphrase_confirm" autocomplete="new-password"></label></p>
</fieldset>

<button type="submit">Create credential</button>
</form>
{{end}}
</body>
</html>
{{end}}

{{define "detail"}}
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web · {{.Name}}</title></head>
<body>
<p><a href="/">&larr; overview</a></p>
<h1>{{.Name}}</h1>
<p>Provider: {{.Provider}} · Type: {{.Type}}</p>
{{if .Notice}}<p>{{.Notice}}</p>{{end}}
{{if .Error}}<p>{{.Error}}</p>{{end}}

<table border="1" cellpadding="4">
<tr><th>Role</th><th>Status</th><th>Replace</th></tr>
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
<label>New secret <input type="password" name="value" autocomplete="new-password"></label>
<label>Vault passphrase, only for the vault's very first secret
<input type="password" name="vault_passphrase" autocomplete="new-password"></label>
<label>Confirm <input type="password" name="vault_passphrase_confirm" autocomplete="new-password"></label>
<button type="submit">Replace</button>
</form>
{{end}}
</td>
</tr>
{{end}}
</table>
</body>
</html>
{{end}}

{{define "connection-provider"}}
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web · set up a connection</title></head>
<body>
<p><a href="/">&larr; overview</a></p>
<h1>Set up a connection · step 1 of 3 · Provider</h1>
<p>Choose the system to connect to. The next steps offer only what this provider defines.</p>
{{if .Error}}<p>{{.Error}}</p>{{end}}
<form method="get" action="/connections/new">
<label>Provider
<select name="provider">
<option value="">choose a provider</option>
{{range .Providers}}<option value="{{.}}">{{.}}</option>
{{end}}
</select>
</label>
<button type="submit">Choose</button>
</form>
</body>
</html>
{{end}}

{{define "connection-build"}}
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web · set up a connection</title></head>
<body>
<p><a href="/connections/new">&larr; choose another provider</a></p>
<h1>Set up a connection · step 2 of 3 · Service, credential, scope, permissions</h1>
{{if .Error}}<p>{{.Error}}</p>{{end}}
<form method="get" action="/connections/new/review">
<input type="hidden" name="provider" value="{{.Provider}}">

<h2>Service</h2>
<label>Reuse a service, or choose {{printf "%s" "(new service)"}}
<select name="service">
{{range .Services}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>
{{end}}
</select>
</label>
<p><label>New service name <input type="text" name="svcname" value="{{.Form.SvcName}}" autocomplete="off"></label></p>
<p><label>New service base URL <input type="text" name="svcbaseurl" value="{{.Form.SvcBaseURL}}" autocomplete="off"></label></p>

<h2>Credential</h2>
<label>Reuse a credential, or choose {{printf "%s" "(new credential)"}}
<select name="credential">
{{range .Credentials}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>
{{end}}
</select>
</label>
<p><label>New credential name <input type="text" name="credname" value="{{.Form.CredName}}" autocomplete="off"></label></p>
<p>Secrets of a new credential are kept in:</p>
<label><input type="radio" name="credstorage" value="keyring"{{if eq .Form.CredStorage "keyring"}} checked{{end}}> the system keyring</label><br>
<label><input type="radio" name="credstorage" value="vault"{{if eq .Form.CredStorage "vault"}} checked{{end}}> the vault</label><br>
<label><input type="radio" name="credstorage" value="env"{{if eq .Form.CredStorage "env"}} checked{{end}}> environment variables</label>
<p>{{.StorageHint}}</p>
{{$form := .Form}}
{{range .Roles}}
<p><label>{{.Name}} environment variable name, only used if a new credential keeps its secrets in environment variables
<input type="text" name="envname_{{.Name}}" value="{{index $form.EnvNames .Name}}" autocomplete="off"></label>
{{if .Description}}<br>{{.Description}}{{end}}
{{if ne $form.CredStorage "env"}}<br>Its secret value, if kept in the system keyring or the vault, is typed on the next page.{{end}}</p>
{{end}}

<h2>Scope</h2>
<p><label>Connection name <input type="text" name="connname" value="{{.Form.ConnName}}" autocomplete="off"></label></p>
<p><label>Targets <input type="text" name="targets" value="{{.Form.Targets}}" autocomplete="off"></label><br>{{.TargetHint}}</p>
<p><label>Description <input type="text" name="description" value="{{.Form.Description}}" autocomplete="off"></label></p>
{{if or .FilesRead .FilesWrite}}
<h2>Local files</h2>
<p>Directories on this machine the tools of this connection may use, absolute or starting with ~/, one per line. A directory gives access to it and everything below. Leave a field empty to release nothing. A change later needs a new approval.</p>
{{if .FilesRead}}<p><label>Upload directories (files the tools may read)<br><textarea name="filesread" rows="3" cols="60" autocomplete="off">{{.Form.FilesRead}}</textarea></label></p>{{end}}
{{if .FilesWrite}}<p><label>Download directories (where the tools may write files)<br><textarea name="fileswrite" rows="3" cols="60" autocomplete="off">{{.Form.FilesWrite}}</textarea></label></p>{{end}}
{{end}}

<h2>Permissions</h2>
<label><input type="radio" name="permmode" value="default"{{if eq .Form.PermMode "default"}} checked{{end}}> use the provider's default</label><br>
<label><input type="radio" name="permmode" value="custom"{{if eq .Form.PermMode "custom"}} checked{{end}}> choose explicitly</label>
{{range .Permissions}}<br><label><input type="checkbox" name="perm" value="{{.Value}}"{{if .Selected}} checked{{end}}> {{.Label}}</label>
{{end}}

<h2>Tools</h2>
<label><input type="radio" name="toolsmode" value="all"{{if eq .Form.ToolsMode "all"}} checked{{end}}> offer every tool the permissions allow</label><br>
<label><input type="radio" name="toolsmode" value="selected"{{if eq .Form.ToolsMode "selected"}} checked{{end}}> offer only the tools ticked below</label>
{{range .Tools}}<br><label><input type="checkbox" name="tool" value="{{.ID}}"{{if .Selected}} checked{{end}}> {{.ID}} ({{.Effect}})</label>
{{end}}

<p><button type="submit">Review</button></p>
</form>
</body>
</html>
{{end}}

{{define "connection-review"}}
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web · set up a connection</title></head>
<body>
<p><a href="/connections/new?provider={{.Form.Provider}}">&larr; back</a></p>
<h1>Set up a connection · step 3 of 3 · Review</h1>
{{if .Error}}<p>{{.Error}}</p>{{end}}
<p>Nothing is written yet. This summary carries no secret.</p>
<table border="1" cellpadding="4">
{{range .Summary}}<tr><th>{{.Label}}</th><td>{{.Value}}</td></tr>
{{end}}
</table>

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
<input type="hidden" name="toolsmode" value="{{.Form.ToolsMode}}">
{{range .Form.Tools}}<input type="hidden" name="tool" value="{{.}}">
{{end}}

{{if .NewCredential}}
<h2>Secrets for the new credential {{.CredentialName}}</h2>
{{if eq .Storage "env"}}
<p>This credential keeps its secrets in environment variables, already named on the previous page: nothing more to type here.</p>
{{else}}
{{range .Roles}}
<fieldset>
<legend>{{.Name}}</legend>
{{if .Description}}<p>{{.Description}}</p>{{end}}
<p><label>Secret value <input type="password" name="secret_{{.Name}}" autocomplete="new-password"></label></p>
</fieldset>
{{end}}
<fieldset>
<legend>Vault passphrase</legend>
<p>Only used if this credential's secrets go to the vault and this is the vault's very first secret.
Leave both empty and it stays unencrypted. Type a passphrase, twice, to encrypt it instead.</p>
<p><label>New passphrase <input type="password" name="vault_passphrase" autocomplete="new-password"></label></p>
<p><label>Confirm <input type="password" name="vault_passphrase_confirm" autocomplete="new-password"></label></p>
</fieldset>
{{end}}
{{end}}

<button type="submit">Create connection</button>
</form>
</body>
</html>
{{end}}

{{define "connection-result"}}
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>qatlas web · {{.Name}}</title></head>
<body>
<p><a href="/">&larr; overview</a></p>
<h1>{{.Name}}</h1>
{{if .Notice}}<p>{{.Notice}}</p>{{end}}
{{if .Error}}<p>{{.Error}}</p>{{end}}
<table border="1" cellpadding="4">
<tr><th>Provider</th><td>{{.Provider}}</td></tr>
<tr><th>Service</th><td>{{.Service}}</td></tr>
<tr><th>Credential</th><td>{{.Credential}}</td></tr>
<tr><th>Targets</th><td>{{.Targets}}</td></tr>
<tr><th>Files</th><td>{{.Files}}</td></tr>
<tr><th>Description</th><td>{{.Description}}</td></tr>
<tr><th>Permissions</th><td>{{.Permissions}}</td></tr>
<tr><th>Tools</th><td>{{.Tools}}</td></tr>
</table>

{{if .TestResult}}<p>{{.TestResult}}</p>{{end}}
{{if .TesterAvailable}}
<form method="post" action="/connections/{{.Name}}/test">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<button type="submit">Test this connection</button>
</form>
{{else}}
<p>Connection testing is not available for this run.</p>
{{end}}
</body>
</html>
{{end}}
`
