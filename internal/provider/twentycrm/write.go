package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// A record write sends the fields the agent names, after each of them was checked against the schema of the
// object: the create shape (SINGULAR, with its required fields) for a create and the update shape
// (SINGULARForUpdate, without required fields) for an update. The body is built from the checked values and
// from nothing else. The response shapes are twenty-server's rest-api-create-one.handler.ts and
// rest-api-update-one.handler.ts (twentyhq/twenty, commit 46fc01c38374c2b489b0d4755719da2d1e5408ac): the record
// arrives as data.create<Singular> or data.update<Singular>.
const (
	maxWriteFields = 100
	maxWriteBytes  = 1 << 20

	errWriteLimit    = "fields must hold at most 100 fields, each value within 64 KiB, and the request within 1 MiB"
	errWriteNone     = "fields must name at least one field to change"
	errWriteField    = "a field or value cannot be written to this object"
	errWriteRequired = "the fields given do not cover what this object requires to create a record"
	errWriteID       = "id must be a record identifier in UUID form"

	// errRecordPermission names the role of the key as the cause, never the text of the provider.
	errRecordPermission = "the workspace role of this API key may not create or change records of this object or " +
		"one of the given fields; check the object and field permissions of the role in Twenty"

	// recordCreateUncertain and recordUpdateUncertain are appended to a failure of a write whose request may have
	// reached Twenty. A create names the danger of a duplicate, because repeating it adds a second record.
	recordCreateUncertain = "; this record may have been created, and repeating the create can add a duplicate; " +
		"search the object for it before creating again"
	recordUpdateUncertain = "; this change may have taken effect, read the record before repeating it"
)

// systemWriteFields are the fields Twenty maintains itself. A write never names them, whatever the schema says.
var systemWriteFields = map[string]bool{
	"id": true, "createdAt": true, "updatedAt": true, "deletedAt": true, "createdBy": true, "updatedBy": true,
	"position": true, "searchVector": true,
}

const writeNote = "Field names and values are checked against the schema of the object before the request: a value " +
	"has the type of its field; a selection or multi-selection takes values of the schema; dates are YYYY-MM-DD " +
	"and date-times RFC 3339; a composite field (emails, phones, links, currency with amountMicros and " +
	"currencyCode, name, address) takes only the parts the schema names; rich text takes only {\"markdown\": text}; " +
	"a relation is set through <relation>Id as a UUID, only when its target object is reachable through the " +
	"connection. System fields, files, and computed fields are refused, and so is null; the refusal names neither " +
	"field nor value. At most 100 fields, each string up to 64 KiB, the request up to 1 MiB. Values are untrusted " +
	"workspace data and often personal data; the role of the API key decides which objects and fields it may write"

func recordWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: recordDataSensitivity}
}

var recordWriteFields = []capability.Field{
	{Name: "id", Description: "Record identifier"},
	{Name: "created_at", Description: "Creation timestamp of the record"},
	{Name: "updated_at", Description: "Last change timestamp of the record"},
	{Name: "fields", Description: "Field name to value as Twenty reports the record after the write, untrusted data"},
}

var recordsCreate = capability.Descriptor{
	ID:      Provider + ".records.create",
	Version: 1,
	Title:   "Create a Twenty CRM record",
	Description: "Create one record in one reachable object of the Twenty workspace of a connection with the given " +
		"field values. " + writeNote,
	Tags:     []string{"twentycrm", "crm", "records", "create"},
	Risk:     recordWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,` +
		`"fields":{"type":"object","maxProperties":100}},"required":["object","fields"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(recordSchema),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list", Required: true},
		{Name: "fields", Description: "Field name to value, at most 100; every field twentycrm.objects.get shows as required must be given", Required: true},
	},
	Fields: recordWriteFields,
	Examples: []capability.Example{{
		Description: "Create a person with a name, an email, and a company",
		Arguments: json.RawMessage(`{"object":"person","fields":{"name":{"firstName":"Ada","lastName":"Lovelace"},` +
			`"emails":{"primaryEmail":"ada@example.com"},"companyId":"11111111-2222-3333-4444-555555555555"}}`),
	}},
}

