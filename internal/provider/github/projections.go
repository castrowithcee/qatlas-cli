package github

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The shared REST projections: pure mappers from the raw objects of the GitHub REST API to the Qatlas view.
// Missing and null values stay out; nothing is derived. Every text GitHub sent is untrusted data: control
// characters are replaced and the length and count are bounded. An address is never fetched. The web address
// of an object is its url; the REST url and every other *_url of an object sit in its own links.

// Bounds of the projections. An address over its bound is left out, never cut, since a cut address names
// another resource.
const (
	projectionNameLimit = 200
	projectionTextLimit = 1000
	projectionURLLimit  = 2048
	projectionBodyLimit = discussionCommentBodyLimit
	projectionListLimit = 100
)

// Links holds the REST url and the other *_url members of one object by their GitHub names.
type Links map[string]string

// cleanText replaces control characters of untrusted text and cuts it at limit runes. A multi-line text keeps
// its line breaks and tabs.
func cleanText(p *string, limit int, multiline bool) (*string, bool) {
	if p == nil {
		return nil, false
	}
	text := strings.Map(func(r rune) rune {
		if multiline && (r == '\n' || r == '\r' || r == '\t') {
			return r
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, *p)
	text, cut := cutRunes(text, limit)
	return &text, cut
}

func nameText(p *string) *string {
	text, _ := cleanText(p, projectionNameLimit, false)
	return text
}

func longText(p *string) *string {
	text, _ := cleanText(p, projectionTextLimit, false)
	return text
}

// address returns an address as sent, or nil when it is empty, over the bound, or holds a control character.
func address(p *string) *string {
	if p == nil || *p == "" || utf8.RuneCountInString(*p) > projectionURLLimit ||
		strings.IndexFunc(*p, unicode.IsControl) >= 0 {
		return nil
	}
	return p
}

// readLinks collects the addresses named by keys from one JSON object; absent, null, empty, non-string, and
// unusable values are left out.
func readLinks(data []byte, keys []string) (Links, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	links := Links{}
	for _, key := range keys {
		var value *string
		if json.Unmarshal(all[key], &value) != nil {
			continue
		}
		if a := address(value); a != nil {
			links[key] = *a
		}
	}
	if len(links) == 0 {
		return nil, nil
	}
	return links, nil
}

// linksSchema is the schema of the links of an object that holds the given keys.
func linksSchema(keys []string) string {
	var b strings.Builder
	b.WriteString(`{"type":"object","properties":{`)
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + key + `":{"type":"string"}`)
	}
	b.WriteString(`},"additionalProperties":false}`)
	return b.String()
}

// objectSchema wraps schema properties into a closed object.
func objectSchema(properties string) string {
	return `{"type":"object","properties":{` + properties + `},"additionalProperties":false}`
}

// detailName is the sibling of a flat field that carries the object: x_detail for one, x_details for a list.
func detailName(flat string, many bool) string {
	if many {
		return flat + "_details"
	}
	return flat + "_detail"
}

// detailProperty is the schema property of the sibling of a flat field; the flat field stays as it is.
func detailProperty(flat, objectSchema string, many bool) string {
	if many {
		objectSchema = `{"type":"array","items":` + objectSchema + `}`
	}
	return `"` + detailName(flat, many) + `":` + objectSchema
}

// detailField describes the sibling of a flat field.
func detailField(flat, what string, many bool) capability.Field {
	if many {
		return capability.Field{Name: detailName(flat, true), Description: what + " behind " + flat +
			", one object per entry; untrusted data"}
	}
	return capability.Field{Name: detailName(flat, false), Description: what + " behind " + flat +
		"; untrusted data"}
}

// capped returns the first projectionListLimit entries and whether more followed.
func capped[T any](items []T) ([]T, bool) {
	if len(items) <= projectionListLimit {
		return items, false
	}
	return items[:projectionListLimit], true
}

// valueOr returns the value p points to, or the zero value for nil.
func valueOr[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// ---- Account (simple-user; the fields an enterprise shares with it)

var restAccountLinkKeys = []string{"url", "followers_url", "following_url", "gists_url", "starred_url",
	"subscriptions_url", "organizations_url", "repos_url", "events_url", "received_events_url"}

type accountRaw struct {
	Login        *string `json:"login"`
	ID           *int64  `json:"id"`
	NodeID       *string `json:"node_id"`
	Type         *string `json:"type"`
	UserViewType *string `json:"user_view_type"`
	SiteAdmin    *bool   `json:"site_admin"`
	Name         *string `json:"name"`
	Email        *string `json:"email"`
	AvatarURL    *string `json:"avatar_url"`
	GravatarID   *string `json:"gravatar_id"`
	StarredAt    *string `json:"starred_at"`
	HTMLURL      *string `json:"html_url"`
	links        Links
}

func (r *accountRaw) UnmarshalJSON(data []byte) (err error) {
	type plain accountRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restAccountLinkKeys)
	return err
}

