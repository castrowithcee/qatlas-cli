package nextcloud

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The further namespaces of the file properties.
const (
	ocsNS = "http://open-collaboration-services.org/ns"
	ocmNS = "http://open-cloud-mesh.org/ns"
)

// Bounds of the nested and multi-valued file properties. A value over a bound is left out and named in
// omitted_fields, never cut, so a caller never mistakes a partial list for a complete one.
const (
	maxTreeDepth   = 4
	maxTreeNodes   = 1024
	maxListItems   = 100
	maxJSONDepth   = 4
	maxJSONNodes   = 100
	maxJSONString  = 256
	maxPermItems   = 32
	maxShortValue  = 256
	maxACLRules    = 50
	maxACLMembers  = 10
	fieldSharees   = "sharees"
	fieldShareType = "share_types"
	fieldTags      = "tags"
	fieldChecksums = "checksums"
	fieldShareAttr = "share_attributes"
	fieldOCMPerms  = "ocm_share_permissions"
	fieldACLList   = "acl_list"
	fieldInhACL    = "inherited_acl_list"
)

// The keys of the file properties added to the normalised entry.
const (
	propGlobalID        = "globalid"
	propCreationDate    = "creationdate"
	propContentLanguage = "contentlanguage"
	propQuotaUsed       = "quotaused"
	propQuotaAvailable  = "quotaavailable"
	propCreationTime    = "creationtime"
	propUploadTime      = "uploadtime"
	propLastActivity    = "lastactivity"
	propFavorite        = "favorite"
	propSharePerms      = "sharepermissions"
	propOCMPerms        = "ocmsharepermissions"
	propOwnerID         = "ownerid"
	propOwnerName       = "ownername"
	propNote            = "note"
	propShareAttrs      = "shareattributes"
	propHideDownload    = "hidedownload"
	propMountType       = "mounttype"
	propIsMountRoot     = "ismountroot"
	propIsFederated     = "isfederated"
	propIsEncrypted     = "isencrypted"
	propCommentsCount   = "commentscount"
	propCommentsUnread  = "commentsunread"
	propCommentsHref    = "commentshref"
	propHasPreview      = "haspreview"
	propHidden          = "hidden"
	propMetadataETag    = "metadataetag"
	propFingerprint     = "datafingerprint"
	propContainedFiles  = "containedfiles"
	propContainedDirs   = "containedfolders"

	treeSharees   = "sharees"
	treeShareType = "sharetypes"
	treeTags      = "tags"
	treeChecksums = "checksums"
	treeACLList   = "acllist"
	treeInhACL    = "inheritedacllist"
)

// The keys of the app and server feature properties.
const (
	propGroupFolderID     = "groupfolderid"
	propACLEnabled        = "aclenabled"
	propACLCanManage      = "aclcanmanage"
	propLock              = "lock"
	propLockOwnerType     = "lockownertype"
	propLockOwner         = "lockowner"
	propLockOwnerName     = "lockownername"
	propLockOwnerEditor   = "lockowneditor"
	propLockTime          = "locktime"
	propLockTimeout       = "locktimeout"
	propRichWorkspaceFile = "richworkspacefile"
	propReminderDue       = "reminderdue"
	propVersionLabel      = "versionlabel"
	propVersionAuthor     = "versionauthor"
)

// extraProps are properties stored under a second key besides the one in wantedProps. oc:id is the tag
// identifier of the system tag tools and the global file identifier of an entry.
var extraProps = map[xml.Name]string{
	{Space: ocNS, Local: "id"}: propGlobalID,
}

// treeProps are the properties whose value is a nested or repeated element structure rather than text.
// Supporting another one of this kind needs an entry here and a mapper in fileProps.
var treeProps = map[xml.Name]string{
	{Space: ncNS, Local: "sharees"}:            treeSharees,
	{Space: ocNS, Local: "share-types"}:        treeShareType,
	{Space: ocNS, Local: "tags"}:               treeTags,
	{Space: ocNS, Local: "checksums"}:          treeChecksums,
	{Space: ncNS, Local: "acl-list"}:           treeACLList,
	{Space: ncNS, Local: "inherited-acl-list"}: treeInhACL,
}

