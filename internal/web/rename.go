package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// Renaming a credential or a connection is two requests over the management core's own operation: a GET
// that plans the rename and shows every place it touches without changing anything, and a POST behind
// withAdminGuard that confirms it. The confirming request carries the configuration's fingerprint and a
// digest of the plan it was shown; it plans again and refuses, as a conflict, to run anything that is not
// exactly the plan on the page. The page logic is only presentation: names, references, approvals, the
// keyring and the vault are all the core's.

// renameRow is one place a rename touches, as one label and one line of text.
type renameRow struct{ Label, Text string }

// renameData is what the rename page renders. Rows stays empty on a page that only reports an error.
type renameData struct {
	Kind, Old, New string
	Rows           []renameRow
	Lapsed         string
	Confirm        bool
	Unread         bool
	CfgVer, Digest string
	CSRF           string
	Error          string
	// Action is the confirming POST route, Back the page of the entry that keeps its name.
	Action, Back string
	// Mode, Title and Button are set for a finding's action (see renamefind.go): the action, the heading and
	// the confirming button.
	Mode, Title, Button string
}

func renamePath(kind, name string) string {
	return "/" + kind + "s/" + url.PathEscape(name)
}

// adminSessionActive reports whether this run's admin approval is active right now, without spending it.
func (s *Server) adminSessionActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adminActiveLocked()
}

func (s *Server) handleRenameCredentialPlan(w http.ResponseWriter, r *http.Request) {
	s.handleRenamePlan(w, r, manage.RenameCredential)
}

func (s *Server) handleRenameConnectionPlan(w http.ResponseWriter, r *http.Request) {
	s.handleRenamePlan(w, r, manage.RenameConnection)
}

func (s *Server) handleRenameCredentialConfirm(w http.ResponseWriter, r *http.Request) {
	s.handleRenameConfirm(w, r, manage.RenameCredential)
}

func (s *Server) handleRenameConnectionConfirm(w http.ResponseWriter, r *http.Request) {
	s.handleRenameConfirm(w, r, manage.RenameConnection)
}

// renameKnown reports whether the entry exists, answering 404 when it does not.
func (s *Server) renameKnown(w http.ResponseWriter, kind, name string) bool {
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return false
	}
	known := false
	if kind == manage.RenameCredential {
		_, known = cfg.Credentials[name]
	} else {
		_, known = cfg.Connections[name]
	}
	if !known {
		http.Error(w, "unknown "+kind, http.StatusNotFound)
	}
	return known
}

// handleRenamePlan shows the dry run of renaming the entry to the name in "to". It changes nothing. The
// confirming form appears only while the admin approval is active, and the plan counts approvals as carried
// only then, so the page shows what the confirmation will do.
func (s *Server) handleRenamePlan(w http.ResponseWriter, r *http.Request, kind string) {
	if !s.credentialsReady(w) {
		return
	}
	old := r.PathValue("name")
	if !s.renameKnown(w, kind, old) {
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("to"))
	data := renameData{Kind: kind, Old: old, New: name, Back: renamePath(kind, old), CSRF: s.csrfValue()}
	if name == "" {
		data.Error = "type the new name"
		s.renderRename(w, data)
		return
	}
	// Read before planning: a change while planning then fails the confirmation instead of slipping by.
	fp, err := s.configFingerprint()
	if err != nil {
		data.Error = s.redact(err.Error())
		s.renderRename(w, data)
		return
	}
	admin := s.adminSessionActive()
	plan, err := s.svc.PlanRename(kind, old, name, admin)
	if err != nil {
		data.Error = s.redact(err.Error())
		s.renderRename(w, data)
		return
	}
	s.fillRename(&data, plan)
	data.CfgVer, data.Digest = fp, renameDigest(data)
	data.Action = renamePath(kind, old) + "/rename"
	data.Confirm = admin && !plan.VaultUnread
	s.renderRename(w, data)
}

