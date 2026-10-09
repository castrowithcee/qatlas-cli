package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Finding duplicates and merging go to the fixed routes POST /rest/<plural>/duplicates and
// PATCH /rest/<plural>/merge of twenty-server's rest-api-find-duplicates and rest-api-merge-many handlers. The
// body holds only the checked identifiers, the priority index, and for a merge the fixed dryRun value of the
// tool; no argument selects between preview and merge. Both reads are POST or PATCH requests without effect and
// go through changeWith like every request with a body, which never repeats a request.
const (
	maxDuplicateIDs = 20
	// maxMergeRecords is Twenty's MUTATION_MAX_MERGE_RECORDS.
	maxMergeRecords = 9
	// maxDuplicateMatches bounds the duplicates reported for one record; Twenty sends at most 60.
	maxDuplicateMatches = 100

	errMergeIDs   = "ids must be distinct record identifiers in UUID form"
	errMergeIndex = "conflict_priority_index must be the position of one of the ids"

	errMergePermission = "the workspace role of this API key may not merge records of this object; check the " +
		"object permissions of the role in Twenty, merging deletes the source records"

	// mergeUncertain is appended to a failure of a merge whose request may have reached Twenty.
	mergeUncertain = "; this merge may have taken effect and deleted the source records, look the records up " +
		"with twentycrm.records.get before repeating it"
)

func idsSchema(min, max int) string {
	return `{"type":"array","minItems":` + strconv.Itoa(min) + `,"maxItems":` + strconv.Itoa(max) +
		`,"uniqueItems":true,"items":` + recordIDSchema + `}`
}

var duplicatesObjectArgument = capability.Argument{Name: "object", Required: true,
	Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list"}

var mergeArguments = []capability.Argument{
	duplicatesObjectArgument,
	{Name: "ids", Required: true, Description: "From 2 to 9 distinct record identifiers of that object, as UUIDs"},
	{Name: "conflict_priority_index", Required: true, Description: "Position in ids (starting at 0) of the record " +
		"whose field values win when the records disagree; this record is the one that remains"},
}

var mergeInput = json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"ids":` +
	idsSchema(2, maxMergeRecords) + `,"conflict_priority_index":{"type":"integer","minimum":0,"maximum":` +
	strconv.Itoa(maxMergeRecords-1) + `}},"required":["object","ids","conflict_priority_index"],` +
	`"additionalProperties":false}`)

var mergeFields = []capability.Field{
	{Name: "id", Description: "Identifier of the remaining record, one of the given ids"},
	{Name: "created_at", Description: "Creation timestamp of the record"},
	{Name: "updated_at", Description: "Last change timestamp of the record"},
	{Name: "fields", Description: "Field name to merged value, untrusted data"},
}

const mergeExampleArguments = `{"object":"person","ids":["11111111-2222-3333-4444-555555555555",` +
	`"66666666-7777-8888-9999-000000000000"],"conflict_priority_index":0}`

var recordsDuplicates = capability.Descriptor{
	ID:      Provider + ".records.duplicates",
	Version: 1,
	Title:   "Find duplicates of Twenty CRM records",
	Description: "Find the possible duplicates of from 1 to 20 records of one reachable object of the Twenty " +
		"workspace of a connection, by the duplicate criteria Twenty defines for the object. Changes nothing. " +
		"Fails when a given record does not exist. " + recordNote,
	Tags:     []string{"twentycrm", "crm", "records", "duplicates"},
	Risk:     recordsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"ids":` +
		idsSchema(1, maxDuplicateIDs) + `},"required":["object","ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"matches":{"type":"array","items":{"type":"object",` +
		`"properties":{"id":{"type":"string"},"total_count":{"type":"integer"},"duplicates":{"type":"array",` +
		`"items":` + recordSchema + `}},"required":["id","total_count","duplicates"],"additionalProperties":false}}},` +
		`"required":["matches"],"additionalProperties":false}`),
	Arguments: []capability.Argument{duplicatesObjectArgument,
		{Name: "ids", Required: true, Description: "From 1 to 20 distinct record identifiers of that object, as UUIDs"}},
	Fields: []capability.Field{
		{Name: "matches", Description: "One entry per given id, in order"},
		{Name: "matches[].id", Description: "The given record the duplicates belong to"},
		{Name: "matches[].total_count", Description: "Number of duplicates Twenty found, possibly more than listed"},
		{Name: "matches[].duplicates", Description: "Records with id, created_at, updated_at, and fields, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "Find the duplicates of one person",
		Arguments:   json.RawMessage(`{"object":"person","ids":["11111111-2222-3333-4444-555555555555"]}`),
	}},
}

