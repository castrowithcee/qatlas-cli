package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// Renames the vault shows that the configuration does not (a connection or credential renamed by hand, and
// the rest of a rename that stopped early) are found by the core (manage.RenameFindings). The overview and
// the page of the entry name them and link to the action the core offers for each: adopt a hand rename,
// finish an unfinished one, or put a credential's back. An action is the same two requests as any rename: a
// GET that shows the dry run and a POST behind withAdminGuard that confirms it. An ambiguous finding is only
// reported; no link and no route acts on it.

const (
	findingPlanPath = "/renames/plan"
	findingRunPath  = "/renames/run"

	findingAdopt  = "adopt"
	findingFinish = "finish"
	findingRevert = "revert"
)

const errFindingChanged = "what the vault shows of this rename changed since this page was opened; " +
	"open the overview again"

// findingAction is one link that opens the dry run of a finding's action.
type findingAction struct{ Label, Href string }

// findingView is one finding as a page shows it: a redacted line and the actions the core offers for it.
type findingView struct {
	Kind, Entry, Text string
	Actions           []findingAction
}

// findingMode is the action a finding is resolved by.
func findingMode(f manage.RenameFinding) string {
	if f.Leftover {
		return findingFinish
	}
	return findingAdopt
}

func findingHref(f manage.RenameFinding, mode string) string {
	q := url.Values{"kind": {f.Kind}, "from": {f.From}, "to": {f.To}, "mode": {mode}}
	return findingPlanPath + "?" + q.Encode()
}

// loadFindingConfig loads the configuration for the overview, which a run without a store does not have.
func (s *Server) loadFindingConfig() (*config.Config, error) {
	if s.svc == nil {
		return nil, errors.New("no configuration store is configured for this run")
	}
	return s.loadConfig()
}

// renameFindings reads the findings of cfg; the vault is read only where it needs no passphrase.
func (s *Server) renameFindings(cfg *config.Config) []manage.RenameFinding {
	if s.secrets == nil {
		return nil
	}
	return manage.RenameFindings(cfg, s.secrets.Vault())
}

// findingViews words the findings, optionally only those of one entry (kind and name, "" for all).
func (s *Server) findingViews(cfg *config.Config, kind, name string) []findingView {
	var out []findingView
	for _, f := range s.renameFindings(cfg) {
		if kind != "" && (f.Kind != kind || f.Entry != name) {
			continue
		}
		out = append(out, s.findingView(f))
	}
	return out
}

func (s *Server) findingView(f manage.RenameFinding) findingView {
	view := findingView{Kind: f.Kind, Entry: s.redact(f.Entry)}
	what := "approval"
	if f.Kind == manage.RenameCredential {
		what = "entry"
	}
	switch {
	case f.Ambiguous:
		view.Text = fmt.Sprintf("The vault holds a %s %s under the name %s that could belong to more than one "+
			"configured %s, or the other way round; nothing is suggested, so there is no action here.",
			f.Kind, what, f.From, f.Kind)
	case f.Leftover && f.Kind == manage.RenameConnection:
		view.Text = fmt.Sprintf("Connection %s has an unfinished rename to %s in the vault: the approval of %s is "+
			"left over. Finishing the rename gives the connection the name %s.", f.From, f.To, f.To, f.To)
		view.Actions = []findingAction{{"Finish the rename", findingHref(f, findingFinish)}}
	case f.Leftover:
		view.Text = fmt.Sprintf("Credential %s has an unfinished rename to %s in the vault; until it is resolved, "+
			"%s is not read from the vault.", f.From, f.To, f.From)
		view.Actions = []findingAction{{"Finish the rename", findingHref(f, findingFinish)},
			{"Put it back", findingHref(f, findingRevert)}}
	default:
		view.Text = fmt.Sprintf("%s %s was renamed by hand from %s; the vault still holds its %s under the old "+
			"name. Adopting the rename makes the %s follow the configuration.", capital(f.Kind), f.To, f.From,
			what, what)
		view.Actions = []findingAction{{"Adopt the rename", findingHref(f, findingAdopt)}}
	}
	view.Text = s.redact(view.Text)
	return view
}

