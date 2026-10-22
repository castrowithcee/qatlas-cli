package seatable

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds of one free column member. A member beyond any of them is left out and named in omitted_fields.
const (
	maxColumnMemberBytes    = 4096
	maxColumnMemberDepth    = 8
	maxColumnMemberElements = 1000
)

// columnMember names the column members the metadata reports beyond key, name, and type.
const (
	memberWidth          = "width"
	memberEditable       = "editable"
	memberResizable      = "resizable"
	memberDraggable      = "draggable"
	memberData           = "data"
	memberPermissionType = "permission_type"
	memberPermittedUsers = "permitted_users"
)

// columnExtras holds the optional members of one column as the output carries them.
type columnExtras struct {
	Width          json.RawMessage `json:"width,omitempty"`
	Editable       *bool           `json:"editable,omitempty"`
	Resizable      *bool           `json:"resizable,omitempty"`
	Draggable      *bool           `json:"draggable,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	PermissionType string          `json:"permission_type,omitempty"`
	PermittedUsers []string        `json:"permitted_users,omitempty"`
	OmittedFields  []string        `json:"omitted_fields,omitempty"`
}

// UnmarshalJSON keeps the raw data object next to the typed members the provider reads from it.
func (c *columnJSON) UnmarshalJSON(raw []byte) error {
	type plain columnJSON
	var typed plain
	if err := json.Unmarshal(raw, &typed); err != nil {
		return err
	}
	var members struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &members); err != nil {
		return err
	}
	*c = columnJSON(typed)
	c.RawData = members.Data
	return nil
}

// columnExtrasOf projects the optional members of a column. Missing and empty members stay out, a member of
// an unexpected shape or beyond the bounds is left out and named. The data object of a link column whose
// joined table the connection may not read loses every member that names that table, its views, or its columns.
func (c *Client) columnExtrasOf(document metadataJSON, current tableJSON, column columnJSON) columnExtras {
	var out columnExtras
	omit := func(name string) { out.OmittedFields = append(out.OmittedFields, name) }

	if present(column.Width) {
		if isNumber(column.Width) {
			out.Width = append(json.RawMessage(nil), bytes.TrimSpace(column.Width)...)
		} else {
			omit(memberWidth)
		}
	}
	for _, flag := range []struct {
		raw  json.RawMessage
		into **bool
		name string
	}{
		{column.Editable, &out.Editable, memberEditable},
		{column.Resizable, &out.Resizable, memberResizable},
		{column.Draggable, &out.Draggable, memberDraggable},
	} {
		if !present(flag.raw) {
			continue
		}
		var value bool
		if json.Unmarshal(flag.raw, &value) != nil {
			omit(flag.name)
			continue
		}
		*flag.into = &value
	}
	if present(column.RawData) {
		switch data, ok := c.columnData(document, current, column); {
		case !ok:
			omit(memberData)
		case data != nil:
			out.Data = data
		}
	}
	if present(column.PermissionType) {
		var text string
		if json.Unmarshal(column.PermissionType, &text) != nil || exceedsBounds(column.PermissionType) {
			omit(memberPermissionType)
		} else {
			out.PermissionType = cleanColumnText(text, true)
		}
	}
	if present(column.PermittedUsers) {
		var users []string
		if json.Unmarshal(column.PermittedUsers, &users) != nil || exceedsBounds(column.PermittedUsers) {
			omit(memberPermittedUsers)
		} else {
			for _, user := range users {
				if user = cleanColumnText(user, true); user != "" {
					out.PermittedUsers = append(out.PermittedUsers, user)
				}
			}
		}
	}
	return out
}

func present(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// isNumber reports whether raw is one JSON number.
func isNumber(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return false
	}
	var number json.Number
	return json.Unmarshal(trimmed, &number) == nil
}

// decodeBounded reads a JSON value without turning numbers into floats. It reports false for a value that
// is larger than 4096 bytes, deeper than 8 levels, holds more than 1000 elements, or cannot be read.
func decodeBounded(raw json.RawMessage) (any, bool) {
	if len(raw) > maxColumnMemberBytes {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	elements := 0
	var walk func(any, int) bool
	walk = func(v any, depth int) bool {
		elements++
		if depth > maxColumnMemberDepth || elements > maxColumnMemberElements {
			return false
		}
		switch typed := v.(type) {
		case map[string]any:
			for _, item := range typed {
				if !walk(item, depth+1) {
					return false
				}
			}
		case []any:
			for _, item := range typed {
				if !walk(item, depth+1) {
					return false
				}
			}
		}
		return true
	}
	return value, walk(value, 1)
}

func exceedsBounds(raw json.RawMessage) bool {
	_, ok := decodeBounded(raw)
	return !ok
}

// cleanTree replaces control characters in every string and key of a decoded value.
func cleanTree(v any) any {
	switch typed := v.(type) {
	case string:
		return cleanColumnText(typed, false)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[cleanColumnText(key, false)] = cleanTree(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cleanTree(item)
		}
		return out
	}
	return v
}

// columnData returns the bounded, cleaned data object of a column. The second result is false when the
// member must be left out and named; a nil object means nothing is left to report.
func (c *Client) columnData(document metadataJSON, current tableJSON, column columnJSON) (json.RawMessage, bool) {
	tree, ok := decodeBounded(column.RawData)
	if !ok {
		return nil, false
	}
	data, isObject := tree.(map[string]any)
	if !isObject {
		return nil, false
	}
	if !c.joinedTableReadable(document, current, column, data) {
		for key := range data {
			if namesJoinedTable(column.Type, key) {
				delete(data, key)
			}
		}
	}
	if len(data) == 0 {
		return nil, true
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(cleanTree(data)) != nil {
		return nil, false
	}
	return json.RawMessage(bytes.TrimSpace(buffer.Bytes())), true
}

// joinedTableReadable reports whether the data of a column may name the table it joins. Only link and
// link-formula columns join a table; a wildcard connection reads every table. A joined table that cannot be
// determined counts as not readable.
func (c *Client) joinedTableReadable(document metadataJSON, current tableJSON, column columnJSON,
	data map[string]any) bool {
	if column.Type != "link" && column.Type != "link-formula" {
		return true
	}
	if c.scope.wildcard {
		return true
	}
	link := column
	if column.Type == "link-formula" {
		key, _ := data["link_column_key"].(string)
		found := false
		for _, candidate := range current.Columns {
			if candidate.Key == key && key != "" && candidate.Type == "link" {
				link, found = candidate, true
				break
			}
		}
		if !found {
			return false
		}
	}
	other, ok := counterpart(current, link)
	return ok && c.tableAllowed(document, other)
}

// namesJoinedTable reports whether a data key of a link or link-formula column names the joined table, one of
// its views, or one of its columns.
func namesJoinedTable(columnType, key string) bool {
	key = strings.ToLower(key)
	switch columnType {
	case "link":
		switch key {
		case "table_id", "other_table_id", "link_id", "other_view_id", "display_column_key":
			return true
		}
	case "link-formula":
		if key == "display_column_key" {
			return true
		}
		for _, part := range []string{"table", "view", "link_id", "linked_column"} {
			if strings.Contains(key, part) {
				return true
			}
		}
	}
	return false
}

// cleanColumnText replaces control characters and invalid UTF-8 by blanks and caps the text.
func cleanColumnText(value string, trim bool) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || r == utf8.RuneError {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(value, " "))
	if trim {
		value = strings.TrimSpace(value)
	}
	if len(value) <= maxViewText {
		return value
	}
	cut := maxViewText
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
