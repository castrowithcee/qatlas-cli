package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Deleting, restoring, and destroying one record go to the fixed routes of twenty-server's
// rest-api-delete-one, rest-api-restore-one, and rest-api-destroy-one handlers. The soft-delete parameter is
// part of the route of delete and absent from destroy; nothing in the arguments selects between them.
const (
	// removeUncertain is appended to a failure of a removal or restore whose request may have reached Twenty.
	removeUncertain = "; this change may have taken effect, look the record up with twentycrm.records.get or in " +
		"the trash with twentycrm.records.list deleted true before repeating it"

	errDeletePermission = "the workspace role of this API key may not delete records of this object; check the " +
		"object permissions of the role in Twenty, the right is called Delete Records"
	errDestroyPermission = "the workspace role of this API key may not permanently delete records of this object; " +
		"check the object permissions of the role in Twenty, the right is called Destroy Records"
	errRestorePermission = "the workspace role of this API key may not restore records of this object; check the " +
		"object permissions of the role in Twenty"
)

var removeInput = json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"id":` + recordIDSchema +
	`},"required":["object","id"],"additionalProperties":false}`)

var removeArguments = []capability.Argument{
	{Name: "object", Description: "Singular API name of the object in camelCase, as returned by twentycrm.objects.list", Required: true},
	{Name: "id", Description: "Record identifier as a UUID, as returned by twentycrm.records.list", Required: true},
}

const removeNote = "Acts on one record of one reachable object. A connection offers this tool only when its tools list names it"

var removeOutput = json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
	`"required":["deleted"],"additionalProperties":false}`)

func removeDescriptor(action, title, description string, effect capability.Effect, output json.RawMessage, allowList bool) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".records." + action, Version: 1, Title: title, Description: description,
		Tags: []string{"twentycrm", "crm", "records", action}, Provider: Provider,
		Risk:                  recordWriteRisk(effect, capability.IdempotencyIdempotent),
		RequiresToolAllowList: allowList,
		InputSchema:           removeInput, OutputSchema: output, Arguments: removeArguments,
		Examples: []capability.Example{{
			Description: title,
			Arguments:   json.RawMessage(`{"object":"person","id":"11111111-2222-3333-4444-555555555555"}`),
		}},
	}
}

var recordsDelete = removeDescriptor("delete", "Delete a Twenty CRM record",
	"Move one record of one reachable object of the Twenty workspace of a connection to the trash. The record stays "+
		"recoverable with twentycrm.records.restore and is found with twentycrm.records.list deleted true. "+removeNote,
	capability.EffectDelete, removeOutput, true)

var recordsDestroy = removeDescriptor("destroy", "Destroy a Twenty CRM record",
	"Permanently delete one record of one reachable object of the Twenty workspace of a connection. This cannot be "+
		"undone: the record is not moved to the trash and twentycrm.records.restore cannot bring it back. Twenty's "+
		"answer does not tell a soft from a permanent deletion, so the result only confirms that Twenty accepted the "+
		"request. "+removeNote,
	capability.EffectDelete, removeOutput, true)

var recordsRestore = removeDescriptor("restore", "Restore a Twenty CRM record",
	"Restore one record of the trash of one reachable object of the Twenty workspace of a connection, as found with "+
		"twentycrm.records.list deleted true",
	capability.EffectUpdate, json.RawMessage(recordSchema), false)

// recordRemoval is the locally checked request of a delete, restore, or destroy.
type recordRemoval struct {
	Object string
	ID     string
}

// newRecordRemoval checks the object against the connection's targets and the identifier before any secret is
// resolved and before any request is sent.
func newRecordRemoval(resolved *config.Resolved, op string, raw json.RawMessage) (*recordRemoval, error) {
	var args writeArguments
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectObject(resolved, args.Object); err != nil {
		return nil, err
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest(errWriteID)
	}
	return &recordRemoval{Object: args.Object, ID: args.ID}, nil
}

func invokeRemoveRecord(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, action string) (any, error) {
	removal, err := newRecordRemoval(resolved, action+" record", raw)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.RemoveRecord(ctx, action, removal)
}

func invokeRecordsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeRemoveRecord(ctx, resolved, secrets, red, raw, "delete")
}

func invokeRecordsDestroy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeRemoveRecord(ctx, resolved, secrets, red, raw, "destroy")
}

func invokeRecordsRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeRemoveRecord(ctx, resolved, secrets, red, raw, "restore")
}

// RemoveRecord sends exactly one writing request for action delete, destroy, or restore. The route comes from
// the workspace catalog and the validated identifier, the method and query are fixed per action, and Twenty's
// answer must name the requested record.
func (c *Client) RemoveRecord(ctx context.Context, action string, removal *recordRemoval) (any, error) {
	op := action + " record"
	object, err := c.recordObject(ctx, op, removal.Object)
	if err != nil {
		return nil, err
	}
	method, suffix, denied := http.MethodDelete, "?soft_delete=true", errDeletePermission
	switch action {
	case "destroy":
		suffix, denied = "", errDestroyPermission
	case "restore":
		method, suffix, denied = http.MethodPatch, "/restore", errRestorePermission
	}
	path := "/rest/" + url.PathEscape(object.Plural) + "/" + url.PathEscape(removal.ID) + suffix
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.changeWith(ctx, op, removeUncertain, method, path, nil, &response); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			failure.Message = denied
		}
		return nil, err
	}
	uncertainFailure := func(message string) error {
		return provider.InvalidResponse(op, message+removeUncertain)
	}
	verb := "delete"
	if action == "restore" {
		verb = action
	}
	item, ok := response.Data[verb+strings.ToUpper(object.Name[:1])+object.Name[1:]]
	if !ok {
		return nil, uncertainFailure("Twenty returned an unusable record")
	}
	if action == "restore" {
		record, err := object.project(op, item, nil)
		if err != nil {
			var failure *provider.Error
			if errors.As(err, &failure) {
				failure.Message += removeUncertain
			}
			return nil, err
		}
		if !strings.EqualFold(record.ID, removal.ID) {
			return nil, uncertainFailure("Twenty answered with a different record than the requested one")
		}
		return record, nil
	}
	var answered struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(item, &answered) != nil || !validUUID(answered.ID) || !strings.EqualFold(answered.ID, removal.ID) {
		return nil, uncertainFailure("Twenty answered with a different record than the requested one")
	}
	return map[string]bool{"deleted": true}, nil
}
