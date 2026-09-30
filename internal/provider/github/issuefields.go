package github

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// github.issuefields.list reads the organization issue fields of a repository, inherited from its
// organization, or, with organization instead, directly of an organization; the same exclusive selection
// github.rulesets.list applies, except that the organization branch is checked the broad way
// github.issuetypes.list checks its own owner argument, since organization-wide issue fields are exactly as
// broad a resource as an organization's issue types. github.issuefields.set writes one or more field values
// of an issue of a repository a connection allows, by the field and option identifiers
// github.issuefields.list reports; GitHub applies every value of one call as a single mutation, so it either
// applies as a whole or not at all.
//
// Verified 2026-09-29 against GitHub's public GraphQL reference schema
// (https://docs.github.com/public/fpt/schema.docs.graphql): Repository.issueFields and
// Organization.issueFields (an IssueFieldsConnection of IssueFieldText, IssueFieldNumber, IssueFieldDate,
// IssueFieldSingleSelect, and IssueFieldMultiSelect) and the setIssueFieldValue mutation
// (IssueFieldCreateOrUpdateInput with fieldId and exactly one of textValue, numberValue, dateValue,
// singleSelectOptionId, multiSelectOptionIds, or delete) carry no @preview marker in this schema, the same
// stability signal github.subissues.* and github.issuedependencies.* were verified by in the first
// milestone; they are not reachable from docs.github.com/en/graphql/reference's rendered pages yet, so this
// is stated on the schema alone. IssueFieldCreateOrUpdateInput also carries confidence, rationale, and
// suggest properties the official github/github-mcp-server (MIT) exposes only behind its own
// update_issue_suggestions GraphQL feature-flag context, an agent-suggestion workflow of that server's own,
// not a capability GitHub documents as generally available; Qatlas does not send them.

const (
	fieldIDSchema  = `{"type":"string","minLength":1,"maxLength":200,"pattern":"` + nodeIDPattern + `"}`
	dateOnlySchema = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}`
)

const issueFieldOptionProperties = `"id":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"color":{"type":"string"}`
const issueFieldOptionRequired = `"required":["id","name"],"additionalProperties":false`

const issueFieldProperties = `"id":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"data_type":{"type":"string"},"visibility":{"type":"string"},` +
	`"options":{"type":"array","items":{"type":"object","properties":{` + issueFieldOptionProperties + `},` +
	issueFieldOptionRequired + `}}`
const issueFieldRequired = `"required":["id","name","data_type","visibility"],"additionalProperties":false`

var issueFieldsRepositoryArgument = capability.Argument{Name: "repository", Description: "Repository as " +
	"OWNER/REPO whose issue fields this call reads, inherited from its organization; exactly one of " +
	"repository or organization is required, never both; must lie inside the targets when the connection " +
	"lists any"}

var issueFieldsOrganizationArgument = capability.Argument{Name: "organization", Description: "Organization " +
	"as orgs/LOGIN whose issue fields this call reads directly; exactly one of repository or organization is " +
	"required, never both; must be an owner target the connection allows, or an organization owning one of " +
	"its repository targets, when it lists any"}

var issueFieldsRepositoryField = capability.Field{Name: "repository", Description: "Repository this call " +
	"read, as OWNER/REPO; present only when repository addressed the call"}

var issueFieldsOrganizationField = capability.Field{Name: "organization", Description: "Organization this " +
	"call read, as orgs/LOGIN; present only when organization addressed the call"}

var issueFieldsList = capability.Descriptor{
	ID:      Provider + ".issuefields.list",
	Version: 1,
	Title:   "List GitHub issue fields",
	Description: "List one bounded batch of the organization issue fields of a repository, inherited from " +
		"its organization, or, with organization instead, directly of an organization a connection " +
		"allows: text, number, date, single-select, and multi-select fields with the options of a select " +
		"field; exactly one of repository or organization is required",
	Tags:        []string{"github", "issues", "issuefields", "list"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"repository":` + repoSchema + `,"organization":` + ownerSchema + `,` + pagingKeys),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"issue_fields":{"type":"array","items":{"type":"object","properties":{` + issueFieldProperties + `},` +
		issueFieldRequired + `}},"repository":{"type":"string"},"organization":{"type":"string"},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["issue_fields","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{issueFieldsRepositoryArgument, issueFieldsOrganizationArgument},
		pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "issue_fields", Description: "Issue fields: id and single_select_option_id/" +
			"multi_select_option_ids of github.issuefields.set need the field's own id, name and description " +
			"(untrusted data), data_type (text, number, date, single_select, or multi_select), visibility " +
			"(all or org_only), and for a select field its options, each with id, name, description, and color"},
		issueFieldsRepositoryField, issueFieldsOrganizationField,
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the issue fields of a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}, {
		Description: "List the issue fields of an organization directly",
		Arguments:   json.RawMessage(`{"organization":"orgs/octo-org"}`),
	}},
}

