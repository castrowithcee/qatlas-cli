package makeapi

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	// The list ceilings keep one scenario entry small; a longer list is cut at the ceiling and flagged by a
	// *_truncated member, the same pattern scenarios.blueprint's masked_truncated follows.
	maxScenarioDevices  = 50
	maxScenarioPackages = 100
	maxScenarioModules  = 200
	maxScenarioLabels   = 100

	maxShortText = 64
	maxNameText  = 256
	maxPathText  = 1024
	maxEmailText = 320

	// A custom-property object over any of these limits is left out as a whole, never cut.
	maxCustomDepth   = 4
	maxCustomEntries = 64
	maxCustomString  = 1024

	maxIDFilter    = 100
	maxLabelFilter = 50
	maxTextFilter  = 256
)

// scenarioColumns is every non-deprecated column Make documents for GET /scenarios and
// GET /scenarios/{id}; Make returns the requested columns only, and some of them only on request.
var scenarioColumns = []string{"id", "name", "teamId", "hookId", "devices", "description", "folderId", "concept",
	"folderPath", "isinvalid", "isActive", "islocked", "isPaused", "usedPackages", "usedModules", "lastEdit",
	"scheduling", "iswaiting", "dlqCount", "allDlqCount", "chainingRole", "isShared", "type", "createdByUser",
	"updatedByUser", "created", "nextExec", "operations", "centicredits", "transfer", "executions", "errors",
	"customProperties", "templateMetadata", "deleted", "deletedAt", "labels"}

var scenarioSortColumns = map[string]bool{"id": true, "name": true, "teamId": true, "folderId": true,
	"isActive": true, "isinvalid": true, "islocked": true, "lastEdit": true, "createdByUserName": true,
	"updatedByUserName": true, "created": true, "dlqCount": true, "operations": true, "executions": true,
	"centicredits": true, "transfer": true, "errors": true}

type deviceJSON struct {
	ID    int64  `json:"id"`
	Scope string `json:"scope"`
}

type usedModuleJSON struct {
	PackageName string `json:"packageName"`
	ModuleName  string `json:"moduleName"`
}

type templateMetadataJSON struct {
	IsOem          *bool `json:"isOem"`
	VersionID      int64 `json:"versionId"`
	MetadataID     int64 `json:"metadataId"`
	InstantiableID int64 `json:"instantiableId"`
}

type labelJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Colour      string `json:"colour"`
	Scope       string `json:"scope"`
	TeamID      int64  `json:"teamId"`
	Description string `json:"description"`
	CreatedBy   *int64 `json:"createdBy"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
}

// ScenarioDevice is one device a scenario uses.
type ScenarioDevice struct {
	ID    int64  `json:"id"`
	Scope string `json:"scope,omitempty"`
}

// ScenarioModule is one app and module a scenario uses.
type ScenarioModule struct {
	PackageName string `json:"package_name,omitempty"`
	ModuleName  string `json:"module_name,omitempty"`
}

// TemplateMetadata links a scenario to the template it was created from.
type TemplateMetadata struct {
	IsOem          *bool `json:"is_oem,omitempty"`
	VersionID      int64 `json:"version_id,omitempty"`
	MetadataID     int64 `json:"metadata_id,omitempty"`
	InstantiableID int64 `json:"instantiable_id,omitempty"`
}

// ScenarioLabel is one team label assigned to a scenario.
type ScenarioLabel struct {
	ID          int64  `json:"id"`
	Name        string `json:"name,omitempty"`
	Colour      string `json:"colour,omitempty"`
	Scope       string `json:"scope,omitempty"`
	TeamID      int64  `json:"team_id,omitempty"`
	Description string `json:"description,omitempty"`
	CreatedBy   *int64 `json:"created_by,omitempty"`
	Created     string `json:"created,omitempty"`
	Updated     string `json:"updated,omitempty"`
}

// capList cuts a provider list at max and reports whether it did.
func capList[T any](list []T, max int) ([]T, bool) {
	if len(list) > max {
		return list[:max], true
	}
	return list, false
}

// addDetails fills the members beyond the original summary. Every absent or empty value stays absent.
func (s *ScenarioSummary) addDetails(j scenarioJSON) {
	s.HookID = j.HookID
	var cut bool
	devices, cut := capList(j.Devices, maxScenarioDevices)
	for _, d := range devices {
		s.Devices = append(s.Devices, ScenarioDevice{ID: d.ID, Scope: cleanText(d.Scope, maxShortText)})
	}
	s.DevicesTruncated = cut
	s.FolderPath = cleanText(j.FolderPath, maxPathText)
	s.Concept = j.Concept
	packages, cut := capList(j.UsedPackages, maxScenarioPackages)
	for _, p := range packages {
		s.UsedPackages = append(s.UsedPackages, cleanText(p, maxNameText))
	}
	s.UsedPackagesTruncated = cut
	modules, cut := capList(j.UsedModules, maxScenarioModules)
	for _, m := range modules {
		s.UsedModules = append(s.UsedModules, ScenarioModule{PackageName: cleanText(m.PackageName, maxNameText),
			ModuleName: cleanText(m.ModuleName, maxNameText)})
	}
	s.UsedModulesTruncated = cut
	s.IsWaiting = j.IsWaiting
	s.AllDlqCount = j.AllDlqCount
	s.ScenarioVersion = j.ScenarioVersion
	s.ModuleSequenceID = j.ModuleSequenceID
	s.Type = cleanText(j.Type, maxShortText)
	s.ChainingRole = cleanText(j.ChainingRole, maxShortText)
	s.IsShared = j.IsShared
	s.Executions = j.Executions
	s.Errors = j.Errors
	s.CustomProperties, s.CustomPropertiesOmitted = boundedCustomProperties(j.CustomProperties)
	if t := j.TemplateMetadata; t != nil && (t.IsOem != nil || t.VersionID != 0 || t.MetadataID != 0 || t.InstantiableID != 0) {
		s.TemplateMetadata = &TemplateMetadata{IsOem: t.IsOem, VersionID: t.VersionID, MetadataID: t.MetadataID,
			InstantiableID: t.InstantiableID}
	}
	labels, cut := capList(j.Labels, maxScenarioLabels)
	for _, l := range labels {
		s.Labels = append(s.Labels, ScenarioLabel{ID: l.ID, Name: cleanText(l.Name, maxNameText),
			Colour: cleanText(l.Colour, maxShortText), Scope: cleanText(l.Scope, maxShortText), TeamID: l.TeamID,
			Description: cleanText(l.Description, 2048), CreatedBy: l.CreatedBy,
			Created: cleanText(l.Created, maxShortText), Updated: cleanText(l.Updated, maxShortText)})
	}
	s.LabelsTruncated = cut
	s.Deleted = j.Deleted
	s.DeletedAt = cleanText(j.DeletedAt, maxShortText)
}

// boundedCustomProperties returns the free custom-property object when it stays inside the depth, entry, and
// length limits. One that exceeds them, or is not an object at all, is left out and reported as omitted:
// a cut object would present a partial value as the whole one. null and {} are plain absence.
func boundedCustomProperties(raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, true
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true
	}
	if len(object) == 0 {
		return nil, false
	}
	entries := 0
	if !withinCustomLimits(object, 1, &entries) {
		return nil, true
	}
	// Re-encoding drops invalid UTF-8 and keeps numbers exactly as Make wrote them.
	out, err := json.Marshal(object)
	if err != nil {
		return nil, true
	}
	return out, false
}

func withinCustomLimits(value any, depth int, entries *int) bool {
	switch typed := value.(type) {
	case map[string]any:
		if depth > maxCustomDepth {
			return false
		}
		for key, child := range typed {
			*entries++
			if *entries > maxCustomEntries || len(key) > maxCustomString || !withinCustomLimits(child, depth+1, entries) {
				return false
			}
		}
	case []any:
		if depth > maxCustomDepth {
			return false
		}
		for _, child := range typed {
			*entries++
			if *entries > maxCustomEntries || !withinCustomLimits(child, depth+1, entries) {
				return false
			}
		}
	case string:
		return len(typed) <= maxCustomString
	}
	return true
}

// ScenarioFilter holds the structured filters of scenarios.list. Each one maps to exactly one documented
// parameter of Make's GET /scenarios; none is a passthrough.
type ScenarioFilter struct {
	IsActive          *bool   `json:"is_active"`
	Name              string  `json:"name"`
	IDs               []int64 `json:"ids"`
	FolderID          int64   `json:"folder_id"`
	IncludeSubfolders *bool   `json:"include_subfolders"`
	LabelIDs          []int64 `json:"label_ids"`
	Concept           *bool   `json:"concept"`
	Type              string  `json:"type"`
	Deleted           *bool   `json:"deleted"`
	Search            string  `json:"search"`
	IsInvalid         *bool   `json:"is_invalid"`
	IsLocked          *bool   `json:"is_locked"`
	CreatedByUserName string  `json:"created_by_user_name"`
	UpdatedByUserName string  `json:"updated_by_user_name"`
	DlqCount          *int64  `json:"dlq_count"`
	SortBy            string  `json:"sort_by"`
	SortDir           string  `json:"sort_dir"`
}

// validate rejects an unusable filter before any request or secret resolution. The input schema already
// states most of this; the rules here are the ones a schema cannot, and a second line for callers of
// ListScenarios that bypass the schema.
func (f ScenarioFilter) validate() error {
	if f.IDs != nil {
		if err := positiveIDs("ids", f.IDs, maxIDFilter); err != nil {
			return err
		}
	}
	if f.LabelIDs != nil {
		if err := positiveIDs("label_ids", f.LabelIDs, maxLabelFilter); err != nil {
			return err
		}
	}
	if f.FolderID < 0 {
		return invalidRequest("folder_id must be a positive integer")
	}
	if f.IncludeSubfolders != nil && f.FolderID == 0 {
		return invalidRequest("include_subfolders needs folder_id")
	}
	// A blank-only value would be matched by Make as a literal run of spaces, so it is refused as unusable.
	for name, value := range map[string]string{"name": f.Name, "search": f.Search,
		"created_by_user_name": f.CreatedByUserName, "updated_by_user_name": f.UpdatedByUserName} {
		if value == "" {
			continue
		}
		if n := utf8.RuneCountInString(value); n > maxTextFilter || strings.TrimSpace(value) == "" {
			return invalidRequest(name + " must be 1 to " + strconv.Itoa(maxTextFilter) + " characters and not blank")
		}
	}
	if f.Type != "" && f.Type != "scenario" && f.Type != "tool" {
		return invalidRequest("type must be scenario or tool")
	}
	if f.DlqCount != nil && *f.DlqCount < 0 {
		return invalidRequest("dlq_count must be 0 or more")
	}
	if f.SortBy != "" && !scenarioSortColumns[f.SortBy] {
		return invalidRequest("sort_by is not a column Make can sort scenarios by")
	}
	if f.SortDir != "" && f.SortDir != "asc" && f.SortDir != "desc" {
		return invalidRequest("sort_dir must be asc or desc")
	}
	return nil
}

func positiveIDs(name string, ids []int64, max int) error {
	if len(ids) == 0 || len(ids) > max {
		return invalidRequest(name + " must hold 1 to " + strconv.Itoa(max) + " identifiers")
	}
	for _, id := range ids {
		if id < 1 {
			return invalidRequest(name + " must hold positive integers")
		}
	}
	return nil
}

func (f ScenarioFilter) apply(query url.Values) {
	setBool := func(key string, value *bool) {
		if value != nil {
			query.Set(key, strconv.FormatBool(*value))
		}
	}
	setText := func(key, value string) {
		if value != "" {
			query.Set(key, value)
		}
	}
	setBool("isActive", f.IsActive)
	setText("name", f.Name)
	for _, id := range f.IDs {
		query.Add("id[]", strconv.FormatInt(id, 10))
	}
	if f.FolderID > 0 {
		query.Set("folderId", strconv.FormatInt(f.FolderID, 10))
	}
	setBool("includeSubfolders", f.IncludeSubfolders)
	for _, id := range f.LabelIDs {
		query.Add("labelIds[]", strconv.FormatInt(id, 10))
	}
	setBool("concept", f.Concept)
	setText("type", f.Type)
	setBool("deleted", f.Deleted)
	setText("search", f.Search)
	setBool("isinvalid", f.IsInvalid)
	setBool("islocked", f.IsLocked)
	setText("createdByUserName", f.CreatedByUserName)
	setText("updatedByUserName", f.UpdatedByUserName)
	if f.DlqCount != nil {
		query.Set("dlqCount", strconv.FormatInt(*f.DlqCount, 10))
	}
	setText("pg[sortBy]", f.SortBy)
	setText("pg[sortDir]", f.SortDir)
}

// scenarioListInput is the input schema of scenarios.list.
var scenarioListInput = `{"type":"object","properties":{` +
	`"offset":{"type":"integer","minimum":0},` +
	`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
	`"is_active":{"type":"boolean"},"name":{"type":"string","minLength":1,"maxLength":256},` +
	`"ids":{"type":"array","items":` + idSchema + `,"minItems":1,"maxItems":100},` +
	`"folder_id":` + idSchema + `,"include_subfolders":{"type":"boolean"},` +
	`"label_ids":{"type":"array","items":` + idSchema + `,"minItems":1,"maxItems":50},` +
	`"concept":{"type":"boolean"},"type":{"type":"string","enum":["scenario","tool"]},` +
	`"deleted":{"type":"boolean"},"search":{"type":"string","minLength":1,"maxLength":256},` +
	`"is_invalid":{"type":"boolean"},"is_locked":{"type":"boolean"},` +
	`"created_by_user_name":{"type":"string","minLength":1,"maxLength":256},` +
	`"updated_by_user_name":{"type":"string","minLength":1,"maxLength":256},` +
	`"dlq_count":{"type":"integer","minimum":0},` +
	`"sort_by":{"type":"string","enum":["id","name","teamId","folderId","isActive","isinvalid","islocked",` +
	`"lastEdit","createdByUserName","updatedByUserName","created","dlqCount","operations","executions",` +
	`"centicredits","transfer","errors"]},` +
	`"sort_dir":{"type":"string","enum":["asc","desc"]}},"additionalProperties":false}`

var scenarioListArguments = []capability.Argument{
	{Name: "ids", Description: "When set, list only these scenarios, 1 to 100 positive integers"},
	{Name: "folder_id", Description: "When set, list only scenarios filed directly under this folder"},
	{Name: "include_subfolders", Description: "With folder_id only: also list scenarios of its descendant folders"},
	{Name: "label_ids", Description: "When set, list scenarios carrying any of these labels, 1 to 50 positive integers"},
	{Name: "concept", Description: "When true, list only scenario concepts"},
	{Name: "type", Description: "When set, list only scenarios (scenario) or only tools (tool)"},
	{Name: "deleted", Description: "When true, list the scenarios in the trash instead"},
	{Name: "search", Description: "When set, list scenarios whose name contains this text, case-insensitive, 1 to 256 characters; a number also matches that scenario id"},
	{Name: "is_invalid", Description: "When set, list only invalid or only valid scenarios"},
	{Name: "is_locked", Description: "When set, list only locked or only unlocked scenarios"},
	{Name: "created_by_user_name", Description: "When set, list scenarios created by the user of this name, 1 to 256 characters"},
	{Name: "updated_by_user_name", Description: "When set, list scenarios last updated by the user of this name, 1 to 256 characters"},
	{Name: "dlq_count", Description: "When set, list scenarios with exactly this number of unresolved incomplete executions"},
	{Name: "sort_by", Description: "Column to sort by: id, name, teamId, folderId, isActive, isinvalid, islocked, lastEdit, " +
		"createdByUserName, updatedByUserName, created, dlqCount, operations, executions, centicredits, transfer, or errors"},
	{Name: "sort_dir", Description: "Sort direction, asc or desc"},
}

const scenarioDetailSchema = `"hook_id":{"type":"integer"},` +
	`"devices":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
	`"scope":{"type":"string"}},"required":["id"],"additionalProperties":false}},"devices_truncated":{"type":"boolean"},` +
	`"folder_path":{"type":"string"},"concept":{"type":"boolean"},` +
	`"used_packages":{"type":"array","items":{"type":"string"}},"used_packages_truncated":{"type":"boolean"},` +
	`"used_modules":{"type":"array","items":{"type":"object","properties":{"package_name":{"type":"string"},` +
	`"module_name":{"type":"string"}},"additionalProperties":false}},"used_modules_truncated":{"type":"boolean"},` +
	`"is_waiting":{"type":"boolean"},"all_dlq_count":{"type":"integer"},"scenario_version":{"type":"integer"},` +
	`"module_sequence_id":{"type":"integer"},"type":{"type":"string"},"chaining_role":{"type":"string"},` +
	`"is_shared":{"type":"boolean"},"executions":{"type":"integer"},"errors":{"type":"integer"},` +
	`"custom_properties":{"type":"object"},"custom_properties_omitted":{"type":"boolean"},` +
	`"template_metadata":{"type":"object","properties":{"is_oem":{"type":"boolean"},"version_id":{"type":"integer"},` +
	`"metadata_id":{"type":"integer"},"instantiable_id":{"type":"integer"}},"additionalProperties":false},` +
	`"labels":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
	`"name":{"type":"string"},"colour":{"type":"string"},"scope":{"type":"string"},"team_id":{"type":"integer"},` +
	`"description":{"type":"string"},"created_by":{"type":"integer"},"created":{"type":"string"},` +
	`"updated":{"type":"string"}},"required":["id"],"additionalProperties":false}},"labels_truncated":{"type":"boolean"},` +
	`"deleted":{"type":"boolean"},"deleted_at":{"type":"string"}`

