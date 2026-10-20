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
// tables listed together with every table of the databases listed.
type scope struct {
	wildcard  bool
	tables    []int64
	databases []int64
}

// allows reports whether the table itself is listed. A table that belongs to a listed database is proven by
// Client.admit, which needs the table list.
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

// parseScope reads the configured targets: the single wildcard, or one or more table/TABLE_ID and
// database/DATABASE_ID entries, none named twice. No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	if len(values) == 0 {
		return scope{}, errors.New("a Baserow connection needs at least one table/TABLE_ID or database/DATABASE_ID target, or the * wildcard")
	}
	if len(values) == 1 && strings.TrimSpace(values[0]) == wildcard {
		return scope{wildcard: true}, nil
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
	values := provider.TargetsOf(resolved)
	bound, err := parseScope(values)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// selectTable checks a table_id argument against the connection's allow-list before any secret is resolved
// and before any request is sent. A table that is not listed itself passes only when database targets exist;
// Client.admit then proves its database. The refusal never names the table.
func selectTable(resolved *config.Resolved, tableID int64) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if tableID <= 0 || len(strconv.FormatInt(tableID, 10)) > maxIDDigits {
		return invalidRequest("table_id must be a positive integer")
	}
	if !bound.allows(tableID) && len(bound.databases) == 0 {
		return invalidRequest(outsideTargets)
	}
	return nil
}

const outsideTargets = "table_id is outside the targets of this connection"

// tableDatabases reads the token's table list once per client and keeps each table's database.
func (c *Client) tableDatabases(ctx context.Context, op string) (map[int64]int64, error) {
	if c.databaseOf != nil {
		return c.databaseOf, nil
	}
	var raw []tableJSON
	if err := c.get(ctx, op, allTablesPath, nil, &raw); err != nil {
		return nil, err
	}
	known := make(map[int64]int64, len(raw))
	for _, entry := range raw {
		if entry.ID > 0 {
			known[entry.ID] = entry.DatabaseID
		}
	}
	c.databaseOf = known
	return known, nil
}

// inScope reports whether the table is listed or belongs to a listed database, by the loaded table list.
func (c *Client) inScope(tableID int64) bool {
	if c.scope.allows(tableID) {
		return true
	}
	return c.databaseOf != nil && c.databaseOf[tableID] != 0 && slices.Contains(c.scope.databases, c.databaseOf[tableID])
}

// admit proves, before any action on the table, that it is listed or in a listed database. A listed table
// costs no request; otherwise one read of the table list answers it, once per client.
func (c *Client) admit(ctx context.Context, op string, tableID int64) error {
	if c.scope.allows(tableID) {
		return nil
	}
	if len(c.scope.databases) == 0 {
		return invalidRequest(outsideTargets)
	}
	if _, err := c.tableDatabases(ctx, op); err != nil {
		return err
	}
	if !c.inScope(tableID) {
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
