package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The attachment tools act on the attachments of one issue or pull request, or of one comment, of a repository
// inside the connection's targets. The owner is proved first by its evidence read (the canonical repository of
// the issue, or the conversation address of the comment, lies inside the targets), and an attachment ID must
// then be one of the attachments that very owner lists; an ID of another owner is refused before any request
// names it. The file travels as in the release asset tools: an upload streams from a directory released under
// files.read, and a download writes below files.write from the instance's own /attachments/UUID address only.

// attachmentOwner is what the issue and the comment attachment tools differ in.
type attachmentOwner struct {
	group     string
	noun      string // "issue or pull request" or "comment"
	label     string // names the operation in messages
	idName    string // the argument that names the owner
	idMeaning string // what that argument holds, for messages
	idSchema  string
	argument  capability.Argument
	path      func(repo target, id int64) string
	// prove is the evidence read of the owner; it returns the owner as the instance answered it.
	prove func(ctx context.Context, c *Client, allowed allowlist, repo target, op string, id int64) (object, error)
}

var attachmentIDArgument = capability.Argument{Name: "attachment_id", Required: true,
	Description: "Numeric ID of the attachment, as the list tool of its owner reports it as id; an attachment of " +
		"another owner is refused"}

var issueAttachmentOwner = attachmentOwner{
	group: "issue-attachments", noun: "issue or pull request", label: "issue attachment",
	idName: "number", idMeaning: "index of an issue or pull request", idSchema: issueSchemaNumber, argument: numberArgument, path: issuePath,
	prove: func(ctx context.Context, c *Client, allowed allowlist, repo target, op string, id int64) (object, error) {
		return readIndexObject(ctx, c, allowed, op, repo, id)
	},
}

var commentAttachmentOwner = attachmentOwner{
	group: "comment-attachments", noun: "comment", label: "comment attachment",
	idName: "id", idMeaning: "ID of a comment", idSchema: issueSchemaID, argument: commentIDArgument, path: commentPath,
	prove: func(ctx context.Context, c *Client, _ allowlist, repo target, op string, id int64) (object, error) {
		return readComment(ctx, c, repo, op, commentPath(repo, id), id)
	},
}

// attachmentArguments are the arguments every attachment tool shares.
type attachmentArguments struct {
	Repository   string  `json:"repository"`
	Number       int64   `json:"number"`
	ID           int64   `json:"id"`
	AttachmentID int64   `json:"attachment_id"`
	Name         *string `json:"name"`
	LocalPath    *string `json:"local_path"`
}

func (a *attachmentArguments) owner(o attachmentOwner) int64 {
	if o.idName == "number" {
		return a.Number
	}
	return a.ID
}

// read decodes the arguments and checks the repository and the IDs before anything is resolved or sent.
func (o attachmentOwner) read(allowed allowlist, raw json.RawMessage, attachment bool) (attachmentArguments, target,
	error) {
	var arguments attachmentArguments
	if err := decodeArguments(raw, &arguments); err != nil {
		return arguments, target{}, err
	}
	repo, err := allowed.chooseRepository(arguments.Repository)
	if err != nil {
		return arguments, target{}, err
	}
	if arguments.owner(o) < 1 {
		return arguments, target{}, invalidArgument(o.idName + " must be the " + o.idMeaning)
	}
	if attachment && arguments.AttachmentID < 1 {
		return arguments, target{}, invalidArgument("attachment_id must be the ID of an attachment")
	}
	return arguments, repo, nil
}

func (o attachmentOwner) attachmentsPath(repo target, id int64) string {
	return o.path(repo, id) + "/assets"
}

func (o attachmentOwner) attachmentPath(repo target, id, attachment int64) string {
	return o.attachmentsPath(repo, id) + "/" + strconv.FormatInt(attachment, 10)
}

// bind proves the owner and, when attachment is not zero, that the owner lists that attachment.
func (o attachmentOwner) bind(ctx context.Context, c *Client, allowed allowlist, repo target, op string, id,
	attachment int64) error {
	owner, err := o.prove(ctx, c, allowed, repo, op, id)
	if err != nil {
		return err
	}
	if attachment != 0 && !listsAsset(owner, attachment) {
		return invalidArgument("the attachment does not belong to this " + o.noun)
	}
	return nil
}

