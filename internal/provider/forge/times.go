package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The time tools read and book tracked time and drive the stopwatches of the issues and pull requests of one
// repository inside the connection's targets. A change reads the issue or pull request of its index first (the
// evidence read) and acts only when the canonical repository of the answer lies inside the targets. Deleting one
// time entry additionally proves that the entry belongs to that index. The two account-wide lists read what the
// token's user tracked everywhere and keep only the rows whose repository lies inside the targets.
//
// A repository can switch time tracking off. The instance then answers the time endpoints with HTTP 400, which
// the API documents for add, delete, reset, and the two repository lists; a validated request has no other
// reason for it, so every time endpoint of a repository path reports 400 as unavailable. The stopwatch
// endpoints document only 403, 404, and 409 and give no distinct signal, so a repository without time tracking
// shows there as the usual refusal.

const (
	// maxTrackedSeconds bounds one booking; the API takes any int64.
	maxTrackedSeconds = 1_000_000_000
	// maxTimeProofPages bounds the pages read while proving that a time entry belongs to an issue.
	maxTimeProofPages = 20

	timeSchemaSeconds = `{"type":"integer","minimum":1,"maximum":1000000000}`
)

var stopWatchMembers = []member{
	{name: "created", kind: kindText, note: "Time the stopwatch was started"},
	{name: "duration", kind: kindText, note: "Elapsed time as the instance formats it, untrusted data"},
	{name: "seconds", kind: kindInteger, note: "Elapsed seconds"},
	{name: "issue_index", kind: kindInteger, note: "Index of the issue or pull request in its repository"},
	{name: "issue_title", kind: kindText, note: "Title of the issue or pull request, untrusted data"},
	{name: "repo_owner_name", kind: kindText, note: "Owner of the repository"},
	{name: "repo_name", kind: kindText, note: "Name of the repository"},
}

var (
	timeUserArgument  = capability.Argument{Name: "user", Description: "Only the times of this user name"}
	timeSinceArgument = capability.Argument{Name: "since",
		Description: "Only times logged at or after this time, RFC 3339"}
	timeBeforeArgument = capability.Argument{Name: "before",
		Description: "Only times logged at or before this time, RFC 3339"}
)

const timeFilterKeys = `"since":` + issueSchemaTime + `,"before":` + issueSchemaTime

// timesDisabled turns the answer of an instance that has time tracking switched off for the repository into a
// plain "not available"; the instance's own text stays out of the message and travels as the original details.
func timesDisabled(op string, status int, err error) error {
	if err != nil && status == http.StatusBadRequest {
		failure := provider.Fail(op, "time tracking is not available for this repository; it may be turned off in "+
			"the repository settings")
		return keepOriginal(failure, err)
	}
	return err
}

// getTimes is a GET of a time endpoint of a repository path.
func (c *Client) getTimes(ctx context.Context, op, path string, query url.Values, out any) (http.Header, error) {
	status, header, err := send(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, path, query, out)
	return header, timesDisabled(op, status, err)
}

// changeTimes sends one change to a time endpoint of a repository path.
func (c *Client) changeTimes(ctx context.Context, op, method, path string, body any, out any) error {
	status, err := c.changeQuery(ctx, op, method, path, nil, body, out)
	return timesDisabled(op, status, err)
}

func repoTimesPath(repo target) string {
	return "/repos/" + escape(repo.owner) + "/" + escape(repo.repo) + "/times"
}

// timeRows projects the tracked times whose issue reports a repository inside the targets.
func timeRows(p *product, allowed allowlist, rows []object) []map[string]any {
	times := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		issue, ok := row.child("issue")
		if !allowed.open() && (!ok || !admitted(allowed, issue)) {
			continue
		}
		times = append(times, project(p, trackedTimeMembers, row))
	}
	return times
}

// stopWatchRows projects the stopwatches whose repository lies inside the targets.
func stopWatchRows(p *product, allowed allowlist, rows []object) []map[string]any {
	watches := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		owner, hasOwner := row.text("repo_owner_name")
		name, hasName := row.text("repo_name")
		if !allowed.open() && (!hasOwner || !hasName || owner == "" || name == "" ||
			!allowed.allowsRepository(owner, name)) {
			continue
		}
		watches = append(watches, project(p, stopWatchMembers, row))
	}
	return watches
}

