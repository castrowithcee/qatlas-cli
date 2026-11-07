package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// The statistics tools read GitHub's precomputed repository statistics through fixed routes, one tool per
// route. GitHub answers 202 while it still computes a statistic and 204 for a repository without commits;
// both are reported as a state without waiting or repeating the request. The raw arrays and tuples GitHub
// sends are given named members, and every list is bounded with the cut visible. Accounts show the members
// GitHub embeds and are never fetched again.

// Bounds of the statistics answers. The weeks kept are the most recent ones.
const (
	maxContributorStats = 100 // GitHub reports at most 100 contributors
	maxContributorWeeks = 52
	maxCommitWeeks      = 52
	maxCodeFrequency    = 260
	maxParticipation    = 52
	maxPunchCard        = 168 // 7 days of 24 hours
)

func statsPermission(what string) string {
	return "GitHub refused this token the " + what + " of this repository; reading it needs no scope for a " +
		"public repository, or repo on a classic token, or read access to the repository on a fine-grained " +
		"token, for a private one, and GitHub does not document which fine-grained permission it checks"
}

var statsStateField = capability.Field{Name: "state", Description: "ready, computing when GitHub still " +
	"computes the statistic and sent none, or empty for a repository without commits"}

const statsStateSchema = `"state":{"type":"string"}`

func statsDescriptor(id, title, description string, tags []string, properties, required string,
	fields []capability.Field) capability.Descriptor {
	return capability.Descriptor{
		ID: id, Version: 1, Title: title, Description: description,
		Tags: tags, Risk: readRisk, Provider: Provider, InputSchema: inputSchema(``),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + statsStateSchema + `,` + properties +
			`},"required":["state",` + required + `],"additionalProperties":false}`),
		Fields:   append([]capability.Field{statsStateField}, fields...),
		Examples: []capability.Example{{Description: title, Arguments: json.RawMessage(`{"repository":"octo-org/example"}`)}},
	}
}

const statsNote = "; GitHub answers 202 while it still computes the statistic, reported as state computing, and " +
	"answers a repository without commits with no content, reported as state empty, never waiting or repeating " +
	"the request"

var contributorWeekSchema = objectSchema(`"week":{"type":"integer"},"additions":{"type":"integer"},` +
	`"deletions":{"type":"integer"},"commits":{"type":"integer"}`)

var statsContributorsGet = statsDescriptor(Provider+".stats-contributors.get", "Get GitHub contributor statistics",
	"Read the commit statistics of the contributors of a repository a connection allows: per contributor the "+
		"account, the total number of commits, and the most recent 52 weeks with additions, deletions, and commits, "+
		"at most 100 contributors, most commits first, the cut visible"+statsNote,
	[]string{"github", "stats", "contributors", "get"},
	`"contributors":{"type":"array","maxItems":100,"items":`+objectSchema(`"author":`+restAccountSchema+
		`,"total":{"type":"integer"},"weeks":{"type":"array","maxItems":52,"items":`+contributorWeekSchema+
		`},"weeks_truncated":{"type":"boolean"}`)+`},"contributors_truncated":{"type":"boolean"}`,
	`"contributors"`,
	[]capability.Field{
		{Name: "contributors", Description: "Contributors, most commits first, at most 100, empty unless state is " +
			"ready, each with author (an account: " + restAccountMembers + "; absent when GitHub reports none), " +
			"total (commits over the whole history), weeks (the most recent 52, oldest first), and weeks_truncated"},
		{Name: "weeks", Description: "Weeks with week (start of the week as a Unix timestamp), additions, " +
			"deletions, and commits; additions and deletions are 0 for a repository of 10,000 or more commits"},
		{Name: "weeks_truncated", Description: "True when the contributor has older weeks than shown"},
		{Name: "contributors_truncated", Description: "True when more contributors than shown were reported"},
	})