// RESTAccount is the view of a user, organization, or bot account.
type RESTAccount struct {
	Login        *string `json:"login,omitempty"`
	ID           *int64  `json:"id,omitempty"`
	NodeID       *string `json:"node_id,omitempty"`
	Type         *string `json:"type,omitempty"`
	UserViewType *string `json:"user_view_type,omitempty"`
	SiteAdmin    *bool   `json:"site_admin,omitempty"`
	Name         *string `json:"name,omitempty"`
	Email        *string `json:"email,omitempty"`
	AvatarURL    *string `json:"avatar_url,omitempty"`
	GravatarID   *string `json:"gravatar_id,omitempty"`
	StarredAt    *string `json:"starred_at,omitempty"`
	URL          *string `json:"url,omitempty"`
	Links        Links   `json:"links,omitempty"`
}

func (r *accountRaw) project() *RESTAccount {
	if r == nil {
		return nil
	}
	return &RESTAccount{Login: nameText(r.Login), ID: r.ID, NodeID: nameText(r.NodeID), Type: nameText(r.Type),
		UserViewType: nameText(r.UserViewType), SiteAdmin: r.SiteAdmin, Name: nameText(r.Name),
		Email: nameText(r.Email), AvatarURL: address(r.AvatarURL), GravatarID: nameText(r.GravatarID),
		StarredAt: nameText(r.StarredAt), URL: address(r.HTMLURL), Links: r.links}
}

var restAccountProperties = `"login":{"type":"string"},"id":{"type":"integer"},"node_id":{"type":"string"},` +
	`"type":{"type":"string"},"user_view_type":{"type":"string"},"site_admin":{"type":"boolean"},` +
	`"name":{"type":"string"},"email":{"type":"string"},"avatar_url":{"type":"string"},` +
	`"gravatar_id":{"type":"string"},"starred_at":{"type":"string"},"url":{"type":"string"},` +
	`"links":` + linksSchema(restAccountLinkKeys)

var restAccountSchema = objectSchema(restAccountProperties)

const restAccountMembers = "login, id, node_id, type, user_view_type, site_admin, name, email, avatar_url, " +
	"gravatar_id, starred_at, url (the web address), and links (the REST url and the other *_url addresses); " +
	"login, name, and email are untrusted data, and an enterprise owner keeps only the members it shares"

var restAccountFields = []capability.Field{
	{Name: "login", Description: "Account name, untrusted data"},
	{Name: "id", Description: "Numeric account id"},
	{Name: "node_id", Description: "GraphQL node id of the account"},
	{Name: "type", Description: "Account type such as User, Organization, or Bot"},
	{Name: "user_view_type", Description: "Whether GitHub shows the account publicly or privately"},
	{Name: "site_admin", Description: "True for a GitHub site administrator"},
	{Name: "name", Description: "Display name, untrusted data"},
	{Name: "email", Description: "Public email address, untrusted data"},
	{Name: "avatar_url", Description: "Avatar address, untrusted data, never fetched"},
	{Name: "gravatar_id", Description: "Gravatar id, untrusted data"},
	{Name: "starred_at", Description: "Time the account starred the repository, in a star list"},
	{Name: "url", Description: "Web address of the account, untrusted data, never fetched"},
	{Name: "links", Description: "REST url and the other *_url addresses of the account by their GitHub names, untrusted data, never fetched"},
}

// ---- Reactions (reaction-rollup)

var reactionLinkKeys = []string{"url"}

type reactionsRaw struct {
	TotalCount *int64 `json:"total_count"`
	PlusOne    *int64 `json:"+1"`
	MinusOne   *int64 `json:"-1"`
	Laugh      *int64 `json:"laugh"`
	Confused   *int64 `json:"confused"`
	Heart      *int64 `json:"heart"`
	Hooray     *int64 `json:"hooray"`
	Eyes       *int64 `json:"eyes"`
	Rocket     *int64 `json:"rocket"`
	links      Links
}

