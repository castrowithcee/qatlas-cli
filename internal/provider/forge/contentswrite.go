package forge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The content write tools send exactly one commit request each. The instance builds the commit: the identity
// of the token is the default, and author, committer, and dates are only sent when the call names them. No tool
// offers a force push or a force overwrite of a new branch, and no batch deletes: a file is deleted only by
// contents.delete, one file per call. Content travels as Base64 in the request, from text or from a local file
// that the connection releases; both are read and bounded before the token is resolved.

const (
	maxCommitMessage = 4096
	maxChangeFiles   = 50
	maxPatchBytes    = 1 << 20
	maxIdentityText  = 255
	maxEmailText     = 254

	identitySchema = `{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"email":{"type":"string","minLength":3,"maxLength":254}},"required":["name","email"],` +
		`"additionalProperties":false}`
	dateSchema  = `{"type":"string","minLength":20,"maxLength":40}`
	datesSchema = `{"type":"object","properties":{"author":` + dateSchema + `,"committer":` + dateSchema +
		`},"additionalProperties":false}`
	writeTextSchema = `{"type":"string","maxLength":262144}`

	// commitKeys are the input properties every commit tool shares.
	commitKeys = `"branch":` + contentRefSchema + `,"new_branch":` + contentRefSchema +
		`,"message":{"type":"string","minLength":1,"maxLength":4096},"author":` + identitySchema +
		`,"committer":` + identitySchema + `,"dates":` + datesSchema + `,"signoff":{"type":"boolean"}`
)

// maxWriteBytes bounds the decoded content of one call, local files included; tests lower it.
var maxWriteBytes int64 = 8 << 20

var commitArguments = []capability.Argument{
	{Name: "branch", Description: "Branch the change is based on and committed to; the default branch when omitted"},
	{Name: "new_branch", Description: "Branch to create from branch for this change; an existing name is refused"},
	{Name: "message", Description: "Commit message, at most 4096 characters; the instance uses a default when omitted"},
	{Name: "author", Description: "Author of the commit as an object with name and email; the committer or the " +
		"token's identity when omitted"},
	{Name: "committer", Description: "Committer of the commit as an object with name and email; the author or the " +
		"token's identity when omitted"},
	{Name: "dates", Description: "Object with author and committer times in RFC 3339; the commit time when omitted"},
	{Name: "signoff", Description: "True adds a Signed-off-by line of the committer to the message"},
}

type commitIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type commitDates struct {
	Author    string `json:"author"`
	Committer string `json:"committer"`
}

// commitOptions are the arguments every commit tool takes besides its files.
type commitOptions struct {
	Branch    string          `json:"branch"`
	NewBranch string          `json:"new_branch"`
	Message   string          `json:"message"`
	Author    *commitIdentity `json:"author"`
	Committer *commitIdentity `json:"committer"`
	Dates     *commitDates    `json:"dates"`
	Signoff   bool            `json:"signoff"`
}

func (i *commitIdentity) check(field string) (map[string]any, error) {
	name := strings.TrimSpace(i.Name)
	if name == "" || len(i.Name) > maxIdentityText || !plainText(i.Name) || strings.ContainsAny(i.Name, "<>") {
		return nil, invalidArgument(field + ".name must be 1 to 255 characters without control characters, < or >")
	}
	address, err := mail.ParseAddress(i.Email)
	if len(i.Email) > maxEmailText || !plainText(i.Email) || err != nil || address.Address != i.Email {
		return nil, invalidArgument(field + ".email must be a plain email address of at most 254 characters")
	}
	return map[string]any{"name": i.Name, "email": i.Email}, nil
}

func checkDate(field, value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", invalidArgument(field + " must be a time in RFC 3339 form")
	}
	return parsed.Format(time.RFC3339), nil
}

