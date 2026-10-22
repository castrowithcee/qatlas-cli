package infomaniakdrive

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// maxIdentifiers bounds each list of user or team identifiers of a drive; they are bare numbers, so far
// more fit than of the object lists bounded by maxListEntries.
const maxIdentifiers = 1000

// Bounds of a free object: the specification leaves some rewind figures untyped, so their content is not
// known. An object beyond any bound is left out and flagged, never cut.
const (
	freeObjectDepth = 3
	freeObjectNodes = 100
	freeObjectText  = 128
)

// stamp is a Unix-seconds timestamp read from Infomaniak and written as RFC 3339 UTC. It reads its own
// output too, so a rendered result decodes again.
type stamp string

func (s *stamp) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		*s = stamp(boundedText(text, 64))
		return nil
	}
	var seconds int64
	if err := json.Unmarshal(trimmed, &seconds); err != nil {
		return err
	}
	*s = stamp(time.Unix(seconds, 0).UTC().Format(time.RFC3339))
	return nil
}

// AccountEntry is the account of a drive, loaded by with=account. Its texts are untrusted data.
type AccountEntry struct {
	ID              int64  `json:"id,omitempty"`
	Beta            *bool  `json:"beta,omitempty"`
	Name            string `json:"name,omitempty"`
	LegalEntityType string `json:"legal_entity_type,omitempty"`
	Phone           string `json:"phone,omitempty"`
	SupportLevel    *int64 `json:"support_level,omitempty"`
	Has2FARequired  *bool  `json:"has_2fa_required,omitempty"`
	IsSSO           *bool  `json:"is_sso,omitempty"`
	Website         string `json:"website,omitempty"`
	CreatedAt       stamp  `json:"created_at,omitempty"`
}

// DriveCapabilities are the features of a drive, loaded by with=capabilities.
type DriveCapabilities struct {
	UseVersioning        *bool  `json:"use_versioning,omitempty"`
	UseUploadCompression *bool  `json:"use_upload_compression,omitempty"`
	UseTeamSpace         *bool  `json:"use_team_space,omitempty"`
	CanAddUser           *bool  `json:"can_add_user,omitempty"`
	CanSeeStats          *bool  `json:"can_see_stats,omitempty"`
	CanUpgradeToKSuite   *bool  `json:"can_upgrade_to_ksuite,omitempty"`
	CanRewind            *bool  `json:"can_rewind,omitempty"`
	CanUseRAG            *bool  `json:"can_use_rag,omitempty"`
	Search               string `json:"search,omitempty"`
}

// PackCapabilities are the features of the drive's pricing plan, loaded by with=pack.capabilities.
type PackCapabilities struct {
	UseVault                 *bool `json:"use_vault,omitempty"`
	UseManageRight           *bool `json:"use_manage_right,omitempty"`
	UseDropbox               *bool `json:"use_dropbox,omitempty"`
	CanRewind                *bool `json:"can_rewind,omitempty"`
	UseFolderCustomColor     *bool `json:"use_folder_custom_color,omitempty"`
	CanAccessDashboard       *bool `json:"can_access_dashboard,omitempty"`
	CanSetSharelinkPassword  *bool `json:"can_set_sharelink_password,omitempty"`
	CanJoinSharelink         *bool `json:"can_join_sharelink,omitempty"`
	CanSetSharelinkExpiry    *bool `json:"can_set_sharelink_expiration,omitempty"`
	CanSetSharelinkCustomURL *bool `json:"can_set_sharelink_custom_url,omitempty"`
	CanUseContentSearch      *bool `json:"can_use_content_search,omitempty"`
	CanUseActivityReports    *bool `json:"can_use_activity_reports,omitempty"`
	CanUseSignatures         *bool `json:"can_use_signatures,omitempty"`
	CanUseStatistics         *bool `json:"can_use_statistics,omitempty"`
	CanEditPDF               *bool `json:"can_edit_pdf,omitempty"`
	CanUseOffice365          *bool `json:"can_use_office365,omitempty"`
	CanSetOffice             *bool `json:"can_set_office,omitempty"`
	StartGuidePDF            *bool `json:"start_guide_pdf,omitempty"`
	CanUseRAG                *bool `json:"can_use_rag,omitempty"`
	CanUseWebDAV             *bool `json:"can_use_webdav,omitempty"`
	CanUseStorageReport      *bool `json:"can_use_storage_report,omitempty"`
	CanUsePhoto              *bool `json:"can_use_photo,omitempty"`
	CanSetTrashDuration      *bool `json:"can_set_trash_duration,omitempty"`
}

