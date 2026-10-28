package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// An excluded conversation answers like one Nextcloud does not find for the identity. Besides the token
// itself, two kinds of conversation belong to something else and are excluded with it: a breakout room
// (object type room), whose object ID is the token of its parent, follows the parent, and a conversation
// about a file (object type file), whose object ID is a file ID, follows the folder exclusions. Both are
// decided from the conversation, which a tool reads once, and only where exclusions make it necessary.
const (
	talkObjectRoom = "room"
	talkObjectFile = "file"

	messageTalkHoldsExcluded = "this connection does not allow this operation on this Talk conversation"
)

// talkMissing answers like a conversation Nextcloud does not find.
func talkMissing(op, message string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: message,
		Original: &provider.Original{Status: http.StatusNotFound}}
}

// talkHides reports whether a conversation token is excluded by name.
func (s scope) talkHides(token string) bool {
	return s.talks.excluding(s.excludes.talks, false).hides(token)
}

func (s scope) folderExcludesAny() bool {
	return len(s.excludes.folders) > 0 || len(s.cross.folders) > 0
}

// talkReadNeeded reports whether a conversation must be read to decide whether it is excluded.
func (s scope) talkReadNeeded() bool { return len(s.excludes.talks) > 0 || s.folderExcludesAny() }

// talkGate reads the conversation once, when exclusions make that necessary, and refuses it as missing when
// it is excluded. The read fails the way the tool's own request for a missing conversation fails.
func (c *Client) talkGate(ctx context.Context, op, token, missing string) error {
	if !c.scope.talkReadNeeded() {
		return nil
	}
	state, err := c.readRoomState(ctx, op, token, missing)
	if err != nil {
		return err
	}
	return c.roomGate(ctx, op, missing, state.ObjectType, string(state.ObjectID))
}

// readRoomState is the one read of a conversation that the gates and the change tools share.
func (c *Client) readRoomState(ctx context.Context, op, token, missing string) (rawRoomState, error) {
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, suffix: []string{token},
		query: url.Values{"noStatusUpdate": {"1"}}})
	if err != nil {
		return rawRoomState{}, talkNotFound(op, err, missing)
	}
	var state rawRoomState
	if err := json.Unmarshal(data, &state); err != nil || state.Token != token {
		return rawRoomState{}, invalidResponse(op, "the Nextcloud conversation could not be read")
	}
	return state, nil
}

// roomGate refuses a conversation that is excluded, given what a read of it reported.
func (c *Client) roomGate(ctx context.Context, op, missing, objectType, objectID string) error {
	hidden, err := c.roomExcluded(ctx, op, objectType, objectID)
	if err != nil {
		return err
	}
	if hidden {
		return talkMissing(op, missing)
	}
	return nil
}

// roomExcluded reports whether a conversation is excluded through the object it belongs to. A file that
// cannot be resolved counts as excluded; a failed request is an error, never an answer.
func (c *Client) roomExcluded(ctx context.Context, op, objectType, objectID string) (bool, error) {
	switch objectType {
	case talkObjectRoom:
		return c.scope.talkHides(objectID), nil
	case talkObjectFile:
		if !c.scope.folderExcludesAny() {
			return false, nil
		}
		return c.fileExcluded(ctx, op, objectID)
	}
	return false, nil
}

// fileExcluded resolves a file ID to the path of the identity with one SEARCH on its whole Files tree, so
// that a file outside the connection root is still told from one that cannot be found. The answered
// location is checked like every location the server hands back.
func (c *Client) fileExcluded(ctx context.Context, op, id string) (bool, error) {
	if len(id) > 20 || !allDigits(id) {
		return true, nil
	}
	body := xmlHeader + `<d:searchrequest xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:basicsearch>` +
		`<d:select><d:prop><oc:fileid/></d:prop></d:select>` +
		`<d:from><d:scope><d:href>` + xmlText(escapePath([]string{"files", c.user})) + `</d:href>` +
		`<d:depth>infinity</d:depth></d:scope></d:from>` +
		`<d:where>` + comparison("eq", "oc:fileid", id) + `</d:where><d:orderby/>` +
		`<d:limit><d:nresults>1</d:nresults></d:limit></d:basicsearch></d:searchrequest>`
	resources, err := c.multistatusAt(ctx, op, methodSearch, c.davRootURL(), body, "text/xml; charset=utf-8", "",
		maxSearchLimit, true)
	if err != nil {
		return false, err
	}
	for i := range resources {
		if resources[i].failure(op) != nil || strings.TrimSpace(resources[i].props[propFileID]) != id {
			continue
		}
		path, err := c.segmentsBelow(op, resources[i].href, c.prefix)
		if err != nil {
			return true, nil
		}
		return c.scope.folderExcluded(path), nil
	}
	return true, nil
}

// breakoutHoldsExcluded reports whether a conversation has a breakout room that is excluded by name. The
// list of the breakout rooms exists only for a conversation that configured them, so the read follows the
// mode of the conversation. A failed read is an error: deleting must not destroy what cannot be checked.
func (c *Client) breakoutHoldsExcluded(ctx context.Context, op, token string, mode flexString) (bool, error) {
	if len(c.scope.excludes.talks) == 0 || mode == "" || mode == "0" {
		return false, nil
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, suffix: []string{token, "breakout-rooms"}})
	if err != nil {
		return false, talkNotFound(op, err, messageTalkNoRoom)
	}
	var rooms []rawRoomState
	if err := json.Unmarshal(data, &rooms); err != nil {
		return false, invalidResponse(op, "the Nextcloud breakout conversations could not be read")
	}
	for _, room := range rooms {
		if c.scope.talkHides(room.Token) {
			return true, nil
		}
	}
	return false, nil
}
