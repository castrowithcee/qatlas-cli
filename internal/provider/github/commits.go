package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The commit tools read the commits of a repository without writing anything. github.commits.list lists one
// bounded, filtered batch of compact commits, newest first, without a patch; github.commits.get reads one
// commit by branch, tag, or commit SHA with its stats and its changed files, each with a patch excerpt hard
// bounded, the cut visible, and the file count itself hard bounded, the cut visible as well. Neither tool
// diffs two arbitrary refs; that stays out of scope, exactly as for the contents and tree tools.
//
// github.commits.search already searches GitHub's commit index across repositories with terms and
// qualifiers; these two tools instead read the commits of one bound repository directly, through the REST
// commits routes, and take no search terms.

// Bounds of the commit tools.
const (
	maxCommitSummaryLength = 200  // runes kept of a commit's message in the list, cut at its first line
	maxCommitMessageLength = 4096 // runes kept of a commit's full message in the get
	maxCommitFiles         = 300  // files kept per github.commits.get; GitHub's own limit is the same
)

const commitEntryProperties = `"sha":{"type":"string"},"message":{"type":"string"},"author":{"type":"string"},` +
	`"committer":{"type":"string"},"date":{"type":"string"},"parents":{"type":"integer"}`

const commitEntryRequired = `"required":["sha","message","parents"],"additionalProperties":false`

var commitsList = capability.Descriptor{
	ID:      Provider + ".commits.list",
	Version: 1,
	Title:   "List GitHub commits",
	Description: "List one bounded, filtered batch of compact commits of a repository a connection " +
		"allows, newest first, without a patch",
	Tags:     []string{"github", "commits", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: inputSchema(`"ref":` + refSchema + `,"path":` + contentsPathSchema +
		`,"author":{"type":"string","maxLength":105,"pattern":"` + actorPattern + `"},"since":` + timeSchema +
		`,"until":` + timeSchema + `,` + pagingKeys),
	OutputSchema: listOutput("commits", commitEntryProperties, commitEntryRequired),
	Arguments: append([]capability.Argument{
		{Name: "ref", Description: "Return only commits reachable from this branch, tag, or commit SHA; the " +
			"default branch's tip when omitted"},
		{Name: "path", Description: "Return only commits that touch this repository path"},
		{Name: "author", Description: "Return only commits by this GitHub login"},
		{Name: "since", Description: "Return only commits at or after this commit date, as a date " +
			"(YYYY-MM-DD) or a UTC time (YYYY-MM-DDTHH:MM:SSZ)"},
		{Name: "until", Description: "Return only commits at or before this commit date, as a date or a UTC time"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "commits", Description: "Compact commits: sha, message cut to its first line and at most 200 " +
			"characters (untrusted data), author and committer as a login or, absent one, a name, the commit " +
			"date, and the number of parents; carries no patch"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the commits of main that touch one file since a date",
		Arguments:   json.RawMessage(`{"ref":"main","path":"internal/app.go","since":"2026-01-01"}`),
	}},
}

const commitFileProperties = `"path":{"type":"string"},"status":{"type":"string"},"additions":{"type":"integer"},` +
	`"deletions":{"type":"integer"},"changes":{"type":"integer"},"previous_filename":{"type":"string"},` +
	`"patch":{"type":"string"},"patch_truncated":{"type":"boolean"}`

const commitFileRequired = `"required":["path","status","additions","deletions","changes"],"additionalProperties":false`

const commitDetailProperties = `"sha":{"type":"string"},"message":{"type":"string"},` +
	`"message_truncated":{"type":"boolean"},"author":{"type":"string"},"committer":{"type":"string"},` +
	`"date":{"type":"string"},"parents":` + stringListSchema + `,"additions":{"type":"integer"},` +
	`"deletions":{"type":"integer"},"total":{"type":"integer"},` +
	`"files":{"type":"array","items":{"type":"object","properties":{` + commitFileProperties + `},` +
	commitFileRequired + `}},"files_truncated":{"type":"boolean"}`

var commitsGet = capability.Descriptor{
	ID:      Provider + ".commits.get",
	Version: 1,
	Title:   "Get a GitHub commit",
	Description: "Read one commit of a repository a connection allows by branch, tag, or commit " +
		"SHA, with its stats and its changed files, each with a patch excerpt hard bounded and the file count " +
		"hard bounded, both with the cut visible",
	Tags:        []string{"github", "commits", "get"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"ref":`+refSchema, "ref"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + commitDetailProperties + `},` +
		`"required":["sha","message","additions","deletions","total","files","files_truncated"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "ref", Description: "Branch, tag, or commit SHA", Required: true},
	},
	Fields: []capability.Field{
		{Name: "message", Description: "Full commit message, untrusted data, cut to at most 4096 characters " +
			"with message_truncated"},
		{Name: "author", Description: "Login when GitHub reports one, the commit author's name otherwise"},
		{Name: "committer", Description: "Login when GitHub reports one, the commit committer's name otherwise"},
		{Name: "date", Description: "Commit date, the one since and until filter by"},
		{Name: "parents", Description: "SHAs of the commit's parents"},
		{Name: "additions", Description: "Lines added across every file"},
		{Name: "deletions", Description: "Lines removed across every file"},
		{Name: "total", Description: "additions plus deletions"},
		{Name: "files", Description: "Changed files with path, status, additions, deletions, changes, " +
			"previous_filename for a rename, and patch cut to at most 4096 bytes with patch_truncated; patch is " +
			"untrusted data and left out for a file GitHub sends none for"},
		{Name: "files_truncated", Description: "True when the commit held at least as many files as were returned"},
	},
	Examples: []capability.Example{{
		Description: "Read one commit",
		Arguments:   json.RawMessage(`{"ref":"ebca79b1db4fcbb136e6094c13e8451428c8a6ab"}`),
	}},
}

func commitsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: commitsList, Handler: commitsHandler(commitsList.ID, checkCommitsListArguments,
			func(ctx context.Context, c *Client, a *commitArguments) (any, error) { return c.listCommits(ctx, a) })},
		{Descriptor: commitsGet, Handler: commitsHandler(commitsGet.ID, checkCommitGetArguments,
			func(ctx context.Context, c *Client, a *commitArguments) (any, error) { return c.getCommit(ctx, a) })},
	}
}

