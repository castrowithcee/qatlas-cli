package vault

import (
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/projectpath"
)

// The kinds of an open connection change.
const (
	ChangeCreate = "create"
	ChangeUpdate = "update"
	ChangeDelete = "delete"
)

// The gaps an agent token can leave in a change: the fields of a connection no vorbild covers, and two
// reasons that are no field. None ever holds a value, only the name of what is missing.
const (
	GapService     = "service"
	GapCredential  = "credential"
	GapPermissions = "permissions"
	GapTools       = "tools"
	GapTargets     = "targets"
	GapPaths       = "paths"
	GapFiles       = "files"
	GapForward     = "forward"
	// GapVorbild marks a change to a connection that is itself the vorbild of an agent token, which no
	// token may change or delete.
	GapVorbild = "vorbild"
	// GapNoVorbild marks a change the token has no usable vorbild for at all: each is renamed, deleted,
	// not approved, or reads a credential stored anew since.
	GapNoVorbild = "no-vorbild"
)

// TokenChange is one open connection change an agent token was asked to approve, and what came of it. It
// never holds a secret.
type TokenChange struct {
	Connection string `json:"connection"`
	// Kind is ChangeCreate, ChangeUpdate, or ChangeDelete.
	Kind     string `json:"kind"`
	Approved bool   `json:"approved"`
	// Gaps names what no vorbild covers, empty for an approved change; see the Gap constants.
	Gaps []string `json:"gaps,omitempty"`
}

// TokenApproval is what ApproveWithToken did.
type TokenApproval struct {
	// Token is the name of the token presented, empty when it matched none.
	Token string `json:"token,omitempty"`
	// Changes lists every open change considered, sorted by connection.
	Changes []TokenChange `json:"changes,omitempty"`
	// Lapsed names the vorbild connections of the token that cover nothing now, sorted.
	Lapsed []string `json:"lapsed,omitempty"`
	// NotOpen names the connections asked for that have no open change, sorted.
	NotOpen []string `json:"not_open,omitempty"`
}

// model is one usable vorbild: its name and the approval that is its ceiling.
type model struct {
	name     string
	approval Approval
}

// ApproveWithToken approves, in the vault unlocked in this process, every open connection change the agent
// token value covers, and writes the vault once. current holds the scope of every connection as configured
// now that reads a vault credential; one whose credential the vault holds no entry for has nothing to
// approve. A connection without an approval is a change of kind create, one whose approval no longer matches
// is an update, and an approval without a connection in current is a delete, which removes the approval.
// only, when not empty, limits the changes to the connections it names.
//
// A change is covered when a vorbild V of the token, as V is approved, not as it may be configured now, has
// the same service (provider and endpoint) and the same credential entry, and the change stays inside V:
// its permissions a subset of V's; its tools a subset of V's tools list, where V has one; its targets among
// V's, where V has any; its paths inside V's, where V is bound to any; its local file directories, per
// direction, inside V's of that direction, and none where V has none; its forward credentials a subset of
// V's, each with the same fields. A delete is covered when the
// approval it removes was. A vorbild counts only while it is a connection in current and its approval still
// reads the credential entry it was given for. A change to a connection that is the vorbild of any token is
// never covered. A change partly outside stays open whole.
//
// It fails with ErrTokenUnknown for a value that matches no token, and with ErrTokenExpired for an expired
// one, in which case the returned approval names it; nothing is written then.
func (v *Vault) ApproveWithToken(value string, current []Scope, only []string, now time.Time) (TokenApproval,
	error) {
	doc, err := v.unlockedDocument()
	if err != nil {
		return TokenApproval{}, err
	}
	tokens, err := v.loadTokens()
	if err != nil {
		return TokenApproval{}, err
	}
	token, ok := matchToken(tokens, value)
	if !ok {
		return TokenApproval{}, ErrTokenUnknown
	}
	result := TokenApproval{Token: token.Name}
	if token.Expired(now) {
		return result, ErrTokenExpired
	}

	ids := map[string]string{}
	for _, entry := range doc.Entries {
		ids[entry.Name] = entry.ID
	}
	configured := map[string]Scope{}
	for _, scope := range current {
		configured[scope.Connection] = scope
	}

	var models []model
	for _, name := range token.Models {
		approval, approved := doc.Approvals[name]
		_, exists := configured[name]
		if !approved || !exists || ids[approval.Scope.Credential] != approval.CredentialID {
			result.Lapsed = append(result.Lapsed, name)
			continue
		}
		models = append(models, model{name: name, approval: approval})
	}
	protected := map[string]bool{}
	for _, t := range tokens {
		for _, name := range t.Models {
			protected[name] = true
		}
	}

	type open struct {
		change TokenChange
		scope  Scope
		id     string
	}
	var changes []open
	for name, scope := range configured {
		id, held := ids[scope.Credential]
		if !held || id == "" {
			continue
		}
		approval, approved := doc.Approvals[name]
		switch {
		case !approved:
			changes = append(changes, open{TokenChange{Connection: name, Kind: ChangeCreate}, scope, id})
		case approval.Fingerprint != Fingerprint(scope, id):
			changes = append(changes, open{TokenChange{Connection: name, Kind: ChangeUpdate}, scope, id})
		}
	}
	for name, approval := range doc.Approvals {
		if _, exists := configured[name]; exists {
			continue
		}
		changes = append(changes, open{TokenChange{Connection: name, Kind: ChangeDelete}, approval.Scope,
			approval.CredentialID})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].change.Connection < changes[j].change.Connection })

	if len(only) > 0 {
		wanted := map[string]bool{}
		for _, name := range only {
			wanted[name] = true
		}
		kept := changes[:0]
		for _, c := range changes {
			if wanted[c.change.Connection] {
				kept = append(kept, c)
				delete(wanted, c.change.Connection)
			}
		}
		changes = kept
		for name := range wanted {
			result.NotOpen = append(result.NotOpen, name)
		}
		sort.Strings(result.NotOpen)
	}

	next := make(map[string]Approval, len(doc.Approvals))
	for name, approval := range doc.Approvals {
		next[name] = approval
	}
	written := false
	for _, c := range changes {
		change := c.change
		switch {
		case protected[change.Connection]:
			change.Gaps = []string{GapVorbild}
		default:
			change.Gaps = bestCover(models, c.scope, c.id)
		}
		if len(change.Gaps) == 0 {
			change.Approved, written = true, true
			if change.Kind == ChangeDelete {
				delete(next, change.Connection)
			} else {
				next[change.Connection] = Approval{
					Fingerprint: Fingerprint(c.scope, c.id), Scope: c.scope.Normalized(), CredentialID: c.id,
					Approved: now.UTC(),
				}
			}
		}
		result.Changes = append(result.Changes, change)
	}
	if written {
		if err := v.writeApprovals(doc, next); err != nil {
			return TokenApproval{Token: token.Name}, err
		}
	}
	return result, nil
}

