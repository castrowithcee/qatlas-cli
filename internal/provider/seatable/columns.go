package seatable

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The three column tools use the one fixed column route with three fixed methods. The table and the column
// are addressed by the names resolved from the base metadata. Only the column types of columnTypes can be
// created or set, and the column data has one bounded shape per type; there is no free provider body.
const (
	columnsPath       = "/columns/"
	minColumnWidth    = 50
	maxColumnWidth    = 1000
	maxColumnOptions  = 50
	columnUncertain   = "; this change may have taken effect, list the columns before repeating it"
	optionIDAlphabet  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	optionIDLength    = 6
	defaultRateColor  = "#FF8000"
	defaultRateStyle  = "dtable-icon-rate"
	defaultRateNumber = 5
)

// columnTypes is the allow-list of column types a change may create or set. Formula, link-formula, button
// (which runs scripts), auto-number, geolocation and url are not part of it.
var columnTypes = map[string]bool{
	"text": true, "long-text": true, "number": true, "date": true, "duration": true,
	"single-select": true, "multiple-select": true, "collaborator": true, "image": true, "file": true,
	"email": true, "checkbox": true, "rate": true, "creator": true, "ctime": true,
	"last-modifier": true, "mtime": true, "link": true,
}

var (
	numberFormats = []string{"number", "percent", "dollar", "euro", "yuan"}
	decimalFormat = []string{"dot", "comma"}
	thousandsMode = []string{"no", "space", "comma"}
	dateFormats   = []string{"YYYY-MM-DD", "YYYY-MM-DD HH:mm", "M/D/YYYY", "M/D/YYYY HH:mm", "DD/MM/YYYY",
		"DD/MM/YYYY HH:mm", "DD.MM.YYYY", "DD.MM.YYYY HH:mm"}
	durationFormats = []string{"h:mm", "h:mm:ss"}
	rateStyles      = []string{"dtable-icon-rate", "dtable-icon-like", "dtable-icon-praise", "dtable-icon-flag"}
)

const (
	columnNameSchema = `{"type":"string","minLength":1,"maxLength":255,"pattern":"^[^.}{` + "`" + `]*$"}`
	columnRefSchema  = `{"type":"string","minLength":1,"maxLength":255}`
	columnTypeSchema = `{"type":"string","enum":["text","long-text","number","date","duration","single-select",` +
		`"multiple-select","collaborator","image","file","email","checkbox","rate","creator","ctime",` +
		`"last-modifier","mtime","link"]}`
	columnDataSchema = `{"type":"object","properties":{` +
		`"format":{"type":"string","enum":["number","percent","dollar","euro","yuan","YYYY-MM-DD","YYYY-MM-DD HH:mm",` +
		`"M/D/YYYY","M/D/YYYY HH:mm","DD/MM/YYYY","DD/MM/YYYY HH:mm","DD.MM.YYYY","DD.MM.YYYY HH:mm"]},` +
		`"decimal":{"type":"string","enum":["dot","comma"]},` +
		`"thousands":{"type":"string","enum":["no","space","comma"]},` +
		`"duration_format":{"type":"string","enum":["h:mm","h:mm:ss"]},` +
		`"rate_max_number":{"type":"integer","minimum":1,"maximum":10},` +
		`"rate_style_color":` + optionColorSchema + `,` +
		`"rate_style_type":{"type":"string","enum":["dtable-icon-rate","dtable-icon-like","dtable-icon-praise","dtable-icon-flag"]},` +
		`"options":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","properties":{` +
		`"name":` + optionNameSchema + `,"color":` + optionColorSchema + `,"text_color":` + optionColorSchema +
		`},"required":["name"],"additionalProperties":false}},` +
		`"link_table":{"type":"string","minLength":1,"maxLength":512}},"additionalProperties":false}`
)

const columnDataHelp = "data: number takes format, decimal, thousands; date takes format; duration takes duration_format; " +
	"rate takes rate_max_number, rate_style_color, rate_style_type; single-select and multiple-select take options " +
	"(up to 50: name, color, text_color); link takes link_table, which must be a table the connection allows"

