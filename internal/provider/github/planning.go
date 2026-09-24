package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Bounds of one project change. At most maxFieldChanges values are written per request, fieldBatchSize of
// them per GraphQL mutation document.
const (
	maxFieldChanges    = 20
	fieldBatchSize     = 10
	maxFieldNameLength = 100
	maxTextLength      = 1024
)

// The outcomes of one field change.
const (
	resultUpdated = "updated"
	resultFailed  = "failed"
	resultUnknown = "unknown"
	resultNotSent = "not_sent"
)

// FieldValues names project fields and the value each should take: an option name of a single-select
// field, a list of option names of a multi-select field, an iteration title, a date as YYYY-MM-DD, a text, a
// number, or null to clear the field; an empty list clears a multi-select field as well. Names and option
// names are compared without case.
type FieldValues map[string]json.RawMessage

// fieldInput is one checked field value: nil clears the field, otherwise a string, a float64, or a list of
// option names.
type fieldInput struct {
	name  string
	value any
}

// inputs checks the field values without the project. The resolved changes are written in the order of
// the project's field names, so a request always writes its fields in the same order.
func (values FieldValues) inputs() ([]fieldInput, error) {
	if len(values) > maxFieldChanges {
		return nil, invalidRequest(fmt.Sprintf("fields accepts at most %d values", maxFieldChanges))
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	inputs := make([]fieldInput, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		if !safeFieldName(name) {
			return nil, invalidRequest("fields contains a name no project field can carry")
		}
		if seen[strings.ToLower(name)] {
			return nil, invalidRequest("fields names one field more than once")
		}
		seen[strings.ToLower(name)] = true
		value, err := fieldValue(values[name])
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, fieldInput{name: name, value: value})
	}
	return inputs, nil
}

func safeFieldName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > maxFieldNameLength || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func fieldValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, invalidRequest("a field value must be a string, a number, a list of option names, or null")
	}
	switch value := value.(type) {
	case nil:
		return nil, nil
	case []any:
		if len(value) > maxOptions {
			return nil, invalidRequest(fmt.Sprintf("a list value names at most %d options", maxOptions))
		}
		names := make([]string, len(value))
		for i, entry := range value {
			name, ok := entry.(string)
			if !ok || name == "" || utf8.RuneCountInString(name) > maxTextLength {
				return nil, invalidRequest("a list value must hold option names")
			}
			names[i] = name
		}
		return names, nil
	case string:
		if utf8.RuneCountInString(value) > maxTextLength {
			return nil, invalidRequest(fmt.Sprintf("a field value must hold at most %d characters", maxTextLength))
		}
		return value, nil
	case json.Number:
		number, err := value.Float64()
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, invalidRequest("a field value is not a usable number")
		}
		return number, nil
	}
	return nil, invalidRequest("a field value must be a string, a number, a list of option names, or null")
}

// fieldChange is one resolved change: the field and the GraphQL value it takes, or nil to clear it.
type fieldChange struct {
	name  string
	id    string
	value map[string]any
}

// settable are the field types whose values the planning tools write.
var settable = map[string]bool{"SINGLE_SELECT": true, "MULTI_SELECT": true, "TEXT": true, "NUMBER": true,
	"DATE": true, "ITERATION": true}

// resolve binds checked field values to the field model of the project. Every value is resolved before
// the first change is sent, so an unknown field or option never leaves a change half done.
func (info *projectInfo) resolve(inputs []fieldInput) ([]fieldChange, error) {
	changes := make([]fieldChange, 0, len(inputs))
	for _, input := range inputs {
		field, ok := info.field(input.name)
		if !ok {
			return nil, invalidRequest("a named field is not a field of this project; fields that can be set are " +
				strings.Join(info.settableNames(), ", "))
		}
		if !settable[field.DataType] {
			return nil, invalidRequest("field " + field.Name + " cannot be set by this tool")
		}
		change := fieldChange{name: field.Name, id: field.ID}
		if input.value != nil {
			// An empty list of a multi-select field resolves to no value and clears the field.
			value, err := fieldValueOf(field, input.value)
			if err != nil {
				return nil, err
			}
			change.value = value
		}
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].name < changes[j].name })
	return changes, nil
}

