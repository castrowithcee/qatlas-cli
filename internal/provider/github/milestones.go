package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Milestones of a repository. github.milestones.list reads them, filtered by state; nothing here creates,
// changes, or deletes one. github.issues.update sets or removes the milestone of one issue by number, in
// issues.go and tools.go.

const milestoneProperties = `"number":{"type":"integer"},"title":{"type":"string"},` +
	`"description":{"type":"string"},"state":{"type":"string"},"due_on":{"type":"string"},` +
	`"open_issues":{"type":"integer"},"closed_issues":{"type":"integer"}`

const milestoneRequired = `"required":["number","title","state"],"additionalProperties":false`

var milestonesList = capability.Descriptor{
	ID:      Provider + ".milestones.list",
	Version: 1,
	Title:   "List GitHub milestones",
	Description: "List one bounded batch of the milestones of a repository an explicit connection allows, " +
		"with their state, due date, and open and closed issue counts",
	Tags:                       []string{"github", "milestones", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"state":{"type":"string","enum":["open","closed","all"]},` + pagingKeys),
	OutputSchema:               listOutput("milestones", milestoneProperties, milestoneRequired),
	Arguments: append([]capability.Argument{
		{Name: "state", Description: "open, closed, or all; open when omitted"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "milestones", Description: "Milestones with number, title, state, due date, and open and " +
			"closed issue counts; title and description are untrusted data, description absent when the " +
			"milestone has none"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the open milestones",
		Arguments:   json.RawMessage(`{"state":"open"}`),
	}},
}

// milestonesArguments holds the arguments of github.milestones.list. page and perPage are derived by the
// check.
type milestonesArguments struct {
	State  string `json:"state"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	page, perPage int
	binding       []byte
}

func checkMilestonesListArguments(a *milestonesArguments, bound target) error {
	switch a.State {
	case "":
		a.State = "open"
	case "open", "closed", "all":
	default:
		return invalidRequest("state must be open, closed, or all")
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("milestones", "list", bound.String(), a.State)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

func (a *milestonesArguments) query() url.Values {
	return url.Values{"state": {a.State}, "per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// milestonesHandler decodes and checks the arguments and the repository before a credential is resolved, so
// a refused request never becomes a provider call, the way labelsHandler does for the label tools.
func milestonesHandler(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments milestonesArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(milestonesList.ID)
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkMilestonesListArguments(&arguments, bound); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listMilestones(ctx, &arguments))
}

func milestonesOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: milestonesList, Handler: capability.Handler(milestonesHandler)},
	}
}

// Permission message of the milestone tool. GitHub decides on every request; the message names what such a
// request needs without claiming what the configured token holds.
const milestonesReadPermission = "GitHub refused this token the milestones of this repository; reading them " +
	"needs no scope for a public repository, or repo on a classic token, or Issues: read on a fine-grained " +
	"token, for a private one"

// Milestone is the compact view of one milestone; its JSON tags match GitHub's own field names, so a
// milestone decodes directly without an intermediate shape.
type Milestone struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	State        string `json:"state"`
	DueOn        string `json:"due_on,omitempty"`
	OpenIssues   int    `json:"open_issues"`
	ClosedIssues int    `json:"closed_issues"`
}

// MilestoneList is one batch of milestones.
type MilestoneList struct {
	Milestones []Milestone `json:"milestones"`
	NextCursor string      `json:"next_cursor,omitempty"`
	HasMore    bool        `json:"has_more"`
}

func (c *Client) listMilestones(ctx context.Context, a *milestonesArguments) (*MilestoneList, error) {
	const op = "list milestones"
	var raw []Milestone
	hasNext, err := c.restPage(ctx, op, c.repoPath("milestones"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, milestonesReadPermission)
	}
	result := &MilestoneList{Milestones: make([]Milestone, 0, len(raw))}
	for _, milestone := range raw {
		if milestone.Number < 1 || milestone.Title == "" {
			return nil, invalidEntry(op, "a milestone")
		}
		result.Milestones = append(result.Milestones, milestone)
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// milestonesSubject names the milestone a repository path below /milestones addresses: milestones for the
// collection, or milestones/NUMBER for one, the way labelsSubject names a label below /labels. It is empty
// for any other path.
func milestonesSubject(path string) string {
	if path == "milestones" {
		return "the milestones of this repository"
	}
	if number, ok := trimNumericSuffix(path, "milestones/"); ok {
		return "milestone " + number
	}
	return ""
}

// trimNumericSuffix reports whether path is prefix followed by one or more digits and nothing else, and
// returns the digits.
func trimNumericSuffix(path, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok || rest == "" {
		return "", false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return rest, true
}
