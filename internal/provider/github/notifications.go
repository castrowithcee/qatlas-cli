package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The notification tools read and manage the notification threads of the account behind the connection's
// token, and the subscriptions to a thread or a repository. A notification thread belongs to the account, not
// to a repository, so a connection whose targets name a repository is scoped as follows: the list and the
// mark-all tool then act on exactly one repository the targets allow, and every tool that takes a thread
// reads the thread first and refuses one whose repository the targets do not allow. A connection whose
// targets name only owners lists the account's notifications narrowed to the repositories of those owners and
// marks everything read only per repository of those owners. Titles of a notification come from other
// accounts and are untrusted data; a title is cut at a fixed length and says so.
//
// Verified 2026-09-30 against https://docs.github.com/en/rest/activity/notifications: "These endpoints only
// support authentication using a personal access token (classic)" and "All calls to these endpoints require
// the notifications or repo scopes", so a fine-grained token is refused before any request. The list takes
// all, participating, since, before, per_page (at most 50 on the account route, 100 on the repository
// route), and page; a thread is read with GET, marked read with PATCH (205), and marked done with DELETE
// (204); marking everything read answers 202 when GitHub finishes it in the background and 205 otherwise;
// a thread subscription is set with PUT and its ignored flag, and removed with DELETE (204); and
// https://docs.github.com/en/rest/activity/watching: a repository subscription is set with PUT (subscribed
// and ignored) and removed with DELETE (204). GitHub documents no answer for repeating a mark-done, a
// mark-all without last_read_at, or a delete, so those changes declare their idempotency as unknown; the
// mark-read and the two subscription states leave the same state when repeated. Every change is sent once
// and never repeated.

const (
	notificationTitleLimit  = 500
	notificationMaxLimit    = 50
	notificationSensitivity = "github-notifications"
)

const (
	threadIDSchema          = `{"type":"string","minLength":1,"maxLength":20,"pattern":"^[0-9]{1,20}$"}`
	notificationLimitSchema = `{"type":"integer","minimum":1,"maximum":50}`
	notificationKeys        = `"limit":` + notificationLimitSchema + `,"cursor":` + cursorSchema
)

const notificationsTokenMessage = "GitHub supports notifications only with a personal access token (classic) " +
	"that has the notifications scope; this connection's token is a fine-grained token"

const notificationsReadPermission = "GitHub refused this token its notifications; they need a personal access " +
	"token (classic) with the notifications scope (or repo), and fine-grained tokens are not supported"

const notificationsChangePermission = "GitHub refused this change of a notification; it needs a personal access " +
	"token (classic) with the notifications scope (or repo), and fine-grained tokens are not supported"

const repositorySubscriptionPermission = "GitHub refused this change of a repository subscription; check the " +
	"scopes or permissions of the token, and that it can see the repository"

const notificationProperties = `"id":{"type":"string"},"repository":{"type":"string"},"reason":{"type":"string"},` +
	`"unread":{"type":"boolean"},"updated_at":{"type":"string"},"last_read_at":{"type":"string"},` +
	`"subject_title":{"type":"string"},"title_truncated":{"type":"boolean"},"subject_type":{"type":"string"},` +
	`"subject_url":{"type":"string"},"latest_comment_url":{"type":"string"}`

const notificationRequired = `"required":["id","repository","reason","unread","updated_at","subject_title",` +
	`"subject_type"],"additionalProperties":false`

var notificationFields = []capability.Field{
	{Name: "id", Description: "Thread id, the value the thread_id argument of the other notification tools takes"},
	{Name: "repository", Description: "Repository the thread belongs to, as OWNER/REPO"},
	{Name: "reason", Description: "Why the account was notified, such as mention, subscribed, or review_requested"},
	{Name: "unread", Description: "True while the thread is unread"},
	{Name: "updated_at", Description: "When the thread last changed"},
	{Name: "last_read_at", Description: "When the account last read the thread; absent when never"},
	{Name: "subject_title", Description: "Title of the issue, pull request, or other subject; untrusted data, cut " +
		"at 500 characters (title_truncated says so)"},
	{Name: "subject_type", Description: "Kind of subject, such as Issue, PullRequest, Commit, Release, or Discussion"},
	{Name: "subject_url", Description: "API address of the subject"},
	{Name: "latest_comment_url", Description: "API address of the latest comment of the subject"},
}