func fieldValueOf(field fieldJSON, value any) (map[string]any, error) {
	text, isText := value.(string)
	switch field.DataType {
	case "NUMBER":
		if number, ok := value.(float64); ok {
			return map[string]any{"number": number}, nil
		}
		return nil, invalidRequest("field " + field.Name + " takes a number")
	case "TEXT":
		if isText {
			return map[string]any{"text": text}, nil
		}
		return nil, invalidRequest("field " + field.Name + " takes a text")
	case "DATE":
		if _, err := time.Parse("2006-01-02", text); isText && err == nil {
			return map[string]any{"date": text}, nil
		}
		return nil, invalidRequest("field " + field.Name + " takes a date as YYYY-MM-DD")
	case "SINGLE_SELECT":
		names := make([]string, len(field.Options))
		for i, option := range field.Options {
			if isText && strings.EqualFold(option.Name, text) && option.ID != "" {
				return map[string]any{"singleSelectOptionId": option.ID}, nil
			}
			names[i] = option.Name
		}
		return nil, invalidRequest("field " + field.Name + " takes one of its options: " + strings.Join(names, ", "))
	case "MULTI_SELECT":
		return multiSelectValue(field, value)
	}
	// An iteration is named by its title, whether it is current, planned, or completed.
	names := []string{}
	if field.Configuration != nil {
		all := append(append([]iterationJSON(nil), field.Configuration.Iterations...),
			field.Configuration.CompletedIterations...)
		for _, iteration := range all {
			if isText && strings.EqualFold(iteration.Title, text) && iteration.ID != "" {
				return map[string]any{"iterationId": iteration.ID}, nil
			}
			names = append(names, iteration.Title)
		}
	}
	return nil, invalidRequest("field " + field.Name + " takes one of its iterations: " + strings.Join(names, ", "))
}

// multiSelectValue resolves a list of option names; an empty list resolves to no value, which clears the
// field. A name listed twice selects its option once.
func multiSelectValue(field fieldJSON, value any) (map[string]any, error) {
	names := make([]string, len(field.Options))
	for i, option := range field.Options {
		names[i] = option.Name
	}
	list, ok := value.([]string)
	if !ok {
		return nil, invalidRequest("field " + field.Name + " takes a list of its options: " + strings.Join(names, ", "))
	}
	if len(list) == 0 {
		return nil, nil
	}
	ids := []string{}
	for _, name := range list {
		i := indexFold(names, name)
		if i < 0 || field.Options[i].ID == "" {
			return nil, invalidRequest("field " + field.Name + " takes a list of its options: " +
				strings.Join(names, ", "))
		}
		if !containsFold(ids, field.Options[i].ID) {
			ids = append(ids, field.Options[i].ID)
		}
	}
	return map[string]any{"multiSelectOptionIds": ids}, nil
}

// field finds a field of the project by name, compared without case.
func (info *projectInfo) field(name string) (fieldJSON, bool) {
	for _, field := range info.fields {
		if strings.EqualFold(field.Name, name) {
			return field, true
		}
	}
	return fieldJSON{}, false
}

func (info *projectInfo) settableNames() []string {
	names := []string{}
	for _, field := range info.fields {
		if settable[field.DataType] {
			names = append(names, field.Name)
		}
	}
	sort.Strings(names)
	return names
}

// fieldNodeSelection is the whole schema of one field: its name and type, the identifier, color, and
// description of every option, and the settings and every iteration of an iteration field.
const fieldNodeSelection = `... on ProjectV2FieldCommon{id name dataType} ` +
	`... on ProjectV2SingleSelectField{options{id name color description}} ` +
	`... on ProjectV2MultiSelectField{multiSelectOptions{id name color description}} ` +
	`... on ProjectV2IterationField{configuration{duration startDay ` +
	`iterations{id title startDate duration} completedIterations{id title startDate duration}}}`

// planningFieldSelection is the field model a change needs: besides the names, the identifiers of every
// option and iteration. GitHub holds at most 50 fields per project, so one page reads them all.
const planningFieldSelection = `fields(first:100){nodes{` + fieldNodeSelection + `}}`

