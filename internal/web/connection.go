package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
)

// The guided connection setup leads a coupled browser from a provider to a saved connection, over the same
// configuration core internal/tui's own guided setup uses (config.ProviderMetadata, Config.SetService,
// Config.SetCredential, Config.SetConnection, Config.Validate): this package adds no second provider or
// permission policy of its own, only HTML pages over the same checks.
//
// The journey is two GET pages, without a script: a provider chooser, and a build page that carries every
// later choice (service, credential, scope, permissions, tools) as ordinary visible or hidden fields. The
// build page's own form leads, by an ordinary GET, to a review page that shows what would be saved,
// secretfree, and, only for a new credential, the fields to type its secrets: exactly there, and nowhere
// earlier, because a GET request would otherwise have carried them in the URL. The review page's own form is
// the one and only POST that ever writes anything, gated by withAdminGuard like every other mutation this
// package makes.
//
// newServiceChoice and newCredentialChoice are the same sentinel words internal/tui's guided setup already
// uses for "none of the above, add one instead": a configuration name can never start with a parenthesis
// (see config.validateName), so neither value can ever collide with a real service or credential.
const (
	newServiceChoice    = "(new service)"
	newCredentialChoice = "(new credential)"
)

// connectionForm is every non-secret choice the guided setup carries from one page to the next, as ordinary
// query or form values. It never carries a secret role's value or a vault passphrase: those are read
// straight off the review page's own POST body and are never round-tripped through this type (see
// handleCreateConnection).
type connectionForm struct {
	Provider                          string
	Service, SvcName, SvcBaseURL      string
	Credential, CredName, CredStorage string
	EnvNames                          map[string]string
	ConnName, Targets, Description    string
	FilesRead, FilesWrite             string
	PermMode                          string
	Perms                             []string
	ToolsMode                         string
	Tools                             []string
	Forward                           []string
}

// parseConnectionForm reads a connectionForm back out of a request's query values (the build and review
// pages, both reached by GET) or its posted form values (the review page's own commit).
func parseConnectionForm(v url.Values) connectionForm {
	envNames := map[string]string{}
	for key, vals := range v {
		if role, ok := strings.CutPrefix(key, "envname_"); ok && len(vals) > 0 {
			envNames[role] = vals[0]
		}
	}
	return connectionForm{
		Provider:    v.Get("provider"),
		Service:     v.Get("service"),
		SvcName:     v.Get("svcname"),
		SvcBaseURL:  v.Get("svcbaseurl"),
		Credential:  v.Get("credential"),
		CredName:    v.Get("credname"),
		CredStorage: v.Get("credstorage"),
		EnvNames:    envNames,
		ConnName:    v.Get("connname"),
		Targets:     v.Get("targets"),
		Description: v.Get("description"),
		FilesRead:   v.Get("filesread"),
		FilesWrite:  v.Get("fileswrite"),
		PermMode:    v.Get("permmode"),
		Perms:       v["perm"],
		ToolsMode:   v.Get("toolsmode"),
		Tools:       v["tool"],
		Forward:     v["forward"],
	}
}

// connectionDraft reads f into the draft the core builds the candidate from. The core checks everything:
// nothing here decides whether a target, a permission or a tool is allowed. A new credential's secret
// roles are never given a value here, except the plain variable names of an env credential, which are
// configuration, not a secret.
func connectionDraft(cfg *config.Config, f connectionForm) manage.ConnectionDraft {
	d := manage.ConnectionDraft{
		Provider:      f.Provider,
		NewService:    f.Service == "" || f.Service == newServiceChoice,
		Service:       f.Service,
		BaseURL:       strings.TrimSpace(f.SvcBaseURL),
		NewCredential: f.Credential == "" || f.Credential == newCredentialChoice,
		Credential:    f.Credential,
		Storage:       f.CredStorage,
		Name:          strings.TrimSpace(f.ConnName),
		Description:   f.Description,
		Forward:       f.Forward,
		Files:         config.Files{Read: splitDirectoryLines(f.FilesRead), Write: splitDirectoryLines(f.FilesWrite)},
	}
	if d.NewService {
		d.Service = strings.TrimSpace(f.SvcName)
	}
	if d.NewCredential {
		d.Credential = strings.TrimSpace(f.CredName)
		d.EnvNames = map[string]string{}
		for _, role := range cfg.SecretRolesOf(f.Provider) {
			d.EnvNames[role] = strings.TrimSpace(f.EnvNames[role])
		}
	}
	d.Targets = splitTargetInput(f.Targets)
	if f.PermMode == "custom" {
		// A custom list without any entry allows nothing; it never falls back to the defaults.
		d.Permissions = strings.Join(f.Perms, ",")
		if strings.TrimSpace(d.Permissions) == "" {
			d.Permissions = "none"
		}
	}
	if f.ToolsMode == "selected" {
		d.ToolsSelected, d.Tools = true, f.Tools
	}
	return d
}