var commitWeekSchema = objectSchema(`"week":{"type":"integer"},"total":{"type":"integer"},` +
	`"days":{"type":"array","maxItems":7,"items":{"type":"integer"}}`)

var statsCommitActivityGet = statsDescriptor(Provider+".stats-commit-activity.get",
	"Get GitHub weekly commit activity",
	"Read the commits per week over the last year of a repository a connection allows, with the commits of "+
		"each day of the week, at most 52 weeks, the cut visible"+statsNote,
	[]string{"github", "stats", "commit-activity", "get"},
	`"weeks":{"type":"array","maxItems":52,"items":`+commitWeekSchema+`},"weeks_truncated":{"type":"boolean"}`,
	`"weeks"`,
	[]capability.Field{
		{Name: "weeks", Description: "Weeks, oldest first, empty unless state is ready, each with week (start of " +
			"the week as a Unix timestamp), total (commits of the week), and days (commits per day, 7 numbers, " +
			"Sunday first)"},
		{Name: "weeks_truncated", Description: "True when GitHub reported more than 52 weeks and the oldest were cut"},
	})

var codeFrequencyWeekSchema = objectSchema(`"week":{"type":"integer"},"additions":{"type":"integer"},` +
	`"deletions":{"type":"integer"}`)

var statsCodeFrequencyGet = statsDescriptor(Provider+".stats-code-frequency.get",
	"Get GitHub weekly code frequency",
	"Read the lines added and deleted per week of a repository a connection allows, the most recent 260 "+
		"weeks, the cut visible; GitHub refuses repositories of 10,000 or more commits"+statsNote,
	[]string{"github", "stats", "code-frequency", "get"},
	`"weeks":{"type":"array","maxItems":260,"items":`+codeFrequencyWeekSchema+`},"weeks_truncated":{"type":"boolean"}`,
	`"weeks"`,
	[]capability.Field{
		{Name: "weeks", Description: "Weeks, oldest first, empty unless state is ready, each with week (start of " +
			"the week as a Unix timestamp), additions, and deletions (a negative number, as GitHub reports it)"},
		{Name: "weeks_truncated", Description: "True when the repository has older weeks than the 260 shown"},
	})

var statsParticipationGet = statsDescriptor(Provider+".stats-participation.get",
	"Get GitHub weekly participation",
	"Read the commits per week over the last year of a repository a connection allows, for everyone and for "+
		"the owner alone, at most 52 weeks each, the cut visible"+statsNote,
	[]string{"github", "stats", "participation", "get"},
	`"all":{"type":"array","maxItems":52,"items":{"type":"integer"}},"all_truncated":{"type":"boolean"},`+
		`"owner":{"type":"array","maxItems":52,"items":{"type":"integer"}},"owner_truncated":{"type":"boolean"}`,
	`"all","owner"`,
	[]capability.Field{
		{Name: "all", Description: "Commits per week by everyone, oldest week first, empty unless state is ready"},
		{Name: "owner", Description: "Commits per week by the owner, oldest week first, empty unless state is ready"},
		{Name: "all_truncated", Description: "True when GitHub reported more than 52 weeks of all and the oldest were cut"},
		{Name: "owner_truncated", Description: "True when GitHub reported more than 52 weeks of owner and the oldest were cut"},
	})

var punchCardEntrySchema = objectSchema(`"day":{"type":"integer"},"hour":{"type":"integer"},"commits":{"type":"integer"}`)

var statsPunchCardGet = statsDescriptor(Provider+".stats-punch-card.get", "Get GitHub commit punch card",
	"Read the commits per day of the week and hour of the day of a repository a connection allows, at most "+
		"168 entries, the cut visible"+statsNote,
	[]string{"github", "stats", "punch-card", "get"},
	`"entries":{"type":"array","maxItems":168,"items":`+punchCardEntrySchema+`},"entries_truncated":{"type":"boolean"}`,
	`"entries"`,
	[]capability.Field{
		{Name: "entries", Description: "Entries, empty unless state is ready, each with day (0 for Sunday through " +
			"6 for Saturday), hour (0 through 23), and commits"},
		{Name: "entries_truncated", Description: "True when GitHub reported more than 168 entries"},
	})

