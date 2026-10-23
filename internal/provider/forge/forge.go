// Package forge implements controlled access to self-hosted Forgejo and Gitea instances through their REST API
// below /api/v1. Both products share this package: an operation is implemented once and registered for each
// product that has it, under that product's own provider ID (forgejo or gitea). The fields and filters follow
// the API descriptions of Forgejo 15 and 17 and of Gitea 1.27 and 28, read from their swagger files, not
// against a live instance.
//
// A connection names its instance as https://HOST, without a path, and an access token that is sent as
// "Authorization: token ...". Nothing else authenticates a request: no basic authentication, no token in the
// query, no one-time password header, and never the Sudo header or parameter. Every handler names a fixed
// method and path; no agent argument chooses a path, a URL, a header, or a body. Redirects are never followed.
//
// The targets of a connection (users/NAME, orgs/NAME, repos/OWNER/REPO) are an optional allow-list the token
// stays an upper bound for. An argument outside them is refused before the credential is resolved and before
// any request is sent, and the refusal never names it. What the API answers is checked against the targets
// again by its canonical names, because renamed owners and repositories still answer under their old ones;
// list rows outside the targets are dropped.
//
// Every value of a response is provider data and untrusted: a response is read up to a size limit, strings
// are bounded, and provider texts never reach an error message.
package forge

import (
	"net/http"
	"slices"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
)

// roleToken is the single secret role: a Forgejo or Gitea access token.
const roleToken = "token"

// Bounds of one request and of the values it returns.
const (
	defaultTimeout   = 30 * time.Second
	maxResponseBytes = 4 << 20
	maxStringLength  = 2048
	apiPrefix        = "/api/v1"
)

// minInterval spaces the requests that share one token. Neither product documents an API limit; public
// instances limit by policy, Codeberg was seen announcing 2000 requests per 600 seconds. Two requests a second
// stay well below that.
const minInterval = 500 * time.Millisecond

// maxHold bounds how long a Retry-After of one answer may delay the next request of this process.
const maxHold = time.Minute

var limiters = ratelimit.NewRegistry(minInterval)

// transport carries every request. A nil value is Go's default transport; the package's tests replace it.
var transport http.RoundTripper

// product is one of the two products this package serves. Its ID is the provider ID and the first segment of
// its tool IDs; minimum is the oldest version a connection test accepts.
type product struct {
	id          string
	name        string
	description string
	minimum     version
}

var (
	forgejo = &product{id: "forgejo", name: "Forgejo", minimum: version{15, 0, 0},
		description: "Self-hosted Forgejo software forge, accounts and repositories through an access token"}
	gitea = &product{id: "gitea", name: "Gitea", minimum: version{1, 27, 0},
		description: "Self-hosted Gitea software forge, accounts and repositories through an access token"}
	products = []*product{forgejo, gitea}
)

// Register adds both providers: their metadata, connection tests, target browsers, and operations.
func Register(reg *capability.Registry) error {
	for _, p := range products {
		if err := register(reg, p); err != nil {
			return err
		}
	}
	return nil
}

func register(reg *capability.Registry, p *product) error {
	if err := reg.RegisterProvider(metadata(p), p.testConnection); err != nil {
		return err
	}
	if err := reg.RegisterTargetBrowser(p.id, capability.TargetBrowser{List: p.browseList, ResolveURL: p.browseResolveURL}); err != nil {
		return err
	}
	var operations []capability.Operation
	for _, t := range tools {
		if _, ok := t.since[p.id]; ok {
			operations = append(operations, capability.Operation{Descriptor: t.descriptor(p), Handler: t.handler(p)})
		}
	}
	return reg.Register(p.id, operations...)
}

