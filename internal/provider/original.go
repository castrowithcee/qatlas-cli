package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// ErrorBodyLimit is the most bytes of a failed answer that are read to build an Original.
const ErrorBodyLimit = 4096

// OriginalFields maps the JSON of one provider's error answer to an Original. Each value is a dotted path
// to a scalar (string or number) below the object it applies to; an empty path means the provider has no
// such field. Message, Code, Type, and Path make up one entry; Documentation is the link of the whole answer.
// List names an array below the object. Without Item, every string in it is one entry's Message. With Item,
// every object in it is one entry, mapped by Item (its own List, Fields, and Documentation are ignored).
// Fields names an object below the object whose keys are field names, such as validation errors per field.
// Each value is a string, a list of strings, or a list of objects (mapped by Item; skipped without Item);
// every such element is one entry whose Path is the key unless Item sets its own. Keys are visited sorted;
// a Fields path that is not an object yields nothing, and nested values are ignored.
type OriginalFields struct {
	Message       string
	Code          string
	Type          string
	Path          string
	Documentation string
	List          string
	Fields        string
	Item          *OriginalFields
}

// ReadErrorBody reads at most ErrorBodyLimit bytes of a failed answer. A read error keeps what was read, so
// the rest of the answer never decides the outcome. A nil reader is an empty body.
func ReadErrorBody(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(r, ErrorBodyLimit))
	return data
}

// ErrorOriginal builds the Original of a failed answer from its status and body (see ReadErrorBody). A body
// that is not JSON is kept as its trimmed text, which covers a proxy's error page. A JSON body keeps only
// what fields names: if that is nothing, or the JSON is not an object, nothing of the body is kept, because
// unnamed content may be nested objects or an echo of the request. An empty body leaves only the status.
func ErrorOriginal(status int, body []byte, fields OriginalFields) *Original {
	original := &Original{Status: status}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return original
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var answer any
	if err := decoder.Decode(&answer); err != nil {
		text := strings.TrimSpace(strings.ToValidUTF8(string(trimmed), ""))
		if text != "" {
			original.Entries = []OriginalEntry{{Message: text}}
		}
		return original
	}
	object, ok := answer.(map[string]any)
	if !ok {
		return original
	}
	original.Documentation = scalarAt(object, fields.Documentation)
	if entry := entryOf(object, fields); entry != (OriginalEntry{}) {
		original.Entries = append(original.Entries, entry)
	}
	if list, ok := valueAt(object, fields.List).([]any); ok {
		for _, element := range list {
			var entry OriginalEntry
			switch {
			case fields.Item == nil:
				if text, ok := element.(string); ok {
					entry.Message = strings.TrimSpace(text)
				}
			default:
				if item, ok := element.(map[string]any); ok {
					entry = entryOf(item, *fields.Item)
				}
			}
			if entry != (OriginalEntry{}) {
				original.Entries = append(original.Entries, entry)
			}
		}
	}
	original.Entries = append(original.Entries, keyedEntries(valueAt(object, fields.Fields), fields.Item)...)
	return original
}

// keyedEntries maps an object of field name to string, string list, or object list to entries.
func keyedEntries(value any, item *OriginalFields) []OriginalEntry {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var entries []OriginalEntry
	add := func(entry OriginalEntry, key string) {
		if entry == (OriginalEntry{}) {
			return
		}
		if entry.Path == "" {
			entry.Path = key
		}
		entries = append(entries, entry)
	}
	for _, key := range keys {
		elements, isList := object[key].([]any)
		if !isList {
			elements = []any{object[key]}
		}
		for _, element := range elements {
			switch typed := element.(type) {
			case string:
				add(OriginalEntry{Message: strings.TrimSpace(typed)}, key)
			case map[string]any:
				if item != nil {
					add(entryOf(typed, *item), key)
				}
			}
		}
	}
	return entries
}

// ClassifyStatusBody is ClassifyStatus plus the Original of the answer, read from body. Class and message
// are exactly those of ClassifyStatus: the body never reaches them or Error().
func ClassifyStatusBody(op string, status int, texts StatusTexts, body io.Reader, fields OriginalFields) *Error {
	failure := ClassifyStatus(op, status, texts)
	failure.Original = ErrorOriginal(status, ReadErrorBody(body), fields)
	return failure
}

func entryOf(object map[string]any, fields OriginalFields) OriginalEntry {
	return OriginalEntry{
		Type:    scalarAt(object, fields.Type),
		Code:    scalarAt(object, fields.Code),
		Path:    scalarAt(object, fields.Path),
		Message: scalarAt(object, fields.Message),
	}
}

func valueAt(object map[string]any, path string) any {
	if path == "" {
		return nil
	}
	var current any = object
	for _, key := range strings.Split(path, ".") {
		next, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = next[key]
	}
	return current
}

// scalarAt returns a string or number at path as trimmed text; any other value, such as an object, an array,
// or a boolean, yields empty.
func scalarAt(object map[string]any, path string) string {
	switch value := valueAt(object, path).(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	}
	return ""
}
