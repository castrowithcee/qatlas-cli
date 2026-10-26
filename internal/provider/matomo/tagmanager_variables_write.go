package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// variableOpSuffix ends the operation name of every variable tool; errors.go words the "name is already in
	// use" refusal by it.
	variableOpSuffix  = " Tag Manager variable"
	maxLookupRows     = 100
	variableIDSchema  = `"idVariable":{"type":"integer","minimum":1,"maximum":2147483647}`
	variableDefSchema = `"defaultValue":{"type":"string","maxLength":16384}`
	lookupTableSchema = `"lookupTable":{"type":"array","maxItems":100,"items":{"type":"object","properties":{` +
		`"matchValue":{"type":"string","maxLength":16384},` +
		`"comparison":{"type":"string","pattern":"^[a-z][a-z_]{0,39}$","maxLength":40},` +
		`"outValue":{"type":"string","maxLength":16384}},` +
		`"required":["matchValue","comparison","outValue"],"additionalProperties":false}}`
)

var (
	variableSentOnce = sentOnceFor("variable", "matomo.tag-manager-variables.list")
	// blockedVariableTypes is the fixed list of variable types the tools refuse: both run code of the caller's
	// choosing in every visitor's browser.
	blockedVariableTypes = []string{"CustomJsFunction", "CustomRequestProcessing"}
	// A configuration variable may only name a path below the connection's Matomo as tracker or script endpoint.
	endpointPathPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*(/[A-Za-z0-9_][A-Za-z0-9_.-]*)*/?$`)
	endpointParameters  = []string{"trackingEndpoint", "trackingEndpointCustom", "jsEndpoint", "jsEndpointCustom"}
	// These parameters of a configuration variable hold an address or code, so only an empty value is accepted.
	emptyOnlyParameters = []string{"setApiUrl", "customRequestProcessing"}
)

const variableGuard = "The types CustomJsFunction and CustomRequestProcessing run custom code in the visitors' " +
	"browsers and are refused. A variable of the type MatomoConfiguration decides where Matomo tags send their data, " +
	"so its matomoUrl and idSite must be written out and name this connection's Matomo and the selected website, " +
	"setApiUrl and customRequestProcessing must stay empty, and a tracker or script endpoint must be a plain path; " +
	"otherwise the tool refuses before it changes anything. Only the draft changes: the tool reads the container " +
	"first, takes the draft from it, and never accepts a version, so nothing is published"

var variableDraftFields = []capability.Field{
	{Name: "idSite", Description: "The website the container belongs to"},
	{Name: "idContainer", Description: "The container the variable belongs to"},
	{Name: "idContainerVersion", Description: "ID of the draft that was changed, as Matomo reported it before the change"},
}

func variableParametersArgument(required bool) capability.Argument {
	return capability.Argument{Name: "parameters", Required: required, Description: "Parameter values keyed by " +
		"the parameter names of the variable type in matomo.tag-manager-catalog.list kind variable-types: text, " +
		"number, true or false, or a list of such values or of rows (objects of such values), at most 100 " +
		"parameters, 100 list members, and 50 cells per row; texts up to 16384 bytes and all texts together up to " +
		"262144 bytes. Matomo keeps only the parameters the type defines and sets the others to their defaults"}
}

func variableLookupArgument(required bool) capability.Argument {
	return capability.Argument{Name: "lookupTable", Required: required, Description: "Rules that translate the " +
		"value, at most 100, each with matchValue, comparison (an ID from matomo.tag-manager-catalog.list kind " +
		"comparisons), and outValue (the value used when the rule matches); texts up to 16384 bytes"}
}

var tagManagerVariablesAdd = capability.Descriptor{
	ID: Provider + ".tag-manager-variables.add", Version: 1, Title: "Add a Matomo Tag Manager variable",
	Description: "Create a variable in the draft of a Tag Manager container (addContainerVariable). " + variableGuard +
		". The name must be new among the variables of the draft; type and parameters come from " +
		"matomo.tag-manager-catalog.list; needs the plugin TagManager and a write right; not idempotent, since a " +
		"repeated call with another name creates a second variable" + variableSentOnce,
	Tags: []string{"matomo", "tag-manager", "variables", "create"},
	Risk: tagManagerChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		triggerTypeSchema + `,` + triggerNameSchema + `,` + triggerParametersSchema + `,` + variableDefSchema + `,` +
		lookupTableSchema + `,` + triggerDescSchema + `},"required":["idContainer","type","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idVariable":{"type":"integer"},` +
		`"created":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idVariable","created"],` +
		`"additionalProperties":false}`),
	Arguments: triggerChangeArguments(
		capability.Argument{Name: "type", Required: true, Description: "Variable template ID from " +
			"matomo.tag-manager-catalog.list kind variable-types, such as Constant; it cannot be changed later"},
		capability.Argument{Name: "name", Required: true, Description: "Name of the variable, 1 to 255 characters; " +
			"tags and triggers refer to it as {{Name}}"},
		variableParametersArgument(false),
		capability.Argument{Name: "defaultValue", Description: "Value used when the variable resolves to none, up to " +
			"16384 bytes; none when omitted"},
		variableLookupArgument(false),
		capability.Argument{Name: "description", Description: "Description, up to 1000 characters; empty when omitted"}),
	Fields: append(append([]capability.Field{}, variableDraftFields...),
		capability.Field{Name: "idVariable", Description: "ID of the new variable"},
		capability.Field{Name: "created", Description: "True once Matomo created the variable"}),
	Examples: []capability.Example{{Description: "Add a constant variable to container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","type":"Constant","name":"Shop name",` +
			`"parameters":{"constantValue":"Example Shop"}}`)}},
}

var tagManagerVariablesUpdate = capability.Descriptor{
	ID: Provider + ".tag-manager-variables.update", Version: 1, Title: "Update a Matomo Tag Manager variable",
	Description: "Replace name, parameters, default value, lookup table, and description of a variable in the draft " +
		"of a Tag Manager container (updateContainerVariable); the type stays. Everything is replaced as given, so " +
		"parameters that are left out go back to their defaults and an empty list or text clears the lookup table " +
		"or default value; read the variable first with matomo.tag-manager-variables.get. The tool reads the " +
		"stored variable first and refuses one of a refused type. " + variableGuard + ". The name must be new among " +
		"the variables of the draft; needs the plugin TagManager and a write right; idempotent in effect" + variableSentOnce,
	Tags: []string{"matomo", "tag-manager", "variables", "update"},
	Risk: tagManagerChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		variableIDSchema + `,` + triggerNameSchema + `,` + triggerParametersSchema + `,` + variableDefSchema + `,` +
		lookupTableSchema + `,` + triggerDescSchema + `},"required":["idContainer","idVariable","name","parameters",` +
		`"defaultValue","lookupTable","description"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idVariable":{"type":"integer"},` +
		`"updated":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idVariable","updated"],` +
		`"additionalProperties":false}`),
	Arguments: triggerChangeArguments(
		capability.Argument{Name: "idVariable", Required: true, Description: "Variable ID from " +
			"matomo.tag-manager-variables.list of the draft"},
		capability.Argument{Name: "name", Required: true, Description: "New name, 1 to 255 characters"},
		variableParametersArgument(true),
		capability.Argument{Name: "defaultValue", Required: true, Description: "New default value, up to 16384 " +
			"bytes; an empty text clears it"},
		variableLookupArgument(true),
		capability.Argument{Name: "description", Required: true, Description: "New description, up to 1000 characters; " +
			"an empty text clears it"}),
	Fields: append(append([]capability.Field{}, variableDraftFields...),
		capability.Field{Name: "idVariable", Description: "ID of the variable that was sent"},
		capability.Field{Name: "updated", Description: "True once Matomo accepted the update"}),
	Examples: []capability.Example{{Description: "Rename variable 4 of the draft of container 6OMh6taM of website 1 and clear its lookup table",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idVariable":4,"name":"Shop name",` +
			`"parameters":{"constantValue":"Example Shop"},"defaultValue":"","lookupTable":[],"description":""}`)}},
}

