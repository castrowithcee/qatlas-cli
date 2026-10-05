package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The merge and branch settings of a repository. They belong to the administrator tools: they decide how
// changes reach the branches of the repository, so each tool requires an allow-list. Reading keeps to a fixed
// list of fields, and a change accepts exactly the merge settings below, nothing else of the repository.

// repositorySettingSensitivity is the data sensitivity of the repository settings tools.
const repositorySettingSensitivity = "github-repository-settings"

// The values the commit message settings accept.
var (
	squashTitleValues   = []string{"PR_TITLE", "COMMIT_OR_PR_TITLE"}
	squashMessageValues = []string{"PR_BODY", "COMMIT_MESSAGES", "BLANK"}
	mergeTitleValues    = []string{"PR_TITLE", "MERGE_MESSAGE"}
	mergeMessageValues  = []string{"PR_BODY", "PR_TITLE", "BLANK"}
	visibilityValues    = []string{"public", "private", "internal"}
)

// repositorySettingProperties are the merge settings both tools carry; the read adds three more fields.
const repositorySettingProperties = `"allow_merge_commit":{"type":"boolean"},"allow_squash_merge":{"type":"boolean"},` +
	`"allow_rebase_merge":{"type":"boolean"},"allow_auto_merge":{"type":"boolean"},` +
	`"allow_update_branch":{"type":"boolean"},"delete_branch_on_merge":{"type":"boolean"},` +
	`"web_commit_signoff_required":{"type":"boolean"},` +
	`"squash_merge_commit_title":{"type":"string"},"squash_merge_commit_message":{"type":"string"},` +
	`"merge_commit_title":{"type":"string"},"merge_commit_message":{"type":"string"}`

const repositorySettingsGetProperties = `"default_branch":{"type":"string"},"visibility":{"type":"string"},` +
	`"archived":{"type":"boolean"},` + repositorySettingProperties