// body is the part of a commit request body the options decide.
func (o *commitOptions) body() (map[string]any, error) {
	body := map[string]any{}
	if o.Branch != "" {
		if err := checkRefName("branch", o.Branch, true); err != nil {
			return nil, err
		}
		body["branch"] = o.Branch
	}
	if o.NewBranch != "" {
		if err := checkRefName("new_branch", o.NewBranch, true); err != nil {
			return nil, err
		}
		body["new_branch"] = o.NewBranch
	}
	if o.Message != "" {
		if len(o.Message) > maxCommitMessage || strings.TrimSpace(o.Message) == "" ||
			!plainTextAllowNewlines(o.Message) || !utf8.ValidString(o.Message) {
			return nil, invalidArgument("message must be a text of at most 4096 bytes without control characters")
		}
		body["message"] = o.Message
	}
	for field, identity := range map[string]*commitIdentity{"author": o.Author, "committer": o.Committer} {
		if identity == nil {
			continue
		}
		checked, err := identity.check(field)
		if err != nil {
			return nil, err
		}
		body[field] = checked
	}
	if o.Dates != nil {
		dates := map[string]any{}
		for field, value := range map[string]string{"author": o.Dates.Author, "committer": o.Dates.Committer} {
			if value == "" {
				continue
			}
			formatted, err := checkDate("dates."+field, value)
			if err != nil {
				return nil, err
			}
			dates[field] = formatted
		}
		if len(dates) > 0 {
			body["dates"] = dates
		}
	}
	if o.Signoff {
		body["signoff"] = true
	}
	return body, nil
}

// checkBlobSHA is the object hash of the file version a change builds on.
func checkBlobSHA(field, value string) error {
	if checkCommitID(value) != nil {
		return invalidArgument(field + " must be the object hash of the current file, 7 to 64 hexadecimal digits")
	}
	return nil
}

// fileSource is where the content of a file comes from: text or a local file, exactly one.
type fileSource struct {
	Content   *string `json:"content"`
	LocalPath *string `json:"local_path"`
}

// encode reads the content, charges it to budget, and returns it Base64-encoded; present is false when the
// source names no content.
func (s fileSource) encode(ctx context.Context, resolved *config.Resolved, field string, budget *int64) (string, bool,
	error) {
	switch {
	case s.Content == nil && s.LocalPath == nil:
		return "", false, nil
	case s.Content != nil && s.LocalPath != nil:
		return "", false, invalidArgument(field + ": content and " + localfile.LocalPathArgument +
			" exclude each other")
	case s.Content != nil:
		text := *s.Content
		if len(text) > maxContentBytes || !utf8.ValidString(text) {
			return "", false, invalidArgument(field + ".content must be valid text of at most 256 KiB; use " +
				localfile.LocalPathArgument + " for more or binary content")
		}
		if int64(len(text)) > *budget {
			return "", false, invalidArgument("the content of the call is larger than the limit of " +
				strconv.FormatInt(maxWriteBytes, 10) + " bytes")
		}
		*budget -= int64(len(text))
		return base64.StdEncoding.EncodeToString([]byte(text)), true, nil
	}
	upload, err := localfile.OpenForUpload(ctx, resolved, *s.LocalPath)
	if err != nil {
		return "", false, err
	}
	defer upload.Close()
	if upload.Size > *budget {
		return "", false, invalidArgument("the content of the call is larger than the limit of " +
			strconv.FormatInt(maxWriteBytes, 10) + " bytes")
	}
	data, err := io.ReadAll(upload)
	if err != nil {
		return "", false, err
	}
	*budget -= int64(len(data))
	return base64.StdEncoding.EncodeToString(data), true, nil
}

// requireContent is encode for an operation that cannot go without content.
func (s fileSource) requireContent(ctx context.Context, resolved *config.Resolved, field string,
	budget *int64) (string, error) {
	encoded, present, err := s.encode(ctx, resolved, field, budget)
	if err != nil {
		return "", err
	}
	if !present {
		return "", invalidArgument(field + ": exactly one of content and " + localfile.LocalPathArgument +
			" is required")
	}
	return encoded, nil
}

// commit sends the one request of a commit tool. A 400, 409, or 422 says the change was refused for its
// content, which is usually a missing or stale sha, an existing or missing path, or a branch that moved; the
// instance's own text is never shown.
func (c *Client) commit(ctx context.Context, op, method, path string, body map[string]any) (object, error) {
	var answer object
	status, err := c.changeQuery(ctx, op, method, path, nil, body, &answer)
	if err != nil {
		var failure *provider.Error
		if (status == http.StatusBadRequest || status == http.StatusConflict ||
			status == http.StatusUnprocessableEntity) && errors.As(err, &failure) &&
			failure.Class == provider.ClassProviderError {
			failure.Message = c.product.name + " refused the change (HTTP " + strconv.Itoa(status) + "); the sha " +
				"may be missing or stale, the path may exist or be missing, the branch may be protected or have " +
				"moved, or the patch may not apply; read the current state before repeating"
		}
		return nil, err
	}
	return answer, nil
}