// propNode is one element of a nested property value, reduced to its name, its text, and its children.
type propNode struct {
	name     xml.Name
	text     string
	children []*propNode
}

// child returns the text of the first child with this local name.
func (n *propNode) child(local string) string {
	for _, c := range n.children {
		if c.name.Local == local {
			return strings.TrimSpace(c.text)
		}
	}
	return ""
}

// treeCapture builds the propNode tree of one nested property during the token walk.
type treeCapture struct {
	key   string
	root  *propNode
	path  []*propNode
	count int
	over  bool
}

func (t *treeCapture) start(name xml.Name) {
	node := &propNode{name: name}
	t.count++
	switch {
	case t.root == nil:
		t.root = node
	case len(t.path) >= maxTreeDepth || t.count > maxTreeNodes:
		t.over = true
	default:
		parent := t.path[len(t.path)-1]
		parent.children = append(parent.children, node)
	}
	t.path = append(t.path, node)
}

func (t *treeCapture) text(data []byte) {
	top := t.path[len(t.path)-1]
	if len(top.text)+len(data) <= maxTextBytes {
		top.text += string(data)
	}
}

// fileProp is one property of the full file entry: its request form, where it may be asked for, and how
// it lands in the entry. A later property of this kind needs one row here plus its field.
type fileProp struct {
	xml string
	// searchXML replaces xml in a search when Nextcloud documents another form there.
	searchXML string
	// search marks a property Nextcloud documents as selectable in a search; only these are requested
	// there and only these are reported there.
	search bool
	// searchOnly marks a property that is asked for in a search only.
	searchOnly bool
	// folder marks a property that is reported for folders only.
	folder bool
	set    func(e *Entry, r *resource, o *omissions)
}

// omissions collects the fields a bound kept out of an entry.
type omissions struct{ fields []string }

func (o *omissions) add(field string) { o.fields = append(o.fields, field) }