var columnCreate = columnDescriptor("create", capability.EffectCreate, capability.IdempotencyNonIdempotent, false,
	"Create a column in an allowed table; the type comes from a fixed list (formula, button, auto-number and "+
		"geolocation are not offered), a link column needs a joined table the connection allows. "+columnDataHelp,
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`,"name":`+columnNameSchema+`,"type":`+columnTypeSchema+
		`,"data":`+columnDataSchema+`,"after":`+columnRefSchema+`},"required":["table","name","type"],"additionalProperties":false}`,
	"created",
	[]capability.Argument{tableRefArg, {Name: "name", Description: "Name of the new column", Required: true},
		{Name: "type", Description: "Column type from the fixed list", Required: true},
		{Name: "data", Description: "Type-specific settings, see the description"},
		{Name: "after", Description: "Name or key of the column the new one is placed after; at the end by default"}},
	json.RawMessage(`{"table":"id:0000","name":"Status","type":"single-select","data":{"options":[{"name":"Offen","color":"#FFE9A8"}]}}`))

var columnUpdate = columnDescriptor("update", capability.EffectUpdate, capability.IdempotencyIdempotent, false,
	"Change one property of a column of an allowed table per call: rename it (name), change its type (type, "+
		"with data; existing cell values may be lost), set its width (width), or move it (target). "+columnDataHelp+
		". A link column, or a change to a link type, needs a joined table the connection allows",
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`,"column":`+columnRefSchema+`,"name":`+columnNameSchema+
		`,"type":`+columnTypeSchema+`,"data":`+columnDataSchema+`,"width":{"type":"integer","minimum":50,"maximum":1000},`+
		`"target":`+columnRefSchema+`},"required":["table","column"],"additionalProperties":false}`,
	"updated",
	[]capability.Argument{tableRefArg, {Name: "column", Description: "Name or key of the column", Required: true},
		{Name: "name", Description: "New name; exactly one of name, type, width and target"},
		{Name: "type", Description: "New column type from the fixed list"},
		{Name: "data", Description: "Type-specific settings for the new type"},
		{Name: "width", Description: "New width from 50 to 1000"},
		{Name: "target", Description: "Name or key of the column whose position the column takes"}},
	json.RawMessage(`{"table":"id:0000","column":"Status","name":"Stand"}`))

var columnDelete = columnDescriptor("delete", capability.EffectDelete, capability.IdempotencyIdempotent, true,
	"Delete a column of an allowed table together with its cell values; system columns cannot be deleted",
	`{"type":"object","properties":{"table":`+tableSelectionSchema+`,"column":`+columnRefSchema+
		`},"required":["table","column"],"additionalProperties":false}`,
	"deleted",
	[]capability.Argument{tableRefArg, {Name: "column", Description: "Name or key of the column", Required: true}},
	json.RawMessage(`{"table":"id:0000","column":"Notiz"}`))

func columnDescriptor(action string, effect capability.Effect, idempotency capability.Idempotency, allowList bool,
	what, input, done string, args []capability.Argument, example json.RawMessage) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".columns." + action, Version: 1,
		Title:       strings.ToUpper(action[:1]) + action[1:] + " a SeaTable column",
		Description: what, Tags: []string{"seatable", "base", "columns", "schema", action},
		Provider: Provider, RequiresToolAllowList: allowList,
		Risk: capability.Risk{Effect: effect, Idempotency: idempotency,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema: json.RawMessage(input),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + done + `":{"type":"boolean"}},"required":["` +
			done + `"],"additionalProperties":false}`),
		Arguments: args,
		Fields:    []capability.Field{{Name: done, Description: "True when SeaTable accepted the change"}},
		Examples:  []capability.Example{{Description: what, Arguments: example}},
	}
}

