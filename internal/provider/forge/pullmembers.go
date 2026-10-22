package forge

// The pull request tools read the API's pull request, changed file, and commit objects with every member they
// document, except what a privacy or size reason leaves out (named at the member). The repositories a pull
// request names may lie outside the targets, such as the head repository of a fork: they keep only their
// identifiers.

const (
	pullSensitivity = "-pull-request-data"
	// maxPullBody bounds the body of a pull request and the message of a commit.
	maxPullBody = 64 << 10
)

// branchRepositoryMembers are what a pull request shows of the repository a branch lies in.
var branchRepositoryMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the repository"},
	{name: "name", kind: kindText, note: "Repository name"},
	{name: "full_name", kind: kindText, note: "Canonical OWNER/REPO"},
	{name: "owner", kind: kindObject, sub: accountIDMembers, note: "Owner, identifiers only"},
}

var branchInfoMembers = []member{
	{name: "label", kind: kindText, note: "Branch label, OWNER:BRANCH for a fork, untrusted data"},
	{name: "ref", kind: kindText, note: "Branch name, untrusted data"},
	{name: "sha", kind: kindText, note: "Commit the branch points to"},
	{name: "repo_id", kind: kindInteger, note: "Numeric ID of the repository of the branch"},
	{name: "repo", kind: kindObject, sub: branchRepositoryMembers, note: "Repository of the branch, identifiers " +
		"only; for the head of a fork it may lie outside the targets"},
}

var pullRequestMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the pull request"},
	{name: "number", kind: kindInteger, note: "Index in the repository"},
	{name: "title", kind: kindText, note: "Title, untrusted data"},
	{name: "body", kind: kindText, max: maxPullBody, note: "Body in Markdown, untrusted data, at most 64 KiB"},
	{name: "state", kind: kindText, note: "open or closed"},
	{name: "draft", kind: kindFlag, note: "True for a draft"},
	{name: "is_locked", kind: kindFlag, note: "True while the conversation is locked"},
	{name: "merged", kind: kindFlag, note: "True once merged"},
	{name: "merged_at", kind: kindText, note: "Merge time"},
	{name: "merged_by", kind: kindObject, sub: personMembers, note: "Person who merged it; personal data"},
	{name: "merge_commit_sha", kind: kindText, note: "Commit the merge created"},
	{name: "merge_base", kind: kindText, note: "Common ancestor of base and head"},
	{name: "mergeable", kind: kindFlag, note: "True when the instance sees no conflict"},
	{name: "allow_maintainer_edit", kind: kindFlag, note: "True when maintainers may push to the head branch"},
	{name: "base", kind: kindObject, sub: branchInfoMembers, note: "Branch the changes go into"},
	{name: "head", kind: kindObject, sub: branchInfoMembers, note: "Branch the changes come from"},
	{name: "additions", kind: kindInteger, note: "Added lines"},
	{name: "deletions", kind: kindInteger, note: "Removed lines"},
	{name: "changed_files", kind: kindInteger, note: "Number of changed files"},
	{name: "comments", kind: kindInteger, note: "Number of comments"},
	{name: "review_comments", kind: kindInteger, note: "Number of review comments"},
	{name: "pin_order", kind: kindInteger, note: "Position among the pinned pull requests, 0 when not pinned"},
	{name: "flow", kind: kindInteger, only: forgejo, note: "Numeric workflow of the pull request, as the " +
		"instance reports it"},
	{name: "content_version", kind: kindInteger, only: gitea, note: "Version of the title and body"},
	{name: "user", kind: kindObject, sub: personMembers, note: "Author; personal data"},
	{name: "assignee", kind: kindObject, sub: personMembers, note: "First assignee; personal data"},
	{name: "assignees", kind: kindObjects, sub: personMembers, note: "Assignees; personal data"},
	{name: "requested_reviewers", kind: kindObjects, sub: personMembers, note: "Reviewers asked for a review; " +
		"personal data"},
	{name: "requested_reviewers_teams", kind: kindObjects, sub: teamMembers, note: "Teams asked for a review"},
	{name: "labels", kind: kindObjects, sub: labelMembers, note: "Labels"},
	{name: "milestone", kind: kindObject, sub: milestoneMembers, note: "Milestone"},
	{name: "due_date", kind: kindText, note: "Due time"},
	{name: "html_url", kind: kindText, note: "Address of the pull request page"},
	{name: "diff_url", kind: kindText, note: "Address of the diff"},
	{name: "patch_url", kind: kindText, note: "Address of the patch"},
	{name: "url", kind: kindText, note: "API address of the pull request"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, note: "Time of the last update"},
	{name: "closed_at", kind: kindText, note: "Time it was closed"},
}

