package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/projectpath"
)

// pathRule states what a paths entry must look like. Like the other rules it never quotes the entry: an
// entry is named by its position.
const pathRule = "must name a directory, either absolute or starting with '~/', without glob patterns"

// validatePaths checks the paths list of one connection. An explicit empty list is refused: it would read
// like a binding to nothing, while leaving paths out is what makes a connection apply everywhere.
func validatePaths(name string, paths []string, report func(string, ...any)) {
	if paths != nil && len(paths) == 0 {
		report("connections.%s.paths: must name at least one directory; leave paths out to use the "+
			"connection in every project", name)
		return
	}
	seen := map[string]bool{}
	for i, entry := range paths {
		if err := CheckPath(entry); err != nil {
			report("connections.%s.paths[%d]: %s", name, i, err)
			continue
		}
		key := filepath.Clean(entry)
		if seen[key] {
			report("connections.%s.paths[%d]: a directory is listed more than once", name, i)
		}
		seen[key] = true
	}
}

// CheckPath checks one paths entry on its own, by the rules the paths list of a connection applies to each
// of its entries. Like those rules, the error never quotes the entry. Whether the directory exists is no
// rule: PathWarnings names such an entry instead.
func CheckPath(entry string) error {
	switch {
	case strings.TrimSpace(entry) == "":
		return errors.New("must not be empty")
	case strings.TrimSpace(entry) != entry:
		return errors.New("must not start or end with blanks")
	case strings.ContainsAny(entry, "*?["):
		return errors.New("glob patterns are not supported; " + pathRule)
	case !projectpath.IsHomeRelative(entry) && !filepath.IsAbs(entry):
		return errors.New(pathRule)
	}
	return nil
}

// SamePath reports whether two entries name the same directory in the sense of the rule that lists a
// directory only once.
func SamePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// PathWarnings names every paths entry that does not name an existing directory, one line per entry, sorted
// by connection. Such an entry is valid, since the directory may be created later, but until then no project
// lies inside it. The line names the connection and the entry's position, never the path.
func (c *Config) PathWarnings() []string {
	var warnings []string
	for _, name := range sortedKeys(c.Connections) {
		for i, entry := range c.Connections[name].Paths {
			dir, err := projectpath.Expand(entry)
			if err == nil {
				var info os.FileInfo
				if info, err = os.Stat(dir); err == nil && !info.IsDir() {
					err = fmt.Errorf("not a directory")
				}
			}
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("connection %q: paths[%d] names no existing directory, "+
					"so no project lies inside it", name, i))
			}
		}
	}
	return warnings
}

// UsesPaths reports whether any connection is bound to paths.
func (c *Config) UsesPaths() bool {
	for _, conn := range c.Connections {
		if len(conn.Paths) > 0 {
			return true
		}
	}
	return false
}

// ConnectionApplies reports whether the named connection may be discovered and run for a call made in
// projects, the project directories as projectpath.Roots returns them. A connection without paths applies
// everywhere. A bound one applies when at least one project equals one of its entries or lies below it,
// both with every symbolic link resolved; no project at all leaves a bound connection out.
func (c *Config) ConnectionApplies(name string, projects []string) bool {
	conn, ok := c.Connections[name]
	if !ok {
		return false
	}
	if len(conn.Paths) == 0 {
		return true
	}
	for _, entry := range conn.Paths {
		dir, err := projectpath.Expand(entry)
		if err != nil {
			continue
		}
		base := projectpath.Canonical(dir)
		for _, project := range projects {
			if projectpath.Within(project, base) {
				return true
			}
		}
	}
	return false
}

// ForProjects returns the view of c that a call made in projects may see: every connection that applies
// there, and only the defaults that point at one of them. Every other part is shared with c, so the view is
// read, never edited or saved. Discovery and execution work on this view, which is how a connection bound to
// another project is missing from every list, search, candidate list and default alike. A configuration
// that binds no connection is its own view.
func (c *Config) ForProjects(projects []string) *Config {
	if !c.UsesPaths() {
		return c
	}
	view := *c
	view.Connections = make(map[string]Connection, len(c.Connections))
	for name, conn := range c.Connections {
		if c.ConnectionApplies(name, projects) {
			view.Connections[name] = conn
		}
	}
	view.Defaults.Connections = make(map[string]string, len(c.Defaults.Connections))
	for domain, name := range c.Defaults.Connections {
		if _, ok := view.Connections[name]; ok {
			view.Defaults.Connections[domain] = name
		}
	}
	return &view
}