var recordsUpdate = capability.Descriptor{
	ID:      Provider + ".records.update",
	Version: 1,
	Title:   "Update a Twenty CRM record",
	Description: "Change the given fields of one record of one reachable object of the Twenty workspace of a " +
		"connection; fields not named stay as they are. " + writeNote,
	Tags:     []string{"twentycrm", "crm", "records", "update"},
	Risk:     recordWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"id":` + recordIDSchema +
		`,"fields":{"type":"object","minProperties":1,"maxProperties":100}},` +
		`"required":["object","id","fields"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(recordSchema),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list", Required: true},
		{Name: "id", Description: "Record identifier as a UUID, as returned by twentycrm.records.list", Required: true},
		{Name: "fields", Description: "Field name to new value, from 1 to 100 fields", Required: true},
	},
	Fields: recordWriteFields,
	Examples: []capability.Example{{
		Description: "Change the city of a person",
		Arguments:   json.RawMessage(`{"object":"person","id":"11111111-2222-3333-4444-555555555555","fields":{"city":"Berlin"}}`),
	}},
}

// recordWrite is the locally checked request of a record write: the object, the record of an update, and the
// decoded values by field name. Nothing in it is checked against the schema yet.
type recordWrite struct {
	Object string
	ID     string
	Create bool
	Values map[string]any
}

type writeArguments struct {
	Object string                     `json:"object"`
	ID     string                     `json:"id"`
	Fields map[string]json.RawMessage `json:"fields"`
}

// newRecordWrite checks the object against the connection's targets and the arguments against their bounds,
// before any secret is resolved and before any request is sent. No error names a field or a value.
func newRecordWrite(resolved *config.Resolved, op string, raw json.RawMessage, create bool) (*recordWrite, error) {
	var args writeArguments
	if len(raw) > maxWriteBytes || json.Unmarshal(raw, &args) != nil || args.Fields == nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectObject(resolved, args.Object); err != nil {
		return nil, err
	}
	if len(args.Fields) > maxWriteFields {
		return nil, invalidRequest(errWriteLimit)
	}
	if !create {
		if !validUUID(args.ID) {
			return nil, invalidRequest(errWriteID)
		}
		if len(args.Fields) == 0 {
			return nil, invalidRequest(errWriteNone)
		}
	}
	write := &recordWrite{Object: args.Object, ID: args.ID, Create: create, Values: make(map[string]any, len(args.Fields))}
	for name, value := range args.Fields {
		if !fieldNamePattern.MatchString(name) || systemWriteFields[name] {
			return nil, invalidRequest(errWriteField)
		}
		decoded, err := decodeValue(value)
		if err != nil || decoded == nil {
			return nil, invalidRequest(errWriteField)
		}
		if checkValue(decoded, 0) != nil {
			return nil, invalidRequest(errWriteLimit)
		}
		write.Values[name] = decoded
	}
	return write, nil
}

func invokeRecordsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	write, err := newRecordWrite(resolved, "create record", raw, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.WriteRecord(ctx, write)
}

func invokeRecordsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	write, err := newRecordWrite(resolved, "update record", raw, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.WriteRecord(ctx, write)
}

// WriteRecord sends exactly one writing request for a locally checked write. The workspace document is the only
// other provider I/O; it is read first, and every field is checked against it before the writing request. The
// route comes from the catalog, the body from the checked values, and the path of an update only from a
// validated identifier. Twenty's answer must name that record.
func (c *Client) WriteRecord(ctx context.Context, write *recordWrite) (*Record, error) {
	op, verb, method, uncertain := "update record", "update", http.MethodPatch, recordUpdateUncertain
	if write.Create {
		op, verb, method, uncertain = "create record", "create", http.MethodPost, recordCreateUncertain
	}
	object, err := c.recordObject(ctx, op, write.Object)
	if err != nil {
		return nil, err
	}
	body, err := object.writeBody(c.scope, write)
	if err != nil {
		return nil, err
	}
	// depth 0 keeps the answer at the record itself, like every read.
	path := "/rest/" + url.PathEscape(object.Plural)
	if !write.Create {
		path += "/" + url.PathEscape(write.ID)
	}
	path += "?depth=" + noRelations
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.changeWith(ctx, op, uncertain, method, path, body, &response); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			failure.Message = errRecordPermission
		}
		return nil, err
	}
	uncertainFailure := func(message string) error {
		return provider.InvalidResponse(op, message+uncertain)
	}
	item, ok := response.Data[verb+strings.ToUpper(object.Name[:1])+object.Name[1:]]
	if !ok {
		return nil, uncertainFailure("Twenty returned an unusable record")
	}
	record, err := object.project(op, item, nil)
	if err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) {
			failure.Message += uncertain
		}
		return nil, err
	}
	if !write.Create && !strings.EqualFold(record.ID, write.ID) {
		return nil, uncertainFailure("Twenty answered with a different record than the requested one")
	}
	return record, nil
}

