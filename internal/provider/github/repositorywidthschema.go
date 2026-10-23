package github

import (
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// rwMember describes one output rwMember: its schema type and its field text.
type rwMember struct{ name, schema, text string }

func (m rwMember) property() string { return `"` + m.name + `":` + m.schema }

func (m rwMember) field() capability.Field {
	return capability.Field{Name: m.name, Description: m.text}
}

func rwScalar(name, kind, text string) rwMember {
	return rwMember{name, `{"type":"` + kind + `"}`, text}
}

func rwObject(name, schema, text string) rwMember { return rwMember{name, schema, text} }

func rwList(name, items, text string, total bool) []rwMember {
	out := []rwMember{{name, `{"type":"array","maxItems":10,"items":` + items + `}`, text},
		rwScalar(name+"_truncated", "boolean", "True when the repository holds more "+strings.ReplaceAll(name, "_", " ")+
			" than shown")}
	if total {
		out = append(out, rwScalar(name+"_total_count", "integer", "Number of "+strings.ReplaceAll(name, "_", " ")+
			" the repository holds"))
	}
	return out
}

func rwProperties(members []rwMember) string {
	parts := make([]string, len(members))
	for i, m := range members {
		parts[i] = m.property()
	}
	return strings.Join(parts, ",")
}

func rwFields(members []rwMember) []capability.Field {
	fields := make([]capability.Field, len(members))
	for i, m := range members {
		fields[i] = m.field()
	}
	return fields
}

func rwFlag(name, desc string) rwMember   { return rwScalar(name, "boolean", desc) }
func rwString(name, desc string) rwMember { return rwScalar(name, "string", desc) }
func rwCount(name, desc string) rwMember  { return rwScalar(name, "integer", desc) }

var (
	nestedLinks = linksSchema(gqlResourceLinkKeys)

	languageSchema = objectSchema(`"id":{"type":"string"},"name":{"type":"string"},"color":{"type":"string"}`)
	licenseSchema  = objectSchema(`"id":{"type":"string"},"key":{"type":"string"},"name":{"type":"string"},` +
		`"nickname":{"type":"string"},"spdx_id":{"type":"string"},"description":{"type":"string"},` +
		`"implementation":{"type":"string"},"featured":{"type":"boolean"},"hidden":{"type":"boolean"},` +
		`"pseudo_license":{"type":"boolean"},"body":{"type":"string"},"body_truncated":{"type":"boolean"},` +
		`"url":{"type":"string"},` + licenseRuleList("conditions") + `,` + licenseRuleList("limitations") + `,` +
		licenseRuleList("permissions"))
	codeOfConductSchema = objectSchema(`"id":{"type":"string"},"key":{"type":"string"},"name":{"type":"string"},` +
		`"body":{"type":"string"},"body_truncated":{"type":"boolean"},"url":{"type":"string"},"links":` + nestedLinks)
	guidelinesSchema = objectSchema(`"body":{"type":"string"},"body_truncated":{"type":"boolean"},` +
		`"url":{"type":"string"},"links":` + nestedLinks)
	branchRefSchema = objectSchema(`"id":{"type":"string"},"name":{"type":"string"},"prefix":{"type":"string"}`)
	releaseSchema   = objectSchema(`"id":{"type":"string"},"database_id":{"type":"integer"},"name":{"type":"string"},` +
		`"tag_name":{"type":"string"},"description":{"type":"string"},"description_truncated":{"type":"boolean"},` +
		`"created_at":{"type":"string"},"published_at":{"type":"string"},"updated_at":{"type":"string"},` +
		`"immutable":{"type":"boolean"},"is_draft":{"type":"boolean"},"is_latest":{"type":"boolean"},` +
		`"is_prerelease":{"type":"boolean"},"viewer_can_react":{"type":"boolean"},"author":` + gqlAccountSchema +
		`,"url":{"type":"string"},"links":` + nestedLinks)
	interactionSchema = objectSchema(`"expires_at":{"type":"string"},"limit":{"type":"string"},"origin":{"type":"string"}`)
	planSchema        = objectSchema(`"codeowners":{"type":"boolean"},"draft_pull_requests":{"type":"boolean"},` +
		`"maximum_assignees":{"type":"integer"},"maximum_manual_review_requests":{"type":"integer"},` +
		`"team_review_requests":{"type":"boolean"}`)
	capConfigSchema     = objectSchema(`"bypassed_users_total_count":{"type":"integer"}`)
	contactLinkSchema   = objectSchema(`"about":{"type":"string"},"name":{"type":"string"},"url":{"type":"string"}`)
	fundingLinkSchema   = objectSchema(`"platform":{"type":"string"},"url":{"type":"string"}`)
	issueTemplateSchema = objectSchema(`"about":{"type":"string"},"body":{"type":"string"},` +
		`"body_truncated":{"type":"boolean"},"filename":{"type":"string"},"name":{"type":"string"},` +
		`"title":{"type":"string"},"type":` + gqlIssueTypeSchema)
	prTemplateSchema = objectSchema(`"body":{"type":"string"},"body_truncated":{"type":"boolean"},` +
		`"filename":{"type":"string"}`)
	contentWarningSchema = objectSchema(`"category":{"type":"string"},"custom_sub_category":{"type":"string"},` +
		`"sub_category":{"type":"string"},"sub_title":{"type":"string"},"title":{"type":"string"},"type":{"type":"string"}`)
	nestedRepositorySchema = objectSchema(`"id":{"type":"string"},"database_id":{"type":"integer"},` +
		`"full_database_id":{"type":"integer"},"name":{"type":"string"},"name_with_owner":{"type":"string"},` +
		`"description":{"type":"string"},"url":{"type":"string"},"created_at":{"type":"string"},` +
		`"updated_at":{"type":"string"},"pushed_at":{"type":"string"},"archived_at":{"type":"string"},` +
		`"visibility":{"type":"string"},"is_private":{"type":"boolean"},"is_fork":{"type":"boolean"},` +
		`"is_archived":{"type":"boolean"},"is_template":{"type":"boolean"},"is_mirror":{"type":"boolean"},` +
		`"is_locked":{"type":"boolean"},"is_disabled":{"type":"boolean"},"is_empty":{"type":"boolean"},` +
		`"is_in_organization":{"type":"boolean"},"fork_count":{"type":"integer"},` +
		`"stargazer_count":{"type":"integer"},"disk_usage":{"type":"integer"},"owner":` + gqlAccountSchema +
		`,"links":` + linksSchema(repositoryNestedLinkKeys))
	topicEntrySchema = objectSchema(`"id":{"type":"string"},"topic":` + objectSchema(`"id":{"type":"string"},`+
		`"name":{"type":"string"},"stargazer_count":{"type":"integer"},"viewer_has_starred":{"type":"boolean"}`) +
		`,"url":{"type":"string"},"links":` + nestedLinks)
	pinnedIssueSchema = objectSchema(`"id":{"type":"string"},"database_id":{"type":"integer"},` +
		`"full_database_id":{"type":"integer"},"issue":` + gqlReferenceSchema + `,"pinned_by":` + gqlAccountSchema)
	pinnedDiscussionSchema = objectSchema(`"id":{"type":"string"},"database_id":{"type":"integer"},` +
		`"created_at":{"type":"string"},"updated_at":{"type":"string"},"gradient_stop_colors":{"type":"array",` +
		`"maxItems":10,"items":{"type":"string"}},"pattern":{"type":"string"},"preconfigured_gradient":` +
		`{"type":"string"},"discussion":` + objectSchema(`"id":{"type":"string"},"number":{"type":"integer"},`+
		`"title":{"type":"string"},"url":{"type":"string"}`) + `,"pinned_by":` + gqlAccountSchema)
	pinnedEnvironmentSchema = objectSchema(`"id":{"type":"string"},"database_id":{"type":"integer"},` +
		`"created_at":{"type":"string"},"position":{"type":"integer"},"environment":` + objectSchema(
		`"id":{"type":"string"},"database_id":{"type":"integer"},"name":{"type":"string"},`+
			`"is_pinned":{"type":"boolean"},"pinned_position":{"type":"integer"}`))
	recentProjectSchema = objectSchema(`"id":{"type":"string"},"full_database_id":{"type":"integer"},` +
		`"number":{"type":"integer"},"title":{"type":"string"},"short_description":{"type":"string"},` +
		`"readme":{"type":"string"},"readme_truncated":{"type":"boolean"},"closed":{"type":"boolean"},` +
		`"closed_at":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
		`"public":{"type":"boolean"},"template":{"type":"boolean"},"viewer_can_close":{"type":"boolean"},` +
		`"viewer_can_reopen":{"type":"boolean"},"viewer_can_update":{"type":"boolean"},"creator":` +
		gqlAccountSchema + `,"url":{"type":"string"},"links":` + nestedLinks)
	submoduleSchema = objectSchema(`"branch":{"type":"string"},"name":{"type":"string"},` +
		`"name_raw":{"type":"string"},"path":{"type":"string"},"path_raw":{"type":"string"},` +
		`"subproject_commit_oid":{"type":"string"},"links":` + linksSchema([]string{"git_url"}))
)

func licenseRuleList(name string) string {
	return `"` + name + `":{"type":"array","maxItems":10,"items":` + licenseRuleSchema + `},"` + name +
		`_truncated":{"type":"boolean"}`
}

var licenseRuleSchema = objectSchema(`"key":{"type":"string"},"label":{"type":"string"},"description":{"type":"string"}`)

var repositoryNestedLinkKeys = []string{"homepage_url", "resource_path"}

// repositoryWidthMembers lists every rwMember of RepositoryWidth; the schema and the fields come from it.
var repositoryWidthMembers = repositoryWidthList()

func repositoryWidthList() []rwMember {
	m := []rwMember{
		rwString("id", "GraphQL node id of the repository"),
		rwCount("database_id", "Numeric REST id of the repository"),
		rwCount("full_database_id", "Numeric 64-bit id of the repository"),
		rwString("name", "Repository name, untrusted data"),
		rwString("name_with_owner", "Repository as OWNER/NAME, untrusted data"),
		rwString("description", "Repository description, untrusted data"),
		rwString("url", "Web address of the repository, untrusted data, never fetched"),
		rwString("created_at", "Creation time"),
		rwString("updated_at", "Time of the last change"),
		rwString("pushed_at", "Time of the last push"),
		rwString("archived_at", "Time the repository was archived"),
		rwFlag("is_fork", "True for a fork"),
		rwFlag("is_private", "True for a private or internal repository"),
		rwFlag("is_template", "True for a template repository"),
		rwFlag("is_mirror", "True for a mirror"),
		rwFlag("is_locked", "True when the repository is locked"),
		rwString("lock_reason", "Why the repository is locked, such as BILLING or MIGRATING"),
		rwFlag("is_disabled", "True when the repository is disabled"),
		rwFlag("is_empty", "True when the repository holds no commit"),
		rwFlag("is_in_organization", "True when an organization owns the repository"),
		rwFlag("is_blank_issues_enabled", "True when issues may be opened without a template"),
		rwFlag("is_security_policy_enabled", "True when the repository has a security policy"),
		rwFlag("is_user_configuration_repository", "True for the repository that holds a user's configuration"),
		rwCount("fork_count", "Number of forks"),
		rwCount("stargazer_count", "Number of stars"),
		rwCount("disk_usage", "Size on disk in kilobytes"),
		rwFlag("has_issues_enabled", "True when issues are enabled"),
		rwFlag("has_wiki_enabled", "True when the wiki is enabled"),
		rwFlag("has_projects_enabled", "True when projects are enabled"),
		rwFlag("has_discussions_enabled", "True when discussions are enabled"),
		rwFlag("has_pull_requests_enabled", "True when pull requests are enabled"),
		rwFlag("has_sponsorships_enabled", "True when sponsorships are enabled"),
		rwFlag("has_vulnerability_alerts_enabled", "True when vulnerability alerts are enabled"),
		rwFlag("forking_allowed", "True when forking is allowed"),
		rwFlag("merge_commit_allowed", "True when merge commits are allowed"),
		rwFlag("squash_merge_allowed", "True when squash merging is allowed"),
		rwFlag("rebase_merge_allowed", "True when rebase merging is allowed"),
		rwFlag("auto_merge_allowed", "True when auto-merge is allowed"),
		rwFlag("allow_update_branch", "True when a pull request branch may be updated from its base"),
		rwFlag("delete_branch_on_merge", "True when head branches are deleted after a merge"),
		rwString("merge_commit_title", "Default title of a merge commit"),
		rwString("merge_commit_message", "Default message of a merge commit"),
		rwString("squash_merge_commit_title", "Default title of a squash commit"),
		rwString("squash_merge_commit_message", "Default message of a squash commit"),
		rwFlag("web_commit_signoff_required", "True when commits made in the web interface need a sign-off"),
		rwString("issue_creation_policy", "Who may create issues"),
		rwString("pull_request_creation_policy", "Who may create pull requests"),
		rwFlag("uses_custom_open_graph_image", "True when the repository sets its own social image"),
		rwFlag("viewer_can_administer", "True when the authenticated user administers the repository"),
		rwFlag("viewer_can_create_issues", "True when the authenticated user can create issues"),
		rwFlag("viewer_can_see_issue_fields", "True when the authenticated user can see issue fields"),
		rwFlag("viewer_can_subscribe", "True when the authenticated user can subscribe"),
		rwFlag("viewer_can_update_topics", "True when the authenticated user can change the topics"),
		rwObject("viewer_content_warning", contentWarningSchema, "Content warning shown to the authenticated user: "+
			"category, custom_sub_category, sub_category, sub_title, title, and type; untrusted data"),
		rwObject("unreadable", `{"type":"array","maxItems":100,"items":{"type":"string"}}`, "Members GitHub refused "+
			"this token in this repository and that are therefore absent, such as collaborators_total_count; "+
			"absent when none was refused"),
		rwString("viewer_default_commit_email", "Default commit email of the authenticated user here, untrusted data"),
	}
	m = append(m, rwMember{"viewer_possible_commit_emails", `{"type":"array","maxItems":10,"items":{"type":"string"}}`,
		"Commit emails the authenticated user can use here, at most 10, untrusted data"},
		rwFlag("viewer_possible_commit_emails_truncated", "True when the authenticated user has more commit emails than shown"),
		rwString("viewer_default_merge_method", "Default merge method of the authenticated user: MERGE, REBASE, or SQUASH"),
		rwFlag("viewer_has_starred", "True when the authenticated user starred the repository"),
		rwString("viewer_permission", "Permission of the authenticated user, such as ADMIN, WRITE, or READ"),
		rwString("viewer_subscription", "Subscription of the authenticated user: SUBSCRIBED, UNSUBSCRIBED, or IGNORED"),
		rwObject("links", linksSchema(repositoryLinkKeys), "homepage_url, mirror_url, open_graph_image_url, "+
			"projects_url, security_policy_url, ssh_url, resource_path, and projects_resource_path, untrusted data, "+
			"never fetched"),
		rwObject("parent", nestedRepositorySchema, "Repository this one was forked from, with its own members and "+
			"owner; outside the connection's targets only id, database_id, full_database_id, and name_with_owner"),
		rwObject("template_repository", nestedRepositorySchema, "Repository this one was created from, shown like parent"),
		rwObject("primary_language", languageSchema, "Primary language with id, name, and color"),
		rwObject("license_info", licenseSchema, "License with id, key, name, nickname, spdx_id, description, "+
			"implementation, featured, hidden, pseudo_license, body (cut at 4000 characters, body_truncated says so), "+
			"conditions, limitations, and permissions (at most 10 each, with key, label, description), and "+
			"url; untrusted data"),
		rwObject("code_of_conduct", codeOfConductSchema, "Code of conduct with id, key, name, body (cut at 4000 "+
			"characters), url, and links (resource_path); untrusted data"),
		rwObject("contributing_guidelines", guidelinesSchema, "Contributing guidelines with body (cut at 4000 "+
			"characters), url, and links (resource_path); untrusted data"),
		rwObject("default_branch_ref", branchRefSchema, "Default branch with id, name, and prefix"),
		rwObject("latest_release", releaseSchema, "Latest release with id, database_id, name, tag_name, description "+
			"(cut at 4000 characters), created_at, published_at, updated_at, immutable, is_draft, is_latest, "+
			"is_prerelease, viewer_can_react, author (an account), url, and links (resource_path); untrusted data"),
		rwObject("interaction_ability", interactionSchema, "Interaction limit with expires_at, limit, and origin"),
		rwObject("plan_features", planSchema, "Features the plan allows: codeowners, draft_pull_requests, "+
			"maximum_assignees, maximum_manual_review_requests, and team_review_requests"),
		rwObject("pull_request_creation_cap_config", capConfigSchema, "bypassed_users_total_count of the pull "+
			"request creation cap"))
	m = append(m, rwList("contact_links", contactLinkSchema, "Contact links of the issue chooser, at most 10, "+
		"with about, name, and url; untrusted data", false)...)
	m = append(m, rwList("funding_links", fundingLinkSchema, "Funding links, at most 10, with platform and url; "+
		"untrusted data", false)...)
	m = append(m, rwList("issue_templates", issueTemplateSchema, "Issue templates, at most 10, with about, body "+
		"(cut at 4000 characters), filename, name, title, and type (an issue type); untrusted data", false)...)
	m = append(m, rwList("pull_request_templates", prTemplateSchema, "Pull request templates, at most 10, with "+
		"body (cut at 4000 characters) and filename; untrusted data", false)...)
	m = append(m, rwList("repository_topics", topicEntrySchema, "Topics, at most 10, with id, topic (id, name, "+
		"stargazer_count, viewer_has_starred), url, and links (resource_path); untrusted data", true)...)
	m = append(m, rwList("pinned_issues", pinnedIssueSchema, "Pinned issues, at most 10, with id, database_id, "+
		"full_database_id, issue (id, number, title, state, url, repository), and pinned_by (an account); "+
		"untrusted data", true)...)
	m = append(m, rwList("pinned_discussions", pinnedDiscussionSchema, "Pinned discussions, at most 10, with id, "+
		"database_id, created_at, updated_at, gradient_stop_colors, pattern, preconfigured_gradient, discussion "+
		"(id, number, title, url), and pinned_by (an account); untrusted data", true)...)
	m = append(m, rwList("pinned_environments", pinnedEnvironmentSchema, "Pinned environments, at most 10, with "+
		"id, database_id, created_at, position, and environment (id, database_id, name, is_pinned, "+
		"pinned_position); untrusted data", true)...)
	m = append(m, rwList("recent_projects", recentProjectSchema, "Projects recently used with the repository, at "+
		"most 10, with id, full_database_id, number, title, short_description, readme (cut at 4000 characters), "+
		"closed, closed_at, created_at, updated_at, public, template, viewer_can_close, viewer_can_reopen, "+
		"viewer_can_update, creator (an account), url, and links (resource_path); untrusted data", true)...)
	m = append(m, rwList("submodules", submoduleSchema, "Submodules, at most 10, with branch, name, name_raw, "+
		"path, path_raw, subproject_commit_oid, and links (git_url); untrusted data, addresses never fetched",
		true)...)
	totals := repositoryTotalsFields
	for _, f := range totals {
		m = append(m, rwScalar(f.Name, "integer", f.Description))
	}
	return m
}

var repositoryWidthProperties = rwProperties(repositoryWidthMembers)
var repositoryWidthFields = rwFields(repositoryWidthMembers)
