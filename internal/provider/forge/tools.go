package forge

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// tool is one operation, implemented once for both products. since names, per product ID, the oldest version
// that has the operation; a product without an entry does not register it. describe builds the descriptor
// without its ID and provider. prepare checks the arguments against the targets before anything is resolved or
// sent and returns what the operation then does with an open client.
type tool struct {
	group    string
	action   string
	since    map[string]version
	describe func(p *product) capability.Descriptor
	prepare  func(p *product, allowed allowlist, raw json.RawMessage) (run, error)
	// needs names a newer version that arguments of the call require, for an argument only later versions of a
	// product know; nil when the arguments never change the version.
	needs func(p *product, raw json.RawMessage) (version, bool)
	// local replaces prepare for a tool that reads or writes a local file: it also opens that file inside the
	// directories the connection releases, before the token is resolved, and returns what releases the file
	// once the operation is over.
	local func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error)
	// forward replaces prepare for a tool that sends the value of a forward credential the connection releases:
	// it also checks that credential's form before the token is resolved, and the operation reads the value
	// through the resolver only once the core has confirmed the call.
	forward func(p *product, allowed allowlist, resolved *config.Resolved, secrets *secret.Resolver,
		raw json.RawMessage) (run, error)
}

type run func(ctx context.Context, c *Client) (any, error)

// everywhere is the since of an operation both products have at their supported minimum.
var everywhere = map[string]version{forgejo.id: forgejo.minimum, gitea.id: gitea.minimum}