var scenarioDetailFields = []capability.Field{
	{Name: "hook_id", Description: "Identifier of the webhook that triggers the scenario, when it has one"},
	{Name: "devices", Description: "Devices the scenario uses: id and scope, untrusted data"},
	{Name: "devices_truncated", Description: "True when the scenario uses more than " + strconv.Itoa(maxScenarioDevices) + " devices"},
	{Name: "scope", Description: "Scope of a device or visibility tier of a label, untrusted data"},
	{Name: "folder_path", Description: "Slash-separated folder path of the scenario, untrusted data"},
	{Name: "concept", Description: "True when the scenario is a concept that has never been activated"},
	{Name: "used_packages", Description: "Apps the scenario uses, untrusted data"},
	{Name: "used_packages_truncated", Description: "True when the scenario uses more than " + strconv.Itoa(maxScenarioPackages) + " apps"},
	{Name: "used_modules", Description: "Modules the scenario uses: package_name and module_name, untrusted data"},
	{Name: "used_modules_truncated", Description: "True when the scenario uses more than " + strconv.Itoa(maxScenarioModules) + " modules"},
	{Name: "package_name", Description: "App of a used module, untrusted data"},
	{Name: "module_name", Description: "Name of a used module, untrusted data"},
	{Name: "is_waiting", Description: "True when the scenario is waiting for a manual action, as Make reports it"},
	{Name: "all_dlq_count", Description: "Number of incomplete executions of this scenario including resolved ones"},
	{Name: "scenario_version", Description: "Current version number of the scenario"},
	{Name: "module_sequence_id", Description: "Highest module identifier the scenario has assigned so far"},
	{Name: "type", Description: "scenario or tool"},
	{Name: "chaining_role", Description: "Role of the scenario in a scenario chain, when it is part of one, untrusted data"},
	{Name: "is_shared", Description: "True when the scenario is shared"},
	{Name: "executions", Description: "Executions of the scenario in the reported period"},
	{Name: "errors", Description: "Failed executions of the scenario in the reported period"},
	{Name: "custom_properties", Description: "Custom-property values by property name, untrusted data; a free object " +
		"that exceeds the depth, entry, or length limit is left out and marked, never cut"},
	{Name: "custom_properties_omitted", Description: "True when the custom properties exceeded the limit (depth " +
		strconv.Itoa(maxCustomDepth) + ", " + strconv.Itoa(maxCustomEntries) + " entries, strings of " +
		strconv.Itoa(maxCustomString) + " bytes) or were not an object, and were left out"},
	{Name: "template_metadata", Description: "Link to the template the scenario was created from"},
	{Name: "is_oem", Description: "True when the template is an OEM template"},
	{Name: "version_id", Description: "Version of the template"},
	{Name: "metadata_id", Description: "Metadata identifier of the template"},
	{Name: "instantiable_id", Description: "Instantiable identifier of the template"},
	{Name: "labels", Description: "Team labels assigned to the scenario, untrusted data"},
	{Name: "labels_truncated", Description: "True when the scenario carries more than " + strconv.Itoa(maxScenarioLabels) + " labels"},
	{Name: "colour", Description: "Colour token of a label, untrusted data"},
	{Name: "created_by", Description: "Identifier of the user who created a label"},
	{Name: "updated", Description: "Last update time of a label, as Make reports it"},
	{Name: "email", Description: "Email address of the creator or editor, untrusted data; read from Make's own user object, never looked up"},
	{Name: "deleted", Description: "True when the scenario is in the trash"},
	{Name: "deleted_at", Description: "Time the scenario was moved to the trash, as Make reports it"},
}