var notificationPaging = []capability.Argument{
	{Name: "limit", Description: "Entries per batch, from 1 through 50; 30 when omitted; a continuation keeps " +
		"the batch size of its first batch"},
	pagingArguments[1],
}

var notificationsList = capability.Descriptor{
	ID:      Provider + ".notifications.list",
	Version: 1,
	Title:   "List GitHub notifications",
	Description: "List one bounded batch of the notification threads of the account behind the connection's " +
		"token, unread ones unless include_read is set; needs a classic token; with a repository " +
		"only that repository's threads; a connection whose targets name repositories needs repository, one " +
		"whose targets name only owners lists just the threads of those owners",
	Tags:                       []string{"github", "notifications", "inbox", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"include_read":{"type":"boolean"},"participating":{"type":"boolean"},` +
		`"since":` + timeSchema + `,"before":` + timeSchema + `,"repository":` + repoSchema + `,` + notificationKeys),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"notifications":{"type":"array","items":` +
		`{"type":"object","properties":{` + notificationProperties + `},` + notificationRequired + `}},` +
		`"repository":{"type":"string"},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["notifications","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "include_read", Description: "Also list threads already read; unread threads only when omitted"},
		{Name: "participating", Description: "Only threads in which the account takes part or is mentioned"},
		{Name: "since", Description: "Only threads updated at or after this date (YYYY-MM-DD, midnight UTC) or UTC time"},
		{Name: "before", Description: "Only threads updated before this date (YYYY-MM-DD, midnight UTC) or UTC time"},
		{Name: "repository", Description: "Repository as OWNER/REPO whose threads to list; optional unless the " +
			"connection's targets name repositories, then it must lie inside them; without it the whole account " +
			"is listed, narrowed to the owner targets of the connection"},
	}, notificationPaging...),
	Fields: append([]capability.Field{
		{Name: "notifications", Description: "Threads of one batch: id, repository, reason, unread, times, " +
			"subject_title (untrusted data), subject_type, and the subject and latest comment address"},
		{Name: "repository", Description: "Repository the list was bound to; absent for the whole account"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List unread notifications of one repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example","participating":true}`),
	}},
}

var notificationsGet = capability.Descriptor{
	ID:      Provider + ".notifications.get",
	Version: 1,
	Title:   "Get a GitHub notification",
	Description: "Read one notification thread by its id; needs a classic token; a thread of a repository " +
		"outside the connection's targets is refused",
	Tags:                       []string{"github", "notifications", "inbox", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"thread_id":`+threadIDSchema, "thread_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + notificationProperties + `},` +
		notificationRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "thread_id", Description: "Thread id, as github.notifications.list reports it", Required: true},
	},
	Fields: notificationFields,
	Examples: []capability.Example{{
		Description: "Read one notification thread",
		Arguments:   json.RawMessage(`{"thread_id":"1234567890"}`),
	}},
}

var notificationsDismiss = capability.Descriptor{
	ID:      Provider + ".notifications.dismiss",
	Version: 1,
	Title:   "Dismiss a GitHub notification",
	Description: "Mark one notification thread read, or done so it leaves the inbox; the thread is read first " +
		"and a thread of a repository outside the connection's targets is refused; needs a classic token",
	Tags:                       []string{"github", "notifications", "inbox", "dismiss"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"thread_id":`+threadIDSchema+`,"state":{"type":"string","enum":["read","done"]}`,
		"thread_id", "state"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"thread_id":{"type":"string"},` +
		`"repository":{"type":"string"},"state":{"type":"string"}},` +
		`"required":["thread_id","repository","state"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "thread_id", Description: "Thread id, as github.notifications.list reports it", Required: true},
		{Name: "state", Description: "read keeps the thread in the inbox as read; done removes it from the inbox", Required: true},
	},
	Fields: []capability.Field{
		{Name: "thread_id", Description: "The thread that was changed"},
		{Name: "repository", Description: "Repository the thread belongs to, as OWNER/REPO"},
		{Name: "state", Description: "read or done, as requested"},
	},
	Examples: []capability.Example{{
		Description: "Mark a thread done",
		Arguments:   json.RawMessage(`{"thread_id":"1234567890","state":"done"}`),
	}},
}

