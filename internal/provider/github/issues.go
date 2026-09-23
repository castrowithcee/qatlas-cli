package github

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

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
		return nil, &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "GitHub does not hold this repository or does not show it to this token"}
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
	if number < 1 || number > 1000000000 {
		return nil, invalidRequest("number must be a positive issue number")
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%s", url.PathEscape(c.target.owner), url.PathEscape(c.target.repo),
		strconv.Itoa(number))
	var raw restIssueJSON
	if err := c.rest(ctx, op, path, &raw); err != nil {
		return nil, err
	}
	if raw.Number != number {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "GitHub answered with a different issue than the requested one"}
	}
	if raw.PullRequest != nil {
		return nil, &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "this number belongs to a pull request; this tool reads issues only"}
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