// ---- github.activity.list

// Values of the activity filters GitHub accepts.
var (
	activityTimePeriods = []string{"day", "week", "month", "quarter", "year"}
	activityTypes       = []string{"push", "force_push", "branch_creation", "branch_deletion", "pr_merge",
		"merge_queue_merge"}
	activityDirections = []string{"asc", "desc"}
)

const maxActivityCursor = 500

var activityProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"before":{"type":"string"},` +
	`"after":{"type":"string"},"ref":{"type":"string"},"timestamp":{"type":"string"},` +
	`"activity_type":{"type":"string"},"actor":` + restAccountSchema

var activityList = capability.Descriptor{
	ID:      Provider + ".activity.list",
	Version: 1,
	Title:   "List GitHub repository activity",
	Description: "List one bounded, filtered batch of the activity feed of a repository a connection allows, " +
		"the pushes, force pushes, branch creations and deletions, and merges to its refs, newest first unless " +
		"direction says otherwise",
	Tags:     []string{"github", "activity", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: inputSchema(`"ref":` + refSchema + `,"actor":{"type":"string","maxLength":105,"pattern":"` +
		actorPattern + `"},"time_period":` + enumSchema(activityTimePeriods) + `,"activity_type":` +
		enumSchema(activityTypes) + `,"direction":` + enumSchema(activityDirections) + `,` + pagingKeys),
	OutputSchema: listOutput("activities", activityProperties,
		`"required":["id","activity_type"],"additionalProperties":false`),
	Arguments: append([]capability.Argument{
		{Name: "ref", Description: "Return only activity on this ref, as a branch name or a full ref such as " +
			"refs/heads/main"},
		{Name: "actor", Description: "Return only activity by this GitHub login"},
		{Name: "time_period", Description: "Return only activity of the last day, week, month, quarter, or year"},
		{Name: "activity_type", Description: "Return only this kind of activity: push, force_push, " +
			"branch_creation, branch_deletion, pr_merge, or merge_queue_merge"},
		{Name: "direction", Description: "asc for the oldest first, desc for the newest first; desc when omitted"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "activities", Description: "Activities with id, node_id, before and after (the commit SHAs of the " +
			"ref before and after the change), ref (untrusted data), timestamp, activity_type, and actor (an " +
			"account: " + restAccountMembers + "; absent when GitHub reports none)"},
		{Name: "activity_type", Description: "push, force_push, branch_creation, branch_deletion, pr_merge, or " +
			"merge_queue_merge"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the force pushes of the last month",
		Arguments:   json.RawMessage(`{"activity_type":"force_push","time_period":"month"}`),
	}},
}

func repositoryStatsOperations() []capability.Operation {
	read := func(d capability.Descriptor, call func(context.Context, *Client) (any, error)) capability.Operation {
		return capability.Operation{Descriptor: d, Handler: repositoryReadHandler(call)}
	}
	return []capability.Operation{
		read(statsContributorsGet, func(ctx context.Context, c *Client) (any, error) { return c.getStatsContributors(ctx) }),
		read(statsCommitActivityGet, func(ctx context.Context, c *Client) (any, error) { return c.getStatsCommitActivity(ctx) }),
		read(statsCodeFrequencyGet, func(ctx context.Context, c *Client) (any, error) { return c.getStatsCodeFrequency(ctx) }),
		read(statsParticipationGet, func(ctx context.Context, c *Client) (any, error) { return c.getStatsParticipation(ctx) }),
		read(statsPunchCardGet, func(ctx context.Context, c *Client) (any, error) { return c.getStatsPunchCard(ctx) }),
		{Descriptor: activityList, Handler: capability.Handler(invokeActivityList)},
	}
}

