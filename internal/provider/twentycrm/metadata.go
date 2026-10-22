package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	metadataGroup = "metadata"

	// metadataSensitivity classifies the data model of a workspace.
	metadataSensitivity = "twentycrm-schema"

	metadataObjectsPath = "/rest/metadata/objects"
	metadataFieldsPath  = "/rest/metadata/fields"

	// A page of objects carries all their fields, so it may be larger than other Twenty answers.
	metadataResponseBytes = 8 << 20

	metaFieldsMax  = 200
	metaOptionsMax = 100
	metaNameMax    = 200
	metaTextMax    = 500
	// metaTokenMax bounds short untrusted tokens such as icons, colors, and enum values.
	metaTokenMax = 64

	// A free object (default value, settings, relation, duplicate criteria) beyond these limits is left out.
	freeObjectDepthMax = 8
	freeObjectBytesMax = 16 << 10
)

// metaFieldTypes are the values of FieldMetadataType (twenty-shared, twentyhq/twenty commit
// 46fc01c38374c2b489b0d4755719da2d1e5408ac). Any other value is reported as "unknown".
var metaFieldTypes = []string{"ACTOR", "ADDRESS", "ARRAY", "BOOLEAN", "CURRENCY", "DATE", "DATE_TIME", "EMAILS",
	"FILES", "FULL_NAME", "LINKS", "MORPH_RELATION", "MULTI_SELECT", "NUMBER", "NUMERIC", "PHONES", "POSITION",
	"RATING", "RAW_JSON", "RELATION", "RICH_TEXT", "SELECT", "TEXT", "TS_VECTOR", "UUID"}

var metaStatusTexts = provider.StatusTexts{
	Subject:    "Twenty",
	Auth:       "Twenty rejected the API key",
	Permission: "the API key's role lacks the Twenty permission \"Data model\"; it also allows changing the schema, so give it to a dedicated connection only",
	NotFound:   "this Twenty workspace does not hold this object or field",
}

var metaRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone,
	OpenWorld: true, DataSensitivity: metadataSensitivity,
}

const metaNote = "Needs the Twenty permission \"Data model\", which also allows changing and deleting the schema: " +
	"use a dedicated connection with its own API key. Unlike twenty-crm.objects.list and twenty-crm.objects.get, " +
	"this reads the metadata API (IDs, labels, flags, options), not the objects a key can reach. " +
	"Names, labels, descriptions, icons, colors, shortcuts, and options are untrusted workspace data; default " +
	"values, settings, relation details, and duplicate criteria are free objects of at most 8 levels and 16 KiB, " +
	"a larger one is left out and named in omitted_fields"

