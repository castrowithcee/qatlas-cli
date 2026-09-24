package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// IssueListOptions are the structured filters and the paging of the repository issue list.
type IssueListOptions struct {
	State    string   `json:"state"`
	Labels   []string `json:"labels"`
	Assignee string   `json:"assignee"`
	Limit    int      `json:"limit"`
	Cursor   string   `json:"cursor"`
}

// normalize applies the defaults and bounds of one issue list request and returns the GitHub cursor it
// continues after.
func (o *IssueListOptions) normalize(bound target) (string, error) {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return "", err
	}
	o.Limit = limit
	switch o.State {
	case "":
		o.State = "open"
	case "open", "closed", "all":
	default:
		return "", invalidRequest("state must be open, closed, or all")
	}
	if err := checkFilterList("labels", o.Labels); err != nil {
		return "", err
	}
	if o.Assignee != "" && !validLogin(o.Assignee) {
		return "", invalidRequest("assignee must be a GitHub login")
	}
	return decodeCursor(o.binding(bound), o.Cursor)
}

func (o *IssueListOptions) binding(bound target) []byte {
	return fingerprint("issues", bound.String(), o.State, folded(o.Labels), strings.ToLower(o.Assignee))
}

// issuesQuery lists issues of one repository. The GraphQL issue connection holds no pull requests, and its
// cursor pages stay stable because the order is the fixed creation order.
const issuesQuery = `query($owner:String!,$name:String!,$first:Int!,$after:String,$states:[IssueState!],` +
	`$labels:[String!],$assignee:String){repository(owner:$owner,name:$name){issues(first:$first,after:$after,` +
	`orderBy:{field:CREATED_AT,direction:DESC},filterBy:{states:$states,labels:$labels,assignee:$assignee}){` +
	`pageInfo{hasNextPage endCursor} nodes{number title state url updatedAt ` +
	`assignees(first:10){nodes{login}} labels(first:20){nodes{name}}}}}}`

// IssueList is the normalised batch of repository issues.
type IssueList struct {
	Issues     []IssueSummary `json:"issues"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}

// IssueSummary is the compact list view of one issue.
type IssueSummary struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Assignees []string `json:"assignees"`
	Labels    []string `json:"labels"`
	URL       string   `json:"url,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

type issuesPageJSON struct {
	Repository *struct {
		Issues *struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []struct {
				Number    int           `json:"number"`
				Title     string        `json:"title"`
				State     string        `json:"state"`
				URL       string        `json:"url"`
				UpdatedAt string        `json:"updatedAt"`
				Assignees countedLogins `json:"assignees"`
				Labels    countedLabels `json:"labels"`
			} `json:"nodes"`
		} `json:"issues"`
	} `json:"repository"`
}

// ListIssues reads one bounded batch of issues. See listIssues.
func (c *Client) ListIssues(ctx context.Context, options IssueListOptions) (*IssueList, error) {
	if c.target.kind != kindRepository {
		return nil, providerError("list issues", "this connection is not bound to a repository")
	}
	after, err := options.normalize(c.target)
	if err != nil {
		return nil, err
	}
	return c.listIssues(ctx, options, after)
}

// listIssues reads exactly one server page of issues of the bound repository.
func (c *Client) listIssues(ctx context.Context, options IssueListOptions, after string) (*IssueList, error) {
	const op = "list issues"
	variables := map[string]any{
		"owner": c.target.owner, "name": c.target.repo, "first": options.Limit,
		"after": nil, "states": nil, "labels": nil, "assignee": nil,
	}
	if after != "" {
		variables["after"] = after
	}
	if options.State != "all" {
		variables["states"] = []string{strings.ToUpper(options.State)}
	}
	if len(options.Labels) > 0 {
		variables["labels"] = options.Labels
	}
	if options.Assignee != "" {
		variables["assignee"] = options.Assignee
	}
	var page issuesPageJSON
	if err := c.graphql(ctx, op, issuesQuery, variables, &page); err != nil {
		return nil, err
	}
	if page.Repository == nil || page.Repository.Issues == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	result := &IssueList{Issues: make([]IssueSummary, 0, len(page.Repository.Issues.Nodes))}
	for _, node := range page.Repository.Issues.Nodes {
		if node.Number < 1 {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub returned an issue without a usable number"}
		}
		issue := IssueSummary{Number: node.Number, Title: node.Title, State: strings.ToLower(node.State),
			Assignees: []string{}, Labels: []string{}, URL: node.URL, UpdatedAt: node.UpdatedAt}
		for _, assignee := range node.Assignees.Nodes {
			issue.Assignees = append(issue.Assignees, assignee.Login)
		}
		for _, label := range node.Labels.Nodes {
			issue.Labels = append(issue.Labels, label.Name)
		}
		result.Issues = append(result.Issues, issue)
	}
	pageInfo := page.Repository.Issues.PageInfo
	if pageInfo.HasNextPage {
		if pageInfo.EndCursor == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub announced further issues without a cursor"}
		}
		result.HasMore, result.NextCursor = true, encodeCursor(options.binding(c.target), pageInfo.EndCursor)
	}
	return result, nil
}