// statsBody reads one statistics route once and returns its state; the body matters only for ready.
func (c *Client) statsBody(ctx context.Context, op, route, what string) (string, json.RawMessage, error) {
	var body json.RawMessage
	status, _, err := c.restStatus(ctx, op, c.repoPath(route), nil, &body)
	if err != nil {
		return "", nil, actionsFailure(err, statsPermission(what))
	}
	switch status {
	case http.StatusAccepted:
		return stateComputing, nil, nil
	case http.StatusNoContent:
		return stateEmpty, nil, nil
	}
	return stateReady, body, nil
}

// lastOf keeps the most recent entries of an oldest-first list and reports whether older ones were cut.
func lastOf[T any](items []T, limit int) ([]T, bool) {
	if len(items) <= limit {
		return items, false
	}
	return items[len(items)-limit:], true
}

// ---- github.stats-contributors.get

type contributorStatRaw struct {
	Author *accountRaw `json:"author"`
	Total  *int64      `json:"total"`
	Weeks  []struct {
		W *int64 `json:"w"`
		A *int64 `json:"a"`
		D *int64 `json:"d"`
		C *int64 `json:"c"`
	} `json:"weeks"`
}

// ContributorWeek is one week of a contributor's activity.
type ContributorWeek struct {
	Week      int64 `json:"week"`
	Additions int64 `json:"additions"`
	Deletions int64 `json:"deletions"`
	Commits   int64 `json:"commits"`
}

// ContributorStat is the commit statistics of one contributor.
type ContributorStat struct {
	Author         *RESTAccount      `json:"author,omitempty"`
	Total          int64             `json:"total"`
	Weeks          []ContributorWeek `json:"weeks"`
	WeeksTruncated bool              `json:"weeks_truncated,omitempty"`
}

// ContributorStats are the commit statistics of the contributors of a repository.
type ContributorStats struct {
	State                 string            `json:"state"`
	Contributors          []ContributorStat `json:"contributors"`
	ContributorsTruncated bool              `json:"contributors_truncated,omitempty"`
}

func (c *Client) getStatsContributors(ctx context.Context) (*ContributorStats, error) {
	const op = "get contributor statistics"
	state, body, err := c.statsBody(ctx, op, "stats/contributors", "contributor statistics")
	if err != nil {
		return nil, err
	}
	result := &ContributorStats{State: state, Contributors: []ContributorStat{}}
	if state != stateReady {
		return result, nil
	}
	var raw []contributorStatRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalidResponse(op, false)
	}
	// GitHub lists the least active first; the most active are the ones worth keeping when it sends more.
	sort.SliceStable(raw, func(i, j int) bool { return valueOr(raw[i].Total) > valueOr(raw[j].Total) })
	for i := range raw {
		entry := &raw[i]
		if entry.Total == nil {
			return nil, invalidEntry(op, "a contributor")
		}
		weeks := make([]ContributorWeek, 0, len(entry.Weeks))
		for _, w := range entry.Weeks {
			if w.W == nil || w.A == nil || w.D == nil || w.C == nil {
				return nil, invalidEntry(op, "a week")
			}
			weeks = append(weeks, ContributorWeek{Week: *w.W, Additions: *w.A, Deletions: *w.D, Commits: *w.C})
		}
		kept, cut := lastOf(weeks, maxContributorWeeks)
		result.Contributors = append(result.Contributors, ContributorStat{Author: entry.Author.project(),
			Total: *entry.Total, Weeks: kept, WeeksTruncated: cut})
	}
	if len(result.Contributors) > maxContributorStats {
		result.Contributors, result.ContributorsTruncated = result.Contributors[:maxContributorStats], true
	}
	return result, nil
}

// ---- github.stats-commit-activity.get

// CommitWeek is the commits of one week and of each day in it.
type CommitWeek struct {
	Week  int64   `json:"week"`
	Total int64   `json:"total"`
	Days  []int64 `json:"days"`
}