// planningRequest names what one change resolves before it writes: the field model, the views, the
// workflows, an item that has to belong to the project and, when draft is set, has to be a draft issue, a
// second item after that has to belong to it as well, a status update that has to belong to it, a repository
// the connection allows with one of its issues when number is set, the users that assignees names, and the
// teams of the project's organization that teams names.
type planningRequest struct {
	fields       bool
	views        bool
	workflows    bool
	item         string
	draft        bool
	after        string
	statusUpdate string
	repository   target
	number       int
	assignees    []string
	teams        []string
}

// planningNodes are the nodes a change resolved beside the project: the issue, the repository, the draft
// issue of the item, the users in the order of the request's assignees, and the teams in the order of its
// teams.
type planningNodes struct {
	issue, repository, draft string
	assignees, teams         []string
}

type planningItemJSON struct {
	ID      string `json:"id"`
	Project *struct {
		ID string `json:"id"`
	} `json:"project"`
	Content *struct {
		ID string `json:"id"`
	} `json:"content"`
}

type planningJSON struct {
	ownerJSON
	Item         *planningItemJSON `json:"item"`
	After        *planningItemJSON `json:"after"`
	StatusUpdate *struct {
		ID      string `json:"id"`
		Project *struct {
			ID string `json:"id"`
		} `json:"project"`
	} `json:"statusUpdate"`
	Repository *struct {
		ID    string `json:"id"`
		Issue *struct {
			ID string `json:"id"`
		} `json:"issue"`
	} `json:"repository"`
}

// belongs reports whether an answered item is the requested one and belongs to the project.
func (item *planningItemJSON) belongs(id string, info *projectInfo) bool {
	return item != nil && item.ID == id && item.Project != nil && item.Project.ID == info.id
}

