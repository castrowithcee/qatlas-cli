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
// list names it, since deleting a file the wrong branch relied on cannot be undone. github.files.push writes
// several files as one commit through the Git Data API: it reads the branch's current head and its tree,
// creates one blob per file and one new tree above it, creates one commit from that tree with the read head
// as its only parent, and moves the branch to it with a fast-forward-only ref update, so a branch that moved
// since the read is refused instead of overwritten; it is offered only where a connection's tools list names
// it, since several files land in one commit that cannot be undone by halves. All four refuse a path below
// .github/workflows/, which the workflow file tools maintain instead with their own workflow token
// requirement, and every route lies below the chosen repository. Every change is sent at most once, and none
// of the four ever answers with a file's content: only its path, its blob SHA, and the commit GitHub made.

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
	Description: "Create one new branch of a repository a connection allows from a branch, a tag, " +
		"or a commit SHA, or, when from is left out, the repository's default branch; a branch name already " +
		"taken is refused with a clear message instead of a second attempt",
	Tags:         []string{"github", "branches", "create"},
	Risk:         changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:     Provider,
	InputSchema:  inputSchema(`"name":`+refSchema+`,"from":`+refSchema, "name"),
	OutputSchema: json.RawMessage(branchCreatedOutput),
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
		"repository a connection allows; content is written as UTF-8 text and never below " +
		".github/workflows/, which the workflow file tools maintain instead; not idempotent, since a repeated " +
		"call without the file's new sha is refused",
	Tags:     []string{"github", "contents", "put"},
	Risk:     changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
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
	Description: "Delete one file of a repository a connection allows, with its current blob sha; " +
		"never below .github/workflows/, which has no delete tool of its own; offered only where a connection's " +
		"tools list names it, because deleting a file the wrong branch relied on cannot be undone",
	Tags:                  []string{"github", "contents", "delete"},
	Risk:                  guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, dataSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
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

// Bounds of github.files.push.
const (
	// maxFilesPushCount bounds how many files one push may write in a single commit: enough for a real change
	// set, small enough that a mistaken call cannot script a large tree rewrite through this tool.
	maxFilesPushCount = 100
	// maxFilesPushBytes bounds the combined content of every file of one push; maxContentsPutBytes already
	// bounds each file on its own, so this is the binding limit for a call with many files.
	maxFilesPushBytes = 4 << 20
)

const filesPushFileSchema = `{"type":"object","properties":{"path":` + contentsPathSchema + `,"content":` +
	workflowTextSchema + `},"required":["path","content"],"additionalProperties":false}`

const filesPushFilesSchema = `{"type":"array","minItems":1,"maxItems":100,"items":` + filesPushFileSchema + `}`

const filesPushOutput = `{"type":"object","properties":{"branch":{"type":"string"},"commit_sha":{"type":"string"},` +
	`"commit_url":{"type":"string"},"parent_sha":{"type":"string"},"paths":{"type":"array","items":` +
	`{"type":"string"}}},"required":["branch","commit_sha","parent_sha","paths"],"additionalProperties":false}`

var filesPush = capability.Descriptor{
	ID:      Provider + ".files.push",
	Version: 1,
	Title:   "Push several GitHub repository files as one commit",
	Description: "Write several files of a repository a connection allows as one commit on an " +
		"existing branch, through the Git Data API: reads the branch's current head and tree, creates a blob " +
		"per file and one new tree, creates one commit with the read head as its only parent, and moves the " +
		"branch to it with a fast-forward-only update, refused with nothing written when the branch moved " +
		"since the read; content is written as UTF-8 text and never below .github/workflows/, which the " +
		"workflow file tools maintain instead; offered only where a connection's tools list names it, since " +
		"several files land in one commit that cannot be undone by halves",
	Tags:                  []string{"github", "files", "contents", "push", "commit"},
	Risk:                  guardedRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, dataSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: inputSchema(`"branch":`+refSchema+`,"message":`+commitMessageSchema+`,"files":`+
		filesPushFilesSchema+`,"expected_head_sha":`+blobSHASchema, "branch", "message", "files"),
	OutputSchema: json.RawMessage(filesPushOutput),
	Arguments: []capability.Argument{
		{Name: "branch", Description: "Branch to commit to; refused with nothing written if it moved since " +
			"this call read it", Required: true},
		{Name: "message", Description: "Commit message, at most 1000 characters", Required: true},
		{Name: "files", Description: "Files to write, 1 to 100, each with path (never below " +
			".github/workflows/, no path repeated) and content as UTF-8 text", Required: true},
		{Name: "expected_head_sha", Description: "Commit SHA the branch is expected to be at; when given and " +
			"the branch is at another commit, the call is refused before any blob is created"},
	},
	Fields: []capability.Field{
		{Name: "branch", Description: "Branch the commit was written to"},
		{Name: "commit_sha", Description: "Commit GitHub created for the change"},
		{Name: "commit_url", Description: "URL of the commit GitHub created"},
		{Name: "parent_sha", Description: "Commit SHA the branch was at before this push, the new commit's only parent"},
		{Name: "paths", Description: "Paths written, in the order given"},
	},
	Examples: []capability.Example{{
		Description: "Write two files in one commit",
		Arguments: json.RawMessage(`{"branch":"main","message":"docs: add two pages",` +
			`"files":[{"path":"docs/a.md","content":"# A\n"},{"path":"docs/b.md","content":"# B\n"}]}`),
	}},
}

