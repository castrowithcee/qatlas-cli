package makeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The two tools of this file list and read the data structures (the field layouts data stores follow) of the
// bound team; creating, changing, or deleting a structure is not offered.
// API (udts:read, checked 2026-10-04 against developers.make.com's published API reference, not a live
// account): GET /data-structures (teamId required; cols[], pg[offset], pg[limit], pg[sortBy]=name) answering
// {"dataStructures":[...]} and GET /data-structures/{id} (cols[]) answering {"dataStructure":{...}}. A
// structure reports id, teamId, name (at most 128 characters), strict, and spec, an array of typed fields
// (boolean, text, number, date, buffer, collection, array).

const (
	// dataStructuresSensitivity labels the field layouts these tools describe.
	dataStructuresSensitivity = "make-data-structures"
	needDataStructuresRead    = "the udts:read scope"
	// maxSpecFields bounds the fields of one returned specification, nested ones included; maxSpecDepth bounds
	// its nesting, and maxSpecText every string in it.
	maxSpecFields = 200
	maxSpecDepth  = 4
	maxSpecText   = 128
)

var dataStructureIDSchema = idSchema

var dataStructureIDArgument = capability.Argument{Name: "datastructure_id", Required: true,
	Description: "Make data structure identifier; its team is always re-checked live against Make's own " +
		"report, and a Qatlas connection with a scenario allow-list reaches no data structures at all"}

var dataStructureSummarySchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},` +
	`"team_id":{"type":"integer"},"strict":{"type":"boolean"},` +
	`"spec":{"type":"array","items":{"type":"object"}},"spec_truncated":{"type":"boolean"}},` +
	`"required":["id","team_id"],"additionalProperties":false}`

var dataStructureSummaryFields = []capability.Field{
	{Name: "id", Description: "Make data structure identifier, used as datastructure_id by the data store tools"},
	{Name: "name", Description: "Data structure name, untrusted data"},
	{Name: "team_id", Description: "Team of the data structure; always the Qatlas connection's bound team"},
	{Name: "strict", Description: "True when Make enforces the structure strictly on stored data"},
	{Name: "spec", Description: "Only on get: the structure's fields as name, type, label, required, " +
		"multiline, sequence, codepage, and nested spec, untrusted data; default values are not returned; at " +
		"most " + strconv.Itoa(maxSpecFields) + " fields and " + strconv.Itoa(maxSpecDepth) + " levels"},
	{Name: "spec_truncated", Description: "True when the specification was cut at the field or depth limit"},
}

var dataStructuresReadRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataStructuresSensitivity,
}

var dataStructuresList = capability.Descriptor{
	ID: Provider + ".datastructures.list", Version: 1, Title: "List Make data structures",
	Description: "List the data structures of the bound team by name, id, and strictness, sorted by name, page by " +
		"page; the field specification is read with get. Refused on a connection with a scenario allow-list. " +
		"Needs the udts:read scope",
	Tags: []string{"make", "datastructures", "list", "automation"}, Risk: dataStructuresReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"data_structures":{"type":"array","items":` + dataStructureSummarySchema + `},` +
		`"offset":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["data_structures","offset","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "offset", Description: "Data structures to skip before this page; 0 when omitted"},
		{Name: "limit", Description: "Data structures per page, 1 to " + strconv.Itoa(maxListLimit) + "; " +
			strconv.Itoa(defaultListLimit) + " when omitted"},
	},
	Fields: []capability.Field{
		{Name: "data_structures", Description: "The page's structures as id, name, team_id, and strict, untrusted data"},
		{Name: "offset", Description: "Offset of this page, for computing the next call's offset"},
		{Name: "has_more", Description: "True when a further page likely remains; Make reports no total count, " +
			"so this is true whenever this page was full"},
		{Name: "count", Description: "Number of structures returned after the team boundary was re-applied"},
	},
	Examples: []capability.Example{{Description: "List the team's data structures", Arguments: json.RawMessage(`{}`)}},
}

var dataStructuresGet = capability.Descriptor{
	ID: Provider + ".datastructures.get", Version: 1, Title: "Get a Make data structure",
	Description: "Read one data structure of the bound team with its field specification, bounded and without " +
		"default values. Refused on a connection with a scenario allow-list. Needs the udts:read scope",
	Tags: []string{"make", "datastructures", "get", "automation"}, Risk: dataStructuresReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"datastructure_id":` + dataStructureIDSchema + `},` +
		`"required":["datastructure_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dataStructureSummarySchema),
	Arguments:    []capability.Argument{dataStructureIDArgument},
	Fields:       dataStructureSummaryFields,
	Examples: []capability.Example{{Description: "Read one data structure",
		Arguments: json.RawMessage(`{"datastructure_id":1}`)}},
}

// specJSON is the allow-list of one specification field; defaults and every unknown key are never decoded.
type specJSON struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Label     string          `json:"label"`
	Required  bool            `json:"required"`
	Multiline bool            `json:"multiline"`
	Sequence  bool            `json:"sequence"`
	Codepage  string          `json:"codepage"`
	Spec      json.RawMessage `json:"spec"`
}

type dataStructureJSON struct {
	ID     int64           `json:"id"`
	TeamID int64           `json:"teamId"`
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Spec   json.RawMessage `json:"spec"`
}

// SpecField is one field of a data structure's specification.
type SpecField struct {
	Name      string      `json:"name,omitempty"`
	Type      string      `json:"type"`
	Label     string      `json:"label,omitempty"`
	Required  bool        `json:"required,omitempty"`
	Multiline bool        `json:"multiline,omitempty"`
	Sequence  bool        `json:"sequence,omitempty"`
	Codepage  string      `json:"codepage,omitempty"`
	Spec      []SpecField `json:"spec,omitempty"`
}

// DataStructureSummary is the stable, team-checked view of one data structure.
type DataStructureSummary struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name,omitempty"`
	TeamID        int64       `json:"team_id"`
	Strict        bool        `json:"strict"`
	Spec          []SpecField `json:"spec,omitempty"`
	SpecTruncated bool        `json:"spec_truncated,omitempty"`
}

