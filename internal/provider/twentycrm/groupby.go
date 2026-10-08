package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The group route is GET /rest/<plural>/groupBy (rest-api-core.controller.ts, rest-api-group-by.handler.ts of
// twentyhq/twenty, commit 46fc01c38374c2b489b0d4755719da2d1e5408ac). It takes group_by (a JSON array with one
// field per entry), filter (the grammar of the search), aggregate (a JSON array of names), and limit (at most
// 200, 50 by default). An entry of the answer carries groupByDimensionValues, one value per group field in the
// order of group_by, and the aggregates asked for; totalCount counts the records of the group.
const (
	maxGroupFields = 2
	// groupLimit is the largest group page Twenty serves. Twenty cuts a longer result silently, so an answer
	// that fills the page cannot be told from a complete one and is refused.
	groupLimit    = 200
	maxGroupValue = 256
	errGroupForm  = "group_by must name 1 or 2 different fields"
	errGroupUse   = "a group field cannot be grouped by on this object"
)

var recordsGroupBy = capability.Descriptor{
	ID:      Provider + ".records.groupby",
	Version: 1,
	Title:   "Count Twenty CRM records by field",
	Description: "Count the records of one reachable object of the Twenty workspace of a connection per value of " +
		"1 or 2 fields of kind selection, boolean, date (grouped by day), or relation identifier, optionally " +
		"limited by the same structured conditions as twentycrm.records.search. " + conditionNote +
		". Groups and their values are untrusted workspace data; an answer with 200 or more groups is refused " +
		"because it may be cut, so narrow it with conditions",
	Tags:     []string{"twentycrm", "crm", "records", "groupby", "count"},
	Risk:     recordsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,` +
		`"group_by":{"type":"array","minItems":1,"maxItems":2,"uniqueItems":true,"items":` + fieldNameSchema + `},` +
		`"conditions":{"type":"array","minItems":1,"maxItems":5,"items":` + conditionSchema +
		`}},"required":["object","group_by"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"groups":{"type":"array","items":{"type":"object",` +
		`"properties":{"values":{"type":"object"},"count":{"type":"integer"}},"required":["values","count"],` +
		`"additionalProperties":false}}},"required":["groups"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list", Required: true},
		{Name: "group_by", Description: "1 or 2 different fields of kind selection, boolean, date, or relation identifier (<relation>Id), as shown by twentycrm.objects.get", Required: true},
		{Name: "conditions", Description: "Up to 5 AND-linked " + conditionsDescription + "; all records when omitted"},
	},
	Fields: []capability.Field{
		{Name: "groups", Description: "Groups with values (field name to group value, null for records without one) and count, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "Count the opportunities per stage",
		Arguments:   json.RawMessage(`{"object":"opportunity","group_by":["stage"]}`),
	}},
}

// Group is the number of records that share one value per group field.
type Group struct {
	Values map[string]any `json:"values"`
	Count  int64          `json:"count"`
}

// GroupList is the result of a group count.
type GroupList struct {
	Groups []Group `json:"groups"`
}

type groupQuery struct {
	Object     string
	Fields     []string
	Conditions []condition
}