// filesPushArguments are the arguments of github.files.push.
type filesPushArguments struct {
	Branch          string          `json:"branch"`
	Message         string          `json:"message"`
	Files           []filePushEntry `json:"files"`
	ExpectedHeadSHA string          `json:"expected_head_sha"`
}

// filePushEntry is one file of a github.files.push call.
type filePushEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// checkFilesPushArguments validates every argument before a credential is resolved, so an unusable path, a
// duplicate, an oversized file, or a total past maxFilesPushBytes never reaches GitHub.
func checkFilesPushArguments(a *filesPushArguments) error {
	if !validRef(a.Branch) {
		return invalidRequest("branch must be a branch name")
	}
	if strings.TrimSpace(a.Message) == "" || utf8.RuneCountInString(a.Message) > maxCommitMessage {
		return invalidRequest(fmt.Sprintf("message must hold between 1 and %d characters", maxCommitMessage))
	}
	if err := checkText("message", a.Message); err != nil {
		return err
	}
	if len(a.Files) == 0 || len(a.Files) > maxFilesPushCount {
		return invalidRequest(fmt.Sprintf("files must hold between 1 and %d files", maxFilesPushCount))
	}
	if a.ExpectedHeadSHA != "" && !validCommitSHA(a.ExpectedHeadSHA) {
		return invalidRequest("expected_head_sha must be a full commit SHA")
	}
	seen := map[string]bool{}
	total := 0
	for _, file := range a.Files {
		if err := checkContentsPathArgument(file.Path); err != nil {
			return err
		}
		if isWorkflowPath(file.Path) {
			return invalidRequest(workflowPutMessage)
		}
		if seen[file.Path] {
			return invalidRequest("files must not repeat a path: " + file.Path)
		}
		seen[file.Path] = true
		if file.Content == "" || len(file.Content) > maxContentsPutBytes {
			return invalidRequest(fmt.Sprintf("each file's content must hold between 1 byte and %d KiB",
				maxContentsPutBytes>>10))
		}
		if err := checkText("content", file.Content); err != nil {
			return err
		}
		total += len(file.Content)
	}
	if total > maxFilesPushBytes {
		return invalidRequest(fmt.Sprintf("the combined content of every file must hold at most %d KiB",
			maxFilesPushBytes>>10))
	}
	return nil
}

func invokeFilesPush(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	var arguments filesPushArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(filesPush.ID)
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkFilesPushArguments(&arguments); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.pushFiles(ctx, &arguments))
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
		{Descriptor: filesPush, Handler: capability.Handler(invokeFilesPush)},
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

// FilesPushed describes the commit github.files.push wrote, without any file's content.
type FilesPushed struct {
	Branch    string   `json:"branch"`
	CommitSHA string   `json:"commit_sha"`
	CommitURL string   `json:"commit_url,omitempty"`
	ParentSHA string   `json:"parent_sha"`
	Paths     []string `json:"paths"`
}

