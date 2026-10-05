package github

import (
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// toolGroups are the subject areas GitHub's tools are sorted into for display.
var toolGroups = []config.ToolGroup{
	{ID: "issues", Title: "Issues", Description: "Issues, comments, reactions, labels, milestones, types, fields, dependencies, sub-issues, and Copilot assignments"},
	{ID: "pullrequests", Title: "Pull requests", Description: "Pull requests with their files, commits, checks, reviews, review threads, reviewers, and Copilot reviews"},
	{ID: "projects", Title: "Projects", Description: "Projects with their items, drafts, fields, views, status updates, iterations, templates, and access"},
	{ID: "actions", Title: "Actions", Description: "Workflows, runs, jobs, logs, artifacts, and workflow and Actions permissions"},
	{ID: "code", Title: "Code", Description: "Repository contents, files, trees, branches, tags, commits, and blame"},
	{ID: "repositories", Title: "Repositories", Description: "Repositories, their custom properties, merge settings, rulesets, collaborators, subscriptions, and stars"},
	{ID: "releases", Title: "Releases", Description: "Releases and their assets"},
	{ID: "security", Title: "Security", Description: "Code scanning, Dependabot, and secret scanning alerts, code quality findings, and security advisories"},
	{ID: "discussions", Title: "Discussions", Description: "Discussions, their categories, and their comments"},
	{ID: "gists", Title: "Gists", Description: "Gists and their files"},
	{ID: "notifications", Title: "Notifications", Description: "Notification threads and thread subscriptions"},
	{ID: "accounts", Title: "Accounts", Description: "Users, organizations, teams, team members, and the authenticated account"},
}

// groupBySegment maps the resource segment of a tool ID (github.<segment>.<action>) to its group. Segments
// starting with "project" or "pullrequest" are grouped by prefix, see groupOf.
var groupBySegment = map[string]string{
	"issues": "issues", "comments": "issues", "reactions": "issues", "labels": "issues",
	"milestones": "issues", "issuetypes": "issues", "issuefields": "issues", "issuedependencies": "issues",
	"subissues": "issues", "copilotassignments": "issues",
	"copilotreviews": "pullrequests",
	"workflows":      "actions", "workflowruns": "actions", "workflowjobs": "actions", "workflowrunlogs": "actions",
	"workflowartifacts": "actions", "workflowfiles": "actions", "workflowpermissions": "actions",
	"actionspermissions": "actions",
	"contents":           "code", "files": "code", "branches": "code", "commits": "code", "trees": "code",
	"blame": "code", "code": "code", "tags": "code",
	"repositories": "repositories", "customproperties": "repositories", "rulesets": "repositories",
	"collaborators": "repositories", "repositorysettings": "repositories", "repositorysubscriptions": "repositories", "stars": "repositories",
	"releases": "releases", "releaseassets": "releases",
	"codescanningalerts": "security", "dependabotalerts": "security", "secretscanningalerts": "security",
	"codequalityfindings": "security", "globaladvisories": "security", "organizationadvisories": "security",
	"repositoryadvisories": "security",
	"discussions":          "discussions", "discussioncategories": "discussions", "discussioncomments": "discussions",
	"gists":         "gists",
	"notifications": "notifications", "threadsubscriptions": "notifications",
	"accounts": "accounts", "users": "accounts", "organizations": "accounts", "teams": "accounts",
	"teammembers": "accounts",
}

// groupOf returns the group of a GitHub tool ID, or "" for an ID outside every group.
func groupOf(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 3 {
		return ""
	}
	segment := parts[1]
	switch {
	case strings.HasPrefix(segment, "pullrequest"):
		return "pullrequests"
	case strings.HasPrefix(segment, "project"):
		return "projects"
	}
	return groupBySegment[segment]
}

func withGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = groupOf(d.ID)
	return d
}
