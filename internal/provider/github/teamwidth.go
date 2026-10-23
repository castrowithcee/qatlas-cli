package github

import (
	"strings"
)

// The team width of the project team list: the members of GitHub's Team beyond the slug and name the list
// answered before. A nested team (the parent and the ancestors) carries its scalar, enum, and id members; the
// organization of a team is the owner of the call and not repeated. The first page of the ancestors, the
// member statuses, and the projects of a team is read with repositoryListCap entries; the members, child
// teams, repositories, and invitations are read with the tool for that object, so the list reports their
// counts. A page of 100 teams asks for 100 * (1 + 1 + 10 + 10 + 10 + 10 + 10) nodes, far below GitHub's limit
// of 500000 nodes per query.

const teamListFirst = "10"

// teamMembers are the scalar, enum, and id members of a team, with the addresses of its pages.
const teamMembers = `id databaseId name slug combinedSlug description privacy notificationSetting createdAt ` +
	`updatedAt reviewRequestDelegationEnabled reviewRequestDelegationAlgorithm reviewRequestDelegationMemberCount ` +
	`reviewRequestDelegationNotifyTeam viewerCanAdminister url resourcePath avatarUrl membersUrl ` +
	`membersResourcePath repositoriesUrl repositoriesResourcePath teamsUrl teamsResourcePath editTeamUrl ` +
	`editTeamResourcePath newTeamUrl newTeamResourcePath`

// teamNodeSelection is the team node of the list.
const teamNodeSelection = teamMembers + ` parentTeam{` + teamMembers + `} ` +
	`ancestors(first:` + teamListFirst + `){totalCount nodes{` + teamMembers + `}} ` +
	`memberStatuses(first:` + teamListFirst + `){totalCount nodes{id createdAt updatedAt emoji message ` +
	`indicatesLimitedAvailability expiresAt user{` + gqlUserSelection + `} organization{` +
	gqlOrganizationSelection + `}}} ` +
	`projectsV2(first:` + teamListFirst + `){totalCount nodes{id number title url}} ` +
	`members{totalCount} childTeams{totalCount} repositories{totalCount} invitations{totalCount}`

var teamLinkKeys = []string{"avatar_url", "members_url", "repositories_url", "teams_url", "edit_team_url",
	"new_team_url", "resource_path", "edit_team_resource_path", "members_resource_path", "new_team_resource_path",
	"repositories_resource_path", "teams_resource_path"}

type gqlTeamRaw struct {
	ID                                 *string `json:"id"`
	DatabaseID                         *int64  `json:"databaseId"`
	Name                               *string `json:"name"`
	Slug                               *string `json:"slug"`
	CombinedSlug                       *string `json:"combinedSlug"`
	Description                        *string `json:"description"`
	Privacy                            *string `json:"privacy"`
	NotificationSetting                *string `json:"notificationSetting"`
	CreatedAt                          *string `json:"createdAt"`
	UpdatedAt                          *string `json:"updatedAt"`
	ReviewRequestDelegationEnabled     *bool   `json:"reviewRequestDelegationEnabled"`
	ReviewRequestDelegationAlgorithm   *string `json:"reviewRequestDelegationAlgorithm"`
	ReviewRequestDelegationMemberCount *int64  `json:"reviewRequestDelegationMemberCount"`
	ReviewRequestDelegationNotifyTeam  *bool   `json:"reviewRequestDelegationNotifyTeam"`
	ViewerCanAdminister                *bool   `json:"viewerCanAdminister"`
	URL                                *string `json:"url"`
	ResourcePath                       *string `json:"resourcePath"`
	AvatarURL                          *string `json:"avatarUrl"`
	MembersURL                         *string `json:"membersUrl"`
	MembersResourcePath                *string `json:"membersResourcePath"`
	RepositoriesURL                    *string `json:"repositoriesUrl"`
	RepositoriesResourcePath           *string `json:"repositoriesResourcePath"`
	TeamsURL                           *string `json:"teamsUrl"`
	TeamsResourcePath                  *string `json:"teamsResourcePath"`
	EditTeamURL                        *string `json:"editTeamUrl"`
	EditTeamResourcePath               *string `json:"editTeamResourcePath"`
	NewTeamURL                         *string `json:"newTeamUrl"`
	NewTeamResourcePath                *string `json:"newTeamResourcePath"`
}