// fileProps lists the properties beyond the fixed nine. The search flags follow the selectable list of
// the Nextcloud search documentation.
var fileProps = []fileProp{
	{xml: "<d:creationdate/>", search: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.CreatedAt = timeText(r.props[propCreationDate])
	}},
	{xml: "<nc:creation_time/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.CreationTime = positive(r.props[propCreationTime])
	}},
	{xml: "<nc:upload_time/>", search: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.UploadTime = positive(r.props[propUploadTime])
	}},
	{xml: "<d:getcontentlanguage/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.ContentLanguage = cleanText(r.props[propContentLanguage], maxShortValue)
	}},
	{xml: "<oc:id/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.GlobalID = cleanText(r.props[propGlobalID], maxShortValue)
	}},
	{xml: "<oc:favorite/>", search: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.Favorite = flag(r.props[propFavorite])
	}},
	{xml: "", search: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.Permissions = cleanText(r.props[propPermissions], maxShortValue)
	}},
	{xml: "<ocs:share-permissions/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.SharePermissions = integer(r.props[propSharePerms], 0)
	}},
	{xml: "<ocm:share-permissions/>", set: func(e *Entry, r *resource, o *omissions) {
		e.OCMSharePermissions = stringArray(r.props[propOCMPerms], fieldOCMPerms, o)
	}},
	{xml: "<oc:owner-id/>", search: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.OwnerID = cleanText(r.props[propOwnerID], maxShortValue)
	}},
	{xml: "<oc:owner-display-name/>", search: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.OwnerDisplayName = cleanText(r.props[propOwnerName], maxShortValue)
	}},
	{xml: "<oc:share-types/>", set: func(e *Entry, r *resource, o *omissions) {
		e.ShareTypes = shareTypes(r, o)
	}},
	{xml: "<nc:sharees/>", set: func(e *Entry, r *resource, o *omissions) {
		e.Sharees = sharees(r, o)
	}},
	{xml: "<nc:note/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.Note = cleanText(r.props[propNote], maxValueLength)
	}},
	{xml: "<nc:share-attributes/>", set: func(e *Entry, r *resource, o *omissions) {
		e.ShareAttributes = freeArray(r.props[propShareAttrs], fieldShareAttr, o)
	}},
	{xml: "<nc:hide-download/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.HideDownload = flag(r.props[propHideDownload])
	}},
	{xml: "<nc:mount-type/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.MountType = cleanText(r.props[propMountType], maxShortValue)
	}},
	{xml: "<nc:is-mount-root/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.IsMountRoot = flag(r.props[propIsMountRoot])
	}},
	{xml: "<nc:is-federated/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.IsFederated = flag(r.props[propIsFederated])
	}},
	{xml: "<nc:is-encrypted/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.IsEncrypted = flag(r.props[propIsEncrypted])
	}},
	{xml: "<oc:comments-count/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.CommentsCount = integer(r.props[propCommentsCount], 0)
	}},
	{xml: "<oc:comments-unread/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.CommentsUnread = integer(r.props[propCommentsUnread], 0)
	}},
	{xml: "<oc:comments-href/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.CommentsHref = cleanText(r.props[propCommentsHref], maxValueLength)
	}},
	{xml: "<oc:tags/>", set: func(e *Entry, r *resource, o *omissions) {
		e.Tags = tagList(r, o)
	}},
	{xml: "<oc:checksums/>", search: true, set: func(e *Entry, r *resource, o *omissions) {
		e.Checksums = checksums(r, o)
	}},
	{xml: "<nc:has-preview/>", search: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.HasPreview = flag(r.props[propHasPreview])
	}},
	{xml: "<nc:hidden/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.Hidden = flag(r.props[propHidden])
	}},
	{xml: "<nc:metadata_etag/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.MetadataETag = cleanText(r.props[propMetadataETag], maxShortValue)
	}},
	// The documentation lists the fingerprint under nc: for the file read and under oc: for the search;
	// both forms are accepted.
	{xml: "<nc:data-fingerprint/><oc:data-fingerprint/>", searchXML: "<oc:data-fingerprint/>", search: true,
		set: func(e *Entry, r *resource, _ *omissions) {
			e.DataFingerprint = cleanText(r.props[propFingerprint], maxShortValue)
		}},
	{xml: "<d:quota-used-bytes/>", folder: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.QuotaUsedBytes = integer(r.props[propQuotaUsed], -1<<62)
	}},
	{xml: "<d:quota-available-bytes/>", folder: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.QuotaAvailableBytes = integer(r.props[propQuotaAvailable], -1<<62)
	}},
	{xml: "<nc:contained-file-count/>", folder: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.ContainedFileCount = integer(r.props[propContainedFiles], 0)
	}},
	{xml: "<nc:contained-folder-count/>", folder: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.ContainedFolderCount = integer(r.props[propContainedDirs], 0)
	}},
	{xml: "<nc:group-folder-id/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.GroupFolderID = integer(r.props[propGroupFolderID], 0)
	}},
	{xml: "<nc:acl-enabled/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.ACLEnabled = flag(r.props[propACLEnabled])
	}},
	{xml: "<nc:acl-can-manage/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.ACLCanManage = flag(r.props[propACLCanManage])
	}},
	{xml: "<nc:acl-list/>", set: func(e *Entry, r *resource, o *omissions) {
		e.ACLList = aclRules(r, treeACLList, fieldACLList, o)
	}},
	{xml: "<nc:inherited-acl-list/>", set: func(e *Entry, r *resource, o *omissions) {
		e.InheritedACLList = aclRules(r, treeInhACL, fieldInhACL, o)
	}},
	{xml: "<nc:lock/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.Locked = flag(r.props[propLock])
	}},
	{xml: "<nc:lock-owner-type/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.LockOwnerType = integer(r.props[propLockOwnerType], 0)
	}},
	{xml: "<nc:lock-owner/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.LockOwner = cleanText(r.props[propLockOwner], maxShortValue)
	}},
	{xml: "<nc:lock-owner-displayname/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.LockOwnerDisplayName = cleanText(r.props[propLockOwnerName], maxShortValue)
	}},
	{xml: "<nc:lock-owner-editor/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.LockOwnerEditor = cleanText(r.props[propLockOwnerEditor], maxShortValue)
	}},
	{xml: "<nc:lock-time/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.LockTime = positive(r.props[propLockTime])
	}},
	{xml: "<nc:lock-timeout/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.LockTimeout = integer(r.props[propLockTimeout], 0)
	}},
	{xml: "<nc:rich-workspace-file/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.RichWorkspaceFile = cleanText(r.props[propRichWorkspaceFile], maxShortValue)
	}},
	{xml: "<nc:reminder-due-date/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.ReminderDueDate = timeText(r.props[propReminderDue])
	}},
	{xml: "<nc:version-label/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.VersionLabel = cleanText(r.props[propVersionLabel], maxShortValue)
	}},
	{xml: "<nc:version-author/>", set: func(e *Entry, r *resource, _ *omissions) {
		e.VersionAuthor = cleanText(r.props[propVersionAuthor], maxShortValue)
	}},
	{xml: "<nc:last_activity/>", search: true, searchOnly: true, set: func(e *Entry, r *resource, _ *omissions) {
		e.LastActivity = cleanText(r.props[propLastActivity], maxShortValue)
	}},
}

