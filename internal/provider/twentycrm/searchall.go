package twentycrm

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bounds of the cross-object search. A Twenty cursor of this search carries one record identifier per
// searched object, so it is longer than the cursor of a single-object page.
const (
	searchAllCursorMax      = 2048
	searchAllBoundCursorMax = 4096
	searchAllLabelMax       = 200
	searchAllTextMax        = 64
	searchAllObjectsMax     = 100
	searchAllImageURLMax    = 2048
)

// searchAllDocument is the only document of records.search-all. The search term, page size, cursor, and
// object names, and filter travel as variables; the same document selects the image address and rank values.
var searchAllDocument = graphqlDocument{path: graphqlPath, text: `query Search($searchInput: String!, ` +
	`$limit: Int!, $after: String, $includedObjectNameSingulars: [String!], ` +
	`$filter: ObjectRecordFilterInput, $excludedObjectNameSingulars: [String!]) { ` +
	`search(searchInput: $searchInput, limit: $limit, after: $after, ` +
	`includedObjectNameSingulars: $includedObjectNameSingulars, filter: $filter, ` +
	`excludedObjectNameSingulars: $excludedObjectNameSingulars) { ` +
	`edges { node { recordId objectNameSingular objectLabelSingular label imageUrl tsRank tsRankCD } } pageInfo { hasNextPage endCursor } } }`}