// CommitActivity is the weekly commit activity of the last year.
type CommitActivity struct {
	State          string       `json:"state"`
	Weeks          []CommitWeek `json:"weeks"`
	WeeksTruncated bool         `json:"weeks_truncated,omitempty"`
}

func (c *Client) getStatsCommitActivity(ctx context.Context) (*CommitActivity, error) {
	const op = "get commit activity"
	state, body, err := c.statsBody(ctx, op, "stats/commit_activity", "commit activity")
	if err != nil {
		return nil, err
	}
	result := &CommitActivity{State: state, Weeks: []CommitWeek{}}
	if state != stateReady {
		return result, nil
	}
	var raw []struct {
		Days  []int64 `json:"days"`
		Total *int64  `json:"total"`
		Week  *int64  `json:"week"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalidResponse(op, false)
	}
	for _, w := range raw {
		if w.Total == nil || w.Week == nil || len(w.Days) != 7 {
			return nil, invalidEntry(op, "a week")
		}
		result.Weeks = append(result.Weeks, CommitWeek{Week: *w.Week, Total: *w.Total, Days: w.Days})
	}
	result.Weeks, result.WeeksTruncated = lastOf(result.Weeks, maxCommitWeeks)
	return result, nil
}

// ---- github.stats-code-frequency.get

// CodeFrequencyWeek is the lines added and deleted in one week.
type CodeFrequencyWeek struct {
	Week      int64 `json:"week"`
	Additions int64 `json:"additions"`
	Deletions int64 `json:"deletions"`
}

// CodeFrequency is the weekly code frequency of a repository.
type CodeFrequency struct {
	State          string              `json:"state"`
	Weeks          []CodeFrequencyWeek `json:"weeks"`
	WeeksTruncated bool                `json:"weeks_truncated,omitempty"`
}

func (c *Client) getStatsCodeFrequency(ctx context.Context) (*CodeFrequency, error) {
	const op = "get code frequency"
	state, body, err := c.statsBody(ctx, op, "stats/code_frequency", "code frequency")
	if err != nil {
		return nil, err
	}
	result := &CodeFrequency{State: state, Weeks: []CodeFrequencyWeek{}}
	if state != stateReady {
		return result, nil
	}
	var raw [][]int64
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalidResponse(op, false)
	}
	for _, t := range raw {
		if len(t) != 3 {
			return nil, invalidEntry(op, "a week")
		}
		result.Weeks = append(result.Weeks, CodeFrequencyWeek{Week: t[0], Additions: t[1], Deletions: t[2]})
	}
	result.Weeks, result.WeeksTruncated = lastOf(result.Weeks, maxCodeFrequency)
	return result, nil
}

// ---- github.stats-participation.get

// Participation is the weekly commit counts of everyone and of the owner.
type Participation struct {
	State          string  `json:"state"`
	All            []int64 `json:"all"`
	AllTruncated   bool    `json:"all_truncated,omitempty"`
	Owner          []int64 `json:"owner"`
	OwnerTruncated bool    `json:"owner_truncated,omitempty"`
}

func (c *Client) getStatsParticipation(ctx context.Context) (*Participation, error) {
	const op = "get participation"
	state, body, err := c.statsBody(ctx, op, "stats/participation", "participation statistics")
	if err != nil {
		return nil, err
	}
	result := &Participation{State: state, All: []int64{}, Owner: []int64{}}
	if state != stateReady {
		return result, nil
	}
	var raw struct {
		All   *[]int64 `json:"all"`
		Owner *[]int64 `json:"owner"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalidResponse(op, false)
	}
	if raw.All == nil || raw.Owner == nil {
		return nil, invalidEntry(op, "the participation")
	}
	result.All, result.AllTruncated = lastOf(*raw.All, maxParticipation)
	result.Owner, result.OwnerTruncated = lastOf(*raw.Owner, maxParticipation)
	return result, nil
}

