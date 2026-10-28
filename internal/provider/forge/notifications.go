package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The notification tools act on the notification threads of the account behind the token. A thread belongs to
// the account, not to a repository, so the targets bind it through its repository: a list drops every row whose
// canonical repository lies outside them, and a thread read or marked on a connection with targets is first
// proven by reading it. The count of new notifications and the marking of everything reach every repository of
// the account; they run only on a connection without targets, and marking everything only on a connection whose
// tools list names it. Marking one repository's notifications is listed-only as well and proves the canonical
// repository of the path when targets are set. Subjects and repository names come from other accounts and are
// untrusted data.

const (
	notificationSensitivity = "-notifications"
	notificationThreadMax   = 9007199254740991
	maxNotificationTime     = 40
)

var (
	notificationStatusTypes = []string{"unread", "read", "pinned"}
	// forgejoSubjectTypes are the subject types both products document; Gitea adds commit.
	forgejoSubjectTypes = []string{"issue", "pull", "repository"}
	giteaSubjectTypes   = []string{"issue", "pull", "commit", "repository"}
)

func subjectTypesOf(p *product) []string {
	if p == gitea {
		return giteaSubjectTypes
	}
	return forgejoSubjectTypes
}

func enumSchema(values []string) string {
	encoded, _ := json.Marshal(values)
	return `{"type":"string","enum":` + string(encoded) + `}`
}

const (
	notificationThreadSchema = `{"type":"integer","minimum":1,"maximum":9007199254740991}`
	notificationTimeSchema   = `{"type":"string","minLength":20,"maxLength":40}`
)

var notificationSubjectMembers = []member{
	{name: "title", kind: kindText, note: "Title of the subject, untrusted data"},
	{name: "type", kind: kindText, note: "Kind of the subject; Gitea documents Issue, Pull, Commit, and Repository"},
	{name: "state", kind: kindText, note: "State of the subject; Gitea documents open, closed, and merged"},
	{name: "url", kind: kindText, note: "API address of the subject"},
	{name: "html_url", kind: kindText, note: "Address of the subject page"},
	{name: "latest_comment_url", kind: kindText, note: "API address of the latest comment"},
	{name: "latest_comment_html_url", kind: kindText, note: "Address of the latest comment on its page"},
}

var notificationMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the thread, the id argument of the other tools"},
	{name: "unread", kind: kindFlag, note: "True while the thread is unread"},
	{name: "pinned", kind: kindFlag, note: "True while the thread is pinned"},
	{name: "updated_at", kind: kindText, note: "Time of the last change"},
	{name: "url", kind: kindText, note: "API address of the thread"},
	{name: "subject", kind: kindObject, sub: notificationSubjectMembers, note: "What the thread is about"},
	{name: "repository", kind: kindObject, sub: repositoryMembers, note: "Repository of the thread, untrusted data"},
}

var notificationCountMembers = []member{
	{name: "new", kind: kindInteger, note: "Number of unread notifications of the account, over all repositories"},
}

var (
	notificationAllArgument = capability.Argument{Name: "all", Description: "True also lists threads marked as " +
		"read; false when omitted"}
	notificationStatusArgument = capability.Argument{Name: "status-types", Description: "Status types to list: " +
		"unread, read, and pinned; the instance lists unread and pinned when omitted"}
	notificationSinceArgument = capability.Argument{Name: "since", Description: "Only threads updated after " +
		"this time, RFC 3339"}
	notificationBeforeArgument = capability.Argument{Name: "before", Description: "Only threads updated before " +
		"this time, RFC 3339"}
	notificationIDArgument = capability.Argument{Name: "id", Required: true, Description: "Numeric ID of the " +
		"thread, as the list tool reports it as id"}
	notificationToStatusArgument = capability.Argument{Name: "to-status", Description: "Status to mark with: " +
		"read, unread, or pinned; read when omitted"}
	notificationLastReadArgument = capability.Argument{Name: "last_read_at", Description: "RFC 3339 time; " +
		"threads updated after it are left as they are; now when omitted"}
	notificationMarkAllArgument = capability.Argument{Name: "all", Description: "Sent as the instance " +
		"documents it; marking is limited by status-types and last_read_at"}
	notificationMarkStatusArgument = capability.Argument{Name: "status-types", Description: "Status types to " +
		"mark: unread, read, and pinned; the instance marks unread threads when omitted"}
)