func (r *reactionsRaw) UnmarshalJSON(data []byte) (err error) {
	type plain reactionsRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, reactionLinkKeys)
	return err
}

// RESTReactions is the count of every reaction kind on one object.
type RESTReactions struct {
	TotalCount *int64 `json:"total_count,omitempty"`
	PlusOne    *int64 `json:"plus_one,omitempty"`
	MinusOne   *int64 `json:"minus_one,omitempty"`
	Laugh      *int64 `json:"laugh,omitempty"`
	Confused   *int64 `json:"confused,omitempty"`
	Heart      *int64 `json:"heart,omitempty"`
	Hooray     *int64 `json:"hooray,omitempty"`
	Eyes       *int64 `json:"eyes,omitempty"`
	Rocket     *int64 `json:"rocket,omitempty"`
	Links      Links  `json:"links,omitempty"`
}

func (r *reactionsRaw) project() *RESTReactions {
	if r == nil {
		return nil
	}
	return &RESTReactions{TotalCount: r.TotalCount, PlusOne: r.PlusOne, MinusOne: r.MinusOne, Laugh: r.Laugh,
		Confused: r.Confused, Heart: r.Heart, Hooray: r.Hooray, Eyes: r.Eyes, Rocket: r.Rocket, Links: r.links}
}

var restReactionsProperties = `"total_count":{"type":"integer"},"plus_one":{"type":"integer"},` +
	`"minus_one":{"type":"integer"},"laugh":{"type":"integer"},"confused":{"type":"integer"},` +
	`"heart":{"type":"integer"},"hooray":{"type":"integer"},"eyes":{"type":"integer"},` +
	`"rocket":{"type":"integer"},"links":` + linksSchema(reactionLinkKeys)

var restReactionsSchema = objectSchema(restReactionsProperties)

const restReactionsMembers = "total_count, plus_one, minus_one, laugh, confused, heart, hooray, eyes, rocket, and " +
	"links (the reactions url)"

var restReactionsFields = []capability.Field{
	{Name: "total_count", Description: "Number of all reactions"},
	{Name: "plus_one", Description: "Number of +1 reactions"},
	{Name: "minus_one", Description: "Number of -1 reactions"},
	{Name: "laugh", Description: "Number of laugh reactions"},
	{Name: "confused", Description: "Number of confused reactions"},
	{Name: "heart", Description: "Number of heart reactions"},
	{Name: "hooray", Description: "Number of hooray reactions"},
	{Name: "eyes", Description: "Number of eyes reactions"},
	{Name: "rocket", Description: "Number of rocket reactions"},
	{Name: "links", Description: "Reactions url by its GitHub name, untrusted data, never fetched"},
}

// ---- App (integration)

type appRaw struct {
	ID                 *int64             `json:"id"`
	Slug               *string            `json:"slug"`
	NodeID             *string            `json:"node_id"`
	ClientID           *string            `json:"client_id"`
	Owner              *accountRaw        `json:"owner"`
	Name               *string            `json:"name"`
	Description        *string            `json:"description"`
	ExternalURL        *string            `json:"external_url"`
	HTMLURL            *string            `json:"html_url"`
	CreatedAt          *string            `json:"created_at"`
	UpdatedAt          *string            `json:"updated_at"`
	Permissions        map[string]*string `json:"permissions"`
	Events             []*string          `json:"events"`
	InstallationsCount *int64             `json:"installations_count"`
}

// RESTApp is the view of a GitHub App. Permissions maps a permission name to its level.
type RESTApp struct {
	ID                 *int64            `json:"id,omitempty"`
	Slug               *string           `json:"slug,omitempty"`
	NodeID             *string           `json:"node_id,omitempty"`
	ClientID           *string           `json:"client_id,omitempty"`
	Owner              *RESTAccount      `json:"owner,omitempty"`
	Name               *string           `json:"name,omitempty"`
	Description        *string           `json:"description,omitempty"`
	ExternalURL        *string           `json:"external_url,omitempty"`
	URL                *string           `json:"url,omitempty"`
	CreatedAt          *string           `json:"created_at,omitempty"`
	UpdatedAt          *string           `json:"updated_at,omitempty"`
	Permissions        map[string]string `json:"permissions,omitempty"`
	PermissionsCut     bool              `json:"permissions_truncated,omitempty"`
	Events             []string          `json:"events,omitempty"`
	EventsCut          bool              `json:"events_truncated,omitempty"`
	InstallationsCount *int64            `json:"installations_count,omitempty"`
}