// handleRenameConfirm plans the rename again in the admin session and carries it out only if the
// configuration and the plan are those the confirming form was shown against.
func (s *Server) handleRenameConfirm(w http.ResponseWriter, r *http.Request, kind string) {
	if !s.credentialsReady(w) {
		return
	}
	old := r.PathValue("name")
	if !s.renameKnown(w, kind, old) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("to"))
	data := renameData{Kind: kind, Old: old, New: name, Back: renamePath(kind, old), CSRF: s.csrfValue()}
	fp, err := s.configFingerprint()
	if err != nil {
		data.Error = s.redact(err.Error())
		s.renderRename(w, data)
		return
	}
	if fp != r.PostFormValue("cfgver") {
		data.Error = errConfigChanged
		s.renderRename(w, data)
		return
	}
	plan, err := s.svc.PlanRename(kind, old, name, true)
	if err != nil {
		data.Error = s.redact(err.Error())
		s.renderRename(w, data)
		return
	}
	s.fillRename(&data, plan)
	if renameDigest(data) != r.PostFormValue("digest") {
		data = renameData{Kind: kind, Old: old, New: name, Back: data.Back, CSRF: data.CSRF, Error: errRenamePlanChanged}
		s.renderRename(w, data)
		return
	}
	res, err := s.svc.Rename(r.Context(), plan)
	if err != nil {
		data = renameData{Kind: kind, Old: old, New: name, Back: data.Back, CSRF: data.CSRF}
		if errors.Is(err, config.ErrConflict) {
			data.Error = errConfigChanged
		} else {
			data.Error = s.redact(err.Error())
		}
		s.renderRename(w, data)
		return
	}
	s.setNotice(s.renameNotice(fmt.Sprintf("Renamed %s %s to %s.", kind, old, name), res))
	http.Redirect(w, r, renamePath(kind, name), http.StatusSeeOther)
}

const errRenamePlanChanged = "what the rename touches changed since this page was opened; open the rename again to see it"

// renameNotice words what a finished rename did beyond the configuration, redacted, for the next page.
func (s *Server) renameNotice(lead string, res manage.RenameResult) string {
	parts := []string{lead}
	if len(res.VaultCopies) > 0 {
		parts = append(parts, "The vault files are backed up as "+strings.Join(res.VaultCopies, ", ")+".")
	}
	if len(res.Carried) > 0 {
		parts = append(parts, "Approvals carried over: "+strings.Join(res.Carried, ", ")+".")
	}
	if len(res.EnvVars) > 0 {
		pairs := make([]string, len(res.EnvVars))
		for i, env := range res.EnvVars {
			pairs[i] = env.Old + " -> " + env.New
		}
		parts = append(parts, "Environment variables that override the stored secrets are read under the new name: "+
			strings.Join(pairs, ", ")+"; qatlas changed no environment.")
	}
	for _, warning := range res.Warnings {
		parts = append(parts, "Warning: "+warning)
	}
	return s.redact(strings.Join(parts, " "))
}

// fillRename describes plan on data: the places it touches, redacted, and the lapsing token cover.
func (s *Server) fillRename(data *renameData, plan *manage.RenamePlan) {
	add := func(label, format string, args ...any) {
		data.Rows = append(data.Rows, renameRow{label, s.redact(fmt.Sprintf(format, args...))})
	}
	add("Id", "%s, kept", plan.ID)
	add("Entry", "%ss.%s -> %ss.%s", plan.Kind, plan.Old, plan.Kind, plan.New)
	for _, ref := range plan.References {
		add("Rewrites", "%s", ref)
	}
	data.Unread = plan.VaultUnread
	if plan.Kind == manage.RenameConnection {
		add("Approval", "%s", renameApprovalText(plan))
	} else {
		s.fillCredentialRename(add, plan)
	}
	if len(plan.LapsedTokens) > 0 {
		data.Lapsed = s.redact(fmt.Sprintf("Ends the cover of agent tokens through %s: %s. A token whose "+
			"vorbild is affected no longer covers anything through it; tokens are never rewritten.",
			plan.Old, strings.Join(plan.LapsedTokens, ", ")))
	}
	for _, outside := range plan.Outside {
		add("Outside", "%s, not changed", outside)
	}
}

func (s *Server) fillCredentialRename(add func(label, format string, args ...any), plan *manage.RenamePlan) {
	if plan.Resumes {
		add("Resumes", "a rename to %s that stopped before the configuration was saved; what it already moved "+
			"and carried over stays", plan.New)
	}
	for _, role := range plan.KeyringRoles {
		add("Keyring", "%s/%s -> %s/%s", plan.Old, role, plan.New, role)
	}
	switch {
	case plan.VaultUnread:
		add("Vault", "not read: the vault is locked, so approvals and agent tokens are not shown")
	case plan.VaultEntry:
		add("Vault", "entry %s -> %s, keeping its id and its secrets", plan.Old, plan.New)
	}
	if plan.PendingWrites > 0 {
		add("Pending", "%d pending writes for %s merged into the entry first", plan.PendingWrites, plan.Old)
	}
	for _, reader := range plan.Readers {
		text := renameReaderText(reader)
		if plan.VaultUnread {
			text = "approval not read"
		}
		add("Reader", "%s: %s", reader.Connection, text)
	}
	for _, env := range plan.EnvVars {
		add("Environment", "%s -> %s (%s)", env.Old, env.New, env.Role)
	}
}

