package github

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The repository projection covers the REST repository and minimal-repository objects. temp_clone_token
// grants clone access without sign-in and has no place in the raw struct, so it can never be emitted.

// Bounds of the free-form custom_properties object. Beyond any of them it is left out and marked, never cut.
const (
	customPropertiesDepth   = 3
	customPropertiesNodes   = 500
	customPropertiesText    = 1000
	customPropertiesKeySize = 200
)

var restRepositoryLinkKeys = []string{"url", "archive_url", "assignees_url", "blobs_url", "branches_url",
	"collaborators_url", "comments_url", "commits_url", "compare_url", "contents_url", "contributors_url",
	"deployments_url", "downloads_url", "events_url", "forks_url", "git_commits_url", "git_refs_url",
	"git_tags_url", "git_url", "issue_comment_url", "issue_events_url", "issues_url", "keys_url", "labels_url",
	"languages_url", "merges_url", "milestones_url", "notifications_url", "pulls_url", "releases_url", "ssh_url",
	"stargazers_url", "statuses_url", "subscribers_url", "subscription_url", "tags_url", "teams_url", "trees_url",
	"clone_url", "mirror_url", "hooks_url", "svn_url"}

var licenseLinkKeys = []string{"url"}
var codeOfConductLinkKeys = []string{"url"}

type licenseRaw struct {
	Key     *string `json:"key"`
	Name    *string `json:"name"`
	SPDXID  *string `json:"spdx_id"`
	NodeID  *string `json:"node_id"`
	HTMLURL *string `json:"html_url"`
	links   Links
}

func (r *licenseRaw) UnmarshalJSON(data []byte) (err error) {
	type plain licenseRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, licenseLinkKeys)
	return err
}

type codeOfConductRaw struct {
	Key     *string `json:"key"`
	Name    *string `json:"name"`
	Body    *string `json:"body"`
	HTMLURL *string `json:"html_url"`
	links   Links
}

func (r *codeOfConductRaw) UnmarshalJSON(data []byte) (err error) {
	type plain codeOfConductRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, codeOfConductLinkKeys)
	return err
}

type featureRaw struct {
	Status *string `json:"status"`
}

type securityRaw struct {
	AdvancedSecurity                      *featureRaw `json:"advanced_security"`
	CodeSecurity                          *featureRaw `json:"code_security"`
	DependabotSecurityUpdates             *featureRaw `json:"dependabot_security_updates"`
	SecretScanning                        *featureRaw `json:"secret_scanning"`
	SecretScanningPushProtection          *featureRaw `json:"secret_scanning_push_protection"`
	SecretScanningNonProviderPatterns     *featureRaw `json:"secret_scanning_non_provider_patterns"`
	SecretScanningAIDetection             *featureRaw `json:"secret_scanning_ai_detection"`
	SecretScanningDelegatedAlertDismissal *featureRaw `json:"secret_scanning_delegated_alert_dismissal"`
	SecretScanningDelegatedBypass         *featureRaw `json:"secret_scanning_delegated_bypass"`
	SecretScanningDelegatedBypassOptions  *struct {
		Reviewers []struct {
			ReviewerID   *int64  `json:"reviewer_id"`
			ReviewerType *string `json:"reviewer_type"`
			Mode         *string `json:"mode"`
		} `json:"reviewers"`
	} `json:"secret_scanning_delegated_bypass_options"`
}