// writeBody checks every field against the schema of the object and returns the request body. A create also
// needs every field the create schema requires.
func (o *recordObject) writeBody(bound scope, write *recordWrite) (map[string]any, error) {
	fields := make(map[string]*catalogField, len(o.Fields))
	for i := range o.Fields {
		fields[o.Fields[i].Name] = &o.Fields[i]
	}
	names := make([]string, 0, len(write.Values))
	for name := range write.Values {
		names = append(names, name)
	}
	sort.Strings(names)
	body := make(map[string]any, len(names))
	for _, name := range names {
		field := fields[name]
		if field == nil {
			return nil, invalidRequest(errWriteField)
		}
		shape := field.UpdateShape
		if write.Create {
			shape = field.CreateShape
		}
		value, ok := o.writableValue(bound, field, shape, write.Values[name])
		if !ok {
			return nil, invalidRequest(errWriteField)
		}
		body[name] = value
	}
	if write.Create {
		for _, field := range o.Fields {
			if field.Required && !systemWriteFields[field.Name] && !givenRequired(fields, field, body) {
				return nil, invalidRequest(errWriteRequired)
			}
		}
	}
	data, err := json.Marshal(body)
	if err != nil || len(data) > maxWriteBytes {
		return nil, invalidRequest(errWriteLimit)
	}
	return body, nil
}

// givenRequired reports whether a required field of the create schema is covered. A required relation is given
// through its identifier field; a required name the create schema cannot take is left to Twenty.
func givenRequired(fields map[string]*catalogField, field catalogField, body map[string]any) bool {
	name := field.Name
	if !field.Creatable {
		identifier := fields[name+"Id"]
		if identifier == nil || !identifier.Creatable {
			return true
		}
		name += "Id"
	}
	_, given := body[name]
	return given
}

// writableValue checks one value against the shape of its field and returns what goes into the body.
func (o *recordObject) writableValue(bound scope, field *catalogField, shape *valueShape, value any) (any, bool) {
	if shape == nil {
		return nil, false
	}
	if isRichText(*field) {
		object, ok := value.(map[string]any)
		text, isText := object["markdown"].(string)
		if !ok || len(object) != 1 || !isText || shape.Props["markdown"] == nil || shape.Props["markdown"].Type != "string" {
			return nil, false
		}
		return map[string]any{"markdown": text}, true
	}
	switch shape.Type {
	case "object":
		// Actor fields record where a change came from, and Twenty sets them itself.
		if shape.Props["source"] != nil {
			return nil, false
		}
	case "array":
		// Only a multi-selection is an array of values; a list of files or of plain text is not supported.
		if shape.Items == nil || !shape.Items.Enumerated {
			return nil, false
		}
	case "string":
		if strings.HasSuffix(field.Name, "Id") && shape.Format == "uuid" && !o.reachableRelationID(bound, field.Name) {
			return nil, false
		}
	}
	if !shape.accepts(value) {
		return nil, false
	}
	return value, true
}

// accepts checks a decoded value against a shape: the JSON type, the format, the enum values, the elements of
// an array, and the parts of an object, which must all be named by the shape.
func (s *valueShape) accepts(value any) bool {
	switch s.Type {
	case "string":
		text, ok := value.(string)
		if !ok {
			return false
		}
		if s.Enumerated {
			return containsName(s.Enum, text)
		}
		switch s.Format {
		case "uuid":
			return validUUID(text)
		case "date":
			_, err := time.Parse("2006-01-02", text)
			return datePattern.MatchString(text) && err == nil
		case "date-time":
			_, err := time.Parse(time.RFC3339Nano, text)
			return err == nil
		}
		return true
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Int64()
		return err == nil
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Float64()
		return err == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		items, ok := value.([]any)
		if !ok || s.Items == nil || len(items) > maxValueItems {
			return false
		}
		for _, item := range items {
			if !s.Items.accepts(item) {
				return false
			}
		}
		return true
	case "object":
		object, ok := value.(map[string]any)
		if !ok || len(s.Props) == 0 {
			return false
		}
		for name, part := range object {
			sub := s.Props[name]
			if sub == nil || !sub.accepts(part) {
				return false
			}
		}
		return true
	}
	return false
}
