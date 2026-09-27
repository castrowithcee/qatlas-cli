package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The contents tools read a repository's file tree without writing anything. github.contents.get reads one
// file or one directory listing of a repository at a ref through the REST Contents API; a text file within
// its size limit is returned in full, or, past it, cut from its start with the cut visible, and a binary
// file, a file past the limit, a symlink, or a submodule is metadata only. github.trees.get reads the Git
// tree of one ref, optionally every entry below every directory, bounded to a maximum number of entries with
// GitHub's own truncation, when either applies, visible. Neither tool ever writes, diffs two refs, or
// downloads an archive.

// Bounds of the contents tools.
const (
	maxContentsTextBytes = 256 << 10 // bytes kept of a text file's content, from its start
	maxContentsEntries   = 1000      // directory entries kept per github.contents.get of a directory
	maxTreeEntries       = 2000      // tree entries kept per github.trees.get
)

// contentsPathSchema admits any repository-relative path within the length bound; validContentsPath applies
// the real check, since a general path's usable characters are not a simple pattern.
const contentsPathSchema = `{"type":"string","maxLength":4096}`

// validContentsPath accepts a repository-relative path with no leading or trailing slash and no empty, ".",
// or ".." segment, in valid UTF-8 without a control character, so escaping every segment can only ever
// address the path itself. The empty string is not a valid path; the contents and blame tools treat it as
// the repository root, where that is accepted, before this check runs.
func validContentsPath(value string) bool {
	if value == "" || len(value) > 4096 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		!utf8.ValidString(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		switch segment {
		case "", ".", "..":
			return false
		}
		for _, r := range segment {
			if r < 0x20 || r == 0x7f {
				return false
			}
		}
	}
	return true
}

