package n8n

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The read view of a workflow (workflows.get and workflows.list) is kept apart from the view the change
// tools answer with (WorkflowDetail, workflowDetailSchema, workflowDetailFields), which stays unchanged.

const (
	// maxReadShared, maxPublishHistory, maxNodeGroups, and maxGroupNodeIDs bound the lists of one workflow
	// whatever n8n sends; the telemetry tag lists use maxProjectTelemetryTags.
	maxReadShared     = 100
	maxPublishHistory = 100
	maxNodeGroups     = 100
	maxGroupNodeIDs   = 1000
	// maxLongTextLength bounds the free texts that may legitimately be long: node notes and the caller list.
	maxLongTextLength = 4096
)

// cappedLongText is cappedText with the longer bound of maxLongTextLength.
func cappedLongText(value string) string {
	return cappedTextN(value, maxLongTextLength)
}

type telemetryTagJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type nodeTelemetryTagsJSON struct {
	Tag []telemetryTagJSON `json:"tag"`
}

type nodeGroupJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	NodeIDs     []string `json:"nodeIds"`
}

// workflowSettingsJSON mirrors the settings of n8n's workflowPublicDto. The two save flags are a boolean or
// "DEFAULT", so they are read raw.
type workflowSettingsJSON struct {
	SaveExecutionProgress    json.RawMessage    `json:"saveExecutionProgress"`
	SaveManualExecutions     json.RawMessage    `json:"saveManualExecutions"`
	SaveDataErrorExecution   string             `json:"saveDataErrorExecution"`
	SaveDataSuccessExecution string             `json:"saveDataSuccessExecution"`
	ExecutionTimeout         json.Number        `json:"executionTimeout"`
	ErrorWorkflow            string             `json:"errorWorkflow"`
	Timezone                 string             `json:"timezone"`
	ExecutionOrder           string             `json:"executionOrder"`
	BinaryMode               string             `json:"binaryMode"`
	CallerPolicy             string             `json:"callerPolicy"`
	CallerIDs                string             `json:"callerIds"`
	TimeSavedMode            string             `json:"timeSavedMode"`
	TimeSavedPerExecution    json.Number        `json:"timeSavedPerExecution"`
	RedactionPolicy          string             `json:"redactionPolicy"`
	AvailableInMCP           *bool              `json:"availableInMCP"`
	CustomTelemetryTags      []telemetryTagJSON `json:"customTelemetryTags"`
	CredentialResolverID     string             `json:"credentialResolverId"`
}

type workflowMetaJSON struct {
	OnboardingID                string `json:"onboardingId"`
	TemplateID                  string `json:"templateId"`
	InstanceID                  string `json:"instanceId"`
	TemplateCredsSetupCompleted *bool  `json:"templateCredsSetupCompleted"`
}

type publishEntryJSON struct {
	ID         json.Number `json:"id"`
	WorkflowID string      `json:"workflowId"`
	VersionID  string      `json:"versionId"`
	Event      string      `json:"event"`
	UserID     string      `json:"userId"`
	CreatedAt  string      `json:"createdAt"`
}

type activeVersionJSON struct {
	VersionID              string             `json:"versionId"`
	WorkflowID             string             `json:"workflowId"`
	Nodes                  []nodeJSON         `json:"nodes"`
	Connections            json.RawMessage    `json:"connections"`
	NodeGroups             []nodeGroupJSON    `json:"nodeGroups"`
	Authors                string             `json:"authors"`
	Name                   string             `json:"name"`
	Description            string             `json:"description"`
	Autosaved              *bool              `json:"autosaved"`
	CreatedAt              string             `json:"createdAt"`
	UpdatedAt              string             `json:"updatedAt"`
	WorkflowPublishHistory []publishEntryJSON `json:"workflowPublishHistory"`
}

// ReadTag is a workflow tag as workflows.get and workflows.list report it.
type ReadTag struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// NodeTelemetryTags holds the custom telemetry tags of a node. Both texts are untrusted data.
type NodeTelemetryTags struct {
	Tag []ProjectTelemetryTag `json:"tag"`
}

