package baserow

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	maxCollaborators = 100
	// maxFreeBytes caps the raw size of a free value (AI prompt, field constraints) before it is parsed.
	maxFreeBytes = 16 * 1024
	maxLongText  = 2048
	// maxDecimalText bounds a decimal or floating-point literal.
	maxDecimalText = 64
)

// fieldProperty is one member that a field entry may carry beyond the base members. The output schema and the
// field list of fields.list are generated from this table, so a member cannot be in one and not the other.
type fieldProperty struct{ name, schema, description string }

const (
	intType  = `{"type":"integer"}`
	boolType = `{"type":"boolean"}`
	strType  = `{"type":"string"}`
)

var fieldProperties = []fieldProperty{
	{"table_id", intType, "Table the field belongs to"},
	{"order", intType, "Position of the field, lowest first"},
	{"description", strType, "Field description, untrusted"},
	{"immutable_type", boolType, "True when the field type cannot be changed"},
	{"immutable_properties", boolType, "True when the field properties cannot be changed"},
	{"database_id", intType, "Database the field belongs to"},
	{"workspace_id", intType, "Workspace the field belongs to"},
	{"db_index", boolType, "True when the field has a database index"},
	{"field_constraints", `{}`, "Constraints of the field as Baserow reports them, untrusted; left out when too large"},
	{"field_constraints_omitted", boolType, "True when field_constraints exceeded the limits and was left out"},
	{"text_default", strType, "Default of a text field, untrusted"},
	{"long_text_enable_rich_text", boolType, "True when a long text or AI field uses rich text"},
	{"number_decimal_places", intType, "Decimal places of a number or computed number"},
	{"number_negative", boolType, "True when a number may be negative"},
	{"number_prefix", strType, "Prefix shown before a number, untrusted"},
	{"number_suffix", strType, "Suffix shown after a number, untrusted"},
	{"number_separator", strType, "Thousand and decimal separator format of a number"},
	{"number_default", strType, "Default of a number field as decimal text"},
	{"max_value", intType, "Highest rating of a rating field"},
	{"color", strType, "Symbol color of a rating field, untrusted"},
	{"style", strType, "Symbol style of a rating field"},
	{"boolean_default", boolType, "Default of a boolean field"},
	{"duration_format", strType, "Display format of a duration"},
	{"date_format", strType, "Date format"},
	{"date_include_time", boolType, "True when a date includes the time"},
	{"date_time_format", strType, "Time format, 24 or 12"},
	{"date_show_tzinfo", boolType, "True when a date shows its time zone"},
	{"date_force_timezone", strType, "Time zone forced for a date, untrusted"},
	{"available_collaborators", `{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
		`"name":{"type":"string"}},"required":["id","name"],"additionalProperties":false}}`,
		"Collaborators a user field may name, at most 100 with id and name, untrusted"},
	{"notify_user_when_added", boolType, "True when a collaborator is notified when added"},
	{"single_select_default", intType, "Option ID that a single select starts with"},
	{"multiple_select_default", `{"type":"array","items":{"type":"integer"}}`, "Option IDs that a multiple select starts with"},
	{"link_row_related_field_id", intType, "Field of the other table that mirrors this link, for an allowed link target"},
	{"link_row_limit_selection_view_id", intType, "View that limits the selectable rows, for an allowed link target"},
	{"link_row_table_primary_field", strType, "Name of the other table's primary field, for an allowed link target, untrusted"},
	{"link_row_multiple_relationships", boolType, "True when a link may hold several rows"},
	{"formula_type", strType, "Result type of a formula, count, rollup, or lookup"},
	{"array_formula_type", strType, "Element type when the result is an array"},
	{"nullable", boolType, "True when a computed result may be empty"},
	{"error", strType, "Error of a computed field, left out unless its formula text may be shown, untrusted"},
	{"formula", strType, "Formula text, left out unless every link of the table points into the targets, untrusted"},
	{"through_field_id", intType, "Link field a count, rollup, or lookup goes through"},
	{"through_field_name", strType, "Name of the link field a lookup goes through, untrusted"},
	{"target_field_id", intType, "Field of the linked table a rollup or lookup reads, for an allowed link target"},
	{"target_field_name", strType, "Name of that field, for an allowed link target, untrusted"},
	{"rollup_function", strType, "Function a rollup applies"},
	{"allow_endpoint_authentication", boolType, "True when a password field may authenticate through an endpoint"},
	{"form_view_id", intType, "Form view of a form-edit-row field"},
	{"label", strType, "Label of a button field, untrusted"},
	{"has_workflow_actions", boolType, "True when a button runs workflow actions"},
	{"requires_reconfiguration", boolType, "True when a button needs reconfiguration"},
	{"opens_new_tab", boolType, "True when a button opens a new tab"},
	{"ai_generative_ai_type", strType, "AI provider type of an AI field, untrusted"},
	{"ai_generative_ai_model", strType, "AI model of an AI field, untrusted"},
	{"ai_output_type", strType, "Output type of an AI field"},
	{"ai_temperature", `{"type":"number"}`, "Temperature of an AI field"},
	{"ai_prompt", `{"type":"object"}`, "Prompt of an AI field as Baserow reports it, untrusted; left out when too large"},
	{"ai_prompt_omitted", boolType, "True when ai_prompt exceeded the limits and was left out"},
	{"ai_file_field_id", intType, "File field an AI field reads"},
	{"ai_auto_update", boolType, "True when an AI field updates itself"},
}

// fieldsItemProperties is the item schema of fields.list: the base members and every generated member.
func fieldsItemProperties() string {
	var out strings.Builder
	out.WriteString(`"id":{"type":"integer"},"name":{"type":"string"},"type":{"type":"string"},` +
		`"primary":{"type":"boolean"},"read_only":{"type":"boolean"},"link_table_id":{"type":"integer"},` +
		`"select_options":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
		`"value":{"type":"string"},"color":{"type":"string"}},"additionalProperties":false}}`)
	for _, property := range fieldProperties {
		out.WriteString(`,"` + property.name + `":` + property.schema)
	}
	return out.String()
}

