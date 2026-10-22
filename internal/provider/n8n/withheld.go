package n8n

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// withheldPlaceholder replaces every node parameter value maskParameters withholds. workflows.update
// restores it from the workflow it reads before its PUT; workflows.create refuses it.
const withheldPlaceholder = "__qatlas_withheld__"

// maxWithheldPointers bounds withheld_parameters; a node with more hits reports no parameters at all.
const maxWithheldPointers = 100

// Node types whose top-level "path" alone forms the URL that triggers a workflow without authentication
// (isFullPath in n8n's own node descriptions); the form trigger also reads options.path.
const (
	webhookNodeType     = "n8n-nodes-base.webhook"
	formTriggerNodeType = "n8n-nodes-base.formTrigger"
	mcpTriggerNodeType  = "@n8n/n8n-nodes-langchain.mcpTrigger"
)

var webhookPathTypes = map[string]bool{webhookNodeType: true, formTriggerNodeType: true, mcpTriggerNodeType: true}

// credentialKeys are the names of every node parameter n8n's own node descriptions mark as a password
// field (typeOptions.password). The list is fixed on purpose: no node type description is ever fetched.
var credentialKeys = map[string]bool{
	"password": true, "user_password": true, "http_password": true, "KeystorePassword": true, "token": true,
	"apiToken": true, "apiKeyId": true, "apiKeySecret": true, "secret": true, "privateKey": true,
	"pageToken": true, "temporaryPasswordOptions": true,
}

// headerTextKeys are always withheld when set: a header carries credentials far more often than not.
var headerTextKeys = map[string]bool{"jsonHeaders": true, "headerParametersJson": true}

// jsonTextKeys are withheld when their text names a credential-like key.
var jsonTextKeys = map[string]bool{
	"jsonQuery": true, "queryParametersJson": true, "jsonBody": true, "bodyParametersJson": true, "body": true,
}

var (
	credentialName    = regexp.MustCompile(`(?i)key|token|secret|passw|pwd|auth|signature|session|cookie|credential`)
	credentialJSONKey = regexp.MustCompile(`(?i)["']?[A-Za-z0-9_-]*(key|token|secret|passw|pwd|auth|signature|session|cookie|credential)[A-Za-z0-9_-]*["']?\s*[:=]`)
	// urlUserinfo matches any userinfo after a scheme, and user:pass@ at the very start of a scheme-less
	// value or expression.
	urlUserinfo = regexp.MustCompile(`://[^/?#\s]*@|^=?[^/?#\s@:]+:[^/?#\s@]*@`)
)

// maskParameters is the one gate every output of a node's raw parameters passes. It replaces each value the
// fixed rules withhold by withheldPlaceholder and lists them as RFC 6901 pointers relative to parameters.
// Parameters that cannot be inspected, or that would list more than maxWithheldPointers, are withheld whole.
func maskParameters(nodeType string, raw json.RawMessage) (json.RawMessage, []string, bool) {
	if !hasParameters(raw) {
		return raw, nil, false
	}
	value, err := decodeParameters(raw)
	if err != nil {
		return nil, nil, true
	}
	pointers := withholdValues(nodeType, value)
	switch {
	case len(pointers) == 0:
		return raw, nil, false
	case len(pointers) > maxWithheldPointers:
		return nil, nil, true
	}
	encoded, err := encodeParameters(value)
	if err != nil {
		return nil, nil, true
	}
	return encoded, pointers, false
}

// withholdValues replaces, in place, every value of a decoded parameters tree the fixed rules withhold, and
// returns their pointers in a stable order.
func withholdValues(nodeType string, value any) []string {
	w := withholder{nodeType: nodeType}
	w.walk(value, "", false)
	return w.pointers
}

type withholder struct {
	nodeType string
	pointers []string
}

func (w *withholder) walk(value any, pointer string, inHeader bool) {
	switch v := value.(type) {
	case map[string]any:
		// An n8n name/value pair: its value is withheld under any header key, or when its name looks like a
		// credential.
		name, isPair := v["name"].(string)
		isPair = isPair && (inHeader || credentialName.MatchString(name))
		for _, key := range sortedKeys(v) {
			child := v[key]
			childPointer := pointer + "/" + escapePointer(key)
			if withholdable(child) && ((isPair && key == "value") || w.withholds(pointer, key, child)) {
				v[key] = withheldPlaceholder
				w.pointers = append(w.pointers, childPointer)
				continue
			}
			w.walk(child, childPointer, inHeader || strings.Contains(strings.ToLower(key), "header"))
		}
	case []any:
		for i, item := range v {
			w.walk(item, pointer+"/"+strconv.Itoa(i), inHeader)
		}
	}
}

