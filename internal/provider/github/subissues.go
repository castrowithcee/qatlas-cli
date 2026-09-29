package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Sub-issues of an issue of a repository an explicit connection allows: github.subissues.list reads the
// sub-issues of one issue in their priority order; github.subissues.add and github.subissues.reprioritize
// change them and need their own confirmation; github.subissues.remove detaches one and needs its own
// confirmation as well, but, unlike removing a label or a ruleset, touches only the one relationship between
// the two named issues and deletes neither, so it is not listed-only. A sub-issue is always named by its
// issue number, in the bound repository unless sub_issue_repository names another one; that other repository
// must lie inside the connection's targets as well, checked before any credential is resolved, since a
// connection's targets are its access boundary and GitHub allows a sub-issue relationship to cross
// repositories and organizations. Every mutation resolves the numbers it is given into the internal issue
// identifiers the REST routes need, in the same request budget as the change itself.
//
// Verified 2026-09-29 against https://docs.github.com/en/rest/issues/sub-issues (REST API, X-GitHub-Api-Version
// 2022-11-28): List sub-issues, Add a sub-issue, Remove a sub-issue, and Reprioritize a sub-issue are stable,
// generally available REST endpoints (sub-issues reached general availability on 2025-04-09 and the REST
// surface shipped in December 2024, per the GitHub changelog), not a preview feature.

// subIssuesReadPermission and subIssuesChangePermission are the permission messages of the sub-issue tools.
// GitHub decides on every request; a message names what such a request needs without claiming what the
// configured token holds.
const (
	subIssuesReadPermission = "GitHub refused this token the sub-issues of this issue; reading them needs no " +
		"scope for a public repository, or repo on a classic token, or Issues: read on a fine-grained token, " +
		"for a private one"
	subIssuesChangePermission = "GitHub refused this change of the sub-issues of this issue; it needs repo on " +
		"a classic token, or Issues: read and write on a fine-grained token, of every repository involved"
)

// SubIssueRef names one issue a sub-issue or an issue dependency tool reports: its number, its repository as
// OWNER/REPO when it differs from the repository the request itself named, its title and state as untrusted
// data, and its web address.
type SubIssueRef struct {
	Number     int    `json:"number"`
	Repository string `json:"repository,omitempty"`
	Title      string `json:"title,omitempty"`
	State      string `json:"state,omitempty"`
	URL        string `json:"url,omitempty"`
}

const issueRefProperties = `"number":{"type":"integer"},"repository":{"type":"string"},` +
	`"title":{"type":"string"},"state":{"type":"string"},"url":{"type":"string"}`
const issueRefRequired = `"required":["number"],"additionalProperties":false`
const issueRefOutput = `{"type":"object","properties":{` + issueRefProperties + `},` + issueRefRequired + `}`

const subIssuesSummaryOutput = `{"type":"object","properties":{"total":{"type":"integer"},` +
	`"completed":{"type":"integer"},"percent_completed":{"type":"integer"}},` +
	`"required":["total","completed","percent_completed"],"additionalProperties":false}`

// SubIssuesSummary is the count of sub-issues GitHub reports alongside a parent issue after a change.
type SubIssuesSummary struct {
	Total            int `json:"total"`
	Completed        int `json:"completed"`
	PercentCompleted int `json:"percent_completed"`
}

