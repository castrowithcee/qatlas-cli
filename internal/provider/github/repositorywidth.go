package github

import (
	"encoding/json"
	"strings"
)

// The repository width of the owner list: the members of GitHub's GraphQL Repository beyond the compact view
// the list answered before. Nested objects carry their scalar, enum, and id members and their accounts, never
// their own connections. Lists and the first page of a connection are cut at repositoryListCap per repository,
// with a total count and a truncated marker. Rendered variants, deprecated members, members that need a
// lookup argument, deploy keys, and the clone token are not asked for.

// repositoryListCap bounds every list and every connection page read per repository. A page of 100 repositories
// then asks for 100 + 100 * 6 * 10 = 6100 nodes, far below GitHub's limit of 500000 nodes per query; the
// count-only connections add none.
const repositoryListCap = 10

// gqlTotal is a connection read for its size alone.
type gqlTotal struct {
	TotalCount *int64 `json:"totalCount"`
}

func (t *gqlTotal) count() *int64 {
	if t == nil {
		return nil
	}
	return t.TotalCount
}

// gqlNodes is the first page of a connection with its size.
type gqlNodes[T any] struct {
	TotalCount *int64 `json:"totalCount"`
	Nodes      []*T   `json:"nodes"`
}

// pageItems maps the nodes of a first page and reports the size and whether the page holds fewer than the
// connection.
func pageItems[R, V any](page *gqlNodes[R], project func(*R) *V) (items []V, total *int64, truncated bool) {
	if page == nil {
		return nil, nil, false
	}
	for _, node := range page.Nodes {
		if len(items) == repositoryListCap {
			truncated = true
			break
		}
		if v := project(node); v != nil {
			items = append(items, *v)
		}
	}
	if page.TotalCount != nil && *page.TotalCount > int64(len(page.Nodes)) {
		truncated = true
	}
	return items, page.TotalCount, truncated
}

// capList maps a plain list, which GitHub sends whole, and cuts it at repositoryListCap.
func capList[R, V any](raw []R, project func(R) *V) (items []V, truncated bool) {
	for _, r := range raw {
		if len(items) == repositoryListCap {
			return items, true
		}
		if v := project(r); v != nil {
			items = append(items, *v)
		}
	}
	return items, false
}

func gqlBody(p *string) (*string, bool) { return cleanText(filled(p), projectionBodyLimit, true) }

func gqlLinkKeys(keys ...string) []string { return keys }

// ---- Selections

// gqlOwnerSelection selects the owner of a repository, which is a User or an Organization.
const gqlOwnerSelection = `__typename login avatarUrl url resourcePath ` +
	`... on User{id databaseId name email isSiteAdmin} ... on Organization{id databaseId name email}`

const gqlNestedRepositorySelection = `id databaseId fullDatabaseId name nameWithOwner description url createdAt ` +
	`updatedAt pushedAt archivedAt visibility isPrivate isFork isArchived isTemplate isMirror isLocked isDisabled ` +
	`isEmpty isInOrganization forkCount stargazerCount diskUsage homepageUrl resourcePath owner{` +
	gqlOwnerSelection + `}`

const gqlLicenseRuleLists = `conditions{key label description} limitations{key label description} ` +
	`permissions{key label description}`

// repositoryAdminSelection holds the members that can need rights beyond reading the repository: the
// commit emails of the viewer, the interaction limit, the pull request creation cap, the pinned environments,
// the recent projects, and the counts of connections with their own access rules. GitHub refuses them with an
// error for a token without the right, and a refused member of a non-null type empties the whole repository
// node, so they are asked for only when admin_counts is set.
const repositoryAdminSelection = `viewerDefaultCommitEmail viewerPossibleCommitEmails ` +
	`interactionAbility{expiresAt limit origin} pullRequestCreationCapConfig{bypassedUsers{totalCount}} ` +
	`pinnedEnvironments(first:10){totalCount nodes{id databaseId createdAt position ` +
	`environment{id databaseId name isPinned pinnedPosition}}} ` +
	`recentProjects(first:10){totalCount nodes{id fullDatabaseId number title shortDescription readme closed ` +
	`closedAt createdAt updatedAt public template viewerCanClose viewerCanReopen viewerCanUpdate url ` +
	`resourcePath creator{` + gqlAccountSelection + `}}} ` + repositoryAdminTotalsSelection

// repositorySelection is the repository node of the list.
func repositorySelection(adminCounts bool) string {
	if adminCounts {
		return repositoryWidthSelection + " " + repositoryAdminSelection
	}
	return repositoryWidthSelection
}

const repositoryWidthSelection = `id databaseId fullDatabaseId repositoryName:name nameWithOwner description url createdAt ` +
	`updatedAt pushedAt archivedAt isFork isPrivate isTemplate isMirror isLocked lockReason isDisabled isEmpty ` +
	`isInOrganization isBlankIssuesEnabled isSecurityPolicyEnabled isUserConfigurationRepository forkCount ` +
	`stargazerCount diskUsage hasIssuesEnabled hasWikiEnabled hasProjectsEnabled hasDiscussionsEnabled ` +
	`hasPullRequestsEnabled hasSponsorshipsEnabled hasVulnerabilityAlertsEnabled forkingAllowed ` +
	`mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed autoMergeAllowed allowUpdateBranch ` +
	`deleteBranchOnMerge mergeCommitTitle mergeCommitMessage squashMergeCommitTitle squashMergeCommitMessage ` +
	`webCommitSignoffRequired issueCreationPolicy pullRequestCreationPolicy usesCustomOpenGraphImage ` +
	`viewerCanAdminister viewerCanCreateIssues viewerCanSeeIssueFields viewerCanSubscribe viewerCanUpdateTopics ` +
	`viewerContentWarning{category customSubCategory subCategory subTitle title type} ` +
	`viewerDefaultMergeMethod viewerHasStarred ` +
	`viewerPermission viewerSubscription ` +
	`homepageUrl mirrorUrl openGraphImageUrl projectsUrl securityPolicyUrl sshUrl resourcePath projectsResourcePath ` +
	`parent{` + gqlNestedRepositorySelection + `} ` +
	`templateRepository{` + gqlNestedRepositorySelection + `} primaryLanguage{id name color} ` +
	`licenseInfo{id key name nickname spdxId description implementation featured hidden pseudoLicense body url ` +
	gqlLicenseRuleLists + `} ` +
	`codeOfConduct{id key name body url resourcePath} contributingGuidelines{body url resourcePath} ` +
	`defaultBranchRef{id name prefix} ` +
	`latestRelease{id databaseId name tagName description createdAt publishedAt updatedAt immutable isDraft ` +
	`isLatest isPrerelease viewerCanReact author{` + gqlUserSelection + `} url resourcePath} ` +
	`planFeatures{codeowners draftPullRequests maximumAssignees maximumManualReviewRequests teamReviewRequests} ` +
	`contactLinks{about name url} fundingLinks{platform url} ` +
	`issueTemplates{about body filename name title type{` + gqlIssueTypeSelection + `}} ` +
	`pullRequestTemplates{body filename} ` +
	`repositoryTopics(first:10){totalCount nodes{id url resourcePath topic{id name stargazerCount viewerHasStarred}}} ` +
	`pinnedIssues(first:10){totalCount nodes{id databaseId fullDatabaseId issue{` + gqlReferenceSelection + `} ` +
	`pinnedBy{` + gqlAccountSelection + `}}} ` +
	`pinnedDiscussions(first:10){totalCount nodes{id databaseId createdAt updatedAt gradientStopColors pattern ` +
	`preconfiguredGradient discussion{id number title url} pinnedBy{` + gqlAccountSelection + `}}} ` +
	`submodules(first:10){totalCount nodes{branch gitUrl name nameRaw path pathRaw subprojectCommitOid}} ` +
	repositoryTotalsSelection

// ---- Raw nodes

type gqlTopicRaw struct {
	ID               *string `json:"id"`
	Name             *string `json:"name"`
	StargazerCount   *int64  `json:"stargazerCount"`
	ViewerHasStarred *bool   `json:"viewerHasStarred"`
}

type gqlRepositoryTopicRaw struct {
	ID           *string      `json:"id"`
	URL          *string      `json:"url"`
	ResourcePath *string      `json:"resourcePath"`
	Topic        *gqlTopicRaw `json:"topic"`
}

