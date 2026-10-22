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
	// maxRunServices cuts the caller chain of one provenance; the cut is flagged by services_truncated.
	maxRunServices  = 20
	maxRunIdentText = 256
)

var displayStatusValues = []string{"RUNNING", "WAITING", "SUCCESS", "WARNING", "ERROR"}

// looseText reads a value Make documents as a string or an integer (or has typed either way) as its exact
// text, so a large number never passes through a float. Any other JSON type reads as absent.
type looseText string

func (l *looseText) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) > 0 && raw[0] == '"':
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil
		}
		*l = looseText(text)
	case len(raw) > 0 && (raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9')):
		*l = looseText(raw)
	}
	return nil
}

type mcpJSON struct {
	AuthMethod     string    `json:"authMethod"`
	Client         string    `json:"client"`
	ConversationID string    `json:"conversationId"`
	Entrypoint     string    `json:"entrypoint"`
	Tool           string    `json:"tool"`
	RequestID      looseText `json:"requestId"`
}

type provenanceJSON struct {
	V             *int64   `json:"v"`
	Channel       string   `json:"channel"`
	Auth          string   `json:"auth"`
	Services      []string `json:"services"`
	TokenLabel    string   `json:"tokenLabel"`
	OAuthClientID string   `json:"oauthClientId"`
	MCP           *mcpJSON `json:"mcp"`
	Impersonated  *bool    `json:"impersonated"`
	RequestID     string   `json:"requestId"`
}

type changeFolderJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type changeJSON struct {
	Version    *int64            `json:"version"`
	Kind       string            `json:"kind"`
	Origin     string            `json:"origin"`
	Label      *labelJSON        `json:"label"`
	FromFolder *changeFolderJSON `json:"fromFolder"`
	ToFolder   *changeFolderJSON `json:"toFolder"`
}

type logDetailJSON struct {
	Author *struct {
		Name  string `json:"name"`
		Staff *bool  `json:"staff"`
	} `json:"author"`
	Provenance json.RawMessage `json:"provenance"`
	Change     json.RawMessage `json:"change"`
}

// RunMCP names the MCP caller of a run.
type RunMCP struct {
	AuthMethod     string `json:"auth_method,omitempty"`
	Client         string `json:"client,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Entrypoint     string `json:"entrypoint,omitempty"`
	Tool           string `json:"tool,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
}

// RunProvenance is how and through whom a run was started. Its members are identifiers and labels, never
// credentials.
type RunProvenance struct {
	V                 int64    `json:"v,omitempty"`
	Channel           string   `json:"channel,omitempty"`
	Auth              string   `json:"auth,omitempty"`
	Services          []string `json:"services,omitempty"`
	ServicesTruncated bool     `json:"services_truncated,omitempty"`
	TokenLabel        string   `json:"token_label,omitempty"`
	OAuthClientID     string   `json:"oauth_client_id,omitempty"`
	MCP               *RunMCP  `json:"mcp,omitempty"`
	Impersonated      *bool    `json:"impersonated,omitempty"`
	RequestID         string   `json:"request_id,omitempty"`
}

// RunAuthor is the team member a history entry is attributed to.
type RunAuthor struct {
	Name  string `json:"name,omitempty"`
	Staff *bool  `json:"staff,omitempty"`
}

// RunChangeLabel is the label of a label-assigned or label-unassigned event.
type RunChangeLabel struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
}

// RunChangeFolder is one end of a folder move.
type RunChangeFolder struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

// RunChange is the versioned change of a history entry. Only the shapes Make documents are read; any other
// shape keeps only version, kind, and origin.
type RunChange struct {
	Version    *int64           `json:"version,omitempty"`
	Kind       string           `json:"kind,omitempty"`
	Origin     string           `json:"origin,omitempty"`
	Label      *RunChangeLabel  `json:"label,omitempty"`
	FromFolder *RunChangeFolder `json:"from_folder,omitempty"`
	ToFolder   *RunChangeFolder `json:"to_folder,omitempty"`
}

// RunLogDetail is the documented part of a log entry's detail object.
type RunLogDetail struct {
	Author     *RunAuthor     `json:"author,omitempty"`
	Change     *RunChange     `json:"change,omitempty"`
	Provenance *RunProvenance `json:"provenance,omitempty"`
}

