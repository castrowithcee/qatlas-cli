package lexware

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// rawObject is a provider object kept as received. Changing it touches only the members set explicitly, so
// every member Qatlas does not model survives a read-modify-write round trip byte for byte.
type rawObject map[string]json.RawMessage

// set replaces one member with the JSON encoding of value.
func (o rawObject) set(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	o[key] = data
	return nil
}

// object returns a nested object member, or an empty one when the member is absent or not an object.
func (o rawObject) object(key string) rawObject {
	nested := rawObject{}
	if json.Unmarshal(o[key], &nested) != nil || nested == nil {
		return rawObject{}
	}
	return nested
}

// postObject sends one creation and checks that the answer names the new object. uncertain is appended to
// every failure after the request may have arrived, including an answer without a usable identifier.
func (c *Client) postObject(ctx context.Context, op, path string, payload any, uncertain, noun string) (*createResult, error) {
	var response createResultJSON
	if err := c.post(ctx, op, "", path, nil, payload, &response, uncertain); err != nil {
		return nil, err
	}
	if !validUUID(response.ID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware returned " + noun + " without a usable identifier" + uncertain}
	}
	return response.result(), nil
}

// updateObject changes one object under optimistic locking: it reads the object, lets apply set the fields
// the caller wants changed, and sends exactly one PUT carrying the version it read. A failure of the read is
// no uncertainty, since nothing was written; a 409 on the PUT is the conflict error and proves nothing was
// applied. id must be a validated UUID.
func (c *Client) updateObject(ctx context.Context, op, resource, base, id, uncertain, noun string,
	apply func(rawObject) error) (*createResult, error) {
	path := base + "/" + url.PathEscape(id)
	object := rawObject{}
	if err := c.get(ctx, op, resource, path, nil, &object); err != nil {
		return nil, err
	}
	var readID string
	var version int
	if json.Unmarshal(object["id"], &readID) != nil || !strings.EqualFold(readID, id) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware answered with a different " + resource + " than the requested one"}
	}
	if json.Unmarshal(object["version"], &version) != nil || version < 0 {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware returned " + noun + " without a usable version"}
	}
	if err := apply(object); err != nil {
		return nil, providerError(op, "the change could not be applied")
	}
	var response createResultJSON
	if err := c.put(ctx, op, resource, path, object, &response, uncertain); err != nil {
		return nil, err
	}
	if !strings.EqualFold(response.ID, id) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware answered with a different " + resource + " than the requested one" + uncertain}
	}
	return response.result(), nil
}

// createResultJSON is the answer of a creation or an update: identifier, timestamps and the new version.
type createResultJSON struct {
	ID          string `json:"id"`
	CreatedDate string `json:"createdDate"`
	UpdatedDate string `json:"updatedDate"`
	Version     int    `json:"version"`
}

func (r createResultJSON) result() *createResult {
	return &createResult{ID: r.ID, CreatedDate: r.CreatedDate, UpdatedDate: r.UpdatedDate, Version: r.Version}
}
