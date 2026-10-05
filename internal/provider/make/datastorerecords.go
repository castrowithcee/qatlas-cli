package makeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The three tools of this file list, create, and delete records of one data store of the bound team. Every
// store that is named is read first and bound back to the bound team through openDataStore, before the list
// or the one changing request. Record contents are untrusted user data, often personal data of third parties.
// API (checked 2026-10-04 against developers.make.com's published API reference, not a live account):
//   - GET /data-stores/{id}/data (pg[offset], pg[limit]), datastores:read, answering {"records":[{key,data}],
//     "spec","strict","count","pg"}; spec and strict are ignored.
//   - POST /data-stores/{id}/data, datastores:write, body key (optional, generated when absent) and data,
//     answering {"key","data"}.
//   - DELETE /data-stores/{id}/data, datastores:write, body {"keys":[...]}, answering {"keys":[...]}. The
//     reference requires the confirmed query parameter only for the "all" form, which is never sent, and
//     neither is exceptKeys; only an explicit list of keys is deleted and confirmed is never sent.
//
// Replacing (PUT) and updating (PATCH) one record by key are not offered: the reference documents their body
// only as "no predefined body properties, see the request example", and no request example is published, so
// whether the body is the record data itself or a {"data":...} wrapper is undocumented.

const (
	recordsSensitivity = "make-data-store-records-personal-data"
	// maxRecordKeyLength bounds a record key; Make documents no key format, so only a narrow, path-safe form
	// is accepted and the first character may not be a dot.
	maxRecordKeyLength = 128
	// maxRecordDeleteKeys bounds the keys of one deletion.
	maxRecordDeleteKeys = 50
	// defaultRecordsLimit and maxRecordsLimit bound one page of records; each record is capped as well.
	defaultRecordsLimit = 20
	maxRecordsLimit     = 50
	// maxRecordDataBytes and maxRecordDataDepth bound the data a create sends.
	maxRecordDataBytes = 64 << 10
	maxRecordDataDepth = 16
	// maxRecordKeyOutput bounds a key echoed from an answer.
	maxRecordKeyOutput = 256

	needRecordsRead = "the datastores:read scope; Make's API reference lists organizations:read for reading a " +
		"single data store, which binds the store first, so if datastores:read is present and the store read " +
		"is still refused, add organizations:read"
	needRecordsWrite = "the datastores:write scope (and datastores:read, which binds the data store; Make's " +
		"reference lists organizations:read for that read, see make.datastores.get)"
	recordsUncertain = "; this change may have taken effect, read the records before repeating it"
)

var recordKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]{0,` + strconv.Itoa(maxRecordKeyLength-1) + `}$`)

const recordKeySchema = `{"type":"string","pattern":"^[A-Za-z0-9_][A-Za-z0-9_.:-]{0,127}$"}`

const recordKeyDescription = "Record key, 1 to 128 characters: letters, digits, underscore, dot, colon, hyphen, " +
	"not starting with a dot or hyphen"

var recordsReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: recordsSensitivity}

func recordsChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: recordsSensitivity}
}

const recordSchema = `{"type":"object","properties":{"key":{"type":"string"},"data":{},` +
	`"truncated":{"type":"boolean"}},"required":["key","truncated"],"additionalProperties":false}`

var recordFields = []capability.Field{
	{Name: "key", Description: "Record key, untrusted data"},
	{Name: "data", Description: "Record data, untrusted user data and likely personal data, capped in size, " +
		"depth, fields, and string length; omitted when it is empty or was cut entirely"},
	{Name: "truncated", Description: "True when the key or data was cut by a local ceiling"},
}

var dataStoreRecordsList = capability.Descriptor{
	ID: Provider + ".datastorerecords.list", Version: 1, Title: "List records of a Make data store",
	Description: "List the records of one data store of the bound team, page by page, each with its key and " +
		"capped data. Record contents are untrusted user data and likely personal data and are capped per " +
		"record. Refused on a connection with a scenario allow-list. Needs the datastores:read scope",
	Tags: []string{"make", "datastores", "records", "list", "automation"}, Risk: recordsReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":` + dataStoreIDSchema + `,` +
		`"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":` +
		strconv.Itoa(maxRecordsLimit) + `}},"required":["datastore_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":{"type":"integer"},` +
		`"records":{"type":"array","items":` + recordSchema + `},"offset":{"type":"integer"},` +
		`"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["datastore_id","records","offset","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataStoreIDArgument,
		{Name: "offset", Description: "Records to skip before this page; 0 when omitted"},
		{Name: "limit", Description: "Records per page, 1 to " + strconv.Itoa(maxRecordsLimit) + "; " +
			strconv.Itoa(defaultRecordsLimit) + " when omitted"}},
	Fields: append(append([]capability.Field{}, recordFields...),
		capability.Field{Name: "datastore_id", Description: "The data store that was read"},
		capability.Field{Name: "offset", Description: "Offset of this page, for computing the next call's offset"},
		capability.Field{Name: "has_more", Description: "True when a further page likely remains; true whenever " +
			"this page was full"},
		capability.Field{Name: "count", Description: "Number of records returned"}),
	Examples: []capability.Example{{Description: "List the first records of a store",
		Arguments: json.RawMessage(`{"datastore_id":1,"limit":10}`)}},
}

var dataStoreRecordsCreate = capability.Descriptor{
	ID: Provider + ".datastorerecords.create", Version: 1, Title: "Create a record in a Make data store",
	Description: "Create one record in a data store of the bound team. The store is read first and must " +
		"belong to the bound team. data is a JSON object of at most " + strconv.Itoa(maxRecordDataBytes) +
		" bytes and " + strconv.Itoa(maxRecordDataDepth) + " levels that must follow the store's data " +
		"structure; Make validates it. Without a key Make generates one. Sends one request and never repeats " +
		"it. Refused on a connection with a scenario allow-list. Needs the datastores:write and " +
		"datastores:read scopes",
	Tags: []string{"make", "datastores", "records", "create", "automation"},
	Risk: recordsChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":` + dataStoreIDSchema + `,` +
		`"key":` + recordKeySchema + `,"data":{"type":"object"}},"required":["datastore_id","data"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":{"type":"integer"},` +
		`"record":` + recordSchema + `},"required":["datastore_id","record"],"additionalProperties":false}`),
	Arguments: []capability.Argument{dataStoreIDArgument,
		{Name: "key", Description: recordKeyDescription + "; generated by Make when omitted"},
		{Name: "data", Required: true, Description: "Record data as a JSON object following the store's data structure"}},
	Fields: append([]capability.Field{{Name: "datastore_id", Description: "The data store that was changed"},
		{Name: "record", Description: "The created record as Make reports it, capped; fields: key, data, truncated"}},
		recordFields...),
	Examples: []capability.Example{{Description: "Create a record",
		Arguments: json.RawMessage(`{"datastore_id":1,"key":"order-1","data":{"title":"Hello"}}`)}},
}