type gqlPinnedIssueRaw struct {
	ID             *string          `json:"id"`
	DatabaseID     *int64           `json:"databaseId"`
	FullDatabaseID *json.Number     `json:"fullDatabaseId"`
	Issue          *gqlReferenceRaw `json:"issue"`
	PinnedBy       *gqlAccountRaw   `json:"pinnedBy"`
}

type gqlDiscussionRefRaw struct {
	ID     *string `json:"id"`
	Number *int64  `json:"number"`
	Title  *string `json:"title"`
	URL    *string `json:"url"`
}

type gqlPinnedDiscussionRaw struct {
	ID                    *string              `json:"id"`
	DatabaseID            *int64               `json:"databaseId"`
	CreatedAt             *string              `json:"createdAt"`
	UpdatedAt             *string              `json:"updatedAt"`
	GradientStopColors    []*string            `json:"gradientStopColors"`
	Pattern               *string              `json:"pattern"`
	PreconfiguredGradient *string              `json:"preconfiguredGradient"`
	Discussion            *gqlDiscussionRefRaw `json:"discussion"`
	PinnedBy              *gqlAccountRaw       `json:"pinnedBy"`
}

type gqlEnvironmentRaw struct {
	ID             *string `json:"id"`
	DatabaseID     *int64  `json:"databaseId"`
	Name           *string `json:"name"`
	IsPinned       *bool   `json:"isPinned"`
	PinnedPosition *int64  `json:"pinnedPosition"`
}

type gqlPinnedEnvironmentRaw struct {
	ID          *string            `json:"id"`
	DatabaseID  *int64             `json:"databaseId"`
	CreatedAt   *string            `json:"createdAt"`
	Position    *int64             `json:"position"`
	Environment *gqlEnvironmentRaw `json:"environment"`
}

type gqlProjectRaw struct {
	ID               *string        `json:"id"`
	FullDatabaseID   *json.Number   `json:"fullDatabaseId"`
	Number           *int64         `json:"number"`
	Title            *string        `json:"title"`
	ShortDescription *string        `json:"shortDescription"`
	Readme           *string        `json:"readme"`
	Closed           *bool          `json:"closed"`
	ClosedAt         *string        `json:"closedAt"`
	CreatedAt        *string        `json:"createdAt"`
	UpdatedAt        *string        `json:"updatedAt"`
	Public           *bool          `json:"public"`
	Template         *bool          `json:"template"`
	ViewerCanClose   *bool          `json:"viewerCanClose"`
	ViewerCanReopen  *bool          `json:"viewerCanReopen"`
	ViewerCanUpdate  *bool          `json:"viewerCanUpdate"`
	URL              *string        `json:"url"`
	ResourcePath     *string        `json:"resourcePath"`
	Creator          *gqlAccountRaw `json:"creator"`
}

type gqlSubmoduleRaw struct {
	Branch              *string `json:"branch"`
	GitURL              *string `json:"gitUrl"`
	Name                *string `json:"name"`
	NameRaw             *string `json:"nameRaw"`
	Path                *string `json:"path"`
	PathRaw             *string `json:"pathRaw"`
	SubprojectCommitOid *string `json:"subprojectCommitOid"`
}