var fileCommitMembers = []member{
	{name: "sha", kind: kindText, note: "Commit hash"},
	{name: "message", kind: kindText, max: maxPullBody, note: "Commit message, untrusted data, at most 64 KiB"},
	{name: "url", kind: kindText, address: true, note: "API address; left out with user info or query"},
	{name: "html_url", kind: kindText, address: true, note: "Web address; left out with user info or query"},
	{name: "created", kind: kindText, note: "Commit time"},
	{name: "author", kind: kindObject, sub: commitUserMembers, note: "Author; personal data"},
	{name: "committer", kind: kindObject, sub: commitUserMembers, note: "Committer; personal data"},
	{name: "parents", kind: kindObjects, sub: commitMetaMembers, note: "Parent commits"},
	{name: "tree", kind: kindObject, sub: commitMetaMembers, note: "Tree of the commit"},
}

// writtenMembers are the members of a file the instance reports after a write; the content of a write is
// never echoed.
var writtenMembers = slices.DeleteFunc(slices.Clone(contentMembers), func(m member) bool {
	return m.name == "content" || m.name == "binary" || m.name == "content_truncated"
})

var (
	repositoryResultMember = member{name: "repository", kind: kindText, note: "Repository, as OWNER/REPO"}
	commitResultMember     = member{name: "commit", kind: kindObject, sub: fileCommitMembers,
		note: "The commit the instance made"}
	verificationResultMember = member{name: "verification", kind: kindObject, sub: branchVerificationMembers,
		note: "Signature verification of the commit"}

	fileResultMembers = []member{repositoryResultMember,
		{name: "content", kind: kindObject, sub: writtenMembers, note: "The written file"},
		commitResultMember, verificationResultMember}
	filesResultMembers = []member{repositoryResultMember,
		{name: "files", kind: kindObjects, sub: writtenMembers, note: "The files the commit wrote"},
		commitResultMember, verificationResultMember}
	deleteResultMembers = []member{repositoryResultMember,
		{name: "path", kind: kindText, note: "Path of the deleted file"},
		{name: "deleted", kind: kindFlag, note: "True once the instance deleted the file"},
		commitResultMember, verificationResultMember}
)

// writeResult projects an answer and names the repository.
func writeResult(p *product, members []member, repo target, answer object) map[string]any {
	result := project(p, members, answer)
	result["repository"] = repo.owner + "/" + repo.repo
	return result
}

func writeOutput(p *product, members []member) json.RawMessage {
	return json.RawMessage(schemaOf(p, members, "repository"))
}

// release is the no-op release of a write tool whose files were read completely.
func release() {}

const writeSensitivity = contentSensitivity

var contentsCreate = &tool{
	group: "contents", action: "create", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Create a file in a " + p.name + " repository",
			Description: "Create exactly one new file in a repository inside the connection's targets as one " +
				"commit. The content is text of at most 256 KiB or a local file, at most 8 MiB, from a directory " +
				"the connection releases for reading. An existing path, a protected branch, or an existing " +
				"new_branch is refused by the instance. No force push is offered. Not idempotent: a repeated " +
				"call is refused as the path then exists.",
			Tags:       []string{p.id, "contents", "create"},
			Risk:       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+writeSensitivity),
			LocalFiles: config.LocalFilesRead,
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"path":`+contentPathSchema+`,"content":`+
				writeTextSchema+`,`+localPathProperty()+`,`+commitKeys, "path"),
			OutputSchema: writeOutput(p, fileResultMembers),
			Arguments: append([]capability.Argument{repositoryArgument, contentPathArgument,
				{Name: "content", Description: "File content as text, at most 256 KiB; instead of local_path"},
				localfile.UploadPathArgument()}, commitArguments...),
			Fields: fieldsOf(p, fileResultMembers),
			Examples: []capability.Example{{Description: "Create a file on a new branch",
				Arguments: json.RawMessage(`{"repository":"example/project","path":"docs/notes.md",` +
					`"content":"# Notes\n","new_branch":"docs/notes","message":"docs: add notes"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var arguments struct {
			Repository string `json:"repository"`
			Path       string `json:"path"`
			fileSource
			commitOptions
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, nil, err
		}
		if err := checkFilePath("path", arguments.Path); err != nil {
			return nil, nil, err
		}
		body, err := arguments.commitOptions.body()
		if err != nil {
			return nil, nil, err
		}
		budget := maxWriteBytes
		if body["content"], err = arguments.requireContent(ctx, resolved, "content", &budget); err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			answer, err := c.commit(ctx, "create file", http.MethodPost, treePath(repo, "contents", arguments.Path),
				body)
			if err != nil {
				return nil, err
			}
			return writeResult(p, fileResultMembers, repo, answer), nil
		}, release, nil
	},
}

