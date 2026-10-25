package github

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The shared GraphQL projections: for each object a selection to embed in a query, the raw type that decodes
// the answer, a pure mapper to the Qatlas view, and the schema and field descriptions a tool includes. The
// rules of the REST projections hold: missing, null, and empty values stay out, nothing is derived, texts are
// untrusted and bounded, addresses are never fetched. The web address of an object is its url; resourcePath
// and every other URI member sit in its own links. Nested objects carry only their scalar, enum, and id
// members and their accounts. Counts and ids are int64; progressPercentage stays a json.Number, so the digits
// GitHub sent reach the output without a float round trip.

// gqlName and gqlText bound untrusted text like nameText and longText and also drop an empty string, which
// GraphQL sends for an absent value such as the email of a user.
func gqlName(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return nameText(p)
}

func gqlText(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return longText(p)
}

// gqlLinks keeps the usable addresses under their keys.
func gqlLinks(keys []string, values ...*string) Links {
	links := Links{}
	for i, key := range keys {
		if a := address(values[i]); a != nil {
			links[key] = *a
		}
	}
	if len(links) == 0 {
		return nil
	}
	return links
}

var gqlResourceLinkKeys = []string{"resource_path"}
var gqlAvatarLinkKeys = []string{"avatar_url"}

// gqlBigInt reads a BigInt, which GitHub sends as a string, and keeps its digits; a value that is not an
// integer reads as absent instead of failing the answer.
type gqlBigInt string

var bigIntPattern = regexp.MustCompile(`^-?[0-9]{1,30}$`)

func (b *gqlBigInt) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if bigIntPattern.MatchString(text) {
		*b = gqlBigInt(text)
	}
	return nil
}

// text returns the digits, or nil when there are none.
func (b *gqlBigInt) text() *string {
	if b == nil || *b == "" {
		return nil
	}
	text := string(*b)
	return &text
}

// ---- Account (Actor: User, Bot, Organization, Mannequin, EnterpriseUserAccount)

// gqlAccountSelection selects an Actor. A member a type lacks is not asked for there and stays out of the view.
const gqlAccountSelection = `__typename login avatarUrl url resourcePath ` +
	`... on User{id databaseId name ` + gateUserEmail + `email` + gateEnd + ` isSiteAdmin} ... on Bot{id databaseId} ` +
	gateOrganization + `... on Organization{id databaseId name email}` + gateEnd + ` ` +
	`... on Mannequin{id databaseId name email} ` +
	gateEnterprise + `... on EnterpriseUserAccount{id name}` + gateEnd

// gqlUserSelection selects a User where the schema names the type itself, such as GitActor.user.
const gqlUserSelection = `__typename id databaseId login name ` + gateUserEmail + `email` + gateEnd +
	` avatarUrl url resourcePath isSiteAdmin`

// gqlOrganizationSelection selects an Organization where the schema names the type itself, such as
// Commit.onBehalfOf; the Actor fragments of gqlAccountSelection do not apply to it. Without read:org only its
// type is asked for, because the members GitHub checks on the type itself are not all known.
const gqlOrganizationSelection = `__typename ` + gateOrganization +
	`id databaseId login name email avatarUrl url resourcePath` + gateEnd

type gqlAccountRaw struct {
	Typename     *string `json:"__typename"`
	Login        *string `json:"login"`
	ID           *string `json:"id"`
	DatabaseID   *int64  `json:"databaseId"`
	Name         *string `json:"name"`
	Email        *string `json:"email"`
	AvatarURL    *string `json:"avatarUrl"`
	URL          *string `json:"url"`
	ResourcePath *string `json:"resourcePath"`
	IsSiteAdmin  *bool   `json:"isSiteAdmin"`
}