// buildConnectionCandidate is the core's candidate for the whole form.
func buildConnectionCandidate(cfg *config.Config, f connectionForm) (manage.ConnectionCandidate, error) {
	return manage.BuildConnectionCandidate(cfg, connectionDraft(cfg, f), manage.StageRights)
}

// splitTargetInput reads the build page's single, comma-separated targets field into the target/targets
// list the core stores (see manage.SplitTargets). A comma-separated field, not the TUI's own
// add-one-at-a-time list, is this page's own simplification for a form that ships no script to grow a list
// with.
func splitTargetInput(raw string) []string {
	var values []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

// splitDirectoryLines reads a files field, one directory per line, into the list a connection stores; blank
// lines are dropped and none at all is nil. Whether a direction may be used, and every entry, is left to the
// core's own validation of the candidate; a direction the provider has no tool for is refused there, whatever
// the page offered.
func splitDirectoryLines(raw string) []string {
	var values []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			values = append(values, line)
		}
	}
	return values
}

// applyConnectionDefaults fills a fresh build page's own starting choices: the connection name defaults to
// the provider's own name while that name is still free, the new-service base URL defaults to the
// provider's own default, the new-credential storage defaults to this configuration's own default place for
// a new credential, and, where the provider declares one, its recommended tool profile is ticked, exactly
// as a new connection starts in internal/tui's own guided setup. Every tick stays the person's own to change
// from here.
func applyConnectionDefaults(cfg *config.Config, f *connectionForm) {
	start := manage.ConnectionDefaults(cfg, f.Provider)
	if f.ConnName == "" {
		f.ConnName = start.Name
	}
	if f.SvcBaseURL == "" {
		f.SvcBaseURL = start.BaseURL
	}
	if f.CredStorage == "" {
		f.CredStorage = start.Storage
	}
	if start.ProfileID != "" {
		f.PermMode, f.ToolsMode = "custom", "selected"
		f.Perms = make([]string, len(start.Permissions))
		for i, permission := range start.Permissions {
			f.Perms[i] = string(permission)
		}
		f.Tools = start.Tools
	} else {
		f.PermMode, f.ToolsMode = "default", "all"
	}
}

// optionRow is one choice of a select, radio, or checkbox row, with whatever this page already knows was
// chosen worked out server side, since the template itself does no comparison.
type optionRow struct {
	Value    string
	Label    string
	Selected bool
}

func selectOptions(values []string, chosen string) []optionRow {
	rows := make([]optionRow, len(values))
	for i, v := range values {
		rows[i] = optionRow{Value: v, Label: v, Selected: v == chosen}
	}
	return rows
}

func checkedOptions(values, chosen []string) []optionRow {
	set := make(map[string]bool, len(chosen))
	for _, c := range chosen {
		set[c] = true
	}
	rows := make([]optionRow, len(values))
	for i, v := range values {
		rows[i] = optionRow{Value: v, Label: v, Selected: set[v]}
	}
	return rows
}

// connectionProviderData is what the provider-chooser page renders: the same first step /credentials/new
// already offers, reused here for the same choice.
type connectionProviderData struct {
	Providers []string
	Error     string
}

// connectionBuildData is what the build page renders: the provider's own services, credentials, secret
// roles, permissions, and tools, each already marked with whatever this page's own form last chose.
type connectionBuildData struct {
	Provider    string
	Services    []optionRow
	Credentials []optionRow
	Roles       []roleField
	Form        connectionForm
	TargetHint  string
	// FilesRead and FilesWrite say whether the provider has a tool that reads, or writes, local files; only
	// then does the page offer the matching directory field.
	FilesRead, FilesWrite bool
	Permissions           []optionRow
	Tools                 []toolOption
	StorageHint           string
	ForwardChoices        []optionRow
	ForwardText           string
	BindingNote           string
	Error                 string
}