// notificationCall holds the arguments of every notification tool.
type notificationCall struct {
	Repository  string   `json:"repository"`
	ID          int64    `json:"id"`
	All         *bool    `json:"all"`
	StatusTypes []string `json:"status-types"`
	SubjectType []string `json:"subject-type"`
	Since       string   `json:"since"`
	Before      string   `json:"before"`
	ToStatus    string   `json:"to-status"`
	LastReadAt  string   `json:"last_read_at"`
	Limit       int      `json:"limit"`
	Cursor      string   `json:"cursor"`
}

func checkEnumList(name string, values, allowed []string) error {
	if len(values) > len(allowed) {
		return invalidArgument(name + " names too many entries")
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !slices.Contains(allowed, v) || seen[v] {
			return invalidArgument(name + " must name each of " + strings.Join(allowed, ", ") + " at most once")
		}
		seen[v] = true
	}
	return nil
}

func checkNotificationTime(name, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maxNotificationTime {
		return invalidArgument(name + " must be an RFC 3339 time")
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return invalidArgument(name + " must be an RFC 3339 time")
	}
	return nil
}

// checkMark validates the arguments of a marking.
func (a notificationCall) checkMark(p *product, withBulk bool) error {
	if a.ToStatus != "" && !slices.Contains(notificationStatusTypes, a.ToStatus) {
		return invalidArgument("to-status must be one of " + strings.Join(notificationStatusTypes, ", "))
	}
	if !withBulk {
		return nil
	}
	if err := checkEnumList("status-types", a.StatusTypes, notificationStatusTypes); err != nil {
		return err
	}
	return checkNotificationTime("last_read_at", a.LastReadAt)
}

// checkList validates the filters of a list.
func (a notificationCall) checkList(p *product) error {
	if err := checkEnumList("status-types", a.StatusTypes, notificationStatusTypes); err != nil {
		return err
	}
	if err := checkEnumList("subject-type", a.SubjectType, subjectTypesOf(p)); err != nil {
		return err
	}
	if err := checkNotificationTime("since", a.Since); err != nil {
		return err
	}
	return checkNotificationTime("before", a.Before)
}

func (a notificationCall) listQuery(page paging) url.Values {
	query := page.query()
	if a.All != nil {
		query.Set("all", strconv.FormatBool(*a.All))
	}
	for _, v := range a.StatusTypes {
		query.Add("status-types", v)
	}
	for _, v := range a.SubjectType {
		query.Add("subject-type", v)
	}
	if a.Since != "" {
		query.Set("since", a.Since)
	}
	if a.Before != "" {
		query.Set("before", a.Before)
	}
	return query
}

func (a notificationCall) markQuery() url.Values {
	query := url.Values{}
	if a.All != nil {
		query.Set("all", strconv.FormatBool(*a.All))
	}
	for _, v := range a.StatusTypes {
		query.Add("status-types", v)
	}
	if a.ToStatus != "" {
		query.Set("to-status", a.ToStatus)
	}
	if a.LastReadAt != "" {
		query.Set("last_read_at", a.LastReadAt)
	}
	return query
}

func (a notificationCall) binding(p *product, scope string) []byte {
	all := ""
	if a.All != nil {
		all = strconv.FormatBool(*a.All)
	}
	return provider.CursorBinding(p.id, "notifications.list", strings.ToLower(scope), all,
		strings.Join(a.StatusTypes, ","), strings.Join(a.SubjectType, ","), a.Since, a.Before)
}

// threadPath is the path of one thread.
func threadPath(id int64) string { return "/notifications/threads/" + strconv.FormatInt(id, 10) }

// readThread reads one thread, requires its ID, and on a connection with targets requires its canonical
// repository to lie inside them. The refusal does not name the repository.
func readThread(ctx context.Context, c *Client, allowed allowlist, op string, id int64) (object, error) {
	var thread object
	if _, err := c.get(ctx, op, threadPath(id), nil, &thread); err != nil {
		return nil, err
	}
	if got, ok := thread.integer("id"); !ok || got != id {
		return nil, provider.InvalidResponse(op, c.product.name+" returned a notification without its ID")
	}
	if !admitted(allowed, thread) {
		name, _ := issueRepository(thread)
		return nil, allowed.outside(op, c.product, name, "notification thread")
	}
	return thread, nil
}