type gqlLanguageRaw struct {
	ID    *string `json:"id"`
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

type gqlLicenseRuleRaw struct {
	Key         *string `json:"key"`
	Label       *string `json:"label"`
	Description *string `json:"description"`
}

type gqlLicenseRaw struct {
	ID             *string              `json:"id"`
	Key            *string              `json:"key"`
	Name           *string              `json:"name"`
	Nickname       *string              `json:"nickname"`
	SPDXID         *string              `json:"spdxId"`
	Description    *string              `json:"description"`
	Implementation *string              `json:"implementation"`
	Featured       *bool                `json:"featured"`
	Hidden         *bool                `json:"hidden"`
	PseudoLicense  *bool                `json:"pseudoLicense"`
	Body           *string              `json:"body"`
	URL            *string              `json:"url"`
	Conditions     []*gqlLicenseRuleRaw `json:"conditions"`
	Limitations    []*gqlLicenseRuleRaw `json:"limitations"`
	Permissions    []*gqlLicenseRuleRaw `json:"permissions"`
}

type gqlCodeOfConductRaw struct {
	ID           *string `json:"id"`
	Key          *string `json:"key"`
	Name         *string `json:"name"`
	Body         *string `json:"body"`
	URL          *string `json:"url"`
	ResourcePath *string `json:"resourcePath"`
}

type gqlGuidelinesRaw struct {
	Body         *string `json:"body"`
	URL          *string `json:"url"`
	ResourcePath *string `json:"resourcePath"`
}

type gqlRefRaw struct {
	ID     *string `json:"id"`
	Name   *string `json:"name"`
	Prefix *string `json:"prefix"`
}

type gqlReleaseRaw struct {
	ID             *string        `json:"id"`
	DatabaseID     *int64         `json:"databaseId"`
	Name           *string        `json:"name"`
	TagName        *string        `json:"tagName"`
	Description    *string        `json:"description"`
	CreatedAt      *string        `json:"createdAt"`
	PublishedAt    *string        `json:"publishedAt"`
	UpdatedAt      *string        `json:"updatedAt"`
	Immutable      *bool          `json:"immutable"`
	IsDraft        *bool          `json:"isDraft"`
	IsLatest       *bool          `json:"isLatest"`
	IsPrerelease   *bool          `json:"isPrerelease"`
	ViewerCanReact *bool          `json:"viewerCanReact"`
	Author         *gqlAccountRaw `json:"author"`
	URL            *string        `json:"url"`
	ResourcePath   *string        `json:"resourcePath"`
}

type gqlInteractionAbilityRaw struct {
	ExpiresAt *string `json:"expiresAt"`
	Limit     *string `json:"limit"`
	Origin    *string `json:"origin"`
}

type gqlPlanFeaturesRaw struct {
	Codeowners                  *bool  `json:"codeowners"`
	DraftPullRequests           *bool  `json:"draftPullRequests"`
	MaximumAssignees            *int64 `json:"maximumAssignees"`
	MaximumManualReviewRequests *int64 `json:"maximumManualReviewRequests"`
	TeamReviewRequests          *bool  `json:"teamReviewRequests"`
}

type gqlContactLinkRaw struct {
	About *string `json:"about"`
	Name  *string `json:"name"`
	URL   *string `json:"url"`
}

type gqlFundingLinkRaw struct {
	Platform *string `json:"platform"`
	URL      *string `json:"url"`
}

type gqlIssueTemplateRaw struct {
	About    *string          `json:"about"`
	Body     *string          `json:"body"`
	Filename *string          `json:"filename"`
	Name     *string          `json:"name"`
	Title    *string          `json:"title"`
	Type     *gqlIssueTypeRaw `json:"type"`
}

type gqlPullRequestTemplateRaw struct {
	Body     *string `json:"body"`
	Filename *string `json:"filename"`
}

type gqlContentWarningRaw struct {
	Category          *string `json:"category"`
	CustomSubCategory *string `json:"customSubCategory"`
	SubCategory       *string `json:"subCategory"`
	SubTitle          *string `json:"subTitle"`
	Title             *string `json:"title"`
	Type              *string `json:"type"`
}

type gqlNestedRepositoryRaw struct {
	ID               *string        `json:"id"`
	DatabaseID       *int64         `json:"databaseId"`
	FullDatabaseID   *json.Number   `json:"fullDatabaseId"`
	Name             *string        `json:"name"`
	NameWithOwner    *string        `json:"nameWithOwner"`
	Description      *string        `json:"description"`
	URL              *string        `json:"url"`
	CreatedAt        *string        `json:"createdAt"`
	UpdatedAt        *string        `json:"updatedAt"`
	PushedAt         *string        `json:"pushedAt"`
	ArchivedAt       *string        `json:"archivedAt"`
	Visibility       *string        `json:"visibility"`
	IsPrivate        *bool          `json:"isPrivate"`
	IsFork           *bool          `json:"isFork"`
	IsArchived       *bool          `json:"isArchived"`
	IsTemplate       *bool          `json:"isTemplate"`
	IsMirror         *bool          `json:"isMirror"`
	IsLocked         *bool          `json:"isLocked"`
	IsDisabled       *bool          `json:"isDisabled"`
	IsEmpty          *bool          `json:"isEmpty"`
	IsInOrganization *bool          `json:"isInOrganization"`
	ForkCount        *int64         `json:"forkCount"`
	StargazerCount   *int64         `json:"stargazerCount"`
	DiskUsage        *int64         `json:"diskUsage"`
	HomepageURL      *string        `json:"homepageUrl"`
	ResourcePath     *string        `json:"resourcePath"`
	Owner            *gqlAccountRaw `json:"owner"`
}

// repositoryWidthRaw is the part of one GraphQL Repository the repository width reads; it is decoded beside the
// compact view of the list, from the same node.
type repositoryWidthRaw struct {
	ID             *string      `json:"id"`
	DatabaseID     *int64       `json:"databaseId"`
	FullDatabaseID *json.Number `json:"fullDatabaseId"`
	// RepositoryName is asked for under its own key, so the name of the compact view does not shadow it.
	Name                          *string                   `json:"repositoryName"`
	NameWithOwner                 *string                   `json:"nameWithOwner"`
	Description                   *string                   `json:"description"`
	URL                           *string                   `json:"url"`
	CreatedAt                     *string                   `json:"createdAt"`
	UpdatedAt                     *string                   `json:"updatedAt"`
	PushedAt                      *string                   `json:"pushedAt"`
	ArchivedAt                    *string                   `json:"archivedAt"`
	IsFork                        *bool                     `json:"isFork"`
	IsPrivate                     *bool                     `json:"isPrivate"`
	IsTemplate                    *bool                     `json:"isTemplate"`
	IsMirror                      *bool                     `json:"isMirror"`
	IsLocked                      *bool                     `json:"isLocked"`
	LockReason                    *string                   `json:"lockReason"`
	IsDisabled                    *bool                     `json:"isDisabled"`
	IsEmpty                       *bool                     `json:"isEmpty"`
	IsInOrganization              *bool                     `json:"isInOrganization"`
	IsBlankIssuesEnabled          *bool                     `json:"isBlankIssuesEnabled"`
	IsSecurityPolicyEnabled       *bool                     `json:"isSecurityPolicyEnabled"`
	IsUserConfigurationRepository *bool                     `json:"isUserConfigurationRepository"`
	ForkCount                     *int64                    `json:"forkCount"`
	StargazerCount                *int64                    `json:"stargazerCount"`
	DiskUsage                     *int64                    `json:"diskUsage"`
	HasIssuesEnabled              *bool                     `json:"hasIssuesEnabled"`
	HasWikiEnabled                *bool                     `json:"hasWikiEnabled"`
	HasProjectsEnabled            *bool                     `json:"hasProjectsEnabled"`
	HasDiscussionsEnabled         *bool                     `json:"hasDiscussionsEnabled"`
	HasPullRequestsEnabled        *bool                     `json:"hasPullRequestsEnabled"`
	HasSponsorshipsEnabled        *bool                     `json:"hasSponsorshipsEnabled"`
	HasVulnerabilityAlertsEnabled *bool                     `json:"hasVulnerabilityAlertsEnabled"`
	ForkingAllowed                *bool                     `json:"forkingAllowed"`
	MergeCommitAllowed            *bool                     `json:"mergeCommitAllowed"`
	SquashMergeAllowed            *bool                     `json:"squashMergeAllowed"`
	RebaseMergeAllowed            *bool                     `json:"rebaseMergeAllowed"`
	AutoMergeAllowed              *bool                     `json:"autoMergeAllowed"`
	AllowUpdateBranch             *bool                     `json:"allowUpdateBranch"`
	DeleteBranchOnMerge           *bool                     `json:"deleteBranchOnMerge"`
	MergeCommitTitle              *string                   `json:"mergeCommitTitle"`
	MergeCommitMessage            *string                   `json:"mergeCommitMessage"`
	SquashMergeCommitTitle        *string                   `json:"squashMergeCommitTitle"`
	SquashMergeCommitMessage      *string                   `json:"squashMergeCommitMessage"`
	WebCommitSignoffRequired      *bool                     `json:"webCommitSignoffRequired"`
	IssueCreationPolicy           *string                   `json:"issueCreationPolicy"`
	PullRequestCreationPolicy     *string                   `json:"pullRequestCreationPolicy"`
	UsesCustomOpenGraphImage      *bool                     `json:"usesCustomOpenGraphImage"`
	ViewerCanAdminister           *bool                     `json:"viewerCanAdminister"`
	ViewerCanCreateIssues         *bool                     `json:"viewerCanCreateIssues"`
	ViewerCanSeeIssueFields       *bool                     `json:"viewerCanSeeIssueFields"`
	ViewerCanSubscribe            *bool                     `json:"viewerCanSubscribe"`
	ViewerCanUpdateTopics         *bool                     `json:"viewerCanUpdateTopics"`
	ViewerContentWarning          *gqlContentWarningRaw     `json:"viewerContentWarning"`
	ViewerDefaultCommitEmail      *string                   `json:"viewerDefaultCommitEmail"`
	ViewerPossibleCommitEmails    []*string                 `json:"viewerPossibleCommitEmails"`
	ViewerDefaultMergeMethod      *string                   `json:"viewerDefaultMergeMethod"`
	ViewerHasStarred              *bool                     `json:"viewerHasStarred"`
	ViewerPermission              *string                   `json:"viewerPermission"`
	ViewerSubscription            *string                   `json:"viewerSubscription"`
	HomepageURL                   *string                   `json:"homepageUrl"`
	MirrorURL                     *string                   `json:"mirrorUrl"`
	OpenGraphImageURL             *string                   `json:"openGraphImageUrl"`
	ProjectsURL                   *string                   `json:"projectsUrl"`
	SecurityPolicyURL             *string                   `json:"securityPolicyUrl"`
	SSHURL                        *string                   `json:"sshUrl"`
	ResourcePath                  *string                   `json:"resourcePath"`
	ProjectsResourcePath          *string                   `json:"projectsResourcePath"`
	Parent                        *gqlNestedRepositoryRaw   `json:"parent"`
	TemplateRepository            *gqlNestedRepositoryRaw   `json:"templateRepository"`
	PrimaryLanguage               *gqlLanguageRaw           `json:"primaryLanguage"`
	LicenseInfo                   *gqlLicenseRaw            `json:"licenseInfo"`
	CodeOfConduct                 *gqlCodeOfConductRaw      `json:"codeOfConduct"`
	ContributingGuidelines        *gqlGuidelinesRaw         `json:"contributingGuidelines"`
	DefaultBranchRef              *gqlRefRaw                `json:"defaultBranchRef"`
	LatestRelease                 *gqlReleaseRaw            `json:"latestRelease"`
	InteractionAbility            *gqlInteractionAbilityRaw `json:"interactionAbility"`
	PlanFeatures                  *gqlPlanFeaturesRaw       `json:"planFeatures"`
	PullRequestCreationCapConfig  *struct {
		BypassedUsers *gqlTotal `json:"bypassedUsers"`
	} `json:"pullRequestCreationCapConfig"`
	ContactLinks         []*gqlContactLinkRaw               `json:"contactLinks"`
	FundingLinks         []*gqlFundingLinkRaw               `json:"fundingLinks"`
	IssueTemplates       []*gqlIssueTemplateRaw             `json:"issueTemplates"`
	PullRequestTemplates []*gqlPullRequestTemplateRaw       `json:"pullRequestTemplates"`
	RepositoryTopics     *gqlNodes[gqlRepositoryTopicRaw]   `json:"repositoryTopics"`
	PinnedIssues         *gqlNodes[gqlPinnedIssueRaw]       `json:"pinnedIssues"`
	PinnedDiscussions    *gqlNodes[gqlPinnedDiscussionRaw]  `json:"pinnedDiscussions"`
	PinnedEnvironments   *gqlNodes[gqlPinnedEnvironmentRaw] `json:"pinnedEnvironments"`
	RecentProjects       *gqlNodes[gqlProjectRaw]           `json:"recentProjects"`
	Submodules           *gqlNodes[gqlSubmoduleRaw]         `json:"submodules"`
	repositoryTotalsRaw
}

// ---- Views

// GraphQLTopic is the topic of a repository topic.
type GraphQLTopic struct {
	ID               *string `json:"id,omitempty"`
	Name             *string `json:"name,omitempty"`
	StargazerCount   *int64  `json:"stargazer_count,omitempty"`
	ViewerHasStarred *bool   `json:"viewer_has_starred,omitempty"`
}

// GraphQLRepositoryTopic is one topic a repository carries.
type GraphQLRepositoryTopic struct {
	ID    *string       `json:"id,omitempty"`
	Topic *GraphQLTopic `json:"topic,omitempty"`
	URL   *string       `json:"url,omitempty"`
	Links Links         `json:"links,omitempty"`
}

// GraphQLPinnedIssue is one issue pinned in a repository.
type GraphQLPinnedIssue struct {
	ID             *string           `json:"id,omitempty"`
	DatabaseID     *int64            `json:"database_id,omitempty"`
	FullDatabaseID *json.Number      `json:"full_database_id,omitempty"`
	Issue          *GraphQLReference `json:"issue,omitempty"`
	PinnedBy       *GraphQLAccount   `json:"pinned_by,omitempty"`
}

// GraphQLDiscussionRef is the discussion a pinned discussion names.
type GraphQLDiscussionRef struct {
	ID     *string `json:"id,omitempty"`
	Number *int64  `json:"number,omitempty"`
	Title  *string `json:"title,omitempty"`
	URL    *string `json:"url,omitempty"`
}

// GraphQLPinnedDiscussion is one discussion pinned in a repository.
type GraphQLPinnedDiscussion struct {
	ID                    *string               `json:"id,omitempty"`
	DatabaseID            *int64                `json:"database_id,omitempty"`
	CreatedAt             *string               `json:"created_at,omitempty"`
	UpdatedAt             *string               `json:"updated_at,omitempty"`
	GradientStopColors    []string              `json:"gradient_stop_colors,omitempty"`
	Pattern               *string               `json:"pattern,omitempty"`
	PreconfiguredGradient *string               `json:"preconfigured_gradient,omitempty"`
	Discussion            *GraphQLDiscussionRef `json:"discussion,omitempty"`
	PinnedBy              *GraphQLAccount       `json:"pinned_by,omitempty"`
}

// GraphQLEnvironment is the environment a pinned environment names.
type GraphQLEnvironment struct {
	ID             *string `json:"id,omitempty"`
	DatabaseID     *int64  `json:"database_id,omitempty"`
	Name           *string `json:"name,omitempty"`
	IsPinned       *bool   `json:"is_pinned,omitempty"`
	PinnedPosition *int64  `json:"pinned_position,omitempty"`
}

// GraphQLPinnedEnvironment is one environment pinned in a repository.
type GraphQLPinnedEnvironment struct {
	ID          *string             `json:"id,omitempty"`
	DatabaseID  *int64              `json:"database_id,omitempty"`
	CreatedAt   *string             `json:"created_at,omitempty"`
	Position    *int64              `json:"position,omitempty"`
	Environment *GraphQLEnvironment `json:"environment,omitempty"`
}

// GraphQLProject is a project a repository is recently used in.
type GraphQLProject struct {
	ID               *string         `json:"id,omitempty"`
	FullDatabaseID   *json.Number    `json:"full_database_id,omitempty"`
	Number           *int64          `json:"number,omitempty"`
	Title            *string         `json:"title,omitempty"`
	ShortDescription *string         `json:"short_description,omitempty"`
	Readme           *string         `json:"readme,omitempty"`
	ReadmeTruncated  bool            `json:"readme_truncated,omitempty"`
	Closed           *bool           `json:"closed,omitempty"`
	ClosedAt         *string         `json:"closed_at,omitempty"`
	CreatedAt        *string         `json:"created_at,omitempty"`
	UpdatedAt        *string         `json:"updated_at,omitempty"`
	Public           *bool           `json:"public,omitempty"`
	Template         *bool           `json:"template,omitempty"`
	ViewerCanClose   *bool           `json:"viewer_can_close,omitempty"`
	ViewerCanReopen  *bool           `json:"viewer_can_reopen,omitempty"`
	ViewerCanUpdate  *bool           `json:"viewer_can_update,omitempty"`
	Creator          *GraphQLAccount `json:"creator,omitempty"`
	URL              *string         `json:"url,omitempty"`
	Links            Links           `json:"links,omitempty"`
}

// GraphQLSubmodule is a submodule of a repository.
type GraphQLSubmodule struct {
	Branch              *string `json:"branch,omitempty"`
	Name                *string `json:"name,omitempty"`
	NameRaw             *string `json:"name_raw,omitempty"`
	Path                *string `json:"path,omitempty"`
	PathRaw             *string `json:"path_raw,omitempty"`
	SubprojectCommitOid *string `json:"subproject_commit_oid,omitempty"`
	Links               Links   `json:"links,omitempty"`
}

// GraphQLLanguage is the primary language of a repository.
type GraphQLLanguage struct {
	ID    *string `json:"id,omitempty"`
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
}

// GraphQLLicenseRule is one condition, limitation, or permission of a license.
type GraphQLLicenseRule struct {
	Key         *string `json:"key,omitempty"`
	Label       *string `json:"label,omitempty"`
	Description *string `json:"description,omitempty"`
}

// GraphQLLicense is the license of a repository.
type GraphQLLicense struct {
	ID                   *string              `json:"id,omitempty"`
	Key                  *string              `json:"key,omitempty"`
	Name                 *string              `json:"name,omitempty"`
	Nickname             *string              `json:"nickname,omitempty"`
	SPDXID               *string              `json:"spdx_id,omitempty"`
	Description          *string              `json:"description,omitempty"`
	Implementation       *string              `json:"implementation,omitempty"`
	Featured             *bool                `json:"featured,omitempty"`
	Hidden               *bool                `json:"hidden,omitempty"`
	PseudoLicense        *bool                `json:"pseudo_license,omitempty"`
	Body                 *string              `json:"body,omitempty"`
	BodyTruncated        bool                 `json:"body_truncated,omitempty"`
	URL                  *string              `json:"url,omitempty"`
	Conditions           []GraphQLLicenseRule `json:"conditions,omitempty"`
	ConditionsTruncated  bool                 `json:"conditions_truncated,omitempty"`
	Limitations          []GraphQLLicenseRule `json:"limitations,omitempty"`
	LimitationsTruncated bool                 `json:"limitations_truncated,omitempty"`
	Permissions          []GraphQLLicenseRule `json:"permissions,omitempty"`
	PermissionsTruncated bool                 `json:"permissions_truncated,omitempty"`
}

// GraphQLCodeOfConduct is the code of conduct of a repository.
type GraphQLCodeOfConduct struct {
	ID            *string `json:"id,omitempty"`
	Key           *string `json:"key,omitempty"`
	Name          *string `json:"name,omitempty"`
	Body          *string `json:"body,omitempty"`
	BodyTruncated bool    `json:"body_truncated,omitempty"`
	URL           *string `json:"url,omitempty"`
	Links         Links   `json:"links,omitempty"`
}

// GraphQLContributingGuidelines are the contributing guidelines of a repository.
type GraphQLContributingGuidelines struct {
	Body          *string `json:"body,omitempty"`
	BodyTruncated bool    `json:"body_truncated,omitempty"`
	URL           *string `json:"url,omitempty"`
	Links         Links   `json:"links,omitempty"`
}

// GraphQLRef is the default branch of a repository.
type GraphQLRef struct {
	ID     *string `json:"id,omitempty"`
	Name   *string `json:"name,omitempty"`
	Prefix *string `json:"prefix,omitempty"`
}

// GraphQLRelease is the latest release of a repository.
type GraphQLRelease struct {
	ID                   *string         `json:"id,omitempty"`
	DatabaseID           *int64          `json:"database_id,omitempty"`
	Name                 *string         `json:"name,omitempty"`
	TagName              *string         `json:"tag_name,omitempty"`
	Description          *string         `json:"description,omitempty"`
	DescriptionTruncated bool            `json:"description_truncated,omitempty"`
	CreatedAt            *string         `json:"created_at,omitempty"`
	PublishedAt          *string         `json:"published_at,omitempty"`
	UpdatedAt            *string         `json:"updated_at,omitempty"`
	Immutable            *bool           `json:"immutable,omitempty"`
	IsDraft              *bool           `json:"is_draft,omitempty"`
	IsLatest             *bool           `json:"is_latest,omitempty"`
	IsPrerelease         *bool           `json:"is_prerelease,omitempty"`
	ViewerCanReact       *bool           `json:"viewer_can_react,omitempty"`
	Author               *GraphQLAccount `json:"author,omitempty"`
	URL                  *string         `json:"url,omitempty"`
	Links                Links           `json:"links,omitempty"`
}

// GraphQLInteractionAbility is the interaction limit of a repository.
type GraphQLInteractionAbility struct {
	ExpiresAt *string `json:"expires_at,omitempty"`
	Limit     *string `json:"limit,omitempty"`
	Origin    *string `json:"origin,omitempty"`
}

// GraphQLPlanFeatures are the features the plan of a repository allows.
type GraphQLPlanFeatures struct {
	Codeowners                  *bool  `json:"codeowners,omitempty"`
	DraftPullRequests           *bool  `json:"draft_pull_requests,omitempty"`
	MaximumAssignees            *int64 `json:"maximum_assignees,omitempty"`
	MaximumManualReviewRequests *int64 `json:"maximum_manual_review_requests,omitempty"`
	TeamReviewRequests          *bool  `json:"team_review_requests,omitempty"`
}

// GraphQLPullRequestCreationCapConfig is the pull request creation cap of a repository; only the size of the
// list of users who bypass it is reported.
type GraphQLPullRequestCreationCapConfig struct {
	BypassedUsersTotalCount *int64 `json:"bypassed_users_total_count,omitempty"`
}

// GraphQLContactLink is a contact link of the issue chooser.
type GraphQLContactLink struct {
	About *string `json:"about,omitempty"`
	Name  *string `json:"name,omitempty"`
	URL   *string `json:"url,omitempty"`
}

// GraphQLFundingLink is a funding link of a repository.
type GraphQLFundingLink struct {
	Platform *string `json:"platform,omitempty"`
	URL      *string `json:"url,omitempty"`
}

// GraphQLIssueTemplate is an issue template of a repository.
type GraphQLIssueTemplate struct {
	About         *string           `json:"about,omitempty"`
	Body          *string           `json:"body,omitempty"`
	BodyTruncated bool              `json:"body_truncated,omitempty"`
	Filename      *string           `json:"filename,omitempty"`
	Name          *string           `json:"name,omitempty"`
	Title         *string           `json:"title,omitempty"`
	Type          *GraphQLIssueType `json:"type,omitempty"`
}

// GraphQLPullRequestTemplate is a pull request template of a repository.
type GraphQLPullRequestTemplate struct {
	Body          *string `json:"body,omitempty"`
	BodyTruncated bool    `json:"body_truncated,omitempty"`
	Filename      *string `json:"filename,omitempty"`
}

// GraphQLContentWarning is the content warning GitHub shows the viewer for a repository.
type GraphQLContentWarning struct {
	Category          *string `json:"category,omitempty"`
	CustomSubCategory *string `json:"custom_sub_category,omitempty"`
	SubCategory       *string `json:"sub_category,omitempty"`
	SubTitle          *string `json:"sub_title,omitempty"`
	Title             *string `json:"title,omitempty"`
	Type              *string `json:"type,omitempty"`
}

// GraphQLNestedRepository is the parent or the template of a repository. Outside the connection's targets it
// keeps only ID, DatabaseID, FullDatabaseID, and NameWithOwner.
type GraphQLNestedRepository struct {
	ID               *string         `json:"id,omitempty"`
	DatabaseID       *int64          `json:"database_id,omitempty"`
	FullDatabaseID   *json.Number    `json:"full_database_id,omitempty"`
	Name             *string         `json:"name,omitempty"`
	NameWithOwner    *string         `json:"name_with_owner,omitempty"`
	Description      *string         `json:"description,omitempty"`
	URL              *string         `json:"url,omitempty"`
	CreatedAt        *string         `json:"created_at,omitempty"`
	UpdatedAt        *string         `json:"updated_at,omitempty"`
	PushedAt         *string         `json:"pushed_at,omitempty"`
	ArchivedAt       *string         `json:"archived_at,omitempty"`
	Visibility       *string         `json:"visibility,omitempty"`
	IsPrivate        *bool           `json:"is_private,omitempty"`
	IsFork           *bool           `json:"is_fork,omitempty"`
	IsArchived       *bool           `json:"is_archived,omitempty"`
	IsTemplate       *bool           `json:"is_template,omitempty"`
	IsMirror         *bool           `json:"is_mirror,omitempty"`
	IsLocked         *bool           `json:"is_locked,omitempty"`
	IsDisabled       *bool           `json:"is_disabled,omitempty"`
	IsEmpty          *bool           `json:"is_empty,omitempty"`
	IsInOrganization *bool           `json:"is_in_organization,omitempty"`
	ForkCount        *int64          `json:"fork_count,omitempty"`
	StargazerCount   *int64          `json:"stargazer_count,omitempty"`
	DiskUsage        *int64          `json:"disk_usage,omitempty"`
	Owner            *GraphQLAccount `json:"owner,omitempty"`
	Links            Links           `json:"links,omitempty"`
}

// RepositoryWidth holds the members of GitHub's GraphQL Repository the repository list adds to its compact
// view. The flat members repository, visibility, and archived stay as they were.
type RepositoryWidth struct {
	ID                                  *string                              `json:"id,omitempty"`
	DatabaseID                          *int64                               `json:"database_id,omitempty"`
	FullDatabaseID                      *json.Number                         `json:"full_database_id,omitempty"`
	Name                                *string                              `json:"name,omitempty"`
	NameWithOwner                       *string                              `json:"name_with_owner,omitempty"`
	Description                         *string                              `json:"description,omitempty"`
	URL                                 *string                              `json:"url,omitempty"`
	CreatedAt                           *string                              `json:"created_at,omitempty"`
	UpdatedAt                           *string                              `json:"updated_at,omitempty"`
	PushedAt                            *string                              `json:"pushed_at,omitempty"`
	ArchivedAt                          *string                              `json:"archived_at,omitempty"`
	IsFork                              *bool                                `json:"is_fork,omitempty"`
	IsPrivate                           *bool                                `json:"is_private,omitempty"`
	IsTemplate                          *bool                                `json:"is_template,omitempty"`
	IsMirror                            *bool                                `json:"is_mirror,omitempty"`
	IsLocked                            *bool                                `json:"is_locked,omitempty"`
	LockReason                          *string                              `json:"lock_reason,omitempty"`
	IsDisabled                          *bool                                `json:"is_disabled,omitempty"`
	IsEmpty                             *bool                                `json:"is_empty,omitempty"`
	IsInOrganization                    *bool                                `json:"is_in_organization,omitempty"`
	IsBlankIssuesEnabled                *bool                                `json:"is_blank_issues_enabled,omitempty"`
	IsSecurityPolicyEnabled             *bool                                `json:"is_security_policy_enabled,omitempty"`
	IsUserConfigurationRepository       *bool                                `json:"is_user_configuration_repository,omitempty"`
	ForkCount                           *int64                               `json:"fork_count,omitempty"`
	StargazerCount                      *int64                               `json:"stargazer_count,omitempty"`
	DiskUsage                           *int64                               `json:"disk_usage,omitempty"`
	HasIssuesEnabled                    *bool                                `json:"has_issues_enabled,omitempty"`
	HasWikiEnabled                      *bool                                `json:"has_wiki_enabled,omitempty"`
	HasProjectsEnabled                  *bool                                `json:"has_projects_enabled,omitempty"`
	HasDiscussionsEnabled               *bool                                `json:"has_discussions_enabled,omitempty"`
	HasPullRequestsEnabled              *bool                                `json:"has_pull_requests_enabled,omitempty"`
	HasSponsorshipsEnabled              *bool                                `json:"has_sponsorships_enabled,omitempty"`
	HasVulnerabilityAlertsEnabled       *bool                                `json:"has_vulnerability_alerts_enabled,omitempty"`
	ForkingAllowed                      *bool                                `json:"forking_allowed,omitempty"`
	MergeCommitAllowed                  *bool                                `json:"merge_commit_allowed,omitempty"`
	SquashMergeAllowed                  *bool                                `json:"squash_merge_allowed,omitempty"`
	RebaseMergeAllowed                  *bool                                `json:"rebase_merge_allowed,omitempty"`
	AutoMergeAllowed                    *bool                                `json:"auto_merge_allowed,omitempty"`
	AllowUpdateBranch                   *bool                                `json:"allow_update_branch,omitempty"`
	DeleteBranchOnMerge                 *bool                                `json:"delete_branch_on_merge,omitempty"`
	MergeCommitTitle                    *string                              `json:"merge_commit_title,omitempty"`
	MergeCommitMessage                  *string                              `json:"merge_commit_message,omitempty"`
	SquashMergeCommitTitle              *string                              `json:"squash_merge_commit_title,omitempty"`
	SquashMergeCommitMessage            *string                              `json:"squash_merge_commit_message,omitempty"`
	WebCommitSignoffRequired            *bool                                `json:"web_commit_signoff_required,omitempty"`
	IssueCreationPolicy                 *string                              `json:"issue_creation_policy,omitempty"`
	PullRequestCreationPolicy           *string                              `json:"pull_request_creation_policy,omitempty"`
	UsesCustomOpenGraphImage            *bool                                `json:"uses_custom_open_graph_image,omitempty"`
	ViewerCanAdminister                 *bool                                `json:"viewer_can_administer,omitempty"`
	ViewerCanCreateIssues               *bool                                `json:"viewer_can_create_issues,omitempty"`
	ViewerCanSeeIssueFields             *bool                                `json:"viewer_can_see_issue_fields,omitempty"`
	ViewerCanSubscribe                  *bool                                `json:"viewer_can_subscribe,omitempty"`
	ViewerCanUpdateTopics               *bool                                `json:"viewer_can_update_topics,omitempty"`
	ViewerContentWarning                *GraphQLContentWarning               `json:"viewer_content_warning,omitempty"`
	ViewerDefaultCommitEmail            *string                              `json:"viewer_default_commit_email,omitempty"`
	ViewerPossibleCommitEmails          []string                             `json:"viewer_possible_commit_emails,omitempty"`
	ViewerPossibleCommitEmailsTruncated bool                                 `json:"viewer_possible_commit_emails_truncated,omitempty"`
	ViewerDefaultMergeMethod            *string                              `json:"viewer_default_merge_method,omitempty"`
	ViewerHasStarred                    *bool                                `json:"viewer_has_starred,omitempty"`
	ViewerPermission                    *string                              `json:"viewer_permission,omitempty"`
	ViewerSubscription                  *string                              `json:"viewer_subscription,omitempty"`
	Links                               Links                                `json:"links,omitempty"`
	Parent                              *GraphQLNestedRepository             `json:"parent,omitempty"`
	TemplateRepository                  *GraphQLNestedRepository             `json:"template_repository,omitempty"`
	PrimaryLanguage                     *GraphQLLanguage                     `json:"primary_language,omitempty"`
	LicenseInfo                         *GraphQLLicense                      `json:"license_info,omitempty"`
	CodeOfConduct                       *GraphQLCodeOfConduct                `json:"code_of_conduct,omitempty"`
	ContributingGuidelines              *GraphQLContributingGuidelines       `json:"contributing_guidelines,omitempty"`
	DefaultBranchRef                    *GraphQLRef                          `json:"default_branch_ref,omitempty"`
	LatestRelease                       *GraphQLRelease                      `json:"latest_release,omitempty"`
	InteractionAbility                  *GraphQLInteractionAbility           `json:"interaction_ability,omitempty"`
	PlanFeatures                        *GraphQLPlanFeatures                 `json:"plan_features,omitempty"`
	PullRequestCreationCapConfig        *GraphQLPullRequestCreationCapConfig `json:"pull_request_creation_cap_config,omitempty"`
	ContactLinks                        []GraphQLContactLink                 `json:"contact_links,omitempty"`
	ContactLinksTruncated               bool                                 `json:"contact_links_truncated,omitempty"`
	FundingLinks                        []GraphQLFundingLink                 `json:"funding_links,omitempty"`
	FundingLinksTruncated               bool                                 `json:"funding_links_truncated,omitempty"`
	IssueTemplates                      []GraphQLIssueTemplate               `json:"issue_templates,omitempty"`
	IssueTemplatesTruncated             bool                                 `json:"issue_templates_truncated,omitempty"`
	PullRequestTemplates                []GraphQLPullRequestTemplate         `json:"pull_request_templates,omitempty"`
	PullRequestTemplatesTruncated       bool                                 `json:"pull_request_templates_truncated,omitempty"`
	// Unreadable names the members GitHub refused this token inside this repository.
	Unreadable                   []string                   `json:"unreadable,omitempty"`
	RepositoryTopics             []GraphQLRepositoryTopic   `json:"repository_topics,omitempty"`
	RepositoryTopicsTotalCount   *int64                     `json:"repository_topics_total_count,omitempty"`
	RepositoryTopicsTruncated    bool                       `json:"repository_topics_truncated,omitempty"`
	PinnedIssues                 []GraphQLPinnedIssue       `json:"pinned_issues,omitempty"`
	PinnedIssuesTotalCount       *int64                     `json:"pinned_issues_total_count,omitempty"`
	PinnedIssuesTruncated        bool                       `json:"pinned_issues_truncated,omitempty"`
	PinnedDiscussions            []GraphQLPinnedDiscussion  `json:"pinned_discussions,omitempty"`
	PinnedDiscussionsTotalCount  *int64                     `json:"pinned_discussions_total_count,omitempty"`
	PinnedDiscussionsTruncated   bool                       `json:"pinned_discussions_truncated,omitempty"`
	PinnedEnvironments           []GraphQLPinnedEnvironment `json:"pinned_environments,omitempty"`
	PinnedEnvironmentsTotalCount *int64                     `json:"pinned_environments_total_count,omitempty"`
	PinnedEnvironmentsTruncated  bool                       `json:"pinned_environments_truncated,omitempty"`
	RecentProjects               []GraphQLProject           `json:"recent_projects,omitempty"`
	RecentProjectsTotalCount     *int64                     `json:"recent_projects_total_count,omitempty"`
	RecentProjectsTruncated      bool                       `json:"recent_projects_truncated,omitempty"`
	Submodules                   []GraphQLSubmodule         `json:"submodules,omitempty"`
	SubmodulesTotalCount         *int64                     `json:"submodules_total_count,omitempty"`
	SubmodulesTruncated          bool                       `json:"submodules_truncated,omitempty"`
	RepositoryTotals
}

// ---- Mapping

func (r *gqlTopicRaw) project() *GraphQLTopic {
	if r == nil {
		return nil
	}
	return &GraphQLTopic{ID: gqlName(r.ID), Name: gqlName(r.Name), StargazerCount: r.StargazerCount,
		ViewerHasStarred: r.ViewerHasStarred}
}

func (r *gqlRepositoryTopicRaw) project() *GraphQLRepositoryTopic {
	if r == nil {
		return nil
	}
	return &GraphQLRepositoryTopic{ID: gqlName(r.ID), Topic: r.Topic.project(), URL: address(r.URL),
		Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath)}
}

