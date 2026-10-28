package nextcloud

import (
	"errors"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// maxExcludesPerKind bounds the exclusions of one kind.
const maxExcludesPerKind = 100

// exclusions are the parsed exclusions of one connection, held in comparison form (see comparisonForm).
// Folder paths are relative to the Files root, like the folder target; notes entries are normalised
// categories. The other kinds hold one comparison-form ID each.
type exclusions struct {
	folders, notes                        [][]string
	calendars, addressbooks, talks, decks []string
}

// crossExcludes are the exclusions of one kind seen from the other: the Notes app keeps its notes as files
// below the notes path, so a folder exclusion hides notes and a notes exclusion hides folders. They are
// derived from the Notes settings of the identity, which only a read gives, so they are empty until the
// client has filled them (see Client.deriveCross).
type crossExcludes struct {
	folders, notes [][]string
}

// crossNeeded reports whether the exclusions reach the other kind: a folder exclusion reaches the notes
// of a connection that binds notes, a notes exclusion the folders of a connection that binds a folder.
func (s scope) crossNeeded() bool {
	return len(s.excludes.folders) > 0 && s.notes.bound() || len(s.excludes.notes) > 0 && s.hasFolder
}

// derive turns the exclusions into their counterpart of the other kind for a notes path relative to the
// Files root. A folder exclusion on or above the notes path hides every note.
func (s scope) derive(notesPath []string) crossExcludes {
	var out crossExcludes
	root := comparisonSegments(notesPath)
	for _, f := range s.excludes.folders {
		switch {
		case hasPrefix(f, root):
			out.notes = append(out.notes, f[len(root):])
		case hasPrefix(root, f):
			out.notes = append(out.notes, nil)
		}
	}
	for _, n := range s.excludes.notes {
		out.folders = append(out.folders, append(append([]string{}, root...), n...))
	}
	return out
}

var (
	errExcludeInvalid = errors.New("a Nextcloud exclusion must be folder/PATH, notes/PATH, calendar/ID, " +
		"addressbook/ID, deck/ID, or talk/ID, below a target of the same kind, named once, and not nested " +
		"in another exclusion")
	errExcludeTooMany    = errors.New("a Nextcloud connection may list at most 100 exclusions per kind")
	errExcludeBesideKind = errors.New("a Nextcloud exclusion is not supported next to the account target yet")
)

// comparisonForm brings a segment into the form exclusions are compared in: canonical composition, then
// Unicode case folding (full folding, so that ß and SS meet), then composition again because folding may
// decompose. Matching is deliberately broader than the file system: an exclusion may hide more than it
// names, never less. Targets are not put into this form.
func comparisonForm(segment string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(segment)))
}

func comparisonSegments(segments []string) []string {
	out := make([]string, len(segments))
	for i, segment := range segments {
		out[i] = comparisonForm(segment)
	}
	return out
}

// normalizeCategory applies what the Nextcloud Notes app does to a category before it names a folder: it
// removes the characters * | / \ : " < > ? inside a segment, then leading dots and white space and
// surrounding white space, and drops segments that end up empty. The result is in comparison form.
func normalizeCategory(category string) []string {
	var out []string
	for _, segment := range strings.Split(category, "/") {
		segment = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`*|/\:"<>?`, r) {
				return -1
			}
			return r
		}, segment)
		segment = strings.TrimFunc(strings.TrimLeftFunc(segment, func(r rune) bool { return r == '.' || unicode.IsSpace(r) }),
			func(r rune) bool { return unicode.IsSpace(r) || r == 0 })
		if segment != "" {
			out = append(out, comparisonForm(segment))
		}
	}
	return out
}

// hasPrefix reports whether path is on or below prefix, segment by segment.
func hasPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && slices.Equal(path[:len(prefix)], prefix)
}

// onOrBelow reports whether path lies on or below any of the entries.
func onOrBelow(entries [][]string, path []string) bool {
	return slices.ContainsFunc(entries, func(e []string) bool { return hasPrefix(path, e) })
}

// ancestorOf reports whether path is a strict ancestor of any of the entries.
func ancestorOf(entries [][]string, path []string) bool {
	return slices.ContainsFunc(entries, func(e []string) bool { return len(e) > len(path) && hasPrefix(e, path) })
}

// folderExcluded reports whether a folder path relative to the Files root lies on or below an excluded
// folder.
func (s scope) folderExcluded(path []string) bool {
	p := comparisonSegments(path)
	return onOrBelow(s.excludes.folders, p) || onOrBelow(s.cross.folders, p)
}

// folderHoldsExcluded reports whether a folder path relative to the Files root is a strict ancestor of an
// excluded folder, so that something below it is hidden.
func (s scope) folderHoldsExcluded(path []string) bool {
	p := comparisonSegments(path)
	return ancestorOf(s.excludes.folders, p) || ancestorOf(s.cross.folders, p)
}

// categoryExcluded reports whether a notes category lies on or below an excluded category.
func (s scope) categoryExcluded(category string) bool {
	c := normalizeCategory(category)
	return onOrBelow(s.excludes.notes, c) || onOrBelow(s.cross.notes, c)
}

// categoryHoldsExcluded reports whether a notes category is a strict ancestor of an excluded category.
func (s scope) categoryHoldsExcluded(category string) bool {
	c := normalizeCategory(category)
	return ancestorOf(s.excludes.notes, c) || ancestorOf(s.cross.notes, c)
}