var recordsMergePreview = capability.Descriptor{
	ID:      Provider + ".records.mergepreview",
	Version: 1,
	Title:   "Preview the merge of Twenty CRM records",
	Description: "Show the record that merging from 2 to 9 records of one reachable object of the Twenty workspace of " +
		"a connection would leave, without merging: Twenty computes the result in a dry run and changes nothing. " +
		"Use twentycrm.records.merge to merge. " + recordNote,
	Tags:         []string{"twentycrm", "crm", "records", "merge", "preview"},
	Risk:         recordsRisk,
	Provider:     Provider,
	InputSchema:  mergeInput,
	OutputSchema: json.RawMessage(recordSchema),
	Arguments:    mergeArguments,
	Fields:       mergeFields,
	Examples: []capability.Example{{
		Description: "Preview merging two people, the first one winning conflicts",
		Arguments:   json.RawMessage(mergeExampleArguments),
	}},
}

var recordsMerge = capability.Descriptor{
	ID:      Provider + ".records.merge",
	Version: 1,
	Title:   "Merge Twenty CRM records",
	Description: "Merge from 2 to 9 records of one reachable object of the Twenty workspace of a connection into " +
		"the record at conflict_priority_index: the other records are deleted and their relations are moved to " +
		"the remaining record. Look at the result with twentycrm.records.mergepreview first. Twenty's answer does " +
		"not say whether the deleted records can be restored. A connection offers this tool only when its tools " +
		"list names it. " + recordNote,
	Tags:                  []string{"twentycrm", "crm", "records", "merge"},
	Risk:                  recordWriteRisk(capability.EffectDelete, capability.IdempotencyNonIdempotent),
	RequiresToolAllowList: true,
	Provider:              Provider,
	InputSchema:           mergeInput,
	OutputSchema:          json.RawMessage(recordSchema),
	Arguments:             mergeArguments,
	Fields:                mergeFields,
	Examples: []capability.Example{{
		Description: "Merge two people into the first one",
		Arguments:   json.RawMessage(mergeExampleArguments),
	}},
}

// recordMerge is the locally checked request of the duplicate search, the merge preview, and the merge.
type recordMerge struct {
	Object string
	IDs    []string
	Index  int
}

type mergeRequestArguments struct {
	Object string   `json:"object"`
	IDs    []string `json:"ids"`
	Index  *int     `json:"conflict_priority_index"`
}

// newRecordMerge checks the object against the connection's targets and the identifiers and the index against
// their bounds, before any secret is resolved and before any request is sent. A duplicate search takes no index.
func newRecordMerge(resolved *config.Resolved, op string, raw json.RawMessage, merge bool) (*recordMerge, error) {
	var args mergeRequestArguments
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectObject(resolved, args.Object); err != nil {
		return nil, err
	}
	min, max := 1, maxDuplicateIDs
	if merge {
		min, max = 2, maxMergeRecords
	}
	if len(args.IDs) < min || len(args.IDs) > max {
		return nil, invalidRequest("ids must hold from " + strconv.Itoa(min) + " to " + strconv.Itoa(max) + " record identifiers")
	}
	seen := make(map[string]bool, len(args.IDs))
	for _, id := range args.IDs {
		key := strings.ToLower(id)
		if !validUUID(id) || seen[key] {
			return nil, invalidRequest(errMergeIDs)
		}
		seen[key] = true
	}
	request := &recordMerge{Object: args.Object, IDs: args.IDs}
	if merge {
		if args.Index == nil || *args.Index < 0 || *args.Index >= len(args.IDs) {
			return nil, invalidRequest(errMergeIndex)
		}
		request.Index = *args.Index
	}
	return request, nil
}