var notificationsMarkAll = capability.Descriptor{
	ID:      Provider + ".notifications.markall",
	Version: 1,
	Title:   "Mark GitHub notifications read",
	Description: "Mark every notification thread of the account, or of one repository, read, optionally only " +
		"those last read before a time; offered only where a connection's tools list names it; without a " +
		"repository only a connection without targets may mark the whole account",
	Tags:                       []string{"github", "notifications", "inbox", "markall"},
	Risk:                       guardedRisk(capability.EffectUpdate, capability.IdempotencyUnknown, notificationSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"repository":` + repoSchema + `,"last_read_at":` + timeSchema),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"marked":{"type":"boolean"},` +
		`"queued":{"type":"boolean"},"repository":{"type":"string"}},"required":["marked","queued"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "repository", Description: "Repository as OWNER/REPO whose threads to mark; required when the " +
			"connection has any target, and then it must lie inside the targets; the whole account when omitted"},
		{Name: "last_read_at", Description: "Only threads not updated after this date (YYYY-MM-DD, midnight UTC) " +
			"or UTC time are marked; the current time when omitted"},
	},
	Fields: []capability.Field{
		{Name: "marked", Description: "True once GitHub accepted the request"},
		{Name: "queued", Description: "True when GitHub finishes marking in the background (HTTP 202); " +
			"read the list again to see the result"},
		{Name: "repository", Description: "Repository the request was bound to; absent for the whole account"},
	},
	Examples: []capability.Example{{
		Description: "Mark all notifications of a repository read",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}},
}

var subscriptionActions = `"enum":["watch","ignore","delete"]`

var threadSubscriptionsSet = capability.Descriptor{
	ID:      Provider + ".threadsubscriptions.set",
	Version: 1,
	Title:   "Set a GitHub thread subscription",
	Description: "Subscribe to one notification thread (watch), mute it (ignore), or remove the subscription " +
		"(delete); the thread is read first and a thread of a repository outside the connection's targets is " +
		"refused; needs a classic token",
	Tags:                       []string{"github", "notifications", "subscriptions", "thread"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"thread_id":`+threadIDSchema+`,"action":{"type":"string",`+subscriptionActions+`}`,
		"thread_id", "action"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"thread_id":{"type":"string"},` +
		`"repository":{"type":"string"},"action":{"type":"string"},"subscribed":{"type":"boolean"},` +
		`"ignored":{"type":"boolean"}},"required":["thread_id","repository","action","subscribed","ignored"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "thread_id", Description: "Thread id, as github.notifications.list reports it", Required: true},
		{Name: "action", Description: "watch receives notifications of the thread, ignore mutes it, delete " +
			"removes the subscription", Required: true},
	},
	Fields: []capability.Field{
		{Name: "thread_id", Description: "The thread that was changed"},
		{Name: "repository", Description: "Repository the thread belongs to, as OWNER/REPO"},
		{Name: "action", Description: "The action that was applied"},
		{Name: "subscribed", Description: "Whether the account is subscribed to the thread afterwards"},
		{Name: "ignored", Description: "Whether the thread is muted afterwards"},
	},
	Examples: []capability.Example{{
		Description: "Mute a thread",
		Arguments:   json.RawMessage(`{"thread_id":"1234567890","action":"ignore"}`),
	}},
}