// subIssueJSON is the REST issue shape the sub-issue and issue dependency routes answer with: only the
// fields Qatlas normalises into a SubIssueRef.
type subIssueJSON struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	State      string `json:"state"`
	HTMLURL    string `json:"html_url"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// ref normalises one REST issue answer of the sub-issue or issue dependency routes into a SubIssueRef. own is
// the repository the request was addressed to; the reported repository is left out when GitHub's answer
// names it exactly, and reported in full otherwise, since GitHub allows both relationships to cross
// repositories and organizations.
func (raw subIssueJSON) ref(op string, own target) (SubIssueRef, error) {
	if raw.Number < 1 {
		return SubIssueRef{}, invalidEntry(op, "an issue")
	}
	item := SubIssueRef{Number: raw.Number, Title: raw.Title, State: raw.State, URL: raw.HTMLURL}
	if raw.Repository != nil && raw.Repository.FullName != "" &&
		!strings.EqualFold(raw.Repository.FullName, own.owner+"/"+own.repo) {
		item.Repository = raw.Repository.FullName
	}
	return item, nil
}

// repoTarget returns the repository target one REST issue answer belongs to: own when GitHub's answer
// leaves the repository out or names it exactly, otherwise the target parsed from its full_name. A
// malformed full_name yields a target that matches no allowlist entry, so it is withheld rather than shown
// as own.
func (raw subIssueJSON) repoTarget(own target) target {
	if raw.Repository == nil || raw.Repository.FullName == "" ||
		strings.EqualFold(raw.Repository.FullName, own.owner+"/"+own.repo) {
		return own
	}
	owner, repo, _ := strings.Cut(raw.Repository.FullName, "/")
	return target{kind: kindRepository, owner: owner, repo: repo}
}

// subIssueParentJSON is the parent issue the add, remove, and reprioritize routes answer with, carrying its
// updated sub-issue count.
type subIssueParentJSON struct {
	Number  int `json:"number"`
	Summary *struct {
		Total            int `json:"total"`
		Completed        int `json:"completed"`
		PercentCompleted int `json:"percent_completed"`
	} `json:"sub_issues_summary"`
}

func (raw subIssueParentJSON) summary() *SubIssuesSummary {
	if raw.Summary == nil {
		return nil
	}
	return &SubIssuesSummary{Total: raw.Summary.Total, Completed: raw.Summary.Completed,
		PercentCompleted: raw.Summary.PercentCompleted}
}

var subIssuesList = capability.Descriptor{
	ID:      Provider + ".subissues.list",
	Version: 1,
	Title:   "List GitHub sub-issues",
	Description: "List one bounded batch of the sub-issues of one issue of a repository an explicit " +
		"connection allows, in their priority order; a sub-issue may live in another repository, named in " +
		"full when it does; when the connection's targets name any, a sub-issue outside them is withheld " +
		"and counted in withheld instead of being listed",
	Tags:                       []string{"github", "issues", "subissues", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema+`,`+pagingKeys, "number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"sub_issues":{"type":"array","items":` +
		`{"type":"object","properties":{` + issueRefProperties + `},` + issueRefRequired + `}},` +
		`"withheld":{"type":"integer"},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["sub_issues","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "number", Description: "Parent issue number in the repository", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "sub_issues", Description: "Sub-issues in their priority order, untrusted data"},
		{Name: "withheld", Description: "Count of sub-issues in this batch whose repository lies outside " +
			"the connection's targets, omitted when none were withheld"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the sub-issues of one issue",
		Arguments:   json.RawMessage(`{"number":42}`),
	}},
}

var subIssuesAdd = capability.Descriptor{
	ID:      Provider + ".subissues.add",
	Version: 1,
	Title:   "Add a GitHub sub-issue",
	Description: "Add one issue as a sub-issue of another issue of a repository an explicit connection " +
		"allows; the sub-issue may live in another repository named by sub_issue_repository, which must lie " +
		"inside the connection's targets as well; refused when it is already a sub-issue of this or another " +
		"issue, or would create a cycle, so a repeated call changes nothing further",
	Tags:                       []string{"github", "issues", "subissues", "add"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"sub_issue_number":`+numberSchema+
		`,"sub_issue_repository":`+repoSchema+`,"replace_parent":{"type":"boolean"}`, "number", "sub_issue_number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"sub_issue":` + issueRefOutput + `,"sub_issues_summary":` + subIssuesSummaryOutput + `},` +
		`"required":["number","sub_issue"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Parent issue number in the repository", Required: true},
		{Name: "sub_issue_number", Description: "Issue number to add as a sub-issue", Required: true},
		{Name: "sub_issue_repository", Description: "Repository of the sub-issue as OWNER/REPO, when it " +
			"differs from the parent's repository; must lie inside the connection's targets"},
		{Name: "replace_parent", Description: "true to move the sub-issue from its current parent to this one"},
	},
	Fields: []capability.Field{
		{Name: "sub_issue", Description: "The added sub-issue, by number and, when it differs, repository"},
		{Name: "sub_issues_summary", Description: "Total, completed, and percent_completed sub-issues of the " +
			"parent after the change"},
	},
	Examples: []capability.Example{{
		Description: "Add issue 43 as a sub-issue of issue 42",
		Arguments:   json.RawMessage(`{"number":42,"sub_issue_number":43}`),
	}},
}

var subIssuesRemove = capability.Descriptor{
	ID:      Provider + ".subissues.remove",
	Version: 1,
	Title:   "Remove a GitHub sub-issue",
	Description: "Detach one sub-issue from its parent issue of a repository an explicit connection allows; " +
		"this only removes the relationship and deletes neither issue, so it is not listed-only the way " +
		"removing a label is",
	Tags:                       []string{"github", "issues", "subissues", "remove"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"sub_issue_number":`+numberSchema+
		`,"sub_issue_repository":`+repoSchema, "number", "sub_issue_number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"sub_issue":` + issueRefOutput + `,"removed":{"type":"boolean"},"sub_issues_summary":` +
		subIssuesSummaryOutput + `},"required":["number","sub_issue","removed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Parent issue number to detach the sub-issue from", Required: true},
		{Name: "sub_issue_number", Description: "Issue number of the sub-issue to detach", Required: true},
		{Name: "sub_issue_repository", Description: "Repository of the sub-issue as OWNER/REPO, when it " +
			"differs from the parent's repository"},
	},
	Fields: []capability.Field{
		{Name: "sub_issue", Description: "The sub-issue that was detached"},
		{Name: "removed", Description: "True once GitHub detached the sub-issue"},
		{Name: "sub_issues_summary", Description: "Total, completed, and percent_completed sub-issues of the " +
			"parent after the change"},
	},
	Examples: []capability.Example{{
		Description: "Detach sub-issue 43 from its parent 42",
		Arguments:   json.RawMessage(`{"number":42,"sub_issue_number":43}`),
	}},
}

var subIssuesReprioritize = capability.Descriptor{
	ID:      Provider + ".subissues.reprioritize",
	Version: 1,
	Title:   "Reorder a GitHub sub-issue",
	Description: "Move one sub-issue of a repository an explicit connection allows to just after or just " +
		"before another sub-issue of the same parent; the reference sub-issue is looked up in the same " +
		"repository as the sub-issue being moved",
	Tags:                       []string{"github", "issues", "subissues", "reprioritize"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"sub_issue_number":`+numberSchema+
		`,"sub_issue_repository":`+repoSchema+`,"after_number":`+numberSchema+`,"before_number":`+numberSchema,
		"number", "sub_issue_number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"sub_issue":` + issueRefOutput + `},"required":["number","sub_issue"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Parent issue number", Required: true},
		{Name: "sub_issue_number", Description: "Issue number of the sub-issue to move", Required: true},
		{Name: "sub_issue_repository", Description: "Repository of the sub-issue as OWNER/REPO, when it " +
			"differs from the parent's repository"},
		{Name: "after_number", Description: "Place the sub-issue just after this sibling sub-issue, in the " +
			"same repository as sub_issue_number; give exactly one of after_number or before_number"},
		{Name: "before_number", Description: "Place the sub-issue just before this sibling sub-issue, in the " +
			"same repository as sub_issue_number; give exactly one of after_number or before_number"},
	},
	Fields: []capability.Field{
		{Name: "sub_issue", Description: "The sub-issue that was moved"},
	},
	Examples: []capability.Example{{
		Description: "Move sub-issue 44 to just after sub-issue 43",
		Arguments:   json.RawMessage(`{"number":42,"sub_issue_number":44,"after_number":43}`),
	}},
}

