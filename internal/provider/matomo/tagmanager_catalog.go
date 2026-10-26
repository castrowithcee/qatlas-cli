package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxTemplateText     = 2000
	maxTemplateParams   = 100
	maxParamChoices     = 100
	maxTemplateContexts = 20
	maxParamValueText   = 512
)

// contextPattern is the form of a context ID: Matomo stores it in ten characters.
var contextPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,9}$`)

// catalogKind is one fixed TagManager method of the tool. needsContext and needsSite name the argument the
// method takes; templates marks the kinds that answer template metadata grouped by category.
type catalogKind struct {
	name, method            string
	needsContext, needsSite bool
	templates               bool
}

var catalogKinds = []catalogKind{
	{name: "contexts", method: "TagManager.getAvailableContexts"},
	{name: "environments", method: "TagManager.getAvailableEnvironments"},
	{name: "publishable-environments", method: "TagManager.getAvailableEnvironmentsWithPublishCapability", needsSite: true},
	{name: "fire-limits", method: "TagManager.getAvailableTagFireLimits"},
	{name: "comparisons", method: "TagManager.getAvailableComparisons"},
	{name: "tag-types", method: "TagManager.getAvailableTagTypesInContext", needsContext: true, templates: true},
	{name: "trigger-types", method: "TagManager.getAvailableTriggerTypesInContext", needsContext: true, templates: true},
	{name: "variable-types", method: "TagManager.getAvailableVariableTypesInContext", needsContext: true, templates: true},
}

func catalogKindNames() []string {
	names := make([]string, len(catalogKinds))
	for i, k := range catalogKinds {
		names[i] = k.name
	}
	return names
}

func catalogKindOf(name string) (catalogKind, bool) {
	i := slices.IndexFunc(catalogKinds, func(k catalogKind) bool { return k.name == name })
	if i < 0 {
		return catalogKind{}, false
	}
	return catalogKinds[i], true
}

const (
	catalogParameterProperties = `"name":{"type":"string"},"title":{"type":"string"},"type":{"type":"string"},` +
		`"ui_control":{"type":"string"},"description":{"type":"string"},"inline_help":{"type":"string"},` +
		`"introduction":{"type":"string"},"default_value":{"type":"string"},"condition":{"type":"string"},` +
		`"ui_control_attributes":{"type":"string"},"available_values":{"type":"array","items":{"type":"object",` +
		`"properties":{"value":{"type":"string"},"label":{"type":"string"}},"additionalProperties":false}}`
	catalogEntryProperties = `"id":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},` +
		`"category":{"type":"string"},"icon":{"type":"string"},"help":{"type":"string"},"order":{"type":"integer"},` +
		`"contexts":{"type":"array","items":{"type":"string"}},"has_advanced_settings":{"type":"boolean"},` +
		`"is_custom_template":{"type":"boolean"},"parameters":{"type":"array","items":{"type":"object",` +
		`"properties":{` + catalogParameterProperties + `},"additionalProperties":false}}`
)

var tagManagerCatalogList = capability.Descriptor{
	ID: Provider + ".tag-manager-catalog.list", Version: 1, Title: "List the Matomo Tag Manager catalog",
	Description: "Read what Tag Manager offers, in batches. kind contexts lists the contexts that have tag types " +
		"(getAvailableContexts), environments the environments a container can be released to " +
		"(getAvailableEnvironments), publishable-environments those the token may publish to on a website " +
		"(getAvailableEnvironmentsWithPublishCapability, needs idSite), fire-limits the tag fire limits, " +
		"comparisons the comparison operators of trigger conditions, and tag-types, trigger-types, and " +
		"variable-types the templates of a context with their parameters (idContext is required). Needs the " +
		"plugin TagManager and a view right; names and texts are untrusted",
	Tags: []string{"matomo", "tag-manager", "catalog", "tags", "triggers", "variables", "list"},
	Risk: readRisk(tagManagerSensitive), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":[` +
		quotedList(catalogKindNames()) + `]},` + idSiteSchema + `,` +
		`"idContext":{"type":"string","pattern":"^[a-z][a-z0-9]{0,9}$","maxLength":10},` + pagingKeys +
		`},"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},` +
		`"idSite":{"type":"integer"},"idContext":{"type":"string"},"entries":{"type":"array","items":{"type":"object",` +
		`"properties":{` + catalogEntryProperties + `},"required":["id"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["kind","entries","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "kind", Required: true, Description: "What to read: " + strings.Join(catalogKindNames(), ", ")},
		{Name: "idSite", Description: "Website ID from matomo.sites.list; for publishable-environments only, and " +
			"optional there when the connection binds exactly one website; must lie inside the connection's targets",
			Target: "site"},
		{Name: "idContext", Description: "Context ID such as web, android, or ios, from kind contexts; required for " +
			"tag-types, trigger-types, and variable-types, refused for the others"}}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "kind", Description: "The kind read"},
		{Name: "idSite", Description: "The website read, for publishable-environments"},
		{Name: "idContext", Description: "The context read, for the template kinds"},
		{Name: "entries", Description: "Entries in the order Matomo lists them; the template kinds are flattened " +
			"from their categories"},
		{Name: "entries[].id", Description: "ID of the context, environment, fire limit, comparison, or template"},
		{Name: "entries[].name", Description: "Name as Matomo labels it for the token's user, untrusted"},
		{Name: "entries[].description", Description: "Template kinds: what the template does, untrusted"},
		{Name: "entries[].category", Description: "Template kinds: category of the template, untrusted"},
		{Name: "entries[].icon", Description: "Template kinds: icon path in the Matomo interface, untrusted text that " +
			"is never fetched"},
		{Name: "entries[].help", Description: "Template kinds: help text, untrusted"},
		{Name: "entries[].order", Description: "Template kinds: sort position within the category"},
		{Name: "entries[].contexts", Description: "Template kinds: contexts the template supports"},
		{Name: "entries[].has_advanced_settings", Description: "Template kinds: true when the template has advanced settings"},
		{Name: "entries[].is_custom_template", Description: "Template kinds: true for a custom template"},
		{Name: "entries[].parameters", Description: "Template kinds: at most 100 parameters of the template"},
		{Name: "entries[].parameters[].name", Description: "Parameter name, as the parameters of a tag, trigger, or variable use it"},
		{Name: "entries[].parameters[].title", Description: "Parameter label, untrusted"},
		{Name: "entries[].parameters[].type", Description: "Value type of the parameter"},
		{Name: "entries[].parameters[].ui_control", Description: "Form control Matomo shows for the parameter"},
		{Name: "entries[].parameters[].description", Description: "Parameter description, untrusted"},
		{Name: "entries[].parameters[].inline_help", Description: "Parameter help text, untrusted"},
		{Name: "entries[].parameters[].introduction", Description: "Heading above the parameter, untrusted"},
		{Name: "entries[].parameters[].default_value", Description: "Default value as text (other types as JSON), untrusted"},
		{Name: "entries[].parameters[].condition", Description: "Expression that decides when the parameter is shown, untrusted"},
		{Name: "entries[].parameters[].ui_control_attributes", Description: "Attributes of the form control as JSON text, untrusted"},
		{Name: "entries[].parameters[].available_values", Description: "Values the parameter offers, at most 100"},
		{Name: "entries[].parameters[].available_values[].value", Description: "Value to use"},
		{Name: "entries[].parameters[].available_values[].label", Description: "Label of the value, untrusted"},
	}, pagingFields...),
	Examples: []capability.Example{
		{Description: "List the contexts", Arguments: json.RawMessage(`{"kind":"contexts"}`)},
		{Description: "List the tag types of the web context", Arguments: json.RawMessage(`{"kind":"tag-types","idContext":"web"}`)},
	},
}

// CatalogChoice is one value a template parameter offers.
type CatalogChoice struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// CatalogParameter is a parameter of a template. All text is untrusted provider data.
type CatalogParameter struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Type         string          `json:"type,omitempty"`
	UIControl    string          `json:"ui_control,omitempty"`
	Description  string          `json:"description,omitempty"`
	InlineHelp   string          `json:"inline_help,omitempty"`
	Introduction string          `json:"introduction,omitempty"`
	DefaultValue string          `json:"default_value,omitempty"`
	Condition    string          `json:"condition,omitempty"`
	UIAttributes string          `json:"ui_control_attributes,omitempty"`
	Available    []CatalogChoice `json:"available_values,omitempty"`
}

// CatalogEntry is an entry of the catalog: a context, environment, fire limit, comparison, or template.
type CatalogEntry struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name,omitempty"`
	Description         string             `json:"description,omitempty"`
	Category            string             `json:"category,omitempty"`
	Icon                string             `json:"icon,omitempty"`
	Help                string             `json:"help,omitempty"`
	Order               *int               `json:"order,omitempty"`
	Contexts            []string           `json:"contexts,omitempty"`
	HasAdvancedSettings *bool              `json:"has_advanced_settings,omitempty"`
	IsCustomTemplate    *bool              `json:"is_custom_template,omitempty"`
	Parameters          []CatalogParameter `json:"parameters,omitempty"`
}