const (
	maxSchedulingList     = 64
	maxSchedulingRestrict = 32
	maxSchedulingText     = 64
)

// BlueprintScheduling is the documented scheduling object of a blueprint answer.
type BlueprintScheduling struct {
	Type                 string                  `json:"type,omitempty"`
	Interval             *int64                  `json:"interval,omitempty"`
	Date                 string                  `json:"date,omitempty"`
	Between              []string                `json:"between,omitempty"`
	Time                 string                  `json:"time,omitempty"`
	Days                 []int64                 `json:"days,omitempty"`
	Months               []int64                 `json:"months,omitempty"`
	Restrict             []SchedulingRestriction `json:"restrict,omitempty"`
	MaximumRunsPerMinute *int64                  `json:"maximum_runs_per_minute,omitempty"`
}

// SchedulingRestriction is one time window a schedule is restricted to; a null time stays null.
type SchedulingRestriction struct {
	Time   []*string `json:"time,omitempty"`
	Days   []int64   `json:"days,omitempty"`
	Months []int64   `json:"months,omitempty"`
}

// structuredScheduling reads the documented members of a blueprint's scheduling. A value that is not of that
// shape, or that exceeds a list or text limit, is left out and reported as omitted, never cut. Absence and
// an empty object are plain absence.
func structuredScheduling(raw json.RawMessage) (*BlueprintScheduling, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var s BlueprintScheduling
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, true
	}
	ok := len(s.Between) <= maxSchedulingList && len(s.Days) <= maxSchedulingList &&
		len(s.Months) <= maxSchedulingList && len(s.Restrict) <= maxSchedulingRestrict
	texts := append([]string{s.Type, s.Date, s.Time}, s.Between...)
	for _, r := range s.Restrict {
		ok = ok && len(r.Time) <= maxSchedulingList && len(r.Days) <= maxSchedulingList && len(r.Months) <= maxSchedulingList
		for _, t := range r.Time {
			if t != nil {
				texts = append(texts, *t)
			}
		}
	}
	for _, t := range texts {
		ok = ok && len(t) <= maxSchedulingText
	}
	if !ok {
		return nil, true
	}
	s.Type, s.Date, s.Time = cleanText(s.Type, maxSchedulingText), cleanText(s.Date, maxSchedulingText), cleanText(s.Time, maxSchedulingText)
	for i := range s.Between {
		s.Between[i] = cleanText(s.Between[i], maxSchedulingText)
	}
	for _, r := range s.Restrict {
		for _, t := range r.Time {
			if t != nil {
				*t = cleanText(*t, maxSchedulingText)
			}
		}
	}
	if s.Type == "" && s.Interval == nil && s.Date == "" && len(s.Between) == 0 && s.Time == "" && len(s.Days) == 0 &&
		len(s.Months) == 0 && len(s.Restrict) == 0 && s.MaximumRunsPerMinute == nil {
		return nil, false
	}
	return &s, false
}

