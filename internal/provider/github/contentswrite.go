package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Repository writes: branches and file contents. github.branches.create makes one new branch of a repository
// from a branch, a tag, or a commit SHA, resolved through the same commits route github.commits.get uses, so
// it always starts at a real commit; a branch name already taken is refused with a clear message instead of a
// second attempt. github.contents.put creates a new file, or, while sha names its current blob, updates an
// existing one; a missing or stale sha is refused with the file's current SHA once it can be read. github.
// contents.delete deletes one file with its current blob sha and is offered only where a connection's tools
// list names it, since deleting a file the wrong branch relied on cannot be undone. All three refuse a path
// below .github/workflows/, which the workflow file tools maintain instead with their own workflow token
// requirement, and every route lies below the chosen repository. Every change is sent at most once, and none
// of the three ever answers with a file's content: only its path, its blob SHA, and the commit GitHub made.

// maxContentsPutBytes bounds the content github.contents.put writes, the way maxWorkflowFile bounds a
// workflow file's.
const maxContentsPutBytes = maxWorkflowFile

const branchCreatedOutput = `{"type":"object","properties":{"name":{"type":"string"},"ref":{"type":"string"},` +
	`"sha":{"type":"string"},"from":{"type":"string"}},"required":["name","ref","sha","from"],` +
	`"additionalProperties":false}`

