package localfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// canary names the directory every test file lives below; no error may contain it.
const canary = "CANARY-9d2e"

// tree is a scratch file system: read and kunde-a are released for reading, write for writing, and outside
// is released for nothing.
type tree struct{ top, read, write, outside, kundeA, kundeAB string }

func newTree(t *testing.T) tree {
	t.Helper()
	top := filepath.Join(t.TempDir(), canary)
	tr := tree{
		top:     top,
		read:    filepath.Join(top, "read"),
		write:   filepath.Join(top, "write"),
		outside: filepath.Join(top, "outside"),
		kundeA:  filepath.Join(top, "kunde-a"),
		kundeAB: filepath.Join(top, "kunde-ab"),
	}
	for _, dir := range []string{tr.read, filepath.Join(tr.read, "sub"), tr.write, filepath.Join(tr.write, "sub"),
		tr.outside, tr.kundeA, tr.kundeAB} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(tr.read, "file.txt"), "hello")
	writeFile(t, filepath.Join(tr.read, "sub", "secret.txt"), "inside")
	writeFile(t, filepath.Join(tr.outside, "secret.txt"), "secret")
	writeFile(t, filepath.Join(tr.kundeAB, "file.txt"), "other customer")
	writeFile(t, filepath.Join(tr.write, "existing.txt"), "old")
	return tr
}

func (tr tree) resolved() *config.Resolved {
	return &config.Resolved{Files: config.Files{Read: []string{tr.read, tr.kundeA}, Write: []string{tr.write}}}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func symlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}
}

func sum(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

// checkError fails unless err is a *PathError with reason, or any *PathError for an empty reason, and
// names no path.
func checkPathError(t *testing.T, err error, reason string) {
	t.Helper()
	var pathErr *PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("error = %v, want a *PathError", err)
	}
	if reason != "" && pathErr.Reason != reason {
		t.Fatalf("reason = %q, want %q", pathErr.Reason, reason)
	}
	if pathErr.Argument != LocalPathArgument {
		t.Fatalf("argument = %q", pathErr.Argument)
	}
	checkNoPath(t, err)
}

func checkNoPath(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), canary) {
		t.Fatalf("error names a path: %v", err)
	}
}

// checkNoTemp fails when dir holds a temporary download file.
func checkNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), tempPrefix) {
			t.Fatalf("a temporary file is left behind: %s", entry.Name())
		}
	}
}

func withHook(t *testing.T, hook func()) {
	t.Helper()
	afterResolve = hook
	t.Cleanup(func() { afterResolve = nil })
}

func withLimit(t *testing.T, limit int64) {
	t.Helper()
	maxBytes = limit
	t.Cleanup(func() { maxBytes = MaxFileBytes })
}

