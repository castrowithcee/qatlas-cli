package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// ValidateJSON checks raw against a JSON schema and names the first violation by its path, such as
// "$.foo is not allowed". A field the schema does not allow is named before a missing required one, since
// it is often the misspelling of that field.
func ValidateJSON(schema, raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("arguments must be valid JSON")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("arguments must contain exactly one JSON value")
	}
	return validateValue(schema, value, true)
}

// validateValue checks value against rawSchema. Only input is held to the member counts of objects and
// the uniqueness of arrays; a provider response is not refused for them.
func validateValue(rawSchema json.RawMessage, value any, input bool) error {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		return fmt.Errorf("schema is invalid")
	}
	return validateAt(schema, value, "$", input)
}

func validateAt(schema map[string]json.RawMessage, value any, path string, input bool) error {
	var kind string
	if raw := schema["type"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &kind); err != nil {
			return fmt.Errorf("schema at %s has an invalid type", path)
		}
		if !isType(kind, value) {
			return fmt.Errorf("%s must be %s", path, kind)
		}
	}

	if raw := schema["enum"]; len(raw) > 0 {
		var allowed []any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&allowed) != nil {
			return fmt.Errorf("schema at %s has an invalid enum", path)
		}
		match := false
		encoded, _ := json.Marshal(value)
		for _, candidate := range allowed {
			other, _ := json.Marshal(candidate)
			if bytes.Equal(encoded, other) {
				match = true
				break
			}
		}
		if !match {
			return fmt.Errorf("%s is not an allowed value", path)
		}
	}

	switch value := value.(type) {
	case map[string]any:
		properties := map[string]json.RawMessage{}
		if raw := schema["properties"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &properties); err != nil {
				return fmt.Errorf("schema at %s has invalid properties", path)
			}
		}
		additional := true
		var extra map[string]json.RawMessage
		if raw := schema["additionalProperties"]; len(raw) > 0 {
			if json.Unmarshal(raw, &additional) != nil {
				additional = true
				// A schema for the other members is held to on input only.
				if input && json.Unmarshal(raw, &extra) != nil {
					return fmt.Errorf("schema at %s has invalid additionalProperties", path)
				}
			}
		}
		if input {
			if raw := schema["minProperties"]; len(raw) > 0 {
				var minimum int
				if json.Unmarshal(raw, &minimum) == nil && len(value) < minimum {
					if minimum == 1 {
						return fmt.Errorf("%s must not be empty", path)
					}
					return fmt.Errorf("%s must have at least %d members", path, minimum)
				}
			}
			if raw := schema["maxProperties"]; len(raw) > 0 {
				var maximum int
				if json.Unmarshal(raw, &maximum) == nil && len(value) > maximum {
					return fmt.Errorf("%s must have at most %d members", path, maximum)
				}
			}
		}
		names := make([]string, 0, len(value))
		for name := range value {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, ok := properties[name]; !ok && !additional {
				return fmt.Errorf("%s.%s is not allowed", path, name)
			}
		}
		var required []string
		if raw := schema["required"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &required); err != nil {
				return fmt.Errorf("schema at %s has invalid required fields", path)
			}
		}
		for _, name := range required {
			if _, ok := value[name]; !ok {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		for _, name := range names {
			child := value[name]
			raw, ok := properties[name]
			if !ok {
				if extra != nil {
					if err := validateAt(extra, child, path+"."+name, input); err != nil {
						return err
					}
				}
				continue
			}
			var childSchema map[string]json.RawMessage
			if err := json.Unmarshal(raw, &childSchema); err != nil {
				return fmt.Errorf("schema at %s.%s is invalid", path, name)
			}
			if err := validateAt(childSchema, child, path+"."+name, input); err != nil {
				return err
			}
		}
	case []any:
		if raw := schema["minItems"]; len(raw) > 0 {
			var minimum int
			if json.Unmarshal(raw, &minimum) == nil && len(value) < minimum {
				if minimum == 1 {
					return fmt.Errorf("%s must not be empty", path)
				}
				return fmt.Errorf("%s must have at least %d items", path, minimum)
			}
		}
		if raw := schema["maxItems"]; len(raw) > 0 {
			var maximum int
			if json.Unmarshal(raw, &maximum) == nil && len(value) > maximum {
				return fmt.Errorf("%s must have at most %d items", path, maximum)
			}
		}
		if raw := schema["uniqueItems"]; input && string(raw) == "true" {
			first := make(map[string]int, len(value))
			for i, item := range value {
				key := equalityKey(item)
				if earlier, ok := first[key]; ok {
					return fmt.Errorf("%s[%d] repeats %s[%d]", path, i, path, earlier)
				}
				first[key] = i
			}
		}
		if raw := schema["items"]; len(raw) > 0 {
			var itemSchema map[string]json.RawMessage
			if err := json.Unmarshal(raw, &itemSchema); err != nil {
				return fmt.Errorf("schema at %s has invalid items", path)
			}
			for i, item := range value {
				if err := validateAt(itemSchema, item, path+"["+strconv.Itoa(i)+"]", input); err != nil {
					return err
				}
			}
		}
	case string:
		if raw := schema["minLength"]; len(raw) > 0 {
			var minimum int
			if json.Unmarshal(raw, &minimum) == nil && utf8.RuneCountInString(value) < minimum {
				return fmt.Errorf("%s is shorter than %d characters", path, minimum)
			}
		}
		if raw := schema["maxLength"]; len(raw) > 0 {
			var maximum int
			if json.Unmarshal(raw, &maximum) == nil && utf8.RuneCountInString(value) > maximum {
				return fmt.Errorf("%s is longer than %d characters", path, maximum)
			}
		}
		if raw := schema["pattern"]; len(raw) > 0 {
			var pattern string
			if err := json.Unmarshal(raw, &pattern); err != nil {
				return fmt.Errorf("schema at %s has an invalid pattern", path)
			}
			expression, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("schema at %s has an invalid pattern", path)
			}
			if !expression.MatchString(value) {
				// A pattern is no help to read, so a schema may name the form in words under x-form. It is
				// the schema's own text, never the rejected value.
				var form string
				if raw := schema["x-form"]; len(raw) > 0 && json.Unmarshal(raw, &form) == nil && form != "" {
					return fmt.Errorf("%s does not have the required form %s", path, form)
				}
				return fmt.Errorf("%s does not have the required form", path)
			}
		}
	case json.Number:
		if raw := schema["minimum"]; len(raw) > 0 {
			minimum, _ := strconv.ParseFloat(string(raw), 64)
			number, _ := value.Float64()
			if number < minimum {
				return fmt.Errorf("%s must be at least %v", path, minimum)
			}
		}
		if raw := schema["maximum"]; len(raw) > 0 {
			maximum, _ := strconv.ParseFloat(string(raw), 64)
			number, _ := value.Float64()
			if number > maximum {
				return fmt.Errorf("%s must be at most %v", path, maximum)
			}
		}
	}
	if raw := schema["anyOf"]; input && len(raw) > 0 {
		return validateAnyOf(raw, value, path)
	}
	return nil
}

