// Package n8n implements controlled access to one n8n instance (n8n Cloud or self-hosted) over its Public
// API (https://docs.n8n.io/api/api-reference/, packages/cli/src/public-api/v1 of n8n-io/n8n): it lists and
// reads workflows and executions, including a bounded, filtered view of a failed execution's error, creates
// and replaces workflows, activates and deactivates them, and retries and stops executions. There is no tool
// to start a workflow: the Public API documents no endpoint for it. There is also no tool to delete a
// workflow or an execution, and no archive, unarchive, publish, unpublish, transfer, or test-run action, and
// no credential, user, tag, variable, project, or data table management; those are deliberately left to a
// later milestone, which this package's Client, error classes, and target model are built to extend without
// a rewrite.
//
// A connection binds one n8n instance, through its configured base URL, and one Public API key
// (X-N8N-API-KEY header, see docs/connect/n8n-api/authentication.md), plus, optionally, an allow-list of
// projects (project/PROJECT_ID, repeatable) and, independently, an allow-list of workflows
// (workflow/WORKFLOW_ID, repeatable) of that instance. Neither allow-list is required: named connections
// already separate customer instances by base URL and API key, unlike Infomaniak's kDrive and kChat
// providers, where one token can reach every account or team its owner administers. The allow-lists exist
// for the narrower case this API key's own instance still spans more than the caller should see: n8n's
// Projects feature (Enterprise) can hold several customers' or teams' workflows under one instance and one
// administrative key, exactly the multi-tenant situation a service provider holding several customers'
// credentials has to keep apart.
//
// A workflow_id argument outside a configured workflow allow-list is refused locally, before any request is
// sent. A configured project allow-list is checked live, against the instance's own answer, never against
// local configuration alone: the workflowPublicDto schema reports a workflow's project membership through
// its "shared" array (role, projectId, project), and workflows.list already receives that array in the same
// response it lists with, so a project allow-list is enforced there without any extra request; workflows.get
// re-checks it against the same response it reads; and executions.get, whose Execution resource carries only
// a workflowId and no project information of its own (per the getExecution/getExecutions schemas), fetches
// that workflow once more to learn its projects before any execution content is returned. An n8n instance or
// version that does not report "shared" at all (an older Public API, or a non-Enterprise instance without
// the Projects feature) cannot prove membership; a project-restricted connection then fails closed rather
// than guessing, and says so. executions.list applies the same reasoning: since a listed execution carries no
// project of its own, a project-restricted connection can only offer it non-emptied of foreign workflows when
// given an explicit workflow_id argument to verify once for the whole page; without one it refuses instead of
// showing every execution of the instance's other projects.
//
// A workflow read never returns a credential's value: n8n's own API does not put one in a workflow response
// either, only the credential's id and name per node (workflowPublicDto's node.credentials), which is how
// this provider passes it on, unmodified, and it is what a person editing the workflow already sees in the
// n8n editor. An execution read never returns its run data: the base execution resource already excludes it
// (includeData=false), and executions.get additionally fetches with includeData=true only when the base
// answer reports status "error" or "crashed", and only to extract the result error's message and the node it
// occurred on (data.resultData.error and data.resultData.lastNodeExecuted); every other part of that
// includeData answer, in particular the per-node run data other n8n API consumers use for replay or
// debugging, is read only far enough to find the error and is never part of the output. workflowData on an
// execution (a snapshot of the whole workflow at run time) is dropped the same way for the same reason.
//
// A base URL must use https. n8n does not document local-only http support the way this codebase's generic
// configuration validator leaves http open for a local test server; every other provider here that can also
// be self-hosted (GitHub Enterprise Server, Nextcloud, SeaTable, Twenty) requires https unconditionally
// rather than trusting a locally reachable http origin, and this provider follows that same rule for a self-
// hosted n8n instance, cloud or not.
//
// Node parameters, tag names, workflow and project names, and every other value a listing or a read answers
// with arrive from the n8n instance and are treated as untrusted data: normalised into a stable envelope,
// passed through the output encoders, and never rendered, executed, or stored.
//
// Every tool that changes something needs its own confirmation, sends exactly one changing request, and is
// never retried by this provider: a failure that could mean the request nonetheless reached n8n (a timeout,
// a connection reset, or a 5xx) is reported as uncertain instead, see (*Client).do. workflows.create refuses
// outright on a connection restricted by a workflow allow-list, since a workflow that does not exist yet can
// never already be on that list, and requires an explicit, allow-listed project_id on a connection restricted
// by a project allow-list, since n8n's own createWorkflow schema does let a caller steer the target project.
// Both workflows.create and workflows.update re-read the resulting workflow after their one changing request
// and re-apply the project allow-list to it; a mismatch is reported as a provider error, not an invalid
// request, because the change has already happened and this milestone has no delete tool to undo it.
// workflows.update is a full PUT replacement of name, nodes, connections, and settings, exactly as n8n's own
// updateWorkflow endpoint is; it never changes the active state, which workflows.activate and
// workflows.deactivate own instead.
package n8n

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "n8n"

