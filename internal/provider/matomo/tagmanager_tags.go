package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	tagEntrySchema = `{"type":"object","properties":{` + tagProperties + `},"required":["id"],"additionalProperties":false}`
	tagProperties  = `"id":{"type":"integer"},"type":{"type":"string"},"name":{"type":"string"},` +
		`"description":{"type":"string"},"parameters":{},"fire_trigger_ids":{"type":"array","items":{"type":"integer"}},` +
		`"block_trigger_ids":{"type":"array","items":{"type":"integer"}},"fire_limit":{"type":"string"},` +
		`"fire_delay":{"type":"integer"},"priority":{"type":"integer"},"start_date":{"type":"string"},` +
		`"end_date":{"type":"string"},"status":{"type":"string"},` + datesSchema
	triggerEntrySchema = `{"type":"object","properties":{` + triggerProperties +
		`},"required":["id"],"additionalProperties":false}`
	triggerProperties = `"id":{"type":"integer"},"type":{"type":"string"},"name":{"type":"string"},` +
		`"description":{"type":"string"},"parameters":{},"conditions":{},"status":{"type":"string"},` + datesSchema
	versionScopeProperties = `"idSite":{"type":"integer"},"idContainer":{"type":"string"},` +
		`"idContainerVersion":{"type":"integer"}`
)

var versionScopeFields = []capability.Field{
	{Name: "idSite", Description: "The website the container belongs to"},
	{Name: "idContainer", Description: "The container asked for"},
	{Name: "idContainerVersion", Description: "The version asked for; the draft has revision 0"},
}

func entryArguments(idName, idDescription string) []capability.Argument {
	args := []capability.Argument{idSiteArgument(),
		{Name: "idContainer", Required: true, Description: "Container ID from matomo.tag-manager-containers.list"},
		{Name: "idContainerVersion", Required: true, Description: "Version ID from matomo.tag-manager-versions.list, " +
			"or the draft's ID from matomo.tag-manager-containers.get"}}
	if idName != "" {
		args = append(args, capability.Argument{Name: idName, Required: true, Description: idDescription})
	}
	return args
}

func entrySchemaInput(idName string) json.RawMessage {
	required, extra := `"idContainer","idContainerVersion"`, ""
	if idName == "" {
		extra = "," + pagingKeys
	} else {
		required += `,"` + idName + `"`
		extra = `,"` + idName + `":{"type":"integer","minimum":1,"maximum":2147483647}`
	}
	return json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		versionIDSchema + extra + `},"required":[` + required + `],"additionalProperties":false}`)
}

