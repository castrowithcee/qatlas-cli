package seatable

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The three option tools use one fixed route with three fixed methods. The column is addressed by the name
// resolved from the base metadata, and only single-select and multiple-select columns are accepted.
const (
	optionsPath      = "/column-options/"
	maxOptionChanges = 50
	optionUncertain  = "; this change may have taken effect, list the columns before repeating it"
)

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

const optionFields = `"table":` + tableSelectionSchema + `,"column":{"type":"string","minLength":1,"maxLength":255}`
const optionColorSchema = `{"type":"string","pattern":"^#[0-9a-fA-F]{6}$"}`
const optionNameSchema = `{"type":"string","minLength":1,"maxLength":255}`

var optionsAdd = optionDescriptor("add", capability.EffectCreate, capability.IdempotencyNonIdempotent, false,
	"Add options to a single-select or multiple-select column of an allowed table",
	`{"type":"object","properties":{`+optionFields+`,"options":{"type":"array","minItems":1,"maxItems":50,"items":`+
		`{"type":"object","properties":{"name":`+optionNameSchema+`,"color":`+optionColorSchema+`,"text_color":`+optionColorSchema+
		`},"required":["name"],"additionalProperties":false}}},"required":["table","column","options"],"additionalProperties":false}`,
	[]capability.Argument{tableRefArg, columnArg,
		{Name: "options", Description: "Up to 50 new options: name, optional color and text_color as #RRGGBB", Required: true}},
	json.RawMessage(`{"table":"id:0000","column":"Status","options":[{"name":"Offen","color":"#FFE9A8"}]}`))

var optionsUpdate = optionDescriptor("update", capability.EffectUpdate, capability.IdempotencyIdempotent, false,
	"Rename or recolor existing options of a single-select or multiple-select column of an allowed table",
	`{"type":"object","properties":{`+optionFields+`,"options":{"type":"array","minItems":1,"maxItems":50,"items":`+
		`{"type":"object","properties":{"name":`+optionNameSchema+`,"new_name":`+optionNameSchema+`,"color":`+optionColorSchema+
		`,"text_color":`+optionColorSchema+`},"required":["name"],"additionalProperties":false}}},`+
		`"required":["table","column","options"],"additionalProperties":false}`,
	[]capability.Argument{tableRefArg, columnArg,
		{Name: "options", Description: "Up to 50 existing options: name, and new_name, color or text_color to change", Required: true}},
	json.RawMessage(`{"table":"id:0000","column":"Status","options":[{"name":"Offen","new_name":"Neu"}]}`))

var optionsDelete = optionDescriptor("delete", capability.EffectDelete, capability.IdempotencyIdempotent, true,
	"Delete options of a single-select or multiple-select column of an allowed table; cells that hold a deleted option are cleared",
	`{"type":"object","properties":{`+optionFields+`,"names":{"type":"array","minItems":1,"maxItems":50,"items":`+optionNameSchema+
		`}},"required":["table","column","names"],"additionalProperties":false}`,
	[]capability.Argument{tableRefArg, columnArg, {Name: "names", Description: "Up to 50 existing option names to delete", Required: true}},
	json.RawMessage(`{"table":"id:0000","column":"Status","names":["Offen"]}`))

var columnArg = capability.Argument{Name: "column", Description: "Name or key of a single-select or multiple-select column", Required: true}

func optionDescriptor(action string, effect capability.Effect, idempotency capability.Idempotency, allowList bool,
	what, input string, args []capability.Argument, example json.RawMessage) capability.Descriptor {
	done := map[string]string{"add": "added", "update": "updated", "delete": "deleted"}[action]
	return capability.Descriptor{
		ID: Provider + ".columns.options" + action, Version: 1,
		Title:       strings.ToUpper(action[:1]) + action[1:] + " SeaTable column options",
		Description: what, Tags: []string{"seatable", "base", "columns", "options", action},
		Provider: Provider, RequiresToolAllowList: allowList,
		Risk: capability.Risk{Effect: effect, Idempotency: idempotency,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:  json.RawMessage(input),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + done + `":{"type":"boolean"}},"required":["` + done + `"],"additionalProperties":false}`),
		Arguments:    args,
		Fields:       []capability.Field{{Name: done, Description: "True when SeaTable accepted the change"}},
		Examples:     []capability.Example{{Description: what, Arguments: example}},
	}
}

// OptionInput is one option of an add or update request.
type OptionInput struct {
	Name      string `json:"name"`
	NewName   string `json:"new_name"`
	Color     string `json:"color"`
	TextColor string `json:"text_color"`
}

// OptionsInput carries the arguments of the three option tools.
type OptionsInput struct {
	Table   string        `json:"table"`
	Column  string        `json:"column"`
	Options []OptionInput `json:"options"`
	Names   []string      `json:"names"`
}

