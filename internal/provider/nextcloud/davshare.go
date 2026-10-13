package nextcloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
)

// Sharing of DAV collections with users and groups of the instance: one oc:share POST per change and the
// oc:invite property for the recipients. Only principals/users/<id> and principals/groups/<id> are ever
// built, so no e-mail, team, or remote recipient can be addressed.

const (
	davShareUsers  = "users"
	davShareGroups = "groups"
	davShareOther  = "other"

	davShareRead      = "read"
	davShareReadWrite = "read-write"

	maxDavShareName = 256

	davShareTypeSchema = `"type":{"type":"string","enum":["users","groups"]}`
	davShareIDSchema   = `"id":{"type":"string","minLength":1,"maxLength":64}`
)

var (
	davShareTypeArgument = capability.Argument{Name: "type", Required: true, Description: "users or groups"}
	davShareIDArgument   = capability.Argument{Name: "id", Required: true,
		Description: "ID of an existing user or group of the instance, 1 to 64 characters; no e-mail address"}
)

const davShareSchema = `{"type":"object","properties":{"type":{"type":"string"},"id":{"type":"string"},` +
	`"name":{"type":"string"},"access":{"type":"string"},"status":{"type":"string"}},` +
	`"required":["type"],"additionalProperties":false}`

// CollectionShare is one recipient of a shared collection. A recipient that is no user or group carries neither id nor
// name, so an e-mail address never leaves the provider answer.
type CollectionShare struct {
	Type   string `json:"type"`
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Access string `json:"access,omitempty"`
	Status string `json:"status,omitempty"`
}

// davSharePrincipal checks a recipient and returns its principal href. Nothing but the two fixed principal
// collections and a plain user or group ID can pass.
func davSharePrincipal(op, kind, id string) (string, error) {
	var ok bool
	switch kind {
	case davShareUsers:
		ok = userIDPattern.MatchString(id)
	case davShareGroups:
		ok = groupIDPattern.MatchString(id)
	default:
		return "", providerError(op, "type must be users or groups")
	}
	// A dot-only ID would turn the principal into a relative path.
	if !ok || strings.Trim(id, ".") == "" {
		return "", providerError(op, "id must be the ID of a user or group: 1 to 64 letters, digits, and . _ @ ' -")
	}
	return "principal:principals/" + kind + "/" + id, nil
}

// davShareSetBody grants or changes the access of one recipient.
func davShareSetBody(href string, write bool) string {
	access := "<o:read/>"
	if write {
		access = "<o:read-write/>"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<o:share xmlns:d="DAV:" xmlns:o="http://owncloud.org/ns"><o:set><d:href>` + xmlText(href) +
		`</d:href>` + access + `</o:set></o:share>`
}

// davShareRemoveBody revokes the share of one recipient.
func davShareRemoveBody(href string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<o:share xmlns:d="DAV:" xmlns:o="http://owncloud.org/ns"><o:remove><d:href>` + xmlText(href) +
		`</d:href></o:remove></o:share>`
}

// sendDavShare sends the one share POST. Nextcloud answers a handled change with 200; any other success status
// leaves the outcome open like an unclear failure.
func (c *Client) sendDavShare(ctx context.Context, op string, segments []string, body, hint string) error {
	status, _, err := c.collectionSend(ctx, op, http.MethodPost, segments, body, nil, hint)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return invalidResponse(op, "Nextcloud answered the change with an unexpected status"+hint)
	}
	return nil
}

// davSharesOf reduces the invites of a collection to users and groups; any other principal is typed other.
func davSharesOf(invites []dav.Invite, truncated bool) ([]CollectionShare, bool) {
	var shares []CollectionShare
	for _, invite := range invites {
		shares = append(shares, davShareOf(invite))
	}
	return shares, truncated
}

func davShareOf(invite dav.Invite) CollectionShare {
	share := CollectionShare{Type: davShareOther, Access: invite.Access, Status: invite.Status}
	parts := strings.Split(strings.TrimPrefix(invite.Href, "principal:"), "/")
	if len(parts) != 3 || parts[0] != "principals" || parts[1] != davShareUsers && parts[1] != davShareGroups {
		return share
	}
	id, err := url.PathUnescape(parts[2])
	if err != nil {
		return share
	}
	if _, err := davSharePrincipal("read shares", parts[1], id); err != nil {
		return share
	}
	share.Type, share.ID = parts[1], id
	// A name that looks like an e-mail address is dropped.
	if name := dav.Clean(invite.Name, maxDavShareName); !strings.Contains(name, "@") {
		share.Name = name
	}
	return share
}