var tools = []*tool{accountsMe, repositoriesList, repositoriesGet, settingsGet, versionGet, nodeInfoGet, signingKeysGet,
	markdownRender, markupRender, issuesList, issuesSearch, issuesGet, issuesCreate, issuesUpdate, issuesClose,
	issuesReopen, issuesDelete, issueDeadlinesSet, issueTimelineList, tagsList, tagsGet, tagsCreate, tagsDelete,
	issueTemplatesList, issueConfigGet, issueConfigValidate, commentsList, commentsListRepository, commentsGet,
	commentsCreate, commentsUpdate, commentsDelete, assigneesList, assigneesCheck, issueAssigneesAdd,
	issueAssigneesRemove, issueAssigneesCheck, labelsList, labelsGet, labelsCreate, labelsUpdate, labelsDelete,
	orgLabelsList, orgLabelsGet, orgLabelsCreate, orgLabelsUpdate, orgLabelsDelete, issueLabelsList, issueLabelsAdd,
	issueLabelsReplace, issueLabelsRemove, issueLabelsClear, milestonesList, milestonesGet, milestonesCreate,
	milestonesUpdate, milestonesDelete, pullRequestsList, pullRequestsGet, pullRequestsGetByBranches,
	pullRequestsListPinned, pullRequestsCheckMerged, commitPullRequestsGet, pullRequestFilesList,
	pullRequestCommitsList, pullRequestDiffsGet, releasesList, releasesGet, releasesGetLatest, releasesGetByTag,
	releasesCreate, releasesUpdate, releasesDelete, releasesDeleteByTag, pullRequestsCreate, pullRequestsUpdate,
	pullRequestsClose, pullRequestsReopen, pullRequestsCancelAutoMerge, pullRequestsMerge, pullRequestBranchesUpdate,
	releaseAssetsList, releaseAssetsGet, releaseAssetsDownload, releaseAssetsUpload, releaseAssetsUpdate,
	releaseAssetsDelete, issueReactionsList, issueReactionsAdd, issueReactionsRemove, commentReactionsList,
	commentReactionsAdd, commentReactionsRemove, issueTimesList, issueTimesAdd, issueTimesDelete, issueTimesReset,
	issueStopwatchesStart, issueStopwatchesStop, issueStopwatchesDelete, repositoryTimesList, userTimesList,
	myTimesList, myStopwatchesList, issueDependenciesList, issueDependenciesAdd, issueDependenciesRemove,
	issueBlocksList, issueBlocksAdd, issueBlocksRemove, issueAttachmentsList, issueAttachmentsGet,
	issueAttachmentsUpload, issueAttachmentsUpdate, issueAttachmentsDelete, issueAttachmentsDownload,
	commentAttachmentsList, commentAttachmentsGet, commentAttachmentsUpload, commentAttachmentsUpdate,
	commentAttachmentsDelete, commentAttachmentsDownload, issuePinsList, issuePinsCheckAllowed, issuePinsPin,
	issuePinsUnpin, issuePinsMove, projectsList, projectsGet, projectsCreate, projectsUpdate, projectsDelete,
	projectColumnsList, projectColumnsGet, projectColumnsCreate, projectColumnsUpdate, projectColumnsDelete,
	projectColumnsMove, projectColumnsSetDefault, projectIssuesList, projectIssuesAdd, projectIssuesRemove,
	projectIssuesMove, issueSubscriptionsList, issueSubscriptionsCheck, issueSubscriptionsAdd,
	issueSubscriptionsRemove, starsList, starsCheck, starsAdd, starsRemove, repositoryStargazersList,
	repositorySubscribersList, repositorySubscriptionsGet, repositorySubscriptionsSet, repositorySubscriptionsDelete,
	subscriptionsList, issueLocksLock, issueLocksUnlock, branchesList, branchesGet, branchesCreate,
	branchesRename, branchesUpdateRef, branchesDelete, reviewsList, reviewsGet, reviewsCreate, reviewsSubmit,
	reviewsApprove, reviewsDelete, reviewsDismiss, reviewsUndismiss, reviewCommentsList, reviewCommentsGet,
	reviewCommentsCreate, reviewCommentsDelete, reviewCommentsReply, reviewCommentsResolve,
	reviewCommentsUnresolve, workflowRunsList, workflowRunsGet, workflowRunsDelete, workflowRunsCancel,
	workflowRunsForceCancel, workflowRunsRerun, workflowRunsRerunFailed, workflowRunsApprove,
	workflowRunAttemptsGet, workflowRunAttemptsListJobs, workflowRunLogsGet, workflowJobsList, workflowJobsGet,
	workflowJobsRerun, workflowJobLogsGet, actionsTasksList, commitsList, commitsGet, commitDiffsGet, compareGet,
	commitStatusesList, commitStatusesGetCombined, commitStatusesCreate, gitBlobsGet, gitBlobsList, gitTreesGet,
	gitRefsList, gitTagsGet, gitNotesGet, gitNotesSet, gitNotesDelete, contentsList, contentsGet, contentsGetExt,
	contentsGetMany, contentsGetRaw, archivesGet, editorconfigGet, reviewersList, reviewersRequest,
	reviewersRemove, workflowArtifactsList, workflowArtifactsGet, workflowArtifactsDownload,
	workflowArtifactsDelete, workflowsList, workflowsGet, workflowsDispatch, contentsCreate, contentsUpdate,
	contentsChangeFiles, contentsApplyDiff, contentsDelete, repositoriesSearch, repositoryLanguagesList,
	repositoryActivitiesList, repositorySigningKeysGet, actionsSecretsList, actionsSecretsSet,
	actionsSecretsDelete, actionsVariablesList, actionsVariablesGet, actionsVariablesCreate,
	actionsVariablesUpdate, actionsVariablesDelete, actionsRunnersList, actionsRunnersGet, actionsRunnersUpdate,
	actionsRunnersDelete, actionsRunnerJobsSearch, workflowsEnable, workflowsDisable, repositoriesCreate,
	repositoriesUpdate, repositoriesFork, repositoriesListForks, repositoriesGenerate, repositoriesSyncFork,
	repositoriesGetSyncState, repositoriesConvert, repositoriesDelete, repositoriesTransfer,
	repositoriesAcceptTransfer, repositoriesRejectTransfer, repositoryMirrorsSync, repositoryAvatarsUpdate,
	repositoryAvatarsDelete, topicsSearch, repositoryTopicsList, repositoryTopicsSet,
	repositoryTopicsAdd, repositoryTopicsRemove, gitignoreTemplatesList, gitignoreTemplatesGet, labelTemplatesList,
	labelTemplatesGet, licenseTemplatesList, licenseTemplatesGet, repositoryLicensesList, wikiPagesList,
	wikiPagesGet, wikiPagesCreate, wikiPagesUpdate, wikiPagesDelete, wikiRevisionsList, orgsList,
	orgsListMine, orgsListUser, orgsGet, orgsCreate, orgsUpdate, orgsRename, orgsDelete, orgAvatarsUpdate,
	orgAvatarsDelete, orgActivitiesList, orgPermissionsGet, orgMembersList, orgMembersCheck, orgMembersRemove,
	orgPublicMembersList, orgPublicMembersCheck, orgPublicMembersPublicize, orgPublicMembersConceal, orgBlocksList,
	orgBlocksCheck, orgBlocksAdd, orgBlocksRemove, teamsList, teamsSearch, teamsGet, teamsCreate, teamsUpdate,
	teamsDelete, teamActivitiesList, teamMembersList, teamMembersCheck, teamMembersAdd, teamMembersRemove,
	teamRepositoriesList, teamRepositoriesCheck, teamRepositoriesAdd, teamRepositoriesRemove, repositoryTeamsList,
	repositoryTeamsCheck, repositoryTeamsAdd, repositoryTeamsRemove, usersSearch, usersGet, userActivitiesList,
	userHeatmapGet, userFollowersList, userFollowingList, userFollowingCheck, userFollowsAdd, userFollowsRemove, userSettingsGet,
	userSettingsUpdate, userAvatarsUpdate, userAvatarsDelete, userEmailsList, userEmailsDelete, userBlocksList,
	userBlocksCheck, userBlocksAdd, userBlocksRemove, webhooksList, webhooksGet, webhooksDelete, gitHooksList,
	gitHooksGet, gitHooksDelete, pushMirrorsList, pushMirrorsGet, pushMirrorsSync, pushMirrorsDelete,
	deployKeysList, deployKeysGet, deployKeysDelete, userKeysList, userKeysGet, userKeysDelete, userGPGKeysList,
	userGPGKeysGet, userGPGKeysDelete, accessTokensList, accessTokensGetCurrent, accessTokensDeleteCurrent,
	oauth2AppsList, oauth2AppsGet, oauth2AppsDelete, packagesList, packagesGet, packageFilesList,
	packageVersionsList, packageVersionsGetLatest, packagesLink, packagesUnlink, packagesDeleteVersion, packagesDelete}