type repositoryRaw struct {
	ID                        *int64                     `json:"id"`
	NodeID                    *string                    `json:"node_id"`
	Name                      *string                    `json:"name"`
	FullName                  *string                    `json:"full_name"`
	Owner                     *accountRaw                `json:"owner"`
	Private                   *bool                      `json:"private"`
	HTMLURL                   *string                    `json:"html_url"`
	Description               *string                    `json:"description"`
	Fork                      *bool                      `json:"fork"`
	Homepage                  *string                    `json:"homepage"`
	Language                  *string                    `json:"language"`
	ForksCount                *int64                     `json:"forks_count"`
	StargazersCount           *int64                     `json:"stargazers_count"`
	WatchersCount             *int64                     `json:"watchers_count"`
	Size                      *int64                     `json:"size"`
	DefaultBranch             *string                    `json:"default_branch"`
	OpenIssuesCount           *int64                     `json:"open_issues_count"`
	IsTemplate                *bool                      `json:"is_template"`
	Topics                    []*string                  `json:"topics"`
	HasIssues                 *bool                      `json:"has_issues"`
	HasProjects               *bool                      `json:"has_projects"`
	HasWiki                   *bool                      `json:"has_wiki"`
	HasPages                  *bool                      `json:"has_pages"`
	HasDiscussions            *bool                      `json:"has_discussions"`
	HasPullRequests           *bool                      `json:"has_pull_requests"`
	PullRequestCreationPolicy *string                    `json:"pull_request_creation_policy"`
	Archived                  *bool                      `json:"archived"`
	Disabled                  *bool                      `json:"disabled"`
	Visibility                *string                    `json:"visibility"`
	PushedAt                  *string                    `json:"pushed_at"`
	CreatedAt                 *string                    `json:"created_at"`
	UpdatedAt                 *string                    `json:"updated_at"`
	Permissions               *RESTPermissions           `json:"permissions"`
	RoleName                  *string                    `json:"role_name"`
	DeleteBranchOnMerge       *bool                      `json:"delete_branch_on_merge"`
	SubscribersCount          *int64                     `json:"subscribers_count"`
	NetworkCount              *int64                     `json:"network_count"`
	CodeOfConduct             *codeOfConductRaw          `json:"code_of_conduct"`
	License                   *licenseRaw                `json:"license"`
	Forks                     *int64                     `json:"forks"`
	OpenIssues                *int64                     `json:"open_issues"`
	Watchers                  *int64                     `json:"watchers"`
	AllowForking              *bool                      `json:"allow_forking"`
	WebCommitSignoffRequired  *bool                      `json:"web_commit_signoff_required"`
	SecurityAndAnalysis       *securityRaw               `json:"security_and_analysis"`
	CustomProperties          json.RawMessage            `json:"custom_properties"`
	AllowRebaseMerge          *bool                      `json:"allow_rebase_merge"`
	AllowSquashMerge          *bool                      `json:"allow_squash_merge"`
	AllowAutoMerge            *bool                      `json:"allow_auto_merge"`
	AllowUpdateBranch         *bool                      `json:"allow_update_branch"`
	SquashMergeCommitTitle    *string                    `json:"squash_merge_commit_title"`
	SquashMergeCommitMessage  *string                    `json:"squash_merge_commit_message"`
	MergeCommitTitle          *string                    `json:"merge_commit_title"`
	MergeCommitMessage        *string                    `json:"merge_commit_message"`
	AllowMergeCommit          *bool                      `json:"allow_merge_commit"`
	MasterBranch              *string                    `json:"master_branch"`
	StarredAt                 *string                    `json:"starred_at"`
	AnonymousAccessEnabled    *bool                      `json:"anonymous_access_enabled"`
	CodeSearchIndexStatus     *RESTCodeSearchIndexStatus `json:"code_search_index_status"`
	links                     Links
}

func (r *repositoryRaw) UnmarshalJSON(data []byte) (err error) {
	type plain repositoryRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restRepositoryLinkKeys)
	return err
}

// RESTPermissions is what the token may do in a repository.
type RESTPermissions struct {
	Admin    *bool `json:"admin,omitempty"`
	Maintain *bool `json:"maintain,omitempty"`
	Push     *bool `json:"push,omitempty"`
	Triage   *bool `json:"triage,omitempty"`
	Pull     *bool `json:"pull,omitempty"`
}

// RESTCodeSearchIndexStatus is the state of the code search index of a repository.
type RESTCodeSearchIndexStatus struct {
	LexicalSearchOK  *bool   `json:"lexical_search_ok,omitempty"`
	LexicalCommitSHA *string `json:"lexical_commit_sha,omitempty"`
}

