package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// github.comments.hide and github.comments.unhide minimize and restore one issue comment, pull request
// review, or pull request line comment of a repository. GitHub offers the change only as the GraphQL
// mutations minimizeComment and unminimizeComment, which take a node identifier. The caller never supplies
// that identifier: the tool reads the object by its REST identifier below the bound repository, checks
// that the object names this repository, and sends the node_id of that very answer. A review is read below
// its pull request, so kind review also needs pull_number, and so does kind pull-request-comment, a
// conversation comment of a pull request: it is read by the issue comment route, bound to exactly that pull
// request, and the number must name a pull request. The other kinds refuse pull_number. Like
// github.comments.update, kind issue-comment refuses a pull request conversation comment.

var (
	commentVisibilityReasons = []string{"SPAM", "ABUSE", "OFF_TOPIC", "OUTDATED", "DUPLICATE", "RESOLVED"}
)

const (
	commentVisibilityKindSchema = `"kind":{"type":"string","enum":["issue-comment","pull-request-comment","review","review-comment"]}`
	commentVisibilityNumber     = `"pull_number":{"type":"integer","minimum":1,"maximum":1000000000}`
	commentVisibilityOutput     = `{"type":"object","properties":{"kind":{"type":"string"},"id":{"type":"integer"},` +
		`"hidden":{"type":"boolean"},"reason":{"type":"string"}},"required":["kind","id","hidden"],` +
		`"additionalProperties":false}`
)

var commentVisibilityArguments = []capability.Argument{
	{Name: "kind", Description: "issue-comment for an issue comment, pull-request-comment for a conversation " +
		"comment of a pull request, review for a pull request review, review-comment for a pull request line comment", Required: true},
	{Name: "id", Description: "Numeric identifier of the object: the comment_id of an issue comment or " +
		"pull request conversation comment, the review id, or the id github.pull-request-review-comments.list reports for a line comment", Required: true},
}

var commentVisibilityPullNumber = capability.Argument{Name: "pull_number", Description: "Number of the " +
	"pull request the review or conversation comment belongs to; required for kind review and " +
	"pull-request-comment, refused for every other kind"}

var commentVisibilityFields = []capability.Field{
	{Name: "kind", Description: "Kind of the object this call acted on"},
	{Name: "id", Description: "Identifier of the object this call acted on"},
	{Name: "hidden", Description: "Whether GitHub reports the object as hidden after this call"},
	{Name: "reason", Description: "Reason GitHub reports for a hidden object, one of SPAM, ABUSE, OFF_TOPIC, " +
		"OUTDATED, DUPLICATE, RESOLVED; absent when the object is not hidden"},
}

var commentsHide = capability.Descriptor{
	ID:      Provider + ".comments.hide",
	Version: 1,
	Title:   "Hide a GitHub comment or review",
	Description: "Hide (minimize) one issue comment, pull request conversation comment, pull request review, " +
		"or pull request line comment of a repository a connection allows, with a reason; an object GitHub " +
		"does not hold in that repository, or a pull request conversation comment given as an issue comment, " +
		"is refused; hiding an object that is already hidden is " +
		"reported with its current state",
	Tags:     []string{"github", "comments", "reviews", "hide"},
	Risk:     changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: inputSchema(commentVisibilityKindSchema+`,"id":`+actionsIDSchema+`,"reason":`+
		`{"type":"string","enum":["SPAM","ABUSE","OFF_TOPIC","OUTDATED","DUPLICATE","RESOLVED"]},`+
		commentVisibilityNumber, "kind", "id", "reason"),
	OutputSchema: json.RawMessage(commentVisibilityOutput),
	Arguments: append(append([]capability.Argument{}, commentVisibilityArguments...),
		commentVisibilityPullNumber,
		capability.Argument{Name: "reason", Description: "SPAM, ABUSE, OFF_TOPIC, OUTDATED, DUPLICATE, or " +
			"RESOLVED", Required: true}),
	Fields: commentVisibilityFields,
	Examples: []capability.Example{{
		Description: "Hide an issue comment as off topic",
		Arguments:   json.RawMessage(`{"kind":"issue-comment","id":123456789,"reason":"OFF_TOPIC"}`),
	}, {
		Description: "Hide a pull request conversation comment as spam",
		Arguments:   json.RawMessage(`{"kind":"pull-request-comment","pull_number":7,"id":123456789,"reason":"SPAM"}`),
	}, {
		Description: "Hide a review as outdated",
		Arguments:   json.RawMessage(`{"kind":"review","pull_number":7,"id":987654321,"reason":"OUTDATED"}`),
	}},
}

