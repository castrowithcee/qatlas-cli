package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// System tags of Nextcloud (apps/dav SystemTag): the catalog is the collection remote.php/dav/systemtags,
// a tag is its child <tag id>, and the tags of one file are the children of
// remote.php/dav/systemtags-relations/files/<file id>. A PUT on <file id>/<tag id> assigns a tag and a DELETE
// removes it. The file ID comes from a stat of the requested path, never from the caller, so only files
// below the connection root are addressable. Only visible tags are ever reported or touched.
var (
	systemTagsRoot   = []string{"remote.php", "dav", "systemtags"}
	systemTagsRelOff = []string{"remote.php", "dav", "systemtags-relations", "files"}
)

const (
	// maxTagNodes bounds the nodes the parser accepts for one catalog or relation read before it is refused.
	maxTagNodes = 5000
	// maxTags bounds one listing; a cut is reported.
	maxTags     = 200
	maxTagIDLen = 20
	tagIDSchema = `{"type":"string","minLength":1,"maxLength":20,"pattern":"^[0-9]+$","x-form":"a tag_id of systemtags.list"}`

	uncertainTagAdded   = "; the tag may have been assigned, check filetags.list before repeating"
	uncertainTagRemoved = "; the tag may have been removed, check filetags.list before repeating"

	messageTagMissing    = "this Nextcloud instance holds no such visible system tag"
	messageTagNotAllowed = "this Nextcloud identity may not assign this system tag"
	messageTagAssigned   = "Nextcloud refused the assignment; the tag may already be assigned to this file"
	messageTagNotOnFile  = "this system tag is not assigned to this file or the file is gone"
)

// tagPropfindBody asks for exactly the properties a system tag is built from.
const tagPropfindBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>` +
	`<d:resourcetype/><oc:id/><oc:display-name/><oc:user-visible/><oc:user-assignable/><oc:can-assign/>` +
	`</d:prop></d:propfind>`

const tagSchema = `{"type":"object","properties":{"tag_id":{"type":"string"},"name":{"type":"string"},` +
	`"assignable":{"type":"boolean"}},"required":["tag_id","name","assignable"],"additionalProperties":false}`

var tagFields = []capability.Field{
	{Name: "tags", Description: "Visible system tags, untrusted data, with tag_id, name, and assignable"},
	{Name: "tag_id", Description: "Identifier of the tag; pass it to filetags.add or filetags.remove"},
	{Name: "name", Description: "Name of the tag"},
	{Name: "assignable", Description: "True when this identity may assign the tag to a file"},
	{Name: "count", Description: "Number of reported tags"},
	{Name: "truncated", Description: "True when more tags exist than reported"},
}

var tagPathArgument = capability.Argument{Name: "path", Required: true,
	Description: "File or folder relative to the fixed root folder of this connection; never the root itself"}

var tagIDArgument = capability.Argument{Name: "tag_id", Required: true,
	Description: "System tag, a tag_id reported by systemtags.list or filetags.list"}

var systemtagsList = capability.Descriptor{
	ID: Provider + ".systemtags.list", Version: 1, Title: "List Nextcloud system tags",
	Description: "List the visible system tags of the Nextcloud instance, at most 200; the catalog is shared by the " +
		"whole instance and not limited to a root folder",
	Tags: []string{"nextcloud", "systemtags", "tags", "webdav", "list"}, Risk: nextcloudReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"tags":{"type":"array","items":` + tagSchema + `},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["tags","count","truncated"],"additionalProperties":false}`),
	Fields:   tagFields,
	Examples: []capability.Example{{Description: "List the visible system tags", Arguments: json.RawMessage(`{}`)}},
}