// GraphQLAccount is the view of an account inside another object.
type GraphQLAccount struct {
	Login       *string `json:"login,omitempty"`
	Type        *string `json:"type,omitempty"`
	ID          *string `json:"id,omitempty"`
	DatabaseID  *int64  `json:"database_id,omitempty"`
	Name        *string `json:"name,omitempty"`
	Email       *string `json:"email,omitempty"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
	URL         *string `json:"url,omitempty"`
	Links       Links   `json:"links,omitempty"`
	IsSiteAdmin *bool   `json:"is_site_admin,omitempty"`
}

func (r *gqlAccountRaw) project() *GraphQLAccount {
	if r == nil {
		return nil
	}
	a := &GraphQLAccount{Login: gqlName(r.Login), Type: gqlName(r.Typename), ID: gqlName(r.ID),
		DatabaseID: r.DatabaseID, Name: gqlName(r.Name), Email: gqlName(r.Email), AvatarURL: address(r.AvatarURL),
		URL: address(r.URL), Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath)}
	// Only a User has the member; a type that sent it otherwise is not the account the contract describes.
	if r.Typename != nil && *r.Typename == "User" {
		a.IsSiteAdmin = r.IsSiteAdmin
	}
	return a
}

var gqlAccountProperties = `"login":{"type":"string"},"type":{"type":"string"},"id":{"type":"string"},` +
	`"database_id":{"type":"integer"},"name":{"type":"string"},"email":{"type":"string"},` +
	`"avatar_url":{"type":"string"},"url":{"type":"string"},"links":` + linksSchema(gqlResourceLinkKeys) + `,` +
	`"is_site_admin":{"type":"boolean"}`

var gqlAccountSchema = objectSchema(gqlAccountProperties)

const gqlAccountMembers = "login, type (User, Bot, Organization, Mannequin, or EnterpriseUserAccount), id, " +
	"database_id, name, email, avatar_url, url (the web address), links (resource_path), and for a User " +
	"is_site_admin; a member the account type lacks is absent; login, name, email, and the addresses are " +
	"untrusted data"

var gqlAccountFields = []capability.Field{
	{Name: "login", Description: "Account name, untrusted data"},
	{Name: "type", Description: "Account type: User, Bot, Organization, Mannequin, or EnterpriseUserAccount"},
	{Name: "id", Description: "GraphQL node id of the account"},
	{Name: "database_id", Description: "Numeric account id, absent for an enterprise user account"},
	{Name: "name", Description: "Display name, untrusted data, absent for a bot"},
	{Name: "email", Description: "Public email address, untrusted data, absent for a bot and an enterprise user account"},
	{Name: "avatar_url", Description: "Avatar address, untrusted data, never fetched"},
	{Name: "url", Description: "Web address of the account, untrusted data, never fetched"},
	{Name: "links", Description: "resource_path of the account, untrusted data, never fetched"},
	{Name: "is_site_admin", Description: "True for a GitHub site administrator; only a User has it"},
}

// ---- Git actor (GitActor)

const gqlGitActorSelection = `name email date avatarUrl user{` + gqlUserSelection + `}`

type gqlGitActorRaw struct {
	Name      *string        `json:"name"`
	Email     *string        `json:"email"`
	Date      *string        `json:"date"`
	AvatarURL *string        `json:"avatarUrl"`
	User      *gqlAccountRaw `json:"user"`
}

// GraphQLGitActor is the view of the name, email, and time a commit records, with the user they match.
type GraphQLGitActor struct {
	Name  *string         `json:"name,omitempty"`
	Email *string         `json:"email,omitempty"`
	Date  *string         `json:"date,omitempty"`
	User  *GraphQLAccount `json:"user,omitempty"`
	Links Links           `json:"links,omitempty"`
}

func (r *gqlGitActorRaw) project() *GraphQLGitActor {
	if r == nil {
		return nil
	}
	return &GraphQLGitActor{Name: gqlName(r.Name), Email: gqlName(r.Email), Date: gqlName(r.Date),
		User: r.User.project(), Links: gqlLinks(gqlAvatarLinkKeys, r.AvatarURL)}
}

var gqlGitActorProperties = `"name":{"type":"string"},"email":{"type":"string"},"date":{"type":"string"},` +
	`"user":` + gqlAccountSchema + `,"links":` + linksSchema(gqlAvatarLinkKeys)

var gqlGitActorSchema = objectSchema(gqlGitActorProperties)

const gqlGitActorMembers = "name, email, date, user (an account), and links (avatar_url); name, email, and " +
	"the address are untrusted data"

var gqlGitActorFields = []capability.Field{
	{Name: "name", Description: "Name recorded in the commit, untrusted data"},
	{Name: "email", Description: "Email recorded in the commit, untrusted data"},
	{Name: "date", Description: "Time recorded in the commit"},
	{Name: "user", Description: "GitHub user the actor matches: " + gqlAccountMembers},
	{Name: "links", Description: "avatar_url of the actor, untrusted data, never fetched"},
}

// ---- Reaction groups (ReactionGroup)

const gqlReactionGroupSelection = `content createdAt viewerHasReacted reactors{totalCount}`

type gqlReactionGroupRaw struct {
	Content          *string `json:"content"`
	CreatedAt        *string `json:"createdAt"`
	ViewerHasReacted *bool   `json:"viewerHasReacted"`
	Reactors         *struct {
		TotalCount *int64 `json:"totalCount"`
	} `json:"reactors"`
}

// GraphQLReactionGroup is the count of one reaction kind on an object.
type GraphQLReactionGroup struct {
	Content          *string `json:"content,omitempty"`
	CreatedAt        *string `json:"created_at,omitempty"`
	ViewerHasReacted *bool   `json:"viewer_has_reacted,omitempty"`
	Count            *int64  `json:"count,omitempty"`
}

func (r *gqlReactionGroupRaw) project() *GraphQLReactionGroup {
	if r == nil {
		return nil
	}
	g := &GraphQLReactionGroup{Content: gqlName(r.Content), CreatedAt: gqlName(r.CreatedAt),
		ViewerHasReacted: r.ViewerHasReacted}
	if r.Reactors != nil {
		g.Count = r.Reactors.TotalCount
	}
	return g
}

// projectGQLReactionGroups maps a list and leaves out null entries. GitHub sends at most one group per
// reaction kind, so the list needs no cap.
func projectGQLReactionGroups(raw []*gqlReactionGroupRaw) []GraphQLReactionGroup {
	var groups []GraphQLReactionGroup
	for _, r := range raw {
		if g := r.project(); g != nil {
			groups = append(groups, *g)
		}
	}
	return groups
}

var gqlReactionGroupSchema = objectSchema(`"content":{"type":"string"},"created_at":{"type":"string"},` +
	`"viewer_has_reacted":{"type":"boolean"},"count":{"type":"integer"}`)

const gqlReactionGroupMembers = "content, created_at, viewer_has_reacted, and count"

var gqlReactionGroupFields = []capability.Field{
	{Name: "content", Description: "Reaction kind such as THUMBS_UP or HEART"},
	{Name: "created_at", Description: "Time the reaction was created"},
	{Name: "viewer_has_reacted", Description: "True when the authenticated user gave this reaction"},
	{Name: "count", Description: "Number of users who gave this reaction"},
}

// ---- Label (Label)

const gqlLabelSelection = `id name description color isDefault createdAt updatedAt url resourcePath`

type gqlLabelRaw struct {
	ID           *string `json:"id"`
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	Color        *string `json:"color"`
	IsDefault    *bool   `json:"isDefault"`
	CreatedAt    *string `json:"createdAt"`
	UpdatedAt    *string `json:"updatedAt"`
	URL          *string `json:"url"`
	ResourcePath *string `json:"resourcePath"`
}

// GraphQLLabel is the view of a label.
type GraphQLLabel struct {
	ID          *string `json:"id,omitempty"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty"`
	IsDefault   *bool   `json:"is_default,omitempty"`
	CreatedAt   *string `json:"created_at,omitempty"`
	UpdatedAt   *string `json:"updated_at,omitempty"`
	URL         *string `json:"url,omitempty"`
	Links       Links   `json:"links,omitempty"`
}

