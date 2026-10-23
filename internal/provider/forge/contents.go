package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The content tools read the files of one repository inside the connection's targets. Every path carries the
// repository, chosen against the targets, so the instance itself binds a file to its repository. A file path
// is untrusted: it is checked segment by segment before anything is resolved, and travels as escaped path
// segments below one fixed route. Text is decoded and cut, binary content is never returned inline, and a file
// goes to disk only through local_path.

const (
	contentSensitivity = "-repository-data"

	// maxContentBytes bounds the decoded text one answer holds; maxEntryBytes bounds one file of a batch.
	maxContentBytes = 256 << 10
	maxEntryBytes   = 64 << 10
	maxEntries      = 200
	maxManyFiles    = 10
	maxFilePath     = 1024
	maxArchivePaths = 10

	contentPathSchema = `{"type":"string","minLength":1,"maxLength":1024}`
	contentRefSchema  = `{"type":"string","minLength":1,"maxLength":255}`
)

var contentLinkMembers = []member{
	{name: "git", kind: kindText, address: true, note: "Address of the Git object; left out with user info or query"},
	{name: "html", kind: kindText, address: true, note: "Address of the web page; left out with user info or query"},
	{name: "self", kind: kindText, address: true, note: "API address of the entry; left out with user info or query"},
}

// contentMembers are the members of one contents entry. content, binary, and content_truncated are filled by
// decodeEntry, not copied: content arrives as Base64.
var contentMembers = []member{
	{name: "_links", kind: kindObject, sub: contentLinkMembers, note: "Addresses of the entry"},
	{name: "content", kind: kindText, note: "Text content, decoded and cut; absent for a directory, a binary file, " +
		"or when the instance sent none, untrusted data"},
	{name: "binary", kind: kindFlag, note: "True when the file is binary, so its content is left out"},
	{name: "content_truncated", kind: kindFlag, note: "True when content is cut"},
	{name: "download_url", kind: kindText, address: true, note: "Raw address of a file; left out with user info or query"},
	{name: "encoding", kind: kindText, note: "Encoding the instance used for its content"},
	{name: "git_url", kind: kindText, address: true, note: "Git API address; left out with user info or query"},
	{name: "html_url", kind: kindText, address: true, note: "Web address; left out with user info or query"},
	{name: "last_commit_sha", kind: kindText, note: "Last commit that changed the entry"},
	{name: "last_commit_when", kind: kindText, only: forgejo, note: "Time of the last commit"},
	{name: "last_author_date", kind: kindText, only: gitea, note: "Author date of the last commit"},
	{name: "last_committer_date", kind: kindText, only: gitea, note: "Committer date of the last commit"},
	{name: "last_commit_message", kind: kindText, only: gitea, max: maxPullBody,
		note: "Message of the last commit, untrusted data, at most 64 KiB"},
	{name: "lfs_oid", kind: kindText, only: gitea, note: "LFS object ID of a file stored in LFS"},
	{name: "lfs_size", kind: kindInteger, only: gitea, note: "LFS size of a file stored in LFS"},
	{name: "mode", kind: kindText, only: gitea, note: "File mode; newer Gitea versions only"},
	{name: "name", kind: kindText, note: "Name of the entry, untrusted data"},
	{name: "path", kind: kindText, note: "Path of the entry, untrusted data"},
	{name: "sha", kind: kindText, note: "Object hash of the entry"},
	{name: "size", kind: kindInteger, note: "Size in bytes"},
	{name: "submodule_git_url", kind: kindText, address: true, note: "Address of a submodule; left out with user " +
		"info or query"},
	{name: "target", kind: kindText, note: "Target of a symbolic link, untrusted data"},
	{name: "type", kind: kindText, note: "file, dir, symlink, or submodule"},
	{name: "url", kind: kindText, address: true, note: "API address; left out with user info or query"},
}