func (r *appRaw) project() *RESTApp {
	if r == nil {
		return nil
	}
	app := &RESTApp{ID: r.ID, Slug: nameText(r.Slug), NodeID: nameText(r.NodeID), ClientID: nameText(r.ClientID),
		Owner: r.Owner.project(), Name: nameText(r.Name), Description: longText(r.Description),
		ExternalURL: address(r.ExternalURL), URL: address(r.HTMLURL), CreatedAt: nameText(r.CreatedAt),
		UpdatedAt: nameText(r.UpdatedAt), InstallationsCount: r.InstallationsCount}
	names := make([]string, 0, len(r.Permissions))
	for name, level := range r.Permissions {
		if level != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names, app.PermissionsCut = capped(names)
	for _, name := range names {
		if app.Permissions == nil {
			app.Permissions = map[string]string{}
		}
		app.Permissions[*nameText(&name)] = *nameText(r.Permissions[name])
	}
	var events []string
	for _, event := range r.Events {
		if event != nil {
			events = append(events, *nameText(event))
		}
	}
	app.Events, app.EventsCut = capped(events)
	return app
}

var restAppProperties = `"id":{"type":"integer"},"slug":{"type":"string"},"node_id":{"type":"string"},` +
	`"client_id":{"type":"string"},"owner":` + restAccountSchema + `,"name":{"type":"string"},` +
	`"description":{"type":"string"},"external_url":{"type":"string"},"url":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"permissions":{"type":"object","maxProperties":100,"additionalProperties":{"type":"string"}},` +
	`"permissions_truncated":{"type":"boolean"},` +
	`"events":{"type":"array","maxItems":100,"items":{"type":"string"}},"events_truncated":{"type":"boolean"},` +
	`"installations_count":{"type":"integer"}`

var restAppSchema = objectSchema(restAppProperties)

const restAppMembers = "id, slug, node_id, client_id, owner (an account), name, description, external_url, url (the " +
	"web address), created_at, updated_at, permissions, events, and installations_count; name, description, " +
	"and the addresses are untrusted data"

var restAppFields = []capability.Field{
	{Name: "id", Description: "Numeric app id"},
	{Name: "slug", Description: "URL name of the app, untrusted data"},
	{Name: "node_id", Description: "GraphQL node id of the app"},
	{Name: "client_id", Description: "OAuth client id of the app"},
	{Name: "owner", Description: "Account that owns the app; an enterprise owner keeps only the account members it shares"},
	{Name: "name", Description: "Name of the app, untrusted data"},
	{Name: "description", Description: "Description of the app, untrusted data"},
	{Name: "external_url", Description: "Homepage of the app, untrusted data, never fetched"},
	{Name: "url", Description: "Web address of the app, untrusted data, never fetched"},
	{Name: "created_at", Description: "Creation time"},
	{Name: "updated_at", Description: "Time of the last change"},
	{Name: "permissions", Description: "Permission name to its level, at most 100 entries, untrusted data"},
	{Name: "permissions_truncated", Description: "True when the app holds more permissions than shown"},
	{Name: "events", Description: "Events the app subscribes to, at most 100, untrusted data"},
	{Name: "events_truncated", Description: "True when the app subscribes to more events than shown"},
	{Name: "installations_count", Description: "Number of installations of the app"},
}

// ---- Issue comment (issue-comment)

var restIssueCommentLinkKeys = []string{"url", "issue_url"}

type issueCommentRaw struct {
	ID                    *int64        `json:"id"`
	NodeID                *string       `json:"node_id"`
	Body                  *string       `json:"body"`
	User                  *accountRaw   `json:"user"`
	CreatedAt             *string       `json:"created_at"`
	UpdatedAt             *string       `json:"updated_at"`
	AuthorAssociation     *string       `json:"author_association"`
	PerformedViaGitHubApp *appRaw       `json:"performed_via_github_app"`
	Reactions             *reactionsRaw `json:"reactions"`
	Pin                   *struct {
		PinnedAt *string     `json:"pinned_at"`
		PinnedBy *accountRaw `json:"pinned_by"`
	} `json:"pin"`
	Minimized *struct {
		Reason *string `json:"reason"`
	} `json:"minimized"`
	HTMLURL *string `json:"html_url"`
	links   Links
}

func (r *issueCommentRaw) UnmarshalJSON(data []byte) (err error) {
	type plain issueCommentRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restIssueCommentLinkKeys)
	return err
}