// ColumnData is the bounded, per-type settings of a column. Only the fields that belong to the type are
// accepted.
type ColumnData struct {
	Format         string        `json:"format"`
	Decimal        string        `json:"decimal"`
	Thousands      string        `json:"thousands"`
	DurationFormat string        `json:"duration_format"`
	RateMax        int           `json:"rate_max_number"`
	RateColor      string        `json:"rate_style_color"`
	RateStyle      string        `json:"rate_style_type"`
	Options        []OptionInput `json:"options"`
	LinkTable      string        `json:"link_table"`
}

// ColumnInput carries the arguments of the three column tools.
type ColumnInput struct {
	Table  string      `json:"table"`
	Column string      `json:"column"`
	Name   string      `json:"name"`
	Type   string      `json:"type"`
	Data   *ColumnData `json:"data"`
	Width  int         `json:"width"`
	Target string      `json:"target"`
	After  string      `json:"after"`
}

func invokeColumnsChange(op, kind string, change func(*Client, context.Context, string, ColumnInput) error,
	done string) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		var input ColumnInput
		if json.Unmarshal(raw, &input) != nil {
			return nil, providerError(op, "the validated arguments could not be read")
		}
		if resolved == nil {
			return nil, providerError(op, "no connection was selected")
		}
		bound, err := parseScope(resolved)
		if err != nil {
			return nil, providerError(op, err.Error())
		}
		if _, err := checkColumnRequest(op, kind, bound, input); err != nil {
			return nil, err
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		if err := change(client, ctx, op, input); err != nil {
			return nil, err
		}
		return map[string]bool{done: true}, nil
	}
}

// columnChange names which single property an update changes.
type columnChange string

const (
	changeNone   columnChange = ""
	changeName   columnChange = "name"
	changeType   columnChange = "type"
	changeWidth  columnChange = "width"
	changeTarget columnChange = "target"
)

// checkColumnRequest validates the shape and the table boundary without any I/O, so a malformed request or a
// table outside the boundary is refused before the credential is resolved. It returns what an update changes.
func checkColumnRequest(op, kind string, bound scope, input ColumnInput) (columnChange, error) {
	if err := checkTableRequest(op, "columns", bound, TableInput{Table: input.Table}); err != nil {
		return changeNone, err
	}
	refuse := func(message string) (columnChange, error) { return changeNone, providerError(op, message) }
	if kind == "create" {
		if input.Column != "" || input.Width != 0 || input.Target != "" {
			return refuse("column, width and target do not apply to a create")
		}
		if err := checkColumnName(input.Name); err != nil {
			return refuse(err.Error())
		}
		if input.After != "" && (!validName(input.After) || systemColumns[input.After]) {
			return refuse("the anchor column is unusable")
		}
		if _, err := columnData(op, input.Type, input.Data); err != nil {
			return changeNone, err
		}
		return changeNone, nil
	}
	if !validName(input.Column) || systemColumns[input.Column] {
		return refuse("the column is unusable; system columns cannot be changed")
	}
	if input.After != "" {
		return refuse("after applies to a create only")
	}
	if kind == "delete" {
		if input.Name != "" || input.Type != "" || input.Data != nil || input.Width != 0 || input.Target != "" {
			return refuse("a delete takes only table and column")
		}
		return changeNone, nil
	}
	var changes []columnChange
	if input.Name != "" {
		changes = append(changes, changeName)
	}
	if input.Type != "" {
		changes = append(changes, changeType)
	}
	if input.Width != 0 {
		changes = append(changes, changeWidth)
	}
	if input.Target != "" {
		changes = append(changes, changeTarget)
	}
	if len(changes) != 1 {
		return refuse("give exactly one of name, type, width and target")
	}
	if input.Data != nil && changes[0] != changeType {
		return refuse("data applies to a type change only")
	}
	switch changes[0] {
	case changeName:
		if err := checkColumnName(input.Name); err != nil {
			return refuse(err.Error())
		}
	case changeType:
		if _, err := columnData(op, input.Type, input.Data); err != nil {
			return changeNone, err
		}
	case changeWidth:
		if input.Width < minColumnWidth || input.Width > maxColumnWidth {
			return refuse("the width is from 50 to 1000")
		}
	case changeTarget:
		if !validName(input.Target) || systemColumns[input.Target] {
			return refuse("the target column is unusable")
		}
	}
	return changes[0], nil
}

