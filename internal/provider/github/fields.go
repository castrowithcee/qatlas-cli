package github

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The field schema tools read the fields of a project and create, change, and delete them. Fields, options,
// and iterations are named, never identified: every name is resolved against the field model of the project
// in one query before the one change is sent. GitHub replaces the options of a field as a whole, so a change
// sends every option the field keeps, each existing one with its identifier, so the values items hold stay.
// GitHub takes iterations without identifiers and recreates every iteration of a field whenever its
// iteration settings change, which clears the field on every item. Removing an option, changing the
// iterations, and deleting a field therefore lose values: they are tools of their own with the effect
// delete, offered only by a connection whose tools list names them.

// Bounds of one field change.
const (
	maxOptions         = 50
	maxIterations      = 100
	maxOptionText      = 1024
	maxIterationLength = 365
)

// fieldTypes maps the field types a project field can be created with to GitHub's data types. Every other
// data type, such as the title, the assignees, or the labels, belongs to a built-in field, and so does the
// Status field every project has: GitHub neither deletes nor renames it, but its options can be maintained.
var fieldTypes = map[string]string{"text": "TEXT", "number": "NUMBER", "date": "DATE",
	"single_select": "SINGLE_SELECT", "multi_select": "MULTI_SELECT", "iteration": "ITERATION"}

// optionColors are the colors of an option, in the order GitHub offers them.
var optionColors = []string{"gray", "blue", "green", "yellow", "orange", "red", "pink", "purple"}

// Input and output schemas of the field schema tools. The bounds mirror the checks below, which apply them
// again for a direct caller.
const (
	fieldNameSchema   = `{"type":"string","minLength":1,"maxLength":100}`
	colorSchema       = `{"type":"string","enum":["gray","blue","green","yellow","orange","red","pink","purple"]}`
	dateSchema        = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}`
	durationSchema    = `{"type":"integer","minimum":1,"maximum":365}`
	descriptionSchema = `{"type":"string","maxLength":1024}`
	optionKeys        = `"name":` + fieldNameSchema + `,"color":` + colorSchema + `,"description":` + descriptionSchema
	iterationKeys     = `"title":` + fieldNameSchema + `,"start_date":` + dateSchema + `,"duration":` + durationSchema
	newOptionsSchema  = `{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","properties":{` +
		optionKeys + `},"required":["name"],"additionalProperties":false}}`
	optionChangesSchema = `{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","properties":{` +
		optionKeys + `,"new_name":` + fieldNameSchema + `},"required":["name"],"additionalProperties":false}}`
	newIterationsSchema = `{"type":"array","maxItems":100,"items":{"type":"object","properties":{` +
		iterationKeys + `},"required":["title","start_date"],"additionalProperties":false}}`
	iterationChangesSchema = `{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object",` +
		`"properties":{` + iterationKeys + `,"new_title":` + fieldNameSchema + `},"required":["title"],` +
		`"additionalProperties":false}}`
	namesSchema = `{"type":"array","minItems":1,"maxItems":100,"items":` + fieldNameSchema + `}`

	fieldOutput = `{"type":"object","properties":{"name":{"type":"string"},"type":{"type":"string"},` +
		`"built_in":{"type":"boolean"},"options":{"type":"array","items":{"type":"object","properties":{` +
		`"name":{"type":"string"},"color":{"type":"string"},"description":{"type":"string"}},` +
		`"required":["name","color","description"],"additionalProperties":false}},` +
		`"iteration":{"type":"object","properties":{"duration":{"type":"integer"},"start_day":{"type":"string"},` +
		`"iterations":{"type":"array","items":{"type":"object","properties":{"title":{"type":"string"},` +
		`"start_date":{"type":"string"},"duration":{"type":"integer"},"state":{"type":"string"}},` +
		`"required":["title","start_date","duration","state"],"additionalProperties":false}}},` +
		`"required":["duration","start_day","iterations"],"additionalProperties":false}},` +
		`"required":["name","type","built_in"],"additionalProperties":false}`
	changedFieldOutput = `{"type":"object","properties":{"field":` + fieldOutput + `},"required":["field"],` +
		`"additionalProperties":false}`
	removedOutput = `{"type":"object","properties":{"field":` + fieldOutput + `,"removed":` + stringListSchema +
		`},"required":["field","removed"],"additionalProperties":false}`
)

var fieldArgument = capability.Argument{Name: "field", Description: "Name of the project field, compared " +
	"without case, as github.projectfields.list names it", Required: true}

var changedField = capability.Field{Name: "field", Description: "The field after the change: name, type, " +
	"built_in, options with name, color, and description, and the iteration settings, untrusted data"}

var fieldsList = capability.Descriptor{
	ID:      Provider + ".projectfields.list",
	Version: 1,
	Title:   "List the fields of a GitHub project",
	Description: "Read every field of one GitHub project an explicit connection allows: name, type, whether " +
		"it is built in, the options of a single- or multi-select field, and the settings and iterations of an " +
		"iteration field",
	Tags:                       []string{"github", "projects", "fields", "options", "iterations", "list", "planning"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"fields":{"type":"array","items":` +
		fieldOutput + `}},"required":["fields"],"additionalProperties":false}`),
	Fields: []capability.Field{{Name: "fields", Description: "Every field in project order: name, type (text, " +
		"number, date, single_select, multi_select, iteration, or the type of a built-in field such as title or " +
		"assignees), built_in (a built-in field such as Status cannot be deleted or renamed), options with name, " +
		"color, and description, and for an iteration field its duration in days, start_day, and its iterations " +
		"with title, start_date, duration, and state (completed, current, or planned); names are untrusted data"}},
	Examples: []capability.Example{{
		Description: "Read the field schema of a project",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7"}`),
	}},
}