const issueFieldValueSchema = `{"type":"object","properties":{"field_id":` + fieldIDSchema + `,` +
	`"text_value":{"type":"string","maxLength":65536},"number_value":{"type":"number"},` +
	`"date_value":` + dateOnlySchema + `,"single_select_option_id":` + fieldIDSchema + `,` +
	`"multi_select_option_ids":{"type":"array","maxItems":50,"items":` + fieldIDSchema + `},` +
	`"delete":{"type":"boolean"}},"required":["field_id"],"additionalProperties":false}`
const issueFieldValuesSchema = `{"type":"array","minItems":1,"maxItems":20,"items":` + issueFieldValueSchema + `}`

var issueFieldsSet = capability.Descriptor{
	ID:      Provider + ".issuefields.set",
	Version: 1,
	Title:   "Set GitHub issue field values",
	Description: "Set or clear text, number, date, single-select, and multi-select organization issue field " +
		"values on one issue of a repository a connection allows, by field and option identifier, " +
		"as github.issuefields.list reports them; GitHub applies every value of one call as a single mutation",
	Tags:        []string{"github", "issues", "issuefields", "set"},
	Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:    Provider,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"fields":`+issueFieldValuesSchema, "number", "fields"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"url":{"type":"string"},"updated":{"type":"integer"}},` +
		`"required":["number","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
		{Name: "fields", Description: "Field values to set, each with field_id as github.issuefields.list " +
			"reports it, and exactly one of text_value, number_value, date_value (YYYY-MM-DD), " +
			"single_select_option_id, multi_select_option_ids ([] clears a multi-select field), or delete: " +
			"true to remove the value; at most 20", Required: true},
	},
	Fields: []capability.Field{
		{Name: "url", Description: "Web address of the issue"},
		{Name: "updated", Description: "Count of field values sent, which GitHub applied as a whole"},
	},
	Examples: []capability.Example{{
		Description: "Set a text field and clear a select field",
		Arguments: json.RawMessage(`{"number":42,"fields":[` +
			`{"field_id":"IF_kwDOAxxxx","text_value":"Backend"},{"field_id":"IF_kwDOAyyyy","delete":true}]}`),
	}},
}

// selectIssueFieldsTarget reads github.issuefields.list's own exclusive repository or organization argument:
// a repository call is checked the way every other repository tool is, and an organization call the broad
// way github.issuetypes.list checks its own owner argument. It runs before a credential is resolved.
func selectIssueFieldsTarget(resolved *config.Resolved, raw json.RawMessage) (target, error) {
	if resolved == nil {
		return target{}, providerError("open", "no connection was selected")
	}
	var arguments struct {
		Repository   string `json:"repository"`
		Organization string `json:"organization"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return target{}, unreadable("select issue fields target")
	}
	hasRepository := strings.TrimSpace(arguments.Repository) != ""
	hasOrganization := strings.TrimSpace(arguments.Organization) != ""
	if hasRepository == hasOrganization {
		return target{}, invalidRequest("give exactly one of repository or organization, never both or neither")
	}
	if hasRepository {
		return selectTarget(resolved, kindRepository, raw)
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return target{}, providerError("open", err.Error())
	}
	return selectFieldsOwner(allowed, arguments.Organization)
}