// checkColumnName keeps a name usable in the column route: the API refuses a dot, braces and a backtick, and
// surrounding blanks would be lost.
func checkColumnName(name string) error {
	if !validName(name) || name != strings.TrimSpace(name) || strings.ContainsAny(name, ".{}`") || systemColumns[name] {
		return errColumnName
	}
	return nil
}

var errColumnName = errors.New("a column name has 1 to 255 printable characters, no '.', '{', '}' or backtick, " +
	"no surrounding blanks, and is not a system column name")

func oneOf(value string, list []string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// columnData checks the type and its settings and returns the column_data of the request, without the
// link tables, which need the metadata. It returns nil for a type without settings.
func columnData(op, kind string, data *ColumnData) (map[string]any, error) {
	refuse := func(message string) (map[string]any, error) { return nil, providerError(op, message) }
	if !columnTypes[kind] {
		return refuse("the column type is not one of the supported types")
	}
	if data == nil {
		data = &ColumnData{}
	}
	given := map[string]bool{
		"format": data.Format != "", "decimal": data.Decimal != "", "thousands": data.Thousands != "",
		"duration_format": data.DurationFormat != "", "rate_max_number": data.RateMax != 0,
		"rate_style_color": data.RateColor != "", "rate_style_type": data.RateStyle != "",
		"options": len(data.Options) != 0, "link_table": data.LinkTable != "",
	}
	allowed := map[string][]string{
		"number": {"format", "decimal", "thousands"}, "date": {"format"}, "duration": {"duration_format"},
		"rate":          {"rate_max_number", "rate_style_color", "rate_style_type"},
		"single-select": {"options"}, "multiple-select": {"options"}, "link": {"link_table"},
	}[kind]
	for field, present := range given {
		if present && !oneOf(field, allowed) {
			return refuse("the data field " + field + " does not apply to this column type")
		}
	}
	either := func(value, fallback string, list []string) (string, bool) {
		if value == "" {
			return fallback, true
		}
		return value, oneOf(value, list)
	}
	switch kind {
	case "number":
		format, ok1 := either(data.Format, "number", numberFormats)
		decimal, ok2 := either(data.Decimal, "dot", decimalFormat)
		thousands, ok3 := either(data.Thousands, "no", thousandsMode)
		if !ok1 || !ok2 || !ok3 {
			return refuse("the number settings are not supported")
		}
		return map[string]any{"format": format, "decimal": decimal, "thousands": thousands}, nil
	case "date":
		format, ok := either(data.Format, "YYYY-MM-DD", dateFormats)
		if !ok {
			return refuse("the date format is not supported")
		}
		return map[string]any{"format": format}, nil
	case "duration":
		format, ok := either(data.DurationFormat, "h:mm", durationFormats)
		if !ok {
			return refuse("the duration format is not supported")
		}
		return map[string]any{"format": "duration", "duration_format": format}, nil
	case "rate":
		style, ok := either(data.RateStyle, defaultRateStyle, rateStyles)
		number := data.RateMax
		if number == 0 {
			number = defaultRateNumber
		}
		color := data.RateColor
		if color == "" {
			color = defaultRateColor
		}
		if !ok || number < 1 || number > 10 || !colorPattern.MatchString(color) {
			return refuse("the rate settings are not supported")
		}
		return map[string]any{"rate_max_number": number, "rate_style_color": color, "rate_style_type": style}, nil
	case "single-select", "multiple-select":
		if len(data.Options) > maxColumnOptions {
			return refuse("a select column takes up to 50 options")
		}
		seen := map[string]bool{}
		list := make([]map[string]string, 0, len(data.Options))
		for _, option := range data.Options {
			if !validName(option.Name) || seen[option.Name] || option.NewName != "" ||
				(option.Color != "" && !colorPattern.MatchString(option.Color)) ||
				(option.TextColor != "" && !colorPattern.MatchString(option.TextColor)) {
				return refuse("an option is unusable or repeated, colors are written as #RRGGBB")
			}
			seen[option.Name] = true
			entry := optionBody(option, "")
			entry["id"] = newOptionID()
			list = append(list, entry)
		}
		return map[string]any{"options": list}, nil
	case "link":
		if _, _, err := parseReference(data.LinkTable); err != nil {
			return refuse("a link column needs link_table, the joined table by name or id:")
		}
	}
	return nil, nil
}

// newOptionID draws the six-character identifier the API asks of a select option.
var newOptionID = func() string {
	out := make([]byte, optionIDLength)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(optionIDAlphabet))))
		if err != nil {
			out[i] = optionIDAlphabet[i]
			continue
		}
		out[i] = optionIDAlphabet[n.Int64()]
	}
	return string(out)
}