var repositorySubscriptionsSet = capability.Descriptor{
	ID:      Provider + ".repositorysubscriptions.set",
	Version: 1,
	Title:   "Set a GitHub repository subscription",
	Description: "Watch a repository an explicit connection allows, ignore all its notifications, or remove the " +
		"subscription (delete) for the account behind the token",
	Tags:                       []string{"github", "notifications", "subscriptions", "repository"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"action":{"type":"string",`+subscriptionActions+`}`, "action"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"},` +
		`"subscribed":{"type":"boolean"},"ignored":{"type":"boolean"}},` +
		`"required":["action","subscribed","ignored"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "action", Description: "watch receives all notifications of the repository, ignore mutes them " +
			"all, delete removes the subscription", Required: true},
	},
	Fields: []capability.Field{
		{Name: "action", Description: "The action that was applied"},
		{Name: "subscribed", Description: "Whether the account watches the repository afterwards"},
		{Name: "ignored", Description: "Whether the account ignores the repository afterwards"},
	},
	Examples: []capability.Example{{
		Description: "Watch a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example","action":"watch"}`),
	}},
}

func notificationOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: notificationsList, Handler: capability.Handler(invokeNotificationsList)},
		{Descriptor: notificationsGet, Handler: capability.Handler(invokeNotificationsGet)},
		{Descriptor: notificationsDismiss, Handler: capability.Handler(invokeNotificationsDismiss)},
		{Descriptor: notificationsMarkAll, Handler: capability.Handler(invokeNotificationsMarkAll)},
		{Descriptor: threadSubscriptionsSet, Handler: capability.Handler(invokeThreadSubscriptionsSet)},
		{Descriptor: repositorySubscriptionsSet, Handler: capability.Handler(invokeRepositorySubscriptionsSet)},
	}
}

// Notification is the compact view of one notification thread.
type Notification struct {
	ID               string `json:"id"`
	Repository       string `json:"repository"`
	Reason           string `json:"reason"`
	Unread           bool   `json:"unread"`
	UpdatedAt        string `json:"updated_at"`
	LastReadAt       string `json:"last_read_at,omitempty"`
	SubjectTitle     string `json:"subject_title"`
	TitleTruncated   bool   `json:"title_truncated,omitempty"`
	SubjectType      string `json:"subject_type"`
	SubjectURL       string `json:"subject_url,omitempty"`
	LatestCommentURL string `json:"latest_comment_url,omitempty"`
}

// NotificationList is one batch of notification threads.
type NotificationList struct {
	Notifications []Notification `json:"notifications"`
	Repository    string         `json:"repository,omitempty"`
	NextCursor    string         `json:"next_cursor,omitempty"`
	HasMore       bool           `json:"has_more"`
}

// NotificationDismissed is the answer of github.notifications.dismiss.
type NotificationDismissed struct {
	ThreadID   string `json:"thread_id"`
	Repository string `json:"repository"`
	State      string `json:"state"`
}

// NotificationsMarked is the answer of github.notifications.markall.
type NotificationsMarked struct {
	Marked     bool   `json:"marked"`
	Queued     bool   `json:"queued"`
	Repository string `json:"repository,omitempty"`
}

// ThreadSubscription is the answer of github.threadsubscriptions.set.
type ThreadSubscription struct {
	ThreadID   string `json:"thread_id"`
	Repository string `json:"repository"`
	Action     string `json:"action"`
	Subscribed bool   `json:"subscribed"`
	Ignored    bool   `json:"ignored"`
}

// RepositorySubscription is the answer of github.repositorysubscriptions.set.
type RepositorySubscription struct {
	Action     string `json:"action"`
	Subscribed bool   `json:"subscribed"`
	Ignored    bool   `json:"ignored"`
}