func (r *gqlPinnedIssueRaw) project() *GraphQLPinnedIssue {
	if r == nil {
		return nil
	}
	return &GraphQLPinnedIssue{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, FullDatabaseID: r.FullDatabaseID,
		Issue: r.Issue.project(), PinnedBy: r.PinnedBy.project()}
}

func (r *gqlPinnedDiscussionRaw) project() *GraphQLPinnedDiscussion {
	if r == nil {
		return nil
	}
	d := &GraphQLPinnedDiscussion{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, CreatedAt: gqlName(r.CreatedAt),
		UpdatedAt: gqlName(r.UpdatedAt), Pattern: gqlName(r.Pattern),
		PreconfiguredGradient: gqlName(r.PreconfiguredGradient), PinnedBy: r.PinnedBy.project()}
	for _, color := range r.GradientStopColors {
		if color = gqlName(color); color != nil {
			d.GradientStopColors = append(d.GradientStopColors, *color)
		}
	}
	d.GradientStopColors, _ = capList(d.GradientStopColors, func(s string) *string { return &s })
	if x := r.Discussion; x != nil {
		d.Discussion = &GraphQLDiscussionRef{ID: gqlName(x.ID), Number: x.Number, Title: gqlName(x.Title),
			URL: address(x.URL)}
	}
	return d
}