// safeIdent cleans a provenance identifier and drops one that carries this client's own API token, so a
// label someone typed a token into never reaches a result.
func (c *Client) safeIdent(value string) string {
	value = cleanText(value, maxRunIdentText)
	if c.token != "" && strings.Contains(value, c.token) {
		return ""
	}
	return value
}

func (c *Client) provenanceOf(raw json.RawMessage) *RunProvenance {
	if len(raw) == 0 {
		return nil
	}
	var j provenanceJSON
	// A member of an unexpected type leaves only that member empty.
	_ = json.Unmarshal(raw, &j)
	p := &RunProvenance{Channel: cleanText(j.Channel, maxShortText), Auth: cleanText(j.Auth, maxShortText),
		TokenLabel: c.safeIdent(j.TokenLabel), OAuthClientID: c.safeIdent(j.OAuthClientID),
		Impersonated: j.Impersonated, RequestID: c.safeIdent(j.RequestID)}
	if j.V != nil {
		p.V = *j.V
	}
	services, cut := capList(j.Services, maxRunServices)
	for _, s := range services {
		if s = c.safeIdent(s); s != "" {
			p.Services = append(p.Services, s)
		}
	}
	p.ServicesTruncated = cut
	if m := j.MCP; m != nil {
		p.MCP = &RunMCP{AuthMethod: c.safeIdent(m.AuthMethod), Client: c.safeIdent(m.Client),
			ConversationID: c.safeIdent(m.ConversationID), Entrypoint: c.safeIdent(m.Entrypoint),
			Tool: c.safeIdent(m.Tool), RequestID: c.safeIdent(string(m.RequestID))}
		if *p.MCP == (RunMCP{}) {
			p.MCP = nil
		}
	}
	if p.V == 0 && p.Channel == "" && p.Auth == "" && len(p.Services) == 0 && !p.ServicesTruncated &&
		p.TokenLabel == "" && p.OAuthClientID == "" && p.MCP == nil && p.Impersonated == nil && p.RequestID == "" {
		return nil
	}
	return p
}

func folderOf(f *changeFolderJSON) *RunChangeFolder {
	if f == nil {
		return nil
	}
	return &RunChangeFolder{ID: f.ID, Name: cleanText(f.Name, maxNameText), Path: cleanText(f.Path, maxPathText)}
}

func changeOf(raw json.RawMessage) *RunChange {
	if len(raw) == 0 {
		return nil
	}
	var j changeJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil
	}
	change := &RunChange{Version: j.Version, Kind: cleanText(j.Kind, maxShortText),
		Origin: cleanText(j.Origin, maxShortText)}
	switch j.Kind {
	case "label-assigned", "label-unassigned":
		if j.Label != nil {
			change.Label = &RunChangeLabel{ID: j.Label.ID, Name: cleanText(j.Label.Name, maxNameText)}
		}
	case "folder-moved":
		change.FromFolder = folderOf(j.FromFolder)
		change.ToFolder = folderOf(j.ToFolder)
	}
	if change.Version == nil && change.Kind == "" && change.Origin == "" && change.Label == nil &&
		change.FromFolder == nil && change.ToFolder == nil {
		return nil
	}
	return change
}

// logDetailOf reads the documented members of a log entry's detail. A detail of another type, or a member
// of an unexpected type, leaves that part out.
func (c *Client) logDetailOf(raw json.RawMessage) *RunLogDetail {
	if len(raw) == 0 {
		return nil
	}
	var j logDetailJSON
	_ = json.Unmarshal(raw, &j)
	d := &RunLogDetail{Change: changeOf(j.Change), Provenance: c.provenanceOf(j.Provenance)}
	if a := j.Author; a != nil {
		if author := (RunAuthor{Name: cleanText(a.Name, maxNameText), Staff: a.Staff}); author != (RunAuthor{}) {
			d.Author = &author
		}
	}
	if d.Author == nil && d.Change == nil && d.Provenance == nil {
		return nil
	}
	return d
}

