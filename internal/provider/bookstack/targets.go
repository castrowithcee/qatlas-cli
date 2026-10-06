package bookstack

import (
	"errors"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	bookPrefix = "book/"
	// maxBooks bounds the books one connection may be bound to.
	maxBooks = 100
	// maxIDDigits bounds a configured or argument identifier well above any realistic BookStack id.
	maxIDDigits = 18
)

const (
	targetForm        = "a BookStack target must be book/BOOK_ID with a positive integer"
	outsidePage       = "the page is outside the books this connection is bound to"
	outsideChapter    = "the chapter is outside the books this connection is bound to"
	outsideBook       = "book_id is outside the books this connection is bound to"
	bookIDRequiredMsg = "book_id is required for a connection bound to several books"
)

func invalidRequest(message string) error { return &application.InvalidRequestError{Message: message} }

// scope is the book boundary of one connection. An unbound scope (no target configured) reaches everything
// the token reaches.
type scope struct {
	books []int64
}

func (s scope) bound() bool { return len(s.books) > 0 }

func (s scope) allows(bookID int64) bool {
	for _, allowed := range s.books {
		if allowed == bookID {
			return true
		}
	}
	return false
}

// parseScope reads the configured targets. No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	if len(values) > maxBooks {
		return scope{}, errors.New("a BookStack connection may be bound to at most 100 books")
	}
	var bound scope
	seen := map[int64]bool{}
	for _, raw := range values {
		id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		if seen[id] {
			return scope{}, errors.New("the BookStack book list names a book more than once")
		}
		seen[id] = true
		bound.books = append(bound.books, id)
	}
	return bound, nil
}

// parseTarget reads one book/BOOK_ID entry.
func parseTarget(raw string) (int64, error) {
	if !strings.HasPrefix(raw, bookPrefix) {
		return 0, errors.New(targetForm)
	}
	id, ok := parseID(strings.TrimPrefix(raw, bookPrefix))
	if !ok {
		return 0, errors.New(targetForm)
	}
	return id, nil
}

// parseID accepts plain, non-padded decimal digits only.
func parseID(raw string) (int64, bool) {
	if raw == "" || len(raw) > maxIDDigits || raw[0] == '0' {
		return 0, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil
}

func validateTarget(raw string) error {
	_, err := parseTarget(strings.TrimSpace(raw))
	return err
}

func validateSet(values []string) error {
	_, err := parseScope(values)
	return err
}

// boundScope reads the connection's scope before any secret is resolved. A configured target that cannot be
// read makes the connection unusable instead of widening it.
func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	values := resolved.Targets
	if len(values) == 0 && strings.TrimSpace(resolved.Target) != "" {
		values = []string{resolved.Target}
	}
	bound, err := parseScope(values)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// checkBook checks a book_id argument against the connection's books. An unbound connection accepts any
// positive identifier. The refusal never names the book.
func (s scope) checkBook(bookID int64) error {
	if bookID <= 0 || len(strconv.FormatInt(bookID, 10)) > maxIDDigits {
		return invalidRequest("book_id must be a positive integer")
	}
	if s.bound() && !s.allows(bookID) {
		return invalidRequest(outsideBook)
	}
	return nil
}
