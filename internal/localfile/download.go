package localfile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// Download is a local file being written for a download. The content goes to a temporary file in the
// target's directory, created exclusively and readable only by the user; only Commit makes it visible under
// the target's name, and Abort, like a failed Commit, removes it. No partial file is left under either name.
//
// Without overwrite, Commit links the temporary file to the target's name, which fails when a file of that
// name exists, so a file that appeared during the transfer is never replaced. With overwrite, which needs
// the confirmation of the request and a regular file at the target when the download starts, Commit renames
// the temporary file over it.
type Download struct {
	ctx       context.Context
	dir       *os.Root
	name      string
	temp      string
	file      *os.File
	overwrite bool

	hash         hash.Hash
	written      int64
	expectSize   int64
	expectSHA256 string
	err          error
	committed    bool
	finished     bool
}

// CreateForDownload prepares writing the regular file at path, if its directory exists and lies inside a
// directory the connection releases for writing. A path the request may not use yields a *PathError. A
// file that exists already yields ErrOverwriteNeedsConfirmation unless ctx carries the request's
// confirmation, see capability.WithConfirmed; nothing is created then.
func CreateForDownload(ctx context.Context, resolved *config.Resolved, path string) (*Download, error) {
	const direction = config.LocalFilesWrite
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	loc, err := locate(resolved, direction, path)
	if err != nil {
		return nil, err
	}
	root, err := openRoot(loc, direction)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	// Every later operation runs on the directory opened here, by the last element of the name alone, so a
	// directory swapped in later does not redirect it.
	parent, base := splitRel(loc.rel)
	dir, err := root.OpenRoot(parent)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, pathError(direction, reasonNoParent)
	case err != nil:
		return nil, pathError(direction, reasonUnreachable)
	}
	d := &Download{ctx: ctx, dir: dir, name: base, hash: sha256.New(), expectSize: -1}
	if err := d.checkTarget(direction); err != nil {
		dir.Close()
		return nil, err
	}
	if err := d.createTemp(); err != nil {
		dir.Close()
		return nil, err
	}
	return d, nil
}

// splitRel splits a relative path into its directory, "." for none, and its last element.
func splitRel(rel string) (string, string) {
	i := len(rel) - 1
	for i >= 0 && !isSeparator(rune(rel[i])) {
		i--
	}
	if i < 0 {
		return ".", rel
	}
	return rel[:i], rel[i+1:]
}

// checkTarget decides whether the download creates the target or replaces it.
func (d *Download) checkTarget(direction config.LocalFiles) error {
	info, err := d.dir.Lstat(d.name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return pathError(direction, reasonUnreachable)
	case !info.Mode().IsRegular():
		return pathError(direction, reasonNotRegular)
	case !capability.Confirmed(d.ctx):
		return ErrOverwriteNeedsConfirmation
	}
	d.overwrite = true
	return nil
}

// tempPrefix starts the name of every temporary download file, which a crash may leave behind.
const tempPrefix = ".qatlas-download-"

// createTemp creates the temporary file exclusively, under a fresh random name.
func (d *Download) createTemp() error {
	for attempt := 0; ; attempt++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return failure("creating", err)
		}
		name := tempPrefix + hex.EncodeToString(random[:]) + ".part"
		file, err := d.dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) && attempt < 8 {
			continue
		}
		if err != nil {
			return failure("creating", err)
		}
		d.temp, d.file = name, file
		return nil
	}
}

// ExpectSize announces the size the provider reported. Writing more fails at once, and Commit fails when
// less was written. A size above the limit fails at once. Any error makes the download unusable: Commit
// then fails, and only Abort remains.
func (d *Download) ExpectSize(size int64) error {
	switch {
	case size < 0:
		return d.fail(&IntegrityError{Reason: "has a negative reported size"})
	case size > maxBytes:
		return d.fail(&IntegrityError{Reason: "is larger than the limit of " + formatBytes(maxBytes)})
	case d.written > size:
		return d.fail(&IntegrityError{Reason: "is longer than the reported size"})
	}
	d.expectSize = size
	return nil
}