var changedFileMembers = []member{
	{name: "filename", kind: kindText, note: "Path of the file, untrusted data"},
	{name: "previous_filename", kind: kindText, note: "Path before a rename, untrusted data"},
	{name: "status", kind: kindText, note: "added, modified, deleted, renamed, ..."},
	{name: "additions", kind: kindInteger, note: "Added lines"},
	{name: "deletions", kind: kindInteger, note: "Removed lines"},
	{name: "changes", kind: kindInteger, note: "Added plus removed lines"},
	{name: "html_url", kind: kindText, note: "Address of the file page"},
	{name: "contents_url", kind: kindText, note: "API address of the file contents"},
	{name: "raw_url", kind: kindText, note: "Address of the raw file"},
}

var commitMetaMembers = []member{
	{name: "sha", kind: kindText, note: "Commit or tree ID"},
	{name: "created", kind: kindText, note: "Time"},
	{name: "url", kind: kindText, note: "API address"},
}

var commitUserMembers = []member{
	{name: "name", kind: kindText, note: "Name in the commit, untrusted data; personal data"},
	{name: "email", kind: kindText, note: "Email address in the commit; personal data"},
	{name: "date", kind: kindText, note: "Time in the commit"},
}

var payloadUserMembers = []member{
	{name: "username", kind: kindText, note: "User name of the signer"},
	{name: "full_name", kind: kindText, note: "Display name of the signer; personal data"},
	{name: "email", kind: kindText, note: "Email address of the signer; personal data"},
}

// The raw payload and signature of a verification are left out: they are large signed blobs that repeat the
// committer's identity; verified, reason, and signer say what a reader needs.
var verificationMembers = []member{
	{name: "verified", kind: kindFlag, note: "True when the signature is verified"},
	{name: "reason", kind: kindText, note: "Why it is or is not verified"},
	{name: "signer", kind: kindObject, sub: payloadUserMembers, note: "Signer; personal data"},
}

var repoCommitMembers = []member{
	{name: "message", kind: kindText, max: maxPullBody, note: "Commit message, untrusted data, at most 64 KiB"},
	{name: "author", kind: kindObject, sub: commitUserMembers, note: "Author as the commit names it; personal data"},
	{name: "committer", kind: kindObject, sub: commitUserMembers, note: "Committer as the commit names it; " +
		"personal data"},
	{name: "tree", kind: kindObject, sub: commitMetaMembers, note: "Tree of the commit"},
	{name: "verification", kind: kindObject, sub: verificationMembers, note: "Signature verification"},
	{name: "url", kind: kindText, note: "API address of the commit"},
}

var commitStatsMembers = []member{
	{name: "additions", kind: kindInteger, note: "Added lines"},
	{name: "deletions", kind: kindInteger, note: "Removed lines"},
	{name: "total", kind: kindInteger, note: "Added plus removed lines"},
}

var affectedFileMembers = []member{
	{name: "filename", kind: kindText, note: "Path of the file, untrusted data"},
	{name: "status", kind: kindText, note: "added, modified, deleted, ..."},
}

var commitMembers = []member{
	{name: "sha", kind: kindText, note: "Commit ID"},
	{name: "commit", kind: kindObject, sub: repoCommitMembers, note: "Git data of the commit"},
	{name: "author", kind: kindObject, sub: personMembers, note: "Account of the author; personal data"},
	{name: "committer", kind: kindObject, sub: personMembers, note: "Account of the committer; personal data"},
	{name: "parents", kind: kindObjects, sub: commitMetaMembers, note: "Parent commits"},
	{name: "stats", kind: kindObject, sub: commitStatsMembers, note: "Changed line counts"},
	{name: "files", kind: kindObjects, sub: affectedFileMembers, note: "Files the commit changed"},
	{name: "created", kind: kindText, note: "Time of the commit"},
	{name: "html_url", kind: kindText, note: "Address of the commit page"},
	{name: "url", kind: kindText, note: "API address of the commit"},
}