func (r *gqlPinnedEnvironmentRaw) project() *GraphQLPinnedEnvironment {
	if r == nil {
		return nil
	}
	e := &GraphQLPinnedEnvironment{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, CreatedAt: gqlName(r.CreatedAt),
		Position: r.Position}
	if x := r.Environment; x != nil {
		e.Environment = &GraphQLEnvironment{ID: gqlName(x.ID), DatabaseID: x.DatabaseID, Name: gqlName(x.Name),
			IsPinned: x.IsPinned, PinnedPosition: x.PinnedPosition}
	}
	return e
}

func (r *gqlProjectRaw) project() *GraphQLProject {
	if r == nil {
		return nil
	}
	readme, cut := gqlBody(r.Readme)
	return &GraphQLProject{ID: gqlName(r.ID), FullDatabaseID: r.FullDatabaseID, Number: r.Number,
		Title: gqlName(r.Title), ShortDescription: gqlText(r.ShortDescription), Readme: readme,
		ReadmeTruncated: cut, Closed: r.Closed, ClosedAt: gqlName(r.ClosedAt), CreatedAt: gqlName(r.CreatedAt),
		UpdatedAt: gqlName(r.UpdatedAt), Public: r.Public, Template: r.Template, ViewerCanClose: r.ViewerCanClose,
		ViewerCanReopen: r.ViewerCanReopen, ViewerCanUpdate: r.ViewerCanUpdate, Creator: r.Creator.project(),
		URL: address(r.URL), Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath)}
}