// ---- github.stats-punch-card.get

// PunchCardEntry is the commits of one hour of one day of the week.
type PunchCardEntry struct {
	Day     int64 `json:"day"`
	Hour    int64 `json:"hour"`
	Commits int64 `json:"commits"`
}

// PunchCard is the commits by day of the week and hour of the day.
type PunchCard struct {
	State            string           `json:"state"`
	Entries          []PunchCardEntry `json:"entries"`
	EntriesTruncated bool             `json:"entries_truncated,omitempty"`
}

func (c *Client) getStatsPunchCard(ctx context.Context) (*PunchCard, error) {
	const op = "get punch card"
	state, body, err := c.statsBody(ctx, op, "stats/punch_card", "punch card")
	if err != nil {
		return nil, err
	}
	result := &PunchCard{State: state, Entries: []PunchCardEntry{}}
	if state != stateReady {
		return result, nil
	}
	var raw [][]int64
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalidResponse(op, false)
	}
	for _, t := range raw {
		if len(t) != 3 {
			return nil, invalidEntry(op, "an entry")
		}
		result.Entries = append(result.Entries, PunchCardEntry{Day: t[0], Hour: t[1], Commits: t[2]})
	}
	if len(result.Entries) > maxPunchCard {
		result.Entries, result.EntriesTruncated = result.Entries[:maxPunchCard], true
	}
	return result, nil
}

// ---- github.activity.list

type activityListOptions struct {
	Ref          string `json:"ref"`
	Actor        string `json:"actor"`
	TimePeriod   string `json:"time_period"`
	ActivityType string `json:"activity_type"`
	Direction    string `json:"direction"`
	Limit        int    `json:"limit"`
	Cursor       string `json:"cursor"`

	perPage int
	after   string
	binding []byte
}

// unsetOrOneOf accepts an omitted filter or one of the allowed values.
func unsetOrOneOf(value string, allowed []string) bool { return value == "" || oneOf(value, allowed) }

func (o *activityListOptions) normalize(bound target) error {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return err
	}
	if o.Ref != "" && !validRef(o.Ref) {
		return invalidRequest("ref must be a branch name or a full ref such as refs/heads/main")
	}
	if o.Actor != "" && !validActor(o.Actor) {
		return invalidRequest("actor must be a GitHub login")
	}
	if !unsetOrOneOf(o.TimePeriod, activityTimePeriods) {
		return invalidRequest("time_period must be one of " + strings.Join(activityTimePeriods, ", "))
	}
	if !unsetOrOneOf(o.ActivityType, activityTypes) {
		return invalidRequest("activity_type must be one of " + strings.Join(activityTypes, ", "))
	}
	if !unsetOrOneOf(o.Direction, activityDirections) {
		return invalidRequest("direction must be asc or desc")
	}
	o.binding = fingerprint("activity", "list", bound.String(), o.Ref, strings.ToLower(o.Actor), o.TimePeriod,
		o.ActivityType, o.Direction)
	o.perPage = limit
	after, err := decodeCursor(o.binding, o.Cursor)
	if err != nil {
		return err
	}
	if after != "" {
		size, cursor, _ := strings.Cut(after, ":")
		perPage, sizeErr := strconv.Atoi(size)
		if sizeErr != nil || perPage < 1 || perPage > maxLimit || !validActivityCursor(cursor) {
			return invalidRequest("cursor is not a next_cursor of this list; start the list again without cursor")
		}
		o.perPage, o.after = perPage, cursor
	}
	return nil
}

// validActor mirrors actorPattern.
func validActor(value string) bool {
	return validLogin(strings.TrimSuffix(value, "[bot]")) && len(value) <= 105
}

