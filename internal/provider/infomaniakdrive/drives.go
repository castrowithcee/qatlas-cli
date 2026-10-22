package infomaniakdrive

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// drivesPerPage bounds one page of the drives list. A customer account realistically holds a handful of
// drives, so this is generous rather than tight; the account's own pagination fields are still reported
// transparently for the rare account that holds more.
const drivesPerPage = 100

// maxListEntries bounds every list a drive carries; a longer one is cut and flagged as truncated.
const maxListEntries = 100

// maxDriveTags bounds the tags filter of one drives listing.
const maxDriveTags = 50

// maintenanceReasons are the values the maintenance_reasons filter accepts.
var maintenanceReasons = []string{"demo_end", "invoice_overdue", "not_renew", "technical"}

func quotedList(values []string) string {
	quoted, _ := json.Marshal(values)
	return string(quoted)
}

var driveSchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"name":{"type":"string"},"size":{"type":"integer"},"used_size":{"type":"integer"},` +
	`"in_maintenance":{"type":"boolean"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"maintenance_at":{"type":"string"},` +
	`"expired_at":{"type":"string"},"maintenance_reason":{"type":"string"},` +
	`"maintenance_types":{"type":"array","items":{"type":"object","properties":{` +
	`"name":{"type":"string"},"code":{"type":"string"},"description":{"type":"string"}},` +
	`"additionalProperties":false}},"maintenance_types_truncated":{"type":"boolean"},` +
	`"version":{"type":"string"},"users_count":{"type":"integer"},"users_quota":{"type":"integer"},` +
	`"product_id":{"type":"integer"},"account_id":{"type":"integer"},` +
	`"is_locked":{"type":"boolean"},"is_demo":{"type":"boolean"},"is_in_app_subscription":{"type":"boolean"},` +
	`"role":{"type":"string"},"account_admin":{"type":"boolean"},` +
	`"bdd_storage_host":{"type":"string"},"status":{"type":"string"}},` +
	`"required":["id","name","size","used_size","in_maintenance"],"additionalProperties":false}`

var driveFields = []capability.Field{
	{Name: "id", Description: "Drive identifier, used as drive_id by every other tool of this provider"},
	{Name: "name", Description: "Display name of the kDrive, untrusted data"},
	{Name: "size", Description: "Maximum storage of this drive, in bytes"},
	{Name: "used_size", Description: "Storage currently used on this drive, in bytes"},
	{Name: "in_maintenance", Description: "True while Infomaniak has this drive in maintenance"},
	{Name: "created_at", Description: "When the drive was created, RFC 3339 UTC"},
	{Name: "updated_at", Description: "When the drive was last updated, RFC 3339 UTC"},
	{Name: "maintenance_at", Description: "When the current maintenance was activated, RFC 3339 UTC"},
	{Name: "expired_at", Description: "When the drive's product expires, RFC 3339 UTC"},
	{Name: "maintenance_reason", Description: "Reason of the maintenance: demo_end, invoice_overdue, not_renew or technical"},
	{Name: "maintenance_types", Description: "Active maintenance types, each with name, code and description (untrusted data); at most 100"},
	{Name: "maintenance_types_truncated", Description: "True when the drive reported more maintenance types than listed"},
	{Name: "version", Description: "Backend version of the drive, untrusted data"},
	{Name: "users_count", Description: "Number of user slots used"},
	{Name: "users_quota", Description: "Maximum number of users of this drive"},
	{Name: "product_id", Description: "Identifier of the kDrive product"},
	{Name: "account_id", Description: "Infomaniak account the drive belongs to; always the account this connection is bound to"},
	{Name: "is_locked", Description: "True while the product is locked"},
	{Name: "is_demo", Description: "True while the product is in its free trial period"},
	{Name: "is_in_app_subscription", Description: "True when the drive was purchased within an app store"},
	{Name: "role", Description: "Role of the token's user within this drive: admin, external, none or user"},
	{Name: "account_admin", Description: "True when the token's user administers the drive's account"},
	{Name: "bdd_storage_host", Description: "Host name of the drive's database storage, untrusted data, never contacted"},
	{Name: "status", Description: "Status of the drive: initializing, ok or uninitialized"},
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
	InputSchema: json.RawMessage(`{"type":"object","properties":{"page":{"type":"integer","minimum":1},` +
		`"in_maintenance":{"type":"boolean"},` +
		`"maintenance_reasons":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(len(maintenanceReasons)) +
		`,"uniqueItems":true,"items":{"type":"string","enum":` + quotedList(maintenanceReasons) + `}},` +
		`"tags":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxDriveTags) +
		`,"uniqueItems":true,"items":{"type":"integer","minimum":1}}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"drives":{"type":"array","items":` + driveSchema + `},` +
		`"page":{"type":"integer"},"pages":{"type":"integer"},"total":{"type":"integer"},"count":{"type":"integer"},` +
		`"items_per_page":{"type":"integer"}},` +
		`"required":["drives","page","pages","total","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "page", Description: "1-based page of the account's own drives listing; the first page when omitted"},
		{Name: "in_maintenance", Description: "Only drives that are (true) or are not (false) in maintenance"},
		{Name: "maintenance_reasons", Description: "Only drives in maintenance for one of these reasons (demo_end, invoice_overdue, not_renew, technical); the API ignores it when in_maintenance is set"},
		{Name: "tags", Description: "Only drives carrying one of these tag identifiers; at most 50 positive integers"},
	},
	Fields: append(append([]capability.Field{}, driveFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "pages", Description: "Total number of pages of the bound account, before this connection's drive allow-list is applied"},
		capability.Field{Name: "total", Description: "Total number of drives of the bound account, before this connection's drive allow-list is applied"},
		capability.Field{Name: "count", Description: "Number of drives reported on this page after the allow-list was applied"},
		capability.Field{Name: "items_per_page", Description: "Number of drives per page the API reported"},
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

	// Pointers distinguish an absent member from a real zero or false, which must stay in the output.
	CreatedAt           *int64                 `json:"created_at"`
	UpdatedAt           *int64                 `json:"updated_at"`
	MaintenanceAt       *int64                 `json:"maintenance_at"`
	ExpiredAt           *int64                 `json:"expired_at"`
	MaintenanceReason   string                 `json:"maintenance_reason"`
	MaintenanceTypes    []MaintenanceTypeEntry `json:"maintenance_types"`
	Version             string                 `json:"version"`
	UsersCount          *int64                 `json:"users_count"`
	UsersQuota          *int64                 `json:"users_quota"`
	ProductID           *int64                 `json:"product_id"`
	IsLocked            *bool                  `json:"is_locked"`
	IsDemo              *bool                  `json:"is_demo"`
	IsInAppSubscription *bool                  `json:"is_in_app_subscription"`
	Role                string                 `json:"role"`
	AccountAdmin        *bool                  `json:"account_admin"`
	BddStorageHost      string                 `json:"bdd_storage_host"`
	Status              string                 `json:"status"`
}