// RESTLicense is the license of a repository.
type RESTLicense struct {
	Key    *string `json:"key,omitempty"`
	Name   *string `json:"name,omitempty"`
	SPDXID *string `json:"spdx_id,omitempty"`
	NodeID *string `json:"node_id,omitempty"`
	URL    *string `json:"url,omitempty"`
	Links  Links   `json:"links,omitempty"`
}

// RESTCodeOfConduct is the code of conduct of a repository. Body is cut at the body bound of a comment.
type RESTCodeOfConduct struct {
	Key           *string `json:"key,omitempty"`
	Name          *string `json:"name,omitempty"`
	Body          *string `json:"body,omitempty"`
	BodyTruncated bool    `json:"body_truncated,omitempty"`
	URL           *string `json:"url,omitempty"`
	Links         Links   `json:"links,omitempty"`
}

// RESTFeatureStatus is the state of one security feature, enabled or disabled.
type RESTFeatureStatus struct {
	Status *string `json:"status,omitempty"`
}

// RESTBypassReviewer is a team or role that reviews delegated bypass requests.
type RESTBypassReviewer struct {
	ReviewerID   *int64  `json:"reviewer_id,omitempty"`
	ReviewerType *string `json:"reviewer_type,omitempty"`
	Mode         *string `json:"mode,omitempty"`
}

// RESTBypassOptions lists the reviewers of delegated secret scanning bypass.
type RESTBypassOptions struct {
	Reviewers          []RESTBypassReviewer `json:"reviewers,omitempty"`
	ReviewersTruncated bool                 `json:"reviewers_truncated,omitempty"`
}

// RESTSecurityAndAnalysis is the state of the security features of a repository.
type RESTSecurityAndAnalysis struct {
	AdvancedSecurity                      *RESTFeatureStatus `json:"advanced_security,omitempty"`
	CodeSecurity                          *RESTFeatureStatus `json:"code_security,omitempty"`
	DependabotSecurityUpdates             *RESTFeatureStatus `json:"dependabot_security_updates,omitempty"`
	SecretScanning                        *RESTFeatureStatus `json:"secret_scanning,omitempty"`
	SecretScanningPushProtection          *RESTFeatureStatus `json:"secret_scanning_push_protection,omitempty"`
	SecretScanningNonProviderPatterns     *RESTFeatureStatus `json:"secret_scanning_non_provider_patterns,omitempty"`
	SecretScanningAIDetection             *RESTFeatureStatus `json:"secret_scanning_ai_detection,omitempty"`
	SecretScanningDelegatedAlertDismissal *RESTFeatureStatus `json:"secret_scanning_delegated_alert_dismissal,omitempty"`
	SecretScanningDelegatedBypass         *RESTFeatureStatus `json:"secret_scanning_delegated_bypass,omitempty"`
	SecretScanningDelegatedBypassOptions  *RESTBypassOptions `json:"secret_scanning_delegated_bypass_options,omitempty"`
}