// NodeRead is a node as workflows.get and workflows.list report it: the Node view plus the execution
// attributes. Flags are pointers, so a reported false stays visible and an unreported one stays out.
type NodeRead struct {
	Node
	NotesInFlow         *bool              `json:"notes_in_flow,omitempty"`
	Notes               string             `json:"notes,omitempty"`
	ExecuteOnce         *bool              `json:"execute_once,omitempty"`
	AlwaysOutputData    *bool              `json:"always_output_data,omitempty"`
	RetryOnFail         *bool              `json:"retry_on_fail,omitempty"`
	MaxTries            json.Number        `json:"max_tries,omitempty"`
	WaitBetweenTries    json.Number        `json:"wait_between_tries,omitempty"`
	OnError             string             `json:"on_error,omitempty"`
	CustomTelemetryTags *NodeTelemetryTags `json:"custom_telemetry_tags,omitempty"`
	CreatedAt           string             `json:"created_at,omitempty"`
	UpdatedAt           string             `json:"updated_at,omitempty"`
}

// NodeGroup is a visual grouping of nodes. Its texts are untrusted data.
type NodeGroup struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	NodeIDs     []string `json:"node_ids"`
}

// WorkflowSettings is the typed settings object of a workflow. Texts are untrusted data.
type WorkflowSettings struct {
	SaveExecutionProgress    any                   `json:"save_execution_progress,omitempty"`
	SaveManualExecutions     any                   `json:"save_manual_executions,omitempty"`
	SaveDataErrorExecution   string                `json:"save_data_error_execution,omitempty"`
	SaveDataSuccessExecution string                `json:"save_data_success_execution,omitempty"`
	ExecutionTimeout         json.Number           `json:"execution_timeout,omitempty"`
	ErrorWorkflow            string                `json:"error_workflow,omitempty"`
	Timezone                 string                `json:"timezone,omitempty"`
	ExecutionOrder           string                `json:"execution_order,omitempty"`
	BinaryMode               string                `json:"binary_mode,omitempty"`
	CallerPolicy             string                `json:"caller_policy,omitempty"`
	CallerIDs                string                `json:"caller_ids,omitempty"`
	TimeSavedMode            string                `json:"time_saved_mode,omitempty"`
	TimeSavedPerExecution    json.Number           `json:"time_saved_per_execution,omitempty"`
	RedactionPolicy          string                `json:"redaction_policy,omitempty"`
	AvailableInMCP           *bool                 `json:"available_in_mcp,omitempty"`
	CustomTelemetryTags      []ProjectTelemetryTag `json:"custom_telemetry_tags,omitempty"`
	CredentialResolverID     string                `json:"credential_resolver_id,omitempty"`
}

// WorkflowMeta is the metadata object of a workflow. Texts are untrusted data.
type WorkflowMeta struct {
	OnboardingID                string `json:"onboarding_id,omitempty"`
	TemplateID                  string `json:"template_id,omitempty"`
	InstanceID                  string `json:"instance_id,omitempty"`
	TemplateCredsSetupCompleted *bool  `json:"template_creds_setup_completed,omitempty"`
}

// ProjectRef is the project of a share outside this connection's project allow-list: its identifier only.
type ProjectRef struct {
	ID string `json:"id"`
}