var filesTagsList = capability.Descriptor{
	ID: Provider + ".filetags.list", Version: 1, Title: "List the system tags of a Nextcloud file",
	Description: "List the visible system tags assigned to one file or folder below the fixed root of a connection, " +
		"at most 200; file content is never read",
	Tags: []string{"nextcloud", "files", "systemtags", "tags", "webdav", "list"}, Risk: nextcloudReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `},"required":["path"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"tags":{"type":"array","items":` + tagSchema + `},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["path","tags","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{tagPathArgument},
	Fields:    append([]capability.Field{{Name: "path", Description: "Path whose tags were listed"}}, tagFields...),
	Examples:  []capability.Example{{Description: "List the tags of one file", Arguments: json.RawMessage(`{"path":"Reports/q1.pdf"}`)}},
}

func tagChange(action, done, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".filetags." + action, Version: 1, Title: title, Description: description,
		Tags: []string{"nextcloud", "files", "systemtags", "tags", "webdav", action}, Provider: Provider,
		Risk: organiseRisk(capability.EffectUpdate),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `,"tag_id":` + tagIDSchema + `},` +
			`"required":["path","tag_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + done + `":{"type":"boolean"},"path":{"type":"string"},` +
			`"tag_id":{"type":"string"}},"required":["` + done + `"],"additionalProperties":false}`),
		Arguments: []capability.Argument{tagPathArgument, tagIDArgument},
		Fields: []capability.Field{
			{Name: done, Description: "True when Nextcloud applied the change"},
			{Name: "path", Description: "Path of the file or folder"},
			{Name: "tag_id", Description: "The system tag"},
		},
		Examples: []capability.Example{{Description: "Use a tag_id from systemtags.list",
			Arguments: json.RawMessage(`{"path":"Reports/q1.pdf","tag_id":"7"}`)}},
	}
}

var filesTagsAdd = tagChange("add", "added", "Assign a Nextcloud system tag",
	"Assign one existing visible system tag to one file or folder below the fixed root of a connection; the tag "+
		"must be assignable by this identity. One read-only pre-check (stat of the path and read of the tag) "+
		"precedes exactly one change request; a tag already assigned is refused")

var filesTagsRemove = tagChange("remove", "removed", "Remove a Nextcloud system tag from a file",
	"Remove one visible system tag from one file or folder below the fixed root of a connection, if this identity "+
		"may assign the tag; the tag itself is never deleted. One read-only pre-check (stat of the path and read "+
		"of the tag) precedes exactly one change request")

// SystemTag is the stable Qatlas view of one visible system tag.
type SystemTag struct {
	TagID      string `json:"tag_id"`
	Name       string `json:"name"`
	Assignable bool   `json:"assignable"`
}

// SystemTagList is the listing of visible system tags, of the instance or of one file.
type SystemTagList struct {
	Path      string      `json:"path,omitempty"`
	Tags      []SystemTag `json:"tags"`
	Count     int         `json:"count"`
	Truncated bool        `json:"truncated"`
}

type tagArguments struct {
	Path  string `json:"path"`
	TagID string `json:"tag_id"`
}

func validTagID(value string) bool {
	return value != "" && len(value) <= maxTagIDLen && digitsOnly(value)
}

// readTagArguments decodes strictly, so an argument the schema does not know, such as a file ID, is refused
// here as well. The path and the tag ID are checked before any credential access.
func readTagArguments(op string, raw json.RawMessage, needTag bool) (tagArguments, []string, error) {
	var input tagArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, nil, providerError(op, "the validated arguments could not be read")
	}
	rel, err := splitRelative(input.Path)
	if err != nil || len(rel) == 0 {
		return input, nil, providerError(op, "a path below the connection root is required")
	}
	if needTag && !validTagID(input.TagID) {
		return input, nil, providerError(op, "the tag ID must be a tag_id reported by "+systemtagsList.ID)
	}
	return input, rel, nil
}

func invokeSystemTagsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list system tags"
	if len(bytes.TrimSpace(raw)) > 0 {
		var none struct{}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&none); err != nil {
			return nil, providerError(op, "the validated arguments could not be read")
		}
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.ListSystemTags(ctx)
}

func invokeFilesTagsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list file tags"
	input, rel, err := readTagArguments(op, raw, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListFileTags(ctx, input.Path, rel)
}

func invokeFilesTagsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add file tag"
	input, rel, err := readTagArguments(op, raw, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.changeFileTag(ctx, op, http.MethodPut, "added", input, rel)
}

func invokeFilesTagsRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "remove file tag"
	input, rel, err := readTagArguments(op, raw, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.changeFileTag(ctx, op, http.MethodDelete, "removed", input, rel)
}

// tagsPrefix are the decoded segments of the system tag catalog of this instance.
func (c *Client) tagsPrefix() []string {
	return append(append([]string{}, c.install...), systemTagsRoot...)
}

// tagRelationBound are the decoded segments of the relations of one file.
func (c *Client) tagRelationBound(fileID string) []string {
	return append(append(append([]string{}, c.install...), systemTagsRelOff...), fileID)
}

func (c *Client) tagURL(id ...string) string {
	return c.origin + escapePath(append(c.tagsPrefix(), id...))
}

func (c *Client) tagRelationURL(fileID string, tagID ...string) string {
	return c.origin + escapePath(append(c.tagRelationBound(fileID), tagID...))
}

// tagOf reads one answered tag node. visible is false for a tag this adapter must neither report nor touch,
// which includes a node whose own identifier disagrees with its location.
func tagOf(id string, res *resource) (tag SystemTag, visible bool) {
	if res.props[propTagID] != id || res.props[propTagVisible] != "true" {
		return SystemTag{}, false
	}
	return SystemTag{TagID: id, Name: bounded(res.props[propTagName]),
		Assignable: res.props[propTagAssignable] == "true" && res.props[propTagCanAssign] == "true"}, true
}

// tagsBelow turns the children of an answered collection into visible tags.
func (c *Client) tagsBelow(op string, resources []resource, bound []string) (*SystemTagList, error) {
	tags := []SystemTag{}
	for i := range resources {
		below, err := c.segmentsBelow(op, resources[i].href, bound)
		if err != nil {
			return nil, err
		}
		switch len(below) {
		case 0:
			// The collection itself.
		case 1:
			if !validTagID(below[0]) || resources[i].collection || resources[i].failure(op) != nil {
				continue
			}
			if tag, visible := tagOf(below[0], &resources[i]); visible {
				tags = append(tags, tag)
			}
		default:
			return nil, invalidResponse(op, messageForeignEntry)
		}
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Name != tags[j].Name {
			return tags[i].Name < tags[j].Name
		}
		return tags[i].TagID < tags[j].TagID
	})
	truncated := len(tags) > maxTags
	if truncated {
		tags = tags[:maxTags]
	}
	return &SystemTagList{Tags: tags, Count: len(tags), Truncated: truncated}, nil
}

// ListSystemTags reads the catalog with a single PROPFIND of depth 1.
func (c *Client) ListSystemTags(ctx context.Context) (*SystemTagList, error) {
	const op = "list system tags"
	resources, err := c.propfindAt(ctx, op, c.tagURL(), tagPropfindBody, depthChildren, false, maxTagNodes)
	if err != nil {
		return nil, err
	}
	return c.tagsBelow(op, resources, c.tagsPrefix())
}

// taggedEntry stats the requested path, which may be a file or a folder but never the root, and returns its
// file ID, the only way its relations are addressed.
func (c *Client) taggedEntry(ctx context.Context, op string, rel []string) (*Entry, error) {
	entry, err := c.stat(ctx, op, rel, false)
	if err != nil {
		return nil, err
	}
	if entry.FileID == "" {
		return nil, providerError(op, "Nextcloud reported no file ID for this path")
	}
	return entry, nil
}

// ListFileTags reads the relations of one path with a single PROPFIND of depth 1.
func (c *Client) ListFileTags(ctx context.Context, path string, rel []string) (*SystemTagList, error) {
	const op = "list file tags"
	entry, err := c.taggedEntry(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	resources, err := c.propfindAt(ctx, op, c.tagRelationURL(entry.FileID), tagPropfindBody, depthChildren, false, maxTagNodes)
	if err != nil {
		return nil, err
	}
	list, err := c.tagsBelow(op, resources, c.tagRelationBound(entry.FileID))
	if err != nil {
		return nil, err
	}
	list.Path = path
	return list, nil
}

// fetchTag reads one tag with a single PROPFIND of depth 0. A tag that is invisible is answered like a
// missing one.
func (c *Client) fetchTag(ctx context.Context, op, id string) (SystemTag, error) {
	resources, err := c.propfindAt(ctx, op, c.tagURL(id), tagPropfindBody, depthSelf, false, maxTagNodes)
	if err != nil {
		if isNotFound(err) {
			return SystemTag{}, providerError(op, messageTagMissing)
		}
		return SystemTag{}, err
	}
	if len(resources) != 1 {
		return SystemTag{}, invalidResponse(op, "Nextcloud answered with more than the requested node")
	}
	below, err := c.segmentsBelow(op, resources[0].href, c.tagsPrefix())
	if err != nil {
		return SystemTag{}, err
	}
	if len(below) != 1 || below[0] != id {
		return SystemTag{}, invalidResponse(op, messageForeignEntry)
	}
	if err := resources[0].failure(op); err != nil {
		return SystemTag{}, err
	}
	tag, visible := tagOf(id, &resources[0])
	if !visible {
		return SystemTag{}, providerError(op, messageTagMissing)
	}
	return tag, nil
}

// readTag reads one tag before it is assigned or removed; one this identity may not assign is refused.
func (c *Client) readTag(ctx context.Context, op, id string) error {
	tag, err := c.fetchTag(ctx, op, id)
	if err != nil {
		return err
	}
	if !tag.Assignable {
		return providerError(op, messageTagNotAllowed)
	}
	return nil
}

// changeFileTag stats the path, reads the tag once, and then sends exactly one PUT (assign) or DELETE
// (remove) of the relation. The request is never repeated, and an unclear outcome is reported as such. A
// removal reads the tag too: Nextcloud refuses it for a tag the identity may not assign, so the same check
// refuses it locally and keeps both mutations on one path.
func (c *Client) changeFileTag(ctx context.Context, op, method, done string, input tagArguments, rel []string) (any, error) {
	entry, err := c.taggedEntry(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	if err := c.readTag(ctx, op, input.TagID); err != nil {
		return nil, err
	}
	hint := uncertainTagRemoved
	if method == http.MethodPut {
		hint = uncertainTagAdded
	}
	response, err := c.webdavTo(ctx, op, method, c.tagRelationURL(entry.FileID, input.TagID), nil, "", "", hint, nil)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			switch {
			case method == http.MethodPut && providerErr.Message == statusError(op, http.StatusConflict).(*provider.Error).Message:
				return nil, providerError(op, messageTagAssigned)
			case method == http.MethodDelete && providerErr.Message == messageNotFound:
				return nil, providerError(op, messageTagNotOnFile)
			}
		}
		return nil, err
	}
	response.Body.Close()
	return map[string]any{done: true, "path": input.Path, "tag_id": input.TagID}, nil
}