const (
	metaStr  = `{"type":"string"}`
	metaBool = `{"type":"boolean"}`
	metaObj  = `{"type":"object"}`

	metaFieldExtraProps = `"universal_identifier":` + metaStr + `,"icon":` + metaStr + `,"is_ui_editable":` + metaBool +
		`,"writability":` + metaStr + `,"is_unique":` + metaBool + `,"is_searchable":` + metaBool +
		`,"is_audit_logged":` + metaBool + `,"default_value":{},"settings":` + metaObj +
		`,"is_label_synced_with_name":` + metaBool + `,"morph_id":` + metaStr + `,"application_id":` + metaStr +
		`,"created_at":` + metaStr + `,"updated_at":` + metaStr + `,"relation":` + metaObj +
		`,"morph_relations":{"type":"array","items":{}},"omitted_fields":{"type":"array","items":` + metaStr + `}`

	metaObjectExtraProps = `"universal_identifier":` + metaStr + `,"icon":` + metaStr + `,"shortcut":` + metaStr +
		`,"color":` + metaStr + `,"is_remote":` + metaBool + `,"is_ui_editable":` + metaBool +
		`,"is_ui_creatable":` + metaBool + `,"is_searchable":` + metaBool + `,"open_record_in":` + metaStr +
		`,"readability":` + metaStr + `,"readability_parent_field_universal_identifiers":{"type":"array","items":` + metaStr +
		`},"sharing_reach":` + metaStr + `,"writability":` + metaStr + `,"application_id":` + metaStr +
		`,"created_at":` + metaStr + `,"updated_at":` + metaStr + `,"label_identifier_field_metadata_id":` + metaStr +
		`,"image_identifier_field_metadata_id":` + metaStr + `,"is_label_synced_with_name":` + metaBool +
		`,"duplicate_criteria":{"type":"array","items":{}},"omitted_fields":{"type":"array","items":` + metaStr + `}`

	metaFlags = `"is_custom":{"type":"boolean"},"is_system":{"type":"boolean"},"is_active":{"type":"boolean"}`

	metaFieldSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"label":{"type":"string"},"description":{"type":"string"},"type":{"type":"string"},` + metaFlags + `,` +
		`"is_nullable":{"type":"boolean"},"options":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"string"},"value":{"type":"string"},"label":{"type":"string"},"position":{"type":"integer"}},` +
		`"additionalProperties":false}},"more_options":{"type":"boolean"},"object_metadata_id":{"type":"string"},` +
		metaFieldExtraProps + `},"required":["id","name","label","type"],"additionalProperties":false}`

	metaObjectProps = `"id":{"type":"string"},"name_singular":{"type":"string"},"name_plural":{"type":"string"},` +
		`"label_singular":{"type":"string"},"label_plural":{"type":"string"},"description":{"type":"string"},` +
		metaFlags + `,"field_count":{"type":"integer"},` + metaObjectExtraProps
	metaObjectRequired = `"required":["id","name_singular","name_plural","label_singular","label_plural","field_count"]`
)

// metaObjectFields documents the members of an object, at the top level of meta-objects.get.
var metaObjectFields = []capability.Field{
	{Name: "id", Description: "UUID of the object"},
	{Name: "name_singular", Description: "API name in the singular, untrusted data"},
	{Name: "name_plural", Description: "API name in the plural, untrusted data"},
	{Name: "label_singular", Description: "Label in the singular, untrusted data"},
	{Name: "label_plural", Description: "Label in the plural, untrusted data"},
	{Name: "description", Description: "Description of the object, untrusted data"},
	{Name: "is_custom", Description: "True for an object created in the workspace"},
	{Name: "is_system", Description: "True for a system object"},
	{Name: "is_active", Description: "True while the object is active"},
	{Name: "field_count", Description: "Number of fields of the object"},
	{Name: "universal_identifier", Description: "Universal identifier of the object, untrusted data"},
	{Name: "icon", Description: "Icon name, untrusted data"},
	{Name: "shortcut", Description: "Keyboard shortcut, untrusted data"},
	{Name: "color", Description: "Color, untrusted data"},
	{Name: "is_remote", Description: "True for a remote object"},
	{Name: "is_ui_editable", Description: "True when the object can be edited in the Twenty UI"},
	{Name: "is_ui_creatable", Description: "True when records can be created in the Twenty UI"},
	{Name: "is_searchable", Description: "True when the object is searchable"},
	{Name: "open_record_in", Description: "Where a record opens in the Twenty UI, untrusted data"},
	{Name: "readability", Description: "Read access rule of the object, untrusted data"},
	{Name: "readability_parent_field_universal_identifiers", Description: "Universal identifiers of the fields the read access derives from, at most 100"},
	{Name: "sharing_reach", Description: "Sharing reach of the object, untrusted data"},
	{Name: "writability", Description: "Write access rule of the object, untrusted data"},
	{Name: "application_id", Description: "UUID of the application that owns the object"},
	{Name: "created_at", Description: "Creation timestamp of the object"},
	{Name: "updated_at", Description: "Last change timestamp of the object"},
	{Name: "label_identifier_field_metadata_id", Description: "UUID of the field that labels a record"},
	{Name: "image_identifier_field_metadata_id", Description: "UUID of the field that provides the record image"},
	{Name: "is_label_synced_with_name", Description: "True while the label follows the name"},
	{Name: "duplicate_criteria", Description: "Field names that identify duplicates, untrusted data; at most 8 levels and 16 KiB"},
	{Name: "omitted_fields", Description: "Members whose value exceeded the size limits and was left out, not shortened"},
}

// metaFieldMembers documents the members of a field, at the top level of meta-fields.get.
var metaFieldMembers = []capability.Field{
	{Name: "id", Description: "UUID of the field"},
	{Name: "name", Description: "API name, untrusted data"},
	{Name: "label", Description: "Label, untrusted data"},
	{Name: "description", Description: "Description of the field, untrusted data"},
	{Name: "type", Description: "Field type, unknown for a type this version does not know"},
	{Name: "is_custom", Description: "True for a field created in the workspace"},
	{Name: "is_system", Description: "True for a system field"},
	{Name: "is_active", Description: "True while the field is active"},
	{Name: "is_nullable", Description: "True when the field may be empty"},
	{Name: "object_metadata_id", Description: "UUID of the object that holds the field"},
	{Name: "universal_identifier", Description: "Universal identifier of the field, untrusted data"},
	{Name: "icon", Description: "Icon name, untrusted data"},
	{Name: "is_ui_editable", Description: "True when the field can be edited in the Twenty UI"},
	{Name: "writability", Description: "Write access rule of the field, untrusted data"},
	{Name: "is_unique", Description: "True when values must be unique"},
	{Name: "is_searchable", Description: "True when the field is searchable"},
	{Name: "is_audit_logged", Description: "True when changes are audit logged"},
	{Name: "default_value", Description: "Default value as Twenty sends it, untrusted data; at most 8 levels and 16 KiB"},
	{Name: "settings", Description: "Settings of the field type, untrusted data; at most 8 levels and 16 KiB"},
	{Name: "is_label_synced_with_name", Description: "True while the label follows the name"},
	{Name: "morph_id", Description: "UUID that groups the fields of one morph relation"},
	{Name: "application_id", Description: "UUID of the application that owns the field"},
	{Name: "created_at", Description: "Creation timestamp of the field"},
	{Name: "updated_at", Description: "Last change timestamp of the field"},
	{Name: "relation", Description: "Relation details of a relation field, naming objects and fields of the whole workspace, untrusted data; at most 8 levels and 16 KiB"},
	{Name: "morph_relations", Description: "Relation details of a morph relation field, untrusted data; at most 8 levels and 16 KiB"},
	{Name: "omitted_fields", Description: "Members whose value exceeded the size limits and was left out, not shortened"},
}

var metaObjectsList = capability.Descriptor{
	ID:      Provider + ".meta-objects.list",
	Version: 1,
	Title:   "List Twenty CRM object metadata",
	Description: "List one page of the object metadata of the Twenty workspace of a connection without object " +
		"targets: ID, names, labels, description, flags, display, access rules, identifier fields, duplicate criteria, " +
		"and number of fields; newest IDs first. " + metaNote,
	Tags:     []string{"twentycrm", "metadata", "schema", "objects", "list"},
	Risk:     metaRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":` + workflowLimitSchema +
		`,"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"objects":{"type":"array","items":{` +
		`"type":"object","properties":{` + metaObjectProps + `},` + metaObjectRequired + `,"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"},` + totalCountSchema + `},` +
		`"required":["objects","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "limit", Description: "Objects per page, from 1 through 100; 25 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "objects", Description: "The objects on this page, without their fields, untrusted data; per object the " +
			"members of meta-objects.get except fields and more_fields"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the workspace holds a following page"},
		totalCountField,
	},
	Examples: []capability.Example{{Description: "List the object metadata", Arguments: json.RawMessage(`{}`)}},
}

var metaObjectsGet = capability.Descriptor{
	ID:      Provider + ".meta-objects.get",
	Version: 1,
	Title:   "Get Twenty CRM object metadata",
	Description: "Read the metadata of one object of the Twenty workspace of a connection without object targets " +
		"with its fields: per field ID, name, label, description, type, flags, options, default value, settings, and " +
		"relation details. " + metaNote,
	Tags:     []string{"twentycrm", "metadata", "schema", "objects", "fields", "get"},
	Risk:     metaRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + metaObjectProps + `,"fields":{"type":"array",` +
		`"items":` + metaFieldSchema + `},"more_fields":{"type":"boolean"}},` +
		`"required":["id","name_singular","name_plural","label_singular","label_plural","field_count","fields"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "UUID of the object, as returned by twenty-crm.meta-objects.list", Required: true},
	},
	Fields: append(append([]capability.Field{}, metaObjectFields...),
		capability.Field{Name: "fields", Description: "At most 200 fields with the members of meta-fields.get except " +
			"object_metadata_id, at most 100 options each, untrusted data"},
		capability.Field{Name: "more_fields", Description: "True when the object holds more fields than listed"}),
	Examples: []capability.Example{{
		Description: "Read an object with its fields",
		Arguments:   json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`),
	}},
}

var metaFieldsGet = capability.Descriptor{
	ID:      Provider + ".meta-fields.get",
	Version: 1,
	Title:   "Get Twenty CRM field metadata",
	Description: "Read the metadata of one field of the Twenty workspace of a connection without object targets: " +
		"ID, name, label, description, type, flags, options, icon, access rules, default value, settings, relation " +
		"details, and the ID of its object. " + metaNote,
	Tags:     []string{"twentycrm", "metadata", "schema", "fields", "get"},
	Risk:     metaRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(metaFieldSchema),
	Arguments: []capability.Argument{
		{Name: "id", Description: "UUID of the field, as returned by twenty-crm.meta-objects.get", Required: true},
	},
	Fields: append(append([]capability.Field{}, metaFieldMembers...),
		capability.Field{Name: "options", Description: "At most 100 options of a select field, untrusted data"},
		capability.Field{Name: "more_options", Description: "True when the field holds more options than listed"}),
	Examples: []capability.Example{{
		Description: "Read a field",
		Arguments:   json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`),
	}},
}

