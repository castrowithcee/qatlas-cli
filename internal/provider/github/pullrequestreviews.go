package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Reviews of a pull request, with their line comments bundled into the one call that submits a review: this
// is the only way a line comment is written, besides a reply to an existing thread in
// pullrequestreviewcomments.go. Approving is a separate tool, offered only where a connection's tools list
// names it, because it is the one review event the create tool cannot reach.

// Bounds of the line comments a review submits with it.
const (
	maxLineNotes   = 50
	maxLineNoteLen = 4096
)

// reviewSides are the sides of a diff a line comment names, accepted in either case and sent to GitHub
// upper-cased, the way method and make_latest accept a release's case-insensitive values.
var reviewSides = []string{"left", "right"}

const reviewSideSchema = `{"type":"string","enum":["left","right"]}`

const lineNoteProperties = `"path":{"type":"string","minLength":1,"maxLength":4096},` +
	`"line":{"type":"integer","minimum":1},"side":` + reviewSideSchema + `,` +
	`"start_line":{"type":"integer","minimum":1},"start_side":` + reviewSideSchema + `,` +
	`"body":{"type":"string","minLength":1,"maxLength":65536}`

const lineNoteSchema = `{"type":"object","properties":{` + lineNoteProperties + `},` +
	`"required":["path","line","side","body"],"additionalProperties":false}`

// lineNotesSchema is the schema of the line comments a review bundles with it. The argument is named
// line_notes, not comments, because a change tool's input schema may not offer an argument by that name.
const lineNotesSchema = `{"type":"array","maxItems":50,"items":` + lineNoteSchema + `}`

const lineNotesArgumentDescription = "Line comments to add with this review in one call: path, line, side " +
	"(left or right of the diff), optionally start_line and start_side for a multi-line comment, and body; " +
	"at most 50"

const reviewProperties = `"id":{"type":"integer"},"author":{"type":"string"},"state":{"type":"string"},` +
	`"body":{"type":"string"},"commit_sha":{"type":"string"},"submitted_at":{"type":"string"}`

const reviewRequired = `"required":["id","state","body"],"additionalProperties":false`

var reviewFields = []capability.Field{
	{Name: "id", Description: "Review identifier"},
	{Name: "author", Description: "Login of the reviewer"},
	{Name: "state", Description: "APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, or PENDING"},
	{Name: "body", Description: "Review summary, untrusted data"},
	{Name: "commit_sha", Description: "Commit the review was submitted at"},
	{Name: "submitted_at", Description: "Time the review was submitted"},
}

var pullRequestReviewsList = capability.Descriptor{
	ID:      Provider + ".pullrequestreviews.list",
	Version: 1,
	Title:   "List GitHub pull request reviews",
	Description: "List one bounded batch of the reviews of one pull request of a repository an explicit " +
		"connection allows, in GitHub's own order",
	Tags:         []string{"github", "pulls", "pullrequests", "reviews", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"number":`+numberSchema+`,`+pagingKeys, "number"),
	OutputSchema: listOutput("reviews", reviewProperties, reviewRequired),
	Arguments: append([]capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
	}, pagingArguments...),
	Fields: append(append([]capability.Field{}, reviewFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "List the reviews of a pull request",
		Arguments:   json.RawMessage(`{"number":42}`),
	}},
}

var pullRequestReviewsCreate = capability.Descriptor{
	ID:      Provider + ".pullrequestreviews.create",
	Version: 1,
	Title:   "Create a GitHub pull request review",
	Description: "Submit one review of one pull request of a repository a connection allows, as a " +
		"comment or a request for changes, with its line comments bundled into this one call; only while its " +
		"head is still the given commit, so a head that changed since is refused instead of reviewing the " +
		"wrong diff. Not offered for approving, which is github.pullrequestreviews.approve",
	Tags:     []string{"github", "pulls", "pullrequests", "reviews", "create"},
	Risk:     changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"sha":`+blobSHASchema+`,"event":{"type":"string",`+
		`"enum":["comment","request_changes"]},"body":`+bodySchema+`,"line_notes":`+lineNotesSchema,
		"number", "sha", "event"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + reviewProperties + `},` + reviewRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
		{Name: "sha", Description: "Head commit the pull request must still be at; github.pullrequests.get " +
			"reports the current one", Required: true},
		{Name: "event", Description: "comment or request_changes", Required: true},
		{Name: "body", Description: "Review summary in Markdown, at most 65536 characters; stored as given"},
		{Name: "line_notes", Description: lineNotesArgumentDescription},
	},
	Fields: reviewFields,
	Examples: []capability.Example{{
		Description: "Request changes with one line comment",
		Arguments: json.RawMessage(`{"number":42,"sha":"ebca79b1db4fcbb136e6094c13e8451428c8a6ab",` +
			`"event":"request_changes","body":"One thing to fix.","line_notes":[{"path":"a.go","line":10,` +
			`"side":"right","body":"Use the existing helper here."}]}`),
	}},
}