func TestOpenForUpload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, tr tree) (path string, files *config.Files)
		content string // expected content; empty expects an error
		file    string // expected Name
		reason  string // expected reason of the *PathError; empty accepts any
	}{
		{name: "regular file", content: "hello", file: "file.txt",
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join(tr.read, "file.txt"), nil
			}},
		{name: "most specific release", content: "inside", file: "secret.txt",
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join(tr.read, "sub", "secret.txt"),
					&config.Files{Read: []string{tr.read, filepath.Join(tr.read, "sub")}}
			}},
		{name: "relative path", reason: reasonForm,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join("read", "file.txt"), nil
			}},
		{name: "bare home", reason: reasonForm,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) { return "~", nil }},
		{name: "empty", reason: reasonEmpty,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) { return "", nil }},
		{name: "dot dot", reason: reasonDotDot,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return tr.read + string(filepath.Separator) + ".." + string(filepath.Separator) +
					filepath.Join("read", "file.txt"), nil
			}},
		{name: "dot dot leading out", reason: reasonDotDot,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return tr.read + string(filepath.Separator) + ".." + string(filepath.Separator) +
					filepath.Join("outside", "secret.txt"), nil
			}},
		{name: "outside every release", reason: reasonOutside,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join(tr.outside, "secret.txt"), nil
			}},
		{name: "released directory itself", reason: reasonOutside,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) { return tr.read, nil }},
		{name: "wrong direction", reason: reasonOutside,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join(tr.write, "existing.txt"), nil
			}},
		{name: "prefix trap", reason: reasonOutside,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join(tr.kundeAB, "file.txt"), nil
			}},
		{name: "no release at all", reason: reasonOutside,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join(tr.read, "file.txt"), &config.Files{}
			}},
		{name: "missing file", reason: reasonMissing,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join(tr.read, "none.txt"), nil
			}},
		{name: "directory", reason: reasonNotRegular,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				return filepath.Join(tr.read, "sub"), nil
			}},
		{name: "hard link", reason: reasonHardLinks,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				writeFile(t, filepath.Join(tr.read, "one.txt"), "linked")
				if err := os.Link(filepath.Join(tr.read, "one.txt"), filepath.Join(tr.read, "two.txt")); err != nil {
					t.Skipf("hard links are not available: %v", err)
				}
				return filepath.Join(tr.read, "two.txt"), nil
			}},
		{name: "hard link to a file outside", reason: reasonHardLinks,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				if err := os.Link(filepath.Join(tr.outside, "secret.txt"), filepath.Join(tr.read, "hard.txt")); err != nil {
					t.Skipf("hard links are not available: %v", err)
				}
				return filepath.Join(tr.read, "hard.txt"), nil
			}},
		{name: "file symlink out", reason: reasonUnreachable,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, filepath.Join("..", "outside", "secret.txt"), filepath.Join(tr.read, "link.txt"))
				return filepath.Join(tr.read, "link.txt"), nil
			}},
		{name: "absolute file symlink out", reason: reasonUnreachable,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, filepath.Join(tr.outside, "secret.txt"), filepath.Join(tr.read, "link.txt"))
				return filepath.Join(tr.read, "link.txt"), nil
			}},
		{name: "directory symlink out", reason: reasonUnreachable,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, filepath.Join("..", "outside"), filepath.Join(tr.read, "dir"))
				return filepath.Join(tr.read, "dir", "secret.txt"), nil
			}},
		{name: "absolute directory symlink out", reason: reasonUnreachable,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, tr.outside, filepath.Join(tr.read, "dir"))
				return filepath.Join(tr.read, "dir", "secret.txt"), nil
			}},
		{name: "symlink inside", content: "hello", file: "link.txt",
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, "file.txt", filepath.Join(tr.read, "link.txt"))
				return filepath.Join(tr.read, "link.txt"), nil
			}},
		{name: "directory symlink inside", content: "inside", file: "secret.txt",
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, "sub", filepath.Join(tr.read, "dir"))
				return filepath.Join(tr.read, "dir", "secret.txt"), nil
			}},
		{name: "absolute symlink inside is refused", reason: reasonUnreachable,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, filepath.Join(tr.read, "file.txt"), filepath.Join(tr.read, "link.txt"))
				return filepath.Join(tr.read, "link.txt"), nil
			}},
		{name: "symlink from outside into a release", content: "inside", file: "secret.txt",
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, filepath.Join(tr.read, "sub"), filepath.Join(tr.outside, "into"))
				return filepath.Join(tr.outside, "into", "secret.txt"), nil
			}},
		{name: "released entry is a symlink, written form", content: "hello", file: "file.txt",
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, tr.read, filepath.Join(tr.top, "alias"))
				return filepath.Join(tr.top, "alias", "file.txt"),
					&config.Files{Read: []string{filepath.Join(tr.top, "alias")}}
			}},
		{name: "released entry is a symlink, resolved form", content: "hello", file: "file.txt",
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, tr.read, filepath.Join(tr.top, "alias"))
				return filepath.Join(tr.read, "file.txt"),
					&config.Files{Read: []string{filepath.Join(tr.top, "alias")}}
			}},
		{name: "released entry is a symlink, link out below it", reason: reasonUnreachable,
			prepare: func(t *testing.T, tr tree) (string, *config.Files) {
				symlink(t, tr.read, filepath.Join(tr.top, "alias"))
				symlink(t, filepath.Join("..", "outside"), filepath.Join(tr.read, "dir"))
				return filepath.Join(tr.top, "alias", "dir", "secret.txt"),
					&config.Files{Read: []string{filepath.Join(tr.top, "alias")}}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree(t)
			path, files := tc.prepare(t, tr)
			resolved := tr.resolved()
			if files != nil {
				resolved.Files = *files
			}
			upload, err := OpenForUpload(context.Background(), resolved, path)
			if tc.content == "" {
				if err == nil {
					upload.Close()
					t.Fatal("OpenForUpload succeeded, want an error")
				}
				checkPathError(t, err, tc.reason)
				return
			}
			if err != nil {
				t.Fatalf("OpenForUpload: %v", err)
			}
			defer upload.Close()
			if _, ok := upload.SHA256(); ok {
				t.Fatal("SHA256 must not be ready before the file is read")
			}
			data, err := io.ReadAll(upload)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if string(data) != tc.content || upload.Size != int64(len(tc.content)) || upload.Name != tc.file {
				t.Fatalf("got %q, size %d, name %q", data, upload.Size, upload.Name)
			}
			if got, ok := upload.SHA256(); !ok || got != sum(tc.content) {
				t.Fatalf("SHA256 = %q, %v", got, ok)
			}
		})
	}
}

