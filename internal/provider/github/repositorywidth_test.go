package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// repositoryTotalNames pairs each count-only connection with its output member.
var repositoryTotalNames = [][2]string{{"issues", "issues"}, {"pullRequests", "pull_requests"},
	{"discussions", "discussions"}, {"releases", "releases"}, {"labels", "labels"}, {"milestones", "milestones"},
	{"refs", "refs"}, {"collaborators", "collaborators"}, {"assignableUsers", "assignable_users"},
	{"mentionableUsers", "mentionable_users"}, {"stargazers", "stargazers"}, {"watchers", "watchers"},
	{"forks", "forks"}, {"languages", "languages"}, {"environments", "environments"},
	{"deployments", "deployments"}, {"deployKeys", "deploy_keys"}, {"branchProtectionRules", "branch_protection_rules"},
	{"rulesets", "rulesets"}, {"packages", "packages"}, {"projectsV2", "projects_v2"},
	{"commitComments", "commit_comments"}, {"vulnerabilityAlerts", "vulnerability_alerts"},
	{"dependencyGraphManifests", "dependency_graph_manifests"}, {"discussionCategories", "discussion_categories"},
	{"issueTypes", "issue_types"}, {"issueFields", "issue_fields"},
	{"repositoryCustomPropertyValues", "repository_custom_property_values"},
	{"suggestedActors", "suggested_actors"}}

const listAccount = `{"__typename":"User","login":"octocat","id":"U_1","databaseId":1,"name":"The Octocat",` +
	`"email":"octocat@example.invalid","avatarUrl":"https://avatars.example.invalid/u/1","url":"https://github.com/octocat",` +
	`"resourcePath":"/octocat","isSiteAdmin":false}`

const listAccountOut = `{"login":"octocat","type":"User","id":"U_1","database_id":1,"name":"The Octocat",` +
	`"email":"octocat@example.invalid","avatar_url":"https://avatars.example.invalid/u/1","url":"https://github.com/octocat",` +
	`"links":{"resource_path":"/octocat"},"is_site_admin":false}`

const listOwner = `{"__typename":"Organization","login":"octo-org","id":"O_1","databaseId":9,"name":"Octo Org",` +
	`"email":"org@example.invalid","avatarUrl":"https://avatars.example.invalid/o/9","url":"https://github.com/octo-org",` +
	`"resourcePath":"/octo-org"}`

const listOwnerOut = `{"login":"octo-org","type":"Organization","id":"O_1","database_id":9,"name":"Octo Org",` +
	`"email":"org@example.invalid","avatar_url":"https://avatars.example.invalid/o/9","url":"https://github.com/octo-org",` +
	`"links":{"resource_path":"/octo-org"}}`

func listNestedNode(nameWithOwner string) string {
	return fmt.Sprintf(`{"id":"R_n","databaseId":7,"fullDatabaseId":"900000000000000001","name":%q,"nameWithOwner":%q,`+
		`"description":"nested","url":"https://github.com/%s","createdAt":"2019-01-01T00:00:00Z",`+
		`"updatedAt":"2019-02-01T00:00:00Z","pushedAt":"2019-03-01T00:00:00Z","archivedAt":null,"visibility":"PUBLIC",`+
		`"isPrivate":false,"isFork":false,"isArchived":false,"isTemplate":true,"isMirror":false,"isLocked":false,`+
		`"isDisabled":false,"isEmpty":false,"isInOrganization":true,"forkCount":1,"stargazerCount":2,"diskUsage":3,`+
		`"homepageUrl":"https://home.example.invalid","resourcePath":"/%s","owner":%s}`,
		nameWithOwner[strings.Index(nameWithOwner, "/")+1:], nameWithOwner, nameWithOwner, nameWithOwner, listOwner)
}

func listNestedOut(nameWithOwner string) string {
	return fmt.Sprintf(`{"id":"R_n","database_id":7,"full_database_id":900000000000000001,"name":%q,`+
		`"name_with_owner":%q,"description":"nested","url":"https://github.com/%s","created_at":"2019-01-01T00:00:00Z",`+
		`"updated_at":"2019-02-01T00:00:00Z","pushed_at":"2019-03-01T00:00:00Z","visibility":"PUBLIC",`+
		`"is_private":false,"is_fork":false,"is_archived":false,"is_template":true,"is_mirror":false,"is_locked":false,`+
		`"is_disabled":false,"is_empty":false,"is_in_organization":true,"fork_count":1,"stargazer_count":2,"disk_usage":3,`+
		`"owner":%s,"links":{"homepage_url":"https://home.example.invalid","resource_path":"/%s"}}`,
		nameWithOwner[strings.Index(nameWithOwner, "/")+1:], nameWithOwner, nameWithOwner, listOwnerOut, nameWithOwner)
}