func fileSearchXML() string {
	var b strings.Builder
	b.WriteString(entryPropsXML)
	for _, p := range fileProps {
		switch {
		case !p.search:
		case p.searchXML != "":
			b.WriteString(p.searchXML)
		default:
			b.WriteString(p.xml)
		}
	}
	return b.String()
}

func fileReadXML() string {
	var b strings.Builder
	b.WriteString(entryPropsXML)
	for _, p := range fileProps {
		if !p.searchOnly {
			b.WriteString(p.xml)
		}
	}
	return b.String()
}

// The namespaces of the full property set.
const fileNamespaces = `xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns" ` +
	`xmlns:ocs="http://open-collaboration-services.org/ns" xmlns:ocm="http://open-cloud-mesh.org/ns"`

var (
	// filePropfindBody asks for the full entry of a file read; the writing tools and the version list keep
	// the fixed propfindBody.
	filePropfindBody = `<?xml version="1.0" encoding="UTF-8"?><d:propfind ` + fileNamespaces +
		`><d:prop>` + fileReadXML() + `</d:prop></d:propfind>`
	fileFavoritesBody = xmlHeader + `<oc:filter-files ` + fileNamespaces +
		`><oc:filter-rules><oc:favorite>1</oc:favorite></oc:filter-rules><d:prop>` + fileReadXML() +
		`</d:prop></oc:filter-files>`
	fileSearchProps = fileSearchXML()
)

// addFileProps adds the properties beyond the fixed nine to an entry. search selects the reduced set a
// search may report.
func addFileProps(entry *Entry, res *resource, search bool) {
	var om omissions
	for _, p := range fileProps {
		if (search && !p.search) || (!search && p.searchOnly) || (p.folder && entry.Type != typeFolder) {
			continue
		}
		p.set(entry, res, &om)
	}
	entry.OmittedFields = om.fields
}

// cleanText removes control characters and bounds the length at a character boundary. Provider text is
// untrusted and is neither interpreted nor trusted as a form.
func cleanText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || r == utf8.RuneError {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > limit {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// integer reads a whole number of at least min, without a floating point step; anything else is absent.
func integer(raw string, min int64) *int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < min {
		return nil
	}
	return &value
}

// positive reads a Unix time; the zero Nextcloud reports for an unknown time stays absent.
func positive(raw string) int64 {
	if value := integer(raw, 1); value != nil {
		return *value
	}
	return 0
}

func flag(raw string) *bool {
	var value bool
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		value = true
	case "false", "0":
	default:
		return nil
	}
	return &value
}

// timeText normalises a creation date to RFC 3339 in UTC.
func timeText(raw string) string {
	raw = strings.TrimSpace(raw)
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return ""
}

// children returns the nodes of a nested property, or marks the property when a bound dropped it.
func children(r *resource, key, field string, o *omissions) []*propNode {
	if r.over[key] {
		o.add(field)
		return nil
	}
	if root := r.trees[key]; root != nil {
		if len(root.children) > maxListItems {
			o.add(field)
			return nil
		}
		return root.children
	}
	return nil
}

