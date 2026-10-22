package github

import (
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The pull request projection covers the pull-request-simple object GitHub embeds in other answers, with the
// team and the label and milestone views it holds. The repositories of head and base are shown in full only
// when the connection's targets admit them; a fork outside the targets keeps its identifiers.

var restTeamLinkKeys = []string{"url", "members_url", "repositories_url"}

var restPullSimpleLinkKeys = []string{"url", "diff_url", "patch_url", "issue_url", "commits_url",
	"review_comments_url", "review_comment_url", "comments_url", "statuses_url"}

// restPullRelations are the members of the _links object of a pull request.
var restPullRelations = []string{"comments", "commits", "statuses", "html", "issue", "review_comments",
	"review_comment", "self"}

type teamRaw struct {
	ID                  *int64   `json:"id"`
	NodeID              *string  `json:"node_id"`
	Name                *string  `json:"name"`
	Slug                *string  `json:"slug"`
	Description         *string  `json:"description"`
	Privacy             *string  `json:"privacy"`
	NotificationSetting *string  `json:"notification_setting"`
	Permission          *string  `json:"permission"`
	LDAPDN              *string  `json:"ldap_dn"`
	Type                *string  `json:"type"`
	AccessSource        *string  `json:"access_source"`
	OrganizationID      *int64   `json:"organization_id"`
	EnterpriseID        *int64   `json:"enterprise_id"`
	HTMLURL             *string  `json:"html_url"`
	Parent              *teamRaw `json:"parent"`
	Permissions         *struct {
		Pull     *bool `json:"pull"`
		Triage   *bool `json:"triage"`
		Push     *bool `json:"push"`
		Maintain *bool `json:"maintain"`
		Admin    *bool `json:"admin"`
	} `json:"permissions"`
	links Links
}

func (r *teamRaw) UnmarshalJSON(data []byte) (err error) {
	type plain teamRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restTeamLinkKeys)
	return err
}

// RESTTeamSimple is the view of a team as the parent of another team.
type RESTTeamSimple struct {
	ID                  *int64  `json:"id,omitempty"`
	NodeID              *string `json:"node_id,omitempty"`
	Name                *string `json:"name,omitempty"`
	Slug                *string `json:"slug,omitempty"`
	Description         *string `json:"description,omitempty"`
	Privacy             *string `json:"privacy,omitempty"`
	NotificationSetting *string `json:"notification_setting,omitempty"`
	Permission          *string `json:"permission,omitempty"`
	LDAPDN              *string `json:"ldap_dn,omitempty"`
	Type                *string `json:"type,omitempty"`
	OrganizationID      *int64  `json:"organization_id,omitempty"`
	EnterpriseID        *int64  `json:"enterprise_id,omitempty"`
	URL                 *string `json:"url,omitempty"`
	Links               Links   `json:"links,omitempty"`
}

// RESTTeamPermissions is what a team may do in a repository.
type RESTTeamPermissions struct {
	Pull     *bool `json:"pull,omitempty"`
	Triage   *bool `json:"triage,omitempty"`
	Push     *bool `json:"push,omitempty"`
	Maintain *bool `json:"maintain,omitempty"`
	Admin    *bool `json:"admin,omitempty"`
}

// RESTTeam is the view of a team.
type RESTTeam struct {
	RESTTeamSimple
	AccessSource *string              `json:"access_source,omitempty"`
	Permissions  *RESTTeamPermissions `json:"permissions,omitempty"`
	Parent       *RESTTeamSimple      `json:"parent,omitempty"`
}

func (r *teamRaw) simple() *RESTTeamSimple {
	if r == nil {
		return nil
	}
	return &RESTTeamSimple{ID: r.ID, NodeID: nameText(r.NodeID), Name: nameText(r.Name), Slug: nameText(r.Slug),
		Description: longText(r.Description), Privacy: nameText(r.Privacy),
		NotificationSetting: nameText(r.NotificationSetting), Permission: nameText(r.Permission),
		LDAPDN: nameText(r.LDAPDN), Type: nameText(r.Type), OrganizationID: r.OrganizationID,
		EnterpriseID: r.EnterpriseID, URL: address(r.HTMLURL), Links: r.links}
}

