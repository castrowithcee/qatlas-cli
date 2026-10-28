package nextcloud

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// notesPathOf reads the folder the Notes app keeps its notes in, as segments below the Files root.
func (c *Client) notesPathOf(ctx context.Context, op string) ([]string, error) {
	body, _, err := c.notesGetJSON(ctx, op, c.notesURL(nil, "settings"))
	if err != nil {
		return nil, err
	}
	var raw struct {
		NotesPath string `json:"notesPath"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalidResponse(op, "the Nextcloud notes settings could not be read")
	}
	return splitRelative(strings.Trim(raw.NotesPath, "/"))
}

// deriveCross fills the exclusions of the other kind. The notes path is read on every call of a connection
// whose exclusions reach across the kinds, so a changed setting takes effect at once. The Files gates that
// run before the credential see only the configured exclusions; every request passes the gates of the
// client again, after this read. A read that fails, or answers a path that cannot be used, fails the call
// closed: nothing is sent that could reach what the exclusions hide, and the answer names no exclusion.
func (c *Client) deriveCross(ctx context.Context) error {
	if !c.scope.crossNeeded() {
		return nil
	}
	path, err := c.notesPathOf(ctx, "open")
	if err != nil || len(path) == 0 {
		return blockedError("open")
	}
	c.scope.cross = c.scope.derive(path)
	return nil
}

// categoryHidden reports whether a category named by an argument lies on or below an excluded category,
// judged by the configuration alone so that it runs before any credential access.
func categoryHidden(resolved *config.Resolved, category string) bool {
	s, err := scopeOf(resolved)
	return err != nil || s.categoryExcluded(category)
}

// checkWriteCategory refuses a write whose category, or the root category of a note without one, lies on
// or below an exclusion once the exclusions that cross kinds are known.
func (c *Client) checkWriteCategory(op string, category *string) error {
	if category == nil {
		if c.scope.categoryExcluded("") {
			return categoryRefused(op)
		}
		return nil
	}
	if c.scope.categoryExcluded(*category) {
		return categoryRefused(op)
	}
	return nil
}