var branchesCreate = capability.Descriptor{
	ID:      Provider + ".branches.create",
	Version: 1,
	Title:   "Create a GitHub branch",
	Description: "Create one new branch of a repository an explicit connection allows from a branch, a tag, " +
		"or a commit SHA, or, when from is left out, the repository's default branch; a branch name already " +
		"taken is refused with a clear message instead of a second attempt",
	Tags:                       []string{"github", "branches", "create"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"name":`+refSchema+`,"from":`+refSchema, "name"),
	OutputSchema:               json.RawMessage(branchCreatedOutput),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Name of the new branch", Required: true},
		{Name: "from", Description: "Branch, tag, or commit SHA the new branch starts at; the repository's " +
			"default branch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "name", Description: "Name of the created branch"},
		{Name: "ref", Description: "Full ref GitHub created: refs/heads/ plus name"},
		{Name: "sha", Description: "Commit SHA the branch points at, resolved from from"},
		{Name: "from", Description: "Branch, tag, or commit SHA the branch was created from; the repository's " +
			"default branch when from was left out"},
	},
	Examples: []capability.Example{{
		Description: "Branch a feature branch off main",
		Arguments:   json.RawMessage(`{"name":"feature/login","from":"main"}`),
	}, {
		Description: "Branch off the repository's default branch",
		Arguments:   json.RawMessage(`{"name":"feature/login"}`),
	}},
}

const contentsPutOutput = `{"type":"object","properties":{"path":{"type":"string"},"sha":{"type":"string"},` +
	`"branch":{"type":"string"},"commit_sha":{"type":"string"},"commit_url":{"type":"string"}},` +
	`"required":["path","sha","commit_sha"],"additionalProperties":false}`

var contentsPut = capability.Descriptor{
	ID:      Provider + ".contents.put",
	Version: 1,
	Title:   "Create or update a GitHub repository file",
	Description: "Create one new file, or, while sha names its current blob, update an existing one, of a " +
		"repository an explicit connection allows; content is written as UTF-8 text and never below " +
		".github/workflows/, which the workflow file tools maintain instead; not idempotent, since a repeated " +
		"call without the file's new sha is refused",
	Tags:                       []string{"github", "contents", "put"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"path":`+contentsPathSchema+`,"content":`+workflowTextSchema+`,"message":`+
		commitMessageSchema+`,"sha":`+blobSHASchema+`,"branch":`+refSchema, "path", "content", "message"),
	OutputSchema: json.RawMessage(contentsPutOutput),
	Arguments: []capability.Argument{
		{Name: "path", Description: "Repository-relative path of the file; never below .github/workflows/", Required: true},
		{Name: "content", Description: "Complete new content of the file as UTF-8 text, at most 512 KiB", Required: true},
		{Name: "message", Description: "Commit message, at most 1000 characters", Required: true},
		{Name: "sha", Description: "Blob SHA of the version to replace, as github.contents.get reports it; " +
			"required to change an existing file, refused for a new one"},
		{Name: "branch", Description: "Branch to commit to; the repository's default branch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "path", Description: "Path of the written file"},
		{Name: "sha", Description: "Blob SHA of the file after the change"},
		{Name: "branch", Description: "Branch the change was committed to, when given"},
		{Name: "commit_sha", Description: "Commit GitHub created for the change"},
		{Name: "commit_url", Description: "URL of the commit GitHub created"},
	},
	Examples: []capability.Example{{
		Description: "Create a new file",
		Arguments:   json.RawMessage(`{"path":"docs/notes.md","content":"# Notes\n","message":"docs: add notes"}`),
	}},
}

const contentsDeletedOutput = `{"type":"object","properties":{"path":{"type":"string"},"branch":{"type":"string"},` +
	`"deleted":{"type":"boolean"},"commit_sha":{"type":"string"},"commit_url":{"type":"string"}},` +
	`"required":["path","deleted","commit_sha"],"additionalProperties":false}`

var contentsDelete = capability.Descriptor{
	ID:      Provider + ".contents.delete",
	Version: 1,
	Title:   "Delete a GitHub repository file",
	Description: "Delete one file of a repository an explicit connection allows, with its current blob sha; " +
		"never below .github/workflows/, which has no delete tool of its own; offered only where a connection's " +
		"tools list names it, because deleting a file the wrong branch relied on cannot be undone",
	Tags:                       []string{"github", "contents", "delete"},
	Risk:                       guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, dataSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: inputSchema(`"path":`+contentsPathSchema+`,"sha":`+blobSHASchema+`,"message":`+commitMessageSchema+
		`,"branch":`+refSchema, "path", "sha", "message"),
	OutputSchema: json.RawMessage(contentsDeletedOutput),
	Arguments: []capability.Argument{
		{Name: "path", Description: "Repository-relative path of the file to delete; never below .github/workflows/", Required: true},
		{Name: "sha", Description: "Blob SHA of the version to delete, as github.contents.get reports it", Required: true},
		{Name: "message", Description: "Commit message, at most 1000 characters", Required: true},
		{Name: "branch", Description: "Branch to commit to; the repository's default branch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "path", Description: "Path of the deleted file"},
		{Name: "branch", Description: "Branch the deletion was committed to, when given"},
		{Name: "deleted", Description: "True once GitHub deleted the file"},
		{Name: "commit_sha", Description: "Commit GitHub created for the deletion"},
		{Name: "commit_url", Description: "URL of the commit GitHub created"},
	},
	Examples: []capability.Example{{
		Description: "Delete an obsolete file",
		Arguments: json.RawMessage(`{"path":"docs/old.md","sha":"3d21ec53a331a6f037a91c368710b99387d012c1",` +
			`"message":"docs: remove old notes"}`),
	}},
}

// contentsWriteArguments holds the arguments of every branch and content write tool; the input schema of
// each tool admits only its own.
type contentsWriteArguments struct {
	Name    string `json:"name"`
	From    string `json:"from"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Message string `json:"message"`
	SHA     string `json:"sha"`
	Branch  string `json:"branch"`
}

// contentsWriteHandler decodes and checks the arguments and the repository before a credential is resolved,
// so a refused request never becomes a provider call.
func contentsWriteHandler(id string, check func(*contentsWriteArguments) error,
	call func(context.Context, *Client, *contentsWriteArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments contentsWriteArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func contentsWriteOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: branchesCreate, Handler: contentsWriteHandler(branchesCreate.ID, checkBranchesCreateArguments,
			func(ctx context.Context, c *Client, a *contentsWriteArguments) (any, error) {
				return c.createBranch(ctx, a)
			})},
		{Descriptor: contentsPut, Handler: contentsWriteHandler(contentsPut.ID, checkContentsPutArguments,
			func(ctx context.Context, c *Client, a *contentsWriteArguments) (any, error) {
				return c.putContents(ctx, a)
			})},
		{Descriptor: contentsDelete, Handler: contentsWriteHandler(contentsDelete.ID, checkContentsDeleteArguments,
			func(ctx context.Context, c *Client, a *contentsWriteArguments) (any, error) {
				return c.deleteContents(ctx, a)
			})},
	}
}

func checkBranchesCreateArguments(a *contentsWriteArguments) error {
	if !validRef(a.Name) {
		return invalidRequest("name must be a usable branch name")
	}
	if a.From != "" && !validRef(a.From) {
		return invalidRequest("from must be a branch, a tag, or a commit SHA")
	}
	return nil
}

// isWorkflowPath reports whether path lies below .github/workflows/, matched case-insensitively: GitHub
// itself may or may not treat the directory name case-sensitively depending on the underlying storage, so the
// narrower reading, refusing more paths rather than fewer, is the safe one.
func isWorkflowPath(path string) bool {
	return len(path) >= len(workflowsDir) && strings.EqualFold(path[:len(workflowsDir)], workflowsDir)
}

const workflowPutMessage = "path lies below .github/workflows/; github.workflowfiles.create and " +
	"github.workflowfiles.update maintain workflow files instead, with their own workflow token requirement"

const workflowDeleteMessage = "path lies below .github/workflows/; the workflow file tools have no delete " +
	"tool, so a workflow file cannot be deleted through github.contents.delete"

func checkContentsPathArgument(path string) error {
	if !validContentsPath(path) {
		return invalidRequest("path must not start or end with /, and must carry no empty, \".\", or \"..\" " +
			"segment, or control character")
	}
	return nil
}

func checkContentsPutArguments(a *contentsWriteArguments) error {
	if err := checkContentsPathArgument(a.Path); err != nil {
		return err
	}
	switch {
	case isWorkflowPath(a.Path):
		return invalidRequest(workflowPutMessage)
	case a.Content == "" || len(a.Content) > maxContentsPutBytes:
		return invalidRequest(fmt.Sprintf("content must hold between 1 byte and %d KiB", maxContentsPutBytes>>10))
	case strings.TrimSpace(a.Message) == "" || utf8.RuneCountInString(a.Message) > maxCommitMessage:
		return invalidRequest(fmt.Sprintf("message must hold between 1 and %d characters", maxCommitMessage))
	case a.SHA != "" && !validBlobSHA(a.SHA):
		return invalidRequest("sha must be the blob SHA of the version to replace, as github.contents.get reports it")
	case a.Branch != "" && !validRef(a.Branch):
		return invalidRequest("branch must be a branch name")
	}
	if err := checkText("content", a.Content); err != nil {
		return err
	}
	return checkText("message", a.Message)
}

func checkContentsDeleteArguments(a *contentsWriteArguments) error {
	if err := checkContentsPathArgument(a.Path); err != nil {
		return err
	}
	switch {
	case isWorkflowPath(a.Path):
		return invalidRequest(workflowDeleteMessage)
	case !validBlobSHA(a.SHA):
		return invalidRequest("sha must be the blob SHA of the version to delete, as github.contents.get reports it")
	case strings.TrimSpace(a.Message) == "" || utf8.RuneCountInString(a.Message) > maxCommitMessage:
		return invalidRequest(fmt.Sprintf("message must hold between 1 and %d characters", maxCommitMessage))
	case a.Branch != "" && !validRef(a.Branch):
		return invalidRequest("branch must be a branch name")
	}
	return checkText("message", a.Message)
}

// Permission messages of the branch and content write tools. GitHub decides on every request; a message
// names what such a request needs without claiming what the configured token holds.
const (
	branchesChangePermission = "GitHub refused this change of a branch of this repository; it needs repo on a " +
		"classic token, or Contents: read and write on a fine-grained token"
	contentsChangePermission = "GitHub refused this change of the contents of this repository; it needs repo " +
		"on a classic token, or Contents: read and write on a fine-grained token, and a branch protection or " +
		"repository rule may forbid it as well"
)

// BranchCreated is the answer to a created branch.
type BranchCreated struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	From string `json:"from"`
}

// defaultBranch reads the name of the bound repository's default branch, for github.branches.create when
// from is left out, the same way the official GitHub tooling falls back to it.
func (c *Client) defaultBranch(ctx context.Context) (string, error) {
	const op = "create branch"
	var raw struct {
		DefaultBranch string `json:"default_branch"`
	}
	path := "/repos/" + url.PathEscape(c.target.owner) + "/" + url.PathEscape(c.target.repo)
	if err := c.rest(ctx, op, path, &raw); err != nil {
		return "", actionsFailure(err, branchesChangePermission)
	}
	if raw.DefaultBranch == "" {
		return "", invalidEntry(op, "a repository")
	}
	return raw.DefaultBranch, nil
}

// resolveCommit reads the commit SHA a branch, a tag, or a commit SHA names, through the same commits route
// github.commits.get uses, so a branch is always created from a real commit of the bound repository.
func (c *Client) resolveCommit(ctx context.Context, ref string) (string, error) {
	const op = "create branch"
	var raw struct {
		SHA string `json:"sha"`
	}
	if err := c.rest(ctx, op, c.repoPath("commits/"+url.PathEscape(ref)), &raw); err != nil {
		return "", actionsFailure(err, branchesChangePermission)
	}
	if !validCommitSHA(raw.SHA) {
		return "", invalidEntry(op, "a commit")
	}
	return raw.SHA, nil
}

// createBranch resolves from to a commit SHA and creates one new branch pointing at it. The request is sent
// directly, instead of through restChange, so a 422 answer can be read once and, when it names an existing
// reference, turned into a clear refusal instead of the generic rejected message; the request is still sent
// exactly once, and every other status is classified exactly like restChange would.
func (c *Client) createBranch(ctx context.Context, a *contentsWriteArguments) (*BranchCreated, error) {
	const op = "create branch"
	from := a.From
	if from == "" {
		branch, err := c.defaultBranch(ctx)
		if err != nil {
			return nil, err
		}
		from = branch
	}
	sha, err := c.resolveCommit(ctx, from)
	if err != nil {
		return nil, err
	}
	ref := "refs/heads/" + a.Name
	payload, err := json.Marshal(map[string]any{"ref": ref, "sha": sha})
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "GitHub", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoints.rest+c.repoPath("git/refs"),
		bytes.NewReader(payload))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	defer c.limiter.HoldFor(mutationInterval)
	if err != nil {
		failure := provider.Transport(op, "GitHub", err)
		if failure.Class == provider.ClassTimeout || failure.Cause == provider.CauseConnectionReset ||
			failure.Cause == provider.CauseUnknown {
			failure.Message += uncertain
		}
		return nil, failure
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusUnprocessableEntity &&
			bytes.Contains(bytes.ToLower(data), []byte("already exists")) {
			return nil, invalidRequest("GitHub already holds a branch named " + a.Name +
				"; choose another name, or change the existing branch instead of creating it again")
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		return nil, actionsFailure(c.statusError(op, response, true), branchesChangePermission)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "the GitHub response could not be read within the size limit" + uncertain}
	}
	var answer struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, invalidResponse(op, true)
	}
	if answer.Ref != ref || !validCommitSHA(answer.Object.SHA) {
		return nil, invalidResponse(op, true)
	}
	return &BranchCreated{Name: a.Name, Ref: answer.Ref, SHA: answer.Object.SHA, From: from}, nil
}

// WrittenContents describes a file github.contents.put wrote, without its content.
type WrittenContents struct {
	Path      string `json:"path"`
	SHA       string `json:"sha"`
	Branch    string `json:"branch,omitempty"`
	CommitSHA string `json:"commit_sha"`
	CommitURL string `json:"commit_url,omitempty"`
}

// putContents creates or, with the blob SHA it replaces, updates one file below the bound repository in one
// request that is never repeated. GitHub refuses an update whose blob SHA is not the file's current one and
// a create of an existing file without a blob SHA, so a version the caller has not seen is never overwritten.
func (c *Client) putContents(ctx context.Context, a *contentsWriteArguments) (*WrittenContents, error) {
	const op = "put repository contents"
	body := map[string]any{"message": a.Message, "content": base64.StdEncoding.EncodeToString([]byte(a.Content))}
	if a.SHA != "" {
		body["sha"] = a.SHA
	}
	if a.Branch != "" {
		body["branch"] = a.Branch
	}
	var answer struct {
		Content struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
		} `json:"content"`
		Commit struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
		} `json:"commit"`
	}
	if err := c.restChange(ctx, op, http.MethodPut, c.contentsPath(a.Path, nil), body, &answer); err != nil {
		return nil, c.contentsWriteFailure(ctx, err, a.Path, a.Branch, a.SHA)
	}
	if answer.Content.Path != a.Path || answer.Content.SHA == "" || answer.Commit.SHA == "" {
		return nil, invalidResponse(op, true)
	}
	return &WrittenContents{Path: answer.Content.Path, SHA: answer.Content.SHA, Branch: a.Branch,
		CommitSHA: answer.Commit.SHA, CommitURL: answer.Commit.HTMLURL}, nil
}

// DeletedContents describes a file github.contents.delete removed.
type DeletedContents struct {
	Path      string `json:"path"`
	Branch    string `json:"branch,omitempty"`
	Deleted   bool   `json:"deleted"`
	CommitSHA string `json:"commit_sha"`
	CommitURL string `json:"commit_url,omitempty"`
}

// deleteContents deletes one file below the bound repository in one request that is never repeated. GitHub
// refuses a blob SHA that is not the file's current one, so a version the caller has not seen is never
// deleted in its place.
func (c *Client) deleteContents(ctx context.Context, a *contentsWriteArguments) (*DeletedContents, error) {
	const op = "delete repository contents"
	body := map[string]any{"message": a.Message, "sha": a.SHA}
	if a.Branch != "" {
		body["branch"] = a.Branch
	}
	var answer struct {
		Commit struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
		} `json:"commit"`
	}
	if err := c.restChange(ctx, op, http.MethodDelete, c.contentsPath(a.Path, nil), body, &answer); err != nil {
		return nil, c.contentsWriteFailure(ctx, err, a.Path, a.Branch, a.SHA)
	}
	if answer.Commit.SHA == "" {
		return nil, invalidResponse(op, true)
	}
	return &DeletedContents{Path: a.Path, Branch: a.Branch, Deleted: true, CommitSHA: answer.Commit.SHA,
		CommitURL: answer.Commit.HTMLURL}, nil
}

// contentsWriteFailure names what a refused file write or delete most likely means. A permission refusal is
// replaced outright. A conflict or a rejected request may mean the given sha is missing or stale, so the file
// is read again: only once that read proves the given sha is not the file's current one, or that a file
// already exists although none was given, is the refusal replaced, with the current SHA, as an invalid
// request the caller can act on directly; if the current sha matches the one given, the conflict had another
// cause (such as a branch protection rule) and the original refusal is returned unchanged; the same holds
// when the file cannot be read again at all, since Qatlas cannot then prove what the refusal meant.
func (c *Client) contentsWriteFailure(ctx context.Context, err error, path, branch, givenSHA string) error {
	var failure *provider.Error
	if !errors.As(err, &failure) {
		return err
	}
	if failure.Class == provider.ClassPermission {
		refused := *failure
		refused.Message = contentsChangePermission
		return &refused
	}
	if failure.Message != conflictMessage && failure.Message != rejectedMessage {
		return err
	}
	current, readErr := c.getContents(ctx, path, branch)
	if readErr != nil || current.Type != "file" || current.SHA == "" || current.SHA == givenSHA {
		return err
	}
	if givenSHA != "" {
		return invalidRequest("the file no longer has the given blob SHA, so nothing was written; its current " +
			"SHA is " + current.SHA + "; read it again with github.contents.get and apply the change to its " +
			"current content")
	}
	return invalidRequest("a file already exists at this path with blob SHA " + current.SHA + "; give sha to " +
		"replace it, or choose another path to create a new file")
}