func invokeRecordsDuplicates(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	request, err := newRecordMerge(resolved, "find duplicates", raw, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.FindDuplicates(ctx, request)
}

func invokeRecordsMergePreview(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeMerge(ctx, resolved, secrets, red, raw, true)
}

func invokeRecordsMerge(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeMerge(ctx, resolved, secrets, red, raw, false)
}

func invokeMerge(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, dryRun bool) (any, error) {
	op := "merge records"
	if dryRun {
		op = "preview merge"
	}
	request, err := newRecordMerge(resolved, op, raw, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.MergeRecords(ctx, request, dryRun)
}

// DuplicateMatch holds the possible duplicates of one given record.
type DuplicateMatch struct {
	ID         string   `json:"id"`
	TotalCount int      `json:"total_count"`
	Duplicates []Record `json:"duplicates"`
}

// DuplicateResult is the answer of a duplicate search.
type DuplicateResult struct {
	Matches []DuplicateMatch `json:"matches"`
}

// FindDuplicates sends exactly one request with the checked identifiers. Twenty answers one entry per record
// it found, in the order of the identifiers, and leaves out records it does not find; an answer that does not
// hold one entry per identifier is therefore refused, because the entries could not be told apart.
func (c *Client) FindDuplicates(ctx context.Context, request *recordMerge) (*DuplicateResult, error) {
	const op = "find duplicates"
	object, err := c.recordObject(ctx, op, request.Object)
	if err != nil {
		return nil, err
	}
	path := "/rest/" + url.PathEscape(object.Plural) + "/duplicates?depth=" + noRelations
	var response struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	body := map[string]any{"ids": request.IDs}
	if err := c.changeWith(ctx, op, "", http.MethodPost, path, body, &response); err != nil {
		return nil, err
	}
	if len(response.Data) != len(request.IDs) {
		return nil, provider.InvalidResponse(op, "Twenty did not answer once for every given record; check that "+
			"all of them exist with twentycrm.records.get")
	}
	result := &DuplicateResult{Matches: make([]DuplicateMatch, 0, len(request.IDs))}
	for i, entry := range response.Data {
		var items []json.RawMessage
		var total int
		if json.Unmarshal(entry[object.Name+"Duplicates"], &items) != nil || items == nil ||
			len(items) > maxDuplicateMatches || json.Unmarshal(entry["totalCount"], &total) != nil || total < len(items) {
			return nil, provider.InvalidResponse(op, "Twenty returned unusable duplicates")
		}
		match := DuplicateMatch{ID: request.IDs[i], TotalCount: total, Duplicates: make([]Record, 0, len(items))}
		for _, item := range items {
			record, err := object.project(op, item, nil)
			if err != nil {
				return nil, err
			}
			match.Duplicates = append(match.Duplicates, *record)
		}
		result.Matches = append(result.Matches, match)
	}
	return result, nil
}

// MergeRecords sends exactly one PATCH request. The route comes from the workspace catalog and the body from
// the checked identifiers and the fixed dryRun value of the tool. Twenty's answer must name one of the given
// records; after a merge, a failure that leaves the result open carries the uncertainty note.
func (c *Client) MergeRecords(ctx context.Context, request *recordMerge, dryRun bool) (*Record, error) {
	op, uncertain := "merge records", mergeUncertain
	if dryRun {
		op, uncertain = "preview merge", ""
	}
	object, err := c.recordObject(ctx, op, request.Object)
	if err != nil {
		return nil, err
	}
	path := "/rest/" + url.PathEscape(object.Plural) + "/merge?depth=" + noRelations
	body := map[string]any{"ids": request.IDs, "conflictPriorityIndex": request.Index, "dryRun": dryRun}
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.changeWith(ctx, op, uncertain, http.MethodPatch, path, body, &response); err != nil {
		var failure *provider.Error
		if !dryRun && errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			failure.Message = errMergePermission
		}
		return nil, err
	}
	uncertainFailure := func(message string) error {
		return provider.InvalidResponse(op, message+uncertain)
	}
	item, ok := response.Data["merge"+strings.ToUpper(object.Name[:1])+object.Name[1:]]
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
	for _, id := range request.IDs {
		if strings.EqualFold(record.ID, id) {
			return record, nil
		}
	}
	return nil, uncertainFailure("Twenty answered with a record that was not requested")
}
