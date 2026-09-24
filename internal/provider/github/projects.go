package github

import (
	"context"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// statusFieldName is the project field the status filters address. GitHub projects create it by default.
const statusFieldName = "Status"

// defaultStatusNot is the open roster: without a status filter, finished items stay out.
var defaultStatusNot = []string{"Done"}

// ItemListOptions are the structured filters and the paging of the project item list. Nothing else reaches
// GitHub: the project is the connection's, and the filter expression is built here, never taken from the
// caller.
type ItemListOptions struct {
	Status     []string `json:"status"`
	StatusNot  []string `json:"status_not"`
	Type       string   `json:"type"`
	Repository string   `json:"repository"`
	Assignee   string   `json:"assignee"`
	Labels     []string `json:"labels"`
	Limit      int      `json:"limit"`
	Cursor     string   `json:"cursor"`

	// defaulted records that StatusNot is the default roster rather than the caller's choice.
	defaulted bool
}

// normalize applies the defaults and bounds of one list request and returns the GitHub cursor it continues
// after. The application core validates the input schema first; this keeps a direct caller inside the same
// rules.
func (o *ItemListOptions) normalize(bound target) (string, error) {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return "", err
	}
	o.Limit = limit
	if o.Status == nil && o.StatusNot == nil {
		o.StatusNot, o.defaulted = append([]string(nil), defaultStatusNot...), true
	}
	for _, list := range []struct {
		name   string
		values []string
	}{{"status", o.Status}, {"status_not", o.StatusNot}, {"labels", o.Labels}} {
		if err := checkFilterList(list.name, list.values); err != nil {
			return "", err
		}
	}
	switch o.Type {
	case "", "issue", "pull_request", "draft_issue":
	default:
		return "", invalidRequest("type must be issue, pull_request, or draft_issue")
	}
	if o.Repository != "" && !validRepository(o.Repository) {
		return "", invalidRequest("repository must be owner/name")
	}
	if o.Assignee != "" && !validLogin(o.Assignee) {
		return "", invalidRequest("assignee must be a GitHub login")
	}
	return decodeCursor(o.binding(bound), o.Cursor)
}

// binding identifies the target and the filters of a list request. Values are compared the way GitHub
// compares them, without case, so spellings that select the same items share their cursors.
func (o *ItemListOptions) binding(bound target) []byte {
	return fingerprint("projects.items", bound.String(), folded(o.Status), folded(o.StatusNot), o.Type,
		strings.ToLower(o.Repository), strings.ToLower(o.Assignee), folded(o.Labels))
}

func folded(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = strings.ToLower(value)
	}
	sort.Strings(out)
	return out
}

// query translates the structured filters into the project filter grammar. Every free value is one quoted
// term whose characters were checked, so no value can open a second term. Several values of one filter are
// a comma list, which GitHub reads as "any of"; several negated terms exclude each of their values.
func (o *ItemListOptions) query() string {
	terms := make([]string, 0, 8)
	if len(o.Status) > 0 {
		terms = append(terms, "status:"+quotedList(o.Status))
	}
	for _, value := range o.StatusNot {
		terms = append(terms, "-status:"+quoted(value))
	}
	switch o.Type {
	case "issue":
		terms = append(terms, "is:issue")
	case "pull_request":
		terms = append(terms, "is:pr")
	case "draft_issue":
		terms = append(terms, "is:draft")
	}
	if o.Repository != "" {
		terms = append(terms, "repo:"+o.Repository)
	}
	if o.Assignee != "" {
		terms = append(terms, "assignee:"+o.Assignee)
	}
	if len(o.Labels) > 0 {
		terms = append(terms, "label:"+quotedList(o.Labels))
	}
	return strings.Join(terms, " ")
}

func quoted(value string) string { return `"` + value + `"` }

func quotedList(values []string) string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = quoted(value)
	}
	return strings.Join(out, ",")
}

