package makeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The five tools of this file list, read, create, update, and delete the data stores (the tables scenarios
// store data in) of the bound team. The records inside a store are not touched. Every store that is named is
// read first and bound back to the bound team through fetchDataStore, and a data structure named by a create
// or update is read first and bound the same way, before the one changing request.
// API (checked 2026-10-04 against developers.make.com's published API reference, not a live account):
//   - GET /data-stores (teamId required; cols[], pg[offset], pg[limit], pg[sortBy]=name), datastores:read,
//     answering {"dataStores":[{id,name,records,size,maxSize,teamId,datastructureId}]}; size and maxSize
//     are documented as strings.
//   - GET /data-stores/{id} (cols[]), answering {"dataStore":{...}}. The reference lists the scope
//     organizations:read for this one endpoint although every sibling endpoint lists datastores:read; this
//     looks like a documentation error, so the permission hint names datastores:read and mentions the
//     contradiction.
//   - POST /data-stores, datastores:write, body name (at most 128 characters), teamId, datastructureId, and
//     maxSizeMB, all required, answering {"dataStore":{...}}.
//   - PATCH /data-stores/{id}, datastores:write, body name, datastructureId, and maxSizeMB, all optional,
//     answering {"dataStore":{...}}.
//   - DELETE /data-stores?teamId=&confirmed=, datastores:write, body {"ids":[...]} or {"all":true,...},
//     answering {"dataStores":[ids]}; "confirmed=true" is required when a scenario includes the store,
//     otherwise Make answers an error and deletes nothing. Only the "ids" form with exactly one id is used.

const (
	// dataStoresSensitivity labels the stored data these tools describe.
	dataStoresSensitivity = "make-data-stores"
	// maxDataStoreSizeMB is a local ceiling for maxSizeMB; Make documents none.
	maxDataStoreSizeMB = 1 << 20
	needDataStoresRead = "the datastores:read scope; for reading a single data store Make's API reference " +
		"lists organizations:read instead, which contradicts every other data store endpoint, so if " +
		"datastores:read is present and a single read is still refused, add organizations:read"
	needDataStoresWrite = "the datastores:write scope (and datastores:read, which binds the data store; Make's " +
		"reference lists organizations:read for that read, see make.datastores.get)"
	needDataStoresCreate = "the datastores:write scope and udts:read, which binds the data structure"
	needDataStoresChange = "the datastores:write scope, datastores:read, which binds the data store (Make's " +
		"reference lists organizations:read for that read, see make.datastores.get), and, when " +
		"datastructure_id is given, udts:read, which binds the data structure"
)

var dataStoreCols = []string{"id", "name", "teamId", "records", "size", "maxSize", "datastructureId"}

var dataStoreIDSchema = idSchema

var dataStoreIDArgument = capability.Argument{Name: "datastore_id", Required: true,
	Description: "Make data store identifier; its team is always re-checked live against Make's own report, " +
		"and a Qatlas connection with a scenario allow-list reaches no data stores at all"}

var dataStoreNameSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxConnectionNameLength) + `}`

var dataStoreNameDescription = "Data store name, 1 to " + strconv.Itoa(maxConnectionNameLength) +
	" characters, without control characters; it need not be unique"

var dataStoreSizeSchema = `{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxDataStoreSizeMB) + `}`

var dataStoreSizeDescription = "Maximum size of the data store in MB, 1 to " + strconv.Itoa(maxDataStoreSizeMB)

var dataStoreSummarySchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},` +
	`"team_id":{"type":"integer"},"records":{"type":"integer"},"size":{"type":"string"},` +
	`"max_size":{"type":"string"},"datastructure_id":{"type":"integer"}},` +
	`"required":["id","team_id"],"additionalProperties":false}`

var dataStoreSummaryFields = []capability.Field{
	{Name: "id", Description: "Make data store identifier, used as datastore_id by the other data store tools"},
	{Name: "name", Description: "Data store name, untrusted data"},
	{Name: "team_id", Description: "Team of the data store; always the Qatlas connection's bound team"},
	{Name: "records", Description: "Number of records, as Make reports it"},
	{Name: "size", Description: "Current size as Make reports it, untrusted data"},
	{Name: "max_size", Description: "Maximum size as Make reports it, untrusted data"},
	{Name: "datastructure_id", Description: "Data structure the store follows"},
}

var dataStoresReadRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataStoresSensitivity,
}

func dataStoresChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dataStoresSensitivity}
}

var dataStoresList = capability.Descriptor{
	ID: Provider + ".datastores.list", Version: 1, Title: "List Make data stores",
	Description: "List the data stores of the bound team, sorted by name, page by page; never their records. " +
		"Refused on a connection with a scenario allow-list. Needs the datastores:read scope",
	Tags: []string{"make", "datastores", "list", "automation"}, Risk: dataStoresReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_stores":{"type":"array","items":` + dataStoreSummarySchema + `},` +
		`"offset":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["data_stores","offset","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "offset", Description: "Data stores to skip before this page; 0 when omitted"},
		{Name: "limit", Description: "Data stores per page, 1 to " + strconv.Itoa(maxListLimit) + "; " +
			strconv.Itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, dataStoreSummaryFields...),
		capability.Field{Name: "offset", Description: "Offset of this page, for computing the next call's offset"},
		capability.Field{Name: "has_more", Description: "True when a further page likely remains; Make reports " +
			"no total count, so this is true whenever this page was full"},
		capability.Field{Name: "count", Description: "Number of data stores returned after the team boundary " +
			"was re-applied"},
	),
	Examples: []capability.Example{{Description: "List the team's data stores", Arguments: json.RawMessage(`{}`)}},
}

var dataStoresGet = capability.Descriptor{
	ID: Provider + ".datastores.get", Version: 1, Title: "Get a Make data store",
	Description: "Read one data store of the bound team, never its records. Refused on a connection with a " +
		"scenario allow-list. Needs the datastores:read scope; Make's API reference lists organizations:read " +
		"for this one endpoint, which contradicts the other data store endpoints, so a refusal with " +
		"datastores:read present may need organizations:read as well",
	Tags: []string{"make", "datastores", "get", "automation"}, Risk: dataStoresReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":` + dataStoreIDSchema + `},` +
		`"required":["datastore_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dataStoreSummarySchema),
	Arguments:    []capability.Argument{dataStoreIDArgument},
	Fields:       dataStoreSummaryFields,
	Examples:     []capability.Example{{Description: "Read one data store", Arguments: json.RawMessage(`{"datastore_id":1}`)}},
}

var dataStoresCreate = capability.Descriptor{
	ID: Provider + ".datastores.create", Version: 1, Title: "Create a Make data store",
	Description: "Create one data store in the bound team, always the connection's own team. The data " +
		"structure must belong to the bound team, which is read from Make first; otherwise nothing is " +
		"created. A repeated call creates a second store. Refused on a connection with a scenario " +
		"allow-list. Needs the datastores:write and udts:read scopes",
	Tags: []string{"make", "datastores", "create", "automation"},
	Risk: dataStoresChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + dataStoreNameSchema + `,` +
		`"datastructure_id":` + dataStructureIDSchema + `,"max_size_mb":` + dataStoreSizeSchema + `},` +
		`"required":["name","datastructure_id","max_size_mb"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dataStoreSummarySchema),
	Arguments: []capability.Argument{
		{Name: "name", Required: true, Description: dataStoreNameDescription},
		{Name: "datastructure_id", Required: true, Description: "Data structure of the bound team the store follows"},
		{Name: "max_size_mb", Required: true, Description: dataStoreSizeDescription},
	},
	Fields: dataStoreSummaryFields,
	Examples: []capability.Example{{Description: "Create a data store",
		Arguments: json.RawMessage(`{"name":"Orders","datastructure_id":3,"max_size_mb":10}`)}},
}

