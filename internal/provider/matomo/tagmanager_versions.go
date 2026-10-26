package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// Caps of one export. Tags, triggers, variables, and their parameters hold text that site owners and editors
	// wrote; the shared text budget keeps one export well below the response limit of the client.
	maxExportEntries   = 200
	maxExportText      = 256 << 10
	maxExportMembers   = 100
	maxExportDepth     = 6
	maxExportKeyLength = 128
	maxExportTriggers  = 200
	maxVersionReleases = 50
)

const (
	versionIDSchema = `"idContainerVersion":{"type":"integer","minimum":1,"maximum":2147483647}`
	releaseSchema   = `{"type":"object","properties":{` + releaseProperties + `},"additionalProperties":false}`
	versionDetail   = `"id":{"type":"integer"},"revision":{"type":"integer"},"name":{"type":"string"},` +
		`"description":{"type":"string"},"status":{"type":"string"},"created_date":{"type":"string"},` +
		`"updated_date":{"type":"string"},"created_date_pretty":{"type":"string"},` +
		`"updated_date_pretty":{"type":"string"},"releases":{"type":"array","items":` + releaseSchema + `},` +
		`"releases_truncated":{"type":"boolean"}`
	datesSchema = `"created_date":{"type":"string"},"updated_date":{"type":"string"},` +
		`"created_date_pretty":{"type":"string"},"updated_date_pretty":{"type":"string"}`
	exportEntrySchema = `{"type":"object","properties":{"id":{"type":"integer"},"type":{"type":"string"},` +
		`"name":{"type":"string"},"description":{"type":"string"},"parameters":{},` +
		`"fire_trigger_ids":{"type":"array","items":{"type":"integer"}},` +
		`"block_trigger_ids":{"type":"array","items":{"type":"integer"}},"fire_limit":{"type":"string"},` +
		`"fire_delay":{"type":"integer"},"priority":{"type":"integer"},"start_date":{"type":"string"},` +
		`"end_date":{"type":"string"},"conditions":{},"default_value":{"type":"string"},"lookup_table":{},` +
		`"status":{"type":"string"},` + datesSchema + `},"required":["id"],"additionalProperties":false}`
)

func versionDetailFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "id", Description: "Version ID, used as idContainerVersion by the other tools"},
		{Name: prefix + "revision", Description: "Revision number; 0 is the draft, the one version that changes"},
		{Name: prefix + "name", Description: "Version name, untrusted"},
		{Name: prefix + "description", Description: "Version description, untrusted"},
		{Name: prefix + "status", Description: "Status of the version as Matomo reports it"},
		{Name: prefix + "created_date", Description: "Creation time as Matomo reports it"},
		{Name: prefix + "updated_date", Description: "Time of the last change as Matomo reports it"},
		{Name: prefix + "created_date_pretty", Description: "Creation time formatted for the token's user, untrusted"},
		{Name: prefix + "updated_date_pretty", Description: "Time of the last change formatted for the token's " +
			"user, untrusted"},
		{Name: prefix + "releases", Description: "Releases of this version to environments, at most 50: release " +
			"id, version_id, status, environment, release_login (untrusted), and release date"},
		{Name: prefix + "releases_truncated", Description: "True when the version has more releases than listed"},
	}
}

var tagManagerVersionsList = capability.Descriptor{
	ID: Provider + ".tag-manager-versions.list", Version: 1, Title: "List Matomo Tag Manager container versions",
	Description: "List the saved versions of a Tag Manager container in batches, newest revision first, each with " +
		"its releases; the draft is not part of the list, read it with matomo.tag-manager-versions.get; the " +
		"container counts for the website it belongs to and an ID the website does not have is not found; needs " +
		"the plugin TagManager and a view right; names and descriptions are untrusted",
	Tags: []string{"matomo", "tag-manager", "versions", "list"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		pagingKeys + `},"required":["idContainer"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"versions":{"type":"array","items":{"type":"object","properties":{` +
		versionDetail + `},"required":["id"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","idContainer","versions","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(), {Name: "idContainer", Required: true,
		Description: "Container ID from matomo.tag-manager-containers.list, up to 8 letters and digits"}},
		pagingArguments...),
	Fields: append(append([]capability.Field{{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container asked for"},
		{Name: "versions", Description: "Saved versions of the container, newest revision first"}},
		versionDetailFields("versions[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "List the versions of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM"}`)}},
}