// MetaOption is one option of a select field.
type MetaOption struct {
	ID       string `json:"id,omitempty"`
	Value    string `json:"value,omitempty"`
	Label    string `json:"label,omitempty"`
	Position *int   `json:"position,omitempty"`
}

// MetaField is a field of an object. A flag Twenty does not send is absent. Free objects keep their value
// exactly, numbers included.
type MetaField struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Label            string       `json:"label"`
	Description      string       `json:"description,omitempty"`
	Type             string       `json:"type"`
	IsCustom         *bool        `json:"is_custom,omitempty"`
	IsSystem         *bool        `json:"is_system,omitempty"`
	IsActive         *bool        `json:"is_active,omitempty"`
	IsNullable       *bool        `json:"is_nullable,omitempty"`
	Options          []MetaOption `json:"options,omitempty"`
	MoreOptions      bool         `json:"more_options,omitempty"`
	ObjectMetadataID string       `json:"object_metadata_id,omitempty"`

	UniversalIdentifier   string   `json:"universal_identifier,omitempty"`
	Icon                  string   `json:"icon,omitempty"`
	IsUIEditable          *bool    `json:"is_ui_editable,omitempty"`
	Writability           string   `json:"writability,omitempty"`
	IsUnique              *bool    `json:"is_unique,omitempty"`
	IsSearchable          *bool    `json:"is_searchable,omitempty"`
	IsAuditLogged         *bool    `json:"is_audit_logged,omitempty"`
	DefaultValue          any      `json:"default_value,omitempty"`
	Settings              any      `json:"settings,omitempty"`
	IsLabelSyncedWithName *bool    `json:"is_label_synced_with_name,omitempty"`
	MorphID               string   `json:"morph_id,omitempty"`
	ApplicationID         string   `json:"application_id,omitempty"`
	CreatedAt             string   `json:"created_at,omitempty"`
	UpdatedAt             string   `json:"updated_at,omitempty"`
	Relation              any      `json:"relation,omitempty"`
	MorphRelations        any      `json:"morph_relations,omitempty"`
	OmittedFields         []string `json:"omitted_fields,omitempty"`
}