// validateAnyOf accepts value as soon as one branch does. Otherwise it names the failure the way a reader
// can act on it: the allowed types when no branch has the value's type, the one branch of its type, or the
// members of which one is required.
func validateAnyOf(raw json.RawMessage, value any, path string) error {
	var branches []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &branches); err != nil || len(branches) == 0 {
		return fmt.Errorf("schema at %s has an invalid anyOf", path)
	}
	var errs []error
	var types, requiredPaths []string
	onlyRequired, typeMatch := true, -1
	for i, branch := range branches {
		err := validateAt(branch, value, path, true)
		if err == nil {
			return nil
		}
		errs = append(errs, err)

		var names []string
		if len(branch) != 1 || json.Unmarshal(branch["required"], &names) != nil {
			onlyRequired = false
		}
		for _, name := range names {
			if !contains(requiredPaths, path+"."+name) {
				requiredPaths = append(requiredPaths, path+"."+name)
			}
		}

		var kind string
		if json.Unmarshal(branch["type"], &kind) == nil && kind != "" {
			if !contains(types, kind) {
				types = append(types, kind)
			}
			if typeMatch < 0 && isType(kind, value) {
				typeMatch = i
			}
		}
	}
	switch {
	case onlyRequired:
		return fmt.Errorf("%s is required", joinOr(requiredPaths))
	case len(types) > 0 && typeMatch < 0:
		return fmt.Errorf("%s must be %s", path, joinOr(types))
	case typeMatch >= 0:
		return errs[typeMatch]
	}
	return errs[0]
}

