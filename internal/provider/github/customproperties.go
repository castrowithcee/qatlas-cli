package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Repository and organization custom properties. Every call addresses exactly one of the two: repository,
// checked against the connection's repository targets the way every other repository tool is, or
// organization, checked against its owner targets the way github.rulesets.list checks one; giving both or
// neither is refused before a credential is resolved, the same exclusive selection github.rulesets.list uses.
// github.customproperties.get reads the custom property values of a repository or, with organization instead,
// the custom property definitions (schema) an organization declares. It never reads the values GitHub holds
// for the repositories of an organization: that route names repositories by a bare list of names the caller
// supplies, and binding that list to the connection's own repository targets would need a second, independent
// allow-list check this tool does not perform, so the narrower reading is a schema read only.
// github.customproperties.set changes them and is offered only where a connection's tools list names it,
// since a custom property can gate what a ruleset condition matches: for a repository it sets or removes
// property values, a null value removing one; for an organization it creates or updates property definitions,
// the smaller scope that already covers the official custom properties write for a single repository, since
// that is offered through the repository target above.

// customPropertySensitivity classifies a custom property as a governance setting of the repository or the
// organization it belongs to, the same way rulesetSensitivity does for a ruleset.
const customPropertySensitivity = "github-custom-property"

// maxCustomPropertiesInputBytes bounds the size of one github.customproperties.set request. maxCustomProperties
// bounds how many property values or property definitions it may hold; checkCustomPropertiesSize applies both,
// the way checkRulesetSize applies the bounds a JSON schema's maxItems cannot enforce by itself.
const (
	maxCustomPropertiesInputBytes = 32 << 10
	maxCustomProperties           = 100
)

const (
	propertyNamePattern = `^[A-Za-z0-9_-]{1,100}$`
	propertyNameSchema  = `{"type":"string","minLength":1,"maxLength":100,"pattern":"` + propertyNamePattern + `"}`
	propertyValueSchema = `{"anyOf":[{"type":"string","maxLength":1024},` +
		`{"type":"array","maxItems":50,"items":{"type":"string","maxLength":1024}},{"type":"null"}]}`
	propertyValueTypeSchema   = `{"type":"string","enum":["string","single_select","multi_select","true_false"]}`
	propertyEditableSchema    = `{"type":"string","enum":["org_actors","org_and_repo_actors"]}`
	propertyDescriptionSchema = `{"type":"string","maxLength":1024}`
	propertyAllowedSchema     = `{"type":"array","maxItems":200,"items":{"type":"string","maxLength":1024}}`
)

const customPropertyValueSchema = `{"type":"object","properties":{"property_name":` + propertyNameSchema +
	`,"value":` + propertyValueSchema + `},"required":["property_name","value"],"additionalProperties":false}`

const customPropertyValuesSchema = `{"type":"array","maxItems":100,"items":` + customPropertyValueSchema + `}`

const customPropertyDefinitionSchema = `{"type":"object","properties":{"property_name":` + propertyNameSchema +
	`,"value_type":` + propertyValueTypeSchema + `,"required":{"type":"boolean"},"default_value":` +
	propertyValueSchema + `,"description":` + propertyDescriptionSchema + `,"allowed_values":` +
	propertyAllowedSchema + `,"values_editable_by":` + propertyEditableSchema + `},` +
	`"required":["property_name","value_type"],"additionalProperties":false}`

const customPropertyDefinitionsSchema = `{"type":"array","maxItems":100,"items":` + customPropertyDefinitionSchema + `}`

const customPropertyValueOutput = `{"type":"object","properties":{"property_name":{"type":"string"},` +
	`"value":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}},{"type":"null"}]}},` +
	`"required":["property_name","value"],"additionalProperties":false}`

