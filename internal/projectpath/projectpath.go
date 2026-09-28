// Package projectpath resolves the project a call runs in and decides whether a project lies inside a
// configured path. A connection bound to paths is offered only to a project inside one of them; this
// package answers both halves of that question from the file system alone, without starting a process.
//
// The binding keeps the connections of one project apart from those of another, so an agent working in
// one project does not pick the route of another by mistake. It is no barrier against an agent that
// changes into a bound directory on purpose.
package projectpath

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Expand returns entry with a leading "~" replaced by the user's home directory. Only "~" itself and "~"
// followed by a separator are expanded; any other entry is returned as it is.
func Expand(entry string) (string, error) {
	if !IsHomeRelative(entry) {
		return entry, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot determine the user home directory")
	}
	return filepath.Join(home, entry[1:]), nil
}

// IsHomeRelative reports whether entry is "~" or starts with "~" and a path separator of the running
// platform, the only home-relative form Expand accepts. "~user" is not one.
func IsHomeRelative(entry string) bool {
	if entry == "~" {
		return true
	}
	if !strings.HasPrefix(entry, "~") || len(entry) < 2 {
		return false
	}
	return os.IsPathSeparator(entry[1])
}

// Canonical returns the absolute, cleaned form of dir with every symbolic link resolved. A directory that
// does not exist, or whose links cannot be resolved, keeps its cleaned absolute form, so a configured path
// that is created later still compares by its written form.
func Canonical(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// Roots returns the directories that stand for the project dir lies in: the root of the Git working tree
// that contains dir, or dir itself outside any, and for a linked worktree also the main working tree of the
// same repository, so a worktree checked out elsewhere belongs to the project of its repository. Every
// returned directory is Canonical.
func Roots(dir string) []string {
	dir = Canonical(dir)
	for current := dir; ; {
		marker := filepath.Join(current, ".git")
		if isGitDir(marker) {
			return []string{current}
		}
		if line, ok := readLine(marker); ok && strings.HasPrefix(line, "gitdir:") {
			roots := []string{current}
			if main := mainWorktree(current, marker); main != "" && main != current {
				roots = append(roots, main)
			}
			return roots
		}
		parent := filepath.Dir(current)
		if parent == current {
			return []string{dir}
		}
		current = parent
	}
}

// isGitDir reports whether marker is the Git directory of a main working tree. Like Git itself it takes a
// directory for one only when it holds HEAD, so an empty ".git" directory left somewhere above a project does
// not swallow it.
func isGitDir(marker string) bool {
	if info, err := os.Stat(marker); err != nil || !info.IsDir() {
		return false
	}
	_, err := os.Stat(filepath.Join(marker, "HEAD"))
	return err == nil
}

// maxGitFile bounds what is read of a .git, commondir or gitdir file; each holds a single path.
const maxGitFile = 4096

// mainWorktree returns the main working tree of the linked worktree at root, whose .git file is marker, or
// "" when marker does not describe one. A linked worktree's .git file names its private Git directory, that
// directory's commondir file names the repository's shared Git directory, and the main working tree is the
// directory that holds it as ".git". The worktree must also be the one its private Git directory points
// back to, as Git itself requires, so a stray .git file cannot claim the project of another repository by
// naming it. A submodule or a worktree of a bare repository has no main working tree.
func mainWorktree(root, marker string) string {
	line, ok := readLine(marker)
	gitDir, found := strings.CutPrefix(line, "gitdir:")
	if !ok || !found {
		return ""
	}
	gitDir = resolveFrom(root, strings.TrimSpace(gitDir))
	back, ok := readLine(filepath.Join(gitDir, "gitdir"))
	if !ok || Canonical(resolveFrom(gitDir, back)) != Canonical(marker) {
		return ""
	}
	common, ok := readLine(filepath.Join(gitDir, "commondir"))
	if !ok {
		return ""
	}
	common = Canonical(resolveFrom(gitDir, common))
	if filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}

// readLine returns the first line of a small file, trimmed.
func readLine(name string) (string, bool) {
	f, err := os.Open(name)
	if err != nil {
		return "", false
	}
	defer f.Close()
	line, err := bufio.NewReader(io.LimitReader(f, maxGitFile)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false
	}
	line = strings.TrimSpace(line)
	return line, line != ""
}

// resolveFrom interprets name relative to base unless it is absolute.
func resolveFrom(base, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Join(base, name)
}

// Within reports whether dir equals base or lies below it. Both are compared whole path segment by whole
// path segment, so "/repos/kunde-a" does not contain "/repos/kunde-ab", and under the rules of the running
// platform: on Windows without regard to case, separator style, or the case of the drive letter.
func Within(dir, base string) bool {
	return within(dir, base, runtime.GOOS == "windows")
}

func within(dir, base string, windows bool) bool {
	d, b := normalize(dir, windows), normalize(base, windows)
	if d == b {
		return true
	}
	if !strings.HasSuffix(b, "/") {
		b += "/"
	}
	return strings.HasPrefix(d, b)
}

// normalize returns the comparison form of an absolute path, with "/" as its only separator. A Windows path
// is folded to lower case, which also folds its drive letter, loses a leading \\?\ prefix, and keeps a UNC
// path's leading double separator.
func normalize(p string, windows bool) string {
	if !windows {
		return path.Clean(p)
	}
	p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	if rest, ok := strings.CutPrefix(p, "//?/"); ok {
		p = rest
		if unc, ok := strings.CutPrefix(p, "unc/"); ok {
			p = "//" + unc
		}
	}
	unc := strings.HasPrefix(p, "//")
	p = path.Clean(p)
	if unc && !strings.HasPrefix(p, "//") {
		p = "/" + p
	}
	return p
}
