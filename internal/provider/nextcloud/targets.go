package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The kinds of target a connection binds. A target is either a kind name alone (everything of that kind
// the identity reaches) or kind/ID. Only folder carries a path that may hold several segments, as does
// the notes category.
const (
	kindFolder      = "folder"
	kindCalendar    = "calendar"
	kindAddressbook = "addressbook"
	kindTalk        = "talk"
	kindDeck        = "deck"
	kindNotes       = "notes"
	kindAccount     = "account"
	kindAdmin       = "admin"

	maxTargetsPerKind = 100
	maxBoardIDLength  = 18
)

// groupFiles is the tool group of the Files tools.
const groupFiles = "files"

// groupShares is the tool group of the sharing tools.
const groupShares = "shares"

var toolGroups = []config.ToolGroup{{
	ID: groupFiles, Title: "Files",
	Description: "Folders and files below the Files root folder of the identity",
}, {
	ID: groupShares, Title: "Shares",
	Description: "Shares of and to the identity below the Files root folder, and the recipients of the instance",
}, {
	ID: groupDeck, Title: "Deck",
	Description: "Deck boards, stacks, and cards of the identity that a deck target binds",
}, {
	ID: groupTalk, Title: "Talk",
	Description: "Talk conversations the connection binds, their participants, and their messages",
}, {
	ID: groupNotes, Title: "Notes",
	Description: "Notes of the identity in the bound categories, their embedded attachments, and the Notes settings",
}, {
	ID: groupCalendar, Title: "Calendar",
	Description: "Calendars and events of the identity that a calendar target binds",
}, {
	ID: groupContacts, Title: "Contacts",
	Description: "Address books the connection binds and their contacts",
}}

var targetKinds = []config.TargetKind{{
	Name: kindFolder,
	Description: "the Files folder this connection may access; optional, at most one; the kind alone binds the " +
		"whole Files root of the identity, and PATH may have several segments such as Team/Reports",
	Forms: []string{kindFolder, kindFolder + "/PATH"},
}, {
	Name:        kindCalendar,
	Description: "all calendars of the identity, or one calendar by its URI; optional, and may be listed more than once",
	Forms:       []string{kindCalendar, kindCalendar + "/URI"},
}, {
	Name: kindAddressbook,
	Description: "all address books of the identity except the system address book, or one by its URI; optional, " +
		"and may be listed more than once",
	Forms: []string{kindAddressbook, kindAddressbook + "/URI"},
}, {
	Name:        kindTalk,
	Description: "all Talk conversations of the identity, or one by its token; optional, and may be listed more than once",
	Forms:       []string{kindTalk, kindTalk + "/TOKEN"},
}, {
	Name:        kindDeck,
	Description: "all Deck boards of the identity, or one by its numeric board ID; optional, and may be listed more than once",
	Forms:       []string{kindDeck, kindDeck + "/BOARD_ID"},
}, {
	Name: kindNotes,
	Description: "all notes of the identity, or the notes of one category, which may be a sub-folder such as " +
		"Work/Plans; optional, and may be listed more than once",
	Forms: []string{kindNotes, kindNotes + "/CATEGORY"},
}, {
	Name: kindAccount,
	Description: "the account-wide and instance-wide reach of the identity: notifications, activity, search, " +
		"directory, incoming shares, and the system tag catalog; optional, at most one",
	Forms: []string{kindAccount},
}, {
	Name:        kindAdmin,
	Description: "the provisioning reads of an administrator identity; only valid as the sole target of a connection",
	Forms:       []string{kindAdmin},
}}

// isKind reports whether name is the first segment of a typed target.
func isKind(name string) bool {
	for _, kind := range targetKinds {
		if kind.Name == name {
			return true
		}
	}
	return false
}

// selection is what a connection binds of one kind: everything the identity reaches, or the listed IDs.
type selection struct {
	all bool
	ids []string
}

// bound reports whether the kind is bound at all.
func (s selection) bound() bool { return s.all || len(s.ids) > 0 }

// scope is the parsed target set of one connection. Lists are exact and case-sensitive; there are no
// wildcards.
type scope struct {
	hasFolder    bool
	folder       []string
	calendars    selection
	addressbooks selection
	talks        selection
	decks        selection
	notes        selection
	account      bool
	admin        bool
}

// scopeOf reads the targets of a resolved connection. The single target field keeps its old meaning, the
// root folder, and refuses a value whose first segment names a kind; the targets list is typed.
func scopeOf(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, errors.New("no connection was selected")
	}
	if len(resolved.Targets) == 0 {
		if strings.TrimSpace(resolved.Target) == "" {
			return scope{}, errors.New("a Nextcloud connection needs at least one target")
		}
		root, err := parseLegacyRoot(resolved.Target)
		if err != nil {
			return scope{}, err
		}
		return scope{hasFolder: true, folder: root}, nil
	}
	return parseScope(resolved.Targets)
}

// parseLegacyRoot reads the single target field: a root folder as before. A first segment that equals a
// kind name is refused instead of guessed, because it may be a folder of that name or a typed target.
func parseLegacyRoot(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("a Nextcloud connection needs a fixed root folder as its target")
	}
	first, _, _ := strings.Cut(strings.Trim(trimmed, "/"), "/")
	if isKind(first) {
		return nil, errors.New("a single target whose first path segment is a target kind name is ambiguous; " +
			"list it as folder/PATH in targets")
	}
	return parseRoot(trimmed)
}

