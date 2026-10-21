package manage

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// ImportMode says what an import does with an entry whose name the target already has. Entries are matched
// by name alone.
type ImportMode string

const (
	// ImportModeCreate only creates: any name that exists in the target stops the whole import.
	ImportModeCreate ImportMode = "create"
	// ImportModeMerge adds what is missing and leaves existing entries as they are.
	ImportModeMerge ImportMode = "merge"
	// ImportModeOverwrite replaces existing entries with the file's. A replaced entry keeps the id of the
	// entry it replaces, so approvals and vault entries that record the id stay attached.
	ImportModeOverwrite ImportMode = "overwrite"
)

// ParseImportMode returns the mode named text.
func ParseImportMode(text string) (ImportMode, error) {
	switch m := ImportMode(text); m {
	case ImportModeCreate, ImportModeMerge, ImportModeOverwrite:
		return m, nil
	}
	return "", fmt.Errorf("unknown import mode %q, use %s, %s or %s", text, ImportModeCreate, ImportModeMerge, ImportModeOverwrite)
}

// What an import does with one entry.
const (
	ImportActionCreate  = "create"
	ImportActionSkip    = "skip"
	ImportActionReplace = "replace"
)

// The sections an ImportEntry belongs to.
const (
	ImportServices      = "services"
	ImportCredentials   = "credentials"
	ImportConnections   = "connections"
	ImportDefaults      = "defaults.connections"
	ImportSecretStore   = "defaults.secret_store"
	ImportProviderNotes = "provider_notes"
)

// ImportEntry is what an import does with one entry of the file.
type ImportEntry struct {
	// Section is one of the Import constants above; Name is the entry's name, the domain of a default or the
	// provider of a note. Action is ImportActionCreate, ImportActionSkip or ImportActionReplace.
	Section, Name, Action string
	// ID is the id the entry has afterwards, "" for a section without ids and for a skipped entry. NewID is
	// true when it is not the id the file brought: that one was taken in the target, the entry replaces
	// one that keeps its own, or the file had none.
	ID    string
	NewID bool
	// UsesTarget names the entries of the target that a connection of the file refers to because the file's
	// own entry of that name is skipped, such as "service wiki". The target's entry is the one that counts.
	UsesTarget []string
	// UsedBy names the connections of the target, outside the file, that use a replaced service or credential
	// and so are changed by it.
	UsedBy []string
	// Access names the local access a created or replaced connection brings: "paths", "files.read",
	// "files.write" and "forward_secrets". It is there so a person sees what a foreign file grants.
	Access []string
}

// ImportPlan is what importing a file does, found without writing anything. PlanImport makes it and
// ApplyImport carries it out.
type ImportPlan struct {
	Mode ImportMode
	// Source is the file read.
	Source  string
	Entries []ImportEntry
	// CredentialsWithoutSecret are the created credentials whose secrets live in the keyring or the vault. They
	// hold no secret in this installation until one is set, so no connection can use them before.
	CredentialsWithoutSecret []string
	// CredentialsKeepSecret are the replaced credentials. Whatever secret the target holds for the name stays
	// untouched, and it may not fit the replacement.
	CredentialsKeepSecret []string

	before, cfg *config.Config
	base        config.Revision
}

// Count returns how many entries the plan creates, replaces and skips.
func (p *ImportPlan) Count() (created, replaced, skipped int) {
	for _, e := range p.Entries {
		switch e.Action {
		case ImportActionCreate:
			created++
		case ImportActionReplace:
			replaced++
		default:
			skipped++
		}
	}
	return
}

// ImportExistsError reports the entries ImportModeCreate refused because the target has them. Each is
// "section.name".
type ImportExistsError struct{ Entries []string }

func (e *ImportExistsError) Error() string {
	return "the target already has " + strings.Join(e.Entries, ", ") + "; use merge to keep them or overwrite to replace them"
}

// ImportResult is what ApplyImport did.
type ImportResult struct {
	Created, Replaced, Skipped int
	// Warnings are the redacted failures after the save, none of which undoes it.
	Warnings []string
}

