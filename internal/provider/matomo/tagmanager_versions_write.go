package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// Matomo checks the name of a version at 50 characters in the API methods (the stored field allows 255) and
	// the description at 1000.
	maxVersionName        = 50
	maxVersionDescription = 1000
)

var versionSentOnce = sentOnceFor("version", "matomo.tag-manager-versions.list")

const (
	versionNameSchema        = `"name":{"type":"string","minLength":1,"maxLength":50}`
	versionDescriptionSchema = `"description":{"type":"string","maxLength":1000}`
)

func tagManagerChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: tagManagerSensitive}
}

func versionChangeArguments(extra ...capability.Argument) []capability.Argument {
	return append([]capability.Argument{idSiteArgument(),
		{Name: "idContainer", Required: true, Description: "Container ID from matomo.tag-manager-containers.list, " +
			"up to 8 letters and digits"}}, extra...)
}

var tagManagerVersionsCreate = capability.Descriptor{
	ID: Provider + ".tag-manager-versions.create", Version: 1, Title: "Create a Matomo Tag Manager container version",
	Description: "Save a snapshot of the draft of a Tag Manager container as a new version, or a copy of one saved " +
		"version when idContainerVersion is given; the draft stays as it is and nothing is published to any " +
		"environment. The name must be new within the container; the container and the version must belong to the " +
		"website; needs the plugin TagManager and a write right; not idempotent, since a repeated call with another " +
		"name creates a second version; sent once and never repeated",
	Tags: []string{"matomo", "tag-manager", "versions", "create"},
	Risk: tagManagerChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		versionNameSchema + `,` + versionDescriptionSchema + `,` + versionIDSchema + `},` +
		`"required":["idContainer","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"idContainerVersion":{"type":"integer"},"created":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","created"],"additionalProperties":false}`),
	Arguments: versionChangeArguments(
		capability.Argument{Name: "name", Required: true, Description: "Name of the new version, 1 to 50 characters"},
		capability.Argument{Name: "description", Description: "Description of the new version, up to 1000 characters; " +
			"empty when omitted"},
		capability.Argument{Name: "idContainerVersion", Description: "Version ID from matomo.tag-manager-versions.list " +
			"to copy; the draft is saved when omitted"}),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container the version was created in"},
		{Name: "idContainerVersion", Description: "ID of the new version"},
		{Name: "created", Description: "True once Matomo created the version"},
	},
	Examples: []capability.Example{{Description: "Save the draft of container 6OMh6taM of website 1 as version v2",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","name":"v2","description":"before the sale"}`)}},
}

var tagManagerVersionsUpdate = capability.Descriptor{
	ID: Provider + ".tag-manager-versions.update", Version: 1, Title: "Update a Matomo Tag Manager container version",
	Description: "Rename a version of a Tag Manager container and replace its description, the draft included; the " +
		"tags, triggers, and variables of the version stay as they are and nothing is published. The description " +
		"is replaced as given, so an empty one clears it. The name must be new within the container; the version " +
		"must belong to the container and the website; needs the plugin TagManager and a write right; idempotent " +
		"in effect, sent once and never repeated",
	Tags: []string{"matomo", "tag-manager", "versions", "update"},
	Risk: tagManagerChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		versionIDSchema + `,` + versionNameSchema + `,` + versionDescriptionSchema + `},` +
		`"required":["idContainer","idContainerVersion","name","description"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"idContainerVersion":{"type":"integer"},"updated":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","updated"],"additionalProperties":false}`),
	Arguments: versionChangeArguments(
		capability.Argument{Name: "idContainerVersion", Required: true, Description: "Version ID from " +
			"matomo.tag-manager-versions.list, or the draft's ID from matomo.tag-manager-containers.get"},
		capability.Argument{Name: "name", Required: true, Description: "New name, 1 to 50 characters"},
		capability.Argument{Name: "description", Required: true, Description: "New description, up to 1000 characters; " +
			"an empty text clears it"}),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container the version belongs to"},
		{Name: "idContainerVersion", Description: "ID of the updated version"},
		{Name: "updated", Description: "True once Matomo updated the version"},
	},
	Examples: []capability.Example{{Description: "Rename version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"name":"v1 final","description":""}`)}},
}

var tagManagerVersionsDelete = capability.Descriptor{
	ID: Provider + ".tag-manager-versions.delete", Version: 1, Title: "Delete a Matomo Tag Manager container version",
	Description: "Delete one saved version of a Tag Manager container; it cannot be undone. Matomo refuses the " +
		"draft and a version that is released to an environment, so nothing live is removed; the version must " +
		"belong to the container and the website; needs the plugin TagManager and a write right; idempotent in " +
		"effect, sent once and never repeated; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "tag-manager", "versions", "delete"},
	Risk: tagManagerChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		versionIDSchema + `},"required":["idContainer","idContainerVersion"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"idContainer":{"type":"string"},"idContainerVersion":{"type":"integer"},"deleted":{"type":"boolean"}},` +
		`"required":["idSite","idContainer","idContainerVersion","deleted"],"additionalProperties":false}`),
	Arguments: versionChangeArguments(
		capability.Argument{Name: "idContainerVersion", Required: true, Description: "Version ID from " +
			"matomo.tag-manager-versions.list"}),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the container belongs to"},
		{Name: "idContainer", Description: "The container the version belonged to"},
		{Name: "idContainerVersion", Description: "ID of the deleted version"},
		{Name: "deleted", Description: "True once Matomo deleted the version"},
	},
	Examples: []capability.Example{{Description: "Delete version 7 of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`)}},
}

// VersionCreated is the answer of tag-manager-versions.create.
type VersionCreated struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	Created            bool   `json:"created"`
}