var commentsUnhide = capability.Descriptor{
	ID:      Provider + ".comments.unhide",
	Version: 1,
	Title:   "Show a hidden GitHub comment or review again",
	Description: "Show one hidden issue comment, pull request conversation comment, pull request review, " +
		"or pull request line comment of a repository a connection allows again; an object GitHub does not " +
		"hold in that repository, or a pull request conversation comment given as an issue comment, is refused; showing an object that is not hidden is reported with its " +
		"current state",
	Tags:     []string{"github", "comments", "reviews", "unhide"},
	Risk:     changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: inputSchema(commentVisibilityKindSchema+`,"id":`+actionsIDSchema+`,`+commentVisibilityNumber,
		"kind", "id"),
	OutputSchema: json.RawMessage(commentVisibilityOutput),
	Arguments: append(append([]capability.Argument{}, commentVisibilityArguments...),
		commentVisibilityPullNumber),
	Fields: commentVisibilityFields,
	Examples: []capability.Example{{
		Description: "Show a hidden issue comment again",
		Arguments:   json.RawMessage(`{"kind":"issue-comment","id":123456789}`),
	}},
}

// CommentVisibility is the answer of github.comments.hide and github.comments.unhide: the state GitHub
// reports after the change.
type CommentVisibility struct {
	Kind   string `json:"kind"`
	ID     int64  `json:"id"`
	Hidden bool   `json:"hidden"`
	Reason string `json:"reason,omitempty"`
}

type commentVisibilityArgs struct {
	Kind       string `json:"kind"`
	ID         int64  `json:"id"`
	Reason     string `json:"reason"`
	PullNumber int    `json:"pull_number"`
}

func checkCommentVisibility(a *commentVisibilityArgs, hide bool) error {
	switch a.Kind {
	case "issue-comment", "pull-request-comment", "review", "review-comment":
	default:
		return invalidRequest("kind must be issue-comment, pull-request-comment, review, or review-comment")
	}
	if a.ID < 1 {
		return invalidRequest("id must be a positive numeric identifier")
	}
	if a.Kind == "review" || a.Kind == "pull-request-comment" {
		if a.PullNumber < 1 || a.PullNumber > 1000000000 {
			return invalidRequest("pull_number must be a positive pull request number for kind review and " +
				"pull-request-comment")
		}
	} else if a.PullNumber != 0 {
		return invalidRequest("pull_number is only allowed for kind review and pull-request-comment")
	}
	if !hide {
		if a.Reason != "" {
			return invalidRequest("reason is only allowed when hiding")
		}
		return nil
	}
	for _, reason := range commentVisibilityReasons {
		if a.Reason == reason {
			return nil
		}
	}
	return invalidRequest("reason must be one of SPAM, ABUSE, OFF_TOPIC, OUTDATED, DUPLICATE, RESOLVED")
}

// commentVisibilityHandler decodes and checks the arguments and the repository before a credential is
// resolved, so a refused request never becomes a provider call.
func commentVisibilityHandler(id string, hide bool) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments commentVisibilityArgs
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := checkCommentVisibility(&arguments, hide); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(client.setCommentVisibility(ctx, &arguments, hide))
	}
}

func commentVisibilityOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: commentsHide, Handler: commentVisibilityHandler(commentsHide.ID, true)},
		{Descriptor: commentsUnhide, Handler: commentVisibilityHandler(commentsUnhide.ID, false)},
	}
}

// Permission message of both tools; GitHub keeps comments and reviews under issues and pull requests, so the
// scopes are those of the object that is hidden.
const commentVisibilityPermission = "GitHub refused this change of a comment or review; it needs repo on a " +
	"classic token, or Issues: read and write for an issue comment, or Pull requests: read and write for a " +
	"review or a line comment, on a fine-grained token"

const (
	minimizeCommentMutation = `mutation($id:ID!,$classifier:ReportedContentClassifiers!){` +
		`result:minimizeComment(input:{subjectId:$id,classifier:$classifier}){` +
		`minimizedComment{isMinimized minimizedReason}}}`
	unminimizeCommentMutation = `mutation($id:ID!){result:unminimizeComment(input:{subjectId:$id}){` +
		`unminimizedComment{isMinimized minimizedReason}}}`
)

// repoObjectNumber reads the number below kind of an API address of the form .../repos/OWNER/REPO/KIND/NUMBER
// and reports whether it names the bound repository. The comparison is case-insensitive since GitHub does not
// distinguish case in names. Nothing at the address is ever requested.
func (c *Client) repoObjectNumber(apiURL, kind string) (int, bool) {
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return 0, false
	}
	idx := strings.LastIndex(parsed.Path, "/repos/")
	if idx < 0 {
		return 0, false
	}
	parts := strings.Split(parsed.Path[idx+len("/repos/"):], "/")
	if len(parts) != 4 || !strings.EqualFold(parts[0], c.target.owner) ||
		!strings.EqualFold(parts[1], c.target.repo) || parts[2] != kind {
		return 0, false
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 {
		return 0, false
	}
	return number, true
}

// foreignObject is the refusal of an object that does not name the bound repository, whatever GitHub's
// route answered; nothing is changed.
func foreignObject(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "GitHub does not hold this object in the repository the connection allows"}
}

