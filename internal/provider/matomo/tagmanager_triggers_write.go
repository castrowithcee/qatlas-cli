package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// triggerOpSuffix ends the operation name of every trigger tool; errors.go words the shared "name is already
	// in use" refusal by it.
	triggerOpSuffix = " Tag Manager trigger"
	// Matomo checks the name of a trigger at 255 characters; the description is stored in 1000.
	maxTriggerName        = 255
	maxTriggerDescription = 1000
	// The caps of one value, of all values together, and of the shapes: parameters name a template's parameters,
	// a list stands for a table parameter (rows of text cells), a condition has three members.
	maxTriggerText       = 16384
	maxTriggerTotalText  = 262144
	maxTriggerParameters = 100
	maxParameterList     = 100
	maxParameterCells    = 50
	maxTriggerConditions = 50
)

var (
	triggerTypePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	parameterNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	comparisonPattern    = regexp.MustCompile(`^[a-z][a-z_]{0,39}$`)
	triggerSentOnce      = sentOnceFor("trigger", "matomo.tag-manager-triggers.list")
)

const (
	triggerTypeSchema = `"type":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$","maxLength":64}`
	triggerNameSchema = `"name":{"type":"string","minLength":1,"maxLength":255}`
	triggerDescSchema = `"description":{"type":"string","maxLength":1000}`
	triggerIDSchema   = `"idTrigger":{"type":"integer","minimum":1,"maximum":2147483647}`
	// A parameter value is text, a number, a flag, or a list of texts or of rows of text cells.
	parameterScalarSchema = `{"anyOf":[{"type":"string","maxLength":16384},{"type":"number"},{"type":"boolean"}]}`
	parameterValueSchema  = `{"anyOf":[{"type":"string","maxLength":16384},{"type":"number"},{"type":"boolean"},` +
		`{"type":"array","minItems":1,"maxItems":100,"items":{"anyOf":[{"type":"string","maxLength":16384},` +
		`{"type":"number"},{"type":"boolean"},{"type":"object","minProperties":1,"maxProperties":50,` +
		`"propertyNames":{"pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},"additionalProperties":` + parameterScalarSchema + `}]}}]}`
	triggerParametersSchema = `"parameters":{"type":"object","maxProperties":100,"propertyNames":` +
		`{"pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},"additionalProperties":` + parameterValueSchema + `}`
	triggerConditionsSchema = `"conditions":{"type":"array","maxItems":50,"items":{"type":"object","properties":{` +
		`"actual":{"type":"string","minLength":1,"maxLength":255},` +
		`"comparison":{"type":"string","pattern":"^[a-z][a-z_]{0,39}$","maxLength":40},` +
		`"expected":{"type":"string","minLength":1,"maxLength":16384}},` +
		`"required":["actual","comparison","expected"],"additionalProperties":false}}`
)

func triggerChangeArguments(extra ...capability.Argument) []capability.Argument {
	return append([]capability.Argument{idSiteArgument(),
		{Name: "idContainer", Required: true, Description: "Container ID from matomo.tag-manager-containers.list, " +
			"up to 8 letters and digits"}}, extra...)
}

var (
	triggerParametersArgument = func(required bool) capability.Argument {
		return capability.Argument{Name: "parameters", Required: required, Description: "Parameter values keyed by " +
			"the parameter names of the trigger type in matomo.tag-manager-catalog.list kind trigger-types: text, " +
			"number, true or false, or a list of such values or of rows (objects of such values), at most 100 " +
			"parameters, 100 list members, and 50 cells per row; texts up to 16384 bytes and all texts together up to " +
			"262144 bytes. Matomo keeps only the parameters the type defines and sets the others to their defaults"}
	}
	triggerConditionsArgument = func(required bool) capability.Argument {
		return capability.Argument{Name: "conditions", Required: required, Description: "Conditions that must all " +
			"match before the trigger fires, at most 50, each with actual (the name of a variable of the draft or a " +
			"pre-configured variable), comparison (an ID from matomo.tag-manager-catalog.list kind comparisons), and " +
			"expected (text, up to 16384 bytes)"}
	}
)

var triggerDraftFields = []capability.Field{
	{Name: "idSite", Description: "The website the container belongs to"},
	{Name: "idContainer", Description: "The container the trigger belongs to"},
	{Name: "idContainerVersion", Description: "ID of the draft that was changed, as Matomo reported it before the change"},
}

