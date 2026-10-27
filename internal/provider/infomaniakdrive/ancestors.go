package infomaniakdrive

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// A folder above an excluded object (an ancestor) stays reachable, but a tool that would act on everything
// below it, or show a share link or dropbox that gives access to it, must not. The ancestors of a drive are
// the parents of its excluded files and folders, read once per call with one request each.

// loadAncestors reads the ancestors of a drive's excluded objects once and keeps them for the rest of the
// call. An object Infomaniak does not find is excluded in vain and adds none; one it will not show, or an
// answer that cannot be read, refuses the call because the folders above it stay unknown.
func (c *Client) loadAncestors(ctx context.Context, op string, driveID int64) (map[int64]bool, error) {
	if set, ok := c.ancestors[driveID]; ok {
		return set, nil
	}
	set := map[int64]bool{}
	for _, id := range c.scope.excludedIn(driveID) {
		item, err := c.readWithParents(ctx, op, driveID, id)
		if err != nil {
			var providerErr *provider.Error
			if errors.As(err, &providerErr) && providerErr.Class == provider.ClassNotFound {
				continue
			}
			return nil, refuseUnproven(err)
		}
		if item.ID != id || item.Parents == nil {
			return nil, invalidRequest(outsideSubtree)
		}
		for _, parent := range *item.Parents {
			set[parent.ID] = true
		}
		set[rootFileID] = true
	}
	if c.ancestors == nil {
		c.ancestors = map[int64]map[int64]bool{}
	}
	c.ancestors[driveID] = set
	return set, nil
}

// refuseAncestor refuses a folder above an excluded object, with the message of an object outside the
// targets. A drive without excluded files or folders has none and needs no read.
func (c *Client) refuseAncestor(ctx context.Context, op string, driveID, fileID int64) error {
	if !c.scope.hasFileExcludes(driveID) {
		return nil
	}
	set, err := c.loadAncestors(ctx, op, driveID)
	if err != nil {
		return err
	}
	if set[fileID] {
		return invalidRequest(outsideSubtree)
	}
	return nil
}

// hidesLinks reports whether the share link and dropbox of a file must not be shown: the file lies above an
// excluded object, or the ancestors of the drive have not been read, which cannot tell.
func (c *Client) hidesLinks(driveID, fileID int64) bool {
	if !c.scope.hasFileExcludes(driveID) {
		return false
	}
	set, ok := c.ancestors[driveID]
	return !ok || set[fileID]
}

// loadAncestorsFor reads the ancestors where the with values ask for a share link or a dropbox, which the
// entries of a folder above an excluded object leave out.
func (c *Client) loadAncestorsFor(ctx context.Context, op string, driveID int64, with []string) error {
	if !c.scope.hasFileExcludes(driveID) || !slices.ContainsFunc(with, linkRelated) {
		return nil
	}
	_, err := c.loadAncestors(ctx, op, driveID)
	return err
}

func linkRelated(value string) bool {
	return strings.HasPrefix(value, "sharelink") || strings.HasPrefix(value, "dropbox")
}