type gqlUserStatusRaw struct {
	ID                           *string        `json:"id"`
	CreatedAt                    *string        `json:"createdAt"`
	UpdatedAt                    *string        `json:"updatedAt"`
	Emoji                        *string        `json:"emoji"`
	Message                      *string        `json:"message"`
	IndicatesLimitedAvailability *bool          `json:"indicatesLimitedAvailability"`
	ExpiresAt                    *string        `json:"expiresAt"`
	User                         *gqlAccountRaw `json:"user"`
	Organization                 *gqlAccountRaw `json:"organization"`
}

type teamProjectRaw struct {
	ID     *string `json:"id"`
	Number *int64  `json:"number"`
	Title  *string `json:"title"`
	URL    *string `json:"url"`
}

// teamNodeRaw is one team of the list.
type teamNodeRaw struct {
	gqlTeamRaw
	ParentTeam     *gqlTeamRaw                 `json:"parentTeam"`
	Ancestors      *gqlNodes[gqlTeamRaw]       `json:"ancestors"`
	MemberStatuses *gqlNodes[gqlUserStatusRaw] `json:"memberStatuses"`
	ProjectsV2     *gqlNodes[teamProjectRaw]   `json:"projectsV2"`
	Members        *gqlTotal                   `json:"members"`
	ChildTeams     *gqlTotal                   `json:"childTeams"`
	Repositories   *gqlTotal                   `json:"repositories"`
	Invitations    *gqlTotal                   `json:"invitations"`
	// unreadable names the members GitHub refused this token in this node.
	unreadable []string
}

// GraphQLTeam is a team inside another object.
type GraphQLTeam struct {
	ID                                 *string `json:"id,omitempty"`
	DatabaseID                         *int64  `json:"database_id,omitempty"`
	Name                               *string `json:"name,omitempty"`
	Slug                               *string `json:"slug,omitempty"`
	CombinedSlug                       *string `json:"combined_slug,omitempty"`
	Description                        *string `json:"description,omitempty"`
	Privacy                            *string `json:"privacy,omitempty"`
	NotificationSetting                *string `json:"notification_setting,omitempty"`
	CreatedAt                          *string `json:"created_at,omitempty"`
	UpdatedAt                          *string `json:"updated_at,omitempty"`
	ReviewRequestDelegationEnabled     *bool   `json:"review_request_delegation_enabled,omitempty"`
	ReviewRequestDelegationAlgorithm   *string `json:"review_request_delegation_algorithm,omitempty"`
	ReviewRequestDelegationMemberCount *int64  `json:"review_request_delegation_member_count,omitempty"`
	ReviewRequestDelegationNotifyTeam  *bool   `json:"review_request_delegation_notify_team,omitempty"`
	ViewerCanAdminister                *bool   `json:"viewer_can_administer,omitempty"`
	URL                                *string `json:"url,omitempty"`
	Links                              Links   `json:"links,omitempty"`
}