// withExcludes reads the exclusions of the connection into the scope. It does not rely on the core
// validation, so an invalid exclusion fails closed here too. No error quotes a configured value.
func (s scope) withExcludes(values []string) (scope, error) {
	if len(values) == 0 {
		return s, nil
	}
	var ex exclusions
	for _, raw := range values {
		kind, rest, ok := strings.Cut(strings.TrimSpace(raw), "/")
		if !ok || rest == "" {
			return scope{}, errExcludeInvalid
		}
		if err := ex.add(kind, rest); err != nil {
			return scope{}, err
		}
	}
	if err := ex.check(s); err != nil {
		return scope{}, err
	}
	s.excludes = ex
	return s, nil
}

func (ex *exclusions) add(kind, rest string) error {
	switch kind {
	case kindFolder:
		segments, err := splitRelative(rest)
		if err != nil || len(segments) == 0 {
			return errExcludeInvalid
		}
		ex.folders = append(ex.folders, comparisonSegments(segments))
	case kindNotes:
		if checkCategory(rest) != nil {
			return errExcludeInvalid
		}
		category := normalizeCategory(rest)
		if len(category) == 0 {
			return errExcludeInvalid
		}
		ex.notes = append(ex.notes, category)
	case kindCalendar, kindAddressbook, kindTalk:
		if checkSegment(rest) != nil {
			return errExcludeInvalid
		}
		// The scheduling and trash collections and the system address book are never in reach of a whole-kind
		// target, so an exclusion naming one is invalid.
		if kind == kindCalendar && reservedCalendar(rest) ||
			kind == kindAddressbook && comparisonForm(rest) == comparisonForm(systemAddressbook) {
			return errExcludeInvalid
		}
		list := map[string]*[]string{kindCalendar: &ex.calendars, kindAddressbook: &ex.addressbooks, kindTalk: &ex.talks}[kind]
		*list = append(*list, comparisonForm(rest))
	case kindDeck:
		if checkBoardID(rest) != nil {
			return errExcludeInvalid
		}
		ex.decks = append(ex.decks, canonicalNumber(rest))
	default:
		return errExcludeInvalid
	}
	return nil
}

// check validates the parsed exclusions against the targets: each lies strictly below a target of its
// kind, none repeats or nests, and no kind exceeds its bound. The account target takes none, because its
// tools are not enforced yet.
func (ex exclusions) check(s scope) error {
	for _, n := range []int{len(ex.folders), len(ex.notes), len(ex.calendars), len(ex.addressbooks),
		len(ex.talks), len(ex.decks)} {
		if n > maxExcludesPerKind {
			return errExcludeTooMany
		}
	}
	if !s.folderTargetsAllow(ex.folders) || !s.categoryTargetsAllow(ex.notes) {
		return errExcludeInvalid
	}
	for _, list := range [][][]string{ex.folders, ex.notes} {
		for i, a := range list {
			for j, b := range list {
				if i != j && hasPrefix(a, b) {
					return errExcludeInvalid
				}
			}
		}
	}
	for _, pair := range []struct {
		ids []string
		sel selection
	}{{ex.calendars, s.calendars}, {ex.addressbooks, s.addressbooks}, {ex.talks, s.talks}, {ex.decks, s.decks}} {
		if len(pair.ids) > 0 && !pair.sel.all {
			return errExcludeInvalid
		}
		for i, id := range pair.ids {
			if slices.Index(pair.ids, id) != i {
				return errExcludeInvalid
			}
		}
	}
	if s.account {
		return errExcludeBesideKind
	}
	return nil
}

// folderTargetsAllow reports whether every folder exclusion lies strictly below the folder target. The
// root folder target binds every path.
func (s scope) folderTargetsAllow(folders [][]string) bool {
	if len(folders) == 0 {
		return true
	}
	if !s.hasFolder {
		return false
	}
	root := comparisonSegments(s.folder)
	return !slices.ContainsFunc(folders, func(f []string) bool { return len(f) <= len(root) || !hasPrefix(f, root) })
}

// categoryTargetsAllow reports whether every category exclusion lies strictly below a notes target. A
// target that normalises to the empty category binds the whole notes root, as in the Notes app.
func (s scope) categoryTargetsAllow(categories [][]string) bool {
	if len(categories) == 0 {
		return true
	}
	if !s.notes.bound() {
		return false
	}
	targets := [][]string{nil}
	if !s.notes.all {
		targets = nil
		for _, id := range s.notes.ids {
			targets = append(targets, normalizeCategory(id))
		}
	}
	for _, c := range categories {
		if slices.ContainsFunc(targets, func(t []string) bool { return slices.Equal(c, t) }) ||
			!slices.ContainsFunc(targets, func(t []string) bool { return hasPrefix(c, t) }) {
			return false
		}
	}
	return true
}

// canonicalNumber drops the leading zeros of a decimal number, so that 07 and 7 name the same ID.
func canonicalNumber(id string) string {
	if trimmed := strings.TrimLeft(id, "0"); trimmed != "" {
		return trimmed
	}
	return "0"
}

// excluding returns the selection with the exclusions of its kind; ids are in the form add stores them in.
func (s selection) excluding(ids []string, numeric bool) selection {
	s.excluded, s.numeric = ids, numeric
	return s
}

// hides reports whether the ID is excluded: URIs compare in comparison form, board IDs as numbers.
func (s selection) hides(id string) bool {
	if len(s.excluded) == 0 {
		return false
	}
	if s.numeric {
		return slices.Contains(s.excluded, canonicalNumber(id))
	}
	return slices.Contains(s.excluded, comparisonForm(id))
}