func TestOpenForUploadHomeRelative(t *testing.T) {
	tr := newTree(t)
	t.Setenv("HOME", tr.top)
	t.Setenv("USERPROFILE", tr.top)
	resolved := &config.Resolved{Files: config.Files{Read: []string{"~/read"}}}
	upload, err := OpenForUpload(context.Background(), resolved, "~/read/file.txt")
	if err != nil {
		t.Fatalf("OpenForUpload: %v", err)
	}
	upload.Close()
	_, err = OpenForUpload(context.Background(), resolved, "~/outside/secret.txt")
	checkPathError(t, err, reasonOutside)
}

func TestOpenForUploadRace(t *testing.T) {
	tr := newTree(t)
	sub := filepath.Join(tr.read, "sub")
	withHook(t, func() {
		if err := os.Rename(sub, sub+"-old"); err != nil {
			t.Fatal(err)
		}
		symlink(t, filepath.Join("..", "outside"), sub)
	})
	_, err := OpenForUpload(context.Background(), tr.resolved(), filepath.Join(sub, "secret.txt"))
	checkPathError(t, err, reasonUnreachable)
}

// TestUploadSwappedFile swaps the file for another one between its check and its open.
func TestUploadSwappedFile(t *testing.T) {
	tr := newTree(t)
	name := filepath.Join(tr.read, "file.txt")
	beforeOpen = func() {
		if err := os.Rename(filepath.Join(tr.read, "sub", "secret.txt"), name); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeOpen = nil })
	_, err := OpenForUpload(context.Background(), tr.resolved(), name)
	checkPathError(t, err, reasonChanged)
}

func TestOpenForUploadLimit(t *testing.T) {
	tr := newTree(t)
	withLimit(t, 4)
	_, err := OpenForUpload(context.Background(), tr.resolved(), filepath.Join(tr.read, "file.txt"))
	checkPathError(t, err, "names a file larger than the limit of 4 bytes")
	withLimit(t, 5)
	upload, err := OpenForUpload(context.Background(), tr.resolved(), filepath.Join(tr.read, "file.txt"))
	if err != nil {
		t.Fatalf("a file at the limit: %v", err)
	}
	upload.Close()
}

func TestUploadChangedWhileRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(name string) error
	}{
		{"grown", func(name string) error {
			f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.WriteString(" world")
			return err
		}},
		{"shrunk", func(name string) error { return os.Truncate(name, 2) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree(t)
			name := filepath.Join(tr.read, "file.txt")
			upload, err := OpenForUpload(context.Background(), tr.resolved(), name)
			if err != nil {
				t.Fatal(err)
			}
			defer upload.Close()
			if err := tc.change(name); err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(upload)
			if !errors.Is(err, errChanged) {
				t.Fatalf("error = %v, want errChanged", err)
			}
			if _, ok := upload.SHA256(); ok {
				t.Fatal("SHA256 must not be ready for a file that changed")
			}
		})
	}
}

