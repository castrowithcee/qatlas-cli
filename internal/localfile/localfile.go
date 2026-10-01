// Package localfile reads the local files a tool uploads and writes the local files a tool downloads, both
// only inside the directories a connection releases for that direction: Files.Read for uploads, Files.Write
// for downloads.
//
// A path is accepted only when it is absolute or starts with "~/", contains no "..", and lies below a
// released directory, compared whole path segment by whole path segment. The released directory and the
// path both count in their written form and with symbolic links resolved: the released directory whole, the
// path up to its parent. Every file operation then runs through an os.Root opened on the resolved released
// directory, so a symbolic link that leads out of it, or one swapped in after the check, fails instead of
// being followed. Symbolic links that stay inside the released directory are followed.
//
// Only regular files are read or written. An upload refuses a file with more than one hard link. A download
// never creates a parent directory, never leaves a partial file behind, and replaces an existing file only
// when the context carries the request's confirmation; without it, it returns ErrOverwriteNeedsConfirmation
// and changes nothing.
//
// No error of this package names a path: a path error names the argument and the direction, and a failed
// file operation names the operation and the system's reason.
package localfile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/projectpath"
)

// MaxFileBytes bounds every file this package reads or writes: 10 GiB.
const MaxFileBytes int64 = 10 << 30

// maxBytes is the bound in force; tests lower it.
var maxBytes = MaxFileBytes

// afterResolve runs between the check of a path and the first file operation on it; tests use it to change
// the file system in between.
var afterResolve func()

// beforeOpen runs between the check of the file an upload reads and its open; tests use it to swap the file.
var beforeOpen func()

// The names of the arguments that carry file content. An upload tool takes exactly one of LocalPathArgument
// and ContentArgument; a download tool writes the file to LocalPathArgument when it is given and returns the
// content in ContentArgument otherwise.
const (
	LocalPathArgument = "local_path"
	ContentArgument   = "content_base64"
)

// LocalPathSchema is the JSON schema of the local_path argument, for a tool's input schema.
const LocalPathSchema = `{"type":"string","minLength":1,"maxLength":4096}`

// UploadPathArgument is the discovery metadata of local_path for an upload tool.
func UploadPathArgument() capability.Argument {
	return capability.Argument{
		Name: LocalPathArgument,
		Description: "Local file to upload, absolute or starting with ~/, inside a directory the connection " +
			"releases for reading; instead of " + ContentArgument,
	}
}

// DownloadPathArgument is the discovery metadata of local_path for a download tool.
func DownloadPathArgument() capability.Argument {
	return capability.Argument{
		Name: LocalPathArgument,
		Description: "Local file to write, absolute or starting with ~/, in an existing directory the " +
			"connection releases for writing; an existing file is replaced only with confirmation",
	}
}

// Source says where the content of an upload comes from.
type Source int

// The sources of an upload's content.
const (
	SourceContent   Source = iota + 1 // content_base64, inline in the request
	SourceLocalPath                   // local_path, a local file
)

// UploadSource picks the source of an upload from the arguments given, nil for an argument that is absent.
// Exactly one of the two must be given; otherwise it returns a *PathError.
func UploadSource(localPath, content *string) (Source, error) {
	switch {
	case localPath != nil && content == nil:
		return SourceLocalPath, nil
	case localPath == nil && content != nil:
		return SourceContent, nil
	}
	return 0, &PathError{Argument: LocalPathArgument, Direction: config.LocalFilesRead,
		Reason: "and " + ContentArgument + " exclude each other; exactly one of them is required"}
}

// ErrOverwriteNeedsConfirmation reports that a download would replace an existing local file and the
// context carries no confirmation. Nothing was written; the same request with confirmation replaces it.
var ErrOverwriteNeedsConfirmation = errors.New("the local file already exists; replacing it needs confirmation")

// PathError reports a local path the request may not use: malformed, outside every directory released for
// its direction, or naming something other than a regular file. Its message names the argument and the
// direction, never the path.
type PathError struct {
	Argument  string
	Direction config.LocalFiles
	Reason    string
}

func (e *PathError) Error() string {
	return fmt.Sprintf("$.%s %s (files.%s)", e.Argument, e.Reason, e.Direction)
}

// IntegrityError reports downloaded content that does not match what the provider announced, or exceeds
// the size limit. Nothing was written.
type IntegrityError struct{ Reason string }

func (e *IntegrityError) Error() string { return "the downloaded content " + e.Reason }