// selectFieldsOwner resolves the organization argument of github.issuefields.list's organization-wide mode
// against the connection's targets, exactly the way selectIssueTypesOwner resolves github.issuetypes.list's
// owner argument: an explicit owner target, a repository target of that organization, or a connection
// without targets allow it.
func selectFieldsOwner(allowed allowlist, value string) (target, error) {
	parsed, err := parseTarget(value)
	if err != nil || parsed.kind != kindOwner || strings.TrimSpace(value) != value {
		return target{}, invalidRequest("organization must be orgs/LOGIN")
	}
	if parsed.scope != "orgs" {
		return target{}, invalidRequest("organization must be an organization, as orgs/LOGIN; issue fields " +
			"are organization-wide")
	}
	if !allowed.lists(parsed, kindRepository) {
		return target{}, invalidRequest("organization is outside the targets of this connection; pass one " +
			"they allow, or add it or one of its repositories to the connection's targets")
	}
	for _, entry := range allowed {
		if entry.kind == kindOwner && strings.EqualFold(entry.String(), parsed.String()) {
			parsed = entry
		}
	}
	return parsed, nil
}

// issueFieldsLocate adds the repository or the organization github.issuefields.list addressed to its
// successful result, under that argument's own name, the way rulesetLocate does for the ruleset tools.
func (t target) issueFieldsLocate(value any, err error) (any, error) {
	if err != nil {
		return value, err
	}
	encoded, merr := json.Marshal(value)
	var fields map[string]json.RawMessage
	if merr != nil || json.Unmarshal(encoded, &fields) != nil || fields == nil {
		return value, nil
	}
	name := "repository"
	if t.kind == kindOwner {
		name = "organization"
	}
	fields[name], _ = json.Marshal(t.argument())
	return fields, nil
}

const issueFieldSelection = `id name description dataType visibility ` +
	`... on IssueFieldSingleSelect{options{id name description color}} ` +
	`... on IssueFieldMultiSelect{options{id name description color}}`

const issueFieldsRepoQuery = `query($owner:String!,$name:String!,$first:Int!,$after:String){` +
	`repository(owner:$owner,name:$name){issueFields(first:$first,after:$after){` +
	`pageInfo{hasNextPage endCursor} nodes{` + issueFieldSelection + `}}}}`

const issueFieldsOrgQuery = `query($owner:String!,$first:Int!,$after:String){` +
	`organization(login:$owner){issueFields(first:$first,after:$after){` +
	`pageInfo{hasNextPage endCursor} nodes{` + issueFieldSelection + `}}}}`

type issueFieldOptionJSON struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Color       string  `json:"color"`
}

type issueFieldNodeJSON struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description *string                `json:"description"`
	DataType    string                 `json:"dataType"`
	Visibility  string                 `json:"visibility"`
	Options     []issueFieldOptionJSON `json:"options"`
}

type issueFieldsConnectionJSON struct {
	PageInfo pageInfoJSON         `json:"pageInfo"`
	Nodes    []issueFieldNodeJSON `json:"nodes"`
}

type issueFieldsRepoPageJSON struct {
	Repository *struct {
		IssueFields *issueFieldsConnectionJSON `json:"issueFields"`
	} `json:"repository"`
}

type issueFieldsOrgPageJSON struct {
	Organization *struct {
		IssueFields *issueFieldsConnectionJSON `json:"issueFields"`
	} `json:"organization"`
}

// IssueFieldOption is one option of a single-select or a multi-select issue field.
type IssueFieldOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
}

// IssueField is one organization issue field, as github.issuefields.list reports it.
type IssueField struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	DataType    string             `json:"data_type"`
	Visibility  string             `json:"visibility"`
	Options     []IssueFieldOption `json:"options,omitempty"`
}

func issueFieldOf(op string, node issueFieldNodeJSON) (IssueField, error) {
	if node.ID == "" || node.Name == "" || node.DataType == "" {
		return IssueField{}, invalidEntry(op, "an issue field")
	}
	field := IssueField{ID: node.ID, Name: node.Name, DataType: strings.ToLower(node.DataType),
		Visibility: strings.ToLower(node.Visibility)}
	if node.Description != nil {
		field.Description = *node.Description
	}
	for _, option := range node.Options {
		item := IssueFieldOption{ID: option.ID, Name: option.Name, Color: strings.ToLower(option.Color)}
		if option.Description != nil {
			item.Description = *option.Description
		}
		field.Options = append(field.Options, item)
	}
	return field, nil
}