func (r *gqlSubmoduleRaw) project() *GraphQLSubmodule {
	if r == nil {
		return nil
	}
	return &GraphQLSubmodule{Branch: gqlName(r.Branch), Name: gqlName(r.Name), NameRaw: gqlName(r.NameRaw),
		Path: gqlName(r.Path), PathRaw: gqlName(r.PathRaw), SubprojectCommitOid: gqlName(r.SubprojectCommitOid),
		Links: gqlLinks(gqlLinkKeys("git_url"), r.GitURL)}
}

func (r *gqlLicenseRaw) project() *GraphQLLicense {
	if r == nil {
		return nil
	}
	body, cut := gqlBody(r.Body)
	l := &GraphQLLicense{ID: gqlName(r.ID), Key: gqlName(r.Key), Name: gqlName(r.Name),
		Nickname: gqlName(r.Nickname), SPDXID: gqlName(r.SPDXID), Description: gqlText(r.Description),
		Implementation: gqlText(r.Implementation), Featured: r.Featured, Hidden: r.Hidden,
		PseudoLicense: r.PseudoLicense, Body: body, BodyTruncated: cut, URL: address(r.URL)}
	l.Conditions, l.ConditionsTruncated = capList(r.Conditions, projectLicenseRule)
	l.Limitations, l.LimitationsTruncated = capList(r.Limitations, projectLicenseRule)
	l.Permissions, l.PermissionsTruncated = capList(r.Permissions, projectLicenseRule)
	return l
}