// subIssuesArguments holds the arguments of every sub-issue tool; the input schema of each tool admits only
// its own. page, perPage, and binding are derived by the checks.
type subIssuesArguments struct {
	Number             int    `json:"number"`
	SubIssueNumber     int    `json:"sub_issue_number"`
	SubIssueRepository string `json:"sub_issue_repository"`
	ReplaceParent      *bool  `json:"replace_parent"`
	AfterNumber        *int   `json:"after_number"`
	BeforeNumber       *int   `json:"before_number"`
	Limit              int    `json:"limit"`
	Cursor             string `json:"cursor"`

	page, perPage int
	binding       []byte
	allowed       allowlist
}

func (a *subIssuesArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// subIssuesHandler decodes and checks the arguments, the bound parent repository, and, where withSubRepo is
// true, the sub-issue's own repository, before a credential is resolved, so a repository outside the
// connection's targets is refused before any secret access.
func subIssuesHandler(id string, withSubRepo bool, check func(*subIssuesArguments, target) error,
	call func(context.Context, *Client, target, *subIssuesArguments, target) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments subIssuesArguments
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
		arguments.allowed = allowed
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		subRepo := bound
		if withSubRepo {
			subRepo, err = selectSecondaryRepository(resolved, bound, arguments.SubIssueRepository)
			if err != nil {
				return nil, err
			}
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, bound, &arguments, subRepo))
	}
}