// roleAPIKey is the single secret role an n8n credential must supply. It is sent as the X-N8N-API-KEY
// header, never as a bearer token: that is the one authentication scheme the Public API documents for a
// programmatic client (docs/connect/n8n-api/authentication.md), the other two, a session cookie and a JWT
// bearer token, both belong to the browser-facing editor, not to this provider.
const roleAPIKey = "api-key"

// apiPath is the fixed Public API root below the configured instance origin.
const apiPath = "/api/v1"

// dataSensitivity classifies results as workflow and execution data of the configured n8n instance.
const dataSensitivity = "n8n-workflows"

// Bounds of one request and one answer.
const (
	// maxResponseBytes bounds every answer this provider reads without includeData, and the base
	// executions.get answer before it decides whether a second, includeData=true request is needed.
	maxResponseBytes = 4 << 20
	// maxErrorResponseBytes bounds the one includeData=true request executions.get may send. n8n's own
	// server-side size limit for included execution data is far larger and configurable per instance, so
	// this is a local ceiling on what this process reads to find one error message and node name, not a
	// belief about what the instance would send: a response that exceeds it is refused rather than parsed
	// partially, and the base detail is still returned without the error fields.
	maxErrorResponseBytes = 16 << 20
	defaultTimeout        = 30 * time.Second
	maxCursorLength       = 2048
	// defaultListLimit and maxListLimit mirror the Public API's own "limit" query parameter (default 100,
	// maximum 250; shared/spec/parameters/limit.yml), so a caller who omits it gets the same page size n8n
	// itself defaults to.
	defaultListLimit = 100
	maxListLimit     = 250
	// maxWorkflowWriteBytes bounds the marshaled request body of workflows.create and workflows.update. It
	// is a local ceiling on what this provider ever sends, not a belief about what the instance would
	// accept; a caller with a larger workflow definition is refused before any request is built.
	maxWorkflowWriteBytes = 4 << 20
	// maxWorkflowNodes bounds how many nodes a single create or update may define, well above what a real
	// workflow needs, so a malformed or excessive payload fails fast on its shape alone.
	maxWorkflowNodes = 1000
)

// limiters holds the rate-limit budget of every API key this process has used. n8n documents no fixed
// request budget for the Public API, of a self-hosted instance or of Cloud, so this provider applies no
// proactive spacing of its own; a 429 the instance itself reports is still classified and, when it names a
// Retry-After, holds this connection's own limiter before the next request, exactly as the kChat provider
// does for the same reason.
var limiters = ratelimit.NewRegistry(0)

// Client binds one n8n Public API key to the instance origin and the project/workflow scope of its
// connection.
type Client struct {
	scope   scope
	origin  string
	apiKey  string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// Open resolves the API key of one selected connection and returns a client for its bound instance and
// scope.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

// open is the internal seam. A caller may supply the rate limiter, and the package's own tests replace the
// transport, so no test ever reaches a real n8n instance.
func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	const op = "open"
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	origin, err := parseInstance(resolved.BaseURL)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleAPIKey)
	if err != nil {
		return nil, err
	}
	if !validAPIKey(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: op, Message: "the n8n API key is unusable"}
	}
	if red != nil {
		red.Add(value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{scope: bound, origin: origin, apiKey: value.Secret, http: newHTTPClient(), limiter: lim}, nil
}

// instanceReason is the one refusal message of a malformed base URL. It never quotes the value that was
// refused.
const instanceReason = "an n8n service needs a usable https URL, without user, query, or fragment"

// parseInstance validates the configured base URL of one n8n instance: a plain https origin, optionally
// below an installation path, with no port restriction, user, query, or fragment. n8n Cloud instances live
// at https://NAME.app.n8n.cloud; a self-hosted instance may be reverse-proxied below an arbitrary path
// (n8n's own N8N_PATH setting), so, unlike the kChat provider's single fixed domain, no host shape is
// enforced beyond https. The Public API root is this origin plus the fixed apiPath.
func parseInstance(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New(instanceReason)
	}
	if parsed.Scheme != "https" {
		return "", errors.New(instanceReason)
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Opaque != "" {
		return "", errors.New(instanceReason)
	}
	origin := parsed.Scheme + "://" + parsed.Host + strings.TrimSuffix(parsed.Path, "/")
	return origin, nil
}

// transport carries every n8n request. A nil value is Go's default transport; the package's own tests
// replace it with a local fake.
var transport http.RoundTripper

// newHTTPClient is used for every request this provider sends. The API key travels in a header, and no
// endpoint this provider calls is documented to redirect, so none is followed: a redirect here could only be
// a mistake or an exfiltration route to a host this connection was never bound to.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultTimeout,
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// get sends one bounded GET below the Public API root and decodes its body into out. maxBytes bounds how
// much of the answer this process reads before giving up; every read of this provider uses maxResponseBytes
// except the one includeData=true request executions.get may send, which uses maxErrorResponseBytes.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any, maxBytes int) error {
	return c.do(ctx, op, http.MethodGet, path, query, nil, out, maxBytes, false)
}