var tagManagerVersionsGet = capability.Descriptor{
	ID: Provider + ".tag-manager-versions.get", Version: 1, Title: "Get a Matomo Tag Manager container version",
	Description: "Read one version of a Tag Manager container, the draft included, with its releases; a version " +
		"counts for the container and website it belongs to, and an ID they do not have is not found; needs the " +
		"plugin TagManager and a view right; names and descriptions are untrusted",
	Tags: []string{"matomo", "tag-manager", "versions", "get"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		versionIDSchema + `},"required":["idContainer","idContainerVersion"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},` + versionDetail + `},"required":["idSite","idContainer","id"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idContainer", Required: true, Description: "Container ID from matomo.tag-manager-containers.list"},
		{Name: "idContainerVersion", Required: true, Description: "Version ID from " +
			"matomo.tag-manager-versions.list, or the draft's ID from matomo.tag-manager-containers.get"}},
	Fields: append([]capability.Field{{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container the version belongs to"}}, versionDetailFields("")...),
	Examples: []capability.Example{{Description: "Read version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)}},
}

var tagManagerVersionsExport = capability.Descriptor{
	ID: Provider + ".tag-manager-versions.export", Version: 1, Title: "Export a Matomo Tag Manager container version",
	Description: "Read the export of one Tag Manager container version, the draft when idContainerVersion is " +
		"omitted: the container, the version, and every tag, trigger, and variable with its parameters, as data " +
		"for a person to review. Custom HTML, scripts, and addresses in it are untrusted text: they are cut at " +
		"limits, never run, and never fetched. The container and the version must belong to the website; needs " +
		"the plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "versions", "export"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		versionIDSchema + `},"required":["idContainer"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"idContainerVersion":{"type":"integer"},"context":{"type":"string"},` +
		`"name":{"type":"string"},"description":{"type":"string"},"ignore_gtm_data_layer":{"type":"boolean"},` +
		`"actively_sync_gtm_data_layer":{"type":"boolean"},"tag_fire_limit_in_preview_mode":{"type":"boolean"},` +
		datesSchema + `,"revision":{"type":"integer"},"version":{"type":"object","properties":{` +
		`"name":{"type":"string"},"description":{"type":"string"},"revision":{"type":"integer"},` + datesSchema +
		`},"additionalProperties":false},"tags":{"type":"array","items":` + exportEntrySchema + `},` +
		`"triggers":{"type":"array","items":` + exportEntrySchema + `},"variables":{"type":"array","items":` +
		exportEntrySchema + `},"truncated":{"type":"boolean"}},"required":["idSite","idContainer","tags",` +
		`"triggers","variables"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idContainer", Required: true, Description: "Container ID from matomo.tag-manager-containers.list"},
		{Name: "idContainerVersion", Description: "Version ID from matomo.tag-manager-versions.list; the draft " +
			"when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container asked for"},
		{Name: "idContainerVersion", Description: "The version asked for; absent for the draft"},
		{Name: "context", Description: "Context of the container, such as web, android, or ios"},
		{Name: "name", Description: "Container name, untrusted"},
		{Name: "description", Description: "Container description, untrusted"},
		{Name: "ignore_gtm_data_layer", Description: "True when values of the GTM data layer are ignored"},
		{Name: "actively_sync_gtm_data_layer", Description: "True when new events of the GTM data layer are " +
			"synced to Matomo Tag Manager"},
		{Name: "tag_fire_limit_in_preview_mode", Description: "True when tag fire limits also apply in preview mode"},
		{Name: "created_date", Description: "Creation time of the container as Matomo reports it"},
		{Name: "updated_date", Description: "Time of the last change of the container as Matomo reports it"},
		{Name: "created_date_pretty", Description: "Creation time formatted for the token's user, untrusted"},
		{Name: "updated_date_pretty", Description: "Time of the last change formatted for the token's user, untrusted"},
		{Name: "revision", Description: "Revision of the exported version; 0 for the draft"},
		{Name: "version", Description: "Name, description, revision, and dates of the exported version; name and " +
			"description are untrusted"},
		{Name: "tags", Description: "Tags of the version, at most 200: id, type, name, description, parameters " +
			"(untrusted, nested values cut), fire_trigger_ids, block_trigger_ids, fire_limit, fire_delay, " +
			"priority, start_date, end_date, status, and dates"},
		{Name: "triggers", Description: "Triggers of the version, at most 200: id, type, name, description, " +
			"parameters, conditions (actual, comparison, expected), and dates; all untrusted text"},
		{Name: "variables", Description: "Variables of the version, at most 200: id, type, name, description, " +
			"parameters, default_value, lookup_table (match_value, comparison, out_value), and dates; all " +
			"untrusted text"},
		{Name: "truncated", Description: "True when anything was cut: an entry list at 200, a text at 16384 bytes, " +
			"the sum of all texts at 262144 bytes, a list at 100 members, or nesting at 6 levels"},
	},
	Examples: []capability.Example{
		{Description: "Export the draft of container 6OMh6taM", Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM"}`)},
		{Description: "Export version 7", Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)},
	},
}