func checkSubIssuesList(a *subIssuesArguments, bound target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("subissues", "list", bound.String(), a.Number)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

// checkSubIssue applies the bounds shared by github.subissues.add and github.subissues.remove.
func checkSubIssue(a *subIssuesArguments, _ target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	return checkNumber(a.SubIssueNumber)
}

func checkSubIssueReprioritize(a *subIssuesArguments, bound target) error {
	if err := checkSubIssue(a, bound); err != nil {
		return err
	}
	switch {
	case a.AfterNumber == nil && a.BeforeNumber == nil:
		return invalidRequest("give after_number or before_number to place the sub-issue")
	case a.AfterNumber != nil && a.BeforeNumber != nil:
		return invalidRequest("give only one of after_number or before_number")
	}
	if a.AfterNumber != nil {
		if err := checkNumber(*a.AfterNumber); err != nil {
			return err
		}
	}
	if a.BeforeNumber != nil {
		if err := checkNumber(*a.BeforeNumber); err != nil {
			return err
		}
	}
	return nil
}

// subIssuesOperations binds every sub-issue tool to its handler.
func subIssuesOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: subIssuesList, Handler: subIssuesHandler(subIssuesList.ID, false, checkSubIssuesList,
			func(ctx context.Context, c *Client, bound target, a *subIssuesArguments, _ target) (any, error) {
				return c.listSubIssues(ctx, bound, a)
			})},
		{Descriptor: subIssuesAdd, Handler: subIssuesHandler(subIssuesAdd.ID, true, checkSubIssue,
			func(ctx context.Context, c *Client, bound target, a *subIssuesArguments, subRepo target) (any, error) {
				return c.addSubIssue(ctx, bound, a, subRepo)
			})},
		{Descriptor: subIssuesRemove, Handler: subIssuesHandler(subIssuesRemove.ID, true, checkSubIssue,
			func(ctx context.Context, c *Client, bound target, a *subIssuesArguments, subRepo target) (any, error) {
				return c.removeSubIssue(ctx, bound, a, subRepo)
			})},
		{Descriptor: subIssuesReprioritize, Handler: subIssuesHandler(subIssuesReprioritize.ID, true,
			checkSubIssueReprioritize,
			func(ctx context.Context, c *Client, bound target, a *subIssuesArguments, subRepo target) (any, error) {
				return c.reprioritizeSubIssue(ctx, bound, a, subRepo)
			})},
	}
}

func subIssuesPath(repo target, number int) string {
	return issuePath(repo, number) + "/sub_issues"
}

func subIssuePath(repo target, number int) string {
	return issuePath(repo, number) + "/sub_issue"
}

func subIssuePriorityPath(repo target, number int) string {
	return issuePath(repo, number) + "/sub_issues/priority"
}

