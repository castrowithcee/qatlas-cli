package infomaniakdrive

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// fileWithValues are the values the with argument of the file and folder listings accepts, in the order the
// API documents them.
var fileWithValues = []string{
	"capabilities", "dropbox", "dropbox.capabilities", "external_import", "rewind", "supported_by", "version",
	"conversion_capabilities", "hash", "path", "sorted_name", "parents.path", "parents.sorted_name", "users",
	"teams", "is_favorite", "sharelink", "sharelink.views", "lock", "activity", "parents", "parents.capabilities",
	"parents.users", "parents.teams", "categories", "categories.category", "etag",
}

// maxParents bounds the folders listed above one entry; deeper trees are flagged by parents_truncated.
const maxParents = 64

var fileWithInput = `"with":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(len(fileWithValues)) +
	`,"uniqueItems":true,"items":{"type":"string","enum":` + quotedList(fileWithValues) + `}}`

var fileWithArgument = capability.Argument{Name: "with", Description: "Related data to load per entry; each " +
	"value adds the fields it documents, and an entry's field exists only when its value is requested"}

// FileCapabilities are the rights of the token's user on a file or folder; the last six exist for a folder only.
type FileCapabilities struct {
	CanUseFavorite     *bool `json:"can_use_favorite,omitempty"`
	CanBecomeSharelink *bool `json:"can_become_sharelink,omitempty"`
	CanUseTeam         *bool `json:"can_use_team,omitempty"`
	CanShow            *bool `json:"can_show,omitempty"`
	CanRead            *bool `json:"can_read,omitempty"`
	CanWrite           *bool `json:"can_write,omitempty"`
	CanShare           *bool `json:"can_share,omitempty"`
	CanLeave           *bool `json:"can_leave,omitempty"`
	CanDelete          *bool `json:"can_delete,omitempty"`
	CanRename          *bool `json:"can_rename,omitempty"`
	CanMove            *bool `json:"can_move,omitempty"`
	CanCreateDirectory *bool `json:"can_create_directory,omitempty"`
	CanCreateFile      *bool `json:"can_create_file,omitempty"`
	CanUpload          *bool `json:"can_upload,omitempty"`
	CanMoveInto        *bool `json:"can_move_into,omitempty"`
	CanBecomeDropbox   *bool `json:"can_become_dropbox,omitempty"`
	Colorable          *bool `json:"colorable,omitempty"`
}

// FileVersion describes the versions of a file.
type FileVersion struct {
	IsMultiple  *bool  `json:"is_multiple,omitempty"`
	Number      *int64 `json:"number,omitempty"`
	TotalSize   *int64 `json:"total_size,omitempty"`
	KeepForever *bool  `json:"keep_forever,omitempty"`
}

// ConversionCapabilities say how a file converts on download or when opened in OnlyOffice.
type ConversionCapabilities struct {
	WhenDownloading             *bool    `json:"when_downloading,omitempty"`
	DownloadExtensions          []string `json:"download_extensions,omitempty"`
	DownloadExtensionsTruncated bool     `json:"download_extensions_truncated,omitempty"`
	WhenOnlyofficeOpening       *bool    `json:"when_onlyoffice_opening,omitempty"`
	OnlyofficeExtension         string   `json:"onlyoffice_extension,omitempty"`
}

// FileActivity is the last access of the token's user.
type FileActivity struct {
	LastAccessedAt stamp `json:"last_accessed_at,omitempty"`
}

// FileLock is the lock of a file. It deliberately has no token: the lock token is never decoded.
type FileLock struct {
	LockedAt    stamp  `json:"locked_at,omitempty"`
	UnlockedAt  stamp  `json:"unlocked_at,omitempty"`
	Description string `json:"description,omitempty"`
}

// FileCategory is one category set on a file; category needs with=categories.category.
type FileCategory struct {
	CategoryID      int64          `json:"category_id,omitempty"`
	AddedAt         stamp          `json:"added_at,omitempty"`
	UserValidation  string         `json:"user_validation,omitempty"`
	IsGeneratedByAI *bool          `json:"is_generated_by_ai,omitempty"`
	UserID          int64          `json:"user_id,omitempty"`
	Category        *CategoryEntry `json:"category,omitempty"`
}

// scalarText reads a documented text that may arrive as another JSON scalar, keeping its literal form.
type scalarText string

func (s *scalarText) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		*s = scalarText(boundedText(text, 256))
		return nil
	}
	if bytes.Equal(trimmed, []byte("null")) || len(trimmed) == 0 || trimmed[0] == '{' || trimmed[0] == '[' {
		return nil
	}
	*s = scalarText(boundedText(string(trimmed), 64))
	return nil
}

// ExternalImportEntry is the import a folder was created by.
type ExternalImportEntry struct {
	ID                int64      `json:"id,omitempty"`
	Application       string     `json:"application,omitempty"`
	AccountName       string     `json:"account_name,omitempty"`
	Status            string     `json:"status,omitempty"`
	ErrorCode         string     `json:"error_code,omitempty"`
	Path              string     `json:"path,omitempty"`
	DirectoryID       *int64     `json:"directory_id,omitempty"`
	HasSharedFiles    scalarText `json:"has_shared_files,omitempty"`
	CreatedAt         stamp      `json:"created_at,omitempty"`
	UpdatedAt         stamp      `json:"updated_at,omitempty"`
	CountSuccessFiles *int64     `json:"count_success_files,omitempty"`
	CountFailedFiles  *int64     `json:"count_failed_files,omitempty"`
}

// ShareLinkCapabilities are what holders of a share link may do.
type ShareLinkCapabilities struct {
	CanEdit          *bool `json:"can_edit,omitempty"`
	CanSeeStats      *bool `json:"can_see_stats,omitempty"`
	CanSeeInfo       *bool `json:"can_see_info,omitempty"`
	CanDownload      *bool `json:"can_download,omitempty"`
	CanComment       *bool `json:"can_comment,omitempty"`
	CanRequestAccess *bool `json:"can_request_access,omitempty"`
}

// FileShareLink is the share link of an entry. It deliberately has no url: the link grants access without
// an account, and these tools need no explicit tools listing, so it is never decoded.
type FileShareLink struct {
	FileID        int64                  `json:"file_id,omitempty"`
	Right         string                 `json:"right,omitempty"`
	ValidUntil    stamp                  `json:"valid_until,omitempty"`
	CreatedBy     *int64                 `json:"created_by,omitempty"`
	CreatedAt     stamp                  `json:"created_at,omitempty"`
	UpdatedAt     stamp                  `json:"updated_at,omitempty"`
	Capabilities  *ShareLinkCapabilities `json:"capabilities,omitempty"`
	AccessBlocked *bool                  `json:"access_blocked,omitempty"`
	Views         *int64                 `json:"views,omitempty"`
}

// DropboxCapabilities are the settings of a dropbox; the password itself is never part of them.
type DropboxCapabilities struct {
	HasPassword     *bool  `json:"has_password,omitempty"`
	HasNotification *bool  `json:"has_notification,omitempty"`
	HasValidity     *bool  `json:"has_validity,omitempty"`
	HasSizeLimit    *bool  `json:"has_size_limit,omitempty"`
	Validity        string `json:"validity,omitempty"`
	Size            string `json:"size,omitempty"`
}

// FileDropbox is the dropbox of a folder. It deliberately has no url and no uuid: both grant upload access
// without an account, so they are never decoded.
type FileDropbox struct {
	ID             int64                `json:"id,omitempty"`
	Name           string               `json:"name,omitempty"`
	UsersCount     *int64               `json:"users_count,omitempty"`
	CreatedBy      *int64               `json:"created_by,omitempty"`
	CreatedAt      stamp                `json:"created_at,omitempty"`
	UpdatedAt      stamp                `json:"updated_at,omitempty"`
	LastUploadedAt stamp                `json:"last_uploaded_at,omitempty"`
	Capabilities   *DropboxCapabilities `json:"capabilities,omitempty"`
}

// fileWithJSON carries the with-loaded members of one file or folder, as read from Infomaniak.
type fileWithJSON struct {
	Capabilities           *FileCapabilities       `json:"capabilities"`
	Version                *FileVersion            `json:"version"`
	ConversionCapabilities *ConversionCapabilities `json:"conversion_capabilities"`
	Hash                   string                  `json:"hash"`
	Path                   string                  `json:"path"`
	SortedName             string                  `json:"sorted_name"`
	IsFavorite             *bool                   `json:"is_favorite"`
	Activity               *FileActivity           `json:"activity"`
	Lock                   *FileLock               `json:"lock"`
	Categories             []FileCategory          `json:"categories"`
	Etag                   string                  `json:"etag"`
	SupportedBy            json.RawMessage         `json:"supported_by"`
	ExternalImport         *ExternalImportEntry    `json:"external_import"`
	Rewind                 *rewindJSON             `json:"rewind"`
	Users                  []int64                 `json:"users"`
	Teams                  []int64                 `json:"teams"`
	Sharelink              *FileShareLink          `json:"sharelink"`
	Dropbox                *FileDropbox            `json:"dropbox"`
}

// EntryWith holds the with-loaded members of an entry; each exists only when its with value was asked for.
type EntryWith struct {
	Capabilities           *FileCapabilities       `json:"capabilities,omitempty"`
	Version                *FileVersion            `json:"version,omitempty"`
	ConversionCapabilities *ConversionCapabilities `json:"conversion_capabilities,omitempty"`
	Hash                   string                  `json:"hash,omitempty"`
	Path                   string                  `json:"path,omitempty"`
	SortedName             string                  `json:"sorted_name,omitempty"`
	IsFavorite             *bool                   `json:"is_favorite,omitempty"`
	Activity               *FileActivity           `json:"activity,omitempty"`
	Lock                   *FileLock               `json:"lock,omitempty"`
	Categories             []FileCategory          `json:"categories,omitempty"`
	CategoriesTruncated    bool                    `json:"categories_truncated,omitempty"`
	Etag                   string                  `json:"etag,omitempty"`
	SupportedBy            []string                `json:"supported_by,omitempty"`
	SupportedByTruncated   bool                    `json:"supported_by_truncated,omitempty"`
	ExternalImport         *ExternalImportEntry    `json:"external_import,omitempty"`
	Rewind                 *RewindEntry            `json:"rewind,omitempty"`
	Users                  []int64                 `json:"users,omitempty"`
	UsersTruncated         bool                    `json:"users_truncated,omitempty"`
	Teams                  []int64                 `json:"teams,omitempty"`
	TeamsTruncated         bool                    `json:"teams_truncated,omitempty"`
	Parents                []Entry                 `json:"parents,omitempty"`
	ParentsTruncated       bool                    `json:"parents_truncated,omitempty"`
	Sharelink              *FileShareLink          `json:"sharelink,omitempty"`
	Dropbox                *FileDropbox            `json:"dropbox,omitempty"`
}

// reach is how far the connection reaches into one drive: the whole drive, or only its files targets.
type reach struct {
	whole    bool
	isTarget func(fileID int64) bool
}

func (c *Client) reachOf(driveID int64) reach {
	return reach{whole: c.scope.wholeDrive(driveID),
		isTarget: func(fileID int64) bool { return c.scope.isSubtreeRoot(driveID, fileID) }}
}

// entryWith maps one file or folder and adds the members its with values asked for.
func (c *Client) entryWith(driveID int64, f fileJSON, with []string) Entry {
	entry := entryOf(f)
	if len(with) > 0 {
		f.applyWith(&entry, with, c.reachOf(driveID))
	}
	return entry
}

// withQuery joins the with values of a request: a fixed value the tool always needs first, then the
// caller's values without it.
func withQuery(fixed string, with []string) string {
	values := make([]string, 0, len(with)+1)
	if fixed != "" {
		values = append(values, fixed)
	}
	for _, value := range with {
		if value != fixed {
			values = append(values, value)
		}
	}
	return strings.Join(values, ",")
}

// applyWith copies the members the caller asked for into the entry, bounded and cleaned. A member
// Infomaniak reports without its with value having been asked for is dropped. For a drive bound only by
// files targets, members that can name a folder outside them are left out, and parents keeps only the
// targets and the folders below them.
func (f fileJSON) applyWith(entry *Entry, with []string, r reach) {
	asked := func(values ...string) bool {
		return slices.ContainsFunc(values, func(v string) bool { return slices.Contains(with, v) })
	}
	w := f.fileWithJSON
	if asked("capabilities") {
		entry.Capabilities = valuePointer(w.Capabilities)
	}
	if asked("version") {
		entry.Version = valuePointer(w.Version)
	}
	if asked("conversion_capabilities") && w.ConversionCapabilities != nil {
		conversion := cleaned(valuePointer(w.ConversionCapabilities))
		conversion.DownloadExtensions, conversion.DownloadExtensionsTruncated =
			capList(conversion.DownloadExtensions, maxListEntries)
		entry.ConversionCapabilities = conversion
	}
	if asked("hash") {
		entry.Hash = boundedText(w.Hash, 256)
	}
	if asked("path") && r.whole {
		entry.Path = boundedText(w.Path, maxPathLength)
	}
	if asked("sorted_name") {
		entry.SortedName = boundedText(w.SortedName, 1024)
	}
	if asked("is_favorite") {
		entry.IsFavorite = valuePointer(w.IsFavorite)
	}
	if asked("activity") {
		entry.Activity = valuePointer(w.Activity)
	}
	if asked("lock") && w.Lock != nil {
		entry.Lock = cleaned(valuePointer(w.Lock))
	}
	if asked("categories", "categories.category") {
		categories, truncated := capList(w.Categories, maxListEntries)
		entry.Categories, entry.CategoriesTruncated = slices.Clone(categories), truncated
		for i := range entry.Categories {
			category := &entry.Categories[i]
			if asked("categories.category") && category.Category != nil {
				category.Category = valuePointer(category.Category)
				category.Category.UserUses = nil
			} else {
				category.Category = nil
			}
			cleanTexts(reflect.ValueOf(category))
		}
	}
	if asked("etag") {
		entry.Etag = boundedText(w.Etag, 256)
	}
	if asked("supported_by") {
		entry.SupportedBy, entry.SupportedByTruncated = supportedBy(w.SupportedBy)
	}
	if asked("external_import") && w.ExternalImport != nil {
		imported := cleaned(valuePointer(w.ExternalImport))
		if !r.whole {
			imported.Path = ""
		}
		entry.ExternalImport = imported
	}
	if asked("rewind") && w.Rewind != nil {
		// The files listings offer no rewind.directory, rewind.sanitizer or rewind.summary.
		entry.Rewind = rewindEntryOf(*w.Rewind, false, false, false, r.whole)
	}
	if asked("users") {
		entry.Users, entry.UsersTruncated = capList(slices.Clone(w.Users), maxIdentifiers)
	}
	if asked("teams") {
		entry.Teams, entry.TeamsTruncated = capList(slices.Clone(w.Teams), maxIdentifiers)
	}
	if asked("parents", "parents.path", "parents.sorted_name", "parents.capabilities", "parents.users",
		"parents.teams") && f.Parents != nil {
		entry.Parents, entry.ParentsTruncated = parentsOf(*f.Parents, with, r)
	}
	if asked("sharelink", "sharelink.views") && w.Sharelink != nil {
		link := cleaned(valuePointer(w.Sharelink))
		if !asked("sharelink.views") {
			link.Views = nil
		}
		entry.Sharelink = link
	}
	if asked("dropbox", "dropbox.capabilities") && w.Dropbox != nil {
		dropbox := cleaned(valuePointer(w.Dropbox))
		if !asked("dropbox.capabilities") {
			dropbox.Capabilities = nil
		}
		entry.Dropbox = dropbox
	}
}

// parentsOf maps the folders above an entry, each without parents of its own. For a drive bound only by
// files targets it keeps a folder only when it is a target or its chain of parent_id reaches one; the order
// of the list is not relied on, and a broken or looping chain keeps nothing.
func parentsOf(parents []fileJSON, with []string, r reach) ([]Entry, bool) {
	kept := parents
	if !r.whole {
		byID := make(map[int64]fileJSON, len(parents))
		for _, p := range parents {
			byID[p.ID] = p
		}
		kept = nil
		for _, p := range parents {
			cur, ok := p, true
			for steps := 0; ok && steps <= len(parents) && !r.isTarget(cur.ID); steps++ {
				cur, ok = byID[cur.ParentID]
			}
			if ok && r.isTarget(cur.ID) {
				kept = append(kept, p)
			}
		}
	}
	kept, truncated := capList(kept, maxParents)
	out := make([]Entry, 0, len(kept))
	for _, p := range kept {
		parent := entryOf(p)
		p.Parents = nil
		parentWith := make([]string, 0, len(with))
		for _, value := range with {
			if name, ok := strings.CutPrefix(value, "parents."); ok {
				parentWith = append(parentWith, name)
			}
		}
		p.applyWith(&parent, parentWith, r)
		out = append(out, parent)
	}
	return out, truncated
}

// supportedBy reads the applications that support a file: a list, or a single value as Infomaniak also
// documents it.
func supportedBy(raw json.RawMessage) ([]string, bool) {
	var list []string
	var single string
	switch {
	case json.Unmarshal(raw, &single) == nil && single != "":
		list = []string{single}
	case json.Unmarshal(raw, &list) == nil:
	default:
		var mixed []any
		if json.Unmarshal(raw, &mixed) != nil {
			return nil, false
		}
		for _, item := range mixed {
			if text, ok := item.(string); ok {
				list = append(list, text)
			}
		}
	}
	list = slices.DeleteFunc(list, func(s string) bool { return s == "" })
	list, truncated := capList(list, maxListEntries)
	for i := range list {
		list[i] = boundedText(list[i], 64)
	}
	return list, truncated
}

var (
	fileCapabilitiesSchema = schemaOf(members(schemaBool, "can_use_favorite", "can_become_sharelink",
		"can_use_team", "can_show", "can_read", "can_write", "can_share", "can_leave", "can_delete", "can_rename",
		"can_move", "can_create_directory", "can_create_file", "can_upload", "can_move_into",
		"can_become_dropbox", "colorable")...)
	identifiersSchema = schemaList(schemaInt)

	// parentEntrySchema is one folder above an entry: an entry without parents of its own, with the members
	// the parents.* with values load.
	parentEntrySchema = `{"type":"object","properties":{` + entryProperties + `,` + strings.Join([]string{
		member("path", schemaStr), member("sorted_name", schemaStr),
		member("capabilities", fileCapabilitiesSchema),
		member("users", identifiersSchema), member("users_truncated", schemaBool),
		member("teams", identifiersSchema), member("teams_truncated", schemaBool),
	}, ",") + `},"required":["id","name","type","parent_id"],"additionalProperties":false}`
)

// fileWithSchemaMembers are the output schema members of the with-loaded entry members, by member name.
var fileWithSchemaMembers = [][2]string{
	{"capabilities", fileCapabilitiesSchema},
	{"version", schemaOf(join(members(schemaBool, "is_multiple", "keep_forever"),
		members(schemaInt, "number", "total_size"))...)},
	{"conversion_capabilities", schemaOf(join(
		members(schemaBool, "when_downloading", "when_onlyoffice_opening", "download_extensions_truncated"),
		[]string{member("download_extensions", schemaList(schemaStr)), member("onlyoffice_extension", schemaStr)})...)},
	{"hash", schemaStr},
	{"path", schemaStr},
	{"sorted_name", schemaStr},
	{"is_favorite", schemaBool},
	{"activity", schemaOf(member("last_accessed_at", schemaStr))},
	{"lock", schemaOf(members(schemaStr, "locked_at", "unlocked_at", "description")...)},
	{"categories", schemaList(schemaOf(join(members(schemaInt, "category_id", "user_id"),
		members(schemaStr, "added_at", "user_validation"), members(schemaBool, "is_generated_by_ai"),
		[]string{member("category", categorySchema)})...))},
	{"categories_truncated", schemaBool},
	{"etag", schemaStr},
	{"supported_by", schemaList(schemaStr)},
	{"supported_by_truncated", schemaBool},
	{"external_import", schemaOf(join(
		members(schemaInt, "id", "directory_id", "count_success_files", "count_failed_files"),
		members(schemaStr, "application", "account_name", "status", "error_code", "path", "has_shared_files",
			"created_at", "updated_at"))...)},
	{"rewind", rewindSchema},
	{"users", identifiersSchema},
	{"users_truncated", schemaBool},
	{"teams", identifiersSchema},
	{"teams_truncated", schemaBool},
	{"parents", schemaList(parentEntrySchema)},
	{"parents_truncated", schemaBool},
	{"sharelink", schemaOf(join(members(schemaInt, "file_id", "created_by", "views"),
		members(schemaStr, "right", "valid_until", "created_at", "updated_at"), members(schemaBool, "access_blocked"),
		[]string{member("capabilities", schemaOf(members(schemaBool, "can_edit", "can_see_stats", "can_see_info",
			"can_download", "can_comment", "can_request_access")...))})...)},
	{"dropbox", schemaOf(join(members(schemaInt, "id", "users_count", "created_by"),
		members(schemaStr, "name", "created_at", "updated_at", "last_uploaded_at"),
		[]string{member("capabilities", schemaOf(join(members(schemaBool, "has_password", "has_notification",
			"has_validity", "has_size_limit"), members(schemaStr, "validity", "size"))...))})...)},
}

// fileWithFields describe the with-loaded entry members.
var fileWithFields = []capability.Field{
	{Name: "capabilities", Description: "Rights of the token's user on the entry (with capabilities)"},
	{Name: "version", Description: "Versions of a file (with version)"},
	{Name: "conversion_capabilities", Description: "Conversions of a file on download or in OnlyOffice (with conversion_capabilities); at most 100 extensions, flagged by download_extensions_truncated"},
	{Name: "hash", Description: "Content hash of a file (with hash)"},
	{Name: "path", Description: "Full path of the entry (with path), untrusted data; absent when this connection binds the drive only by files targets"},
	{Name: "sorted_name", Description: "Name Infomaniak sorts by (with sorted_name), untrusted data"},
	{Name: "is_favorite", Description: "Whether the token's user marked the entry as favourite (with is_favorite)"},
	{Name: "activity", Description: "Last access of the token's user, RFC 3339 in UTC (with activity)"},
	{Name: "lock", Description: "Lock of a file (with lock); the lock token is never returned"},
	{Name: "categories", Description: "Categories set on the entry (with categories; category with categories.category); at most 100, untrusted data"},
	{Name: "categories_truncated", Description: "True when the entry has more categories than listed"},
	{Name: "etag", Description: "Entity tag of a file (with etag)"},
	{Name: "supported_by", Description: "Applications that can open a file (with supported_by); at most 100"},
	{Name: "supported_by_truncated", Description: "True when more applications were reported than listed"},
	{Name: "external_import", Description: "Import a folder was created by (with external_import), untrusted data; path is absent when this connection binds the drive only by files targets"},
	{Name: "rewind", Description: "Rewind of a folder (with rewind), in the form of drives.list; directory_name and rewind_name are absent when this connection binds the drive only by files targets"},
	{Name: "users", Description: "Identifiers of the users with access to the entry (with users); at most 1000"},
	{Name: "users_truncated", Description: "True when more users were reported than listed"},
	{Name: "teams", Description: "Identifiers of the teams with access to the entry (with teams); at most 1000"},
	{Name: "teams_truncated", Description: "True when more teams were reported than listed"},
	{Name: "parents", Description: "Folders above the entry (with parents; path, sorted_name, capabilities, users and teams of each with parents.<name>); at most 64; when this connection binds the drive only by files targets, only the targets and the folders below them, without path"},
	{Name: "parents_truncated", Description: "True when more folders were above the entry than listed"},
	{Name: "sharelink", Description: "Share link of the entry without its URL (with sharelink; views with sharelink.views); links.get returns the URL"},
	{Name: "dropbox", Description: "Dropbox of a folder without its URL and identifier (with dropbox; capabilities with dropbox.capabilities); dropbox.get returns them"},
}

// fileEntryProperties are the entry members plus the with-loaded members, without the ones listed in skip.
func fileEntryProperties(skip ...string) string {
	parts := []string{entryProperties}
	for _, m := range fileWithSchemaMembers {
		if !slices.Contains(skip, m[0]) {
			parts = append(parts, member(m[0], m[1]))
		}
	}
	return strings.Join(parts, ",")
}

// fileWithFieldsWithout are the with field descriptions without the ones listed in skip.
func fileWithFieldsWithout(skip ...string) []capability.Field {
	return slices.DeleteFunc(slices.Clone(fileWithFields), func(f capability.Field) bool {
		return slices.Contains(skip, f.Name)
	})
}

// fileEntrySchema is one entry of the file and folder reads that take with.
var fileEntrySchema = `{"type":"object","properties":{` + fileEntryProperties() + `},` +
	`"required":["id","name","type","parent_id"],"additionalProperties":false}`