// read binds the attachment and reads its metadata.
func (o attachmentOwner) readAttachment(ctx context.Context, c *Client, allowed allowlist, repo target, op string,
	id, attachment int64) (object, error) {
	if err := o.bind(ctx, c, allowed, repo, op, id, attachment); err != nil {
		return nil, err
	}
	var found object
	if _, err := c.get(ctx, op, o.attachmentPath(repo, id, attachment), nil, &found); err != nil {
		return nil, err
	}
	if err := assetAnswer(c, op, found, attachment); err != nil {
		return nil, err
	}
	return found, nil
}

func (o attachmentOwner) properties() string {
	return `"repository":` + repoSchema + `,"` + o.idName + `":` + o.idSchema
}

func (o attachmentOwner) withAttachment() string {
	return o.properties() + `,"attachment_id":` + releaseSchemaID
}

func (o attachmentOwner) example(extra string) json.RawMessage {
	return json.RawMessage(`{"repository":"example/project","` + o.idName + `":7` + extra + `}`)
}

func (o attachmentOwner) ownerOutput() string { return `"` + o.idName + `":{"type":"integer"}` }

func (o attachmentOwner) ownerField() capability.Field {
	return capability.Field{Name: o.idName, Description: "Index or ID of the " + o.noun}
}

func (o attachmentOwner) list() *tool {
	return &tool{
		group: o.group, action: "list", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "List the attachments of a " + p.name + " " + o.noun,
				Description: "List the attachments of one " + o.noun + " of a repository inside the connection's " +
					"targets, with every member of each attachment; names are untrusted data. The " + o.noun +
					" is read first to bind it to the repository.",
				Tags:        []string{p.id, "attachments", "list"},
				Risk:        readRisk(p.id + issueSensitivity),
				InputSchema: inputSchema(o.properties(), o.idName),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"attachments":{"type":"array",` +
					`"items":` + schemaOf(p, attachmentMembers, "id") + `},` + o.ownerOutput() +
					`,"repository":{"type":"string"}},"required":["attachments","` + o.idName +
					`"],"additionalProperties":false}`),
				Arguments: []capability.Argument{repositoryArgument, o.argument},
				Fields: []capability.Field{{Name: "attachments", Description: "Attachments with every member of " +
					"the attachment object"}, o.ownerField(),
					{Name: "repository", Description: "Repository, as OWNER/REPO"}},
				Examples: []capability.Example{{Description: "List the attachments", Arguments: o.example("")}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			arguments, repo, err := o.read(allowed, raw, false)
			if err != nil {
				return nil, err
			}
			id := arguments.owner(o)
			return func(ctx context.Context, c *Client) (any, error) {
				op := "list " + o.label + "s"
				if err := o.bind(ctx, c, allowed, repo, op, id, 0); err != nil {
					return nil, err
				}
				var rows []object
				if _, err := c.get(ctx, op, o.attachmentsPath(repo, id), nil, &rows); err != nil {
					return nil, err
				}
				attachments := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					if row != nil {
						attachments = append(attachments, project(p, attachmentMembers, row))
					}
				}
				return map[string]any{"attachments": attachments, o.idName: id,
					"repository": repo.owner + "/" + repo.repo}, nil
			}, nil
		},
	}
}