// bestCover returns nil when one of models covers scope, read with the credential entry id, and otherwise
// the gaps of the model that comes closest, fewest gaps first and then by name, or GapNoVorbild when there
// is no model at all.
func bestCover(models []model, scope Scope, id string) []string {
	if len(models) == 0 {
		return []string{GapNoVorbild}
	}
	var best []string
	for i, m := range models {
		gaps := coverGaps(m.approval, scope, id)
		if len(gaps) == 0 {
			return nil
		}
		if i == 0 || len(gaps) < len(best) {
			best = gaps
		}
	}
	return best
}

// coverGaps lists the fields of scope, read with the credential entry id, that the approved vorbild does not
// cover, in a fixed order.
func coverGaps(vorbild Approval, scope Scope, id string) []string {
	ceiling, change := vorbild.Scope.Normalized(), scope.Normalized()
	var gaps []string
	if change.Provider != ceiling.Provider || change.Origin != ceiling.Origin {
		gaps = append(gaps, GapService)
	}
	if change.Credential != ceiling.Credential || id != vorbild.CredentialID {
		gaps = append(gaps, GapCredential)
	}
	if !subset(change.Permissions, ceiling.Permissions) {
		gaps = append(gaps, GapPermissions)
	}
	// A vorbild without a tools list admits every tool its permissions do; one with a list admits only a
	// change whose own list stays inside it, never one without a list.
	if ceiling.Tools != nil && (change.Tools == nil || !subset(change.Tools, ceiling.Tools)) {
		gaps = append(gaps, GapTools)
	}
	// Targets compare by their exact entries: a provider's wildcard or pattern in the vorbild covers only
	// that same entry, since the vault knows no provider's rules.
	if len(ceiling.Targets) > 0 && (len(change.Targets) == 0 || !subset(change.Targets, ceiling.Targets)) {
		gaps = append(gaps, GapTargets)
	}
	if len(ceiling.Paths) > 0 && !pathsWithin(change.Paths, ceiling.Paths) {
		gaps = append(gaps, GapPaths)
	}
	if !filesWithin(change.FilesRead, ceiling.FilesRead) || !filesWithin(change.FilesWrite, ceiling.FilesWrite) {
		gaps = append(gaps, GapFiles)
	}
	// Forward credentials compare by name and exact field list: a vorbild covers a change only when every
	// credential the change releases is released by the vorbild with the same fields, so a change that
	// releases more, or other fields, stays open.
	if !forwardWithin(change.Forward, ceiling.Forward) {
		gaps = append(gaps, GapForward)
	}
	return gaps
}

// forwardWithin reports whether every forward credential of forward is released, with the same fields, by
// one entry of of. Both are normalized. No entries are always covered, since they release nothing.
func forwardWithin(forward, of []ForwardSecret) bool {
	for _, entry := range forward {
		covered := false
		for _, base := range of {
			if base.Name == entry.Name && slices.Equal(base.Fields, entry.Fields) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func subset(values, of []string) bool {
	for _, value := range values {
		if !slices.Contains(of, value) {
			return false
		}
	}
	return true
}

// filesWithin reports whether every entry of files lies inside one of bases, the entries of the same
// direction in the vorbild. No entries are always covered, since they release nothing; any entry is
// uncovered where the vorbild releases nothing in that direction.
func filesWithin(files, bases []string) bool {
	return len(files) == 0 || pathsWithin(files, bases)
}

// pathsWithin reports whether paths is not empty and every entry lies inside one of bases. An empty paths
// list means every project, which no bound vorbild covers.
func pathsWithin(paths, bases []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		inside := false
		for _, base := range bases {
			if pathWithin(path, base) {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
	}
	return true
}

// pathWithin reports whether the paths entry path lies inside the entry base, both as written, "~" expanded,
// and with every symbolic link resolved, the way a binding is compared with a project: a link below base
// that leads elsewhere does not stay inside it.
func pathWithin(path, base string) bool {
	p, err := projectpath.Expand(path)
	if err != nil {
		return false
	}
	b, err := projectpath.Expand(base)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(p) || !filepath.IsAbs(b) {
		return false
	}
	p, b = filepath.Clean(p), filepath.Clean(b)
	return projectpath.Within(p, b) && projectpath.Within(projectpath.Canonical(p), projectpath.Canonical(b))
}