// CatalogResult is the answer of tag-manager-catalog.list.
type CatalogResult struct {
	Kind       string         `json:"kind"`
	IDSite     int            `json:"idSite,omitempty"`
	IDContext  string         `json:"idContext,omitempty"`
	Entries    []CatalogEntry `json:"entries"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type catalogRowJSON struct {
	ID   *loose `json:"id"`
	Name *loose `json:"name"`
}

type templateJSON struct {
	ID          *loose          `json:"id"`
	Name        *loose          `json:"name"`
	Description *loose          `json:"description"`
	Category    *loose          `json:"category"`
	Icon        *loose          `json:"icon"`
	Help        *loose          `json:"help"`
	Order       *loose          `json:"order"`
	Contexts    []loose         `json:"contexts"`
	Advanced    *loose          `json:"hasAdvancedSettings"`
	Custom      *loose          `json:"isCustomTemplate"`
	Parameters  []parameterJSON `json:"parameters"`
}

type parameterJSON struct {
	Name         *loose          `json:"name"`
	Title        *loose          `json:"title"`
	Type         *loose          `json:"type"`
	UIControl    *loose          `json:"uiControl"`
	Description  *loose          `json:"description"`
	InlineHelp   *loose          `json:"inlineHelp"`
	Introduction *loose          `json:"introduction"`
	Value        json.RawMessage `json:"value"`
	Default      json.RawMessage `json:"defaultValue"`
	Condition    *loose          `json:"condition"`
	Attributes   json.RawMessage `json:"uiControlAttributes"`
	Available    json.RawMessage `json:"availableValues"`
}

// rawText renders any JSON value as bounded text: a string as itself, null as nothing, the rest as compact JSON.
func rawText(raw json.RawMessage, limit int) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if raw[0] == '"' && json.Unmarshal(raw, &text) == nil {
		return boundString(strings.TrimSpace(text), limit)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return ""
	}
	return boundString(compact.String(), limit)
}

// choices reads availableValues, an object of value and label, in the order of the document.
func choices(raw json.RawMessage) []CatalogChoice {
	raw = bytes.TrimSpace(raw)
	if !bytes.HasPrefix(raw, []byte("{")) {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil
	}
	var out []CatalogChoice
	for decoder.More() && len(out) < maxParamChoices {
		key, err := decoder.Token()
		if err != nil {
			return out
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil && err != io.EOF {
			return out
		}
		name, _ := key.(string)
		out = append(out, CatalogChoice{Value: boundString(name, maxParamValueText), Label: rawText(value, maxStringLength)})
	}
	return out
}

func (p parameterJSON) parameter() (CatalogParameter, bool) {
	name := p.Name.str(maxStringLength)
	if name == "" {
		return CatalogParameter{}, false
	}
	value := p.Default
	if len(bytes.TrimSpace(value)) == 0 {
		value = p.Value
	}
	return CatalogParameter{Name: name, Title: p.Title.str(maxStringLength), Type: p.Type.str(maxStringLength),
		UIControl: p.UIControl.str(maxStringLength), Description: p.Description.str(maxTemplateText),
		InlineHelp: p.InlineHelp.str(maxTemplateText), Introduction: p.Introduction.str(maxTemplateText),
		DefaultValue: rawText(value, maxParamValueText), Condition: p.Condition.str(maxStringLength),
		UIAttributes: rawText(p.Attributes, maxParamValueText), Available: choices(p.Available)}, true
}

func (t templateJSON) entry(category string) (CatalogEntry, bool) {
	id := t.ID.str(maxStringLength)
	if id == "" {
		return CatalogEntry{}, false
	}
	own := t.Category.str(maxStringLength)
	if own == "" {
		own = category
	}
	out := CatalogEntry{ID: id, Name: t.Name.str(maxStringLength), Description: t.Description.str(maxTemplateText),
		Category: own, Icon: t.Icon.str(maxUrlLength), Help: t.Help.str(maxTemplateText), Order: t.Order.number(),
		HasAdvancedSettings: t.Advanced.flag(), IsCustomTemplate: t.Custom.flag()}
	for i := range t.Contexts {
		if len(out.Contexts) == maxTemplateContexts {
			break
		}
		if context := t.Contexts[i].str(maxStringLength); context != "" {
			out.Contexts = append(out.Contexts, context)
		}
	}
	for _, row := range t.Parameters {
		if len(out.Parameters) == maxTemplateParams {
			break
		}
		if parameter, ok := row.parameter(); ok {
			out.Parameters = append(out.Parameters, parameter)
		}
	}
	return out, true
}

// readCatalog reads the answer of one kind into entries; ok is false for an answer of the wrong shape.
func readCatalog(kind catalogKind, answer json.RawMessage) ([]CatalogEntry, bool) {
	rows, ok := jsonRows(answer)
	if !ok {
		return nil, false
	}
	entries := []CatalogEntry{}
	for _, row := range rows {
		if !kind.templates {
			var parsed catalogRowJSON
			if json.Unmarshal(row, &parsed) != nil {
				return nil, false
			}
			if id := parsed.ID.str(maxStringLength); id != "" {
				entries = append(entries, CatalogEntry{ID: id, Name: parsed.Name.str(maxStringLength)})
			}
			continue
		}
		var group struct {
			Name  *loose         `json:"name"`
			Types []templateJSON `json:"types"`
		}
		if json.Unmarshal(row, &group) != nil {
			return nil, false
		}
		for _, template := range group.Types {
			if entry, ok := template.entry(group.Name.str(maxStringLength)); ok {
				entries = append(entries, entry)
			}
		}
	}
	return entries, true
}

func invokeTagManagerCatalogList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list Tag Manager catalog"
	var args struct {
		Kind      string `json:"kind"`
		IDSite    *int   `json:"idSite"`
		IDContext string `json:"idContext"`
		Limit     int    `json:"limit"`
		Cursor    string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	kind, found := catalogKindOf(args.Kind)
	if !found {
		return nil, invalidArgument("kind must be one of " + strings.Join(catalogKindNames(), ", "))
	}
	if !kind.needsSite && args.IDSite != nil {
		return nil, invalidArgument("idSite belongs to kind publishable-environments")
	}
	if kind.needsContext && !contextPattern.MatchString(args.IDContext) {
		return nil, invalidArgument("idContext must be a context ID such as web, android, or ios")
	}
	if !kind.needsContext && args.IDContext != "" {
		return nil, invalidArgument("idContext belongs to the kinds tag-types, trigger-types, and variable-types")
	}
	site := 0
	if kind.needsSite {
		id, err := selectSite(resolved, args.IDSite)
		if err != nil {
			return nil, err
		}
		site = id
	}
	binding := provider.CursorBinding(tagManagerCatalogList.ID, scopeBinding(resolved), kind.name, site, args.IDContext)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	if kind.needsSite {
		params.Set("idSite", itoa(site))
	}
	if kind.needsContext {
		params.Set("idContext", args.IDContext)
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, tagManagerRequirement(tagManagerCatalogList.ID), kind.method, params, &answer); err != nil {
		return nil, err
	}
	entries, ok := readCatalog(kind, answer)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid Tag Manager catalog")
	}
	batch, next := slice(paging, entries)
	if batch == nil {
		batch = []CatalogEntry{}
	}
	return &CatalogResult{Kind: kind.name, IDSite: site, IDContext: args.IDContext, Entries: batch,
		HasMore: next != "", NextCursor: next}, nil
}