var contentsUpdate = &tool{
	group: "contents", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Update a file in a " + p.name + " repository",
			Description: "Replace the content of exactly one existing file in a repository inside the connection's " +
				"targets as one commit. sha is the object hash of the file version being replaced, as " +
				"contents.get reports it; a missing or stale sha is refused by the instance. The content is " +
				"text of at most 256 KiB or a local file, at most 8 MiB, from a directory the connection releases " +
				"for reading. No force push is offered. Idempotent in effect: a repeated call carries the old " +
				"sha, which the instance then refuses.",
			Tags:       []string{p.id, "contents", "update"},
			Risk:       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+writeSensitivity),
			LocalFiles: config.LocalFilesRead,
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"path":`+contentPathSchema+`,"sha":`+branchSchemaID+
				`,"content":`+writeTextSchema+`,`+localPathProperty()+`,`+commitKeys, "path", "sha"),
			OutputSchema: writeOutput(p, fileResultMembers),
			Arguments: append([]capability.Argument{repositoryArgument, contentPathArgument,
				{Name: "sha", Required: true, Description: "Object hash of the file version to replace, 7 to 64 " +
					"hexadecimal digits"},
				{Name: "content", Description: "New file content as text, at most 256 KiB; instead of local_path"},
				localfile.UploadPathArgument()}, commitArguments...),
			Fields: fieldsOf(p, fileResultMembers),
			Examples: []capability.Example{{Description: "Replace a file",
				Arguments: json.RawMessage(`{"repository":"example/project","path":"docs/notes.md",` +
					`"sha":"0123456789abcdef0123456789abcdef01234567","content":"# Notes\nmore\n"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var arguments struct {
			Repository string `json:"repository"`
			Path       string `json:"path"`
			SHA        string `json:"sha"`
			fileSource
			commitOptions
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, nil, err
		}
		if err := checkFilePath("path", arguments.Path); err != nil {
			return nil, nil, err
		}
		if err := checkBlobSHA("sha", arguments.SHA); err != nil {
			return nil, nil, err
		}
		body, err := arguments.commitOptions.body()
		if err != nil {
			return nil, nil, err
		}
		body["sha"] = arguments.SHA
		budget := maxWriteBytes
		if body["content"], err = arguments.requireContent(ctx, resolved, "content", &budget); err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			answer, err := c.commit(ctx, "update file", http.MethodPut, treePath(repo, "contents", arguments.Path),
				body)
			if err != nil {
				return nil, err
			}
			return writeResult(p, fileResultMembers, repo, answer), nil
		}, release, nil
	},
}

var contentsDelete = &tool{
	group: "contents", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a file in a " + p.name + " repository",
			Description: "Delete exactly one file in a repository inside the connection's targets as one commit; " +
				"the file stays in the history. sha is the object hash of the version being deleted, as " +
				"contents.get reports it; a stale sha is refused. Only a connection whose tools list names it " +
				"offers this tool.",
			Tags:                  []string{p.id, "contents", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+writeSensitivity),
			RequiresToolAllowList: true,
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"path":`+contentPathSchema+`,"sha":`+branchSchemaID+
				`,`+commitKeys, "path", "sha"),
			OutputSchema: writeOutput(p, deleteResultMembers),
			Arguments: append([]capability.Argument{repositoryArgument, contentPathArgument,
				{Name: "sha", Required: true, Description: "Object hash of the file version to delete, 7 to 64 " +
					"hexadecimal digits"}}, commitArguments...),
			Fields: fieldsOf(p, deleteResultMembers),
			Examples: []capability.Example{{Description: "Delete a file",
				Arguments: json.RawMessage(`{"repository":"example/project","path":"docs/old.md",` +
					`"sha":"0123456789abcdef0123456789abcdef01234567","message":"docs: remove old notes"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Path       string `json:"path"`
			SHA        string `json:"sha"`
			commitOptions
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkFilePath("path", arguments.Path); err != nil {
			return nil, err
		}
		if err := checkBlobSHA("sha", arguments.SHA); err != nil {
			return nil, err
		}
		body, err := arguments.commitOptions.body()
		if err != nil {
			return nil, err
		}
		body["sha"] = arguments.SHA
		return func(ctx context.Context, c *Client) (any, error) {
			answer, err := c.commit(ctx, "delete file", http.MethodDelete, treePath(repo, "contents", arguments.Path),
				body)
			if err != nil {
				return nil, err
			}
			result := writeResult(p, deleteResultMembers, repo, answer)
			result["path"], result["deleted"] = arguments.Path, true
			return result, nil
		}, nil
	},
}