// DriveEntry is the stable Qatlas view of one kDrive.
type DriveEntry struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	UsedSize      int64  `json:"used_size"`
	InMaintenance bool   `json:"in_maintenance"`

	CreatedAt                 string                 `json:"created_at,omitempty"`
	UpdatedAt                 string                 `json:"updated_at,omitempty"`
	MaintenanceAt             string                 `json:"maintenance_at,omitempty"`
	ExpiredAt                 string                 `json:"expired_at,omitempty"`
	MaintenanceReason         string                 `json:"maintenance_reason,omitempty"`
	MaintenanceTypes          []MaintenanceTypeEntry `json:"maintenance_types,omitempty"`
	MaintenanceTypesTruncated bool                   `json:"maintenance_types_truncated,omitempty"`
	Version                   string                 `json:"version,omitempty"`
	UsersCount                *int64                 `json:"users_count,omitempty"`
	UsersQuota                *int64                 `json:"users_quota,omitempty"`
	ProductID                 *int64                 `json:"product_id,omitempty"`
	AccountID                 int64                  `json:"account_id,omitempty"`
	IsLocked                  *bool                  `json:"is_locked,omitempty"`
	IsDemo                    *bool                  `json:"is_demo,omitempty"`
	IsInAppSubscription       *bool                  `json:"is_in_app_subscription,omitempty"`
	Role                      string                 `json:"role,omitempty"`
	AccountAdmin              *bool                  `json:"account_admin,omitempty"`
	BddStorageHost            string                 `json:"bdd_storage_host,omitempty"`
	Status                    string                 `json:"status,omitempty"`
}

