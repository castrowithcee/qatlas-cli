package baserow

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxFields        = 500
	maxSelectOptions = 100
)

var tableIDArgument = capability.Argument{Name: "table_id", Required: true,
	Description: "Table identifier from baserow.tables.list; must be inside this connection's table targets and not excluded"}

var fieldsList = capability.Descriptor{
	ID: Provider + ".fields.list", Version: 1, Title: "List Baserow fields",
	Description: "List the fields of one table with their type-specific schema properties. " +
		"What points into another table (link target, lookup and rollup target, formula text) is shown only when " +
		"that table is reachable (inside the targets, not excluded). Names, options, and formulas are untrusted provider data",
	Tags: []string{"baserow", "fields", "list", "schema"}, Risk: schemaRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"table_id":` + idSchema + `},` +
		`"required":["table_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"fields":{"type":"array","items":` +
		`{"type":"object","properties":{` + fieldsItemProperties() + `},` +
		`"required":["id","name","type"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["fields","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{tableIDArgument},
	Fields: append([]capability.Field{
		{Name: "fields", Description: "Fields of the table with their schema members; members that point into a table " +
			"outside the targets are left out"},
		{Name: "count", Description: "Number of fields in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at 500 fields"},
	}, fieldItemDescriptions()...),
	Examples: []capability.Example{{Description: "List the fields of one table", Arguments: json.RawMessage(`{"table_id":1}`)}},
}

type selectOptionJSON struct {
	ID    int64  `json:"id"`
	Value string `json:"value"`
	Color string `json:"color"`
}

type fieldJSON struct {
	ID             int64              `json:"id"`
	Name           string             `json:"name"`
	Type           string             `json:"type"`
	Primary        bool               `json:"primary"`
	ReadOnly       bool               `json:"read_only"`
	LinkRowTableID *int64             `json:"link_row_table_id"`
	SelectOptions  []selectOptionJSON `json:"select_options"`
	fieldExtra
}

// SelectOption is one option of a select field.
type SelectOption struct {
	ID    int64  `json:"id"`
	Value string `json:"value"`
	Color string `json:"color,omitempty"`
}

// Field is the stable view of one field.
type Field struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	Type          string         `json:"type"`
	Primary       bool           `json:"primary,omitempty"`
	ReadOnly      bool           `json:"read_only,omitempty"`
	LinkTableID   int64          `json:"link_table_id,omitempty"`
	SelectOptions []SelectOption `json:"select_options,omitempty"`
	FieldSchema
}

// FieldsResult is the answer of fields.list.
type FieldsResult struct {
	Fields    []Field `json:"fields"`
	Count     int     `json:"count"`
	Truncated bool    `json:"truncated"`
}

type tableArguments struct {
	TableID int64 `json:"table_id"`
}

func tablePath(tableID int64) string { return strconv.FormatInt(tableID, 10) + "/" }

func invokeFieldsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list fields"
	var input tableArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTable(resolved, input.TableID); err != nil {
		return nil, err
	}
	client, err := openTable(ctx, resolved, secrets, red, op, input.TableID)
	if err != nil {
		return nil, err
	}
	raws, err := client.readFields(ctx, op, input.TableID)
	if err != nil {
		return nil, err
	}
	gate := client.gateFields(raws)
	result := &FieldsResult{Fields: []Field{}}
	for _, entry := range raws {
		if len(result.Fields) >= maxFields {
			result.Truncated = true
			break
		}
		field := Field{ID: entry.ID, Name: bounded(entry.Name), Type: bounded(entry.Type),
			Primary: entry.Primary, ReadOnly: entry.ReadOnly}
		linkInScope := entry.LinkRowTableID != nil && client.reaches(*entry.LinkRowTableID)
		if linkInScope {
			field.LinkTableID = *entry.LinkRowTableID
		}
		field.FieldSchema = schemaOf(entry, linkInScope, gate)
		for i, option := range entry.SelectOptions {
			if i >= maxSelectOptions {
				break
			}
			field.SelectOptions = append(field.SelectOptions, SelectOption{
				ID: option.ID, Value: bounded(option.Value), Color: bounded(option.Color)})
		}
		result.Fields = append(result.Fields, field)
	}
	result.Count = len(result.Fields)
	return result, nil
}

// readFields reads the raw fields of one table, whose ID the caller has already checked.
func (c *Client) readFields(ctx context.Context, op string, tableID int64) ([]fieldJSON, error) {
	if c.scope.needsDatabases() {
		// Link targets are judged by their database, so the table list is read with the fields.
		if _, err := c.tableDatabases(ctx, op); err != nil {
			return nil, err
		}
	}
	var raw []fieldJSON
	if err := c.get(ctx, op, "/api/database/fields/table/"+tablePath(tableID), nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
