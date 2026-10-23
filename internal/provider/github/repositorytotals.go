package github

import "github.com/castrowithcee/qatlas-cli/internal/capability"

// The connections of a repository the list reports only as a count. A connection asked for totalCount alone
// carries no first or last argument and so adds no nodes to GitHub's node count. refs needs a prefix and
// suggestedActors a capability to be asked at all; refs counts every branch and tag, suggestedActors the
// actors that can be assigned.

const repositoryTotalsSelection = `issues{totalCount} pullRequests{totalCount} discussions{totalCount} releases{totalCount} labels{totalCount} milestones{totalCount} refs(refPrefix:"refs/"){totalCount} stargazers{totalCount} watchers{totalCount} forks{totalCount} languages{totalCount} commitComments{totalCount} discussionCategories{totalCount} issueTypes{totalCount} issueFields{totalCount} repositoryCustomPropertyValues{totalCount}`

// repositoryAdminTotalsSelection are the counts that need rights beyond reading the repository.
const repositoryAdminTotalsSelection = `collaborators{totalCount} assignableUsers{totalCount} mentionableUsers{totalCount} environments{totalCount} deployments{totalCount} deployKeys{totalCount} branchProtectionRules{totalCount} rulesets{totalCount} packages{totalCount} projectsV2{totalCount} vulnerabilityAlerts{totalCount} dependencyGraphManifests{totalCount} suggestedActors(capabilities:[CAN_BE_ASSIGNED]){totalCount}`

type repositoryTotalsRaw struct {
	Issues                         *gqlTotal `json:"issues"`
	PullRequests                   *gqlTotal `json:"pullRequests"`
	Discussions                    *gqlTotal `json:"discussions"`
	Releases                       *gqlTotal `json:"releases"`
	Labels                         *gqlTotal `json:"labels"`
	Milestones                     *gqlTotal `json:"milestones"`
	Refs                           *gqlTotal `json:"refs"`
	Collaborators                  *gqlTotal `json:"collaborators"`
	AssignableUsers                *gqlTotal `json:"assignableUsers"`
	MentionableUsers               *gqlTotal `json:"mentionableUsers"`
	Stargazers                     *gqlTotal `json:"stargazers"`
	Watchers                       *gqlTotal `json:"watchers"`
	Forks                          *gqlTotal `json:"forks"`
	Languages                      *gqlTotal `json:"languages"`
	Environments                   *gqlTotal `json:"environments"`
	Deployments                    *gqlTotal `json:"deployments"`
	DeployKeys                     *gqlTotal `json:"deployKeys"`
	BranchProtectionRules          *gqlTotal `json:"branchProtectionRules"`
	Rulesets                       *gqlTotal `json:"rulesets"`
	Packages                       *gqlTotal `json:"packages"`
	ProjectsV2                     *gqlTotal `json:"projectsV2"`
	CommitComments                 *gqlTotal `json:"commitComments"`
	VulnerabilityAlerts            *gqlTotal `json:"vulnerabilityAlerts"`
	DependencyGraphManifests       *gqlTotal `json:"dependencyGraphManifests"`
	DiscussionCategories           *gqlTotal `json:"discussionCategories"`
	IssueTypes                     *gqlTotal `json:"issueTypes"`
	IssueFields                    *gqlTotal `json:"issueFields"`
	RepositoryCustomPropertyValues *gqlTotal `json:"repositoryCustomPropertyValues"`
	SuggestedActors                *gqlTotal `json:"suggestedActors"`
}