func projectLicenseRule(r *gqlLicenseRuleRaw) *GraphQLLicenseRule {
	if r == nil {
		return nil
	}
	return &GraphQLLicenseRule{Key: gqlName(r.Key), Label: gqlName(r.Label), Description: gqlText(r.Description)}
}

func (r *gqlCodeOfConductRaw) project() *GraphQLCodeOfConduct {
	if r == nil {
		return nil
	}
	body, cut := gqlBody(r.Body)
	return &GraphQLCodeOfConduct{ID: gqlName(r.ID), Key: gqlName(r.Key), Name: gqlName(r.Name), Body: body,
		BodyTruncated: cut, URL: address(r.URL), Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath)}
}

func (r *gqlGuidelinesRaw) project() *GraphQLContributingGuidelines {
	if r == nil {
		return nil
	}
	body, cut := gqlBody(r.Body)
	return &GraphQLContributingGuidelines{Body: body, BodyTruncated: cut, URL: address(r.URL),
		Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath)}
}

func (r *gqlReleaseRaw) project() *GraphQLRelease {
	if r == nil {
		return nil
	}
	description, cut := gqlBody(r.Description)
	return &GraphQLRelease{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, Name: gqlName(r.Name),
		TagName: gqlName(r.TagName), Description: description, DescriptionTruncated: cut,
		CreatedAt: gqlName(r.CreatedAt), PublishedAt: gqlName(r.PublishedAt), UpdatedAt: gqlName(r.UpdatedAt),
		Immutable: r.Immutable, IsDraft: r.IsDraft, IsLatest: r.IsLatest, IsPrerelease: r.IsPrerelease,
		ViewerCanReact: r.ViewerCanReact, Author: r.Author.project(), URL: address(r.URL),
		Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath)}
}

func (r *gqlNestedRepositoryRaw) project(allowed allowlist) *GraphQLNestedRepository {
	if r == nil {
		return nil
	}
	ids := &GraphQLNestedRepository{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, FullDatabaseID: r.FullDatabaseID,
		NameWithOwner: gqlName(r.NameWithOwner)}
	if !r.admitted(allowed) {
		return ids
	}
	ids.Name, ids.Description, ids.URL = gqlName(r.Name), gqlText(r.Description), address(r.URL)
	ids.CreatedAt, ids.UpdatedAt, ids.PushedAt = gqlName(r.CreatedAt), gqlName(r.UpdatedAt), gqlName(r.PushedAt)
	ids.ArchivedAt, ids.Visibility = gqlName(r.ArchivedAt), gqlName(r.Visibility)
	ids.IsPrivate, ids.IsFork, ids.IsArchived, ids.IsTemplate = r.IsPrivate, r.IsFork, r.IsArchived, r.IsTemplate
	ids.IsMirror, ids.IsLocked, ids.IsDisabled, ids.IsEmpty = r.IsMirror, r.IsLocked, r.IsDisabled, r.IsEmpty
	ids.IsInOrganization, ids.ForkCount, ids.StargazerCount = r.IsInOrganization, r.ForkCount, r.StargazerCount
	ids.DiskUsage, ids.Owner = r.DiskUsage, r.Owner.project()
	ids.Links = gqlLinks(gqlLinkKeys("homepage_url", "resource_path"), r.HomepageURL, r.ResourcePath)
	return ids
}

// admitted reports whether the targets let the repository be shown beyond its identifiers; a name that cannot
// be checked counts as outside.
func (r *gqlNestedRepositoryRaw) admitted(allowed allowlist) bool {
	if r.NameWithOwner == nil {
		return false
	}
	owner, name, ok := strings.Cut(*r.NameWithOwner, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return false
	}
	return allowed.admitsRepository(target{kind: kindRepository, owner: owner, repo: name})
}