func (r *gqlTeamRaw) project() *GraphQLTeam {
	if r == nil {
		return nil
	}
	return &GraphQLTeam{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, Name: gqlName(r.Name), Slug: gqlName(r.Slug),
		CombinedSlug: gqlName(r.CombinedSlug), Description: gqlText(r.Description), Privacy: gqlName(r.Privacy),
		NotificationSetting: gqlName(r.NotificationSetting), CreatedAt: gqlName(r.CreatedAt),
		UpdatedAt: gqlName(r.UpdatedAt), ReviewRequestDelegationEnabled: r.ReviewRequestDelegationEnabled,
		ReviewRequestDelegationAlgorithm:   gqlName(r.ReviewRequestDelegationAlgorithm),
		ReviewRequestDelegationMemberCount: r.ReviewRequestDelegationMemberCount,
		ReviewRequestDelegationNotifyTeam:  r.ReviewRequestDelegationNotifyTeam,
		ViewerCanAdminister:                r.ViewerCanAdminister, URL: address(r.URL),
		Links: gqlLinks(teamLinkKeys, r.AvatarURL, r.MembersURL, r.RepositoriesURL, r.TeamsURL, r.EditTeamURL,
			r.NewTeamURL, r.ResourcePath, r.EditTeamResourcePath, r.MembersResourcePath, r.NewTeamResourcePath,
			r.RepositoriesResourcePath, r.TeamsResourcePath)}
}

// GraphQLUserStatus is the status a member of a team set.
type GraphQLUserStatus struct {
	ID                           *string         `json:"id,omitempty"`
	CreatedAt                    *string         `json:"created_at,omitempty"`
	UpdatedAt                    *string         `json:"updated_at,omitempty"`
	Emoji                        *string         `json:"emoji,omitempty"`
	Message                      *string         `json:"message,omitempty"`
	IndicatesLimitedAvailability *bool           `json:"indicates_limited_availability,omitempty"`
	ExpiresAt                    *string         `json:"expires_at,omitempty"`
	User                         *GraphQLAccount `json:"user,omitempty"`
	Organization                 *GraphQLAccount `json:"organization,omitempty"`
}

func (r *gqlUserStatusRaw) project() *GraphQLUserStatus {
	if r == nil {
		return nil
	}
	return &GraphQLUserStatus{ID: gqlName(r.ID), CreatedAt: gqlName(r.CreatedAt), UpdatedAt: gqlName(r.UpdatedAt),
		Emoji: gqlName(r.Emoji), Message: gqlText(r.Message),
		IndicatesLimitedAvailability: r.IndicatesLimitedAvailability, ExpiresAt: gqlName(r.ExpiresAt),
		User: r.User.project(), Organization: r.Organization.project()}
}

// TeamProject is a project a team is linked to.
type TeamProject struct {
	ID     *string `json:"id,omitempty"`
	Number *int64  `json:"number,omitempty"`
	Title  *string `json:"title,omitempty"`
	URL    *string `json:"url,omitempty"`
}

func (r *teamProjectRaw) project() *TeamProject {
	if r == nil {
		return nil
	}
	return &TeamProject{ID: gqlName(r.ID), Number: r.Number, Title: gqlName(r.Title), URL: address(r.URL)}
}