var blueprintSchedulingSchema = `{"type":"object","properties":{"type":{"type":"string"},"interval":{"type":"integer"},` +
	`"date":{"type":"string"},"between":{"type":"array","items":{"type":"string"}},"time":{"type":"string"},` +
	`"days":{"type":"array","items":{"type":"integer"}},"months":{"type":"array","items":{"type":"integer"}},` +
	`"restrict":{"type":"array","items":{"type":"object","properties":{` +
	`"time":{"type":"array","items":{}},"days":{"type":"array","items":{"type":"integer"}},` +
	`"months":{"type":"array","items":{"type":"integer"}}},"additionalProperties":false}},` +
	`"maximum_runs_per_minute":{"type":"integer"}},"additionalProperties":false}`

var blueprintSchedulingFields = []capability.Field{
	{Name: "scheduling", Description: "How the scenario is scheduled, untrusted data: type, interval, date, between, " +
		"time, days, months, restrict, and maximum_runs_per_minute"},
	{Name: "type", Description: "Scheduling type, such as immediately, indefinitely, once, daily, weekly, monthly, or yearly"},
	{Name: "interval", Description: "Interval between runs, as Make reports it"},
	{Name: "date", Description: "Date and time of a single run"},
	{Name: "between", Description: "Date and time bounds of the schedule"},
	{Name: "time", Description: "Time of day of the run, or the times of a restriction"},
	{Name: "days", Description: "Days of the month, or the days of a restriction"},
	{Name: "months", Description: "Months of the year, or the months of a restriction"},
	{Name: "restrict", Description: "Time windows the schedule is restricted to: time, days, and months"},
	{Name: "maximum_runs_per_minute", Description: "Upper bound of runs per minute, when Make reports one"},
}