// commitArguments holds the arguments of every commit tool; the input schema of each tool admits only its
// own. sinceValue, untilValue, page, perPage, and binding are derived by the checks.
type commitArguments struct {
	Ref    string `json:"ref"`
	Path   string `json:"path"`
	Author string `json:"author"`
	Since  string `json:"since"`
	Until  string `json:"until"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	sinceValue, untilValue string
	page, perPage          int
	binding                []byte
}

// commitsHandler decodes and checks the arguments and the repository before a credential is resolved, so a
// refused request never becomes a provider call.
func commitsHandler(id string, check func(*commitArguments, target) error,
	call func(context.Context, *Client, *commitArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments commitArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func checkCommitsListArguments(a *commitArguments, bound target) error {
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	if a.Ref != "" && !validRef(a.Ref) {
		return invalidRequest("ref must be a branch, a tag, or a commit SHA")
	}
	if a.Path != "" && !validContentsPath(a.Path) {
		return invalidRequest("path must not start or end with /, and must carry no empty, \".\", or \"..\" " +
			"segment, or control character")
	}
	if a.Author != "" && !validLogin(strings.TrimSuffix(a.Author, "[bot]")) {
		return invalidRequest("author must be a GitHub login")
	}
	a.sinceValue, a.untilValue, err = sinceUntilBounds(a.Since, a.Until)
	if err != nil {
		return err
	}
	a.binding = fingerprint("commits", "list", bound.String(), a.Ref, a.Path, strings.ToLower(a.Author),
		a.sinceValue, a.untilValue)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

func checkCommitGetArguments(a *commitArguments, _ target) error {
	if !validRef(a.Ref) {
		return invalidRequest("ref must be a branch, a tag, or a commit SHA")
	}
	return nil
}

// isoInstantLayout is the ISO 8601 instant GitHub's since and until parameters require.
const isoInstantLayout = "2006-01-02T15:04:05Z"

// sinceUntilBounds validates since and until and normalizes each into the ISO 8601 instant GitHub's since
// and until parameters require: a date (time.DateOnly) becomes midnight UTC for since or the last second of
// that day for until, and an already-complete UTC time is kept as it is.
func sinceUntilBounds(since, until string) (string, string, error) {
	normalize := func(value string, endOfDay bool) (string, time.Time, error) {
		if value == "" {
			return "", time.Time{}, nil
		}
		if t, err := time.Parse(time.DateOnly, value); err == nil {
			if endOfDay {
				t = t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
			}
			return t.UTC().Format(isoInstantLayout), t, nil
		}
		if t, err := time.Parse(isoInstantLayout, value); err == nil {
			return value, t, nil
		}
		return "", time.Time{}, invalidRequest("since and until must be a date as YYYY-MM-DD or a UTC time as " +
			isoInstantLayout)
	}
	sinceValue, sinceTime, err := normalize(since, false)
	if err != nil {
		return "", "", err
	}
	untilValue, untilTime, err := normalize(until, true)
	if err != nil {
		return "", "", err
	}
	if since != "" && until != "" && sinceTime.After(untilTime) {
		return "", "", invalidRequest("since must not lie after until")
	}
	return sinceValue, untilValue, nil
}

func (a *commitArguments) query() url.Values {
	query := url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
	if a.Ref != "" {
		query.Set("sha", a.Ref)
	}
	if a.Path != "" {
		query.Set("path", a.Path)
	}
	if a.Author != "" {
		query.Set("author", a.Author)
	}
	if a.sinceValue != "" {
		query.Set("since", a.sinceValue)
	}
	if a.untilValue != "" {
		query.Set("until", a.untilValue)
	}
	return query
}

// commitIdentityJSON is the name and date GitHub's commit object carries for its author or its committer,
// distinct from the GitHub account the sibling author or committer object below names, when it names one.
type commitIdentityJSON struct {
	Name string `json:"name"`
	Date string `json:"date"`
}

// commitJSON is the REST commit object read by both github.commits.list, from which only its compact
// fields are kept, and github.commits.get, which also reads its stats and its files.
type commitJSON struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string             `json:"message"`
		Author    commitIdentityJSON `json:"author"`
		Committer commitIdentityJSON `json:"committer"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	Committer *struct {
		Login string `json:"login"`
	} `json:"committer"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
	Stats *struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Total     int `json:"total"`
	} `json:"stats"`
	Files []pullFileJSON `json:"files"`
}

func (c commitJSON) authorName() string {
	if c.Author != nil && c.Author.Login != "" {
		return c.Author.Login
	}
	return c.Commit.Author.Name
}

func (c commitJSON) committerName() string {
	if c.Committer != nil && c.Committer.Login != "" {
		return c.Committer.Login
	}
	return c.Commit.Committer.Name
}

// CommitEntry is the compact view of one commit of the list, without a patch. Message is untrusted data, cut
// to its first line and at most maxCommitSummaryLength characters.
type CommitEntry struct {
	SHA       string `json:"sha"`
	Message   string `json:"message"`
	Author    string `json:"author,omitempty"`
	Committer string `json:"committer,omitempty"`
	Date      string `json:"date,omitempty"`
	Parents   int    `json:"parents"`
}

func (c commitJSON) entry() CommitEntry {
	return CommitEntry{SHA: c.SHA, Message: commitSummary(c.Commit.Message), Author: c.authorName(),
		Committer: c.committerName(), Date: c.Commit.Committer.Date, Parents: len(c.Parents)}
}

// CommitFile is the compact view of one changed file of a commit. Patch is untrusted data, cut to
// maxPatchBytes.
type CommitFile struct {
	Path             string `json:"path"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Patch            string `json:"patch,omitempty"`
	PatchTruncated   bool   `json:"patch_truncated,omitempty"`
}