// PlanImport reads the export file at path, checks it completely and works out what mode does to the
// current configuration, which does not exist yet on a fresh installation. It writes nothing.
//
// The file must be self-contained: every reference in it resolves inside it. The configuration that would
// result - the file's entries as the plan places them, among the target's - must be valid as well, which
// is how a skipped entry of the target that does not fit a connection of the file is found. Either failure,
// and a mode conflict, is returned as an error; the text has passed the service's redaction.
//
// An id the file brings stays only if the target has no entry of that kind with it. Otherwise, and for an
// entry without one, a new id is assigned.
func (s *Service) PlanImport(path string, mode ImportMode) (*ImportPlan, error) {
	if _, err := ParseImportMode(string(mode)); err != nil {
		return nil, err
	}
	file, fileCfg, err := s.readTransfer(path)
	if err != nil {
		return nil, err
	}
	target, rev, err := s.Load()
	if err != nil {
		var nf *config.NotFoundError
		if !errors.As(err, &nf) {
			return nil, err
		}
		target = s.NewConfig()
	}
	before := target
	cand := target.Clone()
	plan := &ImportPlan{Mode: mode, Source: path, before: before, cfg: cand, base: rev}
	var exists []string

	// decide gives the action for one entry and records a conflict of ImportModeCreate.
	decide := func(section, name string, has bool) string {
		switch {
		case !has:
			return ImportActionCreate
		case mode == ImportModeMerge:
			return ImportActionSkip
		case mode == ImportModeOverwrite:
			return ImportActionReplace
		}
		exists = append(exists, section+"."+name)
		return ImportActionSkip
	}
	// takenIDs are the ids the target has per kind; its entries all stay, so none of them may be reused.
	taken := map[string]map[string]bool{ImportServices: {}, ImportCredentials: {}, ImportConnections: {}}
	for _, v := range target.Services {
		taken[ImportServices][v.ID] = true
	}
	for _, v := range target.Credentials {
		taken[ImportCredentials][v.ID] = true
	}
	for _, v := range target.Connections {
		taken[ImportConnections][v.ID] = true
	}
	freeID := func(section, id string) string {
		if id == "" || taken[section][id] {
			return ""
		}
		return id
	}
	actions := map[string]string{}
	add := func(e ImportEntry) { plan.Entries = append(plan.Entries, e); actions[e.Section+"."+e.Name] = e.Action }
	// settle fixes the id of an entry the plan puts in, keeping the target's own id on a replacement.
	settle := func(section, action, fileID, targetID string) string {
		if action == ImportActionReplace && targetID != "" {
			return targetID
		}
		return freeID(section, fileID)
	}

	for _, name := range sortedNames(file.Services) {
		old, has := target.Services[name]
		action := decide(ImportServices, name, has)
		e := ImportEntry{Section: ImportServices, Name: name, Action: action}
		if action != ImportActionSkip {
			v := file.Services[name]
			id := settle(ImportServices, action, v.ID, old.ID)
			v.ID = id
			cand.Services[name] = v
			e.ID = id
			if id != "" {
				taken[ImportServices][id] = true
			}
		}
		add(e)
	}
	for _, name := range sortedNames(file.Credentials) {
		old, has := target.Credentials[name]
		action := decide(ImportCredentials, name, has)
		e := ImportEntry{Section: ImportCredentials, Name: name, Action: action}
		if action != ImportActionSkip {
			v := file.Credentials[name]
			id := settle(ImportCredentials, action, v.ID, old.ID)
			v.ID = id
			cand.Credentials[name] = v
			e.ID = id
			if id != "" {
				taken[ImportCredentials][id] = true
			}
			switch {
			case action == ImportActionReplace:
				plan.CredentialsKeepSecret = append(plan.CredentialsKeepSecret, name)
			case v.Type != config.CredentialTypeEnv:
				plan.CredentialsWithoutSecret = append(plan.CredentialsWithoutSecret, name)
			}
		}
		add(e)
	}
	for _, name := range sortedNames(file.Connections) {
		old, has := target.Connections[name]
		action := decide(ImportConnections, name, has)
		e := ImportEntry{Section: ImportConnections, Name: name, Action: action}
		if action != ImportActionSkip {
			v := file.Connections[name]
			id := settle(ImportConnections, action, v.ID, old.ID)
			v.ID = id
			if err := cand.SetConnection(name, v); err != nil {
				return nil, s.refuse(err)
			}
			e.ID = id
			if id != "" {
				taken[ImportConnections][id] = true
			}
			if len(v.Paths) > 0 {
				e.Access = append(e.Access, "paths")
			}
			if len(v.Files.Read) > 0 {
				e.Access = append(e.Access, "files.read")
			}
			if len(v.Files.Write) > 0 {
				e.Access = append(e.Access, "files.write")
			}
			if len(v.ForwardSecrets) > 0 {
				e.Access = append(e.Access, "forward_secrets")
			}
			ref := func(section, kind, ref string) {
				if actions[section+"."+ref] == ImportActionSkip {
					e.UsesTarget = append(e.UsesTarget, kind+" "+ref)
				}
			}
			ref(ImportServices, "service", v.Service)
			ref(ImportCredentials, "credential", v.Credential)
			for _, f := range v.ForwardSecrets {
				ref(ImportCredentials, "credential", f)
			}
		}
		add(e)
	}
	if file.Defaults != nil {
		for _, domain := range sortedNames(file.Defaults.Connections) {
			old, has := target.Defaults.Connections[domain]
			if has && old == file.Defaults.Connections[domain] {
				add(ImportEntry{Section: ImportDefaults, Name: domain, Action: ImportActionSkip})
				continue
			}
			action := decide(ImportDefaults, domain, has)
			if action != ImportActionSkip {
				cand.Defaults.Connections[domain] = file.Defaults.Connections[domain]
			}
			add(ImportEntry{Section: ImportDefaults, Name: domain, Action: action})
		}
		if store := file.Defaults.SecretStore; store != "" {
			old := target.Defaults.SecretStore
			if old == store {
				add(ImportEntry{Section: ImportSecretStore, Name: "secret_store", Action: ImportActionSkip})
			} else {
				action := decide(ImportSecretStore, "secret_store", old != "")
				if action != ImportActionSkip {
					cand.Defaults.SecretStore = store
				}
				add(ImportEntry{Section: ImportSecretStore, Name: "secret_store", Action: action})
			}
		}
	}
	for _, provider := range sortedNames(file.ProviderNotes) {
		old, has := target.ProviderNotes[provider]
		note := fileCfg.ProviderNotes[provider]
		if has && old == note {
			add(ImportEntry{Section: ImportProviderNotes, Name: provider, Action: ImportActionSkip})
			continue
		}
		action := decide(ImportProviderNotes, provider, has)
		if action != ImportActionSkip {
			if err := cand.SetProviderNote(provider, note); err != nil {
				return nil, s.refuse(err)
			}
		}
		add(ImportEntry{Section: ImportProviderNotes, Name: provider, Action: action})
	}
	if len(exists) > 0 {
		return nil, s.refuse(&ImportExistsError{Entries: exists})
	}

	cand.AssignIDs()
	for i, e := range plan.Entries {
		if e.Action == ImportActionSkip {
			continue
		}
		switch e.Section {
		case ImportServices:
			plan.Entries[i].ID = cand.Services[e.Name].ID
			plan.Entries[i].NewID = plan.Entries[i].ID != file.Services[e.Name].ID
		case ImportCredentials:
			plan.Entries[i].ID = cand.Credentials[e.Name].ID
			plan.Entries[i].NewID = plan.Entries[i].ID != file.Credentials[e.Name].ID
		case ImportConnections:
			plan.Entries[i].ID = cand.Connections[e.Name].ID
			plan.Entries[i].NewID = plan.Entries[i].ID != file.Connections[e.Name].ID
		}
	}
	if err := cand.Validate(); err != nil {
		return nil, s.refuse(fmt.Errorf("the import would leave the configuration invalid: %w", &config.InvalidError{Path: s.store.Path(), Err: err}))
	}
	plan.noteUsedBy(file)
	return plan, nil
}