// RESTRepository is the view of a repository. A repository outside the connection's targets keeps only ID,
// NodeID, and FullName.
type RESTRepository struct {
	ID                        *int64                     `json:"id,omitempty"`
	NodeID                    *string                    `json:"node_id,omitempty"`
	Name                      *string                    `json:"name,omitempty"`
	FullName                  *string                    `json:"full_name,omitempty"`
	Owner                     *RESTAccount               `json:"owner,omitempty"`
	Private                   *bool                      `json:"private,omitempty"`
	URL                       *string                    `json:"url,omitempty"`
	Description               *string                    `json:"description,omitempty"`
	Fork                      *bool                      `json:"fork,omitempty"`
	Homepage                  *string                    `json:"homepage,omitempty"`
	Language                  *string                    `json:"language,omitempty"`
	ForksCount                *int64                     `json:"forks_count,omitempty"`
	StargazersCount           *int64                     `json:"stargazers_count,omitempty"`
	WatchersCount             *int64                     `json:"watchers_count,omitempty"`
	Size                      *int64                     `json:"size,omitempty"`
	DefaultBranch             *string                    `json:"default_branch,omitempty"`
	OpenIssuesCount           *int64                     `json:"open_issues_count,omitempty"`
	IsTemplate                *bool                      `json:"is_template,omitempty"`
	Topics                    []string                   `json:"topics,omitempty"`
	TopicsTruncated           bool                       `json:"topics_truncated,omitempty"`
	HasIssues                 *bool                      `json:"has_issues,omitempty"`
	HasProjects               *bool                      `json:"has_projects,omitempty"`
	HasWiki                   *bool                      `json:"has_wiki,omitempty"`
	HasPages                  *bool                      `json:"has_pages,omitempty"`
	HasDiscussions            *bool                      `json:"has_discussions,omitempty"`
	HasPullRequests           *bool                      `json:"has_pull_requests,omitempty"`
	PullRequestCreationPolicy *string                    `json:"pull_request_creation_policy,omitempty"`
	Archived                  *bool                      `json:"archived,omitempty"`
	Disabled                  *bool                      `json:"disabled,omitempty"`
	Visibility                *string                    `json:"visibility,omitempty"`
	PushedAt                  *string                    `json:"pushed_at,omitempty"`
	CreatedAt                 *string                    `json:"created_at,omitempty"`
	UpdatedAt                 *string                    `json:"updated_at,omitempty"`
	Permissions               *RESTPermissions           `json:"permissions,omitempty"`
	RoleName                  *string                    `json:"role_name,omitempty"`
	DeleteBranchOnMerge       *bool                      `json:"delete_branch_on_merge,omitempty"`
	SubscribersCount          *int64                     `json:"subscribers_count,omitempty"`
	NetworkCount              *int64                     `json:"network_count,omitempty"`
	CodeOfConduct             *RESTCodeOfConduct         `json:"code_of_conduct,omitempty"`
	License                   *RESTLicense               `json:"license,omitempty"`
	Forks                     *int64                     `json:"forks,omitempty"`
	OpenIssues                *int64                     `json:"open_issues,omitempty"`
	Watchers                  *int64                     `json:"watchers,omitempty"`
	AllowForking              *bool                      `json:"allow_forking,omitempty"`
	WebCommitSignoffRequired  *bool                      `json:"web_commit_signoff_required,omitempty"`
	SecurityAndAnalysis       *RESTSecurityAndAnalysis   `json:"security_and_analysis,omitempty"`
	CustomProperties          map[string]any             `json:"custom_properties,omitempty"`
	CustomPropertiesOmitted   bool                       `json:"custom_properties_omitted,omitempty"`
	AllowRebaseMerge          *bool                      `json:"allow_rebase_merge,omitempty"`
	AllowSquashMerge          *bool                      `json:"allow_squash_merge,omitempty"`
	AllowAutoMerge            *bool                      `json:"allow_auto_merge,omitempty"`
	AllowUpdateBranch         *bool                      `json:"allow_update_branch,omitempty"`
	SquashMergeCommitTitle    *string                    `json:"squash_merge_commit_title,omitempty"`
	SquashMergeCommitMessage  *string                    `json:"squash_merge_commit_message,omitempty"`
	MergeCommitTitle          *string                    `json:"merge_commit_title,omitempty"`
	MergeCommitMessage        *string                    `json:"merge_commit_message,omitempty"`
	AllowMergeCommit          *bool                      `json:"allow_merge_commit,omitempty"`
	MasterBranch              *string                    `json:"master_branch,omitempty"`
	StarredAt                 *string                    `json:"starred_at,omitempty"`
	AnonymousAccessEnabled    *bool                      `json:"anonymous_access_enabled,omitempty"`
	CodeSearchIndexStatus     *RESTCodeSearchIndexStatus `json:"code_search_index_status,omitempty"`
	Links                     Links                      `json:"links,omitempty"`
}

