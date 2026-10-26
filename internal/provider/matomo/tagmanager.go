package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
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
	tagManagerSensitive = "matomo-tag-manager"
	tagManagerPlugin    = "TagManager"
	// Caps of one container: its versions and releases grow with every publication.
	maxContainerVersions = 50
	maxContainerReleases = 50
	maxVersionEnvs       = 20
	maxInstallSteps      = 10
	maxStepText          = 1024
)

// containerIDPattern is the form of a container ID: Matomo draws eight letters and digits. environmentPattern is
// the form of an environment ID (2 to 40 letters, digits, and underscores).
var (
	containerIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,8}$`)
	environmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]{0,38}[A-Za-z0-9]$`)
)

const (
	containerIDSchema = `"idContainer":{"type":"string","pattern":"^[A-Za-z0-9]{1,8}$","maxLength":8}`
	environmentSchema = `"environment":{"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9_]{0,38}[A-Za-z0-9]$","maxLength":40}`
	versionProperties = `"id":{"type":"integer"},"revision":{"type":"integer"},"name":{"type":"string"},` +
		`"description":{"type":"string"},"status":{"type":"string"},"created_date":{"type":"string"},` +
		`"updated_date":{"type":"string"},"created_date_pretty":{"type":"string"},` +
		`"updated_date_pretty":{"type":"string"},"environments":{"type":"array","items":{"type":"string"}}`
	releaseProperties = `"id":{"type":"integer"},"version_id":{"type":"integer"},"version_name":{"type":"string"},` +
		`"status":{"type":"string"},"environment":{"type":"string"},"release_login":{"type":"string"},` +
		`"release_date":{"type":"string"},"release_date_pretty":{"type":"string"}`
	containerProperties = `"id":{"type":"string"},"context":{"type":"string"},"name":{"type":"string"},` +
		`"description":{"type":"string"},"ignore_gtm_data_layer":{"type":"boolean"},` +
		`"actively_sync_gtm_data_layer":{"type":"boolean"},"tag_fire_limit_in_preview_mode":{"type":"boolean"},` +
		`"status":{"type":"string"},"created_date":{"type":"string"},"updated_date":{"type":"string"},` +
		`"created_date_pretty":{"type":"string"},"updated_date_pretty":{"type":"string"},` +
		`"draft":{"type":"object","properties":{` + versionProperties + `},"additionalProperties":false},` +
		`"versions":{"type":"array","items":{"type":"object","properties":{` + versionProperties + `},` +
		`"additionalProperties":false}},"versions_truncated":{"type":"boolean"},` +
		`"releases":{"type":"array","items":{"type":"object","properties":{` + releaseProperties + `},` +
		`"additionalProperties":false}},"releases_truncated":{"type":"boolean"}`
)

func tagManagerRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: tagManagerPlugin}
}

func containerFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "id", Description: "Container ID, used as idContainer by the other tools"},
		{Name: prefix + "context", Description: "Context of the container, such as web, android, or ios"},
		{Name: prefix + "name", Description: "Container name, untrusted"},
		{Name: prefix + "description", Description: "Container description, untrusted"},
		{Name: prefix + "ignore_gtm_data_layer", Description: "True when values of the GTM data layer are ignored"},
		{Name: prefix + "actively_sync_gtm_data_layer", Description: "True when new events of the GTM data layer are " +
			"synced to Matomo Tag Manager"},
		{Name: prefix + "tag_fire_limit_in_preview_mode", Description: "True when tag fire limits also apply in " +
			"preview mode"},
		{Name: prefix + "status", Description: "Status of the container as Matomo reports it"},
		{Name: prefix + "created_date", Description: "Creation time as Matomo reports it"},
		{Name: prefix + "updated_date", Description: "Time of the last change as Matomo reports it"},
		{Name: prefix + "created_date_pretty", Description: "Creation time formatted for the token's user, untrusted"},
		{Name: prefix + "updated_date_pretty", Description: "Time of the last change formatted for the token's " +
			"user, untrusted"},
		{Name: prefix + "draft", Description: "The draft version, the only one that changes; it has the members of " +
			"a version and no environments"},
		{Name: prefix + "versions", Description: "Versions of the container, at most 50: version id, revision " +
			"(0 for the draft), name, description, status, dates, and the environments the version is released to"},
		{Name: prefix + "versions_truncated", Description: "True when the container has more versions than listed"},
		{Name: prefix + "releases", Description: "Releases of the container, at most 50: release id, version_id, " +
			"version_name, status, environment, release_login (untrusted), and release date"},
		{Name: prefix + "releases_truncated", Description: "True when the container has more releases than listed"},
	}
}

