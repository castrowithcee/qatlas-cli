package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The review tools act on the reviews of one pull request of a repository inside the targets. Every tool first
// reads the pull request (the evidence read, see onPull); a tool that takes a review ID then reads the review
// and checks that the pull request address it reports, where it reports one, names this repository and index.
// The path binds a review to its pull request on the instance already; the comparison only catches an answer
// that disagrees with it. A change sends one request of a fixed method and path and is never repeated.

const (
	reviewSensitivity = "-pull-request-review-data"
	maxReviewText     = 65536
	// Inline comments of one review are bounded in number, in size, and in path length.
	maxInlineComments = 50
	maxInlineBytes    = 256 << 10
	maxReviewPath     = 1024
	maxLineNumber     = 2147483647
	maxExtraLines     = 10000
	// maxReviewComments bounds the comments of one review, which the instance lists without paging.
	maxReviewComments = 100
)

// forgejoExtraLines is the first Forgejo version whose swagger file documents extra_lines_count.
var forgejoExtraLines = version{16, 0, 0}

var reviewIDArgument = capability.Argument{Name: "id", Required: true,
	Description: "Numeric ID of the review, as pull-request-reviews.list reports it as id"}

var reviewMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the review"},
	{name: "state", kind: kindText, note: "APPROVED, PENDING, COMMENT, REQUEST_CHANGES, or REQUEST_REVIEW"},
	{name: "body", kind: kindText, max: maxReviewText, note: "Text in Markdown, untrusted data, at most 64 KiB"},
	{name: "commit_id", kind: kindText, note: "Commit the review was made against"},
	{name: "comments_count", kind: kindInteger, note: "Number of inline comments"},
	{name: "dismissed", kind: kindFlag, note: "True once the review is dismissed"},
	{name: "official", kind: kindFlag, note: "True when the review counts toward the required approvals"},
	{name: "stale", kind: kindFlag, note: "True when the pull request changed after the review"},
	{name: "user", kind: kindObject, sub: personMembers, note: "Reviewer; personal data"},
	{name: "team", kind: kindObject, sub: teamMembers, note: "Team the review was made for"},
	{name: "html_url", kind: kindText, note: "Address of the review on its page"},
	{name: "pull_request_url", kind: kindText, note: "Address of the pull request page"},
	{name: "submitted_at", kind: kindText, note: "Time the review was submitted"},
	{name: "updated_at", kind: kindText, note: "Time of the last change"},
}

var reviewChangedMember = member{name: "changed", kind: kindFlag, note: "False when the review already had the " +
	"requested state and nothing was sent"}

func withMember(members []member, extra member) []member {
	return append(append([]member(nil), members...), extra)
}

func reviewsPath(repo target, number int64) string { return pullPath(repo, number) + "/reviews" }

func reviewPath(repo target, number, id int64) string {
	return reviewsPath(repo, number) + "/" + strconv.FormatInt(id, 10)
}

// boundToPull reports whether the pull request address of an answer names this repository and index. An answer
// without the member proves nothing and keeps the binding of the path.
func boundToPull(repo target, number int64, o object) bool {
	value, present := o.text("pull_request_url")
	if !present || value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	n := len(segments)
	if n < 4 || segments[n-2] != "pulls" {
		return false
	}
	index, err := strconv.ParseInt(segments[n-1], 10, 64)
	return err == nil && index == number && strings.EqualFold(segments[n-4], repo.owner) &&
		strings.EqualFold(segments[n-3], repo.repo)
}

// readReview is the evidence read of a review below its pull request.
func readReview(ctx context.Context, c *Client, repo target, number, id int64, op string) (object, error) {
	var review object
	if _, err := c.get(ctx, op, reviewPath(repo, number, id), nil, &review); err != nil {
		return nil, err
	}
	if got, ok := review.integer("id"); !ok || got != id {
		return nil, provider.InvalidResponse(op, c.product.name+" returned a review without its ID")
	}
	if !boundToPull(repo, number, review) {
		return nil, invalidArgument("id is not the ID of a review of this pull request")
	}
	return review, nil
}