// Issue is the stable Qatlas view of one issue with its full body.
type Issue struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	State       string   `json:"state"`
	StateReason string   `json:"state_reason,omitempty"`
	Author      string   `json:"author,omitempty"`
	Assignees   []string `json:"assignees"`
	Labels      []string `json:"labels"`
	Milestone   string   `json:"milestone,omitempty"`
	URL         string   `json:"url,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	ClosedAt    string   `json:"closed_at,omitempty"`
	Body        string   `json:"body"`
}

type restIssueJSON struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	State       string  `json:"state"`
	StateReason *string `json:"state_reason"`
	Body        *string `json:"body"`
	User        *struct {
		Login string `json:"login"`
	} `json:"user"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	NodeID      string    `json:"node_id"`
	HTMLURL     string    `json:"html_url"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
	ClosedAt    *string   `json:"closed_at"`
	PullRequest *struct{} `json:"pull_request"`
}

// GetIssue reads exactly one issue of the bound repository through the REST API, without comments. The
// REST issue route also answers for pull requests; such an answer is refused.
func (c *Client) GetIssue(ctx context.Context, number int) (*Issue, error) {
	const op = "get issue"
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	return c.readIssue(ctx, op, number)
}

// readIssue reads one issue of the bound repository and refuses a pull request. Every change of an
// existing issue and every new comment reads the issue this way first, because the REST issue routes
// would change a pull request of the same number just as well.
func (c *Client) readIssue(ctx context.Context, op string, number int) (*Issue, error) {
	if err := checkNumber(number); err != nil {
		return nil, err
	}
	var raw restIssueJSON
	if err := c.rest(ctx, op, issuePath(c.target, number), &raw); err != nil {
		return nil, err
	}
	return issueOf(op, raw, number, false)
}

func checkNumber(number int) error {
	if number < 1 || number > 1000000000 {
		return invalidRequest("number must be a positive issue number")
	}
	return nil
}

func issuesPath(repository target) string {
	return fmt.Sprintf("/repos/%s/%s/issues", url.PathEscape(repository.owner), url.PathEscape(repository.repo))
}

func issuePath(repository target, number int) string {
	return issuesPath(repository) + "/" + strconv.Itoa(number)
}

// issueOf normalises one REST issue. number is the issue that was asked for, or zero for a new one. A
// change already happened when its answer is checked, so a failure then says that its outcome is open.
func issueOf(op string, raw restIssueJSON, number int, change bool) (*Issue, error) {
	if raw.Number < 1 || (number != 0 && raw.Number != number) {
		message := "GitHub answered with a different issue than the requested one"
		if change {
			message += uncertain
		}
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	if raw.PullRequest != nil {
		return nil, &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "this number belongs to a pull request; these tools handle issues only"}
	}
	issue := &Issue{Number: raw.Number, Title: raw.Title, State: raw.State, Assignees: []string{}, Labels: []string{},
		URL: raw.HTMLURL, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt}
	if raw.StateReason != nil {
		issue.StateReason = *raw.StateReason
	}
	if raw.Body != nil {
		issue.Body = *raw.Body
	}
	if raw.User != nil {
		issue.Author = raw.User.Login
	}
	if raw.Milestone != nil {
		issue.Milestone = raw.Milestone.Title
	}
	if raw.ClosedAt != nil {
		issue.ClosedAt = *raw.ClosedAt
	}
	for _, assignee := range raw.Assignees {
		issue.Assignees = append(issue.Assignees, assignee.Login)
	}
	for _, label := range raw.Labels {
		issue.Labels = append(issue.Labels, label.Name)
	}
	return issue, nil
}

// Bounds of the issue content Qatlas writes. GitHub allows 256 characters in a title and 65536 in a body;
// the list bounds keep one request small.
const (
	maxTitleLength = 256
	maxBodyLength  = 65536
	maxLabels      = 20
	maxLabelLength = 50
	maxAssignees   = 10
)

// IssueContent is the content of a new issue, or the part of an existing issue a change replaces. A nil
// field stays as it is; Labels and Assignees replace the whole set, and an empty list removes every entry.
type IssueContent struct {
	Title     *string   `json:"title"`
	Body      *string   `json:"body"`
	Labels    *[]string `json:"labels"`
	Assignees *[]string `json:"assignees"`
}

// check applies the bounds of the issue content. A new issue needs a title; a change needs at least one
// field.
func (content IssueContent) check(create bool) error {
	switch {
	case create && content.Title == nil:
		return invalidRequest("title is required")
	case !create && content.Title == nil && content.Body == nil && content.Labels == nil && content.Assignees == nil:
		return invalidRequest("name at least one of title, body, labels, or assignees to change")
	}
	if content.Title != nil {
		if length := utf8.RuneCountInString(*content.Title); strings.TrimSpace(*content.Title) == "" ||
			length > maxTitleLength {
			return invalidRequest(fmt.Sprintf("title must hold 1 to %d characters", maxTitleLength))
		}
	}
	if content.Body != nil && utf8.RuneCountInString(*content.Body) > maxBodyLength {
		return invalidRequest(fmt.Sprintf("body must hold at most %d characters", maxBodyLength))
	}
	if content.Labels != nil {
		if len(*content.Labels) > maxLabels {
			return invalidRequest(fmt.Sprintf("labels accepts at most %d values", maxLabels))
		}
		for _, label := range *content.Labels {
			if strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > maxLabelLength {
				return invalidRequest(fmt.Sprintf("a label must hold 1 to %d characters", maxLabelLength))
			}
		}
	}
	if content.Assignees != nil {
		if len(*content.Assignees) > maxAssignees {
			return invalidRequest(fmt.Sprintf("assignees accepts at most %d values", maxAssignees))
		}
		for _, login := range *content.Assignees {
			if !validLogin(login) {
				return invalidRequest("assignees must be GitHub logins")
			}
		}
	}
	return nil
}

// payload is the REST body of the content: only the named fields travel.
func (content IssueContent) payload() map[string]any {
	payload := map[string]any{}
	if content.Title != nil {
		payload["title"] = *content.Title
	}
	if content.Body != nil {
		payload["body"] = *content.Body
	}
	if content.Labels != nil {
		payload["labels"] = append([]string{}, *content.Labels...)
	}
	if content.Assignees != nil {
		payload["assignees"] = append([]string{}, *content.Assignees...)
	}
	return payload
}

// CreateIssue opens one issue in the bound repository with exactly one request, which is never repeated.
func (c *Client) CreateIssue(ctx context.Context, content IssueContent) (*Issue, error) {
	if c.target.kind != kindRepository {
		return nil, providerError("create issue", "this connection is not bound to a repository")
	}
	issue, _, err := c.createIssue(ctx, c.target, content)
	return issue, err
}

// createIssue opens one issue in a repository of the connection and returns it with its node identifier.
func (c *Client) createIssue(ctx context.Context, repository target, content IssueContent) (*Issue, string, error) {
	const op = "create issue"
	if err := content.check(true); err != nil {
		return nil, "", err
	}
	var raw restIssueJSON
	if err := c.restChange(ctx, op, http.MethodPost, issuesPath(repository), content.payload(), &raw); err != nil {
		return nil, "", err
	}
	issue, err := issueOf(op, raw, 0, true)
	if err != nil {
		return nil, "", err
	}
	if raw.NodeID == "" {
		return nil, "", invalidResponse(op, true)
	}
	return issue, raw.NodeID, nil
}

// UpdateIssue replaces the named content of one issue of the bound repository. GitHub offers no
// precondition for an issue change, so the last change wins; the issue is read first only to refuse a
// pull request of the same number.
func (c *Client) UpdateIssue(ctx context.Context, number int, content IssueContent) (*Issue, error) {
	if err := content.check(false); err != nil {
		return nil, err
	}
	return c.changeIssue(ctx, "update issue", number, content.payload())
}

// stateReasons are the reasons GitHub records for closing an issue.
var stateReasons = []string{"completed", "not_planned", "duplicate"}

// CloseIssue closes one issue of the bound repository with a reason; closing a closed issue records the
// reason again.
func (c *Client) CloseIssue(ctx context.Context, number int, reason string) (*Issue, error) {
	if reason == "" {
		reason = "completed"
	}
	if !containsFold(stateReasons, reason) {
		return nil, invalidRequest("state_reason must be completed, not_planned, or duplicate")
	}
	return c.changeIssue(ctx, "close issue", number,
		map[string]any{"state": "closed", "state_reason": strings.ToLower(reason)})
}

// ReopenIssue opens one closed issue of the bound repository again.
func (c *Client) ReopenIssue(ctx context.Context, number int) (*Issue, error) {
	return c.changeIssue(ctx, "reopen issue", number, map[string]any{"state": "open", "state_reason": "reopened"})
}

// changeIssue reads the issue to refuse a pull request, then sends the change once.
func (c *Client) changeIssue(ctx context.Context, op string, number int, payload map[string]any) (*Issue, error) {
	if c.target.kind != kindRepository {
		return nil, providerError(op, "this connection is not bound to a repository")
	}
	if _, err := c.readIssue(ctx, op, number); err != nil {
		return nil, err
	}
	var raw restIssueJSON
	if err := c.restChange(ctx, op, http.MethodPatch, issuePath(c.target, number), payload, &raw); err != nil {
		return nil, err
	}
	return issueOf(op, raw, number, true)
}