// uncertain is appended to a failure of a change request whose request may have reached n8n: the change may
// have taken effect although no confirmation ever arrived. Qatlas never repeats such a request by itself; a
// caller is told to read the current state before deciding whether to try again.
const uncertain = "; this change may have taken effect, read the current state before repeating it"

// change sends one bounded, state-changing request below the Public API root, once, with a JSON body when
// body is not nil, and decodes the answer into out when out is not nil. It is never retried by this
// provider; every failure that could mean the request nonetheless reached n8n is marked uncertain instead.
func (c *Client) change(ctx context.Context, op, method, path string, query url.Values, body, out any,
	maxBytes int) error {
	return c.do(ctx, op, method, path, query, body, out, maxBytes, true)
}

// do sends one bounded request below the Public API root, with a JSON body when body is not nil, and
// decodes the answer into out when out is not nil. changing marks a request that may change n8n's state:
// every failure of it that could mean the request nonetheless arrived is marked uncertain, so this provider
// never repeats it by itself, the same contract infomaniakchat and todoist give their own change requests.
func (c *Client) do(ctx context.Context, op, method, path string, query url.Values, body, out any, maxBytes int,
	changing bool) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "n8n", err)
	}
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return providerError(op, "the request could not be built")
		}
		payload = encoded
	}
	endpoint := c.origin + apiPath + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("X-N8N-API-KEY", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(op, "n8n", err)
		if changing && (failure.Class == provider.ClassTimeout || failure.Cause == provider.CauseConnectionReset ||
			failure.Cause == provider.CauseUnknown) {
			failure.Message += uncertain
		}
		return failure
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.statusError(op, response)
		if changing && response.StatusCode >= 500 {
			failure.Message += uncertain
		}
		return failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1))
	if err != nil || len(data) > maxBytes {
		message := "the n8n response could not be read within the size limit"
		if changing {
			message += uncertain
		}
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		message := "n8n returned an invalid response"
		if changing {
			message += uncertain
		}
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	return nil
}

// statusError maps an HTTP status to a stable class. The provider body is never read into the message. It
// returns the concrete type, not the error interface, so (*Client).do can still append the uncertain
// suffix to its message for a change request that a 5xx may nonetheless have carried out.
func (c *Client) statusError(op string, response *http.Response) *provider.Error {
	status := response.StatusCode
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "n8n rejected the API key"}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this n8n API key may not perform this operation; check its scopes under Settings, n8n API"}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "n8n does not hold this resource, does not show it to this API key, or this instance's " +
				"Public API version does not have this endpoint"}
	case status == http.StatusTooManyRequests:
		c.limiter.HoldFor(retryAfter(response.Header))
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "n8n rate-limited the operation"}
	case status == http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "n8n is unavailable or in maintenance"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "n8n did not answer in time"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "n8n answered with a redirect, which Qatlas does not follow for this request"}
	default:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("n8n rejected the operation (HTTP %d)", status)}
	}
}

// retryAfter reads how long n8n asks a client to wait after a rate limit. Zero means n8n named no time, or
// an unusable one.
func retryAfter(header http.Header) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After")))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidResponse(op, message string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

// invalidRequest refuses a request the connection's own configuration, or a live scope check, decides
// against, before the matching content is ever returned. No message ever quotes the refused value.
func invalidRequest(message string) error {
	return &application.InvalidRequestError{Message: message}
}