var tagManagerVariablesDelete = capability.Descriptor{
	ID: Provider + ".tag-manager-variables.delete", Version: 1, Title: "Delete a Matomo Tag Manager variable",
	Description: "Delete one variable from the draft of a Tag Manager container (deleteContainerVariable); it " +
		"cannot be undone. Tags, triggers, and variables of the draft that still refer to it by name no longer " +
		"find it; check matomo.tag-manager-references.list kind variable first. Only the draft changes: the tool " +
		"reads the container first, takes the draft from it, and never accepts a version, so nothing live changes " +
		"before a release. Matomo ignores an ID the draft does not hold without changing anything; needs the " +
		"plugin TagManager and a write right; idempotent in effect; offered only where a connection's tools list " +
		"names it" + variableSentOnce,
	Tags: []string{"matomo", "tag-manager", "variables", "delete"},
	Risk: tagManagerChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		variableIDSchema + `},"required":["idContainer","idVariable"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idVariable":{"type":"integer"},` +
		`"deleted":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idVariable","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: triggerChangeArguments(
		capability.Argument{Name: "idVariable", Required: true, Description: "Variable ID from " +
			"matomo.tag-manager-variables.list of the draft"}),
	Fields: append(append([]capability.Field{}, variableDraftFields...),
		capability.Field{Name: "idVariable", Description: "ID of the variable that was sent"},
		capability.Field{Name: "deleted", Description: "True once Matomo accepted the deletion"}),
	Examples: []capability.Example{{Description: "Delete variable 4 from the draft of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idVariable":4}`)}},
}