var dataStoresUpdate = capability.Descriptor{
	ID: Provider + ".datastores.update", Version: 1, Title: "Update a Make data store",
	Description: "Change the name, data structure, or maximum size of one data store of the bound team; fields " +
		"not given stay as they are, and its records are not touched. A data structure must belong to the " +
		"bound team, which is read from Make first. Needs the datastores:write and datastores:read scopes, " +
		"plus udts:read when datastructure_id is given",
	Tags: []string{"make", "datastores", "update", "automation"},
	Risk: dataStoresChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":` + dataStoreIDSchema + `,` +
		`"name":` + dataStoreNameSchema + `,"datastructure_id":` + dataStructureIDSchema + `,` +
		`"max_size_mb":` + dataStoreSizeSchema + `},"required":["datastore_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dataStoreSummarySchema),
	Arguments: []capability.Argument{dataStoreIDArgument,
		{Name: "name", Description: dataStoreNameDescription},
		{Name: "datastructure_id", Description: "Data structure of the bound team the store should follow"},
		{Name: "max_size_mb", Description: dataStoreSizeDescription},
	},
	Fields: dataStoreSummaryFields,
	Examples: []capability.Example{{Description: "Raise a store's size",
		Arguments: json.RawMessage(`{"datastore_id":1,"max_size_mb":50}`)}},
}

var dataStoresDelete = capability.Descriptor{
	ID: Provider + ".datastores.delete", Version: 1, Title: "Delete a Make data store",
	Description: "Delete one data store of the bound team, with all its records, for good. A scenario that " +
		"includes the store stops working without it, so Make refuses the deletion until it is confirmed: " +
		"without confirm_scenarios_affected this tool sends no confirmation and, when Make refuses and names " +
		"the scenarios, returns them without deleting or repeating anything; with it true, the store is " +
		"deleted and the scenarios using it break. Exactly one id is sent, never the delete-all form. " +
		"Offered only when a connection's tools list names it, in no profile. Needs the datastores:write " +
		"and datastores:read scopes",
	Tags: []string{"make", "datastores", "delete", "automation"},
	Risk: dataStoresChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":` + dataStoreIDSchema + `,` +
		`"confirm_scenarios_affected":{"type":"boolean"}},"required":["datastore_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"datastore_id":{"type":"integer"},` +
		`"deleted":{"type":"boolean"},"confirmation_required":{"type":"boolean"},` +
		`"scenarios":` + affectedScenariosSchema + `},"required":["datastore_id","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{dataStoreIDArgument,
		{Name: "confirm_scenarios_affected", Description: "Set true to delete the data store even though " +
			"scenarios use it; they stop working. When omitted, nothing is deleted while scenarios use it"}},
	Fields: []capability.Field{
		{Name: "datastore_id", Description: "The data store that was addressed"},
		{Name: "deleted", Description: "True when Make deleted the data store"},
		{Name: "confirmation_required", Description: "True when Make refused the deletion because scenarios " +
			"use the store; nothing was deleted and nothing was repeated"},
		{Name: "scenarios", Description: "Scenarios Make's refusal names, at most " +
			strconv.Itoa(maxAffectedScenarios) + "; ids and names are untrusted data, names bounded"},
	},
	Examples: []capability.Example{{Description: "Delete an unused data store",
		Arguments: json.RawMessage(`{"datastore_id":1}`)}},
}

// textOrNumber decodes a value Make documents as a string but could report as a number.
type textOrNumber string

func (t *textOrNumber) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*t = textOrNumber(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*t = textOrNumber(number.String())
	return nil
}

// dataStoreJSON is the allow-list of Make's data store object.
type dataStoreJSON struct {
	ID              int64        `json:"id"`
	Name            string       `json:"name"`
	TeamID          int64        `json:"teamId"`
	Records         int64        `json:"records"`
	Size            textOrNumber `json:"size"`
	MaxSize         textOrNumber `json:"maxSize"`
	DataStructureID int64        `json:"datastructureId"`
}

// DataStoreSummary is the stable, team-checked view of one data store.
type DataStoreSummary struct {
	ID              int64  `json:"id"`
	Name            string `json:"name,omitempty"`
	TeamID          int64  `json:"team_id"`
	Records         int64  `json:"records"`
	Size            string `json:"size,omitempty"`
	MaxSize         string `json:"max_size,omitempty"`
	DataStructureID int64  `json:"datastructure_id,omitempty"`
}

func dataStoreSummaryOf(s dataStoreJSON) DataStoreSummary {
	return DataStoreSummary{ID: s.ID, Name: boundText(s.Name), TeamID: s.TeamID, Records: s.Records,
		Size: boundText(string(s.Size)), MaxSize: boundText(string(s.MaxSize)), DataStructureID: s.DataStructureID}
}

func dataStorePath(id int64) string { return "/data-stores/" + strconv.FormatInt(id, 10) }

func dataStoreColsQuery() url.Values {
	return url.Values{"cols[]": append([]string(nil), dataStoreCols...)}
}

// fetchDataStore reads one data store and binds it back to this connection's team: a store of another team
// is refused without naming whatever it belongs to.
func (c *Client) fetchDataStore(ctx context.Context, op string, id int64) (*dataStoreJSON, error) {
	var wrapper struct {
		DataStore dataStoreJSON `json:"dataStore"`
	}
	if err := c.get(ctx, op, dataStorePath(id), dataStoreColsQuery(), &wrapper, needDataStoresRead); err != nil {
		return nil, err
	}
	if wrapper.DataStore.ID != id || !c.scope.allowsTeam(wrapper.DataStore.TeamID) {
		return nil, invalidRequest("datastore_id is outside the targets of this connection")
	}
	return &wrapper.DataStore, nil
}

// changedDataStore checks a change's answer: it must name the changed store, and, since the change already
// took effect, a result outside the bound team is a provider error, not an invalid request.
func (c *Client) changedDataStore(op string, want int64, answer dataStoreJSON) (any, error) {
	if answer.ID <= 0 || (want != 0 && answer.ID != want) {
		return nil, invalidResponse(op, "Make did not report the changed data store"+uncertain)
	}
	if !c.scope.allowsTeam(answer.TeamID) {
		return nil, providerError(op, "Make did not keep the result inside this connection's targets; the "+
			"change already took effect")
	}
	return dataStoreSummaryOf(answer), nil
}

func invokeDataStoresList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list data stores"
	var input pagingArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectDataStructures(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		DataStores []dataStoreJSON `json:"dataStores"`
	}
	if err := client.get(ctx, op, "/data-stores", input.query(client.scope.teamID, dataStoreCols), &page,
		needDataStoresRead); err != nil {
		return nil, err
	}
	result := &DataStoresPage{DataStores: make([]DataStoreSummary, 0, len(page.DataStores)), Offset: input.Offset,
		HasMore: len(page.DataStores) == input.effectiveLimit()}
	for _, s := range page.DataStores {
		if !client.scope.allowsTeam(s.TeamID) || len(result.DataStores) >= maxListLimit {
			continue
		}
		result.DataStores = append(result.DataStores, dataStoreSummaryOf(s))
	}
	result.Count = len(result.DataStores)
	return result, nil
}

// DataStoresPage is one team-filtered page of data stores.
type DataStoresPage struct {
	DataStores []DataStoreSummary `json:"data_stores"`
	Offset     int                `json:"offset"`
	HasMore    bool               `json:"has_more"`
	Count      int                `json:"count"`
}

type dataStoreArguments struct {
	DataStoreID int64 `json:"datastore_id"`
}

// openDataStore validates the id locally, then opens the client and binds the data store.
func openDataStore(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, id int64) (*Client, *dataStoreJSON, error) {
	if err := selectDataStructures(resolved); err != nil {
		return nil, nil, err
	}
	if id <= 0 {
		return nil, nil, invalidRequest("datastore_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, nil, err
	}
	store, err := client.fetchDataStore(ctx, op, id)
	if err != nil {
		return nil, nil, err
	}
	return client, store, nil
}

func invokeDataStoresGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get data store"
	var input dataStoreArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	_, store, err := openDataStore(ctx, op, resolved, secrets, red, input.DataStoreID)
	if err != nil {
		return nil, err
	}
	return dataStoreSummaryOf(*store), nil
}

type dataStoreCreateArguments struct {
	Name            string `json:"name"`
	DataStructureID int64  `json:"datastructure_id"`
	MaxSizeMB       int64  `json:"max_size_mb"`
}

func validMaxSizeMB(size int64) error {
	if size < 1 || size > maxDataStoreSizeMB {
		return invalidRequest("max_size_mb must be between 1 and " + strconv.Itoa(maxDataStoreSizeMB))
	}
	return nil
}

func invokeDataStoresCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create data store"
	var input dataStoreCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validConnectionName(input.Name); err != nil {
		return nil, err
	}
	if err := validMaxSizeMB(input.MaxSizeMB); err != nil {
		return nil, err
	}
	if input.DataStructureID <= 0 {
		return nil, invalidRequest("datastructure_id must be a positive integer")
	}
	if err := selectDataStructures(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.fetchDataStructure(ctx, op, input.DataStructureID, dataStructureColsBinding); err != nil {
		return nil, err
	}
	var answer struct {
		DataStore dataStoreJSON `json:"dataStore"`
	}
	body := map[string]any{"name": input.Name, "teamId": client.scope.teamID,
		"datastructureId": input.DataStructureID, "maxSizeMB": input.MaxSizeMB}
	if err := client.change(ctx, op, http.MethodPost, "/data-stores", nil, body, &answer, needDataStoresCreate,
		uncertain); err != nil {
		return nil, err
	}
	return client.changedDataStore(op, 0, answer.DataStore)
}

type dataStoreUpdateArguments struct {
	DataStoreID     int64   `json:"datastore_id"`
	Name            *string `json:"name"`
	DataStructureID *int64  `json:"datastructure_id"`
	MaxSizeMB       *int64  `json:"max_size_mb"`
}

func invokeDataStoresUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update data store"
	var input dataStoreUpdateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	body := map[string]any{}
	if input.Name != nil {
		if err := validConnectionName(*input.Name); err != nil {
			return nil, err
		}
		body["name"] = *input.Name
	}
	if input.MaxSizeMB != nil {
		if err := validMaxSizeMB(*input.MaxSizeMB); err != nil {
			return nil, err
		}
		body["maxSizeMB"] = *input.MaxSizeMB
	}
	if input.DataStructureID != nil {
		if *input.DataStructureID <= 0 {
			return nil, invalidRequest("datastructure_id must be a positive integer")
		}
		body["datastructureId"] = *input.DataStructureID
	}
	if len(body) == 0 {
		return nil, invalidRequest("give at least one of name, datastructure_id, or max_size_mb to change")
	}
	client, _, err := openDataStore(ctx, op, resolved, secrets, red, input.DataStoreID)
	if err != nil {
		return nil, err
	}
	if input.DataStructureID != nil {
		if _, err := client.fetchDataStructure(ctx, op, *input.DataStructureID, dataStructureColsBinding); err != nil {
			return nil, err
		}
	}
	var answer struct {
		DataStore dataStoreJSON `json:"dataStore"`
	}
	if err := client.change(ctx, op, http.MethodPatch, dataStorePath(input.DataStoreID), nil, body, &answer,
		needDataStoresChange, uncertain); err != nil {
		return nil, err
	}
	return client.changedDataStore(op, input.DataStoreID, answer.DataStore)
}

type dataStoreDeleteArguments struct {
	DataStoreID              int64 `json:"datastore_id"`
	ConfirmScenariosAffected bool  `json:"confirm_scenarios_affected"`
}

// DataStoreDeletion is the answer of datastores.delete: deleted, or refused by Make until scenarios are
// confirmed.
type DataStoreDeletion struct {
	DataStoreID          int64              `json:"datastore_id"`
	Deleted              bool               `json:"deleted"`
	ConfirmationRequired bool               `json:"confirmation_required,omitempty"`
	Scenarios            []AffectedScenario `json:"scenarios,omitempty"`
}

func invokeDataStoresDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete data store"
	var input dataStoreDeleteArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, _, err := openDataStore(ctx, op, resolved, secrets, red, input.DataStoreID)
	if err != nil {
		return nil, err
	}
	query := url.Values{"teamId": {strconv.FormatInt(client.scope.teamID, 10)}}
	if input.ConfirmScenariosAffected {
		query.Set("confirmed", "true")
	}
	client.wantRefusal = !input.ConfirmScenariosAffected
	var answer struct {
		DataStores []int64 `json:"dataStores"`
	}
	err = client.change(ctx, op, http.MethodDelete, "/data-stores", query,
		map[string]any{"ids": []int64{input.DataStoreID}}, &answer, needDataStoresWrite, uncertain)
	client.wantRefusal = false
	if err != nil {
		scenarios := client.refusedConnectionScenarios()
		if client.refusalStatus == 0 || len(scenarios) == 0 {
			return nil, err
		}
		return &DataStoreDeletion{DataStoreID: input.DataStoreID, ConfirmationRequired: true, Scenarios: scenarios}, nil
	}
	if len(answer.DataStores) > 0 && (len(answer.DataStores) != 1 || answer.DataStores[0] != input.DataStoreID) {
		return nil, invalidResponse(op, "Make reported a different data store than the one deleted"+uncertain)
	}
	return &DataStoreDeletion{DataStoreID: input.DataStoreID, Deleted: true}, nil
}