// listNode is a repository node with every member of the width, and the members the list never reports.
func listNode(name, parent, template string) string {
	totals := ""
	for i, pair := range repositoryTotalNames {
		totals += fmt.Sprintf(`,%q:{"totalCount":%d}`, pair[0], i+1)
	}
	return `{"name":"` + name + `","repositoryName":"` + name + `","visibility":"INTERNAL","isArchived":true,` +
		`"id":"R_1","databaseId":11,"fullDatabaseId":"12345678901234567","nameWithOwner":"octo-org/` + name + `",` +
		`"description":"A repository","url":"https://github.com/octo-org/` + name + `",` +
		`"createdAt":"2020-01-01T00:00:00Z","updatedAt":"2021-01-01T00:00:00Z","pushedAt":"2022-01-01T00:00:00Z",` +
		`"archivedAt":"2023-01-01T00:00:00Z","isFork":true,"isPrivate":true,"isTemplate":false,"isMirror":true,` +
		`"isLocked":true,"lockReason":"MOVING","isDisabled":false,"isEmpty":false,"isInOrganization":true,` +
		`"isBlankIssuesEnabled":true,"isSecurityPolicyEnabled":false,"isUserConfigurationRepository":false,` +
		`"forkCount":3,"stargazerCount":4,"diskUsage":5,"hasIssuesEnabled":true,"hasWikiEnabled":false,` +
		`"hasProjectsEnabled":true,"hasDiscussionsEnabled":false,"hasPullRequestsEnabled":true,` +
		`"hasSponsorshipsEnabled":false,"hasVulnerabilityAlertsEnabled":true,"forkingAllowed":true,` +
		`"mergeCommitAllowed":true,"squashMergeAllowed":false,"rebaseMergeAllowed":true,"autoMergeAllowed":false,` +
		`"allowUpdateBranch":true,"deleteBranchOnMerge":false,"mergeCommitTitle":"PR_TITLE",` +
		`"mergeCommitMessage":"PR_BODY","squashMergeCommitTitle":"COMMIT_OR_PR_TITLE",` +
		`"squashMergeCommitMessage":"COMMIT_MESSAGES","webCommitSignoffRequired":true,` +
		`"issueCreationPolicy":"ANYONE","pullRequestCreationPolicy":"COLLABORATORS_ONLY",` +
		`"usesCustomOpenGraphImage":false,"viewerCanAdminister":true,"viewerCanCreateIssues":true,` +
		`"viewerCanSeeIssueFields":false,"viewerCanSubscribe":true,"viewerCanUpdateTopics":false,` +
		`"viewerContentWarning":{"category":"c","customSubCategory":"cs","subCategory":"s","subTitle":"st","title":"t","type":"ty"},` +
		`"viewerDefaultCommitEmail":"me@example.invalid","viewerPossibleCommitEmails":["me@example.invalid","you@example.invalid"],` +
		`"viewerDefaultMergeMethod":"SQUASH","viewerHasStarred":true,"viewerPermission":"ADMIN",` +
		`"viewerSubscription":"SUBSCRIBED","homepageUrl":"https://home.example.invalid",` +
		`"mirrorUrl":"https://mirror.example.invalid","openGraphImageUrl":"https://og.example.invalid/i.png",` +
		`"projectsUrl":"https://github.com/octo-org/` + name + `/projects","securityPolicyUrl":"https://policy.example.invalid",` +
		`"sshUrl":"git@github.com:octo-org/` + name + `.git","resourcePath":"/octo-org/` + name + `",` +
		`"projectsResourcePath":"/octo-org/` + name + `/projects","tempCloneToken":"ghs_clonesecret",` +
		`"deployKeys":{"totalCount":2,"nodes":[{"key":"ssh-ed25519 AAAAdeploykey"}]},` +
		`"parent":` + listNestedNode(parent) + `,"templateRepository":` + listNestedNode(template) + `,` +
		`"primaryLanguage":{"id":"L_1","name":"Go","color":"#00ADD8"},` +
		`"licenseInfo":{"id":"LI_1","key":"mit","name":"MIT License","nickname":"MIT","spdxId":"MIT",` +
		`"description":"Permissive","implementation":"Add a file","featured":true,"hidden":false,` +
		`"pseudoLicense":false,"body":"License text","url":"https://choosealicense.example.invalid/mit",` +
		`"conditions":[{"key":"c","label":"Cond","description":"Keep notice"}],"limitations":[{"key":"l","label":"Lim","description":"No liability"}],` +
		`"permissions":[{"key":"p","label":"Perm","description":"Use"},{"key":"q","label":"Perm2","description":"Share"}]},` +
		`"codeOfConduct":{"id":"C_1","key":"contributor_covenant","name":"Covenant","body":"Be kind",` +
		`"url":"https://coc.example.invalid","resourcePath":"/coc"},` +
		`"contributingGuidelines":{"body":"Please help","url":"https://github.com/octo-org/` + name + `/contributing",` +
		`"resourcePath":"/octo-org/` + name + `/contributing"},` +
		`"defaultBranchRef":{"id":"B_1","name":"main","prefix":"refs/heads/"},` +
		`"latestRelease":{"id":"RE_1","databaseId":21,"name":"v1","tagName":"v1.0.0","description":"First",` +
		`"createdAt":"2022-01-01T00:00:00Z","publishedAt":"2022-01-02T00:00:00Z","updatedAt":"2022-01-03T00:00:00Z",` +
		`"immutable":true,"isDraft":false,"isLatest":true,"isPrerelease":false,"viewerCanReact":true,` +
		`"author":` + listAccount + `,"url":"https://github.com/octo-org/` + name + `/releases/tag/v1.0.0",` +
		`"resourcePath":"/octo-org/` + name + `/releases/tag/v1.0.0"},` +
		`"interactionAbility":{"expiresAt":"2030-01-01T00:00:00Z","limit":"COLLABORATORS_ONLY","origin":"REPOSITORY"},` +
		`"planFeatures":{"codeowners":true,"draftPullRequests":true,"maximumAssignees":10,` +
		`"maximumManualReviewRequests":15,"teamReviewRequests":false},` +
		`"pullRequestCreationCapConfig":{"bypassedUsers":{"totalCount":4}},` +
		`"contactLinks":[{"about":"Ask","name":"Chat","url":"https://chat.example.invalid"}],` +
		`"fundingLinks":[{"platform":"GITHUB","url":"https://github.com/sponsors/octocat"}],` +
		`"issueTemplates":[{"about":"Bug","body":"Describe","filename":"bug.md","name":"Bug","title":"[Bug]",` +
		`"type":{"id":"IT_1","name":"Bug","description":"A bug","color":"RED","isEnabled":true,"isPrivate":false}}],` +
		`"pullRequestTemplates":[{"body":"Checklist","filename":"pr.md"}],` +
		`"repositoryTopics":{"totalCount":12,"nodes":[{"id":"RT_1","url":"https://github.com/topics/go",` +
		`"resourcePath":"/topics/go","topic":{"id":"T_1","name":"go","stargazerCount":99,"viewerHasStarred":false}}]},` +
		`"pinnedIssues":{"totalCount":1,"nodes":[{"id":"PI_1","databaseId":31,"fullDatabaseId":"31","issue":` +
		`{"id":"I_1","number":5,"title":"Pinned","state":"OPEN","url":"https://github.com/octo-org/` + name + `/issues/5",` +
		`"repository":{"nameWithOwner":"octo-org/` + name + `"}},"pinnedBy":` + listAccount + `}]},` +
		`"pinnedDiscussions":{"totalCount":1,"nodes":[{"id":"PD_1","databaseId":41,"createdAt":"2022-02-01T00:00:00Z",` +
		`"updatedAt":"2022-02-02T00:00:00Z","gradientStopColors":["#fff","#000"],"pattern":"DOTS",` +
		`"preconfiguredGradient":"BLUE_MINT","discussion":{"id":"D_1","number":6,"title":"Talk",` +
		`"url":"https://github.com/octo-org/` + name + `/discussions/6"},"pinnedBy":` + listAccount + `}]},` +
		`"pinnedEnvironments":{"totalCount":1,"nodes":[{"id":"PE_1","databaseId":51,"createdAt":"2022-03-01T00:00:00Z",` +
		`"position":2,"environment":{"id":"E_1","databaseId":52,"name":"prod","isPinned":true,"pinnedPosition":2}}]},` +
		`"recentProjects":{"totalCount":1,"nodes":[{"id":"PV_1","fullDatabaseId":"61","number":3,"title":"Plan",` +
		`"shortDescription":"Short","readme":"Read me","closed":false,"closedAt":null,"createdAt":"2022-04-01T00:00:00Z",` +
		`"updatedAt":"2022-04-02T00:00:00Z","public":true,"template":false,"viewerCanClose":true,` +
		`"viewerCanReopen":false,"viewerCanUpdate":true,"url":"https://github.com/orgs/octo-org/projects/3",` +
		`"resourcePath":"/orgs/octo-org/projects/3","creator":` + listAccount + `}]},` +
		`"submodules":{"totalCount":1,"nodes":[{"branch":"main","gitUrl":"git://github.com/x/y.git","name":"y",` +
		`"nameRaw":"eQ==","path":"vendor/y","pathRaw":"dmVuZG9yL3k=","subprojectCommitOid":"abc123"}]}` + totals + `}`
}