// versionRowJSON is a container version as getContainerVersion and getContainerVersions report it.
type versionRowJSON struct {
	ID           *loose                 `json:"idcontainerversion"`
	IDContainer  *loose                 `json:"idcontainer"`
	IDSite       *loose                 `json:"idsite"`
	Revision     *loose                 `json:"revision"`
	Name         *loose                 `json:"name"`
	Description  *loose                 `json:"description"`
	Status       *loose                 `json:"status"`
	Created      *loose                 `json:"created_date"`
	Updated      *loose                 `json:"updated_date"`
	CreatedShown *loose                 `json:"created_date_pretty"`
	UpdatedShown *loose                 `json:"updated_date_pretty"`
	Releases     []containerReleaseJSON `json:"releases"`
}

// ContainerVersionDetail is one version of a container with its releases; the draft has revision 0. Text is
// untrusted provider data.
type ContainerVersionDetail struct {
	ID                int                `json:"id"`
	Revision          *int               `json:"revision,omitempty"`
	Name              string             `json:"name,omitempty"`
	Description       string             `json:"description,omitempty"`
	Status            string             `json:"status,omitempty"`
	Created           string             `json:"created_date,omitempty"`
	Updated           string             `json:"updated_date,omitempty"`
	CreatedShown      string             `json:"created_date_pretty,omitempty"`
	UpdatedShown      string             `json:"updated_date_pretty,omitempty"`
	Releases          []ContainerRelease `json:"releases"`
	ReleasesTruncated bool               `json:"releases_truncated,omitempty"`
}

// ContainerVersionsResult is the answer of tag-manager-versions.list.
type ContainerVersionsResult struct {
	IDSite      int                      `json:"idSite"`
	IDContainer string                   `json:"idContainer"`
	Versions    []ContainerVersionDetail `json:"versions"`
	HasMore     bool                     `json:"has_more"`
	NextCursor  string                   `json:"next_cursor,omitempty"`
}

// ContainerVersionResult is the answer of tag-manager-versions.get.
type ContainerVersionResult struct {
	IDSite      int    `json:"idSite"`
	IDContainer string `json:"idContainer"`
	ContainerVersionDetail
}

