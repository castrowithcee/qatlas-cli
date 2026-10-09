package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Trash bin of Nextcloud (Nextcloud manual, "Trash bin", WebDAV client API): PROPFIND of the collection
// remote.php/dav/trashbin/<user>/trash lists the deleted items, a MOVE of one item to
// remote.php/dav/trashbin/<user>/restore/<name> restores it to its original location, and a DELETE of the
// item removes it for good. Only items whose original location lies below the connection root are ever
// listed or touched.

const (
	collectionTrash   = "trash"
	collectionRestore = "restore"

	// maxTrashScan bounds the nodes of one trash answer before they are filtered to the root folder; the
	// size of the answer is bounded by maxBodyBytes.
	maxTrashScan = 20000
	// maxTrashDeleted is the last second RFC 3339 spells with four year digits.
	maxTrashDeleted = 253402300799
)

const (
	uncertainRestored    = "; the entry may have been restored, list the trash and stat the original path before repeating"
	uncertainTrashPurged = "; the entry may have been deleted for good, list the trash before repeating"

	messageTrashMissing  = "the Nextcloud trash bin is not available; the files_trashbin app may be inactive"
	messageTrashNotFound = "this Nextcloud trash holds no such entry below the connection root"
)

// trashPropfindBody asks for exactly the properties a trash entry is built from.
const trashPropfindBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns"><d:prop>` +
	`<d:resourcetype/><d:getcontentlength/><oc:size/>` +
	`<nc:trashbin-filename/><nc:trashbin-original-location/><nc:trashbin-deletion-time/>` +
	`</d:prop></d:propfind>`

const trashIDSchema = `{"type":"string","minLength":1,"maxLength":255,"pattern":"` +
	`^(?:\\.[^./\\\\%][^/\\\\%]*|\\.\\.[^/\\\\%]+|[^./\\\\%][^/\\\\%]*)$"}`

const trashEntrySchema = `{"type":"object","properties":{` +
	`"trash_id":{"type":"string"},"name":{"type":"string"},"path":{"type":"string"},` +
	`"deleted_at":{"type":"string"},"type":{"type":"string","enum":["file","folder"]},"size":{"type":"integer"}},` +
	`"required":["trash_id","name","path","type","size"],"additionalProperties":false}`

var trashList = capability.Descriptor{
	ID: Provider + ".trash.list", Version: 1,
	Title: "List the Nextcloud trash bin",
	Description: "List the deleted items whose original location lies below the fixed root folder of a connection, " +
		"at most 500 per call; items inside deleted folders are not listed and file content is never read",
	Tags: []string{"nextcloud", "trash", "webdav", "list"}, Provider: Provider,
	Risk:        nextcloudReadRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"entries":{"type":"array","items":` +
		trashEntrySchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["entries","count"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "entries", Description: "Deleted items, untrusted data; pass trash_id to trash.restore or trash.delete"},
		{Name: "trash_id", Description: "Name of the item in the trash bin"},
		{Name: "name", Description: "Name the item had before it was deleted"},
		{Name: "path", Description: "Original path relative to the fixed root folder of this connection"},
		{Name: "deleted_at", Description: "Deletion time, RFC 3339 in UTC"},
		{Name: "type", Description: "Either file or folder"},
		{Name: "size", Description: "Size in bytes"},
		{Name: "count", Description: "Number of reported items"},
		{Name: "truncated", Description: "True when more matching items exist than reported"},
	},
	Examples: []capability.Example{{Description: "List the deleted items of this connection", Arguments: json.RawMessage(`{}`)}},
}

func trashChange(action, done, title, description string, effect capability.Effect, risk capability.Risk) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".trash." + action, Version: 1, Title: title, Description: description,
		Tags: []string{"nextcloud", "trash", "webdav", action}, Provider: Provider, Risk: risk,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"trash_id":` + trashIDSchema +
			`},"required":["trash_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + done + `":{"type":"boolean"},` +
			`"trash_id":{"type":"string"},"path":{"type":"string"}},"required":["` + done + `"],"additionalProperties":false}`),
		Arguments: []capability.Argument{{Name: "trash_id", Required: true,
			Description: "Name of the trash item, as trash.list reports it"}},
		Fields: []capability.Field{
			{Name: done, Description: "True when Nextcloud applied the change"},
			{Name: "trash_id", Description: "The trash item"},
			{Name: "path", Description: "Original path relative to the fixed root folder of this connection"},
		},
		Examples: []capability.Example{{Description: "Use a trash_id from trash.list",
			Arguments: json.RawMessage(`{"trash_id":"report.pdf.d1700000000"}`)}},
	}
}

var trashRestore = trashChange("restore", "restored", "Restore a Nextcloud trash item",
	"Restore exactly one confirmed deleted item to its original location, if its original location lies below "+
		"the fixed root folder of a connection; there is no other destination",
	capability.EffectUpdate, organiseRisk(capability.EffectUpdate))

var trashDelete = allowListOnly(trashChange("delete", "deleted", "Delete a Nextcloud trash item for good",
	"Permanently delete exactly one confirmed trash item whose original location lies below the fixed root folder "+
		"of a connection; it cannot be restored afterwards",
	capability.EffectDelete, capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}))

// trashEntry is one deleted item whose original location lies below the connection root.
type trashEntry struct {
	ID        string `json:"trash_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	DeletedAt string `json:"deleted_at,omitempty"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
}

type trashListResult struct {
	Entries   []trashEntry `json:"entries"`
	Count     int          `json:"count"`
	Truncated bool         `json:"truncated,omitempty"`
}

type trashArguments struct {
	TrashID string `json:"trash_id"`
}