func timeListOutput(p *product, key string, members []member, required string, extra string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"array","items":` +
		schemaOf(p, members, required) + `},` + extra + `"next_cursor":{"type":"string"},` +
		`"has_more":{"type":"boolean"}},"required":["` + key + `","has_more"],"additionalProperties":false}`)
}

// timeFilter is the filters the list endpoints share.
type timeFilter struct {
	User   string `json:"user"`
	Since  string `json:"since"`
	Before string `json:"before"`
}

func (f timeFilter) check(user bool) error {
	if user {
		if err := checkLogin("user", f.User); err != nil {
			return err
		}
	}
	if err := checkTime("since", f.Since); err != nil {
		return err
	}
	return checkTime("before", f.Before)
}

func (f timeFilter) apply(query url.Values) {
	for key, value := range map[string]string{"user": f.User, "since": f.Since, "before": f.Before} {
		if value != "" {
			query.Set(key, value)
		}
	}
}

// pagedBatch returns the result of one batch of a paged list.
func pagedBatch(key string, items []map[string]any, next string) map[string]any {
	result := map[string]any{key: items, "has_more": next != ""}
	if next != "" {
		result["next_cursor"] = next
	}
	return result
}

// onIndex is the shared start of a time or stopwatch change: the repository and index are checked against the
// targets, then act runs after the evidence read.
func onIndex(allowed allowlist, repository string, number int64, op string,
	act func(ctx context.Context, c *Client, repo target) (any, error)) (run, error) {
	repo, err := allowed.chooseRepository(repository)
	if err != nil {
		return nil, err
	}
	if number < 1 {
		return nil, invalidArgument("number must be the index of an issue or pull request")
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := readIndex(ctx, c, allowed, op, repo, number); err != nil {
			return nil, err
		}
		return act(ctx, c, repo)
	}, nil
}

type indexArguments struct {
	Repository string `json:"repository"`
	Number     int64  `json:"number"`
}

const indexKeys = `"repository":` + repoSchema + `,"number":` + issueSchemaNumber

var timeNumberArgument = commentNumberArgument

var issueTimesList = &tool{
	group: "issue-times", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the tracked times of a " + p.name + " issue",
			Description: "List one batch of the tracked times of an issue or pull request of a repository inside " +
				"the connection's targets, with every member of each entry; user names are personal data. A " +
				"repository can have time tracking turned off, which is reported as not available.",
			Tags:         []string{p.id, "issues", "times", "list"},
			Risk:         readRisk(p.id + issueSensitivity),
			InputSchema:  inputSchema(indexKeys+`,"user":`+issueSchemaLogin+`,`+timeFilterKeys+`,`+pagingKeys, "number"),
			OutputSchema: timeListOutput(p, "times", trackedTimeMembers, "id", ""),
			Arguments: append([]capability.Argument{repositoryArgument, timeNumberArgument, timeUserArgument,
				timeSinceArgument, timeBeforeArgument}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "times", Description: "Tracked times with every member of " +
				"the entry"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the times of an issue",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			indexArguments
			timeFilter
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if arguments.Number < 1 {
			return nil, invalidArgument("number must be the index of an issue or pull request")
		}
		if err := arguments.check(true); err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "issue-times", strings.ToLower(repo.owner+"/"+repo.repo),
			arguments.Number, arguments.User, arguments.Since, arguments.Before), arguments.Limit, arguments.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := page.query()
			arguments.apply(query)
			var rows []object
			header, err := c.getTimes(ctx, "list issue times", issuePath(repo, arguments.Number)+"/times", query, &rows)
			if err != nil {
				return nil, err
			}
			return pagedBatch("times", timeRows(p, allowed, rows), page.next(header, len(rows))), nil
		}, nil
	},
}

var issueTimesAdd = &tool{
	group: "issue-times", action: "add", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Add tracked time to a " + p.name + " issue",
			Description: "Book tracked time on an issue or pull request of a repository inside the connection's " +
				"targets and return the entry. Without user_name the time is booked for the token's user; booking " +
				"for another user needs the rights of a repository administrator. Each call adds an entry. A " +
				"repository can have time tracking turned off, which is reported as not available.",
			Tags: []string{p.id, "issues", "times", "add", "create"},
			Risk: changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+issueSensitivity),
			InputSchema: inputSchema(indexKeys+`,"time":`+timeSchemaSeconds+`,"created":`+issueSchemaTime+
				`,"user_name":`+issueSchemaLogin, "number", "time"),
			OutputSchema: json.RawMessage(schemaOf(p, trackedTimeMembers, "id")),
			Arguments: []capability.Argument{repositoryArgument, timeNumberArgument,
				{Name: "time", Required: true, Description: "Tracked time in seconds, 1 to 1000000000"},
				{Name: "created", Description: "Time the work was logged, RFC 3339; now when omitted"},
				{Name: "user_name", Description: "User name of the person who spent the time; the token's user " +
					"when omitted"}},
			Fields: fieldsOf(p, trackedTimeMembers),
			Examples: []capability.Example{{Description: "Book one hour",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"time":3600}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			indexArguments
			Time     int64  `json:"time"`
			Created  string `json:"created"`
			UserName string `json:"user_name"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if arguments.Time < 1 || arguments.Time > maxTrackedSeconds {
			return nil, invalidArgument("time must be between 1 and 1000000000 seconds")
		}
		if err := checkTime("created", arguments.Created); err != nil {
			return nil, err
		}
		if err := checkLogin("user_name", arguments.UserName); err != nil {
			return nil, err
		}
		body := map[string]any{"time": arguments.Time}
		if arguments.Created != "" {
			body["created"] = arguments.Created
		}
		if arguments.UserName != "" {
			body["user_name"] = arguments.UserName
		}
		return onIndex(allowed, arguments.Repository, arguments.Number, "add issue time",
			func(ctx context.Context, c *Client, repo target) (any, error) {
				var created object
				if err := c.changeTimes(ctx, "add issue time", http.MethodPost, issuePath(repo, arguments.Number)+"/times",
					body, &created); err != nil {
					return nil, err
				}
				if id, ok := created.integer("id"); !ok || id < 1 {
					return nil, unsure(provider.InvalidResponse("add issue time",
						c.product.name+" returned a time entry without its ID"))
				}
				return project(p, trackedTimeMembers, created), nil
			})
	},
}

