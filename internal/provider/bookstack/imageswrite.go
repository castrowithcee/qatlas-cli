package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxImageNameChars is the limit BookStack applies to an image name.
const maxImageNameChars = 180

const imageWriteOutput = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"type":{"type":"string"},"page_id":{"type":"integer"},"url":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","name","type","page_id"]}`

const imageBindNote = "On a connection bound to books the page is proven to lie in a bound book first, " +
	"and so is the page of an existing image. Allowed file types are png, jpg, jpeg, gif, and webp; a drawing " +
	"must be a png"

var imageWriteFields = []capability.Field{
	{Name: "id", Description: "Image identifier"},
	{Name: "name", Description: "Name of the image, untrusted data"},
	{Name: "type", Description: "gallery for an image, drawio for a drawing"},
	{Name: "page_id", Description: "Identifier of the page the image belongs to"},
	{Name: "url", Description: "Url of the image file, untrusted data and never fetched"},
	{Name: "created_at", Description: "Creation timestamp"},
	{Name: "updated_at", Description: "Last change timestamp"},
}

var imageLocalPathArgument = capability.Argument{
	Name: localfile.LocalPathArgument, Required: true,
	Description: "Local image file, at most 50 MiB, absolute or starting with ~/, inside a directory the " +
		"connection releases for reading"}

var (
	imagesUpload = capability.Descriptor{
		ID: Provider + ".images.upload", Version: 1, Title: "Upload an image to a BookStack page",
		Description: "Upload one local image of at most 50 MiB to a page, streamed from disk from a directory the " +
			"connection releases for reading. type is gallery for an image or drawio for a drawing (png only). " +
			"Not idempotent: a repeated call adds another image. " + imageBindNote,
		Tags: []string{"knowledge", "images", "upload", "bookstack", "create"}, Provider: Provider,
		LocalFiles:   config.LocalFilesRead,
		Risk:         bookWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"page_id":{"type":"integer","minimum":1},"type":{"type":"string","enum":["gallery","drawio"]},"name":{"type":"string","minLength":1,"maxLength":180},"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["page_id","type","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(imageWriteOutput),
		Arguments: []capability.Argument{
			{Name: "page_id", Description: "Page the image belongs to", Required: true},
			{Name: "type", Description: "gallery for an image, drawio for a drawing", Required: true},
			imageLocalPathArgument,
			{Name: "name", Description: "Image name, 1 to 180 characters; the file name when omitted"}},
		Fields: imageWriteFields,
	}

	imagesUpdate = capability.Descriptor{
		ID: Provider + ".images.update", Version: 1, Title: "Rename a BookStack image",
		Description: "Rename one image. " + imageBindNote,
		Tags:        []string{"knowledge", "images", "bookstack", "update"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":180}},"required":["id","name"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(imageWriteOutput),
		Arguments: []capability.Argument{imageIDArgument,
			{Name: "name", Description: "New image name, 1 to 180 characters", Required: true}},
		Fields: imageWriteFields,
	}

	imagesReplace = capability.Descriptor{
		ID: Provider + ".images.replace", Version: 1, Title: "Replace the file of a BookStack image",
		Description: "Replace the file of an existing image with one local image of at most 50 MiB, streamed from " +
			"disk from a directory the connection releases for reading. The extension of the local file must equal " +
			"the extension of the existing image (jpg and jpeg count as one); the name stays. The previous file is " +
			"gone afterwards. Not idempotent. " + imageBindNote,
		Tags: []string{"knowledge", "images", "upload", "bookstack", "update"}, Provider: Provider,
		LocalFiles:   config.LocalFilesRead,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(imageWriteOutput),
		Arguments:    []capability.Argument{imageIDArgument, imageLocalPathArgument},
		Fields:       imageWriteFields,
	}

	imagesDelete = capability.Descriptor{
		ID: Provider + ".images.delete", Version: 1, Title: "Delete a BookStack image",
		Description: "Delete one image by identifier: final, without a usage check, and it can leave pages with " +
			"broken image references. " + imageBindNote,
		Tags: []string{"knowledge", "images", "bookstack", "delete"}, Provider: Provider,
		RequiresToolAllowList: true,
		Risk:                  bookWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		InputSchema:           booksDelete.InputSchema,
		OutputSchema:          booksDelete.OutputSchema,
		Arguments:             []capability.Argument{imageIDArgument},
	}
)

// imageWriteJSON is the part of a change answer this provider shows. The user fields are left out: the
// answer may carry ids or objects there.
type imageWriteJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Type       string `json:"type"`
	UploadedTo int64  `json:"uploaded_to"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func imageWriteObject(m imageWriteJSON) output.Object {
	return mapObject(imagesUpload, map[string]any{
		"id": m.ID, "name": clip(m.Name, maxResultString), "type": clip(m.Type, maxResultString),
		"page_id": m.UploadedTo, "url": clip(m.URL, maxLinkChars),
		"created_at": m.CreatedAt, "updated_at": m.UpdatedAt,
	})
}

func checkImageName(name string) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > maxImageNameChars {
		return invalidRequest("name must be 1 to 180 characters")
	}
	return nil
}

// imageExtension is the lower-case extension without dot, with jpeg folded into jpg.
func imageExtension(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	if ext == "jpeg" {
		return "jpg"
	}
	return ext
}

// checkImageFile holds the local path to the fixed list of image types before any file or secret is used.
func checkImageFile(localPath *string, imageType string) (string, error) {
	if localPath == nil {
		return "", invalidRequest("local_path is required")
	}
	ext := imageExtension(filepath.Ext(*localPath))
	switch ext {
	case "png":
	case "jpg", "gif", "webp":
		if imageType == "drawio" {
			return "", invalidRequest("a drawing must be a png file")
		}
	default:
		return "", invalidRequest("the file must be a png, jpg, jpeg, gif, or webp image")
	}
	return ext, nil
}

func invokeImagesUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		PageID    int64   `json:"page_id"`
		Type      string  `json:"type"`
		Name      *string `json:"name"`
		LocalPath *string `json:"local_path"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("upload image", "the validated arguments could not be read")
	}
	if err := checkID(in.PageID, "page_id"); err != nil {
		return nil, err
	}
	if !isImageType(in.Type) {
		return nil, invalidRequest("type must be gallery or drawio")
	}
	if in.Name != nil {
		if err := checkImageName(*in.Name); err != nil {
			return nil, err
		}
	}
	if _, err := checkImageFile(in.LocalPath, in.Type); err != nil {
		return nil, err
	}
	upload, err := openUpload(ctx, resolved, in.LocalPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.requirePageBound(ctx, strconv.FormatInt(in.PageID, 10)); err != nil {
		return nil, err
	}
	fields := [][2]string{{"type", in.Type}, {"uploaded_to", strconv.FormatInt(in.PageID, 10)}}
	if in.Name != nil {
		fields = append(fields, [2]string{"name", *in.Name})
	}
	var written imageWriteJSON
	if err := client.sendFile(ctx, "upload image", "/api/image-gallery", "image", fields, upload, &written, argNames(imagesUpload)); err != nil {
		return nil, err
	}
	return imageWriteObject(written), nil
}

func invokeImagesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("update image", "the validated arguments could not be read")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return nil, err
	}
	if err := checkImageName(in.Name); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "update image"
	if _, err := client.readImage(ctx, op, in.ID); err != nil {
		return nil, err
	}
	var written imageWriteJSON
	if err := client.mutate(ctx, op, http.MethodPut, "/api/image-gallery/"+strconv.FormatInt(in.ID, 10),
		map[string]any{"name": in.Name}, &written, argNames(imagesUpdate)); err != nil {
		return nil, err
	}
	return imageWriteObject(written), nil
}

func invokeImagesReplace(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID        int64   `json:"id"`
		LocalPath *string `json:"local_path"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("replace image", "the validated arguments could not be read")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return nil, err
	}
	ext, err := checkImageFile(in.LocalPath, "")
	if err != nil {
		return nil, err
	}
	upload, err := openUpload(ctx, resolved, in.LocalPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "replace image"
	meta, err := client.readImage(ctx, op, in.ID)
	if err != nil {
		return nil, err
	}
	// The narrow reading of "same file type": the extension of the stored file, taken from its url path,
	// must equal the extension of the local file.
	stored := ""
	if u, perr := url.Parse(meta.URL); perr == nil {
		stored = imageExtension(path.Ext(u.Path))
	}
	if stored == "" || stored != ext {
		return nil, invalidRequest("the file must have the same type as the existing image")
	}
	// BookStack takes a multipart body only on POST; _method makes it the update of the image.
	var written imageWriteJSON
	if err := client.sendFile(ctx, op, "/api/image-gallery/"+strconv.FormatInt(in.ID, 10), "image",
		[][2]string{{"_method", "PUT"}}, upload, &written, argNames(imagesReplace)); err != nil {
		return nil, err
	}
	return imageWriteObject(written), nil
}

func invokeImagesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "delete image")
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
	const op = "delete image"
	if _, err := client.readImage(ctx, op, id); err != nil {
		return nil, err
	}
	if err := client.mutate(ctx, op, http.MethodDelete, "/api/image-gallery/"+strconv.FormatInt(id, 10), nil, nil, argNames(imagesDelete)); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}