// fieldItemDescriptions describes every member of a field entry.
func fieldItemDescriptions() []capability.Field {
	out := []capability.Field{
		{Name: "fields[].id", Description: "Field identifier"}, {Name: "fields[].name", Description: "Field name, untrusted"},
		{Name: "fields[].type", Description: "Field type"}, {Name: "fields[].primary", Description: "True for the primary field"},
		{Name: "fields[].read_only", Description: "True when the field cannot be written"},
		{Name: "fields[].link_table_id", Description: "Other table of a link field, only when it is inside the targets"},
		{Name: "fields[].select_options", Description: "Options of a select field with id, value, and color, untrusted"},
	}
	for _, property := range fieldProperties {
		out = append(out, capability.Field{Name: "fields[]." + property.name, Description: property.description})
	}
	return out
}

type collaboratorJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Collaborator is a person a user field may name.
type Collaborator struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// fieldExtra holds the members of a field answer that fields.list reads beyond the base members. Pointers keep
// a false or zero value apart from a missing one.
type fieldExtra struct {
	TableID                      *int64             `json:"table_id"`
	Order                        *int64             `json:"order"`
	Description                  *string            `json:"description"`
	ImmutableType                *bool              `json:"immutable_type"`
	ImmutableProperties          *bool              `json:"immutable_properties"`
	DatabaseID                   *int64             `json:"database_id"`
	WorkspaceID                  *int64             `json:"workspace_id"`
	DBIndex                      *bool              `json:"db_index"`
	FieldConstraints             json.RawMessage    `json:"field_constraints"`
	TextDefault                  *string            `json:"text_default"`
	LongTextEnableRichText       *bool              `json:"long_text_enable_rich_text"`
	NumberDecimalPlaces          *int64             `json:"number_decimal_places"`
	NumberNegative               *bool              `json:"number_negative"`
	NumberPrefix                 *string            `json:"number_prefix"`
	NumberSuffix                 *string            `json:"number_suffix"`
	NumberSeparator              *string            `json:"number_separator"`
	NumberDefault                json.RawMessage    `json:"number_default"`
	MaxValue                     *int64             `json:"max_value"`
	Color                        *string            `json:"color"`
	Style                        *string            `json:"style"`
	BooleanDefault               *bool              `json:"boolean_default"`
	DurationFormat               *string            `json:"duration_format"`
	DateFormat                   *string            `json:"date_format"`
	DateIncludeTime              *bool              `json:"date_include_time"`
	DateTimeFormat               *string            `json:"date_time_format"`
	DateShowTzinfo               *bool              `json:"date_show_tzinfo"`
	DateForceTimezone            *string            `json:"date_force_timezone"`
	AvailableCollaborators       []collaboratorJSON `json:"available_collaborators"`
	NotifyUserWhenAdded          *bool              `json:"notify_user_when_added"`
	SingleSelectDefault          *int64             `json:"single_select_default"`
	MultipleSelectDefault        []int64            `json:"multiple_select_default"`
	LinkRowRelatedFieldID        *int64             `json:"link_row_related_field_id"`
	LinkRowLimitSelectionViewID  *int64             `json:"link_row_limit_selection_view_id"`
	LinkRowTablePrimaryField     *string            `json:"link_row_table_primary_field"`
	LinkRowMultipleRelationships *bool              `json:"link_row_multiple_relationships"`
	FormulaType                  *string            `json:"formula_type"`
	ArrayFormulaType             *string            `json:"array_formula_type"`
	Nullable                     *bool              `json:"nullable"`
	Error                        *string            `json:"error"`
	Formula                      *string            `json:"formula"`
	ThroughFieldID               *int64             `json:"through_field_id"`
	ThroughFieldName             *string            `json:"through_field_name"`
	TargetFieldID                *int64             `json:"target_field_id"`
	TargetFieldName              *string            `json:"target_field_name"`
	RollupFunction               *string            `json:"rollup_function"`
	AllowEndpointAuthentication  *bool              `json:"allow_endpoint_authentication"`
	FormViewID                   *int64             `json:"form_view_id"`
	Label                        *string            `json:"label"`
	HasWorkflowActions           *bool              `json:"has_workflow_actions"`
	RequiresReconfiguration      *bool              `json:"requires_reconfiguration"`
	OpensNewTab                  *bool              `json:"opens_new_tab"`
	AIGenerativeAIType           *string            `json:"ai_generative_ai_type"`
	AIGenerativeAIModel          *string            `json:"ai_generative_ai_model"`
	AIOutputType                 *string            `json:"ai_output_type"`
	AITemperature                json.RawMessage    `json:"ai_temperature"`
	AIPrompt                     json.RawMessage    `json:"ai_prompt"`
	AIFileFieldID                *int64             `json:"ai_file_field_id"`
	AIAutoUpdate                 *bool              `json:"ai_auto_update"`
}

