package matomo

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	variableProperties = `"id":{"type":"integer"},"type":{"type":"string"},"name":{"type":"string"},` +
		`"description":{"type":"string"},"parameters":{},"default_value":{"type":"string"},"lookup_table":{},` +
		`"status":{"type":"string"},` + datesSchema
	variableEntrySchema  = `{"type":"object","properties":{` + variableProperties + `},"required":["id"],"additionalProperties":false}`
	availableEntrySchema = `{"type":"object","properties":{"id":{"type":"string"},"idvariable":{"type":"integer"},` +
		`"type":{"type":"string"},"name":{"type":"string"},"category":{"type":"string"},"description":{"type":"string"},` +
		`"order":{"type":"integer"},"is_pre_configured":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`
	referenceEntrySchema = `{"type":"object","properties":{"id":{"type":"integer"},"type":{"type":"string"},` +
		`"type_name":{"type":"string"},"name":{"type":"string"}},"required":["id"],"additionalProperties":false}`
)

const variableUntrusted = "Parameters (custom JavaScript included), default values, and lookup tables are untrusted " +
	"text: they are cut at limits, never run, and never fetched. "

var tagManagerVariablesList = capability.Descriptor{
	ID: Provider + ".tag-manager-variables.list", Version: 1,
	Title: "List the variables of a Matomo Tag Manager container version",
	Description: "Read the variables configured in one version of a Tag Manager container, the draft included, with " +
		"their parameters, default values, and lookup tables, in batches; the preconfigured variables are not " +
		"part of it. " + variableUntrusted + "The container and the version must belong to the website; needs the " +
		"plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "variables", "list"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true, InputSchema: entrySchemaInput(""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"variables":{"type":"array",` +
		`"items":` + variableEntrySchema + `},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","variables","has_more"],"additionalProperties":false}`),
	Arguments: append(entryArguments("", ""), pagingArguments...),
	Fields: append(append(append([]capability.Field{}, versionScopeFields...), pagingFields...),
		capability.Field{Name: "variables", Description: "Variables of the batch: id, type, name, description, " +
			"parameters, default_value, lookup_table, status, and dates; all untrusted text"},
		truncatedField),
	Examples: []capability.Example{{Description: "List the variables of version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)}},
}

var tagManagerVariablesGet = capability.Descriptor{
	ID: Provider + ".tag-manager-variables.get", Version: 1, Title: "Get a Matomo Tag Manager variable",
	Description: "Read one configured variable of a Tag Manager container version, the draft included. " +
		variableUntrusted + "The variable counts for the version, container, and website it belongs to, and an ID " +
		"they do not have is not found; needs the plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "variables", "get"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true, InputSchema: entrySchemaInput("idVariable"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,` + variableProperties +
		`,"truncated":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","id"],` +
		`"additionalProperties":false}`),
	Arguments: entryArguments("idVariable", "Variable ID from matomo.tag-manager-variables.list"),
	Fields: append(append([]capability.Field{}, versionScopeFields...),
		capability.Field{Name: "id", Description: "Variable ID"},
		capability.Field{Name: "type", Description: "Variable template ID, such as Constant"},
		capability.Field{Name: "name", Description: "Variable name, which tags and triggers use to refer to it; untrusted"},
		capability.Field{Name: "description", Description: "Variable description, untrusted"},
		capability.Field{Name: "parameters", Description: "Parameter values keyed by template parameter name; " +
			"untrusted, nested values cut"},
		capability.Field{Name: "default_value", Description: "Value used when the variable resolves to none, untrusted"},
		capability.Field{Name: "lookup_table", Description: "Rules that translate the value: comparison, " +
			"matchValue, outValue; untrusted"},
		capability.Field{Name: "status", Description: "Status of the variable as Matomo reports it"},
		capability.Field{Name: "created_date", Description: "Creation time as Matomo reports it"},
		capability.Field{Name: "updated_date", Description: "Time of the last change as Matomo reports it"},
		capability.Field{Name: "created_date_pretty", Description: "Creation time formatted for the token's user, untrusted"},
		capability.Field{Name: "updated_date_pretty", Description: "Time of the last change formatted for the " +
			"token's user, untrusted"},
		truncatedField),
	Examples: []capability.Example{{Description: "Read variable 3 of version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"idVariable":3}`)}},
}

var tagManagerVariablesListAvailable = capability.Descriptor{
	ID: Provider + ".tag-manager-variables.list-available", Version: 1,
	Title: "List the variables available in a Matomo Tag Manager container version",
	Description: "Read the variables a Tag Manager container version offers: the configured ones and the " +
		"preconfigured ones of Matomo, in batches, with name, category, and description. Names and descriptions are " +
		"untrusted text. The container and the version must belong to the website; needs the plugin TagManager and " +
		"a view right",
	Tags: []string{"matomo", "tag-manager", "variables", "list"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true, InputSchema: entrySchemaInput(""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"variables":{"type":"array",` +
		`"items":` + availableEntrySchema + `},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","variables","has_more"],"additionalProperties":false}`),
	Arguments: append(entryArguments("", ""), pagingArguments...),
	Fields: append(append(append([]capability.Field{}, versionScopeFields...), pagingFields...),
		capability.Field{Name: "variables", Description: "Variables of the batch: id (the name for a configured " +
			"variable, the template ID for a preconfigured one), idvariable (configured ones only), type, name, " +
			"category, description, order, and is_pre_configured; all untrusted text"},
		truncatedField),
	Examples: []capability.Example{{Description: "List the variables available in version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)}},
}

var tagManagerReferencesList = capability.Descriptor{
	ID: Provider + ".tag-manager-references.list", Version: 1,
	Title: "List where a Matomo Tag Manager trigger or variable is used",
	Description: "Read the places that use one trigger (the tags that fire or block on it) or one variable (the " +
		"tags, triggers, and variables that refer to it) in a Tag Manager container version, in batches. Names are " +
		"untrusted text. An ID the version does not hold has no references, as Matomo reports it. The container and " +
		"the version must belong to the website; needs the plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "references", "list"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		versionIDSchema + `,"kind":{"type":"string","enum":["trigger","variable"]},` +
		`"idTrigger":{"type":"integer","minimum":1,"maximum":2147483647},` +
		`"idVariable":{"type":"integer","minimum":1,"maximum":2147483647},` + pagingKeys +
		`},"required":["idContainer","idContainerVersion","kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,` +
		`"kind":{"type":"string"},"idTrigger":{"type":"integer"},"idVariable":{"type":"integer"},"references":{"type":"array",` +
		`"items":` + referenceEntrySchema + `},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","kind","references","has_more"],"additionalProperties":false}`),
	Arguments: append(append(entryArguments("", ""),
		capability.Argument{Name: "kind", Required: true, Description: "trigger asks for the tags that use a trigger, " +
			"variable for the tags, triggers, and variables that use a variable"},
		capability.Argument{Name: "idTrigger", Description: "Trigger ID from matomo.tag-manager-triggers.list; " +
			"required for kind trigger, refused for kind variable"},
		capability.Argument{Name: "idVariable", Description: "Variable ID from matomo.tag-manager-variables.list; " +
			"required for kind variable, refused for kind trigger"}), pagingArguments...),
	Fields: append(append(append([]capability.Field{}, versionScopeFields...), pagingFields...),
		capability.Field{Name: "kind", Description: "The kind asked for, trigger or variable"},
		capability.Field{Name: "idTrigger", Description: "The trigger asked for, for kind trigger"},
		capability.Field{Name: "idVariable", Description: "The variable asked for, for kind variable"},
		capability.Field{Name: "references", Description: "Places of the batch: id, type (tag, trigger, or " +
			"variable), type_name (translated for the token's user), and name; all untrusted text"},
		truncatedField),
	Examples: []capability.Example{{Description: "List the tags that use trigger 7 of version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"kind":"trigger","idTrigger":7}`)}},
}

// VariablesResult is the answer of tag-manager-variables.list.
type VariablesResult struct {
	IDSite             int           `json:"idSite"`
	IDContainer        string        `json:"idContainer"`
	IDContainerVersion int           `json:"idContainerVersion"`
	Variables          []ExportEntry `json:"variables"`
	HasMore            bool          `json:"has_more"`
	NextCursor         string        `json:"next_cursor,omitempty"`
	Truncated          bool          `json:"truncated,omitempty"`
}

// AvailableVariable is a configured or preconfigured variable of tag-manager-variables.list-available.
type AvailableVariable struct {
	ID              string `json:"id"`
	IDVariable      *int   `json:"idvariable,omitempty"`
	Type            string `json:"type,omitempty"`
	Name            string `json:"name,omitempty"`
	Category        string `json:"category,omitempty"`
	Description     string `json:"description,omitempty"`
	Order           *int   `json:"order,omitempty"`
	IsPreConfigured *bool  `json:"is_pre_configured,omitempty"`
}

// AvailableVariablesResult is the answer of tag-manager-variables.list-available.
type AvailableVariablesResult struct {
	IDSite             int                 `json:"idSite"`
	IDContainer        string              `json:"idContainer"`
	IDContainerVersion int                 `json:"idContainerVersion"`
	Variables          []AvailableVariable `json:"variables"`
	HasMore            bool                `json:"has_more"`
	NextCursor         string              `json:"next_cursor,omitempty"`
	Truncated          bool                `json:"truncated,omitempty"`
}

// Reference is a place that uses a trigger or a variable.
type Reference struct {
	ID       int    `json:"id"`
	Type     string `json:"type,omitempty"`
	TypeName string `json:"type_name,omitempty"`
	Name     string `json:"name,omitempty"`
}

// ReferencesResult is the answer of tag-manager-references.list.
type ReferencesResult struct {
	IDSite             int         `json:"idSite"`
	IDContainer        string      `json:"idContainer"`
	IDContainerVersion int         `json:"idContainerVersion"`
	Kind               string      `json:"kind"`
	IDTrigger          int         `json:"idTrigger,omitempty"`
	IDVariable         int         `json:"idVariable,omitempty"`
	References         []Reference `json:"references"`
	HasMore            bool        `json:"has_more"`
	NextCursor         string      `json:"next_cursor,omitempty"`
	Truncated          bool        `json:"truncated,omitempty"`
}

type availableJSON struct {
	ID              *loose `json:"id"`
	IDVariable      *loose `json:"idvariable"`
	Type            *loose `json:"type"`
	Name            *loose `json:"name"`
	Category        *loose `json:"category"`
	Description     *loose `json:"description"`
	Order           *loose `json:"order"`
	IsPreConfigured *loose `json:"is_pre_configured"`
}

type referenceJSON struct {
	ID       *loose `json:"referenceId"`
	Type     *loose `json:"referenceType"`
	TypeName *loose `json:"referenceTypeName"`
	Name     *loose `json:"referenceName"`
}

func invokeTagManagerVariablesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list Tag Manager variables"
	req, err := entryRequestOf(resolved, raw, "", tagManagerVariablesList.ID)
	if err != nil {
		return nil, err
	}
	answer, err := req.read(ctx, resolved, secrets, red, op, tagManagerVariablesList.ID, "TagManager.getContainerVariables", req.params())
	if err != nil {
		return nil, err
	}
	variables, next, truncated, err := req.listEntries(op, answer)
	if err != nil {
		return nil, err
	}
	return &VariablesResult{IDSite: req.site, IDContainer: req.container, IDContainerVersion: req.version,
		Variables: variables, HasMore: next != "", NextCursor: next, Truncated: truncated}, nil
}

func invokeTagManagerVariablesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get Tag Manager variable"
	req, err := entryRequestOf(resolved, raw, "idVariable", "")
	if err != nil {
		return nil, err
	}
	params := req.params()
	params.Set("idVariable", strconv.Itoa(req.object))
	return req.get(ctx, resolved, secrets, red, op, tagManagerVariablesGet.ID, "TagManager.getContainerVariable", params)
}

func invokeTagManagerVariablesListAvailable(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list available Tag Manager variables"
	req, err := entryRequestOf(resolved, raw, "", tagManagerVariablesListAvailable.ID)
	if err != nil {
		return nil, err
	}
	answer, err := req.read(ctx, resolved, secrets, red, op, tagManagerVariablesListAvailable.ID,
		"TagManager.getAvailableContainerVariables", req.params())
	if err != nil {
		return nil, err
	}
	invalid := provider.InvalidResponse(op, "Matomo returned an invalid list")
	groups, ok := jsonRows(answer)
	if !ok {
		return nil, invalid
	}
	// Matomo groups the variables by category; the tool flattens them in that order.
	var rows []availableJSON
	for _, group := range groups {
		var parsed struct {
			Types json.RawMessage `json:"types"`
		}
		if json.Unmarshal(group, &parsed) != nil {
			return nil, invalid
		}
		members, ok := jsonRows(parsed.Types)
		if !ok {
			return nil, invalid
		}
		for _, member := range members {
			var row availableJSON
			if json.Unmarshal(member, &row) != nil || row.ID == nil {
				return nil, invalid
			}
			rows = append(rows, row)
		}
	}
	batch, next := slice(req.paging, rows)
	budget := &exportBudget{remaining: maxExportText}
	out := make([]AvailableVariable, 0, len(batch))
	for _, row := range batch {
		item := AvailableVariable{ID: budget.text(row.ID.str(maxStringLength), maxStringLength),
			IDVariable: row.IDVariable.number(), Type: budget.text(row.Type.str(maxStringLength), maxStringLength),
			Name:        budget.text(row.Name.str(maxStringLength), maxStringLength),
			Category:    budget.text(row.Category.str(maxStringLength), maxStringLength),
			Description: budget.text(row.Description.str(maxStepText), maxStepText),
			Order:       row.Order.number(), IsPreConfigured: row.IsPreConfigured.flag()}
		out = append(out, item)
	}
	return &AvailableVariablesResult{IDSite: req.site, IDContainer: req.container, IDContainerVersion: req.version,
		Variables: out, HasMore: next != "", NextCursor: next, Truncated: budget.truncated}, nil
}

func invokeTagManagerReferencesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list Tag Manager references"
	req, err := entryRequestOf(resolved, raw, "reference", tagManagerReferencesList.ID)
	if err != nil {
		return nil, err
	}
	method, key := "TagManager.getContainerTriggerReferences", "idTrigger"
	if req.kind == "variable" {
		method, key = "TagManager.getContainerVariableReferences", "idVariable"
	}
	params := req.params()
	params.Set(key, strconv.Itoa(req.object))
	answer, err := req.read(ctx, resolved, secrets, red, op, tagManagerReferencesList.ID, method, params)
	if err != nil {
		return nil, err
	}
	invalid := provider.InvalidResponse(op, "Matomo returned an invalid list")
	rows, ok := jsonRows(answer)
	if !ok {
		return nil, invalid
	}
	parsed := make([]referenceJSON, 0, len(rows))
	for _, row := range rows {
		var reference referenceJSON
		if json.Unmarshal(row, &reference) != nil || reference.ID.number() == nil {
			return nil, invalid
		}
		parsed = append(parsed, reference)
	}
	batch, next := slice(req.paging, parsed)
	budget := &exportBudget{remaining: maxExportText}
	out := make([]Reference, 0, len(batch))
	for _, reference := range batch {
		out = append(out, Reference{ID: *reference.ID.number(),
			Type:     budget.text(reference.Type.str(maxStringLength), maxStringLength),
			TypeName: budget.text(reference.TypeName.str(maxStringLength), maxStringLength),
			Name:     budget.text(reference.Name.str(maxStringLength), maxStringLength)})
	}
	result := &ReferencesResult{IDSite: req.site, IDContainer: req.container, IDContainerVersion: req.version,
		Kind: req.kind, References: out, HasMore: next != "", NextCursor: next, Truncated: budget.truncated}
	if req.kind == "trigger" {
		result.IDTrigger = req.object
	} else {
		result.IDVariable = req.object
	}
	return result, nil
}