// The reasons of a PathError. None quotes the path.
const (
	reasonEmpty       = "must not be empty"
	reasonForm        = "must be an absolute path or start with ~/"
	reasonDotDot      = "must not contain '..'"
	reasonOutside     = "is not inside a directory the connection releases for this direction"
	reasonUnreachable = "cannot be reached inside a directory the connection releases for this direction"
	reasonMissing     = "names no existing file"
	reasonNoParent    = "names a directory that does not exist"
	reasonNotRegular  = "does not name a regular file"
	reasonChanged     = "changed while it was opened"
	reasonHardLinks   = "names a file with more than one hard link"
)

func pathError(direction config.LocalFiles, reason string) *PathError {
	return &PathError{Argument: LocalPathArgument, Direction: direction, Reason: reason}
}

// location is a path found inside a released directory: root is the released directory with every
// symbolic link resolved, rel the path below it.
type location struct {
	root string
	rel  string
	name string // the last element of the path as written
}

// locate finds the released directory of direction that path lies in. Of several, the most specific wins,
// the one with the most path segments, so the operation is confined as narrowly as possible.
func locate(resolved *config.Resolved, direction config.LocalFiles, path string) (location, error) {
	switch {
	case path == "":
		return location{}, pathError(direction, reasonEmpty)
	case strings.ContainsRune(path, 0),
		!filepath.IsAbs(path) && (path == "~" || !projectpath.IsHomeRelative(path)):
		return location{}, pathError(direction, reasonForm)
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || isSeparator(r) }) {
		if part == ".." {
			return location{}, pathError(direction, reasonDotDot)
		}
	}
	expanded, err := projectpath.Expand(path)
	if err != nil || !filepath.IsAbs(expanded) {
		return location{}, pathError(direction, reasonForm)
	}
	target := filepath.Clean(expanded)
	targets := []string{target}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(target)); err == nil {
		if joined := filepath.Join(parent, filepath.Base(target)); joined != target {
			targets = append(targets, joined)
		}
	}

	var entries []string
	if resolved != nil {
		switch direction {
		case config.LocalFilesRead:
			entries = resolved.Files.Read
		case config.LocalFilesWrite:
			entries = resolved.Files.Write
		}
	}
	best, bestDepth := location{}, -1
	for _, entry := range entries {
		if config.CheckFilesEntry(entry) != nil {
			continue
		}
		dir, err := projectpath.Expand(entry)
		if err != nil {
			continue
		}
		written := filepath.Clean(dir)
		canonical, err := filepath.EvalSymlinks(written)
		if err != nil {
			continue // a released directory that does not exist holds no file
		}
		for _, form := range []string{written, canonical} {
			for _, candidate := range targets {
				if !projectpath.Within(candidate, form) {
					continue
				}
				rel, err := filepath.Rel(form, candidate)
				if err != nil || rel == "." || !filepath.IsLocal(rel) || !validName(rel) {
					continue
				}
				if depth := segments(form); depth > bestDepth {
					best, bestDepth = location{root: canonical, rel: rel, name: filepath.Base(target)}, depth
				}
			}
		}
	}
	if bestDepth < 0 {
		return location{}, pathError(direction, reasonOutside)
	}
	return best, nil
}

// segments counts the path segments of a clean absolute path.
func segments(path string) int {
	return len(strings.FieldsFunc(path, isSeparator))
}

// isSeparator reports whether r is a path separator of the running platform.
func isSeparator(r rune) bool { return r < utf8.RuneSelf && os.IsPathSeparator(uint8(r)) }

// openRoot opens the released directory of loc, after the hook tests use to race the check.
func openRoot(loc location, direction config.LocalFiles) (*os.Root, error) {
	if afterResolve != nil {
		afterResolve()
	}
	root, err := os.OpenRoot(loc.root)
	if err != nil {
		return nil, pathError(direction, reasonUnreachable)
	}
	return root, nil
}

// formatBytes names a number of bytes for a message.
func formatBytes(n int64) string { return strconv.FormatInt(n, 10) + " bytes" }

// failure turns an error of a file operation into one without a path: it keeps the system's reason, such as
// "no space left on device", and drops the path error around it, which would name the file.
func failure(action string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	for {
		var pathErr *fs.PathError
		var linkErr *os.LinkError
		var syscallErr *os.SyscallError
		switch {
		case errors.As(err, &pathErr):
			err = pathErr.Err
		case errors.As(err, &linkErr):
			err = linkErr.Err
		case errors.As(err, &syscallErr):
			err = syscallErr.Err
		default:
			return fmt.Errorf("%s the local file failed: %w", action, err)
		}
	}
}