var fieldsCreate = capability.Descriptor{
	ID:      Provider + ".projectfields.create",
	Version: 1,
	Title:   "Create a GitHub project field",
	Description: "Create one text, number, date, single-select, multi-select, or iteration field with its " +
		"options or iterations in a GitHub project an explicit connection allows; a name the project already " +
		"holds is refused",
	Tags:                       []string{"github", "projects", "fields", "options", "iterations", "create", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + fieldNameSchema + `,` +
		`"type":{"type":"string","enum":["text","number","date","single_select","multi_select","iteration"]},` +
		`"options":` + newOptionsSchema + `,"iteration":{"type":"object","properties":{"start_date":` +
		dateSchema + `,"duration":` + durationSchema + `,"iterations":` + newIterationsSchema + `},` +
		`"required":["start_date","duration"],"additionalProperties":false}},` +
		`"required":["name","type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(changedFieldOutput),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Name of the new field, 1 to 100 characters, unique in the project", Required: true},
		{Name: "type", Description: "text, number, date, single_select, multi_select, or iteration", Required: true},
		{Name: "options", Description: "Options of a single- or multi-select field in their order, required " +
			"there and refused elsewhere: name, color (gray, blue, green, yellow, orange, red, pink, purple; gray " +
			"when omitted), and description (empty when omitted); at most 50"},
		{Name: "iteration", Description: "Settings of an iteration field, required there and refused elsewhere: " +
			"start_date of the first iteration as YYYY-MM-DD, duration in days (1 to 365), and iterations with " +
			"title, start_date, and duration (the field's duration when omitted); without iterations the field " +
			"starts empty"},
	},
	Fields: []capability.Field{changedField},
	Examples: []capability.Example{{
		Description: "Create a priority field",
		Arguments: json.RawMessage(`{"name":"Priority","type":"single_select","options":[` +
			`{"name":"P1","color":"red"},{"name":"P2","color":"yellow"},{"name":"P3"}]}`),
	}, {
		Description: "Create a two-week sprint field with its first sprints",
		Arguments: json.RawMessage(`{"name":"Sprint","type":"iteration","iteration":{"start_date":"2026-10-05",` +
			`"duration":14,"iterations":[{"title":"Sprint 1","start_date":"2026-10-05"},` +
			`{"title":"Sprint 2","start_date":"2026-10-19"}]}}`),
	}},
}

var fieldsUpdate = capability.Descriptor{
	ID:      Provider + ".projectfields.update",
	Version: 1,
	Title:   "Update a GitHub project field",
	Description: "Rename one field of a GitHub project an explicit connection allows, or add, rename, " +
		"recolor, describe, or reorder the options of a single- or multi-select field, the built-in Status " +
		"included; options left out stay, and so do the values items hold. Iterations change through " +
		"github.projectiterations.replace",
	Tags:                       []string{"github", "projects", "fields", "options", "update", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"field":` + fieldNameSchema + `,` +
		`"name":` + fieldNameSchema + `,"options":` + optionChangesSchema + `,"order":` + namesSchema + `},` +
		`"required":["field"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(changedFieldOutput),
	Arguments: []capability.Argument{
		fieldArgument,
		{Name: "name", Description: "New name of the field; the built-in Status keeps its name"},
		{Name: "options", Description: "Options to change or add, by name without case: new_name renames an " +
			"option, color and description replace its own; a name the field lacks adds an option at the end, " +
			"gray and without description unless given; at most 50"},
		{Name: "order", Description: "Every option name of the field after the other changes, each exactly " +
			"once, in the new order"},
	},
	Fields: []capability.Field{changedField},
	Examples: []capability.Example{{
		Description: "Add a status option and put it before Done",
		Arguments: json.RawMessage(`{"field":"Status","options":[{"name":"Review","color":"purple"}],` +
			`"order":["Todo","In progress","Review","Done"]}`),
	}, {
		Description: "Rename a field and one of its options",
		Arguments:   json.RawMessage(`{"field":"Priority","name":"Urgency","options":[{"name":"P1","new_name":"High"}]}`),
	}},
}

var fieldOptionsDelete = capability.Descriptor{
	ID:      Provider + ".projectfieldoptions.delete",
	Version: 1,
	Title:   "Delete options of a GitHub project field",
	Description: "Remove named options of a single- or multi-select field of a GitHub project an explicit " +
		"connection allows; every item loses a removed option, and at least one option stays. Offered only by a " +
		"connection whose tools list names it",
	Tags:                       []string{"github", "projects", "fields", "options", "delete"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"field":` + fieldNameSchema + `,` +
		`"options":` + namesSchema + `},"required":["field","options"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(removedOutput),
	Arguments: []capability.Argument{
		fieldArgument,
		{Name: "options", Description: "Names of the options to remove, compared without case", Required: true},
	},
	Fields: []capability.Field{changedField,
		{Name: "removed", Description: "Names of the removed options"}},
	Examples: []capability.Example{{
		Description: "Remove an unused priority",
		Arguments:   json.RawMessage(`{"field":"Priority","options":["P3"]}`),
	}},
}

var iterationsReplace = capability.Descriptor{
	ID:      Provider + ".projectiterations.replace",
	Version: 1,
	Title:   "Replace the iterations of a GitHub project field",
	Description: "Change the start date or duration of one iteration field of a GitHub project an explicit " +
		"connection allows, and add, change, or remove its iterations; GitHub recreates every iteration, so " +
		"every item loses its value of this field. Offered only by a connection whose tools list names it",
	Tags:                       []string{"github", "projects", "fields", "iterations", "update", "delete"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"field":` + fieldNameSchema + `,` +
		`"start_date":` + dateSchema + `,"duration":` + durationSchema + `,"iterations":` +
		iterationChangesSchema + `,"remove":` + namesSchema + `},"required":["field"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(removedOutput),
	Arguments: []capability.Argument{
		fieldArgument,
		{Name: "start_date", Description: "New start date of the field's first iteration, as YYYY-MM-DD; the " +
			"first iteration's start when omitted"},
		{Name: "duration", Description: "New default duration in days, 1 to 365"},
		{Name: "iterations", Description: "Iterations to change or add, by title without case: new_title, " +
			"start_date, and duration change an iteration; a title the field lacks adds one, which needs " +
			"start_date and takes the field's duration unless given; iterations left out stay"},
		{Name: "remove", Description: "Titles of the iterations to remove"},
	},
	Fields: []capability.Field{changedField,
		{Name: "removed", Description: "Titles of the removed iterations"}},
	Examples: []capability.Example{{
		Description: "Plan the next sprint and drop an old one",
		Arguments: json.RawMessage(`{"field":"Sprint","iterations":[{"title":"Sprint 3",` +
			`"start_date":"2026-11-02"}],"remove":["Sprint 0"]}`),
	}},
}

var fieldsDelete = capability.Descriptor{
	ID:      Provider + ".projectfields.delete",
	Version: 1,
	Title:   "Delete a GitHub project field",
	Description: "Delete one field that is not built in, with its values on every item, from a GitHub " +
		"project an explicit connection allows. Offered only by a connection whose tools list names it",
	Tags:                       []string{"github", "projects", "fields", "delete"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"field":` + fieldNameSchema + `},` +
		`"required":["field"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
		`"deleted":{"type":"boolean"}},"required":["name","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{fieldArgument},
	Fields: []capability.Field{
		{Name: "name", Description: "Name of the deleted field"},
		{Name: "deleted", Description: "True once GitHub deleted the field"},
	},
	Examples: []capability.Example{{
		Description: "Delete a field",
		Arguments:   json.RawMessage(`{"field":"Estimate"}`),
	}},
}

// fieldOperations are the field schema tools.
func fieldOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: fieldsList, Handler: capability.Handler(invokeFieldsList)},
		{Descriptor: fieldsCreate, Handler: capability.Handler(invokeFieldsCreate)},
		{Descriptor: fieldsUpdate, Handler: capability.Handler(invokeFieldsUpdate)},
		{Descriptor: fieldOptionsDelete, Handler: capability.Handler(invokeFieldOptionsDelete)},
		{Descriptor: iterationsReplace, Handler: capability.Handler(invokeIterationsReplace)},
		{Descriptor: fieldsDelete, Handler: capability.Handler(invokeFieldsDelete)},
	}
}