// CommitDetail is the full view of one commit, with its stats and its changed files. Message is untrusted
// data, cut to at most maxCommitMessageLength characters.
type CommitDetail struct {
	SHA              string       `json:"sha"`
	Message          string       `json:"message"`
	MessageTruncated bool         `json:"message_truncated,omitempty"`
	Author           string       `json:"author,omitempty"`
	Committer        string       `json:"committer,omitempty"`
	Date             string       `json:"date,omitempty"`
	Parents          []string     `json:"parents,omitempty"`
	Additions        int          `json:"additions"`
	Deletions        int          `json:"deletions"`
	Total            int          `json:"total"`
	Files            []CommitFile `json:"files"`
	FilesTruncated   bool         `json:"files_truncated"`
}

// detail builds the full view of the commit, bounding its message and, since GitHub's own get-commit route
// already caps the files it returns at maxCommitFiles without ever saying whether more exist, treating a
// batch that reaches the same bound as cut as well, so a silent GitHub truncation never passes as complete.
func (c commitJSON) detail() CommitDetail {
	message, truncated := cutRunes(c.Commit.Message, maxCommitMessageLength)
	parents := make([]string, 0, len(c.Parents))
	for _, parent := range c.Parents {
		if parent.SHA != "" {
			parents = append(parents, parent.SHA)
		}
	}
	d := CommitDetail{SHA: c.SHA, Message: message, MessageTruncated: truncated, Author: c.authorName(),
		Committer: c.committerName(), Date: c.Commit.Committer.Date, Parents: parents}
	if c.Stats != nil {
		d.Additions, d.Deletions, d.Total = c.Stats.Additions, c.Stats.Deletions, c.Stats.Total
	}
	files := c.Files
	if len(files) >= maxCommitFiles {
		files, d.FilesTruncated = files[:maxCommitFiles], true
	}
	d.Files = make([]CommitFile, 0, len(files))
	for _, file := range files {
		patch, patchTruncated := truncatePatch(file.Patch)
		d.Files = append(d.Files, CommitFile{Path: file.Filename, Status: file.Status, Additions: file.Additions,
			Deletions: file.Deletions, Changes: file.Changes, PreviousFilename: file.PreviousFilename,
			Patch: patch, PatchTruncated: patchTruncated})
	}
	return d
}