// resolveStatus binds the status filters to the Status field of the project. A positive value must name an
// option; its canonical spelling is used from then on. The default roster is dropped for a project without
// a Status field, while an explicit status filter on such a project is refused.
func (o *ItemListOptions) resolveStatus(info *projectInfo) error {
	if info.statusID == "" {
		if len(o.Status) > 0 || (len(o.StatusNot) > 0 && !o.defaulted) {
			return invalidRequest("this project has no Status field to filter by")
		}
		o.StatusNot = nil
		return nil
	}
	for i, value := range o.Status {
		option, ok := info.option(value)
		if !ok {
			return invalidRequest("a status value is not an option of this project's Status field; options are " +
				strings.Join(info.statusOptions, ", "))
		}
		o.Status[i] = option
	}
	for i, value := range o.StatusNot {
		if option, ok := info.option(value); ok {
			o.StatusNot[i] = option
		}
	}
	return nil
}

// matches verifies one item against the structured filters. GitHub already filtered the batch; this check
// keeps the contract exact where the server reading of a filter is broader. A value that was cut off by a
// nested page bound cannot be verified here and is left to the server's answer.
func (o *ItemListOptions) matches(item normalizedItem) bool {
	switch o.Type {
	case "":
	case item.Type:
	default:
		return false
	}
	if o.Repository != "" && !strings.EqualFold(item.Repository, o.Repository) {
		return false
	}
	if o.Assignee != "" && item.assigneesComplete && !containsFold(item.Assignees, o.Assignee) {
		return false
	}
	if len(o.Labels) > 0 && item.labelsComplete && !anyFold(item.Labels, o.Labels) {
		return false
	}
	if len(o.Status) > 0 && (item.Status != "" || item.fieldsComplete) && !containsFold(o.Status, item.Status) {
		return false
	}
	if item.Status != "" && containsFold(o.StatusNot, item.Status) {
		return false
	}
	return true
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func anyFold(values, wanted []string) bool {
	for _, want := range wanted {
		if containsFold(values, want) {
			return true
		}
	}
	return false
}

// ItemList is the normalised batch of project items.
type ItemList struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// Item is the stable Qatlas view of one project item. Body is set only by the detail read.
type Item struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Title      string         `json:"title,omitempty"`
	Number     int            `json:"number,omitempty"`
	Repository string         `json:"repository,omitempty"`
	State      string         `json:"state,omitempty"`
	Status     string         `json:"status,omitempty"`
	Fields     map[string]any `json:"fields"`
	Assignees  []string       `json:"assignees"`
	Labels     []string       `json:"labels"`
	URL        string         `json:"url,omitempty"`
	Body       *string        `json:"body,omitempty"`
}

// normalizedItem carries whether its nested lists are complete, which the filter verification needs.
type normalizedItem struct {
	Item
	assigneesComplete bool
	labelsComplete    bool
	fieldsComplete    bool
}

// The GraphQL documents. Only fixed text is concatenated; every value travels as a variable.
const fieldSelection = `fields(first:50){nodes{... on ProjectV2FieldCommon{id name dataType} ` +
	`... on ProjectV2SingleSelectField{options{name}}}}`

const fieldRef = `field{... on ProjectV2FieldCommon{id}}`

const peopleAndLabels = `assignees(first:10){totalCount nodes{login}} labels(first:20){totalCount nodes{name}}`

const itemFragment = `fragment itemFields on ProjectV2Item{id type ` +
	`fieldValues(first:30){totalCount nodes{` +
	`... on ProjectV2ItemFieldSingleSelectValue{name ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldTextValue{text ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldNumberValue{number ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldDateValue{date ` + fieldRef + `} ` +
	`... on ProjectV2ItemFieldIterationValue{title ` + fieldRef + `}}} ` +
	`content{__typename ` +
	`... on Issue{number title issueState:state url repository{nameWithOwner} ` + peopleAndLabels + `} ` +
	`... on PullRequest{number title pullRequestState:state url repository{nameWithOwner} ` + peopleAndLabels + `} ` +
	`... on DraftIssue{title assignees(first:10){totalCount nodes{login}}}}}`