// onReview prepares an operation on one review of a pull request: repository, index, and ID are checked before
// anything is sent, then act runs on the evidence-read pull request and review.
func onReview(allowed allowlist, repository string, number, id int64, op string,
	act func(ctx context.Context, c *Client, repo target, review object) (any, error)) (run, error) {
	if _, err := allowed.chooseRepository(repository); err != nil {
		return nil, err
	}
	if number < 1 {
		return nil, invalidArgument("number must be the index of a pull request")
	}
	if id < 1 {
		return nil, invalidArgument("id must be the ID of a review")
	}
	return onPull(allowed, repository, number, op,
		func(ctx context.Context, c *Client, repo target, _ string, _ object) (any, error) {
			review, err := readReview(ctx, c, repo, number, id, op)
			if err != nil {
				return nil, err
			}
			return act(ctx, c, repo, review)
		})
}

// reviewRefused maps a 422 of a review change to what it means, without the instance's text in the message: the
// instance refuses an own pull request, a review that is not pending, and a review without content with the same
// status.
func reviewRefused(c *Client, op string, status int, err error) error {
	if status != http.StatusUnprocessableEntity {
		return err
	}
	return keepOriginal(&provider.Error{Class: provider.ClassProviderError, Op: op, Message: c.product.name +
		" refused the review; for example the token's user opened the pull request and cannot approve or " +
		"request changes on it, the review is not pending, or it has neither text nor comments"}, err)
}

func checkReviewText(name string, value string) error {
	if utf8.RuneCountInString(value) > maxReviewText || !utf8.ValidString(value) {
		return invalidArgument(name + " must be valid text of at most 65536 characters")
	}
	return nil
}

func checkReviewCommit(value string) error {
	if checkCommitID(value) != nil {
		return invalidArgument("commit_id must be a commit ID of 7 to 64 hexadecimal digits")
	}
	return nil
}