// issueFieldsListOptions is the paging of github.issuefields.list.
type issueFieldsListOptions struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

func (o *issueFieldsListOptions) normalize(bound target) ([]byte, string, error) {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return nil, "", err
	}
	o.Limit = limit
	binding := fingerprint("issuefields", bound.String())
	after, err := decodeCursor(binding, o.Cursor)
	return binding, after, err
}

// IssueFieldList is one batch of issue fields.
type IssueFieldList struct {
	IssueFields []IssueField `json:"issue_fields"`
	NextCursor  string       `json:"next_cursor,omitempty"`
	HasMore     bool         `json:"has_more"`
}

// listIssueFields reads one server page of the issue fields of the bound target: a repository, inherited
// from its organization, or an organization directly.
func (c *Client) listIssueFields(ctx context.Context, options issueFieldsListOptions, binding []byte,
	after string) (*IssueFieldList, error) {
	const op = "list issue fields"
	var connection *issueFieldsConnectionJSON
	if c.target.kind == kindRepository {
		var page issueFieldsRepoPageJSON
		variables := map[string]any{"owner": c.target.owner, "name": c.target.repo, "first": options.Limit, "after": nil}
		if after != "" {
			variables["after"] = after
		}
		if err := c.graphql(ctx, op, issueFieldsRepoQuery, variables, &page); err != nil {
			return nil, actionsFailure(err, issueFieldsReadPermission)
		}
		if page.Repository == nil {
			return nil, notFound(op, subject{in: c.target})
		}
		connection = page.Repository.IssueFields
	} else {
		var page issueFieldsOrgPageJSON
		variables := map[string]any{"owner": c.target.owner, "first": options.Limit, "after": nil}
		if after != "" {
			variables["after"] = after
		}
		if err := c.graphql(ctx, op, issueFieldsOrgQuery, variables, &page); err != nil {
			return nil, actionsFailure(err, issueFieldsReadPermission)
		}
		if page.Organization == nil {
			return nil, notFound(op, subject{in: c.target})
		}
		connection = page.Organization.IssueFields
	}
	if connection == nil {
		return nil, invalidResponse(op, false)
	}
	result := &IssueFieldList{IssueFields: make([]IssueField, 0, len(connection.Nodes))}
	for _, node := range connection.Nodes {
		field, err := issueFieldOf(op, node)
		if err != nil {
			return nil, err
		}
		result.IssueFields = append(result.IssueFields, field)
	}
	if connection.PageInfo.HasNextPage {
		if connection.PageInfo.EndCursor == "" {
			return nil, invalidResponse(op, false)
		}
		result.HasMore, result.NextCursor = true, encodeCursor(binding, connection.PageInfo.EndCursor)
	}
	return result, nil
}

// issueFieldsReadPermission names what a token needs to read the issue fields of a repository or an
// organization. GitHub decides on every request; this names the requirement and never claims what the
// configured token holds.
const issueFieldsReadPermission = "GitHub refused this token the issue fields of this repository or " +
	"organization; reading them needs read:org on a classic token, or, as far as GitHub documents it, the " +
	"organization permission Issue types: read, or Administration: read, on a fine-grained token"

const issueFieldsChangePermission = "GitHub refused this change of the issue field values of this issue; it " +
	"needs repo on a classic token, or Issues: read and write on a fine-grained token"

func invokeIssueFieldsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options issueFieldsListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable("list issue fields")
	}
	bound, err := selectIssueFieldsTarget(resolved, raw)
	if err != nil {
		return nil, err
	}
	binding, after, err := options.normalize(bound)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.issueFieldsLocate(client.listIssueFields(ctx, options, binding, after))
}

// issueFieldValueArgument is one entry of github.issuefields.set's own fields argument.
type issueFieldValueArgument struct {
	FieldID              string   `json:"field_id"`
	TextValue            *string  `json:"text_value"`
	NumberValue          *float64 `json:"number_value"`
	DateValue            *string  `json:"date_value"`
	SingleSelectOptionID *string  `json:"single_select_option_id"`
	MultiSelectOptionIDs []string `json:"multi_select_option_ids"`
	Delete               *bool    `json:"delete"`
}