func specText(value string) string {
	if len(value) > maxSpecText {
		return value[:maxSpecText]
	}
	return value
}

// specBudget counts the fields still allowed in one returned specification.
type specBudget struct {
	left      int
	truncated bool
}

// parseSpec reads an array of fields (a structure or a collection) or, for the array type, one field object,
// within the field and depth budget. Anything unreadable is dropped and counted as truncated.
func parseSpec(raw json.RawMessage, depth int, budget *specBudget) []SpecField {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var entries []specJSON
	if raw[0] == '{' {
		var one specJSON
		if json.Unmarshal(raw, &one) != nil {
			budget.truncated = true
			return nil
		}
		entries = []specJSON{one}
	} else if json.Unmarshal(raw, &entries) != nil {
		budget.truncated = true
		return nil
	}
	if depth >= maxSpecDepth {
		if len(entries) > 0 {
			budget.truncated = true
		}
		return nil
	}
	out := make([]SpecField, 0, len(entries))
	for _, entry := range entries {
		if budget.left <= 0 {
			budget.truncated = true
			break
		}
		budget.left--
		out = append(out, SpecField{Name: specText(entry.Name), Type: specText(entry.Type),
			Label: specText(entry.Label), Required: entry.Required, Multiline: entry.Multiline,
			Sequence: entry.Sequence, Codepage: specText(entry.Codepage),
			Spec: parseSpec(entry.Spec, depth+1, budget)})
	}
	return out
}

func dataStructureSummaryOf(s dataStructureJSON, withSpec bool) DataStructureSummary {
	out := DataStructureSummary{ID: s.ID, Name: boundText(s.Name), TeamID: s.TeamID, Strict: s.Strict}
	if withSpec {
		budget := &specBudget{left: maxSpecFields}
		out.Spec = parseSpec(s.Spec, 0, budget)
		out.SpecTruncated = budget.truncated
	}
	return out
}

