package github

import (
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The ref width: the members of GitHub's short branch, tag, Git ref, and Git tag beyond the compact views of
// the branch and tag tools. The flat members keep their own treatment in the tools.

var (
	refObjectLinkKeys        = []string{"url"}
	branchProtectionLinkKeys = []string{"url", "protection_url"}
	statusChecksLinkKeys     = []string{"url", "contexts_url"}
	branchActorsLinkKeys     = []string{"url", "users_url", "teams_url", "apps_url"}
	tagArchiveLinkKeys       = []string{"zipball_url", "tarball_url"}
)

// linkOf returns the one address as links, or nil while it is unusable.
func linkOf(key string, value *string) Links {
	if a := address(value); a != nil {
		return Links{key: *a}
	}
	return nil
}

// RESTRefCommit is the commit a branch or a tag points at.
type RESTRefCommit struct {
	SHA   string `json:"sha"`
	Links Links  `json:"links,omitempty"`
}

// RESTRefObject is the Git object a ref or a tag object points at.
type RESTRefObject struct {
	SHA   *string `json:"sha,omitempty"`
	Type  *string `json:"type,omitempty"`
	Links Links   `json:"links,omitempty"`
}

type refObjectRaw struct {
	SHA  *string `json:"sha"`
	Type *string `json:"type"`
	URL  *string `json:"url"`
}

func (r *refObjectRaw) project() *RESTRefObject {
	if r == nil || r.SHA == nil && r.Type == nil && r.URL == nil {
		return nil
	}
	return &RESTRefObject{SHA: nameText(r.SHA), Type: nameText(r.Type), Links: linkOf("url", r.URL)}
}

var restRefObjectSchema = objectSchema(`"sha":{"type":"string"},"type":{"type":"string"},"links":` +
	linksSchema(refObjectLinkKeys))

var restRefCommitSchema = objectSchema(`"sha":{"type":"string"},"links":` + linksSchema(refObjectLinkKeys))

// ---- Branch protection (branch-protection)

// switchRaw is a protection setting that is on or off, some with an address of their own.
type switchRaw struct {
	Enabled *bool `json:"enabled"`
	links   Links
}

func (r *switchRaw) UnmarshalJSON(data []byte) (err error) {
	type plain switchRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, refObjectLinkKeys)
	return err
}

type statusCheckRaw struct {
	Context *string `json:"context"`
	AppID   *int64  `json:"app_id"`
}

type statusChecksRaw struct {
	EnforcementLevel *string          `json:"enforcement_level"`
	Strict           *bool            `json:"strict"`
	Contexts         []*string        `json:"contexts"`
	Checks           []statusCheckRaw `json:"checks"`
	links            Links
}

func (r *statusChecksRaw) UnmarshalJSON(data []byte) (err error) {
	type plain statusChecksRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, statusChecksLinkKeys)
	return err
}

// actorsRaw is a list of users, teams, and apps a protection rule names.
type actorsRaw struct {
	Users []accountRaw `json:"users"`
	Teams []teamCore   `json:"teams"`
	Apps  []appRaw     `json:"apps"`
	links Links
}

func (r *actorsRaw) UnmarshalJSON(data []byte) (err error) {
	type plain actorsRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, branchActorsLinkKeys)
	return err
}

type requiredReviewsRaw struct {
	DismissalRestrictions        *actorsRaw `json:"dismissal_restrictions"`
	BypassPullRequestAllowances  *actorsRaw `json:"bypass_pull_request_allowances"`
	DismissStaleReviews          *bool      `json:"dismiss_stale_reviews"`
	RequireCodeOwnerReviews      *bool      `json:"require_code_owner_reviews"`
	RequiredApprovingReviewCount *int64     `json:"required_approving_review_count"`
	RequireLastPushApproval      *bool      `json:"require_last_push_approval"`
	links                        Links
}

func (r *requiredReviewsRaw) UnmarshalJSON(data []byte) (err error) {
	type plain requiredReviewsRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, refObjectLinkKeys)
	return err
}

type branchProtectionRaw struct {
	Enabled                        *bool               `json:"enabled"`
	Name                           *string             `json:"name"`
	RequiredStatusChecks           *statusChecksRaw    `json:"required_status_checks"`
	EnforceAdmins                  *switchRaw          `json:"enforce_admins"`
	RequiredPullRequestReviews     *requiredReviewsRaw `json:"required_pull_request_reviews"`
	Restrictions                   *actorsRaw          `json:"restrictions"`
	RequiredLinearHistory          *switchRaw          `json:"required_linear_history"`
	AllowForcePushes               *switchRaw          `json:"allow_force_pushes"`
	AllowDeletions                 *switchRaw          `json:"allow_deletions"`
	BlockCreations                 *switchRaw          `json:"block_creations"`
	RequiredConversationResolution *switchRaw          `json:"required_conversation_resolution"`
	RequiredSignatures             *switchRaw          `json:"required_signatures"`
	LockBranch                     *switchRaw          `json:"lock_branch"`
	AllowForkSyncing               *switchRaw          `json:"allow_fork_syncing"`
	links                          Links
}