var tagManagerTriggersAdd = capability.Descriptor{
	ID: Provider + ".tag-manager-triggers.add", Version: 1, Title: "Add a Matomo Tag Manager trigger",
	Description: "Create a trigger in the draft of a Tag Manager container (addContainerTrigger). Triggers collect " +
		"nothing themselves but decide when tags fire. Only the draft changes: the tool reads the container first, " +
		"takes the draft from it, and never accepts a version, so saved versions and releases stay as they are and " +
		"nothing is published. The name must be new among the triggers of the draft; type, parameters, and " +
		"conditions come from matomo.tag-manager-catalog.list; a custom template needs the right to use custom " +
		"templates; needs the plugin TagManager and a write right; not idempotent, since a repeated call with " +
		"another name creates a second trigger" + triggerSentOnce,
	Tags: []string{"matomo", "tag-manager", "triggers", "create"},
	Risk: tagManagerChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		triggerTypeSchema + `,` + triggerNameSchema + `,` + triggerParametersSchema + `,` + triggerConditionsSchema + `,` +
		triggerDescSchema + `},"required":["idContainer","type","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idTrigger":{"type":"integer"},` +
		`"created":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idTrigger","created"],` +
		`"additionalProperties":false}`),
	Arguments: triggerChangeArguments(
		capability.Argument{Name: "type", Required: true, Description: "Trigger template ID from " +
			"matomo.tag-manager-catalog.list kind trigger-types, such as PageView; it cannot be changed later"},
		capability.Argument{Name: "name", Required: true, Description: "Name of the trigger, 1 to 255 characters"},
		triggerParametersArgument(false), triggerConditionsArgument(false),
		capability.Argument{Name: "description", Description: "Description, up to 1000 characters; empty when omitted"}),
	Fields: append(append([]capability.Field{}, triggerDraftFields...),
		capability.Field{Name: "idTrigger", Description: "ID of the new trigger"},
		capability.Field{Name: "created", Description: "True once Matomo created the trigger"}),
	Examples: []capability.Example{{Description: "Add a page view trigger with one condition to container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","type":"PageView","name":"Shop pages",` +
			`"conditions":[{"actual":"PagePath","comparison":"starts_with","expected":"/shop"}]}`)}},
}

var tagManagerTriggersUpdate = capability.Descriptor{
	ID: Provider + ".tag-manager-triggers.update", Version: 1, Title: "Update a Matomo Tag Manager trigger",
	Description: "Replace name, parameters, conditions, and description of a trigger in the draft of a Tag Manager " +
		"container (updateContainerTrigger); the type stays. Everything is replaced as given, so omitted " +
		"conditions are gone and parameters that are left out go back to their defaults; read the trigger first with " +
		"matomo.tag-manager-triggers.get. Only the draft changes: the tool reads the container first, takes the " +
		"draft from it, and never accepts a version. Matomo takes the trigger by its ID within the draft and ignores " +
		"an ID the draft does not hold without changing anything. The name must be new among the triggers of the " +
		"draft; needs the plugin TagManager and a write right; idempotent in effect" + triggerSentOnce,
	Tags: []string{"matomo", "tag-manager", "triggers", "update"},
	Risk: tagManagerChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		triggerIDSchema + `,` + triggerNameSchema + `,` + triggerParametersSchema + `,` + triggerConditionsSchema + `,` +
		triggerDescSchema + `},"required":["idContainer","idTrigger","name","parameters","conditions","description"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idTrigger":{"type":"integer"},` +
		`"updated":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idTrigger","updated"],` +
		`"additionalProperties":false}`),
	Arguments: triggerChangeArguments(
		capability.Argument{Name: "idTrigger", Required: true, Description: "Trigger ID from " +
			"matomo.tag-manager-triggers.list of the draft"},
		capability.Argument{Name: "name", Required: true, Description: "New name, 1 to 255 characters"},
		triggerParametersArgument(true), triggerConditionsArgument(true),
		capability.Argument{Name: "description", Required: true, Description: "New description, up to 1000 characters; " +
			"an empty text clears it"}),
	Fields: append(append([]capability.Field{}, triggerDraftFields...),
		capability.Field{Name: "idTrigger", Description: "ID of the trigger that was sent"},
		capability.Field{Name: "updated", Description: "True once Matomo accepted the update"}),
	Examples: []capability.Example{{Description: "Rename trigger 4 of the draft of container 6OMh6taM of website 1 and clear its conditions",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idTrigger":4,"name":"All pages","parameters":{},` +
			`"conditions":[],"description":""}`)}},
}