// resolve reads everything one change needs in exactly one query: the project and, as requested, its field
// model, its views, its workflows, the items and the status update it must hold, a repository or one of its
// issues, the users to assign, and the teams of its organization.
func (c *Client) resolve(ctx context.Context, op string, request planningRequest) (*projectInfo, planningNodes, error) {
	var nodes planningNodes
	declarations := "$owner:String!,$number:Int!"
	selection := "id"
	if request.fields {
		selection += " " + planningFieldSelection
	}
	if request.views {
		selection += " " + viewsSelection
	}
	if request.workflows {
		selection += " " + workflowsSelection
	}
	variables := c.projectVariables()
	// A team is resolved inside the organization that owns the project, so it can be no team of another one.
	teams := ""
	for i, slug := range request.teams {
		alias := "team" + strconv.Itoa(i)
		declarations += ",$" + alias + ":String!"
		teams += " " + alias + ":team(slug:$" + alias + "){id}"
		variables[alias] = slug
	}
	body := `owner:` + c.target.ownerField() + `(login:$owner){projectV2(number:$number){` + selection + `}` +
		teams + `}`
	if request.item != "" {
		content := ""
		if request.draft {
			content = " content{... on DraftIssue{id}}"
		}
		declarations += ",$item:ID!"
		body += ` item:node(id:$item){... on ProjectV2Item{id project{id}` + content + `}}`
		variables["item"] = request.item
	}
	if request.after != "" {
		declarations += ",$after:ID!"
		body += ` after:node(id:$after){... on ProjectV2Item{id project{id}}}`
		variables["after"] = request.after
	}
	if request.statusUpdate != "" {
		declarations += ",$statusUpdate:ID!"
		body += ` statusUpdate:node(id:$statusUpdate){... on ProjectV2StatusUpdate{id project{id}}}`
		variables["statusUpdate"] = request.statusUpdate
	}
	if request.repository.kind == kindRepository {
		inner := "id"
		declarations += ",$repoOwner:String!,$repoName:String!"
		if request.number != 0 {
			inner = "issue(number:$issue){id}"
			declarations += ",$issue:Int!"
			variables["issue"] = request.number
		}
		body += ` repository(owner:$repoOwner,name:$repoName){` + inner + `}`
		variables["repoOwner"], variables["repoName"] = request.repository.owner, request.repository.repo
	}
	for i, login := range request.assignees {
		alias := "assignee" + strconv.Itoa(i)
		declarations += ",$" + alias + ":String!"
		body += " " + alias + ":user(login:$" + alias + "){id}"
		variables[alias] = login
	}
	var data json.RawMessage
	if err := c.graphql(ctx, op, `query(`+declarations+`){`+body+`}`, variables, &data); err != nil {
		return nil, nodes, err
	}
	var answer planningJSON
	var aliases map[string]json.RawMessage
	if json.Unmarshal(data, &answer) != nil || json.Unmarshal(data, &aliases) != nil {
		return nil, nodes, invalidResponse(op, false)
	}
	info, err := projectInfoOf(op, c.target, answer.ownerJSON)
	if err != nil {
		return nil, nodes, err
	}
	if request.item != "" && !answer.Item.belongs(request.item, info) {
		return nil, nodes, notFound(op, subject{in: c.target, what: "this item"})
	}
	if request.draft {
		if answer.Item.Content == nil || answer.Item.Content.ID == "" {
			return nil, nodes, invalidRequest("item_id names an issue or pull request of this project; only a " +
				"draft issue can be changed or converted this way")
		}
		nodes.draft = answer.Item.Content.ID
	}
	if request.after != "" && !answer.After.belongs(request.after, info) {
		return nil, nodes, notFound(op, subject{in: c.target, what: "the item after_id names"})
	}
	for i, login := range request.assignees {
		var user struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(aliases["assignee"+strconv.Itoa(i)], &user) != nil || user.ID == "" {
			return nil, nodes, notFound(op, subject{what: "user " + login})
		}
		nodes.assignees = append(nodes.assignees, user.ID)
	}
	if request.statusUpdate != "" && (answer.StatusUpdate == nil || answer.StatusUpdate.ID != request.statusUpdate ||
		answer.StatusUpdate.Project == nil || answer.StatusUpdate.Project.ID != info.id) {
		return nil, nodes, notFound(op, subject{in: c.target, what: "this status update"})
	}
	var owner map[string]json.RawMessage
	_ = json.Unmarshal(aliases["owner"], &owner)
	for i, slug := range request.teams {
		var team struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(owner["team"+strconv.Itoa(i)], &team) != nil || team.ID == "" {
			return nil, nodes, notFound(op, subject{what: "team " + slug + " of " + c.target.scope + "/" +
				c.target.owner})
		}
		nodes.teams = append(nodes.teams, team.ID)
	}
	if request.repository.kind != kindRepository {
		return info, nodes, nil
	}
	if answer.Repository == nil || (request.number == 0 && answer.Repository.ID == "") {
		return nil, nodes, notFound(op, subject{in: request.repository})
	}
	nodes.repository = answer.Repository.ID
	if request.number == 0 {
		return info, nodes, nil
	}
	if answer.Repository.Issue == nil || answer.Repository.Issue.ID == "" {
		return nil, nodes, notFound(op, subject{in: request.repository, what: "issue #" + strconv.Itoa(request.number)})
	}
	nodes.issue = answer.Repository.Issue.ID
	return info, nodes, nil
}

// FieldResult is the outcome of one field change: updated, failed, unknown when the change may have been
// applied without a confirmation, or not_sent when an earlier failure stopped the request.
type FieldResult struct {
	Field   string `json:"field"`
	Result  string `json:"result"`
	Message string `json:"message,omitempty"`
}

// IssueRef names the issue a planning change created.
type IssueRef struct {
	Number     int    `json:"number"`
	Repository string `json:"repository"`
	URL        string `json:"url,omitempty"`
}

// Planning is the answer of a project change: the item it concerns, the issue it created, and the outcome
// of every field change in name order. Complete is true only when every step succeeded; otherwise Error
// says what stopped it. Whatever already happened stays reported, so nothing that exists is created twice.
type Planning struct {
	ItemID   string        `json:"item_id,omitempty"`
	Issue    *IssueRef     `json:"issue,omitempty"`
	Fields   []FieldResult `json:"fields"`
	Complete bool          `json:"complete"`
	Error    string        `json:"error,omitempty"`
}