// VariableCreated is the answer of tag-manager-variables.add.
type VariableCreated struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDVariable         int    `json:"idVariable"`
	Created            bool   `json:"created"`
}

// VariableUpdated is the answer of tag-manager-variables.update.
type VariableUpdated struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDVariable         int    `json:"idVariable"`
	Updated            bool   `json:"updated"`
}

// VariableDeleted is the answer of tag-manager-variables.delete.
type VariableDeleted struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDVariable         int    `json:"idVariable"`
	Deleted            bool   `json:"deleted"`
}

func variableIDOf(given *int) (int, error) {
	if given == nil || *given < 1 || *given > 2147483647 {
		return 0, invalidArgument("idVariable must be a variable ID between 1 and 2147483647")
	}
	return *given, nil
}

func isBlockedVariableType(variableType string) bool {
	variableType = strings.TrimSpace(variableType)
	for _, blocked := range blockedVariableTypes {
		if strings.EqualFold(variableType, blocked) {
			return true
		}
	}
	return false
}

func blockedVariableRefusal() error {
	return invalidArgument("the variable types CustomJsFunction and CustomRequestProcessing run custom code in the " +
		"visitors' browsers and are refused; nothing was changed")
}

// checkConfigurationVariable holds a variable of the type MatomoConfiguration to this connection's Matomo and the
// selected website: that variable decides where every Matomo tag sends its data.
func checkConfigurationVariable(baseURL string, site int, params map[string]any) error {
	refused := invalidArgument("a variable of the type " + configVariableType + " needs matomoUrl and idSite written " +
		"out and naming this connection's Matomo and the selected website, setApiUrl and customRequestProcessing " +
		"empty, and tracker and script endpoints that are plain paths; nothing was changed")
	if !trackingTargetMatches(baseURL, site, params) {
		return refused
	}
	for _, name := range emptyOnlyParameters {
		if value, set := params[name]; set && value != nil {
			if text, isText := value.(string); !isText || text != "" {
				return refused
			}
		}
	}
	for _, name := range endpointParameters {
		if value, set := params[name]; set && value != nil {
			if text, isText := value.(string); !isText || text != "" && !endpointPathPattern.MatchString(text) {
				return refused
			}
		}
	}
	return nil
}