var tagManagerTriggersDelete = capability.Descriptor{
	ID: Provider + ".tag-manager-triggers.delete", Version: 1, Title: "Delete a Matomo Tag Manager trigger",
	Description: "Delete one trigger from the draft of a Tag Manager container (deleteContainerTrigger); it cannot be " +
		"undone. Only the draft changes: the tool reads the container first, takes the draft from it, and never " +
		"accepts a version. Matomo refuses a trigger that a tag of the draft still uses as fire or block trigger and " +
		"ignores an ID the draft does not hold without changing anything; needs the plugin TagManager and a write " +
		"right; idempotent in effect; offered only where a connection's tools list names it" + triggerSentOnce,
	Tags: []string{"matomo", "tag-manager", "triggers", "delete"},
	Risk: tagManagerChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		triggerIDSchema + `},"required":["idContainer","idTrigger"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idTrigger":{"type":"integer"},` +
		`"deleted":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idTrigger","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: triggerChangeArguments(
		capability.Argument{Name: "idTrigger", Required: true, Description: "Trigger ID from " +
			"matomo.tag-manager-triggers.list of the draft"}),
	Fields: append(append([]capability.Field{}, triggerDraftFields...),
		capability.Field{Name: "idTrigger", Description: "ID of the trigger that was sent"},
		capability.Field{Name: "deleted", Description: "True once Matomo accepted the deletion"}),
	Examples: []capability.Example{{Description: "Delete trigger 4 from the draft of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idTrigger":4}`)}},
}

// TriggerCreated is the answer of tag-manager-triggers.add.
type TriggerCreated struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDTrigger          int    `json:"idTrigger"`
	Created            bool   `json:"created"`
}

// TriggerUpdated is the answer of tag-manager-triggers.update.
type TriggerUpdated struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDTrigger          int    `json:"idTrigger"`
	Updated            bool   `json:"updated"`
}

// TriggerDeleted is the answer of tag-manager-triggers.delete.
type TriggerDeleted struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDTrigger          int    `json:"idTrigger"`
	Deleted            bool   `json:"deleted"`
}

func triggerNameOf(given *string) (string, error) {
	if given == nil || !formText(*given) {
		return "", invalidArgument("name must be 1 to 255 characters")
	}
	// Matomo trims the name before it checks and stores it.
	if length := utf8.RuneCountInString(strings.TrimSpace(*given)); length < 1 || length > maxTriggerName {
		return "", invalidArgument("name must be 1 to 255 characters")
	}
	return *given, nil
}

func triggerDescriptionOf(given *string, required bool) (string, error) {
	if given == nil {
		if required {
			return "", invalidArgument("description is required; an empty text clears it")
		}
		return "", nil
	}
	if !formText(*given) || utf8.RuneCountInString(*given) > maxTriggerDescription {
		return "", invalidArgument("description must be at most 1000 characters")
	}
	return *given, nil
}

func triggerIDOf(given *int) (int, error) {
	if given == nil || *given < 1 || *given > 2147483647 {
		return 0, invalidArgument("idTrigger must be a trigger ID between 1 and 2147483647")
	}
	return *given, nil
}

// formText accepts text that is valid UTF-8 and holds no NUL, which no form field of Matomo carries.
func formText(text string) bool { return utf8.ValidString(text) && !strings.ContainsRune(text, 0) }

// formBudget counts the bytes of all texts of one request.
type formBudget struct{ remaining int }

func (b *formBudget) text(value string) (string, error) {
	if len(value) > maxTriggerText || !formText(value) {
		return "", invalidArgument("a text in parameters or conditions is longer than 16384 bytes or not valid text")
	}
	b.remaining -= len(value)
	if b.remaining < 0 {
		return "", invalidArgument("the texts in parameters and conditions together exceed 262144 bytes")
	}
	return value, nil
}

// scalar turns a text, a number, or a flag into the value of a form field. A flag becomes 1 or 0, which every
// reading of a Matomo flag setting takes alike.
func (b *formBudget) scalar(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return b.text(v)
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	case json.Number:
		if i, err := strconv.ParseInt(v.String(), 10, 64); err == nil {
			return strconv.FormatInt(i, 10), nil
		}
		f, err := strconv.ParseFloat(v.String(), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return "", invalidArgument("a number in parameters is not a finite number")
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	}
	return "", invalidArgument("a value in parameters must be a text, a number, a flag, or a list of them")
}

