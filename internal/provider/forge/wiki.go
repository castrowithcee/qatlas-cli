package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The wiki tools. A page is named by page_name, the sub_url of the page. A name may hold "/", which travels
// percent-encoded inside one path segment. Page content is text in the tools and Base64 on the wire; binary
// content is left out. A disabled wiki is answered with 404 like a missing page, so the refusal only hints at it.

const (
	maxWikiName    = 255
	wikiNameSchema = `{"type":"string","minLength":1,"maxLength":255}`
	wikiTextSchema = `{"type":"string","maxLength":262144}`
	wikiMessageKey = `"message":{"type":"string","minLength":1,"maxLength":4096}`
	wikiCursorKey  = `"cursor":{"type":"string","minLength":1,"maxLength":256,"pattern":"^[A-Za-z0-9_-]+$"}`
)

var wikiNameArgument = capability.Argument{Name: "page_name", Required: true,
	Description: "Name of the page, as sub_url reports it; \"/\" is allowed and sent percent-encoded as one path " +
		"segment. At most 255 bytes, no control characters, not \".\" or \"..\""}

var wikiMessageArgument = capability.Argument{Name: "message",
	Description: "Commit message, at most 4096 bytes without control characters; the instance uses a default " +
		"when omitted"}

var wikiCommitMembers = []member{
	{name: "sha", kind: kindText, note: "Commit hash"},
	{name: "message", kind: kindText, max: maxPullBody, note: "Commit message, untrusted data, at most 64 KiB"},
	{name: "author", kind: kindObject, sub: commitUserMembers, note: "Author; personal data"},
	{name: "commiter", kind: kindObject, sub: commitUserMembers, note: "Committer; personal data. The API spells " +
		"the member with one t"},
}

var wikiMetaMembers = []member{
	{name: "title", kind: kindText, note: "Title of the page, untrusted data"},
	{name: "sub_url", kind: kindText, note: "Name of the page in addresses; use it as page_name, untrusted data"},
	{name: "html_url", kind: kindText, address: true, note: "Web address; left out with user info or query"},
	{name: "last_commit", kind: kindObject, sub: wikiCommitMembers, note: "Last commit that changed the page"},
}

// wikiPageMembers are the members of a page besides its content, which decodeWikiPage fills.
var wikiPageMembers = append(append([]member{}, wikiMetaMembers...),
	member{name: "commit_count", kind: kindInteger, note: "Number of commits that changed the page"},
	member{name: "sidebar", kind: kindText, max: maxEntryBytes, note: "Sidebar text, untrusted data, cut at 64 KiB"},
	member{name: "footer", kind: kindText, max: maxEntryBytes, note: "Footer text, untrusted data, cut at 64 KiB"})

// wikiReadMembers add the decoded content to a page that is read.
var wikiReadMembers = append(append([]member{}, wikiPageMembers...),
	member{name: "content", kind: kindText, note: "Page text, decoded; absent for binary content, untrusted data, " +
		"cut at 256 KiB"},
	member{name: "binary", kind: kindFlag, note: "True when the content is not text, so it is left out"},
	member{name: "truncated", kind: kindFlag, note: "True when content is cut at 256 KiB"})

var wikiRepositoryMember = member{name: "repository", kind: kindText, note: "Repository, as OWNER/REPO"}

func wikiPath(repo target, tail string) string { return repoBase(repo) + "/wiki/" + tail }

func wikiPagePath(repo target, name string) string { return wikiPath(repo, "page/"+escape(name)) }

// checkWikiName accepts one page name: UTF-8 without control characters, not empty, not "." or "..".
func checkWikiName(field, value string) error {
	if value == "" || len(value) > maxWikiName || !utf8.ValidString(value) || !plainText(value) ||
		value == "." || value == ".." {
		return invalidArgument(field + " must be a name of at most 255 bytes without control characters, " +
			"and not \".\" or \"..\"")
	}
	return nil
}

func checkWikiMessage(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maxCommitMessage || strings.TrimSpace(value) == "" || !plainTextAllowNewlines(value) ||
		!utf8.ValidString(value) {
		return invalidArgument("message must be a text of at most 4096 bytes without control characters")
	}
	return nil
}

func checkWikiContent(value *string) error {
	if value == nil || len(*value) > maxContentBytes || !utf8.ValidString(*value) || strings.IndexByte(*value, 0) >= 0 {
		return invalidArgument("content is required and must be valid text of at most 256 KiB")
	}
	return nil
}