// check applies the bound every field value of github.issuefields.set shares: exactly one value or delete.
func (f issueFieldValueArgument) check() error {
	if strings.TrimSpace(f.FieldID) == "" {
		return invalidRequest("fields.field_id is required")
	}
	count := 0
	if f.TextValue != nil {
		count++
	}
	if f.NumberValue != nil {
		count++
	}
	if f.DateValue != nil {
		count++
	}
	if f.SingleSelectOptionID != nil {
		count++
	}
	if f.MultiSelectOptionIDs != nil {
		count++
	}
	if f.Delete != nil && *f.Delete {
		count++
	}
	if count != 1 {
		return invalidRequest("each field needs exactly one of text_value, number_value, date_value, " +
			"single_select_option_id, multi_select_option_ids, or delete: true")
	}
	return nil
}

// mutationInput builds the GraphQL IssueFieldCreateOrUpdateInput of one field value.
func (f issueFieldValueArgument) mutationInput() map[string]any {
	input := map[string]any{"fieldId": f.FieldID}
	switch {
	case f.TextValue != nil:
		input["textValue"] = *f.TextValue
	case f.NumberValue != nil:
		input["numberValue"] = *f.NumberValue
	case f.DateValue != nil:
		input["dateValue"] = *f.DateValue
	case f.SingleSelectOptionID != nil:
		input["singleSelectOptionId"] = *f.SingleSelectOptionID
	case f.MultiSelectOptionIDs != nil:
		input["multiSelectOptionIds"] = f.MultiSelectOptionIDs
	case f.Delete != nil && *f.Delete:
		input["delete"] = true
	}
	return input
}

const setIssueFieldValueMutation = `mutation($issueId:ID!,$fields:[IssueFieldCreateOrUpdateInput!]!){` +
	`setIssueFieldValue(input:{issueId:$issueId,issueFields:$fields}){issue{id url}}}`

// IssueFieldsChange confirms one github.issuefields.set call.
type IssueFieldsChange struct {
	Number  int    `json:"number"`
	URL     string `json:"url,omitempty"`
	Updated int    `json:"updated"`
}

// setIssueFields sends every field value of one call as a single GraphQL mutation, which GitHub applies as a
// whole: a failure changes nothing this call sent.
func (c *Client) setIssueFields(ctx context.Context, number int, values []issueFieldValueArgument) (*IssueFieldsChange, error) {
	const op = "set issue fields"
	nodeID, err := c.issueNodeID(ctx, op, number)
	if err != nil {
		return nil, actionsFailure(err, issueFieldsChangePermission)
	}
	inputs := make([]map[string]any, len(values))
	for i, value := range values {
		inputs[i] = value.mutationInput()
	}
	var answer struct {
		SetIssueFieldValue struct {
			Issue struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"issue"`
		} `json:"setIssueFieldValue"`
	}
	variables := map[string]any{"issueId": nodeID, "fields": inputs}
	if err := c.mutate(ctx, op, setIssueFieldValueMutation, variables, &answer); err != nil {
		return nil, actionsFailure(err, issueFieldsChangePermission)
	}
	return &IssueFieldsChange{Number: number, URL: answer.SetIssueFieldValue.Issue.URL, Updated: len(values)}, nil
}

func invokeIssueFieldsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Number int                       `json:"number"`
		Fields []issueFieldValueArgument `json:"fields"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("set issue fields")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkNumber(arguments.Number); err != nil {
		return nil, err
	}
	if len(arguments.Fields) == 0 {
		return nil, invalidRequest("fields must name at least one field value")
	}
	for _, value := range arguments.Fields {
		if err := value.check(); err != nil {
			return nil, err
		}
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.setIssueFields(ctx, arguments.Number, arguments.Fields))
}

func issueFieldsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: issueFieldsList, Handler: capability.Handler(invokeIssueFieldsList)},
		{Descriptor: issueFieldsSet, Handler: capability.Handler(invokeIssueFieldsSet)},
	}
}