// triggerParameters flattens the parameters into form fields of the shape name, name[0], or name[0][cell], which
// PHP reads back as a value, a list, or a list of rows.
func triggerParameters(raw json.RawMessage, budget *formBudget, form url.Values) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	parameters, err := decodeJSONObject(raw)
	if err != nil || len(parameters) > maxTriggerParameters {
		return invalidArgument("parameters must be an object of at most 100 parameters")
	}
	for name, value := range parameters {
		if !parameterNamePattern.MatchString(name) {
			return invalidArgument("a parameter name must be a letter followed by up to 63 letters, digits, or underscores")
		}
		key := "parameters[" + name + "]"
		list, isList := value.([]any)
		if !isList {
			text, err := budget.scalar(value)
			if err != nil {
				return err
			}
			form.Set(key, text)
			continue
		}
		if len(list) < 1 || len(list) > maxParameterList {
			return invalidArgument("a list in parameters must hold 1 to 100 members")
		}
		for i, member := range list {
			itemKey := key + "[" + strconv.Itoa(i) + "]"
			row, isRow := member.(map[string]any)
			if !isRow {
				text, err := budget.scalar(member)
				if err != nil {
					return err
				}
				form.Set(itemKey, text)
				continue
			}
			if len(row) < 1 || len(row) > maxParameterCells {
				return invalidArgument("a row in parameters must hold 1 to 50 cells")
			}
			for cell, cellValue := range row {
				if !parameterNamePattern.MatchString(cell) {
					return invalidArgument("a cell name in parameters must be a letter followed by up to 63 letters, digits, or underscores")
				}
				text, err := budget.scalar(cellValue)
				if err != nil {
					return err
				}
				form.Set(itemKey+"["+cell+"]", text)
			}
		}
	}
	return nil
}

func decodeJSONObject(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil || out == nil {
		return nil, fmt.Errorf("not an object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data")
	}
	return out, nil
}

// triggerConditions flattens the conditions into the form fields conditions[0][actual] and so on.
func triggerConditions(raw json.RawMessage, budget *formBudget, form url.Values) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var conditions []struct {
		Actual     *string `json:"actual"`
		Comparison *string `json:"comparison"`
		Expected   *string `json:"expected"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&conditions); err != nil || len(conditions) > maxTriggerConditions {
		return invalidArgument("conditions must be a list of at most 50 objects with actual, comparison, and expected")
	}
	for i, c := range conditions {
		if c.Actual == nil || c.Comparison == nil || c.Expected == nil {
			return invalidArgument("each condition needs actual, comparison, and expected")
		}
		if length := utf8.RuneCountInString(*c.Actual); length < 1 || length > 255 {
			return invalidArgument("actual of a condition must be 1 to 255 characters")
		}
		if !comparisonPattern.MatchString(*c.Comparison) {
			return invalidArgument("comparison of a condition must be an ID from the comparisons of the catalog")
		}
		if *c.Expected == "" {
			return invalidArgument("expected of a condition must not be empty")
		}
		prefix := "conditions[" + strconv.Itoa(i) + "]"
		for name, value := range map[string]string{"actual": *c.Actual, "comparison": *c.Comparison, "expected": *c.Expected} {
			text, err := budget.text(value)
			if err != nil {
				return err
			}
			form.Set(prefix+"["+name+"]", text)
		}
	}
	return nil
}

// triggerDraft reads the container once and returns the ID of its draft. Every later request of the tool goes
// to this version, which the caller cannot choose: Matomo itself would accept a saved version, too.
func triggerDraft(ctx context.Context, client *Client, op, tool string, site int, container string) (int, error) {
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idContainer": {container}}
	if err := client.call(ctx, op, tagManagerRequirement(tool), "TagManager.getContainer", params, &answer); err != nil {
		return 0, err
	}
	var row containerJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &row) != nil || row.ID == nil {
		return 0, provider.InvalidResponse(op, "Matomo returned an invalid container; nothing was changed")
	}
	found, ok := row.container(site)
	if !ok || found.ID != container {
		return 0, provider.InvalidResponse(op, "Matomo returned another container than asked for; nothing was changed")
	}
	if found.Draft == nil || found.Draft.ID < 1 || found.Draft.Revision == nil || *found.Draft.Revision != 0 {
		return 0, provider.InvalidResponse(op, "Matomo returned no draft for the container; nothing was changed")
	}
	return found.Draft.ID, nil
}

// triggerChange runs the shared steps of the three tools after their arguments are valid: the Nachweis read of the
// draft, then the one writing request into it.
func triggerChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op, tool, method string, site int, container string, fields url.Values) (int, json.RawMessage, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return 0, nil, err
	}
	draft, err := triggerDraft(ctx, client, op, tool, site, container)
	if err != nil {
		return 0, nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idContainer": {container},
		"idContainerVersion": {strconv.Itoa(draft)}}
	for name, values := range fields {
		params[name] = values
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tool), method, params, triggerSentOnce, &answer); err != nil {
		return 0, nil, err
	}
	return draft, answer, nil
}

func invokeTagManagerTriggersAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add" + triggerOpSuffix
	var args struct {
		IDSite      *int            `json:"idSite"`
		IDContainer *string         `json:"idContainer"`
		Type        *string         `json:"type"`
		Name        *string         `json:"name"`
		Parameters  json.RawMessage `json:"parameters"`
		Conditions  json.RawMessage `json:"conditions"`
		Description *string         `json:"description"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	containerID, err := containerIDOf(args.IDContainer)
	if err != nil {
		return nil, err
	}
	if args.Type == nil || !triggerTypePattern.MatchString(*args.Type) {
		return nil, invalidArgument("type must be a trigger template ID of letters, digits, and underscores")
	}
	name, err := triggerNameOf(args.Name)
	if err != nil {
		return nil, err
	}
	description, err := triggerDescriptionOf(args.Description, false)
	if err != nil {
		return nil, err
	}
	fields := url.Values{"type": {*args.Type}, "name": {name}, "description": {description}}
	budget := &formBudget{remaining: maxTriggerTotalText}
	if err := triggerParameters(args.Parameters, budget, fields); err != nil {
		return nil, err
	}
	if err := triggerConditions(args.Conditions, budget, fields); err != nil {
		return nil, err
	}
	draft, answer, err := triggerChange(ctx, resolved, secrets, red, op, tagManagerTriggersAdd.ID,
		"TagManager.addContainerTrigger", id, containerID, fields)
	if err != nil {
		return nil, err
	}
	var created int
	if scalar(answer, &created) != nil || created < 1 {
		return nil, unclearChange(op, "Matomo returned no usable ID for the new trigger", triggerSentOnce)
	}
	return &TriggerCreated{IDSite: id, IDContainer: containerID, IDContainerVersion: draft, IDTrigger: created,
		Created: true}, nil
}

func invokeTagManagerTriggersUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update" + triggerOpSuffix
	var args struct {
		IDSite      *int            `json:"idSite"`
		IDContainer *string         `json:"idContainer"`
		IDTrigger   *int            `json:"idTrigger"`
		Name        *string         `json:"name"`
		Parameters  json.RawMessage `json:"parameters"`
		Conditions  json.RawMessage `json:"conditions"`
		Description *string         `json:"description"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	containerID, err := containerIDOf(args.IDContainer)
	if err != nil {
		return nil, err
	}
	triggerID, err := triggerIDOf(args.IDTrigger)
	if err != nil {
		return nil, err
	}
	name, err := triggerNameOf(args.Name)
	if err != nil {
		return nil, err
	}
	description, err := triggerDescriptionOf(args.Description, true)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(args.Parameters)) == 0 || len(bytes.TrimSpace(args.Conditions)) == 0 {
		return nil, invalidArgument("parameters and conditions are required; an empty object and an empty list clear them")
	}
	fields := url.Values{"idTrigger": {strconv.Itoa(triggerID)}, "name": {name}, "description": {description}}
	budget := &formBudget{remaining: maxTriggerTotalText}
	if err := triggerParameters(args.Parameters, budget, fields); err != nil {
		return nil, err
	}
	if err := triggerConditions(args.Conditions, budget, fields); err != nil {
		return nil, err
	}
	draft, answer, err := triggerChange(ctx, resolved, secrets, red, op, tagManagerTriggersUpdate.ID,
		"TagManager.updateContainerTrigger", id, containerID, fields)
	if err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", triggerSentOnce)
	}
	return &TriggerUpdated{IDSite: id, IDContainer: containerID, IDContainerVersion: draft, IDTrigger: triggerID,
		Updated: true}, nil
}

func invokeTagManagerTriggersDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete" + triggerOpSuffix
	var args struct {
		IDSite      *int    `json:"idSite"`
		IDContainer *string `json:"idContainer"`
		IDTrigger   *int    `json:"idTrigger"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	containerID, err := containerIDOf(args.IDContainer)
	if err != nil {
		return nil, err
	}
	triggerID, err := triggerIDOf(args.IDTrigger)
	if err != nil {
		return nil, err
	}
	fields := url.Values{"idTrigger": {strconv.Itoa(triggerID)}}
	draft, answer, err := triggerChange(ctx, resolved, secrets, red, op, tagManagerTriggersDelete.ID,
		"TagManager.deleteContainerTrigger", id, containerID, fields)
	if err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", triggerSentOnce)
	}
	return &TriggerDeleted{IDSite: id, IDContainer: containerID, IDContainerVersion: draft, IDTrigger: triggerID,
		Deleted: true}, nil
}