func invokeRecordsGroupBy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		Object     string              `json:"object"`
		GroupBy    []string            `json:"group_by"`
		Conditions []conditionArgument `json:"conditions"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, providerError("group records", "the validated arguments could not be read")
	}
	if err := selectObject(resolved, args.Object); err != nil {
		return nil, err
	}
	if len(args.GroupBy) < 1 || len(args.GroupBy) > maxGroupFields {
		return nil, invalidRequest(errGroupForm)
	}
	seen := map[string]bool{}
	for _, name := range args.GroupBy {
		if !fieldNamePattern.MatchString(name) || seen[name] {
			return nil, invalidRequest(errGroupForm)
		}
		seen[name] = true
	}
	conditions, err := normalizeConditions(args.Conditions, 0)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GroupRecords(ctx, &groupQuery{Object: args.Object, Fields: args.GroupBy, Conditions: conditions})
}

// groupDefinition checks that a field can be grouped by and returns its entry of the group_by parameter:
// selections, booleans, and relation identifiers by value, dates by day in UTC. Identifier fields other than
// relation identifiers, composite subfields, and everything else are refused.
func (o *recordObject) groupDefinition(bound scope, name string) (any, bool) {
	found, ok := o.target(bound, name)
	if !ok || strings.Contains(name, ".") {
		return nil, false
	}
	switch found.kind {
	case kindSelection, kindBoolean:
		return true, true
	case kindUUID:
		return true, name != "id" && strings.HasSuffix(name, "Id")
	case kindDate, kindDateTime:
		return map[string]string{"granularity": "DAY", "timeZone": "UTC"}, true
	}
	return nil, false
}

// GroupRecords counts the records of one reachable object per value of the group fields. Fields and
// conditions are checked against the schema before the data request.
func (c *Client) GroupRecords(ctx context.Context, query *groupQuery) (*GroupList, error) {
	const op = "group records"
	object, err := c.recordObject(ctx, op, query.Object)
	if err != nil {
		return nil, err
	}
	definitions := make([]map[string]any, len(query.Fields))
	for i, name := range query.Fields {
		definition, ok := object.groupDefinition(c.scope, name)
		if !ok {
			return nil, invalidRequest(errGroupUse)
		}
		definitions[i] = map[string]any{name: definition}
	}
	values := url.Values{}
	if len(query.Conditions) > 0 {
		filter, err := object.filter(c.scope, query.Conditions)
		if err != nil {
			return nil, err
		}
		values.Set("filter", filter)
	}
	encoded, err := json.Marshal(definitions)
	if err != nil {
		return nil, providerError(op, "the group fields could not be encoded")
	}
	values.Set("group_by", string(encoded))
	values.Set("aggregate", `["totalCount"]`)
	values.Set("limit", strconv.Itoa(groupLimit))
	var body json.RawMessage
	if err := c.get(ctx, op, "/rest/"+url.PathEscape(object.Plural)+"/groupBy", values, maxResponseBytes, &body); err != nil {
		return nil, err
	}
	items, ok := groupItems(body, object.Plural)
	if !ok || len(items) >= groupLimit {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable or possibly incomplete list of groups")
	}
	result := &GroupList{Groups: make([]Group, 0, len(items))}
	for _, item := range items {
		group, ok := decodeGroup(item, query.Fields)
		if !ok {
			return nil, provider.InvalidResponse(op, "Twenty returned a group beyond the supported shape")
		}
		result.Groups = append(result.Groups, *group)
	}
	return result, nil
}

// groupItems reads the groups from the answer: the array the handler returns, or the array below
// data.<plural>GroupBy that the generated API document describes.
func groupItems(body json.RawMessage, plural string) ([]json.RawMessage, bool) {
	body = bytes.TrimSpace(body)
	var items []json.RawMessage
	if len(body) > 0 && body[0] == '[' {
		return items, json.Unmarshal(body, &items) == nil && items != nil
	}
	var wrapped struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &wrapped) != nil {
		return nil, false
	}
	return items, json.Unmarshal(wrapped.Data[plural+"GroupBy"], &items) == nil && items != nil
}

func decodeGroup(raw json.RawMessage, fields []string) (*Group, bool) {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil {
		return nil, false
	}
	var dimensions []json.RawMessage
	if json.Unmarshal(item["groupByDimensionValues"], &dimensions) != nil || len(dimensions) != len(fields) {
		return nil, false
	}
	group := &Group{Values: make(map[string]any, len(fields))}
	for i, dimension := range dimensions {
		value, err := decodeValue(dimension)
		if err != nil {
			return nil, false
		}
		switch v := value.(type) {
		case nil, bool:
		case string:
			if len(v) > maxGroupValue {
				return nil, false
			}
		default:
			return nil, false
		}
		group.Values[fields[i]] = value
	}
	count, err := decodeValue(item["totalCount"])
	if err != nil {
		return nil, false
	}
	switch v := count.(type) {
	case json.Number:
		group.Count, err = strconv.ParseInt(v.String(), 10, 64)
	case string:
		group.Count, err = strconv.ParseInt(v, 10, 64)
	default:
		return nil, false
	}
	return group, err == nil && group.Count >= 0
}