// selectDataStructures refuses a connection with a scenario allow-list before any secret is read: data
// stores and data structures belong to no scenario, so the narrower reading is that such a connection
// reaches none.
func selectDataStructures(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if len(bound.scenarios) > 0 {
		return invalidRequest("Make data stores and data structures are not scenario-bound; a connection with " +
			"a scenario allow-list cannot reach them")
	}
	return nil
}

var dataStructureColsFull = []string{"id", "name", "teamId", "spec", "strict"}
var dataStructureColsBinding = []string{"id", "name", "teamId"}
var dataStructureColsList = []string{"id", "name", "teamId", "strict"}

type pagingArguments struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

func (p pagingArguments) query(teamID int64, cols []string) url.Values {
	limit := p.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	return url.Values{"teamId": {strconv.FormatInt(teamID, 10)}, "pg[offset]": {strconv.Itoa(p.Offset)},
		"pg[limit]": {strconv.Itoa(limit)}, "pg[sortBy]": {"name"}, "pg[sortDir]": {"asc"},
		"cols[]": append([]string(nil), cols...)}
}

func (p pagingArguments) effectiveLimit() int {
	if p.Limit == 0 {
		return defaultListLimit
	}
	return p.Limit
}

// DataStructuresPage is one team-filtered page of data structures.
type DataStructuresPage struct {
	DataStructures []DataStructureSummary `json:"data_structures"`
	Offset         int                    `json:"offset"`
	HasMore        bool                   `json:"has_more"`
	Count          int                    `json:"count"`
}

func invokeDataStructuresList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list data structures"
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
		DataStructures []dataStructureJSON `json:"dataStructures"`
	}
	if err := client.get(ctx, op, "/data-structures", input.query(client.scope.teamID, dataStructureColsList),
		&page, needDataStructuresRead); err != nil {
		return nil, err
	}
	result := &DataStructuresPage{DataStructures: make([]DataStructureSummary, 0, len(page.DataStructures)),
		Offset: input.Offset, HasMore: len(page.DataStructures) == input.effectiveLimit()}
	for _, s := range page.DataStructures {
		if !client.scope.allowsTeam(s.TeamID) || len(result.DataStructures) >= maxListLimit {
			continue
		}
		result.DataStructures = append(result.DataStructures, dataStructureSummaryOf(s, false))
	}
	result.Count = len(result.DataStructures)
	return result, nil
}

func dataStructurePath(id int64) string { return "/data-structures/" + strconv.FormatInt(id, 10) }

// fetchDataStructure reads one data structure and binds it back to this connection's team: a structure of
// another team is refused without naming whatever it belongs to.
func (c *Client) fetchDataStructure(ctx context.Context, op string, id int64, cols []string) (*dataStructureJSON, error) {
	var wrapper struct {
		DataStructure dataStructureJSON `json:"dataStructure"`
	}
	query := url.Values{"cols[]": append([]string(nil), cols...)}
	if err := c.get(ctx, op, dataStructurePath(id), query, &wrapper, needDataStructuresRead); err != nil {
		return nil, err
	}
	if wrapper.DataStructure.ID != id || !c.scope.allowsTeam(wrapper.DataStructure.TeamID) {
		return nil, invalidRequest("datastructure_id is outside the targets of this connection")
	}
	return &wrapper.DataStructure, nil
}

type dataStructureArguments struct {
	DataStructureID int64 `json:"datastructure_id"`
}

func invokeDataStructuresGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get data structure"
	var input dataStructureArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectDataStructures(resolved); err != nil {
		return nil, err
	}
	if input.DataStructureID <= 0 {
		return nil, invalidRequest("datastructure_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	structure, err := client.fetchDataStructure(ctx, op, input.DataStructureID, dataStructureColsFull)
	if err != nil {
		return nil, err
	}
	return dataStructureSummaryOf(*structure, true), nil
}