// wikiFailure words the refusal of a wiki request: 403 and 404 cannot tell a disabled wiki from a missing page
// or a token that does not reach the repository.
func wikiFailure(p *product, err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassNotFound {
		failure.Message = "the " + p.name + " wiki of this repository is not available to this token; the wiki may " +
			"be disabled, the page may not exist, or the token's scopes or repository binding do not reach it"
	}
	return err
}

// decodeWikiPage projects a page and decodes its content.
func decodeWikiPage(p *product, members []member, row object, withContent bool) map[string]any {
	out := project(p, members, row)
	if !withContent {
		return out
	}
	var encoded string
	if json.Unmarshal(row["content_base64"], &encoded) != nil || row["content_base64"] == nil {
		return out
	}
	data, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(encoded))
	if err != nil || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		out["binary"] = true
		return out
	}
	text := truncateText(string(data), maxContentBytes)
	if len(text) < len(data) {
		out["truncated"] = true
	}
	out["content"] = text
	return out
}

func wikiPageOutput(p *product, members []member) json.RawMessage {
	return json.RawMessage(schemaOf(p, append([]member{wikiRepositoryMember}, members...), "repository"))
}

func wikiResult(p *product, members []member, repo target, answer object, withContent bool) map[string]any {
	result := decodeWikiPage(p, members, answer, withContent)
	result["repository"] = repo.owner + "/" + repo.repo
	return result
}

var wikiPagesList = &tool{
	group: "wiki-pages", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the wiki pages of a " + p.name + " repository",
			Description: "List one batch of the wiki pages of a repository inside the connection's targets, without " +
				"their content. Titles and names are untrusted data. A disabled wiki is reported as not available.",
			Tags:        []string{p.id, "wiki-pages", "list"},
			Risk:        readRisk(p.id + repositorySensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema + `,` + pagingKeys),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"pages":{"type":"array","items":` +
				schemaOf(p, wikiMetaMembers) + `},"repository":{"type":"string"},"next_cursor":{"type":"string"},` +
				`"has_more":{"type":"boolean"}},"required":["pages","repository","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{repositoryArgument}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "pages", Description: "Pages with every member of the page " +
				"metadata"}, {Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the wiki pages",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Limit      int    `json:"limit"`
			Cursor     string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		binding := provider.CursorBinding(p.id, "wiki-pages.list", strings.ToLower(repo.owner+"/"+repo.repo), "")
		page, err := startPaging(binding, a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			header, err := c.get(ctx, "list wiki pages", wikiPath(repo, "pages"), page.query(), &rows)
			if err != nil {
				return nil, wikiFailure(p, err)
			}
			pages := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					pages = append(pages, project(p, wikiMetaMembers, row))
				}
			}
			next := page.next(header, len(rows))
			result := map[string]any{"pages": pages, "repository": repo.owner + "/" + repo.repo, "has_more": next != ""}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