// gitRefHeadPath is the Git refs route of one branch, read and moved by github.files.push, escaped as one
// opaque path segment the way getTree escapes a ref: a slash inside the branch name becomes %2F rather than
// a further path segment, so it can never address another route.
func (c *Client) gitRefHeadPath(branch string) string {
	return c.repoPath("git/refs/heads/" + url.PathEscape(branch))
}

// branchHead reads the commit SHA a branch currently points at, through the Git refs API, so github.
// files.push always builds its commit on the branch's real, current head.
func (c *Client) branchHead(ctx context.Context, branch string) (string, error) {
	const op = "push repository files"
	var raw struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := c.rest(ctx, op, c.gitRefHeadPath(branch), &raw); err != nil {
		return "", actionsFailure(err, contentsChangePermission)
	}
	if raw.Ref != "refs/heads/"+branch || raw.Object.Type != "commit" || !validCommitSHA(raw.Object.SHA) {
		return "", invalidEntry(op, "a branch")
	}
	return raw.Object.SHA, nil
}

// commitTreeSHA reads the tree SHA of one commit, through the Git commits API, so a new tree can be built
// above it with base_tree instead of repeating every entry the commit already has.
func (c *Client) commitTreeSHA(ctx context.Context, commit string) (string, error) {
	const op = "push repository files"
	var raw struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.rest(ctx, op, c.repoPath("git/commits/"+url.PathEscape(commit)), &raw); err != nil {
		return "", actionsFailure(err, contentsChangePermission)
	}
	if !validBlobSHA(raw.Tree.SHA) {
		return "", invalidEntry(op, "a commit")
	}
	return raw.Tree.SHA, nil
}

// createBlob creates one Git blob of a file's UTF-8 content, sent as utf-8 straight from the argument
// instead of base64, since github.files.push admits only text.
func (c *Client) createBlob(ctx context.Context, content string) (string, error) {
	const op = "push repository files"
	var raw struct {
		SHA string `json:"sha"`
	}
	if err := c.restChange(ctx, op, http.MethodPost, c.repoPath("git/blobs"),
		map[string]any{"content": content, "encoding": "utf-8"}, &raw); err != nil {
		return "", err
	}
	if !validBlobSHA(raw.SHA) {
		return "", invalidResponse(op, true)
	}
	return raw.SHA, nil
}

// createTree creates one new Git tree above baseTree with entries added or replaced, without repeating
// every entry the base tree already carries.
func (c *Client) createTree(ctx context.Context, baseTree string, entries []map[string]any) (string, error) {
	const op = "push repository files"
	var raw struct {
		SHA string `json:"sha"`
	}
	if err := c.restChange(ctx, op, http.MethodPost, c.repoPath("git/trees"),
		map[string]any{"base_tree": baseTree, "tree": entries}, &raw); err != nil {
		return "", err
	}
	if !validBlobSHA(raw.SHA) {
		return "", invalidResponse(op, true)
	}
	return raw.SHA, nil
}

// createCommit creates one new Git commit above tree with exactly one parent: the branch's head github.
// files.push read, so the commit it moves the branch to is always built on the branch's own history.
func (c *Client) createCommit(ctx context.Context, message, tree, parent string) (string, string, error) {
	const op = "push repository files"
	var raw struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.restChange(ctx, op, http.MethodPost, c.repoPath("git/commits"),
		map[string]any{"message": message, "tree": tree, "parents": []string{parent}}, &raw); err != nil {
		return "", "", err
	}
	if !validCommitSHA(raw.SHA) {
		return "", "", invalidResponse(op, true)
	}
	return raw.SHA, raw.HTMLURL, nil
}

// noBranchEffect names what a refused blob, tree, or commit creation means for github.files.push: only the
// final ref update ever moves the branch, so a refusal at an earlier step leaves it exactly as it was; a
// blob, a tree, or a commit the request already created may still exist as a loose Git object, but it has no
// effect on anything until a later commit and ref update reference it, so nothing needs to be undone by hand.
func (c *Client) noBranchEffect(err error, branch string) error {
	var failure *provider.Error
	if !errors.As(err, &failure) {
		return err
	}
	refused := *failure
	if failure.Class == provider.ClassPermission {
		refused.Message = contentsChangePermission
		return &refused
	}
	refused.Message = strings.TrimSuffix(refused.Message, uncertain) + "; branch " + branch + " is unchanged: " +
		"a blob, a tree, or a commit this request already created may still exist as a loose Git object with " +
		"no effect until referenced, so nothing needs to be undone"
	return &refused
}

