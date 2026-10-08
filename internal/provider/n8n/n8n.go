// Package n8n implements controlled access to one n8n instance (n8n Cloud or self-hosted) over its Public
// API (https://docs.n8n.io/api/api-reference/, packages/cli/src/public-api/v1 of n8n-io/n8n): it lists and
// reads workflows and executions, including a bounded, filtered view of a failed execution's error, creates
// and replaces workflows, activates and deactivates them, and retries and stops executions. There is no tool
// to start a workflow: the Public API documents no endpoint for it. There is also no tool to delete a
// workflow or an execution, and no archive, unarchive, publish, unpublish, transfer, or test-run action, and
// no user management; those are deliberately left
// to a later milestone, which this package's Client, error classes, and target model are built to extend
// without a rewrite. Projects themselves are listed, created, renamed, and deleted (projects.go); see there
// for the allow-list rules and for what deleting a project does. The members of a project are listed, added,
// re-roled, and removed (members.go). Data tables are listed, read (columns, never rows), created without
// columns, renamed, and deleted (datatables.go); their columns are listed, added, renamed or moved, and
// deleted (datacolumns.go); their rows are listed, inserted, updated, upserted, and deleted by a
// structured filter, never cleared (datarows.go). Variables are listed, created, updated, and deleted, project-bound and global
// ones kept apart (variables.go). Tags are managed instance-wide (tags.go). Instance users are listed,
// read, invited, re-roled, and deleted instance-wide, owner-only, with the fate of their resources chosen
// explicitly (users.go). Credentials are listed and read
// as metadata only, never a stored value, tested, and their type schemas read (credentials.go); they are
// created, updated, moved between projects, and deleted (credentialchanges.go), secret values only by a
// reference to a released forward credential, never as an argument.
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
// The security audit is generated, and the source control status read, instance-wide; pulling from and
// pushing to the connected Git repository are confirmed, listed-only tools whose HTTP 409 is a result,
// not a failure (audit.go, sourcecontrol.go).
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
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	return &Client{scope: bound, origin: origin, apiKey: value.Secret, http: provider.NoRedirectClient(defaultTimeout, transport), limiter: lim}, nil
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

// get sends one bounded GET below the Public API root and decodes its body into out. maxBytes bounds how
// much of the answer this process reads before giving up; every read of this provider uses maxResponseBytes
// except the one includeData=true request executions.get may send, which uses maxErrorResponseBytes.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any, maxBytes int) error {
	return c.do(ctx, op, http.MethodGet, path, query, nil, out, maxBytes, false, nil)
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
	return c.do(ctx, op, method, path, query, body, out, maxBytes, true, nil)
}

// errConflict is returned by do, instead of a provider error, for an HTTP 409 on a request that asked for
// its conflict body: n8n refused deterministically and changed nothing, so the caller reports it as a
// result, not as an unclear failure.
var errConflict = errors.New("n8n reported a conflict")

// do sends one bounded request below the Public API root, with a JSON body when body is not nil, and
// decodes the answer into out when out is not nil. A non-nil conflict receives the body of an HTTP 409
// (best effort, never an error) and makes do return errConflict. changing marks a request that may change n8n's state:
// every failure of it that could mean the request nonetheless arrived is marked uncertain, so this provider
// never repeats it by itself, the same contract infomaniakchat and todoist give their own change requests.
func (c *Client) do(ctx context.Context, op, method, path string, query url.Values, body, out any, maxBytes int,
	changing bool, conflict any) error {
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
		if changing && failure.MayHaveArrived() {
			failure.Message += uncertain
		}
		return failure
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusConflict && conflict != nil {
		if data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1)); err == nil && len(data) <= maxBytes {
			_ = json.Unmarshal(data, conflict)
		}
		return errConflict
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		failure := c.responseError(op, response)
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