// ProjectField is one field of a project as the schema tools show it.
type ProjectField struct {
	Name      string             `json:"name"`
	Type      string             `json:"type"`
	BuiltIn   bool               `json:"built_in"`
	Options   []FieldOption      `json:"options,omitempty"`
	Iteration *IterationSettings `json:"iteration,omitempty"`
}

// FieldOption is one option of a single- or multi-select field.
type FieldOption struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// IterationSettings are the duration and start day of an iteration field and every iteration it holds, in
// start order.
type IterationSettings struct {
	Duration   int         `json:"duration"`
	StartDay   string      `json:"start_day"`
	Iterations []Iteration `json:"iterations"`
}

// Iteration is one iteration: completed, current, or planned.
type Iteration struct {
	Title     string `json:"title"`
	StartDate string `json:"start_date"`
	Duration  int    `json:"duration"`
	State     string `json:"state"`
}

// FieldSchema is the answer of the field list.
type FieldSchema struct {
	Fields []ProjectField `json:"fields"`
}

// ChangedField is the answer of a field change.
type ChangedField struct {
	Field ProjectField `json:"field"`
}

// RemovedEntries is the answer of a change that removed options or iterations.
type RemovedEntries struct {
	Field   ProjectField `json:"field"`
	Removed []string     `json:"removed"`
}

// DeletedField is the answer of a deleted field.
type DeletedField struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// today is the date iteration states are judged by.
var today = func() time.Time { return time.Now().UTC() }

// custom reports whether a field is of a type fields can be created with.
func (field fieldJSON) custom() bool {
	for _, dataType := range fieldTypes {
		if field.DataType == dataType {
			return true
		}
	}
	return false
}

// status reports whether a field is the built-in Status field. GitHub neither deletes nor renames it, so
// its name identifies it.
func (field fieldJSON) status() bool {
	return field.DataType == "SINGLE_SELECT" && strings.EqualFold(field.Name, statusFieldName)
}

// builtIn reports whether a field came with the project and cannot be deleted.
func (field fieldJSON) builtIn() bool {
	return !field.custom() || field.status()
}

// schema is the Qatlas view of one field.
func (field fieldJSON) schema() ProjectField {
	out := ProjectField{Name: field.Name, Type: strings.ToLower(field.DataType), BuiltIn: field.builtIn()}
	for _, option := range field.Options {
		out.Options = append(out.Options, FieldOption{Name: option.Name, Color: strings.ToLower(option.Color),
			Description: option.Description})
	}
	if field.DataType != "ITERATION" || field.Configuration == nil {
		return out
	}
	configuration := field.Configuration
	// GitHub counts the start day from Sunday as 0.
	settings := &IterationSettings{Duration: configuration.Duration, Iterations: []Iteration{},
		StartDay: strings.ToLower(time.Weekday(configuration.StartDay % 7).String())}
	date := today().Format(time.DateOnly)
	for _, iteration := range configuration.Iterations {
		state := "planned"
		start, err := time.Parse(time.DateOnly, iteration.StartDate)
		if err == nil && iteration.StartDate <= date &&
			start.AddDate(0, 0, iteration.Duration).Format(time.DateOnly) > date {
			state = "current"
		}
		settings.Iterations = append(settings.Iterations, Iteration{Title: iteration.Title,
			StartDate: iteration.StartDate, Duration: iteration.Duration, State: state})
	}
	for _, iteration := range configuration.CompletedIterations {
		settings.Iterations = append(settings.Iterations, Iteration{Title: iteration.Title,
			StartDate: iteration.StartDate, Duration: iteration.Duration, State: "completed"})
	}
	sort.SliceStable(settings.Iterations, func(i, j int) bool {
		return settings.Iterations[i].StartDate < settings.Iterations[j].StartDate
	})
	out.Iteration = settings
	return out
}

