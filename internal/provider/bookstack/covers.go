package bookstack

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var removeCoverArgument = capability.Argument{
	Name: "remove_cover", Description: "true removes the cover image; false changes nothing"}

const coverFileNote = "Allowed file types are png, jpg, jpeg, gif, and webp, at most 50 MiB, streamed from disk from " +
	"a directory the connection releases for reading. Not idempotent"

var (
	booksSetCover = capability.Descriptor{
		ID: Provider + ".books.setcover", Version: 1, Title: "Set the cover image of a BookStack book",
		Description: "Set the cover image of one book from a local image file. " + coverFileNote +
			". On a connection bound to books the book must be a bound book",
		Tags: []string{"knowledge", "books", "bookstack", "update", "cover"}, Provider: Provider,
		LocalFiles:   config.LocalFilesRead,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(bookWriteOutput),
		Arguments: []capability.Argument{{Name: "id", Description: "Book identifier", Required: true},
			imageLocalPathArgument},
		Fields: bookWriteFields,
	}

	shelvesSetCover = capability.Descriptor{
		ID: Provider + ".shelves.setcover", Version: 1, Title: "Set the cover image of a BookStack shelf",
		Description: "Set the cover image of one shelf from a local image file. " + coverFileNote +
			". Shelves span the whole instance, so a connection bound to books cannot use this tool",
		Tags: []string{"knowledge", "shelves", "bookstack", "update", "cover"}, Provider: Provider,
		LocalFiles:   config.LocalFilesRead,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
		InputSchema:  booksSetCover.InputSchema,
		OutputSchema: json.RawMessage(shelfWriteOutput),
		Arguments: []capability.Argument{{Name: "id", Description: "Shelf identifier", Required: true},
			imageLocalPathArgument},
		Fields: shelfMetadataFields,
	}
)

func decodeCover(raw json.RawMessage, op string) (int64, *string, error) {
	var in struct {
		ID        int64   `json:"id"`
		LocalPath *string `json:"local_path"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return 0, nil, providerError(op, "the validated arguments could not be read")
	}
	return in.ID, in.LocalPath, nil
}

func invokeBooksSetCover(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set book cover"
	id, localPath, err := decodeCover(raw, op)
	if err != nil {
		return nil, err
	}
	if err := checkID(id, "id"); err != nil {
		return nil, err
	}
	if _, err := checkImageFile(localPath, ""); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := bound.checkBook(id); err != nil {
		return nil, err
	}
	upload, err := openUpload(ctx, resolved, localPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var book bookJSON
	// BookStack takes a multipart body only on POST; _method makes it the update of the book.
	if err := client.sendFile(ctx, op, "/api/books/"+strconv.FormatInt(id, 10), "image",
		[][2]string{{"_method", "PUT"}}, upload, &book, argNames(booksSetCover)); err != nil {
		return nil, err
	}
	return bookWriteObject(book), nil
}

func invokeShelvesSetCover(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set shelf cover"
	id, localPath, err := decodeCover(raw, op)
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, shelvesInstanceWide); err != nil {
		return nil, err
	}
	if err := checkShelfID(id); err != nil {
		return nil, err
	}
	if _, err := checkImageFile(localPath, ""); err != nil {
		return nil, err
	}
	upload, err := openUpload(ctx, resolved, localPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var shelf shelfJSON
	if err := client.sendFile(ctx, op, "/api/shelves/"+strconv.FormatInt(id, 10), "image",
		[][2]string{{"_method", "PUT"}}, upload, &shelf, argNames(shelvesSetCover)); err != nil {
		return nil, err
	}
	return shelfWriteObject(shelf), nil
}