// FieldSchema holds the schema members of a field. Every member is optional.
type FieldSchema struct {
	TableID                      *int64         `json:"table_id,omitempty"`
	Order                        *int64         `json:"order,omitempty"`
	Description                  string         `json:"description,omitempty"`
	ImmutableType                *bool          `json:"immutable_type,omitempty"`
	ImmutableProperties          *bool          `json:"immutable_properties,omitempty"`
	DatabaseID                   *int64         `json:"database_id,omitempty"`
	WorkspaceID                  *int64         `json:"workspace_id,omitempty"`
	DBIndex                      *bool          `json:"db_index,omitempty"`
	FieldConstraints             any            `json:"field_constraints,omitempty"`
	FieldConstraintsOmitted      bool           `json:"field_constraints_omitted,omitempty"`
	TextDefault                  string         `json:"text_default,omitempty"`
	LongTextEnableRichText       *bool          `json:"long_text_enable_rich_text,omitempty"`
	NumberDecimalPlaces          *int64         `json:"number_decimal_places,omitempty"`
	NumberNegative               *bool          `json:"number_negative,omitempty"`
	NumberPrefix                 string         `json:"number_prefix,omitempty"`
	NumberSuffix                 string         `json:"number_suffix,omitempty"`
	NumberSeparator              string         `json:"number_separator,omitempty"`
	NumberDefault                string         `json:"number_default,omitempty"`
	MaxValue                     *int64         `json:"max_value,omitempty"`
	Color                        string         `json:"color,omitempty"`
	Style                        string         `json:"style,omitempty"`
	BooleanDefault               *bool          `json:"boolean_default,omitempty"`
	DurationFormat               string         `json:"duration_format,omitempty"`
	DateFormat                   string         `json:"date_format,omitempty"`
	DateIncludeTime              *bool          `json:"date_include_time,omitempty"`
	DateTimeFormat               string         `json:"date_time_format,omitempty"`
	DateShowTzinfo               *bool          `json:"date_show_tzinfo,omitempty"`
	DateForceTimezone            string         `json:"date_force_timezone,omitempty"`
	AvailableCollaborators       []Collaborator `json:"available_collaborators,omitempty"`
	NotifyUserWhenAdded          *bool          `json:"notify_user_when_added,omitempty"`
	SingleSelectDefault          *int64         `json:"single_select_default,omitempty"`
	MultipleSelectDefault        []int64        `json:"multiple_select_default,omitempty"`
	LinkRowRelatedFieldID        *int64         `json:"link_row_related_field_id,omitempty"`
	LinkRowLimitSelectionViewID  *int64         `json:"link_row_limit_selection_view_id,omitempty"`
	LinkRowTablePrimaryField     string         `json:"link_row_table_primary_field,omitempty"`
	LinkRowMultipleRelationships *bool          `json:"link_row_multiple_relationships,omitempty"`
	FormulaType                  string         `json:"formula_type,omitempty"`
	ArrayFormulaType             string         `json:"array_formula_type,omitempty"`
	Nullable                     *bool          `json:"nullable,omitempty"`
	Error                        string         `json:"error,omitempty"`
	Formula                      string         `json:"formula,omitempty"`
	ThroughFieldID               *int64         `json:"through_field_id,omitempty"`
	ThroughFieldName             string         `json:"through_field_name,omitempty"`
	TargetFieldID                *int64         `json:"target_field_id,omitempty"`
	TargetFieldName              string         `json:"target_field_name,omitempty"`
	RollupFunction               string         `json:"rollup_function,omitempty"`
	AllowEndpointAuthentication  *bool          `json:"allow_endpoint_authentication,omitempty"`
	FormViewID                   *int64         `json:"form_view_id,omitempty"`
	Label                        string         `json:"label,omitempty"`
	HasWorkflowActions           *bool          `json:"has_workflow_actions,omitempty"`
	RequiresReconfiguration      *bool          `json:"requires_reconfiguration,omitempty"`
	OpensNewTab                  *bool          `json:"opens_new_tab,omitempty"`
	AIGenerativeAIType           string         `json:"ai_generative_ai_type,omitempty"`
	AIGenerativeAIModel          string         `json:"ai_generative_ai_model,omitempty"`
	AIOutputType                 string         `json:"ai_output_type,omitempty"`
	AITemperature                json.Number    `json:"ai_temperature,omitempty"`
	AIPrompt                     any            `json:"ai_prompt,omitempty"`
	AIPromptOmitted              bool           `json:"ai_prompt_omitted,omitempty"`
	AIFileFieldID                *int64         `json:"ai_file_field_id,omitempty"`
	AIAutoUpdate                 *bool          `json:"ai_auto_update,omitempty"`
}