// RepositoryTotals are the sizes of the connections of a repository that the list does not read.
type RepositoryTotals struct {
	IssuesTotalCount                         *int64 `json:"issues_total_count,omitempty"`
	PullRequestsTotalCount                   *int64 `json:"pull_requests_total_count,omitempty"`
	DiscussionsTotalCount                    *int64 `json:"discussions_total_count,omitempty"`
	ReleasesTotalCount                       *int64 `json:"releases_total_count,omitempty"`
	LabelsTotalCount                         *int64 `json:"labels_total_count,omitempty"`
	MilestonesTotalCount                     *int64 `json:"milestones_total_count,omitempty"`
	RefsTotalCount                           *int64 `json:"refs_total_count,omitempty"`
	CollaboratorsTotalCount                  *int64 `json:"collaborators_total_count,omitempty"`
	AssignableUsersTotalCount                *int64 `json:"assignable_users_total_count,omitempty"`
	MentionableUsersTotalCount               *int64 `json:"mentionable_users_total_count,omitempty"`
	StargazersTotalCount                     *int64 `json:"stargazers_total_count,omitempty"`
	WatchersTotalCount                       *int64 `json:"watchers_total_count,omitempty"`
	ForksTotalCount                          *int64 `json:"forks_total_count,omitempty"`
	LanguagesTotalCount                      *int64 `json:"languages_total_count,omitempty"`
	EnvironmentsTotalCount                   *int64 `json:"environments_total_count,omitempty"`
	DeploymentsTotalCount                    *int64 `json:"deployments_total_count,omitempty"`
	DeployKeysTotalCount                     *int64 `json:"deploy_keys_total_count,omitempty"`
	BranchProtectionRulesTotalCount          *int64 `json:"branch_protection_rules_total_count,omitempty"`
	RulesetsTotalCount                       *int64 `json:"rulesets_total_count,omitempty"`
	PackagesTotalCount                       *int64 `json:"packages_total_count,omitempty"`
	ProjectsV2TotalCount                     *int64 `json:"projects_v2_total_count,omitempty"`
	CommitCommentsTotalCount                 *int64 `json:"commit_comments_total_count,omitempty"`
	VulnerabilityAlertsTotalCount            *int64 `json:"vulnerability_alerts_total_count,omitempty"`
	DependencyGraphManifestsTotalCount       *int64 `json:"dependency_graph_manifests_total_count,omitempty"`
	DiscussionCategoriesTotalCount           *int64 `json:"discussion_categories_total_count,omitempty"`
	IssueTypesTotalCount                     *int64 `json:"issue_types_total_count,omitempty"`
	IssueFieldsTotalCount                    *int64 `json:"issue_fields_total_count,omitempty"`
	RepositoryCustomPropertyValuesTotalCount *int64 `json:"repository_custom_property_values_total_count,omitempty"`
	SuggestedActorsTotalCount                *int64 `json:"suggested_actors_total_count,omitempty"`
}

func (r *repositoryTotalsRaw) totals() RepositoryTotals {
	return RepositoryTotals{
		IssuesTotalCount:                         r.Issues.count(),
		PullRequestsTotalCount:                   r.PullRequests.count(),
		DiscussionsTotalCount:                    r.Discussions.count(),
		ReleasesTotalCount:                       r.Releases.count(),
		LabelsTotalCount:                         r.Labels.count(),
		MilestonesTotalCount:                     r.Milestones.count(),
		RefsTotalCount:                           r.Refs.count(),
		CollaboratorsTotalCount:                  r.Collaborators.count(),
		AssignableUsersTotalCount:                r.AssignableUsers.count(),
		MentionableUsersTotalCount:               r.MentionableUsers.count(),
		StargazersTotalCount:                     r.Stargazers.count(),
		WatchersTotalCount:                       r.Watchers.count(),
		ForksTotalCount:                          r.Forks.count(),
		LanguagesTotalCount:                      r.Languages.count(),
		EnvironmentsTotalCount:                   r.Environments.count(),
		DeploymentsTotalCount:                    r.Deployments.count(),
		DeployKeysTotalCount:                     r.DeployKeys.count(),
		BranchProtectionRulesTotalCount:          r.BranchProtectionRules.count(),
		RulesetsTotalCount:                       r.Rulesets.count(),
		PackagesTotalCount:                       r.Packages.count(),
		ProjectsV2TotalCount:                     r.ProjectsV2.count(),
		CommitCommentsTotalCount:                 r.CommitComments.count(),
		VulnerabilityAlertsTotalCount:            r.VulnerabilityAlerts.count(),
		DependencyGraphManifestsTotalCount:       r.DependencyGraphManifests.count(),
		DiscussionCategoriesTotalCount:           r.DiscussionCategories.count(),
		IssueTypesTotalCount:                     r.IssueTypes.count(),
		IssueFieldsTotalCount:                    r.IssueFields.count(),
		RepositoryCustomPropertyValuesTotalCount: r.RepositoryCustomPropertyValues.count(),
		SuggestedActorsTotalCount:                r.SuggestedActors.count(),
	}
}