// project maps one repository. The repository is shown in full only when the targets admit it, by the
// connection's existing repository check; an unparsable full_name cannot be checked and counts as outside.
func (r *repositoryRaw) project(allowed allowlist) *RESTRepository {
	if r == nil {
		return nil
	}
	if !r.admitted(allowed) {
		return &RESTRepository{ID: r.ID, NodeID: nameText(r.NodeID), FullName: nameText(r.FullName)}
	}
	out := &RESTRepository{ID: r.ID, NodeID: nameText(r.NodeID), Name: nameText(r.Name),
		FullName: nameText(r.FullName), Owner: r.Owner.project(), Private: r.Private, URL: address(r.HTMLURL),
		Description: longText(r.Description), Fork: r.Fork, Homepage: address(r.Homepage),
		Language: nameText(r.Language), ForksCount: r.ForksCount, StargazersCount: r.StargazersCount,
		WatchersCount: r.WatchersCount, Size: r.Size, DefaultBranch: nameText(r.DefaultBranch),
		OpenIssuesCount: r.OpenIssuesCount, IsTemplate: r.IsTemplate, HasIssues: r.HasIssues,
		HasProjects: r.HasProjects, HasWiki: r.HasWiki, HasPages: r.HasPages, HasDiscussions: r.HasDiscussions,
		HasPullRequests: r.HasPullRequests, PullRequestCreationPolicy: nameText(r.PullRequestCreationPolicy),
		Archived: r.Archived, Disabled: r.Disabled, Visibility: nameText(r.Visibility),
		PushedAt: nameText(r.PushedAt), CreatedAt: nameText(r.CreatedAt), UpdatedAt: nameText(r.UpdatedAt),
		Permissions: r.Permissions, RoleName: nameText(r.RoleName), DeleteBranchOnMerge: r.DeleteBranchOnMerge,
		SubscribersCount: r.SubscribersCount, NetworkCount: r.NetworkCount, Forks: r.Forks,
		OpenIssues: r.OpenIssues, Watchers: r.Watchers, AllowForking: r.AllowForking,
		WebCommitSignoffRequired: r.WebCommitSignoffRequired, AllowRebaseMerge: r.AllowRebaseMerge,
		AllowSquashMerge: r.AllowSquashMerge, AllowAutoMerge: r.AllowAutoMerge,
		AllowUpdateBranch:        r.AllowUpdateBranch,
		SquashMergeCommitTitle:   nameText(r.SquashMergeCommitTitle),
		SquashMergeCommitMessage: nameText(r.SquashMergeCommitMessage),
		MergeCommitTitle:         nameText(r.MergeCommitTitle), MergeCommitMessage: nameText(r.MergeCommitMessage),
		AllowMergeCommit: r.AllowMergeCommit, MasterBranch: nameText(r.MasterBranch),
		StarredAt: nameText(r.StarredAt), AnonymousAccessEnabled: r.AnonymousAccessEnabled,
		Links: r.links}
	var topics []string
	for _, topic := range r.Topics {
		if topic != nil {
			topics = append(topics, *nameText(topic))
		}
	}
	out.Topics, out.TopicsTruncated = capped(topics)
	if s := r.CodeSearchIndexStatus; s != nil {
		out.CodeSearchIndexStatus = &RESTCodeSearchIndexStatus{LexicalSearchOK: s.LexicalSearchOK,
			LexicalCommitSHA: nameText(s.LexicalCommitSHA)}
	}
	if l := r.License; l != nil {
		out.License = &RESTLicense{Key: nameText(l.Key), Name: nameText(l.Name), SPDXID: nameText(l.SPDXID),
			NodeID: nameText(l.NodeID), URL: address(l.HTMLURL), Links: l.links}
	}
	if c := r.CodeOfConduct; c != nil {
		body, cut := cleanText(c.Body, projectionBodyLimit, true)
		out.CodeOfConduct = &RESTCodeOfConduct{Key: nameText(c.Key), Name: nameText(c.Name), Body: body,
			BodyTruncated: cut, URL: address(c.HTMLURL), Links: c.links}
	}
	out.SecurityAndAnalysis = r.SecurityAndAnalysis.project()
	out.CustomProperties, out.CustomPropertiesOmitted = projectCustomProperties(r.CustomProperties)
	return out
}