var repositorySettingsGet = capability.Descriptor{
	ID:      Provider + ".repositorysettings.get",
	Version: 1,
	Title:   "Get the merge and branch settings of a GitHub repository",
	Description: "Read the default branch, visibility, archived state, and merge settings of a repository a " +
		"connection allows and nothing else of the repository; offered only where the connection lists it",
	Tags:                  []string{"github", "repository", "settings", "merge", "branch", "get", "admin"},
	Risk:                  guardedRisk(capability.EffectRead, capability.IdempotencySafe, repositorySettingSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema:           inputSchema(""),
	OutputSchema:          settingsOutput(repositorySettingsGetProperties, `"default_branch","visibility","archived"`),
	Fields: []capability.Field{
		{Name: "default_branch", Description: "Name of the default branch"},
		{Name: "visibility", Description: "public, private, or internal"},
		{Name: "archived", Description: "True when the repository is archived"},
		{Name: "allow_merge_commit", Description: "True when pull requests may be merged with a merge commit"},
		{Name: "allow_squash_merge", Description: "True when pull requests may be squash-merged"},
		{Name: "allow_rebase_merge", Description: "True when pull requests may be rebase-merged"},
		{Name: "allow_auto_merge", Description: "True when auto-merge may be enabled on pull requests"},
		{Name: "allow_update_branch", Description: "True when a pull request branch may be updated from its base"},
		{Name: "delete_branch_on_merge", Description: "True when head branches are deleted after a merge"},
		{Name: "squash_merge_commit_title", Description: "PR_TITLE or COMMIT_OR_PR_TITLE"},
		{Name: "squash_merge_commit_message", Description: "PR_BODY, COMMIT_MESSAGES, or BLANK"},
		{Name: "merge_commit_title", Description: "PR_TITLE or MERGE_MESSAGE"},
		{Name: "merge_commit_message", Description: "PR_BODY, PR_TITLE, or BLANK"},
		{Name: "web_commit_signoff_required", Description: "True when web commits must be signed off"},
	},
	Examples: []capability.Example{{Description: "Read the merge settings", Arguments: json.RawMessage(`{}`)}},
}

var repositorySettingsUpdate = capability.Descriptor{
	ID:      Provider + ".repositorysettings.update",
	Version: 1,
	Title:   "Change the merge and branch settings of a GitHub repository",
	Description: "Change which merge methods a repository a connection allows accepts, its auto-merge, " +
		"branch update, and branch deletion settings, its merge commit messages, and sign-off for web commits; " +
		"a value left out stays as it is, nothing else of the repository changes, and the three merge methods " +
		"are never all switched off; offered only where the connection lists it",
	Tags:                  []string{"github", "repository", "settings", "merge", "branch", "update", "admin"},
	Risk:                  guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, repositorySettingSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: inputSchema(`"allow_merge_commit":{"type":"boolean"},"allow_squash_merge":{"type":"boolean"},` +
		`"allow_rebase_merge":{"type":"boolean"},"allow_auto_merge":{"type":"boolean"},` +
		`"allow_update_branch":{"type":"boolean"},"delete_branch_on_merge":{"type":"boolean"},` +
		`"web_commit_signoff_required":{"type":"boolean"},` +
		`"squash_merge_commit_title":` + enumSchema(squashTitleValues) + `,` +
		`"squash_merge_commit_message":` + enumSchema(squashMessageValues) + `,` +
		`"merge_commit_title":` + enumSchema(mergeTitleValues) + `,` +
		`"merge_commit_message":` + enumSchema(mergeMessageValues)),
	OutputSchema: settingsOutput(repositorySettingProperties, ""),
	Arguments: []capability.Argument{
		{Name: "allow_merge_commit", Description: "true to allow merging pull requests with a merge commit"},
		{Name: "allow_squash_merge", Description: "true to allow squash-merging pull requests"},
		{Name: "allow_rebase_merge", Description: "true to allow rebase-merging pull requests"},
		{Name: "allow_auto_merge", Description: "true to allow auto-merge on pull requests"},
		{Name: "allow_update_branch", Description: "true to offer updating a pull request branch from its base"},
		{Name: "delete_branch_on_merge", Description: "true to delete head branches after a merge"},
		{Name: "web_commit_signoff_required", Description: "true to require sign-off on web commits"},
		{Name: "squash_merge_commit_title", Description: "PR_TITLE or COMMIT_OR_PR_TITLE"},
		{Name: "squash_merge_commit_message", Description: "PR_BODY, COMMIT_MESSAGES, or BLANK"},
		{Name: "merge_commit_title", Description: "PR_TITLE or MERGE_MESSAGE"},
		{Name: "merge_commit_message", Description: "PR_BODY, PR_TITLE, or BLANK"},
	},
	Fields: []capability.Field{
		{Name: "allow_merge_commit", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "allow_squash_merge", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "allow_rebase_merge", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "allow_auto_merge", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "allow_update_branch", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "delete_branch_on_merge", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "web_commit_signoff_required", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "squash_merge_commit_title", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "squash_merge_commit_message", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "merge_commit_title", Description: "Present only when the call gave it: the value GitHub reports"},
		{Name: "merge_commit_message", Description: "Present only when the call gave it: the value GitHub reports"},
	},
	Examples: []capability.Example{{
		Description: "Allow only squash merges and delete head branches after a merge",
		Arguments: json.RawMessage(`{"allow_merge_commit":false,"allow_squash_merge":true,"allow_rebase_merge":false,` +
			`"delete_branch_on_merge":true}`),
	}},
}

// Permission messages of the repository settings tools; GitHub decides on every request.
const (
	repositorySettingsReadPermission = "GitHub refused this token the settings of this repository or does not " +
		"show the repository to it; reading them needs repo on a classic token for a private repository, or " +
		"Metadata: read on a fine-grained token"
	repositorySettingsChangePermission = "GitHub refused this change of the repository settings or does not show " +
		"the repository to this token; it needs the admin role on the repository with repo on a classic token, or " +
		"Administration: write on a fine-grained token, and an organization policy may forbid it as well"
)

func checkRepositorySettings(a *actionsArguments, _ target) error {
	switch {
	case a.AllowMergeCommit == nil && a.AllowSquashMerge == nil && a.AllowRebaseMerge == nil &&
		a.AllowAutoMerge == nil && a.AllowUpdateBranch == nil && a.DeleteBranchOnMerge == nil &&
		a.WebCommitSignoffRequired == nil && a.SquashMergeCommitTitle == "" && a.SquashMergeCommitMessage == "" &&
		a.MergeCommitTitle == "" && a.MergeCommitMessage == "":
		return invalidRequest("give at least one repository setting")
	case a.SquashMergeCommitTitle != "" && !contains(squashTitleValues, a.SquashMergeCommitTitle):
		return invalidRequest("squash_merge_commit_title must be one of " + strings.Join(squashTitleValues, ", "))
	case a.SquashMergeCommitMessage != "" && !contains(squashMessageValues, a.SquashMergeCommitMessage):
		return invalidRequest("squash_merge_commit_message must be one of " + strings.Join(squashMessageValues, ", "))
	case a.MergeCommitTitle != "" && !contains(mergeTitleValues, a.MergeCommitTitle):
		return invalidRequest("merge_commit_title must be one of " + strings.Join(mergeTitleValues, ", "))
	case a.MergeCommitMessage != "" && !contains(mergeMessageValues, a.MergeCommitMessage):
		return invalidRequest("merge_commit_message must be one of " + strings.Join(mergeMessageValues, ", "))
	case isFalse(a.AllowMergeCommit) && isFalse(a.AllowSquashMerge) && isFalse(a.AllowRebaseMerge):
		return invalidRequest("at least one merge method must stay allowed; allow_merge_commit, " +
			"allow_squash_merge, and allow_rebase_merge must not all be false")
	}
	return nil
}

func isFalse(value *bool) bool { return value != nil && !*value }

// RepositorySettings are the merge and branch settings GitHub reports for the bound repository. A setting
// GitHub leaves out, as it does for a token without write access, is absent here as well.
type RepositorySettings struct {
	DefaultBranch string `json:"default_branch"`
	Visibility    string `json:"visibility"`
	Archived      bool   `json:"archived"`
	MergeSettings
}

// MergeSettings are the settings repositorysettings.update can change.
type MergeSettings struct {
	AllowMergeCommit         *bool  `json:"allow_merge_commit,omitempty"`
	AllowSquashMerge         *bool  `json:"allow_squash_merge,omitempty"`
	AllowRebaseMerge         *bool  `json:"allow_rebase_merge,omitempty"`
	AllowAutoMerge           *bool  `json:"allow_auto_merge,omitempty"`
	AllowUpdateBranch        *bool  `json:"allow_update_branch,omitempty"`
	DeleteBranchOnMerge      *bool  `json:"delete_branch_on_merge,omitempty"`
	SquashMergeCommitTitle   string `json:"squash_merge_commit_title,omitempty"`
	SquashMergeCommitMessage string `json:"squash_merge_commit_message,omitempty"`
	MergeCommitTitle         string `json:"merge_commit_title,omitempty"`
	MergeCommitMessage       string `json:"merge_commit_message,omitempty"`
	WebCommitSignoffRequired *bool  `json:"web_commit_signoff_required,omitempty"`
}

// repositorySettingsAnswer is the part of a repository GitHub's answer is decoded into; nothing else of it is
// read.
type repositorySettingsAnswer struct {
	DefaultBranch string `json:"default_branch"`
	Visibility    string `json:"visibility"`
	Archived      bool   `json:"archived"`
	MergeSettings
}

// valid reports whether every text of the answer is one of the values the tools name, so an answer is never
// echoed with text of GitHub's own.
func (a *repositorySettingsAnswer) valid() bool {
	one := func(values []string, value string) bool { return value == "" || contains(values, value) }
	return a.DefaultBranch != "" && len(a.DefaultBranch) <= 255 && validRef(a.DefaultBranch) &&
		contains(visibilityValues, a.Visibility) && one(squashTitleValues, a.SquashMergeCommitTitle) &&
		one(squashMessageValues, a.SquashMergeCommitMessage) && one(mergeTitleValues, a.MergeCommitTitle) &&
		one(mergeMessageValues, a.MergeCommitMessage)
}

// repositorySettingsFailure names the permission a refused or unseen repository needs, without any text of
// GitHub's.
func repositorySettingsFailure(err error, message string) error {
	var failure *provider.Error
	if errors.As(err, &failure) && (failure.Class == provider.ClassPermission || failure.Class == provider.ClassNotFound) {
		refused := *failure
		refused.Message = message
		return &refused
	}
	return err
}

func (c *Client) repositorySettings(ctx context.Context) (*RepositorySettings, error) {
	const op = "get repository settings"
	var answer repositorySettingsAnswer
	path := "/repos/" + url.PathEscape(c.target.owner) + "/" + url.PathEscape(c.target.repo)
	if err := c.rest(ctx, op, path, &answer); err != nil {
		return nil, repositorySettingsFailure(err, repositorySettingsReadPermission)
	}
	if !answer.valid() {
		return nil, invalidResponse(op, false)
	}
	return &RepositorySettings{DefaultBranch: answer.DefaultBranch, Visibility: answer.Visibility,
		Archived: answer.Archived, MergeSettings: answer.MergeSettings}, nil
}

// updateRepositorySettings sends the given settings, and only those, in one request that is never repeated.
// The answer reports what GitHub holds now; the result names the settings this call gave, with those values.
func (c *Client) updateRepositorySettings(ctx context.Context, a *actionsArguments) (*MergeSettings, error) {
	const op = "update repository settings"
	body := map[string]any{}
	for name, value := range map[string]*bool{"allow_merge_commit": a.AllowMergeCommit,
		"allow_squash_merge": a.AllowSquashMerge, "allow_rebase_merge": a.AllowRebaseMerge,
		"allow_auto_merge": a.AllowAutoMerge, "allow_update_branch": a.AllowUpdateBranch,
		"delete_branch_on_merge": a.DeleteBranchOnMerge, "web_commit_signoff_required": a.WebCommitSignoffRequired} {
		if value != nil {
			body[name] = *value
		}
	}
	for name, value := range map[string]string{"squash_merge_commit_title": a.SquashMergeCommitTitle,
		"squash_merge_commit_message": a.SquashMergeCommitMessage, "merge_commit_title": a.MergeCommitTitle,
		"merge_commit_message": a.MergeCommitMessage} {
		if value != "" {
			body[name] = value
		}
	}
	var answer repositorySettingsAnswer
	path := "/repos/" + url.PathEscape(c.target.owner) + "/" + url.PathEscape(c.target.repo)
	if err := c.restChange(ctx, op, http.MethodPatch, path, body, &answer); err != nil {
		return nil, repositorySettingsFailure(err, repositorySettingsChangePermission)
	}
	// Only a setting this call gave is reported, and GitHub must have reported it as well.
	result := &MergeSettings{}
	for _, field := range []struct {
		given, answered **bool
		sent            bool
	}{
		{&result.AllowMergeCommit, &answer.AllowMergeCommit, a.AllowMergeCommit != nil},
		{&result.AllowSquashMerge, &answer.AllowSquashMerge, a.AllowSquashMerge != nil},
		{&result.AllowRebaseMerge, &answer.AllowRebaseMerge, a.AllowRebaseMerge != nil},
		{&result.AllowAutoMerge, &answer.AllowAutoMerge, a.AllowAutoMerge != nil},
		{&result.AllowUpdateBranch, &answer.AllowUpdateBranch, a.AllowUpdateBranch != nil},
		{&result.DeleteBranchOnMerge, &answer.DeleteBranchOnMerge, a.DeleteBranchOnMerge != nil},
		{&result.WebCommitSignoffRequired, &answer.WebCommitSignoffRequired, a.WebCommitSignoffRequired != nil},
	} {
		if !field.sent {
			continue
		}
		if *field.answered == nil {
			return nil, invalidResponse(op, true)
		}
		*field.given = *field.answered
	}
	for _, field := range []struct {
		given, answered *string
		allowed         []string
		sent            string
	}{
		{&result.SquashMergeCommitTitle, &answer.SquashMergeCommitTitle, squashTitleValues, a.SquashMergeCommitTitle},
		{&result.SquashMergeCommitMessage, &answer.SquashMergeCommitMessage, squashMessageValues, a.SquashMergeCommitMessage},
		{&result.MergeCommitTitle, &answer.MergeCommitTitle, mergeTitleValues, a.MergeCommitTitle},
		{&result.MergeCommitMessage, &answer.MergeCommitMessage, mergeMessageValues, a.MergeCommitMessage},
	} {
		if field.sent == "" {
			continue
		}
		if !contains(field.allowed, *field.answered) {
			return nil, invalidResponse(op, true)
		}
		*field.given = *field.answered
	}
	return result, nil
}