func listExpected(name, parent, template string) string {
	totals := ""
	for i, pair := range repositoryTotalNames {
		totals += fmt.Sprintf(`,%q:%d`, pair[1]+"_total_count", i+1)
	}
	return `{"repository":"octo-org/` + name + `","visibility":"internal","archived":true,` +
		`"id":"R_1","database_id":11,"full_database_id":12345678901234567,"name":"` + name + `",` +
		`"name_with_owner":"octo-org/` + name + `","description":"A repository",` +
		`"url":"https://github.com/octo-org/` + name + `","created_at":"2020-01-01T00:00:00Z",` +
		`"updated_at":"2021-01-01T00:00:00Z","pushed_at":"2022-01-01T00:00:00Z","archived_at":"2023-01-01T00:00:00Z",` +
		`"is_fork":true,"is_private":true,"is_template":false,"is_mirror":true,"is_locked":true,` +
		`"lock_reason":"MOVING","is_disabled":false,"is_empty":false,"is_in_organization":true,` +
		`"is_blank_issues_enabled":true,"is_security_policy_enabled":false,"is_user_configuration_repository":false,` +
		`"fork_count":3,"stargazer_count":4,"disk_usage":5,"has_issues_enabled":true,"has_wiki_enabled":false,` +
		`"has_projects_enabled":true,"has_discussions_enabled":false,"has_pull_requests_enabled":true,` +
		`"has_sponsorships_enabled":false,"has_vulnerability_alerts_enabled":true,"forking_allowed":true,` +
		`"merge_commit_allowed":true,"squash_merge_allowed":false,"rebase_merge_allowed":true,"auto_merge_allowed":false,` +
		`"allow_update_branch":true,"delete_branch_on_merge":false,"merge_commit_title":"PR_TITLE",` +
		`"merge_commit_message":"PR_BODY","squash_merge_commit_title":"COMMIT_OR_PR_TITLE",` +
		`"squash_merge_commit_message":"COMMIT_MESSAGES","web_commit_signoff_required":true,` +
		`"issue_creation_policy":"ANYONE","pull_request_creation_policy":"COLLABORATORS_ONLY",` +
		`"uses_custom_open_graph_image":false,"viewer_can_administer":true,"viewer_can_create_issues":true,` +
		`"viewer_can_see_issue_fields":false,"viewer_can_subscribe":true,"viewer_can_update_topics":false,` +
		`"viewer_content_warning":{"category":"c","custom_sub_category":"cs","sub_category":"s","sub_title":"st","title":"t","type":"ty"},` +
		`"viewer_default_commit_email":"me@example.invalid","viewer_possible_commit_emails":["me@example.invalid","you@example.invalid"],` +
		`"viewer_default_merge_method":"SQUASH","viewer_has_starred":true,"viewer_permission":"ADMIN",` +
		`"viewer_subscription":"SUBSCRIBED","links":{"homepage_url":"https://home.example.invalid",` +
		`"mirror_url":"https://mirror.example.invalid","open_graph_image_url":"https://og.example.invalid/i.png",` +
		`"projects_url":"https://github.com/octo-org/` + name + `/projects","security_policy_url":"https://policy.example.invalid",` +
		`"ssh_url":"git@github.com:octo-org/` + name + `.git","resource_path":"/octo-org/` + name + `",` +
		`"projects_resource_path":"/octo-org/` + name + `/projects"},` +
		`"parent":` + parent + `,"template_repository":` + template + `,` +
		`"primary_language":{"id":"L_1","name":"Go","color":"#00ADD8"},` +
		`"license_info":{"id":"LI_1","key":"mit","name":"MIT License","nickname":"MIT","spdx_id":"MIT",` +
		`"description":"Permissive","implementation":"Add a file","featured":true,"hidden":false,` +
		`"pseudo_license":false,"body":"License text","url":"https://choosealicense.example.invalid/mit",` +
		`"conditions":[{"key":"c","label":"Cond","description":"Keep notice"}],"limitations":[{"key":"l","label":"Lim","description":"No liability"}],` +
		`"permissions":[{"key":"p","label":"Perm","description":"Use"},{"key":"q","label":"Perm2","description":"Share"}]},` +
		`"code_of_conduct":{"id":"C_1","key":"contributor_covenant","name":"Covenant","body":"Be kind",` +
		`"url":"https://coc.example.invalid","links":{"resource_path":"/coc"}},` +
		`"contributing_guidelines":{"body":"Please help","url":"https://github.com/octo-org/` + name + `/contributing",` +
		`"links":{"resource_path":"/octo-org/` + name + `/contributing"}},` +
		`"default_branch_ref":{"id":"B_1","name":"main","prefix":"refs/heads/"},` +
		`"latest_release":{"id":"RE_1","database_id":21,"name":"v1","tag_name":"v1.0.0","description":"First",` +
		`"created_at":"2022-01-01T00:00:00Z","published_at":"2022-01-02T00:00:00Z","updated_at":"2022-01-03T00:00:00Z",` +
		`"immutable":true,"is_draft":false,"is_latest":true,"is_prerelease":false,"viewer_can_react":true,` +
		`"author":` + listAccountOut + `,"url":"https://github.com/octo-org/` + name + `/releases/tag/v1.0.0",` +
		`"links":{"resource_path":"/octo-org/` + name + `/releases/tag/v1.0.0"}},` +
		`"interaction_ability":{"expires_at":"2030-01-01T00:00:00Z","limit":"COLLABORATORS_ONLY","origin":"REPOSITORY"},` +
		`"plan_features":{"codeowners":true,"draft_pull_requests":true,"maximum_assignees":10,` +
		`"maximum_manual_review_requests":15,"team_review_requests":false},` +
		`"pull_request_creation_cap_config":{"bypassed_users_total_count":4},` +
		`"contact_links":[{"about":"Ask","name":"Chat","url":"https://chat.example.invalid"}],` +
		`"funding_links":[{"platform":"GITHUB","url":"https://github.com/sponsors/octocat"}],` +
		`"issue_templates":[{"about":"Bug","body":"Describe","filename":"bug.md","name":"Bug","title":"[Bug]",` +
		`"type":{"id":"IT_1","name":"Bug","description":"A bug","color":"RED","is_enabled":true,"is_private":false}}],` +
		`"pull_request_templates":[{"body":"Checklist","filename":"pr.md"}],` +
		`"repository_topics":[{"id":"RT_1","url":"https://github.com/topics/go","links":{"resource_path":"/topics/go"},` +
		`"topic":{"id":"T_1","name":"go","stargazer_count":99,"viewer_has_starred":false}}],` +
		`"repository_topics_total_count":12,"repository_topics_truncated":true,` +
		`"pinned_issues":[{"id":"PI_1","database_id":31,"full_database_id":31,"issue":{"id":"I_1","number":5,` +
		`"title":"Pinned","state":"OPEN","url":"https://github.com/octo-org/` + name + `/issues/5",` +
		`"repository":"octo-org/` + name + `"},"pinned_by":` + listAccountOut + `}],"pinned_issues_total_count":1,` +
		`"pinned_discussions":[{"id":"PD_1","database_id":41,"created_at":"2022-02-01T00:00:00Z",` +
		`"updated_at":"2022-02-02T00:00:00Z","gradient_stop_colors":["#fff","#000"],"pattern":"DOTS",` +
		`"preconfigured_gradient":"BLUE_MINT","discussion":{"id":"D_1","number":6,"title":"Talk",` +
		`"url":"https://github.com/octo-org/` + name + `/discussions/6"},"pinned_by":` + listAccountOut + `}],` +
		`"pinned_discussions_total_count":1,` +
		`"pinned_environments":[{"id":"PE_1","database_id":51,"created_at":"2022-03-01T00:00:00Z","position":2,` +
		`"environment":{"id":"E_1","database_id":52,"name":"prod","is_pinned":true,"pinned_position":2}}],` +
		`"pinned_environments_total_count":1,` +
		`"recent_projects":[{"id":"PV_1","full_database_id":61,"number":3,"title":"Plan","short_description":"Short",` +
		`"readme":"Read me","closed":false,"created_at":"2022-04-01T00:00:00Z","updated_at":"2022-04-02T00:00:00Z",` +
		`"public":true,"template":false,"viewer_can_close":true,"viewer_can_reopen":false,"viewer_can_update":true,` +
		`"creator":` + listAccountOut + `,"url":"https://github.com/orgs/octo-org/projects/3",` +
		`"links":{"resource_path":"/orgs/octo-org/projects/3"}}],"recent_projects_total_count":1,` +
		`"submodules":[{"branch":"main","name":"y","name_raw":"eQ==","path":"vendor/y","path_raw":"dmVuZG9yL3k=",` +
		`"subproject_commit_oid":"abc123","links":{"git_url":"git://github.com/x/y.git"}}],"submodules_total_count":1` +
		totals + `}`
}

