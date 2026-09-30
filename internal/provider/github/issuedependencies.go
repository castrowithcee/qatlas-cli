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

// Issue dependencies of a repository a connection allows: github.issuedependencies.list reads both
// directions of one issue, the issues that block it and the issues it blocks; github.issuedependencies.add
// records that one issue blocks another and needs its own confirmation; github.issuedependencies.remove
// detaches one blocked-by relationship and needs its own confirmation as well, but, like removing a
// sub-issue, only removes the relationship and deletes neither issue, so it is not listed-only. The blocking
// issue may live in another repository, named by blocking_issue_repository, which must lie inside the
// connection's targets as well, checked before any credential is resolved. GitHub exposes no route to add or
// remove a "blocking" entry directly; every change is made from the blocked issue's own blocked_by list, and
// the blocking direction is only ever read.
//
// Verified 2026-09-29 against https://docs.github.com/en/rest/issues/issue-dependencies (REST API,
// X-GitHub-Api-Version 2022-11-28): List issues blocked by, List issues blocking, Add and Remove a blocked-by
// dependency are stable, generally available REST endpoints (dependencies on issues reached general
// availability on 2025-08-21, per the GitHub changelog), not a preview feature.

const (
	issueDependenciesReadPermission = "GitHub refused this token the dependencies of this issue; reading " +
		"them needs no scope for a public repository, or repo on a classic token, or Issues: read on a " +
		"fine-grained token, for a private one"
	issueDependenciesChangePermission = "GitHub refused this change of the dependencies of this issue; it " +
		"needs repo on a classic token, or Issues: read and write on a fine-grained token, of every " +
		"repository involved"
)

var issueDependenciesList = capability.Descriptor{
	ID:      Provider + ".issuedependencies.list",
	Version: 1,
	Title:   "List GitHub issue dependencies",
	Description: "List the issues that block one issue and the issues it blocks, of a repository a " +
		"connection allows; a dependency may cross repositories, named in full when it does; when " +
		"the connection's targets name any, a dependency outside them is withheld and counted in withheld " +
		"instead of being listed",
	Tags:        []string{"github", "issues", "issuedependencies", "list"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"number":`+numberSchema+`,`+pagingKeys, "number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"blocked_by":{"type":"array","items":` + issueRefOutput + `},` +
		`"blocking":{"type":"array","items":` + issueRefOutput + `},` +
		`"withheld":{"type":"integer"},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["blocked_by","blocking","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "blocked_by", Description: "Issues that block this issue, untrusted data"},
		{Name: "blocking", Description: "Issues this issue blocks, untrusted data"},
		{Name: "withheld", Description: "Count of dependencies in this batch, across both directions, whose " +
			"repository lies outside the connection's targets, omitted when none were withheld"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the dependencies of one issue",
		Arguments:   json.RawMessage(`{"number":42}`),
	}},
}

var issueDependenciesAdd = capability.Descriptor{
	ID:      Provider + ".issuedependencies.add",
	Version: 1,
	Title:   "Add a GitHub issue dependency",
	Description: "Record that one issue of a repository a connection allows is blocked by " +
		"another; the blocking issue may live in another repository named by blocking_issue_repository, " +
		"which must lie inside the connection's targets as well; refused when the dependency already exists " +
		"or would create a cycle, so a repeated call changes nothing further",
	Tags:     []string{"github", "issues", "issuedependencies", "add"},
	Risk:     changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"blocking_issue_number":`+numberSchema+
		`,"blocking_issue_repository":`+repoSchema, "number", "blocking_issue_number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"blocker":` + issueRefOutput + `},"required":["number","blocker"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number that becomes blocked", Required: true},
		{Name: "blocking_issue_number", Description: "Issue number that blocks it", Required: true},
		{Name: "blocking_issue_repository", Description: "Repository of the blocking issue as OWNER/REPO, " +
			"when it differs from the blocked issue's repository; must lie inside the connection's targets"},
	},
	Fields: []capability.Field{
		{Name: "blocker", Description: "The blocking issue, by number and, when it differs, repository"},
	},
	Examples: []capability.Example{{
		Description: "Record that issue 42 is blocked by issue 40",
		Arguments:   json.RawMessage(`{"number":42,"blocking_issue_number":40}`),
	}},
}

