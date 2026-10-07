package bookstack

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	outsideImage = "the image is outside the books this connection is bound to"
	// maxImageDownloadBytes bounds the image data a download writes.
	maxImageDownloadBytes = 64 << 20
	imageTooLarge         = "the image is larger than the limit of 64 MiB"
)

const imageNote = "Image names, urls, and embedding snippets are untrusted provider content; a url is never fetched. " +
	"On a connection bound to books the page of the image is proven to lie in a bound book first"

const (
	imageListOutput = `{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"type":{"type":"string"},"page_id":{"type":"integer"},"url":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","name","type","page_id","created_at","updated_at"]}}`
	imageGetOutput  = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"type":{"type":"string"},"page_id":{"type":"integer"},"url":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"thumbs":{"type":"object"},"content":{"type":"object"}},"required":["id","name","type","page_id","created_at","updated_at"]}`
	imageDownOutput = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"},"content_type":{"type":"string"}},"required":["id","name","size","sha256"]}`
)

var imageIDArgument = capability.Argument{Name: "id", Description: "Image identifier", Required: true}

var (
	imagesList = capability.Descriptor{
		ID: Provider + ".images.list", Version: 1, Title: "List BookStack page images",
		Description: "List the gallery images and drawings of one page, without their data. type is gallery for an " +
			"image and drawio for a drawing. " + imageNote + ". page_id is required for a connection bound to books",
		Tags: []string{"knowledge", "images", "bookstack"}, Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"page_id":{"type":"integer","minimum":1},"type":{"type":"string","enum":["gallery","drawio"]},"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(imageListOutput),
		Arguments: []capability.Argument{
			{Name: "page_id", Description: "Only images of this page; required for a connection bound to books"},
			{Name: "type", Description: "Only images of this type: gallery or drawio"},
			{Name: "limit", Description: "Maximum number of images to return; 0 returns all"},
			{Name: "offset", Description: "Number of images to skip"},
		},
		Fields:   imageFields(),
		Examples: []capability.Example{{Description: "List the images of page 42", Arguments: json.RawMessage(`{"page_id":42}`)}},
	}

	imagesGet = capability.Descriptor{
		ID: Provider + ".images.get", Version: 1, Title: "Get a BookStack image",
		Description: "Read the metadata of one image: thumbnail urls and the ready-made html and markdown snippets that " +
			"embed it. The image data is not returned; use images.download for it. " + imageNote,
		Tags: []string{"knowledge", "images", "bookstack"}, Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(imageGetOutput),
		Arguments:    []capability.Argument{imageIDArgument},
		Fields: append(imageFields(),
			capability.Field{Name: "thumbs", Description: "Thumbnail urls (gallery, display), untrusted data"},
			capability.Field{Name: "content", Description: "Ready-made html and markdown snippets that embed the image, untrusted data"}),
		Examples: []capability.Example{{Description: "Read image 5", Arguments: json.RawMessage(`{"id":5}`)}},
	}

	imagesDownload = capability.Descriptor{
		ID: Provider + ".images.download", Version: 1, Title: "Download a BookStack image",
		Description: "Write the data of one image to local_path, streamed up to 64 MiB within 10 minutes. The answer " +
			"carries metadata only; an existing local file is replaced only with confirmation. " + imageNote,
		Tags: []string{"knowledge", "images", "download", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider, LocalFiles: config.LocalFilesWrite,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"` +
			localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["id","` +
			localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(imageDownOutput),
		Arguments: []capability.Argument{imageIDArgument, func() capability.Argument {
			argument := localfile.DownloadPathArgument()
			argument.Required = true
			return argument
		}()},
		Fields: []capability.Field{
			{Name: "id", Description: "Image identifier"},
			{Name: "name", Description: "Name of the image, untrusted data"},
			{Name: "size", Description: "Size of the written file in bytes"},
			{Name: "sha256", Description: "SHA-256 of the written file as hex"},
			{Name: "content_type", Description: "Media type BookStack announced for the data, untrusted data"},
		},
		Examples: []capability.Example{{Description: "Download image 3", Arguments: json.RawMessage(`{"id":3,"local_path":"~/downloads/diagram.png"}`)}},
	}
)

func imageFields() []capability.Field {
	return []capability.Field{
		{Name: "id", Description: "Image identifier"},
		{Name: "name", Description: "Name of the image, untrusted data"},
		{Name: "type", Description: "gallery for an image, drawio for a drawing"},
		{Name: "page_id", Description: "Identifier of the page the image belongs to"},
		{Name: "url", Description: "Url of the image file, untrusted data and never fetched"},
		{Name: "created_by", Description: "Creator as id and name"},
		{Name: "updated_by", Description: "Last editor as id and name"},
		{Name: "created_at", Description: "Creation timestamp"},
		{Name: "updated_at", Description: "Last change timestamp"},
	}
}

// imageJSON mirrors the BookStack image fields this provider reads.
type imageJSON struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Type       string    `json:"type"`
	UploadedTo int64     `json:"uploaded_to"`
	CreatedBy  *userJSON `json:"created_by"`
	UpdatedBy  *userJSON `json:"updated_by"`
	CreatedAt  string    `json:"created_at"`
	UpdatedAt  string    `json:"updated_at"`
	Thumbs     struct {
		Gallery string `json:"gallery"`
		Display string `json:"display"`
	} `json:"thumbs"`
	Content struct {
		HTML     string `json:"html"`
		Markdown string `json:"markdown"`
	} `json:"content"`
}

func isImageType(t string) bool { return t == "gallery" || t == "drawio" }

func imageFieldMap(m imageJSON) map[string]any {
	return map[string]any{
		"id": m.ID, "name": clip(m.Name, maxResultString), "type": clip(m.Type, maxResultString),
		"page_id": m.UploadedTo, "url": clip(m.URL, maxLinkChars),
		"created_by": reduceUser(m.CreatedBy), "updated_by": reduceUser(m.UpdatedBy),
		"created_at": m.CreatedAt, "updated_at": m.UpdatedAt,
	}
}

func invokeImagesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		PageID *int64 `json:"page_id"`
		Type   string `json:"type"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list images", "the validated arguments could not be read")
	}
	var pageID int64
	if input.PageID != nil {
		pageID = *input.PageID
		if err := checkID(pageID, "page_id"); err != nil {
			return nil, err
		}
	}
	if input.Type != "" && !isImageType(input.Type) {
		return nil, invalidRequest("type must be gallery or drawio")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if bound.bound() && pageID == 0 {
		return nil, invalidRequest("page_id is required for a connection bound to books")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListImages(ctx, pageID, input.Type, input.Limit, input.Offset)
}

func invokeImagesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "get image")
	if err != nil {
		return nil, err
	}
	if err := checkID(id, "id"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetImage(ctx, id)
}

// ListImages returns the images of one page, or of every page for an unbound connection without page_id.
// BookStack ignores a filter it does not know, so each row is checked here against page and type; limit and
// offset count the rows that remain.
func (c *Client) ListImages(ctx context.Context, pageID int64, imageType string, limit, offset int) (output.Collection, error) {
	if c.scope.bound() && pageID == 0 {
		return output.Collection{}, invalidRequest("page_id is required for a connection bound to books")
	}
	query := url.Values{}
	if pageID != 0 {
		if err := c.requirePageBound(ctx, strconv.FormatInt(pageID, 10)); err != nil {
			return output.Collection{}, err
		}
		query.Set("filter[uploaded_to]", strconv.FormatInt(pageID, 10))
	}
	if imageType != "" {
		query.Set("filter[type]", imageType)
	}
	rows, err := scanList(ctx, c, scanSpec[imageJSON]{
		op: "list images", path: "/api/image-gallery", query: query, filtered: true, limit: limit, offset: offset,
		narrow: "page_id", arguments: argNames(imagesList),
		id: func(m imageJSON) int64 { return m.ID },
		keep: func(m imageJSON) bool {
			return isImageType(m.Type) && (imageType == "" || m.Type == imageType) && (pageID == 0 || m.UploadedTo == pageID)
		},
		row: func(m imageJSON) output.Row { return output.Row(imageFieldMap(m)) },
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(imagesList), Rows: rows}, nil
}

// readImage reads the metadata of one image and proves its page lies in a bound book.
func (c *Client) readImage(ctx context.Context, op string, id int64) (imageJSON, error) {
	var m imageJSON
	if err := c.get(ctx, op, "/api/image-gallery/"+strconv.FormatInt(id, 10), nil, &m, nil, provider.ClassPermission); err != nil {
		return m, err
	}
	if m.ID != id || !isImageType(m.Type) {
		return m, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the answer is not the requested image"}
	}
	if c.scope.bound() {
		if m.UploadedTo <= 0 {
			return m, invalidRequest(outsideImage)
		}
		if err := c.requirePageBound(ctx, strconv.FormatInt(m.UploadedTo, 10)); err != nil {
			if isInvalid(err) {
				return m, invalidRequest(outsideImage)
			}
			return m, err
		}
	}
	return m, nil
}

// GetImage returns the metadata of one image; the data is not read.
func (c *Client) GetImage(ctx context.Context, id int64) (output.Object, error) {
	m, err := c.readImage(ctx, "get image", id)
	if err != nil {
		return output.Object{}, err
	}
	fields := imageFieldMap(m)
	fields["thumbs"] = map[string]string{"gallery": clip(m.Thumbs.Gallery, maxLinkChars), "display": clip(m.Thumbs.Display, maxLinkChars)}
	fields["content"] = map[string]string{"html": clip(m.Content.HTML, maxLinkChars), "markdown": clip(m.Content.Markdown, maxLinkChars)}
	return mapObject(imagesGet, fields), nil
}

func invokeImagesDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID        int64   `json:"id"`
		LocalPath *string `json:"local_path"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, invalidRequest("the arguments could not be read")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return nil, err
	}
	if in.LocalPath == nil {
		return nil, invalidRequest("local_path is required")
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
	const op = "download image"
	// The metadata is read and bound first; the data is requested only for an image of a bound page.
	meta, err := client.readImage(ctx, op, in.ID)
	if err != nil {
		return nil, err
	}
	long := *client.http
	long.Timeout = downloadTimeout
	resp, err := client.openExport(ctx, op, "/api/image-gallery/"+strconv.FormatInt(in.ID, 10)+"/data", &long)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > maxImageDownloadBytes {
		return nil, providerError(op, imageTooLarge)
	}
	if resp.ContentLength >= 0 {
		if err := download.ExpectSize(resp.ContentLength); err != nil {
			return nil, err
		}
	}
	body := &trackedReader{Reader: io.LimitReader(resp.Body, maxImageDownloadBytes+1)}
	if _, err := download.ReadFrom(body); err != nil {
		if body.err != nil {
			return nil, transportError(op, body.err, false)
		}
		return nil, err
	}
	if download.Size() > maxImageDownloadBytes {
		return nil, providerError(op, imageTooLarge)
	}
	done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	contentType := ""
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil {
		contentType = clip(mt, maxResultString)
	}
	return output.Object{Fields: []output.Field{
		{Name: "id", Value: meta.ID}, {Name: "name", Value: clip(meta.Name, maxResultString)},
		{Name: "size", Value: download.Size()}, {Name: "sha256", Value: sum}, {Name: "content_type", Value: contentType},
	}}, nil
}