func (r *teamRaw) project() *RESTTeam {
	if r == nil {
		return nil
	}
	team := &RESTTeam{RESTTeamSimple: *r.simple(), AccessSource: nameText(r.AccessSource),
		Parent: r.Parent.simple()}
	if p := r.Permissions; p != nil {
		team.Permissions = &RESTTeamPermissions{Pull: p.Pull, Triage: p.Triage, Push: p.Push,
			Maintain: p.Maintain, Admin: p.Admin}
	}
	return team
}

const restTeamSimpleProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"name":{"type":"string"},` +
	`"slug":{"type":"string"},"description":{"type":"string"},"privacy":{"type":"string"},` +
	`"notification_setting":{"type":"string"},"permission":{"type":"string"},"ldap_dn":{"type":"string"},` +
	`"type":{"type":"string"},"organization_id":{"type":"integer"},"enterprise_id":{"type":"integer"},` +
	`"url":{"type":"string"}`

var restTeamSimpleSchema = objectSchema(restTeamSimpleProperties + `,"links":` + linksSchema(restTeamLinkKeys))

var restTeamSchema = objectSchema(restTeamSimpleProperties + `,"links":` + linksSchema(restTeamLinkKeys) +
	`,"access_source":{"type":"string"},"permissions":` + objectSchema(`"pull":{"type":"boolean"},`+
	`"triage":{"type":"boolean"},"push":{"type":"boolean"},"maintain":{"type":"boolean"},`+
	`"admin":{"type":"boolean"}`) + `,"parent":` + restTeamSimpleSchema)

type pullRefRaw struct {
	Label *string        `json:"label"`
	Ref   *string        `json:"ref"`
	SHA   *string        `json:"sha"`
	User  *accountRaw    `json:"user"`
	Repo  *repositoryRaw `json:"repo"`
}

// RESTPullRef is one side of a pull request: head or base.
type RESTPullRef struct {
	Label *string         `json:"label,omitempty"`
	Ref   *string         `json:"ref,omitempty"`
	SHA   *string         `json:"sha,omitempty"`
	User  *RESTAccount    `json:"user,omitempty"`
	Repo  *RESTRepository `json:"repo,omitempty"`
}

func (r *pullRefRaw) project(allowed allowlist) *RESTPullRef {
	if r == nil {
		return nil
	}
	return &RESTPullRef{Label: nameText(r.Label), Ref: nameText(r.Ref), SHA: nameText(r.SHA),
		User: r.User.project(), Repo: r.Repo.project(allowed)}
}

var restPullRefSchema = objectSchema(`"label":{"type":"string"},"ref":{"type":"string"},"sha":{"type":"string"},` +
	`"user":` + restAccountSchema + `,"repo":` + restRepositorySchema)

type pullSimpleRaw struct {
	ID                 *int64        `json:"id"`
	NodeID             *string       `json:"node_id"`
	HTMLURL            *string       `json:"html_url"`
	Number             *int64        `json:"number"`
	State              *string       `json:"state"`
	Locked             *bool         `json:"locked"`
	Title              *string       `json:"title"`
	User               *accountRaw   `json:"user"`
	Body               *string       `json:"body"`
	Labels             []labelRaw    `json:"labels"`
	Milestone          *milestoneRaw `json:"milestone"`
	ActiveLockReason   *string       `json:"active_lock_reason"`
	CreatedAt          *string       `json:"created_at"`
	UpdatedAt          *string       `json:"updated_at"`
	ClosedAt           *string       `json:"closed_at"`
	MergedAt           *string       `json:"merged_at"`
	MergeCommitSHA     *string       `json:"merge_commit_sha"`
	Assignee           *accountRaw   `json:"assignee"`
	Assignees          []accountRaw  `json:"assignees"`
	RequestedReviewers []accountRaw  `json:"requested_reviewers"`
	RequestedTeams     []teamRaw     `json:"requested_teams"`
	Head               *pullRefRaw   `json:"head"`
	Base               *pullRefRaw   `json:"base"`
	RelLinks           map[string]*struct {
		Href *string `json:"href"`
	} `json:"_links"`
	AuthorAssociation *string `json:"author_association"`
	AutoMerge         *struct {
		EnabledBy     *accountRaw `json:"enabled_by"`
		MergeMethod   *string     `json:"merge_method"`
		CommitTitle   *string     `json:"commit_title"`
		CommitMessage *string     `json:"commit_message"`
	} `json:"auto_merge"`
	Stack *struct {
		Base *struct {
			Ref *string `json:"ref"`
			SHA *string `json:"sha"`
		} `json:"base"`
		Size     *int64 `json:"size"`
		Position *int64 `json:"position"`
		ID       *int64 `json:"id"`
		Number   *int64 `json:"number"`
	} `json:"stack"`
	Draft *bool `json:"draft"`
	links Links
}

func (r *pullSimpleRaw) UnmarshalJSON(data []byte) (err error) {
	type plain pullSimpleRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restPullSimpleLinkKeys)
	return err
}

// RESTPullAutoMerge is the auto-merge setting of a pull request.
type RESTPullAutoMerge struct {
	EnabledBy        *RESTAccount `json:"enabled_by,omitempty"`
	MergeMethod      *string      `json:"merge_method,omitempty"`
	CommitTitle      *string      `json:"commit_title,omitempty"`
	CommitMessage    *string      `json:"commit_message,omitempty"`
	CommitMessageCut bool         `json:"commit_message_truncated,omitempty"`
}

// RESTPullStackBase is the base of the stack a pull request belongs to.
type RESTPullStackBase struct {
	Ref *string `json:"ref,omitempty"`
	SHA *string `json:"sha,omitempty"`
}

// RESTPullStack is the stack a pull request belongs to.
type RESTPullStack struct {
	Base     *RESTPullStackBase `json:"base,omitempty"`
	Size     *int64             `json:"size,omitempty"`
	Position *int64             `json:"position,omitempty"`
	ID       *int64             `json:"id,omitempty"`
	Number   *int64             `json:"number,omitempty"`
}

// RESTPullSimple is the view of a pull request as GitHub embeds it in other answers. URL is the web address;
// the REST url and the other *_url addresses sit in Links, and the hyperlinks of _links in LinkRelations.
type RESTPullSimple struct {
	ID                      *int64             `json:"id,omitempty"`
	NodeID                  *string            `json:"node_id,omitempty"`
	Number                  *int64             `json:"number,omitempty"`
	State                   *string            `json:"state,omitempty"`
	Locked                  *bool              `json:"locked,omitempty"`
	Title                   *string            `json:"title,omitempty"`
	User                    *RESTAccount       `json:"user,omitempty"`
	Body                    *string            `json:"body,omitempty"`
	BodyTruncated           bool               `json:"body_truncated,omitempty"`
	Labels                  []Label            `json:"labels,omitempty"`
	LabelsTruncated         bool               `json:"labels_truncated,omitempty"`
	Milestone               *Milestone         `json:"milestone,omitempty"`
	ActiveLockReason        *string            `json:"active_lock_reason,omitempty"`
	CreatedAt               *string            `json:"created_at,omitempty"`
	UpdatedAt               *string            `json:"updated_at,omitempty"`
	ClosedAt                *string            `json:"closed_at,omitempty"`
	MergedAt                *string            `json:"merged_at,omitempty"`
	MergeCommitSHA          *string            `json:"merge_commit_sha,omitempty"`
	Assignee                *RESTAccount       `json:"assignee,omitempty"`
	Assignees               []RESTAccount      `json:"assignees,omitempty"`
	AssigneesTruncated      bool               `json:"assignees_truncated,omitempty"`
	RequestedReviewers      []RESTAccount      `json:"requested_reviewers,omitempty"`
	RequestedReviewersCut   bool               `json:"requested_reviewers_truncated,omitempty"`
	RequestedTeams          []RESTTeam         `json:"requested_teams,omitempty"`
	RequestedTeamsTruncated bool               `json:"requested_teams_truncated,omitempty"`
	Head                    *RESTPullRef       `json:"head,omitempty"`
	Base                    *RESTPullRef       `json:"base,omitempty"`
	LinkRelations           map[string]string  `json:"link_relations,omitempty"`
	AuthorAssociation       *string            `json:"author_association,omitempty"`
	AutoMerge               *RESTPullAutoMerge `json:"auto_merge,omitempty"`
	Stack                   *RESTPullStack     `json:"stack,omitempty"`
	Draft                   *bool              `json:"draft,omitempty"`
	URL                     *string            `json:"url,omitempty"`
	Links                   Links              `json:"links,omitempty"`
}

// accounts projects a list of accounts, capped.
func accounts(raw []accountRaw) ([]RESTAccount, bool) {
	raw, cut := capped(raw)
	var out []RESTAccount
	for i := range raw {
		out = append(out, *raw[i].project())
	}
	return out, cut
}

func (r *pullSimpleRaw) project(allowed allowlist) *RESTPullSimple {
	body, bodyCut := cleanText(r.Body, projectionBodyLimit, true)
	pull := &RESTPullSimple{ID: r.ID, NodeID: nameText(r.NodeID), Number: r.Number, State: nameText(r.State),
		Locked: r.Locked, Title: longText(r.Title), User: r.User.project(), Body: body, BodyTruncated: bodyCut,
		ActiveLockReason: nameText(r.ActiveLockReason), CreatedAt: nameText(r.CreatedAt),
		UpdatedAt: nameText(r.UpdatedAt), ClosedAt: nameText(r.ClosedAt), MergedAt: nameText(r.MergedAt),
		MergeCommitSHA: nameText(r.MergeCommitSHA), Assignee: r.Assignee.project(),
		Head: r.Head.project(allowed), Base: r.Base.project(allowed), AuthorAssociation: nameText(r.AuthorAssociation),
		Draft: r.Draft, URL: address(r.HTMLURL), Links: r.links}
	pull.Assignees, pull.AssigneesTruncated = accounts(r.Assignees)
	pull.RequestedReviewers, pull.RequestedReviewersCut = accounts(r.RequestedReviewers)
	labels, cut := capped(r.Labels)
	pull.LabelsTruncated = cut
	for i := range labels {
		label := labels[i].project()
		label.Name = *nameText(&label.Name)
		label.Description = *longText(&label.Description)
		pull.Labels = append(pull.Labels, label)
	}
	if m := r.Milestone; m != nil {
		milestone := m.project()
		milestone.Title = *longText(&milestone.Title)
		milestone.Description = *longText(&milestone.Description)
		pull.Milestone = &milestone
	}
	teams, cut := capped(r.RequestedTeams)
	pull.RequestedTeamsTruncated = cut
	for i := range teams {
		pull.RequestedTeams = append(pull.RequestedTeams, *teams[i].project())
	}
	for _, name := range restPullRelations {
		if rel := r.RelLinks[name]; rel != nil {
			if href := address(rel.Href); href != nil {
				if pull.LinkRelations == nil {
					pull.LinkRelations = map[string]string{}
				}
				pull.LinkRelations[name] = *href
			}
		}
	}
	if a := r.AutoMerge; a != nil {
		message, cut := cleanText(a.CommitMessage, projectionBodyLimit, true)
		pull.AutoMerge = &RESTPullAutoMerge{EnabledBy: a.EnabledBy.project(), MergeMethod: nameText(a.MergeMethod),
			CommitTitle: longText(a.CommitTitle), CommitMessage: message, CommitMessageCut: cut}
	}
	if s := r.Stack; s != nil {
		pull.Stack = &RESTPullStack{Size: s.Size, Position: s.Position, ID: s.ID, Number: s.Number}
		if b := s.Base; b != nil {
			pull.Stack.Base = &RESTPullStackBase{Ref: nameText(b.Ref), SHA: nameText(b.SHA)}
		}
	}
	return pull
}

var restPullSimpleProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"number":{"type":"integer"},` +
	`"state":{"type":"string"},"locked":{"type":"boolean"},"title":{"type":"string"},"user":` + restAccountSchema +
	`,"body":{"type":"string"},"body_truncated":{"type":"boolean"},"labels":{"type":"array","maxItems":100,` +
	`"items":` + labelOutput + `},"labels_truncated":{"type":"boolean"},"milestone":` +
	objectSchema(milestoneProperties) + `,"active_lock_reason":{"type":"string"},"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"closed_at":{"type":"string"},"merged_at":{"type":"string"},` +
	`"merge_commit_sha":{"type":"string"},"assignee":` + restAccountSchema + `,` +
	`"assignees":{"type":"array","maxItems":100,"items":` + restAccountSchema + `},"assignees_truncated":{"type":"boolean"},` +
	`"requested_reviewers":{"type":"array","maxItems":100,"items":` + restAccountSchema + `},` +
	`"requested_reviewers_truncated":{"type":"boolean"},` +
	`"requested_teams":{"type":"array","maxItems":100,"items":` + restTeamSchema + `},` +
	`"requested_teams_truncated":{"type":"boolean"},"head":` + restPullRefSchema + `,"base":` + restPullRefSchema + `,` +
	`"link_relations":{"type":"object","maxProperties":8,"additionalProperties":{"type":"string"}},` +
	`"author_association":{"type":"string"},"auto_merge":` + objectSchema(`"enabled_by":`+restAccountSchema+`,`+
	`"merge_method":{"type":"string"},"commit_title":{"type":"string"},"commit_message":{"type":"string"},`+
	`"commit_message_truncated":{"type":"boolean"}`) + `,"stack":` + objectSchema(`"base":`+
	objectSchema(`"ref":{"type":"string"},"sha":{"type":"string"}`)+`,"size":{"type":"integer"},`+
	`"position":{"type":"integer"},"id":{"type":"integer"},"number":{"type":"integer"}`) +
	`,"draft":{"type":"boolean"},"url":{"type":"string"},"links":` + linksSchema(restPullSimpleLinkKeys)

