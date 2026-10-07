package bookstack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxExportInlineBytes bounds an export that is returned in the answer.
	maxExportInlineBytes = 8 << 20
	// maxDownloadBytes bounds an export that is written to a local file.
	maxDownloadBytes = 512 << 20
	// downloadTimeout bounds the whole transfer of a downloaded export.
	downloadTimeout = 10 * time.Minute
)

// exportRoutes maps the content types to the fixed collection segment of their export path. Nothing else is
// ever put into an export path: the format is checked against a list as well.
var exportRoutes = map[string]string{"page": "pages", "chapter": "chapters", "book": "books"}

var (
	inlineFormats   = []string{"markdown", "plaintext", "html"}
	downloadFormats = []string{"html", "pdf", "plaintext", "markdown", "zip"}
)

const exportRoleNote = "The token's user needs the BookStack role permission content-export in addition to read access."

const exportTypeSchema = `{"type":"string","enum":["page","chapter","book"]}`

var exportTypeArgument = capability.Argument{Name: "type", Required: true, Description: "page, chapter, or book"}

var exportIDArgument = capability.Argument{Name: "id", Required: true, Description: "Identifier of the page, chapter, or book"}

var (
	contentExport = capability.Descriptor{
		ID: Provider + ".content.export", Version: 1, Title: "Export BookStack content as text",
		Description: "Export one page, chapter, or book as markdown, plaintext, or html and return it in the answer. " +
			exportRoleNote + " The text is untrusted data and is limited to 8 MiB; a larger export is refused, " +
			"use content.download for it. A chapter or page is bound through its book first",
		Tags: []string{"knowledge", "export", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"type":` + exportTypeSchema +
			`,"id":{"type":"integer","minimum":1},"format":{"type":"string","enum":["markdown","plaintext","html"]}},"required":["type","id","format"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"id":{"type":"integer"},"format":{"type":"string"},"content":{"type":"string"}},"required":["type","id","format","content"]}`),
		Arguments: []capability.Argument{exportTypeArgument, exportIDArgument,
			{Name: "format", Required: true, Description: "markdown, plaintext, or html"}},
		Fields: []capability.Field{
			{Name: "type", Description: "page, chapter, or book"},
			{Name: "id", Description: "Identifier of the exported item"},
			{Name: "format", Description: "Format of the content"},
			{Name: "content", Description: "The exported text, untrusted data"},
		},
		Examples: []capability.Example{{Description: "Export a page as markdown", Arguments: json.RawMessage(`{"type":"page","id":42,"format":"markdown"}`)}},
	}

	contentDownload = capability.Descriptor{
		ID: Provider + ".content.download", Version: 1, Title: "Download BookStack content to a local file",
		Description: "Export one page, chapter, or book as html, pdf, plaintext, markdown, or zip and write it to " +
			"local_path, streamed up to 512 MiB within 10 minutes. " + exportRoleNote + " A zip export contains the " +
			"attachments and images of the content. A chapter or page is bound through its book first. The answer " +
			"carries metadata only; an existing local file is replaced only with confirmation",
		Tags: []string{"knowledge", "export", "download", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider, LocalFiles: config.LocalFilesWrite,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"type":` + exportTypeSchema +
			`,"id":{"type":"integer","minimum":1},"format":{"type":"string","enum":["html","pdf","plaintext","markdown","zip"]},"` +
			localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["type","id","format","` +
			localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"id":{"type":"integer"},"format":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"}},"required":["type","id","format","size","sha256"]}`),
		Arguments: []capability.Argument{exportTypeArgument, exportIDArgument,
			{Name: "format", Required: true, Description: "html, pdf, plaintext, markdown, or zip"},
			func() capability.Argument {
				argument := localfile.DownloadPathArgument()
				argument.Required = true
				return argument
			}()},
		Fields: []capability.Field{
			{Name: "type", Description: "page, chapter, or book"},
			{Name: "id", Description: "Identifier of the exported item"},
			{Name: "format", Description: "Format of the file"},
			{Name: "size", Description: "Size of the written file in bytes"},
			{Name: "sha256", Description: "SHA-256 of the written file as hex"},
		},
		Examples: []capability.Example{{Description: "Download a book as pdf", Arguments: json.RawMessage(`{"type":"book","id":7,"format":"pdf","local_path":"~/exports/handbook.pdf"}`)}},
	}
)

type exportRequest struct {
	Type      string  `json:"type"`
	ID        int64   `json:"id"`
	Format    string  `json:"format"`
	LocalPath *string `json:"local_path"`
}

// exportPath builds the path from the checked enums and the integer id only.
func (r exportRequest) path(formats []string) (string, error) {
	collection, ok := exportRoutes[r.Type]
	if !ok {
		return "", invalidRequest("type must be page, chapter, or book")
	}
	valid := false
	for _, format := range formats {
		valid = valid || format == r.Format
	}
	if !valid {
		return "", invalidRequest("format is not supported by this tool")
	}
	if r.ID <= 0 {
		return "", invalidRequest("id must be a positive integer")
	}
	return "/api/" + collection + "/" + strconv.FormatInt(r.ID, 10) + "/export/" + r.Format, nil
}