// toolOption is one row of the tool checklist: its ID, the effect it needs permission for, and whether this
// page's form has it ticked.
type toolOption struct {
	ID, Effect string
	Selected   bool
}

// connectionReviewData is what the review page renders: a secretfree summary of what pressing "Create
// connection" would save, the secret role fields of a new credential when there is one to fill in, and every
// earlier choice carried forward as a hidden field, so the one POST this package ever makes for a connection
// carries the whole of it at once.
type connectionReviewData struct {
	Summary        []summaryRow
	NewCredential  bool
	CredentialName string
	Storage        string
	Roles          []roleField
	CfgVer         string
	CSRF           string
	Form           connectionForm
	FormValues     url.Values
	BindingNote    string
	Error          string
}

type summaryRow struct{ Label, Value string }

// renderConnectionProvider shows the provider chooser, the guided connection setup's first step.
func (s *Server) renderConnectionProvider(w http.ResponseWriter, cfg *config.Config, errText string) {
	data := connectionProviderData{Providers: cfg.Providers(), Error: errText}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.credTmpl.ExecuteTemplate(w, "connection-provider", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// renderConnectionBuild shows the build page: service, credential, scope, and permissions/tools, all at
// once, the same way /credentials/new shows a new credential's name, storage, and roles at once once a
// provider is chosen.
func (s *Server) renderConnectionBuild(w http.ResponseWriter, cfg *config.Config, f connectionForm, errText string) {
	metadata, _ := cfg.ProviderMetadata(f.Provider)
	services := append(manage.ProviderServices(cfg, f.Provider), newServiceChoice)
	credentials := append(manage.ProviderCredentials(cfg, f.Provider), newCredentialChoice)

	var roles []roleField
	for _, role := range cfg.SecretRolesOf(f.Provider) {
		roles = append(roles, roleField{Name: role, Description: cfg.SecretRoleDescriptionOf(f.Provider, role)})
	}

	permissionNames := make([]string, len(metadata.SupportedPermissions))
	for i, p := range metadata.SupportedPermissions {
		permissionNames[i] = string(p)
	}
	var tools []toolOption
	selectedTools := make(map[string]bool, len(f.Tools))
	for _, id := range f.Tools {
		selectedTools[id] = true
	}
	for _, tool := range metadata.Tools {
		tools = append(tools, toolOption{ID: tool.ID, Effect: string(tool.Effect), Selected: selectedTools[tool.ID]})
	}

	data := connectionBuildData{
		Provider:    f.Provider,
		Services:    selectOptions(services, f.Service),
		Credentials: selectOptions(credentials, f.Credential),
		Roles:       roles,
		Form:        f,
		TargetHint:  targetHintFor(metadata),
		FilesRead:   metadata.LocalFiles.Read,
		FilesWrite:  metadata.LocalFiles.Write,
		Permissions: checkedOptions(permissionNames, f.Perms),
		Tools:       tools,
		StorageHint: storageHintText,
		Error:       errText,
	}
	if choices := manage.ForwardChoices(cfg); len(choices) > 0 {
		data.ForwardChoices = checkedOptions(choices, f.Forward)
		data.ForwardText = forwardText
		data.BindingNote = s.forwardBindingNote(cfg, "")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.credTmpl.ExecuteTemplate(w, "connection-build", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// targetHintFor says what one target of the provider is and whether the provider allows more than one, the
// same explanation internal/tui's own guided setup gives at its scope step.
func targetHintFor(metadata config.ProviderMetadata) string {
	hint := metadata.Target.Description
	if metadata.Target.Required {
		hint += "; required for " + metadata.Name + ", so it cannot be left empty"
	}
	if !metadata.Target.Multiple {
		hint += "; one " + metadata.Target.Label + " at most"
	} else {
		hint += "; separate more than one with a comma"
	}
	return hint
}

// storageHintText mirrors internal/tui's own storageHint for the same three choices, in the words this
// page's own storage row shows.
const storageHintText = "system keyring keeps the secrets on this machine, with nothing to set up or " +
	"export; vault keeps them in a file beside the configuration, unencrypted unless a passphrase is set " +
	"for it; environment variables suit CI and containers"

// renderConnectionReview shows the review page: a secretfree summary of cand's newly added connection, and,
// only for a new credential, the fields that supply its secrets in the very same request that saves
// everything (see handleCreateConnection). It is reached only from a successfully built candidate, so
// nothing here ever has to explain why a choice was refused; that already happened on the build page.
func (s *Server) renderConnectionReview(w http.ResponseWriter, cfg *config.Config, built manage.ConnectionCandidate,
	f connectionForm, errText string) {
	cand, newCredential := built.Config, built.NewCredential
	conn := cand.Connections[built.Name]
	metadata, _ := cfg.ProviderMetadata(f.Provider)
	serviceState := "existing, unchanged"
	if f.Service == "" || f.Service == newServiceChoice {
		serviceState = "new"
	}
	credentialState := "existing, unchanged"
	storage := ""
	if newCredential {
		credentialState = "new"
		storage = cand.Credentials[conn.Credential].Type
	} else {
		storage = cand.Credentials[conn.Credential].Type
	}

	permissions := "default: " + config.FormatPermissions(manage.DefaultPermissions(metadata))
	if conn.Permissions != nil {
		permissions = config.FormatPermissions(conn.Permissions)
	}
	tools := "all tools its permissions allow"
	switch {
	case conn.Tools == nil:
	case len(conn.Tools) == 0:
		tools = "none: no tool is offered"
	default:
		tools = strings.Join(conn.Tools, ", ")
	}
	targets := strings.Join(conn.TargetValues(), ", ")
	if targets == "" {
		targets = "none"
	}

	summary := []summaryRow{
		{"provider", metadata.Name},
		{"service", cand.Services[conn.Service].BaseURL + " (" + serviceState + ")"},
		{"credential", conn.Credential + " (" + credentialState + ") · " + storage},
		{"connection", f.ConnName},
		{"targets", targets},
		{"description", conn.Description},
		{"files", approval.FilesText(conn.Files.Read, conn.Files.Write)},
		{"permissions", permissions},
		{"tools", tools},
	}
	bindingNote := ""
	if len(conn.ForwardSecrets) > 0 {
		summary = append(summary, summaryRow{"forward_secrets", strings.Join(conn.ForwardSecrets, ", ")})
		bindingNote = s.forwardBindingNote(cand, conn.Credential)
	}

	var roles []roleField
	if newCredential && storage != config.CredentialTypeEnv {
		for _, role := range cfg.SecretRolesOf(f.Provider) {
			roles = append(roles, roleField{Name: role, Description: cfg.SecretRoleDescriptionOf(f.Provider, role)})
		}
	}

	cfgver := ""
	if fp, err := s.configFingerprint(); err == nil {
		cfgver = fp
	}

	data := connectionReviewData{
		Summary: summary, NewCredential: newCredential, CredentialName: conn.Credential, Storage: storage,
		Roles: roles, CfgVer: cfgver, CSRF: s.csrfValue(), Form: f, BindingNote: bindingNote, Error: errText,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.credTmpl.ExecuteTemplate(w, "connection-review", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleConnectionsNew serves the provider chooser and, once a provider is in the query, the build page.
func (s *Server) handleConnectionsNew(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	provider := q.Get("provider")
	if provider == "" || !contains(cfg.Providers(), provider) {
		s.renderConnectionProvider(w, cfg, "")
		return
	}
	f := parseConnectionForm(q)
	if !q.Has("toolsmode") {
		applyConnectionDefaults(cfg, &f)
	}
	s.renderConnectionBuild(w, cfg, f, "")
}

// handleConnectionReview builds the candidate the build page's own form described and, when it is valid,
// shows the review page over it. An invalid candidate returns to the build page with the reason and every
// non-secret field the person already typed, exactly as the build page last had them.
func (s *Server) handleConnectionReview(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	f := parseConnectionForm(r.URL.Query())
	cand, err := buildConnectionCandidate(cfg, f)
	if err != nil {
		s.renderConnectionBuild(w, cfg, f, s.redact(err.Error()))
		return
	}
	s.renderConnectionReview(w, cfg, cand, f, "")
}

// handleCreateConnection is the guided connection setup's one and only write: it rebuilds and revalidates
// the candidate exactly as handleConnectionReview does, refuses a configuration that changed since the
// review page was shown as a conflict, and, only then, saves it through manage.Service.SaveConnectionSetup,
// the same save internal/tui's own guided setup uses, so a store that turns out to be locked or missing
// after some roles were written leaves neither a stray secret nor a stray credential.
func (s *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	cfg, rev, err := s.svc.Load()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	f := parseConnectionForm(r.PostForm)
	cand, err := buildConnectionCandidate(cfg, f)
	if err != nil {
		s.renderConnectionBuild(w, cfg, f, s.redact(err.Error()))
		return
	}
	failReview := func(errText string) {
		s.renderConnectionReview(w, cfg, cand, f, errText)
	}

	if string(rev) != r.PostFormValue("cfgver") {
		failReview(errConfigChanged)
		return
	}

	connName := cand.Name
	var values map[string]string
	if cand.StoresSecrets() {
		values = make(map[string]string, len(cand.Roles))
		for _, role := range cand.Roles {
			v := r.PostFormValue("secret_" + role)
			values[role] = v
			if v != "" {
				s.registerSecret(v)
			}
		}
	}

	res, err := s.svc.SaveConnectionSetup(cfg, cand, rev, values, vaultOfferFromForm(r))
	if err != nil {
		var missing *manage.RoleMissingError
		if errors.As(err, &missing) {
			failReview(missing.Error())
			return
		}
		failReview(s.saveFailure(err))
		return
	}

	notice := withWarning(s.approvalNotice(cand.Config, connName), res.Logged)
	s.setNotice(createdNotice(notice, s.redact(res.Warning)))
	http.Redirect(w, r, "/connections/"+url.PathEscape(connName)+"?created=1", http.StatusSeeOther)
}

// createdNotice puts the warning of a saved setup, if any, ahead of the notice. The warning already starts
// with its own "warning: " (see manage.Service.SyncVaultProcess).
func createdNotice(notice, warning string) string {
	switch {
	case warning == "":
		return notice
	case notice == "":
		return warning
	}
	return warning + "; " + notice
}

// approvalNotice approves, in an encrypted and unlocked vault only, the one connection connName of cand - the
// candidate configuration handleCreateConnection just saved - by the core's direct-only policy
// (manage.ApprovalDirectOnly): a connection saved through this route is always new to the vault's own
// approvals (its name could not already exist; see buildConnectionCandidate), so this only ever finds it
// open for a reason outside the form (a stale approval left under the same name by something outside this
// run, naming a provider, origin, or credential entry the form never showed) and leaves it open then, never
// approving it.
//
// Any other vault-credential connection this create happened to newly open - most often every one of them
// at once, the moment this very save is what first encrypts the vault - is deliberately left alone: unlike
// internal/tui, this never sweeps and approves the rest.
//
// "" means there is nothing to say: no vault, an unencrypted or missing vault (which binds no connection
// at all), or a connection whose credential is not a vault credential in the first place.
func (s *Server) approvalNotice(cand *config.Config, connName string) string {
	res := manage.ApproveAfterChange(context.Background(), s.secrets.Vault(), manage.ApprovalSnapshot{}, cand,
		connName, manage.ApprovalDirectOnly)
	switch {
	case res.CheckErr != nil:
		return "approval failed: " + s.redact(res.CheckErr.Error())
	case res.StayedOpen != "":
		return fmt.Sprintf("stays open: %s; approve it with `qatlas vault approve` or in the TUI's Approvals section",
			res.StayReason)
	case res.ApproveErr != nil:
		return "approval failed: " + s.redact(res.ApproveErr.Error())
	case res.Warning != "":
		return "approval failed: " + s.redact(res.Warning)
	case len(res.Approved) > 0:
		return "approved"
	}
	return ""
}

// connectionResultData is what the connection result page renders: a secretfree row of the connection, this
// run's one-time creation and approval notice when there is one, and, when this run was given a Tester, the
// "Test this connection" form and the outcome of the last test run from this page.
type connectionResultData struct {
	Name, Provider, Service, Credential, Description, Targets, Permissions, Tools string
	Files                                                                         string
	Notice, Error                                                                 string
	CSRF                                                                          string
	TesterAvailable                                                               bool
	TestResult                                                                    string
	// Forward is the row of payload credentials this connection releases; shown only while one exists or is
	// listed.
	ShowForward    bool
	ForwardChoices []optionRow
	ForwardText    string
	BindingNote    string
	CfgVer         string
}

// handleConnectionResult shows one connection: what the guided setup redirects to once it saved, and a
// plain lookup for any other connection by name, secretfree, from a freshly loaded configuration the same
// way handleCredentialForm already does. The creation notice comes from the "created=1" flag the guided
// setup's own redirect carries; the approval notice, and any vault-process warning alongside it, comes from
// this run's own one-time hint (see approvalNotice, setNotice, takeNotice) and is consumed here, never
// carried in the URL.
func (s *Server) handleConnectionResult(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	name := r.PathValue("name")
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	conn, ok := cfg.Connections[name]
	if !ok {
		http.Error(w, "unknown connection", http.StatusNotFound)
		return
	}
	notice := ""
	if r.URL.Query().Get("created") == "1" {
		notice = "Connection created."
	}
	if r.URL.Query().Get("forward") == "1" {
		notice = "Released payload credentials saved."
	}
	if extra := s.takeNotice(); extra != "" {
		if notice != "" {
			notice += " "
		}
		notice += extra
	}
	s.renderConnectionResult(w, cfg, name, conn, notice, "")
}

// renderConnectionResult builds and renders the connection result page: notice is this run's own creation
// and approval hint (see handleConnectionResult), testResult the outcome of a test just run from this same
// page (see handleTestConnection); each is "" when there is nothing of that kind to show.
func (s *Server) renderConnectionResult(w http.ResponseWriter, cfg *config.Config, name string,
	conn config.Connection, notice, testResult string) {
	targets := strings.Join(conn.TargetValues(), ", ")
	tools := "all tools its permissions allow"
	switch {
	case conn.Tools == nil:
	case len(conn.Tools) == 0:
		tools = "none"
	default:
		tools = strings.Join(conn.Tools, ", ")
	}
	provider := cfg.Services[conn.Service].Provider
	data := connectionResultData{
		Name: name, Provider: provider, Service: conn.Service, Credential: conn.Credential,
		Description: conn.Description, Targets: targets,
		Files:       approval.FilesText(conn.Files.Read, conn.Files.Write),
		Permissions: config.FormatPermissions(cfg.ConnectionPermissions(name)), Tools: tools,
		Notice: notice, CSRF: s.csrfValue(), TesterAvailable: s.tester != nil, TestResult: testResult,
	}
	choices := manage.ForwardChoices(cfg)
	for _, listed := range conn.ForwardSecrets {
		if !slices.Contains(choices, listed) {
			choices = append(choices, listed)
		}
	}
	if len(choices) > 0 {
		data.ShowForward = true
		data.ForwardChoices = checkedOptions(choices, conn.ForwardSecrets)
		data.ForwardText = forwardText
		data.BindingNote = s.forwardBindingNote(cfg, conn.Credential)
		if fp, err := s.configFingerprint(); err == nil {
			data.CfgVer = fp
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.credTmpl.ExecuteTemplate(w, "connection-result", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// connectionRows rebuilds the Connections section of the overview from a freshly loaded configuration, the
// same way credentialRows already does for credentials: never from the one-time snapshot New was built
// with, so a connection created in this run is visible without restarting it.
func (s *Server) connectionRows() ([]ConnectionRow, error) {
	if s.svc == nil {
		return nil, fmt.Errorf("no configuration store is configured for this run")
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cfg.Connections))
	for name := range cfg.Connections {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]ConnectionRow, 0, len(names))
	for _, name := range names {
		conn := cfg.Connections[name]
		rows = append(rows, ConnectionRow{
			Name: name, Provider: cfg.Services[conn.Service].Provider, Description: conn.Description,
			Permissions: config.FormatPermissions(cfg.ConnectionPermissions(name)),
			Tools:       toolsSummary(conn.Tools),
		})
	}
	return rows, nil
}

// toolsSummary is this page's own short reading of a connection's tools list: not a policy decision, only
// how a nil, empty, or populated list reads as one word or a list of IDs.
func toolsSummary(tools []string) string {
	switch {
	case tools == nil:
		return "all"
	case len(tools) == 0:
		return "none"
	default:
		return strings.Join(tools, ", ")
	}
}