func (o attachmentOwner) get() *tool {
	return &tool{
		group: o.group, action: "get", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "Get an attachment of a " + p.name + " " + o.noun,
				Description: "Read the metadata of one attachment of a " + o.noun + " of a repository inside the " +
					"connection's targets, never its content; use " + o.group + ".download for it. The attachment " +
					"must be one the " + o.noun + " lists; its name is untrusted data.",
				Tags:         []string{p.id, "attachments", "get"},
				Risk:         readRisk(p.id + issueSensitivity),
				InputSchema:  inputSchema(o.withAttachment(), o.idName, "attachment_id"),
				OutputSchema: assetOutput(p),
				Arguments:    []capability.Argument{repositoryArgument, o.argument, attachmentIDArgument},
				Fields:       fieldsOf(p, attachmentMembers),
				Examples: []capability.Example{{Description: "Read an attachment",
					Arguments: o.example(`,"attachment_id":3`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			arguments, repo, err := o.read(allowed, raw, true)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				found, err := o.readAttachment(ctx, c, allowed, repo, "get "+o.label, arguments.owner(o),
					arguments.AttachmentID)
				if err != nil {
					return nil, err
				}
				return project(p, attachmentMembers, found), nil
			}, nil
		},
	}
}

func (o attachmentOwner) download() *tool {
	return &tool{
		group: o.group, action: "download", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			path := localfile.DownloadPathArgument()
			path.Required = true
			return capability.Descriptor{
				Title: "Download an attachment of a " + p.name + " " + o.noun,
				Description: "Write the content of one uploaded attachment of a " + o.noun + " of a repository " +
					"inside the connection's targets to local_path, at most 2 GiB, streamed while it is read. Only " +
					"an attachment the instance serves at its own address is downloaded; an external attachment, an " +
					"address elsewhere, or a redirect is refused. The instance counts the download. The answer " +
					"carries metadata only; an existing local file is replaced only with confirmation.",
				Tags:       []string{p.id, "attachments", "download"},
				Risk:       readRisk(p.id + issueSensitivity),
				LocalFiles: config.LocalFilesWrite,
				InputSchema: inputSchema(o.withAttachment()+`,"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema,
					o.idName, "attachment_id", localfile.LocalPathArgument),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{` + o.ownerOutput() +
					`,"attachment_id":{"type":"integer"},"name":{"type":"string"},"local_path":{"type":"string"},` +
					`"size":{"type":"integer"},"sha256":{"type":"string"}},"required":["` + o.idName +
					`","attachment_id","local_path","size","sha256"],"additionalProperties":false}`),
				Arguments: []capability.Argument{repositoryArgument, o.argument, attachmentIDArgument, path},
				Fields: []capability.Field{o.ownerField(),
					{Name: "attachment_id", Description: "ID of the attachment"},
					{Name: "name", Description: "Name of the attachment, untrusted data"},
					{Name: "local_path", Description: "Local file written, as given"},
					{Name: "size", Description: "Size of the written file in bytes"},
					{Name: "sha256", Description: "SHA-256 of the written file as hex"}},
				Examples: []capability.Example{{Description: "Download an attachment",
					Arguments: o.example(`,"attachment_id":3,"local_path":"~/downloads/log.txt"`)}},
			}
		},
		local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
			raw json.RawMessage) (run, func(), error) {
			arguments, repo, err := o.read(allowed, raw, true)
			if err != nil {
				return nil, nil, err
			}
			if arguments.LocalPath == nil {
				return nil, nil, invalidArgument("local_path is required")
			}
			// The target is prepared before the token is resolved, so a path outside the released directories,
			// or a file that would be replaced without confirmation, is refused without secret access or a request.
			download, err := localfile.CreateForDownload(ctx, resolved, *arguments.LocalPath)
			if err != nil {
				return nil, nil, err
			}
			release := func() { _ = download.Abort() }
			id := arguments.owner(o)
			return func(ctx context.Context, c *Client) (any, error) {
				op := "download " + o.label
				found, err := o.readAttachment(ctx, c, allowed, repo, op, id, arguments.AttachmentID)
				if err != nil {
					return nil, err
				}
				path, err := assetContentPath(c.origin, found)
				if err != nil {
					return nil, err
				}
				size, ok := found.integer("size")
				if !ok || size < 0 {
					return nil, provider.InvalidResponse(op, c.product.name+" returned an attachment without its size")
				}
				if size > maxAssetBytes {
					return nil, invalidArgument("the attachment is larger than the limit of 2 GiB")
				}
				if err := download.ExpectSize(size); err != nil {
					return nil, err
				}
				if err := c.fetchFile(ctx, op, path, size, download); err != nil {
					return nil, err
				}
				if err := download.Commit(); err != nil {
					return nil, err
				}
				sum, _ := download.SHA256()
				result := map[string]any{o.idName: id, "attachment_id": arguments.AttachmentID,
					"local_path": *arguments.LocalPath, "size": download.Size(), "sha256": sum}
				if name, ok := found.text("name"); ok {
					result["name"] = name
				}
				return result, nil
			}, release, nil
		},
	}
}