// visibilityNode reads the object by its REST route below the bound repository and returns its node
// identifier, only after the object proved to belong to that repository.
func (c *Client) visibilityNode(ctx context.Context, op string, a *commentVisibilityArgs) (string, error) {
	id := strconv.FormatInt(a.ID, 10)
	switch a.Kind {
	case "issue-comment":
		var raw restCommentDetailJSON
		if err := c.rest(ctx, op, c.commentPath(a.ID), &raw); err != nil {
			return "", actionsFailure(err, commentVisibilityPermission)
		}
		if raw.ID != a.ID || raw.NodeID == "" {
			return "", invalidEntry(op, "a comment")
		}
		number, ok := c.repoObjectNumber(raw.IssueURL, "issues")
		if !ok {
			return "", foreignObject(op)
		}
		if _, err := c.readIssue(ctx, op, number); err != nil {
			var invalid *provider.InvalidRequestError
			if errors.As(err, &invalid) && strings.Contains(invalid.Message, "is a pull request") {
				return "", invalidRequest(invalid.Message + "; use kind pull-request-comment for its conversation comments")
			}
			return "", err
		}
		return raw.NodeID, nil
	case "pull-request-comment":
		var raw restCommentDetailJSON
		if err := c.rest(ctx, op, c.commentPath(a.ID), &raw); err != nil {
			return "", actionsFailure(err, commentVisibilityPermission)
		}
		if raw.ID != a.ID || raw.NodeID == "" {
			return "", invalidEntry(op, "a comment")
		}
		if number, ok := c.repoObjectNumber(raw.IssueURL, "issues"); !ok || number != a.PullNumber {
			return "", foreignObject(op)
		}
		if err := c.checkPullRequestForComment(ctx, op, a.PullNumber); err != nil {
			return "", err
		}
		return raw.NodeID, nil
	case "review":
		var raw struct {
			ID             int64  `json:"id"`
			NodeID         string `json:"node_id"`
			PullRequestURL string `json:"pull_request_url"`
		}
		path := c.repoPath("pulls/" + strconv.Itoa(a.PullNumber) + "/reviews/" + id)
		if err := c.rest(ctx, op, path, &raw); err != nil {
			return "", actionsFailure(err, commentVisibilityPermission)
		}
		if raw.ID != a.ID || raw.NodeID == "" {
			return "", invalidEntry(op, "a review")
		}
		if number, ok := c.repoObjectNumber(raw.PullRequestURL, "pulls"); !ok || number != a.PullNumber {
			return "", foreignObject(op)
		}
		return raw.NodeID, nil
	default:
		var raw struct {
			ID             int64  `json:"id"`
			NodeID         string `json:"node_id"`
			PullRequestURL string `json:"pull_request_url"`
		}
		if err := c.rest(ctx, op, c.repoPath("pulls/comments/"+id), &raw); err != nil {
			return "", actionsFailure(err, commentVisibilityPermission)
		}
		if raw.ID != a.ID || raw.NodeID == "" {
			return "", invalidEntry(op, "a line comment")
		}
		if _, ok := c.repoObjectNumber(raw.PullRequestURL, "pulls"); !ok {
			return "", foreignObject(op)
		}
		return raw.NodeID, nil
	}
}

// visibilityReason maps the minimizedReason GitHub reports, such as "off-topic", to the classifier name the
// tool accepts, and drops any other text.
func visibilityReason(reported string) string {
	name := strings.ToUpper(strings.ReplaceAll(reported, "-", "_"))
	for _, reason := range commentVisibilityReasons {
		if name == reason {
			return reason
		}
	}
	return ""
}

// setCommentVisibility binds the object to the repository by reading it, then sends exactly one GraphQL
// mutation with its node identifier, which is never repeated. GitHub answers a repeated hide or unhide with
// the current state, so both directions are idempotent without a further read.
func (c *Client) setCommentVisibility(ctx context.Context, a *commentVisibilityArgs, hide bool) (*CommentVisibility, error) {
	op := "unhide comment"
	if hide {
		op = "hide comment"
	}
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	nodeID, err := c.visibilityNode(ctx, op, a)
	if err != nil {
		return nil, err
	}
	document, variables := unminimizeCommentMutation, map[string]any{"id": nodeID}
	if hide {
		document, variables = minimizeCommentMutation, map[string]any{"id": nodeID, "classifier": a.Reason}
	}
	var answer struct {
		Result *struct {
			Minimized   *commentMinimizedState `json:"minimizedComment"`
			Unminimized *commentMinimizedState `json:"unminimizedComment"`
		} `json:"result"`
	}
	if err := c.mutate(ctx, op, document, variables, &answer); err != nil {
		return nil, actionsFailure(err, commentVisibilityPermission)
	}
	var state *commentMinimizedState
	if answer.Result != nil {
		state = answer.Result.Minimized
		if !hide {
			state = answer.Result.Unminimized
		}
	}
	if state == nil || state.IsMinimized == nil {
		return nil, invalidResponse(op, true)
	}
	result := &CommentVisibility{Kind: a.Kind, ID: a.ID, Hidden: *state.IsMinimized}
	if result.Hidden {
		result.Reason = visibilityReason(state.MinimizedReason)
	}
	return result, nil
}

type commentMinimizedState struct {
	IsMinimized     *bool  `json:"isMinimized"`
	MinimizedReason string `json:"minimizedReason"`
}