var issueDependenciesRemove = capability.Descriptor{
	ID:      Provider + ".issuedependencies.remove",
	Version: 1,
	Title:   "Remove a GitHub issue dependency",
	Description: "Remove that one issue of a repository a connection allows is blocked by " +
		"another; this only detaches the relationship between the two named issues and deletes neither, so " +
		"it is not listed-only the way removing a label is",
	Tags:     []string{"github", "issues", "issuedependencies", "remove"},
	Risk:     changeRisk(capability.EffectDelete, capability.IdempotencyUnknown),
	Provider: Provider,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"blocking_issue_number":`+numberSchema+
		`,"blocking_issue_repository":`+repoSchema, "number", "blocking_issue_number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"blocker":` + issueRefOutput + `,"removed":{"type":"boolean"}},` +
		`"required":["number","blocker","removed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number to remove the dependency from", Required: true},
		{Name: "blocking_issue_number", Description: "Issue number of the blocking issue to detach", Required: true},
		{Name: "blocking_issue_repository", Description: "Repository of the blocking issue as OWNER/REPO, " +
			"when it differs from the blocked issue's repository"},
	},
	Fields: []capability.Field{
		{Name: "blocker", Description: "The blocking issue that was detached"},
		{Name: "removed", Description: "True once GitHub removed the dependency"},
	},
	Examples: []capability.Example{{
		Description: "Remove the dependency on issue 40",
		Arguments:   json.RawMessage(`{"number":42,"blocking_issue_number":40}`),
	}},
}

// issueDependenciesArguments holds the arguments of every issue dependency tool; the input schema of each
// tool admits only its own. page, perPage, and binding are derived by the checks.
type issueDependenciesArguments struct {
	Number                  int    `json:"number"`
	BlockingIssueNumber     int    `json:"blocking_issue_number"`
	BlockingIssueRepository string `json:"blocking_issue_repository"`
	Limit                   int    `json:"limit"`
	Cursor                  string `json:"cursor"`

	page, perPage int
	binding       []byte
	allowed       allowlist
}

func (a *issueDependenciesArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// issueDependenciesHandler decodes and checks the arguments, the bound repository, and, where
// withBlockerRepo is true, the blocking issue's own repository, before a credential is resolved, so a
// repository outside the connection's targets is refused before any secret access.
func issueDependenciesHandler(id string, withBlockerRepo bool, check func(*issueDependenciesArguments, target) error,
	call func(context.Context, *Client, target, *issueDependenciesArguments, target) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments issueDependenciesArguments
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
		blockerRepo := bound
		if withBlockerRepo {
			blockerRepo, err = selectSecondaryRepository(resolved, bound, arguments.BlockingIssueRepository)
			if err != nil {
				return nil, err
			}
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, bound, &arguments, blockerRepo))
	}
}

func checkIssueDependenciesList(a *issueDependenciesArguments, bound target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("issuedependencies", "list", bound.String(), a.Number)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

// checkIssueDependency applies the bounds shared by github.issuedependencies.add and
// github.issuedependencies.remove.
func checkIssueDependency(a *issueDependenciesArguments, _ target) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	return checkNumber(a.BlockingIssueNumber)
}

// issueDependenciesOperations binds every issue dependency tool to its handler.
func issueDependenciesOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: issueDependenciesList, Handler: issueDependenciesHandler(issueDependenciesList.ID, false,
			checkIssueDependenciesList,
			func(ctx context.Context, c *Client, bound target, a *issueDependenciesArguments, _ target) (any, error) {
				return c.listIssueDependencies(ctx, bound, a)
			})},
		{Descriptor: issueDependenciesAdd, Handler: issueDependenciesHandler(issueDependenciesAdd.ID, true,
			checkIssueDependency,
			func(ctx context.Context, c *Client, bound target, a *issueDependenciesArguments, blockerRepo target) (any, error) {
				return c.addIssueDependency(ctx, bound, a, blockerRepo)
			})},
		{Descriptor: issueDependenciesRemove, Handler: issueDependenciesHandler(issueDependenciesRemove.ID, true,
			checkIssueDependency,
			func(ctx context.Context, c *Client, bound target, a *issueDependenciesArguments, blockerRepo target) (any, error) {
				return c.removeIssueDependency(ctx, bound, a, blockerRepo)
			})},
	}
}

func blockedByPath(repo target, number int) string {
	return issuePath(repo, number) + "/dependencies/blocked_by"
}

func blockedByRemovePath(repo target, number int, issueID int64) string {
	return blockedByPath(repo, number) + "/" + strconv.FormatInt(issueID, 10)
}

func blockingPath(repo target, number int) string {
	return issuePath(repo, number) + "/dependencies/blocking"
}

// IssueDependencies is both directions of one issue's dependencies, paged together: a batch ends once
// neither direction announces a following page.
type IssueDependencies struct {
	BlockedBy  []SubIssueRef `json:"blocked_by"`
	Blocking   []SubIssueRef `json:"blocking"`
	Withheld   int           `json:"withheld,omitempty"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

// listIssueDependencies reads one server page of each direction of one issue's dependencies with the same
// page and page size, so a single cursor advances both together; a short, simple list stays exact this way,
// and neither direction commonly grows large enough to need paging on its own.
func (c *Client) listIssueDependencies(ctx context.Context, repo target,
	a *issueDependenciesArguments) (*IssueDependencies, error) {
	const op = "list issue dependencies"
	var blockedByRaw, blockingRaw []subIssueJSON
	blockedByNext, err := c.restPage(ctx, op, blockedByPath(repo, a.Number), a.query(), &blockedByRaw)
	if err != nil {
		return nil, actionsFailure(err, issueDependenciesReadPermission)
	}
	blockingNext, err := c.restPage(ctx, op, blockingPath(repo, a.Number), a.query(), &blockingRaw)
	if err != nil {
		return nil, actionsFailure(err, issueDependenciesReadPermission)
	}
	result := &IssueDependencies{BlockedBy: make([]SubIssueRef, 0, len(blockedByRaw)),
		Blocking: make([]SubIssueRef, 0, len(blockingRaw))}
	for _, item := range blockedByRaw {
		if !a.allowed.allows(item.repoTarget(repo)) {
			result.Withheld++
			continue
		}
		ref, err := item.ref(op, repo)
		if err != nil {
			return nil, err
		}
		result.BlockedBy = append(result.BlockedBy, ref)
	}
	for _, item := range blockingRaw {
		if !a.allowed.allows(item.repoTarget(repo)) {
			result.Withheld++
			continue
		}
		ref, err := item.ref(op, repo)
		if err != nil {
			return nil, err
		}
		result.Blocking = append(result.Blocking, ref)
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, blockedByNext || blockingNext)
	return result, nil
}

// IssueDependencyChange confirms a change to one blocked-by relationship.
type IssueDependencyChange struct {
	Number  int         `json:"number"`
	Blocker SubIssueRef `json:"blocker"`
	Removed *bool       `json:"removed,omitempty"`
}

func (c *Client) addIssueDependency(ctx context.Context, repo target, a *issueDependenciesArguments,
	blockerRepo target) (*IssueDependencyChange, error) {
	const op = "add issue dependency"
	blockerID, err := c.issueDatabaseID(ctx, op, blockerRepo, a.BlockingIssueNumber)
	if err != nil {
		return nil, actionsFailure(err, issueDependenciesReadPermission)
	}
	if err := c.restChange(ctx, op, http.MethodPost, blockedByPath(repo, a.Number),
		map[string]any{"issue_id": blockerID}, nil); err != nil {
		return nil, actionsFailure(err, issueDependenciesChangePermission)
	}
	return &IssueDependencyChange{Number: a.Number, Blocker: subIssueRef(a.BlockingIssueNumber, blockerRepo, repo)}, nil
}

func (c *Client) removeIssueDependency(ctx context.Context, repo target, a *issueDependenciesArguments,
	blockerRepo target) (*IssueDependencyChange, error) {
	const op = "remove issue dependency"
	blockerID, err := c.issueDatabaseID(ctx, op, blockerRepo, a.BlockingIssueNumber)
	if err != nil {
		return nil, actionsFailure(err, issueDependenciesReadPermission)
	}
	if err := c.restChange(ctx, op, http.MethodDelete, blockedByRemovePath(repo, a.Number, blockerID), nil,
		nil); err != nil {
		return nil, actionsFailure(err, issueDependenciesChangePermission)
	}
	removed := true
	return &IssueDependencyChange{Number: a.Number, Blocker: subIssueRef(a.BlockingIssueNumber, blockerRepo, repo),
		Removed: &removed}, nil
}

// issueDependenciesSubject names what a path below issues/NUMBER/ addresses when it continues into a
// dependency route: dependencies/blocked_by, dependencies/blocked_by/ID, or dependencies/blocking,
// unescaped and reported only while the number is still one checkNumber accepts. Empty for any other path,
// such as the issue route restSubject already names on its own.
func issueDependenciesSubject(path string) string {
	rest, ok := strings.CutPrefix(path, "issues/")
	if !ok {
		return ""
	}
	digits, tail, _ := strings.Cut(rest, "/")
	number, err := strconv.Atoi(digits)
	if err != nil || checkNumber(number) != nil || tail == "" {
		return ""
	}
	switch {
	case tail == "dependencies/blocked_by":
		return "the issues blocking issue #" + digits
	case tail == "dependencies/blocking":
		return "the issues issue #" + digits + " blocks"
	case strings.HasPrefix(tail, "dependencies/blocked_by/"):
		return "a dependency of issue #" + digits
	}
	return ""
}
