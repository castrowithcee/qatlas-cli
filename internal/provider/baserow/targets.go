package baserow

import (
	"errors"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	tablePrefix = "table/"
	wildcard    = "*"
	// maxIDDigits bounds a configured or argument identifier well above any realistic Baserow id.
	maxIDDigits = 18
)

// scope is the table boundary of one connection: every table the token reads (an explicit wildcard) or only
// the tables listed.
type scope struct {
	wildcard bool
	tables   []int64
}

func (s scope) allows(tableID int64) bool {
	if s.wildcard {
		return true
	}
	for _, allowed := range s.tables {
		if allowed == tableID {
			return true
		}
	}
	return false
}

// parseScope reads the configured targets: the single wildcard, or one or more table/TABLE_ID entries, none
// named twice. No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	if len(values) == 0 {
		return scope{}, errors.New("a Baserow connection needs at least one table/TABLE_ID target or the * wildcard")
	}
	if len(values) == 1 && strings.TrimSpace(values[0]) == wildcard {
		return scope{wildcard: true}, nil
	}
	var bound scope
	seen := map[int64]bool{}
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == wildcard {
			return scope{}, errors.New("the Baserow * wildcard must be the only configured target")
		}
		id, err := parseTarget(raw)
		if err != nil {
			return scope{}, err
		}
		if seen[id] {
			return scope{}, errors.New("the Baserow table allow-list names a table more than once")
		}
		seen[id] = true
		bound.tables = append(bound.tables, id)
	}
	return bound, nil
}

// parseTarget reads one table/TABLE_ID entry.
func parseTarget(raw string) (int64, error) {
	const form = "a Baserow target must be table/TABLE_ID with a positive integer, or *"
	if !strings.HasPrefix(raw, tablePrefix) {
		return 0, errors.New(form)
	}
	id, ok := parseID(strings.TrimPrefix(raw, tablePrefix))
	if !ok {
		return 0, errors.New(form)
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
	raw = strings.TrimSpace(raw)
	if raw == wildcard {
		return nil
	}
	_, err := parseTarget(raw)
	return err
}

func validateSet(values []string) error {
	_, err := parseScope(values)
	return err
}

// boundScope reads the connection's scope before any secret is resolved.
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

// selectTable checks a table_id argument against the connection's allow-list before any secret is resolved
// and before any request is sent. The refusal never names the table.
func selectTable(resolved *config.Resolved, tableID int64) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if tableID <= 0 || len(strconv.FormatInt(tableID, 10)) > maxIDDigits {
		return invalidRequest("table_id must be a positive integer")
	}
	if !bound.allows(tableID) {
		return invalidRequest("table_id is outside the targets of this connection")
	}
	return nil
}