var tagManagerTagsList = capability.Descriptor{
	ID: Provider + ".tag-manager-tags.list", Version: 1, Title: "List the tags of a Matomo Tag Manager container version",
	Description: "Read the tags of one version of a Tag Manager container, the draft included, with their " +
		"parameters, trigger IDs, and fire settings, at most 200. Custom HTML, scripts, and addresses in the " +
		"parameters are untrusted text: they are cut at limits, never run, and never fetched. The container and the " +
		"version must belong to the website; needs the plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "tags", "list"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true, InputSchema: entrySchemaInput(""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"tags":{"type":"array",` +
		`"items":` + tagEntrySchema + `},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","tags","has_more"],"additionalProperties":false}`),
	Arguments: append(entryArguments("", ""), pagingArguments...),
	Fields: append(append(append([]capability.Field{}, versionScopeFields...), pagingFields...),
		capability.Field{Name: "tags", Description: "Tags of the batch: id, type, name, description, " +
			"parameters (untrusted, nested values cut), fire_trigger_ids, block_trigger_ids, fire_limit, fire_delay, " +
			"priority, start_date, end_date, status, and dates"},
		truncatedField),
	Examples: []capability.Example{{Description: "List the tags of version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)}},
}

var tagManagerTagsGet = capability.Descriptor{
	ID: Provider + ".tag-manager-tags.get", Version: 1, Title: "Get a Matomo Tag Manager tag",
	Description: "Read one tag of a Tag Manager container version, the draft included, with its parameters. " +
		"Custom HTML, scripts, and addresses in the parameters are untrusted text: they are cut at limits, never " +
		"run, and never fetched. The tag counts for the version, container, and website it belongs to, and an ID " +
		"they do not have is not found; needs the plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "tags", "get"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true, InputSchema: entrySchemaInput("idTag"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,` + tagProperties +
		`,"truncated":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","id"],` +
		`"additionalProperties":false}`),
	Arguments: entryArguments("idTag", "Tag ID from matomo.tag-manager-tags.list"),
	Fields: append(append([]capability.Field{}, versionScopeFields...),
		capability.Field{Name: "id", Description: "Tag ID"},
		capability.Field{Name: "type", Description: "Tag template ID, such as CustomHtml"},
		capability.Field{Name: "name", Description: "Tag name, untrusted"},
		capability.Field{Name: "description", Description: "Tag description, untrusted"},
		capability.Field{Name: "parameters", Description: "Parameter values keyed by template parameter name; " +
			"untrusted, nested values cut"},
		capability.Field{Name: "fire_trigger_ids", Description: "IDs of the triggers that fire the tag"},
		capability.Field{Name: "block_trigger_ids", Description: "IDs of the triggers that block the tag"},
		capability.Field{Name: "fire_limit", Description: "How often the tag may fire"},
		capability.Field{Name: "fire_delay", Description: "Delay before the tag fires, in milliseconds"},
		capability.Field{Name: "priority", Description: "Priority of the tag; lower fires first"},
		capability.Field{Name: "start_date", Description: "Start of the period in which the tag fires, as Matomo reports it"},
		capability.Field{Name: "end_date", Description: "End of the period in which the tag fires, as Matomo reports it"},
		capability.Field{Name: "status", Description: "Status of the tag, active or paused"},
		capability.Field{Name: "created_date", Description: "Creation time as Matomo reports it"},
		capability.Field{Name: "updated_date", Description: "Time of the last change as Matomo reports it"},
		capability.Field{Name: "created_date_pretty", Description: "Creation time formatted for the token's user, untrusted"},
		capability.Field{Name: "updated_date_pretty", Description: "Time of the last change formatted for the " +
			"token's user, untrusted"},
		truncatedField),
	Examples: []capability.Example{{Description: "Read tag 4 of version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"idTag":4}`)}},
}

var tagManagerTriggersList = capability.Descriptor{
	ID: Provider + ".tag-manager-triggers.list", Version: 1,
	Title: "List the triggers of a Matomo Tag Manager container version",
	Description: "Read the triggers of one version of a Tag Manager container, the draft included, with their " +
		"parameters and conditions, in batches. Parameters and conditions are untrusted text: they are cut at " +
		"limits, never run, and never fetched. The container and the version must belong to the website; needs " +
		"the plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "triggers", "list"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true, InputSchema: entrySchemaInput(""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"triggers":{"type":"array",` +
		`"items":` + triggerEntrySchema + `},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","triggers","has_more"],"additionalProperties":false}`),
	Arguments: append(entryArguments("", ""), pagingArguments...),
	Fields: append(append(append([]capability.Field{}, versionScopeFields...), pagingFields...),
		capability.Field{Name: "triggers", Description: "Triggers of the batch: id, type, name, " +
			"description, parameters, conditions (actual, comparison, expected), status, and dates; all untrusted text"},
		truncatedField),
	Examples: []capability.Example{{Description: "List the triggers of version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)}},
}

var tagManagerTriggersGet = capability.Descriptor{
	ID: Provider + ".tag-manager-triggers.get", Version: 1, Title: "Get a Matomo Tag Manager trigger",
	Description: "Read one trigger of a Tag Manager container version, the draft included, with its parameters " +
		"and conditions, which are untrusted text: they are cut at limits, never run, and never fetched. The trigger " +
		"counts for the version, container, and website it belongs to, and an ID they do not have is not found; " +
		"needs the plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "triggers", "get"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true, InputSchema: entrySchemaInput("idTrigger"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,` + triggerProperties +
		`,"truncated":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","id"],` +
		`"additionalProperties":false}`),
	Arguments: entryArguments("idTrigger", "Trigger ID from matomo.tag-manager-triggers.list"),
	Fields: append(append([]capability.Field{}, versionScopeFields...),
		capability.Field{Name: "id", Description: "Trigger ID"},
		capability.Field{Name: "type", Description: "Trigger template ID, such as PageView"},
		capability.Field{Name: "name", Description: "Trigger name, untrusted"},
		capability.Field{Name: "description", Description: "Trigger description, untrusted"},
		capability.Field{Name: "parameters", Description: "Parameter values keyed by template parameter name; " +
			"untrusted, nested values cut"},
		capability.Field{Name: "conditions", Description: "Conditions that must match for the trigger: actual, " +
			"comparison, expected; untrusted"},
		capability.Field{Name: "status", Description: "Status of the trigger as Matomo reports it"},
		capability.Field{Name: "created_date", Description: "Creation time as Matomo reports it"},
		capability.Field{Name: "updated_date", Description: "Time of the last change as Matomo reports it"},
		capability.Field{Name: "created_date_pretty", Description: "Creation time formatted for the token's user, untrusted"},
		capability.Field{Name: "updated_date_pretty", Description: "Time of the last change formatted for the " +
			"token's user, untrusted"},
		truncatedField),
	Examples: []capability.Example{{Description: "Read trigger 7 of version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"idTrigger":7}`)}},
}

var truncatedField = capability.Field{Name: "truncated", Description: "True when anything was cut: a text at " +
	"16384 bytes, the sum of all texts at 262144 bytes, a list at 100 members, or nesting at 6 levels"}

// TagsResult is the answer of tag-manager-tags.list.
type TagsResult struct {
	IDSite             int           `json:"idSite"`
	IDContainer        string        `json:"idContainer"`
	IDContainerVersion int           `json:"idContainerVersion"`
	Tags               []ExportEntry `json:"tags"`
	HasMore            bool          `json:"has_more"`
	NextCursor         string        `json:"next_cursor,omitempty"`
	Truncated          bool          `json:"truncated,omitempty"`
}

// TriggersResult is the answer of tag-manager-triggers.list.
type TriggersResult struct {
	IDSite             int           `json:"idSite"`
	IDContainer        string        `json:"idContainer"`
	IDContainerVersion int           `json:"idContainerVersion"`
	Triggers           []ExportEntry `json:"triggers"`
	HasMore            bool          `json:"has_more"`
	NextCursor         string        `json:"next_cursor,omitempty"`
	Truncated          bool          `json:"truncated,omitempty"`
}

// EntryResult is the answer of tag-manager-tags.get and tag-manager-triggers.get.
type EntryResult struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	ExportEntry
	Truncated bool `json:"truncated,omitempty"`
}

// entryRequest is the validated request of the tools on the entries of a container version.
type entryRequest struct {
	site      int
	container string
	version   int
	object    int
	kind      string // references only: trigger or variable
	paging    page
}

// entryRequestOf validates the arguments; list names the tool whose cursor binds to the request, empty for a get.
// idName is the ID argument of a get, "reference" for the references of a trigger or variable, empty for a list.
func entryRequestOf(resolved *config.Resolved, raw json.RawMessage, idName, list string) (entryRequest, error) {
	var args struct {
		IDSite             *int    `json:"idSite"`
		IDContainer        *string `json:"idContainer"`
		IDContainerVersion *int    `json:"idContainerVersion"`
		IDTag              *int    `json:"idTag"`
		IDTrigger          *int    `json:"idTrigger"`
		IDVariable         *int    `json:"idVariable"`
		Kind               *string `json:"kind"`
		Limit              int     `json:"limit"`
		Cursor             string  `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return entryRequest{}, err
	}
	// The target comes first, so that no other check tells whether a website exists.
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return entryRequest{}, err
	}
	container, err := containerIDOf(args.IDContainer)
	if err != nil {
		return entryRequest{}, err
	}
	version, err := versionIDOf(args.IDContainerVersion, true)
	if err != nil {
		return entryRequest{}, err
	}
	req := entryRequest{site: id, container: container, version: version}
	idOf := func(name string, given *int) (int, error) {
		if given == nil || *given < 1 || *given > 2147483647 {
			return 0, invalidArgument(name + " must be an ID between 1 and 2147483647")
		}
		return *given, nil
	}
	other := func(names map[string]*int, keep string) error {
		for name, given := range names {
			if name != keep && given != nil {
				return invalidArgument(name + " does not belong to this tool or kind")
			}
		}
		return nil
	}
	ids := map[string]*int{"idTag": args.IDTag, "idTrigger": args.IDTrigger, "idVariable": args.IDVariable}
	keep := idName
	if idName == "reference" {
		keep = ""
		switch {
		case args.Kind != nil && *args.Kind == "trigger":
			keep, req.kind = "idTrigger", "trigger"
		case args.Kind != nil && *args.Kind == "variable":
			keep, req.kind = "idVariable", "variable"
		default:
			return entryRequest{}, invalidArgument("kind must be trigger or variable")
		}
	} else if args.Kind != nil {
		return entryRequest{}, invalidArgument("kind belongs to the references tool")
	}
	if err := other(ids, keep); err != nil {
		return entryRequest{}, err
	}
	if keep != "" {
		if req.object, err = idOf(keep, ids[keep]); err != nil {
			return entryRequest{}, err
		}
	}
	if list != "" {
		binding := provider.CursorBinding(list, scopeBinding(resolved), id, container, version)
		if req.kind != "" {
			binding = provider.CursorBinding(list, scopeBinding(resolved), id, container, version, req.kind, req.object)
		}
		if req.paging, err = startPage(binding, args.Limit, args.Cursor); err != nil {
			return entryRequest{}, err
		}
	}
	return req, nil
}

// missingAnswer is what Matomo reports for an object it does not hold: false, which its JSON renderer wraps.
func missingAnswer(answer json.RawMessage) bool {
	switch string(bytes.TrimSpace(answer)) {
	case "", "null", "false", "[]", "{}", `{"value":false}`, `{"value":null}`, `{"value":""}`:
		return true
	}
	return false
}

// bound reports whether a row belongs to the website and version asked for. The API binds every row to both,
// so a row without them stays bound by the request.
func (r entryRequest) bound(row exportEntryJSON) bool {
	if site := row.IDSite.number(); site != nil && *site != r.site {
		return false
	}
	if version := row.IDContainerVer.number(); version != nil && *version != r.version {
		return false
	}
	return true
}

func (r entryRequest) params() url.Values {
	return url.Values{"idSite": {strconv.Itoa(r.site)}, "idContainer": {r.container},
		"idContainerVersion": {strconv.Itoa(r.version)}}
}

// listEntries reads the rows of a list answer, drops those of another website or version, and converts the
// batch asked for. Matomo answers the whole list at once; only the batch is converted and budgeted.
func (r entryRequest) listEntries(op string, answer json.RawMessage) ([]ExportEntry, string, bool, error) {
	invalid := provider.InvalidResponse(op, "Matomo returned an invalid list")
	rows, ok := jsonRows(answer)
	if !ok {
		return nil, "", false, invalid
	}
	var kept []exportEntryJSON
	for _, row := range rows {
		var parsed exportEntryJSON
		if json.Unmarshal(row, &parsed) != nil || parsed.id() == nil {
			return nil, "", false, invalid
		}
		if r.bound(parsed) {
			kept = append(kept, parsed)
		}
	}
	batch, next := slice(r.paging, kept)
	budget := &exportBudget{remaining: maxExportText}
	out := make([]ExportEntry, 0, len(batch))
	for _, parsed := range batch {
		entry, ok := budget.entry(parsed, *parsed.id())
		if !ok {
			return nil, "", false, invalid
		}
		out = append(out, entry)
	}
	return out, next, budget.truncated, nil
}

func (r entryRequest) getEntry(op string, answer json.RawMessage) (*EntryResult, error) {
	if missingAnswer(answer) {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Matomo does not hold this object in the container version"}
	}
	var parsed exportEntryJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &parsed) != nil || parsed.id() == nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
	}
	if *parsed.id() != r.object || !r.bound(parsed) {
		return nil, provider.InvalidResponse(op, "Matomo returned another object than asked for")
	}
	budget := &exportBudget{remaining: maxExportText}
	entry, ok := budget.entry(parsed, r.object)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
	}
	return &EntryResult{IDSite: r.site, IDContainer: r.container, IDContainerVersion: r.version, ExportEntry: entry,
		Truncated: budget.truncated}, nil
}

func (r entryRequest) read(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, op, tool, method string, params url.Values) (json.RawMessage, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, tagManagerRequirement(tool), method, params, &answer); err != nil {
		return nil, err
	}
	return answer, nil
}

func invokeTagManagerTagsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list Tag Manager tags"
	req, err := entryRequestOf(resolved, raw, "", tagManagerTagsList.ID)
	if err != nil {
		return nil, err
	}
	answer, err := req.read(ctx, resolved, secrets, red, op, tagManagerTagsList.ID, "TagManager.getContainerTags", req.params())
	if err != nil {
		return nil, err
	}
	tags, next, truncated, err := req.listEntries(op, answer)
	if err != nil {
		return nil, err
	}
	return &TagsResult{IDSite: req.site, IDContainer: req.container, IDContainerVersion: req.version, Tags: tags,
		HasMore: next != "", NextCursor: next, Truncated: truncated}, nil
}

func invokeTagManagerTagsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get Tag Manager tag"
	req, err := entryRequestOf(resolved, raw, "idTag", "")
	if err != nil {
		return nil, err
	}
	params := req.params()
	params.Set("idTag", strconv.Itoa(req.object))
	return req.get(ctx, resolved, secrets, red, op, tagManagerTagsGet.ID, "TagManager.getContainerTag", params)
}

func invokeTagManagerTriggersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list Tag Manager triggers"
	req, err := entryRequestOf(resolved, raw, "", tagManagerTriggersList.ID)
	if err != nil {
		return nil, err
	}
	answer, err := req.read(ctx, resolved, secrets, red, op, tagManagerTriggersList.ID, "TagManager.getContainerTriggers", req.params())
	if err != nil {
		return nil, err
	}
	triggers, next, truncated, err := req.listEntries(op, answer)
	if err != nil {
		return nil, err
	}
	return &TriggersResult{IDSite: req.site, IDContainer: req.container, IDContainerVersion: req.version,
		Triggers: triggers, HasMore: next != "", NextCursor: next, Truncated: truncated}, nil
}

func invokeTagManagerTriggersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get Tag Manager trigger"
	req, err := entryRequestOf(resolved, raw, "idTrigger", "")
	if err != nil {
		return nil, err
	}
	params := req.params()
	params.Set("idTrigger", strconv.Itoa(req.object))
	return req.get(ctx, resolved, secrets, red, op, tagManagerTriggersGet.ID, "TagManager.getContainerTrigger", params)
}

func (r entryRequest) get(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, op, tool, method string, params url.Values) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, tagManagerRequirement(tool), method, params, &answer); err != nil {
		return nil, err
	}
	return r.getEntry(op, answer)
}
