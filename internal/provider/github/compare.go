package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// github.compare.get compares two refs of the bound repository through GitHub's compare route and answers
// the comparison with its commits and its changed files, each file with a patch excerpt hard bounded and the
// cut visible. GitHub pages the commits and the files of one comparison by the same page and per_page, so a
// cursor moves both. It writes nothing and downloads no archive.

// compareReadPermission names what the compare route needs without claiming what the configured token holds.
const compareReadPermission = "GitHub refused this token the comparison in this repository; reading it needs " +
	"no scope for a public repository, or repo on a classic token, or Contents: read on a fine-grained token, " +
	"for a private one"

var compareCommitProperties = commitEntryProperties

var compareProperties = `"url":{"type":"string"},"links":` + linksSchema(compareLinkKeys) + `,` +
	`"base_commit":` + objectSchema(compareCommitProperties) + `,"merge_base_commit":` +
	objectSchema(compareCommitProperties) + `,"status":{"type":"string"},"ahead_by":{"type":"integer"},` +
	`"behind_by":{"type":"integer"},"total_commits":{"type":"integer"},"commits":{"type":"array","items":` +
	objectSchema(compareCommitProperties) + `},"files":{"type":"array","maxItems":300,"items":{"type":"object",` +
	`"properties":{` + commitFileProperties + `},` + commitFileRequired + `}},"files_truncated":{"type":"boolean"},` +
	`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}`

var compareLinkKeys = []string{"url", "permalink_url", "diff_url", "patch_url"}

var compareGet = capability.Descriptor{
	ID:      Provider + ".compare.get",
	Version: 1,
	Title:   "Compare two GitHub refs",
	Description: "Compare two branches, tags, or commit SHAs of a repository a connection allows, base first, " +
		"with the status, the ahead and behind counts, the merge base, one bounded batch of the commits, and " +
		"the changed files, each with a patch excerpt hard bounded and the cut visible; a fork's ref is " +
		"accepted as OWNER:ref only for an owner the connection's targets allow",
	Tags:        []string{"github", "compare", "commits", "get"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"base":`+headSchema+`,"head":`+headSchema+`,`+pagingKeys, "base", "head"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + compareProperties + `},` +
		`"required":["status","ahead_by","behind_by","total_commits","commits","files","files_truncated",` +
		`"has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "base", Description: "Branch, tag, or commit SHA to compare from, or OWNER:ref for a fork of an " +
			"owner the connection's targets allow", Required: true},
		{Name: "head", Description: "Branch, tag, or commit SHA to compare to, or OWNER:ref for a fork of an " +
			"owner the connection's targets allow", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "url", Description: "Web address of the comparison, untrusted data, never fetched"},
		{Name: "links", Description: "REST url, permalink_url, diff_url, and patch_url by their GitHub names, " +
			"untrusted data, never fetched"},
		{Name: "base_commit", Description: "Commit of base as a compact commit: the members of commits below"},
		{Name: "merge_base_commit", Description: "Common ancestor of base and head as a compact commit"},
		{Name: "status", Description: "diverged, ahead, behind, or identical, as seen from head"},
		{Name: "ahead_by", Description: "Number of commits head is ahead of base"},
		{Name: "behind_by", Description: "Number of commits head is behind base"},
		{Name: "total_commits", Description: "Number of commits of the whole comparison, across every batch"},
		{Name: "commits", Description: "One batch of the commits of the comparison, oldest first as GitHub " +
			"orders them: sha, message cut to its first line and at most 200 characters (untrusted data), " +
			"author, committer, date, and parents as a count, then the members of GitHub's commit; carries " +
			"no stats and no files, since files holds the diff"},
		{Name: "files", Description: "Changed files of the batch, at most 300, with path, status, additions, " +
			"deletions, changes, previous_filename for a rename, patch cut to at most 4096 bytes with " +
			"patch_truncated, and the file's blob sha and links; patch is untrusted data and left out for a " +
			"file GitHub sends none for, such as a binary file"},
		{Name: "files_truncated", Description: "True when the batch held at least as many files as GitHub " +
			"returns at most per request, so more may exist than were returned"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "Compare a feature branch with main",
		Arguments:   json.RawMessage(`{"base":"main","head":"feature/login","limit":50}`),
	}},
}

func compareOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: compareGet, Handler: commitLinkHandler(compareGet.ID, checkCompareArguments,
			func(ctx context.Context, c *Client, a *commitLinkArguments) (any, error) { return c.compare(ctx, a) })},
	}
}

// commitLinkArguments holds the arguments of the compare, branch, and commit relation tools; the input schema
// of each tool admits only its own. page, perPage, and binding are derived by the checks.
type commitLinkArguments struct {
	Base      string `json:"base"`
	Head      string `json:"head"`
	Branch    string `json:"branch"`
	CommitSHA string `json:"commit_sha"`
	CommentID int64  `json:"comment_id"`
	Limit     int    `json:"limit"`
	Cursor    string `json:"cursor"`

	page, perPage int
	binding       []byte
}

// commitLinkHandler decodes and checks the arguments and the repository, and with it every fork owner a
// compare names, before a credential is resolved, so a refused request never becomes a provider call.
func commitLinkHandler(id string, check func(*commitLinkArguments, target, allowlist) error,
	call func(context.Context, *Client, *commitLinkArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments commitLinkArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		allowed, err := allowlistOf(resolved)
		if err != nil {
			return nil, providerError("open", err.Error())
		}
		if err := check(&arguments, bound, allowed); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func checkCompareArguments(a *commitLinkArguments, bound target, allowed allowlist) error {
	if err := checkCompareSide("base", a.Base, bound, allowed); err != nil {
		return err
	}
	if err := checkCompareSide("head", a.Head, bound, allowed); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("compare", "get", bound.String(), a.Base, a.Head)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

// checkCompareSide accepts a ref of the bound repository or OWNER:ref. A fork's repository is not named by
// the connection's targets, only its owner can be: the fork must be admitted under the bound repository's
// name, which is the narrower reading, so a fork renamed away from it is refused rather than guessed. The
// refusal never quotes the owner.
func checkCompareSide(name, value string, bound target, allowed allowlist) error {
	owner, ref, forked := strings.Cut(value, ":")
	if !forked {
		ref = value
	}
	if !validRef(ref) || forked && !validLogin(owner) {
		return invalidRequest(name + " must be a branch, a tag, or a commit SHA, or OWNER:ref for a fork")
	}
	if forked && !allowed.admitsRepository(target{kind: kindRepository, owner: owner, repo: bound.repo}) {
		return invalidRequest(name + " names a fork owner outside the targets of this connection; add the " +
			"owner to the connection's targets or compare a ref of the repository itself")
	}
	return nil
}

// comparisonRaw is the REST comparison object.
type comparisonRaw struct {
	HTMLURL         *string         `json:"html_url"`
	BaseCommit      *commitItem     `json:"base_commit"`
	MergeBaseCommit *commitItem     `json:"merge_base_commit"`
	Status          string          `json:"status"`
	AheadBy         int             `json:"ahead_by"`
	BehindBy        int             `json:"behind_by"`
	TotalCommits    int             `json:"total_commits"`
	Commits         []commitItem    `json:"commits"`
	Files           []commitFileRaw `json:"files"`
	links           Links
}

func (r *comparisonRaw) UnmarshalJSON(data []byte) (err error) {
	type plain comparisonRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, compareLinkKeys)
	return err
}

// Comparison is the view of two compared refs. Files carries the diff of the batch, so the commits carry
// none.
type Comparison struct {
	URL             *string       `json:"url,omitempty"`
	Links           Links         `json:"links,omitempty"`
	BaseCommit      *CommitEntry  `json:"base_commit,omitempty"`
	MergeBaseCommit *CommitEntry  `json:"merge_base_commit,omitempty"`
	Status          string        `json:"status"`
	AheadBy         int           `json:"ahead_by"`
	BehindBy        int           `json:"behind_by"`
	TotalCommits    int           `json:"total_commits"`
	Commits         []CommitEntry `json:"commits"`
	Files           []CommitFile  `json:"files"`
	FilesTruncated  bool          `json:"files_truncated"`
	NextCursor      string        `json:"next_cursor,omitempty"`
	HasMore         bool          `json:"has_more"`
}

// commitFileOf is the bounded view of one changed file: the patch is cut and binary content never arrives.
func commitFileOf(f commitFileRaw) CommitFile {
	path, _ := cleanText(f.Filename, projectionPathLimit, false)
	previous, _ := cleanText(f.PreviousFilename, projectionPathLimit, false)
	file := CommitFile{Path: valueOr(path), Status: valueOr(nameText(f.Status)), PreviousFilename: valueOr(previous),
		SHA: nameText(f.SHA), Links: f.links}
	if f.Additions != nil {
		file.Additions = *f.Additions
	}
	if f.Deletions != nil {
		file.Deletions = *f.Deletions
	}
	if f.Changes != nil {
		file.Changes = *f.Changes
	}
	if f.Patch != nil {
		file.Patch, file.PatchTruncated = truncatePatch(*f.Patch)
	}
	return file
}

func compareEntry(op string, item *commitItem) (*CommitEntry, error) {
	if item == nil {
		return nil, nil
	}
	if item.commit.SHA == "" {
		return nil, invalidEntry(op, "a commit")
	}
	entry := item.entry()
	return &entry, nil
}

func (c *Client) compare(ctx context.Context, a *commitLinkArguments) (*Comparison, error) {
	const op = "compare refs"
	// Each side is one path segment: a slash of a branch name is escaped, and git forbids ".." and
	// therefore "..." inside a ref, so the separator is unambiguous.
	path := c.repoPath("compare/" + url.PathEscape(a.Base) + "..." + url.PathEscape(a.Head))
	var raw comparisonRaw
	hasNext, err := c.restPage(ctx, op, path, url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}, &raw)
	if err != nil {
		return nil, actionsFailure(err, compareReadPermission)
	}
	if raw.Status == "" {
		return nil, invalidEntry(op, "a comparison")
	}
	result := &Comparison{URL: address(raw.HTMLURL), Links: raw.links, Status: raw.Status, AheadBy: raw.AheadBy,
		BehindBy: raw.BehindBy, TotalCommits: raw.TotalCommits,
		Commits: make([]CommitEntry, 0, len(raw.Commits)), Files: make([]CommitFile, 0, len(raw.Files))}
	if result.BaseCommit, err = compareEntry(op, raw.BaseCommit); err != nil {
		return nil, err
	}
	if result.MergeBaseCommit, err = compareEntry(op, raw.MergeBaseCommit); err != nil {
		return nil, err
	}
	for i := range raw.Commits {
		entry, err := compareEntry(op, &raw.Commits[i])
		if err != nil {
			return nil, err
		}
		result.Commits = append(result.Commits, *entry)
	}
	files := raw.Files
	if len(files) >= maxCommitFiles {
		files, result.FilesTruncated = files[:maxCommitFiles], true
	}
	for _, file := range files {
		result.Files = append(result.Files, commitFileOf(file))
	}
	more := hasNext || a.page*a.perPage < raw.TotalCommits
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, more)
	return result, nil
}

// compareSubject names the comparison a compare path below a repository addresses: compare/BASE...HEAD. It
// names only refs of the characters the input schema allows, and is empty otherwise.
func compareSubject(path string) string {
	rest, ok := strings.CutPrefix(path, "compare/")
	if !ok {
		return ""
	}
	rest, _, _ = strings.Cut(rest, "?")
	rest, err := url.PathUnescape(rest)
	if err != nil {
		return ""
	}
	base, head, ok := strings.Cut(rest, "...")
	if !ok || !validHead(base) || !validHead(head) {
		return ""
	}
	return "comparison " + base + "..." + head
}
