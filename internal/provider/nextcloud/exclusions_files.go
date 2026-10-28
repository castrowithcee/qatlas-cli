package nextcloud

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The Files tools see an excluded folder the way Nextcloud shows a folder the identity may not read: it
// does not exist. Every gate below takes a path relative to the connection root and returns the error the
// tool answers with, or nil. The gates read nothing but the configuration, so they run before any
// credential access, local I/O, or request. No error names the path or the exclusion.

// joined turns a path relative to the connection root into one relative to the Files root, the form the
// exclusions are written in.
func (s scope) joined(rel []string) []string {
	return append(append([]string{}, s.folder...), rel...)
}

// relHidden reports whether a path lies on or below an excluded folder.
func (s scope) relHidden(rel []string) bool { return s.folderExcluded(s.joined(rel)) }

// relHolds reports whether a path is a strict ancestor of an excluded folder.
func (s scope) relHolds(rel []string) bool { return s.folderHoldsExcluded(s.joined(rel)) }

// relIsExcluded reports whether a path is exactly an excluded folder.
func (s scope) relIsExcluded(rel []string) bool {
	full := comparisonSegments(s.joined(rel))
	equal := func(e []string) bool { return slices.Equal(e, full) }
	return slices.ContainsFunc(s.excludes.folders, equal) || slices.ContainsFunc(s.cross.folders, equal)
}

// hiddenError answers like a node that does not exist.
func hiddenError(op string) error {
	return withOriginalValue(statusError(op, http.StatusNotFound), &provider.Original{Status: http.StatusNotFound})
}

// missingParentError answers like a create below a folder that does not exist.
func missingParentError(op string) error {
	return withOriginalValue(statusError(op, http.StatusConflict), &provider.Original{Status: http.StatusConflict})
}

// blockedError refuses an operation whose effect would reach what is hidden.
func blockedError(op string) error {
	return &provider.Error{Class: provider.ClassPermission, Op: op,
		Message: "this connection does not allow this operation on the path"}
}

// gateRead guards a read or a change of an existing node: hidden is the same as missing.
func (s scope) gateRead(op string, rel []string) error {
	if s.relHidden(rel) {
		return hiddenError(op)
	}
	return nil
}

// gateCreate guards the creation of a node: below an excluded folder the parent is missing, and the
// excluded folder itself cannot be created.
func (s scope) gateCreate(op string, rel []string) error {
	switch {
	case s.relIsExcluded(rel):
		return blockedError(op)
	case s.relHidden(rel):
		return missingParentError(op)
	}
	return nil
}

// uploadGate is the gate of a content write: create makes a new node, update changes an existing one.
func uploadGate(create bool) func(scope, string, []string) error {
	if create {
		return scope.gateCreate
	}
	return scope.gateRead
}

// gateSource guards a delete, move, or copy of a node. What is hidden is missing, and an ancestor of an
// excluded folder is refused, because the operation would take the hidden content along or detach it
// from its path.
func (s scope) gateSource(op string, rel []string) error {
	if err := s.gateRead(op, rel); err != nil {
		return err
	}
	if s.relHolds(rel) {
		return blockedError(op)
	}
	return nil
}

// gateDestination guards the new path of a move or copy: it is created, and it must not be an ancestor
// of an excluded folder either.
func (s scope) gateDestination(op string, rel []string) error {
	if err := s.gateCreate(op, rel); err != nil {
		return err
	}
	if s.relHolds(rel) {
		return blockedError(op)
	}
	return nil
}

// gateOpen reads the scope of a connection and applies a gate before any credential access.
func gateOpen(resolved *config.Resolved, op string, gate func(scope) error) error {
	s, err := scopeOf(resolved)
	if err != nil {
		return providerError(op, err.Error())
	}
	return gate(s)
}

// preflight applies one gate to one path argument before any credential access, local file access, or
// request. A path that does not parse is left to the tool, which refuses it as before.
func preflight(resolved *config.Resolved, op, path string, gate func(scope, string, []string) error) error {
	rel, err := splitRelative(path)
	if err != nil {
		return nil
	}
	return preflightRel(resolved, op, rel, gate)
}

// preflightRel is preflight for a path the tool has already split.
func preflightRel(resolved *config.Resolved, op string, rel []string, gate func(scope, string, []string) error) error {
	return gateOpen(resolved, op, func(s scope) error { return gate(s, op, rel) })
}

// urlHidden reports whether an absolute URL or a path addresses a node on or below an excluded folder of
// the Files tree of this identity. It is the second line behind the gates: every request of the Files
// tools passes it, so a path no gate foresaw is still not sent. An address it cannot read is treated as
// hidden.
func (c *Client) urlHidden(target string) bool {
	if len(c.scope.excludes.folders) == 0 && len(c.scope.cross.folders) == 0 {
		return false
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return true
	}
	if parsed.Host != "" && parsed.Scheme+"://"+parsed.Host != c.origin {
		return false
	}
	var segments []string
	for _, part := range strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/") {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return true
		}
		segments = append(segments, decoded)
	}
	prefix := comparisonSegments(c.prefix)
	if !hasPrefix(comparisonSegments(segments), prefix) {
		return false
	}
	return c.scope.folderExcluded(segments[len(prefix):])
}

// guardURLs refuses a request one of whose addresses is hidden, as the server would answer for a node
// that does not exist.
func (c *Client) guardURLs(op string, targets ...string) error {
	for _, target := range targets {
		if target != "" && c.urlHidden(target) {
			return hiddenError(op)
		}
	}
	return nil
}