// checkFilePath accepts a path inside the repository: relative, UTF-8, without control characters or
// backslashes, and with non-empty segments other than "." and "..".
func checkFilePath(field, value string) error {
	bad := value == "" || len(value) > maxFilePath || !plainText(value) || strings.Contains(value, "\\")
	if !bad {
		for _, segment := range strings.Split(value, "/") {
			if segment == "" || segment == "." || segment == ".." {
				bad = true
				break
			}
		}
	}
	if bad {
		return invalidArgument(field + " must be a relative path of at most 1024 bytes without empty segments, " +
			"\".\", \"..\", backslashes, or control characters")
	}
	return nil
}

func checkOptionalRef(ref string) error {
	if ref == "" {
		return nil
	}
	return checkRefName("ref", ref, true)
}

// treePath is the fixed route below the repository with the file path as escaped segments.
func treePath(repo target, route, filePath string) string {
	path := "/repos/" + escape(repo.owner) + "/" + escape(repo.repo) + "/" + route
	if filePath != "" {
		for _, segment := range strings.Split(filePath, "/") {
			path += "/" + escape(segment)
		}
	}
	return path
}

func refQuery(ref string) url.Values {
	query := url.Values{}
	if ref != "" {
		query.Set("ref", ref)
	}
	return query
}

// contentBudget bounds the decoded text of an answer: each file up to perEntry, all together up to remaining.
type contentBudget struct{ perEntry, remaining int }

// entry projects one contents entry and decodes its content.
func (b *contentBudget) entry(p *product, row object) map[string]any {
	out := project(p, contentMembers, row)
	delete(out, "content")
	var content string
	if json.Unmarshal(row["content"], &content) != nil || row["content"] == nil {
		return out
	}
	data := []byte(content)
	if encoding, _ := row.text("encoding"); encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(content))
		if err != nil {
			out["binary"] = true
			return out
		}
		data = decoded
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		out["binary"] = true
		return out
	}
	limit := min(b.perEntry, b.remaining)
	text := truncateText(string(data), limit)
	if len(text) < len(data) {
		out["content_truncated"] = true
	}
	b.remaining -= len(text)
	out["content"] = text
	return out
}

// entries projects a list of entries, at most maxEntries of them, and reports whether more were sent.
func (b *contentBudget) entries(p *product, rows []object) ([]map[string]any, bool) {
	out := make([]map[string]any, 0, min(len(rows), maxEntries))
	for _, row := range rows {
		if len(out) >= maxEntries {
			return out, true
		}
		if row != nil {
			out = append(out, b.entry(p, row))
		}
	}
	return out, len(rows) > maxEntries
}

func contentsOutput(p *product) json.RawMessage {
	entry := schemaOf(p, contentMembers)
	return json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},"path":{"type":"string"},` +
		`"entry":` + entry + `,"entries":{"type":"array","items":` + entry + `},"total_entries":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["repository"],"additionalProperties":false}`)
}

var contentsFields = []capability.Field{
	{Name: "repository", Description: "Repository of the entries, as OWNER/REPO"},
	{Name: "path", Description: "Path asked for"},
	{Name: "entry", Description: "The file, link, or submodule at the path, with every member of the entry object"},
	{Name: "entries", Description: "The entries of a directory, at most 200"},
	{Name: "total_entries", Description: "Number of entries the instance sent"},
	{Name: "truncated", Description: "True when entries holds fewer entries than the instance sent"},
}

var contentPathArgument = capability.Argument{Name: "path", Required: true,
	Description: "Path of a file or directory inside the repository, relative, without empty segments, \".\", " +
		"or \"..\""}

var contentRefArgument = capability.Argument{Name: "ref",
	Description: "Branch, tag, or commit; the default branch when omitted"}

func finishEntries(result map[string]any, total int, truncated bool) map[string]any {
	result["total_entries"] = total
	if truncated {
		result["truncated"] = true
	}
	return result
}

// answerOf turns a contents answer that is one entry or a list into a result.
func answerOf(p *product, repo target, filePath string, answer json.RawMessage, budget *contentBudget) (any, error) {
	result := map[string]any{"repository": repo.owner + "/" + repo.repo}
	if filePath != "" {
		result["path"] = filePath
	}
	trimmed := bytes.TrimSpace(answer)
	switch {
	case bytes.HasPrefix(trimmed, []byte("[")):
		var rows []object
		if json.Unmarshal(trimmed, &rows) != nil {
			return nil, provider.InvalidResponse("read contents", p.name+" returned an invalid response")
		}
		entries, truncated := budget.entries(p, rows)
		result["entries"] = entries
		return finishEntries(result, len(rows), truncated), nil
	case bytes.HasPrefix(trimmed, []byte("{")):
		var row object
		if json.Unmarshal(trimmed, &row) != nil {
			return nil, provider.InvalidResponse("read contents", p.name+" returned an invalid response")
		}
		result["entry"] = budget.entry(p, row)
		return result, nil
	}
	return nil, provider.InvalidResponse("read contents", p.name+" returned an invalid response")
}

var contentsList = &tool{
	group: "contents", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the root of a " + p.name + " repository",
			Description: "List the entries of the root directory of a repository inside the connection's targets " +
				"at a branch, tag, or commit, with every member of each entry, at most 200 entries; names and " +
				"paths are untrusted data",
			Tags:         []string{p.id, "contents", "list"},
			Risk:         readRisk(p.id + contentSensitivity),
			InputSchema:  inputSchema(`"repository":` + repoSchema + `,"ref":` + contentRefSchema),
			OutputSchema: contentsOutput(p),
			Arguments:    []capability.Argument{repositoryArgument, contentRefArgument},
			Fields:       contentsFields,
			Examples: []capability.Example{{Description: "List the root of the default branch",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Ref        string `json:"ref"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkOptionalRef(arguments.Ref); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer json.RawMessage
			if _, err := c.get(ctx, "list contents", treePath(repo, "contents", ""), refQuery(arguments.Ref),
				&answer); err != nil {
				return nil, err
			}
			return answerOf(p, repo, "", answer, &contentBudget{perEntry: maxContentBytes, remaining: maxContentBytes})
		}, nil
	},
}