// fieldBatch builds one mutation document of aliased field changes. Only fixed text and indexes are
// concatenated; every identifier and value travels as a variable.
func fieldBatch(projectID, itemID string, changes []fieldChange) (string, map[string]any) {
	declarations := []string{"$project:ID!", "$item:ID!"}
	selections := make([]string, 0, len(changes))
	variables := map[string]any{"project": projectID, "item": itemID}
	for i, change := range changes {
		n := strconv.Itoa(i)
		declarations = append(declarations, "$f"+n+":ID!")
		variables["f"+n] = change.id
		if change.value == nil {
			selections = append(selections, "f"+n+":clearProjectV2ItemFieldValue(input:{projectId:$project,"+
				"itemId:$item,fieldId:$f"+n+"}){projectV2Item{id}}")
			continue
		}
		declarations = append(declarations, "$v"+n+":ProjectV2FieldValue!")
		variables["v"+n] = change.value
		selections = append(selections, "f"+n+":updateProjectV2ItemFieldValue(input:{projectId:$project,"+
			"itemId:$item,fieldId:$f"+n+",value:$v"+n+"}){projectV2Item{id}}")
	}
	return "mutation(" + strings.Join(declarations, ",") + "){" + strings.Join(selections, " ") + "}", variables
}

// applyFields writes the resolved changes of one item in serial batches and reports each field. GitHub runs
// the aliased mutations of one document independently, so every alias is judged on its own answer. A
// failure stops the request after its batch: the remaining fields are not sent. The first failure is
// returned alongside the outcomes.
func (c *Client) applyFields(ctx context.Context, op, projectID, itemID string, changes []fieldChange) ([]FieldResult, error) {
	results := notSent(changes)
	for start := 0; start < len(changes); start += fieldBatchSize {
		batch := changes[start:min(start+fieldBatchSize, len(changes))]
		document, variables := fieldBatch(projectID, itemID, batch)
		envelope, err := c.post(ctx, op, document, variables, true)
		if err != nil {
			outcome := resultFailed
			if strings.HasSuffix(messageOf(err), uncertain) {
				outcome = resultUnknown
			}
			for i := range batch {
				results[start+i].Result, results[start+i].Message = outcome, messageOf(err)
			}
			return results, err
		}
		var data map[string]json.RawMessage
		_ = json.Unmarshal(envelope.Data, &data)
		var first error
		for i := range batch {
			alias := "f" + strconv.Itoa(i)
			var answer struct {
				Item *struct {
					ID string `json:"id"`
				} `json:"projectV2Item"`
			}
			if json.Unmarshal(data[alias], &answer) == nil && answer.Item != nil && answer.Item.ID == itemID {
				results[start+i].Result = resultUpdated
				continue
			}
			var failure *provider.Error
			if errs := errorsAt(envelope.Errors, alias); len(errs) > 0 {
				failure = c.graphQLFailure(op, errs, variables, true)
				results[start+i].Result = resultFailed
			} else {
				failure = invalidResponse(op, true)
				results[start+i].Result = resultUnknown
			}
			results[start+i].Message = failure.Message
			if first == nil {
				first = failure
			}
		}
		if first != nil {
			return results, first
		}
	}
	return results, nil
}

// errorsAt returns the errors of one alias, or, when GitHub named none, the errors that name no alias.
func errorsAt(errs []graphQLError, alias string) []graphQLError {
	var own, general []graphQLError
	for _, e := range errs {
		switch {
		case len(e.Path) == 0:
			general = append(general, e)
		case e.Path[0] == alias:
			own = append(own, e)
		}
	}
	if len(own) > 0 {
		return own
	}
	return general
}

func messageOf(err error) string {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Message
	}
	return err.Error()
}

// finish writes the field changes of an item that was just created or added and completes its answer. It
// never fails: the item exists, and the answer has to say so.
func (c *Client) finish(ctx context.Context, op string, result *Planning, projectID string,
	changes []fieldChange) *Planning {
	fields, err := c.applyFields(ctx, op, projectID, result.ItemID, changes)
	result.Fields, result.Complete = fields, err == nil
	if err != nil {
		result.Error = messageOf(err)
	}
	return result
}