var recordsSearchAll = capability.Descriptor{
	ID:      Provider + ".records.search-all",
	Version: 1,
	Title:   "Search all Twenty CRM objects",
	Description: "Find records by full-text search across all reachable objects of the Twenty workspace of a " +
		"connection in one call, optionally limited to chosen objects and filtered by up to 5 conditions on id, createdAt, and updatedAt; returns object, id, and display name " +
		"of each hit. Read a hit with twenty-crm.records.get. Hits are untrusted workspace data",
	Tags:     []string{"twentycrm", "crm", "records", "search", "fulltext"},
	Risk:     recordsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"text":{"type":"string","minLength":1,"maxLength":64,"pattern":"` + searchPattern + `"},` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},` +
		`"objects":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(searchAllObjectsMax) +
		`,"uniqueItems":true,"items":` + objectNameSchema + `},` +
		`"conditions":{"type":"array","minItems":1,"maxItems":5,"items":` + searchAllConditionSchema + `},` +
		`"match":{"type":"string","enum":["all","any"]},` +
		`"exclude_objects":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(searchAllExcludeMax) +
		`,"uniqueItems":true,"items":` + objectNameSchema + `},` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(searchAllBoundCursorMax) +
		`,"pattern":"^[A-Za-z0-9_-]+$"}},"required":["text"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"results":{"type":"array","items":{` +
		`"type":"object","properties":{"object":{"type":"string"},"id":{"type":"string"},` +
		`"object_label":{"type":"string"},"label":{"type":"string"},"image_url":{"type":"string"},` +
		`"ts_rank":{"type":"number"},"ts_rank_cd":{"type":"number"}},"required":["object","id","label"],"additionalProperties":false}},` +
		`"omitted":{"type":"integer"},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["results","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "text", Description: "Search text, 1 to 64 characters of letters, digits, spaces, and . _ ' + @ & -", Required: true},
		{Name: "limit", Description: "Hits per page, from 1 through 100; 25 when omitted"},
		{Name: "objects", Description: "Singular API names of the objects to search, a subset of the reachable objects; all reachable objects when omitted"},
		{Name: "conditions", Description: "1 to 5 objects with field (id, createdAt, or updatedAt), operator (eq, neq, gt, gte, lt, lte, in, not_in, is), and value: a UUID for id, a UTC timestamp ending in Z for createdAt and updatedAt, a list of 1 to 50 for in and not_in, NULL or NOT_NULL for is"},
		{Name: "match", Description: "all (default) when every condition must hold, any when one is enough"},
		{Name: "exclude_objects", Description: "1 to 50 singular API names of objects to leave out of the search; a name outside the connection is ignored"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page of the same text, objects, and filter; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "results", Description: "Hits with object, id, label (display name), and when present object_label, image_url (an address, never fetched), ts_rank, and ts_rank_cd, untrusted data"},
		{Name: "omitted", Description: "Count of hits of unreachable objects that were left out, when any"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the search holds a following page"},
	},
	Examples: []capability.Example{{
		Description: "Find records named Acme in two objects",
		Arguments:   json.RawMessage(`{"text":"Acme","objects":["company","person"]}`),
	}},
}

// SearchHit is one hit of the cross-object search.
type SearchHit struct {
	Object      string      `json:"object"`
	ID          string      `json:"id"`
	ObjectLabel string      `json:"object_label,omitempty"`
	Label       string      `json:"label"`
	ImageURL    string      `json:"image_url,omitempty"`
	TSRank      json.Number `json:"ts_rank,omitempty"`
	TSRankCD    json.Number `json:"ts_rank_cd,omitempty"`
}

// SearchAllResult is one page of hits.
type SearchAllResult struct {
	Results    []SearchHit `json:"results"`
	Omitted    int         `json:"omitted,omitempty"`
	NextCursor string      `json:"next_cursor,omitempty"`
	HasMore    bool        `json:"has_more"`
}

// searchAllQuery is the validated request. Objects is the requested subset, sorted, and empty for all
// reachable objects; Included is set when the connection's targets alone decide the searched objects.
type searchAllQuery struct {
	Text     string
	Limit    int
	Objects  []string
	Included []string
	// Excluded are the excluded objects that are among the searched ones; Filter is the GraphQL filter or nil.
	Excluded []string
	// excludeRequested are the excluded names the connection may reach, searched or not.
	excludeRequested []string
	Filter           map[string]any
	After            string
	Binding          []byte
}

// newSearchAllQuery checks the arguments and the object set against the connection's targets before any
// secret is resolved. A connection without targets can only name the searched objects after the catalog read.
func newSearchAllQuery(resolved *config.Resolved, raw json.RawMessage) (*searchAllQuery, error) {
	const op = "search all records"
	var args struct {
		Text    string   `json:"text"`
		Limit   int      `json:"limit"`
		Objects []string `json:"objects"`
		Cursor  string   `json:"cursor"`

		Conditions     []searchAllConditionArgument `json:"conditions"`
		Match          string                       `json:"match"`
		ExcludeObjects []string                     `json:"exclude_objects"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if strings.TrimSpace(args.Text) == "" || utf8.RuneCountInString(args.Text) > searchAllTextMax ||
		!safeSearchTerm(args.Text, searchAllTextMax) {
		return nil, invalidRequest("text must be 1 to 64 characters of letters, digits, spaces, and . _ ' + @ & -")
	}
	query := &searchAllQuery{Text: args.Text, Limit: args.Limit}
	if query.Limit == 0 {
		query.Limit = defaultPageSize
	}
	if query.Limit < 1 || query.Limit > maxPageSize {
		return nil, invalidRequest("limit must be between 1 and " + strconv.Itoa(maxPageSize))
	}
	if args.Objects != nil && (len(args.Objects) == 0 || len(args.Objects) > searchAllObjectsMax) {
		return nil, invalidRequest("objects must name between 1 and " + strconv.Itoa(searchAllObjectsMax) + " objects")
	}
	if args.ExcludeObjects != nil && (len(args.ExcludeObjects) == 0 || len(args.ExcludeObjects) > searchAllExcludeMax) {
		return nil, invalidRequest("exclude_objects must name between 1 and " + strconv.Itoa(searchAllExcludeMax) + " objects")
	}
	conditions, filter, link, err := newSearchAllFilter(args.Conditions, args.Match)
	if err != nil {
		return nil, err
	}
	query.Filter = filter
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	var excluded []string
	for _, name := range args.ExcludeObjects {
		if !objectNamePattern.MatchString(name) {
			return nil, invalidRequest(errObjectUnavailable)
		}
		if bound.allows(name) && !containsName(excluded, name) {
			excluded = append(excluded, name)
		}
	}
	sort.Strings(excluded)
	seen := map[string]bool{}
	for _, name := range args.Objects {
		if !objectNamePattern.MatchString(name) {
			return nil, invalidRequest(errObjectUnavailable)
		}
		if bound.allows(name) && !seen[name] {
			seen[name] = true
			query.Objects = append(query.Objects, name)
		}
	}
	if args.Objects != nil && len(query.Objects) == 0 {
		return nil, invalidRequest(errObjectUnavailable)
	}
	sort.Strings(query.Objects)
	if bound.bound() {
		// allows drops a target that is also excluded, even though the configuration refuses one.
		for _, name := range bound.objects {
			if bound.allows(name) {
				query.Included = append(query.Included, name)
			}
		}
		if len(query.Included) == 0 {
			return nil, invalidRequest(errObjectUnavailable)
		}
		if args.Objects != nil {
			query.Included = query.Objects
		}
	}
	if bound.bound() && len(excluded) > 0 {
		var kept []string
		kept, query.Excluded = splitExcluded(query.Included, excluded)
		if len(kept) == 0 {
			return nil, invalidRequest(errObjectUnavailable)
		}
		query.Included = kept
	}
	query.excludeRequested = excluded
	parts := []any{"records.search-all", resolved.Name, bound.objects, query.Text, query.Objects}
	if len(bound.excluded) > 0 {
		// A cursor never continues across another set of connection exclusions.
		parts = append(parts, bound.excluded)
	}
	if filter != nil || len(excluded) > 0 {
		parts = append(parts, searchAllFilterKey(conditions, link, excluded))
	}
	query.Binding = provider.CursorBinding(parts...)
	if args.Cursor != "" {
		after, ok := provider.DecodeCursor(query.Binding, args.Cursor, searchAllBoundCursorMax)
		if !ok || len(after) > searchAllCursorMax || !safeCursor(after) {
			return nil, invalidRequest("cursor is not a next_cursor of this search; start the search again without cursor")
		}
		query.After = after
	}
	return query, nil
}