func (r *gqlLabelRaw) project() *GraphQLLabel {
	if r == nil {
		return nil
	}
	return &GraphQLLabel{ID: gqlName(r.ID), Name: gqlName(r.Name), Description: gqlText(r.Description),
		Color: gqlName(r.Color), IsDefault: r.IsDefault, CreatedAt: gqlName(r.CreatedAt),
		UpdatedAt: gqlName(r.UpdatedAt), URL: address(r.URL), Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath)}
}

var gqlLabelSchema = objectSchema(`"id":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"color":{"type":"string"},"is_default":{"type":"boolean"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"},` +
	`"links":` + linksSchema(gqlResourceLinkKeys))

const gqlLabelMembers = "id, name, description, color, is_default, created_at, updated_at, url (the web " +
	"address), and links (resource_path); name, description, and the addresses are untrusted data"

var gqlLabelFields = []capability.Field{
	{Name: "id", Description: "GraphQL node id of the label"},
	{Name: "name", Description: "Label name, untrusted data"},
	{Name: "description", Description: "Label description, untrusted data"},
	{Name: "color", Description: "Label color as a hex code without #, untrusted data"},
	{Name: "is_default", Description: "True for a label GitHub creates in every new repository"},
	{Name: "created_at", Description: "Creation time"},
	{Name: "updated_at", Description: "Time of the last change"},
	{Name: "url", Description: "Web address of the label, untrusted data, never fetched"},
	{Name: "links", Description: "resource_path of the label, untrusted data, never fetched"},
}