// admitted reports whether the targets let the repository be shown beyond its identifiers.
func (r *repositoryRaw) admitted(allowed allowlist) bool {
	if r.FullName == nil {
		return false
	}
	owner, name, ok := strings.Cut(*r.FullName, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return false
	}
	return allowed.admitsRepository(target{kind: kindRepository, owner: owner, repo: name})
}

func (s *securityRaw) project() *RESTSecurityAndAnalysis {
	if s == nil {
		return nil
	}
	feature := func(f *featureRaw) *RESTFeatureStatus {
		if f == nil {
			return nil
		}
		return &RESTFeatureStatus{Status: nameText(f.Status)}
	}
	out := &RESTSecurityAndAnalysis{AdvancedSecurity: feature(s.AdvancedSecurity),
		CodeSecurity: feature(s.CodeSecurity), DependabotSecurityUpdates: feature(s.DependabotSecurityUpdates),
		SecretScanning: feature(s.SecretScanning), SecretScanningPushProtection: feature(s.SecretScanningPushProtection),
		SecretScanningNonProviderPatterns:     feature(s.SecretScanningNonProviderPatterns),
		SecretScanningAIDetection:             feature(s.SecretScanningAIDetection),
		SecretScanningDelegatedAlertDismissal: feature(s.SecretScanningDelegatedAlertDismissal),
		SecretScanningDelegatedBypass:         feature(s.SecretScanningDelegatedBypass)}
	if o := s.SecretScanningDelegatedBypassOptions; o != nil {
		options := &RESTBypassOptions{}
		reviewers, cut := capped(o.Reviewers)
		for _, reviewer := range reviewers {
			options.Reviewers = append(options.Reviewers, RESTBypassReviewer{ReviewerID: reviewer.ReviewerID,
				ReviewerType: nameText(reviewer.ReviewerType), Mode: nameText(reviewer.Mode)})
		}
		options.ReviewersTruncated = cut
		out.SecretScanningDelegatedBypassOptions = options
	}
	return out
}

// projectCustomProperties decodes the free-form custom_properties object. Null values are dropped. When the
// object exceeds a bound it is returned empty with omitted set, since a cut object would state wrong values.
func projectCustomProperties(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, true
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true
	}
	nodes := 0
	cleaned, ok := boundedValue(object, 1, &nodes)
	if !ok {
		return nil, true
	}
	result, _ := cleaned.(map[string]any)
	if len(result) == 0 {
		return nil, false
	}
	return result, false
}

// boundedValue copies a decoded JSON value with control characters of its texts replaced, and reports false
// when it exceeds the depth, node, or text bounds.
func boundedValue(value any, depth int, nodes *int) (any, bool) {
	*nodes++
	if depth > customPropertiesDepth || *nodes > customPropertiesNodes {
		return nil, false
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if item == nil {
				continue
			}
			if utf8.RuneCountInString(key) > customPropertiesKeySize {
				return nil, false
			}
			clean, ok := boundedValue(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out[*mustClean(&key, customPropertiesKeySize)] = clean
		}
		return out, true
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if item == nil {
				continue
			}
			clean, ok := boundedValue(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out = append(out, clean)
		}
		return out, true
	case string:
		if utf8.RuneCountInString(v) > customPropertiesText {
			return nil, false
		}
		return *mustClean(&v, customPropertiesText), true
	}
	return value, true
}

// mustClean replaces control characters; the bound was checked before, so nothing is cut.
func mustClean(p *string, limit int) *string {
	text, _ := cleanText(p, limit, false)
	return text
}