func capital(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// findingByRequest finds, among the current findings, the one with the kind, names and mode of the request.
// Only a finding the core offers that action for matches: an ambiguous one never does.
func (s *Server) findingByRequest(cfg *config.Config, get func(string) string) (manage.RenameFinding, string, bool) {
	kind, from, to, mode := get("kind"), get("from"), get("to"), get("mode")
	for _, f := range s.renameFindings(cfg) {
		if f.Ambiguous || f.Kind != kind || f.From != from || f.To != to {
			continue
		}
		switch {
		case mode == findingRevert && f.CanRevert(), mode == findingMode(f) && mode != findingRevert:
			return f, mode, true
		}
	}
	return manage.RenameFinding{}, "", false
}

// refuseFinding answers a request for an action that no finding offers.
func refuseFinding(w http.ResponseWriter) {
	http.Error(w, "no such rename to resolve", http.StatusConflict)
}

// findingData starts the rename page for f's plan.
func (s *Server) findingData(f manage.RenameFinding, mode string) renameData {
	data := renameData{Kind: f.Kind, Old: f.From, New: f.To, Mode: mode, Back: "/", CSRF: s.csrfValue()}
	switch mode {
	case findingAdopt:
		data.Title = fmt.Sprintf("Adopt the rename of %s %s to %s", f.Kind, f.From, f.To)
		data.Button = "Adopt the rename"
	case findingFinish:
		data.Title = fmt.Sprintf("Finish the rename of %s %s to %s", f.Kind, f.From, f.To)
		data.Button = "Finish the rename"
	default:
		data.Title = fmt.Sprintf("Put %s %s back", f.Kind, f.From)
		data.Button = "Put it back"
	}
	data.Title = s.redact(data.Title)
	return data
}

// handleFindingPlan shows the dry run of a finding's action. It changes nothing.
func (s *Server) handleFindingPlan(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	f, mode, ok := s.findingByRequest(cfg, r.URL.Query().Get)
	if !ok {
		refuseFinding(w)
		return
	}
	data := s.findingData(f, mode)
	fp, err := s.configFingerprint()
	if err != nil {
		data.Error = s.redact(err.Error())
		s.renderRename(w, data)
		return
	}
	admin := s.adminSessionActive()
	plan, err := s.svc.PlanFinding(f, mode == findingRevert, admin)
	if err != nil {
		data.Error = s.redact(err.Error())
		s.renderRename(w, data)
		return
	}
	s.fillRename(&data, plan)
	data.CfgVer, data.Digest = fp, renameDigest(data)
	data.Action = findingRunPath
	data.Confirm = admin && !plan.VaultUnread
	s.renderRename(w, data)
}

// handleFindingRun plans the action again in the admin session and carries it out only if the finding, the
// configuration and the plan are those the confirming form was shown against.
func (s *Server) handleFindingRun(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	f, mode, ok := s.findingByRequest(cfg, r.PostFormValue)
	if !ok {
		refuseFinding(w)
		return
	}
	data := s.findingData(f, mode)
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
	plan, err := s.svc.PlanFinding(f, mode == findingRevert, true)
	if err != nil {
		data.Error = s.redact(err.Error())
		s.renderRename(w, data)
		return
	}
	s.fillRename(&data, plan)
	if renameDigest(data) != r.PostFormValue("digest") {
		data = s.findingData(f, mode)
		data.Error = errRenamePlanChanged
		s.renderRename(w, data)
		return
	}
	res, err := s.svc.Rename(r.Context(), plan)
	if err != nil {
		data = s.findingData(f, mode)
		data.Error = s.saveFailure(err)
		s.renderRename(w, data)
		return
	}
	entry, lead := f.To, ""
	switch mode {
	case findingAdopt:
		lead = fmt.Sprintf("Adopted the rename of %s %s to %s.", f.Kind, f.From, f.To)
	case findingFinish:
		lead = fmt.Sprintf("Finished the rename of %s %s to %s.", f.Kind, f.From, f.To)
	default:
		entry, lead = f.From, fmt.Sprintf("Put %s %s back.", f.Kind, f.From)
	}
	s.setNotice(s.renameNotice(lead, res))
	http.Redirect(w, r, renamePath(f.Kind, entry), http.StatusSeeOther)
}

const findingTemplates = `
{{define "findings"}}{{range .}}<div class="notice" role="status">
<p>{{.Text}}</p>
{{if .Actions}}<p class="actions">{{range .Actions}}<a href="{{.Href}}">{{.Label}}</a> {{end}}</p>{{end}}
</div>
{{end}}{{end}}
`