func (r *branchProtectionRaw) UnmarshalJSON(data []byte) (err error) {
	type plain branchProtectionRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, branchProtectionLinkKeys)
	return err
}

// RESTBranchSwitch is one protection setting.
type RESTBranchSwitch struct {
	Enabled *bool `json:"enabled,omitempty"`
	Links   Links `json:"links,omitempty"`
}

// RESTStatusCheck is one status check a protected branch requires.
type RESTStatusCheck struct {
	Context *string `json:"context,omitempty"`
	AppID   *int64  `json:"app_id,omitempty"`
}

// RESTRequiredStatusChecks is the status check rule of a protected branch.
type RESTRequiredStatusChecks struct {
	EnforcementLevel  *string           `json:"enforcement_level,omitempty"`
	Strict            *bool             `json:"strict,omitempty"`
	Contexts          []string          `json:"contexts,omitempty"`
	ContextsTruncated bool              `json:"contexts_truncated,omitempty"`
	Checks            []RESTStatusCheck `json:"checks,omitempty"`
	ChecksTruncated   bool              `json:"checks_truncated,omitempty"`
	Links             Links             `json:"links,omitempty"`
}

// RESTBranchActors are the users, teams, and apps a protection rule names.
type RESTBranchActors struct {
	Users          []RESTAccount    `json:"users,omitempty"`
	UsersTruncated bool             `json:"users_truncated,omitempty"`
	Teams          []RESTTeamSimple `json:"teams,omitempty"`
	TeamsTruncated bool             `json:"teams_truncated,omitempty"`
	Apps           []RESTApp        `json:"apps,omitempty"`
	AppsTruncated  bool             `json:"apps_truncated,omitempty"`
	Links          Links            `json:"links,omitempty"`
}

// RESTRequiredReviews is the pull request review rule of a protected branch.
type RESTRequiredReviews struct {
	DismissalRestrictions        *RESTBranchActors `json:"dismissal_restrictions,omitempty"`
	BypassPullRequestAllowances  *RESTBranchActors `json:"bypass_pull_request_allowances,omitempty"`
	DismissStaleReviews          *bool             `json:"dismiss_stale_reviews,omitempty"`
	RequireCodeOwnerReviews      *bool             `json:"require_code_owner_reviews,omitempty"`
	RequiredApprovingReviewCount *int64            `json:"required_approving_review_count,omitempty"`
	RequireLastPushApproval      *bool             `json:"require_last_push_approval,omitempty"`
	Links                        Links             `json:"links,omitempty"`
}

// RESTBranchProtection is the protection of a branch as GitHub's short branch carries it.
type RESTBranchProtection struct {
	Name                           *string                   `json:"name,omitempty"`
	Enabled                        *bool                     `json:"enabled,omitempty"`
	RequiredStatusChecks           *RESTRequiredStatusChecks `json:"required_status_checks,omitempty"`
	EnforceAdmins                  *RESTBranchSwitch         `json:"enforce_admins,omitempty"`
	RequiredPullRequestReviews     *RESTRequiredReviews      `json:"required_pull_request_reviews,omitempty"`
	Restrictions                   *RESTBranchActors         `json:"restrictions,omitempty"`
	RequiredLinearHistory          *RESTBranchSwitch         `json:"required_linear_history,omitempty"`
	AllowForcePushes               *RESTBranchSwitch         `json:"allow_force_pushes,omitempty"`
	AllowDeletions                 *RESTBranchSwitch         `json:"allow_deletions,omitempty"`
	BlockCreations                 *RESTBranchSwitch         `json:"block_creations,omitempty"`
	RequiredConversationResolution *RESTBranchSwitch         `json:"required_conversation_resolution,omitempty"`
	RequiredSignatures             *RESTBranchSwitch         `json:"required_signatures,omitempty"`
	LockBranch                     *RESTBranchSwitch         `json:"lock_branch,omitempty"`
	AllowForkSyncing               *RESTBranchSwitch         `json:"allow_fork_syncing,omitempty"`
	Links                          Links                     `json:"links,omitempty"`
}

