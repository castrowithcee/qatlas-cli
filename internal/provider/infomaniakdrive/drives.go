package infomaniakdrive

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// drivesPerPage bounds one page of the drives list. A customer account realistically holds a handful of
// drives, so this is generous rather than tight; the account's own pagination fields are still reported
// transparently for the rare account that holds more.
const drivesPerPage = 100

var driveSchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"name":{"type":"string"},"size":{"type":"integer"},"used_size":{"type":"integer"},` +
	`"in_maintenance":{"type":"boolean"}},` +
	`"required":["id","name","size","used_size","in_maintenance"],"additionalProperties":false}`

var driveFields = []capability.Field{
	{Name: "id", Description: "Drive identifier, used as drive_id by every other tool of this provider"},
	{Name: "name", Description: "Display name of the kDrive, untrusted data"},
	{Name: "size", Description: "Maximum storage of this drive, in bytes"},
	{Name: "used_size", Description: "Storage currently used on this drive, in bytes"},
	{Name: "in_maintenance", Description: "True while Infomaniak has this drive in maintenance"},
}

var drivesReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

var drivesList = capability.Descriptor{
	ID:      Provider + ".drives.list",
	Version: 1,
	Title:   "List Infomaniak kDrives",
	Description: "List the kDrives of the Infomaniak account this connection is bound to, restricted to its " +
		"drive allow-list when it has one; page by page, transparently",
	Tags:     []string{"infomaniak", "kdrive", "drives", "list"},
	Risk:     drivesReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer","minimum":1}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"drives":{"type":"array","items":` + driveSchema + `},` +
		`"page":{"type":"integer"},"pages":{"type":"integer"},"total":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["drives","page","pages","total","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "page", Description: "1-based page of the account's own drives listing; the first page when omitted"},
	},
	Fields: append(append([]capability.Field{}, driveFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "pages", Description: "Total number of pages of the bound account, before this connection's drive allow-list is applied"},
		capability.Field{Name: "total", Description: "Total number of drives of the bound account, before this connection's drive allow-list is applied"},
		capability.Field{Name: "count", Description: "Number of drives reported on this page after the allow-list was applied"},
	),
	Examples: []capability.Example{{Description: "List the reachable drives", Arguments: json.RawMessage(`{}`)}},
}

// driveJSON is the subset of the Infomaniak Drive resource this provider reads.
type driveJSON struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	UsedSize      int64  `json:"used_size"`
	InMaintenance bool   `json:"in_maintenance"`
	AccountID     int64  `json:"account_id"`
}

// DriveEntry is the stable Qatlas view of one kDrive.
type DriveEntry struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	UsedSize      int64  `json:"used_size"`
	InMaintenance bool   `json:"in_maintenance"`
}

// DrivesPage is one paginated, allow-list-filtered listing of the drives of the bound account.
type DrivesPage struct {
	Drives []DriveEntry `json:"drives"`
	Page   int          `json:"page"`
	Pages  int          `json:"pages"`
	Total  int          `json:"total"`
	Count  int          `json:"count"`
}

// paginationJSON is the page-based pagination envelope Infomaniak reports as siblings of data.
type paginationJSON struct {
	Page  int `json:"page"`
	Pages int `json:"pages"`
	Total int `json:"total"`
}

type drivesListArguments struct {
	Page int `json:"page"`
}

func invokeDrivesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input drivesListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list drives", "the validated arguments could not be read")
	}
	page := input.Page
	if page == 0 {
		page = 1
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListDrives(ctx, page)
}

// ListDrives reads one page of the drives of the bound account and keeps only those the connection's drive
// allow-list admits, and whose own account_id matches the bound account: a defence in depth against a drive
// Infomaniak reported under an account other than the one this connection is bound to.
func (c *Client) ListDrives(ctx context.Context, page int) (*DrivesPage, error) {
	const op = "list drives"
	query := url.Values{
		"account_id": {strconv.FormatInt(c.scope.accountID, 10)},
		"page":       {strconv.Itoa(page)},
		"per_page":   {strconv.Itoa(drivesPerPage)},
	}
	var drives []driveJSON
	var meta paginationJSON
	if err := c.doInto(ctx, op, "/2/drive", query, &drives, &meta); err != nil {
		return nil, err
	}
	entries := make([]DriveEntry, 0, len(drives))
	for _, d := range drives {
		if d.AccountID != c.scope.accountID || !c.scope.allowsDrive(d.ID) {
			continue
		}
		entries = append(entries, DriveEntry{
			ID: d.ID, Name: bounded(d.Name), Size: d.Size, UsedSize: d.UsedSize, InMaintenance: d.InMaintenance,
		})
	}
	return &DrivesPage{Drives: entries, Page: meta.Page, Pages: meta.Pages, Total: meta.Total, Count: len(entries)}, nil
}