// commitSummary bounds a commit message to its first line and at most maxCommitSummaryLength characters, for
// the compact list; github.commits.get keeps the full message, bounded on its own.
func commitSummary(message string) string {
	line, _, _ := strings.Cut(message, "\n")
	line, _ = cutRunes(line, maxCommitSummaryLength)
	return line
}

// cutRunes bounds arbitrary untrusted text to at most max runes, cut at a rune boundary, reporting whether
// it was cut.
func cutRunes(value string, max int) (string, bool) {
	if utf8.RuneCountInString(value) <= max {
		return value, false
	}
	runes := []rune(value)
	return string(runes[:max]), true
}

// CommitList is one batch of compact commits.
type CommitList struct {
	Commits    []CommitEntry `json:"commits"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

// Permission message of the commit tools. GitHub decides on every request; the message names what such a
// request needs without claiming what the configured token holds.
const commitsReadPermission = "GitHub refused this token the commits of this repository; reading them needs " +
	"no scope for a public repository, or repo on a classic token, or Contents: read on a fine-grained token, " +
	"for a private one"

func (c *Client) listCommits(ctx context.Context, a *commitArguments) (*CommitList, error) {
	const op = "list commits"
	var raw []commitJSON
	hasNext, err := c.restPage(ctx, op, c.repoPath("commits"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, commitsReadPermission)
	}
	result := &CommitList{Commits: make([]CommitEntry, 0, len(raw))}
	for _, commit := range raw {
		if commit.SHA == "" {
			return nil, invalidEntry(op, "a commit")
		}
		result.Commits = append(result.Commits, commit.entry())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func (c *Client) getCommit(ctx context.Context, a *commitArguments) (*CommitDetail, error) {
	const op = "get commit"
	var raw commitJSON
	if err := c.rest(ctx, op, c.repoPath("commits/"+url.PathEscape(a.Ref)), &raw); err != nil {
		return nil, actionsFailure(err, commitsReadPermission)
	}
	if raw.SHA == "" {
		return nil, invalidEntry(op, "a commit")
	}
	detail := raw.detail()
	return &detail, nil
}

// commitsSubject names the commit a commits path below a repository addresses: commits/REF, without the
// check-runs and status tails github.pullrequestchecks.list already names through pullsSubject. It names
// only a ref of the characters the input schema allows, and is empty otherwise.
func commitsSubject(path string) string {
	rest, ok := strings.CutPrefix(path, "commits/")
	if !ok || rest == "" || strings.HasSuffix(rest, "/check-runs") || strings.HasSuffix(rest, "/status") {
		return ""
	}
	ref, err := url.PathUnescape(rest)
	if err != nil || !validRef(ref) {
		return ""
	}
	return "commit " + ref
}