// trashURL is the absolute URL of the trash collection, or of one node in it.
func (c *Client) trashURL(collection string, id ...string) string {
	segments := append(append(append([]string{}, c.trashbin...), collection), id...)
	return c.origin + escapePath(segments)
}

// trashEntryOf reads one answered node. ok is false for a node whose original location lies outside the
// connection root; such a node is never reported.
func (c *Client) trashEntryOf(res *resource, id string) (trashEntry, bool) {
	location := strings.Split(strings.TrimPrefix(res.props[propTrashOrigin], "/"), "/")
	if len(location) > maxSegments {
		return trashEntry{}, false
	}
	for _, segment := range location {
		if checkResponseSegment(segment) != nil {
			return trashEntry{}, false
		}
	}
	if len(location) <= len(c.root) || !equalSegments(location[:len(c.root)], c.root) {
		return trashEntry{}, false
	}
	relative := location[len(c.root):]
	entry := trashEntry{ID: id, Name: bounded(res.props[propTrashName]), Path: strings.Join(relative, "/"), Type: typeFile}
	if entry.Name == "" {
		entry.Name = relative[len(relative)-1]
	}
	if res.collection {
		entry.Type = typeFolder
		entry.Size = number(res.props[propSize])
	} else {
		entry.Size = number(res.props[propContentLength])
	}
	if seconds, err := strconv.ParseInt(res.props[propTrashDeleted], 10, 64); err == nil && seconds > 0 && seconds <= maxTrashDeleted {
		entry.DeletedAt = time.Unix(seconds, 0).UTC().Format(time.RFC3339)
	}
	return entry, true
}

// trashProps reads one trash collection node with the fixed property set. A missing collection means the
// trash bin app is not installed.
func (c *Client) trashProps(ctx context.Context, op, target, depth string) ([]resource, error) {
	return c.propfindAt(ctx, op, target, trashPropfindBody, depth, false, maxTrashScan)
}

func isNotFound(err error) bool {
	var providerErr *provider.Error
	return errors.As(err, &providerErr) && providerErr.Message == messageNotFound
}

// ListTrash lists the deleted items below the connection root with one PROPFIND of depth 1.
func (c *Client) ListTrash(ctx context.Context) (*trashListResult, error) {
	const op = "list trash"
	resources, err := c.trashProps(ctx, op, c.trashURL(collectionTrash), depthChildren)
	if err != nil {
		if isNotFound(err) {
			return nil, providerError(op, messageTrashMissing)
		}
		return nil, err
	}
	bound := append(append([]string{}, c.trashbin...), collectionTrash)
	result := &trashListResult{Entries: []trashEntry{}}
	for i := range resources {
		relative, err := c.segmentsBelow(op, resources[i].href, bound)
		if err != nil {
			return nil, err
		}
		if len(relative) == 0 {
			continue
		}
		if len(relative) != 1 {
			return nil, invalidResponse(op, messageForeignEntry)
		}
		if resources[i].failure(op) != nil {
			continue
		}
		entry, ok := c.trashEntryOf(&resources[i], relative[0])
		if !ok {
			continue
		}
		if len(result.Entries) >= maxEntries {
			result.Truncated = true
			break
		}
		result.Entries = append(result.Entries, entry)
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.ID < b.ID
	})
	result.Count = len(result.Entries)
	return result, nil
}

// readTrashEntry reads one item before it is changed. An item outside the connection root is answered
// like a missing one, so the call neither names nor touches it.
func (c *Client) readTrashEntry(ctx context.Context, op, id string) (trashEntry, error) {
	resources, err := c.trashProps(ctx, op, c.trashURL(collectionTrash, id), depthSelf)
	if err != nil {
		if isNotFound(err) {
			return trashEntry{}, providerError(op, messageTrashNotFound)
		}
		return trashEntry{}, err
	}
	if len(resources) != 1 {
		return trashEntry{}, invalidResponse(op, "Nextcloud answered with more than the requested node")
	}
	bound := append(append([]string{}, c.trashbin...), collectionTrash)
	relative, err := c.segmentsBelow(op, resources[0].href, bound)
	if err != nil {
		return trashEntry{}, err
	}
	if len(relative) != 1 || relative[0] != id {
		return trashEntry{}, invalidResponse(op, messageForeignEntry)
	}
	if err := resources[0].failure(op); err != nil {
		return trashEntry{}, err
	}
	entry, ok := c.trashEntryOf(&resources[0], id)
	if !ok {
		return trashEntry{}, providerError(op, messageTrashNotFound)
	}
	return entry, nil
}

func invokeTrashList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListTrash(ctx)
}

func invokeTrashRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTrashChange(ctx, resolved, secrets, red, raw, "restore trash item", "restored")
}

func invokeTrashDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTrashChange(ctx, resolved, secrets, red, raw, "delete trash item", "deleted")
}

// invokeTrashChange reads the item once and then sends exactly one MOVE (restore) or DELETE. The request is
// never repeated, and an unclear outcome is reported as such.
func invokeTrashChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage, op, done string) (any, error) {
	var input trashArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkSegment(input.TrashID); err != nil {
		return nil, providerError(op, "trash_id is not a trash item name as trash.list reports it")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	entry, err := client.readTrashEntry(ctx, op, input.TrashID)
	if err != nil {
		return nil, err
	}
	method, hint, extra := http.MethodDelete, uncertainTrashPurged, http.Header(nil)
	if done == "restored" {
		method, hint = methodMove, uncertainRestored
		extra = http.Header{"Destination": {client.trashURL(collectionRestore, input.TrashID)}}
	}
	response, err := client.webdavTo(ctx, op, method, client.trashURL(collectionTrash, input.TrashID), nil, "", "", hint, extra)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	return map[string]any{done: true, "trash_id": input.TrashID, "path": entry.Path}, nil
}
