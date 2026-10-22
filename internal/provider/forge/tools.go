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
}

type run func(ctx context.Context, c *Client) (any, error)

// everywhere is the since of an operation both products have at their supported minimum.
var everywhere = map[string]version{forgejo.id: forgejo.minimum, gitea.id: gitea.minimum}

var tools = []*tool{accountsMe, repositoriesList, repositoriesGet, settingsGet, versionGet, nodeInfoGet, signingKeysGet,
	markdownRender, markupRender, issuesList, issuesSearch, issuesGet, issuesCreate, issuesUpdate, issuesClose,
	issuesReopen, issuesDelete, issueDeadlinesSet, issueTimelineList, issueTemplatesList, issueConfigGet,
	issueConfigValidate, commentsList, commentsListRepository, commentsGet, commentsCreate, commentsUpdate,
	commentsDelete, assigneesList, assigneesCheck, issueAssigneesAdd, issueAssigneesRemove, issueAssigneesCheck,
	labelsList, labelsGet, labelsCreate, labelsUpdate, labelsDelete, orgLabelsList, orgLabelsGet, orgLabelsCreate,
	orgLabelsUpdate, orgLabelsDelete, issueLabelsList, issueLabelsAdd, issueLabelsReplace, issueLabelsRemove,
	issueLabelsClear, milestonesList, milestonesGet, milestonesCreate, milestonesUpdate, milestonesDelete,
	pullRequestsList, pullRequestsGet, pullRequestsGetByBranches, pullRequestsListPinned, pullRequestsCheckMerged,
	commitPullRequestsGet, pullRequestFilesList, pullRequestCommitsList, pullRequestDiffsGet,
	pullRequestsCreate, pullRequestsUpdate, pullRequestsClose, pullRequestsReopen, pullRequestsCancelAutoMerge,
	pullRequestsMerge, pullRequestBranchesUpdate}

func (p *product) toolID(t *tool) string { return p.id + "." + t.group + "." + t.action }

func (t *tool) descriptor(p *product) capability.Descriptor {
	d := t.describe(p)
	d.ID, d.Provider, d.Version = p.toolID(t), p.id, 1
	return d
}

// handler runs one operation in a fixed order: targets and arguments, then the version the operation needs,
// then the token, then the operation's own requests. A refusal of the first two never resolves the token, and
// a target refusal never sends a request.
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
		operation, err := t.prepare(p, allowed, raw)
		if err != nil {
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
		return operation(ctx, client)
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