// joinOr lists words in English: "a or b", "a, b, or c".
func joinOr(words []string) string {
	switch len(words) {
	case 1:
		return words[0]
	case 2:
		return words[0] + " or " + words[1]
	}
	return strings.Join(words[:len(words)-1], ", ") + ", or " + words[len(words)-1]
}

// equalityKey encodes a decoded JSON value so that equal JSON values yield equal keys. Numbers are
// normalized to their exact value at every level, so 1, 1.0 and 1e0 match; a number never matches a string.
func equalityKey(value any) string {
	switch v := value.(type) {
	case json.Number:
		if r, ok := new(big.Rat).SetString(string(v)); ok {
			return "n" + r.String()
		}
		return "n" + string(v)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = equalityKey(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		names := make([]string, 0, len(v))
		for name := range v {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, len(names))
		for i, name := range names {
			parts[i] = strconv.Quote(name) + ":" + equalityKey(v[name])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	encoded, _ := json.Marshal(value)
	return "v" + string(encoded)
}

func isType(kind string, value any) bool {
	switch kind {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		n, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := n.Int64()
		return err == nil
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

// CompactDescriptor is the contract describe publishes unless the complete descriptor is asked for:
// everything invoking a tool needs, with both schemas condensed into tables instead of being repeated
// beside them. Arguments has one row per argument, derived from the input schema, and Fields names every
// top-level field of the result, derived from the output schema; the fields of a result that is an array
// are those of its entries. The members of the risk stand beside the others rather than in an object of
// their own, and all are ordered by meaning: the identifier, its descriptions, the risk, then the tables.
//
// SelectableFields names the members of the result list entries that invoke can select with fields; it is
// left out for a tool without a result list.
//
// The provider is the prefix of the ID, the tags serve the search that found the tool, and whether a tool
// needs a tools list that names it only decides which connections offer it, which the connections beside
// the contract answer; these, the types of the result fields, and both schemas stay in the complete
// descriptor. Arguments are always validated against the complete input schema, not against this view.
type CompactDescriptor struct {
	ID               string                  `json:"id"`
	Version          int                     `json:"version"`
	Title            string                  `json:"title"`
	Description      string                  `json:"description"`
	Effect           capability.Effect       `json:"effect"`
	Idempotency      capability.Idempotency  `json:"idempotency"`
	Confirmation     capability.Confirmation `json:"confirmation"`
	OpenWorld        bool                    `json:"open_world"`
	DataSensitivity  string                  `json:"data_sensitivity"`
	Arguments        []ArgumentRow           `json:"arguments"`
	Fields           []capability.Field      `json:"fields"`
	SelectableFields []string                `json:"selectable_fields,omitempty"`
	Examples         []capability.Example    `json:"examples"`
}

// ArgumentRow is one argument of a compact contract, derived from the input schema. A member of an object
// argument has a row of its own, named with a dot, such as address.city, and inside an array with [], such
// as line_items[].name; Required then means required within that object. Type is the schema type, with []
// for an array of that type. Form lists the allowed values separated by |, or names the written form a
// pattern checks, and is empty otherwise. Limits names the bounds: a range such as 1..100 for a number,
// with len for the characters of a string, items for the entries of an array, and keys for the members of
// an object, and each for the bounds of every entry.
type ArgumentRow struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Form        string `json:"form"`
	Limits      string `json:"limits"`
	Description string `json:"description"`
}

// Compact condenses a descriptor into its compact contract. The rows follow the order of the descriptor's
// Arguments and Fields, which list the important names first; members the schemas declare beyond them
// follow in name order, and the members of an object argument list its required ones first.
func Compact(d capability.Descriptor) CompactDescriptor {
	argumentText := map[string]string{}
	var argumentOrder []string
	for _, argument := range d.Arguments {
		argumentText[argument.Name] = argument.Description
		argumentOrder = append(argumentOrder, argument.Name)
	}
	arguments := []ArgumentRow{}
	input := parseSchema(d.InputSchema)
	for _, name := range memberOrder(input, argumentOrder) {
		arguments = input.argumentRows(arguments, name, name, argumentText[name])
	}

	// The descriptor's fields keep their descriptions and come first; the other top-level fields the output
	// schema declares follow with the schema's own description, which is usually none.
	fields := append([]capability.Field{}, d.Fields...)
	listed := map[string]bool{}
	for _, field := range d.Fields {
		listed[field.Name] = true
	}
	result := parseSchema(d.OutputSchema)
	if result.Items != nil {
		result = *result.Items
	}
	for _, name := range memberOrder(result, nil) {
		if !listed[name] {
			fields = append(fields, capability.Field{Name: name, Description: result.Properties[name].Description})
		}
	}

	return CompactDescriptor{
		ID: d.ID, Version: d.Version, Title: d.Title, Description: d.Description,
		Effect: d.Risk.Effect, Idempotency: d.Risk.Idempotency, Confirmation: d.Risk.Confirmation,
		OpenWorld: d.Risk.OpenWorld, DataSensitivity: d.Risk.DataSensitivity,
		Arguments: arguments, Fields: fields,
		SelectableFields: SelectableFields(d),
		Examples:         append([]capability.Example{}, d.Examples...),
	}
}

// requiredArguments names the required top-level arguments of d as the compact contract lists them, each
// followed by :form where it has one. Members of an object argument are not listed: they are
// required only within that object.
func requiredArguments(d capability.Descriptor) []string {
	var required []string
	for _, row := range Compact(d).Arguments {
		if !row.Required || strings.ContainsAny(row.Name, ".[") {
			continue
		}
		if row.Form != "" {
			required = append(required, row.Name+":"+row.Form)
		} else {
			required = append(required, row.Name)
		}
	}
	return required
}

// schemaNode is the part of a JSON schema the compact contract reads. A bound that is absent stays empty.
type schemaNode struct {
	Type          string                 `json:"type"`
	AnyOf         []*schemaNode          `json:"anyOf"`
	Description   string                 `json:"description"`
	Enum          []json.RawMessage      `json:"enum"`
	Form          string                 `json:"x-form"`
	Properties    map[string]*schemaNode `json:"properties"`
	Required      []string               `json:"required"`
	Items         *schemaNode            `json:"items"`
	Minimum       json.Number            `json:"minimum"`
	Maximum       json.Number            `json:"maximum"`
	MinLength     json.Number            `json:"minLength"`
	MaxLength     json.Number            `json:"maxLength"`
	MinItems      json.Number            `json:"minItems"`
	MaxItems      json.Number            `json:"maxItems"`
	MinProperties json.Number            `json:"minProperties"`
	MaxProperties json.Number            `json:"maxProperties"`
	UniqueItems   bool                   `json:"uniqueItems"`
}

// parseSchema reads a schema for the compact contract. A schema it cannot read yields no rows; describe
// still answers, and the complete descriptor shows the schema as it is.
func parseSchema(raw json.RawMessage) schemaNode {
	var node schemaNode
	_ = json.Unmarshal(raw, &node)
	return node
}

// memberOrder lists the members of an object schema: the names of first that it declares, in that order,
// then the others by name.
func memberOrder(node schemaNode, first []string) []string {
	names := make([]string, 0, len(node.Properties))
	listed := map[string]bool{}
	for _, name := range first {
		if _, ok := node.Properties[name]; ok && !listed[name] {
			names = append(names, name)
			listed[name] = true
		}
	}
	rest := make([]string, 0, len(node.Properties))
	for name := range node.Properties {
		if !listed[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(names, rest...)
}

// members returns the object schema whose members a row of node expands into, with the name prefix of
// those rows, or nil when node is neither an object nor an array of objects that declares members.
func (n schemaNode) members(name string) (*schemaNode, string) {
	switch {
	case len(n.Properties) > 0:
		return &n, name + "."
	case n.Items != nil && len(n.Items.Properties) > 0:
		return n.Items, name + "[]."
	}
	return nil, ""
}

// argumentRows appends the row of the member key of n under the name path, then the rows of its own
// members.
func (n schemaNode) argumentRows(rows []ArgumentRow, key, path, description string) []ArgumentRow {
	node := n.Properties[key]
	if node == nil {
		return rows
	}
	if description == "" {
		description = node.Description
	}
	rows = append(rows, ArgumentRow{
		Name: path, Type: node.typeName(), Required: contains(n.Required, key), Form: node.form(),
		Limits: node.limits(), Description: description,
	})
	if object, prefix := node.members(path); object != nil {
		for _, name := range memberOrder(*object, object.Required) {
			rows = object.argumentRows(rows, name, prefix+name, "")
		}
	}
	return rows
}

// typeName is the schema type, with [] for an array of that type, the types of the branches separated by |
// for a union, and any where the schema names none.
func (n schemaNode) typeName() string {
	if n.Type == "array" && n.Items != nil {
		item := n.Items.typeName()
		if strings.Contains(item, "|") {
			item = "(" + item + ")"
		}
		return item + "[]"
	}
	if n.Type == "" && len(n.AnyOf) > 0 {
		var names []string
		for _, branch := range n.AnyOf {
			if branch == nil || branch.typeName() == "any" {
				return "any"
			}
			if name := branch.typeName(); !contains(names, name) {
				names = append(names, name)
			}
		}
		return strings.Join(names, "|")
	}
	if n.Type == "" {
		return "any"
	}
	return n.Type
}

// form lists the allowed values of a value, or of every entry of an array, or names its written form.
func (n schemaNode) form() string {
	if n.Type == "array" && n.Items != nil && len(n.Enum) == 0 && n.Form == "" {
		return n.Items.form()
	}
	if len(n.Enum) > 0 {
		values := make([]string, len(n.Enum))
		for i, raw := range n.Enum {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				text = string(raw)
			}
			values[i] = text
		}
		return strings.Join(values, "|")
	}
	return n.Form
}

// limits names the bounds of a value; see ArgumentRow.
func (n schemaNode) limits() string {
	var parts []string
	add := func(label string, minimum, maximum json.Number) {
		var bound string
		switch {
		case minimum != "" && minimum == maximum:
			bound = string(minimum)
		case minimum != "" && maximum != "":
			bound = string(minimum) + ".." + string(maximum)
		case minimum != "":
			bound = "min " + string(minimum)
		case maximum != "":
			bound = "max " + string(maximum)
		default:
			return
		}
		if label != "" {
			bound = label + " " + bound
		}
		parts = append(parts, bound)
	}
	add("", n.Minimum, n.Maximum)
	add("len", n.MinLength, n.MaxLength)
	add("items", n.MinItems, n.MaxItems)
	add("keys", n.MinProperties, n.MaxProperties)
	if n.UniqueItems {
		parts = append(parts, "unique")
	}
	if n.Type == "array" && n.Items != nil && len(n.Items.Properties) == 0 {
		if each := n.Items.limits(); each != "" {
			parts = append(parts, "each "+each)
		}
	}
	return strings.Join(parts, "; ")
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