const customPropertyDefinitionOutput = `{"type":"object","properties":{"property_name":{"type":"string"},` +
	`"value_type":{"type":"string"},"required":{"type":"boolean"},"default_value":{"anyOf":[{"type":"string"},` +
	`{"type":"array","items":{"type":"string"}},{"type":"null"}]},"description":{"type":"string"},` +
	`"allowed_values":{"type":"array","items":{"type":"string"}},"values_editable_by":{"type":"string"}},` +
	`"required":["property_name","value_type"],"additionalProperties":false}`

const customPropertiesOutput = `{"type":"object","properties":{"properties":{"type":"array","items":` +
	customPropertyValueOutput + `},"schema":{"type":"array","items":` + customPropertyDefinitionOutput + `},` +
	`"repository":{"type":"string"},"organization":{"type":"string"}},"additionalProperties":false}`

// The exclusive target arguments of the custom properties tools, worded like rulesetRepositoryArgument and
// rulesetOrganizationArgument: exactly one of repository or organization is required, never both.
var customPropertiesRepositoryArgument = capability.Argument{Name: "repository", Description: "Repository as " +
	"OWNER/REPO whose custom property values this call addresses; exactly one of repository or organization is " +
	"required, never both; must lie inside the targets when the connection lists any"}

var customPropertiesOrganizationArgument = capability.Argument{Name: "organization", Description: "Organization " +
	"as orgs/LOGIN whose custom property schema this call addresses; exactly one of repository or organization " +
	"is required, never both; must be an owner target the connection allows when it lists any"}

var customPropertiesRepositoryField = capability.Field{Name: "repository", Description: "Repository this call " +
	"addressed, as OWNER/REPO; present only for a repository call"}

var customPropertiesOrganizationField = capability.Field{Name: "organization", Description: "Organization this " +
	"call addressed, as orgs/LOGIN; present only for an organization call"}

var customPropertiesFields = []capability.Field{
	{Name: "properties", Description: "Property values of the repository: property_name and value (a string, " +
		"an array of strings for a multi-select property, or null while unset); present only for a repository call"},
	{Name: "schema", Description: "Property definitions the organization declares: property_name, value_type " +
		"(string, single_select, multi_select, or true_false), required, default_value, description, " +
		"allowed_values, and values_editable_by; present only for an organization call"},
	customPropertiesRepositoryField, customPropertiesOrganizationField,
}