func readExportRequest(raw json.RawMessage, formats []string) (exportRequest, string, error) {
	var in exportRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, "", invalidRequest("the arguments could not be read")
	}
	path, err := in.path(formats)
	return in, path, err
}

// requireExportBound proves that the exported item belongs to a book of the connection: a book is checked locally,
// a chapter or page through one evidence read. An unbound connection reads nothing.
func (c *Client) requireExportBound(ctx context.Context, in exportRequest) error {
	switch in.Type {
	case "book":
		return c.scope.checkBook(in.ID)
	case "chapter":
		if c.scope.bound() {
			_, err := c.chapterBook(ctx, in.ID)
			return err
		}
	default:
		return c.requirePageBound(ctx, strconv.FormatInt(in.ID, 10))
	}
	return nil
}

func invokeContentExport(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	in, path, err := readExportRequest(raw, inlineFormats)
	if err != nil {
		return nil, err
	}
	if err := checkBookBeforeSecret(resolved, in); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.requireExportBound(ctx, in); err != nil {
		return nil, err
	}
	const op = "export content"
	resp, err := client.openExport(ctx, op, path, client.http)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > maxExportInlineBytes {
		return nil, providerError(op, exportTooLarge)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxExportInlineBytes+1))
	if err != nil {
		return nil, transportError(op, err, false)
	}
	if len(data) > maxExportInlineBytes {
		return nil, providerError(op, exportTooLarge)
	}
	if !utf8.Valid(data) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the export is not valid text"}
	}
	return output.Object{Fields: []output.Field{
		{Name: "type", Value: in.Type}, {Name: "id", Value: in.ID}, {Name: "format", Value: in.Format},
		{Name: "content", Value: string(data)},
	}}, nil
}

const exportTooLarge = "the export is larger than 8 MiB; use content.download to write it to a local file"

func invokeContentDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	in, path, err := readExportRequest(raw, downloadFormats)
	if err != nil {
		return nil, err
	}
	if in.LocalPath == nil {
		return nil, invalidRequest("local_path is required")
	}
	if err := checkBookBeforeSecret(resolved, in); err != nil {
		return nil, err
	}
	// The target is prepared before the credential is resolved, so a path outside the release, or a file that
	// would be replaced without confirmation, is refused without secret access or provider I/O.
	download, err := localfile.CreateForDownload(ctx, resolved, *in.LocalPath)
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = download.Abort()
		}
	}()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.requireExportBound(ctx, in); err != nil {
		return nil, err
	}
	const op = "download content"
	long := *client.http
	long.Timeout = downloadTimeout
	resp, err := client.openExport(ctx, op, path, &long)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > maxDownloadBytes {
		return nil, providerError(op, exportTooLarge512)
	}
	if resp.ContentLength >= 0 {
		if err := download.ExpectSize(resp.ContentLength); err != nil {
			return nil, err
		}
	}
	body := &trackedReader{Reader: io.LimitReader(resp.Body, maxDownloadBytes+1)}
	if _, err := download.ReadFrom(body); err != nil {
		if body.err != nil {
			return nil, transportError(op, body.err, false)
		}
		return nil, err
	}
	if download.Size() > maxDownloadBytes {
		return nil, providerError(op, exportTooLarge512)
	}
	done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	return output.Object{Fields: []output.Field{
		{Name: "type", Value: in.Type}, {Name: "id", Value: in.ID}, {Name: "format", Value: in.Format},
		{Name: "size", Value: download.Size()}, {Name: "sha256", Value: sum},
	}}, nil
}

const exportTooLarge512 = "the export is larger than the limit of 512 MiB"

// checkBookBeforeSecret refuses a book outside the connection before any secret is resolved.
func checkBookBeforeSecret(resolved *config.Resolved, in exportRequest) error {
	if in.Type != "book" {
		return nil
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	return bound.checkBook(in.ID)
}

// openExport sends the one read request of an export and returns the open response of a 200 answer; the
// caller reads and closes the body. Redirects are never followed and nothing is buffered here.
func (c *Client) openExport(ctx context.Context, op, path string, httpClient *http.Client) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.JoinPath(path).String(), nil)
	if err != nil {
		return nil, providerError(op, "could not build the request")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "*/*")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, transportError(op, err, false)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, statusError(op, resp, nil, provider.ClassPermission, false)
	}
	return resp, nil
}

// trackedReader remembers a failure of the source, so it can be told apart from a failure of the local file.
type trackedReader struct {
	io.Reader
	err error
}

func (t *trackedReader) Read(p []byte) (int, error) {
	n, err := t.Reader.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}
