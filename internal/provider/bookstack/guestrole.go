package bookstack

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	// publicSystemName is the system name BookStack gives the role of anonymous visitors (guest access).
	publicSystemName = "public"
	// maxRoleListing is the most roles one guest-role lookup reads.
	maxRoleListing = 100

	guestUnavailable = "the guest role (public access) of the instance could not be determined, so nothing was " +
		"changed; reading roles needs the BookStack role permission to manage user roles"
)

// guestRole is the system role of anonymous visitors and the users that belong to it.
type guestRole struct {
	id      int64
	members map[int64]bool
}

// guestRole determines the guest role of the instance with two reads: the role listing finds the role whose
// system name is public, and a read of that role confirms the system name and lists the guest users. Every
// failure refuses closed with its own message; no provider text is relayed. The result belongs to one tool
// call and is not cached beyond it.
func (c *Client) guestRole(ctx context.Context) (guestRole, error) {
	const op = "determine guest role"
	var list struct {
		Data []struct {
			ID         int64  `json:"id"`
			SystemName string `json:"system_name"`
		} `json:"data"`
	}
	query := url.Values{"count": {strconv.Itoa(maxRoleListing)}}
	if err := c.get(ctx, op, "/api/roles", query, &list, nil, provider.ClassPermission); err != nil {
		return guestRole{}, guestRoleFailure(op, err)
	}
	var id int64
	for _, role := range list.Data {
		if role.SystemName != publicSystemName {
			continue
		}
		if id != 0 || role.ID <= 0 {
			return guestRole{}, guestRoleFailure(op, nil)
		}
		id = role.ID
	}
	if id == 0 {
		return guestRole{}, guestRoleFailure(op, nil)
	}
	var detail struct {
		ID         int64  `json:"id"`
		SystemName string `json:"system_name"`
		Users      []struct {
			ID int64 `json:"id"`
		} `json:"users"`
	}
	if err := c.get(ctx, op, "/api/roles/"+strconv.FormatInt(id, 10), nil, &detail, nil, provider.ClassPermission); err != nil {
		return guestRole{}, guestRoleFailure(op, err)
	}
	if detail.ID != id || detail.SystemName != publicSystemName {
		return guestRole{}, guestRoleFailure(op, nil)
	}
	members := make(map[int64]bool, len(detail.Users))
	for _, user := range detail.Users {
		members[user.ID] = true
	}
	return guestRole{id: id, members: members}, nil
}

// guestRoleFailure keeps the class of a failed request and replaces its message.
func guestRoleFailure(op string, cause error) error {
	class := provider.ClassProviderError
	var failure *provider.Error
	if errors.As(cause, &failure) {
		class = failure.Class
	}
	return &provider.Error{Class: class, Op: op, Message: guestUnavailable}
}