// variableForm validates the settings shared by add and update and flattens them into form fields. params is the
// decoded parameters object, nil when none was given.
func variableForm(name *string, description *string, defaultValue *string, parameters, lookup json.RawMessage,
	replace bool) (url.Values, map[string]any, error) {
	form := url.Values{}
	nameText, err := triggerNameOf(name)
	if err != nil {
		return nil, nil, err
	}
	form.Set("name", nameText)
	descriptionText, err := triggerDescriptionOf(description, replace)
	if err != nil {
		return nil, nil, err
	}
	form.Set("description", descriptionText)
	budget := &formBudget{remaining: maxTriggerTotalText}
	if defaultValue == nil {
		if replace {
			return nil, nil, invalidArgument("defaultValue is required; an empty text clears it")
		}
	} else {
		text, err := budget.text(*defaultValue)
		if err != nil {
			return nil, nil, invalidArgument("defaultValue is longer than 16384 bytes or not valid text")
		}
		form.Set("defaultValue", text)
	}
	var params map[string]any
	if len(bytes.TrimSpace(parameters)) == 0 {
		if replace {
			return nil, nil, invalidArgument("parameters are required; an empty object clears them")
		}
	} else if params, err = decodeJSONObject(parameters); err != nil {
		return nil, nil, invalidArgument("parameters must be an object of at most 100 parameters")
	}
	if err := triggerParameters(parameters, budget, form); err != nil {
		return nil, nil, err
	}
	if len(bytes.TrimSpace(lookup)) == 0 {
		if replace {
			return nil, nil, invalidArgument("lookupTable is required; an empty list clears it")
		}
	} else if err := variableLookupTable(lookup, budget, form); err != nil {
		return nil, nil, err
	}
	return form, params, nil
}