func TestUploadCanceled(t *testing.T) {
	tr := newTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	upload, err := OpenForUpload(ctx, tr.resolved(), filepath.Join(tr.read, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Close()
	cancel()
	if _, err := io.ReadAll(upload); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// download runs a whole download of content into path.
func download(ctx context.Context, resolved *config.Resolved, path, content string) (*Download, error) {
	d, err := CreateForDownload(ctx, resolved, path)
	if err != nil {
		return nil, err
	}
	if _, err := d.ReadFrom(strings.NewReader(content)); err != nil {
		d.Abort()
		return d, err
	}
	return d, d.Commit()
}

func TestCreateForDownload(t *testing.T) {
	confirmed := capability.WithConfirmed(context.Background())
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		prepare func(t *testing.T, tr tree) string
		want    string // expected content of the target afterwards; empty expects no target
		err     error  // expected error; nil expects success
		reason  string // expected reason of a *PathError
	}{
		{name: "new file", ctx: context.Background(), want: "data",
			prepare: func(t *testing.T, tr tree) string { return filepath.Join(tr.write, "new.txt") }},
		{name: "new file in a subdirectory", ctx: context.Background(), want: "data",
			prepare: func(t *testing.T, tr tree) string { return filepath.Join(tr.write, "sub", "new.txt") }},
		{name: "existing file without confirmation", ctx: context.Background(), want: "old",
			err:     ErrOverwriteNeedsConfirmation,
			prepare: func(t *testing.T, tr tree) string { return filepath.Join(tr.write, "existing.txt") }},
		{name: "existing file with confirmation", ctx: confirmed, want: "data",
			prepare: func(t *testing.T, tr tree) string { return filepath.Join(tr.write, "existing.txt") }},
		{name: "missing parent directory", ctx: confirmed, reason: reasonNoParent,
			prepare: func(t *testing.T, tr tree) string { return filepath.Join(tr.write, "none", "new.txt") }},
		{name: "wrong direction", ctx: confirmed, reason: reasonOutside,
			prepare: func(t *testing.T, tr tree) string { return filepath.Join(tr.read, "new.txt") }},
		{name: "outside every release", ctx: confirmed, reason: reasonOutside,
			prepare: func(t *testing.T, tr tree) string { return filepath.Join(tr.outside, "new.txt") }},
		{name: "relative path", ctx: confirmed, reason: reasonForm,
			prepare: func(t *testing.T, tr tree) string { return "new.txt" }},
		{name: "dot dot", ctx: confirmed, reason: reasonDotDot,
			prepare: func(t *testing.T, tr tree) string {
				return tr.write + string(filepath.Separator) + ".." + string(filepath.Separator) + "new.txt"
			}},
		{name: "directory", ctx: confirmed, reason: reasonNotRegular,
			prepare: func(t *testing.T, tr tree) string { return filepath.Join(tr.write, "sub") }},
		{name: "symlink target", ctx: confirmed, reason: reasonNotRegular,
			prepare: func(t *testing.T, tr tree) string {
				symlink(t, "existing.txt", filepath.Join(tr.write, "link.txt"))
				return filepath.Join(tr.write, "link.txt")
			}},
		{name: "directory symlink out", ctx: confirmed, reason: reasonUnreachable,
			prepare: func(t *testing.T, tr tree) string {
				symlink(t, filepath.Join("..", "outside"), filepath.Join(tr.write, "dir"))
				return filepath.Join(tr.write, "dir", "new.txt")
			}},
		{name: "absolute directory symlink out", ctx: confirmed, reason: reasonUnreachable,
			prepare: func(t *testing.T, tr tree) string {
				symlink(t, tr.outside, filepath.Join(tr.write, "dir"))
				return filepath.Join(tr.write, "dir", "secret.txt")
			}},
		{name: "directory symlink inside", ctx: context.Background(), want: "data",
			prepare: func(t *testing.T, tr tree) string {
				symlink(t, "sub", filepath.Join(tr.write, "dir"))
				return filepath.Join(tr.write, "dir", "new.txt")
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree(t)
			path := tc.prepare(t, tr)
			d, err := download(tc.ctx, tr.resolved(), path, "data")
			switch {
			case tc.reason != "":
				checkPathError(t, err, tc.reason)
			case tc.err != nil:
				if !errors.Is(err, tc.err) {
					t.Fatalf("error = %v, want %v", err, tc.err)
				}
				checkNoPath(t, err)
			case err != nil:
				t.Fatalf("download: %v", err)
			default:
				if got, ok := d.SHA256(); !ok || got != sum("data") || d.Size() != 4 {
					t.Fatalf("SHA256 = %q, %v, size %d", got, ok, d.Size())
				}
			}
			if tc.want != "" {
				if got := readFile(t, path); got != tc.want {
					t.Fatalf("content = %q, want %q", got, tc.want)
				}
				if info, err := os.Lstat(path); err != nil || (runtime.GOOS != "windows" && tc.err == nil &&
					info.Mode().Perm() != 0o600) {
					t.Fatalf("target: %v, %v", info.Mode(), err)
				}
			}
			if readFile(t, filepath.Join(tr.outside, "secret.txt")) != "secret" {
				t.Fatal("the file outside changed")
			}
			for _, dir := range []string{tr.write, filepath.Join(tr.write, "sub"), tr.outside} {
				checkNoTemp(t, dir)
			}
			if _, err := os.Lstat(filepath.Join(tr.outside, "new.txt")); err == nil {
				t.Fatal("a file was written outside")
			}
		})
	}
}

func TestDownloadAbort(t *testing.T) {
	for _, name := range []string{"new.txt", "existing.txt"} {
		t.Run(name, func(t *testing.T) {
			tr := newTree(t)
			path := filepath.Join(tr.write, name)
			d, err := CreateForDownload(capability.WithConfirmed(context.Background()), tr.resolved(), path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Write([]byte("partial")); err != nil {
				t.Fatal(err)
			}
			if err := d.Abort(); err != nil {
				t.Fatal(err)
			}
			if err := d.Abort(); err != nil {
				t.Fatalf("a second Abort: %v", err)
			}
			if err := d.Commit(); err == nil {
				t.Fatal("Commit after Abort must fail")
			}
			checkNoTemp(t, tr.write)
			if name == "new.txt" {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the target exists after Abort: %v", err)
				}
			} else if got := readFile(t, path); got != "old" {
				t.Fatalf("content = %q after Abort", got)
			}
		})
	}
}

func TestDownloadIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int64
		run   func(d *Download) error
	}{
		{name: "shorter than reported", run: func(d *Download) error {
			if err := d.ExpectSize(10); err != nil {
				return err
			}
			if _, err := d.Write([]byte("data")); err != nil {
				return err
			}
			return d.Commit()
		}},
		{name: "longer than reported by Write", run: func(d *Download) error {
			if err := d.ExpectSize(2); err != nil {
				return err
			}
			if _, err := d.Write([]byte("data")); err != nil {
				return err
			}
			return d.Commit()
		}},
		{name: "longer than reported by ReadFrom", run: func(d *Download) error {
			if err := d.ExpectSize(2); err != nil {
				return err
			}
			if _, err := d.ReadFrom(strings.NewReader("data")); err != nil {
				return err
			}
			return d.Commit()
		}},
		{name: "reported after more was written", run: func(d *Download) error {
			if _, err := d.Write([]byte("data")); err != nil {
				return err
			}
			if err := d.ExpectSize(2); err != nil {
				return err
			}
			return d.Commit()
		}},
		{name: "checksum mismatch", run: func(d *Download) error {
			if err := d.ExpectSHA256(sum("other")); err != nil {
				return err
			}
			if _, err := d.Write([]byte("data")); err != nil {
				return err
			}
			return d.Commit()
		}},
		{name: "malformed checksum", run: func(d *Download) error { return d.ExpectSHA256("abc") }},
		{name: "above the limit", limit: 3, run: func(d *Download) error {
			if _, err := d.ReadFrom(strings.NewReader("data")); err != nil {
				return err
			}
			return d.Commit()
		}},
		{name: "reported above the limit", limit: 3, run: func(d *Download) error { return d.ExpectSize(4) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.limit > 0 {
				withLimit(t, tc.limit)
			}
			tr := newTree(t)
			path := filepath.Join(tr.write, "new.txt")
			d, err := CreateForDownload(context.Background(), tr.resolved(), path)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.run(d)
			var integrity *IntegrityError
			if !errors.As(err, &integrity) {
				t.Fatalf("error = %v, want an *IntegrityError", err)
			}
			checkNoPath(t, err)
			if err := d.Commit(); err == nil {
				t.Fatal("Commit after an integrity error must fail")
			}
			d.Abort()
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the target exists: %v", err)
			}
			checkNoTemp(t, tr.write)
		})
	}
}

