package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// The JSON routes the script's target chooser reads a connection in setup with. They accept a form-encoded
// POST like every other route: the draft is read from the same non-secret fields the build page carries,
// plus csrf, so no secret and no CSRF value ever sits in a URL, and a new credential's secrets are never
// accepted here.
const (
	targetListPath    = "/connections/new/targets/list"
	targetResolvePath = "/connections/new/targets/resolve"
)

// targetEntryJSON and the responses are the stable wire shapes of the chooser. Provider text appears only
// as JSON strings.
type targetEntryJSON struct {
	Target      string `json:"target"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Path        string `json:"path,omitempty"`
	HasChildren bool   `json:"has_children,omitempty"`
	// Excludable says the core accepts the entry as an exclusion of the connection's provider.
	Excludable bool `json:"excludable,omitempty"`
}

type targetListResponse struct {
	Entries      []targetEntryJSON `json:"entries"`
	Next         string            `json:"next,omitempty"`
	ServerSearch bool              `json:"server_search"`
	Warning      string            `json:"warning,omitempty"`
}

type targetResolveResponse struct {
	Entries []targetEntryJSON `json:"entries"`
	Warning string            `json:"warning,omitempty"`
}

type targetErrorResponse struct {
	Error targetErrorBody `json:"error"`
}

type targetErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// withTargetGuard is withAdminGuard for a read: the same session, Origin and CSRF checks, and the admin
// approval spent exactly as a mutation spends it. A read is not exempt because it reads the credential's
// secret and the provider's data, and with vault.admin_timeout of 0 it therefore uses up the one approval
// like a write does (the TUI does the same). It does not take writeMu, since it writes no configuration.
// Without an active approval the proof stays empty and the core answers admin-required for an encrypted
// vault; it needs none otherwise.
func (s *Server) withTargetGuard(next func(w http.ResponseWriter, r *http.Request, proof manage.AdminProof)) http.HandlerFunc {
	return s.withSessionGuard(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		proof, _ := s.admin.Authorize(s.adminTimeout)
		s.mu.Unlock()
		next(w, r, proof)
	})
}

func writeTargetJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeTargetError answers with the fixed text of manage.TargetChoiceError. Any other failure (an invalid
// draft, an unreadable configuration) is reported as invalid-request: its own text can carry what was typed.
func writeTargetError(w http.ResponseWriter, err error) {
	var choice *manage.TargetChoiceError
	if !errors.As(err, &choice) {
		choice = &manage.TargetChoiceError{Code: "invalid-request"}
	}
	status := http.StatusBadGateway
	switch choice.Code {
	case "invalid-request":
		status = http.StatusBadRequest
	case "admin-required":
		status = http.StatusForbidden
	}
	writeTargetJSON(w, status, targetErrorResponse{Error: targetErrorBody{Code: choice.Code, Message: choice.Error()}})
}

// targetDraft reads the connection in setup from the request's non-secret form fields.
func (s *Server) targetDraft(r *http.Request) (manage.ConnectionDraft, config.TargetMetadata, error) {
	if s.svc == nil {
		return manage.ConnectionDraft{}, config.TargetMetadata{}, errors.New("no configuration store is configured for this run")
	}
	cfg, _, err := s.svc.Load()
	var notFound *config.NotFoundError
	if errors.As(err, &notFound) {
		cfg, err = s.svc.NewConfig(), nil
	}
	if err != nil {
		return manage.ConnectionDraft{}, config.TargetMetadata{}, err
	}
	f := parseConnectionForm(r.PostForm)
	metadata, _ := cfg.ProviderMetadata(f.Provider)
	return connectionDraft(cfg, f), metadata.Target, nil
}

func targetEntries(entries []config.TargetEntry, metadata config.TargetMetadata) []targetEntryJSON {
	out := make([]targetEntryJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, targetEntryJSON{Target: e.Target, Name: e.Name, Kind: e.Kind, Path: e.Path,
			HasChildren: e.HasChildren, Excludable: canExclude(metadata, e.Target)})
	}
	return out
}

// handleTargetList answers one page of one level of the targets the draft's provider offers. The form
// fields kind, parent, query and cursor are the config.TargetListRequest.
func (s *Server) handleTargetList(w http.ResponseWriter, r *http.Request, proof manage.AdminProof) {
	draft, metadata, err := s.targetDraft(r)
	if err != nil {
		writeTargetError(w, err)
		return
	}
	req := config.TargetListRequest{
		Kind: r.PostFormValue("kind"), Parent: r.PostFormValue("parent"),
		Query: r.PostFormValue("query"), Cursor: r.PostFormValue("cursor"),
	}
	page, warning, err := s.svc.ListDraftTargets(r.Context(), proof, draft, req)
	if err != nil {
		writeTargetError(w, err)
		return
	}
	writeTargetJSON(w, http.StatusOK, targetListResponse{
		Entries: targetEntries(page.Entries, metadata), Next: page.Next, ServerSearch: page.ServerSearch, Warning: warning,
	})
}

// handleTargetResolve turns the form field url, a web URL of the service, into targets.
func (s *Server) handleTargetResolve(w http.ResponseWriter, r *http.Request, proof manage.AdminProof) {
	draft, metadata, err := s.targetDraft(r)
	if err != nil {
		writeTargetError(w, err)
		return
	}
	entries, warning, err := s.svc.ResolveDraftTargetURL(r.Context(), proof, draft, r.PostFormValue("url"))
	if err != nil {
		writeTargetError(w, err)
		return
	}
	writeTargetJSON(w, http.StatusOK, targetResolveResponse{Entries: targetEntries(entries, metadata), Warning: warning})
}

// targetDialogData is what the build page hands the script's chooser: routes, the kinds and their parent
// relation, and the CSRF value (as an attribute, never as a form field, since the build form submits by GET).
type targetDialogData struct {
	CSRF, ListURL, ResolveURL, Script, Noun string
	Kinds                                   string
	Multiple                                bool
}

type targetKindJSON struct {
	Name    string   `json:"name"`
	Parents []string `json:"parents,omitempty"`
	Nested  bool     `json:"nested,omitempty"`
	Top     bool     `json:"top,omitempty"`
	// Unlisted kinds have no list; the script offers no list when every kind is unlisted.
	Unlisted bool `json:"unlisted,omitempty"`
}

func newTargetDialogData(t config.TargetMetadata, csrf string) *targetDialogData {
	kinds := make([]targetKindJSON, 0, len(t.Kinds))
	for _, k := range t.Kinds {
		kinds = append(kinds, targetKindJSON{Name: k.Name, Parents: k.Parents, Nested: k.Nested, Top: k.Top, Unlisted: k.Unlisted})
	}
	b, _ := json.Marshal(kinds)
	return &targetDialogData{
		CSRF: csrf, ListURL: targetListPath, ResolveURL: targetResolvePath,
		Script: scriptPrefix + "target-dialog.js", Noun: t.Label, Kinds: string(b), Multiple: t.Multiple,
	}
}