func shareTypes(r *resource, o *omissions) []int64 {
	var out []int64
	for _, n := range children(r, treeShareType, fieldShareType, o) {
		if n.name.Local == "share-type" {
			if value := integer(n.text, 0); value != nil {
				out = append(out, *value)
			}
		}
	}
	return out
}

func tagList(r *resource, o *omissions) []string {
	var out []string
	for _, n := range children(r, treeTags, fieldTags, o) {
		if n.name.Local == "tag" {
			if value := cleanText(n.text, maxValueLength); value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

// checksums reads the oc:checksum elements; Nextcloud writes several algorithms into one element
// separated by spaces, so each part is one entry.
func checksums(r *resource, o *omissions) []string {
	var out []string
	for _, n := range children(r, treeChecksums, fieldChecksums, o) {
		if n.name.Local != "checksum" {
			continue
		}
		for _, part := range strings.Fields(n.text) {
			if value := cleanText(part, maxShortValue); value != "" {
				out = append(out, value)
			}
		}
	}
	if len(out) > maxListItems {
		o.add(fieldChecksums)
		return nil
	}
	return out
}

func sharees(r *resource, o *omissions) []FileSharee {
	var out []FileSharee
	for _, n := range children(r, treeSharees, fieldSharees, o) {
		if n.name.Local != "sharee" {
			continue
		}
		s := FileSharee{
			ID:          cleanText(n.child("id"), maxShortValue),
			DisplayName: cleanText(n.child("display-name"), maxShortValue),
			Type:        integer(n.child("type"), 0),
		}
		if s.ID != "" || s.DisplayName != "" || s.Type != nil {
			out = append(out, s)
		}
	}
	return out
}

// aclRules reads the rules of a group folder ACL list. Each rule is an nc:acl element whose children
// become keys (the local name in snake_case) with their cleaned text. A list over the rule bound, or a
// rule over the member bound, is left out as a whole and named.
func aclRules(r *resource, tree, field string, o *omissions) []map[string]string {
	var out []map[string]string
	for _, n := range children(r, tree, field, o) {
		if n.name.Local != "acl" {
			continue
		}
		if len(n.children) > maxACLMembers {
			o.add(field)
			return nil
		}
		rule := map[string]string{}
		for _, c := range n.children {
			key := strings.ReplaceAll(c.name.Local, "-", "_")
			if _, seen := rule[key]; seen {
				continue
			}
			if value := cleanText(c.text, maxShortValue); value != "" {
				rule[key] = value
			}
		}
		if len(rule) > 0 {
			out = append(out, rule)
		}
	}
	if len(out) > maxACLRules {
		o.add(field)
		return nil
	}
	return out
}

// decodeJSON reads one JSON document without a floating point step and refuses trailing data.
func decodeJSON(raw string) (any, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	if _, err := decoder.Token(); err == nil {
		return nil, false
	}
	return value, true
}

// stringArray reads a JSON array of strings; any deviation leaves the field out and names it.
func stringArray(raw, field string, o *omissions) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, ok := decodeJSON(raw)
	items, isArray := value.([]any)
	if !ok || !isArray || len(items) > maxPermItems {
		o.add(field)
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, isText := item.(string)
		if !isText || len(text) > maxShortValue {
			o.add(field)
			return nil
		}
		if text = cleanText(text, maxShortValue); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// freeArray reads a free JSON array bounded in depth, node count, and string length. Over a bound the
// whole value is left out and named, never cut.
func freeArray(raw, field string, o *omissions) []any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, ok := decodeJSON(raw)
	nodes := 0
	cleaned, within := boundJSON(value, 1, &nodes)
	items, isArray := cleaned.([]any)
	if !ok || !within || !isArray {
		o.add(field)
		return nil
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func boundJSON(value any, depth int, nodes *int) (any, bool) {
	*nodes++
	if depth > maxJSONDepth || *nodes > maxJSONNodes {
		return nil, false
	}
	switch v := value.(type) {
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			clean, ok := boundJSON(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out = append(out, clean)
		}
		return out, true
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if len(key) > maxJSONString {
				return nil, false
			}
			clean, ok := boundJSON(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out[cleanText(key, maxJSONString)] = clean
		}
		return out, true
	case string:
		if len(v) > maxJSONString {
			return nil, false
		}
		return cleanText(v, maxJSONString), true
	default:
		return v, true
	}
}

// FileSharee is one recipient of a share as Nextcloud lists it for a node.
type FileSharee struct {
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Type        *int64 `json:"type,omitempty"`
}

// fileEntryOf is entryOf plus the file properties; search reduces them to the set a search may report.
func (c *Client) fileEntryOf(relative []string, res *resource, search bool) *Entry {
	entry := c.entryOf(relative, res)
	addFileProps(entry, res, search)
	return entry
}

// aclSchema is one ACL rule list: objects of text values keyed by the child element names.
const aclSchema = `{"type":"array","items":{"type":"object","additionalProperties":{"type":"string"}}}`

// entryExtraSchema is the schema of the properties beyond the fixed nine; it ends the property list of
// entrySchema.
const entryExtraSchema = `"created_at":{"type":"string"},"creation_time":{"type":"integer"},` +
	`"upload_time":{"type":"integer"},"content_language":{"type":"string"},"global_id":{"type":"string"},` +
	`"favorite":{"type":"boolean"},"permissions":{"type":"string"},"share_permissions":{"type":"integer"},` +
	`"ocm_share_permissions":{"type":"array","items":{"type":"string"}},` +
	`"owner_id":{"type":"string"},"owner_display_name":{"type":"string"},` +
	`"share_types":{"type":"array","items":{"type":"integer"}},` +
	`"sharees":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
	`"display_name":{"type":"string"},"type":{"type":"integer"}},"additionalProperties":false}},` +
	`"note":{"type":"string"},"share_attributes":{"type":"array","items":{}},` +
	`"hide_download":{"type":"boolean"},"mount_type":{"type":"string"},"is_mount_root":{"type":"boolean"},` +
	`"is_federated":{"type":"boolean"},"is_encrypted":{"type":"boolean"},` +
	`"comments_count":{"type":"integer"},"comments_unread":{"type":"integer"},"comments_href":{"type":"string"},` +
	`"tags":{"type":"array","items":{"type":"string"}},"checksums":{"type":"array","items":{"type":"string"}},` +
	`"has_preview":{"type":"boolean"},"hidden":{"type":"boolean"},"metadata_etag":{"type":"string"},` +
	`"data_fingerprint":{"type":"string"},"quota_used_bytes":{"type":"integer"},` +
	`"quota_available_bytes":{"type":"integer"},"contained_file_count":{"type":"integer"},` +
	`"contained_folder_count":{"type":"integer"},"last_activity":{"type":"string"},` +
	`"group_folder_id":{"type":"integer"},"acl_enabled":{"type":"boolean"},"acl_can_manage":{"type":"boolean"},` +
	`"acl_list":` + aclSchema + `,"inherited_acl_list":` + aclSchema + `,` +
	`"locked":{"type":"boolean"},"lock_owner_type":{"type":"integer"},"lock_owner":{"type":"string"},` +
	`"lock_owner_display_name":{"type":"string"},"lock_owner_editor":{"type":"string"},` +
	`"lock_time":{"type":"integer"},"lock_timeout":{"type":"integer"},"rich_workspace_file":{"type":"string"},` +
	`"reminder_due_date":{"type":"string"},"version_label":{"type":"string"},"version_author":{"type":"string"},` +
	`"omitted_fields":{"type":"array","items":{"type":"string"}}`

// entryExtraFields describes the properties beyond the fixed nine.
var entryExtraFields = []capability.Field{
	{Name: "created_at", Description: "Creation time, normalised to RFC 3339 in UTC"},
	{Name: "creation_time", Description: "Creation time in Unix seconds, absent when unknown"},
	{Name: "upload_time", Description: "Upload time in Unix seconds, absent when unknown"},
	{Name: "content_language", Description: "Content language of the file, untrusted data"},
	{Name: "global_id", Description: "Instance-wide file identifier of Nextcloud, untrusted data"},
	{Name: "favorite", Description: "True when the identity marked the node as favorite"},
	{Name: "permissions", Description: "Effective permission letters of this identity, untrusted data"},
	{Name: "share_permissions", Description: "Share permission bitmask of the node"},
	{Name: "ocm_share_permissions", Description: "Open Cloud Mesh share permissions, untrusted data"},
	{Name: "owner_id", Description: "User ID of the owner, untrusted data"},
	{Name: "owner_display_name", Description: "Display name of the owner, untrusted data"},
	{Name: "share_types", Description: "Share type numbers of the shares on the node"},
	{Name: "sharees", Description: "Recipients of the shares on the node with id, display_name and share type number, untrusted data"},
	{Name: "note", Description: "Note of a share on the node, untrusted data"},
	{Name: "share_attributes", Description: "Share attributes as Nextcloud reports them, a bounded free JSON array, untrusted data"},
	{Name: "hide_download", Description: "True when the share hides the download"},
	{Name: "mount_type", Description: "Kind of the mount the node lies on, absent for local storage, untrusted data"},
	{Name: "is_mount_root", Description: "True when the node is the root of a mount"},
	{Name: "is_federated", Description: "True when the node lies on a federated share"},
	{Name: "is_encrypted", Description: "True when the node is end-to-end encrypted"},
	{Name: "comments_count", Description: "Number of comments on the node"},
	{Name: "comments_unread", Description: "Number of unread comments on the node"},
	{Name: "comments_href", Description: "Location of the comments of the node as Nextcloud reports it, untrusted data that is never fetched"},
	{Name: "tags", Description: "System tag names of the node, untrusted data"},
	{Name: "checksums", Description: "Checksums such as SHA1:<hex> the server keeps for the file, untrusted data"},
	{Name: "has_preview", Description: "True when Nextcloud can render a preview"},
	{Name: "hidden", Description: "True when the node is hidden"},
	{Name: "metadata_etag", Description: "Entity tag of the metadata of the node"},
	{Name: "data_fingerprint", Description: "Data fingerprint of the instance, untrusted data"},
	{Name: "quota_used_bytes", Description: "Folders only: bytes used; negative special values of Nextcloud stay numbers"},
	{Name: "quota_available_bytes", Description: "Folders only: bytes available; negative special values of Nextcloud stay numbers"},
	{Name: "contained_file_count", Description: "Folders only: number of files in the subtree"},
	{Name: "contained_folder_count", Description: "Folders only: number of folders in the subtree"},
	{Name: "last_activity", Description: "Search only: last activity of the node as Nextcloud reports it, untrusted data"},
	{Name: "group_folder_id", Description: "Group folder identifier of the node, absent outside a group folder"},
	{Name: "acl_enabled", Description: "True when advanced permissions (ACL) are enabled on the group folder"},
	{Name: "acl_can_manage", Description: "True when the identity may manage the ACL rules of the group folder"},
	{Name: "acl_list", Description: "ACL rules of the node, each an object of the rule elements such as acl_mapping_type, acl_mapping_id, acl_mapping_display_name, acl_mask and acl_permissions; at most 50 rules of 10 members, otherwise left out; untrusted data"},
	{Name: "inherited_acl_list", Description: "ACL rules the node inherits, in the form of acl_list; untrusted data"},
	{Name: "locked", Description: "True when the file is locked"},
	{Name: "lock_owner_type", Description: "Kind of the lock owner: 0 user, 1 app such as Office or Text, 2 WebDAV"},
	{Name: "lock_owner", Description: "User ID or app that holds the lock, untrusted data"},
	{Name: "lock_owner_display_name", Description: "Display name of the lock owner, untrusted data"},
	{Name: "lock_owner_editor", Description: "Editor or app through which the lock was taken, untrusted data"},
	{Name: "lock_time", Description: "Lock time in Unix seconds, absent when unknown"},
	{Name: "lock_timeout", Description: "Lock lifetime in seconds; 0 means no expiry"},
	{Name: "rich_workspace_file", Description: "File ID of the workspace file of the folder; its text is never read"},
	{Name: "reminder_due_date", Description: "Due date of the reminder of the identity, normalised to RFC 3339 in UTC"},
	{Name: "version_label", Description: "Label of the file version, untrusted data"},
	{Name: "version_author", Description: "User ID of the author of the file version, untrusted data"},
	{Name: "omitted_fields", Description: "Fields left out because their value exceeded a bound"},
}