// mayHaveChanged reports whether a field change was written or may have been.
func mayHaveChanged(fields []FieldResult) bool {
	for _, field := range fields {
		if field.Result == resultUpdated || field.Result == resultUnknown {
			return true
		}
	}
	return false
}

func notSent(changes []fieldChange) []FieldResult {
	results := make([]FieldResult, len(changes))
	for i, change := range changes {
		results[i] = FieldResult{Field: change.name, Result: resultNotSent}
	}
	return results
}

func checkItemID(id string) error {
	if id == "" || len(id) > 200 {
		return invalidRequest("item_id is not a project item identifier")
	}
	return nil
}

// UpdateItemFields writes field values of one item of the bound project. The project, its fields, their
// options, and the item's membership are resolved in one query; the values are then written in batches.
// A request that certainly changed nothing fails; once a value was or may have been written, the answer
// names every outcome.
func (c *Client) UpdateItemFields(ctx context.Context, itemID string, values FieldValues) (*Planning, error) {
	const op = "update project item"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := checkItemID(itemID); err != nil {
		return nil, err
	}
	inputs, err := values.inputs()
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, invalidRequest("fields must name at least one field")
	}
	info, _, err := c.resolve(ctx, op, planningRequest{fields: true, item: itemID})
	if err != nil {
		return nil, err
	}
	changes, err := info.resolve(inputs)
	if err != nil {
		return nil, err
	}
	fields, err := c.applyFields(ctx, op, info.id, itemID, changes)
	if err != nil && !mayHaveChanged(fields) {
		return nil, err
	}
	result := &Planning{ItemID: itemID, Fields: fields, Complete: err == nil}
	if err != nil {
		result.Error = messageOf(err)
	}
	return result, nil
}

const addItemMutation = `mutation($project:ID!,$content:ID!){add:addProjectV2ItemById(` +
	`input:{projectId:$project,contentId:$content}){item{id}}}`

type addedJSON struct {
	Add *struct {
		Item *struct {
			ID string `json:"id"`
		} `json:"item"`
	} `json:"add"`
}

// addItem adds one issue node to the project and returns the item. GitHub answers with the existing item
// when the issue is already in the project. Adding and changing an item are separate requests, because
// GitHub does not allow both in one.
func (c *Client) addItem(ctx context.Context, op, projectID, contentID string) (string, error) {
	var added addedJSON
	if err := c.mutate(ctx, op, addItemMutation, map[string]any{"project": projectID, "content": contentID},
		&added); err != nil {
		return "", err
	}
	if added.Add == nil || added.Add.Item == nil || added.Add.Item.ID == "" {
		return "", invalidResponse(op, true)
	}
	return added.Add.Item.ID, nil
}

// AddIssue adds one issue of a repository the connection allows to the bound project and then writes the
// given field values. Everything is resolved and checked before the first change.
func (c *Client) AddIssue(ctx context.Context, repository string, number int, values FieldValues) (*Planning, error) {
	repo, err := c.allowed.choose(kindRepository, repository)
	if err != nil {
		return nil, err
	}
	return c.addIssue(ctx, repo, number, values)
}

// addIssue is AddIssue for a repository selectTarget has already checked.
func (c *Client) addIssue(ctx context.Context, repo target, number int, values FieldValues) (*Planning, error) {
	const op = "add project item"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := checkNumber(number); err != nil {
		return nil, err
	}
	inputs, err := values.inputs()
	if err != nil {
		return nil, err
	}
	info, nodes, err := c.resolve(ctx, op, planningRequest{fields: len(inputs) > 0, repository: repo,
		number: number})
	if err != nil {
		return nil, err
	}
	changes, err := info.resolve(inputs)
	if err != nil {
		return nil, err
	}
	itemID, err := c.addItem(ctx, op, info.id, nodes.issue)
	if err != nil {
		return nil, err
	}
	return c.finish(ctx, op, &Planning{ItemID: itemID}, info.id, changes), nil
}