func findColumn(table tableJSON, reference string) (columnJSON, bool) {
	for _, byKey := range []bool{false, true} {
		for _, candidate := range table.Columns {
			if (!byKey && candidate.Name == reference) || (byKey && candidate.Key == reference) {
				return candidate, validName(candidate.Name)
			}
		}
	}
	return columnJSON{}, false
}

// counterpart returns the table a link column of this table joins.
func counterpart(current tableJSON, column columnJSON) (string, bool) {
	first, second := column.Data.TableID, column.Data.OtherTableID
	switch {
	case first == current.ID && second != "":
		return second, true
	case second == current.ID && first != "":
		return first, true
	}
	return "", false
}

// guardLinkColumn refuses to touch an existing link column whose joined table is outside the allow-list.
func (c *Client) guardLinkColumn(op string, document metadataJSON, current tableJSON, column columnJSON) error {
	if column.Type != "link" {
		return nil
	}
	other, ok := counterpart(current, column)
	if !ok || !c.tableAllowed(document, other) {
		return providerError(op, "the link column joins a table outside this connection's allow-list")
	}
	return nil
}

// linkData resolves the joined table of a new link column and requires it inside the allow-list.
func (c *Client) linkData(op string, document metadataJSON, current tableJSON, reference string) (map[string]any, error) {
	ref, byID, err := parseReference(reference)
	if err != nil {
		return nil, providerError(op, "the joined table is unusable")
	}
	for _, candidate := range document.Metadata.Tables {
		if (byID && candidate.ID == ref) || (!byID && candidate.Name == ref) {
			if !validName(candidate.Name) || !c.tableAllowed(document, candidate.ID) {
				break
			}
			return map[string]any{"table": current.Name, "other_table": candidate.Name}, nil
		}
	}
	return nil, providerError(op, "the joined table is unknown or outside this connection's allow-list")
}

// columnScope resolves the table and the metadata; the metadata read is the only provider I/O before a
// metadata-based refusal.
func (c *Client) columnScope(ctx context.Context, op, table string) (*viewScope, metadataJSON, error) {
	scoped, err := c.resolveViewTable(ctx, op, table)
	if err != nil {
		return nil, metadataJSON{}, err
	}
	document, err := c.metadata(ctx, op)
	if err != nil {
		return nil, metadataJSON{}, err
	}
	return scoped, document, nil
}

// fullColumnData adds the resolved link tables to the settings of a type.
func (c *Client) fullColumnData(op string, document metadataJSON, current tableJSON, kind string,
	data *ColumnData) (map[string]any, error) {
	settings, err := columnData(op, kind, data)
	if err != nil {
		return nil, err
	}
	if kind == "link" {
		return c.linkData(op, document, current, data.LinkTable)
	}
	return settings, nil
}