// MetaObjectSummary is an object without its fields.
type MetaObjectSummary struct {
	ID            string `json:"id"`
	NameSingular  string `json:"name_singular"`
	NamePlural    string `json:"name_plural"`
	LabelSingular string `json:"label_singular"`
	LabelPlural   string `json:"label_plural"`
	Description   string `json:"description,omitempty"`
	IsCustom      *bool  `json:"is_custom,omitempty"`
	IsSystem      *bool  `json:"is_system,omitempty"`
	IsActive      *bool  `json:"is_active,omitempty"`
	FieldCount    int    `json:"field_count"`

	UniversalIdentifier                        string   `json:"universal_identifier,omitempty"`
	Icon                                       string   `json:"icon,omitempty"`
	Shortcut                                   string   `json:"shortcut,omitempty"`
	Color                                      string   `json:"color,omitempty"`
	IsRemote                                   *bool    `json:"is_remote,omitempty"`
	IsUIEditable                               *bool    `json:"is_ui_editable,omitempty"`
	IsUICreatable                              *bool    `json:"is_ui_creatable,omitempty"`
	IsSearchable                               *bool    `json:"is_searchable,omitempty"`
	OpenRecordIn                               string   `json:"open_record_in,omitempty"`
	Readability                                string   `json:"readability,omitempty"`
	ReadabilityParentFieldUniversalIdentifiers []string `json:"readability_parent_field_universal_identifiers,omitempty"`
	SharingReach                               string   `json:"sharing_reach,omitempty"`
	Writability                                string   `json:"writability,omitempty"`
	ApplicationID                              string   `json:"application_id,omitempty"`
	CreatedAt                                  string   `json:"created_at,omitempty"`
	UpdatedAt                                  string   `json:"updated_at,omitempty"`
	LabelIdentifierFieldMetadataID             string   `json:"label_identifier_field_metadata_id,omitempty"`
	ImageIdentifierFieldMetadataID             string   `json:"image_identifier_field_metadata_id,omitempty"`
	IsLabelSyncedWithName                      *bool    `json:"is_label_synced_with_name,omitempty"`
	DuplicateCriteria                          any      `json:"duplicate_criteria,omitempty"`
	OmittedFields                              []string `json:"omitted_fields,omitempty"`
}