// answerRepositories makes every GraphQL call answer with the given nodes as the only page.
func answerRepositories(f *fakeGitHub, nodes ...string) {
	edges := make([]string, len(nodes))
	for i, node := range nodes {
		edges[i] = `{"cursor":"c` + strconv.Itoa(i) + `","node":` + node + `}`
	}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/graphql" {
			return false
		}
		fmt.Fprintf(w, `{"data":{"owner":{"repositories":{"pageInfo":{"hasNextPage":false,"endCursor":"c%d"},"edges":[%s]}}}}`,
			len(nodes)-1, strings.Join(edges, ","))
		return true
	}
}

func decodeExact(t *testing.T, data string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return value
}

// Every member of the width reaches the output with its exact value; the clone token and the deploy keys never do.
func TestRepositoryListReportsTheWholeWidth(t *testing.T) {
	f := &fakeGitHub{}
	answerRepositories(f, listNode("repo-00", "upstream/base", "octo-org/skeleton"))
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Repositories []json.RawMessage `json:"repositories"`
	}
	if err := json.Unmarshal(result, &got); err != nil || len(got.Repositories) != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	want := listExpected("repo-00", listNestedOut("upstream/base"), listNestedOut("octo-org/skeleton"))
	if g, w := decodeExact(t, string(got.Repositories[0])), decodeExact(t, want); !reflect.DeepEqual(g, w) {
		gj, _ := json.MarshalIndent(g, "", " ")
		wj, _ := json.MarshalIndent(w, "", " ")
		t.Errorf("entry differs:\n got %s\nwant %s", gj, wj)
	}
	for _, secret := range []string{"ghs_clonesecret", "AAAAdeploykey", "temp_clone_token", "deploy_keys\":"} {
		if strings.Contains(string(result), secret) {
			t.Errorf("the output carries %q", secret)
		}
	}
}