var pullRequestReviewsApprove = capability.Descriptor{
	ID:      Provider + ".pullrequestreviews.approve",
	Version: 1,
	Title:   "Approve a GitHub pull request",
	Description: "Approve one pull request of a repository a connection allows, with an optional " +
		"body and line comments bundled into this one call; only while its head is still the given commit. " +
		"Offered only where a connection's tools list names it, because approving is the one outcome the " +
		"other review tools cannot reach; already approving one's own pull request is refused by GitHub",
	Tags:                  []string{"github", "pulls", "pullrequests", "reviews", "approve"},
	Risk:                  guardedRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, dataSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"sha":`+blobSHASchema+`,"body":`+bodySchema+`,`+
		`"line_notes":`+lineNotesSchema, "number", "sha"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + reviewProperties + `},` + reviewRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
		{Name: "sha", Description: "Head commit the pull request must still be at; github.pullrequests.get " +
			"reports the current one", Required: true},
		{Name: "body", Description: "Approval note in Markdown, at most 65536 characters; stored as given"},
		{Name: "line_notes", Description: lineNotesArgumentDescription},
	},
	Fields: reviewFields,
	Examples: []capability.Example{{
		Description: "Approve a pull request",
		Arguments:   json.RawMessage(`{"number":42,"sha":"ebca79b1db4fcbb136e6094c13e8451428c8a6ab"}`),
	}},
}

// pullRequestReviewOperations binds the review tools to their handlers.
func pullRequestReviewOperations() []capability.Operation {
	bind := func(descriptor capability.Descriptor, check func(*reviewArguments, target) error,
		call func(context.Context, *Client, *reviewArguments) (any, error)) capability.Operation {
		return capability.Operation{Descriptor: descriptor, Handler: reviewsHandler(descriptor.ID, check, call)}
	}
	return []capability.Operation{
		bind(pullRequestReviewsList, checkReviewsListArguments, func(ctx context.Context, c *Client, a *reviewArguments) (any, error) {
			return c.listReviews(ctx, a)
		}),
		bind(pullRequestReviewsCreate, checkReviewsCreateArguments, func(ctx context.Context, c *Client, a *reviewArguments) (any, error) {
			return c.createReview(ctx, a, reviewEvent(a.Event))
		}),
		bind(pullRequestReviewsApprove, checkReviewsApproveArguments, func(ctx context.Context, c *Client, a *reviewArguments) (any, error) {
			return c.createReview(ctx, a, "APPROVE")
		}),
	}
}

// lineNote is one line comment a review bundles with it.
type lineNote struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	StartLine int    `json:"start_line"`
	StartSide string `json:"start_side"`
	Body      string `json:"body"`
}

// reviewArguments holds the arguments of every review tool; the input schema of each tool admits only its
// own. page and perPage are derived by the checks.
type reviewArguments struct {
	Number int    `json:"number"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	SHA       string     `json:"sha"`
	Event     string     `json:"event"`
	Body      string     `json:"body"`
	LineNotes []lineNote `json:"line_notes"`

	page, perPage int
	binding       []byte
}