// changeOperations are the operations of a batch per product: Gitea adds upload and rename. A delete is never
// part of a batch.
func changeOperations(p *product) []string {
	if p == gitea {
		return []string{"create", "update", "upload", "rename"}
	}
	return []string{"create", "update"}
}

type changeFile struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	SHA       string `json:"sha"`
	FromPath  string `json:"from_path"`
	fileSource
}

// entry checks one file of a batch against its operation and returns the file of the request body.
func (f *changeFile) entry(ctx context.Context, p *product, resolved *config.Resolved, index int,
	budget *int64) (map[string]any, error) {
	field := "files[" + strconv.Itoa(index) + "]"
	switch {
	case f.Operation == "delete":
		return nil, invalidArgument(field + ".operation delete is not offered in a batch; contents.delete deletes " +
			"one file")
	case (f.Operation == "upload" || f.Operation == "rename") && p != gitea:
		return nil, otherProduct(p, field+".operation "+f.Operation)
	case !slices.Contains(changeOperations(p), f.Operation):
		return nil, invalidArgument(field + ".operation must be one of " + strings.Join(changeOperations(p), ", "))
	}
	if err := checkFilePath(field+".path", f.Path); err != nil {
		return nil, err
	}
	entry := map[string]any{"operation": f.Operation, "path": f.Path}
	if f.SHA != "" {
		if f.Operation == "create" {
			return nil, invalidArgument(field + ".sha is not used by create")
		}
		if err := checkBlobSHA(field+".sha", f.SHA); err != nil {
			return nil, err
		}
		entry["sha"] = f.SHA
	} else if f.Operation == "update" {
		return nil, invalidArgument(field + ".sha is required by update")
	}
	if f.FromPath != "" {
		if f.Operation != "rename" {
			return nil, invalidArgument(field + ".from_path is only used by rename")
		}
		if err := checkFilePath(field+".from_path", f.FromPath); err != nil {
			return nil, err
		}
		if f.FromPath == f.Path {
			return nil, invalidArgument(field + ".from_path must differ from path")
		}
		entry["from_path"] = f.FromPath
	} else if f.Operation == "rename" {
		return nil, invalidArgument(field + ".from_path is required by rename")
	}
	var encoded string
	var err error
	if f.Operation == "rename" {
		var present bool
		if encoded, present, err = f.encode(ctx, resolved, field, budget); err == nil && present {
			entry["content"] = encoded
		}
	} else if encoded, err = f.requireContent(ctx, resolved, field, budget); err == nil {
		entry["content"] = encoded
	}
	if err != nil {
		return nil, err
	}
	return entry, nil
}