func (o attachmentOwner) upload() *tool {
	return &tool{
		group: o.group, action: "upload", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "Upload an attachment to a " + p.name + " " + o.noun,
				Description: "Upload one local file of at most 2 GiB as an attachment of a " + o.noun + " of a " +
					"repository inside the connection's targets, streamed from a directory the connection releases " +
					"for reading. The instance may refuse a file type or size its settings do not allow. Not " +
					"idempotent: a repeated call adds another attachment.",
				Tags:       []string{p.id, "attachments", "upload", "create"},
				Risk:       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+issueSensitivity),
				LocalFiles: config.LocalFilesRead,
				InputSchema: inputSchema(o.properties()+`,"name":`+assetSchemaName+`,"`+localfile.LocalPathArgument+`":`+
					localfile.LocalPathSchema, o.idName, "name", localfile.LocalPathArgument),
				OutputSchema: assetOutput(p),
				Arguments: []capability.Argument{repositoryArgument, o.argument,
					{Name: "name", Required: true, Description: "Name of the attachment, 1 to 255 characters; the " +
						"local file's own name is not sent"},
					{Name: localfile.LocalPathArgument, Required: true, Description: "Local file to upload, at most " +
						"2 GiB, absolute or starting with ~/, inside a directory the connection releases for reading"}},
				Fields: fieldsOf(p, attachmentMembers),
				Examples: []capability.Example{{Description: "Upload a log as an attachment",
					Arguments: o.example(`,"name":"log.txt","local_path":"~/logs/log.txt"`)}},
			}
		},
		local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
			raw json.RawMessage) (run, func(), error) {
			arguments, repo, err := o.read(allowed, raw, false)
			if err != nil {
				return nil, nil, err
			}
			if arguments.Name == nil {
				return nil, nil, invalidArgument("name is required")
			}
			if err := checkAssetName(*arguments.Name); err != nil {
				return nil, nil, err
			}
			if arguments.LocalPath == nil {
				return nil, nil, invalidArgument("local_path is required")
			}
			upload, err := localfile.OpenForUpload(ctx, resolved, *arguments.LocalPath)
			if err != nil {
				return nil, nil, err
			}
			if upload.Size > maxAssetBytes {
				_ = upload.Close()
				return nil, nil, invalidArgument("the file is larger than the limit of 2 GiB")
			}
			release := func() { _ = upload.Close() }
			name, id := *arguments.Name, arguments.owner(o)
			return func(ctx context.Context, c *Client) (any, error) {
				op := "upload " + o.label
				if err := o.bind(ctx, c, allowed, repo, op, id, 0); err != nil {
					return nil, err
				}
				var created object
				if err := c.sendFile(ctx, op, o.attachmentsPath(repo, id), url.Values{"name": {name}},
					"attachment", name, upload, upload.Size, &created); err != nil {
					return nil, err
				}
				if got, ok := created.integer("id"); !ok || got < 1 {
					return nil, unsure(provider.InvalidResponse(op, c.product.name+" returned an attachment without its ID"))
				}
				return project(p, attachmentMembers, created), nil
			}, release, nil
		},
	}
}