// detail converts a row; ok is false for a row without a version ID or one that names another website or
// container than asked for. The API binds the version to both, so a row without them stays bound by the request.
func (v versionRowJSON) detail(site int, container string) (ContainerVersionDetail, bool) {
	id := v.ID.number()
	if id == nil {
		return ContainerVersionDetail{}, false
	}
	if row := v.IDSite.number(); row != nil && *row != site {
		return ContainerVersionDetail{}, false
	}
	if row := v.IDContainer.str(maxStringLength); row != "" && row != container {
		return ContainerVersionDetail{}, false
	}
	out := ContainerVersionDetail{ID: *id, Revision: v.Revision.number(), Name: v.Name.str(maxStringLength),
		Description: v.Description.str(maxStringLength), Status: v.Status.str(maxStringLength),
		Created: v.Created.str(maxStringLength), Updated: v.Updated.str(maxStringLength),
		CreatedShown: v.CreatedShown.str(maxStringLength), UpdatedShown: v.UpdatedShown.str(maxStringLength),
		Releases: []ContainerRelease{}}
	for _, row := range v.Releases {
		if release, ok := row.release(); ok {
			if len(out.Releases) == maxVersionReleases {
				out.ReleasesTruncated = true
				break
			}
			out.Releases = append(out.Releases, release)
		}
	}
	return out, true
}

func invokeTagManagerVersionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list Tag Manager container versions"
	var args struct {
		IDSite      *int    `json:"idSite"`
		IDContainer *string `json:"idContainer"`
		Limit       int     `json:"limit"`
		Cursor      string  `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	containerID, err := containerIDOf(args.IDContainer)
	if err != nil {
		return nil, err
	}
	binding := provider.CursorBinding(tagManagerVersionsList.ID, scopeBinding(resolved), id, containerID)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID}}
	if err := client.call(ctx, op, tagManagerRequirement(tagManagerVersionsList.ID), "TagManager.getContainerVersions",
		params, &answer); err != nil {
		return nil, err
	}
	rows, ok := jsonRows(answer)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid version list")
	}
	versions := []ContainerVersionDetail{}
	for _, row := range rows {
		var parsed versionRowJSON
		if json.Unmarshal(row, &parsed) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid version list")
		}
		if version, ok := parsed.detail(id, containerID); ok {
			versions = append(versions, version)
		}
	}
	batch, next := slice(paging, versions)
	if batch == nil {
		batch = []ContainerVersionDetail{}
	}
	return &ContainerVersionsResult{IDSite: id, IDContainer: containerID, Versions: batch, HasMore: next != "",
		NextCursor: next}, nil
}

func versionIDOf(given *int, required bool) (int, error) {
	if given == nil && !required {
		return 0, nil
	}
	if given == nil || *given < 1 || *given > 2147483647 {
		return 0, invalidArgument("idContainerVersion must be a version ID between 1 and 2147483647")
	}
	return *given, nil
}

func invokeTagManagerVersionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get Tag Manager container version"
	var args struct {
		IDSite             *int    `json:"idSite"`
		IDContainer        *string `json:"idContainer"`
		IDContainerVersion *int    `json:"idContainerVersion"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	containerID, err := containerIDOf(args.IDContainer)
	if err != nil {
		return nil, err
	}
	versionID, err := versionIDOf(args.IDContainerVersion, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID},
		"idContainerVersion": {strconv.Itoa(versionID)}}
	if err := client.call(ctx, op, tagManagerRequirement(tagManagerVersionsGet.ID), "TagManager.getContainerVersion",
		params, &answer); err != nil {
		return nil, err
	}
	var row versionRowJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &row) != nil || row.ID == nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid container version")
	}
	found, ok := row.detail(id, containerID)
	if !ok || found.ID != versionID {
		return nil, provider.InvalidResponse(op, "Matomo returned another container version than asked for")
	}
	return &ContainerVersionResult{IDSite: id, IDContainer: containerID, ContainerVersionDetail: found}, nil
}

// ExportedVersion is the version part of an export.
type ExportedVersion struct {
	Name         string `json:"name,omitempty"`
	Description  string `json:"description,omitempty"`
	Revision     *int   `json:"revision,omitempty"`
	Created      string `json:"created_date,omitempty"`
	Updated      string `json:"updated_date,omitempty"`
	CreatedShown string `json:"created_date_pretty,omitempty"`
	UpdatedShown string `json:"updated_date_pretty,omitempty"`
}