const repositoryTotalsProperties = `"issues_total_count":{"type":"integer"},"pull_requests_total_count":{"type":"integer"},"discussions_total_count":{"type":"integer"},"releases_total_count":{"type":"integer"},"labels_total_count":{"type":"integer"},"milestones_total_count":{"type":"integer"},"refs_total_count":{"type":"integer"},"collaborators_total_count":{"type":"integer"},"assignable_users_total_count":{"type":"integer"},"mentionable_users_total_count":{"type":"integer"},"stargazers_total_count":{"type":"integer"},"watchers_total_count":{"type":"integer"},"forks_total_count":{"type":"integer"},"languages_total_count":{"type":"integer"},"environments_total_count":{"type":"integer"},"deployments_total_count":{"type":"integer"},"deploy_keys_total_count":{"type":"integer"},"branch_protection_rules_total_count":{"type":"integer"},"rulesets_total_count":{"type":"integer"},"packages_total_count":{"type":"integer"},"projects_v2_total_count":{"type":"integer"},"commit_comments_total_count":{"type":"integer"},"vulnerability_alerts_total_count":{"type":"integer"},"dependency_graph_manifests_total_count":{"type":"integer"},"discussion_categories_total_count":{"type":"integer"},"issue_types_total_count":{"type":"integer"},"issue_fields_total_count":{"type":"integer"},"repository_custom_property_values_total_count":{"type":"integer"},"suggested_actors_total_count":{"type":"integer"}`

var repositoryTotalsFields = []capability.Field{
	{Name: "issues_total_count", Description: "Number of issues"},
	{Name: "pull_requests_total_count", Description: "Number of pull requests"},
	{Name: "discussions_total_count", Description: "Number of discussions"},
	{Name: "releases_total_count", Description: "Number of releases"},
	{Name: "labels_total_count", Description: "Number of labels"},
	{Name: "milestones_total_count", Description: "Number of milestones"},
	{Name: "refs_total_count", Description: "Number of branches and tags"},
	{Name: "collaborators_total_count", Description: "Number of collaborators; only with admin_counts"},
	{Name: "assignable_users_total_count", Description: "Number of assignable users; only with admin_counts"},
	{Name: "mentionable_users_total_count", Description: "Number of mentionable users; only with admin_counts"},
	{Name: "stargazers_total_count", Description: "Number of stargazers"},
	{Name: "watchers_total_count", Description: "Number of watchers"},
	{Name: "forks_total_count", Description: "Number of forks"},
	{Name: "languages_total_count", Description: "Number of languages"},
	{Name: "environments_total_count", Description: "Number of environments; only with admin_counts"},
	{Name: "deployments_total_count", Description: "Number of deployments; only with admin_counts"},
	{Name: "deploy_keys_total_count", Description: "Number of deploy keys; the keys themselves are never reported; only with admin_counts"},
	{Name: "branch_protection_rules_total_count", Description: "Number of branch protection rules; only with admin_counts"},
	{Name: "rulesets_total_count", Description: "Number of rulesets; only with admin_counts"},
	{Name: "packages_total_count", Description: "Number of packages; only with admin_counts"},
	{Name: "projects_v2_total_count", Description: "Number of projects the repository is linked to; only with admin_counts"},
	{Name: "commit_comments_total_count", Description: "Number of commit comments"},
	{Name: "vulnerability_alerts_total_count", Description: "Number of vulnerability alerts; only with admin_counts"},
	{Name: "dependency_graph_manifests_total_count", Description: "Number of dependency graph manifests; only with admin_counts"},
	{Name: "discussion_categories_total_count", Description: "Number of discussion categories"},
	{Name: "issue_types_total_count", Description: "Number of issue types"},
	{Name: "issue_fields_total_count", Description: "Number of issue fields"},
	{Name: "repository_custom_property_values_total_count", Description: "Number of custom property values"},
	{Name: "suggested_actors_total_count", Description: "Number of actors that can be assigned to issues and pull requests; only with admin_counts"},
}

// repositoryTotalMembers are the GraphQL members the list reads as a count.
var repositoryTotalMembers = []string{"issues", "pullRequests", "discussions", "releases", "labels", "milestones", "refs", "collaborators", "assignableUsers", "mentionableUsers", "stargazers", "watchers", "forks", "languages", "environments", "deployments", "deployKeys", "branchProtectionRules", "rulesets", "packages", "projectsV2", "commitComments", "vulnerabilityAlerts", "dependencyGraphManifests", "discussionCategories", "issueTypes", "issueFields", "repositoryCustomPropertyValues", "suggestedActors"}
