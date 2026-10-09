package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// A batch goes to the fixed routes of twenty-server's rest-api-create-many, rest-api-update-many, and
// rest-api-delete-many handlers. Update and delete select their records only by the filter id[in]:[...] built
// from validated, distinct UUIDs: Twenty acts on every record when the filter is missing, so an empty list is
// refused before any I/O. Whether Twenty applies a batch as a whole when one record fails is not established,
// so an answer that does not name exactly the requested records is reported as an uncertain partial effect.
const (
	maxBatchRecords = 60

	errBatchCount = "a batch takes 1 to 60 records"
	errBatchIDs   = "ids must hold 1 to 60 distinct record identifiers in UUID form"

	// batchUncertain is appended to a failure of a batch whose request may have reached Twenty.
	batchUncertain = "; some or all of these records may have been changed, and a repeated batch can duplicate " +
		"or change them again; read the records before repeating it"
)

const batchNote = "One to 60 records per call, sent as one request that is never repeated; Twenty may apply a batch " +
	"in part, and Qatlas then reports an uncertain result instead of success. "

const batchIDsSchema = `{"type":"array","minItems":1,"maxItems":60,"uniqueItems":true,"items":` + recordIDSchema + `}`

const batchRecordsOutput = `{"type":"object","properties":{"records":{"type":"array","items":` + recordSchema +
	`},"count":{"type":"integer"}},"required":["records","count"],"additionalProperties":false}`