func (p *product) toolID(t *tool) string { return p.id + "." + t.group + "." + t.action }

func (t *tool) descriptor(p *product) capability.Descriptor {
	d := t.describe(p)
	d.ID, d.Provider, d.Version = p.toolID(t), p.id, 1
	return d
}

// handler runs one operation in a fixed order: targets and arguments, then a local file, then the version the
// operation needs, then the token, then the operation's own requests. A refusal of the first three never
// resolves the token, and a target refusal never sends a request.
func (t *tool) handler(p *product) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		if resolved == nil {
			return nil, provider.Fail("open", "no connection was selected")
		}
		if _, err := originOf(resolved.BaseURL); err != nil {
			return nil, provider.Fail("open", err.Error())
		}
		allowed, err := allowlistOf(resolved)
		if err != nil {
			return nil, err
		}
		var operation run
		if t.local != nil {
			prepared, release, err := t.local(ctx, p, allowed, resolved, raw)
			if err != nil {
				return nil, err
			}
			defer release()
			operation = prepared
		} else if t.forward != nil {
			if operation, err = t.forward(p, allowed, resolved, secrets, raw); err != nil {
				return nil, err
			}
		} else if operation, err = t.prepare(p, allowed, raw); err != nil {
			return nil, err
		}
		needed := t.since[p.id]
		if t.needs != nil {
			if extra, ok := t.needs(p, raw); ok && needed.less(extra) {
				needed = extra
			}
		}
		if p.minimum.less(needed) {
			if err := requireVersion(ctx, p, resolved, secrets, red, t.group+" "+t.action, needed); err != nil {
				return nil, err
			}
		}
		client, err := open(ctx, p, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return operation(context.WithValue(ctx, redactorKey{}, red), client)
	}
}

// decodeArguments reads the arguments into out; the input schema has already been applied.
func decodeArguments(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if json.Unmarshal(raw, out) != nil {
		return invalidArgument("the arguments could not be read")
	}
	return nil
}

func readRisk(sensitivity string) capability.Risk {
	return capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: sensitivity}
}

// changeRisk is the risk of every change: it needs confirmation, and the instance is open to its users.
func changeRisk(effect capability.Effect, idempotency capability.Idempotency, sensitivity string) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: sensitivity}
}

func inputSchema(properties string, required ...string) json.RawMessage {
	names := make([]string, len(required))
	for i, name := range required {
		names[i] = strconv.Quote(name)
	}
	return json.RawMessage(`{"type":"object","properties":{` + properties + `},"required":[` +
		strings.Join(names, ",") + `],"additionalProperties":false}`)
}

// escape is one path segment of a validated name.
func escape(segment string) string { return url.PathEscape(segment) }