// ExportEntry is a tag, a trigger, or a variable of an exported version; the members of the other kinds stay
// empty. Parameters, conditions, and the lookup table are untrusted text that is cut, never run.
type ExportEntry struct {
	ID              int    `json:"id"`
	Type            string `json:"type,omitempty"`
	Name            string `json:"name,omitempty"`
	Description     string `json:"description,omitempty"`
	Parameters      any    `json:"parameters,omitempty"`
	FireTriggerIDs  []int  `json:"fire_trigger_ids,omitempty"`
	BlockTriggerIDs []int  `json:"block_trigger_ids,omitempty"`
	FireLimit       string `json:"fire_limit,omitempty"`
	FireDelay       *int   `json:"fire_delay,omitempty"`
	Priority        *int   `json:"priority,omitempty"`
	StartDate       string `json:"start_date,omitempty"`
	EndDate         string `json:"end_date,omitempty"`
	Conditions      any    `json:"conditions,omitempty"`
	DefaultValue    string `json:"default_value,omitempty"`
	LookupTable     any    `json:"lookup_table,omitempty"`
	Status          string `json:"status,omitempty"`
	Created         string `json:"created_date,omitempty"`
	Updated         string `json:"updated_date,omitempty"`
	CreatedShown    string `json:"created_date_pretty,omitempty"`
	UpdatedShown    string `json:"updated_date_pretty,omitempty"`
}

// VersionExportResult is the answer of tag-manager-versions.export.
type VersionExportResult struct {
	IDSite             int              `json:"idSite"`
	IDContainer        string           `json:"idContainer"`
	IDContainerVersion *int             `json:"idContainerVersion,omitempty"`
	Context            string           `json:"context,omitempty"`
	Name               string           `json:"name,omitempty"`
	Description        string           `json:"description,omitempty"`
	IgnoreGTM          *bool            `json:"ignore_gtm_data_layer,omitempty"`
	SyncGTM            *bool            `json:"actively_sync_gtm_data_layer,omitempty"`
	FireLimit          *bool            `json:"tag_fire_limit_in_preview_mode,omitempty"`
	Created            string           `json:"created_date,omitempty"`
	Updated            string           `json:"updated_date,omitempty"`
	CreatedShown       string           `json:"created_date_pretty,omitempty"`
	UpdatedShown       string           `json:"updated_date_pretty,omitempty"`
	Revision           *int             `json:"revision,omitempty"`
	Version            *ExportedVersion `json:"version,omitempty"`
	Tags               []ExportEntry    `json:"tags"`
	Triggers           []ExportEntry    `json:"triggers"`
	Variables          []ExportEntry    `json:"variables"`
	Truncated          bool             `json:"truncated,omitempty"`
	// inexactTypes marks an entry whose type is no plain text as the plugin compares it.
	inexactTypes bool
}

type exportEntryJSON struct {
	IDSite          *loose          `json:"idsite"`
	IDContainerVer  *loose          `json:"idcontainerversion"`
	IDTag           *loose          `json:"idtag"`
	IDTrigger       *loose          `json:"idtrigger"`
	IDVariable      *loose          `json:"idvariable"`
	Type            *loose          `json:"type"`
	Name            *loose          `json:"name"`
	Description     *loose          `json:"description"`
	Parameters      json.RawMessage `json:"parameters"`
	FireTriggerIDs  json.RawMessage `json:"fire_trigger_ids"`
	BlockTriggerIDs json.RawMessage `json:"block_trigger_ids"`
	FireLimit       *loose          `json:"fire_limit"`
	FireDelay       *loose          `json:"fire_delay"`
	Priority        *loose          `json:"priority"`
	StartDate       *loose          `json:"start_date"`
	EndDate         *loose          `json:"end_date"`
	Conditions      json.RawMessage `json:"conditions"`
	DefaultValue    *loose          `json:"default_value"`
	LookupTable     json.RawMessage `json:"lookup_table"`
	Status          *loose          `json:"status"`
	Created         *loose          `json:"created_date"`
	Updated         *loose          `json:"updated_date"`
	CreatedShown    *loose          `json:"created_date_pretty"`
	UpdatedShown    *loose          `json:"updated_date_pretty"`
}

type exportJSON struct {
	containerJSON
	Revision  *loose          `json:"revision"`
	Version   *versionRowJSON `json:"version"`
	Tags      json.RawMessage `json:"tags"`
	Triggers  json.RawMessage `json:"triggers"`
	Variables json.RawMessage `json:"variables"`
}