// checkReviewPath refuses a file path that leaves the repository tree: absolute, with a backslash, an empty
// segment, or a dot segment.
func checkReviewPath(value string) error {
	const refused = "path must be a relative file path of the repository, without . or .. segments"
	if value == "" || len(value) > maxReviewPath || !plainText(value) || strings.HasPrefix(value, "/") ||
		strings.Contains(value, `\`) {
		return invalidArgument(refused)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return invalidArgument(refused)
		}
	}
	return nil
}

// inlineComment is one comment of a new review (Forgejo: of a pending review) on a line of a file.
type inlineComment struct {
	Path       string `json:"path"`
	Body       string `json:"body"`
	NewLine    int64  `json:"new_position"`
	OldLine    int64  `json:"old_position"`
	ExtraLines *int64 `json:"extra_lines_count"`
}

// body checks a comment and builds its request member. Exactly one of new_position and old_position names the
// line, since the instance prefers the old one when both are set and a comment without a line has no place.
func (i inlineComment) body(p *product) (map[string]any, error) {
	if err := checkReviewPath(i.Path); err != nil {
		return nil, err
	}
	if i.Body == "" || checkReviewText("body", i.Body) != nil {
		return nil, invalidArgument("the body of a comment must be valid text of 1 to 65536 characters")
	}
	if i.NewLine < 0 || i.OldLine < 0 || i.NewLine > maxLineNumber || i.OldLine > maxLineNumber ||
		(i.NewLine == 0) == (i.OldLine == 0) {
		return nil, invalidArgument("a comment takes exactly one of new_position and old_position, a line " +
			"number from 1")
	}
	out := map[string]any{"path": i.Path, "body": i.Body, "new_position": i.NewLine, "old_position": i.OldLine}
	if i.ExtraLines != nil {
		if p != forgejo {
			return nil, otherProduct(p, "extra_lines_count")
		}
		if *i.ExtraLines < 0 || *i.ExtraLines > maxExtraLines {
			return nil, invalidArgument("extra_lines_count must be from 0 through 10000")
		}
		out["extra_lines_count"] = *i.ExtraLines
	}
	return out, nil
}

func inlineCommentProperties(p *product) string {
	properties := `"path":{"type":"string","minLength":1,"maxLength":1024},"body":` + commentSchemaBody +
		`,"new_position":{"type":"integer","minimum":1,"maximum":2147483647},"old_position":` +
		`{"type":"integer","minimum":1,"maximum":2147483647}`
	if p == forgejo {
		properties += `,"extra_lines_count":{"type":"integer","minimum":0,"maximum":10000}`
	}
	return properties
}

var inlineArguments = func(p *product) []capability.Argument {
	arguments := []capability.Argument{
		{Name: "path", Required: true, Description: "File path in the repository, relative, without . or .. segments"},
		{Name: "body", Required: true, Description: "Comment text in Markdown, 1 to 65536 characters"},
		{Name: "new_position", Description: "Line of the new file the comment is on; give this or old_position"},
		{Name: "old_position", Description: "Line of the old file the comment is on; give this or new_position"}}
	if p == forgejo {
		arguments = append(arguments, capability.Argument{Name: "extra_lines_count", Description: "Further " +
			"lines after the line that the comment spans; Forgejo 16 and later only"})
	}
	return arguments
}

func inlineArgumentNote(p *product) string {
	text := "Inline comments, at most 50 and 256 KiB of text in all; each takes path, body, and exactly one of " +
		"new_position and old_position"
	if p == forgejo {
		text += ", and on Forgejo 16 and later extra_lines_count"
	}
	return text
}

// reviewBody builds the body of a review from its text, commit, event, and inline comments.
func reviewBody(p *product, text, commit, event string, comments []inlineComment) (map[string]any, error) {
	body := map[string]any{}
	if text != "" {
		if err := checkReviewText("body", text); err != nil {
			return nil, err
		}
		body["body"] = text
	}
	if commit != "" {
		if err := checkReviewCommit(commit); err != nil {
			return nil, err
		}
		body["commit_id"] = commit
	}
	if event != "" {
		body["event"] = event
	}
	if len(comments) > maxInlineComments {
		return nil, invalidArgument("comments takes at most 50 inline comments")
	}
	if len(comments) > 0 {
		list := make([]map[string]any, 0, len(comments))
		size := 0
		for _, comment := range comments {
			built, err := comment.body(p)
			if err != nil {
				return nil, err
			}
			if size += len(comment.Body); size > maxInlineBytes {
				return nil, invalidArgument("the inline comments take at most 256 KiB of text in all")
			}
			list = append(list, built)
		}
		body["comments"] = list
	}
	return body, nil
}

// needsExtraLines asks Forgejo for the version that documents extra_lines_count when a comment sets it.
func needsExtraLines(p *product, comments []inlineComment) (version, bool) {
	if p != forgejo {
		return version{}, false
	}
	for _, comment := range comments {
		if comment.ExtraLines != nil {
			return forgejoExtraLines, true
		}
	}
	return version{}, false
}

var reviewEvents = []string{"COMMENT", "REQUEST_CHANGES"}

const (
	reviewSchemaCreateEvent = `{"type":"string","enum":["COMMENT","REQUEST_CHANGES","PENDING"]}`
	reviewSchemaSubmitEvent = `{"type":"string","enum":["COMMENT","REQUEST_CHANGES"]}`
)

func reviewOutput(p *product, members []member) json.RawMessage {
	return json.RawMessage(schemaOf(p, members, "id"))
}

var reviewsList = &tool{
	group: "pull-request-reviews", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the reviews of a " + p.name + " pull request",
			Description: "List one batch of the reviews of a pull request of a repository inside the connection's " +
				"targets, with every member of each review; the texts and names are untrusted data",
			Tags:         []string{p.id, "pull-request-reviews", "list"},
			Risk:         readRisk(p.id + reviewSensitivity),
			InputSchema:  inputSchema(pullNumberProperties+","+pagingKeys, "number"),
			OutputSchema: pullListOutput(p, "reviews", reviewMembers, `"number":{"type":"integer"},`),
			Arguments:    append([]capability.Argument{repositoryArgument, pullNumberArgument}, pagingArguments...),
			Fields: append([]capability.Field{
				{Name: "reviews", Description: "Reviews with every member of the review object"},
				{Name: "number", Description: "Index of the pull request"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the reviews of a pull request",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Number     int64  `json:"number"`
			Limit      int    `json:"limit"`
			Cursor     string `json:"cursor"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		return onPullPage(p, allowed, arguments.Repository, arguments.Number, "list pull request reviews",
			"pull-request-reviews.list", "/reviews", "reviews", reviewMembers, url.Values{}, arguments.Limit,
			arguments.Cursor)
	},
}