// VersionUpdated is the answer of tag-manager-versions.update.
type VersionUpdated struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	Updated            bool   `json:"updated"`
}

// VersionDeleted is the answer of tag-manager-versions.delete.
type VersionDeleted struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	Deleted            bool   `json:"deleted"`
}

func versionNameOf(given *string) (string, error) {
	if given == nil || strings.TrimSpace(*given) == "" || utf8.RuneCountInString(*given) > maxVersionName {
		return "", invalidArgument("name must be 1 to 50 characters")
	}
	return *given, nil
}

func versionDescriptionOf(given *string, required bool) (string, error) {
	if given == nil {
		if required {
			return "", invalidArgument("description is required; an empty text clears it")
		}
		return "", nil
	}
	if utf8.RuneCountInString(*given) > maxVersionDescription {
		return "", invalidArgument("description must be at most 1000 characters")
	}
	return *given, nil
}

// The write methods of the plugin pair the container with the website, and the version with both, before they
// change anything (checkContainerExists, checkContainerVersionExists), so no read comes first.
func invokeTagManagerVersionsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create Tag Manager container version"
	var args struct {
		IDSite             *int    `json:"idSite"`
		IDContainer        *string `json:"idContainer"`
		Name               *string `json:"name"`
		Description        *string `json:"description"`
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
	name, err := versionNameOf(args.Name)
	if err != nil {
		return nil, err
	}
	description, err := versionDescriptionOf(args.Description, false)
	if err != nil {
		return nil, err
	}
	source, err := versionIDOf(args.IDContainerVersion, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID}, "name": {name},
		"description": {description}}
	if source != 0 {
		params.Set("idContainerVersion", strconv.Itoa(source))
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tagManagerVersionsCreate.ID),
		"TagManager.createContainerVersion", params, versionSentOnce, &answer); err != nil {
		return nil, err
	}
	var created int
	if scalar(answer, &created) != nil || created < 1 {
		return nil, unclearChange(op, "Matomo returned no usable ID for the new version", versionSentOnce)
	}
	return &VersionCreated{IDSite: id, IDContainer: containerID, IDContainerVersion: created, Created: true}, nil
}

func invokeTagManagerVersionsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update Tag Manager container version"
	var args struct {
		IDSite             *int    `json:"idSite"`
		IDContainer        *string `json:"idContainer"`
		IDContainerVersion *int    `json:"idContainerVersion"`
		Name               *string `json:"name"`
		Description        *string `json:"description"`
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
	name, err := versionNameOf(args.Name)
	if err != nil {
		return nil, err
	}
	description, err := versionDescriptionOf(args.Description, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID},
		"idContainerVersion": {strconv.Itoa(versionID)}, "name": {name}, "description": {description}}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tagManagerVersionsUpdate.ID),
		"TagManager.updateContainerVersion", params, versionSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", versionSentOnce)
	}
	return &VersionUpdated{IDSite: id, IDContainer: containerID, IDContainerVersion: versionID, Updated: true}, nil
}

func invokeTagManagerVersionsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete Tag Manager container version"
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
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idContainer": {containerID},
		"idContainerVersion": {strconv.Itoa(versionID)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, tagManagerRequirement(tagManagerVersionsDelete.ID),
		"TagManager.deleteContainerVersion", params, versionSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", versionSentOnce)
	}
	return &VersionDeleted{IDSite: id, IDContainer: containerID, IDContainerVersion: versionID, Deleted: true}, nil
}
