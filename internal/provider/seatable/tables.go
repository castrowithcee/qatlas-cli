package seatable

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// tablesPath is the table route of the API gateway. The fixed routes and methods below are the only ones
// the four table tools use; the table is addressed by name, resolved from the base metadata.
const (
	tablesPath     = "/tables/"
	duplicatePath  = tablesPath + "duplicate-table/"
	tableUncertain = "; this change may have taken effect, list the tables before repeating it"
)

const tableNameSchema = `{"type":"string","minLength":1,"maxLength":255}`

var tableRefArg = capability.Argument{Name: "table", Description: "Table reference returned by seatable.tables.list", Required: true}

var tablesCreate = tableMutationDescriptor("create", capability.EffectCreate, capability.IdempotencyNonIdempotent,
	dataSensitivity, false,
	"Create an empty table with a name in the base; only a connection that exposes the whole base (*) may do this, "+
		"because a new table cannot be part of an allow-list",
	`{"type":"object","properties":{"name":`+tableNameSchema+`},"required":["name"],"additionalProperties":false}`,
	`{"type":"object","properties":{"created":{"type":"boolean"}},"required":["created"],"additionalProperties":false}`,
	[]capability.Argument{{Name: "name", Description: "Name of the new table", Required: true}},
	json.RawMessage(`{"name":"Angebote"}`))

var tablesRename = tableMutationDescriptor("rename", capability.EffectUpdate, capability.IdempotencyIdempotent,
	dataSensitivity, false,
	"Rename a table allowed by the connection; a table that is a connection target by name cannot be renamed",
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`,"name":`+tableNameSchema+
		`},"required":["table","name"],"additionalProperties":false}`,
	`{"type":"object","properties":{"renamed":{"type":"boolean"}},"required":["renamed"],"additionalProperties":false}`,
	[]capability.Argument{tableRefArg, {Name: "name", Description: "New name of the table", Required: true}},
	json.RawMessage(`{"table":"id:0000","name":"Kundenstamm"}`))

var tablesDuplicate = tableMutationDescriptor("duplicate", capability.EffectCreate, capability.IdempotencyNonIdempotent,
	dataSensitivity, false,
	"Duplicate a table of the base, with its structure and optionally its rows; only a connection that exposes "+
		"the whole base (*) may do this, because the copy cannot be part of an allow-list",
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`,"with_rows":{"type":"boolean"}},`+
		`"required":["table"],"additionalProperties":false}`,
	`{"type":"object","properties":{"duplicated":{"type":"boolean"}},"required":["duplicated"],"additionalProperties":false}`,
	[]capability.Argument{tableRefArg, {Name: "with_rows", Description: "Also copy the rows; false by default"}},
	json.RawMessage(`{"table":"id:0000","with_rows":false}`))

var tablesDelete = tableMutationDescriptor("delete", capability.EffectDelete, capability.IdempotencyIdempotent,
	dataSensitivity, true,
	"Delete a table allowed by the connection together with its rows and views",
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`},"required":["table"],"additionalProperties":false}`,
	`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`,
	[]capability.Argument{tableRefArg},
	json.RawMessage(`{"table":"id:0001"}`))

func tableMutationDescriptor(action string, effect capability.Effect, idempotency capability.Idempotency,
	sensitivity string, allowList bool, what, input, output string, args []capability.Argument,
	example json.RawMessage) capability.Descriptor {
	done := action + "d"
	return capability.Descriptor{
		ID: Provider + ".tables." + action, Version: 1,
		Title:       strings.ToUpper(action[:1]) + action[1:] + " a SeaTable table",
		Description: what, Tags: []string{"seatable", "base", "tables", action, "table"},
		Provider: Provider, RequiresToolAllowList: allowList,
		Risk: capability.Risk{Effect: effect, Idempotency: idempotency,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: sensitivity},
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output),
		Arguments: args,
		Fields:    []capability.Field{{Name: done, Description: "True when SeaTable accepted the change"}},
		Examples:  []capability.Example{{Description: what, Arguments: example}},
	}
}

// TableInput carries the arguments of the four table tools.
type TableInput struct {
	Table    string `json:"table"`
	Name     string `json:"name"`
	WithRows bool   `json:"with_rows"`
}

// openForTableChange decodes the arguments and settles the connection's own table boundary and the shape of the
// request before the credential is resolved.
func openForTableChange(ctx context.Context, op, kind string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (*Client, TableInput, error) {
	var input TableInput
	if json.Unmarshal(raw, &input) != nil {
		return nil, input, providerError(op, "the validated arguments could not be read")
	}
	if resolved == nil {
		return nil, input, providerError(op, "no connection was selected")
	}
	bound, err := parseScope(resolved)
	if err != nil {
		return nil, input, providerError(op, err.Error())
	}
	if err := checkTableRequest(op, kind, bound, input); err != nil {
		return nil, input, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	return client, input, err
}

func invokeTablesChange(op, kind string, change func(*Client, context.Context, string, TableInput) error,
	done string) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		client, input, err := openForTableChange(ctx, op, kind, resolved, secrets, red, raw)
		if err != nil {
			return nil, err
		}
		if err := change(client, ctx, op, input); err != nil {
			return nil, err
		}
		return map[string]bool{done: true}, nil
	}
}