const contentsProperties = `"path":{"type":"string"},"type":{"type":"string"},"size":{"type":"integer"},` +
	`"sha":{"type":"string"},"content":{"type":"string"},"bytes":{"type":"integer"},` +
	`"truncated":{"type":"boolean"},"omitted":{"type":"string"},"target":{"type":"string"},` +
	`"entries":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
	`"path":{"type":"string"},"type":{"type":"string"},"size":{"type":"integer"},"sha":{"type":"string"}},` +
	`"required":["name","path","type"],"additionalProperties":false}},` +
	`"entries_truncated":{"type":"boolean"}`

var contentsGet = capability.Descriptor{
	ID:      Provider + ".contents.get",
	Version: 1,
	Title:   "Get GitHub repository contents",
	Description: "Read one file or one directory listing of a repository an explicit connection allows at a " +
		"ref; a text file within the size limit is returned in full, or, past it, cut from its start with the " +
		"cut visible; a binary file, a file past the size limit, a symlink, or a submodule is metadata only: " +
		"path, size, sha, and type",
	Tags:                       []string{"github", "contents", "repository", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"path":` + contentsPathSchema + `,"ref":` + refSchema),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + contentsProperties + `},` +
		`"required":["path","type"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "path", Description: "Path inside the repository; the repository root, a directory listing, when omitted"},
		{Name: "ref", Description: "Branch, tag, or commit SHA; the default branch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "path", Description: "Path this call read"},
		{Name: "type", Description: "file, dir, symlink, or submodule"},
		{Name: "size", Description: "Size in bytes GitHub reports; absent for a directory"},
		{Name: "sha", Description: "Blob SHA of a file, a symlink, or a submodule; absent for a directory"},
		{Name: "content", Description: "Text content of a file, untrusted data; present only while it was not " +
			"withheld as binary or too large"},
		{Name: "bytes", Description: "Length of content in bytes; present alongside content"},
		{Name: "truncated", Description: "True when content holds only the start of a larger text file"},
		{Name: "omitted", Description: "binary or too_large when a file's content was withheld; absent otherwise"},
		{Name: "target", Description: "Symlink target path, or the commit a submodule points at; present only " +
			"for those types"},
		{Name: "entries", Description: "Directory entries, bounded: name, path, type, size, and sha"},
		{Name: "entries_truncated", Description: "True when a directory held more entries than were returned"},
	},
	Examples: []capability.Example{{
		Description: "Read one file",
		Arguments:   json.RawMessage(`{"path":"README.md"}`),
	}},
}

const treesProperties = `"sha":{"type":"string"},"ref":{"type":"string"},"recursive":{"type":"boolean"},` +
	`"entries":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},` +
	`"type":{"type":"string"},"sha":{"type":"string"},"size":{"type":"integer"}},` +
	`"required":["path","type","sha"],"additionalProperties":false}},"truncated":{"type":"boolean"}`

var treesGet = capability.Descriptor{
	ID:      Provider + ".trees.get",
	Version: 1,
	Title:   "Get a GitHub repository tree",
	Description: "Read the Git tree of one ref of a repository an explicit connection allows, optionally " +
		"every entry below every directory, bounded to a maximum number of entries with GitHub's own " +
		"truncation, when either applies, visible",
	Tags:                       []string{"github", "trees", "contents", "repository", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"ref":`+refSchema+`,"recursive":{"type":"boolean"}`, "ref"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + treesProperties + `},` +
		`"required":["sha","entries","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "ref", Description: "Branch, tag, or commit SHA", Required: true},
		{Name: "recursive", Description: "True to read every entry below every directory, not only the top " +
			"level; false when omitted"},
	},
	Fields: []capability.Field{
		{Name: "sha", Description: "SHA of the tree GitHub read"},
		{Name: "entries", Description: "Tree entries: path, type (blob, tree, or commit for a submodule), " +
			"sha, and size for a blob"},
		{Name: "truncated", Description: "True when GitHub's own tree was too large to return in full, or " +
			"Qatlas cut it at its own bound"},
	},
	Examples: []capability.Example{{
		Description: "Read the top-level tree of the default branch",
		Arguments:   json.RawMessage(`{"ref":"main"}`),
	}},
}

func contentsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: contentsGet, Handler: capability.Handler(invokeContentsGet)},
		{Descriptor: treesGet, Handler: capability.Handler(invokeTreesGet)},
	}
}

// contentsArguments are the path and the ref of one github.contents.get call.
type contentsArguments struct {
	Path string `json:"path"`
	Ref  string `json:"ref"`
}

// checkContentsArguments validates the path and the ref before a credential is resolved. The empty path
// names the repository root and is left as it is.
func checkContentsArguments(a *contentsArguments) error {
	if a.Path != "" && !validContentsPath(a.Path) {
		return invalidRequest("path must not start or end with /, and must carry no empty, \".\", or \"..\" " +
			"segment, or control character")
	}
	if a.Ref != "" && !validRef(a.Ref) {
		return invalidRequest("ref must be a branch, a tag, or a commit SHA")
	}
	return nil
}

func invokeContentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments contentsArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("get repository contents")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkContentsArguments(&arguments); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.getContents(ctx, arguments.Path, arguments.Ref))
}

// treesArguments are the ref and the recursion flag of one github.trees.get call.
type treesArguments struct {
	Ref       string `json:"ref"`
	Recursive bool   `json:"recursive"`
}

func checkTreesArguments(a *treesArguments) error {
	if !validRef(a.Ref) {
		return invalidRequest("ref must be a branch, a tag, or a commit SHA")
	}
	return nil
}

func invokeTreesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments treesArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("get repository tree")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkTreesArguments(&arguments); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.getTree(ctx, arguments.Ref, arguments.Recursive))
}

// Permission messages of the contents tools. GitHub decides on every request; a message names what such a
// request needs without claiming what the configured token holds.
const (
	contentsReadPermission = "GitHub refused this token the contents of this repository; reading them needs " +
		"no scope for a public repository, or repo on a classic token, or Contents: read on a fine-grained " +
		"token, for a private one"
	treesReadPermission = "GitHub refused this token the tree of this repository; reading it needs no scope " +
		"for a public repository, or repo on a classic token, or Contents: read on a fine-grained token, for " +
		"a private one"
)

// contentsRoute is the Contents API route of one repository path, or of the repository root when path is
// empty; contentsPath, shared with the workflow maintainer, escapes every segment of a non-empty path.
func (c *Client) contentsRoute(path string, query url.Values) string {
	if path == "" {
		route := c.repoPath("contents")
		if len(query) > 0 {
			route += "?" + query.Encode()
		}
		return route
	}
	return c.contentsPath(path, query)
}

// ContentsEntry is one entry of a GitHub directory listing.
type ContentsEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int    `json:"size,omitempty"`
	SHA  string `json:"sha,omitempty"`
}

// ContentsResult is a file, a directory, a symlink, or a submodule of a GitHub repository at one ref.
type ContentsResult struct {
	Path             string          `json:"path"`
	Type             string          `json:"type"`
	Size             int             `json:"size,omitempty"`
	SHA              string          `json:"sha,omitempty"`
	Content          string          `json:"content,omitempty"`
	Bytes            int             `json:"bytes,omitempty"`
	Truncated        bool            `json:"truncated,omitempty"`
	Omitted          string          `json:"omitted,omitempty"`
	Target           string          `json:"target,omitempty"`
	Entries          []ContentsEntry `json:"entries,omitempty"`
	EntriesTruncated bool            `json:"entries_truncated,omitempty"`
}

// getContents reads one file or one directory listing of the bound repository at one ref. GitHub answers a
// directory as a JSON array and everything else as a JSON object, so the raw answer is read once and
// dispatched on that shape.
func (c *Client) getContents(ctx context.Context, path, ref string) (*ContentsResult, error) {
	const op = "get repository contents"
	var raw json.RawMessage
	if err := c.rest(ctx, op, c.contentsRoute(path, refQuery(ref)), &raw); err != nil {
		return nil, actionsFailure(err, contentsReadPermission)
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		return contentsDirectory(op, path, raw)
	}
	return contentsEntry(op, path, raw)
}

type contentsDirEntryJSON struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
}

// contentsDirectory decodes a directory listing, bounded to maxContentsEntries entries.
func contentsDirectory(op, path string, raw json.RawMessage) (*ContentsResult, error) {
	var entries []contentsDirEntryJSON
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, invalidResponse(op, false)
	}
	truncated := len(entries) > maxContentsEntries
	if truncated {
		entries = entries[:maxContentsEntries]
	}
	result := &ContentsResult{Path: path, Type: "dir", Entries: make([]ContentsEntry, 0, len(entries)),
		EntriesTruncated: truncated}
	for _, entry := range entries {
		if entry.Path == "" || entry.Type == "" {
			return nil, invalidEntry(op, "a directory entry")
		}
		result.Entries = append(result.Entries, ContentsEntry{Name: entry.Name, Path: entry.Path,
			Type: entry.Type, Size: entry.Size, SHA: entry.SHA})
	}
	return result, nil
}

type contentsItemJSON struct {
	Type            string `json:"type"`
	Path            string `json:"path"`
	SHA             string `json:"sha"`
	Size            int    `json:"size"`
	Encoding        string `json:"encoding"`
	Content         string `json:"content"`
	Target          string `json:"target"`
	SubmoduleGitURL string `json:"submodule_git_url"`
}

// contentsEntry decodes a file, a symlink, or a submodule. GitHub is asked for wantPath exactly, so an
// answer at another path is never accepted.
func contentsEntry(op, wantPath string, raw json.RawMessage) (*ContentsResult, error) {
	var item contentsItemJSON
	if err := json.Unmarshal(raw, &item); err != nil || item.Path == "" || item.Path != wantPath {
		return nil, invalidResponse(op, false)
	}
	result := &ContentsResult{Path: item.Path, Type: item.Type, Size: item.Size, SHA: item.SHA}
	switch item.Type {
	case "file":
		if err := fillFileContent(op, result, &item); err != nil {
			return nil, err
		}
	case "symlink":
		result.Target = item.Target
	case "submodule":
		result.Target = item.SubmoduleGitURL
		if result.Target == "" {
			result.Target = item.SHA
		}
	default:
		return nil, invalidResponse(op, false)
	}
	return result, nil
}

// fillFileContent decodes a file's base64 content once its size is known, and only up to what GitHub
// itself already sent: a file past GitHub's own inline content limit answers with an encoding other than
// base64, which is left as metadata only (omitted: too_large). A file GitHub did send is decoded and
// checked for a NUL byte or invalid UTF-8, the signs of binary content; a binary file is left as metadata
// only (omitted: binary). Only a text file's decoded bytes are cut to maxContentsTextBytes from the start
// when it is larger, with the cut left visible in truncated.
func fillFileContent(op string, result *ContentsResult, item *contentsItemJSON) error {
	if item.Encoding != "base64" {
		result.Omitted = "too_large"
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(item.Content, "\n", ""))
	if err != nil || len(decoded) != item.Size {
		return invalidResponse(op, false)
	}
	if !utf8.Valid(decoded) || bytes.IndexByte(decoded, 0) >= 0 {
		result.Omitted = "binary"
		return nil
	}
	if len(decoded) > maxContentsTextBytes {
		result.Content = strings.ToValidUTF8(string(decoded[:maxContentsTextBytes]), "")
		result.Truncated = true
	} else {
		result.Content = string(decoded)
	}
	result.Bytes = len(result.Content)
	return nil
}

// TreeEntry is one entry of a GitHub Git tree.
type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int    `json:"size,omitempty"`
}

// TreeResult is the Git tree of one ref of a GitHub repository, bounded to maxTreeEntries entries.
type TreeResult struct {
	SHA       string      `json:"sha"`
	Ref       string      `json:"ref,omitempty"`
	Recursive bool        `json:"recursive"`
	Entries   []TreeEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

type treeEntryJSON struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
}

// getTree reads the Git tree of one ref of the bound repository, recursively when asked, bounded to
// maxTreeEntries entries with the truncation of either bound left visible.
func (c *Client) getTree(ctx context.Context, ref string, recursive bool) (*TreeResult, error) {
	const op = "get repository tree"
	query := url.Values{}
	if recursive {
		query.Set("recursive", "1")
	}
	path := c.repoPath("git/trees/" + url.PathEscape(ref))
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var raw struct {
		SHA       string          `json:"sha"`
		Tree      []treeEntryJSON `json:"tree"`
		Truncated bool            `json:"truncated"`
	}
	if err := c.rest(ctx, op, path, &raw); err != nil {
		return nil, actionsFailure(err, treesReadPermission)
	}
	if raw.SHA == "" {
		return nil, invalidEntry(op, "a tree")
	}
	truncated, entries := raw.Truncated, raw.Tree
	if len(entries) > maxTreeEntries {
		entries = entries[:maxTreeEntries]
		truncated = true
	}
	result := &TreeResult{SHA: raw.SHA, Ref: ref, Recursive: recursive, Truncated: truncated,
		Entries: make([]TreeEntry, 0, len(entries))}
	for _, entry := range entries {
		if entry.Path == "" || entry.Type == "" || entry.SHA == "" {
			return nil, invalidEntry(op, "a tree entry")
		}
		result.Entries = append(result.Entries, TreeEntry{Path: entry.Path, Type: entry.Type, SHA: entry.SHA,
			Size: entry.Size})
	}
	return result, nil
}