// withholds decides one key of the object at parent by the fixed rules, independent of its ancestors.
func (w *withholder) withholds(parent, key string, value any) bool {
	text, isText := value.(string)
	switch {
	case key == "path" && parent == "" && webhookPathTypes[w.nodeType]:
		return true
	case key == "path" && parent == "/options" && w.nodeType == formTriggerNodeType:
		return true
	case credentialKeys[key], headerTextKeys[key]:
		return true
	case jsonTextKeys[key]:
		return isText && credentialJSONKey.MatchString(text)
	case key == "url":
		return isText && urlCarriesCredential(text)
	}
	return false
}

// urlCarriesCredential reports a URL with userinfo or with a query parameter whose name looks like a
// credential. The value may be an n8n expression, so it is scanned as text, not parsed as a URL.
func urlCarriesCredential(text string) bool {
	if urlUserinfo.MatchString(text) {
		return true
	}
	_, query, found := strings.Cut(text, "?")
	if !found {
		return false
	}
	query, _, _ = strings.Cut(query, "#")
	for _, part := range strings.FieldsFunc(query, func(r rune) bool { return r == '&' || r == ';' }) {
		name, _, _ := strings.Cut(part, "=")
		if unescaped, err := url.QueryUnescape(name); err == nil {
			name = unescaped
		}
		if credentialName.MatchString(name) {
			return true
		}
	}
	return false
}

// withholdable keeps an absent or empty value visible: there is nothing to withhold in it.
func withholdable(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	}
	return true
}