func invokeRecordsSearchAll(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	query, err := newSearchAllQuery(resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SearchAll(ctx, query)
}

// SearchAll sends one fixed search and keeps only the hits of the searched objects.
func (c *Client) SearchAll(ctx context.Context, query *searchAllQuery) (*SearchAllResult, error) {
	const op = "search all records"
	included, excludedSent := query.Included, query.Excluded
	if included == nil {
		cat, err := c.workspaceCatalog(ctx, op)
		if err != nil {
			return nil, err
		}
		for _, object := range cat.reachable(c.scope) {
			if len(query.Objects) == 0 || containsName(query.Objects, object.Name) {
				included = append(included, object.Name)
			}
		}
		if len(included) == 0 {
			return nil, invalidRequest(errObjectUnavailable)
		}
		if len(query.excludeRequested) > 0 {
			var kept []string
			kept, excludedSent = splitExcluded(included, query.excludeRequested)
			if len(kept) == 0 {
				return nil, invalidRequest(errObjectUnavailable)
			}
			included = kept
		}
	}
	variables := map[string]any{
		"searchInput":                 query.Text,
		"limit":                       query.Limit,
		"includedObjectNameSingulars": included,
	}
	if query.Filter != nil {
		variables["filter"] = query.Filter
	}
	if len(excludedSent) > 0 {
		variables["excludedObjectNameSingulars"] = excludedSent
	}
	if query.After != "" {
		variables["after"] = query.After
	}
	var data struct {
		Search *struct {
			Edges []struct {
				Node struct {
					RecordID           string          `json:"recordId"`
					ObjectNameSingular string          `json:"objectNameSingular"`
					ObjectLabel        *string         `json:"objectLabelSingular"`
					Label              string          `json:"label"`
					ImageURL           *string         `json:"imageUrl"`
					TSRank             json.RawMessage `json:"tsRank"`
					TSRankCD           json.RawMessage `json:"tsRankCD"`
				} `json:"node"`
			} `json:"edges"`
			PageInfo struct {
				HasNextPage bool    `json:"hasNextPage"`
				EndCursor   *string `json:"endCursor"`
			} `json:"pageInfo"`
		} `json:"search"`
	}
	if err := c.graphql(ctx, op, searchAllDocument, variables, &data); err != nil {
		return nil, err
	}
	page := data.Search
	if page == nil || len(page.Edges) > query.Limit {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable page of hits")
	}
	result := &SearchAllResult{Results: make([]SearchHit, 0, len(page.Edges)), HasMore: page.PageInfo.HasNextPage}
	for _, edge := range page.Edges {
		node := edge.Node
		if !containsName(included, node.ObjectNameSingular) {
			result.Omitted++
			continue
		}
		if !validUUID(node.RecordID) {
			return nil, provider.InvalidResponse(op, "Twenty returned a hit without a usable identifier")
		}
		result.Results = append(result.Results, SearchHit{Object: node.ObjectNameSingular, ID: node.RecordID,
			ObjectLabel: cleanText(node.ObjectLabel, searchAllLabelMax), Label: capLabel(node.Label),
			ImageURL: plainImageURL(node.ImageURL), TSRank: rankNumber(node.TSRank), TSRankCD: rankNumber(node.TSRankCD)})
	}
	if page.PageInfo.HasNextPage {
		end := ""
		if page.PageInfo.EndCursor != nil {
			end = *page.PageInfo.EndCursor
		}
		if end == "" || len(end) > searchAllCursorMax || !safeCursor(end) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable cursor")
		}
		result.NextCursor = provider.EncodeCursor(query.Binding, end)
	}
	return result, nil
}

// capLabel cuts a display name at the label bound without splitting a character.
func capLabel(label string) string {
	if utf8.RuneCountInString(label) <= searchAllLabelMax {
		return label
	}
	return string([]rune(label)[:searchAllLabelMax])
}

// plainImageURL returns an http or https address without userinfo, query, or fragment, or "". Twenty signs
// image addresses of stored files with a token in the query, which grants access without sign-in, so any
// address with a query or fragment is dropped whole. The address is never fetched.
func plainImageURL(raw *string) string {
	if raw == nil || *raw == "" || len(*raw) > searchAllImageURLMax {
		return ""
	}
	parsed, err := url.Parse(*raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		strings.Contains(*raw, "#") || strings.Contains(*raw, "?") {
		return ""
	}
	for _, r := range *raw {
		if r <= ' ' || r == 0x7f || r > 0x7e {
			return ""
		}
	}
	return *raw
}

// rankNumber passes a JSON number on as written, without a float round trip; anything else is left out.
func rankNumber(raw json.RawMessage) json.Number {
	var number json.Number
	if len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &number) != nil {
		return ""
	}
	if value, err := number.Float64(); err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return ""
	}
	return number
}