// noteUsedBy names, for each replaced service and credential, the connections of the target that are not
// part of the file and still use it.
func (p *ImportPlan) noteUsedBy(file *transferFile) {
	for i, e := range p.Entries {
		if e.Action != ImportActionReplace || (e.Section != ImportServices && e.Section != ImportCredentials) {
			continue
		}
		for _, name := range sortedNames(p.cfg.Connections) {
			if _, inFile := file.Connections[name]; inFile {
				continue
			}
			conn := p.cfg.Connections[name]
			if (e.Section == ImportServices && conn.Service == e.Name) ||
				(e.Section == ImportCredentials && (conn.Credential == e.Name || slices.Contains(conn.ForwardSecrets, e.Name))) {
				p.Entries[i].UsedBy = append(p.Entries[i].UsedBy, name)
			}
		}
	}
}

// ApplyImport writes plan, which PlanImport made, as one change: the configuration is saved only if the file
// still has the revision the plan was based on, and then completely. Another change since the plan is a
// *config.ConflictError (errors.Is(err, config.ErrConflict)) with nothing written and nothing merged; plan
// again to see the new state. It touches no secret, vault entry or approval. A plan that changes nothing
// writes nothing.
func (s *Service) ApplyImport(plan *ImportPlan) (ImportResult, error) {
	var res ImportResult
	if plan == nil || plan.cfg == nil {
		return res, errors.New("no import was planned")
	}
	res.Created, res.Replaced, res.Skipped = plan.Count()
	if res.Created+res.Replaced == 0 {
		return res, nil
	}
	warning, err := s.SaveConfig(plan.before, plan.cfg, plan.base)
	if err != nil {
		return ImportResult{}, err
	}
	if warning != "" {
		res.Warnings = append(res.Warnings, warning)
	}
	return res, nil
}
