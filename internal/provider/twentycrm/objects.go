package twentycrm

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	companiesGroup = "companies"
	objectsGroup   = "objects"
)

// toolGroups are the subject areas Twenty's tools are sorted into for display.
var toolGroups = []config.ToolGroup{
	{ID: companiesGroup, Title: "Companies", Description: "Companies of the workspace"},
	{ID: objectsGroup, Title: "Objects", Description: "Objects of the workspace and their fields"},
	{ID: recordsGroup, Title: "Records", Description: "Records of the reachable objects of the workspace"},
}

func inGroup(d capability.Descriptor, group string) capability.Descriptor {
	d.Group = group
	return d
}

const objectNameSchema = `{"type":"string","minLength":1,"maxLength":63,"pattern":"^[a-z][A-Za-z0-9]{0,62}$"}`

var objectsList = capability.Descriptor{
	ID:      Provider + ".objects.list",
	Version: 1,
	Title:   "List Twenty CRM objects",
	Description: "List the objects of the Twenty workspace of a connection that the connection reaches, " +
		"with their singular and plural API names and their number of fields",
	Tags:        []string{"twentycrm", "crm", "objects", "list", "schema"},
	Risk:        twentyReadRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"objects":{"type":"array","items":{` +
		`"type":"object","properties":{"name":{"type":"string"},"plural":{"type":"string"},` +
		`"field_count":{"type":"integer"}},"required":["name","plural","field_count"],` +
		`"additionalProperties":false}}},"required":["objects"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "objects", Description: "The reachable objects sorted by name, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "List the objects this connection reaches",
		Arguments:   json.RawMessage(`{}`),
	}},
}

var objectsGet = capability.Descriptor{
	ID:      Provider + ".objects.get",
	Version: 1,
	Title:   "Get a Twenty CRM object",
	Description: "Read the fields of one object of the Twenty workspace of a connection that the connection " +
		"reaches: JSON type, subfields of composite fields, whether a value is required or writable, and the " +
		"reachable object a relation points to",
	Tags:     []string{"twentycrm", "crm", "objects", "get", "schema", "fields"},
	Risk:     twentyReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `},` +
		`"required":["object"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
		`"plural":{"type":"string"},"fields":{"type":"array","items":{"type":"object","properties":{` +
		`"name":{"type":"string"},"type":{"type":"string"},"format":{"type":"string"},` +
		`"required":{"type":"boolean"},"writable":{"type":"boolean"},` +
		`"subfields":{"type":"array","items":{"type":"object","properties":{` +
		`"name":{"type":"string"},"type":{"type":"string"}},"required":["name"],"additionalProperties":false}},` +
		`"relation":{"type":"string"}},"required":["name","required","writable"],"additionalProperties":false}}},` +
		`"required":["name","plural","fields"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "name", Description: "Singular API name of the object"},
		{Name: "plural", Description: "Plural API name of the object"},
		{Name: "fields", Description: "The fields of the object sorted by name, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "Read the fields of the person object",
		Arguments:   json.RawMessage(`{"object":"person"}`),
	}},
}

// ObjectSummary is one reachable object in a listing.
type ObjectSummary struct {
	Name       string `json:"name"`
	Plural     string `json:"plural"`
	FieldCount int    `json:"field_count"`
}

// ObjectList is the result of ListObjects.
type ObjectList struct {
	Objects []ObjectSummary `json:"objects"`
}

// SubfieldInfo is one part of a composite field.
type SubfieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// FieldInfo is one field of an object.
type FieldInfo struct {
	Name      string         `json:"name"`
	Type      string         `json:"type,omitempty"`
	Format    string         `json:"format,omitempty"`
	Required  bool           `json:"required"`
	Writable  bool           `json:"writable"`
	Subfields []SubfieldInfo `json:"subfields,omitempty"`
	Relation  string         `json:"relation,omitempty"`
}

// ObjectInfo is the result of GetObject.
type ObjectInfo struct {
	Name   string      `json:"name"`
	Plural string      `json:"plural"`
	Fields []FieldInfo `json:"fields"`
}

// ListObjects returns the objects the connection reaches. It reads the workspace document at most once.
func (c *Client) ListObjects(ctx context.Context) (*ObjectList, error) {
	cat, err := c.workspaceCatalog(ctx, "list objects")
	if err != nil {
		return nil, err
	}
	reachable := cat.reachable(c.scope)
	result := &ObjectList{Objects: make([]ObjectSummary, 0, len(reachable))}
	for _, object := range reachable {
		result.Objects = append(result.Objects, ObjectSummary{Name: object.Name, Plural: object.Plural, FieldCount: len(object.Fields)})
	}
	return result, nil
}

// GetObject returns the fields of one reachable object. Relations to objects the connection cannot reach
// are left out.
func (c *Client) GetObject(ctx context.Context, name string) (*ObjectInfo, error) {
	if !c.scope.allows(name) {
		return nil, invalidRequest(errObjectUnavailable)
	}
	cat, err := c.workspaceCatalog(ctx, "get object")
	if err != nil {
		return nil, err
	}
	if _, err := cat.resolve(c.scope, name); err != nil {
		return nil, err
	}
	for _, object := range cat.reachable(c.scope) {
		if object.Name != name {
			continue
		}
		info := &ObjectInfo{Name: object.Name, Plural: object.Plural, Fields: make([]FieldInfo, 0, len(object.Fields))}
		for _, field := range object.Fields {
			item := FieldInfo{Name: field.Name, Type: field.Type, Format: field.Format, Required: field.Required,
				Writable: field.Writable, Relation: field.Relation}
			for _, sub := range field.Subfields {
				item.Subfields = append(item.Subfields, SubfieldInfo{Name: sub.Name, Type: sub.Type})
			}
			info.Fields = append(info.Fields, item)
		}
		return info, nil
	}
	return nil, invalidRequest(errObjectUnavailable)
}

func invokeObjectsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListObjects(ctx)
}

func invokeObjectsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Object string `json:"object"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get object", "the validated arguments could not be read")
	}
	if err := selectObject(resolved, arguments.Object); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetObject(ctx, arguments.Object)
}