var tagManagerContainersList = capability.Descriptor{
	ID: Provider + ".tag-manager-containers.list", Version: 1, Title: "List Matomo Tag Manager containers",
	Description: "List the Tag Manager containers of a website in batches ordered by name, each with its draft, " +
		"versions, and releases; needs the plugin TagManager and a view right; names and descriptions are untrusted",
	Tags: []string{"matomo", "tag-manager", "containers", "list"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"containers":` +
		`{"type":"array","items":{"type":"object","properties":{` + containerProperties + `},` +
		`"required":["id"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","containers","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, pagingArguments...),
	Fields: append(append([]capability.Field{{Name: "idSite", Description: "The website the containers belong to"},
		{Name: "containers", Description: "Containers of the website ordered by name"}},
		containerFields("containers[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "List the containers of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var tagManagerContainersGet = capability.Descriptor{
	ID: Provider + ".tag-manager-containers.get", Version: 1, Title: "Get a Matomo Tag Manager container",
	Description: "Read one Tag Manager container of a website with its draft, versions, and releases; a container " +
		"counts for the website it belongs to, and an ID the website does not have is not found; needs the plugin " +
		"TagManager and a view right; names and descriptions are untrusted",
	Tags: []string{"matomo", "tag-manager", "containers", "get"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema +
		`},"required":["idContainer"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` + containerProperties +
		`},"required":["idSite","id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), {Name: "idContainer", Required: true,
		Description: "Container ID from matomo.tag-manager-containers.list, up to 8 letters and digits"}},
	Fields: append([]capability.Field{{Name: "idSite", Description: "The website the container belongs to"}},
		containerFields("")...),
	Examples: []capability.Example{{Description: "Read container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM"}`)}},
}

const (
	formEmbedCode           = "embed-code"
	formInstallInstructions = "install-instructions"
)

var tagManagerContainersGetEmbedCode = capability.Descriptor{
	ID: Provider + ".tag-manager-containers.get-embed-code", Version: 1, Title: "Get a Matomo Tag Manager embed code",
	Description: "Read how a container release is embedded on a website: form embed-code calls " +
		"getContainerEmbedCode and returns the snippet of the web context (empty for a mobile container), form " +
		"install-instructions calls getContainerInstallInstructions and returns the steps, with the React variant " +
		"when jsFramework is react. The text is untrusted for a person to review: it is never run and no address " +
		"in it is ever fetched; needs the plugin TagManager and a view right",
	Tags: []string{"matomo", "tag-manager", "containers", "embed", "install"}, Risk: readRisk(tagManagerSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		environmentSchema + `,"form":{"type":"string","enum":["embed-code","install-instructions"]},` +
		`"jsFramework":{"type":"string","enum":["react"]}},"required":["idContainer","environment"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"environment":{"type":"string"},"form":{"type":"string"},` +
		`"code":{"type":"string"},"truncated":{"type":"boolean"},"steps":{"type":"array","items":{"type":"object",` +
		`"properties":{"description":{"type":"string"},"code":{"type":"string"},"truncated":{"type":"boolean"},` +
		`"help_url":{"type":"string"},"page_view_trigger_edit_url":{"type":"string"}},"additionalProperties":false}}},` +
		`"required":["idSite","idContainer","environment","form"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idContainer", Required: true, Description: "Container ID from matomo.tag-manager-containers.list"},
		{Name: "environment", Required: true, Description: "Environment ID the release is loaded from, such as live " +
			"or an ID from matomo.tag-manager-catalog.list kind environments; 2 to 40 letters, digits, and underscores"},
		{Name: "form", Description: "embed-code for the snippet, install-instructions for the steps; embed-code " +
			"when omitted"},
		{Name: "jsFramework", Description: "react for the React variant of the instructions; install-instructions " +
			"only; the plain variant when omitted"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container asked for"},
		{Name: "environment", Description: "The environment asked for"},
		{Name: "form", Description: "The form asked for"},
		{Name: "code", Description: "For embed-code: the snippet as text, at most 16384 bytes, untrusted; empty " +
			"for a container that is not of the web context"},
		{Name: "truncated", Description: "True when the snippet was cut at the limit"},
		{Name: "steps", Description: "For install-instructions: at most 10 steps"},
		{Name: "steps[].description", Description: "What to do, untrusted"},
		{Name: "steps[].code", Description: "Snippet or code of the step, at most 16384 bytes, untrusted"},
		{Name: "steps[].truncated", Description: "True when the code of the step was cut at the limit"},
		{Name: "steps[].help_url", Description: "Address of the Matomo guide, untrusted text that is never fetched"},
		{Name: "steps[].page_view_trigger_edit_url", Description: "Address of the page view trigger in the Matomo " +
			"interface, for the React variant with a write right; untrusted text that is never fetched"},
	},
	Examples: []capability.Example{
		{Description: "Snippet of the live release of container 6OMh6taM",
			Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","environment":"live"}`)},
		{Description: "React instructions for the live release",
			Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","environment":"live","form":"install-instructions","jsFramework":"react"}`)},
	},
}

// containerVersionJSON is a container version as TagManager reports it. Every member is read loosely.
type containerVersionJSON struct {
	ID           *loose  `json:"idcontainerversion"`
	Revision     *loose  `json:"revision"`
	Name         *loose  `json:"name"`
	Description  *loose  `json:"description"`
	Status       *loose  `json:"status"`
	Created      *loose  `json:"created_date"`
	Updated      *loose  `json:"updated_date"`
	CreatedShown *loose  `json:"created_date_pretty"`
	UpdatedShown *loose  `json:"updated_date_pretty"`
	Environments []loose `json:"environments"`
}

type containerReleaseJSON struct {
	ID           *loose `json:"idcontainerrelease"`
	VersionID    *loose `json:"idcontainerversion"`
	VersionName  *loose `json:"version_name"`
	Status       *loose `json:"status"`
	Environment  *loose `json:"environment"`
	Login        *loose `json:"release_login"`
	Released     *loose `json:"release_date"`
	ReleasedShow *loose `json:"release_date_pretty"`
}

type containerJSON struct {
	ID           *loose                 `json:"idcontainer"`
	IDSite       *loose                 `json:"idsite"`
	Context      *loose                 `json:"context"`
	Name         *loose                 `json:"name"`
	Description  *loose                 `json:"description"`
	IgnoreGTM    *loose                 `json:"ignoreGtmDataLayer"`
	SyncGTM      *loose                 `json:"activelySyncGtmDataLayer"`
	FireLimit    *loose                 `json:"isTagFireLimitAllowedInPreviewMode"`
	Status       *loose                 `json:"status"`
	Created      *loose                 `json:"created_date"`
	Updated      *loose                 `json:"updated_date"`
	CreatedShown *loose                 `json:"created_date_pretty"`
	UpdatedShown *loose                 `json:"updated_date_pretty"`
	Draft        json.RawMessage        `json:"draft"`
	Versions     []containerVersionJSON `json:"versions"`
	Releases     []containerReleaseJSON `json:"releases"`
}

// ContainerVersion is a version of a container; the draft has revision 0. Text is untrusted provider data.
type ContainerVersion struct {
	ID           int      `json:"id"`
	Revision     *int     `json:"revision,omitempty"`
	Name         string   `json:"name,omitempty"`
	Description  string   `json:"description,omitempty"`
	Status       string   `json:"status,omitempty"`
	Created      string   `json:"created_date,omitempty"`
	Updated      string   `json:"updated_date,omitempty"`
	CreatedShown string   `json:"created_date_pretty,omitempty"`
	UpdatedShown string   `json:"updated_date_pretty,omitempty"`
	Environments []string `json:"environments,omitempty"`
}

// ContainerRelease is the release of a version to an environment.
type ContainerRelease struct {
	ID           int    `json:"id"`
	VersionID    int    `json:"version_id,omitempty"`
	VersionName  string `json:"version_name,omitempty"`
	Status       string `json:"status,omitempty"`
	Environment  string `json:"environment,omitempty"`
	Login        string `json:"release_login,omitempty"`
	Released     string `json:"release_date,omitempty"`
	ReleasedShow string `json:"release_date_pretty,omitempty"`
}

// Container is a Tag Manager container of a website. All text is untrusted provider data.
type Container struct {
	ID                string             `json:"id"`
	Context           string             `json:"context,omitempty"`
	Name              string             `json:"name,omitempty"`
	Description       string             `json:"description,omitempty"`
	IgnoreGTM         *bool              `json:"ignore_gtm_data_layer,omitempty"`
	SyncGTM           *bool              `json:"actively_sync_gtm_data_layer,omitempty"`
	FireLimit         *bool              `json:"tag_fire_limit_in_preview_mode,omitempty"`
	Status            string             `json:"status,omitempty"`
	Created           string             `json:"created_date,omitempty"`
	Updated           string             `json:"updated_date,omitempty"`
	CreatedShown      string             `json:"created_date_pretty,omitempty"`
	UpdatedShown      string             `json:"updated_date_pretty,omitempty"`
	Draft             *ContainerVersion  `json:"draft,omitempty"`
	Versions          []ContainerVersion `json:"versions"`
	VersionsTruncated bool               `json:"versions_truncated,omitempty"`
	Releases          []ContainerRelease `json:"releases"`
	ReleasesTruncated bool               `json:"releases_truncated,omitempty"`
}

// ContainersResult is the answer of tag-manager-containers.list.
type ContainersResult struct {
	IDSite     int         `json:"idSite"`
	Containers []Container `json:"containers"`
	HasMore    bool        `json:"has_more"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// ContainerResult is the answer of tag-manager-containers.get.
type ContainerResult struct {
	IDSite int `json:"idSite"`
	Container
}

func (v containerVersionJSON) version() (ContainerVersion, bool) {
	id := v.ID.number()
	if id == nil {
		return ContainerVersion{}, false
	}
	out := ContainerVersion{ID: *id, Revision: v.Revision.number(), Name: v.Name.str(maxStringLength),
		Description: v.Description.str(maxStringLength), Status: v.Status.str(maxStringLength),
		Created: v.Created.str(maxStringLength), Updated: v.Updated.str(maxStringLength),
		CreatedShown: v.CreatedShown.str(maxStringLength), UpdatedShown: v.UpdatedShown.str(maxStringLength)}
	for i := range v.Environments {
		if len(out.Environments) == maxVersionEnvs {
			break
		}
		if env := v.Environments[i].str(40); env != "" {
			out.Environments = append(out.Environments, env)
		}
	}
	return out, true
}

func (r containerReleaseJSON) release() (ContainerRelease, bool) {
	id := r.ID.number()
	if id == nil {
		return ContainerRelease{}, false
	}
	out := ContainerRelease{ID: *id, VersionName: r.VersionName.str(maxStringLength),
		Status: r.Status.str(maxStringLength), Environment: r.Environment.str(40), Login: r.Login.str(maxStringLength),
		Released: r.Released.str(maxStringLength), ReleasedShow: r.ReleasedShow.str(maxStringLength)}
	if version := r.VersionID.number(); version != nil {
		out.VersionID = *version
	}
	return out, true
}

// container converts a row; ok is false for a row without a container ID or from another website than site.
func (c containerJSON) container(site int) (Container, bool) {
	id := c.ID.str(maxStringLength)
	if id == "" || !containerIDPattern.MatchString(id) {
		return Container{}, false
	}
	if row := c.IDSite.number(); row != nil && *row != site {
		return Container{}, false
	}
	out := Container{ID: id, Context: c.Context.str(maxStringLength), Name: c.Name.str(maxStringLength),
		Description: c.Description.str(maxStringLength), IgnoreGTM: c.IgnoreGTM.flag(), SyncGTM: c.SyncGTM.flag(),
		FireLimit: c.FireLimit.flag(), Status: c.Status.str(maxStringLength), Created: c.Created.str(maxStringLength),
		Updated: c.Updated.str(maxStringLength), CreatedShown: c.CreatedShown.str(maxStringLength),
		UpdatedShown: c.UpdatedShown.str(maxStringLength), Versions: []ContainerVersion{}, Releases: []ContainerRelease{}}
	if draft := bytes.TrimSpace(c.Draft); bytes.HasPrefix(draft, []byte("{")) {
		var row containerVersionJSON
		if json.Unmarshal(draft, &row) == nil {
			if version, ok := row.version(); ok {
				out.Draft = &version
			}
		}
	}
	for _, row := range c.Versions {
		if version, ok := row.version(); ok {
			if len(out.Versions) == maxContainerVersions {
				out.VersionsTruncated = true
				break
			}
			out.Versions = append(out.Versions, version)
		}
	}
	for _, row := range c.Releases {
		if release, ok := row.release(); ok {
			if len(out.Releases) == maxContainerReleases {
				out.ReleasesTruncated = true
				break
			}
			out.Releases = append(out.Releases, release)
		}
	}
	return out, true
}

// jsonRows reads a list answer: an array, or an object whose members are the rows in the order of their keys
// (PHP keeps the keys of a filtered list).
func jsonRows(raw json.RawMessage) ([]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	switch {
	case bytes.HasPrefix(raw, []byte("[")):
		var rows []json.RawMessage
		return rows, json.Unmarshal(raw, &rows) == nil
	case bytes.HasPrefix(raw, []byte("{")):
		var keyed map[string]json.RawMessage
		if json.Unmarshal(raw, &keyed) != nil {
			return nil, false
		}
		keys := make([]string, 0, len(keyed))
		for key := range keyed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, errA := strconv.Atoi(keys[i])
			b, errB := strconv.Atoi(keys[j])
			if errA == nil && errB == nil {
				return a < b
			}
			return keys[i] < keys[j]
		})
		rows := make([]json.RawMessage, 0, len(keys))
		for _, key := range keys {
			rows = append(rows, keyed[key])
		}
		return rows, true
	}
	return nil, false
}

func invokeTagManagerContainersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list Tag Manager containers"
	var args struct {
		IDSite *int   `json:"idSite"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	binding := provider.CursorBinding(tagManagerContainersList.ID, scopeBinding(resolved), id)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if err := client.call(ctx, op, tagManagerRequirement(tagManagerContainersList.ID), "TagManager.getContainers",
		params, &answer); err != nil {
		return nil, err
	}
	rows, ok := jsonRows(answer)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid container list")
	}
	containers := []Container{}
	for _, row := range rows {
		var parsed containerJSON
		if json.Unmarshal(row, &parsed) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid container list")
		}
		if container, ok := parsed.container(id); ok {
			containers = append(containers, container)
		}
	}
	slices.SortStableFunc(containers, func(a, b Container) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	batch, next := slice(paging, containers)
	if batch == nil {
		batch = []Container{}
	}
	return &ContainersResult{IDSite: id, Containers: batch, HasMore: next != "", NextCursor: next}, nil
}

func containerIDOf(given *string) (string, error) {
	if given == nil || !containerIDPattern.MatchString(*given) {
		return "", invalidArgument("idContainer must be a container ID of up to 8 letters and digits")
	}
	return *given, nil
}

func invokeTagManagerContainersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get Tag Manager container"
	var args struct {
		IDSite      *int    `json:"idSite"`
		IDContainer *string `json:"idContainer"`
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
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID}}
	if err := client.call(ctx, op, tagManagerRequirement(tagManagerContainersGet.ID), "TagManager.getContainer",
		params, &answer); err != nil {
		return nil, err
	}
	var row containerJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &row) != nil || row.ID == nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid container")
	}
	found, ok := row.container(id)
	if !ok || found.ID != containerID {
		return nil, provider.InvalidResponse(op, "Matomo returned another container than asked for")
	}
	return &ContainerResult{IDSite: id, Container: found}, nil
}