func renameReaderText(reader manage.RenameReader) string {
	switch {
	case reader.Approval == "":
		return "no approval to carry over"
	case reader.Carried:
		return "approved; carried over, so it stays approved"
	case reader.Approval == manage.ApprovalOpen:
		return "open; stays open until it is approved"
	}
	return "approved; not carried over, so it needs a new approval"
}

func renameApprovalText(plan *manage.RenamePlan) string {
	switch {
	case plan.VaultUnread:
		return "not read: the vault is locked, so approvals and agent tokens are not shown"
	case plan.Approval == "":
		return "none to carry over"
	case plan.CarryApproval && plan.Approval == manage.ApprovalOpen:
		return "open; carried over, so the connection stays open until it is approved"
	case plan.CarryApproval:
		return "approved; carried over, so the connection stays approved"
	}
	return plan.Approval + "; not carried over, so the connection needs a new approval"
}

// renameDigest identifies the plan a page shows: everything it lists, and the names it is for.
func renameDigest(data renameData) string {
	h := sha256.New()
	for _, part := range []string{data.Kind, data.Old, data.New, data.Lapsed, fmt.Sprint(data.Unread)} {
		fmt.Fprintf(h, "%d:%s\n", len(part), part)
	}
	for _, row := range data.Rows {
		fmt.Fprintf(h, "%d:%s%d:%s\n", len(row.Label), row.Label, len(row.Text), row.Text)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Server) renderRename(w http.ResponseWriter, data renameData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.credTmpl.ExecuteTemplate(w, "rename", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// renameTemplates defines the rename page and the start forms the credential and connection pages embed.
const renameTemplates = `
{{define "rename"}}
{{template "layout-top" (page "Rename" (print .Kind "s"))}}
<h1>{{if .Title}}{{.Title}}{{else}}Rename {{.Kind}} {{.Old}}{{if .New}} to {{.New}}{{end}}{{end}}</h1>
{{if .Error}}<p class="notice notice-error" role="alert">{{.Error}}</p>{{end}}
{{if .Rows}}
<p>Nothing has been changed yet. The rename touches these places:</p>
<section class="section" aria-labelledby="rename-heading">
<h2 id="rename-heading">What the rename touches</h2>
<div class="table-wrap" role="region" aria-labelledby="rename-heading" tabindex="0">
<table>
<tbody>
{{range .Rows}}<tr><th scope="row">{{.Label}}</th><td>{{.Text}}</td></tr>
{{end}}</tbody>
</table>
</div>
</section>
{{if .Lapsed}}<p class="notice notice-error" role="alert">{{.Lapsed}}</p>{{end}}
{{if .Confirm}}
<form method="post" action="{{.Action}}">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="cfgver" value="{{.CfgVer}}">
<input type="hidden" name="digest" value="{{.Digest}}">
<input type="hidden" name="to" value="{{.New}}">
{{if .Mode}}<input type="hidden" name="kind" value="{{.Kind}}">
<input type="hidden" name="from" value="{{.Old}}">
<input type="hidden" name="mode" value="{{.Mode}}">
{{end}}<div class="actions"><button type="submit">{{if .Button}}{{.Button}}{{else}}Rename {{.Kind}}{{end}}</button></div>
</form>
{{else if .Unread}}
<p class="notice" role="status">The vault is locked, so this rename cannot be carried out here. Unlock it and open the rename again.</p>
{{else}}
<p class="notice" role="status">Confirming needs the admin approval of this run. Approve it on the overview, then open the rename again.</p>
{{end}}
{{end}}
<p><a href="{{.Back}}">{{if .Rows}}Cancel{{else}}Back to {{.Old}}{{end}}</a></p>
{{template "layout-bottom"}}
{{end}}

{{define "rename-credential"}}
<section class="section" aria-labelledby="rename-heading">
<h2 id="rename-heading">Rename</h2>
<p>Shows everything a rename touches first and changes nothing until confirmed.</p>
<form method="get" action="/credentials/{{.Name}}/rename">
<div class="field">
<label for="rename-to">New name</label>
<input id="rename-to" type="text" name="to" autocomplete="off">
</div>
<div class="actions"><button type="submit">Review rename</button></div>
</form>
</section>
{{end}}

{{define "rename-connection"}}
<section class="section" aria-labelledby="rename-heading">
<h2 id="rename-heading">Rename</h2>
<p>Shows everything a rename touches first and changes nothing until confirmed.</p>
<form method="get" action="/connections/{{.Name}}/rename">
<div class="field">
<label for="rename-to">New name</label>
<input id="rename-to" type="text" name="to" autocomplete="off">
</div>
<div class="actions"><button type="submit">Review rename</button></div>
</form>
</section>
{{end}}
`
