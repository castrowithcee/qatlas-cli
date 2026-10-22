package github

import (
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The views the account, search, collaborator, and team tools share on top of their own members. Everything
// here is new next to what the tools already answered: a member GitHub leaves out or sends empty stays out,
// every text is untrusted data and goes through the shared projection bounds, and an address is never
// fetched. The members a tool answered before keep their own treatment in the tool.

// filled drops an empty text, so a value GitHub sent empty stays out like a missing one.
func filled(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}

func shortText(p *string) *string { return nameText(filled(p)) }

func bulkText(p *string) *string { return longText(filled(p)) }

// ---- Account facets and person

// AccountFacets are the members every account-like object carries beyond its login: ids, view flags, the
// avatar, and the REST addresses.
type AccountFacets struct {
	ID           *int64  `json:"id,omitempty"`
	NodeID       *string `json:"node_id,omitempty"`
	UserViewType *string `json:"user_view_type,omitempty"`
	SiteAdmin    *bool   `json:"site_admin,omitempty"`
	AvatarURL    *string `json:"avatar_url,omitempty"`
	GravatarID   *string `json:"gravatar_id,omitempty"`
	Links        Links   `json:"links,omitempty"`
}

func (r *accountRaw) facets() AccountFacets {
	return AccountFacets{ID: r.ID, NodeID: shortText(r.NodeID), UserViewType: shortText(r.UserViewType),
		SiteAdmin: r.SiteAdmin, AvatarURL: address(r.AvatarURL), GravatarID: shortText(r.GravatarID), Links: r.links}
}

// AccountPerson adds the display name and the email address GitHub reports for an account.
type AccountPerson struct {
	AccountFacets
	Name  *string `json:"name,omitempty"`
	Email *string `json:"email,omitempty"`
}

func (r *accountRaw) person() AccountPerson {
	return AccountPerson{AccountFacets: r.facets(), Name: shortText(r.Name), Email: shortText(r.Email)}
}

var accountFacetsProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},` +
	`"user_view_type":{"type":"string"},"site_admin":{"type":"boolean"},"avatar_url":{"type":"string"},` +
	`"gravatar_id":{"type":"string"},"links":` + linksSchema(restAccountLinkKeys)

var accountPersonProperties = accountFacetsProperties + `,"name":{"type":"string"},"email":{"type":"string"}`

var accountFacetsFields = []capability.Field{
	{Name: "id", Description: "Numeric account id"},
	{Name: "node_id", Description: "GraphQL node id of the account, untrusted data"},
	{Name: "user_view_type", Description: "Whether GitHub shows the account publicly or privately, untrusted data"},
	{Name: "site_admin", Description: "True for a GitHub site administrator"},
	{Name: "avatar_url", Description: "Avatar address, untrusted data, never fetched"},
	{Name: "gravatar_id", Description: "Gravatar id, untrusted data"},
	{Name: "links", Description: "REST url and the other *_url addresses of the account by their GitHub names, " +
		"untrusted data, never fetched"},
}

var accountPersonFields = append(append([]capability.Field(nil), accountFacetsFields...),
	capability.Field{Name: "name", Description: "Display name, untrusted data"},
	capability.Field{Name: "email", Description: "Email address as GitHub reports it, untrusted data"})

// ---- Profile (the public profile members of a user or organization)

type profileRaw struct {
	Bio         *string `json:"bio"`
	Company     *string `json:"company"`
	Blog        *string `json:"blog"`
	Location    *string `json:"location"`
	Hireable    *bool   `json:"hireable"`
	PublicRepos *int64  `json:"public_repos"`
	PublicGists *int64  `json:"public_gists"`
	Followers   *int64  `json:"followers"`
	Following   *int64  `json:"following"`
	CreatedAt   *string `json:"created_at"`
	UpdatedAt   *string `json:"updated_at"`
}

// AccountProfile is the public profile of a user or organization.
type AccountProfile struct {
	Bio         *string `json:"bio,omitempty"`
	Company     *string `json:"company,omitempty"`
	Blog        *string `json:"blog,omitempty"`
	Location    *string `json:"location,omitempty"`
	Hireable    *bool   `json:"hireable,omitempty"`
	PublicRepos *int64  `json:"public_repos,omitempty"`
	PublicGists *int64  `json:"public_gists,omitempty"`
	Followers   *int64  `json:"followers,omitempty"`
	Following   *int64  `json:"following,omitempty"`
	CreatedAt   *string `json:"created_at,omitempty"`
	UpdatedAt   *string `json:"updated_at,omitempty"`
}

func (r *profileRaw) project() AccountProfile {
	return AccountProfile{Bio: bulkText(r.Bio), Company: shortText(r.Company), Blog: bulkText(r.Blog),
		Location: shortText(r.Location), Hireable: r.Hireable, PublicRepos: r.PublicRepos,
		PublicGists: r.PublicGists, Followers: r.Followers, Following: r.Following,
		CreatedAt: shortText(r.CreatedAt), UpdatedAt: shortText(r.UpdatedAt)}
}

const accountProfileProperties = `"bio":{"type":"string"},"company":{"type":"string"},"blog":{"type":"string"},` +
	`"location":{"type":"string"},"hireable":{"type":"boolean"},"public_repos":{"type":"integer"},` +
	`"public_gists":{"type":"integer"},"followers":{"type":"integer"},"following":{"type":"integer"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}`

var accountProfileFields = []capability.Field{
	{Name: "bio", Description: "Profile text, untrusted data"},
	{Name: "company", Description: "Company, untrusted data"},
	{Name: "blog", Description: "Blog or website as the profile states it, untrusted data, never fetched"},
	{Name: "location", Description: "Location, untrusted data"},
	{Name: "hireable", Description: "Whether the account marks itself as available for hire"},
	{Name: "public_repos", Description: "Number of public repositories"},
	{Name: "public_gists", Description: "Number of public gists"},
	{Name: "followers", Description: "Number of followers"},
	{Name: "following", Description: "Number of accounts followed"},
	{Name: "created_at", Description: "Creation time"},
	{Name: "updated_at", Description: "Time of the last change"},
}

// ---- Search extras (score, suspension, text matches)

type textMatchRaw struct {
	ObjectURL  *string `json:"object_url"`
	ObjectType *string `json:"object_type"`
	Property   *string `json:"property"`
	Fragment   *string `json:"fragment"`
	Matches    []struct {
		Text    *string `json:"text"`
		Indices []int64 `json:"indices"`
	} `json:"matches"`
}

// RESTTextMatch is one place a search term matched: the property, a fragment of its text, and the matched
// parts with their indices. Links holds the object_url.
type RESTTextMatch struct {
	ObjectType        *string             `json:"object_type,omitempty"`
	Property          *string             `json:"property,omitempty"`
	Fragment          *string             `json:"fragment,omitempty"`
	FragmentTruncated bool                `json:"fragment_truncated,omitempty"`
	Matches           []RESTTextMatchPart `json:"matches,omitempty"`
	MatchesTruncated  bool                `json:"matches_truncated,omitempty"`
	Links             Links               `json:"links,omitempty"`
}

// RESTTextMatchPart is one matched text with the indices GitHub reports for it.
type RESTTextMatchPart struct {
	Text             *string `json:"text,omitempty"`
	Indices          []int64 `json:"indices,omitempty"`
	IndicesTruncated bool    `json:"indices_truncated,omitempty"`
}

func projectTextMatches(raws []textMatchRaw) ([]RESTTextMatch, bool) {
	raws, cut := capped(raws)
	var out []RESTTextMatch
	for _, raw := range raws {
		fragment, fragmentCut := cleanText(filled(raw.Fragment), projectionTextLimit, true)
		match := RESTTextMatch{ObjectType: shortText(raw.ObjectType), Property: shortText(raw.Property),
			Fragment: fragment, FragmentTruncated: fragmentCut}
		if a := address(raw.ObjectURL); a != nil {
			match.Links = Links{"object_url": *a}
		}
		parts, partsCut := capped(raw.Matches)
		match.MatchesTruncated = partsCut
		for _, part := range parts {
			indices, indicesCut := capped(part.Indices)
			match.Matches = append(match.Matches, RESTTextMatchPart{Text: shortText(part.Text), Indices: indices,
				IndicesTruncated: indicesCut})
		}
		out = append(out, match)
	}
	return out, cut
}

// SearchExtras are the members only a search hit carries.
type SearchExtras struct {
	SuspendedAt          *string         `json:"suspended_at,omitempty"`
	Score                *json.Number    `json:"score,omitempty"`
	TextMatches          []RESTTextMatch `json:"text_matches,omitempty"`
	TextMatchesTruncated bool            `json:"text_matches_truncated,omitempty"`
}

type searchExtrasRaw struct {
	SuspendedAt *string        `json:"suspended_at"`
	Score       *json.Number   `json:"score"`
	TextMatches []textMatchRaw `json:"text_matches"`
}

func (r *searchExtrasRaw) project() SearchExtras {
	extras := SearchExtras{SuspendedAt: shortText(r.SuspendedAt), Score: r.Score}
	extras.TextMatches, extras.TextMatchesTruncated = projectTextMatches(r.TextMatches)
	return extras
}

const textMatchSchema = `{"type":"object","properties":{"object_type":{"type":"string"},` +
	`"property":{"type":"string"},"fragment":{"type":"string"},"fragment_truncated":{"type":"boolean"},` +
	`"matches":{"type":"array","maxItems":100,"items":{"type":"object","properties":{"text":{"type":"string"},` +
	`"indices":{"type":"array","maxItems":100,"items":{"type":"integer"}},"indices_truncated":{"type":"boolean"}},` +
	`"additionalProperties":false}},"matches_truncated":{"type":"boolean"},` +
	`"links":{"type":"object","properties":{"object_url":{"type":"string"}},"additionalProperties":false}},` +
	`"additionalProperties":false}`

const searchExtrasProperties = `"suspended_at":{"type":"string"},"score":{"type":"number"},` +
	`"text_matches":{"type":"array","maxItems":100,"items":` + textMatchSchema + `},` +
	`"text_matches_truncated":{"type":"boolean"}`

var searchExtrasFields = []capability.Field{
	{Name: "suspended_at", Description: "Time the account was suspended"},
	{Name: "score", Description: "Relevance score GitHub gave the match"},
	{Name: "text_matches", Description: "Where the terms matched, only when text_matches is set, at most 100: " +
		"object_type, property, fragment (cut at 1000 characters, fragment_truncated says so), matches (text and " +
		"indices), and links with the object_url; untrusted data, never fetched"},
	{Name: "text_matches_truncated", Description: "True when more text matches followed than shown"},
}

// ---- Team (team, team-simple)

type teamCore struct {
	ID                  *int64  `json:"id"`
	NodeID              *string `json:"node_id"`
	Name                *string `json:"name"`
	Slug                *string `json:"slug"`
	Description         *string `json:"description"`
	Privacy             *string `json:"privacy"`
	NotificationSetting *string `json:"notification_setting"`
	Permission          *string `json:"permission"`
	Type                *string `json:"type"`
	AccessSource        *string `json:"access_source"`
	OrganizationID      *int64  `json:"organization_id"`
	EnterpriseID        *int64  `json:"enterprise_id"`
	LdapDN              *string `json:"ldap_dn"`
	HTMLURL             *string `json:"html_url"`
	links               Links
}

var restTeamLinkKeys = []string{"url", "members_url", "repositories_url"}

func (r *teamCore) UnmarshalJSON(data []byte) (err error) {
	type plain teamCore
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restTeamLinkKeys)
	return err
}

// RESTTeamSimple is the parent team of a team.
type RESTTeamSimple struct {
	ID                  *int64  `json:"id,omitempty"`
	NodeID              *string `json:"node_id,omitempty"`
	Name                *string `json:"name,omitempty"`
	Slug                *string `json:"slug,omitempty"`
	Description         *string `json:"description,omitempty"`
	Privacy             *string `json:"privacy,omitempty"`
	NotificationSetting *string `json:"notification_setting,omitempty"`
	Permission          *string `json:"permission,omitempty"`
	Type                *string `json:"type,omitempty"`
	OrganizationID      *int64  `json:"organization_id,omitempty"`
	EnterpriseID        *int64  `json:"enterprise_id,omitempty"`
	LdapDN              *string `json:"ldap_dn,omitempty"`
	URL                 *string `json:"url,omitempty"`
	Links               Links   `json:"links,omitempty"`
}

func (r *teamCore) simple() *RESTTeamSimple {
	if r == nil {
		return nil
	}
	return &RESTTeamSimple{ID: r.ID, NodeID: shortText(r.NodeID), Name: shortText(r.Name), Slug: shortText(r.Slug),
		Description: bulkText(r.Description), Privacy: shortText(r.Privacy),
		NotificationSetting: shortText(r.NotificationSetting), Permission: shortText(r.Permission),
		Type: shortText(r.Type), OrganizationID: r.OrganizationID, EnterpriseID: r.EnterpriseID,
		LdapDN: shortText(r.LdapDN), URL: address(r.HTMLURL), Links: r.links}
}

const teamLinksProperties = `"links":{"type":"object","properties":{"url":{"type":"string"},` +
	`"members_url":{"type":"string"},"repositories_url":{"type":"string"}},"additionalProperties":false}`

const teamSimpleSchema = `{"type":"object","properties":{"id":{"type":"integer"},"node_id":{"type":"string"},` +
	`"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},` +
	`"privacy":{"type":"string"},"notification_setting":{"type":"string"},"permission":{"type":"string"},` +
	`"type":{"type":"string"},"organization_id":{"type":"integer"},"enterprise_id":{"type":"integer"},` +
	`"ldap_dn":{"type":"string"},"url":{"type":"string"},` + teamLinksProperties + `},"additionalProperties":false}`

const teamPermissionsSchema = `{"type":"object","properties":{"pull":{"type":"boolean"},"triage":{"type":"boolean"},` +
	`"push":{"type":"boolean"},"maintain":{"type":"boolean"},"admin":{"type":"boolean"}},` +
	`"additionalProperties":false}`