// TeamWidth is the team beyond the slug and name of the list.
type TeamWidth struct {
	ID                                 *string             `json:"id,omitempty"`
	DatabaseID                         *int64              `json:"database_id,omitempty"`
	Slug                               *string             `json:"slug,omitempty"`
	CombinedSlug                       *string             `json:"combined_slug,omitempty"`
	Description                        *string             `json:"description,omitempty"`
	Privacy                            *string             `json:"privacy,omitempty"`
	NotificationSetting                *string             `json:"notification_setting,omitempty"`
	CreatedAt                          *string             `json:"created_at,omitempty"`
	UpdatedAt                          *string             `json:"updated_at,omitempty"`
	ParentTeam                         *GraphQLTeam        `json:"parent_team,omitempty"`
	ReviewRequestDelegationEnabled     *bool               `json:"review_request_delegation_enabled,omitempty"`
	ReviewRequestDelegationAlgorithm   *string             `json:"review_request_delegation_algorithm,omitempty"`
	ReviewRequestDelegationMemberCount *int64              `json:"review_request_delegation_member_count,omitempty"`
	ReviewRequestDelegationNotifyTeam  *bool               `json:"review_request_delegation_notify_team,omitempty"`
	ViewerCanAdminister                *bool               `json:"viewer_can_administer,omitempty"`
	URL                                *string             `json:"url,omitempty"`
	Links                              Links               `json:"links,omitempty"`
	Ancestors                          []GraphQLTeam       `json:"ancestors,omitempty"`
	AncestorsTruncated                 bool                `json:"ancestors_truncated,omitempty"`
	AncestorsTotalCount                *int64              `json:"ancestors_total_count,omitempty"`
	MemberStatuses                     []GraphQLUserStatus `json:"member_statuses,omitempty"`
	MemberStatusesTruncated            bool                `json:"member_statuses_truncated,omitempty"`
	MemberStatusesTotalCount           *int64              `json:"member_statuses_total_count,omitempty"`
	ProjectsV2                         []TeamProject       `json:"projects_v2,omitempty"`
	ProjectsV2Truncated                bool                `json:"projects_v2_truncated,omitempty"`
	ProjectsV2TotalCount               *int64              `json:"projects_v2_total_count,omitempty"`
	MembersTotalCount                  *int64              `json:"members_total_count,omitempty"`
	ChildTeamsTotalCount               *int64              `json:"child_teams_total_count,omitempty"`
	RepositoriesTotalCount             *int64              `json:"repositories_total_count,omitempty"`
	InvitationsTotalCount              *int64              `json:"invitations_total_count,omitempty"`
	// Unreadable names the members GitHub refused this token inside this team.
	Unreadable []string `json:"unreadable,omitempty"`
}

func (r *teamNodeRaw) width() TeamWidth {
	team := r.gqlTeamRaw.project()
	w := TeamWidth{ID: team.ID, DatabaseID: team.DatabaseID, Slug: team.Slug, CombinedSlug: team.CombinedSlug,
		Description: team.Description, Privacy: team.Privacy, NotificationSetting: team.NotificationSetting,
		CreatedAt: team.CreatedAt, UpdatedAt: team.UpdatedAt, ParentTeam: r.ParentTeam.project(),
		ReviewRequestDelegationEnabled:     team.ReviewRequestDelegationEnabled,
		ReviewRequestDelegationAlgorithm:   team.ReviewRequestDelegationAlgorithm,
		ReviewRequestDelegationMemberCount: team.ReviewRequestDelegationMemberCount,
		ReviewRequestDelegationNotifyTeam:  team.ReviewRequestDelegationNotifyTeam,
		ViewerCanAdminister:                team.ViewerCanAdminister, URL: team.URL, Links: team.Links,
		MembersTotalCount: r.Members.count(), ChildTeamsTotalCount: r.ChildTeams.count(),
		RepositoriesTotalCount: r.Repositories.count(), InvitationsTotalCount: r.Invitations.count()}
	w.Ancestors, w.AncestorsTotalCount, w.AncestorsTruncated = pageItems(r.Ancestors, (*gqlTeamRaw).project)
	w.MemberStatuses, w.MemberStatusesTotalCount, w.MemberStatusesTruncated = pageItems(r.MemberStatuses,
		(*gqlUserStatusRaw).project)
	w.Unreadable = r.unreadable
	w.ProjectsV2, w.ProjectsV2TotalCount, w.ProjectsV2Truncated = pageItems(r.ProjectsV2, (*teamProjectRaw).project)
	return w
}

// ---- Schema

var teamSchema = objectSchema(`"id":{"type":"string"},"database_id":{"type":"integer"},"name":{"type":"string"},` +
	`"slug":{"type":"string"},"combined_slug":{"type":"string"},"description":{"type":"string"},` +
	`"privacy":{"type":"string"},"notification_setting":{"type":"string"},"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"review_request_delegation_enabled":{"type":"boolean"},` +
	`"review_request_delegation_algorithm":{"type":"string"},` +
	`"review_request_delegation_member_count":{"type":"integer"},` +
	`"review_request_delegation_notify_team":{"type":"boolean"},"viewer_can_administer":{"type":"boolean"},` +
	`"url":{"type":"string"},"links":` + linksSchema(teamLinkKeys))