func (r *switchRaw) project() *RESTBranchSwitch {
	if r == nil {
		return nil
	}
	return &RESTBranchSwitch{Enabled: r.Enabled, Links: r.links}
}

func (r *statusChecksRaw) project() *RESTRequiredStatusChecks {
	if r == nil {
		return nil
	}
	out := &RESTRequiredStatusChecks{EnforcementLevel: shortText(r.EnforcementLevel), Strict: r.Strict, Links: r.links}
	var contexts []string
	for _, context := range r.Contexts {
		if text := nameText(context); text != nil {
			contexts = append(contexts, *text)
		}
	}
	out.Contexts, out.ContextsTruncated = capped(contexts)
	checks, cut := capped(r.Checks)
	for _, check := range checks {
		out.Checks = append(out.Checks, RESTStatusCheck{Context: nameText(check.Context), AppID: check.AppID})
	}
	out.ChecksTruncated = cut
	return out
}

func (r *actorsRaw) project() *RESTBranchActors {
	if r == nil {
		return nil
	}
	out := &RESTBranchActors{Links: r.links}
	out.Users, out.UsersTruncated = accounts(r.Users)
	teams, cut := capped(r.Teams)
	for i := range teams {
		out.Teams = append(out.Teams, *teams[i].simple())
	}
	out.TeamsTruncated = cut
	apps, cut := capped(r.Apps)
	for i := range apps {
		out.Apps = append(out.Apps, *apps[i].project())
	}
	out.AppsTruncated = cut
	return out
}

func (r *requiredReviewsRaw) project() *RESTRequiredReviews {
	if r == nil {
		return nil
	}
	return &RESTRequiredReviews{DismissalRestrictions: r.DismissalRestrictions.project(),
		BypassPullRequestAllowances: r.BypassPullRequestAllowances.project(), DismissStaleReviews: r.DismissStaleReviews,
		RequireCodeOwnerReviews: r.RequireCodeOwnerReviews, RequiredApprovingReviewCount: r.RequiredApprovingReviewCount,
		RequireLastPushApproval: r.RequireLastPushApproval, Links: r.links}
}

func (r *branchProtectionRaw) project() *RESTBranchProtection {
	if r == nil {
		return nil
	}
	return &RESTBranchProtection{Name: nameText(r.Name), Enabled: r.Enabled,
		RequiredStatusChecks: r.RequiredStatusChecks.project(), EnforceAdmins: r.EnforceAdmins.project(),
		RequiredPullRequestReviews: r.RequiredPullRequestReviews.project(), Restrictions: r.Restrictions.project(),
		RequiredLinearHistory: r.RequiredLinearHistory.project(), AllowForcePushes: r.AllowForcePushes.project(),
		AllowDeletions: r.AllowDeletions.project(), BlockCreations: r.BlockCreations.project(),
		RequiredConversationResolution: r.RequiredConversationResolution.project(),
		RequiredSignatures:             r.RequiredSignatures.project(), LockBranch: r.LockBranch.project(),
		AllowForkSyncing: r.AllowForkSyncing.project(), Links: r.links}
}

var restBranchSwitchSchema = objectSchema(`"enabled":{"type":"boolean"},"links":` + linksSchema(refObjectLinkKeys))

var restBranchActorsSchema = objectSchema(`"users":{"type":"array","maxItems":100,"items":` + restAccountSchema +
	`},"users_truncated":{"type":"boolean"},"teams":{"type":"array","maxItems":100,"items":` + teamSimpleSchema +
	`},"teams_truncated":{"type":"boolean"},"apps":{"type":"array","maxItems":100,"items":` + restAppSchema +
	`},"apps_truncated":{"type":"boolean"},"links":` + linksSchema(branchActorsLinkKeys))