func (o attachmentOwner) update() *tool {
	return &tool{
		group: o.group, action: "update", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "Rename an attachment of a " + p.name + " " + o.noun,
				Description: "Change the name of one attachment of a " + o.noun + " of a repository inside the " +
					"connection's targets; the content stays. The attachment must be one the " + o.noun + " lists.",
				Tags:         []string{p.id, "attachments", "update"},
				Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+issueSensitivity),
				InputSchema:  inputSchema(o.withAttachment()+`,"name":`+assetSchemaName, o.idName, "attachment_id", "name"),
				OutputSchema: assetOutput(p),
				Arguments: []capability.Argument{repositoryArgument, o.argument, attachmentIDArgument,
					{Name: "name", Required: true, Description: "New name of the attachment, 1 to 255 characters"}},
				Fields: fieldsOf(p, attachmentMembers),
				Examples: []capability.Example{{Description: "Rename an attachment",
					Arguments: o.example(`,"attachment_id":3,"name":"crash.log"`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			arguments, repo, err := o.read(allowed, raw, true)
			if err != nil {
				return nil, err
			}
			if arguments.Name == nil {
				return nil, invalidArgument("name is required")
			}
			if err := checkAssetName(*arguments.Name); err != nil {
				return nil, err
			}
			body, id := map[string]any{"name": *arguments.Name}, arguments.owner(o)
			return func(ctx context.Context, c *Client) (any, error) {
				op := "update " + o.label
				if err := o.bind(ctx, c, allowed, repo, op, id, arguments.AttachmentID); err != nil {
					return nil, err
				}
				var updated object
				if err := c.change(ctx, op, http.MethodPatch, o.attachmentPath(repo, id, arguments.AttachmentID), body,
					&updated); err != nil {
					return nil, err
				}
				if err := assetAnswer(c, op, updated, arguments.AttachmentID); err != nil {
					return nil, err
				}
				return project(p, attachmentMembers, updated), nil
			}, nil
		},
	}
}

func (o attachmentOwner) remove() *tool {
	return &tool{
		group: o.group, action: "delete", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "Delete an attachment of a " + p.name + " " + o.noun,
				Description: "Delete one attachment of a " + o.noun + " of a repository inside the connection's " +
					"targets for good; this cannot be undone. The attachment must be one the " + o.noun + " lists. " +
					"Only a connection whose tools list names it offers this tool.",
				Tags:                  []string{p.id, "attachments", "delete"},
				Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+issueSensitivity),
				RequiresToolAllowList: true,
				InputSchema:           inputSchema(o.withAttachment(), o.idName, "attachment_id"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{` + o.ownerOutput() +
					`,"attachment_id":{"type":"integer"},"deleted":{"type":"boolean"}},"required":["` + o.idName +
					`","attachment_id","deleted"],"additionalProperties":false}`),
				Arguments: []capability.Argument{repositoryArgument, o.argument, attachmentIDArgument},
				Fields: []capability.Field{o.ownerField(), {Name: "attachment_id", Description: "ID of the attachment"},
					{Name: "deleted", Description: "True once the instance deleted the attachment"}},
				Examples: []capability.Example{{Description: "Delete an attachment", Arguments: o.example(`,"attachment_id":3`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			arguments, repo, err := o.read(allowed, raw, true)
			if err != nil {
				return nil, err
			}
			id := arguments.owner(o)
			return func(ctx context.Context, c *Client) (any, error) {
				op := "delete " + o.label
				if err := o.bind(ctx, c, allowed, repo, op, id, arguments.AttachmentID); err != nil {
					return nil, err
				}
				if err := c.change(ctx, op, http.MethodDelete, o.attachmentPath(repo, id, arguments.AttachmentID), nil,
					nil); err != nil {
					return nil, err
				}
				return map[string]any{o.idName: id, "attachment_id": arguments.AttachmentID, "deleted": true}, nil
			}, nil
		},
	}
}

var (
	issueAttachmentsList     = issueAttachmentOwner.list()
	issueAttachmentsGet      = issueAttachmentOwner.get()
	issueAttachmentsUpload   = issueAttachmentOwner.upload()
	issueAttachmentsUpdate   = issueAttachmentOwner.update()
	issueAttachmentsDelete   = issueAttachmentOwner.remove()
	issueAttachmentsDownload = issueAttachmentOwner.download()

	commentAttachmentsList     = commentAttachmentOwner.list()
	commentAttachmentsGet      = commentAttachmentOwner.get()
	commentAttachmentsUpload   = commentAttachmentOwner.upload()
	commentAttachmentsUpdate   = commentAttachmentOwner.update()
	commentAttachmentsDelete   = commentAttachmentOwner.remove()
	commentAttachmentsDownload = commentAttachmentOwner.download()
)