// MaintenanceTypeEntry is one active maintenance type of a drive. All texts are untrusted data.
type MaintenanceTypeEntry struct {
	Name        string `json:"name,omitempty"`
	Code        string `json:"code,omitempty"`
	Description string `json:"description,omitempty"`
}

// DrivesPage is one paginated, allow-list-filtered listing of the drives of the bound account.
type DrivesPage struct {
	Drives []DriveEntry `json:"drives"`
	Page   int          `json:"page"`
	Pages  int          `json:"pages"`
	Total  int          `json:"total"`
	Count  int          `json:"count"`

	ItemsPerPage int `json:"items_per_page,omitempty"`
}

// paginationJSON is the page-based pagination envelope Infomaniak reports as siblings of data.
type paginationJSON struct {
	Page  int `json:"page"`
	Pages int `json:"pages"`
	Total int `json:"total"`

	ItemsPerPage int `json:"items_per_page"`
}

type drivesListArguments struct {
	Page               int      `json:"page"`
	InMaintenance      *bool    `json:"in_maintenance"`
	MaintenanceReasons []string `json:"maintenance_reasons"`
	Tags               []int64  `json:"tags"`
}

// DriveFilter narrows one drives listing. The zero value sends no filter.
type DriveFilter struct {
	InMaintenance      *bool
	MaintenanceReasons []string
	Tags               []int64
}

// values adds the filter to a listing query. Lists go as repeated parameters, in the order the caller gave
// them.
func (f DriveFilter) values(query url.Values) {
	if f.InMaintenance != nil {
		query.Set("in_maintenance", strconv.FormatBool(*f.InMaintenance))
	}
	for _, reason := range f.MaintenanceReasons {
		query.Add("maintenance_reasons", reason)
	}
	for _, tag := range f.Tags {
		query.Add("tags", strconv.FormatInt(tag, 10))
	}
}

// validate refuses an argument outside the fixed value lists and bounds before anything is requested. The
// input schema already enforces this; the check keeps the filter safe for any other caller.
func (f DriveFilter) validate() error {
	seenReason := map[string]bool{}
	for _, reason := range f.MaintenanceReasons {
		if seenReason[reason] || !slices.Contains(maintenanceReasons, reason) {
			return invalidRequest("maintenance_reasons must hold distinct values this tool offers")
		}
		seenReason[reason] = true
	}
	if len(f.Tags) > maxDriveTags {
		return invalidRequest("tags holds more identifiers than allowed")
	}
	seen := map[int64]bool{}
	for _, tag := range f.Tags {
		if tag < 1 || seen[tag] {
			return invalidRequest("tags must be distinct positive integers")
		}
		seen[tag] = true
	}
	return nil
}

// timestampOf renders an optional Unix-seconds timestamp as RFC 3339 UTC; an absent one stays empty.
func timestampOf(seconds *int64) string {
	if seconds == nil {
		return ""
	}
	return time.Unix(*seconds, 0).UTC().Format(time.RFC3339)
}

// valuePointer returns a copy of an optional number so the output does not alias the decoded response.
func valuePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
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
	filter := DriveFilter{
		InMaintenance: input.InMaintenance, MaintenanceReasons: input.MaintenanceReasons,
		Tags: input.Tags,
	}
	if err := filter.validate(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListDrives(ctx, page, filter)
}