// ExpectSHA256 announces the SHA-256 checksum the provider reported, as hex; Commit fails when the content
// has another one. A checksum that is no SHA-256 makes the download unusable.
func (d *Download) ExpectSHA256(sum string) error {
	sum = strings.ToLower(sum)
	if decoded, err := hex.DecodeString(sum); err != nil || len(decoded) != sha256.Size {
		return d.fail(&IntegrityError{Reason: "has a reported checksum that is no SHA-256"})
	}
	d.expectSHA256 = sum
	return nil
}

// limit is the most bytes the download may still hold in total.
func (d *Download) limit() int64 {
	if d.expectSize >= 0 && d.expectSize < maxBytes {
		return d.expectSize
	}
	return maxBytes
}

// Write appends p to the temporary file. It refuses content beyond the reported size or the limit.
func (d *Download) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	if d.finished {
		return 0, errFinished
	}
	if err := d.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > d.limit()-d.written {
		if d.expectSize >= 0 {
			return 0, d.fail(&IntegrityError{Reason: "is longer than the reported size"})
		}
		return 0, d.fail(&IntegrityError{Reason: "is larger than the limit of " + formatBytes(maxBytes)})
	}
	n, err := d.file.Write(p)
	d.hash.Write(p[:n])
	d.written += int64(n)
	if err != nil {
		return n, d.fail(failure("writing", err))
	}
	return n, nil
}

// ReadFrom copies r into the download, reading at most one byte beyond what it may still hold, so a
// response longer than announced is detected without being read whole.
func (d *Download) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{d}, io.LimitReader(r, d.limit()-d.written+1))
}

// fail records the first error that makes the download unusable.
func (d *Download) fail(err error) error {
	if d.err == nil {
		d.err = err
	}
	return err
}

// errFinished reports a download used after Commit or Abort.
var errFinished = errors.New("the local file is already finished")

// Size returns the number of bytes written so far.
func (d *Download) Size() int64 { return d.written }

// SHA256 returns the lowercase hex SHA-256 of the content once Commit succeeded; before, it reports false.
func (d *Download) SHA256() (string, bool) {
	if !d.committed {
		return "", false
	}
	return hex.EncodeToString(d.hash.Sum(nil)), true
}

// Commit checks the content against what the provider announced and makes it visible under the target's
// name. On any failure it removes the temporary file and leaves the target as it was.
func (d *Download) Commit() error {
	if d.finished {
		return errFinished
	}
	if err := d.commit(); err != nil {
		d.Abort()
		return err
	}
	d.committed, d.finished = true, true
	d.dir.Close()
	return nil
}

func (d *Download) commit() error {
	if d.err != nil {
		return d.err
	}
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if d.expectSize >= 0 && d.written != d.expectSize {
		return &IntegrityError{Reason: "is shorter than the reported size"}
	}
	if d.expectSHA256 != "" && hex.EncodeToString(d.hash.Sum(nil)) != d.expectSHA256 {
		return &IntegrityError{Reason: "does not match the reported checksum"}
	}
	if err := d.file.Sync(); err != nil {
		return failure("writing", err)
	}
	if err := d.file.Close(); err != nil {
		return failure("writing", err)
	}
	if !d.overwrite {
		err := d.dir.Link(d.temp, d.name)
		if errors.Is(err, fs.ErrExist) {
			return ErrOverwriteNeedsConfirmation
		}
		if err != nil {
			return failure("creating", err)
		}
		// The target holds the content now; the temporary name only goes away.
		_ = d.dir.Remove(d.temp)
		return nil
	}
	info, err := d.dir.Lstat(d.name)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return pathError(config.LocalFilesWrite, reasonNotRegular)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return failure("replacing", err)
	}
	if err := d.dir.Rename(d.temp, d.name); err != nil {
		return failure("replacing", err)
	}
	return nil
}

// Abort discards the download: it removes the temporary file and leaves the target as it was. It does
// nothing after Commit or a previous Abort.
func (d *Download) Abort() error {
	if d.finished {
		return nil
	}
	d.finished = true
	defer d.dir.Close()
	d.file.Close()
	if err := d.dir.Remove(d.temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return failure("removing", err)
	}
	return nil
}
