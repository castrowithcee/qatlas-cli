package baserow

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	tablePrefix    = "table/"
	databasePrefix = "database/"
	wildcard       = "*"
	// maxIDDigits bounds a configured or argument identifier well above any realistic Baserow id.
	maxIDDigits = 18
)

// scope is the table boundary of one connection: every table the token reads (an explicit wildcard), or the
// tables listed together with every table of the databases listed. Exclusions take tables out of either; they
// are never targets.
type scope struct {
	wildcard          bool
	tables            []int64
	databases         []int64
	excludedTables    []int64
	excludedDatabases []int64
}

// reaches is the one predicate behind every gate, mask and check: whether the table is reachable, given the
// database it belongs to (0 when unknown). An exclusion wins over a table, a database and the wildcard, and a
// table whose database is unknown counts as excluded where databases are excluded.
func (s scope) reaches(tableID, databaseID int64) bool {
	if slices.Contains(s.excludedTables, tableID) {
		return false
	}
	if len(s.excludedDatabases) > 0 && (databaseID == 0 || slices.Contains(s.excludedDatabases, databaseID)) {
		return false
	}
	return s.wildcard || slices.Contains(s.tables, tableID) || databaseID != 0 && slices.Contains(s.databases, databaseID)
}

// mayReach is the necessary condition of reaches that the table ID alone decides: it fails only for a table
// that no database could make reachable, so a refusal needs neither a secret nor a request.
func (s scope) mayReach(tableID int64) bool {
	return !slices.Contains(s.excludedTables, tableID) &&
		(s.wildcard || slices.Contains(s.tables, tableID) || len(s.databases) > 0)
}

// unrestricted is the wildcard without exclusions: every table is reachable and nothing needs checking.
func (s scope) unrestricted() bool {
	return s.wildcard && len(s.excludedTables) == 0 && len(s.excludedDatabases) == 0
}

// needsDatabases reports whether judging a table takes the table list, which maps tables to databases.
func (s scope) needsDatabases() bool { return len(s.databases) > 0 || len(s.excludedDatabases) > 0 }

// parseScope reads the configured targets: the single wildcard, or one or more table/TABLE_ID and
// database/DATABASE_ID entries, none named twice; then the optional exclusions of the same two forms, each named
// once and never equal to a target. The core validation is not relied on. No error quotes a configured value.
func parseScope(values []string, excludes ...string) (scope, error) {
	if len(values) == 0 {
		return scope{}, errors.New("a Baserow connection needs at least one table/TABLE_ID or database/DATABASE_ID target, or the * wildcard")
	}
	if len(values) == 1 && strings.TrimSpace(values[0]) == wildcard {
		return parseExcludes(scope{wildcard: true}, excludes)
	}
	var bound scope
	seenTables, seenDatabases := map[int64]bool{}, map[int64]bool{}
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == wildcard {
			return scope{}, errors.New("the Baserow * wildcard must be the only configured target")
		}
		database, id, err := parseTarget(raw)
		if err != nil {
			return scope{}, err
		}
		if database {
			if seenDatabases[id] {
				return scope{}, errors.New("the Baserow allow-list names a database more than once")
			}
			seenDatabases[id] = true
			bound.databases = append(bound.databases, id)
			continue
		}
		if seenTables[id] {
			return scope{}, errors.New("the Baserow table allow-list names a table more than once")
		}
		seenTables[id] = true
		bound.tables = append(bound.tables, id)
	}
	return parseExcludes(bound, excludes)
}

// parseExcludes adds the exclusions to a scope that already holds the targets.
func parseExcludes(bound scope, excludes []string) (scope, error) {
	invalid := errors.New("a Baserow exclusion must be table/TABLE_ID or database/DATABASE_ID, named once and " +
		"different from every target")
	for _, raw := range excludes {
		database, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, invalid
		}
		excluded, targets := &bound.excludedTables, bound.tables
		if database {
			excluded, targets = &bound.excludedDatabases, bound.databases
		}
		if slices.Contains(*excluded, id) || slices.Contains(targets, id) {
			return scope{}, invalid
		}
		*excluded = append(*excluded, id)
	}
	return bound, nil
}

// parseTarget reads one table/TABLE_ID or database/DATABASE_ID entry; database reports the second form.
func parseTarget(raw string) (database bool, id int64, err error) {
	const form = "a Baserow target must be table/TABLE_ID or database/DATABASE_ID with a positive integer, or *"
	rest, isDatabase := strings.CutPrefix(raw, databasePrefix)
	if !isDatabase {
		var isTable bool
		if rest, isTable = strings.CutPrefix(raw, tablePrefix); !isTable {
			return false, 0, errors.New(form)
		}
	}
	id, ok := parseID(rest)
	if !ok {
		return false, 0, errors.New(form)
	}
	return isDatabase, id, nil
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
	_, _, err := parseTarget(raw)
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
	bound, err := parseScope(provider.TargetsOf(resolved), provider.ExcludesOf(resolved)...)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// selectTable checks a table_id argument against the connection's targets and exclusions before any secret is
// resolved and before any request is sent. An excluded table, and one that is not listed itself while no database
// target exists, is refused here; Client.admit proves the rest by database. The refusal never names the table.
func selectTable(resolved *config.Resolved, tableID int64) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if tableID <= 0 || len(strconv.FormatInt(tableID, 10)) > maxIDDigits {
		return invalidRequest("table_id must be a positive integer")
	}
	if !bound.mayReach(tableID) {
		return invalidRequest(outsideTargets)
	}
	return nil
}

const outsideTargets = "table_id is outside the targets of this connection"

// indexTables maps each table of a table list to its database, 0 where the answer gives none.
func indexTables(raw []tableJSON) map[int64]int64 {
	known := make(map[int64]int64, len(raw))
	for _, entry := range raw {
		if entry.ID > 0 {
			known[entry.ID] = entry.DatabaseID
		}
	}
	return known
}

// tableDatabases reads the token's table list once per client and keeps each table's database.
func (c *Client) tableDatabases(ctx context.Context, op string) (map[int64]int64, error) {
	if c.databaseOf != nil {
		return c.databaseOf, nil
	}
	var raw []tableJSON
	if err := c.get(ctx, op, allTablesPath, nil, &raw); err != nil {
		return nil, err
	}
	c.databaseOf = indexTables(raw)
	return c.databaseOf, nil
}

// reaches reports whether the table is reachable, by the loaded table list. Where databases matter and the list
// is not loaded or does not show the table, its database is unknown.
func (c *Client) reaches(tableID int64) bool {
	return c.scope.reaches(tableID, c.databaseOf[tableID])
}

// admit proves, before any action on the table, that it is reachable. A table that no exclusion of a database
// can touch and that the targets name or the wildcard covers costs no request; otherwise one read of the table
// list answers it, once per client.
func (c *Client) admit(ctx context.Context, op string, tableID int64) error {
	if !c.scope.mayReach(tableID) {
		return invalidRequest(outsideTargets)
	}
	if len(c.scope.excludedDatabases) == 0 && c.scope.reaches(tableID, 0) {
		return nil
	}
	if _, err := c.tableDatabases(ctx, op); err != nil {
		return err
	}
	if !c.reaches(tableID) {
		return invalidRequest(outsideTargets)
	}
	return nil
}

// openTable opens the client of a call on one table and proves the table is inside the targets.
func openTable(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string, tableID int64) (*Client, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.admit(ctx, op, tableID); err != nil {
		return nil, err
	}
	return client, nil
}