// fieldNamed resolves the field model of the bound project and the one field a change names. It is the
// shared name resolution of every tool that addresses a field.
func (c *Client) fieldNamed(ctx context.Context, op, name string) (*projectInfo, fieldJSON, error) {
	if c.target.kind != kindProject {
		return nil, fieldJSON{}, providerError(op, "this connection is not bound to a project")
	}
	info, _, err := c.resolve(ctx, op, planningRequest{fields: true})
	if err != nil {
		return nil, fieldJSON{}, err
	}
	field, err := info.named("field", name)
	if err != nil {
		return nil, fieldJSON{}, err
	}
	return info, field, nil
}

// named finds the field a name in argument addresses, or names every field of the project in its refusal.
func (info *projectInfo) named(argument, name string) (fieldJSON, error) {
	field, ok := info.field(name)
	if !ok {
		return fieldJSON{}, invalidRequest(argument + " is not a field of this project; its fields are " +
			strings.Join(info.fieldNames(), ", "))
	}
	return field, nil
}

func (info *projectInfo) fieldNames() []string {
	names := make([]string, len(info.ordered))
	for i, field := range info.ordered {
		names[i] = field.Name
	}
	return names
}

// ListFields reads the field schema of the bound project.
func (c *Client) ListFields(ctx context.Context) (*FieldSchema, error) {
	const op = "list project fields"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	info, _, err := c.resolve(ctx, op, planningRequest{fields: true})
	if err != nil {
		return nil, err
	}
	schema := &FieldSchema{Fields: make([]ProjectField, len(info.ordered))}
	for i, field := range info.ordered {
		schema.Fields[i] = field.schema()
	}
	return schema, nil
}