// A repository outside the targets keeps only its identifiers where the width names it as parent or template;
// one the targets allow is shown in full.
func TestRepositoryListParentOutsideTheTargets(t *testing.T) {
	f := &fakeGitHub{}
	answerRepositories(f, listNode("repo-03", "upstream/base", "octo-org/repo-07"))
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)
	result, err := invoke(t, core, repositoriesList.ID, "concrete", `{"owner":"orgs/octo-org"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Repositories []struct {
			Parent   map[string]any `json:"parent"`
			Template map[string]any `json:"template_repository"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(result, &got); err != nil || len(got.Repositories) != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	parent, template := got.Repositories[0].Parent, got.Repositories[0].Template
	if len(parent) != 4 || parent["name_with_owner"] != "upstream/base" || parent["id"] != "R_n" ||
		parent["database_id"] == nil || parent["full_database_id"] == nil {
		t.Errorf("parent outside the targets = %v, want only identifiers", parent)
	}
	if template["owner"] == nil || template["description"] != "nested" {
		t.Errorf("template inside the targets = %v, want it in full", template)
	}
}

// Filters reach GitHub as variables, bind the cursor, and the order stays the name when none is given.
func TestRepositoryListFilters(t *testing.T) {
	f := &fakeGitHub{}
	for i := 0; i < 5; i++ {
		f.ownerRepos = append(f.ownerRepos, fmt.Sprintf("repo-%02d", i))
	}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)

	last := func() map[string]any {
		requests := f.recorded()
		return requests[len(requests)-1].variables
	}
	if _, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org","limit":1}`, false); err != nil {
		t.Fatal(err)
	}
	if v := last(); !reflect.DeepEqual(v["orderBy"], map[string]any{"field": "NAME", "direction": "ASC"}) ||
		v["privacy"] != nil || v["isArchived"] != nil || v["isFork"] != nil || v["isLocked"] != nil ||
		v["hasIssuesEnabled"] != nil || v["visibility"] != nil {
		t.Errorf("default variables = %v, want name order and no filter", v)
	}

	args := `{"owner":"orgs/octo-org","limit":1,"privacy":"PRIVATE","is_archived":false,"is_fork":true,` +
		`"is_locked":false,"has_issues_enabled":true,"order_by":"STARGAZERS","direction":"DESC"}`
	first, err := invoke(t, core, repositoriesList.ID, "open", args, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"privacy": "PRIVATE", "isArchived": false, "isFork": true, "isLocked": false,
		"hasIssuesEnabled": true, "orderBy": map[string]any{"field": "STARGAZERS", "direction": "DESC"}}
	v := last()
	for name, value := range want {
		if !reflect.DeepEqual(v[name], value) {
			t.Errorf("variable %s = %v, want %v", name, v[name], value)
		}
	}
	if _, ok := v["visibility"]; ok {
		t.Errorf("visibility was sent without being set: %v", v)
	}
	cursor := nextCursorOf(first)
	if cursor == "" {
		t.Fatalf("first batch = %s, want a cursor", first)
	}
	if _, err := invoke(t, core, repositoriesList.ID, "open", strings.Replace(args, `"limit":1`, `"limit":1,"cursor":"`+cursor+`"`, 1),
		false); err != nil {
		t.Errorf("the cursor with the same filters = %v", err)
	}
	for _, change := range []string{`"direction":"ASC"`, `"order_by":"NAME"`, `"is_fork":false`, `"visibility":"INTERNAL"`} {
		changed := regexp.MustCompile(`"`+strings.Split(strings.Trim(change, `"`), `"`)[0]+`":("[A-Z_]+"|true|false)`).
			ReplaceAllString(args, "")
		changed = strings.Replace(changed, `"limit":1`, `"limit":1,`+change+`,"cursor":"`+cursor+`"`, 1)
		changed = strings.ReplaceAll(strings.ReplaceAll(changed, ",,", ","), ",}", "}")
		if _, err := invoke(t, core, repositoriesList.ID, "open", changed, false); !isInvalidRequest(err) {
			t.Errorf("the cursor with changed filter %s = %v, want an invalid request", change, err)
		}
	}
	// A cursor of an unfiltered list does not continue a filtered one either.
	plain, _ := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org","limit":1}`, false)
	if _, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org","is_fork":true,"cursor":"`+
		nextCursorOf(plain)+`"}`, false); !isInvalidRequest(err) {
		t.Errorf("an unfiltered cursor on a filtered list = %v, want an invalid request", err)
	}
}