var restPullSimpleSchema = objectSchema(restPullSimpleProperties)

var restPullSimpleFields = []capability.Field{
	{Name: "id", Description: "Numeric pull request id"},
	{Name: "node_id", Description: "GraphQL node id of the pull request"},
	{Name: "number", Description: "Number of the pull request in its repository"},
	{Name: "state", Description: "open or closed"},
	{Name: "locked", Description: "True when the conversation is locked"},
	{Name: "title", Description: "Title, untrusted data"},
	{Name: "user", Description: "Author as an account: " + restAccountMembers},
	{Name: "body", Description: "Description, cut at 4000 characters (body_truncated says so), untrusted data"},
	{Name: "body_truncated", Description: "True when the body was cut"},
	{Name: "labels", Description: "Labels, at most 100 (labels_truncated says so), untrusted data"},
	{Name: "labels_truncated", Description: "True when the pull request holds more labels than shown"},
	{Name: "milestone", Description: "Milestone with number, title, state, counts, creator, times, url, and links"},
	{Name: "active_lock_reason", Description: "Reason the conversation is locked"},
	{Name: "created_at", Description: "Creation time"},
	{Name: "updated_at", Description: "Time of the last change"},
	{Name: "closed_at", Description: "Time the pull request was closed"},
	{Name: "merged_at", Description: "Time the pull request was merged"},
	{Name: "merge_commit_sha", Description: "Commit of the merge"},
	{Name: "assignee", Description: "First assignee as an account"},
	{Name: "assignees", Description: "Assignees as accounts, at most 100"},
	{Name: "assignees_truncated", Description: "True when the pull request holds more assignees than shown"},
	{Name: "requested_reviewers", Description: "Requested reviewers as accounts, at most 100"},
	{Name: "requested_reviewers_truncated", Description: "True when more reviewers were requested than shown"},
	{Name: "requested_teams", Description: "Requested review teams, at most 100, untrusted data"},
	{Name: "requested_teams_truncated", Description: "True when more teams were requested than shown"},
	{Name: "head", Description: "Head with label, ref, sha, user, and repo; a repository outside the connection's targets keeps only id, node_id, and full_name"},
	{Name: "base", Description: "Base with label, ref, sha, user, and repo, shown like head"},
	{Name: "link_relations", Description: "Hyperlinks of the _links object by their GitHub names, untrusted data, never fetched"},
	{Name: "author_association", Description: "How the author relates to the repository, such as OWNER or MEMBER"},
	{Name: "auto_merge", Description: "enabled_by (an account), merge_method, commit_title, and commit_message of the auto-merge setting"},
	{Name: "stack", Description: "base (ref and sha), size, position, id, and number of the stack the pull request belongs to"},
	{Name: "draft", Description: "True for a draft pull request"},
	{Name: "url", Description: "Web address of the pull request, untrusted data, never fetched"},
	{Name: "links", Description: "REST url and the other *_url addresses by their GitHub names, untrusted data, never fetched"},
}