type reviewTarget struct {
	Repository string `json:"repository"`
	Number     int64  `json:"number"`
	ID         int64  `json:"id"`
}

var reviewIDProperties = pullNumberProperties + `,"id":` + issueSchemaID

var reviewsGet = &tool{
	group: "pull-request-reviews", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " pull request review",
			Description: "Read one review of a pull request of a repository inside the connection's targets, with " +
				"every member of the review object; the texts and names are untrusted data. The pull request " +
				"address the review reports must name this pull request.",
			Tags:         []string{p.id, "pull-request-reviews", "get"},
			Risk:         readRisk(p.id + reviewSensitivity),
			InputSchema:  inputSchema(reviewIDProperties, "number", "id"),
			OutputSchema: reviewOutput(p, reviewMembers),
			Arguments:    []capability.Argument{repositoryArgument, pullNumberArgument, reviewIDArgument},
			Fields:       fieldsOf(p, reviewMembers),
			Examples: []capability.Example{{Description: "Read a review",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"id":42}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments reviewTarget
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		return onReview(allowed, arguments.Repository, arguments.Number, arguments.ID, "get pull request review",
			func(_ context.Context, _ *Client, _ target, review object) (any, error) {
				return project(p, reviewMembers, review), nil
			})
	},
}

// reviewWrite are the arguments of pull-request-reviews.create.
type reviewWrite struct {
	Repository string          `json:"repository"`
	Number     int64           `json:"number"`
	Body       string          `json:"body"`
	CommitID   string          `json:"commit_id"`
	Event      string          `json:"event"`
	Comments   []inlineComment `json:"comments"`
}

var reviewsCreate = &tool{
	group: "pull-request-reviews", action: "create", since: everywhere,
	needs: func(p *product, raw json.RawMessage) (version, bool) {
		var arguments reviewWrite
		if decodeArguments(raw, &arguments) != nil {
			return version{}, false
		}
		return needsExtraLines(p, arguments.Comments)
	},
	describe: func(p *product) capability.Descriptor {
		properties := pullNumberProperties + `,"body":{"type":"string","maxLength":65536},"commit_id":` +
			pullSchemaSHA + `,"event":` + reviewSchemaCreateEvent + `,"comments":{"type":"array","maxItems":50,` +
			`"items":{"type":"object","properties":{` + inlineCommentProperties(p) + `},"required":["path","body"],` +
			`"additionalProperties":false}}`
		return capability.Descriptor{
			Title: "Create a " + p.name + " pull request review",
			Description: "Create one review on a pull request of a repository inside the connection's targets, " +
				"with a text and inline comments in one request. event is COMMENT or REQUEST_CHANGES, which " +
				"submits the review, or PENDING (also when left out), which keeps it pending for " +
				"pull-request-reviews.submit. A review that approves is created by pull-request-reviews.approve. " +
				"The instance refuses a review of the token's own pull request that requests changes. A repeated " +
				"call creates a second review.",
			Tags:         []string{p.id, "pull-request-reviews", "create"},
			Risk:         changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+reviewSensitivity),
			InputSchema:  inputSchema(properties, "number"),
			OutputSchema: reviewOutput(p, reviewMembers),
			Arguments: []capability.Argument{repositoryArgument, pullNumberArgument,
				{Name: "body", Description: "Review text in Markdown, at most 65536 characters"},
				{Name: "commit_id", Description: "Commit the review is made against, 7 to 64 hexadecimal digits; " +
					"the head of the pull request when omitted"},
				{Name: "event", Description: "COMMENT, REQUEST_CHANGES, or PENDING; PENDING when omitted"},
				{Name: "comments", Description: inlineArgumentNote(p)}},
			Fields: fieldsOf(p, reviewMembers),
			Examples: []capability.Example{{Description: "Request changes with one inline comment",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"event":"REQUEST_CHANGES",` +
					`"body":"Please fix","comments":[{"path":"main.go","body":"Nil check","new_position":12}]}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments reviewWrite
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
			return nil, err
		}
		switch arguments.Event {
		case "", "PENDING", "COMMENT", "REQUEST_CHANGES":
		default:
			return nil, invalidArgument("event must be COMMENT, REQUEST_CHANGES, or PENDING; " +
				"pull-request-reviews.approve sends APPROVED")
		}
		body, err := reviewBody(p, arguments.Body, arguments.CommitID, arguments.Event, arguments.Comments)
		if err != nil {
			return nil, err
		}
		return onPull(allowed, arguments.Repository, arguments.Number, "create pull request review",
			func(ctx context.Context, c *Client, repo target, _ string, _ object) (any, error) {
				var created object
				status, err := c.changeQuery(ctx, "create pull request review", http.MethodPost,
					reviewsPath(repo, arguments.Number), nil, body, &created)
				if err != nil {
					return nil, reviewRefused(c, "create pull request review", status, err)
				}
				return project(p, reviewMembers, created), nil
			})
	},
}