// validAPIKey keeps an obviously unusable value out of a request header. The real check is n8n's own. A
// Public API key is a JWT: long, and built only of base64url characters and dots.
func validAPIKey(value string) bool {
	if len(value) < 16 || len(value) > 8192 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// bounded keeps an oversized provider string out of a result without interpreting it.
func bounded(value string) string {
	const maxValueLength = 2048
	if len(value) > maxValueLength {
		return value[:maxValueLength]
	}
	return value
}

// TestConnection performs the smallest safe authenticated read: one page of at most one workflow of the
// bound instance. It proves that the API key is accepted and that this instance serves the Public API; it
// says nothing about a project or workflow allow-list narrower than that, because every workflow and
// execution a connection's own operations name is checked again on its own request.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	const op = "test connection"
	var page workflowsPageJSON
	if err := client.get(ctx, op, "/workflows", url.Values{"limit": {"1"}}, &page, maxResponseBytes); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// Register adds n8n metadata, its read-only connection test, its read operations, and its confirmed
// workflow and execution changes.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "n8n", DefaultPermissions: []config.Permission{config.PermissionRead},
		Description: "Workflow automation platform, self-hosted or n8n Cloud, read and confirmed changes " +
			"through its Public API",
		SecretRoles: []config.SecretRole{{
			Name: roleAPIKey,
			Description: "n8n Public API key, created under Settings, n8n API, Create an API key; on an " +
				"Enterprise plan it may itself be scoped to a subset of resources and actions, which is a " +
				"further ceiling this connection's own targets and permissions narrow, never widen",
		}},
		Target: config.TargetMetadata{
			Label:    "projects and workflows",
			Multiple: true,
			Description: "optional, independent allow-lists of this instance's project/PROJECT_ID (Enterprise " +
				"Projects feature) and workflow/WORKFLOW_ID targets; without either, every workflow and " +
				"execution this API key can reach is reachable. A workflow_id argument outside a configured " +
				"workflow allow-list is refused locally, before any request is sent. A configured project " +
				"allow-list is checked against the instance's own report of a workflow's project membership, " +
				"never against local configuration alone; an instance or Public API version that does not " +
				"report it fails the check closed instead of guessing",
			Kinds: []config.TargetKind{{
				Name: "project",
				Description: "one project (Enterprise Projects feature) of the bound instance; optional, and " +
					"may be listed more than once; find its ID in the n8n editor's project settings URL or in " +
					"a workflow's own project membership",
				Forms: []string{"project/PROJECT_ID"},
			}, {
				Name:        "workflow",
				Description: "one workflow of the bound instance; optional, and may be listed more than once",
				Forms:       []string{"workflow/WORKFLOW_ID"},
			}},
			Validate:    validateTarget,
			ValidateSet: func(values []string) error { _, err := parseScope(values); return err },
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read workflows and executions", Recommended: true,
			Description: "lists and reads workflows, and lists and reads executions including a bounded " +
				"view of a failed execution's error; changes nothing and starts nothing",
			Tools: readTools,
		}, {
			ID: "manage", Title: "Manage workflows and executions",
			Description: "reads what the read profile reads, creates and replaces workflows, activates and " +
				"deactivates them, and retries and stops executions; every change needs its own confirmation. " +
				"There is no tool to start a workflow, delete a workflow, or delete an execution: the Public " +
				"API documents no endpoint for the first, and this milestone deliberately leaves out the other " +
				"two",
			Tools: append(append([]string{}, readTools...), workflowsCreate.ID, workflowsUpdate.ID,
				workflowsActivate.ID, workflowsDeactivate.ID, executionsRetry.ID, executionsStop.ID),
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: workflowsList, Handler: capability.Handler(invokeWorkflowsList)},
		capability.Operation{Descriptor: workflowsGet, Handler: capability.Handler(invokeWorkflowsGet)},
		capability.Operation{Descriptor: executionsList, Handler: capability.Handler(invokeExecutionsList)},
		capability.Operation{Descriptor: executionsGet, Handler: capability.Handler(invokeExecutionsGet)},
		capability.Operation{Descriptor: workflowsCreate, Handler: capability.Handler(invokeWorkflowsCreate)},
		capability.Operation{Descriptor: workflowsUpdate, Handler: capability.Handler(invokeWorkflowsUpdate)},
		capability.Operation{Descriptor: workflowsActivate, Handler: capability.Handler(invokeWorkflowsActivate)},
		capability.Operation{Descriptor: workflowsDeactivate, Handler: capability.Handler(invokeWorkflowsDeactivate)},
		capability.Operation{Descriptor: executionsRetry, Handler: capability.Handler(invokeExecutionsRetry)},
		capability.Operation{Descriptor: executionsStop, Handler: capability.Handler(invokeExecutionsStop)},
	)
}

// readTools are the four tools of Milestone A, unchanged in scope; the manage profile reuses this list
// instead of repeating it.
var readTools = []string{workflowsList.ID, workflowsGet.ID, executionsList.ID, executionsGet.ID}