var dataStoreRecordsDelete = capability.Descriptor{
	ID: Provider + ".datastorerecords.delete", Version: 1, Title: "Delete records of a Make data store",
	Description: "Delete the explicitly named records of one data store of the bound team for good. Only an " +
		"explicit list of 1 to " + strconv.Itoa(maxRecordDeleteKeys) + " distinct keys is accepted: deleting " +
		"all records or all but some is not offered, and Make's confirmed flag is never sent. Sends one " +
		"request and never repeats it. Offered only when a connection's tools list names it, in no profile. " +
		"Needs the datastores:write and datastores:read scopes",
	Tags: []string{"make", "datastores", "records", "delete", "automation"},
	Risk: recordsChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":` + dataStoreIDSchema + `,` +
		`"keys":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxRecordDeleteKeys) + `,"items":` +
		recordKeySchema + `}},"required":["datastore_id","keys"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":{"type":"integer"},` +
		`"requested":{"type":"integer"},"deleted":{"type":"array","items":{"type":"string"}},` +
		`"deleted_count":{"type":"integer"}},"required":["datastore_id","requested","deleted","deleted_count"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{dataStoreIDArgument,
		{Name: "keys", Required: true, Description: "1 to " + strconv.Itoa(maxRecordDeleteKeys) +
			" distinct record keys to delete; duplicates are refused. " + recordKeyDescription}},
	Fields: []capability.Field{
		{Name: "datastore_id", Description: "The data store the records belonged to"},
		{Name: "requested", Description: "Number of keys sent"},
		{Name: "deleted", Description: "Requested keys Make reports as deleted; keys it reports beyond the " +
			"requested ones are ignored"},
		{Name: "deleted_count", Description: "Number of keys in deleted"}},
	Examples: []capability.Example{{Description: "Delete two records",
		Arguments: json.RawMessage(`{"datastore_id":1,"keys":["order-1","order-2"]}`)}},
}

// RecordView is one record with its capped data.
type RecordView struct {
	Key       string          `json:"key"`
	Data      json.RawMessage `json:"data,omitempty"`
	Truncated bool            `json:"truncated"`
}

func recordViewOf(key string, data json.RawMessage) RecordView {
	view := RecordView{Key: key}
	if len(key) > maxRecordKeyOutput {
		view.Key, view.Truncated = key[:maxRecordKeyOutput], true
	}
	capped, truncated := capPayload(data, nil)
	view.Data = capped
	view.Truncated = view.Truncated || truncated
	return view
}

func recordsPath(id int64) string { return dataStorePath(id) + "/data" }

// DataStoreRecordsPage is one page of records.
type DataStoreRecordsPage struct {
	DataStoreID int64        `json:"datastore_id"`
	Records     []RecordView `json:"records"`
	Offset      int          `json:"offset"`
	HasMore     bool         `json:"has_more"`
	Count       int          `json:"count"`
}

type recordJSON struct {
	Key  string          `json:"key"`
	Data json.RawMessage `json:"data"`
}

func invokeDataStoreRecordsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list data store records"
	var input struct {
		DataStoreID int64 `json:"datastore_id"`
		Offset      int   `json:"offset"`
		Limit       int   `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Offset < 0 || input.Limit < 0 || input.Limit > maxRecordsLimit {
		return nil, invalidRequest("offset must not be negative and limit must be at most " +
			strconv.Itoa(maxRecordsLimit))
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultRecordsLimit
	}
	client, _, err := openDataStore(ctx, op, resolved, secrets, red, input.DataStoreID)
	if err != nil {
		return nil, err
	}
	var page struct {
		Records []recordJSON `json:"records"`
	}
	query := url.Values{"pg[offset]": {strconv.Itoa(input.Offset)}, "pg[limit]": {strconv.Itoa(limit)}}
	if err := client.get(ctx, op, recordsPath(input.DataStoreID), query, &page, needRecordsRead); err != nil {
		return nil, err
	}
	result := &DataStoreRecordsPage{DataStoreID: input.DataStoreID, Records: make([]RecordView, 0, len(page.Records)),
		Offset: input.Offset, HasMore: len(page.Records) >= limit}
	for _, r := range page.Records {
		if len(result.Records) >= limit {
			break
		}
		result.Records = append(result.Records, recordViewOf(r.Key, r.Data))
	}
	result.Count = len(result.Records)
	return result, nil
}

