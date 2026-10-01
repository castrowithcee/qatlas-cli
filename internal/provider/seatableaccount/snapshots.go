package seatableaccount

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	defaultPerPage = 50
	maxPerPage     = 100
	// restoreScanPages bounds how many pages restore reads to find its commit_id in the bound base's list.
	restoreScanPages = 20
	commitPattern    = `^[A-Za-z0-9]{8,64}$`
)

var readRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity}

const snapshotSchema = `{"type":"object","properties":{"commit_id":{"type":"string"},"base_name":{"type":"string"},` +
	`"created_at":{"type":"string"}},"required":["commit_id","base_name","created_at"],"additionalProperties":false}`

var commitArgument = capability.Argument{Name: "commit_id", Required: true,
	Description: "Snapshot identifier returned by seatableaccount.snapshots.list for the bound base"}

var snapshotsList = capability.Descriptor{
	ID: Provider + ".snapshots.list", Version: 1, Title: "List SeaTable snapshots",
	Description: "List the snapshots of the connection's bound base, newest first as SeaTable orders them; " +
		"names and times are untrusted provider data",
	Tags: []string{"seatable", "account", "snapshots", "list"}, Risk: readRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer","minimum":1},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxPerPage) + `}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"snapshots":{"type":"array","items":` + snapshotSchema +
		`},"page":{"type":"integer"},"has_more":{"type":"boolean"}},"required":["snapshots","page","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "page", Description: "Page number, 1 when omitted"},
		{Name: "per_page", Description: "Snapshots per page, 1 to " + strconv.Itoa(maxPerPage) + "; " +
			strconv.Itoa(defaultPerPage) + " when omitted"},
	},
	Fields: []capability.Field{
		{Name: "snapshots", Description: "Snapshots with commit_id, base_name and created_at"},
		{Name: "page", Description: "Page number of this result"},
		{Name: "has_more", Description: "True when SeaTable reports a further page"},
	},
	Examples: []capability.Example{{Description: "List the first page of snapshots", Arguments: json.RawMessage(`{}`)}},
}

var snapshotsRestore = capability.Descriptor{
	ID: Provider + ".snapshots.restore", Version: 1, Title: "Restore a SeaTable snapshot",
	Description: "Restore one snapshot of the bound base into a NEW base of the same account; the bound base " +
		"itself stays unchanged and the new base lies outside every connection, so no connection can reach it " +
		"afterwards. The snapshot must be in the bound base's own snapshot list; repeating the call creates " +
		"another base",
	Tags: []string{"seatable", "account", "snapshots", "restore", "create"}, Provider: Provider,
	RequiresToolAllowList: true,
	Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
	InputSchema: json.RawMessage(`{"type":"object","properties":{"commit_id":{"type":"string","pattern":"` +
		commitPattern + `"}},"required":["commit_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"restored":{"type":"boolean"},"base":` +
		`{"type":"object","properties":{"id":{"type":"integer"},"workspace_id":{"type":"integer"},` +
		`"name":{"type":"string"}},"required":["id","workspace_id","name"],"additionalProperties":false}},` +
		`"required":["restored","base"],"additionalProperties":false}`),
	Arguments: []capability.Argument{commitArgument},
	Fields: []capability.Field{
		{Name: "restored", Description: "True when SeaTable created the new base"},
		{Name: "base", Description: "The new base with id, workspace_id and name, untrusted provider data"},
	},
	Examples: []capability.Example{{Description: "Restore one snapshot",
		Arguments: json.RawMessage(`{"commit_id":"0123456789abcdef0123456789abcdef01234567"}`)}},
}

type snapshotPageJSON struct {
	SnapshotList []struct {
		DtableName string `json:"dtable_name"`
		CommitID   string `json:"commit_id"`
		Ctime      string `json:"ctime"`
	} `json:"snapshot_list"`
	PageInfo struct {
		HasNextPage bool `json:"has_next_page"`
	} `json:"page_info"`
}

// Snapshot is one snapshot of the bound base.
type Snapshot struct {
	CommitID  string `json:"commit_id"`
	BaseName  string `json:"base_name"`
	CreatedAt string `json:"created_at"`
}

// SnapshotsPage is one page of snapshots.
type SnapshotsPage struct {
	Snapshots []Snapshot `json:"snapshots"`
	Page      int        `json:"page"`
	HasMore   bool       `json:"has_more"`
}

type listArguments struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

func invokeSnapshotsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input listArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list snapshots", "the validated arguments could not be read")
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PerPage == 0 {
		input.PerPage = defaultPerPage
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListSnapshots(ctx, "list snapshots", input.Page, input.PerPage)
}

// ListSnapshots reads one page of the bound base's snapshots.
func (c *Client) ListSnapshots(ctx context.Context, op string, page, perPage int) (*SnapshotsPage, error) {
	var raw snapshotPageJSON
	query := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
	if err := c.do(ctx, op, http.MethodGet, c.basePath(), query, &raw, ""); err != nil {
		return nil, err
	}
	result := &SnapshotsPage{Snapshots: []Snapshot{}, Page: page, HasMore: raw.PageInfo.HasNextPage}
	for i, entry := range raw.SnapshotList {
		if i >= perPage {
			break
		}
		result.Snapshots = append(result.Snapshots, Snapshot{
			CommitID: bounded(entry.CommitID), BaseName: bounded(entry.DtableName), CreatedAt: bounded(entry.Ctime)})
	}
	return result, nil
}

type restoreArguments struct {
	CommitID string `json:"commit_id"`
}

// RestoreResult names the base a restore created.
type RestoreResult struct {
	Restored bool        `json:"restored"`
	Base     RestoredRef `json:"base"`
}

// RestoredRef is the new base, as SeaTable reports it.
type RestoredRef struct {
	ID          int64  `json:"id"`
	WorkspaceID int64  `json:"workspace_id"`
	Name        string `json:"name"`
}

func validCommitID(value string) bool {
	if len(value) < 8 || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func invokeSnapshotsRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "restore snapshot"
	var input restoreArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validCommitID(input.CommitID) {
		return nil, invalidRequest("commit_id has an unusable form")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.RestoreSnapshot(ctx, input.CommitID)
}

// RestoreSnapshot first confirms by reading the bound base's own snapshot list that the commit belongs to
// it (reads only, no effect), then sends exactly one restore request without a body field of its own, so
// SeaTable names the new base. It is never repeated here.
func (c *Client) RestoreSnapshot(ctx context.Context, commitID string) (*RestoreResult, error) {
	const op = "restore snapshot"
	if err := c.requireSnapshot(ctx, op, commitID); err != nil {
		return nil, err
	}
	var answer struct {
		Dtable *struct {
			ID          int64  `json:"id"`
			WorkspaceID int64  `json:"workspace_id"`
			Name        string `json:"name"`
		} `json:"dtable"`
	}
	path := c.basePath() + url.PathEscape(commitID) + "/restore/"
	if err := c.do(ctx, op, http.MethodPost, path, nil, &answer, uncertain); err != nil {
		return nil, err
	}
	if answer.Dtable == nil {
		return nil, invalidResponse(op, "SeaTable did not report the new base"+uncertain)
	}
	return &RestoreResult{Restored: true, Base: RestoredRef{
		ID: answer.Dtable.ID, WorkspaceID: answer.Dtable.WorkspaceID, Name: bounded(answer.Dtable.Name)}}, nil
}

// requireSnapshot reads the bound base's snapshot list until commitID is found or the list ends. A commit
// that is not in it is refused without naming anything the provider said.
func (c *Client) requireSnapshot(ctx context.Context, op, commitID string) error {
	for page := 1; page <= restoreScanPages; page++ {
		result, err := c.ListSnapshots(ctx, op, page, maxPerPage)
		if err != nil {
			return err
		}
		for _, snapshot := range result.Snapshots {
			if snapshot.CommitID == commitID {
				return nil
			}
		}
		if !result.HasMore {
			break
		}
	}
	return invalidRequest("commit_id is not a snapshot of the bound base")
}