const draftMutation = `mutation($project:ID!,$title:String!,$body:String){add:addProjectV2DraftIssue(` +
	`input:{projectId:$project,title:$title,body:$body}){projectItem{id}}}`

// CreateDraft adds one draft issue to the bound project and then writes the given field values.
func (c *Client) CreateDraft(ctx context.Context, title string, body *string, values FieldValues) (*Planning, error) {
	const op = "create draft issue"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := (IssueContent{Title: &title, Body: body}).check(true); err != nil {
		return nil, err
	}
	inputs, err := values.inputs()
	if err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{fields: len(inputs) > 0})
	if err != nil {
		return nil, err
	}
	changes, err := info.resolve(inputs)
	if err != nil {
		return nil, err
	}
	variables := map[string]any{"project": info.id, "title": title, "body": nil}
	if body != nil {
		variables["body"] = *body
	}
	var added struct {
		Add *struct {
			Item *struct {
				ID string `json:"id"`
			} `json:"projectItem"`
		} `json:"add"`
	}
	if err := c.mutate(ctx, op, draftMutation, variables, &added); err != nil {
		return nil, err
	}
	if added.Add == nil || added.Add.Item == nil || added.Add.Item.ID == "" {
		return nil, invalidResponse(op, true)
	}
	return c.finish(ctx, op, &Planning{ItemID: added.Add.Item.ID}, info.id, changes), nil
}

const archiveMutation = `mutation($project:ID!,$item:ID!){archive:archiveProjectV2Item(` +
	`input:{projectId:$project,itemId:$item}){item{id}}}`

// Archived is the answer of an archived item.
type Archived struct {
	ItemID   string `json:"item_id"`
	Archived bool   `json:"archived"`
}

// ArchiveItem archives one item of the bound project. The item stays restorable in GitHub.
func (c *Client) ArchiveItem(ctx context.Context, itemID string) (*Archived, error) {
	const op = "archive project item"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := checkItemID(itemID); err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{item: itemID})
	if err != nil {
		return nil, err
	}
	var archived struct {
		Archive *struct {
			Item *struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"archive"`
	}
	if err := c.mutate(ctx, op, archiveMutation, map[string]any{"project": info.id, "item": itemID},
		&archived); err != nil {
		return nil, err
	}
	if archived.Archive == nil || archived.Archive.Item == nil || archived.Archive.Item.ID != itemID {
		return nil, invalidResponse(op, true)
	}
	return &Archived{ItemID: itemID, Archived: true}, nil
}

// CreatePlannedIssue opens one issue in a repository the connection allows, adds it to the bound project,
// and writes the given field values, each step in a request of its own. The project and every field value
// are resolved before the issue is created. Once the issue exists, the answer reports it, whatever fails
// afterwards.
func (c *Client) CreatePlannedIssue(ctx context.Context, repository string, content IssueContent,
	values FieldValues) (*Planning, error) {
	repo, err := c.allowed.choose(kindRepository, repository)
	if err != nil {
		return nil, err
	}
	return c.createPlannedIssue(ctx, repo, content, values)
}

// createPlannedIssue is CreatePlannedIssue for a repository selectTarget has already checked.
func (c *Client) createPlannedIssue(ctx context.Context, repo target, content IssueContent,
	values FieldValues) (*Planning, error) {
	const op = "create planned issue"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := content.check(true); err != nil {
		return nil, err
	}
	inputs, err := values.inputs()
	if err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{fields: len(inputs) > 0})
	if err != nil {
		return nil, err
	}
	changes, err := info.resolve(inputs)
	if err != nil {
		return nil, err
	}
	issue, nodeID, err := c.createIssue(ctx, repo, content)
	if err != nil {
		return nil, err
	}
	result := &Planning{Issue: &IssueRef{Number: issue.Number, Repository: repo.owner + "/" + repo.repo,
		URL: issue.URL}}
	itemID, err := c.addItem(ctx, op, info.id, nodeID)
	if err != nil {
		result.Fields = notSent(changes)
		result.Error = "the issue was created but not added to the project: " + messageOf(err)
		return result, nil
	}
	result.ItemID = itemID
	return c.finish(ctx, op, result, info.id, changes), nil
}