// ListDrives reads one page of the drives of the bound account and keeps only those the connection's drive
// allow-list admits, and whose own account_id matches the bound account: a defence in depth against a drive
// Infomaniak reported under an account other than the one this connection is bound to.
func (c *Client) ListDrives(ctx context.Context, page int, filter DriveFilter) (*DrivesPage, error) {
	if err := filter.validate(); err != nil {
		return nil, err
	}
	drives, meta, err := c.readDrivesFiltered(ctx, "list drives", c.scope.accountID, page, filter)
	if err != nil {
		return nil, err
	}
	entries := make([]DriveEntry, 0, len(drives))
	for _, d := range drives {
		if d.AccountID != c.scope.accountID || !c.scope.allowsDrive(d.ID) {
			continue
		}
		entries = append(entries, driveEntryOf(d))
	}
	return &DrivesPage{
		Drives: entries, Page: meta.Page, Pages: meta.Pages, Total: meta.Total, Count: len(entries),
		ItemsPerPage: meta.ItemsPerPage,
	}, nil
}

// driveEntryOf maps one admitted drive. Foreign texts are cleaned of control characters and bounded.
func driveEntryOf(d driveJSON) DriveEntry {
	entry := DriveEntry{
		ID: d.ID, Name: bounded(d.Name), Size: d.Size, UsedSize: d.UsedSize, InMaintenance: d.InMaintenance,
		CreatedAt: timestampOf(d.CreatedAt), UpdatedAt: timestampOf(d.UpdatedAt),
		MaintenanceAt: timestampOf(d.MaintenanceAt), ExpiredAt: timestampOf(d.ExpiredAt),
		MaintenanceReason: boundedText(d.MaintenanceReason, 64), Version: boundedText(d.Version, 128),
		UsersCount: valuePointer(d.UsersCount), UsersQuota: valuePointer(d.UsersQuota),
		ProductID: valuePointer(d.ProductID), AccountID: d.AccountID,
		IsLocked: valuePointer(d.IsLocked), IsDemo: valuePointer(d.IsDemo),
		IsInAppSubscription: valuePointer(d.IsInAppSubscription), Role: boundedText(d.Role, 64),
		AccountAdmin: valuePointer(d.AccountAdmin), BddStorageHost: boundedText(d.BddStorageHost, 255),
		Status: boundedText(d.Status, 64),
	}
	types := d.MaintenanceTypes
	if len(types) > maxListEntries {
		types, entry.MaintenanceTypesTruncated = types[:maxListEntries], true
	}
	for _, kind := range types {
		entry.MaintenanceTypes = append(entry.MaintenanceTypes, MaintenanceTypeEntry{
			Name: boundedText(kind.Name, 256), Code: boundedText(kind.Code, 128),
			Description: boundedText(kind.Description, 1024),
		})
	}
	return entry
}

// readDrives reads one raw page of the drives listing. An accountID of zero omits the filter and lists the
// drives of every account the token reaches. It applies no allow-list.
func (c *Client) readDrives(ctx context.Context, op string, accountID int64, page int) ([]driveJSON, paginationJSON, error) {
	return c.readDrivesFiltered(ctx, op, accountID, page, DriveFilter{})
}

// readDrivesFiltered is readDrives with the optional filter of the drives.list tool; every
// other caller goes through readDrives and so never sends them.
func (c *Client) readDrivesFiltered(ctx context.Context, op string, accountID int64, page int,
	filter DriveFilter) ([]driveJSON, paginationJSON, error) {
	query := url.Values{
		"page":     {strconv.Itoa(page)},
		"per_page": {strconv.Itoa(drivesPerPage)},
	}
	if accountID != 0 {
		query.Set("account_id", strconv.FormatInt(accountID, 10))
	}
	filter.values(query)
	var drives []driveJSON
	var meta paginationJSON
	if err := c.doInto(ctx, op, "/2/drive", query, &drives, &meta); err != nil {
		return nil, paginationJSON{}, err
	}
	return drives, meta, nil
}
