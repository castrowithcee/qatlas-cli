package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var (
	containerSentOnce = sentOnceFor("container", "matomo.tag-manager-containers.list")
	// A context ID is a short lower-case word such as web, android, or ios; Matomo checks it against its contexts.
	containerContextPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
)

const (
	containerNameSchema    = `"name":{"type":"string","minLength":1,"maxLength":50}`
	containerDescSchema    = `"description":{"type":"string","maxLength":1000}`
	containerContextSchema = `"context":{"type":"string","pattern":"^[a-z][a-z0-9_-]{0,39}$","maxLength":40}`
	containerFlagsSchema   = `"ignoreGtmDataLayer":{"type":"boolean"},"isTagFireLimitAllowedInPreviewMode":{"type":"boolean"},` +
		`"activelySyncGtmDataLayer":{"type":"boolean"}`
)

func containerFlagArguments(required bool) []capability.Argument {
	suffix := "; false when omitted"
	if required {
		suffix = "; required because the update replaces every setting"
	}
	return []capability.Argument{
		{Name: "ignoreGtmDataLayer", Required: required, Description: "True to ignore values of the Google Tag Manager " +
			"data layer; takes effect after the next publication of a version" + suffix},
		{Name: "isTagFireLimitAllowedInPreviewMode", Required: required, Description: "True to apply tag fire " +
			"limits in preview mode, too" + suffix},
		{Name: "activelySyncGtmDataLayer", Required: required, Description: "True to sync new events of the Google " +
			"Tag Manager data layer to Matomo Tag Manager; takes effect after the next publication of a version" + suffix},
	}
}

var tagManagerContainersCreateDefault = capability.Descriptor{
	ID: Provider + ".tag-manager-containers.create-default", Version: 1,
	Title: "Create the default Matomo Tag Manager container",
	Description: "Create the default Tag Manager container of a website (createDefaultContainerForSite). Matomo " +
		"creates the container with its draft only: nothing is saved as a version and nothing is published, so the " +
		"container does nothing on the website until a person releases a version. Needs the plugin TagManager and a " +
		"write right; not idempotent, since a repeated call may create a second container" + containerSentOnce,
	Tags: []string{"matomo", "tag-manager", "containers", "create"},
	Risk: tagManagerChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema:           json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"created":{"type":"boolean"}},"required":["idSite","created"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container was created for"},
		{Name: "idContainer", Description: "ID of the new container when Matomo reports it; otherwise read " +
			"matomo.tag-manager-containers.list"},
		{Name: "created", Description: "True once Matomo accepted the request"},
	},
	Examples: []capability.Example{{Description: "Create the default container of website 1",
		Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var tagManagerContainersAdd = capability.Descriptor{
	ID: Provider + ".tag-manager-containers.add", Version: 1, Title: "Add a Matomo Tag Manager container",
	Description: "Create a Tag Manager container for a website (addContainer). Matomo creates the container with " +
		"its draft only: nothing is saved as a version and nothing is published. The context comes from " +
		"matomo.tag-manager-catalog.list kind contexts; needs the plugin TagManager and a write right; not " +
		"idempotent, since a repeated call creates a second container" + containerSentOnce,
	Tags: []string{"matomo", "tag-manager", "containers", "create"},
	Risk: tagManagerChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerContextSchema + `,` +
		containerNameSchema + `,` + containerDescSchema + `,` + containerFlagsSchema + `},` +
		`"required":["context","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"created":{"type":"boolean"}},"required":["idSite","idContainer","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "context", Required: true, Description: "Context ID from matomo.tag-manager-catalog.list kind " +
			"contexts, such as web, android, or ios; it cannot be changed later"},
		{Name: "name", Required: true, Description: "Name of the container, 1 to 50 characters"},
		{Name: "description", Description: "Description, up to 1000 characters; empty when omitted"}},
		containerFlagArguments(false)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "ID of the new container"},
		{Name: "created", Description: "True once Matomo created the container"},
	},
	Examples: []capability.Example{{Description: "Add a web container named Main to website 1",
		Arguments: json.RawMessage(`{"idSite":1,"context":"web","name":"Main","description":"Shop"}`)}},
}

var tagManagerContainersUpdate = capability.Descriptor{
	ID: Provider + ".tag-manager-containers.update", Version: 1, Title: "Update a Matomo Tag Manager container",
	Description: "Replace name, description, and the three settings of a Tag Manager container " +
		"(updateContainer); the context stays. Everything is replaced as given, so an empty description clears " +
		"it; read the container first with matomo.tag-manager-containers.get. The tool reads the container first " +
		"to bind it to the website. The settings ignoreGtmDataLayer and activelySyncGtmDataLayer act only after a " +
		"later publication of a version; nothing is published here. Needs the plugin TagManager, a write right, " +
		"and the right to publish; idempotent in effect" + containerSentOnce,
	Tags: []string{"matomo", "tag-manager", "containers", "update"},
	Risk: tagManagerChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		containerNameSchema + `,` + containerDescSchema + `,` + containerFlagsSchema + `},` +
		`"required":["idContainer","name","description","ignoreGtmDataLayer","isTagFireLimitAllowedInPreviewMode",` +
		`"activelySyncGtmDataLayer"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"updated":{"type":"boolean"}},"required":["idSite","idContainer","updated"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "idContainer", Required: true, Description: "Container ID from matomo.tag-manager-containers.list, " +
			"up to 8 letters and digits"},
		{Name: "name", Required: true, Description: "New name, 1 to 50 characters"},
		{Name: "description", Required: true, Description: "New description, up to 1000 characters; an empty text " +
			"clears it"}},
		containerFlagArguments(true)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container that was changed"},
		{Name: "updated", Description: "True once Matomo accepted the update"},
	},
	Examples: []capability.Example{{Description: "Rename container 6OMh6taM of website 1 and keep the data layer settings off",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","name":"Main","description":"",` +
			`"ignoreGtmDataLayer":false,"isTagFireLimitAllowedInPreviewMode":false,"activelySyncGtmDataLayer":false}`)}},
}