// ---- Milestone (Milestone)

const gqlMilestoneSelection = `id number title description state closed closedAt createdAt updatedAt dueOn ` +
	`openIssueCount closedIssueCount progressPercentage creator{` + gqlAccountSelection + `} ` +
	`viewerCanClose viewerCanReopen url resourcePath`

type gqlMilestoneRaw struct {
	ID               *string        `json:"id"`
	Number           *int64         `json:"number"`
	Title            *string        `json:"title"`
	Description      *string        `json:"description"`
	State            *string        `json:"state"`
	Closed           *bool          `json:"closed"`
	ClosedAt         *string        `json:"closedAt"`
	CreatedAt        *string        `json:"createdAt"`
	UpdatedAt        *string        `json:"updatedAt"`
	DueOn            *string        `json:"dueOn"`
	OpenIssueCount   *int64         `json:"openIssueCount"`
	ClosedIssueCount *int64         `json:"closedIssueCount"`
	ProgressPercent  *json.Number   `json:"progressPercentage"`
	Creator          *gqlAccountRaw `json:"creator"`
	ViewerCanClose   *bool          `json:"viewerCanClose"`
	ViewerCanReopen  *bool          `json:"viewerCanReopen"`
	URL              *string        `json:"url"`
	ResourcePath     *string        `json:"resourcePath"`
}

// GraphQLMilestone is the view of a milestone. ProgressPercentage keeps the digits GitHub sent.
type GraphQLMilestone struct {
	ID                 *string         `json:"id,omitempty"`
	Number             *int64          `json:"number,omitempty"`
	Title              *string         `json:"title,omitempty"`
	Description        *string         `json:"description,omitempty"`
	State              *string         `json:"state,omitempty"`
	Closed             *bool           `json:"closed,omitempty"`
	ClosedAt           *string         `json:"closed_at,omitempty"`
	CreatedAt          *string         `json:"created_at,omitempty"`
	UpdatedAt          *string         `json:"updated_at,omitempty"`
	DueOn              *string         `json:"due_on,omitempty"`
	OpenIssueCount     *int64          `json:"open_issue_count,omitempty"`
	ClosedIssueCount   *int64          `json:"closed_issue_count,omitempty"`
	ProgressPercentage *json.Number    `json:"progress_percentage,omitempty"`
	Creator            *GraphQLAccount `json:"creator,omitempty"`
	ViewerCanClose     *bool           `json:"viewer_can_close,omitempty"`
	ViewerCanReopen    *bool           `json:"viewer_can_reopen,omitempty"`
	URL                *string         `json:"url,omitempty"`
	Links              Links           `json:"links,omitempty"`
}