// Range is a base value with optional bounds; Infomaniak reports null for an unbounded side.
type Range struct {
	Base *int64 `json:"base,omitempty"`
	Giga *int64 `json:"giga,omitempty"`
	Min  *int64 `json:"min,omitempty"`
	Max  *int64 `json:"max,omitempty"`
}

// TrashLimits are the retention options of the trash, in days.
type TrashLimits struct {
	RetentionOptions          []int64 `json:"retention_options,omitempty"`
	RetentionOptionsTruncated bool    `json:"retention_options_truncated,omitempty"`
}

// PackLimits are the limits of the pricing plan, loaded by with=pack.limits.
type PackLimits struct {
	Users   *Range       `json:"users,omitempty"`
	Trash   *TrashLimits `json:"trash,omitempty"`
	Storage *Range       `json:"storage,omitempty"`
}

// PackPrice is one price of the pricing plan. The amounts keep Infomaniak's exact decimal text.
type PackPrice struct {
	Type          string      `json:"type,omitempty"`
	Unit          *int64      `json:"unit,omitempty"`
	Period        *int64      `json:"period,omitempty"`
	CurrencyID    *int64      `json:"currency_id,omitempty"`
	AmountExclVAT json.Number `json:"amount_excl_vat,omitempty"`
	AmountInclVAT json.Number `json:"amount_incl_vat,omitempty"`
}

// PackEntry is the pricing plan of a drive; with=pack loads its identity, the other pack values its parts.
type PackEntry struct {
	ID              int64             `json:"id,omitempty"`
	Name            string            `json:"name,omitempty"`
	DisplayName     string            `json:"display_name,omitempty"`
	IsFree          *bool             `json:"is_free,omitempty"`
	Capabilities    *PackCapabilities `json:"capabilities,omitempty"`
	Limits          *PackLimits       `json:"limits,omitempty"`
	Prices          []PackPrice       `json:"prices,omitempty"`
	PricesTruncated bool              `json:"prices_truncated,omitempty"`
}

// SettingsEntry are the settings of a drive, loaded by with=settings.
type SettingsEntry struct {
	AIScan *struct {
		IsEnabled                *bool `json:"is_enabled,omitempty"`
		HasApproved              *bool `json:"has_approved,omitempty"`
		HasApprovedAICategories  *bool `json:"has_approved_ai_categories,omitempty"`
		HasApprovedContentSearch *bool `json:"has_approved_content_search,omitempty"`
		UpdatedAt                stamp `json:"updated_at,omitempty"`
	} `json:"ai_scan,omitempty"`
	Versioning *struct {
		MaxNumbers *int64 `json:"max_numbers,omitempty"`
		MaxDays    *int64 `json:"max_days,omitempty"`
	} `json:"versioning,omitempty"`
	Office *struct {
		Presentation string `json:"presentation,omitempty"`
		Spreadsheet  string `json:"spreadsheet,omitempty"`
		Text         string `json:"text,omitempty"`
		DefaultMode  string `json:"default_mode,omitempty"`
	} `json:"office,omitempty"`
	MaxKeepDeletedUser string `json:"max_keep_deleted_user,omitempty"`
	Files              *struct {
		Private *struct {
			SizeThreshold *int64 `json:"size_threshold,omitempty"`
		} `json:"private,omitempty"`
	} `json:"files,omitempty"`
	Report *struct {
		StorageReport *struct {
			Frequency string `json:"frequency,omitempty"`
		} `json:"storage_report,omitempty"`
	} `json:"report,omitempty"`
}

// PreferencesEntry are the token user's preferences for a drive, loaded by with=preferences.
type PreferencesEntry struct {
	Color       string `json:"color,omitempty"`
	Hide        *bool  `json:"hide,omitempty"`
	Default     *bool  `json:"default,omitempty"`
	DefaultPage string `json:"default_page,omitempty"`
	UI          *struct {
		StorageWarningAlert *bool `json:"storage_warning_alert,omitempty"`
		StorageLimitAlert   *bool `json:"storage_limit_alert,omitempty"`
		RAGOCRPromotion     *bool `json:"rag_ocr_promotion,omitempty"`
	} `json:"ui,omitempty"`
}