// variableLookupTable flattens the rows into the form fields lookupTable[0][matchValue] and so on.
func variableLookupTable(raw json.RawMessage, budget *formBudget, form url.Values) error {
	var rows []struct {
		MatchValue *string `json:"matchValue"`
		Comparison *string `json:"comparison"`
		OutValue   *string `json:"outValue"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rows); err != nil || len(rows) > maxLookupRows {
		return invalidArgument("lookupTable must be a list of at most 100 objects with matchValue, comparison, and outValue")
	}
	for i, row := range rows {
		if row.MatchValue == nil || row.Comparison == nil || row.OutValue == nil {
			return invalidArgument("each row of lookupTable needs matchValue, comparison, and outValue")
		}
		if !comparisonPattern.MatchString(*row.Comparison) {
			return invalidArgument("comparison of a lookup row must be an ID from the comparisons of the catalog")
		}
		prefix := "lookupTable[" + strconv.Itoa(i) + "]"
		for name, value := range map[string]string{"matchValue": *row.MatchValue, "comparison": *row.Comparison,
			"outValue": *row.OutValue} {
			text, err := budget.text(value)
			if err != nil {
				return invalidArgument("a text in lookupTable is longer than 16384 bytes, not valid text, or the " +
					"texts together exceed 262144 bytes")
			}
			form.Set(prefix+"["+name+"]", text)
		}
	}
	return nil
}

// variableArguments are the arguments of the add and update tools.
type variableArguments struct {
	IDSite       *int            `json:"idSite"`
	IDContainer  *string         `json:"idContainer"`
	IDVariable   *int            `json:"idVariable"`
	Type         *string         `json:"type"`
	Name         *string         `json:"name"`
	Parameters   json.RawMessage `json:"parameters"`
	DefaultValue *string         `json:"defaultValue"`
	LookupTable  json.RawMessage `json:"lookupTable"`
	Description  *string         `json:"description"`
}

// readVariable reads the stored variable of the draft once; it is the evidence for the type that update checks.
func readVariable(ctx context.Context, client *Client, op, tool string, site int, container string, draft, id int) (string, error) {
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idContainer": {container},
		"idContainerVersion": {strconv.Itoa(draft)}, "idVariable": {strconv.Itoa(id)}}
	var answer json.RawMessage
	if err := client.call(ctx, op, tagManagerRequirement(tool), "TagManager.getContainerVariable", params, &answer); err != nil {
		return "", err
	}
	if missingAnswer(answer) {
		return "", &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Matomo does not hold this variable in the draft; nothing was changed"}
	}
	var parsed exportEntryJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &parsed) != nil || parsed.id() == nil ||
		*parsed.id() != id || !(entryRequest{site: site, version: draft}).bound(parsed) {
		return "", provider.InvalidResponse(op, "Matomo returned an invalid variable; nothing was changed")
	}
	return strings.TrimSpace(looseText(parsed.Type)), nil
}

func variableScope(resolved *config.Resolved, a *variableArguments) (int, string, error) {
	id, err := selectSite(resolved, a.IDSite)
	if err != nil {
		return 0, "", err
	}
	container, err := containerIDOf(a.IDContainer)
	if err != nil {
		return 0, "", err
	}
	return id, container, nil
}

func invokeTagManagerVariablesAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add" + variableOpSuffix
	var args variableArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, container, err := variableScope(resolved, &args)
	if err != nil {
		return nil, err
	}
	if args.IDVariable != nil {
		return nil, invalidArgument("idVariable does not belong to this tool")
	}
	if args.Type == nil || !triggerTypePattern.MatchString(*args.Type) {
		return nil, invalidArgument("type must be a variable template ID of letters, digits, and underscores")
	}
	// The refusals below come before the secret is read and before any request.
	if isBlockedVariableType(*args.Type) {
		return nil, blockedVariableRefusal()
	}
	fields, params, err := variableForm(args.Name, args.Description, args.DefaultValue, args.Parameters, args.LookupTable, false)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(*args.Type, configVariableType) {
		if err := checkConfigurationVariable(resolved.BaseURL, id, params); err != nil {
			return nil, err
		}
	}
	fields.Set("type", *args.Type)
	draft, answer, err := triggerChange(ctx, resolved, secrets, red, op, tagManagerVariablesAdd.ID,
		"TagManager.addContainerVariable", id, container, fields, variableSentOnce)
	if err != nil {
		return nil, err
	}
	var created int
	if scalar(answer, &created) != nil || created < 1 {
		return nil, unclearChange(op, "Matomo returned no usable ID for the new variable", variableSentOnce)
	}
	return &VariableCreated{IDSite: id, IDContainer: container, IDContainerVersion: draft, IDVariable: created,
		Created: true}, nil
}

func invokeTagManagerVariablesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update" + variableOpSuffix
	var args variableArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, container, err := variableScope(resolved, &args)
	if err != nil {
		return nil, err
	}
	variableID, err := variableIDOf(args.IDVariable)
	if err != nil {
		return nil, err
	}
	if args.Type != nil {
		return nil, invalidArgument("the type of a variable cannot be changed")
	}
	fields, params, err := variableForm(args.Name, args.Description, args.DefaultValue, args.Parameters, args.LookupTable, true)
	if err != nil {
		return nil, err
	}
	fields.Set("idVariable", strconv.Itoa(variableID))
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	draft, err := triggerDraft(ctx, client, op, tagManagerVariablesUpdate.ID, id, container)
	if err != nil {
		return nil, err
	}
	stored, err := readVariable(ctx, client, op, tagManagerVariablesUpdate.ID, id, container, draft, variableID)
	if err != nil {
		return nil, err
	}
	if isBlockedVariableType(stored) {
		return nil, blockedVariableRefusal()
	}
	if strings.EqualFold(stored, configVariableType) {
		if err := checkConfigurationVariable(resolved.BaseURL, id, params); err != nil {
			return nil, err
		}
	}
	request := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {container}, "idContainerVersion": {strconv.Itoa(draft)}}
	for name, values := range fields {
		request[name] = values
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tagManagerVariablesUpdate.ID),
		"TagManager.updateContainerVariable", request, variableSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", variableSentOnce)
	}
	return &VariableUpdated{IDSite: id, IDContainer: container, IDContainerVersion: draft, IDVariable: variableID,
		Updated: true}, nil
}

func invokeTagManagerVariablesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete" + variableOpSuffix
	var args struct {
		IDSite      *int    `json:"idSite"`
		IDContainer *string `json:"idContainer"`
		IDVariable  *int    `json:"idVariable"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	container, err := containerIDOf(args.IDContainer)
	if err != nil {
		return nil, err
	}
	variableID, err := variableIDOf(args.IDVariable)
	if err != nil {
		return nil, err
	}
	fields := url.Values{"idVariable": {strconv.Itoa(variableID)}}
	draft, answer, err := triggerChange(ctx, resolved, secrets, red, op, tagManagerVariablesDelete.ID,
		"TagManager.deleteContainerVariable", id, container, fields, variableSentOnce)
	if err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", variableSentOnce)
	}
	return &VariableDeleted{IDSite: id, IDContainer: container, IDContainerVersion: draft, IDVariable: variableID,
		Deleted: true}, nil
}