// RESTIssueComment is the view of one issue comment. The rendered body variants are left out.
type RESTIssueComment struct {
	ID                    *int64                `json:"id,omitempty"`
	NodeID                *string               `json:"node_id,omitempty"`
	Body                  *string               `json:"body,omitempty"`
	BodyTruncated         bool                  `json:"body_truncated,omitempty"`
	User                  *RESTAccount          `json:"user,omitempty"`
	CreatedAt             *string               `json:"created_at,omitempty"`
	UpdatedAt             *string               `json:"updated_at,omitempty"`
	AuthorAssociation     *string               `json:"author_association,omitempty"`
	PerformedViaGitHubApp *RESTApp              `json:"performed_via_github_app,omitempty"`
	Reactions             *RESTReactions        `json:"reactions,omitempty"`
	Pin                   *RESTCommentPin       `json:"pin,omitempty"`
	Minimized             *RESTCommentMinimized `json:"minimized,omitempty"`
	URL                   *string               `json:"url,omitempty"`
	Links                 Links                 `json:"links,omitempty"`
}

// RESTCommentPin tells when and by whom a comment was pinned.
type RESTCommentPin struct {
	PinnedAt *string      `json:"pinned_at,omitempty"`
	PinnedBy *RESTAccount `json:"pinned_by,omitempty"`
}

// RESTCommentMinimized tells why a comment was minimized.
type RESTCommentMinimized struct {
	Reason *string `json:"reason,omitempty"`
}

func (r *issueCommentRaw) project() *RESTIssueComment {
	if r == nil {
		return nil
	}
	body, cut := cleanText(r.Body, projectionBodyLimit, true)
	comment := &RESTIssueComment{ID: r.ID, NodeID: nameText(r.NodeID), Body: body, BodyTruncated: cut,
		User: r.User.project(), CreatedAt: nameText(r.CreatedAt), UpdatedAt: nameText(r.UpdatedAt),
		AuthorAssociation: nameText(r.AuthorAssociation), PerformedViaGitHubApp: r.PerformedViaGitHubApp.project(),
		Reactions: r.Reactions.project(), URL: address(r.HTMLURL), Links: r.links}
	if r.Pin != nil {
		comment.Pin = &RESTCommentPin{PinnedAt: nameText(r.Pin.PinnedAt), PinnedBy: r.Pin.PinnedBy.project()}
	}
	if r.Minimized != nil {
		comment.Minimized = &RESTCommentMinimized{Reason: nameText(r.Minimized.Reason)}
	}
	return comment
}

var restIssueCommentProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"body":{"type":"string"},` +
	`"body_truncated":{"type":"boolean"},"user":` + restAccountSchema + `,"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"author_association":{"type":"string"},` +
	`"performed_via_github_app":` + restAppSchema + `,"reactions":` + restReactionsSchema + `,` +
	`"pin":` + objectSchema(`"pinned_at":{"type":"string"},"pinned_by":`+restAccountSchema) + `,` +
	`"minimized":` + objectSchema(`"reason":{"type":"string"}`) + `,"url":{"type":"string"},` +
	`"links":` + linksSchema(restIssueCommentLinkKeys)

var restIssueCommentSchema = objectSchema(restIssueCommentProperties)

var restIssueCommentFields = []capability.Field{
	{Name: "id", Description: "Numeric comment id"},
	{Name: "node_id", Description: "GraphQL node id of the comment"},
	{Name: "body", Description: "Comment text, cut at 4000 characters (body_truncated says so), untrusted data"},
	{Name: "body_truncated", Description: "True when the body was cut"},
	{Name: "user", Description: "Author as an account: " + restAccountMembers},
	{Name: "created_at", Description: "Creation time"},
	{Name: "updated_at", Description: "Time of the last change"},
	{Name: "author_association", Description: "How the author relates to the repository, such as OWNER or MEMBER"},
	{Name: "performed_via_github_app", Description: "App the comment was made through: " + restAppMembers},
	{Name: "reactions", Description: "Reaction counts: " + restReactionsMembers},
	{Name: "pin", Description: "pinned_at and pinned_by (an account) of a pinned comment"},
	{Name: "minimized", Description: "reason a comment was minimized, untrusted data"},
	{Name: "url", Description: "Web address of the comment, untrusted data, never fetched"},
	{Name: "links", Description: "REST url and issue_url by their GitHub names, untrusted data, never fetched"},
}