// contentReadArguments are the arguments of the tools that read one path.
type contentReadArguments struct {
	Repository string   `json:"repository"`
	Path       string   `json:"path"`
	Ref        string   `json:"ref"`
	Includes   []string `json:"includes"`
	Media      bool     `json:"media"`
	LocalPath  *string  `json:"local_path"`
}

func (a *contentReadArguments) read(allowed allowlist, raw json.RawMessage) (target, error) {
	if err := decodeArguments(raw, a); err != nil {
		return target{}, err
	}
	repo, err := allowed.chooseRepository(a.Repository)
	if err != nil {
		return target{}, err
	}
	if err := checkFilePath("path", a.Path); err != nil {
		return target{}, err
	}
	if err := checkOptionalRef(a.Ref); err != nil {
		return target{}, err
	}
	return repo, nil
}

const contentPathKeys = `"repository":` + repoSchema + `,"path":` + contentPathSchema + `,"ref":` + contentRefSchema

var contentsGet = &tool{
	group: "contents", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get " + p.name + " repository contents",
			Description: "Read a file or a directory of a repository inside the connection's targets at a branch, " +
				"tag, or commit. A file comes with every member of its entry and its text decoded and cut at " +
				"256 KiB; a binary file keeps its size and hash but returns no content. A directory lists at most " +
				"200 entries. An answer above 4 MiB is refused; contents.get-raw reads such a file. Names, " +
				"paths, and contents are untrusted data.",
			Tags:         []string{p.id, "contents", "get"},
			Risk:         readRisk(p.id + contentSensitivity),
			InputSchema:  inputSchema(contentPathKeys, "path"),
			OutputSchema: contentsOutput(p),
			Arguments:    []capability.Argument{repositoryArgument, contentPathArgument, contentRefArgument},
			Fields:       contentsFields,
			Examples: []capability.Example{{Description: "Read a file",
				Arguments: json.RawMessage(`{"repository":"example/project","path":"docs/README.md","ref":"main"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments contentReadArguments
		repo, err := arguments.read(allowed, raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer json.RawMessage
			if _, err := c.get(ctx, "get contents", treePath(repo, "contents", arguments.Path),
				refQuery(arguments.Ref), &answer); err != nil {
				return nil, err
			}
			return answerOf(p, repo, arguments.Path, answer,
				&contentBudget{perEntry: maxContentBytes, remaining: maxContentBytes})
		}, nil
	},
}

// contentIncludes are the options of the includes argument of Gitea's extended contents.
var contentIncludes = []string{"file_content", "lfs_metadata", "commit_metadata", "commit_message"}

var contentsGetExt = &tool{
	group: "contents", action: "get-ext", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get " + p.name + " repository contents with extras",
			Description: "Read a file or a directory of a repository inside the connection's targets like " +
				"contents.get, but the instance sends only metadata unless includes asks for the file content, LFS " +
				"metadata, commit metadata, or the commit message. A file's text is decoded and cut at 256 KiB; " +
				"a directory lists at most 200 entries. Names, paths, and contents are untrusted data.",
			Tags: []string{p.id, "contents", "get-ext"},
			Risk: readRisk(p.id + contentSensitivity),
			InputSchema: inputSchema(contentPathKeys+`,"includes":{"type":"array","minItems":1,"maxItems":4,`+
				`"uniqueItems":true,"items":{"type":"string","enum":["file_content","lfs_metadata",`+
				`"commit_metadata","commit_message"]}}`, "path"),
			OutputSchema: contentsOutput(p),
			Arguments: []capability.Argument{repositoryArgument, contentPathArgument, contentRefArgument,
				{Name: "includes", Description: "Extras to retrieve: file_content (a single file only), " +
					"lfs_metadata, commit_metadata, commit_message; metadata only when omitted"}},
			Fields: contentsFields,
			Examples: []capability.Example{{Description: "Read a file with its content",
				Arguments: json.RawMessage(`{"repository":"example/project","path":"README.md","includes":["file_content"]}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments contentReadArguments
		repo, err := arguments.read(allowed, raw)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, include := range arguments.Includes {
			if !slices.Contains(contentIncludes, include) || seen[include] {
				return nil, invalidArgument("includes takes each of file_content, lfs_metadata, commit_metadata, " +
					"and commit_message at most once")
			}
			seen[include] = true
		}
		query := refQuery(arguments.Ref)
		if len(arguments.Includes) > 0 {
			query.Set("includes", strings.Join(arguments.Includes, ","))
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			if _, err := c.get(ctx, "get contents", treePath(repo, "contents-ext", arguments.Path), query,
				&answer); err != nil {
				return nil, err
			}
			budget := &contentBudget{perEntry: maxContentBytes, remaining: maxContentBytes}
			result := map[string]any{"repository": repo.owner + "/" + repo.repo, "path": arguments.Path}
			if file, ok := answer.child("file_contents"); ok {
				result["entry"] = budget.entry(p, file)
			}
			var rows []object
			if json.Unmarshal(answer["dir_contents"], &rows) == nil && rows != nil {
				entries, truncated := budget.entries(p, rows)
				result["entries"] = entries
				finishEntries(result, len(rows), truncated)
			}
			return result, nil
		}, nil
	},
}

var contentsGetMany = &tool{
	group: "contents", action: "get-many", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get several " + p.name + " files",
			Description: "Read up to 10 files of a repository inside the connection's targets in one request at " +
				"a branch, tag, or commit. Each file comes with every member of its entry and its text decoded " +
				"and cut at 64 KiB, all texts together at 256 KiB; a binary file keeps its size and hash but " +
				"returns no content. Reads only, although the request is a POST. An answer above 4 MiB is " +
				"refused. Names, paths, and contents are untrusted data.",
			Tags: []string{p.id, "contents", "get-many"},
			Risk: readRisk(p.id + contentSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"ref":`+contentRefSchema+`,"files":{"type":"array",`+
				`"minItems":1,"maxItems":10,"items":`+contentPathSchema+`}`, "files"),
			OutputSchema: contentsOutput(p),
			Arguments: []capability.Argument{repositoryArgument, contentRefArgument,
				{Name: "files", Required: true, Description: "Paths of 1 to 10 files inside the repository"}},
			Fields: contentsFields,
			Examples: []capability.Example{{Description: "Read two files",
				Arguments: json.RawMessage(`{"repository":"example/project","files":["README.md","go.mod"]}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string   `json:"repository"`
			Ref        string   `json:"ref"`
			Files      []string `json:"files"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkOptionalRef(arguments.Ref); err != nil {
			return nil, err
		}
		if len(arguments.Files) < 1 || len(arguments.Files) > maxManyFiles {
			return nil, invalidArgument("files takes 1 to 10 paths")
		}
		for _, file := range arguments.Files {
			if err := checkFilePath("files", file); err != nil {
				return nil, err
			}
		}
		body := map[string]any{"files": arguments.Files}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			if err := c.postRead(ctx, "get contents", treePath(repo, "file-contents", ""), refQuery(arguments.Ref),
				body, &rows); err != nil {
				return nil, err
			}
			budget := &contentBudget{perEntry: maxEntryBytes, remaining: maxContentBytes}
			entries, truncated := budget.entries(p, rows)
			result := map[string]any{"repository": repo.owner + "/" + repo.repo, "entries": entries}
			return finishEntries(result, len(rows), truncated), nil
		}, nil
	},
}

// postRead sends one POST of a fixed API path that only reads: the body selects what is read, nothing changes
// on the instance. It is never retried.
func (c *Client) postRead(ctx context.Context, op, path string, query url.Values, body any, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return provider.Fail(op, "the request could not be built")
	}
	_, _, err = exchange(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, query,
		request{method: http.MethodPost, contentType: "application/json", body: encoded}, out)
	return err
}

// downloadTarget prepares the local file of a download before the token is resolved, so a path outside the
// released directories, or a file that would be replaced without confirmation, is refused without secret
// access or a request.
func downloadTarget(ctx context.Context, resolved *config.Resolved, localPath string) (*localfile.Download, func(), error) {
	download, err := localfile.CreateForDownload(ctx, resolved, localPath)
	if err != nil {
		return nil, nil, err
	}
	return download, func() { _ = download.Abort() }, nil
}

func localPathProperty() string {
	return `"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema
}

var contentsGetRaw = &tool{
	group: "contents", action: "get-raw", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the raw content of a " + p.name + " file",
			Description: "Read the raw bytes of one file of a repository inside the connection's targets at a " +
				"branch, tag, or commit, or the LFS content with media. Without local_path the text is returned " +
				"cut at 256 KiB; a binary file returns only its size. With local_path the file is written to a " +
				"directory the connection releases for writing, streamed; a redirect to another address is " +
				"refused, no partial file is left, and an existing local file is replaced only with " +
				"confirmation. Contents are untrusted data.",
			Tags:        []string{p.id, "contents", "raw", "get"},
			Risk:        readRisk(p.id + contentSensitivity),
			LocalFiles:  config.LocalFilesWrite,
			InputSchema: inputSchema(contentPathKeys+`,"media":{"type":"boolean"},`+localPathProperty(), "path"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"path":{"type":"string"},"media":{"type":"boolean"},"content":{"type":"string"},` +
				`"binary":{"type":"boolean"},"truncated":{"type":"boolean"},"size":{"type":"integer"},` +
				`"local_path":{"type":"string"},"sha256":{"type":"string"}},"required":["repository","path"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, contentPathArgument, contentRefArgument,
				{Name: "media", Description: "True reads the content of an LFS file instead of its pointer"},
				localfile.DownloadPathArgument()},
			Fields: []capability.Field{
				{Name: "repository", Description: "Repository of the file, as OWNER/REPO"},
				{Name: "path", Description: "Path asked for"},
				{Name: "media", Description: "True when the LFS route was used"},
				{Name: "content", Description: "The text of the file, at most 256 KiB, untrusted data; absent " +
					"for a binary file or with local_path"},
				{Name: "binary", Description: "True when the file is binary, so its content is left out"},
				{Name: "truncated", Description: "True when the file holds more text than content"},
				{Name: "size", Description: "Size of the file in bytes, when known"},
				{Name: "local_path", Description: "Local file written, as given"},
				{Name: "sha256", Description: "SHA-256 of the written file as hex"},
			},
			Examples: []capability.Example{{Description: "Read a file as text",
				Arguments: json.RawMessage(`{"repository":"example/project","path":"README.md"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var arguments contentReadArguments
		repo, err := arguments.read(allowed, raw)
		if err != nil {
			return nil, nil, err
		}
		route := "raw"
		if arguments.Media {
			route = "media"
		}
		path, query := treePath(repo, route, arguments.Path), refQuery(arguments.Ref)
		result := map[string]any{"repository": repo.owner + "/" + repo.repo, "path": arguments.Path,
			"media": arguments.Media}
		const op = "get raw file"
		if arguments.LocalPath == nil {
			return func(ctx context.Context, c *Client) (any, error) {
				answer := &textAnswer{limit: maxContentBytes}
				header, err := c.getRaw(ctx, op, path, query, answer)
				if err != nil {
					return nil, err
				}
				data := answer.data
				// A cut inside a character drops that character's bytes, at most three.
				for i := 0; i < utf8.UTFMax && answer.truncated && len(data) > 0 && !utf8.Valid(data); i++ {
					data = data[:len(data)-1]
				}
				if size := int64(len(data)); !answer.truncated {
					result["size"] = size
				} else if announced, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64); err == nil &&
					announced >= 0 {
					result["size"] = announced
				}
				if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
					result["binary"] = true
					return result, nil
				}
				result["content"], result["truncated"] = string(data), answer.truncated
				return result, nil
			}, func() {}, nil
		}
		download, release, err := downloadTarget(ctx, resolved, *arguments.LocalPath)
		if err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.fetch(ctx, op, apiPrefix+path, query, -1, download); err != nil {
				return nil, err
			}
			if err := download.Commit(); err != nil {
				return nil, err
			}
			sum, _ := download.SHA256()
			result["local_path"], result["size"], result["sha256"] = *arguments.LocalPath, download.Size(), sum
			return result, nil
		}, release, nil
	},
}

// getRaw sends one GET of a fixed API path and reads the answer as text, with the response headers.
func (c *Client) getRaw(ctx context.Context, op, path string, query url.Values, answer *textAnswer) (http.Header, error) {
	_, header, err := exchange(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, query,
		request{}, answer)
	return header, err
}

var archiveFormats = []string{"zip", "tar.gz"}

var archivesGet = &tool{
	group: "archives", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := `"repository":` + repoSchema + `,"ref":` + contentRefSchema +
			`,"format":{"type":"string","enum":["zip","tar.gz"]},` + localPathProperty()
		arguments := []capability.Argument{repositoryArgument,
			{Name: "ref", Required: true, Description: "Branch, tag, or commit to archive"},
			{Name: "format", Description: "zip (default) or tar.gz"}}
		if p == gitea {
			properties += `,"paths":{"type":"array","minItems":1,"maxItems":10,"items":` + contentPathSchema + `}`
			arguments = append(arguments, capability.Argument{Name: "paths",
				Description: "Only these paths of the repository, 1 to 10; Gitea only"})
		}
		path := localfile.DownloadPathArgument()
		path.Required = true
		return capability.Descriptor{
			Title: "Download a " + p.name + " repository archive",
			Description: "Write the archive of a branch, tag, or commit of a repository inside the connection's " +
				"targets to local_path, in a directory the connection releases for writing, streamed. A redirect " +
				"to another address is refused, no partial file is left, and an existing local file is replaced " +
				"only with confirmation. The answer carries metadata only.",
			Tags:        []string{p.id, "archives", "get", "download"},
			Risk:        readRisk(p.id + contentSensitivity),
			LocalFiles:  config.LocalFilesWrite,
			InputSchema: inputSchema(properties, "ref", localfile.LocalPathArgument),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"ref":{"type":"string"},"format":{"type":"string"},"local_path":{"type":"string"},` +
				`"size":{"type":"integer"},"sha256":{"type":"string"}},"required":["repository","ref","format",` +
				`"local_path","size","sha256"],"additionalProperties":false}`),
			Arguments: append(arguments, path),
			Fields: []capability.Field{
				{Name: "repository", Description: "Repository of the archive, as OWNER/REPO"},
				{Name: "ref", Description: "Branch, tag, or commit archived"},
				{Name: "format", Description: "zip or tar.gz"},
				{Name: "local_path", Description: "Local file written, as given"},
				{Name: "size", Description: "Size of the written file in bytes"},
				{Name: "sha256", Description: "SHA-256 of the written file as hex"},
			},
			Examples: []capability.Example{{Description: "Download a release tag as zip",
				Arguments: json.RawMessage(`{"repository":"example/project","ref":"v1.2.0","local_path":"~/downloads/project.zip"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var arguments struct {
			Repository string   `json:"repository"`
			Ref        string   `json:"ref"`
			Format     string   `json:"format"`
			Paths      []string `json:"paths"`
			LocalPath  *string  `json:"local_path"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, nil, err
		}
		if err := checkRefName("ref", arguments.Ref, true); err != nil {
			return nil, nil, err
		}
		if arguments.Format == "" {
			arguments.Format = "zip"
		}
		if !slices.Contains(archiveFormats, arguments.Format) {
			return nil, nil, invalidArgument("format must be zip or tar.gz")
		}
		query := url.Values{}
		if arguments.Paths != nil {
			if p != gitea {
				return nil, nil, otherProduct(p, "paths")
			}
			if len(arguments.Paths) < 1 || len(arguments.Paths) > maxArchivePaths {
				return nil, nil, invalidArgument("paths takes 1 to 10 paths")
			}
			for _, path := range arguments.Paths {
				if err := checkFilePath("paths", path); err != nil {
					return nil, nil, err
				}
				query.Add("path", path)
			}
		}
		if arguments.LocalPath == nil {
			return nil, nil, invalidArgument("local_path is required")
		}
		download, release, err := downloadTarget(ctx, resolved, *arguments.LocalPath)
		if err != nil {
			return nil, nil, err
		}
		path := treePath(repo, "archive", arguments.Ref+"."+arguments.Format)
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.fetch(ctx, "download archive", apiPrefix+path, query, -1, download); err != nil {
				return nil, err
			}
			if err := download.Commit(); err != nil {
				return nil, err
			}
			sum, _ := download.SHA256()
			return map[string]any{"repository": repo.owner + "/" + repo.repo, "ref": arguments.Ref,
				"format": arguments.Format, "local_path": *arguments.LocalPath, "size": download.Size(),
				"sha256": sum}, nil
		}, release, nil
	},
}

var editorconfigGet = &tool{
	group: "editorconfig", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the " + p.name + " editor configuration of a file",
			Description: "Read the editor configuration that applies to one path of a repository inside the " +
				"connection's targets at a branch, tag, or commit, as the settings of its .editorconfig files; " +
				"values are untrusted data",
			Tags:        []string{p.id, "editorconfig", "get"},
			Risk:        readRisk(p.id + contentSensitivity),
			InputSchema: inputSchema(contentPathKeys, "path"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"path":{"type":"string"},"config":{"type":"object"}},"required":["repository","path"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, contentPathArgument, contentRefArgument},
			Fields: []capability.Field{
				{Name: "repository", Description: "Repository of the file, as OWNER/REPO"},
				{Name: "path", Description: "Path asked for"},
				{Name: "config", Description: "Settings that apply to the path, such as indent_style; empty " +
					"when the instance sent none, untrusted data"},
			},
			Examples: []capability.Example{{Description: "Read the settings of a file",
				Arguments: json.RawMessage(`{"repository":"example/project","path":"main.go"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments contentReadArguments
		repo, err := arguments.read(allowed, raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get editor configuration"
			answer := &textAnswer{limit: maxEntryBytes}
			if _, err := c.getRaw(ctx, op, treePath(repo, "editorconfig", arguments.Path), refQuery(arguments.Ref),
				answer); err != nil {
				return nil, err
			}
			settings := map[string]any{}
			if body := bytes.TrimSpace(answer.data); len(body) > 0 {
				if answer.truncated || json.Unmarshal(body, &settings) != nil || settings == nil {
					return nil, provider.InvalidResponse(op, p.name+" returned an invalid response")
				}
			}
			settings, _ = boundedFree(settings, 0).(map[string]any)
			return map[string]any{"repository": repo.owner + "/" + repo.repo, "path": arguments.Path,
				"config": settings}, nil
		}, nil
	},
}