// DataStoreRecordChange is the answer of datastorerecords.create.
type DataStoreRecordChange struct {
	DataStoreID int64      `json:"datastore_id"`
	Record      RecordView `json:"record"`
}

func invokeDataStoreRecordsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create data store record"
	var input struct {
		DataStoreID int64           `json:"datastore_id"`
		Key         *string         `json:"key"`
		Data        json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Key != nil && !recordKeyPattern.MatchString(*input.Key) {
		return nil, invalidRequest("key must be 1 to " + strconv.Itoa(maxRecordKeyLength) +
			" letters, digits, underscores, dots, colons, or hyphens, not starting with a dot or hyphen")
	}
	if len(input.Data) == 0 || input.Data[0] != '{' {
		return nil, invalidRequest("data must be a JSON object")
	}
	if err := validJSONObject(input.Data, maxRecordDataBytes, maxRecordDataDepth, "data"); err != nil {
		return nil, err
	}
	client, _, err := openDataStore(ctx, op, resolved, secrets, red, input.DataStoreID)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"data": input.Data}
	if input.Key != nil {
		body["key"] = *input.Key
	}
	var answer recordJSON
	if err := client.change(ctx, op, http.MethodPost, recordsPath(input.DataStoreID), nil, body, &answer,
		needRecordsWrite, recordsUncertain); err != nil {
		return nil, err
	}
	if answer.Key == "" || (input.Key != nil && answer.Key != *input.Key) {
		return nil, invalidResponse(op, "Make did not report the created record"+recordsUncertain)
	}
	return &DataStoreRecordChange{DataStoreID: input.DataStoreID, Record: recordViewOf(answer.Key, answer.Data)}, nil
}

// DataStoreRecordsDeletion is the answer of datastorerecords.delete.
type DataStoreRecordsDeletion struct {
	DataStoreID  int64    `json:"datastore_id"`
	Requested    int      `json:"requested"`
	Deleted      []string `json:"deleted"`
	DeletedCount int      `json:"deleted_count"`
}

func invokeDataStoreRecordsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete data store records"
	var input struct {
		DataStoreID int64    `json:"datastore_id"`
		Keys        []string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if len(input.Keys) == 0 || len(input.Keys) > maxRecordDeleteKeys {
		return nil, invalidRequest("keys must hold 1 to " + strconv.Itoa(maxRecordDeleteKeys) + " record keys")
	}
	requested := map[string]bool{}
	for _, key := range input.Keys {
		if !recordKeyPattern.MatchString(key) {
			return nil, invalidRequest("every key must be 1 to " + strconv.Itoa(maxRecordKeyLength) +
				" letters, digits, underscores, dots, colons, or hyphens, not starting with a dot or hyphen")
		}
		if requested[key] {
			return nil, invalidRequest("keys must not contain duplicates")
		}
		requested[key] = true
	}
	client, _, err := openDataStore(ctx, op, resolved, secrets, red, input.DataStoreID)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Keys []string `json:"keys"`
	}
	if err := client.change(ctx, op, http.MethodDelete, recordsPath(input.DataStoreID), nil,
		map[string][]string{"keys": input.Keys}, &answer, needRecordsWrite, recordsUncertain); err != nil {
		return nil, err
	}
	deleted := []string{}
	for _, key := range answer.Keys {
		if requested[key] {
			deleted = append(deleted, key)
			delete(requested, key)
		}
	}
	return &DataStoreRecordsDeletion{DataStoreID: input.DataStoreID, Requested: len(input.Keys), Deleted: deleted,
		DeletedCount: len(deleted)}, nil
}