// responseError maps an HTTP status to a stable class. The provider body is never read into the message. It
// returns the concrete type, not the error interface, so (*Client).do can still append the uncertain
// suffix to its message for a change request that a 5xx may nonetheless have carried out.
func (c *Client) responseError(op string, response *http.Response) *provider.Error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode == http.StatusTooManyRequests {
		c.limiter.HoldFor(provider.RetryAfter(response.Header))
	}
	return provider.ClassifyStatus(op, response.StatusCode, provider.StatusTexts{
		Subject:    "n8n",
		Auth:       "n8n rejected the API key",
		Permission: "this n8n API key may not perform this operation; check its scopes under Settings, n8n API",
		NotFound: "n8n does not hold this resource, does not show it to this API key, or this instance's " +
			"Public API version does not have this endpoint",
	})
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
	return &provider.InvalidRequestError{Message: message}
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
			"of workflows, executions, and projects through its Public API",
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
		}, {
			ID: "projects-read", Title: "Read projects",
			Description: "lists the projects of the instance, restricted to this connection's project " +
				"allow-list; needs the Enterprise Projects feature; changes nothing",
			Tools: []string{projectsList.ID},
		}, {
			ID: "projects-manage", Title: "Manage projects",
			Description: "lists, creates, and renames projects; every change needs its own confirmation. " +
				"Deleting a project is never part of a profile: n8n then deletes every workflow, credential, " +
				"and data table the project owns, so n8n.projects.delete is offered only by a connection " +
				"whose tools list names it",
			Tools: []string{projectsList.ID, projectsCreate.ID, projectsUpdate.ID},
		}, {
			ID: "members-read", Title: "Read project members",
			Description: "lists the members (id, email, name, role) of a project inside this connection's " +
				"project allow-list; needs a project target and the Enterprise Projects feature; changes nothing",
			Tools: []string{membersList.ID},
		}, {
			ID: "members-manage", Title: "Manage project members",
			Description: "lists members, adds existing users to a project, and changes their role, within this " +
				"connection's project allow-list; every change needs its own confirmation. Removing a member is " +
				"never part of a profile: n8n.projectmembers.remove is offered only by a connection whose " +
				"tools list names it",
			Tools: []string{membersList.ID, membersAdd.ID, membersSetRole.ID},
		}, {
			ID: "datatables-read", Title: "Read data tables",
			Description: "lists and reads the data tables (name, project, columns, never rows) of this " +
				"connection's project allow-list; changes nothing",
			Tools: []string{dataTablesList.ID, dataTablesGet.ID},
		}, {
			ID: "datatables-manage", Title: "Manage data tables",
			Description: "reads, creates, and renames data tables within this connection's project allow-list; " +
				"every change needs its own confirmation. Deleting a data table is never part of a profile: it " +
				"deletes all rows, so n8n.datatables.delete is offered only by a connection whose tools list " +
				"names it",
			Tools: []string{dataTablesList.ID, dataTablesGet.ID, dataTablesCreate.ID, dataTablesRename.ID},
		}, {
			ID: "datacolumns-read", Title: "Read data table columns",
			Description: "lists the columns (id, name, type, position, never rows) of data tables of this " +
				"connection's project allow-list; changes nothing",
			Tools: []string{dataColumnsList.ID},
		}, {
			ID: "datacolumns-manage", Title: "Manage data table columns",
			Description: "lists, adds, renames, and moves columns of data tables within this connection's " +
				"project allow-list; every change needs its own confirmation. Deleting a column is never part " +
				"of a profile: it deletes the column's data in every row, so n8n.datacolumns.delete is offered " +
				"only by a connection whose tools list names it",
			Tools: []string{dataColumnsList.ID, dataColumnsAdd.ID, dataColumnsUpdate.ID},
		}, {
			ID: "datarows-read", Title: "Read data table rows",
			Description: "lists rows of data tables of this connection's project allow-list, optionally " +
				"narrowed by a structured filter; row values are untrusted data; changes nothing",
			Tools: []string{dataRowsList.ID},
		}, {
			ID: "datarows-manage", Title: "Manage data table rows",
			Description: "lists rows and inserts new rows into data tables within this connection's project " +
				"allow-list; every insert needs its own confirmation. Updating, upserting, and deleting rows " +
				"are never part of a profile: a filter can match any number of rows, so n8n.datarows.update, " +
				"n8n.datarows.upsert, and n8n.datarows.delete are offered only by a connection whose tools " +
				"list names them",
			Tools: []string{dataRowsList.ID, dataRowsInsert.ID},
		}, {
			ID: "variables-read", Title: "Read variables",
			Description: "lists the variables (key and plain-text value) of this connection's project " +
				"allow-list, or all of them without one; needs the instance's feat:variables license; changes nothing",
			Tools: []string{variablesList.ID},
		}, {
			ID: "variables-manage", Title: "Manage variables",
			Description: "lists, creates, and updates variables within this connection's project allow-list; a " +
				"global variable only on a connection without project and workflow targets; every change needs " +
				"its own confirmation. Deleting a variable is never part of a profile: n8n.variables.delete is " +
				"offered only by a connection whose tools list names it",
			Tools: []string{variablesList.ID, variablesCreate.ID, variablesUpdate.ID},
		}, {
			ID: "tags-read", Title: "Read tags",
			Description: "lists and reads the instance-wide tags; only on a connection without project and " +
				"workflow targets; changes nothing",
			Tools: []string{tagsList.ID, tagsGet.ID},
		}, {
			ID: "tags-manage", Title: "Manage tags",
			Description: "lists, reads, creates, and renames the instance-wide tags; only on a connection " +
				"without project and workflow targets; every change needs its own confirmation. Deleting a tag " +
				"is never part of a profile: n8n.tags.delete is offered only by a connection whose tools list " +
				"names it",
			Tools: []string{tagsList.ID, tagsGet.ID, tagsCreate.ID, tagsUpdate.ID},
		}, {
			ID: "users-read", Title: "Read users",
			Description: "lists and reads the instance users (email, name, role, pending status; personal data); " +
				"owner-only and only on a connection without project and workflow targets; changes nothing",
			Tools: []string{usersList.ID, usersGet.ID},
		}, {
			ID: "users-manage", Title: "Manage users",
			Description: "lists, reads, invites, and changes the instance role of users (never global:owner); " +
				"owner-only and only on a connection without project and workflow targets; every change needs " +
				"its own confirmation. Deleting a user is never part of a profile: n8n.users.delete is offered " +
				"only by a connection whose tools list names it",
			Tools: []string{usersList.ID, usersGet.ID, usersInvite.ID, usersSetRole.ID},
		}, {
			ID: "audit", Title: "Generate a security audit",
			Description: "generates n8n's security audit report (credentials, database, nodes, filesystem, " +
				"instance risks) and returns it capped; owner or admin role only, and only on a connection " +
				"without project and workflow targets; changes nothing",
			Tools: []string{auditGenerate.ID},
		}, {
			ID: "sourcecontrol-read", Title: "Read source control status",
			Description: "previews the pending changes between the instance and its connected Git branch, in " +
				"the push or pull direction; needs the Enterprise source control feature and only on a " +
				"connection without project and workflow targets; changes nothing. Pulling and pushing are " +
				"never part of a profile: n8n.sourcecontrol.pull and n8n.sourcecontrol.push are offered only by " +
				"a connection whose tools list names them",
			Tools: []string{sourceControlStatus.ID},
		}, {
			ID: "credentials-read", Title: "Read credential metadata",
			Description: "lists and reads credentials as metadata (id, name, type, times, never a stored value) " +
				"within this connection's project allow-list, and reads credential type schemas; changes nothing",
			Tools: []string{credentialsList.ID, credentialsGet.ID, credentialsSchema.ID},
		}, {
			ID: "credentials-test", Title: "Test credentials",
			Description: "tests a credential's connection: n8n uses the stored secret against the third-party " +
				"service; every test needs its own confirmation and is sent once. Separate from the read " +
				"profile so it can be granted on its own",
			Tools: []string{credentialsTest.ID},
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
		capability.Operation{Descriptor: projectsList, Handler: capability.Handler(invokeProjectsList)},
		capability.Operation{Descriptor: projectsCreate, Handler: capability.Handler(invokeProjectsCreate)},
		capability.Operation{Descriptor: projectsUpdate, Handler: capability.Handler(invokeProjectsUpdate)},
		capability.Operation{Descriptor: projectsDelete, Handler: capability.Handler(invokeProjectsDelete)},
		capability.Operation{Descriptor: membersList, Handler: capability.Handler(invokeMembersList)},
		capability.Operation{Descriptor: membersAdd, Handler: capability.Handler(invokeMembersAdd)},
		capability.Operation{Descriptor: membersSetRole, Handler: capability.Handler(invokeMembersSetRole)},
		capability.Operation{Descriptor: membersRemove, Handler: capability.Handler(invokeMembersRemove)},
		capability.Operation{Descriptor: dataTablesList, Handler: capability.Handler(invokeDataTablesList)},
		capability.Operation{Descriptor: dataTablesGet, Handler: capability.Handler(invokeDataTablesGet)},
		capability.Operation{Descriptor: dataTablesCreate, Handler: capability.Handler(invokeDataTablesCreate)},
		capability.Operation{Descriptor: dataTablesRename, Handler: capability.Handler(invokeDataTablesRename)},
		capability.Operation{Descriptor: dataTablesDelete, Handler: capability.Handler(invokeDataTablesDelete)},
		capability.Operation{Descriptor: dataColumnsList, Handler: capability.Handler(invokeDataColumnsList)},
		capability.Operation{Descriptor: dataColumnsAdd, Handler: capability.Handler(invokeDataColumnsAdd)},
		capability.Operation{Descriptor: dataColumnsUpdate, Handler: capability.Handler(invokeDataColumnsUpdate)},
		capability.Operation{Descriptor: dataColumnsDelete, Handler: capability.Handler(invokeDataColumnsDelete)},
		capability.Operation{Descriptor: dataRowsList, Handler: capability.Handler(invokeDataRowsList)},
		capability.Operation{Descriptor: dataRowsInsert, Handler: capability.Handler(invokeDataRowsInsert)},
		capability.Operation{Descriptor: dataRowsUpdate, Handler: capability.Handler(invokeDataRowsUpdate)},
		capability.Operation{Descriptor: dataRowsUpsert, Handler: capability.Handler(invokeDataRowsUpsert)},
		capability.Operation{Descriptor: dataRowsDelete, Handler: capability.Handler(invokeDataRowsDelete)},
		capability.Operation{Descriptor: variablesList, Handler: capability.Handler(invokeVariablesList)},
		capability.Operation{Descriptor: variablesCreate, Handler: capability.Handler(invokeVariablesCreate)},
		capability.Operation{Descriptor: variablesUpdate, Handler: capability.Handler(invokeVariablesUpdate)},
		capability.Operation{Descriptor: variablesDelete, Handler: capability.Handler(invokeVariablesDelete)},
		capability.Operation{Descriptor: tagsList, Handler: capability.Handler(invokeTagsList)},
		capability.Operation{Descriptor: tagsGet, Handler: capability.Handler(invokeTagsGet)},
		capability.Operation{Descriptor: tagsCreate, Handler: capability.Handler(invokeTagsCreate)},
		capability.Operation{Descriptor: tagsUpdate, Handler: capability.Handler(invokeTagsUpdate)},
		capability.Operation{Descriptor: tagsDelete, Handler: capability.Handler(invokeTagsDelete)},
		capability.Operation{Descriptor: usersList, Handler: capability.Handler(invokeUsersList)},
		capability.Operation{Descriptor: usersGet, Handler: capability.Handler(invokeUsersGet)},
		capability.Operation{Descriptor: usersInvite, Handler: capability.Handler(invokeUsersInvite)},
		capability.Operation{Descriptor: usersSetRole, Handler: capability.Handler(invokeUsersSetRole)},
		capability.Operation{Descriptor: usersDelete, Handler: capability.Handler(invokeUsersDelete)},
		capability.Operation{Descriptor: auditGenerate, Handler: capability.Handler(invokeAuditGenerate)},
		capability.Operation{Descriptor: sourceControlStatus, Handler: capability.Handler(invokeSourceControlStatus)},
		capability.Operation{Descriptor: sourceControlPull, Handler: capability.Handler(invokeSourceControlPull)},
		capability.Operation{Descriptor: sourceControlPush, Handler: capability.Handler(invokeSourceControlPush)},
		capability.Operation{Descriptor: credentialsList, Handler: capability.Handler(invokeCredentialsList)},
		capability.Operation{Descriptor: credentialsGet, Handler: capability.Handler(invokeCredentialsGet)},
		capability.Operation{Descriptor: credentialsTest, Handler: capability.Handler(invokeCredentialsTest)},
		capability.Operation{Descriptor: credentialsSchema, Handler: capability.Handler(invokeCredentialsSchema)},
		capability.Operation{Descriptor: credentialsCreate, Handler: capability.Handler(invokeCredentialsCreate)},
		capability.Operation{Descriptor: credentialsUpdate, Handler: capability.Handler(invokeCredentialsUpdate)},
		capability.Operation{Descriptor: credentialsTransfer, Handler: capability.Handler(invokeCredentialsTransfer)},
		capability.Operation{Descriptor: credentialsDelete, Handler: capability.Handler(invokeCredentialsDelete)},
	)
}

// readTools are the four tools of Milestone A, unchanged in scope; the manage profile reuses this list
// instead of repeating it.
var readTools = []string{workflowsList.ID, workflowsGet.ID, executionsList.ID, executionsGet.ID}