// KSuiteEntry is the kSuite a drive belongs to, loaded by with=k_suite.
type KSuiteEntry struct {
	ID     int64  `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	PackID *int64 `json:"pack_id,omitempty"`
	Pack   string `json:"pack,omitempty"`
}

// TagEntry is one tag of a drive; product_count needs with=tags.#products.
type TagEntry struct {
	ID           int64  `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Color        *int64 `json:"color,omitempty"`
	ProductCount *int64 `json:"product_count,omitempty"`
}

// RightsEntry are the rights of the token user, loaded by with=rights.
type RightsEntry struct {
	Technical *bool `json:"technical,omitempty"`
	Statistic *bool `json:"statistic,omitempty"`
	Check     *bool `json:"check,omitempty"`
	Sale      *bool `json:"sale,omitempty"`
}

// CategoryEntry is one file category of a drive; user_uses needs with=categories.user_uses.
type CategoryEntry struct {
	ID           int64  `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Color        string `json:"color,omitempty"`
	IsPredefined *bool  `json:"is_predefined,omitempty"`
	CreatedBy    *int64 `json:"created_by,omitempty"`
	CreatedAt    stamp  `json:"created_at,omitempty"`
	UserUses     *int64 `json:"user_uses,omitempty"`
}

// CategoriesPermissionsEntry are the category permissions of the token user.
type CategoriesPermissionsEntry struct {
	CanCreate     *bool `json:"can_create,omitempty"`
	CanEdit       *bool `json:"can_edit,omitempty"`
	CanDelete     *bool `json:"can_delete,omitempty"`
	CanReadOnFile *bool `json:"can_read_on_file,omitempty"`
	CanPutOnFile  *bool `json:"can_put_on_file,omitempty"`
}

// UsersEntry holds user identifiers of a drive by group, loaded by with=users. Identifiers only.
type UsersEntry struct {
	Account           []int64 `json:"account,omitempty"`
	AccountTruncated  bool    `json:"account_truncated,omitempty"`
	Drive             []int64 `json:"drive,omitempty"`
	DriveTruncated    bool    `json:"drive_truncated,omitempty"`
	External          []int64 `json:"external,omitempty"`
	ExternalTruncated bool    `json:"external_truncated,omitempty"`
	Internal          []int64 `json:"internal,omitempty"`
	InternalTruncated bool    `json:"internal_truncated,omitempty"`
}

// TeamsEntry holds team identifiers of a drive by group, loaded by with=teams. Identifiers only.
type TeamsEntry struct {
	Account          []int64 `json:"account,omitempty"`
	AccountTruncated bool    `json:"account_truncated,omitempty"`
	Drive            []int64 `json:"drive,omitempty"`
	DriveTruncated   bool    `json:"drive_truncated,omitempty"`
}

// QuotaEntry are the feature limits of the drive's offer, loaded by with=quota.
type QuotaEntry struct {
	Dropbox    string `json:"dropbox,omitempty"`
	SharedLink string `json:"shared_link,omitempty"`
}

// rewindJSON is the rewind of a drive as Infomaniak reports it; the untyped figures stay raw.
type rewindJSON struct {
	ID            int64           `json:"id"`
	Status        string          `json:"status"`
	Mode          string          `json:"mode"`
	Archiving     string          `json:"archiving"`
	DirectoryID   *int64          `json:"directory_id"`
	Directory     *fileJSON       `json:"directory"`
	DirectoryName string          `json:"directory_name"`
	UserID        *int64          `json:"user_id"`
	RewindAt      stamp           `json:"rewind_at"`
	CreatedAt     stamp           `json:"created_at"`
	FinishedAt    stamp           `json:"finished_at"`
	ExpiresAt     stamp           `json:"expires_at"`
	CreatedBy     *int64          `json:"created_by"`
	IsApproved    *bool           `json:"is_approved"`
	ApprovalAt    stamp           `json:"approval_at"`
	RewindName    string          `json:"rewind_name"`
	Sanitizer     json.RawMessage `json:"sanitizer"`
	Summary       json.RawMessage `json:"summary"`
}

// RewindEntry is the rewind of a drive, loaded by with=rewind and its parts. directory, directory_name and
// rewind_name are left out for a drive bound only by files targets, because they can name a folder outside
// the targets.
type RewindEntry struct {
	ID            int64            `json:"id,omitempty"`
	Status        string           `json:"status,omitempty"`
	Mode          string           `json:"mode,omitempty"`
	Archiving     string           `json:"archiving,omitempty"`
	DirectoryID   *int64           `json:"directory_id,omitempty"`
	Directory     *Entry           `json:"directory,omitempty"`
	DirectoryName string           `json:"directory_name,omitempty"`
	UserID        *int64           `json:"user_id,omitempty"`
	RewindAt      stamp            `json:"rewind_at,omitempty"`
	CreatedAt     stamp            `json:"created_at,omitempty"`
	FinishedAt    stamp            `json:"finished_at,omitempty"`
	ExpiresAt     stamp            `json:"expires_at,omitempty"`
	CreatedBy     *int64           `json:"created_by,omitempty"`
	IsApproved    *bool            `json:"is_approved,omitempty"`
	ApprovalAt    stamp            `json:"approval_at,omitempty"`
	RewindName    string           `json:"rewind_name,omitempty"`
	Sanitizer     *RewindSanitizer `json:"sanitizer,omitempty"`
	Summary       *RewindSummary   `json:"summary,omitempty"`
}

// RewindSanitizer holds the sanitizer figures of a rewind. The figure groups are bounded free objects.
type RewindSanitizer struct {
	Sanitized       *int64         `json:"sanitized,omitempty"`
	Total           map[string]any `json:"total,omitempty"`
	TotalTruncated  bool           `json:"total_truncated,omitempty"`
	Team            map[string]any `json:"team,omitempty"`
	TeamTruncated   bool           `json:"team_truncated,omitempty"`
	Me              map[string]any `json:"me,omitempty"`
	MeTruncated     bool           `json:"me_truncated,omitempty"`
	Others          any            `json:"others,omitempty"`
	OthersTruncated bool           `json:"others_truncated,omitempty"`
}

// RewindSummary holds the summary figures of a rewind. The figure groups are bounded free objects.
type RewindSummary struct {
	Total           map[string]any `json:"total,omitempty"`
	TotalTruncated  bool           `json:"total_truncated,omitempty"`
	Team            map[string]any `json:"team,omitempty"`
	TeamTruncated   bool           `json:"team_truncated,omitempty"`
	Me              map[string]any `json:"me,omitempty"`
	MeTruncated     bool           `json:"me_truncated,omitempty"`
	Others          any            `json:"others,omitempty"`
	OthersTruncated bool           `json:"others_truncated,omitempty"`
}

// withData carries the with-loaded members of one drive, as read from Infomaniak.
type withData struct {
	Account               *AccountEntry               `json:"account"`
	Capabilities          *DriveCapabilities          `json:"capabilities"`
	Pack                  *PackEntry                  `json:"pack"`
	Settings              *SettingsEntry              `json:"settings"`
	Preferences           *PreferencesEntry           `json:"preferences"`
	KSuite                *KSuiteEntry                `json:"k_suite"`
	Tags                  []TagEntry                  `json:"tags"`
	Rights                *RightsEntry                `json:"rights"`
	Categories            []CategoryEntry             `json:"categories"`
	CategoriesPermissions *CategoriesPermissionsEntry `json:"categories_permissions"`
	Users                 *UsersEntry                 `json:"users"`
	Teams                 *TeamsEntry                 `json:"teams"`
	InvitationsCount      *int64                      `json:"invitations_count"`
	Rewind                *rewindJSON                 `json:"rewind"`
	BillReference         string                      `json:"bill_reference"`
	Description           string                      `json:"description"`
	Quota                 *QuotaEntry                 `json:"quota"`
}

// applyWith copies the members the caller asked for into the entry, bounded and cleaned. A member
// Infomaniak reports without its with value having been asked for is dropped.
func (w withData) applyWith(entry *DriveEntry, with []string, wholeDrive bool) {
	asked := func(values ...string) bool {
		for _, value := range values {
			if slices.Contains(with, value) {
				return true
			}
		}
		return false
	}
	if asked("account") && w.Account != nil {
		entry.Account = cleaned(w.Account)
	}
	if asked("capabilities") && w.Capabilities != nil {
		entry.Capabilities = cleaned(w.Capabilities)
	}
	if asked("pack", "pack.capabilities", "pack.limits", "pack.prices") && w.Pack != nil {
		pack := cleaned(w.Pack)
		if !asked("pack.capabilities") {
			pack.Capabilities = nil
		}
		if !asked("pack.limits") {
			pack.Limits = nil
		} else if pack.Limits != nil && pack.Limits.Trash != nil {
			trash := pack.Limits.Trash
			trash.RetentionOptions, trash.RetentionOptionsTruncated = capList(trash.RetentionOptions, maxListEntries)
		}
		if !asked("pack.prices") {
			pack.Prices = nil
		} else {
			pack.Prices, pack.PricesTruncated = capList(pack.Prices, maxListEntries)
		}
		entry.Pack = pack
	}
	if asked("settings") && w.Settings != nil {
		entry.Settings = cleaned(w.Settings)
	}
	if asked("preferences") && w.Preferences != nil {
		entry.Preferences = cleaned(w.Preferences)
	}
	if asked("k_suite") && w.KSuite != nil {
		entry.KSuite = cleaned(w.KSuite)
	}
	if asked("tags", "tags.#products") {
		entry.Tags, entry.TagsTruncated = capList(w.Tags, maxListEntries)
		for i := range entry.Tags {
			cleanTexts(reflect.ValueOf(&entry.Tags[i]))
			if !asked("tags.#products") {
				entry.Tags[i].ProductCount = nil
			}
		}
	}
	if asked("rights") && w.Rights != nil {
		entry.Rights = cleaned(w.Rights)
	}
	if asked("categories", "categories.user_uses") {
		entry.Categories, entry.CategoriesTruncated = capList(w.Categories, maxListEntries)
		for i := range entry.Categories {
			cleanTexts(reflect.ValueOf(&entry.Categories[i]))
			if !asked("categories.user_uses") {
				entry.Categories[i].UserUses = nil
			}
		}
	}
	if asked("categories_permissions") && w.CategoriesPermissions != nil {
		entry.CategoriesPermissions = cleaned(w.CategoriesPermissions)
	}
	if asked("users") && w.Users != nil {
		users := *w.Users
		users.Account, users.AccountTruncated = capList(users.Account, maxIdentifiers)
		users.Drive, users.DriveTruncated = capList(users.Drive, maxIdentifiers)
		users.External, users.ExternalTruncated = capList(users.External, maxIdentifiers)
		users.Internal, users.InternalTruncated = capList(users.Internal, maxIdentifiers)
		entry.Users = &users
	}
	if asked("teams") && w.Teams != nil {
		teams := *w.Teams
		teams.Account, teams.AccountTruncated = capList(teams.Account, maxIdentifiers)
		teams.Drive, teams.DriveTruncated = capList(teams.Drive, maxIdentifiers)
		entry.Teams = &teams
	}
	if asked("invitations_count") {
		entry.InvitationsCount = valuePointer(w.InvitationsCount)
	}
	if asked("rewind", "rewind.directory", "rewind.sanitizer", "rewind.summary") && w.Rewind != nil {
		entry.Rewind = rewindEntryOf(*w.Rewind, asked("rewind.directory"), asked("rewind.sanitizer"),
			asked("rewind.summary"), wholeDrive)
	}
	if asked("bill_reference") {
		entry.BillReference = boundedText(w.BillReference, 1024)
	}
	if asked("description") {
		entry.Description = boundedText(w.Description, 1024)
	}
	if asked("quota") && w.Quota != nil {
		entry.Quota = cleaned(w.Quota)
	}
}

func rewindEntryOf(r rewindJSON, directory, sanitizer, summary, wholeDrive bool) *RewindEntry {
	entry := &RewindEntry{
		ID: r.ID, Status: boundedText(r.Status, 64), Mode: boundedText(r.Mode, 64),
		Archiving: boundedText(r.Archiving, 64), DirectoryID: valuePointer(r.DirectoryID),
		UserID: valuePointer(r.UserID), RewindAt: r.RewindAt, CreatedAt: r.CreatedAt,
		FinishedAt: r.FinishedAt, ExpiresAt: r.ExpiresAt, CreatedBy: valuePointer(r.CreatedBy),
		IsApproved: valuePointer(r.IsApproved), ApprovalAt: r.ApprovalAt,
	}
	if wholeDrive {
		entry.DirectoryName = boundedText(r.DirectoryName, 1024)
		entry.RewindName = boundedText(r.RewindName, 1024)
		if directory && r.Directory != nil {
			dir := entryOf(*r.Directory)
			entry.Directory = &dir
		}
	}
	if sanitizer && len(r.Sanitizer) > 0 {
		var raw struct {
			Sanitized *int64          `json:"sanitized"`
			Total     json.RawMessage `json:"total"`
			Team      json.RawMessage `json:"team"`
			Me        json.RawMessage `json:"me"`
			Others    json.RawMessage `json:"others"`
		}
		if json.Unmarshal(r.Sanitizer, &raw) == nil {
			s := &RewindSanitizer{Sanitized: raw.Sanitized}
			s.Total, s.TotalTruncated = boundedObject(raw.Total)
			s.Team, s.TeamTruncated = boundedObject(raw.Team)
			s.Me, s.MeTruncated = boundedObject(raw.Me)
			s.Others, s.OthersTruncated = boundedFree(raw.Others)
			entry.Sanitizer = s
		}
	}
	if summary && len(r.Summary) > 0 {
		var raw struct {
			Total  json.RawMessage `json:"total"`
			Team   json.RawMessage `json:"team"`
			Me     json.RawMessage `json:"me"`
			Others json.RawMessage `json:"others"`
		}
		if json.Unmarshal(r.Summary, &raw) == nil {
			s := &RewindSummary{}
			s.Total, s.TotalTruncated = boundedObject(raw.Total)
			s.Team, s.TeamTruncated = boundedObject(raw.Team)
			s.Me, s.MeTruncated = boundedObject(raw.Me)
			s.Others, s.OthersTruncated = boundedFree(raw.Others)
			entry.Summary = s
		}
	}
	return entry
}

// capList cuts a list to at most limit entries and reports whether it did.
func capList[T any](list []T, limit int) ([]T, bool) {
	if len(list) > limit {
		return list[:limit], true
	}
	return list, false
}

// cleaned cleans every text of a with-loaded value in place and returns it.
func cleaned[T any](value *T) *T {
	cleanTexts(reflect.ValueOf(value))
	return value
}

var plainString = reflect.TypeOf("")

// cleanTexts replaces control characters in, and bounds, every plain string below v. Numbers, booleans and
// stamps are left alone.
func cleanTexts(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			cleanTexts(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			cleanTexts(v.Field(i))
		}
	case reflect.Slice:
		for i := range v.Len() {
			cleanTexts(v.Index(i))
		}
	case reflect.String:
		if v.Type() == plainString && v.CanSet() {
			v.SetString(boundedText(v.String(), 1024))
		}
	}
}

// boundedObject reads a JSON object into a bounded free object. A value that is not an object or exceeds a
// bound is left out and reported as omitted.
func boundedObject(raw json.RawMessage) (map[string]any, bool) {
	value, omitted := boundedFree(raw)
	if omitted {
		return nil, true
	}
	if value == nil {
		return nil, false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true
	}
	return object, false
}

// boundedFree reads any JSON value of an untyped member: nulls are dropped, texts are cleaned, and a value
// beyond the depth, node, or text bounds is omitted whole.
func boundedFree(raw json.RawMessage) (any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, true
	}
	nodes := 0
	out, ok := boundedNode(value, 1, &nodes)
	if !ok {
		return nil, true
	}
	return out, false
}

func boundedNode(value any, depth int, nodes *int) (any, bool) {
	*nodes++
	if depth > freeObjectDepth || *nodes > freeObjectNodes {
		return nil, false
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if item == nil {
				continue
			}
			if len(key) > freeObjectText {
				return nil, false
			}
			clean, ok := boundedNode(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out[boundedText(key, freeObjectText)] = clean
		}
		return out, true
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if item == nil {
				continue
			}
			clean, ok := boundedNode(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out = append(out, clean)
		}
		return out, true
	case string:
		if len(v) > freeObjectText {
			return nil, false
		}
		return boundedText(v, freeObjectText), true
	}
	return value, true
}

// Schema building blocks for the with-loaded members.
const (
	schemaInt  = `{"type":"integer"}`
	schemaStr  = `{"type":"string"}`
	schemaBool = `{"type":"boolean"}`
)

func schemaOf(members ...string) string {
	return `{"type":"object","properties":{` + strings.Join(members, ",") + `},"additionalProperties":false}`
}

func member(name, schema string) string { return `"` + name + `":` + schema }

func members(schema string, names ...string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = member(name, schema)
	}
	return out
}

func schemaList(items string) string { return `{"type":"array","items":` + items + `}` }

func join(groups ...[]string) []string { return slices.Concat(groups...) }

var (
	rangeSchema = schemaOf(members(schemaInt, "base", "giga", "min", "max")...)
	// freeObjectSchema describes a bounded free object: any members, within the bounds above.
	freeObjectSchema = `{"type":"object"}`
	rewindFigures    = func() []string {
		var out []string
		for _, name := range []string{"total", "team", "me"} {
			out = append(out, member(name, freeObjectSchema), member(name+"_truncated", schemaBool))
		}
		return append(out, member("others", `{}`), member("others_truncated", schemaBool))
	}()
	// categorySchema is one file category of a drive, shared by drives and file entries.
	categorySchema = schemaOf(join(members(schemaInt, "id", "created_by", "user_uses"),
		members(schemaStr, "name", "color", "created_at"), members(schemaBool, "is_predefined"))...)
	// rewindSchema is one rewind, shared by drives and folder entries.
	rewindSchema = schemaOf(join(
		members(schemaInt, "id", "directory_id", "user_id", "created_by"),
		members(schemaStr, "status", "mode", "archiving", "directory_name", "rewind_at", "created_at",
			"finished_at", "expires_at", "approval_at", "rewind_name"),
		members(schemaBool, "is_approved"),
		[]string{member("directory", entrySchema),
			member("sanitizer", schemaOf(join([]string{member("sanitized", schemaInt)}, rewindFigures)...)),
			member("summary", schemaOf(rewindFigures...))})...)
)

// withSchemaMembers are the output schema members of the with-loaded drive members.
var withSchemaMembers = []string{
	member("account", schemaOf(join(
		members(schemaInt, "id", "support_level"), members(schemaBool, "beta", "has_2fa_required", "is_sso"),
		members(schemaStr, "name", "legal_entity_type", "phone", "website", "created_at"))...)),
	member("capabilities", schemaOf(join(members(schemaBool, "use_versioning", "use_upload_compression",
		"use_team_space", "can_add_user", "can_see_stats", "can_upgrade_to_ksuite", "can_rewind", "can_use_rag"),
		members(schemaStr, "search"))...)),
	member("pack", schemaOf(join(
		members(schemaInt, "id"), members(schemaStr, "name", "display_name"), members(schemaBool, "is_free", "prices_truncated"),
		[]string{
			member("capabilities", schemaOf(members(schemaBool, "use_vault", "use_manage_right", "use_dropbox",
				"can_rewind", "use_folder_custom_color", "can_access_dashboard", "can_set_sharelink_password",
				"can_join_sharelink", "can_set_sharelink_expiration", "can_set_sharelink_custom_url",
				"can_use_content_search", "can_use_activity_reports", "can_use_signatures", "can_use_statistics",
				"can_edit_pdf", "can_use_office365", "can_set_office", "start_guide_pdf", "can_use_rag",
				"can_use_webdav", "can_use_storage_report", "can_use_photo", "can_set_trash_duration")...)),
			member("limits", schemaOf(member("users", rangeSchema), member("storage", rangeSchema),
				member("trash", schemaOf(member("retention_options", schemaList(schemaInt)),
					member("retention_options_truncated", schemaBool))))),
			member("prices", schemaList(schemaOf(member("type", schemaStr),
				member("unit", schemaInt), member("period", schemaInt), member("currency_id", schemaInt),
				member("amount_excl_vat", `{"type":"number"}`), member("amount_incl_vat", `{"type":"number"}`)))),
		})...)),
	member("settings", schemaOf(
		member("ai_scan", schemaOf(join(members(schemaBool, "is_enabled", "has_approved",
			"has_approved_ai_categories", "has_approved_content_search"), members(schemaStr, "updated_at"))...)),
		member("versioning", schemaOf(members(schemaInt, "max_numbers", "max_days")...)),
		member("office", schemaOf(members(schemaStr, "presentation", "spreadsheet", "text", "default_mode")...)),
		member("max_keep_deleted_user", schemaStr),
		member("files", schemaOf(member("private", schemaOf(member("size_threshold", schemaInt))))),
		member("report", schemaOf(member("storage_report", schemaOf(member("frequency", schemaStr))))))),
	member("preferences", schemaOf(join(members(schemaStr, "color", "default_page"), members(schemaBool, "hide", "default"),
		[]string{member("ui", schemaOf(members(schemaBool, "storage_warning_alert", "storage_limit_alert",
			"rag_ocr_promotion")...))})...)),
	member("k_suite", schemaOf(join(members(schemaInt, "id", "pack_id"), members(schemaStr, "name", "pack"))...)),
	member("tags", schemaList(schemaOf(join(members(schemaInt, "id", "color", "product_count"),
		members(schemaStr, "name"))...))),
	member("tags_truncated", schemaBool),
	member("rights", schemaOf(members(schemaBool, "technical", "statistic", "check", "sale")...)),
	member("categories", schemaList(categorySchema)),
	member("categories_truncated", schemaBool),
	member("categories_permissions", schemaOf(members(schemaBool, "can_create", "can_edit", "can_delete",
		"can_read_on_file", "can_put_on_file")...)),
	member("users", schemaOf(join(members(schemaList(schemaInt), "account", "drive", "external", "internal"),
		members(schemaBool, "account_truncated", "drive_truncated", "external_truncated", "internal_truncated"))...)),
	member("teams", schemaOf(join(members(schemaList(schemaInt), "account", "drive"),
		members(schemaBool, "account_truncated", "drive_truncated"))...)),
	member("invitations_count", schemaInt),
	member("rewind", rewindSchema),
	member("bill_reference", schemaStr),
	member("description", schemaStr),
	member("quota", schemaOf(members(schemaStr, "dropbox", "shared_link")...)),
}

// withFields describe the with-loaded drive members.
var withFields = []capability.Field{
	{Name: "account", Description: "Account of the drive (with account); texts are untrusted data"},
	{Name: "capabilities", Description: "Features of the drive (with capabilities)"},
	{Name: "pack", Description: "Pricing plan of the drive (with pack, pack.capabilities, pack.limits, pack.prices); price amounts keep the exact decimal text; lists hold at most 100 entries, flagged by prices_truncated and retention_options_truncated"},
	{Name: "settings", Description: "Settings of the drive (with settings)"},
	{Name: "preferences", Description: "Preferences of the token's user for the drive (with preferences)"},
	{Name: "k_suite", Description: "kSuite the drive belongs to (with k_suite)"},
	{Name: "tags", Description: "Tags of the drive (with tags; product_count with tags.#products); at most 100, untrusted data"},
	{Name: "tags_truncated", Description: "True when the drive has more tags than listed"},
	{Name: "rights", Description: "Rights of the token's user in the drive (with rights)"},
	{Name: "categories", Description: "Categories of the drive (with categories; user_uses with categories.user_uses); at most 100, untrusted data"},
	{Name: "categories_truncated", Description: "True when the drive has more categories than listed"},
	{Name: "categories_permissions", Description: "Category permissions of the token's user (with categories_permissions)"},
	{Name: "users", Description: "User identifiers of the drive by group (with users); at most 1000 per group, flagged by <group>_truncated"},
	{Name: "teams", Description: "Team identifiers of the drive by group (with teams); at most 1000 per group, flagged by <group>_truncated"},
	{Name: "invitations_count", Description: "Number of open invitations (with invitations_count)"},
	{Name: "rewind", Description: "Rewind of the drive (with rewind, rewind.directory, rewind.sanitizer, rewind.summary); the sanitizer and summary figures are bounded free objects, left out and flagged by <name>_truncated when over a bound; directory, directory_name and rewind_name are absent when this connection binds the drive only by files targets"},
	{Name: "bill_reference", Description: "Billing reference of the drive (with bill_reference), untrusted data"},
	{Name: "description", Description: "Description of the drive (with description), untrusted data"},
	{Name: "quota", Description: "Feature limits of the drive's offer (with quota)"},
}