// refuseWithheldPlaceholders refuses parameters that still carry a placeholder, for workflows.create,
// where no earlier read exists to restore it from.
func refuseWithheldPlaceholders(nodes []nodeInput) error {
	for i, n := range nodes {
		if n.ParametersWithheld {
			return invalidRequest(fmt.Sprintf("nodes[%d] sets parameters_withheld, which only workflows.update "+
				"can keep for a node of the existing workflow", i))
		}
		if !hasParameters(n.Parameters) {
			continue
		}
		value, err := decodeParameters(n.Parameters)
		if err != nil {
			return invalidRequest(fmt.Sprintf("nodes[%d].parameters could not be read", i))
		}
		if _, err := rewriteStrings(value, "", func(pointer, text string) (any, error) {
			if strings.Contains(text, withheldPlaceholder) {
				return nil, invalidRequest(fmt.Sprintf("nodes[%d].parameters at %q holds %s, which a new "+
					"workflow has no value for; set the real value instead", i, pointer, withheldPlaceholder))
			}
			return text, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// restoreWithheld mirrors maskParameters for workflows.update, from the workflow read just before the PUT:
// a node with the same id and type as in that read gets back its webhookId, every placeholder at a position
// the read withheld, and an omitted webhook path. Anything else carrying a placeholder is refused, so no
// value is ever sent to n8n that the caller did not see or set.
func restoreWithheld(nodes []nodeInput, read []nodeJSON) error {
	readByID := make(map[string]*nodeJSON, len(read))
	duplicate := map[string]bool{}
	for i := range read {
		if _, seen := readByID[read[i].ID]; seen {
			duplicate[read[i].ID] = true
		}
		readByID[read[i].ID] = &read[i]
	}
	for i := range nodes {
		n := &nodes[i]
		match := readByID[n.ID]
		if match == nil || duplicate[n.ID] || match.Type != n.Type {
			match = nil
		}
		if match != nil {
			n.WebhookID = match.WebhookID
		}
		if n.ParametersWithheld {
			switch {
			case match == nil:
				return invalidRequest(fmt.Sprintf("nodes[%d] sets parameters_withheld, which only a node with "+
					"the same id and type as in the current workflow can keep", i))
			case hasParameters(n.Parameters):
				return invalidRequest(fmt.Sprintf("nodes[%d] sets both parameters and parameters_withheld; "+
					"send only one of them", i))
			}
			n.Parameters = match.Parameters
			continue
		}
		restored, err := restoreParameters(i, n.Type, n.Parameters, match)
		if err != nil {
			return err
		}
		n.Parameters = restored
	}
	return nil
}

func restoreParameters(index int, nodeType string, raw json.RawMessage, match *nodeJSON) (json.RawMessage, error) {
	unreadable := invalidRequest(fmt.Sprintf("nodes[%d].parameters could not be read", index))
	var value, original any
	var err error
	if hasParameters(raw) {
		if value, err = decodeParameters(raw); err != nil {
			return nil, unreadable
		}
	}
	withheld := map[string]bool{}
	if match != nil && hasParameters(match.Parameters) {
		original, _ = decodeParameters(match.Parameters)
		masked, _ := decodeParameters(match.Parameters)
		for _, pointer := range withholdValues(match.Type, masked) {
			withheld[pointer] = true
		}
	}
	changed := false
	var restored []string
	value, err = rewriteStrings(value, "", func(pointer, text string) (any, error) {
		if !strings.Contains(text, withheldPlaceholder) {
			return text, nil
		}
		previous, found := valueAt(original, pointer)
		if text == withheldPlaceholder && withheld[pointer] {
			changed = true
			restored = append(restored, pointer)
			return previous, nil
		}
		if same, ok := previous.(string); found && ok && same == text {
			return text, nil
		}
		return nil, invalidRequest(fmt.Sprintf("nodes[%d].parameters at %q holds %s, which cannot be restored: "+
			"only a node with the same id and type as in the current workflow gets back the value withheld "+
			"at the same position", index, pointer, withheldPlaceholder))
	})
	if err != nil {
		return nil, err
	}
	if match != nil && webhookPathTypes[nodeType] && original != nil {
		if value, err = restoreWebhookPath(nodeType, value, original, &changed); err != nil {
			return nil, err
		}
	}
	if !changed {
		return raw, nil
	}
	encoded, err := encodeParameters(value)
	if err != nil {
		return nil, unreadable
	}
	// A restored value must stay withheld in what n8n will report back: a renamed or reordered name/value
	// pair would otherwise turn a withheld value into a visible one.
	if len(restored) > 0 {
		check, err := decodeParameters(encoded)
		if err != nil {
			return nil, unreadable
		}
		still := map[string]bool{}
		for _, pointer := range withholdValues(nodeType, check) {
			still[pointer] = true
		}
		for _, pointer := range restored {
			if !still[pointer] {
				return nil, invalidRequest(fmt.Sprintf("nodes[%d].parameters at %q holds %s, but this change "+
					"would no longer withhold the value there; keep the name and position the read reported, or "+
					"set a new value", index, pointer, withheldPlaceholder))
			}
		}
	}
	return encoded, nil
}

// restoreWebhookPath copies path, and the form trigger's options.path, from the read when the caller left
// it out, so leaving out a withheld value never changes a workflow's trigger URL.
func restoreWebhookPath(nodeType string, value, original any, changed *bool) (any, error) {
	if value == nil {
		value = map[string]any{}
	}
	params, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	added := false
	if previous, found := valueAt(original, "/path"); found {
		if _, has := params["path"]; !has {
			params["path"], added = previous, true
		}
	}
	if nodeType == formTriggerNodeType {
		if previous, found := valueAt(original, "/options/path"); found {
			switch options := params["options"].(type) {
			case nil:
				if _, has := params["options"]; !has {
					params["options"], added = map[string]any{"path": previous}, true
				}
			case map[string]any:
				if _, has := options["path"]; !has {
					options["path"], added = previous, true
				}
			}
		}
	}
	if added {
		*changed = true
	}
	return params, nil
}

// rewriteStrings visits every string value of a decoded tree in a stable order and replaces it by what
// visit returns; a replacement is not visited again.
func rewriteStrings(value any, pointer string, visit func(pointer, text string) (any, error)) (any, error) {
	switch v := value.(type) {
	case string:
		return visit(pointer, v)
	case map[string]any:
		for _, key := range sortedKeys(v) {
			replaced, err := rewriteStrings(v[key], pointer+"/"+escapePointer(key), visit)
			if err != nil {
				return nil, err
			}
			v[key] = replaced
		}
	case []any:
		for i, item := range v {
			replaced, err := rewriteStrings(item, pointer+"/"+strconv.Itoa(i), visit)
			if err != nil {
				return nil, err
			}
			v[i] = replaced
		}
	}
	return value, nil
}

// valueAt resolves an RFC 6901 pointer in a decoded tree.
func valueAt(value any, pointer string) (any, bool) {
	if pointer == "" {
		return value, true
	}
	for _, token := range strings.Split(pointer[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch v := value.(type) {
		case map[string]any:
			child, ok := v[token]
			if !ok {
				return nil, false
			}
			value = child
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(v) || strconv.Itoa(i) != token {
				return nil, false
			}
			value = v[i]
		default:
			return nil, false
		}
	}
	return value, true
}

func escapePointer(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hasParameters(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// decodeParameters keeps numbers as their literal text, so a masked or restored tree re-encodes them
// unchanged.
func decodeParameters(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("trailing data")
	}
	return value, nil
}

func encodeParameters(value any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buffer.Bytes(), "\n")), nil
}