// fastForwardRef moves branch to sha with force set to false, so GitHub refuses the update, instead of
// rewriting history, the moment the branch is no longer at the head this call built the commit from. The
// request is sent directly, instead of through restChange, so a 422 answer can be read once and, when it
// names a non-fast-forward update, turned into an invalid request that names the branch's current head, read
// again on a best-effort basis, instead of the generic rejected message; the request is still sent exactly
// once, and every other status is classified exactly like restChange would, with the branch named as
// unaffected wherever GitHub gave a definite answer rather than one a timeout or a reset leaves open.
func (c *Client) fastForwardRef(ctx context.Context, branch, sha string) error {
	const op = "push repository files"
	payload, err := json.Marshal(map[string]any{"sha": sha, "force": false})
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "GitHub", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.endpoints.rest+c.gitRefHeadPath(branch),
		bytes.NewReader(payload))
	if err != nil {
		return providerError(op, "the request could not be built")
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
		return failure
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusUnprocessableEntity &&
			bytes.Contains(bytes.ToLower(data), []byte("fast forward")) {
			message := "nothing was written to branch " + branch + ": GitHub refused the update because " + sha +
				" is not a fast-forward of its current head"
			if current, err := c.branchHead(ctx, branch); err == nil {
				message += "; branch " + branch + " is now at " + current + "; read it again and apply the " +
					"change to its current head"
			} else {
				message += "; read the branch again before retrying"
			}
			return invalidRequest(message)
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		failure := c.statusError(op, response, true)
		if response.StatusCode >= 500 {
			// A server error leaves the outcome open; statusError already marked it uncertain.
			return failure
		}
		return c.noBranchEffect(failure, branch)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "the GitHub response could not be read within the size limit" + uncertain}
	}
	var answer struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return invalidResponse(op, true)
	}
	if answer.Ref != "refs/heads/"+branch || answer.Object.SHA != sha {
		return invalidResponse(op, true)
	}
	return nil
}

// pushFiles writes every file of a as one commit on the bound repository's branch: it reads the branch's
// current head and tree, creates one blob per file and one new tree above it, creates one commit from that
// tree with the read head as its only parent, and moves the branch to it, fast-forward only. expected_head_sha,
// when given, is checked against the head as soon as it is read, before any blob is created, so a caller that
// already knows the branch moved fails cheaply instead of creating objects that would only be discarded; the
// final ref update still guards against a branch that moves after that check, whether or not it was given.
func (c *Client) pushFiles(ctx context.Context, a *filesPushArguments) (*FilesPushed, error) {
	head, err := c.branchHead(ctx, a.Branch)
	if err != nil {
		return nil, err
	}
	if a.ExpectedHeadSHA != "" && a.ExpectedHeadSHA != head {
		return nil, invalidRequest("branch " + a.Branch + " is at " + head + ", not the given expected_head_sha " +
			a.ExpectedHeadSHA + "; read it again and apply the change to its current head")
	}
	baseTree, err := c.commitTreeSHA(ctx, head)
	if err != nil {
		return nil, err
	}
	entries := make([]map[string]any, 0, len(a.Files))
	paths := make([]string, 0, len(a.Files))
	for _, file := range a.Files {
		blob, err := c.createBlob(ctx, file.Content)
		if err != nil {
			return nil, c.noBranchEffect(err, a.Branch)
		}
		entries = append(entries, map[string]any{"path": file.Path, "mode": "100644", "type": "blob", "sha": blob})
		paths = append(paths, file.Path)
	}
	tree, err := c.createTree(ctx, baseTree, entries)
	if err != nil {
		return nil, c.noBranchEffect(err, a.Branch)
	}
	commitSHA, commitURL, err := c.createCommit(ctx, a.Message, tree, head)
	if err != nil {
		return nil, c.noBranchEffect(err, a.Branch)
	}
	if err := c.fastForwardRef(ctx, a.Branch, commitSHA); err != nil {
		return nil, err
	}
	return &FilesPushed{Branch: a.Branch, CommitSHA: commitSHA, CommitURL: commitURL, ParentSHA: head,
		Paths: paths}, nil
}