// InstallStep is one step of the installation instructions of a container release.
type InstallStep struct {
	Description            string `json:"description,omitempty"`
	Code                   string `json:"code,omitempty"`
	Truncated              bool   `json:"truncated,omitempty"`
	HelpURL                string `json:"help_url,omitempty"`
	PageViewTriggerEditURL string `json:"page_view_trigger_edit_url,omitempty"`
}

// EmbedCodeResult is the answer of tag-manager-containers.get-embed-code.
type EmbedCodeResult struct {
	IDSite      int           `json:"idSite"`
	IDContainer string        `json:"idContainer"`
	Environment string        `json:"environment"`
	Form        string        `json:"form"`
	Code        string        `json:"code,omitempty"`
	Truncated   bool          `json:"truncated,omitempty"`
	Steps       []InstallStep `json:"steps,omitempty"`
}

type installStepJSON struct {
	Description *loose `json:"description"`
	EmbedCode   *loose `json:"embedCode"`
	HelpURL     *loose `json:"helpUrl"`
	TriggerURL  *loose `json:"pageViewTriggerEditUrl"`
}

func invokeTagManagerContainersGetEmbedCode(ctx context.Context, resolved *config.Resolved,
	secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get Tag Manager embed code"
	var args struct {
		IDSite      *int    `json:"idSite"`
		IDContainer *string `json:"idContainer"`
		Environment string  `json:"environment"`
		Form        string  `json:"form"`
		JSFramework string  `json:"jsFramework"`
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
	if !environmentPattern.MatchString(args.Environment) {
		return nil, invalidArgument("environment must be an environment ID of 2 to 40 letters, digits, and underscores")
	}
	form := args.Form
	if form == "" {
		form = formEmbedCode
	}
	if form != formEmbedCode && form != formInstallInstructions {
		return nil, invalidArgument("form must be embed-code or install-instructions")
	}
	if args.JSFramework != "" && (args.JSFramework != "react" || form != formInstallInstructions) {
		return nil, invalidArgument("jsFramework must be react and belongs to form install-instructions")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID},
		"environment": {args.Environment}}
	req := tagManagerRequirement(tagManagerContainersGetEmbedCode.ID)
	result := &EmbedCodeResult{IDSite: id, IDContainer: containerID, Environment: args.Environment, Form: form}
	var answer json.RawMessage
	if form == formEmbedCode {
		if err := client.call(ctx, op, req, "TagManager.getContainerEmbedCode", params, &answer); err != nil {
			return nil, err
		}
		var code string
		if scalar(answer, &code) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid embed code")
		}
		result.Code, result.Truncated = boundString(code, maxCodeLength), len(code) > maxCodeLength
		return result, nil
	}
	if args.JSFramework != "" {
		params.Set("jsFramework", args.JSFramework)
	}
	if err := client.call(ctx, op, req, "TagManager.getContainerInstallInstructions", params, &answer); err != nil {
		return nil, err
	}
	rows, ok := jsonRows(answer)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned invalid installation instructions")
	}
	result.Steps = []InstallStep{}
	for _, row := range rows {
		var step installStepJSON
		if json.Unmarshal(row, &step) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned invalid installation instructions")
		}
		if len(result.Steps) == maxInstallSteps {
			break
		}
		code := ""
		if step.EmbedCode != nil {
			code = step.EmbedCode.text
		}
		result.Steps = append(result.Steps, InstallStep{Description: step.Description.str(maxStepText),
			Code: boundString(code, maxCodeLength), Truncated: len(code) > maxCodeLength,
			HelpURL: step.HelpURL.str(maxUrlLength), PageViewTriggerEditURL: step.TriggerURL.str(maxUrlLength)})
	}
	return result, nil
}