// exportBudget bounds the untrusted content of one export. It only shortens; it never interprets a text.
type exportBudget struct {
	remaining    int
	truncated    bool
	inexactTypes bool
}

func (b *exportBudget) text(value string, limit int) string {
	limit = min(limit, b.remaining)
	out := boundString(value, limit)
	if len(out) < len(value) {
		b.truncated = true
	}
	b.remaining -= len(out)
	return out
}

// value copies a decoded JSON value with bounded strings, lists, objects, and depth.
func (b *exportBudget) value(v any, depth int) any {
	switch typed := v.(type) {
	case string:
		return b.text(typed, maxCodeLength)
	case []any:
		if depth >= maxExportDepth {
			b.truncated = true
			return nil
		}
		if len(typed) > maxExportMembers {
			typed, b.truncated = typed[:maxExportMembers], true
		}
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, b.value(item, depth+1))
		}
		return out
	case map[string]any:
		if depth >= maxExportDepth {
			b.truncated = true
			return nil
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > maxExportMembers {
			keys, b.truncated = keys[:maxExportMembers], true
		}
		out := make(map[string]any, len(keys))
		for _, key := range keys {
			out[b.text(key, maxExportKeyLength)] = b.value(typed[key], depth+1)
		}
		return out
	}
	return v
}

// structured reads a nested member; PHP reports an empty list or map as [], which counts as absent.
func (b *exportBudget) structured(raw json.RawMessage) (any, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, true
	}
	var parsed any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&parsed) != nil {
		return nil, false
	}
	switch typed := parsed.(type) {
	case []any:
		if len(typed) == 0 {
			return nil, true
		}
	case map[string]any:
		if len(typed) == 0 {
			return nil, true
		}
	}
	return b.value(parsed, 0), true
}

func triggerIDs(raw json.RawMessage) ([]int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, true
	}
	rows, ok := jsonRows(raw)
	if !ok {
		return nil, false
	}
	var ids []int
	for _, row := range rows {
		var one loose
		if json.Unmarshal(row, &one) != nil {
			return nil, false
		}
		if n := one.number(); n != nil && len(ids) < maxExportTriggers {
			ids = append(ids, *n)
		}
	}
	return ids, true
}

func (p exportEntryJSON) id() *int {
	for _, candidate := range []*loose{p.IDTag, p.IDTrigger, p.IDVariable} {
		if candidate != nil {
			return candidate.number()
		}
	}
	return nil
}

// entry converts one tag, trigger, or variable row; ok is false for a member of the wrong shape.
func (b *exportBudget) entry(parsed exportEntryJSON, id int) (ExportEntry, bool) {
	entry := ExportEntry{ID: id, Type: parsed.Type.str(maxStringLength), Name: parsed.Name.str(maxStringLength),
		Description: b.text(parsed.Description.str(maxStepText), maxStepText),
		FireLimit:   parsed.FireLimit.str(maxStringLength), FireDelay: parsed.FireDelay.number(),
		Priority: parsed.Priority.number(), StartDate: parsed.StartDate.str(maxStringLength),
		EndDate: parsed.EndDate.str(maxStringLength), Status: parsed.Status.str(maxStringLength),
		Created: parsed.Created.str(maxStringLength), Updated: parsed.Updated.str(maxStringLength),
		CreatedShown: parsed.CreatedShown.str(maxStringLength), UpdatedShown: parsed.UpdatedShown.str(maxStringLength)}
	if parsed.DefaultValue != nil {
		entry.DefaultValue = b.text(strings.TrimSpace(parsed.DefaultValue.text), maxStepText)
	}
	var ok1, ok2, ok3, ok4, ok5 bool
	entry.Parameters, ok1 = b.structured(parsed.Parameters)
	entry.Conditions, ok2 = b.structured(parsed.Conditions)
	entry.LookupTable, ok3 = b.structured(parsed.LookupTable)
	entry.FireTriggerIDs, ok4 = triggerIDs(parsed.FireTriggerIDs)
	entry.BlockTriggerIDs, ok5 = triggerIDs(parsed.BlockTriggerIDs)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return ExportEntry{}, false
	}
	return entry, true
}