// fieldGate carries what the target binding decides for one table's field list.
type fieldGate struct {
	// formulaOpen is true when formula text and errors may be shown: the connection has the wildcard, or every
	// link field of the table points into the targets. A formula text can name fields of linked tables.
	formulaOpen bool
	// throughInScope holds the link fields whose other table is inside the targets.
	throughInScope map[int64]bool
}

func (c *Client) gateFields(raws []fieldJSON) fieldGate {
	gate := fieldGate{formulaOpen: true, throughInScope: map[int64]bool{}}
	for _, entry := range raws {
		if entry.Type != "link_row" {
			continue
		}
		if entry.LinkRowTableID != nil && c.inScope(*entry.LinkRowTableID) {
			gate.throughInScope[entry.ID] = true
		} else {
			gate.formulaOpen = false
		}
	}
	if c.scope.wildcard {
		gate.formulaOpen = true
	}
	return gate
}

// schemaOf converts the schema members of one field answer. linkInScope says whether the field's own other
// table is inside the targets.
func schemaOf(entry fieldJSON, linkInScope bool, gate fieldGate) FieldSchema {
	x := entry.fieldExtra
	out := FieldSchema{
		TableID: x.TableID, Order: x.Order, ImmutableType: x.ImmutableType, ImmutableProperties: x.ImmutableProperties,
		DatabaseID: x.DatabaseID, WorkspaceID: x.WorkspaceID, DBIndex: x.DBIndex,
		LongTextEnableRichText: x.LongTextEnableRichText, NumberDecimalPlaces: x.NumberDecimalPlaces,
		NumberNegative: x.NumberNegative, MaxValue: x.MaxValue, BooleanDefault: x.BooleanDefault,
		DateIncludeTime: x.DateIncludeTime, DateShowTzinfo: x.DateShowTzinfo, NotifyUserWhenAdded: x.NotifyUserWhenAdded,
		SingleSelectDefault: x.SingleSelectDefault, LinkRowMultipleRelationships: x.LinkRowMultipleRelationships,
		Nullable: x.Nullable, ThroughFieldID: x.ThroughFieldID, AllowEndpointAuthentication: x.AllowEndpointAuthentication,
		FormViewID: x.FormViewID, HasWorkflowActions: x.HasWorkflowActions,
		RequiresReconfiguration: x.RequiresReconfiguration, OpensNewTab: x.OpensNewTab,
		AIFileFieldID: x.AIFileFieldID, AIAutoUpdate: x.AIAutoUpdate,
		Description:         cleanText(x.Description, maxLongText),
		TextDefault:         cleanText(x.TextDefault, maxStringLength),
		NumberPrefix:        cleanText(x.NumberPrefix, maxStringLength),
		NumberSuffix:        cleanText(x.NumberSuffix, maxStringLength),
		NumberSeparator:     cleanText(x.NumberSeparator, maxStringLength),
		NumberDefault:       decimalText(x.NumberDefault),
		Color:               cleanText(x.Color, maxStringLength),
		Style:               cleanText(x.Style, maxStringLength),
		DurationFormat:      cleanText(x.DurationFormat, maxStringLength),
		DateFormat:          cleanText(x.DateFormat, maxStringLength),
		DateTimeFormat:      cleanText(x.DateTimeFormat, maxStringLength),
		DateForceTimezone:   cleanText(x.DateForceTimezone, maxStringLength),
		FormulaType:         cleanText(x.FormulaType, maxStringLength),
		ArrayFormulaType:    cleanText(x.ArrayFormulaType, maxStringLength),
		ThroughFieldName:    cleanText(x.ThroughFieldName, maxStringLength),
		RollupFunction:      cleanText(x.RollupFunction, maxStringLength),
		Label:               cleanText(x.Label, maxStringLength),
		AIGenerativeAIType:  cleanText(x.AIGenerativeAIType, maxStringLength),
		AIGenerativeAIModel: cleanText(x.AIGenerativeAIModel, maxStringLength),
		AIOutputType:        cleanText(x.AIOutputType, maxStringLength),
		AITemperature:       numberLiteral(x.AITemperature),
	}
	out.FieldConstraints, out.FieldConstraintsOmitted = freeValue(x.FieldConstraints)
	out.AIPrompt, out.AIPromptOmitted = freeValue(x.AIPrompt)
	for i, collaborator := range x.AvailableCollaborators {
		if i >= maxCollaborators {
			break
		}
		out.AvailableCollaborators = append(out.AvailableCollaborators,
			Collaborator{ID: collaborator.ID, Name: cleanText(&collaborator.Name, maxStringLength)})
	}
	if len(x.MultipleSelectDefault) > maxSelectOptions {
		out.MultipleSelectDefault = x.MultipleSelectDefault[:maxSelectOptions]
	} else {
		out.MultipleSelectDefault = x.MultipleSelectDefault
	}
	if linkInScope {
		out.LinkRowRelatedFieldID = x.LinkRowRelatedFieldID
		out.LinkRowLimitSelectionViewID = x.LinkRowLimitSelectionViewID
		out.LinkRowTablePrimaryField = cleanText(x.LinkRowTablePrimaryField, maxStringLength)
	}
	if x.ThroughFieldID != nil && gate.throughInScope[*x.ThroughFieldID] {
		out.TargetFieldID = x.TargetFieldID
		out.TargetFieldName = cleanText(x.TargetFieldName, maxStringLength)
	}
	if gate.formulaOpen {
		out.Formula = cleanText(x.Formula, maxLongText)
		out.Error = cleanText(x.Error, maxLongText)
	}
	return out
}