func (r *gqlMilestoneRaw) project() *GraphQLMilestone {
	if r == nil {
		return nil
	}
	return &GraphQLMilestone{ID: gqlName(r.ID), Number: r.Number, Title: gqlName(r.Title),
		Description: gqlText(r.Description), State: gqlName(r.State), Closed: r.Closed,
		ClosedAt: gqlName(r.ClosedAt), CreatedAt: gqlName(r.CreatedAt), UpdatedAt: gqlName(r.UpdatedAt),
		DueOn: gqlName(r.DueOn), OpenIssueCount: r.OpenIssueCount, ClosedIssueCount: r.ClosedIssueCount,
		ProgressPercentage: r.ProgressPercent, Creator: r.Creator.project(), ViewerCanClose: r.ViewerCanClose,
		ViewerCanReopen: r.ViewerCanReopen, URL: address(r.URL), Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath)}
}

var gqlMilestoneSchema = objectSchema(`"id":{"type":"string"},"number":{"type":"integer"},` +
	`"title":{"type":"string"},"description":{"type":"string"},"state":{"type":"string"},` +
	`"closed":{"type":"boolean"},"closed_at":{"type":"string"},"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"due_on":{"type":"string"},"open_issue_count":{"type":"integer"},` +
	`"closed_issue_count":{"type":"integer"},"progress_percentage":{"type":"number"},` +
	`"creator":` + gqlAccountSchema + `,"viewer_can_close":{"type":"boolean"},` +
	`"viewer_can_reopen":{"type":"boolean"},"url":{"type":"string"},"links":` + linksSchema(gqlResourceLinkKeys))

const gqlMilestoneMembers = "id, number, title, description, state, closed, closed_at, created_at, updated_at, " +
	"due_on, open_issue_count, closed_issue_count, progress_percentage, creator (an account), " +
	"viewer_can_close, viewer_can_reopen, url (the web address), and links (resource_path); title, " +
	"description, and the addresses are untrusted data"

var gqlMilestoneFields = []capability.Field{
	{Name: "id", Description: "GraphQL node id of the milestone"},
	{Name: "number", Description: "Milestone number in the repository"},
	{Name: "title", Description: "Milestone title, untrusted data"},
	{Name: "description", Description: "Milestone description, untrusted data"},
	{Name: "state", Description: "OPEN or CLOSED"},
	{Name: "closed", Description: "True when the milestone is closed"},
	{Name: "closed_at", Description: "Time the milestone was closed"},
	{Name: "created_at", Description: "Creation time"},
	{Name: "updated_at", Description: "Time of the last change"},
	{Name: "due_on", Description: "Due time"},
	{Name: "open_issue_count", Description: "Number of open issues and pull requests in the milestone"},
	{Name: "closed_issue_count", Description: "Number of closed issues and pull requests in the milestone"},
	{Name: "progress_percentage", Description: "Share of closed items, in percent, as GitHub sent it"},
	{Name: "creator", Description: "Account that created the milestone: " + gqlAccountMembers},
	{Name: "viewer_can_close", Description: "True when the authenticated user can close the milestone"},
	{Name: "viewer_can_reopen", Description: "True when the authenticated user can reopen the milestone"},
	{Name: "url", Description: "Web address of the milestone, untrusted data, never fetched"},
	{Name: "links", Description: "resource_path of the milestone, untrusted data, never fetched"},
}

// ---- Issue type (IssueType)

const gqlIssueTypeSelection = `id name description color isEnabled isPrivate`