// Privacy and visibility exclude each other in GitHub; the request ends before a credential is read.
func TestRepositoryListRefusesPrivacyWithVisibility(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, &reads), red)
	for _, arguments := range []string{`{"owner":"orgs/octo-org","privacy":"PUBLIC","visibility":"PUBLIC"}`,
		`{"owner":"orgs/octo-org","visibility":"SECRET"}`, `{"owner":"orgs/octo-org","order_by":"SIZE"}`,
		`{"owner":"orgs/octo-org","direction":"UP"}`, `{"owner":"orgs/octo-org","is_fork":"yes"}`} {
		if _, err := invoke(t, core, repositoriesList.ID, "open", arguments, false); !isInvalidRequest(err) || reads != 0 ||
			len(f.recorded()) != 0 {
			t.Errorf("%s = %v, want an invalid request before the secret and GitHub", arguments, err)
		}
	}
}

// The list asks for no member that is deprecated, rendered, a lookup, or a secret, and a page of 100 repositories
// stays far below GitHub's node limit of 500000.
func TestRepositoryListQueryStaysWithinTheNodeLimit(t *testing.T) {
	selection := repositoryWidthSelection
	for _, forbidden := range []string{"tempCloneToken", "deployKeys{totalCount nodes", "descriptionHTML",
		"shortDescriptionHTML", "squashPrTitleUsedAsDefault", "viewerCanCreateProjects", " projects(", " project(",
		"codeowners(", "mergeQueue", "issue(", "pullRequest(", "discussion(", "release(", "ruleset(", "object(",
		"environment(", "ref(", "label(", "milestone("} {
		if strings.Contains(selection, forbidden) {
			t.Errorf("the selection asks for %q", forbidden)
		}
	}
	if !strings.Contains(selection, "deployKeys{totalCount}") {
		t.Error("the selection does not read deploy keys as a count")
	}
	// Every connection that carries nodes asks for the cap; none sits inside another.
	pages := regexp.MustCompile(`first:(\d+)\)\{totalCount nodes\{`).FindAllStringSubmatch(selection, -1)
	if len(pages) != 6 {
		t.Fatalf("connections with a first page = %d, want 6", len(pages))
	}
	perRepository := 0
	for _, page := range pages {
		size, _ := strconv.Atoi(page[1])
		if size != repositoryListCap {
			t.Errorf("a connection asks for %d nodes, want %d", size, repositoryListCap)
		}
		perRepository += size
	}
	if got := strings.Count(selection, "first:"); got != len(pages) {
		t.Errorf("first: occurs %d times, want only the %d pages", got, len(pages))
	}
	const pageSize = 100
	if pageSize != maxLimit {
		t.Fatalf("the largest page is %d repositories, the node count below assumes %d", maxLimit, pageSize)
	}
	nodes := pageSize + pageSize*perRepository
	if nodes != 6100 || nodes > 500000 {
		t.Errorf("a page asks for %d nodes, want 6100 and below 500000", nodes)
	}
}