// entries reads the tags, triggers, or variables of an export.
func (b *exportBudget) entries(raw json.RawMessage) ([]ExportEntry, bool) {
	out := []ExportEntry{}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, true
	}
	rows, ok := jsonRows(raw)
	if !ok {
		return nil, false
	}
	for _, row := range rows {
		var parsed exportEntryJSON
		if json.Unmarshal(row, &parsed) != nil {
			return nil, false
		}
		id := parsed.id()
		if id == nil {
			return nil, false
		}
		if len(out) == maxExportEntries {
			b.truncated = true
			break
		}
		if parsed.Type == nil || parsed.Type.str(maxStringLength) != parsed.Type.text {
			b.inexactTypes = true
		}
		entry, ok := b.entry(parsed, *id)
		if !ok {
			return nil, false
		}
		out = append(out, entry)
	}
	return out, true
}

func invokeTagManagerVersionsExport(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "export Tag Manager container version"
	var args struct {
		IDSite             *int    `json:"idSite"`
		IDContainer        *string `json:"idContainer"`
		IDContainerVersion *int    `json:"idContainerVersion"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	containerID, err := containerIDOf(args.IDContainer)
	if err != nil {
		return nil, err
	}
	versionID, err := versionIDOf(args.IDContainerVersion, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.exportVersion(ctx, op, tagManagerRequirement(tagManagerVersionsExport.ID), id, containerID, versionID)
}

// exportVersion reads the export of a version, the draft when version is 0, bound to the website and container.
func (c *Client) exportVersion(ctx context.Context, op string, req requirement, id int, containerID string,
	versionID int) (*VersionExportResult, error) {
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID}}
	if versionID != 0 {
		params.Set("idContainerVersion", strconv.Itoa(versionID))
	}
	if err := c.call(ctx, op, req, "TagManager.exportContainerVersion", params, &answer); err != nil {
		return nil, err
	}
	var row exportJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &row) != nil || row.ID == nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid container export")
	}
	if row.ID.str(maxStringLength) != containerID {
		return nil, provider.InvalidResponse(op, "Matomo returned another container than asked for")
	}
	if site := row.IDSite.number(); site != nil && *site != id {
		return nil, provider.InvalidResponse(op, "Matomo returned another container than asked for")
	}
	budget := &exportBudget{remaining: maxExportText}
	result := &VersionExportResult{IDSite: id, IDContainer: containerID, Context: row.Context.str(maxStringLength),
		Name: row.Name.str(maxStringLength), Description: row.Description.str(maxStringLength),
		IgnoreGTM: row.IgnoreGTM.flag(), SyncGTM: row.SyncGTM.flag(), FireLimit: row.FireLimit.flag(),
		Created: row.Created.str(maxStringLength), Updated: row.Updated.str(maxStringLength),
		CreatedShown: row.CreatedShown.str(maxStringLength), UpdatedShown: row.UpdatedShown.str(maxStringLength),
		Revision: row.Revision.number()}
	if versionID != 0 {
		result.IDContainerVersion = &versionID
	}
	if v := row.Version; v != nil {
		result.Version = &ExportedVersion{Name: v.Name.str(maxStringLength), Description: v.Description.str(maxStringLength),
			Revision: v.Revision.number(), Created: v.Created.str(maxStringLength), Updated: v.Updated.str(maxStringLength),
			CreatedShown: v.CreatedShown.str(maxStringLength), UpdatedShown: v.UpdatedShown.str(maxStringLength)}
	}
	var ok1, ok2, ok3 bool
	result.Tags, ok1 = budget.entries(row.Tags)
	result.Triggers, ok2 = budget.entries(row.Triggers)
	result.Variables, ok3 = budget.entries(row.Variables)
	if !ok1 || !ok2 || !ok3 {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid container export")
	}
	result.Truncated, result.inexactTypes = budget.truncated, budget.inexactTypes
	return result, nil
}