type gqlIssueTypeRaw struct {
	ID          *string `json:"id"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Color       *string `json:"color"`
	IsEnabled   *bool   `json:"isEnabled"`
	IsPrivate   *bool   `json:"isPrivate"`
}

// GraphQLIssueType is the view of an issue type.
type GraphQLIssueType struct {
	ID          *string `json:"id,omitempty"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty"`
	IsEnabled   *bool   `json:"is_enabled,omitempty"`
	IsPrivate   *bool   `json:"is_private,omitempty"`
}

func (r *gqlIssueTypeRaw) project() *GraphQLIssueType {
	if r == nil {
		return nil
	}
	return &GraphQLIssueType{ID: gqlName(r.ID), Name: gqlName(r.Name), Description: gqlText(r.Description),
		Color: gqlName(r.Color), IsEnabled: r.IsEnabled, IsPrivate: r.IsPrivate}
}

var gqlIssueTypeSchema = objectSchema(`"id":{"type":"string"},"name":{"type":"string"},` +
	`"description":{"type":"string"},"color":{"type":"string"},"is_enabled":{"type":"boolean"},"is_private":{"type":"boolean"}`)

const gqlIssueTypeMembers = "id, name, description, color, is_enabled, and is_private; name and description are untrusted data"

var gqlIssueTypeFields = []capability.Field{
	{Name: "id", Description: "GraphQL node id of the issue type"},
	{Name: "name", Description: "Issue type name, untrusted data"},
	{Name: "description", Description: "Issue type description, untrusted data"},
	{Name: "color", Description: "Issue type color such as BLUE or RED"},
	{Name: "is_enabled", Description: "True when the issue type can be used"},
	{Name: "is_private", Description: "True when the issue type is private"},
}

// ---- Issue and pull request reference (Issue, PullRequest)

// gqlReferenceSelection selects an Issue or a PullRequest as a reference. The repository is the only
// nested object and reduces to its owner/name.
const gqlReferenceSelection = `id number title state url repository{nameWithOwner}`

type gqlReferenceRaw struct {
	ID         *string `json:"id"`
	Number     *int64  `json:"number"`
	Title      *string `json:"title"`
	State      *string `json:"state"`
	URL        *string `json:"url"`
	Repository *struct {
		NameWithOwner *string `json:"nameWithOwner"`
	} `json:"repository"`
}

// GraphQLReference is the view of an issue or pull request named from another object.
type GraphQLReference struct {
	ID         *string `json:"id,omitempty"`
	Number     *int64  `json:"number,omitempty"`
	Title      *string `json:"title,omitempty"`
	State      *string `json:"state,omitempty"`
	URL        *string `json:"url,omitempty"`
	Repository *string `json:"repository,omitempty"`
}

func (r *gqlReferenceRaw) project() *GraphQLReference {
	if r == nil {
		return nil
	}
	ref := &GraphQLReference{ID: gqlName(r.ID), Number: r.Number, Title: gqlName(r.Title),
		State: gqlName(r.State), URL: address(r.URL)}
	if r.Repository != nil {
		ref.Repository = gqlName(r.Repository.NameWithOwner)
	}
	return ref
}

var gqlReferenceSchema = objectSchema(`"id":{"type":"string"},"number":{"type":"integer"},` +
	`"title":{"type":"string"},"state":{"type":"string"},"url":{"type":"string"},"repository":{"type":"string"}`)

const gqlReferenceMembers = "id, number, title, state, url (the web address), and repository (owner/name); " +
	"title and the address are untrusted data"

var gqlReferenceFields = []capability.Field{
	{Name: "id", Description: "GraphQL node id of the issue or pull request"},
	{Name: "number", Description: "Number in its repository"},
	{Name: "title", Description: "Title, untrusted data"},
	{Name: "state", Description: "State such as OPEN, CLOSED, or MERGED"},
	{Name: "url", Description: "Web address, untrusted data, never fetched"},
	{Name: "repository", Description: "Repository as owner/name, untrusted data"},
}