var userStatusSchema = objectSchema(`"id":{"type":"string"},"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"emoji":{"type":"string"},"message":{"type":"string"},` +
	`"indicates_limited_availability":{"type":"boolean"},"expires_at":{"type":"string"},"user":` +
	gqlAccountSchema + `,"organization":` + gqlAccountSchema)

var teamProjectSchema = objectSchema(`"id":{"type":"string"},"number":{"type":"integer"},` +
	`"title":{"type":"string"},"url":{"type":"string"}`)

const teamMembersText = "id, database_id, name, slug, combined_slug, description, privacy, notification_setting, " +
	"created_at, updated_at, the review request delegation settings, viewer_can_administer, url (the web address), " +
	"and links (avatar_url, members_url, repositories_url, teams_url, edit_team_url, new_team_url, and the " +
	"resource paths of those pages); names, descriptions, and addresses are untrusted data, addresses never fetched"

var teamWidthMembers = func() []rwMember {
	members := []rwMember{
		rwObject("unreadable", unreadableSchema, "Members GitHub refused this token, such as "+
			"invitations_total_count or member_statuses without the rights to read them; they are absent from the entry"),
		rwString("id", "GraphQL node id of the team"),
		rwCount("database_id", "Numeric identifier of the team"),
		rwString("slug", "Slug of the team, as in team"),
		rwString("combined_slug", "Slug with the organization, as ORG/SLUG, untrusted data"),
		rwString("description", "Description of the team, untrusted data"),
		rwString("privacy", "SECRET or VISIBLE"),
		rwString("notification_setting", "NOTIFICATIONS_ENABLED or NOTIFICATIONS_DISABLED"),
		rwString("created_at", "Creation time"),
		rwString("updated_at", "Time of the last change"),
		rwObject("parent_team", teamSchema, "Parent team: "+teamMembersText),
		rwFlag("review_request_delegation_enabled", "True when review requests are delegated to members of the team"),
		rwString("review_request_delegation_algorithm", "ROUND_ROBIN or LOAD_BALANCE"),
		rwCount("review_request_delegation_member_count", "Number of members review requests are delegated to"),
		rwFlag("review_request_delegation_notify_team", "True when the whole team is notified of a delegated request"),
		rwFlag("viewer_can_administer", "True when the authenticated user can administer the team"),
		rwString("url", "Web address of the team, untrusted data, never fetched"),
		rwObject("links", linksSchema(teamLinkKeys), "avatar_url, members_url, repositories_url, teams_url, "+
			"edit_team_url, new_team_url, and the resource paths of those pages, untrusted data, never fetched"),
	}
	members = append(members, rwListOf("team", "ancestors", teamSchema, "Ancestors of the team, at most 10: "+
		teamMembersText, true)...)
	members = append(members, rwListOf("team", "member_statuses", userStatusSchema, "Statuses members of the "+
		"team set, at most 10, with id, created_at, updated_at, emoji, message, indicates_limited_availability, "+
		"expires_at, user (an account), and organization (an account); untrusted data", true)...)
	members = append(members, rwListOf("team", "projects_v2", teamProjectSchema, "Projects the team is linked "+
		"to, at most 10, with id, number, title, and url; untrusted data, addresses never fetched", true)...)
	for _, count := range []string{"members", "child_teams", "repositories", "invitations"} {
		members = append(members, rwCount(count+"_total_count", "Number of "+strings.ReplaceAll(count, "_", " ")+
			" the team holds"))
	}
	return members
}()

var (
	teamWidthProperties = rwProperties(teamWidthMembers)
	teamWidthFields     = rwFields(teamWidthMembers)
)