func TestDownloadMatchingChecksumAndSize(t *testing.T) {
	tr := newTree(t)
	withLimit(t, 4)
	path := filepath.Join(tr.write, "new.txt")
	d, err := CreateForDownload(context.Background(), tr.resolved(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ExpectSize(4); err != nil {
		t.Fatal(err)
	}
	if err := d.ExpectSHA256(strings.ToUpper(sum("data"))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReadFrom(strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "data" {
		t.Fatalf("content = %q", got)
	}
}

func TestDownloadTargetAppearsDuringTransfer(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), capability.WithConfirmed(context.Background())} {
		tr := newTree(t)
		path := filepath.Join(tr.write, "new.txt")
		d, err := CreateForDownload(ctx, tr.resolved(), path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Write([]byte("data")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, "someone else's")
		if err := d.Commit(); !errors.Is(err, ErrOverwriteNeedsConfirmation) {
			t.Fatalf("Commit = %v, want ErrOverwriteNeedsConfirmation", err)
		}
		if got := readFile(t, path); got != "someone else's" {
			t.Fatalf("content = %q", got)
		}
		checkNoTemp(t, tr.write)
	}
}

func TestDownloadTargetReplacedByDirectory(t *testing.T) {
	tr := newTree(t)
	path := filepath.Join(tr.write, "existing.txt")
	d, err := CreateForDownload(capability.WithConfirmed(context.Background()), tr.resolved(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	checkPathError(t, d.Commit(), reasonNotRegular)
	checkNoTemp(t, tr.write)
}

func TestCreateForDownloadRace(t *testing.T) {
	tr := newTree(t)
	sub := filepath.Join(tr.write, "sub")
	withHook(t, func() {
		if err := os.Rename(sub, sub+"-old"); err != nil {
			t.Fatal(err)
		}
		symlink(t, filepath.Join("..", "outside"), sub)
	})
	_, err := download(context.Background(), tr.resolved(), filepath.Join(sub, "secret.txt"), "data")
	checkPathError(t, err, reasonUnreachable)
	if got := readFile(t, filepath.Join(tr.outside, "secret.txt")); got != "secret" {
		t.Fatalf("the file outside changed: %q", got)
	}
}

// TestDownloadDirectoryPinned swaps the target's directory for a link out after the download started: the
// file still lands in the directory that was checked.
func TestDownloadDirectoryPinned(t *testing.T) {
	tr := newTree(t)
	sub := filepath.Join(tr.write, "sub")
	d, err := CreateForDownload(context.Background(), tr.resolved(), filepath.Join(sub, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(sub, sub+"-old"); err != nil {
		t.Skipf("an open directory cannot be renamed here: %v", err)
	}
	symlink(t, tr.outside, sub)
	if err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(tr.outside, "new.txt")); err == nil {
		t.Fatal("the download landed outside")
	}
	if got := readFile(t, filepath.Join(sub+"-old", "new.txt")); got != "data" {
		t.Fatalf("content = %q", got)
	}
}

func TestDownloadNoPartialFileWhileWriting(t *testing.T) {
	tr := newTree(t)
	path := filepath.Join(tr.write, "new.txt")
	d, err := CreateForDownload(context.Background(), tr.resolved(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Abort()
	if _, err := d.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the target is visible before Commit: %v", err)
	}
}

func TestDownloadCanceled(t *testing.T) {
	tr := newTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	d, err := CreateForDownload(ctx, tr.resolved(), filepath.Join(tr.write, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := d.Write([]byte("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write = %v, want context.Canceled", err)
	}
	if err := d.Commit(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Commit = %v, want context.Canceled", err)
	}
	checkNoTemp(t, tr.write)
}

func TestLocateMostSpecific(t *testing.T) {
	tr := newTree(t)
	sub := filepath.Join(tr.read, "sub")
	resolved := &config.Resolved{Files: config.Files{Read: []string{tr.read, sub}}}
	loc, err := locate(resolved, config.LocalFilesRead, filepath.Join(sub, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	if loc.root != canonical || loc.rel != "secret.txt" {
		t.Fatalf("location = %+v", loc)
	}
}

func TestUploadSource(t *testing.T) {
	value := "x"
	for _, tc := range []struct {
		name          string
		path, content *string
		want          Source
	}{
		{"local path", &value, nil, SourceLocalPath},
		{"content", nil, &value, SourceContent},
		{"both", &value, &value, 0},
		{"neither", nil, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := UploadSource(tc.path, tc.content)
			if tc.want == 0 {
				checkPathError(t, err, "")
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("UploadSource = %v, %v", got, err)
			}
		})
	}
}

func TestPathErrorMessage(t *testing.T) {
	err := pathError(config.LocalFilesWrite, reasonOutside)
	want := "$.local_path " + reasonOutside + " (files.write)"
	if err.Error() != want {
		t.Fatalf("message = %q, want %q", err.Error(), want)
	}
}

func TestFailureDropsThePath(t *testing.T) {
	_, err := os.Open(filepath.Join(t.TempDir(), canary, "missing"))
	wrapped := failure("reading", err)
	checkNoPath(t, wrapped)
	if !errors.Is(wrapped, os.ErrNotExist) {
		t.Fatalf("failure lost the reason: %v", wrapped)
	}
}