// proveTime reads the times of the issue until it finds the entry, so that an ID of another issue is refused
// before anything is deleted. The API has no read of one entry. The list shows the entries the token may see; an
// entry it does not list is not deleted.
func proveTime(ctx context.Context, c *Client, op string, repo target, number, id int64) error {
	for page := 1; page <= maxTimeProofPages; page++ {
		query := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(maxLimit)}}
		var rows []object
		header, err := c.getTimes(ctx, op, issuePath(repo, number)+"/times", query, &rows)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if got, ok := row.integer("id"); ok && got == id {
				return nil
			}
		}
		if !hasMore(header, page, maxLimit, len(rows)) {
			break
		}
	}
	return invalidArgument("id is not the ID of a tracked time of this issue")
}

var issueTimesDelete = &tool{
	group: "issue-times", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a tracked time of a " + p.name + " issue",
			Description: "Delete one tracked time entry of an issue or pull request of a repository inside the " +
				"connection's targets for good; this cannot be undone. The entry must be one the issue's time list " +
				"shows. Only a connection whose tools list names it offers this tool.",
			Tags:                  []string{p.id, "issues", "times", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+issueSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(indexKeys+`,"id":`+issueSchemaID, "number", "id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
				`"id":{"type":"integer"},"deleted":{"type":"boolean"}},"required":["number","id","deleted"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, timeNumberArgument, {Name: "id", Required: true,
				Description: "Numeric ID of the time entry, as issue-times.list reports it as id"}},
			Fields: []capability.Field{{Name: "number", Description: "Index of the issue"},
				{Name: "id", Description: "ID of the entry"},
				{Name: "deleted", Description: "True once the instance deleted the entry"}},
			Examples: []capability.Example{{Description: "Delete a time entry",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			indexArguments
			ID int64 `json:"id"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if arguments.ID < 1 {
			return nil, invalidArgument("id must be the ID of a time entry")
		}
		const op = "delete issue time"
		return onIndex(allowed, arguments.Repository, arguments.Number, op,
			func(ctx context.Context, c *Client, repo target) (any, error) {
				if err := proveTime(ctx, c, op, repo, arguments.Number, arguments.ID); err != nil {
					return nil, err
				}
				path := issuePath(repo, arguments.Number) + "/times/" + strconv.FormatInt(arguments.ID, 10)
				if err := c.changeTimes(ctx, op, http.MethodDelete, path, nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"number": arguments.Number, "id": arguments.ID, "deleted": true}, nil
			})
	},
}

var issueTimesReset = &tool{
	group: "issue-times", action: "reset", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Reset the tracked times of a " + p.name + " issue",
			Description: "Delete every tracked time entry of an issue or pull request of a repository inside the " +
				"connection's targets for good, those of other users included; this cannot be undone. Only a " +
				"connection whose tools list names it offers this tool.",
			Tags:                  []string{p.id, "issues", "times", "reset", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+issueSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(indexKeys, "number"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
				`"reset":{"type":"boolean"}},"required":["number","reset"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, timeNumberArgument},
			Fields: []capability.Field{{Name: "number", Description: "Index of the issue"},
				{Name: "reset", Description: "True once the instance deleted the entries"}},
			Examples: []capability.Example{{Description: "Reset the times of an issue",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments indexArguments
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		return onIndex(allowed, arguments.Repository, arguments.Number, "reset issue times",
			func(ctx context.Context, c *Client, repo target) (any, error) {
				if err := c.changeTimes(ctx, "reset issue times", http.MethodDelete,
					issuePath(repo, arguments.Number)+"/times", nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"number": arguments.Number, "reset": true}, nil
			})
	},
}

// stopwatchTool is issue-stopwatches.start, .stop, and .delete: one request without a body.
func stopwatchTool(action, method, title, description string, effect capability.Effect,
	idempotency capability.Idempotency, resultKey string) *tool {
	return &tool{
		group: "issue-stopwatches", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title:       title + " on a " + p.name + " issue",
				Description: description,
				Tags:        []string{p.id, "issues", "stopwatches", action, string(effect)},
				Risk:        changeRisk(effect, idempotency, p.id+issueSensitivity),
				InputSchema: inputSchema(indexKeys, "number"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},"` +
					resultKey + `":{"type":"boolean"}},"required":["number","` + resultKey +
					`"],"additionalProperties":false}`),
				Arguments: []capability.Argument{repositoryArgument, timeNumberArgument},
				Fields: []capability.Field{{Name: "number", Description: "Index of the issue"},
					{Name: resultKey, Description: "True once the instance accepted the request"}},
				Examples: []capability.Example{{Description: title,
					Arguments: json.RawMessage(`{"repository":"example/project","number":7}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments indexArguments
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			op := action + " issue stopwatch"
			return onIndex(allowed, arguments.Repository, arguments.Number, op,
				func(ctx context.Context, c *Client, repo target) (any, error) {
					if err := c.change(ctx, op, method, issuePath(repo, arguments.Number)+"/stopwatch/"+action,
						nil, nil); err != nil {
						return nil, err
					}
					return map[string]any{"number": arguments.Number, resultKey: true}, nil
				})
		},
	}
}

var (
	issueStopwatchesStart = stopwatchTool("start", http.MethodPost, "Start the stopwatch",
		"Start the token user's stopwatch on an issue or pull request of a repository inside the connection's "+
			"targets. The instance refuses it when the stopwatch already runs.",
		capability.EffectUpdate, capability.IdempotencyNonIdempotent, "started")
	issueStopwatchesStop = stopwatchTool("stop", http.MethodPost, "Stop the stopwatch",
		"Stop the token user's stopwatch on an issue or pull request of a repository inside the connection's "+
			"targets and book the elapsed time as a tracked time entry. The instance refuses it when no stopwatch runs.",
		capability.EffectUpdate, capability.IdempotencyNonIdempotent, "stopped")
	issueStopwatchesDelete = stopwatchTool("delete", http.MethodDelete, "Discard the stopwatch",
		"Discard the token user's running stopwatch on an issue or pull request of a repository inside the "+
			"connection's targets without booking any time; the elapsed time is lost. The instance refuses it when "+
			"no stopwatch runs.",
		capability.EffectDelete, capability.IdempotencyNonIdempotent, "discarded")
)

var repositoryTimesList = &tool{
	group: "repository-times", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the tracked times of a " + p.name + " repository",
			Description: "List one batch of the tracked times of a repository inside the connection's targets, with " +
				"every member of each entry; user names are personal data. A repository can have time tracking " +
				"turned off, which is reported as not available.",
			Tags:         []string{p.id, "repositories", "times", "list"},
			Risk:         readRisk(p.id + issueSensitivity),
			InputSchema:  inputSchema(`"repository":` + repoSchema + `,"user":` + issueSchemaLogin + `,` + timeFilterKeys + `,` + pagingKeys),
			OutputSchema: timeListOutput(p, "times", trackedTimeMembers, "id", ""),
			Arguments: append([]capability.Argument{repositoryArgument, timeUserArgument, timeSinceArgument,
				timeBeforeArgument}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "times", Description: "Tracked times with every member of " +
				"the entry"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the times of a repository since a date",
				Arguments: json.RawMessage(`{"repository":"example/project","since":"2030-01-01T00:00:00Z"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			timeFilter
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if err := arguments.check(true); err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "repository-times", strings.ToLower(repo.owner+"/"+repo.repo),
			arguments.User, arguments.Since, arguments.Before), arguments.Limit, arguments.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := page.query()
			arguments.apply(query)
			var rows []object
			header, err := c.getTimes(ctx, "list repository times", repoTimesPath(repo), query, &rows)
			if err != nil {
				return nil, err
			}
			return pagedBatch("times", timeRows(p, allowed, rows), page.next(header, len(rows))), nil
		}, nil
	},
}

var userTimesList = &tool{
	group: "user-times", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the tracked times of a user in a " + p.name + " repository",
			Description: "List the tracked times one user booked in a repository inside the connection's targets, " +
				"with every member of each entry; the instance serves this list without paging, so a list longer " +
				"than 100 entries is cut and marked truncated. User names are personal data. A repository can " +
				"have time tracking turned off, which is reported as not available.",
			Tags:        []string{p.id, "users", "times", "list"},
			Risk:        readRisk(p.id + issueSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"user":`+issueSchemaLogin, "user"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"times":{"type":"array","items":` +
				schemaOf(p, trackedTimeMembers, "id") + `},"truncated":{"type":"boolean"}},` +
				`"required":["times"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, {Name: "user", Required: true,
				Description: "User name whose times to list"}},
			Fields: []capability.Field{{Name: "times", Description: "Tracked times with every member of the entry"},
				{Name: "truncated", Description: "True when the instance sent more entries than the tool returns"}},
			Examples: []capability.Example{{Description: "List the times of a user",
				Arguments: json.RawMessage(`{"repository":"example/project","user":"alice"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			User       string `json:"user"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		if arguments.User == "" {
			return nil, invalidArgument("user is required")
		}
		if err := checkLogin("user", arguments.User); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			if _, err := c.getTimes(ctx, "list user times", repoTimesPath(repo)+"/"+escape(arguments.User), nil,
				&rows); err != nil {
				return nil, err
			}
			result := map[string]any{}
			if len(rows) > maxListItems {
				rows, result["truncated"] = rows[:maxListItems], true
			}
			result["times"] = timeRows(p, allowed, rows)
			return result, nil
		}, nil
	},
}

var myTimesList = &tool{
	group: "my-times", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the tracked times of the " + p.name + " token's user",
			Description: "List one batch of the times the token's user tracked across repositories and return those " +
				"of repositories inside the connection's targets; every row is checked against the targets, so a " +
				"batch may hold fewer entries than the instance sent. User names are personal data.",
			Tags:         []string{p.id, "users", "times", "list"},
			Risk:         readRisk(p.id + issueSensitivity),
			InputSchema:  inputSchema(timeFilterKeys + `,` + pagingKeys),
			OutputSchema: timeListOutput(p, "times", trackedTimeMembers, "id", ""),
			Arguments:    append([]capability.Argument{timeSinceArgument, timeBeforeArgument}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "times", Description: "Tracked times with every member of " +
				"the entry, the issue naming its repository"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List my times since a date",
				Arguments: json.RawMessage(`{"since":"2030-01-01T00:00:00Z"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			timeFilter
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if err := arguments.check(false); err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "my-times", arguments.Since, arguments.Before),
			arguments.Limit, arguments.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := page.query()
			arguments.apply(query)
			var rows []object
			header, err := c.get(ctx, "list my times", "/user/times", query, &rows)
			if err != nil {
				return nil, err
			}
			return pagedBatch("times", timeRows(p, allowed, rows), page.next(header, len(rows))), nil
		}, nil
	},
}

var myStopwatchesList = &tool{
	group: "my-stopwatches", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the running stopwatches of the " + p.name + " token's user",
			Description: "List one batch of the stopwatches the token's user has running across repositories and " +
				"return those of repositories inside the connection's targets; every row is checked against the " +
				"targets, so a batch may hold fewer entries than the instance sent. Issue titles are untrusted data.",
			Tags:         []string{p.id, "users", "stopwatches", "list"},
			Risk:         readRisk(p.id + issueSensitivity),
			InputSchema:  inputSchema(pagingKeys),
			OutputSchema: timeListOutput(p, "stopwatches", stopWatchMembers, "seconds", ""),
			Arguments:    pagingArguments,
			Fields: append([]capability.Field{{Name: "stopwatches", Description: "Running stopwatches with every " +
				"member of the stopwatch object"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List my running stopwatches",
				Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "my-stopwatches"), arguments.Limit, arguments.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			header, err := c.get(ctx, "list my stopwatches", "/user/stopwatches", page.query(), &rows)
			if err != nil {
				return nil, err
			}
			return pagedBatch("stopwatches", stopWatchRows(p, allowed, rows), page.next(header, len(rows))), nil
		}, nil
	},
}