type notificationJSON struct {
	ID         string `json:"id"`
	Unread     bool   `json:"unread"`
	Reason     string `json:"reason"`
	UpdatedAt  string `json:"updated_at"`
	LastReadAt string `json:"last_read_at"`
	Subject    struct {
		Title            string `json:"title"`
		URL              string `json:"url"`
		LatestCommentURL string `json:"latest_comment_url"`
		Type             string `json:"type"`
	} `json:"subject"`
	Repository struct {
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

// repositoryTarget reads the repository of a thread as a checked target; ok is false for an entry that names
// none Qatlas can address.
func (n notificationJSON) repositoryTarget() (target, bool) {
	parsed, err := parseArgument(kindRepository, n.Repository.FullName)
	return parsed, err == nil
}

func (n notificationJSON) view(op string) (Notification, error) {
	repository, ok := n.repositoryTarget()
	if n.ID == "" || !ok {
		return Notification{}, invalidEntry(op, "a notification")
	}
	title, cut := clipText(n.Subject.Title, notificationTitleLimit)
	return Notification{ID: n.ID, Repository: repository.argument(), Reason: n.Reason, Unread: n.Unread,
		UpdatedAt: n.UpdatedAt, LastReadAt: n.LastReadAt, SubjectTitle: title, TitleTruncated: cut,
		SubjectType: n.Subject.Type, SubjectURL: n.Subject.URL, LatestCommentURL: n.Subject.LatestCommentURL}, nil
}

// admitsRepository reports whether the list lets a tool touch data of one repository: the list is empty, or
// names the repository, or names its owner.
func (a allowlist) admitsRepository(t target) bool {
	return a.allows(t) || (len(a) > 0 && a.ownerNames(t.owner))
}

// selectNotificationScope resolves the optional repository argument of the list and the mark-all tool against
// the connection's targets before a credential is resolved. A repository argument must lie inside the targets,
// as a repository target or through an owner target. Without it, a connection whose targets name a repository
// or a project needs the one repository they name exactly; otherwise the whole account is meant, which the
// mark-all tool allows only for a connection without any target, since GitHub cannot narrow it to an owner.
func selectNotificationScope(resolved *config.Resolved, raw json.RawMessage, wholeAccountNeedsNoTargets bool) (target, bool, error) {
	if resolved == nil {
		return target{}, false, providerError("open", "no connection was selected")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return target{}, false, providerError("open", err.Error())
	}
	var arguments struct {
		Repository string `json:"repository"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return target{}, false, unreadable("select notification scope")
	}
	if arguments.Repository != "" {
		chosen, err := parseArgument(kindRepository, arguments.Repository)
		if err != nil {
			return target{}, false, invalidRequest("repository must be OWNER/REPO")
		}
		if allowed.allows(chosen) {
			chosen, err = allowed.choose(kindRepository, arguments.Repository)
			return chosen, true, err
		}
		if !allowed.admitsRepository(chosen) {
			return target{}, false, invalidRequest("repository is outside the targets of this connection; pass " +
				"one they allow, or add it to the connection's targets")
		}
		return chosen, true, nil
	}
	if allowed.names(kindRepository) || allowed.names(kindProject) {
		chosen, err := allowed.choose(kindRepository, "")
		return chosen, err == nil, err
	}
	if wholeAccountNeedsNoTargets && len(allowed) > 0 {
		return target{}, false, invalidRequest("repository is required because the connection has targets; " +
			"GitHub marks the whole account read, which the targets cannot narrow, so pass a repository they allow")
	}
	return target{}, false, nil
}

// requireThreadTargets refuses, before a credential is resolved, a thread tool on a connection whose targets
// can admit no repository at all.
func requireThreadTargets(resolved *config.Resolved) error {
	if resolved == nil {
		return providerError("open", "no connection was selected")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return providerError("open", err.Error())
	}
	if len(allowed) > 0 && !allowed.names(kindRepository) && !allowed.names(kindOwner) {
		return invalidRequest("the targets of this connection allow no repository; add one or an owner to its " +
			"targets or use another connection")
	}
	return nil
}

func readArguments(raw json.RawMessage, op string, out any) error {
	if json.Unmarshal(raw, out) != nil {
		return unreadable(op)
	}
	return nil
}

// notificationTime normalizes a date or a UTC time to the instant GitHub takes; a date is midnight UTC.
func notificationTime(name, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if t, err := time.Parse(time.DateOnly, value); err == nil {
		return t.UTC().Format(isoInstantLayout), nil
	}
	if _, err := time.Parse(isoInstantLayout, value); err == nil {
		return value, nil
	}
	return "", invalidRequest(name + " must be a date as YYYY-MM-DD or a UTC time as " + isoInstantLayout)
}

// notificationListOptions are the filters and the paging of the list.
type notificationListOptions struct {
	IncludeRead   bool   `json:"include_read"`
	Participating bool   `json:"participating"`
	Since         string `json:"since"`
	Before        string `json:"before"`
	Limit         int    `json:"limit"`
	Cursor        string `json:"cursor"`

	page, perPage int
	binding       []byte
}

func (o *notificationListOptions) normalize(scope target, bound bool) error {
	limit := o.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > notificationMaxLimit {
		return invalidRequest("limit must be between 1 and " + strconv.Itoa(notificationMaxLimit))
	}
	var err error
	if o.Since, err = notificationTime("since", o.Since); err != nil {
		return err
	}
	if o.Before, err = notificationTime("before", o.Before); err != nil {
		return err
	}
	if o.Since != "" && o.Before != "" && o.Since > o.Before {
		return invalidRequest("since must not lie after before")
	}
	name := ""
	if bound {
		name = strings.ToLower(scope.String())
	}
	o.binding = fingerprint("notifications", "list", name, o.IncludeRead, o.Participating, o.Since, o.Before)
	o.page, o.perPage, err = pageOf(o.binding, o.Cursor, limit)
	return err
}

func (o *notificationListOptions) query() url.Values {
	query := url.Values{"per_page": {strconv.Itoa(o.perPage)}, "page": {strconv.Itoa(o.page)}}
	if o.IncludeRead {
		query.Set("all", "true")
	}
	if o.Participating {
		query.Set("participating", "true")
	}
	if o.Since != "" {
		query.Set("since", o.Since)
	}
	if o.Before != "" {
		query.Set("before", o.Before)
	}
	return query
}

func invokeNotificationsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options notificationListOptions
	if err := readArguments(raw, "list notifications", &options); err != nil {
		return nil, err
	}
	scope, bound, err := selectNotificationScope(resolved, raw, false)
	if err != nil {
		return nil, err
	}
	if err := options.normalize(scope, bound); err != nil {
		return nil, err
	}
	client, err := openScoped(ctx, resolved, secrets, red, scope, bound)
	if err != nil {
		return nil, err
	}
	return client.listNotifications(ctx, &options, scope, bound)
}

// openScoped opens a client bound to the repository a notification tool acts on, or unbound for the account.
func openScoped(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	scope target, bound bool) (*Client, error) {
	if bound {
		return openAt(ctx, resolved, secrets, red, scope)
	}
	return Open(ctx, resolved, secrets, red)
}

// requireClassicToken refuses a fine-grained token before any request: GitHub's notification endpoints
// support only a personal access token (classic). The token value is never quoted.
func (c *Client) requireClassicToken(op string) error {
	if strings.HasPrefix(c.auth, "Bearer github_pat_") {
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: notificationsTokenMessage}
	}
	return nil
}

func (c *Client) listNotifications(ctx context.Context, o *notificationListOptions, scope target, bound bool) (*NotificationList, error) {
	const op = "list notifications"
	if err := c.requireClassicToken(op); err != nil {
		return nil, err
	}
	path := "/notifications"
	if bound {
		path = "/repos/" + url.PathEscape(scope.owner) + "/" + url.PathEscape(scope.repo) + "/notifications"
	}
	var raw []notificationJSON
	hasNext, err := c.restPage(ctx, op, path, o.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, notificationsReadPermission)
	}
	result := &NotificationList{Notifications: make([]Notification, 0, len(raw))}
	if bound {
		result.Repository = scope.argument()
	}
	for _, entry := range raw {
		view, err := entry.view(op)
		if err != nil {
			return nil, err
		}
		if !bound && !c.allowed.ownerNames(entry.Repository.Owner.Login) {
			continue
		}
		result.Notifications = append(result.Notifications, view)
	}
	result.HasMore, result.NextCursor = morePage(o.binding, o.page, o.perPage, hasNext)
	return result, nil
}

// notificationThread reads one thread and refuses it unless the connection's targets admit its repository.
func (c *Client) notificationThread(ctx context.Context, op, id string) (notificationJSON, error) {
	if err := c.requireClassicToken(op); err != nil {
		return notificationJSON{}, err
	}
	var thread notificationJSON
	if err := c.rest(ctx, op, "/notifications/threads/"+url.PathEscape(id), &thread); err != nil {
		return notificationJSON{}, actionsFailure(err, notificationsReadPermission)
	}
	repository, ok := thread.repositoryTarget()
	if thread.ID == "" || !ok {
		return notificationJSON{}, invalidEntry(op, "a notification")
	}
	if !c.allowed.admitsRepository(repository) {
		return notificationJSON{}, notFound(op, subject{what: "this notification thread"})
	}
	return thread, nil
}

func checkNotificationThread(id string) error {
	if id == "" || len(id) > 20 || strings.Trim(id, "0123456789") != "" {
		return invalidRequest("thread_id must be the numeric id of a notification thread")
	}
	return nil
}

func invokeNotificationsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		ThreadID string `json:"thread_id"`
	}
	if err := readArguments(raw, "get notification", &a); err != nil {
		return nil, err
	}
	if err := checkNotificationThread(a.ThreadID); err != nil {
		return nil, err
	}
	if err := requireThreadTargets(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "get notification"
	thread, err := client.notificationThread(ctx, op, a.ThreadID)
	if err != nil {
		return nil, err
	}
	return thread.view(op)
}

func invokeNotificationsDismiss(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		ThreadID string `json:"thread_id"`
		State    string `json:"state"`
	}
	if err := readArguments(raw, "dismiss notification", &a); err != nil {
		return nil, err
	}
	if err := checkNotificationThread(a.ThreadID); err != nil {
		return nil, err
	}
	if a.State != "read" && a.State != "done" {
		return nil, invalidRequest("state must be read or done")
	}
	if err := requireThreadTargets(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.dismissNotification(ctx, a.ThreadID, a.State)
}

// dismissNotification marks a thread read (PATCH, 205) or done (DELETE, 204) after reading it once.
func (c *Client) dismissNotification(ctx context.Context, id, state string) (*NotificationDismissed, error) {
	const op = "dismiss notification"
	thread, err := c.notificationThread(ctx, op, id)
	if err != nil {
		return nil, err
	}
	method := http.MethodPatch
	if state == "done" {
		method = http.MethodDelete
	}
	if err := c.restChange(ctx, op, method, "/notifications/threads/"+url.PathEscape(id), struct{}{}, nil); err != nil {
		return nil, actionsFailure(err, notificationsChangePermission)
	}
	return &NotificationDismissed{ThreadID: id, Repository: thread.Repository.FullName, State: state}, nil
}

func invokeNotificationsMarkAll(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		LastReadAt string `json:"last_read_at"`
	}
	if err := readArguments(raw, "mark notifications read", &a); err != nil {
		return nil, err
	}
	scope, bound, err := selectNotificationScope(resolved, raw, true)
	if err != nil {
		return nil, err
	}
	lastRead, err := notificationTime("last_read_at", a.LastReadAt)
	if err != nil {
		return nil, err
	}
	client, err := openScoped(ctx, resolved, secrets, red, scope, bound)
	if err != nil {
		return nil, err
	}
	return client.markAllNotifications(ctx, scope, bound, lastRead)
}

// markAllNotifications marks the threads of the account or of the bound repository read with one request:
// GitHub answers 202 when it finishes in the background and 205 when it is done.
func (c *Client) markAllNotifications(ctx context.Context, scope target, bound bool, lastRead string) (*NotificationsMarked, error) {
	const op = "mark notifications read"
	if err := c.requireClassicToken(op); err != nil {
		return nil, err
	}
	path := "/notifications"
	body := map[string]any{}
	if bound {
		path = "/repos/" + url.PathEscape(scope.owner) + "/" + url.PathEscape(scope.repo) + "/notifications"
	}
	if lastRead != "" {
		body["last_read_at"] = lastRead
	}
	status, err := c.restChangeStatus(ctx, op, http.MethodPut, path, body, nil)
	if err != nil {
		return nil, actionsFailure(err, notificationsChangePermission)
	}
	result := &NotificationsMarked{Marked: true, Queued: status == http.StatusAccepted}
	if bound {
		result.Repository = scope.argument()
	}
	return result, nil
}

func invokeThreadSubscriptionsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		ThreadID string `json:"thread_id"`
		Action   string `json:"action"`
	}
	if err := readArguments(raw, "set thread subscription", &a); err != nil {
		return nil, err
	}
	if err := checkNotificationThread(a.ThreadID); err != nil {
		return nil, err
	}
	if err := checkSubscriptionAction(a.Action); err != nil {
		return nil, err
	}
	if err := requireThreadTargets(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.setThreadSubscription(ctx, a.ThreadID, a.Action)
}

func checkSubscriptionAction(action string) error {
	if action != "watch" && action != "ignore" && action != "delete" {
		return invalidRequest("action must be watch, ignore, or delete")
	}
	return nil
}

type subscriptionJSON struct {
	Subscribed bool `json:"subscribed"`
	Ignored    bool `json:"ignored"`
}

// setThreadSubscription sets or removes the subscription of a thread after reading the thread once. watch and
// ignore are one PUT with the ignored flag, delete is one DELETE.
func (c *Client) setThreadSubscription(ctx context.Context, id, action string) (*ThreadSubscription, error) {
	const op = "set thread subscription"
	thread, err := c.notificationThread(ctx, op, id)
	if err != nil {
		return nil, err
	}
	path := "/notifications/threads/" + url.PathEscape(id) + "/subscription"
	result := &ThreadSubscription{ThreadID: id, Repository: thread.Repository.FullName, Action: action}
	if action == "delete" {
		if err := c.restChange(ctx, op, http.MethodDelete, path, struct{}{}, nil); err != nil {
			return nil, actionsFailure(err, notificationsChangePermission)
		}
		return result, nil
	}
	var answer subscriptionJSON
	if err := c.restChange(ctx, op, http.MethodPut, path, map[string]any{"ignored": action == "ignore"},
		&answer); err != nil {
		return nil, actionsFailure(err, notificationsChangePermission)
	}
	result.Subscribed, result.Ignored = answer.Subscribed, answer.Ignored
	return result, nil
}

func invokeRepositorySubscriptionsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		Action string `json:"action"`
	}
	if err := readArguments(raw, "set repository subscription", &a); err != nil {
		return nil, err
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkSubscriptionAction(a.Action); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.setRepositorySubscription(ctx, a.Action))
}

// setRepositorySubscription sets or removes the account's subscription to the bound repository with one
// request. watch is subscribed without ignored, ignore is ignored without subscribed.
func (c *Client) setRepositorySubscription(ctx context.Context, action string) (*RepositorySubscription, error) {
	const op = "set repository subscription"
	path := c.repoPath("subscription")
	result := &RepositorySubscription{Action: action}
	if action == "delete" {
		if err := c.restChange(ctx, op, http.MethodDelete, path, struct{}{}, nil); err != nil {
			return nil, actionsFailure(err, repositorySubscriptionPermission)
		}
		return result, nil
	}
	var answer subscriptionJSON
	body := map[string]any{"subscribed": action == "watch", "ignored": action == "ignore"}
	if err := c.restChange(ctx, op, http.MethodPut, path, body, &answer); err != nil {
		return nil, actionsFailure(err, repositorySubscriptionPermission)
	}
	result.Subscribed, result.Ignored = answer.Subscribed, answer.Ignored
	return result, nil
}

// notificationsSubject names what a path below /notifications addresses. It names only what the input schema
// allows and never a value GitHub sent.
func notificationsSubject(tail string) subject {
	tail, _, _ = strings.Cut(strings.Trim(tail, "/"), "?")
	parts := strings.Split(tail, "/")
	switch {
	case tail == "":
		return subject{what: "the account's notifications"}
	case parts[0] == "threads" && len(parts) >= 3 && parts[2] == "subscription":
		return subject{what: "the subscription of this notification thread"}
	case parts[0] == "threads":
		return subject{what: "this notification thread"}
	}
	return subject{what: "this resource"}
}

// notificationRepositorySubject names the notifications or the subscription route below a repository.
func notificationRepositorySubject(rest string) string {
	switch strings.Trim(strings.SplitN(rest, "?", 2)[0], "/") {
	case "notifications":
		return "the notifications of this repository"
	case "subscription":
		return "the subscription to this repository"
	}
	return ""
}
