package makeapi

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// maxMaskedPointers bounds the masked list a blueprint answer names; the masking itself is never bounded.
const maxMaskedPointers = 500

// credentialHeaders are the header names, lowercase, whose value a module's mapper carries as a credential.
var credentialHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true, "x-api-key": true, "x-auth-token": true,
}

// walkCredentials visits every credential slot of a decoded blueprint in a deterministic order: a module's
// mapper.authPass and the string value of its credential headers, wherever a module object nests (flow,
// onerror, router routes). The slots are a fixed list on purpose; nothing is guessed from a field's content.
// pointer is the RFC 6901 pointer of the slot, and set replaces its value.
func walkCredentials(value any, pointer string, visit func(pointer, current string, set func(string))) {
	switch v := value.(type) {
	case []any:
		for i, child := range v {
			walkCredentials(child, pointer+"/"+strconv.Itoa(i), visit)
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := pointer + "/" + escapePointer(key)
			if mapper, ok := v[key].(map[string]any); ok && key == "mapper" {
				visitMapper(mapper, child, visit)
			}
			walkCredentials(v[key], child, visit)
		}
	}
}

func visitMapper(mapper map[string]any, pointer string, visit func(pointer, current string, set func(string))) {
	if pass, ok := mapper["authPass"].(string); ok {
		visit(pointer+"/authPass", pass, func(s string) { mapper["authPass"] = s })
	}
	headers, _ := mapper["headers"].([]any)
	for i, item := range headers {
		header, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, ok := header["name"].(string)
		if !ok || !credentialHeaders[strings.ToLower(name)] {
			continue
		}
		if current, ok := header["value"].(string); ok {
			visit(pointer+"/headers/"+strconv.Itoa(i)+"/value", current, func(s string) { header["value"] = s })
		}
	}
}

func escapePointer(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func decodeBlueprint(raw json.RawMessage) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// maskBlueprint replaces every non-empty credential slot with redactedMarker and returns the re-encoded
// blueprint with the first maxMaskedPointers masked pointers. A blueprint without a credential slot is
// returned byte for byte. Numbers stay exact through json.Number.
func maskBlueprint(raw json.RawMessage) (out json.RawMessage, masked []string, truncated bool, err error) {
	depth, err := jsonDepth(raw)
	if err != nil || depth > maxBlueprintDepth {
		return nil, nil, false, invalidResponse("get blueprint", "Make reported a blueprint nested deeper than this provider reads")
	}
	value, err := decodeBlueprint(raw)
	if err != nil {
		return nil, nil, false, invalidResponse("get blueprint", "Make reported a blueprint that is not a JSON object")
	}
	count := 0
	walkCredentials(value, "", func(pointer, current string, set func(string)) {
		if current == "" {
			return
		}
		set(redactedMarker)
		count++
		if len(masked) < maxMaskedPointers {
			masked = append(masked, pointer)
		} else {
			truncated = true
		}
	})
	if count == 0 {
		return raw, nil, false, nil
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, nil, false, invalidResponse("get blueprint", "the blueprint could not be encoded")
	}
	return bytes.TrimSpace(buffer.Bytes()), masked, truncated, nil
}

// maskedBlueprintPointer returns the pointer of the first credential slot of a blueprint written by a
// caller that already holds the masking marker, or "" when there is none. Writing it would replace a stored
// credential with the marker.
func maskedBlueprintPointer(raw json.RawMessage) string {
	value, err := decodeBlueprint(raw)
	if err != nil {
		return ""
	}
	first := ""
	walkCredentials(value, "", func(pointer, current string, _ func(string)) {
		if first == "" && current == redactedMarker {
			first = pointer
		}
	})
	return first
}

func refuseMaskedBlueprint(raw json.RawMessage) error {
	if pointer := maskedBlueprintPointer(raw); pointer != "" {
		return invalidRequest("blueprint holds the redaction marker at " + pointer +
			"; scenarios.blueprint masks credentials, so write the real value or remove the field")
	}
	return nil
}