// SubIssueList is one batch of sub-issues.
type SubIssueList struct {
	SubIssues  []SubIssueRef `json:"sub_issues"`
	Withheld   int           `json:"withheld,omitempty"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

func (c *Client) listSubIssues(ctx context.Context, repo target, a *subIssuesArguments) (*SubIssueList, error) {
	const op = "list sub-issues"
	var raw []subIssueJSON
	hasNext, err := c.restPage(ctx, op, subIssuesPath(repo, a.Number), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, subIssuesReadPermission)
	}
	result := &SubIssueList{SubIssues: make([]SubIssueRef, 0, len(raw))}
	for _, item := range raw {
		if !a.allowed.allows(item.repoTarget(repo)) {
			result.Withheld++
			continue
		}
		ref, err := item.ref(op, repo)
		if err != nil {
			return nil, err
		}
		result.SubIssues = append(result.SubIssues, ref)
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// SubIssueChange confirms a change to one sub-issue relationship: the sub-issue it named, whether the change
// applies (Removed, absent from an add or a reprioritize), and the parent's sub-issue count where GitHub
// reported it.
type SubIssueChange struct {
	Number   int               `json:"number"`
	SubIssue SubIssueRef       `json:"sub_issue"`
	Removed  *bool             `json:"removed,omitempty"`
	Summary  *SubIssuesSummary `json:"sub_issues_summary,omitempty"`
}

func (c *Client) addSubIssue(ctx context.Context, parent target, a *subIssuesArguments,
	subRepo target) (*SubIssueChange, error) {
	const op = "add sub-issue"
	subID, err := c.issueDatabaseID(ctx, op, subRepo, a.SubIssueNumber)
	if err != nil {
		return nil, actionsFailure(err, subIssuesReadPermission)
	}
	body := map[string]any{"sub_issue_id": subID}
	if a.ReplaceParent != nil {
		body["replace_parent"] = *a.ReplaceParent
	}
	var raw subIssueParentJSON
	if err := c.restChange(ctx, op, http.MethodPost, subIssuesPath(parent, a.Number), body, &raw); err != nil {
		return nil, actionsFailure(err, subIssuesChangePermission)
	}
	return &SubIssueChange{Number: a.Number, SubIssue: subIssueRef(a.SubIssueNumber, subRepo, parent),
		Summary: raw.summary()}, nil
}

func (c *Client) removeSubIssue(ctx context.Context, parent target, a *subIssuesArguments,
	subRepo target) (*SubIssueChange, error) {
	const op = "remove sub-issue"
	subID, err := c.issueDatabaseID(ctx, op, subRepo, a.SubIssueNumber)
	if err != nil {
		return nil, actionsFailure(err, subIssuesReadPermission)
	}
	var raw subIssueParentJSON
	if err := c.restChange(ctx, op, http.MethodDelete, subIssuePath(parent, a.Number),
		map[string]any{"sub_issue_id": subID}, &raw); err != nil {
		return nil, actionsFailure(err, subIssuesChangePermission)
	}
	removed := true
	return &SubIssueChange{Number: a.Number, SubIssue: subIssueRef(a.SubIssueNumber, subRepo, parent),
		Removed: &removed, Summary: raw.summary()}, nil
}

func (c *Client) reprioritizeSubIssue(ctx context.Context, parent target, a *subIssuesArguments,
	subRepo target) (*SubIssueChange, error) {
	const op = "reprioritize sub-issue"
	subID, err := c.issueDatabaseID(ctx, op, subRepo, a.SubIssueNumber)
	if err != nil {
		return nil, actionsFailure(err, subIssuesReadPermission)
	}
	body := map[string]any{"sub_issue_id": subID}
	if a.AfterNumber != nil {
		afterID, err := c.issueDatabaseID(ctx, op, subRepo, *a.AfterNumber)
		if err != nil {
			return nil, actionsFailure(err, subIssuesReadPermission)
		}
		body["after_id"] = afterID
	}
	if a.BeforeNumber != nil {
		beforeID, err := c.issueDatabaseID(ctx, op, subRepo, *a.BeforeNumber)
		if err != nil {
			return nil, actionsFailure(err, subIssuesReadPermission)
		}
		body["before_id"] = beforeID
	}
	if err := c.restChange(ctx, op, http.MethodPatch, subIssuePriorityPath(parent, a.Number), body, nil); err != nil {
		return nil, actionsFailure(err, subIssuesChangePermission)
	}
	return &SubIssueChange{Number: a.Number, SubIssue: subIssueRef(a.SubIssueNumber, subRepo, parent)}, nil
}

// subIssueRef names the sub-issue a change confirms, reporting its repository only when it differs from the
// parent's.
func subIssueRef(number int, repo, parent target) SubIssueRef {
	ref := SubIssueRef{Number: number}
	if repo != parent {
		ref.Repository = repo.owner + "/" + repo.repo
	}
	return ref
}

// subIssuesSubject names what a path below issues/NUMBER/ addresses when it continues into a sub-issue
// route: sub_issues, sub_issue, or sub_issues/priority, unescaped and reported only while the number is
// still one checkNumber accepts. Empty for any other path, such as the issue route restSubject already
// names on its own.
func subIssuesSubject(path string) string {
	rest, ok := strings.CutPrefix(path, "issues/")
	if !ok {
		return ""
	}
	digits, tail, _ := strings.Cut(rest, "/")
	number, err := strconv.Atoi(digits)
	if err != nil || checkNumber(number) != nil || tail == "" {
		return ""
	}
	switch tail {
	case "sub_issues":
		return "the sub-issues of issue #" + digits
	case "sub_issue":
		return "a sub-issue of issue #" + digits
	case "sub_issues/priority":
		return "the sub-issue order of issue #" + digits
	}
	return ""
}