// parseScope reads a typed target list.
func parseScope(values []string) (scope, error) {
	var s scope
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return scope{}, errors.New("a Nextcloud target must not be empty")
		}
		if seen[value] {
			return scope{}, errors.New("the Nextcloud target list names a target more than once")
		}
		seen[value] = true
		if err := s.add(value); err != nil {
			return scope{}, err
		}
	}
	if s.admin && (s.hasFolder || s.account || s.calendars.bound() || s.addressbooks.bound() || s.talks.bound() ||
		s.decks.bound() || s.notes.bound() || len(values) != 1) {
		return scope{}, errors.New("the admin target must be the only target of a Nextcloud connection")
	}
	for _, sel := range []selection{s.calendars, s.addressbooks, s.talks, s.decks, s.notes} {
		if sel.all && len(sel.ids) > 0 {
			return scope{}, errors.New("a Nextcloud target kind is bound both as a whole and by ID")
		}
		if len(sel.ids) > maxTargetsPerKind {
			return scope{}, errors.New("a Nextcloud connection may list at most 100 targets per kind")
		}
	}
	return s, nil
}

func (s *scope) add(value string) error {
	first, rest, hasRest := strings.Cut(value, "/")
	if !isKind(first) {
		// A value without a kind is the root folder, as before.
		return s.setFolder(value)
	}
	switch first {
	case kindFolder:
		if !hasRest {
			return s.setFolder("/")
		}
		if rest == "" || strings.HasPrefix(rest, "/") {
			return errors.New("a folder target is folder or folder/PATH with a relative path")
		}
		return s.setFolder(rest)
	case kindAccount, kindAdmin:
		if hasRest {
			return errors.New("the account and admin targets take no ID")
		}
		if first == kindAccount {
			s.account = true
		} else {
			s.admin = true
		}
		return nil
	}
	var sel *selection
	var check func(string) error
	switch first {
	case kindCalendar:
		sel, check = &s.calendars, checkSegment
	case kindAddressbook:
		sel, check = &s.addressbooks, checkSegment
	case kindTalk:
		sel, check = &s.talks, checkSegment
	case kindDeck:
		sel, check = &s.decks, checkBoardID
	default:
		sel, check = &s.notes, checkCategory
	}
	if !hasRest {
		if sel.all {
			return errors.New("the Nextcloud target list names a target more than once")
		}
		sel.all = true
		return nil
	}
	if err := check(rest); err != nil {
		return errors.New("a Nextcloud target ID is unusable: " + err.Error())
	}
	sel.ids = append(sel.ids, rest)
	return nil
}

func (s *scope) setFolder(raw string) error {
	if s.hasFolder {
		return errors.New("a Nextcloud connection binds at most one folder")
	}
	root, err := parseRoot(raw)
	if err != nil {
		return err
	}
	s.hasFolder, s.folder = true, root
	return nil
}

func checkBoardID(id string) error {
	if id == "" || len(id) > maxBoardIDLength || !digitsOnly(id) {
		return errors.New("a Deck board ID is a number")
	}
	return nil
}

func checkCategory(category string) error {
	if category == "" {
		return errors.New("a notes category must not be empty")
	}
	_, err := splitRelative(category)
	return err
}

// validateTarget checks one entry on its own. A value without a kind is a folder; a value with one is
// checked as the typed form.
func validateTarget(raw string) error {
	var s scope
	value := strings.TrimSpace(raw)
	if value == "" {
		return errors.New("a Nextcloud target must not be empty")
	}
	return s.add(value)
}

// validateSingleTarget checks the single target field, which keeps the old root folder meaning.
func validateSingleTarget(raw string) error {
	_, err := parseLegacyRoot(raw)
	return err
}

func validateTargetSet(values []string) error {
	_, err := parseScope(values)
	return err
}

// requireFolder returns the root folder a Files tool works in. A connection without a folder target
// refuses locally, before any credential access or request, and names no other target.
func requireFolder(resolved *config.Resolved) ([]string, error) {
	s, err := scopeOf(resolved)
	if err != nil {
		return nil, err
	}
	if !s.hasFolder {
		return nil, &provider.Error{
			Class: provider.ClassPermission, Op: "open",
			Message: "this connection is not bound to a Files folder",
		}
	}
	return s.folder, nil
}

// folderBound wraps a Files handler so a connection without a folder target refuses before the handler
// reads any argument, credential, or local file.
func folderBound(handler capability.Handler) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		if _, err := requireFolder(resolved); err != nil {
			return nil, err
		}
		return handler(ctx, resolved, secrets, red, raw)
	}
}

// requireAccount refuses a connection without an account target locally, before any credential access or
// request, and names no other target.
func requireAccount(resolved *config.Resolved) error {
	s, err := scopeOf(resolved)
	if err != nil {
		return err
	}
	if !s.account {
		return &provider.Error{
			Class: provider.ClassPermission, Op: "open",
			Message: "this connection is not bound to the account of the identity",
		}
	}
	return nil
}

// accountBound is folderBound for the tools that reach the instance rather than a folder.
func accountBound(handler capability.Handler) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		if err := requireAccount(resolved); err != nil {
			return nil, err
		}
		return handler(ctx, resolved, secrets, red, raw)
	}
}

// requireCalendar refuses a connection without a calendar target locally, before any credential access or
// request, and names no other target.
func requireCalendar(resolved *config.Resolved) (selection, error) {
	s, err := scopeOf(resolved)
	if err != nil {
		return selection{}, err
	}
	if !s.calendars.bound() {
		return selection{}, &provider.Error{
			Class: provider.ClassPermission, Op: "open", Message: "this connection is not bound to calendars",
		}
	}
	return s.calendars, nil
}

// calendarBound is folderBound for the calendar tools.
func calendarBound(handler capability.Handler) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		if _, err := requireCalendar(resolved); err != nil {
			return nil, err
		}
		return handler(ctx, resolved, secrets, red, raw)
	}
}