var restBranchProtectionSchema = objectSchema(`"name":{"type":"string"},"enabled":{"type":"boolean"},` +
	`"required_status_checks":` + objectSchema(`"enforcement_level":{"type":"string"},"strict":{"type":"boolean"},`+
	`"contexts":{"type":"array","maxItems":100,"items":{"type":"string"}},"contexts_truncated":{"type":"boolean"},`+
	`"checks":{"type":"array","maxItems":100,"items":`+objectSchema(`"context":{"type":"string"},`+
	`"app_id":{"type":"integer"}`)+`},"checks_truncated":{"type":"boolean"},"links":`+
	linksSchema(statusChecksLinkKeys)) + `,"enforce_admins":` + restBranchSwitchSchema +
	`,"required_pull_request_reviews":` + objectSchema(`"dismissal_restrictions":`+restBranchActorsSchema+
	`,"bypass_pull_request_allowances":`+restBranchActorsSchema+`,"dismiss_stale_reviews":{"type":"boolean"},`+
	`"require_code_owner_reviews":{"type":"boolean"},"required_approving_review_count":{"type":"integer"},`+
	`"require_last_push_approval":{"type":"boolean"},"links":`+linksSchema(refObjectLinkKeys)) +
	`,"restrictions":` + restBranchActorsSchema + `,"required_linear_history":` + restBranchSwitchSchema +
	`,"allow_force_pushes":` + restBranchSwitchSchema + `,"allow_deletions":` + restBranchSwitchSchema +
	`,"block_creations":` + restBranchSwitchSchema + `,"required_conversation_resolution":` +
	restBranchSwitchSchema + `,"required_signatures":` + restBranchSwitchSchema + `,"lock_branch":` +
	restBranchSwitchSchema + `,"allow_fork_syncing":` + restBranchSwitchSchema + `,"links":` +
	linksSchema(branchProtectionLinkKeys))

// Schemas and fields of the branch width.

var branchWidthProperties = `"commit":` + restRefCommitSchema + `,"protection":` + restBranchProtectionSchema +
	`,"links":` + linksSchema([]string{"protection_url"})

var branchWidthFields = []capability.Field{
	{Name: "commit", Description: "Latest commit with its sha and links (the REST url)"},
	{Name: "protection", Description: "Protection of the branch, only where GitHub sends it with the branch: " +
		"name, enabled, required_status_checks, enforce_admins, required_pull_request_reviews, restrictions, " +
		"and the switches required_linear_history, allow_force_pushes, allow_deletions, block_creations, " +
		"required_conversation_resolution, required_signatures, lock_branch, and allow_fork_syncing, each " +
		"with its own links; users, teams, and apps at most 100 each; untrusted data"},
	{Name: "links", Description: "REST protection_url by its GitHub name, untrusted data, never fetched"},
}

var tagWidthProperties = `"node_id":{"type":"string"},"commit":` + restRefCommitSchema + `,"links":` +
	linksSchema(tagArchiveLinkKeys)

var tagWidthFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the tag"},
	{Name: "commit", Description: "Commit the tag points at with its sha and links (the REST url)"},
	{Name: "links", Description: "REST zipball_url and tarball_url by their GitHub names, untrusted data, never fetched"},
}

// RESTTagRef is the Git ref behind a tag.
type RESTTagRef struct {
	Ref    *string        `json:"ref,omitempty"`
	NodeID *string        `json:"node_id,omitempty"`
	Object *RESTRefObject `json:"object,omitempty"`
	Links  Links          `json:"links,omitempty"`
}

// TagWidth holds the members of GitHub's Git ref and Git tag that tags.get adds to its compact view. The
// tag object's members are only there for an annotated tag.
type TagWidth struct {
	NodeID       *string                 `json:"node_id,omitempty"`
	Links        Links                   `json:"links,omitempty"`
	TaggerDetail *RESTCommitIdentity     `json:"tagger_detail,omitempty"`
	ObjectDetail *RESTRefObject          `json:"object_detail,omitempty"`
	Verification *RESTCommitVerification `json:"verification,omitempty"`
	RefDetail    *RESTTagRef             `json:"ref_detail,omitempty"`
}

var tagGetWidthProperties = `"node_id":{"type":"string"},"links":` + linksSchema(refObjectLinkKeys) +
	`,"tagger_detail":` + restCommitIdentitySchema + `,"object_detail":` + restRefObjectSchema +
	`,"verification":` + restCommitVerificationSchema + `,"ref_detail":` + objectSchema(`"ref":{"type":"string"},`+
	`"node_id":{"type":"string"},"object":`+restRefObjectSchema+`,"links":`+linksSchema(refObjectLinkKeys))

var tagGetWidthFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the tag object; present only for an annotated tag"},
	{Name: "links", Description: "REST url of the tag object by its GitHub name, untrusted data, never fetched; " +
		"present only for an annotated tag"},
	{Name: "tagger_detail", Description: "Name, email, and date of the tagger; untrusted data; present only for " +
		"an annotated tag"},
	{Name: "object_detail", Description: "Object the tag object points at with sha, type, and links; present " +
		"only for an annotated tag"},
	{Name: "verification", Description: "Signature verification of an annotated tag: verified, reason, " +
		"verified_at, payload and signature each cut at 4096 characters with payload_truncated and " +
		"signature_truncated; untrusted data"},
	{Name: "ref_detail", Description: "Git ref of the tag: ref, node_id, object (sha, type, links) it names, " +
		"and links; untrusted data"},
}