var tagManagerContainersDelete = capability.Descriptor{
	ID: Provider + ".tag-manager-containers.delete", Version: 1, Title: "Delete a Matomo Tag Manager container",
	Description: "Delete a Tag Manager container with all its versions, releases, tags, triggers, and variables " +
		"(deleteContainer); it cannot be undone. When the container was live, its container script disappears from " +
		"the website and its tags stop running. The tool reads the container first to bind it to the website. " +
		"Needs the plugin TagManager, a write right, and the right to publish; idempotent in effect; offered only " +
		"where a connection's tools list names it" + containerSentOnce,
	Tags: []string{"matomo", "tag-manager", "containers", "delete"},
	Risk: tagManagerChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `},` +
		`"required":["idContainer"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"deleted":{"type":"boolean"}},"required":["idSite","idContainer","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idContainer", Required: true, Description: "Container ID from matomo.tag-manager-containers.list, " +
			"up to 8 letters and digits"}},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belonged to"},
		{Name: "idContainer", Description: "The container that was deleted"},
		{Name: "deleted", Description: "True once Matomo accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM"}`)}},
}

// ContainerCreated is the answer of tag-manager-containers.create-default and .add.
type ContainerCreated struct {
	IDSite      int    `json:"idSite"`
	IDContainer string `json:"idContainer,omitempty"`
	Created     bool   `json:"created"`
}

// ContainerUpdated is the answer of tag-manager-containers.update.
type ContainerUpdated struct {
	IDSite      int    `json:"idSite"`
	IDContainer string `json:"idContainer"`
	Updated     bool   `json:"updated"`
}

// ContainerDeleted is the answer of tag-manager-containers.delete.
type ContainerDeleted struct {
	IDSite      int    `json:"idSite"`
	IDContainer string `json:"idContainer"`
	Deleted     bool   `json:"deleted"`
}

func containerFlagValue(given *bool, name string, required bool) (string, error) {
	if given == nil {
		if required {
			return "", invalidArgument(name + " is required; the update replaces every setting")
		}
		return "0", nil
	}
	return flagValue(given), nil
}

// containerSettings validates the arguments the add and update tools share and returns them as form fields.
func containerSettings(name, description *string, flags [3]*bool, required bool) (url.Values, error) {
	checkedName, err := versionNameOf(name)
	if err != nil {
		return nil, err
	}
	checkedDescription, err := versionDescriptionOf(description, required)
	if err != nil {
		return nil, err
	}
	fields := url.Values{"name": {checkedName}, "description": {checkedDescription}}
	for i, key := range [3]string{"ignoreGtmDataLayer", "isTagFireLimitAllowedInPreviewMode", "activelySyncGtmDataLayer"} {
		value, err := containerFlagValue(flags[i], key, required)
		if err != nil {
			return nil, err
		}
		fields.Set(key, value)
	}
	return fields, nil
}

