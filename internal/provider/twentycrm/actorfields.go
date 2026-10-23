package twentycrm

import (
	"encoding/json"
	"regexp"
)

const (
	actorSourceMax = 64
	numberTextMax  = 64
)

// settingPattern is the shape of a Twenty select value such as a color scheme or a date format.
var settingPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// actorObject reads the createdBy or updatedBy composite of a Twenty record under the Qatlas names of
// actorKeys. The name is text a person typed, so control characters are dropped and its length is capped;
// an identifier that is not a UUID is left out. A context beyond the size limits is left out and marked by
// context_omitted, never shortened.
func actorObject(raw json.RawMessage) map[string]any {
	decoded, err := decodeValue(raw)
	object, ok := decoded.(map[string]any)
	if err != nil || !ok {
		return nil
	}
	result := map[string]any{}
	if text, ok := object["source"].(string); ok {
		if cleaned := cleanText(&text, actorSourceMax); cleaned != "" {
			result[actorKeys["source"]] = cleaned
		}
	}
	if text, ok := object["workspaceMemberId"].(string); ok && validUUID(text) {
		result[actorKeys["workspaceMemberId"]] = text
	}
	if text, ok := object["name"].(string); ok {
		if cleaned := cleanText(&text, memberNameMax); cleaned != "" {
			result[actorKeys["name"]] = cleaned
		}
	}
	if context, present := object["context"]; present && !emptyValue(context) {
		if checkValue(context, 1) != nil {
			result["context_omitted"] = true
		} else {
			result[actorKeys["context"]] = context
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// plainNumber returns a JSON number exactly as Twenty sent it, without a floating-point detour; any other
// value yields nothing.
func plainNumber(raw json.RawMessage) json.Number {
	decoded, err := decodeValue(raw)
	number, ok := decoded.(json.Number)
	if err != nil || !ok || len(number) > numberTextMax {
		return ""
	}
	return number
}