func invokeOptionsChange(op, kind string, change func(*Client, context.Context, string, OptionsInput) error,
	done string) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		var input OptionsInput
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
		if err := checkOptionsRequest(op, kind, bound, input); err != nil {
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

// checkOptionsRequest validates the shape and the table boundary without any I/O.
func checkOptionsRequest(op, kind string, bound scope, input OptionsInput) error {
	if err := checkTableRequest(op, "options", bound, TableInput{Table: input.Table}); err != nil {
		return err
	}
	if !validName(input.Column) {
		return providerError(op, "the column is unusable")
	}
	seen := map[string]bool{}
	claim := func(name string) bool {
		if !validName(name) || seen[name] {
			return false
		}
		seen[name] = true
		return true
	}
	if kind == "delete" {
		if len(input.Names) == 0 || len(input.Names) > maxOptionChanges || len(input.Options) != 0 {
			return providerError(op, "give 1 to 50 option names")
		}
		for _, name := range input.Names {
			if !claim(name) {
				return providerError(op, "an option name is unusable or repeated")
			}
		}
		return nil
	}
	if len(input.Options) == 0 || len(input.Options) > maxOptionChanges || len(input.Names) != 0 {
		return providerError(op, "give 1 to 50 options")
	}
	for _, option := range input.Options {
		if !claim(option.Name) {
			return providerError(op, "an option name is unusable or repeated")
		}
		if (option.Color != "" && !colorPattern.MatchString(option.Color)) ||
			(option.TextColor != "" && !colorPattern.MatchString(option.TextColor)) {
			return providerError(op, "a color is written as #RRGGBB")
		}
		if kind == "add" && option.NewName != "" {
			return providerError(op, "new_name applies to an update only")
		}
		if kind == "update" && option.NewName == "" && option.Color == "" && option.TextColor == "" {
			return providerError(op, "an update option changes nothing")
		}
		if kind == "update" && option.NewName != "" && !validName(option.NewName) {
			return providerError(op, "an option name is unusable or repeated")
		}
	}
	return nil
}

// optionColumn resolves the column by name, or failing that by key, and accepts select columns only.
func optionColumn(op string, table tableJSON, reference string) (columnJSON, error) {
	for _, byKey := range []bool{false, true} {
		for _, candidate := range table.Columns {
			if (!byKey && candidate.Name == reference) || (byKey && candidate.Key == reference) {
				if candidate.Type != "single-select" && candidate.Type != "multiple-select" || !validName(candidate.Name) {
					return columnJSON{}, providerError(op, "the column is not a single-select or multiple-select column")
				}
				return candidate, nil
			}
		}
	}
	return columnJSON{}, providerError(op, "the table has no column with this name or key")
}

func (c *Client) changeOptions(ctx context.Context, op, method, kind string, input OptionsInput) error {
	if err := checkOptionsRequest(op, kind, c.scope, input); err != nil {
		return err
	}
	scoped, err := c.resolveViewTable(ctx, op, input.Table)
	if err != nil {
		return err
	}
	column, err := optionColumn(op, scoped.table, input.Column)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, option := range column.Data.Options {
		existing[option.Name] = true
	}
	payload := map[string]any{"table_name": scoped.table.Name, "column": column.Name}
	switch kind {
	case "add":
		list := make([]map[string]string, 0, len(input.Options))
		for _, option := range input.Options {
			if existing[option.Name] {
				return providerError(op, "an option with this name already exists")
			}
			list = append(list, optionBody(option, ""))
		}
		payload["options"] = list
	case "update":
		list := make([]map[string]string, 0, len(input.Options))
		for _, option := range input.Options {
			if !existing[option.Name] {
				return providerError(op, "an option to change does not exist")
			}
			if option.NewName != "" && option.NewName != option.Name && existing[option.NewName] {
				return providerError(op, "an option with the new name already exists")
			}
			list = append(list, optionBody(option, option.Name))
		}
		payload["options"] = list
	default:
		for _, name := range input.Names {
			if !existing[name] {
				return providerError(op, "an option to delete does not exist")
			}
		}
		payload["options"] = input.Names
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	c.meta = nil
	return c.changeOnce(ctx, op, method, gatewayPath+url.PathEscape(scoped.access.uuid)+optionsPath,
		scoped.access.token, body, optionUncertain)
}

func optionBody(option OptionInput, old string) map[string]string {
	body := map[string]string{"name": option.Name}
	if old != "" {
		body["old_name"] = old
		if option.NewName != "" {
			body["name"] = option.NewName
		}
	}
	if option.Color != "" {
		body["color"] = option.Color
	}
	if option.TextColor != "" {
		body["textColor"] = option.TextColor
	}
	return body
}

// AddOptions sends exactly one request that adds options to a select column.
func (c *Client) AddOptions(ctx context.Context, op string, input OptionsInput) error {
	return c.changeOptions(ctx, op, http.MethodPost, "add", input)
}

// UpdateOptions sends exactly one request that changes existing options.
func (c *Client) UpdateOptions(ctx context.Context, op string, input OptionsInput) error {
	return c.changeOptions(ctx, op, http.MethodPut, "update", input)
}

// DeleteOptions sends exactly one request that deletes options and clears the cells that hold them.
func (c *Client) DeleteOptions(ctx context.Context, op string, input OptionsInput) error {
	return c.changeOptions(ctx, op, http.MethodDelete, "delete", input)
}