// submitPending sends the event of a pending review. The review must be pending, which the evidence read
// shows, so a review that is final is refused without a request.
func submitPending(ctx context.Context, c *Client, p *product, repo target, number, id int64, review object, op string,
	body map[string]any) (any, error) {
	if state, ok := review.text("state"); ok && state != "PENDING" {
		return nil, invalidArgument("the review is not pending; only a pending review is submitted")
	}
	var submitted object
	status, err := c.changeQuery(ctx, op, http.MethodPost, reviewPath(repo, number, id), nil, body, &submitted)
	if err != nil {
		return nil, reviewRefused(c, op, status, err)
	}
	return project(p, reviewMembers, submitted), nil
}

var reviewsSubmit = &tool{
	group: "pull-request-reviews", action: "submit", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := reviewIDProperties + `,"event":` + reviewSchemaSubmitEvent + `,"body":` +
			`{"type":"string","maxLength":65536}`
		return capability.Descriptor{
			Title: "Submit a " + p.name + " pull request review",
			Description: "Submit one pending review of a pull request of a repository inside the connection's " +
				"targets as COMMENT or REQUEST_CHANGES; it never approves (pull-request-reviews.approve does). " +
				"The review is read first, and one that is not pending is refused without a request. The " +
				"instance refuses requesting changes on the token's own pull request.",
			Tags:         []string{p.id, "pull-request-reviews", "submit", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+reviewSensitivity),
			InputSchema:  inputSchema(properties, "number", "id", "event"),
			OutputSchema: reviewOutput(p, reviewMembers),
			Arguments: []capability.Argument{repositoryArgument, pullNumberArgument, reviewIDArgument,
				{Name: "event", Required: true, Description: "COMMENT or REQUEST_CHANGES"},
				{Name: "body", Description: "Review text in Markdown, at most 65536 characters"}},
			Fields: fieldsOf(p, reviewMembers),
			Examples: []capability.Example{{Description: "Submit a pending review as a comment",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"id":42,"event":"COMMENT"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			reviewTarget
			Event string `json:"event"`
			Body  string `json:"body"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
			return nil, err
		}
		if !contains(reviewEvents, arguments.Event) {
			return nil, invalidArgument("event must be COMMENT or REQUEST_CHANGES; " +
				"pull-request-reviews.approve sends APPROVED")
		}
		body, err := reviewBody(p, arguments.Body, "", arguments.Event, nil)
		if err != nil {
			return nil, err
		}
		return onReview(allowed, arguments.Repository, arguments.Number, arguments.ID, "submit pull request review",
			func(ctx context.Context, c *Client, repo target, review object) (any, error) {
				return submitPending(ctx, c, p, repo, arguments.Number, arguments.ID, review,
					"submit pull request review", body)
			})
	},
}

var reviewsApprove = &tool{
	group: "pull-request-reviews", action: "approve", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := pullNumberProperties + `,"review_id":` + issueSchemaID + `,"body":` +
			`{"type":"string","maxLength":65536},"commit_id":` + pullSchemaSHA
		return capability.Descriptor{
			Title: "Approve a " + p.name + " pull request",
			Description: "Approve a pull request of a repository inside the connection's targets in the name of " +
				"the token's user: with review_id it submits that pending review as approved, otherwise it " +
				"creates a review that approves. This can unlock a merge. The instance refuses approving the " +
				"token's own pull request. Only a connection whose tools list names it offers this tool.",
			Tags:                  []string{p.id, "pull-request-reviews", "approve", "update"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+reviewSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(properties, "number"),
			OutputSchema:          reviewOutput(p, reviewMembers),
			Arguments: []capability.Argument{repositoryArgument, pullNumberArgument,
				{Name: "review_id", Description: "ID of a pending review to submit as approved; a new review " +
					"when omitted"},
				{Name: "body", Description: "Review text in Markdown, at most 65536 characters"},
				{Name: "commit_id", Description: "Commit the new review approves, 7 to 64 hexadecimal digits; " +
					"only without review_id; the head of the pull request when omitted"}},
			Fields: fieldsOf(p, reviewMembers),
			Examples: []capability.Example{{Description: "Approve a pull request",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"body":"Looks good"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Number     int64  `json:"number"`
			ReviewID   *int64 `json:"review_id"`
			Body       string `json:"body"`
			CommitID   string `json:"commit_id"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
			return nil, err
		}
		if arguments.ReviewID != nil && arguments.CommitID != "" {
			return nil, invalidArgument("commit_id belongs to a new review; a pending review keeps its commit")
		}
		body, err := reviewBody(p, arguments.Body, arguments.CommitID, "APPROVED", nil)
		if err != nil {
			return nil, err
		}
		const op = "approve pull request"
		if arguments.ReviewID != nil {
			id := *arguments.ReviewID
			return onReview(allowed, arguments.Repository, arguments.Number, id, op,
				func(ctx context.Context, c *Client, repo target, review object) (any, error) {
					return submitPending(ctx, c, p, repo, arguments.Number, id, review, op, body)
				})
		}
		return onPull(allowed, arguments.Repository, arguments.Number, op,
			func(ctx context.Context, c *Client, repo target, _ string, _ object) (any, error) {
				var created object
				status, err := c.changeQuery(ctx, op, http.MethodPost, reviewsPath(repo, arguments.Number), nil, body,
					&created)
				if err != nil {
					return nil, reviewRefused(c, op, status, err)
				}
				return project(p, reviewMembers, created), nil
			})
	},
}

var reviewsDelete = &tool{
	group: "pull-request-reviews", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a " + p.name + " pull request review",
			Description: "Delete one review of a pull request of a repository inside the connection's targets for " +
				"good, with its inline comments; this cannot be undone. The review is read first and one that " +
				"does not prove it belongs to the pull request is refused. Only a connection whose tools list " +
				"names it offers this tool.",
			Tags:                  []string{p.id, "pull-request-reviews", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+reviewSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(reviewIDProperties, "number", "id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},` +
				`"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, pullNumberArgument, reviewIDArgument},
			Fields: []capability.Field{{Name: "id", Description: "ID of the review"},
				{Name: "deleted", Description: "True once the instance deleted the review"}},
			Examples: []capability.Example{{Description: "Delete a review",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"id":42}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments reviewTarget
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		return onReview(allowed, arguments.Repository, arguments.Number, arguments.ID, "delete pull request review",
			func(ctx context.Context, c *Client, repo target, _ object) (any, error) {
				if err := c.change(ctx, "delete pull request review", http.MethodDelete,
					reviewPath(repo, arguments.Number, arguments.ID), nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"id": arguments.ID, "deleted": true}, nil
			})
	},
}

// dismissTool is pull-request-reviews.dismiss and undismiss: a review that already has the requested state is
// returned unchanged without a request.
func dismissTool(action, suffix string, dismiss bool) *tool {
	members := func() []member { return withMember(reviewMembers, reviewChangedMember) }
	return &tool{
		group: "pull-request-reviews", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			title, properties := "Undismiss a "+p.name+" pull request review", reviewIDProperties
			arguments := []capability.Argument{repositoryArgument, pullNumberArgument, reviewIDArgument}
			description := "Restore one dismissed review of a pull request of a repository inside the " +
				"connection's targets; a review that is not dismissed is returned unchanged and nothing is sent. " +
				"The instance allows it to users who may dismiss reviews."
			example := `{"repository":"example/project","number":7,"id":42}`
			if dismiss {
				title = "Dismiss a " + p.name + " pull request review"
				properties += `,"message":{"type":"string","minLength":1,"maxLength":65536},"priors":{"type":"boolean"}`
				arguments = append(arguments,
					capability.Argument{Name: "message", Required: true, Description: "Reason shown with the " +
						"dismissal, 1 to 65536 characters"},
					capability.Argument{Name: "priors", Description: "True dismisses also the earlier reviews of " +
						"the same reviewer"})
				description = "Dismiss one approving or rejecting review of a pull request of a repository inside " +
					"the connection's targets, with a reason; a review that is already dismissed is returned " +
					"unchanged and nothing is sent. priors extends it to the earlier reviews of the same " +
					"reviewer. The instance allows it to users who may dismiss reviews."
				example = `{"repository":"example/project","number":7,"id":42,"message":"Outdated"}`
			}
			required := []string{"number", "id"}
			if dismiss {
				required = append(required, "message")
			}
			return capability.Descriptor{
				Title: title, Description: description,
				Tags:         []string{p.id, "pull-request-reviews", action, "update"},
				Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+reviewSensitivity),
				InputSchema:  inputSchema(properties, required...),
				OutputSchema: reviewOutput(p, members()),
				Arguments:    arguments,
				Fields:       fieldsOf(p, members()),
				Examples:     []capability.Example{{Description: title, Arguments: json.RawMessage(example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments struct {
				reviewTarget
				Message string `json:"message"`
				Priors  *bool  `json:"priors"`
			}
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
				return nil, err
			}
			var body map[string]any
			if dismiss {
				if arguments.Message == "" || checkReviewText("message", arguments.Message) != nil {
					return nil, invalidArgument("message must be valid text of 1 to 65536 characters")
				}
				body = map[string]any{"message": arguments.Message}
				if arguments.Priors != nil {
					body["priors"] = *arguments.Priors
				}
			} else if arguments.Message != "" || arguments.Priors != nil {
				return nil, invalidArgument("undismiss takes no message or priors")
			}
			op := action + " pull request review"
			return onReview(allowed, arguments.Repository, arguments.Number, arguments.ID, op,
				func(ctx context.Context, c *Client, repo target, review object) (any, error) {
					if current, ok := review.flag("dismissed"); ok && current == dismiss {
						result := project(p, reviewMembers, review)
						result["changed"] = false
						return result, nil
					}
					var changed object
					var payload any // a nil map would be sent as null
					if body != nil {
						payload = body
					}
					_, err := c.changeQuery(ctx, op, http.MethodPost,
						reviewPath(repo, arguments.Number, arguments.ID)+suffix, nil, payload, &changed)
					if err != nil {
						return nil, err
					}
					result := project(p, reviewMembers, changed)
					result["changed"] = true
					return result, nil
				})
		},
	}
}

var (
	reviewsDismiss   = dismissTool("dismiss", "/dismissals", true)
	reviewsUndismiss = dismissTool("undismiss", "/undismissals", false)
)