var batchObjectArgument = capability.Argument{Name: "object", Required: true,
	Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list"}

var batchRecordFields = []capability.Field{
	{Name: "records", Description: "The records as Twenty reports them after the write, untrusted data"},
	{Name: "count", Description: "Number of records Twenty returned"},
}

// batchcreate carries the effect update because upsert may change existing records, and a descriptor holds one
// risk; a connection needs the update permission for it even when upsert is not set.
var recordsBatchCreate = capability.Descriptor{
	ID: Provider + ".records.batchcreate", Version: 1, Title: "Create Twenty CRM records in a batch",
	Description: batchNote + "Create up to 60 records in one reachable object, each with the field values of its entry " +
		"in records; every record is checked like twentycrm.records.create, and one refused record refuses the " +
		"batch. With upsert true Twenty matches each entry against existing records by the unique fields of the " +
		"object and changes the match instead of creating a record, so the call can create or change records. " +
		writeNote,
	Tags:     []string{"twentycrm", "crm", "records", "batch", "create"},
	Risk:     recordWriteRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,` +
		`"records":{"type":"array","minItems":1,"maxItems":60,"items":{"type":"object","maxProperties":100}},` +
		`"upsert":{"type":"boolean"}},"required":["object","records"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(batchRecordsOutput),
	Arguments: []capability.Argument{batchObjectArgument,
		{Name: "records", Required: true, Description: "1 to 60 objects of field name to value, each with every required field"},
		{Name: "upsert", Description: "True to match entries against existing records by unique fields and change them; default false"}},
	Fields: batchRecordFields,
	Examples: []capability.Example{{Description: "Create two people",
		Arguments: json.RawMessage(`{"object":"person","records":[{"name":{"firstName":"Ada"},` +
			`"companyId":"11111111-2222-3333-4444-555555555555"},{"name":{"firstName":"Bob"},` +
			`"companyId":"11111111-2222-3333-4444-555555555555"}]}`)}},
}

var recordsBatchUpdate = capability.Descriptor{
	ID: Provider + ".records.batchupdate", Version: 1, Title: "Update Twenty CRM records in a batch",
	Description: batchNote + "Set the same field values on up to 60 records of one reachable object, selected by ids; " +
		"fields not named stay as they are, and the fields are checked like twentycrm.records.update. " + writeNote,
	Tags:     []string{"twentycrm", "crm", "records", "batch", "update"},
	Risk:     recordWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"ids":` + batchIDsSchema +
		`,"fields":{"type":"object","minProperties":1,"maxProperties":100}},` +
		`"required":["object","ids","fields"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(batchRecordsOutput),
	Arguments: []capability.Argument{batchObjectArgument,
		{Name: "ids", Required: true, Description: "1 to 60 distinct record identifiers as UUIDs, as returned by twentycrm.records.list"},
		{Name: "fields", Required: true, Description: "Field name to new value, from 1 to 100 fields, applied to every record"}},
	Fields: batchRecordFields,
	Examples: []capability.Example{{Description: "Set the city of two people",
		Arguments: json.RawMessage(`{"object":"person","ids":["11111111-2222-3333-4444-555555555555",` +
			`"66666666-7777-8888-9999-000000000000"],"fields":{"city":"Berlin"}}`)}},
}

var recordsBatchDelete = capability.Descriptor{
	ID: Provider + ".records.batchdelete", Version: 1, Title: "Delete Twenty CRM records in a batch",
	Description: batchNote + "Move up to 60 records of one reachable object, selected by ids, to the trash. They stay " +
		"recoverable one by one with twentycrm.records.restore. " + removeNote,
	Tags:     []string{"twentycrm", "crm", "records", "batch", "delete"},
	Risk:     recordWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"ids":` + batchIDsSchema +
		`},"required":["object","ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},` +
		`"count":{"type":"integer"}},"required":["deleted","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{batchObjectArgument,
		{Name: "ids", Required: true, Description: "1 to 60 distinct record identifiers as UUIDs, as returned by twentycrm.records.list"}},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Twenty confirmed every requested record"},
		{Name: "count", Description: "Number of records deleted"},
	},
	Examples: []capability.Example{{Description: "Delete two people", Arguments: json.RawMessage(
		`{"object":"person","ids":["11111111-2222-3333-4444-555555555555","66666666-7777-8888-9999-000000000000"]}`)}},
}

// BatchRecords is the answer of batchcreate and batchupdate; BatchDeleted the answer of batchdelete.
type BatchRecords struct {
	Records []Record `json:"records"`
	Count   int      `json:"count"`
}

type BatchDeleted struct {
	Deleted bool `json:"deleted"`
	Count   int  `json:"count"`
}

// recordBatch is the locally checked request of a batch. Nothing in it is checked against the schema yet.
type recordBatch struct {
	Object string
	Mode   string // create, update, or delete
	Upsert bool
	IDs    []string         // lower case, distinct (update, delete)
	Values []map[string]any // one entry per record (create) or the single shared set (update)
}

type batchArguments struct {
	Object  string                       `json:"object"`
	Records []map[string]json.RawMessage `json:"records"`
	IDs     []string                     `json:"ids"`
	Fields  map[string]json.RawMessage   `json:"fields"`
	Upsert  bool                         `json:"upsert"`
}

// newRecordBatch checks the object against the connection's targets and the arguments against their bounds
// before any secret is resolved and before any request is sent. No error names a field, a value, or an id.
func newRecordBatch(resolved *config.Resolved, op, mode string, raw json.RawMessage) (*recordBatch, error) {
	var args batchArguments
	if len(raw) > maxWriteBytes || json.Unmarshal(raw, &args) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectObject(resolved, args.Object); err != nil {
		return nil, err
	}
	batch := &recordBatch{Object: args.Object, Mode: mode, Upsert: args.Upsert}
	if mode == "create" {
		if len(args.Records) < 1 || len(args.Records) > maxBatchRecords {
			return nil, invalidRequest(errBatchCount)
		}
		for _, record := range args.Records {
			if record == nil {
				return nil, invalidRequest(errWriteField)
			}
			values, err := decodeWriteFields(record)
			if err != nil {
				return nil, err
			}
			batch.Values = append(batch.Values, values)
		}
		return batch, nil
	}
	if len(args.IDs) < 1 || len(args.IDs) > maxBatchRecords {
		return nil, invalidRequest(errBatchIDs)
	}
	seen := make(map[string]bool, len(args.IDs))
	for _, id := range args.IDs {
		if !validUUID(id) || seen[strings.ToLower(id)] {
			return nil, invalidRequest(errBatchIDs)
		}
		seen[strings.ToLower(id)] = true
		batch.IDs = append(batch.IDs, strings.ToLower(id))
	}
	if mode == "update" {
		if len(args.Fields) == 0 {
			return nil, invalidRequest(errWriteNone)
		}
		values, err := decodeWriteFields(args.Fields)
		if err != nil {
			return nil, err
		}
		batch.Values = []map[string]any{values}
	}
	return batch, nil
}

func invokeRecordsBatch(mode, op string) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		batch, err := newRecordBatch(resolved, op, mode, raw)
		if err != nil {
			return nil, err
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return client.WriteBatch(ctx, op, batch)
	}
}

var (
	invokeRecordsBatchCreate = invokeRecordsBatch("create", "create records")
	invokeRecordsBatchUpdate = invokeRecordsBatch("update", "update records")
	invokeRecordsBatchDelete = invokeRecordsBatch("delete", "delete records")
)

// WriteBatch sends exactly one writing request for a locally checked batch. Every record is checked against
// the schema before it. The answer must name exactly the records the request concerns.
func (c *Client) WriteBatch(ctx context.Context, op string, batch *recordBatch) (any, error) {
	object, err := c.recordObject(ctx, op, batch.Object)
	if err != nil {
		return nil, err
	}
	var body any
	method, denied := http.MethodPatch, errRecordPermission
	values := url.Values{}
	values.Set("depth", noRelations)
	path := "/rest/" + url.PathEscape(object.Plural)
	switch batch.Mode {
	case "create":
		items := make([]map[string]any, 0, len(batch.Values))
		total := 0
		for _, value := range batch.Values {
			item, err := object.writeBody(c.scope, &recordWrite{Object: batch.Object, Create: true, Values: value})
			if err != nil {
				return nil, err
			}
			encoded, _ := json.Marshal(item)
			if total += len(encoded); total > maxWriteBytes {
				return nil, invalidRequest(errWriteLimit)
			}
			items = append(items, item)
		}
		body, method = items, http.MethodPost
		path = "/rest/batch/" + url.PathEscape(object.Plural)
		if batch.Upsert {
			values.Set("upsert", "true")
		}
	case "update":
		item, err := object.writeBody(c.scope, &recordWrite{Object: batch.Object, Values: batch.Values[0]})
		if err != nil {
			return nil, err
		}
		body = item
	default:
		method, denied = http.MethodDelete, errDeletePermission
		values.Set("soft_delete", "true")
	}
	if batch.Mode != "create" {
		values.Set("filter", "id[in]:["+strings.Join(batch.IDs, ",")+"]")
	}
	if batch.Mode == "delete" {
		values.Del("depth")
	}
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.changeWith(ctx, op, batchUncertain, method, path+"?"+values.Encode(), body, &response); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			failure.Message = denied
		}
		return nil, err
	}
	uncertainFailure := func(message string) error {
		return provider.InvalidResponse(op, message+batchUncertain)
	}
	key := batch.Mode + strings.ToUpper(object.Plural[:1]) + object.Plural[1:]
	var items []json.RawMessage
	if json.Unmarshal(response.Data[key], &items) != nil || items == nil {
		return nil, uncertainFailure("Twenty returned an unusable batch answer")
	}
	want := len(batch.IDs)
	if batch.Mode == "create" {
		want = len(batch.Values)
	}
	if len(items) != want {
		return nil, uncertainFailure("Twenty answered for a different number of records than requested")
	}
	records := make([]Record, 0, len(items))
	answered := make(map[string]bool, len(items))
	for _, item := range items {
		var record *Record
		if batch.Mode == "delete" {
			var named struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(item, &named) != nil || !validUUID(named.ID) {
				return nil, uncertainFailure("Twenty returned a record without a usable identifier")
			}
			record = &Record{ID: named.ID}
		} else if record, err = object.project(op, item, nil); err != nil {
			var failure *provider.Error
			if errors.As(err, &failure) {
				failure.Message += batchUncertain
			}
			return nil, err
		}
		id := strings.ToLower(record.ID)
		if answered[id] && !batch.Upsert {
			return nil, uncertainFailure("Twenty answered with the same record twice")
		}
		answered[id] = true
		records = append(records, *record)
	}
	if batch.Mode != "create" {
		for _, id := range batch.IDs {
			if !answered[id] {
				return nil, uncertainFailure("Twenty answered with other records than the requested ones")
			}
		}
	}
	if batch.Mode == "delete" {
		return &BatchDeleted{Deleted: true, Count: len(records)}, nil
	}
	return &BatchRecords{Records: records, Count: len(records)}, nil
}
