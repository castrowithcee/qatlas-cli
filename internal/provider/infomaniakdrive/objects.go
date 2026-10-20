package infomaniakdrive

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// A connection may be limited to a folder or file of a drive (drive/DRIVE_ID/files/FILE_ID). Where a drive
// is bound only that way, every file, folder, or destination identifier a tool receives is proven to lie
// in such a subtree before the tool has any effect. The proof is one read of the object with its parents.
// It fails closed, and no refusal names the identifier or the name of the object.

const outsideSubtree = "the file or folder is outside the targets of this connection"

// parentJSON is one folder above an object.
type parentJSON struct {
	ID int64 `json:"id"`
}

// readWithParents reads one object together with all folders above it, once.
func (c *Client) readWithParents(ctx context.Context, op string, driveID, fileID int64) (*fileJSON, error) {
	path := fmt.Sprintf("/3/drive/%d/files/%d", driveID, fileID)
	var item fileJSON
	if err := c.do(ctx, op, path, url.Values{"with": {"parents"}}, &item); err != nil {
		return nil, err
	}
	return &item, nil
}

// inSubtree reports whether an object read with its parents is a files target of its drive or lies below
// one. An answer without parents, or for another object than asked for, proves nothing.
func (c *Client) inSubtree(driveID, fileID int64, item *fileJSON) bool {
	if item.ID != fileID || item.Parents == nil {
		return false
	}
	if c.scope.isSubtreeRoot(driveID, fileID) {
		return true
	}
	for _, parent := range *item.Parents {
		if c.scope.isSubtreeRoot(driveID, parent.ID) {
			return true
		}
	}
	return false
}

// refuseUnproven turns a failed read into a refusal that cannot tell a missing object from one outside the
// targets; a failure that says nothing about the object, such as a rate limit, keeps its class.
func refuseUnproven(err error) error {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		switch providerErr.Class {
		case provider.ClassNotFound, provider.ClassPermission, provider.ClassInvalidResponse:
			return invalidRequest(outsideSubtree)
		}
	}
	return err
}

// proveObjects proves each object to lie in the subtree targets of the connection, one read each. A drive
// bound as a whole needs no proof and no read.
func (c *Client) proveObjects(ctx context.Context, op string, driveID int64, ids ...int64) error {
	if c.scope.wholeDrive(driveID) {
		return nil
	}
	for _, id := range ids {
		item, err := c.readWithParents(ctx, op, driveID, id)
		if err != nil {
			return refuseUnproven(err)
		}
		if !c.inSubtree(driveID, id, item) {
			return invalidRequest(outsideSubtree)
		}
	}
	return nil
}

// statProven reads the metadata of one object. Where the drive is bound only as a subtree, the same read
// carries the parents and is the proof; there is no second read.
func (c *Client) statProven(ctx context.Context, driveID, fileID int64) (*Entry, error) {
	const op = "stat file"
	if c.scope.wholeDrive(driveID) {
		return c.StatFile(ctx, driveID, fileID)
	}
	item, err := c.readWithParents(ctx, op, driveID, fileID)
	if err != nil {
		return nil, refuseUnproven(err)
	}
	if !c.inSubtree(driveID, fileID, item) {
		return nil, invalidRequest(outsideSubtree)
	}
	entry := entryOf(*item)
	return &entry, nil
}

// protectTarget refuses a change that would take a files target itself out of its place: moving it or
// putting it in the trash. Renaming it stays possible.
func protectTarget(resolved *config.Resolved, driveID, fileID int64) error {
	bound, err := scopeOf(resolved)
	if err != nil {
		return providerError("open", err.Error())
	}
	if bound.isSubtreeRoot(driveID, fileID) {
		return invalidRequest("a file or folder this connection is limited to cannot be moved or trashed")
	}
	return nil
}

// requireWholeDrive refuses a tool that acts on a whole drive where the connection binds the drive only as
// a subtree.
func requireWholeDrive(resolved *config.Resolved, driveID int64) error {
	bound, err := scopeOf(resolved)
	if err != nil {
		return providerError("open", err.Error())
	}
	if !bound.wholeDrive(driveID) {
		return invalidRequest("this tool needs the whole drive in the targets of this connection")
	}
	return nil
}

// wholeDriveTools are the tools that act on the whole drive and so are out of reach of a binding to a
// folder or file alone.
var wholeDriveTools = []string{
	Provider + ".trash.list", Provider + ".trash.restore", Provider + ".trash.delete",
	Provider + ".trash.empty", Provider + ".links.list",
}

// reaches says whether a binding reaches a tool. Without a files target everything is reached; with one, a
// whole-drive tool needs at least one drive/DRIVE_ID target, and the call itself checks the concrete drive.
func reaches(tool string, targets []string) (bool, []string) {
	if !slices.Contains(wholeDriveTools, tool) {
		return true, nil
	}
	hasFiles := false
	for _, raw := range targets {
		segments := strings.Split(strings.TrimSpace(raw), "/")
		switch {
		case len(segments) == 2 && segments[0] == "drive":
			return true, nil
		case len(segments) == 4 && segments[0] == "drive" && segments[2] == "files":
			hasFiles = true
		}
	}
	if !hasFiles {
		return true, nil
	}
	return false, []string{"drive/DRIVE_ID"}
}