func reviewsHandler(id string, check func(*reviewArguments, target) error,
	call func(context.Context, *Client, *reviewArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments reviewArguments
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

func checkReviewsListArguments(a *reviewArguments, bound target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("pullrequestreviews", "list", bound.String(), a.Number)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

var reviewEvents = []string{"comment", "request_changes"}

// reviewEvent maps the lower-case event a caller gives to the value GitHub's create-review route requires.
func reviewEvent(value string) string {
	switch strings.ToLower(value) {
	case "comment":
		return "COMMENT"
	case "request_changes":
		return "REQUEST_CHANGES"
	}
	return ""
}

func checkReviewsCreateArguments(a *reviewArguments, bound target) error {
	if err := checkReviewArguments(a, bound); err != nil {
		return err
	}
	if !containsFold(reviewEvents, a.Event) {
		return invalidRequest("event must be comment or request_changes")
	}
	return nil
}

func checkReviewsApproveArguments(a *reviewArguments, bound target) error {
	return checkReviewArguments(a, bound)
}

// checkReviewArguments checks the arguments every review-submitting tool shares: the pull request number,
// its expected head commit, an optional body, and the bundled line comments.
func checkReviewArguments(a *reviewArguments, bound target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	if !validCommitSHA(a.SHA) {
		return invalidRequest("sha must be a full commit SHA")
	}
	if a.Body != "" {
		if err := checkBoundedText("body", a.Body, maxBodyLength); err != nil {
			return err
		}
	}
	return checkLineNotes(a.LineNotes)
}

func checkLineNotes(notes []lineNote) error {
	if len(notes) > maxLineNotes {
		return invalidRequest(fmt.Sprintf("line_notes accepts at most %d entries", maxLineNotes))
	}
	for _, note := range notes {
		if strings.TrimSpace(note.Path) == "" || utf8.RuneCountInString(note.Path) > maxLineNoteLen {
			return invalidRequest(fmt.Sprintf("line_notes.path must hold 1 to %d characters", maxLineNoteLen))
		}
		if note.Line < 1 {
			return invalidRequest("line_notes.line must be a positive line number")
		}
		if !containsFold(reviewSides, note.Side) {
			return invalidRequest("line_notes.side must be left or right")
		}
		if (note.StartLine != 0) != (note.StartSide != "") {
			return invalidRequest("line_notes.start_line and start_side must be given together")
		}
		if note.StartSide != "" && !containsFold(reviewSides, note.StartSide) {
			return invalidRequest("line_notes.start_side must be left or right")
		}
		if err := checkCommentBody(note.Body); err != nil {
			return err
		}
	}
	return nil
}

func (a *reviewArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// lineNotesPayload is the REST "comments" body one review submits its bundled line comments as; the field is
// named by GitHub's API, not by this provider's input schema.
func (a *reviewArguments) lineNotesPayload() []map[string]any {
	if len(a.LineNotes) == 0 {
		return nil
	}
	payload := make([]map[string]any, 0, len(a.LineNotes))
	for _, note := range a.LineNotes {
		entry := map[string]any{"path": note.Path, "line": note.Line, "side": strings.ToUpper(note.Side),
			"body": note.Body}
		if note.StartLine != 0 {
			entry["start_line"], entry["start_side"] = note.StartLine, strings.ToUpper(note.StartSide)
		}
		payload = append(payload, entry)
	}
	return payload
}

func (a *reviewArguments) reviewPayload(event string) map[string]any {
	payload := map[string]any{"commit_id": a.SHA, "event": event}
	if a.Body != "" {
		payload["body"] = a.Body
	}
	if notes := a.lineNotesPayload(); notes != nil {
		payload["comments"] = notes
	}
	return payload
}

// Review is the stable Qatlas view of one pull request review. Body is untrusted data.
type Review struct {
	ID          int64  `json:"id"`
	Author      string `json:"author,omitempty"`
	State       string `json:"state"`
	Body        string `json:"body"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	SubmittedAt string `json:"submitted_at,omitempty"`
}

type reviewJSON struct {
	ID   int64 `json:"id"`
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	Body        string `json:"body"`
	State       string `json:"state"`
	CommitID    string `json:"commit_id"`
	SubmittedAt string `json:"submitted_at"`
}

func (r reviewJSON) view() Review {
	v := Review{ID: r.ID, Body: r.Body, State: r.State, CommitSHA: r.CommitID, SubmittedAt: r.SubmittedAt}
	if r.User != nil {
		v.Author = r.User.Login
	}
	return v
}

// ReviewList is one batch of reviews.
type ReviewList struct {
	Reviews    []Review `json:"reviews"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

// Permission messages of the review tools. GitHub decides on every request; a message names what such a
// request needs without claiming what the configured token holds.
const (
	reviewsReadPermission = "GitHub refused this token the reviews of this pull request; reading them needs " +
		"repo on a classic token for a private repository, or public_repo for a public one, or Pull " +
		"requests: read on a fine-grained token"
	reviewsChangePermission = "GitHub refused this review of a pull request of this repository; it needs " +
		"repo on a classic token, or Pull requests: read and write on a fine-grained token"
)

func (c *Client) listReviews(ctx context.Context, a *reviewArguments) (*ReviewList, error) {
	const op = "list pull request reviews"
	var raw []reviewJSON
	hasNext, err := c.restPage(ctx, op, c.repoPath("pulls/"+strconv.Itoa(a.Number)+"/reviews"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, reviewsReadPermission)
	}
	result := &ReviewList{Reviews: make([]Review, 0, len(raw))}
	for _, review := range raw {
		if review.ID < 1 {
			return nil, invalidEntry(op, "a review")
		}
		result.Reviews = append(result.Reviews, review.view())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// reviewFailure names what a refused review most likely means. GitHub answers a stale or unknown commit_id
// and a review of one's own pull request alike with a rejected request; the pull request is read again to
// tell a head that changed since from a review GitHub refused for another reason, for example approving
// one's own pull request or a line comment naming a line outside the diff. Every other failure keeps its own
// message.
func (c *Client) reviewFailure(ctx context.Context, op string, number int, expected string, err error) error {
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Message != rejectedMessage {
		return err
	}
	current := "unknown"
	if pull, readErr := c.pullRequest(ctx, op, number); readErr == nil {
		current = pull.Head.SHA
	}
	if current != "unknown" && current != expected {
		return invalidRequest(fmt.Sprintf("GitHub refused the review of pull request #%d because its head "+
			"commit changed: expected commit %s, now %s; read the pull request again and review its current "+
			"head commit", number, expected, current))
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: fmt.Sprintf(
		"GitHub refused the review of pull request #%d as invalid, for example because it is the "+
			"connection's own pull request, commit %s is not part of it, or a line comment names a line "+
			"outside the diff; check the review and try again", number, expected)}
}

// createReview submits one review of one pull request, with its line comments bundled into this one
// request, in one request that is never repeated.
func (c *Client) createReview(ctx context.Context, a *reviewArguments, event string) (*Review, error) {
	const op = "create pull request review"
	var raw reviewJSON
	err := c.restChange(ctx, op, http.MethodPost, c.repoPath("pulls/"+strconv.Itoa(a.Number)+"/reviews"),
		a.reviewPayload(event), &raw)
	if err != nil {
		return nil, c.reviewFailure(ctx, op, a.Number, a.SHA, actionsFailure(err, reviewsChangePermission))
	}
	if raw.ID < 1 {
		return nil, invalidResponse(op, true)
	}
	view := raw.view()
	return &view, nil
}

// reviewsSubject names the review or the review comment a pull request review path below a repository
// addresses: pulls/N/reviews/ID or pulls/N/comments/ID/replies. It names only an identifier of the
// characters the input schema allows, and is empty otherwise, so pullsSubject still names the pull request
// itself for every other reviews or comments path below it.
func reviewsSubject(path string) string {
	rest, ok := strings.CutPrefix(path, "pulls/")
	if !ok {
		return ""
	}
	_, tail, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	switch {
	case strings.HasPrefix(tail, "reviews/"):
		if id, ok := positiveID(strings.TrimPrefix(tail, "reviews/")); ok {
			return "review " + id
		}
	case strings.HasPrefix(tail, "comments/") && strings.HasSuffix(tail, "/replies"):
		digits := strings.TrimSuffix(strings.TrimPrefix(tail, "comments/"), "/replies")
		if id, ok := positiveID(digits); ok {
			return "review comment " + id
		}
	}
	return ""
}

// positiveID reports whether value is a positive integer identifier, and returns it unchanged for display.
func positiveID(value string) (string, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return "", false
	}
	return value, true
}