const itemsQuery = `query($project:ID!,$first:Int!,$after:String,$query:String!){project:node(id:$project){` +
	`... on ProjectV2{items(first:$first,after:$after,query:$query){pageInfo{hasNextPage endCursor} ` +
	`edges{cursor node{...itemFields}}}}}} ` + itemFragment

// ownerField is the GraphQL root field of the owner kind of a project target.
func (t target) ownerField() string {
	if t.scope == "orgs" {
		return "organization"
	}
	return "user"
}

func (c *Client) projectQuery() string {
	return `query($owner:String!,$number:Int!){owner:` + c.target.ownerField() +
		`(login:$owner){projectV2(number:$number){id ` + fieldSelection + `}}}`
}

func (c *Client) itemQuery() string {
	return `query($owner:String!,$number:Int!,$item:ID!){owner:` + c.target.ownerField() +
		`(login:$owner){projectV2(number:$number){id ` + fieldSelection + `}} ` +
		`item:node(id:$item){... on ProjectV2Item{...itemFields project{id} ` +
		`content{... on Issue{body} ... on DraftIssue{body}}}}} ` + itemFragment
}

func (c *Client) projectVariables() map[string]any {
	return map[string]any{"owner": c.target.owner, "number": c.target.number}
}

type fieldJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DataType string `json:"dataType"`
	Options  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"options"`
	Configuration *struct {
		Iterations          []iterationJSON `json:"iterations"`
		CompletedIterations []iterationJSON `json:"completedIterations"`
	} `json:"configuration"`
}

type iterationJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type projectJSON struct {
	ID     string `json:"id"`
	Fields struct {
		Nodes []fieldJSON `json:"nodes"`
	} `json:"fields"`
}

type ownerJSON struct {
	Owner *struct {
		ProjectV2 *projectJSON `json:"projectV2"`
	} `json:"owner"`
}

// projectInfo is the field model of the bound project, resolved once per request.
type projectInfo struct {
	id            string
	fields        map[string]fieldJSON
	statusID      string
	statusOptions []string
}

func (p *projectInfo) option(value string) (string, bool) {
	for _, option := range p.statusOptions {
		if strings.EqualFold(option, value) {
			return option, true
		}
	}
	return "", false
}

// projectInfoOf reads the field model of the project an owner query answered. An owner or project GitHub
// left out is one it does not hold or does not show to this token.
func projectInfoOf(op string, project target, owner ownerJSON) (*projectInfo, error) {
	if owner.Owner == nil || owner.Owner.ProjectV2 == nil || owner.Owner.ProjectV2.ID == "" {
		return nil, notFound(op, subject{in: project})
	}
	info := &projectInfo{id: owner.Owner.ProjectV2.ID, fields: map[string]fieldJSON{}}
	for _, field := range owner.Owner.ProjectV2.Fields.Nodes {
		if field.ID == "" {
			continue
		}
		info.fields[field.ID] = field
		if field.DataType == "SINGLE_SELECT" && strings.EqualFold(field.Name, statusFieldName) && info.statusID == "" {
			info.statusID = field.ID
			for _, option := range field.Options {
				info.statusOptions = append(info.statusOptions, option.Name)
			}
		}
	}
	return info, nil
}

// project resolves the bound project and its field model with one query.
func (c *Client) project(ctx context.Context, op string) (*projectInfo, error) {
	var owner ownerJSON
	if err := c.graphql(ctx, op, c.projectQuery(), c.projectVariables(), &owner); err != nil {
		return nil, err
	}
	return projectInfoOf(op, c.target, owner)
}

type countedLogins struct {
	TotalCount int `json:"totalCount"`
	Nodes      []struct {
		Login string `json:"login"`
	} `json:"nodes"`
}

type countedLabels struct {
	TotalCount int `json:"totalCount"`
	Nodes      []struct {
		Name string `json:"name"`
	} `json:"nodes"`
}

type fieldValueJSON struct {
	Name   *string  `json:"name"`
	Text   *string  `json:"text"`
	Number *float64 `json:"number"`
	Date   *string  `json:"date"`
	Title  *string  `json:"title"`
	Field  struct {
		ID string `json:"id"`
	} `json:"field"`
}

type itemJSON struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	FieldValues struct {
		TotalCount int              `json:"totalCount"`
		Nodes      []fieldValueJSON `json:"nodes"`
	} `json:"fieldValues"`
	Content *struct {
		Typename         string `json:"__typename"`
		Number           int    `json:"number"`
		Title            string `json:"title"`
		IssueState       string `json:"issueState"`
		PullRequestState string `json:"pullRequestState"`
		URL              string `json:"url"`
		Repository       *struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
		Assignees countedLogins `json:"assignees"`
		Labels    countedLabels `json:"labels"`
		Body      *string       `json:"body"`
	} `json:"content"`
	Project *struct {
		ID string `json:"id"`
	} `json:"project"`
}

// itemTypes maps the GitHub item types to the stable Qatlas names.
var itemTypes = map[string]string{
	"ISSUE": "issue", "PULL_REQUEST": "pull_request", "DRAFT_ISSUE": "draft_issue", "REDACTED": "redacted",
}

// normalize reduces one GitHub item to the compact Qatlas view. Field values are named through the field
// model resolved for this request; only single-select, text, number, date, and iteration values are kept,
// and the Status value becomes its own property.
func (info *projectInfo) normalize(raw itemJSON) normalizedItem {
	kind, ok := itemTypes[raw.Type]
	if !ok {
		kind = strings.ToLower(raw.Type)
	}
	item := normalizedItem{
		Item:              Item{ID: raw.ID, Type: kind, Fields: map[string]any{}, Assignees: []string{}, Labels: []string{}},
		assigneesComplete: true, labelsComplete: true,
		fieldsComplete: raw.FieldValues.TotalCount <= len(raw.FieldValues.Nodes),
	}
	for _, value := range raw.FieldValues.Nodes {
		field, known := info.fields[value.Field.ID]
		if !known {
			continue
		}
		switch {
		case field.ID == info.statusID && value.Name != nil:
			item.Status = *value.Name
		case field.DataType == "SINGLE_SELECT" && value.Name != nil:
			item.Fields[field.Name] = *value.Name
		case field.DataType == "TEXT" && value.Text != nil:
			item.Fields[field.Name] = *value.Text
		case field.DataType == "NUMBER" && value.Number != nil:
			item.Fields[field.Name] = *value.Number
		case field.DataType == "DATE" && value.Date != nil:
			item.Fields[field.Name] = *value.Date
		case field.DataType == "ITERATION" && value.Title != nil:
			item.Fields[field.Name] = *value.Title
		}
	}
	content := raw.Content
	if content == nil {
		return item
	}
	item.Title, item.Number, item.URL = content.Title, content.Number, content.URL
	item.State = strings.ToLower(content.IssueState + content.PullRequestState)
	if content.Repository != nil {
		item.Repository = content.Repository.NameWithOwner
	}
	for _, node := range content.Assignees.Nodes {
		item.Assignees = append(item.Assignees, node.Login)
	}
	for _, node := range content.Labels.Nodes {
		item.Labels = append(item.Labels, node.Name)
	}
	item.assigneesComplete = content.Assignees.TotalCount <= len(content.Assignees.Nodes)
	item.labelsComplete = content.Labels.TotalCount <= len(content.Labels.Nodes)
	return item
}

type itemsPageJSON struct {
	Project *struct {
		Items *struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Edges []struct {
				Cursor string   `json:"cursor"`
				Node   itemJSON `json:"node"`
			} `json:"edges"`
		} `json:"items"`
	} `json:"project"`
}

// ListItems reads one bounded batch of project items. See listItems.
func (c *Client) ListItems(ctx context.Context, options ItemListOptions) (*ItemList, error) {
	if c.target.kind != kindProject {
		return nil, providerError("list project items", "this connection is not bound to a project")
	}
	after, err := options.normalize(c.target)
	if err != nil {
		return nil, err
	}
	return c.listItems(ctx, options, after)
}

// listItems resolves the field model once, then reads server-filtered item batches after the continuation
// point until the requested number of verified items plus one look-ahead item is found, the project holds
// no further items, or the scan bound is reached. The next cursor always points at the last item this
// batch consumed, so every matching item is reached exactly once and in project order.
func (c *Client) listItems(ctx context.Context, options ItemListOptions, after string) (*ItemList, error) {
	const op = "list project items"
	binding := options.binding(c.target)
	info, err := c.project(ctx, op)
	if err != nil {
		return nil, err
	}
	if err := options.resolveStatus(info); err != nil {
		return nil, err
	}
	query := options.query()

	result := &ItemList{Items: []Item{}}
	last := after
	for scan := 0; scan < maxScanRequests; scan++ {
		first := options.Limit - len(result.Items) + 1
		if first > maxLimit {
			first = maxLimit
		}
		variables := map[string]any{"project": info.id, "first": first, "query": query, "after": nil}
		if after != "" {
			variables["after"] = after
		}
		var page itemsPageJSON
		if err := c.graphql(ctx, op, itemsQuery, variables, &page); err != nil {
			return nil, err
		}
		if page.Project == nil || page.Project.Items == nil {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub returned a project without items"}
		}
		for _, edge := range page.Project.Items.Edges {
			if edge.Node.ID == "" || edge.Cursor == "" {
				return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
					Message: "GitHub returned an item without a usable identifier"}
			}
			item := info.normalize(edge.Node)
			if options.matches(item) {
				if len(result.Items) == options.Limit {
					// A further matching item exists: the batch is full and continues before it.
					result.HasMore, result.NextCursor = true, encodeCursor(binding, last)
					return result, nil
				}
				result.Items = append(result.Items, item.Item)
			}
			last = edge.Cursor
		}
		pageInfo := page.Project.Items.PageInfo
		if !pageInfo.HasNextPage {
			return result, nil
		}
		if pageInfo.EndCursor == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub announced further items without a cursor"}
		}
		after, last = pageInfo.EndCursor, pageInfo.EndCursor
	}
	// The scan bound was reached while the project still holds unread items. The batch may be short or even
	// empty, but it is never presented as the end.
	result.HasMore, result.NextCursor = true, encodeCursor(binding, last)
	return result, nil
}

type itemDetailJSON struct {
	ownerJSON
	Item *itemJSON `json:"item"`
}

// GetItem reads exactly one item of the bound project with its fields and, for issue content, the full
// body. It resolves the project and the item in one query and refuses an item of any other project.
func (c *Client) GetItem(ctx context.Context, id string) (*Item, error) {
	const op = "get project item"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if id == "" || len(id) > 200 {
		return nil, invalidRequest("item_id is not a project item identifier")
	}
	variables := c.projectVariables()
	variables["item"] = id
	var detail itemDetailJSON
	if err := c.graphql(ctx, op, c.itemQuery(), variables, &detail); err != nil {
		return nil, err
	}
	info, err := projectInfoOf(op, c.target, detail.ownerJSON)
	if err != nil {
		return nil, err
	}
	if detail.Item == nil || detail.Item.ID != id || detail.Item.Project == nil || detail.Item.Project.ID != info.id {
		return nil, notFound(op, subject{in: c.target, what: "this item"})
	}
	item := info.normalize(*detail.Item).Item
	if content := detail.Item.Content; content != nil && content.Body != nil &&
		(item.Type == "issue" || item.Type == "draft_issue") {
		body := *content.Body
		item.Body = &body
	}
	return &item, nil
}
