package forge

// The issue tools read the API's issue and timeline objects with every member they document, except those the
// API deprecates in all supported versions. A member one product does not know is offered for the other only.
// An object that may lie outside the targets, such as an issue another issue refers to, keeps only its
// identifiers.

const (
	// maxIssueText bounds the body of an issue and the text of a timeline event.
	maxIssueText = 64 << 10
	// maxIssueTitle bounds an issue title in characters.
	maxIssueTitle = 255
)

var labelMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the label"},
	{name: "name", kind: kindText, note: "Label name, untrusted data"},
	{name: "description", kind: kindText, note: "Description, untrusted data"},
	{name: "color", kind: kindText, note: "Color as hexadecimal digits"},
	{name: "exclusive", kind: kindFlag, note: "True when the label excludes others of its scope"},
	{name: "is_archived", kind: kindFlag, note: "True for an archived label"},
	{name: "url", kind: kindText, note: "API address of the label"},
}

var milestoneMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the milestone"},
	{name: "title", kind: kindText, note: "Title, untrusted data"},
	{name: "description", kind: kindText, note: "Description, untrusted data"},
	{name: "state", kind: kindText, note: "open or closed"},
	{name: "open_issues", kind: kindInteger, note: "Number of open issues"},
	{name: "closed_issues", kind: kindInteger, note: "Number of closed issues"},
	{name: "due_on", kind: kindText, note: "Due time"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, note: "Time of the last update"},
	{name: "closed_at", kind: kindText, note: "Time it was closed"},
}

var attachmentMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the attachment"},
	{name: "name", kind: kindText, note: "File name, untrusted data"},
	{name: "size", kind: kindInteger, note: "Size in bytes"},
	{name: "type", kind: kindText, note: "attachment or external"},
	{name: "uuid", kind: kindText, note: "UUID of the attachment"},
	{name: "download_count", kind: kindInteger, note: "Number of downloads"},
	{name: "browser_download_url", kind: kindText, address: true, note: "Download address; left out when it " +
		"carries user info or a query"},
	{name: "created_at", kind: kindText, note: "Creation time"},
}

var projectMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the project"},
	{name: "title", kind: kindText, note: "Title, untrusted data"},
	{name: "description", kind: kindText, note: "Description, untrusted data"},
	{name: "html_url", kind: kindText, note: "Address of the project page"},
	{name: "state", kind: kindText, note: "open or closed; Gitea 28 and later"},
	{name: "is_closed", kind: kindFlag, note: "True for a closed project; Gitea 1.27"},
	{name: "card_type", kind: kindText, note: "text_only or images_and_text"},
	{name: "template_type", kind: kindText, note: "Template the columns came from"},
	{name: "type", kind: kindText, note: "Scope of the project"},
	{name: "num_issues", kind: kindInteger, note: "Number of issues"},
	{name: "num_open_issues", kind: kindInteger, note: "Number of open issues"},
	{name: "num_closed_issues", kind: kindInteger, note: "Number of closed issues"},
	{name: "owner_id", kind: kindInteger, note: "Numeric ID of the owner"},
	{name: "repo_id", kind: kindInteger, note: "Numeric ID of the repository"},
	{name: "creator", kind: kindObject, sub: personMembers, note: "Creator; Gitea 28 and later"},
	{name: "creator_id", kind: kindInteger, note: "Numeric ID of the creator; Gitea 1.27"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, note: "Time of the last update"},
	{name: "closed_at", kind: kindText, note: "Time it was closed"},
}

var repositoryMetaMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the repository"},
	{name: "name", kind: kindText, note: "Repository name"},
	{name: "owner", kind: kindText, note: "Owner name"},
	{name: "full_name", kind: kindText, note: "Canonical OWNER/REPO"},
}

var pullRequestMetaMembers = []member{
	{name: "draft", kind: kindFlag, note: "True for a draft"},
	{name: "html_url", kind: kindText, note: "Address of the pull request page"},
	{name: "merged", kind: kindFlag, note: "True once merged"},
	{name: "merged_at", kind: kindText, note: "Merge time"},
}

// issueRefMembers are what a timeline event shows of another issue or pull request, which may lie in a
// repository outside the targets: identifiers only.
var issueRefMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID"},
	{name: "number", kind: kindInteger, note: "Index in its repository"},
	{name: "repository", kind: kindObject, sub: repositoryMetaMembers, note: "Repository it belongs to"},
}

var organizationIDMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID"},
	{name: "name", kind: kindText, note: "Organization name"},
}

var teamMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the team"},
	{name: "name", kind: kindText, note: "Team name, untrusted data"},
	{name: "description", kind: kindText, note: "Description, untrusted data"},
	{name: "organization", kind: kindObject, sub: organizationIDMembers, note: "Organization, identifiers only"},
	{name: "permission", kind: kindText, note: "none, read, write, admin, or owner"},
	{name: "includes_all_repositories", kind: kindFlag, note: "True when the team reaches every repository"},
	{name: "can_create_org_repo", kind: kindFlag, note: "True when the team may create repositories"},
	{name: "units", kind: kindTexts, note: "Units the team reaches; reported until Forgejo 17 and Gitea 28"},
	{name: "units_map", kind: kindFree, note: "Permission per unit"},
	{name: "visibility", kind: kindText, only: gitea, note: "public, limited, or private"},
}

var trackedTimeMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the entry"},
	{name: "created", kind: kindText, note: "Time it was logged"},
	{name: "time", kind: kindInteger, note: "Tracked seconds"},
	{name: "user_name", kind: kindText, note: "User name of the person who tracked it"},
	{name: "issue", kind: kindObject, sub: issueRefMembers, note: "Issue it belongs to, identifiers only"},
}

var commentRefMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the comment"},
}

const issueSensitivity = "-issue-data"

var issueMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the issue"},
	{name: "number", kind: kindInteger, note: "Index in the repository"},
	{name: "title", kind: kindText, note: "Title, untrusted data"},
	{name: "body", kind: kindText, max: maxIssueText, note: "Body in Markdown, untrusted data, at most 64 KiB"},
	{name: "state", kind: kindText, note: "open or closed"},
	{name: "is_locked", kind: kindFlag, note: "True while the conversation is locked"},
	{name: "ref", kind: kindText, note: "Reference such as a branch the issue concerns"},
	{name: "pin_order", kind: kindInteger, note: "Position among the pinned issues, 0 when not pinned"},
	{name: "comments", kind: kindInteger, note: "Number of comments"},
	{name: "user", kind: kindObject, sub: personMembers, note: "Author; personal data"},
	{name: "original_author", kind: kindText, note: "Author in the system a migration came from, untrusted data"},
	{name: "original_author_id", kind: kindInteger, note: "ID of that author in the original system"},
	{name: "assignee", kind: kindObject, sub: personMembers, note: "First assignee; personal data"},
	{name: "assignees", kind: kindObjects, sub: personMembers, note: "Assignees; personal data"},
	{name: "labels", kind: kindObjects, sub: labelMembers, note: "Labels"},
	{name: "milestone", kind: kindObject, sub: milestoneMembers, note: "Milestone"},
	{name: "projects", kind: kindObjects, sub: projectMembers, only: gitea, note: "Projects the issue belongs to"},
	{name: "assets", kind: kindObjects, sub: attachmentMembers, note: "Attachments"},
	{name: "pull_request", kind: kindObject, sub: pullRequestMetaMembers, note: "Present for a pull request, " +
		"which the issue tools never return"},
	{name: "repository", kind: kindObject, sub: repositoryMetaMembers, note: "Repository the issue belongs to"},
	{name: "content_version", kind: kindInteger, only: gitea, note: "Version of the title and body, to pass " +
		"as content_version when updating them"},
	{name: "time_estimate", kind: kindInteger, only: gitea, note: "Estimated time in seconds"},
	{name: "due_date", kind: kindText, note: "Due time"},
	{name: "html_url", kind: kindText, note: "Address of the issue page"},
	{name: "url", kind: kindText, note: "API address of the issue"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, note: "Time of the last update; Forgejo takes it as updated_at when " +
		"updating"},
	{name: "closed_at", kind: kindText, note: "Time it was closed"},
}

// changedMember is added to the answer of a close or reopen.
var changedMember = member{name: "changed", kind: kindFlag, note: "False when the issue already had the " +
	"requested state and nothing was sent"}

var issueDeadlineMembers = []member{
	{name: "number", kind: kindInteger, note: "Index of the issue"},
	{name: "due_date", kind: kindText, note: "Due time the instance reports, absent when there is none"},
}

var timelineMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the event"},
	{name: "type", kind: kindText, note: "Kind of the event"},
	{name: "user", kind: kindObject, sub: personMembers, note: "Person who caused the event; personal data"},
	{name: "body", kind: kindText, max: maxIssueText, note: "Text of the event, untrusted data, at most 64 KiB"},
	{name: "created_at", kind: kindText, note: "Time of the event"},
	{name: "updated_at", kind: kindText, note: "Time of the last change"},
	{name: "html_url", kind: kindText, note: "Address of the event on the issue page"},
	{name: "issue_url", kind: kindText, note: "API address of the issue"},
	{name: "pull_request_url", kind: kindText, note: "API address of the pull request, if it is one"},
	{name: "assignee", kind: kindObject, sub: personMembers, note: "Person assigned or unassigned; personal data"},
	{name: "assignee_team", kind: kindObject, sub: teamMembers, note: "Team assigned or unassigned"},
	{name: "removed_assignee", kind: kindFlag, note: "True when the assignee was removed, false when added"},
	{name: "label", kind: kindObject, sub: labelMembers, note: "Label added or removed"},
	{name: "milestone", kind: kindObject, sub: milestoneMembers, note: "Milestone set"},
	{name: "old_milestone", kind: kindObject, sub: milestoneMembers, note: "Milestone before the change"},
	{name: "old_project_id", kind: kindInteger, note: "Project before the change"},
	{name: "project_id", kind: kindInteger, note: "Project after the change"},
	{name: "old_title", kind: kindText, note: "Title before the change, untrusted data"},
	{name: "new_title", kind: kindText, note: "Title after the change, untrusted data"},
	{name: "old_ref", kind: kindText, note: "Reference before the change"},
	{name: "new_ref", kind: kindText, note: "Reference after the change"},
	{name: "ref_action", kind: kindText, note: "Kind of reference"},
	{name: "ref_commit_sha", kind: kindText, note: "Commit that referenced the issue"},
	{name: "ref_issue", kind: kindObject, sub: issueRefMembers, note: "Issue that referenced this one, " +
		"identifiers only"},
	{name: "ref_comment", kind: kindObject, sub: commentRefMembers, note: "Comment that referenced this issue, " +
		"identifiers only"},
	{name: "dependent_issue", kind: kindObject, sub: issueRefMembers, note: "Issue a dependency was added " +
		"or removed for, identifiers only"},
	{name: "review_id", kind: kindInteger, note: "Review the event belongs to"},
	{name: "resolve_doer", kind: kindObject, sub: personMembers, note: "Person who resolved a conversation; " +
		"personal data"},
	{name: "tracked_time", kind: kindObject, sub: trackedTimeMembers, note: "Tracked time of the event"},
}
