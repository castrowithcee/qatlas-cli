package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const tagOpSuffix = " Tag Manager tag"

var tagSentOnce = sentOnceFor("tag", "matomo.tag-manager-tags.list")

const tagIDSchema = `"idTag":{"type":"integer","minimum":1,"maximum":2147483647}`

var tagDraftFields = []capability.Field{
	{Name: "idSite", Description: "The website the container belongs to"},
	{Name: "idContainer", Description: "The container the tag belongs to"},
	{Name: "idContainerVersion", Description: "ID of the draft that was changed, as Matomo reported it before the change"},
}

var tagIDArgument = capability.Argument{Name: "idTag", Required: true,
	Description: "Tag ID from matomo.tag-manager-tags.list of the draft"}

var tagManagerTagsDelete = capability.Descriptor{
	ID: Provider + ".tag-manager-tags.delete", Version: 1, Title: "Delete a Matomo Tag Manager tag",
	Description: "Delete one tag from the draft of a Tag Manager container (deleteContainerTag); it cannot be " +
		"undone and removes a data path. Only the draft changes: the tool reads the container first, takes the " +
		"draft from it, and never accepts a version, so nothing live changes before a release. A custom template " +
		"tag needs the right to use custom templates; needs the plugin TagManager and a write right; idempotent " +
		"in effect; offered only where a connection's tools list names it" + tagSentOnce,
	Tags: []string{"matomo", "tag-manager", "tags", "delete"},
	Risk: tagManagerChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		tagIDSchema + `},"required":["idContainer","idTag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idTag":{"type":"integer"},` +
		`"deleted":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idTag","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: triggerChangeArguments(tagIDArgument),
	Fields: append(append([]capability.Field{}, tagDraftFields...),
		capability.Field{Name: "idTag", Description: "ID of the tag that was sent"},
		capability.Field{Name: "deleted", Description: "True once Matomo accepted the deletion"}),
	Examples: []capability.Example{{Description: "Delete tag 4 from the draft of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idTag":4}`)}},
}

var tagManagerTagsPause = capability.Descriptor{
	ID: Provider + ".tag-manager-tags.pause", Version: 1, Title: "Pause a Matomo Tag Manager tag",
	Description: "Pause one tag in the draft of a Tag Manager container (pauseContainerTag). Only the draft " +
		"changes: the tool reads the container first, takes the draft from it, and never accepts a version; the " +
		"pause takes effect live only after a release of the draft. A custom template tag needs the right to use " +
		"custom templates; needs the plugin TagManager and a write right; idempotent in effect; offered only " +
		"where a connection's tools list names it" + tagSentOnce,
	Tags: []string{"matomo", "tag-manager", "tags", "update"},
	Risk: tagManagerChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + containerIDSchema + `,` +
		tagIDSchema + `},"required":["idContainer","idTag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + versionScopeProperties + `,"idTag":{"type":"integer"},` +
		`"paused":{"type":"boolean"}},"required":["idSite","idContainer","idContainerVersion","idTag","paused"],` +
		`"additionalProperties":false}`),
	Arguments: triggerChangeArguments(tagIDArgument),
	Fields: append(append([]capability.Field{}, tagDraftFields...),
		capability.Field{Name: "idTag", Description: "ID of the tag that was sent"},
		capability.Field{Name: "paused", Description: "True once Matomo accepted the pause"}),
	Examples: []capability.Example{{Description: "Pause tag 4 in the draft of container 6OMh6taM of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idContainer":"6OMh6taM","idTag":4}`)}},
}

// TagDeleted is the answer of tag-manager-tags.delete.
type TagDeleted struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDTag              int    `json:"idTag"`
	Deleted            bool   `json:"deleted"`
}

// TagPaused is the answer of tag-manager-tags.pause.
type TagPaused struct {
	IDSite             int    `json:"idSite"`
	IDContainer        string `json:"idContainer"`
	IDContainerVersion int    `json:"idContainerVersion"`
	IDTag              int    `json:"idTag"`
	Paused             bool   `json:"paused"`
}

func tagIDOf(given *int) (int, error) {
	if given == nil || *given < 1 || *given > 2147483647 {
		return 0, invalidArgument("idTag must be a tag ID between 1 and 2147483647")
	}
	return *given, nil
}

// tagChange validates the shared arguments of both tools and sends the one request into the draft.
func tagChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, op, tool, method string) (site int, container string, draft, tag int, answer json.RawMessage, err error) {
	var args struct {
		IDSite      *int    `json:"idSite"`
		IDContainer *string `json:"idContainer"`
		IDTag       *int    `json:"idTag"`
	}
	if err = decodeArguments(raw, &args); err != nil {
		return
	}
	if site, err = selectSite(resolved, args.IDSite); err != nil {
		return
	}
	if container, err = containerIDOf(args.IDContainer); err != nil {
		return
	}
	if tag, err = tagIDOf(args.IDTag); err != nil {
		return
	}
	fields := url.Values{"idTag": {strconv.Itoa(tag)}}
	draft, answer, err = triggerChange(ctx, resolved, secrets, red, op, tool, method, site, container, fields, tagSentOnce)
	return
}

func invokeTagManagerTagsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete" + tagOpSuffix
	site, container, draft, tag, answer, err := tagChange(ctx, resolved, secrets, red, raw, op,
		tagManagerTagsDelete.ID, "TagManager.deleteContainerTag")
	if err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", tagSentOnce)
	}
	return &TagDeleted{IDSite: site, IDContainer: container, IDContainerVersion: draft, IDTag: tag, Deleted: true}, nil
}

func invokeTagManagerTagsPause(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "pause" + tagOpSuffix
	site, container, draft, tag, answer, err := tagChange(ctx, resolved, secrets, red, raw, op,
		tagManagerTagsPause.ID, "TagManager.pauseContainerTag")
	if err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the pause", tagSentOnce)
	}
	return &TagPaused{IDSite: site, IDContainer: container, IDContainerVersion: draft, IDTag: tag, Paused: true}, nil
}
