package twentycrm

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The workspace catalog is the internal projection of the generated workspace document: which objects the
// workspace has, under which REST plural, and with which fields. It is built from one read of the document
// and is the only way an object name becomes a REST path. The document itself and every description in it
// are provider data and never leave this file; only names, JSON types, and flags are taken, each bounded.
const (
	maxCatalogObjects = 512
	maxObjectFields   = 512
	maxSubfields      = 64
	// Bounds of the enum values taken from the document, which exist only to validate a filter value.
	maxEnumValues = 100
)

var (
	// pluralPattern is the shape of an object's REST path segment.
	pluralPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,62}$`)
	// fieldNamePattern is the shape of a field or subfield name taken from the document.
	fieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	formatPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	enumValuePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
)

// catalog holds the objects of one workspace by singular name.
type catalog struct {
	objects map[string]*catalogObject
}

// catalogObject is one workspace object. Plural is its segment below /rest.
type catalogObject struct {
	Name   string
	Plural string
	Fields []catalogField
}

// catalogField is one field of an object. Type is the JSON type of the field (string, number, integer,
// boolean, object, or array), Format a bounded schema format such as uuid or date-time. Subfields are the
// parts of a composite field. Required comes from the create shape, Writable from the update shape.
// Relation names the object a relation field points to, when the document shows it. Reference is set for
// every field that points to another schema, even when that object is not in the catalog. InResponse is set
// for a field of the SINGULARForResponse schema, the shape of a record read. Enumerated is set when the
// document lists enum values (a selection field), Enum holds them when they are few and plain enough to
// validate a filter value against. Enum never leaves the provider: no output shows it.
type catalogField struct {
	Name       string
	Type       string
	Format     string
	Required   bool
	Writable   bool
	Creatable  bool
	Subfields  []catalogSubfield
	Relation   string
	Reference  bool
	InResponse bool
	Enumerated bool
	Enum       []string
	// CreateShape and UpdateShape are the value shapes of the field in the create schema (SINGULAR) and the
	// update schema (SINGULARForUpdate); nil when that schema does not hold the field. Only record writes use
	// them. Creatable is set when the create schema holds the field, Writable when the update schema does.
	CreateShape *valueShape
	UpdateShape *valueShape
}

type catalogSubfield struct {
	Name string
	Type string
}

// workspaceCatalog reads the workspace document at most once per client and returns its catalog.
func (c *Client) workspaceCatalog(ctx context.Context, op string) (*catalog, error) {
	if c.catalog != nil {
		return c.catalog, nil
	}
	var document openAPIJSON
	if err := c.get(ctx, op, schemaPath, nil, maxSchemaBytes, &document); err != nil {
		return nil, err
	}
	c.catalog = parseCatalog(&document)
	return c.catalog, nil
}

// openAPIJSON mirrors the parts of the generated workspace document the catalog reads.
type openAPIJSON struct {
	Paths      map[string]pathJSON `json:"paths"`
	Components struct {
		Schemas map[string]schemaJSON `json:"schemas"`
	} `json:"components"`
}

type pathJSON struct {
	Post *struct {
		OperationID string `json:"operationId"`
	} `json:"post"`
}

type schemaJSON struct {
	Required   []string            `json:"required"`
	Properties map[string]propJSON `json:"properties"`
}

type propJSON struct {
	Type   json.RawMessage `json:"type"`
	Format string          `json:"format"`
	Ref    string          `json:"$ref"`
	Enum   json.RawMessage `json:"enum"`
	// OneOf holds the reference of a many-to-one relation property, which the generated response schema
	// writes as {"type":"object","oneOf":[{"$ref":...}]}.
	OneOf []struct {
		Ref string `json:"$ref"`
	} `json:"oneOf"`
	Items      *propJSON           `json:"items"`
	Properties map[string]propJSON `json:"properties"`
}

// fill returns p with every part it lacks taken from the earlier, higher-priority definition.
func (p propJSON) fill(earlier propJSON) propJSON {
	if len(earlier.Type) > 0 {
		p.Type = earlier.Type
	}
	if earlier.Format != "" {
		p.Format = earlier.Format
	}
	if earlier.Ref != "" {
		p.Ref = earlier.Ref
	}
	if len(earlier.Enum) > 0 {
		p.Enum = earlier.Enum
	}
	if len(earlier.OneOf) > 0 {
		p.OneOf = earlier.OneOf
	}
	if earlier.Items != nil {
		p.Items = earlier.Items
	}
	if len(earlier.Properties) > 0 {
		p.Properties = earlier.Properties
	}
	return p
}

const createOnePrefix = "createOne"

// parseCatalog derives the objects from the document. An object is a path /PLURAL with a createOneSINGULAR
// operation whose schema exists; the schemas SINGULAR, SINGULARForUpdate, and SINGULARForResponse together
// describe its fields. Anything that does not fit the expected shapes is left out, never guessed.
func parseCatalog(document *openAPIJSON) *catalog {
	result := &catalog{objects: map[string]*catalogObject{}}
	plurals := make([]string, 0, len(document.Paths))
	for path := range document.Paths {
		plurals = append(plurals, path)
	}
	sort.Strings(plurals)
	for _, path := range plurals {
		plural := strings.TrimPrefix(path, "/")
		if plural == path || !pluralPattern.MatchString(plural) {
			continue
		}
		post := document.Paths[path].Post
		if post == nil || !strings.HasPrefix(post.OperationID, createOnePrefix) {
			continue
		}
		pascal := strings.TrimPrefix(post.OperationID, createOnePrefix)
		if pascal == "" || pascal[0] < 'A' || pascal[0] > 'Z' {
			continue
		}
		name := strings.ToLower(pascal[:1]) + pascal[1:]
		if !objectNamePattern.MatchString(name) || result.objects[name] != nil {
			continue
		}
		if _, ok := document.Components.Schemas[pascal]; !ok {
			continue
		}
		if len(result.objects) >= maxCatalogObjects {
			break
		}
		result.objects[name] = &catalogObject{Name: name, Plural: plural}
	}
	for name, object := range result.objects {
		object.Fields = result.fieldsOf(document, strings.ToUpper(name[:1])+name[1:])
	}
	return result
}

// fieldsOf merges the three generated schemas of one object. Each part of a field comes from the response
// shape first, then the plain shape, then the update shape.
func (cat *catalog) fieldsOf(document *openAPIJSON, pascal string) []catalogField {
	plain := document.Components.Schemas[pascal]
	update := document.Components.Schemas[pascal+"ForUpdate"]
	response := document.Components.Schemas[pascal+"ForResponse"]

	required := map[string]bool{}
	for _, name := range plain.Required {
		required[name] = true
	}
	names := map[string]propJSON{}
	for _, schema := range []schemaJSON{response, plain, update} {
		for name, prop := range schema.Properties {
			names[name] = prop.fill(names[name])
		}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		if fieldNamePattern.MatchString(name) {
			sorted = append(sorted, name)
		}
	}
	sort.Strings(sorted)
	if len(sorted) > maxObjectFields {
		sorted = sorted[:maxObjectFields]
	}
	fields := make([]catalogField, 0, len(sorted))
	for _, name := range sorted {
		prop := names[name]
		updateProp, writable := update.Properties[name]
		createProp, creatable := plain.Properties[name]
		_, inResponse := response.Properties[name]
		field := catalogField{
			Name: name, Type: jsonType(prop.Type), Required: required[name], Writable: writable,
			Creatable: creatable, InResponse: inResponse,
		}
		if writable {
			field.UpdateShape = shapeOf(updateProp, 0)
		}
		if creatable {
			field.CreateShape = shapeOf(createProp, 0)
		}
		if formatPattern.MatchString(prop.Format) {
			field.Format = prop.Format
		}
		field.Enumerated = len(prop.Enum) > 0
		field.Enum = enumValues(prop.Enum)
		ref := prop.Ref
		if ref == "" && len(prop.OneOf) > 0 {
			ref = prop.OneOf[0].Ref
		}
		if ref == "" && prop.Items != nil {
			ref = prop.Items.Ref
		}
		field.Relation = relationTarget(ref)
		field.Reference = ref != ""
		subs := make([]string, 0, len(prop.Properties))
		for sub := range prop.Properties {
			if fieldNamePattern.MatchString(sub) {
				subs = append(subs, sub)
			}
		}
		sort.Strings(subs)
		if len(subs) > maxSubfields {
			subs = subs[:maxSubfields]
		}
		for _, sub := range subs {
			field.Subfields = append(field.Subfields, catalogSubfield{Name: sub, Type: jsonType(prop.Properties[sub].Type)})
		}
		if field.Type == "" && field.Relation != "" {
			field.Type = "object"
		}
		fields = append(fields, field)
	}
	// A relation target that is not an object of this workspace is not a relation.
	for i := range fields {
		if fields[i].Relation != "" && cat.objects[fields[i].Relation] == nil {
			fields[i].Relation = ""
		}
	}
	return fields
}

// valueShape is the bounded shape of one writable value: its JSON type, format, enum values, the element
// shape of an array, and the parts of an object. It exists only to check a record write against the schema
// and never leaves the provider.
type valueShape struct {
	Type       string
	Format     string
	Enumerated bool
	Enum       []string
	Items      *valueShape
	Props      map[string]*valueShape
}

// maxShapeDepth bounds the nesting taken from the document: a field, its parts, the elements of an array part,
// and the parts of those elements.
const maxShapeDepth = 4

func shapeOf(prop propJSON, depth int) *valueShape {
	shape := &valueShape{Type: jsonType(prop.Type), Enumerated: len(prop.Enum) > 0, Enum: enumValues(prop.Enum)}
	if formatPattern.MatchString(prop.Format) {
		shape.Format = prop.Format
	}
	if depth >= maxShapeDepth {
		return shape
	}
	if prop.Items != nil {
		shape.Items = shapeOf(*prop.Items, depth+1)
	}
	if len(prop.Properties) > 0 {
		names := make([]string, 0, len(prop.Properties))
		for name := range prop.Properties {
			if fieldNamePattern.MatchString(name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) > maxSubfields {
			names = names[:maxSubfields]
		}
		shape.Props = make(map[string]*valueShape, len(names))
		for _, name := range names {
			shape.Props[name] = shapeOf(prop.Properties[name], depth+1)
		}
	}
	return shape
}

// enumValues reads the enum values of a selection field. A list that is too long, holds a value outside the
// plain identifier alphabet, or is not a list of strings yields none, so the field cannot be filtered.
func enumValues(raw json.RawMessage) []string {
	var values []string
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > maxEnumValues {
		return nil
	}
	for _, value := range values {
		if !enumValuePattern.MatchString(value) {
			return nil
		}
	}
	return values
}

// relationTarget reads the object a schema reference points to: #/components/schemas/PersonForResponse is person.
func relationTarget(ref string) string {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	pascal := strings.TrimPrefix(ref, prefix)
	pascal = strings.TrimSuffix(strings.TrimSuffix(pascal, "ForResponse"), "ForUpdate")
	if pascal == "" || pascal[0] < 'A' || pascal[0] > 'Z' {
		return ""
	}
	name := strings.ToLower(pascal[:1]) + pascal[1:]
	if !objectNamePattern.MatchString(name) {
		return ""
	}
	return name
}

// jsonType reads a JSON schema type, which may be a name or a list of names with null, into one of the six
// JSON value types. Anything else is empty.
func jsonType(raw json.RawMessage) string {
	var one string
	candidates := []string{}
	if json.Unmarshal(raw, &one) == nil {
		candidates = append(candidates, one)
	} else {
		var many []string
		if json.Unmarshal(raw, &many) == nil {
			candidates = many
		}
	}
	for _, candidate := range candidates {
		switch candidate {
		case "string", "number", "integer", "boolean", "object", "array":
			return candidate
		}
	}
	return ""
}

// object returns one object of the workspace by singular name, whether or not a connection may reach it.
func (cat *catalog) object(name string) (*catalogObject, bool) {
	object, ok := cat.objects[name]
	return object, ok
}

// reachable returns the objects a connection with this scope reaches, sorted by name. Relation fields into
// objects the connection cannot reach are dropped, so a projection does not point outside the boundary.
func (cat *catalog) reachable(bound scope) []catalogObject {
	names := make([]string, 0, len(cat.objects))
	for name := range cat.objects {
		if bound.allows(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := make([]catalogObject, 0, len(names))
	for _, name := range names {
		object := *cat.objects[name]
		object.Fields = make([]catalogField, 0, len(cat.objects[name].Fields))
		for _, field := range cat.objects[name].Fields {
			if field.Relation != "" && !bound.allows(field.Relation) {
				continue
			}
			object.Fields = append(object.Fields, field)
		}
		result = append(result, object)
	}
	return result
}

// resolve returns the object of a name the connection may reach and the workspace holds. The scope check
// repeats the one selectObject did, so a caller cannot skip it.
func (cat *catalog) resolve(bound scope, name string) (*catalogObject, error) {
	if !bound.allows(name) {
		return nil, invalidRequest(errObjectUnavailable)
	}
	object, ok := cat.objects[name]
	if !ok {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: "resolve object",
			Message: "this Twenty workspace does not hold this object"}
	}
	return object, nil
}