// cleanText replaces control characters and invalid UTF-8 of a provider text and cuts it at the limit.
func cleanText(value *string, limit int) string {
	if value == nil {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, *value)
	return boundString(cleaned, limit)
}

// decimalText returns a decimal as text, from a JSON string or number, and nothing for any other value.
func decimalText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	text := string(raw)
	if raw[0] == '"' {
		if json.Unmarshal(raw, &text) != nil {
			return ""
		}
	}
	if len(text) == 0 || len(text) > maxDecimalText || strings.Trim(text, "0123456789.-+eE") != "" {
		return ""
	}
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return ""
	}
	return text
}

// numberLiteral keeps a JSON number as it was written, without a float round trip.
func numberLiteral(raw json.RawMessage) json.Number {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] == '"' || len(raw) > maxDecimalText {
		return ""
	}
	if _, err := strconv.ParseFloat(string(raw), 64); err != nil {
		return ""
	}
	return json.Number(raw)
}

// freeValue bounds a free JSON value like a cell. A value beyond the limits is left out and reported as omitted
// instead of being cut, so what is shown is never a partial object.
func freeValue(raw json.RawMessage) (value any, omitted bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	if len(raw) > maxFreeBytes {
		return nil, true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var parsed any
	if decoder.Decode(&parsed) != nil {
		return nil, true
	}
	switch typed := parsed.(type) {
	case []any:
		if len(typed) == 0 {
			return nil, false
		}
	case map[string]any:
		if len(typed) == 0 {
			return nil, false
		}
	}
	cleaned, ok := cleanFree(parsed, 0)
	if !ok {
		return nil, true
	}
	return cleaned, false
}

func cleanFree(value any, depth int) (any, bool) {
	switch typed := value.(type) {
	case string:
		if len(typed) > maxCellString {
			return nil, false
		}
		return cleanText(&typed, maxCellString), true
	case []any:
		if depth >= maxCellDepth || len(typed) > maxCellItems {
			return nil, false
		}
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			cleaned, ok := cleanFree(item, depth+1)
			if !ok {
				return nil, false
			}
			out = append(out, cleaned)
		}
		return out, true
	case map[string]any:
		if depth >= maxCellDepth || len(typed) > maxCellItems {
			return nil, false
		}
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if len(key) > maxStringLength {
				return nil, false
			}
			cleaned, ok := cleanFree(item, depth+1)
			if !ok {
				return nil, false
			}
			out[cleanText(&key, maxStringLength)] = cleaned
		}
		return out, true
	}
	return value, true
}