var customPropertiesGet = capability.Descriptor{
	ID:      Provider + ".customproperties.get",
	Version: 1,
	Title:   "Get GitHub custom properties",
	Description: "Read the custom property values of a repository or, with organization instead, the custom " +
		"property definitions (schema) an organization declares; exactly one of repository or organization is " +
		"required",
	Tags:                       []string{"github", "customproperties", "governance", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"repository":` + repoSchema + `,"organization":` + ownerSchema),
	OutputSchema:               json.RawMessage(customPropertiesOutput),
	Arguments:                  []capability.Argument{customPropertiesRepositoryArgument, customPropertiesOrganizationArgument},
	Fields:                     customPropertiesFields,
	Examples: []capability.Example{{
		Description: "Read the custom property values of a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}, {
		Description: "Read the custom property schema of an organization",
		Arguments:   json.RawMessage(`{"organization":"orgs/octo-org"}`),
	}},
}

var customPropertiesSet = capability.Descriptor{
	ID:      Provider + ".customproperties.set",
	Version: 1,
	Title:   "Set GitHub custom properties",
	Description: "Set or remove the custom property values of a repository with properties, or, with " +
		"organization and definitions instead, create or update the custom property definitions (schema) an " +
		"organization declares; exactly one of repository or organization is required, matched by exactly one " +
		"of properties or definitions; sent at most once. Offered only where a connection's tools list names " +
		"it, since a custom property can gate what a ruleset condition matches",
	Tags:                       []string{"github", "customproperties", "governance", "set"},
	Risk:                       guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, customPropertySensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: inputSchema(`"repository":` + repoSchema + `,"organization":` + ownerSchema +
		`,"properties":` + customPropertyValuesSchema + `,"definitions":` + customPropertyDefinitionsSchema),
	OutputSchema: json.RawMessage(customPropertiesOutput),
	Arguments: []capability.Argument{
		customPropertiesRepositoryArgument, customPropertiesOrganizationArgument,
		{Name: "properties", Description: "For a repository: property values to set, each property_name and " +
			"value (a string, an array of strings for a multi-select property, or null to remove the property); " +
			"required with repository, refused with organization"},
		{Name: "definitions", Description: "For an organization: property definitions to create or replace, each " +
			"property_name, value_type (string, single_select, multi_select, or true_false), required, " +
			"default_value, description, allowed_values (for single_select or multi_select), and " +
			"values_editable_by (org_actors or org_and_repo_actors); required with organization, refused with " +
			"repository"},
	},
	Fields: customPropertiesFields,
	Examples: []capability.Example{{
		Description: "Set a repository's team custom property and clear another",
		Arguments: json.RawMessage(`{"repository":"octo-org/example","properties":[` +
			`{"property_name":"team","value":"atlas"},{"property_name":"cost_center","value":null}]}`),
	}, {
		Description: "Create or update an organization property definition",
		Arguments: json.RawMessage(`{"organization":"orgs/octo-org","definitions":[{"property_name":"team",` +
			`"value_type":"single_select","required":false,"allowed_values":["atlas","hydra"]}]}`),
	}},
}

// customPropertiesOperations binds every custom property tool to its handler.
func customPropertiesOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: customPropertiesGet, Handler: capability.Handler(invokeCustomPropertiesGet)},
		{Descriptor: customPropertiesSet, Handler: capability.Handler(invokeCustomPropertiesSet)},
	}
}

// selectCustomPropertiesTarget mirrors selectRulesetTarget: a repository call needs repository, an
// organization call needs organization, and never both or neither, checked before a credential is resolved.
func selectCustomPropertiesTarget(resolved *config.Resolved, raw json.RawMessage) (target, error) {
	if resolved == nil {
		return target{}, providerError("open", "no connection was selected")
	}
	var arguments struct {
		Repository   string `json:"repository"`
		Organization string `json:"organization"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return target{}, unreadable("select custom properties target")
	}
	hasRepository := strings.TrimSpace(arguments.Repository) != ""
	hasOrganization := strings.TrimSpace(arguments.Organization) != ""
	if hasRepository == hasOrganization {
		return target{}, invalidRequest("give exactly one of repository or organization, never both or neither")
	}
	if hasRepository {
		return selectTarget(resolved, kindRepository, raw)
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return target{}, providerError("open", err.Error())
	}
	bound, _, err := parseAllowedOrganization(allowed, "organization", arguments.Organization)
	return bound, err
}

// customPropertiesLocate adds the repository or the organization a custom properties tool addressed to its
// successful result, under that argument's own name, the way target.rulesetLocate does for a ruleset.
func (t target) customPropertiesLocate(value any, err error) (any, error) {
	if err != nil {
		return value, err
	}
	encoded, merr := json.Marshal(value)
	var fields map[string]json.RawMessage
	if merr != nil || json.Unmarshal(encoded, &fields) != nil || fields == nil {
		return value, nil
	}
	name := "repository"
	if t.kind == kindOwner {
		name = "organization"
	}
	fields[name], _ = json.Marshal(t.argument())
	return fields, nil
}

// Permission messages of the custom properties tools. GitHub decides on every request; a message names what
// such a request needs without claiming what the configured token holds.
func customPropertiesReadPermission(t target) string {
	if t.kind == kindOwner {
		return "GitHub refused this token the custom property schema of this organization; reading it needs " +
			"the organization permission Custom properties: read on a fine-grained token, or, as far as GitHub " +
			"documents it, admin:org on a classic token"
	}
	return "GitHub refused this token the custom property values of this repository; reading them needs " +
		"Custom properties: read on a fine-grained token, or repo on a classic token"
}