var restRepositoryProperties = func() string {
	str := `{"type":"string"}`
	integer := `{"type":"integer"}`
	boolean := `{"type":"boolean"}`
	feature := objectSchema(`"status":` + str)
	names := []struct{ name, schema string }{
		{"id", integer}, {"node_id", str}, {"name", str}, {"full_name", str}, {"owner", restAccountSchema},
		{"private", boolean}, {"url", str}, {"description", str}, {"fork", boolean}, {"homepage", str},
		{"language", str}, {"forks_count", integer}, {"stargazers_count", integer}, {"watchers_count", integer},
		{"size", integer}, {"default_branch", str}, {"open_issues_count", integer}, {"is_template", boolean},
		{"topics", `{"type":"array","maxItems":100,"items":` + str + `}`}, {"topics_truncated", boolean},
		{"has_issues", boolean}, {"has_projects", boolean}, {"has_wiki", boolean}, {"has_pages", boolean},
		{"has_discussions", boolean}, {"has_pull_requests", boolean}, {"pull_request_creation_policy", str},
		{"archived", boolean}, {"disabled", boolean}, {"visibility", str}, {"pushed_at", str},
		{"created_at", str}, {"updated_at", str},
		{"permissions", objectSchema(`"admin":` + boolean + `,"maintain":` + boolean + `,"push":` + boolean +
			`,"triage":` + boolean + `,"pull":` + boolean)},
		{"role_name", str}, {"delete_branch_on_merge", boolean}, {"subscribers_count", integer},
		{"network_count", integer},
		{"code_of_conduct", objectSchema(`"key":` + str + `,"name":` + str + `,"body":` + str +
			`,"body_truncated":` + boolean + `,"url":` + str + `,"links":` + linksSchema(codeOfConductLinkKeys))},
		{"license", objectSchema(`"key":` + str + `,"name":` + str + `,"spdx_id":` + str + `,"node_id":` + str +
			`,"url":` + str + `,"links":` + linksSchema(licenseLinkKeys))},
		{"forks", integer}, {"open_issues", integer}, {"watchers", integer}, {"allow_forking", boolean},
		{"web_commit_signoff_required", boolean},
		{"security_and_analysis", objectSchema(`"advanced_security":` + feature + `,"code_security":` + feature +
			`,"dependabot_security_updates":` + feature + `,"secret_scanning":` + feature +
			`,"secret_scanning_push_protection":` + feature + `,"secret_scanning_non_provider_patterns":` + feature +
			`,"secret_scanning_ai_detection":` + feature + `,"secret_scanning_delegated_alert_dismissal":` + feature +
			`,"secret_scanning_delegated_bypass":` + feature + `,"secret_scanning_delegated_bypass_options":` +
			objectSchema(`"reviewers":{"type":"array","maxItems":100,"items":`+
				objectSchema(`"reviewer_id":`+integer+`,"reviewer_type":`+str+`,"mode":`+str)+`},"reviewers_truncated":`+boolean))},
		{"custom_properties", `{"type":"object","additionalProperties":true}`},
		{"custom_properties_omitted", boolean},
		{"allow_rebase_merge", boolean}, {"allow_squash_merge", boolean}, {"allow_auto_merge", boolean},
		{"allow_update_branch", boolean}, {"squash_merge_commit_title", str}, {"squash_merge_commit_message", str},
		{"merge_commit_title", str}, {"merge_commit_message", str}, {"allow_merge_commit", boolean},
		{"master_branch", str}, {"starred_at", str}, {"anonymous_access_enabled", boolean},
		{"code_search_index_status", objectSchema(`"lexical_search_ok":` + boolean + `,"lexical_commit_sha":` + str)},
		{"links", linksSchema(restRepositoryLinkKeys)},
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = `"` + n.name + `":` + n.schema
	}
	return strings.Join(parts, ",")
}()

var restRepositorySchema = objectSchema(restRepositoryProperties)

