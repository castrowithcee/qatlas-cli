package infomaniakdav

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The two target kinds a connection combines. Each value is the last path segment of a collection below
// the discovered calendar or address book home set.
const (
	calendarPrefix    = "calendar/"
	addressbookPrefix = "addressbook/"

	maxTargetIDLength = 128
	maxTargetsPerKind = 100
)

// scope is the allow-list of one connection. Both lists are exact and case-sensitive; there are no
// wildcards. A kind without an entry lists nothing.
type scope struct {
	calendars    []string
	addressbooks []string
}

func contains(list []string, id string) bool {
	for _, allowed := range list {
		if allowed == id {
			return true
		}
	}
	return false
}

func scopeOf(resolved *config.Resolved) (scope, error) {
	values := provider.TargetsOf(resolved)
	return parseScope(values)
}

// parseScope reads the configured targets: at least one calendar/ID or addressbook/ID entry, none named
// twice within its own kind. No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	var bound scope
	for _, raw := range values {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		list := &bound.calendars
		if kind == "addressbook" {
			list = &bound.addressbooks
		}
		if contains(*list, id) {
			return scope{}, errors.New("the Infomaniak DAV target list names a collection more than once")
		}
		*list = append(*list, id)
	}
	if len(bound.calendars)+len(bound.addressbooks) == 0 {
		return scope{}, errors.New("an Infomaniak DAV connection needs at least one calendar/ID or addressbook/ID target")
	}
	if len(bound.calendars) > maxTargetsPerKind || len(bound.addressbooks) > maxTargetsPerKind {
		return scope{}, errors.New("an Infomaniak DAV connection may list at most 100 targets per kind")
	}
	return bound, nil
}

func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

func parseTarget(raw string) (kind, id string, err error) {
	switch {
	case strings.HasPrefix(raw, calendarPrefix):
		kind, id = "calendar", strings.TrimPrefix(raw, calendarPrefix)
	case strings.HasPrefix(raw, addressbookPrefix):
		kind, id = "addressbook", strings.TrimPrefix(raw, addressbookPrefix)
	default:
		return "", "", errors.New("an Infomaniak DAV target must be calendar/ID or addressbook/ID")
	}
	if !validCollectionID(id) {
		return "", "", errors.New("an Infomaniak DAV target ID must be one literal path segment of at most 128 " +
			"bytes without separators, percent signs, or control characters")
	}
	return kind, id, nil
}

// validCollectionID accepts one literal, decoded path segment: never empty, relative, percent-encoded,
// separated, or carrying a control character.
func validCollectionID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > maxTargetIDLength || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if r == '/' || r == '\\' || r == '%' || r == '?' || r == '#' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