// MetaObjectList is one page of object metadata.
type MetaObjectList struct {
	Objects    []MetaObjectSummary `json:"objects"`
	NextCursor string              `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
	TotalCount *int64              `json:"total_count,omitempty"`
}

// MetaObject is an object with its fields.
type MetaObject struct {
	MetaObjectSummary
	Fields     []MetaField `json:"fields"`
	MoreFields bool        `json:"more_fields,omitempty"`
}

type metaOptionRecord struct {
	ID       *string  `json:"id"`
	Value    *string  `json:"value"`
	Label    *string  `json:"label"`
	Position *float64 `json:"position"`
}

type metaFieldRecord struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Name             *string         `json:"name"`
	Label            *string         `json:"label"`
	Description      *string         `json:"description"`
	IsCustom         *bool           `json:"isCustom"`
	IsSystem         *bool           `json:"isSystem"`
	IsActive         *bool           `json:"isActive"`
	IsNullable       *bool           `json:"isNullable"`
	Options          json.RawMessage `json:"options"`
	ObjectMetadataID *string         `json:"objectMetadataId"`

	UniversalIdentifier   *string         `json:"universalIdentifier"`
	Icon                  *string         `json:"icon"`
	IsUIEditable          *bool           `json:"isUIEditable"`
	Writability           *string         `json:"writability"`
	IsUnique              *bool           `json:"isUnique"`
	IsSearchable          *bool           `json:"isSearchable"`
	IsAuditLogged         *bool           `json:"isAuditLogged"`
	DefaultValue          json.RawMessage `json:"defaultValue"`
	Settings              json.RawMessage `json:"settings"`
	IsLabelSyncedWithName *bool           `json:"isLabelSyncedWithName"`
	MorphID               *string         `json:"morphId"`
	ApplicationID         *string         `json:"applicationId"`
	CreatedAt             *string         `json:"createdAt"`
	UpdatedAt             *string         `json:"updatedAt"`
	Relation              json.RawMessage `json:"relation"`
	MorphRelations        json.RawMessage `json:"morphRelations"`
}

type metaObjectRecord struct {
	ID            string          `json:"id"`
	NameSingular  *string         `json:"nameSingular"`
	NamePlural    *string         `json:"namePlural"`
	LabelSingular *string         `json:"labelSingular"`
	LabelPlural   *string         `json:"labelPlural"`
	Description   *string         `json:"description"`
	IsCustom      *bool           `json:"isCustom"`
	IsSystem      *bool           `json:"isSystem"`
	IsActive      *bool           `json:"isActive"`
	Fields        json.RawMessage `json:"fields"`

	UniversalIdentifier                        *string         `json:"universalIdentifier"`
	Icon                                       *string         `json:"icon"`
	Shortcut                                   *string         `json:"shortcut"`
	Color                                      *string         `json:"color"`
	IsRemote                                   *bool           `json:"isRemote"`
	IsUIEditable                               *bool           `json:"isUIEditable"`
	IsUICreatable                              *bool           `json:"isUICreatable"`
	IsSearchable                               *bool           `json:"isSearchable"`
	OpenRecordIn                               *string         `json:"openRecordIn"`
	Readability                                *string         `json:"readability"`
	ReadabilityParentFieldUniversalIdentifiers json.RawMessage `json:"readabilityParentFieldUniversalIdentifiers"`
	SharingReach                               *string         `json:"sharingReach"`
	Writability                                *string         `json:"writability"`
	ApplicationID                              *string         `json:"applicationId"`
	CreatedAt                                  *string         `json:"createdAt"`
	UpdatedAt                                  *string         `json:"updatedAt"`
	LabelIdentifierFieldMetadataID             *string         `json:"labelIdentifierFieldMetadataId"`
	ImageIdentifierFieldMetadataID             *string         `json:"imageIdentifierFieldMetadataId"`
	IsLabelSyncedWithName                      *bool           `json:"isLabelSyncedWithName"`
	DuplicateCriteria                          json.RawMessage `json:"duplicateCriteria"`
}

// metaUUID returns a UUID Twenty sent, or "" for anything else.
func metaUUID(value *string) string {
	if value != nil && validUUID(*value) {
		return *value
	}
	return ""
}

// freeObjects collects the names of free objects left out for their size.
type freeObjects []string

// bound decodes a free object without a float detour. A value deeper than freeObjectDepthMax or larger than
// freeObjectBytesMax serialized is left out whole and named, never shortened; an empty value is absent.
func (o *freeObjects) bound(name string, raw json.RawMessage) any {
	if isNull(raw) {
		return nil
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return nil
	}
	value, err := decodeValue(compact.Bytes())
	if err != nil || emptyValue(value) {
		return nil
	}
	if compact.Len() > freeObjectBytesMax || valueDepth(value) > freeObjectDepthMax {
		*o = append(*o, name)
		return nil
	}
	return value
}

// valueDepth counts the levels of nested arrays and objects.
func valueDepth(value any) int {
	deepest := 0
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			deepest = max(deepest, valueDepth(item))
		}
		return deepest + 1
	case map[string]any:
		for _, item := range v {
			deepest = max(deepest, valueDepth(item))
		}
		return deepest + 1
	}
	return 0
}

// metaText drops invalid UTF-8, control and format characters, then caps the length.
func metaText(value *string, max int) string {
	if value == nil {
		return ""
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(*value, ""))
	return capLabelTo(clean, max)
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// rawItems decodes an optional array.
func rawItems(raw json.RawMessage) ([]json.RawMessage, bool) {
	if isNull(raw) {
		return nil, true
	}
	var items []json.RawMessage
	return items, json.Unmarshal(raw, &items) == nil && items != nil
}

func (r metaFieldRecord) project(op string) (MetaField, error) {
	if !validUUID(r.ID) {
		return MetaField{}, provider.InvalidResponse(op, "Twenty returned a field without a usable identifier")
	}
	field := MetaField{ID: r.ID, Name: metaText(r.Name, metaNameMax), Label: metaText(r.Label, metaNameMax),
		Description: metaText(r.Description, metaTextMax), Type: enumToken(metaFieldTypes, r.Type),
		IsCustom: r.IsCustom, IsSystem: r.IsSystem, IsActive: r.IsActive, IsNullable: r.IsNullable}
	field.ObjectMetadataID = metaUUID(r.ObjectMetadataID)
	field.UniversalIdentifier = metaText(r.UniversalIdentifier, metaTokenMax)
	field.Icon = metaText(r.Icon, metaTokenMax)
	field.IsUIEditable, field.IsUnique, field.IsSearchable = r.IsUIEditable, r.IsUnique, r.IsSearchable
	field.IsAuditLogged, field.IsLabelSyncedWithName = r.IsAuditLogged, r.IsLabelSyncedWithName
	field.Writability = metaText(r.Writability, metaTokenMax)
	field.MorphID, field.ApplicationID = metaUUID(r.MorphID), metaUUID(r.ApplicationID)
	field.CreatedAt, field.UpdatedAt = metaText(r.CreatedAt, metaTokenMax), metaText(r.UpdatedAt, metaTokenMax)
	var omitted freeObjects
	field.DefaultValue = omitted.bound("default_value", r.DefaultValue)
	field.Settings = omitted.bound("settings", r.Settings)
	field.Relation = omitted.bound("relation", r.Relation)
	field.MorphRelations = omitted.bound("morph_relations", r.MorphRelations)
	field.OmittedFields = omitted
	options, ok := rawItems(r.Options)
	if !ok {
		return MetaField{}, provider.InvalidResponse(op, "Twenty returned unusable options")
	}
	for i, item := range options {
		if i == metaOptionsMax {
			field.MoreOptions = true
			break
		}
		var option metaOptionRecord
		if json.Unmarshal(item, &option) != nil {
			return MetaField{}, provider.InvalidResponse(op, "Twenty returned an unusable option")
		}
		projected := MetaOption{Value: metaText(option.Value, metaNameMax), Label: metaText(option.Label, metaNameMax)}
		if option.ID != nil && validUUID(*option.ID) {
			projected.ID = *option.ID
		}
		if option.Position != nil && *option.Position >= 0 && *option.Position < 1e6 {
			position := int(*option.Position)
			projected.Position = &position
		}
		field.Options = append(field.Options, projected)
	}
	return field, nil
}

func (r metaObjectRecord) summary(op string, fieldCount int) (MetaObjectSummary, error) {
	if !validUUID(r.ID) {
		return MetaObjectSummary{}, provider.InvalidResponse(op, "Twenty returned an object without a usable identifier")
	}
	summary := MetaObjectSummary{ID: r.ID, NameSingular: metaText(r.NameSingular, metaNameMax),
		NamePlural: metaText(r.NamePlural, metaNameMax), LabelSingular: metaText(r.LabelSingular, metaNameMax),
		LabelPlural: metaText(r.LabelPlural, metaNameMax), Description: metaText(r.Description, metaTextMax),
		IsCustom: r.IsCustom, IsSystem: r.IsSystem, IsActive: r.IsActive, FieldCount: fieldCount,
		UniversalIdentifier: metaText(r.UniversalIdentifier, metaTokenMax), Icon: metaText(r.Icon, metaTokenMax),
		Shortcut: metaText(r.Shortcut, metaTokenMax), Color: metaText(r.Color, metaTokenMax),
		IsRemote: r.IsRemote, IsUIEditable: r.IsUIEditable, IsUICreatable: r.IsUICreatable,
		IsSearchable: r.IsSearchable, OpenRecordIn: metaText(r.OpenRecordIn, metaTokenMax),
		Readability: metaText(r.Readability, metaTokenMax), SharingReach: metaText(r.SharingReach, metaTokenMax),
		Writability: metaText(r.Writability, metaTokenMax), ApplicationID: metaUUID(r.ApplicationID),
		CreatedAt: metaText(r.CreatedAt, metaTokenMax), UpdatedAt: metaText(r.UpdatedAt, metaTokenMax),
		LabelIdentifierFieldMetadataID: metaUUID(r.LabelIdentifierFieldMetadataID),
		ImageIdentifierFieldMetadataID: metaUUID(r.ImageIdentifierFieldMetadataID),
		IsLabelSyncedWithName:          r.IsLabelSyncedWithName}
	var omitted freeObjects
	if ids, ok := rawItems(r.ReadabilityParentFieldUniversalIdentifiers); ok && len(ids) > maxValueItems {
		omitted = append(omitted, "readability_parent_field_universal_identifiers")
	} else if ok {
		for _, item := range ids {
			var id *string
			if json.Unmarshal(item, &id) == nil && metaUUID(id) != "" {
				summary.ReadabilityParentFieldUniversalIdentifiers = append(summary.ReadabilityParentFieldUniversalIdentifiers, *id)
			}
		}
	}
	summary.DuplicateCriteria = omitted.bound("duplicate_criteria", r.DuplicateCriteria)
	summary.OmittedFields = omitted
	return summary, nil
}

// unwrapMeta returns the entity of a get answer. The new format is the entity itself, the legacy format wraps
// it as {"data":{key:entity}}; anything else, or both at once, is not understood.
func unwrapMeta(op string, body json.RawMessage, key string) (json.RawMessage, error) {
	bad := provider.InvalidResponse(op, "Twenty answered in an unknown format")
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil || top == nil {
		return nil, bad
	}
	data, wrapped := top["data"]
	if !wrapped {
		return body, nil
	}
	var inner map[string]json.RawMessage
	if _, direct := top["id"]; direct || json.Unmarshal(data, &inner) != nil || inner == nil {
		return nil, bad
	}
	entity, ok := inner[key]
	if !ok {
		return nil, bad
	}
	return entity, nil
}

type metaPage struct {
	Data       json.RawMessage `json:"data"`
	TotalCount json.RawMessage `json:"totalCount"`
	PageInfo   struct {
		HasNextPage bool    `json:"hasNextPage"`
		EndCursor   *string `json:"endCursor"`
	} `json:"pageInfo"`
}

// items accepts the new format (data is the array) and the legacy one (data.objects is the array).
func (p *metaPage) items(op string, limit int) ([]json.RawMessage, error) {
	bad := provider.InvalidResponse(op, "Twenty returned an unusable page of objects")
	raw := bytes.TrimSpace(p.Data)
	if len(raw) > 0 && raw[0] == '{' {
		var legacy map[string]json.RawMessage
		if json.Unmarshal(raw, &legacy) != nil {
			return nil, bad
		}
		raw = legacy["objects"]
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || items == nil || len(items) > limit {
		return nil, bad
	}
	return items, nil
}

func (p *metaPage) nextCursor(op string, binding []byte) (string, error) {
	if !p.PageInfo.HasNextPage {
		return "", nil
	}
	if p.PageInfo.EndCursor == nil || !validUUID(*p.PageInfo.EndCursor) {
		return "", provider.InvalidResponse(op, "Twenty returned an unusable cursor")
	}
	return provider.EncodeCursor(binding, *p.PageInfo.EndCursor), nil
}

// ListMetaObjects reads one page of object metadata, without the fields.
func (c *Client) ListMetaObjects(ctx context.Context, connection string, limit int, cursor string) (*MetaObjectList, error) {
	const op = "list object metadata"
	binding := provider.CursorBinding("meta-objects.list", connection)
	limit, after, err := workflowPaging(limit, cursor, binding)
	if err != nil {
		return nil, err
	}
	if after != "" && !validUUID(after) {
		return nil, invalidRequest("cursor is not a next_cursor of this request; start again without cursor")
	}
	values := url.Values{"limit": {strconv.Itoa(limit)}}
	if after != "" {
		values.Set("starting_after", after)
	}
	var page metaPage
	if err := c.getWith(ctx, op, metadataObjectsPath, values, metadataResponseBytes, &page, metaStatusTexts); err != nil {
		return nil, err
	}
	items, err := page.items(op, limit)
	if err != nil {
		return nil, err
	}
	total, err := pageTotal(op, page.TotalCount)
	if err != nil {
		return nil, err
	}
	result := &MetaObjectList{Objects: make([]MetaObjectSummary, 0, len(items)), HasMore: page.PageInfo.HasNextPage, TotalCount: total}
	for _, item := range items {
		var record metaObjectRecord
		if json.Unmarshal(item, &record) != nil {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable object")
		}
		fields, ok := rawItems(record.Fields)
		if !ok {
			return nil, provider.InvalidResponse(op, "Twenty returned unusable fields")
		}
		summary, err := record.summary(op, len(fields))
		if err != nil {
			return nil, err
		}
		result.Objects = append(result.Objects, summary)
	}
	if result.NextCursor, err = page.nextCursor(op, binding); err != nil {
		return nil, err
	}
	return result, nil
}

// GetMetaObject reads one object with its fields.
func (c *Client) GetMetaObject(ctx context.Context, id string) (*MetaObject, error) {
	const op = "get object metadata"
	if !validUUID(id) {
		return nil, invalidRequest("the object id must be a UUID")
	}
	var body json.RawMessage
	if err := c.getWith(ctx, op, metadataObjectsPath+"/"+url.PathEscape(id), nil, metadataResponseBytes, &body,
		metaStatusTexts); err != nil {
		return nil, err
	}
	entity, err := unwrapMeta(op, body, "object")
	if err != nil {
		return nil, err
	}
	var record metaObjectRecord
	if json.Unmarshal(entity, &record) != nil || !equalUUID(record.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different object than the requested one")
	}
	fields, ok := rawItems(record.Fields)
	if !ok {
		return nil, provider.InvalidResponse(op, "Twenty returned unusable fields")
	}
	summary, err := record.summary(op, len(fields))
	if err != nil {
		return nil, err
	}
	result := &MetaObject{MetaObjectSummary: summary, Fields: make([]MetaField, 0, min(len(fields), metaFieldsMax))}
	for i, item := range fields {
		if i == metaFieldsMax {
			result.MoreFields = true
			break
		}
		var fieldRecord metaFieldRecord
		if json.Unmarshal(item, &fieldRecord) != nil {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable field")
		}
		field, err := fieldRecord.project(op)
		if err != nil {
			return nil, err
		}
		field.ObjectMetadataID = ""
		result.Fields = append(result.Fields, field)
	}
	return result, nil
}

// GetMetaField reads one field.
func (c *Client) GetMetaField(ctx context.Context, id string) (*MetaField, error) {
	const op = "get field metadata"
	if !validUUID(id) {
		return nil, invalidRequest("the field id must be a UUID")
	}
	var body json.RawMessage
	if err := c.getWith(ctx, op, metadataFieldsPath+"/"+url.PathEscape(id), nil, metadataResponseBytes, &body,
		metaStatusTexts); err != nil {
		return nil, err
	}
	entity, err := unwrapMeta(op, body, "field")
	if err != nil {
		return nil, err
	}
	var record metaFieldRecord
	if json.Unmarshal(entity, &record) != nil || !equalUUID(record.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different field than the requested one")
	}
	field, err := record.project(op)
	if err != nil {
		return nil, err
	}
	return &field, nil
}

func invokeMetaObjectsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("list object metadata", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListMetaObjects(ctx, resolved.Name, args.Limit, args.Cursor)
}

func invokeMetaObjectsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("get object metadata", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the object id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetMetaObject(ctx, args.ID)
}

func invokeMetaFieldsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("get field metadata", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the field id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetMetaField(ctx, args.ID)
}
