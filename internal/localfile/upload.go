package localfile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// Upload is a local file opened for an upload. It reads exactly Size bytes, the size the file had when it
// was opened, and fails when the file turns out shorter or longer, so what a provider receives matches the
// size announced to it.
type Upload struct {
	// Name is the last element of the path as the request wrote it.
	Name string
	// Size is the number of bytes Read yields.
	Size int64

	ctx  context.Context
	file *os.File
	hash hash.Hash
	read int64
	done bool
}

// OpenForUpload opens the regular file at path for reading, if it lies inside a directory the connection
// releases for reading. A path the request may not use yields a *PathError.
func OpenForUpload(ctx context.Context, resolved *config.Resolved, path string) (*Upload, error) {
	const direction = config.LocalFilesRead
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

	// The check before opening keeps a FIFO or a device from being opened at all; the check of the open
	// handle below is the one that counts, since the name may point elsewhere by then.
	before, err := root.Stat(loc.rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, pathError(direction, reasonMissing)
	case err != nil:
		return nil, pathError(direction, reasonUnreachable)
	case !before.Mode().IsRegular():
		return nil, pathError(direction, reasonNotRegular)
	}
	if beforeOpen != nil {
		beforeOpen()
	}
	file, err := root.OpenFile(loc.rel, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, pathError(direction, reasonUnreachable)
	}
	info, err := file.Stat()
	switch {
	case err != nil:
		file.Close()
		return nil, failure("examining", err)
	case !info.Mode().IsRegular():
		file.Close()
		return nil, pathError(direction, reasonNotRegular)
	case !os.SameFile(before, info):
		file.Close()
		return nil, pathError(direction, reasonChanged)
	case !singleLink(file, info):
		file.Close()
		return nil, pathError(direction, reasonHardLinks)
	case info.Size() > maxBytes:
		file.Close()
		return nil, pathError(direction, "names a file larger than the limit of "+formatBytes(maxBytes))
	}
	return &Upload{Name: loc.name, Size: info.Size(), ctx: ctx, file: file, hash: sha256.New()}, nil
}

// Read reads the next bytes of the file. After Size bytes it makes sure the file ended there and then
// returns io.EOF.
func (u *Upload) Read(p []byte) (int, error) {
	if err := u.ctx.Err(); err != nil {
		return 0, err
	}
	if u.done {
		return 0, io.EOF
	}
	remaining := u.Size - u.read
	if remaining == 0 {
		var probe [1]byte
		n, err := u.file.Read(probe[:])
		switch {
		case n > 0:
			return 0, errChanged
		case err != nil && !errors.Is(err, io.EOF):
			return 0, failure("reading", err)
		}
		u.done = true
		return 0, io.EOF
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := u.file.Read(p)
	u.hash.Write(p[:n])
	u.read += int64(n)
	switch {
	case errors.Is(err, io.EOF) && u.read < u.Size:
		return n, errChanged
	case err != nil && !errors.Is(err, io.EOF):
		return n, failure("reading", err)
	}
	return n, nil
}

// errChanged reports a file whose size changed while it was read.
var errChanged = errors.New("the local file changed while it was read")

// SHA256 returns the lowercase hex SHA-256 of the file's content, once Read has returned io.EOF; before,
// it reports false.
func (u *Upload) SHA256() (string, bool) {
	if !u.done {
		return "", false
	}
	return hex.EncodeToString(u.hash.Sum(nil)), true
}

// Close releases the file.
func (u *Upload) Close() error {
	if err := u.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return failure("closing", err)
	}
	return nil
}