func customPropertiesChangePermission(t target) string {
	if t.kind == kindOwner {
		return "GitHub refused this change of an organization's custom property schema; it needs the " +
			"organization permission Custom properties: write on a fine-grained token, or, as far as GitHub " +
			"documents it, admin:org on a classic token"
	}
	return "GitHub refused this change of a repository's custom property values; it needs Custom properties: " +
		"write on a fine-grained token, or repo on a classic token"
}

// checkCustomPropertiesSize applies the bounds a JSON schema's maxItems cannot enforce by itself: how many
// property values or property definitions one request may hold, and the size of the whole request.
func checkCustomPropertiesSize(raw, properties, definitions json.RawMessage) error {
	if len(raw) > maxCustomPropertiesInputBytes {
		return invalidRequest(fmt.Sprintf("the request must be at most %d KiB", maxCustomPropertiesInputBytes>>10))
	}
	if n := jsonArrayLen(properties); n > maxCustomProperties {
		return invalidRequest(fmt.Sprintf("properties accepts at most %d entries", maxCustomProperties))
	}
	if n := jsonArrayLen(definitions); n > maxCustomProperties {
		return invalidRequest(fmt.Sprintf("definitions accepts at most %d entries", maxCustomProperties))
	}
	return nil
}

// CustomPropertyValue is one property value of a repository. Value is GitHub's own shape, a JSON string, an
// array of strings for a multi-select property, or null while the property is unset.
type CustomPropertyValue struct {
	PropertyName string          `json:"property_name"`
	Value        json.RawMessage `json:"value"`
}

// CustomPropertyDefinition is one property definition (schema) an organization declares.
type CustomPropertyDefinition struct {
	PropertyName     string          `json:"property_name"`
	ValueType        string          `json:"value_type"`
	Required         bool            `json:"required,omitempty"`
	DefaultValue     json.RawMessage `json:"default_value,omitempty"`
	Description      string          `json:"description,omitempty"`
	AllowedValues    []string        `json:"allowed_values,omitempty"`
	ValuesEditableBy string          `json:"values_editable_by,omitempty"`
}

// CustomPropertiesResult is the answer of both custom properties tools: properties for a repository call, or
// schema for an organization call, never both.
type CustomPropertiesResult struct {
	Properties []CustomPropertyValue      `json:"properties,omitempty"`
	Schema     []CustomPropertyDefinition `json:"schema,omitempty"`
}

// normalizedPropertyValue reports the value GitHub sent as a usable JSON value: a string, an array of
// strings, or null; it refuses anything else.
func normalizedPropertyValue(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage("null"), true
	}
	switch trimmed[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, false
		}
		return raw, true
	case '[':
		var items []string
		if json.Unmarshal(raw, &items) != nil {
			return nil, false
		}
		return raw, true
	}
	return nil, false
}

// customPropertyDefinitionJSON is the property definition GitHub answers with, decoded once and turned into
// CustomPropertyDefinition by view.
type customPropertyDefinitionJSON struct {
	PropertyName     string          `json:"property_name"`
	ValueType        string          `json:"value_type"`
	Required         bool            `json:"required"`
	DefaultValue     json.RawMessage `json:"default_value"`
	Description      string          `json:"description"`
	AllowedValues    []string        `json:"allowed_values"`
	ValuesEditableBy string          `json:"values_editable_by"`
}

func (a customPropertyDefinitionJSON) view() (CustomPropertyDefinition, bool) {
	if a.PropertyName == "" || a.ValueType == "" {
		return CustomPropertyDefinition{}, false
	}
	defaultValue, ok := normalizedPropertyValue(a.DefaultValue)
	if !ok {
		return CustomPropertyDefinition{}, false
	}
	if string(defaultValue) == "null" {
		defaultValue = nil
	}
	return CustomPropertyDefinition{PropertyName: a.PropertyName, ValueType: a.ValueType, Required: a.Required,
		DefaultValue: defaultValue, Description: a.Description, AllowedValues: a.AllowedValues,
		ValuesEditableBy: a.ValuesEditableBy}, true
}

func invokeCustomPropertiesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := selectCustomPropertiesTarget(resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	if bound.kind == kindOwner {
		return bound.customPropertiesLocate(client.getOrganizationPropertySchema(ctx))
	}
	return bound.customPropertiesLocate(client.getRepositoryPropertyValues(ctx))
}

// getRepositoryPropertyValues reads the custom property values of the bound repository.
func (c *Client) getRepositoryPropertyValues(ctx context.Context) (*CustomPropertiesResult, error) {
	const op = "get repository custom property values"
	var raw []struct {
		PropertyName string          `json:"property_name"`
		Value        json.RawMessage `json:"value"`
	}
	if err := c.rest(ctx, op, c.repoPath("properties/values"), &raw); err != nil {
		return nil, actionsFailure(err, customPropertiesReadPermission(c.target))
	}
	result := &CustomPropertiesResult{Properties: make([]CustomPropertyValue, 0, len(raw))}
	for _, entry := range raw {
		if entry.PropertyName == "" {
			return nil, invalidEntry(op, "a custom property value")
		}
		value, ok := normalizedPropertyValue(entry.Value)
		if !ok {
			return nil, invalidResponse(op, false)
		}
		result.Properties = append(result.Properties, CustomPropertyValue{PropertyName: entry.PropertyName, Value: value})
	}
	return result, nil
}

// getOrganizationPropertySchema reads the custom property definitions the bound organization declares.
func (c *Client) getOrganizationPropertySchema(ctx context.Context) (*CustomPropertiesResult, error) {
	const op = "get organization custom property schema"
	var raw []customPropertyDefinitionJSON
	if err := c.rest(ctx, op, c.orgPath("properties/schema"), &raw); err != nil {
		return nil, actionsFailure(err, customPropertiesReadPermission(c.target))
	}
	result := &CustomPropertiesResult{Schema: make([]CustomPropertyDefinition, 0, len(raw))}
	for _, entry := range raw {
		def, ok := entry.view()
		if !ok {
			return nil, invalidEntry(op, "a custom property definition")
		}
		result.Schema = append(result.Schema, def)
	}
	return result, nil
}

// customPropertiesSetArguments holds the arguments of github.customproperties.set: exactly one of properties
// (for a repository) or definitions (for an organization) applies, matching the exclusive target.
type customPropertiesSetArguments struct {
	Properties  []CustomPropertyValue      `json:"properties"`
	Definitions []CustomPropertyDefinition `json:"definitions"`
}

func invokeCustomPropertiesSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var sizes struct {
		Properties  json.RawMessage `json:"properties"`
		Definitions json.RawMessage `json:"definitions"`
	}
	if json.Unmarshal(raw, &sizes) != nil {
		return nil, unreadable(customPropertiesSet.ID)
	}
	if err := checkCustomPropertiesSize(raw, sizes.Properties, sizes.Definitions); err != nil {
		return nil, err
	}
	var arguments customPropertiesSetArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(customPropertiesSet.ID)
	}
	bound, err := selectCustomPropertiesTarget(resolved, raw)
	if err != nil {
		return nil, err
	}
	if bound.kind == kindOwner {
		if len(arguments.Properties) > 0 {
			return nil, invalidRequest("properties applies only to a repository; give definitions for an organization")
		}
		if len(arguments.Definitions) == 0 {
			return nil, invalidRequest("definitions is required for an organization")
		}
	} else {
		if len(arguments.Definitions) > 0 {
			return nil, invalidRequest("definitions applies only to an organization; give properties for a repository")
		}
		if len(arguments.Properties) == 0 {
			return nil, invalidRequest("properties is required for a repository")
		}
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	if bound.kind == kindOwner {
		return bound.customPropertiesLocate(client.setOrganizationPropertySchema(ctx, arguments.Definitions))
	}
	return bound.customPropertiesLocate(client.setRepositoryPropertyValues(ctx, arguments.Properties))
}