// checkTableRequest validates the shape and the connection boundary of a request without any I/O, so a
// malformed request or a table outside the boundary is refused before the credential is resolved. A table
// change never widens the boundary: a new or duplicated table is not in an allow-list, so both need the
// wildcard, and a rename cannot give an allowed table a name that an allow-list entry already claims.
func checkTableRequest(op, kind string, bound scope, input TableInput) error {
	if kind == "create" || kind == "duplicate" {
		if !bound.wildcard {
			return providerError(op, "this change needs a connection that exposes the whole base with *, "+
				"because the new table cannot be part of an allow-list")
		}
	}
	if kind == "create" || kind == "rename" {
		if err := checkNewTableName(input.Name); err != nil {
			return providerError(op, err.Error())
		}
	}
	if kind == "create" {
		return nil
	}
	selected, err := bound.selectTarget(input.Table)
	if err != nil || strings.TrimSpace(input.Table) == "" {
		return providerError(op, "the selected SeaTable table is unusable or outside this connection's allow-list")
	}
	if selected.view != "" {
		return providerError(op, "a table selection narrowed to one view cannot be changed")
	}
	if kind != "rename" || bound.wildcard {
		return nil
	}
	if selected.tableParam == "table_name" {
		return providerError(op, "a table that is configured as a connection target by name cannot be renamed")
	}
	for _, configured := range bound.targets {
		if configured.tableParam == "table_name" && configured.table == input.Name {
			return providerError(op, "the new name is claimed by a connection target")
		}
	}
	return nil
}

// checkNewTableName keeps a name addressable: a slash or an id: prefix would change how a target that
// names the table is read, and surrounding blanks would be lost when a target is parsed.
func checkNewTableName(name string) error {
	if !validName(name) || strings.Contains(name, "/") || name != strings.TrimSpace(name) ||
		strings.HasPrefix(name, idPrefix) || name == "*" {
		return errTableName
	}
	return nil
}

var errTableName = errors.New("a table name has 1 to 255 printable characters, no '/', no surrounding blanks, " +
	"no 'id:' prefix, and is not '*'")

func tablesURL(access *baseAccess, path string) string {
	return gatewayPath + url.PathEscape(access.uuid) + path
}

// CreateTable sends exactly one request that creates an empty table.
func (c *Client) CreateTable(ctx context.Context, op string, input TableInput) error {
	if err := checkTableRequest(op, "create", c.scope, input); err != nil {
		return err
	}
	access, err := c.access(ctx, op)
	if err != nil {
		return err
	}
	return c.changeTable(ctx, op, http.MethodPost, tablesURL(access, tablesPath), access,
		map[string]any{"table_name": input.Name})
}

// RenameTable sends exactly one request that renames the table.
func (c *Client) RenameTable(ctx context.Context, op string, input TableInput) error {
	if err := checkTableRequest(op, "rename", c.scope, input); err != nil {
		return err
	}
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return err
	}
	if scoped.table.Name == input.Name {
		return providerError(op, "the table already has this name")
	}
	if !c.scope.wildcard {
		for _, configured := range c.scope.targets {
			if configured.tableParam == "table_name" && matchesTable(configured, scoped.table) {
				return providerError(op, "a table that is configured as a connection target by name cannot be renamed")
			}
		}
	}
	return c.changeTable(ctx, op, http.MethodPut, tablesURL(scoped.access, tablesPath), scoped.access,
		map[string]any{"table_name": scoped.table.Name, "new_table_name": input.Name})
}

// DuplicateTable sends exactly one request that copies the table.
func (c *Client) DuplicateTable(ctx context.Context, op string, input TableInput) error {
	if err := checkTableRequest(op, "duplicate", c.scope, input); err != nil {
		return err
	}
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return err
	}
	return c.changeTable(ctx, op, http.MethodPost, tablesURL(scoped.access, duplicatePath), scoped.access,
		map[string]any{"table_name": scoped.table.Name, "is_duplicate_records": input.WithRows})
}

// DeleteTable sends exactly one request that deletes the table with its rows.
func (c *Client) DeleteTable(ctx context.Context, op string, input TableInput) error {
	if err := checkTableRequest(op, "delete", c.scope, input); err != nil {
		return err
	}
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return err
	}
	return c.changeTable(ctx, op, http.MethodDelete, tablesURL(scoped.access, tablesPath), scoped.access,
		map[string]any{"table_name": scoped.table.Name})
}

// changeTable sends one change and drops the cached metadata, because the schema of the base has changed
// or may have.
func (c *Client) changeTable(ctx context.Context, op, method, path string, access *baseAccess, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	c.meta = nil
	return c.changeOnce(ctx, op, method, path, access.token, body, tableUncertain)
}