// proveRepository reads the repository of a path and requires its canonical name to lie inside the targets, so
// that a renamed repository cannot be reached under its old name.
func proveRepository(ctx context.Context, c *Client, allowed allowlist, op string, repo target) error {
	if allowed.open() {
		return nil
	}
	var answer object
	if _, err := c.get(ctx, op, repositoryPath(repo), nil, &answer); err != nil {
		return err
	}
	name, ok := answer.text("full_name")
	if !ok || !allowed.allowsFullName(name) {
		return allowed.outside(op, c.product, name, "repository")
	}
	return nil
}

func listProperties(p *product, repository bool) (string, []capability.Argument) {
	properties := `"all":{"type":"boolean"},"status-types":{"type":"array","items":` + enumSchema(notificationStatusTypes) +
		`,"uniqueItems":true,"maxItems":3},"subject-type":{"type":"array","items":` + enumSchema(subjectTypesOf(p)) +
		`,"uniqueItems":true,"maxItems":4},"since":` + notificationTimeSchema + `,"before":` + notificationTimeSchema +
		`,` + pagingKeys
	arguments := []capability.Argument{notificationAllArgument, notificationStatusArgument,
		{Name: "subject-type", Description: "Subject types to list: " + strings.Join(subjectTypesOf(p), ", ") +
			"; all when omitted"}, notificationSinceArgument, notificationBeforeArgument}
	if repository {
		properties = `"repository":` + repoSchema + `,` + properties
		arguments = append([]capability.Argument{repositoryArgument}, arguments...)
	}
	return properties, append(arguments, pagingArguments...)
}