// validActivityCursor accepts the opaque cursor GitHub puts in its Link header only as one query value of a
// restricted alphabet, so it can never carry another parameter.
func validActivityCursor(value string) bool {
	if value == "" || len(value) > maxActivityCursor {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' ||
			r == '=' || r == '.') {
			return false
		}
	}
	return true
}

func (o *activityListOptions) query() url.Values {
	values := url.Values{"per_page": {strconv.Itoa(o.perPage)}}
	for key, value := range map[string]string{"ref": o.Ref, "actor": o.Actor, "time_period": o.TimePeriod,
		"activity_type": o.ActivityType, "direction": o.Direction, "after": o.after} {
		if value != "" {
			values.Set(key, value)
		}
	}
	return values
}

// nextAfter returns the after cursor of the rel="next" link of a Link header and whether such a link exists.
func nextAfter(header http.Header) (string, bool) {
	for _, part := range strings.Split(header.Get("Link"), ",") {
		address, params, ok := strings.Cut(part, ";")
		if !ok || strings.TrimSpace(params) != `rel="next"` {
			continue
		}
		address = strings.Trim(strings.TrimSpace(address), "<>")
		parsed, err := url.Parse(address)
		if err != nil {
			return "", true
		}
		return parsed.Query().Get("after"), true
	}
	return "", false
}

// invokeActivityList checks the target, the filters, and the cursor before a credential is resolved.
func invokeActivityList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options activityListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable(activityList.ID)
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := options.normalize(bound); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listActivity(ctx, &options))
}

type activityRaw struct {
	ID           *int64      `json:"id"`
	NodeID       *string     `json:"node_id"`
	Before       *string     `json:"before"`
	After        *string     `json:"after"`
	Ref          *string     `json:"ref"`
	Timestamp    *string     `json:"timestamp"`
	ActivityType *string     `json:"activity_type"`
	Actor        *accountRaw `json:"actor"`
}

// Activity is one entry of the activity feed of a repository.
type Activity struct {
	ID           int64        `json:"id"`
	NodeID       *string      `json:"node_id,omitempty"`
	Before       *string      `json:"before,omitempty"`
	After        *string      `json:"after,omitempty"`
	Ref          *string      `json:"ref,omitempty"`
	Timestamp    *string      `json:"timestamp,omitempty"`
	ActivityType string       `json:"activity_type"`
	Actor        *RESTAccount `json:"actor,omitempty"`
}

// ActivityList is one batch of the activity feed of a repository.
type ActivityList struct {
	Activities []Activity `json:"activities"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

func (c *Client) listActivity(ctx context.Context, o *activityListOptions) (*ActivityList, error) {
	const op = "list activity"
	var raw []activityRaw
	path := c.repoPath("activity") + "?" + o.query().Encode()
	var header http.Header
	if err := c.do(ctx, op, http.MethodGet, c.endpoints.rest+path, nil, &raw, false, &header, nil); err != nil {
		return nil, actionsFailure(err, statsPermission("activity feed"))
	}
	result := &ActivityList{Activities: []Activity{}}
	for i := range raw {
		a := &raw[i]
		if a.ID == nil || a.ActivityType == nil {
			return nil, invalidEntry(op, "an activity")
		}
		result.Activities = append(result.Activities, Activity{ID: *a.ID, NodeID: nameText(a.NodeID),
			Before: nameText(a.Before), After: nameText(a.After), Ref: nameText(a.Ref),
			Timestamp: nameText(a.Timestamp), ActivityType: *nameText(a.ActivityType), Actor: a.Actor.project()})
	}
	// GitHub pages this feed by cursor, so the continuation carries the cursor of its Link header.
	after, announced := nextAfter(header)
	if !announced {
		return result, nil
	}
	// A following page this tool cannot continue must not read as the end of the feed.
	if !validActivityCursor(after) {
		return nil, invalidResponse(op, false)
	}
	result.HasMore = true
	result.NextCursor = encodeCursor(o.binding, strconv.Itoa(o.perPage)+":"+after)
	return result, nil
}