// RunFilter holds the optional filters of runs.list. Zero values mean "not set" except where a pointer is
// used because 0 is a valid bound.
type RunFilter struct {
	Status         int    `json:"status"`
	FromMS         int64  `json:"from_ms"`
	ToMS           int64  `json:"to_ms"`
	DisplayStatus  string `json:"display_status"`
	DurationFrom   *int64 `json:"duration_from"`
	DurationTo     *int64 `json:"duration_to"`
	OperationsFrom *int64 `json:"operations_from"`
	OperationsTo   *int64 `json:"operations_to"`
	TransferFrom   *int64 `json:"transfer_from"`
	TransferTo     *int64 `json:"transfer_to"`
	CreditsFrom    *int64 `json:"credits_from"`
	CreditsTo      *int64 `json:"credits_to"`
	ExecutionName  string `json:"execution_name"`
	ShowCheckRuns  *bool  `json:"show_check_runs"`
	SortDir        string `json:"sort_dir"`
}

type runRange struct {
	name     string
	from, to *int64
	fromKey  string
	toKey    string
}

func (f RunFilter) ranges() []runRange {
	return []runRange{
		{"duration", f.DurationFrom, f.DurationTo, "durationFrom", "durationTo"},
		{"operations", f.OperationsFrom, f.OperationsTo, "operationsFrom", "operationsTo"},
		{"transfer", f.TransferFrom, f.TransferTo, "transferFrom", "transferTo"},
		{"credits", f.CreditsFrom, f.CreditsTo, "creditsFrom", "creditsTo"},
	}
}

// validate rejects an unusable filter before any request or secret resolution; it is also a second line for
// callers of ListRuns that bypass the input schema.
func (f RunFilter) validate() error {
	if f.Status != 0 && f.DisplayStatus != "" {
		return invalidRequest("status and display_status exclude each other")
	}
	if f.Status != 0 {
		valid := false
		for _, s := range statusValues {
			valid = valid || s == f.Status
		}
		if !valid {
			return invalidRequest("status must be 1, 2, or 3")
		}
	}
	if f.FromMS < 0 || f.ToMS < 0 {
		return invalidRequest("from_ms and to_ms must be 0 or more")
	}
	if f.DisplayStatus != "" {
		valid := false
		for _, s := range displayStatusValues {
			valid = valid || s == f.DisplayStatus
		}
		if !valid {
			return invalidRequest("display_status must be one of " + strings.Join(displayStatusValues, ", "))
		}
	}
	for _, r := range f.ranges() {
		for _, bound := range []*int64{r.from, r.to} {
			if bound != nil && *bound < 0 {
				return invalidRequest(r.name + "_from and " + r.name + "_to must be 0 or more")
			}
		}
		if r.from != nil && r.to != nil && *r.from > *r.to {
			return invalidRequest(r.name + "_from must not exceed " + r.name + "_to")
		}
	}
	if f.ExecutionName != "" {
		if n := utf8.RuneCountInString(f.ExecutionName); n > maxTextFilter || strings.TrimSpace(f.ExecutionName) == "" {
			return invalidRequest("execution_name must be 1 to " + strconv.Itoa(maxTextFilter) + " characters and not blank")
		}
	}
	if f.SortDir != "" && f.SortDir != "asc" && f.SortDir != "desc" {
		return invalidRequest("sort_dir must be asc or desc")
	}
	return nil
}

func (f RunFilter) apply(query url.Values) {
	if f.Status != 0 {
		query.Set("status", strconv.Itoa(f.Status))
	}
	if f.FromMS > 0 {
		query.Set("from", strconv.FormatInt(f.FromMS, 10))
	}
	if f.ToMS > 0 {
		query.Set("to", strconv.FormatInt(f.ToMS, 10))
	}
	if f.DisplayStatus != "" {
		query.Set("displayStatus", f.DisplayStatus)
	}
	for _, r := range f.ranges() {
		if r.from != nil {
			query.Set(r.fromKey, strconv.FormatInt(*r.from, 10))
		}
		if r.to != nil {
			query.Set(r.toKey, strconv.FormatInt(*r.to, 10))
		}
	}
	if f.ExecutionName != "" {
		query.Set("executionName", f.ExecutionName)
	}
	if f.ShowCheckRuns != nil {
		query.Set("showCheckRuns", strconv.FormatBool(*f.ShowCheckRuns))
	}
	if f.SortDir != "" {
		query.Set("pg[sortDir]", f.SortDir)
	}
}