// SharedWorkflow is one share of a workflow. Project is a ProjectSummary, or a ProjectRef when the project
// is not reachable: outside this connection's project allow-list, or excluded.
type SharedWorkflow struct {
	Role       string `json:"role,omitempty"`
	WorkflowID string `json:"workflow_id,omitempty"`
	ProjectID  string `json:"project_id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Project    any    `json:"project,omitempty"`
}

// PublishEvent is one entry of a version's publish history.
type PublishEvent struct {
	ID         json.Number `json:"id,omitempty"`
	WorkflowID string      `json:"workflow_id,omitempty"`
	VersionID  string      `json:"version_id,omitempty"`
	Event      string      `json:"event,omitempty"`
	UserID     string      `json:"user_id,omitempty"`
	CreatedAt  string      `json:"created_at,omitempty"`
}

// ActiveVersion is the published version of a workflow. Authors, name, and description are untrusted data;
// authors names other people.
type ActiveVersion struct {
	VersionID              string          `json:"version_id,omitempty"`
	WorkflowID             string          `json:"workflow_id,omitempty"`
	Nodes                  []NodeRead      `json:"nodes"`
	Connections            json.RawMessage `json:"connections"`
	NodeGroups             []NodeGroup     `json:"node_groups,omitempty"`
	Authors                string          `json:"authors,omitempty"`
	Name                   string          `json:"name,omitempty"`
	Description            string          `json:"description,omitempty"`
	Autosaved              *bool           `json:"autosaved,omitempty"`
	CreatedAt              string          `json:"created_at,omitempty"`
	UpdatedAt              string          `json:"updated_at,omitempty"`
	WorkflowPublishHistory []PublishEvent  `json:"workflow_publish_history,omitempty"`
}

// WorkflowRead is the read view of one workflow, the same for workflows.get and workflows.list. It extends
// the summary; Tags and Nodes shadow the summary's and detail's narrower members.
type WorkflowRead struct {
	WorkflowSummary
	Description      string            `json:"description,omitempty"`
	ActiveVersionID  string            `json:"active_version_id,omitempty"`
	VersionID        string            `json:"version_id,omitempty"`
	VersionCounter   json.Number       `json:"version_counter,omitempty"`
	SourceWorkflowID string            `json:"source_workflow_id,omitempty"`
	Tags             []ReadTag         `json:"tags"`
	Nodes            []NodeRead        `json:"nodes"`
	Connections      json.RawMessage   `json:"connections"`
	NodeGroups       []NodeGroup       `json:"node_groups,omitempty"`
	Settings         *WorkflowSettings `json:"settings,omitempty"`
	Meta             *WorkflowMeta     `json:"meta,omitempty"`
	Shared           []SharedWorkflow  `json:"shared,omitempty"`
	ActiveVersion    *ActiveVersion    `json:"active_version,omitempty"`
}

func telemetryTagsOf(tags []telemetryTagJSON) []ProjectTelemetryTag {
	var out []ProjectTelemetryTag
	for _, tag := range tags {
		if len(out) == maxProjectTelemetryTags {
			break
		}
		if key := cappedText(tag.Key); key != "" {
			out = append(out, ProjectTelemetryTag{Key: key, Value: cappedText(tag.Value)})
		}
	}
	return out
}

// nodeReadOf extends nodeOf, the one conversion that masks parameters, so no node output bypasses it.
func nodeReadOf(n nodeJSON) NodeRead {
	out := NodeRead{Node: nodeOf(n), NotesInFlow: n.NotesInFlow, Notes: cappedLongText(n.Notes),
		ExecuteOnce: n.ExecuteOnce, AlwaysOutputData: n.AlwaysOutputData, RetryOnFail: n.RetryOnFail,
		MaxTries: n.MaxTries, WaitBetweenTries: n.WaitBetweenTries, OnError: cappedText(n.OnError),
		CreatedAt: bounded(n.CreatedAt), UpdatedAt: bounded(n.UpdatedAt)}
	if n.CustomTelemetryTags != nil {
		if tags := telemetryTagsOf(n.CustomTelemetryTags.Tag); len(tags) > 0 {
			out.CustomTelemetryTags = &NodeTelemetryTags{Tag: tags}
		}
	}
	return out
}

func nodesReadOf(nodes []nodeJSON) []NodeRead {
	out := make([]NodeRead, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeReadOf(n))
	}
	return out
}

func connectionsOf(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}

func nodeGroupsOf(groups []nodeGroupJSON) []NodeGroup {
	var out []NodeGroup
	for _, g := range groups {
		if len(out) == maxNodeGroups {
			break
		}
		ids := make([]string, 0, len(g.NodeIDs))
		for _, id := range g.NodeIDs {
			if len(ids) == maxGroupNodeIDs {
				break
			}
			ids = append(ids, bounded(id))
		}
		out = append(out, NodeGroup{ID: bounded(g.ID), Name: cappedText(g.Name),
			Description: cappedText(g.Description), NodeIDs: ids})
	}
	return out
}

// saveFlagOf keeps a boolean or text setting and drops any other JSON type.
func saveFlagOf(raw json.RawMessage) any {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		if text := cappedText(typed); text != "" {
			return text
		}
	}
	return nil
}

func settingsOf(s *workflowSettingsJSON) *WorkflowSettings {
	if s == nil {
		return nil
	}
	out := WorkflowSettings{SaveExecutionProgress: saveFlagOf(s.SaveExecutionProgress),
		SaveManualExecutions:     saveFlagOf(s.SaveManualExecutions),
		SaveDataErrorExecution:   cappedText(s.SaveDataErrorExecution),
		SaveDataSuccessExecution: cappedText(s.SaveDataSuccessExecution), ExecutionTimeout: s.ExecutionTimeout,
		ErrorWorkflow: cappedText(s.ErrorWorkflow), Timezone: cappedText(s.Timezone),
		ExecutionOrder: cappedText(s.ExecutionOrder), BinaryMode: cappedText(s.BinaryMode),
		CallerPolicy: cappedText(s.CallerPolicy), CallerIDs: cappedLongText(s.CallerIDs),
		TimeSavedMode: cappedText(s.TimeSavedMode), TimeSavedPerExecution: s.TimeSavedPerExecution,
		RedactionPolicy: cappedText(s.RedactionPolicy), AvailableInMCP: s.AvailableInMCP,
		CustomTelemetryTags:  telemetryTagsOf(s.CustomTelemetryTags),
		CredentialResolverID: cappedText(s.CredentialResolverID)}
	if reflect.ValueOf(out).IsZero() {
		return nil
	}
	return &out
}

func metaOf(m *workflowMetaJSON) *WorkflowMeta {
	if m == nil {
		return nil
	}
	out := WorkflowMeta{OnboardingID: cappedText(m.OnboardingID), TemplateID: cappedText(m.TemplateID),
		InstanceID: cappedText(m.InstanceID), TemplateCredsSetupCompleted: m.TemplateCredsSetupCompleted}
	if out == (WorkflowMeta{}) {
		return nil
	}
	return &out
}

// sharedOf maps the shares of a workflow. The project of a share that is not reachable (outside this
// connection's project allow-list, or excluded) is reduced to its identifier; a share whose project cannot be identified reports none.
func sharedOf(shared []sharedJSON, s scope) []SharedWorkflow {
	var out []SharedWorkflow
	for _, entry := range shared {
		if len(out) == maxReadShared {
			break
		}
		item := SharedWorkflow{Role: cappedText(entry.Role), WorkflowID: bounded(entry.WorkflowID),
			ProjectID: bounded(entry.ProjectID), CreatedAt: bounded(entry.CreatedAt), UpdatedAt: bounded(entry.UpdatedAt)}
		if entry.Project != nil {
			id := entry.Project.ID
			if id == "" {
				id = entry.ProjectID
			}
			switch {
			case id == "":
			case s.restrictsProjects() && !s.reachesProject(id):
				item.Project = ProjectRef{ID: bounded(id)}
			default:
				item.Project = summarizeProject(*entry.Project)
			}
		}
		if !reflect.ValueOf(item).IsZero() {
			out = append(out, item)
		}
	}
	return out
}

func activeVersionOf(v *activeVersionJSON) *ActiveVersion {
	if v == nil {
		return nil
	}
	out := &ActiveVersion{VersionID: bounded(v.VersionID), WorkflowID: bounded(v.WorkflowID),
		Nodes: nodesReadOf(v.Nodes), Connections: connectionsOf(v.Connections), NodeGroups: nodeGroupsOf(v.NodeGroups),
		Authors: cappedText(v.Authors), Name: cappedText(v.Name), Description: cappedText(v.Description),
		Autosaved: v.Autosaved, CreatedAt: bounded(v.CreatedAt), UpdatedAt: bounded(v.UpdatedAt)}
	for _, h := range v.WorkflowPublishHistory {
		if len(out.WorkflowPublishHistory) == maxPublishHistory {
			break
		}
		out.WorkflowPublishHistory = append(out.WorkflowPublishHistory, PublishEvent{ID: h.ID,
			WorkflowID: bounded(h.WorkflowID), VersionID: bounded(h.VersionID), Event: cappedText(h.Event),
			UserID: cappedText(h.UserID), CreatedAt: bounded(h.CreatedAt)})
	}
	return out
}

// readOf is the one conversion of a workflow n8n reported into its read view.
func readOf(w workflowJSON, s scope) WorkflowRead {
	tags := make([]ReadTag, 0, len(w.Tags))
	for _, t := range w.Tags {
		tags = append(tags, ReadTag{ID: bounded(t.ID), Name: bounded(t.Name),
			CreatedAt: bounded(t.CreatedAt), UpdatedAt: bounded(t.UpdatedAt)})
	}
	return WorkflowRead{WorkflowSummary: summaryOf(w), Description: cappedText(w.Description),
		ActiveVersionID: bounded(w.ActiveVersionID), VersionID: bounded(w.VersionID),
		VersionCounter: w.VersionCounter, SourceWorkflowID: bounded(w.SourceWorkflowID), Tags: tags,
		Nodes: nodesReadOf(w.Nodes), Connections: connectionsOf(w.Connections), NodeGroups: nodeGroupsOf(w.NodeGroups),
		Settings: settingsOf(w.Settings), Meta: metaOf(w.Meta), Shared: sharedOf(w.Shared, s),
		ActiveVersion: activeVersionOf(w.ActiveVersion)}
}

var telemetryTagsSchema = `{"type":"array","maxItems":` + strconv.Itoa(maxProjectTelemetryTags) + `,"items":{"type":"object","properties":{` +
	`"key":{"type":"string"},"value":{"type":"string"}},"required":["key","value"],"additionalProperties":false}}`

var readNodeSchema = `{"type":"object","properties":{` + nodeProperties + `,` +
	`"notes_in_flow":{"type":"boolean"},"notes":{"type":"string"},"execute_once":{"type":"boolean"},` +
	`"always_output_data":{"type":"boolean"},"retry_on_fail":{"type":"boolean"},` +
	`"max_tries":{"type":"number"},"wait_between_tries":{"type":"number"},"on_error":{"type":"string"},` +
	`"custom_telemetry_tags":{"type":"object","properties":{"tag":` + telemetryTagsSchema + `},` +
	`"required":["tag"],"additionalProperties":false},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["id","name","type","disabled"],"additionalProperties":false}`

var readTagSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["id","name"],"additionalProperties":false}`

var nodeGroupSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"node_ids":{"type":"array","items":{"type":"string"}}},` +
	`"required":["id","name","node_ids"],"additionalProperties":false}`

var saveFlagSchema = `{"anyOf":[{"type":"boolean"},{"type":"string"}]}`

var workflowSettingsSchema = `{"type":"object","properties":{` +
	`"save_execution_progress":` + saveFlagSchema + `,"save_manual_executions":` + saveFlagSchema + `,` +
	`"save_data_error_execution":{"type":"string"},"save_data_success_execution":{"type":"string"},` +
	`"execution_timeout":{"type":"number"},"error_workflow":{"type":"string"},"timezone":{"type":"string"},` +
	`"execution_order":{"type":"string"},"binary_mode":{"type":"string"},"caller_policy":{"type":"string"},` +
	`"caller_ids":{"type":"string"},"time_saved_mode":{"type":"string"},` +
	`"time_saved_per_execution":{"type":"number"},"redaction_policy":{"type":"string"},` +
	`"available_in_mcp":{"type":"boolean"},"custom_telemetry_tags":` + telemetryTagsSchema + `,` +
	`"credential_resolver_id":{"type":"string"}},"additionalProperties":false}`

var workflowMetaSchema = `{"type":"object","properties":{"onboarding_id":{"type":"string"},` +
	`"template_id":{"type":"string"},"instance_id":{"type":"string"},` +
	`"template_creds_setup_completed":{"type":"boolean"}},"additionalProperties":false}`

// sharedProjectSchema is the project schema with only the identifier required, since a project outside the
// project allow-list is reported by its identifier alone.
var sharedProjectSchema = strings.Replace(projectSummarySchema,
	`"required":["id","name","type","created_at","updated_at"]`, `"required":["id"]`, 1)

var sharedWorkflowSchema = `{"type":"object","properties":{"role":{"type":"string"},` +
	`"workflow_id":{"type":"string"},"project_id":{"type":"string"},"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"project":` + sharedProjectSchema + `},"additionalProperties":false}`

var publishEventSchema = `{"type":"object","properties":{"id":{"type":"integer"},"workflow_id":{"type":"string"},` +
	`"version_id":{"type":"string"},"event":{"type":"string"},"user_id":{"type":"string"},` +
	`"created_at":{"type":"string"}},"additionalProperties":false}`

var activeVersionSchema = `{"type":"object","properties":{"version_id":{"type":"string"},` +
	`"workflow_id":{"type":"string"},"nodes":{"type":"array","items":` + readNodeSchema + `},` +
	`"connections":{"type":"object"},"node_groups":{"type":"array","items":` + nodeGroupSchema + `},` +
	`"authors":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},` +
	`"autosaved":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"workflow_publish_history":{"type":"array","items":` + publishEventSchema + `}},` +
	`"required":["nodes","connections"],"additionalProperties":false}`

var workflowReadSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"active":{"type":"boolean"},` +
	`"is_archived":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"trigger_count":{"type":"integer"},"node_count":{"type":"integer"},` +
	`"tags":{"type":"array","items":` + readTagSchema + `},` +
	`"project_ids":{"type":"array","items":{"type":"string"}},` +
	`"description":{"type":"string"},"active_version_id":{"type":"string"},"version_id":{"type":"string"},` +
	`"version_counter":{"type":"integer"},"source_workflow_id":{"type":"string"},` +
	`"nodes":{"type":"array","items":` + readNodeSchema + `},"connections":{"type":"object"},` +
	`"node_groups":{"type":"array","items":` + nodeGroupSchema + `},` +
	`"settings":` + workflowSettingsSchema + `,"meta":` + workflowMetaSchema + `,` +
	`"shared":{"type":"array","items":` + sharedWorkflowSchema + `},"active_version":` + activeVersionSchema + `},` +
	`"required":["id","name","active","is_archived","created_at","updated_at","trigger_count","node_count",` +
	`"tags","project_ids","nodes","connections"],"additionalProperties":false}`

var workflowReadFields = append(append([]capability.Field{}, workflowDetailFields...),
	capability.Field{Name: "description", Description: "Description, untrusted data, capped; also the description " +
		"of a node group or of the active version"},
	capability.Field{Name: "active_version_id", Description: "Identifier of the active (published) version"},
	capability.Field{Name: "version_id", Description: "Identifier of the current version; also the version " +
		"identifier of the active version"},
	capability.Field{Name: "version_counter", Description: "Counter of the workflow's versions"},
	capability.Field{Name: "source_workflow_id", Description: "Identifier of the workflow this one was copied from"},
	capability.Field{Name: "node_groups", Description: "Visual groupings of nodes (id, name, description, " +
		"node_ids), untrusted data, capped to " + strconv.Itoa(maxNodeGroups) + " groups"},
	capability.Field{Name: "settings", Description: "Execution and behaviour settings of the workflow, typed " +
		"member by member; texts are untrusted data. staticData and pinData are never read"},
	capability.Field{Name: "meta", Description: "Workflow metadata such as template information, untrusted data"},
	capability.Field{Name: "shared", Description: "Shares of the workflow (role, workflow_id, project_id, " +
		"timestamps) and the project of each, capped to " + strconv.Itoa(maxReadShared) + " entries; a project " +
		"outside this connection's project allow-list is reported by its id only"},
	capability.Field{Name: "active_version", Description: "The active (published) version with its nodes, " +
		"connections, node groups, authors, and publish history; its nodes follow the same withholding rules as " +
		"nodes; absent when the workflow has none; texts are untrusted data"},
	// Members of the settings object.
	capability.Field{Name: "save_execution_progress", Description: "Whether to save progress; true, false, or DEFAULT"},
	capability.Field{Name: "save_manual_executions", Description: "Whether to save manual executions; true, false, or DEFAULT"},
	capability.Field{Name: "save_data_error_execution", Description: "Data saved for failed executions: DEFAULT, all, or none"},
	capability.Field{Name: "save_data_success_execution", Description: "Data saved for successful executions: DEFAULT, all, or none"},
	capability.Field{Name: "execution_timeout", Description: "Execution timeout in seconds"},
	capability.Field{Name: "error_workflow", Description: "Identifier of the workflow that handles this workflow's errors"},
	capability.Field{Name: "timezone", Description: "Time zone of the workflow"},
	capability.Field{Name: "execution_order", Description: "Execution order, such as v1"},
	capability.Field{Name: "binary_mode", Description: "How binary data is resolved: separate or combined"},
	capability.Field{Name: "caller_policy", Description: "Which workflows may call this one: any, none, " +
		"workflowsFromAList, or workflowsFromSameOwner"},
	capability.Field{Name: "caller_ids", Description: "Comma-separated workflow ids allowed to call this one, capped"},
	capability.Field{Name: "time_saved_mode", Description: "How time saved per execution is calculated: fixed or dynamic"},
	capability.Field{Name: "time_saved_per_execution", Description: "Estimated minutes saved per execution"},
	capability.Field{Name: "redaction_policy", Description: "Execution data redaction: none, non-manual, manual-only, or all"},
	capability.Field{Name: "available_in_mcp", Description: "Whether the workflow is reachable over the Model Context Protocol"},
	capability.Field{Name: "custom_telemetry_tags", Description: "Custom telemetry tags (key, value) of the " +
		"workflow or, as an object with a tag list, of a node; untrusted data, capped to " +
		strconv.Itoa(maxProjectTelemetryTags) + " entries"},
	capability.Field{Name: "credential_resolver_id", Description: "Identifier of the credential resolver of the workflow"},
	// Members of the meta object.
	capability.Field{Name: "onboarding_id", Description: "Onboarding identifier"},
	capability.Field{Name: "template_id", Description: "Identifier of the template the workflow came from"},
	capability.Field{Name: "instance_id", Description: "Identifier of the instance the workflow came from"},
	capability.Field{Name: "template_creds_setup_completed", Description: "Whether the template's credential setup is completed"},
	// Members of a share, of the active version, and of its publish history.
	capability.Field{Name: "role", Description: "Role of the share, such as workflow:owner"},
	capability.Field{Name: "workflow_id", Description: "Identifier of the workflow a share or a version belongs to"},
	capability.Field{Name: "project_id", Description: "Identifier of the project a workflow is shared into"},
	capability.Field{Name: "project", Description: "Project of a share, as projects.list reports it, or its " +
		"id only when it lies outside this connection's project allow-list"},
	capability.Field{Name: "authors", Description: "Authors of the active version, personal data, untrusted"},
	capability.Field{Name: "node_ids", Description: "Identifiers of the nodes of a node group, capped"},
	capability.Field{Name: "autosaved", Description: "True when the version was saved automatically"},
	capability.Field{Name: "workflow_publish_history", Description: "Publish events (id, workflow_id, " +
		"version_id, event, user_id, created_at) of the active version, capped to " +
		strconv.Itoa(maxPublishHistory) + " entries; user_id names another person"},
	// Members of a node.
	capability.Field{Name: "type", Description: "Node type, such as n8n-nodes-base.httpRequest"},
	capability.Field{Name: "type_version", Description: "Version of the node type"},
	capability.Field{Name: "position", Description: "Canvas position of the node"},
	capability.Field{Name: "disabled", Description: "True when the node is disabled"},
	capability.Field{Name: "parameters", Description: "Parameters of the node, masked as described for nodes"},
	capability.Field{Name: "withheld_parameters", Description: "JSON pointers of withheld parameter values"},
	capability.Field{Name: "parameters_withheld", Description: "True when all parameters are withheld"},
	capability.Field{Name: "credentials", Description: "Credential references (id, name) of the node, by type"},
	capability.Field{Name: "notes_in_flow", Description: "True when the node's notes show on the canvas"},
	capability.Field{Name: "notes", Description: "Notes of the node, untrusted data, capped"},
	capability.Field{Name: "execute_once", Description: "True when the node runs once for all items"},
	capability.Field{Name: "always_output_data", Description: "True when the node outputs data even if empty"},
	capability.Field{Name: "retry_on_fail", Description: "True when the node retries on failure"},
	capability.Field{Name: "max_tries", Description: "Maximum tries when retrying"},
	capability.Field{Name: "wait_between_tries", Description: "Milliseconds to wait between tries"},
	capability.Field{Name: "on_error", Description: "Error handling of the node, such as stopWorkflow; the " +
		"deprecated continueOnFail is not reported"},
)
