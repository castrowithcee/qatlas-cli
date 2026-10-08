package nextcloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Chunked upload v2 of Nextcloud (developer manual, "Chunked file upload", version 2, read 2026-10-06):
// MKCOL of one upload folder below remote.php/dav/uploads/<user>/, one PUT per chunk named 1 to 10000, and
// a MOVE of <folder>/.file to the target below remote.php/dav/files/<user>/. Every request but the abort
// carries a Destination header. The manual gives chunks of 5 MiB to 5 GiB, except the last one, and names
// OC-Total-Length for the quota check. It documents no If-Match or If-None-Match for the MOVE, so the target
// is checked by a stat before the upload and again right before the MOVE; see uploadChunked.
const (
	maxChunks = 10000
	// uploadFolderTimeout bounds the best-effort removal of an abandoned upload folder.
	uploadFolderTimeout = 30 * time.Second
)

// chunkSize is the size of every chunk but the last. Tests lower it; the documented minimum is 5 MiB.
var chunkSize int64 = 10 << 20

func chunkCount(size int64) int64 { return (size + chunkSize - 1) / chunkSize }

// uploadID is the random, unguessable name of one upload folder.
func uploadID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// recordReader remembers a failure of the local file, so it is not mistaken for a transport failure.
type recordReader struct {
	r   io.Reader
	err error
}

func (r *recordReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

// uploadChunked sends the file in chunks, each exactly once, and assembles it at the target with one MOVE.
// A failure before the MOVE leaves no file at the target, and the upload folder is removed best effort. An
// unclear MOVE is reported as uncertain and never repeated.
//
// The remaining gap: the manual documents no condition for the MOVE, so a file created or changed between
// the final stat and the MOVE is overwritten (create) or replaced regardless of its version (update).
func (c *Client) uploadChunked(ctx context.Context, op string, rel []string, upload *localfile.Upload, match string) (string, error) {
	id, err := uploadID()
	if err != nil {
		return "", providerError(op, "no upload ID could be generated")
	}
	if err := c.checkTarget(ctx, op, rel, match); err != nil {
		return "", err
	}
	destination := c.requestURL(rel)[len(c.origin):]
	folder := c.uploadURL(id)
	total := strconv.FormatInt(upload.Size, 10)
	fail := func(err error) (string, error) {
		c.abortUpload(ctx, folder)
		return "", err
	}

	response, err := c.chunkRequest(ctx, "MKCOL", folder, destination, "", nil, 0)
	if err != nil {
		return fail(provider.Transport(op, "Nextcloud", err))
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fail(statusError(op, response.StatusCode))
	}

	count := chunkCount(upload.Size)
	for n := int64(1); n <= count; n++ {
		length := min(chunkSize, upload.Size-(n-1)*chunkSize)
		source := &recordReader{r: io.LimitReader(upload, length)}
		response, err := c.chunkRequest(ctx, http.MethodPut, folder+"/"+strconv.FormatInt(n, 10), destination, total, source, length)
		if err != nil {
			if source.err != nil && ctx.Err() == nil {
				return fail(providerError(op, "the local file could not be read completely"))
			}
			return fail(provider.Transport(op, "Nextcloud", err))
		}
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fail(statusError(op, response.StatusCode))
		}
	}
	// The file must end where it was measured; this read also completes the SHA-256.
	if _, err := io.Copy(io.Discard, upload); err != nil {
		return fail(providerError(op, "the local file changed while it was read"))
	}
	if err := c.checkTarget(ctx, op, rel, match); err != nil {
		return fail(err)
	}

	response, err = c.chunkRequest(ctx, "MOVE", folder+"/.file", destination, total, nil, 0)
	if err != nil {
		return "", withUncertainty(provider.Transport(op, "Nextcloud", err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if isRedirect(response.StatusCode) || response.StatusCode >= 400 && response.StatusCode < 500 {
			return fail(statusError(op, response.StatusCode))
		}
		return "", sentStatusError(op, response.StatusCode)
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		etag = response.Header.Get("OC-ETag")
	}
	return bounded(strings.Trim(etag, `"`)), nil
}

// uploadURL is the absolute URL of one upload folder of this identity, or of a node in it.
func (c *Client) uploadURL(id string) string {
	return c.origin + escapePath(append(append([]string{}, c.uploads...), id))
}

// chunkRequest sends one request of the chunked upload. The Destination is always a path below the Files
// root, built from checked segments; total, when not empty, is the length of the whole file.
func (c *Client) chunkRequest(ctx context.Context, method, target, destination, total string, body io.Reader, length int64) (*http.Response, error) {
	if body == nil {
		body = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = length
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Destination", destination)
	if total != "" {
		req.Header.Set("OC-Total-Length", total)
	}
	return c.transferClient().Do(req)
}

// abortUpload removes the upload folder, best effort and without a Destination as the manual describes.
func (c *Client) abortUpload(ctx context.Context, folder string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), uploadFolderTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, folder, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", c.auth)
	if response, err := c.http.Do(req); err == nil {
		response.Body.Close()
	}
}

// checkTarget stands in for the conditions the manual does not document for the MOVE: a create needs the
// target to be absent, an update needs the file to carry the given version.
func (c *Client) checkTarget(ctx context.Context, op string, rel []string, match string) error {
	entry, err := c.stat(ctx, op, rel, false)
	if match == "*" {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) && providerErr.Message == messageNotFound {
			return nil
		}
		if err == nil {
			err = &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "the Nextcloud file changed or already exists"}
		}
		return err
	}
	if err != nil {
		return err
	}
	if entry.Type != typeFile || entry.ETag != strings.Trim(match, `"`) {
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "the Nextcloud file changed or already exists"}
	}
	return nil
}