// propertyValuesBody turns the given property values into the REST body github.customproperties.set sends:
// every value as given, including an explicit null, which GitHub reads as removing that property.
func propertyValuesBody(properties []CustomPropertyValue) []map[string]any {
	body := make([]map[string]any, 0, len(properties))
	for _, p := range properties {
		var value any
		trimmed := bytes.TrimSpace(p.Value)
		if len(trimmed) > 0 && string(trimmed) != "null" {
			_ = json.Unmarshal(p.Value, &value)
		}
		body = append(body, map[string]any{"property_name": p.PropertyName, "value": value})
	}
	return body
}

// setRepositoryPropertyValues sends every given property value in one PATCH that is never repeated: GitHub
// answers this route with no body, so the result echoes the values as sent.
func (c *Client) setRepositoryPropertyValues(ctx context.Context, properties []CustomPropertyValue) (*CustomPropertiesResult, error) {
	const op = "set repository custom property values"
	body := map[string]any{"properties": propertyValuesBody(properties)}
	if err := c.restChange(ctx, op, http.MethodPatch, c.repoPath("properties/values"), body, nil); err != nil {
		return nil, actionsFailure(err, customPropertiesChangePermission(c.target))
	}
	return &CustomPropertiesResult{Properties: properties}, nil
}

// definitionsBody turns the given property definitions into the REST body github.customproperties.set sends.
func definitionsBody(definitions []CustomPropertyDefinition) []map[string]any {
	body := make([]map[string]any, 0, len(definitions))
	for _, d := range definitions {
		entry := map[string]any{"property_name": d.PropertyName, "value_type": d.ValueType}
		if d.Required {
			entry["required"] = true
		}
		if trimmed := bytes.TrimSpace(d.DefaultValue); len(trimmed) > 0 && string(trimmed) != "null" {
			var value any
			if json.Unmarshal(d.DefaultValue, &value) == nil {
				entry["default_value"] = value
			}
		}
		if d.Description != "" {
			entry["description"] = d.Description
		}
		if len(d.AllowedValues) > 0 {
			entry["allowed_values"] = d.AllowedValues
		}
		if d.ValuesEditableBy != "" {
			entry["values_editable_by"] = d.ValuesEditableBy
		}
		body = append(body, entry)
	}
	return body
}

// setOrganizationPropertySchema creates or updates every given property definition of the bound organization
// in one PATCH that is never repeated.
func (c *Client) setOrganizationPropertySchema(ctx context.Context, definitions []CustomPropertyDefinition) (*CustomPropertiesResult, error) {
	const op = "set organization custom property schema"
	body := map[string]any{"properties": definitionsBody(definitions)}
	var raw []customPropertyDefinitionJSON
	if err := c.restChange(ctx, op, http.MethodPatch, c.orgPath("properties/schema"), body, &raw); err != nil {
		return nil, actionsFailure(err, customPropertiesChangePermission(c.target))
	}
	result := &CustomPropertiesResult{Schema: make([]CustomPropertyDefinition, 0, len(raw))}
	for _, entry := range raw {
		def, ok := entry.view()
		if !ok {
			return nil, invalidEntry(op, "a custom property definition")
		}
		result.Schema = append(result.Schema, def)
	}
	return result, nil
}

// customPropertiesSubject names the custom property values a repository path below /properties addresses. It
// is empty for any other path, such as the repository route restSubject already names.
func customPropertiesSubject(path string) string {
	if path == "properties/values" {
		return "the custom property values"
	}
	return ""
}