var restRepositoryFields = []capability.Field{
	{Name: "id", Description: "Numeric repository id"},
	{Name: "node_id", Description: "GraphQL node id of the repository"},
	{Name: "name", Description: "Repository name, untrusted data"},
	{Name: "full_name", Description: "OWNER/REPO, untrusted data"},
	{Name: "owner", Description: "Owner as an account: " + restAccountMembers},
	{Name: "private", Description: "True for a private repository"},
	{Name: "url", Description: "Web address of the repository, untrusted data, never fetched"},
	{Name: "description", Description: "Repository description, untrusted data"},
	{Name: "fork", Description: "True for a fork"},
	{Name: "homepage", Description: "Homepage address, untrusted data, never fetched"},
	{Name: "language", Description: "Main language, untrusted data"},
	{Name: "forks_count", Description: "Number of forks"},
	{Name: "stargazers_count", Description: "Number of stars"},
	{Name: "watchers_count", Description: "Number of watchers"},
	{Name: "size", Description: "Size in kilobytes"},
	{Name: "default_branch", Description: "Default branch, untrusted data"},
	{Name: "open_issues_count", Description: "Number of open issues and pull requests"},
	{Name: "is_template", Description: "True for a template repository"},
	{Name: "topics", Description: "Topics, at most 100, untrusted data"},
	{Name: "topics_truncated", Description: "True when the repository holds more topics than shown"},
	{Name: "has_issues", Description: "True when issues are enabled"},
	{Name: "has_projects", Description: "True when projects are enabled"},
	{Name: "has_wiki", Description: "True when the wiki is enabled"},
	{Name: "has_pages", Description: "True when GitHub Pages is enabled"},
	{Name: "has_discussions", Description: "True when discussions are enabled"},
	{Name: "has_pull_requests", Description: "True when pull requests are enabled"},
	{Name: "pull_request_creation_policy", Description: "Who may open pull requests: all or collaborators_only"},
	{Name: "archived", Description: "True for an archived repository"},
	{Name: "disabled", Description: "True for a disabled repository"},
	{Name: "visibility", Description: "public, private, or internal"},
	{Name: "pushed_at", Description: "Time of the last push"},
	{Name: "created_at", Description: "Creation time"},
	{Name: "updated_at", Description: "Time of the last change"},
	{Name: "permissions", Description: "admin, maintain, push, triage, and pull of the token in the repository"},
	{Name: "role_name", Description: "Role of the token in the repository, untrusted data"},
	{Name: "delete_branch_on_merge", Description: "True when merged branches are deleted"},
	{Name: "subscribers_count", Description: "Number of subscribers"},
	{Name: "network_count", Description: "Number of repositories in the fork network"},
	{Name: "code_of_conduct", Description: "key, name, body (cut at 4000 characters, body_truncated says so), url, and links of the code of conduct, untrusted data"},
	{Name: "license", Description: "key, name, spdx_id, node_id, url (the web address), and links of the license, untrusted data"},
	{Name: "forks", Description: "Number of forks, the same as forks_count"},
	{Name: "open_issues", Description: "Number of open issues, the same as open_issues_count"},
	{Name: "watchers", Description: "Number of watchers, the same as watchers_count"},
	{Name: "allow_forking", Description: "True when forking is allowed"},
	{Name: "web_commit_signoff_required", Description: "True when web commits need a sign-off"},
	{Name: "security_and_analysis", Description: "Enabled or disabled status of each security feature, and the reviewers of delegated secret scanning bypass"},
	{Name: "custom_properties", Description: "Custom property name to value, untrusted data; left out when it exceeds the bounds"},
	{Name: "custom_properties_omitted", Description: "True when custom_properties exceeded the bounds and was left out"},
	{Name: "allow_rebase_merge", Description: "True when rebase merging is allowed"},
	{Name: "allow_squash_merge", Description: "True when squash merging is allowed"},
	{Name: "allow_auto_merge", Description: "True when auto-merge is allowed"},
	{Name: "allow_update_branch", Description: "True when a pull request branch may be updated from the base"},
	{Name: "squash_merge_commit_title", Description: "Default squash commit title"},
	{Name: "squash_merge_commit_message", Description: "Default squash commit message"},
	{Name: "merge_commit_title", Description: "Default merge commit title"},
	{Name: "merge_commit_message", Description: "Default merge commit message"},
	{Name: "allow_merge_commit", Description: "True when merge commits are allowed"},
	{Name: "master_branch", Description: "Former default branch name, untrusted data"},
	{Name: "starred_at", Description: "Time the account starred the repository, in a star list"},
	{Name: "anonymous_access_enabled", Description: "True when anonymous Git access is enabled"},
	{Name: "code_search_index_status", Description: "lexical_search_ok and lexical_commit_sha of the code search index"},
	{Name: "links", Description: "REST url and every other *_url address of the repository by their GitHub names, untrusted data, never fetched"},
}