// A repository node that lacks every new member yields the compact entry alone.
func TestRepositoryListWithoutTheWidthIsUnchanged(t *testing.T) {
	f := &fakeGitHub{}
	answerRepositories(f, `{"name":"repo-00","visibility":"PUBLIC","isArchived":false}`)
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)
	result, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org"}`, false)
	want := `{"has_more":false,"owner":"orgs/octo-org","repositories":[{"archived":false,"repository":"octo-org/repo-00",` +
		`"visibility":"public"}]}`
	if err != nil || !bytes.Equal(result, []byte(want)) {
		t.Errorf("result = %s, %v\nwant %s", result, err, want)
	}
}

// Lists and first pages are cut at the cap with their total and a truncated marker.
func TestRepositoryListCutsListsAndPages(t *testing.T) {
	topics := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		topics = append(topics, fmt.Sprintf(`{"id":"RT_%d","topic":{"name":"t%d"}}`, i, i))
	}
	links := strings.Repeat(`{"platform":"GITHUB","url":"https://example.invalid/f"},`, 11)
	f := &fakeGitHub{}
	answerRepositories(f, `{"name":"repo-00","visibility":"PUBLIC","isArchived":false,"repositoryTopics":`+
		`{"totalCount":12,"nodes":[`+strings.Join(topics[:10], ",")+`]},"fundingLinks":[`+strings.TrimSuffix(links, ",")+`]}`)
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)
	result, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Repositories []struct {
			Topics          []any `json:"repository_topics"`
			TopicsTotal     int   `json:"repository_topics_total_count"`
			TopicsTruncated bool  `json:"repository_topics_truncated"`
			Funding         []any `json:"funding_links"`
			FundingCut      bool  `json:"funding_links_truncated"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(result, &got); err != nil || len(got.Repositories) != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	r := got.Repositories[0]
	if len(r.Topics) != 10 || r.TopicsTotal != 12 || !r.TopicsTruncated || len(r.Funding) != 10 || !r.FundingCut {
		t.Errorf("entry = %+v, want 10 topics of 12 and 10 funding links, both marked", r)
	}
}

// answerWithErrors answers every GraphQL call with two repositories and the given errors.
func answerWithErrors(f *fakeGitHub, errors string) {
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/graphql" {
			return false
		}
		fmt.Fprintf(w, `{"data":{"owner":{"repositories":{"pageInfo":{"hasNextPage":false,"endCursor":"c1"},"edges":[`+
			`{"cursor":"c0","node":{"name":"repo-00","visibility":"PUBLIC","isArchived":false,"collaborators":null,`+
			`"viewerDefaultCommitEmail":null,"issues":{"totalCount":3}}},`+
			`{"cursor":"c1","node":{"name":"repo-01","visibility":"PUBLIC","isArchived":false,"collaborators":{"totalCount":2},`+
			`"issues":{"totalCount":4}}}]}}},"errors":%s}`, errors)
		return true
	}
}

// A member GitHub refuses a token inside one repository is left out of that entry and named; any other
// error still fails the list.
func TestRepositoryListToleratesRefusedMembers(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), ownersConfig(base), resolver(red, nil), red)

	answerWithErrors(f, `[{"type":"FORBIDDEN","path":["owner","repositories","edges",0,"node","collaborators"],"message":"x"},`+
		`{"type":"INSUFFICIENT_SCOPES","path":["owner","repositories","edges",0,"node","viewerDefaultCommitEmail"],"message":"x"}]`)
	result, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Repositories []map[string]any `json:"repositories"`
	}
	if err := json.Unmarshal(result, &got); err != nil || len(got.Repositories) != 2 {
		t.Fatalf("result = %s, %v", result, err)
	}
	first, second := got.Repositories[0], got.Repositories[1]
	if _, ok := first["collaborators_total_count"]; ok || first["issues_total_count"] != float64(3) ||
		!reflect.DeepEqual(first["unreadable"], []any{"collaborators_total_count", "viewer_default_commit_email"}) {
		t.Errorf("first entry = %v, want the refused members named and absent", first)
	}
	if _, ok := second["unreadable"]; ok || second["collaborators_total_count"] != float64(2) {
		t.Errorf("second entry = %v, want it complete", second)
	}

	for _, tt := range []struct{ name, errors string }{
		{"the owner itself", `[{"type":"FORBIDDEN","path":["owner"],"message":"x"}]`},
		{"the list", `[{"type":"FORBIDDEN","path":["owner","repositories"],"message":"x"}]`},
		{"another type", `[{"type":"SOMETHING","path":["owner","repositories","edges",0,"node","collaborators"],"message":"x"}]`},
		{"not found", `[{"type":"NOT_FOUND","path":["owner","repositories","edges",0,"node","collaborators"],"message":"x"}]`},
		{"rate limited", `[{"type":"RATE_LIMITED","path":["owner","repositories","edges",0,"node","collaborators"],"message":"x"}]`},
		{"a compact member", `[{"type":"FORBIDDEN","path":["owner","repositories","edges",0,"node","name"],"message":"x"}]`},
		{"an index beyond the page", `[{"type":"FORBIDDEN","path":["owner","repositories","edges",5,"node","collaborators"],"message":"x"}]`},
		{"one tolerated and one not", `[{"type":"FORBIDDEN","path":["owner","repositories","edges",0,"node","collaborators"],"message":"x"},` +
			`{"type":"FORBIDDEN","path":["owner"],"message":"x"}]`},
	} {
		answerWithErrors(f, tt.errors)
		if _, err := invoke(t, core, repositoriesList.ID, "open", `{"owner":"orgs/octo-org"}`, false); err == nil {
			t.Errorf("%s: the list succeeded, want the failure it had before", tt.name)
		}
	}
}