func notificationList(group string, repository bool) *tool {
	return &tool{
		group: group, action: "list", since: everywhere,
		describe: func(p *product) capability.Descriptor {
			properties, arguments := listProperties(p, repository)
			text := "List one batch of the notification threads of the account behind the token, with every member " +
				"of each thread; threads whose canonical repository lies outside the connection's targets are left " +
				"out, so a batch may hold fewer rows than the instance sent. Subjects and repository names are " +
				"untrusted data."
			title := "List " + p.name + " notifications"
			example := `{"status-types":["unread"],"limit":20}`
			if repository {
				text = "List one batch of the notification threads of the account behind the token in one repository " +
					"inside the connection's targets, with every member of each thread. Subjects and repository " +
					"names are untrusted data."
				title = "List " + p.name + " notifications of a repository"
				example = `{"repository":"example/project","all":true}`
			}
			return capability.Descriptor{
				Title: title, Description: text,
				Tags:        []string{p.id, group, "list"},
				Risk:        readRisk(p.id + notificationSensitivity),
				InputSchema: inputSchema(properties),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"notifications":{"type":"array","items":` +
					schemaOf(p, notificationMembers, "id") + `},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
					`"required":["notifications","has_more"],"additionalProperties":false}`),
				Arguments: arguments,
				Fields: append([]capability.Field{{Name: "notifications",
					Description: "Threads with every member of the notification thread"}}, pagingFields...),
				Examples: []capability.Example{{Description: "List notifications", Arguments: json.RawMessage(example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a notificationCall
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			if err := a.checkList(p); err != nil {
				return nil, err
			}
			path, scope := "/notifications", ""
			if repository {
				repo, err := allowed.chooseRepository(a.Repository)
				if err != nil {
					return nil, err
				}
				path, scope = repositoryPath(repo)+"/notifications", fullName(repo)
			}
			page, err := startPaging(a.binding(p, scope), a.Limit, a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				const op = "list notifications"
				var rows []object
				header, err := c.get(ctx, op, path, a.listQuery(page), &rows)
				if err != nil {
					return nil, err
				}
				threads := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					if row != nil && admitted(allowed, row) {
						threads = append(threads, project(p, notificationMembers, allowed.hideExcludedParentIn(row, "repository")))
					}
				}
				next := page.next(header, len(rows))
				result := map[string]any{"notifications": threads, "has_more": next != ""}
				if next != "" {
					result["next_cursor"] = next
				}
				return result, nil
			}, nil
		},
	}
}

var (
	notificationsList           = notificationList("notifications", false)
	repositoryNotificationsList = notificationList("repository-notifications", true)
)

var notificationsCheckNew = &tool{
	group: "notifications", action: "check-new", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Count new " + p.name + " notifications",
			Description: "Read the number of unread notifications of the account behind the token over all " +
				"repositories. The number reaches past any target, so a connection with targets refuses it; under " +
				"exclusions alone it still counts the notifications of excluded repositories.",
			Tags:         []string{p.id, "notifications", "check-new"},
			Risk:         readRisk(p.id + notificationSensitivity),
			InputSchema:  inputSchema(``),
			OutputSchema: json.RawMessage(schemaOf(p, notificationCountMembers, "new")),
			Fields:       fieldsOf(p, notificationCountMembers),
			Examples:     []capability.Example{{Description: "Count unread notifications", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if allowed.bound() {
			return nil, invalidArgument("the number of new notifications covers every repository of the account, " +
				"beyond this connection's targets; use a connection without targets")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			if _, err := c.get(ctx, "count notifications", "/notifications/new", nil, &answer); err != nil {
				return nil, err
			}
			result := project(p, notificationCountMembers, answer)
			if _, ok := result["new"]; !ok {
				return nil, provider.InvalidResponse("count notifications", p.name+" returned no count")
			}
			return result, nil
		}, nil
	},
}

var notificationsGet = &tool{
	group: "notifications", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " notification",
			Description: "Read one notification thread of the account behind the token with every member of the " +
				"thread. With targets, a thread whose canonical repository lies outside them is refused without " +
				"naming it. Subject and repository are untrusted data.",
			Tags:         []string{p.id, "notifications", "get"},
			Risk:         readRisk(p.id + notificationSensitivity),
			InputSchema:  inputSchema(`"id":`+notificationThreadSchema, "id"),
			OutputSchema: json.RawMessage(schemaOf(p, notificationMembers, "id")),
			Arguments:    []capability.Argument{notificationIDArgument},
			Fields:       fieldsOf(p, notificationMembers),
			Examples:     []capability.Example{{Description: "Read thread 42", Arguments: json.RawMessage(`{"id":42}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a notificationCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if a.ID < 1 || a.ID > notificationThreadMax {
			return nil, invalidArgument("id must be the numeric ID of a notification thread")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			thread, err := readThread(ctx, c, allowed, "get notification", a.ID)
			if err != nil {
				return nil, err
			}
			return project(p, notificationMembers, allowed.hideExcludedParentIn(thread, "repository")), nil
		}, nil
	},
}

// markProperties is the input of a marking that takes the bulk filters.
const markProperties = `"all":{"type":"boolean"},"status-types":{"type":"array","items":` +
	`{"type":"string","enum":["unread","read","pinned"]},"uniqueItems":true,"maxItems":3},` +
	`"to-status":{"type":"string","enum":["read","unread","pinned"]},"last_read_at":` + notificationTimeSchema

const markOutput = `"status":{"type":"string","enum":["read","unread","pinned"]}`

func markedStatus(a notificationCall) string {
	if a.ToStatus == "" {
		return "read"
	}
	return a.ToStatus
}

var notificationsMarkThread = &tool{
	group: "notifications", action: "mark-thread", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Mark a " + p.name + " notification",
			Description: "Mark one notification thread of the account behind the token as read, unread, or pinned. " +
				"On a connection with targets the thread is read first and its canonical repository must lie " +
				"inside them. Idempotent; the request is sent once and never repeated after an unclear outcome.",
			Tags:         []string{p.id, "notifications", "mark-thread"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+notificationSensitivity),
			InputSchema:  inputSchema(`"id":`+notificationThreadSchema+`,"to-status":`+enumSchema([]string{"read", "unread", "pinned"}), "id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},` + markOutput + `},"required":["id","status"],"additionalProperties":false}`),
			Arguments:    []capability.Argument{notificationIDArgument, notificationToStatusArgument},
			Fields: []capability.Field{{Name: "id", Description: "The thread the call acted on"},
				{Name: "status", Description: "The status the thread was marked with"}},
			Examples: []capability.Example{{Description: "Mark thread 42 as read", Arguments: json.RawMessage(`{"id":42}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a notificationCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if a.ID < 1 || a.ID > notificationThreadMax {
			return nil, invalidArgument("id must be the numeric ID of a notification thread")
		}
		if err := a.checkMark(p, false); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "mark notification"
			if !allowed.open() {
				if _, err := readThread(ctx, c, allowed, op, a.ID); err != nil {
					return nil, err
				}
			}
			query := url.Values{}
			if a.ToStatus != "" {
				query.Set("to-status", a.ToStatus)
			}
			if _, err := c.changeQuery(ctx, op, http.MethodPatch, threadPath(a.ID), query, nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ID, "status": markedStatus(a)}, nil
		}, nil
	},
}

var notificationsMarkAllRead = &tool{
	group: "notifications", action: "mark-all-read", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Mark all " + p.name + " notifications",
			Description: "Mark the notification threads of the account behind the token in every repository as read, " +
				"unread, or pinned, limited by status-types and last_read_at. It reaches past any target and any " +
				"exclusion, so a connection with targets or exclusions refuses it before any request. The answer is " +
				"discarded unread. Without " +
				"last_read_at a repeat can reach threads that changed in between; the request is sent once and never " +
				"repeated after an unclear outcome. Offered only by a connection without targets whose tools list names " +
				"it, in no profile.",
			Tags:                  []string{p.id, "notifications", "mark-all-read"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyUnknown, p.id+notificationSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(markProperties),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{` + markOutput +
				`},"required":["status"],"additionalProperties":false}`),
			Arguments: []capability.Argument{notificationMarkAllArgument, notificationMarkStatusArgument,
				notificationToStatusArgument, notificationLastReadArgument},
			Fields: []capability.Field{{Name: "status", Description: "The status the threads were marked with"}},
			Examples: []capability.Example{{Description: "Mark everything read up to a time",
				Arguments: json.RawMessage(`{"last_read_at":"2026-01-01T00:00:00Z"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a notificationCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if !allowed.open() {
			return nil, invalidArgument("marking every notification reaches past this connection's targets and " +
				"exclusions; use a connection without either")
		}
		if err := a.checkMark(p, true); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := c.changeQuery(ctx, "mark all notifications", http.MethodPut, "/notifications",
				a.markQuery(), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"status": markedStatus(a)}, nil
		}, nil
	},
}

var repositoryNotificationsMarkRead = &tool{
	group: "repository-notifications", action: "mark-read", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Mark the notifications of a " + p.name + " repository",
			Description: "Mark the notification threads of the account behind the token in one repository inside the " +
				"connection's targets as read, unread, or pinned, limited by status-types and last_read_at; with " +
				"targets the canonical repository is read first. The answer is discarded unread. Without last_read_at " +
				"a repeat can reach threads that changed in between; the request is sent once and never repeated " +
				"after an unclear outcome. Offered only by a connection whose tools list names it, in no profile.",
			Tags:                  []string{p.id, "repository-notifications", "mark-read"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyUnknown, p.id+notificationSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"repository":` + repoSchema + `,` + markProperties),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` + markOutput +
				`},"required":["repository","status"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, notificationMarkAllArgument,
				notificationMarkStatusArgument, notificationToStatusArgument, notificationLastReadArgument},
			Fields: []capability.Field{{Name: "repository", Description: "The repository the call acted on"},
				{Name: "status", Description: "The status the threads were marked with"}},
			Examples: []capability.Example{{Description: "Mark the notifications of a repository read",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a notificationCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := a.checkMark(p, true); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "mark repository notifications"
			if err := proveRepository(ctx, c, allowed, op, repo); err != nil {
				return nil, err
			}
			if _, err := c.changeQuery(ctx, op, http.MethodPut, repositoryPath(repo)+"/notifications",
				a.markQuery(), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "status": markedStatus(a)}, nil
		}, nil
	},
}