const runProvenanceSchema = `{"type":"object","properties":{"v":{"type":"integer"},"channel":{"type":"string"},` +
	`"auth":{"type":"string"},"services":{"type":"array","items":{"type":"string"}},` +
	`"services_truncated":{"type":"boolean"},"token_label":{"type":"string"},"oauth_client_id":{"type":"string"},` +
	`"mcp":{"type":"object","properties":{"auth_method":{"type":"string"},"client":{"type":"string"},` +
	`"conversation_id":{"type":"string"},"entrypoint":{"type":"string"},"tool":{"type":"string"},` +
	`"request_id":{"type":"string"}},"additionalProperties":false},"impersonated":{"type":"boolean"},` +
	`"request_id":{"type":"string"}},"additionalProperties":false}`

const runFolderSchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},` +
	`"path":{"type":"string"}},"required":["id"],"additionalProperties":false}`

const runDetailObjectSchema = `{"type":"object","properties":{` +
	`"author":{"type":"object","properties":{"name":{"type":"string"},"staff":{"type":"boolean"}},` +
	`"additionalProperties":false},` +
	`"change":{"type":"object","properties":{"version":{"type":"integer"},"kind":{"type":"string"},` +
	`"origin":{"type":"string"},"label":{"type":"object","properties":{"id":{"type":"integer"},` +
	`"name":{"type":"string"}},"required":["id"],"additionalProperties":false},` +
	`"from_folder":` + runFolderSchema + `,"to_folder":` + runFolderSchema + `},"additionalProperties":false},` +
	`"provenance":` + runProvenanceSchema + `},"additionalProperties":false}`

// runExtraSchema is the schema members added by the full response width, spliced into a run schema.
const runExtraSchema = `"imt_id":{"type":"string"},"instant":{"type":"boolean"},"display_status":{"type":"string"},` +
	`"resume_at":{"type":"string"},"interrupt_module_name":{"type":"string"},` +
	`"provenance":` + runProvenanceSchema + `,"detail":` + runDetailObjectSchema

var runExtraFields = []capability.Field{
	{Name: "imt_id", Description: "Make's internal execution identifier, untrusted data"},
	{Name: "instant", Description: "Whether the run was started by an instant trigger"},
	{Name: "display_status", Description: "RUNNING, WAITING (a timed, parked run), SUCCESS, WARNING, or ERROR; " +
		"absent on history rows that are no execution"},
	{Name: "resume_at", Description: "When a parked, time-based run resumes, as Make reports it"},
	{Name: "interrupt_module_name", Description: "Name of the module the run is paused at, untrusted data"},
	{Name: "provenance", Description: "How and through whom the run was started; identifiers only, untrusted data"},
	{Name: "v", Description: "Version of the provenance form"},
	{Name: "channel", Description: "Where the run came from, for example ui, api_token, oauth_app, mcp, " +
		"service, external, or unknown; Make may add values"},
	{Name: "auth", Description: "How the caller authenticated; Make may add values"},
	{Name: "services", Description: "Services the run passed through, original caller first, cut at " +
		strconv.Itoa(maxRunServices)},
	{Name: "services_truncated", Description: "True when more services were reported than services holds"},
	{Name: "token_label", Description: "Label of the API token that started the run"},
	{Name: "oauth_client_id", Description: "Identifier of the OAuth app that started the run"},
	{Name: "mcp", Description: "MCP caller of the run: auth_method, client, conversation_id, entrypoint, tool, " +
		"request_id"},
	{Name: "impersonated", Description: "True when a Make admin acted as the user"},
	{Name: "request_id", Description: "Identifier of the request that started the run"},
	{Name: "detail", Description: "The documented part of the log entry's detail, untrusted data"},
	{Name: "author", Description: "Team member a history entry is attributed to: name, staff"},
	{Name: "change", Description: "Versioned change of a history entry: version, kind, origin; plus label (id, " +
		"name) for label events or from_folder and to_folder (id, name, path) for folder moves; any other " +
		"kind carries only version, kind, and origin"},
}