// OptionSpec names one option of a create or an update. A nil setting keeps the option's own value, or,
// for a new option, takes gray and an empty description.
type OptionSpec struct {
	Name        string  `json:"name"`
	NewName     *string `json:"new_name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
}

// IterationEntry names one iteration of a new field or of a replacement; a zero value keeps the iteration's
// own, or, for a new iteration, takes the field's duration.
type IterationEntry struct {
	Title     string  `json:"title"`
	NewTitle  *string `json:"new_title"`
	StartDate string  `json:"start_date"`
	Duration  int     `json:"duration"`
}

// IterationSpec are the settings of a new iteration field.
type IterationSpec struct {
	StartDate  string           `json:"start_date"`
	Duration   int              `json:"duration"`
	Iterations []IterationEntry `json:"iterations"`
}

// FieldSpec is a new field.
type FieldSpec struct {
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Options   []OptionSpec   `json:"options"`
	Iteration *IterationSpec `json:"iteration"`
}

// FieldChanges are the changes of one field that keep every value; settings left out stay.
type FieldChanges struct {
	Field   string       `json:"field"`
	Name    *string      `json:"name"`
	Options []OptionSpec `json:"options"`
	Order   []string     `json:"order"`
}

// OptionRemoval names the options to remove from one field.
type OptionRemoval struct {
	Field   string   `json:"field"`
	Options []string `json:"options"`
}

// IterationChanges change the settings and the iterations of one iteration field; a zero start date or
// duration keeps the field's own, and iterations neither named nor removed stay.
type IterationChanges struct {
	Field      string           `json:"field"`
	StartDate  string           `json:"start_date"`
	Duration   int              `json:"duration"`
	Iterations []IterationEntry `json:"iterations"`
	Remove     []string         `json:"remove"`
}

func checkName(argument, name string) error {
	if !safeFieldName(name) {
		return invalidRequest(argument + " must be 1 to 100 characters without control characters or " +
			"surrounding spaces")
	}
	return nil
}

func checkDate(argument, value string) error {
	if _, err := time.Parse(time.DateOnly, value); err != nil {
		return invalidRequest(argument + " must be a date as YYYY-MM-DD")
	}
	return nil
}

func checkDuration(argument string, days int) error {
	if days < 1 || days > maxIterationLength {
		return invalidRequest(fmt.Sprintf("%s must be between 1 and %d days", argument, maxIterationLength))
	}
	return nil
}

// checkNames checks a list of names that must each be usable and distinct, compared without case.
func checkNames(argument string, names []string, most int) error {
	if len(names) > most {
		return invalidRequest(fmt.Sprintf("%s accepts at most %d entries", argument, most))
	}
	seen := map[string]bool{}
	for _, name := range names {
		if err := checkName("a name in "+argument, name); err != nil {
			return err
		}
		if seen[strings.ToLower(name)] {
			return invalidRequest(argument + " names one entry more than once")
		}
		seen[strings.ToLower(name)] = true
	}
	return nil
}

func (spec OptionSpec) check(update bool) error {
	if spec.NewName != nil {
		if !update {
			return invalidRequest("new_name renames an existing option and is not part of a new field")
		}
		if err := checkName("new_name", *spec.NewName); err != nil {
			return err
		}
	}
	if spec.Color != nil && !containsFold(optionColors, *spec.Color) {
		return invalidRequest("color must be one of " + strings.Join(optionColors, ", "))
	}
	if spec.Description != nil && utf8.RuneCountInString(*spec.Description) > maxOptionText {
		return invalidRequest(fmt.Sprintf("an option description holds at most %d characters", maxOptionText))
	}
	return nil
}

func checkOptions(options []OptionSpec, update bool) error {
	names := make([]string, len(options))
	for i, option := range options {
		if err := option.check(update); err != nil {
			return err
		}
		names[i] = option.Name
	}
	return checkNames("options", names, maxOptions)
}

// checkIterations checks the iterations of a new field or of a replacement. A new field names only new
// iterations, each with its start date.
func checkIterations(entries []IterationEntry, replace bool) error {
	titles := make([]string, len(entries))
	for i, entry := range entries {
		if entry.NewTitle != nil {
			if !replace {
				return invalidRequest("new_title renames an existing iteration and is not part of a new field")
			}
			if err := checkName("new_title", *entry.NewTitle); err != nil {
				return err
			}
		}
		if entry.StartDate != "" || !replace {
			if err := checkDate("the start_date of an iteration", entry.StartDate); err != nil {
				return err
			}
		}
		if entry.Duration != 0 {
			if err := checkDuration("the duration of an iteration", entry.Duration); err != nil {
				return err
			}
		}
		titles[i] = entry.Title
	}
	return checkNames("iterations", titles, maxIterations)
}

func (spec FieldSpec) check() error {
	if err := checkName("name", spec.Name); err != nil {
		return err
	}
	dataType, ok := fieldTypes[spec.Type]
	if !ok {
		return invalidRequest("type must be text, number, date, single_select, multi_select, or iteration")
	}
	selectType := dataType == "SINGLE_SELECT" || dataType == "MULTI_SELECT"
	switch {
	case selectType && len(spec.Options) == 0:
		return invalidRequest("a " + spec.Type + " field needs options")
	case !selectType && spec.Options != nil:
		return invalidRequest("options belong to a single_select or multi_select field")
	case dataType == "ITERATION" && spec.Iteration == nil:
		return invalidRequest("an iteration field needs iteration with start_date and duration")
	case dataType != "ITERATION" && spec.Iteration != nil:
		return invalidRequest("iteration belongs to an iteration field")
	}
	if err := checkOptions(spec.Options, false); err != nil {
		return err
	}
	if spec.Iteration == nil {
		return nil
	}
	if err := checkDate("start_date", spec.Iteration.StartDate); err != nil {
		return err
	}
	if err := checkDuration("duration", spec.Iteration.Duration); err != nil {
		return err
	}
	return checkIterations(spec.Iteration.Iterations, false)
}

func (changes FieldChanges) check() error {
	if err := checkName("field", changes.Field); err != nil {
		return err
	}
	if changes.Name == nil && changes.Options == nil && changes.Order == nil {
		return invalidRequest("name at least one of name, options, or order to change")
	}
	if changes.Name != nil {
		if err := checkName("name", *changes.Name); err != nil {
			return err
		}
	}
	if err := checkOptions(changes.Options, true); err != nil {
		return err
	}
	return checkNames("order", changes.Order, maxOptions)
}

func (removal OptionRemoval) check() error {
	if err := checkName("field", removal.Field); err != nil {
		return err
	}
	if len(removal.Options) == 0 {
		return invalidRequest("options must name at least one option to remove")
	}
	return checkNames("options", removal.Options, maxOptions)
}

func (changes IterationChanges) check() error {
	if err := checkName("field", changes.Field); err != nil {
		return err
	}
	if changes.StartDate == "" && changes.Duration == 0 && len(changes.Iterations) == 0 && len(changes.Remove) == 0 {
		return invalidRequest("name at least one of start_date, duration, iterations, or remove")
	}
	if changes.StartDate != "" {
		if err := checkDate("start_date", changes.StartDate); err != nil {
			return err
		}
	}
	if changes.Duration != 0 {
		if err := checkDuration("duration", changes.Duration); err != nil {
			return err
		}
	}
	if err := checkIterations(changes.Iterations, true); err != nil {
		return err
	}
	if err := checkNames("remove", changes.Remove, maxIterations); err != nil {
		return err
	}
	for _, entry := range changes.Iterations {
		if containsFold(changes.Remove, entry.Title) {
			return invalidRequest("an iteration is named in both iterations and remove")
		}
	}
	return nil
}

// optionEntry is one option as GitHub takes it; an existing option keeps its identifier.
type optionEntry struct {
	id, name, color, description string
}

func (e optionEntry) input() map[string]any {
	input := map[string]any{"name": e.name, "color": strings.ToUpper(e.color), "description": e.description}
	if e.id != "" {
		input["id"] = e.id
	}
	return input
}

func optionInputs(entries []optionEntry) []map[string]any {
	inputs := make([]map[string]any, len(entries))
	for i, entry := range entries {
		inputs[i] = entry.input()
	}
	return inputs
}

func newOption(spec OptionSpec) optionEntry {
	entry := optionEntry{name: spec.Name, color: "GRAY"}
	if spec.Color != nil {
		entry.color = *spec.Color
	}
	if spec.Description != nil {
		entry.description = *spec.Description
	}
	return entry
}

func existingOptions(field fieldJSON) []optionEntry {
	entries := make([]optionEntry, len(field.Options))
	for i, option := range field.Options {
		entries[i] = optionEntry{id: option.ID, name: option.Name, color: option.Color,
			description: option.Description}
	}
	return entries
}

func optionNames(entries []optionEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.name
	}
	return names
}

func indexFold(names []string, want string) int {
	for i, name := range names {
		if strings.EqualFold(name, want) {
			return i
		}
	}
	return -1
}

// distinct reports whether no two names are alike without case.
func distinct(names []string) bool {
	seen := map[string]bool{}
	for _, name := range names {
		if seen[strings.ToLower(name)] {
			return false
		}
		seen[strings.ToLower(name)] = true
	}
	return true
}

// mergeOptions applies option changes, additions, and an order to the options of a field. Every existing
// option stays, with its identifier.
func mergeOptions(field fieldJSON, specs []OptionSpec, order []string) ([]optionEntry, error) {
	entries := existingOptions(field)
	for _, spec := range specs {
		i := indexFold(optionNames(entries), spec.Name)
		if i < 0 {
			if spec.NewName != nil {
				return nil, invalidRequest("new_name renames an option field " + field.Name + " holds; its options " +
					"are " + strings.Join(optionNames(existingOptions(field)), ", "))
			}
			entries = append(entries, newOption(spec))
			continue
		}
		if spec.NewName != nil {
			entries[i].name = *spec.NewName
		}
		if spec.Color != nil {
			entries[i].color = *spec.Color
		}
		if spec.Description != nil {
			entries[i].description = *spec.Description
		}
	}
	if len(entries) > maxOptions {
		return nil, invalidRequest(fmt.Sprintf("a field holds at most %d options", maxOptions))
	}
	if !distinct(optionNames(entries)) {
		return nil, invalidRequest("field " + field.Name + " would hold two options of one name")
	}
	if order == nil {
		return entries, nil
	}
	ordered := make([]optionEntry, 0, len(entries))
	for _, name := range order {
		if i := indexFold(optionNames(entries), name); i >= 0 {
			ordered = append(ordered, entries[i])
		}
	}
	if len(order) != len(entries) || len(ordered) != len(entries) {
		return nil, invalidRequest("order must name every option of field " + field.Name + " exactly once: " +
			strings.Join(optionNames(entries), ", "))
	}
	return ordered, nil
}

// iterationEntry is one iteration as GitHub takes it.
type iterationEntry struct {
	title, startDate string
	duration         int
}

// existingIterations are the iterations of a field in start order, completed ones included.
func existingIterations(field fieldJSON) []iterationEntry {
	entries := []iterationEntry{}
	if field.Configuration == nil {
		return entries
	}
	for _, list := range [][]iterationJSON{field.Configuration.CompletedIterations, field.Configuration.Iterations} {
		for _, iteration := range list {
			entries = append(entries, iterationEntry{title: iteration.Title, startDate: iteration.StartDate,
				duration: iteration.Duration})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].startDate < entries[j].startDate })
	return entries
}

func iterationTitles(entries []iterationEntry) []string {
	titles := make([]string, len(entries))
	for i, entry := range entries {
		titles[i] = entry.title
	}
	return titles
}

// iterationConfiguration is the whole configuration GitHub takes for an iteration field: its start date,
// its duration, and every iteration it keeps, in start order. The start date is the given one, or else the
// start of the first iteration, or else the field's own first start.
func iterationConfiguration(field fieldJSON, startDate string, duration int,
	entries []iterationEntry) (map[string]any, error) {
	if len(entries) > maxIterations {
		return nil, invalidRequest(fmt.Sprintf("a field holds at most %d iterations here", maxIterations))
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].startDate < entries[j].startDate })
	if duration == 0 && field.Configuration != nil {
		duration = field.Configuration.Duration
	}
	if startDate == "" && len(entries) > 0 {
		startDate = entries[0].startDate
	}
	if existing := existingIterations(field); startDate == "" && len(existing) > 0 {
		startDate = existing[0].startDate
	}
	if startDate == "" || duration == 0 {
		return nil, invalidRequest("field " + field.Name + " needs start_date and duration")
	}
	iterations := make([]map[string]any, len(entries))
	for i, entry := range entries {
		iterations[i] = map[string]any{"title": entry.title, "startDate": entry.startDate, "duration": entry.duration}
	}
	return map[string]any{"startDate": startDate, "duration": duration, "iterations": iterations}, nil
}

// mergeIterations removes, changes, and adds iterations of a field. Every other iteration, completed ones
// included, stays. It returns the configuration and the titles of the removed iterations.
func mergeIterations(field fieldJSON, changes IterationChanges) (map[string]any, []string, error) {
	existing := existingIterations(field)
	entries, removed := []iterationEntry{}, []string{}
	for _, entry := range existing {
		if containsFold(changes.Remove, entry.title) {
			removed = append(removed, entry.title)
			continue
		}
		entries = append(entries, entry)
	}
	if len(removed) != len(changes.Remove) {
		return nil, nil, invalidRequest("remove names an iteration field " + field.Name + " lacks; its iterations " +
			"are " + strings.Join(iterationTitles(existing), ", "))
	}
	duration := changes.Duration
	if duration == 0 && field.Configuration != nil {
		duration = field.Configuration.Duration
	}
	for _, change := range changes.Iterations {
		i := indexFold(iterationTitles(entries), change.Title)
		if i < 0 {
			if change.NewTitle != nil {
				return nil, nil, invalidRequest("new_title renames an iteration field " + field.Name + " holds; its " +
					"iterations are " + strings.Join(iterationTitles(existing), ", "))
			}
			if change.StartDate == "" {
				return nil, nil, invalidRequest("a new iteration of field " + field.Name + " needs start_date")
			}
			entry := iterationEntry{title: change.Title, startDate: change.StartDate, duration: change.Duration}
			if entry.duration == 0 {
				entry.duration = duration
			}
			entries = append(entries, entry)
			continue
		}
		if change.NewTitle != nil {
			entries[i].title = *change.NewTitle
		}
		if change.StartDate != "" {
			entries[i].startDate = change.StartDate
		}
		if change.Duration != 0 {
			entries[i].duration = change.Duration
		}
	}
	if !distinct(iterationTitles(entries)) {
		return nil, nil, invalidRequest("field " + field.Name + " would hold two iterations of one title")
	}
	configuration, err := iterationConfiguration(field, changes.StartDate, changes.Duration, entries)
	return configuration, removed, err
}

// The mutations of the field schema. Every identifier and value travels as a variable; an update names
// only the settings it changes.
const (
	fieldAnswerSelection = `projectV2Field{` + fieldNodeSelection + `}`
	createFieldMutation  = `mutation($project:ID!,$type:ProjectV2CustomFieldType!,$name:String!,` +
		`$single:[ProjectV2SingleSelectFieldOptionInput!],$multi:[ProjectV2MultiSelectFieldOptionInput!],` +
		`$iteration:ProjectV2IterationFieldConfigurationInput){field:createProjectV2Field(input:{` +
		`projectId:$project,dataType:$type,name:$name,singleSelectOptions:$single,multiSelectOptions:$multi,` +
		`iterationConfiguration:$iteration}){` + fieldAnswerSelection + `}}`
	deleteFieldMutation = `mutation($field:ID!){field:deleteProjectV2Field(input:{fieldId:$field}){` +
		`projectV2Field{... on ProjectV2FieldCommon{id}}}}`
)

type fieldAnswerJSON struct {
	Field *struct {
		Field *fieldJSON `json:"projectV2Field"`
	} `json:"field"`
}

// changed reads the field a change answered with. The change already happened, so an answer without it, or
// with another field than the changed one, leaves the outcome open.
func (answer fieldAnswerJSON) changed(op, id string) (ProjectField, error) {
	if answer.Field == nil || answer.Field.Field == nil || answer.Field.Field.ID == "" ||
		(id != "" && answer.Field.Field.ID != id) {
		return ProjectField{}, invalidResponse(op, true)
	}
	field := *answer.Field.Field
	field.merge()
	return field.schema(), nil
}

// CreateField creates one field in the bound project. The name must not be taken yet.
func (c *Client) CreateField(ctx context.Context, spec FieldSpec) (*ChangedField, error) {
	const op = "create project field"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := spec.check(); err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{fields: true})
	if err != nil {
		return nil, err
	}
	if _, taken := info.field(spec.Name); taken {
		return nil, invalidRequest("this project already holds a field of that name")
	}
	variables := map[string]any{"project": info.id, "type": fieldTypes[spec.Type], "name": spec.Name,
		"single": nil, "multi": nil, "iteration": nil}
	options := make([]optionEntry, len(spec.Options))
	for i, option := range spec.Options {
		options[i] = newOption(option)
	}
	switch spec.Type {
	case "single_select":
		variables["single"] = optionInputs(options)
	case "multi_select":
		variables["multi"] = optionInputs(options)
	case "iteration":
		entries := make([]iterationEntry, len(spec.Iteration.Iterations))
		for i, iteration := range spec.Iteration.Iterations {
			entries[i] = iterationEntry{title: iteration.Title, startDate: iteration.StartDate,
				duration: iteration.Duration}
			if entries[i].duration == 0 {
				entries[i].duration = spec.Iteration.Duration
			}
		}
		configuration, err := iterationConfiguration(fieldJSON{Name: spec.Name}, spec.Iteration.StartDate,
			spec.Iteration.Duration, entries)
		if err != nil {
			return nil, err
		}
		variables["iteration"] = configuration
	}
	var answer fieldAnswerJSON
	if err := c.mutate(ctx, op, createFieldMutation, variables, &answer); err != nil {
		return nil, err
	}
	field, err := answer.changed(op, "")
	if err != nil {
		return nil, err
	}
	return &ChangedField{Field: field}, nil
}

// updateField sends one updateProjectV2Field with the settings it names and reads the field it answers.
func (c *Client) updateField(ctx context.Context, op string, field fieldJSON, settings map[string]any) (ProjectField, error) {
	declarations, inputs := []string{"$field:ID!"}, []string{"fieldId:$field"}
	variables := map[string]any{"field": field.ID}
	for _, setting := range []struct{ name, kind string }{
		{"name", "String!"},
		{"singleSelectOptions", "[ProjectV2SingleSelectFieldOptionInput!]!"},
		{"multiSelectOptions", "[ProjectV2MultiSelectFieldOptionInput!]!"},
		{"iterationConfiguration", "ProjectV2IterationFieldConfigurationInput!"},
	} {
		if value, ok := settings[setting.name]; ok {
			declarations = append(declarations, "$"+setting.name+":"+setting.kind)
			inputs = append(inputs, setting.name+":$"+setting.name)
			variables[setting.name] = value
		}
	}
	document := "mutation(" + strings.Join(declarations, ",") + "){field:updateProjectV2Field(input:{" +
		strings.Join(inputs, ",") + "}){" + fieldAnswerSelection + "}}"
	var answer fieldAnswerJSON
	if err := c.mutate(ctx, op, document, variables, &answer); err != nil {
		return ProjectField{}, err
	}
	return answer.changed(op, field.ID)
}

// optionsSetting is the name of the input that replaces the options of a select field.
func optionsSetting(field fieldJSON) string {
	if field.DataType == "MULTI_SELECT" {
		return "multiSelectOptions"
	}
	return "singleSelectOptions"
}

func selectField(field fieldJSON) bool {
	return field.DataType == "SINGLE_SELECT" || field.DataType == "MULTI_SELECT"
}

// UpdateField renames one field of the bound project, or changes, adds, or reorders its options, in one
// mutation. Existing options keep their identifiers, so no item loses a value.
func (c *Client) UpdateField(ctx context.Context, changes FieldChanges) (*ChangedField, error) {
	const op = "update project field"
	if err := changes.check(); err != nil {
		return nil, err
	}
	info, field, err := c.fieldNamed(ctx, op, changes.Field)
	if err != nil {
		return nil, err
	}
	switch {
	case !field.custom():
		return nil, invalidRequest("field " + field.Name + " is built in and cannot be changed")
	case field.status() && changes.Name != nil && *changes.Name != field.Name:
		// GitHub answers a new name of the Status field with success but keeps the old one.
		return nil, invalidRequest("field " + field.Name + " is built in and keeps its name")
	case !selectField(field) && (changes.Options != nil || changes.Order != nil):
		return nil, invalidRequest("field " + field.Name + " has no options; options and order belong to a " +
			"single_select or multi_select field")
	}
	settings := map[string]any{}
	if changes.Name != nil {
		if other, taken := info.field(*changes.Name); taken && other.ID != field.ID {
			return nil, invalidRequest("this project already holds a field of that name")
		}
		settings["name"] = *changes.Name
	}
	if changes.Options != nil || changes.Order != nil {
		entries, err := mergeOptions(field, changes.Options, changes.Order)
		if err != nil {
			return nil, err
		}
		settings[optionsSetting(field)] = optionInputs(entries)
	}
	changed, err := c.updateField(ctx, op, field, settings)
	if err != nil {
		return nil, err
	}
	return &ChangedField{Field: changed}, nil
}

// RemoveOptions removes named options from one select field of the bound project. Every item loses a
// removed option; the options that stay keep their identifiers.
func (c *Client) RemoveOptions(ctx context.Context, removal OptionRemoval) (*RemovedEntries, error) {
	const op = "delete project field options"
	if err := removal.check(); err != nil {
		return nil, err
	}
	_, field, err := c.fieldNamed(ctx, op, removal.Field)
	if err != nil {
		return nil, err
	}
	if !selectField(field) {
		return nil, invalidRequest("field " + field.Name + " has no options")
	}
	kept, removed := []optionEntry{}, []string{}
	for _, entry := range existingOptions(field) {
		if containsFold(removal.Options, entry.name) {
			removed = append(removed, entry.name)
			continue
		}
		kept = append(kept, entry)
	}
	if len(removed) != len(removal.Options) {
		return nil, invalidRequest("options names an option field " + field.Name + " lacks; its options are " +
			strings.Join(optionNames(existingOptions(field)), ", "))
	}
	if len(kept) == 0 {
		return nil, invalidRequest("field " + field.Name + " keeps at least one option; delete the field instead")
	}
	changed, err := c.updateField(ctx, op, field, map[string]any{optionsSetting(field): optionInputs(kept)})
	if err != nil {
		return nil, err
	}
	return &RemovedEntries{Field: changed, Removed: removed}, nil
}

// ReplaceIterations changes the settings of one iteration field of the bound project and removes, changes,
// and adds its iterations. GitHub recreates every iteration, so every item loses its value of the field.
func (c *Client) ReplaceIterations(ctx context.Context, changes IterationChanges) (*RemovedEntries, error) {
	const op = "replace project iterations"
	if err := changes.check(); err != nil {
		return nil, err
	}
	_, field, err := c.fieldNamed(ctx, op, changes.Field)
	if err != nil {
		return nil, err
	}
	if field.DataType != "ITERATION" {
		return nil, invalidRequest("field " + field.Name + " is no iteration field")
	}
	configuration, removed, err := mergeIterations(field, changes)
	if err != nil {
		return nil, err
	}
	changed, err := c.updateField(ctx, op, field, map[string]any{"iterationConfiguration": configuration})
	if err != nil {
		return nil, err
	}
	return &RemovedEntries{Field: changed, Removed: removed}, nil
}

// DeleteField deletes one field of the bound project that is not built in.
func (c *Client) DeleteField(ctx context.Context, name string) (*DeletedField, error) {
	const op = "delete project field"
	if err := checkName("field", name); err != nil {
		return nil, err
	}
	_, field, err := c.fieldNamed(ctx, op, name)
	if err != nil {
		return nil, err
	}
	if field.builtIn() {
		return nil, invalidRequest("field " + field.Name + " is built in and cannot be deleted")
	}
	var answer fieldAnswerJSON
	if err := c.mutate(ctx, op, deleteFieldMutation, map[string]any{"field": field.ID}, &answer); err != nil {
		return nil, err
	}
	if answer.Field == nil || answer.Field.Field == nil || answer.Field.Field.ID != field.ID {
		return nil, invalidResponse(op, true)
	}
	return &DeletedField{Name: field.Name, Deleted: true}, nil
}

// The handlers check the target and the arguments before a credential is resolved, so a refused request
// never becomes a secret read or a provider call.

func invokeFieldsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.ListFields(ctx))
}

// projectHandler decodes the arguments of one change of a project's fields or views, checks its target and
// the arguments, and only then opens the client that runs it.
func projectHandler[T interface{ check() error }](op string, run func(*Client, context.Context, T) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments T
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(op)
		}
		bound, err := selectTarget(resolved, kindProject, raw)
		if err != nil {
			return nil, err
		}
		if err := arguments.check(); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(run(client, ctx, arguments))
	}
}

// fieldName is the argument of a field delete.
type fieldName struct {
	Field string `json:"field"`
}

func (name fieldName) check() error { return checkName("field", name.Field) }

var (
	invokeFieldsCreate = projectHandler("create project field",
		func(c *Client, ctx context.Context, spec FieldSpec) (any, error) { return c.CreateField(ctx, spec) })
	invokeFieldsUpdate = projectHandler("update project field",
		func(c *Client, ctx context.Context, changes FieldChanges) (any, error) {
			return c.UpdateField(ctx, changes)
		})
	invokeFieldOptionsDelete = projectHandler("delete project field options",
		func(c *Client, ctx context.Context, removal OptionRemoval) (any, error) {
			return c.RemoveOptions(ctx, removal)
		})
	invokeIterationsReplace = projectHandler("replace project iterations",
		func(c *Client, ctx context.Context, changes IterationChanges) (any, error) {
			return c.ReplaceIterations(ctx, changes)
		})
	invokeFieldsDelete = projectHandler("delete project field",
		func(c *Client, ctx context.Context, name fieldName) (any, error) {
			return c.DeleteField(ctx, name.Field)
		})
)