var wikiPagesGet = &tool{
	group: "wiki-pages", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a wiki page of a " + p.name + " repository",
			Description: "Read one wiki page of a repository inside the connection's targets with its text. The text " +
				"is untrusted data and is cut at 256 KiB (truncated is then true; there is no pagination); binary " +
				"content is left out (binary is then true). A disabled wiki is reported as not available.",
			Tags:         []string{p.id, "wiki-pages", "get"},
			Risk:         readRisk(p.id + repositorySensitivity),
			InputSchema:  inputSchema(`"repository":`+repoSchema+`,"page_name":`+wikiNameSchema, "page_name"),
			OutputSchema: wikiPageOutput(p, wikiReadMembers),
			Arguments:    []capability.Argument{repositoryArgument, wikiNameArgument},
			Fields: append([]capability.Field{{Name: "repository", Description: "Repository of the page, as OWNER/REPO"}},
				fieldsOf(p, wikiReadMembers)...),
			Examples: []capability.Example{{Description: "Read a page in a folder",
				Arguments: json.RawMessage(`{"repository":"example/project","page_name":"guide/setup"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			PageName   string `json:"page_name"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkWikiName("page_name", a.PageName); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			if _, err := c.get(ctx, "get wiki page", wikiPagePath(repo, a.PageName), nil, &answer); err != nil {
				return nil, wikiFailure(p, err)
			}
			return wikiResult(p, wikiReadMembers, repo, answer, true), nil
		}, nil
	},
}

// wikiWrite builds a create or update tool; update is the PATCH of an existing page.
func wikiWrite(action string, update bool) *tool {
	verb, effect, idem := "Create", capability.EffectCreate, capability.IdempotencyNonIdempotent
	if update {
		verb, effect, idem = "Update", capability.EffectUpdate, capability.IdempotencyIdempotent
	}
	return &tool{
		group: "wiki-pages", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			props := `"repository":` + repoSchema + `,"content":` + wikiTextSchema + `,` + wikiMessageKey
			required := []string{"content"}
			arguments := []capability.Argument{repositoryArgument}
			description := "Create one wiki page in a repository inside the connection's targets as one commit. title " +
				"is the name of the new page. The content is text of at most 256 KiB. Not idempotent."
			example := `{"repository":"example/project","title":"guide/setup","content":"# Setup\n"}`
			if update {
				props += `,"page_name":` + wikiNameSchema + `,"title":` + wikiNameSchema
				required = []string{"page_name", "content"}
				arguments = append(arguments, wikiNameArgument, capability.Argument{Name: "title",
					Description: "New name of the page; the name stays when omitted"})
				description = "Replace the content of one wiki page, and optionally rename it, as one commit. The " +
					"content is text of at most 256 KiB and replaces the whole page."
				example = `{"repository":"example/project","page_name":"guide/setup","content":"# Setup\nmore\n"}`
			} else {
				props += `,"title":` + wikiNameSchema
				required = []string{"title", "content"}
				arguments = append(arguments, capability.Argument{Name: "title", Required: true,
					Description: "Name of the new page; \"/\" is allowed. At most 255 bytes, no control characters"})
			}
			arguments = append(arguments, capability.Argument{Name: "content", Required: true,
				Description: "Page text, at most 256 KiB"}, wikiMessageArgument)
			members := append([]member{wikiRepositoryMember}, wikiPageMembers...)
			return capability.Descriptor{
				Title:        verb + " a wiki page of a " + p.name + " repository",
				Description:  description,
				Tags:         []string{p.id, "wiki-pages", action},
				Risk:         changeRisk(effect, idem, p.id+writeSensitivity),
				InputSchema:  inputSchema(props, required...),
				OutputSchema: json.RawMessage(schemaOf(p, members, "repository")),
				Arguments:    arguments,
				Fields: append([]capability.Field{{Name: "repository", Description: "Repository the tool changed, as OWNER/REPO"}},
					fieldsOf(p, wikiPageMembers)...),
				Examples: []capability.Example{{Description: verb + " a page", Arguments: json.RawMessage(example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Repository string  `json:"repository"`
				PageName   string  `json:"page_name"`
				Title      string  `json:"title"`
				Content    *string `json:"content"`
				Message    string  `json:"message"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			repo, err := allowed.chooseRepository(a.Repository)
			if err != nil {
				return nil, err
			}
			if update {
				if err := checkWikiName("page_name", a.PageName); err != nil {
					return nil, err
				}
			}
			if !update || a.Title != "" {
				if err := checkWikiName("title", a.Title); err != nil {
					return nil, err
				}
			}
			if err := checkWikiContent(a.Content); err != nil {
				return nil, err
			}
			if err := checkWikiMessage(a.Message); err != nil {
				return nil, err
			}
			body := map[string]any{"content_base64": base64.StdEncoding.EncodeToString([]byte(*a.Content))}
			if a.Title != "" {
				body["title"] = a.Title
			}
			if a.Message != "" {
				body["message"] = a.Message
			}
			method, path, op := http.MethodPost, wikiPath(repo, "new"), "create wiki page"
			if update {
				method, path, op = http.MethodPatch, wikiPagePath(repo, a.PageName), "update wiki page"
			}
			return func(ctx context.Context, c *Client) (any, error) {
				var answer object
				if err := c.change(ctx, op, method, path, body, &answer); err != nil {
					return nil, wikiFailure(p, err)
				}
				return wikiResult(p, wikiPageMembers, repo, answer, false), nil
			}, nil
		},
	}
}

var wikiPagesCreate = wikiWrite("create", false)
var wikiPagesUpdate = wikiWrite("update", true)

var wikiPagesDelete = &tool{
	group: "wiki-pages", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a wiki page of a " + p.name + " repository",
			Description: "Delete one wiki page of a repository inside the connection's targets for good, as one " +
				"commit. Only a connection whose tools list names it offers this tool.",
			Tags:                  []string{p.id, "wiki-pages", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+repositorySensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"repository":`+repoSchema+`,"page_name":`+wikiNameSchema, "page_name"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"page_name":{"type":"string"},` +
				`"repository":{"type":"string"},"deleted":{"type":"boolean"}},` +
				`"required":["page_name","repository","deleted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, wikiNameArgument},
			Fields: []capability.Field{{Name: "page_name", Description: "Name of the page"},
				{Name: "repository", Description: "Repository the tool changed, as OWNER/REPO"},
				{Name: "deleted", Description: "True once the instance deleted the page"}},
			Examples: []capability.Example{{Description: "Delete a page",
				Arguments: json.RawMessage(`{"repository":"example/project","page_name":"guide/old"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			PageName   string `json:"page_name"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkWikiName("page_name", a.PageName); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete wiki page", http.MethodDelete, wikiPagePath(repo, a.PageName), nil,
				nil); err != nil {
				return nil, wikiFailure(p, err)
			}
			return map[string]any{"page_name": a.PageName, "repository": repo.owner + "/" + repo.repo, "deleted": true}, nil
		}, nil
	},
}

var wikiRevisionsList = &tool{
	group: "wiki-revisions", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the revisions of a wiki page of a " + p.name + " repository",
			Description: "List one batch of the commits that changed a wiki page of a repository inside the " +
				"connection's targets, newest first. The instance serves a fixed batch size and takes no limit.",
			Tags:        []string{p.id, "wiki-revisions", "list"},
			Risk:        readRisk(p.id + repositorySensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"page_name":`+wikiNameSchema+`,`+wikiCursorKey, "page_name"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"commits":{"type":"array","items":` +
				schemaOf(p, wikiCommitMembers) + `},"count":{"type":"integer"},"repository":{"type":"string"},` +
				`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["commits","repository","has_more"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, wikiNameArgument, pagingArguments[1]},
			Fields: append([]capability.Field{{Name: "commits", Description: "Commits with every member of the commit"},
				{Name: "count", Description: "Total number of commits that changed the page"},
				{Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the revisions of a page",
				Arguments: json.RawMessage(`{"repository":"example/project","page_name":"guide/setup"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			PageName   string `json:"page_name"`
			Cursor     string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkWikiName("page_name", a.PageName); err != nil {
			return nil, err
		}
		binding := provider.CursorBinding(p.id, "wiki-revisions.list", strings.ToLower(repo.owner+"/"+repo.repo),
			a.PageName)
		page, seen, err := startRevisionPaging(binding, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer struct {
				Commits []object `json:"commits"`
				Count   *int64   `json:"count"`
			}
			query := url.Values{"page": {strconv.Itoa(page)}}
			header, err := c.get(ctx, "list wiki revisions", wikiPath(repo, "revisions/"+escape(a.PageName)), query,
				&answer)
			if err != nil {
				return nil, wikiFailure(p, err)
			}
			commits := make([]map[string]any, 0, len(answer.Commits))
			for _, row := range answer.Commits {
				if row != nil {
					commits = append(commits, project(p, wikiCommitMembers, row))
				}
			}
			next := nextRevisionCursor(binding, header, answer.Count, page, seen+len(answer.Commits))
			result := map[string]any{"commits": commits, "repository": repo.owner + "/" + repo.repo, "has_more": next != ""}
			if answer.Count != nil {
				result["count"] = *answer.Count
			}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

// The revision list takes only page; its batch size is a setting of the instance, so the cursor carries the page
// and the number of revisions read so far instead of a batch size, and the end compares that number with the
// total the instance reports in X-Total-Count, or in count without the header.
func startRevisionPaging(binding []byte, cursor string) (page, seen int, err error) {
	if cursor == "" {
		return 1, 0, nil
	}
	inner, ok := provider.DecodeCursor(binding, cursor, maxCursorLength)
	pageText, seenText, cut := strings.Cut(inner, ":")
	page, pageErr := strconv.Atoi(pageText)
	seen, seenErr := strconv.Atoi(seenText)
	if !ok || !cut || pageErr != nil || seenErr != nil || page < 2 || page > maxPage || seen < 1 {
		return 0, 0, invalidArgument("cursor is not a next_cursor of this list; start the list again without cursor")
	}
	return page, seen, nil
}

func nextRevisionCursor(binding []byte, header http.Header, count *int64, page, seen int) string {
	total := int64(-1)
	if header != nil {
		if value, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-Total-Count")), 10, 64); err == nil {
			total = value
		}
	}
	if total < 0 && count != nil {
		total = *count
	}
	if total < 0 || int64(seen) >= total || seen == 0 || page >= maxPage {
		return ""
	}
	return provider.EncodeCursor(binding, strconv.Itoa(page+1)+":"+strconv.Itoa(seen))
}