// width maps the node. The members the compact view answered before are not repeated.
func (r *repositoryWidthRaw) width(allowed allowlist) RepositoryWidth {
	w := RepositoryWidth{ID: gqlName(r.ID), DatabaseID: r.DatabaseID, FullDatabaseID: r.FullDatabaseID,
		Name: gqlName(r.Name), NameWithOwner: gqlName(r.NameWithOwner), Description: gqlText(r.Description),
		URL: address(r.URL), CreatedAt: gqlName(r.CreatedAt), UpdatedAt: gqlName(r.UpdatedAt),
		PushedAt: gqlName(r.PushedAt), ArchivedAt: gqlName(r.ArchivedAt), IsFork: r.IsFork,
		IsPrivate: r.IsPrivate, IsTemplate: r.IsTemplate, IsMirror: r.IsMirror, IsLocked: r.IsLocked,
		LockReason: gqlName(r.LockReason), IsDisabled: r.IsDisabled, IsEmpty: r.IsEmpty,
		IsInOrganization: r.IsInOrganization, IsBlankIssuesEnabled: r.IsBlankIssuesEnabled,
		IsSecurityPolicyEnabled: r.IsSecurityPolicyEnabled, IsUserConfigurationRepository: r.IsUserConfigurationRepository,
		ForkCount: r.ForkCount, StargazerCount: r.StargazerCount, DiskUsage: r.DiskUsage,
		HasIssuesEnabled: r.HasIssuesEnabled, HasWikiEnabled: r.HasWikiEnabled,
		HasProjectsEnabled: r.HasProjectsEnabled, HasDiscussionsEnabled: r.HasDiscussionsEnabled,
		HasPullRequestsEnabled: r.HasPullRequestsEnabled, HasSponsorshipsEnabled: r.HasSponsorshipsEnabled,
		HasVulnerabilityAlertsEnabled: r.HasVulnerabilityAlertsEnabled, ForkingAllowed: r.ForkingAllowed,
		MergeCommitAllowed: r.MergeCommitAllowed, SquashMergeAllowed: r.SquashMergeAllowed,
		RebaseMergeAllowed: r.RebaseMergeAllowed, AutoMergeAllowed: r.AutoMergeAllowed,
		AllowUpdateBranch: r.AllowUpdateBranch, DeleteBranchOnMerge: r.DeleteBranchOnMerge,
		MergeCommitTitle: gqlName(r.MergeCommitTitle), MergeCommitMessage: gqlName(r.MergeCommitMessage),
		SquashMergeCommitTitle: gqlName(r.SquashMergeCommitTitle), SquashMergeCommitMessage: gqlName(r.SquashMergeCommitMessage),
		WebCommitSignoffRequired: r.WebCommitSignoffRequired, IssueCreationPolicy: gqlName(r.IssueCreationPolicy),
		PullRequestCreationPolicy: gqlName(r.PullRequestCreationPolicy),
		UsesCustomOpenGraphImage:  r.UsesCustomOpenGraphImage, ViewerCanAdminister: r.ViewerCanAdminister,
		ViewerCanCreateIssues: r.ViewerCanCreateIssues, ViewerCanSeeIssueFields: r.ViewerCanSeeIssueFields,
		ViewerCanSubscribe: r.ViewerCanSubscribe, ViewerCanUpdateTopics: r.ViewerCanUpdateTopics,
		ViewerDefaultCommitEmail: gqlName(r.ViewerDefaultCommitEmail),
		ViewerDefaultMergeMethod: gqlName(r.ViewerDefaultMergeMethod), ViewerHasStarred: r.ViewerHasStarred,
		ViewerPermission: gqlName(r.ViewerPermission), ViewerSubscription: gqlName(r.ViewerSubscription),
		Links: gqlLinks(repositoryLinkKeys, r.HomepageURL, r.MirrorURL, r.OpenGraphImageURL, r.ProjectsURL,
			r.SecurityPolicyURL, r.SSHURL, r.ResourcePath, r.ProjectsResourcePath),
		Parent:             r.Parent.project(allowed),
		TemplateRepository: r.TemplateRepository.project(allowed),
		LicenseInfo:        r.LicenseInfo.project(), CodeOfConduct: r.CodeOfConduct.project(),
		ContributingGuidelines: r.ContributingGuidelines.project(), LatestRelease: r.LatestRelease.project(),
		RepositoryTotals: r.repositoryTotalsRaw.totals()}
	if x := r.ViewerContentWarning; x != nil {
		w.ViewerContentWarning = &GraphQLContentWarning{Category: gqlName(x.Category),
			CustomSubCategory: gqlName(x.CustomSubCategory), SubCategory: gqlName(x.SubCategory),
			SubTitle: gqlName(x.SubTitle), Title: gqlName(x.Title), Type: gqlName(x.Type)}
	}
	w.ViewerPossibleCommitEmails, w.ViewerPossibleCommitEmailsTruncated = capList(r.ViewerPossibleCommitEmails,
		func(s *string) *string { return gqlName(s) })
	if x := r.PrimaryLanguage; x != nil {
		w.PrimaryLanguage = &GraphQLLanguage{ID: gqlName(x.ID), Name: gqlName(x.Name), Color: gqlName(x.Color)}
	}
	if x := r.DefaultBranchRef; x != nil {
		w.DefaultBranchRef = &GraphQLRef{ID: gqlName(x.ID), Name: gqlName(x.Name), Prefix: gqlName(x.Prefix)}
	}
	if x := r.InteractionAbility; x != nil {
		w.InteractionAbility = &GraphQLInteractionAbility{ExpiresAt: gqlName(x.ExpiresAt), Limit: gqlName(x.Limit),
			Origin: gqlName(x.Origin)}
	}
	if x := r.PlanFeatures; x != nil {
		w.PlanFeatures = &GraphQLPlanFeatures{Codeowners: x.Codeowners, DraftPullRequests: x.DraftPullRequests,
			MaximumAssignees: x.MaximumAssignees, MaximumManualReviewRequests: x.MaximumManualReviewRequests,
			TeamReviewRequests: x.TeamReviewRequests}
	}
	if x := r.PullRequestCreationCapConfig; x != nil {
		w.PullRequestCreationCapConfig = &GraphQLPullRequestCreationCapConfig{BypassedUsersTotalCount: x.BypassedUsers.count()}
	}
	w.ContactLinks, w.ContactLinksTruncated = capList(r.ContactLinks, func(x *gqlContactLinkRaw) *GraphQLContactLink {
		if x == nil {
			return nil
		}
		return &GraphQLContactLink{About: gqlText(x.About), Name: gqlName(x.Name), URL: address(x.URL)}
	})
	w.FundingLinks, w.FundingLinksTruncated = capList(r.FundingLinks, func(x *gqlFundingLinkRaw) *GraphQLFundingLink {
		if x == nil {
			return nil
		}
		return &GraphQLFundingLink{Platform: gqlName(x.Platform), URL: address(x.URL)}
	})
	w.IssueTemplates, w.IssueTemplatesTruncated = capList(r.IssueTemplates, func(x *gqlIssueTemplateRaw) *GraphQLIssueTemplate {
		if x == nil {
			return nil
		}
		body, cut := gqlBody(x.Body)
		return &GraphQLIssueTemplate{About: gqlText(x.About), Body: body, BodyTruncated: cut,
			Filename: gqlName(x.Filename), Name: gqlName(x.Name), Title: gqlText(x.Title), Type: x.Type.project()}
	})
	w.PullRequestTemplates, w.PullRequestTemplatesTruncated = capList(r.PullRequestTemplates,
		func(x *gqlPullRequestTemplateRaw) *GraphQLPullRequestTemplate {
			if x == nil {
				return nil
			}
			body, cut := gqlBody(x.Body)
			return &GraphQLPullRequestTemplate{Body: body, BodyTruncated: cut, Filename: gqlName(x.Filename)}
		})
	w.RepositoryTopics, w.RepositoryTopicsTotalCount, w.RepositoryTopicsTruncated = pageItems(r.RepositoryTopics,
		(*gqlRepositoryTopicRaw).project)
	w.PinnedIssues, w.PinnedIssuesTotalCount, w.PinnedIssuesTruncated = pageItems(r.PinnedIssues,
		(*gqlPinnedIssueRaw).project)
	w.PinnedDiscussions, w.PinnedDiscussionsTotalCount, w.PinnedDiscussionsTruncated = pageItems(r.PinnedDiscussions,
		(*gqlPinnedDiscussionRaw).project)
	w.PinnedEnvironments, w.PinnedEnvironmentsTotalCount, w.PinnedEnvironmentsTruncated = pageItems(r.PinnedEnvironments,
		(*gqlPinnedEnvironmentRaw).project)
	w.RecentProjects, w.RecentProjectsTotalCount, w.RecentProjectsTruncated = pageItems(r.RecentProjects,
		(*gqlProjectRaw).project)
	w.Submodules, w.SubmodulesTotalCount, w.SubmodulesTruncated = pageItems(r.Submodules,
		(*gqlSubmoduleRaw).project)
	return w
}

var repositoryLinkKeys = gqlLinkKeys("homepage_url", "mirror_url", "open_graph_image_url", "projects_url",
	"security_policy_url", "ssh_url", "resource_path", "projects_resource_path")