func metadata(p *product) config.ProviderMetadata {
	return config.ProviderMetadata{
		ID: p.id, Name: p.name, Description: p.description,
		ValidateBaseURL: func(raw string) error {
			_, err := originOf(raw)
			return err
		},
		DefaultEffects: []config.Effect{config.EffectRead},
		SecretRoles: []config.SecretRole{{
			Name: roleToken,
			Description: p.name + " access token, created under the account's applications settings and sent as " +
				"'Authorization: token ...'; reading the account needs read:user, reading repositories " +
				"read:repository, listing a user's repositories or the token's own also read:user, and listing an " +
				"organization's repositories or choosing an organization as a target read:organization" +
				tokenNote(p),
		}},
		Target: config.TargetMetadata{
			Label:    "users, organizations, and repositories",
			Multiple: true,
			Description: "optional allow-list of users/NAME and orgs/NAME, which allow everything the user or " +
				"organization owns, and repos/OWNER/REPO for one repository; without targets a connection reaches " +
				"whatever its token reaches",
			Kinds: []config.TargetKind{{
				Name: "owner", Description: "a user or an organization and everything it owns",
				Forms: []string{"users/NAME", "orgs/NAME"},
			}, {
				Name: "repository", Description: "one repository",
				Forms: []string{"repos/OWNER/REPO"}, Parents: []string{"owner"}, Top: true,
			}},
			Validate: func(raw string) error {
				_, err := parseTarget(raw)
				return err
			},
			ValidateSet: func(values []string) error {
				_, err := parseAllowlist(values)
				return err
			},
			Reaches: reaches(p),
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read the account and repositories", Recommended: true,
			Description: "reads the account behind the token, lists and reads repositories, and reads the " +
				"instance settings and version; changes nothing",
			Tools: []string{p.toolID(accountsMe), p.toolID(repositoriesList), p.toolID(repositoriesGet),
				p.toolID(settingsGet), p.toolID(versionGet)},
		}, {
			ID: "actions-observer", Title: "Actions observer",
			Description: "reads the workflow runs, attempts, jobs, tasks, workflows, artifact metadata, the end of " +
				"job logs, and downloads the log archive of a run and the archive of an artifact of a repository " +
				"and starts nothing",
			Tools: p.profileTools(actionsObserverTools),
		}, {
			ID: "actions-operator", Title: "Actions operator",
			Description: "observes the Actions of a repository and dispatches workflows, re-runs runs or jobs, " +
				"cancels and force-cancels runs, and approves waiting runs; every execution needs its own " +
				"confirmation, and deleting runs or artifacts stays unticked, since it is offered only where a " +
				"connection's tools list names it",
			Tools: p.profileTools(append(slices.Clone(actionsObserverTools), actionsOperatorTools...)),
		}},
	}
}

// profileTools keeps the tools the product registers, in the given order.
func (p *product) profileTools(list []*tool) []string {
	var ids []string
	for _, t := range list {
		if _, ok := t.since[p.id]; ok {
			ids = append(ids, p.toolID(t))
		}
	}
	return ids
}

// The Actions tools of the two setup profiles: every reading tool, and the tools that start or stop work. The
// deleting tools are in neither.
var (
	actionsObserverTools = []*tool{workflowRunsList, workflowRunsGet, workflowRunAttemptsGet,
		workflowRunAttemptsListJobs, workflowRunLogsGet, workflowJobsList, workflowJobsGet, workflowJobLogsGet,
		actionsTasksList, workflowArtifactsList, workflowArtifactsGet, workflowArtifactsDownload, workflowsList,
		workflowsGet}
	actionsOperatorTools = []*tool{workflowRunsCancel, workflowRunsForceCancel, workflowRunsRerun,
		workflowRunsRerunFailed, workflowRunsApprove, workflowJobsRerun, workflowsDispatch}
)

// tokenNote adds what only one product's tokens can do.
func tokenNote(p *product) string {
	if p == forgejo {
		return "; Forgejo requires at least one scope on every token, also for public repositories, and a token " +
			"bound to single repositories narrows it further"
	}
	return ""
}
