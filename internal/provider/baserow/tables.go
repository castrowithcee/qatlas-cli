package baserow

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	allTablesPath = "/api/database/tables/all-tables/"
	maxTables     = 1000
)

const idSchema = `{"type":"integer","minimum":1}`

var schemaRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: schemaSensitive}

var tablesList = capability.Descriptor{
	ID: Provider + ".tables.list", Version: 1, Title: "List Baserow tables",
	Description: "List the tables the database token reads, restricted to the connection's tables and databases " +
		"when it has one; names are untrusted provider data",
	Tags: []string{"baserow", "tables", "list", "database"}, Risk: schemaRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"tables":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},` +
		`"database_id":{"type":"integer"},"order":{"type":"integer"}},"required":["id","name"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["tables","count","truncated"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "tables", Description: "Tables with id (used as table_id by the other tools), name, database_id, and order"},
		{Name: "tables[].id", Description: "Table identifier, used as table_id by the other tools"},
		{Name: "tables[].name", Description: "Table name, untrusted"},
		{Name: "tables[].database_id", Description: "Database the table belongs to"},
		{Name: "tables[].order", Description: "Position of the table, lowest first"},
		{Name: "count", Description: "Number of tables in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at 1000 tables"},
	},
	Examples: []capability.Example{{Description: "List the reachable tables", Arguments: json.RawMessage(`{}`)}},
}

type tableJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	DatabaseID int64  `json:"database_id"`
	Order      *int64 `json:"order"`
}

// Table is one table of the connection's allow-list.
type Table struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	DatabaseID int64  `json:"database_id,omitempty"`
	Order      *int64 `json:"order,omitempty"`
}

// TablesResult is the answer of tables.list.
type TablesResult struct {
	Tables    []Table `json:"tables"`
	Count     int     `json:"count"`
	Truncated bool    `json:"truncated"`
}

func invokeTablesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListTables(ctx, "list tables")
}

// ListTables reads every table of the token and keeps only those the allow-list admits.
func (c *Client) ListTables(ctx context.Context, op string) (*TablesResult, error) {
	var raw []tableJSON
	if err := c.get(ctx, op, allTablesPath, nil, &raw); err != nil {
		return nil, err
	}
	result := &TablesResult{Tables: []Table{}}
	for _, entry := range raw {
		if entry.ID <= 0 || !c.scope.allows(entry.ID) && !slices.Contains(c.scope.databases, entry.DatabaseID) {
			continue
		}
		if len(result.Tables) >= maxTables {
			result.Truncated = true
			break
		}
		result.Tables = append(result.Tables, Table{ID: entry.ID, Name: bounded(entry.Name), DatabaseID: entry.DatabaseID,
			Order: entry.Order})
	}
	result.Count = len(result.Tables)
	return result, nil
}