func changeFilesSchema(p *product) string {
	operations := make([]string, 0, 4)
	for _, operation := range changeOperations(p) {
		operations = append(operations, strconv.Quote(operation))
	}
	file := `{"type":"object","properties":{"operation":{"type":"string","enum":[` + strings.Join(operations, ",") +
		`]},"path":` + contentPathSchema + `,"sha":` + branchSchemaID + `,"from_path":` + contentPathSchema +
		`,"content":` + writeTextSchema + `,` + localPathProperty() + `},"required":["operation","path"],` +
		`"additionalProperties":false}`
	return `{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxChangeFiles) + `,"items":` + file + `}`
}

var contentsChangeFiles = &tool{
	group: "contents", action: "change-files", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		operations := "create and update"
		if p == gitea {
			operations = "create, update, upload (create or replace), and rename"
		}
		return capability.Descriptor{
			Title: "Change several files in a " + p.name + " repository",
			Description: "Write up to 50 files of a repository inside the connection's targets as one commit " +
				"(" + operations + "), each path at most once. update needs the sha of the version being " +
				"replaced. Each file's content is text of at most 256 KiB or a local file from a directory the " +
				"connection releases for reading, together at most 8 MiB. A batch never deletes: contents.delete " +
				"deletes one file. No force push is offered. Not idempotent: a repeated call is refused or adds " +
				"another commit on the instance.",
			Tags:       []string{p.id, "contents", "change-files"},
			Risk:       changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+writeSensitivity),
			LocalFiles: config.LocalFilesRead,
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"files":`+changeFilesSchema(p)+`,`+commitKeys,
				"files"),
			OutputSchema: writeOutput(p, filesResultMembers),
			Arguments: append([]capability.Argument{repositoryArgument,
				{Name: "files", Required: true, Description: "Files to write, 1 to 50: objects with operation (" +
					operations + "), path, content or local_path, sha (required by update), and from_path " +
					"(required by rename, Gitea only)"}}, commitArguments...),
			Fields: fieldsOf(p, filesResultMembers),
			Examples: []capability.Example{{Description: "Create one file and update another in one commit",
				Arguments: json.RawMessage(`{"repository":"example/project","message":"docs: update notes","files":[` +
					`{"operation":"create","path":"docs/a.md","content":"# A\n"},` +
					`{"operation":"update","path":"docs/b.md","sha":"0123456789abcdef0123456789abcdef01234567",` +
					`"content":"# B\n"}]}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var arguments struct {
			Repository string       `json:"repository"`
			Files      []changeFile `json:"files"`
			commitOptions
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, nil, err
		}
		if len(arguments.Files) < 1 || len(arguments.Files) > maxChangeFiles {
			return nil, nil, invalidArgument("files takes 1 to 50 entries")
		}
		body, err := arguments.commitOptions.body()
		if err != nil {
			return nil, nil, err
		}
		budget := maxWriteBytes
		entries := make([]map[string]any, 0, len(arguments.Files))
		paths := map[string]bool{}
		for i := range arguments.Files {
			file := &arguments.Files[i]
			if paths[file.Path] {
				return nil, nil, invalidArgument("files names a path twice")
			}
			paths[file.Path] = true
			entry, err := file.entry(ctx, p, resolved, i, &budget)
			if err != nil {
				return nil, nil, err
			}
			entries = append(entries, entry)
		}
		body["files"] = entries
		return func(ctx context.Context, c *Client) (any, error) {
			answer, err := c.commit(ctx, "change files", http.MethodPost, treePath(repo, "contents", ""), body)
			if err != nil {
				return nil, err
			}
			return writeResult(p, filesResultMembers, repo, answer), nil
		}, release, nil
	},
}

var contentsApplyDiff = &tool{
	group: "contents", action: "apply-diff", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Apply a patch to a " + p.name + " repository",
			Description: "Apply a unified diff of at most 1 MiB to a repository inside the connection's targets as " +
				"one commit. A patch that does not apply is refused by the instance. No force push is offered. " +
				"Not idempotent: a repeated call is refused or adds another commit on the instance.",
			Tags: []string{p.id, "contents", "apply-diff"},
			Risk: changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+writeSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"content":{"type":"string","minLength":1,`+
				`"maxLength":1048576},`+commitKeys, "content"),
			OutputSchema: writeOutput(p, fileResultMembers),
			Arguments: append([]capability.Argument{repositoryArgument,
				{Name: "content", Required: true, Description: "The patch as unified diff text, at most 1 MiB"}},
				commitArguments...),
			Fields: fieldsOf(p, fileResultMembers),
			Examples: []capability.Example{{Description: "Apply a one-line patch",
				Arguments: json.RawMessage(`{"repository":"example/project","message":"fix: typo",` +
					`"content":"diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-Helo\n+Hello\n"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Content    string `json:"content"`
			commitOptions
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if arguments.Content == "" || len(arguments.Content) > maxPatchBytes || !utf8.ValidString(arguments.Content) ||
			strings.IndexByte(arguments.Content, 0) >= 0 {
			return nil, invalidArgument("content must be a patch of text of 1 byte to 1 MiB without NUL")
		}
		body, err := arguments.commitOptions.body()
		if err != nil {
			return nil, err
		}
		body["content"] = arguments.Content
		return func(ctx context.Context, c *Client) (any, error) {
			answer, err := c.commit(ctx, "apply patch", http.MethodPost, treePath(repo, "diffpatch", ""), body)
			if err != nil {
				return nil, err
			}
			return writeResult(p, fileResultMembers, repo, answer), nil
		}, nil
	},
}