func (c *Client) sendColumn(ctx context.Context, op, method string, scoped *viewScope, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	c.meta = nil
	return c.changeOnce(ctx, op, method, gatewayPath+url.PathEscape(scoped.access.uuid)+columnsPath,
		scoped.access.token, body, columnUncertain)
}

// CreateColumn sends exactly one request that creates a column.
func (c *Client) CreateColumn(ctx context.Context, op string, input ColumnInput) error {
	if _, err := checkColumnRequest(op, "create", c.scope, input); err != nil {
		return err
	}
	scoped, document, err := c.columnScope(ctx, op, input.Table)
	if err != nil {
		return err
	}
	for _, existing := range scoped.table.Columns {
		if existing.Name == input.Name {
			return providerError(op, "the table already has a column with this name")
		}
	}
	payload := map[string]any{"table_name": scoped.table.Name, "column_name": input.Name, "column_type": input.Type}
	if input.After != "" {
		anchor, ok := findColumn(scoped.table, input.After)
		if !ok {
			return providerError(op, "the anchor column does not exist")
		}
		payload["anchor_column"] = anchor.Name
	}
	settings, err := c.fullColumnData(op, document, scoped.table, input.Type, input.Data)
	if err != nil {
		return err
	}
	if settings != nil {
		payload["column_data"] = settings
	}
	return c.sendColumn(ctx, op, http.MethodPost, scoped, payload)
}

// UpdateColumn sends exactly one request that changes one property of a column.
func (c *Client) UpdateColumn(ctx context.Context, op string, input ColumnInput) error {
	change, err := checkColumnRequest(op, "update", c.scope, input)
	if err != nil {
		return err
	}
	scoped, document, err := c.columnScope(ctx, op, input.Table)
	if err != nil {
		return err
	}
	column, ok := findColumn(scoped.table, input.Column)
	if !ok {
		return providerError(op, "the table has no column with this name or key")
	}
	if err := c.guardLinkColumn(op, document, scoped.table, column); err != nil {
		return err
	}
	payload := map[string]any{"table_name": scoped.table.Name, "column": column.Name}
	switch change {
	case changeName:
		if input.Name == column.Name {
			return providerError(op, "the column already has this name")
		}
		for _, existing := range scoped.table.Columns {
			if existing.Name == input.Name {
				return providerError(op, "the table already has a column with this name")
			}
		}
		payload["op_type"], payload["new_column_name"] = "rename_column", input.Name
	case changeType:
		if column.Type == input.Type && column.Type != "single-select" && column.Type != "multiple-select" {
			return providerError(op, "the column already has this type")
		}
		settings, err := c.fullColumnData(op, document, scoped.table, input.Type, input.Data)
		if err != nil {
			return err
		}
		payload["op_type"], payload["new_column_type"] = "modify_column_type", input.Type
		if settings != nil {
			payload["column_data"] = settings
		}
	case changeWidth:
		payload["op_type"], payload["new_column_width"] = "resize_column", input.Width
	case changeTarget:
		target, ok := findColumn(scoped.table, input.Target)
		if !ok || target.Key == column.Key {
			return providerError(op, "the target column does not exist or is the column itself")
		}
		payload["op_type"], payload["target_column"] = "move_column", target.Name
	}
	return c.sendColumn(ctx, op, http.MethodPut, scoped, payload)
}

// DeleteColumn sends exactly one request that deletes a column with its cell values.
func (c *Client) DeleteColumn(ctx context.Context, op string, input ColumnInput) error {
	if _, err := checkColumnRequest(op, "delete", c.scope, input); err != nil {
		return err
	}
	scoped, document, err := c.columnScope(ctx, op, input.Table)
	if err != nil {
		return err
	}
	column, ok := findColumn(scoped.table, input.Column)
	if !ok {
		return providerError(op, "the table has no column with this name or key")
	}
	if err := c.guardLinkColumn(op, document, scoped.table, column); err != nil {
		return err
	}
	return c.sendColumn(ctx, op, http.MethodDelete, scoped,
		map[string]any{"table_name": scoped.table.Name, "column": column.Name})
}
