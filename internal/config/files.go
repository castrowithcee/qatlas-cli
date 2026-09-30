package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/projectpath"
)

// LocalFiles is the direction in which a tool touches local files: it reads them (an upload) or writes them
// (a download). The empty value means the tool has no local file access.
type LocalFiles string

// The directions of local file access. A connection releases directories per direction, in Files.Read and
// Files.Write; the two are never mixed.
const (
	LocalFilesRead  LocalFiles = "read"
	LocalFilesWrite LocalFiles = "write"
)

// LocalFilesSupport states which directions of local file access a provider has tools for.
type LocalFilesSupport struct {
	Read  bool
	Write bool
}

// Files is the local directories a connection releases to its tools. Read lists the directories tools may
// read files from, Write those they may write files to. Without entries a connection gives no local file
// access; a directory gives access to it and everything below.
type Files struct {
	Read  []string `yaml:"read,omitempty"`
	Write []string `yaml:"write,omitempty"`
}

// Empty reports whether no directory is released in either direction.
func (f Files) Empty() bool { return len(f.Read) == 0 && len(f.Write) == 0 }

// Clone returns a copy of f that shares no list with it.
func (f Files) Clone() Files {
	return Files{Read: append([]string(nil), f.Read...), Write: append([]string(nil), f.Write...)}
}

// filesRule states what an entry that is too broad or unclear looks like. Like every rule it never quotes
// the entry: an entry is named by its direction and position.
const filesRule = "must name a directory below the file system root and below the home directory"

// localFilesOf returns the local file directions of the provider that conn's service belongs to; a service
// or provider that is unknown supports none.
func (c *Config) localFilesOf(conn Connection, providers ProviderCatalog) LocalFilesSupport {
	service, ok := c.Services[conn.Service]
	if !ok {
		return LocalFilesSupport{}
	}
	metadata, _ := providers.ProviderMetadata(service.Provider)
	return metadata.LocalFiles
}

// validateFiles checks the files lists of one connection. A direction the provider has no tool for is
// refused whole, so a released directory never rests unused on a connection that cannot use it. Every entry
// follows the rules of a paths entry and is, on top of that, neither the file system root, the whole home
// directory, nor a path with '..'. Messages name direction and position, never the path.
func validateFiles(name string, files Files, support LocalFilesSupport, report func(string, ...any)) {
	for _, direction := range []struct {
		name    string
		entries []string
		offered bool
	}{
		{"read", files.Read, support.Read},
		{"write", files.Write, support.Write},
	} {
		if direction.entries != nil && len(direction.entries) == 0 {
			report("connections.%s.files.%s: must name at least one directory; leave it out to release none",
				name, direction.name)
			continue
		}
		if len(direction.entries) > 0 && !direction.offered {
			report("connections.%s.files.%s: the provider of this connection has no tool that %ss local files",
				name, direction.name, direction.name)
			continue
		}
		seen := map[string]bool{}
		for i, entry := range direction.entries {
			if err := checkFilesEntry(entry); err != nil {
				report("connections.%s.files.%s[%d]: %s", name, direction.name, i, err)
				continue
			}
			key := filepath.Clean(entry)
			if seen[key] {
				report("connections.%s.files.%s[%d]: a directory is listed more than once", name, direction.name, i)
			}
			seen[key] = true
		}
	}
}

// checkFilesEntry checks one files entry: the paths rules, and no root, no bare home, no '..'.
func checkFilesEntry(entry string) error {
	if err := CheckPath(entry); err != nil {
		return err
	}
	for _, part := range strings.FieldsFunc(entry, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		if part == ".." {
			return fmt.Errorf("must not contain '..'; %s", filesRule)
		}
	}
	clean := filepath.Clean(entry)
	if clean == "~" || filepath.Dir(clean) == clean {
		return errors.New(filesRule)
	}
	return nil
}

// FilesWarnings names every files entry that does not name an existing directory, one line per entry,
// sorted by connection, reads before writes. Such an entry is valid, since the directory may be created
// later, but until then nothing can be read from or written to it. The line names connection, direction and
// position, never the path.
func (c *Config) FilesWarnings() []string {
	var warnings []string
	for _, name := range sortedKeys(c.Connections) {
		files := c.Connections[name].Files
		for _, direction := range []struct {
			name    string
			entries []string
		}{{"read", files.Read}, {"write", files.Write}} {
			for i, entry := range direction.entries {
				if !isExistingDir(entry) {
					warnings = append(warnings, fmt.Sprintf("connection %q: files.%s[%d] names no existing "+
						"directory, so no file can be %s there", name, direction.name, i,
						map[string]string{"read": "read from", "write": "written to"}[direction.name]))
				}
			}
		}
	}
	return warnings
}

// isExistingDir reports whether the paths entry names an existing directory, "~" expanded.
func isExistingDir(entry string) bool {
	dir, err := projectpath.Expand(entry)
	if err != nil {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