// containerProof reads the container once to prove that it belongs to the website. The write methods take it by
// ID, and no change may reach a container the website does not hold.
func containerProof(ctx context.Context, client *Client, op, tool string, site int, container string) error {
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idContainer": {container}}
	if err := client.call(ctx, op, tagManagerRequirement(tool), "TagManager.getContainer", params, &answer); err != nil {
		return err
	}
	var row containerJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &row) != nil || row.ID == nil {
		return provider.InvalidResponse(op, "Matomo returned an invalid container; nothing was changed")
	}
	if found, ok := row.container(site); !ok || found.ID != container {
		return provider.InvalidResponse(op, "Matomo returned another container than asked for; nothing was changed")
	}
	return nil
}

func invokeTagManagerContainersCreateDefault(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create default Tag Manager container"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tagManagerContainersCreateDefault.ID),
		"TagManager.createDefaultContainerForSite", url.Values{"idSite": {strconv.Itoa(id)}}, containerSentOnce,
		&answer); err != nil {
		return nil, err
	}
	// Matomo answers with the new container ID or with no value; any other answer is no confirmation.
	var created string
	switch {
	case scalar(answer, &created) == nil && containerIDPattern.MatchString(created):
		return &ContainerCreated{IDSite: id, IDContainer: created, Created: true}, nil
	case isSuccessAnswer(answer):
		return &ContainerCreated{IDSite: id, Created: true}, nil
	}
	return nil, unclearChange(op, "Matomo returned no confirmation of the creation", containerSentOnce)
}

func invokeTagManagerContainersAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add Tag Manager container"
	var args struct {
		IDSite       *int    `json:"idSite"`
		Context      *string `json:"context"`
		Name         *string `json:"name"`
		Description  *string `json:"description"`
		IgnoreGTM    *bool   `json:"ignoreGtmDataLayer"`
		FireLimit    *bool   `json:"isTagFireLimitAllowedInPreviewMode"`
		ActivelySync *bool   `json:"activelySyncGtmDataLayer"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.Context == nil || !containerContextPattern.MatchString(*args.Context) {
		return nil, invalidArgument("context must be a context ID of lower-case letters, digits, hyphens, and underscores")
	}
	fields, err := containerSettings(args.Name, args.Description, [3]*bool{args.IgnoreGTM, args.FireLimit, args.ActivelySync}, false)
	if err != nil {
		return nil, err
	}
	fields.Set("idSite", strconv.Itoa(id))
	fields.Set("context", *args.Context)
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tagManagerContainersAdd.ID), "TagManager.addContainer",
		fields, containerSentOnce, &answer); err != nil {
		return nil, err
	}
	var created string
	if scalar(answer, &created) != nil || !containerIDPattern.MatchString(created) {
		return nil, unclearChange(op, "Matomo returned no usable ID for the new container", containerSentOnce)
	}
	return &ContainerCreated{IDSite: id, IDContainer: created, Created: true}, nil
}

func invokeTagManagerContainersUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update Tag Manager container"
	var args struct {
		IDSite       *int    `json:"idSite"`
		IDContainer  *string `json:"idContainer"`
		Name         *string `json:"name"`
		Description  *string `json:"description"`
		IgnoreGTM    *bool   `json:"ignoreGtmDataLayer"`
		FireLimit    *bool   `json:"isTagFireLimitAllowedInPreviewMode"`
		ActivelySync *bool   `json:"activelySyncGtmDataLayer"`
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
	fields, err := containerSettings(args.Name, args.Description, [3]*bool{args.IgnoreGTM, args.FireLimit, args.ActivelySync}, true)
	if err != nil {
		return nil, err
	}
	fields.Set("idSite", strconv.Itoa(id))
	fields.Set("idContainer", containerID)
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	tool := tagManagerContainersUpdate.ID
	if err := containerProof(ctx, client, op, tool, id, containerID); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tool), "TagManager.updateContainer", fields,
		containerSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", containerSentOnce)
	}
	return &ContainerUpdated{IDSite: id, IDContainer: containerID, Updated: true}, nil
}

func invokeTagManagerContainersDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete Tag Manager container"
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
	tool := tagManagerContainersDelete.ID
	if err := containerProof(ctx, client, op, tool, id, containerID); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID}}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tool), "TagManager.deleteContainer", params,
		containerSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", containerSentOnce)
	}
	return &ContainerDeleted{IDSite: id, IDContainer: containerID, Deleted: true}, nil
}
